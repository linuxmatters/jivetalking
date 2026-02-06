// Package logging provides analysis report generation for processed audio files.
// This file contains reusable table formatting infrastructure for multi-column
// metric comparison tables (Input → Filtered → Final).

package logging

import (
	"fmt"
	"math"
	"strings"
)

// MetricRow represents a single row in a comparison table.
// Values are pre-formatted strings to allow for mixed formatting (decimals, scientific notation).
type MetricRow struct {
	Label          string   // Row label, e.g., "Integrated Loudness"
	Values         []string // One value per column (Input, Filtered, Final)
	Unit           string   // Unit suffix, e.g., "LUFS", "Hz", "" for unitless
	Interpretation string   // Optional interpretation text (only shown if non-empty)
}

// MetricTable formats aligned columns for metric comparison.
// Handles variable column widths, missing values, and optional interpretation column.
type MetricTable struct {
	Headers []string    // Column headers, e.g., ["Input", "Filtered", "Final"]
	Rows    []MetricRow // Data rows
}

// String renders the table with aligned columns.
// - Labels are left-aligned
// - Numeric values are right-aligned within their column
// - Units are appended after the last value column
// - Interpretation column only shown if any row has one
func (t *MetricTable) String() string {
	if len(t.Rows) == 0 {
		return ""
	}

	// Determine if we need an interpretation column
	hasInterpretation := false
	for _, row := range t.Rows {
		if row.Interpretation != "" {
			hasInterpretation = true
			break
		}
	}

	// Calculate column widths
	// Column 0: Label
	// Columns 1-N: Values (one per header)
	// Column N+1: Unit (if any rows have units)
	// Column N+2: Interpretation (if any rows have interpretations)

	labelWidth := 0
	for _, row := range t.Rows {
		if len(row.Label) > labelWidth {
			labelWidth = len(row.Label)
		}
	}

	// Value column widths (one per header)
	valueWidths := make([]int, len(t.Headers))
	for i, header := range t.Headers {
		valueWidths[i] = len(header) // Start with header width
	}
	for _, row := range t.Rows {
		for i, val := range row.Values {
			if i < len(valueWidths) && len(val) > valueWidths[i] {
				valueWidths[i] = len(val)
			}
		}
	}

	// Unit width (find max unit length)
	unitWidth := 0
	for _, row := range t.Rows {
		if len(row.Unit) > unitWidth {
			unitWidth = len(row.Unit)
		}
	}

	// Build output
	var sb strings.Builder

	// Header row
	sb.WriteString(strings.Repeat(" ", labelWidth+2)) // Label column + gap
	for i, header := range t.Headers {
		sb.WriteString(fmt.Sprintf("%*s  ", valueWidths[i], header))
	}
	if unitWidth > 0 {
		sb.WriteString(strings.Repeat(" ", unitWidth+1)) // Unit column placeholder
	}
	if hasInterpretation {
		sb.WriteString("Interpretation")
	}
	sb.WriteString("\n")

	// Data rows
	for _, row := range t.Rows {
		// Label (left-aligned)
		sb.WriteString(fmt.Sprintf("%-*s  ", labelWidth, row.Label))

		// Values (right-aligned within their columns)
		for i := 0; i < len(t.Headers); i++ {
			val := "-" // Default for missing values
			if i < len(row.Values) && row.Values[i] != "" {
				val = row.Values[i]
			}
			sb.WriteString(fmt.Sprintf("%*s  ", valueWidths[i], val))
		}

		// Unit (left-aligned, after values)
		if unitWidth > 0 {
			sb.WriteString(fmt.Sprintf("%-*s ", unitWidth, row.Unit))
		}

		// Interpretation (left-aligned, if present)
		if hasInterpretation {
			sb.WriteString(row.Interpretation)
		}

		sb.WriteString("\n")
	}

	return sb.String()
}

// =============================================================================
// Metric Formatting Helpers
// =============================================================================

// MissingValue is the placeholder for unavailable measurements
const MissingValue = "-"

// DigitalSilenceThreshold is the dBFS level below which we consider the signal to be digital silence.
// FFmpeg reports -Inf for true digital zero; anything below -120 dBFS is effectively silent.
const DigitalSilenceThreshold = -120.0

// isDigitalSilence returns true if the value represents digital silence (true zero or below threshold).
func isDigitalSilence(value float64) bool {
	return math.IsInf(value, -1) || value <= DigitalSilenceThreshold
}

// formatMetric formats a numeric value with appropriate precision.
// Handles:
// - Regular floats: formatted to specified decimal places
// - Very small values (< 0.0001): scientific notation
// - NaN/Inf: returns MissingValue
// - Zero: returns "0" with appropriate decimals
func formatMetric(value float64, decimals int) string {
	// Handle invalid values
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return MissingValue
	}

	// Use scientific notation for very small non-zero values
	if value != 0 && math.Abs(value) < 0.0001 {
		return fmt.Sprintf("%.2e", value)
	}

	// Standard formatting
	format := fmt.Sprintf("%%.%df", decimals)
	return fmt.Sprintf(format, value)
}

