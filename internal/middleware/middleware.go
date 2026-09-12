// Package middleware contains HTTP middleware shared by the API and probes.
package middleware

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"sync"
	"time"
)

type contextKey string

const requestIDKey contextKey = "request-id"

// RequestIDFromContext reads the correlation ID installed by RequestID.
func RequestIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

// RequestID adds or preserves x-request-id and echoes it on the response.
func RequestID(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("x-request-id")
		if id == "" {
			id = newRequestID()
		}
		wrapped := &requestIDWriter{ResponseWriter: w, id: id}
		wrapped.Header().Set("x-request-id", id)
		request := r.WithContext(context.WithValue(r.Context(), requestIDKey, id))
		next.ServeHTTP(wrapped, request)
	})
}

type requestIDWriter struct {
	http.ResponseWriter
	id string
}

func (w *requestIDWriter) WriteHeader(status int) {
	w.Header().Set("x-request-id", w.id)
	w.ResponseWriter.WriteHeader(status)
}

func (w *requestIDWriter) Write(body []byte) (int, error) {
	w.Header().Set("x-request-id", w.id)
	return w.ResponseWriter.Write(body)
}

func (w *requestIDWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *requestIDWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *requestIDWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// RequestIDMiddleware is a named constructor for integrations that prefer a
// middleware value over the RequestID function.
func RequestIDMiddleware(next http.Handler) http.Handler { return RequestID(next) }

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// A monotonically changing timestamp still supplies a useful local
		// correlation value if the OS entropy source is unavailable.
		now := time.Now().UnixNano()
		return hex.EncodeToString([]byte{
			byte(now >> 56), byte(now >> 48), byte(now >> 40), byte(now >> 32),
			byte(now >> 24), byte(now >> 16), byte(now >> 8), byte(now),
		})
	}
	return hex.EncodeToString(b)
}

// LogJSON writes one structured JSON line; nil values are omitted from fields,
// matching the legacy service's logging contract.
func LogJSON(event string, fields map[string]any) {
	record := make(map[string]any, len(fields)+2)
	record["ts"] = float64(time.Now().UnixMilli()) / 1000
	record["event"] = event
	for key, value := range fields {
		if value != nil {
			record[key] = value
		}
	}
	encoded, err := json.Marshal(record)
	if err == nil {
		fmt.Println(string(encoded))
	}
}

// Limit describes a sliding-window request limit.
type Limit struct {
	MaxHits int
	Window  time.Duration
}

// RateLimiter is an in-memory per-process sliding-window limiter. Rules are
// keyed by "METHOD /path" and buckets by that rule plus client host.
type RateLimiter struct {
	mu     sync.Mutex
	limits map[string]Limit
	hits   map[string][]time.Time
	now    func() time.Time
}

func NewRateLimiter(limits map[string]Limit) *RateLimiter {
	copyLimits := make(map[string]Limit, len(limits))
	for key, limit := range limits {
		copyLimits[key] = limit
	}
	return &RateLimiter{limits: copyLimits, hits: make(map[string][]time.Time), now: time.Now}
}

// SetClock exists for deterministic tests and embedded simulations.
func (l *RateLimiter) SetClock(clock func() time.Time) {
	l.mu.Lock()
	if clock == nil {
		l.now = time.Now
	} else {
		l.now = clock
	}
	l.mu.Unlock()
}

// Allow checks and consumes one hit. RetryAfter is in whole seconds and is at
// least one when denied, matching the API middleware's header behavior.
func (l *RateLimiter) Allow(method, path, client string) (allowed bool, retryAfter int) {
	if l == nil {
		return true, 0
	}
	ruleKey := method + " " + path
	l.mu.Lock()
	defer l.mu.Unlock()
	limit, ok := l.limits[ruleKey]
	if !ok || limit.MaxHits < 0 || limit.Window <= 0 {
		return true, 0
	}
	now := l.now
	if now == nil {
		now = time.Now
	}
	nowValue := now()
	bucketKey := ruleKey + "#" + client
	hits := l.hits[bucketKey]
	horizon := nowValue.Add(-limit.Window)
	kept := hits[:0]
	for _, hit := range hits {
		if hit.After(horizon) {
			kept = append(kept, hit)
		}
	}
	hits = kept
	if len(hits) >= limit.MaxHits {
		remaining := limit.Window - nowValue.Sub(hits[0])
		retryAfter = int(remaining.Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		l.hits[bucketKey] = hits
		return false, retryAfter
	}
	hits = append(hits, nowValue)
	l.hits[bucketKey] = hits
	return true, 0
}

// Handler enforces configured limits before invoking next.
func (l *RateLimiter) Handler(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client := clientHost(r)
		allowed, retryAfter := l.Allow(r.Method, r.URL.Path, client)
		if !allowed {
			w.Header().Set("Retry-After", formatSeconds(retryAfter))
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"detail": "too many requests; slow down",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RateLimitMiddleware provides an explicit constructor-style alias.
func RateLimitMiddleware(next http.Handler, limits map[string]Limit) http.Handler {
	return NewRateLimiter(limits).Handler(next)
}

// AuthRateLimits converts common environment-driven values into rules. The
// caller owns parsing environment variables; this helper keeps the contract
// in one place for API binaries.
func AuthRateLimits(login, register int, window time.Duration) map[string]Limit {
	return map[string]Limit{
		"POST /auth/login":    {MaxHits: login, Window: window},
		"POST /auth/register": {MaxHits: register, Window: window},
	}
}

func clientHost(r *http.Request) string {
	if r == nil || r.RemoteAddr == "" {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func formatSeconds(seconds int) string {
	if seconds < 1 {
		seconds = 1
	}
	return strconvItoa(seconds)
}

// Avoid pulling a formatting package into hot middleware paths.
func strconvItoa(value int) string {
	if value == 0 {
		return "0"
	}
	negative := value < 0
	if negative {
		value = -value
	}
	var digits [32]byte
	i := len(digits)
	for value > 0 {
		i--
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	if negative {
		i--
		digits[i] = '-'
	}
	return string(digits[i:])
}

// RetryAfterSeconds is exposed for clients that need the same truncation
// policy without constructing an HTTP handler.
func RetryAfterSeconds(window, elapsed time.Duration) int {
	seconds := int(math.Floor((window - elapsed).Seconds()))
	if seconds < 1 {
		return 1
	}
	return seconds
}
