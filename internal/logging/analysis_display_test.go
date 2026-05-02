package logging

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// makeMinimalMeasurements creates measurements with enough data to exercise DisplayAnalysisResults.
func makeMinimalMeasurements() *processor.AudioMeasurements {
	return &processor.AudioMeasurements{
		BaseMeasurements: processor.BaseMeasurements{
			RMSLevel:     -24.0,
			PeakLevel:    -6.0,
			DynamicRange: 18.0,
		},
		InputI:             -23.0,
		InputTP:            -1.0,
		InputLRA:           6.0,
		PreScanNoiseFloor:  -50.0,
		SilenceDetectLevel: -45.0,
	}
}

func makeMinimalMetadata() *audio.Metadata {
	return &audio.Metadata{
		Duration:   120.0,
		SampleRate: 48000,
		Channels:   1,
	}
}

func TestDisplayAnalysisResults_VoiceActivated_NoElectedCandidate(t *testing.T) {
	m := makeMinimalMeasurements()
	m.VoiceActivated = true
	m.SilenceCandidates = []processor.SilenceCandidateMetrics{
		{
			Score:            0.0,
			TransientWarning: "rejected: digital silence",
			Region:           processor.SilenceRegion{Start: 10 * time.Second, Duration: 5 * time.Second},
		},
	}

	config := processor.DefaultFilterConfig()
	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config)
	output := buf.String()

	if !strings.Contains(output, "Voice-activated recording detected") {
		t.Error("expected 'Voice-activated recording detected' in output for voice-activated recording with no elected candidate")
	}
}

func TestDisplayAnalysisResults_VoiceActivated_NoSilence(t *testing.T) {
	m := makeMinimalMeasurements()
	m.VoiceActivated = true
	// No SilenceCandidates, no NoiseProfile

	config := processor.DefaultFilterConfig()
	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config)
	output := buf.String()

	if !strings.Contains(output, "No silence detected") {
		t.Error("expected 'No silence detected' in output")
	}
	if !strings.Contains(output, "Voice-activated recording detected") {
		t.Error("expected 'Voice-activated recording detected' in no-silence branch")
	}
}

func TestDisplayAnalysisResults_Normal_NoVoiceActivated(t *testing.T) {
	m := makeMinimalMeasurements()
	m.VoiceActivated = false
	m.NoiseProfile = &processor.NoiseProfile{
		Start:              30 * time.Second,
		Duration:           5 * time.Second,
		MeasuredNoiseFloor: -55.0,
	}
	m.SilenceCandidates = []processor.SilenceCandidateMetrics{
		{
			Score:  0.85,
			Region: processor.SilenceRegion{Start: 30 * time.Second, Duration: 5 * time.Second},
		},
	}

	config := processor.DefaultFilterConfig()
	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config)
	output := buf.String()

	if strings.Contains(output, "Voice-activated") {
		t.Error("expected no 'Voice-activated' in output for normal recording")
	}
}

func TestDisplayAnalysisResults_CompanderDisabled(t *testing.T) {
	m := makeMinimalMeasurements()
	config := processor.DefaultFilterConfig()
	config.NoiseRemoveCompandEnabled = false

	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config)
	output := buf.String()

	if !strings.Contains(output, "NR Compander:   disabled") {
		t.Error("expected 'NR Compander: disabled' when compander is disabled")
	}
	if strings.Contains(output, "NR Threshold:") {
		t.Error("expected no 'NR Threshold' line when compander is disabled")
	}
	if strings.Contains(output, "NR Expansion:") {
		t.Error("expected no 'NR Expansion' line when compander is disabled")
	}
}

func TestDisplayAnalysisResults_CompanderEnabled(t *testing.T) {
	m := makeMinimalMeasurements()
	config := processor.DefaultFilterConfig()
	config.NoiseRemoveCompandEnabled = true
	config.NoiseRemoveCompandThreshold = -55
	config.NoiseRemoveCompandExpansion = 6

	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config)
	output := buf.String()

	if !strings.Contains(output, "NR Threshold:") {
		t.Error("expected 'NR Threshold' when compander is enabled")
	}
	if !strings.Contains(output, "NR Expansion:") {
		t.Error("expected 'NR Expansion' when compander is enabled")
	}
	if strings.Contains(output, "NR Compander:") {
		t.Error("expected no 'NR Compander: disabled' when compander is enabled")
	}
}

func TestDisplayAnalysisResults_TimingLabels(t *testing.T) {
	m := makeMinimalMeasurements()
	config := processor.DefaultFilterConfig()
	timings := AnalysisTimings{
		Analysis:     2 * time.Second,
		Adaptation:   100 * time.Millisecond,
		ReportOutput: 50 * time.Millisecond,
	}

	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config, timings)
	output := buf.String()

	for _, want := range []string{
		"ANALYSIS TIMINGS",
		"Analysis:",
		"Adaptation:",
		"Report Output:",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("expected %q in output", want)
		}
	}
}

