// Package processor handles audio analysis and processing
package processor

import (
	_ "embed"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

//go:embed models/bd.rnnn
var rnnModelBD []byte

//go:embed models/cb.rnnn
var rnnModelCB []byte

//go:embed models/sh.rnnn
var rnnModelSH []byte

// RNNModel identifies which embedded RNN model to use for arnndn
type RNNModel string

const (
	// RNNModelBD - Beguiling Drafter model (default)
	// Voice in a reasonable recording environment. Fans, AC, computers, etc.
	RNNModelBD RNNModel = "bd"
	// RNNModelCB - Conjoined Burgers model
	// General use in a reasonable recording environment. Fans, AC, computers, etc.
	RNNModelCB RNNModel = "cb"
	// RNNModelSH - Stationary Highpass model
	// Speech in a reasonable recording environment. Fans, AC, computers, etc.
	// Note that "speech" means speech, not other human sounds; laughter, coughing, etc are not included.
	RNNModelSH RNNModel = "sh"
)

// DefaultRNNModel is the default model used for arnndn denoising
const DefaultRNNModel = RNNModelBD

// FilterID identifies a filter in the processing chain
type FilterID string

// Filter identifiers for the audio processing chain
const (
	// Infrastructure filters (applied in both passes or pass-specific)
	FilterDownmix       FilterID = "downmix"       // Stereo → mono conversion (both passes)
	FilterAnalysis      FilterID = "analysis"      // ebur128 + astats + aspectralstats (both passes)
	FilterSilenceDetect FilterID = "silencedetect" // Silence region detection (Pass 1 only)
	FilterResample      FilterID = "resample"      // Output format: 44.1kHz/16-bit/mono (Pass 2 only)

	// DS201-inspired frequency-conscious filtering (Pass 2 only)
	// Drawmer DS201 pioneered HP/LP side-chain filtering for frequency-conscious gating.
	// We apply these filters to the audio path before the gate for equivalent effect.
	FilterDS201HighPass FilterID = "ds201_highpass" // HP + hum notch composite (part of DS201 side-chain)
	FilterDS201LowPass  FilterID = "ds201_lowpass"  // LP for ultrasonic rejection (adaptive)
	FilterDS201Gate     FilterID = "ds201_gate"     // Soft expander inspired by DS201

	// CEDAR DNS-1500-inspired noise reduction (Pass 2 only)
	// Adaptive spectral denoising using learned noise profile from detected silence
	FilterDNS1500 FilterID = "dns1500"

	// Dolby SR-inspired noise reduction (Pass 2 only)
	// 6-band multiband compander implementing SR philosophy: Least Treatment, transparency over depth
	// Used as fallback when no silence region is detected for DNS-1500
	FilterDolbySR FilterID = "dolbysr"

	// Processing filters (Pass 2 only)
	FilterDC1Declick     FilterID = "dc1_declick" // CEDAR DC-1 inspired declicker
	FilterArnndn         FilterID = "arnndn"
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
// - DS201HighPass: removes subsonic rumble before other filters (DS201-inspired side-chain)
// - DS201LowPass: removes ultrasonic content that could trigger false gates (adaptive)
// - DNS1500: primary noise reduction using learned noise profile (requires detected silence)
// - DolbySR: fallback noise reduction when no silence detected (profile-free expansion)
// - Arnndn: AI-based denoising for complex/dynamic noise patterns
// - DS201Gate: soft expander for inter-speech cleanup (after denoising lowers floor)
// - DC1Declick: CEDAR DC-1 inspired declicker - removes clicks/pops
// - LA2ACompressor: LA-2A style optical compression evens dynamics before normalisation
// - Deesser: after compression (which emphasises sibilance)
// - Analysis: measures output for comparison with Pass 1 (ebur128 upsamples to 192kHz/f64)
// - Resample: standardises output format (44.1kHz/16-bit/mono) - MUST be last
var Pass2FilterOrder = []FilterID{
	FilterDownmix,
	FilterDS201HighPass,
	FilterDS201LowPass,
	FilterDNS1500,
	FilterDolbySR,
	FilterArnndn,
	FilterDS201Gate,
	FilterDC1Declick,
	FilterLA2ACompressor,
	FilterDeesser,
	// FilterUREI1176 moved to Pass 3 for peak protection after gain normalisation
	FilterAnalysis,
	FilterResample,
}

// DefaultFilterOrder kept for backwards compatibility, points to Pass2FilterOrder
var DefaultFilterOrder = Pass2FilterOrder

// =============================================================================
// Dolby SR mcompand Configuration
// =============================================================================

// DolbySRBandConfig defines per-band parameters for the 6-band mcompand expander.
// This implements the Dolby SR-inspired voice-protective multiband noise reduction.
type DolbySRBandConfig struct {
	CrossoverHz  float64 // Upper frequency boundary for this band
	Attack       float64 // Attack time in seconds
	Decay        float64 // Decay time in seconds
	SoftKnee     float64 // Soft knee radius in dB
	ScalePercent float64 // Expansion scaling (100 = base, 105 = +5% more expansion)
}

// Dolby SR mcompand constants (validated across 3 presenters, 27 test cases)
const (
	// DolbySRDefaultExpansionDB is the default expansion depth.
	// 16 dB provides good noise reduction for clean sources while preserving voice quality.
	DolbySRDefaultExpansionDB = 16.0

	// DolbySRDefaultThresholdDB is the default expansion threshold.
	// -50 dB is the baseline for clean sources; adapted to -45/-40 for noisier sources.
	DolbySRDefaultThresholdDB = -50.0
)

// =============================================================================
// Normalisation Constants (Pass 3)
// =============================================================================

// Normalisation target and tolerance for Pass 3 gain adjustment
const (
	// NormTargetLUFS is the podcast loudness standard.
	// -15 LUFS provides headroom while remaining broadcast-compliant.
	// (Industry standard is -16 LUFS, but -15 reduces limiter workload)
	NormTargetLUFS = -15.0

	// NormToleranceLU is the acceptable deviation from target.
	// ±0.5 LU is industry standard for loudness compliance.
	NormToleranceLU = 0.5
)

// defaultDolbySRBands returns the validated 6-band voice-protective configuration.
// Voice bands (F1/F2) get slightly more expansion to counteract multiband spectral darkening.
// Air band gets slightly less to prevent over-brightness on some voices.
func defaultDolbySRBands() []DolbySRBandConfig {
	return []DolbySRBandConfig{
		{CrossoverHz: 100, Attack: 0.006, Decay: 0.095, SoftKnee: 6, ScalePercent: 100},   // Sub-bass
		{CrossoverHz: 300, Attack: 0.005, Decay: 0.100, SoftKnee: 8, ScalePercent: 100},   // Chest
		{CrossoverHz: 800, Attack: 0.005, Decay: 0.100, SoftKnee: 10, ScalePercent: 105},  // Voice F1
		{CrossoverHz: 3300, Attack: 0.005, Decay: 0.100, SoftKnee: 12, ScalePercent: 103}, // Voice F2 (critical band - maximum smoothness)
		{CrossoverHz: 8000, Attack: 0.002, Decay: 0.085, SoftKnee: 10, ScalePercent: 100}, // Presence
		{CrossoverHz: 20500, Attack: 0.002, Decay: 0.080, SoftKnee: 6, ScalePercent: 95},  // Air
	}
}

var (
	cachedModelPaths = make(map[RNNModel]string)
	modelCacheMutex  sync.Mutex
)

// filterBuilderFunc is a function that builds a filter spec from config.
// Returns the FFmpeg filter specification string, or empty string if disabled.
type filterBuilderFunc func(*FilterChainConfig) string

// filterBuilders maps FilterID to its builder function.
// This registry centralises filter spec generation and avoids per-call map allocation.
var filterBuilders = map[FilterID]filterBuilderFunc{
	FilterDownmix:        (*FilterChainConfig).buildDownmixFilter,
	FilterAnalysis:       (*FilterChainConfig).buildAnalysisFilter,
	FilterSilenceDetect:  (*FilterChainConfig).buildSilenceDetectFilter,
	FilterResample:       (*FilterChainConfig).buildResampleFilter,
	FilterDS201HighPass:  (*FilterChainConfig).buildDS201HighpassFilter,
	FilterDS201LowPass:   (*FilterChainConfig).buildDS201LowPassFilter,
	FilterArnndn:         (*FilterChainConfig).buildArnnDnFilter,
	FilterDNS1500:        (*FilterChainConfig).buildDNS1500Filter,
	FilterDolbySR:        (*FilterChainConfig).buildDolbySRFilter,
	FilterDS201Gate:      (*FilterChainConfig).buildDS201GateFilter,
	FilterDC1Declick:     (*FilterChainConfig).buildDC1DeclickFilter,
	FilterLA2ACompressor: (*FilterChainConfig).buildLA2ACompressorFilter,
	FilterDeesser:        (*FilterChainConfig).buildDeesserFilter,
	FilterUREI1176:       (*FilterChainConfig).buildUREI1176Filter,
}

// getRNNModelPath returns the path to the cached RNN model file for the specified model.
// On first call for each model, it extracts the embedded model to ~/.cache/jivetalking/<model>.rnnn
// Subsequent calls return the cached path. Thread-safe.
func getRNNModelPath(model RNNModel) (string, error) {
	modelCacheMutex.Lock()
	defer modelCacheMutex.Unlock()

	// Return cached path if already extracted
	if path, ok := cachedModelPaths[model]; ok {
		return path, nil
	}

	// Get model data based on selection
	var modelData []byte
	var modelFilename string
	switch model {
	case RNNModelBD:
		modelData = rnnModelBD
		modelFilename = "bd.rnnn"
	case RNNModelCB:
		modelData = rnnModelCB
		modelFilename = "cb.rnnn"
	case RNNModelSH:
		modelData = rnnModelSH
		modelFilename = "sh.rnnn"
	default:
		return "", fmt.Errorf("unknown RNN model: %s", model)
	}

	// Get user cache directory (works on Linux and macOS)
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user cache directory: %w", err)
	}

	// Create jivetalking cache directory
	jiveCacheDir := filepath.Join(cacheDir, "jivetalking")
	if err := os.MkdirAll(jiveCacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	// Model file path
	modelPath := filepath.Join(jiveCacheDir, modelFilename)

	// Check if model already exists
	if _, err := os.Stat(modelPath); err == nil {
		// Model already cached
		cachedModelPaths[model] = modelPath
		return modelPath, nil
	}

	// Write embedded model to cache
	if err := os.WriteFile(modelPath, modelData, 0644); err != nil {
		return "", fmt.Errorf("failed to write model file: %w", err)
	}

	cachedModelPaths[model] = modelPath
	return modelPath, nil
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

	// Silence Detection (silencedetect) - finds quiet regions for noise profiling
	// Pass 1 only - identifies silence regions for noise sample extraction
	SilenceDetectEnabled  bool
	SilenceDetectLevel    float64 // dB threshold for silence detection
	SilenceDetectDuration float64 // Minimum silence duration in seconds

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

	// DS201-Inspired Hum Filter (bandreject) - removes 50/60Hz hum and harmonics
	// Part of the DS201HighPass composite: removes tonal interference before gate detection
	// Tuned conditionally when Pass 1 entropy indicates tonal noise (set DS201HumHarmonics=0 to disable)
	DS201HumFrequency float64 // Fundamental frequency (50Hz UK/EU, 60Hz US)
	DS201HumHarmonics int     // Number of harmonics to filter (1-4, default 4)
	DS201HumWidth     float64 // Notch width in Hz (e.g., 0.5 = 0.5Hz wide notch at each harmonic)
	DS201HumTransform string  // Filter transform type: "tdii" (transposed direct form II, best floating-point accuracy)
	DS201HumMix       float64 // Wet/dry mix (0-1, 1=full filter, 0.9=subtle)

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

	// DC1 Declick (CEDAR DC-1-inspired) - removes clicks and pops via AR interpolation
	// Conditionally enabled based on Pass 1 measurements (MaxDifference, SpectralCrest)
	DC1DeclickEnabled   bool    // Enable DC1 declicker
	DC1DeclickThreshold float64 // Detection sensitivity (1-8, lower = more aggressive)
	DC1DeclickWindow    float64 // Analysis window size (40-80 ms)
	DC1DeclickOverlap   float64 // Window overlap (50-95%)
	DC1DeclickAROrder   float64 // AR model order as % of window (0-25%)
	DC1DeclickBurst     float64 // Max burst length as % of window (0-10%)
	DC1DeclickMethod    string  // 'a' = overlap-add, 's' = overlap-save
	DC1DeclickReason    string  // Why enabled/disabled (for logging)

	// DNS-1500 Noise Reduction (afftdn with inline sampling)
	// CEDAR DNS-1500-inspired adaptive spectral denoising using learned noise profile
	DNS1500Enabled      bool    // Enable when silence detected (primary NR filter)
	DNS1500NoiseReduce  float64 // nr: 0.01–97 dB noise reduction amount
	DNS1500NoiseFloor   float64 // nf: -80 to -20 dB noise floor level
	DNS1500TrackNoise   bool    // tn: Enable continuous noise tracking after learning
	DNS1500Adaptivity   float64 // ad: 0–1, how fast gains adapt (higher = slower)
	DNS1500GainSmooth   int     // gs: 0–50, spatial smoothing across frequency bins
	DNS1500ResidFloor   float64 // rf: -80 to -20 dB residual floor level
	DNS1500SilenceStart float64 // Timestamp (seconds) to start noise sampling
	DNS1500SilenceEnd   float64 // Timestamp (seconds) to stop noise sampling

	// Dolby SR Denoise - 6-band multiband compander inspired by "Least Treatment" philosophy
	// Uses mcompand with FLAT reduction curve for artifact-free noise elimination
	// Used as fallback when DNS-1500 cannot be enabled (no silence detected)
	DolbySREnabled     bool                // Enable Dolby SR filter (fallback when DNS-1500 disabled)
	DolbySRExpansionDB float64             // Base expansion depth (12-16 dB, default: 13)
	DolbySRThresholdDB float64             // Expansion threshold (-50 to -45 dB, adaptive based on trough)
	DolbySRBands       []DolbySRBandConfig // 6-band voice-protective configuration

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
	TargetI   float64 // LUFS target reference (podcast standard: -16)
	TargetTP  float64 // dBTP, true peak ceiling reference
	TargetLRA float64 // LU, loudness range reference

	// RNN Denoise (arnndn) - neural network noise reduction
	// Positioned after afftdn to handle complex/dynamic noise that spectral subtraction misses
	ArnnDnEnabled bool     // Enable RNN denoise
	ArnnDnModel   RNNModel // Which RNN model to use (bd, cb, sh)
	ArnnDnMix     float64  // Mix amount -1.0 to 1.0 (1.0 = full filtering, negative = keep noise)

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
	// Use DefaultFilterOrder or customise for experimentation
	FilterOrder []FilterID

	// Pass 1 measurements (nil for first pass)
	Measurements *AudioMeasurements

	// Output Analysis - enables astats/ebur128/aspectralstats at end of Pass 2 filter chain
	// When enabled, measurements are extracted from processed audio for comparison with Pass 1
	OutputAnalysisEnabled bool

	// Normalisation (Pass 3) - pure gain adjustment to hit target LUFS
	// No dynamic processing—just clean linear gain + UREI 1176 peak protection
	NormTargetI   float64 // Target integrated loudness (LUFS)
	NormGainDB    float64 // Calculated gain adjustment (dB) - set during Pass 3
	NormTolerance float64 // Acceptable deviation (LU)
}

// DefaultFilterConfig returns the scientifically-tuned default filter configuration
// for podcast spoken word audio processing.
//
// FILTER ENABLE/DISABLE STATUS:
// Currently only highpass and bandreject are enabled while the filter chain is
// being recalibrated. Other filters have been disabled to establish a clean baseline.
func DefaultFilterConfig() *FilterChainConfig {
	return &FilterChainConfig{
		// Pass (set by caller, defaults to 0 meaning unset)
		Pass: 0,

		// Downmix - always enabled to ensure mono processing
		DownmixEnabled: true,

		// Analysis - always enabled to collect measurements
		AnalysisEnabled: true,

		// Silence Detection - enabled by default (Pass 1 only via filter order)
		SilenceDetectEnabled:  true,
		SilenceDetectLevel:    -50.0, // -50dB threshold
		SilenceDetectDuration: 0.5,   // 0.5 second minimum

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

		// DS201-Inspired Hum Notch Filter - removes 50/60Hz hum and harmonics (part of DS201HighPass composite)
		// Tuned by tuneDS201HumFilter; set DS201HumHarmonics=0 to disable hum notches
		DS201HumFrequency: 50.0,   // 50Hz (UK/EU mains), can be set to 60Hz for US
		DS201HumHarmonics: 4,      // Filter 4 harmonics (50, 100, 150, 200Hz)
		DS201HumWidth:     1.0,    // 1Hz wide notch at each harmonic
		DS201HumTransform: "tdii", // Transposed Direct Form II - best floating-point numerical accuracy
		DS201HumMix:       1.0,    // Full wet signal (can reduce for subtle application)

		// DS201-Inspired Low-pass Filter - removes ultrasonic noise (part of DS201 side-chain)
		DS201LPEnabled:   true,
		DS201LPFreq:      16000.0, // 16kHz cutoff (conservative default, preserves all audible content)
		DS201LPPoles:     2,       // 12dB/oct standard slope
		DS201LPWidth:     0.707,   // Butterworth Q (maximally flat passband)
		DS201LPMix:       1.0,     // Full wet signal
		DS201LPTransform: "tdii",  // Transposed Direct Form II - best floating-point accuracy

		// DC1 Declick (CEDAR DC-1-inspired) - removes clicks/pops via AR interpolation
		// Enabled adaptively based on MaxDifference/SpectralCrest measurements
		DC1DeclickEnabled:   false, // Enabled adaptively based on MaxDifference/SpectralCrest
		DC1DeclickThreshold: 2.0,   // Default: conservative (CEDAR DC-1 medium sensitivity)
		DC1DeclickWindow:    55.0,  // Default: 55ms (balanced)
		DC1DeclickOverlap:   75.0,  // Default: 75% (good quality)
		DC1DeclickAROrder:   2.0,   // Default: 2% (low CPU)
		DC1DeclickBurst:     2.0,   // Default: 2% (standard burst handling)
		DC1DeclickMethod:    "s",   // overlap-save (better quality)
		DC1DeclickReason:    "",    // Set by tuneDC1Declick()

		// DNS-1500 Noise Reduction - CEDAR DNS-1500-inspired adaptive spectral denoising
		// Primary noise reduction filter; enabled by tuneDNS1500() when valid NoiseProfile exists
		DNS1500Enabled:      false, // Enabled only when valid NoiseProfile exists
		DNS1500NoiseReduce:  12.0,  // Overridden by tuneDNS1500()
		DNS1500NoiseFloor:   -50.0, // Overridden: NoiseProfile.MeasuredNoiseFloor
		DNS1500TrackNoise:   true,  // Always enabled for continuous adaptation
		DNS1500Adaptivity:   0.5,   // Overridden: derived from InputLRA
		DNS1500GainSmooth:   0,     // Overridden: derived from NoiseProfile.SpectralFlatness
		DNS1500ResidFloor:   -38.0, // Overridden: relative to MeasuredNoiseFloor
		DNS1500SilenceStart: 0.0,   // Overridden: NoiseProfile.Start
		DNS1500SilenceEnd:   0.0,   // Overridden: NoiseProfile.Start + Duration

		// Dolby SR-Inspired Denoise - 6-band multiband compander
		// Implements SR philosophy: Least Treatment via FLAT reduction curve
		// Uses mcompand with voice-protective band scaling
		// Fallback noise reduction when DNS-1500 cannot be enabled (no silence detected)
		DolbySREnabled:     false,                     // Disabled by default; enabled as fallback by AdaptConfig()
		DolbySRExpansionDB: DolbySRDefaultExpansionDB, // 13 dB (adaptive: 12-16 based on noise floor)
		DolbySRThresholdDB: DolbySRDefaultThresholdDB, // -50 dB (adaptive: -50/-47/-45 based on trough)
		DolbySRBands:       defaultDolbySRBands(),     // 6-band voice-protective configuration

		// DS201-Inspired Gate - soft expander for natural speech transitions
		// All parameters set adaptively based on Pass 1 measurements
		DS201GateEnabled:   true,
		DS201GateThreshold: 0.01,   // -40dBFS default (adaptive: based on silence peak + headroom)
		DS201GateRatio:     2.0,    // 2:1 ratio - soft expander (adaptive: based on LRA)
		DS201GateAttack:    12,     // 12ms attack (adaptive: 0.5-25ms based on MaxDifference/Crest)
		DS201GateRelease:   350,    // 350ms release (adaptive: based on flux/ZCR, +50ms hold compensation)
		DS201GateRange:     0.0625, // -24dB reduction (adaptive: based on silence entropy)
		DS201GateKnee:      3.0,    // Soft knee (adaptive: based on spectral crest)
		DS201GateMakeup:    1.0,    // Adaptive makeup (LUFS-based)
		DS201GateDetection: "rms",  // RMS detection (adaptive: rms for bleed, peak for clean)

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
		LA2AMakeup:    0,   // Adaptive makeup (LUFS-based gain staging like DS201 gate)
		LA2AKnee:      4.0, // Soft knee (LA-2A T4 optical cell characteristic)
		LA2AMix:       1.0, // 100% wet (true LA-2A has no parallel compression)

		// De-esser - automatic sibilance reduction
		DeessEnabled:   false,
		DeessIntensity: 0.0, // 0.0 = disabled by default, will be set adaptively if enabled
		DeessAmount:    0.5, // 50% ducking on treble (moderate reduction)
		DeessFreq:      0.5, // Keep 50% of original frequency content (balanced)

		// Target values (for reference only)
		TargetI:   -16.0, // Reference LUFS target (not enforced)
		TargetTP:  -0.3,  // Reference true peak (not enforced, alimiter does real limiting at -1.5)
		TargetLRA: 7.0,   // Reference loudness range (EBU R128 default)

		// RNN Denoise - neural network noise reduction
		// Uses specified RNN model for speech denoising
		// Enabled by default but tuneArnndn may disable for very clean sources
		ArnnDnEnabled: false,
		ArnnDnModel:   DefaultRNNModel, // Use cb.rnnn (Conjoined Burgers) by default
		ArnnDnMix:     0.7,             // Initial mix (will be tuned adaptively based on measurements)

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
		FilterOrder: DefaultFilterOrder,

		Measurements: nil, // Will be set after Pass 1

		// Normalisation target for Pass 3
		NormTargetI:   NormTargetLUFS,  // -15 LUFS
		NormGainDB:    0.0,             // Calculated in Pass 3
		NormTolerance: NormToleranceLU, // ±0.5 LU
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
// ebur128 is placed LAST because it upsamples to 192kHz internally and outputs f64,
// which would skew spectral measurements if placed first. astats and aspectralstats
// measure the original signal format, then ebur128 does its own internal upsampling
// for accurate true peak detection without affecting other measurements.
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
	// Note: astats measure_perchannel=all requests all available per-channel statistics
	return fmt.Sprintf(
		"astats=metadata=1:measure_perchannel=all,"+
			"aspectralstats=win_size=2048:win_func=hann:measure=all,"+
			"ebur128=metadata=1:peak=sample+true:target=%.0f",
		cfg.TargetI)
}

// buildSilenceDetectFilter builds the silence detection filter.
// Identifies quiet regions for noise sample extraction in Pass 1.
func (cfg *FilterChainConfig) buildSilenceDetectFilter() string {
	if !cfg.SilenceDetectEnabled {
		return ""
	}
	return fmt.Sprintf("silencedetect=noise=%.0fdB:duration=%.2f",
		cfg.SilenceDetectLevel, cfg.SilenceDetectDuration)
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

// buildDS201HighpassFilter builds the DS201-inspired composite high-pass filter.
// This is a frequency-conscious filter chain that combines:
// 1. High-pass filter - removes subsonic rumble (HVAC, handling noise, etc.)
// 2. Mains hum notches - surgical removal of 50/60Hz hum and harmonics (when enabled)
//
// The DS201's frequency-conscious gating uses side-chain HP/LP filters to prevent
// false triggers. Since FFmpeg doesn't support side-chain filtering, we apply
// frequency filtering to the audio path before gating to achieve the same effect.
//
// High-pass parameters:
// - frequency: cutoff frequency in Hz (adaptive: 60-120Hz based on voice)
// - poles: 1=6dB/oct (gentle), 2=12dB/oct (standard)
// - width: Q factor (0.707=Butterworth, lower=gentler for warm voices)
// - transform: filter algorithm (tdii=best floating-point accuracy)
// - mix: wet/dry blend (1.0=full filter, 0.7=subtle for warm voices)
//
// Hum notch parameters (when DS201HumHarmonics > 0):
// - frequency: fundamental (50Hz UK/EU, 60Hz US)
// - harmonics: number of harmonics to filter (1-4, 0=disabled)
// - width: notch width in Hz
// - transform: zdf for minimal ringing
//
// Returns combined filter chain: highpass=...,bandreject=...,bandreject=...
func (cfg *FilterChainConfig) buildDS201HighpassFilter() string {
	if !cfg.DS201HPEnabled {
		return ""
	}

	var filters []string

	// 1. Build high-pass filter for rumble removal
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

	filters = append(filters, hpSpec)

	// 2. Add hum notch filters if harmonics > 0
	if cfg.DS201HumHarmonics > 0 && cfg.DS201HumFrequency > 0 {
		for harmonic := 1; harmonic <= cfg.DS201HumHarmonics; harmonic++ {
			freq := cfg.DS201HumFrequency * float64(harmonic)
			// Skip frequencies above Nyquist for 44.1kHz output (22050Hz)
			if freq >= 22000 {
				break
			}

			// Build filter with Hz-based width for consistent notch size across harmonics
			// width_type=h specifies width in Hz (more predictable than Q)
			notchSpec := fmt.Sprintf("bandreject=f=%.0f:width_type=h:w=%.2f", freq, cfg.DS201HumWidth)

			// Add transform type if specified (zdf = zero delay feedback, less ringing)
			if cfg.DS201HumTransform != "" {
				notchSpec += fmt.Sprintf(":a=%s", cfg.DS201HumTransform)
			}

			// Add mix parameter if not full wet (1.0)
			if cfg.DS201HumMix > 0 && cfg.DS201HumMix < 1.0 {
				notchSpec += fmt.Sprintf(":m=%.2f", cfg.DS201HumMix)
			}

			filters = append(filters, notchSpec)
		}
	}

	// Join all filters with commas
	return strings.Join(filters, ",")
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

// buildDC1DeclickFilter builds the CEDAR DC-1-inspired declicker filter specification.
// Uses FFmpeg's adeclick filter with AR interpolation for click/pop removal.
// Parameters are tuned adaptively based on Pass 1 measurements.
func (cfg *FilterChainConfig) buildDC1DeclickFilter() string {
	if !cfg.DC1DeclickEnabled {
		return ""
	}
	return fmt.Sprintf("adeclick=window=%.0f:overlap=%.0f:arorder=%.0f:threshold=%.0f:burst=%.0f:method=%s",
		cfg.DC1DeclickWindow,
		cfg.DC1DeclickOverlap,
		cfg.DC1DeclickAROrder,
		cfg.DC1DeclickThreshold,
		cfg.DC1DeclickBurst,
		cfg.DC1DeclickMethod,
	)
}

// buildDolbySRFilter builds the Dolby SR-inspired 6-band multiband compander filter.
// Implements the Dolby SR noise reduction philosophy using mcompand:
// - FLAT reduction curve: identical dB reduction at all points below threshold (artifact-free)
// - Voice-protective band scaling: F1/F2 bands get slightly more expansion
// - Linkwitz-Riley crossover compensation via separate volume filter
//
// The mcompand filter uses expansion (below-threshold reduction) rather than compression,
// which naturally reduces room noise during quiet passages without affecting speech.
func (cfg *FilterChainConfig) buildDolbySRFilter() string {
	if !cfg.DolbySREnabled {
		return ""
	}

	// Use default bands if not set
	bands := cfg.DolbySRBands
	if len(bands) == 0 {
		bands = defaultDolbySRBands()
	}

	// Use default expansion if not set
	baseExp := cfg.DolbySRExpansionDB
	if baseExp == 0 {
		baseExp = DolbySRDefaultExpansionDB
	}

	// Use default threshold if not set (adaptive based on RMS trough)
	threshold := cfg.DolbySRThresholdDB
	if threshold == 0 {
		threshold = DolbySRDefaultThresholdDB
	}

	// Build per-band specifications
	var bandSpecs []string
	for _, band := range bands {
		// Calculate per-band expansion with voice-protective scaling
		bandExp := baseExp * band.ScalePercent / 100.0

		// Build FLAT reduction curve: identical reduction at all points below threshold
		// This is the key insight that eliminates pumping artifacts
		curve := buildFlatReductionCurve(bandExp, threshold)

		// Format: attack\,decay soft-knee curve crossover
		// Note: commas in attack,decay pair must be escaped for mcompand
		spec := fmt.Sprintf("%.3f\\,%.3f %.0f %s %.0f",
			band.Attack,
			band.Decay,
			band.SoftKnee,
			curve,
			band.CrossoverHz,
		)
		bandSpecs = append(bandSpecs, spec)
	}

	// Join bands with escaped pipe separators (space before and after)
	filter := "mcompand=args=" + strings.Join(bandSpecs, " \\| ")

	// Note: Linkwitz-Riley crossover compensation is applied in the LA2A compressor
	// makeup gain when DolbySR is enabled (see tuneLA2AMakeup in adaptive.go).
	// This simplifies gain staging by consolidating makeup gains in one place.

	return filter
}

// buildFlatReductionCurve creates a FLAT reduction curve for mcompand.
// FLAT means all points below the threshold receive identical dB reduction.
// This eliminates pumping/breathing artifacts that occur with true expansion curves.
//
// The threshold is adaptive based on RMS trough (noise floor indicator):
//   - Clean sources (trough < -85 dB): -50 dB threshold
//   - Moderate noise (-85 to -80 dB): -47 dB threshold
//   - Noisy sources (trough > -80 dB): -45 dB threshold
//
// Curve format: -90/out90,-75/out75,threshold/threshold,-30/-30,0/0
// Where out90 = -90 - expansion and out75 = -75 - expansion
func buildFlatReductionCurve(expansionDB, thresholdDB float64) string {
	out90 := -90 - expansionDB
	out75 := -75 - expansionDB
	// Note: commas between points must be escaped for mcompand args parameter
	return fmt.Sprintf("-90/%.0f\\,-75/%.0f\\,%.0f/%.0f\\,-30/-30\\,0/0", out90, out75, thresholdDB, thresholdDB)
}

// buildDNS1500Filter builds the CEDAR DNS-1500-inspired noise reduction filter.
// Uses FFmpeg's afftdn with inline noise learning via asendcmd during detected silence.
// This is the primary noise reduction filter when valid silence timestamps are available.
// Falls back to DolbySR (mcompand) when no suitable silence is detected.
//
// Filter graph structure:
//
//	asendcmd=c='start_time afftdn@dns1500 sn start; end_time afftdn@dns1500 sn stop',
//	afftdn@dns1500=nr=X:nf=Y:tn=1:ad=Z:gs=W:rf=R
//
// The asendcmd filter sends commands at specific timestamps:
//   - At silence start: triggers noise sampling start
//   - At silence end: stops noise sampling and locks the learned profile
//
// afftdn parameters (all adaptive via tuneDNS1500):
//   - nr: noise reduction amount (0.01-97 dB)
//   - nf: noise floor level (-80 to -20 dB)
//   - tn: track noise continuously (0/1)
//   - ad: adaptivity rate (0-1, higher = slower adaptation)
//   - gs: gain smoothing across frequency bins (0-50)
//   - rf: residual floor level (-80 to -20 dB)
func (cfg *FilterChainConfig) buildDNS1500Filter() string {
	if !cfg.DNS1500Enabled {
		return ""
	}

	// Silence timing comes from NoiseProfile (validated by tuneDNS1500)
	silenceStart := cfg.DNS1500SilenceStart
	silenceEnd := cfg.DNS1500SilenceEnd

	// Build asendcmd command string
	// Format: asendcmd=c='time1 target command arg; time2 target command arg'
	// Note: timestamps are bare values; brackets are only for FLAGS like [enter]/[leave]
	cmdSpec := fmt.Sprintf(
		"asendcmd=c='%.3f afftdn@dns1500 sn start; %.3f afftdn@dns1500 sn stop'",
		silenceStart,
		silenceEnd,
	)

	// Build afftdn filter with instance name for asendcmd targeting
	afftdnSpec := fmt.Sprintf(
		"afftdn@dns1500=nr=%.1f:nf=%.1f:tn=%d:ad=%.2f:gs=%d:rf=%.1f",
		cfg.DNS1500NoiseReduce,
		cfg.DNS1500NoiseFloor,
		boolToInt(cfg.DNS1500TrackNoise),
		cfg.DNS1500Adaptivity,
		cfg.DNS1500GainSmooth,
		cfg.DNS1500ResidFloor,
	)

	// Return combined filter chain
	return fmt.Sprintf("%s,%s", cmdSpec, afftdnSpec)
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

// buildArnnDnFilter builds the arnndn (RNN denoise) filter specification.
// Neural network noise reduction for heavily uplifted audio.
// Uses embedded RNN model (bd, cb, or sh) trained for recorded speech.
func (cfg *FilterChainConfig) buildArnnDnFilter() string {
	if !cfg.ArnnDnEnabled {
		return ""
	}

	// Use default model if not specified
	model := cfg.ArnnDnModel
	if model == "" {
		model = DefaultRNNModel
	}

	modelPath, err := getRNNModelPath(model)
	if err != nil {
		// Gracefully degrade if model unavailable
		return ""
	}

	return fmt.Sprintf("arnndn=m=%s:mix=%.2f", modelPath, cfg.ArnnDnMix)
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
// Filter order is determined by cfg.FilterOrder (or DefaultFilterOrder if empty).
// Each filter checks its Enabled flag and returns empty string if disabled.
// Uses the package-level filterBuilders registry for filter spec generation.
func (cfg *FilterChainConfig) BuildFilterSpec() string {
	// Use configured order or default
	order := cfg.FilterOrder
	if len(order) == 0 {
		order = DefaultFilterOrder
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
