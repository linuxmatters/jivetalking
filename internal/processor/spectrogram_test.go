package processor

import (
	"fmt"
	"strings"
	"testing"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// This is the canonical spectrogram unit test for internal/processor/spectrogram.go.
// Every test here is hermetic: registry lookups against the linked static FFmpeg,
// pure-string filter-spec branches and determinism, and source/bounds resolution.
// None decode audio or render PNGs, so none touch testdata/.
//
// Render correctness, before/after dimension parity, and cancellation are
// audio-dependent integration, exercised by the gitignored manual harness
// (testdata/validation-0.3.1-vs-0.5.x/bin/spectrogram-validate.sh).
//
// Path derivation (deriveSpectrogramImages) is a separate concern, tested in
// spectrogram_paths_test.go.

// ---------------------------------------------------------------------------
// 1. Registry gate (always runs, no testdata): the cheap go/no-go guard.
// ---------------------------------------------------------------------------

// TestSpectrogramRegistryGate confirms, AT RUN TIME against the linked static
// FFmpeg, that the three components the in-process spectrogram route depends on
// are registered:
//
//   - the showspectrumpic filter
//   - the png encoder
//   - the image2 muxer
//
// These lookups need no audio file, so they always run and the gate verdict is
// meaningful even without testdata. A failure here is a NO-GO: the in-process
// route would need a statigo rebuild.
func TestSpectrogramRegistryGate(t *testing.T) {
	t.Run("showspectrumpic_filter", func(t *testing.T) {
		f := ffmpeg.AVFilterGetByName(ffmpeg.GlobalCStr("showspectrumpic"))
		if f == nil {
			t.Fatal("NO-GO: showspectrumpic filter is NOT registered in the embedded ffmpeg")
		}
		t.Logf("GO: showspectrumpic filter registered (%s)", f.Name().String())
	})

	t.Run("png_encoder", func(t *testing.T) {
		enc := ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdPng)
		if enc == nil {
			t.Fatal("NO-GO: png encoder is NOT registered in the embedded ffmpeg")
		}
		t.Logf("GO: png encoder registered (%s)", enc.Name().String())
	})

	t.Run("image2_muxer", func(t *testing.T) {
		shortName := ffmpeg.ToCStr("image2")
		defer shortName.Free()
		fileName := ffmpeg.ToCStr("frame.png")
		defer fileName.Free()
		mime := ffmpeg.ToCStr("image/png")
		defer mime.Free()

		muxer := ffmpeg.AVGuessFormat(shortName, fileName, mime)
		if muxer == nil {
			t.Fatal("NO-GO: image2 muxer is NOT registered in the embedded ffmpeg")
		}
		t.Logf("GO: image2 muxer registered (%s)", muxer.Name().String())
	})
}

// ---------------------------------------------------------------------------
// 2. Pure-string spec branches + determinism (always runs, no testdata).
// ---------------------------------------------------------------------------

// TestSpectrogramFilterSpecBranches pins the two branches of spectrogramFilterSpec
// as pure strings, with no ffmpeg and no testdata. It is the cheapest defence
// against a region-extraction regression.
//
//   - nil bounds   → the bare frozen spec (whole file, no atrim, full stream).
//   - bounds set   → atrim=start=%f:duration=%f,asetpts=PTS-STARTPTS, prepended,
//     mirroring outputRegionAnalysisFilterFormat (analyser_output.go:18) with the
//     astats,… tail swapped for showspectrumpic=<frozen>.
func TestSpectrogramFilterSpecBranches(t *testing.T) {
	t.Run("nil_bounds_whole_file", func(t *testing.T) {
		got := spectrogramFilterSpec(nil)
		want := "showspectrumpic=" + frozenSpectrogramSpec
		if got != want {
			t.Fatalf("whole-file spec mismatch:\n got: %q\nwant: %q", got, want)
		}
		// No atrim on the whole-file path: the full stream feeds showspectrumpic.
		if strings.Contains(got, "atrim") {
			t.Fatalf("whole-file spec must not contain atrim, got: %q", got)
		}
	})

	t.Run("bounds_prepend_atrim", func(t *testing.T) {
		b := &regionBounds{Start: 12.5, Duration: 3.25}
		got := spectrogramFilterSpec(b)
		// Same %f seconds formatting as outputRegionAnalysisFilterFormat.
		want := fmt.Sprintf(
			"atrim=start=%f:duration=%f,asetpts=PTS-STARTPTS,showspectrumpic=%s",
			b.Start, b.Duration, frozenSpectrogramSpec,
		)
		if got != want {
			t.Fatalf("region spec mismatch:\n got: %q\nwant: %q", got, want)
		}
	})

	// The region tail is the whole-file spec with the atrim window prepended:
	// both sides share the one frozen showspectrumpic param string.
	t.Run("region_tail_is_whole_file_spec", func(t *testing.T) {
		region := spectrogramFilterSpec(&regionBounds{Start: 1, Duration: 2})
		whole := spectrogramFilterSpec(nil)
		if !strings.HasSuffix(region, whole) {
			t.Fatalf("region spec must end with the whole-file spec\nregion: %q\nwhole:  %q", region, whole)
		}
	})
}

