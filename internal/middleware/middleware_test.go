package middleware

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
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

func TestMiddlewareHelpersAndOptionalResponseWriterInterfaces(t *testing.T) {
	if RequestIDFromContext(nil) != "" || RequestIDFromContext(context.WithValue(context.Background(), contextKey("other"), "x")) != "" {
		t.Fatal("request ID context fallbacks failed")
	}
	if RequestIDMiddleware(nil) == nil {
		t.Fatal("named request ID middleware returned nil")
	}
	missing := httptest.NewRecorder()
	RequestID(nil).ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("nil RequestID next status = %d", missing.Code)
	}
	if AuthRateLimits(2, 3, time.Minute)["POST /auth/login"].MaxHits != 2 {
		t.Fatal("auth rate-limit rules were not built")
	}
	if clientHost(nil) != "unknown" || clientHost(&http.Request{}) != "unknown" {
		t.Fatal("nil request host fallback failed")
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "not-a-host-port"
	if clientHost(request) != "not-a-host-port" {
		t.Fatal("unparseable host was changed")
	}
	if formatSeconds(0) != "1" || formatSeconds(-2) != "1" || formatSeconds(42) != "42" {
		t.Fatal("formatSeconds edges failed")
	}
	if strconvItoa(0) != "0" || strconvItoa(-42) != "-42" || strconvItoa(12345) != "12345" {
		t.Fatal("strconvItoa edges failed")
	}
	if RetryAfterSeconds(time.Second, 2*time.Second) != 1 || RetryAfterSeconds(5*time.Second, time.Second) != 4 {
		t.Fatal("RetryAfterSeconds edges failed")
	}

	originalRead := readRandom
	readRandom = func([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
	if len(newRequestID()) != 16 {
		t.Fatal("random request ID fallback returned an invalid value")
	}
	readRandom = originalRead

	base := httptest.NewRecorder()
	writer := &requestIDWriter{ResponseWriter: base, id: "request-1"}
	writer.WriteHeader(http.StatusCreated)
	writer.WriteHeader(http.StatusNoContent)
	_, _ = writer.Write([]byte("body"))
	writer.Flush()
	if writer.Unwrap() != base || base.Header().Get("x-request-id") != "request-1" {
		t.Fatalf("request ID writer = %#v", base.Header())
	}
	bare := &bareResponseWriter{header: make(http.Header)}
	bareWriter := &requestIDWriter{ResponseWriter: bare, id: "request-2"}
	_, _ = bareWriter.Write([]byte("body"))
	bareWriter.Flush()
	if bare.header.Get("x-request-id") != "request-2" {
		t.Fatal("bare request ID writer did not set header")
	}
	if _, _, err := (&requestIDWriter{ResponseWriter: httptest.NewRecorder()}).Hijack(); err == nil {
		t.Fatal("Hijack unexpectedly succeeded")
	}
	left, right := net.Pipe()
	hijack := &hijackResponseWriter{ResponseRecorder: httptest.NewRecorder(), conn: left}
	conn, rw, err := (&requestIDWriter{ResponseWriter: hijack}).Hijack()
	if err != nil || conn != left || rw == nil {
		t.Fatalf("Hijack forwarding failed: %v", err)
	}
	_ = rw.Flush()
	_ = left.Close()
	_ = right.Close()
}

func TestRateLimiterNilAndInvalidRules(t *testing.T) {
	var nilLimiter *RateLimiter
	if ok, retry := nilLimiter.Allow("GET", "/", "client"); !ok || retry != 0 {
		t.Fatal("nil limiter did not allow")
	}
	if nilLimiter.Handler(nil) == nil {
		t.Fatal("nil limiter handler returned nil")
	}
	missing := httptest.NewRecorder()
	nilLimiter.Handler(nil).ServeHTTP(missing, httptest.NewRequest(http.MethodGet, "/", nil))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("nil limiter next status = %d", missing.Code)
	}
	limiter := NewRateLimiter(map[string]Limit{
		"GET /negative":    {MaxHits: -1, Window: time.Minute},
		"GET /zero-window": {MaxHits: 1},
	})
	limiter.SetClock(nil)
	limiter.limits["GET /nil-clock"] = Limit{MaxHits: 1, Window: time.Minute}
	limiter.now = nil
	if ok, _ := limiter.Allow("GET", "/nil-clock", "client"); !ok {
		t.Fatal("nil clock rule was unexpectedly blocked")
	}
	for _, path := range []string{"/negative", "/zero-window", "/missing"} {
		if ok, retry := limiter.Allow("GET", path, "client"); !ok || retry != 0 {
			t.Fatalf("invalid rule %s was limited: %v %d", path, ok, retry)
		}
	}
	limiter.SetClock(func() time.Time { return time.Unix(100, 0) })
	if ok, _ := limiter.Allow("GET", "/negative", "client"); !ok {
		t.Fatal("invalid rule rejected after clock reset")
	}
	short := NewRateLimiter(map[string]Limit{"GET /short": {MaxHits: 1, Window: time.Second}})
	clock := time.Unix(500, 0)
	short.SetClock(func() time.Time { return clock })
	if ok, _ := short.Allow("GET", "/short", "client"); !ok {
		t.Fatal("short limiter first request was blocked")
	}
	clock = clock.Add(999 * time.Millisecond)
	if ok, retry := short.Allow("GET", "/short", "client"); ok || retry != 1 {
		t.Fatalf("short limiter retry = %d, allowed=%v", retry, ok)
	}

	called := false
	handler := RateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusAccepted) }), map[string]Limit{})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusAccepted || !called {
		t.Fatal("RateLimitMiddleware did not pass through")
	}
}

type bareResponseWriter struct {
	header http.Header
	status int
}

func (w *bareResponseWriter) Header() http.Header            { return w.header }
func (w *bareResponseWriter) WriteHeader(status int)         { w.status = status }
func (w *bareResponseWriter) Write(body []byte) (int, error) { return len(body), nil }

type hijackResponseWriter struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w *hijackResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}
