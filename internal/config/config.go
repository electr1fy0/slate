// Package config contains the runtime settings shared by the API and Lambda
// binaries.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Settings is the complete set of backend settings. Durations are expressed
// in seconds to keep the configuration contract explicit.
type Settings struct {
	Region                string
	AppRole               string
	BackendID             string
	FencingEnabled        bool
	RoleFile              string
	TableName             string
	DDBEndpointMode       string
	DeepProbeIntervalS    int
	ErrorRateWindow       int
	ErrorRateThreshold    float64
	DiskThresholdPct      float64
	LoadThreshold         float64
	ChaosErrorPct         int
	ChaosLatencyMS        int
	DBStall               bool
	AdminToken            string
	HeartbeatTTLS         int
	JWTSecret             string
	JWTAlgorithm          string
	AccessTokenTTLS       int
	RefreshTokenTTLS      int
	RateLimitWindowS      int
	LoginRateLimit        int
	RegisterRateLimit     int
	BootstrapTeacherEmail string
	BootstrapTeacherPass  string
	WSMaxMessageBytes     int
	WSMaxIdleS            int
	WSSweepIntervalS      int
	CORSOrigins           []string
	SMTPHost              string
	SMTPPort              int
	SMTPUsername          string
	SMTPPassword          string
	SMTPFrom              string
	PublicBaseURL         string
}

// Defaults returns a new settings value with the same defaults as the legacy
// service. A fresh slice is returned so callers can safely modify origins.
func Defaults() Settings {
	return Settings{
		Region:             "ap-southeast-2",
		AppRole:            "active",
		RoleFile:           "/etc/continuum/role",
		TableName:          "continuum",
		DeepProbeIntervalS: 5,
		ErrorRateWindow:    100,
		ErrorRateThreshold: 0.5,
		DiskThresholdPct:   90,
		LoadThreshold:      8,
		AdminToken:         "continuum-demo-token",
		HeartbeatTTLS:      300,
		JWTSecret:          "dev-only-insecure-secret-change-me-32b",
		JWTAlgorithm:       "HS256",
		AccessTokenTTLS:    3600,
		RefreshTokenTTLS:   14 * 24 * 3600,
		RateLimitWindowS:   60,
		LoginRateLimit:     5,
		RegisterRateLimit:  3,
		WSMaxMessageBytes:  64 * 1024,
		WSMaxIdleS:         90,
		WSSweepIntervalS:   15,
		SMTPPort:           587,
		CORSOrigins:        []string{"http://localhost:3000", "http://127.0.0.1:3000"},
	}
}

// Load parses environment variables. Invalid numeric values are returned as
// errors instead of silently selecting an unsafe default.
func Load() (Settings, error) { return LoadFrom(os.Getenv) }

