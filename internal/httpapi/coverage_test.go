package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ayush/slate/internal/boards"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/exams"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
	"github.com/golang-jwt/jwt/v5"
)

// coverageBackend keeps the real in-memory semantics while making transport
// failures deterministic. The HTTP package is intentionally tested through
// the same Backend interface used by the production server.
type coverageBackend struct {
	*store.MemoryStore

	createUserErr      error
	getUserErr         error
	getUserNil         bool
	findEmailErr       error
	ensureExamErr      error
	getExamErr         error
	getExamMetaErr     error
	listExamsErr       error
	deleteExamErr      error
	listSubmissionsErr error
	listOutboxErr      error
	getSubmissionErr   error
	heartbeatErr       error
	recordAttemptErr   error
	attemptDeadlineErr error
	getAttemptErr      error
	saveAttemptErr     error
	submitAttemptErr   error
	createDraftErr     error
	getDraftErr        error
	listDraftsErr      error
	listDraftsNil      bool
	updateDraftErr     error
	deleteDraftErr     error
	publishDraftErr    error
	duplicateQuizErr   error
	updateExamErr      error
}

func (b *coverageBackend) CreateUser(ctx context.Context, user *model.User, hash string) error {
	if b.createUserErr != nil {
		return b.createUserErr
	}
	return b.MemoryStore.CreateUser(ctx, user, hash)
}

func (b *coverageBackend) GetUser(ctx context.Context, id string) (*model.User, error) {
	if b.getUserErr != nil {
		return nil, b.getUserErr
	}
	if b.getUserNil {
		return nil, nil
	}
	return b.MemoryStore.GetUser(ctx, id)
}

func (b *coverageBackend) FindUserIDByEmail(ctx context.Context, email string) (string, error) {
	if b.findEmailErr != nil {
		return "", b.findEmailErr
	}
	return b.MemoryStore.FindUserIDByEmail(ctx, email)
}

func (b *coverageBackend) EnsureExam(ctx context.Context, id, title string, questions []model.Question, window map[string]any) error {
	if b.ensureExamErr != nil {
		return b.ensureExamErr
	}
	return b.MemoryStore.EnsureExam(ctx, id, title, questions, window)
}

func (b *coverageBackend) EnsureExamIfMissing(ctx context.Context, id, title string, questions []model.Question, window map[string]any) error {
	if b.ensureExamErr != nil {
		return b.ensureExamErr
	}
	return b.MemoryStore.EnsureExamIfMissing(ctx, id, title, questions, window)
}

func (b *coverageBackend) GetExam(ctx context.Context, id string, include bool) (*model.Exam, error) {
	if b.getExamErr != nil {
		return nil, b.getExamErr
	}
	return b.MemoryStore.GetExam(ctx, id, include)
}

func (b *coverageBackend) GetExamMeta(ctx context.Context, id string) (*model.ExamMeta, error) {
	if b.getExamMetaErr != nil {
		return nil, b.getExamMetaErr
	}
	return b.MemoryStore.GetExamMeta(ctx, id)
}

func (b *coverageBackend) ListExams(ctx context.Context, limit int) ([]model.ExamMeta, error) {
	if b.listExamsErr != nil {
		return nil, b.listExamsErr
	}
	return b.MemoryStore.ListExams(ctx, limit)
}

func (b *coverageBackend) DeleteExam(ctx context.Context, id string) (int, error) {
	if b.deleteExamErr != nil {
		return 0, b.deleteExamErr
	}
	return b.MemoryStore.DeleteExam(ctx, id)
}

func (b *coverageBackend) ListSubmissions(ctx context.Context, examID string, limit int) ([]model.Submission, error) {
	if b.listSubmissionsErr != nil {
		return nil, b.listSubmissionsErr
	}
	return b.MemoryStore.ListSubmissions(ctx, examID, limit)
}

func (b *coverageBackend) ListOutbox(ctx context.Context, limit int) ([]model.OutboxEntry, error) {
	if b.listOutboxErr != nil {
		return nil, b.listOutboxErr
	}
	return b.MemoryStore.ListOutbox(ctx, limit)
}

func (b *coverageBackend) GetSubmission(ctx context.Context, examID, studentID string) (*model.Submission, error) {
	if b.getSubmissionErr != nil {
		return nil, b.getSubmissionErr
	}
	return b.MemoryStore.GetSubmission(ctx, examID, studentID)
}

func (b *coverageBackend) HeartbeatProbe(ctx context.Context, region string) (bool, float64, error) {
	if b.heartbeatErr != nil {
		return false, 0, b.heartbeatErr
	}
	return b.MemoryStore.HeartbeatProbe(ctx, region)
}

func (b *coverageBackend) RecordAttemptStart(ctx context.Context, examID, studentID string) (*model.Attempt, error) {
	if b.recordAttemptErr != nil {
		return nil, b.recordAttemptErr
	}
	return b.MemoryStore.RecordAttemptStart(ctx, examID, studentID)
}

func (b *coverageBackend) AttemptDeadline(ctx context.Context, examID, studentID string) (*model.Attempt, *int64, error) {
	if b.attemptDeadlineErr != nil {
		return nil, nil, b.attemptDeadlineErr
	}
	return b.MemoryStore.AttemptDeadline(ctx, examID, studentID)
}

func (b *coverageBackend) GetStudentAttempt(ctx context.Context, examID, studentID string) (*model.Attempt, error) {
	if b.getAttemptErr != nil {
		return nil, b.getAttemptErr
	}
	return b.MemoryStore.GetStudentAttempt(ctx, examID, studentID)
}

func (b *coverageBackend) SaveAttempt(ctx context.Context, examID, studentID string, answers map[string]int, version int, requestID string) (*model.Attempt, error) {
	if b.saveAttemptErr != nil {
		return nil, b.saveAttemptErr
	}
	return b.MemoryStore.SaveAttempt(ctx, examID, studentID, answers, version, requestID)
}

func (b *coverageBackend) SaveAttemptForAttempt(ctx context.Context, examID, studentID string, answers map[string]int, version int, requestID string, attemptNumber int) (*model.Attempt, error) {
	if b.saveAttemptErr != nil {
		return nil, b.saveAttemptErr
	}
	return b.MemoryStore.SaveAttemptForAttempt(ctx, examID, studentID, answers, version, requestID, attemptNumber)
}

func (b *coverageBackend) SubmitAttempt(ctx context.Context, examID, studentID, name string, answers map[string]int, requestID string) (model.SubmissionResult, error) {
	if b.submitAttemptErr != nil {
		return model.SubmissionResult{}, b.submitAttemptErr
	}
	return b.MemoryStore.SubmitAttempt(ctx, examID, studentID, name, answers, requestID)
}

func (b *coverageBackend) CreateQuizDraft(ctx context.Context, draft *model.QuizDraft) (*model.QuizDraft, error) {
	if b.createDraftErr != nil {
		return nil, b.createDraftErr
	}
	return b.MemoryStore.CreateQuizDraft(ctx, draft)
}

func (b *coverageBackend) GetQuizDraft(ctx context.Context, id string) (*model.QuizDraft, error) {
	if b.getDraftErr != nil {
		return nil, b.getDraftErr
	}
	return b.MemoryStore.GetQuizDraft(ctx, id)
}

func (b *coverageBackend) ListQuizDrafts(ctx context.Context, ownerID string, limit int) ([]model.QuizDraft, error) {
	if b.listDraftsErr != nil {
		return nil, b.listDraftsErr
	}
	if b.listDraftsNil {
		return nil, nil
	}
	return b.MemoryStore.ListQuizDrafts(ctx, ownerID, limit)
}

func (b *coverageBackend) UpdateQuizDraft(ctx context.Context, ownerID string, draft *model.QuizDraft) (*model.QuizDraft, error) {
	if b.updateDraftErr != nil {
		return nil, b.updateDraftErr
	}
	return b.MemoryStore.UpdateQuizDraft(ctx, ownerID, draft)
}

