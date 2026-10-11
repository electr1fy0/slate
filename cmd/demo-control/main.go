package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	awslambda "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
	"github.com/ayush/slate/internal/continuum"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	cfg, err := continuum.LoadCloudConfig(os.Getenv)
	if err != nil {
		return err
	}
	if cfg.AdminToken == "" {
		return fmt.Errorf("ADMIN_TOKEN is required by demo-control")
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return fmt.Errorf("load AWS configuration: %w", err)
	}
	dynamo := dynamodb.NewFromConfig(awsCfg)
	control := continuum.NewDDBControl(dynamo, cfg.TableName)
	if err := control.Ensure(ctx, continuum.ControlState{ActiveBackend: "A", Epoch: 1}); err != nil {
		return fmt.Errorf("ensure shared control state: %w", err)
	}
	cloud := continuum.NewCloudControl(cfg, control, &continuum.DDBEventStore{Client: dynamo, TableName: cfg.TableName},
		awslambda.NewFromConfig(awsCfg), sfn.NewFromConfig(awsCfg), ec2.NewFromConfig(awsCfg))
	lambda.Start(func(ctx context.Context, raw json.RawMessage) (any, error) {
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, err
		}
		if _, apiGateway := envelope["requestContext"]; apiGateway {
			var request events.APIGatewayV2HTTPRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				return nil, err
			}
			return cloud.HandleHTTPAPI(ctx, request)
		}
		var event map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&event); err != nil {
			return nil, err
		}
		return cloud.Dispatch(ctx, event)
	})
	return nil
}
