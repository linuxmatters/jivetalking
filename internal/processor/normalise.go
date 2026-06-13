// Package processor handles audio analysis and processing
package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// normaliseDuration returns the total audio length in seconds from the Pass 1
// measurements, or 0 when they are unavailable. Used to carry Duration through
// the Pass 3/4 progress callbacks without re-opening the input.
func normaliseDuration(m *AudioMeasurements) float64 {
	if m == nil {
		return 0
	}
	return m.Duration
}

// Limiter ceiling constants used by calculateLimiterCeiling and pre-gain deficit
// calculation.
const (
	// minLimiterCeilingDB is the practical minimum for FFmpeg's alimiter.
	// limit=0.0625 = 20*log10(0.0625) ≈ -24.08 dBTP; we use -24.0 with a small buffer.
	// This is the alimiter engine floor, not a tuning constant.
	minLimiterCeilingDB = -24.0 // dBTP

	// brickwallTruePeakHeadroomDB is the inter-sample (sample-peak vs true-peak)
	// allowance subtracted from loudnorm's TargetTP to set the brickwall's
	// sample-peak ceiling. The brickwall alimiter limits SAMPLE peak, but the spec
	// targets oversampled TRUE peak; the gap between them (the inter-sample excess)
	// pushes realised true peak above the sample ceiling.
	//
	// Corpus-derived (testdata/validation-0.5.x-vs-0.6.x/out-combined,
	// _combined-metrics.csv): across the combined post-Phase-2 run the
	// inter-sample excess (final true_peak dBTP − sample_peak dBFS) peaked at
	// 0.817 dB on LMP-76 popey, driven up by the Phase-2 loudness-cap relaxation
	// (the former static loudnorm TP relax) from the 0.60 dB the earlier 0.7 value
	// was sized for. The excess is positive on every file — realised true peak sits above
	// the sample ceiling on all of them. 0.9 dB covers the p100 of 0.817 with a
	// ~0.08 dB safety allowance, so the sample-peak brickwall keeps oversampled
	// true peak ≤ the loudnorm TargetTP on the whole corpus.
	brickwallTruePeakHeadroomDB = 0.9 // dB

	// measurementCushionDB is the fixed measurement-disagreement cushion added to
	// loudnorm's INTERNAL true-peak target (loudnormInternalTargetTP). It guards
	// the per-file projected post-gain peak against loudnorm's OWN internal
	// oversampled true-peak estimate disagreeing with our Pass-3 measured TP: Go
	// computes projectedPeak from measured_TP/measured_I; FFmpeg's loudnorm
	// computes its own output-peak estimate at a different point. If loudnorm's
	// estimate exceeds our projection by more than the cushion, loudnorm can trip
	// to DYNAMIC mode — the failure the Pass-4 leveling limiter exists to prevent.
	//
	// Corpus-measured (testdata/validation-0.5.x-vs-0.6.x/out-final/*.json):
	// delta = loudnorm_output_tp − projectedPeak is ≤ 0.05 dB across all 48 files,
	// one-sided positive (loudnorm estimates its peak ≈0.04 dB HOTTER on every
	// file). 0.2 clears the measured 0.05 bias ~4×, leaving headroom for
	// off-corpus material (other producers' audio) where the Go/FFmpeg estimator
	// gap could be larger.
	//
	// This is the ONLY static margin left in loudnorm's internal targeting. The
	// variable, corpus-dependent loudness shortfall the former 0.5 relax constant
	// covered is now derived per file from each file's Pass-3 measured_I/measured_TP
	// (see loudnormInternalTargetTP), so no corpus-tuned number remains. It applies
	// ONLY to loudnorm's internal targeting; the brickwall ceiling stays at
	// TargetTP − brickwallTruePeakHeadroomDB, unchanged.
	measurementCushionDB = 0.2 // dB

	// linearSafetyMargin keeps loudnorm safely inside linear-mode bounds in the
	// calculateLinearModeTarget guard. It accounts for floating-point precision
	// differences between Go and FFmpeg, rounding in filter parameter passing, and
	// measurement variance. loudnormInternalTargetTP folds it into the per-file
	// internal TP so the linear-mode cap is inert by construction.
	linearSafetyMargin = 0.1 // dB

	// loudnormTPMaxDB / loudnormTPMinDB bound the value emitted into loudnorm's
	// TP= option. FFmpeg's af_loudnorm rejects a TP outside [-9.0, 0.0] dBTP
	// (AVERROR(ERANGE) at graph build, see set_options in af_loudnorm.c). The
	// per-file loudnormInternalTargetTP is unbounded by construction, so the
	// emitted TP= is clamped to this range; the linear-mode guard keeps the
	// unclamped value (see the clamp site in the loudnorm filter string).
	loudnormTPMaxDB = 0.0  // dBTP
	loudnormTPMinDB = -9.0 // dBTP
)

// MinLimiterCeilingDB exports minLimiterCeilingDB so the logging package can reference the ceiling without duplicating the literal.
const MinLimiterCeilingDB = minLimiterCeilingDB

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

// parseLoudnormStatsFile reads loudnorm's JSON stats from a file and parses it
// into LoudnormStats. A missing or empty file, or one without a JSON object,
// returns an error in the same failure class as the av_log "no JSON found" path.
// This is a pure parse; it is not wired into any filter graph.
func parseLoudnormStatsFile(path string) (*LoudnormStats, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no JSON found in loudnorm stats file %s: %w", path, err)
	}

	output := string(data)

	start := strings.Index(output, "{")
	end := strings.LastIndex(output, "}")
	if start == -1 || end == -1 || end <= start {
		return nil, fmt.Errorf("no JSON found in loudnorm stats file %s (read %d bytes)", path, len(output))
	}

	jsonStr := output[start : end+1]

	var stats LoudnormStats
	if err := json.Unmarshal([]byte(jsonStr), &stats); err != nil {
		return nil, fmt.Errorf("failed to parse loudnorm JSON: %w", err)
	}

	return &stats, nil
}