func (b *coverageBackend) DeleteQuizDraft(ctx context.Context, id, ownerID string) (bool, error) {
	if b.deleteDraftErr != nil {
		return false, b.deleteDraftErr
	}
	return b.MemoryStore.DeleteQuizDraft(ctx, id, ownerID)
}

func (b *coverageBackend) PublishQuizDraft(ctx context.Context, id, ownerID string) (*model.ExamMeta, error) {
	if b.publishDraftErr != nil {
		return nil, b.publishDraftErr
	}
	return b.MemoryStore.PublishQuizDraft(ctx, id, ownerID)
}

func (b *coverageBackend) DuplicateQuiz(ctx context.Context, sourceID, ownerID, requestedID, title string) (*model.QuizDraft, error) {
	if b.duplicateQuizErr != nil {
		return nil, b.duplicateQuizErr
	}
	return b.MemoryStore.DuplicateQuiz(ctx, sourceID, ownerID, requestedID, title)
}

func (b *coverageBackend) UpdateExamWindow(ctx context.Context, id string, title *string, window map[string]any) (*model.ExamMeta, error) {
	if b.updateExamErr != nil {
		return nil, b.updateExamErr
	}
	return b.MemoryStore.UpdateExamWindow(ctx, id, title, window)
}

func newCoverageServer(t *testing.T) (*Server, *coverageBackend, http.Handler) {
	t.Helper()
	settings := config.Defaults()
	settings.RoleFile = filepath.Join(t.TempDir(), "role")
	settings.LoginRateLimit = 1000
	settings.RegisterRateLimit = 1000
	settings.DiskThresholdPct = 101
	settings.LoadThreshold = 1000
	backend := &coverageBackend{MemoryStore: store.NewMemory(settings.Region)}
	server := New(settings, backend)
	server.Now = func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) }
	// JWT validation uses wall-clock time; keep issuance close to it while
	// business-rule tests use the deterministic server clock below.
	server.Auth.Now = time.Now
	server.Exams.Now = server.Now
	server.Classrooms.Now = server.Now
	if err := server.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	return server, backend, server.Handler()
}

func authHeader(t *testing.T, handler http.Handler, path, email, password string) map[string]string {
	t.Helper()
	response := call(t, handler, http.MethodPost, path, map[string]any{"email": email, "password": password}, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", email, response.Code, response.Body.String())
	}
	return map[string]string{"Authorization": "Bearer " + decodeBody(t, response)["access_token"].(string)}
}

func rawCall(t *testing.T, handler http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestHTTPAPICoverageAuthAndCoreEdges(t *testing.T) {
	server, backend, handler := newCoverageServer(t)
	admin := map[string]string{"x-admin-token": server.Settings.AdminToken}

	root := call(t, handler, http.MethodGet, "/", nil, nil)
	if root.Code != http.StatusOK || !strings.Contains(root.Body.String(), "CONTINUUM EXAM PLATFORM") {
		t.Fatalf("active index: %d %s", root.Code, root.Body.String())
	}
	wsMissing := call(t, handler, http.MethodGet, "/ws/board/no-room", nil, nil)
	if wsMissing.Code != http.StatusNotFound {
		t.Fatalf("missing websocket handler: %d", wsMissing.Code)
	}
	if got := New(config.Defaults(), &store.Store{}); got.Boards == nil {
		t.Fatal("Dynamo-backed New should configure a board persistence adapter")
	}

	badBodies := []struct {
		path string
		body any
	}{
		{"/auth/register", "not-json"},
		{"/auth/register", map[string]any{"email": "bad", "password": "12345678", "name": "n"}},
		{"/auth/register", map[string]any{"email": "a@example.com", "password": "short", "name": "n"}},
		{"/auth/register", map[string]any{"email": "a@example.com", "password": "12345678", "name": strings.Repeat("n", 61)}},
	}
	for _, tc := range badBodies {
		var response *httptest.ResponseRecorder
		if raw, ok := tc.body.(string); ok {
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			response = httptest.NewRecorder()
			handler.ServeHTTP(response, req)
		} else {
			response = call(t, handler, http.MethodPost, tc.path, tc.body, nil)
		}
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("bad body %v: %d %s", tc.body, response.Code, response.Body.String())
		}
	}
	extra := call(t, handler, http.MethodPost, "/auth/register", nil, nil)
	if extra.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty register body: %d", extra.Code)
	}

	student, registered := registerStudent(t, handler, "coverage@example.com")
	if duplicate := call(t, handler, http.MethodPost, "/auth/register", map[string]any{"email": "coverage@example.com", "password": "hunter2hunter2", "name": "Ada"}, nil); duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate registration: %d", duplicate.Code)
	}
	if invalidLogin := call(t, handler, http.MethodPost, "/auth/login", map[string]any{"email": "missing@example.com", "password": "hunter2hunter2"}, nil); invalidLogin.Code != http.StatusUnauthorized {
		t.Fatalf("invalid login: %d", invalidLogin.Code)
	}
	if missingLogin := call(t, handler, http.MethodPost, "/auth/login", map[string]any{"email": "missing@example.com"}, nil); missingLogin.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing login field: %d", missingLogin.Code)
	}
	backend.findEmailErr = errors.New("email backend down")
	if response := call(t, handler, http.MethodPost, "/auth/login", map[string]any{"email": "missing@example.com", "password": "hunter2hunter2"}, nil); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("login backend error: %d", response.Code)
	}
	backend.findEmailErr = nil

	refreshToken := registered["refresh_token"].(string)
	if refreshed := call(t, handler, http.MethodPost, "/auth/refresh", map[string]any{"refresh_token": refreshToken}, nil); refreshed.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", refreshed.Code, refreshed.Body.String())
	}
	for _, body := range []any{map[string]any{}, map[string]any{"refresh_token": "bad"}} {
		if response := call(t, handler, http.MethodPost, "/auth/refresh", body, nil); response.Code != http.StatusUnauthorized && body.(map[string]any)["refresh_token"] != nil {
			t.Fatalf("invalid refresh: %d", response.Code)
		}
	}
	if response := call(t, handler, http.MethodPost, "/auth/refresh", map[string]any{}, nil); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing refresh token: %d", response.Code)
	}
	backend.getUserNil = true
	if response := call(t, handler, http.MethodPost, "/auth/refresh", map[string]any{"refresh_token": refreshToken}, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("deleted account refresh: %d", response.Code)
	}
	backend.getUserNil = false
	backend.getUserErr = errors.New("user backend down")
	if response := call(t, handler, http.MethodGet, "/auth/me", nil, student); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("current user backend error: %d", response.Code)
	}
	backend.getUserErr = nil
	if response := call(t, handler, http.MethodGet, "/auth/me", nil, map[string]string{"Authorization": "Bearer bad"}); response.Code != http.StatusUnauthorized {
		t.Fatalf("invalid session: %d", response.Code)
	}

	backend.createUserErr = errors.New("write unavailable")
	if response := call(t, handler, http.MethodPost, "/auth/register", map[string]any{"email": "error@example.com", "password": "hunter2hunter2", "name": "Error"}, nil); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("register backend error: %d", response.Code)
	}
	backend.createUserErr = nil
	server.Auth.SetSigner(func(*jwt.Token, []byte) (string, error) { return "", errors.New("signing failed") })
	if response := call(t, handler, http.MethodPost, "/auth/register", map[string]any{"email": "sign@example.com", "password": "hunter2hunter2", "name": "Sign"}, nil); response.Code != http.StatusInternalServerError {
		t.Fatalf("register signing error: %d", response.Code)
	}
	server.Auth.SetSigner(nil)

	if response := call(t, handler, http.MethodGet, "/admin/status", nil, map[string]string{"x-admin-token": "wrong"}); response.Code != http.StatusForbidden {
		t.Fatalf("invalid admin token: %d", response.Code)
	}
	if response := call(t, handler, http.MethodPost, "/admin/users", map[string]any{"email": "bad-role@example.com", "password": "hunter2hunter2", "name": "Bad", "role": "admin"}, admin); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid admin role: %d", response.Code)
	}
	backend.createUserErr = errors.New("admin write unavailable")
	if response := call(t, handler, http.MethodPost, "/admin/users", map[string]any{"email": "admin-error@example.com", "password": "hunter2hunter2", "name": "Error"}, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("admin create backend error: %d", response.Code)
	}
	backend.createUserErr = nil
	if response := call(t, handler, http.MethodPost, "/admin/users", map[string]any{"email": "admin-user@example.com", "password": "hunter2hunter2", "name": "Admin"}, admin); response.Code != http.StatusCreated {
		t.Fatalf("admin create default role: %d", response.Code)
	}
	if response := call(t, handler, http.MethodPost, "/admin/users", map[string]any{"email": "admin-user@example.com", "password": "hunter2hunter2", "name": "Admin"}, admin); response.Code != http.StatusConflict {
		t.Fatalf("admin duplicate: %d", response.Code)
	}

	standbyServer, _, standbyHandler := newCoverageServer(t)
	standbyServer.Settings.AppRole = "standby"
	standbyServer.Roles.fallback = "standby"
	if response := call(t, standbyHandler, http.MethodGet, "/", nil, nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "STANDBY") {
		t.Fatalf("standby index: %d %s", response.Code, response.Body.String())
	}
}

