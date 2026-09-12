package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var terminalCommandStates = map[string]bool{
	"Success":   true,
	"Failed":    true,
	"Cancelled": true,
	"TimedOut":  true,
}

// FailoverOps contains the side-effecting operations used by both the
// failover and manual failback state machines. All clients are interfaces so
// tests can exercise timeout, failure, and rollback branches without AWS.
type FailoverOps struct {
	Config  FailoverOpsConfig
	SSM     CommandClient
	Route53 HealthCheckClient
	DDB     DDBProbe
	Now     func() time.Time
	Sleep   func(time.Duration)
	ID      func() string
	Log     io.Writer
}

func NewFailoverOps(cfg FailoverOpsConfig, ssm CommandClient, route53 HealthCheckClient, ddb DDBProbe) *FailoverOps {
	return &FailoverOps{
		Config:  cfg,
		SSM:     ssm,
		Route53: route53,
		DDB:     ddb,
		Now:     time.Now,
		Sleep:   time.Sleep,
		ID:      randomID,
		Log:     os.Stdout,
	}
}

// Handle dispatches one action. Errors are returned to aws-lambda-go and
// therefore become Lambda failures, which is required for Step Functions
// States.TaskFailed/Catch routing.
func (o *FailoverOps) Handle(ctx context.Context, event map[string]any) (map[string]any, error) {
	rawAction, present := event["action"]
	action, _ := rawAction.(string)
	var result map[string]any
	var err error
	switch action {
	case "promote":
		result, err = o.Promote(ctx)
	case "demote":
		result, err = o.Demote(ctx)
	case "traffic_shift":
		result, err = o.TrafficShift(ctx, event)
	case "rollback_traffic":
		result, err = o.TrafficShift(ctx, map[string]any{"inverted": false})
	case "replication_status":
		result, err = o.ReplicationStatus(ctx)
	default:
		actions := []string{"demote", "promote", "replication_status", "rollback_traffic", "traffic_shift"}
		displayAction := fmt.Sprint(rawAction)
		if !present || rawAction == nil {
			displayAction = "None"
		}
		return nil, fmt.Errorf("unknown action '%s'; expected one of %s", displayAction, strings.Join(actions, ", "))
	}
	if err != nil {
		return nil, err
	}
	o.printJSON(map[string]any{"action": action, "result": result})
	return result, nil
}

func (o *FailoverOps) Promote(ctx context.Context) (map[string]any, error) {
	return o.runVerifiedCommand(ctx, o.Config.StandbyInstanceID, o.Config.PromoteCommand, "Continuum: promote standby to active")
}

func (o *FailoverOps) Demote(ctx context.Context) (map[string]any, error) {
	result, err := o.runVerifiedCommand(ctx, o.Config.StandbyInstanceID, o.Config.DemoteCommand, "Continuum: demote standby back to read-only")
	if err != nil {
		return nil, err
	}
	result["promoted"] = false
	result["demoted"] = true
	return result, nil
}

func (o *FailoverOps) runVerifiedCommand(ctx context.Context, instanceID, command, comment string) (map[string]any, error) {
	commandID, err := o.SSM.Send(ctx, instanceID, command, comment)
	if err != nil {
		return nil, err
	}
	deadline := o.Now().Add(time.Duration(o.Config.CommandWaitTimeoutS) * time.Second)
	var invocation CommandInvocation
	for {
		invocation, err = o.SSM.Invocation(ctx, commandID, instanceID)
		if err != nil {
			return nil, err
		}
		if terminalCommandStates[invocation.Status] {
			break
		}
		if o.Now().After(deadline) {
			return nil, fmt.Errorf("ssm command %s still '%s' after %ds", commandID, invocation.Status, o.Config.CommandWaitTimeoutS)
		}
		o.Sleep(time.Duration(o.Config.CommandPollIntervalS * float64(time.Second)))
	}
	if invocation.Status != "Success" {
		return nil, fmt.Errorf("ssm command %s finished as '%s': %s", commandID, invocation.Status, truncate(invocation.StandardError, 200))
	}
	return map[string]any{
		"promoted":   true,
		"command_id": commandID,
		"instance":   instanceID,
		"output":     truncate(invocation.StandardOutput, 500),
		"status":     invocation.Status,
	}, nil
}

func (o *FailoverOps) TrafficShift(ctx context.Context, payload map[string]any) (map[string]any, error) {
	inverted := true
	if value, ok := payload["inverted"]; ok {
		inverted = truthy(value)
	}
	stored, err := o.Route53.Update(ctx, o.Config.PrimaryHealthCheckID, inverted)
	if err != nil {
		return nil, err
	}
	if stored != inverted {
		return nil, fmt.Errorf("route53 did not confirm inversion=%t for %s", inverted, o.Config.PrimaryHealthCheckID)
	}
	return map[string]any{
		"traffic_shifted": true,
		"health_check":    o.Config.PrimaryHealthCheckID,
		"inverted":        inverted,
	}, nil
}

func (o *FailoverOps) ReplicationStatus(ctx context.Context) (map[string]any, error) {
	probeID := o.ID()
	now := o.Now().UnixMilli()
	key := map[string]dynamodbtypes.AttributeValue{
		"pk": &dynamodbtypes.AttributeValueMemberS{Value: "REPLMARKER#" + probeID},
		"sk": &dynamodbtypes.AttributeValueMemberS{Value: "X"},
	}
	item := map[string]dynamodbtypes.AttributeValue{
		"pk":  key["pk"],
		"sk":  key["sk"],
		"ts":  &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", now)},
		"ttl": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", now/1000+3600)},
	}
	started := o.Now()
	deadline := started.Add(30 * time.Second)
	for {
		// Match the verifier loop's pre-flight deadline check: a slow probe is
		// allowed to complete the current read, then backs off once before the
		// next iteration notices the 30-second limit.
		if !o.Now().Before(deadline) {
			return nil, fmt.Errorf("replication probe timed out after 30s")
		}
		if err := o.DDB.Put(ctx, item); err != nil {
			return nil, fmt.Errorf("replication probe failed: %s", truncate(err.Error(), 200))
		}
		got, err := o.DDB.Get(ctx, key, false)
		if err != nil {
			return nil, fmt.Errorf("replication probe failed: %s", truncate(err.Error(), 200))
		}
		if value, ok := attributeNumber(got["ts"]); ok && value == now {
			if err := o.DDB.Delete(ctx, key); err != nil {
				return nil, fmt.Errorf("replication probe failed: %s", truncate(err.Error(), 200))
			}
			return map[string]any{
				"replication_ok": true,
				"note":           "local replica writable",
				"elapsed_s":      roundOneDecimal(o.Now().Sub(started).Seconds()),
			}, nil
		}
		o.Sleep(2 * time.Second)
	}
}

func attributeNumber(value dynamodbtypes.AttributeValue) (int64, bool) {
	n, ok := value.(*dynamodbtypes.AttributeValueMemberN)
	if !ok || n == nil {
		return 0, false
	}
	var parsed int64
	parsed, err := strconv.ParseInt(n.Value, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}

func (o *FailoverOps) printJSON(value any) {
	if o.Log == nil {
		return
	}
	data, err := json.Marshal(value)
	if err == nil {
		_, _ = o.Log.Write(append(data, '\n'))
	}
}
