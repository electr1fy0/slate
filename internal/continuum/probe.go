package continuum

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ayush/slate/internal/clusterstate"
)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPBackendProbe probes liveness separately from deep application readiness.
// Each request has a deadline so a down node cannot pin a monitor cycle.
func HTTPBackendProbe(client HTTPDoer, urls map[string]string, timeout time.Duration) ProbeFunc {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	if httpClient, ok := client.(*http.Client); ok {
		copyClient := *httpClient
		copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &copyClient
	}
	return func(ctx context.Context, backend string) ProbeResult {
		base := strings.TrimRight(strings.TrimSpace(urls[backend]), "/")
		if base == "" {
			return ProbeResult{Reason: "backend URL is not configured"}
		}
		liveStatus, liveErr := probeWithBackend(ctx, client, timeout, base+"/health/live", backend, nil)
		liveness := liveErr == nil && liveStatus >= 200 && liveStatus < 300
		if liveErr != nil {
			return ProbeResult{Liveness: false, Ready: false, Reason: "liveness probe failed: " + brief(liveErr.Error())}
		}
		var deep struct {
			Healthy bool           `json:"healthy"`
			Checks  map[string]any `json:"checks"`
		}
		deepStatus, deepErr := probeWithBackend(ctx, client, timeout, base+"/health/deep", backend, &deep)
		ready := deepErr == nil && deepStatus == http.StatusOK && deep.Healthy
		if deepErr != nil {
			return ProbeResult{Liveness: liveness, Ready: false, Reason: "deep readiness probe failed: " + brief(deepErr.Error())}
		}
		if !ready {
			reason := "deep readiness returned unhealthy"
			if deepStatus != http.StatusOK {
				reason = fmt.Sprintf("deep readiness returned HTTP %d", deepStatus)
			}
			if ddb, ok := deep.Checks["ddb"].(map[string]any); ok {
				if why, ok := ddb["reason"].(string); ok && why != "" {
					reason = "database readiness failed: " + brief(why)
				}
			}
			return ProbeResult{Liveness: liveness, Ready: false, Reason: reason}
		}
		return ProbeResult{Liveness: liveness, Ready: true, Reason: "deep health passed"}
	}
}

func probeWithBackend(ctx context.Context, client HTTPDoer, timeout time.Duration, target, backend string, decode any) (int, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	// Health requests are read-only but the application middleware still
	// requires authenticated routing metadata when fencing is enabled. Epoch 1
	// is sufficient for these non-mutating probes; commits always use the
	// router-provided current epoch.
	req.Header.Set(clusterstate.BackendHeader, backend)
	req.Header.Set(clusterstate.EpochHeader, "1")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if decode != nil {
		decoder := json.NewDecoder(io.LimitReader(resp.Body, (1<<20)+1))
		if err := decoder.Decode(decode); err != nil {
			return resp.StatusCode, err
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return resp.StatusCode, fmt.Errorf("invalid trailing health data")
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	}
	return resp.StatusCode, nil
}

func probeHTTP(ctx context.Context, client HTTPDoer, timeout time.Duration, target string, decode any) (int, error) {
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, target, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if decode != nil {
		if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(decode); err != nil {
			return resp.StatusCode, err
		}
	} else {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	}
	return resp.StatusCode, nil
}

func randomIDString() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

func brief(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 180 {
		return value[:180]
	}
	return value
}
