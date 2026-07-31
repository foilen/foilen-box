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

// BatteryProvider is implemented on the Android side (Kotlin's
// BatteryManager) and registered via SetAndroidBatteryProvider, since Go has
// no reliable way to read battery state there: /sys/class/power_supply is
// commonly blocked by SELinux for regular (non-system) apps, so
// batteryInfosSysfs's plain file reads silently find nothing. Split into two
// single-value methods (rather than one call returning percent+status)
// because gomobile bind doesn't support methods with more than one non-error
// return value.
type BatteryProvider interface {
	// BatteryPercent returns the current battery charge percentage (0-100),
	// or -1 if no battery is present/known.
	BatteryPercent() int32
	// BatteryStatus returns a human-readable charging status (e.g.
	// "Charging", "Discharging", "Full"); "" if unknown.
	BatteryStatus() string
}

var androidBatteryProvider BatteryProvider

// SetAndroidBatteryProvider registers the platform-specific battery provider
// (see BatteryProvider). Passing nil clears it, reverting to (best-effort,
// often empty on Android) sysfs-based detection.
func SetAndroidBatteryProvider(p BatteryProvider) {
	androidBatteryProvider = p
}

// batteryInfos returns the batteries detected on this system, best-effort.
// Like GPU detection, gopsutil has no cross-platform battery support, so this
// reads OS-specific sources; on any error (no battery present, e.g. a desktop
// PC, or a sandboxed environment) it just returns nil rather than failing the
// rest of the report.
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
		// Pure sysfs file reads (no exec), kept as a fallback for Android in
		// case no BatteryProvider has been registered yet (e.g.
		// RealmForegroundService starting the engine on boot, before
		// MainActivity exists); usually finds nothing there, per the SELinux
		// note above.
		return batteryInfosSysfs()
	case "darwin":
		return batteryInfosDarwin()
	case "windows":
		return batteryInfosWindows()
	default:
		return nil
	}
}

// --- Linux / Android (sysfs) ------------------------------------------------

// batteryInfosSysfs reads /sys/class/power_supply/*, which exposes one
// directory per power supply (battery, AC adapter, etc). Devices whose "type"
// isn't "Battery", or that report themselves absent via "present", are
// skipped.
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

// --- Darwin ------------------------------------------------------------

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

// --- Windows -------------------------------------------------------------

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
