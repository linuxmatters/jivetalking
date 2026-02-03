// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// AnalyzeOnly performs Pass 1 analysis without processing.
// Returns measurements and the adapted filter configuration.
// Useful for rapid testing of silence detection algorithms without full processing.
func AnalyzeOnly(inputPath string, config *FilterChainConfig,
	progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements),
) (*AudioMeasurements, *FilterChainConfig, error) {
	// Pass 1: Analysis
	if progressCallback != nil {
		progressCallback(1, "Analyzing", 0.0, 0.0, nil)
	}

	measurements, err := AnalyzeAudio(inputPath, config, progressCallback)
	if err != nil {
		return nil, nil, fmt.Errorf("analysis failed: %w", err)
	}

	if progressCallback != nil {
		progressCallback(1, "Analyzing", 1.0, 0.0, measurements)
	}

	// Adapt config to show what would be used in Pass 2
	AdaptConfig(config, measurements)

	return measurements, config, nil
}

// ProcessAudio performs complete two-pass audio processing:
//   - Pass 1: Analyze audio to get measurements and noise floor estimate
//   - Pass 2: Process audio through filter chain (downmix → ds201_highpass → ds201_lowpass → noiseremove[anlmdn+compand] → agate → la2a → deesser → analysis → resample)
//     (Pass 3 measures loudnorm; Pass 4 applies alimiter (Volumax) + loudnorm)
//
// The output file will be named <basename>-processed.<ext> in the same directory as the input
// If progressCallback is not nil, it will be called with progress updates
func ProcessAudio(inputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements)) (*ProcessingResult, error) {
	// Pass 1: Analysis
	if progressCallback != nil {
		progressCallback(1, "Analyzing", 0.0, 0.0, nil)
	}

	measurements, err := AnalyzeAudio(inputPath, config, progressCallback)
	if err != nil {
		return nil, fmt.Errorf("Pass 1 failed: %w", err)
	}

	if progressCallback != nil {
		progressCallback(1, "Analyzing", 1.0, 0.0, measurements)
	}

	// Adapt filter configuration based on Pass 1 measurements
	AdaptConfig(config, measurements)

	// Pass 2: Processing
	if progressCallback != nil {
		progressCallback(2, "Processing", 0.0, 0.0, measurements)
	}

	// Generate output filename: input.flac → input-processed.flac
	outputPath := generateOutputPath(inputPath)

	// Enable output analysis to measure processed audio characteristics
	config.OutputAnalysisEnabled = true

	// Set Pass 2 configuration for filter chain
	config.Pass = 2
	config.FilterOrder = Pass2FilterOrder

	// Track output measurements from Pass 2 (filtered but not yet normalised)
	var filteredMeasurements *OutputMeasurements

	if err := processWithFilters(inputPath, outputPath, config, progressCallback, measurements, &filteredMeasurements); err != nil {
		return nil, fmt.Errorf("Pass 2 failed: %w", err)
	}

	if progressCallback != nil {
		progressCallback(2, "Processing", 1.0, 0.0, measurements)
	}

	// Measure silence region in Pass 2 output (before normalisation) for noise comparison
	if filteredMeasurements != nil && measurements.NoiseProfile != nil {
		silenceRegion := SilenceRegion{
			Start:    measurements.NoiseProfile.Start,
			End:      measurements.NoiseProfile.Start + measurements.NoiseProfile.Duration,
			Duration: measurements.NoiseProfile.Duration,
		}
		if silenceSample, err := MeasureOutputSilenceRegion(outputPath, silenceRegion); err == nil {
			filteredMeasurements.SilenceSample = silenceSample
		} else {
			// Log the error for debugging but don't fail the entire processing
			debugLog("Warning: Failed to measure Pass 2 silence region: %v", err)
		}
		// Non-fatal if measurement fails - we still have the other output measurements
	}

	// Measure speech region in Pass 2 output (before normalisation) for processing comparison
	if filteredMeasurements != nil && measurements.SpeechProfile != nil {
		speechRegion := SpeechRegion{
			Start:    measurements.SpeechProfile.Region.Start,
			End:      measurements.SpeechProfile.Region.End,
			Duration: measurements.SpeechProfile.Region.Duration,
		}
		if speechSample, err := MeasureOutputSpeechRegion(outputPath, speechRegion); err == nil {
			filteredMeasurements.SpeechSample = speechSample
		} else {
			// Log the error for debugging but don't fail the entire processing
			debugLog("Warning: Failed to measure Pass 2 speech region: %v", err)
		}
		// Non-fatal if measurement fails - we still have the other output measurements
	}

	// Pass 3/4: Normalisation (measurement + loudnorm application)
	// The FinalMeasurements in the result include region measurements captured in Pass 4
	var normResult *NormalisationResult
	if filteredMeasurements != nil {
		normResult, err = ApplyNormalisation(outputPath, config, filteredMeasurements, measurements, progressCallback)
		if err != nil {
			return nil, fmt.Errorf("Pass 3 failed: %w", err)
		}
	}

	// Return the processing result with output measurements for comparison
	result := &ProcessingResult{
		OutputPath:           outputPath,
		InputLUFS:            measurements.InputI,
		OutputLUFS:           0.0, // Will be set to final value below
		NoiseFloor:           measurements.NoiseFloor,
		Measurements:         measurements,
		Config:               config, // Include config for logging adaptive parameters
		FilteredMeasurements: filteredMeasurements,
		NormResult:           normResult,
	}

	// Set OutputLUFS to final value (after normalisation if applied)
	if normResult != nil && !normResult.Skipped {
		result.OutputLUFS = normResult.OutputLUFS
	} else if filteredMeasurements != nil {
		result.OutputLUFS = filteredMeasurements.OutputI
	}

	return result, nil
}

