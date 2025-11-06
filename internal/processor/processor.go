// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/csnewman/ffmpeg-go"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// ProcessAudio performs complete two-pass audio processing:
// - Pass 1: Analyze audio to get loudnorm measurements and noise floor estimate
// - Pass 2: Process audio through complete filter chain (afftdn → agate → acompressor → loudnorm)
//
// The output file will be named <basename>-processed.<ext> in the same directory as the input
// If progressCallback is not nil, it will be called with progress updates
func ProcessAudio(inputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *LoudnormMeasurements)) (*ProcessingResult, error) {
	// Pass 1: Analysis
	// (printf output suppressed for UI compatibility)

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

	// Update config with measurements and noise floor for Pass 2
	config.Measurements = measurements
	config.NoiseFloor = measurements.NoiseFloor

	// Adaptively set highpass frequency based on spectral centroid
	// Lower spectral centroid (darker/warmer voice) = use lower cutoff to preserve warmth
	// Higher spectral centroid (brighter voice) = use higher cutoff to remove more rumble
	if measurements.SpectralCentroid > 0 {
		if measurements.SpectralCentroid > 6000 {
			// Bright voice with high-frequency energy concentration
			// Safe to use higher cutoff - voice energy is well above 100Hz
			config.HighpassFreq = 100.0
		} else if measurements.SpectralCentroid > 4000 {
			// Normal voice with balanced frequency distribution
			// Use standard cutoff for podcast speech
			config.HighpassFreq = 80.0
		} else {
			// Dark/warm voice with low-frequency energy concentration
			// Use lower cutoff to preserve voice warmth and body
			config.HighpassFreq = 60.0
		}
	}
	// If no spectral analysis available (SpectralCentroid == 0), keep default 80Hz

	// Adaptively set de-esser intensity based on spectral analysis
	// Uses both spectral centroid (energy concentration) and rolloff (high-frequency extension)
	// to intelligently detect likelihood of harsh sibilance
	if measurements.SpectralCentroid > 0 && measurements.SpectralRolloff > 0 {
		// Start with centroid-based baseline
		var baseIntensity float64
		if measurements.SpectralCentroid > 7000 {
			baseIntensity = 0.6 // Bright voice baseline
		} else if measurements.SpectralCentroid > 6000 {
			baseIntensity = 0.5 // Normal voice baseline
		} else {
			baseIntensity = 0.4 // Dark voice baseline
		}

		// Refine based on spectral rolloff (high-frequency extension)
		// Rolloff indicates where HF content actually extends to
		if measurements.SpectralRolloff < 6000 {
			// Very limited high-frequency content - unlikely to have sibilance
			// Skip deesser entirely for dark/warm voices with no HF extension
			config.DeessIntensity = 0.0
		} else if measurements.SpectralRolloff < 8000 {
			// Limited HF extension - reduce intensity
			// Even bright voices may not need much deessing if HF content drops off early
			config.DeessIntensity = baseIntensity * 0.7 // Reduce by 30%
			if config.DeessIntensity < 0.3 {
				config.DeessIntensity = 0.0 // Skip if too low
			}
		} else if measurements.SpectralRolloff > 12000 {
			// Extensive high-frequency content - likely to have sibilance
			// Increase intensity even for moderate centroid values
			config.DeessIntensity = baseIntensity * 1.2 // Increase by 20%
			if config.DeessIntensity > 0.8 {
				config.DeessIntensity = 0.8 // Cap at 0.8 for safety
			}
		} else {
			// Normal HF extension (8-12 kHz) - use baseline
			config.DeessIntensity = baseIntensity
		}
	} else if measurements.SpectralCentroid > 0 {
		// Fallback: only centroid available (no rolloff measurement)
		if measurements.SpectralCentroid > 7000 {
			config.DeessIntensity = 0.6
		} else if measurements.SpectralCentroid > 6000 {
			config.DeessIntensity = 0.5
		} else {
			config.DeessIntensity = 0.4
		}
	}
	// If no spectral analysis available, keep default 0.0 (disabled)

	// Adaptively set gate threshold based on measured noise floor from astats
	// The gate threshold should be set above the noise floor to remove noise while preserving speech
	// Offset strategy:
	// - Clean recordings (low noise floor < -60dB): Use larger offset (10dB) for safety margin
	// - Moderate recordings (-60 to -50dB): Use standard offset (8dB) for good balance
	// - Noisy recordings (> -50dB): Use smaller offset (6dB) to preserve more speech
	var gateOffsetDB float64
	if measurements.NoiseFloor < -60.0 {
		// Very clean recording - use larger margin to avoid false triggers
		gateOffsetDB = 10.0
	} else if measurements.NoiseFloor < -50.0 {
		// Typical podcast recording - standard margin
		gateOffsetDB = 8.0
	} else {
		// Noisy recording - use smaller margin to preserve more speech
		gateOffsetDB = 6.0
	}

	// Calculate gate threshold: noise floor + offset (in dB), then convert to linear
	gateThresholdDB := measurements.NoiseFloor + gateOffsetDB
	config.GateThreshold = math.Pow(10, gateThresholdDB/20.0)

	// Safety limits: clamp between -55dB (0.0018) and -25dB (0.056)
	// This prevents extremes while allowing adaptation to various recording conditions
	const minThresholdDB = -55.0
	const maxThresholdDB = -25.0
	minThresholdLinear := math.Pow(10, minThresholdDB/20.0)
	maxThresholdLinear := math.Pow(10, maxThresholdDB/20.0)

	if config.GateThreshold < minThresholdLinear {
		config.GateThreshold = minThresholdLinear // -55dBFS minimum (very quiet studio)
	} else if config.GateThreshold > maxThresholdLinear {
		config.GateThreshold = maxThresholdLinear // -25dBFS maximum (noisy environment)
	}

	// Adaptively set compression based on measured dynamic range from astats
	// Dynamic range indicates how much variation exists between loud and quiet parts
	// This informs how aggressively we should compress to even out the levels
	if measurements.DynamicRange > 0 {
		if measurements.DynamicRange > 20.0 {
			// High dynamic range (>20dB) - expressive content with intentional level variations
			// Examples: Storytelling, dramatic reading, varied speaking styles
			// Strategy: Use gentle compression to preserve expression while providing some consistency
			config.CompRatio = 2.0       // Gentle 2:1 ratio
			config.CompThreshold = -18.0 // Higher threshold (only compress peaks)
			config.CompMakeup = 2.0      // Less makeup gain needed
		} else if measurements.DynamicRange > 12.0 {
			// Moderate dynamic range (12-20dB) - typical conversational podcast
			// Examples: Interview, discussion, normal speech patterns
			// Strategy: Standard compression for broadcast-quality consistency
			config.CompRatio = 2.5       // Moderate 2.5:1 ratio (default)
			config.CompThreshold = -20.0 // Standard threshold
			config.CompMakeup = 3.0      // Standard makeup gain (default)
		} else if measurements.DynamicRange > 8.0 {
			// Low-moderate dynamic range (8-12dB) - already fairly consistent
			// Examples: Experienced podcaster, good mic technique, some processing
			// Strategy: Light compression to avoid over-processing
			config.CompRatio = 2.0       // Gentle ratio
			config.CompThreshold = -22.0 // Lower threshold to catch more
			config.CompMakeup = 2.0      // Less makeup needed
		} else {
			// Very low dynamic range (<8dB) - already heavily compressed/limited
			// Examples: Pre-processed audio, aggressive recording chain, broadcast feeds
			// Strategy: Minimal or no compression to avoid artifacts
			config.CompRatio = 1.5       // Very gentle
			config.CompThreshold = -16.0 // High threshold (barely compress)
			config.CompMakeup = 1.0      // Minimal makeup gain
			// Note: Could skip compression entirely, but gentle settings provide safety net
		}
	}
	// If no dynamic range measurement available, keep defaults (ratio: 2.5, threshold: -20dB)

	// Safety checks: ensure no NaN or Inf values
	if math.IsNaN(config.HighpassFreq) || math.IsInf(config.HighpassFreq, 0) {
		config.HighpassFreq = 80.0
	}
	if math.IsNaN(config.DeessIntensity) || math.IsInf(config.DeessIntensity, 0) {
		config.DeessIntensity = 0.0
	}
	if math.IsNaN(config.GateThreshold) || math.IsInf(config.GateThreshold, 0) || config.GateThreshold <= 0 {
		config.GateThreshold = 0.01 // -40dBFS default
	}
	if math.IsNaN(config.CompRatio) || math.IsInf(config.CompRatio, 0) {
		config.CompRatio = 2.5
	}
	if math.IsNaN(config.CompThreshold) || math.IsInf(config.CompThreshold, 0) {
		config.CompThreshold = -20.0
	}
	if math.IsNaN(config.CompMakeup) || math.IsInf(config.CompMakeup, 0) {
		config.CompMakeup = 3.0
	}

	// Pass 2: Processing
	// (printf output suppressed for UI compatibility)

	if progressCallback != nil {
		progressCallback(2, "Processing", 0.0, 0.0, measurements)
	}

	// Generate output filename: input.flac → input-processed.flac
	outputPath := generateOutputPath(inputPath)

	if err := processWithFilters(inputPath, outputPath, config, progressCallback, measurements); err != nil {
		return nil, fmt.Errorf("Pass 2 failed: %w", err)
	}

	if progressCallback != nil {
		progressCallback(2, "Processing", 1.0, 0.0, measurements)
	}

	// Return the processing result
	result := &ProcessingResult{
		OutputPath:   outputPath,
		InputLUFS:    measurements.InputI,
		OutputLUFS:   config.TargetI,
		NoiseFloor:   measurements.NoiseFloor,
		Measurements: measurements,
		Config:       config, // Include config for logging adaptive parameters
	}

	return result, nil
}

