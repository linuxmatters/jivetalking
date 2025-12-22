// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"
	"math"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

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
// TODO: Use metadata parameter for output file metadata passthrough
func createOutputEncoder(outputPath string, _ *audio.Metadata, bufferSinkCtx *ffmpeg.AVFilterContext) (*Encoder, error) {
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

// Close closes the encoder and output file
// Safe to call multiple times - subsequent calls are no-ops.
func (e *Encoder) Close() error {
	// Guard against double-close
	if e.fmtCtx == nil {
		return nil
	}

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
	e.fmtCtx = nil // Mark as closed

	return nil
}

// calculateFrameLevel calculates the RMS (Root Mean Square) level of an audio frame in dB
// This provides accurate audio level measurement for VU meter display
func calculateFrameLevel(frame *ffmpeg.AVFrame) float64 {
	sumSquares, sampleCount, _, ok := frameSumSquaresAndPeak(frame)
	if !ok {
		return -30.0 // Unsupported format
	}
	if sampleCount == 0 {
		return -60.0 // Silence threshold
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
