package control

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"time"

	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// HTTPDoer is intentionally smaller than *http.Client so the probe can be
// tested against connection errors, non-200 responses, and malformed bodies.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type HealthVerifier struct {
	Config  HealthVerifierConfig
	DDB     DDBProbe
	Metrics MetricsPublisher
	HTTP    HTTPDoer
	Now     func() time.Time
	Sleep   func(time.Duration)
	ID      func() string
	Log     io.Writer
}

var readRandom = rand.Read
var marshalJSON = json.Marshal

func NewHealthVerifier(cfg HealthVerifierConfig, ddb DDBProbe, metrics MetricsPublisher, httpClient HTTPDoer) *HealthVerifier {
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	return &HealthVerifier{
		Config:  cfg,
		DDB:     ddb,
		Metrics: metrics,
		HTTP:    httpClient,
		Now:     time.Now,
		Sleep:   time.Sleep,
		ID:      randomID,
		Log:     os.Stdout,
	}
}

// ProbeHTTP calls /health/deep and accepts the response only when both its
// HTTP status and the application's healthy field agree with the probe.
func (h *HealthVerifier) ProbeHTTP(ctx context.Context, targetURL string) map[string]any {
	url := strings.TrimRight(targetURL, "/")
	if url == "" {
		return map[string]any{"ok": false, "reason": "no target url"}
	}
	url += "/health/deep"
	started := h.Now()
	probeCtx, cancel := context.WithTimeout(ctx, time.Duration(h.Config.ProbeTimeoutS*float64(time.Second)))
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return map[string]any{"ok": false, "reason": truncate(err.Error(), 200)}
	}
	resp, err := h.HTTP.Do(req)
	if err != nil {
		return map[string]any{"ok": false, "reason": truncate(err.Error(), 200)}
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return map[string]any{"ok": false, "reason": truncate(err.Error(), 200)}
	}
	latency := roundOneDecimal(h.Now().Sub(started).Seconds() * 1000)
	healthy, _ := body["healthy"].(bool)
	result := map[string]any{
		"ok":          resp.StatusCode == http.StatusOK && healthy,
		"http_status": resp.StatusCode,
		"latency_ms":  latency,
	}
	if checks, ok := body["checks"]; ok {
		result["detail"] = checks
	} else {
		// A verifier response with an empty checks object includes an empty detail key when the
		// application omits checks, including on a healthy response.
		result["detail"] = map[string]any{}
	}
	return result
}

func (h *HealthVerifier) ProbeDDB(ctx context.Context) map[string]any {
	probeID := h.ID()
	now := h.Now().UnixMilli()
	key := map[string]dynamodbtypes.AttributeValue{
		"pk": &dynamodbtypes.AttributeValueMemberS{Value: "HEARTBEAT"},
		"sk": &dynamodbtypes.AttributeValueMemberS{Value: "PROBE#lambda-" + h.Config.RoleLabel + "-" + probeID},
	}
	item := map[string]dynamodbtypes.AttributeValue{
		"pk":  key["pk"],
		"sk":  key["sk"],
		"ts":  &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", now)},
		"ttl": &dynamodbtypes.AttributeValueMemberN{Value: fmt.Sprintf("%d", now/1000+300)},
	}
	started := h.Now()
	if err := h.DDB.Put(ctx, item); err != nil {
		return map[string]any{"ok": false, "reason": truncate(err.Error(), 200)}
	}
	got, err := h.DDB.Get(ctx, key, true)
	if err != nil {
		return map[string]any{"ok": false, "reason": truncate(err.Error(), 200)}
	}
	latency := roundOneDecimal(h.Now().Sub(started).Seconds() * 1000)
	if err := h.DDB.Delete(ctx, key); err != nil {
		return map[string]any{"ok": false, "reason": truncate(err.Error(), 200)}
	}
	return map[string]any{"ok": len(got) > 0, "latency_ms": latency}
}