// loudnormDeps injects the loudnorm passes' FFmpeg-touching collaborators
// (graph setup, frame loop, encoder construction, output rename) so tests can
// substitute fakes without mutating package state, following the
// analysisOnlyDeps pattern in cmd/jivetalking/main.go. Production callers use
// defaultLoudnormDeps().
type loudnormDeps struct {
	runFilterGraph   func(context.Context, *audio.Reader, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, FrameLoopConfig) error
	setupFilterGraph func(*ffmpeg.AVCodecContext, string) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error)
	createEncoder    func(string, *audio.Metadata, *ffmpeg.AVFilterContext) (loudnormOutputEncoder, error)
	rename           func(oldpath, newpath string) error
}

func defaultLoudnormDeps() loudnormDeps {
	return loudnormDeps{
		runFilterGraph:   runFilterGraph,
		setupFilterGraph: setupFilterGraph,
		createEncoder: func(outputPath string, metadata *audio.Metadata, bufferSinkCtx *ffmpeg.AVFilterContext) (loudnormOutputEncoder, error) {
			return createOutputEncoder(outputPath, metadata, bufferSinkCtx)
		},
		rename: os.Rename,
	}
}

type loudnormOutputEncoder interface {
	WriteFrame(*ffmpeg.AVFrame) error
	Flush() error
	Close() error
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
//   - deps: Injected collaborators (defaultLoudnormDeps() in production)
//
// Returns:
//   - measurement: Loudnorm measurements for second pass
//   - err: Error if measurement failed
func measureWithLoudnorm(ctx context.Context, inputPath string, config *EffectiveFilterConfig, filterPrefix string, progressCallback ProgressCallback, deps loudnormDeps) (*LoudnormMeasurement, error) {
	// Open input file
	reader, metadata, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open input: %w", err)
	}
	defer reader.Close()

	// Calculate total samples for progress reporting
	totalSamples := int64(metadata.Duration * float64(metadata.SampleRate))
	var samplesProcessed int64
	var frameCount int
	// currentLevel holds the instantaneous per-frame level for the live VU meter.
	var currentLevel float64
	const progressUpdateInterval = 100 // Send progress update every N frames

	// Per-call stats file: loudnorm writes its JSON to this path in uninit() on
	// graph free, isolating each graph's output (never stdout/'-', which routes
	// back through the process-global stream and reintroduces cross-graph
	// collision). Read strictly post-free; unlink on every path so no .tmp.json
	// residue survives success or error.
	statsPath, err := createSiblingStatsPath(inputPath, "loudnorm")
	if err != nil {
		return nil, fmt.Errorf("failed to create loudnorm stats file: %w", err)
	}
	defer func() { _ = os.Remove(statsPath) }()

	// Build measurement filter: loudnorm (without linear=true) + null sink
	// loudnorm in single-pass mode outputs its measurements to JSON when freed
	// We use print_format=json to get input_i, input_tp, input_lra, input_thresh, target_offset
	loudnorm := config.Loudnorm
	filterSpec := fmt.Sprintf(
		"loudnorm=I=%.1f:TP=%.1f:LRA=%.1f:dual_mono=%s:print_format=json:stats_file=%s",
		loudnorm.TargetI,
		loudnorm.TargetTP,
		loudnorm.TargetLRA,
		boolToString(loudnorm.DualMono),
		escapeFilterGraphOptionValue(statsPath),
	)

	if filterPrefix != "" {
		filterSpec = filterPrefix + "," + filterSpec
	}

	// Create filter graph
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := deps.setupFilterGraph(
		reader.GetDecoderContext(),
		filterSpec,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create filter graph: %w", err)
	}
	// Note: We free the filter graph explicitly to trigger loudnorm JSON output

	// Process all frames through loudnorm (no encoding - just measurement)
	lenientHandler := func(err error) error { return nil }
	loopErr := deps.runFilterGraph(ctx, reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnPushError: lenientHandler,
		OnPullError: lenientHandler,
		OnInputFrame: func(inputFrame *ffmpeg.AVFrame) {
			// Measure the instantaneous level of the Pass-2 output being read so the
			// VU meter animates consistently with Passes 1-2.
			currentLevel = calculateFrameLevel(inputFrame)

			samplesProcessed += int64(inputFrame.NbSamples())
			frameCount++
			if progressCallback != nil && frameCount%progressUpdateInterval == 0 {
				progress := min(0.99, float64(samplesProcessed)/float64(totalSamples))
				progressCallback(ProgressUpdate{
					Pass:     PassMeasuring,
					PassName: "Measuring",
					Progress: progress,
					Level:    currentLevel,
					Duration: metadata.Duration,
				})
			}
		},
	})

	// Free filter graph to trigger loudnorm JSON output. uninit() writes statsPath
	// as the graph frees, so the stats-file read below is strictly post-free.
	// The stats file is the sole stats source: a missing, empty, or unparseable
	// file is a measurement error.
	ffmpeg.AVFilterGraphFree(&filterGraph)
	if loopErr != nil {
		return nil, fmt.Errorf("loudnorm measurement loop failed: %w", loopErr)
	}

	stats, err := parseLoudnormStatsFile(statsPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read loudnorm stats file: %w", err)
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
// The ceiling is derived from the loudness targets. It places the post-limiter
// sample peak the full crest budget B above the pre-limiter loudness:
//
//	B = target_TP - target_I              (the fixed crest budget)
//	ceiling = target_TP - gainRequired    (= filtered_I + B, the full crest budget)
//
// The post-adeclick true-peak overshoot is caught by the downstream brickwall
// limiter (buildBrickwallLimiter, pinned to target_TP), which is the backstop.
//
// FFmpeg alimiter constraint: the limit parameter accepts 0.0625 to 1.0 (linear),
// which corresponds to -24.08 dBTP to 0 dBTP. Ceilings below this are clamped.
//
// Parameters:
//   - measured_I: Measured integrated loudness from Pass 3 (LUFS)
//   - measured_TP: Measured true peak from Pass 3 (dBTP)
//   - target_I: Target integrated loudness (LUFS), typically -16.0
//   - target_TP: Target true peak (dBTP), typically -1.0
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

	// Derived ceiling: targetTP - gainRequired (= filtered_I + B, the full crest budget).
	ceiling = targetTP - gainRequired

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
//   - targetTP: Target true peak (dBTP), typically -1.0
//
// Returns:
//   - preGainDB: The pre-gain amount in dB (positive when clamped, 0.0 otherwise)
//   - reDerivedCeiling: The limiter ceiling re-derived from post-gain values (0.0 when not clamped)
func calculatePreGain(measuredI, targetI, targetTP float64) (preGainDB, reDerivedCeiling float64) {
	gainRequired := targetI - measuredI
	idealCeiling := targetTP - gainRequired

	// No pre-gain needed if ceiling is at or above alimiter's minimum
	if idealCeiling >= minLimiterCeilingDB {
		return 0.0, 0.0
	}

	// Deficit: how far below the minimum the ideal ceiling sits
	preGainDB = minLimiterCeilingDB - idealCeiling

	// Re-derive ceiling from post-gain values
	postGainI := measuredI + preGainDB
	newGainRequired := targetI - postGainI
	reDerivedCeiling = targetTP - newGainRequired

	return preGainDB, reDerivedCeiling
}

// buildPreLimiterPrefix constructs the filter prefix for pre-limiting in Pass 3/4.
// Returns a comma-separated filter string fragment containing volume (when pre-gain
// is active) and alimiter (when limiting is needed), or "" when no limiting is needed.
//
// CBS Volumax-inspired parameters for transparent peak limiting:
//   - attack=5ms: Gentle attack preserves transient shape
//   - release=100ms: Smooth recovery eliminates pumping
//   - asc=1, asc_level=0.8: program-dependent release shaper, dormant on typical
//     material - only engages under heavy sustained limiting; kept as a safety-net
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

	limiterCeilingLinear := Decibels(ceiling).LinearAmplitude().Float64()
	limiterFilter := fmt.Sprintf(
		"alimiter=limit=%.6f:attack=5:release=100:level_in=1:level_out=1:level=0:latency=1:asc=1:asc_level=0.8",
		limiterCeilingLinear,
	)
	parts = append(parts, limiterFilter)

	return strings.Join(parts, ",")
}

