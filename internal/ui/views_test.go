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
)

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
