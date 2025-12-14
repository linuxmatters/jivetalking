// Package logging handles generation of analysis reports for processed audio files

package logging

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// linearToDb converts linear amplitude to decibels
func linearToDb(linear float64) float64 {
	if linear <= 0 {
		return -100.0 // Effectively -infinity
	}
	return 20.0 * math.Log10(linear)
}

// ============================================================================
// Spectral Characteristic Interpretation Functions
// ============================================================================
// These functions interpret spectral measurements and return human-readable
// descriptions of audio characteristics. Based on MATLAB Audio Toolbox
// documentation and standard audio analysis conventions.

// interpretCentroid describes the spectral "brightness" based on centroid frequency.
// Centroid is the "centre of gravity" of the spectrum - where spectral energy is concentrated.
//
// Reference values for speech:
// - Male voiced speech: 500-2500 Hz
// - Female voiced speech: 800-3500 Hz
// - Unvoiced consonants: 3000-8000+ Hz
//
// Higher centroid indicates brighter voice; useful for de-esser tuning.
func interpretCentroid(hz float64) string {
	switch {
	case hz < 500:
		return "very dark, bass-heavy"
	case hz < 1500:
		return "warm, full-bodied"
	case hz < 2500:
		return "balanced, natural voice"
	case hz < 4000:
		return "present, forward"
	case hz < 6000:
		return "bright, crisp"
	default:
		return "very bright, potentially harsh"
	}
}

// interpretSpread describes the spectral bandwidth around the centroid.
// Spread is the standard deviation of the spectrum around the centroid.
// It represents "instantaneous bandwidth" and indicates tonal dominance.
//
// Pure vowels show narrow spread; consonants and noise show wide spread.
// Low spread indicates tone dominance; high spread indicates broadband content.
func interpretSpread(hz float64) string {
	switch {
	case hz < 500:
		return "narrow, tonal, focused"
	case hz < 1500:
		return "moderate bandwidth, typical voiced"
	case hz < 3000:
		return "wide bandwidth, mixed content"
	default:
		return "very wide, noise-like or broadband"
	}
}

// interpretSkewness describes the spectral distribution asymmetry.
// Positive skewness: energy concentrated below centroid (bass-heavy with HF tail).
// Negative skewness: energy concentrated above centroid (HF concentrated, sibilant-like).
//
// Voiced speech typically shows positive skewness (0.5-2.0); fricatives may show negative.
func interpretSkewness(skew float64) string {
	switch {
	case skew < -0.5:
		return "HF concentrated, sibilant-like"
	case skew < 0.5:
		return "symmetric distribution"
	case skew < 1.5:
		return "LF emphasis with HF tail (typical voice)"
	default:
		return "strongly bass-concentrated"
	}
}

// interpretKurtosis describes the spectral peakiness.
// Kurtosis measures how peaked vs flat the spectrum is; indicates harmonic clarity vs noise.
// Higher values: peaked/tonal spectrum with dominant frequencies.
// Lower values: flatter spectrum, more noise-like.
//
// Healthy voiced speech typically 4-8; pathological or noisy voice trends toward 3.
func interpretKurtosis(kurt float64) string {
	switch {
	case kurt < 2.5:
		return "flat, noise-dominated"
	case kurt < 3.5:
		return "Gaussian-like, mixed content"
	case kurt < 5.0:
		return "moderately peaked, good harmonics"
	case kurt < 8.0:
		return "clearly peaked, strong harmonics"
	default:
		return "highly peaked, very tonal"
	}
}

// interpretEntropy describes spectral randomness/order.
// Entropy measures disorder in spectral energy distribution (0 to 1 normalised).
// Lower entropy: ordered/predictable spectrum (tonal content, harmonic structure).
// Higher entropy: random/unpredictable spectrum (noise, complex transients).
//
// Note: If values appear outside 0-1 range, the metric may be unnormalised (raw bits).
func interpretEntropy(entropy float64) string {
	switch {
	case entropy < 0.3:
		return "highly ordered, clear pitch"
	case entropy < 0.5:
		return "moderately ordered, typical voiced"
	case entropy < 0.7:
		return "mixed order, voiced with noise"
	case entropy < 0.9:
		return "disordered, noise-like"
	default:
		return "highly disordered, approaching white noise"
	}
}

// interpretFlatness describes the tonality vs noise character.
// Flatness is the ratio of geometric mean to arithmetic mean of spectrum (0 to 1).
// Values near 0: tonal/harmonic content with spectral peaks.
// Values near 1: flat/white noise spectrum.
//
// Clean voiced speech 0.1-0.3; breathy voice 0.3-0.5; fricatives 0.4-0.7.
func interpretFlatness(flatness float64) string {
	switch {
	case flatness < 0.1:
		return "highly tonal, pure harmonics"
	case flatness < 0.25:
		return "tonal with some noise, clean voiced"
	case flatness < 0.4:
		return "moderate tonality, typical speech"
	case flatness < 0.6:
		return "mixed tonal/noise, breathy content"
	default:
		return "noise-dominant, very breathy"
	}
}

// interpretCrest describes the peak-to-average ratio of the spectrum.
// Higher crest: spectrum has prominent peaks (tonal, resonant).
// Lower crest: spectrum is more uniform (noise-like, broadband).
//
// Higher crest indicates clearer harmonics; low crest suggests noise contamination.
func interpretCrest(crest float64) string {
	switch {
	case crest < 10:
		return "flat spectrum, noise-like"
	case crest < 25:
		return "moderate peaks, mixed content"
	case crest < 40:
		return "prominent peaks, clear harmonics"
	case crest < 60:
		return "strong peaks, very tonal"
	default:
		return "extremely peaked, dominant frequency"
	}
}

// interpretFlux describes frame-to-frame spectral variation.
// Flux measures how much the spectrum changes between frames.
// Low flux: stable/sustained sound (held notes, steady speech).
// High flux: dynamic/changing sound (transients, varied speech).
//
// Vowels show low flux; plosives (p,t,k,b,d,g) show high flux spikes.
func interpretFlux(flux float64) string {
	switch {
	case flux < 0.001:
		return "very stable, sustained phonation"
	case flux < 0.01:
		return "stable, steady speech"
	case flux < 0.05:
		return "moderate variation, natural articulation"
	case flux < 0.2:
		return "high variation, transients/plosives"
	default:
		return "rapid change, onsets/consonant bursts"
	}
}

// interpretDecrease describes low-frequency weighted spectral decrease.
// Similar to slope but emphasises low-frequency behaviour.
// Used for characterising bass/warmth in the signal.
//
// Male voices typically show more negative decrease than female voices.
func interpretDecrease(decrease float64) string {
	switch {
	case decrease < -0.05:
		return "strong bass concentration, warm foundation"
	case decrease < -0.02:
		return "moderate LF emphasis, typical male voice"
	case decrease < 0:
		return "balanced low-frequency content"
	default:
		return "weak bass, thin, lacking body"
	}
}

// interpretRolloff describes the effective bandwidth of the signal.
// Rolloff is the frequency below which 85-95% of spectral energy exists.
// Higher rolloff indicates more high-frequency content; useful for sibilance detection.
func interpretRolloff(hz float64) string {
	switch {
	case hz < 3000:
		return "dark, muffled, heavy filtering"
	case hz < 5000:
		return "warm, controlled high frequencies"
	case hz < 8000:
		return "balanced brightness, natural speech"
	case hz < 12000:
		return "bright, airy, good articulation"
	default:
		return "very bright, significant sibilance"
	}
}

// interpretSlope describes the overall spectral tilt (linear regression coefficient).
// Modal speech approximately -6 dB/octave; breathy voice steeper; pressed voice shallower.
func interpretSlope(slope float64) string {
	switch {
	case slope < -5e-05:
		return "steep negative slope, dark/warm"
	case slope < -1e-05:
		return "moderate slope, balanced brightness"
	case slope < 0:
		return "shallow slope, bright/energetic"
	default:
		return "positive slope, very bright"
	}
}

// formatSpectralValue formats small values using scientific notation when appropriate
func formatSpectralValue(value float64, decimals int) string {
	// Use scientific notation for very small values
	if value != 0 && math.Abs(value) < 0.0001 {
		return fmt.Sprintf("%.2e", value)
	}
	format := fmt.Sprintf("%%.%df", decimals)
	return fmt.Sprintf(format, value)
}

// =============================================================================
// Report Section Formatting Helpers
// =============================================================================

// writeSection writes a section header with title and dashed underline.
// The underline length matches the title length.
func writeSection(f *os.File, title string) {
	fmt.Fprintln(f, title)
	fmt.Fprintln(f, strings.Repeat("-", len(title)))
}

// spectralMetrics holds spectral measurement values for formatting.
// Used to deduplicate spectral metric output between input and output sections.
type spectralMetrics struct {
	Mean     float64
	Variance float64
	Centroid float64
	Spread   float64
	Skewness float64
	Kurtosis float64
	Entropy  float64
	Flatness float64
	Crest    float64
	Flux     float64
	Slope    float64
	Decrease float64
	Rolloff  float64
}

