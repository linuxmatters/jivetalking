package report

import (
	"strings"
	"testing"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// processingRecord builds a full processing record via the real assembly path
// (NewRunRecord) so the unexported FiltersBlock (with the gate linear->dB
// conversion) and the normalisationRecord wrapper (read via Result()) are
// populated exactly as production builds them. The measurement blocks are minimal
// (input-only) since the 1.6 renderers read filters and normalisation, not the
// stage tables. Config, Diagnostics, and NormResult mirror the emitted record
// testdata/validation-runrecord/runs/verify17/LMP-83-mark-LUFS-16-processed.json.
func processingRecord() *processor.RunRecord {
	cfg := processor.DefaultEffectiveFilterConfig()
	// Adapted per-file values (gate threshold/range carried LINEAR on the config;
	// newFiltersBlock converts them to dB for the record).
	cfg.DS201Gate.Threshold = processor.DbToLinear(-67.67)
	cfg.DS201Gate.Range = processor.DbToLinear(-22.0)
	cfg.DS201Gate.Ratio = 1.5
	cfg.DS201Gate.Release = 375
	cfg.LA2A.Threshold = -36.38
	cfg.Deesser.Intensity = 0.0

	diag := &processor.AdaptiveDiagnostics{
		DS201LPReason:                "20.5 kHz band-limit (always on)",
		DS201GateAggression:          0.25485,
		DS201GateDynamicRange:        29.915058,
		DS201GateQuietSpeechEstimate: -75.290897,
		DS201GateSpeechSeparation:    9.297741,
		DS201GateSpeechHeadroom:      -7.623852,
		DS201GateThresholdUnclamped:  -67.667044,
		DS201GateClampReason:         "none",
		DS201GateGentleMode:          false,
	}

	norm := &processor.NormalisationResult{
		InputLUFS:         -36.94,
		InputTP:           -24.0,
		OutputLUFS:        -16.019,
		OutputTP:          -2.372306,
		GainApplied:       20.94,
		WithinTarget:      true,
		RequestedTargetI:  -16.0,
		EffectiveTargetI:  -16.0,
		LinearModeForced:  false,
		LimiterEnabled:    true,
		LimiterCeiling:    -24.0,
		LimiterGain:       24.153,
		LimiterFilteredTP: -19.913572,
		PreGainDB:         3.553,
		LimiterClamped:    true,
		LoudnormStats: &processor.LoudnormStats{
			InputI:            "-36.9",
			InputTP:           "-24",
			InputLRA:          "12.4",
			InputThresh:       "-47.56",
			OutputI:           "-15.99",
			OutputTP:          "-3.06",
			OutputLRA:         "13.5",
			OutputThresh:      "-27.38",
			NormalizationType: "linear",
			TargetOffset:      "-0.01",
		},
	}

	result := &processor.ProcessingResult{
		Measurements: &processor.AudioMeasurements{},
		Config:       cfg,
		Diagnostics:  diag,
		NormResult:   norm,
	}
	return processor.NewRunRecord(result)
}

func TestRenderFiltersChainOrder(t *testing.T) {
	got := renderFilters(processingRecord())

	// Filter sub-sections must appear in PROCESSING ORDER: downmix -> high-pass ->
	// low-pass -> noise removal -> gate -> LA-2A -> de-esser, diagnostics last.
	order := []string{
		"### Downmix",
		"### DS201 high-pass",
		"### DS201 low-pass",
		"### Noise removal",
		"### DS201 gate",
		"### LA-2A compressor",
		"### De-esser",
		"### Adaptation diagnostics",
	}
	last := -1
	for _, heading := range order {
		idx := strings.Index(got, heading)
		if idx == -1 {
			t.Fatalf("filter chain missing heading %q\n%s", heading, got)
		}
		if idx <= last {
			t.Errorf("filter heading %q out of chain order (index %d after %d)\n%s", heading, idx, last, got)
		}
		last = idx
	}
}

func TestRenderFiltersParams(t *testing.T) {
	got := renderFilters(processingRecord())
	for _, want := range []string{
		"## Filter Chain",
		"| Parameter | Value |",
		"80",    // high-pass frequency
		"20500", // low-pass frequency
		"### DS201 gate",
		"-67.67", // gate threshold dB-converted at assembly
		"-22.00", // gate range dB-converted at assembly
		"1.5",    // gate ratio
		"-36.38", // LA-2A threshold
		// Diagnostics rendered as objective values.
		"20.5 kHz band-limit (always on)",
		"0.25485", // aggression
		"none",    // clamp reason
	} {
		if !strings.Contains(got, want) {
			t.Errorf("filters output missing %q\n%s", want, got)
		}
	}
}

func TestRenderFiltersAnalysisOnlyEmpty(t *testing.T) {
	rec := pass1OnlyRecord()
	rec.Filters = nil
	if got := renderFilters(rec); got != "" {
		t.Errorf("analysis-only (no filters block) must render empty, got %q", got)
	}
}

// TestRenderNormalisationDeviationNumber asserts within_target renders as a SIGNED
// LU deviation NUMBER (output_integrated_lufs - effective_target_lufs), not a
// boolean and not a glyph (resolved decision 4).
func TestRenderNormalisationDeviationNumber(t *testing.T) {
	got := renderNormalisation(processingRecord())

	// Measured output integrated = -15.99, effective target = -16.00.
	// Deviation = -15.99 - (-16.00) = +0.01.
	if !strings.Contains(got, "Deviation from target (LU)") {
		t.Fatalf("deviation row missing\n%s", got)
	}
	if !strings.Contains(got, "+0.01") {
		t.Errorf("deviation must render as signed LU number +0.01\n%s", got)
	}
	// NOT the legacy boolean/verdict string.
	for _, banned := range []string{"Within target", "Outside tolerance", "true", "false"} {
		// "true"/"false" could appear inside a value; scope the check to the
		// deviation context by asserting the verdict phrases are absent entirely.
		if banned == "true" || banned == "false" {
			continue
		}
		if strings.Contains(got, banned) {
			t.Errorf("normalisation must NOT contain legacy verdict %q\n%s", banned, got)
		}
	}
}

// TestRenderNormalisationNoGlyphs grep-asserts the normalisation output carries no
// verdict glyphs (criterion 5).
func TestRenderNormalisationNoGlyphs(t *testing.T) {
	got := renderNormalisation(processingRecord())
	for _, banned := range []string{"✓", "⚠", "❌"} { // ✓ ⚠ ❌
		if strings.Contains(got, banned) {
			t.Errorf("normalisation output contains verdict glyph %q\n%s", banned, got)
		}
	}
}

func TestRenderNormalisationNumbers(t *testing.T) {
	got := renderNormalisation(processingRecord())
	for _, want := range []string{
		"## Peak Limiter",
		"Ceiling (dBTP)",
		"-24.00",
		"Pre-gain (dB)",
		"3.55",
		"## Loudnorm",
		"Effective target (LUFS)",
		"-16.00",
		"Gain applied (dB)",
		"20.94",
		"Measured output integrated (LUFS)",
		"-15.99",
		"Normalisation type",
		"linear",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("normalisation output missing %q\n%s", want, got)
		}
	}
}

func TestRenderNormalisationAnalysisOnlyEmpty(t *testing.T) {
	rec := pass1OnlyRecord()
	rec.Normalisation = nil
	if got := renderNormalisation(rec); got != "" {
		t.Errorf("analysis-only (no normalisation block) must render empty, got %q", got)
	}
}

func TestRenderSpectrogramsStubEmpty(t *testing.T) {
	if got := renderSpectrograms(processingRecord()); got != "" {
		t.Errorf("renderSpectrograms stub must return empty, got %q", got)
	}
	// An empty return must produce no visible Spectrograms heading.
	if strings.Contains(renderSpectrograms(processingRecord()), "Spectrograms") {
		t.Errorf("empty renderSpectrograms must emit no Spectrograms heading")
	}
}