// ProcessingResult contains the results of audio processing
type ProcessingResult struct {
	OutputPath   string
	InputLUFS    float64
	OutputLUFS   float64
	NoiseFloor   float64
	Measurements *LoudnormMeasurements
	Config       *FilterChainConfig // Contains adaptive parameters used
}

// processWithFilters performs the actual audio processing with the complete filter chain
func processWithFilters(inputPath, outputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *LoudnormMeasurements), measurements *LoudnormMeasurements) error {
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

	// Track frame count for periodic progress updates
	frameCount := 0

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
			currentLevel := calculateFrameLevel(filteredFrame)

			// Send periodic progress updates based on frame count
			updateInterval := 100 // Send progress update every N frames
			if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
				progress := float64(frameCount) / estimatedTotalFrames
				if progress > 1.0 {
					progress = 1.0
				}
				progressCallback(2, "Processing", progress, currentLevel, measurements)
			}

			// Set timebase for the filtered frame
			filteredFrame.SetTimeBase(ffmpeg.AVBuffersinkGetTimeBase(bufferSinkCtx))

			// Encode and write the filtered frame
			if err := encoder.WriteFrame(filteredFrame); err != nil {
				return fmt.Errorf("failed to write frame: %w", err)
			}

			ffmpeg.AVFrameUnref(filteredFrame)
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

