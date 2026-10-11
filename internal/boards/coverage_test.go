package boards

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type contextJSONTransport struct{}

func (*contextJSONTransport) SendJSON(context.Context, any) error { return nil }

type jsonTransport struct{}

func (*jsonTransport) SendJSON(any) error { return nil }

type jsonWriterTransport struct{}

func (*jsonWriterTransport) WriteJSON(any) error { return nil }

type contextTextTransport struct{}

func (*contextTextTransport) SendText(context.Context, string) error { return nil }

type textTransport struct{}

func (*textTransport) SendText(string) error { return nil }

type textWriterTransport struct{}

func (*textWriterTransport) WriteText(string) error { return nil }

type contextCloseTransport struct {
	code   int
	reason string
}

func (s *contextCloseTransport) Close(_ context.Context, code int, reason string) error {
	s.code, s.reason = code, reason
	return nil
}

type reasonCloseTransport struct {
	code   int
	reason string
}

func (s *reasonCloseTransport) Close(code int, reason string) error {
	s.code, s.reason = code, reason
	return nil
}

type codeCloseTransport struct{ code int }

func (s *codeCloseTransport) Close(code int) error {
	s.code = code
	return nil
}

func TestManagerCoverageDefensivePaths(t *testing.T) {
	var nilManager *Manager
	nilManager.SetPersistence(NoopSnapshotStore{}, time.Second)

	store := newMemoryBoardStore()
	manager := NewManagerWithPersistence(store, time.Hour)
	manager.Absorb("pending", []map[string]any{{"id": "p", "version": 1}})
	manager.SetPersistence(NoopSnapshotStore{}, time.Hour)
	manager.SetPersistence(nil, time.Hour)
	manager.Reset()

	if got := manager.Connect("invalid", nil, "", ""); got != "" {
		t.Fatalf("nil socket id = %q", got)
	}
	if got := manager.Connect("invalid", []int{}, "", ""); got != "" {
		t.Fatalf("non-comparable socket id = %q", got)
	}
	if manager.Identify("missing", &testSocket{}, "name", "student") != nil {
		t.Fatal("identify created a missing room")
	}
	if manager.SetCursor("missing", &testSocket{}, nil) != nil {
		t.Fatal("cursor created a missing room")
	}
	if manager.Disconnect("missing", &testSocket{}) != nil {
		t.Fatal("disconnect found a missing room")
	}
	if manager.SetLastSeen("missing", &testSocket{}, time.Time{}) {
		t.Fatal("last-seen updated a missing room")
	}
	if got := manager.Snapshot("missing"); got.RoomID != "" {
		t.Fatalf("missing snapshot = %#v", got)
	}

	now := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	manager.SetClock(func() time.Time { return now })
	first, second := &testSocket{}, &testSocket{}
	manager.Connect("room", first, "", "teacher")
	manager.Connect("room", second, "student", "unknown")
	if manager.Identify("room", &testSocket{}, "nope", "student") != nil {
		t.Fatal("identify found an unknown socket")
	}
	peer := manager.Identify("room", first, "updated", "unknown")
	if peer == nil || peer.Name != "updated" || peer.Role != "student" {
		t.Fatalf("updated peer = %#v", peer)
	}
	if !manager.SetLastSeen("room", first, now) {
		t.Fatal("set last-seen failed for a connected peer")
	}
	cursor := map[string]any{"nested": map[string]any{"x": 1}}
	peer = manager.SetCursor("room", first, cursor)
	cursor["nested"].(map[string]any)["x"] = 2
	if peer == nil || peer.Cursor["nested"].(map[string]any)["x"] != 1 {
		t.Fatalf("cursor was not copied: %#v", peer)
	}
	if got := len(manager.Peers("room", &testSocket{})); got != 2 {
		t.Fatalf("unknown exclusion changed peers: %d", got)
	}
	if got := len(manager.Peers("room", first)); got != 1 {
		t.Fatalf("matching exclusion returned %d peers", got)
	}
	manager.Touch("missing", first)
	manager.Touch("room", &testSocket{})
	if manager.Disconnect("room", &testSocket{}) != nil {
		t.Fatal("disconnect found an unknown socket")
	}
	if manager.Disconnect("room", second) == nil || manager.Disconnect("room", first) == nil {
		t.Fatal("connected peers were not disconnected")
	}
	if manager.ActiveRooms() != 0 {
		t.Fatalf("active rooms after disconnect = %d", manager.ActiveRooms())
	}

	manager.SetClock(nil)
	manager.now = nil
	if manager.nowLocked().IsZero() || manager.nowTime().IsZero() {
		t.Fatal("nil clock did not fall back to wall time")
	}
	manager.SetPersistence(store, time.Hour)
	manager.Absorb("reset", []map[string]any{{"id": "e", "version": 1}})
	manager.Reset()
	if manager.ActiveRooms() != 0 || manager.hasPendingSave("reset") {
		t.Fatal("reset left board state or pending saves")
	}

	collisionManager := NewManager()
	peerSequence.Lock()
	peerSequence.n = 100
	peerSequence.Unlock()
	collisionManager.Connect("collision", &testSocket{}, "first", "student")
	peerSequence.Lock()
	peerSequence.n = 100
	peerSequence.Unlock()
	if got := collisionManager.Connect("collision", &testSocket{}, "second", "student"); got == "" {
		t.Fatal("collision retry did not produce a peer id")
	}
}

