package ui

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/linuxmatters/jivetalking/internal/cli"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// TestSpeedFraction verifies the badge fraction un-scales Pass 1's capped progress
// and passes other passes through unchanged.
func TestSpeedFraction(t *testing.T) {
	tests := []struct {
		name     string
		pass     processor.PassNumber
		progress float64
		want     float64
	}{
		{"pass 1 mid un-scales", processor.PassAnalysis, 0.475, 0.5},
		{"pass 1 at cap reaches 1.0", processor.PassAnalysis, processor.BandPhaseProgressStart, 1.0},
		{"pass 1 above cap clamps to 1.0", processor.PassAnalysis, 0.97, 1.0},
		{"pass 2 passes through", processor.PassProcessing, 0.5, 0.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := speedFraction(tc.pass, tc.progress)
			if math.Abs(got-tc.want) > 1e-9 {
				t.Fatalf("speedFraction(%v, %v) = %v, want %v", tc.pass, tc.progress, got, tc.want)
			}
		})
	}
}

// perFrameMeterRamp is the oracle: the exact per-frame ramp-build logic that
// renderAudioLevelMeter ran before the ramp was cached. The cached meterRamp()
// must produce a byte-identical slice and identical rendered meters.
func perFrameMeterRamp() []color.Color {
	width := meterWidth
	minDB := meterFloorDB
	maxDB := 0.0
	greenZone := int((((-16.0) - minDB) / (maxDB - minDB)) * float64(width))
	greenZone = max(0, min(greenZone, width))

	ramp := make([]color.Color, 0, width)
	ramp = append(ramp, lipgloss.Blend1D(greenZone, cli.ColorGreen, cli.ColorYellow)...)
	ramp = append(ramp, lipgloss.Blend1D(width-greenZone, cli.ColorYellow, cli.ColorOrange, cli.ColorRed)...)
	return ramp
}

// colorsEqual compares two color.Color values by their resolved RGBA channels.
func colorsEqual(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// TestMeterRampMatchesPerFrame asserts the cached ramp is cell-for-cell identical
// to the previous per-frame computation.
func TestMeterRampMatchesPerFrame(t *testing.T) {
	want := perFrameMeterRamp()
	got := meterRamp()

	if len(got) != len(want) {
		t.Fatalf("ramp length = %d, want %d", len(got), len(want))
	}
	if len(got) != meterWidth {
		t.Fatalf("ramp length = %d, want meterWidth %d", len(got), meterWidth)
	}
	for i := range want {
		if !colorsEqual(got[i], want[i]) {
			wr, wg, wb, _ := want[i].RGBA()
			gr, gg, gb, _ := got[i].RGBA()
			t.Errorf("ramp[%d] = (%d,%d,%d), want (%d,%d,%d)",
				i, gr>>8, gg>>8, gb>>8, wr>>8, wg>>8, wb>>8)
		}
	}
}

// TestMeterRampStableAcrossCalls asserts repeated calls return the same cached
// slice (identity), confirming the once-only build.
func TestMeterRampStableAcrossCalls(t *testing.T) {
	a := meterRamp()
	b := meterRamp()
	if &a[0] != &b[0] {
		t.Errorf("meterRamp() returned distinct backing arrays across calls; expected a single cached slice")
	}
}

// TestMeterRampStylesMatchRamp asserts the cached style slice has one style per
// ramp colour (same length) and that each style's foreground resolves to the
// matching ramp colour, so the flush's position-indexed style matches the
// per-cell colour the oracle paints. It also checks the off-ramp fallback style
// resolves to cli.ColorRed, matching cellColor's out-of-range branch.
func TestMeterRampStylesMatchRamp(t *testing.T) {
	ramp := meterRamp()
	styles := meterRampStyles()

	if len(styles) != len(ramp) {
		t.Fatalf("meterRampStyles length = %d, want ramp length %d", len(styles), len(ramp))
	}
	for i := range ramp {
		if !colorsEqual(styles[i].GetForeground(), ramp[i]) {
			sr, sg, sb, _ := styles[i].GetForeground().RGBA()
			rr, rg, rb, _ := ramp[i].RGBA()
			t.Errorf("style[%d] foreground = (%d,%d,%d), want ramp (%d,%d,%d)",
				i, sr>>8, sg>>8, sb>>8, rr>>8, rg>>8, rb>>8)
		}
	}

	// Off-ramp fallback: cellColor returns cli.ColorRed for an out-of-range index;
	// the flush uses meterOffRampStyle for the same case.
	if !colorsEqual(meterOffRampStyle.GetForeground(), cli.ColorRed) {
		or, og, ob, _ := meterOffRampStyle.GetForeground().RGBA()
		rr, rg, rb, _ := cli.ColorRed.RGBA()
		t.Errorf("meterOffRampStyle foreground = (%d,%d,%d), want cli.ColorRed (%d,%d,%d)",
			or>>8, og>>8, ob>>8, rr>>8, rg>>8, rb>>8)
	}
}

// TestRenderAudioLevelMeterMatchesOracle renders the meter across a range of dB
// fill levels and asserts the output equals a reference renderer that uses the
// per-frame ramp oracle. A mismatch means the cache changed the visible meter.
func TestRenderAudioLevelMeterMatchesOracle(t *testing.T) {
	levels := []float64{
		meterFloorDB, -70.0, -65.0, -60.0, -50.0, -40.0, -30.0,
		-20.0, -16.0, -12.0, -8.0, -6.0, -3.0, -1.0, 0.0,
	}
	for _, lvl := range levels {
		want := renderMeterWithRamp(lvl, perFrameMeterRamp())
		got := renderMeterWithRamp(lvl, meterRamp())
		if got != want {
			t.Errorf("meter(level=%g):\n got  %q\n want %q", lvl, got, want)
		}
	}
}

// peakMarkerColorOracle is the previous hex-string round-trip: interpolate the two
// oranges to 8-bit channels, format to "#RRGGBB", and parse back via
// lipgloss.Color. The struct-built peakMarkerColor must produce the identical
// resolved RGBA at every phase.
func peakMarkerColorOracle(elapsed time.Duration) color.Color {
	const pulseHz = 1.2
	phase := 0.5 * (1 + math.Sin(2*math.Pi*pulseHz*elapsed.Seconds()))

	dr, dg, db := rgb8(cli.ColorOrangeDim)
	br, bg, bb := rgb8(cli.ColorOrange)
	lerp := func(a, b uint8) uint8 {
		return uint8(float64(a) + phase*(float64(b)-float64(a)) + 0.5)
	}
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X",
		lerp(dr, br), lerp(dg, bg), lerp(db, bb)))
}

