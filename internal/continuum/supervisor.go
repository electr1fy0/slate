package continuum

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const PolicyVersion = "intent-v1"

type Policy struct {
	Version             string `json:"version"`
	ConfirmationProbes  int    `json:"confirmation_probes"`
	ProbeIntervalMillis int    `json:"probe_interval_ms"`
}

type BackendStatus struct {
	ID       string `json:"id"`
	Healthy  bool   `json:"healthy"`
	Liveness bool   `json:"liveness"`
	Ready    bool   `json:"ready"`
	Reason   string `json:"reason"`
	URL      string `json:"url,omitempty"`
}

type Event struct {
	ID         string `json:"id" dynamodbav:"id"`
	At         string `json:"at" dynamodbav:"at"`
	Type       string `json:"type" dynamodbav:"type"`
	Message    string `json:"message" dynamodbav:"message"`
	Backend    string `json:"backend,omitempty" dynamodbav:"backend,omitempty"`
	Epoch      int64  `json:"epoch,omitempty" dynamodbav:"epoch,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty" dynamodbav:"duration_ms,omitempty"`
}

type Metrics struct {
	Failovers    int   `json:"failovers"`
	Suppressed   int   `json:"suppressed"`
	RecoveryMS   int64 `json:"recovery_ms"`
	Reconciled   *bool `json:"reconciled,omitempty"`
	FencedWrites *int  `json:"fenced_writes,omitempty"`
}

type Status struct {
	Mode          string          `json:"mode"`
	Region        string          `json:"region"`
	ActiveBackend string          `json:"active_backend"`
	Epoch         int64           `json:"epoch"`
	Paused        bool            `json:"paused"`
	Policy        Policy          `json:"policy"`
	Backends      []BackendStatus `json:"backends"`
	Events        []Event         `json:"events"`
	Metrics       Metrics         `json:"metrics"`
}

type ProbeResult struct {
	Liveness bool
	Ready    bool
	Reason   string
}

func (p ProbeResult) Healthy() bool { return p.Liveness && p.Ready }

type ProbeFunc func(context.Context, string) ProbeResult
type FaultInjector interface {
	Inject(context.Context, string, string) error
	Reset(context.Context) error
}
type ReconcileFunc func(context.Context) (bool, error)

type ActionRequest struct {
	Action  string `json:"action"`
	Backend string `json:"backend,omitempty"`
}

var allowedActions = map[string]bool{
	"crash": true, "partial": true, "blip": true, "unsafe_standby": true,
	"lost_response": true, "reset": true, "verify": true, "failback": true,
	"reconcile": true,
}

type Controller struct {
	Control   ControlPlane
	Mode      string
	Region    string
	Policy    Policy
	Probe     ProbeFunc
	Smoke     func(context.Context) bool
	Faults    FaultInjector
	Reconcile ReconcileFunc
	Now       func() time.Time
	NewID     func() string

	mu sync.RWMutex
	// monitorMu serializes scheduled and operator-triggered monitor passes. A
	// confirmation count must represent distinct observations, not overlapping
	// invocations of the same probe.
	monitorMu     sync.Mutex
	backendStatus map[string]BackendStatus
	events        []Event
	metrics       Metrics
	publicSmokeMS int64

	consecutiveUnhealthy int
	lastFailureAt        time.Time
	lastObservationAt    time.Time
	failureEpoch         int64
	lastSuppressedEpoch  int64
	lockLease            time.Duration
}

func NewController(control ControlPlane, mode, region string, policy Policy, probe ProbeFunc, faults FaultInjector, reconcile ReconcileFunc) *Controller {
	if policy.Version == "" {
		policy.Version = PolicyVersion
	}
	if policy.ConfirmationProbes <= 0 {
		policy.ConfirmationProbes = 3
	}
	if policy.ProbeIntervalMillis <= 0 {
		policy.ProbeIntervalMillis = 5000
	}
	if mode == "" {
		mode = "local"
	}
	return &Controller{Control: control, Mode: mode, Region: region, Policy: policy,
		Probe: probe, Faults: faults, Reconcile: reconcile,
		Now: time.Now, NewID: randomIDString, backendStatus: make(map[string]BackendStatus),
		lockLease: 2 * time.Minute}
}

