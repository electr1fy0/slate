package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

type dynamoAPI interface {
	Scan(context.Context, *dynamodb.ScanInput, ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	TransactWriteItems(context.Context, *dynamodb.TransactWriteItemsInput, ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error)
}

type summary struct {
	Scanned          int               `json:"scanned"`
	Processed        int               `json:"processed"`
	AlreadyProcessed int               `json:"already_processed"`
	Conflicts        []string          `json:"conflicts"`
	More             bool              `json:"more"`
	Cursor           map[string]string `json:"cursor,omitempty"`
}

type event struct {
	Cursor map[string]string `json:"cursor,omitempty"`
}

type reconciler struct {
	client dynamoAPI
	table  string
	now    func() time.Time
}

func textAV(item map[string]types.AttributeValue, name string) string {
	v, ok := item[name].(*types.AttributeValueMemberS)
	if !ok {
		return ""
	}
	return v.Value
}

func conditionalOnly(err error) bool {
	var canceled *types.TransactionCanceledException
	if !errors.As(err, &canceled) {
		return false
	}
	conditional := false
	for _, reason := range canceled.CancellationReasons {
		if aws.ToString(reason.Code) == "ConditionalCheckFailed" {
			conditional = true
		} else if reason.Code != nil && aws.ToString(reason.Code) != "None" {
			return false
		}
	}
	return conditional
}

func (r reconciler) handle(ctx context.Context, input event) (summary, error) {
	result := summary{Conflicts: []string{}}
	var cursor map[string]types.AttributeValue
	if len(input.Cursor) > 0 {
		if len(input.Cursor) != 2 || input.Cursor["pk"] == "" || input.Cursor["sk"] == "" {
			return result, errors.New("invalid reconciliation cursor")
		}
		cursor = map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: input.Cursor["pk"]}, "sk": &types.AttributeValueMemberS{Value: input.Cursor["sk"]}}
	}
	// Bound work and return the actual scan continuation. Restarting at the
	// beginning after 1,000 evaluated records would starve later journals.
	for page := 0; page < 20; page++ {
		out, err := r.client.Scan(ctx, &dynamodb.ScanInput{
			TableName: aws.String(r.table), ExclusiveStartKey: cursor, Limit: aws.Int32(50),
			FilterExpression:          aws.String("#sk = :outbox AND (attribute_not_exists(#processed) OR #processed = :false)"),
			ExpressionAttributeNames:  map[string]string{"#sk": "sk", "#processed": "processed"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":outbox": &types.AttributeValueMemberS{Value: "OUTBOX"}, ":false": &types.AttributeValueMemberBOOL{Value: false}},
		})
		if err != nil {
			return result, err
		}
		result.Scanned += int(out.ScannedCount)
		for _, journal := range out.Items {
			id := textAV(journal, "request_id")
			pk, sk := textAV(journal, "pk"), textAV(journal, "sk")
			targetPK, targetSK := textAV(journal, "target_pk"), textAV(journal, "target_sk")
			if id == "" || pk == "" || sk == "" || targetPK == "" || targetSK == "" {
				result.Conflicts = append(result.Conflicts, "invalid journal")
				continue
			}
			auditKey := map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "AUDIT#" + id}, "sk": &types.AttributeValueMemberS{Value: "SUMMARY"}}
			audit := map[string]types.AttributeValue{"pk": auditKey["pk"], "sk": auditKey["sk"], "request_id": &types.AttributeValueMemberS{Value: id}, "target_pk": &types.AttributeValueMemberS{Value: targetPK}, "target_sk": &types.AttributeValueMemberS{Value: targetSK}, "processed_at": &types.AttributeValueMemberS{Value: r.now().UTC().Format(time.RFC3339Nano)}}
			for _, field := range []string{"score", "total"} {
				if v, ok := journal[field]; ok {
					audit[field] = v
				}
			}
			_, err = r.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
				{ConditionCheck: &types.ConditionCheck{TableName: aws.String(r.table), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: targetPK}, "sk": &types.AttributeValueMemberS{Value: targetSK}}, ConditionExpression: aws.String("request_id = :id"), ExpressionAttributeValues: map[string]types.AttributeValue{":id": &types.AttributeValueMemberS{Value: id}}}},
				{Put: &types.Put{TableName: aws.String(r.table), Item: audit, ConditionExpression: aws.String("attribute_not_exists(pk)")}},
				{Update: &types.Update{TableName: aws.String(r.table), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}, UpdateExpression: aws.String("SET #processed = :true"), ConditionExpression: aws.String("request_id = :id AND (attribute_not_exists(#processed) OR #processed = :false)"), ExpressionAttributeNames: map[string]string{"#processed": "processed"}, ExpressionAttributeValues: map[string]types.AttributeValue{":id": &types.AttributeValueMemberS{Value: id}, ":true": &types.AttributeValueMemberBOOL{Value: true}, ":false": &types.AttributeValueMemberBOOL{Value: false}}}},
			}})
			if err == nil {
				result.Processed++
				continue
			}
			// Only a known conditional conflict can represent an already
			// processed event; throttles/transaction conflicts must be retried.
			if !conditionalOnly(err) {
				return result, err
			}
			existing, e := r.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(r.table), Key: auditKey, ConsistentRead: aws.Bool(true)})
			if e != nil {
				return result, e
			}
			if textAV(existing.Item, "request_id") != id || textAV(existing.Item, "target_pk") != targetPK || textAV(existing.Item, "target_sk") != targetSK {
				result.Conflicts = append(result.Conflicts, id)
				continue
			}
			// Repair a pending journal left by an older/manual partial write.
			// Recheck both identities atomically: the read above alone cannot
			// establish that the canonical submission still matches this audit.
			identity := map[string]types.AttributeValue{":id": &types.AttributeValueMemberS{Value: id}, ":pk": &types.AttributeValueMemberS{Value: targetPK}, ":sk": &types.AttributeValueMemberS{Value: targetSK}}
			journalValues := map[string]types.AttributeValue{":id": identity[":id"], ":pk": identity[":pk"], ":sk": identity[":sk"], ":true": &types.AttributeValueMemberBOOL{Value: true}}
			_, err = r.client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
				{ConditionCheck: &types.ConditionCheck{TableName: aws.String(r.table), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: targetPK}, "sk": &types.AttributeValueMemberS{Value: targetSK}}, ConditionExpression: aws.String("request_id = :id"), ExpressionAttributeValues: map[string]types.AttributeValue{":id": identity[":id"]}}},
				{ConditionCheck: &types.ConditionCheck{TableName: aws.String(r.table), Key: auditKey, ConditionExpression: aws.String("request_id = :id AND target_pk = :pk AND target_sk = :sk"), ExpressionAttributeValues: identity}},
				{Update: &types.Update{TableName: aws.String(r.table), Key: map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: pk}, "sk": &types.AttributeValueMemberS{Value: sk}}, UpdateExpression: aws.String("SET #processed = :true"), ConditionExpression: aws.String("request_id = :id AND target_pk = :pk AND target_sk = :sk"), ExpressionAttributeNames: map[string]string{"#processed": "processed"}, ExpressionAttributeValues: journalValues}},
			}})
			if err == nil {
				result.AlreadyProcessed++
			} else if conditionalOnly(err) {
				result.Conflicts = append(result.Conflicts, id)
			} else {
				return result, err
			}
		}
		cursor = out.LastEvaluatedKey
		if len(cursor) == 0 {
			return result, nil
		}
	}
	result.More = true
	result.Cursor = map[string]string{"pk": textAV(cursor, "pk"), "sk": textAV(cursor, "sk")}
	return result, nil
}

func main() {
	table := os.Getenv("TABLE_NAME")
	if table == "" {
		panic("TABLE_NAME is required")
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		panic(fmt.Errorf("load AWS configuration: %w", err))
	}
	lambda.Start(reconciler{client: dynamodb.NewFromConfig(cfg), table: table, now: time.Now}.handle)
}
