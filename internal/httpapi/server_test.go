package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ayush/slate/internal/auth"
	"github.com/ayush/slate/internal/boards"
	"github.com/ayush/slate/internal/chaos"
	"github.com/ayush/slate/internal/clusterstate"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
	"github.com/gorilla/websocket"
)

func newTestServer(t *testing.T, role string) (*Server, http.Handler) {
	t.Helper()
	settings := config.Defaults()
	settings.RoleFile = filepath.Join(t.TempDir(), "role")
	settings.AppRole = role
	settings.LoginRateLimit = 100
	settings.RegisterRateLimit = 100
	settings.DiskThresholdPct = 101
	settings.LoadThreshold = 1000
	backend := store.NewMemory(settings.Region)
	server := New(settings, backend)
	if err := server.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	return server, server.Handler()
}

func call(t *testing.T, handler http.Handler, method, path string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeBody(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", response.Body.String(), err)
	}
	return body
}

func registerStudent(t *testing.T, handler http.Handler, email string) (map[string]string, map[string]any) {
	t.Helper()
	response := call(t, handler, http.MethodPost, "/auth/register", map[string]any{
		"email": email, "password": "hunter2hunter2", "name": "Ada",
	}, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", response.Code, response.Body.String())
	}
	body := decodeBody(t, response)
	return map[string]string{"Authorization": "Bearer " + body["access_token"].(string)}, body
}

func TestAuthExamSubmissionAndAdminJourney(t *testing.T) {
	_, handler := newTestServer(t, "active")
	student, registered := registerStudent(t, handler, "Ada@Example.COM")
	if registered["user"].(map[string]any)["email"] != "ada@example.com" {
		t.Fatalf("email was not normalized: %#v", registered)
	}

	me := call(t, handler, http.MethodGet, "/auth/me", nil, student)
	if me.Code != http.StatusOK || me.Header().Get("x-request-id") == "" {
		t.Fatalf("me: %d headers=%v body=%s", me.Code, me.Header(), me.Body.String())
	}

	exam := call(t, handler, http.MethodGet, "/exam/cs101-midterm", nil, student)
	if exam.Code != http.StatusOK || strings.Contains(exam.Body.String(), `"answer"`) {
		t.Fatalf("exam leaked answer or failed: %d %s", exam.Code, exam.Body.String())
	}

	submission := call(t, handler, http.MethodPost, "/exam/cs101-midterm/submit", map[string]any{
		"answers": map[string]int{"0": 1}, "request_id": "request-1",
	}, student)
	if submission.Code != http.StatusOK || decodeBody(t, submission)["status"] != "committed" {
		t.Fatalf("submit: %d %s", submission.Code, submission.Body.String())
	}
	duplicate := call(t, handler, http.MethodPost, "/exam/cs101-midterm/submit", map[string]any{
		"answers": map[string]int{"0": 1}, "request_id": "request-1",
	}, student)
	if duplicate.Code != http.StatusOK || decodeBody(t, duplicate)["status"] != "duplicate" {
		t.Fatalf("duplicate: %d %s", duplicate.Code, duplicate.Body.String())
	}
	conflict := call(t, handler, http.MethodPost, "/exam/cs101-midterm/submit", map[string]any{
		"answers": map[string]int{"0": 0}, "request_id": "request-2",
	}, student)
	if conflict.Code != http.StatusConflict {
		t.Fatalf("conflict: %d %s", conflict.Code, conflict.Body.String())
	}

	result := call(t, handler, http.MethodGet, "/exam/cs101-midterm/result", nil, student)
	resultBody := decodeBody(t, result)
	if result.Code != http.StatusOK || resultBody["score"] != float64(1) || len(resultBody["review"].([]any)) != 5 {
		t.Fatalf("result: %d %#v", result.Code, resultBody)
	}

	admin := map[string]string{"x-admin-token": config.Defaults().AdminToken}
	outbox := call(t, handler, http.MethodGet, "/admin/outbox", nil, admin)
	if outbox.Code != http.StatusOK || !strings.Contains(outbox.Body.String(), "request-1") {
		t.Fatalf("outbox: %d %s", outbox.Code, outbox.Body.String())
	}
	status := call(t, handler, http.MethodGet, "/admin/status", nil, admin)
	if status.Code != http.StatusOK || decodeBody(t, status)["role"] != "active" {
		t.Fatalf("status: %d %s", status.Code, status.Body.String())
	}
}

