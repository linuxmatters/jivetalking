package processor

import (
	"fmt"
	"math"
	"slices"
	"time"
)

// analysisIntervalHop is the single per-interval sampling hop for Pass 1. It is
// the one owner of the interval duration: collectAnalysisFrames reads it to
// close each interval window, and the voice-activity detector reads it to
// convert its duration-expressed bounds into interval counts. The value is a
// measured choice; the hop-separation sweep (Phase 6.1) may revise it. Keeping
// it a single named value means that revision is a one-line change.
const analysisIntervalHop = 250 * time.Millisecond

// Voice-activity detector run-formation bounds, expressed as durations rather
// than interval counts so the hop is free to change. intervalsForDuration turns
// each into a count against the active hop.
const (
	// vadMinSpeechDuration is the minimum length of a contiguous speech run for
	// it to become a region (10 s, matching the historical 40-interval minimum
	// at the 250 ms hop).
	vadMinSpeechDuration = 10 * time.Second

	// vadGapToleranceFloor and vadGapToleranceCeiling clamp the data-derived
	// bridgeable-gap tolerance (2 s lower, 10 s upper), re-expressing the former
	// [8, 40]-interval clamp in time units.
	vadGapToleranceFloor   = 2 * time.Second
	vadGapToleranceCeiling = 10 * time.Second
)

// intervalsForDuration converts a duration to a count of interval hops, rounded
// to the nearest whole interval. A non-positive hop yields 0 (no division by
// zero). The detector uses this so every run-formation bound is derived from a
// duration divided by the active hop, never a baked-in interval count.
func intervalsForDuration(d, hop time.Duration) int {
	if hop <= 0 {
		return 0
	}
	return int((d + hop/2) / hop)
}

// levelAxis names the per-interval amplitude signal the detector splits on. It
// is the single named choice the validation gate (Phase 6.2) may flip, so the
// fallback is a one-line change.
type levelAxis int

const (
	// axisMomentaryLUFS is the primary axis: ebur128 momentary loudness. It is
	// steadier across a brief breath than 250 ms RMS and is the BS.1770
	// foreground-gate signal, already measured per interval.
	axisMomentaryLUFS levelAxis = iota
	// axisRMS is the fallback axis: per-interval RMS level.
	axisRMS
)

// vadLevelFloorDB is the dB level at or below which an interval is treated as
// floored (digital silence / unmeasurable) and excluded from the histogram and
// the level set. The interval finaliser pins both RMS and a silent momentary
// window near -120 dBFS; this margin sits just above that measurement floor.
const vadLevelFloorDB = -115.0

// intervalLevel returns the per-interval level on the selected axis.
func intervalLevel(s IntervalSample, axis levelAxis) float64 {
	switch axis {
	case axisRMS:
		return s.RMSLevel
	default:
		return s.MomentaryLUFS
	}
}

// histogram holds bin counts of per-interval levels on a fixed-width grid plus
// the observed level extent. binWidth and minLevel define the bin edges:
// bin i covers [minLevel + i*binWidth, minLevel + (i+1)*binWidth). count is the
// number of non-floored intervals; it equals the sum of bins.
type histogram struct {
	bins     []int
	binWidth float64
	minLevel float64 // lower edge of bin 0 (smallest non-floored level seen)
	maxLevel float64 // largest non-floored level seen
	count    int     // total non-floored intervals binned
}

// binCentre returns the level at the centre of bin i.
func (h histogram) binCentre(i int) float64 {
	return h.minLevel + (float64(i)+0.5)*h.binWidth
}

