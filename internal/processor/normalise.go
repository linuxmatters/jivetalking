// Package processor handles audio analysis and processing
package processor

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// LoudnormStats contains the JSON output from the loudnorm filter.
// This is used to diagnose whether loudnorm is using linear or dynamic mode.
type LoudnormStats struct {
	InputI            string `json:"input_i"`
	InputTP           string `json:"input_tp"`
	InputLRA          string `json:"input_lra"`
	InputThresh       string `json:"input_thresh"`
	OutputI           string `json:"output_i"`
	OutputTP          string `json:"output_tp"`
	OutputLRA         string `json:"output_lra"`
	OutputThresh      string `json:"output_thresh"`
	NormalizationType string `json:"normalization_type"`
	TargetOffset      string `json:"target_offset"`
}

// loudnormLogCapture manages thread-safe capture of loudnorm's JSON output.
var loudnormLogCapture = struct {
	mu           sync.Mutex
	buffer       strings.Builder
	capturing    bool
	prevLogLevel int
}{}

// loudnormLogCallback captures loudnorm JSON output from FFmpeg's logging system.
// loudnorm outputs JSON at AV_LOG_INFO level when print_format=json is set.
func loudnormLogCallback(_ *ffmpeg.LogCtx, _ int, msg string) {
	loudnormLogCapture.mu.Lock()
	defer loudnormLogCapture.mu.Unlock()

	if loudnormLogCapture.capturing {
		loudnormLogCapture.buffer.WriteString(msg)
	}
}

// startLoudnormCapture begins capturing loudnorm log output.
// Must be called before processing frames through the loudnorm filter.
// Temporarily raises log level to INFO since loudnorm outputs JSON at that level.
func startLoudnormCapture() {
	loudnormLogCapture.mu.Lock()
	defer loudnormLogCapture.mu.Unlock()

	loudnormLogCapture.buffer.Reset()
	loudnormLogCapture.capturing = true
	loudnormLogCapture.prevLogLevel, _ = ffmpeg.AVLogGetLevel()
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogInfo) // loudnorm outputs JSON at INFO level
	ffmpeg.AVLogSetCallback(loudnormLogCallback)
}

// stopLoudnormCapture ends capture, restores default logging, and parses the JSON.
// Returns the parsed LoudnormStats or an error if JSON was not found/parseable.
func stopLoudnormCapture() (*LoudnormStats, error) {
	loudnormLogCapture.mu.Lock()
	defer loudnormLogCapture.mu.Unlock()

	loudnormLogCapture.capturing = false
	ffmpeg.AVLogSetCallback(nil)                          // Restore default logging
	ffmpeg.AVLogSetLevel(loudnormLogCapture.prevLogLevel) // Restore previous log level

	// Extract JSON from captured log output
	output := loudnormLogCapture.buffer.String()

	// Find JSON object in output (loudnorm outputs {...})
	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON found in loudnorm output (captured %d bytes)", len(output))
	}

	jsonStr := output[start : end+1]

	var stats LoudnormStats
	if err := json.Unmarshal([]byte(jsonStr), &stats); err != nil {
		return nil, fmt.Errorf("failed to parse loudnorm JSON: %w", err)
	}

	return &stats, nil
}

// LoudnormMeasurement holds the results from loudnorm's first pass (measurement mode).
// This is populated by measureWithLoudnorm() which reads the Pass 2 output file
// and runs loudnorm without encoding to get the measurements needed for second pass.
type LoudnormMeasurement struct {
	InputI       float64 // Loudnorm's measured integrated loudness (LUFS)
	InputTP      float64 // Loudnorm's measured true peak (dBTP)
	InputLRA     float64 // Loudnorm's measured loudness range (LU)
	InputThresh  float64 // Loudnorm's measured threshold (LUFS)
	TargetOffset float64 // Loudnorm's calculated offset for second pass
}

