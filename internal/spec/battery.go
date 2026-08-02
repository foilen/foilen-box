package spec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// batteryInfo is a best-effort description of one battery.
type batteryInfo struct {
	Percent int    // 0-100
	Status  string // e.g. "Charging", "Discharging", "Full"; "" if unknown
	Model   string // manufacturer/model, if known; "" otherwise
}

func (b batteryInfo) String() string {
	s := fmt.Sprintf("%d%%", b.Percent)
	if b.Status != "" {
		s += fmt.Sprintf(" (%s)", b.Status)
	}
	if b.Model != "" {
		s = b.Model + ": " + s
	}
	return s
}

// BatteryProvider is implemented on Android (Kotlin's BatteryManager), set via
// SetAndroidBatteryProvider — sysfs reads are usually SELinux-blocked there.
// Two single-value methods because gomobile bind can't return multiple values.
type BatteryProvider interface {
	// BatteryPercent returns 0-100, or -1 if unknown.
	BatteryPercent() int32
	// BatteryStatus returns e.g. "Charging", "Discharging", "Full"; "" if unknown.
	BatteryStatus() string
}

var androidBatteryProvider BatteryProvider

// SetAndroidBatteryProvider sets the platform battery provider; nil reverts to sysfs detection.
func SetAndroidBatteryProvider(p BatteryProvider) {
	androidBatteryProvider = p
}

// batteryInfos returns detected batteries, best-effort (nil on any error, e.g. no battery present).
func batteryInfos() []batteryInfo {
	if androidBatteryProvider != nil {
		percent := androidBatteryProvider.BatteryPercent()
		if percent < 0 {
			return nil
		}
		return []batteryInfo{{Percent: int(percent), Status: androidBatteryProvider.BatteryStatus()}}
	}

	switch runtime.GOOS {
	case "linux", "android":
		// Fallback for Android before a BatteryProvider is registered (e.g. boot);
		// usually finds nothing there due to SELinux.
		return batteryInfosSysfs()
	case "darwin":
		return batteryInfosDarwin()
	case "windows":
		return batteryInfosWindows()
	default:
		return nil
	}
}

// batteryInfosSysfs reads /sys/class/power_supply/*, skipping non-battery
// or absent devices.
func batteryInfosSysfs() []batteryInfo {
	matches, err := filepath.Glob("/sys/class/power_supply/*")
	if err != nil {
		return nil
	}

	var infos []batteryInfo
	for _, dir := range matches {
		typ, err := readSysfsString(filepath.Join(dir, "type"))
		if err != nil || !strings.EqualFold(typ, "Battery") {
			continue
		}
		if present, err := readSysfsString(filepath.Join(dir, "present")); err == nil && present == "0" {
			continue
		}
		percent, err := readSysfsInt(filepath.Join(dir, "capacity"))
		if err != nil {
			continue
		}

		info := batteryInfo{Percent: percent}
		if status, err := readSysfsString(filepath.Join(dir, "status")); err == nil {
			info.Status = status
		}
		manufacturer, _ := readSysfsString(filepath.Join(dir, "manufacturer"))
		model, _ := readSysfsString(filepath.Join(dir, "model_name"))
		info.Model = strings.TrimSpace(manufacturer + " " + model)

		infos = append(infos, info)
	}
	return infos
}

func readSysfsString(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func readSysfsInt(path string) (int, error) {
	s, err := readSysfsString(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(s)
}

var pmsetBattRe = regexp.MustCompile(`(\d+)%;\s*([a-zA-Z ]+);`)

func batteryInfosDarwin() []batteryInfo {
	out, err := exec.Command("pmset", "-g", "batt").Output()
	if err != nil {
		return nil
	}

	var infos []batteryInfo
	for _, line := range strings.Split(string(out), "\n") {
		m := pmsetBattRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		percent, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		infos = append(infos, batteryInfo{Percent: percent, Status: strings.TrimSpace(m[2])})
	}
	return infos
}

var winBatteryStatus = map[string]string{
	"1":  "Discharging",
	"2":  "On AC Power",
	"3":  "Full",
	"4":  "Low",
	"5":  "Critical",
	"6":  "Charging",
	"7":  "Charging",
	"8":  "Charging",
	"9":  "Charging",
	"10": "Undefined",
	"11": "Partially Charged",
}

func batteryInfosWindows() []batteryInfo {
	out, err := exec.Command("powershell", "-NoProfile", "-Command",
		`Get-CimInstance Win32_Battery | ForEach-Object { "$($_.EstimatedChargeRemaining)|$($_.BatteryStatus)|$($_.Name)" }`).Output()
	if err != nil {
		return nil
	}

	var infos []batteryInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		percent, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			continue
		}
		info := batteryInfo{Percent: percent, Status: winBatteryStatus[strings.TrimSpace(parts[1])]}
		if name := strings.TrimSpace(parts[2]); name != "" {
			info.Model = name
		}
		infos = append(infos, info)
	}
	return infos
}