// buildLevelHistogram bins the per-interval levels on the chosen axis into
// fixed-width bins of binWidthDB. Floored intervals (level <= vadLevelFloorDB,
// or non-finite) are skipped consistently so digital silence does not invent a
// spurious low mode. Returns the zero histogram when no interval clears the
// floor or binWidthDB is non-positive.
func buildLevelHistogram(intervals []IntervalSample, axis levelAxis, binWidthDB float64) histogram {
	if binWidthDB <= 0 {
		return histogram{}
	}

	levels := make([]float64, 0, len(intervals))
	minLevel := math.Inf(1)
	maxLevel := math.Inf(-1)
	for _, iv := range intervals {
		level := intervalLevel(iv, axis)
		if math.IsInf(level, 0) || math.IsNaN(level) || level <= vadLevelFloorDB {
			continue
		}
		levels = append(levels, level)
		minLevel = min(minLevel, level)
		maxLevel = max(maxLevel, level)
	}

	if len(levels) == 0 {
		return histogram{}
	}

	// Number of bins spans [minLevel, maxLevel]; the +1 keeps maxLevel inside the
	// last bin rather than falling on an exclusive upper edge.
	binCount := int((maxLevel-minLevel)/binWidthDB) + 1
	h := histogram{
		bins:     make([]int, binCount),
		binWidth: binWidthDB,
		minLevel: minLevel,
		maxLevel: maxLevel,
	}
	for _, level := range levels {
		idx := int((level - minLevel) / binWidthDB)
		if idx >= binCount {
			idx = binCount - 1
		}
		h.bins[idx]++
		h.count++
	}

	return h
}

// vadLevels returns the sorted slice of non-floored per-interval levels on the
// chosen axis. Shared by the percentile floor and the p75 split clamp so both
// read the same axis the histogram split was computed on.
func vadLevels(intervals []IntervalSample, axis levelAxis) []float64 {
	levels := make([]float64, 0, len(intervals))
	for _, iv := range intervals {
		level := intervalLevel(iv, axis)
		if math.IsInf(level, 0) || math.IsNaN(level) || level <= vadLevelFloorDB {
			continue
		}
		levels = append(levels, level)
	}
	slices.Sort(levels)
	return levels
}

// percentileOfSorted returns the value at the given percentile (0-100) of an
// already-sorted slice using nearest-rank. Returns 0 for an empty slice.
func percentileOfSorted(sorted []float64, pct float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	pct = max(0, min(100, pct))
	idx := int(pct / 100 * float64(len(sorted)-1))
	return sorted[idx]
}

// otsuSplit returns the level that maximises the between-class variance of the
// two histogram classes (Otsu's method), in one O(bins) pass with no tunable
// constant. The returned split is the centre of the bin at the optimal
// threshold. Returns the histogram midpoint when the histogram is empty or
// degenerate (a single populated bin), leaving the clamp to make it sane.
func otsuSplit(h histogram) float64 {
	if h.count == 0 || len(h.bins) < 2 {
		return (h.minLevel + h.maxLevel) / 2
	}

	total := float64(h.count)

	// Sum of (binCentre * count) over all bins, for the global mean numerator.
	var sumAll float64
	for i, c := range h.bins {
		sumAll += h.binCentre(i) * float64(c)
	}

	var weightBackground float64 // cumulative count below the threshold
	var sumBackground float64    // cumulative (centre*count) below the threshold
	var bestVariance float64
	bestIdx := -1

	// Threshold between bin i and i+1: background is bins [0..i], foreground the rest.
	for i := 0; i < len(h.bins)-1; i++ {
		weightBackground += float64(h.bins[i])
		sumBackground += h.binCentre(i) * float64(h.bins[i])

		weightForeground := total - weightBackground
		if weightBackground == 0 || weightForeground == 0 {
			continue
		}

		meanBackground := sumBackground / weightBackground
		meanForeground := (sumAll - sumBackground) / weightForeground

		diff := meanBackground - meanForeground
		variance := weightBackground * weightForeground * diff * diff
		if variance > bestVariance {
			bestVariance = variance
			bestIdx = i
		}
	}

	if bestIdx < 0 {
		return (h.minLevel + h.maxLevel) / 2
	}

	// Split sits on the upper edge of the background bin (between bin bestIdx and
	// bestIdx+1), the boundary that separated the two classes.
	return h.minLevel + float64(bestIdx+1)*h.binWidth
}

// vadNoiseFloorPercentile is the low percentile of the per-interval level set
// taken as the noise floor (minimum-statistics logic). 10th percentile sits in
// the research-suggested 5th-to-10th band. It ignores the occasional quiet
// breath without chasing the single quietest interval.
const vadNoiseFloorPercentile = 10.0

