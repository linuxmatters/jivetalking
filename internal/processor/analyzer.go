// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// SilenceRegion represents a detected silence period in the audio
type SilenceRegion struct {
	Start    time.Duration `json:"start"`
	End      time.Duration `json:"end"`
	Duration time.Duration `json:"duration"`
}

// NoiseProfile contains information about an extracted noise sample
type NoiseProfile struct {
	FilePath           string        `json:"file_path"`                    // Path to extracted noise sample WAV file
	Start              time.Duration `json:"start"`                        // Start time of silence region used
	Duration           time.Duration `json:"duration"`                     // Duration of extracted sample
	MeasuredNoiseFloor float64       `json:"measured_noise_floor"`         // dBFS, RMS level of silence (average noise)
	PeakLevel          float64       `json:"peak_level"`                   // dBFS, peak level in silence (transient noise indicator)
	CrestFactor        float64       `json:"crest_factor"`                 // Peak - RMS in dB (high = impulsive noise, low = steady noise)
	Entropy            float64       `json:"entropy"`                      // Signal randomness (1.0 = white noise, lower = tonal noise like hum)
	ExtractionWarning  string        `json:"extraction_warning,omitempty"` // Warning message if extraction had issues
}

// Cached metadata keys for frame extraction - avoids per-frame C string allocations
// These use GlobalCStr which maintains an internal cache, so identical strings share the same CStr
var (
	metaKeySpectralCentroid  = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.centroid")
	metaKeySpectralRolloff   = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.rolloff")
	metaKeyDynamicRange      = ffmpeg.GlobalCStr("lavfi.astats.1.Dynamic_range")
	metaKeyRMSLevel          = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_level")
	metaKeyPeakLevel         = ffmpeg.GlobalCStr("lavfi.astats.1.Peak_level")
	metaKeyRMSTrough         = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_trough")
	metaKeyDCOffset          = ffmpeg.GlobalCStr("lavfi.astats.1.DC_offset")
	metaKeyFlatFactor        = ffmpeg.GlobalCStr("lavfi.astats.1.Flat_factor")
	metaKeyZeroCrossingsRate = ffmpeg.GlobalCStr("lavfi.astats.1.Zero_crossings_rate")
	metaKeyMaxDifference     = ffmpeg.GlobalCStr("lavfi.astats.1.Max_difference")
	metaKeyEntropy           = ffmpeg.GlobalCStr("lavfi.astats.1.Entropy")
	metaKeyEbur128I          = ffmpeg.GlobalCStr("lavfi.r128.I")
	metaKeyEbur128TruePeak   = ffmpeg.GlobalCStr("lavfi.r128.true_peak")
	metaKeyEbur128LRA        = ffmpeg.GlobalCStr("lavfi.r128.LRA")

	// Silence detection metadata keys (from silencedetect filter)
	// For mono audio these are lavfi.silence_start.1, lavfi.silence_end.1, lavfi.silence_duration.1
	metaKeySilenceStart    = ffmpeg.GlobalCStr("lavfi.silence_start")
	metaKeySilenceStart1   = ffmpeg.GlobalCStr("lavfi.silence_start.1")
	metaKeySilenceEnd      = ffmpeg.GlobalCStr("lavfi.silence_end")
	metaKeySilenceEnd1     = ffmpeg.GlobalCStr("lavfi.silence_end.1")
	metaKeySilenceDuration = ffmpeg.GlobalCStr("lavfi.silence_duration")
	metaKeySilenceDur1     = ffmpeg.GlobalCStr("lavfi.silence_duration.1")
)

// metadataAccumulators holds all accumulator variables for frame metadata extraction.
// Spectral stats (centroid, rolloff) are averaged across all frames.
// astats and ebur128 values are cumulative, so we keep the latest.
type metadataAccumulators struct {
	// Spectral statistics (averaged across frames)
	spectralCentroidSum float64
	spectralRolloffSum  float64
	spectralFrameCount  int

	// astats measurements (cumulative - we keep latest values)
	astatsDynamicRange      float64
	astatsRMSLevel          float64
	astatsPeakLevel         float64
	astatsRMSTrough         float64
	astatsDCOffset          float64
	astatsFlatFactor        float64
	astatsZeroCrossingsRate float64
	astatsMaxDifference     float64
	astatsFound             bool

	// ebur128 measurements (cumulative - we keep latest values)
	ebur128InputI   float64
	ebur128InputTP  float64
	ebur128InputLRA float64
	ebur128Found    bool

	// Silence detection (collected across frames)
	// silencedetect sets lavfi.silence_start on first frame of silence,
	// then lavfi.silence_end and lavfi.silence_duration on first frame after silence ends
	silenceRegions      []SilenceRegion
	pendingSilenceStart float64 // Pending silence start timestamp (seconds)
	hasPendingSilence   bool    // Whether we have a pending silence start
}

