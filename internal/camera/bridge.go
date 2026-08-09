package camera

// PlatformBridge is implemented on Android (cmd/mobile.CameraBridge,
// CameraCaptureBridge.kt) to drive native camera capture; nil on desktop,
// where ffmpegCapturer is used instead. Passed to Manager.SetPlatformBridge.
type PlatformBridge interface {
	// ListCameras returns a JSON array of {"id":"...","label":"..."} objects.
	ListCameras() (string, error)

	// StartCapture tells the platform to start encoding deviceID at
	// width x height to H.264 and stream the raw Annex-B bytes to a TCP
	// connection to 127.0.0.1:tcpPort, which the Go side is already
	// listening on. tcpPort/width/height are int32 (not int) since this
	// interface is implemented by Kotlin — see cmd/mobile.BatteryProvider
	// for the same gomobile convention.
	StartCapture(deviceID string, tcpPort int32, width int32, height int32) error

	// StopCapture tells the platform to stop capturing/encoding and close
	// its connection to the loopback listener.
	StopCapture() error
}
