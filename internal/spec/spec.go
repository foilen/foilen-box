// Package spec gathers a human-readable system information report (OS, CPU,
// RAM, GPU, disk, Go runtime), replacing the Java SpecPanel/SpecFragment's
// use of OperatingSystemMXBean/StatFs with the cross-platform gopsutil
// library.
package spec

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
)

// Summary is a compact, one-line-per-field version of Report, meant for
// display in a table (e.g. the Realm peers Specs subtab) rather than a full
// text dump. Any field is left empty if that piece of information couldn't
// be determined.
type Summary struct {
	CPU  string `json:"cpu"`
	Mem  string `json:"mem"`
	GPU  string `json:"gpu"`
	Disk string `json:"disk"`
}

// GetSummary returns the compact Summary. extraPath is used the same way as
// in Report: as the disk usage fallback when no system partition can be
// statted (e.g. Android's sandboxed app storage).
func GetSummary(extraPath string) Summary {
	var s Summary

	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		info := infos[0]
		var parts []string
		switch {
		case info.ModelName != "":
			parts = append(parts, info.ModelName)
		case info.VendorID != "":
			parts = append(parts, info.VendorID)
		}
		if info.Mhz > 0 {
			parts = append(parts, fmt.Sprintf("%.2f GHz", info.Mhz/1000))
		}
		parts = append(parts, fmt.Sprintf("%d cores", runtime.NumCPU()))
		s.CPU = strings.Join(parts, ", ")
	}

	if vmem, err := mem.VirtualMemory(); err == nil {
		s.Mem = fmt.Sprintf("%s / %s used", formatBytes(vmem.Used), formatBytes(vmem.Total))
	}

	if infos := gpuInfos(); len(infos) > 0 {
		names := make([]string, len(infos))
		for i, info := range infos {
			names[i] = info.String()
		}
		s.GPU = strings.Join(names, ", ")
	}

	if usage := primaryDiskUsage(extraPath); usage != nil {
		s.Disk = fmt.Sprintf("%s free of %s", formatBytes(usage.Free), formatBytes(usage.Total))
	}

	return s
}