// percentileFloor returns the vadNoiseFloorPercentile low percentile of the
// per-interval level set as the noise floor, clamped not below
// noiseFloorSeed + speechRMSMinimumNoiseMargin. The seed is the measured
// pre-scan floor. The clamp keeps the floor from dropping into digital silence
// on voice-activated material. levels must already be sorted ascending. The
// percentile is the named constant, the single tuning seam.
func percentileFloor(levels []float64, noiseFloorSeed float64) float64 {
	floor := percentileOfSorted(levels, vadNoiseFloorPercentile)
	anchor := noiseFloorSeed + speechRMSMinimumNoiseMargin
	return max(floor, anchor)
}

// clampSplit constrains the split to [noiseFloor + speechRMSMinimumNoiseMargin,
// p75]. The lower clamp (the existing measured 6 dB margin) stops a degenerate
// low split from admitting room tone; the upper clamp (the 75th percentile of
// the per-interval level) stops a degenerate high split from rejecting all
// speech. When the bounds invert (lower > upper, a near-uniform file), the
// lower bound wins so the split never drops into the noise.
func clampSplit(split, noiseFloor, p75 float64) float64 {
	lower := noiseFloor + speechRMSMinimumNoiseMargin
	if p75 < lower {
		return lower
	}
	return max(lower, min(p75, split))
}

// passesSpectralVeto reports whether an interval's spectral metrics allow it to
// count as speech: centroid inside the voice band and entropy below the
// ceiling. The voice-band bounds (speechCentroidMin/Max) and entropy ceiling
// (speechEntropyMax) are retained for v1; making them adaptive is a follow-up.
// The flag and the loud-gap guard share this one veto.
func passesSpectralVeto(s IntervalSample) bool {
	return s.Spectral.Centroid >= speechCentroidMin &&
		s.Spectral.Centroid <= speechCentroidMax &&
		s.Spectral.Entropy < speechEntropyMax
}

// isSpeechInterval flags an interval as speech with one rule: level at or above
// the split AND the spectral veto passes. No weighted score, no rescue of
// below-split voiced intervals. This is the same predicate the loud-gap guard
// applies inside a run.
func isSpeechInterval(s IntervalSample, split float64, axis levelAxis) bool {
	return intervalLevel(s, axis) >= split && passesSpectralVeto(s)
}

const (
	// vadHysteresisFraction sets the hysteresis margin as a fraction of the
	// split-to-upper-mode distance. The two thresholds are split + margin (enter)
	// and split - margin (leave). Data-derived, not a fixed dB.
	vadHysteresisFraction = 0.25

	// vadHysteresisFallbackDB is the fixed-dB margin used when the upper-mode
	// distance is non-positive (a degenerate single-mode histogram leaves no
	// foreground class to measure).
	vadHysteresisFallbackDB = 1.0
)

// upperModeCentre returns the mean level of the foreground class (bins whose
// centre is at or above the split), the centre of the high mode. Returns the
// split when no foreground bin is populated.
func upperModeCentre(h histogram, split float64) float64 {
	var weighted, count float64
	for i, c := range h.bins {
		centre := h.binCentre(i)
		if centre >= split {
			weighted += centre * float64(c)
			count += float64(c)
		}
	}
	if count == 0 {
		return split
	}
	return weighted / count
}

// hysteresisMargin derives the hysteresis margin from the histogram as a
// fraction of the split-to-upper-mode distance. Falls back to a small fixed dB
// when that distance is non-positive. The margin is always positive.
func hysteresisMargin(h histogram, split float64) float64 {
	distance := upperModeCentre(h, split) - split
	if distance <= 0 {
		return vadHysteresisFallbackDB
	}
	return distance * vadHysteresisFraction
}

