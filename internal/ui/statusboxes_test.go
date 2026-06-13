package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// litSummary is an in-memory AdaptedSummary with the chain + analysis rows known
// but the limiter still pending. Mirrors the spec mockup values.
func litSummary() AdaptedSummary {
	return AdaptedSummary{
		ChainReady:   true,
		DownmixMono:  true,
		SampleRate:   44100,
		HighPassHz:   80,
		LowPassHz:    20500,
		DenoiseNLM:   true,
		DenoiseFFT:   true,
		GateThreshDB: -42.1,
		CompThreshDB: -11.9,
		DeesserOn:    false,
		DeesserI:     0,
		HasSpeech:    true,
		VoiceAvgDB:   -20.9,
		NoiseFloorDB: -68,
		SeparationDB: 47,
		InputLRA:     8.2,
		GateRatio:    2.0,
		TruePeakDBTP: -3.2,
		HasSibilance: true,
		SibilanceDB:  -4,
		GentleMode:   true,
		InputLUFS:    -24.3,
		TargetLUFS:   -16,
	}
}

// TestChainBoxPendingRows confirms that before the chain is known every Filter
// Chain row shows the pending glyph ○ and the ⋯ placeholder, not a value. The
// ⋯ placeholder distinguishes a pending row (○ … ⋯) from an off row (○ … OFF).
func TestChainBoxPendingRows(t *testing.T) {
	plain := ansi.Strip(renderChainBox(AdaptedSummary{}, 0))

	if !strings.Contains(plain, "Filter Chain") {
		t.Fatalf("chain box missing title:\n%s", plain)
	}
	for _, label := range []string{"Downmix", "Hi-pass", "Lo-pass", "Denoise", "Gate", "Comp", "De-esser", "Limiter"} {
		if !strings.Contains(plain, label) {
			t.Errorf("chain box missing %q row:\n%s", label, plain)
		}
	}
	if !strings.Contains(plain, glyphPending) || !strings.Contains(plain, valuePending) {
		t.Errorf("pending chain box should show ○ and ⋯:\n%s", plain)
	}
	// No lit glyph yet.
	if strings.Contains(plain, "80 "+unitHz) {
		t.Errorf("pending chain box should not show values:\n%s", plain)
	}
}

// TestPendingVsOffRow confirms a pending row (○ … ⋯) reads distinctly from an off
// row (○ … OFF): both share the ○ glyph, but the pending value is the ⋯ placeholder
// while an off value is OFF. The lit chain box carries both (Limiter pending, De-esser
// off), so a single render exercises the distinction.
func TestPendingVsOffRow(t *testing.T) {
	plain := ansi.Strip(renderChainBox(litSummary(), 0))

	// Pending Limiter row: ○ glyph + ⋯ placeholder, never OFF.
	if !strings.Contains(plain, "Limiter") || !strings.Contains(plain, glyphOff+" ") || !strings.Contains(plain, valuePending) {
		t.Errorf("pending Limiter row should read ○ … ⋯:\n%s", plain)
	}
	// Off De-esser row: ○ glyph + OFF, never the ⋯ placeholder on that row.
	for line := range strings.SplitSeq(plain, "\n") {
		if strings.Contains(line, "De-esser") {
			if !strings.Contains(line, glyphOff) || !strings.Contains(line, "OFF") {
				t.Errorf("off De-esser row should read ○ … OFF:\n%s", line)
			}
			if strings.Contains(line, valuePending) {
				t.Errorf("off row should not carry the ⋯ pending placeholder:\n%s", line)
			}
		}
	}
}

// TestFormatSampleRate confirms the Mix sample rate uses the ㎑ glyph (U+3391) and
// trims a trailing ".0" (44100 → 44.1㎑, 48000 → 48㎑), and that lipgloss measures
// the wide glyph as width 2 so the Mix row stays aligned via fitWidth.
func TestFormatSampleRate(t *testing.T) {
	for _, tc := range []struct {
		hz   int
		want string
	}{
		{44100, "44.1" + unitKHz},
		{48000, "48" + unitKHz},
	} {
		if got := formatSampleRate(tc.hz); got != tc.want {
			t.Errorf("formatSampleRate(%d) = %q, want %q", tc.hz, got, tc.want)
		}
	}
	if w := lipgloss.Width(unitKHz); w != 2 {
		t.Errorf("㎑ should measure as display width 2 (East-Asian wide), got %d", w)
	}
}