// measureWithLoudnorm performs loudnorm's first pass (measurement mode) on the audio file.
// Reads the file through loudnorm without encoding output to get measurements needed
// for the second pass (application mode with linear=true).
//
// This is a separate pass because loudnorm has no "measure only" mode - it always
// processes audio. Running it in the Pass 2 filter chain would cause double-normalisation.
// Instead, we read the Pass 2 output file here without writing, just to get measurements.
//
// Parameters:
//   - inputPath: Path to Pass 2 output file (the -processed.flac file)
//   - config: Filter configuration (contains loudnorm targets)
//   - progressCallback: Optional progress updates (pass 3)
//
// Returns:
//   - measurement: Loudnorm measurements for second pass
//   - err: Error if measurement failed
func measureWithLoudnorm(inputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements)) (*LoudnormMeasurement, error) {
	// Start capturing loudnorm log output
	startLoudnormCapture()

	// Helper to stop capture and parse stats
	getLoudnormStats := func() (*LoudnormStats, error) {
		return stopLoudnormCapture()
	}

	// Open input file
	reader, metadata, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		getLoudnormStats() // Clean up capture
		return nil, fmt.Errorf("failed to open input: %w", err)
	}
	defer reader.Close()

	// Calculate total samples for progress reporting
	totalSamples := int64(metadata.Duration * float64(metadata.SampleRate))
	var samplesProcessed int64
	var frameCount int
	const progressUpdateInterval = 100 // Send progress update every N frames

	// Build measurement filter: loudnorm (without linear=true) + null sink
	// loudnorm in single-pass mode outputs its measurements to JSON when freed
	// We use print_format=json to get input_i, input_tp, input_lra, input_thresh, target_offset
	filterSpec := fmt.Sprintf(
		"loudnorm=I=%.1f:TP=%.1f:LRA=%.1f:dual_mono=%s:print_format=json",
		config.LoudnormTargetI,
		config.LoudnormTargetTP,
		config.LoudnormTargetLRA,
		boolToString(config.LoudnormDualMono),
	)

	// Create filter graph
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(
		reader.GetDecoderContext(),
		filterSpec,
	)
	if err != nil {
		getLoudnormStats() // Clean up capture
		return nil, fmt.Errorf("failed to create filter graph: %w", err)
	}
	// Note: We free the filter graph explicitly to trigger loudnorm JSON output

	// Process all frames through loudnorm (no encoding - just measurement)
	for {
		frame, err := reader.ReadFrame()
		if err != nil || frame == nil {
			break
		}

		// Track samples for progress
		samplesProcessed += int64(frame.NbSamples())

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			continue
		}

		// Pull filtered frames (discard - we only want the measurements)
		filteredFrame := ffmpeg.AVFrameAlloc()
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				break
			}
			ffmpeg.AVFrameUnref(filteredFrame)
		}
		ffmpeg.AVFrameFree(&filteredFrame)

		// Progress update periodically (every N frames for smooth updates)
		frameCount++
		if progressCallback != nil && frameCount%progressUpdateInterval == 0 {
			progress := math.Min(0.99, float64(samplesProcessed)/float64(totalSamples))
			progressCallback(3, "Measuring", progress, 0.0, nil)
		}
	}

	// Flush filter graph
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err == nil {
		filteredFrame := ffmpeg.AVFrameAlloc()
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				break
			}
			ffmpeg.AVFrameUnref(filteredFrame)
		}
		ffmpeg.AVFrameFree(&filteredFrame)
	}

	// Free filter graph to trigger loudnorm JSON output
	ffmpeg.AVFilterGraphFree(&filterGraph)

	// Capture loudnorm stats from log output
	stats, err := getLoudnormStats()
	if err != nil {
		return nil, fmt.Errorf("failed to capture loudnorm measurements: %w", err)
	}

	// Parse string values to measurement struct
	measurement := &LoudnormMeasurement{}
	fmt.Sscanf(stats.InputI, "%f", &measurement.InputI)
	fmt.Sscanf(stats.InputTP, "%f", &measurement.InputTP)
	fmt.Sscanf(stats.InputLRA, "%f", &measurement.InputLRA)
	fmt.Sscanf(stats.InputThresh, "%f", &measurement.InputThresh)
	fmt.Sscanf(stats.TargetOffset, "%f", &measurement.TargetOffset)

	return measurement, nil
}

