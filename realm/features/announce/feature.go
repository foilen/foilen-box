package announce

import (
	"encoding/json"
	"log"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"

	realm "foilen-realm"
	realmmaps "foilen-realm/features/maps"
	"foilen-realm/model"
)

const (
	FeatureName = "common/announce"

	storeName = "common"

	peersKeyPrefix = "peers/"

	specRefreshInterval = 6 * time.Hour

	peerInfoRefreshInterval = 24 * time.Hour

	commonMapDefaultAutoDeleteHours = 168
)

type SpecSummary struct {
	OS      string
	CPU     string
	Mem     string
	Battery string
	GPU     string
	Disk    string
}

type peerAnnounceInfo struct {
	Hostname            string   `json:"hostname"`
	Description         string   `json:"description"`
	Addresses           []string `json:"addresses"`
	RelayServiceEnabled bool     `json:"relayServiceEnabled"`
	Version             string   `json:"version"`
}

type Feature struct {
	mapsFeature *realmmaps.Feature
	specText    func() string
	specSummary func() SpecSummary
	hostname    func() string
	appVersion  func() string

	mu            sync.Mutex
	lastSpecPost  time.Time
	lastPeersPost time.Time
	postedInfo    peerAnnounceInfo
}

func New(mapsFeature *realmmaps.Feature, specText func() string, specSummary func() SpecSummary, hostname func() string, appVersion func() string) *Feature {
	return &Feature{mapsFeature: mapsFeature, specText: specText, specSummary: specSummary, hostname: hostname, appVersion: appVersion}
}

func (f *Feature) Name() string { return FeatureName }

func (f *Feature) Actions() []model.PermissionAction { return nil }

func (f *Feature) RegisterHandlers(reg *realm.Registrar) {}

func (f *Feature) RunPeriodic(reg *realm.Registrar) {
	cfg := reg.Config()
	if cfg.PeerID.ID == "" || len(cfg.Groups) == 0 {
		return
	}

	postSpec := f.dueForSpecPost()
	var specJSON []byte
	if postSpec {
		summary := f.specSummary()
		peerSpec := model.PeerSpec{
			PeerID:    cfg.PeerID.ID,
			Text:      f.specText(),
			OS:        summary.OS,
			CPU:       summary.CPU,
			Mem:       summary.Mem,
			Battery:   summary.Battery,
			GPU:       summary.GPU,
			Disk:      summary.Disk,
			FetchedAt: time.Now().UTC(),
		}
		b, err := json.Marshal(peerSpec)
		if err != nil {
			log.Printf("announce: failed to marshal own spec: %v", err)
			postSpec = false
		} else {
			specJSON = b
		}
	}

	peerInfo := peerAnnounceInfo{Hostname: f.hostname(), Description: cfg.Description, Addresses: ownAddresses(reg), RelayServiceEnabled: cfg.EnableRelayService, Version: f.appVersion()}
	postPeerInfo := f.dueForPeerInfoPost(peerInfo)
	var peerInfoJSON []byte
	if postPeerInfo {
		b, err := json.Marshal(peerInfo)
		if err != nil {
			log.Printf("announce: failed to marshal own peer info: %v", err)
			postPeerInfo = false
		} else {
			peerInfoJSON = b
		}
	}

	for _, group := range cfg.Groups {
		f.seedCommonConfig(group)

		for _, svc := range cfg.Services {
			b, err := json.Marshal(svc)
			if err != nil {
				log.Printf("announce: failed to marshal service %q: %v", svc.Name, err)
				continue
			}
			key := serviceMapKey(cfg.PeerID.ID, svc.Name)
			if err := f.mapsFeature.SetValue(group.KeyPair.ID, storeName, key, string(b)); err != nil {
				log.Printf("announce: failed to post service %q to group %q: %v", svc.Name, group.Name, err)
			}
		}
		for _, sc := range cfg.Scripts {
			b, err := json.Marshal(sc)
			if err != nil {
				log.Printf("announce: failed to marshal script %q: %v", sc.Name, err)
				continue
			}
			key := "scripts/" + cfg.PeerID.ID + "/" + sc.Name
			if err := f.mapsFeature.SetValue(group.KeyPair.ID, storeName, key, string(b)); err != nil {
				log.Printf("announce: failed to post script %q to group %q: %v", sc.Name, group.Name, err)
			}
		}
		if postSpec {
			key := "specs/" + cfg.PeerID.ID
			if err := f.mapsFeature.SetValue(group.KeyPair.ID, storeName, key, string(specJSON)); err != nil {
				log.Printf("announce: failed to post spec to group %q: %v", group.Name, err)
			}
		}
		if postPeerInfo {
			key := peersKeyPrefix + cfg.PeerID.ID
			if err := f.mapsFeature.SetValue(group.KeyPair.ID, storeName, key, string(peerInfoJSON)); err != nil {
				log.Printf("announce: failed to post peer info to group %q: %v", group.Name, err)
			}
		}
	}

	if postSpec {
		f.mu.Lock()
		f.lastSpecPost = time.Now()
		f.mu.Unlock()
	}
	if postPeerInfo {
		f.mu.Lock()
		f.lastPeersPost = time.Now()
		f.postedInfo = peerInfo
		f.mu.Unlock()
	}

	f.consumePeerInfo(reg, cfg)
}

