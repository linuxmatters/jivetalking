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
	FilterUREI1176       FilterID = "urei1176_limiter" // UREI 1176-inspired safety limiter
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
	NormTargetLUFS = -18.0

	// NormToleranceLU is the acceptable deviation from target.
	// ±0.5 LU is industry standard for loudness compliance.
	NormToleranceLU = 0.5
)

// filterBuilderFunc is a function that builds a filter spec from config.
// Returns the FFmpeg filter specification string, or empty string if disabled.
type filterBuilderFunc func(*FilterChainConfig) string

// filterBuilders maps FilterID to its builder function.
// This registry centralises filter spec generation and avoids per-call map allocation.
var filterBuilders = map[FilterID]filterBuilderFunc{
	FilterDownmix:        (*FilterChainConfig).buildDownmixFilter,
	FilterAnalysis:       (*FilterChainConfig).buildAnalysisFilter,
	FilterResample:       (*FilterChainConfig).buildResampleFilter,
	FilterDS201HighPass:  (*FilterChainConfig).buildDS201HighpassFilter,
	FilterDS201LowPass:   (*FilterChainConfig).buildDS201LowPassFilter,
	FilterNoiseRemove:    (*FilterChainConfig).buildNoiseRemoveFilter,
	FilterDS201Gate:      (*FilterChainConfig).buildDS201GateFilter,
	FilterLA2ACompressor: (*FilterChainConfig).buildLA2ACompressorFilter,
	FilterDeesser:        (*FilterChainConfig).buildDeesserFilter,
	FilterUREI1176:       (*FilterChainConfig).buildUREI1176Filter,
}

