package control

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/encoding/cbor"
)

type awsReply struct {
	status      int
	body        string
	contentType string
}

type awsTestTransport struct {
	mu      sync.Mutex
	replies []awsReply
	paths   []string
}

func (t *awsTestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.paths = append(t.paths, req.URL.Path)
	if len(t.replies) == 0 {
		t.mu.Unlock()
		return nil, errors.New("unexpected AWS request")
	}
	reply := t.replies[0]
	t.replies = t.replies[1:]
	t.mu.Unlock()
	contentType := reply.contentType
	if contentType == "" {
		contentType = "application/x-amz-json-1.0"
	}
	return &http.Response{
		StatusCode: reply.status,
		Header:     http.Header{"Content-Type": []string{contentType}},
		Body:       io.NopCloser(strings.NewReader(reply.body)),
		Request:    req,
	}, nil
}

func awsConfig(transport *awsTestTransport) aws.Config {
	return aws.Config{
		Region:       "ap-south-1",
		BaseEndpoint: aws.String("https://aws.test"),
		Credentials:  credentials.NewStaticCredentialsProvider("access", "secret", "session"),
		HTTPClient:   &http.Client{Transport: transport},
		Retryer:      func() aws.Retryer { return aws.NopRetryer{} },
	}
}

func TestAWSAdaptersCoverage(t *testing.T) {
	ddbTransport := &awsTestTransport{replies: []awsReply{
		{status: 200, body: `{}`},
		{status: 200, body: `{"Item":{"value":{"S":"ok"}}}`},
		{status: 200, body: `{}`},
	}}
	ddbClient := dynamodb.NewFromConfig(awsConfig(ddbTransport))
	db := NewAWSDynamoProbe(ddbClient, "table")
	if db.table != "table" || db.client != ddbClient {
		t.Fatal("DynamoDB adapter was not constructed")
	}
	if err := db.Put(context.Background(), map[string]dynamodbtypes.AttributeValue{"pk": &dynamodbtypes.AttributeValueMemberS{Value: "p"}}); err != nil {
		t.Fatal(err)
	}
	item, err := db.Get(context.Background(), map[string]dynamodbtypes.AttributeValue{"pk": &dynamodbtypes.AttributeValueMemberS{Value: "p"}}, true)
	if err != nil || item["value"].(*dynamodbtypes.AttributeValueMemberS).Value != "ok" {
		t.Fatalf("DynamoDB get = %#v %v", item, err)
	}
	if err := db.Delete(context.Background(), map[string]dynamodbtypes.AttributeValue{"pk": &dynamodbtypes.AttributeValueMemberS{Value: "p"}}); err != nil {
		t.Fatal(err)
	}

	for _, call := range []func(*AWSDynamoProbe) error{
		func(adapter *AWSDynamoProbe) error { return adapter.Put(context.Background(), nil) },
		func(adapter *AWSDynamoProbe) error {
			_, err := adapter.Get(context.Background(), nil, false)
			return err
		},
		func(adapter *AWSDynamoProbe) error { return adapter.Delete(context.Background(), nil) },
	} {
		transport := &awsTestTransport{replies: []awsReply{{status: 500, body: `{}`}}}
		if err := call(NewAWSDynamoProbe(dynamodb.NewFromConfig(awsConfig(transport)), "table")); err == nil {
			t.Fatal("DynamoDB error response was ignored")
		}
	}

	metricsTransport := &awsTestTransport{replies: []awsReply{
		{status: 200, body: string(cbor.Encode(cbor.Map{})), contentType: "application/cbor"},
		{status: 500, body: string(cbor.Encode(cbor.Map{})), contentType: "application/cbor"},
	}}
	metricsClient := cloudwatch.NewFromConfig(awsConfig(metricsTransport))
	metrics := NewAWSCloudWatchMetrics(metricsClient)
	if metrics.client != metricsClient {
		t.Fatal("CloudWatch adapter was not constructed")
	}
	metric := Metric{Name: "Health", Dimensions: map[string]string{"Role": "primary"}, Value: 1}
	if err := metrics.Put(context.Background(), "Ops", metric); err != nil {
		t.Fatal(err)
	}
	if err := metrics.PutBatch(context.Background(), "Ops", []Metric{}); err == nil {
		t.Fatal("CloudWatch error response was ignored")
	}

	commandTransport := &awsTestTransport{replies: []awsReply{
		{status: 200, body: `{"Command":{"CommandId":"command-1"}}`},
		{status: 200, body: `{"Status":"Success","StandardErrorContent":"stderr","StandardOutputContent":"stdout"}`},
		{status: 200, body: `{"Command":{}}`},
		{status: 500, body: `{}`},
		{status: 500, body: `{}`},
	}}
	commandClient := ssm.NewFromConfig(awsConfig(commandTransport))
	commands := NewAWSCommandClient(commandClient)
	if commands.client != commandClient {
		t.Fatal("SSM adapter was not constructed")
	}
	commandID, err := commands.Send(context.Background(), "i-1", "echo ok", "comment")
	if err != nil || commandID != "command-1" {
		t.Fatalf("SSM send = %q %v", commandID, err)
	}
	invocation, err := commands.Invocation(context.Background(), commandID, "i-1")
	if err != nil || invocation.Status != "Success" || invocation.StandardOutput != "stdout" {
		t.Fatalf("SSM invocation = %#v %v", invocation, err)
	}
	if _, err := commands.Send(context.Background(), "i-1", "echo", "comment"); err == nil {
		t.Fatal("missing SSM command id accepted")
	}
	if _, err := commands.Send(context.Background(), "i-1", "echo", "comment"); err == nil {
		t.Fatal("SSM send error ignored")
	}
	if _, err := commands.Invocation(context.Background(), commandID, "i-1"); err == nil {
		t.Fatal("SSM invocation error ignored")
	}

	goodRoute := `<UpdateHealthCheckResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><HealthCheck><HealthCheckConfig><Inverted>true</Inverted></HealthCheckConfig></HealthCheck></UpdateHealthCheckResponse>`
	routeTransport := &awsTestTransport{replies: []awsReply{
		{status: 200, body: goodRoute, contentType: "text/xml"},
		{status: 200, body: `<UpdateHealthCheckResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"></UpdateHealthCheckResponse>`, contentType: "text/xml"},
		{status: 500, body: `{}`, contentType: "text/xml"},
	}}
	routeClient := route53.NewFromConfig(awsConfig(routeTransport))
	route := NewAWSHealthCheckClient(routeClient)
	if route.client != routeClient {
		t.Fatal("Route53 adapter was not constructed")
	}
	if stored, err := route.Update(context.Background(), "health", true); err != nil || !stored {
		t.Fatalf("Route53 update = %v %v", stored, err)
	}
	if stored, err := route.Update(context.Background(), "health", false); err != nil || stored {
		t.Fatalf("Route53 fallback update = %v %v", stored, err)
	}
	if _, err := route.Update(context.Background(), "health", true); err == nil {
		t.Fatal("Route53 error response ignored")
	}
}

