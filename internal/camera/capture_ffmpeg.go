package camera

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"runtime"
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

func (ffmpegCapturer) start(ctx context.Context, deviceID string, resolution string) (io.ReadCloser, error) {
	inputArgs, err := ffmpegInputArgs(deviceID, resolution)
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
		"-f", "h264",
		"-",
	)

	cmd := exec.CommandContext(ctx, ffmpegBinary(), args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("camera: failed to open ffmpeg stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("camera: failed to start ffmpeg (device %q): %w", deviceID, err)
	}
	return &ffmpegProcess{cmd: cmd, stdout: stdout}, nil
}

// ffmpegInputArgs returns ffmpeg's OS-specific "-f <format> ... -i <input>"
// args addressing deviceID (as returned by listDevices) and requesting
// resolution (a "WIDTHxHEIGHT" string) from the device via -video_size.
func ffmpegInputArgs(deviceID string, resolution string) ([]string, error) {
	switch runtime.GOOS {
	case "linux":
		return []string{"-f", "v4l2", "-video_size", resolution, "-i", deviceID}, nil
	case "darwin":
		// "<video index>:none" explicitly selects no audio device.
		return []string{"-f", "avfoundation", "-framerate", "30", "-video_size", resolution, "-i", deviceID + ":none"}, nil
	case "windows":
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
}

func (p *ffmpegProcess) Read(b []byte) (int, error) { return p.stdout.Read(b) }

func (p *ffmpegProcess) Close() error {
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	return p.cmd.Wait()
}
