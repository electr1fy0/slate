package chaos

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStateRollingErrorRateAndReset(t *testing.T) {
	state := NewState(Config{ErrorWindow: 3, ErrorPct: 100, LatencyMS: 2, DBStall: true})
	for _, status := range []int{200, 500, 503, 200} {
		state.RecordStatus(status)
	}
	if got := state.Statuses(); len(got) != 3 || got[0] != 500 || got[1] != 503 || got[2] != 200 {
		t.Fatalf("rolling statuses = %#v", got)
	}
	if got := state.ErrorRate(); got != 2.0/3.0 {
		t.Fatalf("error rate = %v, want %v", got, 2.0/3.0)
	}
	state.Reset()
	if got := state.Snapshot(); got.ErrorPct != 0 || got.LatencyMS != 0 || got.DBStall {
		t.Fatalf("reset snapshot = %#v", got)
	}
	if len(state.Statuses()) != 3 {
		t.Fatal("reset should preserve completed status window")
	}
}

func TestHandlerInjectsAndExemptsHealthAndAdmin(t *testing.T) {
	state := NewState(Config{ErrorPct: 100, ErrorWindow: 10})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusAccepted)
	})
	handler := state.Handler(next)
	for _, path := range []string{"/health/live", "/admin/status"} {
		record := httptest.NewRecorder()
		handler.ServeHTTP(record, httptest.NewRequest(http.MethodGet, path, nil))
		if record.Code != http.StatusAccepted {
			t.Fatalf("%s status = %d", path, record.Code)
		}
	}
	record := httptest.NewRecorder()
	handler.ServeHTTP(record, httptest.NewRequest(http.MethodGet, "/exam/1", nil))
	if record.Code != http.StatusInternalServerError || record.Body.String() != `{"error":"chaos injected"}` {
		t.Fatalf("injected response = %d %q", record.Code, record.Body.String())
	}
	statuses := state.Statuses()
	if len(statuses) != 3 || statuses[0] != http.StatusAccepted || statuses[1] != http.StatusAccepted || statuses[2] != 500 {
		t.Fatalf("recorded statuses = %#v", statuses)
	}
}

func TestHandlerDoesNotInterceptWebSocketUpgrades(t *testing.T) {
	state := NewState(Config{ErrorPct: 100})
	handler := state.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))
	request := httptest.NewRequest(http.MethodGet, "/ws/board/room", nil)
	request.Header.Set("Upgrade", "websocket")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusSwitchingProtocols {
		t.Fatalf("websocket upgrade was intercepted: %d %s", response.Code, response.Body.String())
	}
}

func TestDelayHonorsCancellation(t *testing.T) {
	state := NewState(Config{LatencyMS: 1000})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := state.Delay(ctx); err != context.Canceled {
		t.Fatalf("delay error = %v, want context canceled", err)
	}
}

