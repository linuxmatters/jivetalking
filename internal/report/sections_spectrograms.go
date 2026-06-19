package report

import (
	"strings"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// This file holds the Spectrograms section renderer: the kind/stage order tables
// and the pure record-to-Markdown image-link builder. It is a separate change
// axis from the metric and filter renderers - it reads rec.Spectrograms only and
// emits image links, never numbers.

// spectrogramKindOrder is the stable kind order for the Spectrograms table
// (whole-file first, then the two elected regions). Each kind carries the row
// label shown in column 0.
var spectrogramKindOrder = []struct {
	kind  string
	label string
}{
	{processor.SpectrogramKindWhole, "Whole file"},
	{processor.SpectrogramKindRoomTone, "Room tone"},
	{processor.SpectrogramKindSpeech, "Speech"},
}

// spectrogramStageOrder is the stable stage (column) order. Processing records
// carry before+after; analysis-only records carry input only. A column is
// emitted only when at least one image populates it, mirroring the absent-stage
// convention the metric tables use (renderLoudness etc.).
var spectrogramStageOrder = []struct {
	stage  string
	header string
}{
	{processor.SpectrogramStageBefore, "Before"},
	{processor.SpectrogramStageAfter, "After"},
	{processor.SpectrogramStageInput, "Input"},
}

// renderSpectrograms renders the Spectrograms section from rec.Spectrograms ONLY:
// a Markdown table grouped by kind (whole -> roomtone -> speech) with one column
// per present stage. Processing records yield a Before | After pair per kind;
// analysis-only records yield a single Input column. Cells are Markdown image
// links to the record's relative basenames (![<kind> <stage>](<path>)). This
// renderer is a PURE record consumer - it reads the slice and builds Markdown,
// never calling ffmpeg/exec. An empty slice returns "" so the orchestrator emits
// no heading, matching the empty-section discipline the other renderers follow.
func renderSpectrograms(rec *processor.RunRecord) string {
	if len(rec.Spectrograms) == 0 {
		return ""
	}

	// Index the images by kind+stage for stable, order-independent lookup.
	type key struct{ kind, stage string }
	byKey := make(map[key]processor.SpectrogramImage, len(rec.Spectrograms))
	present := make(map[string]bool)
	for _, img := range rec.Spectrograms {
		byKey[key{img.Kind, img.Stage}] = img
		present[img.Stage] = true
	}

	// Columns: keep only stages that at least one image populates.
	stages := make([]struct{ stage, header string }, 0, len(spectrogramStageOrder))
	for _, s := range spectrogramStageOrder {
		if present[s.stage] {
			stages = append(stages, s)
		}
	}

	headers := make([]string, 0, len(stages)+1)
	headers = append(headers, "Region")
	for _, s := range stages {
		headers = append(headers, s.header)
	}

	body := make([][]string, 0, len(spectrogramKindOrder))
	for _, k := range spectrogramKindOrder {
		row := []string{k.label}
		any := false
		for _, s := range stages {
			img, ok := byKey[key{k.kind, s.stage}]
			if !ok {
				row = append(row, placeholder)
				continue
			}
			any = true
			row = append(row, spectrogramCell(k.kind, s.stage, img.Path))
		}
		if any {
			body = append(body, row)
		}
	}

	var b strings.Builder
	b.WriteString("## Spectrograms\n\n")
	b.WriteString(mdTable(headers, body))
	return b.String()
}

// spectrogramCell renders one Markdown image link to a relative basename:
// ![<kind> <stage>](<path>).
func spectrogramCell(kind, stage, path string) string {
	return "![" + kind + " " + stage + "](" + path + ")"
}
