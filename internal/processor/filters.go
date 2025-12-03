// Package processor handles audio analysis and processing
package processor

import (
	_ "embed"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

//go:embed models/cb.rnnn
var rnnModelData []byte

// FilterID identifies a filter in the processing chain
type FilterID string

// Filter identifiers for the audio processing chain
const (
	FilterHighpass    FilterID = "highpass"
	FilterAdeclick    FilterID = "adeclick"
	FilterAfftdn      FilterID = "afftdn"
	FilterAgate       FilterID = "agate"
	FilterAcompressor FilterID = "acompressor"
	FilterDeesser     FilterID = "deesser"
	FilterSpeechnorm  FilterID = "speechnorm"
	FilterArnndn      FilterID = "arnndn"
	FilterAnlmdn      FilterID = "anlmdn"
	FilterDynaudnorm  FilterID = "dynaudnorm"
	FilterAlimiter    FilterID = "alimiter"
)

// DefaultFilterOrder defines the standard filter chain order for podcast audio processing.
// Order rationale:
// - Highpass first: removes rumble before it affects other filters
// - Adeclick early: removes impulse noise before spectral processing
// - Afftdn: spectral noise reduction on clean signal
// - Agate: removes low-level noise between speech
// - Acompressor: evens dynamics before normalisation
// - Deesser: after compression (which emphasises sibilance)
// - Speechnorm: cycle-level normalisation for speech
// - Arnndn/Anlmdn: mop up amplified noise after expansion
// - Dynaudnorm: frame-level normalisation for final consistency
// - Alimiter: brick-wall safety net at the end
var DefaultFilterOrder = []FilterID{
	FilterHighpass,
	FilterAdeclick,
	FilterAfftdn,
	FilterAgate,
	FilterAcompressor,
	FilterDeesser,
	FilterSpeechnorm,
	FilterArnndn,
	FilterAnlmdn,
	FilterDynaudnorm,
	FilterAlimiter,
}

var (
	cachedModelPath string
	modelCacheMutex sync.Mutex
)

// getRNNModelPath returns the path to the cached RNN model file.
// On first call, it extracts the embedded model to ~/.cache/jivetalking/cb.rnnn
// Subsequent calls return the cached path. Thread-safe.
func getRNNModelPath() (string, error) {
	modelCacheMutex.Lock()
	defer modelCacheMutex.Unlock()

	// Return cached path if already extracted
	if cachedModelPath != "" {
		return cachedModelPath, nil
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
	modelPath := filepath.Join(jiveCacheDir, "cb.rnnn")

	// Check if model already exists
	if _, err := os.Stat(modelPath); err == nil {
		// Model already cached
		cachedModelPath = modelPath
		return cachedModelPath, nil
	}

	// Write embedded model to cache
	if err := os.WriteFile(modelPath, rnnModelData, 0644); err != nil {
		return "", fmt.Errorf("failed to write model file: %w", err)
	}

	cachedModelPath = modelPath
	return cachedModelPath, nil
}

// FilterChainConfig holds configuration for the audio processing filter chain
type FilterChainConfig struct {
	// High-Pass Filter (highpass) - removes subsonic rumble
	HighpassEnabled bool    // Enable highpass filter
	HighpassFreq    float64 // Hz, cutoff frequency (removes frequencies below this)

	// Click/Pop Removal (adeclick) - removes clicks and pops
	AdeclickEnabled bool   // Enable adeclick filter
	AdeclickMethod  string // 'a' = overlap-add, 's' = overlap-save (default: 's')

	// Noise Reduction (afftdn)
	AfftdnEnabled  bool    // Enable afftdn filter
	NoiseFloor     float64 // dB, estimated noise floor from Pass 1
	NoiseReduction float64 // dB, reduction amount
	NoiseTrack     bool    // Enable automatic noise tracking (tn=1)

	// Gate (agate) - removes silence and low-level noise
	GateEnabled   bool    // Enable agate filter
	GateThreshold float64 // Activation threshold (0.0-1.0, linear)
	GateRatio     float64 // Reduction ratio (1.0-9000.0)
	GateAttack    float64 // Attack time (ms)
	GateRelease   float64 // Release time (ms)
	GateRange     float64 // Level of gain reduction below threshold (0.0-1.0)
	GateKnee      float64 // Knee curve softness (1.0-8.0)
	GateMakeup    float64 // Makeup gain after gating (1.0-64.0)

	// Compression (acompressor) - evens out dynamic range
	CompEnabled   bool    // Enable acompressor filter
	CompThreshold float64 // dB, compression threshold (stored in dB, converted to linear)
	CompRatio     float64 // Compression ratio (1.0-20.0)
	CompAttack    float64 // Attack time (ms)
	CompRelease   float64 // Release time (ms)
	CompMakeup    float64 // dB, makeup gain (stored in dB, converted to linear)
	CompKnee      float64 // Knee curve softness (1.0-8.0)
	CompMix       float64 // Wet/dry mix (0.0-1.0, 1.0 = 100% compressed)

	// De-esser (deesser) - removes harsh sibilance automatically
	DeessEnabled   bool    // Enable deesser filter
	DeessIntensity float64 // 0.0-1.0, intensity for triggering de-essing (0=off, 1=max)
	DeessAmount    float64 // 0.0-1.0, amount of ducking on treble (how much to reduce)
	DeessFreq      float64 // 0.0-1.0, how much original frequency content to keep

	// Target values (for reference only)
	TargetI   float64 // LUFS target reference (podcast standard: -16)
	TargetTP  float64 // dBTP, true peak ceiling reference
	TargetLRA float64 // LU, loudness range reference

	// Dynamic Audio Normalizer (dynaudnorm) - primary normalization method
	DynaudnormEnabled     bool    // Enable dynaudnorm filter
	DynaudnormFrameLen    int     // Frame length in milliseconds (10-8000, default 500)
	DynaudnormFilterSize  int     // Filter size for Gaussian filter (3-301, default 31)
	DynaudnormPeakValue   float64 // Target peak value 0.0-1.0 (default 0.95)
	DynaudnormMaxGain     float64 // Maximum gain factor (1.0-100.0, default 10.0)
	DynaudnormTargetRMS   float64 // Target RMS 0.0-1.0 (default 0.0 = disabled)
	DynaudnormCompress    float64 // Compression factor 0.0-30.0 (default 0.0 = disabled)
	DynaudnormThreshold   float64 // Minimum magnitude to normalize 0.0-1.0 (default 0.0 = all frames)
	DynaudnormChannels    bool    // Process channels independently (default false = coupled)
	DynaudnormDCCorrect   bool    // Enable DC bias correction (default false)
	DynaudnormAltBoundary bool    // Enable alternative boundary mode (default false)

	// Speech Normalizer (speechnorm) - alternative normalization method
	SpeechnormEnabled     bool    // Enable speechnorm filter
	SpeechnormPeak        float64 // Target peak value 0.0-1.0 (default 0.95)
	SpeechnormExpansion   float64 // Max expansion factor 1.0-50.0 (default 2.0)
	SpeechnormCompression float64 // Max compression factor 1.0-50.0 (default 2.0)
	SpeechnormThreshold   float64 // Threshold below which to stop normalization 0.0-1.0 (default 0.0)
	SpeechnormRaise       float64 // Smoothing for peak rise 0.0-1.0 (default 0.001)
	SpeechnormFall        float64 // Smoothing for peak fall 0.0-1.0 (default 0.001)
	SpeechnormRMS         float64 // Target RMS value 0.0-1.0 (default 0.0 = disabled)
	SpeechnormChannels    bool    // Process channels independently (default false = coupled)

	// RNN Denoise (arnndn) - neural network noise reduction for heavily uplifted audio
	ArnnDnEnabled bool    // Enable RNN denoise (adaptive, for heavily uplifted audio)
	ArnnDnMix     float64 // Mix amount -1.0 to 1.0 (1.0 = full filtering, negative = keep noise)

	// Non-Local Means Denoise (anlmdn) - patch-based noise reduction for heavily uplifted audio
	AnlmDnEnabled  bool    // Enable NLM denoise (adaptive, for heavily uplifted audio)
	AnlmDnStrength float64 // Denoising strength 0.00001-10000.0 (default 0.00001)
	AnlmDnPatch    float64 // Patch radius in milliseconds 1-100ms (default 2ms)
	AnlmDnResearch float64 // Research radius in milliseconds 2-300ms (default 6ms)

	// True Peak Limiter (alimiter) - brick-wall safety net
	LimiterEnabled bool    // Enable alimiter filter
	LimiterCeiling float64 // 0.0625-1.0, peak ceiling (0.98 = -0.17dBFS)
	LimiterAttack  float64 // ms, attack time
	LimiterRelease float64 // ms, release time

	// Filter chain order - controls the sequence of filters in the processing chain
	// Use DefaultFilterOrder or customise for experimentation
	FilterOrder []FilterID

	// Pass 1 measurements (nil for first pass)
	Measurements *AudioMeasurements
}

// DefaultFilterConfig returns the scientifically-tuned default filter configuration
// for podcast spoken word audio processing
func DefaultFilterConfig() *FilterChainConfig {
	return &FilterChainConfig{
		// High-pass - remove subsonic rumble
		HighpassEnabled: true,
		HighpassFreq:    80.0, // 80Hz cutoff

		// Click/Pop Removal - use overlap-save method with defaults
		AdeclickEnabled: false, // Disabled: causes artifacts on some recordings
		AdeclickMethod:  "s",   // overlap-save (default for better quality)

		// Noise Reduction - will use Pass 1 noise floor estimate
		AfftdnEnabled:  true,
		NoiseFloor:     -25.0, // Placeholder, will be updated from measurements
		NoiseReduction: 12.0,  // 12 dB reduction (FFT denoise default, good for speech)
		NoiseTrack:     true,  // Enable adaptive tracking

		// Gate - remove silence and low-level noise between speech
		// Threshold will be set adaptively based on noise floor in Pass 2
		GateEnabled:   true,
		GateThreshold: 0.01,   // -40dBFS default (will be adaptive)
		GateRatio:     2.0,    // 2:1 expansion ratio (gentle, preserves natural pauses)
		GateAttack:    20,     // 20ms attack (protects speech onset, prevents clipping)
		GateRelease:   250,    // 250ms release (smooth, natural decay)
		GateRange:     0.0625, // -24dB reduction (moderate, avoids voice ducking)
		GateKnee:      2.828,  // Soft knee (2.828 = default, smooth engagement)
		GateMakeup:    1.0,    // No makeup gain (normalization handled by dynaudnorm)

		// Compression - even out dynamics naturally
		// LA-2A-style gentle compression for podcast speech
		CompEnabled:   true,
		CompThreshold: -20, // -20dB threshold (gentle, preserves dynamics)
		CompRatio:     2.5, // 2.5:1 ratio (gentle compression)
		CompAttack:    15,  // 15ms attack (preserves transients)
		CompRelease:   80,  // 80ms release (smooth, natural)
		CompMakeup:    3,   // 3dB makeup gain (compensate for reduction)
		CompKnee:      2.5, // Soft knee for smooth compression
		CompMix:       1.0, // 100% compressed signal (no parallel compression)

		// De-esser - automatic sibilance reduction
		DeessEnabled:   true, // Enabled, but intensity set adaptively (0.0 = effectively off)
		DeessIntensity: 0.0,  // 0.0 = disabled by default, will be set adaptively if enabled
		DeessAmount:    0.5,  // 50% ducking on treble (moderate reduction)
		DeessFreq:      0.5,  // Keep 50% of original frequency content (balanced)

		// Target values (for reference only)
		TargetI:   -16.0, // Reference LUFS target (not enforced)
		TargetTP:  -0.3,  // Reference true peak (not enforced, alimiter does real limiting at -1.5)
		TargetLRA: 7.0,   // Reference loudness range (EBU R128 default)

		// Dynamic Audio Normalizer - adaptive loudness normalization
		DynaudnormEnabled:     true,
		DynaudnormFrameLen:    500,   // 500ms frames (default, good for speech)
		DynaudnormFilterSize:  31,    // Gaussian filter size (default, smooth transitions)
		DynaudnormPeakValue:   0.95,  // Target peak 0.95 (default, leaves headroom)
		DynaudnormMaxGain:     10.0,  // Maximum 10x gain (default, prevents over-amplification)
		DynaudnormTargetRMS:   0.0,   // Disabled (default, use peak normalization only)
		DynaudnormCompress:    0.0,   // No compression (default, preserve dynamics)
		DynaudnormThreshold:   0.0,   // Normalize all frames (default)
		DynaudnormChannels:    false, // Coupled channels (default, mono so no effect)
		DynaudnormDCCorrect:   false, // No DC correction (default)
		DynaudnormAltBoundary: false, // Standard boundary mode (default)

		// Speech Normalizer - alternative cycle-level normalization
		SpeechnormEnabled:     true,
		SpeechnormPeak:        0.95,  // Target peak 0.95 (matches dynaudnorm)
		SpeechnormExpansion:   3.0,   // Max 3x expansion (moderate, tames loud peaks)
		SpeechnormCompression: 2.0,   // Max 2x compression (gentle, lifts quiet sections)
		SpeechnormThreshold:   0.10,  // Threshold 0.10 (normalize above this level)
		SpeechnormRaise:       0.001, // Fast rise smoothing (responsive to speech onsets)
		SpeechnormFall:        0.001, // Fast fall smoothing (responsive to speech offsets)
		SpeechnormRMS:         0.0,   // RMS targeting disabled by default (will be set adaptively)
		SpeechnormChannels:    false, // Coupled channels (default, mono so no effect)

		// RNN Denoise - neural network noise reduction
		ArnnDnEnabled: false, // Disabled by default (will be enabled adaptively for heavily uplifted audio)
		ArnnDnMix:     0.8,   // Full filtering when enabled (1.0 = 100% denoised)

		// Non-Local Means Denoise - patch-based noise reduction
		AnlmDnEnabled:  false,   // Disabled by default (will be enabled adaptively for heavily uplifted audio)
		AnlmDnStrength: 0.00001, // Very conservative default (will be set adaptively)
		AnlmDnPatch:    2.0,     // 2ms patch radius (default from docs)
		AnlmDnResearch: 6.0,     // 6ms research radius (default from docs)

		// Limiter - brick-wall safety net with soft knee (via ASC)
		LimiterEnabled: true,
		LimiterCeiling: 0.84, // -1.5dBTP (actual limiting target)
		LimiterAttack:  5.0,  // 5ms lookahead for smooth limiting
		LimiterRelease: 50.0, // 50ms release for natural sound

		// Filter chain order - use default order
		FilterOrder: DefaultFilterOrder,

		Measurements: nil, // Will be set after Pass 1
	}
}

// dbToLinear converts decibel value to linear amplitude
// Used for converting dB parameters to FFmpeg's linear format
func dbToLinear(db float64) float64 {
	return math.Pow(10, db/20.0)
}

// boolToInt converts a bool to 0 or 1 for FFmpeg filter parameters
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// buildHighpassFilter builds the highpass (rumble removal) filter specification.
// Removes subsonic frequencies below cutoff (HVAC, handling noise, etc.)
// Uses Butterworth response (Q=0.707) for maximally flat passband.
// poles=2 gives 12dB/octave rolloff, normalize=1 prevents level shift.
func (cfg *FilterChainConfig) buildHighpassFilter() string {
	if !cfg.HighpassEnabled {
		return ""
	}
	return fmt.Sprintf("highpass=f=%.0f:poles=2:width_type=q:width=0.707:normalize=1",
		cfg.HighpassFreq)
}

// buildAdeclickFilter builds the adeclick (click/pop removal) filter specification.
// Removes clicks, pops, and impulse noise using AR model.
// m: method ('a' = overlap-add, 's' = overlap-save for better quality)
func (cfg *FilterChainConfig) buildAdeclickFilter() string {
	if !cfg.AdeclickEnabled {
		return ""
	}
	return fmt.Sprintf("adeclick=m=%s", cfg.AdeclickMethod)
}

// buildAfftdnFilter builds the afftdn (FFT denoise) filter specification.
// Removes noise using FFT analysis with adaptive tracking.
// Parameters adapt based on noise reduction amount for optimal quality.
func (cfg *FilterChainConfig) buildAfftdnFilter() string {
	if !cfg.AfftdnEnabled {
		return ""
	}

	// Clamp noise floor to afftdn's valid range: -80 to -20 dB
	noiseFloorClamped := cfg.NoiseFloor
	if noiseFloorClamped < -80.0 {
		noiseFloorClamped = -80.0
	} else if noiseFloorClamped > -20.0 {
		noiseFloorClamped = -20.0
	}

	// Adaptive parameters based on noise reduction amount
	var residualFloor, adaptivity float64
	var gainSmooth int

	if cfg.NoiseReduction >= 30.0 {
		// Aggressive noise reduction for very quiet sources
		residualFloor = -70.0
		adaptivity = 0.2
		gainSmooth = 12
	} else if cfg.NoiseReduction >= 20.0 {
		// Moderate noise reduction
		residualFloor = -50.0
		adaptivity = 0.3
		gainSmooth = 8
	} else {
		// Light noise reduction
		residualFloor = -38.0
		adaptivity = 0.5
		gainSmooth = 5
	}

	return fmt.Sprintf(
		"afftdn=nf=%.1f:nr=%.1f:tn=%d:rf=%.1f:ad=%.2f:fo=%.1f:gs=%d:om=%s:nt=%s",
		noiseFloorClamped,
		cfg.NoiseReduction,
		boolToInt(cfg.NoiseTrack),
		residualFloor,
		adaptivity,
		1.0, // Floor offset factor
		gainSmooth,
		"o", // Output mode: filtered audio
		"w", // Noise type: white noise
	)
}

// buildAgateFilter builds the agate (noise gate) filter specification.
// Removes low-level noise between speech while preserving natural pauses.
// Uses RMS detection for smooth, natural speech gating.
func (cfg *FilterChainConfig) buildAgateFilter() string {
	if !cfg.GateEnabled {
		return ""
	}
	return fmt.Sprintf(
		"agate=threshold=%.3f:ratio=%.1f:attack=%.0f:release=%.0f:"+
			"range=%.3f:knee=%.1f:detection=rms:makeup=%.1f",
		cfg.GateThreshold,
		cfg.GateRatio,
		cfg.GateAttack,
		cfg.GateRelease,
		cfg.GateRange,
		cfg.GateKnee,
		cfg.GateMakeup,
	)
}

// buildAcompressorFilter builds the acompressor (dynamics) filter specification.
// Evens out dynamic range with soft knee compression.
// Converts dB values to linear for FFmpeg's format.
func (cfg *FilterChainConfig) buildAcompressorFilter() string {
	if !cfg.CompEnabled {
		return ""
	}
	return fmt.Sprintf(
		"acompressor=threshold=%.6f:ratio=%.1f:attack=%.0f:release=%.0f:"+
			"makeup=%.2f:knee=%.1f:detection=rms:mix=%.2f",
		dbToLinear(cfg.CompThreshold),
		cfg.CompRatio,
		cfg.CompAttack,
		cfg.CompRelease,
		dbToLinear(cfg.CompMakeup),
		cfg.CompKnee,
		cfg.CompMix,
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

// buildDynaudnormFilter builds the dynaudnorm (Dynamic Audio Normalizer) filter specification.
// Provides adaptive local normalization with Gaussian smoothing.
// Conservative settings prevent over-amplification while normalizing levels.
func (cfg *FilterChainConfig) buildDynaudnormFilter() string {
	if !cfg.DynaudnormEnabled {
		return ""
	}
	return fmt.Sprintf(
		"dynaudnorm=f=%d:g=%d:p=%.2f:m=%.1f:r=%.3f:t=%.6f:n=%d:c=%d:b=%d:s=%.1f",
		cfg.DynaudnormFrameLen,
		cfg.DynaudnormFilterSize,
		cfg.DynaudnormPeakValue,
		cfg.DynaudnormMaxGain,
		cfg.DynaudnormTargetRMS,
		cfg.DynaudnormThreshold,
		boolToInt(cfg.DynaudnormChannels),
		boolToInt(cfg.DynaudnormDCCorrect),
		boolToInt(cfg.DynaudnormAltBoundary),
		cfg.DynaudnormCompress,
	)
}

// buildSpeechnormFilter builds the speechnorm (Speech Normalizer) filter specification.
// Cycle-level normalization using zero-crossing half-cycles.
// Fast, speech-optimized alternative to dynaudnorm's frame-based approach.
func (cfg *FilterChainConfig) buildSpeechnormFilter() string {
	if !cfg.SpeechnormEnabled {
		return ""
	}
	return fmt.Sprintf(
		"speechnorm=p=%.2f:e=%.1f:c=%.1f:t=%.2f:r=%.3f:f=%.3f:m=%.3f:l=%d",
		cfg.SpeechnormPeak,
		cfg.SpeechnormExpansion,
		cfg.SpeechnormCompression,
		cfg.SpeechnormThreshold,
		cfg.SpeechnormRaise,
		cfg.SpeechnormFall,
		cfg.SpeechnormRMS,
		boolToInt(cfg.SpeechnormChannels),
	)
}

// buildArnnDnFilter builds the arnndn (RNN denoise) filter specification.
// Neural network noise reduction for heavily uplifted audio.
// Uses embedded conjoined-burgers model trained for recorded speech.
func (cfg *FilterChainConfig) buildArnnDnFilter() string {
	if !cfg.ArnnDnEnabled {
		return ""
	}
	modelPath, err := getRNNModelPath()
	if err != nil {
		// Gracefully degrade if model unavailable
		return ""
	}
	return fmt.Sprintf("arnndn=m=%s:mix=%.2f", modelPath, cfg.ArnnDnMix)
}

// buildAnlmDnFilter builds the anlmdn (Non-Local Means denoise) filter specification.
// Patch-based noise reduction for heavily uplifted audio.
// Better at preserving texture/detail than RNN approach.
func (cfg *FilterChainConfig) buildAnlmDnFilter() string {
	if !cfg.AnlmDnEnabled {
		return ""
	}
	return fmt.Sprintf("anlmdn=s=%f", cfg.AnlmDnStrength)
}

// buildAlimiterFilter builds the alimiter (true peak limiter) filter specification.
// Brick-wall safety net using lookahead and ASC for smooth, musical limiting.
func (cfg *FilterChainConfig) buildAlimiterFilter() string {
	if !cfg.LimiterEnabled {
		return ""
	}
	return fmt.Sprintf(
		"alimiter=level_in=%.2f:level_out=%.2f:limit=%.2f:"+
			"attack=%.0f:release=%.0f:asc=%d:asc_level=%.1f:level=%d:latency=%d",
		1.0, // No input gain adjustment
		1.0, // No output gain adjustment
		cfg.LimiterCeiling,
		cfg.LimiterAttack,
		cfg.LimiterRelease,
		1,   // Enable ASC
		0.5, // Moderate ASC influence
		0,   // Disable auto-level normalization
		1,   // Enable latency compensation
	)
}

// BuildFilterSpec builds the FFmpeg filter specification string for Pass 2 processing.
// Filter order is determined by cfg.FilterOrder (or DefaultFilterOrder if empty).
// Each filter checks its Enabled flag and returns empty string if disabled.
func (cfg *FilterChainConfig) BuildFilterSpec() string {
	// Map FilterID to builder method
	builders := map[FilterID]func() string{
		FilterHighpass:    cfg.buildHighpassFilter,
		FilterAdeclick:    cfg.buildAdeclickFilter,
		FilterAfftdn:      cfg.buildAfftdnFilter,
		FilterAgate:       cfg.buildAgateFilter,
		FilterAcompressor: cfg.buildAcompressorFilter,
		FilterDeesser:     cfg.buildDeesserFilter,
		FilterSpeechnorm:  cfg.buildSpeechnormFilter,
		FilterArnndn:      cfg.buildArnnDnFilter,
		FilterAnlmdn:      cfg.buildAnlmDnFilter,
		FilterDynaudnorm:  cfg.buildDynaudnormFilter,
		FilterAlimiter:    cfg.buildAlimiterFilter,
	}

	// Use configured order or default
	order := cfg.FilterOrder
	if len(order) == 0 {
		order = DefaultFilterOrder
	}

	// Build filters in specified order, skipping disabled/empty
	var filters []string
	for _, id := range order {
		if builder, ok := builders[id]; ok {
			if spec := builder(); spec != "" {
				filters = append(filters, spec)
			}
		}
	}

	// Add output format filters (always enabled)
	// aformat: podcast-standard output (44.1kHz, mono, s16)
	// asetnsamples: fixed frame size for FLAC encoder
	filters = append(filters, "aformat=sample_rates=44100:channel_layouts=mono:sample_fmts=s16")
	filters = append(filters, "asetnsamples=n=4096")

	// Join with commas
	filterChain := ""
	for i, f := range filters {
		if i > 0 {
			filterChain += ","
		}
		filterChain += f
	}

	return filterChain
}

// CreateProcessingFilterGraph creates an AVFilterGraph for complete audio processing
// This is used in Pass 2 to apply the full filter chain
func CreateProcessingFilterGraph(
	decCtx *ffmpeg.AVCodecContext,
	config *FilterChainConfig,
) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {

	filterGraph := ffmpeg.AVFilterGraphAlloc()
	if filterGraph == nil {
		return nil, nil, nil, fmt.Errorf("failed to allocate filter graph")
	}

	// Get abuffer and abuffersink filters
	bufferSrc := ffmpeg.AVFilterGetByName(ffmpeg.GlobalCStr("abuffer"))
	bufferSink := ffmpeg.AVFilterGetByName(ffmpeg.GlobalCStr("abuffersink"))
	if bufferSrc == nil || bufferSink == nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("abuffer or abuffersink filter not found")
	}

	// Get channel layout description
	layoutPtr := ffmpeg.AllocCStr(64)
	defer layoutPtr.Free()

	if _, err := ffmpeg.AVChannelLayoutDescribe(decCtx.ChLayout(), layoutPtr, 64); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to get channel layout: %w", err)
	}

	// Create abuffer source
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
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to create abuffer: %w", err)
	}

	// Create abuffersink
	var bufferSinkCtx *ffmpeg.AVFilterContext
	if _, err := ffmpeg.AVFilterGraphCreateFilter(
		&bufferSinkCtx,
		bufferSink,
		ffmpeg.GlobalCStr("out"),
		nil,
		nil,
		filterGraph,
	); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to create abuffersink: %w", err)
	}

	// Build the complete filter specification
	filterSpec := config.BuildFilterSpec()

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
