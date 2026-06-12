package ui

import (
	"fmt"
	"image/color"
	"math"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"
	"github.com/linuxmatters/jivetalking/internal/cli"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// renderProcessingView renders the main processing view
func renderProcessingView(m Model) string {
	var b strings.Builder

	// Header (title only)
	b.WriteString(renderHeader())
	b.WriteString("\n\n")

	// Overall progress box, directly under the title
	b.WriteString(renderOverallProgress(m))
	b.WriteString("\n\n")

	// File queue
	b.WriteString(renderFileQueue(m, m.progress))

	return b.String()
}

// renderHeader renders the application header. The title word is drawn as a
// per-letter cyan→sky-blue gradient via the shared cli.RenderTitle() helper, so
// the TUI and the --version banner share one wordmark implementation.
func renderHeader() string {
	return cli.RenderTitle()
}

// renderFileQueue renders the list of files with their status
func renderFileQueue(m Model, prog progress.Model) string {
	var b strings.Builder

	for i := range m.Files {
		// Use the eased meter and progress positions for the active display;
		// fall back to the raw values when no spring slot exists.
		easedLevel := m.Files[i].CurrentLevel
		easedProgress := m.Files[i].Progress
		easedPeak := m.Files[i].PeakLevel
		if i < len(m.meters) {
			easedLevel = m.meters[i].pos
			easedProgress = m.meters[i].progPos
			easedPeak = m.meters[i].peakPos
		}
		b.WriteString(renderFileEntry(m.Files[i], prog, easedLevel, easedProgress, easedPeak, m.Width))
		b.WriteString("\n")
	}

	return b.String()
}

// renderFileEntry renders a single file entry in the queue. termWidth gates the
// side-by-side status boxes: they are dropped on narrow terminals so the Pass box
// never wraps.
func renderFileEntry(file FileProgress, prog progress.Model, easedLevel, easedProgress, easedPeak float64, termWidth int) string {
	fileName := filepath.Base(file.InputPath)

	switch file.Status {
	case StatusComplete:
		return renderDoneBox(file)

	case StatusAnalyzing, StatusProcessing, StatusNormalising:
		// active file with detailed progress, with the filter-chain status boxes
		// joined to the right of the Pass box.
		icon := lipgloss.NewStyle().Foreground(cli.ColorOrange).Render("∿")
		passBox := renderFileDetails(file, prog, easedLevel, easedProgress, easedPeak)
		body := joinStatusBoxes(passBox, file.Summary, termWidth)
		return fmt.Sprintf(" %s %s\n%s", icon, fileName, body)

	case StatusError:
		// ✗ failed file
		icon := lipgloss.NewStyle().Foreground(cli.ColorRed).Render("✗")
		return fmt.Sprintf(" %s %s\n   Error: %v", icon, fileName, file.Error)

	default:
		// ⧗ queued file
		icon := lipgloss.NewStyle().Foreground(cli.ColorMuted).Render("⧗")
		return fmt.Sprintf(" %s %s\n   Queued...", icon, fileName)
	}
}

// renderFileDetails renders detailed progress for the active file. easedLevel is
// the spring-smoothed audio level used for the meter display.
func renderFileDetails(file FileProgress, prog progress.Model, easedLevel, easedProgress, easedPeak float64) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cli.ColorSkyBlue).
		Padding(0, 1)

	var content strings.Builder

	// Pass indicator. "Pass N/4" sits in the top border (spliced below, matching
	// the Filter Chain / Analysis boxes); the pass name is the first content row.
	var passName string
	switch file.CurrentPass {
	case processor.PassAnalysis:
		passName = "Analysing Audio" //nolint:gosec // G101 false positive: not a credential
	case processor.PassProcessing:
		passName = "Processing Audio"
	case processor.PassMeasuring:
		passName = "Measuring Levels"
	case processor.PassNormalising:
		passName = "Normalising Audio"
	default:
		passName = "Processing"
	}
	fmt.Fprintf(&content, "%s\n", passName)

	// Progress bar (spring-eased fill for smooth motion)
	content.WriteString(prog.ViewAs(easedProgress))
	content.WriteString("\n\n")

	// Time block: elapsed clock, mini dot timeline, projected total clock, and a
	// realtime-speed badge.
	content.WriteString(renderTimeline(file))
	content.WriteByte('\n')

	// Audio level visualization. Both the displayed level and the peak marker ease
	// toward their targets via springs; the critically-damped peak spring keeps the
	// eased peak from ever exceeding the measured peak-hold value.
	if file.CurrentLevel != 0 {
		content.WriteString("\n")
		content.WriteString(renderAudioLevelMeter(easedLevel, easedPeak, file.ElapsedTime))
	}

	title := fmt.Sprintf("Pass %d/4", file.CurrentPass)
	return overlayBorderTitle(box.Render(content.String()), title, cli.ColorSkyBlue)
}