// TestChainBoxLitRows confirms each chain row lights to its value once known, the
// De-esser settles to ○ OFF, and the Limiter stays pending until completion.
func TestChainBoxLitRows(t *testing.T) {
	plain := ansi.Strip(renderChainBox(litSummary(), 0))

	for _, want := range []string{"mono/44.1" + unitKHz, "80 " + unitHz, "20.5 " + unitKHz, "NLM+FFT", "-42.1 " + unitDB, "-11.9 " + unitDB} {
		if !strings.Contains(plain, want) {
			t.Errorf("lit chain box missing %q:\n%s", want, plain)
		}
	}
	// De-esser disabled → ○ OFF.
	if !strings.Contains(plain, glyphOff) || !strings.Contains(plain, "OFF") {
		t.Errorf("disabled de-esser should show ○ OFF:\n%s", plain)
	}
	// Limiter still pending (no ceiling yet).
	if !strings.Contains(plain, "Limiter") || !strings.Contains(plain, valuePending) {
		t.Errorf("limiter should stay pending until completion:\n%s", plain)
	}
	if !strings.Contains(plain, glyphActive) {
		t.Errorf("lit chain box should show the active glyph ●:\n%s", plain)
	}
}

// TestChainBoxLimiterLitDuringPass4 confirms the Pass-4 limiter snapshot
// (WithLimiterProgress) lights the row to its ceiling DURING processing, before
// completion. This is the fix for the row never resolving on a still-rendering box.
func TestChainBoxLimiterLitDuringPass4(t *testing.T) {
	s := litSummary().WithLimiterProgress(&processor.LimiterProgress{
		Enabled: true,
		Ceiling: -2.8,
	})
	plain := ansi.Strip(renderChainBox(s, 0))

	if !strings.Contains(plain, "-2.8 "+unitDBTP) {
		t.Errorf("Pass-4 limiter should show its ceiling -2.8 dBTP:\n%s", plain)
	}
	if strings.Contains(plain, "Limiter   "+valuePending) {
		t.Errorf("Pass-4 limiter should no longer be pending:\n%s", plain)
	}

	// Disabled limiter settles to OFF, still resolved (not pending).
	off := ansi.Strip(renderChainBox(litSummary().WithLimiterProgress(&processor.LimiterProgress{Enabled: false}), 0))
	if strings.Contains(off, "Limiter   "+valuePending) {
		t.Errorf("disabled Pass-4 limiter should settle to OFF, not pending:\n%s", off)
	}
}

// TestChainBoxDeesserEngaged confirms an engaged de-esser lights to ● i=value.
func TestChainBoxDeesserEngaged(t *testing.T) {
	s := litSummary()
	s.DeesserOn = true
	s.DeesserI = 0.62
	plain := ansi.Strip(renderChainBox(s, 0))

	if !strings.Contains(plain, "i=0.62") {
		t.Errorf("engaged de-esser should show i=0.62:\n%s", plain)
	}
}

// TestChainBoxLimiterLit confirms the limiter row lights with its ceiling once the
// summary carries the completion limiter data.
func TestChainBoxLimiterLit(t *testing.T) {
	s := litSummary().WithLimiter(&processor.NormalisationResult{
		LimiterEnabled: true,
		LimiterCeiling: -2.8,
	})
	plain := ansi.Strip(renderChainBox(s, 0))

	if !strings.Contains(plain, "-2.8 "+unitDBTP) {
		t.Errorf("lit limiter should show its ceiling -2.8 dBTP:\n%s", plain)
	}
	if strings.Contains(plain, "Limiter   "+valuePending) {
		t.Errorf("limiter should no longer be pending:\n%s", plain)
	}
}

