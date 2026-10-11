package continuum

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LoadCloudConfig parses the shared outside-VPC control/monitor environment.
// getenv is injectable so deployments can validate the exact Lambda contract.
func LoadCloudConfig(getenv func(string) string) (CloudConfig, error) {
	region := strings.TrimSpace(getenv("AWS_REGION"))
	if region == "" {
		return CloudConfig{}, errors.New("AWS_REGION is required")
	}
	table := strings.TrimSpace(getenv("TABLE_NAME"))
	if table == "" {
		return CloudConfig{}, errors.New("TABLE_NAME is required")
	}
	token := getenv("ADMIN_TOKEN")
	runtimeEnabled := false
	if raw := strings.TrimSpace(getenv("RUNTIME_ENABLED")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return CloudConfig{}, errors.New("RUNTIME_ENABLED must be true or false")
		}
		runtimeEnabled = parsed
	}
	verifier := firstConfigured(getenv, "FUNCTION_VERIFIER", "VERIFIER_FUNCTION_NAME")
	if verifier == "" {
		return CloudConfig{}, errors.New("FUNCTION_VERIFIER or VERIFIER_FUNCTION_NAME is required")
	}
	probes, err := configuredInt(getenv, "POLICY_PROBES", 3, 1, 10)
	if err != nil {
		return CloudConfig{}, err
	}
	interval, err := configuredInt(getenv, "PROBE_INTERVAL_MS", 5000, 100, 300000)
	if err != nil {
		return CloudConfig{}, err
	}
	probeTimeout, err := configuredInt(getenv, "PROBE_TIMEOUT_MS", 2000, 100, 30000)
	if err != nil {
		return CloudConfig{}, err
	}
	lease, err := configuredInt(getenv, "INCIDENT_LOCK_LEASE_SECONDS", 900, 10, 3600)
	if err != nil {
		return CloudConfig{}, err
	}
	return CloudConfig{Region: region, TableName: table, AdminToken: token, RuntimeEnabled: runtimeEnabled, VerifierFunction: verifier,
		MonitorFunction:         firstConfigured(getenv, "MONITOR_FUNCTION_NAME"),
		StateMachineARN:         strings.TrimSpace(getenv("STATE_MACHINE_ARN")),
		FailbackStateMachineARN: strings.TrimSpace(getenv("FAILBACK_STATE_MACHINE_ARN")),
		ReconcilerFunction:      strings.TrimSpace(getenv("RECONCILER_FUNCTION_NAME")),
		PublicAPIURL:            strings.TrimSpace(getenv("PUBLIC_API_URL")),
		InstanceIDA:             strings.TrimSpace(getenv("INSTANCE_ID_A")), InstanceIDB: strings.TrimSpace(getenv("INSTANCE_ID_B")),
		BackendAURL: strings.TrimSpace(getenv("BACKEND_A_URL")), BackendBURL: strings.TrimSpace(getenv("BACKEND_B_URL")),
		PolicyProbes: probes, ProbeIntervalMillis: interval,
		ProbeTimeout: time.Duration(probeTimeout) * time.Millisecond, LockLease: time.Duration(lease) * time.Second}, nil
}

func firstConfigured(getenv func(string) string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(getenv(name)); value != "" {
			return value
		}
	}
	return ""
}

func configuredInt(getenv func(string) string, name string, fallback, min, max int) (int, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return 0, fmt.Errorf("%s must be an integer from %d to %d", name, min, max)
	}
	return value, nil
}