// writeSpectralMetrics writes all 13 spectral metrics with interpretations.
// If compare is non-nil, includes comparison with input values.
func writeSpectralMetrics(f *os.File, m spectralMetrics, compare *spectralMetrics) {
	if compare == nil {
		// Input format: value — interpretation
		fmt.Fprintf(f, "Spectral Mean:       %s (avg magnitude)\n", formatSpectralValue(m.Mean, 6))
		fmt.Fprintf(f, "Spectral Variance:   %s (magnitude spread)\n", formatSpectralValue(m.Variance, 6))
		fmt.Fprintf(f, "Spectral Centroid:   %.0f Hz — %s\n", m.Centroid, interpretCentroid(m.Centroid))
		fmt.Fprintf(f, "Spectral Spread:     %.0f Hz — %s\n", m.Spread, interpretSpread(m.Spread))
		fmt.Fprintf(f, "Spectral Skewness:   %.3f — %s\n", m.Skewness, interpretSkewness(m.Skewness))
		fmt.Fprintf(f, "Spectral Kurtosis:   %.3f — %s\n", m.Kurtosis, interpretKurtosis(m.Kurtosis))
		fmt.Fprintf(f, "Spectral Entropy:    %s — %s\n", formatSpectralValue(m.Entropy, 6), interpretEntropy(m.Entropy))
		fmt.Fprintf(f, "Spectral Flatness:   %s — %s\n", formatSpectralValue(m.Flatness, 6), interpretFlatness(m.Flatness))
		fmt.Fprintf(f, "Spectral Crest:      %.3f — %s\n", m.Crest, interpretCrest(m.Crest))
		fmt.Fprintf(f, "Spectral Flux:       %s — %s\n", formatSpectralValue(m.Flux, 6), interpretFlux(m.Flux))
		fmt.Fprintf(f, "Spectral Slope:      %s — %s\n", formatSpectralValue(m.Slope, 9), interpretSlope(m.Slope))
		fmt.Fprintf(f, "Spectral Decrease:   %s — %s\n", formatSpectralValue(m.Decrease, 6), interpretDecrease(m.Decrease))
		fmt.Fprintf(f, "Spectral Rolloff:    %.0f Hz — %s\n", m.Rolloff, interpretRolloff(m.Rolloff))
	} else {
		// Output format: value — interpretation (was X)
		fmt.Fprintf(f, "Spectral Mean:       %s %s\n", formatSpectralValue(m.Mean, 6), formatComparisonSpectral(m.Mean, compare.Mean, 6))
		fmt.Fprintf(f, "Spectral Variance:   %s %s\n", formatSpectralValue(m.Variance, 6), formatComparisonSpectral(m.Variance, compare.Variance, 6))
		fmt.Fprintf(f, "Spectral Centroid:   %.0f Hz — %s %s\n", m.Centroid, interpretCentroid(m.Centroid), formatComparison(m.Centroid, compare.Centroid, "Hz", 0))
		fmt.Fprintf(f, "Spectral Spread:     %.0f Hz — %s %s\n", m.Spread, interpretSpread(m.Spread), formatComparison(m.Spread, compare.Spread, "Hz", 0))
		fmt.Fprintf(f, "Spectral Skewness:   %.3f — %s %s\n", m.Skewness, interpretSkewness(m.Skewness), formatComparisonNoUnit(m.Skewness, compare.Skewness, 3))
		fmt.Fprintf(f, "Spectral Kurtosis:   %.3f — %s %s\n", m.Kurtosis, interpretKurtosis(m.Kurtosis), formatComparisonNoUnit(m.Kurtosis, compare.Kurtosis, 3))
		fmt.Fprintf(f, "Spectral Entropy:    %s — %s %s\n", formatSpectralValue(m.Entropy, 6), interpretEntropy(m.Entropy), formatComparisonSpectral(m.Entropy, compare.Entropy, 6))
		fmt.Fprintf(f, "Spectral Flatness:   %s — %s %s\n", formatSpectralValue(m.Flatness, 6), interpretFlatness(m.Flatness), formatComparisonNoUnit(m.Flatness, compare.Flatness, 6))
		fmt.Fprintf(f, "Spectral Crest:      %.3f — %s %s\n", m.Crest, interpretCrest(m.Crest), formatComparisonNoUnit(m.Crest, compare.Crest, 3))
		fmt.Fprintf(f, "Spectral Flux:       %s — %s %s\n", formatSpectralValue(m.Flux, 6), interpretFlux(m.Flux), formatComparisonSpectral(m.Flux, compare.Flux, 6))
		fmt.Fprintf(f, "Spectral Slope:      %s — %s %s\n", formatSpectralValue(m.Slope, 9), interpretSlope(m.Slope), formatComparisonSpectral(m.Slope, compare.Slope, 9))
		fmt.Fprintf(f, "Spectral Decrease:   %s — %s %s\n", formatSpectralValue(m.Decrease, 6), interpretDecrease(m.Decrease), formatComparisonSpectral(m.Decrease, compare.Decrease, 6))
		fmt.Fprintf(f, "Spectral Rolloff:    %.0f Hz — %s %s\n", m.Rolloff, interpretRolloff(m.Rolloff), formatComparison(m.Rolloff, compare.Rolloff, "Hz", 0))
	}
}

// spectralFromMeasurements extracts spectral metrics from AudioMeasurements.
func spectralFromMeasurements(m *processor.AudioMeasurements) spectralMetrics {
	return spectralMetrics{
		Mean:     m.SpectralMean,
		Variance: m.SpectralVariance,
		Centroid: m.SpectralCentroid,
		Spread:   m.SpectralSpread,
		Skewness: m.SpectralSkewness,
		Kurtosis: m.SpectralKurtosis,
		Entropy:  m.SpectralEntropy,
		Flatness: m.SpectralFlatness,
		Crest:    m.SpectralCrest,
		Flux:     m.SpectralFlux,
		Slope:    m.SpectralSlope,
		Decrease: m.SpectralDecrease,
		Rolloff:  m.SpectralRolloff,
	}
}

// spectralFromOutputMeasurements extracts spectral metrics from OutputMeasurements.
func spectralFromOutputMeasurements(m *processor.OutputMeasurements) spectralMetrics {
	return spectralMetrics{
		Mean:     m.SpectralMean,
		Variance: m.SpectralVariance,
		Centroid: m.SpectralCentroid,
		Spread:   m.SpectralSpread,
		Skewness: m.SpectralSkewness,
		Kurtosis: m.SpectralKurtosis,
		Entropy:  m.SpectralEntropy,
		Flatness: m.SpectralFlatness,
		Crest:    m.SpectralCrest,
		Flux:     m.SpectralFlux,
		Slope:    m.SpectralSlope,
		Decrease: m.SpectralDecrease,
		Rolloff:  m.SpectralRolloff,
	}
}

// formatComparison returns "(unchanged)" if values match within tolerance, otherwise "(was X unit)"
func formatComparison(output, input float64, unit string, decimals int) string {
	// Use tolerance based on decimal places shown
	tolerance := math.Pow(10, -float64(decimals)) * 0.5
	if math.Abs(output-input) < tolerance {
		return "(unchanged)"
	}
	format := fmt.Sprintf("(was %%.%df %s)", decimals, unit)
	return fmt.Sprintf(format, input)
}

// formatComparisonNoUnit returns "(unchanged)" if values match within tolerance, otherwise "(was X)"
func formatComparisonNoUnit(output, input float64, decimals int) string {
	tolerance := math.Pow(10, -float64(decimals)) * 0.5
	if math.Abs(output-input) < tolerance {
		return "(unchanged)"
	}
	format := fmt.Sprintf("(was %%.%df)", decimals)
	return fmt.Sprintf(format, input)
}

// formatComparisonSpectral returns "(unchanged)" if values match, otherwise "(was X)" using scientific notation for small values
func formatComparisonSpectral(output, input float64, decimals int) string {
	tolerance := math.Pow(10, -float64(decimals)) * 0.5
	if math.Abs(output-input) < tolerance {
		return "(unchanged)"
	}
	return fmt.Sprintf("(was %s)", formatSpectralValue(input, decimals))
}

// maxRealisticDynamicRange is the theoretical maximum for 24-bit audio (~144 dB).
// Values above this indicate measurement artifacts (e.g., near-digital-silence after denoising).
const maxRealisticDynamicRange = 144.0

// formatDynamicRange clamps dynamic range to a realistic maximum.
// FFmpeg's astats calculates dynamic range as Peak_level - Noise_floor.
// After aggressive denoising, the noise floor can drop to near digital silence,
// producing unrealistic values (300+ dB). We clamp to 144 dB (24-bit theoretical max)
// and add a note indicating the measurement is limited by digital precision.
func formatDynamicRange(value float64) string {
	if value > maxRealisticDynamicRange {
		return fmt.Sprintf("%.1f dB (clamped — digital silence floor)", maxRealisticDynamicRange)
	}
	return fmt.Sprintf("%.1f dB", value)
}

// formatDynamicRangeComparison formats dynamic range with comparison to input value
func formatDynamicRangeComparison(output, input float64) string {
	outputClamped := output
	if outputClamped > maxRealisticDynamicRange {
		outputClamped = maxRealisticDynamicRange
	}

	result := fmt.Sprintf("%.1f dB", outputClamped)

	// Add clamping note
	if output > maxRealisticDynamicRange {
		result += " (clamped)"
	}

	// Add comparison
	tolerance := 0.5
	if math.Abs(outputClamped-input) < tolerance {
		result += " (unchanged)"
	} else {
		result += fmt.Sprintf(" (was %.1f dB)", input)
	}

	return result
}

// maxRealisticCrestFactor is the upper bound for realistic audio crest factor.
// Typical speech: 10-25 dB. Values >40 dB indicate measurement across near-silence
// (peak/tiny-RMS = huge ratio). We clamp to 40 dB and note the anomaly.
const maxRealisticCrestFactor = 40.0

// formatCrestFactor clamps crest factor to a realistic maximum.
// Very high values (>40 dB) indicate FFmpeg astats measured across audio segments
// with near-silence periods, causing peak/RMS to be extremely large.
func formatCrestFactor(value float64) string {
	if value > maxRealisticCrestFactor {
		return fmt.Sprintf(">%.0f dB (measured %.1f dB — includes near-silence)", maxRealisticCrestFactor, value)
	}
	return fmt.Sprintf("%.1f dB", value)
}

