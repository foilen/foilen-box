package webserver

import (
	"encoding/json"
	"log"
	"reflect"
	"sync"
	"time"

	realm "foilen-realm"
	realmmaps "foilen-realm/features/maps"
	realmmodel "foilen-realm/model"

	appspec "foilen-box/internal/spec"
)

const (
	// announceStoreName is the RealmMap store every peer posts its own
	// services, scripts, spec, and reachability info into, on all of its
	// groups.
	announceStoreName = "common"

	// peersKeyPrefix namespaces the peers/{peerId} entries every peer posts
	// with its own hostname/description/addresses, so other members can
	// discover and reconnect to it without a direct peer-to-peer gossip
	// protocol.
	peersKeyPrefix = "peers/"

	// specRefreshInterval caps how often the (comparatively expensive)
	// system report is gathered and posted, independently of RunPeriodic's
	// own (much shorter) cadence.
	specRefreshInterval = 24 * time.Hour

	// peerInfoRefreshInterval caps how often this peer's own
	// hostname/description/addresses are re-posted when nothing has
	// actually changed; a real change to any of those fields is posted
	// immediately instead of waiting for this.
	peerInfoRefreshInterval = 24 * time.Hour
)

// peerAnnounceInfo is the JSON shape posted under peers/{peerId}: a peer's
// own self-reported reachability info, analogous to model.PeerSpec but for
// "how do I connect to you" rather than "what's your system report".
type peerAnnounceInfo struct {
	Hostname            string   `json:"hostname"`
	Description         string   `json:"description"`
	Addresses           []string `json:"addresses"`
	RelayServiceEnabled bool     `json:"relayServiceEnabled"`
	Version             string   `json:"version"`
}

// realmAnnounce implements realm.Feature and realm.PeriodicHook: on every
// engine tick it posts this peer's own services, scripts, and reachability
// info (hostname/description/addresses) to every currently configured
// group's "common" RealmMap, and its own spec (refreshed at most once per
// specRefreshInterval, since gathering it is comparatively expensive). It
// also consumes every configured group's "common" map on each tick,
// upserting whatever peers/{peerId} entries it finds into the local known-
// peers store, replacing the old direct peer-to-peer "peer share" gossip.
type realmAnnounce struct {
	mapsFeature *realmmaps.Feature
	specText    func() string
	specSummary func() appspec.Summary
	hostname    func() string

	mu            sync.Mutex
	lastSpecPost  time.Time
	lastPeersPost time.Time
	postedInfo    peerAnnounceInfo
}

func newRealmAnnounce(mapsFeature *realmmaps.Feature, specText func() string, specSummary func() appspec.Summary, hostname func() string) *realmAnnounce {
	return &realmAnnounce{mapsFeature: mapsFeature, specText: specText, specSummary: specSummary, hostname: hostname}
}

func (a *realmAnnounce) Name() string { return "common/announce" }

func (a *realmAnnounce) Actions() []realmmodel.PermissionAction { return nil }

func (a *realmAnnounce) RegisterHandlers(reg *realm.Registrar) {}

