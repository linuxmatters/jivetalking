// Package processor handles audio analysis and processing
package processor

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// Limiter ceiling constants used by calculateLimiterCeiling and pre-gain deficit
// calculation. These are the canonical values; adaptive.go defines
// la2aHighCrestSafetyMargin and la2aHighCrestMinLimiterCeiling with identical
// values for the high-crest override path - keep them in sync.
const (
	// safetyMarginDB accounts for inter-sample peak (ISP) creation during limiting.
	// See calculateLimiterCeiling for detailed rationale.
	safetyMarginDB = 1.5 // dB

	// minLimiterCeilingDB is the practical minimum for FFmpeg's alimiter.
	// limit=0.0625 = 20*log10(0.0625) ≈ -24.08 dBTP; we use -24.0 with a small buffer.
	minLimiterCeilingDB = -24.0 // dBTP
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
//   - inputPath: Path to Pass 2 output file (the -processed file, before LUFS rename)
//   - config: Filter configuration (contains loudnorm targets)
//   - filterPrefix: Optional filter chain to prepend before loudnorm (e.g. volume+alimiter);
//     empty string preserves existing behaviour
//   - progressCallback: Optional progress updates (pass 3)
//
// Returns:
//   - measurement: Loudnorm measurements for second pass
//   - err: Error if measurement failed
func measureWithLoudnorm(inputPath string, config *FilterChainConfig, filterPrefix string, progressCallback func(pass PassNumber, passName string, progress float64, level float64, measurements *AudioMeasurements)) (*LoudnormMeasurement, error) {
	// Start capturing loudnorm log output
	startLoudnormCapture()

	// Open input file
	reader, metadata, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		_, _ = stopLoudnormCapture() // Clean up capture
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

	if filterPrefix != "" {
		filterSpec = filterPrefix + "," + filterSpec
	}

	// Create filter graph
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(
		reader.GetDecoderContext(),
		filterSpec,
	)
	if err != nil {
		_, _ = stopLoudnormCapture() // Clean up capture
		return nil, fmt.Errorf("failed to create filter graph: %w", err)
	}
	// Note: We free the filter graph explicitly to trigger loudnorm JSON output

	// Process all frames through loudnorm (no encoding - just measurement)
	lenientHandler := func(err error) error { return nil }
	_ = runFilterGraph(reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnPushError: lenientHandler,
		OnPullError: lenientHandler,
		OnInputFrame: func(inputFrame *ffmpeg.AVFrame) {
			samplesProcessed += int64(inputFrame.NbSamples())
			frameCount++
			if progressCallback != nil && frameCount%progressUpdateInterval == 0 {
				progress := math.Min(0.99, float64(samplesProcessed)/float64(totalSamples))
				progressCallback(PassMeasuring, "Measuring", progress, 0.0, nil)
			}
		},
	})

	// Free filter graph to trigger loudnorm JSON output
	ffmpeg.AVFilterGraphFree(&filterGraph)

	// Capture loudnorm stats from log output
	stats, err := stopLoudnormCapture()
	if err != nil {
		return nil, fmt.Errorf("failed to capture loudnorm measurements: %w", err)
	}

	// Parse string values to measurement struct
	parseFloat := func(name, value string) (float64, error) {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 0, fmt.Errorf("invalid loudnorm %s value %q: %w", name, value, err)
		}
		return parsed, nil
	}

	measurement := &LoudnormMeasurement{}
	if measurement.InputI, err = parseFloat("input_i", stats.InputI); err != nil {
		return nil, err
	}
	if measurement.InputTP, err = parseFloat("input_tp", stats.InputTP); err != nil {
		return nil, err
	}
	if measurement.InputLRA, err = parseFloat("input_lra", stats.InputLRA); err != nil {
		return nil, err
	}
	if measurement.InputThresh, err = parseFloat("input_thresh", stats.InputThresh); err != nil {
		return nil, err
	}
	if measurement.TargetOffset, err = parseFloat("target_offset", stats.TargetOffset); err != nil {
		return nil, err
	}

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
func calculateLimiterCeiling(measuredI, measuredTP, targetI, targetTP float64) (ceiling float64, needed bool, clamped bool) {
	gainRequired := targetI - measuredI
	projectedTP := measuredTP + gainRequired

	// No limiting needed if linear mode already possible
	if projectedTP <= targetTP {
		return 0, false, false
	}

	// Calculate ceiling: targetTP - gainRequired - safetyMarginDB
	ceiling = targetTP - gainRequired - safetyMarginDB

	// Clamp to alimiter's minimum supported ceiling
	if ceiling < minLimiterCeilingDB {
		ceiling = minLimiterCeilingDB
		clamped = true
	}

	return ceiling, true, clamped
}