// FilterChainConfig holds configuration for the audio processing filter chain
type FilterChainConfig struct {
	// Pass indicates which processing pass is being executed (1 = analysis, 2 = processing)
	// Used by filters that need pass-specific behaviour
	Pass int

	// Downmix (pan) - stereo to mono conversion
	// Applied first to ensure all downstream filters work with mono
	DownmixEnabled bool

	// Analysis (ebur128 + astats + aspectralstats) - audio measurement collection
	// Captures loudness, dynamics, spectral characteristics
	AnalysisEnabled bool

	// Resample (aformat) - output format standardisation
	// Pass 2 only - ensures consistent output format
	ResampleEnabled    bool
	ResampleSampleRate int    // Output sample rate (default: 44100)
	ResampleFormat     string // Output sample format (default: s16)
	ResampleFrameSize  int    // Samples per frame (default: 4096)

	// DS201-Inspired High-Pass Filter (highpass) - removes subsonic rumble
	// Part of the DS201 side-chain composite: removes rumble before gate detection
	DS201HPEnabled   bool    // Enable DS201 high-pass filter
	DS201HPFreq      float64 // Hz, cutoff frequency (removes frequencies below this)
	DS201HPPoles     int     // Filter poles: 1=6dB/oct (gentle), 2=12dB/oct (standard)
	DS201HPWidth     float64 // Q factor: 0.707=Butterworth (default), lower=gentler rolloff
	DS201HPMix       float64 // Wet/dry mix (0-1, 1=full filter, 0.7=subtle for warm voices)
	DS201HPTransform string  // Filter transform: "tdii" (best accuracy), "zdf", etc.

	// DS201-Inspired Low-Pass Filter (lowpass) - removes ultrasonic noise
	// Part of the DS201 side-chain composite: prevents HF noise from triggering gate
	// Enabled adaptively based on content type and HF noise indicators
	DS201LPEnabled      bool        // Enable DS201 low-pass filter
	DS201LPFreq         float64     // Hz, cutoff frequency (removes frequencies above this)
	DS201LPPoles        int         // Filter poles: 1=6dB/oct (gentle), 2=12dB/oct (standard)
	DS201LPWidth        float64     // Q factor: 0.707=Butterworth (default)
	DS201LPMix          float64     // Wet/dry mix (0-1, 1=full filter)
	DS201LPTransform    string      // Filter transform: "tdii" (best accuracy), "zdf", etc.
	DS201LPContentType  ContentType // Detected content type (speech/music/mixed)
	DS201LPReason       string      // Why enabled/disabled (for logging)
	DS201LPRolloffRatio float64     // Actual rolloff/centroid ratio (for logging)

	// NoiseRemove - anlmdn + compand noise reduction
	// Non-Local Means denoiser (anlmdn) with a compand for residual suppression
	// Validated fast-compand config: -13 to -20 dB silence reduction, 20-24x realtime, ±1-2% spectral preservation
	NoiseRemoveEnabled          bool    // Enable anlmdn+compand noise reduction
	NoiseRemoveStrength         float64 // anlmdn strength (0.00001 = minimum, kept constant)
	NoiseRemovePatchSec         float64 // Patch size in seconds (context window for similarity)
	NoiseRemoveResearchSec      float64 // Research radius in seconds (search window for matching)
	NoiseRemoveSmooth           float64 // Smoothing factor for weights (1-1000)
	NoiseRemoveCompandThreshold float64 // Expansion threshold (dB) - set to measured noise floor
	NoiseRemoveCompandExpansion float64 // Expansion depth (dB) - gap between input floor and target floor
	NoiseRemoveCompandAttack    float64 // Attack time (seconds) - fixed at 5ms for speech
	NoiseRemoveCompandDecay     float64 // Decay time (seconds) - fixed at 100ms for speech
	NoiseRemoveCompandKnee      float64 // Soft knee (dB) - fixed at 6dB for transparency

	// DS201-Inspired Gate (agate) - Drawmer DS201 style soft expander
	// Uses gentle ratio (2:1-4:1) rather than DS201's hard gate for natural speech transitions.
	// Sub-millisecond attack capability for transient preservation.
	DS201GateEnabled    bool    // Enable DS201-style gate
	DS201GateThreshold  float64 // Activation threshold (0.0-1.0, linear)
	DS201GateRatio      float64 // Reduction ratio - soft expander (2:1-4:1), not hard gate
	DS201GateAttack     float64 // Attack time (ms) - supports 0.5ms+ for transient preservation
	DS201GateRelease    float64 // Release time (ms) - includes +50ms to compensate for no Hold param
	DS201GateRange      float64 // Level of gain reduction below threshold (0.0-1.0)
	DS201GateKnee       float64 // Knee curve softness (1.0-8.0) - soft knee for natural transitions
	DS201GateMakeup     float64 // Makeup gain after gating (1.0-64.0)
	DS201GateDetection  string  // Level detection mode: "rms" (default, smoother) or "peak" (tighter)
	DS201GateGentleMode bool    // Gentle mode active - for extreme LUFS gap + low LRA recordings

	// Breath reduction mode - adjusts gate threshold to target breath sounds
	BreathReductionEnabled bool

	// LA-2A Compressor - Teletronix LA-2A style optical compression
	// The LA-2A is legendary for its gentle, program-dependent character from the T4 optical cell.
	LA2AEnabled   bool    // Enable LA-2A compressor
	LA2AThreshold float64 // dB, compression threshold (stored in dB, converted to linear)
	LA2ARatio     float64 // Compression ratio (1.0-20.0)
	LA2AAttack    float64 // Attack time (ms) - LA-2A has fixed ~10ms attack
	LA2ARelease   float64 // Release time (ms) - LA-2A has program-dependent two-stage release
	LA2AMakeup    float64 // dB, makeup gain (stored in dB, converted to linear)
	LA2AKnee      float64 // Knee curve softness (1.0-8.0) - T4 cell provides inherent soft knee
	LA2AMix       float64 // Wet/dry mix (0.0-1.0, 1.0 = 100% compressed)

	// De-esser (deesser) - removes harsh sibilance automatically
	DeessEnabled   bool    // Enable deesser filter
	DeessIntensity float64 // 0.0-1.0, intensity for triggering de-essing (0=off, 1=max)
	DeessAmount    float64 // 0.0-1.0, amount of ducking on treble (how much to reduce)
	DeessFreq      float64 // 0.0-1.0, how much original frequency content to keep

	// Target values (for reference only)
	TargetI   float64 // LUFS target reference (podcast standard: -18)
	TargetTP  float64 // dBTP, true peak ceiling reference
	TargetLRA float64 // LU, loudness range reference

	// UREI 1176-Inspired Limiter - final brick-wall safety net
	// Attack/release adapt based on transient and dynamics measurements
	// ASC provides program-dependent release approximation
	UREI1176Enabled     bool    // Enable 1176-inspired limiter
	UREI1176Ceiling     float64 // dBTP - peak ceiling (-1.0 = podcast standard)
	UREI1176Attack      float64 // ms - attack time (0.1-1.0)
	UREI1176Release     float64 // ms - release time (100-200)
	UREI1176ASC         bool    // Enable Auto Soft Clipping (program-dependent release)
	UREI1176ASCLevel    float64 // 0.0-1.0 - ASC release influence
	UREI1176InputLevel  float64 // Linear - input gain (default 1.0)
	UREI1176OutputLevel float64 // Linear - output gain (default 1.0)

	// Filter chain order - controls the sequence of filters in the processing chain
	// Use Pass2FilterOrder or customise for experimentation
	FilterOrder []FilterID

	// Pass 1 measurements (nil for first pass)
	Measurements *AudioMeasurements

	// Output Analysis - enables astats/ebur128/aspectralstats at end of Pass 2 filter chain
	// When enabled, measurements are extracted from processed audio for comparison with Pass 1
	OutputAnalysisEnabled bool

	// Loudnorm (Pass 3) - EBU R128 dynamic loudness normalisation
	// Replaces simple volume gain + 1176 limiting with integrated dynamic normalisation
	// Uses two-pass mode with measurements from Pass 2 for optimal transparency
	LoudnormEnabled   bool    // Enable loudnorm in Pass 3 (default: true)
	LoudnormTargetI   float64 // Target integrated loudness (LUFS), default: -18.0
	LoudnormTargetTP  float64 // Target true peak (dBTP), default: -1.5
	LoudnormTargetLRA float64 // Target loudness range (LU), default: 11.0
	LoudnormDualMono  bool    // Treat mono as dual-mono (CRITICAL for mono files)
	LoudnormLinear    bool    // Prefer linear mode (falls back to dynamic if needed)
}

