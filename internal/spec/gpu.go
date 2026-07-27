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

// gpuInfo is a best-effort description of one display adapter.
type gpuInfo struct {
	Name      string
	VRAMBytes uint64 // 0 if unknown
}

func (g gpuInfo) String() string {
	if g.VRAMBytes > 0 {
		return fmt.Sprintf("%s (%s VRAM)", g.Name, formatBytes(g.VRAMBytes))
	}
	return g.Name
}

// gpuInfos returns the display adapters detected on this system, best-effort.
// Unlike CPU/RAM/disk, gopsutil has no cross-platform GPU support, so this
// shells out to whatever OS-specific tool reports it; on any error (tool
// missing, sandboxed environment such as Android) it just returns nil rather
// than failing the rest of the report. VRAM is filled in when it can be
// determined; it's left at 0 otherwise.
func gpuInfos() []gpuInfo {
	switch runtime.GOOS {
	case "linux":
		return gpuInfosLinux()
	case "darwin":
		return gpuInfosDarwin()
	case "windows":
		return gpuInfosWindows()
	default:
		return nil
	}
}

// --- Linux -------------------------------------------------------------

type pciDevice struct {
	slot, class, vendor, device string
	deviceID, revision          string
}

func gpuInfosLinux() []gpuInfo {
	devices := lspciVGADevices()
	if len(devices) == 0 {
		return nil
	}

	vramBySlot := gpuVRAMBySlotLinux()
	nvidiaSMIInfos := nvidiaSMIGPUs()

	var infos []gpuInfo
	nvIdx := 0
	for _, d := range devices {
		if strings.Contains(d.vendor, "NVIDIA") && nvIdx < len(nvidiaSMIInfos) {
			infos = append(infos, nvidiaSMIInfos[nvIdx])
			nvIdx++
			continue
		}
		name := amdgpuMarketingName(d.deviceID, d.revision)
		if name == "" {
			name = strings.TrimSpace(shortVendor(d.vendor) + " " + shortDevice(d.device))
		}
		info := gpuInfo{Name: name}
		if vram, ok := vramBySlot[d.slot]; ok {
			info.VRAMBytes = vram
		}
		infos = append(infos, info)
	}
	return infos
}

// lspciVGADevices runs `lspci -vmmnn`, whose block-structured "Key:\tValue"
// output (blank-line separated per device) is far easier and more robust to
// parse than the single-line human-readable format; the "nn" numeric IDs are
// needed to look up the precise marketing name in amdgpu.ids (see
// amdgpuMarketingName).
func lspciVGADevices() []pciDevice {
	out, err := exec.Command("lspci", "-vmmnn").Output()
	if err != nil {
		return nil
	}

	var devices []pciDevice
	cur := pciDevice{}
	flush := func() {
		switch cur.class {
		case "VGA compatible controller", "3D controller", "Display controller":
			devices = append(devices, cur)
		}
		cur = pciDevice{}
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			flush()
			continue
		}
		key, val, ok := strings.Cut(line, ":\t")
		if !ok {
			continue
		}
		switch key {
		case "Slot":
			cur.slot = val
		case "Class":
			cur.class, _ = splitTrailingID(val)
		case "Vendor":
			cur.vendor, _ = splitTrailingID(val)
		case "Device":
			cur.device, cur.deviceID = splitTrailingID(val)
		case "Rev":
			cur.revision = val
		}
	}
	flush()
	return devices
}

// splitTrailingID splits an lspci "-nn" value like "Navi 33 [Radeon RX
// 7700S] [7480]" into its name ("Navi 33 [Radeon RX 7700S]") and trailing
// numeric ID ("7480").
func splitTrailingID(s string) (name, id string) {
	s = strings.TrimSpace(s)
	if !strings.HasSuffix(s, "]") {
		return s, ""
	}
	start := strings.LastIndex(s, "[")
	if start < 0 {
		return s, ""
	}
	return strings.TrimSpace(s[:start]), s[start+1 : len(s)-1]
}

