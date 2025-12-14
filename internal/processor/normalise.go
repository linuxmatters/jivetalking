// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// CalculateNormalisationGain computes the gain adjustment needed to hit target LUFS.
// Returns the gain in dB and whether normalisation is needed (outside tolerance).
//
// Parameters:
//   - outputI: Measured integrated loudness from Pass 2 (LUFS)
//   - targetI: Target integrated loudness (LUFS), typically -16.0
//   - tolerance: Acceptable deviation (LU), typically 0.5
//
// Returns:
//   - gainDB: Required gain adjustment in dB (positive = boost, negative = attenuate)
//   - needed: True if adjustment exceeds tolerance
//   - err: Error if gain would exceed safe limits
func CalculateNormalisationGain(outputI, targetI, tolerance float64) (gainDB float64, needed bool, err error) {
	// Handle silence (outputI is -inf or very negative)
	if math.IsInf(outputI, -1) || outputI < -70.0 {
		return 0.0, false, fmt.Errorf("cannot normalise silent audio (measured %.1f LUFS)", outputI)
	}

	// Calculate required gain
	gainDB = targetI - outputI

	// Check if within tolerance
	if math.Abs(gainDB) <= tolerance {
		return 0.0, false, nil
	}

	// No gain limits - apply whatever is needed to hit target.
	// Low-gain recordings (e.g., SM7B) may need significant boost.
	// The limiter in Pass 3 handles any resulting peaks.

	return gainDB, true, nil
}

// NormalisationResult contains the outcome of the normalisation pass.
type NormalisationResult struct {
	InputLUFS    float64 // Pre-normalisation loudness (from Pass 2 output)
	OutputLUFS   float64 // Post-normalisation loudness (measured)
	GainApplied  float64 // Gain adjustment applied (dB)
	WithinTarget bool    // True if final output is within tolerance of target
	Skipped      bool    // True if normalisation was skipped (already within tolerance)
}

// tuneUREI1176FromOutput tunes the UREI 1176 limiter parameters based on Pass 2 output measurements.
// This is called in Pass 3, allowing the limiter to be tuned for the actual post-processed audio
// rather than the raw input characteristics.
//
// Uses these OutputMeasurements fields (via embedded BaseMeasurements):
//   - MaxDifference: transient intensity → attack time
//   - SpectralCrest: spectral peakiness → attack/release balance
//   - SpectralFlux: spectral dynamics → release time
//   - DynamicRange: overall dynamics → limiting aggressiveness
//   - OutputLRA: loudness range → release time adjustment
//   - AstatsNoiseFloor: noise level → ASC noisy boost
func tuneUREI1176FromOutput(config *FilterChainConfig, output *OutputMeasurements) {
	if output == nil {
		return // Keep defaults
	}

	// Attack time: transients need faster attack
	// MaxDifference indicates sample-to-sample changes (transient intensity)
	tuneUREI1176AttackFromOutput(config, output)

	// Release time: dynamic content needs faster release to recover
	// SpectralFlux indicates frame-to-frame spectral changes
	tuneUREI1176ReleaseFromOutput(config, output)

	// ASC (Auto-Speed Control): noisy content needs more conservative release
	// Prevents pumping on noise floor
	tuneUREI1176ASCFromOutput(config, output)
}

// tuneUREI1176AttackFromOutput sets attack time based on output transient characteristics.
// Mirrors tuneUREI1176Attack() logic but uses OutputMeasurements.
func tuneUREI1176AttackFromOutput(config *FilterChainConfig, output *OutputMeasurements) {
	// MaxDifference from astats is in sample units (0-32768 for 16-bit audio)
	// Normalize to 0-1 ratio for comparison with thresholds
	maxDiff := output.MaxDifference / 32768.0

	// Check for extreme transients (plosives, hard consonants)
	if maxDiff > u1176MaxDiffExtreme || output.SpectralCrest > u1176CrestExtreme {
		config.UREI1176Attack = u1176AttackExtreme
		return
	}

	// Sharp transients
	if maxDiff > u1176MaxDiffSharp || output.SpectralCrest > u1176CrestSharp {
		config.UREI1176Attack = u1176AttackSharp
		return
	}

	// Normal transients
	if maxDiff > u1176MaxDiffNormal {
		config.UREI1176Attack = u1176AttackNormal
		return
	}

	// Soft delivery - minimal limiting needed
	config.UREI1176Attack = u1176AttackGentle
}

