package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

func makeReportData(t *testing.T) ReportData {
	t.Helper()

	start := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	return ReportData{
		InputPath:  filepath.Join(t.TempDir(), "input.wav"),
		OutputPath: filepath.Join(t.TempDir(), "input-LUFS-16-processed.flac"),
		StartTime:  start,
		EndTime:    start.Add(8 * time.Second),
		Timings: ProcessingTimings{
			Pass1: 1 * time.Second,
			Pass2: 2 * time.Second,
			Pass3: 3 * time.Second,
			Pass4: 4 * time.Second,
		},
		Result: &processor.ProcessingResult{
			Config:     processor.DefaultFilterConfig(),
			NormResult: &processor.NormalisationResult{},
			RegionTimings: processor.RegionMeasurementTimings{
				FilteredOutput: 100 * time.Millisecond,
				FinalOutput:    200 * time.Millisecond,
			},
		},
		SampleRate:   48000,
		Channels:     1,
		DurationSecs: 120,
	}
}

func TestGenerateReport_RegionMetricTimings(t *testing.T) {
	output := generateReportText(t, makeReportData(t))

	for _, want := range []string{
		"Metric Timings",
		"Filtered Regions:     0.1s",
		"Final Regions:        0.2s",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("report missing %q", want)
		}
	}

	for _, blocked := range []string{
		"wall time",
		"user CPU",
		"system CPU",
		"RSS",
		"pprof",
		".bench/",
	} {
		if strings.Contains(output, blocked) {
			t.Errorf("report contains benchmark/runtime artefact field %q", blocked)
		}
	}
}

func generateReportText(t *testing.T, data ReportData) string {
	t.Helper()

	if err := GenerateReport(data); err != nil {
		t.Fatalf("GenerateReport failed: %v", err)
	}

	logPath := strings.TrimSuffix(data.OutputPath, filepath.Ext(data.OutputPath)) + ".log"
	text, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read report: %v", err)
	}
	return string(text)
}

func TestGenerateReport_ProcessingTimingLabels(t *testing.T) {
	output := generateReportText(t, makeReportData(t))

	for _, want := range []string{
		"Pass 1 (Analysis):    1.0s",
		"Pass 2 (Processing):  2.0s",
		"Pass 3 (Measuring):   3.0s",
		"Pass 4 (Normalising): 4.0s",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("report missing %q", want)
		}
	}
}

func TestGenerateReport_SkippedNormalisationTimingLabels(t *testing.T) {
	data := makeReportData(t)
	data.Timings.Pass3 = 0
	data.Timings.Pass4 = 0
	data.Result.NormResult.Skipped = true

	output := generateReportText(t, data)

	for _, want := range []string{
		"Pass 3 (Measuring):   skipped",
		"Pass 4 (Normalising): skipped",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("report missing %q", want)
		}
	}
}
