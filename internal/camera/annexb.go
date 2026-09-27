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

const h264ClockRate = 90000

const (
	nalTypeSPS = 7
	nalTypePPS = 8
)

func nalType(nal []byte) byte {
	if len(nal) == 0 {
		return 0
	}
	return nal[0] & 0x1f
}

func isVCLNAL(t byte) bool {
	return t >= 1 && t <= 5
}

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

func trimTrailingZeros(nal []byte) []byte {
	for len(nal) > 0 && nal[len(nal)-1] == 0 {
		nal = nal[:len(nal)-1]
	}
	return nal
}

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