func TestHTTPAPICoverageExamAndQuizEdges(t *testing.T) {
	server, backend, handler := newCoverageServer(t)
	admin := map[string]string{"x-admin-token": server.Settings.AdminToken}
	teacher := registerTeacher(t, handler, "coverage-teacher@example.com")
	student, _ := registerStudent(t, handler, "coverage-student@example.com")

	if response := call(t, handler, http.MethodGet, "/exams", nil, nil); response.Code != http.StatusOK {
		t.Fatalf("list exams: %d", response.Code)
	}
	backend.listExamsErr = errors.New("exam list unavailable")
	if response := call(t, handler, http.MethodGet, "/exams", nil, nil); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("list exams error: %d", response.Code)
	}
	backend.listExamsErr = nil

	badExamBodies := []map[string]any{
		{},
		{"title": "x"},
		{"title": "x", "questions": []any{map[string]any{"options": []string{"a", "b"}, "answer": 0}}},
		{"title": "x", "questions": []any{map[string]any{"question": "q", "options": []string{"a"}, "answer": 0}}},
		{"title": "x", "questions": []any{map[string]any{"question": "q", "options": []string{"a", ""}, "answer": 0}}},
		{"title": "x", "questions": []any{map[string]any{"question": "q", "options": []string{"a", "b"}}}},
		{"title": "x", "questions": []any{map[string]any{"question": "q", "options": []string{"a", "b"}, "answer": 2}}},
		{"title": "x", "questions": []any{map[string]any{"question": "q", "options": []string{"a", "b"}, "answer": 0, "points": 1001}}},
		{"title": "x", "questions": []any{map[string]any{"question": "q", "options": []string{"a", "b"}, "answer": 0}}, "max_attempts": 21},
		{"title": "x", "questions": []any{map[string]any{"question": "q", "options": []string{"a", "b"}, "answer": 0}}, "late_grace_minutes": -1},
		{"title": "x", "questions": []any{map[string]any{"question": "q", "options": []string{"a", "b"}, "answer": 0}}, "opens_at": "bad", "closes_at": "also-bad"},
	}
	for _, body := range badExamBodies {
		if response := call(t, handler, http.MethodPost, "/exam", body, admin); response.Code != http.StatusBadRequest && response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("bad exam %#v: %d %s", body, response.Code, response.Body.String())
		}
	}
	fullExam := map[string]any{
		"title": "Coverage Physics", "questions": []map[string]any{{"question": " Force? ", "options": []string{"Mass", "Acceleration"}, "answer": 1, "points": 2, "feedback": " F=ma "}},
		"opens_at": "2026-01-01T00:00:00Z", "closes_at": "2027-01-01T00:00:00Z", "duration_minutes": 10, "max_attempts": 2, "allow_late": true, "late_grace_minutes": 5,
	}
	created := call(t, handler, http.MethodPost, "/exam", fullExam, admin)
	if created.Code != http.StatusCreated {
		t.Fatalf("full exam create: %d %s", created.Code, created.Body.String())
	}
	if response := call(t, handler, http.MethodPost, "/exam", fullExam, admin); response.Code != http.StatusCreated {
		t.Fatalf("generated exam suffix: %d %s", response.Code, response.Body.String())
	}
	id := decodeBody(t, created)["id"].(string)
	duplicate := fullExam
	duplicate["id"] = id
	if response := call(t, handler, http.MethodPost, "/exam", duplicate, admin); response.Code != http.StatusConflict {
		t.Fatalf("duplicate exam id: %d", response.Code)
	}
	backend.getExamErr = errors.New("exam read unavailable")
	if response := call(t, handler, http.MethodPost, "/exam", map[string]any{"id": "read-error", "title": "Read Error", "questions": fullExam["questions"]}, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("create exam read error: %d", response.Code)
	}
	backend.getExamErr = nil
	backend.ensureExamErr = errors.New("exam write unavailable")
	if response := call(t, handler, http.MethodPost, "/exam", map[string]any{"id": "write-error", "title": "Write Error", "questions": fullExam["questions"]}, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("create exam write error: %d", response.Code)
	}
	backend.ensureExamErr = nil

	if response := call(t, handler, http.MethodGet, "/exam/does-not-exist", nil, student); response.Code != http.StatusNotFound {
		t.Fatalf("missing exam: %d", response.Code)
	}
	backend.getExamErr = errors.New("delivery read unavailable")
	if response := call(t, handler, http.MethodGet, "/exam/coverage-physics", nil, student); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("exam delivery error: %d", response.Code)
	}
	backend.getExamErr = nil
	if response := call(t, handler, http.MethodGet, "/exam/coverage-physics/preview", nil, teacher); response.Code != http.StatusOK {
		t.Fatalf("exam preview: %d", response.Code)
	}
	if response := call(t, handler, http.MethodGet, "/exam/does-not-exist/preview", nil, teacher); response.Code != http.StatusNotFound {
		t.Fatalf("missing preview: %d", response.Code)
	}
	backend.getExamErr = errors.New("preview read unavailable")
	if response := call(t, handler, http.MethodGet, "/exam/coverage-physics/preview", nil, teacher); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("preview error: %d", response.Code)
	}
	backend.getExamErr = nil
	if response := call(t, handler, http.MethodGet, "/exam/coverage-physics/preview", nil, student); response.Code != http.StatusForbidden {
		t.Fatalf("student preview: %d", response.Code)
	}

	patchBody := map[string]any{"title": "Updated Coverage", "opens_at": "2026-02-01T00:00:00Z", "closes_at": "2027-02-01T00:00:00Z", "duration_minutes": 20, "max_attempts": 3, "allow_late": true, "late_grace_minutes": 6}
	if response := call(t, handler, http.MethodPatch, "/exam/coverage-physics", patchBody, admin); response.Code != http.StatusOK {
		t.Fatalf("patch exam: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodPatch, "/exam/none", patchBody, admin); response.Code != http.StatusNotFound {
		t.Fatalf("missing patch: %d", response.Code)
	}
	backend.getExamMetaErr = errors.New("patch meta unavailable")
	if response := call(t, handler, http.MethodPatch, "/exam/coverage-physics", patchBody, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("patch meta error: %d", response.Code)
	}
	backend.getExamMetaErr = nil
	if response := call(t, handler, http.MethodPatch, "/exam/coverage-physics", map[string]any{"duration_minutes": 0, "max_attempts": 0, "late_grace_minutes": 0}, admin); response.Code != http.StatusOK {
		t.Fatalf("clear exam timer and attempt limits: %d %s", response.Code, response.Body.String())
	} else if body := decodeBody(t, response); body["duration_minutes"] != float64(0) {
		t.Fatalf("untimed exam response: %#v", body)
	}
	backend.updateExamErr = errors.New("patch write unavailable")
	if response := call(t, handler, http.MethodPatch, "/exam/coverage-physics", patchBody, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("patch write error: %d", response.Code)
	}
	backend.updateExamErr = nil
	if response := call(t, handler, http.MethodPost, "/exam/none/close", nil, admin); response.Code != http.StatusNotFound {
		t.Fatalf("missing close: %d", response.Code)
	}
	backend.updateExamErr = errors.New("close write unavailable")
	if response := call(t, handler, http.MethodPost, "/exam/coverage-physics/close", nil, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("close error: %d", response.Code)
	}
	backend.updateExamErr = nil
	if response := call(t, handler, http.MethodPost, "/exam/coverage-physics/close", nil, admin); response.Code != http.StatusOK {
		t.Fatalf("close exam: %d", response.Code)
	}
	if response := call(t, handler, http.MethodDelete, "/exam/none", nil, admin); response.Code != http.StatusNotFound {
		t.Fatalf("missing delete: %d", response.Code)
	}
	backend.deleteExamErr = errors.New("delete unavailable")
	if response := call(t, handler, http.MethodDelete, "/exam/coverage-physics-2", nil, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("delete error: %d", response.Code)
	}
	backend.deleteExamErr = nil

	// Durable attempt handlers: exercise validation, service errors, success,
	// duplicate submission, and the optional empty-body submit route.
	if response := call(t, handler, http.MethodGet, "/exam/cs101-midterm/attempt", nil, student); response.Code != http.StatusOK {
		t.Fatalf("get attempt: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodPut, "/exam/cs101-midterm/attempt", map[string]any{"request_id": "x"}, student); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("save missing answers: %d", response.Code)
	}
	if response := call(t, handler, http.MethodPut, "/exam/cs101-midterm/attempt", map[string]any{"answers": map[string]int{"bad": 1}, "request_id": "x"}, student); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("save invalid answers: %d", response.Code)
	}
	backend.saveAttemptErr = errors.New("save unavailable")
	if response := call(t, handler, http.MethodPut, "/exam/cs101-midterm/attempt", map[string]any{"answers": map[string]int{}, "request_id": "save-error"}, student); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("save error: %d", response.Code)
	}
	backend.saveAttemptErr = nil
	if response := call(t, handler, http.MethodPut, "/exam/cs101-midterm/attempt", map[string]any{"answers": map[string]int{"0": 1}, "expected_version": 0, "request_id": "save"}, student); response.Code != http.StatusOK {
		t.Fatalf("save success: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodPost, "/exam/cs101-midterm/attempt/submit", nil, student); response.Code != http.StatusOK {
		t.Fatalf("submit saved empty body: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodGet, "/exam/cs101-midterm/result", nil, student); response.Code != http.StatusOK {
		t.Fatalf("result after saved submit: %d", response.Code)
	}
	if response := call(t, handler, http.MethodPost, "/exam/cs101-midterm/submit", map[string]any{}, student); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("submit missing answers: %d", response.Code)
	}
	backend.getSubmissionErr = errors.New("result unavailable")
	if response := call(t, handler, http.MethodGet, "/exam/cs101-midterm/result", nil, student); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("result error: %d", response.Code)
	}
	backend.getSubmissionErr = nil
	if response := call(t, handler, http.MethodGet, "/exam/none/result", nil, student); response.Code != http.StatusNotFound {
		t.Fatalf("missing result: %d", response.Code)
	}

	// Quiz draft route errors and helper validation.
	draftPayload := map[string]any{"id": "coverage-draft", "title": "Coverage Draft", "questions": []map[string]any{{"question": "Q", "options": []string{"A", "B"}, "answer": 1}}}
	if response := call(t, handler, http.MethodPost, "/quiz/drafts", draftPayload, teacher); response.Code != http.StatusCreated {
		t.Fatalf("draft create: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodGet, "/quiz/drafts/coverage-draft", nil, teacher); response.Code != http.StatusOK {
		t.Fatalf("draft get: %d", response.Code)
	}
	if response := call(t, handler, http.MethodGet, "/quiz/drafts/missing", nil, teacher); response.Code != http.StatusNotFound {
		t.Fatalf("missing draft get: %d", response.Code)
	}
	otherTeacher := registerTeacher(t, handler, "coverage-other@example.com")
	if response := call(t, handler, http.MethodGet, "/quiz/drafts/coverage-draft", nil, otherTeacher); response.Code != http.StatusForbidden {
		t.Fatalf("draft ownership: %d", response.Code)
	}
	backend.getDraftErr = errors.New("draft read unavailable")
	if response := call(t, handler, http.MethodGet, "/quiz/drafts/coverage-draft", nil, teacher); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("draft get error: %d", response.Code)
	}
	backend.getDraftErr = nil
	backend.listDraftsErr = errors.New("draft list unavailable")
	if response := call(t, handler, http.MethodGet, "/quiz/drafts", nil, teacher); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("draft list error: %d", response.Code)
	}
	backend.listDraftsErr = nil
	if response := call(t, handler, http.MethodPost, "/quiz/drafts", map[string]any{"title": "bad", "questions": []any{}}, teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("draft validation: %d", response.Code)
	}
	backend.createDraftErr = store.ErrQuizConflict
	if response := call(t, handler, http.MethodPost, "/quiz/drafts", draftPayload, teacher); response.Code != http.StatusConflict {
		t.Fatalf("draft conflict: %d", response.Code)
	}
	backend.createDraftErr = nil
	backend.updateDraftErr = errors.New("draft update unavailable")
	if response := call(t, handler, http.MethodPatch, "/quiz/drafts/coverage-draft", draftPayload, teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("draft update error: %d", response.Code)
	}
	backend.updateDraftErr = nil
	if response := call(t, handler, http.MethodDelete, "/quiz/drafts/missing", nil, teacher); response.Code != http.StatusNotFound {
		t.Fatalf("missing draft delete: %d", response.Code)
	}
	if response := call(t, handler, http.MethodDelete, "/quiz/drafts/coverage-draft", nil, otherTeacher); response.Code != http.StatusForbidden {
		t.Fatalf("draft delete ownership: %d", response.Code)
	}
	backend.deleteDraftErr = errors.New("draft delete unavailable")
	if response := call(t, handler, http.MethodDelete, "/quiz/drafts/coverage-draft", nil, teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("draft delete error: %d", response.Code)
	}
	backend.deleteDraftErr = nil
	backend.publishDraftErr = errors.New("draft publish unavailable")
	if response := call(t, handler, http.MethodPost, "/quiz/drafts/coverage-draft/publish", nil, teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("draft publish error: %d", response.Code)
	}
	backend.publishDraftErr = nil
	backend.duplicateQuizErr = errors.New("duplicate unavailable")
	if response := call(t, handler, http.MethodPost, "/quiz/drafts/coverage-draft/duplicate", nil, teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("draft duplicate error: %d", response.Code)
	}
	backend.duplicateQuizErr = nil
	if response := call(t, handler, http.MethodPost, "/quiz/duplicate", map[string]any{}, teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate source validation: %d", response.Code)
	}
	if response := call(t, handler, http.MethodPost, "/quiz/duplicate", map[string]any{"source_id": "coverage-draft", "id": "copy"}, teacher); response.Code != http.StatusCreated {
		t.Fatalf("duplicate quiz: %d %s", response.Code, response.Body.String())
	}
}

