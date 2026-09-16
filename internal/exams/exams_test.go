package exams

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ayush/slate/internal/model"
	"github.com/ayush/slate/internal/store"
)

func TestWindowAndMalformedBoundary(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	m := &model.ExamMeta{OpensAt: now.Add(time.Hour).Format(time.RFC3339), ClosesAt: now.Add(2 * time.Hour).Format(time.RFC3339)}
	err := EnforceWindow(m, "student", now)
	var we *WindowError
	if !errors.As(err, &we) || we.Code != "EXAM_NOT_OPEN" {
		t.Fatalf("got %#v", err)
	}
	m.OpensAt = "corrupt"
	if err := EnforceWindow(m, "student", now); err != nil {
		t.Fatal(err)
	}
	if err := EnforceWindow(m, "teacher", now); err != nil {
		t.Fatal(err)
	}
}

func TestWindowRulesAndSmallHelpers(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if (&WindowError{Message: "window"}).Error() != "window" {
		t.Fatal("WindowError.Error returned the wrong message")
	}
	for _, tc := range []struct {
		name string
		meta model.ExamMeta
		want string
	}{
		{name: "closed", meta: model.ExamMeta{ClosesAt: now.Add(-time.Minute).Format(time.RFC3339)}, want: "EXAM_CLOSED"},
		{name: "late closed", meta: model.ExamMeta{ClosesAt: now.Add(-time.Hour).Format(time.RFC3339), AllowLate: true, LateGraceMinutes: 30}, want: "LATE_WINDOW_CLOSED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := EnforceWindow(&tc.meta, "student", now)
			var windowErr *WindowError
			if !errors.As(err, &windowErr) || windowErr.Code != tc.want {
				t.Fatalf("EnforceWindow = %#v", err)
			}
		})
	}
	if EnforceWindow(nil, "student", now) != nil || EnforceWindow(&model.ExamMeta{}, "teacher", now) != nil {
		t.Fatal("nil/staff window should be allowed")
	}
	if EnforceWindow(&model.ExamMeta{OpensAt: now.Add(-time.Minute).Format(time.RFC3339Nano), ClosesAt: now.Add(time.Minute).Format(time.RFC3339Nano)}, "student", now) != nil {
		t.Fatal("open window was rejected")
	}
	late := &model.ExamMeta{ClosesAt: now.Add(-time.Minute).Format(time.RFC3339), AllowLate: true, LateGraceMinutes: 5}
	if EnforceWindow(late, "student", now) != nil {
		t.Fatal("late grace window was rejected")
	}
	if userRole(nil) != "" || userRole(&model.User{Role: "student"}) != "student" {
		t.Fatal("userRole edges failed")
	}
	if (&Service{}).now().IsZero() || (&Service{Now: func() time.Time { return now }}).now() != now {
		t.Fatal("service clock edges failed")
	}
	if (&Service{}).graceMS() != (5*time.Second).Milliseconds() || (&Service{GracePeriod: time.Second}).graceMS() != 1000 {
		t.Fatal("grace period edges failed")
	}
	exam := &model.Exam{Questions: []model.Question{{Options: []string{"a", "b"}}}}
	if validateAnswers(exam, map[string]int{"0": 1}) != nil {
		t.Fatal("valid answer rejected")
	}
	for _, answers := range []map[string]int{{"bad": 0}, {"-1": 0}, {"1": 0}, {"0": -1}, {"0": 2}} {
		if validateAnswers(exam, answers) == nil {
			t.Fatalf("invalid answers accepted: %#v", answers)
		}
	}
}

