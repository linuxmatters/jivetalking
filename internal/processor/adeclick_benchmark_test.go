package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
)

const (
	adeclickBenchmarkCaptureEnv      = "JIVETALKING_ADECLICK_BENCH_CAPTURE"
	adeclickBenchmarkCaptureRootEnv  = "JIVETALKING_ADECLICK_BENCH_CAPTURE_ROOT"
	adeclickBenchmarkMediaCaptureEnv = "JIVETALKING_ADECLICK_BENCH_MEDIA_CAPTURE"
	adeclickBenchmarkCaptureRoot     = ".bench/adeclick"
	adeclickBenchmarkBaselineVariant = "adeclick_current_t_2_0_w_55_o_50_m_s"
	adeclickBenchmarkExcerptSecs     = 30.0
)

// adeclickBenchmarkParams describes the variant-specific adeclick clause.
// Enabled=false produces no adeclick clause, modelling the without_adeclick variant.
type adeclickBenchmarkParams struct {
	Enabled   bool
	Threshold float64
	Window    float64
	Overlap   float64
	Method    string // empty = default ("a"); "s" = spline interpolation
}

// Clause renders the adeclick FFmpeg filter clause for the variant, or empty
// when the variant disables adeclick.
func (p adeclickBenchmarkParams) Clause() string {
	if !p.Enabled {
		return ""
	}
	clause := fmt.Sprintf("adeclick=t=%.1f:w=%.0f:o=%.0f", p.Threshold, p.Window, p.Overlap)
	if p.Method != "" {
		clause += ":m=" + p.Method
	}
	return clause
}

// adeclickBenchmarkVariant identifies a Pass 4 candidate by name with its
// adeclick clause parameters. The intent and validation priority mirror the
// shape of the ANLMDN benchmark manifest.
type adeclickBenchmarkVariant struct {
	Name               string
	ParameterIntent    string
	ValidationPriority string
	Params             adeclickBenchmarkParams
}

// adeclickBenchmarkVariants returns the focused Pass 4 adeclick benchmark
// matrix in proposal order. The current production setting is the second
// entry; the first variant disables adeclick to provide an upper-bound
// "no repair" reference.
func adeclickBenchmarkVariants() []adeclickBenchmarkVariant {
	return []adeclickBenchmarkVariant{
		{
			Name:               "without_adeclick",
			ParameterIntent:    "Pass 4 without click/pop repair, upper-bound speed reference.",
			ValidationPriority: "no-adeclick-baseline",
			Params:             adeclickBenchmarkParams{Enabled: false},
		},
		{
			Name:               "adeclick_current_t_2_0_w_55_o_50_m_s",
			ParameterIntent:    "Current production settings: t=2.0, w=55ms, o=50% overlap, spline interpolation (m=s).",
			ValidationPriority: "production-default",
			Params: adeclickBenchmarkParams{
				Enabled:   true,
				Threshold: 2.0,
				Window:    55.0,
				Overlap:   50.0,
				Method:    "s",
			},
		},
		{
			Name:               "adeclick_t_2_0_w_55_o_75",
			ParameterIntent:    "Less sensitive threshold (t=2.0) at the previous production window/overlap.",
			ValidationPriority: "tier1-threshold",
			Params: adeclickBenchmarkParams{
				Enabled:   true,
				Threshold: 2.0,
				Window:    55.0,
				Overlap:   75.0,
			},
		},
		{
			Name:               "adeclick_t_2_0_w_55_o_50",
			ParameterIntent:    "Less sensitive threshold with lower 50% overlap to reduce cost.",
			ValidationPriority: "tier2-overlap",
			Params: adeclickBenchmarkParams{
				Enabled:   true,
				Threshold: 2.0,
				Window:    55.0,
				Overlap:   50.0,
			},
		},
		{
			Name:               "adeclick_t_2_0_w_30_o_50",
			ParameterIntent:    "Less sensitive threshold with smaller 30ms window and lower overlap.",
			ValidationPriority: "tier2-window",
			Params: adeclickBenchmarkParams{
				Enabled:   true,
				Threshold: 2.0,
				Window:    30.0,
				Overlap:   50.0,
			},
		},
	}
}

// adeclickBenchmarkVariantSpec pairs a variant with its built Pass 4 candidate spec.
type adeclickBenchmarkVariantSpec struct {
	Variant adeclickBenchmarkVariant
	Spec    string
}

// buildAdeclickBenchmarkVariantSpec builds the Pass 4 candidate filter graph for a
// single variant. The base chain is the production-derived loudnorm clause and
// limiter prefix taken from the supplied loudnorm setup; only the adeclick segment
// between loudnorm and the metric tail varies between variants.
func buildAdeclickBenchmarkVariantSpec(loudnorm *fullbenchLoudnormSetup, variant adeclickBenchmarkVariant) string {
	if loudnorm == nil || loudnorm.EffectiveConfig == nil || loudnorm.Measurement == nil {
		return ""
	}

	parts := make([]string, 0, 5)
	if loudnorm.Pass3FilterPrefix != "" {
		parts = append(parts, loudnorm.Pass3FilterPrefix)
	}
	parts = append(parts, buildFullbenchLoudnormClause(loudnorm.EffectiveConfig, loudnorm.Measurement))
	if clause := variant.Params.Clause(); clause != "" {
		parts = append(parts, clause)
	}
	if analysis := buildFullbenchPass4OutputAnalysisFilters(loudnorm.EffectiveConfig, loudnorm.Measurement); analysis != "" {
		parts = append(parts, analysis)
	}
	parts = append(parts, buildFullbenchPass4ResampleFilter(loudnorm.EffectiveConfig))

	return strings.Join(parts, ",")
}

// buildAdeclickBenchmarkVariantSpecs builds candidate Pass 4 specs for the entire
// manifest, asserting via tb that each spec is non-empty and contains the shared
// final metric and resample tail.
func buildAdeclickBenchmarkVariantSpecs(tb testing.TB, loudnorm *fullbenchLoudnormSetup) []adeclickBenchmarkVariantSpec {
	tb.Helper()

	if loudnorm == nil || loudnorm.EffectiveConfig == nil || loudnorm.Measurement == nil {
		tb.Fatal("adeclick benchmark variant specs require a loudnorm setup")
	}

	variants := adeclickBenchmarkVariants()
	specs := make([]adeclickBenchmarkVariantSpec, 0, len(variants))
	for _, variant := range variants {
		spec := buildAdeclickBenchmarkVariantSpec(loudnorm, variant)
		if spec == "" {
			tb.Fatalf("adeclick benchmark variant %q produced empty filter spec", variant.Name)
		}
		if !strings.Contains(spec, "loudnorm=") {
			tb.Fatalf("adeclick benchmark variant %q missing loudnorm clause\nSpec: %s", variant.Name, spec)
		}
		specs = append(specs, adeclickBenchmarkVariantSpec{Variant: variant, Spec: spec})
	}

	return specs
}

// findAdeclickBenchmarkVariant returns the named variant from the manifest, or
// fails the test if it is absent.
func findAdeclickBenchmarkVariant(tb testing.TB, name string) adeclickBenchmarkVariant {
	tb.Helper()

	for _, variant := range adeclickBenchmarkVariants() {
		if variant.Name == name {
			return variant
		}
	}
	tb.Fatalf("missing adeclick benchmark variant %q", name)
	return adeclickBenchmarkVariant{}
}

