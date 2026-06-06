package processor

import (
	"math"
	"sort"
	"time"
)

// Speech detection constants for interval-based analysis
const (
	// minimumSpeechIntervals is the minimum consecutive intervals for a speech candidate.
	// 30 seconds / 250ms = 120 intervals
	minimumSpeechIntervals = 120

	// speechInterruptionToleranceIntervals allows natural pauses within speech.
	// 8 intervals = 2 seconds tolerance for breaths, brief pauses.
	speechInterruptionToleranceIntervals = 8

	// voiceActivatedSpeechInterruptionToleranceIntervals is the widened tolerance
	// for voice-activated recordings where digital silence gaps replace natural pauses.
	// 40 intervals = 10 seconds, bridging platform-inserted gaps up to 10s.
	voiceActivatedSpeechInterruptionToleranceIntervals = 40

	// speechSearchStartBuffer adds time after the elected room-tone region end before searching for speech.
	// Allows transition from room tone to actual speech content.
	speechSearchStartBuffer = 2 * time.Second

	// Voice frequency range for centroid validation
	speechCentroidMin = 200.0  // Hz - lower bound for speech
	speechCentroidMax = 4500.0 // Hz - upper bound for speech

	// speechRMSMinimumDefault is the fallback minimum RMS level to be considered speech (not silence).
	// Used when adaptive computation is not possible (zero or -Inf measurements).
	speechRMSMinimumDefault = -40.0 // dBFS

	// speechRMSMinimumOffset is the dB below RMS level for the adaptive speech threshold.
	speechRMSMinimumOffset = 12.0

	// speechRMSMinimumNoiseMargin is the dB above noise floor for the adaptive speech threshold.
	speechRMSMinimumNoiseMargin = 6.0

	// speechEntropyMax is the maximum entropy for speech (structured signal).
	// Pure noise approaches 1.0; speech is typically 0.3-0.7.
	speechEntropyMax = 0.70
)

// computeSpeechRMSMinimum returns the adaptive minimum RMS level for speech detection.
// Formula: max(rmsLevel - 12, noiseFloor + 6).
// Falls back to speechRMSMinimumDefault when measurements are zero or -Inf.
func computeSpeechRMSMinimum(rmsLevel, noiseFloor float64) float64 {
	if rmsLevel == 0 || noiseFloor == 0 || math.IsInf(rmsLevel, -1) || math.IsInf(noiseFloor, -1) {
		return speechRMSMinimumDefault
	}
	return math.Max(rmsLevel-speechRMSMinimumOffset, noiseFloor+speechRMSMinimumNoiseMargin)
}

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

	// Crest factor scoring parameters
	// Reference: Spectral-Metrics-Reference.md shows spoken word optimal is 9-14 dB
	crestFactorMin   = 9.0  // dB - minimum acceptable
	crestFactorMax   = 18.0 // dB - maximum acceptable
	crestFactorIdeal = 12.0 // dB - optimal for spoken word
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

// Scoring weight constants for scoreSpeechCandidate
// Weights sum to 1.0, split between stability (0.40) and quality (0.60)
const (
	candidateWeightAmplitude = 0.20 // Quality: louder = better sample
	candidateWeightCentroid  = 0.15 // Quality: voice range
	candidateWeightCrest     = 0.15 // Quality: typical speech dynamics
	candidateWeightDuration  = 0.10 // Quality: longer = more representative
	candidateWeightVoicing   = 0.10 // Stability: voiced content proportion
	candidateWeightRolloff   = 0.15 // Stability: moderate rolloff
	candidateWeightFlux      = 0.15 // Stability: low spectral change
)

const (
	// Golden speech region refinement constants
	// After selecting the best speech candidate, refine to a representative sub-window
	// to avoid averaging across pauses that contaminate spectral metrics.
	goldenSpeechWindowDuration = 60 * time.Second // Target: 60s of representative speech
	goldenSpeechWindowMinimum  = 30 * time.Second // Minimum acceptable window
)

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
	return clamp(voicingDensity/voicingDensityThreshold, 0.0, 1.0)
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

// speechScore calculates how speech-like an interval is.
// Returns 0.0-1.0 where higher = more likely to be speech.
// Inverts silence detection criteria: rewards amplitude, voice-range centroid, low entropy.
func speechScore(interval IntervalSample, rmsP50 float64, speechRMSMin float64) float64 {
	// Reject if too quiet (likely silence/room tone)
	if interval.RMSLevel < speechRMSMin {
		return 0.0
	}

	// Amplitude score: louder relative to median = better
	// Score decays below median, peaks at +6dB above median
	ampScore := 0.0
	if interval.RMSLevel >= rmsP50 {
		// Above median: score increases up to +6dB
		boost := interval.RMSLevel - rmsP50
		ampScore = clamp(boost/6.0, 0.0, 1.0)
	}

	// Centroid score: voice range (200-4500 Hz) = good
	centroidScore := 0.0
	if interval.Spectral.Centroid >= speechCentroidMin && interval.Spectral.Centroid <= speechCentroidMax {
		// In voice range - score based on how central
		voiceMid := (speechCentroidMin + speechCentroidMax) / 2
		voiceHalfWidth := (speechCentroidMax - speechCentroidMin) / 2
		distFromMid := math.Abs(interval.Spectral.Centroid - voiceMid)
		centroidScore = 1.0 - (distFromMid / voiceHalfWidth * 0.5)
	}

	// Entropy score: lower entropy = more structured = more speech-like
	entropyScore := 0.0
	if interval.Spectral.Entropy < speechEntropyMax {
		entropyScore = 1.0 - (interval.Spectral.Entropy / speechEntropyMax)
	}

	// Weighted combination: amplitude most important, then centroid, then entropy
	return ampScore*0.5 + centroidScore*0.3 + entropyScore*0.2
}

