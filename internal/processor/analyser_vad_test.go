package processor

import (
	"math"
	"testing"
	"time"
)

// vadInterval builds a synthetic IntervalSample on the momentary-LUFS axis with
// an in-voice-band, low-entropy spectral profile (passes the veto by default).
func vadInterval(idx int, momentaryLUFS float64) IntervalSample {
	return IntervalSample{
		Timestamp:     time.Duration(idx) * analysisIntervalHop,
		RMSLevel:      momentaryLUFS,
		MomentaryLUFS: momentaryLUFS,
		Spectral: SpectralMetrics{
			Centroid: 2000.0, // inside [speechCentroidMin, speechCentroidMax]
			Entropy:  0.40,   // below speechEntropyMax
			Found:    true,
		},
	}
}

func TestIntervalsForDuration(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		hop  time.Duration
		want int
	}{
		{"10s at 250ms", 10 * time.Second, 250 * time.Millisecond, 40},
		{"2s at 250ms", 2 * time.Second, 250 * time.Millisecond, 8},
		{"2s at 100ms", 2 * time.Second, 100 * time.Millisecond, 20},
		{"10s at 100ms", 10 * time.Second, 100 * time.Millisecond, 100},
		{"zero hop", 10 * time.Second, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := intervalsForDuration(tt.d, tt.hop); got != tt.want {
				t.Errorf("intervalsForDuration(%v, %v) = %d, want %d", tt.d, tt.hop, got, tt.want)
			}
		})
	}
}

func TestBuildLevelHistogram(t *testing.T) {
	var intervals []IntervalSample
	idx := 0
	// Low cluster around -50 LUFS.
	for i := range 30 {
		intervals = append(intervals, vadInterval(idx, -50+float64(i%3)))
		idx++
	}
	// Valley: nothing around -35.
	// High cluster around -20 LUFS.
	for i := range 30 {
		intervals = append(intervals, vadInterval(idx, -20+float64(i%3)))
		idx++
	}
	// One floored interval, must be skipped.
	intervals = append(intervals, vadInterval(idx, -130))

	h := buildLevelHistogram(intervals, axisMomentaryLUFS, 2.0)

	if h.count != 60 {
		t.Fatalf("histogram count = %d, want 60 (floored interval skipped)", h.count)
	}
	sum := 0
	for _, c := range h.bins {
		sum += c
	}
	if sum != h.count {
		t.Errorf("bin counts sum to %d, want %d", sum, h.count)
	}

	// Two populated regions with a sparse valley between them.
	lowPop, highPop, valley := 0, 0, 0
	for i, c := range h.bins {
		centre := h.binCentre(i)
		switch {
		case centre < -40:
			lowPop += c
		case centre > -30:
			highPop += c
		default:
			valley += c
		}
	}
	if lowPop == 0 || highPop == 0 {
		t.Errorf("expected two populated regions, got low=%d high=%d", lowPop, highPop)
	}
	if valley != 0 {
		t.Errorf("expected empty valley between modes, got %d", valley)
	}

	// Axis switch reads the other field: set RMS apart from momentary.
	rmsIntervals := []IntervalSample{
		{RMSLevel: -60, MomentaryLUFS: -10, Spectral: SpectralMetrics{Found: true}},
		{RMSLevel: -58, MomentaryLUFS: -10, Spectral: SpectralMetrics{Found: true}},
	}
	hr := buildLevelHistogram(rmsIntervals, axisRMS, 2.0)
	if hr.maxLevel > -50 {
		t.Errorf("RMS axis maxLevel = %.1f, want near -58 (read RMS not momentary)", hr.maxLevel)
	}
}

func TestOtsuSplit(t *testing.T) {
	t.Run("bimodal valley", func(t *testing.T) {
		var intervals []IntervalSample
		idx := 0
		for i := range 40 { // low mode centred near -50
			intervals = append(intervals, vadInterval(idx, -50+float64(i%2)))
			idx++
		}
		for i := range 40 { // high mode centred near -18
			intervals = append(intervals, vadInterval(idx, -18+float64(i%2)))
			idx++
		}
		h := buildLevelHistogram(intervals, axisMomentaryLUFS, 1.0)
		split := otsuSplit(h)
		if split <= -49 || split >= -18 {
			t.Errorf("otsuSplit = %.1f, want strictly between mode centres (-49.5 and -17.5)", split)
		}
	})

	t.Run("single mode stays within clamp bounds", func(t *testing.T) {
		var intervals []IntervalSample
		for i := range 80 { // one tight mode near -18 (all speech)
			intervals = append(intervals, vadInterval(i, -18+float64(i%2)))
		}
		h := buildLevelHistogram(intervals, axisMomentaryLUFS, 1.0)
		levels := vadLevels(intervals, axisMomentaryLUFS)
		p75 := percentileOfSorted(levels, 75)

		noiseFloor := -60.0
		split := clampSplit(otsuSplit(h), noiseFloor, p75)

		lower := noiseFloor + speechMinimumNoiseMarginDB
		if split < lower-0.001 || split > p75+0.001 {
			t.Errorf("clamped split = %.2f, want within [%.2f, %.2f]", split, lower, p75)
		}
	})

	t.Run("degenerate low split pinned to lower bound", func(t *testing.T) {
		// Otsu picks a split inside the data. With the noise floor anchor sitting
		// above the raw split, the clamp must pin to noiseFloor +
		// speechMinimumNoiseMarginDB, never letting a low split admit room tone.
		var intervals []IntervalSample
		for i := range 80 {
			intervals = append(intervals, vadInterval(i, -50+float64(i%2)))
		}
		h := buildLevelHistogram(intervals, axisMomentaryLUFS, 1.0)
		levels := vadLevels(intervals, axisMomentaryLUFS)
		p75 := percentileOfSorted(levels, 75)

		noiseFloor := -48.0 // anchor = -46, above the ~-49 single mode
		split := clampSplit(otsuSplit(h), noiseFloor, p75)
		lower := noiseFloor + speechMinimumNoiseMarginDB
		if math.Abs(split-lower) > 0.001 {
			t.Errorf("clamped split = %.2f, want pinned to lower bound %.2f", split, lower)
		}
	})
}