// getFloatMetadata extracts a float value from the metadata dictionary
func getFloatMetadata(metadata *ffmpeg.AVDictionary, key *ffmpeg.CStr) (float64, bool) {
	if entry := ffmpeg.AVDictGet(metadata, key, nil, 0); entry != nil {
		if value, err := strconv.ParseFloat(entry.Value().String(), 64); err == nil {
			return value, true
		}
	}
	return 0.0, false
}

// extractFrameMetadata extracts audio analysis metadata from a filtered frame.
// Updates accumulators with spectral, astats, and ebur128 measurements.
// Called from both the main processing loop and the flush loop.
func extractFrameMetadata(metadata *ffmpeg.AVDictionary, acc *metadataAccumulators) {
	if metadata == nil {
		return
	}

	// Extract spectral centroid (Hz) - where energy is concentrated
	// For mono audio, spectral stats are under channel .1
	if value, ok := getFloatMetadata(metadata, metaKeySpectralCentroid); ok {
		acc.spectralCentroidSum += value
		acc.spectralFrameCount++
	}

	// Extract spectral rolloff (Hz) - high-frequency energy dropoff point
	if value, ok := getFloatMetadata(metadata, metaKeySpectralRolloff); ok {
		acc.spectralRolloffSum += value
	}

	// Extract astats measurements (cumulative, so we keep the latest)
	// For mono audio, stats are under channel .1
	if value, ok := getFloatMetadata(metadata, metaKeyDynamicRange); ok {
		acc.astatsDynamicRange = value
		acc.astatsFound = true
	}

	if value, ok := getFloatMetadata(metadata, metaKeyRMSLevel); ok {
		acc.astatsRMSLevel = value
	}

	if value, ok := getFloatMetadata(metadata, metaKeyPeakLevel); ok {
		acc.astatsPeakLevel = value
	}

	// Extract RMS_trough - RMS level of quietest segments (best noise floor indicator for speech)
	// In speech audio, quiet inter-word periods contain primarily ambient/electronic noise
	if value, ok := getFloatMetadata(metadata, metaKeyRMSTrough); ok {
		acc.astatsRMSTrough = value
	}

	// Extract DC_offset - mean amplitude displacement from zero
	// High values indicate DC bias that should be removed before processing
	if value, ok := getFloatMetadata(metadata, metaKeyDCOffset); ok {
		acc.astatsDCOffset = value
	}

	// Extract Flat_factor - consecutive samples at peak levels (indicates clipping)
	// High values suggest pre-existing limiting or clipping damage
	if value, ok := getFloatMetadata(metadata, metaKeyFlatFactor); ok {
		acc.astatsFlatFactor = value
	}

	// Extract Zero_crossings_rate - rate of zero crossings per sample
	// Low ZCR = bass-heavy/sustained tones, High ZCR = noise/sibilance
	if value, ok := getFloatMetadata(metadata, metaKeyZeroCrossingsRate); ok {
		acc.astatsZeroCrossingsRate = value
	}

	// Extract Max_difference - largest sample-to-sample change
	// High values indicate impulsive sounds (clicks, pops) - useful for adeclick tuning
	if value, ok := getFloatMetadata(metadata, metaKeyMaxDifference); ok {
		acc.astatsMaxDifference = value
	}

	// Extract ebur128 measurements (cumulative loudness analysis)
	// ebur128 provides: M.* (momentary), S.* (short-term), I (integrated), LRA, sample_peak, true_peak
	// We need the integrated loudness measurements for normalization
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128I); ok {
		acc.ebur128InputI = value
		acc.ebur128Found = true
	}

	if value, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
		acc.ebur128InputTP = value
	}

	if value, ok := getFloatMetadata(metadata, metaKeyEbur128LRA); ok {
		acc.ebur128InputLRA = value
	}

	// Extract silence detection metadata
	// silencedetect sets lavfi.silence_start on the first frame of a silence region,
	// then lavfi.silence_end and lavfi.silence_duration on the first frame after silence ends.
	// For mono audio, these may be suffixed with .1
	var silenceStart float64
	var hasSilenceStart bool
	if value, ok := getFloatMetadata(metadata, metaKeySilenceStart); ok {
		silenceStart = value
		hasSilenceStart = true
	} else if value, ok := getFloatMetadata(metadata, metaKeySilenceStart1); ok {
		silenceStart = value
		hasSilenceStart = true
	}

	if hasSilenceStart {
		acc.pendingSilenceStart = silenceStart
		acc.hasPendingSilence = true
	}

	// Check for silence end - this completes a silence region
	var silenceEnd, silenceDuration float64
	var hasSilenceEnd bool
	if value, ok := getFloatMetadata(metadata, metaKeySilenceEnd); ok {
		silenceEnd = value
		hasSilenceEnd = true
	} else if value, ok := getFloatMetadata(metadata, metaKeySilenceEnd1); ok {
		silenceEnd = value
		hasSilenceEnd = true
	}

	if hasSilenceEnd {
		// Get duration - try both keys
		if value, ok := getFloatMetadata(metadata, metaKeySilenceDuration); ok {
			silenceDuration = value
		} else if value, ok := getFloatMetadata(metadata, metaKeySilenceDur1); ok {
			silenceDuration = value
		}

		// Record the completed silence region
		if acc.hasPendingSilence {
			region := SilenceRegion{
				Start:    time.Duration(acc.pendingSilenceStart * float64(time.Second)),
				End:      time.Duration(silenceEnd * float64(time.Second)),
				Duration: time.Duration(silenceDuration * float64(time.Second)),
			}
			acc.silenceRegions = append(acc.silenceRegions, region)
			acc.hasPendingSilence = false
		}
	}
}

