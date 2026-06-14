package ui

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/linuxmatters/jivetalking/internal/cli"
)

// Filter-chain status boxes (Filter Chain + Analysis), drawn to the right of the
// Pass box. They show ADAPTED CONFIG + MEASURED ANALYSIS, not live metering: the
// values are fixed within a pass, so the boxes re-render only on AdaptedSummaryMsg,
// never on the meter tick. Pure presentation, no DSP, no measurement.

const (
	// Box inner content widths (columns), excluding border + padding. Set to the
	// EXACT widest row content so the longest row fills the inner width with zero
	// fitWidth trailing pad: only the box style's Padding(0,1) remains, giving a
	// symmetric 1-space gutter on both sides. Widest rows (measured via
	// lipgloss.Width, wide unit glyphs ㏈/㎑/㎐ count as 2):
	//   chain:    Mix "● Mix       mono/44.1㎑"        = 23
	//   analysis: Dynamics "● Dynamics     20.0 LU → 2.5:1" = 30
	// Sized against the widest plausible values (3-digit dB, 2-digit LRA, longest
	// sample rate), so realistic values never overflow fitWidth's hard truncate.
	chainBoxInnerWidth    = 23
	analysisBoxInnerWidth = 30

	// chainLabelWidth / analysisLabelWidth reserve a column for the row label so the
	// values align. The glyph + space sits to the left of the label. Each is the
	// longest label + a 2-space gap to the value: chain "De-esser" (8) + 2 = 10;
	// analysis "Noise floor" (11) + 2 = 13.
	chainLabelWidth    = 10
	analysisLabelWidth = 13

	// statusBoxChrome is the horizontal chrome per box: RoundedBorder (2) +
	// Padding(0,1) (2). Used by the narrow-terminal fit test.
	statusBoxChrome = 4

	// separationBarWidth is the cell count of the inline voice/noise bar.
	separationBarWidth = 3
)

// Row-state glyphs. ● active/known, ○ off/disabled and pending (value not yet
// produced). A pending row reads as ○ … ⋯ (the ⋯ value placeholder), distinct
// from an off row's ○ … OFF.
const (
	glyphActive  = "●"
	glyphOff     = "○"
	glyphPending = glyphOff
	valuePending = "⋯"
)

// Square Unicode unit glyphs (East-Asian WIDE, display width 2). lipgloss.Width
// measures them as width 2, so fitWidth pads rows correctly and columns stay
// aligned. unitDBTP is the dB glyph plus "TP".
const (
	unitDB   = "㏈"   // U+33C8, replaces "dB"
	unitKHz  = "㎑"   // U+3391, replaces "kHz"
	unitHz   = "㎐"   // U+3390, replaces "Hz"
	unitDBTP = "㏈TP" // dB glyph + TP, replaces "dBTP"
)

// statusBoxesFit reports whether the terminal is wide enough to place the two
// status boxes beside the Pass box. Below this the boxes are dropped and only the
// Pass box renders, so the Pass box never wraps or breaks on narrow terminals.
func statusBoxesFit(termWidth int) bool {
	// Pass box outer width (meterWidth content + chrome) plus both side boxes' outer
	// widths, with a small margin so the row never reaches the terminal edge.
	passOuter := meterWidth + statusBoxChrome
	chainOuter := chainBoxInnerWidth + statusBoxChrome
	analysisOuter := analysisBoxInnerWidth + statusBoxChrome
	// + 2 single-space separators between the three boxes, + 2 edge margin.
	return termWidth >= passOuter+chainOuter+analysisOuter+2+2
}

