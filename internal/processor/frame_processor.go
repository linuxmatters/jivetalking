// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// FrameAction controls what runFilterGraph does with a filtered frame after OnFrame returns.
type FrameAction int

const (
	// FrameDiscard unrefs and discards the filtered frame.
	FrameDiscard FrameAction = iota
	// FrameKeep leaves the frame referenced; the caller must unref when done.
	FrameKeep
)

// FrameLoopConfig controls the behaviour of runFilterGraph.
type FrameLoopConfig struct {
	// OnReadError is called when reader.ReadFrame returns an error.
	// Return nil to break the read loop (lenient); return the error to abort (strict).
	// nil callback = break on any error (lenient default).
	OnReadError func(err error) error

	// OnPushError is called when AVBuffersrcAddFrameFlags returns an error.
	// Return nil to continue reading (lenient); return the error to abort (strict).
	// nil callback = return the error (strict default).
	OnPushError func(err error) error

	// OnPullError is called when AVBuffersinkGetFrame returns an error that is
	// NOT EAGAIN and NOT EOF (those are handled internally by breaking the pull loop).
	// Return nil to break the pull loop and continue reading (lenient);
	// return the error to abort both loops (strict).
	// nil callback = return the error (strict default).
	OnPullError func(err error) error

	// OnFrame is called for each filtered frame pulled from the sink.
	// inputFrame is the most recently read input frame (before filtering).
	// filteredFrame is the frame pulled from the filter graph output.
	// Return FrameDiscard to have runFilterGraph unref the filtered frame,
	// or FrameKeep if the callback already consumed/unreffed it.
	// A non-nil error aborts both loops.
	OnFrame func(inputFrame, filteredFrame *ffmpeg.AVFrame) (FrameAction, error)

	// OnInputFrame is called for each input frame before it is pushed into
	// the filter graph. Use for pre-filter work (progress tracking, RMS accumulation).
	OnInputFrame func(inputFrame *ffmpeg.AVFrame)
}

// runFilterGraph runs the read-push-pull loop over a filter graph.
// It reads frames from reader, pushes them through the filter graph via
// bufferSrcCtx, pulls filtered frames from bufferSinkCtx, and calls the
// configured callbacks. After EOF, it flushes the filter graph and drains
// remaining frames.
//
// The caller owns the filter graph lifetime - runFilterGraph does NOT free it.
func runFilterGraph(
	reader *audio.Reader,
	bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
	config FrameLoopConfig,
) error {
	filteredFrame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&filteredFrame)

	// pullFrames drains filtered frames from the sink, calling OnFrame for each.
	// Returns an error only when the caller should abort both loops.
	pullFrames := func(inputFrame *ffmpeg.AVFrame) error {
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				if config.OnPullError != nil {
					if cbErr := config.OnPullError(err); cbErr != nil {
						return cbErr
					}
					break // callback returned nil = lenient, break inner loop
				}
				return err // nil callback = strict default
			}

			if config.OnFrame != nil {
				action, err := config.OnFrame(inputFrame, filteredFrame)
				if err != nil {
					return err
				}
				if action == FrameDiscard {
					ffmpeg.AVFrameUnref(filteredFrame)
				}
			} else {
				ffmpeg.AVFrameUnref(filteredFrame)
			}
		}
		return nil
	}

	// Main read loop
	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			if config.OnReadError != nil {
				if cbErr := config.OnReadError(err); cbErr != nil {
					return cbErr
				}
				break // callback returned nil = lenient, stop reading
			}
			break // nil callback = break (lenient default)
		}
		if frame == nil {
			break // EOF
		}

		if config.OnInputFrame != nil {
			config.OnInputFrame(frame)
		}

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			if config.OnPushError != nil {
				if cbErr := config.OnPushError(err); cbErr != nil {
					return cbErr
				}
				continue // callback returned nil = lenient, skip this frame
			}
			return err // nil callback = strict default
		}

		// Pull filtered frames
		if err := pullFrames(frame); err != nil {
			return err
		}
	}

	// Flush the filter graph by sending nil frame
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err != nil {
		if config.OnPushError != nil {
			if cbErr := config.OnPushError(err); cbErr != nil {
				return cbErr
			}
			return nil // flush push failed but callback swallowed it
		}
		return err // nil callback = strict default
	}

	// Drain remaining filtered frames
	return pullFrames(nil)
}