// gapToleranceIntervals measures the inter-speech gaps in a first speech-flag
// pass and returns clamp(p75(gaps), vadGapToleranceFloor, vadGapToleranceCeiling)
// converted to interval counts against the hop. Only gaps bounded by speech on
// both sides count: the trailing post-speech tail to EOF is excluded (it is not
// a bridgeable gap). With no interior gap, the floor applies.
func gapToleranceIntervals(flags []bool, hop time.Duration) int {
	floor := intervalsForDuration(vadGapToleranceFloor, hop)
	ceiling := intervalsForDuration(vadGapToleranceCeiling, hop)

	firstSpeech := -1
	lastSpeech := -1
	for i, f := range flags {
		if f {
			if firstSpeech < 0 {
				firstSpeech = i
			}
			lastSpeech = i
		}
	}
	if firstSpeech < 0 {
		return floor
	}

	// Measure runs of non-speech strictly between the first and last speech flag.
	var gaps []float64
	gapLen := 0
	for i := firstSpeech; i <= lastSpeech; i++ {
		if flags[i] {
			if gapLen > 0 {
				gaps = append(gaps, float64(gapLen))
			}
			gapLen = 0
			continue
		}
		gapLen++
	}

	if len(gaps) == 0 {
		return floor
	}

	slices.Sort(gaps)
	p75 := int(math.Round(percentileOfSorted(gaps, 75)))
	return max(floor, min(ceiling, p75))
}

// speechFlags returns the per-interval speech flag (isSpeechInterval) over the
// whole interval stream, the first pass the gap-tolerance measurement consumes.
func speechFlags(intervals []IntervalSample, split float64, axis levelAxis) []bool {
	flags := make([]bool, len(intervals))
	for i := range intervals {
		flags[i] = isSpeechInterval(intervals[i], split, axis)
	}
	return flags
}

// buildSpeechRuns forms speech regions from the interval stream with a
// two-threshold hysteresis builder and a loud-gap over-bridging guard.
//
//   - Enter a run when an interval is above the high threshold (split + margin)
//     and passes the spectral veto.
//   - Stay in the run while intervals are speech (isSpeechInterval) or quiet
//     gaps below the low threshold (split - margin), bridging up to tol gap
//     intervals.
//   - Loud-gap guard: end the run when a bridging interval is at or above the
//     split yet fails the spectral veto (a loud non-speech interruption such as
//     a music bed or second speaker). Quiet gaps (below the split) stay
//     bridgeable.
//   - A run becomes a region only when it spans at least minIntervals.
//
// There is no hangover and no outward segment-end extension: golden refinement
// biases the elected sample inward, so outward extension would fight it. Run
// end times derive from the hop, not a baked-in interval duration.
func buildSpeechRuns(intervals []IntervalSample, split, margin float64, tol int, axis levelAxis, hop time.Duration) []SpeechRegion {
	minIntervals := intervalsForDuration(vadMinSpeechDuration, hop)
	if len(intervals) < minIntervals || minIntervals <= 0 {
		return nil
	}

	high := split + margin
	low := split - margin

	var runs []SpeechRegion
	var runStart time.Duration
	var runSpeechCount int // speech intervals counted toward the minimum
	var lastSpeechIdx int  // index of the most recent speech interval in the run
	var pendingGap int     // consecutive bridging (non-speech) intervals since last speech
	inRun := false

	flush := func(endIdx int) {
		if inRun && runSpeechCount >= minIntervals {
			endTime := intervals[endIdx].Timestamp + hop
			runs = append(runs, SpeechRegion{
				Start:    runStart,
				End:      endTime,
				Duration: endTime - runStart,
			})
		}
		inRun = false
		runSpeechCount = 0
		pendingGap = 0
	}

	for i := range intervals {
		s := intervals[i]
		level := intervalLevel(s, axis)
		veto := passesSpectralVeto(s)
		isSpeech := level >= split && veto

		if !inRun {
			// Enter only above the high threshold with a passing veto.
			if level >= high && veto {
				runStart = s.Timestamp
				runSpeechCount = 1
				lastSpeechIdx = i
				pendingGap = 0
				inRun = true
			}
			continue
		}

		if isSpeech {
			runSpeechCount++
			lastSpeechIdx = i
			pendingGap = 0
			continue
		}

		// Loud-gap guard: a loud (>= split) interval that fails the veto ends the
		// run at the last speech interval, wherever it occurs.
		if level >= split && !veto {
			flush(lastSpeechIdx)
			continue
		}

		// Neutral zone (low <= level < split): held by hysteresis, not counted as
		// a gap. Only intervals below the low threshold are bridgeable gaps, and
		// the run leaves when such a gap outlasts the tolerance.
		if level < low {
			pendingGap++
			if pendingGap > tol {
				flush(lastSpeechIdx)
			}
		}
	}

	flush(lastSpeechIdx)
	return runs
}

