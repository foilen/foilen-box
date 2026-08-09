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
// listener, tells the bridge to stream Annex-B H.264 to it, and returns the
// resulting accepted connection as the reader — keeping the gomobile
// boundary to lifecycle calls only, per docs/features.md's guidance to keep
// high-frequency data off per-call gomobile invocations.
type bridgeCapturer struct {
	bridge PlatformBridge
}

func (c bridgeCapturer) listDevices() ([]Device, error) {
	data, err := c.bridge.ListCameras()
	if err != nil {
		return nil, err
	}
	var devices []Device
	if err := json.Unmarshal([]byte(data), &devices); err != nil {
		return nil, fmt.Errorf("camera: failed to parse device list from platform bridge: %w", err)
	}
	return devices, nil
}

func (c bridgeCapturer) start(ctx context.Context, deviceID string, resolution string) (io.ReadCloser, error) {
	width, height, err := ParseResolution(resolution)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("camera: failed to open local capture listener: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	if err := c.bridge.StartCapture(deviceID, int32(port), int32(width), int32(height)); err != nil {
		listener.Close()
		return nil, fmt.Errorf("camera: platform bridge failed to start capture: %w", err)
	}

	type acceptResult struct {
		conn net.Conn
		err  error
	}
	resultCh := make(chan acceptResult, 1)
	go func() {
		conn, err := listener.Accept()
		resultCh <- acceptResult{conn, err}
	}()

	select {
	case res := <-resultCh:
		listener.Close() // exactly one connection is expected
		if res.err != nil {
			_ = c.bridge.StopCapture()
			return nil, fmt.Errorf("camera: platform bridge did not connect: %w", res.err)
		}
		return &bridgeConn{Conn: res.conn, bridge: c.bridge}, nil
	case <-time.After(bridgeAcceptTimeout):
		listener.Close()
		_ = c.bridge.StopCapture()
		return nil, fmt.Errorf("camera: timed out waiting for platform bridge to connect")
	case <-ctx.Done():
		listener.Close()
		_ = c.bridge.StopCapture()
		return nil, ctx.Err()
	}
}

// bridgeConn wraps the accepted loopback connection so closing it (either
// because the capture read loop ended, or ctx was cancelled) also tells the
// platform bridge to stop capturing.
type bridgeConn struct {
	net.Conn
	bridge PlatformBridge
}

func (c *bridgeConn) Close() error {
	err := c.Conn.Close()
	if stopErr := c.bridge.StopCapture(); stopErr != nil && err == nil {
		err = stopErr
	}
	return err
}
