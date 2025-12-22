// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// SilenceRegion represents a detected silence period in the audio
type SilenceRegion struct {
	Start    time.Duration `json:"start"`
	End      time.Duration `json:"end"`
	Duration time.Duration `json:"duration"`
}

// NoiseProfile contains information about an extracted noise sample
type NoiseProfile struct {
	Start              time.Duration `json:"start"`                        // Start time of silence region used
	Duration           time.Duration `json:"duration"`                     // Duration of extracted sample
	MeasuredNoiseFloor float64       `json:"measured_noise_floor"`         // dBFS, RMS level of silence (average noise)
	PeakLevel          float64       `json:"peak_level"`                   // dBFS, peak level in silence (transient noise indicator)
	CrestFactor        float64       `json:"crest_factor"`                 // Peak - RMS in dB (high = impulsive noise, low = steady noise)
	Entropy            float64       `json:"entropy"`                      // Signal randomness (1.0 = white noise, lower = tonal noise like hum)
	ExtractionWarning  string        `json:"extraction_warning,omitempty"` // Warning message if extraction had issues

	// Spectral characteristics for contamination detection (added during candidate evaluation)
	SpectralCentroid float64 `json:"spectral_centroid,omitempty"` // Hz, where energy is concentrated (voice range: 300-4000 Hz)
	SpectralFlatness float64 `json:"spectral_flatness,omitempty"` // 0-1, noise-like vs tonal (higher = more noise-like)
	SpectralKurtosis float64 `json:"spectral_kurtosis,omitempty"` // Peakiness (high = peaked harmonics like speech)

	// Golden sub-region refinement info (populated when a long candidate is refined)
	OriginalStart    time.Duration `json:"original_start,omitempty"`    // Original candidate start before refinement
	OriginalDuration time.Duration `json:"original_duration,omitempty"` // Original candidate duration before refinement
	WasRefined       bool          `json:"was_refined,omitempty"`       // True if region was refined from a longer candidate
}

// SilenceCandidateMetrics contains measurements for evaluating silence region candidates.
// These metrics are collected before final selection to enable multi-metric scoring.
type SilenceCandidateMetrics struct {
	Region SilenceRegion // The silence region being evaluated

	// Amplitude metrics (from astats)
	RMSLevel    float64 // dBFS, average level (lower = quieter)
	PeakLevel   float64 // dBFS, peak level (transient indicator)
	CrestFactor float64 // Peak - RMS in dB (high = impulsive)
	Entropy     float64 // 0-1, signal randomness (1.0 = broadband noise)

	// Spectral metrics (from aspectralstats) - key for crosstalk detection
	SpectralCentroid float64 // Hz, where energy is concentrated
	SpectralFlatness float64 // 0-1, noise-like (high) vs tonal (low)
	SpectralKurtosis float64 // Peakiness - high values indicate speech harmonics

	// Scoring (computed after measurement)
	Score float64 // Composite score for candidate ranking
}

// IntervalSample contains all measurements for a 250ms audio window.
// Captures comprehensive metrics from astats, aspectralstats, and ebur128 for
// silence detection, adaptive filter tuning, and post-hoc analysis.
type IntervalSample struct {
	Timestamp time.Duration `json:"timestamp"` // Start of this interval

	// ─── Amplitude metrics (calculated per-interval from raw samples) ───────────
	RMSLevel  float64 `json:"rms_level"`  // dBFS, RMS level calculated from raw frame samples
	PeakLevel float64 `json:"peak_level"` // dBFS, peak level (max tracked per interval)

	// ─── aspectralstats spectral metrics (valid per-window from FFmpeg) ─────────
	SpectralMean     float64 `json:"spectral_mean"`     // Average magnitude
	SpectralVariance float64 `json:"spectral_variance"` // Magnitude spread
	SpectralCentroid float64 `json:"spectral_centroid"` // Hz - "brightness", speech 300-3000 Hz
	SpectralSpread   float64 `json:"spectral_spread"`   // Hz - frequency bandwidth
	SpectralSkewness float64 `json:"spectral_skewness"` // Distribution asymmetry
	SpectralKurtosis float64 `json:"spectral_kurtosis"` // Distribution peakedness
	SpectralEntropy  float64 `json:"spectral_entropy"`  // 0-1 - speech has lower entropy than noise
	SpectralFlatness float64 `json:"spectral_flatness"` // 0-1 - high = noise-like, low = tonal
	SpectralCrest    float64 `json:"spectral_crest"`    // Spectral peakiness
	SpectralFlux     float64 `json:"spectral_flux"`     // Rate of spectral change (transitions)
	SpectralSlope    float64 `json:"spectral_slope"`    // High-frequency roll-off rate
	SpectralDecrease float64 `json:"spectral_decrease"` // High-frequency energy decay
	SpectralRolloff  float64 `json:"spectral_rolloff"`  // Hz - frequency below which 85% energy lies

	// ─── ebur128 loudness metrics (windowed measurements) ───────────────────────
	MomentaryLUFS float64 `json:"momentary_lufs"`  // LUFS - 400ms window loudness
	ShortTermLUFS float64 `json:"short_term_lufs"` // LUFS - 3s window loudness
	TruePeak      float64 `json:"true_peak"`       // dBTP - true peak level (max tracked)
	SamplePeak    float64 `json:"sample_peak"`     // dBFS - sample peak level (max tracked)
}

// intervalAccumulator holds accumulated values for a 250ms interval window.
// Values are aggregated appropriately: sums for averaging, min/max for extremes.
type intervalAccumulator struct {
	frameCount int // Number of frames in this interval

	// ─── Raw sample RMS accumulator (for accurate per-interval silence detection) ─
	// These are calculated directly from frame samples, not from astats metadata,
	// because astats with reset=0 provides cumulative stats, not per-interval.
	rawSumSquares  float64 // Sum of squared sample values (normalized -1 to 1)
	rawSampleCount int64   // Total sample count for this interval
	rawPeakAbs     float64 // Maximum absolute sample value (linear, 0.0-1.0) for this interval

	// ─── Peak tracking (max per interval, from astats metadata) ─────────────────
	peakMax float64 // Maximum peak level from astats (dBFS) - cumulative, less accurate

	// ─── aspectralstats accumulators (valid per-window from FFmpeg) ─────────────
	spectralMeanSum     float64
	spectralVarianceSum float64
	spectralCentroidSum float64
	spectralSpreadSum   float64
	spectralSkewnessSum float64
	spectralKurtosisSum float64
	spectralEntropySum  float64
	spectralFlatnessSum float64
	spectralCrestSum    float64
	spectralFluxSum     float64
	spectralSlopeSum    float64
	spectralDecreaseSum float64
	spectralRolloffSum  float64

	// ─── ebur128 accumulators (windowed measurements) ───────────────────────────
	momentaryLUFSSum float64
	shortTermLUFSSum float64
	truePeakMax      float64 // Maximum true peak
	samplePeakMax    float64 // Maximum sample peak
}

// intervalFrameMetrics holds per-frame metrics extracted from FFmpeg metadata.
// Only includes metrics that are valid per-window (not cumulative astats).
type intervalFrameMetrics struct {
	// Peak tracking (used for max tracking)
	PeakLevel float64

	// aspectralstats (valid per-window)
	SpectralMean     float64
	SpectralVariance float64
	SpectralCentroid float64
	SpectralSpread   float64
	SpectralSkewness float64
	SpectralKurtosis float64
	SpectralEntropy  float64
	SpectralFlatness float64
	SpectralCrest    float64
	SpectralFlux     float64
	SpectralSlope    float64
	SpectralDecrease float64
	SpectralRolloff  float64

	// ebur128 (windowed measurements)
	MomentaryLUFS float64
	ShortTermLUFS float64
	TruePeak      float64
	SamplePeak    float64
}

// add accumulates a frame's metrics into the interval.
func (a *intervalAccumulator) add(m intervalFrameMetrics) {
	// Peak levels: keep maximum
	if a.frameCount == 0 || m.PeakLevel > a.peakMax {
		a.peakMax = m.PeakLevel
	}
	if a.frameCount == 0 || m.TruePeak > a.truePeakMax {
		a.truePeakMax = m.TruePeak
	}
	if a.frameCount == 0 || m.SamplePeak > a.samplePeakMax {
		a.samplePeakMax = m.SamplePeak
	}

	// aspectralstats sums for averaging (valid per-window measurements)
	a.spectralMeanSum += m.SpectralMean
	a.spectralVarianceSum += m.SpectralVariance
	a.spectralCentroidSum += m.SpectralCentroid
	a.spectralSpreadSum += m.SpectralSpread
	a.spectralSkewnessSum += m.SpectralSkewness
	a.spectralKurtosisSum += m.SpectralKurtosis
	a.spectralEntropySum += m.SpectralEntropy
	a.spectralFlatnessSum += m.SpectralFlatness
	a.spectralCrestSum += m.SpectralCrest
	a.spectralFluxSum += m.SpectralFlux
	a.spectralSlopeSum += m.SpectralSlope
	a.spectralDecreaseSum += m.SpectralDecrease
	a.spectralRolloffSum += m.SpectralRolloff

	// ebur128 sums for averaging (windowed measurements)
	a.momentaryLUFSSum += m.MomentaryLUFS
	a.shortTermLUFSSum += m.ShortTermLUFS

	a.frameCount++
}

// frameSumSquaresAndPeak calculates sum of squared sample values, sample count, and peak from an audio frame.
// Handles S16, FLT, S32, and DBL sample formats, normalizing to [-1.0, 1.0] range.
// Returns sumSquares, sampleCount, peakAbsolute, and ok (false if format is unsupported or frame is invalid).
func frameSumSquaresAndPeak(frame *ffmpeg.AVFrame) (sumSquares float64, sampleCount int64, peakAbs float64, ok bool) {
	if frame == nil || frame.NbSamples() == 0 {
		return 0, 0, 0, false
	}

	sampleFmt := frame.Format()
	nbSamples := frame.NbSamples()
	nbChannels := frame.ChLayout().NbChannels()

	dataPtr := frame.Data().Get(0)
	if dataPtr == nil {
		return 0, 0, 0, false
	}

	switch ffmpeg.AVSampleFormat(sampleFmt) {
	case ffmpeg.AVSampleFmtS16, ffmpeg.AVSampleFmtS16P:
		samples := unsafe.Slice((*int16)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample) / 32768.0
			sumSquares += normalized * normalized
			sampleCount++
			absVal := math.Abs(normalized)
			if absVal > peakAbs {
				peakAbs = absVal
			}
		}
		return sumSquares, sampleCount, peakAbs, true

	case ffmpeg.AVSampleFmtFlt, ffmpeg.AVSampleFmtFltp:
		samples := unsafe.Slice((*float32)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample)
			sumSquares += normalized * normalized
			sampleCount++
			absVal := math.Abs(normalized)
			if absVal > peakAbs {
				peakAbs = absVal
			}
		}
		return sumSquares, sampleCount, peakAbs, true

	case ffmpeg.AVSampleFmtS32, ffmpeg.AVSampleFmtS32P:
		samples := unsafe.Slice((*int32)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample) / 2147483648.0
			sumSquares += normalized * normalized
			sampleCount++
			absVal := math.Abs(normalized)
			if absVal > peakAbs {
				peakAbs = absVal
			}
		}
		return sumSquares, sampleCount, peakAbs, true

	case ffmpeg.AVSampleFmtDbl, ffmpeg.AVSampleFmtDblp:
		samples := unsafe.Slice((*float64)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			sumSquares += sample * sample
			sampleCount++
			absVal := math.Abs(sample)
			if absVal > peakAbs {
				peakAbs = absVal
			}
		}
		return sumSquares, sampleCount, peakAbs, true

	default:
		return 0, 0, 0, false
	}
}

