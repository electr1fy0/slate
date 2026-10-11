package continuum

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ayush/slate/internal/chaos"
	"github.com/ayush/slate/internal/clusterstate"
)

type VerifierConfig struct {
	Region       string
	AdminToken   string
	BackendURLs  map[string]string
	ProbeTimeout time.Duration
}

// Verifier runs inside the private backend VPC. It only calls private backend
// URLs and the DynamoDB endpoint through DDBControl; it has no public-network
// dependency and never chooses a route.
type Verifier struct {
	Config  VerifierConfig
	Control StateReader
	HTTP    HTTPDoer
	Timeout time.Duration
	Now     func() time.Time
}

func (v *Verifier) Handle(ctx context.Context, event map[string]any) (map[string]any, error) {
	if v.Control == nil {
		return nil, errors.New("verifier control record reader is not configured")
	}
	state, err := v.Control.Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("read shared control record: %w", err)
	}
	if v.HTTP == nil {
		v.HTTP = &http.Client{Timeout: v.requestTimeout()}
	}
	switch stringValue(event, "action") {
	case "status", "probe":
		type probeResult struct {
			id     string
			result ProbeResult
		}
		results := make(chan probeResult, 2)
		for _, id := range []string{"A", "B"} {
			go func(backend string) { results <- probeResult{id: backend, result: v.probe(ctx, backend)} }(id)
		}
		byID := make(map[string]ProbeResult, 2)
		for range 2 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case result := <-results:
				byID[result.id] = result.result
			}
		}
		backends := make([]BackendStatus, 0, 2)
		for _, id := range []string{"A", "B"} {
			result := byID[id]
			backends = append(backends, BackendStatus{ID: id, Healthy: result.Healthy(), Liveness: result.Liveness, Ready: result.Ready, Reason: result.Reason})
		}
		return map[string]any{"region": v.Config.Region, "active_backend": state.ActiveBackend, "epoch": state.Epoch,
			"paused": state.Paused, "control_readable": true, "backends": backends}, nil
	case "inject_fault":
		backend := strings.ToUpper(strings.TrimSpace(stringValue(event, "backend")))
		if !validBackend(backend) {
			return nil, ErrInvalidBackend
		}
		fault := strings.ToLower(strings.TrimSpace(stringValue(event, "fault")))
		durationMS := int64Value(event, "duration_ms", 0)
		var expiresAtMS int64
		if fault == "blip" {
			if durationMS < 1 || durationMS > int64((5*time.Minute)/time.Millisecond) {
				return nil, errors.New("blip duration_ms must be between 1 and 300000")
			}
			expiresAtMS = v.timeNow().Add(time.Duration(durationMS) * time.Millisecond).UnixMilli()
		}
		patch, err := verifierFaultPatch(fault, expiresAtMS)
		if err != nil {
			return nil, err
		}
		if err := v.postChaos(ctx, backend, "/admin/chaos", patch); err != nil {
			return nil, err
		}
		return map[string]any{"accepted": true, "backend": backend, "fault": fault}, nil
	case "clear_faults":
		for _, backend := range []string{"A", "B"} {
			if err := v.postChaos(ctx, backend, "/admin/chaos/reset", nil); err != nil {
				return nil, err
			}
		}
		return map[string]any{"cleared": true}, nil
	default:
		return nil, fmt.Errorf("unknown verifier action %q", stringValue(event, "action"))
	}
}

func (v *Verifier) probe(ctx context.Context, backend string) ProbeResult {
	return HTTPBackendProbe(v.HTTP, v.Config.BackendURLs, v.requestTimeout())(ctx, backend)
}

func (v *Verifier) postChaos(ctx context.Context, backend, path string, patch *chaos.Patch) error {
	base := strings.TrimRight(strings.TrimSpace(v.Config.BackendURLs[backend]), "/")
	if base == "" {
		return fmt.Errorf("backend %s URL is not configured", backend)
	}
	var body []byte
	var err error
	if patch != nil {
		body, err = json.Marshal(patch)
		if err != nil {
			return err
		}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("content-type", "application/json")
	request.Header.Set("x-admin-token", v.Config.AdminToken)
	request.Header.Set(clusterstate.BackendHeader, backend)
	request.Header.Set(clusterstate.EpochHeader, "1")
	client := v.HTTP
	if client == nil {
		client = &http.Client{Timeout: v.requestTimeout()}
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("inject fault on backend %s: %w", backend, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("backend %s rejected fault request with HTTP %d", backend, response.StatusCode)
	}
	return nil
}

func verifierFaultPatch(fault string, errorExpiresMS int64) (*chaos.Patch, error) {
	patch := &chaos.Patch{}
	switch fault {
	case "partial", "unsafe_standby":
		value := true
		patch.DBStall = &value
	case "blip":
		value := 100
		patch.ErrorPctExpiresMS = &errorExpiresMS
		patch.ErrorPct = &value
	case "lost_response":
		value := true
		patch.DropResponseOnce = &value
	default:
		return nil, fmt.Errorf("unsupported verifier fault %q", fault)
	}
	return patch, nil
}

func (v *Verifier) timeNow() time.Time {
	if v.Now != nil {
		return v.Now()
	}
	return time.Now()
}

func (v *Verifier) requestTimeout() time.Duration {
	if v.Config.ProbeTimeout > 0 {
		return v.Config.ProbeTimeout
	}
	if v.Timeout > 0 {
		return v.Timeout
	}
	return 2 * time.Second
}