// newAdeclickBenchmarkLoudnormSetup creates a synthetic loudnorm setup suitable
// for spec-shape tests that do not require a real fixture.
func newAdeclickBenchmarkLoudnormSetup(includeLimiterPrefix bool) *fullbenchLoudnormSetup {
	config := newTestConfig()
	config.AdeclickEnabled = true
	config.ResampleEnabled = false
	measurement := &LoudnormMeasurement{
		InputI:       -24.0,
		InputTP:      -6.0,
		InputLRA:     7.0,
		InputThresh:  -34.0,
		TargetOffset: -1.0,
	}

	setup := &fullbenchLoudnormSetup{
		Measurement:     measurement,
		EffectiveConfig: config,
	}
	if includeLimiterPrefix {
		setup.Pass3FilterPrefix = buildPreLimiterPrefix(0, -12.0, true)
		setup.LimiterNeeded = true
		setup.LimiterCeiling = -12.0
	}
	return setup
}

// =============================================================================
// Phase 1 tests
// =============================================================================

func TestAdeclickBenchmarkVariantManifest(t *testing.T) {
	variants := adeclickBenchmarkVariants()
	expectedNames := []string{
		"without_adeclick",
		"adeclick_current_t_2_0_w_55_o_50_m_s",
		"adeclick_t_2_0_w_55_o_75",
		"adeclick_t_2_0_w_55_o_50",
		"adeclick_t_2_0_w_30_o_50",
	}

	if len(variants) != len(expectedNames) {
		t.Fatalf("got %d adeclick benchmark variants, want %d", len(variants), len(expectedNames))
	}

	for i, want := range expectedNames {
		if variants[i].Name != want {
			t.Fatalf("variants[%d].Name = %q, want %q", i, variants[i].Name, want)
		}
		if variants[i].ParameterIntent == "" {
			t.Fatalf("variant %q missing parameter intent", variants[i].Name)
		}
		if variants[i].ValidationPriority == "" {
			t.Fatalf("variant %q missing validation priority", variants[i].Name)
		}
	}

	current := findAdeclickBenchmarkVariant(t, "adeclick_current_t_2_0_w_55_o_50_m_s")
	if !current.Params.Enabled {
		t.Fatal("adeclick_current_t_2_0_w_55_o_50_m_s must enable adeclick")
	}
	if current.Params.Threshold != 2.0 || current.Params.Window != 55.0 || current.Params.Overlap != 50.0 {
		t.Fatalf("adeclick_current_t_2_0_w_55_o_50_m_s params = %+v, want t=2.0 w=55 o=50", current.Params)
	}
	if current.Params.Method != "s" {
		t.Fatalf("adeclick_current_t_2_0_w_55_o_50_m_s method = %q, want %q", current.Params.Method, "s")
	}

	none := findAdeclickBenchmarkVariant(t, "without_adeclick")
	if none.Params.Enabled {
		t.Fatal("without_adeclick must disable adeclick")
	}
	if clause := none.Params.Clause(); clause != "" {
		t.Fatalf("without_adeclick clause = %q, want empty", clause)
	}
}

func TestAdeclickBenchmarkVariantParamClauses(t *testing.T) {
	tests := []struct {
		name       string
		wantClause string
	}{
		{"without_adeclick", ""},
		{"adeclick_current_t_2_0_w_55_o_50_m_s", "adeclick=t=2.0:w=55:o=50:m=s"},
		{"adeclick_t_2_0_w_55_o_75", "adeclick=t=2.0:w=55:o=75"},
		{"adeclick_t_2_0_w_55_o_50", "adeclick=t=2.0:w=55:o=50"},
		{"adeclick_t_2_0_w_30_o_50", "adeclick=t=2.0:w=30:o=50"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			variant := findAdeclickBenchmarkVariant(t, tt.name)
			if got := variant.Params.Clause(); got != tt.wantClause {
				t.Fatalf("clause = %q, want %q", got, tt.wantClause)
			}
		})
	}
}

func TestAdeclickBenchmarkVariantSpecsContainCommonTail(t *testing.T) {
	loudnorm := newAdeclickBenchmarkLoudnormSetup(false)
	specs := buildAdeclickBenchmarkVariantSpecs(t, loudnorm)
	if len(specs) != len(adeclickBenchmarkVariants()) {
		t.Fatalf("got %d specs, want %d", len(specs), len(adeclickBenchmarkVariants()))
	}

	for _, vs := range specs {
		t.Run(vs.Variant.Name, func(t *testing.T) {
			assertFullbenchSpecContains(t, vs.Spec, []string{
				"loudnorm=",
				"astats=",
				"aspectralstats=",
				"ebur128=",
				"aformat=sample_rates=44100",
				"asetnsamples=",
			})
			assertFullbenchSpecOrder(t, vs.Spec, []string{
				"loudnorm=",
				"astats=",
				"aspectralstats=",
				"ebur128=",
				"aformat=sample_rates=44100",
				"asetnsamples=",
			})
		})
	}
}

func TestAdeclickBenchmarkCurrentVariantPlacement(t *testing.T) {
	loudnorm := newAdeclickBenchmarkLoudnormSetup(false)
	current := findAdeclickBenchmarkVariant(t, "adeclick_current_t_2_0_w_55_o_50_m_s")
	spec := buildAdeclickBenchmarkVariantSpec(loudnorm, current)

	if !strings.Contains(spec, "adeclick=t=2.0:w=55:o=50:m=s") {
		t.Fatalf("current variant missing production adeclick clause\nSpec: %s", spec)
	}
	assertFullbenchSpecOrder(t, spec, []string{
		"loudnorm=",
		"adeclick=t=2.0:w=55:o=50:m=s",
		"astats=",
		"aspectralstats=",
		"ebur128=",
		"aformat=sample_rates=44100",
		"asetnsamples=",
	})
}

func TestAdeclickBenchmarkWithoutAdeclickOmitsClause(t *testing.T) {
	loudnorm := newAdeclickBenchmarkLoudnormSetup(false)
	variant := findAdeclickBenchmarkVariant(t, "without_adeclick")
	spec := buildAdeclickBenchmarkVariantSpec(loudnorm, variant)

	assertFullbenchSpecExcludes(t, spec, []string{"adeclick="})
	assertFullbenchSpecContains(t, spec, []string{
		"loudnorm=",
		"astats=",
		"aspectralstats=",
		"ebur128=",
		"aformat=sample_rates=44100",
		"asetnsamples=",
	})
}

func TestAdeclickBenchmarkLimiterPrefixSharedAcrossVariants(t *testing.T) {
	loudnorm := newAdeclickBenchmarkLoudnormSetup(true)
	if loudnorm.Pass3FilterPrefix == "" {
		t.Fatal("synthetic loudnorm setup missing limiter prefix")
	}
	specs := buildAdeclickBenchmarkVariantSpecs(t, loudnorm)

	// Each variant must start with the limiter prefix and end with the loudnorm
	// clause before any adeclick/metric segment.
	for _, vs := range specs {
		t.Run(vs.Variant.Name, func(t *testing.T) {
			if !strings.HasPrefix(vs.Spec, loudnorm.Pass3FilterPrefix+",") {
				t.Fatalf("variant %q missing or misplaced limiter prefix\nSpec: %s", vs.Variant.Name, vs.Spec)
			}
			limiterIdx := strings.Index(vs.Spec, "alimiter=")
			loudnormIdx := strings.Index(vs.Spec, "loudnorm=")
			if limiterIdx == -1 {
				t.Fatalf("variant %q missing alimiter clause\nSpec: %s", vs.Variant.Name, vs.Spec)
			}
			if loudnormIdx == -1 || loudnormIdx <= limiterIdx {
				t.Fatalf("variant %q has alimiter=/loudnorm= out of order\nSpec: %s", vs.Variant.Name, vs.Spec)
			}
		})
	}
}

