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
	HighpassFreq float64 // Hz, cutoff frequency (removes frequencies below this)

	// Click/Pop Removal (adeclick) - removes clicks and pops
	AdeclickMethod string // 'a' = overlap-add, 's' = overlap-save (default: 's')

	// Noise Reduction (afftdn)
	NoiseFloor     float64 // dB, estimated noise floor from Pass 1
	NoiseReduction float64 // 0.0-1.0, reduction amount
	NoiseTrack     bool    // Enable automatic noise tracking (tn=1)

	NoiseStrength     float64 // Set denoising strength. Allowed range is from 0.00001 to 10000. Default value is 0.00001.
	NoisePatchSize    float64 // Set patch radius duration. Allowed range is from 1 to 100 milliseconds. Default value is 2 milliseconds.
	NoiseResearchSize float64 // Set research radius duration. Allowed range is from 2 to 300 milliseconds. Default value is 6 milliseconds.
	NoiseSmoothFactor float64 // Set smooth factor. Default value is 11. Allowed range is from 1 to 1000.
	NoiseOutputMode   string  // 'i' = input (bypass), 'n' = noise only, 'o' = output (processed)

	// Gate (agate) - removes silence and low-level noise
	GateThreshold float64 // Activation threshold (0.0-1.0, linear)
	GateRatio     float64 // Reduction ratio (1.0-9000.0)
	GateAttack    float64 // Attack time (ms)
	GateRelease   float64 // Release time (ms)
	GateRange     float64 // Level of gain reduction below threshold (0.0-1.0)
	GateKnee      float64 // Knee curve softness (1.0-8.0)
	GateMakeup    float64 // Makeup gain after gating (1.0-64.0)

	// Compression (acompressor) - evens out dynamic range
	CompThreshold float64 // dB, compression threshold (stored in dB, converted to linear)
	CompRatio     float64 // Compression ratio (1.0-20.0)
	CompAttack    float64 // Attack time (ms)
	CompRelease   float64 // Release time (ms)
	CompMakeup    float64 // dB, makeup gain (stored in dB, converted to linear)
	CompKnee      float64 // Knee curve softness (1.0-8.0)
	CompMix       float64 // Wet/dry mix (0.0-1.0, 1.0 = 100% compressed)

	// De-esser (deesser) - removes harsh sibilance automatically
	DeessIntensity float64 // 0.0-1.0, intensity for triggering de-essing (0=off, 1=max)
	DeessAmount    float64 // 0.0-1.0, amount of ducking on treble (how much to reduce)
	DeessFreq      float64 // 0.0-1.0, how much original frequency content to keep

	// Target values (for reference only)
	TargetI   float64 // LUFS target reference (podcast standard: -16)
	TargetTP  float64 // dBTP, true peak ceiling reference
	TargetLRA float64 // LU, loudness range reference

	// Dynamic Audio Normalizer (dynaudnorm) - primary normalization method
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
	LimiterCeiling float64 // 0.0625-1.0, peak ceiling (0.98 = -0.17dBFS)
	LimiterAttack  float64 // ms, attack time
	LimiterRelease float64 // ms, release time

	// Pass 1 measurements (nil for first pass)
	Measurements *AudioMeasurements
}

// DefaultFilterConfig returns the scientifically-tuned default filter configuration
// for podcast spoken word audio processing
func DefaultFilterConfig() *FilterChainConfig {
	return &FilterChainConfig{
		// High-pass - remove subsonic rumble
		HighpassFreq: 80.0, // 80Hz cutoff

		// Click/Pop Removal - use overlap-save method with defaults
		AdeclickMethod: "s", // overlap-save (default for better quality)

		// Noise Reduction - will use Pass 1 noise floor estimate
		NoiseFloor:     -25.0, // Placeholder, will be updated from measurements
		NoiseReduction: 12.0,  // 12 dB reduction (FFT denoise default, good for speech)
		NoiseTrack:     true,  // Enable adaptive tracking

		// Gate - remove silence and low-level noise between speech
		// Threshold will be set adaptively based on noise floor in Pass 2
		GateThreshold: 0.01,   // -40dBFS default (will be adaptive)
		GateRatio:     2.0,    // 2:1 expansion ratio (gentle, preserves natural pauses)
		GateAttack:    20,     // 20ms attack (protects speech onset, prevents clipping)
		GateRelease:   250,    // 250ms release (smooth, natural decay)
		GateRange:     0.0625, // -24dB reduction (moderate, avoids voice ducking)
		GateKnee:      2.828,  // Soft knee (2.828 = default, smooth engagement)
		GateMakeup:    1.0,    // No makeup gain (normalization handled by dynaudnorm)

		// Compression - even out dynamics naturally
		// LA-2A-style gentle compression for podcast speech
		CompThreshold: -20, // -20dB threshold (gentle, preserves dynamics)
		CompRatio:     2.5, // 2.5:1 ratio (gentle compression)
		CompAttack:    15,  // 15ms attack (preserves transients)
		CompRelease:   80,  // 80ms release (smooth, natural)
		CompMakeup:    3,   // 3dB makeup gain (compensate for reduction)
		CompKnee:      2.5, // Soft knee for smooth compression
		CompMix:       1.0, // 100% compressed signal (no parallel compression)

		// De-esser - automatic sibilance reduction
		DeessIntensity: 0.0, // 0.0 = disabled by default, will be set adaptively if enabled
		DeessAmount:    0.5, // 50% ducking on treble (moderate reduction)
		DeessFreq:      0.5, // Keep 50% of original frequency content (balanced)

		// Target values (for reference only)
		TargetI:   -16.0, // Reference LUFS target (not enforced)
		TargetTP:  -0.3,  // Reference true peak (not enforced, alimiter does real limiting at -1.5)
		TargetLRA: 7.0,   // Reference loudness range (EBU R128 default)

		// Dynamic Audio Normalizer - adaptive loudness normalization
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
		LimiterCeiling: 0.84, // -1.5dBTP (actual limiting target)
		LimiterAttack:  5.0,  // 5ms lookahead for smooth limiting
		LimiterRelease: 50.0, // 50ms release for natural sound

		Measurements: nil, // Will be set after Pass 1
	}
}