// calculateLimiterCeiling calculates the adaptive ceiling for pre-limiting in Pass 4.
// This allows loudnorm to apply full linear gain without exceeding target TP.
//
// The ceiling is calculated so that after loudnorm applies its gain:
//
//	projected_TP = ceiling + gainRequired <= target_TP
//
// Solving for ceiling: ceiling = target_TP - gainRequired - safety_margin
//
// FFmpeg alimiter constraint: the limit parameter accepts 0.0625 to 1.0 (linear),
// which corresponds to -24.08 dBTP to 0 dBTP. Ceilings below this are clamped.
//
// Parameters:
//   - measured_I: Measured integrated loudness from Pass 3 (LUFS)
//   - measured_TP: Measured true peak from Pass 3 (dBTP)
//   - target_I: Target integrated loudness (LUFS), typically -16.0
//   - target_TP: Target true peak (dBTP), typically -2.0
//
// Returns:
//   - ceiling: The limiter ceiling in dBTP (clamped to minLimiterCeilingDB if needed)
//   - needed: True if limiting is required (projected TP exceeds target)
//   - clamped: True if ceiling was clamped to minimum (loudnorm may need to adjust target)
func calculateLimiterCeiling(measured_I, measured_TP, target_I, target_TP float64) (ceiling float64, needed bool, clamped bool) {
	// Safety margin accounts for inter-sample peak (ISP) creation during limiting.
	// FFmpeg's alimiter operates on sample peaks, not true peaks. When the limiter
	// shapes waveforms to reduce peaks, the resulting waveform can have ISPs that
	// exceed the sample peak ceiling. These ISPs are then amplified by loudnorm's gain.
	//
	// Observed ISP creation varies by source material:
	// - Most files: 0.1-0.5 dB ISP after limiting+gain
	// - Worst case (E70-Martin): 1.6 dB ISP after limiting+gain
	//
	// Using 2.0 dB margin ensures broadcast compliance (-2.0 dBTP) even for
	// worst-case ISP creation. This is conservative but guarantees compliance.
	const safetyMargin = 2.0 // dB - accounts for ISP creation during limiting

	// FFmpeg alimiter minimum: limit=0.0625 = 20*log10(0.0625) ≈ -24.08 dBTP
	// Use -24.0 dBTP as practical minimum with small safety buffer
	const minLimiterCeilingDB = -24.0

	gainRequired := target_I - measured_I
	projectedTP := measured_TP + gainRequired

	// No limiting needed if linear mode already possible
	if projectedTP <= target_TP {
		return 0, false, false
	}

	// Calculate ceiling: target_TP - gainRequired - safetyMargin
	ceiling = target_TP - gainRequired - safetyMargin

	// Clamp to alimiter's minimum supported ceiling
	if ceiling < minLimiterCeilingDB {
		ceiling = minLimiterCeilingDB
		clamped = true
	}

	return ceiling, true, clamped
}

// calculateLinearModeTarget calculates the target I and offset that ensure loudnorm
// stays in linear mode (never falls back to dynamic normalization).
//
// For linear mode, loudnorm requires: measured_TP + (target_I - measured_I) <= target_TP
// Rearranging: target_I <= target_TP - measured_TP + measured_I
//
// This function returns the effective target I (clamped to linear mode) and the
// corresponding offset. If the desired target can be achieved in linear mode,
// it returns the original target. Otherwise, it returns the maximum achievable
// target that still allows linear mode.
//
// Parameters:
//   - measured_I: Measured integrated loudness (LUFS)
//   - measured_TP: Measured true peak (dBTP)
//   - desired_I: Desired target integrated loudness (LUFS), typically -16.0
//   - target_TP: Target true peak (dBTP), typically -1.5
//
// Returns:
//   - effectiveTargetI: The target I to use (may be lower than desired to ensure linear mode)
//   - offset: The gain offset to apply (effectiveTargetI - measured_I)
//   - linearPossible: True if the desired target can be achieved in linear mode
func calculateLinearModeTarget(measured_I, measured_TP, desired_I, target_TP float64) (effectiveTargetI, offset float64, linearPossible bool) {
	// Calculate the maximum target I that allows linear mode
	// Formula: measured_TP + (target_I - measured_I) <= target_TP
	// Solving for target_I: target_I <= target_TP - measured_TP + measured_I
	//
	// We subtract a small safety margin (0.1 dB) to account for:
	// - Floating point precision differences between Go and FFmpeg's internal calculations
	// - Potential rounding in filter parameter passing
	// - Any measurement variance during processing
	const safetyMargin = 0.1 // dB - ensures we stay safely within linear mode bounds
	maxLinearTargetI := target_TP - measured_TP + measured_I - safetyMargin

	// Check if desired target is achievable in linear mode (with safety margin)
	if desired_I <= maxLinearTargetI {
		// Desired target is achievable - use it directly
		return desired_I, desired_I - measured_I, true
	}

	// Desired target would require dynamic mode - clamp to linear-safe maximum
	return maxLinearTargetI, maxLinearTargetI - measured_I, false
}