// addFrameRMSAndPeak accumulates RMS and peak from raw frame samples for accurate per-interval measurement.
// This bypasses astats metadata (which is cumulative) to get true per-interval RMS and peak.
func (a *intervalAccumulator) addFrameRMSAndPeak(frame *ffmpeg.AVFrame) {
	if ss, count, peak, ok := frameSumSquaresAndPeak(frame); ok {
		a.rawSumSquares += ss
		a.rawSampleCount += count
		if peak > a.rawPeakAbs {
			a.rawPeakAbs = peak
		}
	}
}

// finalize converts accumulated values to an IntervalSample.
func (a *intervalAccumulator) finalize(timestamp time.Duration) IntervalSample {
	// PeakLevel: Use raw sample calculation for accurate per-interval measurement
	// This is calculated directly from frame samples, not from astats metadata,
	// because astats with reset=0 provides cumulative stats, not per-interval.
	var peakLevelDB float64
	if a.rawPeakAbs > 0 {
		peakLevelDB = 20.0 * math.Log10(a.rawPeakAbs)
	} else {
		peakLevelDB = -120.0
	}

	sample := IntervalSample{
		Timestamp: timestamp,

		// Max values
		PeakLevel:  peakLevelDB,
		TruePeak:   a.truePeakMax,
		SamplePeak: a.samplePeakMax,
	}

	// RMS Level: Use raw sample calculation for accurate per-interval measurement
	// This is calculated directly from frame samples, not from astats metadata,
	// because astats with reset=0 provides cumulative stats, not per-interval.
	if a.rawSampleCount > 0 {
		rms := math.Sqrt(a.rawSumSquares / float64(a.rawSampleCount))
		if rms < 0.00001 { // Equivalent to < -100 dB
			sample.RMSLevel = -120.0
		} else {
			sample.RMSLevel = 20.0 * math.Log10(rms)
		}
	} else {
		sample.RMSLevel = -120.0
	}

	if a.frameCount > 0 {
		n := float64(a.frameCount)

		// aspectralstats averages (valid per-window measurements)
		sample.SpectralMean = a.spectralMeanSum / n
		sample.SpectralVariance = a.spectralVarianceSum / n
		sample.SpectralCentroid = a.spectralCentroidSum / n
		sample.SpectralSpread = a.spectralSpreadSum / n
		sample.SpectralSkewness = a.spectralSkewnessSum / n
		sample.SpectralKurtosis = a.spectralKurtosisSum / n
		sample.SpectralEntropy = a.spectralEntropySum / n
		sample.SpectralFlatness = a.spectralFlatnessSum / n
		sample.SpectralCrest = a.spectralCrestSum / n
		sample.SpectralFlux = a.spectralFluxSum / n
		sample.SpectralSlope = a.spectralSlopeSum / n
		sample.SpectralDecrease = a.spectralDecreaseSum / n
		sample.SpectralRolloff = a.spectralRolloffSum / n

		// ebur128 averages (windowed measurements)
		sample.MomentaryLUFS = a.momentaryLUFSSum / n
		sample.ShortTermLUFS = a.shortTermLUFSSum / n
	}

	return sample
}

// reset clears the accumulator for the next interval.
func (a *intervalAccumulator) reset() {
	a.frameCount = 0

	// Raw sample RMS and peak
	a.rawSumSquares = 0
	a.rawSampleCount = 0
	a.rawPeakAbs = 0

	// Peak tracking (astats metadata)
	a.peakMax = -120.0

	// aspectralstats
	a.spectralMeanSum = 0
	a.spectralVarianceSum = 0
	a.spectralCentroidSum = 0
	a.spectralSpreadSum = 0
	a.spectralSkewnessSum = 0
	a.spectralKurtosisSum = 0
	a.spectralEntropySum = 0
	a.spectralFlatnessSum = 0
	a.spectralCrestSum = 0
	a.spectralFluxSum = 0
	a.spectralSlopeSum = 0
	a.spectralDecreaseSum = 0
	a.spectralRolloffSum = 0

	// ebur128
	a.momentaryLUFSSum = 0
	a.shortTermLUFSSum = 0
	a.truePeakMax = -120.0
	a.samplePeakMax = -120.0
}

// Silence detection constants for interval-based analysis
const (
	// minimumSilenceIntervals is the minimum number of consecutive silent intervals
	// for a region to be considered a valid silence candidate.
	// Must match minimumSilenceDuration (8s) for profile extraction: 8s / 250ms = 32 intervals
	minimumSilenceIntervals = 32

	// excludeFirstSeconds: ignore candidates starting in this initial period
	// (typically contains preamble before intentional room tone recording)
	excludeFirstSeconds = 15.0

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

	// silenceSearchPercent is the percentage of recording to search for silence candidates (15%).
	silenceSearchPercent = 15

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

	// No refinement needed if already at or below target duration
	if candidate.Duration <= goldenWindowDuration {
		return candidate
	}

	// Extract intervals within the candidate's time range
	candidateIntervals := getIntervalsInRange(intervals, candidate.Start, candidate.End)
	if candidateIntervals == nil {
		return candidate
	}

	// Calculate window size in intervals (10s / 250ms = 40 intervals)
	windowIntervals := int(goldenWindowDuration / goldenIntervalSize)
	minimumIntervals := int(goldenWindowMinimum / goldenIntervalSize)

	// Need at least minimum window worth of intervals
	if len(candidateIntervals) < minimumIntervals {
		return candidate
	}

	// If we have fewer intervals than target window, use what we have
	if len(candidateIntervals) < windowIntervals {
		windowIntervals = len(candidateIntervals)
	}

	// Slide window across intervals, finding position with lowest average RMS
	bestStartIdx := 0
	bestRMS := scoreIntervalWindow(candidateIntervals[:windowIntervals])

	for startIdx := 1; startIdx <= len(candidateIntervals)-windowIntervals; startIdx++ {
		windowRMS := scoreIntervalWindow(candidateIntervals[startIdx : startIdx+windowIntervals])
		if windowRMS < bestRMS {
			bestRMS = windowRMS
			bestStartIdx = startIdx
		}
	}

	// Calculate refined region bounds from the best window position
	refinedStart := candidateIntervals[bestStartIdx].Timestamp
	refinedDuration := time.Duration(windowIntervals) * goldenIntervalSize
	refinedEnd := refinedStart + refinedDuration

	return &SilenceRegion{
		Start:    refinedStart,
		End:      refinedEnd,
		Duration: refinedDuration,
	}
}

