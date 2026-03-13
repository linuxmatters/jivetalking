// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// DebugLog is a package-level function for debug logging.
// When set (non-nil), diagnostic output is written via this function.
// Set by main.go when --debug flag is enabled.
var DebugLog func(format string, args ...interface{})

// debugLog writes to the debug log if enabled, otherwise does nothing.
func debugLog(format string, args ...interface{}) {
	if DebugLog != nil {
		DebugLog(format, args...)
	}
}

// SilenceRegion represents a detected silence period in the audio
type SilenceRegion struct {
	Start    time.Duration `json:"start"`
	End      time.Duration `json:"end"`
	Duration time.Duration `json:"duration"`
}

// NoiseProfile contains measurements from the elected silence region.
// These measurements serve as a reference baseline for adaptive filter tuning:
//   - MeasuredNoiseFloor → compand expansion threshold (NoiseRemove)
//   - Entropy → gate release timing and range adaptation (DS201Gate)
//     (See docs/Spectral-Metrics-Reference.md for entropy value interpretations:
//     low entropy 0.08-0.30 = ordered/voiced; high entropy > 0.50 = disordered/noise)
//   - CrestFactor/PeakLevel → transient detection mode selection
//
// Note: The silence region is also re-measured in Pass 2 and Pass 4 for
// before/after comparison of noise reduction effectiveness.
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

// regionMeasurements holds the common measurement results from analysing an
// output audio region. Both silence and speech region measurement functions
// share this intermediate type before mapping to their specific candidate types.
type regionMeasurements struct {
	RMSLevel      float64
	PeakLevel     float64
	CrestFactor   float64
	Spectral      SpectralMetrics
	MomentaryLUFS float64
	ShortTermLUFS float64
	TruePeak      float64
	SamplePeak    float64
	FramesProcessed int64
}

// SilenceCandidateMetrics contains measurements for evaluating silence region candidates.
// These metrics are collected before final selection to enable multi-metric scoring.
// Includes all measurements available from IntervalSample for future filter tuning.
type SilenceCandidateMetrics struct {
	Region SilenceRegion `json:"region"` // The silence region being evaluated

	// Amplitude metrics
	RMSLevel    float64 `json:"rms_level"`    // dBFS, average level (lower = quieter)
	PeakLevel   float64 `json:"peak_level"`   // dBFS, max peak level across region
	CrestFactor float64 `json:"crest_factor"` // Peak - RMS in dB (high = impulsive)

	// Spectral metrics (averaged across region)
	Spectral SpectralMetrics `json:"spectral"`

	// Loudness metrics (averaged/max across region)
	MomentaryLUFS float64 `json:"momentary_lufs"`  // LUFS, average momentary loudness
	ShortTermLUFS float64 `json:"short_term_lufs"` // LUFS, average short-term loudness
	TruePeak      float64 `json:"true_peak"`       // dBTP, max true peak across region
	SamplePeak    float64 `json:"sample_peak"`     // dBFS, max sample peak across region

	// Warning flags (populated during scoring)
	TransientWarning string `json:"transient_warning,omitempty"` // Warning if danger zone signature detected

	// Scoring (computed after measurement)
	Score float64 `json:"score"` // Composite score for candidate ranking

	// StabilityScore measures the temporal consistency of the silence region (0-1).
	// Higher scores indicate more stable measurements across the region, suggesting
	// intentionally-recorded room tone rather than accidental gaps between speech.
	// Calculated from RMS variance and average spectral flux across intervals.
	StabilityScore float64 `json:"stability_score"`
}

// SpeechRegion represents a detected continuous speech period in the audio.
// Used for extracting representative speech measurements for adaptive tuning.
type SpeechRegion struct {
	Start    time.Duration `json:"start"`
	End      time.Duration `json:"end"`
	Duration time.Duration `json:"duration"`
}

