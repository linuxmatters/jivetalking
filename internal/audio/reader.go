// Package audio provides audio file I/O using ffmpeg-statigo
package audio

import (
	"errors"
	"fmt"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// Reader wraps ffmpeg-go demuxer and decoder for audio file reading
type Reader struct {
	fmtCtx    *ffmpeg.AVFormatContext
	decCtx    *ffmpeg.AVCodecContext
	streamIdx int
	frame     *ffmpeg.AVFrame
	packet    *ffmpeg.AVPacket
}

// Metadata contains audio file metadata
type Metadata struct {
	Duration   float64 // seconds
	SampleRate int
	Channels   int
	SampleFmt  string
	ChLayout   string
	BitDepth   int
}

// OpenAudioFile opens an audio file for reading
func OpenAudioFile(filename string) (*Reader, *Metadata, error) {
	// Format context will be allocated by AVFormatOpenInput
	var fmtCtx *ffmpeg.AVFormatContext

	// Open input file
	filenameC := ffmpeg.ToCStr(filename)
	defer filenameC.Free()

	if _, err := ffmpeg.AVFormatOpenInput(&fmtCtx, filenameC, nil, nil); err != nil {
		return nil, nil, fmt.Errorf("failed to open input file: %w", err)
	}

	// Read stream info
	if _, err := ffmpeg.AVFormatFindStreamInfo(fmtCtx, nil); err != nil {
		ffmpeg.AVFormatCloseInput(&fmtCtx)
		return nil, nil, fmt.Errorf("failed to find stream info: %w", err)
	}

	// Find audio stream
	streamIdx := -1
	var audioStream *ffmpeg.AVStream
	streams := fmtCtx.Streams()
	for i := 0; i < int(fmtCtx.NbStreams()); i++ {
		stream := streams.Get(uintptr(i))
		if stream.Codecpar().CodecType() == ffmpeg.AVMediaTypeAudio {
			streamIdx = i
			audioStream = stream
			break
		}
	}

	if streamIdx == -1 {
		ffmpeg.AVFormatCloseInput(&fmtCtx)
		return nil, nil, fmt.Errorf("no audio stream found in file: %s", filename)
	}

	// Find decoder
	codecPar := audioStream.Codecpar()
	decoder := ffmpeg.AVCodecFindDecoder(codecPar.CodecId())
	if decoder == nil {
		ffmpeg.AVFormatCloseInput(&fmtCtx)
		return nil, nil, fmt.Errorf("decoder not found for codec ID %d in file: %s", codecPar.CodecId(), filename)
	}

	// Allocate decoder context
	decCtx := ffmpeg.AVCodecAllocContext3(decoder)
	if decCtx == nil {
		ffmpeg.AVFormatCloseInput(&fmtCtx)
		return nil, nil, fmt.Errorf("failed to allocate decoder context for file: %s", filename)
	}

	// Copy codec parameters to decoder context
	if _, err := ffmpeg.AVCodecParametersToContext(decCtx, codecPar); err != nil {
		ffmpeg.AVCodecFreeContext(&decCtx)
		ffmpeg.AVFormatCloseInput(&fmtCtx)
		return nil, nil, fmt.Errorf("failed to copy codec parameters: %w", err)
	}

	// Open decoder
	if _, err := ffmpeg.AVCodecOpen2(decCtx, decoder, nil); err != nil {
		ffmpeg.AVCodecFreeContext(&decCtx)
		ffmpeg.AVFormatCloseInput(&fmtCtx)
		return nil, nil, fmt.Errorf("failed to open decoder: %w", err)
	}

	// Extract metadata
	duration := float64(fmtCtx.Duration()) / float64(ffmpeg.AVTimeBase)

	// Get channel layout description
	layoutPtr := ffmpeg.AllocCStr(64)
	defer layoutPtr.Free()

	if _, err := ffmpeg.AVChannelLayoutDescribe(decCtx.ChLayout(), layoutPtr, 64); err != nil {
		ffmpeg.AVCodecFreeContext(&decCtx)
		ffmpeg.AVFormatCloseInput(&fmtCtx)
		return nil, nil, fmt.Errorf("failed to get channel layout: %w", err)
	}

	sampleFmtName := ffmpeg.AVGetSampleFmtName(decCtx.SampleFmt())
	bytesPerSample, _ := ffmpeg.AVGetBytesPerSample(decCtx.SampleFmt())

	metadata := &Metadata{
		Duration:   duration,
		SampleRate: decCtx.SampleRate(),
		Channels:   decCtx.ChLayout().NbChannels(),
		SampleFmt:  sampleFmtName.String(),
		ChLayout:   layoutPtr.String(),
		BitDepth:   bytesPerSample * 8,
	}

	reader := &Reader{
		fmtCtx:    fmtCtx,
		decCtx:    decCtx,
		streamIdx: streamIdx,
		frame:     ffmpeg.AVFrameAlloc(),
		packet:    ffmpeg.AVPacketAlloc(),
	}

	return reader, metadata, nil
}

// ReadFrame reads the next decoded audio frame
// Returns nil when end of file is reached
func (r *Reader) ReadFrame() (*ffmpeg.AVFrame, error) {
	for {
		// Try to receive a frame from the decoder
		if _, err := ffmpeg.AVCodecReceiveFrame(r.decCtx, r.frame); err == nil {
			// Set PTS for filter graph
			r.frame.SetPts(r.frame.BestEffortTimestamp())
			return r.frame, nil
		} else if !errors.Is(err, ffmpeg.EAgain) {
			if errors.Is(err, ffmpeg.AVErrorEOF) {
				return nil, nil // EOF
			}
			return nil, fmt.Errorf("failed to receive frame: %w", err)
		}

		// Need more packets, read from file
		if _, err := ffmpeg.AVReadFrame(r.fmtCtx, r.packet); err != nil {
			if errors.Is(err, ffmpeg.AVErrorEOF) {
				// Flush decoder
				if _, err := ffmpeg.AVCodecSendPacket(r.decCtx, nil); err != nil {
					return nil, fmt.Errorf("failed to flush decoder: %w", err)
				}
				continue
			}
			return nil, fmt.Errorf("failed to read frame: %w", err)
		}

		// Skip non-audio packets
		if r.packet.StreamIndex() != r.streamIdx {
			ffmpeg.AVPacketUnref(r.packet)
			continue
		}

		// Send packet to decoder
		if _, err := ffmpeg.AVCodecSendPacket(r.decCtx, r.packet); err != nil {
			ffmpeg.AVPacketUnref(r.packet)
			return nil, fmt.Errorf("failed to send packet: %w", err)
		}

		ffmpeg.AVPacketUnref(r.packet)
	}
}

// GetTimeBase returns the time base of the audio stream
func (r *Reader) GetTimeBase() *ffmpeg.AVRational {
	return r.fmtCtx.Streams().Get(uintptr(r.streamIdx)).TimeBase()
}

// GetDecoderContext returns the decoder context (needed for filter graph setup)
func (r *Reader) GetDecoderContext() *ffmpeg.AVCodecContext {
	return r.decCtx
}

// Close releases all resources
func (r *Reader) Close() {
	if r.frame != nil {
		ffmpeg.AVFrameFree(&r.frame)
	}
	if r.packet != nil {
		ffmpeg.AVPacketFree(&r.packet)
	}
	if r.decCtx != nil {
		ffmpeg.AVCodecFreeContext(&r.decCtx)
	}
	if r.fmtCtx != nil {
		ffmpeg.AVFormatCloseInput(&r.fmtCtx)
	}
}
