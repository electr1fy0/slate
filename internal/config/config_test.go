package config

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultsMatchLegacy(t *testing.T) {
	s := Defaults()
	if s.Region != "ap-southeast-2" || s.AppRole != "active" || s.TableName != "continuum" {
		t.Fatalf("unexpected defaults: %+v", s)
	}
	if s.RefreshTokenTTLS != 14*24*3600 || s.WSMaxMessageBytes != 64*1024 {
		t.Fatalf("unexpected token/ws defaults: %+v", s)
	}
	if len(s.CORSOrigins) != 2 || s.CORSOrigins[0] != "http://localhost:3000" {
		t.Fatalf("unexpected CORS defaults: %#v", s.CORSOrigins)
	}
}

func TestLoadFromParsesAndOverrides(t *testing.T) {
	env := map[string]string{"AWS_REGION": "eu-west-1", "ERROR_RATE_THRESHOLD": ".25", "DB_STALL": "1", "BACKEND_ID": " b ", "FENCING_ENABLED": "1", "CORS_ORIGINS": " https://one.test,https://two.test "}
	s, err := LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if s.Region != "eu-west-1" || s.ErrorRateThreshold != .25 || !s.DBStall || s.BackendID != "B" || !s.FencingEnabled {
		t.Fatalf("overrides not applied: %+v", s)
	}
	if len(s.CORSOrigins) != 2 || s.CORSOrigins[0] != "https://one.test" || s.CORSOrigins[1] != "https://two.test" {
		t.Fatalf("bad origins %#v", s.CORSOrigins)
	}
}

func TestFencingConfigurationValidationAndLegacyDefaults(t *testing.T) {
	if s := Defaults(); s.FencingEnabled || s.BackendID != "" {
		t.Fatalf("legacy defaults unexpectedly enable fencing: %+v", s)
	}
	for _, env := range []map[string]string{
		{"FENCING_ENABLED": "1"},
		{"FENCING_ENABLED": "true", "BACKEND_ID": "A"},
		{"FENCING_ENABLED": "1", "BACKEND_ID": "C"},
	} {
		if _, err := LoadFrom(func(key string) string { return env[key] }); err == nil {
			t.Fatalf("invalid fencing environment accepted: %#v", env)
		}
	}
}

func TestLoadFromReturnsInvalidNumericError(t *testing.T) {
	_, err := LoadFrom(func(k string) string {
		if k == "LOGIN_RATE_LIMIT" {
			return "oops"
		}
		return ""
	})
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadFromRejectsEveryNumericSetting(t *testing.T) {
	keys := []string{
		"DEEP_PROBE_INTERVAL_S", "ERROR_RATE_WINDOW", "ERROR_RATE_THRESHOLD", "DISK_THRESHOLD_PCT",
		"LOAD_THRESHOLD", "CHAOS_ERROR_PCT", "CHAOS_LATENCY_MS", "HEARTBEAT_TTL_S",
		"ACCESS_TOKEN_TTL_S", "REFRESH_TOKEN_TTL_S", "RATE_LIMIT_WINDOW_S", "LOGIN_RATE_LIMIT",
		"REGISTER_RATE_LIMIT", "WS_MAX_MESSAGE_BYTES", "WS_MAX_IDLE_S", "WS_SWEEP_INTERVAL_S",
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			_, err := LoadFrom(func(name string) string {
				if name == key {
					return "not-a-number"
				}
				return ""
			})
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("LoadFrom(%s) error = %v", key, err)
			}
		})
	}
}

func TestConfigHelpersAndMustLoad(t *testing.T) {
	getenv := func(key string) string {
		if key == "INT" {
			return "42"
		}
		if key == "FLOAT" {
			return "3.5"
		}
		return ""
	}
	if got, err := intEnv(getenv, "INT", 1); err != nil || got != 42 {
		t.Fatalf("intEnv valid = %d, %v", got, err)
	}
	if got, err := intEnv(getenv, "MISSING", 7); err != nil || got != 7 {
		t.Fatalf("intEnv fallback = %d, %v", got, err)
	}
	if got, err := floatEnv(getenv, "FLOAT", 1); err != nil || got != 3.5 {
		t.Fatalf("floatEnv valid = %v, %v", got, err)
	}
	if got, err := floatEnv(getenv, "MISSING", 7); err != nil || got != 7 {
		t.Fatalf("floatEnv fallback = %v, %v", got, err)
	}
	if _, err := intEnv(func(string) string { return "bad" }, "INT", 1); err == nil {
		t.Fatal("invalid int accepted")
	}
	if _, err := floatEnv(func(string) string { return "bad" }, "FLOAT", 1); err == nil {
		t.Fatal("invalid float accepted")
	}

	old := os.Getenv("LOGIN_RATE_LIMIT")
	t.Setenv("LOGIN_RATE_LIMIT", old)
	if got := MustLoad(); got.LoginRateLimit == 0 {
		t.Fatalf("MustLoad returned empty settings: %+v", got)
	}
	t.Setenv("LOGIN_RATE_LIMIT", "bad")
	defer func() {
		if recover() == nil {
			t.Fatal("MustLoad did not panic on invalid configuration")
		}
	}()
	_ = MustLoad()
}

func TestLoadUsesProcessEnvironment(t *testing.T) {
	t.Setenv("AWS_REGION", "eu-central-1")
	s, err := Load()
	if err != nil || s.Region != "eu-central-1" {
		t.Fatalf("Load = %+v, %v", s, err)
	}
}
