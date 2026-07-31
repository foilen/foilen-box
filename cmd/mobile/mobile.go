// Package mobile is the gomobile bind entry point used to build the AAR the
// Android app (android/) embeds. It wraps internal/webserver so the Kotlin
// MainActivity can start the same local web UI/API server the desktop
// binary uses and point a WebView at it.
package mobile

import (
	"errors"
	"sync"
	"time"

	appspec "foilen-box/internal/spec"
	"foilen-box/internal/webserver"
	realmmodel "foilen-realm/model"
)

var (
	mu     sync.Mutex
	server *webserver.Server
)

// NotificationSink is implemented on the Kotlin side (MainActivity) and
// passed to StartServer; gomobile binds it to a Java/Kotlin interface so a
// received Realm notification can be posted as a real Android system
// notification, independent of whether the WebView UI is visible.
type NotificationSink interface {
	Notify(from, title, body string)
}

// RealmStateSink is implemented on the Kotlin side (MainActivity) and
// passed to StartServer; gomobile binds it to a Java/Kotlin interface so the
// Android foreground-service notification can be updated whenever the user
// toggles Realm networking on/off, independent of whether the WebView UI is
// visible.
type RealmStateSink interface {
	SetRealmEnabled(enabled bool)
}

// BatteryProvider is implemented on the Kotlin side (MainActivity, backed by
// Android's BatteryManager) and passed to StartServer; gomobile binds it to a
// Java/Kotlin interface so the specs report can show battery info on
// Android, where Go's usual sysfs-based detection (internal/spec) is
// unreliable — see appspec.BatteryProvider.
type BatteryProvider interface {
	BatteryPercent() int32
	BatteryStatus() string
}

// StartServer starts the local web UI/API server, persisting local config
// (Early API credentials) under filesDir (the Android app's private files
// directory), and returns the base URL to load in a WebView. deviceName is
// reported as the Realm config's hostname in place of os.Hostname(), which
// always returns "localhost" on Android; pass "" to fall back to that
// default. notifSink, stateSink, and batteryProvider may be nil; deviceName
// is only applied on the first call. Calling it again while already running
// still (re)applies any non-nil sink passed this time, so a caller that
// started the server without sinks (e.g. RealmForegroundService starting the
// engine on boot, before MainActivity exists) can be followed by one that
// wires them up.
func StartServer(filesDir string, deviceName string, notifSink NotificationSink, stateSink RealmStateSink, batteryProvider BatteryProvider) (string, error) {
	mu.Lock()
	defer mu.Unlock()

	if server == nil {
		// Matches the original Java app's TimeZone.setDefault(UTC).
		time.Local = time.UTC

		s, err := webserver.Start(filesDir, realmmodel.DhtModeClient, deviceName)
		if err != nil {
			return "", err
		}
		server = s
	}
	if notifSink != nil {
		server.SetNotificationSink(notifSink)
	}
	if stateSink != nil {
		server.SetRealmStateSink(stateSink)
	}
	if batteryProvider != nil {
		appspec.SetAndroidBatteryProvider(batteryProvider)
	}
	return server.URL(), nil
}

// StopServer stops the server started by StartServer, if any.
func StopServer() error {
	mu.Lock()
	defer mu.Unlock()

	if server == nil {
		return errors.New("server is not running")
	}
	err := server.Stop()
	server = nil
	return err
}
