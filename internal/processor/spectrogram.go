// Package processor: spectrogram generation (Stage 3, AUDIO-MEASUREMENTS.md §7.2).
//
// This file holds the frozen showspectrumpic parameter string and the
// audio-decode → showspectrumpic → PNG generation function. The honesty contract
// is ONE frozen parameter string, applied identically to both sides of every
// before/after pair: s, gain, and the start/stop frequency range are LITERAL,
// never computed per file or per duration.
package processor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/ffmpeg-statigo/av"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// frozenSpectrogramSpec is the single, frozen showspectrumpic parameter string for
// every spectrogram image. It is applied identically to both sides of every
// before/after pair, the honest-comparison contract. It is derived from the
// showspectrumpic example in docs/Spectral-Metrics-Reference.md.
//
// Why each term is load-bearing:
//   - s=1024x512   fixed pixel dimensions across before/after (the §7.2 hard
//     requirement); the SAME constant for every image, never derived from duration.
//   - scale=log    fixed magnitude/dB mapping → equal colour-to-dB key both sides.
//   - fscale=log   fixed frequency-axis mapping → a frequency reads at the same Y.
//   - start=20 / stop=20000  fixed frequency range; without it ffmpeg's auto-range
//     could differ between full-band input and band-limited output and break the axis.
//   - gain=1       no per-image auto-gain that would silently re-reference the scale.
//   - color=intensity  same palette both sides.
//
// legend=1 renders a FIXED 0 → -117 dBFS colour-to-dB scale, NOT a per-image
// auto-scaled one: a real before/after pair (raw input vs its -LUFS-NN-processed
// output) produces byte-identical legend strips, so the dB key matches across the
// pair. gain=1 pins the magnitude reference and the legend reads off it; no
// separate magnitude-pinning term is needed.
//
// One definition, never mutated or derived per call.
const frozenSpectrogramSpec = "s=1024x512:scale=log:fscale=log:start=20:stop=20000:gain=1:color=intensity:legend=1"

// regionBounds is an optional time window for a region spectrogram. Both fields
// are in SECONDS, matching the atrim=start=%f:duration=%f precedent in
// outputRegionAnalysisFilterFormat (analyser_output.go) and the record's Elected
// profile _s float seconds. A nil *regionBounds means the whole-file path (no
// atrim, full stream).
type regionBounds struct {
	Start    float64
	Duration float64
}

// spectrogramFilterSpec builds the showspectrumpic filter spec for a graph.
// With nil bounds it is the bare frozen spec (whole file). With bounds, it
// prepends an atrim window, mirroring outputRegionAnalysisFilterFormat:
//
//	atrim=start=%f:duration=%f,asetpts=PTS-STARTPTS,showspectrumpic=<frozen>
//
// The leading filter name showspectrumpic= is added here so frozenSpectrogramSpec
// stays a pure parameter string (the honesty contract is the params, not the
// filter name).
func spectrogramFilterSpec(bounds *regionBounds) string {
	if bounds == nil {
		return "showspectrumpic=" + frozenSpectrogramSpec
	}
	return fmt.Sprintf(
		"atrim=start=%f:duration=%f,asetpts=PTS-STARTPTS,showspectrumpic=%s",
		bounds.Start, bounds.Duration, frozenSpectrogramSpec,
	)
}

