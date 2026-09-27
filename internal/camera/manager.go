package camera

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
)

const stopCaptureDebounce = 2 * time.Second

type Status struct {
	Enabled           bool   `json:"enabled"`
	DeviceID          string `json:"deviceId"`
	DeviceLabel       string `json:"deviceLabel"`
	AudioDeviceID     string `json:"audioDeviceId"`
	AudioDeviceLabel  string `json:"audioDeviceLabel"`
	Port              int    `json:"port"`
	BindAllInterfaces bool   `json:"bindAllInterfaces"`
	ExposeAsService   bool   `json:"exposeAsService"`
	Resolution        string `json:"resolution"`
	Streaming         bool   `json:"streaming"`
	ViewerCount       int    `json:"viewerCount"`
	LastError         string `json:"lastError,omitempty"`

	ManualControl bool `json:"manualControl"`
}

type SaveConfigParams struct {
	Enabled           bool
	DeviceID          string
	DeviceLabel       string
	AudioDeviceID     string
	AudioDeviceLabel  string
	Port              int
	BindAllInterfaces bool
	ExposeAsService   bool
	Resolution        string
}

type Manager struct {
	store *Store

	mu     sync.Mutex
	bridge PlatformBridge

	server      *gortsplib.Server
	stream      *gortsplib.ServerStream
	media       *description.Media
	format      *format.H264
	audioMedia  *description.Media
	audioFormat *format.MPEG4Audio

	playingSessions map[*gortsplib.ServerSession]struct{}
	stopTimer       *time.Timer

	capturing     bool
	captureCancel context.CancelFunc
	lastError     string
}

func NewManager(dataDir string) (*Manager, error) {
	store, err := NewStore(dataDir)
	if err != nil {
		return nil, err
	}
	return &Manager{store: store, playingSessions: map[*gortsplib.ServerSession]struct{}{}}, nil
}

func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cfg := m.store.Get()
	if !cfg.Enabled {
		return nil
	}
	return m.startServerLocked(cfg)
}

func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopCaptureLocked()
	m.stopServerLocked()
	m.mu.Unlock()
	_ = m.store.Flush()
}

func (m *Manager) SetPlatformBridge(bridge PlatformBridge) {
	m.mu.Lock()
	m.bridge = bridge
	m.mu.Unlock()
}

func (m *Manager) ListDevices() ([]Device, error) {
	m.mu.Lock()
	c := m.capturerSnapshotLocked()
	m.mu.Unlock()
	return c.listDevices()
}

func (m *Manager) ListAudioDevices() ([]Device, error) {
	m.mu.Lock()
	c := m.capturerSnapshotLocked()
	m.mu.Unlock()
	return c.listAudioDevices()
}

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
		AudioDeviceID:     cfg.AudioDeviceID,
		AudioDeviceLabel:  cfg.AudioDeviceLabel,
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

// Configuration

func (m *Manager) SaveConfig(p SaveConfigParams) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	prev := m.store.Get()
	next := Data{
		Enabled:           p.Enabled,
		DeviceID:          p.DeviceID,
		DeviceLabel:       p.DeviceLabel,
		AudioDeviceID:     p.AudioDeviceID,
		AudioDeviceLabel:  p.AudioDeviceLabel,
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

	if (prev.DeviceID != next.DeviceID || prev.Resolution != next.Resolution || prev.AudioDeviceID != next.AudioDeviceID) && m.capturing {
		m.stopCaptureLocked()
	}

	switch {
	case prev.Enabled && !next.Enabled:
		m.stopCaptureLocked()
		m.stopServerLocked()
	case next.Enabled && (!prev.Enabled || prev.Port != next.Port || prev.BindAllInterfaces != next.BindAllInterfaces || prev.AudioDeviceID != next.AudioDeviceID):
		m.stopCaptureLocked()
		m.stopServerLocked()
		if err := m.startServerLocked(next); err != nil {
			m.lastError = err.Error()
			return m.statusLocked(), err
		}
	}

	return m.statusLocked(), nil
}

// Capture

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

func (m *Manager) StartCapture() (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stream == nil {
		return m.statusLocked(), fmt.Errorf("camera: enable camera exposure first")
	}
	m.startCaptureLocked()
	return m.statusLocked(), nil
}

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

func (m *Manager) capturerSnapshotLocked() capturer {
	if m.bridge != nil {
		return bridgeCapturer{bridge: m.bridge}
	}
	return ffmpegCapturer{}
}

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
	refs := captureRefs{
		stream:      m.stream,
		media:       m.media,
		h264Format:  m.format,
		audioMedia:  m.audioMedia,
		audioFormat: m.audioFormat,
	}
	go m.runCapture(ctx, cfg, refs, c)
}

type captureRefs struct {
	stream      *gortsplib.ServerStream
	media       *description.Media
	h264Format  *format.H264
	audioMedia  *description.Media
	audioFormat *format.MPEG4Audio
}

func (m *Manager) stopCaptureLocked() {
	if m.captureCancel != nil {
		m.captureCancel()
		m.captureCancel = nil
	}
}

func (m *Manager) runCapture(ctx context.Context, cfg Data, refs captureRefs, c capturer) {
	cs, err := c.start(ctx, cfg.DeviceID, cfg.Resolution, cfg.AudioDeviceID)
	if err != nil {
		m.mu.Lock()
		m.capturing = false
		m.captureCancel = nil
		m.lastError = err.Error()
		m.mu.Unlock()
		log.Printf("camera: failed to start capture (device %q): %v", cfg.DeviceID, err)
		return
	}
	log.Printf("camera: capture started (device %q)", cfg.DeviceID)
	defer cs.Close()

	var runErr error
	switch {
	case cs.muxed && refs.audioMedia != nil:
		runErr = runMuxedCapture(ctx, cs.video, refs.stream, refs.media, refs.h264Format, refs.audioMedia, refs.audioFormat)
	case cs.audio != nil && refs.audioMedia != nil:
		runErr = runSplitCapture(ctx, cs.video, cs.audio, refs.stream, refs.media, refs.h264Format, refs.audioMedia, refs.audioFormat)
	default:
		runErr = m.runH264Capture(ctx, cs.video, refs)
	}

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

func (m *Manager) runH264Capture(ctx context.Context, reader io.Reader, refs captureRefs) error {
	enc, err := refs.h264Format.CreateEncoder()
	if err != nil {
		return err
	}
	pub := newH264Publisher(refs.stream, refs.media, refs.h264Format, enc, 0)
	return readAnnexBUnits(ctx, reader, pub.publish)
}
