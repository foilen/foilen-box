package camera

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtpmpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/mpeg4audio"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mpegts/codecs"
)

// Audio is captured as AAC-LC at a fixed rate/layout so the RTSP session
// description (built before capture starts) always matches the stream.
const (
	audioSampleRate   = 48000
	audioChannelCount = 2
)

// newMPEG4AudioFormat returns the RTP format advertised for the camera's
// audio track: AAC-LC at audioSampleRate / audioChannelCount.
func newMPEG4AudioFormat() *format.MPEG4Audio {
	return &format.MPEG4Audio{
		PayloadTyp: 97,
		Config: &mpeg4audio.AudioSpecificConfig{
			Type:          mpeg4audio.ObjectTypeAACLC,
			SampleRate:    audioSampleRate,
			ChannelConfig: audioChannelCount,
		},
		SizeLength:       13,
		IndexLength:      3,
		IndexDeltaLength: 3,
	}
}

// runMuxedCapture reads an MPEG-TS stream (H.264 video + AAC audio, as
// produced by ffmpegCapturer.start with an audio device selected) from
// reader and publishes each track to its RTSP media until ctx is cancelled
// or the source ends/errors.
func runMuxedCapture(
	ctx context.Context,
	reader io.Reader,
	stream *gortsplib.ServerStream,
	videoMedia *description.Media,
	h264Format *format.H264,
	audioMedia *description.Media,
	audioFormat *format.MPEG4Audio,
) error {
	r := &mpegts.Reader{R: bufio.NewReaderSize(reader, 64*1024)}
	if err := r.Initialize(); err != nil {
		return fmt.Errorf("camera: failed to read MPEG-TS header: %w", err)
	}

	videoEnc, err := h264Format.CreateEncoder()
	if err != nil {
		return err
	}
	videoPub := newH264Publisher(stream, videoMedia, h264Format, videoEnc, 0)

	audioEnc, err := audioFormat.CreateEncoder()
	if err != nil {
		return err
	}
	audioPub := &aacPublisher{stream: stream, media: audioMedia, enc: audioEnc}

	for _, track := range r.Tracks() {
		switch track.Codec.(type) {
		case *codecs.H264:
			r.OnDataH264(track, func(_, _ int64, au [][]byte) error {
				return videoPub.publish(au)
			})
		case *codecs.MPEG4Audio:
			r.OnDataMPEG4Audio(track, func(_ int64, aus [][]byte) error {
				return audioPub.publish(aus)
			})
		}
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := r.Read(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// aacPublisher writes AAC access units into an RTSP ServerStream as RTP
// packets, timestamped off wall-clock elapsed time since the muxed ffmpeg
// output carries no timestamps this pipeline preserves (mirrors
// h264Publisher).
type aacPublisher struct {
	stream *gortsplib.ServerStream
	media  *description.Media
	enc    *rtpmpeg4audio.Encoder

	start time.Time
}

func (p *aacPublisher) publish(aus [][]byte) error {
	if p.start.IsZero() {
		p.start = time.Now()
	}
	packets, err := p.enc.Encode(aus)
	if err != nil {
		return err
	}
	ts := uint32(time.Since(p.start).Seconds() * audioSampleRate)
	for _, pkt := range packets {
		pkt.Timestamp = ts
		if err := p.stream.WritePacketRTP(p.media, pkt); err != nil {
			return err
		}
	}
	return nil
}
