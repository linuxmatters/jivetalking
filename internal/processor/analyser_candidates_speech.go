package processor

import (
	"math"
	"time"
)

// Speech detection constants for interval-based analysis
const (
	// Voice frequency range for centroid validation
	speechCentroidMin = 200.0  // Hz - lower bound for speech
	speechCentroidMax = 6000.0 // Hz - upper bound for speech

	// speechMinimumNoiseMarginDB is the protective gap (dB) above the loudest
	// measured room tone that the VAD split and floor anchor must keep, applied on
	// the unified momentary-LUFS axis (the axis the VAD split, floor, and seed
	// share). The former 6.0 silently encoded the RMS-to-LUFS offset (about +4 dB
	// on HF beds) and over-clamped once the seed moved onto the LUFS axis; 2.0 is
	// the honest gap on one scale. Corpus-validated to preserve the elected split:
	// this is a calibration fix and must not move elections.
	speechMinimumNoiseMarginDB = 2.0

	// speechEntropyMax is the maximum entropy for speech (structured signal).
	// Pure noise approaches 1.0; speech is typically 0.3-0.7.
	speechEntropyMax = 0.70
)

// Speech window stability scoring constants
const (
	// voicingDensityThreshold is the target proportion of intervals
	// that should have kurtosis > voicedKurtosisThreshold (voiced speech indicator).
	// Used to normalise voicing density score: 60% density = score 1.0.
	// Regions below this threshold are penalised but can still be compared.
	voicingDensityThreshold = 0.6

	// voicedKurtosisThreshold is the kurtosis level above which
	// an interval is considered "voiced" for density calculation.
	// Reference: Spectral-Metrics-Reference.md shows spoken word target is 4-12,
	// with 5-10 indicating "Clear harmonics" / "Good voice quality".
	// Using 4.5 to include the lower end of spoken word range while
	// excluding "Mixed tonal and noise" content (3-5 range).
	voicedKurtosisThreshold = 4.5

	// rolloffIdealMin/Max define the ideal rolloff range for stable comparison.
	// Aligned with Spectral-Metrics-Reference.md vocal targets:
	//   - Spoken word (male): 4000-8000 Hz
	//   - Spoken word (female): 5000-10000 Hz
	// Using male range as ideal since it captures both genders' lower range.
	rolloffIdealMin = 4000.0 // Hz
	rolloffIdealMax = 8000.0 // Hz

	// rolloffAcceptableMin/Max define the acceptable rolloff range.
	// Expanded to accommodate female vocal targets (up to 10000 Hz).
	// Below 2500 Hz is "Dark, heavy voiced" per reference.
	rolloffAcceptableMin = 2500.0  // Hz
	rolloffAcceptableMax = 10000.0 // Hz

	// Flux thresholds aligned with Spectral-Metrics-Reference.md:
	//   < 0.001: Very stable, sustained (held vowels)
	//   0.001-0.005: Stable, continuous (sustained phonation)
	//   0.005-0.02: Moderate variation (natural articulation)
	//   0.02-0.05: High variation (consonant transitions)
	//   > 0.05: Very high, transient (plosives)
	//
	// Vocal targets from reference:
	//   - Spoken word (sustained vowels): < 0.005
	//   - Spoken word (natural speech): 0.005-0.03

	// fluxStableThreshold: within "Stable, continuous" range (sustained phonation).
	fluxStableThreshold = 0.004

	// fluxNormalThreshold: mid-point of "Moderate variation" (natural articulation).
	fluxNormalThreshold = 0.010

	// fluxTransientThreshold: boundary of "High variation" (consonant transitions).
	fluxTransientThreshold = 0.020

	// fluxAcceptableThreshold: natural speech upper bound.
	fluxAcceptableThreshold = 0.030

	// minSNRMargin is the minimum speech-to-noise-floor gap (dB) for a speech
	// candidate; below it, spectral metrics measure noise rather than speech.
	minSNRMargin = 20.0 // dB

	// snrSaturationMargin is the SNR margin (dB) at which extra margin stops
	// raising the SNR score: candidates at or above it score the SNR maximum.
	// Set by the Phase 3 inform sweep (52-stem corpus, 1040 candidates): it sits at
	// the clean-stem (floor <= -65 dBFS) cleanest-candidate p90 (40.2 dB) and corpus
	// candidate p99 (39.6 dB), below the absolute max (41.9 dB), so only the cleanest
	// stems saturate while the [minSNRMargin, 40] band spreads the axis across the
	// bulk of elected candidates (cleanest-candidate p50 28.7 to p90 38.6 dB).
	snrSaturationMargin = 40.0 // dB (Phase 3 sweep: clean-stem cleanest-candidate p90 / corpus p99)
)

