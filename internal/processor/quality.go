package processor

import "math"

// QualityScore is an objective 0-5 star rating of a processed file, derived
// entirely from real Pass 4 output measurements. Stars and Label are the
// display-ready form; Score is the underlying 0-100 composite so callers can
// retune the star thresholds without recomputing the components.
type QualityScore struct {
	Score float64 // 0-100 composite
	Stars int     // 0-5 filled stars (rounded from Score)
	Label string  // Excellent / Great / Good / Fair / Poor
}

// Quality rubric weights. They sum to 1.0. Loudness accuracy dominates because
// hitting the -16 LUFS target is the tool's primary contract; true-peak safety
// guards against clipping; noise rewards a clean room-tone *result*, not how
// much was removed, so an already-clean recording is not penalised for having
// little to clean up.
const (
	qualityWeightLoudness = 0.50 // |OutputLUFS - target|
	qualityWeightTruePeak = 0.30 // output true peak vs ceiling
	qualityWeightNoise    = 0.20 // output room-tone noise floor cleanliness
)

// Loudness accuracy thresholds (LUFS deviation from target).
// Within tightTol = full marks; degrades linearly to 0 at looseTol.
const (
	qualityLoudnessTightTol = 0.5 // within +-0.5 LUFS scores 1.0
	qualityLoudnessLooseTol = 3.0 // at +-3.0 LUFS scores 0.0
)

// True-peak safety thresholds (dBTP). At or below safeTP = full marks; degrades
// to 0 as the peak approaches/exceeds 0 dBTP (clipping).
const (
	qualityTPSafe = -1.0 // <= -1 dBTP scores 1.0
	qualityTPHot  = 0.0  // >= 0 dBTP scores 0.0 (clipping)
)

// Noise cleanliness thresholds (output room-tone RMS floor in dBFS). The score
// rewards a quiet *result*, not the amount removed: a recording that arrives
// already clean scores full marks even though little was taken out. A lower
// (more negative) final floor is cleaner. At or below qualityNoiseCleanFloor =
// full marks; degrading linearly to 0 at qualityNoiseDirtyFloor.
const (
	qualityNoiseCleanFloor = -75.0 // <= -75 dBFS final floor scores 1.0
	qualityNoiseDirtyFloor = -50.0 // >= -50 dBFS final floor scores 0.0
)

// Star label thresholds map the 0-100 composite to a 0-5 star count.
// 5 -> Excellent, 4 -> Great, 3 -> Good, 2 -> Fair, <=1 -> Poor.
var qualityStarBands = []struct {
	min   float64
	stars int
	label string
}{
	{90, 5, "Excellent"},
	{75, 4, "Great"},
	{60, 3, "Good"},
	{40, 2, "Fair"},
	{0, 1, "Poor"},
}

// ComputeQualityScore derives an objective quality rating from a completed
// ProcessingResult. It reads the real measured output loudness, output true
// peak, and room-tone noise-floor reduction; it never returns a constant.
func ComputeQualityScore(result *ProcessingResult) QualityScore {
	if result == nil {
		return QualityScore{Stars: 0, Label: "Poor"}
	}

	target := NormTargetLUFS
	if result.NormResult != nil && result.NormResult.RequestedTargetI != 0 {
		target = result.NormResult.RequestedTargetI
	}

	loudness := scoreLoudness(result.OutputLUFS, target)
	truePeak := scoreTruePeak(outputTruePeak(result))
	noise := scoreNoiseCleanliness(result)

	composite := 100 * (qualityWeightLoudness*loudness +
		qualityWeightTruePeak*truePeak +
		qualityWeightNoise*noise)

	stars, label := starsForScore(composite)
	return QualityScore{Score: composite, Stars: stars, Label: label}
}

// scoreLoudness returns 1.0 within the tight tolerance, falling linearly to 0.0
// at the loose tolerance.
func scoreLoudness(outputLUFS, target float64) float64 {
	return linearScore(math.Abs(outputLUFS-target), qualityLoudnessTightTol, qualityLoudnessLooseTol)
}

// scoreTruePeak returns 1.0 at or below the safe ceiling, falling linearly to
// 0.0 as the peak reaches 0 dBTP (clipping).
func scoreTruePeak(tp float64) float64 {
	return linearScore(tp, qualityTPSafe, qualityTPHot)
}

// scoreNoiseCleanliness rates the cleanliness of the *output* room tone, not the
// amount of noise removed. A lower (more negative) final floor scores higher, so
// an already-clean recording is never scored below a noisier one with identical
// loudness and true peak. When no final room-tone sample exists, fall back to the
// input floor so the score stays honest rather than awarding free credit.
func scoreNoiseCleanliness(result *ProcessingResult) float64 {
	floor, ok := finalRoomToneRMS(result)
	if !ok {
		floor, ok = inputRoomToneRMS(result)
		if !ok {
			return 0.0
		}
	}
	// Digital silence in the final room tone is maximally clean. This guard must
	// stay ahead of linearScore, which does not special-case -Inf.
	if math.IsInf(floor, -1) {
		return 1.0
	}
	return linearScore(floor, qualityNoiseCleanFloor, qualityNoiseDirtyFloor)
}

