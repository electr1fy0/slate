package control

import (
	"fmt"
	"strconv"
)

// HealthVerifierConfig is the environment contract for the deep-health
// verifier Lambda. Required values are checked when the binary starts, while
// the defaults mirror the deployed API contract.
type HealthVerifierConfig struct {
	Region          string
	TableName       string
	TargetURL       string
	RoleLabel       string
	MetricNamespace string
	ProbeTimeoutS   float64
}

func LoadHealthVerifierConfig(getenv func(string) string) (HealthVerifierConfig, error) {
	c := HealthVerifierConfig{
		Region:          envOr(getenv, "AWS_REGION", "ap-south-1"),
		TargetURL:       getenv("TARGET_URL"),
		RoleLabel:       envOr(getenv, "ROLE_LABEL", "primary"),
		MetricNamespace: envOr(getenv, "METRIC_NAMESPACE", "Continuum"),
	}
	c.TableName = getenv("TABLE_NAME")
	if c.TableName == "" {
		return c, fmt.Errorf("TABLE_NAME is required")
	}
	var err error
	c.ProbeTimeoutS, err = floatEnv(getenv, "PROBE_TIMEOUT_S", 8)
	if err != nil {
		return c, err
	}
	if c.ProbeTimeoutS <= 0 {
		return c, fmt.Errorf("PROBE_TIMEOUT_S must be greater than zero")
	}
	return c, nil
}

// FailoverOpsConfig is the environment contract for promotion/failback
// actions. The commands intentionally retain the existing shell command
// defaults because the systemd role file is part of the deployment contract.
type FailoverOpsConfig struct {
	StandbyInstanceID    string
	PrimaryHealthCheckID string
	SourceRegion         string
	LocalRegion          string
	StandbyRegion        string
	TableName            string
	CommandWaitTimeoutS  int
	CommandPollIntervalS float64
	PromoteCommand       string
	DemoteCommand        string
}

const (
	defaultPromoteCommand = "echo active > /etc/continuum/role && systemctl restart continuum && sleep 3 && curl -s http://localhost/health/live"
	defaultDemoteCommand  = "echo standby > /etc/continuum/role && systemctl restart continuum && sleep 3 && curl -s http://localhost/health/live"
)

func LoadFailoverOpsConfig(getenv func(string) string) (FailoverOpsConfig, error) {
	c := FailoverOpsConfig{
		StandbyInstanceID:    getenv("STANDBY_INSTANCE_ID"),
		PrimaryHealthCheckID: getenv("PRIMARY_HEALTH_CHECK_ID"),
		SourceRegion:         envOr(getenv, "SOURCE_REGION", "ap-south-1"),
		LocalRegion:          envOr(getenv, "LOCAL_REGION", "ap-south-1"),
		StandbyRegion:        envOr(getenv, "STANDBY_REGION", "ap-southeast-1"),
		TableName:            getenv("TABLE_NAME"),
		PromoteCommand:       envOr(getenv, "PROMOTE_COMMAND", defaultPromoteCommand),
		DemoteCommand:        envOr(getenv, "DEMOTE_COMMAND", defaultDemoteCommand),
	}
	if c.StandbyInstanceID == "" {
		return c, fmt.Errorf("STANDBY_INSTANCE_ID is required")
	}
	if c.PrimaryHealthCheckID == "" {
		return c, fmt.Errorf("PRIMARY_HEALTH_CHECK_ID is required")
	}
	if c.TableName == "" {
		return c, fmt.Errorf("TABLE_NAME is required")
	}
	var err error
	c.CommandWaitTimeoutS, err = intEnv(getenv, "COMMAND_WAIT_TIMEOUT_S", 180)
	if err != nil {
		return c, err
	}
	c.CommandPollIntervalS, err = floatEnv(getenv, "COMMAND_POLL_INTERVAL_S", 5)
	if err != nil {
		return c, err
	}
	if c.CommandWaitTimeoutS <= 0 {
		return c, fmt.Errorf("COMMAND_WAIT_TIMEOUT_S must be greater than zero")
	}
	if c.CommandPollIntervalS < 0 {
		return c, fmt.Errorf("COMMAND_POLL_INTERVAL_S must not be negative")
	}
	return c, nil
}

type ReconcilerConfig struct {
	TableName       string
	LookbackMinutes int
	MetricNamespace string
}

func LoadReconcilerConfig(getenv func(string) string) (ReconcilerConfig, error) {
	c := ReconcilerConfig{
		TableName:       getenv("TABLE_NAME"),
		MetricNamespace: envOr(getenv, "METRIC_NAMESPACE", "Continuum"),
	}
	if c.TableName == "" {
		return c, fmt.Errorf("TABLE_NAME is required")
	}
	var err error
	c.LookbackMinutes, err = intEnv(getenv, "LOOKBACK_MINUTES", 7*24*60)
	if err != nil {
		return c, err
	}
	if c.LookbackMinutes < 0 {
		return c, fmt.Errorf("LOOKBACK_MINUTES must not be negative")
	}
	return c, nil
}

func envOr(getenv func(string) string, name, fallback string) string {
	if value := getenv(name); value != "" {
		return value
	}
	return fallback
}

func intEnv(getenv func(string) string, name string, fallback int) (int, error) {
	raw := getenv(name)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return v, nil
}

func floatEnv(getenv func(string) string, name string, fallback float64) (float64, error) {
	raw := getenv(name)
	if raw == "" {
		return fallback, nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", name, err)
	}
	return v, nil
}
