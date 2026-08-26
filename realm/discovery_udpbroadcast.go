package realm

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"

	"foilen-realm/model"
)

// udpBroadcastPort is the fixed LAN discovery port, shared by every Realm
// node regardless of group membership: only the group hashes carried in each
// broadcast (see udpBroadcastMessage) are group-scoped.
const udpBroadcastPort = 58421

// udpBroadcastMinInterval/udpBroadcastMaxInterval bound the random delay
// between broadcasts: random (not fixed) so many nodes on the same LAN don't
// keep beating in lockstep.
const (
	udpBroadcastMinInterval = 2 * time.Minute
	udpBroadcastMaxInterval = 3 * time.Minute
)

// udpBroadcastMessage is the wire format sent to 255.255.255.255:58421.
// GroupHashes uses groupTopic's daily hash(date+group-private-key) (same
// construction as the DHT rendezvous topic) instead of the group's id/name,
// so a passive LAN observer can't tell which groups exist without already
// knowing a group's key.
type udpBroadcastMessage struct {
	PeerID      string   `json:"peerId"`
	GroupHashes []string `json:"groupHashes"`
	Addrs       []string `json:"addrs"`
}

// startUdpBroadcastLocked opens the shared broadcast/listen socket and starts
// the receive and send loops. Must be called with e.mu held.
func (e *Engine) startUdpBroadcastLocked(ctx context.Context, h host.Host) {
	conn, err := listenUdpBroadcast()
	if err != nil {
		log.Printf("realm engine: failed to start UDP broadcast discovery: %v", err)
		return
	}
	e.udpBroadcastConn = conn
	e.udpBroadcastSeen = make(map[string]struct{})
	log.Printf("realm engine: UDP broadcast discovery started on port %d", udpBroadcastPort)

	go e.runUdpBroadcastReceiveLoop(ctx, conn, h)
	go e.runUdpBroadcastSendLoop(ctx, conn, h)
}

// stopUdpBroadcastLocked closes the broadcast socket, if running. Must be
// called with e.mu held.
func (e *Engine) stopUdpBroadcastLocked() {
	if e.udpBroadcastConn != nil {
		if err := e.udpBroadcastConn.Close(); err != nil {
			log.Printf("realm engine: failed to close UDP broadcast socket: %v", err)
		}
		e.udpBroadcastConn = nil
	}
	e.udpBroadcastSeen = nil
}

// listenUdpBroadcast binds the shared send/receive socket on udpBroadcastPort
// and sets SO_BROADCAST on it (required to send to 255.255.255.255 on both
// unix and Windows). Broadcasting to the limited-broadcast address, rather
// than each interface's directed broadcast address, avoids enumerating
// network interfaces — which is SELinux-blocked for regular apps on Android
// (golang/go#40569), the reason mDNS couldn't be used there.
func listenUdpBroadcast() (*net.UDPConn, error) {
	lc := net.ListenConfig{
		Control: func(_, _ string, c syscall.RawConn) error {
			var sockErr error
			if err := c.Control(func(fd uintptr) {
				sockErr = setSocketBroadcast(fd)
			}); err != nil {
				return err
			}
			return sockErr
		},
	}
	pc, err := lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf(":%d", udpBroadcastPort))
	if err != nil {
		return nil, err
	}
	return pc.(*net.UDPConn), nil
}

func (e *Engine) runUdpBroadcastReceiveLoop(ctx context.Context, conn *net.UDPConn, h host.Host) {
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	buf := make([]byte, 65507)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			continue
		}
		var msg udpBroadcastMessage
		if err := json.Unmarshal(buf[:n], &msg); err != nil {
			continue
		}
		log.Printf("realm engine: UDP broadcast received from %s: peer %s, group hashes %v (%s)", from, model.ShortID(msg.PeerID), msg.GroupHashes, e.describeUdpGroupHashes(msg.GroupHashes))
		e.handleUdpBroadcastMessage(msg, h)
	}
}

