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

// ProcessAudio performs complete two-pass audio processing:
// - Pass 1: Analyze audio to get measurements and noise floor estimate
// - Pass 2: Process audio through complete filter chain (afftdn → agate → acompressor → dynaudnorm → alimiter)
//
// The output file will be named <basename>-processed.<ext> in the same directory as the input
// If progressCallback is not nil, it will be called with progress updates
func ProcessAudio(inputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements)) (*ProcessingResult, error) {
	// Pass 1: Analysis
	if progressCallback != nil {
		progressCallback(1, "Analyzing", 0.0, 0.0, nil)
	}

	measurements, err := AnalyzeAudio(inputPath, config.TargetI, config.TargetTP, config.TargetLRA, progressCallback)
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

	// Track output measurements from Pass 2
	var outputMeasurements *OutputMeasurements

	if err := processWithFilters(inputPath, outputPath, config, progressCallback, measurements, &outputMeasurements); err != nil {
		return nil, fmt.Errorf("Pass 2 failed: %w", err)
	}

	if progressCallback != nil {
		progressCallback(2, "Processing", 1.0, 0.0, measurements)
	}

	// Measure silence region in output file (same region as Pass 1) for noise comparison
	// This is done after processing so we can analyse the actual output file
	if outputMeasurements != nil && measurements.NoiseProfile != nil {
		silenceRegion := SilenceRegion{
			Start:    measurements.NoiseProfile.Start,
			Duration: measurements.NoiseProfile.Duration,
		}
		if silenceAnalysis, err := MeasureOutputSilenceRegion(outputPath, silenceRegion); err == nil {
			outputMeasurements.SilenceSample = silenceAnalysis
		}
		// Non-fatal if measurement fails - we still have the other output measurements
	}

	// Return the processing result with output measurements for comparison
	result := &ProcessingResult{
		OutputPath:         outputPath,
		InputLUFS:          measurements.InputI,
		OutputLUFS:         0.0, // Will be populated from OutputMeasurements if available
		NoiseFloor:         measurements.NoiseFloor,
		Measurements:       measurements,
		Config:             config, // Include config for logging adaptive parameters
		OutputMeasurements: outputMeasurements,
	}

	// Populate OutputLUFS from measurements if available
	if outputMeasurements != nil {
		result.OutputLUFS = outputMeasurements.OutputI
	}

	return result, nil
}

// ProcessingResult contains the results of audio processing
type ProcessingResult struct {
	OutputPath   string
	InputLUFS    float64
	OutputLUFS   float64
	NoiseFloor   float64
	Measurements *AudioMeasurements
	Config       *FilterChainConfig // Contains adaptive parameters used

	// Noise profile processing stats (populated when using noise profile)
	NoiseProfileUsed    bool // Whether noise profile was used for afftdn
	NoiseProfileFrames  int  // Number of frames fed for spectral learning
	MainFramesProcessed int  // Number of main audio frames processed

	// Output analysis (populated when OutputAnalysisEnabled is true)
	OutputMeasurements *OutputMeasurements // Pass 2 output measurements for comparison with Pass 1
}

// processWithFilters performs the actual audio processing with the complete filter chain.
// If a noise profile is available in the config, uses the dual-input noise profile filter graph
// for precise spectral denoising based on the extracted silence sample.
// If outputMeasurements is non-nil and config.OutputAnalysisEnabled is true, populates it with Pass 2 output analysis.
func processWithFilters(inputPath, outputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements), measurements *AudioMeasurements, outputMeasurements **OutputMeasurements) error {
	// Check if we should use noise profile processing
	if config.NoiseProfilePath != "" && config.NoiseProfileDuration > 0 {
		return processWithNoiseProfile(inputPath, outputPath, config, progressCallback, measurements, outputMeasurements)
	}

	// Standard processing without noise profile
	return processWithStandardFilters(inputPath, outputPath, config, progressCallback, measurements, outputMeasurements)
}

// processWithStandardFilters performs audio processing using the standard single-input filter graph.
// If outputMeasurements is non-nil and config.OutputAnalysisEnabled is true, populates it with Pass 2 output analysis.
func processWithStandardFilters(inputPath, outputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements), measurements *AudioMeasurements, outputMeasurements **OutputMeasurements) error {
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

	// Finalize output measurements if enabled
	if outputAcc != nil && outputMeasurements != nil {
		*outputMeasurements = finalizeOutputMeasurements(outputAcc)
	}

	// Flush the encoder
	if err := encoder.Flush(); err != nil {
		return fmt.Errorf("failed to flush encoder: %w", err)
	}

	return nil
}