// outputTruePeak resolves the real Pass 4 output true peak in dBTP. Prefers the
// normalisation result; falls back to the final output measurements.
func outputTruePeak(result *ProcessingResult) float64 {
	if result.NormResult != nil {
		if !result.NormResult.Skipped {
			return result.NormResult.OutputTP
		}
		if result.NormResult.FinalMeasurements != nil {
			return result.NormResult.FinalMeasurements.Loudness.OutputTP
		}
	}
	if result.FilteredMeasurements != nil {
		return result.FilteredMeasurements.Loudness.OutputTP
	}
	// No measurement available: assume worst case so the score is honest.
	return qualityTPHot
}

// OutputNoiseFloor resolves the genuine Pass 4 output room-tone RMS floor (dBFS),
// the after half of the done-box before->after pair. It does NOT fall back to
// the input floor: the bool is false when no Pass 4
// room-tone sample exists, so the done box can show the input figure alone
// rather than a misleading input->input arrow.
func OutputNoiseFloor(result *ProcessingResult) (float64, bool) {
	if result == nil {
		return 0, false
	}
	return finalRoomToneRMS(result)
}

// InputNoiseFloor resolves the input room-tone RMS floor (dBFS) for display,
// the before half of the done-box before->after pair whose after half is
// OutputNoiseFloor. Both ends are RegionSample.RMSLevel on the same astats RMS
// dBFS axis, so the pair is honestly comparable. It delegates to
// InputRoomToneFloorDB, which reads only the elected room-tone RegionSample
// (ElectedRoomToneSample, measured by the same interval accumulation that
// MeasureOutputRegions uses for the output). The bool is false when no elected
// sample exists.
func InputNoiseFloor(result *ProcessingResult) (float64, bool) {
	if result == nil {
		return 0, false
	}
	return InputRoomToneFloorDB(result.Measurements)
}

// InputRoomToneFloorDB resolves the canonical input room-tone RMS floor (dBFS)
// for display, the single source of truth shared by the done-box "before"
// (via InputNoiseFloor) and the live Analysis box (summary.go). Both surfaces
// must show the same number for the same file, so both read this one resolver.
//
// It returns the unweighted astats RMS dBFS floor from the elected room-tone
// RegionSample (ElectedRoomToneSample.RMSLevel), the same axis and measurement
// method as the output room-tone re-measure, so the before/after pair is
// honestly comparable. It must NOT return NoiseProfile.MeasuredNoiseFloor: that
// field is on the K-weighted momentary-LUFS axis (the VAD split / afftdn seed
// floor, overwritten in detectVoiceActivity), a different axis from the
// displayed astats RMS. See the "Measurement axes" section in AGENTS.md.
//
// A real astats RMS dBFS floor is finite and negative; a 0.0 or non-finite
// RMSLevel means unmeasured, so the bool is false and the UI shows its
// single-value / n/a path rather than a bogus number. The bool is also false
// when no ElectedRoomToneSample exists.
func InputRoomToneFloorDB(m *AudioMeasurements) (float64, bool) {
	if m == nil || m.Regions.ElectedRoomToneSample == nil {
		return 0, false
	}
	floor := m.Regions.ElectedRoomToneSample.RMSLevel
	if floor == 0 || math.IsNaN(floor) || math.IsInf(floor, 0) {
		return 0, false
	}
	return floor, true
}

// OutputTP resolves the final-output true peak (dBTP) for the done-box
// before->after row. It reads NormResult.OutputTP, ebur128's measured peak on
// the final Pass 4 output, the same value the pool read inline. The bool is
// false when NormResult is nil (normalisation disabled or skipped), so the UI
// gates the row rather than showing a bogus 0.
func OutputTP(result *ProcessingResult) (float64, bool) {
	if result == nil || result.NormResult == nil {
		return 0, false
	}
	return result.NormResult.OutputTP, true
}

// OutputLRA resolves the final-output loudness range (LU) for the done-box
// before->after row, read from the final-stage FinalMeasurements measured by
// ebur128, the same value the pool read inline. The bool is false when
// NormResult or its FinalMeasurements is nil, so the UI gates the row.
func OutputLRA(result *ProcessingResult) (float64, bool) {
	if result == nil || result.NormResult == nil || result.NormResult.FinalMeasurements == nil {
		return 0, false
	}
	return result.NormResult.FinalMeasurements.Loudness.OutputLRA, true
}

// inputRoomToneRMS resolves the elected input room-tone RMS floor (dBFS) from a
// ProcessingResult, delegating to InputRoomToneFloorDB so the quality scorer's
// input-floor fallback shares the one display resolver.
func inputRoomToneRMS(result *ProcessingResult) (float64, bool) {
	return InputRoomToneFloorDB(result.Measurements)
}

// finalRoomToneRMS resolves the Pass 4 room-tone RMS level (dBFS).
func finalRoomToneRMS(result *ProcessingResult) (float64, bool) {
	if result.NormResult == nil || result.NormResult.FinalMeasurements == nil {
		return 0, false
	}
	sample := result.NormResult.FinalMeasurements.RoomToneSample
	if sample == nil {
		return 0, false
	}
	return sample.RMSLevel, true
}

// starsForScore maps a 0-100 composite to a star count and word label.
func starsForScore(score float64) (int, string) {
	for _, band := range qualityStarBands {
		if score >= band.min {
			return band.stars, band.label
		}
	}
	return 1, "Poor"
}
