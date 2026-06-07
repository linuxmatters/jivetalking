package ui

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// TestHeaderHasNoSubtitle confirms the redundant "Processing N file(s)"
// subtitle was removed from the header while the title stays. The per-letter
// gradient inserts ANSI escapes between letters, so strip them before matching
// the contiguous title word.
func TestHeaderHasNoSubtitle(t *testing.T) {
	header := renderHeader()
	plain := ansi.Strip(header)

	if !strings.Contains(plain, "Jivetalking") {
		t.Errorf("header missing title: %q", header)
	}
	if strings.Contains(plain, "file(s)") {
		t.Errorf("header still contains subtitle: %q", header)
	}
}

// headerColors extracts the distinct RGB foreground triples from the styled
// header. Title letters carry a bold prefix (1;38;2;r;g;b), so match the
// 38;2;r;g;b foreground regardless of any leading SGR attributes.
func headerColors(s string) [][3]int {
	var out [][3]int
	seen := map[[3]int]bool{}
	for seg := range strings.SplitSeq(s, "\x1b[") {
		_, after, found := strings.Cut(seg, "38;2;")
		if !found {
			continue
		}
		body, _, _ := strings.Cut(after, "m")
		parts := strings.Split(body, ";")
		if len(parts) < 3 {
			continue
		}
		var c [3]int
		ok := true
		for i := range 3 {
			n, err := strconv.Atoi(parts[i])
			if err != nil {
				ok = false
				break
			}
			c[i] = n
		}
		if ok && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// TestHeaderIsGradient confirms the title word is drawn as a multi-colour
// per-letter gradient (more than one distinct foreground across the letters)
// and never uses the brand red foreground.
func TestHeaderIsGradient(t *testing.T) {
	header := renderHeader()

	colors := headerColors(header)
	if len(colors) < 2 {
		t.Errorf("expected a multi-colour header gradient, got %d colours: %v", len(colors), colors)
	}
	// Brand red (#A40000 -> 164,0,0) must not colour the title.
	if slices.Contains(colors, [3]int{164, 0, 0}) {
		t.Errorf("header contains brand red 164,0,0:\n%q", header)
	}
}

// TestProcessingViewSectionOrder confirms the overall-progress box sits directly
// under the title and above the file queue, with no subtitle.
func TestProcessingViewSectionOrder(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav"})
	m.Width = 120
	m.Height = 40

	view := ansi.Strip(renderProcessingView(m))

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

// TestFinalSummaryReturnsCompletionContent confirms FinalSummary renders the
// title, the overall-progress status box, and a done box per completed file,
// with the old green banner and Audacity lines removed.
func TestFinalSummaryReturnsCompletionContent(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav"})

	updated, _ := m.Update(ProgressMsg{FileIndex: 0, Pass: processor.PassProcessing, Progress: 0.5, Level: -12})
	m = updated.(Model)
	updated, _ = m.Update(FileCompleteMsg{
		FileIndex: 0, OutputPath: "a-out.wav", InputLUFS: -30.9, OutputLUFS: -15.9, FinalNoiseFloor: -80.0,
		Quality: processor.QualityScore{Stars: 4, Label: "Great"},
	})
	m = updated.(Model)
	updated, _ = m.Update(FileCompleteMsg{
		FileIndex: 1, OutputPath: "b-out.wav", InputLUFS: -20.0, OutputLUFS: -16.0, FinalNoiseFloor: -65.0,
		Quality: processor.QualityScore{Stars: 5, Label: "Excellent"},
	})
	m = updated.(Model)
	m.Done = true

	summary := ansi.Strip(FinalSummary(m))

	// Title and overall-progress status box appear, matching the live view.
	if !strings.Contains(summary, "Jivetalking") {
		t.Errorf("summary missing title: %q", summary)
	}
	if !strings.Contains(summary, "2 files") || !strings.Contains(summary, "complete") {
		t.Errorf("summary missing overall-progress status box: %q", summary)
	}

	// Per-file done boxes.
	if !strings.Contains(summary, "a-out.wav") || !strings.Contains(summary, "b-out.wav") {
		t.Errorf("summary missing per-file results: %q", summary)
	}

	// Removed strings must be gone.
	for _, gone := range []string{"Processing Complete", "Audacity", "normalized to -16", "level-matched"} {
		if strings.Contains(summary, gone) {
			t.Errorf("summary still contains removed string %q: %q", gone, summary)
		}
	}
}

// TestDoneBoxRendersIndigoLabelledRows confirms a completed file renders as an
// indigo-bordered box with the three labelled rows (Loudness/Noise/Quality), the
// ㏈ glyph, the signed Δ, and the stars + word label.
func TestDoneBoxRendersIndigoLabelledRows(t *testing.T) {
	file := FileProgress{
		InputPath:       "LMP-81-mark.flac",
		OutputPath:      "LMP-81-mark-LUFS-16-processed.flac",
		Status:          StatusComplete,
		InputLUFS:       -30.9,
		OutputLUFS:      -15.9,
		FinalNoiseFloor: -80.0,
		Quality:         processor.QualityScore{Stars: 4, Label: "Excellent"},
	}

	raw := renderDoneBox(file)
	plain := ansi.Strip(raw)

	// Indigo border (#6366F1 -> 99,102,241) must colour the box.
	if !slices.Contains(headerColors(raw), [3]int{99, 102, 241}) {
		t.Errorf("done box border is not indigo (99,102,241):\n%q", raw)
	}

	// Labelled rows.
	for _, label := range []string{"Loudness", "Noise floor", "Quality"} {
		if !strings.Contains(plain, label) {
			t.Errorf("done box missing %q row:\n%s", label, plain)
		}
	}

	// Loudness row: Input → Output with ㏈ glyph and signed Δ.
	if !strings.Contains(plain, "-30.9 → -15.9 ㏈") {
		t.Errorf("done box missing loudness values:\n%s", plain)
	}
	if !strings.Contains(plain, "Δ +15.0") {
		t.Errorf("done box missing signed Δ:\n%s", plain)
	}

	// Noise row shows the output noise floor in dBFS, not an amount "reduced".
	if !strings.Contains(plain, "-80 ㏈") {
		t.Errorf("done box missing noise floor value:\n%s", plain)
	}
	if strings.Contains(plain, "reduced") {
		t.Errorf("done box still labels the noise floor as 'reduced':\n%s", plain)
	}

	// Quality row: filled + empty stars and the word label.
	if !strings.Contains(plain, "★★★★☆") {
		t.Errorf("done box missing 4-of-5 star bar:\n%s", plain)
	}
	if !strings.Contains(plain, "Excellent") {
		t.Errorf("done box missing quality label:\n%s", plain)
	}

	// No leftover hardcoded full-star bar.
	if strings.Contains(plain, "★★★★★") {
		t.Errorf("done box renders hardcoded 5 stars for a 4-star file:\n%s", plain)
	}
}

// TestDoneBoxNoiseAndStarsMoveTogether confirms the rendered Noise floor metric
// and the star count point the same direction: a cleaner file (lower dBFS floor)
// must show both a more-negative floor and at least as many stars as a noisier
// file. This locks the display against the prior contradiction where the better
// number sat next to fewer stars.
func TestDoneBoxNoiseAndStarsMoveTogether(t *testing.T) {
	clean := FileProgress{
		InputPath:       "clean.flac",
		OutputPath:      "clean-out.flac",
		Status:          StatusComplete,
		OutputLUFS:      -16.0,
		FinalNoiseFloor: -80.0,
		Quality:         processor.QualityScore{Stars: 5, Label: "Excellent"},
	}
	noisy := FileProgress{
		InputPath:       "noisy.flac",
		OutputPath:      "noisy-out.flac",
		Status:          StatusComplete,
		OutputLUFS:      -16.0,
		FinalNoiseFloor: -55.0,
		Quality:         processor.QualityScore{Stars: 4, Label: "Great"},
	}

	cleanPlain := ansi.Strip(renderDoneBox(clean))
	noisyPlain := ansi.Strip(renderDoneBox(noisy))

	if !strings.Contains(cleanPlain, "-80 ㏈") {
		t.Errorf("clean done box missing -80 floor:\n%s", cleanPlain)
	}
	if !strings.Contains(noisyPlain, "-55 ㏈") {
		t.Errorf("noisy done box missing -55 floor:\n%s", noisyPlain)
	}

	// Cleaner file: more-negative floor AND more stars. The number and the stars
	// move together, so the display can never contradict the score.
	if !strings.Contains(cleanPlain, "★★★★★") {
		t.Errorf("clean (cleaner floor) should show 5 stars:\n%s", cleanPlain)
	}
	if !strings.Contains(noisyPlain, "★★★★☆") {
		t.Errorf("noisy (higher floor) should show 4 stars:\n%s", noisyPlain)
	}
}