// formatMetricDB formats a dB value with special handling for digital silence.
// Shows "< -120" for values at or below the measurement floor (-Inf or very low values).
func formatMetricDB(value float64, decimals int) string {
	if math.IsNaN(value) || math.IsInf(value, 1) {
		return MissingValue
	}
	if isDigitalSilence(value) {
		return "< -120"
	}
	format := fmt.Sprintf("%%.%df", decimals)
	return fmt.Sprintf(format, value)
}

// LUFSMeasurementFloor is the lowest reliable LUFS measurement from ebur128.
// Values below this indicate signal too quiet to measure reliably.
const LUFSMeasurementFloor = -70.0

// formatMetricLUFS formats a LUFS value with special handling for values below measurement floor.
// Shows "< -70" for values below the ebur128 measurement threshold.
func formatMetricLUFS(value float64, decimals int) string {
	if math.IsNaN(value) || math.IsInf(value, 1) {
		return MissingValue
	}
	// ebur128 can report values well below -70 for near-silent signals
	// These are unreliable, so we show them as below threshold
	if value < LUFSMeasurementFloor {
		return "< -70"
	}
	format := fmt.Sprintf("%%.%df", decimals)
	return fmt.Sprintf(format, value)
}

// formatMetricPeak formats a linear peak value (0.0-1.0 scale) with dB conversion.
// For digital silence (peak = 0), shows "< -120" instead of -Inf.
func formatMetricPeak(value float64, decimals int) string {
	if math.IsNaN(value) {
		return MissingValue
	}
	// Linear peak of 0.0 means true digital silence
	if value <= 0 {
		return "< -120"
	}
	// Convert linear to dB
	dB := 20.0 * math.Log10(value)
	if dB < DigitalSilenceThreshold {
		return "< -120"
	}
	format := fmt.Sprintf("%%.%df", decimals)
	return fmt.Sprintf(format, dB)
}

// SpectralSilenceValue is the placeholder for spectral metrics when digital silence is detected.
// Spectral analysis is undefined for zero-signal audio - there's no spectrum to analyse.
const SpectralSilenceValue = "n/a"

// formatMetricSpectral formats a spectral metric value with special handling for digital silence.
// When isDigitalSilence is true, returns "n/a" since spectral metrics are undefined for zero signal.
func formatMetricSpectral(value float64, decimals int, isDigitalSilence bool) string {
	if isDigitalSilence {
		return SpectralSilenceValue
	}
	return formatMetric(value, decimals)
}

// formatMetricSigned formats a value with explicit sign for positive values.
// Useful for showing gain changes like "+2.5 dB" or "-1.2 dB".
func formatMetricSigned(value float64, decimals int) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return MissingValue
	}

	format := fmt.Sprintf("%%+.%df", decimals)
	return fmt.Sprintf(format, value)
}

// formatMetricWithUnit combines value and unit for display.
// Returns "value unit" if unit is non-empty, otherwise just "value".
func formatMetricWithUnit(value float64, decimals int, unit string) string {
	formatted := formatMetric(value, decimals)
	if formatted == MissingValue || unit == "" {
		return formatted
	}
	return formatted + " " + unit
}

// =============================================================================
// Table Builder Helpers
// =============================================================================

// NewMetricTable creates a new MetricTable with standard Input/Filtered/Final headers.
func NewMetricTable() *MetricTable {
	return &MetricTable{
		Headers: []string{"Input", "Filtered", "Final"},
		Rows:    make([]MetricRow, 0),
	}
}

// AddRow adds a row to the table with pre-formatted values.
func (t *MetricTable) AddRow(label string, values []string, unit string, interpretation string) {
	t.Rows = append(t.Rows, MetricRow{
		Label:          label,
		Values:         values,
		Unit:           unit,
		Interpretation: interpretation,
	})
}

// AddMetricRow adds a row with numeric values, formatting them automatically.
// Pass math.NaN() for missing values - they will display as "-".
func (t *MetricTable) AddMetricRow(label string, input, filtered, final float64, decimals int, unit string, interpretation string) {
	t.Rows = append(t.Rows, MetricRow{
		Label: label,
		Values: []string{
			formatMetric(input, decimals),
			formatMetric(filtered, decimals),
			formatMetric(final, decimals),
		},
		Unit:           unit,
		Interpretation: interpretation,
	})
}

// normaliseForGain compensates a spectral metric value for the gain applied during normalisation.
// scalingPower is 1 for metrics that scale linearly with gain (Mean, Slope)
// or 2 for metrics that scale with gain squared (Variance, Flux).
// Returns math.NaN() if rawValue is NaN, gain is NaN, or gainDB is 0
// (a zero gain means no normalisation occurred and the caller should use the raw value).
func normaliseForGain(rawValue, gainDB float64, scalingPower int) float64 {
	if math.IsNaN(rawValue) || math.IsNaN(gainDB) || gainDB == 0 {
		return math.NaN()
	}
	divisor := math.Pow(10, gainDB*float64(scalingPower)/20.0)
	return rawValue / divisor
}
