package boards

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
)

// ProtocolError is a recoverable board-frame error. The server sends it as an
// error frame and keeps the connection open, except for an oversized frame.
type ProtocolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ProtocolError) Error() string { return e.Code + ": " + e.Message }

func errorFrame(code, message string) map[string]any {
	return map[string]any{"type": "error", "code": code, "message": message}
}

// ParseFrame parses and validates the top-level JSON object. It distinguishes
// malformed JSON from valid JSON values that are not objects.
func ParseFrame(raw string) (map[string]any, *ProtocolError) {
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return nil, &ProtocolError{Code: ErrorMalformedJSON, Message: "frame is not valid JSON"}
	}
	frame, ok := value.(map[string]any)
	if !ok {
		return nil, &ProtocolError{Code: ErrorFrameNotObject, Message: "frame must be a JSON object"}
	}
	return frame, nil
}

// Identity is supplied by the authenticated token. Clients can change only
// Name through a join frame; Role is always restored from this value.
type Identity struct {
	Name string
	Role string
}

// Session owns a single room connection. It deliberately does not read from a
// transport; integrations can use HandleFrame from their receive loop and
// translate closeCode into the transport's protocol close operation.
type Session struct {
	Manager  *Manager
	RoomID   string
	Socket   any
	PeerID   string
	Identity Identity
	Joined   bool
	closed   atomic.Bool
}

// NewSession connects a socket and returns a protocol session. A nil result
// indicates that socket is not a comparable map key.
func NewSession(manager *Manager, roomID string, socket any, identity Identity) *Session {
	if manager == nil {
		manager = NewManager()
	}
	identity.Role = normaliseRole(identity.Role)
	peerID := manager.Connect(roomID, socket, identity.Name, identity.Role)
	if peerID == "" {
		return nil
	}
	return &Session{Manager: manager, RoomID: roomID, Socket: socket, PeerID: peerID,
		Identity: identity}
}

// HandleFrame processes one text frame. It returns closeCode=0 for a live
// session, or ClosePayloadTooLarge when the caller must close the transport.
// Transport errors from sending a response are returned to the receive loop.
func (s *Session) HandleFrame(ctx context.Context, raw string, maxBytes int) (closeCode int, err error) {
	if s == nil || s.Manager == nil {
		return 0, fmt.Errorf("nil board session")
	}
	if s.closed.Load() || !s.registered() {
		return 0, fmt.Errorf("board session is closed")
	}
	s.Manager.Touch(s.RoomID, s.Socket)
	if maxBytes > 0 && len([]byte(raw)) > maxBytes {
		if err := s.Manager.Send(ctx, s.Socket,
			errorFrame(ErrorPayloadTooLarge, "frame too large")); err != nil {
			return ClosePayloadTooLarge, err
		}
		return ClosePayloadTooLarge, nil
	}
	frame, protocolErr := ParseFrame(raw)
	if protocolErr != nil {
		return 0, s.Manager.Send(ctx, s.Socket, errorFrame(protocolErr.Code, protocolErr.Message))
	}

	kind, _ := frame["type"].(string)
	switch kind {
	case "ping":
		return 0, s.Manager.Send(ctx, s.Socket, map[string]any{"type": "pong"})

	case "join":
		s.Manager.controlMu.Lock()
		defer s.Manager.controlMu.Unlock()
		previousPeer := s.findPeer()
		requestedName := s.Identity.Name
		if value, exists := frame["name"]; exists {
			// The handler accepts any JSON value after stringification. Name is
			// clamped by Identify, so preserve that forgiving behavior.
			if text, ok := value.(string); ok {
				requestedName = text
			} else if value != nil {
				requestedName = fmt.Sprint(value)
			}
		}
		s.Manager.Identify(s.RoomID, s.Socket, requestedName, s.Identity.Role)
		if err := s.Manager.Resume(ctx, s.RoomID); err != nil {
			return 0, s.Manager.Send(ctx, s.Socket, errorFrame(
				ErrorPersistence, "board snapshot is temporarily unavailable"))
		}
		snapshot := s.Manager.Snapshot(s.RoomID)
		peers := s.Manager.Peers(s.RoomID, s.Socket)
		if err := s.Manager.Send(ctx, s.Socket, map[string]any{
			"type": "init", "you": s.PeerID, "elements": snapshot.Elements,
			"peers": peers, "room": snapshot.RoomMetadata,
		}); err != nil {
			return 0, err
		}
		alreadyJoined := s.Joined
		s.Joined = true
		peer := s.findPeer()
		if !alreadyJoined || previousPeer == nil || (peer != nil && (previousPeer.Name != peer.Name || previousPeer.Role != peer.Role)) {
			s.Manager.Broadcast(ctx, s.RoomID,
				map[string]any{"type": "peer-joined", "peer": peer}, s.Socket)
		}
		return 0, nil

	case "elements":
		s.Manager.controlMu.Lock()
		defer s.Manager.controlMu.Unlock()
		values, ok := frame["elements"].([]any)
		if !ok {
			return 0, s.Manager.Send(ctx, s.Socket,
				errorFrame(ErrorBadElements, "elements must be a list"))
		}
		if !s.Joined || !s.Manager.CanDraw(s.RoomID, s.Socket) {
			return 0, s.Manager.Send(ctx, s.Socket, errorFrame("DRAWING_FORBIDDEN", "your teacher has disabled student drawing"))
		}
		incoming := make([]map[string]any, 0, len(values))
		for _, value := range values {
			if element, ok := value.(map[string]any); ok {
				incoming = append(incoming, element)
			}
		}
		if s.Manager.AbsorbContext(ctx, s.RoomID, incoming) > 0 {
			s.Manager.Broadcast(ctx, s.RoomID,
				map[string]any{"type": "elements", "elements": values}, s.Socket)
		}
		return 0, nil
	case "permissions":
		allowed, ok := frame["students_draw"].(bool)
		if !ok {
			return 0, s.Manager.Send(ctx, s.Socket, errorFrame("BAD_PERMISSIONS", "students_draw must be a boolean"))
		}
		if !s.Joined || s.Identity.Role != "teacher" {
			return 0, s.Manager.Send(ctx, s.Socket, errorFrame("PERMISSIONS_FORBIDDEN", "only a teacher can change drawing permissions"))
		}
		if err := s.Manager.SetStudentDrawing(ctx, s.RoomID, allowed); err != nil {
			return 0, s.Manager.Send(ctx, s.Socket, errorFrame(ErrorPersistence, "drawing permissions could not be saved"))
		}
		return 0, nil

	case "cursor":
		peer := s.Manager.SetCursor(s.RoomID, s.Socket, map[string]any{
			"x": frame["x"], "y": frame["y"], "tool": frame["tool"],
		})
		if peer != nil {
			s.Manager.Broadcast(ctx, s.RoomID,
				map[string]any{"type": "cursor", "peer": peer}, s.Socket)
		}
		return 0, nil

	default:
		return 0, s.Manager.Send(ctx, s.Socket, errorFrame(
			ErrorUnknownType, fmt.Sprintf("unrecognised type %s", valueRepr(frame["type"]))))
	}
}

