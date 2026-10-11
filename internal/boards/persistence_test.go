package boards

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/ayush/slate/internal/clusterstate"
)

type memoryBoardStore struct {
	mu       sync.Mutex
	current  map[string]BoardSnapshot
	versions map[string]map[int64]BoardSnapshot
	saved    chan BoardSnapshot
}

type writerSaveResult struct {
	writer    clusterstate.Writer
	hasWriter bool
	ctxErr    error
	err       error
}

type writerCaptureBoardStore struct {
	active clusterstate.Writer
	saves  chan writerSaveResult
}

func (s *writerCaptureBoardStore) Load(context.Context, string) (*BoardSnapshot, error) {
	return nil, ErrSnapshotNotFound
}

func (s *writerCaptureBoardStore) Save(ctx context.Context, _ BoardSnapshot) error {
	writer, hasWriter := clusterstate.WriterFrom(ctx)
	var err error
	if !hasWriter || writer != s.active {
		err = clusterstate.ErrFenced
	}
	s.saves <- writerSaveResult{writer: writer, hasWriter: hasWriter, ctxErr: ctx.Err(), err: err}
	return err
}

func (s *writerCaptureBoardStore) ListVersions(context.Context, string, int) ([]BoardVersion, error) {
	return nil, nil
}

func (s *writerCaptureBoardStore) LoadVersion(context.Context, string, int64) (*BoardSnapshot, error) {
	return nil, ErrSnapshotNotFound
}

func newMemoryBoardStore() *memoryBoardStore {
	return &memoryBoardStore{
		current:  make(map[string]BoardSnapshot),
		versions: make(map[string]map[int64]BoardSnapshot),
		saved:    make(chan BoardSnapshot, 16),
	}
}

func (s *memoryBoardStore) Load(_ context.Context, roomID string) (*BoardSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.current[roomID]
	if !ok {
		return nil, ErrSnapshotNotFound
	}
	copy := snapshot.Clone()
	return &copy, nil
}

func (s *memoryBoardStore) Save(_ context.Context, snapshot BoardSnapshot) error {
	s.mu.Lock()
	copy := snapshot.Clone()
	s.current[snapshot.RoomID] = copy
	if s.versions[snapshot.RoomID] == nil {
		s.versions[snapshot.RoomID] = make(map[int64]BoardSnapshot)
	}
	s.versions[snapshot.RoomID][snapshot.Version] = copy.Clone()
	s.mu.Unlock()
	select {
	case s.saved <- copy:
	default:
	}
	return nil
}

func (s *memoryBoardStore) ListVersions(_ context.Context, roomID string, limit int) ([]BoardVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	versions := make([]BoardVersion, 0, len(s.versions[roomID]))
	for _, snapshot := range s.versions[roomID] {
		versions = append(versions, BoardVersion{
			RoomID: snapshot.RoomID, Version: snapshot.Version,
			CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt,
			ElementCount: len(snapshot.Elements),
		})
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i].Version > versions[j].Version })
	if limit > 0 && len(versions) > limit {
		versions = versions[:limit]
	}
	return versions, nil
}

func (s *memoryBoardStore) LoadVersion(_ context.Context, roomID string, version int64) (*BoardSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snapshot, ok := s.versions[roomID][version]
	if !ok {
		return nil, ErrSnapshotNotFound
	}
	copy := snapshot.Clone()
	return &copy, nil
}

func waitForSaved(t *testing.T, store *memoryBoardStore) BoardSnapshot {
	t.Helper()
	select {
	case snapshot := <-store.saved:
		return snapshot
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for debounced board save")
		return BoardSnapshot{}
	}
}

func TestManagerResumeSendsPersistedSnapshotAndMetadata(t *testing.T) {
	store := newMemoryBoardStore()
	created := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	if err := store.Save(context.Background(), BoardSnapshot{
		SchemaVersion: BoardSnapshotSchemaVersion,
		RoomMetadata: RoomMetadata{
			RoomID: "room", Version: 4, CreatedAt: created,
			UpdatedAt: created.Add(time.Minute),
		},
		Elements: []map[string]any{{"id": "persisted", "version": 9, "x": 10}},
	}); err != nil {
		t.Fatal(err)
	}

	manager := NewManagerWithPersistence(store, time.Hour)
	socket := &testSocket{}
	session := NewSession(manager, "room", socket, Identity{Name: "Student", Role: "student"})
	if _, err := session.HandleFrame(context.Background(), `{"type":"join"}`, 1024); err != nil {
		t.Fatal(err)
	}
	messages := socket.messages()
	if len(messages) != 1 || messages[0]["type"] != "init" {
		t.Fatalf("unexpected init messages: %#v", messages)
	}
	elements := messages[0]["elements"].([]any)
	if len(elements) != 1 || elements[0].(map[string]any)["id"] != "persisted" {
		t.Fatalf("persisted elements missing from init: %#v", elements)
	}
	room := messages[0]["room"].(map[string]any)
	if room["version"] != float64(4) || room["created_at"] != created.Format(time.RFC3339Nano) {
		t.Fatalf("persisted room metadata missing: %#v", room)
	}
	metadata, ok := manager.Metadata("room")
	if !ok || metadata.ParticipantCount != 1 || metadata.Version != 4 {
		t.Fatalf("unexpected live metadata: %#v %v", metadata, ok)
	}
}

