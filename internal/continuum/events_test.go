package continuum

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type eventDynamoRoundTripStub struct {
	item  map[string]types.AttributeValue
	query *dynamodb.QueryInput
}

func (s *eventDynamoRoundTripStub) PutItem(_ context.Context, input *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	s.item = make(map[string]types.AttributeValue, len(input.Item))
	for key, value := range input.Item {
		s.item[key] = value
	}
	return &dynamodb.PutItemOutput{}, nil
}

func (s *eventDynamoRoundTripStub) Query(_ context.Context, input *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	copy := *input
	s.query = &copy
	return &dynamodb.QueryOutput{Items: []map[string]types.AttributeValue{s.item}}, nil
}

func TestDDBEventStoreRoundTripsRecoveryDurationAndMetrics(t *testing.T) {
	ctx := context.Background()
	dynamo := &eventDynamoRoundTripStub{}
	store := &DDBEventStore{Client: dynamo, TableName: "continuum"}
	want := Event{
		ID: "event-recovery-1", At: "2026-10-10T12:00:00.000Z", Type: "recovered",
		Message: "route recovery took 50472 ms from the first failed monitor probe; public health smoke took 230 ms",
		Backend: "B", Epoch: 3, DurationMS: 50472,
	}
	if err := store.Append(ctx, want); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if got := dynamo.item["duration_ms"].(*types.AttributeValueMemberN).Value; got != "50472" {
		t.Fatalf("stored duration_ms = %q, want 50472", got)
	}

	got, err := store.Recent(ctx, 50)
	if err != nil {
		t.Fatalf("query events: %v", err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("round-trip events = %#v, want %#v", got, want)
	}
	if dynamo.query == nil || !*dynamo.query.ConsistentRead || *dynamo.query.Limit != 50 {
		t.Fatalf("recent query was not strong and bounded: %+v", dynamo.query)
	}
	if metrics := MetricsFromEvents(got); metrics.RecoveryMS != want.DurationMS {
		t.Fatalf("recovery metric = %d ms, want %d ms", metrics.RecoveryMS, want.DurationMS)
	}
}