// calculatePreGain computes the pre-gain amount needed when the limiter ceiling is
// clamped to alimiter's minimum. The deficit (preGainDB) raises the signal before
// limiting so that loudnorm can apply full linear gain. When the ceiling is not
// clamped, returns (0.0, 0.0).
//
// Parameters:
//   - measuredI: Measured integrated loudness (LUFS)
//   - targetI: Target integrated loudness (LUFS), typically -16.0
//   - targetTP: Target true peak (dBTP), typically -2.0
//
// Returns:
//   - preGainDB: The pre-gain amount in dB (positive when clamped, 0.0 otherwise)
//   - reDerivedCeiling: The limiter ceiling re-derived from post-gain values (0.0 when not clamped)
func calculatePreGain(measuredI, targetI, targetTP float64) (preGainDB, reDerivedCeiling float64) {
	gainRequired := targetI - measuredI
	idealCeiling := targetTP - gainRequired - safetyMarginDB

	// No pre-gain needed if ceiling is at or above alimiter's minimum
	if idealCeiling >= minLimiterCeilingDB {
		return 0.0, 0.0
	}

	// Deficit: how far below the minimum the ideal ceiling sits
	preGainDB = minLimiterCeilingDB - idealCeiling

	// Re-derive ceiling from post-gain values
	postGainI := measuredI + preGainDB
	newGainRequired := targetI - postGainI
	reDerivedCeiling = targetTP - newGainRequired - safetyMarginDB

	return preGainDB, reDerivedCeiling
}

