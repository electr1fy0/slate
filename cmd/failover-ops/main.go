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
	ctx := context.Background()
	cfg, err := control.LoadFailoverOpsConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.LocalRegion))
	if err != nil {
		log.Fatal(err)
	}
	standbyCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.StandbyRegion))
	if err != nil {
		log.Fatal(err)
	}
	ops := control.NewFailoverOps(
		cfg,
		control.NewAWSCommandClient(ssm.NewFromConfig(standbyCfg)),
		control.NewAWSHealthCheckClient(route53.NewFromConfig(awsCfg)),
		control.NewAWSDynamoProbe(dynamodb.NewFromConfig(awsCfg), cfg.TableName),
	)
	lambda.Start(ops.Handle)
}
