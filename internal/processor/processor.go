// Package processor handles audio analysis and processing
package processor

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// AnalysisResult contains analysis-only measurements and stage timings.
type AnalysisResult struct {
	Measurements       *AudioMeasurements
	Config             *EffectiveFilterConfig
	Diagnostics        *AdaptiveDiagnostics
	AnalysisDuration   time.Duration
	AdaptationDuration time.Duration
}

// AnalyzeOnlyDetailed performs Pass 1 analysis and returns stage timing details.
func AnalyzeOnlyDetailed(inputPath string, config *BaseFilterConfig,
	progressCallback func(pass PassNumber, passName string, progress float64, level float64, measurements *AudioMeasurements),
) (*AnalysisResult, error) {
	// Pass 1: Analysis
	if progressCallback != nil {
		progressCallback(PassAnalysis, "Analyzing", 0.0, 0.0, nil)
	}

	analysisStart := time.Now()
	measurements, err := AnalyzeAudio(inputPath, config, progressCallback)
	if err != nil {
		return nil, fmt.Errorf("analysis failed: %w", err)
	}
	analysisDuration := time.Since(analysisStart)

	if progressCallback != nil {
		progressCallback(PassAnalysis, "Analyzing", 1.0, 0.0, measurements)
	}

	// Adapt config to show what would be used in Pass 2
	adaptationStart := time.Now()
	effectiveConfig, diagnostics := AdaptConfig(config, measurements)
	adaptationDuration := time.Since(adaptationStart)

	return &AnalysisResult{
		Measurements:       measurements,
		Config:             effectiveConfig,
		Diagnostics:        diagnostics,
		AnalysisDuration:   analysisDuration,
		AdaptationDuration: adaptationDuration,
	}, nil
}

