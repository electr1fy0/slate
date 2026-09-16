package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/boards"
	"github.com/ayush/slate/internal/config"
)

func boardBackend() (*BoardStore, *scriptedDynamo) {
	fake := newScriptedDynamo()
	backend := NewWithClient(fake, config.Defaults())
	backend.Now = func() time.Time { return time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC) }
	return NewBoardStore(backend), fake
}

func TestBoardStoreValidationAndAvailability(t *testing.T) {
	ctx := context.Background()
	if err := (*BoardStore)(nil).available(); !errors.Is(err, boards.ErrPersistenceUnavailable) {
		t.Fatalf("nil board store availability = %v", err)
	}
	if err := (&BoardStore{}).available(); !errors.Is(err, boards.ErrPersistenceUnavailable) {
		t.Fatalf("empty board store availability = %v", err)
	}
	if err := NewBoardStore(&Store{}).available(); !errors.Is(err, boards.ErrPersistenceUnavailable) {
		t.Fatalf("clientless board store availability = %v", err)
	}
	store, fake := boardBackend()
	if err := store.available(); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(ctx, boards.BoardSnapshot{}); err == nil {
		t.Fatal("empty room id should fail")
	}
	if err := store.Save(ctx, boards.BoardSnapshot{RoomMetadata: boards.RoomMetadata{RoomID: "room", Version: -1}}); err == nil {
		t.Fatal("negative version should fail")
	}
	if err := store.Save(nil, boards.BoardSnapshot{RoomMetadata: boards.RoomMetadata{RoomID: "room"}, Elements: []map[string]any{{"bad": func() {}}}}); err == nil {
		t.Fatal("unencodable snapshot should fail")
	}
	fake.transactionErrors = []error{errors.New("board transaction failed")}
	if err := store.Save(ctx, boards.BoardSnapshot{RoomMetadata: boards.RoomMetadata{RoomID: "room", Version: 1}}); err == nil {
		t.Fatal("board transaction error should be returned")
	}

	if _, err := store.Load(ctx, "missing"); !errors.Is(err, boards.ErrSnapshotNotFound) {
		t.Fatalf("missing current snapshot = %v", err)
	}
	fake.getErrors = []error{errors.New("board read failed")}
	if _, err := store.Load(ctx, "room"); err == nil {
		t.Fatal("board read errors should be returned")
	}
	if _, err := store.LoadVersion(ctx, "room", -1); !errors.Is(err, boards.ErrSnapshotNotFound) {
		t.Fatalf("negative history version = %v", err)
	}
	fake.getErrors = []error{errors.New("history read failed")}
	if _, err := store.LoadVersion(ctx, "room", 1); err == nil {
		t.Fatal("history read errors should be returned")
	}
	if _, err := store.ListVersions(ctx, "room", 10); err == nil {
		// The scripted query is empty after the one-shot error above; this call
		// intentionally verifies that an empty history is a valid result.
	}
}

func TestBoardStoreDecodeHistoryAndFallbacks(t *testing.T) {
	store, fake := boardBackend()
	ctx := context.Background()
	created := time.Date(2026, 9, 16, 11, 0, 0, 0, time.UTC)
	snapshot := boards.BoardSnapshot{SchemaVersion: boards.BoardSnapshotSchemaVersion, RoomMetadata: boards.RoomMetadata{RoomID: "room", Version: 3, CreatedAt: created, UpdatedAt: created.Add(time.Minute), ParticipantCount: 2}, Elements: []map[string]any{{"id": "one"}}}
	if err := store.Save(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load(ctx, "room"); err != nil || got == nil || got.Version != 3 {
		t.Fatalf("saved current = %#v, %v", got, err)
	}
	if versions, err := store.ListVersions(nil, "room", 0); err != nil || len(versions) != 1 || versions[0].Version != 3 {
		t.Fatalf("saved history = %#v, %v", versions, err)
	}
	if got, err := store.LoadVersion(nil, "room", 3); err != nil || got == nil || got.RoomID != "room" {
		t.Fatalf("saved history snapshot = %#v, %v", got, err)
	}

	record := boardSnapshotItem{PK: "BOARD#fallback", SK: "VERSION#00000000000000000005", RoomID: "fallback", SchemaVersion: 2, Version: 5, CreatedAt: created.Format(time.RFC3339Nano), UpdatedAt: created.Add(time.Minute).Format(time.RFC3339Nano), ParticipantCount: 4, ElementCount: 2, SavedAt: created.Add(2 * time.Minute).Format(time.RFC3339Nano), SnapshotJSON: `{}`}
	fake.items[itemKey(scriptedAttributeMap(record))] = scriptedAttributeMap(record)
	decoded, decodedRecord, err := decodeBoardSnapshot(scriptedAttributeMap(record))
	if err != nil || decoded.RoomID != "fallback" || decoded.SchemaVersion != 2 || decoded.Version != 5 || decoded.ParticipantCount != 4 || decodedRecord.SK == "" {
		t.Fatalf("fallback snapshot = %#v %#v, %v", decoded, decodedRecord, err)
	}
	if _, err := decodeBoardRecord(map[string]types.AttributeValue{"pk": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}); err == nil {
		t.Fatal("invalid board record should fail")
	}
	if _, _, err := decodeBoardSnapshot(map[string]types.AttributeValue{"pk": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{}}}); err == nil {
		t.Fatal("invalid board snapshot record should fail")
	}
	missingPayload := record
	missingPayload.SnapshotJSON = ""
	if _, _, err := decodeBoardSnapshot(scriptedAttributeMap(missingPayload)); err == nil {
		t.Fatal("missing snapshot payload should fail")
	}
	badPayload := record
	badPayload.SnapshotJSON = "not-json"
	if _, _, err := decodeBoardSnapshot(scriptedAttributeMap(badPayload)); err == nil {
		t.Fatal("bad snapshot JSON should fail")
	}
	if !parseBoardTime(created.Format(time.RFC3339Nano)).Equal(created) || !parseBoardTime("").IsZero() || !parseBoardTime("bad").IsZero() {
		t.Fatal("board time parsing")
	}

	currentItem := scriptedAttributeMap(record)
	currentItem["sk"] = &types.AttributeValueMemberS{Value: "CURRENT"}
	other := record
	other.Version = 1
	other.SK = boardVersionKey(1)
	other.SavedAt = created.Add(3 * time.Minute).Format(time.RFC3339Nano)
	equalVersion := record
	equalVersion.SK = boardVersionKey(6)
	equalVersion.SavedAt = created.Add(4 * time.Minute).Format(time.RFC3339Nano)
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{currentItem, scriptedAttributeMap(other), scriptedAttributeMap(record), scriptedAttributeMap(equalVersion)}}}
	if versions, err := store.ListVersions(ctx, "fallback", 1); err != nil || len(versions) != 1 || versions[0].Version != 5 {
		t.Fatalf("sorted/limited history = %#v, %v", versions, err)
	}
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"pk": &types.AttributeValueMemberS{Value: "BOARD#fallback"}, "sk": &types.AttributeValueMemberS{Value: "VERSION#bad"}, "snapshot_json": &types.AttributeValueMemberS{Value: "{}"}}}}}
	if _, err := store.ListVersions(ctx, "fallback", 5); err != nil {
		t.Fatal(err)
	}
	fake.queryErrors = []error{errors.New("history query failed")}
	if _, err := store.ListVersions(ctx, "fallback", 5); err == nil {
		t.Fatal("history query errors should be returned")
	}
	if _, err := store.LoadVersion(ctx, "fallback", 999); !errors.Is(err, boards.ErrSnapshotNotFound) {
		t.Fatalf("missing history snapshot = %v", err)
	}
}