func TestPercentileFloor(t *testing.T) {
	t.Run("equals configured percentile", func(t *testing.T) {
		var levels []float64
		for i := range 100 { // -60..-0 wide spread, sorted ascending
			levels = append(levels, -60+float64(i))
		}
		seed := -200.0 // anchor far below, so the percentile wins
		got := percentileFloor(levels, seed)
		want := percentileOfSorted(levels, vadNoiseFloorPercentile)
		if got != want {
			t.Errorf("percentileFloor = %.2f, want configured percentile %.2f", got, want)
		}
	})

	t.Run("clamped to seed anchor", func(t *testing.T) {
		levels := []float64{-90, -89, -88, -87, -86} // percentile would be ~-90
		seed := -50.0                                // anchor = -48, above the percentile
		got := percentileFloor(levels, seed)
		want := seed + speechMinimumNoiseMarginDB
		if got != want {
			t.Errorf("percentileFloor = %.2f, want clamp to seed+speechMinimumNoiseMarginDB = %.2f", got, want)
		}
	})
}

func TestFlooredFraction(t *testing.T) {
	t.Run("gated slice flips true: floored fraction over threshold", func(t *testing.T) {
		var iv []IntervalSample
		idx := 0
		// Sparse speech: 40 above-split intervals.
		for range 40 {
			iv = append(iv, vadInterval(idx, -15))
			idx++
		}
		// Dense silence: 40 floored intervals and 20 fully silent (-inf) windows.
		for range 40 {
			iv = append(iv, vadInterval(idx, -130))
			idx++
		}
		for range 20 {
			iv = append(iv, vadInterval(idx, math.Inf(-1)))
			idx++
		}

		got := flooredFraction(iv, axisMomentaryLUFS)
		// 60 of 100 intervals are floored (including -inf windows).
		want := 60.0 / 100.0
		if math.Abs(got-want) > 0.001 {
			t.Errorf("flooredFraction = %.3f, want %.3f (floored and -inf count)", got, want)
		}
		if got < vadVoiceActivatedFraction {
			t.Errorf("fraction %.3f below threshold %.3f, should flag voice-activated", got, vadVoiceActivatedFraction)
		}
	})

	t.Run("sparse below-split slice stays false: zero floored", func(t *testing.T) {
		// The per-speaker podcast track Option A got wrong: a high below-split
		// fraction but ZERO digital-silence intervals. Floored-only keeps it false.
		var iv []IntervalSample
		idx := 0
		// 70 below-split-but-measurable intervals (well above the floor).
		for range 70 {
			iv = append(iv, vadInterval(idx, -55))
			idx++
		}
		// 30 above-split speech intervals.
		for range 30 {
			iv = append(iv, vadInterval(idx, -15))
			idx++
		}

		got := flooredFraction(iv, axisMomentaryLUFS)
		if got != 0 {
			t.Errorf("flooredFraction = %.3f, want 0 (no interval is floored)", got)
		}
		if got >= vadVoiceActivatedFraction {
			t.Errorf("sparse below-split track flagged voice-activated at %.3f; Option A's failure case", got)
		}
	})

	t.Run("continuous slice stays false: no floored, mostly above-split", func(t *testing.T) {
		var iv []IntervalSample
		idx := 0
		for range 95 {
			iv = append(iv, vadInterval(idx, -15))
			idx++
		}
		for range 5 {
			iv = append(iv, vadInterval(idx, -60))
			idx++
		}

		got := flooredFraction(iv, axisMomentaryLUFS)
		if got != 0 {
			t.Errorf("flooredFraction = %.3f, want 0 (no interval is floored)", got)
		}
		if got >= vadVoiceActivatedFraction {
			t.Errorf("continuous track flagged voice-activated at %.3f", got)
		}
	})

	t.Run("all-floored slice returns 1.0 (true)", func(t *testing.T) {
		var iv []IntervalSample
		for i := range 30 {
			iv = append(iv, vadInterval(i, -130))
		}
		got := flooredFraction(iv, axisMomentaryLUFS)
		if got != 1.0 {
			t.Errorf("flooredFraction = %.3f, want 1.0 (every interval floored)", got)
		}
		if got < vadVoiceActivatedFraction {
			t.Errorf("all-floored slice not flagged voice-activated at %.3f", got)
		}
	})

	t.Run("NaN momentary counts as floored", func(t *testing.T) {
		// macOS arm64 FFmpeg ebur128 reports digital silence as NaN. A NaN window
		// must add to both numerator and denominator so the gated capture is
		// detected the same as on Linux (-inf/finite-low).
		iv := []IntervalSample{
			vadInterval(0, math.NaN()),
			vadInterval(1, -15),
		}
		got := flooredFraction(iv, axisMomentaryLUFS)
		if want := 0.5; math.Abs(got-want) > 0.001 {
			t.Errorf("flooredFraction = %.3f, want %.3f (NaN counts as floored)", got, want)
		}
	})

	t.Run("mixed NaN, finite-low, and normal windows", func(t *testing.T) {
		var iv []IntervalSample
		idx := 0
		// 25 NaN (macOS silence) + 25 finite-low (Linux silence) = 50 floored.
		for range 25 {
			iv = append(iv, vadInterval(idx, math.NaN()))
			idx++
		}
		for range 25 {
			iv = append(iv, vadInterval(idx, -120)) // <= vadLevelFloorDB (-115)
			idx++
		}
		// 50 normal-level speech windows.
		for range 50 {
			iv = append(iv, vadInterval(idx, -15))
			idx++
		}

		got := flooredFraction(iv, axisMomentaryLUFS)
		if want := 50.0 / 100.0; math.Abs(got-want) > 0.001 {
			t.Errorf("flooredFraction = %.3f, want %.3f (NaN + finite-low both floor)", got, want)
		}
	})

	t.Run("all-NaN slice returns 1.0 (every window floors)", func(t *testing.T) {
		var iv []IntervalSample
		for i := range 20 {
			iv = append(iv, vadInterval(i, math.NaN()))
		}
		if got := flooredFraction(iv, axisMomentaryLUFS); got != 1.0 {
			t.Errorf("flooredFraction = %.3f, want 1.0 (every window is digital silence)", got)
		}
	})

	t.Run("empty slice returns 0 via the zero-guard", func(t *testing.T) {
		if got := flooredFraction(nil, axisMomentaryLUFS); got != 0 {
			t.Errorf("flooredFraction = %.3f, want 0 (no intervals)", got)
		}
	})
}

// seedInterval builds a synthetic IntervalSample for the noise-floor seed
// estimator: the momentary-LUFS level and spectral flux drive roomToneScore. Any
// interval with MomentaryLUFS <= the level median AND flux <= the flux median
// scores exactly 1.0, so such intervals tie at the top of the scored set whatever
// their exact level.
func seedInterval(level, flux float64) IntervalSample {
	return IntervalSample{
		RMSLevel:      level,
		MomentaryLUFS: level,
		Spectral:      SpectralMetrics{Flux: flux, Found: true},
	}
}

