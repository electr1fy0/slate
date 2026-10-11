package continuum

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const eventPK = "CONTROL#EVENTS"

type EventStore interface {
	Append(context.Context, Event) error
	Recent(context.Context, int32) ([]Event, error)
}

type eventDynamoAPI interface {
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	Query(context.Context, *dynamodb.QueryInput, ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error)
}

// DDBEventStore keeps a small, bounded audit trail in the existing regional
// table. The event partition has no secondary-index or cross-region needs.
type DDBEventStore struct {
	Client    eventDynamoAPI
	TableName string
	Now       func() time.Time
}

func (s *DDBEventStore) Append(ctx context.Context, event Event) error {
	if event.At == "" {
		event.At = s.timeNow().UTC().Format(time.RFC3339Nano)
	}
	if event.ID == "" {
		event.ID = "event-" + randomIDString()
	}
	when := s.timeNow()
	if parsed, err := time.Parse(time.RFC3339Nano, event.At); err == nil {
		when = parsed
	}
	item, err := attributevalue.MarshalMap(struct {
		PK         string `dynamodbav:"pk"`
		SK         string `dynamodbav:"sk"`
		ID         string `dynamodbav:"id"`
		At         string `dynamodbav:"at"`
		Type       string `dynamodbav:"type"`
		Message    string `dynamodbav:"message"`
		Backend    string `dynamodbav:"backend,omitempty"`
		Epoch      int64  `dynamodbav:"epoch,omitempty"`
		DurationMS int64  `dynamodbav:"duration_ms,omitempty"`
		TTL        int64  `dynamodbav:"ttl"`
	}{PK: eventPK, SK: fmt.Sprintf("AT#%013d#%s", when.UnixMilli(), event.ID), ID: event.ID, At: event.At,
		Type: event.Type, Message: event.Message, Backend: event.Backend, Epoch: event.Epoch,
		DurationMS: event.DurationMS, TTL: when.Add(7 * 24 * time.Hour).Unix()})
	if err != nil {
		return err
	}
	_, err = s.Client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.TableName), Item: item, ConditionExpression: aws.String("attribute_not_exists(pk) AND attribute_not_exists(sk)")})
	return err
}

func (s *DDBEventStore) Recent(ctx context.Context, limit int32) ([]Event, error) {
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	out, err := s.Client.Query(ctx, &dynamodb.QueryInput{
		TableName:                 aws.String(s.TableName),
		KeyConditionExpression:    aws.String("#pk = :pk"),
		ExpressionAttributeNames:  map[string]string{"#pk": "pk"},
		ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: eventPK}},
		Limit:                     aws.Int32(limit), ScanIndexForward: aws.Bool(false), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(out.Items))
	for _, item := range out.Items {
		var event Event
		if err := attributevalue.UnmarshalMap(item, &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, nil
}

func MetricsFromEvents(events []Event) Metrics {
	var metrics Metrics
	recoverySet := false
	for _, event := range events {
		switch event.Type {
		case "failover":
			metrics.Failovers++
		case "suppressed":
			metrics.Suppressed++
		case "recovered":
			if !recoverySet && event.DurationMS > 0 {
				metrics.RecoveryMS = event.DurationMS
				recoverySet = true
			}
		case "reconcile":
			if metrics.Reconciled == nil {
				value := true
				metrics.Reconciled = &value
			}
		case "reconcile_failed", "reconcile_conflict":
			if metrics.Reconciled == nil {
				value := false
				metrics.Reconciled = &value
			}
		}
	}
	return metrics
}

func (s *DDBEventStore) timeNow() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