// timelineWidth is the cell count of the mini dot timeline in the Time block.
// Kept small (8) so the whole "MM:SS ▰… MM:SS · ⚡ N×" line stays within the
// meterWidth-cell box inner width.
const timelineWidth = 8

// renderTimeline renders the Time block: an elapsed clock, a mini dot timeline
// filled to the pass progress, a projected total-pass clock, and a realtime
// speed badge. The whole line stays within the box inner width (~meterWidth).
func renderTimeline(file FileProgress) string {
	elapsed := file.ElapsedTime
	elapsedSecs := elapsed.Seconds()

	// Projected total pass time = elapsed / progress (consistent with the prior
	// ETA derivation). Show placeholder until progress is meaningful.
	rightClock := "--:--"
	if file.Progress > 0 {
		rightClock = formatElapsed(time.Duration(elapsedSecs / file.Progress * float64(time.Second)))
	}

	// Mini dot timeline filled to progress. Filled dots muted, empty dots use the
	// meter empty-track colour, so the timeline reads as secondary to the main
	// gradient bar above.
	filled := int(file.Progress*float64(timelineWidth) + 0.5)
	filled = max(0, min(filled, timelineWidth))
	filledStyle := lipgloss.NewStyle().Foreground(cli.ColorMuted)
	emptyStyle := lipgloss.NewStyle().Foreground(cli.ColorFill)
	timeline := filledStyle.Render(strings.Repeat("▰", filled)) +
		emptyStyle.Render(strings.Repeat("▱", timelineWidth-filled))

	// Realtime speed badge: (progress × duration) / elapsed. Guard against
	// start-up garbage and missing duration.
	badge := "⚡ —×"
	if file.Duration > 0 && file.Progress > 0.02 && elapsedSecs > 0.3 {
		rt := (file.Progress * file.Duration) / elapsedSecs
		badge = fmt.Sprintf("⚡ %.1f×", rt)
	}

	muted := lipgloss.NewStyle().Foreground(cli.ColorMuted)
	return fmt.Sprintf("%s %s %s  %s  %s",
		formatElapsed(elapsed),
		timeline,
		rightClock,
		muted.Render("·"),
		muted.Render(badge))
}