// joinStatusBoxes places the Filter Chain and Analysis boxes to the right of the
// rendered Pass box via lipgloss.JoinHorizontal, matching their height to the Pass
// box so the three panels line up at the top. On a narrow terminal the side boxes
// are dropped and the Pass box is returned unchanged.
func joinStatusBoxes(passBox string, file *FileProgress, termWidth int) string {
	if !statusBoxesFit(termWidth) {
		return passBox
	}

	// Match the side boxes to the Pass box's rendered height (it varies: the level
	// meter rows only appear once a level is seen). lipgloss pads the shorter boxes
	// with blank lines so the top edges align.
	passHeight := lipgloss.Height(passBox)

	// Reuse the memoised panels when the cache key matches. The panels depend only
	// on (Summary, passHeight): box widths/glyphs/units are compile-time constants
	// and the palette is fixed at startup. termWidth never enters the panel render
	// (the inner widths are constants), so it is not part of the key; it only gates
	// the early return above. AdaptedSummary is comparable, so == is a complete key
	// check. A height change (meter rows appearing/disappearing) re-renders here.
	c := &file.statusBoxCache
	if !c.valid || c.summary != file.Summary || c.joinHeight != passHeight {
		c.chain = renderChainBox(file.Summary, passHeight)
		c.analysis = renderAnalysisBox(file.Summary, passHeight)
		c.summary = file.Summary
		c.joinHeight = passHeight
		c.valid = true
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, passBox, " ", c.chain, " ", c.analysis)
}

// statusBox builds a bordered status box from pre-rendered content rows. The
// title sits IN the top border (╭─Title──╮), matching the Pass box. The border
// is sky-blue while the chain is pending (in step with the active Pass box),
// settling to indigo once the chain is known. Height pads the box to match the
// Pass box.
func statusBox(title string, innerWidth, height int, ready bool, rows []string) string {
	border := cli.ColorSkyBlue
	if ready {
		border = cli.ColorIndigo
	}
	// No Width() on the style: lipgloss's wrap counts the ●/○ glyphs as ambiguous
	// width-2 runes and would re-wrap a row that fitWidth already sized. Rows are
	// pre-padded to innerWidth below, so the border hugs them without wrapping.
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1)
	if height > 0 {
		// Height is the inner content height: the frame adds 2 border rows, matching
		// JoinHorizontal's top alignment against the Pass box.
		style = style.Height(height - 2)
	}

	// Pad every row to exactly innerWidth columns so the box never re-wraps a row
	// onto a second line: the glyph (●/○) is an ambiguous-width rune, so relying
	// on the box's own word-wrap is fragile. fitWidth measures display columns and
	// pads (or hard-truncates) deterministically.
	padded := make([]string, 0, len(rows))
	for _, r := range rows {
		padded = append(padded, fitWidth(r, innerWidth))
	}
	content := strings.Join(padded, "\n")
	rendered := style.Render(content)
	return overlayBorderTitle(rendered, title, border)
}

// overlayBorderTitle splices a title into the rendered box's top border line,
// producing ╭─Title─────╮ in the style of the Pass box. It rewrites only the
// first line (the top border): it keeps the leading ╭─, lays the muted title
// over the following dashes, then keeps the remaining dashes and the ╮ corner so
// the border width is unchanged. Lipgloss v2.0.3 has no native border-title API,
// so this overlay is the mechanism.
func overlayBorderTitle(box, title string, borderColor color.Color) string {
	nl := strings.IndexByte(box, '\n')
	if nl < 0 {
		return box
	}
	top, rest := box[:nl], box[nl:]

	// The top border is styled (ANSI-wrapped). Strip styling to operate on the raw
	// runes, then restyle the whole line in the border colour. The title itself is
	// muted so it reads as a label, not as border.
	plain := ansi.Strip(top)
	runes := []rune(plain)
	// Need ╭ ─ <title> … ╮: at least the two corners plus one dash each side.
	titleRunes := []rune(title)
	if len(runes) < len(titleRunes)+4 {
		return box
	}

	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	titleStyle := lipgloss.NewStyle().Foreground(cli.ColorMuted)

	var b strings.Builder
	// ╭─ lead-in.
	b.WriteString(borderStyle.Render(string(runes[0:2])))
	b.WriteString(titleStyle.Render(title))
	// Remaining border dashes + ╮, preserving the original line width.
	tail := runes[2+len(titleRunes):]
	b.WriteString(borderStyle.Render(string(tail)))

	return b.String() + rest
}

// fitWidth pads s with trailing spaces to exactly width display columns, or
// hard-truncates (dropping trailing ANSI styling) if it overflows. Used so each
// status-box row occupies exactly one line.
func fitWidth(s string, width int) string {
	w := lipgloss.Width(s)
	if w == width {
		return s
	}
	if w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return ansi.Truncate(s, width, "")
}