// RunPeriodic posts this peer's services, scripts, and reachability info
// (the latter only when its hostname, description, or addresses changed, or
// peerInfoRefreshInterval elapsed), and (at most daily) its spec, to every
// currently configured group's "common" map; it then pulls every such map's
// peers/* entries into the local known-peers store, per realm.PeriodicHook.
func (a *realmAnnounce) RunPeriodic(reg *realm.Registrar) {
	cfg := reg.Config()
	if cfg.PeerID.ID == "" || len(cfg.Groups) == 0 {
		return
	}

	postSpec := a.dueForSpecPost()
	var specJSON []byte
	if postSpec {
		summary := a.specSummary()
		peerSpec := realmmodel.PeerSpec{
			PeerID:    cfg.PeerID.ID,
			Text:      a.specText(),
			CPU:       summary.CPU,
			Mem:       summary.Mem,
			Battery:   summary.Battery,
			GPU:       summary.GPU,
			Disk:      summary.Disk,
			FetchedAt: time.Now().UTC(),
		}
		b, err := json.Marshal(peerSpec)
		if err != nil {
			log.Printf("realm announce: failed to marshal own spec: %v", err)
			postSpec = false
		} else {
			specJSON = b
		}
	}

	peerInfo := peerAnnounceInfo{Hostname: a.hostname(), Description: cfg.Description, Addresses: ownAddresses(reg), RelayServiceEnabled: cfg.EnableRelayService, Version: appVersion()}
	postPeerInfo := a.dueForPeerInfoPost(peerInfo)
	var peerInfoJSON []byte
	if postPeerInfo {
		b, err := json.Marshal(peerInfo)
		if err != nil {
			log.Printf("realm announce: failed to marshal own peer info: %v", err)
			postPeerInfo = false
		} else {
			peerInfoJSON = b
		}
	}

	for _, group := range cfg.Groups {
		for _, svc := range cfg.Services {
			b, err := json.Marshal(svc)
			if err != nil {
				log.Printf("realm announce: failed to marshal service %q: %v", svc.Name, err)
				continue
			}
			key := serviceMapKey(cfg.PeerID.ID, svc.Name)
			if err := a.mapsFeature.SetValue(group.KeyPair.ID, announceStoreName, key, string(b)); err != nil {
				log.Printf("realm announce: failed to post service %q to group %q: %v", svc.Name, group.Name, err)
			}
		}
		for _, sc := range cfg.Scripts {
			b, err := json.Marshal(sc)
			if err != nil {
				log.Printf("realm announce: failed to marshal script %q: %v", sc.Name, err)
				continue
			}
			key := "scripts/" + cfg.PeerID.ID + "/" + sc.Name
			if err := a.mapsFeature.SetValue(group.KeyPair.ID, announceStoreName, key, string(b)); err != nil {
				log.Printf("realm announce: failed to post script %q to group %q: %v", sc.Name, group.Name, err)
			}
		}
		if postSpec {
			key := "specs/" + cfg.PeerID.ID
			if err := a.mapsFeature.SetValue(group.KeyPair.ID, announceStoreName, key, string(specJSON)); err != nil {
				log.Printf("realm announce: failed to post spec to group %q: %v", group.Name, err)
			}
		}
		if postPeerInfo {
			key := peersKeyPrefix + cfg.PeerID.ID
			if err := a.mapsFeature.SetValue(group.KeyPair.ID, announceStoreName, key, string(peerInfoJSON)); err != nil {
				log.Printf("realm announce: failed to post peer info to group %q: %v", group.Name, err)
			}
		}
	}

	if postSpec {
		a.mu.Lock()
		a.lastSpecPost = time.Now()
		a.mu.Unlock()
	}
	if postPeerInfo {
		a.mu.Lock()
		a.lastPeersPost = time.Now()
		a.postedInfo = peerInfo
		a.mu.Unlock()
	}

	a.consumePeerInfo(reg, cfg)
}

// serviceMapKey returns the RealmMap key a service is posted under, so an
// immediate publish/retraction (announceServiceNow/retractServiceNow) and
// the periodic re-post above agree on the same entry.
func serviceMapKey(peerID, name string) string {
	return "services/" + peerID + "/" + name
}

// announceServiceNow immediately posts svc to every one of cfg's groups'
// "common" RealmMap, instead of waiting for the next RunPeriodic tick, so a
// service added or edited through the app is visible to peers right away.
func announceServiceNow(mapsFeature *realmmaps.Feature, cfg realmmodel.Config, svc realmmodel.Service) {
	b, err := json.Marshal(svc)
	if err != nil {
		log.Printf("realm announce: failed to marshal service %q: %v", svc.Name, err)
		return
	}
	key := serviceMapKey(cfg.PeerID.ID, svc.Name)
	for _, group := range cfg.Groups {
		if err := mapsFeature.SetValue(group.KeyPair.ID, announceStoreName, key, string(b)); err != nil {
			log.Printf("realm announce: failed to post service %q to group %q: %v", svc.Name, group.Name, err)
		}
	}
}