// ProcessAudio performs complete two-pass audio processing:
//   - Pass 1: Analyze audio to get measurements and noise floor estimate
//   - Pass 2: Process audio through filter chain (downmix → ds201_highpass → ds201_lowpass → noiseremove[anlmdn+compand] → agate → la2a → deesser → analysis → resample)
//     (Pass 3 measures loudnorm; Pass 4 applies alimiter (Volumax) + loudnorm)
//
// The output file will be named <basename>-LUFS-NN-processed.<ext> in the same directory as the input
// If progressCallback is not nil, it will be called with progress updates
func ProcessAudio(inputPath string, config *BaseFilterConfig, progressCallback func(pass PassNumber, passName string, progress float64, level float64, measurements *AudioMeasurements)) (*ProcessingResult, error) {
	// Pass 1: Analysis
	if progressCallback != nil {
		progressCallback(PassAnalysis, "Analyzing", 0.0, 0.0, nil)
	}

	measurements, err := AnalyzeAudio(inputPath, config, progressCallback)
	if err != nil {
		return nil, fmt.Errorf("pass 1 failed: %w", err)
	}

	if progressCallback != nil {
		progressCallback(PassAnalysis, "Analyzing", 1.0, 0.0, measurements)
	}

	// Adapt filter configuration based on Pass 1 measurements
	effectiveConfig, diagnostics := AdaptConfig(config, measurements)
	if effectiveConfig == nil {
		return nil, fmt.Errorf("adaptive config failed")
	}

	// Pass 2: Processing
	if progressCallback != nil {
		progressCallback(PassProcessing, "Processing", 0.0, 0.0, measurements)
	}

	// Generate output filename: input.flac → input-processed.flac
	outputPath := generateOutputPath(inputPath)

	// Set Pass 2 configuration for filter chain
	effectiveConfig.Pass = PassProcessing
	effectiveConfig.FilterOrder = append([]FilterID(nil), Pass2FilterOrder...)

	// Track output measurements from Pass 2 (filtered but not yet normalised)
	var filteredMeasurements *OutputMeasurements
	var regionTimings RegionMeasurementTimings

	inputMetadata, err := processWithFilters(inputPath, outputPath, effectiveConfig, progressCallback, measurements, &filteredMeasurements)
	if err != nil {
		return nil, fmt.Errorf("pass 2 failed: %w", err)
	}

	if progressCallback != nil {
		progressCallback(PassProcessing, "Processing", 1.0, 0.0, measurements)
	}

	// Measure silence and speech regions in Pass 2 output (before normalisation) for comparison
	if filteredMeasurements != nil {
		silRegion, spRegion := extractRegionPair(measurements)
		if silRegion != nil || spRegion != nil {
			regionStart := time.Now()
			silSample, spSample := MeasureOutputRegions(outputPath, silRegion, spRegion)
			regionTimings.FilteredOutput = time.Since(regionStart)
			filteredMeasurements.SilenceSample = silSample
			filteredMeasurements.SpeechSample = spSample
		}
	}

	// Pass 3/4: Normalisation (measurement + loudnorm application)
	// The FinalMeasurements in the result include region measurements captured in Pass 4
	var normResult *NormalisationResult
	if filteredMeasurements != nil {
		normResult, err = ApplyNormalisation(outputPath, effectiveConfig, filteredMeasurements, measurements, progressCallback)
		if err != nil {
			return nil, fmt.Errorf("pass 3 failed: %w", err)
		}
		regionTimings.FinalOutput = normResult.RegionMeasurementTime
	}

	// Return the processing result with output measurements for comparison
	result := &ProcessingResult{
		OutputPath:           outputPath,
		InputLUFS:            measurements.InputI,
		OutputLUFS:           0.0, // Will be set to final value below
		NoiseFloor:           measurements.NoiseFloor,
		Measurements:         measurements,
		Config:               effectiveConfig, // Include per-file config for logging adaptive parameters
		Diagnostics:          diagnostics,
		InputMetadata:        inputMetadata,
		RegionTimings:        regionTimings,
		FilteredMeasurements: filteredMeasurements,
		NormResult:           normResult,
	}

	// Set OutputLUFS to final value (after normalisation if applied)
	if normResult != nil && !normResult.Skipped {
		result.OutputLUFS = normResult.OutputLUFS
	} else if filteredMeasurements != nil {
		result.OutputLUFS = filteredMeasurements.OutputI
	}

	// Rename output file to include LUFS value: <name>-processed.<ext> → <name>-LUFS-NN-processed.<ext>
	lufsValue := int(math.Abs(result.OutputLUFS))
	finalPath := generateLUFSOutputPath(inputPath, lufsValue)
	if err := os.Rename(outputPath, finalPath); err != nil {
		return nil, fmt.Errorf("failed to rename output: %w", err)
	}
	result.OutputPath = finalPath

	return result, nil
}

// InputMetadata contains the report-needed subset of input file metadata.
type InputMetadata struct {
	SampleRate   int
	Channels     int
	DurationSecs float64
}

// RegionMeasurementTimings contains optional reportable region measurement durations.
type RegionMeasurementTimings struct {
	FilteredOutput time.Duration
	FinalOutput    time.Duration
}

// ProcessingResult contains the results of audio processing
type ProcessingResult struct {
	OutputPath   string
	InputLUFS    float64
	OutputLUFS   float64 // Final output loudness (after normalisation if applied)
	NoiseFloor   float64
	Measurements *AudioMeasurements
	Config       *EffectiveFilterConfig // Contains adaptive parameters used
	Diagnostics  *AdaptiveDiagnostics

	InputMetadata InputMetadata
	RegionTimings RegionMeasurementTimings

	// Pass 2 output analysis (populated when requested by the processing pass)
	// Contains measurements after filter chain but before normalisation
	FilteredMeasurements *OutputMeasurements

	// Normalisation result (Pass 3/4)
	// NormResult.FinalMeasurements contains measurements after normalisation
	NormResult *NormalisationResult // nil if normalisation disabled or skipped
}

