package store

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/config"
	"github.com/ayush/slate/internal/model"
)

// recordingDynamo is deliberately tiny but stores the actual AttributeValue
// maps, making schema and transaction tests independent of AWS or DynamoDB
// Local.
type recordingDynamo struct {
	items        map[string]map[string]types.AttributeValue
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
	if err != nil || first.Status != "committed" || len(f.transactions) != 1 {
		t.Fatalf("first %#v %v", first, err)
	}
	put := f.transactions[0].TransactItems[0].Put
	if put == nil || put.ConditionExpression == nil {
		t.Fatalf("submission put must be conditional: %#v", put)
	}
	again, err := s.SubmitAttempt(ctx, "e", "student", "S", map[string]int{"0": 0}, "request-1")
	if err != nil || again.Status != "duplicate" || again.Score != 1 {
		t.Fatalf("duplicate %#v %v", again, err)
	}
	_, err = s.SubmitAttempt(ctx, "e", "student", "S", map[string]int{"0": 1}, "request-2")
	if err == nil || err != ErrSubmissionConflict {
		t.Fatalf("conflict %v", err)
	}
}
