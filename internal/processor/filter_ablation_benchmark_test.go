package processor

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

const fullbenchFixtureEnv = "JIVETALKING_BENCH_FIXTURE"

type fullbenchAdaptedSetup struct {
	Config       *EffectiveFilterConfig
	Measurements *AudioMeasurements
}

type fullbenchPass2Seed struct {
	OutputPath         string
	Config             *EffectiveFilterConfig
	InputMeasurements  *AudioMeasurements
	OutputMeasurements *OutputMeasurements
	InputMetadata      InputMetadata
}

type fullbenchLoudnormSetup struct {
	Measurement       *LoudnormMeasurement
	EffectiveConfig   *EffectiveFilterConfig
	Pass3FilterPrefix string
	PreGainDB         float64
	LimiterCeiling    float64
	LimiterNeeded     bool
	LimiterClamped    bool
	LinearModeForced  bool
}

type fullbenchPass4Seed struct {
	Pass2Seed *fullbenchPass2Seed
	Loudnorm  *fullbenchLoudnormSetup
}

type fullbenchFilterSpecRunResult struct {
	InputMetadata      InputMetadata
	OutputMetadata     *audio.Metadata
	OutputMeasurements *OutputMeasurements
}

type fullbenchPass2AblationVariant struct {
	Name                string
	ExtractMeasurements bool
	BuildSpec           func(*EffectiveFilterConfig) string
}

type fullbenchPass4AblationVariant struct {
	Name                string
	RequiresLimiter     bool
	ExtractMeasurements bool
	BuildSpec           func(*fullbenchLoudnormSetup) string
}

func newFullbenchEffectiveTestConfig() *EffectiveFilterConfig {
	return deriveEffectiveFilterConfig(newTestBaseConfig())
}

func resolveFullbenchFixture(tb testing.TB) string {
	tb.Helper()

	fixturePath, ok := resolveFullbenchFixtureFromEnv(tb)
	if !ok {
		tb.Skipf("set %s to benchmark fullbench ablations with a real local fixture", fullbenchFixtureEnv)
	}

	return fixturePath
}

func resolveFullbenchFixtureFromEnv(tb testing.TB) (string, bool) {
	tb.Helper()

	fixturePath := os.Getenv(fullbenchFixtureEnv)
	if fixturePath == "" {
		return "", false
	}

	if _, err := os.Stat(fixturePath); err != nil { // #nosec G703 -- local benchmark fixture path is explicitly supplied by JIVETALKING_BENCH_FIXTURE.
		if os.IsNotExist(err) {
			tb.Skipf("%s is set but benchmark fixture is absent: %s", fullbenchFixtureEnv, fixturePath)
		}
		tb.Fatalf("%s is set but cannot be accessed: %v", fullbenchFixtureEnv, err)
	}
	return copyBenchmarkFixture(tb, fixturePath, tb.TempDir()), true
}

func TestResolveFullbenchFixtureFromEnvSkipsWhenUnset(t *testing.T) {
	t.Setenv(fullbenchFixtureEnv, "")

	fixturePath, ok := resolveFullbenchFixtureFromEnv(t)
	if ok {
		t.Fatalf("expected no fixture without %s, got %q", fullbenchFixtureEnv, fixturePath)
	}
	if fixturePath != "" {
		t.Fatalf("expected empty fixture path without %s, got %q", fullbenchFixtureEnv, fixturePath)
	}
}

func TestResolveFullbenchFixtureFromEnvCopiesEnvironmentFixture(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.flac")
	sourceBytes := []byte("fixture")
	if err := os.WriteFile(sourcePath, sourceBytes, 0o600); err != nil {
		t.Fatalf("failed to write source fixture: %v", err)
	}
	t.Setenv(fullbenchFixtureEnv, sourcePath)

	copiedPath, ok := resolveFullbenchFixtureFromEnv(t)
	if !ok {
		t.Fatalf("expected fixture when %s is set", fullbenchFixtureEnv)
	}
	if copiedPath == sourcePath {
		t.Fatal("resolver returned source path instead of a copy")
	}
	if filepath.Base(copiedPath) != filepath.Base(sourcePath) {
		t.Fatalf("copy basename mismatch: got %q, want %q", filepath.Base(copiedPath), filepath.Base(sourcePath))
	}

	copiedBytes, err := os.ReadFile(copiedPath)
	if err != nil {
		t.Fatalf("failed to read copied fixture: %v", err)
	}
	if string(copiedBytes) != string(sourceBytes) {
		t.Fatalf("copied fixture content mismatch: got %q, want %q", copiedBytes, sourceBytes)
	}

	afterBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("failed to reread source fixture: %v", err)
	}
	if string(afterBytes) != string(sourceBytes) {
		t.Fatal("source fixture was modified")
	}
}

func runFullbenchFilterSpec(tb testing.TB, inputPath, outputPath, filterSpec string, extractMeasurements bool) (InputMetadata, *OutputMeasurements) {
	tb.Helper()

	result := runFullbenchFilterSpecCore(tb, inputPath, outputPath, filterSpec, extractMeasurements, false)
	return result.InputMetadata, result.OutputMeasurements
}

