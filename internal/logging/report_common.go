package logging

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// writeSection writes a section header with title and dashed underline.
// The underline length matches the title length.
func writeSection(f *os.File, title string) {
	fmt.Fprintln(f, title)
	fmt.Fprintln(f, strings.Repeat("-", len(title)))
}

// loudnormModeString reports loudnorm's ACTUAL mode from its normalization_type
// JSON, not the requested config bool. When stats are available it reflects what
// loudnorm did; the "(target adjusted ...)" qualifier appears only when a linear
// target adjustment actually happened. When stats are nil it falls back to the
// config bool so the headline is still populated.
func loudnormModeString(result *processor.NormalisationResult, linear bool) string {
	if result != nil && result.LoudnormStats != nil {
		normType := strings.ToLower(strings.TrimSpace(result.LoudnormStats.NormalizationType))
		switch normType {
		case "linear":
			if result.LinearModeForced {
				return "Linear (target adjusted to prevent dynamic fallback)"
			}
			return "Linear"
		case "dynamic":
			return "Dynamic (fell back — output not linearly normalised)"
		}
	}

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
