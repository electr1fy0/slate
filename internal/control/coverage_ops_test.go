package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestFailoverOpsCoverage(t *testing.T) {
	unknownCases := []map[string]any{{}, {"action": nil}, {"action": 123}}
	for _, event := range unknownCases {
		if _, err := testOps(&fakeCommand{}, &fakeHealthCheck{}, &fakeProbe{}).Handle(context.Background(), event); err == nil {
			t.Fatalf("unknown action accepted: %#v", event)
		}
	}

	sendErr := errors.New("send failed")
	if _, err := testOps(&fakeCommand{err: sendErr}, &fakeHealthCheck{}, &fakeProbe{}).Promote(context.Background()); !errors.Is(err, sendErr) {
		t.Fatalf("send error = %v", err)
	}
	invokeErr := errors.New("invoke failed")
	if _, err := testOps(splitCommand{invokeErr: invokeErr}, &fakeHealthCheck{}, &fakeProbe{}).Promote(context.Background()); !errors.Is(err, invokeErr) {
		t.Fatalf("invoke error = %v", err)
	}
	failedCommand := &fakeCommand{commandID: "cid", states: []CommandInvocation{{Status: "Failed", StandardError: "failure"}}}
	if _, err := testOps(failedCommand, &fakeHealthCheck{}, &fakeProbe{}).Promote(context.Background()); err == nil {
		t.Fatal("failed command accepted")
	}
	demoteErr := errors.New("demote failed")
	if _, err := testOps(&fakeCommand{err: demoteErr}, &fakeHealthCheck{}, &fakeProbe{}).Demote(context.Background()); !errors.Is(err, demoteErr) {
		t.Fatalf("demote error = %v", err)
	}

	routeErr := errors.New("route failed")
	ops := testOps(&fakeCommand{}, &fakeHealthCheck{err: routeErr}, &fakeProbe{})
	if _, err := ops.TrafficShift(context.Background(), nil); !errors.Is(err, routeErr) {
		t.Fatalf("route error = %v", err)
	}
	confirmed := &fakeHealthCheck{stored: false}
	ops.Route53 = confirmed
	result, err := ops.TrafficShift(context.Background(), map[string]any{"inverted": false})
	if err != nil || result["inverted"] != false || confirmed.value {
		t.Fatalf("false traffic shift = %#v %v", result, err)
	}
	result, err = ops.Handle(context.Background(), map[string]any{"action": "rollback_traffic"})
	if err != nil || result["inverted"] != false {
		t.Fatalf("rollback traffic = %#v %v", result, err)
	}

	probe := &fakeProbe{item: map[string]dynamodbtypes.AttributeValue{
		"ts": &dynamodbtypes.AttributeValueMemberN{Value: "100000"},
	}}
	ops = testOps(&fakeCommand{}, &fakeHealthCheck{}, probe)
	ops.ID = func() string { return "id" }
	ops.Now = func() time.Time { return time.Unix(100, 0) }
	ops.Sleep = func(time.Duration) {}
	if result, err := ops.ReplicationStatus(context.Background()); err != nil || result["replication_ok"] != true {
		t.Fatalf("replication success = %#v %v", result, err)
	}

	getErr := errors.New("get failed")
	getProbe := &fakeProbe{getErr: getErr}
	if _, err := testOps(&fakeCommand{}, &fakeHealthCheck{}, getProbe).ReplicationStatus(context.Background()); err == nil || !bytes.Contains([]byte(err.Error()), []byte("get failed")) {
		t.Fatalf("replication get error = %v", err)
	}
	deleteErr := errors.New("delete failed")
	deleteProbe := &fakeProbe{item: map[string]dynamodbtypes.AttributeValue{"ts": &dynamodbtypes.AttributeValueMemberN{Value: "100000"}}, deleteErr: deleteErr}
	ops = testOps(&fakeCommand{}, &fakeHealthCheck{}, deleteProbe)
	ops.Now = func() time.Time { return time.Unix(100, 0) }
	if _, err := ops.ReplicationStatus(context.Background()); err == nil || !bytes.Contains([]byte(err.Error()), []byte("delete failed")) {
		t.Fatalf("replication delete error = %v", err)
	}

	timedOut := testOps(&fakeCommand{}, &fakeHealthCheck{}, &fakeProbe{})
	nowCalls := 0
	timedOut.Now = func() time.Time {
		nowCalls++
		if nowCalls <= 2 {
			return time.Unix(100, 0)
		}
		return time.Unix(131, 0)
	}
	if _, err := timedOut.ReplicationStatus(context.Background()); err == nil {
		t.Fatal("timed-out replication probe succeeded")
	}

	attributeCases := []struct {
		value dynamodbtypes.AttributeValue
		ok    bool
	}{
		{value: nil, ok: false},
		{value: &dynamodbtypes.AttributeValueMemberS{Value: "1"}, ok: false},
		{value: &dynamodbtypes.AttributeValueMemberN{Value: "bad"}, ok: false},
		{value: &dynamodbtypes.AttributeValueMemberN{Value: "1"}, ok: true},
	}
	for _, test := range attributeCases {
		_, ok := attributeNumber(test.value)
		if ok != test.ok {
			t.Fatalf("attributeNumber(%#v) ok=%v, want %v", test.value, ok, test.ok)
		}
	}
	ops.Log = nil
	ops.printJSON(map[string]any{"ok": true})
	ops.Log = &bytes.Buffer{}
	ops.printJSON(func() {})

	if got := fmt.Sprint(terminalCommandStates["Cancelled"]); got != "true" {
		t.Fatal("terminal command map was not initialized")
	}
}

type replicationSequenceProbe struct {
	items []map[string]dynamodbtypes.AttributeValue
	gets  int
}

type splitCommand struct {
	sendErr   error
	invokeErr error
}

func (c splitCommand) Send(context.Context, string, string, string) (string, error) {
	return "command", c.sendErr
}

func (c splitCommand) Invocation(context.Context, string, string) (CommandInvocation, error) {
	return CommandInvocation{}, c.invokeErr
}

func (p *replicationSequenceProbe) Put(context.Context, map[string]dynamodbtypes.AttributeValue) error {
	return nil
}

func (p *replicationSequenceProbe) Get(context.Context, map[string]dynamodbtypes.AttributeValue, bool) (map[string]dynamodbtypes.AttributeValue, error) {
	item := p.items[p.gets]
	p.gets++
	return item, nil
}

func (p *replicationSequenceProbe) Delete(context.Context, map[string]dynamodbtypes.AttributeValue) error {
	return nil
}

func TestFailoverOpsReplicationRetryAndLogging(t *testing.T) {
	probe := &replicationSequenceProbe{items: []map[string]dynamodbtypes.AttributeValue{
		{"ts": &dynamodbtypes.AttributeValueMemberN{Value: "0"}},
		{"ts": &dynamodbtypes.AttributeValueMemberN{Value: "100000"}},
	}}
	ops := testOps(&fakeCommand{}, &fakeHealthCheck{}, probe)
	ops.ID = func() string { return "id" }
	ops.Now = func() time.Time { return time.Unix(100, 0) }
	ops.Sleep = func(time.Duration) {}
	result, err := ops.ReplicationStatus(context.Background())
	if err != nil || result["replication_ok"] != true || probe.gets != 2 {
		t.Fatalf("replication retry = %#v %v gets=%d", result, err, probe.gets)
	}
}