func runFullbenchFilterSpecResult(tb testing.TB, inputPath, outputPath, filterSpec string) *fullbenchFilterSpecRunResult {
	tb.Helper()

	return runFullbenchFilterSpecCore(tb, inputPath, outputPath, filterSpec, true, true)
}

func runFullbenchFilterSpecCore(tb testing.TB, inputPath, outputPath, filterSpec string, extractMeasurements, includeOutputMetadata bool) *fullbenchFilterSpecRunResult {
	tb.Helper()

	if filterSpec == "" {
		tb.Fatal("fullbench filter spec must not be empty")
	}

	reader, metadata, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		tb.Fatalf("failed to open input file: %v", err)
	}
	defer reader.Close()

	inputMetadata := InputMetadata{
		SampleRate:   metadata.SampleRate,
		Channels:     metadata.Channels,
		DurationSecs: metadata.Duration,
	}

	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(reader.GetDecoderContext(), filterSpec)
	if err != nil {
		tb.Fatalf("failed to create fullbench filter graph: %v", err)
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	encoder, err := createOutputEncoder(outputPath, metadata, bufferSinkCtx)
	if err != nil {
		tb.Fatalf("failed to create fullbench output encoder: %v", err)
	}
	encoderClosed := false
	defer func() {
		if !encoderClosed {
			_ = encoder.Close()
		}
	}()

	var outputAcc *outputMetadataAccumulators
	if extractMeasurements {
		outputAcc = &outputMetadataAccumulators{}
	}

	if err := runFilterGraph(context.Background(), reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnReadError: func(err error) error {
			return fmt.Errorf("failed to read frame: %w", err)
		},
		OnPushError: func(err error) error {
			return fmt.Errorf("failed to push frame to filter: %w", err)
		},
		OnPullError: func(err error) error {
			return fmt.Errorf("failed to pull frame from filter: %w", err)
		},
		OnFrame: func(inputFrame, filteredFrame *ffmpeg.AVFrame) error {
			if outputAcc != nil {
				extractOutputFrameMetadata(filteredFrame.Metadata(), outputAcc)
			}

			filteredFrame.SetTimeBase(ffmpeg.AVBuffersinkGetTimeBase(bufferSinkCtx))
			if err := encoder.WriteFrame(filteredFrame); err != nil {
				return fmt.Errorf("failed to write frame: %w", err)
			}

			return nil
		},
	}); err != nil {
		tb.Fatalf("fullbench filter graph failed: %v", err)
	}

	if err := encoder.Flush(); err != nil {
		tb.Fatalf("failed to flush fullbench output encoder: %v", err)
	}
	if err := encoder.Close(); err != nil {
		tb.Fatalf("failed to close fullbench output encoder: %v", err)
	}
	encoderClosed = true

	result := &fullbenchFilterSpecRunResult{
		InputMetadata: inputMetadata,
	}
	if includeOutputMetadata {
		result.OutputMetadata = readFullbenchOutputMetadata(tb, outputPath)
	}
	if outputAcc != nil {
		result.OutputMeasurements = finalizeOutputMeasurements(outputAcc)
	}

	return result
}

func fullbenchPass2AblationVariants() []fullbenchPass2AblationVariant {
	return []fullbenchPass2AblationVariant{
		{
			Name:                "full_chain",
			ExtractMeasurements: true,
			BuildSpec: func(config *EffectiveFilterConfig) string {
				return config.BuildFilterSpec()
			},
		},
		{
			Name:                "without_output_analysis",
			ExtractMeasurements: false,
			BuildSpec: func(config *EffectiveFilterConfig) string {
				ablated := *config
				ablated.Analysis.Enabled = false
				return ablated.BuildFilterSpec()
			},
		},
		{
			Name:                "without_anlmdn",
			ExtractMeasurements: true,
			BuildSpec:           buildFullbenchPass2WithoutAnlmdnSpec,
		},
		{
			Name:                "without_afftdn",
			ExtractMeasurements: true,
			BuildSpec: func(config *EffectiveFilterConfig) string {
				ablated := *config
				ablated.NoiseReduction.AfftdnEnabled = false
				return ablated.BuildFilterSpec()
			},
		},
		{
			Name:                "without_gate_compressor",
			ExtractMeasurements: true,
			BuildSpec: func(config *EffectiveFilterConfig) string {
				ablated := *config
				ablated.SpeechGate.Enabled = false
				ablated.LevellingCompressor.Enabled = false
				return ablated.BuildFilterSpec()
			},
		},
	}
}

func buildFullbenchPass2WithoutAnlmdnSpec(config *EffectiveFilterConfig) string {
	ablated := *config
	order := ablated.FilterOrder
	if len(order) == 0 {
		order = Pass2FilterOrder
	}

	filters := make([]string, 0, len(order))
	for _, id := range order {
		if id == FilterNoiseReduction {
			// Drop anlmdn; keep only the afftdn tail of the noise block.
			if ablated.NoiseReduction.Enabled {
				if spec := ablated.NoiseReduction.buildAfftdnFilter(); spec != "" {
					filters = append(filters, spec)
				}
			}
			continue
		}

		builder, ok := filterBuilders[id]
		if !ok {
			continue
		}
		if spec := builder(&ablated); spec != "" {
			filters = append(filters, spec)
		}
	}

	return strings.Join(filters, ",")
}

