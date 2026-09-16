package control

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

func TestHealthVerifierCoverage(t *testing.T) {
	if verifier := NewHealthVerifier(HealthVerifierConfig{}, nil, nil, nil); verifier == nil {
		t.Fatal("nil dependencies did not construct a verifier")
	}
	h, ddb, metrics, _ := testVerifier()
	if got := h.ProbeHTTP(context.Background(), ""); !mapBool(got, "ok") && got["reason"] != "no target url" {
		t.Fatalf("empty target result = %#v", got)
	}
	if got := h.ProbeHTTP(context.Background(), "http://[::1"); mapBool(got, "ok") || got["reason"] == "" {
		t.Fatalf("invalid URL result = %#v", got)
	}
	h.HTTP = &fakeHTTP{err: errors.New(strings.Repeat("x", 300))}
	if got := h.ProbeHTTP(context.Background(), "http://app"); mapBool(got, "ok") || len(got["reason"].(string)) != 200 {
		t.Fatalf("HTTP error result = %#v", got)
	}
	h.HTTP = &fakeHTTP{response: response(200, "not json")}
	if got := h.ProbeHTTP(context.Background(), "http://app"); mapBool(got, "ok") || got["reason"] == "" {
		t.Fatalf("malformed HTTP result = %#v", got)
	}
	h.HTTP = &fakeHTTP{response: response(503, `{"healthy":true}`)}
	if got := h.ProbeHTTP(context.Background(), "http://app"); mapBool(got, "ok") || got["detail"].(map[string]any) == nil {
		t.Fatalf("non-200 HTTP result = %#v", got)
	}
	h.HTTP = &fakeHTTP{response: response(200, `{"healthy":false}`)}
	if got := h.ProbeHTTP(context.Background(), "http://app"); mapBool(got, "ok") || len(got["detail"].(map[string]any)) != 0 {
		t.Fatalf("missing-checks HTTP result = %#v", got)
	}

	ddb.getErr = errors.New("get failed")
	if got := h.ProbeDDB(context.Background()); mapBool(got, "ok") || got["reason"] == "" {
		t.Fatalf("DDB get result = %#v", got)
	}
	ddb.getErr = nil
	ddb.deleteErr = errors.New("delete failed")
	if got := h.ProbeDDB(context.Background()); mapBool(got, "ok") || got["reason"] == "" {
		t.Fatalf("DDB delete result = %#v", got)
	}
	ddb.deleteErr = nil
	h.DDB = emptyDDB{}
	if got := h.ProbeDDB(context.Background()); mapBool(got, "ok") {
		t.Fatalf("empty DDB item reported healthy: %#v", got)
	}

	h.Publish(context.Background(), true, false)
	h.Metrics = nil
	h.Publish(context.Background(), true, true)
	h.Metrics = metrics
	metrics.err = nil
	h.Publish(context.Background(), true, true)
	if len(metrics.metrics) == 0 || metrics.metrics[len(metrics.metrics)-1].Value != 1 {
		t.Fatal("successful metric was not published")
	}

	h.Log = nil
	h.printJSON(map[string]any{"ok": true})
	h.Log = &bytes.Buffer{}
	h.printJSON(func() {})
	oldMarshal := marshalJSON
	marshalJSON = func(any) ([]byte, error) { return nil, errors.New("marshal failed") }
	if _, err := h.Handle(context.Background(), map[string]any{"mode": "scheduled"}); err == nil {
		t.Fatal("scheduled marshal failure was ignored")
	}
	marshalJSON = oldMarshal

	oldRandom := readRandom
	readRandom = func([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
	if got := randomID(); got == "" {
		t.Fatal("random ID fallback was empty")
	}
	readRandom = oldRandom
	if got := randomID(); len(got) != 32 {
		t.Fatalf("random ID length = %d", len(got))
	}
}

type emptyDDB struct{}

func (emptyDDB) Put(context.Context, map[string]dynamodbtypes.AttributeValue) error { return nil }
func (emptyDDB) Get(context.Context, map[string]dynamodbtypes.AttributeValue, bool) (map[string]dynamodbtypes.AttributeValue, error) {
	return nil, nil
}
func (emptyDDB) Delete(context.Context, map[string]dynamodbtypes.AttributeValue) error { return nil }

func TestHealthVerifierTruthyAndNumberHelpers(t *testing.T) {
	truthyCases := []struct {
		value any
		want  bool
	}{
		{true, true}, {false, false}, {nil, false}, {"", false}, {"x", true},
		{float64(0), false}, {float64(1), true}, {float32(0), false}, {float32(1), true},
		{int(0), false}, {int(1), true}, {int64(0), false}, {int64(1), true},
		{map[string]any{}, true},
	}
	for _, test := range truthyCases {
		if got := truthy(test.value); got != test.want {
			t.Fatalf("truthy(%#v) = %v, want %v", test.value, got, test.want)
		}
	}
	event := map[string]any{
		"f64": float64(1.5), "f32": float32(2.5), "int": int(3), "int64": int64(4), "bad": "5",
	}
	if numberValue(event, "missing", 9) != 9 || numberValue(event, "f64", 0) != 1.5 || numberValue(event, "f32", 0) != 2.5 || numberValue(event, "int", 0) != 3 || numberValue(event, "int64", 0) != 4 || numberValue(event, "bad", 9) != 9 {
		t.Fatalf("numberValue cases failed")
	}

	// Force the force-mode retry branch to run with a failed first probe and a
	// successful second probe, while disabling metric side effects.
	h, ddb, _, _ := testVerifier()
	h.Metrics = nil
	h.Sleep = func(time.Duration) {}
	sequence := &sequenceHTTP{responses: []httpResult{
		{resp: response(500, `{"healthy":false}`)},
		{resp: response(200, `{"healthy":true}`)},
	}}
	h.HTTP = sequence
	result, err := h.Handle(context.Background(), map[string]any{
		"mode": "force", "target_url": "http://target", "retry_once": true, "retry_delay_s": float32(0),
	})
	if err != nil || !result.(map[string]any)["healthy"].(bool) || ddb.puts != 2 {
		t.Fatalf("force retry result = %#v %v ddb=%d", result, err, ddb.puts)
	}
}
