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
	if err := run(); err != nil {
		fatal(err)
	}
}

var (
	loadAWSConfig = awsconfig.LoadDefaultConfig
	startLambda   = lambda.Start
	exit          = os.Exit
	fatal         = func(err error) { log.Print(err); exit(1) }
)

func run() error {
	ctx := context.Background()
	cfg, err := control.LoadReconcilerConfig(os.Getenv)
	if err != nil {
		return err
	}
	awsCfg, err := loadAWSConfig(ctx)
	if err != nil {
		return err
	}
	reconciler := control.NewReconciler(
		cfg,
		control.NewAWSReconcilerStore(dynamodb.NewFromConfig(awsCfg), cfg.TableName),
		control.NewAWSCloudWatchMetrics(cloudwatch.NewFromConfig(awsCfg)),
	)
	startLambda(reconciler.Handle)
	return nil
}
