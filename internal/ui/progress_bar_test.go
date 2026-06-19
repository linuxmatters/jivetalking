package ui

import (
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// fillColors extracts the distinct RGB foreground triples (38;2;r;g;b) from a
// rendered bar, in order of first appearance, ignoring the empty-track colour.
func fillColors(s string) [][3]int {
	var out [][3]int
	seen := map[[3]int]bool{}
	for seg := range strings.SplitSeq(s, "\x1b[") {
		if !strings.HasPrefix(seg, "38;2;") {
			continue
		}
		body, _, _ := strings.Cut(seg, "m")
		parts := strings.Split(body, ";")
		if len(parts) < 5 {
			continue
		}
		r, err1 := strconv.Atoi(parts[2])
		g, err2 := strconv.Atoi(parts[3])
		b, err3 := strconv.Atoi(parts[4])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		c := [3]int{r, g, b}
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// hasColor reports whether the rendered bar contains a given RGB foreground.
func hasColor(s string, r, g, b int) bool {
	return slices.Contains(fillColors(s), [3]int{r, g, b})
}

// durSec returns a time.Duration for a fractional second count.
func durSec(s float64) time.Duration {
	return time.Duration(s * float64(time.Second))
}

// arrowColor extracts the RGB foreground applied to whichever up-tip peak-marker
// arrow (⬑ leading or ⬏ trailing) is present, or nil if neither is.
func arrowColor(s string) *[3]int {
	for seg := range strings.SplitSeq(s, "\x1b[") {
		if !strings.HasPrefix(seg, "38;2;") {
			continue
		}
		head, rest, _ := strings.Cut(seg, "m")
		if !strings.ContainsRune(rest, '⬑') && !strings.ContainsRune(rest, '⬏') {
			continue
		}
		parts := strings.Split(head, ";")
		if len(parts) < 5 {
			continue
		}
		r, err1 := strconv.Atoi(parts[2])
		g, err2 := strconv.Atoi(parts[3])
		b, err3 := strconv.Atoi(parts[4])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		return &[3]int{r, g, b}
	}
	return nil
}

// TestProgressFillIsGradient asserts the fill is a multi-colour gradient that
// starts at the sky-blue accent, ends at the indigo accent, never uses the red
// brand colour, and has no muddy/grey midpoint.
func TestProgressFillIsGradient(t *testing.T) {
	p := newProgressModel()
	p.SetWidth(meterWidth)
	out := p.ViewAs(0.5)

	colors := fillColors(out)
	// Drop the trailing empty-track colour: it is the dark fill (#444444 dark /
	// #CCCCCC light). The gradient fill must still carry multiple stops.
	if len(colors) < 3 {
		t.Fatalf("expected a multi-colour gradient fill, got %d colours: %v", len(colors), colors)
	}

	// Brand red (#A40000) must not appear anywhere in the fill.
	if hasColor(out, 164, 0, 0) {
		t.Errorf("progress fill contains brand red 38;2;164;0;0:\n%q", out)
	}

	// Start endpoint: sky-blue #38BDF8 must appear exactly.
	if !hasColor(out, 56, 189, 248) {
		t.Errorf("progress fill missing sky-blue start #38BDF8 (56,189,248):\n%v", colors)
	}
	// End endpoint: at a partial fill the last cell approaches but need not equal
	// the indigo stop #6366F1 (99,102,241). Assert the final fill colour is close
	// (each channel within 12) and clearly indigo (blue dominant).
	fill := colors[:len(colors)-1] // drop trailing empty-track colour
	last := fill[len(fill)-1]
	near := abs(last[0]-99) <= 12 && abs(last[1]-102) <= 12 && abs(last[2]-241) <= 12
	if !near {
		t.Errorf("final fill colour %v not near indigo end (99,102,241)", last)
	}

	// Midpoint sanity: at least one mid-gradient colour must stay vivid (channel
	// spread > 40) rather than collapsing to a muddy near-grey.
	vivid := false
	for _, c := range colors {
		lo := min(c[0], min(c[1], c[2]))
		hi := max(c[0], max(c[1], c[2]))
		if hi-lo > 40 {
			vivid = true
			break
		}
	}
	if !vivid {
		t.Errorf("gradient looks muddy (no vivid colour found): %v", colors)
	}
}

// TestMeterIsGradient asserts the audio level meter colours its filled cells as
// a smooth green→yellow→orange→red ramp rather than three flat zones: green-ish
// at the first cell, red-ish at the last, and more than 3 distinct fill colours.
func TestMeterIsGradient(t *testing.T) {
	// Drive the level to the hot end so every cell is filled and coloured.
	out := renderAudioLevelMeter(-1.0, 0.0, 0)

	colors := fillColors(out)
	if len(colors) <= 3 {
		t.Fatalf("expected a gradient (>3 distinct fill colours), got %d: %v", len(colors), colors)
	}

	first := colors[0]
	last := colors[len(colors)-1]

	// First cell green-ish: green channel dominant over red and blue.
	greenDominant := first[1] > first[0] && first[1] > first[2]
	if !greenDominant {
		t.Errorf("first cell %v is not green-dominant", first)
	}
	// Last cell red-ish: red channel dominant over green and blue.
	redDominant := last[0] > last[1] && last[0] > last[2]
	if !redDominant {
		t.Errorf("last cell %v is not red-dominant", last)
	}

	// No muddy/grey midpoint: at least one mid colour stays vivid.
	vivid := false
	for _, c := range colors {
		lo := min(c[0], min(c[1], c[2]))
		hi := max(c[0], max(c[1], c[2]))
		if hi-lo > 40 {
			vivid = true
			break
		}
	}
	if !vivid {
		t.Errorf("meter gradient looks muddy (no vivid colour): %v", colors)
	}
}

// TestMeterHasNoInBarPeakGlyph asserts the peak marker is no longer overlaid
// inside the bar: the bar cells are pure filled/empty gradient with no '|'.
func TestMeterHasNoInBarPeakGlyph(t *testing.T) {
	out := renderAudioLevelMeter(-20.0, -10.0, 0)
	bar := ansi.Strip(out)
	// Take the bar line: the line that contains the gradient cells.
	var barLine string
	found := false
	for line := range strings.SplitSeq(bar, "\n") {
		if strings.ContainsRune(line, '▓') || strings.ContainsRune(line, '░') {
			barLine = line
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no meter bar line (▓/░) rendered; cannot assert peak glyph absence:\n%q", bar)
	}
	if strings.ContainsRune(barLine, '|') {
		t.Errorf("bar line still contains in-bar peak glyph '|':\n%q", barLine)
	}
}

// markerLine returns the single peak-marker line (the one carrying an up-tip
// arrow ⬑ or ⬏) from a rendered, ansi-stripped meter, plus the total line count.
func markerLine(plain string) (string, int) {
	lines := strings.Split(plain, "\n")
	for _, line := range lines {
		if strings.ContainsRune(line, '⬑') || strings.ContainsRune(line, '⬏') {
			return line, len(lines)
		}
	}
	return "", len(lines)
}

// TestMeterPeakMarkerIsSingleLine asserts the peak label collapses to exactly one
// line beneath the bar (header + bar + marker = 3 lines), carrying an up-tip
// arrow, for two peak levels.
func TestMeterPeakMarkerIsSingleLine(t *testing.T) {
	for _, peak := range []float64{-10.0, -30.0} {
		plain := ansi.Strip(renderAudioLevelMeter(-40.0, peak, 0))
		line, n := markerLine(plain)
		if line == "" {
			t.Fatalf("peak=%g: no arrow marker line found in:\n%q", peak, plain)
		}
		// header (Level:) + bar + one marker line = 3 lines, no trailing newline.
		if n != 3 {
			t.Errorf("peak=%g: meter has %d lines, want 3 (header+bar+marker):\n%q",
				peak, n, plain)
		}
	}
}

// TestMeterHeaderShowsLevelNotPeak asserts the meter header line carries the
// current level only; the peak value moved down to the elbow connector line.
func TestMeterHeaderShowsLevelNotPeak(t *testing.T) {
	out := ansi.Strip(renderAudioLevelMeter(-20.0, -10.0, 0))
	header, _, _ := strings.Cut(out, "\n")
	if !strings.Contains(header, "Level:") {
		t.Errorf("header missing 'Level:': %q", header)
	}
	if strings.Contains(header, "Peak:") {
		t.Errorf("header still contains 'Peak:': %q", header)
	}
}

// displayCol returns the display column (cell offset) of the first occurrence of
// r in s, measuring the prefix with ansi.StringWidth so multi-byte superscript
// glyphs count their true display columns. It returns -1 when r is absent.
func displayCol(s string, r rune) int {
	idx := strings.IndexRune(s, r)
	if idx < 0 {
		return -1
	}
	return ansi.StringWidth(s[:idx])
}

// TestMeterPeakArrowTethersValue asserts the single marker line carries the peak
// value in superscript with no trailing unit, the up-tip arrow aligns at the peak
// display column (⬑ leading under the peak, or ⬏ trailing at the column when
// flipped), and the line stays within the bar width in both orientations.
func TestMeterPeakArrowTethersValue(t *testing.T) {
	cases := []struct {
		peak     float64
		peakPos  int
		wantLeft bool // true => left-flip form "value ⬏", false => "⬑ value"
	}{
		{-30.0, 22, false}, // room to the right: ⬑ ⁻³⁰·⁰
		{-10.0, 34, true},  // near right edge: flips to ⁻¹⁰·⁰ ⬏
	}
	for _, tc := range cases {
		plain := ansi.Strip(renderAudioLevelMeter(-40.0, tc.peak, 0))
		line, _ := markerLine(plain)
		if line == "" {
			t.Fatalf("peak=%g: no arrow marker line in:\n%q", tc.peak, plain)
		}

		// Value renders as superscript digits only, with no ㏈ unit appended.
		wantValue := superscriptValue(strconv.FormatFloat(tc.peak, 'f', 1, 64))
		if !strings.Contains(line, wantValue) {
			t.Errorf("peak=%g: marker line missing superscript value %q: %q",
				tc.peak, wantValue, line)
		}
		if strings.Contains(line, "㏈") {
			t.Errorf("peak=%g: marker line should carry no ㏈ unit: %q", tc.peak, line)
		}

		// Arrow aligns at the peak display column. Measure columns by display
		// width, not byte index, since superscript runes are multi-byte.
		var arrowCol int
		if tc.wantLeft {
			arrowCol = displayCol(line, '⬏')
			if !strings.HasSuffix(strings.TrimRight(line, " "), "⬏") {
				t.Errorf("peak=%g: left-flip line not ending in '⬏': %q", tc.peak, line)
			}
		} else {
			arrowCol = displayCol(line, '⬑')
		}
		if arrowCol != tc.peakPos {
			t.Errorf("peak=%g: arrow display column %d != peakPos %d\n%q",
				tc.peak, arrowCol, tc.peakPos, line)
		}

		if w := ansi.StringWidth(line); w > meterWidth {
			t.Errorf("peak=%g: marker line width %d > meterWidth %d", tc.peak, w, meterWidth)
		}
	}
}

// TestSuperscriptValue asserts the numeric superscript conversion: minus to ⁻,
// digits to their superscript forms, '.' to '·', and that the result carries no
// ㏈ unit and no residual ASCII numerals.
func TestSuperscriptValue(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"-20.3", "⁻²⁰·³"},
		{"6.0", "⁶·⁰"},
		{"-7", "⁻⁷"},
		{"123456789.0", "¹²³⁴⁵⁶⁷⁸⁹·⁰"},
	}
	for _, tc := range cases {
		got := superscriptValue(tc.in)
		if got != tc.want {
			t.Errorf("superscriptValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.Contains(got, "㏈") {
			t.Errorf("superscriptValue(%q) = %q, should carry no ㏈ unit", tc.in, got)
		}
		if strings.ContainsAny(got, "-.0123456789") {
			t.Errorf("superscriptValue(%q) = %q still carries ASCII numerals", tc.in, got)
		}
	}
}

// TestMeterPeakAtCeilingStaysInBounds asserts a peak at (and near) the 0 dB
// ceiling places the arrow at the last in-bounds column (width-1), not one cell
// beyond the bar, and that no rendered line exceeds the meter width. Near the
// ceiling the label flips to the trailing-arrow form, so the arrow ends the line.
func TestMeterPeakAtCeilingStaysInBounds(t *testing.T) {
	for _, peak := range []float64{0.0, -0.5, -1.0} {
		plain := ansi.Strip(renderAudioLevelMeter(-40.0, peak, 0))
		line, _ := markerLine(plain)
		if line == "" {
			t.Fatalf("peak=%g: no arrow marker line found in:\n%q", peak, plain)
		}
		// At the ceiling the value leads and the trailing ⬏ lands at the peak
		// column; assert that column is the last in-bounds cell.
		col := displayCol(line, '⬏')
		if col != meterWidth-1 {
			t.Errorf("peak=%g: arrow at column %d, want last in-bounds column %d:\n%q",
				peak, col, meterWidth-1, line)
		}

		// No rendered line may spill past the meter width.
		for l := range strings.SplitSeq(plain, "\n") {
			if w := ansi.StringWidth(l); w > meterWidth {
				t.Errorf("peak=%g: line width %d > meterWidth %d:\n%q", peak, w, meterWidth, l)
			}
		}
	}
}

// TestMeterNoPeakMarkerAtFloor asserts no marker line (neither arrow form)
// renders when the peak is still at the silence floor.
func TestMeterNoPeakMarkerAtFloor(t *testing.T) {
	out := ansi.Strip(renderAudioLevelMeter(-40.0, meterFloorDB, 0))
	if strings.ContainsRune(out, '⬑') || strings.ContainsRune(out, '⬏') {
		t.Errorf("peak marker rendered at silence floor:\n%q", out)
	}
}

// TestMeterPeakArrowIsOrange asserts the marker arrow is styled in an orange
// shade (red > green > blue, with a substantial green component so it reads as
// orange rather than pure red).
func TestMeterPeakArrowIsOrange(t *testing.T) {
	out := renderAudioLevelMeter(-40.0, -10.0, 0)
	c := arrowColor(out)
	if c == nil {
		t.Fatalf("no arrow colour found in:\n%q", out)
	}
	if c[0] <= c[1] || c[1] <= c[2] {
		t.Errorf("arrow colour %v is not an orange shade (want r>g>b)", c)
	}
}

// TestMeterPeakArrowPulses asserts the marker arrow oscillates between two
// distinct orange shades across pulse phases.
func TestMeterPeakArrowPulses(t *testing.T) {
	// Trough and crest of the 1.2 Hz sine: t=0 sits mid-rise; pick phases that
	// land near the dim trough and the bright peak.
	// durSec(0.625): sin = -1 -> dim trough. durSec(0.208): sin ≈ +1 -> bright crest.
	dim := arrowColor(renderAudioLevelMeter(-40.0, -10.0, durSec(0.625)))
	bright := arrowColor(renderAudioLevelMeter(-40.0, -10.0, durSec(0.208)))
	if dim == nil || bright == nil {
		t.Fatalf("missing arrow colour: dim=%v bright=%v", dim, bright)
	}
	if *dim == *bright {
		t.Errorf("arrow colour did not change across pulse phases: %v", *dim)
	}
	// Both endpoints must stay clearly orange so the marker never vanishes.
	for _, c := range []*[3]int{dim, bright} {
		if c[0] <= c[1] || c[1] <= c[2] {
			t.Errorf("pulse shade %v is not an orange shade (want r>g>b)", *c)
		}
	}
}

// TestTimelineClocksAndBadge asserts the Time block renders the elapsed clock,
// projected total clock, a dot timeline filled to progress, and the realtime
// speed badge with the expected value for a known input.
func TestTimelineClocksAndBadge(t *testing.T) {
	fp := FileProgress{
		Status:      StatusProcessing,
		CurrentPass: 2,
		Progress:    0.5,
		Duration:    60.0,
		ElapsedTime: 10 * time.Second,
	}
	line := ansi.Strip(renderTimeline(fp))

	// Elapsed clock 00:10, projected total = 10/0.5 = 20s -> 00:20.
	if !strings.Contains(line, "00:10") {
		t.Errorf("missing elapsed clock 00:10: %q", line)
	}
	if !strings.Contains(line, "00:20") {
		t.Errorf("missing projected clock 00:20: %q", line)
	}

	// realtime × = (0.5 × 60) / 10 = 3.0×.
	if !strings.Contains(line, "⚡ 3.0×") {
		t.Errorf("missing realtime badge '⚡ 3.0×': %q", line)
	}

	// Dot timeline filled to progress: 0.5 of 8 cells = 4 filled, 4 empty.
	filled := strings.Count(line, "▰")
	empty := strings.Count(line, "▱")
	if filled != 4 || empty != 4 {
		t.Errorf("timeline fill %d/%d, want 4/4 for progress 0.5: %q", filled, empty, line)
	}
}

// TestTimelineBadgeGuards asserts the realtime badge shows the placeholder when
// duration, progress, or elapsed are below the display thresholds, and a number
// once all three clear them.
func TestTimelineBadgeGuards(t *testing.T) {
	cases := []struct {
		name    string
		fp      FileProgress
		wantNum bool
	}{
		{"no duration", FileProgress{Progress: 0.5, Duration: 0, ElapsedTime: 10 * time.Second}, false},
		{"progress too low", FileProgress{Progress: 0.01, Duration: 60, ElapsedTime: 10 * time.Second}, false},
		{"elapsed too short", FileProgress{Progress: 0.5, Duration: 60, ElapsedTime: 200 * time.Millisecond}, false},
		{"all clear", FileProgress{Progress: 0.5, Duration: 60, ElapsedTime: 10 * time.Second}, true},
	}
	for _, tc := range cases {
		line := ansi.Strip(renderTimeline(tc.fp))
		hasPlaceholder := strings.Contains(line, "⚡ —×")
		hasNumber := strings.Contains(line, "×") && !hasPlaceholder
		if tc.wantNum && !hasNumber {
			t.Errorf("%s: expected a numeric badge, got: %q", tc.name, line)
		}
		if !tc.wantNum && !hasPlaceholder {
			t.Errorf("%s: expected placeholder '⚡ —×', got: %q", tc.name, line)
		}
	}
}

// TestTimelineFillTracksProgress asserts the dot timeline fill count tracks the
// pass progress across the full range and never overflows the timeline width.
func TestTimelineFillTracksProgress(t *testing.T) {
	for _, p := range []float64{0.0, 0.25, 0.5, 0.99, 1.0} {
		fp := FileProgress{Progress: p, Duration: 60, ElapsedTime: 5 * time.Second}
		line := ansi.Strip(renderTimeline(fp))
		filled := strings.Count(line, "▰")
		want := min(int(p*float64(timelineWidth)+0.5), timelineWidth)
		if filled != want {
			t.Errorf("progress %g: filled %d, want %d: %q", p, filled, want, line)
		}
		if filled+strings.Count(line, "▱") != timelineWidth {
			t.Errorf("progress %g: total dots != %d: %q", p, timelineWidth, line)
		}
	}
}

// TestTimelineProjectedClockPlaceholder asserts the projected total clock shows
// the --:-- placeholder until progress is meaningful.
func TestTimelineProjectedClockPlaceholder(t *testing.T) {
	fp := FileProgress{Progress: 0, Duration: 60, ElapsedTime: 2 * time.Second}
	line := ansi.Strip(renderTimeline(fp))
	if !strings.Contains(line, "--:--") {
		t.Errorf("expected projected clock placeholder '--:--': %q", line)
	}
}

func TestProcessingProgressWidthFitsTerminal(t *testing.T) {
	for _, term := range []int{20, 40, 80, 120, 200} {
		m := NewModel([]string{"a.wav"})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: term, Height: 24})
		m = updated.(Model)

		w := m.progress.Width()
		if w < minProgressWidth || w > maxProgressWidth {
			t.Errorf("term=%d progress width %d out of [%d,%d]", term, w, minProgressWidth, maxProgressWidth)
		}

		// Box outer width = bar width + border(2) + padding(2). It must not
		// exceed the terminal unless the bar hit its minimum floor on a narrow
		// terminal.
		box := w + 4
		if box > term && w > minProgressWidth {
			t.Errorf("term=%d box width %d overflows terminal", term, box)
		}
	}
}

// TestProgressWidthCapsAtMeterWidth locks the bar to the meter width on wide
// terminals so its right edge aligns with the audio level meter.
func TestProgressWidthCapsAtMeterWidth(t *testing.T) {
	for _, term := range []int{80, 120, 200} {
		m := NewModel([]string{"a.wav"})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: term, Height: 24})
		m = updated.(Model)

		if w := m.progress.Width(); w != meterWidth {
			t.Errorf("term=%d progress width %d, want %d (meterWidth)", term, w, meterWidth)
		}
	}
}

