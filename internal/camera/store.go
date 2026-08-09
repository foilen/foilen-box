// Package camera exposes the local camera as an on-demand RTSP stream: the
// capture device only runs while at least one RTSP client is connected.
package camera

import "foilen-realm/jsondb"

const (
	dataFileName = "camera.json"

	// DefaultPort is the RTSP listen port offered by default.
	DefaultPort = 8554

	// DefaultResolution is the capture resolution offered by default, as a
	// "WIDTHxHEIGHT" string (see internal/camera.ParseResolution).
	DefaultResolution = "1280x720"
)

// Data is the on-disk shape of the camera's persisted configuration.
type Data struct {
	Enabled           bool   `json:"enabled"`
	DeviceID          string `json:"deviceId"`
	DeviceLabel       string `json:"deviceLabel"`
	Port              int    `json:"port"`
	BindAllInterfaces bool   `json:"bindAllInterfaces"`
	ExposeAsService   bool   `json:"exposeAsService"`
	Resolution        string `json:"resolution"`
}

// Store persists the camera configuration across restarts.
type Store struct {
	db *jsondb.Store[Data]
}

// NewStore creates the directory if needed and returns a Store backed by
// camera.json inside it, defaulting Port to DefaultPort on first use.
func NewStore(dir string) (*Store, error) {
	db, err := jsondb.NewStore[Data](dir, dataFileName)
	if err != nil {
		return nil, err
	}
	if db.Get().Port == 0 {
		db.Update(func(d *Data) { d.Port = DefaultPort })
	}
	if db.Get().Resolution == "" {
		db.Update(func(d *Data) { d.Resolution = DefaultResolution })
	}
	return &Store{db: db}, nil
}

// Get returns the current persisted configuration.
func (s *Store) Get() Data {
	return s.db.Get()
}

// Update runs fn with exclusive access to the persisted configuration.
func (s *Store) Update(fn func(d *Data)) {
	s.db.Update(fn)
}

// Flush writes the current configuration to disk immediately.
func (s *Store) Flush() error {
	return s.db.Flush()
}