func TestAdeclickBenchmarkOnlyAdeclickClauseDiffers(t *testing.T) {
	loudnorm := newAdeclickBenchmarkLoudnormSetup(true)
	specs := buildAdeclickBenchmarkVariantSpecs(t, loudnorm)

	stripAdeclick := func(spec string) string {
		clauses := strings.Split(spec, ",")
		filtered := clauses[:0]
		for _, clause := range clauses {
			if strings.HasPrefix(clause, "adeclick=") {
				continue
			}
			filtered = append(filtered, clause)
		}
		return strings.Join(filtered, ",")
	}

	baseline := stripAdeclick(specs[0].Spec)
	for _, vs := range specs[1:] {
		if got := stripAdeclick(vs.Spec); got != baseline {
			t.Fatalf("non-adeclick segments drifted for %q\nbaseline: %s\ncandidate: %s", vs.Variant.Name, baseline, got)
		}
	}
}

func TestAdeclickBenchmarkProductionDefaultsUnchanged(t *testing.T) {
	config := DefaultFilterConfig()

	if !config.AdeclickEnabled {
		t.Fatal("DefaultFilterConfig().AdeclickEnabled = false, want true")
	}
	if config.AdeclickThreshold != 2.0 {
		t.Fatalf("DefaultFilterConfig().AdeclickThreshold = %.2f, want 2.0", config.AdeclickThreshold)
	}
	if config.AdeclickWindow != 55.0 {
		t.Fatalf("DefaultFilterConfig().AdeclickWindow = %.2f, want 55.0", config.AdeclickWindow)
	}
	if config.AdeclickOverlap != 50.0 {
		t.Fatalf("DefaultFilterConfig().AdeclickOverlap = %.2f, want 50.0", config.AdeclickOverlap)
	}
	if config.AdeclickMethod != "s" {
		t.Fatalf("DefaultFilterConfig().AdeclickMethod = %q, want %q", config.AdeclickMethod, "s")
	}
}

// =============================================================================
// Phase 2 - benchmark execution and synthetic coverage
// =============================================================================

// BenchmarkAdeclickVariants benchmarks each Pass 4 candidate filter graph
// against the same Pass 2 seed. Pass 1, adapted Pass 2, and Pass 3 loudnorm
// measurement run once before the variant loop, so the per-variant timing
// measures only the candidate Pass 4 graph execution.
func BenchmarkAdeclickVariants(b *testing.B) {
	inputPath := resolveFullbenchFixture(b)
	seed := setupFullbenchPass4Seed(b, inputPath)
	if seed == nil || seed.Pass2Seed == nil || seed.Loudnorm == nil {
		b.Fatal("adeclick benchmark setup returned incomplete seed")
	}

	variantSpecs := buildAdeclickBenchmarkVariantSpecs(b, seed.Loudnorm)

	if root, ok := resolveAdeclickBenchmarkCaptureRoot(); ok {
		captureAdeclickBenchmarkArtifacts(b, inputPath, seed, variantSpecs, root)
	}

	for _, vs := range variantSpecs {
		b.Run(vs.Variant.Name, func(b *testing.B) {
			outputDir := b.TempDir()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				outputPath := filepath.Join(outputDir, fmt.Sprintf("%s-%d.flac", vs.Variant.Name, i))
				_, outputMeasurements := runFullbenchFilterSpec(b, seed.Pass2Seed.OutputPath, outputPath, vs.Spec, true)
				if outputMeasurements == nil {
					b.Fatalf("expected output measurements for adeclick variant %q", vs.Variant.Name)
				}

				b.StopTimer()
				if err := os.Remove(outputPath); err != nil {
					b.Fatalf("failed to remove adeclick benchmark output: %v", err)
				}
				b.StartTimer()
			}
		})
	}
}

// TestAdeclickBenchmarkSyntheticSmoke exercises a representative subset of the
// matrix against synthetic audio so contributors without the real fixture can
// still validate the candidate Pass 4 graph executes and produces measurements.
func TestAdeclickBenchmarkSyntheticSmoke(t *testing.T) {
	inputPath := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 2.0,
		SampleRate:   44100,
		ToneFreq:     180.0,
		ToneLevel:    -18.0,
		NoiseLevel:   -58.0,
		Dir:          t.TempDir(),
	})
	defer cleanupTestAudio(t, inputPath)

	seed := setupFullbenchPass4Seed(t, inputPath)
	if seed == nil || seed.Pass2Seed == nil || seed.Loudnorm == nil {
		t.Fatal("synthetic Pass 4 seed setup returned incomplete result")
	}

	smokeNames := []string{
		"without_adeclick",
		"adeclick_current_t_2_0_w_55_o_50_m_s",
	}
	for _, name := range smokeNames {
		t.Run(name, func(t *testing.T) {
			variant := findAdeclickBenchmarkVariant(t, name)
			spec := buildAdeclickBenchmarkVariantSpec(seed.Loudnorm, variant)
			if spec == "" {
				t.Fatalf("variant %q produced empty spec", name)
			}

			outputPath := filepath.Join(t.TempDir(), name+".flac")
			result := runFullbenchFilterSpecResult(t, seed.Pass2Seed.OutputPath, outputPath, spec)
			if result.OutputMeasurements == nil {
				t.Fatalf("variant %q produced no output measurements", name)
			}
			if result.OutputMetadata == nil {
				t.Fatalf("variant %q produced no output metadata", name)
			}
			assertFullbenchFLACOutput(t, outputPath)

			// Sanity: ebur128 inside the candidate graph must have populated OutputI.
			if result.OutputMeasurements.OutputI == 0 {
				t.Fatalf("variant %q has zero OutputI; candidate metric chain may be inactive", name)
			}
		})
	}
}

// TestAdeclickBenchmarkExtractMeasurementsRequired guards the contract that
// candidate Pass 4 specs always request output measurements when run via the
// benchmark; the candidate graph terminates in astats/aspectralstats/ebur128
// and the snapshot capture relies on those measurements.
func TestAdeclickBenchmarkExtractMeasurementsRequired(t *testing.T) {
	inputPath := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 1.5,
		SampleRate:   44100,
		ToneFreq:     220.0,
		ToneLevel:    -18.0,
		NoiseLevel:   -58.0,
		Dir:          t.TempDir(),
	})
	defer cleanupTestAudio(t, inputPath)

	seed := setupFullbenchPass4Seed(t, inputPath)
	variant := findAdeclickBenchmarkVariant(t, "adeclick_current_t_2_0_w_55_o_50_m_s")
	spec := buildAdeclickBenchmarkVariantSpec(seed.Loudnorm, variant)

	outputPath := filepath.Join(t.TempDir(), "measured.flac")
	_, measured := runFullbenchFilterSpec(t, seed.Pass2Seed.OutputPath, outputPath, spec, true)
	if measured == nil {
		t.Fatal("candidate Pass 4 run did not produce OutputMeasurements when requested")
	}
}

