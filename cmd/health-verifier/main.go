package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/ayush/slate/internal/control"
)

func main() {
	ctx := context.Background()
	cfg, err := control.LoadHealthVerifierConfig(os.Getenv)
	if err != nil {
		log.Fatal(err)
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		log.Fatal(err)
	}
	verifier := control.NewHealthVerifier(
		cfg,
		control.NewAWSDynamoProbe(dynamodb.NewFromConfig(awsCfg), cfg.TableName),
		control.NewAWSCloudWatchMetrics(cloudwatch.NewFromConfig(awsCfg)),
		&http.Client{},
	)
	lambda.Start(verifier.Handle)
}
