package continuum

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func TestLocalDemoLostResponseReturnsCommittedReceiptOnExactRetry(t *testing.T) {
	settings := config.Defaults()
	settings.Region = "local-test"
	settings.AdminToken = "local-test-admin-token"
	settings.LoginRateLimit = 100
	settings.RegisterRateLimit = 100
	settings.LoadThreshold = 1e9
	settings.DiskThresholdPct = 101
	ctx := context.Background()
	demo, err := StartLocalDemo(ctx, settings, LocalDemoConfig{
		FrontendAddr: "127.0.0.1:0", BackendAAddr: "127.0.0.1:0", BackendBAddr: "127.0.0.1:0",
		ProbeMillis: 100, AdminToken: settings.AdminToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := demo.Close(closeCtx); err != nil {
			t.Errorf("close local demo: %v", err)
		}
	}()

	requestJSON := func(method, path string, payload any, headers map[string]string) (*http.Response, map[string]any) {
		t.Helper()
		var body bytes.Buffer
		if payload != nil {
			if err := json.NewEncoder(&body).Encode(payload); err != nil {
				t.Fatal(err)
			}
		}
		request, err := http.NewRequest(method, demo.PublicURL+path, &body)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/json")
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		bodyBytes, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("read %s %s response: %v", method, path, err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(bodyBytes, &decoded); err != nil {
			decoded = map[string]any{"body": string(bodyBytes)}
		}
		return response, decoded
	}
	admin := map[string]string{"x-admin-token": settings.AdminToken}
	if response, _ := requestJSON(http.MethodGet, "/api/continuum/status", nil, nil); response.StatusCode != http.StatusForbidden {
		t.Fatalf("unauthenticated status returned %d, want forbidden", response.StatusCode)
	}
	statusResponse, status := requestJSON(http.MethodGet, "/api/continuum/status", nil, admin)
	if statusResponse.StatusCode != http.StatusOK || status["mode"] != "local" || status["active_backend"] != "A" || status["region"] != "local-test" {
		t.Fatalf("initial local status: %d %#v", statusResponse.StatusCode, status)
	}

	// Isolate receipt recovery from race-instrumented password creation;
	// registration and hashing have separate application/auth tests.
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("hunter2hunter2"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := demo.Store.CreateUser(store.WithBootstrap(ctx), &model.User{UserID: "receipt-student", Email: "lost-receipt@example.test", Name: "Ada", Role: "student"}, string(passwordHash)); err != nil {
		t.Fatal(err)
	}
	registeredResponse, registered := requestJSON(http.MethodPost, "/api/auth/login", map[string]any{
		"email": "lost-receipt@example.test", "password": "hunter2hunter2",
	}, nil)
	if registeredResponse.StatusCode != http.StatusOK {
		t.Fatalf("login student: %d %#v", registeredResponse.StatusCode, registered)
	}
	accessToken := registered["access_token"].(string)
	studentID := registered["user"].(map[string]any)["user_id"].(string)
	if response, body := requestJSON(http.MethodPost, "/api/continuum/action", map[string]any{"action": "lost_response", "backend": "A"}, admin); response.StatusCode != http.StatusOK || body["active_backend"] != "A" {
		t.Fatalf("arm post-commit response loss: %d %#v", response.StatusCode, body)
	}
	student := map[string]string{"Authorization": "Bearer " + accessToken}
	payload := map[string]any{"answers": map[string]int{"0": 1}, "request_id": "local-lost-receipt-1"}
	lost, uncertain := requestJSON(http.MethodPost, "/api/exam/cs101-midterm/submit", payload, student)
	if lost.StatusCode != http.StatusServiceUnavailable || uncertain["detail"].(map[string]any)["code"] != "SUBMISSION_STATUS_UNKNOWN" {
		t.Fatalf("first submission did not lose only its receipt: %d %#v", lost.StatusCode, uncertain)
	}
	retry, receipt := requestJSON(http.MethodPost, "/api/exam/cs101-midterm/submit", payload, student)
	if retry.StatusCode != http.StatusOK || receipt["status"] != "duplicate" || receipt["request_id"] != "local-lost-receipt-1" {
		t.Fatalf("exact retry did not return committed receipt: %d %#v", retry.StatusCode, receipt)
	}
	stored, err := demo.Store.GetSubmission(ctx, "cs101-midterm", studentID)
	if err != nil || stored == nil || stored.RequestID != "local-lost-receipt-1" {
		t.Fatalf("canonical submission missing after lost response: %#v, %v", stored, err)
	}
	if response, result := requestJSON(http.MethodPost, "/api/continuum/action", map[string]any{"action": "reconcile"}, admin); response.StatusCode != http.StatusOK {
		t.Fatalf("reconcile action: %d %#v", response.StatusCode, result)
	}
	statusResponse, status = requestJSON(http.MethodGet, "/continuum/status", nil, admin)
	if statusResponse.StatusCode != http.StatusOK || status["metrics"].(map[string]any)["reconciled"] != true {
		t.Fatalf("reconciliation metric was not based on store outcome: %d %#v", statusResponse.StatusCode, status)
	}
}
