package processor

import (
	"math"
	"strings"
	"testing"
)

// TestGainAdviceKinds locks the four outcomes and their boundaries against the
// -6 dBTP target and the -1/-12 dBTP band edges (corpus sweep 2026-06-12).
func TestGainAdviceKinds(t *testing.T) {
	cases := []struct {
		name    string
		inputTP float64
		want    GainAdviceKind
	}{
		{"clipping at zero", 0.0, GainClipping},
		{"clipping above zero", 0.4, GainClipping},
		{"hot just under zero", -0.1, GainHot},
		{"hot mid-band", -0.5, GainHot},
		{"fine at hot boundary", gainAdviceHotTP, GainFine},     // -1.0 -> Fine
		{"fine mid-band", -6.2, GainFine},                       // 83-mark high-crest
		{"fine at quiet boundary", gainAdviceQuietTP, GainFine}, // -12.0 -> Fine
		{"quiet just below floor", -12.1, GainQuiet},
		{"quiet far below", -21.41, GainQuiet}, // 68-popey
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := GainAdvice(tc.inputTP).Kind; got != tc.want {
				t.Errorf("GainAdvice(%v).Kind = %v, want %v", tc.inputTP, got, tc.want)
			}
		})
	}
}

// TestGainAdviceDeltaSpotValues pins the signed gain delta for the documented
// corpus spot values: negative = lower, positive = raise, 0 = fine.
func TestGainAdviceDeltaSpotValues(t *testing.T) {
	cases := []struct {
		name      string
		inputTP   float64
		wantKind  GainAdviceKind
		wantDelta float64
	}{
		{"83-popey hot", -0.13, GainHot, -6},      // round(-0.13 - -6) = 6, lower
		{"68-popey quiet", -21.41, GainQuiet, 15}, // round(-6 - -21.41) = 15, raise
		{"83-mark fine", -6.21, GainFine, 0},
		{"78-martin clipping", 0.35, GainClipping, -6}, // round(0.35 - -6) = 6, lower
		{"hot boundary fine", -1.0, GainFine, 0},
		{"quiet boundary fine", -12.0, GainFine, 0},
		{"clip exact zero", 0.0, GainClipping, -6},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := GainAdvice(tc.inputTP)
			if got.Kind != tc.wantKind {
				t.Errorf("GainAdvice(%v).Kind = %v, want %v", tc.inputTP, got.Kind, tc.wantKind)
			}
			if got.DeltaDB != tc.wantDelta {
				t.Errorf("GainAdvice(%v).DeltaDB = %v, want %v", tc.inputTP, got.DeltaDB, tc.wantDelta)
			}
			if got.InputTP != tc.inputTP {
				t.Errorf("GainAdvice(%v).InputTP = %v, want %v", tc.inputTP, got.InputTP, tc.inputTP)
			}
		})
	}
}

// TestGainAdviceNonContradiction is the point of the feature: advice keys ONLY
// off the peak. A high-crest capture (peaks healthy at -6.2 dBTP but a very
// quiet -35 LUFS average) must return Fine, never "turn up". Average loudness is
// not even an input to GainAdvice, so this is structurally guaranteed; the test
// documents and guards it.
func TestGainAdviceNonContradiction(t *testing.T) {
	got := GainAdvice(-6.2) // the 'mark' case: healthy peaks, quiet average
	if got.Kind != GainFine {
		t.Errorf("high-crest -6.2 dBTP peak: Kind = %v, want GainFine (never 'turn up')", got.Kind)
	}
	if got.DeltaDB != 0 {
		t.Errorf("high-crest fine: DeltaDB = %v, want 0", got.DeltaDB)
	}
}

