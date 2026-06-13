package processor

import (
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Room tone region scoring for measurement reference extraction.
//
// The "noise profile" is not denoiser training data: the noise-removal stages
// take no profile input (anlmdn self-adapts, afftdn uses a fixed nr).
// These measurements serve as:
// 1. Reference baseline for adaptive filter tuning (gate, highpass)
// 2. Comparative measurement point (same region re-measured in later passes)
//
// Scoring weights are tuned to prefer regions that are:
//   - Quiet (amplitude) - accurate noise floor measurement
//   - Noise-like (spectral) - representative of room ambience, not crosstalk
//     (See docs/Spectral-Metrics-Reference.md for metric interpretations)
//   - Stable (variance) - intentionally recorded, not accidental gaps
//   - Duration 8-18s - sufficient data without absorbing content changes
const (
	// Duration thresholds
	minimumSilenceDuration = 8 * time.Second  // Minimum 8s to avoid inter-word gaps
	idealDurationMin       = 8 * time.Second  // Ideal range lower bound
	idealDurationMax       = 18 * time.Second // Ideal range upper bound

	// Long region segmentation: break up long room tone regions to find cleanest subsection
	// Intentional room tone may be embedded within a longer quiet period (e.g., quiet lead-up + room tone)
	segmentationThreshold = 20 * time.Second // Regions longer than this get segmented
	segmentDuration       = 12 * time.Second // Each segment is this long (ideal duration)
	segmentOverlap        = 4 * time.Second  // Segments overlap by this amount

	// Voice range detection (Hz) - for crosstalk rejection
	voiceCentroidMin = 250.0  // Lower bound of voice frequency range
	voiceCentroidMax = 4500.0 // Upper bound of voice frequency range

	// Scoring thresholds
	crosstalkKurtosisThreshold = 10.0 // Above this + voice centroid = likely crosstalk
	crosstalkPeakRMSGap        = 45.0 // dB - catches severe transient contamination regardless of spectral content

	// roomToneCrosstalkCrestMADs is the crest-outlier (MAD) crosstalk multiplier. An in-band
	// candidate is flagged crosstalk when its crest factor exceeds the candidate-population
	// crest median by more than this many MADs (median absolute deviations). This replaces the
	// former fixed crosstalkCrestFactorThreshold = 15 dB cap, which wrongly rejected healthy
	// quiet room tone: crest inflates as a region quietens, so a 15 dB cap rejected on merit-
	// independent absolute crest. The population-relative test only rejects a candidate whose
	// crest is anomalous *for its own population*. Dimensionless statistical multiplier, mirrors
	// roomToneDispersionMADs. The sweep at .bench/crosstalk-crest-sweep found k = 4 the
	// validated plateau: a sparse-conversational stem flips from electing an out-of-band region by luck to
	// electing an in-band region on merit, while a noisier stem stays bit-identical and genuine crosstalk
	// is still rejected by the kurtosis and 45 dB co-signals.
	roomToneCrosstalkCrestMADs = 4.0

	// roomToneCrosstalkCrestMADEpsilon floors the candidate crest MAD before applying the
	// crest-outlier gate. A uniform-crest population has MAD ~= 0, so an unfloored gate would
	// reject every above-median candidate; treating a near-zero MAD as "uniform, no outlier"
	// keeps such populations passing. With the floor, only genuine outliers trip the gate.
	// Mirrors roomToneDispersionMADEpsilon; value is well below any meaningful crest dispersion (dB).
	roomToneCrosstalkCrestMADEpsilon = 0.05

	// roomToneDispersionMADs is the RMS-dispersion (MAD) transient gate multiplier.
	// A candidate is rejected when any constituent 250 ms interval's RMS deviates more
	// than this many MADs (median absolute deviations) from the candidate's own RMS median.
	// This is a dimensionless, scale-invariant statistical multiplier - it targets the
	// transient failure mode directly (a lone spiking sub-interval inside an otherwise-quiet
	// region) without a fixed dB cap. The sweep at .bench/roomtone-sweep found k = 4 the
	// operating point (stable across k in [3.5, 4.5]); k = 3 over-rejects, k >= 5 admits
	// transient outliers. Replaces the former fixed silenceCrestFactorMax = 25 dB cap.
	roomToneDispersionMADs = 4.0

	// roomToneDispersionMADEpsilon floors the candidate MAD before applying the dispersion
	// gate. A perfectly steady region has MAD ~= 0, so 4*MAD ~= 0 would reject any tiny
	// numerical variation; treating a near-zero MAD as "steady, accept" keeps steady regions
	// passing. Value is well below any meaningful RMS dispersion (dB).
	roomToneDispersionMADEpsilon = 0.05

	// digitalSilenceRMSThreshold is the maximum RMS level (dBFS) considered digital silence.
	// Voice-activated recording platforms (Riverside, Zencastr) clamp non-speech regions
	// to all-zero samples, pinning RMS at -120.0 dBFS (the FFmpeg astats measurement floor).
	// Genuine room tone never drops below ~-95 dBFS due to preamp thermal noise.
	digitalSilenceRMSThreshold = -115.0 // dBFS - 5 dB margin above measurement floor

	// voiceActivatedDigitalSilenceThreshold is the fraction of room tone candidates
	// that must be digital silence to classify a recording as voice-activated.
	// 95% threshold provides a 10-point margin above the highest known normal recording (Marius: 85%).
	voiceActivatedDigitalSilenceThreshold = 0.95

	// Crest factor penalty thresholds for room tone candidates.
	// Context: These apply to ROOM TONE CANDIDATES (RMS < -70 dBFS).
	// In room tone regions, even modest transients produce extreme crest factors:
	//   Peak -30 dBFS, RMS -74 dBFS -> Crest 44 dB (expected, not pathological)
	crestFactorSoftThreshold = 30.0  // dB - start mild penalty
	crestFactorHardThreshold = 35.0  // dB - require peak check
	peakDangerZoneLow        = -40.0 // dBFS
	peakDangerZoneHigh       = -25.0 // dBFS
	rmsSilenceThreshold      = -70.0 // dBFS

	// Scoring weights (must sum to 1.0)
	stabilityScoreWeight = 0.25
	amplitudeScoreWeight = 0.30
	spectralScoreWeight  = 0.35
	durationScoreWeight  = 0.10

	// Minimum acceptable score for "first wins" selection
	// Candidates below this threshold are skipped in favour of later candidates
	// Set low (0.3) to only reject truly problematic candidates (crosstalk, etc.)
	minAcceptableScore = 0.3

	// selectionTolerance is the maximum score gap at which an earlier candidate is
	// preferred over a later, higher-scoring one. Candidates within this tolerance
	// of the maximum score are considered equivalent; the earliest one wins.
	selectionTolerance = 0.02
)

// findBestRoomToneRegionResult contains the selected region and all evaluated candidates.
type findBestRoomToneRegionResult struct {
	BestRegion *RoomToneRegion
	Candidates []RoomToneCandidateMetrics
}

// refineToGoldenSubregion finds the cleanest sub-region within a room tone candidate.
// Uses existing interval samples to find the window with lowest average RMS.
// Returns the original region if it's already at or below goldenWindowDuration,
// or if refinement fails for any reason (insufficient intervals, etc.).
//
// This addresses cases where a 17.2s candidate at 24.0s absorbed
// both pre-intentional (noisier) and intentional (cleaner) room tone periods.
// By refining to the cleanest 10s window, we isolate the optimal noise profile.
func refineToGoldenSubregion(candidate *RoomToneRegion, intervals []IntervalSample) *RoomToneRegion {
	if candidate == nil {
		return nil
	}

	start, end, dur, ok := refineToSubregion(
		candidate.Start, candidate.End, candidate.Duration,
		intervals,
		goldenWindowDuration, goldenWindowMinimum,
		scoreIntervalWindow,
		func(candidate, current float64) bool { return candidate < current },
	)
	if !ok {
		return candidate
	}

	return &RoomToneRegion{Start: start, End: end, Duration: dur}
}

// detectVoiceActivated determines whether a recording was made with a voice-activated
// platform by examining the fraction of room tone candidates flagged as digital silence.
// Returns true when candidates exist and >= 95% have "digital silence" in their TransientWarning.
func detectVoiceActivated(candidates []RoomToneCandidateMetrics) bool {
	if len(candidates) == 0 {
		return false
	}

	digitalSilenceCount := 0
	for _, c := range candidates {
		if strings.Contains(c.TransientWarning, "digital silence") {
			digitalSilenceCount++
		}
	}

	fraction := float64(digitalSilenceCount) / float64(len(candidates))
	return fraction >= voiceActivatedDigitalSilenceThreshold
}

// findBestRoomToneRegion finds the best room tone region for noise profile extraction.
// Evaluates all candidates regardless of temporal position. Uses a two-pass approach:
// first scores all candidates using multi-metric analysis (amplitude, spectral
// characteristics, stability, duration), then elects the earliest candidate whose
// score is within selectionTolerance of the maximum.
//
// Uses pre-collected interval data for measurements - no file re-reading required.
// Returns an empty result if no suitable region is found.
func findBestRoomToneRegion(regions []RoomToneRegion, intervals []IntervalSample, log debugLogger) *findBestRoomToneRegionResult {
	result := &findBestRoomToneRegionResult{}

	if len(regions) == 0 {
		return result
	}

	var candidates []RoomToneRegion
	for _, r := range regions {
		if r.Duration < minimumSilenceDuration {
			continue
		}
		segments := segmentLongRoomToneRegion(r)
		candidates = append(candidates, segments...)
	}

	if len(candidates) == 0 {
		return result
	}

	// First pass: measure (and refine) every candidate. Scoring needs the
	// candidate-population RMS median (clusterP50), so all candidates must be
	// measured before any are scored.
	measured := make([]*RoomToneCandidateMetrics, 0, len(candidates))
	for i := range candidates {
		candidate := &candidates[i]

		metrics := measureRoomToneCandidateFromIntervals(*candidate, intervals)
		if metrics == nil {
			continue
		}

		if candidate.Duration > goldenWindowDuration {
			refined := refineToGoldenSubregion(candidate, intervals)
			wasRefined := refined.Start != candidate.Start || refined.Duration != candidate.Duration
			if wasRefined {
				refinedMetrics := measureRoomToneCandidateFromIntervals(*refined, intervals)
				if refinedMetrics != nil {
					refinedMetrics.WasRefined = true
					refinedMetrics.OriginalStart = candidate.Start
					refinedMetrics.OriginalDuration = candidate.Duration
					metrics = refinedMetrics
				}
			}
		}

		measured = append(measured, metrics)
	}

	// Crest-outlier crosstalk population: median and MAD of crest factor over the
	// digital-silence survivors, computed PRE-crosstalk (we are deciding crosstalk, so the
	// population must not already be crosstalk-filtered). This is a different population from
	// clusterP50, which is computed over crosstalk+silence survivors. The MAD is floored so a
	// uniform-crest population does not reject every above-median candidate.
	crestMedian, crestMAD := computeCrestOutlierStats(measured)

	// clusterP50: median RMS of candidates surviving the structural rejections we keep
	// (crosstalk + digital silence). This is the candidate-population median, distinct
	// from the interval-distribution median in silenceMedians.rmsP50. The amplitude term
	// rewards proximity to it.
	clusterP50 := computeCandidateClusterP50(measured, crestMedian, crestMAD, log)

	// Second pass: score every measured candidate against clusterP50.
	for _, metrics := range measured {
		metrics.Score = scoreRoomToneCandidate(metrics, clusterP50, crestMedian, crestMAD, log)
		result.Candidates = append(result.Candidates, *metrics)
	}

	if len(result.Candidates) > 0 {
		maxScore := 0.0
		for _, c := range result.Candidates {
			if c.Score > maxScore {
				maxScore = c.Score
			}
		}

		for _, c := range result.Candidates {
			if c.Score >= maxScore-selectionTolerance && c.Score >= minAcceptableScore {
				region := c.Region
				result.BestRegion = &RoomToneRegion{
					Start:    region.Start,
					End:      region.End,
					Duration: region.Duration,
				}
				break
			}
		}
	}

	return result
}

// computeCandidateClusterP50 returns the median RMS of candidates that survive the
// structural rejections we keep (digital silence + crosstalk). These are the same gates
// applied in scoreRoomToneCandidate; survivors here are exactly the candidates that go on
// to receive a non-zero amplitude term. Falls back to the median over all measured
// candidates when none survive the gates (so a cluster median always exists for scoring).
func computeCandidateClusterP50(measured []*RoomToneCandidateMetrics, crestMedian, crestMAD float64, log debugLogger) float64 {
	survivors := make([]float64, 0, len(measured))
	for _, m := range measured {
		if m.RMSLevel <= digitalSilenceRMSThreshold {
			continue
		}
		if isLikelyCrosstalk(m, crestMedian, crestMAD, log) {
			continue
		}
		survivors = append(survivors, m.RMSLevel)
	}

	if len(survivors) > 0 {
		return medianFloat64(survivors)
	}

	all := make([]float64, len(measured))
	for i, m := range measured {
		all[i] = m.RMSLevel
	}
	if len(all) == 0 {
		return 0
	}
	return medianFloat64(all)
}

// computeCrestOutlierStats returns the median and MAD of crest factor over the
// digital-silence-surviving candidate population, PRE-crosstalk (the population must not be
// crosstalk-filtered because these stats drive the crosstalk decision). The MAD is floored at
// roomToneCrosstalkCrestMADEpsilon so a uniform-crest population (MAD ~= 0) does not flag every
// above-median candidate as a crest outlier. Returns (0, epsilon) when no candidate survives
// digital silence, which makes the gate fall back to absolute co-signals (kurtosis, 45 dB gap).
func computeCrestOutlierStats(measured []*RoomToneCandidateMetrics) (crestMedian, crestMAD float64) {
	crests := make([]float64, 0, len(measured))
	for _, m := range measured {
		if m.RMSLevel <= digitalSilenceRMSThreshold {
			continue
		}
		crests = append(crests, m.CrestFactor)
	}

	crestMedian, crestMAD = medianAndMAD(crests)
	if crestMAD < roomToneCrosstalkCrestMADEpsilon {
		crestMAD = roomToneCrosstalkCrestMADEpsilon
	}
	return crestMedian, crestMAD
}

// scoreRoomToneCandidate computes a composite score for a room tone region candidate.
// Higher scores indicate better candidates for noise profiling.
// Returns 0.0 for candidates that should be rejected (e.g., crosstalk detected).
//
// clusterP50 is the median RMS of candidates surviving the crosstalk + digital-silence
// gates; the amplitude term rewards proximity to it (see calculateAmplitudeScore).
func scoreRoomToneCandidate(m *RoomToneCandidateMetrics, clusterP50, crestMedian, crestMAD float64, log debugLogger) float64 {
	if m == nil {
		return 0.0
	}

	if m.RMSLevel <= digitalSilenceRMSThreshold {
		log.Logf("scoreRoomToneCandidate: REJECTING candidate at %.3fs - RMS %.1f dBFS at or below %.1f dBFS threshold (digital silence)",
			m.Region.Start.Seconds(), m.RMSLevel, digitalSilenceRMSThreshold)
		m.TransientWarning = fmt.Sprintf(
			"rejected: RMS %.1f dBFS at or below %.1f dBFS threshold (digital silence from voice-activated recording)",
			m.RMSLevel, digitalSilenceRMSThreshold,
		)
		return 0.0
	}

	isCrosstalk := isLikelyCrosstalk(m, crestMedian, crestMAD, log)
	log.Logf("scoreRoomToneCandidate: start=%.3fs, CrestFactor=%.2f dB, isCrosstalk=%v",
		m.Region.Start.Seconds(), m.CrestFactor, isCrosstalk)
	if isCrosstalk {
		log.Logf("scoreRoomToneCandidate: REJECTING candidate at %.3fs (returning score=0.0)", m.Region.Start.Seconds())
		m.TransientWarning = fmt.Sprintf(
			"rejected: crosstalk detected (crest %.1f dB, centroid %.0f Hz)",
			m.CrestFactor, m.Spectral.Centroid,
		)
		return 0.0
	}

	if maxDev, ok := exceedsRMSDispersion(m.intervals); ok {
		log.Logf("scoreRoomToneCandidate: REJECTING candidate at %.3fs - RMS dispersion %.1f MADs exceeds %.1f MAD gate (transient sub-interval)",
			m.Region.Start.Seconds(), maxDev, roomToneDispersionMADs)
		m.TransientWarning = fmt.Sprintf(
			"rejected: RMS dispersion %.1f MADs exceeds %.1f MAD gate (transient sub-interval)",
			maxDev, roomToneDispersionMADs,
		)
		return 0.0
	}

	ampScore := calculateAmplitudeScore(m.RMSLevel, clusterP50)
	specScore := calculateSpectralScore(m.Spectral.Centroid, m.Spectral.Flatness, m.Spectral.Kurtosis)
	durScore := calculateDurationScore(m.Region.Duration)

	baseScore := ampScore*amplitudeScoreWeight +
		specScore*spectralScoreWeight +
		durScore*durationScoreWeight +
		m.StabilityScore*stabilityScoreWeight

	score := applyCrestFactorPenalty(baseScore, m.CrestFactor, m.PeakLevel, m.RMSLevel)

	if m.CrestFactor > crestFactorHardThreshold && m.PeakLevel > peakDangerZoneLow && m.PeakLevel < peakDangerZoneHigh {
		m.TransientWarning = fmt.Sprintf(
			"elevated crest factor (%.1f dB) with peak at %.1f dBFS - noise profile may include transient content",
			m.CrestFactor, m.PeakLevel,
		)
	}

	return score
}

// calculateStabilityScore computes a 0-1 score for intra-region stability.
// Higher stability = more consistent measurements = likely intentional recording.
//
// The score combines two factors:
//   - RMS variance: low variance indicates consistent amplitude (steady room tone)
//   - Average spectral flux: low flux indicates stable spectral content
//
// Thresholds:
//   - RMS variance: 0 dB² (perfect) to 9 dB² (3 dB std dev, poor)
//     Note: 9 dB² represents a 3 dB standard deviation, intentional room tone
//     should show much lower variance (typically < 1 dB²).
//   - Flux: 0 (perfect) to 0.02 (stability threshold)
//     Aligned with Spectral-Metrics-Reference.md where < 0.005 = "Stable, continuous"
//     and > 0.02 = "High variation" (consonant transitions, transients).
//
// Weighting: RMS variance 60%, flux stability 40% (RMS is the primary discriminator).
func calculateStabilityScore(intervals []IntervalSample) float64 {
	if len(intervals) < 2 {
		return 0.5
	}

	var rmsSum, rmsSquaredSum float64
	for _, iv := range intervals {
		rmsSum += iv.RMSLevel
		rmsSquaredSum += iv.RMSLevel * iv.RMSLevel
	}
	n := float64(len(intervals))
	rmsMean := rmsSum / n
	rmsVariance := (rmsSquaredSum / n) - (rmsMean * rmsMean)

	var fluxSum float64
	for _, iv := range intervals {
		fluxSum += iv.Spectral.Flux
	}
	avgFlux := fluxSum / n

	rmsStabilityScore := max(0.0, min(1.0-(rmsVariance/9.0), 1.0))
	fluxStabilityScore := max(0.0, min(1.0-(avgFlux/0.02), 1.0))

	return rmsStabilityScore*0.6 + fluxStabilityScore*0.4
}

// isLikelyCrosstalk detects if a room tone candidate is likely crosstalk (leaked voice).
// Returns true if centroid is in voice range AND has peaked/impulsive characteristics, OR if
// the crest factor indicates severe transient contamination (centroid-independent).
//
// The in-band crest test is population-relative: a candidate is flagged only when its crest
// exceeds the candidate-population crest median by more than roomToneCrosstalkCrestMADs MADs.
// This replaces a fixed absolute crest cap that wrongly rejected healthy quiet room tone
// (crest inflates as a region quietens). crestMedian/crestMAD are computed over the
// digital-silence-surviving population (see computeCrestOutlierStats); crestMAD is already
// floored by its epsilon, so a uniform-crest population never flags an above-median candidate.
func isLikelyCrosstalk(m *RoomToneCandidateMetrics, crestMedian, crestMAD float64, log debugLogger) bool {
	crestExceedsThreshold := m.CrestFactor > crosstalkPeakRMSGap
	log.Logf("isLikelyCrosstalk: CrestFactor=%.2f dB, threshold=%.2f dB, exceeds=%v",
		m.CrestFactor, crosstalkPeakRMSGap, crestExceedsThreshold)
	if crestExceedsThreshold {
		log.Logf("isLikelyCrosstalk: REJECTING candidate due to crest factor %.2f dB > %.2f dB threshold",
			m.CrestFactor, crosstalkPeakRMSGap)
		return true
	}

	inVoiceRange := m.Spectral.Centroid >= voiceCentroidMin && m.Spectral.Centroid <= voiceCentroidMax
	if !inVoiceRange {
		return false
	}

	if m.Spectral.Kurtosis > crosstalkKurtosisThreshold {
		return true
	}

	crestOutlierLimit := crestMedian + roomToneCrosstalkCrestMADs*crestMAD
	if m.CrestFactor > crestOutlierLimit {
		log.Logf("isLikelyCrosstalk: REJECTING in-band candidate, crest %.2f dB > %.2f dB (median %.2f + %.1f*MAD %.2f)",
			m.CrestFactor, crestOutlierLimit, crestMedian, roomToneCrosstalkCrestMADs, crestMAD)
		return true
	}

	return false
}

// calculateAmplitudeScore rewards proximity to the candidate-population RMS median.
// score = clamp(1 - |candidateRMS - clusterP50| / roomToneAmplitudeDecayDB, 0, 1)
//
// This replaces the former monotonic "quieter-is-better" reward, which elected quiet
// outliers (e.g. a bright breath/fricative tail below the typical cluster). A region
// closest to the surviving-candidate median scores 1.0; the score decays linearly to 0
// as the candidate departs by roomToneAmplitudeDecayDB (the existing 6 dB span).
// clusterP50 is the median RMS of candidates surviving the crosstalk + digital-silence
// gates - the candidate population median, not the interval distribution median.
func calculateAmplitudeScore(rmsLevel, clusterP50 float64) float64 {
	dev := math.Abs(rmsLevel-clusterP50) / roomToneAmplitudeDecayDB
	return max(0.0, min(1.0-dev, 1.0))
}

// exceedsRMSDispersion is the RMS-dispersion (MAD) transient gate. It returns the maximum
// per-interval deviation from the candidate RMS median, in MADs, and whether that deviation
// exceeds roomToneDispersionMADs. A lone spiking sub-interval inside an otherwise-quiet
// region produces a high deviation; steady ambience does not.
//
// The MAD is floored at roomToneDispersionMADEpsilon: a perfectly steady region has MAD ~= 0,
// so an unfloored gate would reject any tiny variation. With the floor, steady regions pass.
// Returns (0, false) when there are too few intervals to assess dispersion.
func exceedsRMSDispersion(intervals []IntervalSample) (maxDeviationMADs float64, exceeds bool) {
	if len(intervals) < 2 {
		return 0, false
	}

	rmsValues := make([]float64, len(intervals))
	for i, iv := range intervals {
		rmsValues[i] = iv.RMSLevel
	}
	median := medianFloat64(rmsValues)

	deviations := make([]float64, len(rmsValues))
	for i, v := range rmsValues {
		deviations[i] = math.Abs(v - median)
	}
	mad := medianFloat64(deviations)
	if mad < roomToneDispersionMADEpsilon {
		mad = roomToneDispersionMADEpsilon
	}

	for _, d := range deviations {
		if dev := d / mad; dev > maxDeviationMADs {
			maxDeviationMADs = dev
		}
	}

	return maxDeviationMADs, maxDeviationMADs > roomToneDispersionMADs
}

// medianFloat64 returns the median of the values. It sorts a copy, leaving the input
// untouched. For even-length input it returns the upper-middle element (matching the
// existing percentile convention in computeSilenceMedians).
func medianFloat64(values []float64) float64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	return sorted[len(sorted)/2]
}

