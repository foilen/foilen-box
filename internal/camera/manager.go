package camera

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
)

// stopCaptureDebounce is how long Manager waits after the last RTSP viewer
// disconnects before actually stopping the capture device, absorbing a
// client's normal SETUP-then-PLAY sequencing and quick reconnects without
// visibly reopening the camera each time.
const stopCaptureDebounce = 2 * time.Second

// Status is Manager's state as reported to the web UI.
type Status struct {
	Enabled           bool   `json:"enabled"`
	DeviceID          string `json:"deviceId"`
	DeviceLabel       string `json:"deviceLabel"`
	Port              int    `json:"port"`
	BindAllInterfaces bool   `json:"bindAllInterfaces"`
	ExposeAsService   bool   `json:"exposeAsService"`
	Resolution        string `json:"resolution"`
	Streaming         bool   `json:"streaming"`
	ViewerCount       int    `json:"viewerCount"`
	LastError         string `json:"lastError,omitempty"`

	// ManualControl is true when capture must be started/stopped explicitly
	// (StartCapture/StopCapture) rather than automatically on RTSP viewer
	// connect/disconnect — i.e. a PlatformBridge (Android) is set. Android
	// 14+ refuses to start a camera-typed foreground service unless the app
	// is in the foreground at that moment, which an RTSP client connecting
	// in the background can't guarantee, so Android requires an explicit
	// user action (button press while the app is open) instead.
	ManualControl bool `json:"manualControl"`
}

// SaveConfigParams is Manager.SaveConfig's input.
type SaveConfigParams struct {
	Enabled           bool
	DeviceID          string
	DeviceLabel       string
	Port              int
	BindAllInterfaces bool
	ExposeAsService   bool
	Resolution        string
}

// Manager owns the camera's persisted configuration, its RTSP server, and
// the on-demand capture device: capture only runs while at least one RTSP
// client is actively PLAYing, per package doc.
type Manager struct {
	store *Store

	mu     sync.Mutex
	bridge PlatformBridge // nil on desktop

	server *gortsplib.Server
	stream *gortsplib.ServerStream
	media  *description.Media
	format *format.H264

	playingSessions map[*gortsplib.ServerSession]struct{}
	stopTimer       *time.Timer

	capturing     bool
	captureCancel context.CancelFunc
	lastError     string
}

// NewManager creates the data directory if needed and returns a Manager
// backed by camera.json inside it. The RTSP server isn't started until
// Start (or a SaveConfig call that enables it).
func NewManager(dataDir string) (*Manager, error) {
	store, err := NewStore(dataDir)
	if err != nil {
		return nil, err
	}
	return &Manager{store: store, playingSessions: map[*gortsplib.ServerSession]struct{}{}}, nil
}

// Start starts the RTSP server if the persisted config has it enabled.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg := m.store.Get()
	if !cfg.Enabled {
		return nil
	}
	return m.startServerLocked(cfg)
}

// Stop tears down any running capture/RTSP server and flushes the store.
// Called on app shutdown.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopCaptureLocked()
	m.stopServerLocked()
	m.mu.Unlock()
	_ = m.store.Flush()
}

// SetPlatformBridge installs the Android capture bridge; nil (the default)
// means desktop ffmpeg-based capture.
func (m *Manager) SetPlatformBridge(bridge PlatformBridge) {
	m.mu.Lock()
	m.bridge = bridge
	m.mu.Unlock()
}

// ListDevices enumerates locally available cameras.
func (m *Manager) ListDevices() ([]Device, error) {
	m.mu.Lock()
	c := m.capturerSnapshotLocked()
	m.mu.Unlock()
	return c.listDevices()
}

// GetStatus returns the current configuration and live capture state.
func (m *Manager) GetStatus() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.statusLocked()
}

func (m *Manager) statusLocked() Status {
	cfg := m.store.Get()
	return Status{
		Enabled:           cfg.Enabled,
		DeviceID:          cfg.DeviceID,
		DeviceLabel:       cfg.DeviceLabel,
		Port:              cfg.Port,
		BindAllInterfaces: cfg.BindAllInterfaces,
		ExposeAsService:   cfg.ExposeAsService,
		Resolution:        cfg.Resolution,
		Streaming:         m.capturing,
		ViewerCount:       len(m.playingSessions),
		LastError:         m.lastError,
		ManualControl:     m.bridge != nil,
	}
}

// SaveConfig persists p and reconciles the running RTSP server/capture
// against it: the server is (re)started when it becomes enabled or its
// port/bind address changes, and stopped when disabled. A capture already
// in progress is stopped if the selected device changed, and resumes on
// the next viewer connection rather than being hot-swapped in place — a
// config change made while someone is actively watching is a rare enough
// edge case that a brief stream interruption is an acceptable trade-off
// for not adding restart-loop complexity here.
func (m *Manager) SaveConfig(p SaveConfigParams) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prev := m.store.Get()
	next := Data{
		Enabled:           p.Enabled,
		DeviceID:          p.DeviceID,
		DeviceLabel:       p.DeviceLabel,
		Port:              p.Port,
		BindAllInterfaces: p.BindAllInterfaces,
		ExposeAsService:   p.ExposeAsService,
		Resolution:        p.Resolution,
	}
	if next.Port == 0 {
		next.Port = DefaultPort
	}
	if next.Resolution == "" {
		next.Resolution = DefaultResolution
	}
	m.store.Update(func(d *Data) { *d = next })

	if (prev.DeviceID != next.DeviceID || prev.Resolution != next.Resolution) && m.capturing {
		m.stopCaptureLocked()
	}

	switch {
	case prev.Enabled && !next.Enabled:
		m.stopCaptureLocked()
		m.stopServerLocked()
	case next.Enabled && (!prev.Enabled || prev.Port != next.Port || prev.BindAllInterfaces != next.BindAllInterfaces):
		m.stopCaptureLocked()
		m.stopServerLocked()
		if err := m.startServerLocked(next); err != nil {
			m.lastError = err.Error()
			return m.statusLocked(), err
		}
	}

	return m.statusLocked(), nil
}

