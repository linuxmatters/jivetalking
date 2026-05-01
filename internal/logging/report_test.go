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
			Measurements:         makeInputMeasurements(),
			FilteredMeasurements: makeOutputMeasurements(-20.2, -2.1, 6.4, makeSilenceSample(-64.0), makeSpeechSample(-24.0)),
			Config:               processor.DefaultFilterConfig(),
			NormResult: &processor.NormalisationResult{
				InputLUFS:  -20.2,
				OutputLUFS: -16.0,
				FinalMeasurements: makeOutputMeasurements(
					-16.0,
					-1.1,
					5.9,
					makeSilenceSample(-61.0),
					makeSpeechSample(-19.8),
				),
			},
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

func makeInputMeasurements() *processor.AudioMeasurements {
	silenceRegion := processor.SilenceRegion{
		Start:    10 * time.Second,
		End:      15 * time.Second,
		Duration: 5 * time.Second,
	}
	speechRegion := processor.SpeechRegion{
		Start:    30 * time.Second,
		End:      90 * time.Second,
		Duration: 60 * time.Second,
	}
	silenceSample := makeSilenceSample(-58.0)
	silenceSample.Region = silenceRegion
	speechSample := makeSpeechSample(-28.0)
	speechSample.Region = speechRegion

	return &processor.AudioMeasurements{
		BaseMeasurements: processor.BaseMeasurements{
			SpectralCentroid:  2200,
			SpectralKurtosis:  7.2,
			SpectralFlatness:  0.18,
			SpectralFlux:      0.004,
			DynamicRange:      22.0,
			MomentaryLoudness: -25.0,
			ShortTermLoudness: -24.5,
			SamplePeak:        -4.0,
		},
		InputI:           -24.1,
		InputTP:          -3.2,
		InputLRA:         7.3,
		NoiseFloor:       -58.0,
		NoiseFloorSource: "rms_estimate",
		NoiseProfile: &processor.NoiseProfile{
			Start:              silenceRegion.Start,
			Duration:           silenceRegion.Duration,
			MeasuredNoiseFloor: -58.0,
			PeakLevel:          -45.0,
			CrestFactor:        13.0,
			Entropy:            0.42,
		},
		SilenceCandidates: []processor.SilenceCandidateMetrics{*silenceSample},
		SpeechProfile:     speechSample,
	}
}

func makeOutputMeasurements(inputI, inputTP, inputLRA float64, silenceSample *processor.SilenceCandidateMetrics, speechSample *processor.SpeechCandidateMetrics) *processor.OutputMeasurements {
	return &processor.OutputMeasurements{
		BaseMeasurements: processor.BaseMeasurements{
			MomentaryLoudness: inputI - 0.5,
			ShortTermLoudness: inputI - 0.2,
			SamplePeak:        inputTP - 0.3,
		},
		OutputI:       inputI,
		OutputTP:      inputTP,
		OutputLRA:     inputLRA,
		SilenceSample: silenceSample,
		SpeechSample:  speechSample,
	}
}

func makeSilenceSample(rms float64) *processor.SilenceCandidateMetrics {
	return &processor.SilenceCandidateMetrics{
		Region:      processor.SilenceRegion{Start: 10 * time.Second, End: 15 * time.Second, Duration: 5 * time.Second},
		RMSLevel:    rms,
		PeakLevel:   rms + 12,
		CrestFactor: 12,
		Spectral: processor.SpectralMetrics{
			Mean:     0.001,
			Variance: 0.0001,
			Centroid: 950,
			Spread:   1200,
			Skewness: 0.4,
			Kurtosis: 5.1,
			Entropy:  0.42,
			Flatness: 0.32,
			Crest:    8.5,
			Flux:     0.002,
			Slope:    -0.0002,
			Decrease: 0.04,
			Rolloff:  3600,
		},
		MomentaryLUFS: rms - 2,
		ShortTermLUFS: rms - 1,
		TruePeak:      rms + 12,
		SamplePeak:    rms + 11,
	}
}

func makeSpeechSample(rms float64) *processor.SpeechCandidateMetrics {
	return &processor.SpeechCandidateMetrics{
		Region:      processor.SpeechRegion{Start: 30 * time.Second, End: 90 * time.Second, Duration: 60 * time.Second},
		RMSLevel:    rms,
		PeakLevel:   rms + 10,
		CrestFactor: 10,
		Spectral: processor.SpectralMetrics{
			Mean:     0.01,
			Variance: 0.002,
			Centroid: 2400,
			Spread:   1800,
			Skewness: 0.7,
			Kurtosis: 7.4,
			Entropy:  0.24,
			Flatness: 0.18,
			Crest:    26.0,
			Flux:     0.006,
			Slope:    -0.0001,
			Decrease: 0.06,
			Rolloff:  6200,
		},
		MomentaryLUFS: rms - 1,
		ShortTermLUFS: rms - 0.5,
		TruePeak:      rms + 10,
		SamplePeak:    rms + 9,
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

func TestGenerateReport_MetadataFields(t *testing.T) {
	output := generateReportText(t, makeReportData(t))

	for _, want := range []string{
		"Duration: 2m 0s",
		"Sample Rate: 48000 Hz",
		"Channels: mono",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("report missing %q", want)
		}
	}
}

func TestGenerateReport_AudioMetricTables(t *testing.T) {
	output := generateReportText(t, makeReportData(t))

	for _, want := range []string{
		"Loudness Measurements",
		"Input",
		"Filtered",
		"Final",
		"Integrated Loudness",
		"-24.1",
		"-20.2",
		"-16.0",
		"Noise Floor Analysis",
		"RMS Level",
		"-58.0",
		"-64.0",
		"-61.0",
		"Speech Region Analysis",
		"Spectral Centroid",
		"2400",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("report missing %q", want)
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