func TestHTTPAPICoverageHelpersAndErrors(t *testing.T) {
	server, _, _ := newCoverageServer(t)
	ctx := context.Background()
	if server.now().IsZero() || (&Server{}).now().IsZero() {
		t.Fatal("server clock")
	}
	if got := trimStrings([]string{" a ", "b"}); got[0] != "a" || got[1] != "b" {
		t.Fatalf("trim strings = %#v", got)
	}
	if got := attemptResponse(nil, nil); got["version"] != 0 {
		t.Fatalf("empty attempt response = %#v", got)
	}
	attempt := &model.Attempt{Answers: map[string]int{"0": 1}, Version: 2, StartedAt: 3, UpdatedAt: 4, AttemptNumber: 1}
	deadline := int64(5)
	if got := attemptResponse(attempt, &deadline); got["deadline_ms"] != int64(5) || got["version"] != 2 || got["attempt_number"] != 1 {
		t.Fatalf("attempt response = %#v", got)
	}
	attempt.AttemptNumber = 0
	if got := attemptResponse(attempt, nil); got["attempt_number"] != 1 {
		t.Fatalf("legacy attempt response = %#v", got)
	}
	if got := payloadWindow(nil, nil, nil); len(got) != 0 {
		t.Fatalf("empty payload window = %#v", got)
	}
	open, closeAt, duration := "open", "close", 30
	if got := payloadWindow(&open, &closeAt, &duration); len(got) != 3 {
		t.Fatalf("payload window = %#v", got)
	}
	if slugify(" Hello, World ") != "hello-world" || !strings.HasPrefix(slugify("!!!"), "quiz-") {
		t.Fatal("slugify")
	}
	if queryInt(httptest.NewRequest(http.MethodGet, "/?n=12", nil), "n", 2) != 12 || queryInt(httptest.NewRequest(http.MethodGet, "/?n=x", nil), "n", 2) != 2 {
		t.Fatal("query int")
	}
	if clamp(-1, 1, 3) != 1 || clamp(4, 1, 3) != 3 || clamp(2, 1, 3) != 2 {
		t.Fatal("clamp")
	}
	if got := submissionMaps([]model.Submission{{PK: "p", SK: "s", UpdatedAt: 1}, {PK: "p2", SK: "s2", UpdatedAt: 2}}); len(got) != 2 {
		t.Fatal("submission maps")
	}

	validQuestion := questionPayload{Question: "q", Options: []string{"a", "b"}, Answer: intPtr(0)}
	validExam := examPayload{Title: "Exam", Questions: []questionPayload{validQuestion}}
	for _, invalid := range []examPayload{
		{}, {Title: "x"}, {Title: "x", Questions: []questionPayload{{Options: []string{"a", "b"}, Answer: intPtr(0)}}},
		{Title: "x", Questions: []questionPayload{{Question: "q", Options: []string{"a"}, Answer: intPtr(0)}}},
		{Title: "x", Questions: []questionPayload{{Question: "q", Options: []string{"a", ""}, Answer: intPtr(0)}}},
		{Title: "x", Questions: []questionPayload{{Question: "q", Options: []string{"a", "b"}, Answer: nil}}},
		{Title: "x", Questions: []questionPayload{{Question: "q", Options: []string{"a", "b"}, Answer: intPtr(2)}}},
		{Title: "x", Questions: []questionPayload{{Question: "q", Options: []string{"a", "b"}, Answer: intPtr(0), Points: 1001}}},
		{Title: "x", Questions: []questionPayload{validQuestion}, MaxAttempts: intPtr(21)},
		{Title: "x", Questions: []questionPayload{validQuestion}, LateGraceMinutes: intPtr(-1)},
		{Title: "x", Questions: []questionPayload{validQuestion}, OpensAt: stringPtr("bad"), ClosesAt: stringPtr("also-bad")},
	} {
		w := httptest.NewRecorder()
		if validateExam(w, invalid) || w.Code == 0 {
			t.Fatalf("invalid exam accepted: %#v", invalid)
		}
	}
	if w := httptest.NewRecorder(); !validateExam(w, validExam) {
		t.Fatal("valid exam rejected")
	}
	if ok := validAccountPayload(httptest.NewRecorder(), "valid@example.com", "password", "name"); !ok {
		t.Fatal("valid account rejected")
	}
	baseDraft := quizDraftPayload{Title: "Draft", Questions: []questionPayload{validQuestion}}
	for _, invalid := range []quizDraftPayload{
		{Title: "", Questions: baseDraft.Questions},
		{Title: "Draft"},
		{Title: "Draft", Questions: make([]questionPayload, 101)},
		{Title: "Draft", Questions: []questionPayload{{Options: []string{"a", "b"}, Answer: intPtr(0)}}},
		{Title: "Draft", Questions: []questionPayload{{Question: "q", Options: []string{"a"}, Answer: intPtr(0)}}},
		{Title: "Draft", Questions: []questionPayload{{Question: "q", Options: []string{"a", ""}, Answer: intPtr(0)}}},
		{Title: "Draft", Questions: []questionPayload{{Question: "q", Options: []string{"a", "b"}, Answer: nil}}},
		{Title: "Draft", Questions: []questionPayload{{Question: "q", Options: []string{"a", "b"}, Answer: intPtr(2)}}},
		{Title: "Draft", Questions: []questionPayload{{Question: "q", Options: []string{"a", "b"}, Answer: intPtr(0), Points: 1001}}},
		{Title: "Draft", Questions: baseDraft.Questions, MaxAttempts: intPtr(21)},
		{Title: "Draft", Questions: baseDraft.Questions, LateGraceMinutes: intPtr(-1)},
		{Title: "Draft", Questions: baseDraft.Questions, OpensAt: stringPtr("bad"), ClosesAt: stringPtr("also-bad")},
	} {
		if _, err := server.quizDraftFromPayload(invalid, "teacher", ""); err == nil {
			t.Fatalf("invalid draft accepted: %#v", invalid)
		}
	}
	if draft, err := server.quizDraftFromPayload(baseDraft, "teacher", "Forced ID"); err != nil || draft.ID != "forced-id" || draft.Questions[0].Index != 0 {
		t.Fatalf("valid draft = %#v, %v", draft, err)
	}
	allowLate := true
	fullDraft := baseDraft
	fullDraft.OpensAt = stringPtr("2026-01-01T00:00:00Z")
	fullDraft.ClosesAt = stringPtr("2027-01-01T00:00:00Z")
	fullDraft.DurationMinutes = intPtr(30)
	fullDraft.MaxAttempts = intPtr(2)
	fullDraft.AllowLate = &allowLate
	fullDraft.LateGraceMinutes = intPtr(5)
	if draft, err := server.quizDraftFromPayload(fullDraft, "teacher", ""); err != nil || draft.OpensAt == "" || draft.ClosesAt == "" || draft.DurationMinutes != 30 || draft.MaxAttempts != 2 || !draft.AllowLate || draft.LateGraceMinutes != 5 {
		t.Fatalf("full draft window = %#v, %v", draft, err)
	}

	for _, err := range []error{boards.ErrRoomNotFound, boards.ErrSnapshotNotFound, boards.ErrPersistenceUnavailable, errors.New("other")} {
		w := httptest.NewRecorder()
		server.writeBoardError(w, err)
		if w.Code == 0 {
			t.Fatalf("board error not written: %v", err)
		}
	}
	for _, err := range []error{nil, &exams.WindowError{Code: "C", Message: "window"}, exams.ErrExamNotFound, store.ErrSubmissionConflict, exams.ErrAttemptNotFound, store.ErrAttemptConflict, store.ErrAttemptSubmitted, store.ErrInvalidAnswer, errors.New("other")} {
		w := httptest.NewRecorder()
		if err == nil {
			if server.writeExamError(w, nil) {
				t.Fatal("nil exam error reported")
			}
			continue
		}
		if !server.writeExamError(w, err) || w.Code == 0 {
			t.Fatalf("exam error not written: %v", err)
		}
	}
	for _, err := range []error{nil, &exams.WindowError{Code: "C", Message: "window"}, store.ErrQuizNotFound, store.ErrQuizForbidden, store.ErrQuizConflict, store.ErrAttemptConflict, store.ErrAttemptSubmitted, exams.ErrAttemptNotFound, store.ErrInvalidAnswer, errors.New("other")} {
		w := httptest.NewRecorder()
		server.writeQuizError(w, err)
		if err != nil && w.Code == 0 {
			t.Fatalf("quiz error not written: %v", err)
		}
	}
	_ = ctx
}

