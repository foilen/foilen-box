package realm

import "net/http"

// webBridgePath is where webTransport's Listen (see web_transport.go)
// accepts incoming WebSocket connections, and where Dial connects to reach
// a remote peer's listener.
const webBridgePath = "/p2p"

func handleWebBridgeIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(webBridgeIndexHTML))
}

const webBridgeIndexHTML = `<!doctype html>
<html>
<head><meta charset="utf-8"><title>Realm P2P</title></head>
<body>
<h1>Realm P2P</h1>
<p>This is a Realm peer-to-peer node. It exposes a WebSocket
endpoint at <code>/p2p</code> so other Realm peers can reach it through networks that only allow
outbound web traffic.</p>
<p>Realm is a libp2p-based peer identity, discovery and permissions library; this page isn't a
general-purpose website.</p>
</body>
</html>
`