func TestCompleteAnalysisTimings_CapturesReportOutput(t *testing.T) {
	timings := AnalysisTimings{
		Analysis:   2 * time.Second,
		Adaptation: 100 * time.Millisecond,
	}

	completed := completeAnalysisTimings(timings, time.Now().Add(-2*time.Second))

	if completed.ReportOutput <= 0 {
		t.Fatal("expected ReportOutput to be captured")
	}
	if completed.Analysis != timings.Analysis {
		t.Errorf("Analysis = %s, want %s", completed.Analysis, timings.Analysis)
	}
	if completed.Adaptation != timings.Adaptation {
		t.Errorf("Adaptation = %s, want %s", completed.Adaptation, timings.Adaptation)
	}
}

func TestDisplayAnalysisResults_ExcludesProcessingOnlyFields(t *testing.T) {
	m := makeMinimalMeasurements()
	config := processor.DefaultFilterConfig()
	timings := AnalysisTimings{
		Analysis:   2 * time.Second,
		Adaptation: 100 * time.Millisecond,
	}

	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config, timings)
	output := buf.String()

	for _, forbidden := range []string{
		"Pass 2",
		"Pass 3",
		"Pass 4",
		"Filtered Regions",
		"Filtered Measurements",
		"Final Regions",
		"Final Measurements",
		"Loudnorm",
		"loudnorm",
		"-processed.log",
		".bench/",
		"pprof",
		"Wall Time",
		"User CPU",
		"System CPU",
		"RSS",
	} {
		if strings.Contains(output, forbidden) {
			t.Errorf("expected analysis output to omit %q", forbidden)
		}
	}
}

func TestDisplayAnalysisResults_CapsSilenceCandidatesChronologicallyWithElectedFirst(t *testing.T) {
	m := makeMinimalMeasurements()
	m.SilenceCandidates = makeRankedSilenceCandidates(12)
	m.NoiseProfile = &processor.NoiseProfile{
		Start:              m.SilenceCandidates[0].Region.Start,
		Duration:           m.SilenceCandidates[0].Region.Duration,
		MeasuredNoiseFloor: m.SilenceCandidates[0].RMSLevel,
	}

	config := processor.DefaultFilterConfig()
	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config)
	output := buf.String()

	for _, want := range []string{
		"  Candidates:     12 evaluated",
		"  Displayed:      elected + top 10 chronological (1 omitted)",
		"  #1: 5.0s at 1.0s (elected)",
		"  #2: 5.0s at 2.0s (score: 0.020)",
		"      RMS: -59.8 dBFS, Crest: 12.0 dB, Entropy: 0.420 (mixed voiced/unvoiced)",
		"  Rejected:       0",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("analysis output missing %q", want)
		}
	}

	for _, detailedLine := range []string{
		"      Score:       ",
		"      RMS Level:   ",
		"      Peak Level:  ",
		"      Flatness:    ",
		"      Kurtosis:    ",
	} {
		if count := strings.Count(output, detailedLine); count != 1 {
			t.Fatalf("analysis silence detail line %q count = %d, want 1", strings.TrimSpace(detailedLine), count)
		}
	}

	if count := strings.Count(output, "  #"); count != 11 {
		t.Fatalf("displayed analysis silence candidates = %d, want 11", count)
	}
	assertAppearsBefore(t, output, "#1:", "#2:")
	assertAppearsBefore(t, output, "#2:", "#3:")
	assertAppearsBefore(t, output, "#10:", "#11:")
	if strings.Contains(output, "#12:") {
		t.Fatal("expected silence candidate 12 to be omitted")
	}
	if strings.Contains(output, "[SELECTED]") {
		t.Fatal("expected silence output to use elected terminology")
	}
	assertCandidateSummaryTerminology(t, output, "  Displayed:      elected + top 10 chronological (1 omitted)")
}

func TestDisplayAnalysisResults_CapsSpeechCandidatesChronologicallyWithElectedFirst(t *testing.T) {
	m := makeMinimalMeasurements()
	m.SpeechCandidates = makeRankedSpeechCandidates(12)
	m.SpeechCandidates[0].VoicingDensity = 0.82
	m.SpeechProfile = &m.SpeechCandidates[0]

	config := processor.DefaultFilterConfig()
	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config)
	output := buf.String()

	for _, want := range []string{
		"  Candidates:     12 evaluated",
		"  Displayed:      elected + top 10 chronological (1 omitted)",
		"  #1: 60.0s at 1.0s (elected)",
		"      Voicing:     82%",
		"  #2: 60.0s at 2.0s (score: 0.02)",
		"      RMS: -29.8 dBFS, Crest: 10.0 dB, Centroid: 2400 Hz (forward, clear)",
		"  Rejected:       0",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("analysis output missing %q", want)
		}
	}

	for _, detailedLine := range []string{
		"      Score:       ",
		"      RMS Level:   ",
		"      Kurtosis:    ",
		"      Voicing:     ",
	} {
		if count := strings.Count(output, detailedLine); count != 1 {
			t.Fatalf("analysis speech detail line %q count = %d, want 1", strings.TrimSpace(detailedLine), count)
		}
	}

	if count := strings.Count(output, "  #"); count != 11 {
		t.Fatalf("displayed analysis speech candidates = %d, want 11", count)
	}
	assertAppearsBefore(t, output, "#1:", "#2:")
	assertAppearsBefore(t, output, "#2:", "#3:")
	assertAppearsBefore(t, output, "#10:", "#11:")
	if strings.Contains(output, "#12:") {
		t.Fatal("expected speech candidate 12 to be omitted")
	}
	if strings.Contains(output, "[SELECTED]") {
		t.Fatal("expected speech output to use elected terminology")
	}
	assertCandidateSummaryTerminology(t, output, "  Displayed:      elected + top 10 chronological (1 omitted)")
}

