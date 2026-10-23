// Package exams contains exam window, attempt-clock, and submission rules.
// It is intentionally independent of HTTP so API and workflow handlers share
// exactly the same behavior.
package exams

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
)

var ErrExamNotFound = errors.New("exam not found")
var ErrAttemptNotFound = errors.New("attempt not found")
var ErrInvalidRequest = errors.New("invalid attempt request")

func validateRequestID(requestID string) error {
	if len(requestID) > 256 || strings.IndexFunc(requestID, func(r rune) bool { return r < 32 || r == 127 }) >= 0 {
		return fmt.Errorf("%w: request_id must be at most 256 bytes and contain no control characters", ErrInvalidRequest)
	}
	return nil
}

type WindowError struct {
	Code    string
	Message string
}

func (e *WindowError) Error() string { return e.Message }

type Store interface {
	GetExam(context.Context, string, bool) (*model.Exam, error)
	GetExamMeta(context.Context, string) (*model.ExamMeta, error)
	RecordAttemptStart(context.Context, string, string) (*model.Attempt, error)
	AttemptDeadline(context.Context, string, string) (*model.Attempt, *int64, error)
	GetStudentAttempt(context.Context, string, string) (*model.Attempt, error)
	SaveAttempt(context.Context, string, string, map[string]int, int, string) (*model.Attempt, error)
	SubmitAttempt(context.Context, string, string, string, map[string]int, string) (model.SubmissionResult, error)
}

// SubmissionRequestLookup lets supported stores resolve an exact retry before
// exam-window and attempt-deadline validation. This preserves the committed
// receipt after a response is lost, even if the exam closes before retry.
type SubmissionRequestLookup interface {
	FindSubmissionRequest(context.Context, string, string, map[string]int, string) (model.SubmissionResult, bool, error)
}

type attemptNumberSaver interface {
	SaveAttemptForAttempt(context.Context, string, string, map[string]int, int, string, int) (*model.Attempt, error)
}

type attemptNumberSubmitter interface {
	SubmitAttemptForAttempt(context.Context, string, string, string, map[string]int, string, int) (model.SubmissionResult, error)
}

type attemptNumberReceiptLookup interface {
	FindSubmissionRequestForAttempt(context.Context, string, string, map[string]int, string, int) (model.SubmissionResult, bool, error)
}

type submissionReceiptLookup interface {
	FindSubmissionReceiptByID(context.Context, string, string, string, int) (model.SubmissionResult, bool, error)
}

type retakeStore interface {
	RetakeAttempt(context.Context, string, string, int) (*model.Attempt, error)
}

type retakeMetaStore interface {
	RetakeAttemptWithMeta(context.Context, string, string, int, *model.ExamMeta) (*model.Attempt, error)
}

// QuizStore is the persistence surface for teacher-owned quiz authoring. It
// is deliberately separate from Store so the student delivery service stays
// small while both DynamoDB and MemoryStore expose the same behavior.
type QuizStore interface {
	CreateQuizDraft(context.Context, *model.QuizDraft) (*model.QuizDraft, error)
	GetQuizDraft(context.Context, string) (*model.QuizDraft, error)
	ListQuizDrafts(context.Context, string, int) ([]model.QuizDraft, error)
	UpdateQuizDraft(context.Context, string, *model.QuizDraft) (*model.QuizDraft, error)
	DeleteQuizDraft(context.Context, string, string) (bool, error)
	PublishQuizDraft(context.Context, string, string) (*model.ExamMeta, error)
	DuplicateQuiz(context.Context, string, string, string, string) (*model.QuizDraft, error)
}

type Service struct {
	Store       Store
	Now         func() time.Time
	GracePeriod time.Duration
}

func New(store Store) *Service {
	return &Service{Store: store, Now: time.Now, GracePeriod: 5 * time.Second}
}
func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// EnforceWindow allows staff (any role other than student) to preview an exam.
// Invalid stored ISO boundaries are ignored for compatibility with the
// corruption-tolerant serving behavior.
func EnforceWindow(meta *model.ExamMeta, role string, now time.Time) error {
	if meta == nil || role != "student" {
		return nil
	}
	if boundary, ok := parseBoundary(meta.OpensAt); ok && now.Before(boundary) {
		return &WindowError{Code: "EXAM_NOT_OPEN", Message: "exam is not open yet"}
	}
	if boundary, ok := parseBoundary(meta.ClosesAt); ok && now.After(boundary) {
		if !meta.AllowLate {
			return &WindowError{Code: "EXAM_CLOSED", Message: "exam window has closed"}
		}
		if meta.LateGraceMinutes > 0 && now.After(boundary.Add(time.Duration(meta.LateGraceMinutes)*time.Minute)) {
			return &WindowError{Code: "LATE_WINDOW_CLOSED", Message: "the late submission window has closed"}
		}
	}
	return nil
}