// superscriptValue converts a numeric peak value to Unicode superscript so the
// peak label collapses onto a single marker line. The "Level:" header already
// names the unit, so the marker shows only the value. The mapping is: '-' → '⁻'
// (U+207B), digits 0-9 → ⁰¹²³⁴⁵⁶⁷⁸⁹ (U+2070, U+00B9, U+00B2, U+00B3,
// U+2074-U+2079; 1/2/3 are the Latin-1 superscripts, the rest the Superscripts
// and Subscripts block), and '.' → '·' (U+00B7 middle dot).
func superscriptValue(value string) string {
	const supDigits = "⁰¹²³⁴⁵⁶⁷⁸⁹"
	digits := []rune(supDigits)
	var b strings.Builder
	for _, r := range value {
		switch {
		case r == '-':
			b.WriteRune('⁻')
		case r == '.':
			b.WriteRune('·')
		case r >= '0' && r <= '9':
			b.WriteRune(digits[r-'0'])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// renderAudioLevelMeter renders a live audio level meter with dB visualization.
// elapsed drives the gentle pulse of the peak-hold marker; it is the file's
// running elapsed time, advanced once per meter tick, so no second tick loop is
// needed.
func renderAudioLevelMeter(currentLevel, peakLevel float64, elapsed time.Duration) string {
	var b strings.Builder

	// Display current level only; the peak value is tethered to its marker below.
	fmt.Fprintf(&b, "Level: %.1f ㏈\n", currentLevel)

	// Create visual meter
	// dB range: -70 dB (silence) to 0 dB (maximum)
	// Map to meterWidth-character width meter
	width := meterWidth
	minDB := meterFloorDB
	maxDB := 0.0

	// Calculate fill position for current level
	currentPos := max(0, min(int(((currentLevel-minDB)/(maxDB-minDB))*float64(width)), width))

	// Calculate column for peak marker. Unlike currentPos (an exclusive fill
	// count, so 0..width), this is a 0-based column index, so it must clamp to
	// width-1: at peakLevel == maxDB the raw ratio is 1.0 and would otherwise
	// place the marker and elbow one cell beyond the bar.
	peakPos := max(0, min(int(((peakLevel-minDB)/(maxDB-minDB))*float64(width)), width-1))

	// Build a continuous green→yellow→orange→red colour ramp once per render.
	// Real VU meters keep green dominant across the low range and compress the
	// warm colours into the hot end, so the ramp is built from two piecewise
	// Blend1D segments keyed to the -16 dB threshold: green→yellow fills the low
	// zone, then yellow→orange→red is squeezed into the top ~16 dB.
	greenZone := int((((-16.0) - minDB) / (maxDB - minDB)) * float64(width))
	greenZone = max(0, min(greenZone, width))

	ramp := make([]color.Color, 0, width)
	ramp = append(ramp, lipgloss.Blend1D(greenZone, cli.ColorGreen, cli.ColorYellow)...)
	ramp = append(ramp, lipgloss.Blend1D(width-greenZone, cli.ColorYellow, cli.ColorOrange, cli.ColorRed)...)

	cellColor := func(i int) color.Color {
		if i < 0 || i >= len(ramp) {
			return cli.ColorRed
		}
		return ramp[i]
	}

	meterChar := func(i int) rune {
		if i < currentPos {
			return '▓' // Filled
		}
		return '░' // Empty
	}

	// Build contiguous same-colour runs and style each as one segment so
	// lipgloss emits a single colour sequence per run rather than per rune.
	var run strings.Builder
	var runColor color.Color
	flush := func() {
		if run.Len() == 0 {
			return
		}
		b.WriteString(lipgloss.NewStyle().Foreground(runColor).Render(run.String()))
		run.Reset()
	}

	for i := range width {
		color := cellColor(i)
		if run.Len() > 0 && color != runColor {
			flush()
		}
		runColor = color
		run.WriteRune(meterChar(i))
	}
	flush()

	// Peak-hold marker: a single pulsing line beneath the bar that tethers the
	// peak value to its column via an up-tip arrow, with the value in Unicode
	// superscript so the label and its pointer share one row. The "Level:" header
	// already names the unit, so the marker carries only the value. Skip it when
	// there is no meaningful peak yet (peak still at the silence floor), so no
	// stray marker sits at column 0. Alignment uses lipgloss.Width (display
	// columns), not byte length: every superscript rune is width 1, including the
	// '·' decimal separator pinned to width 1 at startup (see main.go), so the
	// arrow lands exactly under the peak column.
	if peakLevel > minDB {
		pulseColor := peakMarkerColor(elapsed)
		arrowStyle := lipgloss.NewStyle().Foreground(pulseColor)
		valueStyle := lipgloss.NewStyle().Foreground(cli.ColorOrange)
		supValue := superscriptValue(fmt.Sprintf("%.1f", peakLevel))

		b.WriteByte('\n')
		// Default: arrow leads, value to its right (⬑ value). When that form would
		// overflow the bar, flip so the value leads and the arrow trails (value ⬏),
		// keeping the label within meterWidth. The right form renders as
		// `<peakPos spaces>⬑ <supValue>` = peakPos + 1 (⬑) + 1 (space) +
		// lipgloss.Width(supValue) columns; the arrow sits at the peak column.
		if peakPos+lipgloss.Width(supValue)+2 <= width {
			b.WriteString(strings.Repeat(" ", peakPos))
			b.WriteString(arrowStyle.Render("⬑"))
			b.WriteByte(' ')
			b.WriteString(valueStyle.Render(supValue))
		} else {
			// value then a right-up arrow ⬏ ending under the peak column. The form
			// is `<lead spaces><supValue> ⬏`, so lead + width(supValue) + 1 ==
			// peakPos places the arrow at the peak column.
			lead := max(peakPos-(lipgloss.Width(supValue)+1), 0)
			b.WriteString(strings.Repeat(" ", lead))
			b.WriteString(valueStyle.Render(supValue))
			b.WriteByte(' ')
			b.WriteString(arrowStyle.Render("⬏"))
		}
	}

	return b.String()
}

// peakMarkerColor returns the peak-hold marker colour for the current pulse
// phase. It oscillates gently between a deep orange and the full orange at about
// 1.2 Hz, driven by elapsed wall-clock time so it reuses the existing meter tick
// cadence. The interpolation runs straight in sRGB between two oranges so the
// marker stays a clear orange shade at both ends and never drifts off-hue.
func peakMarkerColor(elapsed time.Duration) color.Color {
	const pulseHz = 1.2
	// 0.0 at the dim trough, 1.0 at the bright crest.
	phase := 0.5 * (1 + math.Sin(2*math.Pi*pulseHz*elapsed.Seconds()))

	dr, dg, db := rgb8(cli.ColorOrangeDim)
	br, bg, bb := rgb8(cli.ColorOrange)
	lerp := func(a, b uint8) uint8 {
		return uint8(float64(a) + phase*(float64(b)-float64(a)) + 0.5)
	}
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X",
		lerp(dr, br), lerp(dg, bg), lerp(db, bb)))
}

// gainBarWidth is the cell count of the horizontal gain bar, matching the
// separation bar's grammar (▰ filled / ▱ empty) but wider so the gradient reads
// across width.
const gainBarWidth = 5

// gainBar returns the gain bar; thin alias kept for in-package call sites,
// mirroring qualityStars/QualityStars. See GainBar.
func gainBar(inputTP float64) string {
	return GainBar(inputTP)
}

// GainBar renders the input true peak as a short horizontal bar that fills and
// colours with the peak, mirroring separationBar's grammar. It is pure
// presentation, derived from the same true peak as GainAdvice, with the fill and
// colour aligned to the advice zones so the bar matches the advice category:
//   - Quiet  (TP < -12)        -> ~1 filled, blue end
//   - Fine   (-12 <= TP <= -1) -> ~3 filled, green centre (the -6 target ≈0.5)
//   - Hot    (TP > -1)         -> ~5 filled, red end
//
// Fill fraction is gainGlyphPosition(inputTP); filled cells = round(position *
// width). Colour is a fixed five-stop ramp, one colour per cell
// (bright-cyan→blue→green→amber→red), so the fill edge sits at the zone colour.
// Empty cells render dim. Styling goes through lipgloss so it auto-strips on a
// non-TTY pipe, leaving the bare ▰▱ runes (which still convey fill in mono).
// Exported so the analysis-only console path (cmd/jivetalking) reuses one source
// of truth.
func GainBar(inputTP float64) string {
	position := gainGlyphPosition(inputTP)
	filled := int(math.Round(position * float64(gainBarWidth)))
	// Floor at one cell so the cold/blue end always shows a pip — an empty bar
	// would lose the under-recorded signal. A clipping input (>= 0 dBTP) maxes
	// the bar so the worst case reads as a full, red-tipped run.
	filled = max(1, min(filled, gainBarWidth))
	if inputTP >= 0 {
		filled = gainBarWidth
	}

	// Five fixed stops, one per cell (width == stop count, so Blend1D samples land
	// exactly on the stops with no muddy interpolation): the fill tip reads its
	// zone colour directly — 3 cells = green (spot on), 4 = amber (hot), 5 = red
	// (clipping).
	ramp := lipgloss.Blend1D(gainBarWidth, cli.ColorCyanBright, cli.ColorBlue, cli.ColorGreen, cli.ColorOrange, cli.ColorRed)
	var b strings.Builder
	for i := range gainBarWidth {
		var c color.Color = cli.ColorFill
		ch := "▱"
		if i < filled {
			c = ramp[i]
			ch = "▰"
		}
		b.WriteString(lipgloss.NewStyle().Foreground(c).Render(ch))
	}
	return b.String()
}

// gainGlyphPosition maps an input true peak (dBTP) to a position in [0,1] using
// the GainAdvice zone boundaries, so the bar's fill and colour band coincide with
// the advice category. Each band clamps at its edges.
func gainGlyphPosition(inputTP float64) float64 {
	const (
		quietTP  = -12.0
		hotTP    = -1.0
		quietLo  = -24.0
		hotHi    = 1.0
		quietPos = 0.33
		finePos  = 0.67
	)
	switch {
	case inputTP < quietTP:
		return lerpClamp(inputTP, quietLo, quietTP, 0.0, quietPos)
	case inputTP <= hotTP:
		return lerpClamp(inputTP, quietTP, hotTP, quietPos, finePos)
	default:
		return lerpClamp(inputTP, hotTP, hotHi, finePos, 1.0)
	}
}

// lerpClamp maps v from the input range [inLo,inHi] onto [outLo,outHi], clamping
// the result to the output range at the edges.
func lerpClamp(v, inLo, inHi, outLo, outHi float64) float64 {
	if inHi == inLo {
		return outLo
	}
	t := (v - inLo) / (inHi - inLo)
	t = math.Max(0, math.Min(1, t))
	return outLo + t*(outHi-outLo)
}

// rgb8 resolves a color.Color to 8-bit sRGB channels.
func rgb8(c color.Color) (r, g, b uint8) {
	r16, g16, b16, _ := c.RGBA()
	return uint8((r16 >> 8) & 0xFF), uint8((g16 >> 8) & 0xFF), uint8((b16 >> 8) & 0xFF)
}

// renderOverallProgress renders the overall progress footer
func renderOverallProgress(m Model) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cli.ColorMuted).
		Padding(0, 1)

	content := fmt.Sprintf("Processing %d files, %d complete, %d failed",
		m.TotalFiles, m.CompletedFiles, m.FailedFiles)

	return box.Render(content)
}

