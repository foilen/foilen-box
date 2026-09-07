package camera

// PlatformBridge is implemented on Android (cmd/mobile.CameraBridge,
// CameraCaptureBridge.kt) to drive native camera capture; nil on desktop,
// where ffmpegCapturer is used instead. Passed to Manager.SetPlatformBridge.
type PlatformBridge interface {
	// ListCameras returns a JSON array of {"id":"...","label":"..."} objects.
	ListCameras() (string, error)

	// ListMicrophones returns a JSON array of {"id":"...","label":"..."}
	// objects, or "" / "[]" when the platform has no audio capture.
	ListMicrophones() (string, error)

	// StartCapture tells the platform to start encoding deviceID at
	// width x height to H.264 and stream the raw Annex-B bytes to a TCP
	// connection to 127.0.0.1:tcpPort, which the Go side is already
	// listening on. tcpPort/width/height are int32 (not int) since this
	// interface is implemented by Kotlin — see cmd/mobile.BatteryProvider
	// for the same gomobile convention.
	//
	// When audioDeviceID is non-empty the platform opens two connections to
	// tcpPort instead of one, each prefixed with a single tag byte ('V' for
	// the Annex-B H.264 stream, 'A' for a stream of length-prefixed raw
	// AAC-LC access units); with it empty it opens one untagged Annex-B
	// connection as before.
	StartCapture(deviceID string, audioDeviceID string, tcpPort int32, width int32, height int32) error

	// StopCapture tells the platform to stop capturing/encoding and close
	// its connection(s) to the loopback listener.
	StopCapture() error
}