func (c *Controller) Status(ctx context.Context) (Status, error) {
	state, err := c.Control.Get(ctx)
	if err != nil {
		return Status{}, err
	}
	if source, ok := c.Control.(interface{ FencedWrites() uint64 }); ok {
		fencedWrites := int(source.FencedWrites())
		c.mu.Lock()
		c.metrics.FencedWrites = &fencedWrites
		c.mu.Unlock()
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	backends := []BackendStatus{
		c.backendStatus["A"], c.backendStatus["B"],
	}
	for i, id := range []string{"A", "B"} {
		if backends[i].ID == "" {
			backends[i] = BackendStatus{ID: id, Reason: "not probed yet"}
		}
	}
	events := append([]Event{}, c.events...)
	return Status{Mode: c.Mode, Region: c.Region, ActiveBackend: state.ActiveBackend, Epoch: state.Epoch,
		Paused: state.Paused, Policy: c.Policy, Backends: backends, Events: events, Metrics: c.metrics}, nil
}

func (c *Controller) Action(ctx context.Context, request ActionRequest) (Status, error) {
	request.Action = strings.TrimSpace(strings.ToLower(request.Action))
	request.Backend = strings.ToUpper(strings.TrimSpace(request.Backend))
	if !allowedActions[request.Action] {
		return Status{}, fmt.Errorf("unsupported continuum action %q", request.Action)
	}
	state, err := c.Control.Get(ctx)
	if err != nil {
		return Status{}, err
	}
	backend := request.Backend
	if backend == "" {
		backend = state.ActiveBackend
		if request.Action == "unsafe_standby" {
			backend = otherBackend(state.ActiveBackend)
		}
	}
	if (request.Action == "crash" || request.Action == "partial" || request.Action == "blip" || request.Action == "unsafe_standby" || request.Action == "lost_response") && !validBackend(backend) {
		return Status{}, ErrInvalidBackend
	}

	switch request.Action {
	case "crash", "partial", "blip", "unsafe_standby", "lost_response":
		if c.Faults == nil {
			return Status{}, errors.New("fault injection is unavailable")
		}
		if err := c.Faults.Inject(ctx, request.Action, backend); err != nil {
			return Status{}, err
		}
		message := fmt.Sprintf("%s fault injected on backend %s", request.Action, backend)
		if request.Action == "unsafe_standby" {
			message = fmt.Sprintf("standby backend %s readiness disabled; promotion should be refused", backend)
		}
		c.appendEvent("fault", message, backend, state.Epoch)
	case "verify":
		if err := c.MonitorOnce(ctx); err != nil {
			return Status{}, err
		}
	case "reset":
		if c.Faults != nil {
			if err := c.Faults.Reset(ctx); err != nil {
				return Status{}, err
			}
		}
		if err := c.ProbeBoth(ctx); err != nil {
			return Status{}, err
		}
		c.mu.RLock()
		bothHealthy := c.backendStatus["A"].Healthy && c.backendStatus["B"].Healthy
		c.mu.RUnlock()
		if !bothHealthy {
			c.resetMonitor()
			c.appendEvent("reset_pending", "faults were cleared; waiting for both backends to pass deep health", "", state.Epoch)
			return c.Status(ctx)
		}
		if err := c.Failback(ctx); err != nil {
			return Status{}, err
		}
		c.resetMonitor()
		current, _ := c.Control.Get(ctx)
		c.appendEvent("reset", "faults cleared and both backends passed deep health", current.ActiveBackend, current.Epoch)
	case "failback":
		if err := c.Failback(ctx); err != nil {
			return Status{}, err
		}
	case "reconcile":
		if c.Reconcile == nil {
			return Status{}, errors.New("reconciliation is unavailable")
		}
		ok, err := c.Reconcile(ctx)
		if err != nil {
			return Status{}, err
		}
		c.mu.Lock()
		c.metrics.Reconciled = boolPointer(ok)
		c.mu.Unlock()
		c.appendEvent("reconcile", "downstream work reconciliation completed", state.ActiveBackend, state.Epoch)
	}
	return c.Status(ctx)
}

func (c *Controller) ProbeBoth(ctx context.Context) error {
	if c.Probe == nil {
		return errors.New("backend probe is unavailable")
	}
	type result struct {
		id string
		p  ProbeResult
	}
	results := make(chan result, 2)
	for _, id := range []string{"A", "B"} {
		go func(backend string) { results <- result{id: backend, p: c.Probe(ctx, backend)} }(id)
	}
	statuses := make(map[string]BackendStatus, 2)
	for range 2 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case r := <-results:
			statuses[r.id] = BackendStatus{ID: r.id, Healthy: r.p.Healthy(), Liveness: r.p.Liveness, Ready: r.p.Ready, Reason: r.p.Reason}
		}
	}
	c.mu.Lock()
	for id, status := range statuses {
		c.backendStatus[id] = status
	}
	c.mu.Unlock()
	return nil
}

