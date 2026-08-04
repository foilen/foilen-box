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
	"log"
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

// Start binds a random free port on 127.0.0.1 and begins serving in the
// background. configDir persists local config (""=default resolution on
// desktop; app's private files dir on Android). defaultDhtMode is the
// per-platform default for a freshly created Realm config. hostnameOverride,
// if set, replaces os.Hostname() (Android's is always "localhost").
func Start(configDir string, defaultDhtMode string, hostnameOverride string) (*Server, error) {
	logDir, err := resolveConfigDir(configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config directory: %w", err)
	}
	uiConfigSvc, err := newUIConfigService(logDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize web UI config: %w", err)
	}
	uiCfg := uiConfigSvc.Load()
	if err := logging.Setup(logDir, *uiCfg.ClearLogsOnStartup); err != nil {
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

	listener, err := listenForUI(a.uiConfig.Load())
	if err != nil {
		return nil, fmt.Errorf("failed to listen: %w", err)
	}

	port := listener.Addr().(*net.TCPAddr).Port
	a.currentPort = port
	if err := os.WriteFile(filepath.Join(logDir, "ui-port.txt"), []byte(strconv.Itoa(port)), 0o644); err != nil {
		return nil, fmt.Errorf("failed to write ui-port.txt: %w", err)
	}
	a.realmSms.SetBaseURL(fmt.Sprintf("http://%s/", listener.Addr().String()))

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

// listenForUI binds a random free port on 127.0.0.1, unless cfg pins one; falls
// back to random if the pinned port can't be bound.
func listenForUI(cfg uiConfig) (net.Listener, error) {
	if cfg.RandomPort || cfg.Port == 0 {
		return net.Listen("tcp", "127.0.0.1:0")
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", cfg.Port))
	if err != nil {
		log.Printf("web UI: failed to bind pinned port %d, falling back to a random port: %v", cfg.Port, err)
		return net.Listen("tcp", "127.0.0.1:0")
	}
	return listener, nil
}

// resolveConfigDir mirrors the resolution in internal/early/config and
// foilen-realm/config ($FOILEN_BOX_CONFIG_DIR, else ~/.foilen-box) so the log
// file lands alongside the rest of this peer's state.
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

// RealmStateSink is the platform-specific callback (Android) invoked when
// Realm networking is toggled, so a tied resource (e.g. a foreground-service
// notification) can be updated to match.
type RealmStateSink interface {
	SetRealmEnabled(enabled bool)
}

// SetRealmStateSink registers the platform-specific callback invoked
// whenever Realm's enabled/disabled state changes.
func (s *Server) SetRealmStateSink(sink RealmStateSink) {
	s.api.realmStateSink = sink
}

// SmsBridge is the platform-specific callback (Android) letting internal/sms
// send/import real texts and show notifications; nil on desktop.
// Structurally identical to cmd/mobile.SmsBridge and
// internal/sms.PlatformBridge — see either's doc for why it's declared
// independently rather than shared via import.
type SmsBridge interface {
	SendSms(phoneNumber string, body string) error
	ReadAllSms() (string, error)
	ShowNotification(title string, body string, deepLink string)
}

// PeerCounts returns how many known Realm peers are currently connected out
// of the total known, for platform UI that can't poll the WebSocket API
// (e.g. Android's foreground-service notification).
func (s *Server) PeerCounts() (connected int, total int) {
	for _, p := range s.api.realmPeers.List() {
		total++
		if p.Connected {
			connected++
		}
	}
	return connected, total
}

// SetSmsBridge registers the platform-specific SMS bridge.
func (s *Server) SetSmsBridge(bridge SmsBridge) {
	s.api.realmSms.SetBridge(bridge)
}

// HandleIncomingSms forwards a freshly-received text (from Kotlin's
// SmsReceivedReceiver, via cmd/mobile.SmsReceived) to the SMS feature.
func (s *Server) HandleIncomingSms(sender, body string, timestampMillis int64) error {
	return s.api.realmSms.HandleIncomingSms(sender, body, timestampMillis)
}

// Stop shuts the server down.
func (s *Server) Stop() error {
	s.api.shutdown()
	return s.httpSrv.Close()
}