// getIntervalsInRange returns intervals that fall within the given time range.
// Returns nil if no intervals found in range.
func getIntervalsInRange(intervals []IntervalSample, start, end time.Duration) []IntervalSample {
	if len(intervals) == 0 {
		return nil
	}

	// Find first interval at or after start time
	startIdx := -1
	for i, interval := range intervals {
		if interval.Timestamp >= start {
			startIdx = i
			break
		}
	}
	if startIdx < 0 {
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

	// Accumulate metrics for averaging
	var rmsSum, peakMax float64
	var centroidSum, flatnessSum, kurtosisSum, entropySum float64
	peakMax = -120.0

	for _, interval := range regionIntervals {
		rmsSum += interval.RMSLevel
		if interval.PeakLevel > peakMax {
			peakMax = interval.PeakLevel
		}
		centroidSum += interval.SpectralCentroid
		flatnessSum += interval.SpectralFlatness
		kurtosisSum += interval.SpectralKurtosis
		entropySum += interval.SpectralEntropy
	}

	n := float64(len(regionIntervals))

	return &SilenceCandidateMetrics{
		Region:           region,
		RMSLevel:         rmsSum / n,
		PeakLevel:        peakMax,
		CrestFactor:      peakMax - (rmsSum / n), // Peak - RMS in dB
		Entropy:          entropySum / n,
		SpectralCentroid: centroidSum / n,
		SpectralFlatness: flatnessSum / n,
		SpectralKurtosis: kurtosisSum / n,
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
func estimateNoiseFloorAndThreshold(intervals []IntervalSample) (noiseFloor, silenceThreshold float64, ok bool) {
	if len(intervals) < silenceThresholdMinIntervals {
		return 0, 0, false
	}

	// Only use the first silenceSearchPercent% of intervals for threshold calculation
	searchLimit := len(intervals) * silenceSearchPercent / 100
	if searchLimit < silenceThresholdMinIntervals {
		searchLimit = silenceThresholdMinIntervals
	}
	searchIntervals := intervals[:searchLimit]

	// Calculate medians for scoring reference
	rmsLevels := make([]float64, len(searchIntervals))
	fluxValues := make([]float64, len(searchIntervals))
	for i, interval := range searchIntervals {
		rmsLevels[i] = interval.RMSLevel
		fluxValues[i] = interval.SpectralFlux
	}
	sort.Float64s(rmsLevels)
	sort.Float64s(fluxValues)

	rmsP50 := rmsLevels[len(rmsLevels)/2]
	fluxP50 := fluxValues[len(fluxValues)/2]

	// Score each interval for room tone likelihood
	type scoredInterval struct {
		idx   int
		rms   float64
		score float64
	}
	scored := make([]scoredInterval, len(searchIntervals))
	for i, interval := range searchIntervals {
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
// 1. Calculate reference values (medians) for room tone scoring
// 2. Score each interval for "room tone likelihood"
// 3. Use a score threshold (0.5) to identify room tone intervals
// 4. Find consecutive runs that meet minimum duration (8 seconds)
//
// The RMS threshold parameter is used as a hard ceiling - intervals above it
// cannot be silence regardless of spectral characteristics.
// Candidates in the first 15 seconds are excluded (typically contains intro).
func findSilenceCandidatesFromIntervals(intervals []IntervalSample, threshold float64, _ float64) []SilenceRegion {
	if len(intervals) < minimumSilenceIntervals {
		return nil
	}

	// Only search the first silenceSearchPercent% of the recording
	searchLimit := len(intervals) * silenceSearchPercent / 100
	if searchLimit < minimumSilenceIntervals {
		searchLimit = minimumSilenceIntervals
	}
	searchIntervals := intervals[:searchLimit]

	// Calculate medians for room tone scoring
	rmsLevels := make([]float64, len(searchIntervals))
	fluxValues := make([]float64, len(searchIntervals))
	for i, interval := range searchIntervals {
		rmsLevels[i] = interval.RMSLevel
		fluxValues[i] = interval.SpectralFlux
	}
	sort.Float64s(rmsLevels)
	sort.Float64s(fluxValues)

	rmsP50 := rmsLevels[len(rmsLevels)/2]
	fluxP50 := fluxValues[len(fluxValues)/2]

	var candidates []SilenceRegion
	var silenceStart time.Duration
	var silentIntervalCount int
	var interruptionCount int // consecutive intervals below score threshold
	inSilence := false
	excludeTime := time.Duration(excludeFirstSeconds * float64(time.Second))

	for i := 0; i < searchLimit; i++ {
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

					// Only include if not in excluded first 15 seconds
					if silenceStart >= excludeTime {
						candidates = append(candidates, SilenceRegion{
							Start:    silenceStart,
							End:      endTime,
							Duration: duration,
						})
					}
				}
				inSilence = false
				silentIntervalCount = 0
				interruptionCount = 0
			}
			// else: within tolerance, continue silence region
		}
	}

	// Handle silence that extends to the search limit
	if inSilence && silentIntervalCount >= minimumSilenceIntervals {
		lastInterval := intervals[searchLimit-1]
		endTime := lastInterval.Timestamp + 250*time.Millisecond
		duration := endTime - silenceStart

		// Only include if not in excluded first 15 seconds
		if silenceStart >= excludeTime {
			candidates = append(candidates, SilenceRegion{
				Start:    silenceStart,
				End:      endTime,
				Duration: duration,
			})
		}
	}

	return candidates
}

// Cached metadata keys for frame extraction - avoids per-frame C string allocations
// These use GlobalCStr which maintains an internal cache, so identical strings share the same CStr
var (
	// aspectralstats metadata keys (all measurements)
	metaKeySpectralMean     = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.mean")
	metaKeySpectralVariance = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.variance")
	metaKeySpectralCentroid = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.centroid")
	metaKeySpectralSpread   = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.spread")
	metaKeySpectralSkewness = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.skewness")
	metaKeySpectralKurtosis = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.kurtosis")
	metaKeySpectralEntropy  = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.entropy")
	metaKeySpectralFlatness = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.flatness")
	metaKeySpectralCrest    = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.crest")
	metaKeySpectralFlux     = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.flux")
	metaKeySpectralSlope    = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.slope")
	metaKeySpectralDecrease = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.decrease")
	metaKeySpectralRolloff  = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.rolloff")

	// astats per-channel metadata keys (channel .1 for mono after downmix)
	metaKeyDynamicRange      = ffmpeg.GlobalCStr("lavfi.astats.1.Dynamic_range")
	metaKeyRMSLevel          = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_level")
	metaKeyPeakLevel         = ffmpeg.GlobalCStr("lavfi.astats.1.Peak_level")
	metaKeyRMSTrough         = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_trough")
	metaKeyRMSPeak           = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_peak")
	metaKeyDCOffset          = ffmpeg.GlobalCStr("lavfi.astats.1.DC_offset")
	metaKeyFlatFactor        = ffmpeg.GlobalCStr("lavfi.astats.1.Flat_factor")
	metaKeyCrestFactor       = ffmpeg.GlobalCStr("lavfi.astats.1.Crest_factor")
	metaKeyZeroCrossingsRate = ffmpeg.GlobalCStr("lavfi.astats.1.Zero_crossings_rate")
	metaKeyZeroCrossings     = ffmpeg.GlobalCStr("lavfi.astats.1.Zero_crossings")
	metaKeyMaxDifference     = ffmpeg.GlobalCStr("lavfi.astats.1.Max_difference")
	metaKeyMinDifference     = ffmpeg.GlobalCStr("lavfi.astats.1.Min_difference")
	metaKeyMeanDifference    = ffmpeg.GlobalCStr("lavfi.astats.1.Mean_difference")
	metaKeyRMSDifference     = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_difference")
	metaKeyEntropy           = ffmpeg.GlobalCStr("lavfi.astats.1.Entropy")
	metaKeyMinLevel          = ffmpeg.GlobalCStr("lavfi.astats.1.Min_level")
	metaKeyMaxLevel          = ffmpeg.GlobalCStr("lavfi.astats.1.Max_level")
	metaKeyNoiseFloor        = ffmpeg.GlobalCStr("lavfi.astats.1.Noise_floor")
	metaKeyNoiseFloorCount   = ffmpeg.GlobalCStr("lavfi.astats.1.Noise_floor_count")
	metaKeyBitDepth          = ffmpeg.GlobalCStr("lavfi.astats.1.Bit_depth")
	metaKeyNumberOfSamples   = ffmpeg.GlobalCStr("lavfi.astats.1.Number_of_samples")

	// ebur128 metadata keys
	metaKeyEbur128I            = ffmpeg.GlobalCStr("lavfi.r128.I")
	metaKeyEbur128M            = ffmpeg.GlobalCStr("lavfi.r128.M")
	metaKeyEbur128S            = ffmpeg.GlobalCStr("lavfi.r128.S")
	metaKeyEbur128TruePeak     = ffmpeg.GlobalCStr("lavfi.r128.true_peak")
	metaKeyEbur128SamplePeak   = ffmpeg.GlobalCStr("lavfi.r128.sample_peak")
	metaKeyEbur128LRA          = ffmpeg.GlobalCStr("lavfi.r128.LRA")
	metaKeyEbur128TargetThresh = ffmpeg.GlobalCStr("lavfi.r128.target_threshold")

	// Silence detection metadata keys (from silencedetect filter)
	// For mono audio these are lavfi.silence_start.1, lavfi.silence_end.1, lavfi.silence_duration.1
	metaKeySilenceStart    = ffmpeg.GlobalCStr("lavfi.silence_start")
	metaKeySilenceStart1   = ffmpeg.GlobalCStr("lavfi.silence_start.1")
	metaKeySilenceEnd      = ffmpeg.GlobalCStr("lavfi.silence_end")
	metaKeySilenceEnd1     = ffmpeg.GlobalCStr("lavfi.silence_end.1")
	metaKeySilenceDuration = ffmpeg.GlobalCStr("lavfi.silence_duration")
	metaKeySilenceDur1     = ffmpeg.GlobalCStr("lavfi.silence_duration.1")
)

// metadataAccumulators holds all accumulator variables for frame metadata extraction.
// Spectral stats (centroid, rolloff) are averaged across all frames.
// astats and ebur128 values are cumulative, so we keep the latest.
// baseMetadataAccumulators contains fields shared between input (Pass 1) and output (Pass 2) accumulators.
// Embedded in both metadataAccumulators and outputMetadataAccumulators to avoid duplication.
type baseMetadataAccumulators struct {
	// Spectral statistics from aspectralstats (averaged across frames)
	spectralMeanSum     float64
	spectralVarianceSum float64
	spectralCentroidSum float64
	spectralSpreadSum   float64
	spectralSkewnessSum float64
	spectralKurtosisSum float64
	spectralEntropySum  float64
	spectralFlatnessSum float64
	spectralCrestSum    float64
	spectralFluxSum     float64
	spectralSlopeSum    float64
	spectralDecreaseSum float64
	spectralRolloffSum  float64
	spectralFrameCount  int

	// astats measurements (cumulative - we keep latest values)
	astatsDynamicRange      float64
	astatsRMSLevel          float64
	astatsPeakLevel         float64
	astatsRMSTrough         float64
	astatsRMSPeak           float64
	astatsDCOffset          float64
	astatsFlatFactor        float64
	astatsCrestFactor       float64
	astatsZeroCrossingsRate float64
	astatsZeroCrossings     float64
	astatsMaxDifference     float64
	astatsMinDifference     float64
	astatsMeanDifference    float64
	astatsRMSDifference     float64
	astatsEntropy           float64
	astatsMinLevel          float64
	astatsMaxLevel          float64
	astatsNoiseFloor        float64
	astatsNoiseFloorCount   float64
	astatsBitDepth          float64
	astatsNumberOfSamples   float64
	astatsFound             bool
}

// metadataAccumulators holds accumulator variables for Pass 1 frame metadata extraction.
// Uses baseMetadataAccumulators for spectral and astats fields shared with output analysis.
type metadataAccumulators struct {
	// Embed shared spectral and astats fields
	baseMetadataAccumulators

	// ebur128 measurements (cumulative - we keep latest values)
	ebur128InputI   float64
	ebur128InputM   float64 // Momentary loudness (400ms window, updates per frame)
	ebur128InputS   float64 // Short-term loudness (3s window)
	ebur128InputTP  float64
	ebur128InputSP  float64 // Sample peak
	ebur128InputLRA float64
	ebur128Found    bool

	// Silence detection (collected across frames)
	// silencedetect sets lavfi.silence_start on first frame of silence,
	// then lavfi.silence_end and lavfi.silence_duration on first frame after silence ends
	silenceRegions      []SilenceRegion
	pendingSilenceStart float64 // Pending silence start timestamp (seconds)
	hasPendingSilence   bool    // Whether we have a pending silence start
}

// getFloatMetadata extracts a float value from the metadata dictionary
func getFloatMetadata(metadata *ffmpeg.AVDictionary, key *ffmpeg.CStr) (float64, bool) {
	if entry := ffmpeg.AVDictGet(metadata, key, nil, 0); entry != nil {
		if value, err := strconv.ParseFloat(entry.Value().String(), 64); err == nil {
			return value, true
		}
	}
	return 0.0, false
}

// linearRatioToDB converts a linear ratio (e.g., Crest_factor) to decibels.
// FFmpeg's astats Crest_factor is reported as a linear ratio (peak/RMS), not in dB.
func linearRatioToDB(ratio float64) float64 {
	if ratio <= 0 {
		return -120.0 // Floor for zero/negative values
	}
	return 20 * math.Log10(ratio)
}

// linearSampleToDBFS converts a linear sample value to dBFS.
// FFmpeg's astats Min_level and Max_level are reported as linear sample values
// (typically -1.0 to +1.0 for float audio, or integer sample values).
// We normalize assuming the value represents the fraction of full scale.
func linearSampleToDBFS(sample float64) float64 {
	absVal := math.Abs(sample)
	if absVal <= 0 {
		return -120.0 // Floor for zero values
	}
	// For normalized float audio (-1.0 to +1.0), this is direct
	// For integer sample values, we need to detect and normalize
	// If abs value > 1.0, assume integer samples and normalize to 16-bit range
	if absVal > 1.0 {
		// Likely integer sample value (e.g., from 16-bit audio: -32768 to 32767)
		absVal = absVal / 32768.0
	}
	if absVal > 1.0 {
		absVal = 1.0 // Clamp to 0 dBFS max
	}
	return 20 * math.Log10(absVal)
}

// spectralMetrics holds the 13 aspectralstats measurements extracted from FFmpeg metadata.
// These metrics characterise the frequency content of audio frames.
type spectralMetrics struct {
	Mean     float64 // Average spectral power
	Variance float64 // Spectral variance
	Centroid float64 // Spectral centroid (Hz) - where energy is concentrated
	Spread   float64 // Spectral spread (Hz) - bandwidth/fullness indicator
	Skewness float64 // Spectral asymmetry - positive=bright, negative=dark
	Kurtosis float64 // Spectral peakiness - tonal vs broadband content
	Entropy  float64 // Spectral randomness (0-1) - noise classification
	Flatness float64 // Noise vs tonal ratio (0-1) - low=tonal, high=noisy
	Crest    float64 // Spectral peak-to-RMS - transient indicator
	Flux     float64 // Frame-to-frame spectral change
	Slope    float64 // Spectral tilt - negative=more bass
	Decrease float64 // Average spectral decrease
	Rolloff  float64 // Spectral rolloff (Hz) - HF energy dropoff point
	Found    bool    // True if any spectral metric was extracted
}

// extractSpectralMetrics extracts all 13 aspectralstats measurements from FFmpeg metadata.
// Returns a spectralMetrics struct with Found=true if at least one metric was extracted.
func extractSpectralMetrics(metadata *ffmpeg.AVDictionary) spectralMetrics {
	var m spectralMetrics

	if value, ok := getFloatMetadata(metadata, metaKeySpectralMean); ok {
		m.Mean = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralVariance); ok {
		m.Variance = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralCentroid); ok {
		m.Centroid = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralSpread); ok {
		m.Spread = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralSkewness); ok {
		m.Skewness = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralKurtosis); ok {
		m.Kurtosis = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralEntropy); ok {
		m.Entropy = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralFlatness); ok {
		m.Flatness = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralCrest); ok {
		m.Crest = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralFlux); ok {
		m.Flux = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralSlope); ok {
		m.Slope = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralDecrease); ok {
		m.Decrease = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralRolloff); ok {
		m.Rolloff = value
		m.Found = true
	}

	return m
}

// extractIntervalFrameMetrics extracts per-frame metrics for interval accumulation.
// Only collects metrics that are valid per-window (aspectralstats, ebur128 windowed).
// Excludes astats which provides cumulative values, not per-interval.
func extractIntervalFrameMetrics(metadata *ffmpeg.AVDictionary) intervalFrameMetrics {
	var m intervalFrameMetrics

	// Peak level from astats (used for max tracking, which is valid per-interval)
	m.PeakLevel, _ = getFloatMetadata(metadata, metaKeyPeakLevel)

	// aspectralstats metrics (valid per-window measurements)
	spectral := extractSpectralMetrics(metadata)
	m.SpectralMean = spectral.Mean
	m.SpectralVariance = spectral.Variance
	m.SpectralCentroid = spectral.Centroid
	m.SpectralSpread = spectral.Spread
	m.SpectralSkewness = spectral.Skewness
	m.SpectralKurtosis = spectral.Kurtosis
	m.SpectralEntropy = spectral.Entropy
	m.SpectralFlatness = spectral.Flatness
	m.SpectralCrest = spectral.Crest
	m.SpectralFlux = spectral.Flux
	m.SpectralSlope = spectral.Slope
	m.SpectralDecrease = spectral.Decrease
	m.SpectralRolloff = spectral.Rolloff

	// ebur128 windowed measurements
	m.MomentaryLUFS, _ = getFloatMetadata(metadata, metaKeyEbur128M)
	m.ShortTermLUFS, _ = getFloatMetadata(metadata, metaKeyEbur128S)

	// ebur128 peak values are linear ratios, convert to dB
	if rawTP, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
		m.TruePeak = linearRatioToDB(rawTP)
	}
	if rawSP, ok := getFloatMetadata(metadata, metaKeyEbur128SamplePeak); ok {
		m.SamplePeak = linearRatioToDB(rawSP)
	}

	return m
}

// extractFrameMetadata extracts audio analysis metadata from a filtered frame.
// Updates accumulators with spectral, astats, and ebur128 measurements.
// Called from both the main processing loop and the flush loop.
func extractFrameMetadata(metadata *ffmpeg.AVDictionary, acc *metadataAccumulators) {
	if metadata == nil {
		return
	}

	// Extract all aspectralstats measurements (averaged across frames)
	// For mono audio, spectral stats are under channel .1
	spectral := extractSpectralMetrics(metadata)
	if spectral.Found {
		acc.spectralMeanSum += spectral.Mean
		acc.spectralVarianceSum += spectral.Variance
		acc.spectralCentroidSum += spectral.Centroid
		acc.spectralSpreadSum += spectral.Spread
		acc.spectralSkewnessSum += spectral.Skewness
		acc.spectralKurtosisSum += spectral.Kurtosis
		acc.spectralEntropySum += spectral.Entropy
		acc.spectralFlatnessSum += spectral.Flatness
		acc.spectralCrestSum += spectral.Crest
		acc.spectralFluxSum += spectral.Flux
		acc.spectralSlopeSum += spectral.Slope
		acc.spectralDecreaseSum += spectral.Decrease
		acc.spectralRolloffSum += spectral.Rolloff
		acc.spectralFrameCount++
	}

	// Extract astats measurements (cumulative, so we keep the latest)
	// For mono audio, stats are under channel .1
	if value, ok := getFloatMetadata(metadata, metaKeyDynamicRange); ok {
		acc.astatsDynamicRange = value
		acc.astatsFound = true
	}

	if value, ok := getFloatMetadata(metadata, metaKeyRMSLevel); ok {
		acc.astatsRMSLevel = value
	}

	if value, ok := getFloatMetadata(metadata, metaKeyPeakLevel); ok {
		acc.astatsPeakLevel = value
	}

	// Extract RMS_trough - RMS level of quietest segments (best noise floor indicator for speech)
	// In speech audio, quiet inter-word periods contain primarily ambient/electronic noise
	if value, ok := getFloatMetadata(metadata, metaKeyRMSTrough); ok {
		acc.astatsRMSTrough = value
	}

	// Extract RMS_peak - RMS level of loudest segments
	if value, ok := getFloatMetadata(metadata, metaKeyRMSPeak); ok {
		acc.astatsRMSPeak = value
	}

	// Extract DC_offset - mean amplitude displacement from zero
	// High values indicate DC bias that should be removed before processing
	if value, ok := getFloatMetadata(metadata, metaKeyDCOffset); ok {
		acc.astatsDCOffset = value
	}

	// Extract Flat_factor - consecutive samples at peak levels (indicates clipping)
	// High values suggest pre-existing limiting or clipping damage
	if value, ok := getFloatMetadata(metadata, metaKeyFlatFactor); ok {
		acc.astatsFlatFactor = value
	}

	// Extract Crest_factor - FFmpeg reports as linear ratio (peak/RMS), convert to dB
	// High values indicate impulsive/dynamic content, low values indicate compressed/limited audio
	if value, ok := getFloatMetadata(metadata, metaKeyCrestFactor); ok {
		acc.astatsCrestFactor = linearRatioToDB(value)
	}

	// Extract Zero_crossings_rate - rate of zero crossings per sample
	// Low ZCR = bass-heavy/sustained tones, High ZCR = noise/sibilance
	if value, ok := getFloatMetadata(metadata, metaKeyZeroCrossingsRate); ok {
		acc.astatsZeroCrossingsRate = value
	}

	// Extract Zero_crossings - total number of zero crossings
	if value, ok := getFloatMetadata(metadata, metaKeyZeroCrossings); ok {
		acc.astatsZeroCrossings = value
	}

	// Extract Max_difference - largest sample-to-sample change
	// High values indicate impulsive sounds (clicks, pops) - useful for adeclick tuning
	if value, ok := getFloatMetadata(metadata, metaKeyMaxDifference); ok {
		acc.astatsMaxDifference = value
	}

	// Extract Min_difference - smallest sample-to-sample change
	if value, ok := getFloatMetadata(metadata, metaKeyMinDifference); ok {
		acc.astatsMinDifference = value
	}

	// Extract Mean_difference - average sample-to-sample change
	if value, ok := getFloatMetadata(metadata, metaKeyMeanDifference); ok {
		acc.astatsMeanDifference = value
	}

	// Extract RMS_difference - RMS of sample-to-sample changes
	if value, ok := getFloatMetadata(metadata, metaKeyRMSDifference); ok {
		acc.astatsRMSDifference = value
	}

	// Extract Entropy - signal randomness (1.0 = white noise, lower = more structured)
	if value, ok := getFloatMetadata(metadata, metaKeyEntropy); ok {
		acc.astatsEntropy = value
	}

	// Extract Min_level and Max_level - FFmpeg reports as linear sample values, convert to dBFS
	if value, ok := getFloatMetadata(metadata, metaKeyMinLevel); ok {
		acc.astatsMinLevel = linearSampleToDBFS(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMaxLevel); ok {
		acc.astatsMaxLevel = linearSampleToDBFS(value)
	}

	// Extract Noise_floor - FFmpeg's own noise floor estimate (dBFS)
	// Very useful for adaptive gate/noise reduction thresholds
	if value, ok := getFloatMetadata(metadata, metaKeyNoiseFloor); ok {
		acc.astatsNoiseFloor = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyNoiseFloorCount); ok {
		acc.astatsNoiseFloorCount = value
	}

	// Extract Bit_depth - effective bit depth of audio
	if value, ok := getFloatMetadata(metadata, metaKeyBitDepth); ok {
		acc.astatsBitDepth = value
	}

	// Extract Number_of_samples - total samples processed
	if value, ok := getFloatMetadata(metadata, metaKeyNumberOfSamples); ok {
		acc.astatsNumberOfSamples = value
	}

	// Extract ebur128 measurements (cumulative loudness analysis)
	// ebur128 provides: M (momentary 400ms), S (short-term 3s), I (integrated), LRA, sample_peak, true_peak
	// We need these for loudness normalization and interval-based analysis
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128I); ok {
		acc.ebur128InputI = value
		acc.ebur128Found = true
	}

	// Momentary loudness (400ms window) - useful for interval-based silence detection
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128M); ok {
		acc.ebur128InputM = value
	}

	// Short-term loudness (3s window)
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128S); ok {
		acc.ebur128InputS = value
	}

	if value, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
		// ebur128 reports true_peak as linear ratio, convert to dBTP
		// dBTP = 20 * log10(linear)
		if value > 0 {
			acc.ebur128InputTP = 20 * math.Log10(value)
		} else {
			acc.ebur128InputTP = -120.0 // Floor for zero/negative values
		}
	}

	// Sample peak (linear ratio, convert to dB)
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128SamplePeak); ok {
		if value > 0 {
			acc.ebur128InputSP = 20 * math.Log10(value)
		} else {
			acc.ebur128InputSP = -120.0
		}
	}

	if value, ok := getFloatMetadata(metadata, metaKeyEbur128LRA); ok {
		acc.ebur128InputLRA = value
	}

	// Extract silence detection metadata
	// silencedetect sets lavfi.silence_start on the first frame of a silence region,
	// then lavfi.silence_end and lavfi.silence_duration on the first frame after silence ends.
	// For mono audio, these may be suffixed with .1
	var silenceStart float64
	var hasSilenceStart bool
	if value, ok := getFloatMetadata(metadata, metaKeySilenceStart); ok {
		silenceStart = value
		hasSilenceStart = true
	} else if value, ok := getFloatMetadata(metadata, metaKeySilenceStart1); ok {
		silenceStart = value
		hasSilenceStart = true
	}

	if hasSilenceStart {
		acc.pendingSilenceStart = silenceStart
		acc.hasPendingSilence = true
	}

	// Check for silence end - this completes a silence region
	var silenceEnd, silenceDuration float64
	var hasSilenceEnd bool
	if value, ok := getFloatMetadata(metadata, metaKeySilenceEnd); ok {
		silenceEnd = value
		hasSilenceEnd = true
	} else if value, ok := getFloatMetadata(metadata, metaKeySilenceEnd1); ok {
		silenceEnd = value
		hasSilenceEnd = true
	}

	if hasSilenceEnd {
		// Get duration - try both keys
		if value, ok := getFloatMetadata(metadata, metaKeySilenceDuration); ok {
			silenceDuration = value
		} else if value, ok := getFloatMetadata(metadata, metaKeySilenceDur1); ok {
			silenceDuration = value
		}

		// Record the completed silence region
		if acc.hasPendingSilence {
			region := SilenceRegion{
				Start:    time.Duration(acc.pendingSilenceStart * float64(time.Second)),
				End:      time.Duration(silenceEnd * float64(time.Second)),
				Duration: time.Duration(silenceDuration * float64(time.Second)),
			}
			acc.silenceRegions = append(acc.silenceRegions, region)
			acc.hasPendingSilence = false
		}
	}
}

