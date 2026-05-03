package processor

import (
	"math"
	"sort"
	"time"
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
	peakMax, truePeakMax, samplePeakMax := -120.0, -120.0, -120.0
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
	peakMax, truePeakMax, samplePeakMax := -120.0, -120.0, -120.0
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
