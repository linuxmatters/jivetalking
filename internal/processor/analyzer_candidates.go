package processor

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Silence detection constants for interval-based analysis
const (
	// minimumSilenceIntervals is the minimum number of consecutive silent intervals
	// for a region to be considered a valid silence candidate.
	// Must match minimumSilenceDuration (8s) for profile extraction: 8s / 250ms = 32 intervals
	minimumSilenceIntervals = 32

	// roomToneAmplitudeDecayDB is the dB range above median where amplitude score decays from 1.0 to 0.0.
	// 6dB above median = score of 0.0.
	roomToneAmplitudeDecayDB = 6.0

	// roomToneAmplitudeWeight is the weighting factor for amplitude in room tone scoring.
	// Amplitude is weighted more heavily (0.6) since it's the primary discriminator.
	roomToneAmplitudeWeight = 0.6

	// roomToneFluxWeight is the weighting factor for spectral flux in room tone scoring.
	roomToneFluxWeight = 0.4

	// silenceThresholdMinIntervals is the minimum number of intervals required for threshold calculation.
	silenceThresholdMinIntervals = 10

	// roomToneCandidatePercent is the percentage of top-scored intervals to use as room tone candidates (20%).
	roomToneCandidatePercent = 5 // divisor: len/5 = 20%

	// roomToneCandidateMinCount is the minimum number of room tone candidate intervals.
	roomToneCandidateMinCount = 8

	// silenceThresholdHeadroomDB is additional dB added to the detected room tone level for headroom.
	silenceThresholdHeadroomDB = 1.0

	// interruptionToleranceIntervals is the number of consecutive non-silent intervals allowed
	// within a silence region without breaking it. 3 intervals = 750ms tolerance.
	interruptionToleranceIntervals = 3

	// roomToneScoreThreshold is the minimum score (0-1) for an interval to be considered room tone.
	roomToneScoreThreshold = 0.5

	// Golden sub-region refinement constants
	// After selecting the best silence candidate, refine to the cleanest sub-window
	// to isolate optimal noise profile (avoids pre-intentional silence contamination).
	goldenWindowDuration = 10 * time.Second       // Target duration for refined region
	goldenWindowMinimum  = 8 * time.Second        // Minimum acceptable refined duration
	goldenIntervalSize   = 250 * time.Millisecond // Must match interval sampling (intervalDuration)
)