func TestHTTPAPICoverageSeedAndAdminEndpoints(t *testing.T) {
	settings := config.Defaults()
	settings.RoleFile = filepath.Join(t.TempDir(), "role")
	settings.BootstrapTeacherEmail = "bootstrap@example.com"
	settings.BootstrapTeacherPass = "bootstrap-password"
	backend := &coverageBackend{MemoryStore: store.NewMemory(settings.Region)}
	server := New(settings, backend)
	server.Now = func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) }
	server.Auth.Now = time.Now
	server.Exams.Now = server.Now
	server.Classrooms.Now = server.Now
	backend.ensureExamErr = errors.New("seed exam unavailable")
	if err := server.Seed(context.Background()); err == nil {
		t.Fatal("seed exam error should be returned")
	}
	backend.ensureExamErr = nil
	backend.createUserErr = errors.New("seed user unavailable")
	if err := server.Seed(context.Background()); err == nil {
		t.Fatal("seed bootstrap error should be returned")
	}

	server, backend, handler := newCoverageServer(t)
	admin := map[string]string{"x-admin-token": config.Defaults().AdminToken}
	student, _ := registerStudent(t, handler, "admin-endpoint-student@example.com")
	if response := call(t, handler, http.MethodGet, "/admin/submissions", nil, admin); response.Code != http.StatusOK {
		t.Fatalf("empty admin submissions: %d", response.Code)
	}
	backend.listSubmissionsErr = errors.New("submission list unavailable")
	if response := call(t, handler, http.MethodGet, "/admin/submissions", nil, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("admin submissions error: %d", response.Code)
	}
	backend.listSubmissionsErr = nil
	if response := call(t, handler, http.MethodGet, "/admin/outbox", nil, admin); response.Code != http.StatusOK {
		t.Fatalf("empty admin outbox: %d", response.Code)
	}
	backend.listOutboxErr = errors.New("outbox unavailable")
	if response := call(t, handler, http.MethodGet, "/admin/outbox", nil, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("admin outbox error: %d", response.Code)
	}
	backend.listOutboxErr = nil
	if response := call(t, handler, http.MethodPost, "/admin/chaos", map[string]any{"error_pct": 10, "latency_ms": 1, "db_stall": true}, admin); response.Code != http.StatusOK {
		t.Fatalf("set chaos: %d", response.Code)
	}
	if response := call(t, handler, http.MethodPost, "/admin/chaos", "bad-json", admin); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad chaos body: %d", response.Code)
	}
	if response := call(t, handler, http.MethodPost, "/admin/chaos/reset", nil, admin); response.Code != http.StatusOK {
		t.Fatalf("reset chaos: %d", response.Code)
	}
	expiresAt := time.Now().Add(time.Minute).UnixMilli()
	if response := call(t, handler, http.MethodPost, "/admin/chaos", map[string]any{"error_pct": 100, "error_pct_expires_ms": expiresAt}, admin); response.Code != http.StatusOK {
		t.Fatalf("set expiring chaos: %d %s", response.Code, response.Body.String())
	} else if got := decodeBody(t, response)["chaos"].(map[string]any)["error_pct_expires_ms"]; got != float64(expiresAt) {
		t.Fatalf("chaos expiry was not reported: got=%v want=%d", got, expiresAt)
	}
	if response := call(t, handler, http.MethodPost, "/admin/chaos/reset", nil, admin); response.Code != http.StatusOK {
		t.Fatalf("reset expiring chaos: %d", response.Code)
	}
	if response := call(t, handler, http.MethodPost, "/admin/promote", nil, admin); response.Code != http.StatusOK {
		t.Fatalf("promote: %d", response.Code)
	}
	backend.heartbeatErr = errors.New("heartbeat unavailable")
	if response := call(t, handler, http.MethodGet, "/health/deep", nil, nil); response.Code == http.StatusOK {
		t.Fatal("deep health should reflect heartbeat error")
	}
	backend.heartbeatErr = nil
	if response := call(t, handler, http.MethodGet, "/quiz/drafts", nil, student); response.Code != http.StatusForbidden {
		t.Fatalf("student teacher endpoint: %d", response.Code)
	}

	// IssueTokens is deliberately injectable so each transport can verify its
	// own 500 response when the signing service fails.
	teacher := registerTeacher(t, handler, "admin-endpoint-teacher@example.com")
	server.Auth.SetSigner(func(*jwt.Token, []byte) (string, error) { return "", errors.New("signing unavailable") })
	if response := call(t, handler, http.MethodPost, "/auth/login", map[string]any{"email": "admin-endpoint-teacher@example.com", "password": "teacher-password"}, nil); response.Code != http.StatusInternalServerError {
		t.Fatalf("login signing error: %d", response.Code)
	}
	server.Auth.SetSigner(nil)
	_ = teacher
}

