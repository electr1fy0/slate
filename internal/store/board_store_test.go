package store

import (
	"context"
	"testing"
	"time"

	"github.com/ayush/slate/internal/boards"
	"github.com/ayush/slate/internal/config"
)

func TestBoardStorePersistsCurrentAndHistoryAsExportJSON(t *testing.T) {
	fake := newRecordingDynamo()
	backend := NewWithClient(fake, config.Defaults())
	backend.Now = func() time.Time { return time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC) }
	boardStore := NewBoardStore(backend)
	created := time.Date(2026, 9, 12, 8, 0, 0, 0, time.UTC)
	for _, revision := range []struct {
		version int64
		label   string
	}{{version: 1, label: "first"}, {version: 2, label: "second"}} {
		if err := boardStore.Save(context.Background(), boards.BoardSnapshot{
			SchemaVersion: boards.BoardSnapshotSchemaVersion,
			RoomMetadata: boards.RoomMetadata{
				RoomID: "room", Version: revision.version, CreatedAt: created,
				UpdatedAt:        created.Add(time.Duration(revision.version) * time.Minute),
				ParticipantCount: 2,
			},
			Elements: []map[string]any{{"id": "e1", "version": revision.version, "label": revision.label}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	current, err := boardStore.Load(context.Background(), "room")
	if err != nil || current.Version != 2 || current.Elements[0]["label"] != "second" {
		t.Fatalf("current snapshot = %#v, err=%v", current, err)
	}
	versions, err := boardStore.ListVersions(context.Background(), "room", 10)
	if err != nil || len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("history = %#v, err=%v", versions, err)
	}
	first, err := boardStore.LoadVersion(context.Background(), "room", 1)
	if err != nil || first.Elements[0]["label"] != "first" {
		t.Fatalf("version 1 = %#v, err=%v", first, err)
	}
	if _, ok := fake.items["BOARD#room\x00CURRENT"]; !ok {
		t.Fatal("current board pointer was not written")
	}
	if _, ok := fake.items["BOARD#room\x00VERSION#00000000000000000002"]; !ok {
		t.Fatal("board history item was not written")
	}
}
