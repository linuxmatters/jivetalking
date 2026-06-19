// Package audio provides audio file I/O using ffmpeg-statigo
package audio

import (
	"errors"
	"fmt"
	"math"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// Reader wraps an ffmpeg-statigo demuxer and decoder for audio file reading.
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
}

// OpenAudioFile opens an audio file for reading
func OpenAudioFile(filename string) (*Reader, *Metadata, error) {
	// AVFormatOpenInput allocates fmtCtx when passed a nil pointer.
	var fmtCtx *ffmpeg.AVFormatContext

	filenameC := ffmpeg.ToCStr(filename)
	defer filenameC.Free()

	// cleanup frees the cgo resources allocated so far in reverse order (decCtx
	// before fmtCtx). It only runs on an error return; the success path hands
	// ownership to the Reader and frees nothing.
	decCtx := (*ffmpeg.AVCodecContext)(nil)
	cleanup := func() {
		freeContexts(&decCtx, &fmtCtx)
	}

	if _, err := ffmpeg.AVFormatOpenInput(&fmtCtx, filenameC, nil, nil); err != nil {
		return nil, nil, fmt.Errorf("failed to open input file: %w", err)
	}

	if _, err := ffmpeg.AVFormatFindStreamInfo(fmtCtx, nil); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to find stream info: %w", err)
	}

	streamIdx := -1
	var audioStream *ffmpeg.AVStream
	streams := fmtCtx.Streams()
	for i := range int(fmtCtx.NbStreams()) { //nolint:gosec // NbStreams is a small count, overflow impossible
		stream := streams.Get(uintptr(i))
		if stream.Codecpar().CodecType() == ffmpeg.AVMediaTypeAudio {
			streamIdx = i
			audioStream = stream
			break
		}
	}

	if streamIdx == -1 {
		cleanup()
		return nil, nil, fmt.Errorf("no audio stream found in file: %s", filename)
	}

	codecPar := audioStream.Codecpar()
	decoder := ffmpeg.AVCodecFindDecoder(codecPar.CodecId())
	if decoder == nil {
		cleanup()
		return nil, nil, fmt.Errorf("decoder not found for codec ID %d in file: %s", codecPar.CodecId(), filename)
	}

	decCtx = ffmpeg.AVCodecAllocContext3(decoder)
	if decCtx == nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to allocate decoder context for file: %s", filename)
	}

	if _, err := ffmpeg.AVCodecParametersToContext(decCtx, codecPar); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to copy codec parameters: %w", err)
	}

	if _, err := ffmpeg.AVCodecOpen2(decCtx, decoder, nil); err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to open decoder: %w", err)
	}

	duration := float64(fmtCtx.Duration()) / float64(ffmpeg.AVTimeBase)

	metadata := &Metadata{
		Duration:   duration,
		SampleRate: decCtx.SampleRate(),
		Channels:   decCtx.ChLayout().NbChannels(),
	}

	frame := ffmpeg.AVFrameAlloc()
	if frame == nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to allocate frame for file: %s", filename)
	}

	packet := ffmpeg.AVPacketAlloc()
	if packet == nil {
		ffmpeg.AVFrameFree(&frame)
		cleanup()
		return nil, nil, fmt.Errorf("failed to allocate packet for file: %s", filename)
	}

	reader := &Reader{
		fmtCtx:    fmtCtx,
		decCtx:    decCtx,
		streamIdx: streamIdx,
		frame:     frame,
		packet:    packet,
	}

	return reader, metadata, nil
}

// ReadFrame reads the next decoded audio frame
// Returns nil when end of file is reached
// The returned frame is reused by the next ReadFrame call: consume it before
// calling ReadFrame again and do not retain it.
func (r *Reader) ReadFrame() (*ffmpeg.AVFrame, error) {
	for {
		// Drain buffered frames first; only read a new packet once the decoder
		// reports EAgain.
		if _, err := ffmpeg.AVCodecReceiveFrame(r.decCtx, r.frame); err == nil {
			// Carry the decode timestamp forward; the downstream filter graph
			// keys frame ordering off PTS.
			r.frame.SetPts(r.frame.BestEffortTimestamp())
			return r.frame, nil
		} else if !errors.Is(err, ffmpeg.EAgain) {
			if errors.Is(err, ffmpeg.AVErrorEOF) {
				return nil, nil // EOF
			}
			return nil, fmt.Errorf("failed to receive frame: %w", err)
		}

		// Decoder is empty: pull the next packet from the file.
		if _, err := ffmpeg.AVReadFrame(r.fmtCtx, r.packet); err != nil {
			if errors.Is(err, ffmpeg.AVErrorEOF) {
				// Send a nil packet to flush the decoder's remaining frames.
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

		if _, err := ffmpeg.AVCodecSendPacket(r.decCtx, r.packet); err != nil {
			ffmpeg.AVPacketUnref(r.packet)
			return nil, fmt.Errorf("failed to send packet: %w", err)
		}

		ffmpeg.AVPacketUnref(r.packet)
	}
}

// DecoderContext returns the decoder context for filter graph setup. The
// Reader owns this context and frees it in Close. Callers must not retain it
// or use it after Close, or they read freed cgo memory.
func (r *Reader) DecoderContext() *ffmpeg.AVCodecContext {
	return r.decCtx
}

// SeekTo seeks to the specified timestamp in AV_TIME_BASE units.
// Use 0 to seek to the beginning of the file. After seeking, the decoder
// buffers are flushed so that subsequent ReadFrame calls return fresh data.
func (r *Reader) SeekTo(timestamp int64) error {
	if _, err := ffmpeg.AVFormatSeekFile(r.fmtCtx, -1, math.MinInt64, timestamp, math.MaxInt64, 0); err != nil {
		return fmt.Errorf("failed to seek: %w", err)
	}
	ffmpeg.AVCodecFlushBuffers(r.decCtx)
	return nil
}

// freeContexts releases the decoder and format contexts in reverse order of
// acquisition (decCtx before fmtCtx), nil-guarding each. Shared by the
// OpenAudioFile error path and Close so both free the same set in one place.
func freeContexts(decCtx **ffmpeg.AVCodecContext, fmtCtx **ffmpeg.AVFormatContext) {
	if *decCtx != nil {
		ffmpeg.AVCodecFreeContext(decCtx)
	}
	if *fmtCtx != nil {
		ffmpeg.AVFormatCloseInput(fmtCtx)
	}
}

// Close releases all resources
func (r *Reader) Close() {
	if r.frame != nil {
		ffmpeg.AVFrameFree(&r.frame)
	}
	if r.packet != nil {
		ffmpeg.AVPacketFree(&r.packet)
	}
	freeContexts(&r.decCtx, &r.fmtCtx)
}
