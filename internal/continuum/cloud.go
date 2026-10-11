package continuum

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/aws/smithy-go"
	"github.com/ayush/slate/internal/clusterstate"
)

type LambdaInvokeAPI interface {
	Invoke(context.Context, *lambda.InvokeInput, ...func(*lambda.Options)) (*lambda.InvokeOutput, error)
}

type SFNStartAPI interface {
	StartExecution(context.Context, *sfn.StartExecutionInput, ...func(*sfn.Options)) (*sfn.StartExecutionOutput, error)
}

type EC2ControlAPI interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	StopInstances(context.Context, *ec2.StopInstancesInput, ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
	StartInstances(context.Context, *ec2.StartInstancesInput, ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
}

type CloudConfig struct {
	Region                  string
	TableName               string
	AdminToken              string
	RuntimeEnabled          bool
	VerifierFunction        string
	MonitorFunction         string
	StateMachineARN         string
	FailbackStateMachineARN string
	ReconcilerFunction      string
	PublicAPIURL            string
	InstanceIDA             string
	InstanceIDB             string
	BackendAURL             string
	BackendBURL             string
	PolicyProbes            int
	ProbeIntervalMillis     int
	ProbeTimeout            time.Duration
	LockLease               time.Duration
}

type CloudControl struct {
	Config  CloudConfig
	Control *DDBControl
	Events  EventStore
	Lambda  LambdaInvokeAPI
	SFN     SFNStartAPI
	EC2     EC2ControlAPI
	HTTP    HTTPDoer
	Now     func() time.Time
}

func NewCloudControl(cfg CloudConfig, control *DDBControl, events EventStore, lambdaClient LambdaInvokeAPI, sfnClient SFNStartAPI, ec2Client EC2ControlAPI) *CloudControl {
	return &CloudControl{Config: cfg, Control: control, Events: events, Lambda: lambdaClient, SFN: sfnClient, EC2: ec2Client,
		HTTP: &http.Client{Timeout: 5 * time.Second}, Now: time.Now}
}

// HandleHTTPAPI translates the API Gateway HTTP API v2 envelope to the
// faculty-facing status/action contract.
func (c *CloudControl) HandleHTTPAPI(ctx context.Context, event events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	method := strings.ToUpper(event.RequestContext.HTTP.Method)
	path := event.RawPath
	if path == "" {
		path = event.RequestContext.HTTP.Path
	}
	path = stripAPIPrefix(path)
	token := headerValue(event.Headers, "x-admin-token")
	if !constantToken(token, c.Config.AdminToken) {
		return gatewayResponse(http.StatusForbidden, map[string]any{"detail": "invalid admin token"}), nil
	}
	switch {
	case method == http.MethodGet && path == "/continuum/status":
		status, err := c.Status(ctx)
		if err != nil {
			return gatewayResponse(http.StatusServiceUnavailable, map[string]any{"detail": "continuum status unavailable"}), nil
		}
		return gatewayResponse(http.StatusOK, statusMap(status)), nil
	case method == http.MethodPost && path == "/continuum/action":
		body, err := EncodeBody(event.Body, event.IsBase64Encoded, 16*1024)
		if err != nil {
			return gatewayResponse(http.StatusRequestEntityTooLarge, map[string]any{"detail": "request body too large"}), nil
		}
		var action ActionRequest
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&action); err != nil {
			return gatewayResponse(http.StatusBadRequest, map[string]any{"detail": "invalid action payload"}), nil
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return gatewayResponse(http.StatusBadRequest, map[string]any{"detail": "action payload must contain one JSON object"}), nil
		}
		response, err := c.Action(ctx, action)
		if err != nil {
			return gatewayResponse(http.StatusBadRequest, map[string]any{"detail": brief(err.Error())}), nil
		}
		return gatewayResponse(http.StatusOK, response), nil
	default:
		return gatewayResponse(http.StatusNotFound, map[string]any{"detail": "continuum control route not found"}), nil
	}
}

