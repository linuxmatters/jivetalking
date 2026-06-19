// Package processor handles audio analysis and processing
package processor

import (
	"fmt"
	"math"
	"strings"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// FilterID identifies a filter in the processing chain
type FilterID string

// Filter identifiers for the audio processing chain
const (
	// Infrastructure filters (applied in both passes or pass-specific)
	FilterDownmix  FilterID = "downmix"  // Stereo → mono conversion (both passes)
	FilterAnalysis FilterID = "analysis" // ebur128 + astats + aspectralstats (both passes)
	FilterResample FilterID = "resample" // Output format: 44.1kHz/16-bit/mono (Pass 2 only)

	// Frequency-conscious filtering (Pass 2 only)
	// HP/LP side-chain filtering removes frequency extremes before the gate.
	// Applied to the audio path before the gate for equivalent effect.
	FilterRumbleHighPass   FilterID = "rumble_highpass"   // fixed 80 Hz HP corner (rumble removal)
	FilterBandlimitLowPass FilterID = "bandlimit_lowpass" // #nosec G101 -- FFmpeg filter id, not a credential. Unconditional 20.5 kHz band-limit (ultrasonic rejection).
	FilterSpeechGate       FilterID = "speech_gate"       // soft expander for inter-speech gaps

	// NoiseReduction - anlmdn + afftdn noise reduction (Pass 2 only)
	// Non-Local Means denoiser followed by an FFT spectral denoiser
	FilterNoiseReduction FilterID = "noise_reduction"

	// Processing filters (Pass 2 only)
	FilterLevellingCompressor FilterID = "levelling_compressor" // gentle levelling compressor
	FilterDeesser             FilterID = "deesser"
)

// Pass1FilterOrder defines the filter chain for analysis pass.
// Downmix → Analysis
// No processing filters - just measurement for adaptive processing.
// Silence detection runs in Go using 250ms interval sampling, not in the filter graph.
var Pass1FilterOrder = []FilterID{
	FilterDownmix,
	FilterAnalysis,
}

// Pass2FilterOrder defines the filter chain for processing pass.
// Order rationale:
// - Downmix first: ensures all downstream filters work with mono
// - RumbleHighPass: removes subsonic rumble before other filters
// - BandlimitLowPass: unconditional 20.5 kHz band-limit (removes inaudible ultrasonics)
// - NoiseReduction: primary noise reduction using anlmdn + afftdn
// - SpeechGate: soft expander for inter-speech cleanup (after denoising lowers floor)
// - LevellingCompressor: gentle levelling evens dynamics before normalisation
// - Deesser: after compression (which emphasises sibilance)
// - Analysis: measures output for comparison with Pass 1 (ebur128 upsamples to 192kHz/f64)
// - Resample: standardises output format (44.1kHz/16-bit/mono) - MUST be last
var Pass2FilterOrder = []FilterID{
	FilterDownmix,
	FilterRumbleHighPass,
	FilterBandlimitLowPass,
	FilterNoiseReduction,
	FilterSpeechGate,
	FilterLevellingCompressor,
	FilterDeesser,
	FilterAnalysis,
	FilterResample,
}

// =============================================================================
// Normalisation Constants (Pass 3)
// =============================================================================

// Normalisation target and tolerance for Pass 3 gain adjustment
const (
	// NormTargetLUFS is the podcast loudness standard.
	NormTargetLUFS = -16.0

	// NormToleranceLU is the acceptable deviation from target.
	// ±0.5 LU is industry standard for loudness compliance.
	NormToleranceLU = 0.5
)

// NoiseReduction production defaults (anlmdn parameters; matrix spike defaults at
// .bench/anlmdn-matrix-spike):
//   - s: strength (0.00001 = minimum, kept constant)
//   - p: patch size in seconds (6ms = 0.006s, context window for similarity)
//   - r: research radius in seconds (2.0ms = 0.0020s, r_min)
//   - m: smoothing factor (3 = m_strict)
//
// The matrix spike validated `r_min` (r=0.0020) at native source rate against the
// previous 32 kHz cap path with r=0.0045, confirming a ~35 % Pass 2 speedup at
// metric-equivalent quality. `m_strict` (m=3) was a free quality lever - matched
// cleanup at zero speed cost on both fixtures.
const (
	noiseReductionProductionStrength    = 0.00001
	noiseReductionProductionPatchSec    = 0.0060
	noiseReductionProductionResearchSec = 0.0020
	noiseReductionProductionSmooth      = 3.0
)

const (
	// Rumble high-pass is a FIXED 80 Hz, 12 dB/oct (2-pole Butterworth) corner,
	// applied to every file with no adaptation. 80 Hz clears every measured vocal
	// fundamental with margin (lowest male F0 ~91 Hz; female ~165+ Hz, Anna 188 Hz
	// an octave clear); the 2-pole/12 dB-oct slope is symmetric with the
	// unconditional 20.5 kHz lowpass and removes subsonic rumble before the gate.
	rumbleHPDefaultFreq = 80.0
)

type filterConfigDefaults struct {
	// Orchestration configs are not part of the §8.1 `filters` block (which
	// enumerates the six adaptive filters only); they are pipeline plumbing,
	// excluded from the run record.
	Downmix  DownmixConfig  `json:"-"`
	Analysis AnalysisConfig `json:"-"`
	Resample ResampleConfig `json:"-"`

	RumbleHighPass      RumbleHighPassConfig      `json:"rumble_highpass"`
	BandlimitLowPass    BandlimitLowPassConfig    `json:"bandlimit_lowpass"`
	NoiseReduction      NoiseReductionConfig      `json:"noise_reduction"`
	SpeechGate          SpeechGateConfig          `json:"speech_gate"`
	LevellingCompressor LevellingCompressorConfig `json:"levelling_compressor"`
	Deesser             DeesserConfig             `json:"deesser"`

	Adeclick AdeclickConfig `json:"-"`
	Loudnorm LoudnormConfig `json:"-"`

	// Filter chain order - controls the sequence of filters in the processing chain
	// Use Pass2FilterOrder or customise for experimentation
	FilterOrder []FilterID `json:"-"`
}

type DownmixConfig struct {
	Enabled bool
}

type AnalysisConfig struct {
	Enabled bool
}

type ResampleConfig struct {
	Enabled    bool
	SampleRate int
	Format     string
	FrameSize  int
}

// BiquadFilterConfig holds the shared parameters for a single biquad pole/zero
// filter (RBJ-style highpass/lowpass). The ffmpeg keyword (highpass/lowpass) is
// passed to the builder, not serialised, so both filters keep identical JSON.
type BiquadFilterConfig struct {
	Enabled   bool    `json:"enabled"`
	Frequency float64 `json:"frequency_hz"`
	Poles     int     `json:"poles_count"`
	Width     float64 `json:"width"`
	Mix       float64 `json:"mix"`
	Transform string  `json:"transform"`
}

// RumbleHighPassConfig and BandlimitLowPassConfig are the rumble high-pass and
// band-limit low-pass filter configs. Both are biquad filters with identical
// shape, so they alias the shared BiquadFilterConfig; the parent field names and
// JSON keys differ, the struct does not.
type (
	RumbleHighPassConfig   = BiquadFilterConfig
	BandlimitLowPassConfig = BiquadFilterConfig
)

type NoiseReductionConfig struct {
	Enabled     bool    `json:"enabled"`
	Strength    float64 `json:"strength"`
	PatchSec    float64 `json:"patch_s"`
	ResearchSec float64 `json:"research_s"`
	Smooth      float64 `json:"smooth"`
	// afftdn FFT spectral denoise, appended after anlmdn. Targets broadband
	// noise under speech that anlmdn and the gate do not reach. It replaced the
	// former compand residual-suppression stage, which resolved to its gentlest
	// 4 dB expansion on every stem and added floor pumping. Reduction is fixed
	// (nr=12) and validated; not adaptively tuned, because the noisiest voice
	// must be capped at ~12 to avoid warble.
	AfftdnEnabled        bool    `json:"afftdn_enabled"`
	AfftdnNoiseReduction float64 `json:"afftdn_noise_reduction_db"`
	// AfftdnNoiseType selects afftdn's noise model: "w" (white, the default) or
	// "custom" (a measured spectral shape). On the custom path AfftdnBandNoise
	// carries the per-band relative shape; nf still carries the absolute level and
	// nr the depth, so all three stack.
	AfftdnNoiseType  string `json:"afftdn_noise_type"`
	AfftdnTrackNoise bool   `json:"afftdn_track_noise"`
	// AfftdnNoiseFloor is the static measured noise floor (dB) fed to afftdn's nf
	// parameter. The zero value means unset, so nf= is omitted and afftdn uses its
	// own default; a real floor is always negative. tuneNoiseReduction sets it from
	// the measured Noise.Floor (clamped to afftdn's [-80, -20] range) and turns
	// track_noise off so afftdn holds the static measured floor.
	AfftdnNoiseFloor float64 `json:"afftdn_noise_floor_db"`
	// AfftdnBandNoise is the afftdn custom-profile band shape: up to 15 dB values,
	// "|"-separated, one per fixed band, relative to the band mean (white = all
	// zeros). Emitted as bn= only when AfftdnNoiseType is "custom" and the string is
	// non-empty. Empty on the white path.
	AfftdnBandNoise string `json:"afftdn_band_noise,omitempty"`
}

type SpeechGateConfig struct {
	Enabled bool `json:"enabled"`
	// Threshold and Range are stored as LINEAR amplitudes (FFmpeg agate consumes
	// linear). The §8.4 keys carry the _db suffix because the catalogue (§2.2)
	// documents the conceptual derivation in dB; the emitted number is the linear
	// amplitude, convertible via 20·log10.
	Threshold float64 `json:"threshold_db"`
	Ratio     float64 `json:"ratio"`
	Attack    float64 `json:"attack_ms"`
	Release   float64 `json:"release_ms"`
	Range     float64 `json:"range_db"`
	Knee      float64 `json:"knee"`
	Makeup    float64 `json:"makeup"`
	Detection string  `json:"detection"`
}

type LevellingCompressorConfig struct {
	Enabled   bool    `json:"enabled"`
	Threshold float64 `json:"threshold_db"`
	Ratio     float64 `json:"ratio"`
	Attack    float64 `json:"attack_ms"`
	Release   float64 `json:"release_ms"`
	Makeup    float64 `json:"makeup_db"`
	Knee      float64 `json:"knee"`
	Mix       float64 `json:"mix"`
}

type DeesserConfig struct {
	Enabled bool `json:"enabled"`
	// Intensity (i), Amount (m), Frequency (f) are FFmpeg deesser's 0-1 normalised
	// params, not physical units. Frequency is the split-band corner fraction, NOT
	// Hz, so it stays bare (no _hz suffix).
	Intensity float64 `json:"intensity"`
	Amount    float64 `json:"amount"`
	Frequency float64 `json:"frequency"`
}

type AdeclickConfig struct {
	Enabled   bool
	Threshold float64
	Window    float64
	Overlap   float64
	Method    string
}

type LoudnormConfig struct {
	Enabled   bool
	TargetI   float64
	TargetTP  float64
	TargetLRA float64
	DualMono  bool
	Linear    bool
}

type Decibels float64

func (db Decibels) LinearAmplitude() LinearAmplitude {
	return LinearAmplitude(DbToLinear(float64(db)))
}

func (db Decibels) Float64() float64 {
	return float64(db)
}

type LinearAmplitude float64

func (linear LinearAmplitude) Decibels() Decibels {
	return Decibels(LinearToDb(float64(linear)))
}

func (linear LinearAmplitude) Float64() float64 {
	return float64(linear)
}

// BaseFilterConfig holds caller-owned defaults and user-facing options only.
type BaseFilterConfig struct {
	filterConfigDefaults
	logger debugLogger
}

// AdaptiveDiagnostics holds report-only adaptation explanations.
type AdaptiveDiagnostics struct {
	BandlimitLPReason string `json:"bandlimit_lowpass_reason"`

	SpeechGateDynamicRange        float64 `json:"dynamic_range_db"`
	SpeechGateQuietSpeechEstimate float64 `json:"quiet_speech_estimate_dbfs"`
	SpeechGateSpeechSeparation    float64 `json:"separation_db"`
	SpeechGateSpeechHeadroom      float64 `json:"speech_headroom_db"`
	SpeechGateThresholdUnclamped  float64 `json:"threshold_unclamped_db"`
	SpeechGateClampReason         string  `json:"clamp_reason"`
	// SpeechGateDepthDB is the emitted gate attenuation depth as a positive dB
	// value (the depth calculateSpeechGateRangeDB returns: the fixed moderate
	// depth on a wide gap, the gentler depth on a narrow gap). It surfaces the
	// gate depth to the TUI as a value rather than the former gentle-mode on/off.
	SpeechGateDepthDB float64 `json:"speech_gate_depth_db"`

	// SpeechGateNarrowGap is set when the voiced-anchored threshold cannot clear
	// the loud noise (voiced p10 minus the speech margin sits below noise p95 plus
	// the noise margin). The threshold stays on the speech side; this signal tells
	// the depth step to back off rather than over-gate.
	SpeechGateNarrowGap bool `json:"narrow_gap"`

	// AfftdnEnabled records whether the afftdn FFT denoise tail stays in the chain.
	// tuneNoiseReduction disables it on voice-activated captures.
	AfftdnEnabled bool `json:"afftdn_enabled"`
	// AfftdnNoiseFloorDB is the static measured floor (dB) fed to afftdn's nf when
	// the stage stays enabled; zero when unset.
	AfftdnNoiseFloorDB float64 `json:"afftdn_noise_floor_db"`
	// AfftdnDisableReason names why afftdn was dropped (e.g. "voice_activated"),
	// empty when the stage stays enabled.
	AfftdnDisableReason string `json:"afftdn_disable_reason"`
	// AfftdnNoiseType records the elected afftdn noise model: "w" (white) or
	// "custom" (measured room-tone spectral shape). Empty when afftdn is disabled.
	AfftdnNoiseType string `json:"afftdn_noise_type"`
}

// filterBuilderFunc is a function that builds a filter spec from effective config.
// Returns the FFmpeg filter specification string, or empty string if disabled.
type filterBuilderFunc func(*EffectiveFilterConfig) string

// filterBuilders maps FilterID to its builder function.
// This registry centralises filter spec generation and avoids per-call map allocation.
var filterBuilders = map[FilterID]filterBuilderFunc{
	FilterDownmix:             (*EffectiveFilterConfig).buildDownmixFilter,
	FilterAnalysis:            (*EffectiveFilterConfig).buildAnalysisFilter,
	FilterResample:            (*EffectiveFilterConfig).buildResampleFilter,
	FilterRumbleHighPass:      (*EffectiveFilterConfig).buildRumbleHighpassFilter,
	FilterBandlimitLowPass:    (*EffectiveFilterConfig).buildBandlimitLowPassFilter,
	FilterNoiseReduction:      (*EffectiveFilterConfig).buildNoiseReductionFilter,
	FilterSpeechGate:          (*EffectiveFilterConfig).buildSpeechGateFilter,
	FilterLevellingCompressor: (*EffectiveFilterConfig).buildLevellingCompressorFilter,
	FilterDeesser:             (*EffectiveFilterConfig).buildDeesserFilter,
}

// PassNumber identifies which processing pass is being executed.
type PassNumber int

const (
	PassAnalysis    PassNumber = 1
	PassProcessing  PassNumber = 2
	PassMeasuring   PassNumber = 3
	PassNormalising PassNumber = 4
)

// EffectiveFilterConfig is the per-file filter-builder input.
// It excludes diagnostics and pass execution state.
type EffectiveFilterConfig filterConfigDefaults

// DefaultFilterConfig returns the scientifically-tuned caller-owned defaults for
// podcast spoken word audio processing.
func DefaultFilterConfig() *BaseFilterConfig {
	return &BaseFilterConfig{filterConfigDefaults: defaultFilterConfigDefaults()}
}

// SetLogger installs the debug logger used by the filter chain and all passes
// that consume this config. Callers outside the package pass a bare function so
// they need not name the unexported logger type.
func (cfg *BaseFilterConfig) SetLogger(l func(format string, args ...any)) {
	cfg.logger = debugLogger(l)
}

// CloneForWorker returns a per-worker config that shares no mutable state with
// cfg. It shallow-copies the value, deep-copies the sole reference field
// FilterOrder, and installs the per-worker logger. Concurrent workers may each
// own and process their clone without racing on the base.
func (cfg *BaseFilterConfig) CloneForWorker(logger func(format string, args ...any)) *BaseFilterConfig {
	wc := *cfg
	wc.FilterOrder = cloneFilterOrder(cfg.FilterOrder)
	wc.SetLogger(logger)
	return &wc
}

func defaultFilterConfigDefaults() filterConfigDefaults {
	return assembleFilterDefaults(
		defaultDownmixConfig(),
		defaultAnalysisConfig(),
		defaultResampleConfig(),
		defaultRumbleHighPassConfig(),
		defaultBandlimitLowPassConfig(),
		defaultNoiseReductionConfig(),
		defaultSpeechGateConfig(),
		defaultLevellingCompressorConfig(),
		defaultDeesserConfig(),
		defaultAdeclickConfig(),
		defaultLoudnormConfig(),
	)
}

func assembleFilterDefaults(
	downmix DownmixConfig,
	analysis AnalysisConfig,
	resample ResampleConfig,
	rumbleHighPass RumbleHighPassConfig,
	bandlimitLowPass BandlimitLowPassConfig,
	noiseReduction NoiseReductionConfig,
	speechGate SpeechGateConfig,
	levellingCompressor LevellingCompressorConfig,
	deesser DeesserConfig,
	adeclick AdeclickConfig,
	loudnorm LoudnormConfig,
) filterConfigDefaults {
	return filterConfigDefaults{
		Downmix:             downmix,
		Analysis:            analysis,
		Resample:            resample,
		RumbleHighPass:      rumbleHighPass,
		BandlimitLowPass:    bandlimitLowPass,
		NoiseReduction:      noiseReduction,
		SpeechGate:          speechGate,
		LevellingCompressor: levellingCompressor,
		Deesser:             deesser,
		Adeclick:            adeclick,
		Loudnorm:            loudnorm,

		FilterOrder: Pass2FilterOrder,
	}
}

func defaultDownmixConfig() DownmixConfig {
	return DownmixConfig{Enabled: true}
}

func defaultAnalysisConfig() AnalysisConfig {
	return AnalysisConfig{Enabled: true}
}

func defaultResampleConfig() ResampleConfig {
	return ResampleConfig{
		Enabled:    true,
		SampleRate: 44100,
		Format:     "s16",
		FrameSize:  4096,
	}
}

// defaultBiquadConfig builds a biquad filter config with the shared fixed
// parameters (2-pole, 0.707 Butterworth Q, full wet, tdii transform) and a
// per-filter cutoff frequency.
func defaultBiquadConfig(frequency float64) BiquadFilterConfig {
	return BiquadFilterConfig{
		Enabled:   true,
		Frequency: frequency,
		Poles:     2,
		Width:     0.707,
		Mix:       1.0,
		Transform: "tdii",
	}
}

func defaultRumbleHighPassConfig() RumbleHighPassConfig {
	return defaultBiquadConfig(rumbleHPDefaultFreq)
}

func defaultBandlimitLowPassConfig() BandlimitLowPassConfig {
	return defaultBiquadConfig(20500.0)
}

func defaultNoiseReductionConfig() NoiseReductionConfig {
	return NoiseReductionConfig{
		Enabled:     true,
		Strength:    noiseReductionProductionStrength,
		PatchSec:    noiseReductionProductionPatchSec,
		ResearchSec: noiseReductionProductionResearchSec,
		Smooth:      noiseReductionProductionSmooth,
		// Fixed afftdn FFT denoise tail; nr is not adaptively tuned.
		AfftdnEnabled:        true,
		AfftdnNoiseReduction: 12,
		AfftdnNoiseType:      "w",
		AfftdnTrackNoise:     true,
	}
}

func defaultSpeechGateConfig() SpeechGateConfig {
	return SpeechGateConfig{
		Enabled:   true,
		Threshold: 0.01,
		Ratio:     2.0,
		Attack:    speechGateAttackMS,
		Release:   speechGateReleaseFixedMS,
		// Range is a linear amplitude floor (attenuation), so the fixed 14 dB depth
		// is the negative-dB conversion: Decibels(-14).LinearAmplitude() ~= 0.19953.
		Range:     Decibels(-speechGateDepthFixedDB).LinearAmplitude().Float64(),
		Knee:      speechGateKneeFixed,
		Makeup:    1.0,
		Detection: "rms",
	}
}

func defaultLevellingCompressorConfig() LevellingCompressorConfig {
	return LevellingCompressorConfig{
		Enabled:   true,
		Threshold: -18,
		Ratio:     3.0,
		Attack:    10,
		Release:   200,
		Makeup:    0,
		Knee:      4.0,
		Mix:       1.0,
	}
}

func defaultDeesserConfig() DeesserConfig {
	return DeesserConfig{
		Enabled:   true,
		Intensity: 0.0,
		Amount:    0.50, // m: ~12 dB max-cut cap (af_deesser.c maxdess; depth cap, not band)
		Frequency: 0.80, // f: corner ~7.5 kHz → acts on sibilant band, not presence (was 0.5 = ~2 kHz)
	}
}

func defaultAdeclickConfig() AdeclickConfig {
	return AdeclickConfig{
		Enabled:   true,
		Threshold: 1.7,
		Window:    55.0,
		Overlap:   50.0,
		Method:    "s",
	}
}

func defaultLoudnormConfig() LoudnormConfig {
	return LoudnormConfig{
		Enabled:   true,
		TargetI:   -16.0,
		TargetTP:  -1.0,
		TargetLRA: 20.0,
		DualMono:  true,
		Linear:    true,
	}
}

func DefaultEffectiveFilterConfig() *EffectiveFilterConfig {
	return deriveEffectiveFilterConfig(DefaultFilterConfig())
}

func deriveEffectiveFilterConfig(base *BaseFilterConfig) *EffectiveFilterConfig {
	return assembleEffectiveFilterConfig(base, deriveAdaptiveFilterResult(base))
}

func deriveAdaptiveFilterResult(base *BaseFilterConfig) *filterConfigDefaults {
	if base == nil {
		return nil
	}

	defaults := cloneFilterDefaults(&base.filterConfigDefaults)
	return &defaults
}

func assembleEffectiveFilterConfig(base *BaseFilterConfig, adaptive *filterConfigDefaults) *EffectiveFilterConfig {
	if base == nil {
		return nil
	}

	effective := &EffectiveFilterConfig{}
	copyFilterDefaults(effective, &base.filterConfigDefaults)
	if adaptive != nil {
		copyFilterDefaults(effective, adaptive)
	}
	effective.FilterOrder = cloneFilterOrder(base.FilterOrder)

	return effective
}

func cloneFilterDefaults(src *filterConfigDefaults) filterConfigDefaults {
	if src == nil {
		return filterConfigDefaults{}
	}
	dst := *src
	dst.FilterOrder = cloneFilterOrder(src.FilterOrder)
	return dst
}

func cloneFilterOrder(order []FilterID) []FilterID {
	if order == nil {
		return nil
	}
	return append([]FilterID(nil), order...)
}

func copyFilterDefaults(dst *EffectiveFilterConfig, src *filterConfigDefaults) {
	if dst == nil {
		return
	}
	*dst = EffectiveFilterConfig(cloneFilterDefaults(src))
}

// DbToLinear converts decibel value to linear amplitude.
// Used for converting dB parameters to FFmpeg's linear format.
func DbToLinear(db float64) float64 {
	return math.Pow(10, db/20.0)
}

// LinearToDb converts linear amplitude to decibel value.
// Inverse of DbToLinear.
func LinearToDb(linear float64) float64 {
	if linear <= 0 {
		return -120.0 // Practical floor for audio
	}
	return 20.0 * math.Log10(linear)
}

// buildDownmixFilter builds the stereo-to-mono downmix filter specification.
// Uses FFmpeg's built-in channel layout conversion which handles various input
// configurations (stereo, mono, single-channel recordings) correctly.
func (cfg *EffectiveFilterConfig) buildDownmixFilter() string {
	downmix := cfg.Downmix
	if !downmix.Enabled {
		return ""
	}
	// aformat with channel_layouts=mono uses FFmpeg's standard downmix matrix
	// which handles stereo, mono, and single-channel recordings appropriately
	return "aformat=channel_layouts=mono"
}

// Shared analysis-filter segments. Pass 2 (buildAnalysisFilter) and the Pass-4
// chain (buildNormalisationFilters) both measure the signal with the same
// astats and aspectralstats settings; these constants are the single source so
// the two passes cannot drift. The ebur128 stage differs by one option (Pass 2
// appends target=<TargetI>, Pass 4 leaves the default), so it shares only the
// common prefix below. The metric catalogue is documented on buildAnalysisFilter.
const (
	astatsAnalysisSpec         = "astats=metadata=1:measure_perchannel=all"
	aspectralstatsAnalysisSpec = "aspectralstats=win_size=2048:win_func=hann:measure=all"
	ebur128AnalysisSpecPrefix  = "ebur128=metadata=1:peak=sample+true:dualmono=true"
)

// buildAnalysisFilter builds the audio analysis filter chain.
// Combines astats, aspectralstats, and ebur128 for comprehensive measurement.
// Used in both Pass 1 (input analysis) and Pass 2 (output analysis).
//
// Filter order: astats → aspectralstats → ebur128
// The ebur128 filter is placed last because it upsamples to 192 kHz internally and outputs f64,
// which would skew spectral measurements if placed first. astats and aspectralstats
// measure the original signal format, then ebur128 does its own internal upsampling
// for accurate true peak detection without affecting other measurements.
//
// NOTE: loudnorm is NOT included here because it has no "measure only" mode.
// It always processes/normalises audio. Loudnorm measurement for Pass 3 is done
// separately via measureWithLoudnorm() which reads the processed file without
// encoding output.
func (cfg *EffectiveFilterConfig) buildAnalysisFilter() string {
	analysis := cfg.Analysis
	if !analysis.Enabled {
		return ""
	}
	// astats: provides noise floor, dynamic range, and additional measurements for adaptive processing:
	//   - Noise_floor, Dynamic_range, RMS_level, Peak_level: core measurements
	//   - DC_offset: detects DC bias needing removal
	//   - Flat_factor: detects pre-existing clipping/limiting
	//   - Zero_crossings_rate: helps classify noise type
	//   - Max_difference: detects impulsive sounds (clicks/pops)
	// Note: reset=0 (default) allows astats to accumulate statistics across all frames
	// for whole-file measurements. Per-interval RMS is calculated directly from frame
	// samples in Go for accurate silence detection.
	// aspectralstats: comprehensive spectral analysis (measured every run; not every
	// metric drives an adaptation - de-esser intensity keys off speech-region band
	// RMS, not centroid/rolloff, and the compressor threshold is speech-RMS-relative,
	// not crest-driven). Metrics measured:
	//   - centroid: spectral brightness (Hz)
	//   - spread: spectral bandwidth - voice fullness indicator
	//   - skewness: spectral asymmetry - positive=bright, negative=dark
	//   - kurtosis: spectral peakiness - tonal vs broadband content
	//   - entropy: spectral randomness - noise classification
	//   - flatness: noise vs tonal ratio (0-1) - noise type detection
	//   - crest: spectral peak-to-RMS - transient indicator
	//   - rolloff: high-frequency energy point
	//   - variance: spectral energy variation - dynamic content indicator
	//   - mean, slope, decrease: additional spectral shape descriptors
	// ebur128: provides integrated loudness (LUFS), true peak, sample peak, and LRA via metadata
	//   Upsamples to 192kHz internally for accurate true peak detection
	//   metadata=1 writes per-frame loudness data to frame metadata (lavfi.r128.* keys)
	//   peak=sample+true enables both sample peak and true peak measurement
	//   (required for lavfi.r128.sample_peak and lavfi.r128.true_peak metadata)
	//   dualmono=true: CRITICAL - treats mono as dual-mono for correct loudness measurement
	//   (mono without dualmono is measured ~3 LU quieter than intended)
	// Note: astats measure_perchannel=all requests all available per-channel statistics
	//
	// IMPORTANT: loudnorm is NOT included here, even for Pass 2, because loudnorm
	// has no "measure only" mode. It always processes/normalises audio. Loudnorm
	// measurement for Pass 3 is done separately via measureWithLoudnorm() which
	// reads the file without encoding output.
	return fmt.Sprintf(
		"%s,%s,%s:target=%.0f",
		astatsAnalysisSpec,
		aspectralstatsAnalysisSpec,
		ebur128AnalysisSpecPrefix,
		cfg.Loudnorm.TargetI)
}

// buildResampleFilter builds the output format standardisation filter.
// Ensures consistent output: 44.1kHz, 16-bit, mono, fixed frame size.
// Pass 2 only - applied after all processing and analysis.
func (cfg *EffectiveFilterConfig) buildResampleFilter() string {
	resample := cfg.Resample
	if !resample.Enabled {
		return ""
	}
	return cfg.buildRequiredOutputFormatFilter()
}

// buildRequiredOutputFormatFilter builds the mandatory output format filter.
// Use this when a pass must restore encoder-compatible audio regardless of
// Resample.Enabled.
func (cfg *EffectiveFilterConfig) buildRequiredOutputFormatFilter() string {
	resample := cfg.Resample
	return fmt.Sprintf("aformat=sample_rates=%d:channel_layouts=mono:sample_fmts=%s,asetnsamples=n=%d",
		resample.SampleRate, resample.Format, resample.FrameSize)
}

// buildRumbleHighpassFilter builds the rumble high-pass filter.
// Removes subsonic rumble (HVAC, handling noise, etc.) before gating.
//
// Frequency-conscious gating uses side-chain HP/LP filters to prevent false
// triggers. Since FFmpeg doesn't support side-chain filtering, we apply frequency
// filtering to the audio path before gating to achieve the same effect.
//
// Parameters:
// - frequency: cutoff frequency in Hz (fixed 80 Hz)
// - poles: 1=6dB/oct (gentle), 2=12dB/oct (standard, fixed)
// - width: Q factor (0.707=Butterworth, fixed)
// - transform: filter algorithm (tdii=best floating-point accuracy)
// - mix: wet/dry blend (1.0=full filter, fixed)
func (cfg *EffectiveFilterConfig) buildRumbleHighpassFilter() string {
	return buildBiquadFilter(cfg.RumbleHighPass, "highpass")
}

// buildBiquadFilter renders the shared biquad highpass/lowpass filter spec. The
// keyword ("highpass"/"lowpass") selects the ffmpeg filter; every other byte of
// the emitted string is identical between the two filters, so they share one
// builder. Returns empty string when the filter is disabled.
//
// Parameters:
// - f: cutoff frequency in Hz
// - poles: 1=6dB/oct (gentle), 2=12dB/oct (standard, default)
// - width: Q factor (0.707=Butterworth, default)
// - transform: filter algorithm (tdii=best floating-point accuracy)
// - mix: wet/dry blend (1.0=full filter)
func buildBiquadFilter(cfg BiquadFilterConfig, keyword string) string {
	if !cfg.Enabled {
		return ""
	}

	poles := cfg.Poles
	if poles < 1 {
		poles = 2 // Default to standard 12dB/oct
	}

	width := cfg.Width
	if width <= 0 {
		width = 0.707 // Butterworth default
	}

	spec := fmt.Sprintf("%s=f=%.0f:poles=%d:width_type=q:width=%.3f:normalize=1",
		keyword, cfg.Frequency, poles, width)

	// Add transform type if specified (tdii = best floating-point accuracy)
	if cfg.Transform != "" {
		spec += fmt.Sprintf(":a=%s", cfg.Transform)
	}

	// Add mix parameter if not full wet (for subtle application)
	if cfg.Mix > 0 && cfg.Mix < 1.0 {
		spec += fmt.Sprintf(":m=%.2f", cfg.Mix)
	}

	return spec
}

// buildBandlimitLowPassFilter builds the band-limit low-pass filter specification.
// Part of the frequency-conscious filtering chain, placed after highpass.
//
// Purpose: unconditional 20.5 kHz band-limit on all content, giving downstream
// lossy encoders a consistent bandwidth and removing inaudible ultrasonics before
// the gate. Non-adaptive (no content detection); the frequency-conscious side-chain
// filter is applied here to the audio path.
//
// Parameters:
// - f: cutoff frequency (removes frequencies above this)
// - poles: 1=6dB/oct (gentle), 2=12dB/oct (standard)
// - width: Q factor (0.707=Butterworth for maximally flat passband)
// - transform: filter algorithm (tdii=best floating-point accuracy)
// - mix: wet/dry blend (1.0=full filter)
//
// Returns empty string if BandlimitLowPass.Enabled is false.
func (cfg *EffectiveFilterConfig) buildBandlimitLowPassFilter() string {
	return buildBiquadFilter(cfg.BandlimitLowPass, "lowpass")
}

// buildNoiseReductionFilter builds the anlmdn+afftdn noise reduction filter.
// Non-Local Means denoiser followed by an FFT spectral denoiser.
// Runs at the source sample rate; downstream filters (gate, levelling compressor,
// de-esser, analysis) operate at the same rate.
//
// anlmdn s/p/r/m values and their rationale: see the noise-reduction constant block.
//
// afftdn replaced the former compand residual-suppression stage. Sweeps on the
// noisiest corpus stem showed anlmdn → afftdn matches
// or beats anlmdn → compand on under-speech noise while keeping gaps clean with
// less floor modulation. nr is FIXED at 12: a per-presenter sweep showed the
// noisiest voice must be capped at ~12 to avoid warble, so adaptive nr would be
// counter-productive.
func (cfg *EffectiveFilterConfig) buildNoiseReductionFilter() string {
	noiseReduction := cfg.NoiseReduction
	if !noiseReduction.Enabled {
		return ""
	}

	filters := make([]string, 0, 2)
	filters = append(filters, fmt.Sprintf("anlmdn=s=%.5f:p=%.4f:r=%.4f:m=%.0f",
		noiseReduction.Strength,
		noiseReduction.PatchSec,
		noiseReduction.ResearchSec,
		noiseReduction.Smooth,
	))

	// afftdn FFT spectral denoise tail, validated on the noisiest corpus stem.
	// Fixed nr=12 (not adaptive); tn=1 tracks noise so no sample region is needed.
	if spec := noiseReduction.buildAfftdnFilter(); spec != "" {
		filters = append(filters, spec)
	}

	return strings.Join(filters, ",")
}

// buildAfftdnFilter builds the afftdn FFT spectral denoise tail of the noise block.
// Returns empty string when afftdn is disabled. Shared by buildNoiseReductionFilter and
// the ablation benchmark so the benchmark cannot drift from the production spec.
func (cfg *NoiseReductionConfig) buildAfftdnFilter() string {
	if !cfg.AfftdnEnabled {
		return ""
	}
	tn := 0
	if cfg.AfftdnTrackNoise {
		tn = 1
	}
	// On the custom path emit nt=custom:bn=<shape>; bn carries the spectral shape
	// while nf (below) still carries the absolute level and nr the depth. The
	// white path keeps the bare nt=w. sanitizeNoiseReductionConfig reverts a
	// "custom" type with no shape to "w", so bn is always present here when custom.
	var spec string
	if cfg.AfftdnNoiseType == "custom" && cfg.AfftdnBandNoise != "" {
		spec = fmt.Sprintf("afftdn=nr=%g:nt=custom:bn=%s:tn=%d",
			cfg.AfftdnNoiseReduction,
			cfg.AfftdnBandNoise,
			tn,
		)
	} else {
		spec = fmt.Sprintf("afftdn=nr=%g:nt=%s:tn=%d",
			cfg.AfftdnNoiseReduction,
			cfg.AfftdnNoiseType,
			tn,
		)
	}
	// Emit nf only when set (a real floor is negative); zero means unset.
	if cfg.AfftdnNoiseFloor < 0 {
		spec += fmt.Sprintf(":nf=%g", cfg.AfftdnNoiseFloor)
	}
	return spec
}

// buildSpeechGateFilter builds the speech gate filter specification.
// Uses a soft expander approach (1.5:1-2.0:1 ratio) rather than a hard gate for natural speech.
// tuneSpeechGate in adaptive_speech_gate.go adapts threshold and range to Pass 1
// measurements; ratio is LRA-based; attack, release, knee, and detection are fixed.
// Detection is RMS (safe for speech and tonal bleed); an empty Detection field
// defaults to RMS here.
func (cfg *EffectiveFilterConfig) buildSpeechGateFilter() string {
	gate := cfg.SpeechGate
	if !gate.Enabled {
		return ""
	}
	detection := gate.Detection
	if detection == "" {
		// Kept as a live guard: callers that build a bare SpeechGateConfig (no
		// adaptation, no defaultSpeechGateConfig) leave Detection empty, so this
		// fills in rms. There is no peak branch to remove.
		detection = "rms"
	}
	// Note: attack/release use %.2f to support sub-millisecond values (0.5ms minimum)
	return fmt.Sprintf(
		"agate=threshold=%.6f:ratio=%.1f:attack=%.2f:release=%.0f:"+
			"range=%.4f:knee=%.1f:detection=%s:makeup=%.1f",
		gate.Threshold,
		gate.Ratio,
		gate.Attack,
		gate.Release,
		gate.Range,
		gate.Knee,
		detection,
		gate.Makeup,
	)
}

// buildLevellingCompressorFilter builds the levelling compressor filter specification.
// Uses FFmpeg's acompressor with settings tuned for gentle, programme-dependent
// levelling.
// Converts dB values to linear for FFmpeg's format.
func (cfg *EffectiveFilterConfig) buildLevellingCompressorFilter() string {
	levellingCompressor := cfg.LevellingCompressor
	if !levellingCompressor.Enabled {
		return ""
	}
	return fmt.Sprintf(
		"acompressor=threshold=%.6f:ratio=%.1f:attack=%.0f:release=%.0f:"+
			"makeup=%.2f:knee=%.1f:detection=rms:mix=%.2f",
		Decibels(levellingCompressor.Threshold).LinearAmplitude().Float64(),
		levellingCompressor.Ratio,
		levellingCompressor.Attack,
		levellingCompressor.Release,
		Decibels(levellingCompressor.Makeup).LinearAmplitude().Float64(),
		levellingCompressor.Knee,
		levellingCompressor.Mix,
	)
}

// buildDeesserFilter builds the deesser filter specification.
// Automatically detects and reduces harsh sibilance ("s" sounds).
// Returns empty string if disabled or intensity is 0.
func (cfg *EffectiveFilterConfig) buildDeesserFilter() string {
	deesser := cfg.Deesser
	if !deesser.Enabled || deesser.Intensity <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"deesser=i=%.2f:m=%.2f:f=%.2f",
		deesser.Intensity,
		deesser.Amount,
		deesser.Frequency,
	)
}

