package store

import (
	"context"
	"encoding/json"
	"github.com/ayush/slate/internal/boards"
	"sort"
)

type MemoryBoardStore struct{ backend *MemoryStore }

// MemoryStoreBackend allows lightweight test wrappers that embed a
// MemoryStore to retain the same board persistence adapter.
func (m *MemoryStore) MemoryStoreBackend() *MemoryStore { return m }

// NewMemoryBoardStore shares version history across both local backends.
func NewMemoryBoardStore(backend *MemoryStore) *MemoryBoardStore {
	return &MemoryBoardStore{backend: backend}
}

func (s *MemoryBoardStore) Save(ctx context.Context, snapshot boards.BoardSnapshot) error {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return s.backend.withFence(ctx, func() error {
		s.backend.mu.Lock()
		defer s.backend.mu.Unlock()
		if s.backend.boardSnapshots == nil {
			s.backend.boardSnapshots = map[string]map[int64][]byte{}
		}
		versions := s.backend.boardSnapshots[snapshot.RoomID]
		if versions == nil {
			versions = map[int64][]byte{}
			s.backend.boardSnapshots[snapshot.RoomID] = versions
		}
		// Match DynamoDB's monotonic current pointer: a delayed save cannot undo a restore.
		for version := range versions {
			if version > snapshot.Version {
				return nil
			}
		}
		versions[snapshot.Version] = append([]byte(nil), data...)
		return nil
	})
}

func (s *MemoryBoardStore) Load(ctx context.Context, roomID string) (*boards.BoardSnapshot, error) {
	versions, err := s.ListVersions(ctx, roomID, 1)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, boards.ErrSnapshotNotFound
	}
	return s.LoadVersion(ctx, roomID, versions[0].Version)
}

func (s *MemoryBoardStore) LoadVersion(_ context.Context, roomID string, version int64) (*boards.BoardSnapshot, error) {
	s.backend.mu.RLock()
	data := append([]byte(nil), s.backend.boardSnapshots[roomID][version]...)
	s.backend.mu.RUnlock()
	if len(data) == 0 {
		return nil, boards.ErrSnapshotNotFound
	}
	var snapshot boards.BoardSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (s *MemoryBoardStore) ListVersions(_ context.Context, roomID string, limit int) ([]boards.BoardVersion, error) {
	s.backend.mu.RLock()
	defer s.backend.mu.RUnlock()
	snapshots := s.backend.boardSnapshots[roomID]
	keys := make([]int64, 0, len(snapshots))
	for version := range snapshots {
		keys = append(keys, version)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] > keys[j] })
	if limit <= 0 {
		limit = 50
	}
	if len(keys) > limit {
		keys = keys[:limit]
	}
	rows := make([]boards.BoardVersion, 0, len(keys))
	for _, version := range keys {
		var snapshot boards.BoardSnapshot
		if err := json.Unmarshal(snapshots[version], &snapshot); err != nil {
			return nil, err
		}
		rows = append(rows, boards.BoardVersion{RoomID: roomID, Version: version, ElementCount: len(snapshot.Elements), SavedAt: snapshot.UpdatedAt, CreatedAt: snapshot.CreatedAt, UpdatedAt: snapshot.UpdatedAt})
	}
	return rows, nil
}
