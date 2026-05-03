package logging

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// writeSection writes a section header with title and dashed underline.
// The underline length matches the title length.
func writeSection(f *os.File, title string) {
	fmt.Fprintln(f, title)
	fmt.Fprintln(f, strings.Repeat("-", len(title)))
}

// loudnormModeString converts linear bool to readable mode string
func loudnormModeString(linear bool) string {
	if linear {
		return "Linear (target adjusted to prevent dynamic fallback)"
	}
	return "Dynamic"
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}

	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60

	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}

	hours := minutes / 60
	minutes %= 60
	return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
}

// channelName returns a human-readable channel name
func channelName(channels int) string {
	switch channels {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	default:
		return fmt.Sprintf("%d channels", channels)
	}
}
