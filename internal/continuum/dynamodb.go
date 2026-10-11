package continuum

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/clusterstate"
)

// DynamoAPI is the deliberately small DynamoDB surface needed by DDBControl.
type DynamoAPI interface {
	GetItem(context.Context, *dynamodb.GetItemInput, ...func(*dynamodb.Options)) (*dynamodb.GetItemOutput, error)
	PutItem(context.Context, *dynamodb.PutItemInput, ...func(*dynamodb.Options)) (*dynamodb.PutItemOutput, error)
	UpdateItem(context.Context, *dynamodb.UpdateItemInput, ...func(*dynamodb.Options)) (*dynamodb.UpdateItemOutput, error)
}

const (
	lockOwnerAttribute   = "incident_owner"
	lockExpiresAttribute = "incident_expires_at"
)

// DDBControl stores the routing pointer in the shared DynamoDB table. Lock
// expiry is an explicit numeric timestamp in milliseconds; TTL deletion is
// deliberately not part of lock correctness.
type DDBControl struct {
	Client    DynamoAPI
	TableName string
	Now       func() time.Time
}

func NewDDBControl(client DynamoAPI, tableName string) *DDBControl {
	return &DDBControl{Client: client, TableName: tableName, Now: time.Now}
}

// Ensure creates the initial route pointer once. Existing control state is
// never overwritten during startup.
func (c *DDBControl) Ensure(ctx context.Context, initial ControlState) error {
	if !validBackend(initial.ActiveBackend) {
		return ErrInvalidBackend
	}
	if initial.Epoch < 1 {
		initial.Epoch = 1
	}
	_, err := c.Client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(c.TableName),
		Item: map[string]types.AttributeValue{
			"pk":             &types.AttributeValueMemberS{Value: clusterstate.ActivePK},
			"sk":             &types.AttributeValueMemberS{Value: clusterstate.ActiveSK},
			"active_backend": &types.AttributeValueMemberS{Value: initial.ActiveBackend},
			"epoch":          &types.AttributeValueMemberN{Value: strconv.FormatInt(initial.Epoch, 10)},
			"paused":         &types.AttributeValueMemberBOOL{Value: initial.Paused},
		},
		ConditionExpression: aws.String("attribute_not_exists(pk)"),
	})
	if isConditionalFailure(err) {
		return nil
	}
	return err
}

func (c *DDBControl) Get(ctx context.Context) (ControlState, error) {
	out, err := c.Client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(c.TableName),
		Key:            controlKey(),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return ControlState{}, err
	}
	if len(out.Item) == 0 {
		return ControlState{}, ErrControlNotFound
	}
	state, err := decodeControlState(out.Item)
	if err != nil {
		return ControlState{}, err
	}
	if !validBackend(state.ActiveBackend) || state.Epoch < 1 {
		return ControlState{}, fmt.Errorf("invalid continuum control record")
	}
	return state, nil
}

// WithFence performs a strongly consistent preflight before the callback.
// Mutable DynamoDB application writes must additionally include
// clusterstate.FenceConditionCheck in their TransactWriteItems call; that
// transaction condition is what closes the promotion race.
func (c *DDBControl) WithFence(ctx context.Context, writer clusterstate.Writer, commit func() error) error {
	state, err := c.Get(ctx)
	if err != nil {
		return err
	}
	if state.Paused || state.ActiveBackend != writer.Backend || state.Epoch != writer.Epoch {
		return clusterstate.ErrFenced
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return commit()
}

func (c *DDBControl) AcquireIncident(ctx context.Context, owner string, lease time.Duration) (bool, error) {
	if owner == "" || lease <= 0 {
		return false, fmt.Errorf("owner and positive lease are required")
	}
	nowMS := c.timeNow().UnixMilli()
	_, err := c.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(c.TableName),
		Key:                 controlKey(),
		UpdateExpression:    aws.String("SET #owner = :owner, #expires = :expires"),
		ConditionExpression: aws.String("attribute_not_exists(#owner) OR #expires <= :now OR #owner = :owner"),
		ExpressionAttributeNames: map[string]string{
			"#owner":   lockOwnerAttribute,
			"#expires": lockExpiresAttribute,
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":owner":   &types.AttributeValueMemberS{Value: owner},
			":expires": &types.AttributeValueMemberN{Value: strconv.FormatInt(nowMS+lease.Milliseconds(), 10)},
			":now":     &types.AttributeValueMemberN{Value: strconv.FormatInt(nowMS, 10)},
		},
	})
	if isConditionalFailure(err) {
		return false, nil
	}
	return err == nil, err
}

