package control

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeProbe struct {
	putErr, getErr, deleteErr error
	item                      map[string]dynamodbtypes.AttributeValue
	puts, gets, deletes       int
	lastGetConsistent         bool
}

func (f *fakeProbe) Put(_ context.Context, item map[string]dynamodbtypes.AttributeValue) error {
	f.puts++
	if f.putErr != nil {
		return f.putErr
	}
	f.item = item
	return nil
}
func (f *fakeProbe) Get(_ context.Context, _ map[string]dynamodbtypes.AttributeValue, consistent bool) (map[string]dynamodbtypes.AttributeValue, error) {
	f.gets++
	f.lastGetConsistent = consistent
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.item, nil
}
func (f *fakeProbe) Delete(_ context.Context, _ map[string]dynamodbtypes.AttributeValue) error {
	f.deletes++
	return f.deleteErr
}

type fakeMetrics struct {
	metrics []Metric
	err     error
}

func (f *fakeMetrics) Put(_ context.Context, _ string, metric Metric) error {
	if f.err != nil {
		return f.err
	}
	f.metrics = append(f.metrics, metric)
	return nil
}

type fakeHTTP struct {
	response *http.Response
	err      error
	seenURL  string
}

func (f *fakeHTTP) Do(req *http.Request) (*http.Response, error) {
	f.seenURL = req.URL.String()
	return f.response, f.err
}

func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

func testVerifier() (*HealthVerifier, *fakeProbe, *fakeMetrics, *fakeHTTP) {
	ddb := &fakeProbe{item: map[string]dynamodbtypes.AttributeValue{"ok": &dynamodbtypes.AttributeValueMemberBOOL{Value: true}}}
	metrics := &fakeMetrics{}
	httpClient := &fakeHTTP{response: response(200, `{"healthy":true,"checks":{"ddb":{"ok":true}}}`)}
	h := NewHealthVerifier(HealthVerifierConfig{TableName: "continuum", TargetURL: "http://app.internal", RoleLabel: "primary", MetricNamespace: "Continuum", ProbeTimeoutS: 8}, ddb, metrics, httpClient)
	h.ID = func() string { return "probe-id" }
	h.Now = func() time.Time { return time.Unix(100, 0) }
	return h, ddb, metrics, httpClient
}

func TestHealthVerifierProbeHTTPAndDDBContracts(t *testing.T) {
	h, ddb, _, httpClient := testVerifier()
	result := h.ProbeHTTP(context.Background(), "http://app.internal///")
	if !mapBool(result, "ok") || httpClient.seenURL != "http://app.internal/health/deep" {
		t.Fatalf("unexpected HTTP result: %#v URL=%s", result, httpClient.seenURL)
	}
	probe := h.ProbeDDB(context.Background())
	if !mapBool(probe, "ok") || !ddb.lastGetConsistent || ddb.puts != 1 || ddb.deletes != 1 {
		t.Fatalf("unexpected DDB probe: %#v calls=%d/%d/%d", probe, ddb.puts, ddb.gets, ddb.deletes)
	}
}

func TestHealthVerifierFailureAndScheduledEnvelope(t *testing.T) {
	h, ddb, metrics, httpClient := testVerifier()
	h.Log = new(bytes.Buffer)
	httpClient.err = errors.New(strings.Repeat("x", 300))
	ddb.putErr = errors.New("dynamodb throttled")
	result, err := h.Handle(context.Background(), map[string]any{"mode": "scheduled"})
	if err != nil {
		t.Fatal(err)
	}
	envelope := result.(map[string]any)
	if envelope["statusCode"] != 200 || len(metrics.metrics) != 1 || metrics.metrics[0].Value != 0 {
		t.Fatalf("scheduled envelope or metric mismatch: %#v metrics=%#v", envelope, metrics.metrics)
	}
	if !strings.Contains(envelope["body"].(string), `"healthy":false`) {
		t.Fatalf("body did not contain health result: %s", envelope["body"])
	}

	// A metric outage is logged, not promoted into a probe failure.
	httpClient.err = nil
	httpClient.response = response(200, `{"healthy":true}`)
	ddb.putErr = nil
	metrics.err = errors.New("cloudwatch unavailable")
	result, err = h.Handle(context.Background(), map[string]any{"mode": "force", "target_url": "http://override", "publish_metric": true})
	if err != nil || !mapBool(result.(map[string]any), "healthy") || !strings.Contains(h.Log.(*bytes.Buffer).String(), "metric publish failed") {
		t.Fatalf("metric outage changed result: %#v err=%v log=%s", result, err, h.Log.(*bytes.Buffer).String())
	}
}