// NormalisationResult contains the outcome of the normalisation pass.
type NormalisationResult struct {
	InputLUFS        float64        // Pre-normalisation loudness (from Pass 2 loudnorm measurement)
	InputTP          float64        // Pre-normalisation true peak (from Pass 2 loudnorm measurement)
	OutputLUFS       float64        // Post-normalisation loudness (measured)
	OutputTP         float64        // Post-normalisation true peak (measured)
	GainApplied      float64        // Gain adjustment applied (dB) - loudnorm's target_offset
	WithinTarget     bool           // True if final output is within tolerance of target
	Skipped          bool           // True if normalisation was skipped (already within tolerance)
	LoudnormStats    *LoudnormStats // Diagnostic output from loudnorm second pass (nil if capture failed)
	RequestedTargetI float64        // The target I that was requested (from config)
	EffectiveTargetI float64        // The target I actually used (may be lower to ensure linear mode)
	LinearModeForced bool           // True if target was adjusted to force linear mode

	// Limiter diagnostics (Pass 4 pre-limiting)
	LimiterEnabled bool    // True if pre-limiting was applied
	LimiterCeiling float64 // Ceiling in dBTP (only valid if LimiterEnabled)
	LimiterGain    float64 // Gain required that triggered limiting (dB)

	// FinalMeasurements contains full analysis after normalisation (Pass 4)
	// Includes spectral characteristics, amplitude stats, and loudness measurements
	// for comparison with Pass 1 input and Pass 2 filtered measurements
	FinalMeasurements *OutputMeasurements
}