// shuffleIntervals returns a deterministic permutation of the input (reversed),
// so a test can feed the same multiset in a different order without importing a
// PRNG. Reversal alone reorders any tied run, which is all these tests need.
func shuffleIntervals(in []IntervalSample) []IntervalSample {
	out := make([]IntervalSample, len(in))
	for i, iv := range in {
		out[len(in)-1-i] = iv
	}
	return out
}

func TestEstimateNoiseFloorAndThreshold_TiedScoreOrderIndependent(t *testing.T) {
	// Build a set whose quiet, low-flux intervals all score exactly 1.0 (a tie),
	// spread across distinct RMS values, plus louder high-flux intervals that
	// score lower. The tie-break (lower RMS, then index) must make the seeded
	// noise floor identical regardless of input order; an unstable sort over the
	// tied run would otherwise let a different RMS land inside the truncation.
	var intervals []IntervalSample
	// 25 tied score-1.0 intervals, RMS from -80 to -56 dB, all flux below median.
	for i := range 25 {
		intervals = append(intervals, seedInterval(-80+float64(i), 0.01))
	}
	// 25 louder, high-flux intervals (score < 1.0) to set the medians and fill
	// the lower-scored tail.
	for i := range 25 {
		intervals = append(intervals, seedInterval(-30+float64(i), 0.50))
	}

	medians := computeSilenceMedians(intervals)
	floorA, threshA, okA := estimateNoiseFloorAndThreshold(intervals, medians)
	if !okA {
		t.Fatal("estimateNoiseFloorAndThreshold returned ok=false on a valid set")
	}

	shuffled := shuffleIntervals(intervals)
	mediansShuf := computeSilenceMedians(shuffled)
	floorB, threshB, okB := estimateNoiseFloorAndThreshold(shuffled, mediansShuf)
	if !okB {
		t.Fatal("estimateNoiseFloorAndThreshold returned ok=false on the shuffled set")
	}

	if floorA != floorB {
		t.Errorf("noise floor order-dependent: %.3f vs %.3f (tie-break dropped?)", floorA, floorB)
	}
	if threshA != threshB {
		t.Errorf("threshold order-dependent: %.3f vs %.3f", threshA, threshB)
	}
}

func TestEstimateNoiseFloorAndThreshold_TruncationPicksLowestRMS(t *testing.T) {
	// All quiet intervals tie at score 1.0; the louder ones score lower. The
	// truncated seed set keeps floorSeedTopPercent of the scored set (len/5).
	// The deterministic tie-break orders the tied run lowest-RMS first, so the
	// seeded noise floor (max RMS over the truncation) must equal the highest RMS
	// among only the kept lowest-RMS intervals, not any louder tied member.
	const total = 50
	const tiedCount = 25
	var intervals []IntervalSample
	// Tied score-1.0 intervals in DESCENDING RMS order (loudest first). With the
	// tie-break dropped, a score-only unstable sort can keep these input-leading
	// loud members and raise the floor; the tie-break must still keep the lowest
	// RMS values whatever the input order.
	for i := range tiedCount {
		intervals = append(intervals, seedInterval(-56-float64(i), 0.01)) // -56..-80, score 1.0
	}
	for i := range total - tiedCount {
		intervals = append(intervals, seedInterval(-30+float64(i), 0.50)) // louder, score < 1.0
	}

	medians := computeSilenceMedians(intervals)
	floor, _, ok := estimateNoiseFloorAndThreshold(intervals, medians)
	if !ok {
		t.Fatal("estimateNoiseFloorAndThreshold returned ok=false on a valid set")
	}

	// candidateCount = len/floorSeedTopPercent, floored at floorSeedMinCount.
	candidateCount := max(total/floorSeedTopPercent, floorSeedMinCount) // 10
	// The kept tied intervals are the candidateCount lowest RMS values, starting
	// at -80 dB in 1 dB steps; the highest kept RMS is the seeded floor.
	wantFloor := -80.0 + float64(candidateCount-1)
	if math.Abs(floor-wantFloor) > 0.001 {
		t.Errorf("seeded floor = %.3f, want %.3f (lowest-RMS tied intervals kept)", floor, wantFloor)
	}
}

func TestEstimateNoiseFloorAndThreshold_ExcludesFlooredFromSeed(t *testing.T) {
	// Voice-activated capture: the quietest, lowest-flux intervals are true digital
	// silence (floored at -130, below vadLevelFloorDB) and must NOT seed the floor.
	// Real room-tone intervals sit above the floor; the seed is their max level, not
	// the phantom -130.
	var intervals []IntervalSample
	// 3 floored silence gaps (score 1.0: quietest and low-flux). They sort first by
	// lowest level but must be excluded from the seed max.
	for range 3 {
		intervals = append(intervals, seedInterval(-130, 0.01))
	}
	// 40 real room-tone intervals well above the floor, the only valid seed source.
	for i := range 40 {
		intervals = append(intervals, seedInterval(-70+float64(i), 0.01)) // -70..-31
	}
	// 10 louder, high-flux intervals to set the medians.
	for i := range 10 {
		intervals = append(intervals, seedInterval(-10+float64(i), 0.50))
	}

	medians := computeSilenceMedians(intervals)
	floor, _, ok := estimateNoiseFloorAndThreshold(intervals, medians)
	if !ok {
		t.Fatal("estimateNoiseFloorAndThreshold returned ok=false despite real room-tone intervals")
	}
	if floor <= vadLevelFloorDB {
		t.Errorf("seeded floor = %.3f, want above the digital-silence floor %.1f (floored intervals leaked into the seed)", floor, vadLevelFloorDB)
	}
}

func TestEstimateNoiseFloorAndThreshold_AllFlooredReturnsNotOK(t *testing.T) {
	// Every candidate is true digital silence: the seed must NOT fabricate a level.
	// ok=false lets the caller fall back so percentileFloor uses the momentary p10.
	var intervals []IntervalSample
	for range silenceThresholdMinIntervals + 5 {
		intervals = append(intervals, seedInterval(-130, 0.01))
	}

	medians := computeSilenceMedians(intervals)
	_, _, ok := estimateNoiseFloorAndThreshold(intervals, medians)
	if ok {
		t.Error("estimateNoiseFloorAndThreshold returned ok=true on an all-floored set; it must not fabricate a floor")
	}
}

