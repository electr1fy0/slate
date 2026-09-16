package store

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/model"
)

func TestDynamoQuizPublishDuplicateAndWindow(t *testing.T) {
	ctx := context.Background()
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)
	q := model.Question{Question: "Which?", Options: []string{"a", "b"}, Answer: 1, Points: 2}

	if _, err := s.PublishQuizDraft(ctx, "missing", "teacher"); !errors.Is(err, ErrQuizNotFound) {
		t.Fatalf("missing publish = %v", err)
	}
	draft, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "publish", OwnerID: "teacher", Title: "Publish me", Questions: []model.Question{q}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishQuizDraft(ctx, draft.ID, "other"); !errors.Is(err, ErrQuizForbidden) {
		t.Fatalf("forbidden publish = %v", err)
	}
	published, err := s.PublishQuizDraft(ctx, draft.ID, "teacher")
	if err != nil || published == nil || published.ID != draft.ID || published.Status != "published" {
		t.Fatalf("published meta = %#v, %v", published, err)
	}
	if _, err := s.PublishQuizDraft(ctx, draft.ID, "teacher"); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("republish = %v", err)
	}

	copyDraft, err := s.DuplicateQuiz(ctx, "publish", "teacher", "copy", "")
	if err != nil || copyDraft == nil || copyDraft.Title != "Copy of Publish me" || len(copyDraft.Questions) != 1 {
		t.Fatalf("draft duplicate = %#v, %v", copyDraft, err)
	}
	if _, err := s.DuplicateQuiz(ctx, "publish", "other", "copy-other", "x"); !errors.Is(err, ErrQuizForbidden) {
		t.Fatalf("forbidden draft duplicate = %v", err)
	}
	if _, err := s.DuplicateQuiz(ctx, "publish", "", "copy-none", "x"); err == nil {
		t.Fatal("duplicate without owner should fail")
	}

	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "empty-publish", OwnerID: "teacher", Title: "Empty"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PublishQuizDraft(ctx, "empty-publish", "teacher"); err == nil {
		t.Fatal("empty publish should fail")
	}
	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "existing-publish", OwnerID: "teacher", Title: "Existing", Questions: []model.Question{q}}); err != nil {
		t.Fatal(err)
	}
	seedItem(fake, model.ExamMeta{PK: "EXAM#existing-publish", SK: "META", Title: "Existing"})
	if _, err := s.PublishQuizDraft(ctx, "existing-publish", "teacher"); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("existing exam publish = %v", err)
	}

	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "conditional-publish", OwnerID: "teacher", Title: "Conditional", Questions: []model.Question{q}}); err != nil {
		t.Fatal(err)
	}
	fake.putErrors = []error{&types.ConditionalCheckFailedException{}}
	if _, err := s.PublishQuizDraft(ctx, "conditional-publish", "teacher"); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("conditional definition conflict = %v", err)
	}
	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "update-conditional", OwnerID: "teacher", Title: "Conditional update", Questions: []model.Question{q}}); err != nil {
		t.Fatal(err)
	}
	fake.putErrors = []error{nil, nil}
	fake.updateErrors = []error{&types.ConditionalCheckFailedException{}}
	if _, err := s.PublishQuizDraft(ctx, "update-conditional", "teacher"); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("conditional draft update = %v", err)
	}
	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "update-error", OwnerID: "teacher", Title: "Update error", Questions: []model.Question{q}}); err != nil {
		t.Fatal(err)
	}
	fake.putErrors = []error{nil, nil}
	fake.updateErrors = []error{errors.New("draft status update failed")}
	if _, err := s.PublishQuizDraft(ctx, "update-error", "teacher"); err == nil {
		t.Fatal("generic draft update failure should be returned")
	}

	seedItem(fake, model.ExamMeta{PK: "EXAM#source-exam", SK: "META", Title: "Source Exam", OwnerID: "teacher", CreatedAt: "2026-01-01T00:00:00Z"})
	qItem, _ := quizQuestionItem("EXAM#source-exam", 0, q)
	fake.items[itemKey(qItem)] = qItem
	examCopy, err := s.DuplicateQuiz(ctx, "source-exam", "teacher", "", "")
	if err != nil || examCopy == nil || examCopy.Title != "Copy of Source Exam" || len(examCopy.Questions) != 1 {
		t.Fatalf("exam duplicate = %#v, %v", examCopy, err)
	}
	if _, err := s.DuplicateQuiz(ctx, "source-exam", "other", "exam-copy-other", "x"); !errors.Is(err, ErrQuizForbidden) {
		t.Fatalf("forbidden exam duplicate = %v", err)
	}
	if _, err := s.DuplicateQuiz(ctx, "no-source", "teacher", "x", "x"); !errors.Is(err, ErrQuizNotFound) {
		t.Fatalf("missing source duplicate = %v", err)
	}

	title := "Windowed"
	window := map[string]any{"opens_at": "open", "closes_at": "close", "duration_minutes": 45, "owner_id": "teacher", "status": "published", "max_attempts": 2, "allow_late": true, "late_grace_minutes": 5}
	updated, err := s.UpdateExamWindow(ctx, "source-exam", &title, window)
	if err != nil || updated == nil || updated.Title != title || updated.DurationMinutes != 45 {
		t.Fatalf("updated exam window = %#v, %v", updated, err)
	}
	if unchanged, err := s.UpdateExamWindow(ctx, "source-exam", nil, nil); err != nil || unchanged == nil {
		t.Fatalf("unchanged exam window = %#v, %v", unchanged, err)
	}
	fake.updateErrors = []error{errors.New("window update failed")}
	if _, err := s.UpdateExamWindow(ctx, "source-exam", &title, nil); err == nil {
		t.Fatal("window update error should be returned")
	}
	if _, err := s.UpdateExamWindow(ctx, "source-exam", nil, map[string]any{"duration_minutes": failingAttributeValue{}}); err == nil {
		t.Fatal("invalid window value should fail")
	}
}