func (c *CloudControl) Status(ctx context.Context) (Status, error) {
	state, err := c.Control.Get(ctx)
	if err != nil {
		return Status{}, err
	}
	probe, err := c.invoke(ctx, c.Config.VerifierFunction, map[string]any{"action": "status"})
	if err != nil {
		return Status{}, err
	}
	backends := decodeBackendStatuses(probe["backends"])
	events := []Event{}
	metrics := Metrics{}
	if c.Events != nil {
		events, err = c.Events.Recent(ctx, 50)
		if err != nil {
			return Status{}, err
		}
		metrics = MetricsFromEvents(events)
	}
	return Status{Mode: "aws", Region: c.Config.Region, ActiveBackend: state.ActiveBackend, Epoch: state.Epoch, Paused: state.Paused,
		Policy:   Policy{Version: PolicyVersion, ConfirmationProbes: c.probes(), ProbeIntervalMillis: c.intervalMillis()},
		Backends: backends, Events: events, Metrics: metrics}, nil
}

func (c *CloudControl) Action(ctx context.Context, request ActionRequest) (map[string]any, error) {
	request.Action = strings.ToLower(strings.TrimSpace(request.Action))
	request.Backend = strings.ToUpper(strings.TrimSpace(request.Backend))
	if !allowedActions[request.Action] {
		return nil, fmt.Errorf("unsupported continuum action %q", request.Action)
	}
	state, err := c.Control.Get(ctx)
	if err != nil {
		return nil, err
	}
	backend := request.Backend
	if backend == "" {
		backend = state.ActiveBackend
		if request.Action == "unsafe_standby" {
			backend = otherBackend(state.ActiveBackend)
		}
	}
	if needsBackend(request.Action) && !validBackend(backend) {
		return nil, ErrInvalidBackend
	}
	switch request.Action {
	case "crash":
		if err := c.stopInstance(ctx, backend); err != nil {
			return nil, err
		}
		c.appendEvent(ctx, Event{Type: "fault", Message: "EC2 instance stop requested", Backend: backend, Epoch: state.Epoch})
	case "partial", "blip", "unsafe_standby", "lost_response":
		fault := map[string]any{"action": "inject_fault", "fault": request.Action, "backend": backend}
		var blipDuration time.Duration
		if request.Action == "blip" {
			blipDuration, err = c.blipDuration()
			if err != nil {
				return nil, err
			}
			fault["duration_ms"] = blipDuration.Milliseconds()
		}
		_, err := c.invoke(ctx, c.Config.VerifierFunction, fault)
		if err != nil {
			return nil, err
		}
		c.appendEvent(ctx, Event{Type: "fault", Message: request.Action + " fault injected", Backend: backend, Epoch: state.Epoch})
		if request.Action == "blip" {
			started := c.startMonitor(ctx, "blip")
			return map[string]any{"accepted": true, "fault_expires_in_ms": blipDuration.Milliseconds(), "monitor_started": started}, nil
		}
	case "reset":
		if !c.Config.RuntimeEnabled {
			return nil, errors.New("reset is disabled while the demo runtime is paused; run make demo-resume first")
		}
		for _, id := range []string{"A", "B"} {
			if err := c.startInstance(ctx, id); err != nil {
				return nil, err
			}
		}
		readyToClear := true
		for _, id := range []string{"A", "B"} {
			instanceState, err := c.instanceState(ctx, c.instanceID(id))
			if err != nil {
				return nil, err
			}
			if instanceState != "running" {
				readyToClear = false
			}
		}
		if !readyToClear {
			c.appendEvent(ctx, Event{Type: "reset", Message: "backend instances are starting; repeat reset after health checks recover", Backend: state.ActiveBackend, Epoch: state.Epoch})
			return map[string]any{"accepted": true, "pending": true, "message": "backend instances are starting; repeat reset after health checks recover"}, nil
		}
		if _, err := c.invoke(ctx, c.Config.VerifierFunction, map[string]any{"action": "clear_faults"}); err != nil {
			return map[string]any{"accepted": true, "pending": true, "message": "backend processes are not ready yet; repeat reset after health checks recover"}, nil
		}
		c.appendEvent(ctx, Event{Type: "reset", Message: "faults cleared and both backend instances are running", Backend: state.ActiveBackend, Epoch: state.Epoch})
		if state.ActiveBackend == "B" {
			return c.startWorkflow(ctx, "failback", state, "A")
		}
	case "verify":
		if !c.startMonitor(ctx, "operator") {
			return nil, errors.New("MONITOR_FUNCTION_NAME is required and monitor invocation failed")
		}
		return map[string]any{"accepted": true, "monitor_started": true}, nil
	case "failback":
		return c.startWorkflow(ctx, "failback", state, "A")
	case "reconcile":
		return c.startWorkflow(ctx, "reconcile", state, state.ActiveBackend)
	}
	status, err := c.Status(ctx)
	if err != nil {
		return nil, err
	}
	return statusMap(status), nil
}