func TestAdeclickBenchmarkCurrentVariantMatchesProduction(t *testing.T) {
	current := findAdeclickBenchmarkVariant(t, "adeclick_current_t_2_0_w_55_o_50_m_s")
	defaults := DefaultFilterConfig()

	if current.Params.Threshold != defaults.AdeclickThreshold {
		t.Fatalf("variant threshold = %.2f, default = %.2f", current.Params.Threshold, defaults.AdeclickThreshold)
	}
	if current.Params.Window != defaults.AdeclickWindow {
		t.Fatalf("variant window = %.2f, default = %.2f", current.Params.Window, defaults.AdeclickWindow)
	}
	if current.Params.Overlap != defaults.AdeclickOverlap {
		t.Fatalf("variant overlap = %.2f, default = %.2f", current.Params.Overlap, defaults.AdeclickOverlap)
	}
	if current.Params.Method != defaults.AdeclickMethod {
		t.Fatalf("variant method = %q, default = %q", current.Params.Method, defaults.AdeclickMethod)
	}
	wantClause := fmt.Sprintf("adeclick=t=%.1f:w=%.0f:o=%.0f",
		defaults.AdeclickThreshold, defaults.AdeclickWindow, defaults.AdeclickOverlap)
	if defaults.AdeclickMethod != "" {
		wantClause += ":m=" + defaults.AdeclickMethod
	}
	if got := current.Params.Clause(); got != wantClause {
		t.Fatalf("variant clause = %q, want %q", got, wantClause)
	}

	// The benchmark loudnorm clause must remain identical to the production
	// loudnorm clause derived via buildLoudnormFilterSpec().
	productionConfig := *defaults
	productionConfig.AdeclickEnabled = false
	measurement := &LoudnormMeasurement{
		InputI:       -23.5,
		InputTP:      -4.0,
		InputLRA:     6.0,
		InputThresh:  -33.0,
		TargetOffset: -0.5,
	}
	productionClause := extractFullbenchFilterClause(
		buildLoudnormFilterSpec(&productionConfig, measurement, 0, 0, false),
		"loudnorm=",
	)
	benchmarkClause := buildFullbenchLoudnormClause(defaults, measurement)
	if benchmarkClause != productionClause {
		t.Fatalf("benchmark loudnorm clause drifted from production\nbenchmark:  %s\nproduction: %s",
			benchmarkClause, productionClause)
	}
}

// =============================================================================
// Phase 3 - artefact capture
// =============================================================================

// adeclickBenchmarkAudioMetadataSnapshot captures decoded metadata for the
// candidate Pass 4 output FLAC.
type adeclickBenchmarkAudioMetadataSnapshot struct {
	SampleRate    int     `json:"sample_rate"`
	Channels      int     `json:"channels"`
	DurationSecs  float64 `json:"duration_secs"`
	SampleFmt     string  `json:"sample_fmt,omitempty"`
	ChannelLayout string  `json:"channel_layout,omitempty"`
	BitDepth      int     `json:"bit_depth,omitempty"`
}

// adeclickBenchmarkLoudnormSnapshot captures the loudnorm setup that produced
// the candidate Pass 4 chain. These values are shared across variants because
// only the adeclick clause changes between candidates.
type adeclickBenchmarkLoudnormSnapshot struct {
	InputI           float64 `json:"input_i"`
	InputTP          float64 `json:"input_tp"`
	InputLRA         float64 `json:"input_lra"`
	InputThresh      float64 `json:"input_thresh"`
	TargetOffset     float64 `json:"target_offset"`
	RequestedTargetI float64 `json:"requested_target_i"`
	EffectiveTargetI float64 `json:"effective_target_i"`
	LinearModeForced bool    `json:"linear_mode_forced"`
	LimiterEnabled   bool    `json:"limiter_enabled"`
	LimiterCeiling   float64 `json:"limiter_ceiling"`
	PreGainDB        float64 `json:"pre_gain_db"`
	LimiterClamped   bool    `json:"limiter_clamped"`
}

// adeclickBenchmarkEnvironmentSnapshot captures host environment context.
type adeclickBenchmarkEnvironmentSnapshot struct {
	GoVersion                  string `json:"go_version"`
	GOOS                       string `json:"goos"`
	GOARCH                     string `json:"goarch"`
	NumCPU                     int    `json:"num_cpu"`
	CPUModel                   string `json:"cpu_model"`
	BenchmarkFixturePath       string `json:"benchmark_fixture_path"`
	FfmpegStatigoModuleVersion string `json:"ffmpeg_statigo_module_version"`
	FfmpegStatigoModuleReplace string `json:"ffmpeg_statigo_module_replace"`
}

// adeclickBenchmarkMetricsSnapshot is the per-variant capture record persisted
// to metrics.json. The candidate Pass 4 graph terminates in astats /
// aspectralstats / ebur128, so final measurements come directly from the
// candidate run rather than from a separate ApplyNormalisation() call.
type adeclickBenchmarkMetricsSnapshot struct {
	VariantName                 string                                 `json:"variant_name"`
	ParameterIntent             string                                 `json:"parameter_intent"`
	ValidationPriority          string                                 `json:"validation_priority"`
	AdeclickEnabled             bool                                   `json:"adeclick_enabled"`
	AdeclickThreshold           float64                                `json:"adeclick_threshold,omitempty"`
	AdeclickWindow              float64                                `json:"adeclick_window,omitempty"`
	AdeclickOverlap             float64                                `json:"adeclick_overlap,omitempty"`
	AdeclickMethod              string                                 `json:"adeclick_method,omitempty"`
	FilterSpec                  string                                 `json:"filter_spec"`
	Pass4RuntimeMS              float64                                `json:"pass4_runtime_ms"`
	FinalLUFS                   float64                                `json:"final_lufs"`
	FinalTruePeak               float64                                `json:"final_true_peak"`
	FinalLRA                    float64                                `json:"final_lra"`
	FinalNoiseFloor             float64                                `json:"final_noise_floor"`
	FinalSpectralCentroid       float64                                `json:"final_spectral_centroid"`
	FinalSpectralRolloff        float64                                `json:"final_spectral_rolloff"`
	FinalSpeechRMS              float64                                `json:"final_speech_rms"`
	MissingSilenceProfile       bool                                   `json:"missing_silence_profile"`
	MissingSpeechProfile        bool                                   `json:"missing_speech_profile"`
	QualityValidationIncomplete bool                                   `json:"quality_validation_incomplete"`
	InputMetadata               InputMetadata                          `json:"input_metadata"`
	OutputMetadata              adeclickBenchmarkAudioMetadataSnapshot `json:"output_metadata"`
	Environment                 adeclickBenchmarkEnvironmentSnapshot   `json:"environment"`
	FinalMeasurements           *OutputMeasurements                    `json:"final_measurements,omitempty"`
	FinalSilenceSample          *SilenceCandidateMetrics               `json:"final_silence_sample,omitempty"`
	FinalSpeechSample           *SpeechCandidateMetrics                `json:"final_speech_sample,omitempty"`
	Loudnorm                    adeclickBenchmarkLoudnormSnapshot      `json:"loudnorm"`
	Warnings                    []string                               `json:"warnings,omitempty"`
}

// adeclickBenchmarkCaptureResult pairs a variant with its captured snapshot.
type adeclickBenchmarkCaptureResult struct {
	Variant  adeclickBenchmarkVariant
	Snapshot adeclickBenchmarkMetricsSnapshot
}

// resolveAdeclickBenchmarkCaptureRoot returns the directory used to persist
// capture artefacts when JIVETALKING_ADECLICK_BENCH_CAPTURE is truthy.
// Defaults to repo-relative .bench/adeclick; can be overridden by
// JIVETALKING_ADECLICK_BENCH_CAPTURE_ROOT.
func resolveAdeclickBenchmarkCaptureRoot() (string, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(adeclickBenchmarkCaptureEnv))) {
	case "", "0", "false", "no":
		return "", false
	default:
		root := strings.TrimSpace(os.Getenv(adeclickBenchmarkCaptureRootEnv))
		if root == "" {
			root = adeclickBenchmarkCaptureRoot
		}
		return resolveAdeclickBenchmarkRepoPath(root), true
	}
}

func resolveAdeclickBenchmarkRepoPath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	cwd, err := os.Getwd()
	if err != nil {
		return path
	}
	dir := cwd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(dir, path)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Join(cwd, path)
		}
		dir = parent
	}
}

