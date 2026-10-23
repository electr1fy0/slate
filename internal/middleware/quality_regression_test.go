package middleware

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestQualityRateLimitCleanupAndRounding(t *testing.T) {
	now := time.Now()
	limiter := NewRateLimiter(map[string]Limit{"GET /short": {MaxHits: 1, Window: time.Second}, "GET /long": {MaxHits: 1, Window: time.Hour}})
	limiter.SetClock(func() time.Time { return now })
	limiter.Allow("GET", "/long", "long-lived")
	limiter.Allow("GET", "/short", "expired")
	now = now.Add(1500 * time.Millisecond)
	limiter.Allow("GET", "/short", "new")
	if allowed, _ := limiter.Allow("GET", "/long", "long-lived"); allowed {
		t.Fatal("cleanup erased an unexpired rule with a longer window")
	}
	now = now.Add(2 * time.Hour)
	limiter.Allow("GET", "/short", "latest")
	if len(limiter.hits) != 1 {
		t.Fatalf("expired buckets remain: %d", len(limiter.hits))
	}
	if RetryAfterSeconds(2*time.Second, 100*time.Millisecond) != 2 {
		t.Fatal("retry-after rounded down")
	}
	if strconvItoa(math.MinInt) != strconv.Itoa(math.MinInt) {
		t.Fatal("minimum integer formatting")
	}
}

func TestQualityRequestIDLengthAndFlush(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("x-request-id", strings.Repeat("x", 129))
	response := httptest.NewRecorder()
	RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := RequestIDFromContext(r.Context())
		if id == "" || len(id) > 128 {
			t.Fatal("unsafe request ID")
		}
		w.Header().Del("x-request-id")
		w.(http.Flusher).Flush()
	})).ServeHTTP(response, request)
	if response.Result().Header.Get("x-request-id") == "" {
		t.Fatal("flushed correlation ID missing")
	}
	if RequestIDFromContext(context.Background()) != "" {
		t.Fatal("unexpected context ID")
	}
}
