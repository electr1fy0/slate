package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// scriptedDynamo is a deterministic, stateful DynamoDB double. It models the
// small subset of DynamoDB behavior that the store relies on while allowing
// each operation to be scripted with errors or paginated responses.
type scriptedDynamo struct {
	items map[string]map[string]types.AttributeValue

	getErrors         []error
	putErrors         []error
	updateErrors      []error
	deleteErrors      []error
	queryErrors       []error
	scanErrors        []error
	batchErrors       []error
	transactionErrors []error
	getResponses      []map[string]types.AttributeValue

	queryPages       []*dynamodb.QueryOutput
	scanPages        []*dynamodb.ScanOutput
	batchUnprocessed [][]types.WriteRequest
	updateAttributes map[string]types.AttributeValue

	puts         []*dynamodb.PutItemInput
	updates      []*dynamodb.UpdateItemInput
	deletes      []*dynamodb.DeleteItemInput
	queries      []*dynamodb.QueryInput
	scans        []*dynamodb.ScanInput
	batches      []*dynamodb.BatchWriteItemInput
	transactions []*dynamodb.TransactWriteItemsInput
}

func newScriptedDynamo() *scriptedDynamo {
	return &scriptedDynamo{items: map[string]map[string]types.AttributeValue{}}
}

func popScriptedError(script *[]error) error {
	if len(*script) == 0 {
		return nil
	}
	err := (*script)[0]
	*script = (*script)[1:]
	return err
}

