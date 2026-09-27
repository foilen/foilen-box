package camera

import (
	"context"
	"io"
)

type Device struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type capturer interface {
	listDevices() ([]Device, error)

	listAudioDevices() ([]Device, error)

	start(ctx context.Context, deviceID, resolution, audioDeviceID string) (*captureStream, error)
}

type captureStream struct {
	io.Closer
	video io.Reader
	audio io.Reader
	muxed bool
}
