// Package httpapi exposes the Continuum HTTP contract using the Go standard
// library. Business rules and persistence live in the internal services so the
// transport remains small and testable.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ayush/slate/internal/auth"
	"github.com/ayush/slate/internal/boards"
	"github.com/ayush/slate/internal/chaos"
	"github.com/ayush/slate/internal/classrooms"
	"github.com/ayush/slate/internal/clusterstate"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/exams"
	"github.com/ayush/slate/internal/health"
	appmw "github.com/ayush/slate/internal/middleware"
	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
)

type Backend interface {
	EnsureExam(context.Context, string, string, []model.Question, map[string]any) error
	UpdateExamWindow(context.Context, string, *string, map[string]any) (*model.ExamMeta, error)
	GetExamMeta(context.Context, string) (*model.ExamMeta, error)
	GetExam(context.Context, string, bool) (*model.Exam, error)
	ListExams(context.Context, int) ([]model.ExamMeta, error)
	DeleteExam(context.Context, string) (int, error)
	ListSubmissions(context.Context, string, int) ([]model.Submission, error)
	ListOutbox(context.Context, int) ([]model.OutboxEntry, error)
	GetSubmission(context.Context, string, string) (*model.Submission, error)
	HeartbeatProbe(context.Context, string) (bool, float64, error)
	auth.UserStore
	exams.Store
	exams.QuizStore
}

type RoleManager struct {
	mu       sync.RWMutex
	roleFile string
	fallback string
}

func NewRoleManager(roleFile, fallback string) *RoleManager {
	return &RoleManager{roleFile: roleFile, fallback: fallback}
}

func (r *RoleManager) Current() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if data, err := os.ReadFile(r.roleFile); err == nil && strings.TrimSpace(string(data)) != "" {
		return strings.TrimSpace(string(data))
	}
	return r.fallback
}

func (r *RoleManager) Promote() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if info, err := os.Stat(filepath.Dir(r.roleFile)); err == nil && info.IsDir() {
		_ = os.WriteFile(r.roleFile, []byte("active"), 0o644)
	}
	r.fallback = "active"
}

type Server struct {
	Settings   config.Settings
	Store      Backend
	Auth       *auth.Service
	Exams      *exams.Service
	Classrooms *classrooms.Service
	Boards     *boards.Manager
	Chaos      *chaos.State
	Roles      *RoleManager
	Now        func() time.Time
	WS         http.Handler
}

func New(settings config.Settings, backend Backend) *Server {
	var classroomStore classrooms.Store
	if candidate, ok := backend.(classrooms.Store); ok {
		classroomStore = candidate
	}
	var boardStore boards.BoardSnapshotStore
	if candidate, ok := backend.(*store.Store); ok {
		boardStore = store.NewBoardStore(candidate)
	}
	return &Server{
		Settings:   settings,
		Store:      backend,
		Auth:       auth.New(backend, settings),
		Exams:      exams.New(backend),
		Classrooms: classrooms.New(classroomStore),
		Boards:     boards.NewManagerWithPersistence(boardStore, boards.DefaultSaveDebounce),
		Chaos: chaos.NewState(chaos.Config{ErrorPct: settings.ChaosErrorPct, LatencyMS: settings.ChaosLatencyMS,
			DBStall: settings.DBStall, ErrorWindow: settings.ErrorRateWindow}),
		Roles: NewRoleManager(settings.RoleFile, settings.AppRole),
		Now:   time.Now,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.index)
	mux.HandleFunc("GET /health/live", s.live)
	mux.HandleFunc("GET /health/deep", s.deep)
	mux.HandleFunc("POST /auth/register", s.register)
	mux.HandleFunc("POST /auth/login", s.login)
	mux.HandleFunc("POST /auth/refresh", s.refresh)
	mux.HandleFunc("GET /auth/me", s.me)
	if s.Classrooms != nil {
		classroomHandler := classrooms.NewHTTPHandler(s.Classrooms, s.Auth, func(w http.ResponseWriter, _ *http.Request) bool {
			return s.requireActive(w)
		})
		// The project historically mounted API resources at the root. Keep both
		// forms so existing clients continue to work while new clients can use
		// the conventional /api prefix.
		mux.Handle("/classrooms", classroomHandler)
		mux.Handle("/classrooms/", classroomHandler)
		mux.Handle("/api/classrooms", classroomHandler)
		mux.Handle("/api/classrooms/", classroomHandler)
	}
	mux.HandleFunc("POST /admin/users", s.adminCreateUser)
	mux.HandleFunc("GET /exams", s.listExams)
	mux.HandleFunc("POST /exam", s.createExam)
	mux.HandleFunc("GET /exam/{examID}", s.getExam)
	mux.HandleFunc("GET /exam/{examID}/preview", s.previewExam)
	mux.HandleFunc("GET /exam/{examID}/attempt", s.getAttempt)
	mux.HandleFunc("PUT /exam/{examID}/attempt", s.saveAttempt)
	mux.HandleFunc("POST /exam/{examID}/attempt/submit", s.submitSavedAttempt)
	mux.HandleFunc("POST /exam/{examID}/submit", s.submit)
	mux.HandleFunc("GET /exam/{examID}/result", s.result)
	mux.HandleFunc("PATCH /exam/{examID}", s.patchExam)
	mux.HandleFunc("POST /exam/{examID}/close", s.closeExam)
	mux.HandleFunc("DELETE /exam/{examID}", s.deleteExam)
	mux.HandleFunc("GET /quiz/drafts", s.listQuizDrafts)
	mux.HandleFunc("POST /quiz/drafts", s.createQuizDraft)
	mux.HandleFunc("GET /quiz/drafts/{draftID}", s.getQuizDraft)
	mux.HandleFunc("PATCH /quiz/drafts/{draftID}", s.updateQuizDraft)
	mux.HandleFunc("DELETE /quiz/drafts/{draftID}", s.deleteQuizDraft)
	mux.HandleFunc("POST /quiz/drafts/{draftID}/publish", s.publishQuizDraft)
	mux.HandleFunc("POST /quiz/drafts/{draftID}/duplicate", s.duplicateQuizDraft)
	mux.HandleFunc("POST /quiz/duplicate", s.duplicateQuiz)
	mux.HandleFunc("GET /api/boards/{roomID}/history", s.boardHistory)
	mux.HandleFunc("GET /api/boards/{roomID}/export", s.boardExport)
	mux.HandleFunc("POST /api/boards/{roomID}/restore", s.boardRestore)
	mux.HandleFunc("GET /admin/exams/{examID}/results", s.examResults)
	mux.HandleFunc("GET /admin/outbox", s.outbox)
	mux.HandleFunc("GET /admin/submissions", s.submissions)
	mux.HandleFunc("GET /admin/status", s.status)
	mux.HandleFunc("POST /admin/chaos", s.setChaos)
	mux.HandleFunc("POST /admin/chaos/reset", s.resetChaos)
	mux.HandleFunc("POST /admin/promote", s.promote)
	mux.Handle("GET /ws/board/{roomID}", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.WS == nil {
			http.NotFound(w, r)
			return
		}
		s.WS.ServeHTTP(w, r)
	}))
	limiter := appmw.NewRateLimiter(appmw.AuthRateLimits(s.Settings.LoginRateLimit,
		s.Settings.RegisterRateLimit, time.Duration(s.Settings.RateLimitWindowS)*time.Second))
	var h http.Handler = mux
	h = s.Chaos.Handler(h)
	h = s.writerHeaders(h)
	h = limiter.Handler(h)
	h = appmw.RequestID(h)
	h = s.cors(h)
	return h
}

