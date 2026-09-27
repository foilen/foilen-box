package camera

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

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

func listAudioDevices() ([]Device, error) {
	switch runtime.GOOS {
	case "linux":
		return audioDevicesLinux()
	case "darwin":
		return audioDevicesDarwin()
	case "windows":
		return audioDevicesWindows()
	default:
		return nil, nil
	}
}

var asoundPCMRe = regexp.MustCompile(`^(\d+)-(\d+):\s*([^:]+?)\s*:`)

func audioDevicesLinux() ([]Device, error) {
	var devices []Device

	data, err := os.ReadFile("/proc/asound/pcm")
	if err != nil {
		return devices, nil
	}
	cardNames := asoundCardNames()
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.Contains(line, "capture") {
			continue
		}
		m := asoundPCMRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		card, _ := strconv.Atoi(m[1])
		dev, _ := strconv.Atoi(m[2])
		label := strings.TrimSpace(m[3])
		if name := cardNames[card]; name != "" {
			label = name + " — " + label
		}
		devices = append(devices, Device{ID: fmt.Sprintf("hw:%d,%d", card, dev), Label: label})
	}
	return devices, nil
}

var asoundCardRe = regexp.MustCompile(`^\s*(\d+)\s*\[[^\]]*\]:\s*(.+)$`)

func asoundCardNames() map[int]string {
	names := map[int]string{}
	data, err := os.ReadFile("/proc/asound/cards")
	if err != nil {
		return names
	}
	for _, line := range strings.Split(string(data), "\n") {
		if m := asoundCardRe.FindStringSubmatch(line); m != nil {
			id, _ := strconv.Atoi(m[1])
			names[id] = strings.TrimSpace(m[2])
		}
	}
	return names
}

func audioDevicesDarwin() ([]Device, error) {
	return ffmpegListDevices(
		[]string{"-f", "avfoundation", "-list_devices", "true", "-i", ""},
		"AVFoundation audio devices",
		"AVFoundation video devices",
		func(line string) *Device {
			if m := avfoundationDeviceRe.FindStringSubmatch(line); m != nil {
				return &Device{ID: m[1], Label: m[2]}
			}
			return nil
		},
	)
}

func audioDevicesWindows() ([]Device, error) {
	return ffmpegListDevices(
		[]string{"-f", "dshow", "-list_devices", "true", "-i", "dummy"},
		"DirectShow audio devices",
		"DirectShow video devices",
		func(line string) *Device {
			if strings.Contains(line, "Alternative name") {
				return nil
			}
			if m := dshowDeviceRe.FindStringSubmatch(line); m != nil {
				return &Device{ID: m[1], Label: m[1]}
			}
			return nil
		},
	)
}

func ffmpegListDevices(args []string, startMarker, stopMarker string, parse func(string) *Device) ([]Device, error) {
	cmd := exec.Command(ffmpegBinary(), args...)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	var devices []Device
	inSection := false
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.Contains(line, startMarker):
			inSection = true
		case strings.Contains(line, stopMarker):
			inSection = false
		case inSection:
			if d := parse(line); d != nil {
				devices = append(devices, *d)
			}
		}
	}
	_ = cmd.Wait()
	return devices, nil
}

var avfoundationDeviceRe = regexp.MustCompile(`^\[.*\]\s*\[(\d+)\]\s*(.+)$`)

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

		case inVideoSection:
			if m := dshowDeviceRe.FindStringSubmatch(line); m != nil {
				devices = append(devices, Device{ID: m[1], Label: m[1]})
			}
		}
	}
	_ = cmd.Wait()
	return devices, nil
}
