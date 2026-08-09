package camera

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

// listDevices enumerates local capture devices via OS-specific means (no
// cgo/ioctl), mirroring internal/spec/gpu.go's best-effort shell-out style.
// Returns nil, nil on a platform with no local desktop capture support
// (e.g. "android", where Manager instead uses the PlatformBridge).
func listDevices() ([]Device, error) {
	switch runtime.GOOS {
	case "linux":
		return devicesLinux()
	case "darwin":
		return devicesDarwin()
	case "windows":
		return devicesWindows()
	default:
		return nil, nil
	}
}

// devicesLinux lists /dev/video* nodes, resolving each one's friendly name
// from sysfs (no ioctl needed). Some UVC webcams expose more than one node
// (e.g. a metadata node alongside the capture node); all are listed and a
// bad pick simply fails to capture, surfaced as a clear error.
func devicesLinux() ([]Device, error) {
	matches, err := filepath.Glob("/dev/video*")
	if err != nil {
		return nil, err
	}
	sort.Strings(matches)

	devices := make([]Device, 0, len(matches))
	for _, path := range matches {
		name := filepath.Base(path)
		label := name
		if data, err := os.ReadFile("/sys/class/video4linux/" + name + "/name"); err == nil {
			if trimmed := strings.TrimSpace(string(data)); trimmed != "" {
				label = trimmed
			}
		}
		devices = append(devices, Device{ID: path, Label: label})
	}
	return devices, nil
}

var avfoundationDeviceRe = regexp.MustCompile(`^\[.*\]\s*\[(\d+)\]\s*(.+)$`)

// devicesDarwin parses `ffmpeg -f avfoundation -list_devices true -i ""`,
// which always exits non-zero (it deliberately fails to open the dummy
// input after printing the device list to stderr) — so the exit error is
// expected and ignored.
func devicesDarwin() ([]Device, error) {
	cmd := exec.Command(ffmpegBinary(), "-f", "avfoundation", "-list_devices", "true", "-i", "")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var devices []Device
	inVideoSection := false
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.Contains(line, "AVFoundation video devices"):
			inVideoSection = true
		case strings.Contains(line, "AVFoundation audio devices"):
			inVideoSection = false
		case inVideoSection:
			if m := avfoundationDeviceRe.FindStringSubmatch(line); m != nil {
				devices = append(devices, Device{ID: m[1], Label: m[2]})
			}
		}
	}
	_ = cmd.Wait()
	return devices, nil
}

var dshowDeviceRe = regexp.MustCompile(`^\[.*\]\s*"(.+)"$`)

// devicesWindows parses `ffmpeg -f dshow -list_devices true -i dummy`,
// which (like avfoundation above) always exits non-zero after printing the
// device list to stderr.
func devicesWindows() ([]Device, error) {
	cmd := exec.Command(ffmpegBinary(), "-f", "dshow", "-list_devices", "true", "-i", "dummy")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var devices []Device
	inVideoSection := false
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.Contains(line, "DirectShow video devices"):
			inVideoSection = true
		case strings.Contains(line, "DirectShow audio devices"):
			inVideoSection = false
		case inVideoSection && strings.Contains(line, "Alternative name"):
			// skip: the alternate @device_pnp_... identifier, not the name we use
		case inVideoSection:
			if m := dshowDeviceRe.FindStringSubmatch(line); m != nil {
				devices = append(devices, Device{ID: m[1], Label: m[1]})
			}
		}
	}
	_ = cmd.Wait()
	return devices, nil
}