// idealDurationMin and idealDurationMax bound the duration the noise-profile
// extraction treats as ideal; outside this range it emits a short/long
// extraction warning. Read by extractNoiseProfileFromIntervals below.
const (
	idealDurationMin = 8 * time.Second  // Ideal range lower bound
	idealDurationMax = 18 * time.Second // Ideal range upper bound
)

// extractNoiseProfileFromIntervals creates a NoiseProfile using pre-collected interval data.
// This avoids re-reading the audio file - all measurements come from Pass 1's interval samples.
// Returns nil if no intervals fall within the region. Moved here from the room-tone file so the
// detector keeps the exact NoiseProfile field shape and the short/long extraction warnings.
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

// electSpeechProfile feeds the hysteresis-built speech runs to the reused
// findBestSpeechRegion scoring and election (scoring, SNR penalty, golden
// refinement), then returns the elected candidate as a *SpeechCandidateMetrics
// to assign to SpeechProfile. The candidate list is returned for the report.
// Returns (nil, candidates) when no region is elected.
func electSpeechProfile(runs []SpeechRegion, intervals []IntervalSample, noiseProfile *NoiseProfile, log debugLogger) (*SpeechCandidateMetrics, []SpeechCandidateMetrics) {
	result := findBestSpeechRegion(runs, intervals, noiseProfile, log)
	if result.BestRegion == nil {
		return nil, result.Candidates
	}

	for i := range result.Candidates {
		if result.Candidates[i].Region.Start == result.BestRegion.Start {
			return &result.Candidates[i], result.Candidates
		}
	}
	return nil, result.Candidates
}

// pickLowClusterRegion returns the longest contiguous run of below-split
// intervals as the representative room-tone region, golden-refined to a clean
// inner window via the reused refineToSubregion. This replaces the scored
// room-tone election: one split places every below-split interval in the noise
// cluster, and the longest such run is the steadiest sample of it. Returns nil
// when no below-split run exists.
func pickLowClusterRegion(intervals []IntervalSample, split float64, axis levelAxis, hop time.Duration) *RoomToneRegion {
	var best *RoomToneRegion
	var runStart time.Duration
	var runLen int
	inRun := false

	closeRun := func(endIdx int) {
		if !inRun {
			return
		}
		endTime := intervals[endIdx].Timestamp + hop
		region := &RoomToneRegion{Start: runStart, End: endTime, Duration: endTime - runStart}
		if best == nil || region.Duration > best.Duration {
			best = region
		}
		inRun = false
		runLen = 0
	}

	for i := range intervals {
		below := intervalLevel(intervals[i], axis) < split
		if below {
			if !inRun {
				runStart = intervals[i].Timestamp
				inRun = true
			}
			runLen++
			continue
		}
		if inRun {
			closeRun(i - 1)
		}
	}
	if inRun {
		closeRun(len(intervals) - 1)
	}

	if best == nil {
		return nil
	}

	// Golden refinement: trim a long quiet run to its cleanest (lowest-RMS) inner
	// window, biasing the noise sample inward. Reuses the shared sliding-window
	// refinement with the room-tone window bounds.
	start, end, dur, ok := refineToSubregion(
		best.Start, best.End, best.Duration,
		intervals,
		goldenWindowDuration, goldenWindowMinimum,
		scoreIntervalWindow,
		func(candidate, current float64) bool { return candidate < current },
	)
	if ok {
		return &RoomToneRegion{Start: start, End: end, Duration: dur}
	}
	return best
}

// vadVoiceActivatedFraction is the low-cluster fraction at or above which the
// recording is flagged voice-activated. A high fraction of below-split
// intervals means speech is sparse against long silences, the voice-activated
// signature. Replaces the room-tone digital-silence count, which no longer
// exists under the unified detector.
const vadVoiceActivatedFraction = 0.60