func (h *HealthVerifier) Publish(ctx context.Context, ok, enabled bool) {
	if !enabled || h.Metrics == nil {
		return
	}
	err := h.Metrics.Put(ctx, h.Config.MetricNamespace, Metric{
		Name:       "Health",
		Dimensions: map[string]string{"Role": h.Config.RoleLabel},
		Value:      boolFloat(ok),
	})
	if err != nil {
		// A CloudWatch outage must never change the probe result itself.
		h.printJSON(map[string]any{"warning": "metric publish failed", "reason": truncate(err.Error(), 200)})
	}
}

// Handle preserves the Lambda result envelope exactly: scheduled invocations
// return statusCode/body for EventBridge, while force invocations return the
// diagnostic object directly for Step Functions.
func (h *HealthVerifier) Handle(ctx context.Context, event map[string]any) (any, error) {
	mode := stringValue(event, "mode", "scheduled")
	targetURL, hasTargetURL := optionalString(event, "target_url")
	if !hasTargetURL {
		targetURL = h.Config.TargetURL
	}
	publish := !hasTargetURL
	if value, ok := event["publish_metric"]; ok {
		publish = truthy(value)
	}

	httpResult := h.ProbeHTTP(ctx, targetURL)
	ddbResult := h.ProbeDDB(ctx)
	healthy := mapBool(httpResult, "ok") && mapBool(ddbResult, "ok")
	h.Publish(ctx, healthy, publish)
	result := map[string]any{
		"healthy":      healthy,
		"target":       stringValue(event, "target", h.Config.RoleLabel),
		"mode":         mode,
		"region_probe": ddbResult,
		"app_probe":    httpResult,
	}

	if mode == "scheduled" {
		body, err := marshalJSON(result)
		if err != nil {
			return nil, err
		}
		h.printJSON(result)
		return map[string]any{"statusCode": 200, "body": string(body)}, nil
	}

	if !healthy && truthy(event["retry_once"]) {
		delay := numberValue(event, "retry_delay_s", 10)
		h.Sleep(time.Duration(delay * float64(time.Second)))
		httpResult = h.ProbeHTTP(ctx, targetURL)
		ddbResult = h.ProbeDDB(ctx)
		healthy = mapBool(httpResult, "ok") && mapBool(ddbResult, "ok")
		result["healthy"] = healthy
		result["app_probe_retry"] = httpResult
		result["ddb_probe_retry"] = ddbResult
	}
	return result, nil
}

func (h *HealthVerifier) printJSON(value any) {
	if h.Log == nil {
		return
	}
	data, err := json.Marshal(value)
	if err == nil {
		_, _ = h.Log.Write(append(data, '\n'))
	}
}

func randomID() string {
	data := make([]byte, 16)
	if _, err := readRandom(data); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(data)
}

func boolFloat(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func mapBool(value map[string]any, key string) bool {
	result, _ := value[key].(bool)
	return result
}

func stringValue(event map[string]any, key, fallback string) string {
	if value, ok := event[key].(string); ok {
		return value
	}
	return fallback
}

func optionalString(event map[string]any, key string) (string, bool) {
	value, ok := event[key]
	if !ok {
		return "", false
	}
	result, ok := value.(string)
	return result, ok
}

func truthy(value any) bool {
	switch value := value.(type) {
	case bool:
		return value
	case nil:
		return false
	case string:
		return value != ""
	case float64:
		return value != 0
	case float32:
		return value != 0
	case int:
		return value != 0
	case int64:
		return value != 0
	default:
		return true
	}
}

func numberValue(event map[string]any, key string, fallback float64) float64 {
	value, ok := event[key]
	if !ok {
		return fallback
	}
	switch value := value.(type) {
	case float64:
		return value
	case float32:
		return float64(value)
	case int:
		return float64(value)
	case int64:
		return float64(value)
	}
	return fallback
}

func roundOneDecimal(value float64) float64 {
	return math.Round(value*10) / 10
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