// AudioMeasurements contains the measurements from Pass 1 analysis.
// Uses ebur128 (LUFS/LRA), astats (dynamic range/noise floor), and aspectralstats (spectral analysis).
// Silence detection is performed in Go using 250ms interval sampling for improved accuracy.
// BaseMeasurements contains fields shared between input (Pass 1) and output (Pass 2) measurements.
// Embedded in both AudioMeasurements and OutputMeasurements to avoid duplication.
type BaseMeasurements struct {
	// Spectral analysis from aspectralstats (all measurements averaged across frames)
	SpectralMean     float64 `json:"spectral_mean"`     // Mean spectral magnitude
	SpectralVariance float64 `json:"spectral_variance"` // Spectral magnitude variance
	SpectralCentroid float64 `json:"spectral_centroid"` // Spectral centroid (Hz) - where energy is concentrated
	SpectralSpread   float64 `json:"spectral_spread"`   // Spectral spread (Hz) - bandwidth/fullness indicator
	SpectralSkewness float64 `json:"spectral_skewness"` // Spectral asymmetry - positive=bright, negative=dark
	SpectralKurtosis float64 `json:"spectral_kurtosis"` // Spectral peakiness - tonal vs broadband content
	SpectralEntropy  float64 `json:"spectral_entropy"`  // Spectral randomness (0-1) - noise classification
	SpectralFlatness float64 `json:"spectral_flatness"` // Noise vs tonal ratio (0-1) - low=tonal, high=noisy
	SpectralCrest    float64 `json:"spectral_crest"`    // Spectral peak-to-RMS - transient indicator
	SpectralFlux     float64 `json:"spectral_flux"`     // Frame-to-frame spectral change
	SpectralSlope    float64 `json:"spectral_slope"`    // Spectral tilt - negative=more bass
	SpectralDecrease float64 `json:"spectral_decrease"` // Average spectral decrease
	SpectralRolloff  float64 `json:"spectral_rolloff"`  // Spectral rolloff (Hz) - HF energy dropoff point

	// Time-domain statistics from astats
	DynamicRange float64 `json:"dynamic_range"` // Measured dynamic range (dB)
	RMSLevel     float64 `json:"rms_level"`     // Overall RMS level (dBFS)
	PeakLevel    float64 `json:"peak_level"`    // Overall peak level (dBFS)
	RMSTrough    float64 `json:"rms_trough"`    // RMS level of quietest segments (dBFS)
	RMSPeak      float64 `json:"rms_peak"`      // RMS level of loudest segments (dBFS)

	// Additional astats measurements
	DCOffset          float64 `json:"dc_offset"`           // Mean amplitude displacement from zero
	FlatFactor        float64 `json:"flat_factor"`         // Consecutive samples at peak (clipping indicator)
	CrestFactor       float64 `json:"crest_factor"`        // Peak-to-RMS ratio in dB (converted from linear)
	ZeroCrossingsRate float64 `json:"zero_crossings_rate"` // Zero crossing rate (low=bass, high=noise/sibilance)
	ZeroCrossings     float64 `json:"zero_crossings"`      // Total zero crossings
	MaxDifference     float64 `json:"max_difference"`      // Largest sample-to-sample change (clicks/pops indicator)
	MinDifference     float64 `json:"min_difference"`      // Smallest sample-to-sample change
	MeanDifference    float64 `json:"mean_difference"`     // Average sample-to-sample change
	RMSDifference     float64 `json:"rms_difference"`      // RMS of sample-to-sample changes
	Entropy           float64 `json:"entropy"`             // Signal randomness (1.0 = white noise, lower = structured)
	MinLevel          float64 `json:"min_level"`           // dBFS, minimum sample level (converted from linear)
	MaxLevel          float64 `json:"max_level"`           // dBFS, maximum sample level (converted from linear)
	AstatsNoiseFloor  float64 `json:"astats_noise_floor"`  // FFmpeg astats noise floor estimate (dBFS)
	NoiseFloorCount   float64 `json:"noise_floor_count"`   // Number of samples in noise floor measurement
	BitDepth          float64 `json:"bit_depth"`           // Effective bit depth
	NumberOfSamples   float64 `json:"number_of_samples"`   // Total samples processed

	// ebur128 momentary/short-term loudness
	MomentaryLoudness float64 `json:"momentary_loudness"`  // Momentary loudness (400ms window, LUFS)
	ShortTermLoudness float64 `json:"short_term_loudness"` // Short-term loudness (3s window, LUFS)
	SamplePeak        float64 `json:"sample_peak"`         // Sample peak (dBFS)
}

