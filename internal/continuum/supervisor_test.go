package continuum

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ayush/slate/internal/clusterstate"
)

type probeScript struct {
	mu sync.Mutex
	n  map[string]int
	f  func(string, int) ProbeResult
}

func (p *probeScript) probe(_ context.Context, backend string) ProbeResult {
	p.mu.Lock()
	p.n[backend]++
	count := p.n[backend]
	p.mu.Unlock()
	return p.f(backend, count)
}

func (p *probeScript) count(backend string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.n[backend]
}

func healthyProbe() ProbeResult { return ProbeResult{Liveness: true, Ready: true} }
func unhealthyProbe() ProbeResult {
	return ProbeResult{Liveness: true, Ready: false, Reason: "injected not ready"}
}

func newScriptedController(t *testing.T, initial ControlState, probes int, script *probeScript) (*Controller, *MemoryControl) {
	t.Helper()
	control, err := NewMemoryControl(initial)
	if err != nil {
		t.Fatal(err)
	}
	controller := NewController(control, "local", "local", Policy{ConfirmationProbes: probes, ProbeIntervalMillis: 1}, script.probe, nil, nil)
	controller.Smoke = func(context.Context) bool { return true }
	return controller, control
}

func sample(t *testing.T, controller *Controller) {
	t.Helper()
	if err := controller.MonitorOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond)
}

func TestMonitorPromotesOnlyAfterDistinctConfirmationsAndReconciles(t *testing.T) {
	script := &probeScript{n: map[string]int{}, f: func(_ string, _ int) ProbeResult { return healthyProbe() }}
	script.f = func(backend string, _ int) ProbeResult {
		if backend == "A" {
			return unhealthyProbe()
		}
		return healthyProbe()
	}
	controller, control := newScriptedController(t, ControlState{ActiveBackend: "A", Epoch: 1}, 3, script)
	var reconciles atomic.Int32
	controller.Reconcile = func(context.Context) (bool, error) {
		reconciles.Add(1)
		return true, nil
	}
	for i := 0; i < 2; i++ {
		sample(t, controller)
		state, _ := control.Get(context.Background())
		if state.ActiveBackend != "A" || state.Epoch != 1 {
			t.Fatalf("promoted before confirmation %d: %+v", i+1, state)
		}
	}
	sample(t, controller)
	state, _ := control.Get(context.Background())
	if state.ActiveBackend != "B" || state.Epoch != 2 {
		t.Fatalf("route after confirmed failover = %+v", state)
	}
	status, err := controller.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Metrics.Failovers != 1 || status.Metrics.RecoveryMS < 0 || status.Metrics.Reconciled == nil || !*status.Metrics.Reconciled || reconciles.Load() != 1 {
		t.Fatalf("failover/reconciliation metrics = %+v, reconciles=%d", status.Metrics, reconciles.Load())
	}
	var recovered *Event
	for i := range status.Events {
		if status.Events[i].Type == "recovered" {
			recovered = &status.Events[i]
			break
		}
	}
	if recovered == nil || recovered.DurationMS != status.Metrics.RecoveryMS || !strings.Contains(recovered.Message, "from the first failed monitor probe") {
		t.Fatalf("recovery duration should match the first-probe metric and say where its clock starts: metric=%d event=%+v", status.Metrics.RecoveryMS, recovered)
	}
}

func TestMonitorSuppressesSharedDependencyFailure(t *testing.T) {
	script := &probeScript{n: map[string]int{}, f: func(string, int) ProbeResult { return unhealthyProbe() }}
	controller, control := newScriptedController(t, ControlState{ActiveBackend: "A", Epoch: 4}, 2, script)
	sample(t, controller)
	sample(t, controller)
	state, _ := control.Get(context.Background())
	status, _ := controller.Status(context.Background())
	if state.ActiveBackend != "A" || state.Epoch != 4 || status.Metrics.Failovers != 0 || status.Metrics.Suppressed != 1 {
		t.Fatalf("shared outage should not switch: state=%+v metrics=%+v", state, status.Metrics)
	}
}