func (s *Server) writerHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// These two routes only arm/reset process-local fault injection and
		// require the admin token in their handlers. They do not write exam,
		// identity, classroom, or receipt data and are used by the cloud verifier
		// on both backends before/after a fenced write test.
		localChaosControl := r.Method == http.MethodPost && (r.URL.Path == "/admin/chaos" || r.URL.Path == "/admin/chaos/reset")
		if !s.Settings.FencingEnabled || r.Method == http.MethodOptions || r.URL.Path == "/health/live" || r.URL.Path == "/health/deep" || localChaosControl {
			next.ServeHTTP(w, r)
			return
		}
		backendID := strings.ToUpper(strings.TrimSpace(s.Settings.BackendID))
		backendHeader := strings.TrimSpace(r.Header.Get(clusterstate.BackendHeader))
		epochHeader := strings.TrimSpace(r.Header.Get(clusterstate.EpochHeader))
		epoch, err := strconv.ParseInt(epochHeader, 10, 64)
		if (backendID != "A" && backendID != "B") || backendHeader != backendID || strings.Contains(backendHeader, ",") || err != nil || epoch <= 0 || strings.Contains(epochHeader, ",") {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": map[string]any{
				"code": "WRITER_FENCED", "message": "request is missing valid routing writer metadata", "retryable": true,
			}})
			return
		}
		ctx := clusterstate.WithWriter(r.Context(), clusterstate.Writer{Backend: backendID, Epoch: epoch})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) Seed(ctx context.Context) error {
	ctx = store.WithBootstrap(ctx)
	questions := []model.Question{
		{Index: 0, Question: "Which AWS service provides cross-region asynchronous table replication?", Options: []string{"Aurora Global Database", "DynamoDB Global Tables", "S3 CRR", "EBS Snapshots"}, Answer: 1},
		{Index: 1, Question: "What does RPO measure?", Options: []string{"Maximum tolerable data loss window", "Maximum tolerable downtime", "Replication throughput", "Recovery cost"}, Answer: 0},
		{Index: 2, Question: "Route 53 failover routing policy requires at least how many record sets?", Options: []string{"1", "2", "3", "4"}, Answer: 1},
		{Index: 3, Question: "Step Functions rollback branches are modelled using which ASL field?", Options: []string{"Retry", "Catch", "TimeoutSeconds", "HeartbeatSeconds"}, Answer: 1},
		{Index: 4, Question: "What prevents an old backend from committing after promotion?", Options: []string{"A DNS TTL", "An active-backend and epoch condition in the write transaction", "An outbox retry", "A longer heartbeat TTL"}, Answer: 1},
	}
	var examErr error
	demoWindow := map[string]any{"duration_minutes": 15, "max_attempts": 1}
	if seeder, ok := s.Store.(interface {
		EnsureExamIfMissing(context.Context, string, string, []model.Question, map[string]any) error
	}); ok {
		examErr = seeder.EnsureExamIfMissing(ctx, "cs101-midterm", "Cloud Architecture CAT", questions, demoWindow)
	} else {
		var existing *model.ExamMeta
		existing, examErr = s.Store.GetExamMeta(ctx, "cs101-midterm")
		if examErr == nil && existing == nil {
			examErr = s.Store.EnsureExam(ctx, "cs101-midterm", "Cloud Architecture CAT", questions, demoWindow)
		}
	}
	_, authErr := s.Auth.SeedBootstrapTeacher(ctx)
	if examErr != nil {
		return examErr
	}
	return authErr
}

func (s *Server) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	role := s.Roles.Current()
	if role == "standby" {
		fmt.Fprintf(w, "CONTINUUM STANDBY NODE [%s]\nThis node is in standby. Exam delivery unavailable until promoted.\n", s.Settings.Region)
		return
	}
	fmt.Fprintf(w, "CONTINUUM EXAM PLATFORM [%s] role=%s — visit /exam/cs101-midterm\n", s.Settings.Region, strings.ToUpper(role))
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "alive", "region": s.Settings.Region, "role": s.Roles.Current()})
}

func (s *Server) deep(w http.ResponseWriter, r *http.Request) {
	snap := s.Chaos.Snapshot()
	report, status := health.Deep(r.Context(), health.Config{Region: s.Settings.Region, Role: s.Roles.Current(),
		ErrorRateThreshold: s.Settings.ErrorRateThreshold, DiskThresholdPct: s.Settings.DiskThresholdPct,
		LoadThreshold: s.Settings.LoadThreshold}, health.Probe{
		DBProbe: func(ctx context.Context, region string) (map[string]any, error) {
			ok, latency, err := s.Store.HeartbeatProbe(ctx, region)
			return map[string]any{"ok": ok, "latency_ms": latency}, err
		}, ErrorRate: s.Chaos.ErrorRate,
	}, health.ChaosSnapshot{ErrorPct: snap.ErrorPct, LatencyMS: snap.LatencyMS, DBStall: snap.DBStall})
	writeJSON(w, status, report)
}

