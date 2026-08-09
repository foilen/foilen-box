// Package mobile is the gomobile bind entry point for the Android AAR: it
// wraps internal/webserver so Kotlin's MainActivity can start the same
// local web UI/API server the desktop binary uses and point a WebView at it.
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

// RealmStateSink is implemented by Kotlin's MainActivity so the Android
// foreground-service notification reflects Realm networking on/off.
type RealmStateSink interface {
	SetRealmEnabled(enabled bool)
}

// BatteryProvider is implemented by Kotlin's MainActivity (Android's
// BatteryManager) since Go's sysfs-based detection (internal/spec) is
// unreliable on Android — see appspec.BatteryProvider.
type BatteryProvider interface {
	BatteryPercent() int32
	BatteryStatus() string
}

// SmsBridge is implemented by Kotlin's MainActivity for sending/importing SMS
// and showing notifications; nil on desktop. See internal/sms.PlatformBridge,
// the interface the rest of the Go code actually consumes.
type SmsBridge interface {
	SendSms(phoneNumber string, body string) error
	ReadAllSms() (string, error)
	ShowNotification(title string, body string, deepLink string)
}

// CameraBridge is implemented by Kotlin's CameraCaptureBridge for native
// camera capture (CameraX + MediaCodec); nil on desktop, where
// internal/camera uses ffmpeg instead. See internal/camera.PlatformBridge,
// the interface the rest of the Go code actually consumes.
//
// ListCameras returns a JSON array of {"id":"...","label":"..."} objects.
// StartCapture tells Android to start encoding deviceID at width x height to
// H.264 and stream the raw Annex-B bytes to a TCP connection to
// 127.0.0.1:tcpPort, which the Go side is already listening on; StopCapture
// tells it to stop. Frame data
// itself never crosses this gomobile boundary — only these lifecycle calls
// do — since gomobile call overhead makes it unsuitable for a per-frame data
// plane (see internal/camera.bridgeCapturer).
type CameraBridge interface {
	ListCameras() (string, error)
	StartCapture(deviceID string, tcpPort int32, width int32, height int32) error
	StopCapture() error
}

// StartServer starts the local web UI/API server under filesDir and returns
// its base URL for a WebView. deviceName replaces os.Hostname() (always
// "localhost" on Android); osVersion is android.os.Build.VERSION.RELEASE,
// replacing gopsutil's Linux-only distro detection; both default if "".
// deviceName only applies on the first call; stateSink/batteryProvider/
// smsBridge/cameraBridge (nilable) are reapplied every call, so
// RealmForegroundService can start the server on boot before MainActivity
// wires them up.
func StartServer(
	filesDir string,
	deviceName string,
	osVersion string,
	stateSink RealmStateSink,
	batteryProvider BatteryProvider,
	smsBridge SmsBridge,
	cameraBridge CameraBridge,
) (string, error) {
	mu.Lock()
	defer mu.Unlock()

	if server == nil {
		time.Local = time.UTC // matches original Java app's TimeZone.setDefault(UTC)

		s, err := webserver.Start(filesDir, realmmodel.DhtModeClient, deviceName)
		if err != nil {
			return "", err
		}
		server = s
	}
	if osVersion != "" {
		appspec.SetAndroidOSVersion(osVersion)
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
	if cameraBridge != nil {
		server.SetCameraBridge(cameraBridge)
	}
	return server.URL(), nil
}

// SmsReceived is called from Kotlin's SmsReceivedReceiver on every incoming
// text — see internal/sms.Manager.HandleIncomingSms.
func SmsReceived(sender string, body string, timestampMillis int64) error {
	mu.Lock()
	s := server
	mu.Unlock()
	if s == nil {
		return errors.New("server is not running")
	}
	return s.HandleIncomingSms(sender, body, timestampMillis)
}

// ConnectedPeersCount and PeersTotalCount back the Android foreground-service
// notification ("Connected peers X/Y"), which can't use the WebSocket API the
// web UI polls for the same numbers. Split into two calls (rather than one
// struct return) since gomobile bind only supports a single non-error return
// value.
func ConnectedPeersCount() int {
	mu.Lock()
	s := server
	mu.Unlock()
	if s == nil {
		return 0
	}
	connected, _ := s.PeerCounts()
	return connected
}

func PeersTotalCount() int {
	mu.Lock()
	s := server
	mu.Unlock()
	if s == nil {
		return 0
	}
	_, total := s.PeerCounts()
	return total
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
