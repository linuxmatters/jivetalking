package processor

import (
	"cmp"
	"fmt"
	"slices"
	"time"
)

// Silence detection constants for interval-based analysis
const (
	// minimumSilenceIntervals is the minimum number of consecutive silent intervals
	// for a region to be considered a valid room tone candidate.
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
	// within a room tone region without breaking it. 3 intervals = 750ms tolerance.
	interruptionToleranceIntervals = 3

	// roomToneScoreThreshold is the minimum score (0-1) for an interval to be considered room tone.
	roomToneScoreThreshold = 0.5

	// Golden sub-region refinement constants
	// After selecting the best room tone candidate, refine to the cleanest sub-window
	// to isolate optimal noise profile (avoids pre-intentional quiet contamination).
	goldenWindowDuration = 10 * time.Second       // Target duration for refined region
	goldenWindowMinimum  = 8 * time.Second        // Minimum acceptable refined duration
	goldenIntervalSize   = 250 * time.Millisecond // Must match interval sampling (intervalDuration)
)

// roomToneScore calculates a 0-1 score indicating how likely an interval is room tone.
// Room tone is quiet and spectrally stable, so the score combines two cues:
//   - Amplitude (weight roomToneAmplitudeWeight): quieter than the RMS median = more likely room tone.
//   - Spectral flux (weight roomToneFluxWeight): lower than the flux median = stable, not changing.
//
// Only amplitude and flux feed this per-interval likelihood; the richer spectral
// metrics (flatness, entropy, centroid, kurtosis) enter later, at candidate
// scoring (calculateSpectralScore, isLikelyCrosstalk).
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
	if fluxP50 > 0 && interval.Spectral.Flux > fluxP50 {
		// Exponential decay based on ratio above median
		ratio := interval.Spectral.Flux / fluxP50
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
		fluxValues[i] = interval.Spectral.Flux
	}
	slices.Sort(rmsLevels)
	slices.Sort(fluxValues)

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
	slices.SortFunc(scored, func(a, b scoredInterval) int {
		return cmp.Compare(b.score, a.score)
	})

	// Take the top 20% of scored intervals as room tone candidates
	// (or at least roomToneCandidateMinCount intervals for statistical relevance)
	candidateCount := len(scored) / roomToneCandidatePercent
	candidateCount = max(candidateCount, roomToneCandidateMinCount)
	candidateCount = min(candidateCount, len(scored))

	// Noise floor is the maximum RMS among high-confidence room tone intervals
	maxRoomToneRMS := -120.0
	for i := 0; i < candidateCount; i++ {
		if scored[i].rms > maxRoomToneRMS {
			maxRoomToneRMS = scored[i].rms
		}
	}

	return maxRoomToneRMS, maxRoomToneRMS + silenceThresholdHeadroomDB, true
}

// findRoomToneCandidatesFromIntervals identifies room tone regions from interval samples.
// Uses a room tone score approach that considers both amplitude and spectral stability.
//
// Detection algorithm:
// 1. Use pre-computed reference values (medians) for room tone scoring
// 2. Score each interval for "room tone likelihood"
// 3. Use a score threshold (0.5) to identify room tone intervals
// 4. Find consecutive runs that meet minimum duration (8 seconds)
//
// The RMS threshold parameter is used as a hard ceiling - intervals above it
// cannot be room tone regardless of spectral characteristics.
func findRoomToneCandidatesFromIntervals(intervals []IntervalSample, threshold float64, medians silenceMedians) []RoomToneRegion {
	if len(intervals) < minimumSilenceIntervals {
		return nil
	}

	// Use pre-computed medians for room tone scoring
	rmsP50 := medians.rmsP50
	fluxP50 := medians.fluxP50

	var candidates []RoomToneRegion
	var regionStart time.Duration
	var roomToneIntervalCount int
	var interruptionCount int // consecutive intervals below score threshold
	inRoomTone := false

	for i := range len(intervals) {
		interval := intervals[i]

		// Hard ceiling: anything above threshold cannot be room tone
		// Plus check room tone score for more nuanced detection
		score := roomToneScore(interval, rmsP50, fluxP50)
		isRoomTone := interval.RMSLevel <= threshold && score >= roomToneScoreThreshold

		if isRoomTone {
			if !inRoomTone {
				// Start of potential room tone region
				regionStart = interval.Timestamp
				roomToneIntervalCount = 1
				interruptionCount = 0
				inRoomTone = true
			} else {
				roomToneIntervalCount++
				interruptionCount = 0 // reset interruption counter on room tone interval
			}
		} else if inRoomTone {
			// Not room tone - count as interruption
			interruptionCount++

			if interruptionCount > interruptionToleranceIntervals {
				// Too many consecutive interruptions - end room tone region
				// Calculate end time from last room tone interval (before interruptions started)
				lastRoomToneIdx := i - interruptionCount
				if roomToneIntervalCount >= minimumSilenceIntervals && lastRoomToneIdx >= 0 && lastRoomToneIdx < len(intervals) {
					endTime := intervals[lastRoomToneIdx].Timestamp + 250*time.Millisecond
					duration := endTime - regionStart

					candidates = append(candidates, RoomToneRegion{
						Start:    regionStart,
						End:      endTime,
						Duration: duration,
					})
				}
				inRoomTone = false
				roomToneIntervalCount = 0
				interruptionCount = 0
			}
			// else: within tolerance, continue room tone region
		}
	}

	// Handle room tone that extends to the end of the recording
	if inRoomTone && roomToneIntervalCount >= minimumSilenceIntervals {
		// Exclude trailing non-room-tone interruptions, same as the mid-loop case
		lastRoomToneIdx := len(intervals) - 1 - interruptionCount
		lastRoomToneIdx = max(lastRoomToneIdx, 0)
		endTime := intervals[lastRoomToneIdx].Timestamp + 250*time.Millisecond
		duration := endTime - regionStart

		candidates = append(candidates, RoomToneRegion{
			Start:    regionStart,
			End:      endTime,
			Duration: duration,
		})
	}

	return candidates
}