func parseBoundary(raw string) (time.Time, bool) {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339, raw)
	}
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func (s *Service) Get(ctx context.Context, examID string, user *model.User) (*model.Exam, error) {
	if user == nil {
		return nil, fmt.Errorf("%w: user is required", ErrInvalidRequest)
	}
	exam, err := s.Store.GetExam(ctx, examID, false)
	if err != nil {
		return nil, err
	}
	if exam == nil {
		return nil, ErrExamNotFound
	}
	if err := EnforceWindow(&exam.ExamMeta, userRole(user), s.now()); err != nil {
		return nil, err
	}
	if userRole(user) == "student" {
		attempt, err := s.Store.RecordAttemptStart(ctx, examID, user.UserID)
		if err != nil {
			return nil, err
		}
		if attempt.AttemptNumber <= 0 {
			attempt.AttemptNumber = 1
		}
		exam.Attempt = attempt
		exam.AttemptStartedAt = attempt.StartedAt
		if exam.DurationMinutes > 0 {
			exam.DeadlineMS = attempt.StartedAt + int64(exam.DurationMinutes)*60_000
		}
	}
	return exam, nil
}

func userRole(user *model.User) string {
	if user == nil {
		return ""
	}
	return user.Role
}

func (s *Service) Submit(ctx context.Context, examID string, user *model.User, answers map[string]int, requestID string) (model.SubmissionResult, error) {
	return s.SubmitForAttempt(ctx, examID, user, answers, requestID, 0)
}

// SubmitForAttempt binds the request to one attempt when the client supplies
// an attempt number. The zero value retains the legacy attempt-one contract.
func (s *Service) SubmitForAttempt(ctx context.Context, examID string, user *model.User, answers map[string]int, requestID string, attemptNumber int) (model.SubmissionResult, error) {
	if err := validateRequestID(requestID); err != nil {
		return model.SubmissionResult{}, err
	}
	if user == nil {
		return model.SubmissionResult{}, fmt.Errorf("user is required")
	}
	if attemptNumber < 0 {
		return model.SubmissionResult{}, fmt.Errorf("attempt_number must be positive")
	}
	if answers == nil {
		return model.SubmissionResult{}, fmt.Errorf("answers are required")
	}
	if strings.TrimSpace(requestID) != "" {
		if lookup, ok := s.Store.(attemptNumberReceiptLookup); ok {
			result, found, err := lookup.FindSubmissionRequestForAttempt(ctx, examID, user.UserID, answers, requestID, attemptNumber)
			if err != nil {
				return model.SubmissionResult{}, err
			}
			if found {
				return result, nil
			}
		} else if lookup, ok := s.Store.(SubmissionRequestLookup); ok {
			result, found, err := lookup.FindSubmissionRequest(ctx, examID, user.UserID, answers, requestID)
			if err != nil {
				return model.SubmissionResult{}, err
			}
			if found {
				return result, nil
			}
		}
	}
	meta, err := s.Store.GetExamMeta(ctx, examID)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	if meta == nil {
		return model.SubmissionResult{}, ErrExamNotFound
	}
	if err := EnforceWindow(meta, user.Role, s.now()); err != nil {
		return model.SubmissionResult{}, err
	}
	if answers == nil {
		return model.SubmissionResult{}, fmt.Errorf("answers are required")
	}
	exam, err := s.Store.GetExam(ctx, examID, false)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	if exam == nil {
		return model.SubmissionResult{}, ErrExamNotFound
	}
	if err := validateAnswers(exam, answers); err != nil {
		return model.SubmissionResult{}, err
	}
	if user.Role == "student" && meta.DurationMinutes > 0 {
		_, deadline, err := s.Store.AttemptDeadline(ctx, examID, user.UserID)
		if err != nil {
			return model.SubmissionResult{}, err
		}
		if deadline != nil && s.now().UnixMilli() > *deadline+s.graceMS() {
			return model.SubmissionResult{}, &WindowError{Code: "ATTEMPT_EXPIRED", Message: "time is up for this attempt"}
		}
	}
	if submitter, ok := s.Store.(attemptNumberSubmitter); ok {
		return submitter.SubmitAttemptForAttempt(ctx, examID, user.UserID, user.Name, answers, requestID, attemptNumber)
	}
	return s.Store.SubmitAttempt(ctx, examID, user.UserID, user.Name, answers, requestID)
}

// SubmitSaved submits the server-held answer snapshot. It is useful for a
// client that reconnects after autosaving and intentionally avoids trusting a
// stale browser copy of the answer map.
func (s *Service) SubmitSaved(ctx context.Context, examID string, user *model.User, requestID string) (model.SubmissionResult, error) {
	return s.SubmitSavedForAttempt(ctx, examID, user, requestID, 0)
}