// Dispatch is used by Step Functions direct Lambda tasks. It has no HTTP auth
// branch; Step Functions IAM permission is the authorization boundary.
func (c *CloudControl) Dispatch(ctx context.Context, event map[string]any) (map[string]any, error) {
	action := stringValue(event, "action")
	owner := stringValue(event, "incident_id")
	switch action {
	case "acquire_lock":
		leaseSeconds := intValue(event, "lease_seconds", int(c.Config.LockLease.Seconds()))
		if leaseSeconds <= 0 {
			leaseSeconds = 900
		}
		acquired, err := c.Control.AcquireIncident(ctx, owner, time.Duration(leaseSeconds)*time.Second)
		return map[string]any{"lock_acquired": acquired, "incident_id": owner}, err
	case "release_lock":
		return map[string]any{"released": true}, c.Control.ReleaseIncident(ctx, owner)
	case "route", "failback", "rollback":
		current, err := c.Control.Get(ctx)
		if err != nil {
			return nil, err
		}
		expectedBackend := stringValue(event, "expected_backend")
		if expectedBackend == "" {
			expectedBackend = stringValue(event, "active_backend")
		}
		expectedEpoch := int64Value(event, "expected_epoch", current.Epoch)
		target := stringValue(event, "target_backend")
		updated, err := c.Control.Promote(ctx, clusterstate.Writer{Backend: expectedBackend, Epoch: expectedEpoch}, target, owner)
		if err != nil {
			return nil, err
		}
		eventType := "failover"
		if action == "failback" {
			eventType = "failback"
		} else if action == "rollback" {
			eventType = "rollback"
		}
		c.appendEvent(ctx, Event{Type: eventType, Message: "active route updated after conditional epoch check", Backend: target, Epoch: updated.Epoch})
		return map[string]any{"active_backend": updated.ActiveBackend, "epoch": updated.Epoch, "paused": updated.Paused}, nil
	case "reconcile":
		if c.Config.ReconcilerFunction == "" {
			return nil, errors.New("RECONCILER_FUNCTION_NAME is required")
		}
		payload := map[string]any{"action": "reconcile", "incident_id": owner}
		if cursor, exists := event["cursor"]; exists && cursor != nil {
			payload["cursor"] = cursor
		}
		result, err := c.invoke(ctx, c.Config.ReconcilerFunction, payload)
		if err != nil {
			return nil, err
		}
		return result, nil
	case "reconcile_completed", "reconcile_failed":
		current, err := c.Control.Get(ctx)
		if err != nil {
			return nil, err
		}
		summary, _ := event["summary"].(map[string]any)
		eventType := action
		if action == "reconcile_completed" {
			if int64Value(summary, "conflict_count", 0) > 0 {
				eventType = "reconcile_failed"
			} else {
				eventType = "reconcile"
			}
		}
		if err := c.appendEventRequired(ctx, Event{Type: eventType, Message: reconciliationMessage(summary), Backend: current.ActiveBackend, Epoch: current.Epoch}); err != nil {
			return nil, fmt.Errorf("persist reconciliation event: %w", err)
		}
		return map[string]any{"recorded": true, "type": eventType}, nil
	case "public_smoke":
		return c.publicSmoke(ctx, stringValue(event, "public_api_url"), int64Value(event, "incident_started_at_ms", 0))
	case "verify":
		return c.verifyIncident(ctx, event)
	default:
		return nil, fmt.Errorf("unknown Step Functions action %q", action)
	}
}

func (c *CloudControl) startWorkflow(ctx context.Context, operation string, state ControlState, target string) (map[string]any, error) {
	incident := fmt.Sprintf("manual-%s-e%d-%d", operation, state.Epoch, c.timeNow().Unix()/30)
	if operation == "reconcile" {
		// Reconciliation is explicitly repeatable; do not collapse distinct
		// operator requests into the same Step Functions execution name.
		incident = fmt.Sprintf("manual-reconcile-e%d-%s", state.Epoch, randomIDString())
	}
	return c.startWorkflowWithID(ctx, operation, state, target, incident, c.timeNow())
}