// DefaultFilterConfig returns the scientifically-tuned default filter configuration
// for podcast spoken word audio processing.
func DefaultFilterConfig() *FilterChainConfig {
	return &FilterChainConfig{
		// Pass (set by caller, defaults to 0 meaning unset)
		Pass: 0,

		// Downmix - always enabled to ensure mono processing
		DownmixEnabled: true,

		// Analysis - always enabled to collect measurements
		AnalysisEnabled: true,

		// Resample - enabled by default (Pass 2 only via filter order)
		ResampleEnabled:    true,
		ResampleSampleRate: 44100,
		ResampleFormat:     "s16",
		ResampleFrameSize:  4096,

		// DS201-Inspired High-pass - remove subsonic rumble (part of DS201 side-chain)
		DS201HPEnabled:   true,
		DS201HPFreq:      80.0,   // 80Hz cutoff
		DS201HPPoles:     2,      // 12dB/oct standard slope (1=gentle 6dB/oct for warm voices)
		DS201HPWidth:     0.707,  // Butterworth Q (maximally flat passband)
		DS201HPMix:       1.0,    // Full wet signal (reduce for warm voice protection)
		DS201HPTransform: "tdii", // Transposed Direct Form II - best floating-point accuracy

		// DS201-Inspired Low-pass Filter - removes ultrasonic noise (part of DS201 side-chain)
		DS201LPEnabled:   true,
		DS201LPFreq:      16000.0, // 16kHz cutoff (conservative default, preserves all audible content)
		DS201LPPoles:     2,       // 12dB/oct standard slope
		DS201LPWidth:     0.707,   // Butterworth Q (maximally flat passband)
		DS201LPMix:       1.0,     // Full wet signal
		DS201LPTransform: "tdii",  // Transposed Direct Form II - best floating-point accuracy

		// NoiseRemove - anlmdn + compand (validated fast-compand config)
		NoiseRemoveEnabled:          true,    // Primary noise reduction filter
		NoiseRemoveStrength:         0.00001, // Minimum strength (fixed from spike validation)
		NoiseRemovePatchSec:         0.006,   // 6ms patch (fast-compand validated)
		NoiseRemoveResearchSec:      0.0058,  // 5.8ms research (fast-compand validated)
		NoiseRemoveSmooth:           11.0,    // Default smoothing
		NoiseRemoveCompandThreshold: -55.0,   // Overridden by adaptive tuning
		NoiseRemoveCompandExpansion: 6.0,     // Overridden by adaptive tuning
		NoiseRemoveCompandAttack:    0.005,   // 5ms - fixed, empirically validated for speech
		NoiseRemoveCompandDecay:     0.100,   // 100ms - fixed, empirically validated for speech
		NoiseRemoveCompandKnee:      6.0,     // 6dB - fixed, soft knee for transparency

		// DS201-Inspired Gate - soft expander for natural speech transitions
		// All parameters set adaptively based on Pass 1 measurements
		DS201GateEnabled:   true,
		DS201GateThreshold: 0.01,   // -40dBFS default (adaptive: based on silence peak + headroom)
		DS201GateRatio:     2.0,    // 2:1 ratio - soft expander (adaptive: based on LRA)
		DS201GateAttack:    12,     // 12ms attack (adaptive: 0.5-25ms based on MaxDifference/Crest)
		DS201GateRelease:   350,    // 350ms release (adaptive: based on flux/ZCR, +50ms hold compensation)
		DS201GateRange:     0.0625, // -24dB reduction (adaptive: based on silence entropy)
		DS201GateKnee:      3.0,    // Soft knee (adaptive: based on spectral crest)
		DS201GateMakeup:    1.0,    // Unity gain (loudnorm handles all level adjustment)
		DS201GateDetection: "rms",  // RMS detection (adaptive: rms for bleed, peak for clean)

		// Breath reduction mode - enabled by default
		BreathReductionEnabled: true,

		// LA-2A Compressor - Teletronix LA-2A style optical compressor emulation
		// The Teletronix LA-2A is renowned for its gentle, program-dependent character:
		// - Fixed 10ms attack preserves transients
		// - Two-stage release (60ms initial, 1-15s full) - we use ~200ms approximation
		// - Soft 3:1 ratio from T4 optical cell
		// - Very soft knee from T4 optical cell
		// All parameters are tuned adaptively by tuneLA2ACompressor()
		LA2AEnabled:   true,
		LA2AThreshold: -18, // -18dB threshold (tuned relative to RMS in adaptive)
		LA2ARatio:     3.0, // 3:1 ratio (LA-2A Compress mode baseline)
		LA2AAttack:    10,  // 10ms attack (LA-2A fixed attack, preserves transients)
		LA2ARelease:   200, // 200ms release (LA-2A two-stage approximation)
		LA2AMakeup:    0,   // Unity gain (0 dB - loudnorm handles all level adjustment)
		LA2AKnee:      4.0, // Soft knee (LA-2A T4 optical cell characteristic)
		LA2AMix:       1.0, // 100% wet (true LA-2A has no parallel compression)

		// De-esser - automatic sibilance reduction
		DeessEnabled:   true,
		DeessIntensity: 0.0, // 0.0 = disabled by default, will be set adaptively if enabled
		DeessAmount:    0.5, // 50% ducking on treble (moderate reduction)
		DeessFreq:      0.5, // Keep 50% of original frequency content (balanced)

		// Target values (for reference only)
		TargetI:   -18.0, // Reference LUFS target (not enforced)
		TargetTP:  -0.3,  // Reference true peak (not enforced, alimiter does real limiting at -1.5)
		TargetLRA: 7.0,   // Reference loudness range (EBU R128 default)

		// UREI 1176-Inspired Limiter - enabled by default as final safety net
		UREI1176Enabled:     true,
		UREI1176Ceiling:     -1.0,  // -1.0 dBTP (podcast standard)
		UREI1176Attack:      0.8,   // 0.8ms default (normal speech)
		UREI1176Release:     150.0, // 150ms default (standard)
		UREI1176ASC:         true,
		UREI1176ASCLevel:    0.5, // Moderate ASC
		UREI1176InputLevel:  1.0, // Unity input
		UREI1176OutputLevel: 1.0, // Unity output

		// Filter chain order - use default order
		FilterOrder: Pass2FilterOrder,

		Measurements: nil, // Will be set after Pass 1

		// Loudnorm - enabled by default with podcast-optimised settings
		LoudnormEnabled:   true,
		LoudnormTargetI:   -18.0, // Broadcast standard (was -16 podcast standard, -18 for more headroom)
		LoudnormTargetTP:  -2.0,  // Conservative headroom (prevents limiter clipping to 0.0 dBTP)
		LoudnormTargetLRA: 20.0,  // High value to prevent dynamic mode fallback (must be >= source LRA)
		LoudnormDualMono:  true,  // CRITICAL for mono recordings
		LoudnormLinear:    true,  // Prefer linear (transparent) mode
	}
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

// boolToInt converts a bool to 0 or 1 for FFmpeg filter parameters
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// buildDownmixFilter builds the stereo-to-mono downmix filter specification.
// Uses FFmpeg's built-in channel layout conversion which handles various input
// configurations (stereo, mono, single-channel recordings) correctly.
func (cfg *FilterChainConfig) buildDownmixFilter() string {
	if !cfg.DownmixEnabled {
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
// ebur128 is placed last because it upsamples to 192kHz internally and outputs f64,
// which would skew spectral measurements if placed first. astats and aspectralstats
// measure the original signal format, then ebur128 does its own internal upsampling
// for accurate true peak detection without affecting other measurements.
//
// NOTE: loudnorm is NOT included here because it has no "measure only" mode -
// it always processes/normalizes audio. Loudnorm measurement for Pass 3 is done
// separately via measureWithLoudnorm() which reads the processed file without
// encoding output.
func (cfg *FilterChainConfig) buildAnalysisFilter() string {
	if !cfg.AnalysisEnabled {
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
		cfg.TargetI)
}

// buildResampleFilter builds the output format standardisation filter.
// Ensures consistent output: 44.1kHz, 16-bit, mono, fixed frame size.
// Pass 2 only - applied after all processing and analysis.
func (cfg *FilterChainConfig) buildResampleFilter() string {
	if !cfg.ResampleEnabled {
		return ""
	}
	return fmt.Sprintf("aformat=sample_rates=%d:channel_layouts=mono:sample_fmts=%s,asetnsamples=n=%d",
		cfg.ResampleSampleRate, cfg.ResampleFormat, cfg.ResampleFrameSize)
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
func (cfg *FilterChainConfig) buildDS201HighpassFilter() string {
	if !cfg.DS201HPEnabled {
		return ""
	}

	poles := cfg.DS201HPPoles
	if poles < 1 {
		poles = 2 // Default to standard 12dB/oct
	}

	width := cfg.DS201HPWidth
	if width <= 0 {
		width = 0.707 // Butterworth default
	}

	hpSpec := fmt.Sprintf("highpass=f=%.0f:poles=%d:width_type=q:width=%.3f:normalize=1",
		cfg.DS201HPFreq, poles, width)

	// Add transform type if specified (tdii = best floating-point accuracy)
	if cfg.DS201HPTransform != "" {
		hpSpec += fmt.Sprintf(":a=%s", cfg.DS201HPTransform)
	}

	// Add mix parameter if not full wet (for warm voice protection)
	if cfg.DS201HPMix > 0 && cfg.DS201HPMix < 1.0 {
		hpSpec += fmt.Sprintf(":m=%.2f", cfg.DS201HPMix)
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
// Returns empty string if DS201LPEnabled is false.
func (cfg *FilterChainConfig) buildDS201LowPassFilter() string {
	if !cfg.DS201LPEnabled {
		return ""
	}

	poles := cfg.DS201LPPoles
	if poles < 1 {
		poles = 2 // Default to standard 12dB/oct
	}

	width := cfg.DS201LPWidth
	if width <= 0 {
		width = 0.707 // Butterworth default
	}

	lpSpec := fmt.Sprintf("lowpass=f=%.0f:poles=%d:width_type=q:width=%.3f:normalize=1",
		cfg.DS201LPFreq, poles, width)

	// Add transform type if specified (tdii = best floating-point accuracy)
	if cfg.DS201LPTransform != "" {
		lpSpec += fmt.Sprintf(":a=%s", cfg.DS201LPTransform)
	}

	// Add mix parameter if not full wet (for subtle application)
	if cfg.DS201LPMix > 0 && cfg.DS201LPMix < 1.0 {
		lpSpec += fmt.Sprintf(":m=%.2f", cfg.DS201LPMix)
	}

	return lpSpec
}

// buildNoiseRemoveFilter builds the anlmdn+compand noise reduction filter.
// Non-Local Means denoiser followed a compand for residual suppression.
//
// Filter chain: anlmdn → compand
//
// anlmdn parameters (validated fast-compand config):
// - s: strength (0.00001 = minimum, kept constant)
// - p: patch size in seconds (6ms = 0.006s, context window for similarity)
// - r: research radius in seconds (5.8ms = 0.0058s, search window for matching)
// - m: smoothing factor (11 = default, weight smoothing)
//
// compand parameters
// - FLAT reduction curve: uniform expansion below threshold
// - threshold/expansion: derived from Pass 1 measurements in tuneNoiseRemove
// - attack: 5ms (fixed, empirically validated for speech)
// - decay: 100ms (fixed, empirically validated for speech)
// - soft-knee: 6dB (fixed, transparent)
func (cfg *FilterChainConfig) buildNoiseRemoveFilter() string {
	if !cfg.NoiseRemoveEnabled {
		return ""
	}

	// Build anlmdn filter
	anlmdnSpec := fmt.Sprintf("anlmdn=s=%.5f:p=%.4f:r=%.4f:m=%.0f",
		cfg.NoiseRemoveStrength,
		cfg.NoiseRemovePatchSec,
		cfg.NoiseRemoveResearchSec,
		cfg.NoiseRemoveSmooth,
	)

	// Build compand filter for residual suppression
	compandSpec := cfg.buildNoiseRemoveCompandFilter()

	// Combine: anlmdn,compand
	return fmt.Sprintf("%s,%s", anlmdnSpec, compandSpec)
}

// buildNoiseRemoveCompandFilter builds the compand filter for residual noise suppression.
// Compand uses FLAT reduction curve with uniform expansion below threshold.
// Parameters are derived from Pass 1 measurements in tuneNoiseRemove (adaptive.go).
func (cfg *FilterChainConfig) buildNoiseRemoveCompandFilter() string {
	// Build FLAT reduction curve
	// Every point below threshold gets the same expansion (reduction)
	// Points: -90 → (-90 - exp), -75 → (-75 - exp), threshold → threshold, -30 → -30, 0 → 0
	exp := cfg.NoiseRemoveCompandExpansion
	thresh := cfg.NoiseRemoveCompandThreshold

	out90 := -90.0 - exp
	out75 := -75.0 - exp

	// Format: attacks:decays:soft-knee:points
	// Points format: in1/out1|in2/out2|...
	return fmt.Sprintf(
		"compand=attacks=%.3f:decays=%.3f:soft-knee=%.1f:points=-90/%.0f|-75/%.0f|%.0f/%.0f|-30/-30|0/0",
		cfg.NoiseRemoveCompandAttack,
		cfg.NoiseRemoveCompandDecay,
		cfg.NoiseRemoveCompandKnee,
		out90, out75,
		thresh, thresh,
	)
}

// buildDS201GateFilter builds the DS201-inspired gate filter specification.
// Uses soft expander approach (2:1-4:1 ratio) rather than hard gate for natural speech.
// Supports sub-millisecond attack (0.5ms+) for transient preservation.
// Detection mode is adaptive: RMS for tonal bleed, peak for clean recordings.
func (cfg *FilterChainConfig) buildDS201GateFilter() string {
	if !cfg.DS201GateEnabled {
		return ""
	}
	detection := cfg.DS201GateDetection
	if detection == "" {
		detection = "rms" // Safe default for speech
	}
	// Note: attack/release use %.2f to support sub-millisecond values (0.5ms minimum)
	return fmt.Sprintf(
		"agate=threshold=%.6f:ratio=%.1f:attack=%.2f:release=%.0f:"+
			"range=%.4f:knee=%.1f:detection=%s:makeup=%.1f",
		cfg.DS201GateThreshold,
		cfg.DS201GateRatio,
		cfg.DS201GateAttack,
		cfg.DS201GateRelease,
		cfg.DS201GateRange,
		cfg.DS201GateKnee,
		detection,
		cfg.DS201GateMakeup,
	)
}

// buildLA2ACompressorFilter builds the LA-2A style compressor filter specification.
// Uses FFmpeg's acompressor with settings tuned to emulate the Teletronix LA-2A
// optical compressor's gentle, program-dependent character.
// Converts dB values to linear for FFmpeg's format.
func (cfg *FilterChainConfig) buildLA2ACompressorFilter() string {
	if !cfg.LA2AEnabled {
		return ""
	}
	return fmt.Sprintf(
		"acompressor=threshold=%.6f:ratio=%.1f:attack=%.0f:release=%.0f:"+
			"makeup=%.2f:knee=%.1f:detection=rms:mix=%.2f",
		DbToLinear(cfg.LA2AThreshold),
		cfg.LA2ARatio,
		cfg.LA2AAttack,
		cfg.LA2ARelease,
		DbToLinear(cfg.LA2AMakeup),
		cfg.LA2AKnee,
		cfg.LA2AMix,
	)
}

// buildDeesserFilter builds the deesser filter specification.
// Automatically detects and reduces harsh sibilance ("s" sounds).
// Returns empty string if disabled or intensity is 0.
func (cfg *FilterChainConfig) buildDeesserFilter() string {
	if !cfg.DeessEnabled || cfg.DeessIntensity <= 0 {
		return ""
	}
	return fmt.Sprintf(
		"deesser=i=%.2f:m=%.2f:f=%.2f",
		cfg.DeessIntensity,
		cfg.DeessAmount,
		cfg.DeessFreq,
	)
}

// buildUREI1176Filter builds the UREI 1176-inspired limiter filter specification.
// Uses FFmpeg's alimiter with adaptive attack/release and ASC for program-dependent
// release approximation. The 1176's FET character and harmonic enhancement cannot
// be replicated, but we capture its timing behaviour for musical peak protection.
func (cfg *FilterChainConfig) buildUREI1176Filter() string {
	if !cfg.UREI1176Enabled {
		return ""
	}

	// Convert ceiling from dBTP to linear (0.0-1.0)
	ceiling := math.Pow(10, cfg.UREI1176Ceiling/20.0)

	// Default input/output levels to unity if not set (0.0 would mute audio)
	inputLevel := cfg.UREI1176InputLevel
	if inputLevel == 0.0 {
		inputLevel = 1.0
	}
	outputLevel := cfg.UREI1176OutputLevel
	if outputLevel == 0.0 {
		outputLevel = 1.0
	}

	// Build filter with adaptive parameters
	spec := fmt.Sprintf(
		"alimiter=limit=%.6f:attack=%.1f:release=%.1f:level_in=%.4f:level_out=%.4f:level=0:latency=1",
		ceiling,
		cfg.UREI1176Attack,
		cfg.UREI1176Release,
		inputLevel,
		outputLevel,
	)

	// Add ASC parameters
	if cfg.UREI1176ASC {
		spec += fmt.Sprintf(":asc=1:asc_level=%.2f", cfg.UREI1176ASCLevel)
	} else {
		spec += ":asc=0"
	}

	return spec
}

// BuildFilterSpec builds the FFmpeg filter specification string for Pass 2 processing.
// Filter order is determined by cfg.FilterOrder (or Pass2FilterOrder if empty).
// Each filter checks its Enabled flag and returns empty string if disabled.
// Uses the package-level filterBuilders registry for filter spec generation.
func (cfg *FilterChainConfig) BuildFilterSpec() string {
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
	config *FilterChainConfig,
) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
	return setupFilterGraph(decCtx, config.BuildFilterSpec())
}

// setupFilterGraph creates and configures an FFmpeg filter graph with the given
// filter specification. It handles all common boilerplate: graph allocation,
// buffer source/sink creation, parsing, and configuration.
//
// Returns the configured filter graph and source/sink contexts, or an error.
// The caller is responsible for freeing the filter graph with AVFilterGraphFree.
func setupFilterGraph(decCtx *ffmpeg.AVCodecContext, filterSpec string) (
	*ffmpeg.AVFilterGraph,
	*ffmpeg.AVFilterContext,
	*ffmpeg.AVFilterContext,
	error,
) {
	filterGraph := ffmpeg.AVFilterGraphAlloc()
	if filterGraph == nil {
		return nil, nil, nil, fmt.Errorf("failed to allocate filter graph")
	}

	// Create abuffer source
	bufferSrcCtx, err := createBufferSource(filterGraph, decCtx)
	if err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, err
	}

	// Create abuffersink
	bufferSinkCtx, err := createBufferSink(filterGraph)
	if err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, err
	}

	// Parse filter graph
	outputs := ffmpeg.AVFilterInoutAlloc()
	inputs := ffmpeg.AVFilterInoutAlloc()
	defer ffmpeg.AVFilterInoutFree(&outputs)
	defer ffmpeg.AVFilterInoutFree(&inputs)

	outputs.SetName(ffmpeg.ToCStr("in"))
	outputs.SetFilterCtx(bufferSrcCtx)
	outputs.SetPadIdx(0)
	outputs.SetNext(nil)

	inputs.SetName(ffmpeg.ToCStr("out"))
	inputs.SetFilterCtx(bufferSinkCtx)
	inputs.SetPadIdx(0)
	inputs.SetNext(nil)

	filterSpecC := ffmpeg.ToCStr(filterSpec)
	defer filterSpecC.Free()

	if _, err := ffmpeg.AVFilterGraphParsePtr(filterGraph, filterSpecC, &inputs, &outputs, nil); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to parse filter graph: %w", err)
	}

	// Configure filter graph
	if _, err := ffmpeg.AVFilterGraphConfig(filterGraph, nil); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to configure filter graph: %w", err)
	}

	return filterGraph, bufferSrcCtx, bufferSinkCtx, nil
}

// createBufferSource creates and configures the abuffer source filter
func createBufferSource(filterGraph *ffmpeg.AVFilterGraph, decCtx *ffmpeg.AVCodecContext) (*ffmpeg.AVFilterContext, error) {
	bufferSrc := ffmpeg.AVFilterGetByName(ffmpeg.GlobalCStr("abuffer"))
	if bufferSrc == nil {
		return nil, fmt.Errorf("abuffer filter not found")
	}

	// Get channel layout description
	layoutPtr := ffmpeg.AllocCStr(64)
	defer layoutPtr.Free()

	if _, err := ffmpeg.AVChannelLayoutDescribe(decCtx.ChLayout(), layoutPtr, 64); err != nil {
		return nil, fmt.Errorf("failed to get channel layout: %w", err)
	}

	pktTimebase := decCtx.PktTimebase()
	args := fmt.Sprintf(
		"time_base=%d/%d:sample_rate=%d:sample_fmt=%s:channel_layout=%s",
		pktTimebase.Num(), pktTimebase.Den(),
		decCtx.SampleRate(),
		ffmpeg.AVGetSampleFmtName(decCtx.SampleFmt()).String(),
		layoutPtr.String(),
	)

	argsC := ffmpeg.ToCStr(args)
	defer argsC.Free()

	var bufferSrcCtx *ffmpeg.AVFilterContext
	if _, err := ffmpeg.AVFilterGraphCreateFilter(
		&bufferSrcCtx,
		bufferSrc,
		ffmpeg.GlobalCStr("in"),
		argsC,
		nil,
		filterGraph,
	); err != nil {
		return nil, fmt.Errorf("failed to create abuffer: %w", err)
	}

	return bufferSrcCtx, nil
}

// createBufferSink creates and configures the abuffersink filter
func createBufferSink(filterGraph *ffmpeg.AVFilterGraph) (*ffmpeg.AVFilterContext, error) {
	bufferSink := ffmpeg.AVFilterGetByName(ffmpeg.GlobalCStr("abuffersink"))
	if bufferSink == nil {
		return nil, fmt.Errorf("abuffersink filter not found")
	}

	var bufferSinkCtx *ffmpeg.AVFilterContext
	if _, err := ffmpeg.AVFilterGraphCreateFilter(
		&bufferSinkCtx,
		bufferSink,
		ffmpeg.GlobalCStr("out"),
		nil,
		nil,
		filterGraph,
	); err != nil {
		return nil, fmt.Errorf("failed to create abuffersink: %w", err)
	}

	return bufferSinkCtx, nil
}
