package continuum

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ayush/slate/internal/clusterstate"
)

var (
	ErrControlNotFound = errors.New("continuum control record not found")
	ErrIncidentLocked  = errors.New("continuum incident lock is held")
	ErrStalePromotion  = errors.New("continuum promotion is based on a stale epoch")
	ErrInvalidBackend  = errors.New("backend must be A or B")
	ErrControlPaused   = errors.New("continuum control plane is paused")
)

// ControlState is the authoritative route pointer. Epoch is monotonic: every
// promotion, rollback, and failback advances it.
type ControlState struct {
	ActiveBackend string `json:"active_backend" dynamodbav:"active_backend"`
	Epoch         int64  `json:"epoch" dynamodbav:"epoch"`
	Paused        bool   `json:"paused" dynamodbav:"paused"`
}

func (s ControlState) Writer() clusterstate.Writer {
	return clusterstate.Writer{Backend: s.ActiveBackend, Epoch: s.Epoch}
}

// ControlPlane is the shared routing pointer and incident lease contract.
// Implementations must never use DynamoDB TTL deletion to establish lease
// expiry; expiresAt is compared explicitly on every lock operation.
type ControlPlane interface {
	clusterstate.FenceGuard
	Get(context.Context) (ControlState, error)
	AcquireIncident(context.Context, string, time.Duration) (bool, error)
	ReleaseIncident(context.Context, string) error
	Promote(context.Context, clusterstate.Writer, string, string) (ControlState, error)
	SetPaused(context.Context, bool) error
}

// MemoryControl implements a route pointer and commit fence with one mutex.
// WithFence holds a read lock during the write callback, while promotion takes
// the write lock, so a stale commit cannot cross a completed promotion.
type MemoryControl struct {
	mu          sync.RWMutex
	state       ControlState
	lockOwner   string
	lockExpires time.Time
	fenced      atomic.Uint64
	now         func() time.Time
}

func NewMemoryControl(initial ControlState) (*MemoryControl, error) {
	if !validBackend(initial.ActiveBackend) {
		return nil, ErrInvalidBackend
	}
	if initial.Epoch < 1 {
		initial.Epoch = 1
	}
	return &MemoryControl{state: initial, now: time.Now}, nil
}

func (c *MemoryControl) Get(ctx context.Context) (ControlState, error) {
	if err := ctx.Err(); err != nil {
		return ControlState{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state, nil
}

func (c *MemoryControl) WithFence(ctx context.Context, writer clusterstate.Writer, commit func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.state.Paused || c.state.ActiveBackend != writer.Backend || c.state.Epoch != writer.Epoch {
		c.fenced.Add(1)
		return clusterstate.ErrFenced
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return commit()
}

func (c *MemoryControl) FencedWrites() uint64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.fenced.Load()
}

func (c *MemoryControl) AcquireIncident(ctx context.Context, owner string, lease time.Duration) (bool, error) {
	if owner == "" || lease <= 0 {
		return false, fmt.Errorf("owner and positive lease are required")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.timeNow()
	// A duplicate execution for the same incident is still a second executor;
	// only one holder may enter the critical section at a time.
	if c.lockOwner != "" && c.lockExpires.After(now) {
		return false, nil
	}
	c.lockOwner = owner
	c.lockExpires = now.Add(lease)
	return true, nil
}

func (c *MemoryControl) ReleaseIncident(ctx context.Context, owner string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lockOwner == owner {
		c.lockOwner = ""
		c.lockExpires = time.Time{}
	}
	return nil
}

func (c *MemoryControl) Promote(ctx context.Context, expected clusterstate.Writer, target, owner string) (ControlState, error) {
	if !validBackend(target) {
		return ControlState{}, ErrInvalidBackend
	}
	if err := ctx.Err(); err != nil {
		return ControlState{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.timeNow()
	if c.lockOwner != owner || owner == "" || !c.lockExpires.After(now) {
		return c.state, ErrIncidentLocked
	}
	if c.state.Paused {
		return c.state, ErrControlPaused
	}
	if c.state.ActiveBackend != expected.Backend || c.state.Epoch != expected.Epoch {
		// Treat an already-applied response-lost request as success only while
		// the same lease still owns the operation and the epoch matches exactly.
		if c.state.ActiveBackend == target && c.state.Epoch == expected.Epoch+1 {
			return c.state, nil
		}
		return c.state, ErrStalePromotion
	}
	if expected.Epoch < 1 || c.state.Epoch == int64(^uint64(0)>>1) {
		return c.state, fmt.Errorf("invalid or exhausted epoch")
	}
	c.state.ActiveBackend = target
	c.state.Epoch++
	return c.state, nil
}

func (c *MemoryControl) SetPaused(ctx context.Context, paused bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.Paused == paused {
		return nil
	}
	if c.state.Epoch == int64(^uint64(0)>>1) {
		return fmt.Errorf("epoch exhausted")
	}
	c.state.Paused = paused
	c.state.Epoch++
	return nil
}

func (c *MemoryControl) timeNow() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func validBackend(id string) bool { return id == "A" || id == "B" }