// formatCrestFactorComparison formats crest factor with comparison to input value
func formatCrestFactorComparison(output, input float64) string {
	result := formatCrestFactor(output)

	// Clamp both for comparison
	outputClamped := output
	if outputClamped > maxRealisticCrestFactor {
		outputClamped = maxRealisticCrestFactor
	}
	inputClamped := input
	if inputClamped > maxRealisticCrestFactor {
		inputClamped = maxRealisticCrestFactor
	}

	// Add comparison (using clamped values for "unchanged" check)
	tolerance := 0.5
	if math.Abs(outputClamped-inputClamped) < tolerance {
		result += " (unchanged)"
	} else if input > maxRealisticCrestFactor {
		result += fmt.Sprintf(" (was >%.0f dB)", maxRealisticCrestFactor)
	} else {
		result += fmt.Sprintf(" (was %.1f dB)", input)
	}

	return result
}

// ReportData contains all the information needed to generate an analysis report
type ReportData struct {
	InputPath    string
	OutputPath   string
	StartTime    time.Time
	EndTime      time.Time
	Pass1Time    time.Duration
	Pass2Time    time.Duration
	Result       *processor.ProcessingResult
	SampleRate   int
	Channels     int
	DurationSecs float64 // Duration in seconds
}

// GenerateReport creates a detailed analysis report and saves it alongside the output file
// The report filename will be <output>-processed.log
func GenerateReport(data ReportData) error {
	// Generate report filename: presenter1-processed.flac → presenter1-processed.log
	logPath := strings.TrimSuffix(data.OutputPath, filepath.Ext(data.OutputPath)) + ".log"

	// Create report file
	f, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer f.Close()

	// Write report content
	fmt.Fprintln(f, "Jivetalking Analysis Report")
	fmt.Fprintln(f, "============================")
	fmt.Fprintf(f, "File: %s\n", filepath.Base(data.InputPath))
	fmt.Fprintf(f, "Processed: %s\n", data.EndTime.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(f, "Duration: %s\n", formatDuration(time.Duration(data.DurationSecs*float64(time.Second))))
	fmt.Fprintln(f, "")

	// Pass 1: Input Analysis
	writeSection(f, "Pass 1: Input Analysis")
	if data.Result != nil && data.Result.Measurements != nil {
		m := data.Result.Measurements
		// Loudness measurements from ebur128
		fmt.Fprintf(f, "Integrated Loudness: %.1f LUFS\n", m.InputI)
		fmt.Fprintf(f, "Momentary Loudness:  %.1f LUFS (400ms window)\n", m.MomentaryLoudness)
		fmt.Fprintf(f, "Short-term Loudness: %.1f LUFS (3s window)\n", m.ShortTermLoudness)
		fmt.Fprintf(f, "True Peak:           %.1f dBTP\n", m.InputTP)
		fmt.Fprintf(f, "Sample Peak:         %.1f dBFS\n", m.SamplePeak)
		fmt.Fprintf(f, "Loudness Range:      %.1f LU\n", m.InputLRA)

		// Noise floor measurements
		fmt.Fprintf(f, "Noise Floor:         %.1f dB (derived)\n", m.NoiseFloor)
		if m.AstatsNoiseFloor != 0 && !math.IsInf(m.AstatsNoiseFloor, -1) {
			fmt.Fprintf(f, "FFmpeg Noise Floor:  %.1f dB (astats)\n", m.AstatsNoiseFloor)
		}
		// Show adaptive silence detection threshold if different from default
		if m.SilenceDetectLevel != 0 && m.SilenceDetectLevel != -50.0 {
			fmt.Fprintf(f, "Silence Threshold:   %.1f dB (adaptive from %.1f dB pre-scan)\n",
				m.SilenceDetectLevel, m.PreScanNoiseFloor)
		}

		// Dynamic range and level statistics
		fmt.Fprintf(f, "Dynamic Range:       %s\n", formatDynamicRange(m.DynamicRange))
		fmt.Fprintf(f, "RMS Level:           %.1f dBFS\n", m.RMSLevel)
		fmt.Fprintf(f, "RMS Peak:            %.1f dBFS (loudest segments)\n", m.RMSPeak)
		fmt.Fprintf(f, "RMS Trough:          %.1f dBFS (quietest segments)\n", m.RMSTrough)
		fmt.Fprintf(f, "Peak Level:          %.1f dBFS\n", m.PeakLevel)
		if m.MinLevel != 0 {
			fmt.Fprintf(f, "Min Level:           %.1f dBFS\n", m.MinLevel)
		}
		if m.MaxLevel != 0 {
			fmt.Fprintf(f, "Max Level:           %.1f dBFS\n", m.MaxLevel)
		}
		if m.CrestFactor > 0 {
			fmt.Fprintf(f, "Crest Factor:        %s (peak-to-RMS)\n", formatCrestFactor(m.CrestFactor))
		}

		// Spectral analysis (aspectralstats measurements) with characteristic interpretations
		writeSpectralMetrics(f, spectralFromMeasurements(m), nil)

		// Signal quality and audio characteristics
		fmt.Fprintln(f, "")
		fmt.Fprintln(f, "Signal Quality:")
		if m.BitDepth > 0 {
			fmt.Fprintf(f, "  Effective Bit Depth: %.1f bits\n", m.BitDepth)
		}
		if m.NumberOfSamples > 0 {
			fmt.Fprintf(f, "  Total Samples:       %.0f\n", m.NumberOfSamples)
		}
		if m.DCOffset != 0 {
			fmt.Fprintf(f, "  DC Offset:           %.6f\n", m.DCOffset)
		}
		if m.FlatFactor > 0 {
			fmt.Fprintf(f, "  Flat Factor:         %.1f (clipping indicator)\n", m.FlatFactor)
		}
		if m.Entropy > 0 {
			fmt.Fprintf(f, "  Signal Entropy:      %.3f (randomness)\n", m.Entropy)
		}
		if m.ZeroCrossingsRate > 0 {
			fmt.Fprintf(f, "  Zero Crossings Rate: %.4f\n", m.ZeroCrossingsRate)
		}
		if m.ZeroCrossings > 0 {
			fmt.Fprintf(f, "  Zero Crossings:      %.0f total\n", m.ZeroCrossings)
		}

		// Sample-to-sample change metrics (transient/click indicators)
		if m.MaxDifference > 0 || m.MeanDifference > 0 {
			fmt.Fprintln(f, "")
			fmt.Fprintln(f, "Sample Variation (transient indicators):")
			if m.MaxDifference > 0 {
				maxDiffPercent := (m.MaxDifference / 32768.0) * 100.0
				fmt.Fprintf(f, "  Max Difference:      %.1f%% FS\n", maxDiffPercent)
			}
			if m.MinDifference > 0 {
				minDiffPercent := (m.MinDifference / 32768.0) * 100.0
				fmt.Fprintf(f, "  Min Difference:      %.4f%% FS\n", minDiffPercent)
			}
			if m.MeanDifference > 0 {
				meanDiffPercent := (m.MeanDifference / 32768.0) * 100.0
				fmt.Fprintf(f, "  Mean Difference:     %.4f%% FS\n", meanDiffPercent)
			}
			if m.RMSDifference > 0 {
				rmsDiffPercent := (m.RMSDifference / 32768.0) * 100.0
				fmt.Fprintf(f, "  RMS Difference:      %.4f%% FS\n", rmsDiffPercent)
			}
		}

		// Interval sampling summary with RMSLevel distribution analysis
		if len(m.IntervalSamples) > 0 {
			fmt.Fprintln(f, "")
			fmt.Fprintf(f, "Interval Samples:    %d × 250ms windows analysed\n", len(m.IntervalSamples))

			// Calculate and display RMSLevel distribution for silence detection debugging
			// RMSLevel = average level per interval (true silence is consistently quiet)
			rmsValues := make([]float64, 0, len(m.IntervalSamples))
			for _, interval := range m.IntervalSamples {
				if interval.RMSLevel > -120 { // Exclude digital silence
					rmsValues = append(rmsValues, interval.RMSLevel)
				}
			}
			if len(rmsValues) >= 10 {
				// Sort to get percentiles
				sorted := make([]float64, len(rmsValues))
				copy(sorted, rmsValues)
				sort.Float64s(sorted)

				fmt.Fprintf(f, "  RMSLevel Dist:     min %.1f, p10 %.1f, p25 %.1f, p50 %.1f, p75 %.1f, p90 %.1f, max %.1f dBFS\n",
					sorted[0],
					sorted[len(sorted)/10],
					sorted[len(sorted)/4],
					sorted[len(sorted)/2],
					sorted[len(sorted)*3/4],
					sorted[len(sorted)*9/10],
					sorted[len(sorted)-1])

				// Find largest gap for silence/speech boundary detection
				var largestGap float64
				var gapIndex int
				for i := 1; i < len(sorted); i++ {
					gap := sorted[i] - sorted[i-1]
					if gap > largestGap {
						largestGap = gap
						gapIndex = i
					}
				}
				if gapIndex > 0 && gapIndex < len(sorted) {
					fmt.Fprintf(f, "  Largest Gap:       %.1f dB between %.1f and %.1f dBFS (%d intervals below)\n",
						largestGap, sorted[gapIndex-1], sorted[gapIndex], gapIndex)
				}
			}
		}

		// Silence candidates (all evaluated candidates with scores)
		if len(m.SilenceCandidates) > 0 {
			fmt.Fprintf(f, "Silence Candidates:  %d evaluated\n", len(m.SilenceCandidates))
			for i, c := range m.SilenceCandidates {
				// Check if this is the selected candidate
				isSelected := m.NoiseProfile != nil && c.Region.Start == m.NoiseProfile.Start

				if isSelected {
					// Full details for selected candidate
					fmt.Fprintf(f, "  Candidate %d:       %.1fs at %.1fs (score: %.3f) [SELECTED]\n",
						i+1, c.Region.Duration.Seconds(), c.Region.Start.Seconds(), c.Score)
					fmt.Fprintf(f, "    RMS Level:       %.1f dBFS\n", c.RMSLevel)
					fmt.Fprintf(f, "    Peak Level:      %.1f dBFS\n", c.PeakLevel)
					fmt.Fprintf(f, "    Crest Factor:    %.1f dB\n", c.CrestFactor)
					fmt.Fprintf(f, "    Centroid:        %.0f Hz\n", c.SpectralCentroid)
					fmt.Fprintf(f, "    Flatness:        %.3f\n", c.SpectralFlatness)
					fmt.Fprintf(f, "    Kurtosis:        %.1f\n", c.SpectralKurtosis)
					// Classify noise type based on entropy
					noiseType := "broadband"
					if c.Entropy < 0.7 {
						noiseType = "tonal"
					} else if c.Entropy < 0.9 {
						noiseType = "mixed"
					}
					fmt.Fprintf(f, "    Entropy:         %.3f (%s)\n", c.Entropy, noiseType)
				} else {
					// Single line summary for rejected candidates
					reason := ""
					if c.Score == 0.0 {
						reason = " — rejected: too loud"
					}
					fmt.Fprintf(f, "  Candidate %d:       %.1fs at %.1fs (score: %.3f, RMS %.1f dBFS)%s\n",
						i+1, c.Region.Duration.Seconds(), c.Region.Start.Seconds(), c.Score, c.RMSLevel, reason)
				}
			}
		} else if m.NoiseProfile != nil {
			// Fallback: show selected profile if no candidates stored (shouldn't happen)
			fmt.Fprintf(f, "Silence Sample:      %.1fs at %.1fs\n",
				m.NoiseProfile.Duration.Seconds(),
				m.NoiseProfile.Start.Seconds())
			fmt.Fprintf(f, "  Noise Floor:       %.1f dBFS (RMS)\n", m.NoiseProfile.MeasuredNoiseFloor)
			fmt.Fprintf(f, "  Peak Level:        %.1f dBFS\n", m.NoiseProfile.PeakLevel)
			fmt.Fprintf(f, "  Crest Factor:      %.1f dB\n", m.NoiseProfile.CrestFactor)
		} else if len(m.SilenceRegions) > 0 {
			// Show first silence region even if profile extraction failed
			r := m.SilenceRegions[0]
			fmt.Fprintf(f, "Silence Detected:    %.1fs at %.1fs (no profile extracted)\n",
				r.Duration.Seconds(), r.Start.Seconds())
		} else {
			// No silence candidates found at all
			fmt.Fprintf(f, "Silence Candidates:  NONE FOUND\n")
			fmt.Fprintf(f, "  No silence regions detected in audio. Noise profiling unavailable.\n")
		}
	}
	fmt.Fprintf(f, "Sample Rate:         %d Hz\n", data.SampleRate)
	fmt.Fprintf(f, "Channels:            %d (%s)\n", data.Channels, channelName(data.Channels))
	fmt.Fprintln(f, "")

	// Pass 2: Filter Chain (in processing order)
	if data.Result != nil && data.Result.Config != nil {
		formatFilterChain(f, data.Result.Config, data.Result.Measurements)
		fmt.Fprintln(f, "")
	}

	// Pass 2: Output Analysis
	writeSection(f, "Pass 2: Output Analysis")
	if data.Result != nil {
		fmt.Fprintf(f, "Output File:         %s\n", filepath.Base(data.OutputPath))

		if data.Result.OutputMeasurements != nil {
			om := data.Result.OutputMeasurements
			m := data.Result.Measurements // Input measurements for comparison

			if m != nil {
				// Loudness measurements from ebur128
				fmt.Fprintf(f, "Integrated Loudness: %.1f LUFS %s\n", om.OutputI, formatComparison(om.OutputI, m.InputI, "LUFS", 1))
				fmt.Fprintf(f, "Momentary Loudness:  %.1f LUFS %s\n", om.MomentaryLoudness, formatComparison(om.MomentaryLoudness, m.MomentaryLoudness, "LUFS", 1))
				fmt.Fprintf(f, "Short-term Loudness: %.1f LUFS %s\n", om.ShortTermLoudness, formatComparison(om.ShortTermLoudness, m.ShortTermLoudness, "LUFS", 1))
				fmt.Fprintf(f, "True Peak:           %.1f dBTP %s\n", om.OutputTP, formatComparison(om.OutputTP, m.InputTP, "dBTP", 1))
				fmt.Fprintf(f, "Sample Peak:         %.1f dBFS %s\n", om.SamplePeak, formatComparison(om.SamplePeak, m.SamplePeak, "dBFS", 1))
				fmt.Fprintf(f, "Loudness Range:      %.1f LU %s\n", om.OutputLRA, formatComparison(om.OutputLRA, m.InputLRA, "LU", 1))

				// Noise floor comparison
				if om.AstatsNoiseFloor != 0 && !math.IsInf(om.AstatsNoiseFloor, -1) {
					fmt.Fprintf(f, "FFmpeg Noise Floor:  %.1f dB %s\n", om.AstatsNoiseFloor, formatComparison(om.AstatsNoiseFloor, m.AstatsNoiseFloor, "dB", 1))
				}
				if om.NoiseFloorCount != 0 {
					fmt.Fprintf(f, "Noise Floor Count:   %.0f samples %s\n", om.NoiseFloorCount, formatComparisonNoUnit(om.NoiseFloorCount, m.NoiseFloorCount, 0))
				}

				// Dynamic range and level statistics
				fmt.Fprintf(f, "Dynamic Range:       %s\n", formatDynamicRangeComparison(om.DynamicRange, m.DynamicRange))
				fmt.Fprintf(f, "RMS Level:           %.1f dBFS %s\n", om.RMSLevel, formatComparison(om.RMSLevel, m.RMSLevel, "dBFS", 1))
				fmt.Fprintf(f, "RMS Peak:            %.1f dBFS %s\n", om.RMSPeak, formatComparison(om.RMSPeak, m.RMSPeak, "dBFS", 1))
				fmt.Fprintf(f, "RMS Trough:          %.1f dBFS %s\n", om.RMSTrough, formatComparison(om.RMSTrough, m.RMSTrough, "dBFS", 1))
				fmt.Fprintf(f, "Peak Level:          %.1f dBFS %s\n", om.PeakLevel, formatComparison(om.PeakLevel, m.PeakLevel, "dBFS", 1))
				if om.MinLevel != 0 {
					fmt.Fprintf(f, "Min Level:           %.1f dBFS %s\n", om.MinLevel, formatComparison(om.MinLevel, m.MinLevel, "dBFS", 1))
				}
				if om.MaxLevel != 0 {
					fmt.Fprintf(f, "Max Level:           %.1f dBFS %s\n", om.MaxLevel, formatComparison(om.MaxLevel, m.MaxLevel, "dBFS", 1))
				}
				if om.CrestFactor > 0 {
					fmt.Fprintf(f, "Crest Factor:        %s\n", formatCrestFactorComparison(om.CrestFactor, m.CrestFactor))
				}

				// Spectral analysis (aspectralstats measurements) with characteristic interpretations
				inputSpectral := spectralFromMeasurements(m)
				writeSpectralMetrics(f, spectralFromOutputMeasurements(om), &inputSpectral)

				// Signal quality comparison
				fmt.Fprintln(f, "")
				fmt.Fprintln(f, "Signal Quality:")
				if om.BitDepth > 0 {
					fmt.Fprintf(f, "  Effective Bit Depth: %.1f bits %s\n", om.BitDepth, formatComparisonNoUnit(om.BitDepth, m.BitDepth, 1))
				}
				if om.NumberOfSamples > 0 {
					fmt.Fprintf(f, "  Total Samples:       %.0f\n", om.NumberOfSamples)
				}
				if om.DCOffset != 0 || m.DCOffset != 0 {
					fmt.Fprintf(f, "  DC Offset:           %.6f %s\n", om.DCOffset, formatComparisonNoUnit(om.DCOffset, m.DCOffset, 6))
				}
				if om.FlatFactor > 0 || m.FlatFactor > 0 {
					fmt.Fprintf(f, "  Flat Factor:         %.1f %s\n", om.FlatFactor, formatComparisonNoUnit(om.FlatFactor, m.FlatFactor, 1))
				}
				if om.Entropy > 0 {
					fmt.Fprintf(f, "  Signal Entropy:      %.3f %s\n", om.Entropy, formatComparisonNoUnit(om.Entropy, m.Entropy, 3))
				}
				if om.ZeroCrossingsRate > 0 {
					fmt.Fprintf(f, "  Zero Crossings Rate: %.4f %s\n", om.ZeroCrossingsRate, formatComparisonNoUnit(om.ZeroCrossingsRate, m.ZeroCrossingsRate, 4))
				}
				if om.ZeroCrossings > 0 {
					fmt.Fprintf(f, "  Zero Crossings:      %.0f total\n", om.ZeroCrossings)
				}

				// Sample variation comparison
				if om.MaxDifference > 0 || m.MaxDifference > 0 {
					fmt.Fprintln(f, "")
					fmt.Fprintln(f, "Sample Variation:")
					if om.MaxDifference > 0 {
						maxDiffPercent := (om.MaxDifference / 32768.0) * 100.0
						inputMaxDiffPercent := (m.MaxDifference / 32768.0) * 100.0
						fmt.Fprintf(f, "  Max Difference:      %.1f%% FS %s\n", maxDiffPercent, formatComparison(maxDiffPercent, inputMaxDiffPercent, "%%", 1))
					}
					if om.MinDifference > 0 {
						minDiffPercent := (om.MinDifference / 32768.0) * 100.0
						inputMinDiffPercent := (m.MinDifference / 32768.0) * 100.0
						fmt.Fprintf(f, "  Min Difference:      %.4f%% FS %s\n", minDiffPercent, formatComparison(minDiffPercent, inputMinDiffPercent, "%%", 4))
					}
					if om.MeanDifference > 0 {
						meanDiffPercent := (om.MeanDifference / 32768.0) * 100.0
						inputMeanDiffPercent := (m.MeanDifference / 32768.0) * 100.0
						fmt.Fprintf(f, "  Mean Difference:     %.4f%% FS %s\n", meanDiffPercent, formatComparison(meanDiffPercent, inputMeanDiffPercent, "%%", 4))
					}
					if om.RMSDifference > 0 {
						rmsDiffPercent := (om.RMSDifference / 32768.0) * 100.0
						inputRMSDiffPercent := (m.RMSDifference / 32768.0) * 100.0
						fmt.Fprintf(f, "  RMS Difference:      %.4f%% FS %s\n", rmsDiffPercent, formatComparison(rmsDiffPercent, inputRMSDiffPercent, "%%", 4))
					}
				}
			} else {
				fmt.Fprintf(f, "Integrated Loudness: %.1f LUFS\n", om.OutputI)
				fmt.Fprintf(f, "True Peak:           %.1f dBTP\n", om.OutputTP)
				fmt.Fprintf(f, "Loudness Range:      %.1f LU\n", om.OutputLRA)
				fmt.Fprintf(f, "Dynamic Range:       %s\n", formatDynamicRange(om.DynamicRange))
				fmt.Fprintf(f, "RMS Level:           %.1f dBFS\n", om.RMSLevel)
				fmt.Fprintf(f, "Peak Level:          %.1f dBFS\n", om.PeakLevel)

				// Additional astats measurements
				if om.RMSTrough != 0 {
					fmt.Fprintf(f, "RMS Trough:          %.1f dBFS\n", om.RMSTrough)
				}
				if om.RMSPeak != 0 {
					fmt.Fprintf(f, "RMS Peak:            %.1f dBFS\n", om.RMSPeak)
				}
				if om.CrestFactor != 0 {
					fmt.Fprintf(f, "Crest Factor:        %s\n", formatCrestFactor(om.CrestFactor))
				}
				if om.Entropy != 0 {
					fmt.Fprintf(f, "Signal Entropy:      %.3f\n", om.Entropy)
				}
				if om.MinLevel != 0 {
					minLevelPercent := (om.MinLevel / 32768.0) * 100.0
					fmt.Fprintf(f, "Min Level:           %.4f%% FS\n", minLevelPercent)
				}
				if om.MaxLevel != 0 {
					maxLevelPercent := (om.MaxLevel / 32768.0) * 100.0
					fmt.Fprintf(f, "Max Level:           %.4f%% FS\n", maxLevelPercent)
				}
				if om.AstatsNoiseFloor != 0 {
					fmt.Fprintf(f, "Astats Noise Floor:  %.1f dBFS\n", om.AstatsNoiseFloor)
				}
				if om.NoiseFloorCount != 0 {
					fmt.Fprintf(f, "Noise Floor Count:   %.0f samples\n", om.NoiseFloorCount)
				}
				if om.BitDepth != 0 {
					fmt.Fprintf(f, "Bit Depth:           %.1f bits\n", om.BitDepth)
				}
				if om.NumberOfSamples != 0 {
					fmt.Fprintf(f, "Sample Count:        %.0f\n", om.NumberOfSamples)
				}

				// Additional ebur128 momentary/short-term
				if om.MomentaryLoudness != 0 {
					fmt.Fprintf(f, "Momentary Loudness:  %.1f LUFS\n", om.MomentaryLoudness)
				}
				if om.ShortTermLoudness != 0 {
					fmt.Fprintf(f, "Short-term Loudness: %.1f LUFS\n", om.ShortTermLoudness)
				}
				if om.SamplePeak != 0 {
					fmt.Fprintf(f, "Sample Peak:         %.1f dBFS\n", om.SamplePeak)
				}

				// Spectral analysis (aspectralstats measurements) with characteristic interpretations
				writeSpectralMetrics(f, spectralFromOutputMeasurements(om), nil)
			}
			if om.ZeroCrossingsRate > 0 {
				if m != nil && m.ZeroCrossingsRate > 0 {
					fmt.Fprintf(f, "Zero Crossings Rate: %.4f %s\n", om.ZeroCrossingsRate, formatComparisonNoUnit(om.ZeroCrossingsRate, m.ZeroCrossingsRate, 4))
				} else {
					fmt.Fprintf(f, "Zero Crossings Rate: %.4f\n", om.ZeroCrossingsRate)
				}
			}
			if om.MaxDifference > 0 {
				maxDiffPercent := (om.MaxDifference / 32768.0) * 100.0
				if m != nil && m.MaxDifference > 0 {
					inputMaxDiffPercent := (m.MaxDifference / 32768.0) * 100.0
					fmt.Fprintf(f, "Max Difference:      %.1f%% FS %s\n", maxDiffPercent, formatComparison(maxDiffPercent, inputMaxDiffPercent, "%% FS", 1))
				} else {
					fmt.Fprintf(f, "Max Difference:      %.1f%% FS (transient indicator)\n", maxDiffPercent)
				}
			}

			// Show silence sample comparison (same region as Pass 1)
			if om.SilenceSample != nil && data.Result.Measurements != nil && data.Result.Measurements.NoiseProfile != nil {
				ss := om.SilenceSample
				np := data.Result.Measurements.NoiseProfile
				fmt.Fprintf(f, "Silence Sample:      %.1fs at %.1fs\n", ss.Duration.Seconds(), ss.Start.Seconds())

				// Noise Floor with delta if changed
				if math.Abs(ss.NoiseFloor-np.MeasuredNoiseFloor) < 0.05 {
					fmt.Fprintf(f, "  Noise Floor:       %.1f dBFS (unchanged)\n", ss.NoiseFloor)
				} else {
					fmt.Fprintf(f, "  Noise Floor:       %.1f dBFS (was %.1f dBFS, %+.1f dB)\n",
						ss.NoiseFloor, np.MeasuredNoiseFloor, ss.NoiseFloor-np.MeasuredNoiseFloor)
				}

				// Peak Level with delta if changed
				if math.Abs(ss.PeakLevel-np.PeakLevel) < 0.05 {
					fmt.Fprintf(f, "  Peak Level:        %.1f dBFS (unchanged)\n", ss.PeakLevel)
				} else {
					fmt.Fprintf(f, "  Peak Level:        %.1f dBFS (was %.1f dBFS, %+.1f dB)\n",
						ss.PeakLevel, np.PeakLevel, ss.PeakLevel-np.PeakLevel)
				}

				// Crest Factor
				if math.Abs(ss.CrestFactor-np.CrestFactor) < 0.05 {
					fmt.Fprintf(f, "  Crest Factor:      %.1f dB (unchanged)\n", ss.CrestFactor)
				} else {
					fmt.Fprintf(f, "  Crest Factor:      %.1f dB %s\n", ss.CrestFactor, formatComparison(ss.CrestFactor, np.CrestFactor, "dB", 1))
				}

				if ss.Entropy > 0 {
					// Classify noise type based on entropy
					noiseType := "broadband (hiss)"
					if ss.Entropy < 0.7 {
						noiseType = "tonal (hum/buzz)"
					} else if ss.Entropy < 0.9 {
						noiseType = "mixed"
					}
					// Show with comparison to input
					inputNoiseType := "broadband (hiss)"
					if np.Entropy < 0.7 {
						inputNoiseType = "tonal (hum/buzz)"
					} else if np.Entropy < 0.9 {
						inputNoiseType = "mixed"
					}
					if noiseType == inputNoiseType && math.Abs(ss.Entropy-np.Entropy) < 0.0005 {
						fmt.Fprintf(f, "  Noise Character:   %s (unchanged)\n", noiseType)
					} else if noiseType == inputNoiseType {
						fmt.Fprintf(f, "  Noise Character:   %s (entropy %.3f, was %.3f)\n", noiseType, ss.Entropy, np.Entropy)
					} else {
						fmt.Fprintf(f, "  Noise Character:   %s (was %s)\n", noiseType, inputNoiseType)
					}
				}
			}
		} else {
			fmt.Fprintln(f, "Note: Output measurements not available")
		}
	}
	fmt.Fprintln(f, "")

	// Processing Time
	fmt.Fprintln(f, "Processing Time")
	fmt.Fprintln(f, "---------------")
	fmt.Fprintf(f, "Pass 1 (Analysis):   %s\n", formatDuration(data.Pass1Time))
	fmt.Fprintf(f, "Pass 2 (Processing): %s\n", formatDuration(data.Pass2Time))
	totalTime := data.EndTime.Sub(data.StartTime)
	fmt.Fprintf(f, "Total Time:          %s\n", formatDuration(totalTime))

	if data.DurationSecs > 0 {
		audioDuration := time.Duration(data.DurationSecs * float64(time.Second))
		rtf := float64(audioDuration) / float64(totalTime)
		fmt.Fprintf(f, "Real-time Factor:    %.0fx\n", rtf)
	}
	fmt.Fprintln(f, "")

	return nil
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}

	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60

	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}

	hours := minutes / 60
	minutes = minutes % 60
	return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
}

