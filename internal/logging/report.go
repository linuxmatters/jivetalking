// Package logging handles generation of analysis reports for processed audio files

package logging

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
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
	fmt.Fprintln(f, "Pass 1: Input Analysis")
	fmt.Fprintln(f, "----------------------")
	if data.Result != nil && data.Result.Measurements != nil {
		m := data.Result.Measurements
		fmt.Fprintf(f, "Integrated Loudness: %.1f LUFS\n", m.InputI)
		fmt.Fprintf(f, "True Peak:           %.1f dBTP\n", m.InputTP)
		fmt.Fprintf(f, "Loudness Range:      %.1f LU\n", m.InputLRA)
		fmt.Fprintf(f, "Noise Floor:         %.1f dB (measured)\n", m.NoiseFloor)
		fmt.Fprintf(f, "Dynamic Range:       %s\n", formatDynamicRange(m.DynamicRange))
		fmt.Fprintf(f, "RMS Level:           %.1f dBFS\n", m.RMSLevel)
		fmt.Fprintf(f, "Peak Level:          %.1f dBFS\n", m.PeakLevel)

		// Spectral analysis (aspectralstats measurements) with characteristic interpretations
		fmt.Fprintf(f, "Spectral Mean:       %s (avg magnitude)\n", formatSpectralValue(m.SpectralMean, 6))
		fmt.Fprintf(f, "Spectral Variance:   %s (magnitude spread)\n", formatSpectralValue(m.SpectralVariance, 6))
		fmt.Fprintf(f, "Spectral Centroid:   %.0f Hz — %s\n", m.SpectralCentroid, interpretCentroid(m.SpectralCentroid))
		fmt.Fprintf(f, "Spectral Spread:     %.0f Hz — %s\n", m.SpectralSpread, interpretSpread(m.SpectralSpread))
		fmt.Fprintf(f, "Spectral Skewness:   %.3f — %s\n", m.SpectralSkewness, interpretSkewness(m.SpectralSkewness))
		fmt.Fprintf(f, "Spectral Kurtosis:   %.3f — %s\n", m.SpectralKurtosis, interpretKurtosis(m.SpectralKurtosis))
		fmt.Fprintf(f, "Spectral Entropy:    %s — %s\n", formatSpectralValue(m.SpectralEntropy, 6), interpretEntropy(m.SpectralEntropy))
		fmt.Fprintf(f, "Spectral Flatness:   %s — %s\n", formatSpectralValue(m.SpectralFlatness, 6), interpretFlatness(m.SpectralFlatness))
		fmt.Fprintf(f, "Spectral Crest:      %.3f — %s\n", m.SpectralCrest, interpretCrest(m.SpectralCrest))
		fmt.Fprintf(f, "Spectral Flux:       %s — %s\n", formatSpectralValue(m.SpectralFlux, 6), interpretFlux(m.SpectralFlux))
		fmt.Fprintf(f, "Spectral Slope:      %s — %s\n", formatSpectralValue(m.SpectralSlope, 9), interpretSlope(m.SpectralSlope))
		fmt.Fprintf(f, "Spectral Decrease:   %s — %s\n", formatSpectralValue(m.SpectralDecrease, 6), interpretDecrease(m.SpectralDecrease))
		fmt.Fprintf(f, "Spectral Rolloff:    %.0f Hz — %s\n", m.SpectralRolloff, interpretRolloff(m.SpectralRolloff))

		// Additional signal quality measurements
		if m.DCOffset != 0 {
			fmt.Fprintf(f, "DC Offset:           %.6f\n", m.DCOffset)
		}
		if m.FlatFactor > 0 {
			fmt.Fprintf(f, "Flat Factor:         %.1f (clipping indicator)\n", m.FlatFactor)
		}
		if m.ZeroCrossingsRate > 0 {
			fmt.Fprintf(f, "Zero Crossings Rate: %.4f\n", m.ZeroCrossingsRate)
		}
		if m.MaxDifference > 0 {
			// Convert to percentage of full scale for readability
			// Max difference is in sample units; 32768 is full scale for 16-bit
			maxDiffPercent := (m.MaxDifference / 32768.0) * 100.0
			fmt.Fprintf(f, "Max Difference:      %.1f%% FS (transient indicator)\n", maxDiffPercent)
		}

		// Silence sample details (used for noise profile extraction)
		if m.NoiseProfile != nil {
			fmt.Fprintf(f, "Silence Sample:      %.1fs at %.1fs\n",
				m.NoiseProfile.Duration.Seconds(),
				m.NoiseProfile.Start.Seconds())
			fmt.Fprintf(f, "  Noise Floor:       %.1f dBFS (RMS)\n", m.NoiseProfile.MeasuredNoiseFloor)
			fmt.Fprintf(f, "  Peak Level:        %.1f dBFS\n", m.NoiseProfile.PeakLevel)
			fmt.Fprintf(f, "  Crest Factor:      %.1f dB\n", m.NoiseProfile.CrestFactor)
			if m.NoiseProfile.Entropy > 0 {
				// Classify noise type based on entropy
				noiseType := "broadband (hiss)"
				if m.NoiseProfile.Entropy < 0.7 {
					noiseType = "tonal (hum/buzz)"
				} else if m.NoiseProfile.Entropy < 0.9 {
					noiseType = "mixed"
				}
				fmt.Fprintf(f, "  Noise Character:   %s (entropy %.3f)\n", noiseType, m.NoiseProfile.Entropy)
			}
			if m.NoiseProfile.ExtractionWarning != "" {
				fmt.Fprintf(f, "  Warning:           %s\n", m.NoiseProfile.ExtractionWarning)
			}
		} else if len(m.SilenceRegions) > 0 {
			// Show first silence region even if profile extraction failed
			r := m.SilenceRegions[0]
			fmt.Fprintf(f, "Silence Detected:    %.1fs at %.1fs (no profile extracted)\n",
				r.Duration.Seconds(), r.Start.Seconds())
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
	fmt.Fprintln(f, "Pass 2: Output Analysis")
	fmt.Fprintln(f, "-----------------------")
	if data.Result != nil {
		fmt.Fprintf(f, "Output File:         %s\n", filepath.Base(data.OutputPath))

		if data.Result.OutputMeasurements != nil {
			om := data.Result.OutputMeasurements
			m := data.Result.Measurements // Input measurements for comparison

			if m != nil {
				fmt.Fprintf(f, "Integrated Loudness: %.1f LUFS %s\n", om.OutputI, formatComparison(om.OutputI, m.InputI, "LUFS", 1))
				fmt.Fprintf(f, "True Peak:           %.1f dBTP %s\n", om.OutputTP, formatComparison(om.OutputTP, m.InputTP, "dBTP", 1))
				fmt.Fprintf(f, "Loudness Range:      %.1f LU %s\n", om.OutputLRA, formatComparison(om.OutputLRA, m.InputLRA, "LU", 1))
				fmt.Fprintf(f, "Dynamic Range:       %s\n", formatDynamicRangeComparison(om.DynamicRange, m.DynamicRange))
				fmt.Fprintf(f, "RMS Level:           %.1f dBFS %s\n", om.RMSLevel, formatComparison(om.RMSLevel, m.RMSLevel, "dBFS", 1))
				fmt.Fprintf(f, "Peak Level:          %.1f dBFS %s\n", om.PeakLevel, formatComparison(om.PeakLevel, m.PeakLevel, "dBFS", 1))

				// Spectral analysis (aspectralstats measurements) with characteristic interpretations
				fmt.Fprintf(f, "Spectral Mean:       %s %s\n", formatSpectralValue(om.SpectralMean, 6), formatComparisonSpectral(om.SpectralMean, m.SpectralMean, 6))
				fmt.Fprintf(f, "Spectral Variance:   %s %s\n", formatSpectralValue(om.SpectralVariance, 6), formatComparisonSpectral(om.SpectralVariance, m.SpectralVariance, 6))
				fmt.Fprintf(f, "Spectral Centroid:   %.0f Hz — %s %s\n", om.SpectralCentroid, interpretCentroid(om.SpectralCentroid), formatComparison(om.SpectralCentroid, m.SpectralCentroid, "Hz", 0))
				fmt.Fprintf(f, "Spectral Spread:     %.0f Hz — %s %s\n", om.SpectralSpread, interpretSpread(om.SpectralSpread), formatComparison(om.SpectralSpread, m.SpectralSpread, "Hz", 0))
				fmt.Fprintf(f, "Spectral Skewness:   %.3f — %s %s\n", om.SpectralSkewness, interpretSkewness(om.SpectralSkewness), formatComparisonNoUnit(om.SpectralSkewness, m.SpectralSkewness, 3))
				fmt.Fprintf(f, "Spectral Kurtosis:   %.3f — %s %s\n", om.SpectralKurtosis, interpretKurtosis(om.SpectralKurtosis), formatComparisonNoUnit(om.SpectralKurtosis, m.SpectralKurtosis, 3))
				fmt.Fprintf(f, "Spectral Entropy:    %s — %s %s\n", formatSpectralValue(om.SpectralEntropy, 6), interpretEntropy(om.SpectralEntropy), formatComparisonSpectral(om.SpectralEntropy, m.SpectralEntropy, 6))
				fmt.Fprintf(f, "Spectral Flatness:   %s — %s %s\n", formatSpectralValue(om.SpectralFlatness, 6), interpretFlatness(om.SpectralFlatness), formatComparisonNoUnit(om.SpectralFlatness, m.SpectralFlatness, 6))
				fmt.Fprintf(f, "Spectral Crest:      %.3f — %s %s\n", om.SpectralCrest, interpretCrest(om.SpectralCrest), formatComparisonNoUnit(om.SpectralCrest, m.SpectralCrest, 3))
				fmt.Fprintf(f, "Spectral Flux:       %s — %s %s\n", formatSpectralValue(om.SpectralFlux, 6), interpretFlux(om.SpectralFlux), formatComparisonSpectral(om.SpectralFlux, m.SpectralFlux, 6))
				fmt.Fprintf(f, "Spectral Slope:      %s — %s %s\n", formatSpectralValue(om.SpectralSlope, 9), interpretSlope(om.SpectralSlope), formatComparisonSpectral(om.SpectralSlope, m.SpectralSlope, 9))
				fmt.Fprintf(f, "Spectral Decrease:   %s — %s %s\n", formatSpectralValue(om.SpectralDecrease, 6), interpretDecrease(om.SpectralDecrease), formatComparisonSpectral(om.SpectralDecrease, m.SpectralDecrease, 6))
				fmt.Fprintf(f, "Spectral Rolloff:    %.0f Hz — %s %s\n", om.SpectralRolloff, interpretRolloff(om.SpectralRolloff), formatComparison(om.SpectralRolloff, m.SpectralRolloff, "Hz", 0))
			} else {
				fmt.Fprintf(f, "Integrated Loudness: %.1f LUFS\n", om.OutputI)
				fmt.Fprintf(f, "True Peak:           %.1f dBTP\n", om.OutputTP)
				fmt.Fprintf(f, "Loudness Range:      %.1f LU\n", om.OutputLRA)
				fmt.Fprintf(f, "Dynamic Range:       %s\n", formatDynamicRange(om.DynamicRange))
				fmt.Fprintf(f, "RMS Level:           %.1f dBFS\n", om.RMSLevel)
				fmt.Fprintf(f, "Peak Level:          %.1f dBFS\n", om.PeakLevel)

				// Spectral analysis (aspectralstats measurements) with characteristic interpretations
				fmt.Fprintf(f, "Spectral Mean:       %s (avg magnitude)\n", formatSpectralValue(om.SpectralMean, 6))
				fmt.Fprintf(f, "Spectral Variance:   %s (magnitude spread)\n", formatSpectralValue(om.SpectralVariance, 6))
				fmt.Fprintf(f, "Spectral Centroid:   %.0f Hz — %s\n", om.SpectralCentroid, interpretCentroid(om.SpectralCentroid))
				fmt.Fprintf(f, "Spectral Spread:     %.0f Hz — %s\n", om.SpectralSpread, interpretSpread(om.SpectralSpread))
				fmt.Fprintf(f, "Spectral Skewness:   %.3f — %s\n", om.SpectralSkewness, interpretSkewness(om.SpectralSkewness))
				fmt.Fprintf(f, "Spectral Kurtosis:   %.3f — %s\n", om.SpectralKurtosis, interpretKurtosis(om.SpectralKurtosis))
				fmt.Fprintf(f, "Spectral Entropy:    %s — %s\n", formatSpectralValue(om.SpectralEntropy, 6), interpretEntropy(om.SpectralEntropy))
				fmt.Fprintf(f, "Spectral Flatness:   %s — %s\n", formatSpectralValue(om.SpectralFlatness, 6), interpretFlatness(om.SpectralFlatness))
				fmt.Fprintf(f, "Spectral Crest:      %.3f — %s\n", om.SpectralCrest, interpretCrest(om.SpectralCrest))
				fmt.Fprintf(f, "Spectral Flux:       %s — %s\n", formatSpectralValue(om.SpectralFlux, 6), interpretFlux(om.SpectralFlux))
				fmt.Fprintf(f, "Spectral Slope:      %s — %s\n", formatSpectralValue(om.SpectralSlope, 9), interpretSlope(om.SpectralSlope))
				fmt.Fprintf(f, "Spectral Decrease:   %s — %s\n", formatSpectralValue(om.SpectralDecrease, 6), interpretDecrease(om.SpectralDecrease))
				fmt.Fprintf(f, "Spectral Rolloff:    %.0f Hz — %s\n", om.SpectralRolloff, interpretRolloff(om.SpectralRolloff))
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
	case processor.FilterHighpass:
		formatHighpassFilter(f, cfg, m, prefix)
	case processor.FilterBandreject:
		formatBandrejectFilter(f, cfg, m, prefix)
	case processor.FilterAdeclick:
		formatAdeclickFilter(f, cfg, prefix)
	case processor.FilterAfftdn:
		formatAfftdnFilter(f, cfg, m, prefix)
	case processor.FilterAfftdnSimple:
		formatAfftdnSimpleFilter(f, cfg, m, prefix)
	case processor.FilterArnndn:
		formatArnndnFilter(f, cfg, m, prefix)
	case processor.FilterAgate:
		formatAgateFilter(f, cfg, m, prefix)
	case processor.FilterLA2ACompressor:
		formatLA2ACompressorFilter(f, cfg, m, prefix)
	case processor.FilterDeesser:
		formatDeesserFilter(f, cfg, m, prefix)
	case processor.FilterSpeechnorm:
		formatSpeechnormFilter(f, cfg, m, prefix)
	case processor.FilterDynaudnorm:
		formatDynaudnormFilter(f, cfg, prefix)
	case processor.FilterBleedGate:
		formatBleedGateFilter(f, cfg, m, prefix)
	case processor.FilterAlimiter:
		formatAlimiterFilter(f, cfg, prefix)
	default:
		fmt.Fprintf(f, "%s%s: (unknown filter)\n", prefix, filterID)
	}
}

// formatHighpassFilter outputs highpass filter details
func formatHighpassFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.HighpassEnabled {
		fmt.Fprintf(f, "%shighpass: DISABLED\n", prefix)
		return
	}

	// Show slope (6dB/oct for gentle, 12dB/oct for standard)
	slope := "12dB/oct"
	if cfg.HighpassPoles == 1 {
		slope = "6dB/oct"
	}

	// Build header with all relevant parameters
	header := fmt.Sprintf("%shighpass: %.0f Hz cutoff (%s", prefix, cfg.HighpassFreq, slope)

	// Show Q if not default Butterworth
	if cfg.HighpassWidth > 0 && cfg.HighpassWidth != 0.707 {
		header += fmt.Sprintf(", Q=%.2f", cfg.HighpassWidth)
	}

	// Show transform if specified
	if cfg.HighpassTransform == "tdii" {
		header += ", tdii"
	} else if cfg.HighpassTransform != "" {
		header += ", " + cfg.HighpassTransform
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
		if cfg.HighpassMix > 0 && cfg.HighpassMix < 1.0 {
			reason := "warm voice"
			if m.SpectralDecrease < -0.08 {
				reason = "very warm voice"
			} else if m.SpectralSkewness > 1.0 {
				reason = "LF emphasis"
			}
			fmt.Fprintf(f, "        Mix: %.0f%% (%s — blending filtered with dry signal)\n", cfg.HighpassMix*100, reason)
		}

		// Show why low frequency was chosen for warm voices
		if cfg.HighpassFreq <= 40 {
			fmt.Fprintf(f, "        Frequency: %.0f Hz (subsonic only — protecting bass foundation)\n", cfg.HighpassFreq)
		}

		// Show gentle slope explanation
		if cfg.HighpassPoles == 1 {
			fmt.Fprintf(f, "        Slope: 6dB/oct (gentle rolloff — preserving warmth)\n")
		}

		// Show noise character if tonal (explains why no boost)
		if m.NoiseProfile != nil && m.NoiseProfile.Entropy < 0.5 {
			fmt.Fprintf(f, "        Note: no LF boost (tonal noise, entropy %.3f — bandreject handles hum)\n", m.NoiseProfile.Entropy)
		}
	}
}

// formatBandrejectFilter outputs bandreject (hum notch) filter details
func formatBandrejectFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.HumFilterEnabled {
		fmt.Fprintf(f, "%sbandreject: DISABLED\n", prefix)
		return
	}

	// Build the header with width and transform info
	transformInfo := ""
	if cfg.HumTransform == "tdii" {
		transformInfo = ", tdii"
	} else if cfg.HumTransform != "" {
		transformInfo = ", " + cfg.HumTransform
	}
	fmt.Fprintf(f, "%sbandreject: %.0f Hz + %d harmonics (%.1f Hz wide%s)\n",
		prefix, cfg.HumFrequency, cfg.HumHarmonics, cfg.HumWidth, transformInfo)

	if m != nil && m.NoiseProfile != nil {
		fmt.Fprintf(f, "        Rationale: tonal noise detected (entropy %.3f < 0.7)\n", m.NoiseProfile.Entropy)

		// Explain reduced harmonics for warm voices
		if cfg.HumHarmonics <= 2 {
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
					cfg.HumHarmonics, reason)
			}
		}

		// Explain notch width choice
		if cfg.HumWidth != 1.0 { // Not the default
			if cfg.HumWidth <= 0.3 {
				fmt.Fprintf(f, "        Width: %.1f Hz (very narrow — warm voice protection)\n", cfg.HumWidth)
			} else if cfg.HumWidth < 1.0 {
				fmt.Fprintf(f, "        Width: %.1f Hz (narrow surgical notch — very tonal hum)\n", cfg.HumWidth)
			} else {
				fmt.Fprintf(f, "        Width: %.1f Hz (wider notch — mixed tonal noise)\n", cfg.HumWidth)
			}
		}

		// Explain transform type
		if cfg.HumTransform == "tdii" {
			fmt.Fprintf(f, "        Transform: TDII (transposed direct form II — best floating-point accuracy)\n")
		} else if cfg.HumTransform != "" && cfg.HumTransform != "di" {
			fmt.Fprintf(f, "        Transform: %s\n", cfg.HumTransform)
		}

		// Explain mix if not full wet
		if cfg.HumMix > 0 && cfg.HumMix < 1.0 {
			mixReason := "warm voice"
			if cfg.HumMix <= 0.7 {
				mixReason = "very warm voice"
			}
			fmt.Fprintf(f, "        Mix: %.0f%% (%s — blending filtered with dry signal)\n", cfg.HumMix*100, mixReason)
		}
	}
}

