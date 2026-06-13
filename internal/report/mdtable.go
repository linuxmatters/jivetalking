package report

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// =============================================================================
// Markdown table builder
// =============================================================================

// mdTable renders a GitHub-flavoured Markdown table: a header row, a `---`
// separator row, then one row per data slice. Cell content is escaped via
// escapeCell so a literal `|` or newline (e.g. the `|min|,|max|` glosses in the
// metric-definition catalogue) cannot break the table structure.
//
// Rows shorter than the header are padded with the placeholder; cells beyond
// the header width are dropped so the column count stays consistent.
func mdTable(headers []string, rows [][]string) string {
	var b strings.Builder

	hs := make([]string, len(headers))
	for i, h := range headers {
		hs[i] = escapeCell(h)
	}
	b.WriteString("| " + strings.Join(hs, " | ") + " |\n")

	sep := make([]string, len(headers))
	for i := range sep {
		sep[i] = "---"
	}
	b.WriteString("| " + strings.Join(sep, " | ") + " |\n")

	for _, row := range rows {
		cells := make([]string, len(headers))
		for i := range headers {
			if i < len(row) {
				cells[i] = escapeCell(row[i])
			} else {
				cells[i] = placeholder
			}
		}
		b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}

	return b.String()
}

// escapeCell makes a value safe to drop into a Markdown table cell: a literal
// pipe is backslash-escaped (GFM cell convention) and any newline or carriage
// return collapses to a space, so neither character can split the row or column.
// Image-link cells (![alt](path)) are unaffected, they carry no bare pipe or
// newline.
func escapeCell(s string) string {
	if !strings.ContainsAny(s, "|\n\r") {
		return s
	}
	r := strings.NewReplacer(
		"|", "\\|",
		"\n", " ",
		"\r", " ",
	)
	return r.Replace(s)
}

// =============================================================================
// Numeric metric formatters
//
// Thresholds and rendered strings match the legacy internal/logging table
// formatters EXACTLY for golden-test parity. Do not alter cutoffs or output
// tokens without regenerating the golden. The gain-normalised `†` columns are
// deliberately not carried over.
// =============================================================================

// digitalSilenceThreshold is the dBFS level at or below which a signal is
// treated as digital silence. FFmpeg reports -Inf for true digital zero;
// anything at or below -120 dBFS is effectively silent.
const digitalSilenceThreshold = -120.0

// lufsMeasurementFloor is the lowest reliable LUFS measurement from ebur128;
// values below it are too quiet to measure reliably.
const lufsMeasurementFloor = -70.0

// spectralSilenceValue is rendered for spectral metrics under digital silence,
// where the spectrum is undefined.
const spectralSilenceValue = "n/a"

// isDigitalSilence reports whether value represents digital silence: true zero
// (-Inf) or at/below the measurement floor.
func isDigitalSilence(value float64) bool {
	return math.IsInf(value, -1) || value <= digitalSilenceThreshold
}

// formatMetric formats a numeric value with appropriate precision: scientific
// notation for very small non-zero magnitudes (< 0.0001), the placeholder for
// NaN/Inf, otherwise fixed decimals.
func formatMetric(value float64, decimals int) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return placeholder
	}
	if value != 0 && math.Abs(value) < 0.0001 {
		return fmt.Sprintf("%.2e", value)
	}
	return formatFloat(value, decimals)
}

// formatMetricDB formats a dB value, rendering "< -120" for digital silence
// (-Inf or at/below the floor) and the placeholder for NaN/+Inf.
func formatMetricDB(value float64, decimals int) string {
	if math.IsNaN(value) || math.IsInf(value, 1) {
		return placeholder
	}
	if isDigitalSilence(value) {
		return "< -120"
	}
	return formatFloat(value, decimals)
}

// formatMetricLUFS formats a LUFS value, rendering "< -70" below the ebur128
// measurement floor and the placeholder for NaN/+Inf.
func formatMetricLUFS(value float64, decimals int) string {
	if math.IsNaN(value) || math.IsInf(value, 1) {
		return placeholder
	}
	if value < lufsMeasurementFloor {
		return "< -70"
	}
	return formatFloat(value, decimals)
}

// formatMetricPeak formats a linear peak (0.0-1.0) as dB, rendering "< -120"
// for digital silence (peak <= 0 or converted dB below the floor) and the
// placeholder for NaN.
func formatMetricPeak(value float64, decimals int) string {
	if math.IsNaN(value) {
		return placeholder
	}
	if value <= 0 {
		return "< -120"
	}
	dB := 20.0 * math.Log10(value)
	if dB < digitalSilenceThreshold {
		return "< -120"
	}
	return formatFloat(dB, decimals)
}

// formatMetricSpectral formats a spectral metric, rendering "n/a" under digital
// silence (the spectrum is undefined) and otherwise delegating to formatMetric.
func formatMetricSpectral(value float64, decimals int, digitalSilence bool) string {
	if digitalSilence {
		return spectralSilenceValue
	}
	return formatMetric(value, decimals)
}

// formatMetricSigned formats a value with an explicit sign for positives (e.g.
// "+2.5"), and the placeholder for NaN/Inf.
func formatMetricSigned(value float64, decimals int) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return placeholder
	}
	return fmt.Sprintf("%+.*f", decimals, value)
}

// =============================================================================
// Run-metadata formatters
// =============================================================================

// formatDuration formats a duration as a human-readable string: sub-minute as
// seconds, then "Mm Ss", then "Hh Mm Ss".
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

// channelName returns a human-readable channel-count name ("mono", "stereo", or
// "N channels").
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

// durationFromSeconds converts a float seconds value (the record carries audio
// duration as duration_s) into a time.Duration for formatDuration.
func durationFromSeconds(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

// formatSampleRate renders a sample rate in kHz with the unit suffix (e.g.
// "44.1 kHz"), or the placeholder when the rate is unknown (<= 0).
func formatSampleRate(hz int) string {
	if hz <= 0 {
		return placeholder
	}
	return formatFloat(float64(hz)/1000.0, 1) + " kHz"
}

// realTimeFactor computes the real-time factor: audio duration over wall-clock
// processing time. Returns 0 when total is non-positive, so the factor renders
// only once audio duration is known and time has elapsed.
func realTimeFactor(audioDurationSecs float64, total time.Duration) float64 {
	if total <= 0 {
		return 0
	}
	audioDuration := time.Duration(audioDurationSecs * float64(time.Second))
	return float64(audioDuration) / float64(total)
}