func TestFlooredFraction_BoundaryAtThreshold(t *testing.T) {
	// Guards the live >= test against vadVoiceActivatedFraction (0.20). A slice at
	// exactly 0.20 floored must flag voice-activated; one just under must not.
	build := func(floored, total int) []IntervalSample {
		var iv []IntervalSample
		idx := 0
		for range floored {
			iv = append(iv, vadInterval(idx, -130)) // floored: level <= vadLevelFloorDB
			idx++
		}
		for range total - floored {
			iv = append(iv, vadInterval(idx, -15)) // measurable, above floor
			idx++
		}
		return iv
	}

	t.Run("exactly 0.20 floored passes the >= test", func(t *testing.T) {
		iv := build(20, 100) // 0.20 exactly
		got := flooredFraction(iv, axisMomentaryLUFS)
		if math.Abs(got-0.20) > 0.001 {
			t.Fatalf("flooredFraction = %.3f, want 0.200", got)
		}
		if !(got >= vadVoiceActivatedFraction) {
			t.Errorf("fraction %.3f at the boundary must satisfy >= %.3f (voice-activated)", got, vadVoiceActivatedFraction)
		}
	})

	t.Run("just under 0.20 fails the >= test", func(t *testing.T) {
		iv := build(19, 100) // 0.19
		got := flooredFraction(iv, axisMomentaryLUFS)
		if math.Abs(got-0.19) > 0.001 {
			t.Fatalf("flooredFraction = %.3f, want 0.190", got)
		}
		if got >= vadVoiceActivatedFraction {
			t.Errorf("fraction %.3f below the boundary must not satisfy >= %.3f", got, vadVoiceActivatedFraction)
		}
	})
}

func TestIsSpeechInterval(t *testing.T) {
	const split = -30.0
	inBand := func(level, centroid, entropy float64) IntervalSample {
		return IntervalSample{
			MomentaryLUFS: level,
			Spectral:      SpectralMetrics{Centroid: centroid, Entropy: entropy, Found: true},
		}
	}

	tests := []struct {
		name string
		s    IntervalSample
		want bool
	}{
		{"above split, in band, low entropy", inBand(-20, 2000, 0.4), true},
		{"above split, out-of-band centroid", inBand(-20, 8000, 0.4), false},
		{"above split, high entropy", inBand(-20, 2000, 0.9), false},
		{"below split, otherwise speech-like", inBand(-40, 2000, 0.4), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSpeechInterval(tt.s, split, axisMomentaryLUFS); got != tt.want {
				t.Errorf("isSpeechInterval = %v, want %v", got, tt.want)
			}
		})
	}
}

// vadSpeech is a clear speech interval (well above the split, veto passes).
func vadSpeech(idx int) IntervalSample { return vadInterval(idx, -15) }

// vadQuiet is a quiet gap interval (well below the split, veto irrelevant).
func vadQuiet(idx int) IntervalSample { return vadInterval(idx, -60) }

// vadLoudNonSpeech is loud (above the split) but fails the spectral veto:
// out-of-band centroid. This is the loud-gap guard trigger.
func vadLoudNonSpeech(idx int) IntervalSample {
	s := vadInterval(idx, -15)
	s.Spectral.Centroid = 9000 // outside the voice band -> veto fails
	return s
}

func TestBuildSpeechRuns(t *testing.T) {
	const split = -30.0
	const margin = 3.0
	hop := analysisIntervalHop
	minN := intervalsForDuration(vadMinSpeechDuration, hop) // 40 at 250ms
	tol := intervalsForDuration(vadGapToleranceFloor, hop)  // 8 at 250ms

	build := func(intervals []IntervalSample) []SpeechRegion {
		return buildSpeechRuns(intervals, split, margin, tol, axisMomentaryLUFS, hop)
	}

	t.Run("short gap yields one run", func(t *testing.T) {
		var iv []IntervalSample
		idx := 0
		for range 50 {
			iv = append(iv, vadSpeech(idx))
			idx++
		}
		for i := 0; i < tol-1; i++ { // gap shorter than tolerance
			iv = append(iv, vadQuiet(idx))
			idx++
		}
		for range 50 {
			iv = append(iv, vadSpeech(idx))
			idx++
		}
		runs := build(iv)
		if len(runs) != 1 {
			t.Fatalf("got %d runs, want 1 (gap < tolerance bridges)", len(runs))
		}
	})

	t.Run("long gap yields two runs", func(t *testing.T) {
		var iv []IntervalSample
		idx := 0
		for range 50 {
			iv = append(iv, vadSpeech(idx))
			idx++
		}
		for i := 0; i < tol+5; i++ { // gap longer than tolerance
			iv = append(iv, vadQuiet(idx))
			idx++
		}
		for range 50 {
			iv = append(iv, vadSpeech(idx))
			idx++
		}
		runs := build(iv)
		if len(runs) != 2 {
			t.Fatalf("got %d runs, want 2 (gap > tolerance splits)", len(runs))
		}
	})

	t.Run("two-threshold hysteresis holds between thresholds", func(t *testing.T) {
		// A neutral zone interval (below the split, but above the low threshold)
		// does not leave the run; only dropping below the low threshold does.
		var iv []IntervalSample
		idx := 0
		for range 50 {
			iv = append(iv, vadSpeech(idx))
			idx++
		}
		// Neutral: level between low (-33) and split (-30), veto passes.
		for range 3 {
			iv = append(iv, vadInterval(idx, -31))
			idx++
		}
		for range 50 {
			iv = append(iv, vadSpeech(idx))
			idx++
		}
		runs := build(iv)
		if len(runs) != 1 {
			t.Fatalf("got %d runs, want 1 (neutral zone held by hysteresis)", len(runs))
		}
	})

	t.Run("loud-gap guard ends a bridged run", func(t *testing.T) {
		var iv []IntervalSample
		idx := 0
		for range 50 {
			iv = append(iv, vadSpeech(idx))
			idx++
		}
		iv = append(iv, vadLoudNonSpeech(idx)) // above split, veto fails -> ends run
		idx++
		for range 50 {
			iv = append(iv, vadSpeech(idx))
			idx++
		}
		runs := build(iv)
		if len(runs) != 2 {
			t.Fatalf("got %d runs, want 2 (loud non-speech ends the bridged run)", len(runs))
		}
	})

	t.Run("quiet gap below tolerance continues run", func(t *testing.T) {
		// Mirror of the loud-gap case: a single quiet interval bridges, one run.
		var iv []IntervalSample
		idx := 0
		for range 50 {
			iv = append(iv, vadSpeech(idx))
			idx++
		}
		iv = append(iv, vadQuiet(idx)) // below split -> bridgeable
		idx++
		for range 50 {
			iv = append(iv, vadSpeech(idx))
			idx++
		}
		runs := build(iv)
		if len(runs) != 1 {
			t.Fatalf("got %d runs, want 1 (single quiet interval bridges)", len(runs))
		}
	})

	t.Run("run below minimum duration is dropped", func(t *testing.T) {
		var iv []IntervalSample
		for i := 0; i < minN-1; i++ {
			iv = append(iv, vadSpeech(i))
		}
		// Pad with quiet so the slice clears the length guard.
		for i := minN - 1; i < minN+5; i++ {
			iv = append(iv, vadQuiet(i))
		}
		runs := build(iv)
		if len(runs) != 0 {
			t.Fatalf("got %d runs, want 0 (run shorter than minimum)", len(runs))
		}
	})
}

