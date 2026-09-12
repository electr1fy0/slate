package control

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go"
)

// DDBProbe is the small DynamoDB surface needed by the verifier and ops
// replication probe. Keeping this separate from the AWS client makes all
// failure branches deterministic in tests.
type DDBProbe interface {
	Put(ctx context.Context, item map[string]dynamodbtypes.AttributeValue) error
	Get(ctx context.Context, key map[string]dynamodbtypes.AttributeValue, consistent bool) (map[string]dynamodbtypes.AttributeValue, error)
	Delete(ctx context.Context, key map[string]dynamodbtypes.AttributeValue) error
}

type Metric struct {
	Name       string
	Dimensions map[string]string
	Value      float64
}

type MetricsPublisher interface {
	Put(ctx context.Context, namespace string, metric Metric) error
}

type BatchMetricsPublisher interface {
	PutBatch(ctx context.Context, namespace string, metrics []Metric) error
}

type AWSDynamoProbe struct {
	client *dynamodb.Client
	table  string
}

func NewAWSDynamoProbe(client *dynamodb.Client, table string) *AWSDynamoProbe {
	return &AWSDynamoProbe{client: client, table: table}
}

func (d *AWSDynamoProbe) Put(ctx context.Context, item map[string]dynamodbtypes.AttributeValue) error {
	_, err := d.client.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(d.table), Item: item})
	return err
}

func (d *AWSDynamoProbe) Get(ctx context.Context, key map[string]dynamodbtypes.AttributeValue, consistent bool) (map[string]dynamodbtypes.AttributeValue, error) {
	out, err := d.client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(d.table),
		Key:            key,
		ConsistentRead: aws.Bool(consistent),
	})
	if err != nil {
		return nil, err
	}
	return out.Item, nil
}

func (d *AWSDynamoProbe) Delete(ctx context.Context, key map[string]dynamodbtypes.AttributeValue) error {
	_, err := d.client.DeleteItem(ctx, &dynamodb.DeleteItemInput{TableName: aws.String(d.table), Key: key})
	return err
}

type AWSCloudWatchMetrics struct{ client *cloudwatch.Client }

func NewAWSCloudWatchMetrics(client *cloudwatch.Client) *AWSCloudWatchMetrics {
	return &AWSCloudWatchMetrics{client: client}
}

func (m *AWSCloudWatchMetrics) Put(ctx context.Context, namespace string, metric Metric) error {
	return m.PutBatch(ctx, namespace, []Metric{metric})
}

func (m *AWSCloudWatchMetrics) PutBatch(ctx context.Context, namespace string, metrics []Metric) error {
	metricData := make([]types.MetricDatum, 0, len(metrics))
	for _, metric := range metrics {
		dimensions := make([]types.Dimension, 0, len(metric.Dimensions))
		for name, value := range metric.Dimensions {
			dimensions = append(dimensions, types.Dimension{Name: aws.String(name), Value: aws.String(value)})
		}
		metricData = append(metricData, types.MetricDatum{
			MetricName: aws.String(metric.Name),
			Dimensions: dimensions,
			Value:      aws.Float64(metric.Value),
		})
	}
	_, err := m.client.PutMetricData(ctx, &cloudwatch.PutMetricDataInput{
		Namespace:  aws.String(namespace),
		MetricData: metricData,
	})
	return err
}

type CommandInvocation struct {
	Status         string
	StandardError  string
	StandardOutput string
}

type CommandClient interface {
	Send(ctx context.Context, instanceID, command, comment string) (string, error)
	Invocation(ctx context.Context, commandID, instanceID string) (CommandInvocation, error)
}

type AWSCommandClient struct{ client *ssm.Client }

func NewAWSCommandClient(client *ssm.Client) *AWSCommandClient {
	return &AWSCommandClient{client: client}
}

func (c *AWSCommandClient) Send(ctx context.Context, instanceID, command, comment string) (string, error) {
	document := "AWS-RunShellScript"
	out, err := c.client.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{instanceID},
		DocumentName: aws.String(document),
		Comment:      aws.String(comment),
		Parameters:   map[string][]string{"commands": {command}},
	})
	if err != nil {
		return "", err
	}
	if out.Command == nil || out.Command.CommandId == nil || *out.Command.CommandId == "" {
		return "", fmt.Errorf("ssm send_command returned no command id")
	}
	return *out.Command.CommandId, nil
}

