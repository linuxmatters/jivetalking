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
