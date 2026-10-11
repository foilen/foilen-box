package services

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/network"
)

const (
	resumeGrace      = time.Minute
	resumeRetryDelay = 2 * time.Second

	frameData byte = 'D'
	frameAck  byte = 'A'
	frameFin  byte = 'X'

	frameMax   = 32 * 1024
	maxUnacked = 1024 * 1024
)

var errSessionClosed = errors.New("tunnel session closed")

type tunnelSession struct {
	peerID string
	conn   net.Conn

	onDetach func()
	onDone   func()

	applyMu sync.Mutex

	mu    sync.Mutex
	cond  *sync.Cond
	timer *time.Timer

	stream network.Stream
	gen    int
	closed bool

	sendBuf  []byte
	sendBase uint64
	sendSent uint64
	ackSent  uint64
	recv     uint64

	finQueued bool
	finSent   bool
	peerFin   bool
	connDead  bool

	done chan struct{}
}

func newTunnelSession(peerID string, conn net.Conn) *tunnelSession {
	s := &tunnelSession{peerID: peerID, conn: conn, done: make(chan struct{})}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// Lifecycle

func (s *tunnelSession) attach(st network.Stream, r io.Reader, peerRecv uint64) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errSessionClosed
	}
	end := s.sendBase + uint64(len(s.sendBuf))
	if peerRecv < s.sendBase || peerRecv > end {
		s.mu.Unlock()
		return errors.New("peer is missing bytes that are no longer buffered")
	}
	s.sendBuf = s.sendBuf[peerRecv-s.sendBase:]
	s.sendBase = peerRecv
	s.sendSent = peerRecv
	s.ackSent = s.recv
	s.finSent = false
	s.stream = st
	s.gen++
	gen := s.gen
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.cond.Broadcast()
	s.mu.Unlock()

	go s.writeLoop(st, gen)
	go s.readLoop(r, gen)
	return nil
}

func (s *tunnelSession) detachLocked() network.Stream {
	old := s.stream
	s.stream = nil
	s.gen++
	gen := s.gen
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(resumeGrace, func() {
		s.mu.Lock()
		stale := s.closed || s.gen != gen
		s.mu.Unlock()
		if !stale {
			s.shutdown()
		}
	})
	s.cond.Broadcast()
	return old
}

func (s *tunnelSession) finishDetach(old network.Stream) {
	if old != nil {
		_ = old.Reset()
	}
	s.waitApply()
}

func (s *tunnelSession) waitApply() {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
}

func (s *tunnelSession) detach(gen int, why string, cause error) {
	s.mu.Lock()
	if s.closed || s.gen != gen || s.stream == nil {
		s.mu.Unlock()
		return
	}
	log.Printf("realm services: tunnel session with %s detached: %s: %v", s.peerID, why, cause)
	old := s.detachLocked()
	s.mu.Unlock()
	s.finishDetach(old)
	if s.onDetach != nil {
		go s.onDetach()
	}
}

func (s *tunnelSession) forceDetach() {
	s.mu.Lock()
	if s.closed || s.stream == nil {
		s.mu.Unlock()
		return
	}
	old := s.detachLocked()
	s.mu.Unlock()
	s.finishDetach(old)
}

func (s *tunnelSession) shutdown() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	st := s.stream
	s.stream = nil
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	s.cond.Broadcast()
	s.mu.Unlock()

	_ = s.conn.Close()
	if st != nil {
		_ = st.Close()
	}
	close(s.done)
	if s.onDone != nil {
		s.onDone()
	}
}

func (s *tunnelSession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func (s *tunnelSession) recvOffset() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recv
}

// Local connection

