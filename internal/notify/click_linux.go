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

// clickState tracks the lazily-created dbus notifier and the
// notification-ID -> target-URL mapping consulted on click. Entries are
// removed on click or close, whichever comes first.
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

// NotifyClick shows a notification that opens url when clicked, via a dbus
// "default" action (most Linux notification daemons fire this on body
// click). Falls back to a plain notification if dbus isn't reachable.
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