// generateSpectrogram renders one showspectrumpic PNG for inputPath to pngPath.
// With nil bounds it renders the whole file; with bounds it renders that time
// window. It runs ONE audio-decode → mixed-media filter graph → single-frame pull
// → PNG encode/mux per call.
//
// The audio-in/video-out graph is hand-wired through a single AVFilterGraphParsePtr
// pass: an abuffer audio source feeds the spec into a hand-allocated video
// buffersink whose pix_fmts is pinned to rgb24 before init. showspectrumpic emits
// its single frame at EOF, so the loop drives the decode, flushes with a nil
// frame, then pulls the one frame.
//
// ctx discipline: the decode/push loop checks ctx.Err(); on cancellation (or any
// failure) the partial pngPath is removed best-effort so no residue survives. Any
// failure returns a non-nil error so the caller decides fatality.
func generateSpectrogram(ctx context.Context, inputPath string, bounds *regionBounds, pngPath string) (err error) {
	// Remove any partial output on failure or cancellation (best-effort).
	defer func() {
		if err != nil {
			_ = os.Remove(pngPath)
		}
	}()

	reader, _, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		return fmt.Errorf("open audio file: %w", err)
	}
	defer reader.Close()

	// For a region render, skip the pre-region span: seek the demuxer near the
	// region before decoding rather than decoding from frame 0 and letting atrim
	// discard everything ahead of start. The atrim window stays region-absolute,
	// so the rendered PNG is unchanged (see regionSeekPreRoll). The whole-file
	// path (bounds == nil) has no atrim and must decode from frame 0, so it stays
	// seek-free. A nil debugLogger is a no-op sink for the non-fatal seek warning.
	if bounds != nil {
		var log debugLogger
		seekReaderBeforeRegion(reader, time.Duration(bounds.Start*float64(time.Second)), log)
	}

	graph, srcCtx, sinkCtx, err := setupSpectrumGraph(reader.DecoderContext(), spectrogramFilterSpec(bounds))
	if err != nil {
		return err
	}
	defer ffmpeg.AVFilterGraphFree(&graph)

	frame, err := pullSpectrumFrame(ctx, reader, srcCtx, sinkCtx)
	if err != nil {
		return err
	}
	defer ffmpeg.AVFrameFree(&frame)

	return writeSpectrumPNG(frame, pngPath)
}

// RenderSpectrogramImage renders one SpectrogramImage from a run to disk. It is
// the exported per-image entry the pool (cmd/jivetalking) calls in a background
// goroutine, keeping the source/bounds/dest resolution INSIDE this package, which
// owns the record, the kind/stage constants, and the elected-region bounds.
//
// Resolution:
//   - source: Stage before/input → inputPath; Stage after → outputPath.
//   - bounds: Kind whole → nil (whole file); Kind roomtone/speech → the elected
//     profile's Start/Duration as seconds. deriveSpectrogramImages already omits a
//     kind with no elected profile, so a nil profile here is an unexpected state
//     and returns a non-nil error rather than silently rendering the whole file.
//   - dest: filepath.Join(destDir, img.Path) (img.Path is the relative basename).
//
// It delegates the actual render to generateSpectrogram, so ctx cancellation
// aborts the decode and removes the partial PNG, and any failure returns non-nil
// for the caller to surface as a non-fatal warning.
func RenderSpectrogramImage(ctx context.Context, img SpectrogramImage, rec *RunRecord, inputPath, outputPath, destDir string) error {
	source, err := spectrogramSource(img.Stage, inputPath, outputPath)
	if err != nil {
		return err
	}

	bounds, err := spectrogramBounds(img.Kind, rec)
	if err != nil {
		return err
	}

	return generateSpectrogram(ctx, source, bounds, filepath.Join(destDir, img.Path))
}

// spectrogramSource maps a stage to its source audio file: the before/input
// stages read the raw input, the after stage reads the processed output.
func spectrogramSource(stage, inputPath, outputPath string) (string, error) {
	switch stage {
	case SpectrogramStageBefore, SpectrogramStageInput:
		return inputPath, nil
	case SpectrogramStageAfter:
		return outputPath, nil
	default:
		return "", fmt.Errorf("unknown spectrogram stage %q", stage)
	}
}

// spectrogramBounds maps a kind to its region window: whole is the whole file
// (nil bounds), roomtone/speech are the elected profile's Start/Duration in
// seconds. A nil elected profile is unexpected (deriveSpectrogramImages omits
// such kinds) and returns an error.
func spectrogramBounds(kind string, rec *RunRecord) (*regionBounds, error) {
	switch kind {
	case SpectrogramKindWhole:
		return nil, nil //nolint:nilnil // whole-file: nil bounds is the success signal
	case SpectrogramKindRoomTone:
		return boundsForProfile(rec, "room-tone", func() *regionBounds {
			p := rec.Regions.RoomTone.ElectedProfile()
			if p == nil {
				return nil
			}
			return &regionBounds{Start: p.Start.Seconds(), Duration: p.Duration.Seconds()}
		})
	case SpectrogramKindSpeech:
		return boundsForProfile(rec, "speech", func() *regionBounds {
			p := rec.Regions.Speech.ElectedProfile()
			if p == nil {
				return nil
			}
			return &regionBounds{Start: p.Region.Start.Seconds(), Duration: p.Region.Duration.Seconds()}
		})
	default:
		return nil, fmt.Errorf("unknown spectrogram kind %q", kind)
	}
}