// handleUdpBroadcastMessage records every one of msg's group hashes that
// matches one of our own groups as seen (markUdpGroupHashSeen), and surfaces
// the sender as a found peer if at least one matched.
func (e *Engine) handleUdpBroadcastMessage(msg udpBroadcastMessage, h host.Host) {
	if msg.PeerID == h.ID().String() {
		return
	}
	pid, err := peer.Decode(msg.PeerID)
	if err != nil {
		return
	}

	e.mu.Lock()
	groups := e.cfg.Groups
	e.mu.Unlock()

	today := time.Now().UTC().Format("2006-01-02")
	matchedGroup := ""
	for _, g := range groups {
		want := groupTopic(g, today)
		for _, gh := range msg.GroupHashes {
			if gh == want {
				e.markUdpGroupHashSeen(want)
				if matchedGroup == "" {
					matchedGroup = g.Name
				}
			}
		}
	}
	if matchedGroup == "" {
		return
	}

	addrs := parseMultiaddrs(msg.Addrs)
	if len(addrs) == 0 {
		return
	}
	e.handleFoundPeer(peer.AddrInfo{ID: pid, Addrs: addrs}, matchedGroup, "broadcast")
}

// describeUdpGroupHashes maps each of hashes to the label of our own group it
// matches (today's groupTopic), or "unknown" if it matches none of our
// groups, for logging purposes only.
func (e *Engine) describeUdpGroupHashes(hashes []string) string {
	e.mu.Lock()
	groups := e.cfg.Groups
	e.mu.Unlock()

	today := time.Now().UTC().Format("2006-01-02")
	byHash := make(map[string]string, len(groups))
	for _, g := range groups {
		byHash[groupTopic(g, today)] = g.Label()
	}

	labels := make([]string, len(hashes))
	for i, h := range hashes {
		if label, ok := byHash[h]; ok {
			labels[i] = label
		} else {
			labels[i] = "unknown"
		}
	}
	return strings.Join(labels, ", ")
}

func (e *Engine) markUdpGroupHashSeen(hash string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.udpBroadcastSeen != nil {
		e.udpBroadcastSeen[hash] = struct{}{}
	}
}

func (e *Engine) runUdpBroadcastSendLoop(ctx context.Context, conn *net.UDPConn, h host.Host) {
	dest := &net.UDPAddr{IP: net.IPv4bcast, Port: udpBroadcastPort}
	for {
		wait := udpBroadcastMinInterval + time.Duration(rand.Int63n(int64(udpBroadcastMaxInterval-udpBroadcastMinInterval)))
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}

		e.mu.Lock()
		groups := e.cfg.Groups
		e.mu.Unlock()

		today := time.Now().UTC().Format("2006-01-02")
		hashes := make([]string, len(groups))
		for i, g := range groups {
			hashes[i] = groupTopic(g, today)
		}

		if e.allUdpGroupHashesSeenAndReset(hashes) {
			log.Printf("realm engine: UDP broadcast: all group hashes already seen since last beat, skipping this one")
			continue
		}

		msg := udpBroadcastMessage{
			PeerID:      h.ID().String(),
			GroupHashes: hashes,
			Addrs:       addrsToStrings(h.Addrs()),
		}
		data, err := json.Marshal(msg)
		if err != nil {
			log.Printf("realm engine: failed to encode UDP broadcast message: %v", err)
			continue
		}
		if _, err := conn.WriteToUDP(data, dest); err != nil {
			log.Printf("realm engine: failed to send UDP broadcast: %v", err)
			continue
		}
		log.Printf("realm engine: UDP broadcast sent: peer %s, group hashes %v (%s)", model.ShortID(msg.PeerID), hashes, e.describeUdpGroupHashes(hashes))
	}
}

// allUdpGroupHashesSeenAndReset reports whether every one of hashes has been
// observed via a received broadcast since the last time this returned true,
// clearing the seen set on a true result so the next beat starts fresh —
// skipping a beat doesn't restart the random timer, it just leaves this tick
// silent; two different peers advertising different subsets of our groups
// can jointly satisfy this before either does alone. An empty hashes (no
// groups configured) is vacuously satisfied, so a groupless node never
// broadcasts.
func (e *Engine) allUdpGroupHashesSeenAndReset(hashes []string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, hash := range hashes {
		if _, ok := e.udpBroadcastSeen[hash]; !ok {
			return false
		}
	}
	e.udpBroadcastSeen = make(map[string]struct{})
	return true
}
