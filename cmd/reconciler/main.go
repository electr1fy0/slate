package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/ayush/slate/internal/control"
)

func main() {
	ctx := context.Background()
	cfg, err := control.LoadReconcilerConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatal(err)
	}
	reconciler := control.NewReconciler(
		cfg,
		control.NewAWSReconcilerStore(dynamodb.NewFromConfig(awsCfg), cfg.TableName),
		control.NewAWSCloudWatchMetrics(cloudwatch.NewFromConfig(awsCfg)),
	)
	lambda.Start(reconciler.Handle)
}
