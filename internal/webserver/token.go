package webserver

import (
	"crypto/rand"
	"encoding/hex"
)

// newToken returns a random per-process session token embedded into the
// served index.html and required as the first WebSocket message. This stops
// other local pages/processes from silently opening a cross-origin
// WebSocket to this loopback-only server and reading/writing data such as
// the Early API secret.
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
