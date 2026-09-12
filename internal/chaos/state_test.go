package chaos

import (
	"context"
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

func ptrInt(value int) *int    { return &value }
func ptrBool(value bool) *bool { return &value }