func (c *DDBControl) ReleaseIncident(ctx context.Context, owner string) error {
	_, err := c.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:                 aws.String(c.TableName),
		Key:                       controlKey(),
		UpdateExpression:          aws.String("REMOVE #owner, #expires"),
		ConditionExpression:       aws.String("#owner = :owner"),
		ExpressionAttributeNames:  map[string]string{"#owner": lockOwnerAttribute, "#expires": lockExpiresAttribute},
		ExpressionAttributeValues: map[string]types.AttributeValue{":owner": &types.AttributeValueMemberS{Value: owner}},
	})
	if isConditionalFailure(err) {
		return nil
	}
	return err
}

// Promote conditionally changes the route and increments epoch. The same
// method is used for rollback, which therefore receives a newer epoch instead
// of restoring a stale number.
func (c *DDBControl) Promote(ctx context.Context, expected clusterstate.Writer, target, owner string) (ControlState, error) {
	if !validBackend(target) {
		return ControlState{}, ErrInvalidBackend
	}
	if expected.Epoch < 1 || owner == "" {
		return ControlState{}, fmt.Errorf("expected writer and incident owner are required")
	}
	nowMS := c.timeNow().UnixMilli()
	out, err := c.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:           aws.String(c.TableName),
		Key:                 controlKey(),
		UpdateExpression:    aws.String("SET #backend = :target, #epoch = #epoch + :one"),
		ConditionExpression: aws.String("#backend = :expected_backend AND #epoch = :expected_epoch AND #owner = :owner AND #expires > :now AND #paused = :not_paused AND #epoch < :max_epoch"),
		ExpressionAttributeNames: map[string]string{
			"#backend": "active_backend", "#epoch": "epoch", "#owner": lockOwnerAttribute,
			"#expires": lockExpiresAttribute, "#paused": "paused",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":target":           &types.AttributeValueMemberS{Value: target},
			":expected_backend": &types.AttributeValueMemberS{Value: expected.Backend},
			":expected_epoch":   &types.AttributeValueMemberN{Value: strconv.FormatInt(expected.Epoch, 10)},
			":owner":            &types.AttributeValueMemberS{Value: owner},
			":now":              &types.AttributeValueMemberN{Value: strconv.FormatInt(nowMS, 10)},
			":not_paused":       &types.AttributeValueMemberBOOL{Value: false},
			":one":              &types.AttributeValueMemberN{Value: "1"},
			":max_epoch":        &types.AttributeValueMemberN{Value: strconv.FormatInt(int64(^uint64(0)>>1), 10)},
		},
		ReturnValues: types.ReturnValueAllNew,
	})
	if err == nil {
		return decodeControlState(out.Attributes)
	}
	if isConditionalFailure(err) {
		current, readErr := c.Get(ctx)
		if readErr != nil {
			return ControlState{}, readErr
		}
		// A client retry after a committed promotion whose response was lost is
		// safe while the same unexpired incident lease still owns the record.
		if current.ActiveBackend == target && current.Epoch == expected.Epoch+1 && c.leaseOwned(ctx, owner) {
			return current, nil
		}
		if current.Paused {
			return current, ErrControlPaused
		}
		if current.ActiveBackend != expected.Backend || current.Epoch != expected.Epoch {
			return current, ErrStalePromotion
		}
		return current, ErrIncidentLocked
	}
	return ControlState{}, err
}