// Threshold bounds for adaptive room tone detection
const (
	// silenceFallbackHeadroom is added to the noise floor to get the room tone threshold.
	// A 250 ms interval is treated as room tone if its level is within this headroom of the noise floor.
	// Higher values capture more room tone (including quieter ambience) but may include crosstalk.
	silenceFallbackHeadroom = 6.0 // dB

	// silenceMinThreshold prevents the room tone threshold from being too sensitive in very quiet recordings.
	// Even professional recordings rarely have room tone below -70 dBFS.
	silenceMinThreshold = -70.0

	// silenceMaxThreshold prevents loud sections from being mistaken for room tone.
	// If the estimated threshold is above this, something is wrong with the recording.
	silenceMaxThreshold = -35.0
)

// calculateAdaptiveSilenceThreshold computes a bounded room tone threshold from a noise floor estimate.
// Returns a threshold slightly above the noise floor so quiet ambience scores as room tone during interval sampling.
// This is used as a fallback when interval-based estimation has insufficient data.
func calculateAdaptiveSilenceThreshold(noiseFloor float64) float64 {
	// Room tone threshold = noise floor + headroom
	// This admits 250 ms intervals at or slightly above the ambient noise into the room tone candidate set used for noise profiling.
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

// segmentLongRoomToneRegion breaks a long room tone region into overlapping segments.
// This allows finding the cleanest subsection within a long quiet period, as intentional
// room tone may be preceded or followed by other quiet content (breathing, quiet lead-up).
//
// Returns the original region in a slice if it's shorter than the segmentation threshold,
// otherwise returns a slice of overlapping segments covering the original region.
func segmentLongRoomToneRegion(region RoomToneRegion) []RoomToneRegion {
	// Don't segment short regions
	if region.Duration <= segmentationThreshold {
		return []RoomToneRegion{region}
	}

	var segments []RoomToneRegion
	stride := segmentDuration - segmentOverlap // How far to advance each segment
	endTime := region.Start + region.Duration

	for segStart := region.Start; segStart+segmentDuration <= endTime; segStart += stride {
		segments = append(segments, RoomToneRegion{
			Start:    segStart,
			End:      segStart + segmentDuration,
			Duration: segmentDuration,
		})
	}

	// If no segments were created (shouldn't happen), return the original
	if len(segments) == 0 {
		return []RoomToneRegion{region}
	}

	return segments
}

// extractNoiseProfileFromIntervals creates a NoiseProfile using pre-collected interval data.
// This avoids re-reading the audio file - all measurements come from Pass 1's interval samples.
// Returns nil if no intervals fall within the region.
func extractNoiseProfileFromIntervals(region *RoomToneRegion, intervals []IntervalSample) *NoiseProfile {
	if region == nil {
		return nil
	}

	regionIntervals := getIntervalsInRange(intervals, region.Start, region.Start+region.Duration)
	if len(regionIntervals) == 0 {
		return nil
	}

	var rmsSum, peakMax float64
	var entropySum, centroidSum, flatnessSum, kurtosisSum float64
	peakMax = -120.0

	for _, interval := range regionIntervals {
		rmsSum += interval.RMSLevel
		if interval.PeakLevel > peakMax {
			peakMax = interval.PeakLevel
		}
		entropySum += interval.Spectral.Entropy
		centroidSum += interval.Spectral.Centroid
		flatnessSum += interval.Spectral.Flatness
		kurtosisSum += interval.Spectral.Kurtosis
	}

	n := float64(len(regionIntervals))
	avgRMS := rmsSum / n

	profile := &NoiseProfile{
		Start:              region.Start,
		Duration:           region.Duration,
		MeasuredNoiseFloor: avgRMS,
		PeakLevel:          peakMax,
		CrestFactor:        peakMax - avgRMS,
		Entropy:            entropySum / n,
		SpectralCentroid:   centroidSum / n,
		SpectralFlatness:   flatnessSum / n,
		SpectralKurtosis:   kurtosisSum / n,
	}

	if region.Duration < idealDurationMin {
		profile.ExtractionWarning = fmt.Sprintf("using short room tone region (%.1fs) - ideally need >=%ds", region.Duration.Seconds(), int(idealDurationMin.Seconds()))
	} else if region.Duration > idealDurationMax {
		profile.ExtractionWarning = fmt.Sprintf("using long room tone region (%.1fs) - ideally <=%ds", region.Duration.Seconds(), int(idealDurationMax.Seconds()))
	}

	return profile
}
