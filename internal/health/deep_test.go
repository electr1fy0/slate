package health

import (
	"context"
	"errors"
	"net/http"
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