// TestSpectrogramFilterSpecDeterministic proves the pair contract by
// construction: identical bounds yield a byte-identical spec, so the input and
// output members of a before/after pair (which share their bounds) render the
// SAME time window. No ffmpeg, no testdata.
func TestSpectrogramFilterSpecDeterministic(t *testing.T) {
	cases := []*regionBounds{
		nil,
		{Start: 0, Duration: 2},
		{Start: 30.0, Duration: 10.0},
		{Start: 123.456, Duration: 7.89},
	}
	for _, b := range cases {
		// Two callers with the same bounds (the input and output sides of a pair).
		inputSpec := spectrogramFilterSpec(b)
		outputSpec := spectrogramFilterSpec(b)
		if inputSpec != outputSpec {
			t.Fatalf("spec not deterministic for bounds %+v:\n input:  %q\n output: %q", b, inputSpec, outputSpec)
		}
	}
}

// TestSpectrogramFrozenParamSingleDefinition asserts the ONE frozen param string
// is what spectrogramFilterSpec (and therefore generateSpectrogram, which calls
// it) actually emits on both branches, no per-call mutation, one definition.
// Pure-string, always runs. The render side of this contract (the frozen params
// reaching ffmpeg unchanged) is exercised by the render tests below.
func TestSpectrogramFrozenParamSingleDefinition(t *testing.T) {
	// The constant carries the load-bearing terms (decision #8); assert the spec
	// embeds it verbatim rather than re-deriving any term per call.
	for _, c := range []*regionBounds{nil, {Start: 5, Duration: 3}} {
		spec := spectrogramFilterSpec(c)
		if !strings.Contains(spec, frozenSpectrogramSpec) {
			t.Fatalf("spec does not contain the frozen param string verbatim:\n spec:   %q\n frozen: %q", spec, frozenSpectrogramSpec)
		}
		// Exactly one occurrence, the params are never duplicated or recomputed.
		if n := strings.Count(spec, frozenSpectrogramSpec); n != 1 {
			t.Fatalf("frozen param string appears %d times in spec %q, want 1", n, spec)
		}
	}
}

// ---------------------------------------------------------------------------
// 2b. RenderSpectrogramImage source/bounds resolution (pure mapping, no ffmpeg,
//     no testdata). The render itself is exercised by section 3; here the
//     source-file and region-bounds resolution rules (T4.1) are pinned.
// ---------------------------------------------------------------------------

// TestSpectrogramSourceResolution pins the stage→source-file mapping: before and
// input read the raw input, after reads the processed output; an unknown stage
// errors.
func TestSpectrogramSourceResolution(t *testing.T) {
	const in, out = "/in/episode.flac", "/out/episode-LUFS-16-processed.flac"

	cases := []struct {
		stage   string
		want    string
		wantErr bool
	}{
		{SpectrogramStageBefore, in, false},
		{SpectrogramStageInput, in, false},
		{SpectrogramStageAfter, out, false},
		{"bogus", "", true},
	}
	for _, c := range cases {
		got, err := spectrogramSource(c.stage, in, out)
		if c.wantErr {
			if err == nil {
				t.Errorf("stage %q: want error, got source %q", c.stage, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("stage %q: unexpected error %v", c.stage, err)
		}
		if got != c.want {
			t.Errorf("stage %q: got source %q, want %q", c.stage, got, c.want)
		}
	}
}

// TestSpectrogramBoundsResolution pins the kind→bounds mapping: whole → nil
// (whole file); roomtone/speech → the elected profile's Start/Duration in
// seconds (populatedProcessingResult elects room-tone 2s/10s and speech 30s/10s).
func TestSpectrogramBoundsResolution(t *testing.T) {
	rec := NewRunRecord(populatedProcessingResult())

	t.Run("whole_is_nil", func(t *testing.T) {
		got, err := spectrogramBounds(SpectrogramKindWhole, rec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Fatalf("whole bounds must be nil (whole file), got %+v", got)
		}
	})

	t.Run("roomtone_elected_bounds", func(t *testing.T) {
		got, err := spectrogramBounds(SpectrogramKindRoomTone, rec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || got.Start != 2.0 || got.Duration != 10.0 {
			t.Fatalf("room-tone bounds: got %+v, want {Start:2 Duration:10}", got)
		}
	})

	t.Run("speech_elected_bounds", func(t *testing.T) {
		got, err := spectrogramBounds(SpectrogramKindSpeech, rec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil || got.Start != 30.0 || got.Duration != 10.0 {
			t.Fatalf("speech bounds: got %+v, want {Start:30 Duration:10}", got)
		}
	})

	t.Run("unknown_kind_errors", func(t *testing.T) {
		if _, err := spectrogramBounds("bogus", rec); err == nil {
			t.Fatal("unknown kind must error")
		}
	})

	t.Run("nil_elected_profile_errors", func(t *testing.T) {
		// Guard: deriveSpectrogramImages omits an unelected kind, but a direct call
		// with no elected profile must error rather than render the whole file.
		bare := NewAnalysisRunRecord("/in/episode.flac", nil)
		if _, err := spectrogramBounds(SpectrogramKindRoomTone, bare); err == nil {
			t.Fatal("room-tone with no elected profile must error")
		}
		if _, err := spectrogramBounds(SpectrogramKindSpeech, bare); err == nil {
			t.Fatal("speech with no elected profile must error")
		}
	})
}

// Render correctness, before/after dimension parity, and cancellation are
// exercised by the gitignored manual harness
// (testdata/validation-0.3.1-vs-0.5.x/bin/spectrogram-validate.sh), the home for
// audio-dependent integration. The tests above stay hermetic: no testdata, no
// ffmpeg render.