// buildBrickwallLimiter builds the final-stage source-rate brickwall limiter.
// alimiter limits SAMPLE peak, so ceilingDBTP is the sample-peak ceiling: the
// caller sets it below the loudnorm true-peak target by the inter-sample
// allowance (brickwallTruePeakHeadroomDB) so oversampled true peak still lands
// under the target. This helper is a pure dBTP→string converter and applies no
// headroom itself.
func buildBrickwallLimiter(ceilingDBTP float64) string {
	limit := Decibels(ceilingDBTP).LinearAmplitude().Float64()
	return fmt.Sprintf(
		"alimiter=limit=%.6f:attack=1:release=50:level_in=1:level_out=1:level=0:latency=1:asc=1:asc_level=0.8",
		limit,
	)
}

type limiterPlan struct {
	preGainDB   float64
	ceilingDB   float64
	needed      bool
	clamped     bool
	gainDB      float64
	pass3Prefix string
	filteredTP  float64 // Pass-2 filtered true peak (dBTP) the limiter acts on
}

type loudnormApplicationRequest struct {
	inputPath         string
	config            *EffectiveFilterConfig
	measurement       *LoudnormMeasurement
	offset            float64 // Capped linear makeup (effectiveTargetI - measured_I); pins the loudnorm offset= to the capped I=
	inputMeasurements *AudioMeasurements
	limiter           limiterPlan
	progress          ProgressCallback
}

type loudnormApplicationResult struct {
	finalLUFS             float64
	finalTP               float64
	finalMeasurements     *OutputMeasurements
	loudnormStats         *LoudnormStats
	regionMeasurementTime time.Duration
}

type loudnormApplicationPreparation struct {
	reader        *audio.Reader
	metadata      *audio.Metadata
	tempPath      string
	statsPath     string
	filterGraph   *ffmpeg.AVFilterGraph
	bufferSrcCtx  *ffmpeg.AVFilterContext
	bufferSinkCtx *ffmpeg.AVFilterContext
}

type loudnormApplicationExecutionResult struct {
	acc           outputMetadataAccumulators
	loudnormStats *LoudnormStats
}

func planLimiterForLoudnorm(output *OutputMeasurements, config *EffectiveFilterConfig) limiterPlan {
	loudnorm := config.Loudnorm
	ceilingDB, needed, clamped := calculateLimiterCeiling(
		output.Loudness.OutputI, output.Loudness.OutputTP,
		loudnorm.TargetI, loudnorm.TargetTP,
	)
	preGainDB, reDerivedCeiling := calculatePreGain(
		output.Loudness.OutputI, loudnorm.TargetI, loudnorm.TargetTP,
	)
	if clamped {
		ceilingDB = reDerivedCeiling
	}

	return limiterPlan{
		preGainDB:   preGainDB,
		ceilingDB:   ceilingDB,
		needed:      needed,
		clamped:     clamped,
		gainDB:      loudnorm.TargetI - output.Loudness.OutputI,
		pass3Prefix: buildPreLimiterPrefix(preGainDB, ceilingDB, needed),
		filteredTP:  output.Loudness.OutputTP,
	}
}