func TestServiceGetSubmitResumeAndSaveValidation(t *testing.T) {
	ctx := context.Background()
	student := &model.User{UserID: "student", Name: "Student", Role: "student"}
	teacher := &model.User{UserID: "teacher", Name: "Teacher", Role: "teacher"}
	meta := &model.ExamMeta{ID: "exam", Title: "Exam", QuestionCount: 1, DurationMinutes: 1}
	exam := &model.Exam{ExamMeta: *meta, Questions: []model.Question{{Index: 0, Options: []string{"a", "b"}}}}
	attempt := &model.Attempt{StartedAt: 1000, Version: 2, Answers: map[string]int{"0": 1}}
	now := time.UnixMilli(1000)
	newService := func(store *stubExamStore) *Service {
		return &Service{Store: store, Now: func() time.Time { return now }, GracePeriod: time.Second}
	}
	if New(&stubExamStore{}) == nil {
		t.Fatal("New returned nil")
	}
	windowMeta := *meta
	windowMeta.OpensAt = now.Add(time.Hour).Format(time.RFC3339)
	windowExam := *exam
	windowExam.ExamMeta = windowMeta
	if _, err := newService(&stubExamStore{getExam: &windowExam}).Get(ctx, "exam", student); err == nil {
		t.Fatal("Get ignored an unopened student window")
	}
	if _, err := newService(&stubExamStore{meta: &windowMeta}).Submit(ctx, "exam", student, map[string]int{}, "r"); err == nil {
		t.Fatal("Submit ignored an unopened student window")
	}
	if _, _, err := newService(&stubExamStore{meta: &windowMeta}).Resume(ctx, "exam", student); err == nil {
		t.Fatal("Resume ignored an unopened student window")
	}
	if _, err := newService(&stubExamStore{meta: &windowMeta, getExam: exam}).Save(ctx, "exam", student, map[string]int{}, 0, "r"); err == nil {
		t.Fatal("Save ignored an unopened student window")
	}

	getStore := &stubExamStore{getExamErr: errors.New("get exam")}
	if _, err := newService(getStore).Get(ctx, "exam", teacher); !errors.Is(err, getStore.getExamErr) {
		t.Fatalf("Get store error = %v", err)
	}
	getStore = &stubExamStore{}
	if _, err := newService(getStore).Get(ctx, "exam", teacher); !errors.Is(err, ErrExamNotFound) {
		t.Fatalf("Get missing = %v", err)
	}
	getStore.getExam = exam
	if got, err := newService(getStore).Get(ctx, "exam", teacher); err != nil || got.Attempt != nil {
		t.Fatalf("teacher Get = %#v, %v", got, err)
	}
	getStore = &stubExamStore{getExam: exam, recordStartErr: errors.New("start")}
	if _, err := newService(getStore).Get(ctx, "exam", student); !errors.Is(err, getStore.recordStartErr) {
		t.Fatalf("Get attempt error = %v", err)
	}
	getStore = &stubExamStore{getExam: exam, recordStart: attempt}
	got, err := newService(getStore).Get(ctx, "exam", student)
	if err != nil || got.AttemptStartedAt != 1000 || got.DeadlineMS != 61000 {
		t.Fatalf("student Get = %#v, %v", got, err)
	}
	noDuration := *exam
	noDuration.DurationMinutes = 0
	noDuration.DeadlineMS = 0
	getStore = &stubExamStore{getExam: &noDuration, recordStart: attempt}
	got, err = newService(getStore).Get(ctx, "exam", student)
	if err != nil || got.DeadlineMS != 0 {
		t.Fatalf("untimed student Get = %#v, %v", got, err)
	}

	if _, err := newService(&stubExamStore{}).Submit(ctx, "exam", nil, map[string]int{}, "r"); err == nil {
		t.Fatal("nil Submit user accepted")
	}
	submitStore := &stubExamStore{metaErr: errors.New("meta")}
	if _, err := newService(submitStore).Submit(ctx, "exam", teacher, map[string]int{}, "r"); !errors.Is(err, submitStore.metaErr) {
		t.Fatalf("Submit meta error = %v", err)
	}
	if _, err := newService(&stubExamStore{}).Submit(ctx, "exam", teacher, map[string]int{}, "r"); !errors.Is(err, ErrExamNotFound) {
		t.Fatalf("Submit missing meta = %v", err)
	}
	for _, tc := range []struct {
		name       string
		answers    map[string]int
		getExam    *model.Exam
		getExamErr error
		want       error
	}{
		{name: "nil answers", answers: nil, getExam: exam, want: errors.New("answers are required")},
		{name: "exam error", answers: map[string]int{}, getExamErr: errors.New("exam read"), want: errors.New("exam read")},
		{name: "exam missing", answers: map[string]int{}, want: ErrExamNotFound},
		{name: "bad answer", answers: map[string]int{"9": 0}, getExam: exam, want: errors.New("answer index")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubExamStore{meta: meta, getExam: tc.getExam, getExamErr: tc.getExamErr}
			_, err := newService(store).Submit(ctx, "exam", teacher, tc.answers, "r")
			if err == nil || !strings.Contains(err.Error(), tc.want.Error()) {
				t.Fatalf("Submit error = %v, want %v", err, tc.want)
			}
		})
	}

	deadline := int64(-1000)
	submitStore = &stubExamStore{meta: meta, getExam: exam, attemptDeadline: attempt, deadline: &deadline}
	if _, err := newService(submitStore).Submit(ctx, "exam", student, map[string]int{"0": 1}, "r"); err == nil {
		t.Fatal("expired Submit accepted")
	}
	submitStore = &stubExamStore{meta: meta, getExam: exam, attemptDeadlineErr: errors.New("deadline")}
	if _, err := newService(submitStore).Submit(ctx, "exam", student, map[string]int{"0": 1}, "r"); !errors.Is(err, submitStore.attemptDeadlineErr) {
		t.Fatalf("deadline error = %v", err)
	}
	submitStore = &stubExamStore{meta: meta, getExam: exam, submitResult: model.SubmissionResult{Status: "student-committed"}}
	if result, err := newService(submitStore).Submit(ctx, "exam", student, map[string]int{"0": 1}, "r"); err != nil || result.Status != "student-committed" {
		t.Fatalf("student Submit without deadline = %#v, %v", result, err)
	}
	submitStore = &stubExamStore{meta: meta, getExam: exam, submitResult: model.SubmissionResult{Status: "committed", Score: 1}}
	result, err := newService(submitStore).Submit(ctx, "exam", teacher, map[string]int{"0": 1}, "r")
	if err != nil || result.Status != "committed" {
		t.Fatalf("successful Submit = %#v, %v", result, err)
	}

	if _, err := newService(&stubExamStore{}).SubmitSaved(ctx, "exam", nil, "r"); err == nil {
		t.Fatal("nil SubmitSaved user accepted")
	}
	savedStore := &stubExamStore{studentAttemptErr: errors.New("attempt read")}
	if _, err := newService(savedStore).SubmitSaved(ctx, "exam", student, "r"); !errors.Is(err, savedStore.studentAttemptErr) {
		t.Fatalf("SubmitSaved read error = %v", err)
	}
	for _, savedAttempt := range []*model.Attempt{nil, {}} {
		store := &stubExamStore{studentAttempt: savedAttempt}
		if _, err := newService(store).SubmitSaved(ctx, "exam", student, "r"); !errors.Is(err, ErrAttemptNotFound) {
			t.Fatalf("SubmitSaved missing attempt = %v", err)
		}
	}
	savedStore = &stubExamStore{studentAttempt: attempt, meta: meta, getExam: exam, submitResult: model.SubmissionResult{Status: "saved-submit"}}
	if result, err := newService(savedStore).SubmitSaved(ctx, "exam", student, "r"); err != nil || result.Status != "saved-submit" {
		t.Fatalf("SubmitSaved success = %#v, %v", result, err)
	}

	if _, _, err := newService(&stubExamStore{}).Resume(ctx, "exam", nil); err == nil {
		t.Fatal("nil Resume user accepted")
	}
	resumeStore := &stubExamStore{metaErr: errors.New("resume meta")}
	if _, _, err := newService(resumeStore).Resume(ctx, "exam", student); !errors.Is(err, resumeStore.metaErr) {
		t.Fatalf("Resume meta error = %v", err)
	}
	resumeStore = &stubExamStore{}
	if _, _, err := newService(resumeStore).Resume(ctx, "exam", student); !errors.Is(err, ErrExamNotFound) {
		t.Fatalf("Resume missing = %v", err)
	}
	resumeStore = &stubExamStore{meta: meta, recordStartErr: errors.New("resume start")}
	if _, _, err := newService(resumeStore).Resume(ctx, "exam", student); !errors.Is(err, resumeStore.recordStartErr) {
		t.Fatalf("Resume start error = %v", err)
	}
	resumeStore = &stubExamStore{meta: meta, recordStart: attempt}
	if got, deadline, err := newService(resumeStore).Resume(ctx, "exam", student); err != nil || got != attempt || deadline == nil || *deadline != 61000 {
		t.Fatalf("Resume success = %#v %v %v", got, deadline, err)
	}
	resumeMeta := *meta
	resumeMeta.DurationMinutes = 0
	resumeStore = &stubExamStore{meta: &resumeMeta, recordStart: attempt}
	if _, deadline, err := newService(resumeStore).Resume(ctx, "exam", student); err != nil || deadline != nil {
		t.Fatalf("untimed Resume deadline = %v, err=%v", deadline, err)
	}

	if _, err := newService(&stubExamStore{}).Save(ctx, "exam", nil, map[string]int{}, 0, "r"); err == nil {
		t.Fatal("nil Save user accepted")
	}
	if _, err := newService(&stubExamStore{}).Save(ctx, "exam", teacher, map[string]int{}, -1, "r"); err == nil {
		t.Fatal("negative Save version accepted")
	}
	if _, err := newService(&stubExamStore{}).Save(ctx, "exam", teacher, map[string]int{}, 0, " "); err == nil {
		t.Fatal("blank Save request id accepted")
	}
	for _, tc := range []struct {
		name    string
		store   *stubExamStore
		answers map[string]int
		want    error
	}{
		{name: "meta error", store: &stubExamStore{metaErr: errors.New("save meta")}, answers: map[string]int{}, want: errors.New("save meta")},
		{name: "meta missing", store: &stubExamStore{}, answers: map[string]int{}, want: ErrExamNotFound},
		{name: "exam error", store: &stubExamStore{meta: meta, getExamErr: errors.New("save exam")}, answers: map[string]int{}, want: errors.New("save exam")},
		{name: "exam missing", store: &stubExamStore{meta: meta}, answers: map[string]int{}, want: ErrExamNotFound},
		{name: "nil answers", store: &stubExamStore{meta: meta, getExam: exam}, answers: nil, want: errors.New("answers are required")},
		{name: "bad answers", store: &stubExamStore{meta: meta, getExam: exam}, answers: map[string]int{"0": 3}, want: errors.New("out of range")},
	} {
		t.Run("Save "+tc.name, func(t *testing.T) {
			_, err := newService(tc.store).Save(ctx, "exam", teacher, tc.answers, 0, "r")
			if err == nil || !strings.Contains(err.Error(), tc.want.Error()) {
				t.Fatalf("Save error = %v, want %v", err, tc.want)
			}
		})
	}
	readStore := &stubExamStore{meta: meta, getExam: exam, studentAttemptErr: errors.New("before")}
	if _, err := newService(readStore).Save(ctx, "exam", teacher, map[string]int{"0": 1}, 0, "r"); !errors.Is(err, readStore.studentAttemptErr) {
		t.Fatalf("Save attempt read error = %v", err)
	}
	duplicate := &model.Attempt{StartedAt: 1000, LastRequestID: "same", Answers: map[string]int{"0": 1}}
	duplicateStore := &stubExamStore{meta: meta, getExam: exam, studentAttempt: duplicate}
	if result, err := newService(duplicateStore).Save(ctx, "exam", teacher, map[string]int{"0": 1}, 0, "same"); err != nil || result.Status != "duplicate" || result.DeadlineMS != 61000 {
		t.Fatalf("duplicate Save = %#v, %v", result, err)
	}
	expiredBefore := &model.Attempt{StartedAt: -100000, Answers: map[string]int{}}
	expiredStore := &stubExamStore{meta: meta, getExam: exam, studentAttempt: expiredBefore}
	if _, err := newService(expiredStore).Save(ctx, "exam", student, map[string]int{"0": 1}, 0, "new"); err == nil {
		t.Fatal("expired Save accepted")
	}
	saveErrStore := &stubExamStore{meta: meta, getExam: exam, saveAttemptErr: errors.New("save write")}
	if _, err := newService(saveErrStore).Save(ctx, "exam", teacher, map[string]int{"0": 1}, 0, "new"); !errors.Is(err, saveErrStore.saveAttemptErr) {
		t.Fatalf("Save write error = %v", err)
	}
	successStore := &stubExamStore{meta: meta, getExam: exam, saveAttempt: attempt}
	if result, err := newService(successStore).Save(ctx, "exam", teacher, map[string]int{"0": 1}, 0, "new"); err != nil || result.Status != "saved" || result.DeadlineMS != 61000 {
		t.Fatalf("successful Save = %#v, %v", result, err)
	}
	if result := newService(&stubExamStore{}).attemptResult("x", "r", nil, nil); result.DeadlineMS != 0 || result.Status != "x" {
		t.Fatalf("nil attempt result = %#v", result)
	}
}