// tuneUREI1176ReleaseFromOutput sets release time based on output dynamics and flux.
// Mirrors tuneUREI1176Release() logic but uses OutputMeasurements.
func tuneUREI1176ReleaseFromOutput(config *FilterChainConfig, output *OutputMeasurements) {
	var baseRelease float64

	// Classify based on flux and LRA (using OutputLRA instead of InputLRA)
	switch {
	case output.SpectralFlux > u1176FluxDynamic && output.OutputLRA > u1176LRAWide:
		// Expressive delivery - preserve dynamics with longer release
		baseRelease = u1176ReleaseExpressive
	case output.SpectralFlux < u1176FluxStatic && output.OutputLRA < u1176LRANarrow:
		// Controlled delivery - quick response OK
		baseRelease = u1176ReleaseControlled
	default:
		// Standard podcast delivery
		baseRelease = u1176ReleaseStandard
	}

	// Add recovery time for very wide dynamic range
	if output.DynamicRange > u1176DRWide {
		baseRelease += u1176ReleaseDRBoost
	}

	config.UREI1176Release = baseRelease
}

// tuneUREI1176ASCFromOutput configures Auto Soft Clipping based on output characteristics.
// Mirrors tuneUREI1176ASC() logic but uses OutputMeasurements.
func tuneUREI1176ASCFromOutput(config *FilterChainConfig, output *OutputMeasurements) {
	// Enable ASC for dynamic content
	switch {
	case output.DynamicRange > u1176DREnableASC || output.SpectralCrest > u1176CrestEnableASC:
		config.UREI1176ASC = true
		config.UREI1176ASCLevel = u1176ASCDynamic
	case output.DynamicRange > u1176DRModerateASC:
		config.UREI1176ASC = true
		config.UREI1176ASCLevel = u1176ASCModerate
	default:
		config.UREI1176ASC = false
		config.UREI1176ASCLevel = 0
		return // Early return - no ASC, no noise boost
	}

	// Boost ASC for noisy recordings (helps mask pumping artefacts)
	// Use AstatsNoiseFloor from output measurements
	if output.AstatsNoiseFloor > u1176NoiseFloorASC {
		config.UREI1176ASCLevel = math.Min(1.0, config.UREI1176ASCLevel+u1176ASCNoisyBoost)
	}
}