// buildAdeclickFilter builds the click/pop repair filter specification.
// Uses interpolation to repair waveform discontinuities.
// Applied in Pass 4 after loudnorm to catch clicks from limiter and gain changes.
//
// Production defaults are t=1.7, w=55, o=50, m=s (spline) for a ~75% Pass 4
// runtime cut at metric-parity quality; the gentle limiter attack keeps source
// clicks below the relaxed threshold.
//
// Parameters (valid ranges):
// - t (threshold): Detection sensitivity (0.1-8.0, lower=more sensitive)
// - w (window): Analysis window in ms (10-100)
// - o (overlap): Window overlap percentage (50-95)
// - m (method): interpolation method (a=autoregression, s=spline)
func (cfg *EffectiveFilterConfig) buildAdeclickFilter() string {
	adeclick := cfg.Adeclick
	if !adeclick.Enabled {
		return ""
	}
	spec := fmt.Sprintf(
		"adeclick=t=%.1f:w=%.0f:o=%.0f",
		adeclick.Threshold,
		adeclick.Window,
		adeclick.Overlap,
	)
	if adeclick.Method != "" {
		spec += ":m=" + adeclick.Method
	}
	return spec
}

// BuildFilterSpec builds the FFmpeg filter specification string for Pass 2 processing.
// Filter order is determined by cfg.FilterOrder (or Pass2FilterOrder if empty).
// Each filter checks its Enabled flag and returns empty string if disabled.
// Uses the package-level filterBuilders registry for filter spec generation.
func (cfg *EffectiveFilterConfig) BuildFilterSpec() string {
	if cfg == nil {
		return ""
	}
	// Use configured order or default
	order := cfg.FilterOrder
	if len(order) == 0 {
		order = Pass2FilterOrder
	}

	// Build filters in specified order, skipping disabled/empty
	var filters []string
	for _, id := range order {
		if builder, ok := filterBuilders[id]; ok {
			if spec := builder(cfg); spec != "" {
				filters = append(filters, spec)
			}
		}
	}

	return strings.Join(filters, ",")
}

// CreateProcessingFilterGraph creates an AVFilterGraph for complete audio processing
// This is used in Pass 2 to apply the full filter chain.
func CreateProcessingFilterGraph(
	decCtx *ffmpeg.AVCodecContext,
	config *EffectiveFilterConfig,
) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
	return setupFilterGraph(decCtx, config.BuildFilterSpec())
}
