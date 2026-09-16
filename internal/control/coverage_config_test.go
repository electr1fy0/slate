package control

import (
	"strings"
	"testing"
)

func TestControlConfigCoverage(t *testing.T) {
	if got := envOr(func(string) string { return "" }, "KEY", "fallback"); got != "fallback" {
		t.Fatalf("env fallback = %q", got)
	}
	if got := envOr(func(string) string { return "value" }, "KEY", "fallback"); got != "value" {
		t.Fatalf("env value = %q", got)
	}
	if got, err := intEnv(func(string) string { return "" }, "INT", 4); err != nil || got != 4 {
		t.Fatalf("default int = %d %v", got, err)
	}
	if got, err := intEnv(func(string) string { return "7" }, "INT", 4); err != nil || got != 7 {
		t.Fatalf("parsed int = %d %v", got, err)
	}
	if _, err := intEnv(func(string) string { return "nope" }, "INT", 4); err == nil || !strings.Contains(err.Error(), "INT must be an integer") {
		t.Fatalf("invalid int error = %v", err)
	}
	if got, err := floatEnv(func(string) string { return "" }, "FLOAT", 1.5); err != nil || got != 1.5 {
		t.Fatalf("default float = %v %v", got, err)
	}
	if got, err := floatEnv(func(string) string { return "2.5" }, "FLOAT", 1.5); err != nil || got != 2.5 {
		t.Fatalf("parsed float = %v %v", got, err)
	}
	if _, err := floatEnv(func(string) string { return "nope" }, "FLOAT", 1.5); err == nil || !strings.Contains(err.Error(), "FLOAT must be a number") {
		t.Fatalf("invalid float error = %v", err)
	}

	healthMissing := func(key string) string {
		if key == "TABLE_NAME" {
			return ""
		}
		return ""
	}
	if _, err := LoadHealthVerifierConfig(healthMissing); err == nil {
		t.Fatal("missing health table accepted")
	}
	if cfg, err := LoadHealthVerifierConfig(func(key string) string {
		if key == "TABLE_NAME" {
			return "table"
		}
		if key == "PROBE_TIMEOUT_S" {
			return "0"
		}
		return ""
	}); err == nil || cfg.ProbeTimeoutS != 0 {
		t.Fatalf("zero health timeout = %#v %v", cfg, err)
	}
	if cfg, err := LoadHealthVerifierConfig(func(key string) string {
		values := map[string]string{"AWS_REGION": "eu-west-1", "TABLE_NAME": "table", "TARGET_URL": "https://app", "ROLE_LABEL": "secondary", "METRIC_NAMESPACE": "Ops", "PROBE_TIMEOUT_S": "2.5"}
		return values[key]
	}); err != nil || cfg.Region != "eu-west-1" || cfg.ProbeTimeoutS != 2.5 || cfg.RoleLabel != "secondary" {
		t.Fatalf("custom health config = %#v %v", cfg, err)
	}

	missingFailover := []string{"STANDBY_INSTANCE_ID", "PRIMARY_HEALTH_CHECK_ID", "TABLE_NAME"}
	for _, missing := range missingFailover {
		if _, err := LoadFailoverOpsConfig(func(key string) string {
			values := map[string]string{"STANDBY_INSTANCE_ID": "i", "PRIMARY_HEALTH_CHECK_ID": "h", "TABLE_NAME": "t"}
			delete(values, missing)
			return values[key]
		}); err == nil {
			t.Fatalf("missing failover %s accepted", missing)
		}
	}
	if _, err := LoadFailoverOpsConfig(func(key string) string {
		values := map[string]string{"STANDBY_INSTANCE_ID": "i", "PRIMARY_HEALTH_CHECK_ID": "h", "TABLE_NAME": "t", "COMMAND_POLL_INTERVAL_S": "bad"}
		return values[key]
	}); err == nil {
		t.Fatal("invalid failover poll interval accepted")
	}
	for _, values := range []map[string]string{
		{"COMMAND_WAIT_TIMEOUT_S": "0"},
		{"COMMAND_POLL_INTERVAL_S": "-1"},
	} {
		if _, err := LoadFailoverOpsConfig(func(key string) string {
			base := map[string]string{"STANDBY_INSTANCE_ID": "i", "PRIMARY_HEALTH_CHECK_ID": "h", "TABLE_NAME": "t"}
			for k, v := range values {
				base[k] = v
			}
			return base[key]
		}); err == nil {
			t.Fatalf("invalid failover value accepted: %#v", values)
		}
	}
	if cfg, err := LoadFailoverOpsConfig(func(key string) string {
		values := map[string]string{
			"STANDBY_INSTANCE_ID": "i", "PRIMARY_HEALTH_CHECK_ID": "h", "TABLE_NAME": "t",
			"SOURCE_REGION": "source", "LOCAL_REGION": "local", "STANDBY_REGION": "standby",
			"COMMAND_WAIT_TIMEOUT_S": "4", "COMMAND_POLL_INTERVAL_S": "0.25",
			"PROMOTE_COMMAND": "promote", "DEMOTE_COMMAND": "demote",
		}
		return values[key]
	}); err != nil || cfg.CommandWaitTimeoutS != 4 || cfg.CommandPollIntervalS != .25 || cfg.PromoteCommand != "promote" {
		t.Fatalf("custom failover config = %#v %v", cfg, err)
	}

	if _, err := LoadReconcilerConfig(func(string) string { return "" }); err == nil {
		t.Fatal("missing reconciler table accepted")
	}
	if _, err := LoadReconcilerConfig(func(key string) string {
		if key == "TABLE_NAME" {
			return "t"
		}
		if key == "LOOKBACK_MINUTES" {
			return "-1"
		}
		return ""
	}); err == nil {
		t.Fatal("negative lookback accepted")
	}
	if cfg, err := LoadReconcilerConfig(func(key string) string {
		values := map[string]string{"TABLE_NAME": "t", "LOOKBACK_MINUTES": "10", "METRIC_NAMESPACE": "Ops"}
		return values[key]
	}); err != nil || cfg.LookbackMinutes != 10 || cfg.MetricNamespace != "Ops" {
		t.Fatalf("custom reconciler config = %#v %v", cfg, err)
	}
}
