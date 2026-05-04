// Package processor handles audio analysis and processing
package processor

import (
	"fmt"
	"math"
	"strings"
	"time"

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

	// DS201-inspired frequency-conscious filtering (Pass 2 only)
	// Drawmer DS201 pioneered HP/LP side-chain filtering for frequency-conscious gating.
	// We apply these filters to the audio path before the gate for equivalent effect.
	FilterDS201HighPass FilterID = "ds201_highpass" // HP + hum notch composite (part of DS201 side-chain)
	FilterDS201LowPass  FilterID = "ds201_lowpass"  // LP for ultrasonic rejection (adaptive)
	FilterDS201Gate     FilterID = "ds201_gate"     // Soft expander inspired by DS201

	// NoiseRemove - anlmdn + compand noise reduction (Pass 2 only)
	// Non-Local Means denoiser with a compand for residual suppression
	FilterNoiseRemove FilterID = "noiseremove"

	// Processing filters (Pass 2 only)
	FilterLA2ACompressor FilterID = "la2a_compressor" // Teletronix LA-2A style optical compressor
	FilterDeesser        FilterID = "deesser"
)

// Pass1FilterOrder defines the filter chain for analysis pass.
// Downmix → Analysis
// No processing filters - just measurement for adaptive processing.
// Silence detection is now performed in Go using 250ms interval sampling.
var Pass1FilterOrder = []FilterID{
	FilterDownmix,
	FilterAnalysis,
}