// ApplyNormalisation performs Pass 3: gain adjustment + peak limiting to hit target LUFS.
// Reads the Pass 2 output file, applies calculated gain and 1176 limiter, writes in-place.
//
// This is a simple, transparent operation:
// 1. Calculate gain from OutputMeasurements.OutputI
// 2. Tune UREI 1176 from OutputMeasurements
// 3. Apply gain + limiting via FFmpeg filters
// 4. Measure result to validate
//
// Parameters:
//   - inputPath: Path to Pass 2 output file (the -processed.flac file)
//   - config: Filter configuration (contains target and tolerance)
//   - outputMeasurements: Pass 2 measurements (contains OutputI)
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
	if outputMeasurements == nil {
		return nil, fmt.Errorf("normalisation requires output measurements from Pass 2")
	}

	// Calculate required gain
	gainDB, needed, err := CalculateNormalisationGain(
		outputMeasurements.OutputI,
		config.NormTargetI,
		config.NormTolerance,
	)
	if err != nil {
		return nil, fmt.Errorf("gain calculation failed: %w", err)
	}

	// Tune UREI 1176 from Pass 2 output measurements
	// This must happen regardless of gain tolerance—limiter provides peak protection
	tuneUREI1176FromOutput(config, outputMeasurements)

	// If within tolerance AND 1176 is disabled, skip Pass 3 entirely
	// If 1176 is enabled, we still need to run it for peak protection
	if !needed && !config.UREI1176Enabled {
		return &NormalisationResult{
			InputLUFS:    outputMeasurements.OutputI,
			OutputLUFS:   outputMeasurements.OutputI,
			GainApplied:  0.0,
			WithinTarget: true,
			Skipped:      true,
		}, nil
	}

	// Store calculated gain in config for logging
	config.NormGainDB = gainDB

	// Signal pass start
	if progressCallback != nil {
		progressCallback(3, "Normalising", 0.0, 0.0, nil)
	}

	// Apply gain + 1176 limiter and measure result
	finalLUFS, err := applyGainAndMeasure(inputPath, gainDB, config, progressCallback)
	if err != nil {
		return nil, fmt.Errorf("gain application failed: %w", err)
	}

	// Signal pass complete
	if progressCallback != nil {
		progressCallback(3, "Normalising", 1.0, 0.0, nil)
	}

	// Validate result is within tolerance
	deviation := math.Abs(finalLUFS - config.NormTargetI)
	withinTarget := deviation <= config.NormTolerance

	return &NormalisationResult{
		InputLUFS:    outputMeasurements.OutputI,
		OutputLUFS:   finalLUFS,
		GainApplied:  gainDB,
		WithinTarget: withinTarget,
		Skipped:      false,
	}, nil
}

