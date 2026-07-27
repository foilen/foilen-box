package webserver

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	realmmodel "foilen-realm/model"
)

func TestServeIndexAndWebSocketRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Start(dir, realmmodel.DhtModeServer, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	resp, err := http.Get(s.URL())
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), s.token) {
		t.Errorf("index.html does not contain the session token")
	}

	wsURL := "ws" + strings.TrimPrefix(s.URL(), "http") + "ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(authMessage{Token: s.token}); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if err := conn.WriteJSON(request{ID: "1", Action: "spec.report"}); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var resp1 response
	if err := conn.ReadJSON(&resp1); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp1.ID != "1" || resp1.Error != "" {
		t.Errorf("unexpected response: %+v", resp1)
	}
}

func TestWebSocketRejectsBadToken(t *testing.T) {
	dir := t.TempDir()
	s, err := Start(dir, realmmodel.DhtModeServer, "")
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	wsURL := "ws" + strings.TrimPrefix(s.URL(), "http") + "ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial ws: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(authMessage{Token: "wrong"}); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if err := conn.WriteJSON(request{ID: "1", Action: "spec.report"}); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var resp1 response
	if err := conn.ReadJSON(&resp1); err == nil {
		t.Errorf("expected connection to be closed after bad token, got response: %+v", resp1)
	}
}