func fullbenchPass4AblationVariants() []fullbenchPass4AblationVariant {
	return []fullbenchPass4AblationVariant{
		{
			Name: "loudnorm_only",
			BuildSpec: func(loudnorm *fullbenchLoudnormSetup) string {
				return buildFullbenchPass4AblationSpec(loudnorm, false, false)
			},
		},
		{
			Name:            "loudnorm_plus_limiter",
			RequiresLimiter: true,
			BuildSpec: func(loudnorm *fullbenchLoudnormSetup) string {
				return buildFullbenchPass4AblationSpec(loudnorm, true, false)
			},
		},
		{
			Name:                "loudnorm_plus_output_analysis",
			ExtractMeasurements: true,
			BuildSpec: func(loudnorm *fullbenchLoudnormSetup) string {
				return buildFullbenchPass4AblationSpec(loudnorm, false, true)
			},
		},
	}
}

func buildFullbenchPass4AblationSpec(loudnorm *fullbenchLoudnormSetup, includeLimiter, includeOutputAnalysis bool) string {
	if loudnorm == nil || loudnorm.EffectiveConfig == nil || loudnorm.Measurement == nil {
		return ""
	}

	filters := make([]string, 0, 5)
	if includeLimiter && loudnorm.Pass3FilterPrefix != "" {
		filters = append(filters, loudnorm.Pass3FilterPrefix)
	}

	filters = append(filters, buildFullbenchLoudnormClause(loudnorm.EffectiveConfig, loudnorm.Measurement))

	if includeOutputAnalysis {
		if analysis := buildFullbenchPass4OutputAnalysisFilters(loudnorm.EffectiveConfig, loudnorm.Measurement); analysis != "" {
			filters = append(filters, analysis)
		}
	}

	filters = append(filters, buildFullbenchPass4ResampleFilter(loudnorm.EffectiveConfig))

	return strings.Join(filters, ",")
}

func buildFullbenchLoudnormClause(config *EffectiveFilterConfig, measurement *LoudnormMeasurement) string {
	return extractFullbenchFilterClause(
		buildFullbenchProductionPass4SpecWithoutAdeclick(config, measurement),
		"loudnorm=",
	)
}

func buildFullbenchPass4OutputAnalysisFilters(config *EffectiveFilterConfig, measurement *LoudnormMeasurement) string {
	clauses := strings.Split(buildFullbenchProductionPass4SpecWithoutAdeclick(config, measurement), ",")
	analysisStart := -1
	resampleStart := -1
	for i, clause := range clauses {
		switch {
		case strings.HasPrefix(clause, "astats="):
			analysisStart = i
		case strings.HasPrefix(clause, "aformat="):
			resampleStart = i
		}
	}
	if analysisStart == -1 || resampleStart == -1 || analysisStart >= resampleStart {
		return ""
	}

	return strings.Join(clauses[analysisStart:resampleStart], ",")
}

func buildFullbenchPass4ResampleFilter(config *EffectiveFilterConfig) string {
	resampleConfig := *config
	resampleConfig.Resample.Enabled = true
	return resampleConfig.buildResampleFilter()
}

func buildFullbenchProductionPass4SpecWithoutAdeclick(config *EffectiveFilterConfig, measurement *LoudnormMeasurement) string {
	pass4Config := *config
	pass4Config.Adeclick.Enabled = false
	return buildLoudnormFilterSpec(&pass4Config, measurement, measurement.TargetOffset, 0, 0, false, 48000, "")
}

func extractFullbenchFilterClause(spec, prefix string) string {
	for clause := range strings.SplitSeq(spec, ",") {
		if strings.HasPrefix(clause, prefix) {
			return clause
		}
	}

	return ""
}

