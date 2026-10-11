// Package chaos implements the deliberate fault injection and rolling error
// telemetry used by the health endpoint. State is process-local and safe for
// concurrent HTTP requests and admin updates.
package chaos

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultErrorWindow = 100
	maxErrorPercent    = 100
)

// Config is the mutable chaos configuration. ErrorPct is clamped to 0..100;
// negative latency is treated as zero.
type Config struct {
	ErrorPct          int
	ErrorPctExpiresMS int64
	LatencyMS         int
	DBStall           bool
	DropResponseOnce  bool
	ErrorWindow       int
}

// Snapshot is a race-free copy of current chaos flags.
type Snapshot struct {
	ErrorPct          int   `json:"error_pct"`
	ErrorPctExpiresMS int64 `json:"error_pct_expires_ms,omitempty"`
	LatencyMS         int   `json:"latency_ms"`
	DBStall           bool  `json:"db_stall"`
	DropResponseOnce  bool  `json:"drop_response_once"`
}

type statusEntry struct {
	id     uint64
	status int
}

// State stores chaos flags and a bounded window of completed HTTP statuses.
type State struct {
	mu                sync.RWMutex
	errorPct          int
	errorPctExpiresMS int64
	latencyMS         int
	dbStall           bool
	dropResponseOnce  bool
	window            int
	statuses          []statusEntry
	nextID            uint64
	now               func() time.Time
}

// ChaosState is retained as a readable migration alias.
type ChaosState = State

// NewState constructs state from config. A non-positive window uses 100, the
// service's default.
func NewState(config Config) *State {
	window := config.ErrorWindow
	if window <= 0 {
		window = defaultErrorWindow
	}
	s := &State{window: window, now: time.Now}
	s.Set(config)
	return s
}

// NewChaosState is a descriptive alias for NewState.
func NewChaosState(config Config) *State { return NewState(config) }

// Set updates all mutable flags.
func (s *State) Set(config Config) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.errorPct = clampPercent(config.ErrorPct)
	s.errorPctExpiresMS = config.ErrorPctExpiresMS
	s.latencyMS = maxInt(config.LatencyMS, 0)
	s.dbStall = config.DBStall
	s.dropResponseOnce = config.DropResponseOnce
	if config.ErrorWindow > 0 && config.ErrorWindow != s.window {
		s.window = config.ErrorWindow
		if len(s.statuses) > s.window {
			s.statuses = append([]statusEntry(nil), s.statuses[len(s.statuses)-s.window:]...)
		}
	}
	s.mu.Unlock()
}

// Configure changes only flags that are explicitly supplied by callers. This
// is useful for an admin endpoint where omitted JSON fields mean "unchanged".
type Patch struct {
	ErrorPct          *int   `json:"error_pct"`
	ErrorPctExpiresMS *int64 `json:"error_pct_expires_ms"`
	LatencyMS         *int   `json:"latency_ms"`
	DBStall           *bool  `json:"db_stall"`
	DropResponseOnce  *bool  `json:"drop_response_once"`
}

func (s *State) Configure(p Patch) {
	if s == nil {
		return
	}
	s.mu.Lock()
	if p.ErrorPct != nil {
		s.errorPct = clampPercent(*p.ErrorPct)
		if p.ErrorPctExpiresMS == nil {
			// Explicit manual changes remain in effect until reset unless the
			// caller provides an expiry in the same patch.
			s.errorPctExpiresMS = 0
		}
	}
	if p.ErrorPctExpiresMS != nil {
		s.errorPctExpiresMS = *p.ErrorPctExpiresMS
	}
	if p.LatencyMS != nil {
		s.latencyMS = maxInt(*p.LatencyMS, 0)
	}
	if p.DBStall != nil {
		s.dbStall = *p.DBStall
	}
	if p.DropResponseOnce != nil {
		s.dropResponseOnce = *p.DropResponseOnce
	}
	s.mu.Unlock()
}

// Snapshot returns current flags.
func (s *State) Snapshot() Snapshot {
	if s == nil {
		return Snapshot{}
	}
	s.expireErrorPctIfDue()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Snapshot{ErrorPct: s.errorPct, ErrorPctExpiresMS: s.errorPctExpiresMS, LatencyMS: s.latencyMS, DBStall: s.dbStall, DropResponseOnce: s.dropResponseOnce}
}

// SetNow injects the clock used for lazy fault expiry. Production callers
// should leave the default wall clock untouched.
func (s *State) SetNow(now func() time.Time) {
	if s == nil {
		return
	}
	if now == nil {
		now = time.Now
	}
	s.mu.Lock()
	s.now = now
	s.mu.Unlock()
}

func (s *State) expireErrorPctIfDue() {
	if s == nil {
		return
	}
	s.mu.Lock()
	if s.errorPctExpiresMS > 0 {
		now := time.Now
		if s.now != nil {
			now = s.now
		}
		if !now().Before(time.UnixMilli(s.errorPctExpiresMS)) {
			s.errorPct = 0
			s.errorPctExpiresMS = 0
		}
	}
	s.mu.Unlock()
}

// TakeDropResponseOnce consumes the post-commit response fault atomically.
func (s *State) TakeDropResponseOnce() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.dropResponseOnce {
		return false
	}
	s.dropResponseOnce = false
	return true
}

func (s *State) ErrorPct() int {
	if s == nil {
		return 0
	}
	s.expireErrorPctIfDue()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.errorPct
}

func (s *State) LatencyMS() int {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latencyMS
}