func TestHTTPAPICoverageBoardsAndResults(t *testing.T) {
	server, backend, handler := newCoverageServer(t)
	admin := map[string]string{"x-admin-token": server.Settings.AdminToken}
	student, _ := registerStudent(t, handler, "board-api-student@example.com")
	teacher := registerTeacher(t, handler, "board-api-teacher@example.com")

	// With the default in-memory manager, export/restore report the absence of
	// a persistence adapter while history remains a valid empty list.
	if response := call(t, handler, http.MethodGet, "/api/boards/r/history", nil, student); response.Code != http.StatusOK {
		t.Fatalf("empty board history: %d", response.Code)
	}
	server.Boards = boards.NewManagerWithPersistence(&coverageBoardPersistence{loadErr: errors.New("board load unavailable")}, time.Hour)
	if response := call(t, handler, http.MethodGet, "/api/boards/r/export", nil, student); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable board export: %d", response.Code)
	}
	if response := call(t, handler, http.MethodPost, "/api/boards/r/restore", map[string]any{"version": 1}, teacher); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("unavailable board restore: %d", response.Code)
	}

	persistence := &coverageBoardPersistence{
		load:     &boards.BoardSnapshot{SchemaVersion: boards.BoardSnapshotSchemaVersion, RoomMetadata: boards.RoomMetadata{RoomID: "persistent-room", Version: 2}, Elements: []map[string]any{{"id": "one", "version": 1}}},
		versions: []boards.BoardVersion{{RoomID: "persistent-room", Version: 2}},
		restore:  &boards.BoardSnapshot{SchemaVersion: boards.BoardSnapshotSchemaVersion, RoomMetadata: boards.RoomMetadata{RoomID: "persistent-room", Version: 4}, Elements: []map[string]any{{"id": "restored", "version": 4}}},
	}
	server.Boards = boards.NewManagerWithPersistence(persistence, time.Hour)
	if response := call(t, handler, http.MethodGet, "/api/boards/persistent-room/history?limit=0", nil, student); response.Code != http.StatusOK {
		t.Fatalf("board history: %d", response.Code)
	}
	if response := call(t, handler, http.MethodGet, "/api/boards/persistent-room/export", nil, student); response.Code != http.StatusOK {
		t.Fatalf("board export: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodPost, "/api/boards/persistent-room/restore", map[string]any{}, teacher); response.Code != http.StatusOK {
		t.Fatalf("board restore empty body: %d %s", response.Code, response.Body.String())
	}
	persistence.versionsErr = errors.New("history unavailable")
	if response := call(t, handler, http.MethodGet, "/api/boards/persistent-room/history", nil, student); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("board history error: %d", response.Code)
	}
	persistence.versionsErr = nil
	persistence.restoreErr = boards.ErrSnapshotNotFound
	if response := call(t, handler, http.MethodPost, "/api/boards/persistent-room/restore", map[string]any{"version": 99}, teacher); response.Code != http.StatusNotFound {
		t.Fatalf("board restore missing: %d", response.Code)
	}
	persistence.restoreErr = errors.New("restore unavailable")
	if response := call(t, handler, http.MethodPost, "/api/boards/persistent-room/restore", map[string]any{"version": 99}, teacher); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("board restore error: %d", response.Code)
	}
	persistence.restoreErr = nil

	// Submit one real row so exam-results, submissions, outbox, and the
	// question statistics path all receive production-shaped data.
	if response := call(t, handler, http.MethodPost, "/exam/cs101-midterm/submit", map[string]any{"answers": map[string]int{"0": 1}, "request_id": "results-request"}, student); response.Code != http.StatusOK {
		t.Fatalf("results submission: %d %s", response.Code, response.Body.String())
	}
	if response := call(t, handler, http.MethodGet, "/admin/exams/cs101-midterm/results", nil, admin); response.Code != http.StatusOK {
		t.Fatalf("exam results: %d %s", response.Code, response.Body.String())
	}
	backend.getExamErr = errors.New("results exam read unavailable")
	if response := call(t, handler, http.MethodGet, "/admin/exams/cs101-midterm/results", nil, admin); response.Code != http.StatusOK {
		t.Fatalf("exam results tolerate read error: %d", response.Code)
	}
	backend.getExamErr = nil
	backend.listSubmissionsErr = errors.New("results list unavailable")
	if response := call(t, handler, http.MethodGet, "/admin/exams/cs101-midterm/results", nil, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("exam results list error: %d", response.Code)
	}
}