// Scoring weight constants for scoreSpeechIntervalWindow
// Weights sum to 1.0, split between stability (0.55) and quality (0.45)
const (
	weightKurtosis    = 0.15 // Quality: harmonic clarity
	weightFlatness    = 0.10 // Quality: tonal quality
	weightCentroid    = 0.10 // Quality: voice-range frequency
	weightRMS         = 0.10 // Quality: activity level
	weightConsistency = 0.10 // Stability: low variance
	weightVoicing     = 0.15 // Stability: voiced content proportion
	weightRolloff     = 0.15 // Stability: moderate rolloff
	weightFlux        = 0.15 // Stability: low spectral change
)

const (
	// Golden speech region refinement constants
	// After selecting the best speech candidate, refine to a representative sub-window
	// to avoid averaging across pauses that contaminate spectral metrics.
	goldenSpeechWindowDuration = 60 * time.Second // Target: 60s of representative speech
	goldenSpeechWindowMinimum  = 30 * time.Second // Minimum acceptable window
)

// speechDurationAdequacyMinimum is the run length at which the grounded scorer's
// duration term saturates: at or above it a run earns full duration credit, so a
// longer run does NOT outrank a shorter adequate one on duration alone. This is
// the single term protecting voice-activated election, since a sparse short run
// that clears the minimum is not docked for being short.
// Confirmed at 30s by the Phase 3 inform sweep (52 stems): the elected-candidate
// duration distribution stabilises here (p10 = 29.8s, only 7/52 stems elect below
// 30s and all are healthy high-SNR elections), and the shortest voice-activated
// elections (LMP-81s-martin 20.3s, LMP-81s-mark 29.4s) must not be docked, so the
// minimum stays at goldenSpeechWindowMinimum rather than rising.
const speechDurationAdequacyMinimum = goldenSpeechWindowMinimum // Phase 3 sweep: elected-duration p10 ~30s; protects sparse voice-activated runs

// calculateRolloffScore returns a score (0.0-1.0) for spectral rolloff stability.
// Regions with rolloff in the ideal range (4000-8000 Hz) score 1.0.
// Regions in the acceptable range (2500-10000 Hz) score 0.5-1.0.
// Regions outside acceptable range score 0.0.
func calculateRolloffScore(rolloff float64) float64 {
	switch {
	case rolloff >= rolloffIdealMin && rolloff <= rolloffIdealMax:
		return 1.0
	case rolloff >= rolloffAcceptableMin && rolloff < rolloffIdealMin:
		// Below ideal: linear interpolation from 0.5 to 1.0
		return 0.5 + 0.5*(rolloff-rolloffAcceptableMin)/(rolloffIdealMin-rolloffAcceptableMin)
	case rolloff > rolloffIdealMax && rolloff <= rolloffAcceptableMax:
		// Above ideal: linear interpolation from 1.0 to 0.5
		return 0.5 + 0.5*(rolloffAcceptableMax-rolloff)/(rolloffAcceptableMax-rolloffIdealMax)
	default:
		return 0.0
	}
}

// calculateFluxScore returns a score (0.0-1.0) for spectral flux stability.
// Lower flux indicates more stable voicing, which produces more comparable
// before/after metrics.
func calculateFluxScore(flux float64) float64 {
	switch {
	case flux <= fluxStableThreshold:
		return 1.0
	case flux <= fluxNormalThreshold:
		// Linear decay from 1.0 to 0.7
		return 1.0 - (flux-fluxStableThreshold)/(fluxNormalThreshold-fluxStableThreshold)*0.3
	case flux <= fluxTransientThreshold:
		// Linear decay from 0.7 to 0.4
		return 0.7 - (flux-fluxNormalThreshold)/(fluxTransientThreshold-fluxNormalThreshold)*0.3
	case flux <= fluxAcceptableThreshold:
		// Linear decay from 0.4 to 0.2
		return 0.4 - (flux-fluxTransientThreshold)/(fluxAcceptableThreshold-fluxTransientThreshold)*0.2
	default:
		// Floor score for highly dynamic content
		return 0.2
	}
}

// calculateVoicingScore returns a score (0.0-1.0) for voicing density.
// Density at or above voicingDensityThreshold (60%) scores 1.0.
// Lower densities score proportionally less.
func calculateVoicingScore(voicingDensity float64) float64 {
	return max(0.0, min(voicingDensity/voicingDensityThreshold, 1.0))
}

// refineToGoldenSpeechSubregion finds the most representative sub-region within a speech candidate.
// Uses existing interval samples to find the window with highest speech quality score.
// Returns the original region if it's already at or below goldenSpeechWindowDuration,
// or if refinement fails for any reason (insufficient intervals, etc.).
//
// This addresses cases where a long speech region contains pauses that contaminate
// spectral metrics when averaged. By refining to the best 60s window, we isolate
// continuous speech for more accurate adaptive filter tuning.
func refineToGoldenSpeechSubregion(candidate *SpeechRegion, intervals []IntervalSample) *SpeechRegion {
	if candidate == nil {
		return nil
	}

	start, end, dur, ok := refineToSubregion(
		candidate.Start, candidate.End, candidate.Duration,
		intervals,
		goldenSpeechWindowDuration, goldenSpeechWindowMinimum,
		scoreSpeechIntervalWindow,
		func(candidate, current float64) bool { return candidate > current },
	)
	if !ok {
		return candidate
	}

	return &SpeechRegion{Start: start, End: end, Duration: dur}
}