func (c *CloudControl) startWorkflowWithID(ctx context.Context, operation string, state ControlState, target, incident string, incidentStarted time.Time) (map[string]any, error) {
	if c.SFN == nil {
		return nil, errors.New("Step Functions client is unavailable")
	}
	arn := c.Config.StateMachineARN
	if operation == "failback" && c.Config.FailbackStateMachineARN != "" {
		arn = c.Config.FailbackStateMachineARN
	}
	if arn == "" {
		return nil, errors.New("STATE_MACHINE_ARN is required")
	}
	input, _ := json.Marshal(map[string]any{"operation": operation, "incident_id": incident,
		"active_backend": state.ActiveBackend, "expected_backend": state.ActiveBackend,
		"target_backend": target, "expected_epoch": state.Epoch,
		"region": c.Config.Region, "public_api_url": c.Config.PublicAPIURL,
		"incident_started_at_ms": incidentStarted.UnixMilli()})
	name := strings.ReplaceAll(strings.ReplaceAll(incident, "_", "-"), ".", "-")
	_, err := c.SFN.StartExecution(ctx, &sfn.StartExecutionInput{StateMachineArn: aws.String(arn), Name: aws.String(name), Input: aws.String(string(input))})
	if err != nil {
		var apiErr smithy.APIError
		if errors.As(err, &apiErr) && apiErr.ErrorCode() == "ExecutionAlreadyExists" {
			return map[string]any{"accepted": true, "duplicate": true, "execution_name": name}, nil
		}
		return nil, err
	}
	c.appendEvent(ctx, Event{Type: "workflow", Message: operation + " workflow started", Backend: target, Epoch: state.Epoch})
	return map[string]any{"accepted": true, "execution_name": name}, nil
}

// MonitorIncident performs the configured confirmation probes in one bounded
// Lambda invocation. Shared dependency failures suppress promotion; only a
// primary-specific failure with a healthy standby starts Step Functions.
func (c *CloudControl) MonitorIncident(ctx context.Context) (map[string]any, error) {
	initial, err := c.Control.Get(ctx)
	if err != nil {
		return nil, err
	}
	if initial.Paused {
		return map[string]any{"confirmed": false, "reason": "control plane is paused"}, nil
	}
	// Include the initial probe in this controller window. Scheduling delay
	// before this monitor cycle is measured separately by the rehearsal.
	incidentStarted := c.timeNow()
	standbyID := otherBackend(initial.ActiveBackend)
	for sample := 0; sample < c.probes(); sample++ {
		current, err := c.Control.Get(ctx)
		if err != nil {
			return nil, err
		}
		if current.ActiveBackend != initial.ActiveBackend || current.Epoch != initial.Epoch || current.Paused {
			return map[string]any{"confirmed": false, "reason": "route changed during confirmation"}, nil
		}
		probe, err := c.invoke(ctx, c.Config.VerifierFunction, map[string]any{"action": "status"})
		if err != nil {
			return nil, err
		}
		backends := decodeBackendStatuses(probe["backends"])
		active := backendStatusByID(backends, initial.ActiveBackend)
		standby := backendStatusByID(backends, standbyID)
		if active.Healthy {
			if sample > 0 {
				c.appendEvent(ctx, Event{Type: "suppressed", Message: "active backend recovered before confirmation completed", Backend: initial.ActiveBackend, Epoch: initial.Epoch})
			}
			return map[string]any{"confirmed": false, "reason": "active backend recovered", "backends": backends}, nil
		}
		if !standby.Healthy {
			reason := "both backends failed readiness; shared dependency or verifier failure suspected"
			c.appendEvent(ctx, Event{Type: "suppressed", Message: reason, Backend: initial.ActiveBackend, Epoch: initial.Epoch})
			return map[string]any{"confirmed": false, "reason": reason, "backends": backends}, nil
		}
		if sample+1 < c.probes() {
			if err := wait(ctx, time.Duration(c.intervalMillis())*time.Millisecond); err != nil {
				return nil, err
			}
		}
	}
	incident := fmt.Sprintf("auto-%s-e%d-%d", initial.ActiveBackend, initial.Epoch, c.timeNow().Unix()/60)
	started, err := c.startWorkflowWithID(ctx, "route", initial, standbyID, incident, incidentStarted)
	if err != nil {
		return nil, err
	}
	started["confirmed"] = true
	started["incident_id"] = incident
	return started, nil
}

