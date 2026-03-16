// Package processor handles audio analysis and processing
package processor

import (
	"fmt"
	"math"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// DebugLog is a package-level function for debug logging.
// When set (non-nil), diagnostic output is written via this function.
// Set by main.go when --debug flag is enabled.
var DebugLog func(format string, args ...any)

// debugLog writes to the debug log if enabled, otherwise does nothing.
func debugLog(format string, args ...any) {
	if DebugLog != nil {
		DebugLog(format, args...)
	}
}

// SilenceRegion represents a detected silence period in the audio
type SilenceRegion struct {
	Start    time.Duration `json:"start"`
	End      time.Duration `json:"end"`
	Duration time.Duration `json:"duration"`
}

// NoiseProfile contains measurements from the elected silence region.
// These measurements serve as a reference baseline for adaptive filter tuning:
//   - MeasuredNoiseFloor → compand expansion threshold (NoiseRemove)
//   - Entropy → gate release timing and range adaptation (DS201Gate)
//     (See docs/Spectral-Metrics-Reference.md for entropy value interpretations:
//     low entropy 0.08-0.30 = ordered/voiced; high entropy > 0.50 = disordered/noise)
//   - CrestFactor/PeakLevel → transient detection mode selection
//
// Note: The silence region is also re-measured in Pass 2 and Pass 4 for
// before/after comparison of noise reduction effectiveness.
type NoiseProfile struct {
	Start              time.Duration `json:"start"`                        // Start time of silence region used
	Duration           time.Duration `json:"duration"`                     // Duration of extracted sample
	MeasuredNoiseFloor float64       `json:"measured_noise_floor"`         // dBFS, RMS level of silence (average noise)
	PeakLevel          float64       `json:"peak_level"`                   // dBFS, peak level in silence (transient noise indicator)
	CrestFactor        float64       `json:"crest_factor"`                 // Peak - RMS in dB (high = impulsive noise, low = steady noise)
	Entropy            float64       `json:"entropy"`                      // Signal randomness (1.0 = white noise, lower = tonal noise like hum)
	ExtractionWarning  string        `json:"extraction_warning,omitempty"` // Warning message if extraction had issues

	// Spectral characteristics for contamination detection (added during candidate evaluation)
	SpectralCentroid float64 `json:"spectral_centroid,omitempty"` // Hz, where energy is concentrated (voice range: 300-4000 Hz)
	SpectralFlatness float64 `json:"spectral_flatness,omitempty"` // 0-1, noise-like vs tonal (higher = more noise-like)
	SpectralKurtosis float64 `json:"spectral_kurtosis,omitempty"` // Peakiness (high = peaked harmonics like speech)

	// Golden sub-region refinement info (populated when a long candidate is refined)
	OriginalStart    time.Duration `json:"original_start,omitempty"`    // Original candidate start before refinement
	OriginalDuration time.Duration `json:"original_duration,omitempty"` // Original candidate duration before refinement
	WasRefined       bool          `json:"was_refined,omitempty"`       // True if region was refined from a longer candidate
}

// SilenceCandidateMetrics contains measurements for evaluating silence region candidates.
// These metrics are collected before final selection to enable multi-metric scoring.
// Includes all measurements available from IntervalSample for future filter tuning.
type SilenceCandidateMetrics struct {
	Region SilenceRegion `json:"region"` // The silence region being evaluated

	// Amplitude metrics
	RMSLevel    float64 `json:"rms_level"`    // dBFS, average level (lower = quieter)
	PeakLevel   float64 `json:"peak_level"`   // dBFS, max peak level across region
	CrestFactor float64 `json:"crest_factor"` // Peak - RMS in dB (high = impulsive)

	// Spectral metrics (averaged across region)
	Spectral SpectralMetrics `json:"spectral"`

	// Loudness metrics (averaged/max across region)
	MomentaryLUFS float64 `json:"momentary_lufs"`  // LUFS, average momentary loudness
	ShortTermLUFS float64 `json:"short_term_lufs"` // LUFS, average short-term loudness
	TruePeak      float64 `json:"true_peak"`       // dBTP, max true peak across region
	SamplePeak    float64 `json:"sample_peak"`     // dBFS, max sample peak across region

	// Warning flags (populated during scoring)
	TransientWarning string `json:"transient_warning,omitempty"` // Warning if danger zone signature detected

	// Scoring (computed after measurement)
	Score float64 `json:"score"` // Composite score for candidate ranking

	// StabilityScore measures the temporal consistency of the silence region (0-1).
	// Higher scores indicate more stable measurements across the region, suggesting
	// intentionally-recorded room tone rather than accidental gaps between speech.
	// Calculated from RMS variance and average spectral flux across intervals.
	StabilityScore float64 `json:"stability_score"`

	// Refinement metadata (populated when pre-scoring refinement trims the candidate)
	OriginalStart    time.Duration `json:"original_start,omitempty"`    // Original candidate start before refinement
	OriginalDuration time.Duration `json:"original_duration,omitempty"` // Original candidate duration before refinement
	WasRefined       bool          `json:"was_refined,omitempty"`       // True if region was refined from a longer candidate
}

// SpeechRegion represents a detected continuous speech period in the audio.
// Used for extracting representative speech measurements for adaptive tuning.
type SpeechRegion struct {
	Start    time.Duration `json:"start"`
	End      time.Duration `json:"end"`
	Duration time.Duration `json:"duration"`
}

// SpeechCandidateMetrics contains measurements for evaluating speech region candidates.
// These metrics characterise typical speech levels for adaptive filter tuning.
// Includes all measurements available from IntervalSample for future filter tuning.
type SpeechCandidateMetrics struct {
	Region SpeechRegion `json:"region"` // The speech region being evaluated

	// Amplitude metrics
	RMSLevel    float64 `json:"rms_level"`    // dBFS, average level (higher = louder speech)
	PeakLevel   float64 `json:"peak_level"`   // dBFS, max peak level across region
	CrestFactor float64 `json:"crest_factor"` // Peak - RMS in dB (speech typically 9-14 dB, optimal range)

	// Spectral metrics (averaged across region)
	Spectral SpectralMetrics `json:"spectral"`

	// Loudness metrics (averaged/max across region)
	MomentaryLUFS float64 `json:"momentary_lufs"`  // LUFS, average momentary loudness
	ShortTermLUFS float64 `json:"short_term_lufs"` // LUFS, average short-term loudness
	TruePeak      float64 `json:"true_peak"`       // dBTP, max true peak across region
	SamplePeak    float64 `json:"sample_peak"`     // dBFS, max sample peak across region

	// Stability metrics (populated during measurement)
	VoicingDensity float64 `json:"voicing_density,omitempty"` // Proportion of voiced intervals (0-1)

	// Scoring
	Score float64 `json:"score"` // Composite score for candidate ranking

	// Golden sub-region refinement info (populated when a long candidate is refined)
	OriginalStart    time.Duration `json:"original_start,omitempty"`    // Original candidate start before refinement
	OriginalDuration time.Duration `json:"original_duration,omitempty"` // Original candidate duration before refinement
	WasRefined       bool          `json:"was_refined,omitempty"`       // True if region was refined from a longer candidate
}

// Silence detection constants for interval-based analysis
// AudioMeasurements contains the measurements from Pass 1 analysis.
// Uses ebur128 (LUFS/LRA), astats (dynamic range/noise floor), and aspectralstats (spectral analysis).
// Silence detection is performed in Go using 250ms interval sampling for improved accuracy.
// BaseMeasurements contains fields shared between input (Pass 1) and output (Pass 2) measurements.
// Embedded in both AudioMeasurements and OutputMeasurements to avoid duplication.
type BaseMeasurements struct {
	// Spectral analysis from aspectralstats (all measurements averaged across frames)
	SpectralMean     float64 `json:"spectral_mean"`     // Mean spectral magnitude
	SpectralVariance float64 `json:"spectral_variance"` // Spectral magnitude variance
	SpectralCentroid float64 `json:"spectral_centroid"` // Spectral centroid (Hz) - where energy is concentrated
	SpectralSpread   float64 `json:"spectral_spread"`   // Spectral spread (Hz) - bandwidth/fullness indicator
	SpectralSkewness float64 `json:"spectral_skewness"` // Spectral asymmetry - positive=bright, negative=dark
	SpectralKurtosis float64 `json:"spectral_kurtosis"` // Spectral peakiness - tonal vs broadband content
	SpectralEntropy  float64 `json:"spectral_entropy"`  // Spectral randomness (0-1) - noise classification
	SpectralFlatness float64 `json:"spectral_flatness"` // Noise vs tonal ratio (0-1) - low=tonal, high=noisy
	SpectralCrest    float64 `json:"spectral_crest"`    // Spectral peak-to-RMS - transient indicator
	SpectralFlux     float64 `json:"spectral_flux"`     // Frame-to-frame spectral change
	SpectralSlope    float64 `json:"spectral_slope"`    // Spectral tilt - negative=more bass
	SpectralDecrease float64 `json:"spectral_decrease"` // Average spectral decrease
	SpectralRolloff  float64 `json:"spectral_rolloff"`  // Spectral rolloff (Hz) - HF energy dropoff point

	// Time-domain statistics from astats
	DynamicRange float64 `json:"dynamic_range"` // Measured dynamic range (dB)
	RMSLevel     float64 `json:"rms_level"`     // Overall RMS level (dBFS)
	PeakLevel    float64 `json:"peak_level"`    // Overall peak level (dBFS)
	RMSTrough    float64 `json:"rms_trough"`    // RMS level of quietest segments (dBFS)
	RMSPeak      float64 `json:"rms_peak"`      // RMS level of loudest segments (dBFS)

	// Additional astats measurements
	DCOffset          float64 `json:"dc_offset"`           // Mean amplitude displacement from zero
	FlatFactor        float64 `json:"flat_factor"`         // Consecutive samples at peak (clipping indicator)
	CrestFactor       float64 `json:"crest_factor"`        // Peak-to-RMS ratio in dB (converted from linear)
	ZeroCrossingsRate float64 `json:"zero_crossings_rate"` // Zero crossing rate (low=bass, high=noise/sibilance)
	ZeroCrossings     float64 `json:"zero_crossings"`      // Total zero crossings
	MaxDifference     float64 `json:"max_difference"`      // Largest sample-to-sample change (clicks/pops indicator)
	MinDifference     float64 `json:"min_difference"`      // Smallest sample-to-sample change
	MeanDifference    float64 `json:"mean_difference"`     // Average sample-to-sample change
	RMSDifference     float64 `json:"rms_difference"`      // RMS of sample-to-sample changes
	Entropy           float64 `json:"entropy"`             // Signal randomness (1.0 = white noise, lower = structured)
	MinLevel          float64 `json:"min_level"`           // dBFS, minimum sample level (converted from linear)
	MaxLevel          float64 `json:"max_level"`           // dBFS, maximum sample level (converted from linear)
	AstatsNoiseFloor  float64 `json:"astats_noise_floor"`  // FFmpeg astats noise floor estimate (dBFS)
	NoiseFloorCount   float64 `json:"noise_floor_count"`   // Number of samples in noise floor measurement
	BitDepth          float64 `json:"bit_depth"`           // Effective bit depth
	NumberOfSamples   float64 `json:"number_of_samples"`   // Total samples processed

	// ebur128 momentary/short-term loudness
	MomentaryLoudness float64 `json:"momentary_loudness"`  // Momentary loudness (400ms window, LUFS)
	ShortTermLoudness float64 `json:"short_term_loudness"` // Short-term loudness (3s window, LUFS)
	SamplePeak        float64 `json:"sample_peak"`         // Sample peak (dBFS)
}

// AudioMeasurements contains the measurements from Pass 1 analysis.
// Uses ebur128 (LUFS/LRA), astats (dynamic range/noise floor), and aspectralstats (spectral analysis).
// Silence detection is performed in Go using 250ms interval sampling for improved accuracy.
type AudioMeasurements struct {
	// Embed shared measurement fields
	BaseMeasurements

	// Input-specific loudness measurements from ebur128
	InputI           float64 `json:"input_i"`            // Integrated loudness (LUFS)
	InputTP          float64 `json:"input_tp"`           // True peak (dBTP)
	InputLRA         float64 `json:"input_lra"`          // Loudness range (LU)
	InputThresh      float64 `json:"input_thresh"`       // Threshold level
	TargetOffset     float64 `json:"target_offset"`      // Offset for normalization
	NoiseFloor       float64 `json:"noise_floor"`        // Derived noise floor (dBFS), three-tier: astats → RMS estimate → ebur128 estimate
	NoiseFloorSource string  `json:"noise_floor_source"` // Source of NoiseFloor: "astats", "rms_estimate", "ebur128_estimate"

	// Adaptive silence detection thresholds (derived from interval sampling)
	PreScanNoiseFloor  float64 `json:"prescan_noise_floor"`  // Noise floor estimated from interval data (dBFS)
	SilenceDetectLevel float64 `json:"silence_detect_level"` // Adaptive silencedetect threshold used (dBFS)

	// Silence detection results (derived from interval sampling)
	SilenceRegions []SilenceRegion `json:"silence_regions,omitempty"` // Detected silence regions

	// 250ms interval samples for data-driven silence candidate detection
	IntervalSamples []IntervalSample `json:"interval_samples,omitempty"` // Per-interval measurements

	// Scored silence candidates (for debugging/reporting)
	SilenceCandidates []SilenceCandidateMetrics `json:"silence_candidates,omitempty"` // All evaluated candidates with scores

	// Speech detection results
	SpeechRegions    []SpeechRegion           `json:"speech_regions,omitempty"`    // Detected speech regions
	SpeechCandidates []SpeechCandidateMetrics `json:"speech_candidates,omitempty"` // All evaluated candidates with scores

	// Elected speech candidate measurements (for adaptive tuning)
	SpeechProfile *SpeechCandidateMetrics `json:"speech_profile,omitempty"` // Best speech candidate metrics

	// Voice-activated recording detection
	VoiceActivated bool `json:"voice_activated"` // True when >= 95% of silence candidates are digital silence

	// Noise profile extracted from best silence candidate
	NoiseProfile *NoiseProfile `json:"noise_profile,omitempty"` // nil if extraction failed

	// Derived suggestions for Pass 2 adaptive processing
	SuggestedGateThreshold float64 `json:"suggested_gate_threshold"` // Suggested gate threshold (linear amplitude)
	NoiseReductionHeadroom float64 `json:"noise_reduction_headroom"` // dB gap between noise and quiet speech
}

// OutputMeasurements contains the measurements from Pass 2 output analysis.
// Uses BaseMeasurements for comparison with AudioMeasurements.
// Does not include silence detection or noise profile fields (those are input-only).
type OutputMeasurements struct {
	// Embed shared measurement fields
	BaseMeasurements

	// Output-specific loudness measurements from ebur128
	OutputI      float64 `json:"output_i"`      // Integrated loudness (LUFS)
	OutputTP     float64 `json:"output_tp"`     // True peak (dBTP)
	OutputLRA    float64 `json:"output_lra"`    // Loudness range (LU)
	OutputThresh float64 `json:"output_thresh"` // Gating threshold (LUFS) - for loudnorm
	TargetOffset float64 `json:"target_offset"` // Pre-limiter offset (dB) - from loudnorm measurement

	// Loudnorm measurement from Pass 2 analysis chain
	// These come from loudnorm's first pass (measurement mode, without linear=true)
	// and are used for the application pass in Pass 3
	LoudnormInputI       float64 `json:"loudnorm_input_i"`       // Loudnorm's measured integrated loudness (LUFS)
	LoudnormInputTP      float64 `json:"loudnorm_input_tp"`      // Loudnorm's measured true peak (dBTP)
	LoudnormInputLRA     float64 `json:"loudnorm_input_lra"`     // Loudnorm's measured loudness range (LU)
	LoudnormInputThresh  float64 `json:"loudnorm_input_thresh"`  // Loudnorm's measured threshold (LUFS)
	LoudnormTargetOffset float64 `json:"loudnorm_target_offset"` // Loudnorm's calculated offset for second pass
	LoudnormMeasured     bool    `json:"loudnorm_measured"`      // True if loudnorm measurement was captured

	// Silence region analysis (same region as Pass 1, for noise reduction comparison)
	SilenceSample *SilenceCandidateMetrics `json:"silence_sample,omitempty"` // Measurements from same silence region

	// Speech region analysis (same region as Pass 1, for processing comparison)
	SpeechSample *SpeechCandidateMetrics `json:"speech_sample,omitempty"` // Measurements from same speech region
}

// AnalyzeAudio performs Pass 1: ebur128 + astats + aspectralstats analysis to get measurements
// This is required for adaptive processing in Pass 2.
//
// Implementation note: ebur128 and astats write measurements to frame metadata with lavfi.r128.*
// and lavfi.astats.Overall.* keys respectively. We extract these from the last processed frames.
//
// The noise floor and silence threshold are computed from interval data AFTER the full pass,
// eliminating the need for a separate pre-scan phase.
func AnalyzeAudio(filename string, config *FilterChainConfig, progressCallback func(pass PassNumber, passName string, progress float64, level float64, measurements *AudioMeasurements)) (*AudioMeasurements, error) {
	// Default fallback threshold if interval analysis yields insufficient data
	const defaultNoiseFloor = -50.0

	// Open audio file
	reader, metadata, err := audio.OpenAudioFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer reader.Close()

	// Get total duration for progress calculation
	totalDuration := metadata.Duration
	sampleRate := float64(metadata.SampleRate)

	// Calculate total frames estimate (duration * sample_rate / samples_per_frame)
	// For FLAC, typical frame size is 4096 samples
	samplesPerFrame := 4096.0
	estimatedTotalFrames := (totalDuration * sampleRate) / samplesPerFrame

	// Create filter graph for Pass 1 analysis (astats + aspectralstats + ebur128)
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := createAnalysisFilterGraph(
		reader.GetDecoderContext(),
		config,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create filter graph: %w", err)
	}
	// NOTE: filterGraph is explicitly freed at the end (not in defer) to ensure
	// measurements are output via av_log before we try to extract them.
	// On error paths, we still free it immediately
	var filterFreed bool
	defer func() {
		if !filterFreed && filterGraph != nil {
			ffmpeg.AVFilterGraphFree(&filterGraph)
		}
	}()

	// Track frames for periodic progress updates
	frameCount := 0
	updateInterval := 100 // Send progress update every N frames
	currentLevel := 0.0

	// Accumulators for frame metadata extraction
	acc := &metadataAccumulators{}

	// Interval sampling for silence detection (250ms windows)
	const intervalDuration = 250 * time.Millisecond
	var intervals []IntervalSample
	var intervalAcc intervalAccumulator
	intervalAcc.reset() // Initialize with proper defaults
	var intervalStartTime time.Duration

	// Track input frame time (before filter graph, which upsamples to 192kHz)
	var inputSamplesProcessed int64
	inputSampleRate := float64(reader.GetDecoderContext().SampleRate())

	// Process all frames through the filter graph
	if err := runFilterGraph(reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnReadError: func(err error) error {
			return fmt.Errorf("failed to read frame: %w", err)
		},
		OnPushError: func(err error) error {
			return fmt.Errorf("failed to add frame to filter: %w", err)
		},
		OnPullError: func(err error) error {
			return fmt.Errorf("failed to get filtered frame: %w", err)
		},
		OnInputFrame: func(inputFrame *ffmpeg.AVFrame) {
			// Calculate audio level from frame
			currentLevel = calculateFrameLevel(inputFrame)

			// Calculate input frame time based on samples processed (before filter graph upsampling)
			inputFrameTime := time.Duration(float64(inputSamplesProcessed) / inputSampleRate * float64(time.Second))
			inputSamplesProcessed += int64(inputFrame.NbSamples())
			// Accumulate RMS and peak from INPUT frame (before filter graph which upsamples to 192kHz)
			// This gives accurate RMS and peak values matching the original audio levels
			intervalAcc.addFrameRMSAndPeak(inputFrame)

			// Check if interval complete (250ms elapsed) based on input time
			if inputFrameTime-intervalStartTime >= intervalDuration {
				// Finalize and store completed interval
				intervals = append(intervals, intervalAcc.finalize(intervalStartTime))
				intervalStartTime = inputFrameTime
				intervalAcc.reset()
			}

			// Send periodic progress updates based on frame count
			if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
				progress := float64(frameCount) / estimatedTotalFrames
				if progress > 1.0 {
					progress = 1.0
				}
				progressCallback(PassAnalysis, "Analyzing", progress, currentLevel, nil)
			}
			frameCount++
		},
		OnFrame: func(_, filteredFrame *ffmpeg.AVFrame) (FrameAction, error) {
			// Extract spectral metrics once, reuse for both whole-file and interval accumulators
			metadata := filteredFrame.Metadata()
			spectral := extractSpectralMetrics(metadata)

			// Extract measurements from frame metadata (whole-file accumulators)
			extractFrameMetadata(metadata, acc, spectral)

			// Also accumulate into current interval for per-interval spectral data
			// Filtered frames roughly correspond to input timing (just at higher sample rate)
			intervalAcc.add(extractIntervalFrameMetrics(metadata, spectral))

			return FrameDiscard, nil
		},
	}); err != nil {
		return nil, err
	}

	// Finalize any remaining partial interval (if it has data)
	if intervalAcc.rawSampleCount > 0 {
		intervals = append(intervals, intervalAcc.finalize(intervalStartTime))
	}

	// Note: We intentionally discard partial intervals with no data

	// Free the filter graph
	ffmpeg.AVFilterGraphFree(&filterGraph)
	filterFreed = true

	// Estimate noise floor and silence threshold from interval data
	// This replaces the previous separate pre-scan pass

	// Pre-compute silence detection medians (shared by noise estimation and candidate detection)
	silMedians := computeSilenceMedians(intervals)

	noiseFloorEstimate, silenceThreshold, ok := estimateNoiseFloorAndThreshold(intervals, silMedians)
	if !ok {
		// Fallback if insufficient interval data (very short recordings)
		noiseFloorEstimate = defaultNoiseFloor
		silenceThreshold = calculateAdaptiveSilenceThreshold(defaultNoiseFloor)
	}

	// Create measurements struct and populate from accumulators
	measurements := &AudioMeasurements{
		// Noise floor estimated from interval data (replaces pre-scan)
		PreScanNoiseFloor:  noiseFloorEstimate,
		SilenceDetectLevel: silenceThreshold,
	}

	// Populate ebur128 loudness measurements
	if acc.ebur128Found {
		measurements.InputI = acc.ebur128InputI
		measurements.InputTP = acc.ebur128InputTP
		measurements.InputLRA = acc.ebur128InputLRA
		// Calculate threshold based on integrated loudness (ebur128 doesn't provide this directly)
		// Threshold is typically around 10 LU below the integrated loudness
		measurements.InputThresh = acc.ebur128InputI - 10.0
		// Target offset for normalization (difference between measured and target)
		measurements.TargetOffset = config.TargetI - acc.ebur128InputI
	} else {
		return nil, fmt.Errorf("ebur128 measurements not found in metadata for file: %s", filename)
	}

	// Calculate average spectral statistics from aspectralstats
	if acc.spectralFrameCount > 0 {
		spectralFrameCountF := float64(acc.spectralFrameCount)
		measurements.SpectralMean = acc.spectralMeanSum / spectralFrameCountF
		measurements.SpectralVariance = acc.spectralVarianceSum / spectralFrameCountF
		measurements.SpectralCentroid = acc.spectralCentroidSum / spectralFrameCountF
		measurements.SpectralSpread = acc.spectralSpreadSum / spectralFrameCountF
		measurements.SpectralSkewness = acc.spectralSkewnessSum / spectralFrameCountF
		measurements.SpectralKurtosis = acc.spectralKurtosisSum / spectralFrameCountF
		measurements.SpectralEntropy = acc.spectralEntropySum / spectralFrameCountF
		measurements.SpectralFlatness = acc.spectralFlatnessSum / spectralFrameCountF
		measurements.SpectralCrest = acc.spectralCrestSum / spectralFrameCountF
		measurements.SpectralFlux = acc.spectralFluxSum / spectralFrameCountF
		measurements.SpectralSlope = acc.spectralSlopeSum / spectralFrameCountF
		measurements.SpectralDecrease = acc.spectralDecreaseSum / spectralFrameCountF
		measurements.SpectralRolloff = acc.spectralRolloffSum / spectralFrameCountF
	}

	// Store astats measurements (if captured)
	if acc.astatsFound {
		measurements.DynamicRange = acc.astatsDynamicRange
		measurements.RMSLevel = acc.astatsRMSLevel
		measurements.PeakLevel = acc.astatsPeakLevel
		measurements.RMSTrough = acc.astatsRMSTrough
		measurements.RMSPeak = acc.astatsRMSPeak

		// Additional astats measurements for adaptive processing
		measurements.DCOffset = acc.astatsDCOffset
		measurements.FlatFactor = acc.astatsFlatFactor
		measurements.CrestFactor = acc.astatsCrestFactor
		measurements.ZeroCrossingsRate = acc.astatsZeroCrossingsRate
		measurements.ZeroCrossings = acc.astatsZeroCrossings
		measurements.MaxDifference = acc.astatsMaxDifference
		measurements.MinDifference = acc.astatsMinDifference
		measurements.MeanDifference = acc.astatsMeanDifference
		measurements.RMSDifference = acc.astatsRMSDifference
		measurements.Entropy = acc.astatsEntropy
		measurements.MinLevel = acc.astatsMinLevel
		measurements.MaxLevel = acc.astatsMaxLevel
		measurements.AstatsNoiseFloor = acc.astatsNoiseFloor
		measurements.NoiseFloorCount = acc.astatsNoiseFloorCount
		measurements.BitDepth = acc.astatsBitDepth
		measurements.NumberOfSamples = acc.astatsNumberOfSamples
	}

	// Store ebur128 momentary/short-term loudness
	if acc.ebur128Found {
		measurements.MomentaryLoudness = acc.ebur128InputM
		measurements.ShortTermLoudness = acc.ebur128InputS
		measurements.SamplePeak = acc.ebur128InputSP
	}

	// Derive noise floor using three-tier approach based on audio engineering best practices:
	// Tier 1 (Primary): RMS_trough from astats - most accurate
	//   - Measures RMS level during quietest segments (inter-word silence in speech)
	//   - These quiet periods contain primarily room noise, HVAC, electronics noise
	//   - Directly represents the actual noise floor of the recording environment
	// Tier 2 (Secondary): Estimate from RMS_level - 15dB
	//   - Based on typical speech crest factor where quiet segments are 12-18dB below average RMS
	//   - Reasonable approximation when RMS_trough unavailable
	// Tier 3 (Tertiary): Estimate from ebur128 InputThresh with loudness-based offset
	//   - Fallback for when astats data is completely unavailable
	//   - Uses integrated loudness to infer likely noise floor characteristics

	switch {
	case acc.astatsRMSTrough != 0 && !math.IsInf(acc.astatsRMSTrough, -1):
		// Tier 1: Use RMS_trough (best - actual measurement of quiet segments)
		measurements.NoiseFloor = acc.astatsRMSTrough
		measurements.NoiseFloorSource = "astats"
	case acc.astatsRMSLevel != 0 && !math.IsInf(acc.astatsRMSLevel, -1):
		// Tier 2: Estimate from overall RMS level
		// Typical speech has quiet segments 12-18dB below average RMS; use 15dB as balanced estimate
		measurements.NoiseFloor = acc.astatsRMSLevel - 15.0
		measurements.NoiseFloorSource = "rms_estimate"
	default:
		// Tier 3: Estimate from ebur128 integrated loudness threshold
		// Louder recordings typically have better SNR (lower relative noise floor)
		var noiseFloorOffset float64
		switch {
		case measurements.InputI > -20:
			noiseFloorOffset = 18.0 // Professional: very low noise floor
		case measurements.InputI > -30:
			noiseFloorOffset = 12.0 // Typical podcast: moderate noise floor
		default:
			noiseFloorOffset = 8.0 // Quiet source: higher relative noise
		}
		measurements.NoiseFloor = measurements.InputThresh - noiseFloorOffset
		measurements.NoiseFloorSource = "ebur128_estimate"
	}

	// Safety clamp: -90dB (digital silence) to -30dB (very noisy environment)
	// Prevents extreme values while allowing wide range of recording quality
	if measurements.NoiseFloor < -90.0 {
		measurements.NoiseFloor = -90.0
	} else if measurements.NoiseFloor > -30.0 {
		measurements.NoiseFloor = -30.0
	}

	// Store 250ms interval samples for data-driven silence candidate detection
	measurements.IntervalSamples = intervals

	// Detect silence regions using threshold already computed from interval distribution
	// The silenceThreshold was calculated above via estimateNoiseFloorAndThreshold()
	measurements.SilenceRegions = findSilenceCandidatesFromIntervals(intervals, silenceThreshold, silMedians)

	// Extract noise profile from best silence region (if available)
	// Uses interval data for all measurements - no file re-reading required
	silenceResult := findBestSilenceRegion(measurements.SilenceRegions, intervals, totalDuration)

	// Store all evaluated candidates for reporting/debugging
	measurements.SilenceCandidates = silenceResult.Candidates

	// Detect voice-activated recordings from digital silence candidate fraction
	measurements.VoiceActivated = detectVoiceActivated(silenceResult.Candidates)

	// Extract noise profile from best silence region BEFORE speech region selection.
	// This allows the SNR margin check in findBestSpeechRegion to penalise candidates
	// that are too close to the noise floor.
	var noiseProfile *NoiseProfile
	if silenceResult.BestRegion != nil {
		// Refine to golden sub-region: find cleanest 10s window within the candidate.
		// This isolates optimal noise profile from long candidates that may span
		// both pre-intentional (noisier) and intentional (cleaner) silence.
		originalRegion := silenceResult.BestRegion
		refinedRegion := refineToGoldenSubregion(originalRegion, intervals)
		wasRefined := refinedRegion.Start != originalRegion.Start || refinedRegion.Duration != originalRegion.Duration

		// Extract noise profile from interval data (no file re-read)
		if profile := extractNoiseProfileFromIntervals(refinedRegion, intervals); profile != nil {
			noiseProfile = profile
			measurements.NoiseProfile = profile

			// Store refinement info for logging/debugging
			if wasRefined {
				profile.WasRefined = true
				profile.OriginalStart = originalRegion.Start
				profile.OriginalDuration = originalRegion.Duration
			}

			// If we got a noise profile measurement, use it as the primary noise floor
			// This is more accurate than the overall RMS_trough because it's from pure silence
			if profile.MeasuredNoiseFloor != 0 && !math.IsInf(profile.MeasuredNoiseFloor, -1) {
				measurements.NoiseFloor = profile.MeasuredNoiseFloor
				measurements.NoiseFloorSource = "silence_profile"
			}
		}
	}

	// Detect speech candidates (must come after elected silence)
	var speechSearchStart time.Duration
	switch {
	case silenceResult.BestRegion != nil:
		speechSearchStart = silenceResult.BestRegion.End
	case len(measurements.SilenceRegions) > 0:
		// Fallback: use end of first silence region
		speechSearchStart = measurements.SilenceRegions[0].End
	default:
		// No silence found - start speech search after 30 seconds
		speechSearchStart = 30 * time.Second
	}

	measurements.SpeechRegions = findSpeechCandidatesFromIntervals(intervals, speechSearchStart, measurements.VoiceActivated, measurements.RMSLevel, measurements.NoiseFloor)

	// Select best speech region (passing noiseProfile for SNR margin checking)
	speechResult := findBestSpeechRegion(measurements.SpeechRegions, intervals, noiseProfile)
	measurements.SpeechCandidates = speechResult.Candidates

	if speechResult.BestRegion != nil {
		// Store elected speech profile
		for i := range speechResult.Candidates {
			if speechResult.Candidates[i].Region.Start == speechResult.BestRegion.Start {
				measurements.SpeechProfile = &speechResult.Candidates[i]
				break
			}
		}
	}

	// Calculate derived suggestions for Pass 2 adaptive processing
	// These are data-driven values based on actual measurements

	// SuggestedGateThreshold: linear amplitude threshold for gate
	// Data-driven calculation based on actual noise floor and quiet speech measurements
	// Gate should open above noise floor but below quiet speech
	//
	// Strategy:
	// - Use RMSTrough (quietest segments with speech) as reference for quiet speech
	// - Calculate adaptive offset based on gap between noise floor and quiet speech
	// - Smaller gap = smaller offset (preserve speech in noisy recordings)
	// - Larger gap = larger offset (more aggressive gating for clean recordings)
	gateThresholdDB := calculateAdaptiveDS201GateThreshold(measurements.NoiseFloor, measurements.RMSTrough)
	measurements.SuggestedGateThreshold = math.Pow(10, gateThresholdDB/20.0)

	// NoiseReductionHeadroom: dB gap between noise floor and quiet speech
	// This determines how aggressively we can apply noise reduction
	// RMS_trough represents the quietest RMS segments (should be near noise floor)
	// RMS_level represents average level (speech)
	// The gap tells us how much "room" we have to reduce noise without affecting speech
	if measurements.RMSLevel != 0 && measurements.NoiseFloor != 0 {
		// Headroom is the gap between average speech level and noise floor
		// Larger headroom = more aggressive NR possible
		measurements.NoiseReductionHeadroom = measurements.RMSLevel - measurements.NoiseFloor
		if measurements.NoiseReductionHeadroom < 0 {
			measurements.NoiseReductionHeadroom = 0 // Sanity check
		}
		if measurements.NoiseReductionHeadroom > 60 {
			measurements.NoiseReductionHeadroom = 60 // Cap at 60dB (very clean recording)
		}
	} else {
		// Fallback: estimate based on integrated loudness
		// Louder recordings typically have better SNR
		switch {
		case measurements.InputI > -20:
			measurements.NoiseReductionHeadroom = 40.0 // Professional recording
		case measurements.InputI > -30:
			measurements.NoiseReductionHeadroom = 25.0 // Typical podcast
		default:
			measurements.NoiseReductionHeadroom = 15.0 // Quiet recording
		}
	}

	return measurements, nil
}