// loudnormInternalTargetTP returns loudnorm's INTERNAL true-peak target, derived
// PER FILE from the Pass-3 measured true peak and integrated loudness rather than
// from a static relax constant. It is the projected post-gain peak plus a fixed
// measurement cushion:
//
//	internalTP = measuredTP + (loudnorm.TargetI − measuredI) + linearSafetyMargin + measurementCushionDB
//	           = projectedPeak + linearSafetyMargin + measurementCushionDB
//
// where gainRequired = TargetI − measuredI and projectedPeak = measuredTP + gainRequired.
//
// This is the TP value loudnorm targets for itself (its TP= param and the
// linear-mode guard in calculateLinearModeTarget), distinct from the delivered
// ceiling the downstream brickwall enforces at loudnorm.TargetTP. The per-file
// derivation makes the linear-mode cap inert by construction: substituting this
// value as targetTP into calculateLinearModeTarget, the measuredTP and measuredI
// terms cancel and maxLinearTargetI collapses to TargetI + measurementCushionDB
// (≥ TargetI), so desiredI == TargetI <= maxLinearTargetI holds for every file.
// Every file reaches full −16.0 LUFS in linear mode without a corpus-tuned relax
// constant, the deterministic generalisation across any producer's audio.
// NEVER use this for the brickwall ceiling.
func loudnormInternalTargetTP(loudnorm LoudnormConfig, measuredTP, measuredI float64) float64 {
	return measuredTP + (loudnorm.TargetI - measuredI) + linearSafetyMargin + measurementCushionDB
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
//   - target_TP: The TP this guard checks against. The caller feeds loudnorm's
//     per-file internal TP (loudnormInternalTargetTP), NOT the brickwall ceiling,
//     so the measured_TP/measured_I terms cancel and the cap is inert by
//     construction (maxLinearTargetI collapses to desired_I + measurementCushionDB).
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
	// We subtract a small safety margin (linearSafetyMargin, 0.1 dB, a package
	// const so loudnormInternalTargetTP folds the same value into its per-file
	// internal TP) to account for:
	// - Floating point precision differences between Go and FFmpeg's internal calculations
	// - Potential rounding in filter parameter passing
	// - Any measurement variance during processing
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
	InputLUFS         float64        `json:"input_lufs"`            // Pre-normalisation loudness (from Pass 2 loudnorm measurement)
	InputTP           float64        `json:"input_dbtp"`            // Pre-normalisation true peak (from Pass 2 loudnorm measurement)
	OutputLUFS        float64        `json:"output_lufs"`           // Post-normalisation loudness (measured)
	OutputTP          float64        `json:"output_dbtp"`           // Post-normalisation true peak (measured)
	GainApplied       float64        `json:"gain_applied_db"`       // Gain adjustment applied (dB) - loudnorm's target_offset
	WithinTarget      bool           `json:"within_target"`         // True if final output is within tolerance of target
	Skipped           bool           `json:"skipped"`               // True if normalisation was skipped (already within tolerance)
	LoudnormStats     *LoudnormStats `json:"loudnorm_measured"`     // Diagnostic output from loudnorm second pass (nil if capture failed)
	RequestedTargetI  float64        `json:"requested_target_lufs"` // The target I that was requested (from config)
	EffectiveTargetI  float64        `json:"effective_target_lufs"` // The target I actually used (may be lower to ensure linear mode)
	LinearModeForced  bool           `json:"linear_mode_forced"`    // True if target was adjusted to force linear mode
	ActualNormDynamic bool           `json:"actual_norm_dynamic"`   // True if loudnorm's reported normalization_type was "dynamic" (detective)

	// Limiter diagnostics (Pass 4 pre-limiting)
	LimiterEnabled    bool    `json:"limiter_enabled"`     // True if pre-limiting was applied
	LimiterCeiling    float64 `json:"ceiling_dbtp"`        // Ceiling in dBTP (only valid if LimiterEnabled)
	LimiterGain       float64 `json:"gain_db"`             // Gain required that triggered limiting (dB)
	LimiterFilteredTP float64 `json:"filtered_dbtp"`       // Pass-2 filtered true peak (dBTP) the limiter acts on
	PreGainDB         float64 `json:"pre_gain_db"`         // Pre-gain amount in dB (0.0 when no pre-gain applied)
	LimiterClamped    bool    `json:"limiter_clamped"`     // True when calculateLimiterCeiling clamped ceiling to minimum
	Pass3FilterPrefix string  `json:"pass3_filter_prefix"` // Filter prefix used for Pass 3 measurement (empty when no pre-gain/limiting)

	RegionMeasurementTime time.Duration `json:"region_measurement_ns"` // Final-output room tone/speech region measurement duration (ns)

	// FinalMeasurements is the FINAL-stage OutputMeasurements; it is assembled into
	// the record's `stages` map at RunRecord (2.4). Excluded here to avoid a nested
	// duplicate of the final stage under `normalisation`.
	FinalMeasurements *OutputMeasurements `json:"-"`
}

// loudnormFellBackToDynamic reports whether loudnorm's stats say it ran in
// dynamic mode (case-insensitive). On a dynamic fallback it emits a WARNING via
// the supplied logger; the linear-mode target adjustment is preventive only, so
// this is the sole detective signal that the output is not linearly normalised.
// Returns false when stats are nil or report linear mode.
func loudnormFellBackToDynamic(stats *LoudnormStats, inputPath string, log debugLogger) bool {
	if stats == nil || !strings.EqualFold(strings.TrimSpace(stats.NormalizationType), "dynamic") {
		return false
	}
	log.Logf("WARNING: loudnorm fell back to DYNAMIC mode on %s; output is 192kHz-derived and not linearly normalised", inputPath)
	return true
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
	ctx context.Context,
	inputPath string,
	config *EffectiveFilterConfig,
	outputMeasurements *OutputMeasurements,
	inputMeasurements *AudioMeasurements,
	progressCallback ProgressCallback,
	log debugLogger,
) (*NormalisationResult, error) {
	return applyNormalisationWithDeps(ctx, inputPath, config, outputMeasurements, inputMeasurements, progressCallback, log, defaultLoudnormDeps())
}