func (c *CloudControl) verifyIncident(ctx context.Context, event map[string]any) (map[string]any, error) {
	expectedBackend := stringValue(event, "expected_backend")
	target := stringValue(event, "target_backend")
	expectedEpoch := int64Value(event, "expected_epoch", 0)
	operation := stringValue(event, "operation")
	if !validBackend(expectedBackend) || !validBackend(target) || expectedEpoch < 1 {
		return nil, errors.New("verify requires expected_backend, expected_epoch, and target_backend")
	}
	verifierFunction := stringValue(event, "verifier_function")
	if verifierFunction == "" {
		verifierFunction = c.Config.VerifierFunction
	}
	if verifierFunction == "" {
		return nil, errors.New("verifier Lambda is not configured")
	}
	probeCount := c.probes()
	if operation == "route" || operation == "rollback" {
		// Automatic routing has already passed the confirmation window in the
		// monitor. Recheck both endpoints once while holding the workflow lock.
		probeCount = 1
	}
	for sample := 0; sample < probeCount; sample++ {
		state, err := c.Control.Get(ctx)
		if err != nil {
			return nil, err
		}
		if state.ActiveBackend != expectedBackend || state.Epoch != expectedEpoch || state.Paused {
			return map[string]any{"confirmed": false, "reason": "route pointer changed or is paused"}, nil
		}
		probe, err := c.invoke(ctx, verifierFunction, map[string]any{"action": "status"})
		if err != nil {
			return nil, err
		}
		backends := decodeBackendStatuses(probe["backends"])
		active := backendStatusByID(backends, expectedBackend)
		candidate := backendStatusByID(backends, target)
		if !candidate.Healthy {
			return map[string]any{"confirmed": false, "reason": "target backend failed readiness", "backends": backends}, nil
		}
		if operation != "failback" && operation != "rollback" && active.Healthy {
			return map[string]any{"confirmed": false, "reason": "active backend recovered", "backends": backends}, nil
		}
		if sample+1 < probeCount {
			if err := wait(ctx, time.Duration(c.intervalMillis())*time.Millisecond); err != nil {
				return nil, err
			}
		}
	}
	return map[string]any{"confirmed": true, "reason": "required readiness probes passed"}, nil
}

func backendStatusByID(backends []BackendStatus, id string) BackendStatus {
	for _, backend := range backends {
		if backend.ID == id {
			return backend
		}
	}
	return BackendStatus{ID: id, Reason: "backend status missing from verifier response"}
}

func reconciliationMessage(summary map[string]any) string {
	if summary == nil {
		return "reconciliation completed without a summary"
	}
	return fmt.Sprintf("reconciliation scanned=%d processed=%d already_processed=%d conflicts=%d",
		int64Value(summary, "scanned", 0), int64Value(summary, "processed", 0),
		int64Value(summary, "already_processed", 0), int64Value(summary, "conflict_count", 0))
}

func (c *CloudControl) publicSmoke(ctx context.Context, override string, incidentStartedAtMS int64) (map[string]any, error) {
	started := c.timeNow()
	base := strings.TrimRight(strings.TrimSpace(override), "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(c.Config.PublicAPIURL), "/")
	}
	if base == "" {
		return nil, errors.New("PUBLIC_API_URL is required for public smoke verification")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, errors.New("PUBLIC_API_URL must be an absolute HTTP(S) URL")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health/deep", nil)
	if err != nil {
		return nil, err
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return map[string]any{"healthy": false, "reason": brief(err.Error())}, nil
	}
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&body); err != nil {
		return map[string]any{"healthy": false, "reason": brief(err.Error()), "http_status": response.StatusCode}, nil
	}
	healthy, _ := body["healthy"].(bool)
	ok := response.StatusCode == http.StatusOK && healthy
	durationMS := c.timeNow().Sub(started).Milliseconds()
	if incidentStartedAtMS > 0 {
		if duration := c.timeNow().Sub(time.UnixMilli(incidentStartedAtMS)).Milliseconds(); duration >= 0 {
			durationMS = duration
		}
	}
	result := map[string]any{"healthy": ok, "http_status": response.StatusCode, "body": body, "duration_ms": durationMS}
	if ok {
		if state, err := c.Control.Get(ctx); err == nil {
			c.appendEvent(ctx, Event{Type: "recovered", Message: fmt.Sprintf("route recovery took %d ms from monitor check cycle start; public health smoke confirmed recovery", durationMS),
				Backend: state.ActiveBackend, Epoch: state.Epoch, DurationMS: durationMS})
		}
	}
	return result, nil
}

