package boards

import "context"

// CanDraw derives permission from server state, never a browser-supplied role.
func (m *Manager) CanDraw(roomID string, socket any) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := m.rooms[roomID]
	if r == nil || r.peers[socket] == nil {
		return false
	}
	return r.peers[socket].Role == "teacher" || r.studentsDraw
}

// SetStudentDrawing persists the permission with room history and broadcasts
// it to every participant. The session verifies the caller's teacher role.
func (m *Manager) SetStudentDrawing(ctx context.Context, roomID string, allowed bool) error {
	m.controlMu.Lock()
	defer m.controlMu.Unlock()
	if err := m.Resume(ctx, roomID); err != nil {
		return err
	}
	m.mu.Lock()
	r := m.roomLocked(roomID)
	previous := r.studentsDraw
	previousVersion, previousUpdated := r.version, r.updatedAt
	r.studentsDraw = allowed
	r.version++
	r.updatedAt = m.nowLocked()
	snapshot := m.boardSnapshotLocked(roomID, r)
	if err := m.persistSnapshot(ctx, snapshot); err != nil {
		r.studentsDraw = previous
		r.version, r.updatedAt = previousVersion, previousUpdated
		m.mu.Unlock()
		return err
	}
	m.mu.Unlock()
	m.Broadcast(ctx, roomID, map[string]any{"type": "permissions", "students_draw": allowed}, nil)
	return nil
}
