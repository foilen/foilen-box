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

// SmsBridge is implemented on the Kotlin side (MainActivity) and passed to
// StartServer; gomobile binds it to a Java/Kotlin interface so the SMS
// feature (internal/sms) can send/import real texts and show a real
// clickable Android notification. nil on desktop, where none of that is
// possible — see internal/sms.PlatformBridge, the structurally identical
// interface the rest of the Go code actually consumes.
type SmsBridge interface {
	SendSms(phoneNumber string, body string) error
	ReadAllSms() (string, error)
	ShowNotification(title string, body string, deepLink string)
}

// StartServer starts the local web UI/API server, persisting local config
// (Early API credentials) under filesDir (the Android app's private files
// directory), and returns the base URL to load in a WebView. deviceName is
// reported as the Realm config's hostname in place of os.Hostname(), which
// always returns "localhost" on Android; pass "" to fall back to that
// default. stateSink, batteryProvider, and smsBridge may be nil; deviceName
// is only applied on the first call. Calling it again while already running
// still (re)applies any non-nil sink passed this time, so a caller that
// started the server without sinks (e.g. RealmForegroundService starting
// the engine on boot, before MainActivity exists) can be followed by one
// that wires them up.
func StartServer(filesDir string, deviceName string, stateSink RealmStateSink, batteryProvider BatteryProvider, smsBridge SmsBridge) (string, error) {
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
	if stateSink != nil {
		server.SetRealmStateSink(stateSink)
	}
	if batteryProvider != nil {
		appspec.SetAndroidBatteryProvider(batteryProvider)
	}
	if smsBridge != nil {
		server.SetSmsBridge(smsBridge)
	}
	return server.URL(), nil
}

// SmsReceived is called directly from Kotlin's SmsReceivedReceiver
// (BroadcastReceiver, no Activity involved) whenever the device gets a new
// text, so it can be recorded in whichever SMS-* realmmap this device
// currently manages, if any — see internal/sms.Manager.HandleIncomingSms.
func SmsReceived(sender string, body string, timestampMillis int64) error {
	mu.Lock()
	s := server
	mu.Unlock()
	if s == nil {
		return errors.New("server is not running")
	}
	return s.HandleIncomingSms(sender, body, timestampMillis)
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
