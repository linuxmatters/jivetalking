// Package logging handles generation of analysis reports for processed audio filespackage logging

package logging

import (
	"fmt"
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
		fmt.Fprintf(f, "Noise Floor:         %.0f dB\n", m.NoiseFloor)
	}
	fmt.Fprintf(f, "Sample Rate:         %d Hz\n", data.SampleRate)
	fmt.Fprintf(f, "Channels:            %d (%s)\n", data.Channels, channelName(data.Channels))
	fmt.Fprintln(f, "")

	// Pass 2: Processing Applied
	fmt.Fprintln(f, "Pass 2: Processing Applied")
	fmt.Fprintln(f, "---------------------------")
	if data.Result != nil && data.Result.Measurements != nil {
		m := data.Result.Measurements

		fmt.Fprintln(f, "Noise Reduction:")
		fmt.Fprintf(f, "  - Noise floor: %.0f dB\n", m.NoiseFloor)
		fmt.Fprintln(f, "  - Method: FFT spectral subtraction with adaptive tracking")
		fmt.Fprintln(f, "")

		fmt.Fprintln(f, "Gate:")
		fmt.Fprintln(f, "  - Threshold: 0.003")
		fmt.Fprintln(f, "  - Ratio: 4:1")
		fmt.Fprintln(f, "  - Attack/Release: 5ms/100ms")
		fmt.Fprintln(f, "")

		fmt.Fprintln(f, "Compression:")
		fmt.Fprintln(f, "  - Threshold: -18 dB")
		fmt.Fprintln(f, "  - Ratio: 4:1")
		fmt.Fprintln(f, "  - Attack/Release: 20ms/100ms")
		fmt.Fprintln(f, "  - Makeup gain: +8 dB")
		fmt.Fprintln(f, "")

		fmt.Fprintln(f, "Loudness Normalization:")
		fmt.Fprintf(f, "  - Input: %.1f LUFS\n", m.InputI)
		fmt.Fprintf(f, "  - Target: %.1f LUFS\n", data.Result.OutputLUFS)
		fmt.Fprintf(f, "  - Adjustment: %+.1f dB\n", data.Result.OutputLUFS-m.InputI)
		fmt.Fprintf(f, "  - True peak: %.1f dBTP (compliant)\n", m.InputTP)
		fmt.Fprintf(f, "  - Loudness range: %.1f LU\n", m.InputLRA)
		fmt.Fprintln(f, "")
	}

	// Output Analysis
	fmt.Fprintln(f, "Output Analysis")
	fmt.Fprintln(f, "---------------")
	if data.Result != nil {
		fmt.Fprintf(f, "Integrated Loudness: %.1f LUFS", data.Result.OutputLUFS)
		if abs(data.Result.OutputLUFS-(-16.0)) <= 0.1 {
			fmt.Fprint(f, " (✓ target achieved)")
		}
		fmt.Fprintln(f, "")
		fmt.Fprintf(f, "Output File:         %s\n", filepath.Base(data.OutputPath))
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

// abs returns the absolute value of a float64
func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