// retractServiceNow immediately removes a deleted service's entry from every
// one of cfg's groups' "common" RealmMap.
func retractServiceNow(mapsFeature *realmmaps.Feature, cfg realmmodel.Config, name string) {
	key := serviceMapKey(cfg.PeerID.ID, name)
	for _, group := range cfg.Groups {
		if err := mapsFeature.DeleteValue(group.KeyPair.ID, announceStoreName, key); err != nil {
			log.Printf("realm announce: failed to retract service %q from group %q: %v", name, group.Name, err)
		}
	}
}

// consumePeerInfo reads every configured group's "common" map and upserts
// whatever peers/{peerId} entries it finds into the local known-peers
// store, replacing the old peer-share gossip protocol: a valid map entry is
// itself signed with the group's own private key, so (unlike third-hand
// gossip) it's first-hand, trustworthy proof of both membership in that
// group and of the reachability info it carries.
func (a *realmAnnounce) consumePeerInfo(reg *realm.Registrar, cfg realmmodel.Config) {
	peersStore := reg.Peers()
	if peersStore == nil {
		return
	}
	for _, group := range cfg.Groups {
		rm := a.mapsFeature.GetMap(group.KeyPair.ID, announceStoreName)
		for key, entry := range rm.Entries {
			if len(key) <= len(peersKeyPrefix) || key[:len(peersKeyPrefix)] != peersKeyPrefix {
				continue
			}
			peerID := key[len(peersKeyPrefix):]
			if peerID == "" || peerID == cfg.PeerID.ID {
				continue
			}
			var info peerAnnounceInfo
			if err := json.Unmarshal([]byte(entry.Value), &info); err != nil {
				continue
			}

			existing, known := peersStore.Get(peerID)
			lastSeen := time.UnixMilli(entry.UpdatedAtUnixMillis)
			if known && !lastSeen.After(existing.LastSeen) {
				lastSeen = existing.LastSeen
			}
			connected := known && existing.Connected
			groupNames := existing.GroupNames
			if !containsString(groupNames, group.Name) {
				groupNames = append(append([]string{}, groupNames...), group.Name)
			}

			peersStore.Upsert(realmmodel.PeerInfo{
				ID:                  peerID,
				LastSeen:            lastSeen,
				Addresses:           info.Addresses,
				GroupNames:          groupNames,
				Connected:           connected,
				Hostname:            info.Hostname,
				Description:         info.Description,
				RelayServiceEnabled: info.RelayServiceEnabled,
				Version:             info.Version,
			}, "announce")
		}
	}
}

func (a *realmAnnounce) dueForSpecPost() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return time.Since(a.lastSpecPost) >= specRefreshInterval
}

// dueForPeerInfoPost reports whether the own-peer-info entry should be
// (re)posted this tick: either info differs from what was last posted (any
// of hostname, description, or addresses), or peerInfoRefreshInterval has
// elapsed since the last post.
func (a *realmAnnounce) dueForPeerInfoPost(info peerAnnounceInfo) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !reflect.DeepEqual(info, a.postedInfo) {
		return true
	}
	return time.Since(a.lastPeersPost) >= peerInfoRefreshInterval
}

// ownAddresses returns the running host's own listen multiaddrs as strings,
// or nil if the engine isn't running.
func ownAddresses(reg *realm.Registrar) []string {
	h := reg.Host()
	if h == nil {
		return nil
	}
	addrs := h.Addrs()
	result := make([]string, len(addrs))
	for i, a := range addrs {
		result[i] = a.String()
	}
	return result
}

// appVersion returns this build's self-reported application name and
// version, e.g. "FoilenBox - 20260731_1557 abc1234", posted alongside peer
// announce info so other peers can tell which application (and build)
// they're talking to.
func appVersion() string {
	return "FoilenBox - " + displayVersion()
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