// findBestSpeechRegionResult contains the selected region and all evaluated candidates.
type findBestSpeechRegionResult struct {
	BestRegion *SpeechRegion
	Candidates []SpeechCandidateMetrics
}

// findBestSpeechRegion selects the best speech region for measurements.
// Strategy: elect the highest-scoring candidate (SNR-primary, with a
// saturating duration-adequacy term and a consistency tie-break).
// For long candidates (>60s), refines to the best 60s sub-region to avoid
// contaminating spectral metrics with pauses.
// The noiseProfile parameter enables SNR margin checking to penalise candidates
// too close to the noise floor (where spectral metrics would be unreliable).
func findBestSpeechRegion(regions []SpeechRegion, intervals []IntervalSample, noiseProfile *NoiseProfile, log debugLogger) *findBestSpeechRegionResult {
	result := &findBestSpeechRegionResult{}

	if len(regions) == 0 {
		return result
	}

	// noiseFloorDB feeds the grounded scorer's SNR term. With no noise profile,
	// pass a sentinel far below any level so the SNR margin saturates equally for
	// every candidate, making the term neutral within the file rather than crashing.
	noiseFloorDB := math.Inf(-1)
	if noiseProfile != nil {
		noiseFloorDB = noiseProfile.MeasuredNoiseFloor
	} else {
		log.Logf("SNR margin folded as neutral: no noise profile available")
	}

	var bestCandidate *SpeechRegion
	var bestScore float64
	var fallbackCandidate *SpeechRegion
	var fallbackScore float64
	hasFallback := false

	for i := range regions {
		candidate := &regions[i]

		// Measure speech characteristics from interval data
		metrics := measureSpeechCandidateFromIntervals(*candidate, intervals)
		if metrics == nil {
			continue
		}

		// Score the candidate with the grounded scorer: SNR margin (primary),
		// duration adequacy (saturating), and within-region level consistency
		// (tie-break). The SNR penalty is folded into the score, so no post-hoc
		// penalty runs here.
		regionIntervals := getIntervalsInRange(intervals, candidate.Start, candidate.End)
		levelVar := levelVariance(regionIntervals, axisMomentaryLUFS)
		score := scoreSpeechCandidateGrounded(metrics, noiseFloorDB, levelVar)
		metrics.Score = score

		// Store for reporting
		result.Candidates = append(result.Candidates, *metrics)

		if !hasFallback || score > fallbackScore {
			fallbackRegion := metrics.Region
			fallbackCandidate = &fallbackRegion
			fallbackScore = score
			hasFallback = true
		}

		// Selection: highest score above the sanity floor. The grounded score
		// already encodes "long enough" (duration adequacy) and "clean enough"
		// (SNR), so longest-wins is redundant and removed.
		const minViableSpeechScore = 0.3
		if score >= minViableSpeechScore && (bestCandidate == nil || score > bestScore) {
			bestCandidate = candidate
			bestScore = score
		}
	}

	if bestCandidate == nil && hasFallback {
		bestCandidate = fallbackCandidate
	}

	// Refine long candidates to golden sub-region
	if bestCandidate != nil && bestCandidate.Duration > goldenSpeechWindowDuration {
		originalRegion := *bestCandidate
		refined := refineToGoldenSpeechSubregion(bestCandidate, intervals)

		if refined != nil {
			wasRefined := refined.Start != originalRegion.Start ||
				refined.Duration != originalRegion.Duration

			if wasRefined {
				// Re-measure the refined region
				refinedMetrics := measureSpeechCandidateFromIntervals(*refined, intervals)
				if refinedMetrics != nil {
					refinedIntervals := getIntervalsInRange(intervals, refined.Start, refined.End)
					refinedLevelVar := levelVariance(refinedIntervals, axisMomentaryLUFS)
					refinedMetrics.Score = scoreSpeechCandidateGrounded(refinedMetrics, noiseFloorDB, refinedLevelVar)

					// Store refinement metadata
					refinedMetrics.WasRefined = true
					refinedMetrics.OriginalStart = originalRegion.Start
					refinedMetrics.OriginalDuration = originalRegion.Duration

					// Replace the unrefined candidate in the list
					for i := range result.Candidates {
						if result.Candidates[i].Region.Start == originalRegion.Start {
							result.Candidates[i] = *refinedMetrics
							break
						}
					}

					// Update best region to refined version
					bestCandidate = refined
				}
			}
		}
	}

	result.BestRegion = bestCandidate
	return result
}

