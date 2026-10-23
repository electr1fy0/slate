package chaos

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type qualityStatusRecorder struct {
	header http.Header
	codes  []int
}

func (w *qualityStatusRecorder) Header() http.Header            { return w.header }
func (w *qualityStatusRecorder) WriteHeader(code int)           { w.codes = append(w.codes, code) }
func (w *qualityStatusRecorder) Write(body []byte) (int, error) { return len(body), nil }
func (*qualityStatusRecorder) Flush()                           {}

func TestQualityResponseStatusCapture(t *testing.T) {
	underlying := &qualityStatusRecorder{header: make(http.Header)}
	capture := &statusWriter{ResponseWriter: underlying}
	capture.WriteHeader(http.StatusEarlyHints)
	capture.WriteHeader(http.StatusCreated)
	if capture.status != http.StatusCreated || len(underlying.codes) != 2 {
		t.Fatal("informational response swallowed final status")
	}
	underlying = &qualityStatusRecorder{header: make(http.Header)}
	capture = &statusWriter{ResponseWriter: underlying}
	capture.Flush()
	capture.WriteHeader(http.StatusInternalServerError)
	if capture.status != http.StatusOK || len(underlying.codes) != 1 {
		t.Fatal("flush did not commit captured status")
	}
}

func TestQualityFaultsCannotBeBypassedByHeadersAndIncludeRetakes(t *testing.T) {
	state := NewState(Config{ErrorPct: 100})
	req := httptest.NewRequest(http.MethodGet, "/exams", nil)
	req.Header.Set("Upgrade", "websocket")
	response := httptest.NewRecorder()
	state.Handler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("bypassed chaos") })).ServeHTTP(response, req)
	if response.Code != http.StatusInternalServerError {
		t.Fatal(response.Code)
	}
	if !isExamWrite(http.MethodPost, "/exam/example/retake") {
		t.Fatal("retake omitted from write faults")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if NewState(Config{}).Delay(ctx) == nil {
		t.Fatal("ignored zero-delay cancellation")
	}
}
