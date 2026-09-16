package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

func TestRunAndMainBranches(t *testing.T) {
	t.Setenv("STANDBY_INSTANCE_ID", "i-standby")
	t.Setenv("PRIMARY_HEALTH_CHECK_ID", "health-check")
	t.Setenv("TABLE_NAME", "continuum")

	oldLoad := loadAWSConfig
	oldStart := startLambda
	oldExit := exit
	oldFatal := fatal
	t.Cleanup(func() {
		loadAWSConfig = oldLoad
		startLambda = oldStart
		exit = oldExit
		fatal = oldFatal
	})

	loads := 0
	started := 0
	loadAWSConfig = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		loads++
		return aws.Config{Region: "test-region"}, nil
	}
	startLambda = func(any) { started++ }
	if err := run(); err != nil {
		t.Fatalf("run = %v", err)
	}
	if loads != 2 || started != 1 {
		t.Fatalf("AWS loads/start count = %d/%d, want 2/1", loads, started)
	}
	main()
	if started != 2 {
		t.Fatalf("main success start count = %d, want 2", started)
	}

	loadAWSConfig = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, errors.New("local AWS config failed")
	}
	if err := run(); err == nil {
		t.Fatal("local AWS config error should be returned")
	}

	secondLoad := 0
	loadAWSConfig = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		secondLoad++
		if secondLoad == 2 {
			return aws.Config{}, errors.New("standby AWS config failed")
		}
		return aws.Config{Region: "test-region"}, nil
	}
	if err := run(); err == nil {
		t.Fatal("standby AWS config error should be returned")
	}

	t.Setenv("STANDBY_INSTANCE_ID", "")
	if err := run(); err == nil {
		t.Fatal("configuration error should be returned")
	}
	t.Setenv("STANDBY_INSTANCE_ID", "i-standby")

	loadAWSConfig = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, errors.New("main startup failed")
	}
	gotFatal := error(nil)
	fatal = func(err error) { gotFatal = err }
	main()
	if gotFatal == nil {
		t.Fatal("main should pass run failures to fatal")
	}

	exitCode := 0
	exit = func(code int) { exitCode = code }
	fatal = oldFatal
	fatal(errors.New("default fatal hook"))
	if exitCode != 1 {
		t.Fatalf("default fatal hook exit code = %d, want 1", exitCode)
	}
}
