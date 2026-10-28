package boards

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPeerLifecycleMoreRegressions(t *testing.T) {
	m := NewManager()
	var nilSocket *testSocket
	if m.Connect("room", nilSocket, "", "teacher") != "" {
		t.Fatal("accepted typed nil")
	}
	socket := &testSocket{}
	id := m.Connect("room", socket, "  ", "teacher")
	if m.Connect("room", socket, "Again", "teacher") != id || len(m.Peers("room", nil)) != 1 {
		t.Fatal("duplicate registration")
	}
	if m.Peers("room", nil)[0].Name != "Guest" {
		t.Fatal("blank display name")
	}
	invalid := []int{1}
	m.Touch("room", invalid)
	if m.Identify("room", invalid, "", "") != nil || m.SetCursor("room", invalid, nil) != nil || m.Disconnect("room", invalid) != nil || m.CanDraw("room", invalid) || m.SetLastSeen("room", invalid, time.Now()) {
		t.Fatal("invalid socket affected room")
	}
	if len(m.Peers("room", invalid)) != 1 {
		t.Fatal("invalid exclusion affected peers")
	}
	m.Absorb("headless", []map[string]any{{"id": "shape", "version": 1}})
	if m.ActiveRooms() != 1 {
		t.Fatal("peerless room counted active")
	}
	m.recordPersistenceError("room", errors.New("old error"))
	m.Reset()
	if m.LastPersistenceError("room") != nil {
		t.Fatal("reset retained stale error")
	}
}

func TestCanceledBroadcastDoesNotEvictHealthyPeers(t *testing.T) {
	m := NewManager()
	socket := &testSocket{}
	m.Connect("room", socket, "A", "teacher")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if m.Broadcast(ctx, "room", map[string]any{"type": "ping"}, nil) != 0 || len(m.Peers("room", nil)) != 1 {
		t.Fatal("canceled broadcast removed peer")
	}
}

func TestHeartbeatDuringEvictionSweepKeepsPeer(t *testing.T) {
	m := NewManager()
	now := time.Unix(500, 0)
	m.SetClock(func() time.Time { return now })
	a, b := &testSocket{}, &testSocket{}
	m.Connect("room", a, "A", "teacher")
	m.Connect("room", b, "B", "student")
	m.SetLastSeen("room", a, now.Add(-time.Hour))
	m.SetLastSeen("room", b, now.Add(-time.Hour))
	count := m.EvictStale(context.Background(), time.Minute, func(context.Context, any, int) error { m.Touch("room", b); return nil })
	if count != 1 || len(m.Peers("room", nil)) != 1 {
		t.Fatal("fresh heartbeat evicted")
	}
}

func TestElementRevisionAndTypedSnapshotIsolation(t *testing.T) {
	m := NewManager()
	for _, invalid := range []any{math.Inf(1), math.Inf(-1), math.NaN(), -1, 1.5, float64(9007199254740992)} {
		if m.Absorb("room", []map[string]any{{"id": "invalid", "version": invalid}}) != 0 {
			t.Fatalf("accepted revision %v", invalid)
		}
	}
	points := [][]float64{{1, 2}}
	m.Absorb("room", []map[string]any{{"id": "shape", "version": 2, "versionNonce": 8, "points": points}})
	points[0][0] = 99
	snapshot := m.Snapshot("room")
	if snapshot.Elements[0]["points"].([][]float64)[0][0] != 1 {
		t.Fatal("input aliases snapshot")
	}
	snapshot.Elements[0]["points"].([][]float64)[0][0] = 88
	if m.Snapshot("room").Elements[0]["points"].([][]float64)[0][0] != 1 {
		t.Fatal("snapshot aliases room")
	}
	if m.Absorb("room", []map[string]any{{"id": "shape", "version": 2, "versionNonce": 3}}) != 1 {
		t.Fatal("tie breaker lost concurrent edit")
	}
	if m.Absorb("room", []map[string]any{{"id": "shape", "version": 2, "versionNonce": 8}}) != 0 {
		t.Fatal("tie breaker diverged")
	}
}

func TestSessionRepeatedJoinAndClose(t *testing.T) {
	m := NewManager()
	observer := &testSocket{}
	m.Connect("room", observer, "Observer", "teacher")
	session := NewSession(m, "room", &testSocket{}, Identity{Role: "teacher"})
	for i := 0; i < 2; i++ {
		if _, err := session.HandleFrame(context.Background(), `{"type":"join"}`, 0); err != nil {
			t.Fatal(err)
		}
	}
	if len(observer.messages()) != 1 {
		t.Fatal("repeat join announced twice")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	session.Close(ctx)
	if len(observer.messages()) != 2 {
		t.Fatal("cancellation suppressed departure")
	}
	if _, err := session.HandleFrame(context.Background(), `{"type":"permissions","students_draw":true}`, 0); err == nil {
		t.Fatal("closed session reused")
	}
}

func TestPersistedSnapshotValidation(t *testing.T) {
	for _, snapshot := range []BoardSnapshot{
		{RoomMetadata: RoomMetadata{RoomID: "other"}},
		{SchemaVersion: 2},
		{RoomMetadata: RoomMetadata{Version: -1}},
		{RoomMetadata: RoomMetadata{Version: math.MaxInt64}},
	} {
		store := &configurableBoardStore{loaded: &snapshot, historical: &snapshot}
		m := NewManagerWithPersistence(store, time.Hour)
		if err := m.Resume(context.Background(), "room"); err == nil {
			t.Fatalf("loaded invalid snapshot: %#v", snapshot)
		}
	}
	wrong := &configurableBoardStore{historical: &BoardSnapshot{RoomMetadata: RoomMetadata{Version: 2}}}
	if _, err := NewManagerWithPersistence(wrong, time.Hour).Restore(context.Background(), "room", 1); err == nil {
		t.Fatal("restored wrong requested revision")
	}
	store := newMemoryBoardStore()
	m := NewManagerWithPersistence(store, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Resume(ctx, "room"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled load = %v", err)
	}
	if m.rooms["room"].loaded {
		t.Fatal("canceled load cached")
	}
	if err := m.SetStudentDrawing(context.Background(), "room", false); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot("room").Version != 0 {
		t.Fatal("no-op permissions created history")
	}
	m.Absorb("room", []map[string]any{{"id": "x", "version": 1}})
	if err := m.Flush(ctx, "room"); !errors.Is(err, context.Canceled) {
		t.Fatalf("flush ignored cancellation: %v", err)
	}
	m.Reset()
}

func TestWebsocketRequestCancellationClosesTransport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := NewManager()
	h := NewHTTPHandler(m, func(context.Context, string) (Identity, error) { return Identity{Role: "teacher"}, nil }, nil, 0)
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { defer close(done); h.ServeHTTP(w, r.WithContext(ctx)) }))
	defer server.Close()
	conn := dialBoard(t, server.URL, "room", "valid", "")
	defer conn.Close()
	writeBoardMessage(t, conn, `{"type":"join"}`)
	_ = readBoardMessage(t, conn)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled request stuck reading")
	}
	if m.ActiveRooms() != 0 {
		t.Fatal("canceled request left peer")
	}
}
