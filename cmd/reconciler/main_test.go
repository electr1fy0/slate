package main

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
)

func TestRunAndMainBranches(t *testing.T) {
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
	if loads != 1 || started != 1 {
		t.Fatalf("AWS loads/start count = %d/%d, want 1/1", loads, started)
	}
	main()
	if started != 2 {
		t.Fatalf("main success start count = %d, want 2", started)
	}

	loadAWSConfig = func(context.Context, ...func(*awsconfig.LoadOptions) error) (aws.Config, error) {
		return aws.Config{}, errors.New("AWS config failed")
	}
	if err := run(); err == nil {
		t.Fatal("AWS config error should be returned")
	}

	t.Setenv("TABLE_NAME", "")
	if err := run(); err == nil {
		t.Fatal("configuration error should be returned")
	}
	t.Setenv("TABLE_NAME", "continuum")

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