func TestDisplayAnalysisResults_SpeechRejectionSummaryIncludesZeroScore(t *testing.T) {
	m := makeMinimalMeasurements()
	m.SpeechCandidates = makeRankedSpeechCandidates(3)
	m.SpeechCandidates[1].Score = 0.0
	m.SpeechProfile = &m.SpeechCandidates[0]

	config := processor.DefaultFilterConfig()
	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config)
	output := buf.String()

	for _, want := range []string{
		"  Candidates:     3 evaluated",
		"  Displayed:      elected + 1 chronological (1 omitted)",
		"  #1: 60.0s at 1.0s (elected)",
		"  #3: 60.0s at 3.0s (score: 0.03)",
		"      RMS: -29.7 dBFS, Crest: 10.0 dB, Centroid: 2400 Hz (forward, clear)",
		"  Rejected:       1 zero score",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("analysis speech output missing %q", want)
		}
	}

	if strings.Contains(output, "#2:") {
		t.Fatal("expected zero-score speech candidate to be omitted from displayed candidates")
	}
	assertCandidateSummaryTerminology(t, output, "  Displayed:      elected + 1 chronological (1 omitted)")
}

func TestDisplayAnalysisResults_SpeechDisplaySummaryIncludesZeroOmitted(t *testing.T) {
	m := makeMinimalMeasurements()
	m.SpeechCandidates = makeRankedSpeechCandidates(4)
	m.SpeechProfile = &m.SpeechCandidates[0]

	config := processor.DefaultFilterConfig()
	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config)
	output := buf.String()

	for _, want := range []string{
		"  Candidates:     4 evaluated",
		"  Displayed:      elected + 3 chronological (0 omitted)",
		"  #1: 60.0s at 1.0s (elected)",
		"  #4: 60.0s at 4.0s (score: 0.04)",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("analysis speech output missing %q", want)
		}
	}
	assertCandidateSummaryTerminology(t, output, "  Displayed:      elected + 3 chronological (0 omitted)")
}

func TestWriteDiagnosticSilence_VoiceActivated_WithCandidates(t *testing.T) {
	m := makeMinimalMeasurements()
	m.VoiceActivated = true
	m.SilenceCandidates = []processor.SilenceCandidateMetrics{
		{
			Score:            0.0,
			TransientWarning: "rejected: digital silence",
			Region:           processor.SilenceRegion{Start: 5 * time.Second, Duration: 3 * time.Second},
		},
	}

	tmpFile, err := os.CreateTemp("", "diag-silence-test-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	writeDiagnosticSilence(tmpFile, m)
	tmpFile.Close()

	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)

	if !strings.Contains(output, "Voice-Activated:     yes") {
		t.Error("expected 'Voice-Activated: yes' in diagnostic silence output with candidates")
	}
}

func TestWriteDiagnosticSilence_VoiceActivated_NoneFound(t *testing.T) {
	m := makeMinimalMeasurements()
	m.VoiceActivated = true
	// No candidates, no noise profile, no silence regions

	tmpFile, err := os.CreateTemp("", "diag-silence-test-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	writeDiagnosticSilence(tmpFile, m)
	tmpFile.Close()

	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)

	if !strings.Contains(output, "NONE FOUND") {
		t.Error("expected 'NONE FOUND' in output")
	}
	if !strings.Contains(output, "Voice-Activated:     yes") {
		t.Error("expected 'Voice-Activated: yes' in NONE FOUND section")
	}
}

func TestWriteDiagnosticSilence_Normal_NoVoiceActivated(t *testing.T) {
	m := makeMinimalMeasurements()
	m.VoiceActivated = false
	// No candidates - triggers NONE FOUND

	tmpFile, err := os.CreateTemp("", "diag-silence-test-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())

	writeDiagnosticSilence(tmpFile, m)
	tmpFile.Close()

	data, err := os.ReadFile(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)

	if strings.Contains(output, "Voice-Activated") {
		t.Error("expected no 'Voice-Activated' in output for normal recording")
	}
}
