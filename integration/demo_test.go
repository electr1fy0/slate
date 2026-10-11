package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/ayush/slate/internal/clusterstate"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/continuum"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// This is a real HTTP rehearsal. It uses shared memory, not AWS or a simulated
// DynamoDB service; the separate transactional DynamoDB tests verify that path.
func TestExamReceiptSurvivesResponseLossAndBackendRecovery(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	settings := config.Defaults()
	settings.RoleFile = ""
	settings.AdminToken = "integration-control-token"
	settings.BootstrapTeacherEmail = "faculty@continuum.test"
	settings.BootstrapTeacherPass = "Faculty-Demo-2026!"
	settings.LoadThreshold = 1e9
	settings.DiskThresholdPct = 101
	demo, err := continuum.StartLocalDemo(ctx, settings, continuum.LocalDemoConfig{FrontendAddr: "127.0.0.1:0", BackendAAddr: "127.0.0.1:0", BackendBAddr: "127.0.0.1:0", ProbeMillis: 100, AdminToken: settings.AdminToken})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		end, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := demo.Close(end); err != nil {
			t.Error(err)
		}
	})
	// The router has its own 8-second upstream bound. Allow its response to
	// arrive even when race instrumentation slows the bcrypt registration.
	client := &http.Client{Timeout: 12 * time.Second}
	request := func(base, method, path, token string, payload any, headers map[string]string) (int, map[string]any) {
		t.Helper()
		var body io.Reader
		if payload != nil {
			data, e := json.Marshal(payload)
			if e != nil {
				t.Fatal(e)
			}
			body = bytes.NewReader(data)
		}
		req, e := http.NewRequest(method, base+path, body)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		data, e := io.ReadAll(resp.Body)
		if e != nil {
			t.Fatal(e)
		}
		out := map[string]any{}
		_ = json.Unmarshal(data, &out)
		return resp.StatusCode, out
	}
	code, _ := request(demo.PublicURL, "GET", "/continuum/status", "", nil, nil)
	if code != http.StatusForbidden {
		t.Fatalf("unprotected fault console: %d", code)
	}
	// Authentication correctness has separate tests. A cheap fixture hash
	// keeps race-instrumented password creation from consuming the router's
	// real upstream timeout while this test exercises assessment recovery.
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("Student-Demo-2026!"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	student := &model.User{UserID: "demo-student", Email: "student@continuum.test", Name: "Demo student", Role: "student"}
	if err := demo.Store.CreateUser(store.WithBootstrap(ctx), student, string(passwordHash)); err != nil {
		t.Fatal(err)
	}
	code, registered := request(demo.PublicURL, "POST", "/auth/login", "", map[string]any{"email": student.Email, "password": "Student-Demo-2026!"}, nil)
	if code != 200 {
		t.Fatalf("login: %d %+v", code, registered)
	}
	token := registered["access_token"].(string)
	studentID := registered["user"].(map[string]any)["user_id"].(string)
	code, _ = request(demo.PublicURL, "GET", "/exam/cs101-midterm", token, nil, nil)
	if code != 200 {
		t.Fatalf("start exam: %d", code)
	}
	if _, err = demo.Controller.Action(ctx, continuum.ActionRequest{Action: "lost_response"}); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"request_id": "integration-receipt-1", "answers": map[string]int{"0": 1, "1": 0}}
	code, _ = request(demo.PublicURL, "POST", "/exam/cs101-midterm/submit", token, payload, nil)
	if code != 502 && code != 503 {
		t.Fatalf("fault must remove successful response: %d", code)
	}
	canonical, err := demo.Store.GetSubmission(ctx, "cs101-midterm", studentID)
	if err != nil || canonical == nil || canonical.RequestID != "integration-receipt-1" {
		t.Fatalf("commit wasn't durable within local store: %+v, %v", canonical, err)
	}
	// The receipt remains replayable after the teacher closes the exam.
	teacherWriter := clusterstate.WithWriter(ctx, clusterstate.Writer{Backend: "A", Epoch: 1})
	_, err = demo.Store.UpdateExamWindow(teacherWriter, "cs101-midterm", nil, map[string]any{"closes_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = demo.Controller.Action(ctx, continuum.ActionRequest{Action: "crash", Backend: "A"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var status continuum.Status
	for time.Now().Before(deadline) {
		status, err = demo.Controller.Status(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if status.ActiveBackend == "B" && status.Epoch == 2 && status.Metrics.Reconciled != nil && *status.Metrics.Reconciled {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if status.ActiveBackend != "B" || status.Epoch != 2 {
		t.Fatalf("verified recovery failed: %+v", status)
	}
	code, receipt := request(demo.PublicURL, "POST", "/exam/cs101-midterm/submit", token, payload, nil)
	if code != 200 || receipt["request_id"] != "integration-receipt-1" || receipt["status"] != "duplicate" {
		t.Fatalf("lost receipt replay after exam close: %d %+v", code, receipt)
	}
	code, _ = request(demo.PublicURL, "POST", "/exam/cs101-midterm/submit", token, map[string]any{"request_id": "integration-receipt-1", "answers": map[string]int{"0": 0}}, nil)
	if code != 409 {
		t.Fatalf("request ID accepted a changed answer snapshot: %d", code)
	}
	audits, err := demo.Store.ListAuditSummaries(ctx, 10)
	if err != nil || len(audits) != 1 {
		t.Fatalf("exactly one downstream audit expected: %+v, %v", audits, err)
	}
	if count, err := demo.Store.ReconcileOutbox(ctx); err != nil || count != 0 {
		t.Fatalf("replay duplicated work: %d %v", count, err)
	}
	if _, err = demo.Controller.Action(ctx, continuum.ActionRequest{Action: "reset"}); err != nil {
		t.Fatal(err)
	}
	current, err := demo.Control.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if current.ActiveBackend != "A" || current.Epoch <= 2 {
		t.Fatalf("failback reused an epoch: %+v", current)
	}
	code, _ = request(demo.BackendURLs()["A"], "PUT", "/exam/cs101-midterm/attempt", token, map[string]any{"answers": map[string]int{"0": 0}, "expected_version": 0, "request_id": "stale-save"}, map[string]string{clusterstate.BackendHeader: "A", clusterstate.EpochHeader: "1"})
	// The window check can reject first. The direct store call verifies the
	// atomic fence independently of that application-level admission check.
	stale := clusterstate.WithWriter(ctx, clusterstate.Writer{Backend: "A", Epoch: 1})
	_, err = demo.Store.SaveAttempt(stale, "cs101-midterm", studentID, map[string]int{"0": 0}, 0, "stale-save")
	if err != clusterstate.ErrFenced {
		t.Fatalf("stale writer wasn't fenced: HTTP %d, %v", code, err)
	}
	t.Logf("same receipt recovered across A→B; final epoch=%d, exactly one canonical submission/audit", current.Epoch)
}