func (s *State) DBStall() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dbStall
}

// Reset clears flags but intentionally preserves the status window. The
// reset endpoint has the same behavior; use ClearStatuses in tests or
// when a deployment needs a fresh health-rate baseline.
func (s *State) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.errorPct, s.latencyMS, s.dbStall, s.dropResponseOnce = 0, 0, false, false
	s.errorPctExpiresMS = 0
	s.mu.Unlock()
}

func (s *State) ClearStatuses() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.statuses = nil
	s.mu.Unlock()
}

// Begin reserves a slot in the rolling window. Pending requests do not count
// toward errorRate until Record is called. The returned ID is safe to use
// after concurrent requests complete.
func (s *State) Begin() uint64 {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := s.nextID
	s.statuses = append(s.statuses, statusEntry{id: id})
	if len(s.statuses) > s.window {
		s.statuses = s.statuses[len(s.statuses)-s.window:]
	}
	return id
}

// Record completes a Begin reservation. A direct RecordStatus is provided for
// integrations that do not need request-start ordering.
func (s *State) Record(id uint64, status int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	for i := range s.statuses {
		if s.statuses[i].id == id {
			s.statuses[i].status = status
			break
		}
	}
	s.mu.Unlock()
}

func (s *State) RecordStatus(status int) {
	id := s.Begin()
	s.Record(id, status)
}

// Statuses returns completed statuses in arrival order. Zero/pending entries
// are omitted, matching the error_rate implementation.
func (s *State) Statuses() []int {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	statuses := make([]int, 0, len(s.statuses))
	for _, entry := range s.statuses {
		if entry.status != 0 {
			statuses = append(statuses, entry.status)
		}
	}
	return statuses
}

// ErrorRate returns the proportion of completed statuses >= 500.
func (s *State) ErrorRate() float64 {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total, errors int
	for _, entry := range s.statuses {
		if entry.status == 0 {
			continue
		}
		total++
		if entry.status >= 500 {
			errors++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(errors) / float64(total)
}

// ShouldInject applies the API path exemptions and samples the configured
// percentage. /health/live and every /admin/ route are never injected.
func (s *State) ShouldInject(path string) bool {
	if s == nil || isProbeExempt(path) {
		return false
	}
	s.expireErrorPctIfDue()
	s.mu.RLock()
	pct := s.errorPct
	s.mu.RUnlock()
	if pct <= 0 {
		return false
	}
	if pct >= maxErrorPercent {
		return true
	}
	return rand.Intn(100)+1 <= pct
}

func isProbeExempt(path string) bool {
	return len(path) >= len("/health/live") && path[:len("/health/live")] == "/health/live" ||
		len(path) >= len("/admin/") && path[:len("/admin/")] == "/admin/"
}

// Delay waits for configured latency and returns ctx.Err when the request is
// canceled during the delay. A timer avoids blocking a server worker thread.
func (s *State) Delay(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ms := s.LatencyMS()
	if ms <= 0 {
		return nil
	}
	timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Handler wraps an HTTP handler with latency, probabilistic 500 injection and
// status recording. It is intentionally independent of any router framework.
func (s *State) Handler(next http.Handler) http.Handler {
	if next == nil {
		next = http.NotFoundHandler()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			next.ServeHTTP(w, r)
			return
		}
		id := s.Begin()
		if err := s.Delay(r.Context()); err != nil {
			s.Record(id, 499)
			return
		}
		if s.DBStall() && isExamWrite(r.Method, r.URL.Path) {
			s.Record(id, http.StatusServiceUnavailable)
			writeJSONResponse(w, http.StatusServiceUnavailable, map[string]any{"detail": map[string]any{
				"code": "DATABASE_STALLED", "message": "exam data writes are temporarily unavailable", "retryable": true,
			}})
			return
		}
		if s.ShouldInject(r.URL.Path) {
			s.Record(id, http.StatusInternalServerError)
			writeChaosResponse(w)
			return
		}
		capture := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(capture, r)
		status := capture.status
		if status == 0 {
			status = http.StatusOK
		}
		s.Record(id, status)
	})
}

func isExamWrite(method, path string) bool {
	if !strings.HasPrefix(path, "/exam/") {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, "/exam/"), "/")
	switch {
	case method == http.MethodGet && len(parts) == 1:
		return true
	case method == http.MethodGet && len(parts) == 2 && parts[1] == "attempt":
		return true
	case method == http.MethodPut && len(parts) == 2 && parts[1] == "attempt":
		return true
	case method == http.MethodPost && len(parts) == 2 && parts[1] == "submit":
		return true
	case method == http.MethodPost && len(parts) == 3 && parts[1] == "attempt" && parts[2] == "submit":
		return true
	default:
		return false
	}
}

func writeJSONResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeChaosResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	body, _ := json.Marshal(map[string]string{"error": "chaos injected"})
	_, _ = w.Write(body)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

// Hijack preserves WebSocket upgrades through the chaos middleware. Gorilla
// WebSocket checks http.Hijacker on the response writer directly; without
// forwarding this optional interface every board connection fails with a 500
// before the endpoint can even validate its token.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("response writer does not support hijacking")
	}
	return hijacker.Hijack()
}

func (w *statusWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func clampPercent(value int) int {
	if value < 0 {
		return 0
	}
	if value > maxErrorPercent {
		return maxErrorPercent
	}
	return value
}

func maxInt(value, minimum int) int {
	if value < minimum {
		return minimum
	}
	return value
}
