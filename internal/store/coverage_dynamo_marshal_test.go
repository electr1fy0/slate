package store

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/model"
)

func TestDynamoMarshalFailurePaths(t *testing.T) {
	ctx := context.Background()
	realMarshal := marshal
	restore := func() { marshal = realMarshal }
	t.Cleanup(restore)

	s := fixedDynamo()
	marshal = func(any) (map[string]types.AttributeValue, error) {
		return nil, errors.New("profile marshal failed")
	}
	if err := s.CreateUser(ctx, &model.User{UserID: "marshal-user", Email: "marshal@example.com"}, "hash"); err == nil {
		t.Fatal("profile marshal error should be returned")
	}
	count := 0
	marshal = func(value any) (map[string]types.AttributeValue, error) {
		count++
		if count == 2 {
			return nil, errors.New("index marshal failed")
		}
		return realMarshal(value)
	}
	if err := s.CreateUser(ctx, &model.User{UserID: "marshal-user", Email: "marshal@example.com"}, "hash"); err == nil {
		t.Fatal("email index marshal error should be returned")
	}

	marshal = func(any) (map[string]types.AttributeValue, error) {
		return nil, errors.New("exam metadata marshal failed")
	}
	if err := s.EnsureExam(ctx, "marshal-exam", "Exam", nil, nil); err == nil {
		t.Fatal("exam metadata marshal error should be returned")
	}
	count = 0
	marshal = func(value any) (map[string]types.AttributeValue, error) {
		count++
		if count == 2 {
			return nil, errors.New("exam question marshal failed")
		}
		return realMarshal(value)
	}
	if err := s.EnsureExam(ctx, "marshal-exam", "Exam", []model.Question{{Question: "q"}}, nil); err == nil {
		t.Fatal("exam question marshal error should be returned")
	}

	marshal = func(any) (map[string]types.AttributeValue, error) {
		return nil, errors.New("definition metadata marshal failed")
	}
	if err := s.writeExamDefinition(ctx, "definition", model.ExamMeta{}, nil, false); err == nil {
		t.Fatal("definition metadata marshal error should be returned")
	}
	count = 0
	marshal = func(value any) (map[string]types.AttributeValue, error) {
		count++
		if count == 2 {
			return nil, errors.New("definition question marshal failed")
		}
		return realMarshal(value)
	}
	if err := s.writeExamDefinition(ctx, "definition", model.ExamMeta{}, []model.Question{{Question: "q"}}, false); err == nil {
		t.Fatal("definition question marshal error should be returned")
	}

	marshal = func(any) (map[string]types.AttributeValue, error) {
		return nil, errors.New("draft metadata marshal failed")
	}
	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "marshal-draft", OwnerID: "owner", Title: "Draft"}); err == nil {
		t.Fatal("draft metadata marshal error should be returned")
	}
	count = 0
	marshal = func(value any) (map[string]types.AttributeValue, error) {
		count++
		if count == 2 {
			return nil, errors.New("draft question marshal failed")
		}
		return realMarshal(value)
	}
	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "marshal-draft", OwnerID: "owner", Title: "Draft", Questions: []model.Question{{Question: "q"}}}); err == nil {
		t.Fatal("draft question marshal error should be returned")
	}

	marshal = realMarshal
	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "update-marshal", OwnerID: "owner", Title: "Draft", Questions: []model.Question{{Question: "q"}}}); err != nil {
		t.Fatal(err)
	}
	marshal = func(any) (map[string]types.AttributeValue, error) {
		return nil, errors.New("updated draft metadata marshal failed")
	}
	if _, err := s.UpdateQuizDraft(ctx, "owner", &model.QuizDraft{ID: "update-marshal", Title: "Updated"}); err == nil {
		t.Fatal("updated draft metadata marshal error should be returned")
	}
	// Recreate because the failed update deletes the old rows first.
	marshal = realMarshal
	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "update-question-marshal", OwnerID: "owner", Title: "Draft", Questions: []model.Question{{Question: "q"}}}); err != nil {
		t.Fatal(err)
	}
	count = 0
	marshal = func(value any) (map[string]types.AttributeValue, error) {
		count++
		if count == 2 {
			return nil, errors.New("updated draft question marshal failed")
		}
		return realMarshal(value)
	}
	if _, err := s.UpdateQuizDraft(ctx, "owner", &model.QuizDraft{ID: "update-question-marshal", Title: "Updated", Questions: []model.Question{{Question: "new"}}}); err == nil {
		t.Fatal("updated draft question marshal error should be returned")
	}

	marshal = realMarshal
	classroom := &model.Classroom{ID: "marshal-class", OwnerID: "owner", JoinCode: "MC"}
	marshal = func(any) (map[string]types.AttributeValue, error) {
		return nil, errors.New("classroom metadata marshal failed")
	}
	if err := s.CreateClassroom(ctx, classroom); err == nil {
		t.Fatal("classroom metadata marshal error should be returned")
	}
	count = 0
	marshal = func(value any) (map[string]types.AttributeValue, error) {
		count++
		if count == 2 {
			return nil, errors.New("classroom code marshal failed")
		}
		return realMarshal(value)
	}
	if err := s.CreateClassroom(ctx, classroom); err == nil {
		t.Fatal("classroom code marshal error should be returned")
	}
	count = 0
	marshal = func(value any) (map[string]types.AttributeValue, error) {
		count++
		if count == 3 {
			return nil, errors.New("classroom owner marshal failed")
		}
		return realMarshal(value)
	}
	if err := s.CreateClassroom(ctx, classroom); err == nil {
		t.Fatal("classroom owner marshal error should be returned")
	}

	marshal = realMarshal
	if err := s.CreateClassroom(ctx, &model.Classroom{ID: "member-marshal", OwnerID: "owner", JoinCode: "MM"}); err != nil {
		t.Fatal(err)
	}
	marshal = func(any) (map[string]types.AttributeValue, error) {
		return nil, errors.New("member marshal failed")
	}
	if err := s.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "member-marshal", UserID: "student"}); err == nil {
		t.Fatal("member marshal error should be returned")
	}
	count = 0
	marshal = func(value any) (map[string]types.AttributeValue, error) {
		count++
		if count == 2 {
			return nil, errors.New("member index marshal failed")
		}
		return realMarshal(value)
	}
	if err := s.AddClassroomMember(ctx, &model.ClassroomMember{ClassroomID: "member-marshal", UserID: "student"}); err == nil {
		t.Fatal("member index marshal error should be returned")
	}

	marshal = func(any) (map[string]types.AttributeValue, error) {
		return nil, errors.New("submission marshal failed")
	}
	if _, err := s.SubmitAttempt(ctx, "marshal-submit", "student", "Ada", nil, "req"); err == nil {
		t.Fatal("submission marshal error should be returned")
	}
	count = 0
	marshal = func(value any) (map[string]types.AttributeValue, error) {
		count++
		if count == 2 {
			return nil, errors.New("journal marshal failed")
		}
		return realMarshal(value)
	}
	if _, err := s.SubmitAttempt(ctx, "journal-marshal", "student", "Ada", nil, "req"); err == nil {
		t.Fatal("journal marshal error should be returned")
	}

	marshal = func(any) (map[string]types.AttributeValue, error) {
		return nil, errors.New("heartbeat marshal failed")
	}
	if _, _, err := s.HeartbeatProbe(ctx, "marshal"); err == nil {
		t.Fatal("heartbeat marshal error should be returned")
	}
}
