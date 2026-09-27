package camera

import "foilen-realm/jsondb"

const (
	dataFileName = "camera.json"

	DefaultPort = 8554

	DefaultResolution = "1280x720"
)

type Data struct {
	Enabled           bool   `json:"enabled"`
	DeviceID          string `json:"deviceId"`
	DeviceLabel       string `json:"deviceLabel"`
	AudioDeviceID     string `json:"audioDeviceId"`
	AudioDeviceLabel  string `json:"audioDeviceLabel"`
	Port              int    `json:"port"`
	BindAllInterfaces bool   `json:"bindAllInterfaces"`
	ExposeAsService   bool   `json:"exposeAsService"`
	Resolution        string `json:"resolution"`
}

type Store struct {
	db *jsondb.Store[Data]
}

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

func (s *Store) Get() Data {
	return s.db.Get()
}

func (s *Store) Update(fn func(d *Data)) {
	s.db.Update(fn)
}

func (s *Store) Flush() error {
	return s.db.Flush()
}
