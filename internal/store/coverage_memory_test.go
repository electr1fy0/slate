package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ayush/slate/internal/clusterstate"
	"github.com/ayush/slate/internal/model"
)

type memoryFenceStub struct {
	active clusterstate.Writer
	paused bool
}

func (f *memoryFenceStub) WithFence(_ context.Context, writer clusterstate.Writer, fn func() error) error {
	if f.paused || writer != f.active {
		return clusterstate.ErrFenced
	}
	return fn()
}

func TestMemoryExamMutationsUseFenceGuard(t *testing.T) {
	ctx := context.Background()
	m := fixedMemory()
	if err := m.EnsureExam(ctx, "fenced", "Fenced", []model.Question{{Index: 0, Question: "q", Options: []string{"a", "b"}, Answer: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	guard := &memoryFenceStub{active: clusterstate.Writer{Backend: "A", Epoch: 7}}
	m.SetFenceGuard(guard)
	if _, err := m.RecordAttemptStart(ctx, "fenced", "missing-writer"); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("missing writer was accepted: %v", err)
	}
	oldWriter := clusterstate.WithWriter(ctx, clusterstate.Writer{Backend: "A", Epoch: 7})
	if attempt, err := m.RecordAttemptStart(oldWriter, "fenced", "student"); err != nil || attempt == nil {
		t.Fatalf("active attempt start = %#v, %v", attempt, err)
	}
	activeSave, err := m.SaveAttempt(oldWriter, "fenced", "student", map[string]int{"0": 0}, 0, "active-save")
	if err != nil || activeSave == nil || activeSave.Version != 1 || activeSave.Answers["0"] != 0 {
		t.Fatalf("active A save = %#v, %v", activeSave, err)
	}
	beforePromotion, err := m.GetStudentAttempt(ctx, "fenced", "student")
	if err != nil || beforePromotion == nil || beforePromotion.Version != 1 || beforePromotion.Answers["0"] != 0 {
		t.Fatalf("attempt before promotion = %#v, %v", beforePromotion, err)
	}
	staleAnswers := map[string]int{"0": 1}
	guard.active = clusterstate.Writer{Backend: "B", Epoch: 8}
	if _, err := m.SaveAttempt(oldWriter, "fenced", "student", staleAnswers, beforePromotion.Version, "stale-save"); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("stale attempt save was accepted: %v", err)
	}
	if _, err := m.SubmitAttempt(oldWriter, "fenced", "student", "Ada", staleAnswers, "stale-submit"); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("stale final submission was accepted: %v", err)
	}
	afterStaleWrites, err := m.GetStudentAttempt(ctx, "fenced", "student")
	if err != nil || afterStaleWrites == nil || afterStaleWrites.Version != beforePromotion.Version || afterStaleWrites.LastRequestID != beforePromotion.LastRequestID || afterStaleWrites.Submitted || afterStaleWrites.Answers["0"] != beforePromotion.Answers["0"] {
		t.Fatalf("stale writer changed canonical attempt: before=%#v after=%#v err=%v", beforePromotion, afterStaleWrites, err)
	}
	if submission, _ := m.GetSubmission(ctx, "fenced", "student"); submission != nil {
		t.Fatalf("stale writer created submission: %#v", submission)
	}
	if _, found, err := m.FindSubmissionRequest(ctx, "fenced", "student", staleAnswers, "stale-submit"); err != nil || found {
		t.Fatalf("stale writer created receipt: found=%v err=%v", found, err)
	}
	if outbox, err := m.ListOutbox(ctx, 10); err != nil || len(outbox) != 0 {
		t.Fatalf("stale writer created outbox entries: %#v err=%v", outbox, err)
	}
	newWriter := clusterstate.WithWriter(ctx, clusterstate.Writer{Backend: "B", Epoch: 8})
	activeBSave, err := m.SaveAttempt(newWriter, "fenced", "student", staleAnswers, afterStaleWrites.Version, "active-b-save")
	if err != nil || activeBSave == nil || activeBSave.Version != afterStaleWrites.Version+1 || activeBSave.Answers["0"] != staleAnswers["0"] {
		t.Fatalf("active B save = %#v, %v", activeBSave, err)
	}
	result, err := m.SubmitAttempt(newWriter, "fenced", "student", "Ada", staleAnswers, "active-submit")
	if err != nil || result.Status != "committed" {
		t.Fatalf("active final submission = %#v, %v", result, err)
	}
	if submission, err := m.GetSubmission(ctx, "fenced", "student"); err != nil || submission == nil || submission.RequestID != "active-submit" || submission.Answers["0"] != staleAnswers["0"] {
		t.Fatalf("active B submission = %#v, %v", submission, err)
	}
	if attempt, err := m.GetStudentAttempt(ctx, "fenced", "student"); err != nil || attempt == nil || !attempt.Submitted || attempt.Version != activeBSave.Version+1 || attempt.Answers["0"] != staleAnswers["0"] {
		t.Fatalf("attempt after active B submit = %#v, %v", attempt, err)
	}
	if _, found, err := m.FindSubmissionRequest(ctx, "fenced", "student", staleAnswers, "active-submit"); err != nil || !found {
		t.Fatalf("active B submission receipt found=%v err=%v", found, err)
	}
	if outbox, err := m.ListOutbox(ctx, 10); err != nil || len(outbox) != 1 || outbox[0].RequestID != "active-submit" {
		t.Fatalf("outbox after active B submit = %#v err=%v", outbox, err)
	}
}

