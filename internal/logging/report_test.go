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
	config := processor.DefaultEffectiveFilterConfig()
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
			Config:               &processor.EffectiveFilterConfig{FilterChainConfig: *config},
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

func TestWriteDiagnosticSilence_CapsCandidatesChronologicallyWithElectedFirst(t *testing.T) {
	m := makeInputMeasurements()
	m.SilenceCandidates = makeRankedSilenceCandidates(12)
	m.NoiseProfile = &processor.NoiseProfile{
		Start:              m.SilenceCandidates[0].Region.Start,
		Duration:           m.SilenceCandidates[0].Region.Duration,
		MeasuredNoiseFloor: m.SilenceCandidates[0].RMSLevel,
	}

	output := captureReportDiagnostic(t, func(f *os.File) {
		writeDiagnosticSilence(f, m)
	})

	for _, want := range []string{
		"Silence Candidates:  12 evaluated",
		"Displayed:           elected + top 10 chronological (1 omitted)",
		"Candidate 1:       5.0s at 1.0s (score: 0.010, elected)",
		"Candidate 2:       5.0s at 2.0s (score: 0.020)",
		"RMS: -59.8 dBFS, Crest: 12.0 dB, Entropy: 0.420 (mixed voiced/unvoiced)",
		"Rejected:            0",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("silence diagnostic missing %q", want)
		}
	}

	for _, heading := range []string{"    Amplitude:\n", "    Spectral:\n", "    Loudness:\n"} {
		if count := strings.Count(output, heading); count != 1 {
			t.Fatalf("silence diagnostic %q heading count = %d, want 1", strings.TrimSpace(heading), count)
		}
	}

	if count := strings.Count(output, "  Candidate "); count != 11 {
		t.Fatalf("displayed silence candidates = %d, want 11", count)
	}
	assertAppearsBefore(t, output, "Candidate 1:", "Candidate 2:")
	assertAppearsBefore(t, output, "Candidate 2:", "Candidate 3:")
	assertAppearsBefore(t, output, "Candidate 10:", "Candidate 11:")
	if strings.Contains(output, "Candidate 12:") {
		t.Fatal("expected candidate 12 to be omitted")
	}
	if strings.Contains(output, "[SELECTED]") {
		t.Fatal("expected silence diagnostic to use elected terminology")
	}
	assertCandidateSummaryTerminology(t, output, "Displayed:           elected + top 10 chronological (1 omitted)")
}

func TestWriteDiagnosticSpeech_CapsCandidatesChronologicallyWithElectedFirst(t *testing.T) {
	m := makeInputMeasurements()
	m.SpeechCandidates = makeRankedSpeechCandidates(12)
	m.SpeechCandidates[0].VoicingDensity = 0.82
	m.SpeechProfile = &m.SpeechCandidates[0]

	output := captureReportDiagnostic(t, func(f *os.File) {
		writeDiagnosticSpeech(f, m)
	})

	for _, want := range []string{
		"Speech Candidates:   12 evaluated",
		"Displayed:           elected + top 10 chronological (1 omitted)",
		"Candidate 1:       60.0s at 1.0s (score: 0.010, elected)",
		"Voicing Density: 82.0%",
		"Candidate 2:       60.0s at 2.0s (score: 0.020)",
		"RMS: -29.8 dBFS, Crest: 10.0 dB, Centroid: 2400 Hz (forward, clear)",
		"Rejected:            0",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("speech diagnostic missing %q", want)
		}
	}

	for _, heading := range []string{"    Amplitude:\n", "    Spectral:\n", "    Loudness:\n"} {
		if count := strings.Count(output, heading); count != 1 {
			t.Fatalf("speech diagnostic %q heading count = %d, want 1", strings.TrimSpace(heading), count)
		}
	}

	if count := strings.Count(output, "  Candidate "); count != 11 {
		t.Fatalf("displayed speech candidates = %d, want 11", count)
	}
	assertAppearsBefore(t, output, "Candidate 1:", "Candidate 2:")
	assertAppearsBefore(t, output, "Candidate 2:", "Candidate 3:")
	assertAppearsBefore(t, output, "Candidate 10:", "Candidate 11:")
	if strings.Contains(output, "Candidate 12:") {
		t.Fatal("expected candidate 12 to be omitted")
	}
	if strings.Contains(output, "[SELECTED]") {
		t.Fatal("expected speech diagnostic to use elected terminology")
	}
	assertCandidateSummaryTerminology(t, output, "Displayed:           elected + top 10 chronological (1 omitted)")
}