// statusRow renders one box row: a state glyph, a fixed-width label, and a value.
// glyph and value carry the row's state colour; the label is muted.
func statusRow(glyph string, glyphColor color.Color, label string, labelWidth int, value string, valueColor color.Color) string {
	g := lipgloss.NewStyle().Foreground(glyphColor).Render(glyph)
	l := lipgloss.NewStyle().Foreground(cli.ColorMuted).Width(labelWidth).Render(label)
	v := lipgloss.NewStyle().Foreground(valueColor).Render(value)
	return fmt.Sprintf("%s %s%s", g, l, v)
}

// pendingRow renders a dim row whose value is not yet known (○ label ⋯).
func pendingRow(label string, labelWidth int) string {
	return statusRow(glyphPending, cli.ColorMuted, label, labelWidth, valuePending, cli.ColorMuted)
}

// activeRow renders a lit row (● label value) in the active text colour.
func activeRow(label string, labelWidth, _ int, value string) string {
	return statusRow(glyphActive, cli.ColorGreen, label, labelWidth, value, cli.ColorText)
}

// offRow renders a disabled row (○ label OFF) entirely dim.
func offRow(label string, labelWidth int, value string) string {
	return statusRow(glyphOff, cli.ColorMuted, label, labelWidth, value, cli.ColorMuted)
}

// renderChainBox renders the Filter Chain box. Until the chain is known (Pass 1)
// every row is pending; once known each row lights to its value (or settles off).
// The Limiter row stays pending until completion supplies the ceiling.
func renderChainBox(s AdaptedSummary, height int) string {
	w := chainLabelWidth
	if !s.ChainReady {
		rows := []string{
			pendingRow("Downmix", w),
			pendingRow("Hi-pass", w),
			pendingRow("Lo-pass", w),
			pendingRow("Denoise", w),
			pendingRow("Gate", w),
			pendingRow("Comp", w),
			pendingRow("De-esser", w),
			pendingRow("Limiter", w),
		}
		return statusBox("Filter Chain", chainBoxInnerWidth, height, false, rows)
	}

	mix := "—"
	if s.DownmixMono {
		mix = "mono"
	}
	if s.SampleRate > 0 {
		mix = fmt.Sprintf("%s/%s", mix, formatSampleRate(s.SampleRate))
	}

	denoise := "—"
	switch {
	case s.DenoiseNLM && s.DenoiseFFT:
		denoise = "NLM+FFT"
	case s.DenoiseNLM:
		denoise = "NLM"
	case s.DenoiseFFT:
		denoise = "FFT"
	}

	deEsser := offRow("De-esser", w, "OFF")
	if s.DeesserOn {
		deEsser = activeRow("De-esser", w, 0, fmt.Sprintf("i=%.2f", s.DeesserI))
	}

	limiter := pendingRow("Limiter", w)
	if s.LimiterReady {
		if s.LimiterEnabled {
			limiter = activeRow("Limiter", w, 0, fmt.Sprintf("%.1f %s", s.LimiterCeiling, unitDBTP))
		} else {
			limiter = offRow("Limiter", w, "OFF")
		}
	}

	rows := []string{
		activeRow("Downmix", w, 0, mix),
		activeRow("Hi-pass", w, 0, formatHz(s.HighPassHz)),
		activeRow("Lo-pass", w, 0, formatHz(s.LowPassHz)),
		activeRow("Denoise", w, 0, denoise),
		activeRow("Gate", w, 0, fmt.Sprintf("%.1f %s", s.GateThreshDB, unitDB)),
		activeRow("Comp", w, 0, fmt.Sprintf("%.1f %s", s.CompThreshDB, unitDB)),
		deEsser,
		limiter,
	}
	return statusBox("Filter Chain", chainBoxInnerWidth, height, true, rows)
}