// FinalSummary returns the completion-summary string for persisting to the
// normal screen after the alt-screen program exits. Callers gate on Model.Done
// so an early user quit does not print a misleading "complete" summary.
func FinalSummary(m Model) string {
	return renderCompletionSummary(m)
}

// renderCompletionSummary renders the persisted completion view: the same title
// and overall-progress status box as the live view, followed by the stack of
// indigo done boxes. Completed files render through renderDoneBox so they look
// identical to the live processing view at completion.
func renderCompletionSummary(m Model) string {
	var b strings.Builder

	b.WriteString(renderHeader())
	b.WriteString("\n\n")

	b.WriteString(renderOverallProgress(m))
	b.WriteString("\n\n")

	for i := range m.Files {
		if m.Files[i].Status == StatusError {
			b.WriteString(renderFileEntry(m.Files[i], m.progress, 0, 0, 0, m.Width))
			b.WriteString("\n")
			continue
		}
		if m.Files[i].Status == StatusComplete {
			b.WriteString(renderDoneBox(m.Files[i]))
			b.WriteString("\n")
		}
	}

	return b.String()
}

// doneBoxLabelWidth is the column width reserved for the leading label in each
// done-box row so the values align in a column. Wide enough for the longest
// label ("Noise floor" = 11 cols) plus a trailing space.
const doneBoxLabelWidth = 12