// AudioMeasurements contains the measurements from Pass 1 analysis.
// Uses ebur128 (LUFS/LRA), astats (dynamic range/noise floor), and aspectralstats (spectral analysis).
// Silence detection is performed in Go using 250ms interval sampling for improved accuracy.
type AudioMeasurements struct {
	// Embed shared measurement fields
	BaseMeasurements

	// Input-specific loudness measurements from ebur128
	InputI       float64 `json:"input_i"`       // Integrated loudness (LUFS)
	InputTP      float64 `json:"input_tp"`      // True peak (dBTP)
	InputLRA     float64 `json:"input_lra"`     // Loudness range (LU)
	InputThresh  float64 `json:"input_thresh"`  // Threshold level
	TargetOffset float64 `json:"target_offset"` // Offset for normalization
	NoiseFloor   float64 `json:"noise_floor"`   // Measured noise floor from astats (dBFS)

	// Adaptive silence detection thresholds (derived from interval sampling)
	PreScanNoiseFloor  float64 `json:"prescan_noise_floor"`  // Noise floor estimated from first 15% of intervals (dBFS)
	SilenceDetectLevel float64 `json:"silence_detect_level"` // Adaptive silencedetect threshold used (dBFS)

	// Silence detection results (derived from interval sampling)
	SilenceRegions []SilenceRegion `json:"silence_regions,omitempty"` // Detected silence regions

	// 250ms interval samples for data-driven silence candidate detection
	IntervalSamples []IntervalSample `json:"interval_samples,omitempty"` // Per-interval measurements

	// Scored silence candidates (for debugging/reporting)
	SilenceCandidates []SilenceCandidateMetrics `json:"silence_candidates,omitempty"` // All evaluated candidates with scores

	// Noise profile extracted from best silence candidate
	NoiseProfile *NoiseProfile `json:"noise_profile,omitempty"` // nil if extraction failed

	// Derived suggestions for Pass 2 adaptive processing
	SuggestedGateThreshold float64 `json:"suggested_gate_threshold"` // Suggested gate threshold (linear amplitude)
	NoiseReductionHeadroom float64 `json:"noise_reduction_headroom"` // dB gap between noise and quiet speech
}

// SilenceAnalysis contains measurements from a silence region.
// Used for comparing noise characteristics between input and output.
type SilenceAnalysis struct {
	Start       time.Duration `json:"start"`        // Start time of silence region
	Duration    time.Duration `json:"duration"`     // Duration of silence region
	NoiseFloor  float64       `json:"noise_floor"`  // dBFS, RMS level of silence (average noise)
	PeakLevel   float64       `json:"peak_level"`   // dBFS, peak level in silence
	CrestFactor float64       `json:"crest_factor"` // Peak - RMS in dB
	Entropy     float64       `json:"entropy"`      // Signal randomness (1.0 = white noise, lower = tonal)
}

// OutputMeasurements contains the measurements from Pass 2 output analysis.
// Uses BaseMeasurements for comparison with AudioMeasurements.
// Does not include silence detection or noise profile fields (those are input-only).
type OutputMeasurements struct {
	// Embed shared measurement fields
	BaseMeasurements

	// Output-specific loudness measurements from ebur128
	OutputI      float64 `json:"output_i"`      // Integrated loudness (LUFS)
	OutputTP     float64 `json:"output_tp"`     // True peak (dBTP)
	OutputLRA    float64 `json:"output_lra"`    // Loudness range (LU)
	OutputThresh float64 `json:"output_thresh"` // Gating threshold (LUFS) - for loudnorm
	TargetOffset float64 `json:"target_offset"` // Pre-limiter offset (dB) - from loudnorm measurement

	// Loudnorm measurement from Pass 2 analysis chain
	// These come from loudnorm's first pass (measurement mode, without linear=true)
	// and are used for the application pass in Pass 3
	LoudnormInputI       float64 `json:"loudnorm_input_i"`       // Loudnorm's measured integrated loudness (LUFS)
	LoudnormInputTP      float64 `json:"loudnorm_input_tp"`      // Loudnorm's measured true peak (dBTP)
	LoudnormInputLRA     float64 `json:"loudnorm_input_lra"`     // Loudnorm's measured loudness range (LU)
	LoudnormInputThresh  float64 `json:"loudnorm_input_thresh"`  // Loudnorm's measured threshold (LUFS)
	LoudnormTargetOffset float64 `json:"loudnorm_target_offset"` // Loudnorm's calculated offset for second pass
	LoudnormMeasured     bool    `json:"loudnorm_measured"`      // True if loudnorm measurement was captured

	// Silence region analysis (same region as Pass 1, for noise reduction comparison)
	SilenceSample *SilenceAnalysis `json:"silence_sample,omitempty"` // Measurements from same silence region
}

// outputMetadataAccumulators holds accumulator variables for Pass 2 output measurement extraction.
// Mirrors metadataAccumulators but without silence detection fields.
// outputMetadataAccumulators holds accumulator variables for Pass 2 output measurement extraction.
// Uses baseMetadataAccumulators for spectral and astats fields shared with input analysis.
type outputMetadataAccumulators struct {
	// Embed shared spectral and astats fields
	baseMetadataAccumulators

	// ebur128 measurements (cumulative - we keep latest values)
	ebur128OutputI      float64
	ebur128OutputM      float64 // Momentary loudness
	ebur128OutputS      float64 // Short-term loudness
	ebur128OutputTP     float64
	ebur128OutputSP     float64 // Sample peak
	ebur128OutputLRA    float64
	ebur128OutputThresh float64 // Gating threshold for loudnorm
	ebur128Found        bool
}