// shortVendor turns an lspci vendor string (e.g. "Advanced Micro Devices,
// Inc. [AMD/ATI]" or "NVIDIA Corporation") into a short marketing name.
func shortVendor(vendor string) string {
	switch {
	case strings.Contains(vendor, "Advanced Micro Devices") || strings.Contains(vendor, "AMD"):
		return "AMD"
	case strings.Contains(vendor, "NVIDIA"):
		return "NVIDIA"
	case strings.Contains(vendor, "Intel"):
		return "Intel"
	}
	if idx := strings.Index(vendor, "["); idx > 0 {
		return strings.TrimSpace(vendor[:idx])
	}
	if idx := strings.Index(vendor, ","); idx > 0 {
		return vendor[:idx]
	}
	return vendor
}

// shortDevice turns an lspci device string (e.g. "Navi 33 [Radeon RX
// 7600/7600 XT/7600M XT/7600S/7700S / PRO W7600]") into the first, most
// specific marketing name found in brackets ("Radeon RX 7600"), falling back
// to the raw string when there are no brackets.
func shortDevice(device string) string {
	start := strings.Index(device, "[")
	end := strings.LastIndex(device, "]")
	if start >= 0 && end > start {
		variant, _, _ := strings.Cut(device[start+1:end], "/")
		return strings.TrimSpace(variant)
	}
	return strings.TrimSpace(device)
}

// amdgpuIDsPaths are the locations where the amdgpu.ids database (shipped
// with libdrm/Mesa) is commonly installed across distros.
var amdgpuIDsPaths = []string{
	"/usr/share/libdrm/amdgpu.ids",
	"/usr/local/share/libdrm/amdgpu.ids",
}

// amdgpuMarketingName looks up the precise marketing name for an AMD GPU by
// PCI device ID and revision (e.g. "7480", "c1") in amdgpu.ids. This is
// needed because lspci's device string alone is often ambiguous: several
// distinct SKUs (e.g. RX 7600 vs RX 7700S) share the same PCI device ID and
// are only distinguished by revision. Returns "" if no match is found (tool
// missing, unknown ID/revision, non-AMD device).
func amdgpuMarketingName(deviceID, revision string) string {
	if deviceID == "" || revision == "" {
		return ""
	}
	for _, path := range amdgpuIDsPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if name := parseAmdgpuIDs(string(data), deviceID, revision); name != "" {
			return name
		}
	}
	return ""
}

func parseAmdgpuIDs(data, deviceID, revision string) string {
	deviceID = strings.ToUpper(strings.TrimSpace(deviceID))
	revision = strings.ToUpper(strings.TrimSpace(revision))
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ",", 3)
		if len(parts) != 3 {
			continue
		}
		if strings.ToUpper(strings.TrimSpace(parts[0])) == deviceID &&
			strings.ToUpper(strings.TrimSpace(parts[1])) == revision {
			return strings.TrimSpace(parts[2])
		}
	}
	return ""
}

// gpuVRAMBySlotLinux reads the amdgpu/i915-style
// /sys/class/drm/cardN/device/mem_info_vram_total sysfs file for every DRM
// card and returns the byte totals keyed by PCI slot (e.g. "03:00.0"), so
// they can be matched up against lspci's device list.
func gpuVRAMBySlotLinux() map[string]uint64 {
	result := map[string]uint64{}
	matches, err := filepath.Glob("/sys/class/drm/card[0-9]*/device")
	if err != nil {
		return result
	}
	for _, devPath := range matches {
		data, err := os.ReadFile(filepath.Join(devPath, "mem_info_vram_total"))
		if err != nil {
			continue
		}
		bytesVal, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
		if err != nil {
			continue
		}
		target, err := filepath.EvalSymlinks(devPath)
		if err != nil {
			continue
		}
		slot := strings.TrimPrefix(filepath.Base(target), "0000:")
		result[slot] = bytesVal
	}
	return result
}