func TestFullbenchPass2AblationSpecs(t *testing.T) {
	config := newFullbenchEffectiveTestConfig()
	config.Downmix.Enabled = true
	config.RumbleHighPass.Enabled = true
	config.BandlimitLowPass.Enabled = true
	config.NoiseReduction.Enabled = true
	config.NoiseReduction.AfftdnEnabled = true
	config.SpeechGate.Enabled = true
	config.LevellingCompressor.Enabled = true
	config.Deesser.Enabled = true
	config.Deesser.Intensity = 0.5
	config.Analysis.Enabled = true
	config.Resample.Enabled = true
	config.FilterOrder = Pass2FilterOrder

	variantByName := make(map[string]fullbenchPass2AblationVariant)
	for _, variant := range fullbenchPass2AblationVariants() {
		variantByName[variant.Name] = variant
	}

	expectedNames := []string{
		"full_chain",
		"without_output_analysis",
		"without_anlmdn",
		"without_afftdn",
		"without_gate_compressor",
	}
	if len(variantByName) != len(expectedNames) {
		t.Fatalf("got %d Pass 2 ablation variants, want %d", len(variantByName), len(expectedNames))
	}
	for _, name := range expectedNames {
		if _, ok := variantByName[name]; !ok {
			t.Fatalf("missing Pass 2 ablation variant %q", name)
		}
	}

	tests := []struct {
		name                string
		wantPresent         []string
		wantAbsent          []string
		extractMeasurements bool
	}{
		{
			name: "full_chain",
			wantPresent: []string{
				"anlmdn=", "afftdn=", "agate=", "acompressor=",
				"astats=", "aspectralstats=", "ebur128=",
				"aformat=sample_rates=44100", "asetnsamples=",
			},
			wantAbsent:          []string{"compand="},
			extractMeasurements: true,
		},
		{
			name:        "without_output_analysis",
			wantPresent: []string{"anlmdn=", "afftdn=", "agate=", "acompressor=", "aformat=sample_rates=44100", "asetnsamples="},
			wantAbsent:  []string{"astats=", "aspectralstats=", "ebur128="},
		},
		{
			name:                "without_anlmdn",
			wantPresent:         []string{"afftdn=", "agate=", "acompressor=", "astats=", "aspectralstats=", "ebur128=", "aformat=sample_rates=44100", "asetnsamples="},
			wantAbsent:          []string{"anlmdn="},
			extractMeasurements: true,
		},
		{
			name:                "without_afftdn",
			wantPresent:         []string{"anlmdn=", "agate=", "acompressor=", "astats=", "aspectralstats=", "ebur128=", "aformat=sample_rates=44100", "asetnsamples="},
			wantAbsent:          []string{"afftdn=", "compand="},
			extractMeasurements: true,
		},
		{
			name:                "without_gate_compressor",
			wantPresent:         []string{"anlmdn=", "afftdn=", "astats=", "aspectralstats=", "ebur128=", "aformat=sample_rates=44100", "asetnsamples="},
			wantAbsent:          []string{"agate=", "acompressor="},
			extractMeasurements: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			variant := variantByName[tt.name]
			spec := variant.BuildSpec(config)
			assertFullbenchSpecContains(t, spec, tt.wantPresent)
			assertFullbenchSpecExcludes(t, spec, tt.wantAbsent)
			if variant.ExtractMeasurements != tt.extractMeasurements {
				t.Fatalf("ExtractMeasurements = %v, want %v", variant.ExtractMeasurements, tt.extractMeasurements)
			}
		})
	}
}

func TestFullbenchPass2WithoutAnlmdnPreservesOrder(t *testing.T) {
	config := newFullbenchEffectiveTestConfig()
	config.Downmix.Enabled = true
	config.RumbleHighPass.Enabled = true
	config.BandlimitLowPass.Enabled = true
	config.NoiseReduction.Enabled = true
	config.NoiseReduction.AfftdnEnabled = true
	config.SpeechGate.Enabled = true
	config.LevellingCompressor.Enabled = true
	config.Deesser.Enabled = true
	config.Deesser.Intensity = 0.5
	config.Analysis.Enabled = true
	config.Resample.Enabled = true
	config.FilterOrder = Pass2FilterOrder

	spec := buildFullbenchPass2WithoutAnlmdnSpec(config)

	assertFullbenchSpecExcludes(t, spec, []string{"anlmdn=", "compand="})
	assertFullbenchSpecContains(t, spec, []string{"afftdn="})
	assertFullbenchSpecOrder(t, spec, []string{
		"aformat=channel_layouts=mono",
		"highpass=",
		"lowpass=",
		"afftdn=",
		"agate=",
		"acompressor=",
		"deesser=",
		"astats=",
		"aspectralstats=",
		"ebur128=",
		"aformat=sample_rates=44100",
		"asetnsamples=",
	})
}

func TestFullbenchLoudnormClauseMatchesProduction(t *testing.T) {
	config := newFullbenchEffectiveTestConfig()
	config.Adeclick.Enabled = true
	config.Loudnorm.TargetI = -17.25
	config.Loudnorm.TargetTP = -2.25
	config.Loudnorm.TargetLRA = 9.0

	measurement := &LoudnormMeasurement{
		InputI:       -23.25,
		InputTP:      -4.50,
		InputLRA:     5.75,
		InputThresh:  -34.25,
		TargetOffset: -0.75,
	}

	productionConfig := *config
	productionConfig.Adeclick.Enabled = false
	productionClause := extractFullbenchFilterClause(
		buildLoudnormFilterSpec(&productionConfig, measurement, measurement.TargetOffset, 0, 0, false, 48000, ""),
		"loudnorm=",
	)
	benchmarkClause := buildFullbenchLoudnormClause(config, measurement)

	if benchmarkClause != productionClause {
		t.Fatalf("benchmark loudnorm clause drifted from production\nbenchmark:  %s\nproduction: %s", benchmarkClause, productionClause)
	}
	assertFullbenchSpecContains(t, benchmarkClause, []string{
		"I=-17.25",
		"TP=0.00", // per-file loudnorm-internal TP: measured_TP -4.50 + (TargetI -17.25 - measured_I -23.25) + cushion 0.3 = 1.80, clamped to FFmpeg's [-9, 0] range
		"LRA=9.0",
		"measured_I=-23.25",
		"measured_TP=-4.50",
		"measured_LRA=5.75",
		"measured_thresh=-34.25",
		"offset=-0.75",
		"dual_mono=true",
		"linear=true",
		"print_format=json",
	})
	assertFullbenchSpecExcludes(t, benchmarkClause, []string{
		"adeclick=", "astats=", "aspectralstats=", "ebur128=", "aformat=", "asetnsamples=",
	})
}

