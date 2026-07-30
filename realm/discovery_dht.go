package realm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"path/filepath"
	"time"

	leveldb "github.com/ipfs/go-ds-leveldb"
	dht "github.com/libp2p/go-libp2p-kad-dht"
	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	routingdisc "github.com/libp2p/go-libp2p/p2p/discovery/routing"
	discoveryutil "github.com/libp2p/go-libp2p/p2p/discovery/util"

	"foilen-realm/model"
)

// maxRememberedDHTPeers caps how many previously-connected DHT swarm peers
// are remembered (see disconnectDHTSwarmLocked) for a fast reconnect the
// next time DhtModeClient needs to do a lookup.
const maxRememberedDHTPeers = 20

func (e *Engine) startDHT(ctx context.Context, h host.Host, cfg model.Config) error {
	mode := dht.ModeClient
	if cfg.DhtMode == model.DhtModeServer {
		mode = dht.ModeServer
	}

	log.Printf("realm engine: starting DHT (mode=%s)", cfg.DhtMode)

	ds, err := leveldb.NewDatastore(filepath.Join(e.dataDir, dhtDatastoreDirName), nil)
	if err != nil {
		return fmt.Errorf("failed to open DHT datastore: %w", err)
	}

	kadDHT, err := dht.New(ctx, h,
		dht.Mode(mode),
		dht.Datastore(ds),
		dht.BootstrapPeers(dht.GetDefaultBootstrapPeerAddrInfos()...),
	)
	if err != nil {
		return fmt.Errorf("failed to create DHT: %w", err)
	}
	if err := kadDHT.Bootstrap(ctx); err != nil {
		log.Printf("realm engine: DHT bootstrap error: %v", err)
	} else {
		log.Printf("realm engine: DHT bootstrap started")
	}
	e.kadDHT = kadDHT
	e.dhtDatastore = ds
	return nil
}

// startGroupDHTLoopLocked starts the advertise/find loop for a single group
// under a context derived from e.ctx, so it can be cancelled individually
// without tearing down the DHT or the host. Must be called with e.mu held.
func (e *Engine) startGroupDHTLoopLocked(ctx context.Context, group model.Group) {
	groupCtx, cancel := context.WithCancel(ctx)
	e.dhtLoopCancels[groupKey(group)] = cancel
	log.Printf("realm engine: DHT discovery started for group %q", group.Name)
	go e.runGroupDHTLoop(groupCtx, e.routingDiscovery, group)
}

// stopDHTLocked cancels every per-group DHT loop and closes the DHT and its
// datastore. Must be called with e.mu held.
func (e *Engine) stopDHTLocked() {
	for key, cancel := range e.dhtLoopCancels {
		cancel()
		delete(e.dhtLoopCancels, key)
	}
	e.routingDiscovery = nil
	if e.kadDHT != nil {
		if err := e.kadDHT.Close(); err != nil {
			log.Printf("realm engine: failed to close DHT: %v", err)
		}
		e.kadDHT = nil
	}
	if e.dhtDatastore != nil {
		// The DHT doesn't own the datastore we handed it via
		// dht.Datastore(ds); it must be closed separately or the
		// leveldb lock file keeps the next Start from reopening it.
		if err := e.dhtDatastore.Close(); err != nil {
			log.Printf("realm engine: failed to close DHT datastore: %v", err)
		}
		e.dhtDatastore = nil
	}
}

// dhtSwarmPeerIDs returns every peer h is currently connected to that isn't
// a known Realm group peer (i.e. one with at least one confirmed
// GroupNames entry, per connection_ring.go's disconnectExtraPeers) — a
// stranger the host is only connected to because the DHT dialed it while
// bootstrapping or refreshing its routing table.
func (e *Engine) dhtSwarmPeerIDs(h host.Host) []peer.ID {
	var result []peer.ID
	for _, pid := range h.Network().Peers() {
		if info, ok := e.peers.Get(pid.String()); ok && len(info.GroupNames) > 0 {
			continue
		}
		result = append(result, pid)
	}
	return result
}