// extractOutputFrameMetadata extracts audio analysis metadata from a Pass 2 filtered frame.
// Updates accumulators with spectral, astats, and ebur128 measurements.
// This is the output analysis counterpart to extractFrameMetadata.
func extractOutputFrameMetadata(metadata *ffmpeg.AVDictionary, acc *outputMetadataAccumulators) {
	if metadata == nil {
		return
	}

	// Extract all aspectralstats measurements (averaged across frames)
	spectral := extractSpectralMetrics(metadata)
	if spectral.Found {
		acc.spectralMeanSum += spectral.Mean
		acc.spectralVarianceSum += spectral.Variance
		acc.spectralCentroidSum += spectral.Centroid
		acc.spectralSpreadSum += spectral.Spread
		acc.spectralSkewnessSum += spectral.Skewness
		acc.spectralKurtosisSum += spectral.Kurtosis
		acc.spectralEntropySum += spectral.Entropy
		acc.spectralFlatnessSum += spectral.Flatness
		acc.spectralCrestSum += spectral.Crest
		acc.spectralFluxSum += spectral.Flux
		acc.spectralSlopeSum += spectral.Slope
		acc.spectralDecreaseSum += spectral.Decrease
		acc.spectralRolloffSum += spectral.Rolloff
		acc.spectralFrameCount++
	}

	// Extract astats measurements (cumulative, so we keep the latest)
	if value, ok := getFloatMetadata(metadata, metaKeyDynamicRange); ok {
		acc.astatsDynamicRange = value
		acc.astatsFound = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSLevel); ok {
		acc.astatsRMSLevel = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyPeakLevel); ok {
		acc.astatsPeakLevel = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSTrough); ok {
		acc.astatsRMSTrough = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSPeak); ok {
		acc.astatsRMSPeak = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyDCOffset); ok {
		acc.astatsDCOffset = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyFlatFactor); ok {
		acc.astatsFlatFactor = value
	}
	// CrestFactor: FFmpeg reports as linear ratio (peak/RMS), convert to dB
	if value, ok := getFloatMetadata(metadata, metaKeyCrestFactor); ok {
		acc.astatsCrestFactor = linearRatioToDB(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyZeroCrossingsRate); ok {
		acc.astatsZeroCrossingsRate = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyZeroCrossings); ok {
		acc.astatsZeroCrossings = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMaxDifference); ok {
		acc.astatsMaxDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMinDifference); ok {
		acc.astatsMinDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMeanDifference); ok {
		acc.astatsMeanDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSDifference); ok {
		acc.astatsRMSDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEntropy); ok {
		acc.astatsEntropy = value
	}
	// MinLevel/MaxLevel: FFmpeg reports as linear sample values, convert to dBFS
	if value, ok := getFloatMetadata(metadata, metaKeyMinLevel); ok {
		acc.astatsMinLevel = linearSampleToDBFS(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMaxLevel); ok {
		acc.astatsMaxLevel = linearSampleToDBFS(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyNoiseFloor); ok {
		acc.astatsNoiseFloor = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyNoiseFloorCount); ok {
		acc.astatsNoiseFloorCount = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyBitDepth); ok {
		acc.astatsBitDepth = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyNumberOfSamples); ok {
		acc.astatsNumberOfSamples = value
	}

	// Extract ebur128 measurements
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128I); ok {
		acc.ebur128OutputI = value
		acc.ebur128Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128M); ok {
		acc.ebur128OutputM = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128S); ok {
		acc.ebur128OutputS = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
		// ebur128 reports true_peak as linear ratio, convert to dBTP
		// dBTP = 20 * log10(linear)
		if value > 0 {
			acc.ebur128OutputTP = 20 * math.Log10(value)
		} else {
			acc.ebur128OutputTP = -120.0 // Floor for zero/negative values
		}
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128SamplePeak); ok {
		if value > 0 {
			acc.ebur128OutputSP = 20 * math.Log10(value)
		} else {
			acc.ebur128OutputSP = -120.0
		}
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128LRA); ok {
		acc.ebur128OutputLRA = value
	}
	// Gating threshold (for loudnorm two-pass mode)
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128TargetThresh); ok {
		acc.ebur128OutputThresh = value
	}
}

// finalizeOutputMeasurements converts accumulated values to OutputMeasurements struct.
// Returns nil if no measurements were captured.
func finalizeOutputMeasurements(acc *outputMetadataAccumulators) *OutputMeasurements {
	if !acc.ebur128Found && !acc.astatsFound && acc.spectralFrameCount == 0 {
		return nil // No measurements captured
	}

	m := &OutputMeasurements{
		BaseMeasurements: BaseMeasurements{
			// ebur128 momentary/short-term loudness
			MomentaryLoudness: acc.ebur128OutputM,
			ShortTermLoudness: acc.ebur128OutputS,
			SamplePeak:        acc.ebur128OutputSP,

			// astats time-domain measurements
			DynamicRange:      acc.astatsDynamicRange,
			RMSLevel:          acc.astatsRMSLevel,
			PeakLevel:         acc.astatsPeakLevel,
			RMSTrough:         acc.astatsRMSTrough,
			RMSPeak:           acc.astatsRMSPeak,
			DCOffset:          acc.astatsDCOffset,
			FlatFactor:        acc.astatsFlatFactor,
			CrestFactor:       acc.astatsCrestFactor,
			ZeroCrossingsRate: acc.astatsZeroCrossingsRate,
			ZeroCrossings:     acc.astatsZeroCrossings,
			MaxDifference:     acc.astatsMaxDifference,
			MinDifference:     acc.astatsMinDifference,
			MeanDifference:    acc.astatsMeanDifference,
			RMSDifference:     acc.astatsRMSDifference,
			Entropy:           acc.astatsEntropy,
			MinLevel:          acc.astatsMinLevel,
			MaxLevel:          acc.astatsMaxLevel,
			AstatsNoiseFloor:  acc.astatsNoiseFloor,
			NoiseFloorCount:   acc.astatsNoiseFloorCount,
			BitDepth:          acc.astatsBitDepth,
			NumberOfSamples:   acc.astatsNumberOfSamples,
		},
		// Output-specific loudness measurements
		OutputI:      acc.ebur128OutputI,
		OutputTP:     acc.ebur128OutputTP,
		OutputLRA:    acc.ebur128OutputLRA,
		OutputThresh: acc.ebur128OutputThresh,
		TargetOffset: 0.0, // Will be calculated in Pass 3
	}

	// If ebur128 target_threshold metadata is missing, calculate it manually
	// according to EBU R128 standard: gating threshold = integrated loudness - 10 LU
	if m.OutputThresh == 0.0 && m.OutputI != 0.0 {
		m.OutputThresh = m.OutputI - 10.0
	}

	// Calculate average spectral statistics from aspectralstats
	if acc.spectralFrameCount > 0 {
		frameCount := float64(acc.spectralFrameCount)
		m.SpectralMean = acc.spectralMeanSum / frameCount
		m.SpectralVariance = acc.spectralVarianceSum / frameCount
		m.SpectralCentroid = acc.spectralCentroidSum / frameCount
		m.SpectralSpread = acc.spectralSpreadSum / frameCount
		m.SpectralSkewness = acc.spectralSkewnessSum / frameCount
		m.SpectralKurtosis = acc.spectralKurtosisSum / frameCount
		m.SpectralEntropy = acc.spectralEntropySum / frameCount
		m.SpectralFlatness = acc.spectralFlatnessSum / frameCount
		m.SpectralCrest = acc.spectralCrestSum / frameCount
		m.SpectralFlux = acc.spectralFluxSum / frameCount
		m.SpectralSlope = acc.spectralSlopeSum / frameCount
		m.SpectralDecrease = acc.spectralDecreaseSum / frameCount
		m.SpectralRolloff = acc.spectralRolloffSum / frameCount
	}

	return m
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

// AnalyzeAudio performs Pass 1: ebur128 + astats + aspectralstats analysis to get measurements
// This is required for adaptive processing in Pass 2.
//
// Implementation note: ebur128 and astats write measurements to frame metadata with lavfi.r128.*
// and lavfi.astats.Overall.* keys respectively. We extract these from the last processed frames.
//
// The noise floor and silence threshold are computed from interval data AFTER the full pass,
// eliminating the need for a separate pre-scan phase.
func AnalyzeAudio(filename string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements)) (*AudioMeasurements, error) {
	// Default fallback threshold if interval analysis yields insufficient data
	const defaultNoiseFloor = -50.0

	// Open audio file
	reader, metadata, err := audio.OpenAudioFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer reader.Close()

	// Get total duration for progress calculation
	totalDuration := metadata.Duration
	sampleRate := float64(metadata.SampleRate)

	// Calculate total frames estimate (duration * sample_rate / samples_per_frame)
	// For FLAC, typical frame size is 4096 samples
	samplesPerFrame := 4096.0
	estimatedTotalFrames := (totalDuration * sampleRate) / samplesPerFrame

	// Create filter graph for Pass 1 analysis (astats + aspectralstats + ebur128)
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := createAnalysisFilterGraph(
		reader.GetDecoderContext(),
		config,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create filter graph: %w", err)
	}
	// NOTE: filterGraph is explicitly freed at the end (not in defer) to ensure
	// measurements are output via av_log before we try to extract them.
	// On error paths, we still free it immediately
	var filterFreed bool
	defer func() {
		if !filterFreed && filterGraph != nil {
			ffmpeg.AVFilterGraphFree(&filterGraph)
		}
	}()

	// Process all frames through the filter
	filteredFrame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&filteredFrame)

	// Track frames for periodic progress updates
	frameCount := 0
	updateInterval := 100 // Send progress update every N frames
	currentLevel := 0.0

	// Accumulators for frame metadata extraction
	acc := &metadataAccumulators{}

	// Interval sampling for silence detection (250ms windows)
	const intervalDuration = 250 * time.Millisecond
	var intervals []IntervalSample
	var intervalAcc intervalAccumulator
	intervalAcc.reset() // Initialize with proper defaults
	var intervalStartTime time.Duration
	var lastFrameTime time.Duration // Track for end-of-file handling

	// Track input frame time (before filter graph, which upsamples to 192kHz)
	var inputSamplesProcessed int64
	inputSampleRate := float64(reader.GetDecoderContext().SampleRate())

	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			return nil, fmt.Errorf("failed to read frame: %w", err)
		}
		if frame == nil {
			break // EOF
		}

		// Calculate audio level from frame
		currentLevel = calculateFrameLevel(frame)

		// Calculate input frame time based on samples processed (before filter graph upsampling)
		inputFrameTime := time.Duration(float64(inputSamplesProcessed) / inputSampleRate * float64(time.Second))
		inputSamplesProcessed += int64(frame.NbSamples())
		lastFrameTime = inputFrameTime

		// Accumulate RMS and peak from INPUT frame (before filter graph which upsamples to 192kHz)
		// This gives accurate RMS and peak values matching the original audio levels
		intervalAcc.addFrameRMSAndPeak(frame)

		// Check if interval complete (250ms elapsed) based on input time
		if inputFrameTime-intervalStartTime >= intervalDuration {
			// Finalize and store completed interval
			intervals = append(intervals, intervalAcc.finalize(intervalStartTime))
			intervalStartTime = inputFrameTime
			intervalAcc.reset()
		}

		// Send periodic progress updates based on frame count
		if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
			progress := float64(frameCount) / estimatedTotalFrames
			if progress > 1.0 {
				progress = 1.0
			}
			progressCallback(1, "Analyzing", progress, currentLevel, nil)
		}
		frameCount++

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			return nil, fmt.Errorf("failed to add frame to filter: %w", err)
		}

		// Pull filtered frames and extract spectral metadata
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				return nil, fmt.Errorf("failed to get filtered frame: %w", err)
			}

			// Extract measurements from frame metadata (whole-file accumulators)
			extractFrameMetadata(filteredFrame.Metadata(), acc)

			// Also accumulate into current interval for per-interval spectral data
			// Filtered frames roughly correspond to input timing (just at higher sample rate)
			intervalAcc.add(extractIntervalFrameMetrics(filteredFrame.Metadata()))

			ffmpeg.AVFrameUnref(filteredFrame)
		}
	}

	// Flush the filter graph
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err != nil {
		return nil, fmt.Errorf("failed to flush filter: %w", err)
	}

	// Pull remaining frames
	for {
		if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return nil, fmt.Errorf("failed to get filtered frame: %w", err)
		}

		// Extract measurements from remaining frames
		extractFrameMetadata(filteredFrame.Metadata(), acc)

		// Also accumulate into current interval for per-interval spectral data
		intervalAcc.add(extractIntervalFrameMetrics(filteredFrame.Metadata()))

		ffmpeg.AVFrameUnref(filteredFrame)
	}

	// Finalize any remaining partial interval (if it has data)
	if intervalAcc.rawSampleCount > 0 {
		intervals = append(intervals, intervalAcc.finalize(intervalStartTime))
	}

	// Note: We intentionally discard partial intervals with no data
	_ = lastFrameTime // Silence unused variable warning (used for debugging if needed)

	// Free the filter graph
	ffmpeg.AVFilterGraphFree(&filterGraph)
	filterFreed = true

	// Estimate noise floor and silence threshold from interval data
	// This replaces the previous separate pre-scan pass
	noiseFloorEstimate, silenceThreshold, ok := estimateNoiseFloorAndThreshold(intervals)
	if !ok {
		// Fallback if insufficient interval data (very short recordings)
		noiseFloorEstimate = defaultNoiseFloor
		silenceThreshold = calculateAdaptiveSilenceThreshold(defaultNoiseFloor)
	}

	// Create measurements struct and populate from accumulators
	measurements := &AudioMeasurements{
		// Noise floor estimated from interval data (replaces pre-scan)
		PreScanNoiseFloor:  noiseFloorEstimate,
		SilenceDetectLevel: silenceThreshold,
	}

	// Populate ebur128 loudness measurements
	if acc.ebur128Found {
		measurements.InputI = acc.ebur128InputI
		measurements.InputTP = acc.ebur128InputTP
		measurements.InputLRA = acc.ebur128InputLRA
		// Calculate threshold based on integrated loudness (ebur128 doesn't provide this directly)
		// Threshold is typically around 10 LU below the integrated loudness
		measurements.InputThresh = acc.ebur128InputI - 10.0
		// Target offset for normalization (difference between measured and target)
		measurements.TargetOffset = config.TargetI - acc.ebur128InputI
	} else {
		return nil, fmt.Errorf("ebur128 measurements not found in metadata for file: %s", filename)
	}

	// Calculate average spectral statistics from aspectralstats
	if acc.spectralFrameCount > 0 {
		frameCount := float64(acc.spectralFrameCount)
		measurements.SpectralMean = acc.spectralMeanSum / frameCount
		measurements.SpectralVariance = acc.spectralVarianceSum / frameCount
		measurements.SpectralCentroid = acc.spectralCentroidSum / frameCount
		measurements.SpectralSpread = acc.spectralSpreadSum / frameCount
		measurements.SpectralSkewness = acc.spectralSkewnessSum / frameCount
		measurements.SpectralKurtosis = acc.spectralKurtosisSum / frameCount
		measurements.SpectralEntropy = acc.spectralEntropySum / frameCount
		measurements.SpectralFlatness = acc.spectralFlatnessSum / frameCount
		measurements.SpectralCrest = acc.spectralCrestSum / frameCount
		measurements.SpectralFlux = acc.spectralFluxSum / frameCount
		measurements.SpectralSlope = acc.spectralSlopeSum / frameCount
		measurements.SpectralDecrease = acc.spectralDecreaseSum / frameCount
		measurements.SpectralRolloff = acc.spectralRolloffSum / frameCount
	}

	// Store astats measurements (if captured)
	if acc.astatsFound {
		measurements.DynamicRange = acc.astatsDynamicRange
		measurements.RMSLevel = acc.astatsRMSLevel
		measurements.PeakLevel = acc.astatsPeakLevel
		measurements.RMSTrough = acc.astatsRMSTrough
		measurements.RMSPeak = acc.astatsRMSPeak

		// Additional astats measurements for adaptive processing
		measurements.DCOffset = acc.astatsDCOffset
		measurements.FlatFactor = acc.astatsFlatFactor
		measurements.CrestFactor = acc.astatsCrestFactor
		measurements.ZeroCrossingsRate = acc.astatsZeroCrossingsRate
		measurements.ZeroCrossings = acc.astatsZeroCrossings
		measurements.MaxDifference = acc.astatsMaxDifference
		measurements.MinDifference = acc.astatsMinDifference
		measurements.MeanDifference = acc.astatsMeanDifference
		measurements.RMSDifference = acc.astatsRMSDifference
		measurements.Entropy = acc.astatsEntropy
		measurements.MinLevel = acc.astatsMinLevel
		measurements.MaxLevel = acc.astatsMaxLevel
		measurements.AstatsNoiseFloor = acc.astatsNoiseFloor
		measurements.NoiseFloorCount = acc.astatsNoiseFloorCount
		measurements.BitDepth = acc.astatsBitDepth
		measurements.NumberOfSamples = acc.astatsNumberOfSamples
	}

	// Store ebur128 momentary/short-term loudness
	if acc.ebur128Found {
		measurements.MomentaryLoudness = acc.ebur128InputM
		measurements.ShortTermLoudness = acc.ebur128InputS
		measurements.SamplePeak = acc.ebur128InputSP
	}

	// Derive noise floor using three-tier approach based on audio engineering best practices:
	// Tier 1 (Primary): RMS_trough from astats - most accurate
	//   - Measures RMS level during quietest segments (inter-word silence in speech)
	//   - These quiet periods contain primarily room noise, HVAC, electronics noise
	//   - Directly represents the actual noise floor of the recording environment
	// Tier 2 (Secondary): Estimate from RMS_level - 15dB
	//   - Based on typical speech crest factor where quiet segments are 12-18dB below average RMS
	//   - Reasonable approximation when RMS_trough unavailable
	// Tier 3 (Tertiary): Estimate from ebur128 InputThresh with loudness-based offset
	//   - Fallback for when astats data is completely unavailable
	//   - Uses integrated loudness to infer likely noise floor characteristics

	if acc.astatsRMSTrough != 0 && !math.IsInf(acc.astatsRMSTrough, -1) {
		// Tier 1: Use RMS_trough (best - actual measurement of quiet segments)
		measurements.NoiseFloor = acc.astatsRMSTrough
	} else if acc.astatsRMSLevel != 0 && !math.IsInf(acc.astatsRMSLevel, -1) {
		// Tier 2: Estimate from overall RMS level
		// Typical speech has quiet segments 12-18dB below average RMS; use 15dB as balanced estimate
		measurements.NoiseFloor = acc.astatsRMSLevel - 15.0
	} else {
		// Tier 3: Estimate from ebur128 integrated loudness threshold
		// Louder recordings typically have better SNR (lower relative noise floor)
		var noiseFloorOffset float64
		if measurements.InputI > -20 {
			noiseFloorOffset = 18.0 // Professional: very low noise floor
		} else if measurements.InputI > -30 {
			noiseFloorOffset = 12.0 // Typical podcast: moderate noise floor
		} else {
			noiseFloorOffset = 8.0 // Quiet source: higher relative noise
		}
		measurements.NoiseFloor = measurements.InputThresh - noiseFloorOffset
	}

	// Safety clamp: -90dB (digital silence) to -30dB (very noisy environment)
	// Prevents extreme values while allowing wide range of recording quality
	if measurements.NoiseFloor < -90.0 {
		measurements.NoiseFloor = -90.0
	} else if measurements.NoiseFloor > -30.0 {
		measurements.NoiseFloor = -30.0
	}

	// Store 250ms interval samples for data-driven silence candidate detection
	measurements.IntervalSamples = intervals

	// Detect silence regions using threshold already computed from interval distribution
	// The silenceThreshold was calculated above via estimateNoiseFloorAndThreshold()
	measurements.SilenceRegions = findSilenceCandidatesFromIntervals(intervals, silenceThreshold, 0)

	// Extract noise profile from best silence region (if available)
	// Uses interval data for all measurements - no file re-reading required
	silenceResult := findBestSilenceRegion(measurements.SilenceRegions, intervals, totalDuration)

	// Store all evaluated candidates for reporting/debugging
	measurements.SilenceCandidates = silenceResult.Candidates

	if silenceResult.BestRegion != nil {
		// Refine to golden sub-region: find cleanest 10s window within the candidate.
		// This isolates optimal noise profile from long candidates that may span
		// both pre-intentional (noisier) and intentional (cleaner) silence.
		originalRegion := silenceResult.BestRegion
		refinedRegion := refineToGoldenSubregion(originalRegion, intervals)
		wasRefined := refinedRegion.Start != originalRegion.Start || refinedRegion.Duration != originalRegion.Duration

		// Extract noise profile from interval data (no file re-read)
		if profile := extractNoiseProfileFromIntervals(refinedRegion, intervals); profile != nil {
			measurements.NoiseProfile = profile

			// Store refinement info for logging/debugging
			if wasRefined {
				profile.WasRefined = true
				profile.OriginalStart = originalRegion.Start
				profile.OriginalDuration = originalRegion.Duration
			}

			// If we got a noise profile measurement, use it as the primary noise floor
			// This is more accurate than the overall RMS_trough because it's from pure silence
			if profile.MeasuredNoiseFloor != 0 && !math.IsInf(profile.MeasuredNoiseFloor, -1) {
				measurements.NoiseFloor = profile.MeasuredNoiseFloor
			}
		}
	}

	// Calculate derived suggestions for Pass 2 adaptive processing
	// These are data-driven values based on actual measurements

	// SuggestedGateThreshold: linear amplitude threshold for gate
	// Data-driven calculation based on actual noise floor and quiet speech measurements
	// Gate should open above noise floor but below quiet speech
	//
	// Strategy:
	// - Use RMSTrough (quietest segments with speech) as reference for quiet speech
	// - Calculate adaptive offset based on gap between noise floor and quiet speech
	// - Smaller gap = smaller offset (preserve speech in noisy recordings)
	// - Larger gap = larger offset (more aggressive gating for clean recordings)
	gateThresholdDB := calculateAdaptiveDS201GateThreshold(measurements.NoiseFloor, measurements.RMSTrough)
	measurements.SuggestedGateThreshold = math.Pow(10, gateThresholdDB/20.0)

	// NoiseReductionHeadroom: dB gap between noise floor and quiet speech
	// This determines how aggressively we can apply noise reduction
	// RMS_trough represents the quietest RMS segments (should be near noise floor)
	// RMS_level represents average level (speech)
	// The gap tells us how much "room" we have to reduce noise without affecting speech
	if measurements.RMSLevel != 0 && measurements.NoiseFloor != 0 {
		// Headroom is the gap between average speech level and noise floor
		// Larger headroom = more aggressive NR possible
		measurements.NoiseReductionHeadroom = measurements.RMSLevel - measurements.NoiseFloor
		if measurements.NoiseReductionHeadroom < 0 {
			measurements.NoiseReductionHeadroom = 0 // Sanity check
		}
		if measurements.NoiseReductionHeadroom > 60 {
			measurements.NoiseReductionHeadroom = 60 // Cap at 60dB (very clean recording)
		}
	} else {
		// Fallback: estimate based on integrated loudness
		// Louder recordings typically have better SNR
		if measurements.InputI > -20 {
			measurements.NoiseReductionHeadroom = 40.0 // Professional recording
		} else if measurements.InputI > -30 {
			measurements.NoiseReductionHeadroom = 25.0 // Typical podcast
		} else {
			measurements.NoiseReductionHeadroom = 15.0 // Quiet recording
		}
	}

	return measurements, nil
}