// sessionStartedPlaying records session as an active viewer, starting
// capture on the 0->1 transition — unless a PlatformBridge is set, in which
// case capture is only ever started explicitly via StartCapture (see
// Status.ManualControl's doc).
func (m *Manager) sessionStartedPlaying(session *gortsplib.ServerSession) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, already := m.playingSessions[session]; already {
		return
	}
	m.playingSessions[session] = struct{}{}

	if m.stopTimer != nil {
		m.stopTimer.Stop()
		m.stopTimer = nil
	}
	if len(m.playingSessions) == 1 && m.bridge == nil {
		m.startCaptureLocked()
	}
}

// sessionStoppedPlaying removes session from the active-viewer set,
// scheduling a debounced capture stop once it's empty. No-op with a
// PlatformBridge set — see sessionStartedPlaying.
func (m *Manager) sessionStoppedPlaying(session *gortsplib.ServerSession) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.playingSessions[session]; !ok {
		return
	}
	delete(m.playingSessions, session)
	if len(m.playingSessions) > 0 || m.bridge != nil {
		return
	}

	if m.stopTimer != nil {
		m.stopTimer.Stop()
	}
	m.stopTimer = time.AfterFunc(stopCaptureDebounce, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if len(m.playingSessions) == 0 {
			m.stopCaptureLocked()
		}
	})
}

// StartCapture explicitly starts capture — the only way to start it when
// Status.ManualControl is true (see its doc). No-op if already capturing or
// the RTSP server isn't running.
func (m *Manager) StartCapture() (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stream == nil {
		return m.statusLocked(), fmt.Errorf("camera: enable camera exposure first")
	}
	m.startCaptureLocked()
	return m.statusLocked(), nil
}

// StopCapture explicitly stops capture, if running.
func (m *Manager) StopCapture() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopTimer != nil {
		m.stopTimer.Stop()
		m.stopTimer = nil
	}
	m.stopCaptureLocked()
	return m.statusLocked()
}

// capturerSnapshotLocked returns the capturer implementation to use right
// now. Callers must hold m.mu.
func (m *Manager) capturerSnapshotLocked() capturer {
	if m.bridge != nil {
		return bridgeCapturer{bridge: m.bridge}
	}
	return ffmpegCapturer{}
}

// startCaptureLocked starts the capture device in a background goroutine,
// if not already running. Callers must hold m.mu.
func (m *Manager) startCaptureLocked() {
	if m.capturing || m.stream == nil {
		return
	}
	cfg := m.store.Get()
	if cfg.DeviceID == "" {
		m.lastError = "no camera device selected"
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.captureCancel = cancel
	m.capturing = true
	m.lastError = ""

	c := m.capturerSnapshotLocked()
	stream, media, h264Format := m.stream, m.media, m.format
	go m.runCapture(ctx, cfg.DeviceID, cfg.Resolution, stream, media, h264Format, c)
}

// stopCaptureLocked cancels the running capture, if any; the goroutine
// itself clears m.capturing once it has actually stopped. Callers must
// hold m.mu.
func (m *Manager) stopCaptureLocked() {
	if m.captureCancel != nil {
		m.captureCancel()
		m.captureCancel = nil
	}
}

// runCapture reads deviceID's Annex-B H.264 output via c and publishes it
// to stream until ctx is cancelled or the source ends/errors.
func (m *Manager) runCapture(
	ctx context.Context,
	deviceID string,
	resolution string,
	stream *gortsplib.ServerStream,
	media *description.Media,
	h264Format *format.H264,
	c capturer,
) {
	reader, err := c.start(ctx, deviceID, resolution)
	if err != nil {
		m.mu.Lock()
		m.capturing = false
		m.captureCancel = nil
		m.lastError = err.Error()
		m.mu.Unlock()
		log.Printf("camera: failed to start capture (device %q): %v", deviceID, err)
		return
	}
	log.Printf("camera: capture started (device %q)", deviceID)
	defer reader.Close()

	enc, err := h264Format.CreateEncoder()
	if err != nil {
		m.mu.Lock()
		m.capturing = false
		m.captureCancel = nil
		m.lastError = err.Error()
		m.mu.Unlock()
		return
	}
	pub := newH264Publisher(stream, media, h264Format, enc, 0)

	runErr := readAnnexBUnits(ctx, reader, pub.publish)

	m.mu.Lock()
	m.capturing = false
	m.captureCancel = nil
	if runErr != nil && ctx.Err() == nil {
		m.lastError = runErr.Error()
		log.Printf("camera: capture stopped with error: %v", runErr)
	} else {
		m.lastError = ""
		log.Printf("camera: capture stopped")
	}
	m.mu.Unlock()
}
