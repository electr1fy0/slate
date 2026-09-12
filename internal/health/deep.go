// Package health implements the deep-health aggregation used by the API and
// health-verifier Lambda. It has no AWS dependency: callers provide a small DB
// probe, while the standard disk/load probes are available as helpers.
package health

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type Config struct {
	Region             string
	Role               string
	ErrorRateThreshold float64
	DiskThresholdPct   float64
	LoadThreshold      float64
}

type ChaosSnapshot struct {
	ErrorPct  int  `json:"error_pct"`
	LatencyMS int  `json:"latency_ms"`
	DBStall   bool `json:"db_stall"`
}

// Probe is the dependency set needed to build a report. DBProbe should return
// a JSON-compatible check object, conventionally map[string]any{"ok": true}.
// DiskUsage returns used and total bytes. Load1 returns the one-minute load.
type Probe struct {
	DBProbe   func(context.Context, string) (map[string]any, error)
	ErrorRate func() float64
	DiskUsage func(context.Context) (used, total uint64, err error)
	Load1     func(context.Context) (float64, error)
}

type Report struct {
	Healthy bool           `json:"healthy"`
	Region  string         `json:"region"`
	Role    string         `json:"role"`
	Checks  map[string]any `json:"checks"`
	Chaos   ChaosSnapshot  `json:"chaos"`
}

// Deep aggregates DDB, rolling error rate, disk and load checks. Status is
// 200 when healthy and 503 otherwise, matching /health/deep.
func Deep(ctx context.Context, config Config, probe Probe, chaos ChaosSnapshot) (Report, int) {
	if ctx == nil {
		ctx = context.Background()
	}
	report := Report{Healthy: true, Region: config.Region, Role: config.Role,
		Checks: make(map[string]any), Chaos: chaos}

	if chaos.DBStall {
		report.Checks["ddb"] = map[string]any{"ok": false, "reason": "db_stall chaos flag"}
		report.Healthy = false
	} else if !contextAlive(ctx) {
		return canceledReport(report, ctx)
	} else if probe.DBProbe == nil {
		report.Checks["ddb"] = map[string]any{"ok": false, "reason": "db probe unavailable"}
		report.Healthy = false
	} else {
		check, err := probe.DBProbe(ctx, config.Region)
		if err != nil {
			report.Checks["ddb"] = map[string]any{"ok": false, "reason": trimReason(err)}
			report.Healthy = false
		} else {
			if check == nil {
				check = map[string]any{"ok": false, "reason": "empty db probe result"}
			}
			report.Checks["ddb"] = check
			if ok, valid := check["ok"].(bool); !valid || !ok {
				report.Healthy = false
			}
		}
	}

	if !contextAlive(ctx) {
		return canceledReport(report, ctx)
	}
	errRate := 0.0
	if probe.ErrorRate != nil {
		errRate = probe.ErrorRate()
	}
	errCheck := map[string]any{"value": round(errRate, 3), "threshold": config.ErrorRateThreshold,
		"ok": errRate < config.ErrorRateThreshold}
	report.Checks["error_rate"] = errCheck
	if !errCheck["ok"].(bool) {
		report.Healthy = false
	}

	if !contextAlive(ctx) {
		return canceledReport(report, ctx)
	}
	diskUsage := probe.DiskUsage
	if diskUsage == nil {
		diskUsage = func(ctx context.Context) (uint64, uint64, error) {
			return StandardDiskUsage(ctx, "/")
		}
	}
	if diskUsage == nil {
		report.Checks["disk"] = map[string]any{"ok": false, "reason": "disk probe unavailable"}
		report.Healthy = false
	} else {
		used, total, err := diskUsage(ctx)
		if err != nil || total == 0 {
			reason := "disk probe unavailable"
			if err != nil {
				reason = trimReason(err)
			}
			report.Checks["disk"] = map[string]any{"ok": false, "reason": reason}
			report.Healthy = false
		} else {
			usedPct := float64(used) / float64(total) * 100
			diskCheck := map[string]any{"used_pct": round(usedPct, 1), "ok": usedPct < config.DiskThresholdPct}
			report.Checks["disk"] = diskCheck
			if !diskCheck["ok"].(bool) {
				report.Healthy = false
			}
		}
	}

	if !contextAlive(ctx) {
		return canceledReport(report, ctx)
	}
	load1Probe := probe.Load1
	if load1Probe == nil {
		load1Probe = func(ctx context.Context) (float64, error) {
			return StandardLoad1(ctx, "/proc/loadavg")
		}
	}
	if load1Probe == nil {
		report.Checks["load"] = map[string]any{"ok": false, "reason": "load probe unavailable"}
		report.Healthy = false
	} else {
		load1, err := load1Probe(ctx)
		if err != nil {
			report.Checks["load"] = map[string]any{"ok": false, "reason": trimReason(err)}
			report.Healthy = false
		} else {
			loadCheck := map[string]any{"loadavg": round(load1, 2), "ok": load1 < config.LoadThreshold}
			report.Checks["load"] = loadCheck
			if !loadCheck["ok"].(bool) {
				report.Healthy = false
			}
		}
	}

	if report.Healthy {
		return report, http.StatusOK
	}
	return report, http.StatusServiceUnavailable
}

// Aggregate is an expressive alias used by some integrations.
func Aggregate(ctx context.Context, config Config, probe Probe, chaos ChaosSnapshot) (Report, int) {
	return Deep(ctx, config, probe, chaos)
}

func canceledReport(report Report, ctx context.Context) (Report, int) {
	if report.Checks == nil {
		report.Checks = make(map[string]any)
	}
	report.Healthy = false
	report.Checks["context"] = map[string]any{"ok": false, "reason": ctx.Err().Error()}
	return report, http.StatusServiceUnavailable
}

func contextAlive(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

func trimReason(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	if len(value) > 200 {
		return value[:200]
	}
	return value
}

func round(value float64, places int) float64 {
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}

// StandardDiskUsage probes the filesystem containing path. Passing "/"
// reproduces the Python service's deep-health check.
func StandardDiskUsage(ctx context.Context, path string) (used, total uint64, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !contextAlive(ctx) {
		return 0, 0, ctx.Err()
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, err
	}
	total = uint64(stat.Blocks) * uint64(stat.Bsize)
	free := uint64(stat.Bavail) * uint64(stat.Bsize)
	if total < free {
		return 0, total, nil
	}
	return total - free, total, nil
}

// StandardLoad1 reads Linux /proc/loadavg. On unsupported systems it falls
// back to GOMAXPROCS as a conservative local signal rather than panicking.
func StandardLoad1(ctx context.Context, path string) (float64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !contextAlive(ctx) {
		return 0, ctx.Err()
	}
	if path == "" {
		path = "/proc/loadavg"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if path != "/proc/loadavg" {
			return 0, err
		}
		return float64(runtime.GOMAXPROCS(0)), nil
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return 0, fmt.Errorf("loadavg is empty")
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, err
	}
	return value, nil
}