func (c *DDBControl) SetPaused(ctx context.Context, paused bool) error {
	for attempt := 0; attempt < 3; attempt++ {
		current, err := c.Get(ctx)
		if err != nil {
			return err
		}
		if current.Paused == paused {
			return nil
		}
		if current.Epoch == int64(^uint64(0)>>1) {
			return fmt.Errorf("epoch exhausted")
		}
		_, err = c.Client.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: aws.String(c.TableName), Key: controlKey(),
			UpdateExpression:    aws.String("SET #paused = :paused, #epoch = #epoch + :one"),
			ConditionExpression: aws.String("#backend = :backend AND #epoch = :epoch AND #paused = :previous_paused AND #epoch < :max_epoch"),
			ExpressionAttributeNames: map[string]string{
				"#backend": "active_backend", "#epoch": "epoch", "#paused": "paused",
			},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":backend":         &types.AttributeValueMemberS{Value: current.ActiveBackend},
				":epoch":           &types.AttributeValueMemberN{Value: strconv.FormatInt(current.Epoch, 10)},
				":previous_paused": &types.AttributeValueMemberBOOL{Value: current.Paused},
				":paused":          &types.AttributeValueMemberBOOL{Value: paused},
				":one":             &types.AttributeValueMemberN{Value: "1"},
				":max_epoch":       &types.AttributeValueMemberN{Value: strconv.FormatInt(int64(^uint64(0)>>1), 10)},
			},
		})
		if err == nil {
			return nil
		}
		if !isConditionalFailure(err) {
			return err
		}
	}
	return ErrStalePromotion
}

func (c *DDBControl) leaseOwned(ctx context.Context, owner string) bool {
	out, err := c.Client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(c.TableName), Key: controlKey(), ConsistentRead: aws.Bool(true), ProjectionExpression: aws.String("#owner, #expires"), ExpressionAttributeNames: map[string]string{"#owner": lockOwnerAttribute, "#expires": lockExpiresAttribute}})
	if err != nil || len(out.Item) == 0 {
		return false
	}
	lock, ok := out.Item[lockOwnerAttribute].(*types.AttributeValueMemberS)
	if !ok || lock.Value != owner {
		return false
	}
	expires, err := number(out.Item[lockExpiresAttribute])
	return err == nil && expires > c.timeNow().UnixMilli()
}

func controlKey() map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"pk": &types.AttributeValueMemberS{Value: clusterstate.ActivePK},
		"sk": &types.AttributeValueMemberS{Value: clusterstate.ActiveSK},
	}
}

func decodeControlState(item map[string]types.AttributeValue) (ControlState, error) {
	backend, ok := item["active_backend"].(*types.AttributeValueMemberS)
	if !ok {
		return ControlState{}, fmt.Errorf("control record missing active_backend")
	}
	epoch, err := number(item["epoch"])
	if err != nil {
		return ControlState{}, fmt.Errorf("control record epoch: %w", err)
	}
	paused := false
	if value, ok := item["paused"].(*types.AttributeValueMemberBOOL); ok {
		paused = value.Value
	} else if value, exists := item["paused"]; exists {
		return ControlState{}, fmt.Errorf("control record paused must be a boolean (%T)", value)
	}
	return ControlState{ActiveBackend: backend.Value, Epoch: epoch, Paused: paused}, nil
}

func number(value types.AttributeValue) (int64, error) {
	n, ok := value.(*types.AttributeValueMemberN)
	if !ok || n == nil {
		return 0, fmt.Errorf("expected numeric attribute, got %T", value)
	}
	return strconv.ParseInt(n.Value, 10, 64)
}

func isConditionalFailure(err error) bool {
	if err == nil {
		return false
	}
	var conditional *types.ConditionalCheckFailedException
	if errors.As(err, &conditional) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "conditionalcheckfailed")
}

func (c *DDBControl) timeNow() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}
