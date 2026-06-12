package ui

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
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
// indigo-bordered box with the four labelled rows (Time/Loudness/Noise/Quality),
// the ㏈ glyph, the signed Δ, and the stars + word label. The heading shows only
// the processed (output) filename, never the input name nor a "→".
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

	// Heading (first line) shows only the processed filename, never the input
	// name nor a "→". The Loudness row's "→" must not leak into this check.
	headingLine, _, _ := strings.Cut(plain, "\n")
	if !strings.Contains(headingLine, "LMP-81-mark-LUFS-16-processed.flac") {
		t.Errorf("done box heading missing processed filename:\n%s", headingLine)
	}
	if strings.Contains(headingLine, "→") {
		t.Errorf("done box heading still shows '→':\n%s", headingLine)
	}
	if strings.Contains(headingLine, "LMP-81-mark.flac ") || strings.HasSuffix(headingLine, "LMP-81-mark.flac") {
		t.Errorf("done box heading still shows the input filename:\n%s", headingLine)
	}

	// Labelled rows.
	for _, label := range []string{"Time", "Loudness", "True peak", "Dynamics", "Noise floor", "Quality"} {
		if !strings.Contains(plain, label) {
			t.Errorf("done box missing %q row:\n%s", label, plain)
		}
	}

	// Loudness row: Input → Output integrated loudness in LUFS (never ㏈) with a
	// signed Δ that carries no unit.
	if !strings.Contains(plain, "-30.9 → -15.9 LUFS") {
		t.Errorf("done box missing loudness values in LUFS:\n%s", plain)
	}
	if !strings.Contains(plain, "Δ +15.0") {
		t.Errorf("done box missing signed Δ:\n%s", plain)
	}
	// The LUFS values must not be mislabelled with the ㏈ glyph.
	if strings.Contains(plain, "-15.9 ㏈") {
		t.Errorf("done box loudness still uses ㏈ instead of LUFS:\n%s", plain)
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

// TestDoneBoxTimeRow confirms the Time row renders the full-processing elapsed
// clock and a realtime-speed badge. With a known Duration/ProcessingTime pair the
// badge reads "⚡ N×" (audioDuration / processingSeconds); when ProcessingTime is
// zero the placeholder "⚡ —×" shows alongside a 00:00 clock.
func TestDoneBoxTimeRow(t *testing.T) {
	known := FileProgress{
		OutputPath:     "a-out.flac",
		Status:         StatusComplete,
		Duration:       120.0,
		ProcessingTime: 48 * time.Second,
		Quality:        processor.QualityScore{Stars: 4, Label: "Great"},
	}
	plain := ansi.Strip(renderDoneBox(known))

	if !strings.Contains(plain, "Time") {
		t.Errorf("done box missing Time row:\n%s", plain)
	}
	if !strings.Contains(plain, "00:48") {
		t.Errorf("Time row missing elapsed clock 00:48:\n%s", plain)
	}
	// 120s audio / 48s processing = 2.5× realtime.
	if !strings.Contains(plain, "⚡ 2.5×") {
		t.Errorf("Time row missing realtime badge ⚡ 2.5×:\n%s", plain)
	}

	zero := FileProgress{
		OutputPath: "b-out.flac",
		Status:     StatusComplete,
		Duration:   120.0,
		Quality:    processor.QualityScore{Stars: 4, Label: "Great"},
	}
	zeroPlain := ansi.Strip(renderDoneBox(zero))

	if !strings.Contains(zeroPlain, "⚡ —×") {
		t.Errorf("Time row missing placeholder badge ⚡ —× for zero ProcessingTime:\n%s", zeroPlain)
	}
	if !strings.Contains(zeroPlain, "00:00") {
		t.Errorf("Time row missing 00:00 clock for zero ProcessingTime:\n%s", zeroPlain)
	}
}

// TestDoneBoxNoiseFloorClamp confirms the Noise row clamps a floor at or below the
// 16-bit noise floor (~-96 dBFS), including digital-silence -Inf, to "< -96 ㏈",
// while a normal floor keeps the numeric "%.0f ㏈" form.
func TestDoneBoxNoiseFloorClamp(t *testing.T) {
	cases := []struct {
		name  string
		floor float64
		want  string
	}{
		{"negative infinity", math.Inf(-1), "< -96 ㏈"},
		{"below 16-bit floor", -120.0, "< -96 ㏈"},
		{"normal floor", -89.0, "-89 ㏈"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			file := FileProgress{
				OutputPath:      "x-out.flac",
				Status:          StatusComplete,
				FinalNoiseFloor: tc.floor,
				Quality:         processor.QualityScore{Stars: 4, Label: "Great"},
			}
			plain := ansi.Strip(renderDoneBox(file))
			if !strings.Contains(plain, tc.want) {
				t.Errorf("noise floor %v: want %q in:\n%s", tc.floor, tc.want, plain)
			}
		})
	}
}