func TestMemoryFencesRegistrationAndTeacherMetadataUpdates(t *testing.T) {
	ctx := context.Background()
	m := fixedMemory()
	if err := m.EnsureExamIfMissing(ctx, "fenced-teacher", "Original title", []model.Question{{Question: "q", Options: []string{"a", "b"}, Answer: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	guard := &memoryFenceStub{active: clusterstate.Writer{Backend: "A", Epoch: 11}}
	m.SetFenceGuard(guard)
	oldWriter := clusterstate.WithWriter(ctx, clusterstate.Writer{Backend: "A", Epoch: 11})
	if err := m.CreateUser(oldWriter, &model.User{UserID: "active-user", Email: "active@example.com"}, "hash"); err != nil {
		t.Fatalf("active registration = %v", err)
	}

	guard.active = clusterstate.Writer{Backend: "B", Epoch: 12}
	if err := m.CreateUser(oldWriter, &model.User{UserID: "stale-user", Email: "stale@example.com"}, "hash"); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("stale registration = %v, want ErrFenced", err)
	}
	if user, err := m.GetUser(ctx, "stale-user"); err != nil || user != nil {
		t.Fatalf("stale registration persisted user %#v, %v", user, err)
	}

	staleTitle := "Must not commit"
	if _, err := m.UpdateExamWindow(oldWriter, "fenced-teacher", &staleTitle, nil); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("stale teacher update = %v, want ErrFenced", err)
	}
	meta, err := m.GetExamMeta(ctx, "fenced-teacher")
	if err != nil || meta == nil || meta.Title != "Original title" {
		t.Fatalf("stale teacher update changed exam metadata: %#v, %v", meta, err)
	}
	newWriter := clusterstate.WithWriter(ctx, clusterstate.Writer{Backend: "B", Epoch: 12})
	newTitle := "Current writer edit"
	if meta, err := m.UpdateExamWindow(newWriter, "fenced-teacher", &newTitle, nil); err != nil || meta == nil || meta.Title != newTitle {
		t.Fatalf("active teacher update = %#v, %v", meta, err)
	}
}

func fixedMemory() *MemoryStore {
	m := NewMemory("memory-region")
	m.now = func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) }
	return m
}