func TestManagerSendTransportAndCloneCoverage(t *testing.T) {
	manager := NewManager()
	message := map[string]any{"type": "ping"}
	transports := []any{
		&contextJSONTransport{}, &jsonTransport{}, &jsonWriterTransport{},
		&contextTextTransport{}, &textTransport{}, &textWriterTransport{},
	}
	for _, transport := range transports {
		if err := manager.Send(nil, transport, message); err != nil {
			t.Fatalf("send through %T: %v", transport, err)
		}
	}
	if err := manager.Send(context.Background(), struct{}{}, message); err == nil {
		t.Fatal("unsupported transport unexpectedly succeeded")
	}
	if err := manager.Send(context.Background(), struct{}{}, func() {}); err == nil {
		t.Fatal("unmarshalable message unexpectedly succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Send(canceled, &contextJSONTransport{}, message); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled send error = %v", err)
	}

	nested := map[string]any{
		"map":   map[string]any{"value": 1},
		"slice": []any{map[string]any{"value": 2}, "text"},
		"plain": 3,
	}
	copy := cloneMap(nested)
	nested["map"].(map[string]any)["value"] = 9
	nested["slice"].([]any)[0].(map[string]any)["value"] = 8
	if copy["map"].(map[string]any)["value"] != 1 || copy["slice"].([]any)[0].(map[string]any)["value"] != 2 {
		t.Fatalf("nested clone was shallow: %#v", copy)
	}
	if cloneMap(nil) != nil || cloneValue(3) != 3 || clonePeer(nil) != nil {
		t.Fatal("clone helpers mishandled scalar or nil values")
	}
	peer := clonePeer(&Peer{ID: "p", Cursor: map[string]any{"x": 1}})
	peer.Cursor["x"] = 2
	if peer.ID != "p" {
		t.Fatal("peer clone lost identity")
	}

	for _, value := range []any{nil, int(1), int8(1), int16(1), int32(1), int64(1), uint(1), uint8(1), uint16(1), uint32(1), uint64(1), float32(1), float64(1)} {
		if _, ok := versionValue(value); !ok {
			t.Fatalf("version %T rejected", value)
		}
	}
	if _, ok := versionValue(float32(math.NaN())); ok {
		t.Fatal("float32 NaN accepted")
	}
	if _, ok := versionValue(math.NaN()); ok {
		t.Fatal("float64 NaN accepted")
	}
	if _, ok := versionValue("1"); ok {
		t.Fatal("string version accepted")
	}
	if got, ok := stringValue("x"); !ok || got != "x" {
		t.Fatalf("stringValue string = %q %v", got, ok)
	}
	if _, ok := stringValue(1); ok {
		t.Fatal("stringValue accepted a non-string")
	}
}