// TestPeakMarkerColorMatchesOracle samples the pulse across a full cycle and
// asserts the struct-built color.RGBA resolves to the same channels as the former
// hex-string path at every sampled phase. The samples span the sine's trough,
// crest, and mid-points so the rounding boundary is exercised.
func TestPeakMarkerColorMatchesOracle(t *testing.T) {
	// 64 samples across ~one pulse cycle (1/1.2 s), plus the exact trough and
	// crest phases, so the rounding of every interpolated channel is checked.
	var elapsings []time.Duration
	const cycle = time.Second * 10 / 12 // 1/pulseHz
	for i := 0; i <= 64; i++ {
		elapsings = append(elapsings, time.Duration(float64(i)/64.0*float64(cycle)))
	}
	// Add a few absolute offsets to vary the sine phase beyond one cycle.
	elapsings = append(elapsings, 0, time.Millisecond*208, time.Millisecond*417, 5*time.Second)

	for _, e := range elapsings {
		got := peakMarkerColor(e)
		want := peakMarkerColorOracle(e)
		if !colorsEqual(got, want) {
			gr, gg, gb, ga := got.RGBA()
			wr, wg, wb, wa := want.RGBA()
			t.Errorf("peakMarkerColor(%v) = (%d,%d,%d,%d), want (%d,%d,%d,%d)",
				e, gr>>8, gg>>8, gb>>8, ga>>8, wr>>8, wg>>8, wb>>8, wa>>8)
		}
	}
}

// renderMeterWithRamp reproduces renderAudioLevelMeter's bar-painting loop with a
// caller-supplied ramp, so the test can drive both the cached ramp and the oracle
// through identical rendering and compare the strings. It mirrors the production
// run-coalescing and cell logic; the peak-marker block is excluded because it does
// not touch the ramp.
func renderMeterWithRamp(currentLevel float64, ramp []color.Color) string {
	width := meterWidth
	minDB := meterFloorDB
	maxDB := 0.0
	currentPos := max(0, min(int(((currentLevel-minDB)/(maxDB-minDB))*float64(width)), width))

	cellColor := func(i int) color.Color {
		if i < 0 || i >= len(ramp) {
			return cli.ColorRed
		}
		return ramp[i]
	}
	meterChar := func(i int) rune {
		if i < currentPos {
			return '▓'
		}
		return '░'
	}

	var sb, runBuf strings.Builder
	var runColor color.Color
	flush := func() {
		if runBuf.Len() == 0 {
			return
		}
		sb.WriteString(lipgloss.NewStyle().Foreground(runColor).Render(runBuf.String()))
		runBuf.Reset()
	}
	for i := range width {
		c := cellColor(i)
		if runBuf.Len() > 0 && c != runColor {
			flush()
		}
		runColor = c
		runBuf.WriteRune(meterChar(i))
	}
	flush()
	return sb.String()
}

// TestDoneBoxNoiseFloorRow verifies the room-tone floor row. It checks the
// input→output pair when both ends exist, the single value when one end is
// missing, the "< -96" clamp on each end (finite deep floors and -Inf), and the
// "n/a" placeholder when neither end exists.
func TestDoneBoxNoiseFloorRow(t *testing.T) {
	cases := []struct {
		name       string
		input      float64
		output     float64
		haveInput  bool
		haveOutput bool
		contains   []string
		exact      string
	}{
		{
			name: "both ends pair", input: -64, output: -82,
			haveInput: true, haveOutput: true,
			contains: []string{"-64", "→", "-82", "㏈"},
		},
		{
			name: "output only", output: -82,
			haveOutput: true, exact: "-82 ㏈",
		},
		{
			name: "input only", input: -64,
			haveInput: true, exact: "-64 ㏈",
		},
		{
			name: "clamp finite deep floor both ends", input: -97, output: -120,
			haveInput: true, haveOutput: true,
			contains: []string{"< -96", "→"},
		},
		{
			name: "clamp negative infinity output", input: -70, output: math.Inf(-1),
			haveInput: true, haveOutput: true,
			contains: []string{"-70", "→", "< -96"},
		},
		{
			name: "neither end", exact: "n/a",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := doneBoxNoiseFloorRow(tc.input, tc.output, tc.haveInput, tc.haveOutput)
			if tc.exact != "" && strings.TrimSpace(got) != tc.exact {
				t.Errorf("doneBoxNoiseFloorRow = %q, want %q", got, tc.exact)
			}
			for _, want := range tc.contains {
				if !strings.Contains(got, want) {
					t.Errorf("doneBoxNoiseFloorRow = %q, want to contain %q", got, want)
				}
			}
		})
	}
}