// AudioMeasurements contains the measurements from Pass 1 analysis
// Uses ebur128 (LUFS/LRA), astats (dynamic range/noise floor), aspectralstats (spectral analysis),
// and silencedetect (silence regions for noise profile extraction)
type AudioMeasurements struct {
	InputI       float64 `json:"input_i"`       // Integrated loudness (LUFS)
	InputTP      float64 `json:"input_tp"`      // True peak (dBTP)
	InputLRA     float64 `json:"input_lra"`     // Loudness range (LU)
	InputThresh  float64 `json:"input_thresh"`  // Threshold level
	TargetOffset float64 `json:"target_offset"` // Offset for normalization
	NoiseFloor   float64 `json:"noise_floor"`   // Measured noise floor from astats (dBFS)

	// Spectral analysis for adaptive de-esser frequency targeting
	SpectralCentroid float64 `json:"spectral_centroid"` // Average spectral centroid (Hz) - where energy is concentrated
	SpectralRolloff  float64 `json:"spectral_rolloff"`  // Average spectral rolloff (Hz) - high-frequency energy dropoff point

	// Time-domain statistics from astats for adaptive processing
	DynamicRange float64 `json:"dynamic_range"` // Measured dynamic range (dB)
	RMSLevel     float64 `json:"rms_level"`     // Overall RMS level (dBFS)
	PeakLevel    float64 `json:"peak_level"`    // Overall peak level (dBFS)
	RMSTrough    float64 `json:"rms_trough"`    // RMS level of quietest segments - best noise floor indicator (dBFS)

	// Additional astats measurements for adaptive processing
	DCOffset          float64 `json:"dc_offset"`           // Mean amplitude displacement from zero (needs dcshift if significant)
	FlatFactor        float64 `json:"flat_factor"`         // Consecutive samples at peak (indicates clipping/limiting)
	ZeroCrossingsRate float64 `json:"zero_crossings_rate"` // Zero crossing rate (low=bass, high=noise/sibilance)
	MaxDifference     float64 `json:"max_difference"`      // Largest sample-to-sample change (indicates clicks/pops)

	// Silence detection results from silencedetect filter
	SilenceRegions []SilenceRegion `json:"silence_regions,omitempty"` // Detected silence regions

	// Noise profile extracted from longest silence region
	NoiseProfile *NoiseProfile `json:"noise_profile,omitempty"` // nil if extraction failed

	// Derived suggestions for Pass 2 adaptive processing
	SuggestedGateThreshold float64 `json:"suggested_gate_threshold"` // Suggested gate threshold (linear amplitude)
	NoiseReductionHeadroom float64 `json:"noise_reduction_headroom"` // dB gap between noise and quiet speech

	// Pass 2 noise profile processing stats (populated during processing)
	NoiseProfileFramesFed int `json:"noise_profile_frames_fed,omitempty"` // Number of noise frames fed for spectral learning
	MainFramesProcessed   int `json:"main_frames_processed,omitempty"`    // Number of main audio frames processed
}

