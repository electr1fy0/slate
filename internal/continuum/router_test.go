package continuum

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ayush/slate/internal/clusterstate"
	"github.com/gorilla/websocket"
)

func TestRouterUsesControlPointerAndReplacesUntrustedWriterHeaders(t *testing.T) {
	control, err := NewMemoryControl(ControlState{ActiveBackend: "A", Epoch: 17})
	if err != nil {
		t.Fatal(err)
	}
	var aCalls, bCalls atomic.Int32
	backendA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		aCalls.Add(1)
		if got := r.Header.Get(clusterstate.BackendHeader); got != "A" {
			t.Errorf("upstream backend header = %q, want A", got)
		}
		if got := r.Header.Get(clusterstate.EpochHeader); got != "17" {
			t.Errorf("upstream epoch header = %q, want 17", got)
		}
		w.Header().Set(clusterstate.BackendHeader, "spoofed")
		w.Header().Set(clusterstate.EpochHeader, "999")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "from-A "+r.URL.Path)
	}))
	defer backendA.Close()
	backendB := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { bCalls.Add(1) }))
	defer backendB.Close()
	router := &Router{Control: control, BackendURLs: map[string]string{"A": backendA.URL, "B": backendB.URL}, Timeout: time.Second}
	request := httptest.NewRequest(http.MethodGet, "/api/exams", nil)
	request.Header.Set(clusterstate.BackendHeader, "B")
	request.Header.Set(clusterstate.EpochHeader, "999")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "from-A /api/exams" {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get(clusterstate.BackendHeader); got != "A" {
		t.Fatalf("response backend header = %q, want A", got)
	}
	if got := response.Header().Get(clusterstate.EpochHeader); got != "17" {
		t.Fatalf("response epoch header = %q, want 17", got)
	}
	if aCalls.Load() != 1 || bCalls.Load() != 0 {
		t.Fatalf("backend calls A=%d B=%d, want A=1 B=0", aCalls.Load(), bCalls.Load())
	}
}

func TestRouterDoesNotBlindlyFallbackToStandby(t *testing.T) {
	control, _ := NewMemoryControl(ControlState{ActiveBackend: "A", Epoch: 3})
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	var standbyCalls atomic.Int32
	standby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		standbyCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer standby.Close()
	router := &Router{Control: control, BackendURLs: map[string]string{"A": dead.URL, "B": standby.URL}, Timeout: time.Second}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/health/deep", nil))
	if response.Code != http.StatusBadGateway || standbyCalls.Load() != 0 {
		t.Fatalf("selected-backend failure response=%d standby calls=%d", response.Code, standbyCalls.Load())
	}
}

func TestRouterBoundsRequestAndResponseBodies(t *testing.T) {
	control, _ := NewMemoryControl(ControlState{ActiveBackend: "A", Epoch: 1})
	var calls atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, strings.Repeat("x", 8))
	}))
	defer backend.Close()
	router := &Router{Control: control, BackendURLs: map[string]string{"A": backend.URL}, MaxRequestBytes: 4, MaxResponseBytes: 4}
	request := httptest.NewRequest(http.MethodPost, "/exam/submit", strings.NewReader("12345"))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || calls.Load() != 0 {
		t.Fatalf("oversized request response=%d backend calls=%d", response.Code, calls.Load())
	}
	request = httptest.NewRequest(http.MethodGet, "/health/deep", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway || !strings.Contains(response.Body.String(), "response too large") {
		t.Fatalf("oversized response = %d %q", response.Code, response.Body.String())
	}
}

func TestRouterRejectsPausedControl(t *testing.T) {
	control, _ := NewMemoryControl(ControlState{ActiveBackend: "A", Epoch: 1, Paused: true})
	router := &Router{Control: control, BackendURLs: map[string]string{"A": "http://127.0.0.1:1"}}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("paused route response = %d", response.Code)
	}
}

func TestRouterProxiesWebSocketUsingSelectedBackend(t *testing.T) {
	control, err := NewMemoryControl(ControlState{ActiveBackend: "B", Epoch: 23})
	if err != nil {
		t.Fatal(err)
	}
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get(clusterstate.BackendHeader); got != "B" {
			t.Errorf("backend writer header = %q, want B", got)
		}
		if got := r.Header.Get(clusterstate.EpochHeader); got != "23" {
			t.Errorf("epoch writer header = %q, want 23", got)
		}
		conn, err := upgrader.Upgrade(w, r, http.Header{clusterstate.BackendHeader: []string{"spoofed"}, clusterstate.EpochHeader: []string{"999"}})
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			messageType, message, readErr := conn.ReadMessage()
			if readErr != nil {
				return
			}
			if writeErr := conn.WriteMessage(messageType, message); writeErr != nil {
				return
			}
		}
	}))
	defer backend.Close()
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	router := &Router{Control: control, BackendURLs: map[string]string{"A": dead.URL, "B": backend.URL}, Timeout: time.Second}
	frontend := httptest.NewServer(router)
	defer frontend.Close()
	url := "ws" + strings.TrimPrefix(frontend.URL, "http") + "/ws/board/room"
	headers := http.Header{clusterstate.BackendHeader: []string{"A"}, clusterstate.EpochHeader: []string{"1"}}
	conn, response, err := websocket.DefaultDialer.Dial(url, headers)
	if err != nil {
		if response != nil {
			body, _ := io.ReadAll(response.Body)
			t.Fatalf("websocket dial: %v (status=%d body=%s)", err, response.StatusCode, body)
		}
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.Close()
	if got := response.Header.Get(clusterstate.BackendHeader); got != "B" {
		t.Fatalf("response backend header = %q, want B", got)
	}
	if got := response.Header.Get(clusterstate.EpochHeader); got != "23" {
		t.Fatalf("response epoch header = %q, want 23", got)
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	_, message, err := conn.ReadMessage()
	if err != nil || string(message) != "hello" {
		t.Fatalf("echo = %q, err = %v", message, err)
	}
}