func TestSeedDoesNotOverwriteExistingExamOnRestart(t *testing.T) {
	settings := config.Defaults()
	settings.RoleFile = filepath.Join(t.TempDir(), "role")
	backend := store.NewMemory(settings.Region)
	server := New(settings, backend)
	ctx := context.Background()
	if err := server.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	initial, err := backend.GetExamMeta(ctx, "cs101-midterm")
	if err != nil || initial == nil || initial.DurationMinutes != 15 || initial.MaxAttempts != 1 {
		t.Fatalf("fresh demo should have a server-enforced 15-minute attempt: %#v, %v", initial, err)
	}
	const facultyClose = "2026-11-01T12:00:00Z"
	if err := backend.EnsureExam(ctx, "cs101-midterm", "Faculty-edited Cloud CAT", []model.Question{{Question: "Updated by faculty", Options: []string{"A", "B"}, Answer: 0}}, map[string]any{"closes_at": facultyClose, "duration_minutes": 10}); err != nil {
		t.Fatal(err)
	}
	if err := server.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	meta, err := backend.GetExamMeta(ctx, "cs101-midterm")
	if err != nil || meta == nil || meta.Title != "Faculty-edited Cloud CAT" || meta.ClosesAt != facultyClose || meta.DurationMinutes != 10 {
		t.Fatalf("restart reset exam metadata: %#v, %v", meta, err)
	}
	exam, err := backend.GetExam(ctx, "cs101-midterm", true)
	if err != nil || exam == nil || len(exam.Questions) != 1 || exam.Questions[0].Question != "Updated by faculty" || exam.Questions[0].Answer != 0 {
		t.Fatalf("restart reset exam questions: %#v, %v", exam, err)
	}
}

func TestLostSubmissionResponseReturnsDurableReceiptOnExactRetry(t *testing.T) {
	server, handler := newTestServer(t, "active")
	student, _ := registerStudent(t, handler, "uncertain@example.com")
	admin := map[string]string{"x-admin-token": server.Settings.AdminToken}
	armed := call(t, handler, http.MethodPost, "/admin/chaos", map[string]any{"drop_response_once": true}, admin)
	if armed.Code != http.StatusOK || decodeBody(t, armed)["chaos"].(map[string]any)["drop_response_once"] != true {
		t.Fatalf("drop-response fault was not armed: %d %s", armed.Code, armed.Body.String())
	}
	payload := map[string]any{"answers": map[string]int{"0": 1}, "request_id": "lost-response-1"}
	lost := call(t, handler, http.MethodPost, "/exam/cs101-midterm/submit", payload, student)
	if lost.Code != http.StatusServiceUnavailable || decodeBody(t, lost)["detail"].(map[string]any)["code"] != "SUBMISSION_STATUS_UNKNOWN" {
		t.Fatalf("post-commit response fault = %d %s", lost.Code, lost.Body.String())
	}
	retry := call(t, handler, http.MethodPost, "/exam/cs101-midterm/submit", payload, student)
	if retry.Code != http.StatusOK {
		t.Fatalf("exact retry: %d %s", retry.Code, retry.Body.String())
	}
	receipt := decodeBody(t, retry)
	if receipt["status"] != "duplicate" || receipt["committed_at"] == nil || receipt["request_id"] != "lost-response-1" {
		t.Fatalf("retry did not return the durable receipt: %#v", receipt)
	}
	result := call(t, handler, http.MethodGet, "/exam/cs101-midterm/result", nil, student)
	resultBody := decodeBody(t, result)
	if result.Code != http.StatusOK || resultBody["committed_at"] != receipt["committed_at"] {
		t.Fatalf("GET result receipt timestamp = %d %#v, replay receipt = %#v", result.Code, resultBody, receipt)
	}
	if submissions, err := server.Store.ListSubmissions(context.Background(), "cs101-midterm", 10); err != nil || len(submissions) != 1 {
		t.Fatalf("logical submissions = %d, %v", len(submissions), err)
	}
	if outbox, err := server.Store.ListOutbox(context.Background(), 10); err != nil || len(outbox) != 1 {
		t.Fatalf("outbox entries = %d, %v", len(outbox), err)
	}
	changed := call(t, handler, http.MethodPost, "/exam/cs101-midterm/submit", map[string]any{"answers": map[string]int{"0": 0}, "request_id": "lost-response-1"}, student)
	if changed.Code != http.StatusConflict || decodeBody(t, changed)["detail"].(map[string]any)["code"] != "REQUEST_ID_CONFLICT" {
		t.Fatalf("same ID with changed payload = %d %s", changed.Code, changed.Body.String())
	}
}

type apiFence struct {
	mu     sync.RWMutex
	writer clusterstate.Writer
	paused bool
}

