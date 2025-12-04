// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"unsafe"

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

	// Adapt filter configuration based on Pass 1 measurements
	AdaptConfig(config, measurements)

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
	// Note: OutputLUFS is no longer guaranteed to match TargetI
	// Dynaudnorm provides adaptive normalization but doesn't target specific LUFS values
	result := &ProcessingResult{
		OutputPath:   outputPath,
		InputLUFS:    measurements.InputI,
		OutputLUFS:   0.0, // Not measured - would require third pass analysis
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
	Measurements *AudioMeasurements
	Config       *FilterChainConfig // Contains adaptive parameters used
}

// processWithFilters performs the actual audio processing with the complete filter chain
func processWithFilters(inputPath, outputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements), measurements *AudioMeasurements) error {
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
		return nil, fmt.Errorf("FLAC encoder not found for output: %s", outputPath)
	}

	// Create stream
	stream := ffmpeg.AVFormatNewStream(fmtCtx, nil)
	if stream == nil {
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to create stream for output: %s", outputPath)
	}

	// Allocate encoder context
	encCtx := ffmpeg.AVCodecAllocContext3(codec)
	if encCtx == nil {
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to allocate encoder context for output: %s", outputPath)
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
		return nil, fmt.Errorf("failed to allocate packet for output: %s", outputPath)
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