type stubExamStore struct {
	getExam            *model.Exam
	getExamErr         error
	meta               *model.ExamMeta
	metaErr            error
	recordStart        *model.Attempt
	recordStartErr     error
	attemptDeadline    *model.Attempt
	deadline           *int64
	attemptDeadlineErr error
	studentAttempt     *model.Attempt
	studentAttemptErr  error
	saveAttempt        *model.Attempt
	saveAttemptErr     error
	submitResult       model.SubmissionResult
	submitErr          error
}

func (s *stubExamStore) GetExam(context.Context, string, bool) (*model.Exam, error) {
	return s.getExam, s.getExamErr
}
func (s *stubExamStore) GetExamMeta(context.Context, string) (*model.ExamMeta, error) {
	return s.meta, s.metaErr
}
func (s *stubExamStore) RecordAttemptStart(context.Context, string, string) (*model.Attempt, error) {
	return s.recordStart, s.recordStartErr
}
func (s *stubExamStore) AttemptDeadline(context.Context, string, string) (*model.Attempt, *int64, error) {
	return s.attemptDeadline, s.deadline, s.attemptDeadlineErr
}
func (s *stubExamStore) GetStudentAttempt(context.Context, string, string) (*model.Attempt, error) {
	return s.studentAttempt, s.studentAttemptErr
}
func (s *stubExamStore) SaveAttempt(context.Context, string, string, map[string]int, int, string) (*model.Attempt, error) {
	return s.saveAttempt, s.saveAttemptErr
}
func (s *stubExamStore) SubmitAttempt(context.Context, string, string, string, map[string]int, string) (model.SubmissionResult, error) {
	return s.submitResult, s.submitErr
}