func (c *CloudControl) invoke(ctx context.Context, function string, payload map[string]any) (map[string]any, error) {
	if function == "" || c.Lambda == nil {
		return nil, errors.New("verifier Lambda is not configured")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	output, err := c.Lambda.Invoke(ctx, &lambda.InvokeInput{FunctionName: aws.String(function), InvocationType: lambdatypes.InvocationTypeRequestResponse, Payload: body})
	if err != nil {
		return nil, err
	}
	if output.FunctionError != nil && *output.FunctionError != "" {
		return nil, fmt.Errorf("Lambda %s failed: %s", function, string(output.Payload))
	}
	var result map[string]any
	if err := json.Unmarshal(output.Payload, &result); err != nil {
		return nil, fmt.Errorf("decode Lambda %s response: %w", function, err)
	}
	return result, nil
}

func (c *CloudControl) stopInstance(ctx context.Context, backend string) error {
	id := c.instanceID(backend)
	if id == "" || c.EC2 == nil {
		return fmt.Errorf("EC2 instance id for backend %s is not configured", backend)
	}
	state, err := c.instanceState(ctx, id)
	if err != nil {
		return err
	}
	if state == "stopped" || state == "stopping" {
		return nil
	}
	_, err = c.EC2.StopInstances(ctx, &ec2.StopInstancesInput{InstanceIds: []string{id}})
	return err
}

func (c *CloudControl) startInstance(ctx context.Context, backend string) error {
	id := c.instanceID(backend)
	if id == "" || c.EC2 == nil {
		return fmt.Errorf("EC2 instance id for backend %s is not configured", backend)
	}
	state, err := c.instanceState(ctx, id)
	if err != nil {
		return err
	}
	if state == "running" || state == "pending" {
		return nil
	}
	if state != "stopped" {
		return fmt.Errorf("cannot start backend %s instance in state %q", backend, state)
	}
	_, err = c.EC2.StartInstances(ctx, &ec2.StartInstancesInput{InstanceIds: []string{id}})
	return err
}

func (c *CloudControl) instanceState(ctx context.Context, id string) (string, error) {
	out, err := c.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{InstanceIds: []string{id}})
	if err != nil {
		return "", err
	}
	for _, reservation := range out.Reservations {
		for _, instance := range reservation.Instances {
			if instance.InstanceId != nil && *instance.InstanceId == id && instance.State != nil {
				return string(instance.State.Name), nil
			}
		}
	}
	return "", fmt.Errorf("EC2 instance %s was not found", id)
}

func (c *CloudControl) instanceID(backend string) string {
	if backend == "A" {
		return c.Config.InstanceIDA
	}
	return c.Config.InstanceIDB
}

func (c *CloudControl) appendEvent(ctx context.Context, event Event) {
	if c.Events == nil {
		return
	}
	if err := c.appendEventRequired(ctx, event); err != nil {
		// Audit storage is auxiliary to routing: a successful route change must
		// remain successful if a best-effort event write cannot be saved.
		log.Printf("continuum audit event write failed: %v", err)
	}
}

func (c *CloudControl) appendEventRequired(ctx context.Context, event Event) error {
	if c.Events == nil {
		return errors.New("continuum event store is not configured")
	}
	event.ID = "event-" + randomIDString()
	event.At = c.timeNow().UTC().Format(time.RFC3339Nano)
	if err := c.Events.Append(ctx, event); err != nil {
		return err
	}
	return nil
}

