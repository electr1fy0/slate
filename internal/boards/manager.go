// Package boards contains the in-memory relay used by classroom boards.
//
// A room keeps the newest revision of each element and the currently connected
// peers. The manager is safe for use by several HTTP/WebSocket goroutines. A
// persistence adapter can be injected for continuity across reconnects and
// process restarts; NewManager keeps the legacy process-local behavior.
package boards

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"time"
)

const (
	// WebSocket close codes used by the board protocol.
	CloseInvalidToken    = 4401
	CloseIdleTimeout     = 4402
	CloseOriginForbidden = 4403
	ClosePayloadTooLarge = 1009

	ErrorMalformedJSON   = "MALFORMED_JSON"
	ErrorFrameNotObject  = "FRAME_NOT_OBJECT"
	ErrorUnknownType     = "UNKNOWN_MESSAGE_TYPE"
	ErrorBadElements     = "BAD_ELEMENTS_PAYLOAD"
	ErrorPayloadTooLarge = "PAYLOAD_TOO_LARGE"
	ErrorPersistence     = "PERSISTENCE_ERROR"
)

// Peer is the public representation sent in init, peer-joined, peer-left and
// cursor frames. LastSeen is for server-side eviction and is never serialized.
type Peer struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Role     string         `json:"role"`
	Cursor   map[string]any `json:"cursor"`
	LastSeen time.Time      `json:"-"`
}

type room struct {
	peers        map[any]*Peer
	peerOrder    []any
	elements     map[string]map[string]any
	elementOrder []string
	createdAt    time.Time
	updatedAt    time.Time
	version      int64
	loaded       bool
	loading      bool
	loadDone     chan struct{}
	studentsDraw bool
}

// RoomSnapshot is a race-free copy of a room. Slices and maps in the result
// can be safely modified by the caller.
type RoomSnapshot struct {
	RoomMetadata
	Peers    []Peer
	Elements []map[string]any
}

// Manager stores rooms and serializes access to all room state.
type Manager struct {
	mu        sync.RWMutex
	controlMu sync.Mutex
	rooms     map[string]*room
	now       func() time.Time

	persistenceMu      sync.Mutex
	persistence        BoardSnapshotStore
	saveDebounce       time.Duration
	pendingSaves       map[string]*pendingBoardSave
	nextSaveGeneration uint64
	persistenceErrors  map[string]error
	persistenceWriteMu sync.Mutex
}

// NewManager constructs an empty board manager.
func NewManager() *Manager {
	return NewManagerWithPersistence(nil, 0)
}

// NewManagerWithPersistence constructs a manager with optional snapshot
// persistence. The debounce controls automatic saves after accepted element
// batches; use SaveSnapshot or Flush for an explicit save hook.
func NewManagerWithPersistence(store BoardSnapshotStore, debounce time.Duration) *Manager {
	manager := &Manager{
		rooms:             make(map[string]*room),
		now:               time.Now,
		pendingSaves:      make(map[string]*pendingBoardSave),
		persistenceErrors: make(map[string]error),
	}
	manager.configurePersistence(store, debounce)
	return manager
}

// SetPersistence replaces the optional store and debounce configuration. It is
// intended for startup wiring before clients connect; pending saves from the
// previous store are cancelled.
func (m *Manager) SetPersistence(store BoardSnapshotStore, debounce time.Duration) {
	if m == nil {
		return
	}
	m.configurePersistence(store, debounce)
}

func (m *Manager) roomLocked(id string) *room {
	r := m.rooms[id]
	if r == nil {
		now := m.nowLocked()
		r = &room{
			peers:     make(map[any]*Peer),
			elements:  make(map[string]map[string]any),
			createdAt: now,
			updatedAt: now,
		}
		m.rooms[id] = r
	}
	return r
}

func normaliseName(name string) string {
	if name == "" {
		name = "Guest"
	}
	if len([]rune(name)) > 40 {
		name = string([]rune(name)[:40])
	}
	return name
}