func TestManagerBroadcastAndEvictionCoverage(t *testing.T) {
	manager := NewManager()
	if got := manager.Broadcast(nil, "missing", map[string]any{"x": 1}, nil); got != 0 {
		t.Fatalf("missing-room broadcast = %d", got)
	}
	good, failed := &testSocket{}, &testSocket{fail: true}
	manager.Connect("room", good, "good", "student")
	manager.Connect("room", failed, "failed", "student")
	if got := manager.Broadcast(context.Background(), "room", map[string]any{"type": "x"}, good); got != 0 {
		t.Fatalf("excluded broadcast = %d", got)
	}
	if manager.ActiveRooms() != 1 {
		t.Fatal("failed peer should not be removed by an excluded broadcast")
	}
	if got := manager.Broadcast(nil, "room", map[string]any{"type": "x"}, nil); got != 1 {
		t.Fatalf("broadcast sent = %d, want one good peer", got)
	}

	ctxCloser := &contextCloseTransport{}
	if err := closeSocket(context.Background(), ctxCloser, CloseIdleTimeout); err != nil || ctxCloser.reason != "idle timeout" {
		t.Fatalf("context closer = %v %#v", err, ctxCloser)
	}
	reasonCloser := &reasonCloseTransport{}
	if err := closeSocket(context.Background(), reasonCloser, CloseIdleTimeout); err != nil || reasonCloser.reason != "idle timeout" {
		t.Fatalf("reason closer = %v %#v", err, reasonCloser)
	}
	codeCloser := &codeCloseTransport{}
	if err := closeSocket(context.Background(), codeCloser, CloseIdleTimeout); err != nil || codeCloser.code != CloseIdleTimeout {
		t.Fatalf("code closer = %v %#v", err, codeCloser)
	}
	if err := closeSocket(context.Background(), struct{}{}, CloseIdleTimeout); err != nil {
		t.Fatalf("unsupported closer = %v", err)
	}

	now := time.Unix(1000, 0)
	manager.SetClock(func() time.Time { return now })
	stale, live := &testSocket{}, &testSocket{}
	manager.Connect("evict", stale, "stale", "student")
	manager.Connect("evict", live, "live", "student")
	manager.SetLastSeen("evict", stale, now.Add(-time.Hour))
	manager.SetLastSeen("evict", live, now)
	closed := false
	if got := manager.EvictStale(nil, -time.Second, func(_ context.Context, socket any, code int) error {
		closed = socket == stale && code == CloseIdleTimeout
		return nil
	}); got != 1 || !closed {
		t.Fatalf("custom eviction = %d closed=%v", got, closed)
	}
	if len(live.messages()) != 1 {
		t.Fatalf("live peer did not receive peer-left: %#v", live.messages())
	}

	canceledManager := NewManager()
	canceledManager.SetClock(func() time.Time { return now })
	canceledSocket := &testSocket{}
	canceledManager.Connect("cancel", canceledSocket, "stale", "student")
	canceledManager.SetLastSeen("cancel", canceledSocket, now.Add(-time.Hour))
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if got := canceledManager.EvictStale(canceled, time.Minute); got != 0 {
		t.Fatalf("canceled eviction = %d", got)
	}

	duplicateManager := NewManager()
	duplicateManager.SetClock(func() time.Time { return now })
	duplicate := &testSocket{}
	duplicateManager.Connect("duplicate", duplicate, "duplicate", "student")
	duplicateManager.Connect("duplicate", duplicate, "duplicate", "student")
	duplicateManager.SetLastSeen("duplicate", duplicate, now.Add(-time.Hour))
	if got := duplicateManager.EvictStale(context.Background(), time.Minute); got != 1 {
		t.Fatalf("duplicate eviction = %d", got)
	}
	if duplicate.closed != CloseIdleTimeout {
		t.Fatalf("duplicate close code = %d", duplicate.closed)
	}
}

func TestManagerSnapshotAndAbsorbEdgeCoverage(t *testing.T) {
	manager := NewManager()
	manager.mu.Lock()
	r := &room{
		peers:        make(map[any]*Peer),
		elements:     make(map[string]map[string]any),
		createdAt:    time.Unix(1, 0),
		updatedAt:    time.Unix(2, 0),
		peerOrder:    []any{"missing-peer"},
		elementOrder: []string{"missing-element"},
	}
	manager.rooms["edge"] = r
	if got := manager.roomSnapshotLocked("edge", r); len(got.Peers) != 0 || len(got.Elements) != 0 {
		t.Fatalf("empty edge snapshot = %#v", got)
	}
	if manager.absorbElementLocked(r, map[string]any{"id": "bad", "version": "bad"}) {
		t.Fatal("bad version accepted")
	}
	r.elements["bad-current"] = map[string]any{"id": "bad-current", "version": struct{}{}}
	if !manager.absorbElementLocked(r, map[string]any{"id": "bad-current", "version": 1}) {
		t.Fatal("valid update after bad current version rejected")
	}
	manager.mu.Unlock()

	if got := manager.Absorb("empty", nil); got != 0 {
		t.Fatalf("empty absorb = %d", got)
	}
	manager.Absorb("copy", []map[string]any{{"id": "e", "version": 1, "nested": map[string]any{"x": 1}}})
	exported, err := manager.ExportSnapshot("copy")
	if err != nil || len(exported.Elements) != 1 {
		t.Fatalf("export = %#v %v", exported, err)
	}
	exported.Elements[0]["nested"].(map[string]any)["x"] = 9
	if manager.Snapshot("copy").Elements[0]["nested"].(map[string]any)["x"] != 1 {
		t.Fatal("snapshot export was not isolated")
	}
}

type protocolFailSocket struct{}

func (*protocolFailSocket) SendText(string) error { return context.Canceled }

