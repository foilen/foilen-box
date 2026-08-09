package camera

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/gortsplib/v5/pkg/format/rtph264"
)

// h264ClockRate is RFC 6184's fixed RTP clock rate for H.264.
const h264ClockRate = 90000

// H.264 NAL unit types carrying the parameter sets (ITU-T H.264 Table 7-1).
const (
	nalTypeSPS = 7
	nalTypePPS = 8
)

// nalType extracts the H.264 NAL unit type (low 5 bits of the header byte).
func nalType(nal []byte) byte {
	if len(nal) == 0 {
		return 0
	}
	return nal[0] & 0x1f
}

// isVCLNAL reports whether t is a coded slice (the payload of a video
// frame), as opposed to a parameter set/SEI/delimiter NAL.
func isVCLNAL(t byte) bool {
	return t >= 1 && t <= 5
}

// readAnnexBUnits reads r (a raw Annex-B H.264 elementary stream, as
// produced by `ffmpeg ... -f h264 -`) and calls emit once per access unit:
// a run of non-VCL NALs (SPS/PPS/SEI/AUD) followed by exactly one VCL NAL,
// which is how a single-slice-per-frame encoder (x264 default, or a
// MediaCodec encoder) lays out its output. Returns when r is exhausted, ctx
// is cancelled, or emit returns an error.
func readAnnexBUnits(ctx context.Context, r io.Reader, emit func(au [][]byte) error) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var pending [][]byte

	err := scanAnnexBNALs(br, func(nal []byte) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(nal) == 0 {
			return nil
		}
		cp := make([]byte, len(nal))
		copy(cp, nal)
		pending = append(pending, cp)
		if isVCLNAL(nalType(cp)) {
			au := pending
			pending = nil
			return emit(au)
		}
		return nil
	})
	if err == io.EOF {
		return nil
	}
	return err
}

// trimTrailingZeros drops trailing zero bytes, which belong to a 4-byte
// start code (00 00 00 01) rather than the preceding NAL's payload.
func trimTrailingZeros(nal []byte) []byte {
	for len(nal) > 0 && nal[len(nal)-1] == 0 {
		nal = nal[:len(nal)-1]
	}
	return nal
}

// scanAnnexBNALs splits an Annex-B byte stream on 3- or 4-byte start codes
// (00 00 01 / 00 00 00 01), calling emit once per complete NAL unit found.
// Any bytes before the first start code are discarded. Returns io.EOF when
// the underlying reader is exhausted.
func scanAnnexBNALs(br *bufio.Reader, emit func(nal []byte) error) error {
	var nal []byte
	zeros := 0
	started := false

	for {
		b, err := br.ReadByte()
		if err != nil {
			return err
		}
		if b == 0x00 {
			zeros++
			nal = append(nal, b)
			continue
		}
		if b == 0x01 && zeros >= 2 {
			// nal currently ends with the 2-or-more zero bytes that make up
			// the start code just found; everything before them is the
			// previous NAL (if any).
			prev := trimTrailingZeros(nal[:len(nal)-zeros])
			if started {
				if err := emit(prev); err != nil {
					return err
				}
			}
			nal = nal[:0]
			zeros = 0
			started = true
			continue
		}
		zeros = 0
		nal = append(nal, b)
	}
}

// h264Publisher writes access units read from a capture stream into an RTSP
// ServerStream as RTP packets, timestamped off wall-clock elapsed time since
// the raw ffmpeg/MediaCodec output carries no usable presentation
// timestamps of its own.
type h264Publisher struct {
	stream *gortsplib.ServerStream
	media  *description.Media
	h264   *format.H264
	enc    *rtph264.Encoder

	start     time.Time
	startedAt uint32
}

func newH264Publisher(
	stream *gortsplib.ServerStream,
	media *description.Media,
	h264 *format.H264,
	enc *rtph264.Encoder,
	initialTimestamp uint32,
) *h264Publisher {
	return &h264Publisher{stream: stream, media: media, h264: h264, enc: enc, startedAt: initialTimestamp}
}

// updateParameterSets records au's SPS/PPS (if any) onto the stream's H264
// format and reloads its description, so a viewer that DESCRIBEs after
// capture already started — normal now that capture is no longer tied to a
// viewer connecting (see Status.ManualControl) — still gets them via SDP's
// sprop-parameter-sets, since a MediaCodec encoder only emits them once, up
// front, not before every IDR.
func (p *h264Publisher) updateParameterSets(au [][]byte) {
	var sps, pps []byte
	for _, nal := range au {
		switch nalType(nal) {
		case nalTypeSPS:
			sps = nal
		case nalTypePPS:
			pps = nal
		}
	}
	if (sps == nil || bytes.Equal(sps, p.h264.SPS)) && (pps == nil || bytes.Equal(pps, p.h264.PPS)) {
		return
	}
	if sps != nil {
		p.h264.SPS = append([]byte(nil), sps...)
	}
	if pps != nil {
		p.h264.PPS = append([]byte(nil), pps...)
	}
	p.stream.ReloadDesc()
}

func (p *h264Publisher) publish(au [][]byte) error {
	if p.start.IsZero() {
		p.start = time.Now()
	}
	p.updateParameterSets(au)
	packets, err := p.enc.Encode(au)
	if err != nil {
		return err
	}
	ts := p.startedAt + uint32(time.Since(p.start).Seconds()*h264ClockRate)
	for _, pkt := range packets {
		pkt.Timestamp = ts
		if err := p.stream.WritePacketRTP(p.media, pkt); err != nil {
			return err
		}
	}
	return nil
}