// applyNormalisationWithDeps drives the normalisation passes with injected
// dependencies for testing; ApplyNormalisation supplies the production set.
func applyNormalisationWithDeps(
	ctx context.Context,
	inputPath string,
	config *EffectiveFilterConfig,
	outputMeasurements *OutputMeasurements,
	inputMeasurements *AudioMeasurements,
	progressCallback ProgressCallback,
	log debugLogger,
	deps loudnormDeps,
) (*NormalisationResult, error) {
	loudnorm := config.Loudnorm
	if !loudnorm.Enabled {
		return &NormalisationResult{Skipped: true}, nil
	}

	// Signal pass start - first we measure, then we apply
	if progressCallback != nil {
		progressCallback(ProgressUpdate{
			Pass:     PassMeasuring,
			PassName: "Measuring",
			Duration: normaliseDuration(inputMeasurements),
		})
	}

	// Compute the limiter prefix from Pass 2 ebur128 measurements (before Pass 3).
	// This allows Pass 3 to measure through the same volume+alimiter prefix
	// that Pass 4 will apply, closing the measurement mismatch.
	limiter := planLimiterForLoudnorm(outputMeasurements, config)

	// Pass 3: Run loudnorm measurement pass on Pass 2 output.
	// When a prefix is active, loudnorm measures the post-limiter signal,
	// so its InputI/InputTP already reflect pre-gain and limiting.
	measurement, err := measureWithLoudnorm(ctx, inputPath, config, limiter.pass3Prefix, progressCallback, deps)
	if err != nil {
		return nil, fmt.Errorf("loudnorm measurement pass failed: %w", err)
	}

	// Validate measurements are usable
	if math.IsInf(measurement.InputI, -1) || measurement.InputI < -70.0 {
		return nil, fmt.Errorf("cannot normalise silent audio (measured %.1f LUFS)", measurement.InputI)
	}

	// Signal measurement complete, starting application
	if progressCallback != nil {
		progressCallback(ProgressUpdate{
			Pass:     PassMeasuring,
			PassName: "Measuring",
			Progress: 1.0,
			Duration: normaliseDuration(inputMeasurements),
		})
		progressCallback(ProgressUpdate{
			Pass:     PassNormalising,
			PassName: "Normalising",
			Duration: normaliseDuration(inputMeasurements),
			// Surface the limiter ceiling at Pass-4 start, the point it is known
			// (planLimiterForLoudnorm above). This lets the TUI light its Limiter row
			// while the file still renders, not only at completion. Read-only: the
			// ceiling reported here is the same limiter.ceilingDB the final
			// NormalisationResult carries.
			Limiter: &LimiterProgress{
				Enabled: limiter.needed,
				Ceiling: limiter.ceilingDB,
			},
		})
	}

	// Calculate effective target I that ensures linear mode (no dynamic fallback)
	// Pass 3 measured through the same prefix chain as Pass 4, so
	// measurement.InputI and measurement.InputTP already reflect the
	// post-limiter signal. No effectiveMeasuredI/effectiveTP adjustment needed.
	// Guard loudnorm against ITS OWN per-file internal TP target, not the brickwall
	// ceiling. The downstream brickwall (pinned to loudnorm.TargetTP) owns real
	// true-peak delivery. loudnormInternalTargetTP derives the internal TP from this
	// file's measured TP/I, so the measuredTP/measuredI terms cancel in the guard and
	// maxLinearTargetI collapses to TargetI + measurementCushionDB: the cap is inert
	// by construction and every file reaches full −16.0 LUFS in linear mode.
	effectiveTargetI, _, linearPossible := calculateLinearModeTarget(
		measurement.InputI,
		measurement.InputTP,
		loudnorm.TargetI,
		loudnormInternalTargetTP(loudnorm, measurement.InputTP, measurement.InputI),
	)

	// Bind the gain cap: the realised linear scalar gain is the CAPPED makeup
	// (effectiveTargetI - measured_I), not loudnorm's own target_offset. On a
	// high-crest stem the cap lowers effectiveTargetI below targetI, so the
	// matching offset pins the final true peak at targetTP by construction. On a
	// safe stem effectiveTargetI == targetI and this equals the planned makeup.
	offset := effectiveTargetI - measurement.InputI

	// Store the effective target in config for loudnorm filter construction
	effectiveConfig := *config
	effectiveConfig.Loudnorm.TargetI = effectiveTargetI

	// Pass 4: Apply loudnorm with linear=true and the measurements
	application, err := applyLoudnormAndMeasure(ctx, loudnormApplicationRequest{
		inputPath:         inputPath,
		config:            &effectiveConfig,
		measurement:       measurement,
		offset:            offset,
		inputMeasurements: inputMeasurements,
		limiter:           limiter,
		progress:          progressCallback,
	}, log, deps)
	if err != nil {
		return nil, fmt.Errorf("loudnorm application failed: %w", err)
	}

	// Signal pass complete
	if progressCallback != nil {
		progressCallback(ProgressUpdate{
			Pass:     PassNormalising,
			PassName: "Normalising",
			Progress: 1.0,
			Duration: normaliseDuration(inputMeasurements),
		})
	}

	// Validate result is within tolerance of the EFFECTIVE target (not the requested one)
	finalDeviation := math.Abs(application.finalLUFS - effectiveTargetI)
	withinTarget := finalDeviation <= NormToleranceLU

	// Detective check: the linear-mode guarantee is preventive only. If loudnorm
	// reports it actually ran in dynamic mode, the output is 192kHz-derived and
	// not linearly normalised. Warn and record the actual result for the report.
	actualNormDynamic := loudnormFellBackToDynamic(application.loudnormStats, inputPath, log)

	return &NormalisationResult{
		InputLUFS:             measurement.InputI,
		InputTP:               measurement.InputTP,
		OutputLUFS:            application.finalLUFS,
		OutputTP:              application.finalTP,
		GainApplied:           offset,
		WithinTarget:          withinTarget,
		Skipped:               false,
		LoudnormStats:         application.loudnormStats,
		RequestedTargetI:      loudnorm.TargetI,
		EffectiveTargetI:      effectiveTargetI,
		LinearModeForced:      !linearPossible,
		ActualNormDynamic:     actualNormDynamic,
		LimiterEnabled:        limiter.needed,
		LimiterCeiling:        limiter.ceilingDB,
		LimiterGain:           limiter.gainDB,
		LimiterFilteredTP:     limiter.filteredTP,
		PreGainDB:             limiter.preGainDB,
		LimiterClamped:        limiter.clamped,
		Pass3FilterPrefix:     limiter.pass3Prefix,
		RegionMeasurementTime: application.regionMeasurementTime,
		FinalMeasurements:     application.finalMeasurements,
	}, nil
}

