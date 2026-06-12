//go:build integration

package processor

import (
	"context"
	"errors"
	"os"
	"testing"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// openTestFilterGraph opens a real testdata audio file and builds a passthrough
// (anull) filter graph for driving runFilterGraph. It skips the test gracefully
// when no testdata audio is present (project convention). The returned cleanup
// frees the graph and closes the reader.
func openTestFilterGraph(t *testing.T) (
	reader *audio.Reader,
	src, sink *ffmpeg.AVFilterContext,
	cleanup func(),
) {
	t.Helper()

	inputPath := findProbeAudioFile()
	if inputPath == "" {
		t.Skip("no audio file found under testdata/; drop a .flac (e.g. testdata/fixture-5m.flac) to run this test")
	}
	if _, err := os.Stat(inputPath); os.IsNotExist(err) {
		t.Skipf("testdata audio not found: %s", inputPath)
	}

	r, _, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		t.Fatalf("failed to open test audio %s: %v", inputPath, err)
	}

	graph, srcCtx, sinkCtx, err := setupFilterGraph(r.GetDecoderContext(), "anull")
	if err != nil {
		r.Close()
		t.Fatalf("failed to create filter graph: %v", err)
	}

	return r, srcCtx, sinkCtx, func() {
		ffmpeg.AVFilterGraphFree(&graph)
		r.Close()
	}
}

// TestRunFilterGraphContextCancellation proves runFilterGraph honours ctx
// cancellation promptly: it returns context.Canceled before draining the whole
// file rather than only after EOF.
func TestRunFilterGraphContextCancellation(t *testing.T) {
	// Baseline: count total input frames in the file so "promptly" can be
	// asserted as a frame-count bound well below the full length.
	totalFrames := func() int {
		reader, src, sink, cleanup := openTestFilterGraph(t)
		defer cleanup()

		var count int
		err := runFilterGraph(context.Background(), reader, src, sink, FrameLoopConfig{
			OnInputFrame: func(_ *ffmpeg.AVFrame) { count++ },
		})
		if err != nil {
			t.Fatalf("baseline runFilterGraph failed: %v", err)
		}
		if count == 0 {
			t.Fatal("baseline read zero frames; cannot assert a cancellation bound")
		}
		return count
	}()
	t.Logf("baseline total input frames: %d", totalFrames)

	t.Run("pre-cancelled ctx processes ~0 frames", func(t *testing.T) {
		reader, src, sink, cleanup := openTestFilterGraph(t)
		defer cleanup()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		var processed int
		err := runFilterGraph(ctx, reader, src, sink, FrameLoopConfig{
			OnInputFrame: func(_ *ffmpeg.AVFrame) { processed++ },
		})

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		// The ctx.Err() check sits at the top of the read loop, before the
		// first ReadFrame, so a pre-cancelled ctx must process zero frames.
		if processed != 0 {
			t.Errorf("processed %d frames with pre-cancelled ctx, want 0", processed)
		}
	})

	t.Run("ctx cancelled after N frames stops well before EOF", func(t *testing.T) {
		reader, src, sink, cleanup := openTestFilterGraph(t)
		defer cleanup()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		const cancelAfter = 5
		var processed int
		err := runFilterGraph(ctx, reader, src, sink, FrameLoopConfig{
			OnInputFrame: func(_ *ffmpeg.AVFrame) {
				processed++
				if processed == cancelAfter {
					cancel()
				}
			},
		})

		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		// After cancel() fires inside OnInputFrame at frame N, the loop pushes
		// and pulls that frame, then re-checks ctx.Err() at the top of the next
		// iteration and returns. At most one extra frame is processed beyond N.
		if processed > cancelAfter+1 {
			t.Errorf("processed %d frames after cancel-at-%d, want <= %d",
				processed, cancelAfter, cancelAfter+1)
		}
		// Prompt stop: must abort well before draining the whole file.
		if processed >= totalFrames {
			t.Errorf("processed %d frames; cancellation did not stop before EOF (total %d)",
				processed, totalFrames)
		}
		t.Logf("cancelled after %d frames; processed %d of %d total", cancelAfter, processed, totalFrames)
	})
}