func TestHealthVerifierRetryOnce(t *testing.T) {
	h, ddb, _, _ := testVerifier()
	h.Sleep = func(time.Duration) {}
	// Use a response sequence with a tiny adapter to verify retry payloads.
	seq := &sequenceHTTP{responses: []httpResult{{err: errors.New("down")}, {resp: response(200, `{"healthy":true}`)}}}
	h.HTTP = seq
	result, err := h.Handle(context.Background(), map[string]any{"mode": "force", "target_url": "http://x", "publish_metric": false, "retry_once": true, "retry_delay_s": 0})
	if err != nil || !mapBool(result.(map[string]any), "healthy") || len(seq.responses) != 0 || ddb.puts != 2 {
		t.Fatalf("retry did not recover: %#v err=%v remaining=%d ddb=%d", result, err, len(seq.responses), ddb.puts)
	}
}

type httpResult struct {
	resp *http.Response
	err  error
}
type sequenceHTTP struct{ responses []httpResult }

func (s *sequenceHTTP) Do(_ *http.Request) (*http.Response, error) {
	result := s.responses[0]
	s.responses = s.responses[1:]
	return result.resp, result.err
}

type fakeCommand struct {
	commandID string
	states    []CommandInvocation
	sends     int
	invokes   int
	err       error
}

func (f *fakeCommand) Send(_ context.Context, _, _, _ string) (string, error) {
	f.sends++
	return f.commandID, f.err
}
func (f *fakeCommand) Invocation(_ context.Context, _, _ string) (CommandInvocation, error) {
	if f.err != nil {
		return CommandInvocation{}, f.err
	}
	f.invokes++
	state := f.states[0]
	if len(f.states) > 1 {
		f.states = f.states[1:]
	}
	return state, nil
}

type fakeHealthCheck struct {
	stored bool
	err    error
	id     string
	value  bool
}

func (f *fakeHealthCheck) Update(_ context.Context, id string, inverted bool) (bool, error) {
	f.id, f.value = id, inverted
	if f.err != nil {
		return false, f.err
	}
	return f.stored, nil
}

func testOps(command CommandClient, route HealthCheckClient, ddb DDBProbe) *FailoverOps {
	return NewFailoverOps(FailoverOpsConfig{StandbyInstanceID: "i-standby", PrimaryHealthCheckID: "hc-primary", TableName: "continuum", CommandWaitTimeoutS: 3, CommandPollIntervalS: 0}, command, route, ddb)
}

func TestFailoverOpsCommandAndTrafficBranches(t *testing.T) {
	cmd := &fakeCommand{commandID: "cid", states: []CommandInvocation{{Status: "InProgress"}, {Status: "Success", StandardOutput: "alive"}}}
	ops := testOps(cmd, &fakeHealthCheck{stored: true}, &fakeProbe{})
	ops.Sleep = func(time.Duration) {}
	result, err := ops.Handle(context.Background(), map[string]any{"action": "promote"})
	if err != nil || result["promoted"] != true || cmd.invokes != 2 {
		t.Fatalf("promote mismatch: %#v err=%v invokes=%d", result, err, cmd.invokes)
	}
	cmd.states = []CommandInvocation{{Status: "Success"}}
	result, err = ops.Handle(context.Background(), map[string]any{"action": "demote"})
	if err != nil || result["promoted"] != false || result["demoted"] != true {
		t.Fatalf("demote mismatch: %#v err=%v", result, err)
	}
	route := &fakeHealthCheck{stored: false}
	ops.Route53 = route
	if _, err = ops.Handle(context.Background(), map[string]any{"action": "traffic_shift"}); err == nil || !strings.Contains(err.Error(), "did not confirm inversion") {
		t.Fatalf("lying Route53 should fail: %v", err)
	}
	if _, err = ops.Handle(context.Background(), map[string]any{"action": "detonate"}); err == nil || !strings.Contains(err.Error(), "unknown action 'detonate'") {
		t.Fatalf("unknown action should fail: %v", err)
	}
}