func TestGapToleranceIntervals(t *testing.T) {
	hop := analysisIntervalHop
	floor := intervalsForDuration(vadGapToleranceFloor, hop)
	ceiling := intervalsForDuration(vadGapToleranceCeiling, hop)

	t.Run("p75 of interior gaps clamped", func(t *testing.T) {
		// Interior gaps {4, 6, 12, 30}, plus a trailing tail that must be excluded.
		flags := []bool{}
		add := func(n int, v bool) {
			for range n {
				flags = append(flags, v)
			}
		}
		add(5, true)
		add(4, false)
		add(5, true)
		add(6, false)
		add(5, true)
		add(12, false)
		add(5, true)
		add(30, false)
		add(5, true)
		add(20, false) // trailing tail, excluded

		got := gapToleranceIntervals(flags, hop)
		// Mirror the function's own nearest-rank p75 over {4,6,12,30}, then clamp.
		gaps := []float64{4, 6, 12, 30}
		want := max(floor, min(ceiling, int(math.Round(percentileOfSorted(gaps, 75)))))
		if got != want {
			t.Errorf("gapTolerance = %d, want %d (p75 of interior gaps clamped to [%d,%d])", got, want, floor, ceiling)
		}
	})

	t.Run("no interior gap returns floor", func(t *testing.T) {
		flags := []bool{true, true, true, false, false}
		if got := gapToleranceIntervals(flags, hop); got != floor {
			t.Errorf("gapTolerance = %d, want floor %d", got, floor)
		}
	})
}

func TestHysteresisMargin(t *testing.T) {
	t.Run("positive and scales with separation", func(t *testing.T) {
		near := buildBimodal(-40, -30) // small separation
		far := buildBimodal(-50, -10)  // large separation
		split := -30.0

		mNear := hysteresisMargin(near, split)
		mFar := hysteresisMargin(far, split)
		if mNear <= 0 || mFar <= 0 {
			t.Fatalf("margins must be positive, got near=%.2f far=%.2f", mNear, mFar)
		}
		if mFar <= mNear {
			t.Errorf("margin should grow with mode separation: near=%.2f far=%.2f", mNear, mFar)
		}
	})
}

// buildBimodal makes a two-cluster histogram for margin tests.
func buildBimodal(lowCentre, highCentre float64) histogram {
	var iv []IntervalSample
	idx := 0
	for range 40 {
		iv = append(iv, vadInterval(idx, lowCentre))
		idx++
	}
	for range 40 {
		iv = append(iv, vadInterval(idx, highCentre))
		idx++
	}
	return buildLevelHistogram(iv, axisMomentaryLUFS, 1.0)
}

// vadSpeechRich is a speech interval at -16 LUFS with a fuller spectral profile
// so the reused scoring lifts it clear of the minimum acceptable score.
func vadSpeechRich(idx int) IntervalSample {
	return vadSpeechRichAt(idx, -16.0)
}

// vadSpeechRichAt is vadSpeechRich with an explicit RMS level (and momentary set
// to the same value), so a test can vary a run's SNR margin against a noise
// floor while keeping it above the run-building split and passing the veto.
func vadSpeechRichAt(idx int, rms float64) IntervalSample {
	s := vadInterval(idx, rms)
	s.RMSLevel = rms
	s.PeakLevel = rms + 12 // crest ~12 dB, ideal for speech scoring
	s.Spectral.Kurtosis = 6.0
	s.Spectral.Rolloff = 6000.0
	s.Spectral.Flux = 0.004
	s.Spectral.Flatness = 0.2
	return s
}

func TestElectSpeechProfile(t *testing.T) {
	hop := analysisIntervalHop
	var iv []IntervalSample
	idx := 0
	// Run A: 140 intervals (35s, above the 30s duration adequacy minimum), loud at
	// -16 dBFS RMS -> wide SNR margin over the -60 dBFS floor.
	runAStart := time.Duration(idx) * hop
	for range 140 {
		iv = append(iv, vadSpeechRichAt(idx, -16.0))
		idx++
	}
	// Long gap to split the runs.
	for range 20 {
		iv = append(iv, vadInterval(idx, -75))
		idx++
	}
	// Run B: 200 intervals (50s, the LONGER run) but quiet at -34 dBFS RMS ->
	// narrow SNR margin. Momentary stays at -34, above the -45 split.
	for range 200 {
		iv = append(iv, vadSpeechRichAt(idx, -34.0))
		idx++
	}

	// Split at -45 so both runs are above it; floor at -60.
	runs := buildSpeechRuns(iv, -45, 3, intervalsForDuration(vadGapToleranceFloor, hop), axisMomentaryLUFS, hop)
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs for the elect test, got %d", len(runs))
	}

	noiseProfile := &NoiseProfile{MeasuredNoiseFloor: -60.0}
	profile, candidates := electSpeechProfile(runs, iv, noiseProfile, nil)
	if profile == nil {
		t.Fatal("electSpeechProfile returned nil, want elected region")
	}
	if len(candidates) == 0 {
		t.Error("electSpeechProfile returned no candidates")
	}
	// Highest-score election (was longest-wins): the shorter but wider-SNR run A
	// must beat the longer, quieter run B. Both clear the duration adequacy
	// minimum, so duration saturates and SNR margin decides.
	if profile.Region.Start != runAStart {
		t.Errorf("elected region start = %v, want wide-SNR run A start %v (highest score, not longest)", profile.Region.Start, runAStart)
	}
	// Contract fields are non-zero for a populated region.
	if profile.RMSLevel == 0 || profile.CrestFactor == 0 {
		t.Errorf("contract fields zero: RMSLevel=%.1f CrestFactor=%.1f", profile.RMSLevel, profile.CrestFactor)
	}
}