func normaliseRole(role string) string {
	if role == "teacher" {
		return role
	}
	return "student"
}

// comparableSocket prevents a hard-to-diagnose panic if an integration passes
// a non-comparable value (the normal adapters are pointers).
func comparableSocket(socket any) bool {
	if socket == nil {
		return false
	}
	return reflect.TypeOf(socket).Comparable()
}

// Connect adds socket to room and returns a short unique peer ID. It returns an
// empty ID for a nil or non-comparable socket.
func (m *Manager) Connect(roomID string, socket any, name, role string) string {
	if !comparableSocket(socket) {
		return ""
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.roomLocked(roomID)
	// Retry is practically never needed, but it makes the ID invariant explicit
	// even in tests that replace the random source.
	var id string
	for {
		id = randomPeerID()
		used := false
		for _, peer := range r.peers {
			if peer.ID == id {
				used = true
				break
			}
		}
		if !used {
			break
		}
	}
	r.peers[socket] = &Peer{ID: id, Name: normaliseName(name), Role: normaliseRole(role), LastSeen: m.nowLocked()}
	r.peerOrder = append(r.peerOrder, socket)
	r.updatedAt = m.nowLocked()
	return id
}

// randomPeerID is kept in this file so no UUID dependency is required for the
// board package. Crypto-random IDs are unnecessary for routing and the
// process-local counter below is collision-free for normal process lifetime.
var peerSequence struct {
	sync.Mutex
	n uint64
}

func randomPeerID() string {
	peerSequence.Lock()
	peerSequence.n++
	n := peerSequence.n
	peerSequence.Unlock()
	return fmt.Sprintf("%012x", n&0xffffffffffff)
}

// Identify updates only display information. Callers should pass the role
// obtained from the authenticated token, not a client-supplied role.
func (m *Manager) Identify(roomID string, socket any, name, role string) *Peer {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[roomID]
	if r == nil {
		return nil
	}
	peer := r.peers[socket]
	if peer == nil {
		return nil
	}
	peer.Name = normaliseName(name)
	peer.Role = normaliseRole(role)
	return clonePeer(peer)
}

// Touch records app-level traffic for idle eviction.
func (m *Manager) Touch(roomID string, socket any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.rooms[roomID]; r != nil {
		if peer := r.peers[socket]; peer != nil {
			peer.LastSeen = m.nowLocked()
		}
	}
}

// SetCursor updates the peer cursor and returns its public peer record.
func (m *Manager) SetCursor(roomID string, socket any, cursor map[string]any) *Peer {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.rooms[roomID]; r != nil {
		if peer := r.peers[socket]; peer != nil {
			peer.Cursor = cloneMap(cursor)
			return clonePeer(peer)
		}
	}
	return nil
}

// Disconnect removes a peer. A nil result means it was already disconnected.
func (m *Manager) Disconnect(roomID string, socket any) *Peer {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.rooms[roomID]
	if r == nil {
		return nil
	}
	peer := r.peers[socket]
	if peer == nil {
		return nil
	}
	delete(r.peers, socket)
	for i, existing := range r.peerOrder {
		if existing == socket {
			r.peerOrder = append(r.peerOrder[:i], r.peerOrder[i+1:]...)
			break
		}
	}
	r.updatedAt = m.nowLocked()
	if len(r.peers) == 0 {
		delete(m.rooms, roomID)
	}
	return clonePeer(peer)
}

// Absorb applies incoming elements with last-writer-wins semantics. Invalid or
// unidentifiable elements are ignored. The return value counts applied items.
func (m *Manager) Absorb(roomID string, elements []map[string]any) int {
	return m.AbsorbContext(context.Background(), roomID, elements)
}

// AbsorbContext applies incoming elements and attaches the originating routed
// writer to the debounced persistence snapshot. Context values are retained
// after request cancellation so store fencing still sees that writer.
func (m *Manager) AbsorbContext(ctx context.Context, roomID string, elements []map[string]any) int {
	m.mu.Lock()
	r := m.roomLocked(roomID)
	applied := 0
	for _, element := range elements {
		if m.absorbElementLocked(r, element) {
			applied++
		}
	}
	var snapshot *BoardSnapshot
	if applied > 0 {
		r.version++
		r.updatedAt = m.nowLocked()
		exported := m.boardSnapshotLocked(roomID, r)
		snapshot = &exported
	}
	m.mu.Unlock()
	if snapshot != nil {
		m.scheduleSnapshotSave(*snapshot, ctx)
	}
	return applied
}

// Snapshot returns a stable copy. Unknown rooms return an empty snapshot and
// do not create a room.
func (m *Manager) Snapshot(roomID string) RoomSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := m.rooms[roomID]
	if r == nil {
		return RoomSnapshot{}
	}
	return m.roomSnapshotLocked(roomID, r)
}

