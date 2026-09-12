// Package exams contains exam window, attempt-clock, and submission rules.
// It is intentionally independent of HTTP so API and workflow handlers share
// exactly the same behavior.
package exams

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ayush/slate/internal/model"
)

var ErrExamNotFound = errors.New("exam not found")

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
	SubmitAttempt(context.Context, string, string, string, map[string]int, string) (model.SubmissionResult, error)
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
// Invalid stored ISO boundaries are ignored for compatibility with Python's
// corruption-tolerant serving behavior.
func EnforceWindow(meta *model.ExamMeta, role string, now time.Time) error {
	if meta == nil || role != "student" {
		return nil
	}
	if boundary, ok := parseBoundary(meta.OpensAt); ok && now.Before(boundary) {
		return &WindowError{Code: "EXAM_NOT_OPEN", Message: "exam is not open yet"}
	}
	if boundary, ok := parseBoundary(meta.ClosesAt); ok && now.After(boundary) {
		return &WindowError{Code: "EXAM_CLOSED", Message: "exam window has closed"}
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
	if user == nil {
		return model.SubmissionResult{}, fmt.Errorf("user is required")
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
	if user.Role == "student" && meta.DurationMinutes > 0 {
		_, deadline, err := s.Store.AttemptDeadline(ctx, examID, user.UserID)
		if err != nil {
			return model.SubmissionResult{}, err
		}
		if deadline != nil && s.now().UnixMilli() > *deadline+s.graceMS() {
			return model.SubmissionResult{}, &WindowError{Code: "ATTEMPT_EXPIRED", Message: "time is up for this attempt"}
		}
	}
	return s.Store.SubmitAttempt(ctx, examID, user.UserID, user.Name, answers, requestID)
}
func (s *Service) graceMS() int64 {
	grace := s.GracePeriod
	if grace <= 0 {
		grace = 5 * time.Second
	}
	return grace.Milliseconds()
}
