package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
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
	tableName := requiredEnv("TABLE_NAME")
	if tableName == "" {
		return fmt.Errorf("TABLE_NAME is required")
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		return fmt.Errorf("AWS_REGION is required")
	}
	backendA := requiredEnv("BACKEND_A_URL")
	backendB := requiredEnv("BACKEND_B_URL")
	if backendA == "" || backendB == "" {
		return fmt.Errorf("BACKEND_A_URL and BACKEND_B_URL are required")
	}
	awsCfg, err := loadAWSConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return fmt.Errorf("load AWS config: %w", err)
	}
	control := continuum.NewDDBControl(dynamodb.NewFromConfig(awsCfg), tableName)
	// Safe one-time initialization: Ensure uses a conditional PutItem and never
	// overwrites a route or epoch created by an operator or deployment step.
	if err := control.Ensure(ctx, continuum.ControlState{ActiveBackend: "A", Epoch: 1}); err != nil {
		return fmt.Errorf("ensure continuum control state: %w", err)
	}
	router := &continuum.Router{
		Control:          control,
		BackendURLs:      map[string]string{"A": backendA, "B": backendB},
		Timeout:          durationEnv("UPSTREAM_TIMEOUT_MS", 8000),
		MaxRequestBytes:  int64Env("MAX_REQUEST_BYTES", continuum.DefaultMaxRequestBytes),
		MaxResponseBytes: int64Env("MAX_RESPONSE_BYTES", continuum.DefaultMaxResponseBytes),
	}
	adapter := &continuum.APIGatewayRouter{Router: router}
	lambda.Start(func(ctx context.Context, event events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
		return adapter.Handle(ctx, event)
	})
	return nil
}

var loadAWSConfig = awsconfig.LoadDefaultConfig

func requiredEnv(name string) string { return os.Getenv(name) }

func int64Env(name string, fallback int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func durationEnv(name string, fallbackMS int) time.Duration {
	return time.Duration(int64Env(name, int64(fallbackMS))) * time.Millisecond
}