func TestDynamoQuizDeleteAndBatchRetry(t *testing.T) {
	ctx := context.Background()
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)
	q := model.Question{Question: "q", Options: []string{"a"}, Answer: 0}
	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "delete-me", OwnerID: "teacher", Title: "Delete", Questions: []model.Question{q}}); err != nil {
		t.Fatal(err)
	}
	items, err := s.queryAll(ctx, &dynamodb.QueryInput{ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: "QUIZ#delete-me"}}})
	if err != nil || len(items) != 2 {
		t.Fatalf("seeded draft items = %d, %v", len(items), err)
	}
	firstRequest := types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: key("QUIZ#delete-me", "META")}}
	fake.batchUnprocessed = [][]types.WriteRequest{{firstRequest}}
	deleted, err := s.DeleteQuizDraft(ctx, "delete-me", "teacher")
	if err != nil || !deleted {
		t.Fatalf("deleted draft = %v, %v", deleted, err)
	}
	if got, _ := s.GetQuizDraft(ctx, "delete-me"); got != nil {
		t.Fatal("deleted draft still exists")
	}
	if deleted, err := s.DeleteQuizDraft(ctx, "missing", "teacher"); err != nil || deleted {
		t.Fatalf("missing delete = %v, %v", deleted, err)
	}

	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "forbidden-delete", OwnerID: "teacher", Title: "Forbidden"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteQuizDraft(ctx, "forbidden-delete", "other"); !errors.Is(err, ErrQuizForbidden) {
		t.Fatalf("forbidden delete = %v", err)
	}
	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "published-delete", OwnerID: "teacher", Title: "Published", Questions: []model.Question{q}}); err != nil {
		t.Fatal(err)
	}
	// Publishing changes the metadata status and makes deletion a conflict.
	if _, err := s.PublishQuizDraft(ctx, "published-delete", "teacher"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteQuizDraft(ctx, "published-delete", "teacher"); !errors.Is(err, ErrQuizConflict) {
		t.Fatalf("published delete = %v", err)
	}

	if _, err := s.CreateQuizDraft(ctx, &model.QuizDraft{ID: "delete-error", OwnerID: "teacher", Title: "Delete error"}); err != nil {
		t.Fatal(err)
	}
	fake.queryErrors = []error{errors.New("delete query failed")}
	if _, err := s.DeleteQuizDraft(ctx, "delete-error", "teacher"); err == nil {
		t.Fatal("delete query error should be returned")
	}
	fake.batchErrors = []error{errors.New("batch delete failed")}
	if _, err := s.DeleteQuizDraft(ctx, "delete-error", "teacher"); err == nil {
		t.Fatal("batch delete error should be returned")
	}
}