// disconnectDHTSwarmLocked closes every current DHT swarm connection (see
// dhtSwarmPeerIDs), remembering their addresses in e.lastDHTPeers first so
// the next lookup in DhtModeClient can redial them directly (see
// reconnectRememberedDHTPeers) instead of only starting from the public
// bootstrap list again. A peer a feature reports still in use (see
// PeerInUseHook) is left connected. Must be called with e.mu held; h must
// be non-nil.
func (e *Engine) disconnectDHTSwarmLocked(h host.Host) {
	ids := e.dhtSwarmPeerIDs(h)
	if len(ids) == 0 {
		return
	}

	remembered := make([]peer.AddrInfo, 0, len(ids))
	disconnected := 0
	for _, pid := range ids {
		if e.isPeerInUse(pid) {
			continue
		}
		if addrs := h.Peerstore().Addrs(pid); len(addrs) > 0 {
			remembered = append(remembered, peer.AddrInfo{ID: pid, Addrs: addrs})
		}
		if err := h.Network().ClosePeer(pid); err != nil {
			log.Printf("realm engine: failed to disconnect DHT swarm peer %s: %v", pid, err)
			continue
		}
		disconnected++
	}
	if len(remembered) > maxRememberedDHTPeers {
		remembered = remembered[len(remembered)-maxRememberedDHTPeers:]
	}
	if len(remembered) > 0 {
		e.lastDHTPeers = remembered
	}
	if disconnected > 0 {
		log.Printf("realm engine: disconnected %d DHT swarm peer(s)", disconnected)
	}
}

// reconnectRememberedDHTPeers best-effort redials every DHT swarm peer
// remembered by the last disconnectDHTSwarmLocked call, so a DhtModeClient
// lookup resumes with warm connections to peers already known to be
// reachable instead of relying solely on the public bootstrap peers.
func (e *Engine) reconnectRememberedDHTPeers(ctx context.Context, h host.Host) {
	e.mu.Lock()
	remembered := e.lastDHTPeers
	e.mu.Unlock()

	for _, info := range remembered {
		if h.Network().Connectedness(info.ID) == network.Connected {
			continue
		}
		log.Printf("realm engine: reconnecting to remembered DHT swarm peer %s", info.ID)
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		err := h.Connect(dialCtx, info)
		cancel()
		if err != nil {
			log.Printf("realm engine: failed to reconnect remembered DHT peer %s: %v", info.ID, err)
		}
	}
}

// isDHTClientMode reports whether the engine is currently configured for
// DhtModeClient.
func (e *Engine) isDHTClientMode() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg.DhtMode == model.DhtModeClient
}

// maintainDHTSwarm disconnects the DHT swarm (see disconnectDHTSwarmLocked)
// when running in DhtModeClient, so this peer isn't left permanently
// connected to public DHT infrastructure between lookups. Run periodically
// from keepAliveLoop; a no-op if not running, DHT is disabled, or the mode
// is DhtModeServer (which needs to stay reachable to serve others).
func (e *Engine) maintainDHTSwarm() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.host == nil || e.kadDHT == nil || e.cfg.DhtMode != model.DhtModeClient {
		return
	}
	e.disconnectDHTSwarmLocked(e.host)
}

// runGroupDHTLoop advertises/finds peers under the group's daily-rotating
// rendezvous topic (decision 1), recomputing it on each UTC day rollover.
func (e *Engine) runGroupDHTLoop(ctx context.Context, routingDiscovery *routingdisc.RoutingDiscovery, group model.Group) {
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()

	var (
		currentDate  string
		currentTopic string
		dayCancel    context.CancelFunc
	)
	defer func() {
		if dayCancel != nil {
			dayCancel()
		}
	}()

	for {
		if e.isDHTClientMode() {
			if h := e.Host(); h != nil {
				e.reconnectRememberedDHTPeers(ctx, h)
			}
		}

		today := time.Now().UTC().Format("2006-01-02")
		if today != currentDate {
			if dayCancel != nil {
				dayCancel()
			}
			dayCtx, cancel := context.WithCancel(ctx)
			dayCancel = cancel
			currentDate = today
			currentTopic = groupTopic(group, today)
			log.Printf("realm engine: DHT advertising group %q under today's rendezvous topic", group.Name)
			discoveryutil.Advertise(dayCtx, routingDiscovery, currentTopic)
		}

		go e.findGroupPeers(ctx, routingDiscovery, group, currentTopic)

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}
	}
}

func (e *Engine) findGroupPeers(ctx context.Context, routingDiscovery *routingdisc.RoutingDiscovery, group model.Group, topic string) {
	log.Printf("realm engine: DHT searching for peers in group %q", group.Name)
	infos, err := discoveryutil.FindPeers(ctx, routingDiscovery, topic)
	if err != nil {
		log.Printf("realm engine: DHT find peers failed for group %q: %v", group.Name, err)
		return
	}
	log.Printf("realm engine: DHT found %d peer(s) for group %q", len(infos), group.Name)
	for _, info := range infos {
		e.handleFoundPeer(info, group.Name, "dht")
	}
}

// groupTopic derives the daily-rotating DHT rendezvous string for a group
// (decision 1): hash(yyyy-mm-dd + GroupPrivateKey), so the group's presence
// on the public DHT can't be linked long-term.
func groupTopic(group model.Group, utcDate string) string {
	sum := sha256.Sum256([]byte(utcDate + group.KeyPair.PrivateKeyBase64))
	return hex.EncodeToString(sum[:])
}
