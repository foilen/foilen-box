// Package peers persists the app-level view of known/connected Realm group
// peers (decision 2) on top of internal/jsondb, independent of go-libp2p's
// own in-memory peerstore.
package peers

import (
	"sort"
	"time"

	"foilen-realm/jsondb"
	"foilen-realm/model"
)

const dataFileName = "realm-peers.json"

// Data is the on-disk shape: known peers keyed by peer id.
type Data struct {
	Peers map[string]model.PeerInfo `json:"peers"`
}

// Store persists Data to $FOILEN_BOX_CONFIG_DIR/realm-peers.json (or the
// given Android files dir), shared by desktop/mobile.
type Store struct {
	db *jsondb.Store[Data]
}

// New creates the directory if needed and returns a Store backed by
// realm-peers.json inside it.
func New(dir string) (*Store, error) {
	db, err := jsondb.NewStore[Data](dir, dataFileName)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// List returns all known peers, sorted by ID for stable output.
func (s *Store) List() []model.PeerInfo {
	data := s.db.Get()
	result := make([]model.PeerInfo, 0, len(data.Peers))
	for _, p := range data.Peers {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// Get returns a known peer's usage info, if present.
func (s *Store) Get(id string) (model.PeerInfo, bool) {
	data := s.db.Get()
	info, ok := data.Peers[id]
	return info, ok
}

// addressSourcePriority controls the order per-source address lists are
// merged in, and so which addresses come first in the merged result:
// LAN-discovered (mdns) addresses first, then addresses self-reported via
// the realm map (announce), then addresses only known via public DHT
// routing (dht) last, since those are the least likely to be reachable
// without going out to the internet and back. Sources not listed here are
// merged in afterwards, sorted by name for determinism.
var addressSourcePriority = []string{"mdns", "announce", "dht"}

// Upsert records/updates a peer's usage info. source identifies who is
// reporting info.Addresses (e.g. "mdns", "dht", "announce"); that source's
// addresses are recorded independently of every other source's, and
// info.Addresses/info.AddressesBySource are overwritten with the merged
// result (see addressSourcePriority) before the record is stored. Pass
// source "" to leave addresses untouched (e.g. updates unrelated to
// discovery).
func (s *Store) Upsert(info model.PeerInfo, source string) {
	s.db.Update(func(d *Data) {
		if d.Peers == nil {
			d.Peers = map[string]model.PeerInfo{}
		}
		existing := d.Peers[info.ID]
		if source == "" {
			info.AddressesBySource = existing.AddressesBySource
			info.Addresses = existing.Addresses
		} else {
			bySource := make(map[string][]string, len(existing.AddressesBySource)+1)
			for k, v := range existing.AddressesBySource {
				bySource[k] = v
			}
			bySource[source] = info.Addresses
			info.AddressesBySource = bySource
			info.Addresses = mergeAddresses(bySource)
		}
		d.Peers[info.ID] = info
	})
}

// mergeAddresses unions every source's addresses into one deduplicated
// list, ordered per addressSourcePriority.
func mergeAddresses(bySource map[string][]string) []string {
	seen := make(map[string]bool)
	var merged []string
	appendFrom := func(source string) {
		for _, addr := range bySource[source] {
			if seen[addr] {
				continue
			}
			seen[addr] = true
			merged = append(merged, addr)
		}
	}

	for _, source := range addressSourcePriority {
		appendFrom(source)
	}

	var others []string
	for source := range bySource {
		if !containsString(addressSourcePriority, source) {
			others = append(others, source)
		}
	}
	sort.Strings(others)
	for _, source := range others {
		appendFrom(source)
	}

	return merged
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Flush writes the current known peers to disk immediately, bypassing the
// debounce timer — used on shutdown.
func (s *Store) Flush() error {
	return s.db.Flush()
}

// SetConnected updates the connected flag for a known peer, if present.
// Transitioning to connected also refreshes LastSeen, since a live
// connection is itself proof the peer was just seen (discovery via
// mDNS/DHT otherwise only refreshes LastSeen on its own periodic cadence).
func (s *Store) SetConnected(id string, connected bool) {
	s.db.Update(func(d *Data) {
		p, ok := d.Peers[id]
		if !ok {
			return
		}
		p.Connected = connected
		if connected {
			p.LastSeen = time.Now()
		}
		d.Peers[id] = p
	})
}

// SetHostnameDescription updates a known peer's self-reported hostname,
// description and relay-service availability, if present.
func (s *Store) SetHostnameDescription(id, hostname, description string, relayServiceEnabled bool) {
	s.db.Update(func(d *Data) {
		p, ok := d.Peers[id]
		if !ok {
			return
		}
		p.Hostname = hostname
		p.Description = description
		p.RelayServiceEnabled = relayServiceEnabled
		d.Peers[id] = p
	})
}

// AddGroupName records that a peer has passed the group-challenge for
// groupName, if present. A no-op if the peer already has it.
func (s *Store) AddGroupName(id, groupName string) {
	s.db.Update(func(d *Data) {
		p, ok := d.Peers[id]
		if !ok {
			return
		}
		for _, g := range p.GroupNames {
			if g == groupName {
				return
			}
		}
		p.GroupNames = append(p.GroupNames, groupName)
		d.Peers[id] = p
	})
}

// RemoveGroupName strips groupName from every known peer's GroupNames,
// e.g. after the group is deleted from config. Peers are kept even if this
// empties their GroupNames, since they're still a known peer usage-wise.
func (s *Store) RemoveGroupName(groupName string) {
	s.db.Update(func(d *Data) {
		for id, p := range d.Peers {
			filtered := p.GroupNames[:0]
			for _, g := range p.GroupNames {
				if g != groupName {
					filtered = append(filtered, g)
				}
			}
			p.GroupNames = filtered
			d.Peers[id] = p
		}
	})
}

// PruneStale removes every known, currently-disconnected peer last seen
// before cutoff, returning the ids removed. Connected peers are never
// pruned, regardless of their stored LastSeen.
func (s *Store) PruneStale(cutoff time.Time) []string {
	var removed []string
	s.db.Update(func(d *Data) {
		for id, p := range d.Peers {
			if p.Connected || !p.LastSeen.Before(cutoff) {
				continue
			}
			delete(d.Peers, id)
			removed = append(removed, id)
		}
	})
	return removed
}

// ResetAllConnected clears the connected flag for every known peer. Used on
// engine startup, since a persisted "connected" from a prior session doesn't
// reflect the state of the new (not-yet-connected) host.
func (s *Store) ResetAllConnected() {
	s.db.Update(func(d *Data) {
		for id, p := range d.Peers {
			p.Connected = false
			d.Peers[id] = p
		}
	})
}