// channelName returns a human-readable channel name
func channelName(channels int) string {
	switch channels {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	default:
		return fmt.Sprintf("%d channels", channels)
	}
}

// formatFilterChain generates the filter chain section of the report.
// Iterates over filters in chain order, showing enabled/disabled status,
// key parameters, and adaptive rationale for each filter.
func formatFilterChain(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements) {
	fmt.Fprintln(f, "Filter Chain (in processing order)")
	fmt.Fprintln(f, "------------------------------------")

	for i, filterID := range cfg.FilterOrder {
		prefix := fmt.Sprintf("%2d. ", i+1)
		formatFilter(f, filterID, cfg, m, prefix)
	}
}

// formatFilter outputs details for a single filter
func formatFilter(f *os.File, filterID processor.FilterID, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	switch filterID {
	case processor.FilterDownmix:
		formatDownmixFilter(f, cfg, prefix)
	case processor.FilterAnalysis:
		formatAnalysisFilter(f, cfg, prefix)
	case processor.FilterSilenceDetect:
		formatSilenceDetectFilter(f, cfg, prefix)
	case processor.FilterResample:
		formatResampleFilter(f, cfg, prefix)
	case processor.FilterDS201HighPass:
		formatDS201HighpassFilter(f, cfg, m, prefix)
	case processor.FilterDS201LowPass:
		formatDS201LowPassFilter(f, cfg, m, prefix)
	case processor.FilterDC1Declick:
		formatDC1DeclickFilter(f, cfg, prefix)
	case processor.FilterDNS1500:
		formatDNS1500Filter(f, cfg, m, prefix)
	case processor.FilterDolbySR:
		formatDolbySRFilter(f, cfg, m, prefix)
	case processor.FilterArnndn:
		formatArnndnFilter(f, cfg, m, prefix)
	case processor.FilterDS201Gate:
		formatDS201GateFilter(f, cfg, m, prefix)
	case processor.FilterLA2ACompressor:
		formatLA2ACompressorFilter(f, cfg, m, prefix)
	case processor.FilterDeesser:
		formatDeesserFilter(f, cfg, m, prefix)
	case processor.FilterAlimiter:
		formatAlimiterFilter(f, cfg, prefix)
	default:
		fmt.Fprintf(f, "%s%s: (unknown filter)\n", prefix, filterID)
	}
}

