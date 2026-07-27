package services

import "foilen-realm/jsondb"

const dataFileName = "realm-services-active.json"

// PersistedProxy identifies a proxy the user explicitly started, so it can
// be restarted automatically on the next app start.
type PersistedProxy struct {
	PeerID      string `json:"peerId"`
	ServiceName string `json:"serviceName"`
}

// Data is the on-disk shape: every proxy the user explicitly started, keyed
// by peerID+"|"+serviceName.
type Data struct {
	Active map[string]PersistedProxy `json:"active"`
}

// Store persists which service proxies should be running, backed by
// realm-services-active.json, so Feature.RestoreAll can start them again on
// the next app start.
type Store struct {
	db *jsondb.Store[Data]
}

// NewStore creates the directory if needed and returns a Store backed by
// realm-services-active.json inside it.
func NewStore(dir string) (*Store, error) {
	db, err := jsondb.NewStore[Data](dir, dataFileName)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// List returns every persisted proxy.
func (s *Store) List() []PersistedProxy {
	data := s.db.Get()
	result := make([]PersistedProxy, 0, len(data.Active))
	for _, p := range data.Active {
		result = append(result, p)
	}
	return result
}

// Add records that peerID/serviceName should be started automatically on
// the next app start.
func (s *Store) Add(peerID, serviceName string) {
	s.db.Update(func(d *Data) {
		if d.Active == nil {
			d.Active = map[string]PersistedProxy{}
		}
		d.Active[peerID+"|"+serviceName] = PersistedProxy{PeerID: peerID, ServiceName: serviceName}
	})
}

// Remove forgets peerID/serviceName, so it won't be restarted on the next
// app start.
func (s *Store) Remove(peerID, serviceName string) {
	s.db.Update(func(d *Data) {
		delete(d.Active, peerID+"|"+serviceName)
	})
}

// Flush writes the current persisted proxies to disk immediately, bypassing
// the debounce timer — used on shutdown.
func (s *Store) Flush() error {
	return s.db.Flush()
}
