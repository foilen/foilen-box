package camera

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// ffmpegPath is the ffmpeg binary to invoke. Empty (the default) resolves
// "ffmpeg" via $PATH; Nix builds override it via
// -ldflags -X foilen-box/internal/camera.ffmpegPath=/nix/store/.../bin/ffmpeg
// so the packaged app doesn't depend on ffmpeg being separately installed.
var ffmpegPath string

func ffmpegBinary() string {
	if ffmpegPath != "" {
		return ffmpegPath
	}
	return "ffmpeg"
}

// ffmpegCapturer captures a local webcam via ffmpeg's OS-native input
// backend (v4l2/avfoundation/dshow), encoding to raw Annex-B H.264 on
// stdout. Used on every desktop platform; Android uses capture_android.go's
// bridgeCapturer instead.
type ffmpegCapturer struct{}

func (ffmpegCapturer) listDevices() ([]Device, error) {
	return listDevices()
}

func (ffmpegCapturer) listAudioDevices() ([]Device, error) {
	return listAudioDevices()
}

func (ffmpegCapturer) start(ctx context.Context, deviceID, resolution, audioDeviceID string) (*captureStream, error) {
	inputArgs, err := ffmpegInputArgs(deviceID, resolution, audioDeviceID)
	if err != nil {
		return nil, err
	}
	args := append(append([]string{}, inputArgs...),
		"-c:v", "libx264",
		"-preset", "ultrafast",
		"-tune", "zerolatency",
		// x264 defaults to one slice per encoder thread (visible as multiple
		// VCL NALs per frame); readAnnexBUnits treats a VCL NAL as ending an
		// access unit, so multi-slice frames would get split into several
		// bogus access units. sliced-threads=0 keeps it to one slice/frame.
		"-x264-params", "sliced-threads=0",
		"-pix_fmt", "yuv420p",
	)
	if audioDeviceID != "" {
		args = append(args,
			"-c:a", "aac", "-b:a", "128k",
			"-ar", strconv.Itoa(audioSampleRate),
			"-ac", strconv.Itoa(audioChannelCount),
			"-f", "mpegts", "-",
		)
	} else {
		args = append(args, "-f", "h264", "-")
	}

	cmd := exec.CommandContext(ctx, ffmpegBinary(), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("camera: failed to open ffmpeg stdout pipe: %w", err)
	}
	stderr := &tailBuffer{max: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("camera: failed to start ffmpeg (device %q): %w", deviceID, err)
	}
	proc := &ffmpegProcess{cmd: cmd, stdout: stdout, stderr: stderr}
	return &captureStream{Closer: proc, video: proc, muxed: audioDeviceID != ""}, nil
}

// ffmpegInputArgs returns ffmpeg's OS-specific input args addressing
// deviceID (as returned by listDevices) at resolution (a "WIDTHxHEIGHT"
// string), plus a microphone input when audioDeviceID is non-empty (as
// returned by listAudioDevices).
func ffmpegInputArgs(deviceID, resolution, audioDeviceID string) ([]string, error) {
	switch runtime.GOOS {
	case "linux":
		args := []string{"-f", "v4l2", "-video_size", resolution, "-i", deviceID}
		if audioDeviceID != "" {
			args = append(args, "-f", "alsa", "-i", audioDeviceID)
		}
		return args, nil
	case "darwin":
		// avfoundation takes a single combined "<video>:<audio>" input;
		// "none" explicitly selects no audio device.
		audio := "none"
		if audioDeviceID != "" {
			audio = audioDeviceID
		}
		return []string{"-f", "avfoundation", "-framerate", "30", "-video_size", resolution, "-i", deviceID + ":" + audio}, nil
	case "windows":
		if audioDeviceID != "" {
			return []string{"-f", "dshow", "-video_size", resolution, "-i", "video=" + deviceID + ":audio=" + audioDeviceID}, nil
		}
		return []string{"-f", "dshow", "-video_size", resolution, "-i", "video=" + deviceID}, nil
	default:
		return nil, fmt.Errorf("camera: no ffmpeg capture support for %s", runtime.GOOS)
	}
}

// ffmpegProcess adapts a running ffmpeg subprocess's stdout to an
// io.ReadCloser: Close kills the process and reaps it.
type ffmpegProcess struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr *tailBuffer
}

// Read forwards stdout; when the stream ends because ffmpeg exited with a
// failure it replaces the bare io.EOF with an error carrying ffmpeg's last
// stderr lines, so callers (e.g. the MPEG-TS header read) surface why ffmpeg
// quit instead of an opaque "EOF".
func (p *ffmpegProcess) Read(b []byte) (int, error) {
	n, err := p.stdout.Read(b)
	if errors.Is(err, io.EOF) {
		if werr := p.cmd.Wait(); werr != nil {
			return n, fmt.Errorf("ffmpeg exited: %w%s", werr, p.stderr.tailLines())
		}
	}
	return n, err
}

func (p *ffmpegProcess) Close() error {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return p.cmd.Wait()
}

// tailBuffer is an io.Writer keeping only the last max bytes written.
type tailBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

// tailLines returns the last few non-empty stderr lines, prefixed with a
// newline, or "" when nothing was captured.
func (t *tailBuffer) tailLines() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var lines []string
	for _, l := range strings.Split(string(t.buf), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) == 0 {
		return ""
	}
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	return "\n" + strings.Join(lines, "\n")
}