func resolveAdeclickBenchmarkFixtureMetadataPath(inputPath string) string {
	if fixturePath := strings.TrimSpace(os.Getenv(fullbenchFixtureEnv)); fixturePath != "" {
		return adeclickBenchmarkAbsPath(fixturePath)
	}
	return adeclickBenchmarkAbsPath(inputPath)
}

func adeclickBenchmarkAbsPath(path string) string {
	if path == "" {
		return "unavailable"
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return absPath
}

// captureAdeclickBenchmarkArtifacts runs each candidate Pass 4 graph against
// the supplied seed, writes per-variant artefacts under root, and returns the
// captured snapshots. ApplyNormalisation is intentionally not invoked: the
// candidate spec is itself a Pass 4 graph and timing must measure only that
// graph's execution. Final measurements come from the embedded
// astats/aspectralstats/ebur128 metric tail in the candidate spec.
func captureAdeclickBenchmarkArtifacts(
	tb testing.TB,
	inputPath string,
	seed *fullbenchPass4Seed,
	variantSpecs []adeclickBenchmarkVariantSpec,
	root string,
) []adeclickBenchmarkCaptureResult {
	tb.Helper()

	if seed == nil || seed.Pass2Seed == nil || seed.Loudnorm == nil {
		tb.Fatal("adeclick artefact capture requires a full Pass 4 seed")
	}
	if root == "" {
		tb.Fatal("adeclick artefact capture root must not be empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		tb.Fatalf("failed to create adeclick artefact root: %v", err)
	}

	mediaCaptureEnabled := adeclickBenchmarkMediaCaptureEnabled()
	mediaTooling := resolveAdeclickBenchmarkMediaTooling(tb, mediaCaptureEnabled)

	silRegion, spRegion := extractRegionPair(seed.Pass2Seed.InputMeasurements)

	results := make([]adeclickBenchmarkCaptureResult, 0, len(variantSpecs))
	for _, vs := range variantSpecs {
		variantDir := filepath.Join(root, vs.Variant.Name)
		if err := os.MkdirAll(variantDir, 0o755); err != nil {
			tb.Fatalf("failed to create adeclick artefact directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(variantDir, "filter.txt"), []byte(vs.Spec+"\n"), 0o600); err != nil {
			tb.Fatalf("failed to write adeclick filter spec: %v", err)
		}

		outputPath := filepath.Join(variantDir, "processed.flac")
		start := time.Now()
		runResult := runFullbenchFilterSpecResult(tb, seed.Pass2Seed.OutputPath, outputPath, vs.Spec)
		runtimeDur := time.Since(start)
		if runResult.OutputMeasurements == nil {
			tb.Fatalf("adeclick artefact capture for %s produced no final measurements", vs.Variant.Name)
		}

		// Region measurements come from re-opening the candidate output and
		// measuring the same silence/speech regions identified in Pass 1.
		// Skipped when the input lacked detectable profiles.
		var silenceSample *SilenceCandidateMetrics
		var speechSample *SpeechCandidateMetrics
		if silRegion != nil || spRegion != nil {
			silenceSample, speechSample = MeasureOutputRegions(outputPath, silRegion, spRegion)
		}

		snapshot := buildAdeclickBenchmarkMetricsSnapshot(
			vs,
			inputPath,
			seed.Loudnorm,
			runResult,
			silenceSample,
			speechSample,
			runtimeDur,
		)

		writeAdeclickBenchmarkJSON(tb, filepath.Join(variantDir, "metrics.json"), snapshot)
		writeAdeclickBenchmarkTiming(tb, filepath.Join(variantDir, "timing.txt"), snapshot)

		if mediaCaptureEnabled {
			captureAdeclickBenchmarkMediaArtifacts(tb, mediaTooling, outputPath, variantDir)
		}

		results = append(results, adeclickBenchmarkCaptureResult{
			Variant:  vs.Variant,
			Snapshot: snapshot,
		})
	}

	baseline := findAdeclickBenchmarkCaptureResult(results, adeclickBenchmarkBaselineVariant)
	if baseline == nil {
		tb.Fatalf("adeclick artefact capture missing %s baseline", adeclickBenchmarkBaselineVariant)
	}
	for _, result := range results {
		variantDir := filepath.Join(root, result.Variant.Name)
		report := buildAdeclickBenchmarkValidationReport(result, *baseline)
		if err := os.WriteFile(filepath.Join(variantDir, "validation.md"), []byte(report), 0o600); err != nil {
			tb.Fatalf("failed to write adeclick validation report: %v", err)
		}
	}

	return results
}

func buildAdeclickBenchmarkMetricsSnapshot(
	vs adeclickBenchmarkVariantSpec,
	inputPath string,
	loudnorm *fullbenchLoudnormSetup,
	runResult *fullbenchFilterSpecRunResult,
	silenceSample *SilenceCandidateMetrics,
	speechSample *SpeechCandidateMetrics,
	runtimeDur time.Duration,
) adeclickBenchmarkMetricsSnapshot {
	final := runResult.OutputMeasurements

	missingSilence := silenceSample == nil
	missingSpeech := speechSample == nil
	warnings := make([]string, 0, 2)
	if missingSilence {
		warnings = append(warnings, "silence sample missing for candidate Pass 4 output")
	}
	if missingSpeech {
		warnings = append(warnings, "speech sample missing for candidate Pass 4 output")
	}

	finalNoiseFloor := 0.0
	if silenceSample != nil {
		finalNoiseFloor = silenceSample.RMSLevel
	}
	finalSpeechRMS := 0.0
	if speechSample != nil {
		finalSpeechRMS = speechSample.RMSLevel
	}

	loudnormSnapshot := adeclickBenchmarkLoudnormSnapshot{
		InputI:           loudnorm.Measurement.InputI,
		InputTP:          loudnorm.Measurement.InputTP,
		InputLRA:         loudnorm.Measurement.InputLRA,
		InputThresh:      loudnorm.Measurement.InputThresh,
		TargetOffset:     loudnorm.Measurement.TargetOffset,
		RequestedTargetI: NormTargetLUFS,
		EffectiveTargetI: loudnorm.EffectiveConfig.LoudnormTargetI,
		LinearModeForced: loudnorm.LinearModeForced,
		LimiterEnabled:   loudnorm.LimiterNeeded,
		LimiterCeiling:   loudnorm.LimiterCeiling,
		PreGainDB:        loudnorm.PreGainDB,
		LimiterClamped:   loudnorm.LimiterClamped,
	}

	outputMeta := adeclickBenchmarkAudioMetadataSnapshot{}
	if runResult.OutputMetadata != nil {
		outputMeta = adeclickBenchmarkAudioMetadataSnapshot{
			SampleRate:    runResult.OutputMetadata.SampleRate,
			Channels:      runResult.OutputMetadata.Channels,
			DurationSecs:  runResult.OutputMetadata.Duration,
			SampleFmt:     runResult.OutputMetadata.SampleFmt,
			ChannelLayout: runResult.OutputMetadata.ChLayout,
			BitDepth:      runResult.OutputMetadata.BitDepth,
		}
	}

	return adeclickBenchmarkMetricsSnapshot{
		VariantName:                 vs.Variant.Name,
		ParameterIntent:             vs.Variant.ParameterIntent,
		ValidationPriority:          vs.Variant.ValidationPriority,
		AdeclickEnabled:             vs.Variant.Params.Enabled,
		AdeclickThreshold:           vs.Variant.Params.Threshold,
		AdeclickWindow:              vs.Variant.Params.Window,
		AdeclickOverlap:             vs.Variant.Params.Overlap,
		AdeclickMethod:              vs.Variant.Params.Method,
		FilterSpec:                  vs.Spec,
		Pass4RuntimeMS:              float64(runtimeDur.Microseconds()) / 1000.0,
		FinalLUFS:                   final.OutputI,
		FinalTruePeak:               final.OutputTP,
		FinalLRA:                    final.OutputLRA,
		FinalNoiseFloor:             finalNoiseFloor,
		FinalSpectralCentroid:       final.SpectralCentroid,
		FinalSpectralRolloff:        final.SpectralRolloff,
		FinalSpeechRMS:              finalSpeechRMS,
		MissingSilenceProfile:       missingSilence,
		MissingSpeechProfile:        missingSpeech,
		QualityValidationIncomplete: missingSilence || missingSpeech,
		InputMetadata:               runResult.InputMetadata,
		OutputMetadata:              outputMeta,
		Environment:                 buildAdeclickBenchmarkEnvironmentSnapshot(inputPath),
		FinalMeasurements:           final,
		FinalSilenceSample:          silenceSample,
		FinalSpeechSample:           speechSample,
		Loudnorm:                    loudnormSnapshot,
		Warnings:                    warnings,
	}
}

func buildAdeclickBenchmarkEnvironmentSnapshot(inputPath string) adeclickBenchmarkEnvironmentSnapshot {
	version, replace := ffmpegStatigoModuleInfo()
	return adeclickBenchmarkEnvironmentSnapshot{
		GoVersion:                  runtime.Version(),
		GOOS:                       runtime.GOOS,
		GOARCH:                     runtime.GOARCH,
		NumCPU:                     runtime.NumCPU(),
		CPUModel:                   detectAdeclickBenchmarkCPUModel(),
		BenchmarkFixturePath:       resolveAdeclickBenchmarkFixtureMetadataPath(inputPath),
		FfmpegStatigoModuleVersion: version,
		FfmpegStatigoModuleReplace: replace,
	}
}

func detectAdeclickBenchmarkCPUModel() string {
	switch runtime.GOOS {
	case "linux":
		return detectAdeclickBenchmarkLinuxCPUModel()
	case "darwin":
		return detectAdeclickBenchmarkDarwinCPUModel()
	default:
		return "unavailable"
	}
}

func detectAdeclickBenchmarkLinuxCPUModel() string {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return "unavailable"
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.TrimSpace(key) {
		case "model name", "Hardware", "Processor":
			if model := strings.TrimSpace(value); model != "" {
				return model
			}
		}
	}
	return "unavailable"
}

func detectAdeclickBenchmarkDarwinCPUModel() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, key := range []string{"machdep.cpu.brand_string", "hw.model"} {
		output, err := exec.CommandContext(ctx, "sysctl", "-n", key).Output() // #nosec G204 -- static sysctl key allowlist for local benchmark metadata.
		if err != nil {
			continue
		}
		if model := strings.TrimSpace(string(output)); model != "" {
			return model
		}
	}
	return "unavailable"
}

func writeAdeclickBenchmarkJSON(tb testing.TB, path string, snapshot adeclickBenchmarkMetricsSnapshot) {
	tb.Helper()
	data, err := json.MarshalIndent(sanitizeAdeclickBenchmarkJSONValue(reflect.ValueOf(snapshot)), "", "  ")
	if err != nil {
		tb.Fatalf("failed to encode adeclick metrics snapshot: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		tb.Fatalf("failed to write adeclick metrics snapshot: %v", err)
	}
}

// sanitizeAdeclickBenchmarkJSONValue walks a value tree and replaces NaN/Inf
// floats with nil so the JSON output remains spec-compliant. Mirrors the
// behaviour of the equivalent ANLMDN helper but is kept local to this file
// for low blast radius.
func sanitizeAdeclickBenchmarkJSONValue(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return sanitizeAdeclickBenchmarkJSONValue(value.Elem())
	case reflect.Struct:
		result := make(map[string]any, value.NumField())
		valueType := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := valueType.Field(i)
			if field.PkgPath != "" {
				continue
			}
			if field.Anonymous && field.Tag.Get("json") == "" {
				if nested, ok := sanitizeAdeclickBenchmarkJSONValue(value.Field(i)).(map[string]any); ok {
					maps.Copy(result, nested)
				}
				continue
			}
			name, omitEmpty, ok := adeclickBenchmarkJSONFieldName(field)
			if !ok {
				continue
			}
			fieldValue := value.Field(i)
			if omitEmpty && fieldValue.IsZero() {
				continue
			}
			result[name] = sanitizeAdeclickBenchmarkJSONValue(fieldValue)
		}
		return result
	case reflect.Slice, reflect.Array:
		result := make([]any, value.Len())
		for i := 0; i < value.Len(); i++ {
			result[i] = sanitizeAdeclickBenchmarkJSONValue(value.Index(i))
		}
		return result
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return value.Interface()
		}
		result := make(map[string]any, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			result[iter.Key().String()] = sanitizeAdeclickBenchmarkJSONValue(iter.Value())
		}
		return result
	case reflect.Float32, reflect.Float64:
		floatValue := value.Float()
		if math.IsInf(floatValue, 0) || math.IsNaN(floatValue) {
			return nil
		}
		return floatValue
	default:
		return value.Interface()
	}
}