func assertStoreError(t *testing.T, got, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func TestStoreHelpersAndMemoryUsers(t *testing.T) {
	if got := normaliseClassroomCode(" ab-12 "); got != "AB-12" {
		t.Fatalf("normalised code = %q", got)
	}
	if got := boardPartitionKey("r"); got != "BOARD#r" || boardVersionKey(7) != "VERSION#00000000000000000007" {
		t.Fatalf("board keys = %q %q", got, boardVersionKey(7))
	}
	if got := effectiveAttempts(0); got != 1 || effectiveAttempts(-1) != 1 || effectiveAttempts(3) != 3 {
		t.Fatalf("effective attempts")
	}
	if got := effectivePoints(0); got != 1 || effectivePoints(-1) != 1 || effectivePoints(4) != 4 {
		t.Fatalf("effective points")
	}
	if !ownerCanEdit("admin", "someone") || !ownerCanEdit("teacher", "") || !ownerCanEdit("teacher", "teacher") || ownerCanEdit("teacher", "other") {
		t.Fatalf("owner edit policy")
	}
	if toBool(true) != true || toBool(" true ") != true || toBool("1") != true || toBool("false") || toBool(1) {
		t.Fatalf("toBool variants")
	}
	if toInt(2) != 2 || toInt(int64(3)) != 3 || toInt(float64(4)) != 4 || toInt("5") != 0 {
		t.Fatalf("toInt variants")
	}
	if cloneClassroom(nil) != nil || cloneClassroomMember(nil) != nil || cloneDraft(nil) != nil || cloneAnswers(nil) != nil || cloneQuestions(nil) != nil {
		t.Fatalf("nil clone behavior")
	}
	questions := []model.Question{{Question: "q", Options: []string{"a"}}}
	cloned := cloneQuestions(questions)
	cloned[0].Options[0] = "changed"
	if questions[0].Options[0] != "a" || cloned[0].Index != 0 {
		t.Fatalf("question clone = %#v", cloned)
	}

	ctx := context.Background()
	m := fixedMemory()
	if user, err := m.GetUser(ctx, "missing"); err != nil || user != nil {
		t.Fatalf("missing user = %#v, %v", user, err)
	}
	if id, err := m.FindUserIDByEmail(ctx, "missing@example.com"); err != nil || id != "" {
		t.Fatalf("missing email = %q, %v", id, err)
	}
	user := &model.User{UserID: "u1", Email: "u1@example.com", Name: "Ada"}
	if err := m.CreateUser(ctx, user, "hash"); err != nil {
		t.Fatal(err)
	}
	if user.PasswordHash != "" {
		t.Fatal("CreateUser should not mutate the caller")
	}
	got, err := m.GetUser(ctx, "u1")
	if err != nil || got == nil || got.PasswordHash != "hash" {
		t.Fatalf("stored user = %#v, %v", got, err)
	}
	got.Name = "mutated"
	copy, _ := m.GetUser(ctx, "u1")
	if copy.Name != "Ada" {
		t.Fatal("GetUser did not return a copy")
	}
	if id, err := m.FindUserIDByEmail(ctx, " U1@EXAMPLE.COM "); err != nil || id != "u1" {
		t.Fatalf("email lookup = %q, %v", id, err)
	}
	assertStoreError(t, m.CreateUser(ctx, user, "other"), ErrEmailRegistered)
}

func TestMemoryClassroomEdgeCases(t *testing.T) {
	ctx := context.Background()
	m := fixedMemory()
	if err := m.CreateClassroom(ctx, nil); err == nil {
		t.Fatal("nil classroom should fail")
	}
	if err := m.CreateClassroom(ctx, &model.Classroom{ID: "bad", OwnerID: "o"}); err == nil {
		t.Fatal("missing join code should fail")
	}
	if err := m.AddClassroomMember(ctx, nil); err == nil {
		t.Fatal("nil member should fail")
	}
	if err := m.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "missing", UserID: "u"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing classroom member error = %v", err)
	}

	classroom := &model.Classroom{ID: "c1", OwnerID: "owner", Name: "  Math  ", JoinCode: " code1 ", MemberCount: -4}
	if err := m.CreateClassroom(ctx, classroom); err != nil {
		t.Fatal(err)
	}
	if classroom.Name != "Math" || classroom.JoinCode != "CODE1" || classroom.MemberCount != 0 {
		t.Fatalf("normalised classroom = %#v", classroom)
	}
	if err := m.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "c1"}); err == nil {
		t.Fatal("empty classroom member user id should fail")
	}
	duplicateID := &model.Classroom{ID: "c1", OwnerID: "owner", JoinCode: "CODE2"}
	assertStoreError(t, m.CreateClassroom(ctx, duplicateID), ErrClassroomCodeTaken)
	duplicateCode := &model.Classroom{ID: "c2", OwnerID: "owner", JoinCode: "code1"}
	assertStoreError(t, m.CreateClassroom(ctx, duplicateCode), ErrClassroomCodeTaken)
	if err := m.CreateClassroom(ctx, &model.Classroom{ID: "c2", OwnerID: "owner", JoinCode: "CODE2"}); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateClassroom(ctx, &model.Classroom{ID: "c3", OwnerID: "owner", JoinCode: "CODE3"}); err != nil {
		t.Fatal(err)
	}
	m.classrooms["c2"].CreatedAt = "2027-01-01T00:00:00Z"

	if got, _ := m.GetClassroom(ctx, " c1 "); got == nil || got.ID != "c1" {
		t.Fatalf("get classroom = %#v", got)
	}
	if got, _ := m.FindClassroomByJoinCode(ctx, " code1 "); got == nil || got.ID != "c1" {
		t.Fatalf("find classroom = %#v", got)
	}
	if rows, err := m.ListClassroomsByOwner(ctx, " owner ", 1); err != nil || len(rows) != 1 {
		t.Fatalf("owner classrooms = %#v, %v", rows, err)
	}
	if rows, err := m.ListClassroomsForMember(ctx, "u", 0); err != nil || len(rows) != 0 {
		t.Fatalf("member classrooms before join = %#v, %v", rows, err)
	}
	if rows, err := m.ListClassroomMembers(ctx, "missing"); !errors.Is(err, ErrNotFound) || rows != nil {
		t.Fatalf("missing roster = %#v, %v", rows, err)
	}
	if ok, err := m.IsClassroomMember(ctx, "missing", "u"); err != nil || ok {
		t.Fatalf("missing membership = %v, %v", ok, err)
	}
	if err := m.RemoveClassroomMember(ctx, "c1", "owner", "missing"); !errors.Is(err, ErrClassroomMemberNotFound) {
		t.Fatalf("missing removal = %v", err)
	}
	if err := m.RemoveClassroomMember(ctx, "missing", "owner", "u"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing classroom removal = %v", err)
	}

	member := &model.ClassroomMember{ClassroomID: " c1 ", UserID: " u1 ", Name: "Student"}
	if err := m.AddClassroomMember(ctx, member); err != nil {
		t.Fatal(err)
	}
	if member.Role != "student" || member.PK != "CLASSROOM#c1" || member.SK != "MEMBER#u1" {
		t.Fatalf("member defaults = %#v", member)
	}
	if rows, err := m.ListClassroomsForMember(ctx, "u1", 1); err != nil || len(rows) != 1 || rows[0].MemberCount != 1 {
		t.Fatalf("member classrooms = %#v, %v", rows, err)
	}
	// Directly seed two additional records to exercise deterministic roster
	// ordering for both equal and different join timestamps.
	m.classroomMembers["c1"]["u0"] = &model.ClassroomMember{ClassroomID: "c1", UserID: "u0", JoinedAt: member.JoinedAt}
	m.classroomMembers["c1"]["u2"] = &model.ClassroomMember{ClassroomID: "c1", UserID: "u2", JoinedAt: "2020-01-01T00:00:00Z"}
	if rows, err := m.ListClassroomMembers(ctx, "c1"); err != nil || len(rows) != 3 {
		t.Fatalf("roster = %#v, %v", rows, err)
	}
	if ok, err := m.IsClassroomMember(ctx, "c1", " u1 "); err != nil || !ok {
		t.Fatalf("membership = %v, %v", ok, err)
	}
	assertStoreError(t, m.AddClassroomMember(ctx, member), ErrClassroomMember)

	if _, err := m.UpdateClassroom(ctx, "missing", "owner", model.ClassroomPatch{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing update = %v", err)
	}
	if _, err := m.UpdateClassroom(ctx, "c1", "other", model.ClassroomPatch{}); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("wrong owner update = %v", err)
	}
	name, description := "  Algebra  ", "Vectors"
	updated, err := m.UpdateClassroom(ctx, "c1", "owner", model.ClassroomPatch{Name: &name, Description: &description})
	if err != nil || updated.Name != "Algebra" || updated.Description != description {
		t.Fatalf("updated classroom = %#v, %v", updated, err)
	}
	if err := m.RemoveClassroomMember(ctx, "c1", "owner", " u1 "); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteClassroom(ctx, "missing", "owner"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing delete = %v", err)
	}
	if err := m.DeleteClassroom(ctx, "c1", "other"); !errors.Is(err, ErrClassroomForbidden) {
		t.Fatalf("wrong owner delete = %v", err)
	}
	if err := m.DeleteClassroom(ctx, "c1", "owner"); err != nil {
		t.Fatal(err)
	}

	zero := &MemoryStore{now: m.now}
	if err := zero.CreateClassroom(ctx, &model.Classroom{OwnerID: "o", JoinCode: "zcode"}); err != nil {
		t.Fatalf("zero-value classroom store = %v", err)
	}
	if got, _ := zero.GetClassroom(ctx, ""); got != nil {
		t.Fatal("generated classroom should not be stored under an empty id")
	}
	if got, _ := zero.ListClassroomsByOwner(ctx, "o", 0); len(got) != 1 || got[0].ID == "" {
		t.Fatalf("generated classroom = %#v", got)
	}
	resetMembers := &MemoryStore{now: m.now, classrooms: map[string]*model.Classroom{"raw": {ID: "raw", OwnerID: "o"}}, classroomMembers: map[string]map[string]*model.ClassroomMember{}}
	if err := resetMembers.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "raw", UserID: "student"}); err != nil {
		t.Fatalf("nil member index should initialize = %v", err)
	}
}