// Peers returns peers other than exclude. It is a stable copy.
func (m *Manager) Peers(roomID string, exclude any) []Peer {
	snapshot := m.Snapshot(roomID)
	if exclude == nil {
		return snapshot.Peers
	}
	m.mu.RLock()
	r := m.rooms[roomID]
	var excludedID string
	if r != nil {
		if p := r.peers[exclude]; p != nil {
			excludedID = p.ID
		}
	}
	m.mu.RUnlock()
	if excludedID == "" {
		return snapshot.Peers
	}
	peers := snapshot.Peers[:0]
	for _, peer := range snapshot.Peers {
		if peer.ID != excludedID {
			peers = append(peers, peer)
		}
	}
	return peers
}

// ActiveRooms reports the number of rooms that currently have peers.
func (m *Manager) ActiveRooms() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.rooms)
}

// Reset removes all process-local board state.
func (m *Manager) Reset() {
	m.mu.Lock()
	m.rooms = make(map[string]*room)
	m.mu.Unlock()
	m.persistenceMu.Lock()
	for _, pending := range m.pendingSaves {
		if pending.timer != nil {
			pending.timer.Stop()
		}
	}
	m.pendingSaves = make(map[string]*pendingBoardSave)
	m.persistenceMu.Unlock()
}

// SetClock replaces the clock used for LastSeen. It is primarily useful for
// deterministic tests; nil restores time.Now.
func (m *Manager) SetClock(clock func() time.Time) {
	m.mu.Lock()
	if clock == nil {
		m.now = time.Now
	} else {
		m.now = clock
	}
	m.mu.Unlock()
}

// SetLastSeen is a test/integration helper for deterministic eviction.
func (m *Manager) SetLastSeen(roomID string, socket any, at time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r := m.rooms[roomID]; r != nil {
		if peer := r.peers[socket]; peer != nil {
			peer.LastSeen = at
			return true
		}
	}
	return false
}

// Sender interfaces accepted by Send. The context-aware forms are preferred;
// the others make small test doubles and WebSocket adapters easy to write.
type ContextJSONSender interface {
	SendJSON(context.Context, any) error
}
type JSONSender interface{ SendJSON(any) error }
type ContextTextSender interface {
	SendText(context.Context, string) error
}
type TextSender interface{ SendText(string) error }
type JSONWriter interface{ WriteJSON(any) error }
type TextWriter interface{ WriteText(string) error }

// Send encodes message as JSON and writes it to socket.
func (m *Manager) Send(ctx context.Context, socket any, message any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	switch writer := socket.(type) {
	case ContextJSONSender:
		return writer.SendJSON(ctx, message)
	case JSONSender:
		return writer.SendJSON(message)
	case JSONWriter:
		return writer.WriteJSON(message)
	default:
		payload, err := json.Marshal(message)
		if err != nil {
			return err
		}
		switch writer := socket.(type) {
		case ContextTextSender:
			return writer.SendText(ctx, string(payload))
		case TextSender:
			return writer.SendText(string(payload))
		case TextWriter:
			return writer.WriteText(string(payload))
		default:
			return errors.New("board socket does not implement a JSON or text sender")
		}
	}
}