func TestWriteDiagnosticSpeech_RejectionSummaryIncludesZeroScore(t *testing.T) {
	m := makeInputMeasurements()
	m.SpeechCandidates = makeRankedSpeechCandidates(3)
	m.SpeechCandidates[1].Score = 0.0
	m.SpeechProfile = &m.SpeechCandidates[0]

	output := captureReportDiagnostic(t, func(f *os.File) {
		writeDiagnosticSpeech(f, m)
	})

	for _, want := range []string{
		"Speech Candidates:   3 evaluated",
		"Displayed:           elected + 1 chronological (1 omitted)",
		"Candidate 1:       60.0s at 1.0s (score: 0.010, elected)",
		"Candidate 3:       60.0s at 3.0s (score: 0.030)",
		"Rejected:            1 zero score",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("speech diagnostic missing %q", want)
		}
	}

	if strings.Contains(output, "Candidate 2:") {
		t.Fatal("expected zero-score speech candidate to be omitted from displayed candidates")
	}
	assertCandidateSummaryTerminology(t, output, "Displayed:           elected + 1 chronological (1 omitted)")
}

func TestWriteDiagnosticSpeech_DisplaySummaryIncludesZeroOmitted(t *testing.T) {
	m := makeInputMeasurements()
	m.SpeechCandidates = makeRankedSpeechCandidates(4)
	m.SpeechProfile = &m.SpeechCandidates[0]

	output := captureReportDiagnostic(t, func(f *os.File) {
		writeDiagnosticSpeech(f, m)
	})

	for _, want := range []string{
		"Speech Candidates:   4 evaluated",
		"Displayed:           elected + 3 chronological (0 omitted)",
		"Candidate 1:       60.0s at 1.0s (score: 0.010, elected)",
		"Candidate 4:       60.0s at 4.0s (score: 0.040)",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("speech diagnostic missing %q", want)
		}
	}
	assertCandidateSummaryTerminology(t, output, "Displayed:           elected + 3 chronological (0 omitted)")
}

func TestCandidateRejectionSummaries(t *testing.T) {
	silenceCandidates := makeRankedSilenceCandidates(3)
	if got := silenceRejectionSummary(silenceCandidates); got != "0" {
		t.Fatalf("silenceRejectionSummary() = %q, want 0", got)
	}

	silenceCandidates[0].Score = 0.0
	silenceCandidates[0].TransientWarning = "rejected: digital silence"
	silenceCandidates[1].Score = 0.0
	silenceCandidates[1].TransientWarning = "rejected: transient contamination"
	if got := silenceRejectionSummary(silenceCandidates); got != "1 digital silence, 1 transient contamination" {
		t.Fatalf("silenceRejectionSummary() = %q", got)
	}

	speechCandidates := makeRankedSpeechCandidates(3)
	if got := speechRejectionSummary(speechCandidates); got != "0" {
		t.Fatalf("speechRejectionSummary() = %q, want 0", got)
	}
	speechCandidates[1].Score = 0.0
	if got := speechRejectionSummary(speechCandidates); got != "1 zero score" {
		t.Fatalf("speechRejectionSummary() = %q", got)
	}
}

func makeRankedSilenceCandidates(count int) []processor.SilenceCandidateMetrics {
	candidates := make([]processor.SilenceCandidateMetrics, 0, count)
	for i := 1; i <= count; i++ {
		c := *makeSilenceSample(-60.0 + float64(i)/10.0)
		start := time.Duration(i) * time.Second
		c.Region = processor.SilenceRegion{
			Start:    start,
			End:      start + 5*time.Second,
			Duration: 5 * time.Second,
		}
		c.Score = float64(i) / 100.0
		candidates = append(candidates, c)
	}
	return candidates
}

func makeRankedSpeechCandidates(count int) []processor.SpeechCandidateMetrics {
	candidates := make([]processor.SpeechCandidateMetrics, 0, count)
	for i := 1; i <= count; i++ {
		c := *makeSpeechSample(-30.0 + float64(i)/10.0)
		start := time.Duration(i) * time.Second
		c.Region = processor.SpeechRegion{
			Start:    start,
			End:      start + 60*time.Second,
			Duration: 60 * time.Second,
		}
		c.Score = float64(i) / 100.0
		candidates = append(candidates, c)
	}
	return candidates
}

func captureReportDiagnostic(t *testing.T, write func(*os.File)) string {
	t.Helper()

	tmpFile, err := os.CreateTemp("", "report-diagnostic-test-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	write(tmpFile)
	if err := tmpFile.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func assertAppearsBefore(t *testing.T, output, first, second string) {
	t.Helper()

	firstIndex := strings.Index(output, first)
	if firstIndex < 0 {
		t.Fatalf("missing %q", first)
	}
	secondIndex := strings.Index(output, second)
	if secondIndex < 0 {
		t.Fatalf("missing %q", second)
	}
	if firstIndex >= secondIndex {
		t.Fatalf("expected %q to appear before %q", first, second)
	}
}

func assertCandidateSummaryTerminology(t *testing.T, output, expectedSummary string) {
	t.Helper()

	if !strings.Contains(output, expectedSummary) {
		t.Fatalf("missing candidate display summary %q", expectedSummary)
	}
	for _, forbidden := range []string{"by score", "[SELECTED]", "[ELECTED]"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("candidate output contains legacy terminology %q", forbidden)
		}
	}
	if !strings.Contains(output, "elected") {
		t.Fatal("candidate output missing elected terminology")
	}
}
