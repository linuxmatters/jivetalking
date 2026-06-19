package processor

import (
	"cmp"
	"slices"
	"time"
)

// Noise-floor seed estimators and the golden-window refinement bounds.
//
// These run in buildInputMeasurements BEFORE the voice-activity detector: they
// produce the measured pre-scan floor (Noise.FloorPrescan) that anchors the
// detector's split clamp. The unified detector keeps only this seed path plus
// the shared golden-window bounds; it has no scored room-tone election.

// Golden sub-region refinement bounds, shared by the room-tone region picker
// (pickLowClusterRegion) and the shared sliding-window refinement
// (refineToSubregion). They bound the cleanest sub-window extracted from a long
// quiet run.
const (
	goldenWindowDuration = 10 * time.Second       // Target duration for refined region
	goldenWindowMinimum  = 8 * time.Second        // Minimum acceptable refined duration
	goldenIntervalSize   = 250 * time.Millisecond // Must match interval sampling (analysisIntervalHop)
)

// Seed-estimator constants for the pre-scan noise floor.
const (
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

	// floorSeedTopPercent is the percentage of top-scored intervals taken as the noise-floor seed set (20%).
	floorSeedTopPercent = 5 // divisor: len/5 = 20%

	// floorSeedMinCount is the minimum number of intervals in the noise-floor seed set.
	floorSeedMinCount = 8

	// silenceThresholdHeadroomDB is additional dB added to the detected room tone level for headroom.
	silenceThresholdHeadroomDB = 1.0
)

// Threshold bounds for the fallback adaptive silence threshold.
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

// roomToneScore calculates a 0-1 score indicating how likely an interval is room tone.
// Room tone is quiet and spectrally stable, so the score combines two cues:
//   - Amplitude (weight roomToneAmplitudeWeight): quieter than the level median = more likely room tone.
//   - Spectral flux (weight roomToneFluxWeight): lower than the flux median = stable, not changing.
//
// The amplitude cue reads the momentary-LUFS axis (interval.MomentaryLUFS), the
// same axis the VAD split, floor, and noise margin operate on, so the seed and the
// detector share one scale. It feeds the pre-scan noise-floor estimator
// (estimateNoiseFloorAndThreshold); only the amplitude and flux cues are used,
// the richer spectral metrics are not, since there is no scored room-tone election.
func roomToneScore(interval IntervalSample, levelP50, fluxP50 float64) float64 {
	// Amplitude component: quieter = more likely room tone
	// Score 1.0 if at or below median, decreasing above
	amplitudeScore := 1.0
	if interval.MomentaryLUFS > levelP50 {
		// Linear decay: 0dB above = 1.0, roomToneAmplitudeDecayDB above = 0.0
		amplitudeScore = 1.0 - (interval.MomentaryLUFS-levelP50)/roomToneAmplitudeDecayDB
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

// silenceMedians holds pre-computed median values for the noise-floor seed
// estimator. Avoids redundant O(n log n) sorts when the same interval data
// feeds multiple seed functions. levelP50 is the median on the momentary-LUFS
// axis, matching the axis the VAD split and floor operate on.
type silenceMedians struct {
	levelP50 float64
	fluxP50  float64
}

// computeSilenceMedians calculates the momentary-LUFS level and spectral flux
// medians from the interval slice used for the noise-floor seed estimate.
func computeSilenceMedians(searchIntervals []IntervalSample) silenceMedians {
	if len(searchIntervals) == 0 {
		return silenceMedians{}
	}
	levels := make([]float64, len(searchIntervals))
	fluxValues := make([]float64, len(searchIntervals))
	for i, interval := range searchIntervals {
		levels[i] = interval.MomentaryLUFS
		fluxValues[i] = interval.Spectral.Flux
	}
	slices.Sort(levels)
	slices.Sort(fluxValues)

	return silenceMedians{
		levelP50: levels[len(levels)/2],
		fluxP50:  fluxValues[len(fluxValues)/2],
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
// The level is read on the momentary-LUFS axis (the axis the VAD split, floor,
// and noise margin share), so the seeded floor and the detector measure one scale.
// The noise floor is the max level of high-confidence room tone intervals; the
// silence threshold adds headroom to it for detection margin.
//
// Floored intervals (momentary at or below vadLevelFloorDB, or non-finite) are
// excluded before the max so true digital silence between phrases on
// voice-activated captures does not seed a phantom -120 dB floor. When no real
// room-tone interval remains after exclusion, the estimator returns ok=false so
// the caller falls back rather than fabricating a level.
func estimateNoiseFloorAndThreshold(intervals []IntervalSample, medians silenceMedians) (noiseFloor, silenceThreshold float64, ok bool) {
	if len(intervals) < silenceThresholdMinIntervals {
		return 0, 0, false
	}

	// Use pre-computed medians for scoring reference
	levelP50 := medians.levelP50
	fluxP50 := medians.fluxP50

	// Score each interval for room tone likelihood
	type scoredInterval struct {
		idx   int
		level float64
		score float64
	}
	scored := make([]scoredInterval, len(intervals))
	for i, interval := range intervals {
		scored[i] = scoredInterval{
			idx:   i,
			level: interval.MomentaryLUFS,
			score: roomToneScore(interval, levelP50, fluxP50),
		}
	}

	// Sort by score descending to find high-confidence room tone intervals.
	// Break ties deterministically (slices.SortFunc is not stable): lower level
	// first, then original interval index, so the truncated candidate set and the
	// seed it yields are reproducible across runs.
	slices.SortFunc(scored, func(a, b scoredInterval) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		if c := cmp.Compare(a.level, b.level); c != 0 {
			return c
		}
		return cmp.Compare(a.idx, b.idx)
	})

	// Take the top 20% of scored intervals as the noise-floor seed set
	// (or at least floorSeedMinCount intervals for statistical relevance)
	candidateCount := len(scored) / floorSeedTopPercent
	candidateCount = max(candidateCount, floorSeedMinCount)
	candidateCount = min(candidateCount, len(scored))

	// Noise floor is the maximum level among high-confidence room tone intervals,
	// excluding floored (digital-silence / unmeasurable) intervals so a
	// voice-activated capture's true-silence gaps cannot seed a phantom floor.
	maxRoomToneLevel := -120.0
	seen := false
	for i := 0; i < candidateCount; i++ {
		level := scored[i].level
		if isFlooredLevel(level) {
			continue
		}
		if !seen || level > maxRoomToneLevel {
			maxRoomToneLevel = level
			seen = true
		}
	}

	// No real room-tone interval survived the floored-interval exclusion: do not
	// fabricate a level. Return ok=false so the caller uses its low fallback and
	// the downstream percentileFloor falls back to the momentary p10.
	if !seen {
		return 0, 0, false
	}

	return maxRoomToneLevel, maxRoomToneLevel + silenceThresholdHeadroomDB, true
}

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
