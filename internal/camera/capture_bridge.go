package camera

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

const bridgeAcceptTimeout = 10 * time.Second

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
