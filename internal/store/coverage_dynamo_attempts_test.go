package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/model"
)

func TestDynamoAttemptAndSubmissionPaths(t *testing.T) {
	ctx := context.Background()
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)

	started, err := s.RecordAttemptStart(ctx, "exam", "student")
	if err != nil || started == nil || started.StartedAt == 0 {
		t.Fatalf("new attempt = %#v, %v", started, err)
	}
	if existing, err := s.RecordAttemptStart(ctx, "exam", "student"); err != nil || existing == nil || existing.StartedAt != started.StartedAt {
		t.Fatalf("existing attempt = %#v, %v", existing, err)
	}
	fake.getErrors = []error{errors.New("attempt read failed")}
	if _, err := s.RecordAttemptStart(ctx, "read-error", "student"); err == nil {
		t.Fatal("RecordAttemptStart should return read errors")
	}
	fake.putErrors = []error{errors.New("attempt write failed")}
	if _, err := s.RecordAttemptStart(ctx, "write-error", "student"); err == nil {
		t.Fatal("RecordAttemptStart should return write errors")
	}
	fake.putErrors = []error{&types.ConditionalCheckFailedException{}}
	fake.getResponses = []map[string]types.AttributeValue{nil, nil}
	if attempt, err := s.RecordAttemptStart(ctx, "conditional-missing", "student"); err != nil || attempt == nil {
		t.Fatalf("conditional attempt without winner = %#v, %v", attempt, err)
	}
	winner := scriptedAttributeMap(model.Attempt{PK: "ATTEMPT#conditional-winner", SK: "STUDENT#student", StartedAt: 42, StartedAtISO: "winner"})
	fake.putErrors = []error{&types.ConditionalCheckFailedException{}}
	fake.getResponses = []map[string]types.AttributeValue{nil, winner}
	if attempt, err := s.RecordAttemptStart(ctx, "conditional-winner", "student"); err != nil || attempt == nil || attempt.StartedAt != 42 {
		t.Fatalf("conditional attempt winner = %#v, %v", attempt, err)
	}

	seedItem(fake, model.ExamMeta{PK: "EXAM#timed", SK: "META", Title: "Timed", DurationMinutes: 30})
	seedItem(fake, model.Attempt{PK: "ATTEMPT#timed", SK: "STUDENT#student", StartedAt: 100, StartedAtISO: "start"})
	if attempt, deadline, err := s.AttemptDeadline(ctx, "timed", "student"); err != nil || attempt == nil || deadline == nil || *deadline != 100+30*60000 {
		t.Fatalf("deadline = %#v, %v, %v", attempt, deadline, err)
	}
	seedItem(fake, model.ExamMeta{PK: "EXAM#untimed", SK: "META", Title: "Untimed", DurationMinutes: 0})
	if attempt, deadline, err := s.AttemptDeadline(ctx, "untimed", "student"); err != nil || attempt != nil || deadline != nil {
		t.Fatalf("untimed deadline = %#v, %v, %v", attempt, deadline, err)
	}
	if attempt, deadline, err := s.AttemptDeadline(ctx, "timed", "missing"); err != nil || attempt != nil || deadline != nil {
		t.Fatalf("missing attempt deadline = %#v, %v, %v", attempt, deadline, err)
	}
	fake.getErrors = []error{errors.New("deadline metadata failed")}
	if _, _, err := s.AttemptDeadline(ctx, "timed", "student"); err == nil {
		t.Fatal("AttemptDeadline should return metadata errors")
	}
	fake.getErrors = []error{nil, errors.New("deadline attempt failed")}
	if _, _, err := s.AttemptDeadline(ctx, "timed", "student"); err == nil {
		t.Fatal("AttemptDeadline should return attempt errors")
	}

	if attempt, err := s.GetStudentAttempt(ctx, "missing", "student"); err != nil || attempt != nil {
		t.Fatalf("missing student attempt = %#v, %v", attempt, err)
	}
	seedItem(fake, model.Attempt{PK: "ATTEMPT#get", SK: "STUDENT#student", StartedAt: 10, Answers: map[string]int{"0": 1}})
	if attempt, err := s.GetStudentAttempt(ctx, "get", "student"); err != nil || attempt == nil || attempt.Answers["0"] != 1 {
		t.Fatalf("student attempt = %#v, %v", attempt, err)
	}
	fake.getErrors = []error{errors.New("student attempt read failed")}
	if _, err := s.GetStudentAttempt(ctx, "get", "student"); err == nil {
		t.Fatal("GetStudentAttempt should return backend errors")
	}
	seedItem(fake, map[string]any{"pk": "ATTEMPT#bad", "sk": "STUDENT#student", "started_at": "bad"})
	if _, err := s.GetStudentAttempt(ctx, "bad", "student"); err == nil {
		t.Fatal("bad student attempt should fail to decode")
	}

	if _, err := s.SaveAttempt(ctx, "get", "student", nil, 0, ""); err == nil {
		t.Fatal("empty save request should fail")
	}
	save, err := s.SaveAttempt(ctx, "new-save", "student", map[string]int{"0": 1}, 0, "save-1")
	if err != nil || save == nil || save.Version != 1 || save.AttemptNumber != 1 {
		t.Fatalf("new save = %#v, %v", save, err)
	}
	if duplicate, err := s.SaveAttempt(ctx, "new-save", "student", nil, 100, "save-1"); err != nil || duplicate.Version != 1 {
		t.Fatalf("idempotent save = %#v, %v", duplicate, err)
	}
	seedItem(fake, model.Submission{PK: "SUBMISSION#submitted", SK: "STUDENT#student", RequestID: "submit"})
	seedItem(fake, model.Attempt{PK: "ATTEMPT#submitted", SK: "STUDENT#student", Version: 0})
	if _, err := s.SaveAttempt(ctx, "submitted", "student", nil, 0, "save"); !errors.Is(err, ErrAttemptSubmitted) {
		t.Fatalf("save after submission = %v", err)
	}
	seedItem(fake, model.Attempt{PK: "ATTEMPT#flagged", SK: "STUDENT#student", Submitted: true})
	if _, err := s.SaveAttempt(ctx, "flagged", "student", nil, 0, "save"); !errors.Is(err, ErrAttemptSubmitted) {
		t.Fatalf("save flagged submitted = %v", err)
	}
	seedItem(fake, model.Attempt{PK: "ATTEMPT#version", SK: "STUDENT#student", Version: 3})
	if _, err := s.SaveAttempt(ctx, "version", "student", nil, 2, "save"); !errors.Is(err, ErrAttemptConflict) {
		t.Fatalf("save version conflict = %v", err)
	}
	initial := scriptedAttributeMap(model.Attempt{PK: "ATTEMPT#retry", SK: "STUDENT#student", Version: 0})
	current := scriptedAttributeMap(model.Attempt{PK: "ATTEMPT#retry", SK: "STUDENT#student", Version: 1, LastRequestID: "retry"})
	fake.getResponses = []map[string]types.AttributeValue{initial, nil, current}
	fake.putErrors = []error{&types.ConditionalCheckFailedException{}}
	if attempt, err := s.SaveAttempt(ctx, "retry", "student", map[string]int{"0": 1}, 0, "retry"); err != nil || attempt == nil || attempt.LastRequestID != "retry" {
		t.Fatalf("conditional idempotent save = %#v, %v", attempt, err)
	}
	other := scriptedAttributeMap(model.Attempt{PK: "ATTEMPT#retry-other", SK: "STUDENT#student", Version: 1, LastRequestID: "other"})
	fake.getResponses = []map[string]types.AttributeValue{initial, nil, other}
	fake.putErrors = []error{&types.ConditionalCheckFailedException{}}
	if _, err := s.SaveAttempt(ctx, "retry-other", "student", nil, 0, "retry"); !errors.Is(err, ErrAttemptConflict) {
		t.Fatalf("conditional save conflict = %v", err)
	}
	seedItem(fake, model.Attempt{PK: "ATTEMPT#save-error", SK: "STUDENT#student", Version: 0})
	fake.putErrors = []error{errors.New("attempt save failed")}
	if _, err := s.SaveAttempt(ctx, "save-error", "student", nil, 0, "save"); err == nil {
		t.Fatal("generic save error should be returned")
	}

	seedItem(fake, model.ExamMeta{PK: "EXAM#submit", SK: "META", Title: "Submit"})
	question, _ := quizQuestionItem("EXAM#submit", 0, model.Question{Question: "q", Answer: 1, Points: 2})
	fake.items[itemKey(question)] = question
	committed, err := s.SubmitAttempt(ctx, "submit", "student", "Ada", map[string]int{"0": 1}, "req-1")
	if err != nil || committed.Status != "committed" || committed.Score != 2 || committed.Total != 2 {
		t.Fatalf("committed submission = %#v, %v", committed, err)
	}
	if duplicate, err := s.SubmitAttempt(ctx, "submit", "student", "Ada", nil, "req-1"); err != nil || duplicate.Status != "duplicate" || duplicate.Score != 2 {
		t.Fatalf("duplicate submission = %#v, %v", duplicate, err)
	}
	if _, err := s.SubmitAttempt(ctx, "submit", "student", "Ada", nil, "req-2"); !errors.Is(err, ErrSubmissionConflict) {
		t.Fatalf("submission conflict = %v", err)
	}
	fake.getErrors = []error{errors.New("submission read failed")}
	if _, err := s.SubmitAttempt(ctx, "read-submit", "student", "Ada", nil, "req"); err == nil {
		t.Fatal("SubmitAttempt should return initial read errors")
	}
	seedItem(fake, map[string]any{"pk": "SUBMISSION#bad-submit", "sk": "STUDENT#student", "request_id": map[string]any{"bad": true}})
	if _, err := s.SubmitAttempt(ctx, "bad-submit", "student", "Ada", nil, "req"); err == nil {
		t.Fatal("bad existing submission should fail to decode")
	}

	// A conditional transaction can resolve either to the same idempotent
	// winner or to a genuine conflict.
	winnerSub := scriptedAttributeMap(model.Submission{PK: "SUBMISSION#race", SK: "STUDENT#student", RequestID: "race", Version: 1, Score: 2, Total: 2})
	fake.getResponses = []map[string]types.AttributeValue{nil, winnerSub}
	fake.transactionErrors = []error{&types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}}
	if result, err := s.SubmitAttempt(ctx, "race", "student", "Ada", nil, "race"); err != nil || result.Status != "duplicate" {
		t.Fatalf("conditional duplicate winner = %#v, %v", result, err)
	}
	otherSub := scriptedAttributeMap(model.Submission{PK: "SUBMISSION#race-other", SK: "STUDENT#student", RequestID: "other", Version: 1})
	fake.getResponses = []map[string]types.AttributeValue{nil, otherSub}
	fake.transactionErrors = []error{&types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}}
	if _, err := s.SubmitAttempt(ctx, "race-other", "student", "Ada", nil, "race"); !errors.Is(err, ErrSubmissionConflict) {
		t.Fatalf("conditional submission conflict = %v", err)
	}
	// The grade query is intentionally allowed to fail before the transaction.
	fake.queryErrors = []error{errors.New("grade query failed")}
	if _, err := s.SubmitAttempt(ctx, "grade-error", "student", "Ada", nil, "req"); err == nil {
		t.Fatal("submission grading errors should be returned")
	}
	fake.transactionErrors = []error{errors.New("submission transaction failed")}
	if _, err := s.SubmitAttempt(ctx, "tx-error", "student", "Ada", nil, "req"); err == nil {
		t.Fatal("submission transaction errors should be returned")
	}

	seedItem(fake, model.ExamMeta{PK: "EXAM#attempt-submit", SK: "META", Title: "Attempt submit"})
	seedItem(fake, model.Attempt{PK: "ATTEMPT#attempt-submit", SK: "STUDENT#student", AttemptNumber: 3})
	fake.putErrors = []error{errors.New("attempt marker write failed")}
	result, err := s.SubmitAttempt(ctx, "attempt-submit", "student", "Ada", nil, "attempt-submit-req")
	if err != nil || result.Status != "committed" || result.AttemptNumber != 0 {
		t.Fatalf("submission with attempt = %#v, %v", result, err)
	}

	if submission, err := s.GetSubmission(ctx, "missing", "student"); err != nil || submission != nil {
		t.Fatalf("missing submission = %#v, %v", submission, err)
	}
	if submission, err := s.GetSubmission(ctx, "submit", "student"); err != nil || submission == nil || submission.RequestID != "req-1" {
		t.Fatalf("stored submission = %#v, %v", submission, err)
	}
	fake.getErrors = []error{errors.New("submission get failed")}
	if _, err := s.GetSubmission(ctx, "submit", "student"); err == nil {
		t.Fatal("GetSubmission should return backend errors")
	}

	if rows, err := s.ListSubmissions(ctx, "submit", 10); err != nil || len(rows) != 1 {
		t.Fatalf("exam submissions = %#v, %v", rows, err)
	}
	fake.scanPages = []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{scriptedAttributeMap(model.Submission{PK: "SUBMISSION#submit", SK: "STUDENT#student", RequestID: "req-1", Score: 2, Total: 2})}}}
	if rows, err := s.ListSubmissions(ctx, "", 10); err != nil || len(rows) == 0 {
		t.Fatalf("all submissions = %#v, %v", rows, err)
	}
	fake.queryErrors = []error{errors.New("submission query failed")}
	if _, err := s.ListSubmissions(ctx, "submit", 1); err == nil {
		t.Fatal("submission query errors should be returned")
	}
	fake.scanErrors = []error{errors.New("submission scan failed")}
	if _, err := s.ListSubmissions(ctx, "", 1); err == nil {
		t.Fatal("submission scan errors should be returned")
	}

	outbox := scriptedAttributeMap(model.OutboxEntry{PK: "JOURNAL#x", SK: "OUTBOX", RequestID: "x", CreatedAt: 1})
	fake.scanPages = []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{outbox}}}
	if rows, err := s.ListOutbox(ctx, 0); err != nil || len(rows) != 1 || rows[0].RequestID != "x" {
		t.Fatalf("outbox = %#v, %v", rows, err)
	}
	fake.scanErrors = []error{errors.New("outbox scan failed")}
	if _, err := s.ListOutbox(ctx, 1); err == nil {
		t.Fatal("outbox scan errors should be returned")
	}
	fake.scanPages = []*dynamodb.ScanOutput{{Items: []map[string]types.AttributeValue{{"pk": &types.AttributeValueMemberS{Value: "JOURNAL#bad"}, "sk": &types.AttributeValueMemberS{Value: "OUTBOX"}, "created_at": &types.AttributeValueMemberS{Value: "bad"}}}}}
	if _, err := s.ListOutbox(ctx, 1); err == nil {
		t.Fatal("bad outbox should fail to decode")
	}
}