func valueRepr(value any) string {
	if value == nil {
		return "None"
	}
	if text, ok := value.(string); ok {
		// The legacy f"{kind!r}" uses single-quoted strings.
		return "'" + text + "'"
	}
	if boolean, ok := value.(bool); ok {
		if boolean {
			return "True"
		}
		return "False"
	}
	if encoded, err := json.Marshal(value); err == nil {
		return string(encoded)
	}
	return fmt.Sprint(value)
}

func (s *Session) findPeer() *Peer {
	snapshot := s.Manager.Snapshot(s.RoomID)
	for i := range snapshot.Peers {
		if snapshot.Peers[i].ID == s.PeerID {
			return &snapshot.Peers[i]
		}
	}
	return nil
}

func (s *Session) registered() bool {
	if !comparableSocket(s.Socket) {
		return false
	}
	s.Manager.mu.RLock()
	defer s.Manager.mu.RUnlock()
	r := s.Manager.rooms[s.RoomID]
	return r != nil && r.peers[s.Socket] != nil && r.peers[s.Socket].ID == s.PeerID
}

// Close detaches the session and announces the departure exactly once. It is
// safe to call from both the normal receive-loop exit and a transport failure.
func (s *Session) Close(ctx context.Context) {
	if s == nil || s.Manager == nil {
		return
	}
	if s.closed.Swap(true) {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = context.WithoutCancel(ctx)
	if peer := s.Manager.Disconnect(s.RoomID, s.Socket); peer != nil {
		s.Manager.Broadcast(ctx, s.RoomID,
			map[string]any{"type": "peer-left", "peer": peer}, nil)
	}
}

// OriginForbidden matches the browser-origin policy: absent origins
// and an empty allow-list are accepted; a present origin must exactly match.
func OriginForbidden(origin string, allowList []string) bool {
	if origin == "" || len(allowList) == 0 {
		return false
	}
	for _, allowed := range allowList {
		if origin == allowed {
			return false
		}
	}
	return true
}
