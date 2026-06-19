package processor

import "path/filepath"

// Spectrogram kind and stage constants. The path suffix convention is
// `<stem-basename>.spectrogram-<kind>-<stage>.png`. These are the single source of
// truth for the strings the renderer and generator reuse - no scattered literals.
const (
	// SpectrogramKindWhole is the whole-file spectrogram, always rendered.
	SpectrogramKindWhole = "whole"
	// SpectrogramKindRoomTone is the elected room-tone region spectrogram.
	SpectrogramKindRoomTone = "roomtone"
	// SpectrogramKindSpeech is the elected speech region spectrogram.
	SpectrogramKindSpeech = "speech"

	// SpectrogramStageBefore is the raw input stage of a processing pair.
	SpectrogramStageBefore = "before"
	// SpectrogramStageAfter is the processed output stage of a processing pair.
	SpectrogramStageAfter = "after"
	// SpectrogramStageInput is the single analysis-only stage (no output exists).
	SpectrogramStageInput = "input"
)

// ProcessingSpectrogramStages is the stage set for a processing run: a
// before/after pair per kind.
var ProcessingSpectrogramStages = []string{SpectrogramStageBefore, SpectrogramStageAfter}

// AnalysisSpectrogramStages is the stage set for an analysis-only run: a single
// input image per kind (no processed output exists).
var AnalysisSpectrogramStages = []string{SpectrogramStageInput}

// SpectrogramImage is one entry in the record-carried spectrogram list (§8.4
// snake_case tags). Path is a RELATIVE basename (no directory) that resolves
// beside the .md/.json report.
type SpectrogramImage struct {
	Kind  string `json:"kind"`
	Stage string `json:"stage"`
	Path  string `json:"path"`
}

// DeriveSpectrogramImages is the exported wrapper over the pure
// deriveSpectrogramImages: it returns the deterministic image list for a run so
// the pool (cmd/jivetalking, outside this package) can attach it to the record
// synchronously - pure string work, no ffmpeg. stages is one of
// ProcessingSpectrogramStages or AnalysisSpectrogramStages.
func DeriveSpectrogramImages(rec *RunRecord, outputStem string, stages []string) []SpectrogramImage {
	return deriveSpectrogramImages(rec, outputStem, stages)
}

// deriveSpectrogramImages returns the deterministic spectrogram image list for a
// run. It is PURE: no I/O, no ffmpeg - only the record's elected-region presence
// and the output stem drive the result. The renderer links these paths and the
// generator fills them.
//
// Rules:
//   - whole: always present.
//   - roomtone: only when a room-tone profile is elected.
//   - speech: only when a speech profile is elected.
//   - one image per stage in `stages` for each present kind (all-or-nothing per
//     kind - never a half-pair).
//
// outputStem is the report/output stem (e.g. ".../episode-LUFS-16-processed"); the
// stored Path uses filepath.Base(outputStem) so it stays relative.
func deriveSpectrogramImages(rec *RunRecord, outputStem string, stages []string) []SpectrogramImage {
	base := filepath.Base(outputStem)

	kinds := []string{SpectrogramKindWhole}
	if rec != nil && rec.Regions != nil {
		if rec.Regions.RoomTone.ElectedProfile() != nil {
			kinds = append(kinds, SpectrogramKindRoomTone)
		}
		if rec.Regions.Speech.ElectedProfile() != nil {
			kinds = append(kinds, SpectrogramKindSpeech)
		}
	}

	images := make([]SpectrogramImage, 0, len(kinds)*len(stages))
	for _, kind := range kinds {
		for _, stage := range stages {
			images = append(images, SpectrogramImage{
				Kind:  kind,
				Stage: stage,
				Path:  spectrogramPath(base, kind, stage),
			})
		}
	}
	return images
}

// spectrogramPath builds the relative basename for one spectrogram image:
// `<stem-basename>.spectrogram-<kind>-<stage>.png`.
func spectrogramPath(stemBase, kind, stage string) string {
	return stemBase + ".spectrogram-" + kind + "-" + stage + ".png"
}
