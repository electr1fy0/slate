package continuum

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/ayush/slate/internal/clusterstate"
)

type cloudLambdaStub struct {
	invoke func(*lambda.InvokeInput) ([]byte, error)
}

type cloudSFNStub struct {
	input  *sfn.StartExecutionInput
	inputs []*sfn.StartExecutionInput
	err    error
}

func (s *cloudSFNStub) StartExecution(_ context.Context, input *sfn.StartExecutionInput, _ ...func(*sfn.Options)) (*sfn.StartExecutionOutput, error) {
	copy := *input
	s.input = &copy
	s.inputs = append(s.inputs, &copy)
	if s.err != nil {
		return nil, s.err
	}
	return &sfn.StartExecutionOutput{ExecutionArn: aws.String("arn:execution")}, nil
}

func (s cloudLambdaStub) Invoke(_ context.Context, input *lambda.InvokeInput, _ ...func(*lambda.Options)) (*lambda.InvokeOutput, error) {
	payload, err := s.invoke(input)
	if err != nil {
		return nil, err
	}
	return &lambda.InvokeOutput{StatusCode: 200, Payload: payload}, nil
}

type cloudDynamoStub struct {
	item       map[string]types.AttributeValue
	lastGet    *dynamodb.GetItemInput
	lastUpdate *dynamodb.UpdateItemInput
	updateErr  error
}

type resetEC2Stub struct {
	state         ec2types.InstanceStateName
	describeCalls int
	startCalls    int
}

func (s *resetEC2Stub) DescribeInstances(_ context.Context, input *ec2.DescribeInstancesInput, _ ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error) {
	s.describeCalls++
	instances := make([]ec2types.Instance, 0, len(input.InstanceIds))
	for _, id := range input.InstanceIds {
		instances = append(instances, ec2types.Instance{
			InstanceId: aws.String(id),
			State:      &ec2types.InstanceState{Name: s.state},
		})
	}
	return &ec2.DescribeInstancesOutput{Reservations: []ec2types.Reservation{{Instances: instances}}}, nil
}

func (s *resetEC2Stub) StopInstances(context.Context, *ec2.StopInstancesInput, ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error) {
	return &ec2.StopInstancesOutput{}, nil
}

func (s *resetEC2Stub) StartInstances(context.Context, *ec2.StartInstancesInput, ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error) {
	s.startCalls++
	return &ec2.StartInstancesOutput{}, nil
}

func (s *cloudDynamoStub) GetItem(_ context.Context, input *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	copy := *input
	s.lastGet = &copy
	item := s.item
	if item == nil {
		item = map[string]types.AttributeValue{
			"pk":             &types.AttributeValueMemberS{Value: clusterstate.ActivePK},
			"sk":             &types.AttributeValueMemberS{Value: clusterstate.ActiveSK},
			"active_backend": &types.AttributeValueMemberS{Value: "A"},
			"epoch":          &types.AttributeValueMemberN{Value: "1"},
			"paused":         &types.AttributeValueMemberBOOL{Value: false},
		}
	}
	return &dynamodb.GetItemOutput{Item: item}, nil
}

func (s *cloudDynamoStub) PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	return &dynamodb.PutItemOutput{}, nil
}

func (s *cloudDynamoStub) UpdateItem(_ context.Context, input *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	copy := *input
	s.lastUpdate = &copy
	return &dynamodb.UpdateItemOutput{}, s.updateErr
}

type collectingEvents struct{ events []Event }

func (s *collectingEvents) Append(_ context.Context, event Event) error {
	s.events = append(s.events, event)
	return nil
}
func (s *collectingEvents) Recent(context.Context, int32) ([]Event, error) {
	return append([]Event(nil), s.events...), nil
}

type failingEvents struct{ err error }

func (s failingEvents) Append(context.Context, Event) error            { return s.err }
func (s failingEvents) Recent(context.Context, int32) ([]Event, error) { return nil, s.err }

