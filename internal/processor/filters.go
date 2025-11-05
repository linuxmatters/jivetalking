// Package processor handles audio analysis and processing
package processor

import (
	"fmt"

	"github.com/csnewman/ffmpeg-go"
)

// FilterChainConfig holds configuration for the audio processing filter chain
type FilterChainConfig struct {
	// High-Pass Filter (highpass) - removes subsonic rumble
	HighpassFreq float64 // Hz, cutoff frequency (removes frequencies below this)

	// Noise Reduction (afftdn)
	NoiseFloor     float64 // dB, estimated noise floor from Pass 1
	NoiseReduction float64 // 0.0-1.0, reduction amount
	NoiseTrack     bool    // Enable automatic noise tracking (tn=1)

	// Gate (agate)
	GateThreshold float64 // Activation threshold (0.0-1.0)
	GateRatio     float64 // Reduction ratio
	GateAttack    float64 // Attack time (ms)
	GateRelease   float64 // Release time (ms)

	// Compression (acompressor)
	CompThreshold float64 // dB, compression threshold
	CompRatio     float64 // Compression ratio
	CompAttack    float64 // Attack time (ms)
	CompRelease   float64 // Release time (ms)
	CompMakeup    float64 // dB, makeup gain

	// Dynamic EQ De-esser (adynamicequalizer) - reduces harsh sibilance with precise frequency targeting
	DeessThreshold float64 // 0.0-100.0, detection threshold (lower = more aggressive)
	DeessFrequency float64 // Hz, target sibilance frequency (typical: 6000-9000)
	DeessQFactor   float64 // 0.001-1000, bandwidth control (higher = narrower, more surgical)
	DeessRatio     float64 // 0-30, compression ratio for sibilance reduction
	DeessAttack    float64 // ms, attack time for sibilance detection
	DeessRelease   float64 // ms, release time for smooth reduction

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

		// Noise Reduction - will use Pass 1 noise floor estimate
		NoiseFloor:     -25.0, // Placeholder, will be updated from measurements
		NoiseReduction: 0.02,
		NoiseTrack:     true,

		// Gate - remove silence and low-level noise
		GateThreshold: 0.003,
		GateRatio:     4.0,
		GateAttack:    5,
		GateRelease:   100,

		// Compression - even out dynamics
		CompThreshold: -18,
		CompRatio:     4.0,
		CompAttack:    20,
		CompRelease:   100,
		CompMakeup:    8,

		// Dynamic EQ De-esser - precise sibilance reduction at 7kHz
		DeessThreshold: 0.1,  // Very gentle threshold (activates on subtle sibilance)
		DeessFrequency: 7000, // 7kHz center frequency (typical sibilance peak)
		DeessQFactor:   2.0,  // Moderate bandwidth (focused but not too narrow)
		DeessRatio:     3.0,  // 3:1 compression ratio (gentle but effective)
		DeessAttack:    5.0,  // 5ms fast attack (catches sibilance peaks)
		DeessRelease:   50.0, // 50ms moderate release (smooth, natural decay)

		// Loudness - podcast standard
		TargetI:   -16.0,
		TargetTP:  -1.5,
		TargetLRA: 11.0,

		// Limiter - brick-wall safety net
		LimiterCeiling: 0.98, // -0.17dBFS ceiling
		LimiterAttack:  5.0,  // 5ms attack
		LimiterRelease: 50.0, // 50ms release

		Measurements: nil, // Will be set after Pass 1
	}
}

