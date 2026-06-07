package ui

import (
	"strings"
	"testing"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// TestHeaderHasNoSubtitle confirms the redundant "Processing N file(s)"
// subtitle was removed from the header while the title stays.
func TestHeaderHasNoSubtitle(t *testing.T) {
	header := renderHeader()

	if !strings.Contains(header, "Jivetalking") {
		t.Errorf("header missing title: %q", header)
	}
	if strings.Contains(header, "file(s)") {
		t.Errorf("header still contains subtitle: %q", header)
	}
}

// TestProcessingViewSectionOrder confirms the overall-progress box sits directly
// under the title and above the file queue, with no subtitle.
func TestProcessingViewSectionOrder(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav"})
	m.Width = 120
	m.Height = 40

	view := renderProcessingView(m)

	if strings.Contains(view, "file(s)") {
		t.Errorf("processing view still contains subtitle: %q", view)
	}

	titleIdx := strings.Index(view, "Jivetalking")
	boxIdx := strings.Index(view, "complete")
	queueIdx := strings.Index(view, "a.wav")

	if titleIdx < 0 || boxIdx < 0 || queueIdx < 0 {
		t.Fatalf("expected title, overall-progress box, and file queue in view:\n%s", view)
	}
	if titleIdx >= boxIdx || boxIdx >= queueIdx {
		t.Errorf("section order wrong: title=%d box=%d queue=%d\n%s", titleIdx, boxIdx, queueIdx, view)
	}
}

// TestProcessingViewOverallProgressContent confirms the overall-progress box
// content appears near the top of the processing view.
func TestProcessingViewOverallProgressContent(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav", "c.wav"})
	m.Width = 120

	updated, _ := m.Update(FileCompleteMsg{FileIndex: 0, OutputPath: "a-out.wav"})
	m = updated.(Model)

	view := renderProcessingView(m)

	if !strings.Contains(view, "3 files") {
		t.Errorf("view missing total count: %q", view)
	}
	if !strings.Contains(view, "1 complete") {
		t.Errorf("view missing complete count: %q", view)
	}
}

// TestFinalSummaryReturnsCompletionContent confirms FinalSummary returns the
// per-file results and overall summary for a completed model.
func TestFinalSummaryReturnsCompletionContent(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav"})

	updated, _ := m.Update(ProgressMsg{FileIndex: 0, Pass: processor.PassProcessing, Progress: 0.5, Level: -12})
	m = updated.(Model)
	updated, _ = m.Update(FileCompleteMsg{FileIndex: 0, OutputPath: "a-out.wav", InputLUFS: -23.0, OutputLUFS: -16.0, NoiseFloor: 12.0})
	m = updated.(Model)
	updated, _ = m.Update(FileCompleteMsg{FileIndex: 1, OutputPath: "b-out.wav", InputLUFS: -20.0, OutputLUFS: -16.0, NoiseFloor: 8.0})
	m = updated.(Model)
	m.Done = true

	summary := FinalSummary(m)

	if !strings.Contains(summary, "Processing Complete") {
		t.Errorf("summary missing completion header: %q", summary)
	}
	if !strings.Contains(summary, "a-out.wav") || !strings.Contains(summary, "b-out.wav") {
		t.Errorf("summary missing per-file results: %q", summary)
	}
	if !strings.Contains(summary, "-16.0 LUFS") {
		t.Errorf("summary missing output LUFS: %q", summary)
	}
	if !strings.Contains(summary, "-16 LUFS and level-matched") {
		t.Errorf("summary missing overall footer: %q", summary)
	}
}
