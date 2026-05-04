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

// AnalysisTimings contains reportable analysis-only stage durations.
type AnalysisTimings struct {
	Analysis     time.Duration
	Adaptation   time.Duration
	ReportOutput time.Duration
}

type analysisMetricSpec struct {
	Label string
	Value string
}

// DisplayAnalysisResults outputs Pass 1 analysis results to the console.
// Used by --analysis-only mode for rapid inspection without full processing.
func DisplayAnalysisResults(w io.Writer, inputPath string, metadata *audio.Metadata, measurements *processor.AudioMeasurements, config *processor.EffectiveFilterConfig, timings ...AnalysisTimings) {
	DisplayAnalysisResultsWithDiagnostics(w, inputPath, metadata, measurements, config, nil, timings...)
}

// DisplayAnalysisResultsWithDiagnostics outputs Pass 1 analysis results using
// the effective per-file filter config and separately routed adaptive
// diagnostics.
func DisplayAnalysisResultsWithDiagnostics(w io.Writer, inputPath string, metadata *audio.Metadata, measurements *processor.AudioMeasurements, config *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics, timings ...AnalysisTimings) {
	if measurements == nil {
		fmt.Fprintf(w, "No analysis data available for %s\n", filepath.Base(inputPath))
		return
	}
	reportOutputStart := time.Now()

	writeAnalysisHeader(w, inputPath, metadata)
	writeAnalysisLoudnessAndDynamics(w, measurements)
	writeAnalysisSilenceDetection(w, measurements)
	writeAnalysisSpeechDetection(w, measurements)
	writeAnalysisDerivedMeasurements(w, measurements)
	writeAnalysisFilterAdaptation(w, measurements, config, diagnostics)
	writeAnalysisSpectralSummary(w, measurements)
	writeAnalysisTips(w, measurements, config)

	if len(timings) > 0 && hasAnalysisTimings(timings[0]) {
		fmt.Fprintln(w)
		writeAnalysisTimingSection(w, completeAnalysisTimings(timings[0], reportOutputStart))
	}
}

func writeAnalysisHeader(w io.Writer, inputPath string, metadata *audio.Metadata) {
	fmt.Fprintln(w, strings.Repeat("=", 70))
	fmt.Fprintf(w, "ANALYSIS: %s\n", filepath.Base(inputPath))
	fmt.Fprintln(w, strings.Repeat("=", 70))
	fmt.Fprintf(w, "Duration:    %s\n", formatDuration(time.Duration(metadata.Duration*float64(time.Second))))
	fmt.Fprintf(w, "Sample Rate: %d Hz\n", metadata.SampleRate)
	fmt.Fprintf(w, "Channels:    %s\n", channelName(metadata.Channels))
	fmt.Fprintln(w)
}

func writeAnalysisLoudnessAndDynamics(w io.Writer, measurements *processor.AudioMeasurements) {
	writeAnalysisSection(w, "LOUDNESS")
	writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
		{"Integrated", fmt.Sprintf("%.1f LUFS", measurements.InputI)},
		{"True Peak", fmt.Sprintf("%.1f dBTP", measurements.InputTP)},
		{"Loudness Range", fmt.Sprintf("%.1f LU", measurements.InputLRA)},
	})
	fmt.Fprintln(w)

	writeAnalysisSection(w, "DYNAMICS")
	crestFactor := measurements.PeakLevel - measurements.RMSLevel
	crestSource := "full-file"
	if measurements.SpeechProfile != nil && measurements.SpeechProfile.CrestFactor > 0 {
		crestFactor = measurements.SpeechProfile.CrestFactor
		crestSource = "speech"
	}
	writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
		{"RMS Level", fmt.Sprintf("%.1f dBFS", measurements.RMSLevel)},
		{"Peak Level", fmt.Sprintf("%.1f dBFS", measurements.PeakLevel)},
		{"Dynamic Range", fmt.Sprintf("%.1f dB", measurements.DynamicRange)},
		{"Crest Factor", fmt.Sprintf("%.1f dB (%s)", crestFactor, crestSource)},
	})
	fmt.Fprintln(w)
}

func writeAnalysisSilenceDetection(w io.Writer, measurements *processor.AudioMeasurements) {
	writeAnalysisSection(w, "SILENCE DETECTION")
	fmt.Fprintf(w, "  Threshold:      %.1f dB (%.1f dBFS room tone estimate + 1 dB)\n",
		measurements.SilenceDetectLevel, measurements.PreScanNoiseFloor)

	writeAnalysisSilenceCandidates(w, measurements)
	fmt.Fprintln(w)
}

