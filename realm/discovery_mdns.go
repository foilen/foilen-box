package realm

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"runtime"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/p2p/discovery/mdns"

	"foilen-realm/model"
)

// mdnsSupported is false on Android: go-libp2p's mDNS service resolves its
// own listen addresses via net.InterfaceAddrs(), which Android has blocked
// for regular apps (SELinux denies the netlink route dump) since Android 11
// — see https://github.com/golang/go/issues/40569. Every mDNS start there
// would just fail, so it's treated as an unsupported platform rather than a
// per-attempt failure: Start/Reconcile skip it outright regardless of
// cfg.EnableMdns.
var mdnsSupported = runtime.GOOS != "android"

// startGroupMdnsLocked starts the mDNS service for a single group and
// records it under its groupKey. Must be called with e.mu held.
func (e *Engine) startGroupMdnsLocked(h host.Host, group model.Group) {
	svc := mdns.NewMdnsService(h, mdnsServiceName(group), &mdnsNotifee{engine: e, groupName: group.Name})
	if err := svc.Start(); err != nil {
		log.Printf("realm engine: failed to start mDNS for group %q: %v", group.Name, err)
		return
	}
	log.Printf("realm engine: mDNS discovery started for group %q", group.Name)
	e.mdnsSvcs[groupKey(group)] = svc
}

// stopAllMdnsLocked closes every running mDNS service. Must be called with
// e.mu held.
func (e *Engine) stopAllMdnsLocked() {
	for key, svc := range e.mdnsSvcs {
		if err := svc.Close(); err != nil {
			log.Printf("realm engine: failed to close mDNS service: %v", err)
		}
		delete(e.mdnsSvcs, key)
	}
}

type mdnsNotifee struct {
	engine    *Engine
	groupName string
}

func (n *mdnsNotifee) HandlePeerFound(info peer.AddrInfo) {
	n.engine.handleFoundPeer(info, n.groupName, "mdns")
}

// mdnsServiceName scopes mDNS discovery to a group: without this, mDNS
// would surface every Realm node on the LAN regardless of shared group
// membership.
func mdnsServiceName(group model.Group) string {
	sum := sha256.Sum256([]byte("mdns:" + group.KeyPair.PrivateKeyBase64))
	return "_realm-" + hex.EncodeToString(sum[:])[:16] + "._udp"
}