// renderAnalysisBox renders the Analysis box: the Pass-1 measurements that drove
// the chain. All rows light together at Pass-2 start. Rows with no measurement
// (no SpeechProfile / no band data) stay dim/unavailable.
func renderAnalysisBox(s AdaptedSummary, height int) string {
	w := analysisLabelWidth
	if !s.ChainReady {
		rows := []string{
			pendingRow("Voice avg", w),
			pendingRow("Noise floor", w),
			pendingRow("SNR Gap", w),
			pendingRow("Dynamics", w),
			pendingRow("True peak", w),
			pendingRow("Soft gate", w),
			pendingRow("Sibilance", w),
			pendingRow("Loudness", w),
		}
		return statusBox("Analysis", analysisBoxInnerWidth, height, false, rows)
	}

	voiceAvg := offRow("Voice avg", w, valuePending)
	if s.HasSpeech {
		voiceAvg = activeRow("Voice avg", w, 0, fmt.Sprintf("%.1f %s", s.VoiceAvgDB, unitDB))
	}

	separation := offRow("SNR Gap", w, valuePending)
	if s.HasSpeech {
		separation = statusRow(glyphActive, cli.ColorGreen, "SNR Gap", w,
			fmt.Sprintf("%.0f %s %s", s.SeparationDB, unitDB, separationBar(s.SeparationDB)), cli.ColorText)
	}

	sibilance := offRow("Sibilance", w, valuePending)
	if s.HasSibilance {
		sibilance = activeRow("Sibilance", w, 0, fmt.Sprintf("%.0f %s", s.SibilanceDB, unitDB))
	}

	gentle := offRow("Soft gate", w, "OFF")
	if s.GentleMode {
		gentle = activeRow("Soft gate", w, 0, "ON")
	}

	// Soft Gate (gate gentle mode) on row 6 and Sibilance on row 7 so Sibilance lines up with the
	// De-esser at Filter Chain row 7 (its driver). Loudness stays the bottom row.
	rows := []string{
		voiceAvg,
		activeRow("Noise floor", w, 0, fmt.Sprintf("%.0f %s", s.NoiseFloorDB, unitDB)),
		separation,
		activeRow("Dynamics", w, 0, fmt.Sprintf("%.1f LU → %.1f:1", s.InputLRA, s.GateRatio)),
		activeRow("True peak", w, 0, fmt.Sprintf("%.1f %s", s.TruePeakDBTP, unitDBTP)),
		gentle,
		sibilance,
		activeRow("Loudness", w, 0, fmt.Sprintf("%.1f LUFS", s.InputLUFS)),
	}
	return statusBox("Analysis", analysisBoxInnerWidth, height, true, rows)
}

// separationBar renders the inline voice/noise bar: a fill proportional to the
// separation over a 0-60 dB span, coloured by a green→yellow→red Blend1D ramp so
// wider separation reads greener (cleaner). Reuses the meter's colour grammar.
func separationBar(separationDB float64) string {
	const span = 60.0
	frac := separationDB / span
	frac = max(0, min(frac, 1))
	filled := int(frac*float64(separationBarWidth) + 0.5)
	filled = max(0, min(filled, separationBarWidth))

	ramp := lipgloss.Blend1D(separationBarWidth, cli.ColorRed, cli.ColorYellow, cli.ColorGreen)
	return renderFilledBar(separationBarWidth, filled, ramp)
}

// formatHz renders a frequency as "80 ㎐" below 1 kHz and "20.5 ㎑" at/above,
// trimming a trailing ".0". The square unit glyphs (㎐/㎑) are display width 2.
func formatHz(hz float64) string {
	if hz >= 1000 {
		return strings.TrimSuffix(fmt.Sprintf("%.1f", hz/1000), ".0") + " " + unitKHz
	}
	return fmt.Sprintf("%.0f %s", hz, unitHz)
}

// formatSampleRate renders a sample rate in kHz, e.g. 44100 → "44.1㎑", 48000 →
// "48㎑". The square unit glyph (㎑, U+3391) is East-Asian WIDE (display width 2);
// lipgloss.Width measures it as 2, so fitWidth keeps the Mix row aligned.
func formatSampleRate(hz int) string {
	return strings.TrimSuffix(fmt.Sprintf("%.1f", float64(hz)/1000), ".0") + unitKHz
}