func writeAnalysisSpeechDetection(w io.Writer, measurements *processor.AudioMeasurements) {
	writeAnalysisSection(w, "SPEECH DETECTION")
	writeAnalysisSpeechCandidates(w, measurements)
	fmt.Fprintln(w)
}

func writeAnalysisDerivedMeasurements(w io.Writer, measurements *processor.AudioMeasurements) {
	writeAnalysisSection(w, "DERIVED MEASUREMENTS")
	if measurements.NoiseProfile != nil {
		suggestedGateDB := processor.LinearToDb(measurements.SuggestedGateThreshold)
		writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
			{"Noise Floor", fmt.Sprintf("%.1f dBFS (from elected silence)", measurements.NoiseProfile.MeasuredNoiseFloor)},
			{"Gate Baseline", fmt.Sprintf("%.1f dB (noise floor + margin)", suggestedGateDB)},
			{"NR Headroom", fmt.Sprintf("%.1f dB (noise-to-speech gap)", measurements.NoiseReductionHeadroom)},
		})
	} else {
		writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
			{"Noise Floor", fmt.Sprintf("%.1f dBFS (%s)", measurements.NoiseFloor, noiseFloorSourceLabel(measurements.NoiseFloorSource))},
		})
	}
	fmt.Fprintln(w)
}

func writeAnalysisFilterAdaptation(w io.Writer, measurements *processor.AudioMeasurements, config *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics) {
	writeAnalysisSection(w, "FILTER ADAPTATION")
	if config != nil {
		writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
			{"Highpass", fmt.Sprintf("%.0f Hz (from spectral analysis)", config.DS201HighPass.Frequency)},
		})
		if config.DS201LowPass.Enabled {
			lowpassValue := fmt.Sprintf("%.0f Hz", config.DS201LowPass.Frequency)
			if diagnostics != nil && diagnostics.DS201LPReason != "" {
				lowpassValue += fmt.Sprintf(" (%s)", diagnostics.DS201LPReason)
			}
			writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
				{"Lowpass", lowpassValue},
			})
		} else if diagnostics != nil && diagnostics.DS201LPReason != "" {
			writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
				{"Lowpass", fmt.Sprintf("disabled (%s)", diagnostics.DS201LPReason)},
			})
		}
		if measurements.NoiseProfile != nil {
			gateThresholdDB := processor.LinearToDb(config.DS201Gate.Threshold)
			gateDesc := "(from noise floor)"
			if measurements.SpeechProfile != nil {
				gateDesc = "(with breath reduction)"
			}
			writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
				{"Gate Threshold", fmt.Sprintf("%.1f dB %s", gateThresholdDB, gateDesc)},
				{"Gate Ratio", fmt.Sprintf("%.1f:1", config.DS201Gate.Ratio)},
			})
			if diagnostics != nil && diagnostics.DS201GateClampReason != "" && diagnostics.DS201GateClampReason != "none" {
				writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
					{"Gate Clamp", fmt.Sprintf("%s (unclamped %.1f dB)", diagnostics.DS201GateClampReason, diagnostics.DS201GateThresholdUnclamped)},
				})
			}
		}
		if config.NoiseRemove.CompandEnabled {
			writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
				{"NR Threshold", fmt.Sprintf("%.0f dB", config.NoiseRemove.CompandThreshold)},
				{"NR Expansion", fmt.Sprintf("%.0f dB", config.NoiseRemove.CompandExpansion)},
			})
		} else {
			writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
				{"NR Compander", "disabled"},
			})
		}
		if config.Deesser.Intensity > 0 {
			writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
				{"De-esser", fmt.Sprintf("%.0f%% intensity", config.Deesser.Intensity*100)},
			})
		} else {
			writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
				{"De-esser", "disabled (no sibilance detected)"},
			})
		}
		writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
			{"LA-2A Thresh", fmt.Sprintf("%.0f dB", config.LA2A.Threshold)},
			{"LA-2A Ratio", fmt.Sprintf("%.1f:1", config.LA2A.Ratio)},
		})
		if diagnostics != nil && diagnostics.LA2AHighCrestActive {
			writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
				{"LA-2A Crest", fmt.Sprintf("high-crest override active (deficit %.1f dB, severity %.2f)", diagnostics.LA2AHighCrestDeficit, diagnostics.LA2AHighCrestSeverity)},
			})
		}
	}
}

