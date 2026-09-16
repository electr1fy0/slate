package store

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/ayush/slate/internal/boards"
)

const boardVersionPrefix = "VERSION#"

// BoardStore persists board snapshots in the existing single DynamoDB table.
// Each room has one current pointer and one item per saved revision:
//
//	pk = BOARD#{roomID}, sk = CURRENT
//	pk = BOARD#{roomID}, sk = VERSION#{zero-padded revision}
//
// The complete export JSON is stored as a string so arbitrary JSON element
// fields round-trip without DynamoDB's typed-map conversion changing them.
type BoardStore struct {
	backend *Store
}

var marshalBoard = marshal

// NewBoardStore adapts the existing DynamoDB Store to boards' optional
// BoardSnapshotStore contract. Passing nil is allowed, but operations then
// return boards.ErrPersistenceUnavailable.
func NewBoardStore(backend *Store) *BoardStore {
	return &BoardStore{backend: backend}
}

type boardSnapshotItem struct {
	PK               string `dynamodbav:"pk"`
	SK               string `dynamodbav:"sk"`
	RoomID           string `dynamodbav:"room_id"`
	SchemaVersion    int    `dynamodbav:"schema_version"`
	Version          int64  `dynamodbav:"version"`
	CreatedAt        string `dynamodbav:"created_at"`
	UpdatedAt        string `dynamodbav:"updated_at"`
	ParticipantCount int    `dynamodbav:"participant_count"`
	ElementCount     int    `dynamodbav:"element_count"`
	SnapshotJSON     string `dynamodbav:"snapshot_json"`
	SavedAt          string `dynamodbav:"saved_at"`
}

func (s *BoardStore) available() error {
	if s == nil || s.backend == nil || s.backend.Client == nil {
		return boards.ErrPersistenceUnavailable
	}
	return nil
}

// Save writes the current pointer and the corresponding immutable history
// item atomically. Re-saving a revision is idempotent and replaces that
// revision's item, which is useful when a debounce timer and an explicit flush
// race during shutdown.
func (s *BoardStore) Save(ctx context.Context, snapshot boards.BoardSnapshot) error {
	if err := s.available(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if snapshot.RoomID == "" {
		return fmt.Errorf("board snapshot room id is required")
	}
	if snapshot.Version < 0 {
		return fmt.Errorf("board snapshot version must be non-negative: %d", snapshot.Version)
	}
	if snapshot.SchemaVersion == 0 {
		snapshot.SchemaVersion = boards.BoardSnapshotSchemaVersion
	}
	now := s.backend.now()
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = now
	}
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = snapshot.CreatedAt
	}
	snapshot = snapshot.Clone()
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("encode board snapshot: %w", err)
	}
	record := boardSnapshotItem{
		PK:               boardPartitionKey(snapshot.RoomID),
		RoomID:           snapshot.RoomID,
		SchemaVersion:    snapshot.SchemaVersion,
		Version:          snapshot.Version,
		CreatedAt:        snapshot.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:        snapshot.UpdatedAt.UTC().Format(time.RFC3339Nano),
		ParticipantCount: snapshot.ParticipantCount,
		ElementCount:     len(snapshot.Elements),
		SnapshotJSON:     string(payload),
		SavedAt:          now.UTC().Format(time.RFC3339Nano),
	}
	current := record
	current.SK = "CURRENT"
	history := record
	history.SK = boardVersionKey(snapshot.Version)
	currentItem, err := marshalBoard(current)
	if err != nil {
		return fmt.Errorf("encode current board snapshot: %w", err)
	}
	historyItem, err := marshalBoard(history)
	if err != nil {
		return fmt.Errorf("encode board snapshot history: %w", err)
	}
	_, err = s.backend.Client.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Put: &types.Put{TableName: aws.String(s.backend.TableName), Item: currentItem}},
			{Put: &types.Put{TableName: aws.String(s.backend.TableName), Item: historyItem}},
		},
	})
	return err
}

// Load returns the latest current pointer for a room.
func (s *BoardStore) Load(ctx context.Context, roomID string) (*boards.BoardSnapshot, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	item, err := s.backend.get(ctx, key(boardPartitionKey(roomID), "CURRENT"))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, boards.ErrSnapshotNotFound
	}
	snapshot, _, err := decodeBoardSnapshot(item)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

