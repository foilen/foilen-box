package camera

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
)

// maxAACAccessUnit caps a single AAC-LC access unit; real ones are well
// under 1 KB (768 bytes/channel), so anything larger means a desynced or
// hostile audio stream.
const maxAACAccessUnit = 8 * 1024

// runSplitCapture publishes a raw Annex-B H.264 stream and a separate stream
// of length-prefixed AAC access units (the Android bridge's split transport,
// see PlatformBridge) to their RTSP media until ctx is cancelled or either
// source ends/errors.
func runSplitCapture(
	ctx context.Context,
	videoReader, audioReader io.Reader,
	stream *gortsplib.ServerStream,
	videoMedia *description.Media,
	h264Format *format.H264,
	audioMedia *description.Media,
	audioFormat *format.MPEG4Audio,
) error {
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

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Audio problems never tear down the video stream: the audio goroutine
	// just stops publishing and is unblocked when the capture's readers are
	// closed after this returns.
	go func() {
		err := readLengthPrefixedAAC(runCtx, audioReader, func(au []byte) error {
			return audioPub.publish([][]byte{au})
		})
		if err != nil && runCtx.Err() == nil {
			log.Printf("camera: audio stream stopped: %v", err)
		}
	}()

	videoErr := readAnnexBUnits(runCtx, videoReader, videoPub.publish)
	cancel()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return videoErr
}

// readLengthPrefixedAAC reads r as repeated [4-byte big-endian length]
// [access unit] frames, calling emit once per access unit, until r is
// exhausted, ctx is cancelled, or emit returns an error.
func readLengthPrefixedAAC(ctx context.Context, r io.Reader, emit func(au []byte) error) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var header [4]byte
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if _, err := io.ReadFull(br, header[:]); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		n := binary.BigEndian.Uint32(header[:])
		if n == 0 || n > maxAACAccessUnit {
			return fmt.Errorf("camera: implausible AAC access unit length %d", n)
		}
		au := make([]byte, n)
		if _, err := io.ReadFull(br, au); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return err
		}
		if err := emit(au); err != nil {
			return err
		}
	}
}