func (f *apiFence) WithFence(_ context.Context, writer clusterstate.Writer, fn func() error) error {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.paused || writer != f.writer {
		return clusterstate.ErrFenced
	}
	return fn()
}

func TestFencingHeadersAreValidatedAndPassedToMemoryStore(t *testing.T) {
	settings := config.Defaults()
	settings.FencingEnabled = true
	settings.BackendID = "A"
	settings.RoleFile = filepath.Join(t.TempDir(), "role")
	settings.DiskThresholdPct = 101
	settings.LoadThreshold = 1000
	backend := store.NewMemory(settings.Region)
	guard := &apiFence{writer: clusterstate.Writer{Backend: "A", Epoch: 7}}
	backend.SetFenceGuard(guard)
	server := New(settings, backend)
	if err := server.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	writerContext := clusterstate.WithWriter(context.Background(), clusterstate.Writer{Backend: "A", Epoch: 7})
	user, err := server.Auth.CreateUser(writerContext, "fenced@example.com", "hunter2hunter2", "Ada", "student")
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := server.Auth.IssueTokens(user)
	if err != nil {
		t.Fatal(err)
	}
	student := map[string]string{"Authorization": "Bearer " + tokens["access_token"].(string)}
	handler := server.Handler()
	for _, headers := range []map[string]string{
		student,
		{"Authorization": student["Authorization"], clusterstate.BackendHeader: "B", clusterstate.EpochHeader: "7"},
		{"Authorization": student["Authorization"], clusterstate.BackendHeader: "A", clusterstate.EpochHeader: "0"},
	} {
		response := call(t, handler, http.MethodGet, "/exam/cs101-midterm", nil, headers)
		if response.Code != http.StatusServiceUnavailable || decodeBody(t, response)["detail"].(map[string]any)["code"] != "WRITER_FENCED" {
			t.Fatalf("invalid writer headers %v: %d %s", headers, response.Code, response.Body.String())
		}
	}
	if live := call(t, handler, http.MethodGet, "/health/live", nil, nil); live.Code != http.StatusOK {
		t.Fatalf("liveness must not require writer headers: %d", live.Code)
	}
	writerHeaders := map[string]string{"Authorization": student["Authorization"], clusterstate.BackendHeader: "A", clusterstate.EpochHeader: "7"}
	if response := call(t, handler, http.MethodGet, "/exam/cs101-midterm", nil, writerHeaders); response.Code != http.StatusOK {
		t.Fatalf("valid writer metadata: %d %s", response.Code, response.Body.String())
	}
	guard.mu.Lock()
	guard.writer = clusterstate.Writer{Backend: "B", Epoch: 8}
	guard.mu.Unlock()
	response := call(t, handler, http.MethodPost, "/exam/cs101-midterm/submit", map[string]any{"answers": map[string]int{"0": 1}, "request_id": "stale-writer"}, writerHeaders)
	if response.Code != http.StatusServiceUnavailable || decodeBody(t, response)["detail"].(map[string]any)["code"] != "WRITER_FENCED" {
		t.Fatalf("stale transactional writer = %d %s", response.Code, response.Body.String())
	}
}