func writeAnalysisSpectralSummary(w io.Writer, measurements *processor.AudioMeasurements) {
	writeAnalysisSection(w, "SPECTRAL SUMMARY")
	writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
		{"Centroid", fmt.Sprintf("%.0f Hz (%s)", measurements.Spectral.Centroid, interpretCentroid(measurements.Spectral.Centroid))},
		{"Spread", fmt.Sprintf("%.0f Hz (%s)", measurements.Spectral.Spread, interpretSpread(measurements.Spectral.Spread))},
		{"Rolloff", fmt.Sprintf("%.0f Hz (%s)", measurements.Spectral.Rolloff, interpretRolloff(measurements.Spectral.Rolloff))},
		{"Flatness", fmt.Sprintf("%.3f (%s)", measurements.Spectral.Flatness, interpretFlatness(measurements.Spectral.Flatness))},
		{"Kurtosis", fmt.Sprintf("%.1f (%s)", measurements.Spectral.Kurtosis, interpretKurtosis(measurements.Spectral.Kurtosis))},
		{"Skewness", fmt.Sprintf("%.2f (%s)", measurements.Spectral.Skewness, interpretSkewness(measurements.Spectral.Skewness))},
		{"Crest", fmt.Sprintf("%.1f (%s)", measurements.Spectral.Crest, interpretCrest(measurements.Spectral.Crest))},
		{"Slope", fmt.Sprintf("%.2e (%s)", measurements.Spectral.Slope, interpretSlope(measurements.Spectral.Slope))},
		{"Decrease", fmt.Sprintf("%.4f (%s)", measurements.Spectral.Decrease, interpretDecrease(measurements.Spectral.Decrease))},
		{"Entropy", fmt.Sprintf("%.3f (%s)", measurements.Spectral.Entropy, interpretEntropy(measurements.Spectral.Entropy))},
		{"Flux", fmt.Sprintf("%.4f (%s)", measurements.Spectral.Flux, interpretFlux(measurements.Spectral.Flux))},
	})
}

func writeAnalysisTips(w io.Writer, measurements *processor.AudioMeasurements, config *processor.EffectiveFilterConfig) {
	tips := GenerateRecordingTips(measurements, config)
	fmt.Fprintln(w)
	writeAnalysisSection(w, "RECORDING TIPS")
	if len(tips) == 0 {
		fmt.Fprintln(w, "  ✓ Your recording setup looks good. No issues detected.")
	} else {
		for _, tip := range tips {
			wrapped := wrapText(tip.Message, 66, "    ")
			fmt.Fprintf(w, "  ⚠ %s\n", wrapped)
		}
	}
}

func hasAnalysisTimings(timings AnalysisTimings) bool {
	return timings.Analysis > 0 || timings.Adaptation > 0 || timings.ReportOutput > 0
}

func writeAnalysisMetricRows(w io.Writer, indent string, labelWidth int, rows []analysisMetricSpec) {
	for _, row := range rows {
		fmt.Fprintf(w, "%s%-*s %s\n", indent, labelWidth, row.Label+":", row.Value)
	}
}

func writeAnalysisTimingSection(w io.Writer, timings AnalysisTimings) {
	writeAnalysisSection(w, "ANALYSIS TIMINGS")
	writeAnalysisMetricRows(w, "  ", 14, []analysisMetricSpec{
		{"Analysis", formatDuration(timings.Analysis)},
		{"Adaptation", formatDuration(timings.Adaptation)},
		{"Report Output", formatDuration(timings.ReportOutput)},
	})
}

func completeAnalysisTimings(timings AnalysisTimings, reportOutputStart time.Time) AnalysisTimings {
	if timings.ReportOutput <= 0 {
		timings.ReportOutput = time.Since(reportOutputStart)
	}
	return timings
}

// noiseFloorSourceLabel returns a human-readable label for the noise floor derivation source.
func noiseFloorSourceLabel(source string) string {
	switch source {
	case "astats":
		return "from astats"
	case "rms_estimate":
		return "estimated from RMS level"
	case "ebur128_estimate":
		return "estimated from loudness"
	case "silence_profile":
		return "from silence profile"
	default:
		return "derived"
	}
}

// writeAnalysisSection writes a section header for analysis output.
func writeAnalysisSection(w io.Writer, title string) {
	fmt.Fprintln(w, title)
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
		minutes %= 60
		return fmt.Sprintf("%dh %dm %.0fs", hours, minutes, seconds)
	}
	return fmt.Sprintf("%dm %.0fs", minutes, seconds)
}
