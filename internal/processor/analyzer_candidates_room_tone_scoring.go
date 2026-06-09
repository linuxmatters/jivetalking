package processor

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Room tone region scoring for measurement reference extraction.
//
// The "noise profile" is not afftdn training data (anlmdn is self-adapting).
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
	crosstalkKurtosisThreshold    = 10.0 // Above this + voice centroid = likely crosstalk
	crosstalkCrestFactorThreshold = 15.0 // Above this + voice centroid = likely crosstalk
	crosstalkPeakRMSGap           = 45.0 // dB - catches severe transient contamination regardless of spectral content
	// silenceCrestFactorMax is the maximum acceptable crest factor for room tone candidates.
	// Crest factor > 25 dB indicates physical transients (bumps, interference) contaminating
	// the room tone region, making noise floor measurements unreliable.
	// Normal room tone: 5-20 dB; contaminated: 25-45 dB.
	silenceCrestFactorMax = 25.0 // dB - hard rejection above this

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

		score := scoreRoomToneCandidate(metrics, log)
		metrics.Score = score

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
		if result.BestRegion == nil {
			for _, c := range result.Candidates {
				if c.Score > 0.0 && c.Score >= maxScore-selectionTolerance {
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
	}

	return result
}

// scoreRoomToneCandidate computes a composite score for a room tone region candidate.
// Higher scores indicate better candidates for noise profiling.
// Returns 0.0 for candidates that should be rejected (e.g., crosstalk detected).
func scoreRoomToneCandidate(m *RoomToneCandidateMetrics, log debugLogger) float64 {
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

	isCrosstalk := isLikelyCrosstalk(m, log)
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

	if m.CrestFactor > silenceCrestFactorMax {
		log.Logf("scoreRoomToneCandidate: REJECTING candidate at %.3fs - crest factor %.1f dB exceeds %.1f dB threshold",
			m.Region.Start.Seconds(), m.CrestFactor, silenceCrestFactorMax)
		m.TransientWarning = fmt.Sprintf(
			"rejected: crest factor %.1f dB exceeds %.1f dB threshold (transient contamination)",
			m.CrestFactor, silenceCrestFactorMax,
		)
		return 0.0
	}

	ampScore := calculateAmplitudeScore(m.RMSLevel)
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
//     Note: 9 dB² represents a 3 dB standard deviation — intentional room tone
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
// Returns true if centroid is in voice range AND has peaked/impulsive characteristics,
// OR if the crest factor indicates severe transient contamination (centroid-independent).
func isLikelyCrosstalk(m *RoomToneCandidateMetrics, log debugLogger) bool {
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

	if m.CrestFactor > crosstalkCrestFactorThreshold {
		return true
	}

	return false
}

// calculateAmplitudeScore normalises RMS level to a 0-1 score.
// Lower RMS (quieter) = higher score.
// Range: -80 dBFS (best) to -40 dBFS (worst)
func calculateAmplitudeScore(rmsLevel float64) float64 {
	if rmsLevel < -80.0 {
		rmsLevel = -80.0
	}
	if rmsLevel > -40.0 {
		rmsLevel = -40.0
	}

	return (rmsLevel - (-40.0)) / (-80.0 - (-40.0))
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
