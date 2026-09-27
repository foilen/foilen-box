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

type Server struct {
	listener net.Listener
	httpSrv  *http.Server
	api      *api
	token    string
}

func Start(configDir string, defaultDhtMode string, hostnameOverride string) (*Server, error) {
	configDir, err := resolveConfigDir(configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config directory: %w", err)
	}
	uiConfigSvc, err := newUIConfigService(configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize web UI config: %w", err)
	}
	uiCfg := uiConfigSvc.Load()
	if err := logging.Setup(configDir, *uiCfg.ClearLogsOnStartup); err != nil {
		return nil, fmt.Errorf("failed to set up logging: %w", err)
	}
	log.Print("----[ App Starting ]----")

	a, err := newAPI(configDir, defaultDhtMode, hostnameOverride)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize API: %w", err)
	}

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
	if err := os.WriteFile(filepath.Join(configDir, "ui-port.txt"), []byte(strconv.Itoa(port)), 0o644); err != nil {
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

func (s *Server) URL() string {
	return fmt.Sprintf("http://%s/", s.listener.Addr().String())
}

type RealmStateSink interface {
	SetRealmEnabled(enabled bool)
}

func (s *Server) SetRealmStateSink(sink RealmStateSink) {
	s.api.realmStateSink = sink
}

type SmsBridge interface {
	SendSms(phoneNumber string, body string) error
	ReadAllSms() (string, error)
	ShowNotification(title string, body string, deepLink string)
}

type CameraBridge interface {
	ListCameras() (string, error)
	ListMicrophones() (string, error)
	StartCapture(deviceID string, audioDeviceID string, tcpPort int32, width int32, height int32) error
	StopCapture() error
}

func (s *Server) SetCameraBridge(bridge CameraBridge) {
	s.api.camera.SetPlatformBridge(bridge)
}

func (s *Server) PeerCounts() (connected int, total int) {
	for _, p := range s.api.realmPeers.List() {
		total++
		if p.Connected {
			connected++
		}
	}
	return connected, total
}

func (s *Server) SetSmsBridge(bridge SmsBridge) {
	s.api.realmSms.SetBridge(bridge)
}

func (s *Server) HandleIncomingSms(sender, body string, timestampMillis int64) error {
	return s.api.realmSms.HandleIncomingSms(sender, body, timestampMillis)
}

func (s *Server) Stop() error {
	s.api.shutdown()
	return s.httpSrv.Close()
}
