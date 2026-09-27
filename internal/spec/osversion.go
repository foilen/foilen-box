package spec

import (
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v3/host"
)

var androidOSVersion string

func SetAndroidOSVersion(version string) {
	androidOSVersion = version
}

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
