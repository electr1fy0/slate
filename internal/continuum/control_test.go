package continuum

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ayush/slate/internal/clusterstate"
)

func TestMemoryControlFencesStaleWritersAndMonotonicRollback(t *testing.T) {
	ctx := context.Background()
	control, err := NewMemoryControl(ControlState{ActiveBackend: "A", Epoch: 7})
	if err != nil {
		t.Fatal(err)
	}
	acquired, err := control.AcquireIncident(ctx, "incident-1", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("acquire incident: acquired=%v err=%v", acquired, err)
	}
	duplicate, err := control.AcquireIncident(ctx, "incident-1", time.Minute)
	if err != nil || duplicate {
		t.Fatalf("duplicate owner acquired the lock: acquired=%v err=%v", duplicate, err)
	}

	toB, err := control.Promote(ctx, clusterstate.Writer{Backend: "A", Epoch: 7}, "B", "incident-1")
	if err != nil || toB.ActiveBackend != "B" || toB.Epoch != 8 {
		t.Fatalf("promote A to B: state=%+v err=%v", toB, err)
	}
	backToA, err := control.Promote(ctx, toB.Writer(), "A", "incident-1")
	if err != nil || backToA.ActiveBackend != "A" || backToA.Epoch != 9 {
		t.Fatalf("rollback B to A: state=%+v err=%v", backToA, err)
	}
	if _, err := control.Promote(ctx, clusterstate.Writer{Backend: "A", Epoch: 7}, "B", "incident-1"); !errors.Is(err, ErrStalePromotion) {
		t.Fatalf("stale pointer promotion error = %v, want ErrStalePromotion", err)
	}
	callbackRan := false
	if err := control.WithFence(ctx, clusterstate.Writer{Backend: "A", Epoch: 7}, func() error {
		callbackRan = true
		return nil
	}); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("stale writer fence error = %v, want ErrFenced", err)
	}
	if callbackRan {
		t.Fatal("stale writer callback ran")
	}
	if count := control.FencedWrites(); count != 1 {
		t.Fatalf("fenced writes = %d, want 1", count)
	}
	if err := control.ReleaseIncident(ctx, "incident-1"); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryControlSerializesCommitWithPromotion(t *testing.T) {
	ctx := context.Background()
	control, err := NewMemoryControl(ControlState{ActiveBackend: "A", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := control.AcquireIncident(ctx, "promoter", time.Minute); err != nil || !ok {
		t.Fatalf("acquire lock: ok=%v err=%v", ok, err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	commitDone := make(chan error, 1)
	go func() {
		commitDone <- control.WithFence(ctx, clusterstate.Writer{Backend: "A", Epoch: 1}, func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	promotionDone := make(chan error, 1)
	go func() {
		_, err := control.Promote(ctx, clusterstate.Writer{Backend: "A", Epoch: 1}, "B", "promoter")
		promotionDone <- err
	}()
	close(release)
	if err := <-commitDone; err != nil {
		t.Fatalf("in-flight commit failed: %v", err)
	}
	if err := <-promotionDone; err != nil {
		t.Fatalf("promotion failed: %v", err)
	}
	if err := control.WithFence(ctx, clusterstate.Writer{Backend: "A", Epoch: 1}, func() error { return nil }); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("old writer was allowed after promotion: %v", err)
	}
}