// SpeechCandidateMetrics contains measurements for evaluating speech region candidates.
// These metrics characterise typical speech levels for adaptive filter tuning.
// Includes all measurements available from IntervalSample for future filter tuning.
type SpeechCandidateMetrics struct {
	Region SpeechRegion `json:"region"` // The speech region being evaluated

	// Amplitude metrics
	RMSLevel    float64 `json:"rms_level"`    // dBFS, average level (higher = louder speech)
	PeakLevel   float64 `json:"peak_level"`   // dBFS, max peak level across region
	CrestFactor float64 `json:"crest_factor"` // Peak - RMS in dB (speech typically 9-14 dB, optimal range)

	// Spectral metrics (averaged across region)
	Spectral SpectralMetrics `json:"spectral"`

	// Loudness metrics (averaged/max across region)
	MomentaryLUFS float64 `json:"momentary_lufs"`  // LUFS, average momentary loudness
	ShortTermLUFS float64 `json:"short_term_lufs"` // LUFS, average short-term loudness
	TruePeak      float64 `json:"true_peak"`       // dBTP, max true peak across region
	SamplePeak    float64 `json:"sample_peak"`     // dBFS, max sample peak across region

	// Stability metrics (populated during measurement)
	VoicingDensity float64 `json:"voicing_density,omitempty"` // Proportion of voiced intervals (0-1)

	// Scoring
	Score float64 `json:"score"` // Composite score for candidate ranking

	// Golden sub-region refinement info (populated when a long candidate is refined)
	OriginalStart    time.Duration `json:"original_start,omitempty"`    // Original candidate start before refinement
	OriginalDuration time.Duration `json:"original_duration,omitempty"` // Original candidate duration before refinement
	WasRefined       bool          `json:"was_refined,omitempty"`       // True if region was refined from a longer candidate
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
// Weights sum to 1.0, split between stability (0.30) and quality (0.70)
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
// search interval slice used for silence/room-tone detection. The caller
// passes the already-sliced searchIntervals (first silenceSearchPercent% of intervals).
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

	// Only use the first silenceSearchPercent% of intervals for threshold calculation
	searchLimit := len(intervals) * silenceSearchPercent / 100
	if searchLimit < silenceThresholdMinIntervals {
		searchLimit = silenceThresholdMinIntervals
	}
	searchIntervals := intervals[:searchLimit]

	// Use pre-computed medians for scoring reference
	rmsP50 := medians.rmsP50
	fluxP50 := medians.fluxP50

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
// 1. Use pre-computed reference values (medians) for room tone scoring
// 2. Score each interval for "room tone likelihood"
// 3. Use a score threshold (0.5) to identify room tone intervals
// 4. Find consecutive runs that meet minimum duration (8 seconds)
//
// The RMS threshold parameter is used as a hard ceiling - intervals above it
// cannot be silence regardless of spectral characteristics.
// Candidates in the first 15 seconds are excluded (typically contains intro).
func findSilenceCandidatesFromIntervals(intervals []IntervalSample, threshold float64, medians silenceMedians) []SilenceRegion {
	if len(intervals) < minimumSilenceIntervals {
		return nil
	}

	// Only search the first silenceSearchPercent% of the recording
	searchLimit := len(intervals) * silenceSearchPercent / 100
	if searchLimit < minimumSilenceIntervals {
		searchLimit = minimumSilenceIntervals
	}

	// Use pre-computed medians for room tone scoring
	rmsP50 := medians.rmsP50
	fluxP50 := medians.fluxP50

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

	// Speech detection results
	SpeechRegions    []SpeechRegion           `json:"speech_regions,omitempty"`    // Detected speech regions
	SpeechCandidates []SpeechCandidateMetrics `json:"speech_candidates,omitempty"` // All evaluated candidates with scores

	// Elected speech candidate measurements (for adaptive tuning)
	SpeechProfile *SpeechCandidateMetrics `json:"speech_profile,omitempty"` // Best speech candidate metrics

	// Noise profile extracted from best silence candidate
	NoiseProfile *NoiseProfile `json:"noise_profile,omitempty"` // nil if extraction failed

	// Derived suggestions for Pass 2 adaptive processing
	SuggestedGateThreshold float64 `json:"suggested_gate_threshold"` // Suggested gate threshold (linear amplitude)
	NoiseReductionHeadroom float64 `json:"noise_reduction_headroom"` // dB gap between noise and quiet speech
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
	SilenceSample *SilenceCandidateMetrics `json:"silence_sample,omitempty"` // Measurements from same silence region

	// Speech region analysis (same region as Pass 1, for processing comparison)
	SpeechSample *SpeechCandidateMetrics `json:"speech_sample,omitempty"` // Measurements from same speech region
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
func AnalyzeAudio(filename string, config *FilterChainConfig, progressCallback func(pass PassNumber, passName string, progress float64, level float64, measurements *AudioMeasurements)) (*AudioMeasurements, error) {
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

	// Process all frames through the filter graph
	if err := runFilterGraph(reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnReadError: func(err error) error {
			return fmt.Errorf("failed to read frame: %w", err)
		},
		OnPushError: func(err error) error {
			return fmt.Errorf("failed to add frame to filter: %w", err)
		},
		OnPullError: func(err error) error {
			return fmt.Errorf("failed to get filtered frame: %w", err)
		},
		OnInputFrame: func(inputFrame *ffmpeg.AVFrame) {
			// Calculate audio level from frame
			currentLevel = calculateFrameLevel(inputFrame)

			// Calculate input frame time based on samples processed (before filter graph upsampling)
			inputFrameTime := time.Duration(float64(inputSamplesProcessed) / inputSampleRate * float64(time.Second))
			inputSamplesProcessed += int64(inputFrame.NbSamples())
			lastFrameTime = inputFrameTime

			// Accumulate RMS and peak from INPUT frame (before filter graph which upsamples to 192kHz)
			// This gives accurate RMS and peak values matching the original audio levels
			intervalAcc.addFrameRMSAndPeak(inputFrame)

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
				progressCallback(PassAnalysis, "Analyzing", progress, currentLevel, nil)
			}
			frameCount++
		},
		OnFrame: func(_, filteredFrame *ffmpeg.AVFrame) (FrameAction, error) {
			// Extract spectral metrics once, reuse for both whole-file and interval accumulators
			metadata := filteredFrame.Metadata()
			spectral := extractSpectralMetrics(metadata)

			// Extract measurements from frame metadata (whole-file accumulators)
			extractFrameMetadata(metadata, acc, spectral)

			// Also accumulate into current interval for per-interval spectral data
			// Filtered frames roughly correspond to input timing (just at higher sample rate)
			intervalAcc.add(extractIntervalFrameMetrics(metadata, spectral))

			return FrameDiscard, nil
		},
	}); err != nil {
		return nil, err
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

	// Pre-compute silence detection medians (shared by noise estimation and candidate detection)
	silSearchLimit := len(intervals) * silenceSearchPercent / 100
	if silSearchLimit < silenceThresholdMinIntervals {
		silSearchLimit = silenceThresholdMinIntervals
	}
	if silSearchLimit > len(intervals) {
		silSearchLimit = len(intervals)
	}
	silMedians := computeSilenceMedians(intervals[:silSearchLimit])

	noiseFloorEstimate, silenceThreshold, ok := estimateNoiseFloorAndThreshold(intervals, silMedians)
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
	measurements.SilenceRegions = findSilenceCandidatesFromIntervals(intervals, silenceThreshold, silMedians)

	// Extract noise profile from best silence region (if available)
	// Uses interval data for all measurements - no file re-reading required
	silenceResult := findBestSilenceRegion(measurements.SilenceRegions, intervals, totalDuration)

	// Store all evaluated candidates for reporting/debugging
	measurements.SilenceCandidates = silenceResult.Candidates

	// Extract noise profile from best silence region BEFORE speech region selection.
	// This allows the SNR margin check in findBestSpeechRegion to penalise candidates
	// that are too close to the noise floor.
	var noiseProfile *NoiseProfile
	if silenceResult.BestRegion != nil {
		// Refine to golden sub-region: find cleanest 10s window within the candidate.
		// This isolates optimal noise profile from long candidates that may span
		// both pre-intentional (noisier) and intentional (cleaner) silence.
		originalRegion := silenceResult.BestRegion
		refinedRegion := refineToGoldenSubregion(originalRegion, intervals)
		wasRefined := refinedRegion.Start != originalRegion.Start || refinedRegion.Duration != originalRegion.Duration

		// Extract noise profile from interval data (no file re-read)
		if profile := extractNoiseProfileFromIntervals(refinedRegion, intervals); profile != nil {
			noiseProfile = profile
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

	// Detect speech candidates (must come after elected silence)
	var speechSearchStart time.Duration
	if silenceResult.BestRegion != nil {
		speechSearchStart = silenceResult.BestRegion.End
	} else if len(measurements.SilenceRegions) > 0 {
		// Fallback: use end of first silence region
		speechSearchStart = measurements.SilenceRegions[0].End
	} else {
		// No silence found - start speech search after 30 seconds
		speechSearchStart = 30 * time.Second
	}

	measurements.SpeechRegions = findSpeechCandidatesFromIntervals(intervals, speechSearchStart)

	// Select best speech region (passing noiseProfile for SNR margin checking)
	speechResult := findBestSpeechRegion(measurements.SpeechRegions, intervals, noiseProfile)
	measurements.SpeechCandidates = speechResult.Candidates

	if speechResult.BestRegion != nil {
		// Store elected speech profile
		for i := range speechResult.Candidates {
			if speechResult.Candidates[i].Region.Start == speechResult.BestRegion.Start {
				measurements.SpeechProfile = &speechResult.Candidates[i]
				break
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
	config.Pass = PassAnalysis
	config.FilterOrder = Pass1FilterOrder

	return setupFilterGraph(decCtx, config.BuildFilterSpec())
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

	// Candidate selection cutoff
	candidateCutoffPercent = 0.15 // Only consider silence in first 15% of recording

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
// Uses a two-pass approach: first scores all candidates using multi-metric analysis
// (amplitude, spectral characteristics, stability, duration), then elects the earliest
// candidate whose score is within selectionTolerance of the maximum. This avoids the
// pathological case where an intervening low-scoring candidate causes the algorithm
// to miss a higher-scoring candidate later in the sequence.
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

	// ── Pass 1: Score all candidates ──────────────────────────────────────
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

	// Check for crosstalk rejection: voice-range centroid + peaked/impulsive characteristics
	isCrosstalk := isLikelyCrosstalk(m)
	debugLog("scoreSilenceCandidate: start=%.3fs, CrestFactor=%.2f dB, isCrosstalk=%v",
		m.Region.Start.Seconds(), m.CrestFactor, isCrosstalk)
	if isCrosstalk {
		debugLog("scoreSilenceCandidate: REJECTING candidate at %.3fs (returning score=0.0)", m.Region.Start.Seconds())
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

// MeasureOutputSilenceRegion analyses the same silence region in the output file
// that was used for noise profiling in Pass 1. This allows comparing how
// noise characteristics changed after adaptive processing.
//
// The region parameter should use the same Start/Duration as the NoiseProfile
// from Pass 1 analysis. Returns nil if the region cannot be measured.
//
// Returns full SilenceCandidateMetrics with all amplitude, spectral, and loudness measurements.
func MeasureOutputSilenceRegion(outputPath string, region SilenceRegion) (*SilenceCandidateMetrics, error) {
	// Open the processed audio file
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open output file: %w", err)
	}
	defer reader.Close()

	return measureOutputSilenceRegionFromReader(reader, region)
}

// measureOutputRegionFromReader measures amplitude, spectral, and loudness
// metrics for a time region in an already-opened audio file. This is the
// shared implementation behind measureOutputSilenceRegionFromReader and
// measureOutputSpeechRegionFromReader.
func measureOutputRegionFromReader(reader *audio.Reader, start, duration time.Duration) (*regionMeasurements, error) {
	if start < 0 {
		return nil, fmt.Errorf("invalid region: negative start time")
	}
	if duration <= 0 {
		return nil, fmt.Errorf("invalid region: non-positive duration")
	}

	filterSpec := fmt.Sprintf(
		"atrim=start=%f:duration=%f,asetpts=PTS-STARTPTS,astats=metadata=1:measure_perchannel=0,aspectralstats=measure=all,ebur128=metadata=1:peak=sample+true",
		start.Seconds(),
		duration.Seconds(),
	)

	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(reader.GetDecoderContext(), filterSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to create analysis filter graph: %w", err)
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	var rmsLevel float64
	var peakLevel float64
	var crestFactor float64
	var momentaryLUFS float64
	var shortTermLUFS float64
	var truePeak float64
	var samplePeak float64
	var rmsLevelFound bool
	var framesProcessed int64

	var spectralAcc SpectralMetrics
	var spectralFrameCount int64

	extractMeasurements := func(_ *ffmpeg.AVFrame, filteredFrame *ffmpeg.AVFrame) (FrameAction, error) {
		if metadata := filteredFrame.Metadata(); metadata != nil {
			if value, ok := getFloatMetadata(metadata, metaKeyOverallRMSLevel); ok {
				rmsLevel = value
				rmsLevelFound = true
			}
			if value, ok := getFloatMetadata(metadata, metaKeyOverallPeakLevel); ok {
				peakLevel = value
			}
			if value, ok := getFloatMetadata(metadata, metaKeyOverallCrestFactor); ok {
				crestFactor = value
			}

			sm := extractSpectralMetrics(metadata)
			if sm.Found {
				spectralAcc.add(sm)
				spectralFrameCount++
			}

			if value, ok := getFloatMetadata(metadata, metaKeyEbur128M); ok {
				momentaryLUFS = value
			}
			if value, ok := getFloatMetadata(metadata, metaKeyEbur128S); ok {
				shortTermLUFS = value
			}
			if value, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
				truePeak = value
			}
			if value, ok := getFloatMetadata(metadata, metaKeyEbur128SamplePeak); ok {
				samplePeak = value
			}
		}

		framesProcessed++
		return FrameDiscard, nil
	}

	lenientHandler := func(err error) error { return nil }
	_ = runFilterGraph(reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnPushError: lenientHandler,
		OnPullError: lenientHandler,
		OnFrame:     extractMeasurements,
	})

	if framesProcessed == 0 {
		return nil, fmt.Errorf("no frames processed in region")
	}

	var avg SpectralMetrics
	if spectralFrameCount > 0 {
		avg = spectralAcc.average(float64(spectralFrameCount))
	}

	debugLog("  Frames processed: %d", framesProcessed)
	debugLog("  Spectral frames: %d", spectralFrameCount)
	debugLog("  Final ebur128 values:")
	debugLog("    momentaryLUFS: %f", momentaryLUFS)
	debugLog("    shortTermLUFS: %f", shortTermLUFS)
	debugLog("    truePeak: %f", truePeak)
	debugLog("    samplePeak: %f", samplePeak)
	debugLog("  Final astats values:")
	debugLog("    rmsLevel: %f (found: %v)", rmsLevel, rmsLevelFound)
	debugLog("    peakLevel: %f", peakLevel)
	debugLog("  Averaged spectral values:")
	debugLog("    spectralCentroid: %f", avg.Centroid)
	debugLog("    spectralRolloff: %f", avg.Rolloff)

	ebur128Valid := momentaryLUFS != 0.0 || shortTermLUFS != 0.0 || truePeak != 0.0
	if !ebur128Valid {
		debugLog("Warning: ebur128 measurements not captured (insufficient duration or warmup time)")
	}

	if crestFactor == 0.0 && rmsLevelFound && peakLevel != 0 {
		crestFactor = peakLevel - rmsLevel
	}

	result := &regionMeasurements{
		RMSLevel:        rmsLevel,
		PeakLevel:       peakLevel,
		CrestFactor:     crestFactor,
		Spectral:        avg,
		MomentaryLUFS:   momentaryLUFS,
		ShortTermLUFS:   shortTermLUFS,
		TruePeak:        linearRatioToDB(truePeak),
		SamplePeak:      linearRatioToDB(samplePeak),
		FramesProcessed: framesProcessed,
	}

	if !rmsLevelFound {
		result.RMSLevel = -60.0 // Conservative fallback
	}

	return result, nil
}

// measureOutputSilenceRegionFromReader measures a silence region and maps
// the result to SilenceCandidateMetrics.
func measureOutputSilenceRegionFromReader(reader *audio.Reader, region SilenceRegion) (*SilenceCandidateMetrics, error) {
	debugLog("=== MeasureOutputSilenceRegion: start=%.3fs, duration=%.3fs ===",
		region.Start.Seconds(), region.Duration.Seconds())

	result, err := measureOutputRegionFromReader(reader, region.Start, region.Duration)
	if err != nil {
		return nil, err
	}

	debugLog("=== MeasureOutputSilenceRegion SUMMARY ===")

	return &SilenceCandidateMetrics{
		Region:        region,
		RMSLevel:      result.RMSLevel,
		PeakLevel:     result.PeakLevel,
		CrestFactor:   result.CrestFactor,
		Spectral:      result.Spectral,
		MomentaryLUFS: result.MomentaryLUFS,
		ShortTermLUFS: result.ShortTermLUFS,
		TruePeak:      result.TruePeak,
		SamplePeak:    result.SamplePeak,
	}, nil
}

// FrameAction tells runFilterGraph what to do with a filtered frame after OnFrame returns.
type FrameAction int

const (
	// FrameDiscard unrefs and discards the filtered frame.
	FrameDiscard FrameAction = iota
	// FrameKeep leaves the frame referenced; the caller must unref when done.
	FrameKeep
)

// FrameLoopConfig controls the behaviour of runFilterGraph.
type FrameLoopConfig struct {
	// OnReadError is called when reader.ReadFrame returns an error.
	// Return nil to break the read loop (lenient); return the error to abort (strict).
	// nil callback = break on any error (lenient default).
	OnReadError func(err error) error

	// OnPushError is called when AVBuffersrcAddFrameFlags returns an error.
	// Return nil to continue reading (lenient); return the error to abort (strict).
	// nil callback = return the error (strict default).
	OnPushError func(err error) error

	// OnPullError is called when AVBuffersinkGetFrame returns an error that is
	// NOT EAGAIN and NOT EOF (those are handled internally by breaking the pull loop).
	// Return nil to break the pull loop and continue reading (lenient);
	// return the error to abort both loops (strict).
	// nil callback = return the error (strict default).
	OnPullError func(err error) error

	// OnFrame is called for each filtered frame pulled from the sink.
	// inputFrame is the most recently read input frame (before filtering).
	// filteredFrame is the frame pulled from the filter graph output.
	// Return FrameDiscard to have runFilterGraph unref the filtered frame,
	// or FrameKeep if the callback already consumed/unreffed it.
	// A non-nil error aborts both loops.
	OnFrame func(inputFrame, filteredFrame *ffmpeg.AVFrame) (FrameAction, error)

	// OnInputFrame is called for each input frame before it is pushed into
	// the filter graph. Use for pre-filter work (progress tracking, RMS accumulation).
	OnInputFrame func(inputFrame *ffmpeg.AVFrame)
}

// runFilterGraph runs the read-push-pull loop over a filter graph.
// It reads frames from reader, pushes them through the filter graph via
// bufferSrcCtx, pulls filtered frames from bufferSinkCtx, and calls the
// configured callbacks. After EOF, it flushes the filter graph and drains
// remaining frames.
//
// The caller owns the filter graph lifetime - runFilterGraph does NOT free it.
func runFilterGraph(
	reader *audio.Reader,
	bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
	config FrameLoopConfig,
) error {
	filteredFrame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&filteredFrame)

	// pullFrames drains filtered frames from the sink, calling OnFrame for each.
	// Returns an error only when the caller should abort both loops.
	pullFrames := func(inputFrame *ffmpeg.AVFrame) error {
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				if config.OnPullError != nil {
					if cbErr := config.OnPullError(err); cbErr != nil {
						return cbErr
					}
					break // callback returned nil = lenient, break inner loop
				}
				return err // nil callback = strict default
			}

			if config.OnFrame != nil {
				action, err := config.OnFrame(inputFrame, filteredFrame)
				if err != nil {
					return err
				}
				if action == FrameDiscard {
					ffmpeg.AVFrameUnref(filteredFrame)
				}
			} else {
				ffmpeg.AVFrameUnref(filteredFrame)
			}
		}
		return nil
	}

	// Main read loop
	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			if config.OnReadError != nil {
				if cbErr := config.OnReadError(err); cbErr != nil {
					return cbErr
				}
				break // callback returned nil = lenient, stop reading
			}
			break // nil callback = break (lenient default)
		}
		if frame == nil {
			break // EOF
		}

		if config.OnInputFrame != nil {
			config.OnInputFrame(frame)
		}

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			if config.OnPushError != nil {
				if cbErr := config.OnPushError(err); cbErr != nil {
					return cbErr
				}
				continue // callback returned nil = lenient, skip this frame
			}
			return err // nil callback = strict default
		}

		// Pull filtered frames
		if err := pullFrames(frame); err != nil {
			return err
		}
	}

	// Flush the filter graph by sending nil frame
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err != nil {
		if config.OnPushError != nil {
			if cbErr := config.OnPushError(err); cbErr != nil {
				return cbErr
			}
			return nil // flush push failed but callback swallowed it
		}
		return err // nil callback = strict default
	}

	// Drain remaining filtered frames
	return pullFrames(nil)
}