func TestTimedGetIsStableAndSubmitIsIdempotent(t *testing.T) {
	m := store.NewMemory("test")
	ctx := context.Background()
	if err := m.EnsureExam(ctx, "e", "E", []model.Question{{Index: 0, Question: "q", Options: []string{"a", "b"}, Answer: 1}}, map[string]any{"duration_minutes": 1}); err != nil {
		t.Fatal(err)
	}
	s := New(m)
	fixed := time.UnixMilli(1000)
	s.Now = func() time.Time { return fixed }
	u := &model.User{UserID: "u", Name: "U", Role: "student"}
	one, err := s.Get(ctx, "e", u)
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.Get(ctx, "e", u)
	if err != nil {
		t.Fatal(err)
	}
	if one.AttemptStartedAt != two.AttemptStartedAt || one.DeadlineMS != two.DeadlineMS {
		t.Fatalf("clock changed: %#v %#v", one, two)
	}
	r, err := s.Submit(ctx, "e", u, map[string]int{"0": 1}, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "committed" || r.Score != 1 {
		t.Fatalf("bad result %#v", r)
	}
	r, err = s.Submit(ctx, "e", u, map[string]int{"0": 0}, "r1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "duplicate" || r.Score != 1 {
		t.Fatalf("bad duplicate %#v", r)
	}
	_, err = s.Submit(ctx, "e", u, map[string]int{"0": 1}, "r2")
	if !errors.Is(err, store.ErrSubmissionConflict) {
		t.Fatalf("got %v", err)
	}
}
