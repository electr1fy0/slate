package boards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	// BoardSnapshotSchemaVersion is the JSON schema version emitted by
	// ExportSnapshot and accepted by the persistence adapters.
	BoardSnapshotSchemaVersion = 1

	// DefaultSaveDebounce is long enough to coalesce the many element batches
	// produced by a single drawing gesture without making a normal reconnect
	// wait for persistence.
	DefaultSaveDebounce = 250 * time.Millisecond
)

var (
	// ErrSnapshotNotFound is returned by a store when a room has no saved
	// snapshot or when a requested history version does not exist.
	ErrSnapshotNotFound = errors.New("board snapshot not found")

	// ErrPersistenceUnavailable indicates that a persistence-backed operation
	// was requested from a manager that has no store configured.
	ErrPersistenceUnavailable = errors.New("board persistence unavailable")

	// ErrRoomNotFound is returned by export operations for an unknown in-memory
	// room. A persisted room is loaded by Resume before it is exported.
	ErrRoomNotFound = errors.New("board room not found")
)

// BoardSnapshotStore persists the latest exportable board snapshot and its
// immutable version history. Implementations must treat Save as replacing the
// current pointer and upserting the supplied version. Normal drawing saves run outside the room lock; explicit permission changes
// and restores keep the room serialized until Save succeeds.
//
// A nil store is a supported configuration: the manager remains an in-memory
// relay and all persistence hooks become no-ops. NoopSnapshotStore is provided
// for callers that prefer an explicit implementation.
type BoardSnapshotStore interface {
	Load(context.Context, string) (*BoardSnapshot, error)
	Save(context.Context, BoardSnapshot) error
	ListVersions(context.Context, string, int) ([]BoardVersion, error)
	LoadVersion(context.Context, string, int64) (*BoardSnapshot, error)
}

// NoopSnapshotStore preserves the legacy ephemeral behavior while satisfying
// BoardSnapshotStore. It is useful for dependency injection in local tests.
type NoopSnapshotStore struct{}

func (NoopSnapshotStore) Load(context.Context, string) (*BoardSnapshot, error) {
	return nil, ErrSnapshotNotFound
}

func (NoopSnapshotStore) Save(context.Context, BoardSnapshot) error { return nil }

func (NoopSnapshotStore) ListVersions(context.Context, string, int) ([]BoardVersion, error) {
	return nil, nil
}

func (NoopSnapshotStore) LoadVersion(context.Context, string, int64) (*BoardSnapshot, error) {
	return nil, ErrSnapshotNotFound
}

// RoomMetadata describes a room without exposing transport-specific peer
// handles. ParticipantCount is live in RoomSnapshot and is the count at the
// time of export/persistence in BoardSnapshot.
type RoomMetadata struct {
	RoomID           string    `json:"room_id"`
	Version          int64     `json:"version"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
	ParticipantCount int       `json:"participant_count"`
	StudentsDraw     bool      `json:"students_draw"`
}

// BoardSnapshot is the stable, export-friendly representation of board state.
// It intentionally contains only JSON-compatible element data and room
// metadata; connected peers and socket handles never enter a persisted or
// exported snapshot.
type BoardSnapshot struct {
	SchemaVersion int `json:"schema_version"`
	RoomMetadata
	Elements []map[string]any `json:"elements"`
}

// Clone returns a deep copy suitable for handing to a store or caller.
func (s BoardSnapshot) Clone() BoardSnapshot {
	copy := s
	copy.Elements = make([]map[string]any, 0, len(s.Elements))
	for _, element := range s.Elements {
		copy.Elements = append(copy.Elements, cloneMap(element))
	}
	return copy
}

// JSON returns the canonical indented export form. The format is deliberately
// independent of the WebSocket protocol so it can be written to a file,
// attached to an incident, or imported by a future tool.
func (s BoardSnapshot) JSON() ([]byte, error) {
	if s.SchemaVersion == 0 {
		s.SchemaVersion = BoardSnapshotSchemaVersion
	}
	return json.MarshalIndent(s, "", "  ")
}

// BoardVersion is a compact history entry. The full element payload is loaded
// only when Restore is requested.
type BoardVersion struct {
	RoomID       string    `json:"room_id"`
	Version      int64     `json:"version"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	ElementCount int       `json:"element_count"`
	SavedAt      time.Time `json:"saved_at,omitempty"`
}

