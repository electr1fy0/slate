// Package clusterstate contains the shared routing and transactional fencing
// contract. It is independent of the application store and orchestrator.
package clusterstate

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

const (
	ActivePK      = "CONTROL#ACTIVE"
	ActiveSK      = "STATE"
	EpochHeader   = "X-Continuum-Epoch"
	BackendHeader = "X-Continuum-Backend"
)

var ErrFenced = errors.New("writer is no longer the active backend")

type Writer struct {
	Backend string
	Epoch   int64
}

type writerKey struct{}

func WithWriter(ctx context.Context, writer Writer) context.Context {
	return context.WithValue(ctx, writerKey{}, writer)
}

func WriterFrom(ctx context.Context) (Writer, bool) {
	w, ok := ctx.Value(writerKey{}).(Writer)
	return w, ok
}

// FenceGuard serializes an in-memory commit with promotion. The DynamoDB
// implementation uses a ConditionCheck in the same transaction as its writes.
type FenceGuard interface {
	WithFence(context.Context, Writer, func() error) error
}

// FenceConditionCheck returns the DynamoDB transaction condition that must be
// included alongside every mutable application write. Keeping the condition
// in the same TransactWriteItems call makes a promotion atomic with respect to
// stale writers. The DynamoDB control item is intentionally not TTL-managed;
// its explicit paused flag and epoch remain authoritative.
func FenceConditionCheck(tableName string, writer Writer) types.TransactWriteItem {
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: ActivePK},
			"sk": &types.AttributeValueMemberS{Value: ActiveSK},
		},
		ConditionExpression: aws.String("#backend = :backend AND #epoch = :epoch AND #paused = :not_paused"),
		ExpressionAttributeNames: map[string]string{
			"#backend": "active_backend",
			"#epoch":   "epoch",
			"#paused":  "paused",
		},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":backend":    &types.AttributeValueMemberS{Value: writer.Backend},
			":epoch":      &types.AttributeValueMemberN{Value: fmt.Sprintf("%d", writer.Epoch)},
			":not_paused": &types.AttributeValueMemberBOOL{Value: false},
		},
	}}
}