// TestGainAdviceMessage checks the prose surfaces the right direction word, the
// uniform "Peaks at" level clause (signed peak), and the absolute delta
// magnitude for each kind.
func TestGainAdviceMessage(t *testing.T) {
	cases := []struct {
		name     string
		inputTP  float64
		wantSubs []string
		notSubs  []string
	}{
		{"clipping", 0.35, []string{"Clipping.", "Peaks at +0.3 ㏈TP.", "Lower input gain ~6 ㏈."}, []string{"Raise", "baked in"}},
		{"hot", -0.13, []string{"Hot.", "Peaks at -0.1 ㏈TP.", "Lower input gain ~6 ㏈."}, []string{"Raise"}},
		{"quiet", -15.0, []string{"Quiet.", "Peaks at -15.0 ㏈TP.", "Raise input gain ~9 ㏈."}, []string{"Lower"}},
		{"fine", -6.2, []string{"Level well set.", "Peaks at -6.2 ㏈TP.", "No action required."}, []string{"Lower", "Raise"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := GainAdvice(tc.inputTP).Message()
			for _, sub := range tc.wantSubs {
				if !strings.Contains(msg, sub) {
					t.Errorf("message %q missing %q", msg, sub)
				}
			}
			for _, sub := range tc.notSubs {
				if strings.Contains(msg, sub) {
					t.Errorf("message %q must not contain %q", msg, sub)
				}
			}
		})
	}
}

// TestGainAdviceMessageNoBannedGlyphs guards the reformatted prose against the
// dropped decorations: no em dash, no check mark, no brackets in any zone.
func TestGainAdviceMessageNoBannedGlyphs(t *testing.T) {
	banned := []string{"—", "✓", "(", ")"}
	for _, tp := range []float64{0.35, -0.13, -15.0, -6.2} {
		msg := GainAdvice(tp).Message()
		for _, glyph := range banned {
			if strings.Contains(msg, glyph) {
				t.Errorf("GainAdvice(%v).Message() = %q must not contain %q", tp, msg, glyph)
			}
		}
	}
}

// TestGainAdviceCorpusDistribution asserts the 51-file corpus true-peak
// distribution lands ~11 Hot/Clipping (lower) / 4 Quiet (raise) / 36 Fine. The
// input-TP values are the corpus sweep figures (2026-06-12).
func TestGainAdviceCorpusDistribution(t *testing.T) {
	// 51 input true peaks (dBTP) from the corpus sweep. 11 above -1 (hot/clip),
	// 4 below -12 (quiet), 36 in the well-set band.
	corpus := []float64{
		// 11 hot/clipping (> -1 dBTP)
		-0.13, 0.35, -0.5, -0.9, -0.2, 0.1, -0.7, -0.4, 0.05, -0.8, -0.3,
		// 4 quiet (< -12 dBTP)
		-21.41, -14.0, -13.2, -16.5,
		// 36 fine (-12 .. -1 dBTP inclusive)
		-1.0, -12.0, -6.21, -4.9, -4.5, -2.0, -3.0, -5.0, -6.0, -7.0,
		-8.0, -9.0, -10.0, -11.0, -1.5, -2.5, -3.5, -4.0, -5.5, -6.5,
		-7.5, -8.5, -9.5, -10.5, -11.5, -2.2, -3.3, -4.4, -5.6, -6.7,
		-7.8, -8.9, -9.1, -10.2, -11.3, -2.8,
	}
	if len(corpus) != 51 {
		t.Fatalf("corpus has %d entries, want 51", len(corpus))
	}

	var lower, raise, fine int
	for _, tp := range corpus {
		switch GainAdvice(tp).Kind {
		case GainHot, GainClipping:
			lower++
		case GainQuiet:
			raise++
		case GainFine:
			fine++
		}
	}
	if lower != 11 {
		t.Errorf("lower (Hot+Clipping) = %d, want 11", lower)
	}
	if raise != 4 {
		t.Errorf("raise (Quiet) = %d, want 4", raise)
	}
	if fine != 36 {
		t.Errorf("fine = %d, want 36", fine)
	}
}

// TestGainAdviceDeltaIsRounded confirms DeltaDB is a whole-dB figure, matching
// the rounded prose ("~6 ㏈").
func TestGainAdviceDeltaIsRounded(t *testing.T) {
	for _, tp := range []float64{-0.13, -21.41, 0.35, -7.3, -13.7} {
		d := GainAdvice(tp).DeltaDB
		if d != math.Round(d) {
			t.Errorf("GainAdvice(%v).DeltaDB = %v, want a whole dB", tp, d)
		}
	}
}