// Column widths for the three before→after rows (Loudness, True peak, Dynamics)
// so the →, the unit, and the Δ line up vertically into a mini-table. Sized to
// the widest realistic %.1f value across the rows: 5 cols fits -35.2 / +19.2,
// the practical worst case for LUFS / ㏈TP / LU. The whole value portion then
// occupies 27 cols, which with the 12-col label leaves 1 col spare inside the
// 40-col box (no wrap). Right-aligning the numeric columns also lines up the
// decimal points.
//
//	doneBoxValueWidth — before and after numeric columns (shared width).
//	doneBoxUnitWidth  — unit column DISPLAY width; ㏈TP is 4 display cols (㏈ is
//	                    East-Asian-wide = 2 + "TP"), LUFS 4, LU 2 (padded to 4).
//	doneBoxDeltaWidth — signed %+.1f delta column.
const (
	doneBoxValueWidth = 5
	doneBoxUnitWidth  = 4
	doneBoxDeltaWidth = 5
)

// doneBoxBeforeAfterRow formats one before→after row's value portion as
// fixed-width columns so the →, unit, and Δ align across rows. The before and
// after numbers are right-aligned to doneBoxValueWidth (lining up the decimal
// points); the unit is left-padded to doneBoxUnitWidth using fitWidth so the
// wide ㏈ glyph pads by DISPLAY width, not rune count; the delta is
// right-aligned and signed.
func doneBoxBeforeAfterRow(before, after float64, unit string, delta float64) string {
	beforeCol := fmt.Sprintf("%*.1f", doneBoxValueWidth, before)
	afterCol := fmt.Sprintf("%*.1f", doneBoxValueWidth, after)
	unitCol := fitWidth(unit, doneBoxUnitWidth)
	deltaCol := fmt.Sprintf("%+*.1f", doneBoxDeltaWidth, delta)
	return fmt.Sprintf("%s → %s %s  Δ %s", beforeCol, afterCol, unitCol, deltaCol)
}

