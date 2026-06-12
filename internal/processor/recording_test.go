package processor

import (
	"math"
	"testing"
)

// recInput is a compact builder for in-memory AudioMeasurements carrying only
// the Pass-1 INPUT measurements ComputeRecordingScore reads. speechRMS is the
// elected SpeechProfile RMS; a NaN speechRMS means "no SpeechProfile elected"
// (the noise-floor-only fallback path).
func recInput(inputTP, inputI, inputLRA, noiseFloor, speechRMS float64) *AudioMeasurements {
	m := &AudioMeasurements{}
	m.Loudness.InputTP = inputTP
	m.Loudness.InputI = inputI
	m.Loudness.InputLRA = inputLRA
	m.Regions.NoiseProfile = &NoiseProfile{MeasuredNoiseFloor: noiseFloor}
	if !math.IsNaN(speechRMS) {
		sp := &SpeechCandidateMetrics{}
		sp.RMSLevel = speechRMS
		m.Regions.SpeechProfile = sp
	}
	return m
}

// TestComputeRecordingScoreCorpusAnchors locks the score against the corpus
// sanity values from the grounding sweep (2026-06-12). If these stars drift, the
// formula or its thresholds changed.
func TestComputeRecordingScoreCorpusAnchors(t *testing.T) {
	cases := []struct {
		name      string
		inputTP   float64
		inputI    float64
		inputLRA  float64
		floor     float64
		speechRMS float64
		wantStars int
		wantLabel string
	}{
		// 83-popey: hot input (-0.13 dBTP) zeroes headroom -> 2 star Fair (~57.9).
		{"83-popey", -0.13, -29.82, 12.32, -74.04, -39.18, 2, "Fair"},
		// 83-mark (~85.9) and 83-martin (~80.5) land in the 4 star Great band on the
		// sweep: partial headroom (warm input true peak) keeps them off 5 stars.
		{"83-mark", -4.9, -23.0, 11.0, -76.0, -38.0, 4, "Great"},
		{"83-martin", -4.5, -24.0, 12.5, -75.0, -38.0, 4, "Great"},
		// A clean studio-ish capture: healthy headroom, deep floor, wide SNR, sane
		// level and a tight range -> 5 star Excellent.
		{"clean-studio", -9.0, -21.0, 9.0, -78.0, -33.0, 5, "Excellent"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ComputeRecordingScore(recInput(tc.inputTP, tc.inputI, tc.inputLRA, tc.floor, tc.speechRMS))
			if got.Stars != tc.wantStars {
				t.Errorf("%s: stars = %d (score %.2f), want %d", tc.name, got.Stars, got.Score, tc.wantStars)
			}
			if got.Label != tc.wantLabel {
				t.Errorf("%s: label = %q (score %.2f), want %q", tc.name, got.Label, got.Score, tc.wantLabel)
			}
		})
	}
}

// TestComputeRecordingScorePopeyComposite pins the 83-popey composite close to
// the documented ~57.9 so the per-axis blend is exercised end to end, not just
// the star band.
func TestComputeRecordingScorePopeyComposite(t *testing.T) {
	got := ComputeRecordingScore(recInput(-0.13, -29.82, 12.32, -74.04, -39.18))
	if math.Abs(got.Score-57.9) > 0.5 {
		t.Errorf("83-popey composite = %.3f, want ~57.9 (headroom must zero on a -0.13 dBTP input)", got.Score)
	}
}