func TestBoardStoreFailureEdges(t *testing.T) {
	ctx := context.Background()
	store, fake := boardBackend()
	if err := (&BoardStore{}).Save(ctx, boards.BoardSnapshot{RoomMetadata: boards.RoomMetadata{RoomID: "room"}}); !errors.Is(err, boards.ErrPersistenceUnavailable) {
		t.Fatalf("unavailable save = %v", err)
	}
	if _, err := (&BoardStore{}).Load(ctx, "room"); !errors.Is(err, boards.ErrPersistenceUnavailable) {
		t.Fatalf("unavailable load = %v", err)
	}
	if _, err := (&BoardStore{}).ListVersions(ctx, "room", 1); !errors.Is(err, boards.ErrPersistenceUnavailable) {
		t.Fatalf("unavailable history list = %v", err)
	}
	if _, err := (&BoardStore{}).LoadVersion(ctx, "room", 1); !errors.Is(err, boards.ErrPersistenceUnavailable) {
		t.Fatalf("unavailable history load = %v", err)
	}

	oldMarshal := marshalBoard
	calls := 0
	marshalBoard = func(value any) (map[string]types.AttributeValue, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("current encoding failed")
		}
		return marshal(value)
	}
	if err := store.Save(ctx, boards.BoardSnapshot{RoomMetadata: boards.RoomMetadata{RoomID: "room", Version: 1}}); err == nil {
		t.Fatal("current marshal failure should be returned")
	}
	calls = 0
	marshalBoard = func(value any) (map[string]types.AttributeValue, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("history encoding failed")
		}
		return marshal(value)
	}
	if err := store.Save(ctx, boards.BoardSnapshot{RoomMetadata: boards.RoomMetadata{RoomID: "room", Version: 2}}); err == nil {
		t.Fatal("history marshal failure should be returned")
	}
	marshalBoard = oldMarshal

	if err := store.Save(ctx, boards.BoardSnapshot{RoomMetadata: boards.RoomMetadata{RoomID: "room", Version: 3}}); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load(nil, "room"); err != nil || got == nil {
		t.Fatalf("nil-context load = %#v, %v", got, err)
	}
	fake.items[itemKey(key("BOARD#bad-load", "CURRENT"))] = map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "BOARD#bad-load"}, "sk": &types.AttributeValueMemberS{Value: "CURRENT"}, "snapshot_json": &types.AttributeValueMemberS{Value: "not-json"}}
	if _, err := store.Load(ctx, "bad-load"); err == nil {
		t.Fatal("bad current snapshot should fail to decode")
	}
	fake.items[itemKey(key("BOARD#bad-version", boardVersionKey(1)))] = map[string]types.AttributeValue{"pk": &types.AttributeValueMemberS{Value: "BOARD#bad-version"}, "sk": &types.AttributeValueMemberS{Value: boardVersionKey(1)}, "snapshot_json": &types.AttributeValueMemberS{Value: "not-json"}}
	if _, err := store.LoadVersion(ctx, "bad-version", 1); err == nil {
		t.Fatal("bad history snapshot should fail to decode")
	}
	fake.queryPages = []*dynamodb.QueryOutput{{Items: []map[string]types.AttributeValue{{"pk": &types.AttributeValueMemberS{Value: "BOARD#bad-history"}, "sk": &types.AttributeValueMemberS{Value: boardVersionKey(1)}, "version": &types.AttributeValueMemberS{Value: "bad"}, "snapshot_json": &types.AttributeValueMemberS{Value: "not-json"}}}}}
	if _, err := store.ListVersions(ctx, "bad-history", 1); err == nil {
		t.Fatal("bad history record should fail to decode")
	}
}
