package store

import (
	"context"
	"errors"
	"github.com/ayush/slate/internal/boards"
	"github.com/ayush/slate/internal/clusterstate"
	"testing"
)

func TestMemoryBoardVersionsAreSharedAndMonotonic(t *testing.T) {
	backend := NewMemory("local")
	a, b := NewMemoryBoardStore(backend), NewMemoryBoardStore(backend)
	ctx := context.Background()
	first := boards.BoardSnapshot{RoomMetadata: boards.RoomMetadata{RoomID: "room", Version: 1, StudentsDraw: true}, Elements: []map[string]any{{"id": "first"}}}
	if err := a.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first.Clone()
	second.Version = 2
	second.Elements[0]["id"] = "second"
	if err := b.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := a.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	latest, err := a.Load(ctx, "room")
	if err != nil || latest.Version != 2 || latest.Elements[0]["id"] != "second" || !latest.StudentsDraw {
		t.Fatalf("current pointer not shared/monotonic: %#v %v", latest, err)
	}
	old, err := b.LoadVersion(ctx, "room", 1)
	if err != nil || old.Elements[0]["id"] != "first" {
		t.Fatalf("history mutated: %#v %v", old, err)
	}
	old.Elements[0]["id"] = "changed"
	old, err = b.LoadVersion(ctx, "room", 1)
	if err != nil || old.Elements[0]["id"] != "first" {
		t.Fatal("load leaked mutable store data")
	}
	versions, err := b.ListVersions(ctx, "room", 1)
	if err != nil || len(versions) != 1 || versions[0].Version != 2 {
		t.Fatalf("history limit/order: %#v %v", versions, err)
	}
	if _, err := a.Load(ctx, "missing"); !errors.Is(err, boards.ErrSnapshotNotFound) {
		t.Fatalf("missing snapshot: %v", err)
	}
}

func TestMemoryBoardSavesRejectStaleWriters(t *testing.T) {
	backend := NewMemory("local")
	backend.SetFenceGuard(&memoryFenceStub{active: clusterstate.Writer{Backend: "B", Epoch: 2}})
	adapter := NewMemoryBoardStore(backend)
	snapshot := boards.BoardSnapshot{RoomMetadata: boards.RoomMetadata{RoomID: "room", Version: 1}}
	stale := clusterstate.WithWriter(context.Background(), clusterstate.Writer{Backend: "A", Epoch: 1})
	if err := adapter.Save(stale, snapshot); !errors.Is(err, clusterstate.ErrFenced) {
		t.Fatalf("stale save: %v", err)
	}
	if _, err := adapter.Load(context.Background(), "room"); !errors.Is(err, boards.ErrSnapshotNotFound) {
		t.Fatal("stale writer persisted a board")
	}
	fresh := clusterstate.WithWriter(context.Background(), clusterstate.Writer{Backend: "B", Epoch: 2})
	if err := adapter.Save(fresh, snapshot); err != nil {
		t.Fatal(err)
	}
}
