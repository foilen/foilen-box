package camera

type PlatformBridge interface {
	ListCameras() (string, error)

	ListMicrophones() (string, error)

	StartCapture(deviceID string, audioDeviceID string, tcpPort int32, width int32, height int32) error

	StopCapture() error
}