// Speech detection constants for interval-based analysis
const (
	// minimumSpeechIntervals is the minimum consecutive intervals for a speech candidate.
	// 30 seconds / 250ms = 120 intervals
	minimumSpeechIntervals = 120

	// minimumSpeechDuration is the minimum duration for speech candidate selection.
	minimumSpeechDuration = 30 * time.Second

	// speechInterruptionToleranceIntervals allows natural pauses within speech.
	// 8 intervals = 2 seconds tolerance for breaths, brief pauses.
	speechInterruptionToleranceIntervals = 8

	// speechSearchStartBuffer adds time after silence end before searching for speech.
	// Allows transition from room tone to actual speech content.
	speechSearchStartBuffer = 2 * time.Second

	// Voice frequency range for centroid validation
	speechCentroidMin = 200.0  // Hz - lower bound for speech
	speechCentroidMax = 4500.0 // Hz - upper bound for speech

	// speechRMSMinimum is the minimum RMS level to be considered speech (not silence).
	// Set relative to typical normalised speech levels.
	speechRMSMinimum = -40.0 // dBFS

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

	// SNR margin for noise floor separation (see Phase 7)
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

const (
	// Golden speech region refinement constants
	// After selecting the best speech candidate, refine to a representative sub-window
	// to avoid averaging across pauses that contaminate spectral metrics.
	goldenSpeechWindowDuration = 60 * time.Second // Target: 60s of representative speech
	goldenSpeechWindowMinimum  = 30 * time.Second // Minimum acceptable window
)

// refineToSubregion implements the shared sliding-window refinement logic used by both
// silence and speech sub-region selection. It finds the best-scoring contiguous window
// within the given time range, where "best" is determined by the provided scoring function
// and comparison: isBetter(candidate, current) returns true when candidate should replace current.
//
// Returns the refined start, end, and duration. If refinement is not possible (insufficient
// intervals, already within target), returns the original bounds unchanged and ok=false.
func refineToSubregion(
	start, end, duration time.Duration,
	intervals []IntervalSample,
	windowDuration, windowMinimum time.Duration,
	score func([]IntervalSample) float64,
	isBetter func(candidate, current float64) bool,
) (refinedStart, refinedEnd, refinedDuration time.Duration, ok bool) {
	// No refinement needed if already at or below target duration
	if duration <= windowDuration {
		return start, end, duration, false
	}

	// Extract intervals within the candidate's time range
	candidateIntervals := getIntervalsInRange(intervals, start, end)
	if candidateIntervals == nil {
		return start, end, duration, false
	}

	// Calculate window size in intervals
	windowIntervals := int(windowDuration / goldenIntervalSize)
	minimumIntervals := int(windowMinimum / goldenIntervalSize)

	// Need at least minimum window worth of intervals
	if len(candidateIntervals) < minimumIntervals {
		return start, end, duration, false
	}

	// If we have fewer intervals than target window, use what we have
	if len(candidateIntervals) < windowIntervals {
		windowIntervals = len(candidateIntervals)
	}

	// Slide window across intervals, finding the position with the best score
	bestStartIdx := 0
	bestScore := score(candidateIntervals[:windowIntervals])

	for startIdx := 1; startIdx <= len(candidateIntervals)-windowIntervals; startIdx++ {
		windowScore := score(candidateIntervals[startIdx : startIdx+windowIntervals])
		if isBetter(windowScore, bestScore) {
			bestScore = windowScore
			bestStartIdx = startIdx
		}
	}

	// Calculate refined region bounds from the best window position
	refinedStart = candidateIntervals[bestStartIdx].Timestamp
	refinedDuration = time.Duration(windowIntervals) * goldenIntervalSize
	refinedEnd = refinedStart + refinedDuration

	return refinedStart, refinedEnd, refinedDuration, true
}

// refineToGoldenSubregion finds the cleanest sub-region within a silence candidate.
// Uses existing interval samples to find the window with lowest average RMS.
// Returns the original region if it's already at or below goldenWindowDuration,
// or if refinement fails for any reason (insufficient intervals, etc.).
//
// This addresses cases where a 17.2s candidate at 24.0s absorbed
// both pre-intentional (noisier) and intentional (cleaner) silence periods.
// By refining to the cleanest 10s window, we isolate the optimal noise profile.
func refineToGoldenSubregion(candidate *SilenceRegion, intervals []IntervalSample) *SilenceRegion {
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

	return &SilenceRegion{Start: start, End: end, Duration: dur}
}

// getIntervalsInRange returns intervals that fall within the given time range.
// Returns nil if no intervals found in range.
func getIntervalsInRange(intervals []IntervalSample, start, end time.Duration) []IntervalSample {
	if len(intervals) == 0 {
		return nil
	}

	// Find first interval at or after start time using binary search
	// (intervals are sorted by timestamp from the collection loop in AnalyzeAudio)
	startIdx := sort.Search(len(intervals), func(i int) bool {
		return intervals[i].Timestamp >= start
	})
	if startIdx >= len(intervals) {
		return nil
	}

	// Collect intervals until we reach or exceed end time
	var result []IntervalSample
	for i := startIdx; i < len(intervals); i++ {
		if intervals[i].Timestamp >= end {
			break
		}
		result = append(result, intervals[i])
	}

	if len(result) == 0 {
		return nil
	}

	return result
}

// measureSilenceCandidateFromIntervals computes metrics for a silence region using pre-collected interval data.
// This avoids re-reading the audio file - all measurements come from Pass 1's interval samples.
// Returns nil if no intervals fall within the region (should not happen for valid candidates).
func measureSilenceCandidateFromIntervals(region SilenceRegion, intervals []IntervalSample) *SilenceCandidateMetrics {
	// Extract intervals within the candidate region
	regionIntervals := getIntervalsInRange(intervals, region.Start, region.Start+region.Duration)
	if len(regionIntervals) == 0 {
		return nil
	}

	// Accumulate metrics for averaging (sums) and extremes (max)
	var rmsSum float64
	var peakMax, truePeakMax, samplePeakMax float64 = -120.0, -120.0, -120.0
	var spectralSum SpectralMetrics
	var momentarySum, shortTermSum float64

	for _, interval := range regionIntervals {
		rmsSum += interval.RMSLevel
		if interval.PeakLevel > peakMax {
			peakMax = interval.PeakLevel
		}

		spectralSum.add(interval.spectralFields())

		momentarySum += interval.MomentaryLUFS
		shortTermSum += interval.ShortTermLUFS
		if interval.TruePeak > truePeakMax {
			truePeakMax = interval.TruePeak
		}
		if interval.SamplePeak > samplePeakMax {
			samplePeakMax = interval.SamplePeak
		}
	}

	n := float64(len(regionIntervals))
	avgRMS := rmsSum / n
	avgSpectral := spectralSum.average(n)

	return &SilenceCandidateMetrics{
		Region:      region,
		RMSLevel:    avgRMS,
		PeakLevel:   peakMax,
		CrestFactor: peakMax - avgRMS,
		Spectral:    avgSpectral,

		MomentaryLUFS: momentarySum / n,
		ShortTermLUFS: shortTermSum / n,
		TruePeak:      truePeakMax,
		SamplePeak:    samplePeakMax,

		StabilityScore: calculateStabilityScore(regionIntervals),
	}
}

// extractNoiseProfileFromIntervals creates a NoiseProfile using pre-collected interval data.
// This avoids re-reading the audio file - all measurements come from Pass 1's interval samples.
// Returns nil if no intervals fall within the region.
func extractNoiseProfileFromIntervals(region *SilenceRegion, intervals []IntervalSample) *NoiseProfile {
	if region == nil {
		return nil
	}

	// Extract intervals within the silence region
	regionIntervals := getIntervalsInRange(intervals, region.Start, region.Start+region.Duration)
	if len(regionIntervals) == 0 {
		return nil
	}

	// Accumulate metrics for averaging
	var rmsSum, peakMax float64
	var entropySum, centroidSum, flatnessSum, kurtosisSum float64
	peakMax = -120.0

	for _, interval := range regionIntervals {
		rmsSum += interval.RMSLevel
		if interval.PeakLevel > peakMax {
			peakMax = interval.PeakLevel
		}
		entropySum += interval.SpectralEntropy
		centroidSum += interval.SpectralCentroid
		flatnessSum += interval.SpectralFlatness
		kurtosisSum += interval.SpectralKurtosis
	}

	n := float64(len(regionIntervals))
	avgRMS := rmsSum / n

	// Build noise profile from interval data
	profile := &NoiseProfile{
		Start:              region.Start,
		Duration:           region.Duration,
		MeasuredNoiseFloor: avgRMS,
		PeakLevel:          peakMax,
		CrestFactor:        peakMax - avgRMS, // Peak - RMS in dB
		Entropy:            entropySum / n,
		SpectralCentroid:   centroidSum / n,
		SpectralFlatness:   flatnessSum / n,
		SpectralKurtosis:   kurtosisSum / n,
	}

	// Record warning if using silence region outside ideal range (8-18s)
	if region.Duration < idealDurationMin {
		profile.ExtractionWarning = fmt.Sprintf("using short silence region (%.1fs) - ideally need >=%ds", region.Duration.Seconds(), int(idealDurationMin.Seconds()))
	} else if region.Duration > idealDurationMax {
		profile.ExtractionWarning = fmt.Sprintf("using long silence region (%.1fs) - ideally <=%ds", region.Duration.Seconds(), int(idealDurationMax.Seconds()))
	}

	return profile
}

// scoreIntervalWindow calculates a quality score for a contiguous window of intervals.
// Returns average RMS level in dBFS (lower = better/quieter).
// Could be extended to incorporate spectral stability (flux variance) if needed.
func scoreIntervalWindow(intervals []IntervalSample) float64 {
	if len(intervals) == 0 {
		return 0 // Should not happen in normal use
	}

	var sumRMS float64
	for _, interval := range intervals {
		sumRMS += interval.RMSLevel
	}
	return sumRMS / float64(len(intervals))
}

// scoreSpeechIntervalWindow calculates a quality score for a contiguous window of speech intervals.
// Returns a 0-1 score where higher = better quality speech for profiling.
// Scores based on spectral characteristics that indicate clear, continuous speech,
// with emphasis on stability for reliable before/after comparison:
//
// Stability weights (0.55):
//   - Voicing (0.15): high voiced content = predictable behaviour
//   - Consistency (0.10): low variance = stable across window
//   - Rolloff (0.15): moderate rolloff = stable after NR
//   - Flux (0.15): low flux = sustained voicing
//
// Quality weights (0.45):
//   - Kurtosis (0.15): harmonic clarity
//   - Flatness (0.10): tonal quality
//   - Centroid (0.10): voice-range frequency
//   - RMS (0.10): activity level
func scoreSpeechIntervalWindow(intervals []IntervalSample) float64 {
	if len(intervals) == 0 {
		return 0 // Should not happen in normal use
	}

	n := float64(len(intervals))

	// Accumulate metrics
	var kurtosisSum, flatnessSum, centroidSum, rmsSum float64
	var rolloffSum, fluxSum float64
	kurtosisValues := make([]float64, len(intervals))

	for i, interval := range intervals {
		kurtosisSum += interval.SpectralKurtosis
		flatnessSum += interval.SpectralFlatness
		centroidSum += interval.SpectralCentroid
		rmsSum += interval.RMSLevel
		rolloffSum += interval.SpectralRolloff
		fluxSum += interval.SpectralFlux
		kurtosisValues[i] = interval.SpectralKurtosis
	}

	avgKurtosis := kurtosisSum / n
	avgFlatness := flatnessSum / n
	avgCentroid := centroidSum / n
	avgRMS := rmsSum / n
	avgRolloff := rolloffSum / n
	avgFlux := fluxSum / n

	// Calculate kurtosis variance for consistency score
	var kurtosisVarianceSum float64
	for _, k := range kurtosisValues {
		diff := k - avgKurtosis
		kurtosisVarianceSum += diff * diff
	}
	kurtosisVariance := kurtosisVarianceSum / n

	// Voicing density score: prefer regions with high proportion of voiced content.
	// Regions with low voicing density (< 60% of intervals with kurtosis > 4.5)
	// contain too much unvoiced content (fricatives, stops, silence) for stable
	// comparison. Rather than using a hard gate that prevents differentiation
	// among low-density candidates (e.g., whispered speech, heavily accented speech),
	// we use a weighted score component that allows relative ranking.
	voicedCount := 0
	for _, k := range kurtosisValues {
		if k > voicedKurtosisThreshold {
			voicedCount++
		}
	}
	voicingDensity := float64(voicedCount) / n
	voicingScore := calculateVoicingScore(voicingDensity)
	// voicingScore: 0.0 at 0% density, 1.0 at 60%+ density

	// Kurtosis score: higher kurtosis = clearer harmonics
	// Typical speech kurtosis ranges 5-10; score peaks around 7.5 (mid-point)
	// Reference: Gaussian kurtosis=3; speech harmonic structure produces 5-10
	kurtosisScore := clamp(avgKurtosis/7.5, 0.0, 1.0)

	// Flatness score: lower flatness = more tonal = better speech
	// Flatness 0 = pure tone, 1 = white noise; speech typically 0.1-0.4
	flatnessScore := clamp(1.0-avgFlatness, 0.0, 1.0)

	// Centroid score: peak at voice centre, decay toward edges
	// Voice range: speechCentroidMin (200 Hz) to speechCentroidMax (4500 Hz)
	centroidScore := 0.0
	if avgCentroid >= speechCentroidMin && avgCentroid <= speechCentroidMax {
		// Calculate distance from ideal centre (~2000 Hz)
		voiceMid := (speechCentroidMin + speechCentroidMax) / 2
		voiceHalfWidth := (speechCentroidMax - speechCentroidMin) / 2
		distFromMid := math.Abs(avgCentroid - voiceMid)
		// Score decays to 0.5 at edges, 1.0 at centre
		centroidScore = 1.0 - (distFromMid/voiceHalfWidth)*0.5
	}

	// Consistency score: low kurtosis variance = stable voicing
	// Variance > 100 is very inconsistent; clamp score at that point
	consistencyScore := clamp(1.0-(kurtosisVariance/100.0), 0.0, 1.0)

	// RMS score: louder = more active speech
	// Range: -30 dBFS (worst) to -12 dBFS (best)
	rmsScore := 0.0
	if avgRMS > -30.0 {
		rmsScore = clamp((avgRMS-(-30.0))/18.0, 0.0, 1.0)
	}

	// Rolloff score: prefer regions with rolloff in typical voiced speech range.
	// Uses shared helper function for consistency with scoreSpeechCandidate.
	rolloffScore := calculateRolloffScore(avgRolloff)

	// Flux score: prefer regions with low spectral flux (stable voicing).
	// Uses shared helper function for consistency with scoreSpeechCandidate.
	fluxScore := calculateFluxScore(avgFlux)

	// Weighted combination optimised for measurement stability
	// Weights sum to 1.0
	//
	// Stability-focused weights:
	//   - Voicing (0.15): high voiced content = predictable behaviour
	//   - Consistency (0.10): low variance = stable across window
	//   - Rolloff (0.15): moderate rolloff = stable after NR
	//   - Flux (0.15): low flux = sustained voicing
	//
	// Quality weights (reduced from original):
	//   - Kurtosis (0.15): harmonic clarity
	//   - Flatness (0.10): tonal quality
	//   - Centroid (0.10): voice-range frequency
	//   - RMS (0.10): activity level
	return kurtosisScore*weightKurtosis +
		flatnessScore*weightFlatness +
		centroidScore*weightCentroid +
		consistencyScore*weightConsistency +
		rmsScore*weightRMS +
		voicingScore*weightVoicing +
		rolloffScore*weightRolloff +
		fluxScore*weightFlux
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

// roomToneScore calculates a 0-1 score indicating how likely an interval is room tone.
// Room tone has characteristic spectral behaviour:
// - Low SpectralFlux (stable, not changing)
// - Relatively quiet (low RMS)
// - More noise-like spectrum (higher flatness/entropy vs tonal speech)
//
// The score combines these factors with amplitude to identify room tone reliably.
func roomToneScore(interval IntervalSample, rmsP50, fluxP50 float64) float64 {
	// Amplitude component: quieter = more likely room tone
	// Score 1.0 if at or below median, decreasing above
	amplitudeScore := 1.0
	if interval.RMSLevel > rmsP50 {
		// Linear decay: 0dB above = 1.0, roomToneAmplitudeDecayDB above = 0.0
		amplitudeScore = 1.0 - (interval.RMSLevel-rmsP50)/roomToneAmplitudeDecayDB
		if amplitudeScore < 0 {
			amplitudeScore = 0
		}
	}

	// Flux component: room tone is stable (low flux)
	// Score 1.0 if at or below median, decreasing above
	fluxScore := 1.0
	if fluxP50 > 0 && interval.SpectralFlux > fluxP50 {
		// Exponential decay based on ratio above median
		ratio := interval.SpectralFlux / fluxP50
		if ratio > 1 {
			// ratio 1 = 1.0, ratio 2 = 0.5, ratio 4 = 0.25
			fluxScore = 1.0 / ratio
		}
	}

	// Combine scores: both must be reasonable for a good room tone score
	return roomToneAmplitudeWeight*amplitudeScore + roomToneFluxWeight*fluxScore
}

// silenceMedians holds pre-computed median values for silence/room-tone detection.
// Avoids redundant O(n log n) sorts when the same interval data is used by
// multiple detection functions.
type silenceMedians struct {
	rmsP50  float64
	fluxP50 float64
}

// computeSilenceMedians calculates RMS and spectral flux medians from the
// interval slice used for silence/room-tone detection.
func computeSilenceMedians(searchIntervals []IntervalSample) silenceMedians {
	if len(searchIntervals) == 0 {
		return silenceMedians{}
	}
	rmsLevels := make([]float64, len(searchIntervals))
	fluxValues := make([]float64, len(searchIntervals))
	for i, interval := range searchIntervals {
		rmsLevels[i] = interval.RMSLevel
		fluxValues[i] = interval.SpectralFlux
	}
	sort.Float64s(rmsLevels)
	sort.Float64s(fluxValues)

	return silenceMedians{
		rmsP50:  rmsLevels[len(rmsLevels)/2],
		fluxP50: fluxValues[len(fluxValues)/2],
	}
}

// estimateNoiseFloorAndThreshold analyses interval data to estimate noise floor and silence threshold.
// Returns (noiseFloor, silenceThreshold, ok). If ok is false, fallback values should be used.
//
// Uses spectral analysis to identify room tone by its characteristic stability and quietness:
// 1. Room tone is quieter than speech (but may overlap with quiet speech)
// 2. Room tone has low spectral flux (stable, unchanging)
// 3. Room tone has consistent spectral characteristics
//
// The noise floor is the max RMS of high-confidence room tone intervals.
// The silence threshold adds headroom to the noise floor for detection margin.
func estimateNoiseFloorAndThreshold(intervals []IntervalSample, medians silenceMedians) (noiseFloor, silenceThreshold float64, ok bool) {
	if len(intervals) < silenceThresholdMinIntervals {
		return 0, 0, false
	}

	// Use pre-computed medians for scoring reference
	rmsP50 := medians.rmsP50
	fluxP50 := medians.fluxP50

	// Score each interval for room tone likelihood
	type scoredInterval struct {
		idx   int
		rms   float64
		score float64
	}
	scored := make([]scoredInterval, len(intervals))
	for i, interval := range intervals {
		scored[i] = scoredInterval{
			idx:   i,
			rms:   interval.RMSLevel,
			score: roomToneScore(interval, rmsP50, fluxP50),
		}
	}

	// Sort by score descending to find high-confidence room tone intervals
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Take the top 20% of scored intervals as room tone candidates
	// (or at least roomToneCandidateMinCount intervals for statistical relevance)
	candidateCount := len(scored) / roomToneCandidatePercent
	if candidateCount < roomToneCandidateMinCount {
		candidateCount = roomToneCandidateMinCount
	}
	if candidateCount > len(scored) {
		candidateCount = len(scored)
	}

	// Noise floor is the maximum RMS among high-confidence room tone intervals
	maxRoomToneRMS := -120.0
	for i := 0; i < candidateCount; i++ {
		if scored[i].rms > maxRoomToneRMS {
			maxRoomToneRMS = scored[i].rms
		}
	}

	return maxRoomToneRMS, maxRoomToneRMS + silenceThresholdHeadroomDB, true
}

// findSilenceCandidatesFromIntervals identifies silence regions from interval samples.
// Uses a room tone score approach that considers both amplitude and spectral stability.
//
// Detection algorithm:
// 1. Use pre-computed reference values (medians) for room tone scoring
// 2. Score each interval for "room tone likelihood"
// 3. Use a score threshold (0.5) to identify room tone intervals
// 4. Find consecutive runs that meet minimum duration (8 seconds)
//
// The RMS threshold parameter is used as a hard ceiling - intervals above it
// cannot be silence regardless of spectral characteristics.
func findSilenceCandidatesFromIntervals(intervals []IntervalSample, threshold float64, medians silenceMedians) []SilenceRegion {
	if len(intervals) < minimumSilenceIntervals {
		return nil
	}

	// Use pre-computed medians for room tone scoring
	rmsP50 := medians.rmsP50
	fluxP50 := medians.fluxP50

	var candidates []SilenceRegion
	var silenceStart time.Duration
	var silentIntervalCount int
	var interruptionCount int // consecutive intervals below score threshold
	inSilence := false

	for i := 0; i < len(intervals); i++ {
		interval := intervals[i]

		// Hard ceiling: anything above threshold cannot be room tone
		// Plus check room tone score for more nuanced detection
		score := roomToneScore(interval, rmsP50, fluxP50)
		isSilent := interval.RMSLevel <= threshold && score >= roomToneScoreThreshold

		if isSilent {
			if !inSilence {
				// Start of potential silence region
				silenceStart = interval.Timestamp
				silentIntervalCount = 1
				interruptionCount = 0
				inSilence = true
			} else {
				silentIntervalCount++
				interruptionCount = 0 // reset interruption counter on silent interval
			}
		} else if inSilence {
			// Not room tone - count as interruption
			interruptionCount++

			if interruptionCount > interruptionToleranceIntervals {
				// Too many consecutive interruptions - end silence region
				// Calculate end time from last silent interval (before interruptions started)
				lastSilentIdx := i - interruptionCount
				if silentIntervalCount >= minimumSilenceIntervals && lastSilentIdx >= 0 && lastSilentIdx < len(intervals) {
					endTime := intervals[lastSilentIdx].Timestamp + 250*time.Millisecond
					duration := endTime - silenceStart

					candidates = append(candidates, SilenceRegion{
						Start:    silenceStart,
						End:      endTime,
						Duration: duration,
					})
				}
				inSilence = false
				silentIntervalCount = 0
				interruptionCount = 0
			}
			// else: within tolerance, continue silence region
		}
	}

	// Handle silence that extends to the end of the recording
	if inSilence && silentIntervalCount >= minimumSilenceIntervals {
		// Exclude trailing non-silent interruptions, same as the mid-loop case
		lastSilentIdx := len(intervals) - 1 - interruptionCount
		if lastSilentIdx < 0 {
			lastSilentIdx = 0
		}
		endTime := intervals[lastSilentIdx].Timestamp + 250*time.Millisecond
		duration := endTime - silenceStart

		candidates = append(candidates, SilenceRegion{
			Start:    silenceStart,
			End:      endTime,
			Duration: duration,
		})
	}

	return candidates
}

// Threshold bounds for adaptive silence detection
const (
	// silenceFallbackHeadroom is added to the noise floor to get the silencedetect threshold.
	// A region is considered "silence" if it's within this headroom of the noise floor.
	// Higher values detect more silence (including quieter room tone) but may include crosstalk.
	silenceFallbackHeadroom = 6.0 // dB

	// silenceMinThreshold prevents silencedetect from being too sensitive in very quiet recordings.
	// Even professional recordings rarely have silence below -70 dBFS.
	silenceMinThreshold = -70.0

	// silenceMaxThreshold prevents silencedetect from detecting loud sections as silence.
	// If the estimated threshold is above this, something is wrong with the recording.
	silenceMaxThreshold = -35.0
)

// calculateAdaptiveSilenceThreshold computes a bounded silence threshold from a noise floor estimate.
// Returns a threshold that's slightly above the noise floor to detect quiet room tone as silence.
// This is used as a fallback when interval-based estimation has insufficient data.
func calculateAdaptiveSilenceThreshold(noiseFloor float64) float64 {
	// Silence threshold = noise floor + headroom
	// This allows silencedetect to find regions that are at or slightly above the ambient noise
	threshold := noiseFloor + silenceFallbackHeadroom

	// Apply bounds to prevent extreme values
	if threshold < silenceMinThreshold {
		threshold = silenceMinThreshold
	}
	if threshold > silenceMaxThreshold {
		threshold = silenceMaxThreshold
	}

	return threshold
}

// Silence region scoring for measurement reference extraction.
//
// The "noise profile" is no longer used for afftdn training (anlmdn is self-adapting).
// Instead, these measurements serve as:
// 1. Reference baseline for adaptive filter tuning (gate, compand, highpass)
// 2. Comparative measurement point (same region re-measured in later passes)
//
// Scoring weights are tuned to prefer regions that are:
//   - Quiet (amplitude) - accurate noise floor measurement
//   - Noise-like (spectral) - representative of room ambience, not crosstalk
//     (See docs/Spectral-Metrics-Reference.md for metric interpretations)
//   - Stable (variance) - intentionally recorded, not accidental gaps
//   - Duration 8-18s - sufficient data without absorbing content changes
const (
	// Duration thresholds (Task 5: adjusted constraints)
	minimumSilenceDuration = 8 * time.Second  // Minimum 8s (up from 2s) to avoid inter-word gaps
	idealDurationMin       = 8 * time.Second  // Ideal range lower bound
	idealDurationMax       = 18 * time.Second // Ideal range upper bound

	// Long region segmentation: break up long silence regions to find cleanest subsection
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
	// silenceCrestFactorMax is the maximum acceptable crest factor for silence candidates.
	// Crest factor > 25 dB indicates physical transients (bumps, interference) contaminating
	// the silence region, making noise floor measurements unreliable.
	// Normal room tone: 5-20 dB; contaminated: 25-45 dB.
	silenceCrestFactorMax = 25.0 // dB - hard rejection above this

	// digitalSilenceRMSThreshold is the maximum RMS level (dBFS) considered digital silence.
	// Voice-activated recording platforms (Riverside, Zencastr) clamp non-speech regions
	// to all-zero samples, pinning RMS at -120.0 dBFS (the FFmpeg astats measurement floor).
	// Genuine room tone never drops below ~-95 dBFS due to preamp thermal noise.
	digitalSilenceRMSThreshold = -115.0 // dBFS - 5 dB margin above measurement floor

	// voiceActivatedDigitalSilenceThreshold is the fraction of silence candidates
	// that must be digital silence to classify a recording as voice-activated.
	// 95% threshold provides a 10-point margin above the highest known normal recording (Marius: 85%).
	voiceActivatedDigitalSilenceThreshold = 0.95

	// Crest factor penalty thresholds for silence candidates.
	// Context: These apply to SILENCE CANDIDATES (RMS < -70 dBFS).
	// In silence regions, even modest transients produce extreme crest factors:
	//   Peak -30 dBFS, RMS -74 dBFS → Crest 44 dB (expected, not pathological)
	crestFactorSoftThreshold = 30.0  // dB - start mild penalty
	crestFactorHardThreshold = 35.0  // dB - require peak check
	peakDangerZoneLow        = -40.0 // dBFS
	peakDangerZoneHigh       = -25.0 // dBFS
	rmsSilenceThreshold      = -70.0 // dBFS

	// Scoring weights (must sum to 1.0)
	stabilityScoreWeight = 0.25
	amplitudeScoreWeight = 0.30 // was 0.40
	spectralScoreWeight  = 0.35 // was 0.50
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

// segmentLongSilenceRegion breaks a long silence region into overlapping segments.
// This allows finding the cleanest subsection within a long quiet period, as intentional
// room tone may be preceded or followed by other quiet content (breathing, quiet lead-up).
//
// Returns the original region in a slice if it's shorter than the segmentation threshold,
// otherwise returns a slice of overlapping segments covering the original region.
func segmentLongSilenceRegion(region SilenceRegion) []SilenceRegion {
	// Don't segment short regions
	if region.Duration <= segmentationThreshold {
		return []SilenceRegion{region}
	}

	var segments []SilenceRegion
	stride := segmentDuration - segmentOverlap // How far to advance each segment
	endTime := region.Start + region.Duration

	for segStart := region.Start; segStart+segmentDuration <= endTime; segStart += stride {
		segments = append(segments, SilenceRegion{
			Start:    segStart,
			End:      segStart + segmentDuration,
			Duration: segmentDuration,
		})
	}

	// If no segments were created (shouldn't happen), return the original
	if len(segments) == 0 {
		return []SilenceRegion{region}
	}

	return segments
}

// findBestSilenceRegion finds the best silence region for noise profile extraction.
// Evaluates all candidates regardless of temporal position. Uses a two-pass approach:
// first scores all candidates using multi-metric analysis (amplitude, spectral
// characteristics, stability, duration), then elects the earliest candidate whose
// score is within selectionTolerance of the maximum.
//
// Uses pre-collected interval data for measurements - no file re-reading required.
// Returns nil if no suitable region is found.
// findBestSilenceRegionResult contains the selected region and all evaluated candidates
type findBestSilenceRegionResult struct {
	BestRegion *SilenceRegion
	Candidates []SilenceCandidateMetrics
}

// detectVoiceActivated determines whether a recording was made with a voice-activated
// platform by examining the fraction of silence candidates flagged as digital silence.
// Returns true when candidates exist and >= 95% have "digital silence" in their TransientWarning.
func detectVoiceActivated(candidates []SilenceCandidateMetrics) bool {
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

func findBestSilenceRegion(regions []SilenceRegion, intervals []IntervalSample, totalDuration float64) *findBestSilenceRegionResult {
	result := &findBestSilenceRegionResult{}

	if len(regions) == 0 {
		return result
	}

	// Filter to candidates meeting minimum duration, then segment long regions.
	// Long silence regions are broken into overlapping segments to find the cleanest subsection.
	// This helps when intentional room tone is embedded within a longer quiet period.
	var candidates []SilenceRegion
	for _, r := range regions {
		// Must meet minimum duration
		if r.Duration < minimumSilenceDuration {
			continue
		}
		// Segment long regions to find cleanest subsection
		segments := segmentLongSilenceRegion(r)
		candidates = append(candidates, segments...)
	}

	if len(candidates) == 0 {
		return result
	}

	// ── Pass 1: Score all candidates ──────────────────────────────────────
	// For candidates longer than goldenWindowDuration, refine to the cleanest
	// sub-region BEFORE scoring. This trims boundary transients that would
	// otherwise inflate crest factor and cause false rejection.
	for i := range candidates {
		candidate := &candidates[i]

		// Measure spectral characteristics from interval data (no file re-read)
		metrics := measureSilenceCandidateFromIntervals(*candidate, intervals)
		if metrics == nil {
			// No intervals in range - skip this candidate
			continue
		}

		// Pre-scoring refinement: trim boundary transients before crest factor evaluation
		if candidate.Duration > goldenWindowDuration {
			refined := refineToGoldenSubregion(candidate, intervals)
			wasRefined := refined.Start != candidate.Start || refined.Duration != candidate.Duration
			if wasRefined {
				refinedMetrics := measureSilenceCandidateFromIntervals(*refined, intervals)
				if refinedMetrics != nil {
					refinedMetrics.WasRefined = true
					refinedMetrics.OriginalStart = candidate.Start
					refinedMetrics.OriginalDuration = candidate.Duration
					metrics = refinedMetrics
				}
			}
		}

		// Score the candidate based on spectral characteristics
		// Candidates were already validated by the data-driven interval threshold
		score := scoreSilenceCandidate(metrics)
		metrics.Score = score

		// Store candidate metrics for reporting
		result.Candidates = append(result.Candidates, *metrics)
	}

	// ── Pass 2: Elect earliest candidate within tolerance of max score ────
	if len(result.Candidates) > 0 {
		// Find maximum score
		maxScore := 0.0
		for _, c := range result.Candidates {
			if c.Score > maxScore {
				maxScore = c.Score
			}
		}

		// Select earliest candidate within selectionTolerance of max
		// Uses Region field from metrics to avoid index correspondence issues
		// (result.Candidates may have fewer entries than candidates if some
		// returned nil from measureSilenceCandidateFromIntervals)
		for _, c := range result.Candidates {
			if c.Score >= maxScore-selectionTolerance && c.Score >= minAcceptableScore {
				region := c.Region
				result.BestRegion = &SilenceRegion{
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

// scoreSilenceCandidate computes a composite score for a silence region candidate.
// Higher scores indicate better candidates for noise profiling.
// Returns 0.0 for candidates that should be rejected (e.g., crosstalk detected).
func scoreSilenceCandidate(m *SilenceCandidateMetrics) float64 {
	if m == nil {
		return 0.0
	}

	// Reject digital silence: voice-activated platforms (Riverside, Zencastr) clamp
	// non-speech regions to all-zero samples, pinning RMS at -120.0 dBFS. These regions
	// contain no room ambience and are useless for noise reduction profiling.
	if m.RMSLevel <= digitalSilenceRMSThreshold {
		debugLog("scoreSilenceCandidate: REJECTING candidate at %.3fs - RMS %.1f dBFS at or below %.1f dBFS threshold (digital silence)",
			m.Region.Start.Seconds(), m.RMSLevel, digitalSilenceRMSThreshold)
		m.TransientWarning = fmt.Sprintf(
			"rejected: RMS %.1f dBFS at or below %.1f dBFS threshold (digital silence from voice-activated recording)",
			m.RMSLevel, digitalSilenceRMSThreshold,
		)
		return 0.0
	}

	// Check for crosstalk rejection: voice-range centroid + peaked/impulsive characteristics
	isCrosstalk := isLikelyCrosstalk(m)
	debugLog("scoreSilenceCandidate: start=%.3fs, CrestFactor=%.2f dB, isCrosstalk=%v",
		m.Region.Start.Seconds(), m.CrestFactor, isCrosstalk)
	if isCrosstalk {
		debugLog("scoreSilenceCandidate: REJECTING candidate at %.3fs (returning score=0.0)", m.Region.Start.Seconds())
		m.TransientWarning = fmt.Sprintf(
			"rejected: crosstalk detected (crest %.1f dB, centroid %.0f Hz)",
			m.CrestFactor, m.Spectral.Centroid,
		)
		return 0.0 // Reject this candidate
	}

	// Hard rejection: extreme crest factor indicates physical transients (bumps,
	// interference) contaminating the silence region. Unlike the crosstalk check
	// (45 dB), this catches moderate contamination (25-45 dB range).
	if m.CrestFactor > silenceCrestFactorMax {
		debugLog("scoreSilenceCandidate: REJECTING candidate at %.3fs - crest factor %.1f dB exceeds %.1f dB threshold",
			m.Region.Start.Seconds(), m.CrestFactor, silenceCrestFactorMax)
		m.TransientWarning = fmt.Sprintf(
			"rejected: crest factor %.1f dB exceeds %.1f dB threshold (transient contamination)",
			m.CrestFactor, silenceCrestFactorMax,
		)
		return 0.0
	}

	// Calculate individual component scores (all normalised to 0-1 range)
	ampScore := calculateAmplitudeScore(m.RMSLevel)
	specScore := calculateSpectralScore(m.Spectral.Centroid, m.Spectral.Flatness, m.Spectral.Kurtosis)
	durScore := calculateDurationScore(m.Region.Duration)

	// Weighted combination (base score)
	baseScore := ampScore*amplitudeScoreWeight +
		specScore*spectralScoreWeight +
		durScore*durationScoreWeight +
		m.StabilityScore*stabilityScoreWeight

	score := baseScore

	// Apply crest factor penalty for transient contamination
	score = applyCrestFactorPenalty(score, m.CrestFactor, m.PeakLevel, m.RMSLevel)

	// Generate warning when danger zone signature is detected
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
		return 0.5 // Insufficient data, neutral score
	}

	// Calculate variance of RMS levels across intervals
	var rmsSum, rmsSquaredSum float64
	for _, iv := range intervals {
		rmsSum += iv.RMSLevel
		rmsSquaredSum += iv.RMSLevel * iv.RMSLevel
	}
	n := float64(len(intervals))
	rmsMean := rmsSum / n
	rmsVariance := (rmsSquaredSum / n) - (rmsMean * rmsMean)

	// Calculate average spectral flux (already a stability indicator)
	var fluxSum float64
	for _, iv := range intervals {
		fluxSum += iv.SpectralFlux
	}
	avgFlux := fluxSum / n

	// Stability score: low variance + low flux = high stability
	//
	// RMS variance: 0 dB² (perfect) to 9 dB² (3 dB std dev, poor)
	// Flux: 0 (perfect) to 0.02 (stability threshold)
	rmsStabilityScore := clamp(1.0-(rmsVariance/9.0), 0.0, 1.0)
	fluxStabilityScore := clamp(1.0-(avgFlux/0.02), 0.0, 1.0)

	// Combine: RMS variance more important (direct amplitude stability)
	return rmsStabilityScore*0.6 + fluxStabilityScore*0.4
}

// isLikelyCrosstalk detects if a silence candidate is likely crosstalk (leaked voice).
// Returns true if centroid is in voice range AND has peaked/impulsive characteristics,
// OR if the crest factor indicates severe transient contamination (centroid-independent).
func isLikelyCrosstalk(m *SilenceCandidateMetrics) bool {
	// Peak-RMS gap check (centroid-independent)
	// A 45 dB gap in silence indicates transient contamination regardless of spectral content.
	// Normal room tone: 20-30 dB gap (equipment transients, HVAC clicks)
	// Crosstalk: 40-60 dB gap (speech peaks in otherwise quiet track)
	crestExceedsThreshold := m.CrestFactor > crosstalkPeakRMSGap
	debugLog("isLikelyCrosstalk: CrestFactor=%.2f dB, threshold=%.2f dB, exceeds=%v",
		m.CrestFactor, crosstalkPeakRMSGap, crestExceedsThreshold)
	if crestExceedsThreshold {
		debugLog("isLikelyCrosstalk: REJECTING candidate due to crest factor %.2f dB > %.2f dB threshold",
			m.CrestFactor, crosstalkPeakRMSGap)
		return true
	}

	// Check if centroid is in voice frequency range
	inVoiceRange := m.Spectral.Centroid >= voiceCentroidMin && m.Spectral.Centroid <= voiceCentroidMax

	if !inVoiceRange {
		return false // Not in voice range, unlikely to be crosstalk
	}

	// Voice range + peaked harmonics (high kurtosis) = likely speech
	if m.Spectral.Kurtosis > crosstalkKurtosisThreshold {
		return true
	}

	// Voice range + impulsive transients (high crest factor) = likely speech
	if m.CrestFactor > crosstalkCrestFactorThreshold {
		return true
	}

	return false
}

// calculateAmplitudeScore normalises RMS level to a 0-1 score.
// Lower RMS (quieter) = higher score.
// Range: -80 dBFS (best) to -40 dBFS (worst)
func calculateAmplitudeScore(rmsLevel float64) float64 {
	// Clamp to expected range
	if rmsLevel < -80.0 {
		rmsLevel = -80.0
	}
	if rmsLevel > -40.0 {
		rmsLevel = -40.0
	}

	// Normalise: -80 → 1.0, -40 → 0.0
	return (rmsLevel - (-40.0)) / (-80.0 - (-40.0))
}

// calculateSpectralScore combines spectral metrics into a 0-1 score.
// Rewards: high flatness (noise-like), low kurtosis, centroid outside voice range
func calculateSpectralScore(centroid, flatness, kurtosis float64) float64 {
	// Centroid score: 0 if in voice range (250-4500 Hz), 1 otherwise
	var centroidScore float64
	if centroid < voiceCentroidMin || centroid > voiceCentroidMax {
		centroidScore = 1.0
	} else {
		// Partial penalty based on how central to voice range
		voiceMid := (voiceCentroidMin + voiceCentroidMax) / 2
		voiceHalfWidth := (voiceCentroidMax - voiceCentroidMin) / 2
		distFromMid := math.Abs(centroid - voiceMid)
		centroidScore = distFromMid / voiceHalfWidth * 0.5 // Max 0.5 if in voice range
	}

	// Flatness score: higher = more noise-like = better (already 0-1)
	flatnessScore := flatness
	if flatnessScore > 1.0 {
		flatnessScore = 1.0
	}
	if flatnessScore < 0.0 {
		flatnessScore = 0.0
	}

	// Kurtosis score: lower = less peaked = better
	// Normalise: 0 → 1.0, 20+ → 0.0
	kurtosisScore := 1.0 - clamp(kurtosis/20.0, 0.0, 1.0)

	// Combine with weights from the spec
	return centroidScore*0.5 + flatnessScore*0.3 + kurtosisScore*0.2
}

// applyCrestFactorPenalty applies a two-stage penalty for transient contamination.
// Stage 1: Soft penalty for elevated crest factor (maintains ranking stability).
// Stage 2: Hard penalty when the "danger zone" signature is detected.
// See docs/SILENCE-DETECTION-PLAN.md for empirical derivation.
func applyCrestFactorPenalty(score, crestFactor, peak, rms float64) float64 {
	// Stage 1: Soft penalty for elevated crest factor
	if crestFactor > crestFactorSoftThreshold {
		softPenalty := math.Min(0.2, (crestFactor-crestFactorSoftThreshold)/50)
		score *= (1 - softPenalty)
	}

	// Stage 2: Hard penalty for danger zone signature
	// Catches transients loud enough to mask noise but not obviously speech/clipping
	if crestFactor > crestFactorHardThreshold &&
		peak > peakDangerZoneLow && peak < peakDangerZoneHigh &&
		rms < rmsSilenceThreshold {
		score *= 0.5 // 50% penalty
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
	sigmaSecs := 5.0 // Standard deviation for dropoff

	// Full score within ideal range
	if durSecs >= idealMinSecs && durSecs <= idealMaxSecs {
		return 1.0
	}

	// Gaussian dropoff below ideal range
	if durSecs < idealMinSecs {
		diff := durSecs - idealMinSecs
		return math.Exp(-0.5 * (diff / sigmaSecs) * (diff / sigmaSecs))
	}

	// Gaussian dropoff above ideal range
	diff := durSecs - idealMaxSecs
	return math.Exp(-0.5 * (diff / sigmaSecs) * (diff / sigmaSecs))
}

// speechScore calculates how speech-like an interval is.
// Returns 0.0-1.0 where higher = more likely to be speech.
// Inverts silence detection criteria: rewards amplitude, voice-range centroid, low entropy.
func speechScore(interval IntervalSample, rmsP50 float64) float64 {
	// Reject if too quiet (likely silence/room tone)
	if interval.RMSLevel < speechRMSMinimum {
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
	if interval.SpectralCentroid >= speechCentroidMin && interval.SpectralCentroid <= speechCentroidMax {
		// In voice range - score based on how central
		voiceMid := (speechCentroidMin + speechCentroidMax) / 2
		voiceHalfWidth := (speechCentroidMax - speechCentroidMin) / 2
		distFromMid := math.Abs(interval.SpectralCentroid - voiceMid)
		centroidScore = 1.0 - (distFromMid / voiceHalfWidth * 0.5)
	}

	// Entropy score: lower entropy = more structured = more speech-like
	entropyScore := 0.0
	if interval.SpectralEntropy < speechEntropyMax {
		entropyScore = 1.0 - (interval.SpectralEntropy / speechEntropyMax)
	}

	// Weighted combination: amplitude most important, then centroid, then entropy
	return ampScore*0.5 + centroidScore*0.3 + entropyScore*0.2
}

// findSpeechCandidatesFromIntervals identifies speech regions from interval samples.
// Only searches after silenceEnd to ensure speech follows the elected silence candidate.
// Uses a speech score approach that rewards amplitude, voice-range centroid, and low entropy.
//
// Detection algorithm:
// 1. Start searching after silenceEnd + buffer
// 2. Calculate reference values (medians) for speech scoring
// 3. Score each interval for "speech likelihood"
// 4. Find consecutive runs that meet minimum duration (30 seconds)
// 5. Allow brief interruptions (2s) for natural pauses
func findSpeechCandidatesFromIntervals(intervals []IntervalSample, silenceEnd time.Duration) []SpeechRegion {
	if len(intervals) < minimumSpeechIntervals {
		return nil
	}

	// Find start index: after silence end + buffer
	searchStart := silenceEnd + speechSearchStartBuffer
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
		return nil // Not enough intervals after silence
	}

	searchIntervals := intervals[startIdx:]

	// Calculate medians for speech scoring
	rmsLevels := make([]float64, len(searchIntervals))
	for i, interval := range searchIntervals {
		rmsLevels[i] = interval.RMSLevel
	}
	sort.Float64s(rmsLevels)

	rmsP50 := rmsLevels[len(rmsLevels)/2]

	// Speech score threshold (lower than silence since speech varies more)
	const speechScoreThreshold = 0.4

	var candidates []SpeechRegion
	var speechStart time.Duration
	var speechIntervalCount int
	var interruptionCount int
	inSpeech := false

	for i := 0; i < len(searchIntervals); i++ {
		interval := searchIntervals[i]
		score := speechScore(interval, rmsP50)
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

			if interruptionCount > speechInterruptionToleranceIntervals {
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

// measureSpeechCandidateFromIntervals computes metrics for a speech region using pre-collected interval data.
// This avoids re-reading the audio file - all measurements come from Pass 1's interval samples.
// Returns nil if no intervals fall within the region.
func measureSpeechCandidateFromIntervals(region SpeechRegion, intervals []IntervalSample) *SpeechCandidateMetrics {
	// Extract intervals within the candidate region
	regionIntervals := getIntervalsInRange(intervals, region.Start, region.End)
	if len(regionIntervals) == 0 {
		return nil
	}

	// Accumulate metrics for averaging (sums) and extremes (max)
	var rmsSum float64
	var peakMax, truePeakMax, samplePeakMax float64 = -120.0, -120.0, -120.0
	var spectralSum SpectralMetrics
	var momentarySum, shortTermSum float64

	for _, interval := range regionIntervals {
		rmsSum += interval.RMSLevel
		if interval.PeakLevel > peakMax {
			peakMax = interval.PeakLevel
		}

		spectralSum.add(interval.spectralFields())

		momentarySum += interval.MomentaryLUFS
		shortTermSum += interval.ShortTermLUFS
		if interval.TruePeak > truePeakMax {
			truePeakMax = interval.TruePeak
		}
		if interval.SamplePeak > samplePeakMax {
			samplePeakMax = interval.SamplePeak
		}
	}

	n := float64(len(regionIntervals))
	avgRMS := rmsSum / n
	avgSpectral := spectralSum.average(n)

	// Calculate voicing density for stability assessment
	voicedCount := 0
	for _, interval := range regionIntervals {
		if interval.SpectralKurtosis > voicedKurtosisThreshold {
			voicedCount++
		}
	}
	voicingDensity := float64(voicedCount) / n

	return &SpeechCandidateMetrics{
		Region:      region,
		RMSLevel:    avgRMS,
		PeakLevel:   peakMax,
		CrestFactor: peakMax - avgRMS,
		Spectral:    avgSpectral,

		MomentaryLUFS: momentarySum / n,
		ShortTermLUFS: shortTermSum / n,
		TruePeak:      truePeakMax,
		SamplePeak:    samplePeakMax,

		// Stability metrics
		VoicingDensity: voicingDensity,
	}
}

// findBestSpeechRegionResult contains the selected region and all evaluated candidates.
type findBestSpeechRegionResult struct {
	BestRegion *SpeechRegion
	Candidates []SpeechCandidateMetrics
}

// findBestSpeechRegion selects the best speech region for measurements.
// Strategy: prefer longest duration that meets quality threshold.
// Unlike silence (where earlier is better), speech benefits from longer samples.
// For long candidates (>60s), refines to the best 60s sub-region to avoid
// contaminating spectral metrics with pauses.
// The noiseProfile parameter enables SNR margin checking to penalise candidates
// too close to the noise floor (where spectral metrics would be unreliable).
func findBestSpeechRegion(regions []SpeechRegion, intervals []IntervalSample, noiseProfile *NoiseProfile) *findBestSpeechRegionResult {
	result := &findBestSpeechRegionResult{}

	if len(regions) == 0 {
		return result
	}

	var bestCandidate *SpeechRegion
	var bestDuration time.Duration

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
				debugLog("Speech candidate at %.1fs has low SNR margin: %.1f dB < %.1f dB minimum",
					candidate.Start.Seconds(), snrMargin, minSNRMargin)
				// Apply penalty factor rather than rejecting outright
				// This allows selection if no better candidates exist
				snrPenalty := snrMargin / minSNRMargin // 0.0 to 1.0
				score *= clamp(snrPenalty, 0.1, 1.0)
				metrics.Score = score
			}
		} else {
			debugLog("SNR margin check skipped: no noise profile available")
		}

		// Store for reporting
		result.Candidates = append(result.Candidates, *metrics)

		// Selection: longest candidate above minimum quality
		const minAcceptableSpeechScore = 0.3
		if score >= minAcceptableSpeechScore && candidate.Duration > bestDuration {
			bestCandidate = candidate
			bestDuration = candidate.Duration
		}
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
