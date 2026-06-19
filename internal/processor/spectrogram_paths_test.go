package processor

import (
	"fmt"
	"strings"
	"testing"
)

// outputStem mirrors the processing stem derivation in pool.go:
// strings.TrimSuffix(OutputPath, filepath.Ext(OutputPath)). The derivation stores
// filepath.Base of this, so a directory prefix must not leak into the Path.
const testOutputStem = "/tmp/out/episode-LUFS-16-processed"

const testStemBase = "episode-LUFS-16-processed"

// wantImage is one expected (kind, stage) entry for assertImages.
type wantImage struct{ kind, stage string }

// assertImages checks the derived list against an expected (kind, stage) set,
// verifying each Path matches the exact suffix convention and carries no
// directory separator. stemBase is the expected filepath.Base of the stem.
func assertImages(t *testing.T, got []SpectrogramImage, stemBase string, want []wantImage) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d images, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		img := got[i]
		if img.Kind != w.kind || img.Stage != w.stage {
			t.Errorf("image %d: got kind=%q stage=%q, want kind=%q stage=%q",
				i, img.Kind, img.Stage, w.kind, w.stage)
		}
		wantPath := fmt.Sprintf("%s.spectrogram-%s-%s.png", stemBase, w.kind, w.stage)
		if img.Path != wantPath {
			t.Errorf("image %d: got path %q, want %q", i, img.Path, wantPath)
		}
		if strings.ContainsRune(img.Path, '/') {
			t.Errorf("image %d: path %q is not relative (contains a separator)", i, img.Path)
		}
	}
}

// TestDeriveSpectrogramImages_ProcessingBothRegions: full processing record with
// both regions elected → 6 entries (3 kinds × before/after).
func TestDeriveSpectrogramImages_ProcessingBothRegions(t *testing.T) {
	rec := NewRunRecord(populatedProcessingResult())
	got := deriveSpectrogramImages(rec, testOutputStem, ProcessingSpectrogramStages)

	assertImages(t, got, testStemBase, []wantImage{
		{kind: SpectrogramKindWhole, stage: SpectrogramStageBefore},
		{kind: SpectrogramKindWhole, stage: SpectrogramStageAfter},
		{kind: SpectrogramKindRoomTone, stage: SpectrogramStageBefore},
		{kind: SpectrogramKindRoomTone, stage: SpectrogramStageAfter},
		{kind: SpectrogramKindSpeech, stage: SpectrogramStageBefore},
		{kind: SpectrogramKindSpeech, stage: SpectrogramStageAfter},
	})
}

// TestDeriveSpectrogramImages_StemBasename proves the stored Path is always the
// relative basename of the stem - a different directory prefix yields the same
// Path strings (filepath.Base strips the directory).
func TestDeriveSpectrogramImages_StemBasename(t *testing.T) {
	rec := NewAnalysisRunRecord("/in/episode.flac", populatedAudioMeasurements())
	got := deriveSpectrogramImages(rec, "/srv/podcasts/2026/show-LUFS-16-processed", AnalysisSpectrogramStages)
	assertImages(t, got, "show-LUFS-16-processed", []wantImage{
		{kind: SpectrogramKindWhole, stage: SpectrogramStageInput},
		{kind: SpectrogramKindRoomTone, stage: SpectrogramStageInput},
		{kind: SpectrogramKindSpeech, stage: SpectrogramStageInput},
	})
}

// TestDeriveSpectrogramImages_NoRoomTone: record missing the room-tone profile →
// roomtone pair absent, whole+speech present (4 entries). Proves all-or-nothing
// per kind (no half-pair).
func TestDeriveSpectrogramImages_NoRoomTone(t *testing.T) {
	result := populatedProcessingResult()
	// Drop the elected room-tone profile (the speech profile stays elected).
	result.Measurements.Regions.NoiseProfile = nil

	rec := NewRunRecord(result)
	got := deriveSpectrogramImages(rec, testOutputStem, ProcessingSpectrogramStages)

	assertImages(t, got, testStemBase, []wantImage{
		{kind: SpectrogramKindWhole, stage: SpectrogramStageBefore},
		{kind: SpectrogramKindWhole, stage: SpectrogramStageAfter},
		{kind: SpectrogramKindSpeech, stage: SpectrogramStageBefore},
		{kind: SpectrogramKindSpeech, stage: SpectrogramStageAfter},
	})
}

// TestDeriveSpectrogramImages_AnalysisOnly: analysis-only stage set (input) with
// both regions → 3 entries (one input image per kind, no "after").
func TestDeriveSpectrogramImages_AnalysisOnly(t *testing.T) {
	rec := NewAnalysisRunRecord("/in/episode.flac", populatedAudioMeasurements())
	got := deriveSpectrogramImages(rec, testOutputStem, AnalysisSpectrogramStages)

	assertImages(t, got, testStemBase, []wantImage{
		{kind: SpectrogramKindWhole, stage: SpectrogramStageInput},
		{kind: SpectrogramKindRoomTone, stage: SpectrogramStageInput},
		{kind: SpectrogramKindSpeech, stage: SpectrogramStageInput},
	})
}

// TestDeriveSpectrogramImages_WholeOnly: no region elected beyond whole-file → a
// single whole pair (processing) and a single whole input (analysis-only).
func TestDeriveSpectrogramImages_WholeOnly(t *testing.T) {
	result := populatedProcessingResult()
	result.Measurements.Regions.NoiseProfile = nil
	result.Measurements.Regions.SpeechProfile = nil

	rec := NewRunRecord(result)
	got := deriveSpectrogramImages(rec, testOutputStem, ProcessingSpectrogramStages)
	assertImages(t, got, testStemBase, []wantImage{
		{kind: SpectrogramKindWhole, stage: SpectrogramStageBefore},
		{kind: SpectrogramKindWhole, stage: SpectrogramStageAfter},
	})

	got = deriveSpectrogramImages(rec, testOutputStem, AnalysisSpectrogramStages)
	assertImages(t, got, testStemBase, []wantImage{
		{kind: SpectrogramKindWhole, stage: SpectrogramStageInput},
	})
}

// TestDeriveSpectrogramImages_NilRegions: a record with no Regions block (nil) →
// whole-file only, no panic.
func TestDeriveSpectrogramImages_NilRegions(t *testing.T) {
	rec := NewAnalysisRunRecord("/in/episode.flac", nil)
	got := deriveSpectrogramImages(rec, testOutputStem, ProcessingSpectrogramStages)
	assertImages(t, got, testStemBase, []wantImage{
		{kind: SpectrogramKindWhole, stage: SpectrogramStageBefore},
		{kind: SpectrogramKindWhole, stage: SpectrogramStageAfter},
	})
}
