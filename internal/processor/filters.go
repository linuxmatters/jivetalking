// Package processor handles audio analysis and processing
package processor

import (
	"fmt"
	"math"

	"github.com/csnewman/ffmpeg-go"
)

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

	// Loudness Normalization (loudnorm two-pass)
	TargetI   float64 // LUFS target (podcast standard: -16)
	TargetTP  float64 // dBTP, true peak ceiling
	TargetLRA float64 // LU, loudness range target

	// True Peak Limiter (alimiter) - brick-wall safety net
	LimiterCeiling float64 // 0.0625-1.0, peak ceiling (0.98 = -0.17dBFS)
	LimiterAttack  float64 // ms, attack time
	LimiterRelease float64 // ms, release time

	// Pass 1 measurements (nil for first pass)
	Measurements *LoudnormMeasurements
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
		GateMakeup:    1.0,    // No makeup gain (handled by loudnorm)

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

		// Loudness - podcast standard
		TargetI:   -16.0,
		TargetTP:  -0.3, // Very permissive TP for loudnorm (only catches extreme peaks), alimiter does real limiting at -1.5
		TargetLRA: 7.0,  // EBU R128 default, appropriate for speech

		// Limiter - brick-wall safety net with soft knee (via ASC)
		// This does the real true-peak limiting with better sound quality than loudnorm's hard limiter
		LimiterCeiling: 0.84, // -1.5dBTP (actual target, tighter than loudnorm's -1.0)
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
// This creates the complete filter chain: highpass → adeclick → afftdn → agate → acompressor → deesser → loudnorm → alimiter
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

	afftdnFilter := fmt.Sprintf(
		"afftdn=nf=%.1f:nr=%.1f:tn=%d:rf=%.1f:ad=%.2f:fo=%.1f:gs=%d:om=%s:nt=%s",
		noiseFloorClamped,  // Noise floor from Pass 1 measurements (clamped to -80 to -20 dB)
		cfg.NoiseReduction, // Noise reduction amount (dB)
		noiseTrackFlag,     // Enable adaptive noise floor tracking
		-38.0,              // Residual floor (default -38dB)
		0.5,                // Adaptivity factor (0=instant, 1=slow, 0.5=balanced)
		1.0,                // Floor offset factor (adjustment to tracked floor)
		5,                  // Gain smooth radius (reduces musical noise artifacts)
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

	// Build loudnorm (two-pass normalization) filter
	var loudnormFilter string
	if cfg.Measurements == nil {
		// First pass: analysis only
		loudnormFilter = fmt.Sprintf("loudnorm=I=%.1f:TP=%.1f:LRA=%.1f:print_format=json",
			cfg.TargetI, cfg.TargetTP, cfg.TargetLRA)
	} else {
		// Second pass: use measurements from first pass for precise normalization
		// Use dynamic mode (linear=false) to prevent distortion on narrow-LRA sources
		// Dynamic mode adapts to the source material rather than forcing LRA expansion
		// Include offset parameter for proper gain adjustment even in dynamic mode
		loudnormFilter = fmt.Sprintf(
			"loudnorm=I=%.1f:TP=%.1f:LRA=%.1f:"+
				"measured_I=%.2f:measured_TP=%.2f:measured_LRA=%.2f:"+
				"measured_thresh=%.2f:offset=%.2f:"+
				"linear=false:print_format=summary",
			cfg.TargetI, cfg.TargetTP, cfg.TargetLRA,
			cfg.Measurements.InputI, cfg.Measurements.InputTP, cfg.Measurements.InputLRA,
			cfg.Measurements.InputThresh, cfg.Measurements.TargetOffset,
		)
	}

	// Build alimiter (true peak limiter) filter
	// Uses lookahead technology and ASC for smooth, musical limiting
	// This provides better sound quality than loudnorm's hard brick-wall limiter
	// Used in both Pass 1 and Pass 2 for consistent, high-quality peak control
	alimiterFilter := fmt.Sprintf(
		"alimiter=level_in=%.2f:level_out=%.2f:limit=%.2f:"+
			"attack=%.0f:release=%.0f:asc=%d:asc_level=%.1f:level=%d:latency=%d",
		1.0,                // No input gain adjustment
		1.0,                // No output gain adjustment
		cfg.LimiterCeiling, // Peak ceiling (0.84 = -1.5dBTP, actual target)
		cfg.LimiterAttack,  // Lookahead time (5ms for smooth limiting)
		cfg.LimiterRelease, // Release time (50ms for natural sound)
		1,                  // Enable ASC for smoother, more musical limiting
		0.5,                // Moderate ASC influence (soft knee behavior)
		0,                  // Disable auto-level normalization (loudnorm handles LUFS)
		1,                  // Enable latency compensation
	)

	// Chain all filters together with commas
	// Order: highpass → adeclick → denoise → gate → compress → deess → normalize → limit → format → frame
	// Add aformat for podcast-standard output: 44.1kHz, mono, s16
	// Add asetnsamples to ensure fixed frame size for FLAC encoder (which doesn't support variable frame size)

	// Build filter list, skipping empty filters
	var filters []string
	for _, f := range []string{highpassFilter, adeclickFilter, afftdnFilter, agateFilter, acompressorFilter, deesserFilter, loudnormFilter, alimiterFilter} {
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
// This is used in Pass 2 to apply the full filter chain: noise reduction → gate → compression → loudnorm
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