// medianAndMAD returns the median of values and their median absolute deviation about that
// median. Both are scale-invariant robust statistics: the MAD is the median of |v - median|.
// Returns (0, 0) for an empty input. Used by the population-relative crest-outlier crosstalk
// gate (and mirrors the per-interval MAD logic in exceedsRMSDispersion).
func medianAndMAD(values []float64) (median, mad float64) {
	if len(values) == 0 {
		return 0, 0
	}
	median = medianFloat64(values)
	deviations := make([]float64, len(values))
	for i, v := range values {
		deviations[i] = math.Abs(v - median)
	}
	return median, medianFloat64(deviations)
}

// calculateSpectralScore combines spectral metrics into a 0-1 score.
// Rewards: high flatness (noise-like), low kurtosis, centroid outside voice range
func calculateSpectralScore(centroid, flatness, kurtosis float64) float64 {
	var centroidScore float64
	if centroid < voiceCentroidMin || centroid > voiceCentroidMax {
		centroidScore = 1.0
	} else {
		voiceMid := (voiceCentroidMin + voiceCentroidMax) / 2
		voiceHalfWidth := (voiceCentroidMax - voiceCentroidMin) / 2
		distFromMid := math.Abs(centroid - voiceMid)
		centroidScore = distFromMid / voiceHalfWidth * 0.5
	}

	flatnessScore := flatness
	if flatnessScore > 1.0 {
		flatnessScore = 1.0
	}
	if flatnessScore < 0.0 {
		flatnessScore = 0.0
	}

	kurtosisScore := 1.0 - max(0.0, min(kurtosis/20.0, 1.0))

	return centroidScore*0.5 + flatnessScore*0.3 + kurtosisScore*0.2
}

