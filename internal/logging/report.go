// Package logging handles generation of analysis reports for processed audio filespackage logging

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
		fmt.Fprintf(f, "Dynamic Range:       %.1f dB\n", m.DynamicRange)
		fmt.Fprintf(f, "RMS Level:           %.1f dBFS\n", m.RMSLevel)
		fmt.Fprintf(f, "Peak Level:          %.1f dBFS\n", m.PeakLevel)
		fmt.Fprintf(f, "Spectral Centroid:   %.0f Hz\n", m.SpectralCentroid)
		fmt.Fprintf(f, "Spectral Rolloff:    %.0f Hz\n", m.SpectralRolloff)
	}
	fmt.Fprintf(f, "Sample Rate:         %d Hz\n", data.SampleRate)
	fmt.Fprintf(f, "Channels:            %d (%s)\n", data.Channels, channelName(data.Channels))
	fmt.Fprintln(f, "")

	// Adaptive Processing Decisions
	fmt.Fprintln(f, "Adaptive Processing Decisions")
	fmt.Fprintln(f, "-----------------------------")
	if data.Result != nil && data.Result.Config != nil {
		cfg := data.Result.Config
		m := data.Result.Measurements

		fmt.Fprintf(f, "Highpass Frequency:  %.0f Hz (adaptive, based on centroid)\n", cfg.HighpassFreq)

		// Show adaptive noise reduction
		if m.InputI != 0.0 {
			lufsGap := cfg.TargetI - m.InputI
			fmt.Fprintf(f, "Noise Reduction:     %.1f dB (adaptive, LUFS gap: %.1f dB)\n", cfg.NoiseReduction, lufsGap)
		} else {
			fmt.Fprintf(f, "Noise Reduction:     %.1f dB (default)\n", cfg.NoiseReduction)
		}

		// Show deesser decision with both factors
		if cfg.DeessIntensity > 0 {
			fmt.Fprintf(f, "De-esser Intensity:  %.2f (adaptive, centroid: %.0f Hz, rolloff: %.0f Hz)\n",
				cfg.DeessIntensity, m.SpectralCentroid, m.SpectralRolloff)
		} else {
			fmt.Fprintf(f, "De-esser Intensity:  %.2f (disabled, insufficient HF content)\n", cfg.DeessIntensity)
		}

		// Calculate gate threshold in dB for display
		gateThresholdDB := 20.0 * math.Log10(cfg.GateThreshold)
		fmt.Fprintf(f, "Gate Threshold:      %.1f dB (adaptive, based on noise floor)\n", gateThresholdDB)

		// Show adaptive compression settings
		if m.DynamicRange > 0 {
			fmt.Fprintf(f, "Compression Ratio:   %.1f:1 (adaptive, DR: %.1f dB)\n", cfg.CompRatio, m.DynamicRange)
			fmt.Fprintf(f, "Compression Thresh:  %.0f dB (adaptive, DR: %.1f dB)\n", cfg.CompThreshold, m.DynamicRange)
		} else {
			fmt.Fprintf(f, "Compression Ratio:   %.1f:1 (default)\n", cfg.CompRatio)
			fmt.Fprintf(f, "Compression Thresh:  %.0f dB (default)\n", cfg.CompThreshold)
		}
	}
	fmt.Fprintln(f, "") // Pass 2: Processing Applied
	fmt.Fprintln(f, "Pass 2: Processing Applied")
	fmt.Fprintln(f, "---------------------------")
	if data.Result != nil && data.Result.Measurements != nil {
		m := data.Result.Measurements

		fmt.Fprintln(f, "Noise Reduction:")
		fmt.Fprintf(f, "  - Noise floor: %.1f dB (measured)\n", m.NoiseFloor)
		fmt.Fprintln(f, "  - Method: FFT spectral subtraction with adaptive tracking")

		if data.Result.Config != nil {
			fmt.Fprintf(f, "  - Reduction: %.1f dB (adaptive, based on input LUFS)\n", data.Result.Config.NoiseReduction)
		}
		fmt.Fprintln(f, "")

		if data.Result.Config != nil {
			cfg := data.Result.Config
			gateThresholdDB := 20.0 * math.Log10(cfg.GateThreshold)

			fmt.Fprintln(f, "Gate:")
			fmt.Fprintf(f, "  - Threshold: %.1f dB (adaptive, based on noise floor)\n", gateThresholdDB)
			fmt.Fprintf(f, "  - Ratio: %.1f:1\n", cfg.GateRatio)
			fmt.Fprintf(f, "  - Attack/Release: %.0fms/%.0fms\n", cfg.GateAttack, cfg.GateRelease)
			fmt.Fprintln(f, "")

			compThresholdDB := cfg.CompThreshold
			fmt.Fprintln(f, "Compression:")
			fmt.Fprintf(f, "  - Threshold: %.0f dB\n", compThresholdDB)
			fmt.Fprintf(f, "  - Ratio: %.1f:1\n", cfg.CompRatio)
			fmt.Fprintf(f, "  - Attack/Release: %.0fms/%.0fms\n", cfg.CompAttack, cfg.CompRelease)
			fmt.Fprintf(f, "  - Makeup gain: %+.0f dB\n", cfg.CompMakeup)
			fmt.Fprintln(f, "")
		}

		fmt.Fprintln(f, "Adaptive Normalization:")
		fmt.Fprintf(f, "  - Input: %.1f LUFS\n", m.InputI)

		// Show speechnorm configuration with expansion cap info
		if data.Result.Config != nil && data.Result.Config.SpeechnormExpansion > 1.0 {
			cfg := data.Result.Config
			lufsGap := cfg.TargetI - m.InputI
			expansionDB := 20.0 * math.Log10(cfg.SpeechnormExpansion)

			fmt.Fprintf(f, "  - Method: speechnorm (cycle-level normalization)\n")
			fmt.Fprintf(f, "  - Expansion: %.1fx (%.1f dB)\n", cfg.SpeechnormExpansion, expansionDB)

			// Show if expansion was capped
			if lufsGap > 20.0 {
				expectedLUFS := m.InputI + expansionDB
				fmt.Fprintf(f, "  - Note: Expansion capped at 10x (20 dB) for audio quality\n")
				fmt.Fprintf(f, "  - Expected output: ~%.1f LUFS (gap was %.1f dB, capped from %.1f LUFS target)\n",
					expectedLUFS, lufsGap, cfg.TargetI)
			}

			if cfg.SpeechnormRMS > 0.0 {
				fmt.Fprintf(f, "  - RMS target: %.3f\n", cfg.SpeechnormRMS)
			}

			// Show arnndn status (RNN denoise)
			if cfg.ArnnDnEnabled {
				fmt.Fprintf(f, "  - RNN denoise: ENABLED (expansion %.1fx >= 8.0x threshold)\n", cfg.SpeechnormExpansion)
				fmt.Fprintf(f, "    Neural network mop-up of amplified noise after expansion\n")
			}

			// Show anlmdn status (Non-Local Means denoise)
			if cfg.AnlmDnEnabled {
				fmt.Fprintf(f, "  - NLM denoise: ENABLED (expansion %.1fx >= 8.0x threshold)\n", cfg.SpeechnormExpansion)
				fmt.Fprintf(f, "    Patch-based cleanup, strength: %.5f\n", cfg.AnlmDnStrength)
			}
		} else {
			fmt.Fprintf(f, "  - Method: dynaudnorm (adaptive peak-based)\n")
		}

		fmt.Fprintf(f, "  - True peak: %.1f dBTP (input)\n", m.InputTP)
		fmt.Fprintf(f, "  - Loudness range: %.1f LU (input)\n", m.InputLRA)
		fmt.Fprintln(f, "")
	}

	// Output Analysis
	fmt.Fprintln(f, "Output Analysis")
	fmt.Fprintln(f, "---------------")
	if data.Result != nil {
		fmt.Fprintf(f, "Output File:         %s\n", filepath.Base(data.OutputPath))
		fmt.Fprintln(f, "Note: Output LUFS not measured (would require third-pass analysis)")
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