// MonitorOnce observes both backends, treats a dual-backend failure as a
// shared dependency or control-path incident, and promotes only after the
// configured number of consecutive primary-specific failures.
func (c *Controller) MonitorOnce(ctx context.Context) error {
	c.monitorMu.Lock()
	defer c.monitorMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	interval := time.Duration(c.Policy.ProbeIntervalMillis) * time.Millisecond
	c.mu.RLock()
	lastObservation := c.lastObservationAt
	c.mu.RUnlock()
	if !lastObservation.IsZero() {
		remaining := interval - c.timeNow().Sub(lastObservation)
		if remaining > 0 {
			if err := wait(ctx, remaining); err != nil {
				return err
			}
		}
	}
	if err := c.ProbeBoth(ctx); err != nil {
		return err
	}
	c.mu.Lock()
	c.lastObservationAt = c.timeNow()
	c.mu.Unlock()
	state, err := c.Control.Get(ctx)
	if err != nil {
		return err
	}
	c.mu.RLock()
	active := c.backendStatus[state.ActiveBackend]
	standbyID := otherBackend(state.ActiveBackend)
	standby := c.backendStatus[standbyID]
	previousFailureEpoch := c.failureEpoch
	c.mu.RUnlock()
	if state.Paused {
		return nil
	}
	if previousFailureEpoch != 0 && previousFailureEpoch != state.Epoch {
		c.resetMonitor()
	}
	if active.Healthy {
		c.mu.RLock()
		hadCandidate := c.consecutiveUnhealthy > 0
		c.mu.RUnlock()
		if hadCandidate {
			c.suppressOnce(state.Epoch, "active backend recovered before the confirmation window; promotion suppressed", state.ActiveBackend)
		}
		c.resetMonitor()
		return nil
	}
	if !standby.Healthy {
		c.resetMonitor()
		c.suppressOnce(state.Epoch, "both backends failed deep health; shared dependency or verifier failure suspected", state.ActiveBackend)
		return nil
	}
	c.mu.Lock()
	if c.consecutiveUnhealthy == 0 {
		c.lastFailureAt = c.timeNow()
		c.failureEpoch = state.Epoch
	}
	c.consecutiveUnhealthy++
	count := c.consecutiveUnhealthy
	failureAt := c.lastFailureAt
	c.mu.Unlock()
	if count < c.Policy.ConfirmationProbes {
		return nil
	}
	owner := fmt.Sprintf("local-%s-e%d", state.ActiveBackend, state.Epoch)
	acquired, err := c.Control.AcquireIncident(ctx, owner, c.lockLease)
	if err != nil {
		return err
	}
	if !acquired {
		c.suppressOnce(state.Epoch, "promotion suppressed because another incident owns the lock", state.ActiveBackend)
		return nil
	}
	defer c.Control.ReleaseIncident(context.WithoutCancel(ctx), owner)
	current, err := c.Control.Get(ctx)
	if err != nil {
		return err
	}
	if current.ActiveBackend != state.ActiveBackend || current.Epoch != state.Epoch {
		c.resetMonitor()
		return nil
	}
	// Recheck both endpoints under the incident lock immediately before
	// changing the pointer. This suppresses a recovered transient and avoids
	// promoting a stale standby health observation.
	if err := c.ProbeBoth(ctx); err != nil {
		return err
	}
	c.mu.RLock()
	active = c.backendStatus[current.ActiveBackend]
	standby = c.backendStatus[standbyID]
	c.mu.RUnlock()
	if active.Healthy {
		c.suppressOnce(state.Epoch, "active backend recovered during the incident lock check; promotion suppressed", state.ActiveBackend)
		c.resetMonitor()
		return nil
	}
	if !standby.Healthy {
		c.suppressOnce(state.Epoch, "standby failed readiness recheck; route unchanged", standbyID)
		c.resetMonitor()
		return nil
	}
	promoted, err := c.Control.Promote(ctx, current.Writer(), standbyID, owner)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.metrics.Failovers++
	c.consecutiveUnhealthy = 0
	c.mu.Unlock()
	c.appendEvent("failover", fmt.Sprintf("backend %s promoted after %d consecutive failed probes", standbyID, count), standbyID, promoted.Epoch)

	smokeStart := c.timeNow()
	smokeOK := c.PublicSmoke(ctx)
	if smokeOK {
		recoveredAt := c.timeNow()
		recovery := recoveredAt.Sub(failureAt).Milliseconds()
		c.mu.Lock()
		c.metrics.RecoveryMS = recovery
		c.mu.Unlock()
		c.appendEventRecord(Event{Type: "recovered", Message: fmt.Sprintf("route recovery took %d ms from the first failed monitor probe; public health smoke took %d ms", recovery, recoveredAt.Sub(smokeStart).Milliseconds()),
			Backend: standbyID, Epoch: promoted.Epoch, DurationMS: recovery})
		c.reconcileAfterPromotion(ctx, standbyID, promoted.Epoch)
		return nil
	}
	// A failed post-promotion smoke test permits rollback only when the former
	// writer passes a fresh health probe. The route update itself advances the
	// epoch again; cached health from before the smoke is never used for rollback.
	if err := c.ProbeBoth(ctx); err != nil {
		c.appendEvent("smoke_failed", "public smoke failed and backend health could not be rechecked; route remains unchanged", standbyID, promoted.Epoch)
		return nil
	}
	c.mu.RLock()
	former := c.backendStatus[state.ActiveBackend]
	c.mu.RUnlock()
	if former.Healthy {
		rolledBack, rollbackErr := c.Control.Promote(ctx, promoted.Writer(), state.ActiveBackend, owner)
		if rollbackErr != nil {
			return rollbackErr
		}
		c.appendEvent("rollback", "public smoke failed; safely returned route to healthy former backend", state.ActiveBackend, rolledBack.Epoch)
	} else {
		c.appendEvent("smoke_failed", "public smoke failed and former backend is unhealthy; route remains on verified standby", standbyID, promoted.Epoch)
	}
	return nil
}