// findSpeechCandidatesFromIntervals identifies speech regions from interval samples.
// Only searches after roomToneEnd to ensure speech follows the elected room-tone candidate.
// Uses a speech score approach that rewards amplitude, voice-range centroid, and low entropy.
//
// Detection algorithm:
// 1. Start searching after roomToneEnd + buffer
// 2. Calculate reference values (medians) for speech scoring
// 3. Score each interval for "speech likelihood"
// 4. Find consecutive runs that meet minimum duration (30 seconds)
// 5. Allow brief interruptions (2s) for natural pauses
func findSpeechCandidatesFromIntervals(intervals []IntervalSample, roomToneEnd time.Duration, voiceActivated bool, rmsLevel, noiseFloor float64) []SpeechRegion {
	if len(intervals) < minimumSpeechIntervals {
		return nil
	}

	speechRMSMin := computeSpeechRMSMinimum(rmsLevel, noiseFloor)

	// Find start index: after elected room-tone region end + buffer
	searchStart := roomToneEnd + speechSearchStartBuffer
	startIdx := -1
	for i, interval := range intervals {
		if interval.Timestamp >= searchStart {
			startIdx = i
			break
		}
	}

	if startIdx < 0 {
		return nil // No intervals found at or after search start
	}

	if len(intervals)-startIdx < minimumSpeechIntervals {
		return nil // Not enough intervals after the elected room-tone region
	}

	searchIntervals := intervals[startIdx:]

	// Calculate medians for speech scoring
	rmsLevels := make([]float64, len(searchIntervals))
	for i, interval := range searchIntervals {
		rmsLevels[i] = interval.RMSLevel
	}
	sort.Float64s(rmsLevels)

	rmsP50 := rmsLevels[len(rmsLevels)/2]

	// Speech score threshold (lower than the room-tone candidate threshold since speech varies more)
	const speechScoreThreshold = 0.4

	// Select interruption tolerance based on voice-activated status
	interruptionTolerance := speechInterruptionToleranceIntervals
	if voiceActivated {
		interruptionTolerance = voiceActivatedSpeechInterruptionToleranceIntervals
	}

	var candidates []SpeechRegion
	var speechStart time.Duration
	var speechIntervalCount int
	var interruptionCount int
	inSpeech := false

	for i := range len(searchIntervals) {
		interval := searchIntervals[i]
		score := speechScore(interval, rmsP50, speechRMSMin)
		isSpeech := score >= speechScoreThreshold

		if isSpeech {
			if !inSpeech {
				// Start of potential speech region
				speechStart = interval.Timestamp
				speechIntervalCount = 1
				interruptionCount = 0
				inSpeech = true
			} else {
				speechIntervalCount++
				interruptionCount = 0
			}
		} else if inSpeech {
			// Not speech - count as interruption
			interruptionCount++

			if interruptionCount > interruptionTolerance {
				// Too many consecutive interruptions - end speech region
				lastSpeechIdx := i - interruptionCount
				if speechIntervalCount >= minimumSpeechIntervals && lastSpeechIdx >= 0 && lastSpeechIdx < len(searchIntervals) {
					endTime := searchIntervals[lastSpeechIdx].Timestamp + 250*time.Millisecond
					duration := endTime - speechStart
					candidates = append(candidates, SpeechRegion{
						Start:    speechStart,
						End:      endTime,
						Duration: duration,
					})
				}
				inSpeech = false
				speechIntervalCount = 0
				interruptionCount = 0
			}
		}
	}

	// Handle speech extending to end of file
	if inSpeech && speechIntervalCount >= minimumSpeechIntervals {
		lastInterval := searchIntervals[len(searchIntervals)-1]
		endTime := lastInterval.Timestamp + 250*time.Millisecond
		duration := endTime - speechStart
		candidates = append(candidates, SpeechRegion{
			Start:    speechStart,
			End:      endTime,
			Duration: duration,
		})
	}

	return candidates
}

// findBestSpeechRegionResult contains the selected region and all evaluated candidates.
type findBestSpeechRegionResult struct {
	BestRegion *SpeechRegion
	Candidates []SpeechCandidateMetrics
}