// TestProgressBarAlignsWithMeter renders both the eased bar line and the meter
// line at a normal width and asserts their rendered cell widths match.
func TestProgressBarAlignsWithMeter(t *testing.T) {
	m := NewModel([]string{"recording.wav"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = updated.(Model)

	barLine := m.progress.ViewAs(0.5)
	barW := ansi.StringWidth(barLine)
	if barW != meterWidth {
		t.Errorf("bar rendered width %d, want %d", barW, meterWidth)
	}

	meter := renderAudioLevelMeter(-20.0, -10.0, 0)
	// The meter's second line is the bar cells; take the widest non-label line.
	var meterW int
	for line := range strings.SplitSeq(meter, "\n") {
		if w := ansi.StringWidth(line); w > meterW {
			meterW = w
		}
	}
	if meterW != meterWidth {
		t.Errorf("meter bar width %d, want %d", meterW, meterWidth)
	}
}

func TestAnalysisProgressWidthFitsTerminal(t *testing.T) {
	for _, term := range []int{20, 40, 80, 120, 200} {
		m := NewAnalysisModel([]string{"a.wav"})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: term, Height: 24})
		m = updated.(AnalysisModel)

		w := m.progress.Width()
		if w < minProgressWidth || w > maxProgressWidth {
			t.Errorf("term=%d analysis progress width %d out of [%d,%d]", term, w, minProgressWidth, maxProgressWidth)
		}
	}
}