// buildPreLimiterPrefix constructs the filter prefix for pre-limiting in Pass 3/4.
// Returns a comma-separated filter string fragment containing volume (when pre-gain
// is active) and alimiter (when limiting is needed), or "" when no limiting is needed.
//
// CBS Volumax-inspired parameters for transparent peak limiting:
//   - attack=5ms: Gentle attack preserves transient shape
//   - release=100ms: Smooth recovery eliminates pumping
//   - asc=1: Auto Soft Clipping for program-dependent release
//   - asc_level=0.8: Program-dependent smoothing (Volumax characteristic)
//   - level_in/level_out=1: Unity gain (no makeup)
//   - latency=1: Enable lookahead for better transient handling
//
// Parameters:
//   - preGainDB: Pre-gain amount in dB (positive when clamped, 0.0 otherwise)
//   - ceiling: Limiter ceiling in dBTP
//   - needsLimiting: True if limiting is required
//
// Returns the filter string fragment or "" when no limiting needed.
func buildPreLimiterPrefix(preGainDB, ceiling float64, needsLimiting bool) string {
	if !needsLimiting {
		return ""
	}

	var parts []string

	if preGainDB > 0 {
		parts = append(parts, fmt.Sprintf("volume=%.1fdB", preGainDB))
	}

	limiterCeilingLinear := math.Pow(10, ceiling/20.0)
	limiterFilter := fmt.Sprintf(
		"alimiter=limit=%.6f:attack=5:release=100:level_in=1:level_out=1:level=0:latency=1:asc=1:asc_level=0.8",
		limiterCeilingLinear,
	)
	parts = append(parts, limiterFilter)

	return strings.Join(parts, ",")
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
func calculateLinearModeTarget(measuredI, measuredTP, desiredI, targetTP float64) (effectiveTargetI, offset float64, linearPossible bool) {
	// Calculate the maximum target I that allows linear mode
	// Formula: measured_TP + (target_I - measured_I) <= target_TP
	// Solving for target_I: target_I <= target_TP - measured_TP + measured_I
	//
	// We subtract a small safety margin (0.1 dB) to account for:
	// - Floating point precision differences between Go and FFmpeg's internal calculations
	// - Potential rounding in filter parameter passing
	// - Any measurement variance during processing
	const linearSafetyMargin = 0.1 // dB - ensures we stay safely within linear mode bounds
	maxLinearTargetI := targetTP - measuredTP + measuredI - linearSafetyMargin

	// Check if desired target is achievable in linear mode (with safety margin)
	if desiredI <= maxLinearTargetI {
		// Desired target is achievable - use it directly
		return desiredI, desiredI - measuredI, true
	}

	// Desired target would require dynamic mode - clamp to linear-safe maximum
	return maxLinearTargetI, maxLinearTargetI - measuredI, false
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
	PreGainDB      float64 // Pre-gain amount in dB (0.0 when no pre-gain applied)
	LimiterClamped bool    // True when calculateLimiterCeiling clamped ceiling to minimum

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
//   - inputPath: Path to Pass 2 output file (the -processed file, before LUFS rename)
//   - config: Filter configuration (contains loudnorm targets)
//   - outputMeasurements: Pass 2 measurements (for reference, not used for loudnorm)
//   - inputMeasurements: Pass 1 measurements (contains NoiseProfile and SpeechProfile for region capture)
//   - progressCallback: Optional progress updates
//
// Returns:
//   - result: Normalisation outcome with before/after measurements
//   - err: Error if normalisation failed
func ApplyNormalisation(
	inputPath string,
	config *FilterChainConfig,
	outputMeasurements *OutputMeasurements,
	inputMeasurements *AudioMeasurements,
	progressCallback func(pass PassNumber, passName string, progress float64, level float64, measurements *AudioMeasurements),
) (*NormalisationResult, error) {
	if !config.LoudnormEnabled {
		return &NormalisationResult{Skipped: true}, nil
	}

	// Signal pass start - first we measure, then we apply
	if progressCallback != nil {
		progressCallback(PassMeasuring, "Measuring", 0.0, 0.0, nil)
	}

	// Compute ceiling/pre-gain from Pass 2 ebur128 measurements (before Pass 3)
	// This allows Pass 3 to measure through the same volume+alimiter prefix
	// that Pass 4 will apply, closing the measurement mismatch.
	limiterCeiling, limiterNeeded, limiterClamped := calculateLimiterCeiling(
		outputMeasurements.OutputI, outputMeasurements.OutputTP,
		config.LoudnormTargetI, config.LoudnormTargetTP,
	)
	preGainDB, reDerivedCeiling := calculatePreGain(
		outputMeasurements.OutputI, config.LoudnormTargetI, config.LoudnormTargetTP,
	)
	if limiterClamped {
		limiterCeiling = reDerivedCeiling
	}
	limiterGain := config.LoudnormTargetI - outputMeasurements.OutputI

	// Build filter prefix for Pass 3 measurement
	filterPrefix := buildPreLimiterPrefix(preGainDB, limiterCeiling, limiterNeeded)

	// Pass 3: Run loudnorm measurement pass on Pass 2 output
	// When a prefix is active, loudnorm measures the post-limiter signal,
	// so its InputI/InputTP already reflect pre-gain and limiting.
	measurement, err := measureWithLoudnorm(inputPath, config, filterPrefix, progressCallback)
	if err != nil {
		return nil, fmt.Errorf("loudnorm measurement pass failed: %w", err)
	}

	// Validate measurements are usable
	if math.IsInf(measurement.InputI, -1) || measurement.InputI < -70.0 {
		return nil, fmt.Errorf("cannot normalise silent audio (measured %.1f LUFS)", measurement.InputI)
	}

	// Signal measurement complete, starting application
	if progressCallback != nil {
		progressCallback(PassMeasuring, "Measuring", 1.0, 0.0, nil)
		progressCallback(PassNormalising, "Normalising", 0.0, 0.0, nil)
	}

	// Calculate effective target I that ensures linear mode (no dynamic fallback)
	// Pass 3 measured through the same prefix chain as Pass 4, so
	// measurement.InputI and measurement.InputTP already reflect the
	// post-limiter signal. No effectiveMeasuredI/effectiveTP adjustment needed.
	effectiveTargetI, _, linearPossible := calculateLinearModeTarget(
		measurement.InputI,
		measurement.InputTP,
		config.LoudnormTargetI,
		config.LoudnormTargetTP,
	)

	// Use loudnorm's own target_offset from the measurement pass
	offset := measurement.TargetOffset

	// Store the effective target in config for loudnorm filter construction
	effectiveConfig := *config
	effectiveConfig.LoudnormTargetI = effectiveTargetI

	// Pass 4: Apply loudnorm with linear=true and the measurements
	finalLUFS, finalTP, finalMeasurements, loudnormStats, err := applyLoudnormAndMeasure(inputPath, &effectiveConfig, measurement, inputMeasurements, preGainDB, limiterCeiling, limiterNeeded, progressCallback)
	if err != nil {
		return nil, fmt.Errorf("loudnorm application failed: %w", err)
	}

	// Signal pass complete
	if progressCallback != nil {
		progressCallback(PassNormalising, "Normalising", 1.0, 0.0, nil)
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
		PreGainDB:         preGainDB,
		LimiterClamped:    limiterClamped,
		FinalMeasurements: finalMeasurements,
	}, nil
}

// applyLoudnormAndMeasure applies loudnorm's second pass to the audio file and measures the result.
// Uses in-place processing: reads input, applies loudnorm, writes to temp file, renames.
//
// Filter chain: [volume+alimiter] → loudnorm → [adeclick] → astats → aspectralstats → ebur128 → resample
//
// This is the second pass of loudnorm's two-pass workflow. The first pass
// measurements come from measureWithLoudnorm() (stored in LoudnormMeasurement).
// Pre-computed limiter values (preGainDB, ceiling, needsLimiting) are passed through
// from ApplyNormalisation, which derives them from Pass 2 ebur128 measurements.
//
// Returns the measured integrated loudness, true peak, full output measurements,
// and loudnorm diagnostic stats.
func applyLoudnormAndMeasure(
	inputPath string,
	config *FilterChainConfig,
	measurement *LoudnormMeasurement,
	inputMeasurements *AudioMeasurements,
	preGainDB float64,
	ceiling float64,
	needsLimiting bool,
	progressCallback func(pass PassNumber, passName string, progress float64, level float64, measurements *AudioMeasurements),
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

	// Build Pass 4 filter graph: loudnorm (second pass with linear=true) → ebur128 (validation)
	filterSpec := buildLoudnormFilterSpec(config, measurement, preGainDB, ceiling, needsLimiting)

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

	lenientHandler := func(err error) error { return nil }
	loopErr := runFilterGraph(reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnPushError: lenientHandler,
		OnPullError: lenientHandler,
		OnInputFrame: func(inputFrame *ffmpeg.AVFrame) {
			samplesProcessed += int64(inputFrame.NbSamples())
		},
		OnFrame: func(inputFrame, filteredFrame *ffmpeg.AVFrame) (FrameAction, error) {
			// Extract validation measurements using Pass 2's function
			extractOutputFrameMetadata(filteredFrame.Metadata(), &acc)

			// Encode frame
			if err := encoder.WriteFrame(filteredFrame); err != nil {
				ffmpeg.AVFrameUnref(filteredFrame)
				return FrameDiscard, fmt.Errorf("encoding failed: %w", err)
			}

			framesProcessed++

			// Progress update periodically (every N output frames for smooth updates)
			if progressCallback != nil && framesProcessed%progressUpdateInterval == 0 {
				progress := math.Min(0.99, float64(samplesProcessed)/float64(totalSamples))
				progressCallback(PassNormalising, "Normalising", progress, acc.ebur128OutputI, nil)
			}

			return FrameDiscard, nil
		},
	})
	if loopErr != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return 0.0, 0.0, nil, getLoudnormStats(), loopErr
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

	// Measure silence and speech regions in final output (same regions as Pass 1 profiles)
	// NOTE: inputPath now contains the normalised output after os.Rename above
	if inputMeasurements != nil {
		var silRegion *SilenceRegion
		var spRegion *SpeechRegion
		if inputMeasurements.NoiseProfile != nil {
			silRegion = &SilenceRegion{
				Start:    inputMeasurements.NoiseProfile.Start,
				End:      inputMeasurements.NoiseProfile.Start + inputMeasurements.NoiseProfile.Duration,
				Duration: inputMeasurements.NoiseProfile.Duration,
			}
		}
		if inputMeasurements.SpeechProfile != nil {
			spRegion = &SpeechRegion{
				Start:    inputMeasurements.SpeechProfile.Region.Start,
				End:      inputMeasurements.SpeechProfile.Region.End,
				Duration: inputMeasurements.SpeechProfile.Region.Duration,
			}
		}
		if silRegion != nil || spRegion != nil {
			silSample, spSample := MeasureOutputRegions(inputPath, silRegion, spRegion)
			finalMeasurements.SilenceSample = silSample
			finalMeasurements.SpeechSample = spSample
		}
	}

	return acc.ebur128OutputI, acc.ebur128OutputTP, finalMeasurements, stats, nil
}

// buildLoudnormFilterSpec constructs the filter chain for Pass 4 loudnorm application.
//
// Chain order: [volume+alimiter] → loudnorm → [adeclick] → astats → aspectralstats → ebur128 → resample
//
// The caller pre-computes preGainDB, ceiling, and needsLimiting from Pass 2 measurements.
// This function builds the prefix via buildPreLimiterPrefix() and passes measurement.InputI
// and measurement.InputTP directly to loudnorm as measured_I and measured_TP - no manual
// adjustment is needed because Pass 3 already measured through the same prefix chain.
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
// Per ffmpeg-loudnorm-helper: the offset parameter MUST come from loudnorm's own
// first pass measurement, not from external calculations.
func buildLoudnormFilterSpec(config *FilterChainConfig, measurement *LoudnormMeasurement, preGainDB float64, ceiling float64, needsLimiting bool) string {
	var filters []string

	// 1. Build pre-limiter prefix (volume + alimiter) from pre-computed values
	prefix := buildPreLimiterPrefix(preGainDB, ceiling, needsLimiting)
	if prefix != "" {
		filters = append(filters, prefix)
	}

	// 2. loudnorm (second pass mode)
	// measured_i/tp/lra/thresh come from loudnorm's first pass measurement
	// offset: loudnorm's own calculated offset from first pass (critical!)
	// linear=true: Enable linear mode (applies consistent gain, no adaptive EQ)
	// dual_mono=true: CRITICAL - treats mono as dual-mono for correct loudness measurement
	// print_format=json: Outputs JSON with normalization_type, target_offset, output_i/tp/lra
	//
	// Pass 3 now measures with the same volume+alimiter prefix, so measurement.InputI
	// and measurement.InputTP already reflect the post-limiter signal. No manual
	// effectiveMeasuredI/effectiveMeasuredTP adjustment needed.
	loudnormFilter := fmt.Sprintf(
		"loudnorm=I=%.2f:TP=%.2f:LRA=%.1f:measured_I=%.2f:measured_TP=%.2f:measured_LRA=%.2f:measured_thresh=%.2f:offset=%.2f:dual_mono=%s:linear=%s:print_format=json",
		config.LoudnormTargetI,  // Using %.2f for precision on adjusted targets
		config.LoudnormTargetTP, // Also %.2f for consistency
		config.LoudnormTargetLRA,
		measurement.InputI,
		measurement.InputTP,
		measurement.InputLRA,
		measurement.InputThresh,
		measurement.TargetOffset, // From first pass - critical for linear mode
		boolToString(config.LoudnormDualMono),
		boolToString(config.LoudnormLinear),
	)
	filters = append(filters, loudnormFilter)

	// 3. adeclick for click/pop repair
	// Repairs waveform discontinuities from limiter/loudnorm gain transitions
	// Must come after loudnorm (catches its clicks) and before measurement filters
	if config.AdeclickEnabled {
		filters = append(filters, fmt.Sprintf("adeclick=t=%.1f:w=%.0f:o=%.0f",
			config.AdeclickThreshold,
			config.AdeclickWindow,
			config.AdeclickOverlap,
		))
	}

	// 4. astats for amplitude measurements (same as Pass 2)
	// Provides noise floor, dynamic range, RMS level, peak level, etc.
	// measure_perchannel=all requests all available per-channel statistics
	filters = append(filters, "astats=metadata=1:measure_perchannel=all")

	// 5. aspectralstats for spectral analysis (same as Pass 2)
	// Provides centroid, spread, skewness, kurtosis, entropy, flatness, crest, rolloff, etc.
	// win_size=2048 and win_func=hann match Pass 2 settings for comparable measurements
	filters = append(filters, "aspectralstats=win_size=2048:win_func=hann:measure=all")

	// 6. ebur128 for loudness validation (metadata only, no audio modification)
	// dualmono=true ensures accurate mono loudness measurement
	// Note: ebur128 upsamples to 192kHz internally and outputs f64
	filters = append(filters, "ebur128=metadata=1:peak=sample+true:dualmono=true")

	// 7. Resample back to output format (44.1kHz/s16/mono)
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