func TestFailoverOpsTimeoutAndReplication(t *testing.T) {
	cmd := &fakeCommand{commandID: "cid", states: []CommandInvocation{{Status: "Pending"}}}
	ops := testOps(cmd, &fakeHealthCheck{stored: true}, &fakeProbe{})
	clock := time.Unix(100, 0)
	ops.Now = func() time.Time { return clock }
	ops.Sleep = func(time.Duration) { clock = clock.Add(4 * time.Second) }
	if _, err := ops.Handle(context.Background(), map[string]any{"action": "promote"}); err == nil || !strings.Contains(err.Error(), "still 'Pending'") {
		t.Fatalf("pending command should timeout: %v", err)
	}
	probe := &fakeProbe{item: map[string]dynamodbtypes.AttributeValue{"ts": &dynamodbtypes.AttributeValueMemberN{Value: "100000"}}}
	ops = testOps(&fakeCommand{}, &fakeHealthCheck{stored: true}, probe)
	ops.ID = func() string { return "marker" }
	ops.Now = func() time.Time { return time.Unix(100, 0) }
	result, err := ops.Handle(context.Background(), map[string]any{"action": "replication_status"})
	if err != nil || result["replication_ok"] != true || probe.deletes != 1 {
		t.Fatalf("replication mismatch: %#v err=%v", result, err)
	}
	broken := &fakeProbe{putErr: errors.New("dynamodb unavailable")}
	ops.DDB = broken
	if _, err = ops.Handle(context.Background(), map[string]any{"action": "replication_status"}); err == nil || !strings.Contains(err.Error(), "replication probe failed") {
		t.Fatalf("replication errors must be raised: %v", err)
	}
}

type fakeStore struct {
	pages     []ScanPage
	current   []map[string]dynamodbtypes.AttributeValue
	putErr    error
	putCalls  []map[string]dynamodbtypes.AttributeValue
	scanCalls int
	metricErr error
}

func (f *fakeStore) Scan(_ context.Context, _ map[string]dynamodbtypes.AttributeValue, _ int64) (ScanPage, error) {
	page := f.pages[0]
	f.pages = f.pages[1:]
	f.scanCalls++
	return page, nil
}
func (f *fakeStore) GetItem(_ context.Context, key map[string]dynamodbtypes.AttributeValue) (map[string]dynamodbtypes.AttributeValue, error) {
	for _, item := range f.current {
		if stringAttr(item["pk"]) == stringAttr(key["pk"]) && stringAttr(item["sk"]) == stringAttr(key["sk"]) {
			return item, nil
		}
	}
	return nil, nil
}
func (f *fakeStore) PutReconciled(_ context.Context, item map[string]dynamodbtypes.AttributeValue, _ int) error {
	if f.putErr != nil {
		return f.putErr
	}
	f.putCalls = append(f.putCalls, item)
	return nil
}

func stringAttr(value dynamodbtypes.AttributeValue) string {
	if value, ok := value.(*dynamodbtypes.AttributeValueMemberS); ok && value != nil {
		return value.Value
	}
	return ""
}

func nAttr(value int64) dynamodbtypes.AttributeValue {
	return &dynamodbtypes.AttributeValueMemberN{Value: fmtInt(value)}
}
func fmtInt(value int64) string {
	return strconv.FormatInt(value, 10)
}

func journal(id string, version int, created int64) map[string]dynamodbtypes.AttributeValue {
	return map[string]dynamodbtypes.AttributeValue{
		"pk":         &dynamodbtypes.AttributeValueMemberS{Value: "JOURNAL#" + id},
		"sk":         &dynamodbtypes.AttributeValueMemberS{Value: "OUTBOX"},
		"target_pk":  &dynamodbtypes.AttributeValueMemberS{Value: "SUBMISSION#e1"},
		"target_sk":  &dynamodbtypes.AttributeValueMemberS{Value: "STUDENT#" + id},
		"version":    nAttr(int64(version)),
		"request_id": &dynamodbtypes.AttributeValueMemberS{Value: id},
		"created_at": nAttr(created),
		"answers":    &dynamodbtypes.AttributeValueMemberM{Value: map[string]dynamodbtypes.AttributeValue{"0": nAttr(1)}},
	}
}

