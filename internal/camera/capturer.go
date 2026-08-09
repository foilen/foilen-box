package camera

import (
	"context"
	"io"
)

// Device is one enumerable camera, for the UI's device picker.
type Device struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// capturer produces a raw Annex-B H.264 elementary stream for a device.
type capturer interface {
	// listDevices enumerates available cameras.
	listDevices() ([]Device, error)

	// start begins capturing deviceID at resolution (a "WIDTHxHEIGHT"
	// string, see ParseResolution), returning a reader of raw Annex-B H.264
	// bytes. Closing the returned ReadCloser stops capture; ctx cancellation
	// also stops it.
	start(ctx context.Context, deviceID string, resolution string) (io.ReadCloser, error)
}