func (c *Controller) Failback(ctx context.Context) error {
	state, err := c.Control.Get(ctx)
	if err != nil {
		return err
	}
	if state.ActiveBackend == "A" {
		c.appendEvent("failback", "backend A is already active", "A", state.Epoch)
		return nil
	}
	owner := "local-failback-e" + fmt.Sprint(state.Epoch)
	acquired, err := c.Control.AcquireIncident(ctx, owner, c.lockLease)
	if err != nil {
		return err
	}
	if !acquired {
		return ErrIncidentLocked
	}
	defer c.Control.ReleaseIncident(context.WithoutCancel(ctx), owner)
	current, err := c.Control.Get(ctx)
	if err != nil {
		return err
	}
	if current.ActiveBackend != "B" || current.Epoch != state.Epoch {
		return ErrStalePromotion
	}
	for i := 0; i < c.Policy.ConfirmationProbes; i++ {
		if err := c.ProbeBoth(ctx); err != nil {
			return err
		}
		c.mu.RLock()
		aHealthy := c.backendStatus["A"].Healthy
		c.mu.RUnlock()
		if !aHealthy {
			return fmt.Errorf("failback refused: backend A failed readiness probe %d", i+1)
		}
		if i+1 < c.Policy.ConfirmationProbes {
			if err := wait(ctx, time.Duration(c.Policy.ProbeIntervalMillis)*time.Millisecond); err != nil {
				return err
			}
		}
	}
	updated, err := c.Control.Promote(ctx, current.Writer(), "A", owner)
	if err != nil {
		return err
	}
	c.appendEvent("failback", "backend A passed sustained health probes and is active", "A", updated.Epoch)
	if c.PublicSmoke(ctx) {
		c.appendEventRecord(Event{Type: "recovered", Message: "public health smoke succeeded after failback", Backend: "A", Epoch: updated.Epoch})
		return nil
	}
	if err := c.ProbeBoth(ctx); err != nil {
		c.appendEvent("smoke_failed", "failback smoke failed and backend health could not be rechecked; route remains on A", "A", updated.Epoch)
		return nil
	}
	c.mu.RLock()
	formerHealthy := c.backendStatus["B"].Healthy
	c.mu.RUnlock()
	if formerHealthy {
		rolledBack, rollbackErr := c.Control.Promote(ctx, updated.Writer(), "B", owner)
		if rollbackErr != nil {
			return rollbackErr
		}
		c.appendEvent("rollback", "failback smoke failed; safely returned route to healthy backend B", "B", rolledBack.Epoch)
	} else {
		c.appendEvent("smoke_failed", "failback smoke failed and backend B is unhealthy; route remains on verified A", "A", updated.Epoch)
	}
	return nil
}

func (c *Controller) reconcileAfterPromotion(ctx context.Context, backend string, epoch int64) {
	if c.Reconcile == nil {
		return
	}
	ok, err := c.Reconcile(ctx)
	c.mu.Lock()
	c.metrics.Reconciled = boolPointer(err == nil && ok)
	c.mu.Unlock()
	if err != nil {
		c.appendEvent("reconcile_failed", "downstream reconciliation failed: "+brief(err.Error()), backend, epoch)
		return
	}
	message := "downstream work reconciliation completed"
	if !ok {
		message = "downstream work reconciliation returned incomplete"
	}
	c.appendEvent("reconcile", message, backend, epoch)
}