// Encoder wraps the audio encoding and muxing functionality
type Encoder struct {
	fmtCtx    *ffmpeg.AVFormatContext
	encCtx    *ffmpeg.AVCodecContext
	stream    *ffmpeg.AVStream
	packet    *ffmpeg.AVPacket
	streamIdx int
}

// createOutputEncoder creates an encoder for FLAC output
// TODO: Add WAV fallback if FLAC encoder is not available
func createOutputEncoder(outputPath string, metadata *audio.Metadata, bufferSinkCtx *ffmpeg.AVFilterContext) (*Encoder, error) {
	// Allocate output format context
	outputPathC := ffmpeg.ToCStr(outputPath)
	defer outputPathC.Free()

	var fmtCtx *ffmpeg.AVFormatContext
	if _, err := ffmpeg.AVFormatAllocOutputContext2(&fmtCtx, nil, nil, outputPathC); err != nil {
		return nil, fmt.Errorf("failed to allocate output context: %w", err)
	}

	// Find FLAC encoder
	codec := ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdFlac)
	if codec == nil {
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("FLAC encoder not found")
	}

	// Create stream
	stream := ffmpeg.AVFormatNewStream(fmtCtx, nil)
	if stream == nil {
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to create stream")
	}

	// Allocate encoder context
	encCtx := ffmpeg.AVCodecAllocContext3(codec)
	if encCtx == nil {
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to allocate encoder context")
	}

	// Get audio parameters from filter output (we only need sample rate, format is set to S16 via aformat filter)
	if _, err := ffmpeg.AVBuffersinkGetFormat(bufferSinkCtx); err != nil { // Verify filter output is configured
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to get sample format: %w", err)
	}

	sampleRate, err := ffmpeg.AVBuffersinkGetSampleRate(bufferSinkCtx)
	if err != nil {
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to get sample rate: %w", err)
	}

	timeBase := ffmpeg.AVBuffersinkGetTimeBase(bufferSinkCtx)

	// Configure encoder - FLAC supports S16 and S32, we use S16 which matches our aformat filter
	encCtx.SetSampleFmt(ffmpeg.AVSampleFmtS16)
	encCtx.SetSampleRate(sampleRate)

	// Get channel count from filter output and set default channel layout
	channels, err := ffmpeg.AVBuffersinkGetChannels(bufferSinkCtx)
	if err != nil {
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to get channels: %w", err)
	}

	// Set default channel layout for the encoder
	ffmpeg.AVChannelLayoutDefault(encCtx.ChLayout(), channels)

	// Set compression level for FLAC
	if codec.Id() == ffmpeg.AVCodecIdFlac {
		ffmpeg.AVOptSetInt(encCtx.RawPtr(), ffmpeg.GlobalCStr("compression_level"), 5, 0)
		// FLAC encoder requires fixed frame size - must match asetnsamples filter (4096)
		encCtx.SetFrameSize(4096)
	}

	// Set global header flag if needed by the format
	if fmtCtx.Oformat().Flags()&ffmpeg.AVFmtGlobalheader != 0 {
		encCtx.SetFlags(encCtx.Flags() | ffmpeg.AVCodecFlagGlobalHeader)
	}

	encCtx.SetTimeBase(timeBase)

	// Open encoder
	if _, err := ffmpeg.AVCodecOpen2(encCtx, codec, nil); err != nil {
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to open encoder: %w", err)
	}

	// Copy encoder parameters to stream
	if _, err := ffmpeg.AVCodecParametersFromContext(stream.Codecpar(), encCtx); err != nil {
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to copy encoder parameters: %w", err)
	}

	stream.SetTimeBase(encCtx.TimeBase())

	// Open output file
	if fmtCtx.Oformat().Flags()&ffmpeg.AVFmtNofile == 0 {
		var pb *ffmpeg.AVIOContext
		if _, err := ffmpeg.AVIOOpen(&pb, outputPathC, ffmpeg.AVIOFlagWrite); err != nil {
			ffmpeg.AVCodecFreeContext(&encCtx)
			ffmpeg.AVFormatFreeContext(fmtCtx)
			return nil, fmt.Errorf("failed to open output file: %w", err)
		}
		fmtCtx.SetPb(pb)
	}

	// Write header
	if _, err := ffmpeg.AVFormatWriteHeader(fmtCtx, nil); err != nil {
		if fmtCtx.Pb() != nil {
			ffmpeg.AVIOClose(fmtCtx.Pb())
		}
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to write header: %w", err)
	}

	packet := ffmpeg.AVPacketAlloc()
	if packet == nil {
		if fmtCtx.Pb() != nil {
			ffmpeg.AVIOClose(fmtCtx.Pb())
		}
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to allocate packet")
	}

	return &Encoder{
		fmtCtx:    fmtCtx,
		encCtx:    encCtx,
		stream:    stream,
		packet:    packet,
		streamIdx: 0,
	}, nil
}