func TestDDBControlUsesStrongReadsAndExplicitIdempotentLease(t *testing.T) {
	dynamo := &cloudDynamoStub{}
	control := NewDDBControl(dynamo, "continuum")
	state, err := control.Get(context.Background())
	if err != nil || state.ActiveBackend != "A" || state.Epoch != 1 {
		t.Fatalf("get control state: %+v, %v", state, err)
	}
	if dynamo.lastGet == nil || !aws.ToBool(dynamo.lastGet.ConsistentRead) {
		t.Fatal("control pointer was not read with ConsistentRead=true")
	}
	if _, err := control.AcquireIncident(context.Background(), "incident-a", time.Minute); err != nil {
		t.Fatal(err)
	}
	if dynamo.lastUpdate == nil || !strings.Contains(aws.ToString(dynamo.lastUpdate.ConditionExpression), "#expires <= :now") || !strings.Contains(aws.ToString(dynamo.lastUpdate.ConditionExpression), "#owner = :owner") {
		t.Fatalf("lease condition is not expiry-aware and same-owner idempotent: %v", dynamo.lastUpdate)
	}
	if strings.Contains(strings.ToLower(aws.ToString(dynamo.lastUpdate.UpdateExpression)), "ttl") {
		t.Fatal("incident lock unexpectedly relies on TTL deletion")
	}
}

func TestDispatchRecordsReconciliationConflictsAsFailure(t *testing.T) {
	dynamo := &cloudDynamoStub{}
	eventsStore := &collectingEvents{}
	cloud := &CloudControl{Control: NewDDBControl(dynamo, "continuum"), Events: eventsStore}
	result, err := cloud.Dispatch(context.Background(), map[string]any{"action": "reconcile_completed", "incident_id": "i1", "summary": map[string]any{"scanned": float64(3), "processed": float64(1), "already_processed": float64(1), "conflict_count": float64(1)}})
	if err != nil || result["type"] != "reconcile_failed" {
		t.Fatalf("dispatch result=%#v err=%v", result, err)
	}
	if len(eventsStore.events) != 1 || eventsStore.events[0].Type != "reconcile_failed" || !strings.Contains(eventsStore.events[0].Message, "conflicts=1") {
		t.Fatalf("conflict audit event = %#v", eventsStore.events)
	}
	if result["recorded"] != true {
		t.Fatalf("successful audit append should be recorded: %#v", result)
	}
}

func TestDispatchDoesNotClaimReconciliationEventWasRecordedWhenAppendFails(t *testing.T) {
	boom := errors.New("event table unavailable")
	cloud := &CloudControl{Control: NewDDBControl(&cloudDynamoStub{}, "continuum"), Events: failingEvents{err: boom}}
	result, err := cloud.Dispatch(context.Background(), map[string]any{"action": "reconcile_completed", "incident_id": "i1", "summary": map[string]any{"conflict_count": float64(0)}})
	if result != nil || !errors.Is(err, boom) {
		t.Fatalf("failed audit append result=%#v err=%v", result, err)
	}
}

