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
	"runtime/debug"
	"strings"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
)

const (
	anlmdnBenchmarkCaptureEnv       = "JIVETALKING_ANLMDN_BENCH_CAPTURE"
	anlmdnBenchmarkCaptureRootEnv   = "JIVETALKING_ANLMDN_BENCH_CAPTURE_ROOT"
	anlmdnBenchmarkCaptureRoot      = ".bench/anlmdn"
	anlmdnBenchmarkPatchAdjustedSec = 0.0050
	anlmdnBenchmarkFirstRunBestRSec = 0.0045
	ffmpegStatigoModulePath         = "github.com/linuxmatters/ffmpeg-statigo"
)

type anlmdnBenchmarkParams struct {
	Strength    float64
	PatchSec    float64
	ResearchSec float64
	Smooth      float64
}

type anlmdnBenchmarkVariant struct {
	Name                string
	ParameterIntent     string
	ValidationPriority  string
	PreAnlmdnSampleRate int
	Params              anlmdnBenchmarkParams
}

func anlmdnBenchmarkVariants() []anlmdnBenchmarkVariant {
	legacyDefault := anlmdnBenchmarkParams{
		Strength:    noiseRemoveLegacyStrength,
		PatchSec:    noiseRemoveLegacyPatchSec,
		ResearchSec: noiseRemoveLegacyResearchSec,
		Smooth:      noiseRemoveLegacySmooth,
	}
	productionDefault := anlmdnBenchmarkParams{
		Strength:    noiseRemoveProductionStrength,
		PatchSec:    noiseRemoveProductionPatchSec,
		ResearchSec: noiseRemoveProductionResearchSec,
		Smooth:      noiseRemoveProductionSmooth,
	}

	return []anlmdnBenchmarkVariant{
		{
			Name:               "anlmdn_legacy_default",
			ParameterIntent:    "Legacy production baseline before the 32 kHz lower-r default.",
			ValidationPriority: "baseline",
			Params:             legacyDefault,
		},
		{
			Name:               "anlmdn_r_5_0",
			ParameterIntent:    "Conservative lower research radius candidate.",
			ValidationPriority: "tier1-conservative",
			Params: anlmdnBenchmarkParams{
				Strength:    legacyDefault.Strength,
				PatchSec:    legacyDefault.PatchSec,
				ResearchSec: 0.0050,
				Smooth:      legacyDefault.Smooth,
			},
		},
		{
			Name:               "anlmdn_r_4_5",
			ParameterIntent:    "Stronger lower research radius candidate.",
			ValidationPriority: "tier1-balanced",
			Params: anlmdnBenchmarkParams{
				Strength:    legacyDefault.Strength,
				PatchSec:    legacyDefault.PatchSec,
				ResearchSec: 0.0045,
				Smooth:      legacyDefault.Smooth,
			},
		},
		{
			Name:               "anlmdn_r_4_0",
			ParameterIntent:    "Aggressive lower research radius candidate.",
			ValidationPriority: "tier1-aggressive",
			Params: anlmdnBenchmarkParams{
				Strength:    legacyDefault.Strength,
				PatchSec:    legacyDefault.PatchSec,
				ResearchSec: 0.0040,
				Smooth:      legacyDefault.Smooth,
			},
		},
		{
			Name:               "anlmdn_r_patch_adjusted",
			ParameterIntent:    "Lower research radius plus source-supported smaller patch check.",
			ValidationPriority: "tier3-patch-check",
			Params: anlmdnBenchmarkParams{
				Strength:    legacyDefault.Strength,
				PatchSec:    anlmdnBenchmarkPatchAdjustedSec,
				ResearchSec: anlmdnBenchmarkFirstRunBestRSec,
				Smooth:      legacyDefault.Smooth,
			},
		},
		{
			Name:                "anlmdn_sr_32000",
			ParameterIntent:     "32 kHz pre-anlmdn sample-rate cap with legacy anlmdn parameters.",
			ValidationPriority:  "tier2-sample-rate-baseline-r",
			PreAnlmdnSampleRate: noiseRemoveProductionPreSampleRate,
			Params:              legacyDefault,
		},
		{
			Name:                "anlmdn_sr_32000_best_r",
			ParameterIntent:     "Production default: 32 kHz pre-anlmdn sample-rate cap with the selected lower-r candidate.",
			ValidationPriority:  "production-default",
			PreAnlmdnSampleRate: noiseRemoveProductionPreSampleRate,
			Params:              productionDefault,
		},
	}
}

func buildAnlmdnBenchmarkVariantConfig(base *FilterChainConfig, variant anlmdnBenchmarkVariant) *FilterChainConfig {
	if base == nil {
		return nil
	}

	config := *base
	config.Pass = PassProcessing
	config.FilterOrder = Pass2FilterOrder
	config.NoiseRemoveEnabled = true
	config.NoiseRemovePreSampleRate = variant.PreAnlmdnSampleRate
	config.NoiseRemoveStrength = variant.Params.Strength
	config.NoiseRemovePatchSec = variant.Params.PatchSec
	config.NoiseRemoveResearchSec = variant.Params.ResearchSec
	config.NoiseRemoveSmooth = variant.Params.Smooth
	return &config
}

func buildAnlmdnBenchmarkVariantSpec(base *FilterChainConfig, variant anlmdnBenchmarkVariant) string {
	config := buildAnlmdnBenchmarkVariantConfig(base, variant)
	if config == nil {
		return ""
	}

	return config.BuildFilterSpec()
}

type anlmdnBenchmarkVariantSpec struct {
	Variant anlmdnBenchmarkVariant
	Spec    string
}

type anlmdnBenchmarkCaptureResult struct {
	Variant  anlmdnBenchmarkVariant
	Snapshot anlmdnBenchmarkMetricsSnapshot
}

type anlmdnBenchmarkAudioMetadataSnapshot struct {
	SampleRate    int     `json:"sample_rate"`
	Channels      int     `json:"channels"`
	DurationSecs  float64 `json:"duration_secs"`
	SampleFmt     string  `json:"sample_fmt,omitempty"`
	ChannelLayout string  `json:"channel_layout,omitempty"`
	BitDepth      int     `json:"bit_depth,omitempty"`
}

