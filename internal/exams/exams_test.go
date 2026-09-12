package exams

import (
	"context"
	"errors"
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
