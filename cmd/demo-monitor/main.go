package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
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
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return err
	}
	dynamo := dynamodb.NewFromConfig(awsCfg)
	control := continuum.NewDDBControl(dynamo, cfg.TableName)
	monitor := continuum.NewCloudControl(cfg, control, &continuum.DDBEventStore{Client: dynamo, TableName: cfg.TableName},
		awslambda.NewFromConfig(awsCfg), sfn.NewFromConfig(awsCfg), nil)
	lambda.Start(func(ctx context.Context, _ map[string]any) (map[string]any, error) {
		result, err := monitor.MonitorIncident(ctx)
		if err != nil {
			log.Printf("continuum monitor failed: %v", err)
		} else {
			log.Printf("continuum monitor result=%v", result)
		}
		return result, err
	})
	return nil
}