type anlmdnBenchmarkNormalisationSnapshot struct {
	InputLUFS        float64 `json:"input_lufs"`
	InputTP          float64 `json:"input_tp"`
	OutputLUFS       float64 `json:"output_lufs"`
	OutputTP         float64 `json:"output_tp"`
	GainApplied      float64 `json:"gain_applied"`
	WithinTarget     bool    `json:"within_target"`
	RequestedTargetI float64 `json:"requested_target_i"`
	EffectiveTargetI float64 `json:"effective_target_i"`
	LinearModeForced bool    `json:"linear_mode_forced"`
	LimiterEnabled   bool    `json:"limiter_enabled"`
	LimiterCeiling   float64 `json:"limiter_ceiling"`
	PreGainDB        float64 `json:"pre_gain_db"`
	LimiterClamped   bool    `json:"limiter_clamped"`
}

type anlmdnBenchmarkEnvironmentSnapshot struct {
	GoVersion                  string `json:"go_version"`
	GOOS                       string `json:"goos"`
	GOARCH                     string `json:"goarch"`
	NumCPU                     int    `json:"num_cpu"`
	CPUModel                   string `json:"cpu_model"`
	BenchmarkFixturePath       string `json:"benchmark_fixture_path"`
	FfmpegStatigoModuleVersion string `json:"ffmpeg_statigo_module_version"`
	FfmpegStatigoModuleReplace string `json:"ffmpeg_statigo_module_replace"`
}

type anlmdnBenchmarkMetricsSnapshot struct {
	VariantName                 string                               `json:"variant_name"`
	ParameterIntent             string                               `json:"parameter_intent"`
	ValidationPriority          string                               `json:"validation_priority"`
	PreAnlmdnSampleRate         int                                  `json:"pre_anlmdn_sample_rate,omitempty"`
	FilterSpec                  string                               `json:"filter_spec"`
	Pass2RuntimeMS              float64                              `json:"pass2_runtime_ms"`
	FinalLUFS                   float64                              `json:"final_lufs"`
	FinalTruePeak               float64                              `json:"final_true_peak"`
	FinalLRA                    float64                              `json:"final_lra"`
	FinalNoiseFloor             float64                              `json:"final_noise_floor"`
	FinalSpectralCentroid       float64                              `json:"final_spectral_centroid"`
	FinalSpectralRolloff        float64                              `json:"final_spectral_rolloff"`
	FinalSpeechRMS              float64                              `json:"final_speech_rms"`
	MissingSilenceProfile       bool                                 `json:"missing_silence_profile"`
	MissingSpeechProfile        bool                                 `json:"missing_speech_profile"`
	QualityValidationIncomplete bool                                 `json:"quality_validation_incomplete"`
	InputNoiseFloor             float64                              `json:"input_noise_floor"`
	InputNoiseFloorSource       string                               `json:"input_noise_floor_source"`
	InputMetadata               InputMetadata                        `json:"input_metadata"`
	OutputMetadata              anlmdnBenchmarkAudioMetadataSnapshot `json:"output_metadata"`
	Environment                 anlmdnBenchmarkEnvironmentSnapshot   `json:"environment"`
	Pass2Measurements           *OutputMeasurements                  `json:"pass2_measurements,omitempty"`
	FinalMeasurements           *OutputMeasurements                  `json:"final_measurements,omitempty"`
	InputNoiseProfile           *NoiseProfile                        `json:"input_noise_profile,omitempty"`
	InputSpeechProfile          *SpeechCandidateMetrics              `json:"input_speech_profile,omitempty"`
	FinalSilenceSample          *SilenceCandidateMetrics             `json:"final_silence_sample,omitempty"`
	FinalSpeechSample           *SpeechCandidateMetrics              `json:"final_speech_sample,omitempty"`
	Normalisation               anlmdnBenchmarkNormalisationSnapshot `json:"normalisation"`
	Warnings                    []string                             `json:"warnings,omitempty"`
}

func buildAnlmdnBenchmarkVariantSpecs(tb testing.TB, base *FilterChainConfig) []anlmdnBenchmarkVariantSpec {
	tb.Helper()

	variants := anlmdnBenchmarkVariants()
	specs := make([]anlmdnBenchmarkVariantSpec, 0, len(variants))
	for _, variant := range variants {
		spec := buildAnlmdnBenchmarkVariantSpec(base, variant)
		if spec == "" {
			tb.Fatalf("anlmdn benchmark variant %q produced empty filter spec", variant.Name)
		}
		if !strings.Contains(spec, "anlmdn=") {
			tb.Fatalf("anlmdn benchmark variant %q omitted anlmdn\nSpec: %s", variant.Name, spec)
		}
		specs = append(specs, anlmdnBenchmarkVariantSpec{
			Variant: variant,
			Spec:    spec,
		})
	}

	return specs
}

func BenchmarkAnlmdnVariants(b *testing.B) {
	inputPath := resolveFullbenchFixture(b)
	adapted := setupFullbenchAdaptedConfig(b, inputPath)
	variantSpecs := buildAnlmdnBenchmarkVariantSpecs(b, adapted.Config)

	if root, ok := resolveAnlmdnBenchmarkCaptureRoot(); ok {
		captureAnlmdnBenchmarkArtifacts(b, inputPath, adapted, variantSpecs, root)
	}

	for _, variantSpec := range variantSpecs {
		b.Run(variantSpec.Variant.Name, func(b *testing.B) {
			outputDir := b.TempDir()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				outputPath := filepath.Join(outputDir, fmt.Sprintf("%s-%d.flac", variantSpec.Variant.Name, i))
				_, outputMeasurements := runFullbenchFilterSpec(b, inputPath, outputPath, variantSpec.Spec, true)
				if outputMeasurements == nil {
					b.Fatal("expected output measurements for anlmdn benchmark variant")
				}

				b.StopTimer()
				if err := os.Remove(outputPath); err != nil {
					b.Fatalf("failed to remove anlmdn benchmark output: %v", err)
				}
				b.StartTimer()
			}
		})
	}
}

