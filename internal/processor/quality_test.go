package processor

import "testing"

// resultWith builds a minimal ProcessingResult exercising the quality scorer's
// three inputs: output loudness, output true peak, and the output room-tone
// floor. inputNoiseRMS is retained only for the legacy callers; the noise score
// now depends on finalNoiseRMS (output cleanliness), not the reduction amount.
func resultWith(outputLUFS, outputTP, inputNoiseRMS, finalNoiseRMS float64) *ProcessingResult {
	return &ProcessingResult{
		OutputLUFS: outputLUFS,
		Measurements: &AudioMeasurements{
			NoiseProfile: &NoiseProfile{MeasuredNoiseFloor: inputNoiseRMS},
		},
		NormResult: &NormalisationResult{
			OutputTP:         outputTP,
			RequestedTargetI: -16.0,
			FinalMeasurements: &OutputMeasurements{
				RoomToneSample: &RoomToneCandidateMetrics{RMSLevel: finalNoiseRMS},
			},
		},
	}
}

func TestComputeQualityScoreExcellent(t *testing.T) {
	// On target, safe true peak, strong noise reduction.
	q := ComputeQualityScore(resultWith(-15.99, -2.18, -60.0, -82.0))
	if q.Stars != 5 {
		t.Errorf("on-target safe file: stars = %d, want 5 (score %.1f)", q.Stars, q.Score)
	}
	if q.Label != "Excellent" {
		t.Errorf("label = %q, want Excellent", q.Label)
	}
}

func TestComputeQualityScoreHotTruePeakPenalised(t *testing.T) {
	// On target loudness and good noise reduction, but a clipping true peak
	// (0 dBTP) zeroes the 0.30 true-peak weight, capping the composite at 0.70.
	q := ComputeQualityScore(resultWith(-16.0, 0.0, -60.0, -82.0))
	if q.Stars >= 5 {
		t.Errorf("hot true peak should drop below 5 stars, got %d (score %.1f)", q.Stars, q.Score)
	}
	if q.Score >= 71 {
		t.Errorf("hot true peak score = %.1f, want <= 70", q.Score)
	}
}

func TestComputeQualityScoreOffTargetPenalised(t *testing.T) {
	// Loudness 3 LUFS off target zeroes the 0.50 loudness weight.
	onTarget := ComputeQualityScore(resultWith(-16.0, -2.0, -60.0, -82.0))
	offTarget := ComputeQualityScore(resultWith(-19.0, -2.0, -60.0, -82.0))
	if offTarget.Stars >= onTarget.Stars {
		t.Errorf("off-target stars %d should be fewer than on-target %d", offTarget.Stars, onTarget.Stars)
	}
	if offTarget.Score >= onTarget.Score {
		t.Errorf("off-target score %.1f should be lower than on-target %.1f", offTarget.Score, onTarget.Score)
	}
}

func TestComputeQualityScoreCleanOutputScoresFullNoise(t *testing.T) {
	// An output floor at or below qualityNoiseCleanFloor (-75 dBFS) earns the full
	// 0.20 noise weight regardless of how clean the input was, so an already-clean
	// recording (little to remove) still scores full noise marks.
	q := ComputeQualityScore(resultWith(-16.0, -2.0, -78.0, -80.0))
	if q.Stars != 5 || q.Label != "Excellent" {
		t.Errorf("clean output: stars = %d label = %q, want 5 Excellent (score %.1f)", q.Stars, q.Label, q.Score)
	}
}

func TestComputeQualityScoreNoisyOutputDropsNoise(t *testing.T) {
	// An output floor at or above qualityNoiseDirtyFloor (-50 dBFS) zeroes the 0.20
	// noise weight; with perfect loudness and true peak the composite is
	// 0.50+0.30 = 0.80 -> 4 stars (Great).
	q := ComputeQualityScore(resultWith(-16.0, -2.0, -52.0, -50.0))
	if q.Stars != 4 {
		t.Errorf("noisy output: stars = %d, want 4 (score %.1f)", q.Stars, q.Score)
	}
	if q.Label != "Great" {
		t.Errorf("label = %q, want Great", q.Label)
	}
}

// TestComputeQualityScoreCleanInputNotPenalised locks the exact bug fixed here: a
// clean-input file (already low noise floor, little to remove) must score at least
// as high as a noisier-input file with identical loudness and true peak. The old
// scorer rewarded the *reduction amount*, so the clean file scored lower; the new
// scorer rewards output cleanliness, so the clean output wins or ties.
func TestComputeQualityScoreCleanInputNotPenalised(t *testing.T) {
	// Clean recording: -80 dBFS input, stays clean at -80 dBFS output (mark/popey).
	clean := ComputeQualityScore(resultWith(-16.0, -2.0, -80.0, -80.0))
	// Noisier recording: -67 dBFS input, processed to -67 dBFS output (martin).
	noisy := ComputeQualityScore(resultWith(-16.0, -2.0, -67.0, -67.0))

	if clean.Score < noisy.Score {
		t.Errorf("clean output score %.1f must be >= noisy output score %.1f", clean.Score, noisy.Score)
	}
	if clean.Stars < noisy.Stars {
		t.Errorf("clean output stars %d must be >= noisy output stars %d", clean.Stars, noisy.Stars)
	}
}

func TestComputeQualityScoreNeverConstant(t *testing.T) {
	// Distinct inputs must yield distinct scores: the scorer is not a constant.
	a := ComputeQualityScore(resultWith(-15.99, -2.18, -55.0, -82.0))
	b := ComputeQualityScore(resultWith(-19.0, -0.2, -60.0, -61.0))
	if a.Score == b.Score {
		t.Errorf("distinct inputs gave identical score %.2f", a.Score)
	}
}

func TestComputeQualityScoreNilSafe(t *testing.T) {
	q := ComputeQualityScore(nil)
	if q.Stars != 0 {
		t.Errorf("nil result: stars = %d, want 0", q.Stars)
	}
}
