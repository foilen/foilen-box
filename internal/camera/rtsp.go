package camera

import (
	"fmt"
	"log"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
)

// rtspHandler implements gortsplib's ServerHandler interfaces, delegating
// every event that affects whether the camera should be capturing to the
// owning Manager.
type rtspHandler struct {
	mgr *Manager
}

func (h *rtspHandler) OnConnOpen(*gortsplib.ServerHandlerOnConnOpenCtx)       {}
func (h *rtspHandler) OnConnClose(*gortsplib.ServerHandlerOnConnCloseCtx)     {}
func (h *rtspHandler) OnSessionOpen(*gortsplib.ServerHandlerOnSessionOpenCtx) {}

func (h *rtspHandler) OnSessionClose(ctx *gortsplib.ServerHandlerOnSessionCloseCtx) {
	h.mgr.sessionStoppedPlaying(ctx.Session)
}

func (h *rtspHandler) OnDescribe(_ *gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	stream := h.mgr.currentStream()
	if stream == nil {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}
	return &base.Response{StatusCode: base.StatusOK}, stream, nil
}

func (h *rtspHandler) OnSetup(_ *gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	stream := h.mgr.currentStream()
	if stream == nil {
		return &base.Response{StatusCode: base.StatusNotFound}, nil, nil
	}
	return &base.Response{StatusCode: base.StatusOK}, stream, nil
}

func (h *rtspHandler) OnPlay(ctx *gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	h.mgr.sessionStartedPlaying(ctx.Session)
	return &base.Response{StatusCode: base.StatusOK}, nil
}

func (h *rtspHandler) OnPause(ctx *gortsplib.ServerHandlerOnPauseCtx) (*base.Response, error) {
	h.mgr.sessionStoppedPlaying(ctx.Session)
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// startServerLocked (re)creates and starts the RTSP server + its stream
// description bound to cfg's port/bind address. Callers must hold m.mu and
// must have already stopped any previous server via stopServerLocked.
func (m *Manager) startServerLocked(cfg Data) error {
	bindAddr := "127.0.0.1"
	if cfg.BindAllInterfaces {
		bindAddr = "0.0.0.0"
	}

	h264Format := &format.H264{PayloadTyp: 96, PacketizationMode: 1}
	media := &description.Media{
		Type:    description.MediaTypeVideo,
		Formats: []format.Format{h264Format},
	}
	desc := &description.Session{Medias: []*description.Media{media}}

	server := &gortsplib.Server{
		Handler:     &rtspHandler{mgr: m},
		RTSPAddress: fmt.Sprintf("%s:%d", bindAddr, cfg.Port),
	}
	if err := server.Start(); err != nil {
		return fmt.Errorf("camera: failed to start RTSP server on %s: %w", server.RTSPAddress, err)
	}

	stream := &gortsplib.ServerStream{Server: server, Desc: desc}
	if err := stream.Initialize(); err != nil {
		server.Close()
		return fmt.Errorf("camera: failed to initialize RTSP stream: %w", err)
	}

	m.server = server
	m.stream = stream
	m.media = media
	m.format = h264Format
	log.Printf("camera: RTSP server listening on rtsp://%s/stream", server.RTSPAddress)
	return nil
}

// stopServerLocked tears down the running RTSP server/stream, if any.
// Callers must hold m.mu.
func (m *Manager) stopServerLocked() {
	if m.stream != nil {
		m.stream.Close()
		m.stream = nil
	}
	if m.server != nil {
		m.server.Close()
		m.server = nil
	}
	m.media = nil
	m.format = nil
}

// currentStream returns the running ServerStream, or nil if the camera
// isn't enabled.
func (m *Manager) currentStream() *gortsplib.ServerStream {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stream
}
