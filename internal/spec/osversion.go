package spec

import (
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v3/host"
)

// androidOSVersion holds the Android OS version (e.g. "13"), set via
// SetAndroidOSVersion. Go has no /etc/os-release equivalent on Android, so
// the Kotlin side (android.os.Build.VERSION.RELEASE) provides it instead.
var androidOSVersion string

// SetAndroidOSVersion sets the Android OS version, passed from Kotlin
// (cmd/mobile.StartServer). "" leaves osName's output generic ("Android").
func SetAndroidOSVersion(version string) {
	androidOSVersion = version
}

// osName returns a human-readable OS name and version, e.g. "Ubuntu 22.04" or
// "Android 13", falling back to a generic name if undetermined.
func osName() string {
	switch runtime.GOOS {
	case "android":
		if androidOSVersion != "" {
			return "Android " + androidOSVersion
		}
		return "Android"

	case "linux":
		if platform, _, version, err := host.PlatformInformation(); err == nil && platform != "" {
			name := strings.ToUpper(platform[:1]) + platform[1:]
			if version != "" {
				name += " " + version
			}
			return name
		}
		return "Linux"

	case "darwin":
		if _, _, version, err := host.PlatformInformation(); err == nil && version != "" {
			return "macOS " + version
		}
		return "macOS"

	case "windows":
		if platform, _, _, err := host.PlatformInformation(); err == nil && platform != "" {
			return platform
		}
		return "Windows"

	default:
		return runtime.GOOS
	}
}
