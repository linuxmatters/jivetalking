package processor

import (
	"testing"
	"time"
)

// groundedCandidate builds a minimal SpeechCandidateMetrics for the grounded
// scorer: an RMS level and a region duration are all the scorer reads (SNR is
// derived from RMSLevel minus the noise floor passed to the scorer).
func groundedCandidate(rmsLevel float64, duration time.Duration) *SpeechCandidateMetrics {
	return &SpeechCandidateMetrics{
		Region: SpeechRegion{Duration: duration},
		RegionSample: RegionSample{
			RMSLevel: rmsLevel,
		},
	}
}

// flatLevelIntervals returns count intervals all at the same RMS level (zero
// variance on the RMS axis).
func flatLevelIntervals(count int, level float64) []IntervalSample {
	iv := make([]IntervalSample, count)
	for i := range iv {
		iv[i] = IntervalSample{
			Timestamp: time.Duration(i) * analysisIntervalHop,
			RMSLevel:  level,
		}
	}
	return iv
}

// spreadLevelIntervals returns count intervals alternating around centre by
// +/-spread (non-zero variance on the RMS axis).
func spreadLevelIntervals(count int, centre, spread float64) []IntervalSample {
	iv := make([]IntervalSample, count)
	for i := range iv {
		level := centre + spread
		if i%2 == 1 {
			level = centre - spread
		}
		iv[i] = IntervalSample{
			Timestamp: time.Duration(i) * analysisIntervalHop,
			RMSLevel:  level,
		}
	}
	return iv
}

func TestScoreSpeechCandidateGrounded_SNRMonotonicity(t *testing.T) {
	const noiseFloor = -60.0
	const dur = 45 * time.Second // above adequacy minimum for both
	const levelVar = 0.0         // identical consistency

	// Wider SNR margin (louder RMS over the same floor) must score strictly higher.
	narrow := groundedCandidate(noiseFloor+25.0, dur) // 25 dB margin
	wide := groundedCandidate(noiseFloor+45.0, dur)   // 45 dB margin

	narrowScore := scoreSpeechCandidateGrounded(narrow, noiseFloor, levelVar)
	wideScore := scoreSpeechCandidateGrounded(wide, noiseFloor, levelVar)
	if wideScore <= narrowScore {
		t.Errorf("wider SNR must score higher: wide=%.4f narrow=%.4f", wideScore, narrowScore)
	}

	// A candidate below minSNRMargin must score strictly lower than one above it.
	below := groundedCandidate(noiseFloor+(minSNRMargin-10.0), dur) // 10 dB margin
	above := groundedCandidate(noiseFloor+(minSNRMargin+5.0), dur)  // 25 dB margin
	belowScore := scoreSpeechCandidateGrounded(below, noiseFloor, levelVar)
	aboveScore := scoreSpeechCandidateGrounded(above, noiseFloor, levelVar)
	if belowScore >= aboveScore {
		t.Errorf("below minSNRMargin must score lower than above: below=%.4f above=%.4f", belowScore, aboveScore)
	}
}

func TestScoreSpeechCandidateGrounded_DurationAdequacySaturation(t *testing.T) {
	const noiseFloor = -60.0
	const rms = -20.0 // 40 dB margin, identical SNR for all
	const levelVar = 0.0

	// Two runs both at or above the adequacy minimum must score equally: the
	// longer one does NOT outrank the shorter on the duration axis.
	atMin := groundedCandidate(rms, speechDurationAdequacyMinimum)
	wellAbove := groundedCandidate(rms, speechDurationAdequacyMinimum*3)
	atMinScore := scoreSpeechCandidateGrounded(atMin, noiseFloor, levelVar)
	aboveScore := scoreSpeechCandidateGrounded(wellAbove, noiseFloor, levelVar)
	if atMinScore != aboveScore {
		t.Errorf("duration must saturate at adequacy minimum: atMin=%.4f wellAbove=%.4f", atMinScore, aboveScore)
	}

	// A run below the adequacy minimum must score lower on duration.
	belowMin := groundedCandidate(rms, speechDurationAdequacyMinimum/2)
	belowScore := scoreSpeechCandidateGrounded(belowMin, noiseFloor, levelVar)
	if belowScore >= atMinScore {
		t.Errorf("below adequacy minimum must score lower: below=%.4f atMin=%.4f", belowScore, atMinScore)
	}
}