func TestFullbenchPass4AblationSpecs(t *testing.T) {
	config := newFullbenchEffectiveTestConfig()
	config.Adeclick.Enabled = true
	config.Resample.Enabled = false
	measurement := &LoudnormMeasurement{
		InputI:       -24.0,
		InputTP:      -6.0,
		InputLRA:     7.0,
		InputThresh:  -34.0,
		TargetOffset: -1.0,
	}
	loudnorm := &fullbenchLoudnormSetup{
		Measurement:       measurement,
		EffectiveConfig:   config,
		Pass3FilterPrefix: buildPreLimiterPrefix(0, -12.0, true),
		LimiterNeeded:     true,
		LimiterCeiling:    -12.0,
	}

	variantByName := make(map[string]fullbenchPass4AblationVariant)
	for _, variant := range fullbenchPass4AblationVariants() {
		variantByName[variant.Name] = variant
	}

	expectedNames := []string{
		"loudnorm_only",
		"loudnorm_plus_limiter",
		"loudnorm_plus_output_analysis",
	}
	if len(variantByName) != len(expectedNames) {
		t.Fatalf("got %d Pass 4 ablation variants, want %d", len(variantByName), len(expectedNames))
	}
	for _, name := range expectedNames {
		if _, ok := variantByName[name]; !ok {
			t.Fatalf("missing Pass 4 ablation variant %q", name)
		}
	}

	tests := []struct {
		name                string
		wantPresent         []string
		wantAbsent          []string
		extractMeasurements bool
		requiresLimiter     bool
	}{
		{
			name:        "loudnorm_only",
			wantPresent: []string{"loudnorm=", "aformat=sample_rates=44100", "asetnsamples="},
			wantAbsent:  []string{"alimiter=", "astats=", "aspectralstats=", "ebur128=", "adeclick="},
		},
		{
			name:            "loudnorm_plus_limiter",
			wantPresent:     []string{"alimiter=", "loudnorm=", "aformat=sample_rates=44100", "asetnsamples="},
			wantAbsent:      []string{"astats=", "aspectralstats=", "ebur128=", "adeclick="},
			requiresLimiter: true,
		},
		{
			name:                "loudnorm_plus_output_analysis",
			wantPresent:         []string{"loudnorm=", "astats=", "aspectralstats=", "ebur128=", "aformat=sample_rates=44100", "asetnsamples="},
			wantAbsent:          []string{"alimiter=", "adeclick="},
			extractMeasurements: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			variant := variantByName[tt.name]
			spec := variant.BuildSpec(loudnorm)
			assertFullbenchSpecContains(t, spec, tt.wantPresent)
			assertFullbenchSpecExcludes(t, spec, tt.wantAbsent)
			if variant.ExtractMeasurements != tt.extractMeasurements {
				t.Fatalf("ExtractMeasurements = %v, want %v", variant.ExtractMeasurements, tt.extractMeasurements)
			}
			if variant.RequiresLimiter != tt.requiresLimiter {
				t.Fatalf("RequiresLimiter = %v, want %v", variant.RequiresLimiter, tt.requiresLimiter)
			}
		})
	}

	limiterSpec := variantByName["loudnorm_plus_limiter"].BuildSpec(loudnorm)
	assertFullbenchSpecOrder(t, limiterSpec, []string{"alimiter=", "loudnorm=", "aformat=", "asetnsamples="})

	analysisSpec := variantByName["loudnorm_plus_output_analysis"].BuildSpec(loudnorm)
	assertFullbenchSpecOrder(t, analysisSpec, []string{
		"loudnorm=",
		"astats=",
		"aspectralstats=",
		"ebur128=",
		"aformat=",
		"asetnsamples=",
	})
}

func TestFullbenchPass4LimiterVariantOmitsInactivePrefix(t *testing.T) {
	config := newFullbenchEffectiveTestConfig()
	measurement := &LoudnormMeasurement{
		InputI:       -20.0,
		InputTP:      -8.0,
		InputLRA:     5.0,
		InputThresh:  -30.0,
		TargetOffset: 0.0,
	}
	loudnorm := &fullbenchLoudnormSetup{
		Measurement:     measurement,
		EffectiveConfig: config,
	}

	for _, variant := range fullbenchPass4AblationVariants() {
		if variant.Name != "loudnorm_plus_limiter" {
			continue
		}
		if !variant.RequiresLimiter {
			t.Fatal("loudnorm_plus_limiter must require an active limiter prefix")
		}
		spec := variant.BuildSpec(loudnorm)
		assertFullbenchSpecContains(t, spec, []string{"loudnorm=", "aformat=sample_rates=44100", "asetnsamples="})
		assertFullbenchSpecExcludes(t, spec, []string{"alimiter=", "volume="})
		return
	}

	t.Fatal("missing loudnorm_plus_limiter variant")
}