// ListVersions returns history entries newest first. Query results are sorted
// in-process as well because lightweight test doubles and some local DynamoDB
// emulators do not guarantee the requested order.
func (s *BoardStore) ListVersions(ctx context.Context, roomID string, limit int) ([]boards.BoardVersion, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 50
	}
	items, err := s.backend.queryAll(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.backend.TableName),
		KeyConditionExpression: aws.String("pk = :pk AND begins_with(sk, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: boardPartitionKey(roomID)},
			":prefix": &types.AttributeValueMemberS{Value: boardVersionPrefix},
		},
		ScanIndexForward: aws.Bool(false),
	})
	if err != nil {
		return nil, err
	}
	versions := make([]boards.BoardVersion, 0, len(items))
	for _, item := range items {
		record, err := decodeBoardRecord(item)
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(record.SK, boardVersionPrefix) {
			continue
		}
		versions = append(versions, boardVersionFromRecord(record))
	}
	sort.SliceStable(versions, func(i, j int) bool {
		if versions[i].Version == versions[j].Version {
			return versions[i].SavedAt.After(versions[j].SavedAt)
		}
		return versions[i].Version > versions[j].Version
	})
	if len(versions) > limit {
		versions = versions[:limit]
	}
	return versions, nil
}

// LoadVersion returns the full historical export for a requested revision.
func (s *BoardStore) LoadVersion(ctx context.Context, roomID string, version int64) (*boards.BoardSnapshot, error) {
	if err := s.available(); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if version < 0 {
		return nil, boards.ErrSnapshotNotFound
	}
	item, err := s.backend.get(ctx, key(boardPartitionKey(roomID), boardVersionKey(version)))
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, boards.ErrSnapshotNotFound
	}
	snapshot, _, err := decodeBoardSnapshot(item)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func boardPartitionKey(roomID string) string { return "BOARD#" + roomID }

func boardVersionKey(version int64) string {
	return fmt.Sprintf("%s%020d", boardVersionPrefix, version)
}

func decodeBoardRecord(item map[string]types.AttributeValue) (boardSnapshotItem, error) {
	var record boardSnapshotItem
	if err := unmarshal(item, &record); err != nil {
		return boardSnapshotItem{}, fmt.Errorf("decode board snapshot record: %w", err)
	}
	return record, nil
}

func decodeBoardSnapshot(item map[string]types.AttributeValue) (boards.BoardSnapshot, boardSnapshotItem, error) {
	record, err := decodeBoardRecord(item)
	if err != nil {
		return boards.BoardSnapshot{}, boardSnapshotItem{}, err
	}
	var snapshot boards.BoardSnapshot
	if record.SnapshotJSON == "" {
		return boards.BoardSnapshot{}, record, fmt.Errorf("board snapshot record has no snapshot_json")
	}
	if err := json.Unmarshal([]byte(record.SnapshotJSON), &snapshot); err != nil {
		return boards.BoardSnapshot{}, record, fmt.Errorf("decode board snapshot JSON: %w", err)
	}
	if snapshot.RoomID == "" {
		snapshot.RoomID = record.RoomID
	}
	if snapshot.SchemaVersion == 0 {
		snapshot.SchemaVersion = record.SchemaVersion
	}
	if snapshot.Version == 0 && record.Version != 0 {
		snapshot.Version = record.Version
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = parseBoardTime(record.CreatedAt)
	}
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = parseBoardTime(record.UpdatedAt)
	}
	if snapshot.ParticipantCount == 0 && record.ParticipantCount != 0 {
		snapshot.ParticipantCount = record.ParticipantCount
	}
	return snapshot, record, nil
}

func boardVersionFromRecord(record boardSnapshotItem) boards.BoardVersion {
	return boards.BoardVersion{
		RoomID:       record.RoomID,
		Version:      record.Version,
		CreatedAt:    parseBoardTime(record.CreatedAt),
		UpdatedAt:    parseBoardTime(record.UpdatedAt),
		ElementCount: record.ElementCount,
		SavedAt:      parseBoardTime(record.SavedAt),
	}
}

func parseBoardTime(value string) time.Time {
	if value == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return t
}

var _ boards.BoardSnapshotStore = (*BoardStore)(nil)