// BuildFilterSpec builds the FFmpeg filter specification string for Pass 2 processing
// This creates the complete filter chain: highpass → afftdn → agate → acompressor → deesser → loudnorm → alimiter
func (cfg *FilterChainConfig) BuildFilterSpec() string {
	// Build highpass (rumble removal) filter
	// Remove subsonic frequencies below 80Hz (HVAC, handling noise, etc.)
	highpassFilter := fmt.Sprintf("highpass=f=%.0f:t=q",
		cfg.HighpassFreq)

	// Build afftdn (noise reduction) filter
	// Use automatic noise tracking (tn=1) to adapt from initial estimate to actual noise floor
	noiseTrackFlag := 0
	if cfg.NoiseTrack {
		noiseTrackFlag = 1
	}

	afftdnFilter := fmt.Sprintf("afftdn=nf=%.1f:nr=%.2f:tn=%d",
		cfg.NoiseFloor, cfg.NoiseReduction, noiseTrackFlag)

	// Build agate (silence removal) filter
	agateFilter := fmt.Sprintf("agate=threshold=%.3f:ratio=%.1f:attack=%.0f:release=%.0f",
		cfg.GateThreshold, cfg.GateRatio, cfg.GateAttack, cfg.GateRelease)

	// Build acompressor (dynamics) filter
	acompressorFilter := fmt.Sprintf("acompressor=threshold=%.0fdB:ratio=%.1f:attack=%.0f:release=%.0f:makeup=%.0fdB",
		cfg.CompThreshold, cfg.CompRatio, cfg.CompAttack, cfg.CompRelease, cfg.CompMakeup)

	// Build adynamicequalizer (dynamic EQ de-esser) filter
	// Applied after compression to correct emphasized sibilance
	// Uses precise Hz targeting with dynamic compression for natural sibilance control
	// mode: 1=cutabove, dftype: 0=bandpass, tftype: 0=bell
	deesserFilter := fmt.Sprintf(
		"adynamicequalizer=threshold=%.1f:dfrequency=%.0f:tfrequency=%.0f:"+
			"dqfactor=%.1f:tqfactor=%.1f:attack=%.0f:release=%.0f:ratio=%.1f:"+
			"mode=1:dftype=0:tftype=0",
		cfg.DeessThreshold,
		cfg.DeessFrequency, cfg.DeessFrequency, // detection and target same frequency
		cfg.DeessQFactor, cfg.DeessQFactor, // detection and target same bandwidth
		cfg.DeessAttack, cfg.DeessRelease,
		cfg.DeessRatio,
	)

	// Build loudnorm (two-pass normalization) filter
	var loudnormFilter string
	if cfg.Measurements == nil {
		// First pass: analysis only
		loudnormFilter = fmt.Sprintf("loudnorm=I=%.1f:TP=%.1f:LRA=%.1f:print_format=json",
			cfg.TargetI, cfg.TargetTP, cfg.TargetLRA)
	} else {
		// Second pass: use measurements from first pass for precise normalization
		loudnormFilter = fmt.Sprintf(
			"loudnorm=I=%.1f:TP=%.1f:LRA=%.1f:"+
				"measured_I=%.2f:measured_TP=%.2f:measured_LRA=%.2f:"+
				"measured_thresh=%.2f:offset=%.2f:"+
				"linear=true:print_format=summary",
			cfg.TargetI, cfg.TargetTP, cfg.TargetLRA,
			cfg.Measurements.InputI, cfg.Measurements.InputTP, cfg.Measurements.InputLRA,
			cfg.Measurements.InputThresh, cfg.Measurements.TargetOffset,
		)
	}

	// Build alimiter (true peak limiter) filter
	// Brick-wall safety net to catch any true peak violations after loudnorm
	alimiterFilter := fmt.Sprintf("alimiter=level_in=1:level_out=1:limit=%.2f:attack=%.0f:release=%.0f",
		cfg.LimiterCeiling, cfg.LimiterAttack, cfg.LimiterRelease)

	// Chain all filters together with commas
	// Order: highpass → denoise → gate → compress → deess → normalize → limit → format → frame
	// Add aformat for podcast-standard output: 44.1kHz, mono, s16
	// Add asetnsamples to ensure fixed frame size for FLAC encoder (which doesn't support variable frame size)
	return fmt.Sprintf("%s,%s,%s,%s,%s,%s,%s,aformat=sample_rates=44100:channel_layouts=mono:sample_fmts=s16,asetnsamples=n=4096",
		highpassFilter, afftdnFilter, agateFilter, acompressorFilter, deesserFilter, loudnormFilter, alimiterFilter)
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