// WriteFrame encodes and writes a single audio frame
func (e *Encoder) WriteFrame(frame *ffmpeg.AVFrame) error {
	// Rescale PTS to encoder timebase if needed
	if frame.Pts() != ffmpeg.AVNoptsValue {
		frame.SetPts(
			ffmpeg.AVRescaleQ(frame.Pts(), frame.TimeBase(), e.encCtx.TimeBase()),
		)
	}

	// Send frame to encoder
	if _, err := ffmpeg.AVCodecSendFrame(e.encCtx, frame); err != nil {
		return fmt.Errorf("failed to send frame to encoder: %w", err)
	}

	// Receive encoded packets
	return e.receivePackets()
}

// Flush flushes the encoder
func (e *Encoder) Flush() error {
	// Send NULL frame to signal flush
	if _, err := ffmpeg.AVCodecSendFrame(e.encCtx, nil); err != nil {
		return fmt.Errorf("failed to flush encoder: %w", err)
	}

	return e.receivePackets()
}

// receivePackets receives and writes packets from the encoder
func (e *Encoder) receivePackets() error {
	for {
		ffmpeg.AVPacketUnref(e.packet)

		if _, err := ffmpeg.AVCodecReceivePacket(e.encCtx, e.packet); err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return fmt.Errorf("failed to receive packet: %w", err)
		}

		// Set stream index
		e.packet.SetStreamIndex(e.streamIdx)

		// Rescale timestamps
		ffmpeg.AVPacketRescaleTs(e.packet, e.encCtx.TimeBase(), e.stream.TimeBase())

		// Write packet
		if _, err := ffmpeg.AVInterleavedWriteFrame(e.fmtCtx, e.packet); err != nil {
			return fmt.Errorf("failed to write packet: %w", err)
		}
	}

	return nil
}

