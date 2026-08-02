// Package speedtest is the "box/speedtest" Realm feature: measures raw
// download/upload throughput against a connected peer, over a dedicated
// libp2p stream per direction. Lives in foilen-box rather than realm/features
// since it's app-specific, not something every Realm application would want.
//
// Wire protocol and timing (5s per direction) mirror
// github.com/foilen/LANSpeedTest's CheckSpeed/SpeedServer: the initiator
// opens a stream, sends a 1-byte mode, then reads/writes as fast as possible
// until its timer elapses, closing the stream to signal the other side to
// stop. Chunk size is bumped from LANSpeedTest's 1kB to better amortize
// libp2p's stream-framing overhead.
package speedtest

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"

	realm "foilen-realm"
	"foilen-realm/model"
)

const (
	// ProtocolID carries one speed-test direction per stream: a 1-byte mode,
	// a 1-byte ack, then a timed data phase.
	ProtocolID = protocol.ID("/foilen-box/speedtest/1.0.0")

	// FeatureName is this feature's namespace, used to prefix its actions.
	FeatureName = "box/speedtest"

	// ActionRun gates letting another peer run a speed test against this one.
	ActionRun model.PermissionAction = FeatureName + "/run"

	// testDuration matches LANSpeedTest's CheckSpeed.
	testDuration = 5 * time.Second

	// chunkSize: larger than LANSpeedTest's 1kB so yamux's per-write framing
	// overhead doesn't dominate and cap the measured throughput.
	chunkSize = 64 * 1024

	// handshakeTimeout bounds connect/handshake, and is added on top of
	// testDuration as a safety net against an unresponsive peer.
	handshakeTimeout = 10 * time.Second

	modeDownload byte = 0 // initiator measures download speed
	modeUpload   byte = 1 // initiator measures upload speed

	ackOK     byte = 0
	ackDenied byte = 1
)

// Result is one peer's speed test outcome; Error is set (Mbps fields left
// zero) when the test couldn't be completed.
type Result struct {
	PeerID       string  `json:"peerId"`
	DownloadMbps float64 `json:"downloadMbps"`
	UploadMbps   float64 `json:"uploadMbps"`
	Error        string  `json:"error,omitempty"`
}

// Feature implements realm.Feature.
type Feature struct {
	mu  sync.Mutex
	reg *realm.Registrar

	// runMu serializes RunSpeedTest calls so concurrent tests don't skew
	// each other's bandwidth numbers.
	runMu sync.Mutex
}

func New() *Feature { return &Feature{} }

func (f *Feature) registrar() *realm.Registrar {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reg
}

func (f *Feature) Name() string { return FeatureName }

func (f *Feature) Actions() []model.PermissionAction {
	return []model.PermissionAction{ActionRun}
}

func (f *Feature) RegisterHandlers(reg *realm.Registrar) {
	f.mu.Lock()
	f.reg = reg
	f.mu.Unlock()
	reg.SetStreamHandler(ProtocolID, f.handleStream(reg))
}

// handleStream is the libp2p stream handler for ProtocolID: one speed-test
// direction per stream, initiated by the other peer.
func (f *Feature) handleStream(reg *realm.Registrar) network.StreamHandler {
	return func(s network.Stream) {
		defer s.Close()
		remote := s.Conn().RemotePeer()

		_ = s.SetDeadline(time.Now().Add(handshakeTimeout))
		modeBuf := make([]byte, 1)
		if _, err := io.ReadFull(s, modeBuf); err != nil {
			return
		}

		if !reg.IsAllowed(remote, ActionRun) {
			_, _ = s.Write([]byte{ackDenied})
			return
		}
		if _, err := s.Write([]byte{ackOK}); err != nil {
			return
		}

		// Initiator controls the data phase's length by closing the stream;
		// clear the handshake deadline rather than racing it.
		_ = s.SetDeadline(time.Time{})

		chunk := make([]byte, chunkSize)
		switch modeBuf[0] {
		case modeDownload:
			for {
				if _, err := s.Write(chunk); err != nil {
					return
				}
			}
		case modeUpload:
			for {
				if _, err := s.Read(chunk); err != nil {
					return
				}
			}
		}
	}
}

// RunSpeedTest measures download then upload throughput against peerID.
// Always returns a Result; failures are reported via Result.Error rather than
// a Go error.
func (f *Feature) RunSpeedTest(peerID string) Result {
	result := Result{PeerID: peerID}

	reg := f.registrar()
	if reg == nil {
		result.Error = "realm speedtest: not registered on an engine"
		return result
	}
	h := reg.Host()
	ctx := reg.Context()
	if h == nil || ctx == nil {
		result.Error = "realm speedtest: not running"
		return result
	}
	pid, err := peer.Decode(peerID)
	if err != nil {
		result.Error = fmt.Sprintf("invalid peer id %q: %v", peerID, err)
		return result
	}

	f.runMu.Lock()
	defer f.runMu.Unlock()

	connectCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	err = reg.EnsureConnected(connectCtx, pid)
	cancel()
	if err != nil {
		result.Error = fmt.Sprintf("peer unreachable: %v", err)
		return result
	}

	downloadMbps, err := runOneDirection(ctx, h, pid, modeDownload)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.DownloadMbps = downloadMbps

	uploadMbps, err := runOneDirection(ctx, h, pid, modeUpload)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.UploadMbps = uploadMbps

	return result
}

// runOneDirection opens a fresh stream to pid, negotiates mode, then runs
// the timed data phase, returning the measured throughput in Mbps.
func runOneDirection(ctx context.Context, h host.Host, pid peer.ID, mode byte) (float64, error) {
	streamCtx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	s, err := h.NewStream(streamCtx, pid, ProtocolID)
	cancel()
	if err != nil {
		return 0, fmt.Errorf("failed to open stream: %w", err)
	}
	defer s.Close()

	_ = s.SetDeadline(time.Now().Add(handshakeTimeout))
	if _, err := s.Write([]byte{mode}); err != nil {
		return 0, fmt.Errorf("failed to send mode: %w", err)
	}
	ackBuf := make([]byte, 1)
	if _, err := io.ReadFull(s, ackBuf); err != nil {
		return 0, fmt.Errorf("failed to read ack: %w", err)
	}
	if ackBuf[0] == ackDenied {
		return 0, fmt.Errorf("peer denied the speed test")
	}

	_ = s.SetDeadline(time.Now().Add(testDuration + handshakeTimeout))

	chunk := make([]byte, chunkSize)
	start := time.Now()
	deadline := start.Add(testDuration)
	var amountOfBytes int64
	switch mode {
	case modeDownload:
		for time.Now().Before(deadline) {
			n, err := s.Read(chunk)
			amountOfBytes += int64(n)
			if err != nil {
				if amountOfBytes == 0 {
					return 0, fmt.Errorf("failed reading test data: %w", err)
				}
				break
			}
		}
	case modeUpload:
		for time.Now().Before(deadline) {
			n, err := s.Write(chunk)
			amountOfBytes += int64(n)
			if err != nil {
				return 0, fmt.Errorf("failed writing test data: %w", err)
			}
		}
	}

	return calculateMbps(amountOfBytes, time.Since(start)), nil
}

// calculateMbps mirrors LANSpeedTest's CheckSpeed.calculateSpeedInMb:
// decimal megabytes transferred, times 8 for bits, over elapsed seconds.
func calculateMbps(amountOfBytes int64, elapsed time.Duration) float64 {
	seconds := elapsed.Seconds()
	if seconds <= 0 {
		return 0
	}
	megabytes := float64(amountOfBytes) / 1_000_000
	return megabytes / seconds * 8
}