func TestFencingCoversRegistrationAdminAndTeacherExamMetadata(t *testing.T) {
	settings := config.Defaults()
	settings.FencingEnabled = true
	settings.BackendID = "A"
	settings.RoleFile = filepath.Join(t.TempDir(), "role")
	settings.DiskThresholdPct = 101
	settings.LoadThreshold = 1000
	backend := store.NewMemory(settings.Region)
	guard := &apiFence{writer: clusterstate.Writer{Backend: "A", Epoch: 7}}
	backend.SetFenceGuard(guard)
	server := New(settings, backend)
	if err := server.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	activeCtx := clusterstate.WithWriter(context.Background(), clusterstate.Writer{Backend: "A", Epoch: 7})
	teacher, err := server.Auth.CreateUser(activeCtx, "fenced-teacher@example.com", "hunter2hunter2", "Teacher", auth.RoleTeacher)
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := server.Auth.IssueTokens(teacher)
	if err != nil {
		t.Fatal(err)
	}
	teacherHeaders := map[string]string{
		"Authorization":            "Bearer " + tokens["access_token"].(string),
		clusterstate.BackendHeader: "A", clusterstate.EpochHeader: "7",
	}
	handler := server.Handler()
	activeTitle := "Current teacher edit"
	if response := call(t, handler, http.MethodPatch, "/exam/cs101-midterm", map[string]any{"title": activeTitle}, teacherHeaders); response.Code != http.StatusOK {
		t.Fatalf("active teacher metadata update = %d %s", response.Code, response.Body.String())
	}

	guard.mu.Lock()
	guard.writer = clusterstate.Writer{Backend: "B", Epoch: 8}
	guard.mu.Unlock()
	staleHeaders := map[string]string{
		"Authorization":            "Bearer " + tokens["access_token"].(string),
		clusterstate.BackendHeader: "A", clusterstate.EpochHeader: "7",
	}
	response := call(t, handler, http.MethodPatch, "/exam/cs101-midterm", map[string]any{"title": "Stale teacher edit"}, staleHeaders)
	if response.Code != http.StatusServiceUnavailable || decodeBody(t, response)["detail"].(map[string]any)["code"] != "WRITER_FENCED" {
		t.Fatalf("stale teacher update = %d %s", response.Code, response.Body.String())
	}
	meta, err := backend.GetExamMeta(context.Background(), "cs101-midterm")
	if err != nil || meta == nil || meta.Title != activeTitle {
		t.Fatalf("stale teacher update persisted: %#v, %v", meta, err)
	}
	response = call(t, handler, http.MethodPost, "/auth/register", map[string]any{
		"email": "stale-register@example.com", "password": "hunter2hunter2", "name": "Stale",
	}, staleHeaders)
	if response.Code != http.StatusServiceUnavailable || decodeBody(t, response)["detail"].(map[string]any)["code"] != "WRITER_FENCED" {
		t.Fatalf("stale registration = %d %s", response.Code, response.Body.String())
	}
	if id, err := backend.FindUserIDByEmail(context.Background(), "stale-register@example.com"); err != nil || id != "" {
		t.Fatalf("stale registration persisted user ID %q, %v", id, err)
	}
	response = call(t, handler, http.MethodPost, "/admin/users", map[string]any{
		"email": "headerless-admin@example.com", "password": "hunter2hunter2", "name": "Admin",
	}, map[string]string{"X-Admin-Token": settings.AdminToken})
	if response.Code != http.StatusServiceUnavailable || decodeBody(t, response)["detail"].(map[string]any)["code"] != "WRITER_FENCED" {
		t.Fatalf("headerless admin mutation = %d %s", response.Code, response.Body.String())
	}
	adminHeaders := map[string]string{"X-Admin-Token": settings.AdminToken}
	if response := call(t, handler, http.MethodPost, "/admin/chaos", map[string]any{"latency_ms": 0}, adminHeaders); response.Code != http.StatusOK {
		t.Fatalf("admin ephemeral chaos control should not require writer headers: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodPost, "/admin/chaos/reset", nil, adminHeaders); response.Code != http.StatusOK {
		t.Fatalf("admin ephemeral chaos reset should not require writer headers: %d %s", response.Code, response.Body.String())
	}
}

func TestDBStallFailsExamWritesWhileLivenessStaysHealthy(t *testing.T) {
	server, handler := newTestServer(t, "active")
	_, _ = registerStudent(t, handler, "db-stall@example.com")
	stalled := true
	server.Chaos.Configure(chaos.Patch{DBStall: &stalled})
	live := call(t, handler, http.MethodGet, "/health/live", nil, nil)
	if live.Code != http.StatusOK {
		t.Fatalf("liveness changed under db stall: %d", live.Code)
	}
	writes := []struct{ method, path string }{
		{http.MethodGet, "/exam/cs101-midterm"},
		{http.MethodGet, "/exam/cs101-midterm/attempt"},
		{http.MethodPut, "/exam/cs101-midterm/attempt"},
		{http.MethodPost, "/exam/cs101-midterm/submit"},
		{http.MethodPost, "/exam/cs101-midterm/attempt/submit"},
	}
	for _, route := range writes {
		response := call(t, handler, route.method, route.path, map[string]any{"answers": map[string]int{}, "request_id": "stalled"}, nil)
		if response.Code != http.StatusServiceUnavailable || decodeBody(t, response)["detail"].(map[string]any)["code"] != "DATABASE_STALLED" {
			t.Fatalf("database stall %s %s = %d %s", route.method, route.path, response.Code, response.Body.String())
		}
	}
}

func TestExamAuthoringLifecycleAndStandbyPromotion(t *testing.T) {
	_, handler := newTestServer(t, "active")
	admin := map[string]string{"x-admin-token": config.Defaults().AdminToken}
	created := call(t, handler, http.MethodPost, "/exam", map[string]any{
		"id": "Physics 101", "title": "Physics Quiz",
		"questions":        []map[string]any{{"question": "Force?", "options": []string{"Mass", "Acceleration"}, "answer": 1}},
		"duration_minutes": 10,
	}, admin)
	if created.Code != http.StatusCreated || decodeBody(t, created)["id"] != "physics-101" {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	patched := call(t, handler, http.MethodPatch, "/exam/physics-101", map[string]any{"title": "Mechanics"}, admin)
	if patched.Code != http.StatusOK || decodeBody(t, patched)["title"] != "Mechanics" {
		t.Fatalf("patch: %d %s", patched.Code, patched.Body.String())
	}
	closed := call(t, handler, http.MethodPost, "/exam/physics-101/close", nil, admin)
	if closed.Code != http.StatusOK || decodeBody(t, closed)["closed_at"] == nil {
		t.Fatalf("close: %d %s", closed.Code, closed.Body.String())
	}
	deleted := call(t, handler, http.MethodDelete, "/exam/physics-101", nil, admin)
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}

	_, standbyHandler := newTestServer(t, "standby")
	student, _ := registerStudent(t, standbyHandler, "standby@example.com")
	blocked := call(t, standbyHandler, http.MethodGet, "/exam/cs101-midterm", nil, student)
	if blocked.Code != http.StatusServiceUnavailable {
		t.Fatalf("standby served exam: %d", blocked.Code)
	}
	promoted := call(t, standbyHandler, http.MethodPost, "/admin/promote", nil, admin)
	if promoted.Code != http.StatusOK || decodeBody(t, promoted)["role"] != "active" {
		t.Fatalf("promote: %d %s", promoted.Code, promoted.Body.String())
	}
}

func TestHealthCORSValidationAndAuthenticationErrors(t *testing.T) {
	_, handler := newTestServer(t, "active")
	live := call(t, handler, http.MethodGet, "/health/live", nil, map[string]string{"x-request-id": "known-id"})
	if live.Code != http.StatusOK || live.Header().Get("x-request-id") != "known-id" {
		t.Fatalf("live: %d headers=%v", live.Code, live.Header())
	}
	deep := call(t, handler, http.MethodGet, "/health/deep", nil, nil)
	if deep.Code != http.StatusOK || decodeBody(t, deep)["healthy"] != true {
		t.Fatalf("deep: %d %s", deep.Code, deep.Body.String())
	}
	unauthorized := call(t, handler, http.MethodGet, "/auth/me", nil, nil)
	if unauthorized.Code != http.StatusUnauthorized || decodeBody(t, unauthorized)["detail"] != "authentication required" {
		t.Fatalf("unauthorized: %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	invalid := call(t, handler, http.MethodPost, "/auth/register", map[string]any{"email": "bad", "password": "short"}, nil)
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("validation: %d %s", invalid.Code, invalid.Body.String())
	}
	preflight := call(t, handler, http.MethodOptions, "/auth/login", nil, map[string]string{"Origin": "http://localhost:3000"})
	if preflight.Code != http.StatusOK || preflight.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("preflight: %d headers=%v", preflight.Code, preflight.Header())
	}
}

func TestHandlerBoardUpgradePassesThroughChaosMiddleware(t *testing.T) {
	server, handler := newTestServer(t, "active")
	_, registered := registerStudent(t, handler, "board@example.com")
	server.WS = boards.NewHTTPHandler(server.Boards, func(ctx context.Context, token string) (boards.Identity, error) {
		user, err := server.Auth.CurrentUser(ctx, "Bearer "+token)
		if err != nil {
			return boards.Identity{}, boards.ErrInvalidToken
		}
		role := user.Role
		if role != "student" {
			role = "teacher"
		}
		return boards.Identity{Name: user.Name, Role: role}, nil
	}, server.Settings.CORSOrigins, server.Settings.WSMaxMessageBytes)
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()
	parsed, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Scheme = "ws"
	parsed.Path = "/ws/board/room"
	query := parsed.Query()
	query.Set("token", registered["access_token"].(string))
	parsed.RawQuery = query.Encode()
	conn, _, err := websocket.DefaultDialer.Dial(parsed.String(), http.Header{"Origin": []string{"http://localhost:3000"}})
	if err != nil {
		t.Fatalf("websocket upgrade: %v", err)
	}
	defer conn.Close()
	if err := conn.WriteJSON(map[string]any{"type": "join"}); err != nil {
		t.Fatal(err)
	}
	var frame map[string]any
	if err := conn.ReadJSON(&frame); err != nil {
		t.Fatal(err)
	}
	if frame["type"] != "init" {
		t.Fatalf("join frame = %#v", frame)
	}
}
