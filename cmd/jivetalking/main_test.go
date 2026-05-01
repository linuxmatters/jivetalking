package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/logging"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

func TestRunAnalysisOnlyWithDeps_NonTTYOmitsBenchPath(t *testing.T) {
	inputPath := ".bench/analysis/input/sample.wav"
	config := processor.DefaultFilterConfig()
	var output bytes.Buffer

	runAnalysisOnlyWithDeps([]string{inputPath}, config, func(string, ...any) {}, analysisOnlyDeps{
		stdout: &output,
		hasTTY: func() bool {
			return false
		},
		openMetadata: func(path string) (*audio.Metadata, error) {
			if path != inputPath {
				t.Fatalf("openMetadata path = %q, want %q", path, inputPath)
			}
			return &audio.Metadata{
				Duration:   120,
				SampleRate: 48000,
				Channels:   1,
			}, nil
		},
		runWithTUI: func(string, *processor.FilterChainConfig, func(string, ...any)) (*processor.AnalysisResult, error) {
			t.Fatal("runWithTUI should not be called for non-TTY output")
			return nil, nil
		},
		analyzeDetailed: func(path string, cfg *processor.FilterChainConfig, progress func(processor.PassNumber, string, float64, float64, *processor.AudioMeasurements)) (*processor.AnalysisResult, error) {
			if path != inputPath {
				t.Fatalf("analyzeDetailed path = %q, want %q", path, inputPath)
			}
			if progress != nil {
				t.Fatal("progress callback should be nil for non-TTY output")
			}
			return &processor.AnalysisResult{
				Measurements:       makeAnalysisOnlyTestMeasurements(),
				Config:             cfg,
				AnalysisDuration:   2 * time.Second,
				AdaptationDuration: 100 * time.Millisecond,
			}, nil
		},
		displayResults: logging.DisplayAnalysisResults,
		printError: func(message string) {
			t.Fatalf("printError called: %s", message)
		},
	})

	got := output.String()
	if strings.Contains(got, ".bench/") {
		t.Fatalf("analysis-only output leaked benchmark path:\n%s", got)
	}
	for _, want := range []string{
		"Analysing: sample.wav",
		"ANALYSIS: sample.wav",
		"ANALYSIS TIMINGS",
		"Analysis:",
		"Adaptation:",
		"Report Output:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("analysis-only output missing %q:\n%s", want, got)
		}
	}
}

func makeAnalysisOnlyTestMeasurements() *processor.AudioMeasurements {
	return &processor.AudioMeasurements{
		BaseMeasurements: processor.BaseMeasurements{
			RMSLevel:     -24,
			PeakLevel:    -6,
			DynamicRange: 18,
		},
		InputI:             -23,
		InputTP:            -1,
		InputLRA:           6,
		NoiseFloor:         -50,
		NoiseFloorSource:   "rms_estimate",
		PreScanNoiseFloor:  -50,
		SilenceDetectLevel: -45,
	}
}