// applyLoudnormAndMeasure applies loudnorm's second pass to the audio file and measures the result.
// Uses in-place processing: reads input, applies loudnorm, writes to temp file, renames.
//
// Filter chain: [volume+alimiter] → loudnorm → aresample → [adeclick] → brickwall → astats → aspectralstats → ebur128 → resample
//
// This is the second pass of loudnorm's two-pass workflow. The first pass
// measurements come from measureWithLoudnorm() (stored in LoudnormMeasurement).
// Pre-computed limiter values (preGainDB, ceiling, needsLimiting) are passed through
// from ApplyNormalisation, which derives them from Pass 2 ebur128 measurements.
//
// Returns the measured integrated loudness, true peak, full output measurements,
// and loudnorm diagnostic stats.
func applyLoudnormAndMeasure(ctx context.Context, request loudnormApplicationRequest, log debugLogger, deps loudnormDeps) (*loudnormApplicationResult, error) {
	prep, err := prepareLoudnormApplication(ctx, request, deps)
	if err != nil {
		return nil, err
	}
	defer prep.reader.Close()
	defer func() { _ = os.Remove(prep.statsPath) }()
	removeTemp := func() {
		_ = os.Remove(prep.tempPath)
	}
	// freeGraphAndReadStats frees the filter graph (loudnorm writes its JSON to
	// prep.statsPath in uninit() on free) and reads the stats file strictly
	// post-free. The stats file is the sole stats source; a missing, empty, or
	// unparseable file yields nil stats (the report's Norm Type diagnostic is
	// omitted rather than failing the run).
	freeGraphAndReadStats := func() *LoudnormStats {
		ffmpeg.AVFilterGraphFree(&prep.filterGraph)
		stats, err := parseLoudnormStatsFile(prep.statsPath)
		if err != nil {
			log.Logf("Warning: failed to read Pass 4 loudnorm stats file: %v", err)
			return nil
		}
		return stats
	}

	execution, err := executeAndPublishLoudnormApplication(ctx, prep, request, freeGraphAndReadStats, removeTemp, deps)
	if err != nil {
		return &loudnormApplicationResult{loudnormStats: execution.loudnormStats}, err
	}

	stats := freeGraphAndReadStats()

	return finalizeLoudnormApplicationResult(ctx, request, execution, stats, log), nil
}

func prepareLoudnormApplication(ctx context.Context, request loudnormApplicationRequest, deps loudnormDeps) (*loudnormApplicationPreparation, error) {
	// Abort before opening the input and allocating a filter graph if cancelled.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	reader, metadata, err := audio.OpenAudioFile(request.inputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open input: %w", err)
	}

	tempPath, err := createSiblingTempPath(request.inputPath, "loudnorm")
	if err != nil {
		reader.Close()
		return nil, fmt.Errorf("failed to create loudnorm temp output: %w", err)
	}

	// Per-call stats file: loudnorm writes its JSON to this path in uninit() on
	// graph free, isolating each graph's output (never stdout/'-', which routes
	// back through the process-global stream and reintroduces cross-graph
	// collision). Read strictly post-free; unlinked by the caller's deferred
	// removeStats so no .tmp.json residue survives success or error.
	statsPath, err := createSiblingStatsPath(request.inputPath, "loudnorm")
	if err != nil {
		_ = os.Remove(tempPath)
		reader.Close()
		return nil, fmt.Errorf("failed to create loudnorm stats file: %w", err)
	}

	filterSpec := buildLoudnormFilterSpec(
		request.config,
		request.measurement,
		request.offset,
		request.limiter.preGainDB,
		request.limiter.ceilingDB,
		request.limiter.needed,
		metadata.SampleRate,
		statsPath,
	)
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := deps.setupFilterGraph(
		reader.GetDecoderContext(),
		filterSpec,
	)
	if err != nil {
		_ = os.Remove(statsPath)
		_ = os.Remove(tempPath)
		reader.Close()
		return nil, fmt.Errorf("failed to create filter graph: %w", err)
	}

	return &loudnormApplicationPreparation{
		reader:        reader,
		metadata:      metadata,
		tempPath:      tempPath,
		statsPath:     statsPath,
		filterGraph:   filterGraph,
		bufferSrcCtx:  bufferSrcCtx,
		bufferSinkCtx: bufferSinkCtx,
	}, nil
}