// formatDS201HighpassFilter outputs DS201-inspired highpass filter details
func formatDS201HighpassFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DS201HPEnabled {
		fmt.Fprintf(f, "%sDS201 highpass: DISABLED\n", prefix)
		return
	}

	// Show slope (6dB/oct for gentle, 12dB/oct for standard)
	slope := "12dB/oct"
	if cfg.DS201HPPoles == 1 {
		slope = "6dB/oct"
	}

	// Build header with all relevant parameters
	header := fmt.Sprintf("%sDS201 highpass: %.0f Hz cutoff (%s", prefix, cfg.DS201HPFreq, slope)

	// Show Q if not default Butterworth
	if cfg.DS201HPWidth > 0 && cfg.DS201HPWidth != 0.707 {
		header += fmt.Sprintf(", Q=%.2f", cfg.DS201HPWidth)
	}

	// Show transform if specified
	if cfg.DS201HPTransform == "tdii" {
		header += ", tdii"
	} else if cfg.DS201HPTransform != "" {
		header += ", " + cfg.DS201HPTransform
	}

	header += ")"
	fmt.Fprintln(f, header)

	// Show adaptive rationale
	if m != nil && m.SpectralCentroid > 0 {
		voiceType := "normal"
		if m.SpectralCentroid > 6000 {
			voiceType = "bright"
		} else if m.SpectralCentroid < 4000 {
			voiceType = "dark/warm"
		}
		fmt.Fprintf(f, "        Rationale: %s voice (centroid %.0f Hz)\n", voiceType, m.SpectralCentroid)

		// Show warm voice protection if applicable (using mix)
		if cfg.DS201HPMix > 0 && cfg.DS201HPMix < 1.0 {
			reason := "warm voice"
			if m.SpectralDecrease < -0.08 {
				reason = "very warm voice"
			} else if m.SpectralSkewness > 1.0 {
				reason = "LF emphasis"
			}
			fmt.Fprintf(f, "        Mix: %.0f%% (%s — blending filtered with dry signal)\n", cfg.DS201HPMix*100, reason)
		}

		// Show why low frequency was chosen for warm voices
		if cfg.DS201HPFreq <= 40 {
			fmt.Fprintf(f, "        Frequency: %.0f Hz (subsonic only — protecting bass foundation)\n", cfg.DS201HPFreq)
		}

		// Show gentle slope explanation
		if cfg.DS201HPPoles == 1 {
			fmt.Fprintf(f, "        Slope: 6dB/oct (gentle rolloff — preserving warmth)\n")
		}

		// Show noise character if tonal (explains why no boost)
		if m.NoiseProfile != nil && m.NoiseProfile.Entropy < 0.5 {
			fmt.Fprintf(f, "        Note: no LF boost (tonal noise, entropy %.3f — DS201 hum filter handles it)\n", m.NoiseProfile.Entropy)
		}
	}

	// DS201HighPass is composite: also show hum notch filter details if harmonics > 0
	if cfg.DS201HumHarmonics > 0 {
		formatDS201HumFilterInternal(f, cfg, m, prefix)
	}
}