func (c *Controller) PublicSmoke(ctx context.Context) bool {
	if c.Smoke != nil {
		return c.Smoke(ctx)
	}
	if c.Probe == nil {
		return false
	}
	state, err := c.Control.Get(ctx)
	if err != nil {
		return false
	}
	result := c.Probe(ctx, state.ActiveBackend)
	return result.Healthy()
}

func (c *Controller) Run(ctx context.Context) error {
	interval := time.Duration(c.Policy.ProbeIntervalMillis) * time.Millisecond
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if err := c.MonitorOnce(ctx); err != nil && ctx.Err() == nil {
		c.appendEvent("probe_error", "initial monitor probe failed: "+err.Error(), "", 0)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			probeCtx, cancel := context.WithTimeout(ctx, interval)
			err := c.MonitorOnce(probeCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				c.appendEvent("probe_error", "monitor probe failed: "+err.Error(), "", 0)
			}
		}
	}
}

func (c *Controller) LocalHandler(router http.Handler, adminToken string) http.Handler {
	controlHandler := c.HTTPHandler(adminToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, "/api")
			r = r2
		} else if r.URL.Path == "/api" {
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			r = r2
		}
		if r.URL.Path == "/continuum/status" || r.URL.Path == "/continuum/action" {
			controlHandler.ServeHTTP(w, r)
			return
		}
		router.ServeHTTP(w, r)
	})
}

func (c *Controller) HTTPHandler(adminToken string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /continuum/status", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, adminToken) {
			http.Error(w, "invalid admin token", http.StatusForbidden)
			return
		}
		status, err := c.Status(r.Context())
		if err != nil {
			http.Error(w, "continuum status unavailable", http.StatusServiceUnavailable)
			return
		}
		writeJSON(w, http.StatusOK, status)
	})
	mux.HandleFunc("POST /continuum/action", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, adminToken) {
			http.Error(w, "invalid admin token", http.StatusForbidden)
			return
		}
		var request ActionRequest
		body := http.MaxBytesReader(w, r.Body, 16*1024)
		decoder := json.NewDecoder(body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, "invalid action payload", http.StatusBadRequest)
			return
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			http.Error(w, "action payload must contain one JSON object", http.StatusBadRequest)
			return
		}
		status, err := c.Action(r.Context(), request)
		if err != nil {
			statusCode := http.StatusBadRequest
			if errors.Is(err, ErrIncidentLocked) || errors.Is(err, ErrStalePromotion) {
				statusCode = http.StatusConflict
			} else if r.Context().Err() != nil || strings.Contains(strings.ToLower(err.Error()), "unavailable") {
				statusCode = http.StatusServiceUnavailable
			}
			http.Error(w, err.Error(), statusCode)
			return
		}
		writeJSON(w, http.StatusOK, status)
	})
	return mux
}

func (c *Controller) appendEvent(kind, message, backend string, epoch int64) {
	c.appendEventRecord(Event{Type: kind, Message: message, Backend: backend, Epoch: epoch})
}

func (c *Controller) appendEventRecord(event Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if event.ID == "" {
		event.ID = "event-" + c.id()
	}
	if event.At == "" {
		event.At = c.timeNow().UTC().Format(time.RFC3339Nano)
	}
	c.events = append(c.events, event)
	if len(c.events) > 100 {
		c.events = append([]Event(nil), c.events[len(c.events)-100:]...)
	}
}

func (c *Controller) suppressOnce(epoch int64, message, backend string) {
	c.mu.Lock()
	if c.lastSuppressedEpoch == epoch {
		c.mu.Unlock()
		return
	}
	c.lastSuppressedEpoch = epoch
	c.metrics.Suppressed++
	c.mu.Unlock()
	c.appendEvent("suppressed", message, backend, epoch)
}

func (c *Controller) resetMonitor() {
	c.mu.Lock()
	c.consecutiveUnhealthy = 0
	c.lastFailureAt = time.Time{}
	c.failureEpoch = 0
	c.mu.Unlock()
}

func (c *Controller) timeNow() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Controller) id() string {
	if c.NewID != nil {
		return c.NewID()
	}
	return randomIDString()
}

func authorized(r *http.Request, token string) bool {
	if token == "" || len(r.Header.Get("x-admin-token")) != len(token) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("x-admin-token")), []byte(token)) == 1
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func otherBackend(id string) string {
	if id == "A" {
		return "B"
	}
	return "A"
}

func boolPointer(value bool) *bool { return &value }