// calculateFrameLevel calculates the RMS (Root Mean Square) level of an audio frame in dB
// This provides accurate audio level measurement for VU meter display
func calculateFrameLevel(frame *ffmpeg.AVFrame) float64 {
	if frame == nil || frame.NbSamples() == 0 {
		return -60.0 // Silence threshold
	}

	// Get sample format to know how to interpret the data
	sampleFmt := frame.Format()
	nbSamples := frame.NbSamples()
	nbChannels := frame.ChLayout().NbChannels()

	// Get pointer to audio data (first plane for packed formats, or first channel for planar)
	dataPtr := frame.Data().Get(0)
	if dataPtr == nil {
		return -60.0
	}

	// Calculate RMS based on sample format
	// Most common formats: S16 (signed 16-bit) and FLT (32-bit float)
	var sumSquares float64
	var sampleCount int64

	switch ffmpeg.AVSampleFormat(sampleFmt) {
	case ffmpeg.AVSampleFmtS16, ffmpeg.AVSampleFmtS16P:
		// 16-bit signed integer samples
		samples := unsafe.Slice((*int16)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample) / 32768.0 // Normalize to -1.0 to 1.0
			sumSquares += normalized * normalized
			sampleCount++
		}

	case ffmpeg.AVSampleFmtFlt, ffmpeg.AVSampleFmtFltp:
		// 32-bit float samples (already normalized to -1.0 to 1.0)
		samples := unsafe.Slice((*float32)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample)
			sumSquares += normalized * normalized
			sampleCount++
		}

	case ffmpeg.AVSampleFmtS32, ffmpeg.AVSampleFmtS32P:
		// 32-bit signed integer samples
		samples := unsafe.Slice((*int32)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample) / 2147483648.0 // Normalize to -1.0 to 1.0
			sumSquares += normalized * normalized
			sampleCount++
		}

	default:
		// Unsupported format, return neutral value
		return -30.0
	}

	if sampleCount == 0 {
		return -60.0
	}

	// Calculate RMS (Root Mean Square)
	rms := math.Sqrt(sumSquares / float64(sampleCount))

	// Convert to dB: 20 * log10(rms)
	// Add small epsilon to avoid log(0)
	if rms < 0.00001 { // Equivalent to -100 dB
		return -60.0 // Floor at -60 dB for silence
	}

	levelDB := 20.0 * math.Log10(rms)

	// Clamp to reasonable range for display (-60 dB to 0 dB)
	if levelDB < -60.0 {
		levelDB = -60.0
	} else if levelDB > 0.0 {
		levelDB = 0.0
	}

	return levelDB
}

// Close closes the encoder and output file
func (e *Encoder) Close() error {
	// Write trailer
	if _, err := ffmpeg.AVWriteTrailer(e.fmtCtx); err != nil {
		return fmt.Errorf("failed to write trailer: %w", err)
	}

	// Free resources
	ffmpeg.AVPacketFree(&e.packet)
	ffmpeg.AVCodecFreeContext(&e.encCtx)

	// Close output file
	if e.fmtCtx.Oformat().Flags()&ffmpeg.AVFmtNofile == 0 {
		if e.fmtCtx.Pb() != nil {
			if _, err := ffmpeg.AVIOClose(e.fmtCtx.Pb()); err != nil {
				return fmt.Errorf("failed to close output file: %w", err)
			}
			e.fmtCtx.SetPb(nil)
		}
	}

	ffmpeg.AVFormatFreeContext(e.fmtCtx)

	return nil
}
