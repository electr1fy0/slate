package boards

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

type testSocket struct {
	mu     sync.Mutex
	frames []string
	fail   bool
	closed int
}

func (s *testSocket) SendText(value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail {
		return context.Canceled
	}
	s.frames = append(s.frames, value)
	return nil
}

func (s *testSocket) Close(code int) error {
	s.mu.Lock()
	s.closed = code
	s.mu.Unlock()
	return nil
}

func (s *testSocket) messages() []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]map[string]any, 0, len(s.frames))
	for _, frame := range s.frames {
		var message map[string]any
		if err := json.Unmarshal([]byte(frame), &message); err == nil {
			result = append(result, message)
		}
	}
	return result
}

func TestManagerAbsorbLastWriterWins(t *testing.T) {
	manager := NewManager()
	if got := manager.Absorb("room", []map[string]any{
		{"id": "e1", "version": 1},
		{"id": "e1", "version": 0},
		{"id": "e2", "version": 4},
		{"version": 99},
	}); got != 2 {
		t.Fatalf("applied = %d, want 2", got)
	}
	if got := manager.Absorb("room", []map[string]any{{"id": "e1", "version": 7}}); got != 1 {
		t.Fatalf("newer update applied = %d, want 1", got)
	}
	snapshot := manager.Snapshot("room")
	if len(snapshot.Elements) != 2 || snapshot.Elements[0]["version"] != 7 {
		t.Fatalf("unexpected snapshot: %#v", snapshot.Elements)
	}
}

func TestManagerPeerIdentityAndIsolation(t *testing.T) {
	manager := NewManager()
	first, second := &testSocket{}, &testSocket{}
	firstID := manager.Connect("a", first, "", "teacher")
	secondID := manager.Connect("b", second, "second", "wizard")
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("peer ids should be unique: %q %q", firstID, secondID)
	}
	peer := manager.Identify("a", first, "123456789012345678901234567890123456789012", "student")
	if peer.Name != "1234567890123456789012345678901234567890" || peer.Role != "student" {
		t.Fatalf("identity not normalized: %#v", peer)
	}
	if got := len(manager.Peers("a", nil)); got != 1 {
		t.Fatalf("room a peers = %d, want 1", got)
	}
	if got := len(manager.Peers("b", nil)); got != 1 || manager.ActiveRooms() != 2 {
		t.Fatalf("rooms not isolated: peers=%d rooms=%d", got, manager.ActiveRooms())
	}
}

func TestSessionProtocolFrames(t *testing.T) {
	manager := NewManager()
	teacherSocket := &testSocket{}
	teacher := NewSession(manager, "room", teacherSocket, Identity{Name: "Teacher", Role: "teacher"})
	if _, err := teacher.HandleFrame(context.Background(), `{"type":"join"}`, 1024); err != nil {
		t.Fatal(err)
	}
	studentSocket := &testSocket{}
	student := NewSession(manager, "room", studentSocket, Identity{Name: "Student", Role: "student"})
	if _, err := student.HandleFrame(context.Background(), `{"type":"join","name":"Bob"}`, 1024); err != nil {
		t.Fatal(err)
	}
	if _, err := teacher.HandleFrame(context.Background(), `{"type":"elements","elements":[{"id":"e1","version":2}]}`, 1024); err != nil {
		t.Fatal(err)
	}
	if _, err := student.HandleFrame(context.Background(), `{"type":"cursor","x":10,"y":20,"tool":"rectangle"}`, 1024); err != nil {
		t.Fatal(err)
	}
	messages := studentSocket.messages()
	if len(messages) < 2 || messages[0]["type"] != "init" {
		t.Fatalf("student init missing: %#v", messages)
	}
	if messages[1]["type"] != "elements" {
		t.Fatalf("student did not receive element relay: %#v", messages)
	}
	teacherMessages := teacherSocket.messages()
	if teacherMessages[len(teacherMessages)-1]["type"] != "cursor" {
		t.Fatalf("teacher did not receive cursor relay: %#v", teacherMessages)
	}
	joined := teacherMessages[1]
	if joined["type"] != "peer-joined" || joined["peer"].(map[string]any)["role"] != "student" {
		t.Fatalf("token role was not preserved: %#v", joined)
	}
}

func TestSessionErrorsAndOversizedFrame(t *testing.T) {
	manager := NewManager()
	socket := &testSocket{}
	session := NewSession(manager, "room", socket, Identity{Name: "A", Role: "student"})
	if closeCode, err := session.HandleFrame(context.Background(), "not-json", 128); closeCode != 0 || err != nil {
		t.Fatalf("malformed frame: close=%d err=%v", closeCode, err)
	}
	if closeCode, err := session.HandleFrame(context.Background(), `"string"`, 128); closeCode != 0 || err != nil {
		t.Fatalf("non-object frame: close=%d err=%v", closeCode, err)
	}
	if closeCode, err := session.HandleFrame(context.Background(), `{"type":"elements","elements":"nope"}`, 128); closeCode != 0 || err != nil {
		t.Fatalf("bad elements frame: close=%d err=%v", closeCode, err)
	}
	if closeCode, err := session.HandleFrame(context.Background(), `{"type":"ping"}`, 4); closeCode != ClosePayloadTooLarge || err != nil {
		t.Fatalf("oversized frame: close=%d err=%v", closeCode, err)
	}
	messages := socket.messages()
	if len(messages) != 4 || messages[0]["code"] != ErrorMalformedJSON || messages[3]["code"] != ErrorPayloadTooLarge {
		t.Fatalf("unexpected error frames: %#v", messages)
	}
}

func TestEvictStaleClosesAndAnnounces(t *testing.T) {
	manager := NewManager()
	now := time.Unix(100, 0)
	manager.SetClock(func() time.Time { return now })
	live, stale := &testSocket{}, &testSocket{}
	manager.Connect("room", live, "live", "student")
	manager.Connect("room", stale, "stale", "student")
	manager.SetLastSeen("room", live, now)
	manager.SetLastSeen("room", stale, now.Add(-time.Hour))
	if got := manager.EvictStale(context.Background(), time.Minute); got != 1 {
		t.Fatalf("evicted = %d, want 1", got)
	}
	if stale.closed != CloseIdleTimeout {
		t.Fatalf("stale close code = %d", stale.closed)
	}
	messages := live.messages()
	if len(messages) != 1 || messages[0]["type"] != "peer-left" {
		t.Fatalf("missing departure announcement: %#v", messages)
	}
}

func TestOriginForbidden(t *testing.T) {
	if OriginForbidden("", []string{"https://example.com"}) {
		t.Error("missing origin should be accepted")
	}
	if OriginForbidden("https://example.com", []string{"https://example.com"}) {
		t.Error("allow-listed origin should be accepted")
	}
	if !OriginForbidden("https://evil.example", []string{"https://example.com"}) {
		t.Error("unknown origin should be rejected")
	}
}