type pendingBoardSave struct {
	snapshot   BoardSnapshot
	ctx        context.Context
	timer      *time.Timer
	generation uint64
}

// configurePersistence updates the optional persistence configuration. It is
// kept separate from manager.go's room operations so timer lifecycle and room
// locking stay easy to audit.
func (m *Manager) configurePersistence(store BoardSnapshotStore, debounce time.Duration) {
	if debounce <= 0 {
		debounce = DefaultSaveDebounce
	}
	m.persistenceMu.Lock()
	for _, pending := range m.pendingSaves {
		if pending.timer != nil {
			pending.timer.Stop()
		}
	}
	m.pendingSaves = make(map[string]*pendingBoardSave)
	m.persistence = store
	m.saveDebounce = debounce
	m.persistenceErrors = make(map[string]error)
	m.persistenceMu.Unlock()
}

func (m *Manager) persistenceStore() BoardSnapshotStore {
	m.persistenceMu.Lock()
	store := m.persistence
	m.persistenceMu.Unlock()
	return store
}

func (m *Manager) scheduleSnapshotSave(snapshot BoardSnapshot, originatingCtx context.Context) {
	m.persistenceMu.Lock()
	if m.persistence == nil {
		m.persistenceMu.Unlock()
		return
	}
	previous := m.pendingSaves[snapshot.RoomID]
	if previous != nil && previous.timer != nil {
		previous.timer.Stop()
	}
	if originatingCtx == nil && previous != nil {
		// Resume may refresh a pending snapshot after loading durable room
		// state. Keep the writer from the mutation that dirtied it; the join
		// request's newer epoch must not authorize that older write.
		originatingCtx = previous.ctx
	}
	if originatingCtx == nil {
		originatingCtx = context.Background()
	}
	saveCtx := context.WithoutCancel(originatingCtx)
	m.nextSaveGeneration++
	generation := m.nextSaveGeneration
	pending := &pendingBoardSave{snapshot: snapshot.Clone(), ctx: saveCtx, generation: generation}
	delay := m.saveDebounce
	pending.timer = time.AfterFunc(delay, func() {
		m.flushScheduledSnapshot(snapshot.RoomID, generation)
	})
	m.pendingSaves[snapshot.RoomID] = pending
	m.persistenceMu.Unlock()
}

func (m *Manager) flushScheduledSnapshot(roomID string, generation uint64) {
	m.persistenceMu.Lock()
	pending := m.pendingSaves[roomID]
	if pending == nil || pending.generation != generation {
		m.persistenceMu.Unlock()
		return
	}
	delete(m.pendingSaves, roomID)
	snapshot := pending.snapshot.Clone()
	m.persistenceMu.Unlock()

	if err := m.persistSnapshot(pending.ctx, snapshot); err != nil {
		m.recordPersistenceError(roomID, err)
		m.retainFailedSnapshot(snapshot, pending.ctx)
	}
}

func (m *Manager) persistSnapshot(ctx context.Context, snapshot BoardSnapshot) error {
	store := m.persistenceStore()
	if store == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// A store can be shared by several manager goroutines. Serializing writes
	// also keeps a later debounced revision from overtaking an earlier one.
	m.persistenceWriteMu.Lock()
	defer m.persistenceWriteMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := store.Save(ctx, snapshot.Clone()); err != nil {
		return err
	}
	m.recordPersistenceError(snapshot.RoomID, nil)
	return nil
}

func (m *Manager) recordPersistenceError(roomID string, err error) {
	m.persistenceMu.Lock()
	defer m.persistenceMu.Unlock()
	if m.persistenceErrors == nil {
		m.persistenceErrors = make(map[string]error)
	}
	if err == nil {
		delete(m.persistenceErrors, roomID)
		return
	}
	m.persistenceErrors[roomID] = err
}

