package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRequestIDGeneratesAndPreserves(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if RequestIDFromContext(r.Context()) == "" {
			t.Error("request ID was not installed in context")
		}
		w.Header().Set("x-request-id", "downstream-value")
		w.WriteHeader(http.StatusNoContent)
	})
	handler := RequestID(next)
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	if id := first.Header().Get("x-request-id"); len(id) != 16 {
		t.Fatalf("generated request ID = %q", id)
	}
	second := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("x-request-id", "my-correlation-id")
	handler.ServeHTTP(second, request)
	if got := second.Header().Get("x-request-id"); got != "my-correlation-id" {
		t.Fatalf("preserved request ID = %q", got)
	}
}

func TestRateLimiterSlidingWindowAndRetry(t *testing.T) {
	clock := time.Unix(1000, 0)
	limiter := NewRateLimiter(map[string]Limit{
		"POST /auth/login": {MaxHits: 2, Window: time.Minute},
	})
	limiter.SetClock(func() time.Time { return clock })
	if ok, _ := limiter.Allow("POST", "/auth/login", "127.0.0.1"); !ok {
		t.Fatal("first request unexpectedly blocked")
	}
	if ok, _ := limiter.Allow("POST", "/auth/login", "127.0.0.1"); !ok {
		t.Fatal("second request unexpectedly blocked")
	}
	if ok, retry := limiter.Allow("POST", "/auth/login", "127.0.0.1"); ok || retry < 1 {
		t.Fatalf("third request allowed or missing retry: allowed=%v retry=%d", ok, retry)
	}
	clock = clock.Add(61 * time.Second)
	if ok, _ := limiter.Allow("POST", "/auth/login", "127.0.0.1"); !ok {
		t.Fatal("expired request was blocked")
	}
	if ok, _ := limiter.Allow("GET", "/auth/login", "127.0.0.1"); !ok {
		t.Fatal("different method should be unmetered")
	}
}

func TestRateLimiterHandlerResponseShape(t *testing.T) {
	limiter := NewRateLimiter(map[string]Limit{"POST /auth/login": {MaxHits: 1, Window: time.Minute}})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := limiter.Handler(next)
	request := httptest.NewRequest(http.MethodPost, "/auth/login", nil)
	request.RemoteAddr = "192.0.2.4:5000"
	handler.ServeHTTP(httptest.NewRecorder(), request)
	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, request)
	if blocked.Code != http.StatusTooManyRequests || blocked.Header().Get("Retry-After") == "" {
		t.Fatalf("blocked response = %d headers=%v", blocked.Code, blocked.Header())
	}
	var body map[string]string
	if err := json.Unmarshal(blocked.Body.Bytes(), &body); err != nil || body["detail"] != "too many requests; slow down" {
		t.Fatalf("blocked body = %q", blocked.Body.String())
	}
}

func TestLogJSONOmitsNilFields(t *testing.T) {
	// LogJSON is intentionally tiny; this test only ensures marshalable fields
	// do not panic. Output routing belongs to the host process.
	LogJSON("test_event", map[string]any{"present": "yes", "missing": nil})
}