// Report returns the full multi-section system information text. extraPath,
// if non-empty, is always included in the disk space section (as "App
// storage"), in addition to any system partitions gopsutil can enumerate.
// This matters on Android: sandboxed apps often can't stat the system's
// mount points, so extraPath (the app's private files dir) is the only
// storage location guaranteed to be readable there.
func Report(extraPath string) string {
	var sb strings.Builder

	sb.WriteString("=== Operating System ===\n")
	fmt.Fprintf(&sb, "Name:         %s\n", runtime.GOOS)
	fmt.Fprintf(&sb, "Architecture: %s\n", runtime.GOARCH)
	sb.WriteString("\n")

	sb.WriteString("=== CPU ===\n")
	fmt.Fprintf(&sb, "Available processors (cores): %d\n", runtime.NumCPU())
	if counts, err := cpu.Counts(true); err == nil {
		fmt.Fprintf(&sb, "Logical CPUs (reported by OS): %d\n", counts)
	}
	if counts, err := cpu.Counts(false); err == nil {
		fmt.Fprintf(&sb, "Physical cores (reported by OS): %d\n", counts)
	}
	if infos, err := cpu.Info(); err == nil && len(infos) > 0 {
		info := infos[0]
		fmt.Fprintf(&sb, "Vendor:       %s\n", info.VendorID)
		fmt.Fprintf(&sb, "Model:        %s\n", info.ModelName)
		if info.Family != "" || info.Model != "" {
			fmt.Fprintf(&sb, "Family/Model: %s/%s\n", info.Family, info.Model)
		}
		if info.Mhz > 0 {
			fmt.Fprintf(&sb, "Speed:        %.2f GHz\n", info.Mhz/1000)
		}
		if info.CacheSize > 0 {
			fmt.Fprintf(&sb, "Cache:        %d KB\n", info.CacheSize)
		}
	}
	sb.WriteString("\n")

	sb.WriteString("=== Memory (RAM) ===\n")
	if vmem, err := mem.VirtualMemory(); err == nil {
		fmt.Fprintf(&sb, "Total:     %s\n", formatBytes(vmem.Total))
		fmt.Fprintf(&sb, "Used:      %s (%.1f%%)\n", formatBytes(vmem.Used), vmem.UsedPercent)
		fmt.Fprintf(&sb, "Available: %s\n", formatBytes(vmem.Available))
		fmt.Fprintf(&sb, "Free:      %s\n", formatBytes(vmem.Free))
	} else {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		fmt.Fprintf(&sb, "Go Heap In Use: %s\n", formatBytes(m.HeapInuse))
		fmt.Fprintf(&sb, "Go Heap Sys:    %s\n", formatBytes(m.HeapSys))
	}
	if swap, err := mem.SwapMemory(); err == nil && swap.Total > 0 {
		fmt.Fprintf(&sb, "Swap Total: %s\n", formatBytes(swap.Total))
		fmt.Fprintf(&sb, "Swap Used:  %s (%.1f%%)\n", formatBytes(swap.Used), swap.UsedPercent)
	}
	sb.WriteString("\n")

	sb.WriteString("=== GPU ===\n")
	if infos := gpuInfos(); len(infos) > 0 {
		for _, info := range infos {
			fmt.Fprintf(&sb, "%s\n", info)
		}
	} else {
		sb.WriteString("(unavailable)\n")
	}
	sb.WriteString("\n")

	sb.WriteString("=== Disk Space ===\n")
	seen := map[string]bool{}
	if partitions, err := disk.Partitions(false); err == nil {
		for _, p := range partitions {
			usage, err := disk.Usage(p.Mountpoint)
			if err != nil {
				continue
			}
			seen[p.Mountpoint] = true
			fmt.Fprintf(&sb, "Path:        %s\n", p.Mountpoint)
			fmt.Fprintf(&sb, "  Total:     %s\n", formatBytes(usage.Total))
			fmt.Fprintf(&sb, "  Free:      %s\n", formatBytes(usage.Free))
			fmt.Fprintf(&sb, "  Used:      %s\n", formatBytes(usage.Used))
		}
	}
	if extraPath != "" && !seen[extraPath] {
		if usage, err := disk.Usage(extraPath); err == nil {
			fmt.Fprintf(&sb, "Path:        %s (App storage)\n", extraPath)
			fmt.Fprintf(&sb, "  Total:     %s\n", formatBytes(usage.Total))
			fmt.Fprintf(&sb, "  Free:      %s\n", formatBytes(usage.Free))
			fmt.Fprintf(&sb, "  Used:      %s\n", formatBytes(usage.Used))
		}
	}
	sb.WriteString("\n")

	sb.WriteString("=== Go Runtime ===\n")
	fmt.Fprintf(&sb, "Version: %s\n", runtime.Version())

	return sb.String()
}

// primaryDiskUsage returns the usage of the largest system partition
// gopsutil can stat, falling back to extraPath (see Report) if no system
// partition is reachable.
func primaryDiskUsage(extraPath string) *disk.UsageStat {
	var best *disk.UsageStat
	if partitions, err := disk.Partitions(false); err == nil {
		for _, p := range partitions {
			usage, err := disk.Usage(p.Mountpoint)
			if err != nil {
				continue
			}
			if best == nil || usage.Total > best.Total {
				best = usage
			}
		}
	}
	if best == nil && extraPath != "" {
		if usage, err := disk.Usage(extraPath); err == nil {
			best = usage
		}
	}
	return best
}

func formatBytes(bytes uint64) string {
	if bytes == 0 {
		return "0 B"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	value := float64(bytes)
	idx := 0
	for value >= 1024 && idx < len(units)-1 {
		value /= 1024
		idx++
	}
	return fmt.Sprintf("%.2f %s", value, units[idx])
}
