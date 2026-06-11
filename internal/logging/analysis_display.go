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

// AnalysisLogPath derives the analysis report log path for an input file:
// <dir>/<stem>-<ext>-analysis.log, in the same directory as the source. The
// extension is folded into the name so inputs that share a stem but differ by
// extension (e.g. foo.flac and foo.wav in a mixed-format batch directory) get
// distinct logs instead of silently clobbering one another. Examples:
// /x/voice.flac → /x/voice-flac-analysis.log; /tmp/raw → /tmp/raw-analysis.log.
func AnalysisLogPath(inputPath string) string {
	dir := filepath.Dir(inputPath)
	filename := filepath.Base(inputPath)
	ext := filepath.Ext(filename)
	nameWithoutExt := strings.TrimSuffix(filename, ext)
	stem := nameWithoutExt
	if ext != "" {
		stem += "-" + strings.TrimPrefix(ext, ".")
	}
	return filepath.Join(dir, stem+"-analysis.log")
}

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
	writeAnalysisRoomToneDetection(w, measurements)
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
		{"Integrated", fmt.Sprintf("%.1f LUFS", measurements.Loudness.InputI)},
		{"True Peak", fmt.Sprintf("%.1f dBTP", measurements.Loudness.InputTP)},
		{"Loudness Range", fmt.Sprintf("%.1f LU", measurements.Loudness.InputLRA)},
	})
	fmt.Fprintln(w)

	writeAnalysisSection(w, "DYNAMICS")
	crestFactor := measurements.Dynamics.PeakLevel - measurements.Dynamics.RMSLevel
	crestSource := "full-file"
	if measurements.Regions.SpeechProfile != nil && measurements.Regions.SpeechProfile.CrestFactor > 0 {
		crestFactor = measurements.Regions.SpeechProfile.CrestFactor
		crestSource = "speech"
	}
	writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
		{"RMS Level", fmt.Sprintf("%.1f dBFS", measurements.Dynamics.RMSLevel)},
		{"Peak Level", fmt.Sprintf("%.1f dBFS", measurements.Dynamics.PeakLevel)},
		{"Dynamic Range", fmt.Sprintf("%.1f dB", measurements.Dynamics.DynamicRange)},
		{"Crest Factor", fmt.Sprintf("%.1f dB (%s)", crestFactor, crestSource)},
	})
	fmt.Fprintln(w)
}

func writeAnalysisRoomToneDetection(w io.Writer, measurements *processor.AudioMeasurements) {
	writeAnalysisSection(w, "ROOM TONE DETECTION")
	fmt.Fprintf(w, "  Threshold:      %.1f dB (%.1f dBFS room tone estimate + 1 dB)\n",
		measurements.Noise.RoomToneDetectLevel, measurements.Noise.FloorPrescan)

	writeAnalysisRoomToneCandidates(w, measurements)
	fmt.Fprintln(w)
}

func writeAnalysisSpeechDetection(w io.Writer, measurements *processor.AudioMeasurements) {
	writeAnalysisSection(w, "SPEECH DETECTION")
	writeAnalysisSpeechCandidates(w, measurements)
	fmt.Fprintln(w)
}

func writeAnalysisDerivedMeasurements(w io.Writer, measurements *processor.AudioMeasurements) {
	writeAnalysisSection(w, "DERIVED MEASUREMENTS")
	if measurements.Regions.NoiseProfile != nil {
		suggestedGateDB := processor.LinearToDb(measurements.SuggestedGateThreshold)
		writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
			{"Noise Floor", fmt.Sprintf("%.1f dBFS (from elected room tone)", measurements.Regions.NoiseProfile.MeasuredNoiseFloor)},
			{"Gate Baseline", fmt.Sprintf("%.1f dB (noise floor + margin)", suggestedGateDB)},
			{"NR Headroom", fmt.Sprintf("%.1f dB (noise-to-speech gap)", measurements.Noise.ReductionHeadroom)},
		})
	} else {
		writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
			{"Noise Floor", fmt.Sprintf("%.1f dBFS (%s)", measurements.Noise.Floor, noiseFloorSourceLabel(measurements.Noise.FloorSource))},
		})
	}
	fmt.Fprintln(w)
}

func writeAnalysisFilterAdaptation(w io.Writer, measurements *processor.AudioMeasurements, config *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics) {
	writeAnalysisSection(w, "FILTER ADAPTATION")
	if config != nil {
		writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
			{"Highpass", fmt.Sprintf("%.0f Hz (fixed)", config.DS201HighPass.Frequency)},
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
		if measurements.Regions.NoiseProfile != nil {
			gateThresholdDB := processor.LinearToDb(config.DS201Gate.Threshold)
			gateDesc := "(from noise floor)"
			if measurements.Regions.SpeechProfile != nil {
				gateDesc = "(speech-aware)"
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
		if config.NoiseRemove.AfftdnEnabled {
			writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
				{"NR FFT denoise", fmt.Sprintf("%g dB (fixed)", config.NoiseRemove.AfftdnNoiseReduction)},
			})
		}
		if config.Deesser.Intensity > 0 {
			value := fmt.Sprintf("%.0f%% intensity", config.Deesser.Intensity*100)
			if measurements != nil && measurements.Regions.SpeechProfile != nil {
				excess := measurements.Regions.SpeechProfile.SibBandRMS - measurements.Regions.SpeechProfile.BodyBandRMS
				value = fmt.Sprintf("%.0f%% intensity (sibilance excess %.1f dB)", config.Deesser.Intensity*100, excess)
			}
			writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
				{"De-esser", value},
			})
		} else {
			value := "OFF (no speech profile; full-file metrics unreliable)"
			if measurements != nil && measurements.Regions.SpeechProfile != nil {
				if !measurements.Regions.SpeechProfile.BandsMeasured {
					value = "OFF (speech-band RMS not measured)"
				} else {
					excess := measurements.Regions.SpeechProfile.SibBandRMS - measurements.Regions.SpeechProfile.BodyBandRMS
					value = fmt.Sprintf("OFF (sibilance excess %.1f dB)", excess)
				}
			}
			writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
				{"De-esser", value},
			})
		}
		thresholdSource := "adapted"
		if measurements != nil && measurements.Regions.SpeechProfile != nil {
			thresholdSource = fmt.Sprintf("speech RMS %.1f + %.0f dB", measurements.Regions.SpeechProfile.RMSLevel, processor.LA2AThresholdSpeechOffsetDB)
		} else if measurements != nil {
			thresholdSource = fmt.Sprintf("peak %.1f - 20 dB (no speech profile)", measurements.Dynamics.PeakLevel)
		}
		writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
			{"LA-2A Thresh", fmt.Sprintf("%.1f dB (%s)", config.LA2A.Threshold, thresholdSource)},
			{"LA-2A Ratio", fmt.Sprintf("%.1f:1 (fixed)", config.LA2A.Ratio)},
		})
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
		return "from room tone profile"
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
