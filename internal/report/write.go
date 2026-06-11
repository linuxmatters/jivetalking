// Package report renders a processor.RunRecord into a Markdown report. It is a
// clean break from the legacy .log formatters in internal/logging: it reads only
// the in-memory run record (and optional run metadata via Timings), never the
// .json artefact or AudioMeasurements, so a future .json -> .md re-render is a
// thin adapter over RenderMarkdown.
package report

import (
	"fmt"
	"os"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// Timings carries run metadata the RunRecord does not hold: pass durations and
// the real-time factor, supplied alongside the record by the call site. It is
// strictly run metadata - no measurement may travel here; measurements come only
// from the RunRecord. The zero value is valid (analysis-only or when unavailable),
// and renderers omit the Processing Summary when it is zero.
type Timings struct {
	Pass1          time.Duration
	Pass2          time.Duration
	Pass3          time.Duration // Loudnorm measurement pass (may be 0 if skipped)
	Pass4          time.Duration // Loudnorm application pass (may be 0 if skipped)
	Analysis       time.Duration // Pass 1 analysis duration (analysis-only mode)
	Adaptation     time.Duration // Filter adaptation duration (analysis-only mode)
	RealTimeFactor float64       // Audio duration / wall-clock processing time
}

// WriteMarkdownReport renders rec (with timings) to Markdown and writes it to
// path. It mirrors processor.WriteRunRecord (runrecord_write.go): a single
// os.WriteFile at 0o644 with a wrapped error. Like the run record, the report is
// a side artefact - non-fatal-write handling (keep the processed audio on a write
// failure) is the caller's concern; this function only writes and returns the
// error.
func WriteMarkdownReport(rec *processor.RunRecord, timings Timings, path string) error {
	data := RenderMarkdown(rec, timings)
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		return fmt.Errorf("failed to write Markdown report to %s: %w", path, err)
	}
	return nil
}