// LoadFrom is injectable for deterministic tests and embedding applications.
func LoadFrom(getenv func(string) string) (Settings, error) {
	s := Defaults()
	get := func(name, fallback string) string {
		if value := getenv(name); value != "" {
			return value
		}
		return fallback
	}
	var err error
	s.Region = get("AWS_REGION", s.Region)
	s.AppRole = get("APP_ROLE", s.AppRole)
	s.BackendID = strings.ToUpper(strings.TrimSpace(getenv("BACKEND_ID")))
	if s.BackendID != "" && s.BackendID != "A" && s.BackendID != "B" {
		return s, fmt.Errorf("BACKEND_ID must be A or B")
	}
	fencing := getenv("FENCING_ENABLED")
	if fencing != "" && fencing != "0" && fencing != "1" {
		return s, fmt.Errorf("FENCING_ENABLED must be 0 or 1")
	}
	s.FencingEnabled = fencing == "1"
	if s.FencingEnabled && s.BackendID == "" {
		return s, fmt.Errorf("BACKEND_ID must be A or B when FENCING_ENABLED=1")
	}
	s.RoleFile = get("CONTINUUM_ROLE_FILE", s.RoleFile)
	s.TableName = get("TABLE_NAME", s.TableName)
	s.DDBEndpointMode = getenv("DDB_ENDPOINT_MODE")
	if s.DeepProbeIntervalS, err = intEnv(getenv, "DEEP_PROBE_INTERVAL_S", s.DeepProbeIntervalS); err != nil {
		return s, err
	}
	if s.ErrorRateWindow, err = intEnv(getenv, "ERROR_RATE_WINDOW", s.ErrorRateWindow); err != nil {
		return s, err
	}
	if s.ErrorRateThreshold, err = floatEnv(getenv, "ERROR_RATE_THRESHOLD", s.ErrorRateThreshold); err != nil {
		return s, err
	}
	if s.DiskThresholdPct, err = floatEnv(getenv, "DISK_THRESHOLD_PCT", s.DiskThresholdPct); err != nil {
		return s, err
	}
	if s.LoadThreshold, err = floatEnv(getenv, "LOAD_THRESHOLD", s.LoadThreshold); err != nil {
		return s, err
	}
	if s.ChaosErrorPct, err = intEnv(getenv, "CHAOS_ERROR_PCT", s.ChaosErrorPct); err != nil {
		return s, err
	}
	if s.ChaosLatencyMS, err = intEnv(getenv, "CHAOS_LATENCY_MS", s.ChaosLatencyMS); err != nil {
		return s, err
	}
	s.DBStall = getenv("DB_STALL") == "1"
	s.AdminToken = get("ADMIN_TOKEN", s.AdminToken)
	if s.HeartbeatTTLS, err = intEnv(getenv, "HEARTBEAT_TTL_S", s.HeartbeatTTLS); err != nil {
		return s, err
	}
	s.JWTSecret = get("JWT_SECRET", s.JWTSecret)
	s.AccessTokenTTLS, err = intEnv(getenv, "ACCESS_TOKEN_TTL_S", s.AccessTokenTTLS)
	if err != nil {
		return s, err
	}
	s.RefreshTokenTTLS, err = intEnv(getenv, "REFRESH_TOKEN_TTL_S", s.RefreshTokenTTLS)
	if err != nil {
		return s, err
	}
	s.RateLimitWindowS, err = intEnv(getenv, "RATE_LIMIT_WINDOW_S", s.RateLimitWindowS)
	if err != nil {
		return s, err
	}
	s.LoginRateLimit, err = intEnv(getenv, "LOGIN_RATE_LIMIT", s.LoginRateLimit)
	if err != nil {
		return s, err
	}
	s.RegisterRateLimit, err = intEnv(getenv, "REGISTER_RATE_LIMIT", s.RegisterRateLimit)
	if err != nil {
		return s, err
	}
	s.BootstrapTeacherEmail = getenv("BOOTSTRAP_TEACHER_EMAIL")
	s.BootstrapTeacherPass = getenv("BOOTSTRAP_TEACHER_PASSWORD")
	s.SMTPHost = strings.TrimSpace(getenv("SMTP_HOST"))
	s.SMTPPort, err = intEnv(getenv, "SMTP_PORT", s.SMTPPort)
	if err != nil {
		return s, err
	}
	s.SMTPUsername = getenv("SMTP_USERNAME")
	s.SMTPPassword = getenv("SMTP_PASSWORD")
	s.SMTPFrom = strings.TrimSpace(getenv("SMTP_FROM"))
	s.PublicBaseURL = strings.TrimRight(strings.TrimSpace(getenv("PUBLIC_BASE_URL")), "/")
	if s.SMTPPort <= 0 || s.SMTPPort > 65535 {
		return s, fmt.Errorf("SMTP_PORT must be between 1 and 65535")
	}
	if (s.SMTPUsername == "") != (s.SMTPPassword == "") {
		return s, fmt.Errorf("SMTP_USERNAME and SMTP_PASSWORD must be configured together")
	}
	if (s.SMTPHost == "") != (s.SMTPFrom == "") {
		return s, fmt.Errorf("SMTP_HOST and SMTP_FROM must be configured together")
	}
	if s.SMTPHost != "" && s.PublicBaseURL == "" {
		return s, fmt.Errorf("PUBLIC_BASE_URL is required when SMTP is configured")
	}
	s.WSMaxMessageBytes, err = intEnv(getenv, "WS_MAX_MESSAGE_BYTES", s.WSMaxMessageBytes)
	if err != nil {
		return s, err
	}
	s.WSMaxIdleS, err = intEnv(getenv, "WS_MAX_IDLE_S", s.WSMaxIdleS)
	if err != nil {
		return s, err
	}
	s.WSSweepIntervalS, err = intEnv(getenv, "WS_SWEEP_INTERVAL_S", s.WSSweepIntervalS)
	if err != nil {
		return s, err
	}
	if raw := getenv("CORS_ORIGINS"); raw != "" {
		s.CORSOrigins = make([]string, 0)
		for _, origin := range strings.Split(raw, ",") {
			s.CORSOrigins = append(s.CORSOrigins, strings.TrimSpace(origin))
		}
	}
	return s, nil
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

// MustLoad is convenient for binaries where malformed configuration is fatal.
func MustLoad() Settings {
	s, err := Load()
	if err != nil {
		panic(err)
	}
	return s
}