func TestProtocolCoverage(t *testing.T) {
	protocolErr := &ProtocolError{Code: "CODE", Message: "message"}
	if got := protocolErr.Error(); got != "CODE: message" {
		t.Fatalf("protocol error string = %q", got)
	}
	if frame, err := ParseFrame(`{"type":"ping"}`); err != nil || frame["type"] != "ping" {
		t.Fatalf("valid frame = %#v %v", frame, err)
	}

	if session := NewSession(nil, "room", []int{}, Identity{}); session != nil {
		t.Fatal("non-comparable session socket was accepted")
	}
	if session := NewSession(nil, "room", &testSocket{}, Identity{Role: "unknown"}); session == nil || session.Identity.Role != "student" {
		t.Fatalf("nil-manager session = %#v", session)
	}
	var nilSession *Session
	if _, err := nilSession.HandleFrame(context.Background(), `{}`, 0); err == nil {
		t.Fatal("nil session unexpectedly handled a frame")
	}
	if _, err := (&Session{}).HandleFrame(context.Background(), `{}`, 0); err == nil {
		t.Fatal("session without manager unexpectedly handled a frame")
	}

	manager := NewManager()
	socket := &testSocket{}
	session := NewSession(manager, "room", socket, Identity{Name: "Token", Role: "teacher"})
	if _, err := session.HandleFrame(context.Background(), `{"type":"ping"}`, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.HandleFrame(context.Background(), `{"type":"unknown","value":"x"}`, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.HandleFrame(context.Background(), `{"value":true}`, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.HandleFrame(context.Background(), `{"value":false}`, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.HandleFrame(context.Background(), `{"value":null}`, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.HandleFrame(context.Background(), `{"type":{"nested":1}}`, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.HandleFrame(context.Background(), `{"type":1}`, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.HandleFrame(context.Background(), `{"type":true}`, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.HandleFrame(context.Background(), `{"type":false}`, 0); err != nil {
		t.Fatal(err)
	}

	if _, err := session.HandleFrame(context.Background(), `{"type":"join","name":123}`, 0); err != nil {
		t.Fatal(err)
	}
	if !session.Joined {
		t.Fatal("join did not mark the session joined")
	}
	if _, err := session.HandleFrame(context.Background(), `{"type":"elements","elements":["not-an-element"]}`, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := session.HandleFrame(context.Background(), `{"type":"cursor"}`, 0); err != nil {
		t.Fatal(err)
	}
	manager.Disconnect("room", socket)
	if _, err := session.HandleFrame(context.Background(), `{"type":"cursor"}`, 0); err != nil {
		t.Fatal(err)
	}

	failing := NewSession(NewManager(), "room", &protocolFailSocket{}, Identity{})
	if closeCode, err := failing.HandleFrame(context.Background(), "12345", 4); closeCode != ClosePayloadTooLarge || err == nil {
		t.Fatalf("oversized send failure = %d %v", closeCode, err)
	}
	joinFail := NewSession(NewManager(), "room", &protocolFailSocket{}, Identity{})
	if _, err := joinFail.HandleFrame(context.Background(), `{"type":"join"}`, 0); err == nil {
		t.Fatal("join send failure was ignored")
	}
	if _, err := NewSession(NewManager(), "room", &testSocket{}, Identity{}).HandleFrame(context.Background(), `{"type":"join"}`, 0); err != nil {
		t.Fatal(err)
	}

	persistenceStore := &configurableBoardStore{loadErr: errors.New("temporary unavailable")}
	persistenceSession := NewSession(NewManagerWithPersistence(persistenceStore, time.Hour), "room", &testSocket{}, Identity{})
	if _, err := persistenceSession.HandleFrame(context.Background(), `{"type":"join"}`, 0); err != nil {
		t.Fatal(err)
	}

	missingPeer := &Session{Manager: NewManager(), RoomID: "missing", PeerID: "never"}
	if missingPeer.findPeer() != nil {
		t.Fatal("findPeer found a peer in a missing room")
	}
	missingPeer.Close(context.Background())
	var nilClose *Session
	nilClose.Close(context.Background())
	manager.Disconnect("room", socket)
	session.Close(context.Background())
	session.Close(context.Background())

	if got := valueRepr(func() {}); got == "" {
		t.Fatal("unrepresentable value got an empty representation")
	}
}

type configurableBoardStore struct {
	mu              sync.Mutex
	loaded          *BoardSnapshot
	loadErr         error
	loadStarted     chan struct{}
	loadStartedOnce sync.Once
	loadBlock       chan struct{}
	saveErr         error
	list            []BoardVersion
	listErr         error
	historical      *BoardSnapshot
	loadVersionErr  error
	saves           []BoardSnapshot
}

func (s *configurableBoardStore) Load(_ context.Context, _ string) (*BoardSnapshot, error) {
	if s.loadStarted != nil {
		s.loadStartedOnce.Do(func() { close(s.loadStarted) })
	}
	if s.loadBlock != nil {
		<-s.loadBlock
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return nil, s.loadErr
	}
	if s.loaded == nil {
		return nil, ErrSnapshotNotFound
	}
	copy := s.loaded.Clone()
	return &copy, nil
}

func (s *configurableBoardStore) Save(_ context.Context, snapshot BoardSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saves = append(s.saves, snapshot.Clone())
	return nil
}

func (s *configurableBoardStore) ListVersions(_ context.Context, _ string, _ int) ([]BoardVersion, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]BoardVersion(nil), s.list...), nil
}

func (s *configurableBoardStore) LoadVersion(_ context.Context, _ string, _ int64) (*BoardSnapshot, error) {
	if s.loadVersionErr != nil {
		return nil, s.loadVersionErr
	}
	if s.historical == nil {
		return nil, nil
	}
	copy := s.historical.Clone()
	return &copy, nil
}

func TestHTTPHelpersAndServeHTTPBranches(t *testing.T) {
	origins := []string{"https://one.example"}
	handler := NewHandler(HandlerConfig{AllowedOrigins: origins})
	if handler.manager == nil || handler.maxMessageBytes != 64*1024 {
		t.Fatalf("default handler = %#v", handler)
	}
	origins[0] = "changed"
	if handler.allowedOrigins[0] != "https://one.example" {
		t.Fatal("handler did not copy allowed origins")
	}
	if !handler.upgrader.CheckOrigin(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Fatal("upgrader origin check was not permissive")
	}

	boolHandler := NewHTTPHandlerWithBoolResolver(NewManager(), func(_ context.Context, token string) (Identity, bool) {
		return Identity{Name: token, Role: "teacher"}, token == "ok"
	}, nil, 1)
	identity, err := boolHandler.resolveToken(context.Background(), "ok")
	if err != nil || identity.Name != "ok" {
		t.Fatalf("boolean resolver success = %#v %v", identity, err)
	}
	if _, err := boolHandler.resolveToken(context.Background(), "bad"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("boolean resolver failure = %v", err)
	}
	if nilResolver := NewHTTPHandlerWithBoolResolver(nil, nil, nil, 0); nilResolver.resolveToken != nil {
		t.Fatal("nil boolean resolver was adapted")
	}

	requests := []struct {
		path  string
		value any
		want  string
	}{
		{path: "/ignored", value: "from-context", want: "from-context"},
		{path: "/prefix/ws/board/room/tail", value: 123, want: "room"},
		{path: "/prefix/ws/board/room/tail", want: "room"},
		{path: "/other", want: ""},
	}
	for _, test := range requests {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		if test.value != nil {
			request = request.WithContext(context.WithValue(request.Context(), RoomIDContextKey{}, test.value))
		}
		if got := roomIDFromRequest(request); got != test.want {
			t.Fatalf("room id for %s = %q, want %q", test.path, got, test.want)
		}
	}
	if got := closeReason(1234); got == "" {
		t.Fatal("default close reason is empty")
	}
	if got := closeReason(CloseIdleTimeout); got != "idle timeout" {
		t.Fatalf("idle close reason = %q", got)
	}

	var nilHandler *HTTPHandler
	recorder := httptest.NewRecorder()
	nilHandler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code == 101 {
		t.Fatal("non-websocket request unexpectedly upgraded")
	}

	noResolverServer := httptest.NewServer(NewHandler(HandlerConfig{}))
	noResolver := dialBoardWithoutFatal(t, noResolverServer.URL, "room", "token", "")
	if noResolver.err != nil || noResolver.conn == nil {
		t.Fatalf("nil resolver handshake = %#v", noResolver)
	}
	assertCloseCode(t, noResolver.conn, CloseInvalidToken)
	noResolver.conn.Close()
	noResolverServer.Close()

	invalidTokenServer := httptest.NewServer(NewHTTPHandler(NewManager(), func(_ context.Context, _ string) (Identity, error) {
		return Identity{}, ErrInvalidToken
	}, nil, 64*1024))
	invalidToken := dialBoardWithoutFatal(t, invalidTokenServer.URL, "room", "bad", "")
	if invalidToken.err != nil || invalidToken.conn == nil {
		t.Fatalf("resolver-error handshake = %#v", invalidToken)
	}
	assertCloseCode(t, invalidToken.conn, CloseInvalidToken)
	invalidToken.conn.Close()
	invalidTokenServer.Close()

	newSessionServer := func(factory func(*Manager, string, any, Identity) *Session) *httptest.Server {
		manager := NewManager()
		resolver := TokenResolver(func(context.Context, string) (Identity, error) {
			return Identity{Name: "test", Role: "student"}, nil
		})
		handler := NewHTTPHandler(manager, resolver, nil, 64*1024)
		handler.sessionFactory = factory
		mux := http.NewServeMux()
		mux.Handle("/ws/board/", handler)
		return httptest.NewServer(mux)
	}
	nilSessionServer := newSessionServer(func(*Manager, string, any, Identity) *Session { return nil })
	nilSession := dialBoardWithoutFatal(t, nilSessionServer.URL, "room", "valid", "")
	if nilSession.err != nil || nilSession.conn == nil {
		t.Fatalf("nil session handshake = %#v", nilSession)
	}
	assertCloseCode(t, nilSession.conn, CloseInvalidToken)
	nilSession.conn.Close()
	nilSessionServer.Close()

	failedServer := newSessionServer(func(*Manager, string, any, Identity) *Session {
		return &Session{Manager: NewManager(), RoomID: "room", Socket: &protocolFailSocket{}, PeerID: "fail"}
	})
	failedSession := dialBoard(t, failedServer.URL, "room", "valid", "")
	writeBoardMessage(t, failedSession, `{"type":"ping"}`)
	failedSession.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := failedSession.ReadMessage(); err == nil {
		t.Fatal("handler did not close after a session send error")
	}
	failedSession.Close()
	failedServer.Close()

	binaryServer, _ := testHTTPServer(t, 64*1024)
	binaryConn := dialBoard(t, binaryServer.URL, "room", "valid", "")
	if err := binaryConn.WriteMessage(websocket.BinaryMessage, []byte("binary")); err != nil {
		t.Fatal(err)
	}
	assertCloseCode(t, binaryConn, websocket.CloseUnsupportedData)
	binaryConn.Close()
	binaryServer.Close()
}

func TestPersistenceCoverageAndErrors(t *testing.T) {
	noop := NoopSnapshotStore{}
	if snapshot, err := noop.Load(context.Background(), "room"); !errors.Is(err, ErrSnapshotNotFound) || snapshot != nil {
		t.Fatalf("noop load = %#v %v", snapshot, err)
	}
	if err := noop.Save(context.Background(), BoardSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if versions, err := noop.ListVersions(context.Background(), "room", 1); err != nil || versions != nil {
		t.Fatalf("noop versions = %#v %v", versions, err)
	}
	if snapshot, err := noop.LoadVersion(context.Background(), "room", 1); !errors.Is(err, ErrSnapshotNotFound) || snapshot != nil {
		t.Fatalf("noop version = %#v %v", snapshot, err)
	}

	snapshot := BoardSnapshot{RoomMetadata: RoomMetadata{RoomID: "room"}, Elements: []map[string]any{{
		"id": "e", "version": 1, "nested": map[string]any{"x": 1},
	}}}
	copy := snapshot.Clone()
	snapshot.Elements[0]["nested"].(map[string]any)["x"] = 9
	if copy.Elements[0]["nested"].(map[string]any)["x"] != 1 {
		t.Fatal("snapshot clone was shallow")
	}
	jsonBytes, err := (BoardSnapshot{}).JSON()
	if err != nil || len(jsonBytes) == 0 {
		t.Fatalf("zero snapshot json = %s %v", jsonBytes, err)
	}
	if _, err := (BoardSnapshot{Elements: []map[string]any{{"bad": func() {}}}}).JSON(); err == nil {
		t.Fatal("unmarshalable snapshot unexpectedly encoded")
	}

	manager := NewManager()
	if err := manager.persistSnapshot(nil, snapshot); err != nil {
		t.Fatal(err)
	}
	configuredNoop := NewManagerWithPersistence(NoopSnapshotStore{}, time.Hour)
	if err := configuredNoop.persistSnapshot(nil, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := manager.Flush(nil, "missing"); err != nil {
		t.Fatal(err)
	}
	if err := manager.FlushAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if versions, err := manager.ListVersions(nil, "room", 1); err != nil || versions != nil {
		t.Fatalf("in-memory versions = %#v %v", versions, err)
	}
	if got, err := manager.ExportSnapshot("missing"); !errors.Is(err, ErrRoomNotFound) || got.RoomID != "" {
		t.Fatalf("missing export = %#v %v", got, err)
	}
	if _, err := manager.ExportSnapshotJSON("missing"); !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("missing json export = %v", err)
	}
	if _, ok := manager.Metadata("missing"); ok {
		t.Fatal("missing metadata reported as present")
	}
	var nilManager *Manager
	if _, err := nilManager.ExportSnapshot("room"); !errors.Is(err, ErrRoomNotFound) {
		t.Fatalf("nil export = %v", err)
	}
	if _, ok := nilManager.Metadata("room"); ok {
		t.Fatal("nil metadata reported as present")
	}
	if err := nilManager.Resume(context.Background(), "room"); !errors.Is(err, ErrPersistenceUnavailable) {
		t.Fatalf("nil resume = %v", err)
	}

	readErr := errors.New("read failed")
	failedLoad := &configurableBoardStore{loadErr: readErr}
	failedManager := NewManagerWithPersistence(failedLoad, time.Hour)
	if err := failedManager.Resume(nil, "room"); !errors.Is(err, readErr) {
		t.Fatalf("failed resume = %v", err)
	}
	if err := failedManager.Resume(context.Background(), "room"); !errors.Is(err, readErr) {
		t.Fatalf("failed resume retry = %v", err)
	}
	if err := failedManager.Flush(nil, "never-loaded"); !errors.Is(err, nil) {
		t.Fatalf("empty flush = %v", err)
	}

	loadedAt := time.Unix(20, 0)
	loadedStore := &configurableBoardStore{loaded: &BoardSnapshot{
		SchemaVersion: 0,
		RoomMetadata:  RoomMetadata{Version: 4, CreatedAt: loadedAt, UpdatedAt: loadedAt.Add(time.Minute)},
		Elements:      []map[string]any{{"id": "persisted", "version": 2}},
	}}
	loadedManager := NewManagerWithPersistence(loadedStore, time.Hour)
	loadedManager.Absorb("room", []map[string]any{{"id": "local", "version": 1}})
	if err := loadedManager.Resume(nil, "room"); err != nil {
		t.Fatal(err)
	}
	if err := loadedManager.Resume(context.Background(), "room"); err != nil {
		t.Fatal(err)
	}
	if loadedManager.Snapshot("room").Version != 4 {
		t.Fatalf("loaded version = %d", loadedManager.Snapshot("room").Version)
	}
	if !loadedManager.hasPendingSave("room") {
		t.Fatal("resume did not refresh pending save")
	}

	asyncErr := errors.New("save failed")
	asyncStore := &configurableBoardStore{saveErr: asyncErr}
	asyncManager := NewManagerWithPersistence(asyncStore, time.Hour)
	asyncManager.Absorb("room", []map[string]any{{"id": "e", "version": 1}})
	asyncManager.persistenceMu.Lock()
	generation := asyncManager.pendingSaves["room"].generation
	asyncManager.persistenceMu.Unlock()
	asyncManager.flushScheduledSnapshot("missing", generation)
	asyncManager.flushScheduledSnapshot("room", generation+1)
	asyncManager.flushScheduledSnapshot("room", generation)
	if !errors.Is(asyncManager.LastPersistenceError("room"), asyncErr) {
		t.Fatalf("async persistence error = %v", asyncManager.LastPersistenceError("room"))
	}
	if err := asyncManager.Flush(context.Background(), "room"); !errors.Is(err, asyncErr) {
		t.Fatalf("explicit persistence error = %v", err)
	}
	asyncManager.recordPersistenceError("room", nil)
	if asyncManager.LastPersistenceError("room") != nil {
		t.Fatal("persistence error was not cleared")
	}
	var emptyManager Manager
	emptyManager.recordPersistenceError("room", asyncErr)
	if !errors.Is(emptyManager.LastPersistenceError("room"), asyncErr) {
		t.Fatal("recordPersistenceError did not initialize its map")
	}

	manualManager := NewManagerWithPersistence(&configurableBoardStore{}, time.Hour)
	manualManager.persistenceMu.Lock()
	manualManager.pendingSaves["manual"] = &pendingBoardSave{snapshot: snapshot}
	manualManager.persistenceMu.Unlock()
	if pending := manualManager.takePendingSave("manual"); pending == nil {
		t.Fatal("manual pending save was not taken")
	}
	if manualManager.takePendingSave("manual") != nil {
		t.Fatal("missing pending save unexpectedly returned a value")
	}
	manualManager.cancelPendingSave("manual")
	manualManager.scheduleSnapshotSave(snapshot, nil)
	manualManager.cancelPendingSave("room")
	if manualManager.hasPendingSave("room") {
		t.Fatal("pending save was not cancelled")
	}

	fallbackStore := &configurableBoardStore{}
	fallbackManager := NewManagerWithPersistence(fallbackStore, time.Hour)
	fallbackManager.Absorb("gone", []map[string]any{{"id": "e", "version": 1}})
	fallbackManager.mu.Lock()
	delete(fallbackManager.rooms, "gone")
	fallbackManager.mu.Unlock()
	if err := fallbackManager.Flush(nil, "gone"); err != nil {
		t.Fatal(err)
	}

	// Exercise both the successful and error returns of FlushAll, including
	// the union of live rooms and rooms represented only by pending saves.
	flushStore := &configurableBoardStore{}
	flushManager := NewManagerWithPersistence(flushStore, time.Hour)
	flushManager.Absorb("b", []map[string]any{{"id": "b", "version": 1}})
	flushManager.Absorb("a", []map[string]any{{"id": "a", "version": 1}})
	if err := flushManager.FlushAll(nil); err != nil {
		t.Fatal(err)
	}
	flushStore.saveErr = asyncErr
	if err := flushManager.FlushAll(context.Background()); !errors.Is(err, asyncErr) {
		t.Fatalf("FlushAll error = %v", err)
	}

	listStore := &configurableBoardStore{list: []BoardVersion{{RoomID: "room", Version: 1}}}
	listManager := NewManagerWithPersistence(listStore, time.Hour)
	versions, err := listManager.ListVersions(nil, "room", 1)
	if err != nil || len(versions) != 1 {
		t.Fatalf("listed versions = %#v %v", versions, err)
	}
	listStore.listErr = errors.New("list failed")
	if _, err := listManager.ListVersions(context.Background(), "room", 1); err == nil {
		t.Fatal("list error was ignored")
	}
}

func TestPersistenceResumeConcurrencyAndRestoreCoverage(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	store := &configurableBoardStore{
		loadStarted: started,
		loadBlock:   block,
		loaded:      &BoardSnapshot{RoomMetadata: RoomMetadata{Version: 1}, Elements: []map[string]any{{"id": "e", "version": 1}}},
	}
	manager := NewManagerWithPersistence(store, time.Hour)
	firstDone := make(chan error, 1)
	go func() { firstDone <- manager.Resume(context.Background(), "room") }()
	<-started
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.Resume(canceled, "room"); !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting resume cancellation = %v", err)
	}
	waitingDone := make(chan error, 1)
	go func() { waitingDone <- manager.Resume(context.Background(), "room") }()
	close(block)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-waitingDone; err != nil {
		t.Fatal(err)
	}

	high := &configurableBoardStore{historical: &BoardSnapshot{
		RoomMetadata: RoomMetadata{Version: 99}, Elements: []map[string]any{{"id": "old", "version": 5}},
	}}
	highManager := NewManagerWithPersistence(high, time.Hour)
	highManager.SetClock(func() time.Time { return time.Time{} })
	if _, err := highManager.Restore(nil, "room", 1); err != nil {
		t.Fatal(err)
	}
	if highManager.Snapshot("room").Version != 100 {
		t.Fatalf("high restore version = %d", highManager.Snapshot("room").Version)
	}

	historicalCreated := time.Unix(50, 0)
	createdStore := &configurableBoardStore{historical: &BoardSnapshot{
		RoomMetadata: RoomMetadata{Version: 1, CreatedAt: historicalCreated}, Elements: []map[string]any{{"id": "old", "version": 1}},
	}}
	createdManager := NewManagerWithPersistence(createdStore, time.Hour)
	createdManager.SetClock(func() time.Time { return time.Time{} })
	if _, err := createdManager.Restore(nil, "room", 1); err != nil {
		t.Fatal(err)
	}
	if !createdManager.Snapshot("room").CreatedAt.Equal(historicalCreated) {
		t.Fatalf("historical created-at fallback failed: %#v", createdManager.Snapshot("room"))
	}

	noStore := NewManager()
	if _, err := noStore.Restore(context.Background(), "room", 1); !errors.Is(err, ErrPersistenceUnavailable) {
		t.Fatalf("restore without store = %v", err)
	}
	restoreReadErr := errors.New("restore read failed")
	restoreLoadErr := NewManagerWithPersistence(&configurableBoardStore{loadErr: restoreReadErr}, time.Hour)
	if _, err := restoreLoadErr.Restore(context.Background(), "room", 1); !errors.Is(err, restoreReadErr) {
		t.Fatalf("restore load error = %v", err)
	}
	versionErr := errors.New("version failed")
	versionManager := NewManagerWithPersistence(&configurableBoardStore{loadVersionErr: versionErr}, time.Hour)
	if _, err := versionManager.Restore(context.Background(), "room", 1); !errors.Is(err, versionErr) {
		t.Fatalf("restore version error = %v", err)
	}
	nilVersionManager := NewManagerWithPersistence(&configurableBoardStore{}, time.Hour)
	if _, err := nilVersionManager.Restore(context.Background(), "room", 1); !errors.Is(err, ErrSnapshotNotFound) {
		t.Fatalf("nil historical restore = %v", err)
	}

	saveErr := errors.New("restore save failed")
	saveFailStore := &configurableBoardStore{
		saveErr:    saveErr,
		historical: &BoardSnapshot{RoomMetadata: RoomMetadata{Version: 1}, Elements: []map[string]any{{"id": "e", "version": 1}}},
	}
	saveFailManager := NewManagerWithPersistence(saveFailStore, time.Hour)
	if _, err := saveFailManager.Restore(context.Background(), "room", 1); !errors.Is(err, saveErr) {
		t.Fatalf("restore save error = %v", err)
	}
	if !saveFailManager.hasPendingSave("room") {
		t.Fatal("failed restore did not leave a retryable save")
	}

	mergeManager := NewManager()
	mergeManager.mu.Lock()
	mergeRoom := &room{elements: make(map[string]map[string]any), createdAt: time.Unix(10, 0), updatedAt: time.Unix(10, 0)}
	mergeManager.mergeLoadedSnapshotLocked("room", mergeRoom, BoardSnapshot{
		SchemaVersion: 0,
		RoomMetadata:  RoomMetadata{CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(20, 0), Version: 3},
		Elements:      []map[string]any{{"id": "e", "version": 1}, {"version": 2}},
	})
	mergeManager.mu.Unlock()
	if mergeRoom.version != 3 || len(mergeRoom.elements) != 1 {
		t.Fatalf("merged room = %#v", mergeRoom)
	}
}
