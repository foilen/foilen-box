package spec

import (
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v3/host"
)

// androidOSVersion holds the Android OS version (e.g. "13"), set via
// SetAndroidOSVersion. Go has no way to read it directly on Android: unlike
// desktop Linux, there's no /etc/os-release or similar gopsutil's
// host.PlatformInformation can read there, so the Kotlin side (which knows it
// via android.os.Build.VERSION.RELEASE) provides it instead.
var androidOSVersion string

// SetAndroidOSVersion registers the Android OS version, passed from Kotlin's
// MainActivity/RealmForegroundService (see cmd/mobile.StartServer). Passing
// "" leaves osName's Android output generic ("Android").
func SetAndroidOSVersion(version string) {
	androidOSVersion = version
}

// osName returns a human-readable OS name and version, e.g. "Ubuntu 22.04" on
// Linux or "Android 13" on Android, falling back to a generic name if the
// specific distribution/version can't be determined.
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