// formatAdeclickFilter outputs adeclick filter details
func formatAdeclickFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.AdeclickEnabled {
		fmt.Fprintf(f, "%sadeclick: DISABLED\n", prefix)
		return
	}

	method := "overlap-save"
	if cfg.AdeclickMethod == "a" {
		method = "overlap-add"
	}
	fmt.Fprintf(f, "%sadeclick: %s method\n", prefix, method)
}

// formatAfftdnFilter outputs afftdn filter details
func formatAfftdnFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.AfftdnEnabled {
		fmt.Fprintf(f, "%safftdn: DISABLED\n", prefix)
		return
	}

	// Method: profile-based or tracking
	method := "adaptive tracking"
	if cfg.NoiseProfilePath != "" {
		method = fmt.Sprintf("noise profile (%.2fs sample)", cfg.NoiseProfileDuration.Seconds())
	}

	fmt.Fprintf(f, "%safftdn: %.1f dB reduction, floor %.1f dB\n",
		prefix, cfg.NoiseReduction, cfg.NoiseFloor)
	fmt.Fprintf(f, "        Method: %s\n", method)

	// Show adaptive rationale
	if m != nil && m.InputI != 0 {
		lufsGap := cfg.TargetI - m.InputI
		fmt.Fprintf(f, "        Rationale: %.1f dB LUFS gap", lufsGap)

		if m.NoiseReductionHeadroom > 0 {
			quality := "typical"
			if m.NoiseReductionHeadroom < 15 {
				quality = "noisy (conservative NR)"
			} else if m.NoiseReductionHeadroom > 30 {
				quality = "clean (aggressive NR)"
			}
			fmt.Fprintf(f, ", headroom %.1f dB (%s)", m.NoiseReductionHeadroom, quality)
		}
		fmt.Fprintln(f, "")
	}
}