func (s *tunnelSession) pumpLocal() {
	buf := make([]byte, frameMax)
	for {
		n, err := s.conn.Read(buf)
		s.mu.Lock()
		if n > 0 {
			for len(s.sendBuf) >= maxUnacked && !s.closed {
				s.cond.Wait()
			}
			if s.closed {
				s.mu.Unlock()
				return
			}
			s.sendBuf = append(s.sendBuf, buf[:n]...)
			s.cond.Broadcast()
		}
		if err != nil {
			s.finQueued = true
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

// Stream

func (s *tunnelSession) hasWorkLocked() bool {
	return s.recv > s.ackSent ||
		s.sendSent < s.sendBase+uint64(len(s.sendBuf)) ||
		(s.finQueued && !s.finSent)
}

func (s *tunnelSession) writeLoop(st network.Stream, gen int) {
	wbuf := make([]byte, 5+frameMax)
	for {
		s.mu.Lock()
		for s.gen == gen && !s.closed && !s.hasWorkLocked() {
			s.cond.Wait()
		}
		if s.gen != gen || s.closed {
			s.mu.Unlock()
			return
		}

		var frame []byte
		fin := false
		switch {
		case s.recv > s.ackSent:
			wbuf[0] = frameAck
			binary.BigEndian.PutUint64(wbuf[1:9], s.recv)
			s.ackSent = s.recv
			frame = wbuf[:9]
		case s.sendSent < s.sendBase+uint64(len(s.sendBuf)):
			start := s.sendSent - s.sendBase
			n := copy(wbuf[5:], s.sendBuf[start:])
			wbuf[0] = frameData
			binary.BigEndian.PutUint32(wbuf[1:5], uint32(n))
			s.sendSent += uint64(n)
			frame = wbuf[:5+n]
		default:
			wbuf[0] = frameFin
			s.finSent = true
			fin = true
			frame = wbuf[:1]
		}
		s.mu.Unlock()

		if _, err := st.Write(frame); err != nil {
			s.detach(gen, "write failed", err)
			return
		}

		if fin {
			s.mu.Lock()
			complete := s.peerFin && s.gen == gen
			s.mu.Unlock()
			if complete {
				s.shutdown()
				return
			}
		}
	}
}

func (s *tunnelSession) readLoop(r io.Reader, gen int) {
	br := bufio.NewReader(r)
	buf := make([]byte, frameMax)
	for {
		t, err := br.ReadByte()
		if err != nil {
			s.detach(gen, "read failed", err)
			return
		}
		switch t {
		case frameData:
			var lb [4]byte
			if _, err := io.ReadFull(br, lb[:]); err != nil {
				s.detach(gen, "read data length failed", err)
				return
			}
			n := binary.BigEndian.Uint32(lb[:])
			if n > frameMax {
				s.detach(gen, "oversized frame", nil)
				return
			}
			if _, err := io.ReadFull(br, buf[:n]); err != nil {
				s.detach(gen, "read data failed", err)
				return
			}
			if !s.applyData(gen, buf[:n]) {
				return
			}
		case frameAck:
			var ab [8]byte
			if _, err := io.ReadFull(br, ab[:]); err != nil {
				s.detach(gen, "read ack failed", err)
				return
			}
			if !s.applyAck(gen, binary.BigEndian.Uint64(ab[:])) {
				return
			}
		case frameFin:
			if s.applyFin(gen) {
				return
			}
		default:
			s.detach(gen, "unknown frame type", nil)
			return
		}
	}
}

func (s *tunnelSession) applyData(gen int, p []byte) bool {
	s.applyMu.Lock()
	defer s.applyMu.Unlock()

	s.mu.Lock()
	if s.closed || s.gen != gen {
		s.mu.Unlock()
		return false
	}
	dead := s.connDead
	s.mu.Unlock()

	failed := false
	if !dead {
		if _, err := s.conn.Write(p); err != nil {
			failed = true
			_ = s.conn.Close()
		}
	}

	s.mu.Lock()
	s.recv += uint64(len(p))
	if failed {
		s.connDead = true
		s.finQueued = true
	}
	s.cond.Broadcast()
	s.mu.Unlock()
	return true
}

func (s *tunnelSession) applyAck(gen int, off uint64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.gen != gen {
		return false
	}
	if off > s.sendBase && off <= s.sendBase+uint64(len(s.sendBuf)) {
		s.sendBuf = s.sendBuf[off-s.sendBase:]
		s.sendBase = off
		s.cond.Broadcast()
	}
	return true
}

func (s *tunnelSession) applyFin(gen int) bool {
	s.applyMu.Lock()
	s.mu.Lock()
	if s.closed || s.gen != gen {
		s.mu.Unlock()
		s.applyMu.Unlock()
		return true
	}
	s.peerFin = true
	s.connDead = true
	s.finQueued = true
	complete := s.finSent
	s.cond.Broadcast()
	s.mu.Unlock()
	_ = s.conn.Close()
	s.applyMu.Unlock()

	if complete {
		s.shutdown()
		return true
	}
	return false
}