func TestScoreSpeechCandidateGrounded_ConsistencyTieBreak(t *testing.T) {
	const noiseFloor = -60.0
	const rms = -20.0            // identical SNR
	const dur = 45 * time.Second // identical, both above adequacy minimum
	steady := groundedCandidate(rms, dur)
	noisy := groundedCandidate(rms, dur)

	// Equal on SNR and duration adequacy; lower level variance must win.
	steadyScore := scoreSpeechCandidateGrounded(steady, noiseFloor, 1.0)
	noisyScore := scoreSpeechCandidateGrounded(noisy, noiseFloor, 9.0)
	if steadyScore <= noisyScore {
		t.Errorf("lower level variance must win the tie-break: steady=%.4f noisy=%.4f", steadyScore, noisyScore)
	}
}

// speechRunIntervals returns count intervals at the given RMS/momentary level,
// starting at start, with a speech-like crest. Used to feed findBestSpeechRegion
// region candidates for the election tests.
func speechRunIntervals(start time.Duration, count int, level float64) []IntervalSample {
	iv := make([]IntervalSample, count)
	for i := range iv {
		iv[i] = IntervalSample{
			Timestamp:     start + time.Duration(i)*analysisIntervalHop,
			RMSLevel:      level,
			MomentaryLUFS: level,
			PeakLevel:     level + 12.0,
		}
	}
	return iv
}

// TestFindBestSpeechRegion_VoiceActivatedCase proves the saturating duration term
// does not penalise sparse delivery: a sparse short wide-SNR run beats a long
// narrow-SNR run through the live highest-score election path.
func TestFindBestSpeechRegion_VoiceActivatedCase(t *testing.T) {
	const minIntervals = int(speechDurationAdequacyMinimum / analysisIntervalHop)

	// Short run: just over the adequacy minimum, loud (wide SNR over -60 floor).
	shortStart := time.Duration(0)
	shortRun := speechRunIntervals(shortStart, minIntervals+4, -18.0)
	shortEnd := shortRun[len(shortRun)-1].Timestamp + analysisIntervalHop

	// Long run: three times longer, but quiet (narrow SNR).
	longStart := shortEnd + 5*time.Second
	longRun := speechRunIntervals(longStart, (minIntervals+4)*3, -38.0)
	longEnd := longRun[len(longRun)-1].Timestamp + analysisIntervalHop

	intervals := append(append([]IntervalSample{}, shortRun...), longRun...)
	regions := []SpeechRegion{
		{Start: shortStart, End: shortEnd, Duration: shortEnd - shortStart},
		{Start: longStart, End: longEnd, Duration: longEnd - longStart},
	}

	result := findBestSpeechRegion(regions, intervals, &NoiseProfile{MeasuredNoiseFloor: -60.0}, nil)
	if result.BestRegion == nil {
		t.Fatal("expected a best region (always-elect)")
	}
	if result.BestRegion.Start != shortStart {
		t.Errorf("elected start = %v, want sparse wide-SNR run %v (duration adequacy saturates)", result.BestRegion.Start, shortStart)
	}
}

