package report

import (
	"strings"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// RenderMarkdown renders a run record to a Markdown report string. timings is
// optional run metadata (pass durations, real-time factor) the record does not
// carry; pass the zero value for analysis-only or when unavailable.
//
// Section order is criterion 3 of the proposal, with the Spectrograms slot after
// Regions:
//
//	Header -> Processing Summary -> Loudness -> Dynamics -> Spectral ->
//	Noise Floor -> Regions -> Spectrograms (slot) -> Interval Summary ->
//	Filter Chain -> Peak Limiter + Loudnorm (renderNormalisation).
//
// A renderer that returns "" contributes nothing - no heading, no blank section.
// This is how analysis-only / Pass-1-only records naturally drop the processing-
// only blocks: renderProcessingSummary is empty for zero Timings,
// renderSpectrograms is always empty (out-of-scope stub), and renderFilters /
// renderNormalisation return "" when their record blocks are absent. Non-empty
// sections are joined with one blank line between them.
func RenderMarkdown(rec *processor.RunRecord, timings Timings) string {
	if rec == nil {
		return ""
	}

	sections := []string{
		renderHeader(rec),
		renderProcessingSummary(timings),
		renderLoudness(rec),
		renderDynamics(rec),
		renderSpectral(rec),
		renderNoiseFloor(rec),
		renderRegions(rec),
		renderSpectrograms(rec),
		renderIntervalSummary(rec),
		renderFilters(rec),
		renderNormalisation(rec),
	}

	parts := make([]string, 0, len(sections))
	for _, s := range sections {
		if trimmed := strings.TrimRight(s, "\n"); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}

	return strings.Join(parts, "\n\n") + "\n"
}