// applyGainAndMeasure applies gain and limiting to the audio file and measures the result.
// Uses in-place processing: reads input, applies filters, writes to temp file, renames.
//
// Filter chain: volume=<gainDB>dB → UREI 1176 (optional) → ebur128 (validation)
//
// Returns the measured integrated loudness of the normalised output.
func applyGainAndMeasure(
	inputPath string,
	gainDB float64,
	config *FilterChainConfig,
	progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements),
) (float64, error) {
	// Open input file
	reader, metadata, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		return 0.0, fmt.Errorf("failed to open input: %w", err)
	}
	defer reader.Close()

	// Create temporary output file (keep .flac extension so FFmpeg can infer format)
	tempPath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + ".norm.tmp.flac"

	// Build Pass 3 filter graph: volume → UREI 1176 → ebur128
	filterSpec := buildNormalisationFilterSpec(gainDB, config)

	// Create filter graph using existing infrastructure
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(
		reader.GetDecoderContext(),
		filterSpec,
	)
	if err != nil {
		return 0.0, fmt.Errorf("failed to create filter graph: %w", err)
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	// Create output encoder (same format as input)
	encoder, err := createOutputEncoder(tempPath, metadata, bufferSinkCtx)
	if err != nil {
		return 0.0, fmt.Errorf("failed to create encoder: %w", err)
	}
	defer encoder.Close()

	// Allocate frame for processing
	filteredFrame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&filteredFrame)

	// Get total duration for progress calculation
	totalDuration := metadata.Duration
	sampleRate := float64(metadata.SampleRate)
	samplesPerFrame := 4096.0
	estimatedTotalFrames := (totalDuration * sampleRate) / samplesPerFrame

	// Track frame count and LUFS measurement
	frameCount := 0
	var finalLUFS float64

	// Process all frames through the filter chain
	for {
		// Read frame from input
		frame, err := reader.ReadFrame()
		if err != nil {
			return 0.0, fmt.Errorf("failed to read frame: %w", err)
		}
		if frame == nil {
			break // EOF
		}

		frameCount++

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			return 0.0, fmt.Errorf("failed to add frame to filter: %w", err)
		}

		// Pull filtered frames and encode them
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				return 0.0, fmt.Errorf("failed to get filtered frame: %w", err)
			}

			// Extract ebur128 integrated loudness from frame metadata
			if md := filteredFrame.Metadata(); md != nil {
				if entry := ffmpeg.AVDictGet(md, metaKeyEbur128I, nil, 0); entry != nil {
					var lufs float64
					if _, err := fmt.Sscanf(entry.Value().String(), "%f", &lufs); err == nil {
						finalLUFS = lufs
					}
				}
			}

			// Calculate audio level for VU meter
			currentLevel := calculateFrameLevel(filteredFrame)

			// Set timebase for the filtered frame
			filteredFrame.SetTimeBase(ffmpeg.AVBuffersinkGetTimeBase(bufferSinkCtx))

			// Encode and write the filtered frame
			if err := encoder.WriteFrame(filteredFrame); err != nil {
				return 0.0, fmt.Errorf("failed to write frame: %w", err)
			}

			ffmpeg.AVFrameUnref(filteredFrame)

			// Send periodic progress updates
			updateInterval := 100
			if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
				progress := float64(frameCount) / estimatedTotalFrames
				if progress > 1.0 {
					progress = 1.0
				}
				progressCallback(3, "Normalising", progress, currentLevel, nil)
			}
		}
	}

	// Flush the filter graph
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err != nil {
		return 0.0, fmt.Errorf("failed to flush filter: %w", err)
	}

	// Pull remaining filtered frames
	for {
		if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return 0.0, fmt.Errorf("failed to get filtered frame: %w", err)
		}

		// Extract final LUFS measurement
		if md := filteredFrame.Metadata(); md != nil {
			if entry := ffmpeg.AVDictGet(md, metaKeyEbur128I, nil, 0); entry != nil {
				var lufs float64
				if _, err := fmt.Sscanf(entry.Value().String(), "%f", &lufs); err == nil {
					finalLUFS = lufs
				}
			}
		}

		filteredFrame.SetTimeBase(ffmpeg.AVBuffersinkGetTimeBase(bufferSinkCtx))

		if err := encoder.WriteFrame(filteredFrame); err != nil {
			return 0.0, fmt.Errorf("failed to write frame: %w", err)
		}

		ffmpeg.AVFrameUnref(filteredFrame)
	}

	// Flush the encoder
	if err := encoder.Flush(); err != nil {
		return 0.0, fmt.Errorf("failed to flush encoder: %w", err)
	}

	// Close encoder before rename (deferred close will be no-op after this)
	if err := encoder.Close(); err != nil {
		// Clean up temp file on error
		os.Remove(tempPath)
		return 0.0, fmt.Errorf("failed to close encoder: %w", err)
	}

	// Atomic rename: temp file → original file (in-place update)
	if err := os.Rename(tempPath, inputPath); err != nil {
		// Clean up temp file on error
		os.Remove(tempPath)
		return 0.0, fmt.Errorf("failed to rename output: %w", err)
	}

	return finalLUFS, nil
}

// buildNormalisationFilterSpec creates the Pass 3 filter chain.
// Chain order: volume (gain) → UREI 1176 (limiting) → ebur128 (measurement)
//
// The 1176 comes AFTER gain because:
// - Gain may push peaks over the ceiling
// - Limiter provides final brick-wall protection at -1 dBTP
func buildNormalisationFilterSpec(gainDB float64, config *FilterChainConfig) string {
	var filters []string

	// 1. Volume adjustment (pure gain to hit target LUFS)
	// Use 0dB if gain is zero (pass-through for limiting-only mode)
	filters = append(filters, fmt.Sprintf("volume=%.2fdB", gainDB))

	// 2. UREI 1176 limiter (brick-wall peak protection)
	// Tuned from Pass 2 measurements via tuneUREI1176FromOutput()
	if config.UREI1176Enabled {
		if spec := config.buildUREI1176Filter(); spec != "" {
			filters = append(filters, spec)
		}
	}

	// 3. ebur128 for validation measurement (metadata only)
	// peak=sample+true enables both sample peak and true peak measurement
	// Note: ebur128 upsamples to 192kHz internally and outputs f64
	filters = append(filters, fmt.Sprintf("ebur128=metadata=1:peak=sample+true:target=%.0f", config.NormTargetI))

	// 4. Resample back to output format (44.1kHz/s16/mono)
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
