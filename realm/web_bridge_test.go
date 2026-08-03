package realm

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/libp2p/go-libp2p/core/network"
	ma "github.com/multiformats/go-multiaddr"
	manet "github.com/multiformats/go-multiaddr/net"
)

// TestWebListenerAccept exercises webListener end to end (the HTTP(S)
// server started by webTransport.Listen): serving the index page,
// upgrading a /p2p request to a WebSocket, opening a resource-management
// scope, and handing the resulting byte stream to Accept. This stops short
// of webTransport.Listen/Dial's libp2p security/muxer upgrade, which needs
// a full libp2p Upgrader — exercised by go-libp2p's own transport test
// suite, not reimplemented here.
func TestWebListenerAccept(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	wl := &webListener{
		rcmgr:    &network.NullResourceManager{},
		ln:       ln,
		incoming: make(chan webAcceptedConn),
		closed:   make(chan struct{}),
	}
	wl.laddr, err = ma.NewMultiaddr(fmt.Sprintf("/ip4/127.0.0.1/realm-http/%d", port))
	if err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleWebBridgeIndex)
	mux.HandleFunc(webBridgePath, wl.handleP2P)
	wl.server = &http.Server{Handler: mux}
	go wl.server.Serve(ln)
	defer wl.Close()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/", port))
	if err != nil {
		t.Fatalf("index page: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("index page: got status %d", resp.StatusCode)
	}

	wsConn, _, err := websocket.DefaultDialer.Dial(fmt.Sprintf("ws://127.0.0.1:%d%s", port, webBridgePath), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer wsConn.Close()

	want := []byte("hello over the web listener")
	if err := wsConn.WriteMessage(websocket.BinaryMessage, want); err != nil {
		t.Fatalf("write: %v", err)
	}

	type accepted struct {
		conn  manet.Conn
		scope network.ConnManagementScope
		err   error
	}
	acceptedCh := make(chan accepted, 1)
	go func() {
		conn, scope, err := wl.Accept()
		acceptedCh <- accepted{conn: conn, scope: scope, err: err}
	}()

	select {
	case ac := <-acceptedCh:
		if ac.err != nil {
			t.Fatalf("accept: %v", ac.err)
		}
		defer ac.scope.Done()
		defer ac.conn.Close()

		buf := make([]byte, len(want))
		ac.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, err := io.ReadFull(ac.conn, buf); err != nil {
			t.Fatalf("read: %v", err)
		}
		if !bytes.Equal(buf, want) {
			t.Fatalf("got %q, want %q", buf, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Accept")
	}
}
