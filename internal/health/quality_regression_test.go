package health

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
)

func TestQualityInvalidProbeDataIsUnhealthyAndSerializable(t *testing.T) {
	config := Config{ErrorRateThreshold: .5, DiskThresholdPct: 90, LoadThreshold: 10}
	base := Probe{DBProbe: func(context.Context, string) (map[string]any, error) { return map[string]any{"ok": true}, nil }, DiskUsage: func(context.Context) (uint64, uint64, error) { return 10, 100, nil }, Load1: func(context.Context) (float64, error) { return 1, nil }}
	for _, value := range []float64{math.NaN(), math.Inf(1), -1, 2} {
		probe := base
		probe.ErrorRate = func() float64 { return value }
		report, status := Deep(context.Background(), config, probe, ChaosSnapshot{})
		if status != http.StatusServiceUnavailable || report.Healthy {
			t.Fatalf("invalid rate %v was healthy", value)
		}
		if _, err := json.Marshal(report); err != nil {
			t.Fatalf("health JSON failed: %v", err)
		}
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), -1} {
		probe := base
		probe.Load1 = func(context.Context) (float64, error) { return value, nil }
		report, status := Deep(context.Background(), config, probe, ChaosSnapshot{})
		if status != http.StatusServiceUnavailable {
			t.Fatalf("invalid load %v was healthy", value)
		}
		if _, err := json.Marshal(report); err != nil {
			t.Fatal(err)
		}
	}
	probe := base
	probe.DiskUsage = func(context.Context) (uint64, uint64, error) { return 101, 100, nil }
	if report, _ := Deep(context.Background(), config, probe, ChaosSnapshot{}); report.Healthy {
		t.Fatal("accepted impossible disk usage")
	}
	ctx, cancel := context.WithCancel(context.Background())
	probe = base
	probe.Load1 = func(context.Context) (float64, error) { cancel(); return 1, nil }
	if report, _ := Deep(ctx, config, probe, ChaosSnapshot{}); report.Healthy {
		t.Fatal("ignored final-probe cancellation")
	}
}