// nvidiaSMIGPUs shells out to nvidia-smi, which (unlike lspci) reports both
// a clean marketing name and the total VRAM directly, in device order.
func nvidiaSMIGPUs() []gpuInfo {
	out, err := exec.Command("nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	var infos []gpuInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, memStr, ok := strings.Cut(line, ",")
		if !ok {
			continue
		}
		info := gpuInfo{Name: strings.TrimSpace(name)}
		if mib, err := strconv.ParseUint(strings.TrimSpace(memStr), 10, 64); err == nil {
			info.VRAMBytes = mib * 1024 * 1024
		}
		infos = append(infos, info)
	}
	return infos
}

// --- Darwin --------------------------------------------------------------

var (
	systemProfilerChipsetRe = regexp.MustCompile(`Chipset Model:\s*(.+)`)
	systemProfilerVRAMRe    = regexp.MustCompile(`VRAM \([^)]*\):\s*(.+)`)
	vramValueRe             = regexp.MustCompile(`^([\d.]+)\s*(MB|GB)$`)
)

func gpuInfosDarwin() []gpuInfo {
	out, err := exec.Command("system_profiler", "SPDisplaysDataType").Output()
	if err != nil {
		return nil
	}

	var infos []gpuInfo
	var cur *gpuInfo
	for _, line := range strings.Split(string(out), "\n") {
		if m := systemProfilerChipsetRe.FindStringSubmatch(line); m != nil {
			if cur != nil {
				infos = append(infos, *cur)
			}
			cur = &gpuInfo{Name: strings.TrimSpace(m[1])}
			continue
		}
		if cur != nil {
			if m := systemProfilerVRAMRe.FindStringSubmatch(line); m != nil {
				cur.VRAMBytes = parseVRAMString(m[1])
			}
		}
	}
	if cur != nil {
		infos = append(infos, *cur)
	}
	return infos
}

func parseVRAMString(s string) uint64 {
	m := vramValueRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	switch m[2] {
	case "GB":
		return uint64(val * 1024 * 1024 * 1024)
	case "MB":
		return uint64(val * 1024 * 1024)
	}
	return 0
}

// --- Windows ---------------------------------------------------------------

func gpuInfosWindows() []gpuInfo {
	// Win32_VideoController.AdapterRAM is a 32-bit field that overflows (and
	// misreports) for cards with >4GB of VRAM on modern Windows, so VRAM is
	// looked up separately from the driver's HardwareInformation.qwMemorySize
	// registry value and matched back to each adapter by name; AdapterRAM is
	// kept only as a fallback when no registry match is found.
	out, err := exec.Command("powershell", "-NoProfile", "-Command", `
Get-CimInstance Win32_VideoController | ForEach-Object { "NAME|$($_.Name)|$($_.AdapterRAM)" }
Get-ChildItem 'HKLM:\SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}' -ErrorAction SilentlyContinue | ForEach-Object {
  $p = Get-ItemProperty $_.PSPath -ErrorAction SilentlyContinue
  if ($p.'HardwareInformation.qwMemorySize') { "VRAM|$($p.DriverDesc)|$($p.'HardwareInformation.qwMemorySize')" }
}`).Output()
	if err != nil {
		return nil
	}

	vramByName := map[string]uint64{}
	var infos []gpuInfo
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			continue
		}
		kind, name, valStr := parts[0], strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
		val, err := strconv.ParseUint(valStr, 10, 64)
		switch kind {
		case "NAME":
			info := gpuInfo{Name: name}
			if err == nil {
				info.VRAMBytes = val
			}
			infos = append(infos, info)
		case "VRAM":
			if err == nil {
				vramByName[name] = val
			}
		}
	}

	for i := range infos {
		if vram, ok := vramByName[infos[i].Name]; ok {
			infos[i].VRAMBytes = vram
		}
	}
	return infos
}
