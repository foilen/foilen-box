package camera

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

// bridgeAcceptTimeout bounds how long start waits for the platform bridge
// to connect back after StartCapture, in case the native side never does
// (e.g. permission denied, device busy).
const bridgeAcceptTimeout = 10 * time.Second

// bridgeCapturer captures via a PlatformBridge (Android's CameraX/MediaCodec
// pipeline, wired through cmd/mobile.CameraBridge): it opens a loopback
// listener, tells the bridge to stream Annex-B H.264 (plus AAC audio on a
// second connection when a microphone is selected) to it, and returns the
// resulting accepted connection(s) as the reader(s) — keeping the gomobile
// boundary to lifecycle calls only, per docs/features.md's guidance to keep
// high-frequency data off per-call gomobile invocations.
type bridgeCapturer struct {
	bridge PlatformBridge
}

func (c bridgeCapturer) listDevices() ([]Device, error) {
	return parseBridgeDevices(c.bridge.ListCameras())
}

func (c bridgeCapturer) listAudioDevices() ([]Device, error) {
	return parseBridgeDevices(c.bridge.ListMicrophones())
}

func parseBridgeDevices(data string, err error) ([]Device, error) {
	if err != nil {
		return nil, err
	}
	if data == "" {
		return nil, nil
	}
	var devices []Device
	if err := json.Unmarshal([]byte(data), &devices); err != nil {
		return nil, fmt.Errorf("camera: failed to parse device list from platform bridge: %w", err)
	}
	return devices, nil
}

func (c bridgeCapturer) start(ctx context.Context, deviceID, resolution, audioDeviceID string) (*captureStream, error) {
	width, height, err := ParseResolution(resolution)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("camera: failed to open local capture listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	if err := c.bridge.StartCapture(deviceID, audioDeviceID, int32(port), int32(width), int32(height)); err != nil {
		listener.Close()
		return nil, fmt.Errorf("camera: platform bridge failed to start capture: %w", err)
	}

	wantConns := 1
	if audioDeviceID != "" {
		wantConns = 2
	}
	conns, err := acceptCaptureConns(ctx, listener, wantConns)
	listener.Close()
	if err != nil {
		_ = c.bridge.StopCapture()
		return nil, err
	}

	closer := &bridgeCloser{conns: conns, bridge: c.bridge}
	if audioDeviceID == "" {
		return &captureStream{Closer: closer, video: conns[0]}, nil
	}

	video, audio, err := splitTaggedConns(conns)
	if err != nil {
		closer.Close()
		return nil, err
	}
	return &captureStream{Closer: closer, video: video, audio: audio}, nil
}

// acceptCaptureConns waits for the platform bridge to open n connections back
// to listener, bounded by bridgeAcceptTimeout and ctx.
func acceptCaptureConns(ctx context.Context, listener net.Listener, n int) ([]net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan result, n)
	go func() {
		for range n {
			conn, err := listener.Accept()
			resultCh <- result{conn, err}
			if err != nil {
				return
			}
		}
	}()

	var conns []net.Conn
	timeout := time.After(bridgeAcceptTimeout)
	for len(conns) < n {
		select {
		case res := <-resultCh:
			if res.err != nil {
				closeConns(conns)
				return nil, fmt.Errorf("camera: platform bridge did not connect: %w", res.err)
			}
			conns = append(conns, res.conn)
		case <-timeout:
			closeConns(conns)
			return nil, fmt.Errorf("camera: timed out waiting for platform bridge to connect")
		case <-ctx.Done():
			closeConns(conns)
			return nil, ctx.Err()
		}
	}
	return conns, nil
}

// splitTaggedConns reads the leading tag byte ('V'/'A') from each connection
// and returns the video and audio readers accordingly.
func splitTaggedConns(conns []net.Conn) (video, audio io.Reader, err error) {
	for _, conn := range conns {
		_ = conn.SetReadDeadline(time.Now().Add(bridgeAcceptTimeout))
		var tag [1]byte
		if _, err := io.ReadFull(conn, tag[:]); err != nil {
			return nil, nil, fmt.Errorf("camera: failed to read capture stream tag: %w", err)
		}
		_ = conn.SetReadDeadline(time.Time{})
		switch tag[0] {
		case 'V':
			video = conn
		case 'A':
			audio = conn
		default:
			return nil, nil, fmt.Errorf("camera: platform bridge sent unknown capture stream tag %q", tag[0])
		}
	}
	if video == nil || audio == nil {
		return nil, nil, fmt.Errorf("camera: platform bridge did not open both a video and an audio stream")
	}
	return video, audio, nil
}

func closeConns(conns []net.Conn) {
	for _, conn := range conns {
		conn.Close()
	}
}

// bridgeCloser closes the accepted loopback connection(s) and tells the
// platform bridge to stop capturing.
type bridgeCloser struct {
	conns  []net.Conn
	bridge PlatformBridge
}

func (c *bridgeCloser) Close() error {
	var err error
	for _, conn := range c.conns {
		if e := conn.Close(); e != nil && err == nil {
			err = e
		}
	}
	if e := c.bridge.StopCapture(); e != nil && err == nil {
		err = e
	}
	return err
}