// TestAnalysisBoxLitRows confirms each Analysis row lights to its measurement,
// including the inline separation bar and the gentle-mode state.
func TestAnalysisBoxLitRows(t *testing.T) {
	plain := ansi.Strip(renderAnalysisBox(litSummary(), 0))

	for _, want := range []string{"Analysis", "SNR Gap", "-20.9 " + unitDB, "-68 " + unitDB, "47 " + unitDB, "8.2 LU → 2.0:1", "-3.2 " + unitDBTP, "-4 " + unitDB, "-24.3 LUFS"} {
		if !strings.Contains(plain, want) {
			t.Errorf("lit analysis box missing %q:\n%s", want, plain)
		}
	}
	// Voice/noise separation bar present (filled block rune).
	if !strings.Contains(plain, "▰") {
		t.Errorf("separation row should show an inline bar:\n%s", plain)
	}
	// Longest Analysis label "Noise floor" (11) has exactly a 2-space gap to its
	// value: analysisLabelWidth (13) − 11 = 2 trailing pad spaces.
	if !strings.Contains(plain, "Noise floor  -68 "+unitDB) {
		t.Errorf("Noise floor should have a 2-space gap before its value:\n%s", plain)
	}
	// Soft Gate on → ● ON (caps).
	if !strings.Contains(plain, "Soft gate") || !strings.Contains(plain, "ON") {
		t.Errorf("Soft Gate should show its state in caps:\n%s", plain)
	}
}

// TestAnalysisBoxNoSpeechDims confirms the speech-dependent rows fall back to a dim
// placeholder when no SpeechProfile was elected, while the always-available rows
// (noise floor, true peak, loudness) still light.
func TestAnalysisBoxNoSpeechDims(t *testing.T) {
	s := litSummary()
	s.HasSpeech = false
	s.HasSibilance = false
	plain := ansi.Strip(renderAnalysisBox(s, 0))

	// Speech-dependent rows show the placeholder.
	if !strings.Contains(plain, "Voice avg") || !strings.Contains(plain, valuePending) {
		t.Errorf("no-speech analysis box should dim the Voice avg row:\n%s", plain)
	}
	// Always-available rows still light.
	if !strings.Contains(plain, "-68 "+unitDB) || !strings.Contains(plain, "-3.2 "+unitDBTP) {
		t.Errorf("no-speech analysis box should still light noise/true-peak rows:\n%s", plain)
	}
}

// TestJoinStatusBoxesLayout confirms the wide-terminal layout joins the Pass box
// and the two status boxes side by side ([Pass][Chain][Analysis]): on a single
// rendered line all three titles appear, and the Chain title precedes the Analysis
// title to the right of the Pass content.
func TestJoinStatusBoxesLayout(t *testing.T) {
	leftBox := "╭──────────╮\n│ passbox  │\n╰──────────╯"
	out := ansi.Strip(joinStatusBoxes(leftBox, litSummary(), 160))

	if !strings.Contains(out, "Filter Chain") || !strings.Contains(out, "Analysis") {
		t.Fatalf("joined layout missing the side boxes:\n%s", out)
	}

	// On the title line, Pass content sits left of Filter Chain, which sits left of
	// Analysis. JoinHorizontal places them on shared lines, so a single line carries
	// all three.
	var titleLine string
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, "Filter Chain") && strings.Contains(line, "Analysis") {
			titleLine = line
			break
		}
	}
	if titleLine == "" {
		t.Fatalf("expected a line carrying both side-box titles:\n%s", out)
	}
	chainIdx := strings.Index(titleLine, "Filter Chain")
	analysisIdx := strings.Index(titleLine, "Analysis")
	if chainIdx >= analysisIdx {
		t.Errorf("Filter Chain should sit left of Analysis: chain=%d analysis=%d\n%s", chainIdx, analysisIdx, titleLine)
	}
}