func TestMemoryExamsQuizzesAndAttempts(t *testing.T) {
	ctx := context.Background()
	m := fixedMemory()
	questions := []model.Question{
		{Question: "one", Options: []string{"a", "b"}, Answer: 1, Points: 2},
		{Question: "two", Options: []string{"x", "y"}, Answer: 0},
	}
	window := map[string]any{
		"opens_at": "open", "closes_at": "close", "duration_minutes": int64(30), "owner_id": "teacher",
		"status": "published", "max_attempts": float64(2), "allow_late": "1", "late_grace_minutes": 5,
		"ignored": "ignored",
	}
	if err := m.EnsureExam(ctx, "exam-1", "Exam", questions, window); err != nil {
		t.Fatal(err)
	}
	zeroExam := &MemoryStore{now: m.now}
	if err := zeroExam.EnsureExam(ctx, "zero-exam", "Zero", nil, nil); err != nil || zeroExam.exams["zero-exam"] == nil {
		t.Fatalf("nil exam map initialization = %v", err)
	}
	meta, _ := m.GetExamMeta(ctx, "exam-1")
	if meta == nil || meta.ID != "exam-1" || meta.DurationMinutes != 30 || !meta.AllowLate || meta.QuestionCount != 2 {
		t.Fatalf("exam meta = %#v", meta)
	}
	if missing, _ := m.GetExamMeta(ctx, "missing"); missing != nil {
		t.Fatal("missing exam meta should be nil")
	}
	public, _ := m.GetExam(ctx, "exam-1", false)
	private, _ := m.GetExam(ctx, "exam-1", true)
	if public == nil || public.Questions[0].Answer != 0 || public.Questions[0].Feedback != "" || private.Questions[0].Answer != 1 {
		t.Fatalf("exam visibility public=%#v private=%#v", public, private)
	}
	if missing, _ := m.GetExam(ctx, "missing", false); missing != nil {
		t.Fatal("missing exam should be nil")
	}
	title := "Renamed"
	updated, err := m.UpdateExamWindow(ctx, "exam-1", &title, window)
	if err != nil || updated.Title != title || updated.MaxAttempts != 2 || updated.LateGraceMinutes != 5 {
		t.Fatalf("updated exam = %#v, %v", updated, err)
	}
	if noExam, err := m.UpdateExamWindow(ctx, "missing", nil, nil); err != nil || noExam != nil {
		t.Fatalf("missing exam update = %#v, %v", noExam, err)
	}
	if rows, err := m.ListExams(ctx, 1); err != nil || len(rows) != 1 {
		t.Fatalf("limited exams = %#v, %v", rows, err)
	}
	if rows, err := m.ListExams(ctx, 0); err != nil || len(rows) != 1 {
		t.Fatalf("all exams = %#v, %v", rows, err)
	}
	if _, err := m.CreateQuizDraft(ctx, &model.QuizDraft{ID: "exam-1", OwnerID: "teacher", Title: "Collision"}); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("draft/exam collision = %v", err)
	}
	if score, total, err := m.GradeAttempt(ctx, "exam-1", map[string]int{"0": 1, "1": 1}); err != nil || score != 2 || total != 3 {
		t.Fatalf("grade = %d/%d, %v", score, total, err)
	}
	if score, total, err := m.GradeAttempt(ctx, "missing", nil); err != nil || score != 0 || total != 0 {
		t.Fatalf("missing grade = %d/%d, %v", score, total, err)
	}

	if _, err := m.CreateQuizDraft(ctx, nil); err == nil {
		t.Fatal("nil draft should fail")
	}
	if _, err := m.CreateQuizDraft(ctx, &model.QuizDraft{ID: "", OwnerID: "teacher", Title: "T"}); err == nil {
		t.Fatal("draft without id should fail")
	}
	if _, err := m.CreateQuizDraft(ctx, &model.QuizDraft{ID: "d0", OwnerID: "", Title: "T"}); err == nil {
		t.Fatal("draft without owner should fail")
	}
	if _, err := m.CreateQuizDraft(ctx, &model.QuizDraft{ID: "d0", OwnerID: "teacher", Title: " "}); err == nil {
		t.Fatal("draft without title should fail")
	}
	draftStore := &MemoryStore{now: m.now, exams: map[string]*model.Exam{}}
	if _, err := draftStore.CreateQuizDraft(ctx, &model.QuizDraft{ID: "initialized", OwnerID: "teacher", Title: "Initialized"}); err != nil {
		t.Fatalf("nil draft map initialization = %v", err)
	}
	draft, err := m.CreateQuizDraft(ctx, &model.QuizDraft{ID: "d1", OwnerID: "teacher", Title: "  Draft  ", Questions: questions, DurationMinutes: 15})
	if err != nil || draft.Title != "Draft" || draft.QuestionCount != 2 || draft.Status != "draft" {
		t.Fatalf("created draft = %#v, %v", draft, err)
	}
	if _, err := m.CreateQuizDraft(ctx, &model.QuizDraft{ID: "d2", OwnerID: "teacher", Title: "Second"}); err != nil {
		t.Fatalf("second draft = %v", err)
	}
	m.drafts["d2"].UpdatedAt = "2020-01-01T00:00:00Z"
	if _, err := m.CreateQuizDraft(ctx, &model.QuizDraft{ID: "d3", OwnerID: "teacher", Title: "Third"}); err != nil {
		t.Fatalf("third draft = %v", err)
	}
	if _, err := m.CreateQuizDraft(ctx, draft); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("duplicate draft = %v", err)
	}
	if got, _ := m.GetQuizDraft(ctx, " d1 "); got == nil || got.Questions[0].Index != 0 {
		t.Fatalf("get draft = %#v", got)
	}
	if got, _ := m.GetQuizDraft(ctx, "missing"); got != nil {
		t.Fatal("missing draft should be nil")
	}
	if rows, err := m.ListQuizDrafts(ctx, "other", 0); err != nil || len(rows) != 0 {
		t.Fatalf("filtered drafts = %#v, %v", rows, err)
	}
	if rows, err := m.ListQuizDrafts(ctx, "admin", 1); err != nil || len(rows) != 1 {
		t.Fatalf("admin drafts = %#v, %v", rows, err)
	}
	if deleted, err := m.DeleteQuizDraft(ctx, "d2", "other"); !errors.Is(err, ErrQuizForbidden) || deleted {
		t.Fatalf("forbidden draft delete = %v, %v", deleted, err)
	}
	if _, err := m.UpdateQuizDraft(ctx, "teacher", nil); err == nil {
		t.Fatal("nil update draft should fail")
	}
	if _, err := m.UpdateQuizDraft(ctx, "other", &model.QuizDraft{ID: "missing", Title: "x"}); !errors.Is(err, ErrQuizNotFound) {
		t.Fatalf("missing update draft = %v", err)
	}
	if _, err := m.UpdateQuizDraft(ctx, "other", &model.QuizDraft{ID: "d1", Title: "x"}); !errors.Is(err, ErrQuizForbidden) {
		t.Fatalf("forbidden update draft = %v", err)
	}
	if _, err := m.UpdateQuizDraft(ctx, "teacher", &model.QuizDraft{ID: "d1", Title: " "}); err == nil {
		t.Fatal("empty updated title should fail")
	}
	updatedDraft, err := m.UpdateQuizDraft(ctx, "teacher", &model.QuizDraft{ID: "d1", Title: " Updated", Questions: questions[:1]})
	if err != nil || updatedDraft.Title != "Updated" || updatedDraft.QuestionCount != 1 {
		t.Fatalf("updated draft = %#v, %v", updatedDraft, err)
	}

	if _, err := m.PublishQuizDraft(ctx, "missing", "teacher"); !errors.Is(err, ErrQuizNotFound) {
		t.Fatalf("missing publish = %v", err)
	}
	if _, err := m.PublishQuizDraft(ctx, "d1", "other"); !errors.Is(err, ErrQuizForbidden) {
		t.Fatalf("forbidden publish = %v", err)
	}
	empty, _ := m.CreateQuizDraft(ctx, &model.QuizDraft{ID: "empty", OwnerID: "teacher", Title: "Empty"})
	if _, err := m.PublishQuizDraft(ctx, empty.ID, "teacher"); err == nil {
		t.Fatal("empty publish should fail")
	}
	metaPublished, err := m.PublishQuizDraft(ctx, "d1", "teacher")
	if err != nil || metaPublished.Status != "published" {
		t.Fatalf("publish = %#v, %v", metaPublished, err)
	}
	if _, err := m.PublishQuizDraft(ctx, "d1", "teacher"); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("republish = %v", err)
	}
	if rows, err := m.ListExams(ctx, 1); err != nil || len(rows) != 1 {
		t.Fatalf("sorted/limited exams = %#v, %v", rows, err)
	}
	if _, err := m.UpdateQuizDraft(ctx, "teacher", &model.QuizDraft{ID: "d1", Title: "again"}); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("published update = %v", err)
	}
	if _, err := m.DeleteQuizDraft(ctx, "d1", "teacher"); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("published delete = %v", err)
	}
	existing, err := m.CreateQuizDraft(ctx, &model.QuizDraft{ID: "existing", OwnerID: "teacher", Title: "Existing", Questions: questions[:1]})
	if err != nil {
		t.Fatal(err)
	}
	m.exams[existing.ID] = &model.Exam{ExamMeta: model.ExamMeta{ID: existing.ID}}
	if _, err := m.PublishQuizDraft(ctx, existing.ID, "teacher"); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("existing exam publish = %v", err)
	}

	copyDraft, err := m.DuplicateQuiz(ctx, "d1", "teacher", "copy-1", "")
	if err != nil || copyDraft.Title != "Copy of Updated" || copyDraft.Questions[0].Index != 0 {
		t.Fatalf("draft duplicate = %#v, %v", copyDraft, err)
	}
	if _, err := m.DuplicateQuiz(ctx, "d1", "other", "copy-2", "x"); !errors.Is(err, ErrQuizForbidden) {
		t.Fatalf("forbidden duplicate = %v", err)
	}
	if _, err := m.DuplicateQuiz(ctx, "missing", "teacher", "copy-3", "x"); !errors.Is(err, ErrQuizNotFound) {
		t.Fatalf("missing duplicate = %v", err)
	}
	if _, err := m.DuplicateQuiz(ctx, "exam-1", "teacher", "copy-4", "Exam copy"); err != nil {
		t.Fatalf("exam duplicate = %v", err)
	}
	if generated, err := m.DuplicateQuiz(ctx, "exam-1", "teacher", "", ""); err != nil || generated == nil || !strings.HasPrefix(generated.ID, "quiz-") {
		t.Fatalf("generated exam duplicate = %#v, %v", generated, err)
	}
	if _, err := m.DuplicateQuiz(ctx, "exam-1", "other", "copy-5", "x"); !errors.Is(err, ErrQuizForbidden) {
		t.Fatalf("forbidden exam duplicate = %v", err)
	}
	if _, err := m.DuplicateQuiz(ctx, "exam-1", "", "copy-6", "x"); err == nil {
		t.Fatal("duplicate without owner should fail")
	}

	if attempt, err := m.RecordAttemptStart(ctx, "exam-1", "student"); err != nil || attempt == nil {
		t.Fatalf("start attempt = %#v, %v", attempt, err)
	}
	if same, _ := m.RecordAttemptStart(ctx, "exam-1", "student"); same == nil || same.StartedAt == 0 {
		t.Fatal("existing attempt missing")
	}
	if noDeadline, deadline, err := m.AttemptDeadline(ctx, "missing", "student"); err != nil || noDeadline != nil || deadline != nil {
		t.Fatalf("missing deadline = %#v %v %v", noDeadline, deadline, err)
	}
	if noDeadline, deadline, err := m.AttemptDeadline(ctx, "exam-1", "nobody"); err != nil || noDeadline != nil || deadline != nil {
		t.Fatalf("missing attempt deadline = %#v %v %v", noDeadline, deadline, err)
	}
	if attempt, deadline, err := m.AttemptDeadline(ctx, "exam-1", "student"); err != nil || attempt == nil || deadline == nil || *deadline <= attempt.StartedAt {
		t.Fatalf("deadline = %#v %v %v", attempt, deadline, err)
	}
	if attempt, _ := m.GetStudentAttempt(ctx, "missing", "student"); attempt != nil {
		t.Fatal("missing student attempt should be nil")
	}
	if _, err := m.SaveAttempt(ctx, "exam-1", "student", map[string]int{"0": 1}, 0, ""); err == nil {
		t.Fatal("missing request id should fail")
	}
	saved, err := m.SaveAttempt(ctx, "exam-1", "student", map[string]int{"0": 1}, 0, "save-1")
	if err != nil || saved.Version != 1 || saved.AttemptNumber != 1 {
		t.Fatalf("saved attempt = %#v, %v", saved, err)
	}
	if duplicate, err := m.SaveAttempt(ctx, "exam-1", "student", nil, 99, "save-1"); err != nil || duplicate.Version != 1 {
		t.Fatalf("duplicate save = %#v, %v", duplicate, err)
	}
	if _, err := m.SaveAttempt(ctx, "exam-1", "student", nil, 0, "save-2"); !errors.Is(err, ErrAttemptConflict) {
		t.Fatalf("version conflict = %v", err)
	}
	if got, _ := m.GetStudentAttempt(ctx, "exam-1", "student"); got == nil || got.Answers["0"] != 1 {
		t.Fatalf("student attempt = %#v", got)
	}
	zeroAttempt := &MemoryStore{now: m.now, submissions: map[string]*model.Submission{}}
	if created, err := zeroAttempt.SaveAttempt(ctx, "fresh", "student", nil, 0, "fresh-save"); err != nil || created == nil || created.Version != 1 {
		t.Fatalf("nil attempt map initialization = %#v, %v", created, err)
	}
	submittedAttempt := fixedMemory()
	submittedAttempt.attempts["submitted/student"] = &model.Attempt{Submitted: true}
	if _, err := submittedAttempt.SaveAttempt(ctx, "submitted", "student", nil, 0, "submitted-save"); !errors.Is(err, ErrAttemptSubmitted) {
		t.Fatalf("submitted attempt save = %v", err)
	}

	first, err := m.SubmitAttempt(ctx, "exam-1", "student", "Ada", map[string]int{"0": 1, "1": 0}, "submit-1")
	if err != nil || first.Status != "committed" || first.Score != 3 || first.AttemptNumber != 1 {
		t.Fatalf("submit = %#v, %v", first, err)
	}
	if again, err := m.SubmitAttempt(ctx, "exam-1", "student", "Ada", map[string]int{"0": 1, "1": 0}, "submit-1"); err != nil || again.Status != "duplicate" {
		t.Fatalf("duplicate submit = %#v, %v", again, err)
	}
	if _, err := m.SubmitAttempt(ctx, "exam-1", "student", "Ada", nil, "submit-1"); !errors.Is(err, ErrSubmissionRequestConflict) {
		t.Fatalf("request ID payload mismatch = %v", err)
	}
	if _, err := m.SubmitAttempt(ctx, "exam-1", "student", "Ada", nil, "submit-2"); !errors.Is(err, ErrSubmissionConflict) {
		t.Fatalf("submission conflict = %v", err)
	}
	if _, err := m.SaveAttempt(ctx, "exam-1", "student", nil, 1, "after-submit"); !errors.Is(err, ErrAttemptSubmitted) {
		t.Fatalf("save after submit = %v", err)
	}
	if submission, _ := m.GetSubmission(ctx, "exam-1", "student"); submission == nil || submission.Score != 3 {
		t.Fatal("submission missing")
	}
	if missing, _ := m.GetSubmission(ctx, "missing", "student"); missing != nil {
		t.Fatal("missing submission should be nil")
	}
	m.submissions["other/student"] = &model.Submission{PK: "SUBMISSION#other", SK: "STUDENT#student", RequestID: "other", UpdatedAt: m.now().Add(-time.Hour).UnixMilli()}
	if rows, err := m.ListSubmissions(ctx, "exam-1", 1); err != nil || len(rows) != 1 {
		t.Fatalf("exam submissions = %#v, %v", rows, err)
	}
	if rows, err := m.ListSubmissions(ctx, "", 1); err != nil || len(rows) != 1 {
		t.Fatalf("limited submissions = %#v, %v", rows, err)
	}
	if rows, err := m.ListSubmissions(ctx, "", 0); err != nil || len(rows) != 2 {
		t.Fatalf("all submissions = %#v, %v", rows, err)
	}
	duplicate := m.outbox[0]
	m.outbox = append(m.outbox,
		duplicate,
		model.OutboxEntry{RequestID: "older", TargetPK: "SUBMISSION#missing", TargetSK: "STUDENT#student", CreatedAt: m.now().Add(-time.Hour).UnixMilli()},
	)
	if rows, err := m.ListOutbox(ctx, 1); err != nil || len(rows) != 1 {
		t.Fatalf("outbox = %#v, %v", rows, err)
	}
	if count, err := m.ReconcileOutbox(ctx); err != nil || count != 1 {
		t.Fatalf("first outbox reconciliation = %d, %v", count, err)
	}
	if rows, err := m.ListAuditSummaries(ctx, 0); err != nil || len(rows) != 1 || rows[0].RequestID != "submit-1" || rows[0].Score != 3 || rows[0].Total != 3 {
		t.Fatalf("audit summaries = %#v, %v", rows, err)
	}
	if rows, err := m.ListOutbox(ctx, 0); err != nil || len(rows) != 3 || rows[0].Processed || !rows[1].Processed || !rows[2].Processed {
		t.Fatalf("outbox processed state = %#v, %v", rows, err)
	}
	if count, err := m.ReconcileOutbox(ctx); err != nil || count != 0 {
		t.Fatalf("repeated outbox reconciliation = %d, %v", count, err)
	}
	if _, err := fixedMemory().SubmitAttempt(ctx, "no-exam", "student", "", nil, ""); err != nil {
		t.Fatalf("generated submission request id = %v", err)
	}
	if ok, latency, err := m.HeartbeatProbe(ctx, "x"); err != nil || !ok || latency != 0 {
		t.Fatalf("memory heartbeat = %v %v %v", ok, latency, err)
	}
	if ts, err := m.LatestReplicatedTS(ctx, "x"); err != nil || ts != 0 {
		t.Fatalf("memory replication timestamp = %d, %v", ts, err)
	}
	if n, err := m.DeleteExam(ctx, "missing"); err != nil || n != 0 {
		t.Fatalf("missing exam delete = %d, %v", n, err)
	}
	if n, err := m.DeleteExam(ctx, "exam-1"); err != nil || n != 3 {
		t.Fatalf("exam delete = %d, %v", n, err)
	}
	if deleted, err := m.DeleteQuizDraft(ctx, "missing", "teacher"); err != nil || deleted {
		t.Fatalf("missing draft delete = %v, %v", deleted, err)
	}
	if deleted, err := m.DeleteQuizDraft(ctx, "copy-1", "admin"); err != nil || !deleted {
		t.Fatalf("draft delete = %v, %v", deleted, err)
	}
}