// ApplyNormalisation performs Pass 3: EBU R128 dynamic loudness normalisation.
// Uses FFmpeg's loudnorm filter in two-pass mode.
//
// Workflow:
// 1. Pass 3a: Run loudnorm measurement pass on Pass 2 output (measureWithLoudnorm)
// 2. Pass 3b: Apply loudnorm with linear=true using those measurements
//
// This uses loudnorm's own target_offset from the measurement pass, not one we
// calculate ourselves from ebur128 measurements (per ffmpeg-loudnorm-helper).
//
// Unlike simple linear gain, loudnorm:
// - Applies adaptive gain (more to quiet sections, less to loud sections)
// - Includes 100ms lookahead true peak limiter (upsamples to 192kHz internally)
// - Prevents noise floor from being elevated into audibility
// - Preserves natural dynamics while hitting target loudness
//
// Parameters:
//   - inputPath: Path to Pass 2 output file (the -processed.flac file)
//   - config: Filter configuration (contains loudnorm targets)
//   - outputMeasurements: Pass 2 measurements (for reference, not used for loudnorm)
//   - progressCallback: Optional progress updates
//
// Returns:
//   - result: Normalisation outcome with before/after measurements
//   - err: Error if normalisation failed
func ApplyNormalisation(
	inputPath string,
	config *FilterChainConfig,
	outputMeasurements *OutputMeasurements,
	progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements),
) (*NormalisationResult, error) {
	if !config.LoudnormEnabled {
		return &NormalisationResult{Skipped: true}, nil
	}

	// Signal pass start - first we measure, then we apply
	if progressCallback != nil {
		progressCallback(3, "Measuring", 0.0, 0.0, nil)
	}

	// Pass 3: Run loudnorm measurement pass on Pass 2 output
	// This reads the file through loudnorm without encoding to get measurements
	measurement, err := measureWithLoudnorm(inputPath, config, progressCallback)
	if err != nil {
		return nil, fmt.Errorf("loudnorm measurement pass failed: %w", err)
	}

	// Validate measurements are usable
	if math.IsInf(measurement.InputI, -1) || measurement.InputI < -70.0 {
		return nil, fmt.Errorf("cannot normalise silent audio (measured %.1f LUFS)", measurement.InputI)
	}

	// Signal measurement complete, starting application
	if progressCallback != nil {
		progressCallback(3, "Measuring", 1.0, 0.0, nil)
		progressCallback(4, "Normalising", 0.0, 0.0, nil)
	}

	// Calculate limiter ceiling (actual limiting happens in buildLoudnormFilterSpec)
	// clamped=true means ceiling was limited to alimiter's minimum (-24 dBTP),
	// so loudnorm may still need to adjust target for very quiet audio
	limiterCeiling, limiterNeeded, limiterClamped := calculateLimiterCeiling(
		measurement.InputI,
		measurement.InputTP,
		config.LoudnormTargetI,
		config.LoudnormTargetTP,
	)
	limiterGain := config.LoudnormTargetI - measurement.InputI

	// Calculate effective target I that ensures linear mode (no dynamic fallback)
	// loudnorm requires: measured_TP + (target_I - measured_I) <= target_TP for linear mode
	//
	// IMPORTANT: When the limiter is enabled, loudnorm sees the LIMITED peaks (limiterCeiling),
	// not the original measured peaks. This creates the headroom needed for full gain.
	// EXCEPTION: When clamped, the ceiling isn't low enough for full gain, so use original TP
	// to let calculateLinearModeTarget adjust the target appropriately.
	effectiveTP := measurement.InputTP
	if limiterNeeded && !limiterClamped {
		effectiveTP = limiterCeiling
	}
	effectiveTargetI, _, linearPossible := calculateLinearModeTarget(
		measurement.InputI,
		effectiveTP,
		config.LoudnormTargetI,
		config.LoudnormTargetTP,
	)

	// Use loudnorm's own target_offset from the measurement pass
	offset := measurement.TargetOffset

	// Store the effective target in config for loudnorm filter construction
	effectiveConfig := *config
	effectiveConfig.LoudnormTargetI = effectiveTargetI

	// Pass 4: Apply loudnorm with linear=true and the measurements
	finalLUFS, finalTP, finalMeasurements, loudnormStats, err := applyLoudnormAndMeasure(inputPath, &effectiveConfig, measurement, progressCallback)
	if err != nil {
		return nil, fmt.Errorf("loudnorm application failed: %w", err)
	}

	// Signal pass complete
	if progressCallback != nil {
		progressCallback(4, "Normalising", 1.0, 0.0, nil)
	}

	// Validate result is within tolerance of the EFFECTIVE target (not the requested one)
	finalDeviation := math.Abs(finalLUFS - effectiveTargetI)
	withinTarget := finalDeviation <= 0.5

	return &NormalisationResult{
		InputLUFS:         measurement.InputI,
		InputTP:           measurement.InputTP,
		OutputLUFS:        finalLUFS,
		OutputTP:          finalTP,
		GainApplied:       offset,
		WithinTarget:      withinTarget,
		Skipped:           false,
		LoudnormStats:     loudnormStats,
		RequestedTargetI:  config.LoudnormTargetI,
		EffectiveTargetI:  effectiveTargetI,
		LinearModeForced:  !linearPossible,
		LimiterEnabled:    limiterNeeded,
		LimiterCeiling:    limiterCeiling,
		LimiterGain:       limiterGain,
		FinalMeasurements: finalMeasurements,
	}, nil
}