// boundsForProfile holds the shared region-spectrogram guards: it checks the
// record carries regions, then calls extract to fetch the elected profile's
// bounds, treating a nil result as the no-elected-profile error. label names the
// kind in both error messages.
func boundsForProfile(rec *RunRecord, label string, extract func() *regionBounds) (*regionBounds, error) {
	if rec == nil || rec.Regions == nil {
		return nil, fmt.Errorf("%s spectrogram requested but no regions on record", label)
	}
	bounds := extract()
	if bounds == nil {
		return nil, fmt.Errorf("%s spectrogram requested but no elected profile", label)
	}
	return bounds, nil
}

// setupSpectrumGraph builds the mixed-media graph: an abuffer audio source
// (built like createBufferSource) feeds spec into a hand-allocated video
// buffersink whose pixel format is pinned to rgb24 (the png encoder's input
// format) before AVFilterInitStr. The whole spec is parsed through one
// AVFilterGraphParsePtr pass, no need to drop to FilterGraph.Raw(). The caller
// owns the returned graph and must free it.
func setupSpectrumGraph(decCtx *ffmpeg.AVCodecContext, spec string) (
	graph *ffmpeg.AVFilterGraph,
	src, sink *ffmpeg.AVFilterContext,
	err error,
) {
	graph = ffmpeg.AVFilterGraphAlloc()
	if graph == nil {
		return nil, nil, nil, fmt.Errorf("allocate filter graph")
	}

	src, err = createBufferSource(graph, decCtx)
	if err != nil {
		ffmpeg.AVFilterGraphFree(&graph)
		return nil, nil, nil, err
	}

	// Video buffersink: allocated uninitialised so pix_fmts can be set before init.
	sinkFilter := ffmpeg.AVFilterGetByName(ffmpeg.GlobalCStr("buffersink"))
	if sinkFilter == nil {
		ffmpeg.AVFilterGraphFree(&graph)
		return nil, nil, nil, fmt.Errorf("buffersink filter not found")
	}

	sink = ffmpeg.AVFilterGraphAllocFilter(graph, sinkFilter, ffmpeg.GlobalCStr("out"))
	if sink == nil {
		ffmpeg.AVFilterGraphFree(&graph)
		return nil, nil, nil, fmt.Errorf("allocate buffersink")
	}

	pixFmts := []ffmpeg.AVPixelFormat{ffmpeg.AVPixFmtRgb24}
	if _, e := ffmpeg.AVOptSetSlice(sink.RawPtr(), ffmpeg.GlobalCStr("pix_fmts"), pixFmts, ffmpeg.AVOptSearchChildren); e != nil {
		ffmpeg.AVFilterGraphFree(&graph)
		return nil, nil, nil, fmt.Errorf("set buffersink pix_fmts: %w", e)
	}

	if _, e := ffmpeg.AVFilterInitStr(sink, nil); e != nil {
		ffmpeg.AVFilterGraphFree(&graph)
		return nil, nil, nil, fmt.Errorf("init buffersink: %w", e)
	}

	if e := wireSpectrumGraph(graph, src, sink, spec); e != nil {
		ffmpeg.AVFilterGraphFree(&graph)
		return nil, nil, nil, e
	}

	return graph, src, sink, nil
}

// wireSpectrumGraph parses spec linking src→sink and configures the graph,
// mirroring setupFilterGraph's inout wiring in frame_processor.go.
func wireSpectrumGraph(graph *ffmpeg.AVFilterGraph, src, sink *ffmpeg.AVFilterContext, spec string) error {
	outputs := ffmpeg.AVFilterInoutAlloc()
	inputs := ffmpeg.AVFilterInoutAlloc()
	defer ffmpeg.AVFilterInoutFree(&outputs)
	defer ffmpeg.AVFilterInoutFree(&inputs)

	outputs.SetName(ffmpeg.ToCStr("in"))
	outputs.SetFilterCtx(src)
	outputs.SetPadIdx(0)
	outputs.SetNext(nil)

	inputs.SetName(ffmpeg.ToCStr("out"))
	inputs.SetFilterCtx(sink)
	inputs.SetPadIdx(0)
	inputs.SetNext(nil)

	specC := ffmpeg.ToCStr(spec)
	defer specC.Free()

	if _, err := ffmpeg.AVFilterGraphParsePtr(graph, specC, &inputs, &outputs, nil); err != nil {
		return fmt.Errorf("parse filter graph: %w", err)
	}

	if _, err := ffmpeg.AVFilterGraphConfig(graph, nil); err != nil {
		return fmt.Errorf("configure filter graph: %w", err)
	}

	return nil
}