// formatDS201HumFilterInternal outputs DS201-inspired hum notch filter details (called from DS201HighPass composite)
func formatDS201HumFilterInternal(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	// Note: enabled check is done by caller (formatDS201HighpassFilter)

	// Build the header with width and transform info
	transformInfo := ""
	if cfg.DS201HumTransform == "tdii" {
		transformInfo = ", tdii"
	} else if cfg.DS201HumTransform != "" {
		transformInfo = ", " + cfg.DS201HumTransform
	}
	fmt.Fprintf(f, "%sDS201 hum filter: %.0f Hz + %d harmonics (%.1f Hz wide%s)\n",
		prefix, cfg.DS201HumFrequency, cfg.DS201HumHarmonics, cfg.DS201HumWidth, transformInfo)

	if m != nil && m.NoiseProfile != nil {
		fmt.Fprintf(f, "        Rationale: tonal noise detected (entropy %.3f < 0.7)\n", m.NoiseProfile.Entropy)

		// Explain reduced harmonics for warm voices
		if cfg.DS201HumHarmonics <= 2 {
			isWarmSkewness := m.SpectralSkewness > 1.0
			isWarmDecrease := m.SpectralDecrease < -0.02
			if isWarmSkewness || isWarmDecrease {
				reason := ""
				if isWarmSkewness && isWarmDecrease {
					reason = fmt.Sprintf("skewness %.2f, decrease %.3f", m.SpectralSkewness, m.SpectralDecrease)
				} else if isWarmSkewness {
					reason = fmt.Sprintf("skewness %.2f", m.SpectralSkewness)
				} else {
					reason = fmt.Sprintf("decrease %.3f", m.SpectralDecrease)
				}
				fmt.Fprintf(f, "        Harmonics: reduced to %d (warm voice: %s — protecting vocal fundamentals)\n",
					cfg.DS201HumHarmonics, reason)
			}
		}

		// Explain notch width choice (always show for completeness)
		if cfg.DS201HumWidth <= 0.3 {
			fmt.Fprintf(f, "        Width: %.1f Hz (very narrow — warm voice protection)\n", cfg.DS201HumWidth)
		} else if cfg.DS201HumWidth < 1.0 {
			fmt.Fprintf(f, "        Width: %.1f Hz (narrow surgical notch — very tonal hum)\n", cfg.DS201HumWidth)
		} else {
			fmt.Fprintf(f, "        Width: %.1f Hz (standard surgical notch)\n", cfg.DS201HumWidth)
		}

		// Explain transform type
		if cfg.DS201HumTransform == "tdii" {
			fmt.Fprintf(f, "        Transform: TDII (transposed direct form II — best floating-point accuracy)\n")
		} else if cfg.DS201HumTransform != "" && cfg.DS201HumTransform != "di" {
			fmt.Fprintf(f, "        Transform: %s\n", cfg.DS201HumTransform)
		}

		// Explain mix if not full wet
		if cfg.DS201HumMix > 0 && cfg.DS201HumMix < 1.0 {
			mixReason := "warm voice"
			if cfg.DS201HumMix <= 0.7 {
				mixReason = "very warm voice"
			}
			fmt.Fprintf(f, "        Mix: %.0f%% (%s — blending filtered with dry signal)\n", cfg.DS201HumMix*100, mixReason)
		}
	}
}