// ProcessingResult contains the results of audio processing
type ProcessingResult struct {
	OutputPath   string
	InputLUFS    float64
	OutputLUFS   float64 // Final output loudness (after normalisation if applied)
	NoiseFloor   float64
	Measurements *AudioMeasurements
	Config       *FilterChainConfig // Contains adaptive parameters used

	// Pass 2 output analysis (populated when OutputAnalysisEnabled is true)
	// Contains measurements after filter chain but before normalisation
	FilteredMeasurements *OutputMeasurements

	// Normalisation result (Pass 3/4)
	// NormResult.FinalMeasurements contains measurements after normalisation
	NormResult *NormalisationResult // nil if normalisation disabled or skipped
}

// processWithFilters performs audio processing using the standard single-input filter graph.
// Applies the filter chain built by BuildFilterSpec() which includes asendcmd for noise profile learning
// when NoiseProfileStart/End timestamps are set in the config.
// If outputMeasurements is non-nil and config.OutputAnalysisEnabled is true, populates it with Pass 2 output analysis.
func processWithFilters(inputPath, outputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements), measurements *AudioMeasurements, outputMeasurements **OutputMeasurements) error {
	// Open input audio file
	reader, metadata, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer reader.Close()

	// Get total duration for progress calculation
	totalDuration := metadata.Duration
	sampleRate := float64(metadata.SampleRate)

	// Calculate total frames estimate (duration * sample_rate / samples_per_frame)
	// For FLAC, typical frame size is 4096 samples
	samplesPerFrame := 4096.0
	estimatedTotalFrames := (totalDuration * sampleRate) / samplesPerFrame

	// Create filter graph with complete processing chain
	// NOTE: loudnorm is NOT in the Pass 2 filter chain because it always processes audio
	// (no measure-only mode). Loudnorm measurement is done separately in Pass 3.
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := CreateProcessingFilterGraph(
		reader.GetDecoderContext(),
		config,
	)
	if err != nil {
		return fmt.Errorf("failed to create filter graph: %w", err)
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	// Create output encoder
	encoder, err := createOutputEncoder(outputPath, metadata, bufferSinkCtx)
	if err != nil {
		return fmt.Errorf("failed to create encoder: %w", err)
	}
	defer encoder.Close()

	// Allocate frames for processing
	filteredFrame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&filteredFrame)

	// Initialize output measurement accumulators if output analysis is enabled
	var outputAcc *outputMetadataAccumulators
	if config.OutputAnalysisEnabled && outputMeasurements != nil {
		outputAcc = &outputMetadataAccumulators{}
	}

	// Track frame count for periodic progress updates
	frameCount := 0
	currentLevel := 0.0

	// Process all frames through the filter chain
	for {
		// Read frame from input
		frame, err := reader.ReadFrame()
		if err != nil {
			return fmt.Errorf("failed to read frame: %w", err)
		}
		if frame == nil {
			break // EOF
		}

		frameCount++

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			return fmt.Errorf("failed to add frame to filter: %w", err)
		}

		// Pull filtered frames and encode them
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				return fmt.Errorf("failed to get filtered frame: %w", err)
			}

			// Calculate audio level from FILTERED frame (shows processed audio in VU meter)
			currentLevel = calculateFrameLevel(filteredFrame)

			// Extract output measurements from filtered frame metadata (if enabled)
			if outputAcc != nil {
				extractOutputFrameMetadata(filteredFrame.Metadata(), outputAcc)
			}

			// Set timebase for the filtered frame
			filteredFrame.SetTimeBase(ffmpeg.AVBuffersinkGetTimeBase(bufferSinkCtx))

			// Encode and write the filtered frame
			if err := encoder.WriteFrame(filteredFrame); err != nil {
				return fmt.Errorf("failed to write frame: %w", err)
			}

			ffmpeg.AVFrameUnref(filteredFrame)
		}

		// Send periodic progress updates based on INPUT frame count (moved outside inner loop)
		// This ensures consistent update frequency between Pass 1 and Pass 2
		updateInterval := 100 // Send progress update every N frames
		if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
			progress := float64(frameCount) / estimatedTotalFrames
			if progress > 1.0 {
				progress = 1.0
			}
			progressCallback(2, "Processing", progress, currentLevel, measurements)
		}
	}

	// Flush the filter graph
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err != nil {
		return fmt.Errorf("failed to flush filter: %w", err)
	}

	// Pull remaining filtered frames
	for {
		if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return fmt.Errorf("failed to get filtered frame: %w", err)
		}

		// Extract output measurements from remaining frames
		if outputAcc != nil {
			extractOutputFrameMetadata(filteredFrame.Metadata(), outputAcc)
		}

		filteredFrame.SetTimeBase(ffmpeg.AVBuffersinkGetTimeBase(bufferSinkCtx))

		if err := encoder.WriteFrame(filteredFrame); err != nil {
			return fmt.Errorf("failed to write frame: %w", err)
		}

		ffmpeg.AVFrameUnref(filteredFrame)
	}

	// Flush the encoder
	if err := encoder.Flush(); err != nil {
		return fmt.Errorf("failed to flush encoder: %w", err)
	}

	// Finalize output measurements if enabled
	// NOTE: Loudnorm measurements are NOT captured here - loudnorm is not in the Pass 2
	// filter chain. Loudnorm measurement is done separately in Pass 3 via measureWithLoudnorm().
	if outputAcc != nil && outputMeasurements != nil {
		*outputMeasurements = finalizeOutputMeasurements(outputAcc)
	}

	return nil
}

// generateOutputPath creates the output filename from the input filename
// Example: /path/to/audio.flac → /path/to/audio-processed.flac
func generateOutputPath(inputPath string) string {
	dir := filepath.Dir(inputPath)
	filename := filepath.Base(inputPath)
	ext := filepath.Ext(filename)
	nameWithoutExt := strings.TrimSuffix(filename, ext)

	return filepath.Join(dir, nameWithoutExt+"-processed"+ext)
}