func adeclickBenchmarkJSONFieldName(field reflect.StructField) (string, bool, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, false
	}
	if tag == "" {
		return field.Name, false, true
	}
	name, options, _ := strings.Cut(tag, ",")
	if name == "" {
		name = field.Name
	}
	return name, strings.Contains(options, "omitempty"), true
}

func writeAdeclickBenchmarkTiming(tb testing.TB, path string, snapshot adeclickBenchmarkMetricsSnapshot) {
	tb.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "variant: %s\n", snapshot.VariantName)
	fmt.Fprintf(&b, "pass4_runtime_ms: %.3f\n", snapshot.Pass4RuntimeMS)
	fmt.Fprintf(&b, "final_lufs: %.2f\n", snapshot.FinalLUFS)
	fmt.Fprintf(&b, "final_true_peak: %.2f\n", snapshot.FinalTruePeak)
	fmt.Fprintf(&b, "final_lra: %.2f\n", snapshot.FinalLRA)
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		tb.Fatalf("failed to write adeclick timing capture: %v", err)
	}
}

func findAdeclickBenchmarkCaptureResult(results []adeclickBenchmarkCaptureResult, name string) *adeclickBenchmarkCaptureResult {
	for i := range results {
		if results[i].Variant.Name == name {
			return &results[i]
		}
	}
	return nil
}