func assertFullbenchSpecContains(tb testing.TB, spec string, parts []string) {
	tb.Helper()

	for _, part := range parts {
		if !strings.Contains(spec, part) {
			tb.Fatalf("filter spec missing %q\nSpec: %s", part, spec)
		}
	}
}

func assertFullbenchSpecExcludes(tb testing.TB, spec string, parts []string) {
	tb.Helper()

	for _, part := range parts {
		if strings.Contains(spec, part) {
			tb.Fatalf("filter spec contains unwanted %q\nSpec: %s", part, spec)
		}
	}
}

func assertFullbenchSpecOrder(tb testing.TB, spec string, parts []string) {
	tb.Helper()

	previous := -1
	for _, part := range parts {
		current := strings.Index(spec, part)
		if current == -1 {
			tb.Fatalf("filter spec missing %q\nSpec: %s", part, spec)
		}
		if current <= previous {
			tb.Fatalf("filter spec order mismatch at %q\nSpec: %s", part, spec)
		}
		previous = current
	}
}

func TestRunFullbenchFilterSpecSyntheticSmoke(t *testing.T) {
	inputPath := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 1.0,
		SampleRate:   44100,
		ToneFreq:     180.0,
		ToneLevel:    -18.0,
		NoiseLevel:   -58.0,
		Dir:          t.TempDir(),
	})
	defer cleanupTestAudio(t, inputPath)

	config := newFullbenchEffectiveTestConfig()
	config.Analysis.Enabled = true
	config.Resample.Enabled = true
	config.FilterOrder = Pass2FilterOrder
	filterSpec := config.BuildFilterSpec()

	measuredOutputPath := filepath.Join(t.TempDir(), "measured.flac")
	inputMetadata, outputMeasurements := runFullbenchFilterSpec(t, inputPath, measuredOutputPath, filterSpec, true)
	if inputMetadata.SampleRate != 44100 {
		t.Fatalf("input metadata sample rate mismatch: got %d, want 44100", inputMetadata.SampleRate)
	}
	if outputMeasurements == nil {
		t.Fatal("expected output measurements when extraction is requested")
	}
	assertFullbenchFLACOutput(t, measuredOutputPath)

	result := runFullbenchFilterSpecResult(t, inputPath, filepath.Join(t.TempDir(), "result.flac"), filterSpec)
	if result.InputMetadata.SampleRate != 44100 {
		t.Fatalf("result input metadata sample rate mismatch: got %d, want 44100", result.InputMetadata.SampleRate)
	}
	if result.OutputMetadata == nil {
		t.Fatal("expected result output metadata")
	}
	if result.OutputMetadata.SampleRate != 44100 {
		t.Fatalf("result output metadata sample rate mismatch: got %d, want 44100", result.OutputMetadata.SampleRate)
	}
	if result.OutputMetadata.Channels != 1 {
		t.Fatalf("result output metadata channels mismatch: got %d, want 1", result.OutputMetadata.Channels)
	}
	if result.OutputMeasurements == nil {
		t.Fatal("expected result output measurements when extraction is requested")
	}

	unmeasuredOutputPath := filepath.Join(t.TempDir(), "unmeasured.flac")
	_, unmeasuredOutputMeasurements := runFullbenchFilterSpec(t, inputPath, unmeasuredOutputPath, filterSpec, false)
	if unmeasuredOutputMeasurements != nil {
		t.Fatal("did not expect output measurements when extraction is not requested")
	}
	assertFullbenchFLACOutput(t, unmeasuredOutputPath)
}

func BenchmarkPass2FilterAblations(b *testing.B) {
	inputPath := resolveFullbenchFixture(b)
	adapted := setupFullbenchAdaptedConfig(b, inputPath)

	for _, variant := range fullbenchPass2AblationVariants() {
		b.Run(variant.Name, func(b *testing.B) {
			config := *adapted.Config
			config.FilterOrder = append([]FilterID(nil), Pass2FilterOrder...)
			spec := variant.BuildSpec(&config)
			if spec == "" {
				b.Fatal("Pass 2 ablation filter spec must not be empty")
			}

			outputDir := b.TempDir()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				outputPath := filepath.Join(outputDir, fmt.Sprintf("%s-%d.flac", variant.Name, i))
				_, outputMeasurements := runFullbenchFilterSpec(b, inputPath, outputPath, spec, variant.ExtractMeasurements)
				if variant.ExtractMeasurements && outputMeasurements == nil {
					b.Fatal("expected output measurements for Pass 2 ablation variant")
				}
				if !variant.ExtractMeasurements && outputMeasurements != nil {
					b.Fatal("did not expect output measurements for Pass 2 ablation variant")
				}

				b.StopTimer()
				if err := os.Remove(outputPath); err != nil {
					b.Fatalf("failed to remove Pass 2 ablation output: %v", err)
				}
				b.StartTimer()
			}
		})
	}
}

