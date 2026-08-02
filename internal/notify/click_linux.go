//go:build linux

package notify

import (
	"log"
	"sync"

	"github.com/gen2brain/beeep"
	dbusnotify "github.com/esiqveland/notify"
	"github.com/godbus/dbus/v5"

	"foilen-box/internal/browseropen"
)

// clickState tracks the single lazily-created dbus notifier used for
// clickable notifications, and the notification-ID -> target-URL mapping
// consulted when the "default" action (clicking the notification body)
// fires. Entries are removed on click or on close, whichever comes first.
var clickState struct {
	sync.Mutex
	notifier dbusnotify.Notifier
	failed   bool
	targets  map[uint32]string
}

func ensureClickNotifier() dbusnotify.Notifier {
	clickState.Lock()
	defer clickState.Unlock()
	if clickState.notifier != nil || clickState.failed {
		return clickState.notifier
	}

	conn, err := dbus.SessionBus()
	if err != nil {
		log.Printf("notify: dbus session bus unavailable, notification click-to-open disabled: %v", err)
		clickState.failed = true
		return nil
	}
	n, err := dbusnotify.New(conn,
		dbusnotify.WithOnAction(onClickAction),
		dbusnotify.WithOnClosed(onClickClosed),
	)
	if err != nil {
		log.Printf("notify: failed to register dbus notifier, notification click-to-open disabled: %v", err)
		clickState.failed = true
		return nil
	}
	clickState.notifier = n
	clickState.targets = map[uint32]string{}
	return n
}

func onClickAction(sig *dbusnotify.ActionInvokedSignal) {
	clickState.Lock()
	url, ok := clickState.targets[sig.ID]
	delete(clickState.targets, sig.ID)
	clickState.Unlock()
	if !ok {
		return
	}
	if err := browseropen.Open(url); err != nil {
		log.Printf("notify: failed to open browser from notification click: %v", err)
	}
}

func onClickClosed(sig *dbusnotify.NotificationClosedSignal) {
	clickState.Lock()
	delete(clickState.targets, sig.ID)
	clickState.Unlock()
}

// NotifyClick shows title/body as a desktop notification that opens url in
// the default browser when clicked, via a dbus notification "default"
// action (org.freedesktop.Notifications' ActionInvoked signal) - most
// Linux notification daemons invoke this when the user clicks the
// notification body itself. Falls back to a plain (non-clickable)
// notification if the session's dbus notification service isn't reachable.
func NotifyClick(title, body, url string) error {
	n := ensureClickNotifier()
	if n == nil {
		return Notify(title, body)
	}

	id, err := n.SendNotification(dbusnotify.Notification{
		AppName: beeep.AppName,
		Summary: title,
		Body:    body,
		Actions: []dbusnotify.Action{dbusnotify.NewDefaultAction("Open")},
	})
	if err != nil {
		return Notify(title, body)
	}

	clickState.Lock()
	clickState.targets[id] = url
	clickState.Unlock()
	return nil
}