// buildAdeclickBenchmarkValidationReport renders an objective comparison of a
// candidate against the production baseline. It must not recommend a
// production setting; it lists deltas only.
func buildAdeclickBenchmarkValidationReport(result, baseline adeclickBenchmarkCaptureResult) string {
	snapshot := result.Snapshot
	base := baseline.Snapshot

	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", snapshot.VariantName)
	fmt.Fprintf(&b, "Intent: %s\n\n", snapshot.ParameterIntent)
	fmt.Fprintf(&b, "Validation priority: %s\n\n", snapshot.ValidationPriority)

	b.WriteString("## Speed\n\n")
	b.WriteString("| Metric | Candidate | " + adeclickBenchmarkBaselineVariant + " | Delta |\n")
	b.WriteString("|--------|-----------|-----------------------------------|-------|\n")
	writeAdeclickBenchmarkMetricRow(&b, "Pass 4 runtime ms", snapshot.Pass4RuntimeMS, base.Pass4RuntimeMS, "%.3f")

	b.WriteString("\n## Objective Metrics\n\n")
	b.WriteString("| Metric | Candidate | " + adeclickBenchmarkBaselineVariant + " | Delta |\n")
	b.WriteString("|--------|-----------|-----------------------------------|-------|\n")
	writeAdeclickBenchmarkMetricRow(&b, "Final LUFS", snapshot.FinalLUFS, base.FinalLUFS, "%.2f")
	writeAdeclickBenchmarkMetricRow(&b, "True peak dBTP", snapshot.FinalTruePeak, base.FinalTruePeak, "%.2f")
	writeAdeclickBenchmarkMetricRow(&b, "LRA LU", snapshot.FinalLRA, base.FinalLRA, "%.2f")
	writeAdeclickBenchmarkMetricRow(&b, "Noise floor dBFS", snapshot.FinalNoiseFloor, base.FinalNoiseFloor, "%.2f")
	writeAdeclickBenchmarkMetricRow(&b, "Spectral centroid Hz", snapshot.FinalSpectralCentroid, base.FinalSpectralCentroid, "%.1f")
	writeAdeclickBenchmarkMetricRow(&b, "Spectral rolloff Hz", snapshot.FinalSpectralRolloff, base.FinalSpectralRolloff, "%.1f")
	writeAdeclickBenchmarkMetricRow(&b, "Speech RMS dBFS", snapshot.FinalSpeechRMS, base.FinalSpeechRMS, "%.2f")

	b.WriteString("\n## Missing Profiles\n\n")
	fmt.Fprintf(&b, "- Missing silence sample: %t\n", snapshot.MissingSilenceProfile)
	fmt.Fprintf(&b, "- Missing speech sample: %t\n", snapshot.MissingSpeechProfile)
	fmt.Fprintf(&b, "- Quality validation incomplete: %t\n", snapshot.QualityValidationIncomplete)
	if len(snapshot.Warnings) > 0 {
		b.WriteString("\n## Warnings\n\n")
		for _, warning := range snapshot.Warnings {
			fmt.Fprintf(&b, "- %s\n", warning)
		}
	}

	b.WriteString("\n## Listening Checklist\n\n")
	b.WriteString("- Click and pop repair across hard transients\n")
	b.WriteString("- Sibilant integrity (no smearing of /s/ /sh/)\n")
	b.WriteString("- Plosive transients (no softening of /p/ /b/ /t/)\n")
	b.WriteString("- Quiet-passage stability (no ringing or warble)\n")
	b.WriteString("- Background noise character (no pumping near clicks)\n")

	return b.String()
}

func writeAdeclickBenchmarkMetricRow(b *strings.Builder, name string, value, baseline float64, format string) {
	valueFormat := format + " | "
	deltaFormat := "%+" + strings.TrimPrefix(format, "%") + " |\n"
	fmt.Fprintf(b, "| %s | "+valueFormat+valueFormat+deltaFormat, name, value, baseline, value-baseline)
}

// =============================================================================
// Phase 3.5 - optional listening / spectrogram capture
// =============================================================================

type adeclickBenchmarkMediaTooling struct {
	Enabled      bool
	FfmpegPath   string
	WarnNoFfmpeg bool
	ExcerptSecs  float64
}

func adeclickBenchmarkMediaCaptureEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(adeclickBenchmarkMediaCaptureEnv))) {
	case "", "0", "false", "no":
		return false
	default:
		return true
	}
}

func resolveAdeclickBenchmarkMediaTooling(tb testing.TB, enabled bool) adeclickBenchmarkMediaTooling {
	tb.Helper()
	if !enabled {
		return adeclickBenchmarkMediaTooling{}
	}

	tooling := adeclickBenchmarkMediaTooling{
		Enabled:     true,
		ExcerptSecs: adeclickBenchmarkExcerptSecs,
	}

	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		tooling.WarnNoFfmpeg = true
		tb.Logf("adeclick benchmark media capture: ffmpeg binary not found on PATH; skipping excerpt and spectrogram artefacts")
		return tooling
	}
	tooling.FfmpegPath = path
	return tooling
}

func captureAdeclickBenchmarkMediaArtifacts(tb testing.TB, tooling adeclickBenchmarkMediaTooling, processedPath, variantDir string) {
	tb.Helper()
	if !tooling.Enabled || tooling.FfmpegPath == "" {
		// Tooling absent - already logged at resolve time.
		return
	}

	excerptPath := filepath.Join(variantDir, "excerpt.flac")
	excerptDurArg := fmt.Sprintf("%.3f", tooling.ExcerptSecs)
	excerptCtx, excerptCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer excerptCancel()
	excerptCmd := exec.CommandContext(excerptCtx, tooling.FfmpegPath, // #nosec G204 -- ffmpeg path resolved via LookPath; arguments are static.
		"-y",
		"-i", processedPath,
		"-t", excerptDurArg,
		"-c:a", "flac",
		excerptPath,
	)
	if output, err := excerptCmd.CombinedOutput(); err != nil {
		_ = os.Remove(excerptPath)
		tb.Logf("adeclick benchmark media capture: failed to render excerpt for %s: %v\n%s", processedPath, err, output)
		return
	}

	spectrogramPath := filepath.Join(variantDir, "spectrogram.png")
	spectrogramCtx, spectrogramCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer spectrogramCancel()
	spectrogramCmd := exec.CommandContext(spectrogramCtx, tooling.FfmpegPath, // #nosec G204 -- ffmpeg path resolved via LookPath; arguments are static.
		"-y",
		"-i", excerptPath,
		"-lavfi", "showspectrumpic=s=1024x512:legend=disabled",
		spectrogramPath,
	)
	if output, err := spectrogramCmd.CombinedOutput(); err != nil {
		_ = os.Remove(spectrogramPath)
		tb.Logf("adeclick benchmark media capture: failed to render spectrogram for %s: %v\n%s", processedPath, err, output)
	}
}

// =============================================================================
// Phase 3 tests
// =============================================================================

func TestAdeclickBenchmarkCaptureRootOptIn(t *testing.T) {
	t.Setenv(adeclickBenchmarkCaptureEnv, "")
	if root, ok := resolveAdeclickBenchmarkCaptureRoot(); ok || root != "" {
		t.Fatalf("capture root enabled without %s: root=%q ok=%v", adeclickBenchmarkCaptureEnv, root, ok)
	}

	t.Setenv(adeclickBenchmarkCaptureEnv, "1")
	root, ok := resolveAdeclickBenchmarkCaptureRoot()
	if !ok {
		t.Fatalf("expected capture root when %s is set", adeclickBenchmarkCaptureEnv)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("capture root = %q, want absolute path", root)
	}
	if !strings.HasSuffix(filepath.ToSlash(root), adeclickBenchmarkCaptureRoot) {
		t.Fatalf("capture root = %q, want suffix %q", root, adeclickBenchmarkCaptureRoot)
	}

	overrideRoot := filepath.Join(t.TempDir(), "capture-root")
	t.Setenv(adeclickBenchmarkCaptureRootEnv, overrideRoot)
	root, ok = resolveAdeclickBenchmarkCaptureRoot()
	if !ok {
		t.Fatalf("expected capture root when %s is set", adeclickBenchmarkCaptureEnv)
	}
	if root != overrideRoot {
		t.Fatalf("capture root override = %q, want %q", root, overrideRoot)
	}

	t.Setenv(adeclickBenchmarkCaptureEnv, "false")
	if root, ok := resolveAdeclickBenchmarkCaptureRoot(); ok || root != "" {
		t.Fatalf("capture root enabled for false value: root=%q ok=%v", root, ok)
	}
}