func executeAndPublishLoudnormApplication(
	ctx context.Context,
	prep *loudnormApplicationPreparation,
	request loudnormApplicationRequest,
	captureGraphStats func() *LoudnormStats,
	removeTemp func(),
	deps loudnormDeps,
) (*loudnormApplicationExecutionResult, error) {
	result := &loudnormApplicationExecutionResult{}

	// Create output encoder (same format as input)
	encoder, err := deps.createEncoder(prep.tempPath, prep.metadata, prep.bufferSinkCtx)
	if err != nil {
		result.loudnormStats = captureGraphStats()
		removeTemp()
		return result, fmt.Errorf("failed to create encoder: %w", err)
	}
	encoderClosed := false
	defer func() {
		if !encoderClosed {
			_ = encoder.Close()
		}
	}()

	// Calculate total samples for accurate progress reporting
	totalSamples := int64(prep.metadata.Duration * float64(prep.metadata.SampleRate))
	var samplesProcessed int64
	var inputFramesRead int64
	// currentLevel holds the instantaneous per-frame output level for the live VU meter.
	var currentLevel float64
	const progressUpdateInterval = 100 // Send progress update every N frames

	lenientHandler := func(err error) error { return nil }
	loopErr := deps.runFilterGraph(ctx, prep.reader, prep.bufferSrcCtx, prep.bufferSinkCtx, FrameLoopConfig{
		OnPushError: lenientHandler,
		OnPullError: lenientHandler,
		OnInputFrame: func(inputFrame *ffmpeg.AVFrame) {
			// Drive progress from input-frame consumption so the bar advances
			// monotonically. loudnorm and adeclick drain output frames in large
			// bursts, so reporting on output drain alone makes the bar stall then
			// sweep. samplesProcessed and totalSamples are both at the input rate.
			samplesProcessed += int64(inputFrame.NbSamples())
			inputFramesRead++

			if request.progress != nil && inputFramesRead%progressUpdateInterval == 0 {
				progress := min(0.99, float64(samplesProcessed)/float64(totalSamples))
				request.progress(ProgressUpdate{
					Pass:     PassNormalising,
					PassName: "Normalising",
					Progress: progress,
					Level:    currentLevel,
					Duration: prep.metadata.Duration,
				})
			}
		},
		OnFrame: func(inputFrame, filteredFrame *ffmpeg.AVFrame) error {
			// Measure the instantaneous level of the final normalised output frame
			// so the VU meter shows the processed result, consistent with Pass 2.
			currentLevel = calculateFrameLevel(filteredFrame)

			// Extract validation measurements using Pass 2's function
			extractOutputFrameMetadata(filteredFrame.Metadata(), &result.acc)

			// Encode frame
			if err := encoder.WriteFrame(filteredFrame); err != nil {
				return fmt.Errorf("encoding failed: %w", err)
			}

			return nil
		},
	})
	if loopErr != nil {
		result.loudnormStats = captureGraphStats()
		encoderClosed = true
		_ = encoder.Close()
		removeTemp()
		return result, loopErr
	}

	// Flush encoder
	if err := encoder.Flush(); err != nil {
		result.loudnormStats = captureGraphStats()
		encoderClosed = true
		_ = encoder.Close()
		removeTemp()
		return result, fmt.Errorf("failed to flush encoder: %w", err)
	}

	// Close encoder before rename
	encoderClosed = true
	if err := encoder.Close(); err != nil {
		result.loudnormStats = captureGraphStats()
		removeTemp()
		return result, fmt.Errorf("failed to close encoder: %w", err)
	}

	// Atomic rename: temp file → original file (in-place update)
	if err := deps.rename(prep.tempPath, request.inputPath); err != nil {
		result.loudnormStats = captureGraphStats()
		removeTemp()
		return result, fmt.Errorf("failed to rename output: %w", err)
	}

	return result, nil
}

func finalizeLoudnormApplicationResult(
	ctx context.Context,
	request loudnormApplicationRequest,
	execution *loudnormApplicationExecutionResult,
	stats *LoudnormStats,
	log debugLogger,
) *loudnormApplicationResult {
	finalMeasurements, regionMeasurementTime := finalizeLoudnormOutputMeasurements(
		ctx,
		request.inputPath,
		request.inputMeasurements,
		&execution.acc,
		log,
	)

	return &loudnormApplicationResult{
		finalLUFS:             execution.acc.ebur128OutputI,
		finalTP:               execution.acc.ebur128OutputTP,
		finalMeasurements:     finalMeasurements,
		loudnormStats:         stats,
		regionMeasurementTime: regionMeasurementTime,
	}
}

func finalizeLoudnormOutputMeasurements(
	ctx context.Context,
	inputPath string,
	inputMeasurements *AudioMeasurements,
	acc *outputMetadataAccumulators,
	log debugLogger,
) (*OutputMeasurements, time.Duration) {
	finalMeasurements := finalizeOutputMeasurements(acc)
	var regionMeasurementTime time.Duration

	if inputMeasurements == nil {
		return finalMeasurements, regionMeasurementTime
	}

	roomToneRegion, spRegion := extractRegionPair(inputMeasurements)
	if roomToneRegion == nil && spRegion == nil {
		return finalMeasurements, regionMeasurementTime
	}

	regionStart := time.Now()
	roomToneSample, spSample := MeasureOutputRegions(ctx, inputPath, roomToneRegion, spRegion, log)
	regionMeasurementTime = time.Since(regionStart)
	finalMeasurements.RoomToneSample = roomToneSample
	finalMeasurements.SpeechSample = spSample

	return finalMeasurements, regionMeasurementTime
}

