// Package speedtest is the "box/speedtest" Realm feature: lets this peer
// measure raw download/upload throughput against another peer it's
// connected to, over a dedicated libp2p stream per direction. Unlike the
// features under realm/features (part of the foilen-realm library, and so
// usable by any future application built on it), this one lives in the
// foilen-box module itself since a synthetic throughput test is specific to
// this app, not something every Realm-based application would want.
//
// The wire protocol and timing (5 seconds per direction) mirror
// github.com/foilen/LANSpeedTest's CheckSpeed/SpeedServer: the initiator
// opens a stream, sends a 1-byte mode saying which direction it wants to
// measure, then either reads (download) or writes (upload) as fast as
// possible until its own timer elapses, at which point closing the stream
// tells the other side to stop. The chunk size is bumped up from
// LANSpeedTest's 1kB to better amortize libp2p's own stream-framing
// overhead.
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

	// testDuration is how long each direction's data phase runs for,
	// matching LANSpeedTest's CheckSpeed.
	testDuration = 5 * time.Second

	// chunkSize is the size of each read/write during the data phase.
	// LANSpeedTest uses 1kB, sized for raw TCP sockets; libp2p multiplexes
	// streams over yamux, whose per-write framing overhead would dominate
	// at that size, so this uses a larger chunk to actually saturate the
	// link being measured.
	chunkSize = 64 * 1024

	// handshakeTimeout bounds connecting, opening a stream, and the 2-byte
	// mode/ack exchange; it's also added on top of testDuration as the data
	// phase's own deadline, as a safety net against a peer that stops
	// responding mid-test.
	handshakeTimeout = 10 * time.Second

	modeDownload byte = 0 // "send me data": the initiator measures download speed.
	modeUpload   byte = 1 // "receive data from me": the initiator measures upload speed.

	ackOK     byte = 0
	ackDenied byte = 1
)

// Result is one peer's speed test outcome. Error is set (and the Mbps
// fields left zero) when the test couldn't be completed, so a caller running
// this against several peers in sequence can keep going after one fails.
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

	// runMu serializes RunSpeedTest calls, so two tests (e.g. triggered from
	// two browser tabs) never share this machine's bandwidth and skew each
	// other's numbers.
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

		// The data phase's length is controlled entirely by the initiator
		// closing the stream once its own timer elapses, so clear the
		// handshake deadline rather than racing it.
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

// RunSpeedTest measures download then upload throughput against peerID, one
// after the other, each over its own stream. It always returns a Result;
// failures are reported via Result.Error rather than a Go error, so a caller
// testing several peers in a row doesn't need special-case error handling to
// keep going after one peer fails.
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
