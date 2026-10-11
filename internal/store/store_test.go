package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/clusterstate"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
)

// recordingDynamo is deliberately tiny but stores the actual AttributeValue
// maps, making schema and transaction tests independent of AWS or DynamoDB
// Local.
type recordingDynamo struct {
	items        map[string]map[string]types.AttributeValue
	gets         []*dynamodb.GetItemInput
	puts         []*dynamodb.PutItemInput
	transactions []*dynamodb.TransactWriteItemsInput
}

func newRecordingDynamo() *recordingDynamo {
	return &recordingDynamo{items: map[string]map[string]types.AttributeValue{}}
}
func avString(item map[string]types.AttributeValue, field string) string {
	if av, ok := item[field].(*types.AttributeValueMemberS); ok {
		return av.Value
	}
	return ""
}
func itemKey(item map[string]types.AttributeValue) string {
	return avString(item, "pk") + "\x00" + avString(item, "sk")
}
func (f *recordingDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	f.gets = append(f.gets, in)
	return &dynamodb.GetItemOutput{Item: f.items[itemKey(in.Key)]}, nil
}
func (f *recordingDynamo) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.puts = append(f.puts, in)
	f.items[itemKey(in.Item)] = in.Item
	return &dynamodb.PutItemOutput{}, nil
}
func (f *recordingDynamo) UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	return &dynamodb.UpdateItemOutput{}, nil
}
func (f *recordingDynamo) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	delete(f.items, itemKey(in.Key))
	return &dynamodb.DeleteItemOutput{}, nil
}
func (f *recordingDynamo) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	pk := ""
	if av, ok := in.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS); ok {
		pk = av.Value
	}
	out := &dynamodb.QueryOutput{}
	for _, item := range f.items {
		if avString(item, "pk") == pk {
			out.Items = append(out.Items, item)
		}
	}
	return out, nil
}
func (f *recordingDynamo) Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return &dynamodb.ScanOutput{}, nil
}
func (f *recordingDynamo) BatchWriteItem(context.Context, *dynamodb.BatchWriteItemInput, ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	return &dynamodb.BatchWriteItemOutput{}, nil
}
func (f *recordingDynamo) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	f.transactions = append(f.transactions, in)
	for _, tx := range in.TransactItems {
		if tx.Put != nil {
			f.items[itemKey(tx.Put.Item)] = tx.Put.Item
		} else if tx.Update != nil {
			updated := cloneAttributeMap(f.items[itemKey(tx.Update.Key)])
			if updated == nil {
				updated = cloneAttributeMap(tx.Update.Key)
			}
			for placeholder, value := range tx.Update.ExpressionAttributeValues {
				alias := "#" + strings.TrimPrefix(placeholder, ":")
				if field, ok := tx.Update.ExpressionAttributeNames[alias]; ok {
					updated[field] = value
				}
			}
			f.items[itemKey(updated)] = updated
		}
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func TestDynamoStorePreservesExamSchemaAndReadVisibility(t *testing.T) {
	cfg := config.Defaults()
	f := newRecordingDynamo()
	s := NewWithClient(f, cfg)
	s.Now = func() time.Time { return time.Unix(1700000000, 123000000) }
	ctx := context.Background()
	if err := s.EnsureExam(ctx, "x1", "Exam One", []model.Question{{Question: "q1", Options: []string{"a", "b"}, Answer: 1}}, map[string]any{"opens_at": "2026-01-01T00:00:00Z", "duration_minutes": 45}); err != nil {
		t.Fatal(err)
	}
	meta := f.items["EXAM#x1\x00META"]
	if avString(meta, "title") != "Exam One" || avString(meta, "pk") != "EXAM#x1" || avString(meta, "sk") != "META" {
		t.Fatalf("bad meta %#v", meta)
	}
	exam, err := s.GetExam(ctx, "x1", false)
	if err != nil {
		t.Fatal(err)
	}
	if exam.ID != "x1" || len(exam.Questions) != 1 || exam.Questions[0].Index != 0 || exam.Questions[0].Answer != 0 {
		t.Fatalf("bad public exam %#v", exam)
	}
	full, err := s.GetExam(ctx, "x1", true)
	if err != nil || full.Questions[0].Answer != 1 {
		t.Fatalf("bad private exam %#v %v", full, err)
	}
}

func TestDynamoSeedExamIsCreateOnlyAndAtomic(t *testing.T) {
	cfg := config.Defaults()
	f := newRecordingDynamo()
	s := NewWithClient(f, cfg)
	ctx := context.Background()
	questions := []model.Question{{Question: "Seed question", Options: []string{"A", "B"}, Answer: 1}}
	if err := s.EnsureExamIfMissing(ctx, "seeded", "Seed title", questions, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.transactions) != 1 || len(f.transactions[0].TransactItems) != 2 || f.transactions[0].TransactItems[0].Put == nil || f.transactions[0].TransactItems[0].Put.ConditionExpression == nil || *f.transactions[0].TransactItems[0].Put.ConditionExpression != "attribute_not_exists(pk)" {
		t.Fatalf("seed must atomically condition metadata and write questions: %#v", f.transactions)
	}
	meta := f.items["EXAM#seeded\x00META"]
	meta["title"] = &types.AttributeValueMemberS{Value: "Faculty title"}
	if err := s.EnsureExamIfMissing(ctx, "seeded", "Seed title", questions, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.transactions) != 1 || avString(f.items["EXAM#seeded\x00META"], "title") != "Faculty title" {
		t.Fatal("backend restart overwrote an existing exam")
	}
}

func TestDynamoSubmitIsIdempotentAndConditional(t *testing.T) {
	cfg := config.Defaults()
	f := newRecordingDynamo()
	s := NewWithClient(f, cfg)
	s.Now = func() time.Time { return time.Unix(1700000000, 0) }
	ctx := context.Background()
	if err := s.EnsureExam(ctx, "e", "E", []model.Question{{Question: "q", Options: []string{"a", "b"}, Answer: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	first, err := s.SubmitAttempt(ctx, "e", "student", "S", map[string]int{"0": 1}, "request-1")
	if err != nil || first.Status != "committed" || len(f.transactions) != 2 {
		t.Fatalf("first %#v %v", first, err)
	}
	put := f.transactions[1].TransactItems[0].Put
	if put == nil || put.ConditionExpression == nil {
		t.Fatalf("submission put must be conditional: %#v", put)
	}
	again, err := s.SubmitAttempt(ctx, "e", "student", "S", map[string]int{"0": 1}, "request-1")
	if err != nil || again.Status != "duplicate" || again.Score != 1 || again.CommittedAt == 0 {
		t.Fatalf("duplicate %#v %v", again, err)
	}
	if _, err := s.SubmitAttempt(ctx, "e", "student", "S", map[string]int{"0": 0}, "request-1"); !errors.Is(err, ErrSubmissionRequestConflict) {
		t.Fatalf("same request ID with different payload = %v", err)
	}
	_, err = s.SubmitAttempt(ctx, "e", "student", "S", map[string]int{"0": 1}, "request-2")
	if err == nil || err != ErrSubmissionConflict {
		t.Fatalf("conflict %v", err)
	}
}

func TestDynamoExamMutationsCarryTransactionalFenceAndStrongReads(t *testing.T) {
	cfg := config.Defaults()
	f := newRecordingDynamo()
	s := NewWithClient(f, cfg)
	ctx := clusterstate.WithWriter(context.Background(), clusterstate.Writer{Backend: "A", Epoch: 7})
	if err := s.EnsureExam(context.Background(), "fenced", "Fenced", []model.Question{{Question: "q", Options: []string{"a", "b"}, Answer: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAttemptStart(ctx, "fenced", "student"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveAttempt(ctx, "fenced", "student", map[string]int{"0": 1}, 0, "save-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitAttempt(ctx, "fenced", "student", "Ada", map[string]int{"0": 1}, "submit-1"); err != nil {
		t.Fatal(err)
	}
	if len(f.transactions) != 4 {
		t.Fatalf("exam and bootstrap transactions = %d", len(f.transactions))
	}
	for index, tx := range f.transactions[1:] {
		if len(tx.TransactItems) == 0 {
			t.Fatalf("transaction %d had no items", index+1)
		}
		fence := tx.TransactItems[len(tx.TransactItems)-1].ConditionCheck
		if fence == nil || avString(fence.Key, "pk") != clusterstate.ActivePK || avString(fence.Key, "sk") != clusterstate.ActiveSK || fence.ConditionExpression == nil || fence.ExpressionAttributeNames["#backend"] != "active_backend" || fence.ExpressionAttributeNames["#epoch"] != "epoch" || fence.ExpressionAttributeNames["#paused"] != "paused" {
			t.Fatalf("transaction %d missing atomic writer fence: %#v", index+1, tx.TransactItems)
		}
	}
	submissionTx := f.transactions[3]
	if len(submissionTx.TransactItems) != 5 || submissionTx.TransactItems[0].Put == nil || submissionTx.TransactItems[1].Put == nil || submissionTx.TransactItems[2].Put == nil || submissionTx.TransactItems[3].Put == nil {
		t.Fatalf("submit must atomically write submission, receipt, outbox and attempt: %#v", submissionTx.TransactItems)
	}
	if _, err := s.GetStudentAttempt(context.Background(), "fenced", "student"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSubmission(context.Background(), "fenced", "student"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := s.FindSubmissionRequest(context.Background(), "fenced", "student", map[string]int{"0": 1}, "submit-1"); err != nil || !found {
		t.Fatalf("receipt lookup found=%v error=%v", found, err)
	}
	for _, get := range f.gets {
		if avString(get.Key, "pk") == "ATTEMPT#fenced" || avString(get.Key, "pk") == "SUBMISSION#fenced" || strings.HasPrefix(avString(get.Key, "pk"), "SUBMISSION_REQUEST#") {
			if get.ConsistentRead == nil || !*get.ConsistentRead {
				t.Fatalf("attempt/receipt read was eventual: %#v", get)
			}
		}
	}
}

func TestDynamoRegistrationAndTeacherEditsCarryAtomicFences(t *testing.T) {
	cfg := config.Defaults()
	cfg.FencingEnabled = true
	f := newRecordingDynamo()
	s := NewWithClient(f, cfg)
	writerCtx := clusterstate.WithWriter(context.Background(), clusterstate.Writer{Backend: "A", Epoch: 7})
	if err := s.CreateUser(writerCtx, &model.User{UserID: "u1", Email: "u1@example.com", Name: "Ada"}, "hash"); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureExam(writerCtx, "teacher-edit", "Original", []model.Question{{Question: "q", Options: []string{"a", "b"}, Answer: 1}}, nil); err != nil {
		t.Fatal(err)
	}
	title := "Teacher update"
	if meta, err := s.UpdateExamWindow(writerCtx, "teacher-edit", &title, nil); err != nil || meta == nil || meta.Title != title {
		t.Fatalf("teacher metadata update = %#v, %v", meta, err)
	}
	if len(f.transactions) != 3 {
		t.Fatalf("user, seed, and teacher writes = %d transactions", len(f.transactions))
	}
	for index, tx := range f.transactions {
		last := tx.TransactItems[len(tx.TransactItems)-1]
		if last.ConditionCheck == nil || avString(last.ConditionCheck.Key, "pk") != clusterstate.ActivePK || avString(last.ConditionCheck.Key, "sk") != clusterstate.ActiveSK || last.ConditionCheck.ExpressionAttributeValues[":backend"].(*types.AttributeValueMemberS).Value != "A" || last.ConditionCheck.ExpressionAttributeValues[":epoch"].(*types.AttributeValueMemberN).Value != "7" {
			t.Fatalf("durable write transaction %d lacks active writer condition: %#v", index, tx.TransactItems)
		}
	}
	before := len(f.transactions)
	if err := s.CreateUser(context.Background(), &model.User{UserID: "u2", Email: "u2@example.com"}, "hash"); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("headerless registration = %v, want ErrFenced", err)
	}
	if len(f.transactions) != before {
		t.Fatalf("headerless registration reached DynamoDB: %d transactions", len(f.transactions)-before)
	}
}