// buildLoudnormFilterSpec constructs the filter chain for Pass 4 loudnorm application.
//
// Chain order: [volume+alimiter] → loudnorm → aresample → [adeclick] → brickwall → astats → aspectralstats → ebur128 → resample
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
// astats and aspectralstats are placed before ebur128 because ebur128 forces its
// output format to f64/double (always, via query_formats). Its 192kHz true-peak
// oversampling is internal-only and does not reach the output link, so the output
// sample rate equals the input rate. We want spectral measurements at the original
// sample rate to match Pass 2's measurements for accurate comparison.
//
// The offset is the capped linear makeup (effectiveTargetI - measured_I) computed
// by calculateLinearModeTarget, not loudnorm's own first-pass target_offset. This
// makes the gain cap binding: when the cap lowers effectiveTargetI on a high-crest
// stem, the matching offset pins the realised scalar gain to the capped I=, holding
// the final true peak at targetTP. On a safe stem it equals the planned makeup.
func buildLoudnormFilterSpec(config *EffectiveFilterConfig, measurement *LoudnormMeasurement, offset float64, preGainDB float64, ceiling float64, needsLimiting bool, sourceSampleRate int, statsPath string) string {
	var filters []string
	loudnorm := config.Loudnorm

	// 1. Build pre-limiter prefix (volume + alimiter) from pre-computed values
	prefix := buildPreLimiterPrefix(preGainDB, ceiling, needsLimiting)
	if prefix != "" {
		filters = append(filters, prefix)
	}

	// 2. loudnorm (second pass mode)
	// measured_i/tp/lra/thresh come from loudnorm's first pass measurement
	// offset: the capped linear makeup (effectiveTargetI - measured_I), so the
	//   realised scalar gain matches the capped I= and holds final TP at targetTP
	// linear=true: Enable linear mode (applies consistent gain, no adaptive EQ)
	// dual_mono=true: CRITICAL - treats mono as dual-mono for correct loudness measurement
	// print_format=json: Outputs JSON with normalization_type, target_offset, output_i/tp/lra
	//
	// Pass 3 measures through the same volume+alimiter prefix, so measurement.InputI
	// and measurement.InputTP already reflect the post-limiter signal. No manual
	// effectiveMeasuredI/effectiveMeasuredTP adjustment needed.
	// loudnorm targets its INTERNAL per-file TP (the projected post-gain peak plus a
	// fixed measurement cushion, loudnormInternalTargetTP), not the delivered ceiling.
	// The downstream brickwall (built below from the un-relaxed loudnorm.TargetTP)
	// owns the real ceiling, so loudnorm can drive to full −16.0 LUFS without its own
	// limiter fighting the last fraction of a dB.
	//
	// The emitted TP= is clamped to FFmpeg's accepted range [loudnormTPMinDB,
	// loudnormTPMaxDB] = [-9, 0]. On-corpus the prefix limiter holds projectedPeak
	// ≤ TargetTP, so internalTP ≈ -0.7 (never near the upper clamp) and passes
	// through UNCHANGED — clamping to [-9, 0] (NOT to TargetTP) preserves byte
	// parity. The lower clamp only catches genuinely quiet off-corpus recordings
	// whose peak is already below -9 dBTP, where loudnorm's internal limiter is
	// inert anyway, so the clamp changes no on-corpus output. The linear-mode guard
	// above keeps the UNCLAMPED value so "reach -16 in linear mode" still holds for
	// those quiet files.
	internalTP := loudnormInternalTargetTP(loudnorm, measurement.InputTP, measurement.InputI)
	emittedTP := max(loudnormTPMinDB, min(internalTP, loudnormTPMaxDB))
	loudnormFilter := fmt.Sprintf(
		"loudnorm=I=%.2f:TP=%.2f:LRA=%.1f:measured_I=%.2f:measured_TP=%.2f:measured_LRA=%.2f:measured_thresh=%.2f:offset=%.2f:dual_mono=%s:linear=%s:print_format=json",
		loudnorm.TargetI, // %.2f for precision on adjusted targets
		// per-file loudnorm-internal TP (projectedPeak + cushion) clamped to
		// FFmpeg's [-9, 0] range, NOT the brickwall ceiling; the brickwall below
		// owns the delivered ceiling
		emittedTP,
		loudnorm.TargetLRA,
		measurement.InputI,
		measurement.InputTP,
		measurement.InputLRA,
		measurement.InputThresh,
		offset, // Capped makeup matching the capped I= (binds the gain cap)
		boolToString(loudnorm.DualMono),
		boolToString(loudnorm.Linear),
	)
	// stats_file: loudnorm writes its JSON (including normalization_type) here on
	// graph free. The caller reads it post-free for the report's Norm Type
	// diagnostic. Per-call path isolates each graph; never stdout. An empty
	// statsPath omits the option (e.g. the metadata-guard test).
	if statsPath != "" {
		loudnormFilter += ":stats_file=" + escapeFilterGraphOptionValue(statsPath)
	}
	filters = append(filters, loudnormFilter)

	// 3. Rate-normalisation barrier to the source rate before the rate-sensitive
	// section. We request linear mode (which preserves the source rate), but loudnorm
	// silently falls back to dynamic mode when its second-pass preconditions fail, and
	// dynamic mode emits at 192kHz on the output. On those fallback files the downstream
	// filters (adeclick, astats, aspectralstats, ebur128) would otherwise run at 4x the
	// sample count. This aresample is a no-op passthrough when loudnorm already outputs
	// the source rate (the linear case) and does the real downsample only on
	// dynamic-fallback files.
	if sourceSampleRate > 0 {
		filters = append(filters, fmt.Sprintf("aresample=%d", sourceSampleRate))
	}

	// 4. adeclick for click/pop repair
	// Repairs waveform discontinuities from limiter/loudnorm gain transitions
	// Must come after loudnorm (catches its clicks) and before measurement filters
	if spec := config.buildAdeclickFilter(); spec != "" {
		filters = append(filters, spec)
	}

	// alimiter limits sample peak; set its ceiling below the true-peak target by
	// the corpus-derived inter-sample allowance so realised oversampled true peak
	// lands ≤ loudnorm.TargetTP. The subtraction is explicit here, not buried in
	// the helper, so the intent reads at the call site.
	filters = append(filters, buildBrickwallLimiter(loudnorm.TargetTP-brickwallTruePeakHeadroomDB))

	// 5. astats for amplitude measurements (same as Pass 2)
	// Provides noise floor, dynamic range, RMS level, peak level, etc.
	// measure_perchannel=all requests all available per-channel statistics
	filters = append(filters, "astats=metadata=1:measure_perchannel=all")

	// 6. aspectralstats for spectral analysis (same as Pass 2)
	// Provides centroid, spread, skewness, kurtosis, entropy, flatness, crest, rolloff, etc.
	// win_size=2048 and win_func=hann match Pass 2 settings for comparable measurements
	filters = append(filters, "aspectralstats=win_size=2048:win_func=hann:measure=all")

	// 7. ebur128 for loudness validation (metadata only, no audio modification)
	// dualmono=true ensures accurate mono loudness measurement
	// Note: ebur128 forces its output format to f64/double (always); its 192kHz
	// true-peak oversampling is internal-only and the output rate equals the input rate
	filters = append(filters, "ebur128=metadata=1:peak=sample+true:dualmono=true")

	// 8. Resample back to output format (44.1kHz/s16/mono)
	// Required for the f64->s16 conversion ebur128 forces (output format f64, not a
	// rate change); encoder expects s16 at 44.1kHz
	filters = append(filters, config.buildRequiredOutputFormatFilter())

	return strings.Join(filters, ",")
}

// boolToString converts bool to loudnorm's expected string format
func boolToString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
