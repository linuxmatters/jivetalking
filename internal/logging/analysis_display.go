// Package logging handles generation of analysis reports for processed audio files.
// This file provides console display for analysis-only mode.

package logging

import (
	"fmt"
	"io"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// DisplayAnalysisResults outputs Pass 1 analysis results to the console.
// Used by --analysis-only mode for rapid inspection without full processing.
func DisplayAnalysisResults(w io.Writer, inputPath string, metadata *audio.Metadata, measurements *processor.AudioMeasurements, config *processor.FilterChainConfig) {
	// Header
	fmt.Fprintln(w, strings.Repeat("=", 70))
	fmt.Fprintf(w, "ANALYSIS: %s\n", filepath.Base(inputPath))
	fmt.Fprintln(w, strings.Repeat("=", 70))

	// File info
	fmt.Fprintf(w, "Duration:    %s\n", formatDurationHMS(metadata.Duration))
	fmt.Fprintf(w, "Sample Rate: %d Hz\n", metadata.SampleRate)
	fmt.Fprintf(w, "Channels:    %s\n", channelName(metadata.Channels))
	fmt.Fprintln(w)

	// Loudness section
	writeAnalysisSection(w, "LOUDNESS")
	fmt.Fprintf(w, "  Integrated:     %.1f LUFS\n", measurements.InputI)
	fmt.Fprintf(w, "  True Peak:      %.1f dBTP\n", measurements.InputTP)
	fmt.Fprintf(w, "  Loudness Range: %.1f LU\n", measurements.InputLRA)
	fmt.Fprintln(w)

	// Dynamics section
	writeAnalysisSection(w, "DYNAMICS")
	fmt.Fprintf(w, "  RMS Level:      %.1f dBFS\n", measurements.RMSLevel)
	fmt.Fprintf(w, "  Peak Level:     %.1f dBFS\n", measurements.PeakLevel)
	fmt.Fprintf(w, "  Dynamic Range:  %.1f dB\n", measurements.DynamicRange)
	fmt.Fprintf(w, "  Crest Factor:   %.1f dB\n", measurements.PeakLevel-measurements.RMSLevel)
	fmt.Fprintln(w)

	// Silence detection section
	writeAnalysisSection(w, "SILENCE DETECTION")
	fmt.Fprintf(w, "  Threshold:      %.1f dB (from %.1f dB noise floor estimate)\n",
		measurements.SilenceDetectLevel, measurements.PreScanNoiseFloor)

	if len(measurements.SilenceCandidates) > 0 {
		fmt.Fprintf(w, "  Candidates:     %d evaluated\n", len(measurements.SilenceCandidates))
		fmt.Fprintln(w)

		for i, c := range measurements.SilenceCandidates {
			// Check if this candidate was elected
			isElected := false
			wasRefined := false
			var refinedStart time.Duration
			var refinedDuration time.Duration

			if measurements.NoiseProfile != nil {
				if measurements.NoiseProfile.WasRefined {
					isElected = c.Region.Start == measurements.NoiseProfile.OriginalStart
					wasRefined = isElected
					refinedStart = measurements.NoiseProfile.Start
					refinedDuration = measurements.NoiseProfile.Duration
				} else {
					isElected = c.Region.Start == measurements.NoiseProfile.Start
				}
			}

			electedMark := ""
			if isElected {
				electedMark = " [ELECTED]"
			}

			fmt.Fprintf(w, "  #%d: %.1fs at %s%s\n",
				i+1, c.Region.Duration.Seconds(), formatTimestamp(c.Region.Start), electedMark)

			// Show refinement details only for elected candidate
			if isElected && wasRefined {
				fmt.Fprintf(w, "      Refined:     %.1fs at %s (golden sub-region)\n",
					refinedDuration.Seconds(), formatTimestamp(refinedStart))
			}

			// Show full metrics for all candidates
			fmt.Fprintf(w, "      Score:       %.3f\n", c.Score)
			fmt.Fprintf(w, "      RMS Level:   %.1f dBFS\n", c.RMSLevel)
			fmt.Fprintf(w, "      Peak Level:  %.1f dBFS\n", c.PeakLevel)
			fmt.Fprintf(w, "      Crest:       %.1f dB\n", c.CrestFactor)
			fmt.Fprintf(w, "      Entropy:     %.3f (%s)\n", c.SpectralEntropy, interpretEntropy(c.SpectralEntropy))
			fmt.Fprintf(w, "      Flatness:    %.3f (%s)\n", c.SpectralFlatness, interpretFlatness(c.SpectralFlatness))
			fmt.Fprintf(w, "      Kurtosis:    %.1f (%s)\n", c.SpectralKurtosis, interpretKurtosis(c.SpectralKurtosis))
			fmt.Fprintf(w, "      Centroid:    %.0f Hz\n", c.SpectralCentroid)
			fmt.Fprintln(w)
		}
	} else if measurements.NoiseProfile != nil {
		fmt.Fprintf(w, "  Sample:         %.1fs at %s\n",
			measurements.NoiseProfile.Duration.Seconds(), formatTimestamp(measurements.NoiseProfile.Start))
		fmt.Fprintf(w, "  Noise Floor:    %.1f dBFS\n", measurements.NoiseProfile.MeasuredNoiseFloor)
	} else {
		fmt.Fprintln(w, "  No silence detected")
	}

	// Speech detection section
	writeAnalysisSection(w, "SPEECH DETECTION")
	if len(measurements.SpeechCandidates) > 0 {
		fmt.Fprintf(w, "  Candidates:     %d evaluated\n", len(measurements.SpeechCandidates))
		fmt.Fprintln(w)

		for i, c := range measurements.SpeechCandidates {
			// Check if this candidate was elected
			// Note: When a candidate is refined to a golden sub-region, the candidate
			// in the list is replaced with refined metrics. The refined candidate has:
			// - Region.Start = refined start
			// - WasRefined = true
			// - OriginalStart = original candidate start
			// - OriginalDuration = original candidate duration
			isElected := false
			wasRefined := c.WasRefined

			if measurements.SpeechProfile != nil {
				// If this candidate was refined, compare against SpeechProfile.Region.Start
				// (both will have the refined start after replacement)
				// If not refined, compare Region.Start directly
				isElected = c.Region.Start == measurements.SpeechProfile.Region.Start
			}

			electedMark := ""
			if isElected {
				electedMark = " [ELECTED]"
			}

			// For refined candidates, display the original duration/start, then show refined info
			displayStart := c.Region.Start
			displayDuration := c.Region.Duration
			if wasRefined {
				displayStart = c.OriginalStart
				displayDuration = c.OriginalDuration
			}

			fmt.Fprintf(w, "  #%d: %.1fs at %s%s\n",
				i+1, displayDuration.Seconds(), formatTimestamp(displayStart), electedMark)

			// Show refinement details only for elected candidate
			if isElected && wasRefined {
				// Show refined region info (c.Region now contains the refined values)
				fmt.Fprintf(w, "      Refined:     %.1fs at %s (golden sub-region)\n",
					c.Region.Duration.Seconds(), formatTimestamp(c.Region.Start))
			}

			// Show full metrics for all candidates
			fmt.Fprintf(w, "      Score:       %.2f\n", c.Score)
			fmt.Fprintf(w, "      RMS Level:   %.1f dBFS\n", c.RMSLevel)
			fmt.Fprintf(w, "      Crest:       %.1f dB\n", c.CrestFactor)
			fmt.Fprintf(w, "      Centroid:    %.0f Hz (%s)\n", c.SpectralCentroid, interpretCentroid(c.SpectralCentroid))
			fmt.Fprintf(w, "      Kurtosis:    %.1f (%s)\n", c.SpectralKurtosis, interpretKurtosis(c.SpectralKurtosis))
			if c.VoicingDensity > 0 {
				fmt.Fprintf(w, "      Voicing:     %.0f%%\n", c.VoicingDensity*100)
			}
			fmt.Fprintln(w)
		}
	} else if measurements.SpeechProfile != nil {
		fmt.Fprintf(w, "  Sample:         %.1fs at %s\n",
			measurements.SpeechProfile.Region.Duration.Seconds(),
			formatTimestamp(measurements.SpeechProfile.Region.Start))
		fmt.Fprintf(w, "  RMS Level:      %.1f dBFS\n", measurements.SpeechProfile.RMSLevel)
	} else {
		fmt.Fprintln(w, "  No speech profile available")
	}
	fmt.Fprintln(w)

	// Derived measurements section
	writeAnalysisSection(w, "DERIVED MEASUREMENTS")
	if measurements.NoiseProfile != nil {
		fmt.Fprintf(w, "  Noise Floor:    %.1f dBFS (from elected silence)\n", measurements.NoiseProfile.MeasuredNoiseFloor)
		// SuggestedGateThreshold is stored as linear amplitude, convert to dB
		suggestedGateDB := processor.LinearToDb(measurements.SuggestedGateThreshold)
		fmt.Fprintf(w, "  Gate Baseline:  %.1f dB (noise floor + margin)\n", suggestedGateDB)
		fmt.Fprintf(w, "  NR Headroom:    %.1f dB (noise-to-speech gap)\n", measurements.NoiseReductionHeadroom)
	} else {
		fmt.Fprintf(w, "  Noise Floor:    %.1f dBFS (from astats)\n", measurements.AstatsNoiseFloor)
	}
	fmt.Fprintln(w)

	// Filter adaptation section
	writeAnalysisSection(w, "FILTER ADAPTATION")
	if config != nil {
		fmt.Fprintf(w, "  Highpass:       %.0f Hz (from spectral analysis)\n", config.DS201HPFreq)
		if measurements.NoiseProfile != nil {
			gateThresholdDB := processor.LinearToDb(config.DS201GateThreshold)
			// Determine threshold description based on whether SpeechProfile was used
			gateDesc := "(from noise floor)"
			if measurements.SpeechProfile != nil {
				gateDesc = "(with breath reduction)"
			}
			fmt.Fprintf(w, "  Gate Threshold: %.1f dB %s\n", gateThresholdDB, gateDesc)
			fmt.Fprintf(w, "  Gate Ratio:     %.1f:1\n", config.DS201GateRatio)
		}
		fmt.Fprintf(w, "  NR Threshold:   %.0f dB\n", config.NoiseRemoveCompandThreshold)
		fmt.Fprintf(w, "  NR Expansion:   %.0f dB\n", config.NoiseRemoveCompandExpansion)
		if config.DeessIntensity > 0 {
			fmt.Fprintf(w, "  De-esser:       %.0f%% intensity\n", config.DeessIntensity*100)
		} else {
			fmt.Fprintf(w, "  De-esser:       disabled (no sibilance detected)\n")
		}
		fmt.Fprintf(w, "  LA-2A Thresh:   %.0f dB\n", config.LA2AThreshold)
		fmt.Fprintf(w, "  LA-2A Ratio:    %.1f:1\n", config.LA2ARatio)
	}

	// Spectral summary (all spectral metrics from aspectralstats)
	writeAnalysisSection(w, "SPECTRAL SUMMARY")

	// Frequency distribution metrics
	fmt.Fprintf(w, "  Centroid:       %.0f Hz (%s)\n", measurements.SpectralCentroid, interpretCentroid(measurements.SpectralCentroid))
	fmt.Fprintf(w, "  Spread:         %.0f Hz (%s)\n", measurements.SpectralSpread, interpretSpread(measurements.SpectralSpread))
	fmt.Fprintf(w, "  Rolloff:        %.0f Hz (%s)\n", measurements.SpectralRolloff, interpretRolloff(measurements.SpectralRolloff))

	// Distribution shape metrics
	fmt.Fprintf(w, "  Flatness:       %.3f (%s)\n", measurements.SpectralFlatness, interpretFlatness(measurements.SpectralFlatness))
	fmt.Fprintf(w, "  Kurtosis:       %.1f (%s)\n", measurements.SpectralKurtosis, interpretKurtosis(measurements.SpectralKurtosis))
	fmt.Fprintf(w, "  Skewness:       %.2f (%s)\n", measurements.SpectralSkewness, interpretSkewness(measurements.SpectralSkewness))
	fmt.Fprintf(w, "  Crest:          %.1f (%s)\n", measurements.SpectralCrest, interpretCrest(measurements.SpectralCrest))

	// Spectral slope/energy metrics
	fmt.Fprintf(w, "  Slope:          %.2e (%s)\n", measurements.SpectralSlope, interpretSlope(measurements.SpectralSlope))
	fmt.Fprintf(w, "  Decrease:       %.4f (%s)\n", measurements.SpectralDecrease, interpretDecrease(measurements.SpectralDecrease))

	// Dynamics and disorder metrics
	fmt.Fprintf(w, "  Entropy:        %.3f (%s)\n", measurements.SpectralEntropy, interpretEntropy(measurements.SpectralEntropy))
	fmt.Fprintf(w, "  Flux:           %.4f (%s)\n", measurements.SpectralFlux, interpretFlux(measurements.SpectralFlux))
}

// writeAnalysisSection writes a section header for analysis output.
func writeAnalysisSection(w io.Writer, title string) {
	fmt.Fprintln(w, title)
}

// formatDurationHMS formats duration as "Xh Ym Zs" or "Ym Zs" or "Z.Xs".
func formatDurationHMS(seconds float64) string {
	if seconds < 60 {
		return fmt.Sprintf("%.1fs", seconds)
	}

	totalSeconds := int(seconds)
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	secs := totalSeconds % 60

	if hours > 0 {
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, secs)
	}
	return fmt.Sprintf("%dm %ds", minutes, secs)
}

// formatTimestamp formats a duration as a timestamp string (e.g., "1m 32s" or "24.0s").
func formatTimestamp(d time.Duration) string {
	totalSeconds := d.Seconds()
	if totalSeconds < 60 {
		return fmt.Sprintf("%.1fs", totalSeconds)
	}

	minutes := int(totalSeconds) / 60
	seconds := math.Mod(totalSeconds, 60)

	if minutes >= 60 {
		hours := minutes / 60
		minutes = minutes % 60
		return fmt.Sprintf("%dh %dm %.0fs", hours, minutes, seconds)
	}
	return fmt.Sprintf("%dm %.0fs", minutes, seconds)
}