func BenchmarkPass4FilterAblations(b *testing.B) {
	inputPath := resolveFullbenchFixture(b)
	seed := setupFullbenchPass4Seed(b, inputPath)
	if seed.Pass2Seed == nil || seed.Loudnorm == nil {
		b.Fatal("Pass 4 ablation setup returned incomplete seed")
	}

	for _, variant := range fullbenchPass4AblationVariants() {
		b.Run(variant.Name, func(b *testing.B) {
			if variant.RequiresLimiter && seed.Loudnorm.Pass3FilterPrefix == "" {
				b.Fatal("Pass 4 limiter ablation requires a fixture that activates the limiter prefix")
			}

			spec := variant.BuildSpec(seed.Loudnorm)
			if spec == "" {
				b.Fatal("Pass 4 ablation filter spec must not be empty")
			}

			outputDir := b.TempDir()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				outputPath := filepath.Join(outputDir, fmt.Sprintf("%s-%d.flac", variant.Name, i))
				_, outputMeasurements := runFullbenchFilterSpec(
					b,
					seed.Pass2Seed.OutputPath,
					outputPath,
					spec,
					variant.ExtractMeasurements,
				)
				if variant.ExtractMeasurements && outputMeasurements == nil {
					b.Fatal("expected output measurements for Pass 4 ablation variant")
				}
				if !variant.ExtractMeasurements && outputMeasurements != nil {
					b.Fatal("did not expect output measurements for Pass 4 ablation variant")
				}

				b.StopTimer()
				if err := os.Remove(outputPath); err != nil {
					b.Fatalf("failed to remove Pass 4 ablation output: %v", err)
				}
				b.StartTimer()
			}
		})
	}
}

func readFullbenchOutputMetadata(tb testing.TB, outputPath string) *audio.Metadata {
	tb.Helper()

	reader, metadata, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		tb.Fatalf("failed to reopen fullbench output: %v", err)
	}
	defer reader.Close()

	return metadata
}

func assertFullbenchFLACOutput(tb testing.TB, outputPath string) {
	tb.Helper()

	info, err := os.Stat(outputPath)
	if err != nil {
		tb.Fatalf("fullbench output was not written: %v", err)
	}
	if info.Size() == 0 {
		tb.Fatal("fullbench output is empty")
	}

	metadata := readFullbenchOutputMetadata(tb, outputPath)
	if metadata.SampleRate != 44100 {
		tb.Fatalf("output sample rate mismatch: got %d, want 44100", metadata.SampleRate)
	}
	if metadata.Channels != 1 {
		tb.Fatalf("output channels mismatch: got %d, want 1", metadata.Channels)
	}
}

func setupFullbenchAdaptedConfig(tb testing.TB, inputPath string) *fullbenchAdaptedSetup {
	tb.Helper()

	config := DefaultFilterConfig()
	analysisResult, err := AnalyseOnlyDetailed(context.Background(), inputPath, config, nil)
	if err != nil {
		tb.Fatalf("failed to prepare fullbench adapted config: %v", err)
	}
	if analysisResult == nil || analysisResult.Config == nil || analysisResult.Measurements == nil {
		tb.Fatal("fullbench adapted setup returned incomplete analysis result")
	}

	analysisResult.Config.FilterOrder = append([]FilterID(nil), Pass2FilterOrder...)

	return &fullbenchAdaptedSetup{
		Config:       analysisResult.Config,
		Measurements: analysisResult.Measurements,
	}
}

func setupFullbenchPass2Seed(tb testing.TB, inputPath string, adapted *fullbenchAdaptedSetup) *fullbenchPass2Seed {
	tb.Helper()

	if adapted == nil || adapted.Config == nil || adapted.Measurements == nil {
		tb.Fatal("fullbench Pass 2 seed requires adapted config and measurements")
	}

	config := *adapted.Config
	config.FilterOrder = append([]FilterID(nil), Pass2FilterOrder...)
	config.Analysis.Enabled = true

	outputPath := filepath.Join(tb.TempDir(), "fullbench-pass2-seed.flac")
	inputMetadata, outputMeasurements := runFullbenchFilterSpec(
		tb,
		inputPath,
		outputPath,
		config.BuildFilterSpec(),
		true,
	)
	if outputMeasurements == nil {
		tb.Fatal("fullbench Pass 2 seed did not produce output measurements")
	}

	return &fullbenchPass2Seed{
		OutputPath:         outputPath,
		Config:             &config,
		InputMeasurements:  adapted.Measurements,
		OutputMeasurements: outputMeasurements,
		InputMetadata:      inputMetadata,
	}
}