// TestJoinStatusBoxesHeightMatch confirms the side boxes pad to at least the Pass
// box height so the three panels align at the top. The Pass box here is short (3
// lines); the 8-row status boxes are taller and must not be truncated.
func TestJoinStatusBoxesHeightMatch(t *testing.T) {
	leftBox := "╭──────────╮\n│ passbox  │\n╰──────────╯"
	out := joinStatusBoxes(leftBox, litSummary(), 160)
	lines := strings.Count(out, "\n") + 1

	// 8 data rows + 2 border rows (title in the top border) = 10 lines for a status
	// box; the joined block is at least that tall (taller than the 3-line Pass box).
	if lines < 10 {
		t.Errorf("joined block should be at least the status-box height (10), got %d:\n%s", lines, ansi.Strip(out))
	}

	// A tall Pass box (12 lines) must drive the side boxes to match, never truncate.
	tallPanel := "╭────╮\n" + strings.Repeat("│ x  │\n", 10) + "╰────╯"
	tallOut := joinStatusBoxes(tallPanel, litSummary(), 160)
	tallLines := strings.Count(tallOut, "\n") + 1
	if tallLines < strings.Count(tallPanel, "\n")+1 {
		t.Errorf("joined block must be at least the Pass box height, got %d:\n%s", tallLines, ansi.Strip(tallOut))
	}
	// All eight chain rows survive the height match (no truncation).
	plainTall := ansi.Strip(tallOut)
	if !strings.Contains(plainTall, "Limiter") || !strings.Contains(plainTall, "Loudness") {
		t.Errorf("height match should not truncate status-box rows:\n%s", plainTall)
	}
}

// TestJoinStatusBoxesNarrowDegrades confirms that on a narrow terminal the side
// boxes are dropped and the Pass box is returned unchanged (never wrapped/broken).
func TestJoinStatusBoxesNarrowDegrades(t *testing.T) {
	leftBox := "╭──────────╮\n│ passbox  │\n╰──────────╯"

	narrow := joinStatusBoxes(leftBox, litSummary(), 60)
	if narrow != leftBox {
		t.Errorf("narrow terminal should return the Pass box unchanged, got:\n%s", ansi.Strip(narrow))
	}
	if strings.Contains(ansi.Strip(narrow), "Filter Chain") {
		t.Errorf("narrow terminal should drop the side boxes:\n%s", ansi.Strip(narrow))
	}

	// Wide terminal keeps them.
	wide := joinStatusBoxes(leftBox, litSummary(), 160)
	if !strings.Contains(ansi.Strip(wide), "Filter Chain") {
		t.Errorf("wide terminal should keep the side boxes:\n%s", ansi.Strip(wide))
	}
}

// TestBorderTitleInTopBorder confirms each box carries its title spliced into the
// top border (╭─Title─╮): the title sits on the first rendered line, between the
// ╭ corner and the ╮ corner, and the first data row sits directly beneath it (no
// blank first content row), matching how the Pass box's name row sits under its
// border title.
func TestBorderTitleInTopBorder(t *testing.T) {
	for _, tc := range []struct {
		name  string
		box   string
		title string
		first string // a label expected on the first content row (line 1)
	}{
		{"chain", ansi.Strip(renderChainBox(litSummary(), 0)), "Filter Chain", "Downmix"},
		{"analysis", ansi.Strip(renderAnalysisBox(litSummary(), 0)), "Analysis", "Voice avg"},
	} {
		lines := strings.Split(tc.box, "\n")
		if len(lines) < 3 {
			t.Fatalf("%s: too few lines:\n%s", tc.name, tc.box)
		}
		// Title in the top border line, framed by the rounded corners.
		top := lines[0]
		if !strings.HasPrefix(top, "╭") || !strings.HasSuffix(top, "╮") {
			t.Errorf("%s: top line is not a border: %q", tc.name, top)
		}
		if !strings.Contains(top, tc.title) {
			t.Errorf("%s: title %q not in top border: %q", tc.name, tc.title, top)
		}
		// First content row (line 1) carries the first data label directly: no blank row.
		if !strings.Contains(lines[1], tc.first) {
			t.Errorf("%s: expected %q on the first data row, got: %q", tc.name, tc.first, lines[1])
		}
	}
}

// TestPassBoxTitleInBorder confirms the Pass box splits "Pass N/4: <Name>" into a
// border title ("Pass N/4") and a first content row carrying only the pass name.
func TestPassBoxTitleInBorder(t *testing.T) {
	file := FileProgress{CurrentPass: processor.PassNormalising, Status: StatusNormalising}
	plain := ansi.Strip(renderFileDetails(file, newProgressModel(), 0, 0, 0))
	lines := strings.Split(plain, "\n")
	if len(lines) < 2 {
		t.Fatalf("pass box too short:\n%s", plain)
	}
	if !strings.Contains(lines[0], "Pass 4/4") {
		t.Errorf("Pass N/4 should sit in the top border: %q", lines[0])
	}
	// The combined "Pass N/4: Name" form is gone.
	if strings.Contains(plain, "Pass 4/4:") {
		t.Errorf("pass box should not carry the old 'Pass N/4:' content row:\n%s", plain)
	}
	if !strings.Contains(plain, "Normalising Audio") {
		t.Errorf("pass name should remain as a content row:\n%s", plain)
	}
}

