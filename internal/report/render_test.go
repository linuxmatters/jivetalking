package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// fullProcessingRecord assembles a record exercising every section the
// orchestrator emits: the stage tables (loudness/dynamics/spectral with all three
// stages), the regions/noise/interval blocks (via NewAnalysisRunRecord), and the
// filters + normalisation blocks (via NewRunRecord). It splices the processing
// blocks onto a fully-staged measurement record so the orchestrator order test
// sees Filtered/Final columns and the Filter Chain / Peak Limiter / Loudnorm
// sections together.
func fullProcessingRecord() *processor.RunRecord {
	rec := processingRecord() // filters + normalisation blocks
	staged := fullLoudnessRecord()
	rec.Run = staged.Run
	rec.Loudness = staged.Loudness
	rec.Dynamics = staged.Dynamics
	rec.Spectral = staged.Spectral

	regions := regionsRecord() // noise + regions + interval-summary blocks
	rec.Noise = regions.Noise
	rec.Regions = regions.Regions
	rec.IntervalSummary = regions.IntervalSummary
	return rec
}

// sectionPositions returns the index of each section heading in s, asserting each
// is present. -1 means absent (caller decides whether that is a failure).
func sectionIndex(t *testing.T, s, heading string) int {
	t.Helper()
	return strings.Index(s, heading)
}

func TestRenderMarkdownSectionOrder(t *testing.T) {
	got := RenderMarkdown(fullProcessingRecord(), Timings{
		Pass1:          2 * time.Second,
		Pass2:          90 * time.Second,
		RealTimeFactor: 12.5,
	})

	// Criterion 3: sections MUST appear in this exact order. The Spectrograms slot
	// is empty (stub) so it contributes no heading; Interval Summary follows Regions
	// directly.
	order := []string{
		"# Audio Processing Report", // Header
		"## Processing Summary",
		"## Loudness",
		"## Dynamics",
		"## Spectral",
		"## Noise Floor",
		"## Regions",
		"## Interval Summary",
		"## Filter Chain",
		"## Peak Limiter",
		"## Loudnorm",
	}
	last := -1
	for _, heading := range order {
		idx := sectionIndex(t, got, heading)
		if idx == -1 {
			t.Fatalf("section %q missing from full report\n%s", heading, got)
		}
		if idx <= last {
			t.Errorf("section %q out of order (index %d after %d)\n%s", heading, idx, last, got)
		}
		last = idx
	}
}

func TestRenderMarkdownAnalysisOnlyOmitsProcessingSections(t *testing.T) {
	// regionsRecord is an analysis-only record: no filters, no normalisation, no
	// filtered/final stages, no processing timings.
	got := RenderMarkdown(regionsRecord(), Timings{})

	// Processing-only sections must be ABSENT.
	for _, banned := range []string{
		"## Processing Summary",
		"## Filter Chain",
		"## Peak Limiter",
		"## Loudnorm",
		"Spectrograms",
	} {
		if strings.Contains(got, banned) {
			t.Errorf("analysis-only report must omit %q\n%s", banned, got)
		}
	}

	// Analysis-only stage tables carry Input only - no Filtered/Final columns.
	if strings.Contains(got, "| Metric | Definition | Input | Filtered | Final |") {
		t.Errorf("analysis-only report must not carry Filtered/Final columns\n%s", got)
	}

	// The retained analysis sections must be present.
	for _, want := range []string{
		"# Audio Processing Report",
		"## Loudness",
		"## Noise Floor",
		"## Regions",
		"## Interval Summary",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("analysis-only report missing %q\n%s", want, got)
		}
	}
}

// TestRenderMarkdownNoDanglingHeadings asserts no heading is followed immediately
// by another heading or end-of-report with no body (an empty/dangling section).
func TestRenderMarkdownNoDanglingHeadings(t *testing.T) {
	got := RenderMarkdown(regionsRecord(), Timings{})
	// No double blank-line-then-heading collapse and no stray empty section: the
	// orchestrator drops empty returns, so every "## " heading is followed by
	// content before the next blank-separated block. A simple guard: the report must
	// not contain three consecutive newlines (which a dangling empty section between
	// two trimmed blocks would produce).
	if strings.Contains(got, "\n\n\n") {
		t.Errorf("report contains a triple newline (dangling/empty section)\n%q", got)
	}
}

func TestRenderMarkdownNilRecord(t *testing.T) {
	if got := RenderMarkdown(nil, Timings{}); got != "" {
		t.Errorf("nil record must render empty, got %q", got)
	}
}

func TestWriteMarkdownReport(t *testing.T) {
	rec := fullProcessingRecord()
	timings := Timings{Pass1: time.Second, RealTimeFactor: 10}
	want := RenderMarkdown(rec, timings)

	dir := t.TempDir()
	path := filepath.Join(dir, "report.md")
	if err := WriteMarkdownReport(rec, timings, path); err != nil {
		t.Fatalf("WriteMarkdownReport returned error: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written report: %v", err)
	}
	if string(data) != want {
		t.Errorf("written file content != RenderMarkdown output\n--- file ---\n%s\n--- render ---\n%s", data, want)
	}
}