// applyCrestFactorPenalty applies a two-stage penalty for transient contamination.
// Stage 1: Soft penalty for elevated crest factor (maintains ranking stability).
// Stage 2: Hard penalty when the "danger zone" signature is detected.
// See docs/SILENCE-DETECTION-PLAN.md for empirical derivation.
func applyCrestFactorPenalty(score, crestFactor, peak, rms float64) float64 {
	if crestFactor > crestFactorSoftThreshold {
		softPenalty := min(0.2, (crestFactor-crestFactorSoftThreshold)/50)
		score *= (1 - softPenalty)
	}

	if crestFactor > crestFactorHardThreshold &&
		peak > peakDangerZoneLow && peak < peakDangerZoneHigh &&
		rms < rmsSilenceThreshold {
		score *= 0.5
	}

	return score
}

// calculateDurationScore uses a plateau-with-dropoff curve.
// Full score (1.0) for durations in ideal range (8-18s).
// Gaussian dropoff outside the ideal range.
func calculateDurationScore(duration time.Duration) float64 {
	durSecs := duration.Seconds()
	idealMinSecs := idealDurationMin.Seconds()
	idealMaxSecs := idealDurationMax.Seconds()
	sigmaSecs := 5.0

	if durSecs >= idealMinSecs && durSecs <= idealMaxSecs {
		return 1.0
	}

	if durSecs < idealMinSecs {
		diff := durSecs - idealMinSecs
		return math.Exp(-0.5 * (diff / sigmaSecs) * (diff / sigmaSecs))
	}

	diff := durSecs - idealMaxSecs
	return math.Exp(-0.5 * (diff / sigmaSecs) * (diff / sigmaSecs))
}
