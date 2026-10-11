package continuum

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ayush/slate/internal/clusterstate"
)

func TestVerifierBlipUsesBackendLazyExpiry(t *testing.T) {
	var expiryMS atomic.Int64
	baseTime := time.Now().Add(time.Minute).Truncate(time.Millisecond)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/chaos" || r.Header.Get("x-admin-token") != "verifier-secret" {
			http.Error(w, "unexpected request", http.StatusForbidden)
			return
		}
		var patch struct {
			ErrorPct          int   `json:"error_pct"`
			ErrorPctExpiresMS int64 `json:"error_pct_expires_ms"`
		}
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			http.Error(w, "invalid patch", http.StatusBadRequest)
			return
		}
		expiryMS.Store(patch.ErrorPctExpiresMS)
		if patch.ErrorPct != 100 || patch.ErrorPctExpiresMS <= time.Now().UnixMilli() {
			http.Error(w, "missing bounded expiry", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	control, _ := NewMemoryControl(ControlState{ActiveBackend: "A", Epoch: 1})
	verifier := &Verifier{Config: VerifierConfig{Region: "local", AdminToken: "verifier-secret", BackendURLs: map[string]string{"A": server.URL, "B": server.URL}}, Control: control,
		Now: func() time.Time { return baseTime }}
	result, err := verifier.Handle(context.Background(), map[string]any{"action": "inject_fault", "fault": "blip", "backend": "A", "duration_ms": 150})
	if err != nil || result["accepted"] != true {
		t.Fatalf("inject blip result=%#v err=%v", result, err)
	}
	if got, want := expiryMS.Load(), baseTime.Add(150*time.Millisecond).UnixMilli(); got != want {
		t.Fatalf("blip expiry = %d, want exact injected-clock expiry %d", got, want)
	}
}

func TestVerifierProbesBackendsConcurrently(t *testing.T) {
	var liveCalls atomic.Int32
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	servers := map[string]*httptest.Server{}
	for _, id := range []string{"A", "B"} {
		backendID := id
		servers[id] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health/live" {
				if got := r.Header.Get(clusterstate.BackendHeader); got != backendID {
					http.Error(w, "wrong backend metadata", http.StatusForbidden)
					return
				}
				liveCalls.Add(1)
				arrived <- struct{}{}
				select {
				case <-release:
				case <-r.Context().Done():
					return
				case <-time.After(time.Second):
					http.Error(w, "probes were sequential", http.StatusGatewayTimeout)
					return
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			if r.URL.Path == "/health/deep" {
				_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "checks": map[string]any{"ddb": map[string]any{"ok": true}}})
				return
			}
			http.NotFound(w, r)
		}))
		defer servers[id].Close()
	}
	control, _ := NewMemoryControl(ControlState{ActiveBackend: "A", Epoch: 1})
	verifier := &Verifier{Config: VerifierConfig{Region: "local", BackendURLs: map[string]string{"A": servers["A"].URL, "B": servers["B"].URL}, ProbeTimeout: time.Second}, Control: control}
	done := make(chan error, 1)
	go func() {
		_, err := verifier.Handle(context.Background(), map[string]any{"action": "status"})
		done <- err
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-arrived:
		case <-time.After(500 * time.Millisecond):
			close(release)
			t.Fatal("verifier did not probe both backends concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if liveCalls.Load() != 2 {
		t.Fatalf("live probe count = %d, want 2", liveCalls.Load())
	}
}