// MeasureOutputRegions measures both silence and speech regions from the same
// output file in a single open/close cycle. This avoids redundant file opens,
// demuxing, and decoding that would occur when calling MeasureOutputSilenceRegion
// and MeasureOutputSpeechRegion independently.
//
// Either region parameter may be nil to skip that measurement. Returns nil for
// any skipped or failed measurement (non-fatal — matches existing behaviour).
func MeasureOutputRegions(outputPath string, silenceRegion *SilenceRegion, speechRegion *SpeechRegion) (*SilenceCandidateMetrics, *SpeechCandidateMetrics) {
	if silenceRegion == nil && speechRegion == nil {
		return nil, nil
	}

	// Open the output file once for both measurements
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		debugLog("Warning: Failed to open output file for region measurements: %v", err)
		return nil, nil
	}
	defer reader.Close()

	// Measure silence region first (if requested)
	var silenceMetrics *SilenceCandidateMetrics
	if silenceRegion != nil {
		silenceMetrics, err = measureOutputSilenceRegionFromReader(reader, *silenceRegion)
		if err != nil {
			debugLog("Warning: Failed to measure silence region: %v", err)
			// Non-fatal — continue to speech measurement
		}
	}

	// Seek back to the beginning before measuring the speech region
	if speechRegion != nil {
		if silenceRegion != nil {
			// Only need to seek if we already read through the file for silence
			if err := reader.Seek(0); err != nil {
				debugLog("Warning: Failed to seek for speech region measurement: %v", err)
				return silenceMetrics, nil
			}
		}

		speechMetrics, err := measureOutputSpeechRegionFromReader(reader, *speechRegion)
		if err != nil {
			debugLog("Warning: Failed to measure speech region: %v", err)
			return silenceMetrics, nil
		}
		return silenceMetrics, speechMetrics
	}

	return silenceMetrics, nil
}