// applyLoudnormAndMeasure applies loudnorm's second pass to the audio file and measures the result.
// Uses in-place processing: reads input, applies loudnorm, writes to temp file, renames.
//
// Filter chain: loudnorm → astats → aspectralstats → ebur128 → resample
//
// This is the second pass of loudnorm's two-pass workflow. The first pass
// measurements come from measureWithLoudnorm() (stored in LoudnormMeasurement).
//
// Returns the measured integrated loudness, true peak, full output measurements, and loudnorm diagnostic stats.
func applyLoudnormAndMeasure(
	inputPath string,
	config *FilterChainConfig,
	measurement *LoudnormMeasurement,
	progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements),
) (float64, float64, *OutputMeasurements, *LoudnormStats, error) {
	// Start capturing loudnorm's JSON output for diagnostics
	startLoudnormCapture()

	// Helper to stop capture and return stats (may be nil if capture failed)
	getLoudnormStats := func() *LoudnormStats {
		stats, _ := stopLoudnormCapture()
		return stats
	}

	// Open input file
	reader, metadata, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		return 0.0, 0.0, nil, getLoudnormStats(), fmt.Errorf("failed to open input: %w", err)
	}
	defer reader.Close()

	// Create temporary output file with proper audio extension
	// Extract extension from input file and use it for temp file
	ext := filepath.Ext(inputPath)
	if ext == "" {
		ext = ".flac" // Default to FLAC if no extension
	}
	tempPath := strings.TrimSuffix(inputPath, ext) + ".loudnorm.tmp" + ext

	// Build Pass 3 filter graph: loudnorm (second pass with linear=true) → ebur128 (validation)
	filterSpec := buildLoudnormFilterSpec(config, measurement)

	// Create filter graph
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := createLoudnormFilterGraph(
		reader.GetDecoderContext(),
		filterSpec,
	)
	if err != nil {
		return 0.0, 0.0, nil, getLoudnormStats(), fmt.Errorf("failed to create filter graph: %w", err)
	}
	// Note: We free the filter graph explicitly before getting stats, not via defer.
	// loudnorm outputs its JSON when the filter graph is freed.

	// Create output encoder (same format as input)
	encoder, err := createOutputEncoder(tempPath, metadata, bufferSinkCtx)
	if err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return 0.0, 0.0, nil, getLoudnormStats(), fmt.Errorf("failed to create encoder: %w", err)
	}
	defer encoder.Close()

	// Process frames and accumulate ebur128 measurements using Pass 2's extraction function
	var acc outputMetadataAccumulators
	var framesProcessed int64

	// Calculate total samples for accurate progress reporting
	totalSamples := int64(metadata.Duration * float64(metadata.SampleRate))
	var samplesProcessed int64
	const progressUpdateInterval = 100 // Send progress update every N frames

	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			break
		}
		if frame == nil {
			break
		}

		// Track samples for progress
		samplesProcessed += int64(frame.NbSamples())

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			continue
		}

		// Pull filtered frames
		filteredFrame := ffmpeg.AVFrameAlloc()
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				ffmpeg.AVFrameUnref(filteredFrame)
				continue
			}

			// Extract validation measurements using Pass 2's function
			extractOutputFrameMetadata(filteredFrame.Metadata(), &acc)

			// Encode frame
			if err := encoder.WriteFrame(filteredFrame); err != nil {
				ffmpeg.AVFrameUnref(filteredFrame)
				ffmpeg.AVFrameFree(&filteredFrame)
				ffmpeg.AVFilterGraphFree(&filterGraph)
				return 0.0, 0.0, nil, getLoudnormStats(), fmt.Errorf("encoding failed: %w", err)
			}

			framesProcessed++
			ffmpeg.AVFrameUnref(filteredFrame)
		}
		ffmpeg.AVFrameFree(&filteredFrame)

		// Progress update periodically (every N input frames for smooth updates)
		if progressCallback != nil && framesProcessed%progressUpdateInterval == 0 {
			progress := math.Min(0.99, float64(samplesProcessed)/float64(totalSamples))
			progressCallback(4, "Normalising", progress, acc.ebur128OutputI, nil)
		}
	}

	// Flush filter graph
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err == nil {
		filteredFrame := ffmpeg.AVFrameAlloc()
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				break
			}

			// Extract final measurements using Pass 2's function
			extractOutputFrameMetadata(filteredFrame.Metadata(), &acc)

			if err := encoder.WriteFrame(filteredFrame); err != nil {
				ffmpeg.AVFrameUnref(filteredFrame)
				ffmpeg.AVFrameFree(&filteredFrame)
				ffmpeg.AVFilterGraphFree(&filterGraph)
				return 0.0, 0.0, nil, getLoudnormStats(), fmt.Errorf("encoding failed during flush: %w", err)
			}

			ffmpeg.AVFrameUnref(filteredFrame)
		}
		ffmpeg.AVFrameFree(&filteredFrame)
	}

	// Flush encoder
	if err := encoder.Flush(); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return 0.0, 0.0, nil, getLoudnormStats(), fmt.Errorf("failed to flush encoder: %w", err)
	}

	// Close encoder before rename
	if err := encoder.Close(); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return 0.0, 0.0, nil, getLoudnormStats(), fmt.Errorf("failed to close encoder: %w", err)
	}

	// Atomic rename: temp file → original file (in-place update)
	if err := os.Rename(tempPath, inputPath); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return 0.0, 0.0, nil, getLoudnormStats(), fmt.Errorf("failed to rename output: %w", err)
	}

	// Free filter graph before getting stats — loudnorm outputs JSON on graph destruction
	ffmpeg.AVFilterGraphFree(&filterGraph)

	// Capture loudnorm stats (JSON output captured during filter graph free)
	stats := getLoudnormStats()

	// Build complete OutputMeasurements from accumulators
	finalMeasurements := finalizeOutputMeasurements(&acc)

	return acc.ebur128OutputI, acc.ebur128OutputTP, finalMeasurements, stats, nil
}