// renderDoneBox renders a completed file as a filename line above an
// indigo-bordered box with seven labelled rows: Time, Loudness, True peak,
// Dynamics, Noise floor, Recording, and Processed. The loudness-family
// before→after rows are grouped first, then the output-only floor, then the
// source-capture (Recording) and output-quality (Processed) star rows. Shared by
// the live processing view (StatusComplete) and the persisted
// final summary so completed files look identical in both. The box matches the
// active processing box (RoundedBorder, Padding(0,1), meterWidth inner width) but
// uses an indigo border to mark "done" against the active sky-blue.
func renderDoneBox(file FileProgress) string {
	outputName := filepath.Base(file.OutputPath)

	icon := lipgloss.NewStyle().Foreground(cli.ColorGreen).Render("🗸")
	heading := fmt.Sprintf(" %s %s", icon, outputName)

	labelStyle := lipgloss.NewStyle().Foreground(cli.ColorMuted).Width(doneBoxLabelWidth)
	valueStyle := lipgloss.NewStyle().Foreground(cli.ColorText)
	starStyle := lipgloss.NewStyle().Foreground(cli.ColorOrange)

	// Match the active processing box frame width: meterWidth content + Padding(0,1)
	// (2 cols) + RoundedBorder (2 cols) = meterWidth + 4 total columns.
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cli.ColorIndigo).
		Padding(0, 1).
		Width(meterWidth + 4)

	var content strings.Builder

	// Time row: full-processing elapsed clock, a middot, then a realtime-speed
	// badge, reusing renderTimeline's visual grammar. ProcessingTime spans all four
	// passes (plumbed from the pool), so it reflects the whole file, not one pass.
	muted := lipgloss.NewStyle().Foreground(cli.ColorMuted)
	speedBadge := "⚡ —×"
	if file.ProcessingTime > 0 && file.Duration > 0 {
		rt := file.Duration / file.ProcessingTime.Seconds()
		speedBadge = fmt.Sprintf("⚡ %.1f×", rt)
	}
	timeValue := fmt.Sprintf("%s  %s  %s",
		formatElapsed(file.ProcessingTime), muted.Render("·"), muted.Render(speedBadge))
	fmt.Fprintf(&content, "%s%s\n", labelStyle.Render("Time"), timeValue)

	// Loudness row: input → output integrated loudness (LUFS, never ㏈) with a signed
	// Δ that carries no unit. Same before→after grammar as True peak and Dynamics.
	loudnessDelta := file.OutputLUFS - file.InputLUFS
	loudnessValue := doneBoxBeforeAfterRow(file.InputLUFS, file.OutputLUFS, "LUFS", loudnessDelta)
	fmt.Fprintf(&content, "%s%s\n",
		labelStyle.Render("Loudness"), valueStyle.Render(loudnessValue))

	// True peak row: input → output true peak (㏈TP), both ebur128 peak=true so the
	// before→after is honest. The input TP comes from the Pass-1 Summary; show the
	// output alone if the Summary is empty rather than a misleading "0.0 →".
	if file.Summary.ChainReady {
		tpDelta := file.OutputTP - file.Summary.TruePeakDBTP
		tpValue := doneBoxBeforeAfterRow(file.Summary.TruePeakDBTP, file.OutputTP, unitDBTP, tpDelta)
		fmt.Fprintf(&content, "%s%s\n",
			labelStyle.Render("True peak"), valueStyle.Render(tpValue))
	} else {
		tpValue := fmt.Sprintf("%.1f %s", file.OutputTP, unitDBTP)
		fmt.Fprintf(&content, "%s%s\n",
			labelStyle.Render("True peak"), valueStyle.Render(tpValue))
	}

	// Dynamics row: input → output loudness range (LU), both ebur128/loudnorm LU, so
	// the range tightening is honest. Same Summary-empty guard as True peak.
	if file.Summary.ChainReady {
		lraDelta := file.OutputLRA - file.Summary.InputLRA
		lraValue := doneBoxBeforeAfterRow(file.Summary.InputLRA, file.OutputLRA, "LU", lraDelta)
		fmt.Fprintf(&content, "%s%s\n",
			labelStyle.Render("Dynamics"), valueStyle.Render(lraValue))
	} else {
		lraValue := fmt.Sprintf("%.1f LU", file.OutputLRA)
		fmt.Fprintf(&content, "%s%s\n",
			labelStyle.Render("Dynamics"), valueStyle.Render(lraValue))
	}

	// Noise row: the output room-tone noise floor in dBFS. A lower (more negative)
	// floor is cleaner, the same direction the quality stars move, so the number and
	// the stars stay consistent. This is a floor, not an amount removed, so it is
	// labelled "Noise floor", never "reduced". A floor at or below the 16-bit noise
	// floor (~-96 dBFS), including digital-silence -Inf, displays as "< -96 ㏈"
	// rather than a misleading raw figure.
	noiseValue := fmt.Sprintf("%.0f ㏈", file.FinalNoiseFloor)
	if math.IsInf(file.FinalNoiseFloor, -1) || file.FinalNoiseFloor <= -96 {
		noiseValue = "< -96 ㏈"
	}
	fmt.Fprintf(&content, "%s%s\n",
		labelStyle.Render("Noise floor"), valueStyle.Render(noiseValue))

	// Quality rows: source-capture stars (Recording) above output-quality stars
	// (Processed). The pair tells the value story: a low Recording beside a high
	// Processed shows what the tool rescued. Both use the same star/label styling.
	recStars := starStyle.Render(qualityStars(file.RecordingQuality.Stars))
	fmt.Fprintf(&content, "%s%s  %s\n",
		labelStyle.Render("Recording"), recStars, valueStyle.Render(file.RecordingQuality.Label))

	procStars := starStyle.Render(qualityStars(file.Quality.Stars))
	fmt.Fprintf(&content, "%s%s  %s",
		labelStyle.Render("Processed"), procStars, valueStyle.Render(file.Quality.Label))

	return heading + "\n" + box.Render(content.String())
}

// qualityStars renders an n-of-5 star bar as filled ★ followed by empty ☆.
func qualityStars(n int) string {
	return QualityStars(n)
}

// QualityStars renders an n-of-5 star bar as filled ★ followed by empty ☆,
// clamped to [0,5]. Exported so the analysis-only console path (cmd/jivetalking)
// renders the same Recording star bar as the TUI without duplicating the glyphs.
func QualityStars(n int) string {
	if n < 0 {
		n = 0
	}
	if n > 5 {
		n = 5
	}
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}