// processWithNoiseProfile performs audio processing using a dual-input filter graph
// that feeds the noise profile first to train afftdn before processing main audio.
//
// The filter graph uses concat to join noise profile + main audio, asendcmd to trigger
// afftdn's sample_noise learning phase, and atrim to remove the noise prefix from output.
// If outputMeasurements is non-nil and config.OutputAnalysisEnabled is true, populates it with Pass 2 output analysis.
func processWithNoiseProfile(inputPath, outputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements), measurements *AudioMeasurements, outputMeasurements **OutputMeasurements) error {

	// Open noise profile WAV file
	noiseReader, _, err := audio.OpenAudioFile(config.NoiseProfilePath)
	if err != nil {
		// Fall back to standard processing (warning will appear in report via NoiseProfileUsed=false)
		config.NoiseProfilePath = ""
		return processWithStandardFilters(inputPath, outputPath, config, progressCallback, measurements, outputMeasurements)
	}
	defer noiseReader.Close()

	// Open main input audio file
	mainReader, metadata, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer mainReader.Close()

	// Get total duration for progress calculation (main audio only, noise is trimmed)
	totalDuration := metadata.Duration
	sampleRate := float64(metadata.SampleRate)
	samplesPerFrame := 4096.0
	estimatedTotalFrames := (totalDuration * sampleRate) / samplesPerFrame

	// Create dual-input noise profile filter graph
	npGraph, err := CreateNoiseProfileFilterGraph(
		mainReader.GetDecoderContext(),
		noiseReader.GetDecoderContext(),
		config.NoiseProfileDuration,
		config,
	)
	if err != nil {
		// Fall back to standard processing (will be noted in report via NoiseProfileUsed=false)
		config.NoiseProfilePath = ""
		return processWithStandardFilters(inputPath, outputPath, config, progressCallback, measurements, outputMeasurements)
	}
	defer ffmpeg.AVFilterGraphFree(&npGraph.Graph)

	// Create output encoder
	encoder, err := createOutputEncoder(outputPath, metadata, npGraph.BufferSink)
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

	// Phase 1: Feed noise profile frames to train afftdn
	noiseFrameCount := 0
	for {
		frame, err := noiseReader.ReadFrame()
		if err != nil {
			return fmt.Errorf("failed to read noise profile frame: %w", err)
		}
		if frame == nil {
			break // EOF
		}

		noiseFrameCount++

		// Push noise frame to noise input
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(npGraph.NoiseBufferSrc, frame, 0); err != nil {
			return fmt.Errorf("failed to add noise frame to filter: %w", err)
		}
	}

	// Flush noise input to signal EOF
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(npGraph.NoiseBufferSrc, nil, 0); err != nil {
		return fmt.Errorf("failed to flush noise buffer: %w", err)
	}

	// Store noise profile stats for reporting
	if measurements != nil {
		measurements.NoiseProfileFramesFed = noiseFrameCount
	}

	// Phase 2: Feed main audio frames
	frameCount := 0
	currentLevel := 0.0

	for {
		// Read frame from main input
		frame, err := mainReader.ReadFrame()
		if err != nil {
			return fmt.Errorf("failed to read frame: %w", err)
		}
		if frame == nil {
			break // EOF
		}

		frameCount++

		// Push frame to main input
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(npGraph.MainBufferSrc, frame, 0); err != nil {
			return fmt.Errorf("failed to add frame to filter: %w", err)
		}

		// Pull filtered frames and encode them
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(npGraph.BufferSink, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				return fmt.Errorf("failed to get filtered frame: %w", err)
			}

			// Calculate audio level from FILTERED frame
			currentLevel = calculateFrameLevel(filteredFrame)

			// Extract output measurements from filtered frame metadata (if enabled)
			if outputAcc != nil {
				extractOutputFrameMetadata(filteredFrame.Metadata(), outputAcc)
			}

			// Set timebase for the filtered frame
			filteredFrame.SetTimeBase(ffmpeg.AVBuffersinkGetTimeBase(npGraph.BufferSink))

			// Encode and write the filtered frame
			if err := encoder.WriteFrame(filteredFrame); err != nil {
				return fmt.Errorf("failed to write frame: %w", err)
			}

			ffmpeg.AVFrameUnref(filteredFrame)
		}

		// Send periodic progress updates
		updateInterval := 100
		if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
			progress := float64(frameCount) / estimatedTotalFrames
			if progress > 1.0 {
				progress = 1.0
			}
			progressCallback(2, "Processing", progress, currentLevel, measurements)
		}
	}

	// Flush the main buffer
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(npGraph.MainBufferSrc, nil, 0); err != nil {
		return fmt.Errorf("failed to flush main filter: %w", err)
	}

	// Pull remaining filtered frames
	for {
		if _, err := ffmpeg.AVBuffersinkGetFrame(npGraph.BufferSink, filteredFrame); err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return fmt.Errorf("failed to get filtered frame: %w", err)
		}

		// Extract output measurements from remaining frames
		if outputAcc != nil {
			extractOutputFrameMetadata(filteredFrame.Metadata(), outputAcc)
		}

		filteredFrame.SetTimeBase(ffmpeg.AVBuffersinkGetTimeBase(npGraph.BufferSink))

		if err := encoder.WriteFrame(filteredFrame); err != nil {
			return fmt.Errorf("failed to write frame: %w", err)
		}

		ffmpeg.AVFrameUnref(filteredFrame)
	}

	// Finalize output measurements if enabled
	if outputAcc != nil && outputMeasurements != nil {
		*outputMeasurements = finalizeOutputMeasurements(outputAcc)
	}

	// Flush the encoder
	if err := encoder.Flush(); err != nil {
		return fmt.Errorf("failed to flush encoder: %w", err)
	}

	// Store main frame count for reporting
	if measurements != nil {
		measurements.MainFramesProcessed = frameCount
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