// TestDoneBoxTruePeakRow confirms the True peak row renders the input → output
// true peak in ㏈TP with a signed Δ carrying no unit. Input TP comes from the
// Pass-1 Summary (ChainReady), output TP from the completion message.
func TestDoneBoxTruePeakRow(t *testing.T) {
	file := FileProgress{
		OutputPath: "tp-out.flac",
		Status:     StatusComplete,
		OutputTP:   -2.0,
		Summary:    AdaptedSummary{ChainReady: true, TruePeakDBTP: -0.1, InputLRA: 12.3},
		Quality:    processor.QualityScore{Stars: 4, Label: "Great"},
	}
	plain := ansi.Strip(renderDoneBox(file))

	if !strings.Contains(plain, "True peak") {
		t.Errorf("done box missing True peak row:\n%s", plain)
	}
	// Columns are right-aligned to a shared width, so a 4-char value carries one
	// lead space; the after column (4 chars) lands two spaces after the arrow.
	if !strings.Contains(plain, "-0.1 →  -2.0 "+unitDBTP) {
		t.Errorf("done box missing true peak before→after values:\n%s", plain)
	}
	if !strings.Contains(plain, "Δ  -1.9") {
		t.Errorf("done box missing true peak signed Δ:\n%s", plain)
	}
}

// TestDoneBoxDynamicsRow confirms the Dynamics row renders the input → output
// loudness range in LU with a signed Δ carrying no unit.
func TestDoneBoxDynamicsRow(t *testing.T) {
	file := FileProgress{
		OutputPath: "lra-out.flac",
		Status:     StatusComplete,
		OutputLRA:  8.0,
		Summary:    AdaptedSummary{ChainReady: true, TruePeakDBTP: -0.1, InputLRA: 12.3},
		Quality:    processor.QualityScore{Stars: 4, Label: "Great"},
	}
	plain := ansi.Strip(renderDoneBox(file))

	if !strings.Contains(plain, "Dynamics") {
		t.Errorf("done box missing Dynamics row:\n%s", plain)
	}
	// Columns are right-aligned to a shared width: 8.0 (3 chars) carries two lead
	// spaces, landing three spaces after the arrow.
	if !strings.Contains(plain, "12.3 →   8.0 LU") {
		t.Errorf("done box missing dynamics before→after values:\n%s", plain)
	}
	if !strings.Contains(plain, "Δ  -4.3") {
		t.Errorf("done box missing dynamics signed Δ:\n%s", plain)
	}
}

// TestDoneBoxRowOrder locks the row order: Time, Loudness, True peak, Dynamics,
// Noise floor, Quality. The loudness-family before→after rows group first, then
// the output-only floor, then the quality stars.
func TestDoneBoxRowOrder(t *testing.T) {
	file := FileProgress{
		OutputPath:      "order-out.flac",
		Status:          StatusComplete,
		InputLUFS:       -30.9,
		OutputLUFS:      -15.9,
		OutputTP:        -2.0,
		OutputLRA:       8.0,
		FinalNoiseFloor: -80.0,
		Summary:         AdaptedSummary{ChainReady: true, TruePeakDBTP: -0.1, InputLRA: 12.3},
		Quality:         processor.QualityScore{Stars: 4, Label: "Great"},
	}
	plain := ansi.Strip(renderDoneBox(file))

	order := []string{"Time", "Loudness", "True peak", "Dynamics", "Noise floor", "Quality"}
	last := -1
	for _, label := range order {
		idx := strings.Index(plain, label)
		if idx < 0 {
			t.Fatalf("done box missing %q row:\n%s", label, plain)
		}
		if idx <= last {
			t.Errorf("done box row %q out of order (at %d, previous at %d):\n%s", label, idx, last, plain)
		}
		last = idx
	}
}

// TestDoneBoxColumnsAlign confirms the three before→after rows (Loudness, True
// peak, Dynamics) form a mini-table: the → and the Δ sit at the same column
// across all three rows. Right-aligned numeric columns and a display-width-padded
// unit column (so the wide ㏈ glyph does not shift the ㏈TP row) make them line
// up. The display offset is measured via lipgloss.Width on the row prefix, so the
// wide ㏈/arrow glyphs count as their true column span.
func TestDoneBoxColumnsAlign(t *testing.T) {
	file := FileProgress{
		OutputPath: "align-out.flac",
		Status:     StatusComplete,
		InputLUFS:  -29.8,
		OutputLUFS: -16.0,
		OutputTP:   -2.2,
		OutputLRA:  8.8,
		Summary:    AdaptedSummary{ChainReady: true, TruePeakDBTP: -0.1, InputLRA: 12.3},
		Quality:    processor.QualityScore{Stars: 4, Label: "Great"},
	}
	plain := ansi.Strip(renderDoneBox(file))

	// Display column of the first occurrence of marker in line, or -1.
	colOf := func(line, marker string) int {
		before, _, found := strings.Cut(line, marker)
		if !found {
			return -1
		}
		return lipgloss.Width(before)
	}

	rowLine := func(label string) string {
		for l := range strings.SplitSeq(plain, "\n") {
			if strings.Contains(l, label) {
				return l
			}
		}
		return ""
	}

	labels := []string{"Loudness", "True peak", "Dynamics"}
	var arrowCol, deltaCol int
	for i, label := range labels {
		line := rowLine(label)
		if line == "" {
			t.Fatalf("done box missing %q row:\n%s", label, plain)
		}
		a := colOf(line, "→")
		d := colOf(line, "Δ")
		if a < 0 || d < 0 {
			t.Fatalf("%q row missing → or Δ: %q", label, line)
		}
		if i == 0 {
			arrowCol, deltaCol = a, d
			continue
		}
		if a != arrowCol {
			t.Errorf("%q row → at column %d, want %d (Loudness):\n%s", label, a, arrowCol, plain)
		}
		if d != deltaCol {
			t.Errorf("%q row Δ at column %d, want %d (Loudness):\n%s", label, d, deltaCol, plain)
		}
	}
}

