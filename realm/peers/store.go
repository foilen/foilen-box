package peers

import (
	"sort"
	"time"

	"foilen-realm/jsondb"
	"foilen-realm/model"
)

const dataFileName = "realm-peers.json"

type Data struct {
	Peers map[string]model.PeerInfo `json:"peers"`
}

type Store struct {
	db *jsondb.Store[Data]
}

func New(dir string) (*Store, error) {
	db, err := jsondb.NewStore[Data](dir, dataFileName)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) List() []model.PeerInfo {
	data := s.db.Get()
	result := make([]model.PeerInfo, 0, len(data.Peers))
	for _, p := range data.Peers {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s *Store) Get(id string) (model.PeerInfo, bool) {
	data := s.db.Get()
	info, ok := data.Peers[id]
	return info, ok
}

func (s *Store) Label(id string) string {
	if info, ok := s.Get(id); ok {
		return info.Label()
	}
	return model.ShortID(id)
}

var addressSourcePriority = []string{"broadcast", "announce", "dht"}

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

func (s *Store) Flush() error {
	return s.db.Flush()
}

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

func (s *Store) SetHostnameDescription(id, hostname, description string, relayServiceEnabled bool, version string) {
	s.db.Update(func(d *Data) {
		p, ok := d.Peers[id]
		if !ok {
			return
		}
		p.Hostname = hostname
		p.Description = description
		p.RelayServiceEnabled = relayServiceEnabled
		p.Version = version
		d.Peers[id] = p
	})
}

func (s *Store) SetAnnouncedAddresses(id string, addrs []string) {
	s.db.Update(func(d *Data) {
		p, ok := d.Peers[id]
		if !ok {
			return
		}
		bySource := make(map[string][]string, len(p.AddressesBySource)+1)
		for k, v := range p.AddressesBySource {
			bySource[k] = v
		}
		bySource["announce"] = addrs
		p.AddressesBySource = bySource
		p.Addresses = mergeAddresses(bySource)
		d.Peers[id] = p
	})
}

var discoveredSources = []string{"broadcast", "dht"}

func (s *Store) ClearDiscoveredAddresses(id string) bool {
	found := false
	s.db.Update(func(d *Data) {
		p, ok := d.Peers[id]
		if !ok {
			return
		}
		found = true
		bySource := make(map[string][]string, len(p.AddressesBySource))
		for k, v := range p.AddressesBySource {
			bySource[k] = v
		}
		for _, source := range discoveredSources {
			delete(bySource, source)
		}
		p.AddressesBySource = bySource
		p.Addresses = mergeAddresses(bySource)
		d.Peers[id] = p
	})
	return found
}

func (s *Store) ClearAllDiscoveredAddresses() {
	s.db.Update(func(d *Data) {
		for id, p := range d.Peers {
			bySource := make(map[string][]string, len(p.AddressesBySource))
			for k, v := range p.AddressesBySource {
				bySource[k] = v
			}
			for _, source := range discoveredSources {
				delete(bySource, source)
			}
			p.AddressesBySource = bySource
			p.Addresses = mergeAddresses(bySource)
			d.Peers[id] = p
		}
	})
}

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

func (s *Store) PruneStale(cutoff time.Time) []model.PeerInfo {
	var removed []model.PeerInfo
	s.db.Update(func(d *Data) {
		for id, p := range d.Peers {
			if p.Connected || !p.LastSeen.Before(cutoff) {
				continue
			}
			delete(d.Peers, id)
			removed = append(removed, p)
		}
	})
	return removed
}

func (s *Store) Remove(id string) bool {
	removed := false
	s.db.Update(func(d *Data) {
		p, ok := d.Peers[id]
		if !ok || p.Connected {
			return
		}
		delete(d.Peers, id)
		removed = true
	})
	return removed
}

func (s *Store) ResetAllConnected() {
	s.db.Update(func(d *Data) {
		for id, p := range d.Peers {
			p.Connected = false
			d.Peers[id] = p
		}
	})
}