// formatDS201LowPassFilter outputs DS201-inspired low-pass filter details
func formatDS201LowPassFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DS201LPEnabled {
		// Show reason for being disabled (pass-through mode)
		if cfg.DS201LPReason != "" {
			fmt.Fprintf(f, "%sDS201 lowpass: DISABLED (%s)\n", prefix, cfg.DS201LPReason)
		} else {
			fmt.Fprintf(f, "%sDS201 lowpass: DISABLED\n", prefix)
		}
		return
	}

	// Show slope (6dB/oct for gentle, 12dB/oct for standard)
	slope := "12dB/oct"
	if cfg.DS201LPPoles == 1 {
		slope = "6dB/oct"
	}

	// Build header with all relevant parameters
	header := fmt.Sprintf("%sDS201 lowpass: %.0f Hz cutoff (%s", prefix, cfg.DS201LPFreq, slope)

	// Show Q if not default Butterworth
	if cfg.DS201LPWidth > 0 && cfg.DS201LPWidth != 0.707 {
		header += fmt.Sprintf(", Q=%.2f", cfg.DS201LPWidth)
	}

	// Show transform if specified
	if cfg.DS201LPTransform == "tdii" {
		header += ", tdii"
	} else if cfg.DS201LPTransform != "" {
		header += ", " + cfg.DS201LPTransform
	}

	// Show mix if not full wet
	if cfg.DS201LPMix > 0 && cfg.DS201LPMix < 1.0 {
		header += fmt.Sprintf(", mix %.0f%%", cfg.DS201LPMix*100)
	}

	header += ")"
	fmt.Fprintln(f, header)

	// Show rationale
	if cfg.DS201LPReason != "" {
		fmt.Fprintf(f, "        Rationale: %s\n", cfg.DS201LPReason)
	}

	// Show content type detection metrics
	if m != nil {
		fmt.Fprintf(f, "        Content type: %s (kurtosis %.1f, flatness %.3f, flux %.4f)\n",
			cfg.DS201LPContentType.String(), m.SpectralKurtosis, m.SpectralFlatness, m.SpectralFlux)

		// Show the triggering metric details
		switch cfg.DS201LPReason {
		case "rolloff/centroid gap":
			fmt.Fprintf(f, "        Rolloff/centroid ratio: %.2f > 2.5 (rolloff %.0f Hz, centroid %.0f Hz)\n",
				cfg.DS201LPRolloffRatio, m.SpectralRolloff, m.SpectralCentroid)
		case "flat spectral slope":
			fmt.Fprintf(f, "        Spectral slope: %.2e > -1e-05 (unusual HF emphasis)\n", m.SpectralSlope)
		case "high ZCR with low centroid":
			fmt.Fprintf(f, "        ZCR: %.4f > 0.10, centroid %.0f Hz < 4000 Hz (HF noise pattern)\n",
				m.ZeroCrossingsRate, m.SpectralCentroid)
		}
	}
}

// formatDC1DeclickFilter outputs CEDAR DC-1-inspired declicker filter details
func formatDC1DeclickFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.DC1DeclickEnabled {
		if cfg.DC1DeclickReason != "" {
			fmt.Fprintf(f, "%sDC1 Declick: DISABLED (%s)\n", prefix, cfg.DC1DeclickReason)
		} else {
			fmt.Fprintf(f, "%sDC1 Declick: DISABLED\n", prefix)
		}
		return
	}

	method := "overlap-save"
	if cfg.DC1DeclickMethod == "a" {
		method = "overlap-add"
	}
	fmt.Fprintf(f, "%sDC1 Declick: ENABLED\n", prefix)
	fmt.Fprintf(f, "        Threshold: %.0f (1=aggressive, 8=conservative)\n", cfg.DC1DeclickThreshold)
	fmt.Fprintf(f, "        Window: %.0fms, Overlap: %.0f%%, AR Order: %.0f%%\n", cfg.DC1DeclickWindow, cfg.DC1DeclickOverlap, cfg.DC1DeclickAROrder)
	fmt.Fprintf(f, "        Method: %s\n", method)
	if cfg.DC1DeclickReason != "" {
		fmt.Fprintf(f, "        Reason: %s\n", cfg.DC1DeclickReason)
	}
}

// formatDNS1500Filter outputs CEDAR DNS-1500-inspired noise reduction filter details
// Uses afftdn with inline noise learning via asendcmd during detected silence
func formatDNS1500Filter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DNS1500Enabled {
		if cfg.DolbySREnabled {
			fmt.Fprintf(f, "%sDNS-1500: DISABLED (DolbySR forced on)\n", prefix)
		} else {
			fmt.Fprintf(f, "%sDNS-1500: DISABLED (no silence detected, using DolbySR fallback)\n", prefix)
		}
		return
	}

	// Header with key parameters
	fmt.Fprintf(f, "%sDNS-1500: Noise Reduction (inline noise learning)\n", prefix)
	fmt.Fprintf(f, "        Noise Reduction: %.1f dB\n", cfg.DNS1500NoiseReduce)
	fmt.Fprintf(f, "        Noise Floor: %.1f dB\n", cfg.DNS1500NoiseFloor)
	fmt.Fprintf(f, "        Track Noise: %v\n", cfg.DNS1500TrackNoise)
	fmt.Fprintf(f, "        Adaptivity: %.2f\n", cfg.DNS1500Adaptivity)
	fmt.Fprintf(f, "        Gain Smooth: %d\n", cfg.DNS1500GainSmooth)
	fmt.Fprintf(f, "        Residual Floor: %.1f dB\n", cfg.DNS1500ResidFloor)
	fmt.Fprintf(f, "        Silence Window: %.3fs – %.3fs\n", cfg.DNS1500SilenceStart, cfg.DNS1500SilenceEnd)

	// Show adaptive rationale
	if m != nil && m.NoiseProfile != nil {
		np := m.NoiseProfile

		// Noise character description
		var noiseChar string
		switch {
		case np.SpectralFlatness > 0.6:
			noiseChar = "broadband (hiss)"
		case np.SpectralFlatness > 0.4:
			noiseChar = "mixed"
		default:
			noiseChar = "tonal (hum/bleed)"
		}

		// LRA-based adaptivity description
		var adaptivityDesc string
		switch {
		case m.InputLRA < 6.0:
			adaptivityDesc = "fast (uniform material)"
		case m.InputLRA > 15.0:
			adaptivityDesc = "slow (dynamic material)"
		default:
			adaptivityDesc = "moderate"
		}

		fmt.Fprintf(f, "        Rationale: measured floor %.1f dBFS, %s noise → %.1f dB reduction\n",
			np.MeasuredNoiseFloor, noiseChar, cfg.DNS1500NoiseReduce)
		fmt.Fprintf(f, "        Adaptivity: LRA %.1f LU → %s adaptation\n", m.InputLRA, adaptivityDesc)
		fmt.Fprintf(f, "        DNS-1500 philosophy: learn noise from silence, track continuously\n")
	}
}

// formatDolbySRFilter outputs Dolby SR-inspired denoise filter details
// Uses 6-band mcompand multiband expander with FLAT reduction curve
func formatDolbySRFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DolbySREnabled {
		fmt.Fprintf(f, "%sDolby SR: DISABLED\n", prefix)
		return
	}

	// Header: filter name and expansion level
	fmt.Fprintf(f, "%sDolby SR: Noise Reduction (6-band voice-protective expander)\n", prefix)
	fmt.Fprintf(f, "        Expansion: %.0f dB (FLAT reduction curve)\n", cfg.DolbySRExpansionDB)
	fmt.Fprintf(f, "        Threshold: %.0f dB\n", cfg.DolbySRThresholdDB)

	// Show 6-band configuration
	if len(cfg.DolbySRBands) == 6 {
		fmt.Fprintf(f, "        Bands:\n")
		bandNames := []string{"Sub-bass", "Chest", "Voice F1", "Voice F2", "Presence", "Air"}
		for i, band := range cfg.DolbySRBands {
			bandExp := cfg.DolbySRExpansionDB * band.ScalePercent / 100.0
			fmt.Fprintf(f, "          %s (0-%d Hz): %.0fdB, %.0fms/%.0fms, knee %.0f\n",
				bandNames[i], int(band.CrossoverHz), bandExp,
				band.Attack*1000, band.Decay*1000, band.SoftKnee)
		}
	}

	// Show adaptive rationale
	if m != nil {
		// Classify source based on RMS trough (aligned with lockstep threshold+expansion tiers)
		// Tiers: < -85 dB (clean), -85 to -80 dB (moderate), > -80 dB (noisy)
		var severityDesc string
		switch {
		case m.RMSTrough < -85:
			severityDesc = "clean"
		case m.RMSTrough < -80:
			severityDesc = "moderate"
		default:
			severityDesc = "noisy"
		}

		// Lockstep rationale (threshold + expansion tuned together)
		var thresholdRationale string
		switch {
		case m.RMSTrough < -85:
			thresholdRationale = "gentle treatment"
		case m.RMSTrough < -80:
			thresholdRationale = "balanced treatment"
		default:
			thresholdRationale = "aggressive treatment"
		}

		fmt.Fprintf(f, "        Rationale: %s source (trough %.1f dBFS) → %.0f dB expansion, %.0f dB threshold (%s)\n",
			severityDesc, m.RMSTrough, cfg.DolbySRExpansionDB, cfg.DolbySRThresholdDB, thresholdRationale)

		// SR philosophy note
		fmt.Fprintf(f, "        SR philosophy: 6-band voice-protective, FLAT curve (gate handles silence)\n")
	}
}

