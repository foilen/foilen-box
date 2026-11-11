// TODO - Human verify
package webserver

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/gorilla/websocket"

	realmkeypair "foilen-realm/keypair"
	realmmodel "foilen-realm/model"
)

func wsCall(t *testing.T, conn *websocket.Conn, id, action string, params any) response {
	t.Helper()
	raw, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	if err := conn.WriteJSON(request{ID: id, Action: action, Params: raw}); err != nil {
		t.Fatalf("write request: %v", err)
	}
	var resp response
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.ID != id {
		t.Fatalf("response ID = %q, want %q", resp.ID, id)
	}
	return resp
}

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

func TestImportGroupRequiresLocalPeer(t *testing.T) {
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
	if err := conn.WriteJSON(authMessage{Token: s.token}); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	groupKP, err := realmkeypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}

	resp := wsCall(t, conn, "1", "realm.importGroup", map[string]any{
		"name":             "family",
		"privateKeyBase64": groupKP.PrivateKeyBase64,
	})
	if resp.Error != "You need to generate your peer first" {
		t.Errorf("realm.importGroup error = %q, want %q", resp.Error, "You need to generate your peer first")
	}
}

func TestImportGroupWithLocalPeerRecordsOnlinePeers(t *testing.T) {
	dir := t.TempDir()
	s, err := Start(dir, realmmodel.DhtModeClient, "")
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
	if err := conn.WriteJSON(authMessage{Token: s.token}); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	if resp := wsCall(t, conn, "1", "realm.generatePeerId", nil); resp.Error != "" {
		t.Fatalf("realm.generatePeerId error = %q", resp.Error)
	}

	groupKP, err := realmkeypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	strangerKP, err := realmkeypair.Generate()
	if err != nil {
		t.Fatalf("keypair.Generate() error = %v", err)
	}
	strangerAddrs := []string{"/ip4/192.168.1.10/tcp/4001"}

	importResp := wsCall(t, conn, "2", "realm.importGroup", map[string]any{
		"name":             "family",
		"privateKeyBase64": groupKP.PrivateKeyBase64,
		"onlinePeers":      []map[string]any{{"id": strangerKP.ID, "addresses": strangerAddrs}},
	})
	if importResp.Error != "" {
		t.Fatalf("realm.importGroup error = %q", importResp.Error)
	}

	peersResp := wsCall(t, conn, "3", "realm.listPeers", nil)
	if peersResp.Error != "" {
		t.Fatalf("realm.listPeers error = %q", peersResp.Error)
	}
	var peers struct {
		Peers []peerResult `json:"peers"`
	}
	peersRaw, err := json.Marshal(peersResp.Result)
	if err != nil {
		t.Fatalf("marshal listPeers result: %v", err)
	}
	if err := json.Unmarshal(peersRaw, &peers); err != nil {
		t.Fatalf("unmarshal listPeers result: %v", err)
	}
	if len(peers.Peers) != 1 {
		t.Fatalf("peers count = %d, want 1 (imported online peer must be recorded)", len(peers.Peers))
	}
	p := peers.Peers[0]
	if p.ID != strangerKP.ID {
		t.Errorf("peer ID = %q, want %q", p.ID, strangerKP.ID)
	}
	if len(p.Addresses) != 1 || p.Addresses[0] != strangerAddrs[0] {
		t.Errorf("peer Addresses = %v, want %v", p.Addresses, strangerAddrs)
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
