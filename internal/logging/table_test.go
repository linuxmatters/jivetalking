package logging

import (
	"math"
	"strings"
	"testing"
)

func TestFormatMetric(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		decimals int
		want     string
	}{
		{"zero", 0.0, 2, "0.00"},
		{"positive", 3.14159, 2, "3.14"},
		{"negative", -16.5, 1, "-16.5"},
		{"large", 12345.6789, 2, "12345.68"},
		{"small_normal", 0.001, 3, "0.001"},
		{"very_small_scientific", 0.00001, 2, "1.00e-05"},
		{"very_small_negative", -0.00001, 2, "-1.00e-05"},
		{"nan", math.NaN(), 2, MissingValue},
		{"positive_inf", math.Inf(1), 2, MissingValue},
		{"negative_inf", math.Inf(-1), 2, MissingValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMetric(tt.value, tt.decimals)
			if got != tt.want {
				t.Errorf("formatMetric(%v, %d) = %q, want %q", tt.value, tt.decimals, got, tt.want)
			}
		})
	}
}

func TestFormatMetricSigned(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		decimals int
		want     string
	}{
		{"positive", 2.5, 1, "+2.5"},
		{"negative", -1.2, 1, "-1.2"},
		{"zero", 0.0, 1, "+0.0"},
		{"nan", math.NaN(), 1, MissingValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMetricSigned(tt.value, tt.decimals)
			if got != tt.want {
				t.Errorf("formatMetricSigned(%v, %d) = %q, want %q", tt.value, tt.decimals, got, tt.want)
			}
		})
	}
}

func TestFormatMetricWithUnit(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		decimals int
		unit     string
		want     string
	}{
		{"with_unit", -16.0, 1, "LUFS", "-16.0 LUFS"},
		{"no_unit", 1234.5, 1, "", "1234.5"},
		{"nan_with_unit", math.NaN(), 1, "Hz", MissingValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMetricWithUnit(tt.value, tt.decimals, tt.unit)
			if got != tt.want {
				t.Errorf("formatMetricWithUnit(%v, %d, %q) = %q, want %q", tt.value, tt.decimals, tt.unit, got, tt.want)
			}
		})
	}
}

func TestMetricTableString(t *testing.T) {
	t.Run("basic_three_column", func(t *testing.T) {
		table := NewMetricTable()
		table.AddRow("Integrated Loudness", []string{"-23.0", "-18.5", "-16.0"}, "LUFS", "")
		table.AddRow("True Peak", []string{"-3.5", "-1.2", "-1.5"}, "dBTP", "")

		output := table.String()

		// Verify headers present
		if !strings.Contains(output, "Input") {
			t.Error("Output should contain 'Input' header")
		}
		if !strings.Contains(output, "Filtered") {
			t.Error("Output should contain 'Filtered' header")
		}
		if !strings.Contains(output, "Final") {
			t.Error("Output should contain 'Final' header")
		}

		// Verify data present
		if !strings.Contains(output, "Integrated Loudness") {
			t.Error("Output should contain row label")
		}
		if !strings.Contains(output, "-16.0") {
			t.Error("Output should contain value")
		}
		if !strings.Contains(output, "LUFS") {
			t.Error("Output should contain unit")
		}
	})

	t.Run("with_interpretation", func(t *testing.T) {
		table := NewMetricTable()
		table.AddRow("Noise Floor", []string{"-50.0", "-55.0", "-55.0"}, "dB", "Good noise reduction")

		output := table.String()

		if !strings.Contains(output, "Interpretation") {
			t.Error("Output should contain 'Interpretation' header when rows have interpretations")
		}
		if !strings.Contains(output, "Good noise reduction") {
			t.Error("Output should contain interpretation text")
		}
	})

	t.Run("missing_values", func(t *testing.T) {
		table := NewMetricTable()
		table.AddRow("Test Metric", []string{"-10.0", ""}, "dB", "") // Only 2 values for 3 columns

		output := table.String()

		// Missing values should show as "-"
		if !strings.Contains(output, " -  ") {
			t.Error("Missing values should display as dash")
		}
	})

	t.Run("empty_table", func(t *testing.T) {
		table := NewMetricTable()
		output := table.String()

		if output != "" {
			t.Errorf("Empty table should return empty string, got %q", output)
		}
	})

	t.Run("add_metric_row", func(t *testing.T) {
		table := NewMetricTable()
		table.AddMetricRow("Test", -23.5, -18.2, -16.0, 1, "LUFS", "")

		output := table.String()

		if !strings.Contains(output, "-23.5") {
			t.Error("AddMetricRow should format input value")
		}
		if !strings.Contains(output, "-18.2") {
			t.Error("AddMetricRow should format filtered value")
		}
		if !strings.Contains(output, "-16.0") {
			t.Error("AddMetricRow should format final value")
		}
	})

	t.Run("add_metric_row_with_nan", func(t *testing.T) {
		table := NewMetricTable()
		table.AddMetricRow("Test", -23.5, math.NaN(), -16.0, 1, "LUFS", "")

		output := table.String()

		// NaN should display as "-"
		lines := strings.Split(output, "\n")
		if len(lines) < 2 {
			t.Fatal("Expected at least 2 lines (header + data)")
		}
		dataLine := lines[1]
		// Count dashes - should have one for NaN value
		if !strings.Contains(dataLine, " -  ") && !strings.Contains(dataLine, " - ") {
			t.Errorf("NaN value should display as dash in: %q", dataLine)
		}
	})
}

