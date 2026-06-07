package logging

import (
	"math"
	"os"
	"strings"
	"testing"

	"github.com/linuxmatters/jivetalking/internal/processor"
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

// tableLines splits rendered table output into trimmed-right lines, dropping the
// trailing empty element from the final newline.
//
// lipgloss/table with a HiddenBorder renders as:
//
//	line 0  : header row
//	line 1  : blank separator (the hidden border under the header)
//	line 2+ : data rows
func tableLines(output string) []string {
	return strings.Split(strings.TrimRight(output, "\n"), "\n")
}

// headerLine returns the rendered header row (line 0).
func headerLine(t *testing.T, output string) string {
	t.Helper()
	lines := tableLines(output)
	if len(lines) < 1 {
		t.Fatalf("expected at least a header line, got %q", output)
	}
	return lines[0]
}

// dataLines returns the rendered data rows (line 2 onward, after the blank
// separator the hidden border emits under the header).
func dataLines(t *testing.T, output string) []string {
	t.Helper()
	lines := tableLines(output)
	if len(lines) < 3 {
		t.Fatalf("expected header + blank separator + at least one data row, got %d lines: %q", len(lines), output)
	}
	return lines[2:]
}

// countMissingCells counts standalone MissingValue cells in a rendered row,
// splitting on whitespace so a hyphen inside a negative value (e.g. "-10.0")
// is not mistaken for a missing-value marker.
func countMissingCells(row string) int {
	n := 0
	for field := range strings.FieldsSeq(row) {
		if field == MissingValue {
			n++
		}
	}
	return n
}

func TestMetricTableString(t *testing.T) {
	t.Run("basic_three_column", func(t *testing.T) {
		table := NewMetricTable()
		table.AddRow("Integrated Loudness", []string{"-23.0", "-18.5", "-16.0"}, "LUFS", "")
		table.AddRow("True Peak", []string{"-3.5", "-1.2", "-1.5"}, "dBTP", "")

		output := table.String()

		// Header row carries the three value-column headers, in order, and no
		// Interpretation column (no row has interpretation text).
		header := headerLine(t, output)
		for _, h := range []string{"Input", "Filtered", "Final"} {
			if !strings.Contains(header, h) {
				t.Errorf("header line %q should contain %q", header, h)
			}
		}
		if idxInput, idxFiltered, idxFinal := strings.Index(header, "Input"), strings.Index(header, "Filtered"), strings.Index(header, "Final"); idxInput >= idxFiltered || idxFiltered >= idxFinal {
			t.Errorf("headers out of order in %q (Input=%d Filtered=%d Final=%d)", header, idxInput, idxFiltered, idxFinal)
		}
		if strings.Contains(header, "Interpretation") {
			t.Errorf("header should not contain Interpretation column when no row has one: %q", header)
		}

		// Data rows carry label, all values, and unit.
		rows := dataLines(t, output)
		if len(rows) != 2 {
			t.Fatalf("expected 2 data rows, got %d: %q", len(rows), rows)
		}
		for _, want := range []string{"Integrated Loudness", "-23.0", "-18.5", "-16.0", "LUFS"} {
			if !strings.Contains(rows[0], want) {
				t.Errorf("first data row %q should contain %q", rows[0], want)
			}
		}
		for _, want := range []string{"True Peak", "-3.5", "-1.2", "-1.5", "dBTP"} {
			if !strings.Contains(rows[1], want) {
				t.Errorf("second data row %q should contain %q", rows[1], want)
			}
		}
	})

	t.Run("with_interpretation", func(t *testing.T) {
		table := NewMetricTable()
		table.AddRow("Noise Floor", []string{"-50.0", "-55.0", "-55.0"}, "dB", "Good noise reduction")

		output := table.String()

		// Interpretation column header appears when any row has interpretation text.
		header := headerLine(t, output)
		if !strings.Contains(header, "Interpretation") {
			t.Errorf("header should contain Interpretation column: %q", header)
		}

		rows := dataLines(t, output)
		if !strings.Contains(rows[0], "Good noise reduction") {
			t.Errorf("data row %q should contain interpretation text", rows[0])
		}
	})

	t.Run("missing_values", func(t *testing.T) {
		table := NewMetricTable()
		table.AddRow("Test Metric", []string{"-10.0", ""}, "dB", "") // Only 2 values for 3 columns

		output := table.String()

		// Two columns are missing values (empty string and absent third value);
		// both render as MissingValue. The present value remains.
		rows := dataLines(t, output)
		if !strings.Contains(rows[0], "-10.0") {
			t.Errorf("data row %q should contain the present value -10.0", rows[0])
		}
		if got := countMissingCells(rows[0]); got != 2 {
			t.Errorf("expected 2 missing-value markers %q in %q, got %d", MissingValue, rows[0], got)
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

		rows := dataLines(t, output)
		for _, want := range []string{"Test", "-23.5", "-18.2", "-16.0", "LUFS"} {
			if !strings.Contains(rows[0], want) {
				t.Errorf("AddMetricRow data row %q should contain %q", rows[0], want)
			}
		}
	})

	t.Run("add_metric_row_with_nan", func(t *testing.T) {
		table := NewMetricTable()
		table.AddMetricRow("Test", -23.5, math.NaN(), -16.0, 1, "LUFS", "")

		output := table.String()

		// NaN filtered value renders as MissingValue; the two valid values remain.
		rows := dataLines(t, output)
		for _, want := range []string{"Test", "-23.5", "-16.0", "LUFS"} {
			if !strings.Contains(rows[0], want) {
				t.Errorf("data row %q should contain %q", rows[0], want)
			}
		}
		if got := countMissingCells(rows[0]); got != 1 {
			t.Errorf("expected exactly 1 missing-value marker %q for the NaN value in %q, got %d", MissingValue, rows[0], got)
		}
	})
}

func TestMetricTableAlignment(t *testing.T) {
	table := NewMetricTable()
	table.AddRow("Short", []string{"1", "2", "3"}, "", "")
	table.AddRow("Much Longer Label", []string{"100", "200", "300"}, "", "")

	output := table.String()

	rows := dataLines(t, output)
	if len(rows) != 2 {
		t.Fatalf("expected 2 data rows, got %d: %q", len(rows), rows)
	}

	// Value columns are right-aligned: the rightmost digit of each value column
	// lines up across rows. Check the first value column ("1" vs "100") by
	// confirming the trailing position of each shares a column boundary.
	idxShort := strings.Index(rows[0], "1")
	idxLong := strings.Index(rows[1], "100")
	// "100" is wider; right alignment means "1" sits to the right of where "100"
	// starts, and both end at the same column.
	endShort := idxShort + len("1")
	endLong := idxLong + len("100")
	if endShort != endLong {
		t.Errorf("first value column not right-aligned: %q ends at %d, %q ends at %d", rows[0], endShort, rows[1], endLong)
	}

	// Labels are left-aligned: both rows start with their label at column 0.
	if !strings.HasPrefix(rows[0], "Short") {
		t.Errorf("label should be left-aligned at column 0: %q", rows[0])
	}
	if !strings.HasPrefix(rows[1], "Much Longer Label") {
		t.Errorf("label should be left-aligned at column 0: %q", rows[1])
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

func TestNormaliseForGain(t *testing.T) {
	tests := []struct {
		name         string
		rawValue     float64
		gainDB       float64
		scalingPower int
		wantNaN      bool
		wantApprox   float64 // only checked if wantNaN is false
		tolerance    float64 // relative tolerance for comparison
	}{
		{
			name:         "+18 dB gain on xG metric",
			rawValue:     0.004812,
			gainDB:       18.0,
			scalingPower: 1,
			wantApprox:   0.000606, // 0.004812 / 10^(18/20) = 0.004812 / 7.943
			tolerance:    0.001,
		},
		{
			name:         "+18 dB gain on xG2 metric",
			rawValue:     0.018876,
			gainDB:       18.0,
			scalingPower: 2,
			wantApprox:   0.000299, // 0.018876 / 10^(36/20) = 0.018876 / 63.096
			tolerance:    0.001,
		},
		{
			name:         "0 dB gain returns NaN",
			rawValue:     0.005,
			gainDB:       0.0,
			scalingPower: 1,
			wantNaN:      true,
		},
		{
			name:         "NaN input returns NaN",
			rawValue:     math.NaN(),
			gainDB:       18.0,
			scalingPower: 1,
			wantNaN:      true,
		},
		{
			name:         "NaN gain returns NaN",
			rawValue:     0.005,
			gainDB:       math.NaN(),
			scalingPower: 1,
			wantNaN:      true,
		},
		{
			name:         "negative gain (attenuation)",
			rawValue:     0.001,
			gainDB:       -6.0,
			scalingPower: 1,
			wantApprox:   0.001995, // 0.001 / 10^(-6/20) = 0.001 / 0.5012
			tolerance:    0.001,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normaliseForGain(tt.rawValue, tt.gainDB, tt.scalingPower)
			if tt.wantNaN {
				if !math.IsNaN(got) {
					t.Errorf("normaliseForGain() = %v, want NaN", got)
				}
				return
			}
			if math.IsNaN(got) {
				t.Errorf("normaliseForGain() = NaN, want %v", tt.wantApprox)
				return
			}
			// Check relative error
			relErr := math.Abs(got-tt.wantApprox) / math.Abs(tt.wantApprox)
			if relErr > tt.tolerance {
				t.Errorf("normaliseForGain() = %v, want approx %v (relative error %.4f > %.4f)", got, tt.wantApprox, relErr, tt.tolerance)
			}
		})
	}
}

func TestWriteSpeechRegionTableGainNormalisation(t *testing.T) {
	// Helper to create minimal speech metrics with plausible values
	makeSpeechMetrics := func() *processor.SpeechCandidateMetrics {
		return &processor.SpeechCandidateMetrics{
			RMSLevel:    -24.0,
			PeakLevel:   -12.0,
			CrestFactor: 12.0,
			Spectral: processor.SpectralMetrics{
				Mean:     0.004812,
				Variance: 0.018876,
				Centroid: 1500.0,
				Spread:   800.0,
				Skewness: 2.5,
				Kurtosis: 8.0,
				Entropy:  0.65,
				Flatness: 0.15,
				Crest:    12.0,
				Flux:     0.003200,
				Slope:    -0.000045,
				Decrease: -0.00012,
				Rolloff:  4500.0,
			},
		}
	}

	t.Run("with_normalisation_result", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "speech-table-test-*.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())

		input := &processor.AudioMeasurements{
			SpeechProfile: makeSpeechMetrics(),
		}
		filtered := &processor.OutputMeasurements{
			SpeechSample: makeSpeechMetrics(),
		}
		final := &processor.OutputMeasurements{
			SpeechSample: makeSpeechMetrics(),
		}
		normResult := &processor.NormalisationResult{
			OutputLUFS: -18.0,
			InputLUFS:  -36.0,
			Skipped:    false,
		}

		writeSpeechRegionTable(tmpFile, input, filtered, final, normResult)
		tmpFile.Close()

		data, err := os.ReadFile(tmpFile.Name())
		if err != nil {
			t.Fatal(err)
		}
		output := string(data)

		// Verify † markers appear on the 4 gain-dependent metrics
		for _, label := range []string{"Spectral Mean †", "Spectral Variance †", "Spectral Flux †", "Spectral Slope †"} {
			if !strings.Contains(output, label) {
				t.Errorf("expected gain-normalised label %q in output", label)
			}
		}

		// Verify footnote appears
		if !strings.Contains(output, "† Final values gain-normalised") {
			t.Error("expected gain-normalisation footnote in output")
		}
		if !strings.Contains(output, "18.0 dB") {
			t.Error("expected effective gain value '18.0 dB' in footnote")
		}
	})

	t.Run("without_normalisation_result", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "speech-table-test-*.txt")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())

		input := &processor.AudioMeasurements{
			SpeechProfile: makeSpeechMetrics(),
		}
		filtered := &processor.OutputMeasurements{
			SpeechSample: makeSpeechMetrics(),
		}
		final := &processor.OutputMeasurements{
			SpeechSample: makeSpeechMetrics(),
		}

		writeSpeechRegionTable(tmpFile, input, filtered, final, nil)
		tmpFile.Close()

		data, err := os.ReadFile(tmpFile.Name())
		if err != nil {
			t.Fatal(err)
		}
		output := string(data)

		// Verify NO † markers appear
		if strings.Contains(output, "†") {
			t.Error("expected no † markers when normalisation result is nil")
		}

		// Verify NO footnote
		if strings.Contains(output, "gain-normalised") {
			t.Error("expected no gain-normalisation footnote when normalisation result is nil")
		}
	})
}