func TestPickLowClusterRegion(t *testing.T) {
	hop := analysisIntervalHop
	var iv []IntervalSample
	idx := 0
	// Short quiet run (10 intervals).
	for range 10 {
		iv = append(iv, vadInterval(idx, -60))
		idx++
	}
	// Speech.
	for range 20 {
		iv = append(iv, vadSpeechRich(idx))
		idx++
	}
	// Long quiet run (50 intervals) - the one to pick.
	longStart := time.Duration(idx) * hop
	for range 50 {
		iv = append(iv, vadInterval(idx, -60))
		idx++
	}

	region := pickLowClusterRegion(iv, -30, axisMomentaryLUFS, hop)
	if region == nil {
		t.Fatal("pickLowClusterRegion returned nil, want the long quiet run")
	}
	if region.Start < longStart {
		t.Errorf("picked region start %v before long quiet run start %v (picked the short run)", region.Start, longStart)
	}

	profile := extractNoiseProfileFromIntervals(region, iv)
	if profile == nil {
		t.Fatal("extractNoiseProfileFromIntervals returned nil")
	}
	// Detector overrides MeasuredNoiseFloor with the percentile floor.
	levels := vadLevels(iv, axisMomentaryLUFS)
	floor := percentileFloor(levels, -200)
	profile.MeasuredNoiseFloor = floor
	if profile.MeasuredNoiseFloor != floor {
		t.Errorf("MeasuredNoiseFloor = %.2f, want percentile floor %.2f", profile.MeasuredNoiseFloor, floor)
	}
	// Spectral fields come from the picked region (centroid set by vadInterval).
	if profile.Spectral.Centroid == 0 {
		t.Error("NoiseProfile spectral centroid is zero; want region spectral fields")
	}
}

// TestExtractNoiseProfileSpectralFields confirms extractNoiseProfileFromIntervals
// averages and preserves all 13 contamination-detection spectral fields from the
// region's interval samples. These fields have no adaptive consumer yet but are
// the measurement spine the report exposes, so they must survive extraction
// unchanged. The astats Entropy field tracks the spectral-entropy mean (unchanged
// behaviour).
func TestExtractNoiseProfileSpectralFields(t *testing.T) {
	hop := analysisIntervalHop
	// Two intervals with distinct spectral values; the profile must carry their
	// arithmetic mean for each field (no rounding, no drop). Values are chosen so
	// each per-field mean is a clean number.
	iv := []IntervalSample{
		{
			Timestamp: 0, RMSLevel: -60, PeakLevel: -50,
			Spectral: SpectralMetrics{
				Mean: 1.0, Variance: 2.0, Centroid: 1400, Spread: 300,
				Skewness: 0.5, Kurtosis: 2.0, Entropy: 0.4, Flatness: 0.3,
				Crest: 6.0, Flux: 0.02, Slope: -0.4, Decrease: 0.10,
				Rolloff: 6000, Found: true,
			},
		},
		{
			Timestamp: hop, RMSLevel: -58, PeakLevel: -48,
			Spectral: SpectralMetrics{
				Mean: 3.0, Variance: 4.0, Centroid: 1600, Spread: 500,
				Skewness: 1.5, Kurtosis: 4.0, Entropy: 0.6, Flatness: 0.5,
				Crest: 10.0, Flux: 0.06, Slope: -0.2, Decrease: 0.14,
				Rolloff: 8000, Found: true,
			},
		},
	}
	region := &RoomToneRegion{Start: 0, Duration: 2 * hop}

	profile := extractNoiseProfileFromIntervals(region, iv)
	if profile == nil {
		t.Fatal("extractNoiseProfileFromIntervals returned nil")
	}

	const tol = 0.001
	// astats Entropy field carries the spectral-entropy mean (unchanged behaviour).
	if math.Abs(profile.Entropy-0.5) > tol {
		t.Errorf("Entropy = %.4f, want mean 0.5", profile.Entropy)
	}

	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"SpectralMean", profile.Spectral.Mean, 2.0},
		{"SpectralVariance", profile.Spectral.Variance, 3.0},
		{"SpectralCentroid", profile.Spectral.Centroid, 1500},
		{"SpectralSpread", profile.Spectral.Spread, 400},
		{"SpectralSkewness", profile.Spectral.Skewness, 1.0},
		{"SpectralKurtosis", profile.Spectral.Kurtosis, 3.0},
		{"SpectralEntropy", profile.Spectral.Entropy, 0.5},
		{"SpectralFlatness", profile.Spectral.Flatness, 0.4},
		{"SpectralCrest", profile.Spectral.Crest, 8.0},
		{"SpectralFlux", profile.Spectral.Flux, 0.04},
		{"SpectralSlope", profile.Spectral.Slope, -0.3},
		{"SpectralDecrease", profile.Spectral.Decrease, 0.12},
		{"SpectralRolloff", profile.Spectral.Rolloff, 7000},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > tol {
			t.Errorf("%s = %.4f, want mean %.4f", c.name, c.got, c.want)
		}
	}
}