// TestAnalysisRowOrder confirms Soft Gate sits on row 6 and Sibilance on row 7
// (level with the De-esser at Filter Chain row 7), with Loudness on the bottom
// data row.
func TestAnalysisRowOrder(t *testing.T) {
	plain := ansi.Strip(renderAnalysisBox(litSummary(), 0))
	gentle := strings.Index(plain, "Soft gate")
	sibilance := strings.Index(plain, "Sibilance")
	loudness := strings.Index(plain, "Loudness")
	truePeak := strings.Index(plain, "True peak")
	if gentle < 0 || sibilance < 0 || loudness < 0 || truePeak < 0 {
		t.Fatalf("missing a row:\n%s", plain)
	}
	// True peak (row 5) → Soft Gate (row 6) → Sibilance (row 7) → Loudness (row 8).
	if truePeak >= gentle || gentle >= sibilance || sibilance >= loudness {
		t.Errorf("row order wrong: truePeak=%d gentle=%d sibilance=%d loudness=%d\n%s",
			truePeak, gentle, sibilance, loudness, plain)
	}
}

// TestStatusBoxGutterSymmetric confirms the right gutter is one space, matching the
// one-space left gutter. The inner widths equal the widest row content, so a row
// that fills the inner width gets zero fitWidth trailing pad: only the box style's
// Padding(0,1) remains, leaving that row reading "… value │" with a single space
// before the border on both sides. Each box is fed a summary whose widest row fills
// the inner width exactly (chain: Mix "mono/44.1㎑" = 23; analysis: Dynamics
// "20.0 LU → 2.5:1" = 30, the widest plausible value the widths are sized to).
func TestStatusBoxGutterSymmetric(t *testing.T) {
	// Analysis summary whose Dynamics row fills the 30-col inner width.
	fullAnalysis := litSummary()
	fullAnalysis.InputLRA = 20.0
	fullAnalysis.GateRatio = 2.5

	for _, tc := range []struct {
		name string
		box  string
		// the value on the row that fills the inner width, hugging the border.
		longest string
	}{
		// Chain: the Mix row "mono/44.1㎑" is the widest (23 cols).
		{"chain", ansi.Strip(renderChainBox(litSummary(), 0)), "mono/44.1" + unitKHz},
		// Analysis: the Dynamics row "20.0 LU → 2.5:1" fills the 30-col inner width.
		{"analysis", ansi.Strip(renderAnalysisBox(fullAnalysis, 0)), "20.0 LU → 2.5:1"},
	} {
		var got string
		for line := range strings.SplitSeq(tc.box, "\n") {
			if strings.Contains(line, tc.longest) {
				got = line
				break
			}
		}
		if got == "" {
			t.Fatalf("%s: row carrying %q not found:\n%s", tc.name, tc.longest, tc.box)
		}
		// Left gutter: "│ " after the left border.
		if !strings.HasPrefix(got, "│ ") {
			t.Errorf("%s: row should open with a one-space left gutter: %q", tc.name, got)
		}
		// Right gutter: exactly one space between the value and the right border.
		want := tc.longest + " │"
		if !strings.HasSuffix(got, want) {
			t.Errorf("%s: widest row should hug the border with one space (…%q), got: %q", tc.name, want, got)
		}
	}
}

// TestProgressiveLightingBorder confirms the box border tracks readiness: sky-blue
// while pending (in step with the active Pass box), indigo once the chain is known.
func TestProgressiveLightingBorder(t *testing.T) {
	pending := renderChainBox(AdaptedSummary{}, 0)
	lit := renderChainBox(litSummary(), 0)

	// Indigo #6366F1 -> 99,102,241 appears only once lit.
	if strings.Contains(pending, "99;102;241") {
		t.Errorf("pending box should not use the indigo (lit) border")
	}
	if !strings.Contains(lit, "99;102;241") {
		t.Errorf("lit box should use the indigo border")
	}
}