func TestMonitorRechecksRecoveredActiveUnderIncidentLock(t *testing.T) {
	script := &probeScript{n: map[string]int{}, f: func(backend string, count int) ProbeResult {
		if backend == "A" && count == 1 {
			return unhealthyProbe()
		}
		return healthyProbe()
	}}
	controller, control := newScriptedController(t, ControlState{ActiveBackend: "A", Epoch: 1}, 1, script)
	if err := controller.MonitorOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, _ := control.Get(context.Background())
	status, _ := controller.Status(context.Background())
	if state.ActiveBackend != "A" || state.Epoch != 1 || status.Metrics.Failovers != 0 {
		t.Fatalf("recovered active backend was promoted: state=%+v metrics=%+v", state, status.Metrics)
	}
}

func TestFailedSmokeFreshlyProbesFormerBackendBeforeRollback(t *testing.T) {
	script := &probeScript{n: map[string]int{}, f: func(backend string, count int) ProbeResult {
		if backend == "A" && count <= 2 {
			return unhealthyProbe()
		}
		return healthyProbe()
	}}
	controller, control := newScriptedController(t, ControlState{ActiveBackend: "A", Epoch: 1}, 1, script)
	controller.Smoke = func(context.Context) bool { return false }
	if err := controller.MonitorOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, _ := control.Get(context.Background())
	if state.ActiveBackend != "A" || state.Epoch != 3 {
		t.Fatalf("freshly verified rollback should advance epoch to 3: %+v", state)
	}
	if script.count("A") != 3 {
		t.Fatalf("former backend was not freshly probed after smoke failure: A probes=%d", script.count("A"))
	}
}

func TestFailbackSmokeFailureRollsBackWithNewEpoch(t *testing.T) {
	script := &probeScript{n: map[string]int{}, f: func(string, int) ProbeResult { return healthyProbe() }}
	controller, control := newScriptedController(t, ControlState{ActiveBackend: "B", Epoch: 9}, 2, script)
	controller.Smoke = func(context.Context) bool { return false }
	if err := controller.Failback(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, _ := control.Get(context.Background())
	if state.ActiveBackend != "B" || state.Epoch != 11 {
		t.Fatalf("failed failback should return to B at a newer epoch, got %+v", state)
	}
}

func TestMonitorConfirmationCannotBeSpedUpByRapidManualCalls(t *testing.T) {
	script := &probeScript{n: map[string]int{}, f: func(backend string, _ int) ProbeResult {
		if backend == "A" {
			return unhealthyProbe()
		}
		return healthyProbe()
	}}
	controller, control := newScriptedController(t, ControlState{ActiveBackend: "A", Epoch: 1}, 3, script)
	controller.Policy.ProbeIntervalMillis = 500
	if err := controller.MonitorOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- controller.MonitorOnce(ctx) }()
	time.Sleep(40 * time.Millisecond)
	if script.count("A") != 1 {
		cancel()
		<-done
		t.Fatalf("rapid manual call caused another confirmation probe: %d", script.count("A"))
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled spacing wait should return context error")
	}
	state, _ := control.Get(context.Background())
	if state.ActiveBackend != "A" || state.Epoch != 1 {
		t.Fatalf("rapid manual call promoted route: %+v", state)
	}
}

func TestMemoryControlStaleWriterIsFencedAfterRollback(t *testing.T) {
	control, _ := NewMemoryControl(ControlState{ActiveBackend: "A", Epoch: 2})
	if ok, err := control.AcquireIncident(context.Background(), "rollback", time.Minute); err != nil || !ok {
		t.Fatalf("acquire lock: ok=%v err=%v", ok, err)
	}
	state, err := control.Promote(context.Background(), clusterstate.Writer{Backend: "A", Epoch: 2}, "B", "rollback")
	if err != nil {
		t.Fatal(err)
	}
	state, err = control.Promote(context.Background(), state.Writer(), "A", "rollback")
	if err != nil {
		t.Fatal(err)
	}
	if state.Epoch != 4 {
		t.Fatalf("rollback reused epoch instead of advancing it: %+v", state)
	}
	if err := control.WithFence(context.Background(), clusterstate.Writer{Backend: "A", Epoch: 2}, func() error { return nil }); err == nil {
		t.Fatal("pre-failover writer was accepted after rollback")
	}
}