// dbToLinear converts decibel value to linear amplitude
// Used for converting dB parameters to FFmpeg's linear format
func dbToLinear(db float64) float64 {
	return math.Pow(10, db/20.0)
}

// BuildFilterSpec builds the FFmpeg filter specification string for Pass 2 processing
// Filter Chain: highpass → adeclick → afftdn → agate → acompressor → deesser → [dynaudnorm | speechnorm] → alimiter
// Note: dynaudnorm and speechnorm are mutually exclusive - use one or the other, not both
func (cfg *FilterChainConfig) BuildFilterSpec() string {
	// Build highpass (rumble removal) filter
	// Remove subsonic frequencies below 80Hz (HVAC, handling noise, etc.)
	// Use Butterworth response (Q=0.707) for maximally flat passband
	// poles=2 gives 12dB/octave rolloff, normalize=1 prevents level shift
	highpassFilter := fmt.Sprintf("highpass=f=%.0f:poles=2:width_type=q:width=0.707:normalize=1",
		cfg.HighpassFreq)

	// Build adeclick (click/pop removal) filter
	// Removes clicks, pops, and impulse noise using AR model
	// m: method ('a' = overlap-add, 's' = overlap-save for better quality)
	// Uses default values for arorder, threshold, and burst (suitable for most cases)
	adeclickFilter := fmt.Sprintf("adeclick=m=%s", cfg.AdeclickMethod)

	// Build afftdn (FFT denoise) filter
	// Removes noise using FFT analysis with adaptive tracking
	// nf: noise floor estimate from Pass 1, nr: noise reduction in dB
	// tn: enable adaptive tracking, rf: residual floor, ad: adaptivity speed
	// gs: gain smoothing to reduce musical noise artifacts
	noiseTrackFlag := 0
	if cfg.NoiseTrack {
		noiseTrackFlag = 1
	}

	// Clamp noise floor to afftdn's valid range: -80 to -20 dB
	noiseFloorClamped := cfg.NoiseFloor
	if noiseFloorClamped < -80.0 {
		noiseFloorClamped = -80.0
	} else if noiseFloorClamped > -20.0 {
		noiseFloorClamped = -20.0
	}

	// Adaptive afftdn parameters based on noise reduction amount
	// For aggressive noise reduction (>30dB), use more aggressive parameters
	var residualFloor float64
	var adaptivity float64
	var gainSmooth int

	if cfg.NoiseReduction >= 30.0 {
		// Aggressive noise reduction for very quiet sources
		residualFloor = -70.0 // Much lower residual floor (near minimum -80dB)
		adaptivity = 0.2      // Faster adaptation (more responsive to noise variations)
		gainSmooth = 12       // Higher smoothing to reduce musical noise artifacts
	} else if cfg.NoiseReduction >= 20.0 {
		// Moderate noise reduction
		residualFloor = -50.0
		adaptivity = 0.3
		gainSmooth = 8
	} else {
		// Light noise reduction
		residualFloor = -38.0 // Default
		adaptivity = 0.5      // Default
		gainSmooth = 5        // Default
	}

	afftdnFilter := fmt.Sprintf(
		"afftdn=nf=%.1f:nr=%.1f:tn=%d:rf=%.1f:ad=%.2f:fo=%.1f:gs=%d:om=%s:nt=%s",
		noiseFloorClamped,  // Noise floor from Pass 1 measurements (clamped to -80 to -20 dB)
		cfg.NoiseReduction, // Noise reduction amount (dB)
		noiseTrackFlag,     // Enable adaptive noise floor tracking
		residualFloor,      // Residual floor (adaptive, more aggressive for high reduction)
		adaptivity,         // Adaptivity factor (adaptive, faster for high reduction)
		1.0,                // Floor offset factor (adjustment to tracked floor)
		gainSmooth,         // Gain smooth radius (adaptive, higher for aggressive reduction)
		"o",                // Output mode: filtered audio
		"w",                // Noise type: white noise (typical for room/HVAC)
	)

	// Build agate (noise gate) filter
	// Gate removes low-level noise between speech while preserving natural pauses
	// range: amount of reduction below threshold, knee: soft engagement curve
	// detection=rms: smooth RMS detection for natural speech gating
	agateFilter := fmt.Sprintf(
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

	// Build acompressor (dynamics) filter
	// Convert dB values to linear: threshold (0.0-1.0), makeup (1.0-64.0 gain multiplier)
	// knee: soft compression curve, detection=rms: smooth RMS detection for speech
	acompressorFilter := fmt.Sprintf(
		"acompressor=threshold=%.6f:ratio=%.1f:attack=%.0f:release=%.0f:"+
			"makeup=%.2f:knee=%.1f:detection=rms:mix=%.2f",
		dbToLinear(cfg.CompThreshold), // Convert dB to linear (0.0-1.0)
		cfg.CompRatio,
		cfg.CompAttack,
		cfg.CompRelease,
		dbToLinear(cfg.CompMakeup), // Convert dB to linear gain multiplier
		cfg.CompKnee,
		cfg.CompMix,
	)

	// Build deesser filter
	// Purpose-built for sibilance removal - automatically detects and reduces harsh "s" sounds
	// i: intensity for triggering (0=off, higher=more aggressive detection)
	// m: amount of ducking on treble frequencies (how much to reduce sibilance)
	// f: how much of original frequency content to preserve (balance vs naturalness)
	// Skip deesser entirely if intensity is 0 or negative (adaptive logic disabled it)
	var deesserFilter string
	if cfg.DeessIntensity > 0 {
		deesserFilter = fmt.Sprintf(
			"deesser=i=%.2f:m=%.2f:f=%.2f",
			cfg.DeessIntensity, // Triggering sensitivity
			cfg.DeessAmount,    // Reduction amount
			cfg.DeessFreq,      // Frequency preservation
		)
	}
	// If DeessIntensity is 0, deesserFilter stays empty and will be skipped in filter chain

	// Build dynaudnorm (Dynamic Audio Normalizer) filter
	// Conservative fixed settings for adaptive local normalization
	// No RMS targeting (r=0.0) - relies on peak-based normalization
	// No compression (s=0.0) - acompressor handles dynamic range reduction
	// Reduced max gain (m=5.0, with safety check) - prevents over-amplification
	channelFlag := 0
	if cfg.DynaudnormChannels {
		channelFlag = 1
	}
	dcCorrectFlag := 0
	if cfg.DynaudnormDCCorrect {
		dcCorrectFlag = 1
	}
	altBoundaryFlag := 0
	if cfg.DynaudnormAltBoundary {
		altBoundaryFlag = 1
	}

	dynaudnormFilter := fmt.Sprintf(
		"dynaudnorm=f=%d:g=%d:p=%.2f:m=%.1f:r=%.3f:t=%.6f:n=%d:c=%d:b=%d:s=%.1f",
		cfg.DynaudnormFrameLen,   // 500ms frames (fixed)
		cfg.DynaudnormFilterSize, // 31 Gaussian filter size (fixed)
		cfg.DynaudnormPeakValue,  // 0.95 peak target (fixed)
		cfg.DynaudnormMaxGain,    // 5.0 max gain (with safety check reducing if needed)
		cfg.DynaudnormTargetRMS,  // 0.0 - no RMS targeting
		cfg.DynaudnormThreshold,  // 0.0 - normalize all frames
		channelFlag,              // false = coupled channels
		dcCorrectFlag,            // false = no DC correction
		altBoundaryFlag,          // false = standard boundary mode
		cfg.DynaudnormCompress,   // 0.0 - no compression
	)

	// Build speechnorm (Speech Normalizer) filter
	// Cycle-level normalization using zero-crossing half-cycles
	// Fast, speech-optimized alternative to dynaudnorm's frame-based approach
	// p: target peak, e: max expansion, c: max compression
	// t: threshold below which to stop normalizing
	// r: raise smoothing (peak rise), f: fall smoothing (peak fall)
	// l: link channels (0=independent, 1=coupled)
	speechnormChannelFlag := 0
	if cfg.SpeechnormChannels {
		speechnormChannelFlag = 1
	}

	speechnormFilter := fmt.Sprintf(
		"speechnorm=p=%.2f:e=%.1f:c=%.1f:t=%.2f:r=%.3f:f=%.3f:m=%.3f:l=%d",
		cfg.SpeechnormPeak,        // 0.95 target peak
		cfg.SpeechnormExpansion,   // Adaptive expansion (capped at 10x)
		cfg.SpeechnormCompression, // 2.0 max compression
		cfg.SpeechnormThreshold,   // 0.10 threshold
		cfg.SpeechnormRaise,       // 0.001 rise smoothing
		cfg.SpeechnormFall,        // 0.001 fall smoothing
		cfg.SpeechnormRMS,         // RMS targeting (set adaptively)
		speechnormChannelFlag,     // 0 = coupled channels
	)

	// Build arnndn (RNN denoise) filter - neural network noise reduction
	// Only enabled for heavily uplifted audio (expansion >= 8x)
	// Provides "mop up" of amplified noise AFTER speechnorm expansion
	// Uses embedded conjoined-burgers model trained for recorded speech
	var arnnDnFilter string
	if cfg.ArnnDnEnabled {
		// Get cached model path (extracts embedded model on first use)
		modelPath, err := getRNNModelPath()
		if err != nil {
			// If we can't get the model, disable arnndn
			// This shouldn't happen, but gracefully degrade rather than fail
			arnnDnFilter = ""
		} else {
			arnnDnFilter = fmt.Sprintf("arnndn=m=%s:mix=%.2f", modelPath, cfg.ArnnDnMix)
		}
	}
	// If ArnnDnEnabled is false, arnnDnFilter stays empty and will be skipped

	// Build anlmdn (Non-Local Means denoise) filter - patch-based noise reduction
	// Only enabled for heavily uplifted audio (expansion >= 8x)
	// Provides alternative/additional "mop up" of amplified noise AFTER speechnorm expansion
	// Uses patch-based matching algorithm (better for preserving texture/detail)
	var anlmDnFilter string
	if cfg.AnlmDnEnabled {
		// Use only strength parameter, let patch/research/output use defaults
		// Default: p=2ms, r=6ms, o=o (filtered output)
		anlmDnFilter = fmt.Sprintf("anlmdn=s=%f",
			cfg.AnlmDnStrength, // Denoising strength (adaptive)
		)
	}
	// If AnlmDnEnabled is false, anlmDnFilter stays empty and will be skipped

	// Build alimiter (true peak limiter) filter
	// Uses lookahead technology and ASC for smooth, musical limiting
	// Brick-wall safety net for peak control
	alimiterFilter := fmt.Sprintf(
		"alimiter=level_in=%.2f:level_out=%.2f:limit=%.2f:"+
			"attack=%.0f:release=%.0f:asc=%d:asc_level=%.1f:level=%d:latency=%d",
		1.0,                // No input gain adjustment
		1.0,                // No output gain adjustment
		cfg.LimiterCeiling, // Peak ceiling (0.84 = -1.5dBTP)
		cfg.LimiterAttack,  // Lookahead time (5ms for smooth limiting)
		cfg.LimiterRelease, // Release time (50ms for natural sound)
		1,                  // Enable ASC for smoother, more musical limiting
		0.5,                // Moderate ASC influence (soft knee behavior)
		0,                  // Disable auto-level normalization (dynaudnorm handles normalization)
		1,                  // Enable latency compensation
	)

	// Chain all filters together with commas
	// Order: highpass → adeclick → denoise → gate → compress → deess → dynaudnorm → limit → format → frame
	// Add aformat for podcast-standard output: 44.1kHz, mono, s16
	// Add asetnsamples to ensure fixed frame size for FLAC encoder (which doesn't support variable frame size)

	// Build filter list, skipping empty filters
	// Filter order: highpass → adeclick → afftdn → agate → acompressor → deesser → speechnorm → arnndn → anlmdn → dynaudnorm → alimiter
	// arnndn positioned after speechnorm to "mop up" amplified noise from expansion (neural network approach)
	// anlmdn positioned after arnndn to provide alternative/additional cleanup (patch-based approach)

	// Forcibly disable filters for dev/tresting purposes
	//highpassFilter = ""
	adeclickFilter = ""
	//agateFilter = ""
	//acompressorFilter = ""
	//deesserFilter = ""
	//dynaudnormFilter = ""
	//speechnormFilter = ""
	arnnDnFilter = ""
	anlmDnFilter = ""
	//alimiterFilter = ""

	var filters []string
	for _, f := range []string{highpassFilter, adeclickFilter, afftdnFilter, agateFilter, acompressorFilter, deesserFilter, speechnormFilter, arnnDnFilter, anlmDnFilter, dynaudnormFilter, alimiterFilter} {
		if f != "" {
			filters = append(filters, f)
		}
	}
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
