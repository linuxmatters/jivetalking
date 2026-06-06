// Package logging handles generation of analysis reports for processed audio files

package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// ProcessingTimings contains reportable processing pass durations.
type ProcessingTimings struct {
	Pass1 time.Duration
	Pass2 time.Duration
	Pass3 time.Duration // Loudnorm measurement pass (may be 0 if skipped)
	Pass4 time.Duration // Loudnorm application pass (may be 0 if skipped)
}

// ReportData contains all the information needed to generate an analysis report
type ReportData struct {
	InputPath    string
	OutputPath   string
	StartTime    time.Time
	EndTime      time.Time
	Timings      ProcessingTimings
	Result       *processor.ProcessingResult
	SampleRate   int
	Channels     int
	DurationSecs float64 // Duration in seconds
}

// GenerateReport creates a detailed analysis report and saves it alongside the output file.
// The report filename will be <output>-LUFS-NN-processed.log
//
// Report structure:
// 1. Header - file info and timestamp
// 2. Processing Summary - pass timings
// 3. Filter Chain Applied - adaptive parameters
// 4. Loudness Measurements - three-column table (Input/Filtered/Final)
// 5. Noise Floor Analysis - three-column table
// 6. Speech Region Analysis - three-column table with interpretations
// 7. Diagnostic sections - detailed debug info
func GenerateReport(data ReportData) error {
	// Generate report filename: presenter1-LUFS-16-processed.flac → presenter1-LUFS-16-processed.log
	logPath := strings.TrimSuffix(data.OutputPath, filepath.Ext(data.OutputPath)) + ".log"

	// Create report file
	f, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer f.Close()

	// === Main Report Sections (consolidated tables) ===

	// Header
	writeReportHeader(f, data)

	// Processing Summary (timings)
	writeProcessingSummary(f, data)

	// Prepare measurements for use in multiple sections
	var inputMeasurements *processor.AudioMeasurements
	var filteredMeasurements *processor.OutputMeasurements
	var finalMeasurements *processor.OutputMeasurements
	if data.Result != nil {
		inputMeasurements = data.Result.Measurements
		filteredMeasurements = data.Result.FilteredMeasurements
		finalMeasurements = getFinalMeasurements(data.Result)
	}

	// Room Tone Detection (analysis that informs filter chain decisions)
	writeDiagnosticRoomTone(f, inputMeasurements)

	// Speech Detection (for adaptive tuning)
	writeDiagnosticSpeech(f, inputMeasurements)

	// Filter Chain Applied
	if data.Result != nil && data.Result.Config != nil {
		writeFilterChainApplied(f, data.Result.Config, data.Result.Diagnostics, data.Result.Measurements)
	}

	// Peak Limiter (Pass 4 pre-limiting before loudnorm)
	if data.Result != nil && data.Result.NormResult != nil {
		writeDiagnosticPeakLimiter(f, data.Result.NormResult, data.Result.Config)
	}

	// Loudnorm (follows filter chain as it's the final processing stage)
	if data.Result != nil && data.Result.Config != nil {
		writeDiagnosticLoudnorm(f, data.Result.NormResult, data.Result.Config)
	}

	// Loudness Measurements Table (Input → Filtered → Final)
	writeLoudnessTable(f, inputMeasurements, filteredMeasurements, finalMeasurements)

	// Extract normalisation result for gain-dependent metric compensation
	var normResult *processor.NormalisationResult
	if data.Result != nil {
		normResult = data.Result.NormResult
	}

	// Noise Floor Analysis Table
	writeNoiseFloorTable(f, inputMeasurements, filteredMeasurements, finalMeasurements, normResult)

	// Speech Region Analysis Table
	writeSpeechRegionTable(f, inputMeasurements, filteredMeasurements, finalMeasurements, normResult)

	return nil
}

// =============================================================================
// Tabular Report Section Writers
// =============================================================================

// writeReportHeader outputs the report header with file info and timestamp.
func writeReportHeader(f *os.File, data ReportData) {
	fmt.Fprintln(f, "Jivetalking Analysis Report")
	fmt.Fprintln(f, "============================")
	fmt.Fprintf(f, "File: %s\n", filepath.Base(data.InputPath))
	fmt.Fprintf(f, "Processed: %s\n", data.EndTime.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(f, "Duration: %s\n", formatDuration(time.Duration(data.DurationSecs*float64(time.Second))))
	fmt.Fprintf(f, "Sample Rate: %d Hz\n", data.SampleRate)
	fmt.Fprintf(f, "Channels: %s\n", channelName(data.Channels))
	fmt.Fprintln(f, "")
}

// writeProcessingSummary outputs the processing time summary for all passes.
func writeProcessingSummary(f *os.File, data ReportData) {
	writeSection(f, "Processing Summary")

	fmt.Fprintf(f, "Pass 1 (Analysis):    %s\n", formatDuration(data.Timings.Pass1))
	fmt.Fprintf(f, "Pass 2 (Processing):  %s\n", formatDuration(data.Timings.Pass2))

	if data.Timings.Pass3 > 0 || data.Timings.Pass4 > 0 {
		fmt.Fprintf(f, "Pass 3 (Measuring):   %s\n", formatDuration(data.Timings.Pass3))
		fmt.Fprintf(f, "Pass 4 (Normalising): %s\n", formatDuration(data.Timings.Pass4))
	} else if data.Result != nil && data.Result.NormResult != nil && data.Result.NormResult.Skipped {
		fmt.Fprintln(f, "Pass 3 (Measuring):   skipped")
		fmt.Fprintln(f, "Pass 4 (Normalising): skipped")
	}

	if data.Result != nil && (data.Result.RegionTimings.FilteredOutput > 0 || data.Result.RegionTimings.FinalOutput > 0) {
		fmt.Fprintln(f, "")
		fmt.Fprintln(f, "Metric Timings")
		if data.Result.RegionTimings.FilteredOutput > 0 {
			fmt.Fprintf(f, "Filtered Regions:     %s\n", formatDuration(data.Result.RegionTimings.FilteredOutput))
		}
		if data.Result.RegionTimings.FinalOutput > 0 {
			fmt.Fprintf(f, "Final Regions:        %s\n", formatDuration(data.Result.RegionTimings.FinalOutput))
		}
	}

	totalTime := data.EndTime.Sub(data.StartTime)
	fmt.Fprintf(f, "Total:                %s", formatDuration(totalTime))

	if data.DurationSecs > 0 {
		audioDuration := time.Duration(data.DurationSecs * float64(time.Second))
		rtf := float64(audioDuration) / float64(totalTime)
		fmt.Fprintf(f, " (%.0fx real-time)", rtf)
	}
	fmt.Fprintln(f, "")
	fmt.Fprintln(f, "")
}

// getFinalMeasurements safely extracts final measurements from the result.
func getFinalMeasurements(result *processor.ProcessingResult) *processor.OutputMeasurements {
	if result == nil || result.NormResult == nil {
		return nil
	}
	return result.NormResult.FinalMeasurements
}
