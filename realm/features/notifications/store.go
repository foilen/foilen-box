// Package notifications persists Realm notifications (sent and received)
// on top of internal/jsondb, each pruned once it passes its own
// sender-chosen TTL (model.Notification.TTLSeconds) rather than a single
// fixed retention window.
package notifications

import (
	"sort"
	"time"

	"foilen-realm/jsondb"
	"foilen-realm/model"
)

const dataFileName = "realm-notifications.json"

// Data is the on-disk shape: notifications keyed by id.
type Data struct {
	Notifications map[string]model.Notification `json:"notifications"`
}

// Store persists Data to $FOILEN_BOX_CONFIG_DIR/realm-notifications.json
// (or the given Android files dir), shared by desktop/mobile.
type Store struct {
	db *jsondb.Store[Data]
}

// NewStore creates the directory if needed and returns a Store backed by
// realm-notifications.json inside it.
func NewStore(dir string) (*Store, error) {
	db, err := jsondb.NewStore[Data](dir, dataFileName)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

// List returns every not-yet-expired notification, newest first, pruning
// any that have passed their own TTL as a side effect.
func (s *Store) List() []model.Notification {
	now := time.Now()
	var result []model.Notification
	s.db.Update(func(d *Data) {
		for id, n := range d.Notifications {
			if n.Expired(now) {
				delete(d.Notifications, id)
				continue
			}
			result = append(result, n)
		}
	})
	sort.Slice(result, func(i, j int) bool { return result[i].SentAt.After(result[j].SentAt) })
	return result
}

// ListPendingSendsTo returns undelivered "sent" notifications addressed to
// peer id, for the engine to retry once that peer reconnects.
func (s *Store) ListPendingSendsTo(peerID string) []model.Notification {
	now := time.Now()
	var result []model.Notification
	data := s.db.Get()
	for _, n := range data.Notifications {
		if n.Direction == model.NotificationSent && !n.Delivered && n.To == peerID && !n.Expired(now) {
			result = append(result, n)
		}
	}
	return result
}

// Upsert records/updates a notification.
func (s *Store) Upsert(n model.Notification) {
	s.db.Update(func(d *Data) {
		if d.Notifications == nil {
			d.Notifications = map[string]model.Notification{}
		}
		d.Notifications[n.ID] = n
	})
}

// MarkDelivered flags a "sent" notification as having reached its
// recipient, if still present (it may have already expired and been
// pruned).
func (s *Store) MarkDelivered(id string) {
	s.db.Update(func(d *Data) {
		n, ok := d.Notifications[id]
		if !ok {
			return
		}
		n.Delivered = true
		d.Notifications[id] = n
	})
}

// RemovePeer deletes every notification either sent to or received from
// peerID.
func (s *Store) RemovePeer(peerID string) {
	s.db.Update(func(d *Data) {
		for id, n := range d.Notifications {
			if n.From == peerID || n.To == peerID {
				delete(d.Notifications, id)
			}
		}
	})
}

// Flush writes the current notifications to disk immediately, bypassing
// the debounce timer — used on shutdown.
func (s *Store) Flush() error {
	return s.db.Flush()
}