// TestProcessingRowFitsTerminal renders the full active file detail box and
// asserts no line exceeds the terminal width.
func TestProcessingRowFitsTerminal(t *testing.T) {
	for _, term := range []int{40, 80, 120} {
		m := NewModel([]string{"recording.wav"})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: term, Height: 24})
		m = updated.(Model)
		updated, _ = m.Update(ProgressMsg{FileIndex: 0, Pass: 2, PassName: "Processing", Progress: 0.5})
		m = updated.(Model)

		row := renderFileDetails(&m.Files[0], m.progress, -20.0, 0.5, -12.0)
		for line := range strings.SplitSeq(row, "\n") {
			if w := ansi.StringWidth(line); w > term {
				t.Errorf("term=%d line width %d overflows:\n%q", term, w, line)
			}
		}
	}
}

// TestProgressSpringEases asserts the bar fill eases toward a higher target
// after one tick (moves, but does not snap), and that ticking stops once all
// files complete.
func TestProgressSpringEases(t *testing.T) {
	m := NewModel([]string{"a.wav"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// Activate the file and set a target progress well ahead of the eased pos.
	updated, _ = m.Update(ProgressMsg{FileIndex: 0, Pass: 2, PassName: "Processing", Progress: 0.8})
	m = updated.(Model)

	const start = 0.0
	const target = 0.8
	if got := m.meters[0].progPos; got != start {
		t.Fatalf("initial progPos = %v, want %v", got, start)
	}

	updated, cmd := m.Update(meterTickMsg{})
	m = updated.(Model)
	if cmd == nil {
		t.Error("tick returned nil cmd while a file is active; loop must continue")
	}

	eased := m.meters[0].progPos
	if !(start < eased && eased < target) {
		t.Errorf("eased progPos %v not strictly between start %v and target %v", eased, start, target)
	}

	// After AllCompleteMsg the model is Done and the tick must not reschedule.
	updated, _ = m.Update(AllCompleteMsg{})
	m = updated.(Model)
	_, cmd = m.Update(meterTickMsg{})
	if cmd != nil {
		t.Error("tick rescheduled after AllCompleteMsg; loop must terminate")
	}
}

// TestProgressSpringIgnoresOutOfRange asserts out-of-range progress messages do
// not panic or disturb spring state.
func TestProgressSpringIgnoresOutOfRange(t *testing.T) {
	m := NewModel([]string{"a.wav"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	before := m.meters[0].progPos
	updated, _ = m.Update(ProgressMsg{FileIndex: 5, Pass: 2, Progress: 0.9})
	m = updated.(Model)
	updated, _ = m.Update(ProgressMsg{FileIndex: -1, Pass: 2, Progress: 0.9})
	m = updated.(Model)

	if got := m.meters[0].progPos; got != before {
		t.Errorf("out-of-range message disturbed spring state: %v != %v", got, before)
	}
}

// TestPeakSpringInitialisesAtFloor asserts each file's eased peak starts at the
// silence floor so the marker eases up from silence rather than from zero.
func TestPeakSpringInitialisesAtFloor(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav"})
	for i := range m.meters {
		if got := m.meters[i].peakPos; got != meterFloorDB {
			t.Errorf("meters[%d].peakPos = %v, want floor %v", i, got, meterFloorDB)
		}
	}
}

// TestPeakSpringEases asserts the peak marker eases toward a higher target after
// one tick (moves, but does not snap), and that ticking stops once all files
// complete.
func TestPeakSpringEases(t *testing.T) {
	m := NewModel([]string{"a.wav"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	// Activate the file and raise the peak-hold well above the silence floor.
	updated, _ = m.Update(ProgressMsg{FileIndex: 0, Pass: 2, PassName: "Processing", Progress: 0.5, Level: -12})
	m = updated.(Model)

	start := meterFloorDB
	target := m.Files[0].PeakLevel
	if got := m.meters[0].peakPos; got != start {
		t.Fatalf("initial peakPos = %v, want %v", got, start)
	}

	updated, cmd := m.Update(meterTickMsg{})
	m = updated.(Model)
	if cmd == nil {
		t.Error("tick returned nil cmd while a file is active; loop must continue")
	}

	eased := m.meters[0].peakPos
	if !(start < eased && eased < target) {
		t.Errorf("eased peakPos %v not strictly between start %v and target %v", eased, start, target)
	}

	updated, _ = m.Update(AllCompleteMsg{})
	m = updated.(Model)
	_, cmd = m.Update(meterTickMsg{})
	if cmd != nil {
		t.Error("tick rescheduled after AllCompleteMsg; loop must terminate")
	}
}

// TestPeakSpringNoOvershoot asserts the critically-damped peak spring converges
// to a stepped target without ever exceeding it. Peak-hold is monotonic, so any
// overshoot would momentarily render a value louder than the measured peak.
func TestPeakSpringNoOvershoot(t *testing.T) {
	m := NewModel([]string{"a.wav"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	updated, _ = m.Update(ProgressMsg{FileIndex: 0, Pass: 2, PassName: "Processing", Progress: 0.5, Level: -10})
	m = updated.(Model)

	target := m.Files[0].PeakLevel
	prev := m.meters[0].peakPos
	for tick := range 600 {
		updated, _ = m.Update(meterTickMsg{})
		m = updated.(Model)
		cur := m.meters[0].peakPos
		if cur > target {
			t.Fatalf("tick %d: peakPos %v overshot target %v", tick, cur, target)
		}
		if cur < prev-1e-9 {
			t.Fatalf("tick %d: peakPos %v moved backward from %v (non-monotonic)", tick, cur, prev)
		}
		prev = cur
	}
	if math.Abs(m.meters[0].peakPos-target) > 0.01 {
		t.Errorf("peakPos %v did not converge to target %v", m.meters[0].peakPos, target)
	}
}

// TestPeakSpringRisingTargets feeds a rising series of peak-hold targets and
// confirms the eased peak glides monotonically to each without overshoot.
func TestPeakSpringRisingTargets(t *testing.T) {
	m := NewModel([]string{"a.wav"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)
	updated, _ = m.Update(ProgressMsg{FileIndex: 0, Pass: 2, PassName: "Processing", Progress: 0.5, Level: -40})
	m = updated.(Model)

	prev := m.meters[0].peakPos
	for _, level := range []float64{-30, -20, -12, -6} {
		updated, _ = m.Update(ProgressMsg{FileIndex: 0, Pass: 2, PassName: "Processing", Progress: 0.5, Level: level})
		m = updated.(Model)
		target := m.Files[0].PeakLevel
		for tick := range 600 {
			updated, _ = m.Update(meterTickMsg{})
			m = updated.(Model)
			cur := m.meters[0].peakPos
			if cur > target+1e-9 {
				t.Fatalf("level %v tick %d: peakPos %v exceeded target %v", level, tick, cur, target)
			}
			if cur < prev-1e-9 {
				t.Fatalf("level %v tick %d: peakPos %v moved backward from %v", level, tick, cur, prev)
			}
			prev = cur
		}
		if math.Abs(prev-target) > 0.01 {
			t.Errorf("level %v: peakPos %v did not converge to target %v", level, prev, target)
		}
	}
}

// TestPeakSpringIgnoresOutOfRange asserts out-of-range messages do not disturb
// peak spring state.
func TestPeakSpringIgnoresOutOfRange(t *testing.T) {
	m := NewModel([]string{"a.wav"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(Model)

	before := m.meters[0].peakPos
	updated, _ = m.Update(ProgressMsg{FileIndex: 5, Pass: 2, Progress: 0.9, Level: -6})
	m = updated.(Model)
	updated, _ = m.Update(ProgressMsg{FileIndex: -1, Pass: 2, Progress: 0.9, Level: -6})
	m = updated.(Model)

	if got := m.meters[0].peakPos; got != before {
		t.Errorf("out-of-range message disturbed peak spring state: %v != %v", got, before)
	}
}