// LastPersistenceError returns the most recent asynchronous save error for a
// room. A successful explicit save clears it. The returned error is safe to
// inspect but should not be mutated by callers.
func (m *Manager) LastPersistenceError(roomID string) error {
	m.persistenceMu.Lock()
	defer m.persistenceMu.Unlock()
	return m.persistenceErrors[roomID]
}

// Resume loads the persisted current snapshot exactly once for the active
// in-memory room. It is safe for concurrent joins: one caller performs the
// store read and the others wait for the same result. A missing snapshot is a
// normal first-use condition. Other store errors are returned so the protocol
// can leave the connection open and let the client retry join.
func (m *Manager) Resume(ctx context.Context, roomID string) error {
	if m == nil {
		return ErrPersistenceUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		m.mu.Lock()
		r := m.roomLocked(roomID)
		if r.loaded {
			m.mu.Unlock()
			return nil
		}
		if r.loading {
			done := r.loadDone
			m.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		r.loading = true
		r.loadDone = make(chan struct{})
		done := r.loadDone
		store := m.persistenceStore()
		m.mu.Unlock()

		var (
			loaded *BoardSnapshot
			err    error
		)
		if store != nil {
			loaded, err = store.Load(ctx, roomID)
			if errors.Is(err, ErrSnapshotNotFound) {
				err = nil
				loaded = nil
			}
		}
		if err == nil {
			err = ctx.Err()
		}
		if err == nil && loaded != nil {
			err = validateSnapshot(roomID, *loaded)
		}

		m.mu.Lock()
		shouldRefreshPending := false
		if current := m.rooms[roomID]; current == r && err == nil {
			if loaded != nil {
				m.mergeLoadedSnapshotLocked(roomID, r, *loaded)
				shouldRefreshPending = true
			}
			r.loaded = true
		}
		if current := m.rooms[roomID]; current == r {
			r.loading = false
			r.loadDone = nil
		}
		close(done)
		m.mu.Unlock()
		if shouldRefreshPending && m.hasPendingSave(roomID) {
			if snapshot, ok := m.exportSnapshotIfPresent(roomID); ok {
				m.scheduleSnapshotSave(snapshot, nil)
			}
		}
		return err
	}
}

// SaveSnapshot performs an explicit, immediate save hook. It is safe to call
// during graceful shutdown or after a drawing gesture. A room whose last peer
// disconnected can still be flushed because the debounced snapshot is kept
// independently of the live room map.
func (m *Manager) SaveSnapshot(ctx context.Context, roomID string) error {
	return m.Flush(ctx, roomID)
}

// Flush cancels a room's pending debounce and writes its newest available
// snapshot immediately. If the room was already removed, a pending debounced
// snapshot is used. With no persistence configured it is a no-op.
func (m *Manager) Flush(ctx context.Context, roomID string) error {
	if m == nil || m.persistenceStore() == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	pending := m.takePendingSave(roomID)
	saveCtx := ctx
	if pending != nil && pending.ctx != nil {
		// The pending snapshot belongs to its originating routed writer. A
		// later flush request must not refresh its epoch or backend identity.
		saveCtx = flushContext{Context: ctx, writer: pending.ctx}
	}
	snapshot, ok := m.exportSnapshotIfPresent(roomID)
	if !ok {
		if pending == nil {
			return nil
		}
		snapshot = pending.snapshot
	}
	if err := m.persistSnapshot(saveCtx, snapshot); err != nil {
		m.recordPersistenceError(roomID, err)
		m.retainFailedSnapshot(snapshot, saveCtx)
		return err
	}
	return nil
}

// FlushAll writes all current rooms and any pending snapshots left by rooms
// whose peers have disconnected. It is the recommended shutdown hook.
func (m *Manager) FlushAll(ctx context.Context) error {
	if m == nil || m.persistenceStore() == nil {
		return nil
	}
	ids := make(map[string]struct{})
	m.mu.RLock()
	for roomID := range m.rooms {
		ids[roomID] = struct{}{}
	}
	m.mu.RUnlock()
	m.persistenceMu.Lock()
	for roomID := range m.pendingSaves {
		ids[roomID] = struct{}{}
	}
	m.persistenceMu.Unlock()
	ordered := make([]string, 0, len(ids))
	for roomID := range ids {
		ordered = append(ordered, roomID)
	}
	sort.Strings(ordered)
	for _, roomID := range ordered {
		if err := m.Flush(ctx, roomID); err != nil {
			return err
		}
	}
	return nil
}

// ListVersions lists persisted board revisions from newest to oldest. The
// default store-independent behavior is an empty list when persistence is not
// configured.
func (m *Manager) ListVersions(ctx context.Context, roomID string, limit int) ([]BoardVersion, error) {
	store := m.persistenceStore()
	if store == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return store.ListVersions(ctx, roomID, limit)
}

// Restore replaces the live element set with a historical revision and saves
// the result as a new monotonically increasing revision. Existing peers remain
// attached and receive a restored frame so clients replace the scene even when
// historical elements have lower per-element versions.
func (m *Manager) Restore(ctx context.Context, roomID string, version int64) (RoomSnapshot, error) {
	m.controlMu.Lock()
	defer m.controlMu.Unlock()
	store := m.persistenceStore()
	if store == nil {
		return RoomSnapshot{}, ErrPersistenceUnavailable
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := m.Resume(ctx, roomID); err != nil {
		return RoomSnapshot{}, err
	}
	historical, err := store.LoadVersion(ctx, roomID, version)
	if err != nil {
		return RoomSnapshot{}, err
	}
	if historical == nil {
		return RoomSnapshot{}, ErrSnapshotNotFound
	}
	if err := validateSnapshot(roomID, *historical); err != nil {
		return RoomSnapshot{}, err
	}
	if historical.Version != version {
		return RoomSnapshot{}, fmt.Errorf("snapshot revision does not match requested version")
	}
	// Discard the pre-restore debounce before taking the new live snapshot. A
	// concurrent element update after this point gets its own newer debounce.
	previousPending := m.takePendingSave(roomID)

	m.mu.Lock()
	r := m.roomLocked(roomID)
	previousElements, previousOrder := r.elements, r.elementOrder
	previousVersion, previousCreated, previousUpdated, previousLoaded := r.version, r.createdAt, r.updatedAt, r.loaded
	nextVersion := r.version + 1
	if historical.Version >= nextVersion {
		nextVersion = historical.Version + 1
	}
	now := m.nowLocked()
	createdAt := r.createdAt
	if createdAt.IsZero() {
		createdAt = historical.CreatedAt
	}
	if createdAt.IsZero() {
		createdAt = now
	}
	r.elements = make(map[string]map[string]any)
	r.elementOrder = nil
	for _, element := range historical.Elements {
		m.absorbElementLocked(r, element)
	}
	r.loaded = true
	r.version = nextVersion
	r.createdAt = createdAt
	r.updatedAt = now
	snapshot := m.boardSnapshotLocked(roomID, r)
	result := m.roomSnapshotLocked(roomID, r)
	// Keep updates serialized until persistence succeeds. A failed restore
	// cannot replace the live canvas or apply later through an automatic save.
	if err := m.persistSnapshot(ctx, snapshot); err != nil {
		r.elements, r.elementOrder = previousElements, previousOrder
		r.version, r.createdAt, r.updatedAt, r.loaded = previousVersion, previousCreated, previousUpdated, previousLoaded
		result = m.roomSnapshotLocked(roomID, r)
		m.mu.Unlock()
		m.recordPersistenceError(roomID, err)
		if previousPending != nil {
			m.retainFailedSnapshot(previousPending.snapshot, previousPending.ctx)
		}
		return result, err
	}
	m.mu.Unlock()
	m.Broadcast(ctx, roomID, map[string]any{
		"type": "restored", "elements": snapshot.Elements, "version": snapshot.Version,
	}, nil)
	return result, nil
}

// ExportSnapshot returns an export-safe copy that contains no peer/socket
// information. It does not implicitly load persistence; call Resume first
// when exporting a room that may only exist in the backing store.
func (m *Manager) ExportSnapshot(roomID string) (BoardSnapshot, error) {
	if m == nil {
		return BoardSnapshot{}, ErrRoomNotFound
	}
	snapshot, ok := m.exportSnapshotIfPresent(roomID)
	if !ok {
		return BoardSnapshot{}, ErrRoomNotFound
	}
	return snapshot, nil
}

// ExportSnapshotJSON returns the canonical JSON export for a live room.
func (m *Manager) ExportSnapshotJSON(roomID string) ([]byte, error) {
	snapshot, err := m.ExportSnapshot(roomID)
	if err != nil {
		return nil, err
	}
	return snapshot.JSON()
}

// Metadata returns live room metadata without exposing peer handles or board
// element payloads.
func (m *Manager) Metadata(roomID string) (RoomMetadata, bool) {
	if m == nil {
		return RoomMetadata{}, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := m.rooms[roomID]
	if r == nil {
		return RoomMetadata{}, false
	}
	return RoomMetadata{
		RoomID:           roomID,
		Version:          r.version,
		CreatedAt:        r.createdAt,
		UpdatedAt:        r.updatedAt,
		ParticipantCount: len(r.peers),
		StudentsDraw:     r.studentsDraw,
	}, true
}

func (m *Manager) exportSnapshotIfPresent(roomID string) (BoardSnapshot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := m.rooms[roomID]
	if r == nil {
		return BoardSnapshot{}, false
	}
	return m.boardSnapshotLocked(roomID, r), true
}

func (m *Manager) takePendingSave(roomID string) *pendingBoardSave {
	m.persistenceMu.Lock()
	defer m.persistenceMu.Unlock()
	pending := m.pendingSaves[roomID]
	if pending == nil {
		return nil
	}
	if pending.timer != nil {
		pending.timer.Stop()
	}
	delete(m.pendingSaves, roomID)
	return pending
}

func (m *Manager) cancelPendingSave(roomID string) {
	m.persistenceMu.Lock()
	defer m.persistenceMu.Unlock()
	if pending := m.pendingSaves[roomID]; pending != nil {
		if pending.timer != nil {
			pending.timer.Stop()
		}
		delete(m.pendingSaves, roomID)
	}
}

func (m *Manager) hasPendingSave(roomID string) bool {
	m.persistenceMu.Lock()
	defer m.persistenceMu.Unlock()
	_, ok := m.pendingSaves[roomID]
	return ok
}

// retainFailedSnapshot keeps a failed snapshot available for a later explicit
// flush without inventing a fresh writer or retrying a permanently stale
// epoch on a timer. A newer mutation's pending snapshot takes precedence.
func (m *Manager) retainFailedSnapshot(snapshot BoardSnapshot, ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	saveCtx := context.WithoutCancel(ctx)
	m.persistenceMu.Lock()
	defer m.persistenceMu.Unlock()
	if m.persistence == nil || m.pendingSaves[snapshot.RoomID] != nil {
		return
	}
	m.nextSaveGeneration++
	m.pendingSaves[snapshot.RoomID] = &pendingBoardSave{
		snapshot: snapshot.Clone(), ctx: saveCtx, generation: m.nextSaveGeneration,
	}
}

func (m *Manager) mergeLoadedSnapshotLocked(roomID string, r *room, loaded BoardSnapshot) {
	r.studentsDraw = loaded.StudentsDraw
	if loaded.SchemaVersion == 0 {
		loaded.SchemaVersion = BoardSnapshotSchemaVersion
	}
	if loaded.RoomID == "" {
		loaded.RoomID = roomID
	}
	if !loaded.CreatedAt.IsZero() && (r.createdAt.IsZero() || loaded.CreatedAt.Before(r.createdAt)) {
		r.createdAt = loaded.CreatedAt
	}
	if loaded.UpdatedAt.After(r.updatedAt) {
		r.updatedAt = loaded.UpdatedAt
	}
	if loaded.Version > r.version {
		r.version = loaded.Version
	}
	if len(r.elements) == 0 {
		r.elements = make(map[string]map[string]any)
		r.elementOrder = nil
	}
	for _, element := range loaded.Elements {
		m.absorbElementLocked(r, element)
	}
}

func (m *Manager) boardSnapshotLocked(roomID string, r *room) BoardSnapshot {
	elements := make([]map[string]any, 0, len(r.elements))
	for _, id := range r.elementOrder {
		if element := r.elements[id]; element != nil {
			elements = append(elements, cloneMap(element))
		}
	}
	return BoardSnapshot{
		SchemaVersion: BoardSnapshotSchemaVersion,
		RoomMetadata: RoomMetadata{
			RoomID:           roomID,
			Version:          r.version,
			CreatedAt:        r.createdAt,
			UpdatedAt:        r.updatedAt,
			ParticipantCount: len(r.peers),
			StudentsDraw:     r.studentsDraw,
		},
		Elements: elements,
	}
}

func (m *Manager) roomSnapshotLocked(roomID string, r *room) RoomSnapshot {
	snapshot := RoomSnapshot{
		RoomMetadata: RoomMetadata{
			RoomID:           roomID,
			Version:          r.version,
			CreatedAt:        r.createdAt,
			UpdatedAt:        r.updatedAt,
			ParticipantCount: len(r.peers),
			StudentsDraw:     r.studentsDraw,
		},
		Peers:    make([]Peer, 0, len(r.peers)),
		Elements: make([]map[string]any, 0, len(r.elements)),
	}
	for _, socket := range r.peerOrder {
		if peer := r.peers[socket]; peer != nil {
			snapshot.Peers = append(snapshot.Peers, *clonePeer(peer))
		}
	}
	for _, id := range r.elementOrder {
		if element := r.elements[id]; element != nil {
			snapshot.Elements = append(snapshot.Elements, cloneMap(element))
		}
	}
	return snapshot
}

func (m *Manager) absorbElementLocked(r *room, element map[string]any) bool {
	id, ok := stringValue(element["id"])
	if !ok || id == "" {
		return false
	}
	version, ok := versionValue(element["version"])
	if !ok {
		return false
	}
	current, exists := r.elements[id]
	if exists {
		currentVersion, currentOK := versionValue(current["version"])
		if !currentOK {
			currentVersion = 0
		}
		if version < currentVersion {
			return false
		}
		if version == currentVersion {
			incomingNonce, incomingOK := versionValue(element["versionNonce"])
			currentNonce, currentOK := versionValue(current["versionNonce"])
			if !incomingOK || !currentOK || incomingNonce >= currentNonce {
				return false
			}
		}
	}
	r.elements[id] = cloneMap(element)
	if !exists {
		r.elementOrder = append(r.elementOrder, id)
	}
	return true
}

type flushContext struct {
	context.Context
	writer context.Context
}

func (c flushContext) Value(key any) any { return c.writer.Value(key) }

func validateSnapshot(roomID string, snapshot BoardSnapshot) error {
	if snapshot.RoomID != "" && snapshot.RoomID != roomID {
		return fmt.Errorf("snapshot belongs to another room")
	}
	if snapshot.SchemaVersion < 0 || snapshot.SchemaVersion > BoardSnapshotSchemaVersion {
		return fmt.Errorf("unsupported board snapshot schema %d", snapshot.SchemaVersion)
	}
	if snapshot.Version < 0 || snapshot.Version == math.MaxInt64 {
		return fmt.Errorf("invalid board snapshot revision")
	}
	return nil
}

func (m *Manager) nowLocked() time.Time {
	if m.now == nil {
		return time.Now()
	}
	return m.now()
}

var _ BoardSnapshotStore = NoopSnapshotStore{}