// AnalyzeAudio performs Pass 1: ebur128 + astats + aspectralstats analysis to get measurements
// This is required for adaptive processing in Pass 2.
//
// Implementation note: ebur128 and astats write measurements to frame metadata with lavfi.r128.*
// and lavfi.astats.Overall.* keys respectively. We extract these from the last processed frames.
func AnalyzeAudio(filename string, targetI, targetTP, targetLRA float64, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements)) (*AudioMeasurements, error) {
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
		targetI, targetTP, targetLRA,
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

	// Process all frames through the filter
	filteredFrame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&filteredFrame)

	// Track frames for periodic progress updates
	frameCount := 0
	updateInterval := 100 // Send progress update every N frames
	currentLevel := 0.0

	// Accumulators for frame metadata extraction
	acc := &metadataAccumulators{}

	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			return nil, fmt.Errorf("failed to read frame: %w", err)
		}
		if frame == nil {
			break // EOF
		}

		// Calculate audio level from frame
		currentLevel = calculateFrameLevel(frame)

		// Send periodic progress updates based on frame count
		if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
			progress := float64(frameCount) / estimatedTotalFrames
			if progress > 1.0 {
				progress = 1.0
			}
			progressCallback(1, "Analyzing", progress, currentLevel, nil)
		}
		frameCount++

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			return nil, fmt.Errorf("failed to add frame to filter: %w", err)
		}

		// Pull filtered frames and extract spectral metadata
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				return nil, fmt.Errorf("failed to get filtered frame: %w", err)
			}

			// Extract measurements from frame metadata
			extractFrameMetadata(filteredFrame.Metadata(), acc)

			ffmpeg.AVFrameUnref(filteredFrame)
		}
	}

	// Flush the filter graph
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err != nil {
		return nil, fmt.Errorf("failed to flush filter: %w", err)
	}

	// Pull remaining frames
	for {
		if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return nil, fmt.Errorf("failed to get filtered frame: %w", err)
		}

		// Extract measurements from remaining frames
		extractFrameMetadata(filteredFrame.Metadata(), acc)

		ffmpeg.AVFrameUnref(filteredFrame)
	}

	// Free the filter graph
	ffmpeg.AVFilterGraphFree(&filterGraph)
	filterFreed = true

	// Create measurements struct and populate from accumulators
	measurements := &AudioMeasurements{}

	// Populate ebur128 loudness measurements
	if acc.ebur128Found {
		measurements.InputI = acc.ebur128InputI
		measurements.InputTP = acc.ebur128InputTP
		measurements.InputLRA = acc.ebur128InputLRA
		// Calculate threshold based on integrated loudness (ebur128 doesn't provide this directly)
		// Threshold is typically around 10 LU below the integrated loudness
		measurements.InputThresh = acc.ebur128InputI - 10.0
		// Target offset for normalization (difference between measured and target)
		measurements.TargetOffset = targetI - acc.ebur128InputI
	} else {
		return nil, fmt.Errorf("ebur128 measurements not found in metadata for file: %s", filename)
	}

	// Calculate average spectral statistics
	if acc.spectralFrameCount > 0 {
		measurements.SpectralCentroid = acc.spectralCentroidSum / float64(acc.spectralFrameCount)
		measurements.SpectralRolloff = acc.spectralRolloffSum / float64(acc.spectralFrameCount)
	}

	// Store astats measurements (if captured)
	if acc.astatsFound {
		measurements.DynamicRange = acc.astatsDynamicRange
		measurements.RMSLevel = acc.astatsRMSLevel
		measurements.PeakLevel = acc.astatsPeakLevel
		measurements.RMSTrough = acc.astatsRMSTrough

		// Additional astats measurements for adaptive processing
		measurements.DCOffset = acc.astatsDCOffset
		measurements.FlatFactor = acc.astatsFlatFactor
		measurements.ZeroCrossingsRate = acc.astatsZeroCrossingsRate
		measurements.MaxDifference = acc.astatsMaxDifference
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

	if acc.astatsRMSTrough != 0 && !math.IsInf(acc.astatsRMSTrough, -1) {
		// Tier 1: Use RMS_trough (best - actual measurement of quiet segments)
		measurements.NoiseFloor = acc.astatsRMSTrough
	} else if acc.astatsRMSLevel != 0 && !math.IsInf(acc.astatsRMSLevel, -1) {
		// Tier 2: Estimate from overall RMS level
		// Typical speech has quiet segments 12-18dB below average RMS; use 15dB as balanced estimate
		measurements.NoiseFloor = acc.astatsRMSLevel - 15.0
	} else {
		// Tier 3: Estimate from ebur128 integrated loudness threshold
		// Louder recordings typically have better SNR (lower relative noise floor)
		var noiseFloorOffset float64
		if measurements.InputI > -20 {
			noiseFloorOffset = 18.0 // Professional: very low noise floor
		} else if measurements.InputI > -30 {
			noiseFloorOffset = 12.0 // Typical podcast: moderate noise floor
		} else {
			noiseFloorOffset = 8.0 // Quiet source: higher relative noise
		}
		measurements.NoiseFloor = measurements.InputThresh - noiseFloorOffset
	}

	// Safety clamp: -90dB (digital silence) to -30dB (very noisy environment)
	// Prevents extreme values while allowing wide range of recording quality
	if measurements.NoiseFloor < -90.0 {
		measurements.NoiseFloor = -90.0
	} else if measurements.NoiseFloor > -30.0 {
		measurements.NoiseFloor = -30.0
	}

	// Store detected silence regions
	measurements.SilenceRegions = acc.silenceRegions

	// Extract noise profile from best silence region (if available)
	// This provides precise noise floor measurement from actual silence in the recording
	if bestRegion := findBestSilenceRegion(acc.silenceRegions); bestRegion != nil {
		tempDir := filepath.Dir(filename) // Use same directory as input file
		if profile, err := extractNoiseProfile(filename, bestRegion, tempDir); err == nil && profile != nil {
			measurements.NoiseProfile = profile
			// If we got a noise profile measurement, use it as the primary noise floor
			// This is more accurate than the overall RMS_trough because it's from pure silence
			if profile.MeasuredNoiseFloor != 0 && !math.IsInf(profile.MeasuredNoiseFloor, -1) {
				measurements.NoiseFloor = profile.MeasuredNoiseFloor
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
	gateThresholdDB := calculateAdaptiveGateThreshold(measurements.NoiseFloor, measurements.RMSTrough)
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
		if measurements.InputI > -20 {
			measurements.NoiseReductionHeadroom = 40.0 // Professional recording
		} else if measurements.InputI > -30 {
			measurements.NoiseReductionHeadroom = 25.0 // Typical podcast
		} else {
			measurements.NoiseReductionHeadroom = 15.0 // Quiet recording
		}
	}

	return measurements, nil
}

// calculateAdaptiveGateThreshold computes a data-driven gate threshold based on
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
func calculateAdaptiveGateThreshold(noiseFloor, rmsTrough float64) float64 {
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

// createAnalysisFilterGraph creates an AVFilterGraph for Pass 1 analysis
// Uses astats, aspectralstats, silencedetect, and ebur128 filters to extract measurements
func createAnalysisFilterGraph(
	decCtx *ffmpeg.AVCodecContext,
	targetI, targetTP, targetLRA float64,
) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
	// Build filter string for analysis pass
	// Filter chain order:
	// 1. silencedetect - detect silence regions for noise profile extraction
	//    - noise=-50dB: threshold for silence detection (fairly sensitive)
	//    - duration=0.5: minimum silence duration to detect (0.5s catches most pauses)
	// 2. astats - provides noise floor, dynamic range, and additional measurements for adaptive processing:
	//    - Noise_floor, Dynamic_range, RMS_level, Peak_level: core measurements
	//    - DC_offset: detects DC bias needing removal
	//    - Flat_factor: detects pre-existing clipping/limiting
	//    - Zero_crossings_rate: helps classify noise type
	//    - Max_difference: detects impulsive sounds (clicks/pops)
	// 3. aspectralstats - measures spectral centroid and rolloff for adaptive de-esser targeting
	// 4. ebur128 - provides integrated loudness (LUFS), true peak, and LRA via metadata
	// Note: reset=0 (default) allows astats to accumulate statistics across all frames for Overall measurements
	// ebur128 metadata=1 writes per-frame loudness data to frame metadata (lavfi.r128.* keys)
	filterSpec := fmt.Sprintf("silencedetect=noise=-50dB:duration=0.5,astats=metadata=1:measure_perchannel=Noise_floor+Dynamic_range+RMS_level+Peak_level+DC_offset+Flat_factor+Zero_crossings_rate+Max_difference,aspectralstats=win_size=2048:win_func=hann:measure=centroid+rolloff,ebur128=metadata=1:target=%.0f",
		targetI)

	return setupFilterGraph(decCtx, filterSpec)
}

// Minimum silence durations for noise profile extraction
const (
	idealSilenceDuration   = 10 * time.Second // Prefer silence regions >= 10s
	minimumSilenceDuration = 2 * time.Second  // Accept >= 2s with warning
	minimumSilenceStart    = 30 * time.Second // Only consider silence regions starting after 30s
)

// findBestSilenceRegion finds the best silence region for noise profile extraction.
// Returns the FIRST region meeting the length criteria (>= 2s), since room noise
// is typically recorded at the start of podcast recordings.
// Prefers regions >= 10s, accepts >= 2s with a warning.
// Returns nil if no suitable region is found.
func findBestSilenceRegion(regions []SilenceRegion) *SilenceRegion {
	if len(regions) == 0 {
		return nil
	}

	// Find the first region meeting our criteria
	// Regions are already in chronological order from silencedetect
	// Only consider regions starting after minimumSilenceStart (30s) to skip intro music/jingles
	var firstIdeal *SilenceRegion
	var firstAcceptable *SilenceRegion
	var longest *SilenceRegion

	for i := range regions {
		r := &regions[i]

		// Track longest for warning message (regardless of start time)
		if longest == nil || r.Duration > longest.Duration {
			longest = r
		}

		// Skip regions that start too early (before 30s)
		if r.Start < minimumSilenceStart {
			continue
		}

		// Prefer first ideal region (>= 10s)
		if firstIdeal == nil && r.Duration >= idealSilenceDuration {
			firstIdeal = r
			break // Found ideal, no need to continue
		}

		// Track first acceptable region (>= 2s)
		if firstAcceptable == nil && r.Duration >= minimumSilenceDuration {
			firstAcceptable = r
		}
	}

	if firstIdeal != nil {
		return firstIdeal
	}

	if firstAcceptable != nil {
		// Short but acceptable - warning will be noted in profile
		return firstAcceptable
	}

	// No suitable silence region found
	return nil
}

// extractNoiseProfile extracts a noise sample from the silence region and measures its characteristics.
// The extracted sample is written as a WAV file for use with afftdn's noise profiling.
// Uses atrim + astats filter chain to measure RMS level, peak level, and entropy.
// Returns nil, nil if no suitable silence region exists or extraction fails non-fatally.
func extractNoiseProfile(filename string, region *SilenceRegion, tempDir string) (*NoiseProfile, error) {
	if region == nil {
		return nil, nil
	}

	// Generate output filename for the noise profile WAV
	baseName := filepath.Base(filename)
	ext := filepath.Ext(baseName)
	nameWithoutExt := baseName[:len(baseName)-len(ext)]
	outputPath := filepath.Join(tempDir, nameWithoutExt+"_noise_profile.wav")

	// Open the audio file
	reader, _, err := audio.OpenAudioFile(filename)
	if err != nil {
		return nil, nil // Non-fatal - extraction skipped
	}
	defer reader.Close()

	decCtx := reader.GetDecoderContext()

	// Create filter graph for extraction with trimming, format conversion, and measurement
	// atrim: extract only the silence region
	// aformat: convert to WAV-compatible format (44100Hz, mono, S16)
	// astats: measure noise floor + entropy of extracted sample
	//   - RMS_level, Peak_level: noise characteristics
	//   - Entropy: 1.0 = white noise (broadband), lower = tonal noise (hum/buzz)
	filterSpec := fmt.Sprintf(
		"atrim=start=%f:end=%f,aformat=sample_rates=44100:channel_layouts=mono:sample_fmts=s16,astats=metadata=1:measure_perchannel=RMS_level+Peak_level+Entropy",
		region.Start.Seconds(), region.End.Seconds())

	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(decCtx, filterSpec)
	if err != nil {
		return nil, nil // Non-fatal - extraction skipped
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	// Create WAV encoder for writing the noise profile
	wavEncoder, err := createWAVEncoder(outputPath, bufferSinkCtx)
	if err != nil {
		return nil, nil // Non-fatal - extraction skipped
	}
	defer wavEncoder.Close()

	// Process frames through filter to measure noise and write to WAV
	filteredFrame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&filteredFrame)

	// Track measurements from astats
	var measuredNoiseFloor float64
	var peakLevel float64
	var entropy float64
	var noiseFloorFound bool
	var framesProcessed int64
	var wavWriteError error

	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			break
		}
		if frame == nil {
			break // EOF
		}

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			continue // Skip problematic frames
		}

		// Pull filtered frames
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				continue
			}

			// Write frame to WAV file (if encoder available)
			if wavEncoder != nil && wavWriteError == nil {
				if err := wavEncoder.WriteFrame(filteredFrame); err != nil {
					wavWriteError = err // Stop trying to write after first error
				}
			}

			// Extract noise measurements from metadata
			if metadata := filteredFrame.Metadata(); metadata != nil {
				// RMS_level: average noise floor
				if value, ok := getFloatMetadata(metadata, metaKeyRMSLevel); ok {
					measuredNoiseFloor = value
					noiseFloorFound = true
				}
				// Peak_level: transient noise indicator
				if value, ok := getFloatMetadata(metadata, metaKeyPeakLevel); ok {
					peakLevel = value
				}
				// Entropy: noise type classifier (1.0 = broadband/white, lower = tonal/hum)
				if value, ok := getFloatMetadata(metadata, metaKeyEntropy); ok {
					entropy = value
				}
			}

			framesProcessed++
			ffmpeg.AVFrameUnref(filteredFrame)
		}
	}

	// Flush filter graph
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err == nil {
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				break
			}

			// Write remaining frames to WAV
			if wavEncoder != nil && wavWriteError == nil {
				if err := wavEncoder.WriteFrame(filteredFrame); err != nil {
					wavWriteError = err
				}
			}

			if metadata := filteredFrame.Metadata(); metadata != nil {
				if value, ok := getFloatMetadata(metadata, metaKeyRMSLevel); ok {
					measuredNoiseFloor = value
					noiseFloorFound = true
				}
				if value, ok := getFloatMetadata(metadata, metaKeyPeakLevel); ok {
					peakLevel = value
				}
				if value, ok := getFloatMetadata(metadata, metaKeyEntropy); ok {
					entropy = value
				}
			}

			framesProcessed++
			ffmpeg.AVFrameUnref(filteredFrame)
		}
	}

	// Flush encoder
	if wavEncoder != nil && wavWriteError == nil {
		if err := wavEncoder.Flush(); err != nil {
			wavWriteError = err
		}
	}

	if framesProcessed == 0 {
		return nil, nil // No frames in silence region
	}

	// Calculate crest factor from peak and RMS (both in dB)
	// Crest factor (dB) = Peak_level - RMS_level
	// This is more reliable than FFmpeg's linear Crest_factor output for very quiet signals
	crestFactorDB := 0.0
	if noiseFloorFound && peakLevel != 0 {
		crestFactorDB = peakLevel - measuredNoiseFloor
	}

	// Build noise profile result
	// FilePath is set only if WAV was successfully written
	profile := &NoiseProfile{
		Start:       region.Start,
		Duration:    region.Duration,
		PeakLevel:   peakLevel,
		CrestFactor: crestFactorDB, // Stored in dB (peak - RMS)
		Entropy:     entropy,       // 1.0 = broadband noise, lower = tonal noise
	}

	// Set FilePath only if WAV was successfully written
	if wavEncoder != nil && wavWriteError == nil {
		profile.FilePath = outputPath
	}

	// Record warning if using short silence region
	if region.Duration < idealSilenceDuration {
		profile.ExtractionWarning = fmt.Sprintf("using short silence region (%.1fs) - ideally need >=10s", region.Duration.Seconds())
	}

	if noiseFloorFound {
		profile.MeasuredNoiseFloor = measuredNoiseFloor
	} else {
		// Fallback: use overall noise floor estimate
		profile.MeasuredNoiseFloor = -60.0 // Conservative estimate
		if profile.ExtractionWarning != "" {
			profile.ExtractionWarning += "; noise floor estimated"
		} else {
			profile.ExtractionWarning = "noise floor estimated (measurement failed)"
		}
	}

	return profile, nil
}