// Broadcast sends to all current peers other than exclude. Failed transports
// are detached; a failed peer is not announced because it never received the
// frame, matching the original relay behavior.
func (m *Manager) Broadcast(ctx context.Context, roomID string, message any, exclude any) int {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.RLock()
	r := m.rooms[roomID]
	var sockets []any
	if r != nil {
		sockets = make([]any, 0, len(r.peers))
		for _, socket := range r.peerOrder {
			if socket != exclude && r.peers[socket] != nil {
				sockets = append(sockets, socket)
			}
		}
	}
	m.mu.RUnlock()
	sent := 0
	for _, socket := range sockets {
		if err := m.Send(ctx, socket, message); err != nil {
			m.Disconnect(roomID, socket)
			continue
		}
		sent++
	}
	return sent
}

// Close interfaces accepted by EvictStale.
type ContextCloser interface {
	Close(context.Context, int, string) error
}
type ReasonCloser interface{ Close(int, string) error }
type CodeCloser interface{ Close(int) error }

func closeSocket(ctx context.Context, socket any, code int) error {
	switch closer := socket.(type) {
	case ContextCloser:
		return closer.Close(ctx, code, "idle timeout")
	case ReasonCloser:
		return closer.Close(code, "idle timeout")
	case CodeCloser:
		return closer.Close(code)
	default:
		return nil
	}
}

// EvictStale closes and removes peers that have not produced traffic within
// maxIdle. An optional close function supports adapters with a different close
// signature. Peers removed by the sweep trigger peer-left broadcasts.
func (m *Manager) EvictStale(ctx context.Context, maxIdle time.Duration, closeFns ...func(context.Context, any, int) error) int {
	if ctx == nil {
		ctx = context.Background()
	}
	if maxIdle < 0 {
		maxIdle = 0
	}
	now := m.nowTime()
	type stalePeer struct {
		roomID string
		socket any
		peer   *Peer
	}
	var stale []stalePeer
	m.mu.RLock()
	for roomID, r := range m.rooms {
		for _, socket := range r.peerOrder {
			peer := r.peers[socket]
			if peer != nil && now.Sub(peer.LastSeen) > maxIdle {
				stale = append(stale, stalePeer{roomID: roomID, socket: socket, peer: clonePeer(peer)})
			}
		}
	}
	m.mu.RUnlock()
	evicted := 0
	for _, item := range stale {
		select {
		case <-ctx.Done():
			return evicted
		default:
		}
		// Detach first, so a concurrent disconnect cannot announce a duplicate.
		if m.Disconnect(item.roomID, item.socket) == nil {
			continue
		}
		if len(closeFns) > 0 && closeFns[0] != nil {
			_ = closeFns[0](ctx, item.socket, CloseIdleTimeout)
		} else {
			_ = closeSocket(ctx, item.socket, CloseIdleTimeout)
		}
		evicted++
		m.Broadcast(ctx, item.roomID, map[string]any{"type": "peer-left", "peer": item.peer}, nil)
	}
	return evicted
}

func (m *Manager) nowTime() time.Time {
	m.mu.RLock()
	now := m.now
	m.mu.RUnlock()
	if now == nil {
		return time.Now()
	}
	return now()
}

func clonePeer(peer *Peer) *Peer {
	if peer == nil {
		return nil
	}
	copy := *peer
	copy.Cursor = cloneMap(peer.Cursor)
	return &copy
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = cloneValue(value)
	}
	return output
}

func cloneValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		return cloneMap(value)
	case []any:
		output := make([]any, len(value))
		for i, item := range value {
			output[i] = cloneValue(item)
		}
		return output
	default:
		return value
	}
}

func stringValue(value any) (string, bool) {
	switch value := value.(type) {
	case string:
		return value, true
	default:
		return "", false
	}
}

func versionValue(value any) (float64, bool) {
	if value == nil {
		return 0, true
	}
	switch value := value.(type) {
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case float32:
		return float64(value), !math.IsNaN(float64(value))
	case float64:
		return value, !math.IsNaN(value)
	default:
		return 0, false
	}
}