// processWithFilters performs audio processing using the standard single-input filter graph.
// Applies the filter chain built by BuildFilterSpec() which includes asendcmd for noise profile learning
// when NoiseProfileStart/End timestamps are set in the config.
// If outputMeasurements is non-nil, populates it with Pass 2 output analysis.
func processWithFilters(inputPath, outputPath string, config *EffectiveFilterConfig, progressCallback func(pass PassNumber, passName string, progress float64, level float64, measurements *AudioMeasurements), measurements *AudioMeasurements, outputMeasurements **OutputMeasurements) (InputMetadata, error) {
	// Open input audio file
	reader, metadata, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		return InputMetadata{}, fmt.Errorf("failed to open input file: %w", err)
	}
	defer reader.Close()
	inputMetadata := InputMetadata{
		SampleRate:   metadata.SampleRate,
		Channels:     metadata.Channels,
		DurationSecs: metadata.Duration,
	}

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
		return InputMetadata{}, fmt.Errorf("failed to create filter graph: %w", err)
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	// Create output encoder
	encoder, err := createOutputEncoder(outputPath, metadata, bufferSinkCtx)
	if err != nil {
		return InputMetadata{}, fmt.Errorf("failed to create encoder: %w", err)
	}
	defer encoder.Close()

	// Initialize output measurement accumulators if the caller requested output analysis.
	var outputAcc *outputMetadataAccumulators
	if outputMeasurements != nil {
		outputAcc = &outputMetadataAccumulators{}
	}

	// Track frame count for periodic progress updates
	frameCount := 0
	currentLevel := 0.0

	// Process all frames through the filter chain using runFilterGraph
	if err := runFilterGraph(reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnReadError: func(err error) error {
			return fmt.Errorf("failed to read frame: %w", err)
		},
		OnPushError: func(err error) error {
			return fmt.Errorf("failed to push frame to filter: %w", err)
		},
		OnPullError: func(err error) error {
			return fmt.Errorf("failed to pull frame from filter: %w", err)
		},
		OnInputFrame: func(inputFrame *ffmpeg.AVFrame) {
			frameCount++

			// Send periodic progress updates based on INPUT frame count
			updateInterval := 100
			if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
				progress := float64(frameCount) / estimatedTotalFrames
				if progress > 1.0 {
					progress = 1.0
				}
				progressCallback(PassProcessing, "Processing", progress, currentLevel, measurements)
			}
		},
		OnFrame: func(inputFrame, filteredFrame *ffmpeg.AVFrame) error {
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

			return nil
		},
	}); err != nil {
		return InputMetadata{}, err
	}

	// Flush the encoder
	if err := encoder.Flush(); err != nil {
		return InputMetadata{}, fmt.Errorf("failed to flush encoder: %w", err)
	}

	// Finalize output measurements if enabled
	// NOTE: Loudnorm measurements are NOT captured here - loudnorm is not in the Pass 2
	// filter chain. Loudnorm measurement is done separately in Pass 3 via measureWithLoudnorm().
	if outputAcc != nil && outputMeasurements != nil {
		*outputMeasurements = finalizeOutputMeasurements(outputAcc)
	}

	return inputMetadata, nil
}

// generateOutputPath creates the intermediate output filename from the input filename.
// This path is used during processing; the file is later renamed by generateLUFSOutputPath
// to include the measured LUFS value. Output is always FLAC regardless of input extension.
// Example: /path/to/audio.flac → /path/to/audio-processed.flac
// Example: /path/to/audio.wav  → /path/to/audio-processed.flac
func generateOutputPath(inputPath string) string {
	dir := filepath.Dir(inputPath)
	filename := filepath.Base(inputPath)
	nameWithoutExt := strings.TrimSuffix(filename, filepath.Ext(filename))

	return filepath.Join(dir, nameWithoutExt+"-processed.flac")
}

// generateLUFSOutputPath creates the final output filename with the measured LUFS value.
// Output is always FLAC regardless of input extension.
// Example: /path/to/audio.flac → /path/to/audio-LUFS-16-processed.flac
// Example: /path/to/audio.wav  → /path/to/audio-LUFS-16-processed.flac
func generateLUFSOutputPath(inputPath string, lufsValue int) string {
	dir := filepath.Dir(inputPath)
	filename := filepath.Base(inputPath)
	nameWithoutExt := strings.TrimSuffix(filename, filepath.Ext(filename))
	return filepath.Join(dir, fmt.Sprintf("%s-LUFS-%d-processed.flac", nameWithoutExt, lufsValue))
}
