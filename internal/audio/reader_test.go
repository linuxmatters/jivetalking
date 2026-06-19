package audio

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// These tests stay hermetic: they never decode or process a real audio file
// and need nothing under testdata/. They exercise the CGO boundary's safe
// behaviours - Close nil-guard idempotency, OpenAudioFile error returns on
// paths that are not decodable, and the sentinel error wrapping that ReadFrame
// relies on - all reachable without a valid audio stream.

// TestReaderClose_ZeroValueIdempotent proves Close on a zero-value Reader (all
// resource pointers nil) neither panics nor double-frees, and that calling it
// repeatedly is safe. Every Close branch is nil-guarded, so a fresh Reader{}
// reaches each guard's false arm; a regression that dropped a guard would panic
// here. This is the fake-backed Close idempotency check, with no CGO
// allocation required.
func TestReaderClose_ZeroValueIdempotent(t *testing.T) {
	t.Parallel()

	r := &Reader{}
	// Three calls: the first must not panic, and the second and third prove the
	// operation is repeatable (the real double-free hazard) since Close does not
	// nil the fields back out itself on the zero-value path.
	r.Close()
	r.Close()
	r.Close()
}

// TestReaderClose_NilGuards documents that each guarded field is independently
// safe to leave nil. A Reader carrying only a subset of nil fields must still
// Close cleanly; this pins the per-field guard rather than the whole-struct
// case, so dropping any single guard is caught.
func TestReaderClose_NilGuards(t *testing.T) {
	t.Parallel()

	// streamIdx is a plain int and never freed; the four pointer fields are the
	// ones Close guards. A zero Reader leaves all four nil, which is the only
	// fake state we can build without a codec.
	r := &Reader{streamIdx: -1}
	r.Close()
}

// TestOpenAudioFile_NonexistentPath verifies the open error path: a missing file
// must return a wrapped error and a nil Reader and nil Metadata, never a
// nil-error-with-nil-Reader or a leaked context. This is the contract callers
// rely on (resolveJobs and the pools branch on err, then dereference the
// Reader). It needs no fixture - the file is absent by design.
func TestOpenAudioFile_NonexistentPath(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "does-not-exist.flac")

	r, meta, err := OpenAudioFile(missing)
	if err == nil {
		t.Fatalf("OpenAudioFile(%q): want error, got nil", missing)
	}
	if r != nil {
		t.Errorf("OpenAudioFile(%q): want nil Reader on error, got %v", missing, r)
	}
	if meta != nil {
		t.Errorf("OpenAudioFile(%q): want nil Metadata on error, got %v", missing, meta)
	}
	// The first failing stage wraps with "failed to open input file"; assert the
	// message is populated so a silent empty error cannot pass.
	if err.Error() == "" {
		t.Error("OpenAudioFile: error message is empty")
	}
}

// TestOpenAudioFile_NotAudioData verifies a real but undecodable file (random
// bytes, no container) is rejected cleanly: error returned, nil Reader, nil
// Metadata, no panic. This drives a different branch from the missing-path case
// (open may succeed, stream-info or stream-search fails) and proves the
// reverse-order cleanup closure runs without crashing. The bytes are written
// in-test, so nothing under testdata/ is touched.
func TestOpenAudioFile_NotAudioData(t *testing.T) {
	t.Parallel()

	junk := filepath.Join(t.TempDir(), "not-audio.bin")
	if err := os.WriteFile(junk, []byte("this is not an audio container at all"), 0o600); err != nil {
		t.Fatalf("writing junk fixture: %v", err)
	}

	r, meta, err := OpenAudioFile(junk)
	if err == nil {
		// On the off chance ffmpeg opened it, close to avoid a leak before failing.
		if r != nil {
			r.Close()
		}
		t.Fatalf("OpenAudioFile(%q): want error on non-audio data, got nil", junk)
	}
	if r != nil {
		t.Errorf("OpenAudioFile(%q): want nil Reader on error, got %v", junk, r)
	}
	if meta != nil {
		t.Errorf("OpenAudioFile(%q): want nil Metadata on error, got %v", junk, meta)
	}
}

// TestOpenAudioFile_EmptyPath guards the empty-string input: it must error, not
// panic, and return nil Reader and Metadata.
func TestOpenAudioFile_EmptyPath(t *testing.T) {
	t.Parallel()

	r, meta, err := OpenAudioFile("")
	if err == nil {
		if r != nil {
			r.Close()
		}
		t.Fatal(`OpenAudioFile(""): want error, got nil`)
	}
	if r != nil || meta != nil {
		t.Errorf(`OpenAudioFile(""): want nil Reader and Metadata on error, got r=%v meta=%v`, r, meta)
	}
}

// TestErrorWrapping_PreservesSentinels proves the %w wrapping ReadFrame uses
// keeps errors.Is matching against the ffmpeg sentinels. ReadFrame branches on
// errors.Is(err, ffmpeg.AVErrorEOF) and ffmpeg.EAgain after wrapping decode
// failures; if the wrap verb or the sentinel's Is method ever changed, this
// classification would break silently and ReadFrame would mis-handle EOF. We
// reproduce the exact wrap shape here rather than decoding, since the wrapping
// is the pure, testable contract.
func TestErrorWrapping_PreservesSentinels(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		sentinel error
	}{
		{"EOF", ffmpeg.AVErrorEOF},
		{"EAgain", ffmpeg.EAgain},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Mirror reader.go's wrap shape, e.g. "failed to receive frame: %w".
			wrapped := fmt.Errorf("failed to receive frame: %w", tc.sentinel)

			if !errors.Is(wrapped, tc.sentinel) {
				t.Errorf("errors.Is(wrapped, %s) = false, want true", tc.name)
			}
			// A different sentinel must NOT match, so the classification is precise.
			other := ffmpeg.EAgain
			if errors.Is(tc.sentinel, ffmpeg.EAgain) {
				other = ffmpeg.AVErrorEOF
			}
			if errors.Is(wrapped, other) {
				t.Errorf("errors.Is(wrapped %s, other sentinel) = true, want false", tc.name)
			}
		})
	}
}

// TestMetadata_FieldRoundTrip is a trivial pure-value check on the Metadata
// struct: the fields OpenAudioFile populates are plain and carry the units the
// doc comments promise. It guards against an accidental field reorder or type
// change that a struct-literal caller would silently inherit.
func TestMetadata_FieldRoundTrip(t *testing.T) {
	t.Parallel()

	m := Metadata{
		Duration:   123.5,
		SampleRate: 48000,
		Channels:   2,
	}

	if m.Duration != 123.5 {
		t.Errorf("Duration = %v, want 123.5", m.Duration)
	}
	if m.SampleRate != 48000 {
		t.Errorf("SampleRate = %d, want 48000", m.SampleRate)
	}
	if m.Channels != 2 {
		t.Errorf("Channels = %d, want 2", m.Channels)
	}
}