// Pass2FilterOrder defines the filter chain for processing pass.
// Order rationale:
// - Downmix first: ensures all downstream filters work with mono
// - DS201HighPass: removes subsonic rumble before other filters
// - DS201LowPass: removes ultrasonic content that could trigger false gates (adaptive)
// - NoiseRemove: primary noise reduction using anlmdn + compand
// - DS201Gate: soft expander for inter-speech cleanup (after denoising lowers floor)
// - LA2ACompressor: LA-2A style optical compression evens dynamics before normalisation
// - Deesser: after compression (which emphasises sibilance)
// - Analysis: measures output for comparison with Pass 1 (ebur128 upsamples to 192kHz/f64)
// - Resample: standardises output format (44.1kHz/16-bit/mono) - MUST be last
var Pass2FilterOrder = []FilterID{
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

// NoiseRemove production defaults.
//
// The matrix spike at .bench/anlmdn-matrix-spike validated `r_min` (r=0.0020)
// at native source rate against the previous 32 kHz cap path with r=0.0045,
// confirming a ~35 % Pass 2 speedup at metric-equivalent quality. `m_strict`
// (m=3) was a free quality lever - matched cleanup at zero speed cost on both
// fixtures.
const (
	noiseRemoveProductionStrength    = 0.00001
	noiseRemoveProductionPatchSec    = 0.0060
	noiseRemoveProductionResearchSec = 0.0020
	noiseRemoveProductionSmooth      = 3.0
)

const (
	ds201HPDefaultPoles     = 2
	ds201HPDefaultWidth     = 0.707
	ds201HPDefaultMix       = 1.0
	ds201HPDefaultTransform = "tdii"
)

type filterConfigDefaults struct {
	Downmix       DownmixConfig
	Analysis      AnalysisConfig
	Resample      ResampleConfig
	DS201HighPass DS201HighPassConfig
	DS201LowPass  DS201LowPassConfig
	NoiseRemove   NoiseRemoveConfig
	DS201Gate     DS201GateConfig
	LA2A          LA2AConfig
	Deesser       DeesserConfig
	Adeclick      AdeclickConfig
	Loudnorm      LoudnormConfig

	// Filter chain order - controls the sequence of filters in the processing chain
	// Use Pass2FilterOrder or customise for experimentation
	FilterOrder []FilterID
}

type DownmixConfig struct {
	Enabled bool
}

type AnalysisConfig struct {
	Enabled             bool
	SilenceScanDuration time.Duration
}

type ResampleConfig struct {
	Enabled    bool
	SampleRate int
	Format     string
	FrameSize  int
}

type DS201HighPassConfig struct {
	Enabled   bool
	Frequency float64
	Poles     int
	Width     float64
	Mix       float64
	Transform string
}

type DS201LowPassConfig struct {
	Enabled   bool
	Frequency float64
	Poles     int
	Width     float64
	Mix       float64
	Transform string
}

type NoiseRemoveConfig struct {
	Enabled          bool
	CompandEnabled   bool
	Strength         float64
	PatchSec         float64
	ResearchSec      float64
	Smooth           float64
	CompandThreshold float64
	CompandExpansion float64
	CompandAttack    float64
	CompandDecay     float64
	CompandKnee      float64
}

type DS201GateConfig struct {
	Enabled   bool
	Threshold float64
	Ratio     float64
	Attack    float64
	Release   float64
	Range     float64
	Knee      float64
	Makeup    float64
	Detection string
}

type LA2AConfig struct {
	Enabled   bool
	Threshold float64
	Ratio     float64
	Attack    float64
	Release   float64
	Makeup    float64
	Knee      float64
	Mix       float64
}

type DeesserConfig struct {
	Enabled   bool
	Intensity float64
	Amount    float64
	Frequency float64
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
}

// AdaptiveFilterResult holds a complete per-file set of tunable filter values.
// It excludes diagnostics and pass execution state.
type AdaptiveFilterResult struct {
	filterConfigDefaults
}

// AdaptiveDiagnostics holds report-only adaptation explanations.
type AdaptiveDiagnostics struct {
	DS201LPContentType  ContentType
	DS201LPReason       string
	DS201LPRolloffRatio float64

	DS201GateAggression          float64
	DS201GateDynamicRange        float64
	DS201GateQuietSpeechEstimate float64
	DS201GateSpeechSeparation    float64
	DS201GateSpeechHeadroom      float64
	DS201GateThresholdUnclamped  float64
	DS201GateClampReason         string
	DS201GateGentleMode          bool

	LA2AHighCrestActive      bool
	LA2AHighCrestDeficit     float64
	LA2AHighCrestSeverity    float64
	LA2AHighCrestProjectedTP float64
}

// ProcessingFilterContext holds pass execution state outside caller-owned defaults.
type ProcessingFilterContext struct {
	Pass         PassNumber
	Measurements *AudioMeasurements
}

// filterBuilderFunc is a function that builds a filter spec from effective config.
// Returns the FFmpeg filter specification string, or empty string if disabled.
type filterBuilderFunc func(*EffectiveFilterConfig) string

// filterBuilders maps FilterID to its builder function.
// This registry centralises filter spec generation and avoids per-call map allocation.
var filterBuilders = map[FilterID]filterBuilderFunc{
	FilterDownmix:        (*EffectiveFilterConfig).buildDownmixFilter,
	FilterAnalysis:       (*EffectiveFilterConfig).buildAnalysisFilter,
	FilterResample:       (*EffectiveFilterConfig).buildResampleFilter,
	FilterDS201HighPass:  (*EffectiveFilterConfig).buildDS201HighpassFilter,
	FilterDS201LowPass:   (*EffectiveFilterConfig).buildDS201LowPassFilter,
	FilterNoiseRemove:    (*EffectiveFilterConfig).buildNoiseRemoveFilter,
	FilterDS201Gate:      (*EffectiveFilterConfig).buildDS201GateFilter,
	FilterLA2ACompressor: (*EffectiveFilterConfig).buildLA2ACompressorFilter,
	FilterDeesser:        (*EffectiveFilterConfig).buildDeesserFilter,
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

func defaultFilterConfigDefaults() filterConfigDefaults {
	return assembleFilterDefaults(
		defaultDownmixConfig(),
		defaultAnalysisConfig(),
		defaultResampleConfig(),
		defaultDS201HighPassConfig(),
		defaultDS201LowPassConfig(),
		defaultNoiseRemoveConfig(),
		defaultDS201GateConfig(),
		defaultLA2AConfig(),
		defaultDeesserConfig(),
		defaultAdeclickConfig(),
		defaultLoudnormConfig(),
	)
}

func assembleFilterDefaults(
	downmix DownmixConfig,
	analysis AnalysisConfig,
	resample ResampleConfig,
	ds201HighPass DS201HighPassConfig,
	ds201LowPass DS201LowPassConfig,
	noiseRemove NoiseRemoveConfig,
	ds201Gate DS201GateConfig,
	la2a LA2AConfig,
	deesser DeesserConfig,
	adeclick AdeclickConfig,
	loudnorm LoudnormConfig,
) filterConfigDefaults {
	return filterConfigDefaults{
		Downmix:       downmix,
		Analysis:      analysis,
		Resample:      resample,
		DS201HighPass: ds201HighPass,
		DS201LowPass:  ds201LowPass,
		NoiseRemove:   noiseRemove,
		DS201Gate:     ds201Gate,
		LA2A:          la2a,
		Deesser:       deesser,
		Adeclick:      adeclick,
		Loudnorm:      loudnorm,

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

func defaultDS201HighPassConfig() DS201HighPassConfig {
	return DS201HighPassConfig{
		Enabled:   true,
		Frequency: 80.0,
		Poles:     ds201HPDefaultPoles,
		Width:     ds201HPDefaultWidth,
		Mix:       ds201HPDefaultMix,
		Transform: ds201HPDefaultTransform,
	}
}

func defaultDS201LowPassConfig() DS201LowPassConfig {
	return DS201LowPassConfig{
		Enabled:   true,
		Frequency: 16000.0,
		Poles:     2,
		Width:     0.707,
		Mix:       1.0,
		Transform: "tdii",
	}
}

func defaultNoiseRemoveConfig() NoiseRemoveConfig {
	return NoiseRemoveConfig{
		Enabled:          true,
		CompandEnabled:   true,
		Strength:         noiseRemoveProductionStrength,
		PatchSec:         noiseRemoveProductionPatchSec,
		ResearchSec:      noiseRemoveProductionResearchSec,
		Smooth:           noiseRemoveProductionSmooth,
		CompandThreshold: -55.0,
		CompandExpansion: 6.0,
		CompandAttack:    0.005,
		CompandDecay:     0.100,
		CompandKnee:      6.0,
	}
}

func defaultDS201GateConfig() DS201GateConfig {
	return DS201GateConfig{
		Enabled:   true,
		Threshold: 0.01,
		Ratio:     2.0,
		Attack:    12,
		Release:   350,
		Range:     0.0625,
		Knee:      3.0,
		Makeup:    1.0,
		Detection: "rms",
	}
}

func defaultLA2AConfig() LA2AConfig {
	return LA2AConfig{
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
		Amount:    0.5,
		Frequency: 0.5,
	}
}

func defaultAdeclickConfig() AdeclickConfig {
	return AdeclickConfig{
		Enabled:   true,
		Threshold: 2.0,
		Window:    55.0,
		Overlap:   50.0,
		Method:    "s",
	}
}

func defaultLoudnormConfig() LoudnormConfig {
	return LoudnormConfig{
		Enabled:   true,
		TargetI:   -16.0,
		TargetTP:  -2.0,
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

func deriveAdaptiveFilterResult(base *BaseFilterConfig) *AdaptiveFilterResult {
	if base == nil {
		return nil
	}

	defaults := cloneFilterDefaults(&base.filterConfigDefaults)
	return &AdaptiveFilterResult{filterConfigDefaults: defaults}
}

func assembleEffectiveFilterConfig(base *BaseFilterConfig, adaptive *AdaptiveFilterResult) *EffectiveFilterConfig {
	if base == nil {
		return nil
	}

	effective := &EffectiveFilterConfig{}
	copyFilterDefaults(effective, &base.filterConfigDefaults)
	if adaptive != nil {
		copyFilterDefaults(effective, &adaptive.filterConfigDefaults)
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
// NOTE: loudnorm is NOT included here because it has no "measure only" mode -
// it always processes/normalizes audio. Loudnorm measurement for Pass 3 is done
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
	// aspectralstats: comprehensive spectral analysis for adaptive filter tuning
	//   - centroid: spectral brightness (Hz) - informs highpass freq and de-esser
	//   - spread: spectral bandwidth - voice fullness indicator
	//   - skewness: spectral asymmetry - positive=bright, negative=dark
	//   - kurtosis: spectral peakiness - tonal vs broadband content
	//   - entropy: spectral randomness - noise classification
	//   - flatness: noise vs tonal ratio (0-1) - noise type detection
	//   - crest: spectral peak-to-RMS - transient indicator for compressor
	//   - rolloff: high-frequency energy point - de-esser intensity
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
	// doesn't have a "measure only" mode - it always processes/normalizes audio.
	// Loudnorm measurement for Pass 3 is done separately via measureWithLoudnorm()
	// which reads the file without encoding output.
	return fmt.Sprintf(
		"astats=metadata=1:measure_perchannel=all,"+
			"aspectralstats=win_size=2048:win_func=hann:measure=all,"+
			"ebur128=metadata=1:peak=sample+true:dualmono=true:target=%.0f",
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

// buildDS201HighpassFilter builds the DS201-inspired high-pass filter.
// Removes subsonic rumble (HVAC, handling noise, etc.) before gating.
//
// The DS201's frequency-conscious gating uses side-chain HP/LP filters to prevent
// false triggers. Since FFmpeg doesn't support side-chain filtering, we apply
// frequency filtering to the audio path before gating to achieve the same effect.
//
// Parameters:
// - frequency: cutoff frequency in Hz (adaptive: 60-120Hz based on voice)
// - poles: 1=6dB/oct (gentle), 2=12dB/oct (standard)
// - width: Q factor (0.707=Butterworth, lower=gentler for warm voices)
// - transform: filter algorithm (tdii=best floating-point accuracy)
// - mix: wet/dry blend (1.0=full filter, 0.7=subtle for warm voices)
func (cfg *EffectiveFilterConfig) buildDS201HighpassFilter() string {
	highpass := cfg.DS201HighPass
	if !highpass.Enabled {
		return ""
	}

	poles := highpass.Poles
	if poles < 1 {
		poles = 2 // Default to standard 12dB/oct
	}

	width := highpass.Width
	if width <= 0 {
		width = 0.707 // Butterworth default
	}

	hpSpec := fmt.Sprintf("highpass=f=%.0f:poles=%d:width_type=q:width=%.3f:normalize=1",
		highpass.Frequency, poles, width)

	// Add transform type if specified (tdii = best floating-point accuracy)
	if highpass.Transform != "" {
		hpSpec += fmt.Sprintf(":a=%s", highpass.Transform)
	}

	// Add mix parameter if not full wet (for warm voice protection)
	if highpass.Mix > 0 && highpass.Mix < 1.0 {
		hpSpec += fmt.Sprintf(":m=%.2f", highpass.Mix)
	}

	return hpSpec
}

// buildDS201LowPassFilter builds the DS201-inspired low-pass filter specification.
// Part of the DS201 frequency-conscious filtering chain, placed after highpass.
//
// Purpose: Remove ultrasonic content that could trigger false gate openings.
// The Drawmer DS201 includes LP filtering in its side-chain to focus gate detection
// on voice frequencies rather than high-frequency noise artifacts.
//
// Parameters:
// - f: cutoff frequency (removes frequencies above this)
// - poles: 1=6dB/oct (gentle), 2=12dB/oct (standard)
// - width: Q factor (0.707=Butterworth for maximally flat passband)
// - transform: filter algorithm (tdii=best floating-point accuracy)
// - mix: wet/dry blend (1.0=full filter)
//
// Returns empty string if DS201LowPass.Enabled is false.
func (cfg *EffectiveFilterConfig) buildDS201LowPassFilter() string {
	lowpass := cfg.DS201LowPass
	if !lowpass.Enabled {
		return ""
	}

	poles := lowpass.Poles
	if poles < 1 {
		poles = 2 // Default to standard 12dB/oct
	}

	width := lowpass.Width
	if width <= 0 {
		width = 0.707 // Butterworth default
	}

	lpSpec := fmt.Sprintf("lowpass=f=%.0f:poles=%d:width_type=q:width=%.3f:normalize=1",
		lowpass.Frequency, poles, width)

	// Add transform type if specified (tdii = best floating-point accuracy)
	if lowpass.Transform != "" {
		lpSpec += fmt.Sprintf(":a=%s", lowpass.Transform)
	}

	// Add mix parameter if not full wet (for subtle application)
	if lowpass.Mix > 0 && lowpass.Mix < 1.0 {
		lpSpec += fmt.Sprintf(":m=%.2f", lowpass.Mix)
	}

	return lpSpec
}

// buildNoiseRemoveFilter builds the anlmdn+compand noise reduction filter.
// Non-Local Means denoiser followed by a compand for residual suppression.
// Runs at the source sample rate; downstream filters (gate, LA-2A, de-esser,
// analysis) operate at the same rate.
//
// anlmdn parameters (matrix spike defaults at .bench/anlmdn-matrix-spike):
// - s: strength (0.00001 = minimum, kept constant)
// - p: patch size in seconds (6ms = 0.006s, context window for similarity)
// - r: research radius in seconds (2.0ms = 0.0020s, r_min)
// - m: smoothing factor (3 = m_strict)
//
// compand parameters:
// - FLAT reduction curve: uniform expansion below threshold
// - threshold/expansion: derived from Pass 1 measurements in tuneNoiseRemove
// - attack: 5ms (fixed, empirically validated for speech)
// - decay: 100ms (fixed, empirically validated for speech)
// - soft-knee: 6dB (fixed, transparent)
func (cfg *EffectiveFilterConfig) buildNoiseRemoveFilter() string {
	noiseRemove := cfg.NoiseRemove
	if !noiseRemove.Enabled {
		return ""
	}

	filters := make([]string, 0, 2)
	filters = append(filters, fmt.Sprintf("anlmdn=s=%.5f:p=%.4f:r=%.4f:m=%.0f",
		noiseRemove.Strength,
		noiseRemove.PatchSec,
		noiseRemove.ResearchSec,
		noiseRemove.Smooth,
	))

	if noiseRemove.CompandEnabled {
		filters = append(filters, cfg.buildNoiseRemoveCompandFilter())
	}

	return strings.Join(filters, ",")
}

// buildNoiseRemoveCompandFilter builds the compand filter for residual noise suppression.
// Compand uses FLAT reduction curve with uniform expansion below threshold.
// Parameters are derived from Pass 1 measurements in tuneNoiseRemove (adaptive.go).
func (cfg *EffectiveFilterConfig) buildNoiseRemoveCompandFilter() string {
	noiseRemove := cfg.NoiseRemove
	// Build FLAT reduction curve
	// Every point below threshold gets the same expansion (reduction)
	// Points: -90 → (-90 - exp), -75 → (-75 - exp), threshold → threshold, -30 → -30, 0 → 0
	exp := noiseRemove.CompandExpansion
	thresh := noiseRemove.CompandThreshold

	out90 := -90.0 - exp
	out75 := -75.0 - exp

	// Format: attacks:decays:soft-knee:points
	// Points format: in1/out1|in2/out2|...
	return fmt.Sprintf(
		"compand=attacks=%.3f:decays=%.3f:soft-knee=%.1f:points=-90/%.0f|-75/%.0f|%.0f/%.0f|-30/-30|0/0",
		noiseRemove.CompandAttack,
		noiseRemove.CompandDecay,
		noiseRemove.CompandKnee,
		out90, out75,
		thresh, thresh,
	)
}

// buildDS201GateFilter builds the DS201-inspired gate filter specification.
// Uses soft expander approach (2:1-4:1 ratio) rather than hard gate for natural speech.
// Minimum 10ms attack prevents click artifacts from rapid gain changes.
// Detection mode is adaptive: RMS for tonal bleed, peak for clean recordings.
func (cfg *EffectiveFilterConfig) buildDS201GateFilter() string {
	gate := cfg.DS201Gate
	if !gate.Enabled {
		return ""
	}
	detection := gate.Detection
	if detection == "" {
		detection = "rms" // Safe default for speech
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

// buildLA2ACompressorFilter builds the LA-2A style compressor filter specification.
// Uses FFmpeg's acompressor with settings tuned to emulate the Teletronix LA-2A
// optical compressor's gentle, program-dependent character.
// Converts dB values to linear for FFmpeg's format.
func (cfg *EffectiveFilterConfig) buildLA2ACompressorFilter() string {
	la2a := cfg.LA2A
	if !la2a.Enabled {
		return ""
	}
	return fmt.Sprintf(
		"acompressor=threshold=%.6f:ratio=%.1f:attack=%.0f:release=%.0f:"+
			"makeup=%.2f:knee=%.1f:detection=rms:mix=%.2f",
		Decibels(la2a.Threshold).LinearAmplitude().Float64(),
		la2a.Ratio,
		la2a.Attack,
		la2a.Release,
		Decibels(la2a.Makeup).LinearAmplitude().Float64(),
		la2a.Knee,
		la2a.Mix,
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
// Parameters:
// - t (threshold): Detection sensitivity (0.1-8.0, lower=more sensitive)
// - w (window): Analysis window in ms (10-100, default 55)
// - o (overlap): Window overlap percentage (50-95, default 75)
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

func (cfg *BaseFilterConfig) BuildFilterSpec() string {
	return deriveEffectiveFilterConfig(cfg).BuildFilterSpec()
}

// CreateProcessingFilterGraph creates an AVFilterGraph for complete audio processing
// This is used in Pass 2 to apply the full filter chain.
func CreateProcessingFilterGraph(
	decCtx *ffmpeg.AVCodecContext,
	config *EffectiveFilterConfig,
) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
	return setupFilterGraph(decCtx, config.BuildFilterSpec())
}
