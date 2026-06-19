package processor

import (
	"math"
	"testing"
)

// resultWith builds a minimal ProcessingResult exercising the quality scorer's
// three inputs: output loudness, output true peak, and the output room-tone
// floor. inputNoiseRMS is retained only for the legacy callers; the noise score
// now depends on finalNoiseRMS (output cleanliness), not the reduction amount.
func resultWith(outputLUFS, outputTP, inputNoiseRMS, finalNoiseRMS float64) *ProcessingResult {
	return &ProcessingResult{
		OutputLUFS: outputLUFS,
		Measurements: &AudioMeasurements{
			Regions: RegionMetrics{NoiseProfile: &NoiseProfile{MeasuredNoiseFloor: inputNoiseRMS}},
		},
		NormResult: &NormalisationResult{
			OutputTP:         outputTP,
			RequestedTargetI: -16.0,
			FinalMeasurements: &OutputMeasurements{
				RoomToneSample: &RegionSample{RMSLevel: finalNoiseRMS},
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
	// Clean recording: -80 dBFS input, stays clean at -80 dBFS output.
	clean := ComputeQualityScore(resultWith(-16.0, -2.0, -80.0, -80.0))
	// Noisier recording: -67 dBFS input, processed to -67 dBFS output.
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

// TestInputNoiseFloorPrefersElectedSample locks the source preference: when the
// elected room-tone RegionSample is present, InputNoiseFloor returns its RMSLevel
// (the same measurement method as the output), not the NoiseProfile floor.
func TestInputNoiseFloorPrefersElectedSample(t *testing.T) {
	result := &ProcessingResult{
		Measurements: &AudioMeasurements{
			Regions: RegionMetrics{
				ElectedRoomToneSample: &RegionSample{RMSLevel: -71.0},
				NoiseProfile:          &NoiseProfile{MeasuredNoiseFloor: -64.0},
			},
		},
	}
	floor, ok := InputNoiseFloor(result)
	if !ok {
		t.Fatal("InputNoiseFloor: ok = false, want true")
	}
	if floor != -71.0 {
		t.Errorf("InputNoiseFloor = %.1f, want -71.0 (elected sample, not profile)", floor)
	}
}

// TestInputNoiseFloorNoMomentaryLeakage locks the axis contract: with no elected
// room-tone sample, InputNoiseFloor returns ok = false. It must NOT fall back to
// NoiseProfile.MeasuredNoiseFloor, which is on the K-weighted momentary-LUFS
// axis, not the displayed astats RMS dBFS axis.
func TestInputNoiseFloorNoMomentaryLeakage(t *testing.T) {
	result := &ProcessingResult{
		Measurements: &AudioMeasurements{
			Regions: RegionMetrics{NoiseProfile: &NoiseProfile{MeasuredNoiseFloor: -64.0}},
		},
	}
	if floor, ok := InputNoiseFloor(result); ok {
		t.Errorf("InputNoiseFloor = %.1f, ok = true; want ok = false (no momentary-LUFS fallback)", floor)
	}
}

// TestInputNoiseFloorUnmeasuredSample confirms a 0.0 or non-finite RMSLevel is
// treated as unmeasured (ok = false), not a real -0 dBFS floor.
func TestInputNoiseFloorUnmeasuredSample(t *testing.T) {
	for _, rms := range []float64{0, math.NaN(), math.Inf(-1), math.Inf(1)} {
		result := &ProcessingResult{
			Measurements: &AudioMeasurements{
				Regions: RegionMetrics{ElectedRoomToneSample: &RegionSample{RMSLevel: rms}},
			},
		}
		if floor, ok := InputNoiseFloor(result); ok {
			t.Errorf("InputNoiseFloor with RMSLevel %v: floor = %v, ok = true; want ok = false", rms, floor)
		}
	}
}

// TestInputNoiseFloorAbsent confirms ok = false when no elected sample exists.
func TestInputNoiseFloorAbsent(t *testing.T) {
	if _, ok := InputNoiseFloor(&ProcessingResult{}); ok {
		t.Error("InputNoiseFloor with no measurements: ok = true, want false")
	}
	if _, ok := InputNoiseFloor(nil); ok {
		t.Error("InputNoiseFloor(nil): ok = true, want false")
	}
}

// TestOutputNoiseFloorPresent confirms the genuine Pass 4 output floor is
// returned when a final room-tone sample exists.
func TestOutputNoiseFloorPresent(t *testing.T) {
	result := resultWith(-16.0, -2.0, -64.0, -82.0)
	floor, ok := OutputNoiseFloor(result)
	if !ok {
		t.Fatal("OutputNoiseFloor: ok = false, want true")
	}
	if floor != -82.0 {
		t.Errorf("OutputNoiseFloor = %.1f, want -82.0", floor)
	}
}

// TestOutputNoiseFloorAbsentNoFallback locks the no-fallback contract: with no
// Pass 4 room-tone sample, OutputNoiseFloor returns ok = false even when an input
// floor exists, so the done box never renders a misleading input->input arrow.
func TestOutputNoiseFloorAbsentNoFallback(t *testing.T) {
	result := &ProcessingResult{
		Measurements: &AudioMeasurements{
			Regions: RegionMetrics{NoiseProfile: &NoiseProfile{MeasuredNoiseFloor: -64.0}},
		},
	}
	if _, ok := OutputNoiseFloor(result); ok {
		t.Error("OutputNoiseFloor with no Pass 4 sample: ok = true, want false (no input fallback)")
	}
}

// TestOutputTP covers the three nil-guard layers: nil NormResult yields ok =
// false; a populated NormResult returns its top-level OutputTP regardless of
// FinalMeasurements (TP is a NormResult field, the value the pool read inline).
func TestOutputTP(t *testing.T) {
	if _, ok := OutputTP(nil); ok {
		t.Error("OutputTP(nil): ok = true, want false")
	}

	noNorm := &ProcessingResult{}
	if _, ok := OutputTP(noNorm); ok {
		t.Error("OutputTP with nil NormResult: ok = true, want false")
	}

	// Nil FinalMeasurements still yields the TP: it is a top-level NormResult
	// field, so the value is available without FinalMeasurements.
	nilFinal := &ProcessingResult{NormResult: &NormalisationResult{OutputTP: -1.5}}
	tp, ok := OutputTP(nilFinal)
	if !ok {
		t.Fatal("OutputTP with nil FinalMeasurements: ok = false, want true")
	}
	if tp != -1.5 {
		t.Errorf("OutputTP = %.1f, want -1.5", tp)
	}

	full := resultWith(-16.0, -2.0, -64.0, -82.0)
	if tp, ok := OutputTP(full); !ok || tp != -2.0 {
		t.Errorf("OutputTP populated = %.1f, ok = %v; want -2.0, true", tp, ok)
	}
}

// TestOutputLRA covers the three nil-guard layers: nil NormResult and nil
// FinalMeasurements both yield ok = false; a fully populated result returns
// FinalMeasurements.Loudness.OutputLRA, the value the pool read inline.
func TestOutputLRA(t *testing.T) {
	if _, ok := OutputLRA(nil); ok {
		t.Error("OutputLRA(nil): ok = true, want false")
	}

	noNorm := &ProcessingResult{}
	if _, ok := OutputLRA(noNorm); ok {
		t.Error("OutputLRA with nil NormResult: ok = true, want false")
	}

	nilFinal := &ProcessingResult{NormResult: &NormalisationResult{}}
	if _, ok := OutputLRA(nilFinal); ok {
		t.Error("OutputLRA with nil FinalMeasurements: ok = true, want false")
	}

	full := &ProcessingResult{
		NormResult: &NormalisationResult{
			FinalMeasurements: &OutputMeasurements{
				Loudness: OutputLoudnessMetrics{OutputLRA: 7.5},
			},
		},
	}
	lra, ok := OutputLRA(full)
	if !ok {
		t.Fatal("OutputLRA populated: ok = false, want true")
	}
	if lra != 7.5 {
		t.Errorf("OutputLRA = %.1f, want 7.5", lra)
	}
}