func TestManagerDebouncedAndExplicitSaveHooks(t *testing.T) {
	store := newMemoryBoardStore()
	manager := NewManagerWithPersistence(store, 20*time.Millisecond)
	if got := manager.Absorb("room", []map[string]any{{"id": "e1", "version": 1}}); got != 1 {
		t.Fatalf("applied = %d, want 1", got)
	}
	saved := waitForSaved(t, store)
	if saved.Version != 1 || len(saved.Elements) != 1 {
		t.Fatalf("unexpected debounced snapshot: %#v", saved)
	}

	if got := manager.Absorb("room", []map[string]any{{"id": "e1", "version": 2}}); got != 1 {
		t.Fatalf("second applied = %d, want 1", got)
	}
	if err := manager.SaveSnapshot(context.Background(), "room"); err != nil {
		t.Fatal(err)
	}
	if current, err := store.Load(context.Background(), "room"); err != nil || current.Version != 2 {
		t.Fatalf("explicit save did not flush latest snapshot: %#v %v", current, err)
	}
}

func TestManagerDebouncedSaveRetainsOriginatingWriterAfterRequestCancellation(t *testing.T) {
	oldWriter := clusterstate.Writer{Backend: "A", Epoch: 7}
	activeWriter := clusterstate.Writer{Backend: "B", Epoch: 8}
	store := &writerCaptureBoardStore{
		active: activeWriter,
		saves:  make(chan writerSaveResult, 1),
	}
	manager := NewManagerWithPersistence(store, 10*time.Millisecond)

	requestCtx, cancel := context.WithCancel(clusterstate.WithWriter(context.Background(), oldWriter))
	session := NewSession(manager, "room", &testSocket{}, Identity{Name: "Student", Role: "student"})
	if _, err := session.HandleFrame(requestCtx, `{"type":"join"}`, 1024); err != nil {
		t.Fatalf("join frame: %v", err)
	}
	if _, err := session.HandleFrame(requestCtx, `{"type":"elements","elements":[{"id":"e1","version":1}]}`, 1024); err != nil {
		t.Fatalf("elements frame: %v", err)
	}
	cancel()

	select {
	case result := <-store.saves:
		if !result.hasWriter || result.writer != oldWriter {
			t.Fatalf("debounced save writer = %+v, present=%v; want original %+v", result.writer, result.hasWriter, oldWriter)
		}
		if result.ctxErr != nil {
			t.Fatalf("background save retained cancelled request context: %v", result.ctxErr)
		}
		if !errors.Is(result.err, clusterstate.ErrFenced) {
			t.Fatalf("store result = %v, want stale writer rejection", result.err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for debounced save")
	}
	deadline := time.After(time.Second)
	for !errors.Is(manager.LastPersistenceError("room"), clusterstate.ErrFenced) {
		select {
		case <-deadline:
			t.Fatalf("manager did not record stale save error: %v", manager.LastPersistenceError("room"))
		case <-time.After(time.Millisecond):
		}
	}
	if err := manager.Flush(clusterstate.WithWriter(context.Background(), activeWriter), "room"); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("retry flush error = %v, want stale writer rejection", err)
	}
	retry := <-store.saves
	if !retry.hasWriter || retry.writer != oldWriter {
		t.Fatalf("retry flush writer = %+v, present=%v; want original %+v", retry.writer, retry.hasWriter, oldWriter)
	}
}

func TestManagerFlushUsesPendingSnapshotWriterInsteadOfNewerCallerWriter(t *testing.T) {
	oldWriter := clusterstate.Writer{Backend: "A", Epoch: 7}
	activeWriter := clusterstate.Writer{Backend: "B", Epoch: 8}
	store := &writerCaptureBoardStore{
		active: activeWriter,
		saves:  make(chan writerSaveResult, 1),
	}
	manager := NewManagerWithPersistence(store, time.Hour)
	requestCtx := clusterstate.WithWriter(context.Background(), oldWriter)
	manager.AbsorbContext(requestCtx, "room", []map[string]any{{"id": "e1", "version": 1}})

	flushCtx := clusterstate.WithWriter(context.Background(), activeWriter)
	if err := manager.Flush(flushCtx, "room"); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("flush error = %v, want stale snapshot rejection", err)
	}
	result := <-store.saves
	if !result.hasWriter || result.writer != oldWriter {
		t.Fatalf("flush writer = %+v, present=%v; want original %+v", result.writer, result.hasWriter, oldWriter)
	}
}

func TestManagerHistoryRestoreAndExport(t *testing.T) {
	store := newMemoryBoardStore()
	manager := NewManagerWithPersistence(store, time.Hour)
	manager.Absorb("room", []map[string]any{{"id": "e1", "version": 1, "label": "first"}})
	if err := manager.Flush(context.Background(), "room"); err != nil {
		t.Fatal(err)
	}
	manager.Absorb("room", []map[string]any{{"id": "e1", "version": 2, "label": "second"}})
	if err := manager.Flush(context.Background(), "room"); err != nil {
		t.Fatal(err)
	}

	versions, err := manager.ListVersions(context.Background(), "room", 10)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("unexpected history: %#v %v", versions, err)
	}
	restored, err := manager.Restore(context.Background(), "room", 1)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Version != 3 || len(restored.Elements) != 1 || restored.Elements[0]["label"] != "first" {
		t.Fatalf("restore did not create a new live revision: %#v", restored)
	}
	current, err := store.Load(context.Background(), "room")
	if err != nil || current.Version != 3 || current.Elements[0]["version"] != 1 {
		t.Fatalf("restore was not persisted: %#v %v", current, err)
	}

	export, err := manager.ExportSnapshotJSON("room")
	if err != nil {
		t.Fatal(err)
	}
	var decoded BoardSnapshot
	if err := json.Unmarshal(export, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != BoardSnapshotSchemaVersion || decoded.RoomID != "room" || len(decoded.Elements) != 1 {
		t.Fatalf("bad export: %s", export)
	}
}
