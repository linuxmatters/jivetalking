// Package processor handles audio analysis and processing
package processor

import (
	stdcontext "context"
	"fmt"
	"math"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// debugLogger is a config-borne debug logging function threaded through the
// processor.
type debugLogger func(format string, args ...any)

// Logf writes to the logger if non-nil, otherwise does nothing.
func (l debugLogger) Logf(format string, args ...any) {
	if l != nil {
		l(format, args...)
	}
}

// RoomToneRegion represents a detected room tone period in the audio.
// This is the elected quiet ambient region used for noise profiling and
// before/after comparison; it is distinct from true digital silence.
type RoomToneRegion struct {
	Start    time.Duration `json:"start"`
	End      time.Duration `json:"end"`
	Duration time.Duration `json:"duration"`
}

// NoiseProfile contains measurements from the elected room tone region.
// These measurements serve as a reference baseline for adaptive filter tuning:
//   - MeasuredNoiseFloor → compand expansion threshold (NoiseRemove)
//   - Entropy → gate release timing and range adaptation (DS201Gate)
//     (See docs/Spectral-Metrics-Reference.md for entropy value interpretations:
//     low entropy 0.08-0.30 = ordered/voiced; high entropy > 0.50 = disordered/noise)
//   - CrestFactor/PeakLevel → transient detection mode selection
//
// Note: The room tone region is also re-measured in Pass 2 and Pass 4 for
// before/after comparison of noise reduction effectiveness.
type NoiseProfile struct {
	Start              time.Duration `json:"start"`                        // Start time of room tone region used
	Duration           time.Duration `json:"duration"`                     // Duration of extracted sample
	MeasuredNoiseFloor float64       `json:"measured_noise_floor"`         // dBFS, RMS level of room tone (average noise)
	PeakLevel          float64       `json:"peak_level"`                   // dBFS, peak level in room tone (transient noise indicator)
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

// RoomToneCandidateMetrics contains measurements for evaluating room tone region candidates.
// These metrics are collected before final selection to enable multi-metric scoring.
// Includes all measurements available from IntervalSample for future filter tuning.
type RoomToneCandidateMetrics struct {
	Region RoomToneRegion `json:"region"` // The room tone region being evaluated

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

	// StabilityScore measures the temporal consistency of the room tone region (0-1).
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

// BaseMeasurements contains fields shared between input (Pass 1) and output (Pass 2) measurements.
// Embedded in both AudioMeasurements and OutputMeasurements to avoid duplication.
type BaseMeasurements struct {
	// Spectral analysis from aspectralstats (all measurements averaged across frames)
	Spectral SpectralMetrics `json:"-"` // Kept flat in JSON by custom marshal helpers

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
// Room tone detection runs in Go using 250ms interval sampling rather than an FFmpeg silence filter.
type AudioMeasurements struct {
	// Embed shared measurement fields
	BaseMeasurements

	// Duration is the total audio length in seconds, captured at file open. It is
	// in-memory UI plumbing only and excluded from the report JSON contract (the
	// custom MarshalJSON/UnmarshalJSON pair serialises named fields explicitly).
	Duration float64 `json:"-"`

	// Input-specific loudness measurements from ebur128
	InputI           float64 `json:"input_i"`            // Integrated loudness (LUFS)
	InputTP          float64 `json:"input_tp"`           // True peak (dBTP)
	InputLRA         float64 `json:"input_lra"`          // Loudness range (LU)
	InputThresh      float64 `json:"input_thresh"`       // Threshold level
	TargetOffset     float64 `json:"target_offset"`      // Offset for normalization
	NoiseFloor       float64 `json:"noise_floor"`        // Derived noise floor (dBFS), three-tier: astats → RMS estimate → ebur128 estimate
	NoiseFloorSource string  `json:"noise_floor_source"` // Source of NoiseFloor: "astats", "rms_estimate", "ebur128_estimate"

	// Adaptive room tone detection thresholds (derived from interval sampling)
	PreScanNoiseFloor   float64 `json:"prescan_noise_floor"`    // Noise floor estimated from interval data (dBFS)
	RoomToneDetectLevel float64 `json:"room_tone_detect_level"` // Adaptive room tone detection threshold (dBFS)

	// Room tone detection results (derived from interval sampling)
	RoomToneRegions []RoomToneRegion `json:"room_tone_regions,omitempty"` // Detected room tone regions

	// 250ms interval samples for data-driven room tone candidate detection
	IntervalSamples []IntervalSample `json:"interval_samples,omitempty"` // Per-interval measurements

	// Scored room tone candidates (for debugging/reporting)
	RoomToneCandidates []RoomToneCandidateMetrics `json:"room_tone_candidates,omitempty"` // All evaluated candidates with scores

	// Speech detection results
	SpeechRegions    []SpeechRegion           `json:"speech_regions,omitempty"`    // Detected speech regions
	SpeechCandidates []SpeechCandidateMetrics `json:"speech_candidates,omitempty"` // All evaluated candidates with scores

	// Elected speech candidate measurements (for adaptive tuning)
	SpeechProfile *SpeechCandidateMetrics `json:"speech_profile,omitempty"` // Best speech candidate metrics

	// Voice-activated recording detection
	VoiceActivated bool `json:"voice_activated"` // True when >= 95% of room tone candidates are digital silence

	// Noise profile extracted from elected room tone candidate
	NoiseProfile *NoiseProfile `json:"noise_profile,omitempty"` // nil if extraction failed

	// Derived suggestions for Pass 2 adaptive processing
	SuggestedGateThreshold float64 `json:"suggested_gate_threshold"` // Suggested gate threshold (linear amplitude)
	NoiseReductionHeadroom float64 `json:"noise_reduction_headroom"` // dB gap between noise and quiet speech
}

// OutputMeasurements contains the measurements from Pass 2 output analysis.
// Uses BaseMeasurements for comparison with AudioMeasurements.
// Does not include room tone detection or noise profile fields (those are input-only).
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

	// Room tone region analysis (same region as Pass 1, for noise reduction comparison)
	RoomToneSample *RoomToneCandidateMetrics `json:"room_tone_sample,omitempty"` // Measurements from same room tone region

	// Speech region analysis (same region as Pass 1, for processing comparison)
	SpeechSample *SpeechCandidateMetrics `json:"speech_sample,omitempty"` // Measurements from same speech region
}

// AnalyzeAudio performs Pass 1: ebur128 + astats + aspectralstats analysis to get measurements
// This is required for adaptive processing in Pass 2.
//
// Implementation note: ebur128 and astats write measurements to frame metadata with lavfi.r128.*
// and lavfi.astats.Overall.* keys respectively. We extract these from the last processed frames.
//
// The noise floor and silence threshold are computed from interval data after the full pass,
// avoiding the need for a separate pre-scan phase.
func AnalyzeAudio(ctx stdcontext.Context, filename string, config *BaseFilterConfig, progressCallback ProgressCallback) (*AudioMeasurements, error) {
	// Default fallback threshold if interval analysis yields insufficient data
	const defaultNoiseFloor = -50.0

	analysisContext := &ProcessingFilterContext{Pass: PassAnalysis}
	collection, err := collectAnalysisFrames(ctx, filename, config, analysisContext, progressCallback)
	if err != nil {
		return nil, err
	}

	intervals := collection.intervals
	silenceIntervals := collection.silenceIntervals
	silMedians := collection.silenceMedians

	measurements, err := buildInputMeasurements(filename, collection, config, defaultNoiseFloor)
	if err != nil {
		return nil, err
	}

	noiseSelection := selectNoiseProfile(measurements, intervals, silenceIntervals, silMedians, config.logger)
	selectSpeechProfile(measurements, intervals, noiseSelection, config.logger)

	assignInputMeasurementSuggestions(measurements)

	return measurements, nil
}

type noiseProfileSelection struct {
	roomToneResult *findBestRoomToneRegionResult
	noiseProfile   *NoiseProfile
}

func selectNoiseProfile(measurements *AudioMeasurements, intervals, silenceIntervals []IntervalSample, silMedians silenceMedians, log debugLogger) noiseProfileSelection {
	measurements.RoomToneRegions = findRoomToneCandidatesFromIntervals(silenceIntervals, measurements.RoomToneDetectLevel, silMedians)

	roomToneResult := findBestRoomToneRegion(measurements.RoomToneRegions, silenceIntervals, log)
	measurements.RoomToneCandidates = roomToneResult.Candidates
	measurements.VoiceActivated = detectVoiceActivated(roomToneResult.Candidates)

	selection := noiseProfileSelection{roomToneResult: roomToneResult}
	if roomToneResult.BestRegion == nil {
		return selection
	}

	originalRegion := roomToneResult.BestRegion
	refinedRegion := refineToGoldenSubregion(originalRegion, intervals)
	wasRefined := refinedRegion.Start != originalRegion.Start || refinedRegion.Duration != originalRegion.Duration

	profile := extractNoiseProfileFromIntervals(refinedRegion, silenceIntervals)
	if profile == nil {
		return selection
	}

	selection.noiseProfile = profile
	measurements.NoiseProfile = profile

	if wasRefined {
		profile.WasRefined = true
		profile.OriginalStart = originalRegion.Start
		profile.OriginalDuration = originalRegion.Duration
	}

	if profile.MeasuredNoiseFloor != 0 && !math.IsInf(profile.MeasuredNoiseFloor, -1) {
		measurements.NoiseFloor = profile.MeasuredNoiseFloor
		measurements.NoiseFloorSource = "silence_profile"
	}

	return selection
}

func selectSpeechProfile(measurements *AudioMeasurements, intervals []IntervalSample, noiseSelection noiseProfileSelection, log debugLogger) {
	speechSearchStart := 30 * time.Second
	switch {
	case noiseSelection.roomToneResult != nil && noiseSelection.roomToneResult.BestRegion != nil:
		speechSearchStart = noiseSelection.roomToneResult.BestRegion.End
	case len(measurements.RoomToneRegions) > 0:
		speechSearchStart = measurements.RoomToneRegions[0].End
	}

	measurements.SpeechRegions = findSpeechCandidatesFromIntervals(intervals, speechSearchStart, measurements.VoiceActivated, measurements.RMSLevel, measurements.NoiseFloor)

	speechResult := findBestSpeechRegion(measurements.SpeechRegions, intervals, noiseSelection.noiseProfile, log)
	measurements.SpeechCandidates = speechResult.Candidates

	if speechResult.BestRegion == nil {
		return
	}

	for i := range speechResult.Candidates {
		if speechResult.Candidates[i].Region.Start == speechResult.BestRegion.Start {
			measurements.SpeechProfile = &speechResult.Candidates[i]
			return
		}
	}
}

func buildInputMeasurements(filename string, collection *analysisFrameCollection, config *BaseFilterConfig, defaultNoiseFloor float64) (*AudioMeasurements, error) {
	acc := collection.accumulators

	noiseFloorEstimate, silenceThreshold, ok := estimateNoiseFloorAndThreshold(collection.silenceIntervals, collection.silenceMedians)
	if !ok {
		noiseFloorEstimate = defaultNoiseFloor
		silenceThreshold = calculateAdaptiveSilenceThreshold(defaultNoiseFloor)
	}

	measurements := &AudioMeasurements{
		Duration:            collection.totalDuration,
		PreScanNoiseFloor:   noiseFloorEstimate,
		RoomToneDetectLevel: silenceThreshold,
		IntervalSamples:     collection.intervals,
	}

	if !acc.ebur128Found {
		return nil, fmt.Errorf("ebur128 measurements not found in metadata for file: %s", filename)
	}

	measurements.InputI = acc.ebur128InputI
	measurements.InputTP = acc.ebur128InputTP
	measurements.InputLRA = acc.ebur128InputLRA
	measurements.InputThresh = acc.ebur128InputI - 10.0
	measurements.TargetOffset = config.Loudnorm.TargetI - acc.ebur128InputI
	measurements.MomentaryLoudness = acc.ebur128InputM
	measurements.ShortTermLoudness = acc.ebur128InputS
	measurements.SamplePeak = acc.ebur128InputSP

	measurements.Spectral = acc.finalizeSpectral()
	assignAstatsMeasurements(measurements, acc)
	assignInputNoiseFloor(measurements, acc)

	return measurements, nil
}

func assignAstatsMeasurements(measurements *AudioMeasurements, acc *metadataAccumulators) {
	if !acc.astatsFound {
		return
	}

	measurements.DynamicRange = acc.astatsDynamicRange
	measurements.RMSLevel = acc.astatsRMSLevel
	measurements.PeakLevel = acc.astatsPeakLevel
	measurements.RMSTrough = acc.astatsRMSTrough
	measurements.RMSPeak = acc.astatsRMSPeak
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

func assignInputNoiseFloor(measurements *AudioMeasurements, acc *metadataAccumulators) {
	switch {
	case acc.astatsRMSTrough != 0 && !math.IsInf(acc.astatsRMSTrough, -1):
		measurements.NoiseFloor = acc.astatsRMSTrough
		measurements.NoiseFloorSource = "astats"
	case acc.astatsRMSLevel != 0 && !math.IsInf(acc.astatsRMSLevel, -1):
		measurements.NoiseFloor = acc.astatsRMSLevel - 15.0
		measurements.NoiseFloorSource = "rms_estimate"
	default:
		var noiseFloorOffset float64
		switch {
		case measurements.InputI > -20:
			noiseFloorOffset = 18.0
		case measurements.InputI > -30:
			noiseFloorOffset = 12.0
		default:
			noiseFloorOffset = 8.0
		}
		measurements.NoiseFloor = measurements.InputThresh - noiseFloorOffset
		measurements.NoiseFloorSource = "ebur128_estimate"
	}

	if measurements.NoiseFloor < -90.0 {
		measurements.NoiseFloor = -90.0
	} else if measurements.NoiseFloor > -30.0 {
		measurements.NoiseFloor = -30.0
	}
}

func assignInputMeasurementSuggestions(measurements *AudioMeasurements) {
	gateThresholdDB := calculateAdaptiveDS201GateThreshold(measurements.NoiseFloor, measurements.RMSTrough)
	measurements.SuggestedGateThreshold = math.Pow(10, gateThresholdDB/20.0)

	if measurements.RMSLevel != 0 && measurements.NoiseFloor != 0 {
		measurements.NoiseReductionHeadroom = measurements.RMSLevel - measurements.NoiseFloor
		if measurements.NoiseReductionHeadroom < 0 {
			measurements.NoiseReductionHeadroom = 0
		}
		if measurements.NoiseReductionHeadroom > 60 {
			measurements.NoiseReductionHeadroom = 60
		}
		return
	}

	switch {
	case measurements.InputI > -20:
		measurements.NoiseReductionHeadroom = 40.0
	case measurements.InputI > -30:
		measurements.NoiseReductionHeadroom = 25.0
	default:
		measurements.NoiseReductionHeadroom = 15.0
	}
}

type analysisFrameCollection struct {
	accumulators     *metadataAccumulators
	intervals        []IntervalSample
	silenceIntervals []IntervalSample
	silenceMedians   silenceMedians
	totalDuration    float64 // total audio length, seconds (from input metadata)
}

func collectAnalysisFrames(ctx stdcontext.Context, filename string, config *BaseFilterConfig, context *ProcessingFilterContext, progressCallback ProgressCallback) (*analysisFrameCollection, error) {
	reader, metadata, err := audio.OpenAudioFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer reader.Close()

	totalDuration := metadata.Duration
	sampleRate := float64(metadata.SampleRate)
	samplesPerFrame := 4096.0
	estimatedTotalFrames := (totalDuration * sampleRate) / samplesPerFrame

	filterGraph, bufferSrcCtx, bufferSinkCtx, err := createAnalysisFilterGraph(
		reader.GetDecoderContext(),
		config,
		context,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create filter graph: %w", err)
	}
	var filterFreed bool
	defer func() {
		if !filterFreed && filterGraph != nil {
			ffmpeg.AVFilterGraphFree(&filterGraph)
		}
	}()

	frameCount := 0
	updateInterval := 100
	currentLevel := 0.0

	acc := &metadataAccumulators{}

	const intervalDuration = 250 * time.Millisecond
	var intervals []IntervalSample
	var silenceIntervals []IntervalSample
	var intervalAcc intervalAccumulator
	var intervalStartTime time.Duration

	var inputSamplesProcessed int64
	inputSampleRate := float64(reader.GetDecoderContext().SampleRate())

	if err := runFilterGraph(ctx, reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
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
			currentLevel = calculateFrameLevel(inputFrame)

			inputFrameTime := time.Duration(float64(inputSamplesProcessed) / inputSampleRate * float64(time.Second))
			inputSamplesProcessed += int64(inputFrame.NbSamples())
			intervalAcc.addFrameRMSAndPeak(inputFrame)

			if inputFrameTime-intervalStartTime >= intervalDuration {
				finalised := intervalAcc.finalize(intervalStartTime)
				intervals = append(intervals, finalised)
				if config.Analysis.RoomToneScanDuration > 0 && intervalStartTime < config.Analysis.RoomToneScanDuration {
					silenceIntervals = append(silenceIntervals, finalised)
				}
				intervalStartTime = inputFrameTime
				intervalAcc.reset()
			}

			if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
				progress := float64(frameCount) / estimatedTotalFrames
				if progress > 1.0 {
					progress = 1.0
				}
				progressCallback(ProgressUpdate{
					Pass:     context.Pass,
					PassName: "Analyzing",
					Progress: progress,
					Level:    currentLevel,
					Duration: totalDuration,
				})
			}
			frameCount++
		},
		OnFrame: func(_, filteredFrame *ffmpeg.AVFrame) error {
			metadata := filteredFrame.Metadata()
			spectral := extractSpectralMetrics(metadata)

			extractFrameMetadata(metadata, acc, spectral)
			intervalAcc.add(extractIntervalFrameMetrics(metadata, spectral))

			return nil
		},
	}); err != nil {
		return nil, err
	}

	if intervalAcc.rawSampleCount > 0 {
		finalised := intervalAcc.finalize(intervalStartTime)
		intervals = append(intervals, finalised)
		if config.Analysis.RoomToneScanDuration > 0 && intervalStartTime < config.Analysis.RoomToneScanDuration {
			silenceIntervals = append(silenceIntervals, finalised)
		}
	}

	if config.Analysis.RoomToneScanDuration == 0 {
		silenceIntervals = intervals
	}

	ffmpeg.AVFilterGraphFree(&filterGraph)
	filterFreed = true

	return &analysisFrameCollection{
		accumulators:     acc,
		intervals:        intervals,
		silenceIntervals: silenceIntervals,
		silenceMedians:   computeSilenceMedians(silenceIntervals),
		totalDuration:    totalDuration,
	}, nil
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
// Silence detection runs in Go using 250ms interval sampling, not in this graph.
func createAnalysisFilterGraph(
	decCtx *ffmpeg.AVCodecContext,
	config *BaseFilterConfig,
	context *ProcessingFilterContext,
) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
	if context == nil {
		context = &ProcessingFilterContext{Pass: PassAnalysis}
	}
	if context.Pass == 0 {
		context.Pass = PassAnalysis
	}

	analysisConfig := deriveEffectiveFilterConfig(config)
	analysisConfig.FilterOrder = cloneFilterOrder(Pass1FilterOrder)

	return setupFilterGraph(decCtx, analysisConfig.BuildFilterSpec())
}