// formatAfftdnSimpleFilter outputs afftdn_simple (sample-free) filter details
func formatAfftdnSimpleFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.AfftdnSimpleEnabled {
		fmt.Fprintf(f, "%safftdn_simple: DISABLED\n", prefix)
		// Show why disabled if we have measurements
		if m != nil && m.NoiseFloor < -75 {
			fmt.Fprintf(f, "        Rationale: clean source (noise floor %.1f dBFS < -75 dBFS threshold)\n", m.NoiseFloor)
		}
		return
	}

	// Show noise type with explanation
	noiseType := cfg.AfftdnSimpleNoiseType
	if noiseType == "" {
		noiseType = "w"
	}
	noiseTypeDesc := map[string]string{
		"w": "white (broadband)",
		"v": "vinyl (LF-weighted)",
		"s": "shellac (HF-weighted)",
	}[noiseType]
	if noiseTypeDesc == "" {
		noiseTypeDesc = noiseType
	}

	fmt.Fprintf(f, "%safftdn_simple: %.1f dB reduction, type %s\n",
		prefix, cfg.AfftdnSimpleNoiseReduction, noiseTypeDesc)
	fmt.Fprintf(f, "        Floor: %.1f dBFS (from Pass 1 measurements)\n", cfg.AfftdnSimpleNoiseFloor)

	// Show noise type selection rationale
	if m != nil {
		switch noiseType {
		case "v":
			fmt.Fprintf(f, "        Rationale: vinyl mode — LF emphasis (decrease %.3f, slope %.2e)\n",
				m.SpectralDecrease, m.SpectralSlope)
		case "s":
			fmt.Fprintf(f, "        Rationale: shellac mode — HF emphasis (centroid %.0f Hz, rolloff %.0f Hz)\n",
				m.SpectralCentroid, m.SpectralRolloff)
		case "w":
			if m.SpectralFlatness > 0.6 {
				fmt.Fprintf(f, "        Rationale: white mode — high flatness (%.3f)\n", m.SpectralFlatness)
			} else {
				fmt.Fprintf(f, "        Rationale: white mode — default (no strong spectral bias)\n")
			}
		}
	}
}