func TestAWSReconcilerAdapterCoverage(t *testing.T) {
	item := `{"pk":{"S":"JOURNAL#1"}}`
	transport := &awsTestTransport{replies: []awsReply{
		{status: 200, body: `{"Items":[` + item + `],"LastEvaluatedKey":{"pk":{"S":"next"}}}`},
		{status: 200, body: `{"Item":{"pk":{"S":"target"}}}`},
		{status: 200, body: `{}`},
	}}
	client := dynamodb.NewFromConfig(awsConfig(transport))
	store := NewAWSReconcilerStore(client, "table")
	if store.table != "table" || store.client != client {
		t.Fatal("reconciler adapter was not constructed")
	}
	page, err := store.Scan(context.Background(), nil, 123)
	if err != nil || len(page.Items) != 1 || len(page.LastEvaluatedKey) != 1 {
		t.Fatalf("scan = %#v %v", page, err)
	}
	got, err := store.GetItem(context.Background(), map[string]dynamodbtypes.AttributeValue{"pk": &dynamodbtypes.AttributeValueMemberS{Value: "p"}})
	if err != nil || got["pk"] == nil {
		t.Fatalf("get item = %#v %v", got, err)
	}
	if err := store.PutReconciled(context.Background(), map[string]dynamodbtypes.AttributeValue{"pk": &dynamodbtypes.AttributeValueMemberS{Value: "p"}}, 1); err != nil {
		t.Fatal(err)
	}

	for _, operation := range []func(*AWSReconcilerStore) error{
		func(adapter *AWSReconcilerStore) error {
			_, err := adapter.Scan(context.Background(), nil, 1)
			return err
		},
		func(adapter *AWSReconcilerStore) error {
			_, err := adapter.GetItem(context.Background(), nil)
			return err
		},
		func(adapter *AWSReconcilerStore) error { return adapter.PutReconciled(context.Background(), nil, 1) },
	} {
		transport := &awsTestTransport{replies: []awsReply{{status: 500, body: `{}`}}}
		if err := operation(NewAWSReconcilerStore(dynamodb.NewFromConfig(awsConfig(transport)), "table")); err == nil {
			t.Fatal("reconciler AWS error response ignored")
		}
	}
	conditionalTransport := &awsTestTransport{replies: []awsReply{{
		status: 400,
		body:   `{"__type":"com.amazonaws.dynamodb.v20120810#ConditionalCheckFailedException","message":"race"}`,
	}}}
	if err := NewAWSReconcilerStore(dynamodb.NewFromConfig(awsConfig(conditionalTransport)), "table").PutReconciled(context.Background(), map[string]dynamodbtypes.AttributeValue{"pk": &dynamodbtypes.AttributeValueMemberS{Value: "p"}}, 2); !errors.Is(err, ErrConditionalCheckFailed) {
		t.Fatalf("conditional adapter error = %v", err)
	}

	conditional := &smithy.GenericAPIError{Code: "ConditionalCheckFailedException", Message: "race"}
	if !isConditionalCheckFailed(conditional) || isConditionalCheckFailed(errors.New("plain")) || isConditionalCheckFailed(nil) {
		t.Fatal("conditional error detection mismatch")
	}
	if !errors.Is(ErrConditionalCheckFailed, ErrConditionalCheckFailed) {
		t.Fatal("conditional error sentinel is not usable")
	}
}
