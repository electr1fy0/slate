package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

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
	region := strings.TrimSpace(os.Getenv("AWS_REGION"))
	table := strings.TrimSpace(os.Getenv("TABLE_NAME"))
	token := os.Getenv("ADMIN_TOKEN")
	backendA := strings.TrimSpace(os.Getenv("BACKEND_A_URL"))
	backendB := strings.TrimSpace(os.Getenv("BACKEND_B_URL"))
	if region == "" || table == "" || token == "" || backendA == "" || backendB == "" {
		return errors.New("AWS_REGION, TABLE_NAME, ADMIN_TOKEN, BACKEND_A_URL, and BACKEND_B_URL are required")
	}
	timeoutMS := 2000
	if raw := strings.TrimSpace(os.Getenv("PROBE_TIMEOUT_MS")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 100 || parsed > 30000 {
			return errors.New("PROBE_TIMEOUT_MS must be an integer from 100 to 30000")
		}
		timeoutMS = parsed
	}
	ctx := context.Background()
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(region))
	if err != nil {
		return fmt.Errorf("load AWS configuration: %w", err)
	}
	verifier := &continuum.Verifier{Config: continuum.VerifierConfig{Region: region, AdminToken: token,
		BackendURLs: map[string]string{"A": backendA, "B": backendB}, ProbeTimeout: time.Duration(timeoutMS) * time.Millisecond},
		Control: continuum.NewDDBControl(dynamodb.NewFromConfig(awsCfg), table)}
	lambda.Start(func(ctx context.Context, event map[string]any) (map[string]any, error) {
		action, _ := event["action"].(string)
		log.Printf("continuum verifier action=%s", action)
		return verifier.Handle(ctx, event)
	})
	return nil
}
