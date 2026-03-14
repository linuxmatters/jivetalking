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

	if !strings.Contains(output, "NR Compander:   disabled (no noise profile)") {
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