// TestDoneBoxNoiseFloorOutputOnly confirms the Noise floor row stays output-only:
// no before→after arrow and no "reduced" delta. The input and output floors are
// measured by different methods, so a reduction number would be dishonest.
func TestDoneBoxNoiseFloorOutputOnly(t *testing.T) {
	file := FileProgress{
		OutputPath:      "nf-out.flac",
		Status:          StatusComplete,
		FinalNoiseFloor: -80.0,
		Summary:         AdaptedSummary{ChainReady: true, TruePeakDBTP: -0.1, InputLRA: 12.3},
		Quality:         processor.QualityScore{Stars: 4, Label: "Great"},
	}
	plain := ansi.Strip(renderDoneBox(file))

	// Isolate the Noise floor row from the Dynamics/True peak arrows above it.
	var noiseLine string
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Contains(line, "Noise floor") {
			noiseLine = line
			break
		}
	}
	if noiseLine == "" {
		t.Fatalf("done box missing Noise floor row:\n%s", plain)
	}
	if strings.Contains(noiseLine, "→") {
		t.Errorf("Noise floor row must not show a before→after arrow:\n%s", noiseLine)
	}
	if strings.Contains(noiseLine, "Δ") || strings.Contains(noiseLine, "reduced") {
		t.Errorf("Noise floor row must not show a reduction delta:\n%s", noiseLine)
	}
	if !strings.Contains(noiseLine, "-80 ㏈") {
		t.Errorf("Noise floor row missing the output floor value:\n%s", noiseLine)
	}
}

// TestDoneBoxGuardsEmptySummary confirms the True peak and Dynamics rows show the
// output value alone (no before→after arrow) when the Summary is empty, so an
// unset input never produces a misleading comparison.
func TestDoneBoxGuardsEmptySummary(t *testing.T) {
	file := FileProgress{
		OutputPath: "guard-out.flac",
		Status:     StatusComplete,
		OutputTP:   -2.0,
		OutputLRA:  8.0,
		Summary:    AdaptedSummary{ChainReady: false},
		Quality:    processor.QualityScore{Stars: 4, Label: "Great"},
	}
	plain := ansi.Strip(renderDoneBox(file))

	// The True peak and Dynamics rows must show the output value alone, with no
	// before→after arrow (the Loudness row is gated separately and is out of scope).
	for _, label := range []string{"True peak", "Dynamics"} {
		var line string
		for l := range strings.SplitSeq(plain, "\n") {
			if strings.Contains(l, label) {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("done box missing %q row:\n%s", label, plain)
		}
		if strings.Contains(line, "→") {
			t.Errorf("%s row shows misleading before→after with empty Summary:\n%s", label, line)
		}
	}
	if !strings.Contains(plain, "-2.0 "+unitDBTP) {
		t.Errorf("done box missing output-only true peak with empty Summary:\n%s", plain)
	}
	if !strings.Contains(plain, "8.0 LU") {
		t.Errorf("done box missing output-only dynamics with empty Summary:\n%s", plain)
	}
}

// TestFileCompleteMsgCopiesOutputTPAndLRA confirms the FileCompleteMsg handler
// copies OutputTP and OutputLRA onto the routed FileProgress so the done box can
// render the before→after rows.
func TestFileCompleteMsgCopiesOutputTPAndLRA(t *testing.T) {
	m := NewModel([]string{"a.flac"})
	updated, _ := m.Update(FileCompleteMsg{
		FileIndex:  0,
		InputLUFS:  -30.9,
		OutputLUFS: -15.9,
		OutputTP:   -2.0,
		OutputLRA:  8.0,
		OutputPath: "a-out.flac",
	})
	mm := updated.(Model)
	if got := mm.Files[0].OutputTP; got != -2.0 {
		t.Errorf("OutputTP not copied: got %v, want -2.0", got)
	}
	if got := mm.Files[0].OutputLRA; got != 8.0 {
		t.Errorf("OutputLRA not copied: got %v, want 8.0", got)
	}
}