func TestAdeclickBenchmarkJSONSanitisesNonFinite(t *testing.T) {
	snapshot := adeclickBenchmarkMetricsSnapshot{
		VariantName: "test",
		FilterSpec:  "loudnorm=,asetnsamples=n=4096",
		FinalLUFS:   math.NaN(),
		FinalLRA:    math.Inf(1),
	}

	path := filepath.Join(t.TempDir(), "metrics.json")
	writeAdeclickBenchmarkJSON(t, path, snapshot)

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read sanitised metrics: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("sanitised metrics did not decode as JSON: %v\n%s", err, raw)
	}
	if decoded["final_lufs"] != nil {
		t.Fatalf("expected final_lufs to be nil after NaN sanitisation, got %v", decoded["final_lufs"])
	}
	if decoded["final_lra"] != nil {
		t.Fatalf("expected final_lra to be nil after Inf sanitisation, got %v", decoded["final_lra"])
	}
	if decoded["variant_name"] != "test" {
		t.Fatalf("variant_name lost during sanitisation: %v", decoded["variant_name"])
	}
}

func TestAdeclickBenchmarkTimingFile(t *testing.T) {
	snapshot := adeclickBenchmarkMetricsSnapshot{
		VariantName:    "adeclick_current_t_2_0_w_55_o_50_m_s",
		Pass4RuntimeMS: 12.345,
		FinalLUFS:      -16.05,
		FinalTruePeak:  -1.2,
		FinalLRA:       7.5,
	}
	path := filepath.Join(t.TempDir(), "timing.txt")
	writeAdeclickBenchmarkTiming(t, path, snapshot)

	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read timing capture: %v", err)
	}
	body := string(bytes)
	for _, want := range []string{
		"variant: adeclick_current_t_2_0_w_55_o_50_m_s",
		"pass4_runtime_ms: 12.345",
		"final_lufs: -16.05",
		"final_true_peak: -1.20",
		"final_lra: 7.50",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("timing file missing %q\n%s", want, body)
		}
	}
}

func TestAdeclickBenchmarkArtifactCaptureSyntheticSmoke(t *testing.T) {
	inputPath := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 4.0,
		SampleRate:   44100,
		ToneFreq:     180.0,
		ToneLevel:    -20.0,
		NoiseLevel:   -58.0,
		Dir:          t.TempDir(),
		SilenceGap: struct {
			Start    float64
			Duration float64
		}{
			Start:    1.25,
			Duration: 0.75,
		},
	})
	defer cleanupTestAudio(t, inputPath)

	seed := setupFullbenchPass4Seed(t, inputPath)
	if seed.Pass2Seed == nil || seed.Loudnorm == nil {
		t.Fatal("synthetic Pass 4 seed setup returned incomplete result")
	}
	specs := buildAdeclickBenchmarkVariantSpecs(t, seed.Loudnorm)

	// Trim to two representative variants to keep the smoke test fast.
	subset := []adeclickBenchmarkVariantSpec{}
	for _, vs := range specs {
		switch vs.Variant.Name {
		case "without_adeclick", adeclickBenchmarkBaselineVariant:
			subset = append(subset, vs)
		}
	}
	if len(subset) != 2 {
		t.Fatalf("synthetic smoke expected 2 variants, got %d", len(subset))
	}

	root := filepath.Join(t.TempDir(), "adeclick")
	results := captureAdeclickBenchmarkArtifacts(t, inputPath, seed, subset, root)
	if len(results) != len(subset) {
		t.Fatalf("capture result count = %d, want %d", len(results), len(subset))
	}

	for _, vs := range subset {
		variantDir := filepath.Join(root, vs.Variant.Name)
		for _, name := range []string{"processed.flac", "filter.txt", "metrics.json", "validation.md", "timing.txt"} {
			path := filepath.Join(variantDir, name)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("expected artefact %s: %v", path, err)
			}
		}
		assertFullbenchFLACOutput(t, filepath.Join(variantDir, "processed.flac"))

		filterBytes, err := os.ReadFile(filepath.Join(variantDir, "filter.txt"))
		if err != nil {
			t.Fatalf("failed to read filter artefact: %v", err)
		}
		if !strings.Contains(string(filterBytes), "loudnorm=") {
			t.Fatalf("filter artefact missing loudnorm clause: %s", filterBytes)
		}
		if vs.Variant.Params.Enabled && !strings.Contains(string(filterBytes), "adeclick=") {
			t.Fatalf("filter artefact missing adeclick clause for %s: %s", vs.Variant.Name, filterBytes)
		}

		metricsBytes, err := os.ReadFile(filepath.Join(variantDir, "metrics.json"))
		if err != nil {
			t.Fatalf("failed to read metrics artefact: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(metricsBytes, &decoded); err != nil {
			t.Fatalf("failed to decode metrics artefact: %v", err)
		}
		for _, key := range []string{
			"variant_name",
			"filter_spec",
			"pass4_runtime_ms",
			"final_lufs",
			"final_true_peak",
			"final_lra",
			"final_spectral_centroid",
			"final_spectral_rolloff",
			"output_metadata",
			"loudnorm",
			"environment",
		} {
			if _, ok := decoded[key]; !ok {
				t.Fatalf("metrics.json missing key %q for %s", key, vs.Variant.Name)
			}
		}
		if got, ok := decoded["variant_name"].(string); !ok || got != vs.Variant.Name {
			t.Fatalf("metrics.json variant_name = %v, want %s", decoded["variant_name"], vs.Variant.Name)
		}
		if runtimeMS, ok := decoded["pass4_runtime_ms"].(float64); !ok || runtimeMS <= 0 {
			t.Fatalf("metrics.json pass4_runtime_ms = %v, want positive number", decoded["pass4_runtime_ms"])
		}

		reportBytes, err := os.ReadFile(filepath.Join(variantDir, "validation.md"))
		if err != nil {
			t.Fatalf("failed to read validation report: %v", err)
		}
		report := string(reportBytes)
		for _, want := range []string{
			"Pass 4 runtime ms",
			"Final LUFS",
			"True peak dBTP",
			"LRA LU",
			"Noise floor dBFS",
			"Spectral centroid Hz",
			"Spectral rolloff Hz",
			"Speech RMS dBFS",
			"Click and pop repair",
		} {
			if !strings.Contains(report, want) {
				t.Fatalf("validation report missing %q for %s:\n%s", want, vs.Variant.Name, report)
			}
		}
	}
}

func TestAdeclickBenchmarkMediaCaptureSkipsWithoutFfmpeg(t *testing.T) {
	t.Setenv(adeclickBenchmarkMediaCaptureEnv, "1")
	t.Setenv("PATH", t.TempDir()) // empty PATH -> ffmpeg cannot be found

	tooling := resolveAdeclickBenchmarkMediaTooling(t, true)
	if !tooling.Enabled {
		t.Fatal("expected media tooling to be enabled when env var is set")
	}
	if tooling.FfmpegPath != "" {
		t.Fatalf("expected empty FfmpegPath when ffmpeg is missing, got %q", tooling.FfmpegPath)
	}
	if !tooling.WarnNoFfmpeg {
		t.Fatal("expected WarnNoFfmpeg flag when ffmpeg is missing")
	}

	// Capture must not write artefacts when tooling unavailable.
	variantDir := t.TempDir()
	captureAdeclickBenchmarkMediaArtifacts(t, tooling, filepath.Join(variantDir, "missing.flac"), variantDir)
	if _, err := os.Stat(filepath.Join(variantDir, "excerpt.flac")); err == nil {
		t.Fatal("excerpt.flac written when ffmpeg unavailable")
	}
	if _, err := os.Stat(filepath.Join(variantDir, "spectrogram.png")); err == nil {
		t.Fatal("spectrogram.png written when ffmpeg unavailable")
	}
}

// audioMetadataAlias is unused locally but kept as a guard against drift in
// the imported audio package; if its symbols change, the imports above flag
// the regression.
var _ = audio.Metadata{}