// MeasureOutputSpeechRegion analyses a speech region in the output file
// to capture comprehensive metrics for adaptive filter tuning and validation.
//
// The region parameter should identify a representative speech section from
// the processed audio. Returns nil if the region cannot be measured.
//
// Returns full SpeechCandidateMetrics with all amplitude, spectral, and loudness measurements.
func MeasureOutputSpeechRegion(outputPath string, region SpeechRegion) (*SpeechCandidateMetrics, error) {
	// Open the processed audio file
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open output file: %w", err)
	}
	defer reader.Close()

	return measureOutputSpeechRegionFromReader(reader, region)
}

// measureOutputSpeechRegionFromReader measures a speech region and maps
// the result to SpeechCandidateMetrics.
func measureOutputSpeechRegionFromReader(reader *audio.Reader, region SpeechRegion) (*SpeechCandidateMetrics, error) {
	debugLog("=== MeasureOutputSpeechRegion: start=%.3fs, duration=%.3fs ===",
		region.Start.Seconds(), region.Duration.Seconds())

	result, err := measureOutputRegionFromReader(reader, region.Start, region.Duration)
	if err != nil {
		return nil, err
	}

	debugLog("=== MeasureOutputSpeechRegion SUMMARY ===")

	return &SpeechCandidateMetrics{
		Region:        region,
		RMSLevel:      result.RMSLevel,
		PeakLevel:     result.PeakLevel,
		CrestFactor:   result.CrestFactor,
		Spectral:      result.Spectral,
		MomentaryLUFS: result.MomentaryLUFS,
		ShortTermLUFS: result.ShortTermLUFS,
		TruePeak:      result.TruePeak,
		SamplePeak:    result.SamplePeak,
	}, nil
}