func (f *Feature) seedCommonConfig(group model.Group) {
	cfgMap, _, _ := f.mapsFeature.GetMap(group.KeyPair.ID, realmmaps.SystemConfigStoreName)
	if _, ok := cfgMap.Entries[storeName]; ok {
		return
	}
	data, err := json.Marshal(model.RealmMapConfig{AutoDeleteEntriesHours: commonMapDefaultAutoDeleteHours})
	if err != nil {
		log.Printf("announce: failed to marshal default config for %q: %v", storeName, err)
		return
	}
	if err := f.mapsFeature.SetValue(group.KeyPair.ID, realmmaps.SystemConfigStoreName, storeName, string(data)); err != nil {
		log.Printf("announce: failed to seed default config for %q in group %q: %v", storeName, group.Name, err)
	}
}

func serviceMapKey(peerID, name string) string {
	return "services/" + peerID + "/" + name
}

func AnnounceServiceNow(mapsFeature *realmmaps.Feature, cfg model.Config, svc model.Service) {
	b, err := json.Marshal(svc)
	if err != nil {
		log.Printf("announce: failed to marshal service %q: %v", svc.Name, err)
		return
	}
	key := serviceMapKey(cfg.PeerID.ID, svc.Name)
	for _, group := range cfg.Groups {
		if err := mapsFeature.SetValue(group.KeyPair.ID, storeName, key, string(b)); err != nil {
			log.Printf("announce: failed to post service %q to group %q: %v", svc.Name, group.Name, err)
		}
	}
}

func RetractServiceNow(mapsFeature *realmmaps.Feature, cfg model.Config, name string) {
	key := serviceMapKey(cfg.PeerID.ID, name)
	for _, group := range cfg.Groups {
		if err := mapsFeature.DeleteValue(group.KeyPair.ID, storeName, key); err != nil {
			log.Printf("announce: failed to retract service %q from group %q: %v", name, group.Name, err)
		}
	}
}

func (f *Feature) consumePeerInfo(reg *realm.Registrar, cfg model.Config) {
	peersStore := reg.Peers()
	if peersStore == nil {
		return
	}
	for _, group := range cfg.Groups {
		rm, _, _ := f.mapsFeature.GetMap(group.KeyPair.ID, storeName)

		peerInfos := make(map[string]peerAnnounceInfo)
		var relayIDs []string
		for key, entry := range rm.Entries {
			if len(key) <= len(peersKeyPrefix) || key[:len(peersKeyPrefix)] != peersKeyPrefix {
				continue
			}
			peerID := key[len(peersKeyPrefix):]
			if peerID == "" || strings.Contains(peerID, "/") || peerID == cfg.PeerID.ID {
				continue
			}
			var info peerAnnounceInfo
			if err := json.Unmarshal([]byte(entry.Value), &info); err != nil {
				continue
			}
			peerInfos[peerID] = info
			if info.RelayServiceEnabled {
				relayIDs = append(relayIDs, peerID)
			}
		}

		for peerID, info := range peerInfos {
			entry := rm.Entries[peersKeyPrefix+peerID]

			existing, known := peersStore.Get(peerID)
			lastSeen := time.UnixMilli(entry.UpdatedAtUnixMillis)
			if known && !lastSeen.After(existing.LastSeen) {
				lastSeen = existing.LastSeen
			}
			connected := known && existing.Connected

			addrs := info.Addresses
			if target, err := peer.Decode(peerID); err == nil {
				for _, relayIDStr := range relayIDs {
					if relayIDStr == peerID {
						continue
					}
					relayID, err := peer.Decode(relayIDStr)
					if err != nil {
						continue
					}
					relayAddr, err := realm.RelayDialAddr(relayID, target)
					if err != nil {
						continue
					}
					addrs = append(addrs, relayAddr.String())
				}
			}

			peersStore.Upsert(model.PeerInfo{
				ID:                  peerID,
				LastSeen:            lastSeen,
				Addresses:           addrs,
				GroupNames:          existing.GroupNames,
				Connected:           connected,
				Hostname:            info.Hostname,
				Description:         info.Description,
				RelayServiceEnabled: info.RelayServiceEnabled,
				Version:             info.Version,
			}, "announce")
		}
	}
}

func (f *Feature) dueForSpecPost() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return time.Since(f.lastSpecPost) >= specRefreshInterval
}

func (f *Feature) dueForPeerInfoPost(info peerAnnounceInfo) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !reflect.DeepEqual(info, f.postedInfo) {
		return true
	}
	return time.Since(f.lastPeersPost) >= peerInfoRefreshInterval
}

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