// TestComputeRecordingScoreNoSpeechFallback confirms cleanliness falls back to
// the noise-floor-only score when no SpeechProfile is elected (SNR unavailable).
// Two results with an identical floor but different speech RMS must score the
// same when speech is absent, and the cleanliness axis must equal floorScore.
func TestComputeRecordingScoreNoSpeechFallback(t *testing.T) {
	const floor = -60.0
	noSpeech := ComputeRecordingScore(recInput(-9.0, -21.0, 9.0, floor, math.NaN()))

	// floorScore for -60 dBFS: (-60 - -45) / (-75 - -45) = 0.5.
	floorScore := linearScore(floor, recordingFloorFull, recordingFloorZero)
	headroom := linearScore(-9.0, recordingHeadroomFull, recordingHeadroomZero) // 1.0
	deficitScore := linearScore(math.Max(0, -23-(-21.0)), recordingDeficitFull, recordingDeficitZero)
	lraScore := linearScore(9.0, recordingLRAFull, recordingLRAZero)
	level := recordingDeficitWeight*deficitScore + recordingLRAWeight*lraScore
	wantComposite := 100 * (recordingWeightCleanliness*floorScore +
		recordingWeightHeadroom*headroom +
		recordingWeightLevel*level)

	if math.Abs(noSpeech.Score-wantComposite) > 1e-9 {
		t.Errorf("no-speech composite = %.6f, want %.6f (cleanliness must equal floorScore)", noSpeech.Score, wantComposite)
	}

	// Adding a wide-SNR SpeechProfile must change the score (SNR now contributes),
	// proving the fallback genuinely dropped the SNR term.
	withSpeech := ComputeRecordingScore(recInput(-9.0, -21.0, 9.0, floor, -20.0))
	if withSpeech.Score == noSpeech.Score {
		t.Errorf("electing a SpeechProfile must change cleanliness, both scored %.3f", noSpeech.Score)
	}
}

// TestComputeRecordingScoreNilGuard matches ComputeQualityScore's nil guard: nil
// measurements yield the worst rating.
func TestComputeRecordingScoreNilGuard(t *testing.T) {
	if got := ComputeRecordingScore(nil); got.Stars != 0 || got.Label != "Poor" {
		t.Errorf("nil measurements: got %+v, want {Stars:0 Label:Poor}", got)
	}
}

// TestComputeRecordingScoreHeadroomDiscriminates confirms input true peak is the
// real discriminator: a hot capture (>= -1 dBTP) zeroes the headroom axis while
// an otherwise-identical capture with healthy headroom (<= -6 dBTP) scores it
// full, the two differing by exactly the headroom weight in composite terms.
func TestComputeRecordingScoreHeadroomDiscriminates(t *testing.T) {
	hot := ComputeRecordingScore(recInput(-0.5, -21.0, 9.0, -78.0, -33.0))
	healthy := ComputeRecordingScore(recInput(-7.0, -21.0, 9.0, -78.0, -33.0))

	if delta := healthy.Score - hot.Score; math.Abs(delta-100*recordingWeightHeadroom) > 1e-9 {
		t.Errorf("headroom delta = %.6f, want %.6f (full headroom weight)", delta, 100*recordingWeightHeadroom)
	}
}

// TestLinearScoreDirectionAgnostic confirms the ramp clamps to [0,1] and works
// whether full > zero (ascending) or full < zero (descending, e.g. a dBFS floor
// where more negative is better).
func TestLinearScoreDirectionAgnostic(t *testing.T) {
	cases := []struct {
		name       string
		v, full, z float64
		want       float64
	}{
		{"ascending at full", 45, 45, 12, 1.0},
		{"ascending at zero", 12, 45, 12, 0.0},
		{"ascending midpoint", 28.5, 45, 12, 0.5},
		{"ascending below zero clamps", 0, 45, 12, 0.0},
		{"ascending above full clamps", 60, 45, 12, 1.0},
		{"descending at full", -75, -75, -45, 1.0},
		{"descending at zero", -45, -75, -45, 0.0},
		{"descending midpoint", -60, -75, -45, 0.5},
		{"descending above full clamps", -80, -75, -45, 1.0},
		{"descending below zero clamps", -30, -75, -45, 0.0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := linearScore(tc.v, tc.full, tc.z); math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("linearScore(%v,%v,%v) = %.6f, want %.6f", tc.v, tc.full, tc.z, got, tc.want)
			}
		})
	}
}