func (s *Service) SubmitSavedForAttempt(ctx context.Context, examID string, user *model.User, requestID string, attemptNumber int) (model.SubmissionResult, error) {
	if err := validateRequestID(requestID); err != nil {
		return model.SubmissionResult{}, err
	}
	if user == nil {
		return model.SubmissionResult{}, fmt.Errorf("user is required")
	}
	if lookup, ok := s.Store.(submissionReceiptLookup); ok && strings.TrimSpace(requestID) != "" {
		result, found, err := lookup.FindSubmissionReceiptByID(ctx, examID, user.UserID, requestID, attemptNumber)
		if err != nil {
			return model.SubmissionResult{}, err
		}
		if found {
			return result, nil
		}
	}
	attempt, err := s.Store.GetStudentAttempt(ctx, examID, user.UserID)
	if err != nil {
		return model.SubmissionResult{}, err
	}
	if attempt == nil || attempt.Answers == nil {
		return model.SubmissionResult{}, ErrAttemptNotFound
	}
	currentNumber := attemptNumberOrOne(attempt.AttemptNumber)
	if attemptNumber > 0 && attemptNumber != currentNumber || attemptNumber == 0 && currentNumber > 1 {
		return model.SubmissionResult{}, store.ErrAttemptConflict
	}
	return s.SubmitForAttempt(ctx, examID, user, attempt.Answers, requestID, attemptNumber)
}

// Resume starts the current attempt if necessary and returns the durable
// answer snapshot and server-derived deadline.
func (s *Service) Resume(ctx context.Context, examID string, user *model.User) (*model.Attempt, *int64, error) {
	if user == nil {
		return nil, nil, fmt.Errorf("user is required")
	}
	meta, err := s.Store.GetExamMeta(ctx, examID)
	if err != nil {
		return nil, nil, err
	}
	if meta == nil {
		return nil, nil, ErrExamNotFound
	}
	if err := EnforceWindow(meta, user.Role, s.now()); err != nil {
		return nil, nil, err
	}
	attempt, err := s.Store.RecordAttemptStart(ctx, examID, user.UserID)
	if err != nil {
		return nil, nil, err
	}
	if attempt.AttemptNumber <= 0 {
		attempt.AttemptNumber = 1
	}
	var deadline *int64
	if meta.DurationMinutes > 0 {
		value := attempt.StartedAt + int64(meta.DurationMinutes)*60_000
		deadline = &value
	}
	return attempt, deadline, nil
}

// Save validates a partial answer map and persists it with an optimistic
// version check. Reusing requestID is idempotent and does not advance the
// server version.
func (s *Service) Save(ctx context.Context, examID string, user *model.User, answers map[string]int, expectedVersion int, requestID string) (model.AttemptSaveResult, error) {
	return s.SaveForAttempt(ctx, examID, user, answers, expectedVersion, requestID, 0)
}

func (s *Service) SaveForAttempt(ctx context.Context, examID string, user *model.User, answers map[string]int, expectedVersion int, requestID string, attemptNumber int) (model.AttemptSaveResult, error) {
	if err := validateRequestID(requestID); err != nil {
		return model.AttemptSaveResult{}, err
	}
	if user == nil {
		return model.AttemptSaveResult{}, fmt.Errorf("user is required")
	}
	if attemptNumber < 0 {
		return model.AttemptSaveResult{}, fmt.Errorf("attempt_number must be positive")
	}
	if expectedVersion < 0 {
		return model.AttemptSaveResult{}, fmt.Errorf("%w: version must not be negative", ErrInvalidRequest)
	}
	if strings.TrimSpace(requestID) == "" {
		return model.AttemptSaveResult{}, fmt.Errorf("%w: request_id is required", ErrInvalidRequest)
	}
	meta, err := s.Store.GetExamMeta(ctx, examID)
	if err != nil {
		return model.AttemptSaveResult{}, err
	}
	if meta == nil {
		return model.AttemptSaveResult{}, ErrExamNotFound
	}
	if err := EnforceWindow(meta, user.Role, s.now()); err != nil {
		return model.AttemptSaveResult{}, err
	}
	exam, err := s.Store.GetExam(ctx, examID, false)
	if err != nil {
		return model.AttemptSaveResult{}, err
	}
	if exam == nil {
		return model.AttemptSaveResult{}, ErrExamNotFound
	}
	if answers == nil {
		return model.AttemptSaveResult{}, fmt.Errorf("answers are required")
	}
	if err := validateAnswers(exam, answers); err != nil {
		return model.AttemptSaveResult{}, err
	}
	before, err := s.Store.GetStudentAttempt(ctx, examID, user.UserID)
	if err != nil {
		return model.AttemptSaveResult{}, err
	}
	if before != nil {
		currentNumber := attemptNumberOrOne(before.AttemptNumber)
		if attemptNumber > 0 && attemptNumber != currentNumber || attemptNumber == 0 && currentNumber > 1 {
			return model.AttemptSaveResult{}, store.ErrAttemptConflict
		}
	} else if attemptNumber > 1 {
		return model.AttemptSaveResult{}, store.ErrAttemptConflict
	}
	if before != nil && before.LastRequestID == requestID {
		return s.attemptResult("duplicate", requestID, before, meta), nil
	}
	if meta.DurationMinutes > 0 && before != nil {
		deadline := before.StartedAt + int64(meta.DurationMinutes)*60_000
		if s.now().UnixMilli() > deadline+s.graceMS() {
			return model.AttemptSaveResult{}, &WindowError{Code: "ATTEMPT_EXPIRED", Message: "time is up for this attempt"}
		}
	}
	var saved *model.Attempt
	if saver, ok := s.Store.(attemptNumberSaver); ok {
		saved, err = saver.SaveAttemptForAttempt(ctx, examID, user.UserID, answers, expectedVersion, requestID, attemptNumber)
	} else {
		saved, err = s.Store.SaveAttempt(ctx, examID, user.UserID, answers, expectedVersion, requestID)
	}
	if err != nil {
		return model.AttemptSaveResult{}, err
	}
	return s.attemptResult("saved", requestID, saved, meta), nil
}

