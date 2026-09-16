package health

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestDeepHealthyReportAndRounding(t *testing.T) {
	report, status := Deep(context.Background(), Config{
		Region: "ap-south-1", Role: "active", ErrorRateThreshold: .5,
		DiskThresholdPct: 90, LoadThreshold: 8,
	}, Probe{
		DBProbe: func(context.Context, string) (map[string]any, error) {
			return map[string]any{"ok": true, "latency_ms": 2}, nil
		},
		ErrorRate: func() float64 { return .25 },
		DiskUsage: func(context.Context) (uint64, uint64, error) { return 123, 1000, nil },
		Load1:     func(context.Context) (float64, error) { return 1.234, nil },
	}, ChaosSnapshot{})
	if status != http.StatusOK || !report.Healthy {
		t.Fatalf("healthy report = %#v status=%d", report, status)
	}
	if report.Checks["disk"].(map[string]any)["used_pct"] != 12.3 {
		t.Fatalf("disk rounding = %#v", report.Checks["disk"])
	}
	if report.Checks["load"].(map[string]any)["loadavg"] != 1.23 {
		t.Fatalf("load rounding = %#v", report.Checks["load"])
	}
}

func TestDeepUnhealthyChecks(t *testing.T) {
	report, status := Deep(context.Background(), Config{Region: "r", Role: "standby",
		ErrorRateThreshold: .5, DiskThresholdPct: 90, LoadThreshold: 8}, Probe{
		DBProbe:   func(context.Context, string) (map[string]any, error) { return nil, errors.New("ddb unavailable") },
		ErrorRate: func() float64 { return .5 },
		DiskUsage: func(context.Context) (uint64, uint64, error) { return 95, 100, nil },
		Load1:     func(context.Context) (float64, error) { return 9, nil },
	}, ChaosSnapshot{DBStall: true})
	if status != http.StatusServiceUnavailable || report.Healthy {
		t.Fatalf("unhealthy report = %#v status=%d", report, status)
	}
	if report.Checks["ddb"].(map[string]any)["reason"] != "db_stall chaos flag" {
		t.Fatalf("db stall reason = %#v", report.Checks["ddb"])
	}
	for _, key := range []string{"error_rate", "disk", "load"} {
		if !report.Checks[key].(map[string]any)["ok"].(bool) {
			continue
		}
		t.Fatalf("%s should be unhealthy: %#v", key, report.Checks[key])
	}
}

func TestDeepContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, status := Deep(ctx, Config{ErrorRateThreshold: .5}, Probe{
		DBProbe: func(context.Context, string) (map[string]any, error) {
			t.Fatal("DB probe should not run after cancellation")
			return nil, nil
		},
	}, ChaosSnapshot{})
	if status != http.StatusServiceUnavailable || report.Healthy || report.Checks["context"] == nil {
		t.Fatalf("canceled report = %#v status=%d", report, status)
	}
}

