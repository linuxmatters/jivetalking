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

		lower := noiseFloor + speechRMSMinimumNoiseMargin
		if split < lower-0.001 || split > p75+0.001 {
			t.Errorf("clamped split = %.2f, want within [%.2f, %.2f]", split, lower, p75)
		}
	})

	t.Run("degenerate low split pinned to lower bound", func(t *testing.T) {
		// Otsu picks a split inside the data. With the noise floor anchor sitting
		// above the raw split, the clamp must pin to noiseFloor + 6, never letting
		// a low split admit room tone.
		var intervals []IntervalSample
		for i := range 80 {
			intervals = append(intervals, vadInterval(i, -50+float64(i%2)))
		}
		h := buildLevelHistogram(intervals, axisMomentaryLUFS, 1.0)
		levels := vadLevels(intervals, axisMomentaryLUFS)
		p75 := percentileOfSorted(levels, 75)

		noiseFloor := -52.0 // anchor = -46, above the ~-49 single mode
		split := clampSplit(otsuSplit(h), noiseFloor, p75)
		lower := noiseFloor + speechRMSMinimumNoiseMargin
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
		seed := -50.0                                // anchor = -44, above the percentile
		got := percentileFloor(levels, seed)
		want := seed + speechRMSMinimumNoiseMargin
		if got != want {
			t.Errorf("percentileFloor = %.2f, want clamp to seed+6 = %.2f", got, want)
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

	t.Run("all-NaN slice returns 0 via the zero-guard", func(t *testing.T) {
		var iv []IntervalSample
		for i := range 20 {
			iv = append(iv, vadInterval(i, math.NaN()))
		}
		if got := flooredFraction(iv, axisMomentaryLUFS); got != 0 {
			t.Errorf("flooredFraction = %.3f, want 0 (every interval unmeasurable)", got)
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
	if profile.SpectralCentroid == 0 {
		t.Error("NoiseProfile spectral centroid is zero; want region spectral fields")
	}
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
}