func cloneAttributeMap(in map[string]types.AttributeValue) map[string]types.AttributeValue {
	if in == nil {
		return nil
	}
	out := make(map[string]types.AttributeValue, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func (f *scriptedDynamo) GetItem(_ context.Context, in *dynamodb.GetItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error) {
	if err := popScriptedError(&f.getErrors); err != nil {
		return nil, err
	}
	if len(f.getResponses) > 0 {
		item := f.getResponses[0]
		f.getResponses = f.getResponses[1:]
		return &dynamodb.GetItemOutput{Item: cloneAttributeMap(item)}, nil
	}
	return &dynamodb.GetItemOutput{Item: cloneAttributeMap(f.items[itemKey(in.Key)])}, nil
}

func (f *scriptedDynamo) PutItem(_ context.Context, in *dynamodb.PutItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error) {
	f.puts = append(f.puts, in)
	if err := popScriptedError(&f.putErrors); err != nil {
		return nil, err
	}
	f.items[itemKey(in.Item)] = cloneAttributeMap(in.Item)
	return &dynamodb.PutItemOutput{}, nil
}

func (f *scriptedDynamo) UpdateItem(_ context.Context, in *dynamodb.UpdateItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error) {
	f.updates = append(f.updates, in)
	if err := popScriptedError(&f.updateErrors); err != nil {
		return nil, err
	}
	if f.updateAttributes != nil {
		return &dynamodb.UpdateItemOutput{Attributes: cloneAttributeMap(f.updateAttributes)}, nil
	}
	updated := cloneAttributeMap(f.items[itemKey(in.Key)])
	if updated == nil {
		updated = cloneAttributeMap(in.Key)
	}
	for alias, field := range in.ExpressionAttributeNames {
		valueName := ":" + field
		if value, ok := in.ExpressionAttributeValues[valueName]; ok {
			updated[field] = value
		}
		_ = alias
	}
	f.items[itemKey(updated)] = cloneAttributeMap(updated)
	return &dynamodb.UpdateItemOutput{Attributes: cloneAttributeMap(updated)}, nil
}

func (f *scriptedDynamo) DeleteItem(_ context.Context, in *dynamodb.DeleteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.DeleteItemOutput, error) {
	f.deletes = append(f.deletes, in)
	if err := popScriptedError(&f.deleteErrors); err != nil {
		return nil, err
	}
	delete(f.items, itemKey(in.Key))
	return &dynamodb.DeleteItemOutput{}, nil
}

func (f *scriptedDynamo) Query(_ context.Context, in *dynamodb.QueryInput, _ ...func(*dynamodb.Options)) (*dynamodb.QueryOutput, error) {
	f.queries = append(f.queries, in)
	if err := popScriptedError(&f.queryErrors); err != nil {
		return nil, err
	}
	if len(f.queryPages) > 0 {
		out := f.queryPages[0]
		f.queryPages = f.queryPages[1:]
		return out, nil
	}
	pk := ""
	if av, ok := in.ExpressionAttributeValues[":pk"].(*types.AttributeValueMemberS); ok {
		pk = av.Value
	}
	out := &dynamodb.QueryOutput{}
	for _, item := range f.items {
		if avString(item, "pk") == pk {
			out.Items = append(out.Items, cloneAttributeMap(item))
		}
	}
	return out, nil
}

func (f *scriptedDynamo) Scan(_ context.Context, in *dynamodb.ScanInput, _ ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error) {
	f.scans = append(f.scans, in)
	if err := popScriptedError(&f.scanErrors); err != nil {
		return nil, err
	}
	if len(f.scanPages) > 0 {
		out := f.scanPages[0]
		f.scanPages = f.scanPages[1:]
		return out, nil
	}
	out := &dynamodb.ScanOutput{}
	for _, item := range f.items {
		out.Items = append(out.Items, cloneAttributeMap(item))
	}
	return out, nil
}

func (f *scriptedDynamo) BatchWriteItem(_ context.Context, in *dynamodb.BatchWriteItemInput, _ ...func(*dynamodb.Options)) (*dynamodb.BatchWriteItemOutput, error) {
	f.batches = append(f.batches, in)
	if err := popScriptedError(&f.batchErrors); err != nil {
		return nil, err
	}
	tableName := "slate"
	requests := in.RequestItems[tableName]
	if requests == nil {
		for name, value := range in.RequestItems {
			tableName, requests = name, value
			break
		}
	}
	var unprocessed []types.WriteRequest
	if len(f.batchUnprocessed) > 0 {
		unprocessed = f.batchUnprocessed[0]
		f.batchUnprocessed = f.batchUnprocessed[1:]
	}
	for _, request := range requests {
		isUnprocessed := false
		for _, pending := range unprocessed {
			if requestKey(request) == requestKey(pending) {
				isUnprocessed = true
				break
			}
		}
		if isUnprocessed {
			continue
		}
		if request.DeleteRequest != nil {
			delete(f.items, itemKey(request.DeleteRequest.Key))
		}
	}
	if len(unprocessed) == 0 {
		return &dynamodb.BatchWriteItemOutput{}, nil
	}
	return &dynamodb.BatchWriteItemOutput{UnprocessedItems: map[string][]types.WriteRequest{tableName: unprocessed}}, nil
}

func requestKey(request types.WriteRequest) string {
	if request.DeleteRequest == nil {
		return ""
	}
	return itemKey(request.DeleteRequest.Key)
}

func (f *scriptedDynamo) TransactWriteItems(_ context.Context, in *dynamodb.TransactWriteItemsInput, _ ...func(*dynamodb.Options)) (*dynamodb.TransactWriteItemsOutput, error) {
	f.transactions = append(f.transactions, in)
	if err := popScriptedError(&f.transactionErrors); err != nil {
		return nil, err
	}
	// Preserve operation-level failure scripting for transactions too. Validate
	// every item before applying any of them, matching DynamoDB's atomicity.
	for _, tx := range in.TransactItems {
		switch {
		case tx.Put != nil:
			if err := popScriptedError(&f.putErrors); err != nil {
				return nil, err
			}
		case tx.Update != nil:
			if err := popScriptedError(&f.updateErrors); err != nil {
				return nil, err
			}
		case tx.Delete != nil:
			if err := popScriptedError(&f.deleteErrors); err != nil {
				return nil, err
			}
		}
	}
	for _, tx := range in.TransactItems {
		switch {
		case tx.Put != nil:
			f.items[itemKey(tx.Put.Item)] = cloneAttributeMap(tx.Put.Item)
		case tx.Delete != nil:
			delete(f.items, itemKey(tx.Delete.Key))
		case tx.Update != nil:
			updated := cloneAttributeMap(f.items[itemKey(tx.Update.Key)])
			if updated == nil {
				updated = cloneAttributeMap(tx.Update.Key)
			}
			for field, value := range tx.Update.ExpressionAttributeValues {
				name := strings.TrimPrefix(field, ":")
				if alias, ok := tx.Update.ExpressionAttributeNames["#"+name]; ok {
					name = alias
				}
				updated[name] = value
			}
			f.items[itemKey(updated)] = updated
		}
	}
	return &dynamodb.TransactWriteItemsOutput{}, nil
}

func scriptedAttributeMap(t any) map[string]types.AttributeValue {
	item, err := marshal(t)
	if err != nil {
		panic(fmt.Sprintf("marshal scripted item: %v", err))
	}
	return item
}

var (
	_ DynamoAPI = (*scriptedDynamo)(nil)
)
