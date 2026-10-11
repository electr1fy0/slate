package boards

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func testHTTPServer(t *testing.T, maxBytes int) (*httptest.Server, *Manager) {
	t.Helper()
	manager := NewManager()
	resolver := TokenResolver(func(_ context.Context, token string) (Identity, error) {
		if token != "valid" && token != "teacher" {
			return Identity{}, ErrInvalidToken
		}
		role := "student"
		if token == "teacher" {
			role = "teacher"
		}
		return Identity{Name: "Test User", Role: role}, nil
	})
	handler := NewHTTPHandler(manager, resolver, []string{"https://allowed.example"}, maxBytes)
	mux := http.NewServeMux()
	mux.Handle("/ws/board/", handler)
	return httptest.NewServer(mux), manager
}

func dialBoard(t *testing.T, serverURL, room, token, origin string) *websocket.Conn {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Scheme = "ws"
	parsed.Path = "/ws/board/" + room
	parsed.RawQuery = "token=" + url.QueryEscape(token)
	headers := http.Header{}
	if origin != "" {
		headers.Set("Origin", origin)
	}
	conn, response, err := websocket.DefaultDialer.Dial(parsed.String(), headers)
	if err != nil {
		if response != nil {
			response.Body.Close()
		}
		t.Fatal(err)
	}
	return conn
}

func readBoardMessage(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	messageType, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if messageType != websocket.TextMessage {
		t.Fatalf("message type = %d, want text", messageType)
	}
	message, protocolErr := ParseFrame(string(payload))
	if protocolErr != nil {
		t.Fatal(protocolErr)
	}
	return message
}

func writeBoardMessage(t *testing.T, conn *websocket.Conn, raw string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(raw)); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPHandlerBoardJourney(t *testing.T) {
	server, manager := testHTTPServer(t, 64*1024)
	defer server.Close()
	teacher := dialBoard(t, server.URL, "room-1", "teacher", "https://allowed.example")
	defer teacher.Close()
	writeBoardMessage(t, teacher, `{"type":"join"}`)
	init := readBoardMessage(t, teacher)
	if init["type"] != "init" || len(init["you"].(string)) != 12 {
		t.Fatalf("unexpected init: %#v", init)
	}

	student := dialBoard(t, server.URL, "room-1", "valid", "https://allowed.example")
	defer student.Close()
	writeBoardMessage(t, student, `{"type":"join","name":"Bob"}`)
	studentInit := readBoardMessage(t, student)
	if studentInit["type"] != "init" || len(studentInit["peers"].([]any)) != 1 {
		t.Fatalf("unexpected student init: %#v", studentInit)
	}
	joined := readBoardMessage(t, teacher)
	if joined["type"] != "peer-joined" {
		t.Fatalf("unexpected peer join: %#v", joined)
	}

	writeBoardMessage(t, teacher, `{"type":"elements","elements":[{"id":"e1","version":1}]}`)
	if relay := readBoardMessage(t, student); relay["type"] != "elements" {
		t.Fatalf("unexpected element relay: %#v", relay)
	}
	if manager.ActiveRooms() != 1 {
		t.Fatalf("active rooms = %d, want 1", manager.ActiveRooms())
	}
}

func TestHTTPHandlerRejectsInvalidTokenAndOriginWithCloseCodes(t *testing.T) {
	server, manager := testHTTPServer(t, 64*1024)
	defer server.Close()

	invalid := dialBoardWithoutFatal(t, server.URL, "room", "", "")
	if invalid.conn == nil || invalid.err != nil {
		t.Fatalf("invalid-token websocket did not complete handshake: conn=%v err=%v", invalid.conn != nil, invalid.err)
	}
	assertCloseCode(t, invalid.conn, CloseInvalidToken)
	if manager.ActiveRooms() != 0 {
		t.Fatal("invalid token created a room")
	}

	forbidden := dialBoardWithoutFatal(t, server.URL, "room", "valid", "https://evil.example")
	if forbidden.conn == nil || forbidden.err != nil {
		t.Fatalf("origin websocket did not complete handshake: conn=%v err=%v", forbidden.conn != nil, forbidden.err)
	}
	assertCloseCode(t, forbidden.conn, CloseOriginForbidden)
	if manager.ActiveRooms() != 0 {
		t.Fatal("forbidden origin created a room")
	}
}

func TestHTTPHandlerSendsOversizeErrorThen1009(t *testing.T) {
	server, manager := testHTTPServer(t, 64)
	defer server.Close()
	conn := dialBoard(t, server.URL, "room", "valid", "https://allowed.example")
	defer conn.Close()
	writeBoardMessage(t, conn, `{"type":"join"}`)
	if got := readBoardMessage(t, conn)["type"]; got != "init" {
		t.Fatalf("join response = %#v", got)
	}
	writeBoardMessage(t, conn, strings.Repeat("x", 128))
	errorFrame := readBoardMessage(t, conn)
	if errorFrame["code"] != ErrorPayloadTooLarge {
		t.Fatalf("oversize error frame = %#v", errorFrame)
	}
	assertCloseCode(t, conn, ClosePayloadTooLarge)
	if manager.ActiveRooms() != 0 {
		t.Fatal("oversized connection was not cleaned up")
	}
}

type dialResult struct {
	conn     *websocket.Conn
	err      error
	response *http.Response
}

func dialBoardWithoutFatal(t *testing.T, serverURL, room, token, origin string) dialResult {
	t.Helper()
	parsed, _ := url.Parse(serverURL)
	parsed.Scheme = "ws"
	parsed.Path = "/ws/board/" + room
	parsed.RawQuery = "token=" + url.QueryEscape(token)
	headers := http.Header{}
	if origin != "" {
		headers.Set("Origin", origin)
	}
	conn, response, err := websocket.DefaultDialer.Dial(parsed.String(), headers)
	return dialResult{conn: conn, response: response, err: err}
}

func assertCloseCode(t *testing.T, conn *websocket.Conn, want int) {
	t.Helper()
	if conn == nil {
		t.Fatal("nil websocket connection")
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err := conn.ReadMessage()
	var closeErr *websocket.CloseError
	if !errors.As(err, &closeErr) {
		t.Fatalf("read error = %v, want websocket close", err)
	}
	if closeErr.Code != want {
		t.Fatalf("close code = %d, want %d", closeErr.Code, want)
	}
	if response := closeErr.Text; response == "" {
		t.Fatalf("close reason missing for code %d", want)
	}
}