func setupFullbenchLoudnormMeasurement(tb testing.TB, seed *fullbenchPass2Seed) *fullbenchLoudnormSetup {
	tb.Helper()

	if seed == nil || seed.Config == nil || seed.OutputMeasurements == nil {
		tb.Fatal("fullbench loudnorm setup requires a Pass 2 seed with output measurements")
	}

	config := *seed.Config
	limiter := planLimiterForLoudnorm(seed.OutputMeasurements, &config)
	measurement, err := measureWithLoudnorm(context.Background(), seed.OutputPath, &config, limiter.pass3Prefix, nil, defaultLoudnormDeps())
	if err != nil {
		tb.Fatalf("failed to prepare fullbench loudnorm measurement: %v", err)
	}
	if measurement == nil {
		tb.Fatal("fullbench loudnorm setup returned nil measurement")
		return nil
	}
	if math.IsInf(measurement.InputI, -1) || measurement.InputI < -70.0 {
		tb.Fatalf("fullbench loudnorm measurement is not usable: %.1f LUFS", measurement.InputI)
	}

	effectiveTargetI, _, linearPossible := calculateLinearModeTarget(
		measurement.InputI,
		measurement.InputTP,
		config.Loudnorm.TargetI,
		loudnormInternalTargetTP(config.Loudnorm, measurement.InputTP, measurement.InputI),
	)
	effectiveConfig := config
	effectiveConfig.Loudnorm.TargetI = effectiveTargetI

	return &fullbenchLoudnormSetup{
		Measurement:       measurement,
		EffectiveConfig:   &effectiveConfig,
		Pass3FilterPrefix: limiter.pass3Prefix,
		PreGainDB:         limiter.preGainDB,
		LimiterCeiling:    limiter.ceilingDB,
		LimiterNeeded:     limiter.needed,
		LimiterClamped:    limiter.clamped,
		LinearModeForced:  !linearPossible,
	}
}

func setupFullbenchPass4Seed(tb testing.TB, inputPath string) *fullbenchPass4Seed {
	tb.Helper()

	adapted := setupFullbenchAdaptedConfig(tb, inputPath)
	pass2Seed := setupFullbenchPass2Seed(tb, inputPath, adapted)
	loudnorm := setupFullbenchLoudnormMeasurement(tb, pass2Seed)

	return &fullbenchPass4Seed{
		Pass2Seed: pass2Seed,
		Loudnorm:  loudnorm,
	}
}

func TestFullbenchSetupHelpersSyntheticSmoke(t *testing.T) {
	inputPath := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 2.0,
		SampleRate:   44100,
		ToneFreq:     180.0,
		ToneLevel:    -18.0,
		NoiseLevel:   -58.0,
		Dir:          t.TempDir(),
	})
	defer cleanupTestAudio(t, inputPath)

	adapted := setupFullbenchAdaptedConfig(t, inputPath)
	if adapted.Config == nil {
		t.Fatal("expected adapted config")
	}
	if adapted.Measurements == nil {
		t.Fatal("expected Pass 1 measurements")
	}
	assertNoStaleEffectiveConfigFields(t)

	pass2Seed := setupFullbenchPass2Seed(t, inputPath, adapted)
	if pass2Seed.OutputMeasurements == nil {
		t.Fatal("expected Pass 2 seed output measurements")
	}
	assertFullbenchFLACOutput(t, pass2Seed.OutputPath)

	loudnorm := setupFullbenchLoudnormMeasurement(t, pass2Seed)
	if loudnorm.Measurement == nil {
		t.Fatal("expected loudnorm measurement")
	}
	if loudnorm.EffectiveConfig == nil {
		t.Fatal("expected effective loudnorm config")
	}
	if loudnorm.Pass3FilterPrefix != buildPreLimiterPrefix(loudnorm.PreGainDB, loudnorm.LimiterCeiling, loudnorm.LimiterNeeded) {
		t.Fatal("loudnorm setup prefix does not match computed limiter values")
	}
	if loudnorm.LimiterNeeded && loudnorm.Pass3FilterPrefix == "" {
		t.Fatal("active limiter setup did not produce a limiter prefix")
	}
	effectiveTargetI, _, linearPossible := calculateLinearModeTarget(
		loudnorm.Measurement.InputI,
		loudnorm.Measurement.InputTP,
		pass2Seed.Config.Loudnorm.TargetI,
		loudnormInternalTargetTP(pass2Seed.Config.Loudnorm, loudnorm.Measurement.InputTP, loudnorm.Measurement.InputI),
	)
	if math.Abs(loudnorm.EffectiveConfig.Loudnorm.TargetI-effectiveTargetI) > 0.01 {
		t.Fatalf("effective target mismatch: got %.2f, want %.2f", loudnorm.EffectiveConfig.Loudnorm.TargetI, effectiveTargetI)
	}
	if loudnorm.LinearModeForced != !linearPossible {
		t.Fatalf("linear mode forced mismatch: got %v, want %v", loudnorm.LinearModeForced, !linearPossible)
	}

	pass4Seed := setupFullbenchPass4Seed(t, inputPath)
	if pass4Seed.Pass2Seed == nil || pass4Seed.Loudnorm == nil {
		t.Fatal("expected combined Pass 4 seed setup")
	}
	assertFullbenchFLACOutput(t, pass4Seed.Pass2Seed.OutputPath)
}