func TestCloudControlProtectsStatusWithAdminToken(t *testing.T) {
	dynamo := &cloudDynamoStub{}
	client := cloudLambdaStub{invoke: func(*lambda.InvokeInput) ([]byte, error) {
		return json.Marshal(map[string]any{"backends": []any{map[string]any{"id": "A", "healthy": true}, map[string]any{"id": "B", "healthy": true}}})
	}}
	cloud := NewCloudControl(CloudConfig{Region: "ap-southeast-2", TableName: "continuum", AdminToken: "secret", VerifierFunction: "verifier"}, NewDDBControl(dynamo, "continuum"), nil, client, nil, nil)
	bad := events.APIGatewayV2HTTPRequest{RawPath: "/api/continuum/status", Headers: map[string]string{}, RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET", Path: "/api/continuum/status"}}}
	response, err := cloud.HandleHTTPAPI(context.Background(), bad)
	if err != nil || response.StatusCode != 403 {
		t.Fatalf("unauthorized cloud status response=%+v err=%v", response, err)
	}
	bad.Headers["X-Admin-Token"] = "secret"
	response, err = cloud.HandleHTTPAPI(context.Background(), bad)
	if err != nil || response.StatusCode != 200 || !strings.Contains(response.Body, `"active_backend":"A"`) {
		t.Fatalf("authorized cloud status response=%+v err=%v", response, err)
	}
}

func TestLoadCloudConfigAllowsMonitorWithoutAdminToken(t *testing.T) {
	env := map[string]string{"AWS_REGION": "ap-southeast-2", "TABLE_NAME": "continuum", "VERIFIER_FUNCTION_NAME": "verifier"}
	cfg, err := LoadCloudConfig(func(name string) string { return env[name] })
	if err != nil || cfg.AdminToken != "" || cfg.VerifierFunction != "verifier" || cfg.RuntimeEnabled {
		t.Fatalf("monitor config=%+v err=%v", cfg, err)
	}
}

func TestLoadCloudConfigParsesRuntimeEnabledAndRejectsInvalidValue(t *testing.T) {
	env := map[string]string{"AWS_REGION": "ap-southeast-2", "TABLE_NAME": "continuum", "VERIFIER_FUNCTION_NAME": "verifier", "RUNTIME_ENABLED": "true"}
	cfg, err := LoadCloudConfig(func(name string) string { return env[name] })
	if err != nil || !cfg.RuntimeEnabled {
		t.Fatalf("runtime-enabled config=%+v err=%v", cfg, err)
	}
	env["RUNTIME_ENABLED"] = "sometimes"
	if _, err := LoadCloudConfig(func(name string) string { return env[name] }); err == nil || !strings.Contains(err.Error(), "RUNTIME_ENABLED") {
		t.Fatalf("invalid RUNTIME_ENABLED should fail closed, got %v", err)
	}
}

func TestCloudResetCannotStartInstancesWhileRuntimeDisabled(t *testing.T) {
	ec2Client := &resetEC2Stub{state: ec2types.InstanceStateNameStopped}
	cloud := NewCloudControl(CloudConfig{TableName: "continuum", VerifierFunction: "verifier", InstanceIDA: "i-a", InstanceIDB: "i-b"},
		NewDDBControl(&cloudDynamoStub{}, "continuum"), nil,
		cloudLambdaStub{invoke: func(*lambda.InvokeInput) ([]byte, error) { return []byte(`{}`), nil }}, nil, ec2Client)

	result, err := cloud.Action(context.Background(), ActionRequest{Action: "reset"})
	if err == nil || !strings.Contains(err.Error(), "make demo-resume") {
		t.Fatalf("disabled reset result=%#v err=%v", result, err)
	}
	if ec2Client.startCalls != 0 || ec2Client.describeCalls != 0 {
		t.Fatalf("disabled reset called EC2: starts=%d describes=%d", ec2Client.startCalls, ec2Client.describeCalls)
	}
}

func TestCloudResetStillWorksWhenRuntimeEnabled(t *testing.T) {
	ec2Client := &resetEC2Stub{state: ec2types.InstanceStateNameRunning}
	lambdaClient := cloudLambdaStub{invoke: func(input *lambda.InvokeInput) ([]byte, error) {
		var request map[string]any
		if err := json.Unmarshal(input.Payload, &request); err != nil {
			return nil, err
		}
		switch request["action"] {
		case "clear_faults":
			return []byte(`{"cleared":true}`), nil
		case "status":
			return []byte(`{"backends":[{"id":"A","healthy":true,"liveness":true,"ready":true},{"id":"B","healthy":true,"liveness":true,"ready":true}]}`), nil
		default:
			return nil, errors.New("unexpected verifier action")
		}
	}}
	cloud := NewCloudControl(CloudConfig{Region: "ap-southeast-2", TableName: "continuum", RuntimeEnabled: true, VerifierFunction: "verifier", InstanceIDA: "i-a", InstanceIDB: "i-b"},
		NewDDBControl(&cloudDynamoStub{}, "continuum"), nil, lambdaClient, nil, ec2Client)

	result, err := cloud.Action(context.Background(), ActionRequest{Action: "reset"})
	if err != nil || result["active_backend"] != "A" || result["epoch"] != float64(1) {
		t.Fatalf("enabled reset result=%#v err=%v", result, err)
	}
	if ec2Client.describeCalls != 4 || ec2Client.startCalls != 0 {
		t.Fatalf("enabled reset EC2 calls: starts=%d describes=%d", ec2Client.startCalls, ec2Client.describeCalls)
	}
}

func TestCloudMonitorStartsWorkflowOnlyAfterPrimarySpecificConfirmation(t *testing.T) {
	dynamo := &cloudDynamoStub{}
	var probes int
	client := cloudLambdaStub{invoke: func(*lambda.InvokeInput) ([]byte, error) {
		probes++
		return json.Marshal(map[string]any{"backends": []any{
			map[string]any{"id": "A", "healthy": false, "liveness": true, "ready": false},
			map[string]any{"id": "B", "healthy": true, "liveness": true, "ready": true},
		}})
	}}
	stateMachine := &cloudSFNStub{}
	cloud := NewCloudControl(CloudConfig{Region: "ap-southeast-2", TableName: "continuum", VerifierFunction: "verifier",
		StateMachineARN: "arn:state-machine", PolicyProbes: 3, ProbeIntervalMillis: 100, ProbeTimeout: time.Second},
		NewDDBControl(dynamo, "continuum"), nil, client, stateMachine, nil)
	cloud.Now = func() time.Time { return time.Unix(2000000000, 0) }
	result, err := cloud.MonitorIncident(context.Background())
	if err != nil || result["confirmed"] != true || probes != 3 || stateMachine.input == nil {
		t.Fatalf("monitor result=%#v probes=%d execution=%+v err=%v", result, probes, stateMachine.input, err)
	}
	var execution map[string]any
	if err := json.Unmarshal([]byte(aws.ToString(stateMachine.input.Input)), &execution); err != nil {
		t.Fatal(err)
	}
	if execution["operation"] != "route" || execution["target_backend"] != "B" || int64Value(execution, "incident_started_at_ms", 0) == 0 {
		t.Fatalf("workflow input lacks confirmed route and incident start: %#v", execution)
	}

	shared := NewCloudControl(cloud.Config, NewDDBControl(dynamo, "continuum"), nil,
		cloudLambdaStub{invoke: func(*lambda.InvokeInput) ([]byte, error) {
			return json.Marshal(map[string]any{"backends": []any{
				map[string]any{"id": "A", "healthy": false}, map[string]any{"id": "B", "healthy": false},
			}})
		}}, &cloudSFNStub{}, nil)
	sharedResult, err := shared.MonitorIncident(context.Background())
	if err != nil || sharedResult["confirmed"] != false {
		t.Fatalf("shared dependency failure result=%#v err=%v", sharedResult, err)
	}
}

func TestCloudBlipExpiresInsideConfirmationWindowAndSuppressesPromotion(t *testing.T) {
	dynamo := &cloudDynamoStub{}
	stateMachine := &cloudSFNStub{}
	now := time.Unix(2000000000, 0)
	var expiresAt time.Time
	var injectedDuration time.Duration
	client := cloudLambdaStub{invoke: func(input *lambda.InvokeInput) ([]byte, error) {
		var request map[string]any
		if err := json.Unmarshal(input.Payload, &request); err != nil {
			return nil, err
		}
		switch request["action"] {
		case "inject_fault":
			injectedDuration = time.Duration(int64Value(request, "duration_ms", 0)) * time.Millisecond
			expiresAt = now.Add(injectedDuration)
			return []byte(`{"accepted":true}`), nil
		case "status":
			activeHealthy := !now.Before(expiresAt)
			response, err := json.Marshal(map[string]any{"backends": []any{
				map[string]any{"id": "A", "healthy": activeHealthy, "liveness": true, "ready": activeHealthy},
				map[string]any{"id": "B", "healthy": true, "liveness": true, "ready": true},
			}})
			now = now.Add(100 * time.Millisecond)
			return response, err
		default: // the async monitor kick carries only {source:"blip"}.
			return []byte(`{}`), nil
		}
	}}
	cloud := NewCloudControl(CloudConfig{Region: "ap-southeast-2", TableName: "continuum", VerifierFunction: "verifier",
		MonitorFunction: "monitor", StateMachineARN: "arn:state-machine", PolicyProbes: 3, ProbeIntervalMillis: 100},
		NewDDBControl(dynamo, "continuum"), nil, client, stateMachine, nil)
	cloud.Now = func() time.Time { return now }

	action, err := cloud.Action(context.Background(), ActionRequest{Action: "blip"})
	if err != nil || action["accepted"] != true {
		t.Fatalf("inject blip action=%#v err=%v", action, err)
	}
	confirmationWindow := time.Duration(cloud.probes()-1) * time.Duration(cloud.intervalMillis()) * time.Millisecond
	if injectedDuration <= 0 || injectedDuration >= confirmationWindow || int64Value(action, "fault_expires_in_ms", 0) != injectedDuration.Milliseconds() {
		t.Fatalf("blip duration=%s, response=%#v, confirmation window=%s", injectedDuration, action, confirmationWindow)
	}

	result, err := cloud.MonitorIncident(context.Background())
	if err != nil || result["confirmed"] != false || result["reason"] != "active backend recovered" {
		t.Fatalf("transient failure monitor result=%#v err=%v", result, err)
	}
	if stateMachine.input != nil {
		t.Fatalf("transient blip started a failover workflow: %+v", stateMachine.input)
	}
}

func TestCloudBlipDurationStaysBelowTwoProbeConfirmationWindow(t *testing.T) {
	cloud := &CloudControl{Config: CloudConfig{PolicyProbes: 2, ProbeIntervalMillis: 100}}
	duration, err := cloud.blipDuration()
	window := time.Duration(cloud.probes()-1) * time.Duration(cloud.intervalMillis()) * time.Millisecond
	if err != nil || duration <= 0 || duration >= window {
		t.Fatalf("duration=%s confirmation window=%s err=%v", duration, window, err)
	}
}

func TestCloudVerifyActionInvokesMonitorAsynchronously(t *testing.T) {
	dynamo := &cloudDynamoStub{}
	var invocationType string
	client := cloudLambdaStub{invoke: func(input *lambda.InvokeInput) ([]byte, error) {
		invocationType = string(input.InvocationType)
		return []byte(`{}`), nil
	}}
	cloud := NewCloudControl(CloudConfig{Region: "ap-southeast-2", TableName: "continuum", AdminToken: "token",
		VerifierFunction: "verifier", MonitorFunction: "monitor"}, NewDDBControl(dynamo, "continuum"), nil, client, nil, nil)
	response, err := cloud.Action(context.Background(), ActionRequest{Action: "verify"})
	if err != nil || response["accepted"] != true || response["monitor_started"] != true || invocationType != "Event" {
		t.Fatalf("verify response=%#v invocation type=%q err=%v", response, invocationType, err)
	}
}

func TestCloudReconcileActionStartsUniqueWorkflowWithoutInvokingLambdaSynchronously(t *testing.T) {
	stateMachine := &cloudSFNStub{}
	var lambdaInvocations int
	client := cloudLambdaStub{invoke: func(*lambda.InvokeInput) ([]byte, error) {
		lambdaInvocations++
		return nil, errors.New("manual reconciliation must not invoke a page synchronously")
	}}
	cloud := NewCloudControl(CloudConfig{Region: "ap-southeast-2", TableName: "continuum", AdminToken: "token", StateMachineARN: "arn:state-machine"},
		NewDDBControl(&cloudDynamoStub{}, "continuum"), nil, client, stateMachine, nil)
	cloud.Now = func() time.Time { return time.Unix(2000000000, 0) }
	for call := 0; call < 2; call++ {
		result, err := cloud.Action(context.Background(), ActionRequest{Action: "reconcile"})
		if err != nil || result["accepted"] != true || result["execution_name"] == nil {
			t.Fatalf("reconcile response=%#v err=%v", result, err)
		}
	}
	if lambdaInvocations != 0 || len(stateMachine.inputs) != 2 {
		t.Fatalf("synchronous Lambda invokes=%d SFN executions=%d", lambdaInvocations, len(stateMachine.inputs))
	}
	firstName, secondName := aws.ToString(stateMachine.inputs[0].Name), aws.ToString(stateMachine.inputs[1].Name)
	if firstName == secondName {
		t.Fatalf("repeated manual reconciliations reused execution name %q", firstName)
	}
	var input map[string]any
	if err := json.Unmarshal([]byte(aws.ToString(stateMachine.inputs[0].Input)), &input); err != nil {
		t.Fatal(err)
	}
	if input["operation"] != "reconcile" || input["target_backend"] != "A" || int64Value(input, "expected_epoch", 0) != 1 {
		t.Fatalf("manual reconciliation workflow input=%#v", input)
	}
}

func TestDDBControlConditionalPromotionDoesNotTreatPausedAsSuccess(t *testing.T) {
	// A pause racing promotion changes the epoch; the strong read must fail
	// closed rather than acknowledging a same-target response-lost retry.
	dynamo := &cloudDynamoStub{updateErr: &types.ConditionalCheckFailedException{}, item: map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: clusterstate.ActivePK}, "sk": &types.AttributeValueMemberS{Value: clusterstate.ActiveSK},
		"active_backend": &types.AttributeValueMemberS{Value: "B"}, "epoch": &types.AttributeValueMemberN{Value: "3"}, "paused": &types.AttributeValueMemberBOOL{Value: true},
	}}
	control := NewDDBControl(dynamo, "continuum")
	control.Now = func() time.Time { return time.UnixMilli(1000) }
	_, err := control.Promote(context.Background(), clusterstate.Writer{Backend: "A", Epoch: 1}, "B", "owner")
	if !errors.Is(err, ErrControlPaused) {
		t.Fatalf("paused conditional promotion error = %v", err)
	}
}