// Grounded scorer term weights. SNR margin is primary and duration adequacy
// secondary; the consistency tie-break is a small additive term scaled so it can
// only order candidates that are level on the two primary axes, never overturn an
// SNR or adequacy difference.
const (
	groundedSNRWeight              = 0.6
	groundedDurationWeight         = 0.4
	groundedConsistencyTieBreakMax = 0.02 // additive ceiling for the tie-break

	// groundedConsistencyVarianceCap is the level variance at which the
	// consistency tie-break reaches zero credit; steadier (lower-variance) runs
	// earn up to groundedConsistencyTieBreakMax. The cap only sets the tie-break
	// resolution, not the ranking between candidates that differ on SNR or
	// duration, so it is not a swept threshold.
	groundedConsistencyVarianceCap = 25.0
)

// scoreSpeechCandidateGrounded computes a grounded score for a speech candidate
// from three ordered terms: SNR margin (primary), duration adequacy (saturating),
// and within-region level consistency (tie-break). It folds the former post-hoc
// SNR penalty into the score by taking the noise floor as a parameter, so the
// election helper no longer carries scoring maths (god-function mitigation).
//
// Design note: the old composite clustered candidates at 0.58-0.65 and never
// ranked them, so the SNR axis here MUST spread across the candidates within a
// file. The SNR term is
// relative-within-file: it depends on RMSLevel - noiseFloorDB, so a constant floor
// offset shifts every candidate equally and does not change their ranking. The SNR
// saturation point (snrSaturationMargin) is a placeholder set by the Phase 3
// inform sweep from the corpus SNR-margin distribution; the duration adequacy
// minimum (speechDurationAdequacyMinimum) defaults to goldenSpeechWindowMinimum
// until the same sweep confirms it.
//
// Voice-band centroid, entropy, flatness, rolloff, and flux are NOT terms here:
// they are the VAD veto (passesSpectralVeto, analyser_vad.go), already applied per
// interval before a run reaches election.
//
// noiseFloorDB is the measured noise floor in dBFS. Pass a value at or below the
// quietest level (e.g. math.Inf(-1) sentinel handling by the caller) to make the
// SNR term neutral when no noise profile exists.
func scoreSpeechCandidateGrounded(m *SpeechCandidateMetrics, noiseFloorDB float64, levelVar float64) float64 {
	if m == nil {
		return 0.0
	}

	snrScore := groundedSNRScore(m.RMSLevel - noiseFloorDB)
	durScore := groundedDurationScore(m.Region.Duration)
	tieBreak := groundedConsistencyTieBreak(levelVar)

	return snrScore*groundedSNRWeight + durScore*groundedDurationWeight + tieBreak
}

// groundedSNRScore maps an SNR margin (dB) to a rising, saturating score in
// [0, 1]. Below minSNRMargin the score falls continuously toward 0 (a hard
// penalty, never a hard reject, so a file with only low-SNR runs still elects
// something); at minSNRMargin it reaches 0.5; from there it ramps to 1.0 at
// snrSaturationMargin and saturates. Monotonic in snr, so wider margin never
// scores lower and a below-minimum candidate scores strictly below an
// at-or-above-minimum one.
func groundedSNRScore(snr float64) float64 {
	switch {
	case snr <= 0:
		return 0.0
	case snr < minSNRMargin:
		return 0.5 * (snr / minSNRMargin)
	case snr >= snrSaturationMargin:
		return 1.0
	default:
		return 0.5 + 0.5*(snr-minSNRMargin)/(snrSaturationMargin-minSNRMargin)
	}
}

// groundedDurationScore is the SATURATING duration-adequacy gate: full credit
// (1.0) once the run clears speechDurationAdequacyMinimum, reduced credit below
// it. It is NOT linear past the minimum, so a longer adequate run does not outrank
// a shorter adequate one on duration, and a run at the minimum is not docked
// relative to a longer one. This is the term that protects sparse voice-activated
// delivery.
func groundedDurationScore(duration time.Duration) float64 {
	if duration >= speechDurationAdequacyMinimum {
		return 1.0
	}
	return max(0.0, min(duration.Seconds()/speechDurationAdequacyMinimum.Seconds(), 1.0))
}

// groundedConsistencyTieBreak maps within-region level variance to a small
// additive term in [0, groundedConsistencyTieBreakMax]: steadier (lower-variance)
// runs earn more. Bounded so it only orders candidates level on SNR and duration.
func groundedConsistencyTieBreak(levelVar float64) float64 {
	steadiness := max(0.0, min(1.0-(levelVar/groundedConsistencyVarianceCap), 1.0))
	return steadiness * groundedConsistencyTieBreakMax
}
