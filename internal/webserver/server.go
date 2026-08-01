// Package webserver serves the vanilla-JS web UI (embedded from the
// repository's web/ directory) and a WebSocket API backing it, bound to
// 127.0.0.1 only. It is used identically by the desktop binary and the
// Android gomobile-bound library (cmd/mobile).
package webserver

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"foilen-box/internal/logging"
)

//go:embed all:web
var webFS embed.FS

// Server is a running instance of the local web UI + API.
type Server struct {
	listener net.Listener
	httpSrv  *http.Server
	api      *api
	token    string
}

// Start binds a random free port on 127.0.0.1, wires up the static file and
// WebSocket handlers, and begins serving in the background. configDir is the
// directory to persist local config in (Early API credentials, Realm
// config/peers); pass "" to use the default (env var or home dir)
// resolution — used on desktop. On Android, pass the app's private files
// dir. defaultDhtMode (realm/model.DhtModeServer or DhtModeClient) is the
// per-platform default applied the first time Realm config is created.
// hostnameOverride, when non-empty, is reported as the Realm config's
// hostname instead of os.Hostname() — used on Android, where the OS-level
// hostname is always "localhost".
func Start(configDir string, defaultDhtMode string, hostnameOverride string) (*Server, error) {
	logDir, err := resolveConfigDir(configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config directory: %w", err)
	}
	if err := logging.Setup(logDir); err != nil {
		return nil, fmt.Errorf("failed to set up logging: %w", err)
	}

	a, err := newAPI(configDir, defaultDhtMode, hostnameOverride)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize API: %w", err)
	}
	a.logDir = logDir

	token, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate session token: %w", err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(filepath.Join(logDir, "ui-port.txt"), []byte(strconv.Itoa(port)), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write ui-port.txt: %w", err)
	}

	staticRoot, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}
	indexTmpl, err := template.ParseFS(staticRoot, "index.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse index.html: %w", err)
	}

	s := &Server{listener: listener, api: a, token: token}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.FileServer(http.FS(staticRoot)).ServeHTTP(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = indexTmpl.Execute(w, map[string]string{"Token": s.token, "Version": displayVersion()})
	})
	mux.HandleFunc("/ws", s.handleWS)

	s.httpSrv = &http.Server{Handler: mux}
	go s.httpSrv.Serve(listener)

	return s, nil
}

// resolveConfigDir mirrors the default-directory resolution done
// independently by internal/early/config and foilen-realm/config
// ($FOILEN_BOX_CONFIG_DIR, falling back to ~/.foilen-box) so the log file
// lands in the same place as the rest of this peer's local state. On
// Android, configDir is always passed explicitly (the app's files dir).
func resolveConfigDir(configDir string) (string, error) {
	if configDir != "" {
		return configDir, nil
	}
	if dir := os.Getenv("FOILEN_BOX_CONFIG_DIR"); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".foilen-box"), nil
}

// URL returns the base http://127.0.0.1:<port>/ address the UI is served on.
func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/", s.listener.Addr().String())
}

// RealmStateSink is the platform-specific callback (Android) invoked
// whenever the user toggles Realm networking on/off via the API, so a
// platform resource tied to Realm connectivity (e.g. a foreground-service
// notification) can be updated to match.
type RealmStateSink interface {
	SetRealmEnabled(enabled bool)
}

// SetRealmStateSink registers the platform-specific callback invoked
// whenever Realm's enabled/disabled state changes.
func (s *Server) SetRealmStateSink(sink RealmStateSink) {
	s.api.realmStateSink = sink
}

// Stop shuts the server down.
func (s *Server) Stop() error {
	s.api.shutdown()
	return s.httpSrv.Close()
}
