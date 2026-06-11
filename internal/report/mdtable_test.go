package report

import (
	"math"
	"testing"
	"time"
)

// TestMdTableStructure asserts a 2-column, 3-row table renders with the correct
// pipe/dash structure: header, `---` separator, then data rows.
func TestMdTableStructure(t *testing.T) {
	got := mdTable(
		[]string{"Metric", "Value"},
		[][]string{
			{"Integrated", "-16.0"},
			{"True Peak", "-1.5"},
			{"LRA", "7.2"},
		},
	)

	want := "" +
		"| Metric | Value |\n" +
		"| --- | --- |\n" +
		"| Integrated | -16.0 |\n" +
		"| True Peak | -1.5 |\n" +
		"| LRA | 7.2 |\n"

	if got != want {
		t.Errorf("table structure mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestMdTableShortRowPadding asserts a row shorter than the header is padded
// with the placeholder and an over-long row is truncated to the header width.
func TestMdTableShortRowPadding(t *testing.T) {
	got := mdTable(
		[]string{"A", "B"},
		[][]string{
			{"only-a"},
			{"x", "y", "z"},
		},
	)
	want := "" +
		"| A | B |\n" +
		"| --- | --- |\n" +
		"| only-a | - |\n" +
		"| x | y |\n"
	if got != want {
		t.Errorf("padding/truncation mismatch\n got: %q\nwant: %q", got, want)
	}
}

// TestIsDigitalSilence pins the legacy threshold semantics (table.go:131): -Inf
// or at/below -120 dBFS is digital silence.
func TestIsDigitalSilence(t *testing.T) {
	cases := []struct {
		in   float64
		want bool
	}{
		{math.Inf(-1), true},
		{-120.0, true},
		{-120.1, true},
		{-119.9, false},
		{-60.0, false},
		{0.0, false},
	}
	for _, c := range cases {
		if got := isDigitalSilence(c.in); got != c.want {
			t.Errorf("isDigitalSilence(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestFormatMetricDB pins the "< -120" digital-silence rendering and the
// placeholder for NaN/+Inf (table.go:158).
func TestFormatMetricDB(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{math.Inf(-1), "< -120"},  // true digital silence
		{-120.0, "< -120"},        // exactly at the floor
		{-130.0, "< -120"},        // below the floor
		{-119.9, "-119.9"},        // just above the floor: rendered
		{-16.0, "-16.0"},          // normal value
		{math.NaN(), placeholder}, // NaN -> placeholder
		{math.Inf(1), placeholder},
	}
	for _, c := range cases {
		if got := formatMetricDB(c.in, 1); got != c.want {
			t.Errorf("formatMetricDB(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatMetricLUFS pins the "< -70" measurement-floor rendering (table.go:174).
func TestFormatMetricLUFS(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{-70.1, "< -70"},          // below the floor
		{-70.0, "-70.0"},          // exactly at the floor: rendered (strict <)
		{-16.0, "-16.0"},          // normal value
		{math.NaN(), placeholder}, // NaN -> placeholder
		{math.Inf(1), placeholder},
	}
	for _, c := range cases {
		if got := formatMetricLUFS(c.in, 1); got != c.want {
			t.Errorf("formatMetricLUFS(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatMetricPeak pins linear-to-dB conversion and the "< -120" silence
// rendering for non-positive peaks (table.go:188).
func TestFormatMetricPeak(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0.0, "< -120"},           // true silence
		{-0.5, "< -120"},          // non-positive
		{1.0, "0.0"},              // full scale -> 0 dB
		{math.NaN(), placeholder}, // NaN -> placeholder
	}
	for _, c := range cases {
		if got := formatMetricPeak(c.in, 1); got != c.want {
			t.Errorf("formatMetricPeak(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatMetricSpectral pins the "n/a" silence rendering (table.go:210).
func TestFormatMetricSpectral(t *testing.T) {
	if got := formatMetricSpectral(0.5, 2, true); got != spectralSilenceValue {
		t.Errorf("digital-silence spectral = %q, want %q", got, spectralSilenceValue)
	}
	if got := formatMetricSpectral(0.5, 2, false); got != "0.50" {
		t.Errorf("formatMetricSpectral(0.5) = %q, want %q", got, "0.50")
	}
}

// TestFormatMetricScientific pins the scientific-notation path for very small
// non-zero magnitudes (table.go:148).
func TestFormatMetricScientific(t *testing.T) {
	if got := formatMetric(0.00001, 4); got != "1.00e-05" {
		t.Errorf("formatMetric(0.00001) = %q, want %q", got, "1.00e-05")
	}
	if got := formatMetric(0.0, 2); got != "0.00" {
		t.Errorf("formatMetric(0.0) = %q, want %q", got, "0.00")
	}
}

// TestFormatMetricSigned pins explicit-sign rendering (table.go:219).
func TestFormatMetricSigned(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{2.5, "+2.5"},
		{-1.2, "-1.2"},
		{0.0, "+0.0"},
		{math.NaN(), placeholder},
	}
	for _, c := range cases {
		if got := formatMetricSigned(c.in, 1); got != c.want {
			t.Errorf("formatMetricSigned(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestFormatDuration pins the legacy human-readable duration output
// (report_common.go:45).
func TestFormatDuration(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{500 * time.Millisecond, "0.5s"},
		{12500 * time.Millisecond, "12.5s"},
		{90 * time.Second, "1m 30s"},
		{59*time.Minute + 59*time.Second, "59m 59s"},
		{2*time.Hour + 3*time.Minute + 4*time.Second, "2h 3m 4s"},
	}
	for _, c := range cases {
		if got := formatDuration(c.in); got != c.want {
			t.Errorf("formatDuration(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestChannelName pins the legacy channel-name output (report_common.go:63).
func TestChannelName(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{1, "mono"},
		{2, "stereo"},
		{6, "6 channels"},
	}
	for _, c := range cases {
		if got := channelName(c.in); got != c.want {
			t.Errorf("channelName(%d) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRealTimeFactor pins the RTF maths from report.go:writeProcessingSummary:
// rtf = (durationSecs * time.Second) / total.
func TestRealTimeFactor(t *testing.T) {
	// 600s of audio processed in 60s -> 10x real-time.
	if got := realTimeFactor(600, 60*time.Second); got != 10.0 {
		t.Errorf("realTimeFactor(600, 60s) = %v, want 10", got)
	}
	// Non-positive total -> 0 (no division).
	if got := realTimeFactor(600, 0); got != 0 {
		t.Errorf("realTimeFactor(600, 0) = %v, want 0", got)
	}
}