func TestConfigureAndStatusWindowAreConcurrentSafe(t *testing.T) {
	state := NewState(Config{ErrorWindow: 64})
	done := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func() {
			for j := 0; j < 100; j++ {
				state.Configure(Patch{ErrorPct: ptrInt(j), DBStall: ptrBool(j%2 == 0)})
				state.RecordStatus(200)
				_ = state.ErrorRate()
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 8; i++ {
		<-done
	}
	if len(state.Statuses()) > 64 {
		t.Fatal("status window exceeded configured size")
	}
}

func TestContextDelayActuallyWaitsWhenLive(t *testing.T) {
	state := NewState(Config{LatencyMS: 1})
	start := time.Now()
	if err := state.Delay(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < time.Millisecond {
		t.Fatal("latency delay returned too early")
	}
}

func TestStateNilReceiversAndConfigurationEdges(t *testing.T) {
	var nilState *State
	nilState.Set(Config{})
	nilState.Configure(Patch{})
	nilState.Reset()
	nilState.ClearStatuses()
	nilState.Record(1, http.StatusOK)
	nilState.RecordStatus(http.StatusOK)
	if nilState.Snapshot() != (Snapshot{}) || nilState.ErrorPct() != 0 || nilState.LatencyMS() != 0 || nilState.DBStall() {
		t.Fatal("nil receiver accessors returned non-zero values")
	}
	if nilState.Begin() != 0 || nilState.Statuses() != nil || nilState.ErrorRate() != 0 || nilState.ShouldInject("/exam") {
		t.Fatal("nil receiver operations were not no-ops")
	}

	defaultState := NewChaosState(Config{})
	if defaultState == nil || defaultState.ErrorPct() != 0 {
		t.Fatal("default chaos state was not initialized")
	}
	state := NewState(Config{ErrorWindow: 4, ErrorPct: 10, LatencyMS: 2, DBStall: true})
	for i := 0; i < 4; i++ {
		state.RecordStatus(http.StatusOK)
	}
	state.Set(Config{ErrorWindow: 2, ErrorPct: 101, LatencyMS: -1, DBStall: false})
	if got := state.Snapshot(); got != (Snapshot{ErrorPct: 100, LatencyMS: 0, DBStall: false}) {
		t.Fatalf("clamped Set snapshot = %#v", got)
	}
	if state.DBStall() {
		t.Fatal("DB stall should be disabled after Set")
	}
	state.Set(Config{ErrorWindow: 2})
	p := Patch{}
	state.Configure(p)
	state.Configure(Patch{ErrorPct: ptrInt(-1), LatencyMS: ptrInt(-1), DBStall: ptrBool(true)})
	if got := state.Snapshot(); got != (Snapshot{ErrorPct: 0, LatencyMS: 0, DBStall: true}) {
		t.Fatalf("configured snapshot = %#v", got)
	}
	state.ClearStatuses()
	if state.Statuses() == nil && state.ErrorRate() != 0 {
		t.Fatal("cleared state has a non-zero error rate")
	}
	pending := state.Begin()
	state.Record(pending+100, http.StatusInternalServerError)
	if got := state.Statuses(); len(got) != 0 || state.ErrorRate() != 0 {
		t.Fatalf("pending status was counted: %#v rate=%v", got, state.ErrorRate())
	}
	state.Record(pending, http.StatusBadRequest)
	state.RecordStatus(http.StatusInternalServerError)
	if got := state.ErrorRate(); got != .5 {
		t.Fatalf("error rate with non-5xx and 5xx = %v", got)
	}

	if clampPercent(-1) != 0 || clampPercent(101) != 100 || clampPercent(50) != 50 {
		t.Fatal("clampPercent edges failed")
	}
	if maxInt(-1, 0) != 0 || maxInt(3, 2) != 3 {
		t.Fatal("maxInt edges failed")
	}
}

func TestStateInjectionDelayAndHTTPWriterEdges(t *testing.T) {
	state := NewState(Config{ErrorPct: 0})
	if state.ShouldInject("/health/live/ready") || state.ShouldInject("/admin/anything") || state.ShouldInject("/exam") {
		t.Fatal("zero-percent injection occurred")
	}
	state.Configure(Patch{ErrorPct: ptrInt(50)})
	_ = state.ShouldInject("/exam")
	state.Configure(Patch{ErrorPct: ptrInt(100)})
	if !state.ShouldInject("/exam") || state.ShouldInject("/health/live") || state.ShouldInject("/admin/status") {
		t.Fatal("injection boundaries failed")
	}
	if !isProbeExempt("/health/live") || !isProbeExempt("/health/live/ready") || !isProbeExempt("/admin/status") || isProbeExempt("/health/check") {
		t.Fatal("probe exemption matching failed")
	}

	zero := NewState(Config{})
	if err := zero.Delay(nil); err != nil {
		t.Fatalf("nil context zero delay = %v", err)
	}

	state = NewState(Config{ErrorPct: 0})
	called := false
	state.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte("ok"))
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/normal", nil))
	if !called {
		t.Fatal("normal handler was not called")
	}
	state.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/implicit-ok", nil))
	missing := state.Handler(nil)
	response := httptest.NewRecorder()
	missing.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("nil next status = %d", response.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	state = NewState(Config{LatencyMS: 1000})
	response = httptest.NewRecorder()
	state.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("canceled handler was called") })).ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, "/cancel", nil))
	if got := state.Statuses(); len(got) != 1 || got[0] != 499 {
		t.Fatalf("canceled request statuses = %#v", got)
	}

	base := httptest.NewRecorder()
	writer := &statusWriter{ResponseWriter: base}
	writer.WriteHeader(http.StatusCreated)
	writer.WriteHeader(http.StatusNoContent)
	_, _ = writer.Write([]byte("body"))
	writer.Flush()
	if writer.Unwrap() != base || base.Code != http.StatusCreated {
		t.Fatalf("status writer state = %d", base.Code)
	}
	bare := &bareWriter{header: make(http.Header)}
	bareStatus := &statusWriter{ResponseWriter: bare}
	_, _ = bareStatus.Write([]byte("body"))
	bareStatus.Flush()
	if bare.status != http.StatusOK {
		t.Fatalf("bare writer status = %d", bare.status)
	}
	if _, _, err := (&statusWriter{ResponseWriter: httptest.NewRecorder()}).Hijack(); err == nil {
		t.Fatal("Hijack unexpectedly succeeded without an underlying hijacker")
	}
	left, right := net.Pipe()
	hijacker := &testHijackWriter{ResponseRecorder: httptest.NewRecorder(), conn: left}
	gotConn, gotRW, err := (&statusWriter{ResponseWriter: hijacker}).Hijack()
	if err != nil || gotConn != left || gotRW == nil {
		t.Fatalf("Hijack forwarding = %v", err)
	}
	_ = gotRW.Flush()
	_ = right.Close()
	_ = left.Close()

	if _, err := (&statusWriter{ResponseWriter: httptest.NewRecorder()}).Write([]byte("write")); err != nil {
		t.Fatal(err)
	}
}

type bareWriter struct {
	header http.Header
	status int
}

func (w *bareWriter) Header() http.Header            { return w.header }
func (w *bareWriter) WriteHeader(status int)         { w.status = status }
func (w *bareWriter) Write(body []byte) (int, error) { return len(body), nil }

type testHijackWriter struct {
	*httptest.ResponseRecorder
	conn net.Conn
}

func (w *testHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}

func ptrInt(value int) *int    { return &value }
func ptrBool(value bool) *bool { return &value }