func (c *AWSCommandClient) Invocation(ctx context.Context, commandID, instanceID string) (CommandInvocation, error) {
	out, err := c.client.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
		CommandId:  aws.String(commandID),
		InstanceId: aws.String(instanceID),
	})
	if err != nil {
		return CommandInvocation{}, err
	}
	return CommandInvocation{
		Status:         string(out.Status),
		StandardError:  aws.ToString(out.StandardErrorContent),
		StandardOutput: aws.ToString(out.StandardOutputContent),
	}, nil
}

type HealthCheckClient interface {
	Update(ctx context.Context, healthCheckID string, inverted bool) (storedInverted bool, err error)
}

type AWSHealthCheckClient struct{ client *route53.Client }

func NewAWSHealthCheckClient(client *route53.Client) *AWSHealthCheckClient {
	return &AWSHealthCheckClient{client: client}
}

func (c *AWSHealthCheckClient) Update(ctx context.Context, healthCheckID string, inverted bool) (bool, error) {
	out, err := c.client.UpdateHealthCheck(ctx, &route53.UpdateHealthCheckInput{
		HealthCheckId: aws.String(healthCheckID),
		Inverted:      aws.Bool(inverted),
	})
	if err != nil {
		return false, err
	}
	if out.HealthCheck == nil || out.HealthCheck.HealthCheckConfig == nil || out.HealthCheck.HealthCheckConfig.Inverted == nil {
		return inverted, nil
	}
	return *out.HealthCheck.HealthCheckConfig.Inverted, nil
}

// ReconcilerStore is the DynamoDB surface needed by the journal replay. The
// adapter applies the same filter and conditional version expression as the
// worker.
type ReconcilerStore interface {
	Scan(ctx context.Context, startKey map[string]dynamodbtypes.AttributeValue, cutoffMS int64) (ScanPage, error)
	GetItem(ctx context.Context, key map[string]dynamodbtypes.AttributeValue) (map[string]dynamodbtypes.AttributeValue, error)
	PutReconciled(ctx context.Context, item map[string]dynamodbtypes.AttributeValue, version int) error
}

type ScanPage struct {
	Items            []map[string]dynamodbtypes.AttributeValue
	LastEvaluatedKey map[string]dynamodbtypes.AttributeValue
}

type AWSReconcilerStore struct {
	client *dynamodb.Client
	table  string
}

func NewAWSReconcilerStore(client *dynamodb.Client, table string) *AWSReconcilerStore {
	return &AWSReconcilerStore{client: client, table: table}
}

func (d *AWSReconcilerStore) Scan(ctx context.Context, startKey map[string]dynamodbtypes.AttributeValue, cutoffMS int64) (ScanPage, error) {
	prefix := dynamodbtypes.AttributeValueMemberS{Value: "JOURNAL#"}
	cutoff := dynamodbtypes.AttributeValueMemberN{Value: strconv.FormatInt(cutoffMS, 10)}
	out, err := d.client.Scan(ctx, &dynamodb.ScanInput{
		TableName:                 aws.String(d.table),
		FilterExpression:          aws.String("begins_with(pk, :journal_prefix) AND created_at >= :cutoff"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{":journal_prefix": &prefix, ":cutoff": &cutoff},
		ExclusiveStartKey:         startKey,
	})
	if err != nil {
		return ScanPage{}, err
	}
	return ScanPage{Items: out.Items, LastEvaluatedKey: out.LastEvaluatedKey}, nil
}

func (d *AWSReconcilerStore) GetItem(ctx context.Context, key map[string]dynamodbtypes.AttributeValue) (map[string]dynamodbtypes.AttributeValue, error) {
	out, err := d.client.GetItem(ctx, &dynamodb.GetItemInput{TableName: aws.String(d.table), Key: key})
	if err != nil {
		return nil, err
	}
	return out.Item, nil
}

func (d *AWSReconcilerStore) PutReconciled(ctx context.Context, item map[string]dynamodbtypes.AttributeValue, version int) error {
	versionValue := dynamodbtypes.AttributeValueMemberN{Value: strconv.Itoa(version)}
	_, err := d.client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName:                 aws.String(d.table),
		Item:                      item,
		ConditionExpression:       aws.String("attribute_not_exists(pk) OR version < :v"),
		ExpressionAttributeValues: map[string]dynamodbtypes.AttributeValue{":v": &versionValue},
	})
	if err == nil {
		return nil
	}
	if isConditionalCheckFailed(err) {
		return ErrConditionalCheckFailed
	}
	return err
}

// ErrConditionalCheckFailed is returned when another writer won the
// reconciler's conditional put race.
var ErrConditionalCheckFailed = errors.New("conditional check failed")

func isConditionalCheckFailed(err error) bool {
	if err == nil {
		return false
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		return apiErr.ErrorCode() == "ConditionalCheckFailedException"
	}
	return false
}