// calculateAdaptiveDS201GateThreshold computes a data-driven gate threshold based on
// the measured noise floor and RMS trough (quiet speech indicator).
//
// Strategy:
//   - The gate threshold should be above the noise floor but below quiet speech
//   - RMSTrough represents the quietest RMS segments (breaths, quiet consonants)
//   - We place the threshold at a data-driven position between noise and quiet speech
//
// Calculation:
//   - Gap = RMSTrough - NoiseFloor (how much "room" between noise and speech)
//   - If gap is small (<10dB): recording is noisy, threshold at 30% into gap
//   - If gap is moderate (10-20dB): typical, threshold at 40% into gap
//   - If gap is large (>20dB): clean recording, threshold at 50% into gap
//
// Safety bounds:
//   - Never below noise floor (would gate during silence)
//   - Never above -35dBFS (would cut quiet speech)
func calculateAdaptiveDS201GateThreshold(noiseFloor, rmsTrough float64) float64 {
	// If RMSTrough is unavailable or invalid, use a sensible fallback
	if rmsTrough == 0 || rmsTrough <= noiseFloor {
		// Fallback: 6dB above noise floor (conservative default)
		threshold := noiseFloor + 6.0
		if threshold > -35.0 {
			threshold = -35.0
		}
		return threshold
	}

	// Calculate the gap between quiet speech and noise
	gap := rmsTrough - noiseFloor

	// Determine the adaptive offset percentage based on gap size
	var offsetPercent float64
	switch {
	case gap < 10.0:
		// Noisy recording: small gap, be conservative (30% into gap)
		// This preserves more speech at the cost of some noise bleed
		offsetPercent = 0.30
	case gap < 20.0:
		// Typical recording: moderate gap (40% into gap)
		offsetPercent = 0.40
	default:
		// Clean recording: large gap, more aggressive (50% into gap)
		offsetPercent = 0.50
	}

	// Calculate threshold: noise floor + (gap * percentage)
	threshold := noiseFloor + (gap * offsetPercent)

	// Safety bounds
	if threshold < noiseFloor+3.0 {
		// Always at least 3dB above noise floor
		threshold = noiseFloor + 3.0
	}
	if threshold > -35.0 {
		// Never gate above -35dBFS (would cut quiet speech)
		threshold = -35.0
	}

	return threshold
}

