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
	"testing"

	"github.com/ayush/slate/internal/boards"
	"github.com/ayush/slate/internal/config"
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