func TestDeriveGateStatistics(t *testing.T) {
	const split = -30.0

	t.Run("voiced p10, noise p95, separation match hand-computed", func(t *testing.T) {
		var iv []IntervalSample
		idx := 0
		// Noise set: 20 below-split intervals from -60 to -41 dB (1 dB steps).
		// p95 nearest-rank index = int(0.95 * 19) = 18 -> -42 dB.
		for i := range 20 {
			iv = append(iv, vadInterval(idx, -60+float64(i)))
			idx++
		}
		// Voiced set: 21 in-region speech intervals from -25 to -5 dB (1 dB steps).
		// All above the split and passing the veto. p10 index = int(0.10 * 20) = 2
		// -> -23 dB. The region must span exactly these intervals.
		regionStart := time.Duration(idx) * analysisIntervalHop
		for i := range 21 {
			iv = append(iv, vadInterval(idx, -25+float64(i)))
			idx++
		}
		regionEnd := time.Duration(idx) * analysisIntervalHop

		region := &SpeechRegion{Start: regionStart, End: regionEnd, Duration: regionEnd - regionStart}
		got := deriveGateStatistics(iv, split, axisMomentaryLUFS, region)

		const wantVoiced = -23.0
		const wantNoise = -42.0
		if math.Abs(got.VoicedLowPercentile-wantVoiced) > 0.001 {
			t.Errorf("VoicedLowPercentile = %.3f, want %.3f", got.VoicedLowPercentile, wantVoiced)
		}
		if math.Abs(got.NoiseHighPercentile-wantNoise) > 0.001 {
			t.Errorf("NoiseHighPercentile = %.3f, want %.3f", got.NoiseHighPercentile, wantNoise)
		}
		if math.Abs(got.SeparationDB-(wantVoiced-wantNoise)) > 0.001 {
			t.Errorf("SeparationDB = %.3f, want %.3f", got.SeparationDB, wantVoiced-wantNoise)
		}
	})

	t.Run("in-region veto failures excluded from the voiced set", func(t *testing.T) {
		// All 11 region intervals are above the split, but the loud non-speech ones
		// fail the spectral veto and must not enter the voiced set. The voiced p10
		// is computed only over the veto-passing speech intervals.
		var iv []IntervalSample
		idx := 0
		regionStart := time.Duration(idx) * analysisIntervalHop
		// 11 speech intervals at -20..-10 dB (veto passes). p10 index = int(0.10*10)
		// = 1 -> -19 dB.
		for i := range 11 {
			iv = append(iv, vadInterval(idx, -20+float64(i)))
			idx++
		}
		// 5 loud non-speech intervals in-region (above split, veto fails). If wrongly
		// counted they would shift the voiced set.
		for range 5 {
			iv = append(iv, vadLoudNonSpeech(idx))
			idx++
		}
		regionEnd := time.Duration(idx) * analysisIntervalHop

		region := &SpeechRegion{Start: regionStart, End: regionEnd, Duration: regionEnd - regionStart}
		got := deriveGateStatistics(iv, split, axisMomentaryLUFS, region)
		if math.Abs(got.VoicedLowPercentile-(-19.0)) > 0.001 {
			t.Errorf("VoicedLowPercentile = %.3f, want -19.000 (veto failures excluded)", got.VoicedLowPercentile)
		}
	})

	t.Run("only in-region speech counts toward the voiced set", func(t *testing.T) {
		// Speech-like intervals outside the elected region must be ignored. Quiet
		// speech sits before the region; loud speech sits inside it. The voiced p10
		// must reflect only the in-region levels.
		var iv []IntervalSample
		idx := 0
		// Out-of-region speech at -25 dB (would lower p10 if wrongly counted).
		for range 10 {
			iv = append(iv, vadInterval(idx, -25))
			idx++
		}
		regionStart := time.Duration(idx) * analysisIntervalHop
		// In-region speech, all at -15 dB.
		for range 11 {
			iv = append(iv, vadInterval(idx, -15))
			idx++
		}
		regionEnd := time.Duration(idx) * analysisIntervalHop

		region := &SpeechRegion{Start: regionStart, End: regionEnd, Duration: regionEnd - regionStart}
		got := deriveGateStatistics(iv, split, axisMomentaryLUFS, region)
		if math.Abs(got.VoicedLowPercentile-(-15.0)) > 0.001 {
			t.Errorf("VoicedLowPercentile = %.3f, want -15.000 (out-of-region speech ignored)", got.VoicedLowPercentile)
		}
	})

	t.Run("nil region leaves voiced set empty", func(t *testing.T) {
		// No profile elected: voiced p10 is the empty-set zero, separation degrades
		// to -noise-p95, noise p95 stays populated.
		var iv []IntervalSample
		idx := 0
		for i := range 20 {
			iv = append(iv, vadInterval(idx, -60+float64(i))) // -60..-41, all below split
			idx++
		}
		got := deriveGateStatistics(iv, split, axisMomentaryLUFS, nil)
		if got.VoicedLowPercentile != 0 {
			t.Errorf("VoicedLowPercentile = %.3f, want 0 (no region, empty voiced set)", got.VoicedLowPercentile)
		}
		const wantNoise = -42.0 // p95 index = int(0.95*19) = 18 -> -42
		if math.Abs(got.NoiseHighPercentile-wantNoise) > 0.001 {
			t.Errorf("NoiseHighPercentile = %.3f, want %.3f", got.NoiseHighPercentile, wantNoise)
		}
		if math.Abs(got.SeparationDB-(0-wantNoise)) > 0.001 {
			t.Errorf("SeparationDB = %.3f, want %.3f", got.SeparationDB, 0-wantNoise)
		}
	})

	t.Run("empty noise set yields zero noise percentile", func(t *testing.T) {
		// Every interval is at or above the split, so the below-split noise set is
		// empty. percentileOfSorted returns 0 for it; voiced p10 stays real.
		var iv []IntervalSample
		idx := 0
		regionStart := time.Duration(idx) * analysisIntervalHop
		for i := range 11 {
			iv = append(iv, vadInterval(idx, -20+float64(i))) // -20..-10, all above split
			idx++
		}
		regionEnd := time.Duration(idx) * analysisIntervalHop

		region := &SpeechRegion{Start: regionStart, End: regionEnd, Duration: regionEnd - regionStart}
		got := deriveGateStatistics(iv, split, axisMomentaryLUFS, region)
		if got.NoiseHighPercentile != 0 {
			t.Errorf("NoiseHighPercentile = %.3f, want 0 (empty noise set)", got.NoiseHighPercentile)
		}
		// Voiced p10 index = int(0.10*10) = 1 -> -19 dB.
		if math.Abs(got.VoicedLowPercentile-(-19.0)) > 0.001 {
			t.Errorf("VoicedLowPercentile = %.3f, want -19.000", got.VoicedLowPercentile)
		}
	})

	t.Run("single-sample voiced and noise sets", func(t *testing.T) {
		// One in-region speech interval and one below-split noise interval. With a
		// single sample, every percentile resolves to that lone value.
		var iv []IntervalSample
		idx := 0
		iv = append(iv, vadInterval(idx, -55)) // one noise sample, below split
		idx++
		regionStart := time.Duration(idx) * analysisIntervalHop
		iv = append(iv, vadInterval(idx, -12)) // one in-region speech sample
		idx++
		regionEnd := time.Duration(idx) * analysisIntervalHop

		region := &SpeechRegion{Start: regionStart, End: regionEnd, Duration: regionEnd - regionStart}
		got := deriveGateStatistics(iv, split, axisMomentaryLUFS, region)
		if math.Abs(got.VoicedLowPercentile-(-12.0)) > 0.001 {
			t.Errorf("VoicedLowPercentile = %.3f, want -12.000 (lone voiced sample)", got.VoicedLowPercentile)
		}
		if math.Abs(got.NoiseHighPercentile-(-55.0)) > 0.001 {
			t.Errorf("NoiseHighPercentile = %.3f, want -55.000 (lone noise sample)", got.NoiseHighPercentile)
		}
		if math.Abs(got.SeparationDB-(-12.0-(-55.0))) > 0.001 {
			t.Errorf("SeparationDB = %.3f, want %.3f", got.SeparationDB, -12.0-(-55.0))
		}
	})

	t.Run("split governs the voiced and noise partition", func(t *testing.T) {
		// A different split moves the boundary: with split -40, the -45 dB
		// intervals fall into the noise set, not the voiced set. The same data
		// under split -30 partitions differently, so the split is load-bearing.
		var iv []IntervalSample
		idx := 0
		regionStart := time.Duration(idx) * analysisIntervalHop
		// 11 in-region intervals from -50 to -40 dB.
		for i := range 11 {
			iv = append(iv, vadInterval(idx, -50+float64(i)))
			idx++
		}
		regionEnd := time.Duration(idx) * analysisIntervalHop
		region := &SpeechRegion{Start: regionStart, End: regionEnd, Duration: regionEnd - regionStart}

		// Split -45: levels at or above -45 are voiced; below -45 are noise.
		got := deriveGateStatistics(iv, -45.0, axisMomentaryLUFS, region)
		// Voiced set sorted {-45,-44,-43,-42,-41,-40}; p10 index int(0.10*5)=0 -> -45.
		if math.Abs(got.VoicedLowPercentile-(-45.0)) > 0.001 {
			t.Errorf("VoicedLowPercentile = %.3f, want -45.000 at split -45", got.VoicedLowPercentile)
		}
		// Noise set sorted {-50,-49,-48,-47,-46}; p95 index int(0.95*4)=3 -> -47.
		if math.Abs(got.NoiseHighPercentile-(-47.0)) > 0.001 {
			t.Errorf("NoiseHighPercentile = %.3f, want -47.000 at split -45", got.NoiseHighPercentile)
		}
	})

	t.Run("floored intervals excluded from both sets", func(t *testing.T) {
		// Floored (digital-silence) intervals must not enter the noise set, so they
		// cannot pull the noise p95 down. Only measurable below-split levels count.
		var iv []IntervalSample
		idx := 0
		for range 10 {
			iv = append(iv, vadInterval(idx, -130)) // floored, excluded
			idx++
		}
		for i := range 20 {
			iv = append(iv, vadInterval(idx, -60+float64(i))) // -60..-41 measurable noise
			idx++
		}
		got := deriveGateStatistics(iv, split, axisMomentaryLUFS, nil)
		const wantNoise = -42.0 // p95 over the 20 measurable levels, floored excluded
		if math.Abs(got.NoiseHighPercentile-wantNoise) > 0.001 {
			t.Errorf("NoiseHighPercentile = %.3f, want %.3f (floored excluded)", got.NoiseHighPercentile, wantNoise)
		}
	})
}