func resolveAnlmdnBenchmarkCaptureRoot() (string, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(anlmdnBenchmarkCaptureEnv))) {
	case "", "0", "false", "no":
		return "", false
	default:
		root := strings.TrimSpace(os.Getenv(anlmdnBenchmarkCaptureRootEnv))
		if root == "" {
			root = anlmdnBenchmarkCaptureRoot
		}
		return resolveAnlmdnBenchmarkRepoPath(root), true
	}
}

func resolveAnlmdnBenchmarkRepoPath(path string) string {
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

func captureAnlmdnBenchmarkArtifacts(
	tb testing.TB,
	inputPath string,
	adapted *fullbenchAdaptedSetup,
	variantSpecs []anlmdnBenchmarkVariantSpec,
	root string,
) []anlmdnBenchmarkCaptureResult {
	tb.Helper()

	if adapted == nil || adapted.Config == nil || adapted.Measurements == nil {
		tb.Fatal("anlmdn artefact capture requires adapted config and measurements")
	}
	if root == "" {
		tb.Fatal("anlmdn artefact capture root must not be empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		tb.Fatalf("failed to create anlmdn artefact root: %v", err)
	}

	results := make([]anlmdnBenchmarkCaptureResult, 0, len(variantSpecs))
	for _, variantSpec := range variantSpecs {
		variantDir := filepath.Join(root, variantSpec.Variant.Name)
		if err := os.MkdirAll(variantDir, 0o755); err != nil {
			tb.Fatalf("failed to create anlmdn artefact directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(variantDir, "filter.txt"), []byte(variantSpec.Spec+"\n"), 0o600); err != nil {
			tb.Fatalf("failed to write anlmdn filter spec: %v", err)
		}

		outputPath := filepath.Join(variantDir, "processed.flac")
		start := time.Now()
		runResult := runFullbenchFilterSpecResult(tb, inputPath, outputPath, variantSpec.Spec, true)
		pass2Runtime := time.Since(start)
		if runResult.OutputMeasurements == nil {
			tb.Fatalf("anlmdn artefact capture for %s produced no Pass 2 measurements", variantSpec.Variant.Name)
		}

		config := buildAnlmdnBenchmarkVariantConfig(adapted.Config, variantSpec.Variant)
		normResult, err := ApplyNormalisation(outputPath, config, runResult.OutputMeasurements, adapted.Measurements, nil)
		if err != nil {
			tb.Fatalf("failed to normalise anlmdn artefact for %s: %v", variantSpec.Variant.Name, err)
		}
		if normResult == nil || normResult.FinalMeasurements == nil {
			tb.Fatalf("anlmdn artefact capture for %s produced no final measurements", variantSpec.Variant.Name)
		}

		outputMetadata := readFullbenchOutputMetadata(tb, outputPath)
		snapshot := buildAnlmdnBenchmarkMetricsSnapshot(
			variantSpec,
			inputPath,
			adapted.Measurements,
			runResult,
			outputMetadata,
			normResult,
			pass2Runtime,
		)

		writeAnlmdnBenchmarkJSON(tb, filepath.Join(variantDir, "metrics.json"), snapshot)
		writeAnlmdnBenchmarkTiming(tb, filepath.Join(variantDir, "timing.txt"), snapshot)

		results = append(results, anlmdnBenchmarkCaptureResult{
			Variant:  variantSpec.Variant,
			Snapshot: snapshot,
		})
	}

	baseline := findAnlmdnBenchmarkCaptureResult(results, "anlmdn_legacy_default")
	if baseline == nil {
		tb.Fatal("anlmdn artefact capture missing anlmdn_legacy_default baseline")
	}
	for _, result := range results {
		variantDir := filepath.Join(root, result.Variant.Name)
		report := buildAnlmdnBenchmarkValidationReport(result, *baseline)
		if err := os.WriteFile(filepath.Join(variantDir, "validation.md"), []byte(report), 0o600); err != nil {
			tb.Fatalf("failed to write anlmdn validation report: %v", err)
		}
	}

	return results
}

func buildAnlmdnBenchmarkMetricsSnapshot(
	variantSpec anlmdnBenchmarkVariantSpec,
	inputPath string,
	inputMeasurements *AudioMeasurements,
	runResult *fullbenchFilterSpecRunResult,
	outputMetadata *audio.Metadata,
	normResult *NormalisationResult,
	pass2Runtime time.Duration,
) anlmdnBenchmarkMetricsSnapshot {
	final := normResult.FinalMeasurements
	missingSilence := inputMeasurements == nil || inputMeasurements.NoiseProfile == nil || final.SilenceSample == nil
	missingSpeech := inputMeasurements == nil || inputMeasurements.SpeechProfile == nil || final.SpeechSample == nil
	warnings := make([]string, 0, 2)
	if missingSilence {
		warnings = append(warnings, "silence profile or final silence sample missing")
	}
	if missingSpeech {
		warnings = append(warnings, "speech profile or final speech sample missing")
	}

	var inputNoiseFloor float64
	var inputNoiseFloorSource string
	var inputNoiseProfile *NoiseProfile
	var inputSpeechProfile *SpeechCandidateMetrics
	if inputMeasurements != nil {
		inputNoiseFloor = inputMeasurements.NoiseFloor
		inputNoiseFloorSource = inputMeasurements.NoiseFloorSource
		inputNoiseProfile = inputMeasurements.NoiseProfile
		inputSpeechProfile = inputMeasurements.SpeechProfile
	}

	finalNoiseFloor := 0.0
	if final.SilenceSample != nil {
		finalNoiseFloor = final.SilenceSample.RMSLevel
	}
	finalSpeechRMS := 0.0
	if final.SpeechSample != nil {
		finalSpeechRMS = final.SpeechSample.RMSLevel
	}

	return anlmdnBenchmarkMetricsSnapshot{
		VariantName:                 variantSpec.Variant.Name,
		ParameterIntent:             variantSpec.Variant.ParameterIntent,
		ValidationPriority:          variantSpec.Variant.ValidationPriority,
		PreAnlmdnSampleRate:         variantSpec.Variant.PreAnlmdnSampleRate,
		FilterSpec:                  variantSpec.Spec,
		Pass2RuntimeMS:              float64(pass2Runtime.Microseconds()) / 1000.0,
		FinalLUFS:                   normResult.OutputLUFS,
		FinalTruePeak:               normResult.OutputTP,
		FinalLRA:                    final.OutputLRA,
		FinalNoiseFloor:             finalNoiseFloor,
		FinalSpectralCentroid:       final.SpectralCentroid,
		FinalSpectralRolloff:        final.SpectralRolloff,
		FinalSpeechRMS:              finalSpeechRMS,
		MissingSilenceProfile:       missingSilence,
		MissingSpeechProfile:        missingSpeech,
		QualityValidationIncomplete: missingSilence || missingSpeech,
		InputNoiseFloor:             inputNoiseFloor,
		InputNoiseFloorSource:       inputNoiseFloorSource,
		InputMetadata:               runResult.InputMetadata,
		OutputMetadata: anlmdnBenchmarkAudioMetadataSnapshot{
			SampleRate:    outputMetadata.SampleRate,
			Channels:      outputMetadata.Channels,
			DurationSecs:  outputMetadata.Duration,
			SampleFmt:     outputMetadata.SampleFmt,
			ChannelLayout: outputMetadata.ChLayout,
			BitDepth:      outputMetadata.BitDepth,
		},
		Environment:        buildAnlmdnBenchmarkEnvironmentSnapshot(inputPath),
		Pass2Measurements:  runResult.OutputMeasurements,
		FinalMeasurements:  final,
		InputNoiseProfile:  inputNoiseProfile,
		InputSpeechProfile: inputSpeechProfile,
		FinalSilenceSample: final.SilenceSample,
		FinalSpeechSample:  final.SpeechSample,
		Normalisation: anlmdnBenchmarkNormalisationSnapshot{
			InputLUFS:        normResult.InputLUFS,
			InputTP:          normResult.InputTP,
			OutputLUFS:       normResult.OutputLUFS,
			OutputTP:         normResult.OutputTP,
			GainApplied:      normResult.GainApplied,
			WithinTarget:     normResult.WithinTarget,
			RequestedTargetI: normResult.RequestedTargetI,
			EffectiveTargetI: normResult.EffectiveTargetI,
			LinearModeForced: normResult.LinearModeForced,
			LimiterEnabled:   normResult.LimiterEnabled,
			LimiterCeiling:   normResult.LimiterCeiling,
			PreGainDB:        normResult.PreGainDB,
			LimiterClamped:   normResult.LimiterClamped,
		},
		Warnings: warnings,
	}
}

func buildAnlmdnBenchmarkEnvironmentSnapshot(inputPath string) anlmdnBenchmarkEnvironmentSnapshot {
	version, replace := ffmpegStatigoModuleInfo()

	return anlmdnBenchmarkEnvironmentSnapshot{
		GoVersion:                  runtime.Version(),
		GOOS:                       runtime.GOOS,
		GOARCH:                     runtime.GOARCH,
		NumCPU:                     runtime.NumCPU(),
		CPUModel:                   detectAnlmdnBenchmarkCPUModel(),
		BenchmarkFixturePath:       resolveAnlmdnBenchmarkFixtureMetadataPath(inputPath),
		FfmpegStatigoModuleVersion: version,
		FfmpegStatigoModuleReplace: replace,
	}
}

func resolveAnlmdnBenchmarkFixtureMetadataPath(inputPath string) string {
	if fixturePath := strings.TrimSpace(os.Getenv(fullbenchFixtureEnv)); fixturePath != "" {
		return absOrOriginalPath(fixturePath)
	}
	return absOrOriginalPath(inputPath)
}

func absOrOriginalPath(path string) string {
	if path == "" {
		return "unavailable"
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return absPath
}

func ffmpegStatigoModuleInfo() (string, string) {
	version := "unavailable"
	replace := "unavailable"

	buildInfo, ok := debug.ReadBuildInfo()
	if ok {
		for _, dep := range buildInfo.Deps {
			if dep.Path != ffmpegStatigoModulePath {
				continue
			}
			if dep.Version != "" {
				version = dep.Version
			}
			if dep.Replace != nil {
				replace = dep.Replace.Path
				if dep.Replace.Version != "" {
					replace += " " + dep.Replace.Version
				}
			}
			return version, replace
		}
	}

	return ffmpegStatigoModuleInfoFromGoList(version, replace)
}

func ffmpegStatigoModuleInfoFromGoList(defaultVersion, defaultReplace string) (string, string) {
	const moduleFormat = "{{.Version}}{{if .Replace}} => {{.Replace.Path}}{{if .Replace.Version}} {{.Replace.Version}}{{end}}{{end}}"

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	output, err := exec.CommandContext(ctx, "go", "list", "-m", "-f", moduleFormat, ffmpegStatigoModulePath).Output() // #nosec G204 -- static local metadata command for benchmark artefacts.
	if err != nil {
		return defaultVersion, defaultReplace
	}

	moduleInfo := strings.TrimSpace(string(output))
	if moduleInfo == "" {
		return defaultVersion, defaultReplace
	}

	version, replace, hasReplace := strings.Cut(moduleInfo, " => ")
	if strings.TrimSpace(version) == "" {
		version = defaultVersion
	}
	if !hasReplace || strings.TrimSpace(replace) == "" {
		replace = defaultReplace
	}

	return strings.TrimSpace(version), strings.TrimSpace(replace)
}

func detectAnlmdnBenchmarkCPUModel() string {
	switch runtime.GOOS {
	case "linux":
		return detectLinuxCPUModel()
	case "darwin":
		return detectDarwinCPUModel()
	default:
		return "unavailable"
	}
}

func detectLinuxCPUModel() string {
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

func detectDarwinCPUModel() string {
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

func writeAnlmdnBenchmarkJSON(tb testing.TB, path string, snapshot anlmdnBenchmarkMetricsSnapshot) {
	tb.Helper()

	data, err := json.MarshalIndent(sanitizeAnlmdnBenchmarkJSONValue(reflect.ValueOf(snapshot)), "", "  ")
	if err != nil {
		tb.Fatalf("failed to encode anlmdn metrics snapshot: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		tb.Fatalf("failed to write anlmdn metrics snapshot: %v", err)
	}
}

func sanitizeAnlmdnBenchmarkJSONValue(value reflect.Value) any {
	if !value.IsValid() {
		return nil
	}

	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return sanitizeAnlmdnBenchmarkJSONValue(value.Elem())
	case reflect.Struct:
		result := make(map[string]any, value.NumField())
		valueType := value.Type()
		for i := 0; i < value.NumField(); i++ {
			field := valueType.Field(i)
			if field.PkgPath != "" {
				continue
			}
			if field.Anonymous && field.Tag.Get("json") == "" {
				if nested, ok := sanitizeAnlmdnBenchmarkJSONValue(value.Field(i)).(map[string]any); ok {
					maps.Copy(result, nested)
				}
				continue
			}

			name, omitEmpty, ok := anlmdnBenchmarkJSONFieldName(field)
			if !ok {
				continue
			}
			fieldValue := value.Field(i)
			if omitEmpty && fieldValue.IsZero() {
				continue
			}
			result[name] = sanitizeAnlmdnBenchmarkJSONValue(fieldValue)
		}
		return result
	case reflect.Slice, reflect.Array:
		result := make([]any, value.Len())
		for i := 0; i < value.Len(); i++ {
			result[i] = sanitizeAnlmdnBenchmarkJSONValue(value.Index(i))
		}
		return result
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return value.Interface()
		}
		result := make(map[string]any, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			result[iter.Key().String()] = sanitizeAnlmdnBenchmarkJSONValue(iter.Value())
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

func anlmdnBenchmarkJSONFieldName(field reflect.StructField) (string, bool, bool) {
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

func writeAnlmdnBenchmarkTiming(tb testing.TB, path string, snapshot anlmdnBenchmarkMetricsSnapshot) {
	tb.Helper()

	var builder strings.Builder
	fmt.Fprintf(&builder, "variant: %s\n", snapshot.VariantName)
	fmt.Fprintf(&builder, "pass2_runtime_ms: %.3f\n", snapshot.Pass2RuntimeMS)
	fmt.Fprintf(&builder, "final_lufs: %.2f\n", snapshot.FinalLUFS)
	fmt.Fprintf(&builder, "final_true_peak: %.2f\n", snapshot.FinalTruePeak)
	if err := os.WriteFile(path, []byte(builder.String()), 0o600); err != nil {
		tb.Fatalf("failed to write anlmdn timing capture: %v", err)
	}
}

func findAnlmdnBenchmarkCaptureResult(results []anlmdnBenchmarkCaptureResult, name string) *anlmdnBenchmarkCaptureResult {
	for i := range results {
		if results[i].Variant.Name == name {
			return &results[i]
		}
	}
	return nil
}

func buildAnlmdnBenchmarkValidationReport(result, baseline anlmdnBenchmarkCaptureResult) string {
	snapshot := result.Snapshot
	base := baseline.Snapshot

	var builder strings.Builder
	fmt.Fprintf(&builder, "# %s\n\n", snapshot.VariantName)
	fmt.Fprintf(&builder, "Intent: %s\n\n", snapshot.ParameterIntent)
	fmt.Fprintf(&builder, "Validation priority: %s\n\n", snapshot.ValidationPriority)

	builder.WriteString("## Speed\n\n")
	builder.WriteString("| Metric | Candidate | anlmdn_legacy_default | Delta |\n")
	builder.WriteString("|--------|-----------|----------------|-------|\n")
	writeAnlmdnBenchmarkMetricRow(&builder, "Pass 2 runtime ms", snapshot.Pass2RuntimeMS, base.Pass2RuntimeMS, "%.3f")

	builder.WriteString("\n## Objective Metrics\n\n")
	builder.WriteString("| Metric | Candidate | anlmdn_legacy_default | Delta |\n")
	builder.WriteString("|--------|-----------|----------------|-------|\n")
	writeAnlmdnBenchmarkMetricRow(&builder, "Final LUFS", snapshot.FinalLUFS, base.FinalLUFS, "%.2f")
	writeAnlmdnBenchmarkMetricRow(&builder, "True peak dBTP", snapshot.FinalTruePeak, base.FinalTruePeak, "%.2f")
	writeAnlmdnBenchmarkMetricRow(&builder, "LRA LU", snapshot.FinalLRA, base.FinalLRA, "%.2f")
	writeAnlmdnBenchmarkMetricRow(&builder, "Noise floor dBFS", snapshot.FinalNoiseFloor, base.FinalNoiseFloor, "%.2f")
	writeAnlmdnBenchmarkMetricRow(&builder, "Spectral centroid Hz", snapshot.FinalSpectralCentroid, base.FinalSpectralCentroid, "%.1f")
	writeAnlmdnBenchmarkMetricRow(&builder, "Spectral rolloff Hz", snapshot.FinalSpectralRolloff, base.FinalSpectralRolloff, "%.1f")
	writeAnlmdnBenchmarkMetricRow(&builder, "Speech RMS dBFS", snapshot.FinalSpeechRMS, base.FinalSpeechRMS, "%.2f")

	builder.WriteString("\n## Missing Profiles\n\n")
	fmt.Fprintf(&builder, "- Missing silence profile or sample: %t\n", snapshot.MissingSilenceProfile)
	fmt.Fprintf(&builder, "- Missing speech profile or sample: %t\n", snapshot.MissingSpeechProfile)
	fmt.Fprintf(&builder, "- Quality validation incomplete: %t\n", snapshot.QualityValidationIncomplete)
	if len(snapshot.Warnings) > 0 {
		builder.WriteString("\n## Warnings\n\n")
		for _, warning := range snapshot.Warnings {
			fmt.Fprintf(&builder, "- %s\n", warning)
		}
	}

	builder.WriteString("\n## Listening Checklist\n\n")
	builder.WriteString("- Room noise reduction\n")
	builder.WriteString("- Watery artefacts\n")
	builder.WriteString("- Breath tails\n")
	builder.WriteString("- Consonant smearing\n")
	builder.WriteString("- Hollow tone\n")
	builder.WriteString("- Noise pumping between phrases\n")

	return builder.String()
}

func writeAnlmdnBenchmarkMetricRow(builder *strings.Builder, name string, value, baseline float64, format string) {
	valueFormat := format + " | "
	deltaFormat := "%+" + strings.TrimPrefix(format, "%") + " |\n"
	fmt.Fprintf(builder, "| %s | "+valueFormat+valueFormat+deltaFormat, name, value, baseline, value-baseline)
}

func newAnlmdnBenchmarkTestConfig() *FilterChainConfig {
	config := newTestConfig()
	config.DownmixEnabled = true
	config.DS201HPEnabled = true
	config.DS201LPEnabled = true
	config.NoiseRemoveEnabled = true
	config.NoiseRemoveCompandEnabled = true
	config.DS201GateEnabled = true
	config.LA2AEnabled = true
	config.DeessEnabled = true
	config.DeessIntensity = 0.5
	config.AnalysisEnabled = true
	config.OutputAnalysisEnabled = true
	config.ResampleEnabled = true
	config.FilterOrder = Pass2FilterOrder
	return config
}

func TestAnlmdnBenchmarkVariantManifest(t *testing.T) {
	variants := anlmdnBenchmarkVariants()
	expectedNames := []string{
		"anlmdn_legacy_default",
		"anlmdn_r_5_0",
		"anlmdn_r_4_5",
		"anlmdn_r_4_0",
		"anlmdn_r_patch_adjusted",
		"anlmdn_sr_32000",
		"anlmdn_sr_32000_best_r",
	}

	if len(variants) != len(expectedNames) {
		t.Fatalf("got %d anlmdn benchmark variants, want %d", len(variants), len(expectedNames))
	}

	variantByName := make(map[string]anlmdnBenchmarkVariant, len(variants))
	for _, variant := range variants {
		variantByName[variant.Name] = variant
		if variant.ParameterIntent == "" {
			t.Fatalf("variant %q missing parameter intent", variant.Name)
		}
		if variant.ValidationPriority == "" {
			t.Fatalf("variant %q missing validation priority", variant.Name)
		}
	}

	for _, name := range expectedNames {
		if _, ok := variantByName[name]; !ok {
			t.Fatalf("missing anlmdn benchmark variant %q", name)
		}
	}

	for _, name := range []string{"anlmdn_sr_32000", "anlmdn_sr_32000_best_r"} {
		if variantByName[name].PreAnlmdnSampleRate != noiseRemoveProductionPreSampleRate {
			t.Fatalf("%s PreAnlmdnSampleRate = %d, want %d",
				name, variantByName[name].PreAnlmdnSampleRate, noiseRemoveProductionPreSampleRate)
		}
	}
	for _, name := range []string{"anlmdn_legacy_default", "anlmdn_r_5_0", "anlmdn_r_4_5", "anlmdn_r_4_0", "anlmdn_r_patch_adjusted"} {
		if variantByName[name].PreAnlmdnSampleRate != 0 {
			t.Fatalf("%s should not use a pre-anlmdn sample-rate cap", name)
		}
	}

	if got := variantByName["anlmdn_sr_32000_best_r"].Params.ResearchSec; got != noiseRemoveProductionResearchSec {
		t.Fatalf("anlmdn_sr_32000_best_r research radius = %.4f, want production default %.4f",
			got, noiseRemoveProductionResearchSec)
	}
}

func TestAnlmdnBenchmarkParameterOnlySpecs(t *testing.T) {
	base := newAnlmdnBenchmarkTestConfig()
	base.NoiseRemoveCompandThreshold = -47.0
	base.NoiseRemoveCompandExpansion = 9.0

	tests := []struct {
		name     string
		wantSpec string
	}{
		{name: "anlmdn_legacy_default", wantSpec: "anlmdn=s=0.00001:p=0.0060:r=0.0058:m=11"},
		{name: "anlmdn_r_5_0", wantSpec: "anlmdn=s=0.00001:p=0.0060:r=0.0050:m=11"},
		{name: "anlmdn_r_4_5", wantSpec: "anlmdn=s=0.00001:p=0.0060:r=0.0045:m=11"},
		{name: "anlmdn_r_4_0", wantSpec: "anlmdn=s=0.00001:p=0.0060:r=0.0040:m=11"},
		{name: "anlmdn_r_patch_adjusted", wantSpec: "anlmdn=s=0.00001:p=0.0050:r=0.0045:m=11"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			variant := findAnlmdnBenchmarkVariant(t, tt.name)
			config := buildAnlmdnBenchmarkVariantConfig(base, variant)
			spec := buildAnlmdnBenchmarkVariantSpec(base, variant)

			if !config.NoiseRemoveEnabled {
				t.Fatal("parameter-only variant disabled NoiseRemove")
			}
			if config.NoiseRemoveCompandEnabled != base.NoiseRemoveCompandEnabled {
				t.Fatalf("NoiseRemoveCompandEnabled = %v, want preserved %v",
					config.NoiseRemoveCompandEnabled, base.NoiseRemoveCompandEnabled)
			}
			if config.NoiseRemoveCompandThreshold != base.NoiseRemoveCompandThreshold {
				t.Fatalf("compand threshold changed: got %.1f, want %.1f",
					config.NoiseRemoveCompandThreshold, base.NoiseRemoveCompandThreshold)
			}
			if config.NoiseRemoveCompandExpansion != base.NoiseRemoveCompandExpansion {
				t.Fatalf("compand expansion changed: got %.1f, want %.1f",
					config.NoiseRemoveCompandExpansion, base.NoiseRemoveCompandExpansion)
			}
			assertFullbenchSpecContains(t, spec, []string{tt.wantSpec, "anlmdn=", "compand="})
			assertFullbenchSpecExcludes(t, spec, []string{"aformat=sample_rates=32000"})
		})
	}

	assertBaseLegacyAnlmdnConfigUnchanged(t, base)
}

func TestAnlmdnBenchmarkPreSampleRateSpecs(t *testing.T) {
	base := newAnlmdnBenchmarkTestConfig()

	tests := []struct {
		name     string
		wantSpec string
	}{
		{name: "anlmdn_sr_32000", wantSpec: "anlmdn=s=0.00001:p=0.0060:r=0.0058:m=11"},
		{name: "anlmdn_sr_32000_best_r", wantSpec: "anlmdn=s=0.00001:p=0.0060:r=0.0045:m=11"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			variant := findAnlmdnBenchmarkVariant(t, tt.name)
			spec := buildAnlmdnBenchmarkVariantSpec(base, variant)

			assertFullbenchSpecContains(t, spec, []string{
				"aformat=sample_rates=32000:channel_layouts=mono:sample_fmts=fltp",
				tt.wantSpec,
				"anlmdn=",
				"compand=",
				"aformat=sample_rates=44100:channel_layouts=mono:sample_fmts=s16",
				"asetnsamples=n=4096",
			})
			assertFullbenchSpecOrder(t, spec, []string{
				"aformat=channel_layouts=mono",
				"highpass=",
				"lowpass=",
				"aformat=sample_rates=32000:channel_layouts=mono:sample_fmts=fltp",
				"anlmdn=",
				"compand=",
				"agate=",
				"acompressor=",
				"deesser=",
				"astats=",
				"aspectralstats=",
				"ebur128=",
				"aformat=sample_rates=44100",
				"asetnsamples=",
			})
		})
	}
}

func TestAnlmdnBenchmarkVariantSpecsPreservePass2Ordering(t *testing.T) {
	base := newAnlmdnBenchmarkTestConfig()

	for _, variant := range anlmdnBenchmarkVariants() {
		t.Run(variant.Name, func(t *testing.T) {
			spec := buildAnlmdnBenchmarkVariantSpec(base, variant)
			if spec == "" {
				t.Fatal("variant produced empty filter spec")
			}

			assertFullbenchSpecContains(t, spec, []string{
				"anlmdn=",
				"compand=",
				"aformat=sample_rates=44100",
				"asetnsamples=",
			})
			assertFullbenchSpecOrder(t, spec, []string{
				"aformat=channel_layouts=mono",
				"highpass=",
				"lowpass=",
				"anlmdn=",
				"compand=",
				"agate=",
				"acompressor=",
				"deesser=",
				"astats=",
				"aspectralstats=",
				"ebur128=",
				"aformat=sample_rates=44100",
				"asetnsamples=",
			})
		})
	}
}

func TestAnlmdnBenchmarkProductionDefaultsMatchFastVariant(t *testing.T) {
	config := DefaultFilterConfig()

	if !config.NoiseRemoveEnabled {
		t.Fatal("production NoiseRemoveEnabled changed")
	}
	if config.NoiseRemovePreSampleRate != noiseRemoveProductionPreSampleRate {
		t.Fatalf("production NoiseRemovePreSampleRate = %d, want %d",
			config.NoiseRemovePreSampleRate, noiseRemoveProductionPreSampleRate)
	}
	if config.NoiseRemoveStrength != noiseRemoveProductionStrength {
		t.Fatalf("production NoiseRemoveStrength = %.5f, want %.5f",
			config.NoiseRemoveStrength, noiseRemoveProductionStrength)
	}
	if config.NoiseRemovePatchSec != noiseRemoveProductionPatchSec {
		t.Fatalf("production NoiseRemovePatchSec = %.4f, want %.4f",
			config.NoiseRemovePatchSec, noiseRemoveProductionPatchSec)
	}
	if config.NoiseRemoveResearchSec != noiseRemoveProductionResearchSec {
		t.Fatalf("production NoiseRemoveResearchSec = %.4f, want %.4f",
			config.NoiseRemoveResearchSec, noiseRemoveProductionResearchSec)
	}
	if config.NoiseRemoveSmooth != noiseRemoveProductionSmooth {
		t.Fatalf("production NoiseRemoveSmooth = %.0f, want %.0f",
			config.NoiseRemoveSmooth, noiseRemoveProductionSmooth)
	}

	fastVariant := findAnlmdnBenchmarkVariant(t, "anlmdn_sr_32000_best_r")
	if got, want := buildAnlmdnBenchmarkVariantSpec(config, fastVariant), config.BuildFilterSpec(); got != want {
		t.Fatalf("production filter spec drifted from anlmdn_sr_32000_best_r\nvariant:    %s\nproduction: %s", got, want)
	}

	expectedOrder := []FilterID{
		FilterDownmix,
		FilterDS201HighPass,
		FilterDS201LowPass,
		FilterNoiseRemove,
		FilterDS201Gate,
		FilterLA2ACompressor,
		FilterDeesser,
		FilterAnalysis,
		FilterResample,
	}
	if len(Pass2FilterOrder) != len(expectedOrder) {
		t.Fatalf("Pass2FilterOrder length = %d, want %d", len(Pass2FilterOrder), len(expectedOrder))
	}
	for i, want := range expectedOrder {
		if Pass2FilterOrder[i] != want {
			t.Fatalf("Pass2FilterOrder[%d] = %q, want %q", i, Pass2FilterOrder[i], want)
		}
	}
}

func TestAnlmdnBenchmarkCaptureRootOptIn(t *testing.T) {
	t.Setenv(anlmdnBenchmarkCaptureEnv, "")
	if root, ok := resolveAnlmdnBenchmarkCaptureRoot(); ok || root != "" {
		t.Fatalf("capture root enabled without %s: root=%q ok=%v", anlmdnBenchmarkCaptureEnv, root, ok)
	}

	t.Setenv(anlmdnBenchmarkCaptureEnv, "1")
	root, ok := resolveAnlmdnBenchmarkCaptureRoot()
	if !ok {
		t.Fatalf("expected capture root when %s is set", anlmdnBenchmarkCaptureEnv)
	}
	if !filepath.IsAbs(root) {
		t.Fatalf("capture root = %q, want absolute path", root)
	}
	if !strings.HasSuffix(filepath.ToSlash(root), anlmdnBenchmarkCaptureRoot) {
		t.Fatalf("capture root = %q, want suffix %q", root, anlmdnBenchmarkCaptureRoot)
	}

	overrideRoot := filepath.Join(t.TempDir(), "capture-root")
	t.Setenv(anlmdnBenchmarkCaptureRootEnv, overrideRoot)
	root, ok = resolveAnlmdnBenchmarkCaptureRoot()
	if !ok {
		t.Fatalf("expected capture root when %s is set", anlmdnBenchmarkCaptureEnv)
	}
	if root != overrideRoot {
		t.Fatalf("capture root override = %q, want %q", root, overrideRoot)
	}

	t.Setenv(anlmdnBenchmarkCaptureEnv, "false")
	if root, ok := resolveAnlmdnBenchmarkCaptureRoot(); ok || root != "" {
		t.Fatalf("capture root enabled for false value: root=%q ok=%v", root, ok)
	}
}

func TestAnlmdnBenchmarkArtifactCaptureSyntheticSmoke(t *testing.T) {
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

	adapted := setupFullbenchAdaptedConfig(t, inputPath)
	variant := findAnlmdnBenchmarkVariant(t, "anlmdn_legacy_default")
	specs := []anlmdnBenchmarkVariantSpec{
		{
			Variant: variant,
			Spec:    buildAnlmdnBenchmarkVariantSpec(adapted.Config, variant),
		},
	}

	root := filepath.Join(t.TempDir(), "anlmdn")
	results := captureAnlmdnBenchmarkArtifacts(t, inputPath, adapted, specs, root)
	if len(results) != 1 {
		t.Fatalf("capture result count = %d, want 1", len(results))
	}

	variantDir := filepath.Join(root, "anlmdn_legacy_default")
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
	if !strings.Contains(string(filterBytes), "anlmdn=") {
		t.Fatalf("filter artefact missing anlmdn: %s", filterBytes)
	}

	metricsBytes, err := os.ReadFile(filepath.Join(variantDir, "metrics.json"))
	if err != nil {
		t.Fatalf("failed to read metrics artefact: %v", err)
	}
	var snapshot anlmdnBenchmarkMetricsSnapshot
	if err := json.Unmarshal(metricsBytes, &snapshot); err != nil {
		t.Fatalf("failed to decode metrics artefact: %v", err)
	}
	if snapshot.VariantName != "anlmdn_legacy_default" {
		t.Fatalf("snapshot variant = %q, want anlmdn_legacy_default", snapshot.VariantName)
	}
	if snapshot.OutputMetadata.SampleRate != 44100 {
		t.Fatalf("snapshot output sample rate = %d, want 44100", snapshot.OutputMetadata.SampleRate)
	}
	if snapshot.OutputMetadata.Channels != 1 {
		t.Fatalf("snapshot output channels = %d, want 1", snapshot.OutputMetadata.Channels)
	}
	if snapshot.Pass2RuntimeMS <= 0 {
		t.Fatalf("snapshot pass2 runtime = %.3f, want positive", snapshot.Pass2RuntimeMS)
	}
	if snapshot.Environment.GoVersion == "" {
		t.Fatal("snapshot environment missing Go version")
	}
	if snapshot.Environment.GOOS == "" {
		t.Fatal("snapshot environment missing GOOS")
	}
	if snapshot.Environment.GOARCH == "" {
		t.Fatal("snapshot environment missing GOARCH")
	}
	if snapshot.Environment.BenchmarkFixturePath == "" || snapshot.Environment.BenchmarkFixturePath == "unavailable" {
		t.Fatalf("snapshot environment fixture path = %q, want recorded path", snapshot.Environment.BenchmarkFixturePath)
	}
	if snapshot.Environment.FfmpegStatigoModuleVersion == "" || snapshot.Environment.FfmpegStatigoModuleVersion == "unavailable" {
		t.Fatalf("snapshot environment ffmpeg-statigo version = %q, want recorded version", snapshot.Environment.FfmpegStatigoModuleVersion)
	}

	reportBytes, err := os.ReadFile(filepath.Join(variantDir, "validation.md"))
	if err != nil {
		t.Fatalf("failed to read validation report: %v", err)
	}
	report := string(reportBytes)
	for _, want := range []string{
		"Pass 2 runtime ms",
		"Room noise reduction",
		"Watery artefacts",
		"Breath tails",
		"Consonant smearing",
		"Hollow tone",
		"Noise pumping between phrases",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("validation report missing %q:\n%s", want, report)
		}
	}
}

func findAnlmdnBenchmarkVariant(tb testing.TB, name string) anlmdnBenchmarkVariant {
	tb.Helper()

	for _, variant := range anlmdnBenchmarkVariants() {
		if variant.Name == name {
			return variant
		}
	}
	tb.Fatalf("missing anlmdn benchmark variant %q", name)
	return anlmdnBenchmarkVariant{}
}

func assertBaseLegacyAnlmdnConfigUnchanged(tb testing.TB, config *FilterChainConfig) {
	tb.Helper()

	if !config.NoiseRemoveEnabled {
		tb.Fatal("base config NoiseRemoveEnabled changed")
	}
	if config.NoiseRemovePreSampleRate != 0 {
		tb.Fatalf("base NoiseRemovePreSampleRate changed: got %d", config.NoiseRemovePreSampleRate)
	}
	if config.NoiseRemoveStrength != noiseRemoveLegacyStrength {
		tb.Fatalf("base NoiseRemoveStrength changed: got %.5f", config.NoiseRemoveStrength)
	}
	if config.NoiseRemovePatchSec != noiseRemoveLegacyPatchSec {
		tb.Fatalf("base NoiseRemovePatchSec changed: got %.4f", config.NoiseRemovePatchSec)
	}
	if config.NoiseRemoveResearchSec != noiseRemoveLegacyResearchSec {
		tb.Fatalf("base NoiseRemoveResearchSec changed: got %.4f", config.NoiseRemoveResearchSec)
	}
	if config.NoiseRemoveSmooth != noiseRemoveLegacySmooth {
		tb.Fatalf("base NoiseRemoveSmooth changed: got %.0f", config.NoiseRemoveSmooth)
	}
}