// TestFindBestSpeechRegion_AlwaysElects proves a file with a single sub-floor run
// still elects it (never nil when a run exists).
func TestFindBestSpeechRegion_AlwaysElects(t *testing.T) {
	// One short, near-floor run: below both the SNR minimum and the duration
	// adequacy minimum, so its grounded score is under the sanity floor.
	start := time.Duration(0)
	run := speechRunIntervals(start, 12, -33.0) // 3s, 2 dB over a -35 floor
	end := run[len(run)-1].Timestamp + analysisIntervalHop
	regions := []SpeechRegion{{Start: start, End: end, Duration: end - start}}

	result := findBestSpeechRegion(regions, run, &NoiseProfile{MeasuredNoiseFloor: -35.0}, nil)
	if result.BestRegion == nil {
		t.Fatal("expected the lone sub-floor run to be elected via fallback, got nil")
	}
	if result.BestRegion.Start != start {
		t.Errorf("elected start = %v, want %v", result.BestRegion.Start, start)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("len(Candidates) = %d, want 1", len(result.Candidates))
	}
	const minViableSpeechScore = 0.3
	if result.Candidates[0].Score >= minViableSpeechScore {
		t.Errorf("candidate score %.4f, want below the sanity floor %.1f (fallback path)", result.Candidates[0].Score, minViableSpeechScore)
	}
}

// TestFindBestSpeechRegion_AllBelowSNRMinimumElectsHighest is a regression for a
// corpus edge the Phase 3 sweep surfaced: LMP-81s-martin's cleanest run sits at
// ~11.5 dB SNR margin, well below minSNRMargin (20) and far below
// snrSaturationMargin (40). Its candidates both fall in the sub-minimum SNR band,
// yet the file must still elect, and it must elect the HIGHER-SNR of the two (the
// scorer ranks within the sub-minimum band, it does not flatten it). This mirrors
// the real stem: two runs at ~10.65 and ~11.54 dB; the 11.54 dB run wins.
func TestFindBestSpeechRegion_AllBelowSNRMinimumElectsHighest(t *testing.T) {
	const floor = -60.0

	// Lower-SNR run: ~10.65 dB margin (rms -49.35), 18.4s.
	loStart := time.Duration(0)
	loRun := speechRunIntervals(loStart, 74, -49.35) // ~18.5s
	loEnd := loRun[len(loRun)-1].Timestamp + analysisIntervalHop

	// Higher-SNR run: ~11.54 dB margin (rms -48.46), 20.3s.
	hiStart := loEnd + 5*time.Second
	hiRun := speechRunIntervals(hiStart, 81, -48.46) // ~20.3s
	hiEnd := hiRun[len(hiRun)-1].Timestamp + analysisIntervalHop

	intervals := append(append([]IntervalSample{}, loRun...), hiRun...)
	regions := []SpeechRegion{
		{Start: loStart, End: loEnd, Duration: loEnd - loStart},
		{Start: hiStart, End: hiEnd, Duration: hiEnd - hiStart},
	}

	result := findBestSpeechRegion(regions, intervals, &NoiseProfile{MeasuredNoiseFloor: floor}, nil)
	if result.BestRegion == nil {
		t.Fatal("expected a best region even when every candidate is below minSNRMargin")
	}
	if result.BestRegion.Start != hiStart {
		t.Errorf("elected start = %v, want the higher-SNR sub-minimum run %v", result.BestRegion.Start, hiStart)
	}
}

func TestLevelVariance(t *testing.T) {
	flat := flatLevelIntervals(20, -20.0)
	spread := spreadLevelIntervals(20, -20.0, 4.0)

	flatVar := levelVariance(flat, axisRMS)
	spreadVar := levelVariance(spread, axisRMS)

	if flatVar > 1e-9 {
		t.Errorf("flat level set variance = %.6f, want ~0", flatVar)
	}
	if spreadVar <= flatVar {
		t.Errorf("spread variance %.4f must exceed flat variance %.4f", spreadVar, flatVar)
	}

	// Empty region is defined to return 0.
	if got := levelVariance(nil, axisRMS); got != 0 {
		t.Errorf("levelVariance(nil) = %.4f, want 0", got)
	}
}