func (c *CloudControl) timeNow() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *CloudControl) probes() int {
	if c.Config.PolicyProbes > 0 && c.Config.PolicyProbes <= 10 {
		return c.Config.PolicyProbes
	}
	return 3
}

func (c *CloudControl) blipDuration() (time.Duration, error) {
	probes := c.probes()
	if probes < 2 {
		return 0, errors.New("blip requires at least two confirmation probes")
	}
	interval := time.Duration(c.intervalMillis()) * time.Millisecond
	confirmationWindow := time.Duration(probes-1) * interval
	// Expire after the first probe can observe the transient, but strictly
	// before the final confirmation probe. The backend clears this lazily on
	// request access, so a cold start may miss the transient entirely.
	duration := interval + interval/2
	if duration >= confirmationWindow {
		duration = confirmationWindow - time.Millisecond
	}
	if duration > 5*time.Minute {
		duration = 5 * time.Minute
	}
	if duration < time.Millisecond {
		return 0, errors.New("blip confirmation window is too short")
	}
	return duration, nil
}

func (c *CloudControl) startMonitor(ctx context.Context, source string) bool {
	if c.Config.MonitorFunction == "" || c.Lambda == nil {
		return false
	}
	payload, _ := json.Marshal(map[string]any{"source": source})
	_, err := c.Lambda.Invoke(ctx, &lambda.InvokeInput{FunctionName: aws.String(c.Config.MonitorFunction), InvocationType: lambdatypes.InvocationTypeEvent, Payload: payload})
	if err != nil {
		log.Printf("continuum monitor invocation failed (source=%s): %v", source, err)
		return false
	}
	return true
}

func (c *CloudControl) intervalMillis() int {
	if c.Config.ProbeIntervalMillis >= 100 && c.Config.ProbeIntervalMillis <= 300000 {
		return c.Config.ProbeIntervalMillis
	}
	return 5000
}

func decodeBackendStatuses(value any) []BackendStatus {
	if value == nil {
		return []BackendStatus{{ID: "A", Reason: "not probed"}, {ID: "B", Reason: "not probed"}}
	}
	body, err := json.Marshal(value)
	if err != nil {
		return []BackendStatus{{ID: "A", Reason: "invalid probe response"}, {ID: "B", Reason: "invalid probe response"}}
	}
	var result []BackendStatus
	if err := json.Unmarshal(body, &result); err != nil || len(result) == 0 {
		return []BackendStatus{{ID: "A", Reason: "invalid probe response"}, {ID: "B", Reason: "invalid probe response"}}
	}
	return result
}

func statusMap(status Status) map[string]any {
	body, _ := json.Marshal(status)
	var result map[string]any
	_ = json.Unmarshal(body, &result)
	return result
}

func gatewayResponse(status int, payload map[string]any) events.APIGatewayV2HTTPResponse {
	body, _ := json.Marshal(payload)
	return events.APIGatewayV2HTTPResponse{StatusCode: status, Headers: map[string]string{"content-type": "application/json; charset=utf-8"}, Body: string(body)}
}

func stripAPIPrefix(path string) string {
	if strings.HasPrefix(path, "/api/") {
		return strings.TrimPrefix(path, "/api")
	}
	if path == "/api" {
		return "/"
	}
	return path
}

func headerValue(headers map[string]string, key string) string {
	for candidate, value := range headers {
		if strings.EqualFold(candidate, key) {
			return value
		}
	}
	return ""
}

func constantToken(got, expected string) bool {
	if got == "" || expected == "" || len(got) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(expected)) == 1
}

func needsBackend(action string) bool {
	return action == "crash" || action == "partial" || action == "blip" || action == "unsafe_standby" || action == "lost_response"
}

func stringValue(event map[string]any, key string) string {
	value, _ := event[key].(string)
	return value
}

func intValue(event map[string]any, key string, fallback int) int {
	return int(int64Value(event, key, int64(fallback)))
}

func int64Value(event map[string]any, key string, fallback int64) int64 {
	switch value := event[key].(type) {
	case float64:
		return int64(value)
	case json.Number:
		if parsed, err := strconv.ParseInt(string(value), 10, 64); err == nil {
			return parsed
		}
	case int64:
		return value
	case int:
		return int64(value)
	case string:
		if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
			return parsed
		}
	}
	return fallback
}