func TestDynamoHealthListingsAndExamDeletion(t *testing.T) {
	ctx := context.Background()
	s := fixedDynamo()
	fake := s.Client.(*scriptedDynamo)

	ok, latency, err := s.HeartbeatProbe(ctx, "source")
	if err != nil || !ok || latency < 0 {
		t.Fatalf("heartbeat = %v %v %v", ok, latency, err)
	}
	fake.putErrors = []error{errors.New("heartbeat write failed")}
	if ok, _, err := s.HeartbeatProbe(ctx, "source"); err == nil || ok {
		t.Fatalf("heartbeat write error = %v %v", ok, err)
	}
	fake.getErrors = []error{errors.New("heartbeat read failed")}
	if ok, _, err := s.HeartbeatProbe(ctx, "source"); err == nil || ok {
		t.Fatalf("heartbeat read error = %v %v", ok, err)
	}
	fake.getResponses = []map[string]types.AttributeValue{nil}
	if ok, _, err := s.HeartbeatProbe(ctx, "source"); err != nil || ok {
		t.Fatalf("missing heartbeat = %v %v", ok, err)
	}
	fake.deleteErrors = []error{errors.New("heartbeat delete failed")}
	if ok, _, err := s.HeartbeatProbe(ctx, "source"); err == nil || !ok {
		t.Fatalf("heartbeat delete error = %v %v", ok, err)
	}

	fake.queryPages = []*dynamodb.QueryOutput{{}}
	if ts, err := s.LatestReplicatedTS(ctx, "missing"); err != nil || ts != 0 {
		t.Fatalf("missing replicated timestamp = %d, %v", ts, err)
	}
	seedItem(fake, map[string]any{"pk": "HEARTBEAT", "sk": "PROBE#source#one", "ts": int64(123)})
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{scriptedAttributeMap(map[string]any{"pk": "HEARTBEAT", "sk": "PROBE#source#one", "ts": int64(123)})}}}
	if ts, err := s.LatestReplicatedTS(ctx, "source"); err != nil || ts != 123 {
		t.Fatalf("replicated timestamp = %d, %v", ts, err)
	}
	fake.queryErrors = []error{errors.New("heartbeat query failed")}
	if _, err := s.LatestReplicatedTS(ctx, "source"); err == nil {
		t.Fatal("replicated timestamp errors should be returned")
	}
	seedItem(fake, map[string]any{"pk": "HEARTBEAT", "sk": "PROBE#bad#one", "ts": "bad"})
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{scriptedAttributeMap(map[string]any{"pk": "HEARTBEAT", "sk": "PROBE#bad#one", "ts": "bad"})}}}
	if _, err := s.LatestReplicatedTS(ctx, "bad"); err == nil {
		t.Fatal("bad replicated timestamp should fail")
	}

	// DeleteExam batches more than 25 records and retries a throttled request.
	for i := 0; i < 26; i++ {
		item := scriptedAttributeMap(questionItem{PK: "EXAM#delete", SK: fmt.Sprintf("QUESTION#%03d", i), Question: "q"})
		fake.items[itemKey(item)] = item
	}
	firstRequest := types.WriteRequest{DeleteRequest: &types.DeleteRequest{Key: key("EXAM#delete", "QUESTION#000")}}
	fake.batchUnprocessed = [][]types.WriteRequest{{firstRequest}}
	if count, err := s.DeleteExam(ctx, "delete"); err != nil || count != 26 {
		t.Fatalf("deleted exam = %d, %v", count, err)
	}
	if count, err := s.DeleteExam(ctx, "missing-delete"); err != nil || count != 0 {
		t.Fatalf("missing exam deletion = %d, %v", count, err)
	}
	fake.queryErrors = []error{errors.New("exam deletion query failed")}
	if _, err := s.DeleteExam(ctx, "query-error"); err == nil {
		t.Fatal("exam deletion query errors should be returned")
	}
	seedItem(fake, questionItem{PK: "EXAM#batch-error", SK: "QUESTION#000", Question: "q"})
	fake.batchErrors = []error{errors.New("exam deletion batch failed")}
	if _, err := s.DeleteExam(ctx, "batch-error"); err == nil {
		t.Fatal("exam deletion batch errors should be returned")
	}
	seedItem(fake, questionItem{PK: "EXAM#cancel", SK: "QUESTION#000", Question: "q"})
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	fake.batchUnprocessed = [][]types.WriteRequest{{{DeleteRequest: &types.DeleteRequest{Key: key("EXAM#cancel", "QUESTION#000")}}}}
	if _, err := s.DeleteExam(cancelled, "cancel"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled exam deletion = %v", err)
	}
}
