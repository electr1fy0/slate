package continuum

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type qualityStateReader func(context.Context) (ControlState, error)

func (read qualityStateReader) Get(ctx context.Context) (ControlState, error) { return read(ctx) }

func TestQualityRouterSetupAndDeadline(t *testing.T) {
	response := httptest.NewRecorder()
	(&Router{}).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != 503 {
		t.Fatal("unconfigured router did not fail closed")
	}
	control := qualityStateReader(func(ctx context.Context) (ControlState, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("control read has no deadline")
		}
		return ControlState{}, context.DeadlineExceeded
	})
	(&Router{Control: control, Timeout: time.Second}).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}

func TestQualityRouterDoesNotFollowRedirectsAndAllowsEmptyBodies(t *testing.T) {
	var hits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer backend.Close()
	control, _ := NewMemoryControl(ControlState{ActiveBackend: "A", Epoch: 1})
	router := &Router{Control: control, BackendURLs: map[string]string{"A": backend.URL}}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Body = nil
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 307 || hits.Load() != 0 {
		t.Fatal("router followed backend redirect")
	}
	if _, err := EncodeBody(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 1000))), true, 4); err == nil {
		t.Fatal("oversized base64 accepted")
	}
}

func TestQualityProbesRejectRedirectsAndTrailingData(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, `{"healthy":true}`) }))
	defer target.Close()
	for _, body := range []string{`{"healthy":true} {}`, `{"healthy":true} garbage`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
		result := HTTPBackendProbe(nil, map[string]string{"A": server.URL}, time.Second)(context.Background(), "A")
		server.Close()
		if result.Ready {
			t.Fatal("accepted malformed health response")
		}
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer redirect.Close()
	if result := HTTPBackendProbe(nil, map[string]string{"A": redirect.URL}, time.Second)(context.Background(), "A"); result.Ready || result.Liveness {
		t.Fatal("followed health redirect")
	}
}