func TestHTTPAPICoverageRemainingBranches(t *testing.T) {
	server, backend, handler := newCoverageServer(t)
	admin := map[string]string{"x-admin-token": server.Settings.AdminToken}
	student, registered := registerStudent(t, handler, "remaining-student@example.com")
	teacher := registerTeacher(t, handler, "remaining-teacher@example.com")

	if response := call(t, handler, http.MethodPost, "/admin/users", map[string]any{}, map[string]string{"x-admin-token": "wrong"}); response.Code != http.StatusForbidden {
		t.Fatalf("admin user guard: %d", response.Code)
	}
	if response := rawCall(t, handler, http.MethodPost, "/auth/login", "{", nil); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed login: %d", response.Code)
	}
	if response := rawCall(t, handler, http.MethodPost, "/auth/refresh", "{", nil); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed refresh: %d", response.Code)
	}
	if response := rawCall(t, handler, http.MethodPost, "/auth/register", "{} {}", nil); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("extra JSON value: %d", response.Code)
	}
	backend.getUserErr = errors.New("refresh user unavailable")
	if response := call(t, handler, http.MethodPost, "/auth/refresh", map[string]any{"refresh_token": registered["refresh_token"]}, nil); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("refresh user error: %d", response.Code)
	}
	backend.getUserErr = nil
	server.Auth.SetSigner(func(*jwt.Token, []byte) (string, error) { return "", errors.New("refresh signing unavailable") })
	if response := call(t, handler, http.MethodPost, "/auth/refresh", map[string]any{"refresh_token": registered["refresh_token"]}, nil); response.Code != http.StatusInternalServerError {
		t.Fatalf("refresh signing error: %d", response.Code)
	}
	server.Auth.SetSigner(nil)
	backend.getUserNil = true
	if response := call(t, handler, http.MethodGet, "/auth/me", nil, student); response.Code != http.StatusUnauthorized {
		t.Fatalf("deleted current user: %d", response.Code)
	}
	backend.getUserNil = false

	if response := rawCall(t, handler, http.MethodPost, "/admin/users", "{", admin); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed admin user: %d", response.Code)
	}
	if response := call(t, handler, http.MethodPost, "/admin/users", map[string]any{"email": "bad-admin@example.com", "password": "short", "name": "Bad"}, admin); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid admin user: %d", response.Code)
	}

	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/exam/cs101-midterm", nil},
		{http.MethodGet, "/exam/cs101-midterm/attempt", nil},
		{http.MethodPut, "/exam/cs101-midterm/attempt", map[string]any{}},
		{http.MethodPost, "/exam/cs101-midterm/attempt/submit", nil},
		{http.MethodPost, "/exam/cs101-midterm/submit", map[string]any{}},
		{http.MethodGet, "/exam/cs101-midterm/result", nil},
	} {
		if response := call(t, handler, tc.method, tc.path, tc.body, nil); response.Code != http.StatusUnauthorized {
			t.Fatalf("unauthenticated %s %s: %d", tc.method, tc.path, response.Code)
		}
	}
	backend.getExamMetaErr = errors.New("attempt meta unavailable")
	if response := call(t, handler, http.MethodGet, "/exam/cs101-midterm/attempt", nil, student); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("attempt meta error: %d", response.Code)
	}
	backend.getExamMetaErr = nil
	if response := rawCall(t, handler, http.MethodPut, "/exam/cs101-midterm/attempt", "{", student); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed save attempt: %d", response.Code)
	}
	if response := rawCall(t, handler, http.MethodPost, "/exam/cs101-midterm/attempt/submit", "{", student); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed saved submit: %d", response.Code)
	}
	backend.getAttemptErr = errors.New("saved attempt unavailable")
	if response := call(t, handler, http.MethodPost, "/exam/cs101-midterm/attempt/submit", map[string]any{}, student); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("saved attempt error: %d", response.Code)
	}
	backend.getAttemptErr = nil
	if response := rawCall(t, handler, http.MethodPost, "/exam/cs101-midterm/submit", "{", student); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed submit: %d", response.Code)
	}

	if response := call(t, handler, http.MethodGet, "/api/boards/r/history", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated board history: %d", response.Code)
	}
	if response := call(t, handler, http.MethodGet, "/api/boards/r/export", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated board export: %d", response.Code)
	}
	if response := rawCall(t, handler, http.MethodPost, "/api/boards/r/restore", "{", teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed board restore: %d", response.Code)
	}
	persistence := &coverageBoardPersistence{}
	boardManager := boards.NewManagerWithPersistence(persistence, time.Hour)
	persistence.onLoad = boardManager.Reset
	server.Boards = boardManager
	if response := call(t, handler, http.MethodGet, "/api/boards/export-room/export", nil, student); response.Code != http.StatusNotFound {
		t.Fatalf("missing exported room: %d", response.Code)
	}

	if response := call(t, handler, http.MethodGet, "/quiz/drafts", nil, student); response.Code != http.StatusForbidden {
		t.Fatalf("student draft list: %d", response.Code)
	}
	if response := call(t, handler, http.MethodGet, "/quiz/drafts", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated draft list: %d", response.Code)
	}
	for _, tc := range []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/quiz/drafts", map[string]any{}},
		{http.MethodGet, "/quiz/drafts/missing", nil},
		{http.MethodPatch, "/quiz/drafts/missing", map[string]any{}},
		{http.MethodDelete, "/quiz/drafts/missing", nil},
		{http.MethodPost, "/quiz/drafts/missing/publish", nil},
		{http.MethodPost, "/quiz/drafts/missing/duplicate", nil},
		{http.MethodPost, "/quiz/duplicate", map[string]any{}},
	} {
		if response := call(t, handler, tc.method, tc.path, tc.body, student); response.Code != http.StatusForbidden {
			t.Fatalf("student teacher route %s %s: %d", tc.method, tc.path, response.Code)
		}
	}
	if response := rawCall(t, handler, http.MethodPatch, "/quiz/drafts/missing", "{", teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed draft update: %d", response.Code)
	}
	if response := rawCall(t, handler, http.MethodPost, "/quiz/drafts/missing/duplicate", "{", teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed draft duplicate: %d", response.Code)
	}
	if response := rawCall(t, handler, http.MethodPost, "/quiz/drafts", "{", teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed draft create: %d", response.Code)
	}
	if response := call(t, handler, http.MethodPatch, "/quiz/drafts/missing", map[string]any{"title": "invalid"}, teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid draft update payload: %d", response.Code)
	}
	if response := rawCall(t, handler, http.MethodPost, "/quiz/duplicate", "{", teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed quiz duplicate: %d", response.Code)
	}
	backend.duplicateQuizErr = errors.New("quiz duplicate unavailable")
	if response := call(t, handler, http.MethodPost, "/quiz/duplicate", map[string]any{"source_id": "cs101-midterm"}, teacher); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("quiz duplicate error: %d", response.Code)
	}
	backend.duplicateQuizErr = nil
	backend.listDraftsNil = true
	if response := call(t, handler, http.MethodGet, "/quiz/drafts", nil, teacher); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "[]") {
		t.Fatalf("nil draft list: %d %s", response.Code, response.Body.String())
	}
	backend.listDraftsNil = false
	if response := call(t, handler, http.MethodPost, "/quiz/drafts", map[string]any{"id": "delete-me", "title": "Delete me", "questions": []map[string]any{{"question": "q", "options": []string{"a", "b"}, "answer": 0}}}, teacher); response.Code != http.StatusCreated {
		t.Fatalf("draft for delete: %d", response.Code)
	}
	if response := call(t, handler, http.MethodDelete, "/quiz/drafts/delete-me", nil, teacher); response.Code != http.StatusNoContent {
		t.Fatalf("successful draft delete: %d", response.Code)
	}

	backend.getExamErr = errors.New("generated id lookup unavailable")
	if response := call(t, handler, http.MethodPost, "/exam", map[string]any{"title": "Lookup error", "questions": []map[string]any{{"question": "q", "options": []string{"a", "b"}, "answer": 0}}}, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("generated exam lookup error: %d", response.Code)
	}
	backend.getExamErr = nil
	for i := 1; i <= 51; i++ {
		id := "collision"
		if i > 1 {
			id = fmt.Sprintf("collision-%d", i)
		}
		if err := backend.MemoryStore.EnsureExam(context.Background(), id, "Collision", []model.Question{{Question: "q", Options: []string{"a", "b"}, Answer: 0}}, nil); err != nil {
			t.Fatalf("seed collision exam %s: %v", id, err)
		}
	}
	if response := call(t, handler, http.MethodPost, "/exam", map[string]any{"title": "Collision", "questions": []map[string]any{{"question": "q", "options": []string{"a", "b"}, "answer": 0}}}, admin); response.Code != http.StatusCreated {
		t.Fatalf("collision fallback exam: %d %s", response.Code, response.Body.String())
	}
	if response := rawCall(t, handler, http.MethodPatch, "/exam/cs101-midterm", "{", admin); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed exam patch: %d", response.Code)
	}
	backend.getExamMetaErr = errors.New("close meta unavailable")
	if response := call(t, handler, http.MethodPost, "/exam/cs101-midterm/close", nil, admin); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("close meta error: %d", response.Code)
	}
	backend.getExamMetaErr = nil

	for _, path := range []string{"/admin/outbox", "/admin/submissions", "/admin/chaos", "/admin/chaos/reset", "/admin/promote", "/admin/exams/cs101-midterm/results"} {
		method := http.MethodGet
		if strings.HasSuffix(path, "chaos") || strings.HasSuffix(path, "reset") || strings.HasSuffix(path, "promote") {
			method = http.MethodPost
		}
		if response := call(t, handler, method, path, nil, map[string]string{"x-admin-token": "wrong"}); response.Code != http.StatusForbidden {
			t.Fatalf("admin guard %s: %d", path, response.Code)
		}
	}
	backend.getUserErr = errors.New("optional staff lookup unavailable")
	if response := call(t, handler, http.MethodGet, "/exam/cs101-midterm/preview", nil, student); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("staff lookup error: %d", response.Code)
	}
	backend.getUserErr = nil
	if response := call(t, handler, http.MethodGet, "/exam/cs101-midterm/preview", nil, nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("staff auth required: %d", response.Code)
	}

	server.Roles.fallback = "standby"
	for _, tc := range []struct {
		method  string
		path    string
		body    any
		headers map[string]string
	}{
		{http.MethodGet, "/api/boards/r/history", nil, student},
		{http.MethodGet, "/api/boards/r/export", nil, student},
		{http.MethodGet, "/exam/cs101-midterm/attempt", nil, student},
		{http.MethodPut, "/exam/cs101-midterm/attempt", map[string]any{}, student},
		{http.MethodPost, "/exam/cs101-midterm/attempt/submit", nil, student},
		{http.MethodPost, "/exam/cs101-midterm/submit", map[string]any{}, student},
		{http.MethodGet, "/exam/cs101-midterm/result", nil, student},
		{http.MethodPost, "/api/boards/r/restore", map[string]any{}, teacher},
		{http.MethodPost, "/quiz/drafts", map[string]any{}, teacher},
		{http.MethodPatch, "/quiz/drafts/missing", map[string]any{}, teacher},
		{http.MethodDelete, "/quiz/drafts/missing", nil, teacher},
		{http.MethodPost, "/quiz/drafts/missing/publish", nil, teacher},
		{http.MethodPost, "/quiz/drafts/missing/duplicate", nil, teacher},
		{http.MethodPost, "/quiz/duplicate", map[string]any{"source_id": "x"}, teacher},
		{http.MethodPost, "/exam", map[string]any{"title": "standby", "questions": []map[string]any{{"question": "q", "options": []string{"a", "b"}, "answer": 0}}}, admin},
		{http.MethodPatch, "/exam/cs101-midterm", map[string]any{}, admin},
		{http.MethodPost, "/exam/cs101-midterm/close", nil, admin},
		{http.MethodDelete, "/exam/cs101-midterm", nil, admin},
	} {
		if response := call(t, handler, tc.method, tc.path, tc.body, tc.headers); response.Code != http.StatusServiceUnavailable {
			t.Fatalf("standby guard %s %s: %d", tc.method, tc.path, response.Code)
		}
	}
}

func intPtr(value int) *int          { return &value }
func stringPtr(value string) *string { return &value }

type coverageBoardPersistence struct {
	load        *boards.BoardSnapshot
	versions    []boards.BoardVersion
	loadErr     error
	versionsErr error
	restore     *boards.BoardSnapshot
	restoreErr  error
	onLoad      func()
}

func (p *coverageBoardPersistence) Load(context.Context, string) (*boards.BoardSnapshot, error) {
	if p.onLoad != nil {
		p.onLoad()
	}
	return p.load, p.loadErr
}
func (p *coverageBoardPersistence) Save(context.Context, boards.BoardSnapshot) error { return nil }
func (p *coverageBoardPersistence) ListVersions(context.Context, string, int) ([]boards.BoardVersion, error) {
	return p.versions, p.versionsErr
}
func (p *coverageBoardPersistence) LoadVersion(context.Context, string, int64) (*boards.BoardSnapshot, error) {
	return p.restore, p.restoreErr
}
