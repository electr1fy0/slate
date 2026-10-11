package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type fakeDynamo struct {
	scan  func(*dynamodb.ScanInput) (*dynamodb.ScanOutput, error)
	get   func(*dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error)
	write func(*dynamodb.TransactWriteItemsInput) error
}

func (f fakeDynamo) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	return f.scan(in)
}
func (f fakeDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	return f.get(in)
}
func (f fakeDynamo) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	return &dynamodb.TransactWriteItemsOutput{}, f.write(in)
}
func attributes(values map[string]string) map[string]types.AttributeValue {
	out := map[string]types.AttributeValue{}
	for k, v := range values {
		out[k] = &types.AttributeValueMemberS{Value: v}
	}
	return out
}
func journal() map[string]types.AttributeValue {
	return attributes(map[string]string{"pk": "JOURNAL#request-1", "sk": "OUTBOX", "request_id": "request-1", "target_pk": "SUBMISSION#exam-1", "target_sk": "STUDENT#student-1", "answers": "sensitive"})
}
func oneJournal(*dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
	return &dynamodb.ScanOutput{Items: []map[string]types.AttributeValue{journal()}, ScannedCount: 1}, nil
}
func fixture(client fakeDynamo) reconciler {
	return reconciler{client: client, table: "demo", now: func() time.Time { return time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC) }}
}

func TestReconcileAtomicallyChecksCanonicalAndCompletesOutbox(t *testing.T) {
	calls := 0
	r := fixture(fakeDynamo{scan: oneJournal, write: func(in *dynamodb.TransactWriteItemsInput) error {
		calls++
		if len(in.TransactItems) != 3 {
			t.Fatalf("wanted one atomic check/audit/mark transaction: %+v", in)
		}
		check, put, update := in.TransactItems[0].ConditionCheck, in.TransactItems[1].Put, in.TransactItems[2].Update
		if check == nil || put == nil || update == nil {
			t.Fatal("missing atomic operation")
		}
		if textAV(check.Key, "pk") != "SUBMISSION#exam-1" || aws.ToString(check.ConditionExpression) != "request_id = :id" {
			t.Fatal("must verify durable canonical submission")
		}
		if _, exists := put.Item["answers"]; exists {
			t.Fatal("summary must not copy exam answers")
		}
		if textAV(put.Item, "pk") != "AUDIT#request-1" || aws.ToString(update.UpdateExpression) != "SET #processed = :true" {
			t.Fatal("incorrect audit/mark")
		}
		return nil
	}})
	got, err := r.handle(context.Background(), event{})
	if err != nil || got.Processed != 1 || got.More || len(got.Conflicts) != 0 || calls != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestConditionalReplayRequiresMatchingDurableAudit(t *testing.T) {
	for _, scenario := range []string{"repair-pending", "missing-audit", "wrong-sort-key", "canonical-changed"} {
		t.Run(scenario, func(t *testing.T) {
			writes := 0
			client := fakeDynamo{scan: oneJournal, write: func(in *dynamodb.TransactWriteItemsInput) error {
				writes++
				if writes == 2 {
					if len(in.TransactItems) != 3 || in.TransactItems[0].ConditionCheck == nil || in.TransactItems[1].ConditionCheck == nil || in.TransactItems[2].Update == nil {
						t.Fatal("repair must atomically verify canonical, audit and journal identities")
					}
					if textAV(in.TransactItems[1].ConditionCheck.ExpressionAttributeValues, ":sk") != "STUDENT#student-1" || aws.ToString(in.TransactItems[2].Update.ConditionExpression) != "request_id = :id AND target_pk = :pk AND target_sk = :sk" {
						t.Fatal("repair did not bind the entire target key")
					}
					if scenario == "repair-pending" {
						return nil
					}
				}
				return &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}}}
			}, get: func(in *dynamodb.GetItemInput) (*dynamodb.GetItemOutput, error) {
				if !aws.ToBool(in.ConsistentRead) {
					t.Fatal("duplicate check must be strongly consistent")
				}
				item := map[string]types.AttributeValue{}
				if scenario != "missing-audit" {
					item = attributes(map[string]string{"request_id": "request-1", "target_pk": "SUBMISSION#exam-1", "target_sk": "STUDENT#student-1"})
					if scenario == "wrong-sort-key" {
						item["target_sk"] = &types.AttributeValueMemberS{Value: "STUDENT#different"}
					}
				}
				return &dynamodb.GetItemOutput{Item: item}, nil
			}}
			got, err := fixture(client).handle(context.Background(), event{})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "repair-pending" && (got.AlreadyProcessed != 1 || writes != 2) {
				t.Fatal(got)
			}
			if scenario != "repair-pending" && (len(got.Conflicts) != 1 || got.AlreadyProcessed != 0) {
				t.Fatal(got)
			}
			if (scenario == "missing-audit" || scenario == "wrong-sort-key") && writes != 1 {
				t.Fatal("nonmatching audit must not authorize journal repair")
			}
		})
	}
}

func TestThrottleAndScanErrorsRemainRetryable(t *testing.T) {
	boom := errors.New("dependency unavailable")
	_, err := fixture(fakeDynamo{scan: func(*dynamodb.ScanInput) (*dynamodb.ScanOutput, error) { return nil, boom }}).handle(context.Background(), event{})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	throttle := &types.TransactionCanceledException{CancellationReasons: []types.CancellationReason{{Code: aws.String("ConditionalCheckFailed")}, {Code: aws.String("ThrottlingError")}}}
	_, err = fixture(fakeDynamo{scan: oneJournal, write: func(*dynamodb.TransactWriteItemsInput) error { return throttle }}).handle(context.Background(), event{})
	if !errors.Is(err, throttle) {
		t.Fatalf("must not swallow throttle as duplicate: %v", err)
	}
}

func TestContinuationReachesWorkBeyondInvocationLimit(t *testing.T) {
	pages := 0
	client := fakeDynamo{scan: func(in *dynamodb.ScanInput) (*dynamodb.ScanOutput, error) {
		pages++
		if pages == 21 {
			if textAV(in.ExclusiveStartKey, "pk") != "page-20" {
				t.Fatal("continuation was lost")
			}
			return &dynamodb.ScanOutput{}, nil
		}
		return &dynamodb.ScanOutput{ScannedCount: 50, LastEvaluatedKey: attributes(map[string]string{"pk": "page-20", "sk": "STATE"})}, nil
	}}
	r := fixture(client)
	first, err := r.handle(context.Background(), event{})
	if err != nil || !first.More || first.Scanned != 1000 {
		t.Fatalf("%+v, %v", first, err)
	}
	second, err := r.handle(context.Background(), event{Cursor: first.Cursor})
	if err != nil || second.More || pages != 21 {
		t.Fatalf("%+v, %v", second, err)
	}
	if _, err = r.handle(context.Background(), event{Cursor: map[string]string{"pk": "missing-sort"}}); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}
