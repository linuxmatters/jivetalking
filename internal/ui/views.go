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
		b.WriteString(renderFileEntry(m.Files[i], prog, easedLevel, easedProgress, easedPeak))
		b.WriteString("\n")
	}

	return b.String()
}

// renderFileEntry renders a single file entry in the queue
func renderFileEntry(file FileProgress, prog progress.Model, easedLevel, easedProgress, easedPeak float64) string {
	fileName := filepath.Base(file.InputPath)

	switch file.Status {
	case StatusComplete:
		return renderDoneBox(file)

	case StatusAnalyzing, StatusProcessing, StatusNormalising:
		// active file with detailed progress
		icon := lipgloss.NewStyle().Foreground(cli.ColorOrange).Render("∿")
		return fmt.Sprintf(" %s %s\n%s",
			icon, fileName,
			renderFileDetails(file, prog, easedLevel, easedProgress, easedPeak))

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

	// Pass indicator
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
	fmt.Fprintf(&content, "Pass %d/4: %s\n", file.CurrentPass, passName)

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

	return box.Render(content.String())
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

// peakMarkerGlyph is the peak-hold marker drawn on its own line beneath the bar,
// its point meeting the elbow corner directly below at the peak column.
const peakMarkerGlyph = "🭯"

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

	// Calculate position for peak marker
	peakPos := max(0, min(int(((peakLevel-minDB)/(maxDB-minDB))*float64(width)), width))

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

	// Peak-hold marker: a pulsing 🭯 on its own line beneath the bar, aligned to
	// the peak column, plus an elbow connector line that tethers the peak value to
	// the marker. Skip both when there is no meaningful peak yet (peak still at the
	// silence floor), so no stray marker sits at column 0. Alignment uses
	// lipgloss.Width (display columns), not byte length, so the wide ㏈ glyph in
	// the value does not shift the elbow corner off the marker column.
	if peakLevel > minDB {
		pulseColor := peakMarkerColor(elapsed)
		elbowStyle := lipgloss.NewStyle().Foreground(pulseColor)
		valueStyle := lipgloss.NewStyle().Foreground(cli.ColorOrange)
		value := fmt.Sprintf("%.1f ㏈", peakLevel)

		b.WriteByte('\n')
		b.WriteString(strings.Repeat(" ", peakPos))
		b.WriteString(elbowStyle.Render(peakMarkerGlyph))
		b.WriteByte('\n')
		// Default: elbow drops to the right (└ value). When the right-elbow form
		// would overflow the bar, flip to the left (value ┘) so the label stays
		// within meterWidth. The right form renders as `<peakPos spaces>└ <value>`
		// = peakPos + 1 (└) + 1 (space) + lipgloss.Width(value) columns.
		if peakPos+lipgloss.Width(value)+2 <= width {
			b.WriteString(strings.Repeat(" ", peakPos))
			b.WriteString(elbowStyle.Render("└"))
			b.WriteByte(' ')
			b.WriteString(valueStyle.Render(value))
		} else {
			// value then a right-elbow ┘ ending under the peak column. The form is
			// `<lead spaces><value> ┘`, so lead + width(value) + 1 == peakPos.
			lead := max(peakPos-(lipgloss.Width(value)+1), 0)
			b.WriteString(strings.Repeat(" ", lead))
			b.WriteString(valueStyle.Render(value))
			b.WriteByte(' ')
			b.WriteString(elbowStyle.Render("┘"))
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
			b.WriteString(renderFileEntry(m.Files[i], m.progress, 0, 0, 0))
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

// renderDoneBox renders a completed file as a filename line above an
// indigo-bordered box with three labelled rows: Loudness, Noise, and Quality.
// Shared by the live processing view (StatusComplete) and the persisted final
// summary so completed files look identical in both. The box matches the active
// processing box (RoundedBorder, Padding(0,1), meterWidth inner width) but uses
// an indigo border to mark "done" against the active sky-blue.
func renderDoneBox(file FileProgress) string {
	fileName := filepath.Base(file.InputPath)
	outputName := filepath.Base(file.OutputPath)

	icon := lipgloss.NewStyle().Foreground(cli.ColorGreen).Render("🗸")
	heading := fmt.Sprintf(" %s %s → %s", icon, fileName, outputName)

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

	// Loudness row: Input → Output ㏈ with signed Δ.
	delta := file.OutputLUFS - file.InputLUFS
	loudnessValue := fmt.Sprintf("%.1f → %.1f ㏈  Δ %+.1f",
		file.InputLUFS, file.OutputLUFS, delta)
	fmt.Fprintf(&content, "%s%s\n",
		labelStyle.Render("Loudness"), valueStyle.Render(loudnessValue))

	// Noise row: the output room-tone noise floor in dBFS. A lower (more negative)
	// floor is cleaner, the same direction the quality stars move, so the number and
	// the stars stay consistent. This is a floor, not an amount removed, so it is
	// labelled "Noise floor", never "reduced".
	noiseValue := fmt.Sprintf("%.0f ㏈", file.FinalNoiseFloor)
	fmt.Fprintf(&content, "%s%s\n",
		labelStyle.Render("Noise floor"), valueStyle.Render(noiseValue))

	// Quality row: objective stars + word label.
	stars := starStyle.Render(qualityStars(file.Quality.Stars))
	fmt.Fprintf(&content, "%s%s  %s",
		labelStyle.Render("Quality"), stars, valueStyle.Render(file.Quality.Label))

	return heading + "\n" + box.Render(content.String())
}

// qualityStars renders an n-of-5 star bar as filled ★ followed by empty ☆.
func qualityStars(n int) string {
	if n < 0 {
		n = 0
	}
	if n > 5 {
		n = 5
	}
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}
