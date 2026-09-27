package services

import "foilen-realm/jsondb"

const dataFileName = "realm-services-active.json"

type PersistedProxy struct {
	PeerID      string `json:"peerId"`
	ServiceName string `json:"serviceName"`
}

type Data struct {
	Active map[string]PersistedProxy `json:"active"`
}

type Store struct {
	db *jsondb.Store[Data]
}

func NewStore(dir string) (*Store, error) {
	db, err := jsondb.NewStore[Data](dir, dataFileName)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) List() []PersistedProxy {
	data := s.db.Get()
	result := make([]PersistedProxy, 0, len(data.Active))
	for _, p := range data.Active {
		result = append(result, p)
	}
	return result
}

func (s *Store) Add(peerID, serviceName string) {
	s.db.Update(func(d *Data) {
		if d.Active == nil {
			d.Active = map[string]PersistedProxy{}
		}
		d.Active[peerID+"|"+serviceName] = PersistedProxy{PeerID: peerID, ServiceName: serviceName}
	})
}

func (s *Store) Remove(peerID, serviceName string) {
	s.db.Update(func(d *Data) {
		delete(d.Active, peerID+"|"+serviceName)
	})
}

func (s *Store) Flush() error {
	return s.db.Flush()
}