func TestMetricTableAlignment(t *testing.T) {
	table := NewMetricTable()
	table.AddRow("Short", []string{"1", "2", "3"}, "", "")
	table.AddRow("Much Longer Label", []string{"100", "200", "300"}, "", "")

	output := table.String()
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")

	if len(lines) < 3 {
		t.Fatalf("Expected 3 lines (header + 2 data), got %d", len(lines))
	}

	// All data lines should have same position for first value column
	// (values are right-aligned, so the rightmost digit should align)
	// This is a basic check that formatting is consistent
	for i := 1; i < len(lines); i++ {
		if len(lines[i]) < 20 {
			t.Errorf("Line %d seems too short: %q", i, lines[i])
		}
	}
}

func TestIsDigitalSilence(t *testing.T) {
	tests := []struct {
		name  string
		value float64
		want  bool
	}{
		{"negative_infinity", math.Inf(-1), true},
		{"below_threshold", -150.0, true},
		{"at_threshold", -120.0, true},
		{"just_above_threshold", -119.9, false},
		{"normal_value", -60.0, false},
		{"positive_infinity", math.Inf(1), false}, // +Inf is not digital silence
		{"nan", math.NaN(), false},                // NaN is handled separately
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isDigitalSilence(tt.value)
			if got != tt.want {
				t.Errorf("isDigitalSilence(%v) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestFormatMetricDB(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		decimals int
		want     string
	}{
		{"normal_value", -50.0, 1, "-50.0"},
		{"digital_silence_inf", math.Inf(-1), 1, "< -120"},
		{"digital_silence_threshold", -120.0, 1, "< -120"},
		{"digital_silence_below", -150.0, 1, "< -120"},
		{"just_above_threshold", -119.9, 1, "-119.9"},
		{"nan", math.NaN(), 1, MissingValue},
		{"positive_inf", math.Inf(1), 1, MissingValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMetricDB(tt.value, tt.decimals)
			if got != tt.want {
				t.Errorf("formatMetricDB(%v, %d) = %q, want %q", tt.value, tt.decimals, got, tt.want)
			}
		})
	}
}

func TestFormatMetricLUFS(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		decimals int
		want     string
	}{
		{"normal_value", -23.0, 1, "-23.0"},
		{"at_floor", -70.0, 1, "-70.0"},
		{"below_floor", -163.0, 1, "< -70"},
		{"way_below_floor", -171.9, 1, "< -70"},
		{"nan", math.NaN(), 1, MissingValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMetricLUFS(tt.value, tt.decimals)
			if got != tt.want {
				t.Errorf("formatMetricLUFS(%v, %d) = %q, want %q", tt.value, tt.decimals, got, tt.want)
			}
		})
	}
}

func TestFormatMetricPeak(t *testing.T) {
	tests := []struct {
		name     string
		value    float64
		decimals int
		want     string
	}{
		{"full_scale", 1.0, 1, "0.0"},
		{"half_scale", 0.5, 1, "-6.0"},
		{"low_level", 0.01, 1, "-40.0"},
		{"digital_silence_zero", 0.0, 1, "< -120"},
		{"digital_silence_negative", -0.001, 1, "< -120"}, // Invalid, but handle gracefully
		{"nan", math.NaN(), 1, MissingValue},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatMetricPeak(tt.value, tt.decimals)
			if got != tt.want {
				t.Errorf("formatMetricPeak(%v, %d) = %q, want %q", tt.value, tt.decimals, got, tt.want)
			}
		})
	}
}