func TestDeepProbeBranchesAndHelpers(t *testing.T) {
	config := Config{Region: "r", Role: "active", ErrorRateThreshold: .5, DiskThresholdPct: 90, LoadThreshold: 8}
	report, status := Deep(nil, config, Probe{
		DBProbe:   nil,
		ErrorRate: nil,
		DiskUsage: func(context.Context) (uint64, uint64, error) { return 0, 0, errors.New("disk unavailable") },
		Load1:     func(context.Context) (float64, error) { return 0, errors.New("load unavailable") },
	}, ChaosSnapshot{})
	if status != http.StatusServiceUnavailable || report.Healthy || report.Checks["ddb"].(map[string]any)["ok"] != false {
		t.Fatalf("unavailable probes report = %#v status=%d", report, status)
	}
	report, status = Deep(context.Background(), config, Probe{
		DBProbe:   func(context.Context, string) (map[string]any, error) { return nil, errors.New("ddb unavailable") },
		ErrorRate: func() float64 { return 0 },
		DiskUsage: func(context.Context) (uint64, uint64, error) { return 1, 100, nil },
		Load1:     func(context.Context) (float64, error) { return 1, nil },
	}, ChaosSnapshot{})
	if status != http.StatusServiceUnavailable || report.Checks["ddb"].(map[string]any)["reason"] != "ddb unavailable" {
		t.Fatalf("DB error report = %#v status=%d", report, status)
	}
	defaultConfig := config
	defaultConfig.LoadThreshold = 100
	if report, status = Deep(context.Background(), defaultConfig, Probe{
		DBProbe:   func(context.Context, string) (map[string]any, error) { return map[string]any{"ok": true}, nil },
		ErrorRate: func() float64 { return 0 },
	}, ChaosSnapshot{}); status != http.StatusOK || !report.Healthy {
		t.Fatalf("default health probes report = %#v status=%d", report, status)
	}

	for _, check := range []map[string]any{nil, {"ok": false}} {
		report, status = Deep(context.Background(), config, Probe{
			DBProbe:   func(context.Context, string) (map[string]any, error) { return check, nil },
			ErrorRate: func() float64 { return 0 },
			DiskUsage: func(context.Context) (uint64, uint64, error) { return 1, 100, nil },
			Load1:     func(context.Context) (float64, error) { return 1, nil },
		}, ChaosSnapshot{})
		if status != http.StatusServiceUnavailable || report.Healthy {
			t.Fatalf("invalid DB check was healthy: %#v status=%d", report, status)
		}
	}

	for _, cancelStage := range []string{"db", "error", "disk"} {
		ctx, cancel := context.WithCancel(context.Background())
		probe := Probe{
			DBProbe: func(context.Context, string) (map[string]any, error) {
				if cancelStage == "db" {
					cancel()
				}
				return map[string]any{"ok": true}, nil
			},
			ErrorRate: func() float64 {
				if cancelStage == "error" {
					cancel()
				}
				return 0
			},
			DiskUsage: func(context.Context) (uint64, uint64, error) {
				if cancelStage == "disk" {
					cancel()
				}
				return 1, 100, nil
			},
			Load1: func(context.Context) (float64, error) { return 1, nil },
		}
		report, status = Deep(ctx, config, probe, ChaosSnapshot{})
		cancel()
		if status != http.StatusServiceUnavailable || report.Checks["context"] == nil {
			t.Fatalf("cancellation after %s = %#v status=%d", cancelStage, report, status)
		}
	}

	zeroDisk, status := Deep(context.Background(), config, Probe{
		DBProbe:   func(context.Context, string) (map[string]any, error) { return map[string]any{"ok": true}, nil },
		ErrorRate: func() float64 { return 0 },
		DiskUsage: func(context.Context) (uint64, uint64, error) { return 0, 0, nil },
		Load1:     func(context.Context) (float64, error) { return 0, nil },
	}, ChaosSnapshot{})
	if status != http.StatusServiceUnavailable || zeroDisk.Healthy {
		t.Fatalf("zero disk report = %#v status=%d", zeroDisk, status)
	}

	if _, status := Aggregate(context.Background(), config, Probe{
		DBProbe:   func(context.Context, string) (map[string]any, error) { return map[string]any{"ok": true}, nil },
		ErrorRate: func() float64 { return 0 },
		DiskUsage: func(context.Context) (uint64, uint64, error) { return 1, 100, nil },
		Load1:     func(context.Context) (float64, error) { return 1, nil },
	}, ChaosSnapshot{}); status != http.StatusOK {
		t.Fatal("Aggregate did not delegate to Deep")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if report, status := canceledReport(Report{}, ctx); status != http.StatusServiceUnavailable || report.Checks["context"] == nil {
		t.Fatalf("canceledReport = %#v status=%d", report, status)
	}
	if contextAlive(ctx) || !contextAlive(context.Background()) {
		t.Fatal("contextAlive edges failed")
	}
	if trimReason(nil) != "" || trimReason(errors.New("short")) != "short" || len(trimReason(errors.New(strings.Repeat("x", 250)))) != 200 {
		t.Fatal("trimReason edges failed")
	}
	if round(1.2345, 2) != 1.23 {
		t.Fatal("rounding helper failed")
	}
}

func TestStandardHealthProbesAndFallbacks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := StandardDiskUsage(ctx, "/"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled disk probe = %v", err)
	}
	if _, _, err := StandardDiskUsage(nil, "/definitely/missing/slate-path"); err == nil {
		t.Fatal("missing disk path unexpectedly succeeded")
	}
	if used, total, err := StandardDiskUsage(context.Background(), "/"); err != nil || total == 0 || used > total {
		t.Fatalf("root disk probe = %d/%d, %v", used, total, err)
	}
	if used, total := diskUsageBytes(10, 20); used != 0 || total != 10 {
		t.Fatalf("disk usage overflow guard = %d/%d", used, total)
	}
	if used, total := diskUsageBytes(20, 5); used != 15 || total != 20 {
		t.Fatalf("disk usage calculation = %d/%d", used, total)
	}

	temp := t.TempDir()
	validPath := temp + "/loadavg"
	if err := os.WriteFile(validPath, []byte("1.25 2.0 3.0"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := StandardLoad1(nil, validPath); err != nil || got != 1.25 {
		t.Fatalf("valid load probe = %v, %v", got, err)
	}
	if _, err := StandardLoad1(context.Background(), ""); err != nil {
		t.Fatalf("default load path = %v", err)
	}
	emptyPath := temp + "/empty"
	if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := StandardLoad1(context.Background(), emptyPath); err == nil {
		t.Fatal("empty load file unexpectedly succeeded")
	}
	badPath := temp + "/bad"
	if err := os.WriteFile(badPath, []byte("not-a-number"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := StandardLoad1(context.Background(), badPath); err == nil {
		t.Fatal("invalid load file unexpectedly succeeded")
	}
	if _, err := StandardLoad1(context.Background(), "/definitely/missing/slate-load"); err == nil {
		t.Fatal("missing custom load path unexpectedly succeeded")
	}
	if _, err := StandardLoad1(ctx, validPath); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled load probe = %v", err)
	}

	originalReadFile := readFile
	readFile = func(string) ([]byte, error) { return nil, errors.New("proc unavailable") }
	if got, err := StandardLoad1(context.Background(), "/proc/loadavg"); err != nil || got <= 0 {
		t.Fatalf("default load fallback = %v, %v", got, err)
	}
	readFile = originalReadFile
}