// formatArnndnFilter outputs arnndn filter details
func formatArnndnFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.ArnnDnEnabled {
		fmt.Fprintf(f, "%sarnndn: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sarnndn: mix %.0f%%\n", prefix, cfg.ArnnDnMix*100)

	// Show rationale with adjustments
	if m != nil {
		var adjustments []string

		// Base mix explanation (matches thresholds in adaptive.go calculateArnnDnMix)
		var baseMixDesc string
		switch {
		case m.NoiseFloor > -50:
			baseMixDesc = "noisy"
		case m.NoiseFloor > -65:
			baseMixDesc = "moderate"
		case m.NoiseFloor > -75:
			baseMixDesc = "fairly clean"
		default:
			baseMixDesc = "very clean"
		}

		fmt.Fprintf(f, "        Base: %s (noise floor %.1f dBFS)\n", baseMixDesc, m.NoiseFloor)

		// Adjustments applied
		if m.SpectralKurtosis > 8 {
			adjustments = append(adjustments, fmt.Sprintf("-10%% kurtosis %.1f", m.SpectralKurtosis))
		}
		// MaxDifference is in sample units (0-32768 for 16-bit); normalise to 0-1
		maxDiffNorm := m.MaxDifference / 32768.0
		if maxDiffNorm > 0.25 {
			adjustments = append(adjustments, fmt.Sprintf("-10%% transients %.0f%%", maxDiffNorm*100))
		}
		if m.InputLRA > 15 {
			adjustments = append(adjustments, fmt.Sprintf("-5%% LRA %.1f LU", m.InputLRA))
		}
		if m.SpectralFlatness > 0.5 {
			adjustments = append(adjustments, fmt.Sprintf("+10%% flatness %.2f", m.SpectralFlatness))
		}
		if m.NoiseProfile != nil && m.NoiseProfile.Entropy > 0.5 {
			adjustments = append(adjustments, fmt.Sprintf("+10%% entropy %.2f", m.NoiseProfile.Entropy))
		}

		if len(adjustments) > 0 {
			fmt.Fprintf(f, "        Adjustments: %s\n", joinWithComma(adjustments))
		}
	}
}

// joinWithComma joins string slice with comma separator
func joinWithComma(items []string) string {
	result := ""
	for i, item := range items {
		if i > 0 {
			result += ", "
		}
		result += item
	}
	return result
}

// formatDS201GateFilter outputs DS201-inspired gate filter details
func formatDS201GateFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DS201GateEnabled {
		fmt.Fprintf(f, "%sDS201 gate: DISABLED\n", prefix)
		return
	}

	thresholdDB := linearToDb(cfg.DS201GateThreshold)
	rangeDB := linearToDb(cfg.DS201GateRange)

	detection := cfg.DS201GateDetection
	if detection == "" {
		detection = "rms"
	}

	// Show mode indicator if gentle mode is active
	modeNote := ""
	if cfg.DS201GateGentleMode {
		modeNote = " [gentle mode]"
	}

	fmt.Fprintf(f, "%sDS201 gate: threshold %.1f dB, ratio %.1f:1, detection %s%s\n", prefix, thresholdDB, cfg.DS201GateRatio, detection, modeNote)
	fmt.Fprintf(f, "        Timing: attack %.2fms, release %.0fms (soft expander)\n", cfg.DS201GateAttack, cfg.DS201GateRelease)
	fmt.Fprintf(f, "        Range: %.1f dB reduction, knee %.1f\n", rangeDB, cfg.DS201GateKnee)

	// Show makeup gain if applied (> 1.0 linear = 0 dB)
	if cfg.DS201GateMakeup > 1.01 { // Small tolerance for floating point
		makeupDB := linearToDb(cfg.DS201GateMakeup)
		fmt.Fprintf(f, "        Makeup: +%.1f dB (LUFS gap recovery)\n", makeupDB)
	}

	// Show rationale based on measurements
	if m != nil {
		var rationale []string

		// Threshold rationale - must match logic in calculateDS201GateThreshold
		// Peak reference is used when: crest > 20 AND peak != 0 AND lufsGap < 25
		lufsGap := cfg.TargetI - m.InputI
		if lufsGap < 0 {
			lufsGap = 0
		}
		usePeakRef := m.NoiseProfile != nil &&
			m.NoiseProfile.CrestFactor > 20 &&
			m.NoiseProfile.PeakLevel != 0 &&
			lufsGap < 25

		if usePeakRef {
			rationale = append(rationale, fmt.Sprintf("peak ref %.1f dB (crest %.1f dB)", m.NoiseProfile.PeakLevel, m.NoiseProfile.CrestFactor))
		} else if lufsGap >= 25 && m.NoiseProfile != nil && m.NoiseProfile.CrestFactor > 20 {
			rationale = append(rationale, fmt.Sprintf("noise floor %.1f dB (extreme LUFS gap %.0f dB, ignoring crest)", m.NoiseFloor, lufsGap))
		} else {
			rationale = append(rationale, fmt.Sprintf("noise floor %.1f dB", m.NoiseFloor))
		}

		// Ratio rationale
		if m.InputLRA > 0 {
			lraType := "moderate"
			if m.InputLRA > 15 {
				lraType = "wide"
			} else if m.InputLRA < 10 {
				lraType = "narrow"
			}
			rationale = append(rationale, fmt.Sprintf("LRA %.1f LU (%s)", m.InputLRA, lraType))
		}

		// Noise character for range/detection and release
		// Thresholds: very tonal < 0.10, tonal < 0.12, mixed < 0.16, broadband >= 0.16
		if m.NoiseProfile != nil {
			entropy := m.NoiseProfile.Entropy
			if entropy < 0.10 {
				rationale = append(rationale, fmt.Sprintf("very tonal (entropy %.2f, slow release)", entropy))
			} else if entropy < 0.12 {
				rationale = append(rationale, fmt.Sprintf("tonal (entropy %.2f)", entropy))
			} else if entropy < 0.16 {
				rationale = append(rationale, fmt.Sprintf("mixed (entropy %.2f, faster release)", entropy))
			} else {
				rationale = append(rationale, fmt.Sprintf("broadband-ish (entropy %.2f, fast release)", entropy))
			}
		}

		// Gentle mode rationale - for extreme LUFS gap + low LRA recordings
		if cfg.DS201GateGentleMode {
			rationale = append(rationale, "gentle mode (extreme LUFS gap + low LRA)")
		}

		if len(rationale) > 0 {
			fmt.Fprintf(f, "        Rationale: %s\n", strings.Join(rationale, ", "))
		}
	}
}

// formatLA2ACompressorFilter outputs LA-2A Compressor filter details
func formatLA2ACompressorFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.LA2AEnabled {
		fmt.Fprintf(f, "%sLA-2A Compressor: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sLA-2A Compressor: threshold %.0f dB, ratio %.1f:1\n", prefix, cfg.LA2AThreshold, cfg.LA2ARatio)
	fmt.Fprintf(f, "        Timing: attack %.0fms, release %.0fms\n", cfg.LA2AAttack, cfg.LA2ARelease)
	fmt.Fprintf(f, "        Makeup: %+.0f dB, mix %.0f%%, knee %.1f\n", cfg.LA2AMakeup, cfg.LA2AMix*100, cfg.LA2AKnee)

	// Show rationale
	if m != nil && m.DynamicRange > 0 {
		dynamicsType := "moderate"
		if m.DynamicRange > 30 {
			dynamicsType = "expressive (preserving transients)"
		} else if m.DynamicRange < 20 {
			dynamicsType = "already compressed"
		}
		fmt.Fprintf(f, "        Rationale: DR %.1f dB (%s), LRA %.1f LU\n", m.DynamicRange, dynamicsType, m.InputLRA)
	}
}

// formatDeesserFilter outputs deesser filter details
func formatDeesserFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DeessEnabled || cfg.DeessIntensity == 0 {
		fmt.Fprintf(f, "%sdeesser: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sdeesser: intensity %.0f%%, amount %.0f%%, freq %.0f%%\n",
		prefix, cfg.DeessIntensity*100, cfg.DeessAmount*100, cfg.DeessFreq*100)

	// Show rationale
	if m != nil && m.SpectralCentroid > 0 {
		voiceType := "normal"
		if m.SpectralCentroid > 7000 {
			voiceType = "very bright"
		} else if m.SpectralCentroid > 6000 {
			voiceType = "bright"
		}
		fmt.Fprintf(f, "        Rationale: %s voice (centroid %.0f Hz, rolloff %.0f Hz)\n",
			voiceType, m.SpectralCentroid, m.SpectralRolloff)
	}
}

// formatAlimiterFilter outputs alimiter filter details
func formatAlimiterFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.LimiterEnabled {
		fmt.Fprintf(f, "%salimiter: DISABLED\n", prefix)
		return
	}

	ceilingDB := linearToDb(cfg.LimiterCeiling)
	fmt.Fprintf(f, "%salimiter: ceiling %.1f dBFS\n", prefix, ceilingDB)
	fmt.Fprintf(f, "        Timing: attack %.0fms, release %.0fms\n", cfg.LimiterAttack, cfg.LimiterRelease)
	fmt.Fprintln(f, "        Mode: brick-wall safety limiter")
}

// formatDownmixFilter outputs downmix filter details
func formatDownmixFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.DownmixEnabled {
		fmt.Fprintf(f, "%sdownmix: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sdownmix: stereo → mono (FFmpeg builtin)\n", prefix)
}

// formatAnalysisFilter outputs analysis filter details
func formatAnalysisFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.AnalysisEnabled {
		fmt.Fprintf(f, "%sanalysis: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sanalysis: collect audio measurements (ebur128 + astats + aspectralstats)\n", prefix)
}

// formatSilenceDetectFilter outputs silence detection filter details
func formatSilenceDetectFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.SilenceDetectEnabled {
		fmt.Fprintf(f, "%ssilencedetect: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%ssilencedetect: threshold %.0f dB, min duration %.2fs\n",
		prefix, cfg.SilenceDetectLevel, cfg.SilenceDetectDuration)
}

// formatResampleFilter outputs resample filter details
func formatResampleFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.ResampleEnabled {
		fmt.Fprintf(f, "%sresample: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sresample: %d Hz %s mono, %d samples/frame\n",
		prefix, cfg.ResampleSampleRate, cfg.ResampleFormat, cfg.ResampleFrameSize)
}
