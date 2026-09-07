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

// capturer produces an encoded stream for a device: raw Annex-B H.264 when
// audioDeviceID is empty, or video plus AAC audio when an audio device is
// selected (see captureStream).
type capturer interface {
	// listDevices enumerates available cameras.
	listDevices() ([]Device, error)

	// listAudioDevices enumerates available microphones. Returns nil on a
	// platform with no audio-capture support.
	listAudioDevices() ([]Device, error)

	// start begins capturing deviceID at resolution (a "WIDTHxHEIGHT"
	// string, see ParseResolution), optionally with audio from audioDeviceID
	// (as returned by listAudioDevices). ctx cancellation or closing the
	// returned captureStream stops capture.
	start(ctx context.Context, deviceID, resolution, audioDeviceID string) (*captureStream, error)
}

// captureStream is a running capture. Exactly one of these shapes applies:
//
//   - muxed: video carries an MPEG-TS mux of H.264 + AAC (ffmpeg with an
//     audio device selected).
//   - audio != nil: video is a raw Annex-B H.264 elementary stream and audio
//     is a stream of length-prefixed raw AAC-LC access units (4-byte
//     big-endian length + payload) — the Android capture path's split
//     equivalent of ffmpeg's mux.
//   - otherwise: video is a raw Annex-B H.264 elementary stream, no audio.
type captureStream struct {
	io.Closer
	video io.Reader
	audio io.Reader
	muxed bool
}