func TestReconcilerPaginationRepairAndConflicts(t *testing.T) {
	store := &fakeStore{pages: []ScanPage{{Items: []map[string]dynamodbtypes.AttributeValue{journal("r1", 1, 1000)}, LastEvaluatedKey: map[string]dynamodbtypes.AttributeValue{"pk": &dynamodbtypes.AttributeValueMemberS{Value: "cursor"}}}, {Items: []map[string]dynamodbtypes.AttributeValue{journal("r2", 2, 1000), journal("r3", 1, 1000)}}}}
	store.current = []map[string]dynamodbtypes.AttributeValue{{"pk": &dynamodbtypes.AttributeValueMemberS{Value: "SUBMISSION#e1"}, "sk": &dynamodbtypes.AttributeValueMemberS{Value: "STUDENT#r2"}, "version": nAttr(5)}}
	metrics := &fakeMetrics{}
	r := NewReconciler(ReconcilerConfig{TableName: "continuum", LookbackMinutes: 30, MetricNamespace: "Continuum"}, store, metrics)
	r.Now = func() time.Time { return time.Unix(10, 0) }
	r.Region = func() string { return "ap-southeast-1" }
	r.Log = new(bytes.Buffer)
	result, err := r.Handle(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 3 || result.Repaired != 2 || result.Conflicts[0]["reason"] != "newer version present" || store.scanCalls != 2 || len(metrics.metrics) != 2 {
		t.Fatalf("reconciler summary mismatch: %#v scans=%d metrics=%#v", result, store.scanCalls, metrics.metrics)
	}
	if stringAttr(store.putCalls[0]["written_by_region"]) != "ap-southeast-1" || store.putCalls[0]["reconciled"].(*dynamodbtypes.AttributeValueMemberBOOL).Value != true {
		t.Fatalf("reconstructed item missing fields: %#v", store.putCalls[0])
	}
}

func TestReconcilerConditionalConflictAndFailures(t *testing.T) {
	store := &fakeStore{pages: []ScanPage{{Items: []map[string]dynamodbtypes.AttributeValue{journal("r1", 1, 1000)}}}, putErr: ErrConditionalCheckFailed}
	r := NewReconciler(ReconcilerConfig{TableName: "continuum", LookbackMinutes: 30}, store, &fakeMetrics{})
	r.Now = func() time.Time { return time.Unix(10, 0) }
	result, err := r.Handle(context.Background(), nil)
	if err != nil || result.Conflicts[0]["reason"] != "lost race" {
		t.Fatalf("conditional conflict mismatch: %#v err=%v", result, err)
	}
	store.pages = []ScanPage{{}}
	store.putErr = errors.New("dynamodb unavailable")
	metrics := &fakeMetrics{err: errors.New("cloudwatch unavailable")}
	r.Store, r.Metrics = store, metrics
	if _, err = r.Handle(context.Background(), nil); err != nil && !strings.Contains(err.Error(), "cloudwatch") {
		t.Fatalf("empty journal should reach metrics failure: %v", err)
	}
}

func TestLoadConfigsRejectInvalidValues(t *testing.T) {
	if _, err := LoadHealthVerifierConfig(func(key string) string {
		if key == "TABLE_NAME" {
			return "t"
		}
		if key == "PROBE_TIMEOUT_S" {
			return "bad"
		}
		return ""
	}); err == nil {
		t.Fatal("invalid health timeout should fail")
	}
	if _, err := LoadFailoverOpsConfig(func(key string) string {
		values := map[string]string{"STANDBY_INSTANCE_ID": "i", "PRIMARY_HEALTH_CHECK_ID": "h", "TABLE_NAME": "t", "COMMAND_WAIT_TIMEOUT_S": "bad"}
		return values[key]
	}); err == nil {
		t.Fatal("invalid command timeout should fail")
	}
	if _, err := LoadReconcilerConfig(func(key string) string {
		if key == "TABLE_NAME" {
			return "t"
		}
		if key == "LOOKBACK_MINUTES" {
			return "bad"
		}
		return ""
	}); err == nil {
		t.Fatal("invalid lookback should fail")
	}
}