// buildLoudnormFilterSpec constructs the filter chain for Pass 4 loudnorm application.
//
// Chain order: [alimiter] → loudnorm → astats → aspectralstats → ebur128 → resample
//
// The alimiter is inserted when needed to create headroom for loudnorm's linear mode.
// It uses 1176-inspired parameters for transparent peak limiting.
//
// The loudnorm filter in second pass mode:
// - Uses measurements from measureWithLoudnorm() (LoudnormMeasurement)
// - Applies linear gain when possible (more transparent, no adaptive EQ)
// - Includes 100ms lookahead true peak limiter (upsamples to 192kHz internally)
//
// astats and aspectralstats are placed before ebur128 because ebur128 upsamples to
// 192kHz and outputs f64. We want spectral measurements at the original sample rate
// to match Pass 2's measurements for accurate comparison.
//
// Key parameters:
// - I/TP/LRA: Target values (from config)
// - measured_I/TP/LRA/thresh: Measurements from loudnorm first pass
// - offset: Target offset from first pass (critical for linear mode)
// - dual_mono: CRITICAL for mono files (corrects -3 LU measurement error)
// - linear: Enable linear mode (applies consistent gain, no adaptive EQ)
//
// Per ffmpeg-loudnorm-helper: the offset parameter MUST come from loudnorm's own
// first pass measurement, not from external calculations.
func buildLoudnormFilterSpec(config *FilterChainConfig, measurement *LoudnormMeasurement) string {
	var filters []string

	// 1. Pre-limiting with adaptive ceiling (1176-inspired peak limiter)
	// Calculate if limiting is needed to allow loudnorm's full linear gain
	ceiling, needsLimiting, _ := calculateLimiterCeiling(
		measurement.InputI,
		measurement.InputTP,
		config.LoudnormTargetI,
		config.LoudnormTargetTP,
	)
	if needsLimiting {
		// 1176-inspired parameters for transparent peak limiting:
		// - attack=0.1ms: Fast attack catches all peaks (1176's "firm lid")
		// - release=50ms: Quick recovery avoids pumping on speech
		// - asc=1: Auto Soft Clipping approximates 1176's two-stage release
		// - asc_level=0.5: Moderate smoothing for speech
		// - level_in/level_out=1: Unity gain (no makeup)
		// - latency=1: Enable lookahead for better transient handling
		limiterCeilingLinear := math.Pow(10, ceiling/20.0)
		limiterFilter := fmt.Sprintf(
			"alimiter=limit=%.6f:attack=0.1:release=50:level_in=1:level_out=1:level=0:latency=1:asc=1:asc_level=0.5",
			limiterCeilingLinear,
		)
		filters = append(filters, limiterFilter)
	}

	// 2. loudnorm (second pass mode)
	// measured_i/tp/lra/thresh come from loudnorm's first pass measurement
	// offset: loudnorm's own calculated offset from first pass (critical!)
	// linear=true: Enable linear mode (applies consistent gain, no adaptive EQ)
	// dual_mono=true: CRITICAL - treats mono as dual-mono for correct loudness measurement
	// print_format=json: Outputs JSON with normalization_type, target_offset, output_i/tp/lra
	//
	// IMPORTANT: When pre-limiting is enabled, we pass the limiter ceiling as measured_TP
	// so loudnorm knows the actual peak level it will receive. This allows it to apply
	// full linear gain without falling back to dynamic mode.
	effectiveMeasuredTP := measurement.InputTP
	if needsLimiting {
		effectiveMeasuredTP = ceiling
	}
	loudnormFilter := fmt.Sprintf(
		"loudnorm=I=%.2f:TP=%.2f:LRA=%.1f:measured_I=%.2f:measured_TP=%.2f:measured_LRA=%.2f:measured_thresh=%.2f:offset=%.2f:dual_mono=%s:linear=%s:print_format=json",
		config.LoudnormTargetI,  // Using %.2f for precision on adjusted targets
		config.LoudnormTargetTP, // Also %.2f for consistency
		config.LoudnormTargetLRA,
		measurement.InputI,
		effectiveMeasuredTP,
		measurement.InputLRA,
		measurement.InputThresh,
		measurement.TargetOffset, // From first pass - critical for linear mode
		boolToString(config.LoudnormDualMono),
		boolToString(config.LoudnormLinear),
	)
	filters = append(filters, loudnormFilter)

	// 3. astats for amplitude measurements (same as Pass 2)
	// Provides noise floor, dynamic range, RMS level, peak level, etc.
	// measure_perchannel=all requests all available per-channel statistics
	filters = append(filters, "astats=metadata=1:measure_perchannel=all")

	// 4. aspectralstats for spectral analysis (same as Pass 2)
	// Provides centroid, spread, skewness, kurtosis, entropy, flatness, crest, rolloff, etc.
	// win_size=2048 and win_func=hann match Pass 2 settings for comparable measurements
	filters = append(filters, "aspectralstats=win_size=2048:win_func=hann:measure=all")

	// 5. ebur128 for loudness validation (metadata only, no audio modification)
	// dualmono=true ensures accurate mono loudness measurement
	// Note: ebur128 upsamples to 192kHz internally and outputs f64
	filters = append(filters, "ebur128=metadata=1:peak=sample+true:dualmono=true")

	// 6. Resample back to output format (44.1kHz/s16/mono)
	// Required because ebur128 outputs f64 at 192kHz; encoder expects s16 at 44.1kHz
	// Temporarily enable resample to get the filter spec, then restore
	wasEnabled := config.ResampleEnabled
	config.ResampleEnabled = true
	if resampleSpec := config.buildResampleFilter(); resampleSpec != "" {
		filters = append(filters, resampleSpec)
	}
	config.ResampleEnabled = wasEnabled

	return strings.Join(filters, ",")
}

// boolToString converts bool to loudnorm's expected string format
func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// createLoudnormFilterGraph creates a filter graph for loudnorm normalisation.
// Reuses the existing setupFilterGraph function from filters.go for consistency.
func createLoudnormFilterGraph(decoderCtx *ffmpeg.AVCodecContext, filterSpec string) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
	// Use existing setupFilterGraph helper (defined in filters.go)
	return setupFilterGraph(decoderCtx, filterSpec)
}