// findBestSpeechRegion selects the best speech region for measurements.
// Strategy: prefer longest duration that meets quality threshold.
// Unlike room-tone candidate selection (where earlier is better), speech benefits from longer samples.
// For long candidates (>60s), refines to the best 60s sub-region to avoid
// contaminating spectral metrics with pauses.
// The noiseProfile parameter enables SNR margin checking to penalise candidates
// too close to the noise floor (where spectral metrics would be unreliable).
func findBestSpeechRegion(regions []SpeechRegion, intervals []IntervalSample, noiseProfile *NoiseProfile, log debugLogger) *findBestSpeechRegionResult {
	result := &findBestSpeechRegionResult{}

	if len(regions) == 0 {
		return result
	}

	var bestCandidate *SpeechRegion
	var bestDuration time.Duration
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

		// Score the candidate
		score := scoreSpeechCandidate(metrics)
		metrics.Score = score

		// SNR margin check: penalise candidates too close to noise floor.
		// These will show dramatic spectral shifts after denoising because
		// the metrics are measuring noise contribution rather than speech.
		// Both RMSLevel and MeasuredNoiseFloor are in dBFS.
		if noiseProfile != nil {
			snrMargin := metrics.RMSLevel - noiseProfile.MeasuredNoiseFloor
			if snrMargin < minSNRMargin {
				log.Logf("Speech candidate at %.1fs has low SNR margin: %.1f dB < %.1f dB minimum",
					candidate.Start.Seconds(), snrMargin, minSNRMargin)
				// Apply penalty factor rather than rejecting outright
				// This allows selection if no better candidates exist
				snrPenalty := snrMargin / minSNRMargin // 0.0 to 1.0
				score *= clamp(snrPenalty, 0.1, 1.0)
				metrics.Score = score
			}
		} else {
			log.Logf("SNR margin check skipped: no noise profile available")
		}

		// Store for reporting
		result.Candidates = append(result.Candidates, *metrics)

		if !hasFallback || score > fallbackScore {
			fallbackRegion := metrics.Region
			fallbackCandidate = &fallbackRegion
			fallbackScore = score
			hasFallback = true
		}

		// Selection: longest candidate above minimum quality
		const minAcceptableSpeechScore = 0.3
		if score >= minAcceptableSpeechScore && candidate.Duration > bestDuration {
			bestCandidate = candidate
			bestDuration = candidate.Duration
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
					refinedMetrics.Score = scoreSpeechCandidate(refinedMetrics)

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

// scoreSpeechCandidate computes a composite score for a speech region candidate.
// Higher scores indicate better candidates for speech profiling.
func scoreSpeechCandidate(m *SpeechCandidateMetrics) float64 {
	if m == nil {
		return 0.0
	}

	// Amplitude score: louder speech = better sample
	ampScore := 0.0
	if m.RMSLevel > -30.0 {
		ampScore = clamp((m.RMSLevel-(-30.0))/18.0, 0.0, 1.0)
	}

	// Centroid score: voice range = good
	centroidScore := 0.0
	if m.Spectral.Centroid >= speechCentroidMin && m.Spectral.Centroid <= speechCentroidMax {
		centroidScore = 1.0
	}

	// Crest factor score: typical speech crest (9-14 dB optimal) = good
	// Reference: Spectral-Metrics-Reference.md shows spoken word optimal is 9-14 dB
	crestScore := 0.0
	if m.CrestFactor >= crestFactorMin && m.CrestFactor <= crestFactorMax {
		distFromIdeal := math.Abs(m.CrestFactor - crestFactorIdeal)
		maxDist := max(crestFactorIdeal-crestFactorMin, crestFactorMax-crestFactorIdeal)
		crestScore = clamp(1.0-(distFromIdeal/maxDist), 0.0, 1.0)
	}

	// Duration score: longer = better (up to 60s, then plateau)
	durScore := clamp(m.Region.Duration.Seconds()/60.0, 0.0, 1.0)

	// Voicing density score: prefer high voiced content proportion
	// Uses shared helper function for consistency with scoreSpeechIntervalWindow
	voicingScore := calculateVoicingScore(m.VoicingDensity)

	// Rolloff score: prefer moderate rolloff for processing stability
	// Uses shared helper function for consistency with scoreSpeechIntervalWindow
	rolloffScore := calculateRolloffScore(m.Spectral.Rolloff)

	// Flux score: prefer low flux for processing stability
	// Uses shared helper function for consistency with scoreSpeechIntervalWindow
	fluxScore := calculateFluxScore(m.Spectral.Flux)

	// Weighted combination using named constants
	return ampScore*candidateWeightAmplitude +
		centroidScore*candidateWeightCentroid +
		crestScore*candidateWeightCrest +
		durScore*candidateWeightDuration +
		voicingScore*candidateWeightVoicing +
		rolloffScore*candidateWeightRolloff +
		fluxScore*candidateWeightFlux
}