// setupFilterGraph creates and configures a complete filter graph with the given
// filter specification string. Returns the graph, source context, and sink context.
// The caller owns the returned filter graph and must free it.
func setupFilterGraph(decCtx *ffmpeg.AVCodecContext, filterSpec string) (
	*ffmpeg.AVFilterGraph,
	*ffmpeg.AVFilterContext,
	*ffmpeg.AVFilterContext,
	error,
) {
	filterGraph := ffmpeg.AVFilterGraphAlloc()
	if filterGraph == nil {
		return nil, nil, nil, fmt.Errorf("failed to allocate filter graph")
	}

	// Create abuffer source
	bufferSrcCtx, err := createBufferSource(filterGraph, decCtx)
	if err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, err
	}

	// Create abuffersink
	bufferSinkCtx, err := createBufferSink(filterGraph)
	if err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, err
	}

	// Parse filter graph
	outputs := ffmpeg.AVFilterInoutAlloc()
	inputs := ffmpeg.AVFilterInoutAlloc()
	defer ffmpeg.AVFilterInoutFree(&outputs)
	defer ffmpeg.AVFilterInoutFree(&inputs)

	outputs.SetName(ffmpeg.ToCStr("in"))
	outputs.SetFilterCtx(bufferSrcCtx)
	outputs.SetPadIdx(0)
	outputs.SetNext(nil)

	inputs.SetName(ffmpeg.ToCStr("out"))
	inputs.SetFilterCtx(bufferSinkCtx)
	inputs.SetPadIdx(0)
	inputs.SetNext(nil)

	filterSpecC := ffmpeg.ToCStr(filterSpec)
	defer filterSpecC.Free()

	if _, err := ffmpeg.AVFilterGraphParsePtr(filterGraph, filterSpecC, &inputs, &outputs, nil); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to parse filter graph: %w", err)
	}

	// Configure filter graph
	if _, err := ffmpeg.AVFilterGraphConfig(filterGraph, nil); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to configure filter graph: %w", err)
	}

	return filterGraph, bufferSrcCtx, bufferSinkCtx, nil
}

// createBufferSource creates and configures the abuffer source filter
func createBufferSource(filterGraph *ffmpeg.AVFilterGraph, decCtx *ffmpeg.AVCodecContext) (*ffmpeg.AVFilterContext, error) {
	bufferSrc := ffmpeg.AVFilterGetByName(ffmpeg.GlobalCStr("abuffer"))
	if bufferSrc == nil {
		return nil, fmt.Errorf("abuffer filter not found")
	}

	// Get channel layout description
	layoutPtr := ffmpeg.AllocCStr(64)
	defer layoutPtr.Free()

	if _, err := ffmpeg.AVChannelLayoutDescribe(decCtx.ChLayout(), layoutPtr, 64); err != nil {
		return nil, fmt.Errorf("failed to get channel layout: %w", err)
	}

	pktTimebase := decCtx.PktTimebase()
	args := fmt.Sprintf(
		"time_base=%d/%d:sample_rate=%d:sample_fmt=%s:channel_layout=%s",
		pktTimebase.Num(), pktTimebase.Den(),
		decCtx.SampleRate(),
		ffmpeg.AVGetSampleFmtName(decCtx.SampleFmt()).String(),
		layoutPtr.String(),
	)

	argsC := ffmpeg.ToCStr(args)
	defer argsC.Free()

	var bufferSrcCtx *ffmpeg.AVFilterContext
	if _, err := ffmpeg.AVFilterGraphCreateFilter(
		&bufferSrcCtx,
		bufferSrc,
		ffmpeg.GlobalCStr("in"),
		argsC,
		nil,
		filterGraph,
	); err != nil {
		return nil, fmt.Errorf("failed to create abuffer: %w", err)
	}

	return bufferSrcCtx, nil
}

// createBufferSink creates and configures the abuffersink filter
func createBufferSink(filterGraph *ffmpeg.AVFilterGraph) (*ffmpeg.AVFilterContext, error) {
	bufferSink := ffmpeg.AVFilterGetByName(ffmpeg.GlobalCStr("abuffersink"))
	if bufferSink == nil {
		return nil, fmt.Errorf("abuffersink filter not found")
	}

	var bufferSinkCtx *ffmpeg.AVFilterContext
	if _, err := ffmpeg.AVFilterGraphCreateFilter(
		&bufferSinkCtx,
		bufferSink,
		ffmpeg.GlobalCStr("out"),
		nil,
		nil,
		filterGraph,
	); err != nil {
		return nil, fmt.Errorf("failed to create abuffersink: %w", err)
	}

	return bufferSinkCtx, nil
}