// calculateAdaptiveDS201GateThreshold computes a data-driven gate threshold based on
// the measured noise floor and RMS trough (quiet speech indicator).
//
// Strategy:
//   - The gate threshold should be above the noise floor but below quiet speech
//   - RMSTrough represents the quietest RMS segments (breaths, quiet consonants)
//   - We place the threshold at a data-driven position between noise and quiet speech
//
// Calculation:
//   - Gap = RMSTrough - NoiseFloor (how much "room" between noise and speech)
//   - If gap is small (<10dB): recording is noisy, threshold at 30% into gap
//   - If gap is moderate (10-20dB): typical, threshold at 40% into gap
//   - If gap is large (>20dB): clean recording, threshold at 50% into gap
//
// Safety bounds:
//   - Never below noise floor (would gate during silence)
//   - Never above -35dBFS (would cut quiet speech)
func calculateAdaptiveDS201GateThreshold(noiseFloor, rmsTrough float64) float64 {
	// If RMSTrough is unavailable or invalid, use a sensible fallback
	if rmsTrough == 0 || rmsTrough <= noiseFloor {
		// Fallback: 6dB above noise floor (conservative default)
		threshold := noiseFloor + 6.0
		if threshold > -35.0 {
			threshold = -35.0
		}
		return threshold
	}

	// Calculate the gap between quiet speech and noise
	gap := rmsTrough - noiseFloor

	// Determine the adaptive offset percentage based on gap size
	var offsetPercent float64
	switch {
	case gap < 10.0:
		// Noisy recording: small gap, be conservative (30% into gap)
		// This preserves more speech at the cost of some noise bleed
		offsetPercent = 0.30
	case gap < 20.0:
		// Typical recording: moderate gap (40% into gap)
		offsetPercent = 0.40
	default:
		// Clean recording: large gap, more aggressive (50% into gap)
		offsetPercent = 0.50
	}

	// Calculate threshold: noise floor + (gap * percentage)
	threshold := noiseFloor + (gap * offsetPercent)

	// Safety bounds
	if threshold < noiseFloor+3.0 {
		// Always at least 3dB above noise floor
		threshold = noiseFloor + 3.0
	}
	if threshold > -35.0 {
		// Never gate above -35dBFS (would cut quiet speech)
		threshold = -35.0
	}

	return threshold
}

// createAnalysisFilterGraph creates an AVFilterGraph for Pass 1 analysis.
// Uses astats, aspectralstats, and ebur128 filters to extract measurements.
// Silence detection is now performed in Go using 250ms interval sampling.
func createAnalysisFilterGraph(
	decCtx *ffmpeg.AVCodecContext,
	config *FilterChainConfig,
) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
	// Configure for Pass 1 analysis
	// Uses unified BuildFilterSpec() with Pass1FilterOrder:
	// Downmix → Analysis
	config.Pass = PassAnalysis
	config.FilterOrder = Pass1FilterOrder

	return setupFilterGraph(decCtx, config.BuildFilterSpec())
}