// lowClusterFraction returns the fraction of non-floored intervals whose level
// is below the split.
func lowClusterFraction(intervals []IntervalSample, split float64, axis levelAxis) float64 {
	var counted, below float64
	for _, iv := range intervals {
		level := intervalLevel(iv, axis)
		if math.IsInf(level, 0) || math.IsNaN(level) || level <= vadLevelFloorDB {
			continue
		}
		counted++
		if level < split {
			below++
		}
	}
	if counted == 0 {
		return 0
	}
	return below / counted
}

// detectVoiceActivity is the unified Pass 1 voice-activity detector. One bimodal
// split on a per-interval level histogram feeds both outputs the adaptive
// filters consume: the elected SpeechProfile and the NoiseProfile / Noise.Floor.
// It replaces the selectNoiseProfile + selectSpeechProfile pair. The body only
// wires the per-stage helpers; the maths lives in those helpers.
func detectVoiceActivity(measurements *AudioMeasurements, intervals []IntervalSample, noiseFloorSeed float64, hop time.Duration, axis levelAxis, log debugLogger) {
	const histogramBinWidthDB = 1.0

	histogram := buildLevelHistogram(intervals, axis, histogramBinWidthDB)
	levels := vadLevels(intervals, axis)
	p75 := percentileOfSorted(levels, 75)

	split := clampSplit(otsuSplit(histogram), noiseFloorSeed, p75)
	floor := percentileFloor(levels, noiseFloorSeed)

	flags := speechFlags(intervals, split, axis)
	margin := hysteresisMargin(histogram, split)
	tol := gapToleranceIntervals(flags, hop)

	runs := buildSpeechRuns(intervals, split, margin, tol, axis, hop)
	measurements.Regions.SpeechRegions = runs

	noiseRegion := pickLowClusterRegion(intervals, split, axis, hop)
	var noiseProfile *NoiseProfile
	if noiseRegion != nil {
		noiseProfile = extractNoiseProfileFromIntervals(noiseRegion, intervals)
	}
	if noiseProfile != nil {
		noiseProfile.MeasuredNoiseFloor = floor
		measurements.Regions.NoiseProfile = noiseProfile
		setVADRoomToneCandidate(measurements, noiseRegion, intervals)
	}

	profile, candidates := electSpeechProfile(runs, intervals, noiseProfile, log)
	measurements.Regions.SpeechCandidates = candidates
	if profile != nil {
		measurements.Regions.SpeechProfile = profile
	}

	measurements.Noise.Floor = floor
	measurements.Noise.FloorSource = "vad_percentile"
	measurements.Noise.VoiceActivated = lowClusterFraction(intervals, split, axis) >= vadVoiceActivatedFraction

	log.Logf("VAD: split=%.1f dB (axis=%d), floor=%.1f dB, margin=%.2f dB, gapTol=%d, runs=%d, speechElected=%v, noiseRegion=%v",
		split, axis, floor, margin, tol, len(runs), profile != nil, noiseRegion != nil)
}

// setVADRoomToneCandidate populates RoomToneCandidates with a single real entry,
// the elected low-cluster region, so the report's room-tone candidates_summary
// survives and ElectedRoomToneSample (regions.room_tone.samples.input) is set.
// Its Region matches the NoiseProfile Start/Duration so newRoomToneCandidatesSummary
// finds it. Measured with the existing candidate-measurement shape (no new maths).
func setVADRoomToneCandidate(measurements *AudioMeasurements, region *RoomToneRegion, intervals []IntervalSample) {
	metrics := measureRoomToneCandidateFromIntervals(*region, intervals)
	if metrics == nil {
		return
	}
	measurements.Regions.RoomToneRegions = []RoomToneRegion{*region}
	measurements.Regions.RoomToneCandidates = []RoomToneCandidateMetrics{*metrics}
	measurements.Regions.ElectedRoomToneSample = &measurements.Regions.RoomToneCandidates[0].RegionSample
}