type registerPayload struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var p registerPayload
	if !decode(w, r, &p) || !validAccountPayload(w, p.Email, p.Password, p.Name) {
		return
	}
	u, err := s.Auth.CreateUser(r.Context(), p.Email, p.Password, p.Name, auth.RoleStudent)
	if errors.Is(err, auth.ErrEmailAlreadyRegistered) {
		detail(w, 409, "email already registered")
		return
	}
	if err != nil {
		if errors.Is(err, clusterstate.ErrFenced) {
			writeWriterFenced(w)
			return
		}
		detail(w, 503, "data layer unavailable")
		return
	}
	tokens, err := s.Auth.IssueTokens(u)
	if err != nil {
		detail(w, 500, "token issue failed")
		return
	}
	tokens["user"] = u
	writeJSON(w, 201, tokens)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var p registerPayload
	if !decode(w, r, &p) {
		return
	}
	// The login payload requires both fields. Do this check before
	// touching DynamoDB so malformed requests are 422s rather than being
	// treated as failed credentials (401).
	if p.Email == "" || p.Password == "" {
		validation(w, "email and password are required")
		return
	}
	u, err := s.Auth.Authenticate(r.Context(), p.Email, p.Password)
	if err != nil {
		detail(w, 503, "data layer unavailable")
		return
	}
	if u == nil {
		detail(w, 401, "invalid email or password")
		return
	}
	tokens, err := s.Auth.IssueTokens(u)
	if err != nil {
		detail(w, 500, "token issue failed")
		return
	}
	tokens["user"] = u
	writeJSON(w, 200, tokens)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var p struct {
		RefreshToken string `json:"refresh_token"`
	}
	if !decode(w, r, &p) {
		return
	}
	if p.RefreshToken == "" {
		validation(w, "refresh_token is required")
		return
	}
	claims, err := s.Auth.DecodeToken(p.RefreshToken, "refresh")
	if err != nil {
		detail(w, 401, "invalid or expired refresh token")
		return
	}
	id, _ := claims["sub"].(string)
	u, err := s.Auth.GetUser(r.Context(), id)
	if err != nil {
		detail(w, 503, "data layer unavailable")
		return
	}
	if u == nil {
		detail(w, 401, "account no longer exists")
		return
	}
	tokens, err := s.Auth.IssueTokens(u)
	if err != nil {
		detail(w, 500, "token issue failed")
		return
	}
	tokens["user"] = u
	writeJSON(w, 200, tokens)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, map[string]any{"user": u})
}

func (s *Server) adminCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var p struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Name     string `json:"name"`
		Role     string `json:"role"`
	}
	if !decode(w, r, &p) || !validAccountPayload(w, p.Email, p.Password, p.Name) {
		return
	}
	if p.Role == "" {
		p.Role = auth.RoleStudent
	}
	if p.Role != auth.RoleStudent && p.Role != auth.RoleTeacher {
		validation(w, "role must be one of student, teacher")
		return
	}
	u, err := s.Auth.CreateUser(r.Context(), p.Email, p.Password, p.Name, p.Role)
	if errors.Is(err, auth.ErrEmailAlreadyRegistered) {
		detail(w, 409, "email already registered")
		return
	}
	if err != nil {
		if errors.Is(err, clusterstate.ErrFenced) {
			writeWriterFenced(w)
			return
		}
		detail(w, 503, "data layer unavailable")
		return
	}
	writeJSON(w, 201, map[string]any{"user": u})
}

func (s *Server) getExam(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	exam, err := s.Exams.Get(r.Context(), r.PathValue("examID"), u)
	if s.writeExamError(w, err) {
		return
	}
	writeJSON(w, 200, exam)
}

// previewExam deliberately includes answer metadata, but only for staff. The
// student delivery route above always redacts it through exams.Service.
func (s *Server) previewExam(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) || !s.requireStaff(w, r) {
		return
	}
	exam, err := s.Store.GetExam(r.Context(), r.PathValue("examID"), true)
	if err != nil {
		detail(w, 503, "data layer unavailable")
		return
	}
	if exam == nil {
		detail(w, 404, "exam not found")
		return
	}
	writeJSON(w, 200, exam)
}

func (s *Server) getAttempt(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	attempt, deadline, err := s.Exams.Resume(r.Context(), r.PathValue("examID"), user)
	if s.writeExamError(w, err) {
		return
	}
	writeJSON(w, 200, attemptResponse(attempt, deadline))
}

func (s *Server) saveAttempt(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	var payload struct {
		Answers         map[string]int `json:"answers"`
		ExpectedVersion int            `json:"expected_version"`
		RequestID       string         `json:"request_id"`
	}
	if !decode(w, r, &payload) {
		return
	}
	if payload.Answers == nil {
		validation(w, "answers is required")
		return
	}
	result, err := s.Exams.Save(r.Context(), r.PathValue("examID"), user, payload.Answers, payload.ExpectedVersion, payload.RequestID)
	if s.writeExamError(w, err) {
		return
	}
	writeJSON(w, 200, map[string]any{
		"status":      result.Status,
		"request_id":  result.RequestID,
		"version":     result.Attempt.Version,
		"answers":     result.Attempt.Answers,
		"started_at":  result.Attempt.StartedAt,
		"deadline_ms": result.DeadlineMS,
	})
}

