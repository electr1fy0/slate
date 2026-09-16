package boards

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// TokenResolver validates the query-string bearer token and returns the
// authenticated display identity. The adapter never accepts role/name values
// from the client as authority.
type TokenResolver func(context.Context, string) (Identity, error)

// BoolTokenResolver is a convenience for callers whose auth layer exposes a
// boolean validity result rather than an error.
type BoolTokenResolver func(context.Context, string) (Identity, bool)

// HandlerConfig configures an HTTP board endpoint.
type HandlerConfig struct {
	Manager         *Manager
	ResolveToken    TokenResolver
	AllowedOrigins  []string
	MaxMessageBytes int
	Upgrader        websocket.Upgrader
}

// HTTPHandler is directly mountable with net/http:
// mux.Handle("/ws/board/", boards.NewHTTPHandler(...)).
type HTTPHandler struct {
	manager         *Manager
	resolveToken    TokenResolver
	allowedOrigins  []string
	maxMessageBytes int
	upgrader        websocket.Upgrader
	sessionFactory  func(*Manager, string, any, Identity) *Session
}

// NewHandler constructs a WebSocket HTTP handler from a config value.
func NewHandler(config HandlerConfig) *HTTPHandler {
	manager := config.Manager
	if manager == nil {
		manager = NewManager()
	}
	maxBytes := config.MaxMessageBytes
	if maxBytes <= 0 {
		maxBytes = 64 * 1024
	}
	origins := append([]string(nil), config.AllowedOrigins...)
	upgrader := config.Upgrader
	// Origin is checked after upgrade so rejected browser connections receive
	// the protocol-level 4403 code rather than a framework-specific HTTP 403.
	upgrader.CheckOrigin = func(_ *http.Request) bool { return true }
	return &HTTPHandler{manager: manager, resolveToken: config.ResolveToken,
		allowedOrigins: origins, maxMessageBytes: maxBytes, upgrader: upgrader,
		sessionFactory: NewSession}
}

// NewHTTPHandler is the compact constructor used by the API binary.
func NewHTTPHandler(manager *Manager, resolver TokenResolver, allowedOrigins []string,
	maxMessageBytes int) *HTTPHandler {
	return NewHandler(HandlerConfig{Manager: manager, ResolveToken: resolver,
		AllowedOrigins: allowedOrigins, MaxMessageBytes: maxMessageBytes})
}

// NewHTTPHandlerWithBoolResolver adapts a boolean-returning token resolver.
func NewHTTPHandlerWithBoolResolver(manager *Manager, resolver BoolTokenResolver,
	allowedOrigins []string, maxMessageBytes int) *HTTPHandler {
	var adapted TokenResolver
	if resolver != nil {
		adapted = func(ctx context.Context, token string) (Identity, error) {
			identity, ok := resolver(ctx, token)
			if !ok {
				return Identity{}, ErrInvalidToken
			}
			return identity, nil
		}
	}
	return NewHTTPHandler(manager, adapted, allowedOrigins, maxMessageBytes)
}

var ErrInvalidToken = errors.New("invalid board token")

// ServeHTTP upgrades a request, validates token/origin, and runs the board
// protocol until the client disconnects or the handler's context is canceled.
func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil {
		h = NewHandler(HandlerConfig{})
	}
	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws := &websocketSocket{conn: conn}

	// Closing after the upgrade is intentional: it lets clients and tests
	// observe the stable protocol close codes used by the service.
	if h.resolveToken == nil || r.URL.Query().Get("token") == "" {
		_ = ws.Close(CloseInvalidToken)
		return
	}
	identity, err := h.resolveToken(r.Context(), r.URL.Query().Get("token"))
	if err != nil {
		_ = ws.Close(CloseInvalidToken)
		return
	}
	if OriginForbidden(r.Header.Get("Origin"), h.allowedOrigins) {
		_ = ws.Close(CloseOriginForbidden)
		return
	}

	session := h.sessionFactory(h.manager, roomIDFromRequest(r), ws, identity)
	if session == nil {
		_ = ws.Close(CloseInvalidToken)
		return
	}
	defer session.Close(r.Context())
	defer conn.Close()

	// Keep Gorilla's parser bounded above the application limit. The protocol
	// layer must see frames over maxMessageBytes so it can send the required
	// PAYLOAD_TOO_LARGE error before closing with 1009.
	parserLimit := int64(h.maxMessageBytes) * 2
	if parserLimit < 1024*1024 {
		parserLimit = 1024 * 1024
	}
	conn.SetReadLimit(parserLimit)
	for {
		messageType, payload, readErr := conn.ReadMessage()
		if readErr != nil {
			return
		}
		if messageType != websocket.TextMessage {
			_ = ws.Close(websocket.CloseUnsupportedData)
			return
		}
		closeCode, handleErr := session.HandleFrame(r.Context(), string(payload), h.maxMessageBytes)
		if handleErr != nil {
			return
		}
		if closeCode != 0 {
			_ = ws.Close(closeCode)
			return
		}
	}
}

// roomIDFromRequest accepts the canonical /ws/board/{room} path and keeps the
// handler independently mountable under a router or a raw ServeMux. Routers
// may pass the room as a request context value with RoomIDContextKey.
func roomIDFromRequest(r *http.Request) string {
	if value := r.Context().Value(RoomIDContextKey{}); value != nil {
		if roomID, ok := value.(string); ok {
			return roomID
		}
	}
	const prefix = "/ws/board/"
	path := r.URL.Path
	for i := 0; i+len(prefix) <= len(path); i++ {
		if path[i:i+len(prefix)] == prefix {
			roomID := path[i+len(prefix):]
			for j := 0; j < len(roomID); j++ {
				if roomID[j] == '/' {
					roomID = roomID[:j]
					break
				}
			}
			return roomID
		}
	}
	return ""
}

// RoomIDContextKey allows a router to pass an already-decoded room ID without
// requiring the handler to know that router's path-parameter API.
type RoomIDContextKey struct{}

type websocketSocket struct {
	conn  *websocket.Conn
	write sync.Mutex
}

func (s *websocketSocket) WriteJSON(value any) error {
	s.write.Lock()
	defer s.write.Unlock()
	return s.conn.WriteJSON(value)
}

func (s *websocketSocket) Close(code int) error {
	s.write.Lock()
	defer s.write.Unlock()
	deadline := time.Now().Add(time.Second)
	_ = s.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(code, closeReason(code)), deadline)
	return s.conn.Close()
}

func closeReason(code int) string {
	switch code {
	case CloseInvalidToken:
		return "invalid token"
	case CloseOriginForbidden:
		return "origin forbidden"
	case CloseIdleTimeout:
		return "idle timeout"
	case ClosePayloadTooLarge:
		return "payload too large"
	default:
		return "board connection closed"
	}
}