// formatArnndnFilter outputs arnndn filter details
func formatArnndnFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.ArnnDnEnabled {
		fmt.Fprintf(f, "%sarnndn: DISABLED\n", prefix)
		// Show why disabled if we have measurements
		if m != nil {
			if m.NoiseFloor < -80 {
				fmt.Fprintf(f, "        Rationale: very clean source (noise floor %.1f dBFS)\n", m.NoiseFloor)
			}
		}
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
		if cfg.AfftdnEnabled {
			adjustments = append(adjustments, "-10% afftdn active")
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

// formatAgateFilter outputs agate filter details
func formatAgateFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.GateEnabled {
		fmt.Fprintf(f, "%sagate: DISABLED\n", prefix)
		return
	}

	thresholdDB := linearToDb(cfg.GateThreshold)
	rangeDB := linearToDb(cfg.GateRange)

	detection := cfg.GateDetection
	if detection == "" {
		detection = "rms"
	}

	fmt.Fprintf(f, "%sagate: threshold %.1f dB, ratio %.1f:1, detection %s\n", prefix, thresholdDB, cfg.GateRatio, detection)
	fmt.Fprintf(f, "        Timing: attack %.0fms, release %.0fms\n", cfg.GateAttack, cfg.GateRelease)
	fmt.Fprintf(f, "        Range: %.1f dB reduction, knee %.1f\n", rangeDB, cfg.GateKnee)

	// Show rationale based on measurements
	if m != nil {
		var rationale []string

		// Threshold rationale
		if m.NoiseProfile != nil && m.NoiseProfile.CrestFactor > 20 {
			rationale = append(rationale, fmt.Sprintf("peak ref %.1f dB (crest %.1f dB)", m.NoiseProfile.PeakLevel, m.NoiseProfile.CrestFactor))
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

		// Noise character for range/detection
		if m.NoiseProfile != nil {
			if m.NoiseProfile.Entropy < 0.3 {
				rationale = append(rationale, "tonal noise detected")
			}
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

// formatSpeechnormFilter outputs speechnorm filter details
func formatSpeechnormFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.SpeechnormEnabled {
		fmt.Fprintf(f, "%sspeechnorm: DISABLED\n", prefix)
		return
	}

	expansionDB := 20.0 * math.Log10(cfg.SpeechnormExpansion)
	fmt.Fprintf(f, "%sspeechnorm: expansion %.1fx (%.1f dB), peak %.2f\n",
		prefix, cfg.SpeechnormExpansion, expansionDB, cfg.SpeechnormPeak)

	if cfg.SpeechnormRMS > 0 {
		fmt.Fprintf(f, "        RMS target: %.3f\n", cfg.SpeechnormRMS)
	}

	fmt.Fprintf(f, "        Smoothing: raise %.3f, fall %.3f\n", cfg.SpeechnormRaise, cfg.SpeechnormFall)

	// Show rationale
	if m != nil && m.InputI != 0 {
		lufsGap := cfg.TargetI - m.InputI
		fmt.Fprintf(f, "        Rationale: input %.1f LUFS, gap %.1f dB to target %.1f LUFS\n",
			m.InputI, lufsGap, cfg.TargetI)

		// Warn if expansion was capped
		if lufsGap > 20.0 {
			expectedLUFS := m.InputI + expansionDB
			fmt.Fprintf(f, "        Note: expansion capped at 10x (20 dB) for quality — expected output ~%.1f LUFS\n", expectedLUFS)
		}
	}
}

// formatDynaudnormFilter outputs dynaudnorm filter details
func formatDynaudnormFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.DynaudnormEnabled {
		fmt.Fprintf(f, "%sdynaudnorm: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sdynaudnorm: frame %dms, filter size %d\n",
		prefix, cfg.DynaudnormFrameLen, cfg.DynaudnormFilterSize)
	fmt.Fprintf(f, "        Peak: %.2f, max gain %.1fx\n", cfg.DynaudnormPeakValue, cfg.DynaudnormMaxGain)
	fmt.Fprintln(f, "        Mode: conservative fixed parameters (prevents artifacts)")
}

// formatBleedGateFilter outputs bleed gate filter details
func formatBleedGateFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.BleedGateEnabled {
		fmt.Fprintf(f, "%sbleedgate: DISABLED\n", prefix)
		return
	}

	thresholdDB := linearToDb(cfg.BleedGateThreshold)
	rangeDB := linearToDb(cfg.BleedGateRange)

	fmt.Fprintf(f, "%sbleedgate: threshold %.1f dB, ratio %.1f:1\n", prefix, thresholdDB, cfg.BleedGateRatio)
	fmt.Fprintf(f, "        Timing: attack %.0fms, release %.0fms\n", cfg.BleedGateAttack, cfg.BleedGateRelease)
	fmt.Fprintf(f, "        Range: %.1f dB reduction, knee %.1f\n", rangeDB, cfg.BleedGateKnee)

	// Show rationale
	if m != nil && m.NoiseProfile != nil {
		np := m.NoiseProfile
		hasBleed := np.CrestFactor > 15.0 || (np.PeakLevel-np.MeasuredNoiseFloor) > 20.0
		if hasBleed {
			fmt.Fprintf(f, "        Rationale: bleed detected (crest %.1f dB, peak-floor %.1f dB)\n",
				np.CrestFactor, np.PeakLevel-np.MeasuredNoiseFloor)
		} else {
			fmt.Fprintf(f, "        Rationale: noise amplification (predicted output above -40 dB)\n")
		}
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