func (s *Server) submitSavedAttempt(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	user, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	var payload struct {
		RequestID string `json:"request_id"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decode(w, r, &payload) {
		return
	}
	result, err := s.Exams.SubmitSaved(r.Context(), r.PathValue("examID"), user, payload.RequestID)
	if s.writeExamError(w, err) {
		return
	}
	if result.Status == "committed" && s.Chaos.TakeDropResponseOnce() {
		s.writeUncertainSubmission(w)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) submit(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	var p struct {
		Answers   map[string]int `json:"answers"`
		RequestID string         `json:"request_id"`
	}
	if !decode(w, r, &p) {
		return
	}
	// SubmitPayload.answers is required by the legacy Pydantic model. A nil
	// map means the field was omitted or explicitly null; an empty JSON object
	// remains valid and is intentionally accepted.
	if p.Answers == nil {
		validation(w, "answers is required")
		return
	}
	result, err := s.Exams.Submit(r.Context(), r.PathValue("examID"), u, p.Answers, p.RequestID)
	if s.writeExamError(w, err) {
		return
	}
	if result.Status == "committed" && s.Chaos.TakeDropResponseOnce() {
		s.writeUncertainSubmission(w)
		return
	}
	writeJSON(w, 200, result)
}

func (s *Server) writeUncertainSubmission(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": map[string]any{
		"code": "SUBMISSION_STATUS_UNKNOWN", "message": "the submission may have committed; retry with the same request_id", "retryable": true,
	}})
}

func (s *Server) result(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	u, ok := s.currentUser(w, r)
	if !ok {
		return
	}
	examID := r.PathValue("examID")
	sub, err := s.Store.GetSubmission(r.Context(), examID, u.UserID)
	if err != nil {
		detail(w, 503, "data layer unavailable")
		return
	}
	if sub == nil {
		detail(w, 404, "no submission for this exam")
		return
	}
	exam, _ := s.Store.GetExam(r.Context(), examID, true)
	review := make([]map[string]any, 0)
	if exam != nil {
		for _, q := range exam.Questions {
			chosen, exists := sub.Answers[fmt.Sprint(q.Index)]
			var chosenValue any
			if exists {
				chosenValue = chosen
			}
			review = append(review, map[string]any{"index": q.Index, "question": q.Question, "options": q.Options,
				"chosen": chosenValue, "correct_answer": q.Answer, "was_correct": exists && chosen == q.Answer})
		}
	}
	writeJSON(w, 200, map[string]any{"status": "submitted", "score": sub.Score, "total": sub.Total,
		"answers": sub.Answers, "submitted_at": sub.UpdatedAtISO, "committed_at": sub.UpdatedAt, "request_id": sub.RequestID, "review": review})
}

func (s *Server) listExams(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.ListExams(r.Context(), 50)
	if err != nil {
		detail(w, 503, "data layer unavailable")
		return
	}
	writeJSON(w, 200, rows)
}

func (s *Server) boardHistory(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	if _, ok := s.currentUser(w, r); !ok {
		return
	}
	versions, err := s.Boards.ListVersions(r.Context(), r.PathValue("roomID"), clamp(queryInt(r, "limit", 50), 1, 100))
	if err != nil {
		s.writeBoardError(w, err)
		return
	}
	if versions == nil {
		versions = []boards.BoardVersion{}
	}
	writeJSON(w, http.StatusOK, versions)
}

func (s *Server) boardExport(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	if _, ok := s.currentUser(w, r); !ok {
		return
	}
	roomID := r.PathValue("roomID")
	if err := s.Boards.Resume(r.Context(), roomID); err != nil {
		s.writeBoardError(w, err)
		return
	}
	snapshot, err := s.Boards.ExportSnapshot(roomID)
	if err != nil {
		s.writeBoardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) boardRestore(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) || !s.requireStaff(w, r) {
		return
	}
	var payload struct {
		Version int64 `json:"version"`
	}
	if !decode(w, r, &payload) {
		return
	}
	snapshot, err := s.Boards.Restore(r.Context(), r.PathValue("roomID"), payload.Version)
	if err != nil {
		s.writeBoardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) writeBoardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, clusterstate.ErrFenced):
		writeWriterFenced(w)
	case errors.Is(err, boards.ErrRoomNotFound), errors.Is(err, boards.ErrSnapshotNotFound):
		detail(w, http.StatusNotFound, err.Error())
	case errors.Is(err, boards.ErrPersistenceUnavailable):
		detail(w, http.StatusServiceUnavailable, err.Error())
	default:
		detail(w, http.StatusServiceUnavailable, "board persistence unavailable")
	}
}

func (s *Server) listQuizDrafts(w http.ResponseWriter, r *http.Request) {
	user, ok := s.teacherUser(w, r)
	if !ok {
		return
	}
	rows, err := s.Store.ListQuizDrafts(r.Context(), user.UserID, clamp(queryInt(r, "limit", 50), 1, 100))
	if err != nil {
		detail(w, 503, "data layer unavailable")
		return
	}
	if rows == nil {
		rows = []model.QuizDraft{}
	}
	writeJSON(w, 200, rows)
}

func (s *Server) createQuizDraft(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	user, ok := s.teacherUser(w, r)
	if !ok {
		return
	}
	var payload quizDraftPayload
	if !decode(w, r, &payload) {
		return
	}
	draft, err := s.quizDraftFromPayload(payload, user.UserID, "")
	if err != nil {
		s.writeQuizError(w, err)
		return
	}
	created, err := s.Store.CreateQuizDraft(r.Context(), draft)
	if err != nil {
		s.writeQuizError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) getQuizDraft(w http.ResponseWriter, r *http.Request) {
	user, ok := s.teacherUser(w, r)
	if !ok {
		return
	}
	draft, err := s.Store.GetQuizDraft(r.Context(), r.PathValue("draftID"))
	if err != nil {
		detail(w, 503, "data layer unavailable")
		return
	}
	if draft == nil {
		detail(w, 404, "quiz draft not found")
		return
	}
	if draft.OwnerID != user.UserID {
		detail(w, 403, "quiz ownership violation")
		return
	}
	writeJSON(w, 200, draft)
}

func (s *Server) updateQuizDraft(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	user, ok := s.teacherUser(w, r)
	if !ok {
		return
	}
	var payload quizDraftPayload
	if !decode(w, r, &payload) {
		return
	}
	draft, err := s.quizDraftFromPayload(payload, user.UserID, r.PathValue("draftID"))
	if err != nil {
		s.writeQuizError(w, err)
		return
	}
	updated, err := s.Store.UpdateQuizDraft(r.Context(), user.UserID, draft)
	if err != nil {
		s.writeQuizError(w, err)
		return
	}
	writeJSON(w, 200, updated)
}

func (s *Server) deleteQuizDraft(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	user, ok := s.teacherUser(w, r)
	if !ok {
		return
	}
	deleted, err := s.Store.DeleteQuizDraft(r.Context(), r.PathValue("draftID"), user.UserID)
	if err != nil {
		s.writeQuizError(w, err)
		return
	}
	if !deleted {
		detail(w, 404, "quiz draft not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) publishQuizDraft(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	user, ok := s.teacherUser(w, r)
	if !ok {
		return
	}
	meta, err := s.Store.PublishQuizDraft(r.Context(), r.PathValue("draftID"), user.UserID)
	if err != nil {
		s.writeQuizError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, meta)
}

func (s *Server) duplicateQuizDraft(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	user, ok := s.teacherUser(w, r)
	if !ok {
		return
	}
	var payload struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if r.Body != nil && r.ContentLength != 0 && !decode(w, r, &payload) {
		return
	}
	draft, err := s.Store.DuplicateQuiz(r.Context(), r.PathValue("draftID"), user.UserID, slugify(payload.ID), payload.Title)
	if err != nil {
		s.writeQuizError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, draft)
}

func (s *Server) duplicateQuiz(w http.ResponseWriter, r *http.Request) {
	if !s.requireActive(w) {
		return
	}
	user, ok := s.teacherUser(w, r)
	if !ok {
		return
	}
	var payload struct {
		SourceID string `json:"source_id"`
		ID       string `json:"id"`
		Title    string `json:"title"`
	}
	if !decode(w, r, &payload) {
		return
	}
	if strings.TrimSpace(payload.SourceID) == "" {
		validation(w, "source_id is required")
		return
	}
	draft, err := s.Store.DuplicateQuiz(r.Context(), payload.SourceID, user.UserID, slugify(payload.ID), payload.Title)
	if err != nil {
		s.writeQuizError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, draft)
}

type questionPayload struct {
	Question string   `json:"question"`
	Options  []string `json:"options"`
	Answer   *int     `json:"answer"`
	Points   int      `json:"points,omitempty"`
	Feedback string   `json:"feedback,omitempty"`
}
type examPayload struct {
	ID               string            `json:"id"`
	Title            string            `json:"title"`
	Questions        []questionPayload `json:"questions"`
	OpensAt          *string           `json:"opens_at"`
	ClosesAt         *string           `json:"closes_at"`
	DurationMinutes  *int              `json:"duration_minutes"`
	MaxAttempts      *int              `json:"max_attempts"`
	AllowLate        *bool             `json:"allow_late"`
	LateGraceMinutes *int              `json:"late_grace_minutes"`
}

type quizDraftPayload struct {
	ID               string            `json:"id"`
	Title            string            `json:"title"`
	Questions        []questionPayload `json:"questions"`
	OpensAt          *string           `json:"opens_at"`
	ClosesAt         *string           `json:"closes_at"`
	DurationMinutes  *int              `json:"duration_minutes"`
	MaxAttempts      *int              `json:"max_attempts"`
	AllowLate        *bool             `json:"allow_late"`
	LateGraceMinutes *int              `json:"late_grace_minutes"`
}

func (s *Server) createExam(w http.ResponseWriter, r *http.Request) {
	if !s.requireStaff(w, r) || !s.requireActive(w) {
		return
	}
	var p examPayload
	if !decode(w, r, &p) || !validateExam(w, p) {
		return
	}
	id := slugify(p.ID)
	if p.ID == "" {
		id = slugify(p.Title)
	}
	if p.ID != "" {
		if found, err := s.Store.GetExam(r.Context(), id, false); err != nil {
			detail(w, 503, "data layer unavailable")
			return
		} else if found != nil {
			detail(w, 409, fmt.Sprintf("exam id '%s' already exists", id))
			return
		}
	} else {
		base := id
		for suffix := 1; ; suffix++ {
			found, err := s.Store.GetExam(r.Context(), id, false)
			if err != nil {
				detail(w, 503, "data layer unavailable")
				return
			}
			if found == nil {
				break
			}
			id = fmt.Sprintf("%s-%d", base, suffix+1)
			if suffix >= 50 {
				id = "quiz-" + randomSuffix()
				break
			}
		}
	}
	qs := make([]model.Question, len(p.Questions))
	for i, q := range p.Questions {
		qs[i] = model.Question{Index: i, Question: q.Question, Options: trimStrings(q.Options), Answer: *q.Answer, Points: q.Points, Feedback: strings.TrimSpace(q.Feedback)}
	}
	window := payloadWindow(p.OpensAt, p.ClosesAt, p.DurationMinutes)
	if p.MaxAttempts != nil {
		window["max_attempts"] = *p.MaxAttempts
	}
	if p.AllowLate != nil {
		window["allow_late"] = *p.AllowLate
	}
	if p.LateGraceMinutes != nil {
		window["late_grace_minutes"] = *p.LateGraceMinutes
	}
	if err := s.Store.EnsureExam(r.Context(), id, strings.TrimSpace(p.Title), qs, window); err != nil {
		s.writeExamError(w, err)
		return
	}
	body := map[string]any{"id": id, "title": strings.TrimSpace(p.Title), "question_count": len(qs)}
	for k, v := range window {
		body[k] = v
	}
	writeJSON(w, 201, body)
}

func (s *Server) patchExam(w http.ResponseWriter, r *http.Request) {
	if !s.requireStaff(w, r) || !s.requireActive(w) {
		return
	}
	id := r.PathValue("examID")
	if meta, err := s.Store.GetExamMeta(r.Context(), id); err != nil {
		detail(w, 503, "data layer unavailable")
		return
	} else if meta == nil {
		detail(w, 404, "exam not found")
		return
	}
	var p struct {
		Title            *string `json:"title"`
		OpensAt          *string `json:"opens_at"`
		ClosesAt         *string `json:"closes_at"`
		DurationMinutes  *int    `json:"duration_minutes"`
		MaxAttempts      *int    `json:"max_attempts"`
		AllowLate        *bool   `json:"allow_late"`
		LateGraceMinutes *int    `json:"late_grace_minutes"`
	}
	if !decode(w, r, &p) {
		return
	}
	if p.DurationMinutes != nil && (*p.DurationMinutes <= 0 || *p.DurationMinutes > 1440) {
		validation(w, "duration_minutes must be greater than 0 and at most 1440")
		return
	}
	window := payloadWindow(p.OpensAt, p.ClosesAt, p.DurationMinutes)
	if p.MaxAttempts != nil {
		window["max_attempts"] = *p.MaxAttempts
	}
	if p.AllowLate != nil {
		window["allow_late"] = *p.AllowLate
	}
	if p.LateGraceMinutes != nil {
		window["late_grace_minutes"] = *p.LateGraceMinutes
	}
	updated, err := s.Store.UpdateExamWindow(r.Context(), id, p.Title, window)
	if err != nil {
		s.writeExamError(w, err)
		return
	}
	body := map[string]any{"id": id, "title": updated.Title}
	if updated.OpensAt != "" {
		body["opens_at"] = updated.OpensAt
	}
	if updated.ClosesAt != "" {
		body["closes_at"] = updated.ClosesAt
	}
	if updated.DurationMinutes > 0 {
		body["duration_minutes"] = updated.DurationMinutes
	}
	writeJSON(w, 200, body)
}

func (s *Server) closeExam(w http.ResponseWriter, r *http.Request) {
	if !s.requireStaff(w, r) || !s.requireActive(w) {
		return
	}
	id := r.PathValue("examID")
	if meta, err := s.Store.GetExamMeta(r.Context(), id); err != nil {
		detail(w, 503, "data layer unavailable")
		return
	} else if meta == nil {
		detail(w, 404, "exam not found")
		return
	}
	now := s.now().UTC().Format(time.RFC3339Nano)
	if _, err := s.Store.UpdateExamWindow(r.Context(), id, nil, map[string]any{"closes_at": now}); err != nil {
		s.writeExamError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "closed_at": now})
}

func (s *Server) deleteExam(w http.ResponseWriter, r *http.Request) {
	if !s.requireStaff(w, r) || !s.requireActive(w) {
		return
	}
	n, err := s.Store.DeleteExam(r.Context(), r.PathValue("examID"))
	if err != nil {
		s.writeExamError(w, err)
		return
	}
	if n == 0 {
		detail(w, 404, "exam not found")
		return
	}
	w.WriteHeader(204)
}

func (s *Server) examResults(w http.ResponseWriter, r *http.Request) {
	if !s.requireStaff(w, r) {
		return
	}
	id := r.PathValue("examID")
	rows, err := s.Store.ListSubmissions(r.Context(), id, 1000)
	if err != nil {
		detail(w, 503, "data layer unavailable")
		return
	}
	items := make([]map[string]any, 0, len(rows))
	totalPct := 0.0
	scored := 0
	for _, row := range rows {
		if row.Total > 0 {
			totalPct += float64(row.Score) / float64(row.Total) * 100
			scored++
		}
		items = append(items, map[string]any{"student_id": strings.TrimPrefix(row.SK, "STUDENT#"), "student_name": row.StudentName, "answers": row.Answers, "score": row.Score, "total": row.Total, "submitted_at": row.UpdatedAtISO})
	}
	questionStats := make([]map[string]any, 0)
	if exam, err := s.Store.GetExam(r.Context(), id, true); err == nil && exam != nil {
		for _, question := range exam.Questions {
			optionCounts := make([]int, len(question.Options))
			answered, correct := 0, 0
			for _, row := range rows {
				chosen, ok := row.Answers[strconv.Itoa(question.Index)]
				if !ok {
					continue
				}
				answered++
				if chosen >= 0 && chosen < len(optionCounts) {
					optionCounts[chosen]++
				}
				if chosen == question.Answer {
					correct++
				}
			}
			var correctPct any
			if answered > 0 {
				correctPct = float64(int((float64(correct)/float64(answered)*100)*10+0.5)) / 10
			}
			questionStats = append(questionStats, map[string]any{
				"index": question.Index, "question": question.Question, "answered": answered,
				"correct": correct, "correct_pct": correctPct, "option_counts": optionCounts,
			})
		}
	}
	var avg any
	if scored > 0 {
		avg = float64(int((totalPct/float64(scored))*10+0.5)) / 10
	}
	writeJSON(w, 200, map[string]any{"exam_id": id, "count": len(rows), "average_pct": avg, "submissions": items, "question_stats": questionStats})
}

func (s *Server) outbox(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	rows, err := s.Store.ListOutbox(r.Context(), clamp(queryInt(r, "limit", 50), 1, 200))
	if err != nil {
		detail(w, 503, "data layer unavailable")
		return
	}
	writeJSON(w, 200, rows)
}
func (s *Server) submissions(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	rows, err := s.Store.ListSubmissions(r.Context(), "", queryInt(r, "limit", 50))
	if err != nil {
		detail(w, 503, "data layer unavailable")
		return
	}
	writeJSON(w, 200, submissionMaps(rows))
}
func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, 200, map[string]any{"region": s.Settings.Region, "role": s.Roles.Current(), "boards": map[string]any{"active_rooms": s.Boards.ActiveRooms()}, "chaos": s.Chaos.Snapshot()})
}
func (s *Server) setChaos(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var p chaos.Patch
	if !decode(w, r, &p) {
		return
	}
	s.Chaos.Configure(p)
	writeJSON(w, 200, map[string]any{"chaos": s.Chaos.Snapshot()})
}
func (s *Server) resetChaos(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.Chaos.Reset()
	writeJSON(w, 200, map[string]any{"chaos": s.Chaos.Snapshot()})
}
func (s *Server) promote(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	s.Roles.Promote()
	writeJSON(w, 200, map[string]any{"region": s.Settings.Region, "role": s.Roles.Current()})
}

func (s *Server) currentUser(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	u, err := s.Auth.CurrentUser(r.Context(), r.Header.Get("Authorization"))
	if err == nil {
		return u, true
	}
	switch {
	case errors.Is(err, auth.ErrAuthenticationRequired):
		detail(w, 401, "authentication required")
	case errors.Is(err, auth.ErrAccountDeleted):
		detail(w, 401, "account no longer exists")
	case errors.Is(err, auth.ErrInvalidSession):
		detail(w, 401, "invalid or expired session")
	default:
		detail(w, 503, "data layer unavailable")
	}
	return nil, false
}
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("x-admin-token") != s.Settings.AdminToken {
		detail(w, 403, "invalid admin token")
		return false
	}
	return true
}
func (s *Server) requireActive(w http.ResponseWriter) bool {
	if s.Settings.FencingEnabled {
		return true
	}
	if s.Roles.Current() != "active" {
		detail(w, 503, "standby node is read-only; promote to serve traffic")
		return false
	}
	return true
}
func (s *Server) requireStaff(w http.ResponseWriter, r *http.Request) bool {
	u, err := s.Auth.ResolveOptionalUser(r.Context(), r.Header.Get("Authorization"))
	if err != nil {
		detail(w, 503, "data layer unavailable")
		return false
	}
	_, err = s.Auth.TeacherOrAdmin(r.Context(), u, r.Header.Get("x-admin-token"))
	if err == nil {
		return true
	}
	if errors.Is(err, auth.ErrAuthenticationRequired) {
		detail(w, 401, err.Error())
	} else {
		detail(w, 403, err.Error())
	}
	return false
}

func (s *Server) teacherUser(w http.ResponseWriter, r *http.Request) (*model.User, bool) {
	user, ok := s.currentUser(w, r)
	if !ok {
		return nil, false
	}
	if user.Role != auth.RoleTeacher {
		detail(w, http.StatusForbidden, "teacher access required")
		return nil, false
	}
	return user, true
}

func (s *Server) quizDraftFromPayload(payload quizDraftPayload, ownerID, forcedID string) (*model.QuizDraft, error) {
	id := strings.TrimSpace(forcedID)
	if id == "" {
		id = strings.TrimSpace(payload.ID)
	}
	if id == "" {
		id = slugify(payload.Title) + "-draft"
	} else {
		id = slugify(id)
	}
	if strings.TrimSpace(payload.Title) == "" {
		return nil, fmt.Errorf("quiz draft title is required")
	}
	if len(payload.Questions) == 0 {
		return nil, fmt.Errorf("at least one question is required")
	}
	if len(payload.Questions) > 100 {
		return nil, fmt.Errorf("quiz draft has too many questions")
	}
	questions := make([]model.Question, len(payload.Questions))
	for index, question := range payload.Questions {
		if strings.TrimSpace(question.Question) == "" || len(question.Options) < 2 {
			return nil, fmt.Errorf("question %d needs text and at least two non-empty options", index)
		}
		for _, option := range question.Options {
			if strings.TrimSpace(option) == "" {
				return nil, fmt.Errorf("question %d needs non-empty options", index)
			}
		}
		if question.Answer == nil || *question.Answer < 0 || *question.Answer >= len(question.Options) {
			return nil, fmt.Errorf("question %d answer index out of range", index)
		}
		if question.Points < 0 || question.Points > 1000 {
			return nil, fmt.Errorf("question %d points must be between 0 and 1000", index)
		}
		questions[index] = model.Question{
			Index: index, Question: strings.TrimSpace(question.Question), Options: trimStrings(question.Options),
			Answer: *question.Answer, Points: question.Points, Feedback: strings.TrimSpace(question.Feedback),
		}
	}
	if payload.DurationMinutes != nil && (*payload.DurationMinutes <= 0 || *payload.DurationMinutes > 1440) {
		return nil, fmt.Errorf("duration_minutes must be greater than 0 and at most 1440")
	}
	if payload.MaxAttempts != nil && (*payload.MaxAttempts <= 0 || *payload.MaxAttempts > 20) {
		return nil, fmt.Errorf("max_attempts must be between 1 and 20")
	}
	if payload.LateGraceMinutes != nil && (*payload.LateGraceMinutes < 0 || *payload.LateGraceMinutes > 10080) {
		return nil, fmt.Errorf("late_grace_minutes must be between 0 and 10080")
	}
	if payload.OpensAt != nil && payload.ClosesAt != nil {
		opens, openErr := time.Parse(time.RFC3339Nano, *payload.OpensAt)
		closes, closeErr := time.Parse(time.RFC3339Nano, *payload.ClosesAt)
		if openErr != nil || closeErr != nil || !closes.After(opens) {
			return nil, fmt.Errorf("closes_at must be after opens_at")
		}
	}
	draft := &model.QuizDraft{ID: id, OwnerID: ownerID, Title: strings.TrimSpace(payload.Title), Questions: questions}
	if payload.OpensAt != nil {
		draft.OpensAt = strings.TrimSpace(*payload.OpensAt)
	}
	if payload.ClosesAt != nil {
		draft.ClosesAt = strings.TrimSpace(*payload.ClosesAt)
	}
	if payload.DurationMinutes != nil {
		draft.DurationMinutes = *payload.DurationMinutes
	}
	if payload.MaxAttempts != nil {
		draft.MaxAttempts = *payload.MaxAttempts
	}
	if payload.AllowLate != nil {
		draft.AllowLate = *payload.AllowLate
	}
	if payload.LateGraceMinutes != nil {
		draft.LateGraceMinutes = *payload.LateGraceMinutes
	}
	return draft, nil
}

func trimStrings(values []string) []string {
	trimmed := make([]string, len(values))
	for i, value := range values {
		trimmed[i] = strings.TrimSpace(value)
	}
	return trimmed
}

func attemptResponse(attempt *model.Attempt, deadline *int64) map[string]any {
	response := map[string]any{"answers": map[string]int{}, "version": 0}
	if attempt != nil {
		answers := attempt.Answers
		if answers == nil {
			answers = map[string]int{}
		}
		response["answers"] = answers
		response["version"] = attempt.Version
		response["started_at"] = attempt.StartedAt
		response["updated_at"] = attempt.UpdatedAt
		response["attempt_number"] = attempt.AttemptNumber
	}
	if deadline != nil {
		response["deadline_ms"] = *deadline
	}
	return response
}

func (s *Server) writeQuizError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	var window *exams.WindowError
	if errors.As(err, &window) {
		writeJSON(w, http.StatusForbidden, map[string]any{"detail": map[string]any{"code": window.Code, "message": window.Message}})
		return
	}
	switch {
	case errors.Is(err, clusterstate.ErrFenced):
		writeWriterFenced(w)
	case errors.Is(err, store.ErrQuizNotFound):
		detail(w, http.StatusNotFound, "quiz not found")
	case errors.Is(err, store.ErrQuizForbidden):
		detail(w, http.StatusForbidden, "quiz ownership violation")
	case errors.Is(err, store.ErrQuizConflict), errors.Is(err, store.ErrAttemptConflict), errors.Is(err, store.ErrAttemptSubmitted):
		detail(w, http.StatusConflict, err.Error())
	case errors.Is(err, exams.ErrAttemptNotFound):
		detail(w, http.StatusNotFound, "no saved attempt")
	case errors.Is(err, store.ErrInvalidAnswer):
		validation(w, err.Error())
	default:
		validation(w, err.Error())
	}
}
func (s *Server) writeExamError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	var window *exams.WindowError
	switch {
	case errors.As(err, &window):
		writeJSON(w, 403, map[string]any{"detail": map[string]any{"code": window.Code, "message": window.Message}})
	case errors.Is(err, exams.ErrExamNotFound):
		detail(w, 404, "exam not found")
	case errors.Is(err, store.ErrSubmissionConflict):
		detail(w, 409, "this student already submitted for this exam")
	case errors.Is(err, store.ErrSubmissionRequestConflict):
		writeJSON(w, http.StatusConflict, map[string]any{"detail": map[string]any{"code": "REQUEST_ID_CONFLICT", "message": err.Error()}})
	case errors.Is(err, clusterstate.ErrFenced):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": map[string]any{"code": "WRITER_FENCED", "message": "this backend is no longer the active writer", "retryable": true}})
	case errors.Is(err, exams.ErrAttemptNotFound):
		detail(w, 404, "no saved attempt")
	case errors.Is(err, store.ErrAttemptConflict), errors.Is(err, store.ErrAttemptSubmitted):
		detail(w, 409, err.Error())
	case errors.Is(err, store.ErrInvalidAnswer):
		validation(w, err.Error())
	default:
		detail(w, 503, "data layer unavailable")
	}
	return true
}

func writeWriterFenced(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{"detail": map[string]any{
		"code": "WRITER_FENCED", "message": "this backend is no longer the active writer", "retryable": true,
	}})
}

func (s *Server) cors(next http.Handler) http.Handler {
	allowed := map[string]bool{}
	for _, o := range s.Settings.CORSOrigins {
		allowed[o] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if allowed[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Methods", "DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT")
			w.Header().Set("Access-Control-Allow-Headers", "*")
			w.Header().Set("Access-Control-Expose-Headers", "x-request-id")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(200)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(dst); err != nil {
		validation(w, "invalid request body")
		return false
	}
	// The API rejects extra JSON values after the request object. Without
	// this second decode, Go would silently accept bodies such as
	// {"email":"x"}{"password":"y"}.
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		validation(w, "invalid request body")
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func detail(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"detail": message})
}
func validation(w http.ResponseWriter, message string) {
	writeJSON(w, 422, map[string]any{"detail": message})
}
func validAccountPayload(w http.ResponseWriter, email, password, name string) bool {
	if !auth.ValidEmail(email) || len(email) > 120 {
		validation(w, "invalid email")
		return false
	}
	if len(password) < 8 || len(password) > 128 {
		validation(w, "password must contain 8 to 128 characters")
		return false
	}
	if len(name) > 60 {
		validation(w, "name must contain at most 60 characters")
		return false
	}
	return true
}

func validateExam(w http.ResponseWriter, p examPayload) bool {
	if strings.TrimSpace(p.Title) == "" {
		detail(w, 400, "title must not be empty")
		return false
	}
	if len(p.Questions) == 0 {
		detail(w, 400, "at least one question is required")
		return false
	}
	for i, q := range p.Questions {
		if strings.TrimSpace(q.Question) == "" || len(q.Options) < 2 {
			detail(w, 400, fmt.Sprintf("question %d needs text and at least two non-empty options", i))
			return false
		}
		for _, option := range q.Options {
			if strings.TrimSpace(option) == "" {
				detail(w, 400, fmt.Sprintf("question %d needs text and at least two non-empty options", i))
				return false
			}
		}
		if q.Answer == nil || *q.Answer < 0 || *q.Answer >= len(q.Options) {
			detail(w, 400, fmt.Sprintf("question %d answer index out of range", i))
			return false
		}
		if q.Points < 0 || q.Points > 1000 {
			validation(w, fmt.Sprintf("question %d points must be between 0 and 1000", i))
			return false
		}
	}
	if p.DurationMinutes != nil && (*p.DurationMinutes <= 0 || *p.DurationMinutes > 1440) {
		validation(w, "duration_minutes must be greater than 0 and at most 1440")
		return false
	}
	if p.MaxAttempts != nil && (*p.MaxAttempts <= 0 || *p.MaxAttempts > 20) {
		validation(w, "max_attempts must be between 1 and 20")
		return false
	}
	if p.LateGraceMinutes != nil && (*p.LateGraceMinutes < 0 || *p.LateGraceMinutes > 10080) {
		validation(w, "late_grace_minutes must be between 0 and 10080")
		return false
	}
	if p.OpensAt != nil && p.ClosesAt != nil {
		opens, e1 := time.Parse(time.RFC3339Nano, *p.OpensAt)
		closes, e2 := time.Parse(time.RFC3339Nano, *p.ClosesAt)
		if e1 != nil || e2 != nil || !closes.After(opens) {
			validation(w, "closes_at must be after opens_at")
			return false
		}
	}
	return true
}
func payloadWindow(opens, closes *string, duration *int) map[string]any {
	out := map[string]any{}
	if opens != nil {
		out["opens_at"] = *opens
	}
	if closes != nil {
		out["closes_at"] = *closes
	}
	if duration != nil {
		out["duration_minutes"] = *duration
	}
	return out
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(value string) string {
	slug := strings.Trim(slugRE.ReplaceAllString(strings.ToLower(value), "-"), "-")
	if slug == "" {
		return "quiz-" + randomSuffix()
	}
	return slug
}
func randomSuffix() string { return fmt.Sprintf("%08x", time.Now().UnixNano())[:8] }
func queryInt(r *http.Request, key string, fallback int) int {
	value := r.URL.Query().Get(key)
	var parsed int
	if _, err := fmt.Sscan(value, &parsed); err != nil {
		return fallback
	}
	return parsed
}
func clamp(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}
func submissionMaps(rows []model.Submission) []map[string]any {
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{"pk": row.PK, "sk": row.SK, "answers": row.Answers, "version": row.Version, "request_id": row.RequestID, "updated_at": row.UpdatedAt, "updated_at_iso": row.UpdatedAtISO, "written_by_region": row.WrittenByRegion, "student_name": row.StudentName, "score": row.Score, "total": row.Total})
	}
	sort.SliceStable(out, func(i, j int) bool { return fmt.Sprint(out[i]["updated_at"]) < fmt.Sprint(out[j]["updated_at"]) })
	return out
}
