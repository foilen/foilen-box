// Package report renders an AggregateResult as the same plain-text report
// the desktop UI used to build directly in internal/ui/early.go.
package report

import (
	"fmt"
	"sort"
	"strings"

	"foilen-box/internal/early/model"
)

// Format renders the full aggregate report text.
func Format(result *model.AggregateResult) string {
	var sb strings.Builder

	sb.WriteString("---[ Activity Day Tag ]---\n")
	appendSortedMap(&sb, result.DurationInSecByActivityDayTag)

	sb.WriteString("\n---[ Activity Tag ]---\n")
	appendSortedMap(&sb, result.DurationInSecByActivityTag)

	sb.WriteString("\n---[ Activity Day ]---\n")
	appendSortedMap(&sb, result.DurationInSecByActivityDay)

	sb.WriteString("\n---[ Activity ]---\n")
	appendSortedMap(&sb, result.DurationInSecByActivity)

	sb.WriteString("\n---[ TOTAL ]---\n")
	fmt.Fprintf(&sb, "  Total: %s\n", formatDuration(result.TotalDurationInSec))

	return sb.String()
}

func appendSortedMap(sb *strings.Builder, m map[string]int64) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(sb, "  %-60s %s\n", k, formatDuration(m[k]))
	}
}

func formatDuration(totalSec int64) string {
	hours := totalSec / 3600
	minutes := (totalSec % 3600) / 60
	seconds := totalSec % 60
	return fmt.Sprintf("%dh %02dm %02ds", hours, minutes, seconds)
}
