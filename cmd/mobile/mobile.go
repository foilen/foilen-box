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

type RealmStateSink interface {
	SetRealmEnabled(enabled bool)
}

type BatteryProvider interface {
	BatteryPercent() int32
	BatteryStatus() string
}

type SmsBridge interface {
	SendSms(phoneNumber string, body string) error
	ReadAllSms() (string, error)
	ShowNotification(title string, body string, deepLink string)
}

type CameraBridge interface {
	ListCameras() (string, error)
	ListMicrophones() (string, error)
	StartCapture(deviceID string, audioDeviceID string, tcpPort int32, width int32, height int32) error
	StopCapture() error
}

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
		time.Local = time.UTC

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

func SmsReceived(sender string, body string, timestampMillis int64) error {
	mu.Lock()
	s := server
	mu.Unlock()
	if s == nil {
		return errors.New("server is not running")
	}
	return s.HandleIncomingSms(sender, body, timestampMillis)
}

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