// pullSpectrumFrame drives the read/push loop until the graph yields the single
// video frame showspectrumpic emits at EOF, returning a cloned frame the caller
// owns. It checks ctx.Err() each iteration so cancellation aborts the decode.
func pullSpectrumFrame(ctx context.Context, reader *audio.Reader, src, sink *ffmpeg.AVFilterContext) (*ffmpeg.AVFrame, error) {
	scratch := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&scratch)

	pull := func() (*ffmpeg.AVFrame, error) {
		if _, err := ffmpeg.AVBuffersinkGetFrame(sink, scratch); err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				return nil, nil //nolint:nilnil // EAGAIN/EOF: nothing ready yet
			}
			return nil, fmt.Errorf("read rendered video frame: %w", err)
		}
		out := ffmpeg.AVFrameClone(scratch)
		ffmpeg.AVFrameUnref(scratch)
		if out == nil {
			return nil, fmt.Errorf("clone rendered video frame")
		}
		return out, nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		frame, err := reader.ReadFrame()
		if err != nil {
			return nil, fmt.Errorf("read frame: %w", err)
		}
		if frame == nil {
			break // EOF
		}
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(src, frame, 0); err != nil {
			return nil, fmt.Errorf("push frame: %w", err)
		}
		if out, err := pull(); err != nil || out != nil {
			return out, err
		}
	}

	// Flush: showspectrumpic renders its image at EOF.
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(src, nil, 0); err != nil {
		return nil, fmt.Errorf("flush source: %w", err)
	}
	out, err := pull()
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("showspectrumpic produced no video frame")
	}
	return out, nil
}

// writeSpectrumPNG encodes one video frame to a single-frame PNG via the av/
// layer's png encoder and image2 muxer.
func writeSpectrumPNG(frame *ffmpeg.AVFrame, pngPath string) error {
	enc, err := av.NewEncoderByID(ffmpeg.AVCodecIdPng, func(c *ffmpeg.AVCodecContext) {
		c.SetWidth(frame.Width())
		c.SetHeight(frame.Height())
		c.SetPixFmt(ffmpeg.AVPixelFormat(frame.Format())) //nolint:gosec // pixel-format enum, no overflow
		c.SetTimeBase(ffmpeg.AVMakeQ(1, 25))
	})
	if err != nil {
		return fmt.Errorf("create png encoder: %w", err)
	}
	defer func() { _ = enc.Close() }()

	out, err := av.CreateOutput(pngPath)
	if err != nil {
		return fmt.Errorf("create output: %w", err)
	}
	defer func() { _ = out.Close() }()

	// image2 writes an image sequence by default; "update=1" tells it to write a
	// single still to the literal filename (no %d pattern), else it warns.
	updateC := ffmpeg.ToCStr("1")
	defer updateC.Free()
	if _, e := ffmpeg.AVOptSet(out.Raw().PrivData(), ffmpeg.GlobalCStr("update"), updateC, 0); e != nil {
		return fmt.Errorf("set image2 update: %w", e)
	}

	stream, err := out.AddStream(enc)
	if err != nil {
		return fmt.Errorf("add stream: %w", err)
	}

	if err := out.WriteHeader(); err != nil {
		return fmt.Errorf("write header: %w", err)
	}

	frame.SetPts(0)
	if err := enc.Encode(frame, func(pkt *ffmpeg.AVPacket) error {
		pkt.SetStreamIndex(stream.Index())
		return out.WritePacket(pkt)
	}); err != nil {
		return fmt.Errorf("encode frame: %w", err)
	}

	if err := enc.Flush(func(pkt *ffmpeg.AVPacket) error {
		pkt.SetStreamIndex(stream.Index())
		return out.WritePacket(pkt)
	}); err != nil {
		return fmt.Errorf("flush encoder: %w", err)
	}

	return out.WriteTrailer()
}