// Retake starts the next timed attempt only after the current one was
// submitted and only if the configured maximum allows it. The persistence
// layer makes expected_attempt_number idempotent under concurrent retries.
func (s *Service) Retake(ctx context.Context, examID string, user *model.User, expectedAttemptNumber int) (*model.Attempt, *int64, error) {
	if user == nil || user.Role != "student" {
		return nil, nil, fmt.Errorf("student is required")
	}
	if expectedAttemptNumber <= 0 {
		return nil, nil, fmt.Errorf("expected_attempt_number must be positive")
	}
	meta, err := s.Store.GetExamMeta(ctx, examID)
	if err != nil {
		return nil, nil, err
	}
	if meta == nil {
		return nil, nil, ErrExamNotFound
	}
	active, err := s.Store.GetStudentAttempt(ctx, examID, user.UserID)
	if err != nil {
		return nil, nil, err
	}
	if active != nil && attemptNumberOrOne(active.AttemptNumber) == expectedAttemptNumber+1 {
		var deadline *int64
		if meta.DurationMinutes > 0 {
			value := active.StartedAt + int64(meta.DurationMinutes)*60_000
			deadline = &value
		}
		return active, deadline, nil
	}
	if err := EnforceWindow(meta, user.Role, s.now()); err != nil {
		return nil, nil, err
	}
	retaker, ok := s.Store.(retakeStore)
	if !ok {
		return nil, nil, fmt.Errorf("retakes are unavailable")
	}
	var attempt *model.Attempt
	if snapshotRetaker, hasSnapshotRetaker := s.Store.(retakeMetaStore); hasSnapshotRetaker {
		attempt, err = snapshotRetaker.RetakeAttemptWithMeta(ctx, examID, user.UserID, expectedAttemptNumber, meta)
	} else {
		attempt, err = retaker.RetakeAttempt(ctx, examID, user.UserID, expectedAttemptNumber)
	}
	if err != nil {
		return nil, nil, err
	}
	var deadline *int64
	if meta.DurationMinutes > 0 {
		value := attempt.StartedAt + int64(meta.DurationMinutes)*60_000
		deadline = &value
	}
	return attempt, deadline, nil
}

func attemptNumberOrOne(number int) int {
	if number > 0 {
		return number
	}
	return 1
}

func (s *Service) attemptResult(status, requestID string, attempt *model.Attempt, meta *model.ExamMeta) model.AttemptSaveResult {
	result := model.AttemptSaveResult{Status: status, RequestID: requestID, Attempt: attempt}
	if attempt != nil && meta != nil && meta.DurationMinutes > 0 {
		result.DeadlineMS = attempt.StartedAt + int64(meta.DurationMinutes)*60_000
	}
	return result
}

func validateAnswers(exam *model.Exam, answers map[string]int) error {
	for raw, chosen := range answers {
		index, err := strconv.Atoi(raw)
		if err != nil || index < 0 || index >= len(exam.Questions) {
			return fmt.Errorf("answer index %q is invalid", raw)
		}
		if chosen < 0 || chosen >= len(exam.Questions[index].Options) {
			return fmt.Errorf("answer for question %d is out of range", index)
		}
	}
	return nil
}
func (s *Service) graceMS() int64 {
	grace := s.GracePeriod
	if grace <= 0 {
		grace = 5 * time.Second
	}
	return grace.Milliseconds()
}