func TestDetectVoiceActivity(t *testing.T) {
	hop := analysisIntervalHop
	var iv []IntervalSample
	idx := 0
	// Low cluster (room tone), 60 intervals near -55.
	for range 60 {
		iv = append(iv, vadInterval(idx, -55))
		idx++
	}
	// High cluster (speech), 80 intervals near -16.
	for range 80 {
		iv = append(iv, vadSpeechRich(idx))
		idx++
	}

	m := &AudioMeasurements{}
	detectVoiceActivity(m, iv, -70, hop, axisMomentaryLUFS, nil)

	if m.Regions.SpeechProfile == nil {
		t.Error("SpeechProfile nil, want elected speech region")
	}
	if m.Regions.NoiseProfile == nil {
		t.Error("NoiseProfile nil, want low-cluster noise profile")
	}
	if m.Regions.ElectedRoomToneSample == nil {
		t.Error("ElectedRoomToneSample nil, want set from the picked region (runrecord depends on it)")
	}
	if m.Noise.FloorSource != "vad_percentile" {
		t.Errorf("FloorSource = %q, want vad_percentile", m.Noise.FloorSource)
	}
	if m.Noise.Floor >= -16 || m.Noise.Floor <= -120 {
		t.Errorf("Noise.Floor = %.1f, want a sane low value below speech and above silence", m.Noise.Floor)
	}

	// Gate statistics land populated. The voiced low percentile sits up in the
	// speech cluster (~-16), the noise high percentile down in the room-tone
	// cluster (~-55), so the separation is a healthy positive gap.
	if m.Regions.VoicedLowPercentile == 0 {
		t.Error("VoicedLowPercentile = 0, want a populated voiced percentile (a profile was elected)")
	}
	if m.Regions.NoiseHighPercentile == 0 {
		t.Error("NoiseHighPercentile = 0, want a populated noise percentile")
	}
	if m.Regions.GateSeparationDB <= 0 {
		t.Errorf("GateSeparationDB = %.3f, want a positive voiced-over-noise gap", m.Regions.GateSeparationDB)
	}

	// The written fields match deriveGateStatistics called directly on the same
	// inputs: the clamped Otsu split, the same axis, and the elected region.
	histogram := buildLevelHistogram(iv, axisMomentaryLUFS, 1.0)
	levels := vadLevels(iv, axisMomentaryLUFS)
	split := clampSplit(otsuSplit(histogram), -70, percentileOfSorted(levels, 75))
	want := deriveGateStatistics(iv, split, axisMomentaryLUFS, &m.Regions.SpeechProfile.Region)
	if m.Regions.VoicedLowPercentile != want.VoicedLowPercentile {
		t.Errorf("VoicedLowPercentile = %.3f, want %.3f (direct helper)", m.Regions.VoicedLowPercentile, want.VoicedLowPercentile)
	}
	if m.Regions.NoiseHighPercentile != want.NoiseHighPercentile {
		t.Errorf("NoiseHighPercentile = %.3f, want %.3f (direct helper)", m.Regions.NoiseHighPercentile, want.NoiseHighPercentile)
	}
	if m.Regions.GateSeparationDB != want.SeparationDB {
		t.Errorf("GateSeparationDB = %.3f, want %.3f (direct helper)", m.Regions.GateSeparationDB, want.SeparationDB)
	}
}

func TestDetectVoiceActivity_NoProfileLeavesVoicedPercentileZero(t *testing.T) {
	hop := analysisIntervalHop
	var iv []IntervalSample
	// A flat low-level stream with no speech cluster: no profile is elected, so
	// the voiced percentile must stay zero while the noise percentile populates.
	for i := range 60 {
		iv = append(iv, vadInterval(i, -55))
	}

	m := &AudioMeasurements{}
	detectVoiceActivity(m, iv, -70, hop, axisMomentaryLUFS, nil)

	if m.Regions.SpeechProfile != nil {
		t.Fatal("SpeechProfile elected, want none for a flat low-level stream")
	}
	if m.Regions.VoicedLowPercentile != 0 {
		t.Errorf("VoicedLowPercentile = %.3f, want 0 (no profile, empty voiced set)", m.Regions.VoicedLowPercentile)
	}
}

func TestIsFlooredLevel(t *testing.T) {
	cases := []struct {
		name  string
		level float64
		want  bool
	}{
		{"normal value above floor", -40.0, false},
		{"value at floor", vadLevelFloorDB, true},
		{"value below floor", vadLevelFloorDB - 1, true},
		{"positive infinity", math.Inf(1), true},
		{"negative infinity", math.Inf(-1), true},
		{"NaN", math.NaN(), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isFlooredLevel(tc.level); got != tc.want {
				t.Errorf("isFlooredLevel(%v) = %v, want %v", tc.level, got, tc.want)
			}
		})
	}
}
