package processor

import "math"

// ComputeRecordingScore grades the INPUT capture quality from Pass-1
// measurements, the counterpart to ComputeQualityScore (which grades the
// OUTPUT against spec). The output score saturates near 5 stars because the
// normaliser reliably hits the -16 LUFS contract; this score genuinely varies
// with how the source was recorded, so the pair tells the value story
// (Recording 2 stars -> Processed 5 stars = "your capture clipped, we caught
// it"). It reuses the QualityScore type, the qualityStarBands, and starsForScore
// so the two scores share one star/label vocabulary.
//
// Thresholds are grounded on a 51-file corpus sweep (2026-06-12): they spread
// the corpus 5x13 / 4x16 / 3x13 / 2x9 / 1x0, anchored corpus-top and
// absolute-bottom so the low bands populate honestly on bad captures the corpus
// lacks. Each constant cites whether its anchor is corpus-derived or an absolute
// red line. Like ComputeQualityScore it never returns a constant.
const (
	recordingWeightCleanliness = 0.50 // SNR + noise floor
	recordingWeightHeadroom    = 0.30 // input true peak (the real discriminator)
	recordingWeightLevel       = 0.20 // level deficit + loudness range
)

// Cleanliness thresholds.
const (
	// SNR gap (SpeechRMS - NoiseFloor, dB). full=45 corpus-top; zero=12 absolute
	// (broadcast-unacceptable SNR).
	recordingSNRFull = 45.0
	recordingSNRZero = 12.0
	// Noise floor (dBFS). full=-75 corpus/existing-rubric clean anchor; zero=-45
	// absolute (audibly hissy).
	recordingFloorFull = -75.0
	recordingFloorZero = -45.0
	// Blend of SNR and floor when a SpeechProfile is elected.
	recordingSNRWeight   = 0.7
	recordingFloorWeight = 0.3
)

// Headroom thresholds (input true peak, dBTP). full=-6 corpus p25 (healthy
// headroom); zero=-1 absolute clipping / inter-sample red line. This axis does
// the real discriminating: hot captures score 0.
const (
	recordingHeadroomFull = -6.0
	recordingHeadroomZero = -1.0
)

// Level & consistency thresholds.
const (
	// Sane-capture loudness target (LUFS); only deficit below it is penalised,
	// louder captures are not docked on this axis.
	recordingLevelTarget = -23.0
	// Deficit (LU below target). full<=6 LU deficit (InputI>=-29); zero>=18 LU
	// (InputI<=-41, painfully quiet).
	recordingDeficitFull = 6.0
	recordingDeficitZero = 18.0
	// Loudness range (LU). full=13 corpus p25; zero=22 absolute sprawl.
	recordingLRAFull = 13.0
	recordingLRAZero = 22.0
	// Blend of deficit and LRA scores.
	recordingDeficitWeight = 0.6
	recordingLRAWeight     = 0.4
)

// linearScore maps v onto [0,1] along a direction-agnostic linear ramp: 1.0 at
// v==full, 0.0 at v==zero, linear between, clamped. It works whether full is
// greater or less than zero (e.g. a "more negative is better" dBFS axis).
func linearScore(v, full, zero float64) float64 {
	if full == zero {
		if v == full {
			return 1.0
		}
		return 0.0
	}
	t := (v - zero) / (full - zero)
	return math.Max(0.0, math.Min(1.0, t))
}

// ComputeRecordingScore derives the source-capture quality rating from Pass-1
// INPUT measurements. It takes *AudioMeasurements (not *ProcessingResult) so it
// serves both processing mode (result.Measurements) and analysis-only mode
// (which has measurements but no full ProcessingResult). A nil m yields the
// worst rating so the score stays honest.
func ComputeRecordingScore(m *AudioMeasurements) QualityScore {
	if m == nil {
		return QualityScore{Stars: 0, Label: "Poor"}
	}

	cleanliness := recordingCleanliness(m)
	headroom := linearScore(m.Loudness.InputTP, recordingHeadroomFull, recordingHeadroomZero)
	level := recordingLevel(m)

	composite := 100 * (recordingWeightCleanliness*cleanliness +
		recordingWeightHeadroom*headroom +
		recordingWeightLevel*level)

	stars, label := starsForScore(composite)
	return QualityScore{Score: composite, Stars: stars, Label: label}
}

// recordingCleanliness blends SNR and noise floor when a SpeechProfile is
// elected. Without one, SNR is unavailable, so it falls back to the
// noise-floor-only score rather than awarding free credit.
func recordingCleanliness(m *AudioMeasurements) float64 {
	floorScore := linearScore(m.Regions.NoiseProfile.floorOrZero(), recordingFloorFull, recordingFloorZero)

	speech := m.Regions.SpeechProfile
	if speech == nil {
		return floorScore
	}
	snrGap := speech.RMSLevel - m.Regions.NoiseProfile.floorOrZero()
	snrScore := linearScore(snrGap, recordingSNRFull, recordingSNRZero)
	return recordingSNRWeight*snrScore + recordingFloorWeight*floorScore
}

// recordingLevel blends the loudness deficit (quiet captures penalised, loud
// ones not) and the loudness range (sprawl penalised).
func recordingLevel(m *AudioMeasurements) float64 {
	deficit := math.Max(0, recordingLevelTarget-m.Loudness.InputI)
	deficitScore := linearScore(deficit, recordingDeficitFull, recordingDeficitZero)
	lraScore := linearScore(m.Loudness.InputLRA, recordingLRAFull, recordingLRAZero)
	return recordingDeficitWeight*deficitScore + recordingLRAWeight*lraScore
}

// floorOrZero returns the elected room-tone noise floor (dBFS), or 0 when no
// NoiseProfile was elected. A 0 dBFS floor scores as maximally dirty, keeping
// the fallback honest.
func (np *NoiseProfile) floorOrZero() float64 {
	if np == nil {
		return 0
	}
	return np.MeasuredNoiseFloor
}
