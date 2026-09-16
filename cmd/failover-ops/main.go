package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
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
	cfg, err := control.LoadFailoverOpsConfig(os.Getenv)
	if err != nil {
		return err
	}
	awsCfg, err := loadAWSConfig(ctx, awsconfig.WithRegion(cfg.LocalRegion))
	if err != nil {
		return err
	}
	standbyCfg, err := loadAWSConfig(ctx, awsconfig.WithRegion(cfg.StandbyRegion))
	if err != nil {
		return err
	}
	ops := control.NewFailoverOps(
		cfg,
		control.NewAWSCommandClient(ssm.NewFromConfig(standbyCfg)),
		control.NewAWSHealthCheckClient(route53.NewFromConfig(awsCfg)),
		control.NewAWSDynamoProbe(dynamodb.NewFromConfig(awsCfg), cfg.TableName),
	)
	startLambda(ops.Handle)
	return nil
}