// createAnalysisFilterGraph creates an AVFilterGraph for Pass 1 analysis.
// Uses astats, aspectralstats, and ebur128 filters to extract measurements.
// Silence detection is now performed in Go using 250ms interval sampling.
func createAnalysisFilterGraph(
	decCtx *ffmpeg.AVCodecContext,
	config *FilterChainConfig,
) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
	// Configure for Pass 1 analysis
	// Uses unified BuildFilterSpec() with Pass1FilterOrder:
	// Downmix → Analysis
	config.Pass = 1
	config.FilterOrder = Pass1FilterOrder

	return setupFilterGraph(decCtx, config.BuildFilterSpec())
}

// Silence region scoring constants for noise profile extraction
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

	// Scoring weights (must sum to 1.0)
	amplitudeScoreWeight = 0.4
	spectralScoreWeight  = 0.5
	durationScoreWeight  = 0.1

	// Minimum acceptable score for "first wins" selection
	// Candidates below this threshold are skipped in favour of later candidates
	// Set low (0.3) to only reject truly problematic candidates (crosstalk, etc.)
	minAcceptableScore = 0.3

	// Temporal bias constants (still used in scoring for logging, but not for selection)
	temporalBiasMax    = 0.05 // Up to 5% penalty for late regions
	temporalWindowSecs = 90.0 // Regions after 90s get maximum penalty

	// Candidate selection cutoff
	candidateCutoffPercent = 0.15 // Only consider silence in first 15% of recording
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
// Uses multi-metric scoring to select the best candidate, considering:
// - Amplitude (quieter = better)
// - Spectral characteristics (noise-like = better, voice-like = worse)
// - Temporal position (earlier = slightly better)
// - Duration (closer to 15s = better)
//
// Uses pre-collected interval data for measurements - no file re-reading required.
// Returns nil if no suitable region is found.
// findBestSilenceRegionResult contains the selected region and all evaluated candidates
type findBestSilenceRegionResult struct {
	BestRegion *SilenceRegion
	Candidates []SilenceCandidateMetrics
}

func findBestSilenceRegion(regions []SilenceRegion, intervals []IntervalSample, totalDuration float64) *findBestSilenceRegionResult {
	result := &findBestSilenceRegionResult{}

	if len(regions) == 0 {
		return result
	}

	// Calculate cutoff time: only consider silence in first 15% of recording
	// Intentional room tone is always recorded near the start, not deep into the episode
	cutoffTime := time.Duration(totalDuration * candidateCutoffPercent * float64(time.Second))

	// Filter to candidates meeting duration and temporal criteria, then segment long regions.
	// Long silence regions are broken into overlapping segments to find the cleanest subsection.
	// This helps when intentional room tone is embedded within a longer quiet period.
	var candidates []SilenceRegion
	for _, r := range regions {
		// Must meet minimum duration
		if r.Duration < minimumSilenceDuration {
			continue
		}
		// Must start within first 15% of recording
		if r.Start > cutoffTime {
			continue
		}
		// Segment long regions to find cleanest subsection
		segments := segmentLongSilenceRegion(r)
		candidates = append(candidates, segments...)
	}

	if len(candidates) == 0 {
		return result
	}

	// "Stop on regression" selection strategy
	// Intentional room tone is always recorded near the start of the file.
	// Track the best candidate seen so far, but stop searching when we see a score
	// lower than the current best — that indicates we've passed the intentional room tone.
	// Still measure ALL candidates for logging/debugging purposes.
	var selectedCandidate *SilenceRegion
	var selectedIdx int = -1
	var bestScore float64 = -1
	selectionComplete := false

	for i := range candidates {
		candidate := &candidates[i]

		// Measure spectral characteristics from interval data (no file re-read)
		metrics := measureSilenceCandidateFromIntervals(*candidate, intervals)
		if metrics == nil {
			// No intervals in range - skip this candidate
			continue
		}

		// Score the candidate based on spectral characteristics
		// Candidates were already validated by the data-driven interval threshold
		score := scoreSilenceCandidate(metrics)
		metrics.Score = score

		// Store candidate metrics for reporting
		result.Candidates = append(result.Candidates, *metrics)

		// Selection logic: stop on first regression
		if !selectionComplete && score >= minAcceptableScore {
			if bestScore < 0 {
				// First acceptable candidate
				selectedCandidate = candidate
				selectedIdx = len(result.Candidates) - 1
				bestScore = score
			} else if score >= bestScore {
				// Equal or better than current best - prefer later candidate
				// (intentional room tone is recorded after brief intro/setup)
				selectedCandidate = candidate
				selectedIdx = len(result.Candidates) - 1
				bestScore = score
			} else {
				// Score regressed - stop searching, keep current best
				selectionComplete = true
			}
		}
	}

	result.BestRegion = selectedCandidate
	_ = selectedIdx // Used for debugging if needed

	return result
}

// scoreSilenceCandidate computes a composite score for a silence region candidate.
// Higher scores indicate better candidates for noise profiling.
// Returns 0.0 for candidates that should be rejected (e.g., crosstalk detected).
func scoreSilenceCandidate(m *SilenceCandidateMetrics) float64 {
	if m == nil {
		return 0.0
	}

	// Check for crosstalk rejection: voice-range centroid + peaked/impulsive characteristics
	if isLikelyCrosstalk(m) {
		return 0.0 // Reject this candidate
	}

	// Calculate individual component scores (all normalised to 0-1 range)
	ampScore := calculateAmplitudeScore(m.RMSLevel)
	specScore := calculateSpectralScore(m.SpectralCentroid, m.SpectralFlatness, m.SpectralKurtosis)
	durScore := calculateDurationScore(m.Region.Duration)

	// Weighted combination (base score)
	baseScore := ampScore*amplitudeScoreWeight +
		specScore*spectralScoreWeight +
		durScore*durationScoreWeight

	// Apply temporal bias as multiplicative tiebreaker (Task 4)
	// Early regions get up to 10% boost: score *= 1.0 - (startTime / 90s) * 0.1
	// At t=0: multiplier = 1.0, at t=90s+: multiplier = 0.9
	temporalMultiplier := applyTemporalBias(m.Region.Start)

	return baseScore * temporalMultiplier
}

// isLikelyCrosstalk detects if a silence candidate is likely crosstalk (leaked voice).
// Returns true if centroid is in voice range AND has peaked/impulsive characteristics.
func isLikelyCrosstalk(m *SilenceCandidateMetrics) bool {
	// Check if centroid is in voice frequency range
	inVoiceRange := m.SpectralCentroid >= voiceCentroidMin && m.SpectralCentroid <= voiceCentroidMax

	if !inVoiceRange {
		return false // Not in voice range, unlikely to be crosstalk
	}

	// Voice range + peaked harmonics (high kurtosis) = likely speech
	if m.SpectralKurtosis > crosstalkKurtosisThreshold {
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
	kurtosisScore := 1.0 - clampFloat(kurtosis/20.0, 0.0, 1.0)

	// Combine with weights from the spec
	return centroidScore*0.5 + flatnessScore*0.3 + kurtosisScore*0.2
}

// applyTemporalBias returns a multiplicative factor that gives early regions a boost.
// Formula from Task 4: score *= 1.0 - (startTime / 90s) * 0.1
// At t=0: returns 1.0 (no penalty), at t=90s+: returns 0.9 (10% reduction)
// This acts as a tiebreaker for candidates with similar base scores.
func applyTemporalBias(start time.Duration) float64 {
	startSecs := start.Seconds()
	if startSecs < 0 {
		startSecs = 0
	}

	// Linear bias: 0s → 1.0, 90s+ → 0.9
	bias := clampFloat(startSecs/temporalWindowSecs, 0.0, 1.0) * temporalBiasMax
	return 1.0 - bias
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

// clampFloat clamps a value to the range [min, max].
func clampFloat(value, min, max float64) float64 {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}

// MeasureOutputSilenceRegion analyses the same silence region in the output file
// that was measured during Pass 1, enabling direct comparison of noise characteristics.
// Unlike extractNoiseProfile, this does NOT create a WAV file - it only measures.
//
// Parameters:
//   - outputPath: path to the processed audio file
//   - region: the silence region identified during Pass 1 (start time and duration)
//
// Returns SilenceAnalysis with noise floor, peak level, crest factor, and entropy.
func MeasureOutputSilenceRegion(outputPath string, region SilenceRegion) (*SilenceAnalysis, error) {
	if region.Duration == 0 {
		return nil, fmt.Errorf("invalid silence region: zero duration")
	}

	// Open the processed audio file
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open output file: %w", err)
	}
	defer reader.Close()

	// Build filter spec to extract and analyze the silence region
	// Filter chain mirrors Pass 1's extractNoiseProfile for consistent measurements:
	// 1. atrim: extract the specific time region (start/duration format)
	// 2. astats: measure noise floor, peak, entropy on native format
	//
	// Note: No aformat needed here - the output file is already processed and in final format.
	// The key is measuring on identical audio data, not forcing format conversion.
	filterSpec := fmt.Sprintf(
		"atrim=start=%f:duration=%f,astats=metadata=1:measure_perchannel=RMS_level+Peak_level+Entropy",
		region.Start.Seconds(),
		region.Duration.Seconds(),
	)

	// Create filter graph
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(reader.GetDecoderContext(), filterSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to create analysis filter graph: %w", err)
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	// Process frames through filter to measure noise characteristics
	filteredFrame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&filteredFrame)

	// Track measurements from astats
	var noiseFloor float64
	var peakLevel float64
	var entropy float64
	var noiseFloorFound bool
	var framesProcessed int64

	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			break
		}
		if frame == nil {
			break // EOF
		}

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			continue // Skip problematic frames
		}

		// Pull filtered frames
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				continue
			}

			// Extract noise measurements from metadata
			if metadata := filteredFrame.Metadata(); metadata != nil {
				if value, ok := getFloatMetadata(metadata, metaKeyRMSLevel); ok {
					noiseFloor = value
					noiseFloorFound = true
				}
				if value, ok := getFloatMetadata(metadata, metaKeyPeakLevel); ok {
					peakLevel = value
				}
				if value, ok := getFloatMetadata(metadata, metaKeyEntropy); ok {
					entropy = value
				}
			}

			framesProcessed++
			ffmpeg.AVFrameUnref(filteredFrame)
		}
	}

	// Flush filter graph
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err == nil {
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				break
			}

			if metadata := filteredFrame.Metadata(); metadata != nil {
				if value, ok := getFloatMetadata(metadata, metaKeyRMSLevel); ok {
					noiseFloor = value
					noiseFloorFound = true
				}
				if value, ok := getFloatMetadata(metadata, metaKeyPeakLevel); ok {
					peakLevel = value
				}
				if value, ok := getFloatMetadata(metadata, metaKeyEntropy); ok {
					entropy = value
				}
			}

			framesProcessed++
			ffmpeg.AVFrameUnref(filteredFrame)
		}
	}

	if framesProcessed == 0 {
		return nil, fmt.Errorf("no frames processed in silence region")
	}

	// Calculate crest factor from peak and RMS (both in dB)
	crestFactorDB := 0.0
	if noiseFloorFound && peakLevel != 0 {
		crestFactorDB = peakLevel - noiseFloor
	}

	analysis := &SilenceAnalysis{
		Start:       region.Start,
		Duration:    region.Duration,
		PeakLevel:   peakLevel,
		CrestFactor: crestFactorDB,
		Entropy:     entropy,
	}

	if noiseFloorFound {
		analysis.NoiseFloor = noiseFloor
	} else {
		analysis.NoiseFloor = -60.0 // Conservative fallback
	}

	return analysis, nil
}
