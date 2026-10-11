package store

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/model"
)

func TestDynamoExamAttemptAndSubmissionErrorPaths(t *testing.T) {
	ctx := context.Background()
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)

	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"pk": &types.AttributeValueMemberS{Value: "EXAM#bad-meta"}, "sk": &types.AttributeValueMemberS{Value: "META"}, "title": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}}}}
	if _, err := s.GetExam(ctx, "bad-meta", true); err == nil {
		t.Fatal("bad exam metadata should fail to decode")
	}
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"pk": &types.AttributeValueMemberS{Value: "EXAM#bad-question"}, "sk": &types.AttributeValueMemberS{Value: "META"}, "title": &types.AttributeValueMemberS{Value: "Exam"}}, {"pk": &types.AttributeValueMemberS{Value: "EXAM#bad-question"}, "sk": &types.AttributeValueMemberS{Value: "QUESTION#0"}, "question": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}}}}
	if _, err := s.GetExam(ctx, "bad-question", true); err == nil {
		t.Fatal("bad exam question should fail to decode")
	}
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{scriptedAttributeMap(model.ExamMeta{PK: "EXAM#public", SK: "META", Title: "Public"}), scriptedAttributeMap(questionItem{PK: "EXAM#public", SK: "QUESTION#0", Question: "q", Answer: 1, Feedback: "feedback"})}}}
	if exam, err := s.GetExam(ctx, "public", false); err != nil || exam == nil || exam.Questions[0].Answer != 0 || exam.Questions[0].Feedback != "" {
		t.Fatalf("public exam = %#v, %v", exam, err)
	}

	fake.queryErrors = []error{errors.New("answer key query failed")}
	if _, err := s.AnswerKey(ctx, "error"); err == nil {
		t.Fatal("answer key query errors should be returned")
	}
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{
		{"sk": &types.AttributeValueMemberN{Value: "1"}},
		{"sk": &types.AttributeValueMemberS{Value: "OTHER"}},
		{"sk": &types.AttributeValueMemberS{Value: "QUESTION#bad"}},
	}}}
	if keyValues, err := s.AnswerKey(ctx, "shapes"); err != nil || len(keyValues) != 0 {
		t.Fatalf("answer key ignored shapes = %#v, %v", keyValues, err)
	}
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"sk": &types.AttributeValueMemberS{Value: "QUESTION#0"}, "answer": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}}}}
	if _, err := s.AnswerKey(ctx, "bad-answer"); err == nil {
		t.Fatal("bad answer value should fail")
	}

	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"sk": &types.AttributeValueMemberS{Value: "QUESTION#0"}, "question": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}}}}
	if _, _, err := s.GradeAttempt(ctx, "bad-grade", nil); err == nil {
		t.Fatal("bad grade question should fail")
	}

	seedItem(fake, map[string]any{"pk": "ATTEMPT#bad-existing", "sk": "STUDENT#student", "started_at": "bad"})
	if _, err := s.RecordAttemptStart(ctx, "bad-existing", "student"); err == nil {
		t.Fatal("bad existing attempt should fail to decode")
	}
	realMarshal := marshal
	marshal = func(any) (map[string]types.AttributeValue, error) { return nil, errors.New("attempt marshal failed") }
	if _, err := s.RecordAttemptStart(ctx, "marshal-attempt", "student"); err == nil {
		t.Fatal("attempt marshal errors should be returned")
	}
	marshal = realMarshal
	fake.putErrors = []error{&types.ConditionalCheckFailedException{}}
	fake.getErrors = []error{nil, errors.New("conditional winner read failed")}
	if _, err := s.RecordAttemptStart(ctx, "conditional-read-error", "student"); err == nil {
		t.Fatal("conditional winner read errors should be returned")
	}

	seedItem(fake, model.ExamMeta{PK: "EXAM#bad-deadline", SK: "META", Title: "Timed", DurationMinutes: 10})
	seedItem(fake, map[string]any{"pk": "ATTEMPT#bad-deadline", "sk": "STUDENT#student", "started_at": "bad"})
	if _, _, err := s.AttemptDeadline(ctx, "bad-deadline", "student"); err == nil {
		t.Fatal("bad deadline attempt should fail to decode")
	}

	fake.getErrors = []error{errors.New("save read failed")}
	if _, err := s.SaveAttempt(ctx, "save-read-error", "student", nil, 0, "req"); err == nil {
		t.Fatal("save read errors should be returned")
	}
	fake.putErrors = []error{errors.New("save start failed")}
	if _, err := s.SaveAttempt(ctx, "save-start-error", "student", nil, 0, "req"); err == nil {
		t.Fatal("save start errors should be returned")
	}
	seedItem(fake, model.Attempt{PK: "ATTEMPT#save-submission-error", SK: "STUDENT#student", Version: 0})
	fake.getErrors = []error{nil, errors.New("save submission read failed")}
	if _, err := s.SaveAttempt(ctx, "save-submission-error", "student", nil, 0, "req"); err == nil {
		t.Fatal("save submission read errors should be returned")
	}
	seedItem(fake, model.Attempt{PK: "ATTEMPT#save-marshal", SK: "STUDENT#student", Version: 0})
	marshal = func(any) (map[string]types.AttributeValue, error) { return nil, errors.New("save marshal failed") }
	if _, err := s.SaveAttempt(ctx, "save-marshal", "student", nil, 0, "req"); err == nil {
		t.Fatal("save marshal errors should be returned")
	}
	marshal = realMarshal
	seedItem(fake, model.Attempt{PK: "ATTEMPT#save-conditional-error", SK: "STUDENT#student", Version: 0})
	fake.getErrors = []error{nil, nil, errors.New("save conditional read failed")}
	fake.transactionErrors = []error{&types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}}
	if _, err := s.SaveAttempt(ctx, "save-conditional-error", "student", nil, 0, "req"); err == nil {
		t.Fatal("conditional save read errors should be returned")
	}

	fake.getResponses = []map[string]types.AttributeValue{nil}
	fake.transactionErrors = []error{&types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}}
	fake.getErrors = []error{nil, errors.New("submission winner read failed")}
	if _, err := s.SubmitAttempt(ctx, "winner-read-error", "student", "Ada", nil, "winner"); err == nil {
		t.Fatal("submission winner read errors should be returned")
	}
	winningSubmission, _ := marshal(model.Submission{PK: "SUBMISSION#bad-winner", SK: "STUDENT#student", RequestID: "another-request", Answers: map[string]int{}})
	fake.getResponses = []map[string]types.AttributeValue{nil, nil, nil, nil, winningSubmission}
	fake.getErrors = nil
	fake.transactionErrors = []error{&types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}}
	if _, err := s.SubmitAttempt(ctx, "bad-winner", "student", "Ada", nil, "winner"); !errors.Is(err, ErrSubmissionConflict) {
		t.Fatalf("bad submission winner = %v", err)
	}
	if result, err := s.SubmitAttempt(ctx, "generated-request", "student", "Ada", nil, ""); err != nil || result.RequestID == "" {
		t.Fatalf("generated request submission = %#v, %v", result, err)
	}

	seedItem(fake, map[string]any{"pk": "SUBMISSION#bad-get", "sk": "STUDENT#student", "request_id": map[string]any{"bad": true}})
	if _, err := s.GetSubmission(ctx, "bad-get", "student"); err == nil {
		t.Fatal("bad submission should fail to decode")
	}
	fake.scanPages = []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{{"pk": &types.AttributeValueMemberS{Value: "SUBMISSION#bad-list"}, "sk": &types.AttributeValueMemberS{Value: "STUDENT#student"}, "request_id": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}}}}
	if _, err := s.ListSubmissions(ctx, "", 1); err == nil {
		t.Fatal("bad listed submission should fail to decode")
	}

	// Receipt serialization must fail before the canonical submit transaction.
	seedItem(fake, model.ExamMeta{PK: "EXAM#marker", SK: "META", Title: "Marker"})
	markerQuestion, _ := quizQuestionItem("EXAM#marker", 0, model.Question{Question: "q"})
	fake.items[itemKey(markerQuestion)] = markerQuestion
	seedItem(fake, model.Attempt{PK: "ATTEMPT#marker", SK: "STUDENT#student"})
	count := 0
	marshal = func(value any) (map[string]types.AttributeValue, error) {
		count++
		if count == 3 {
			return nil, errors.New("marker marshal failed")
		}
		return realMarshal(value)
	}
	transactionCount := len(fake.transactions)
	if result, err := s.SubmitAttempt(ctx, "marker", "student", "Ada", nil, "marker-req"); err == nil || err.Error() != "marker marshal failed" {
		t.Fatalf("submission receipt marshal result = %#v, %v", result, err)
	}
	if len(fake.transactions) != transactionCount {
		t.Fatal("submission transaction ran after receipt serialization failed")
	}
	marshal = realMarshal
}
