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

func makeFullAnalysisMeasurements() *processor.AudioMeasurements {
	m := makeMinimalMeasurements()
	m.InputI = -18.4
	m.InputTP = -2.1
	m.InputLRA = 7.8
	m.RMSLevel = -28.2
	m.PeakLevel = -7.4
	m.DynamicRange = 20.8
	m.CrestFactor = 11.4
	m.PreScanNoiseFloor = -58.4
	m.SilenceDetectLevel = -57.4
	m.NoiseFloor = -58.4
	m.NoiseFloorSource = "silence_profile"
	m.SuggestedGateThreshold = processor.DbToLinear(-52.4)
	m.NoiseReductionHeadroom = 24.6
	m.Spectral.Centroid = 2450
	m.Spectral.Spread = 1800
	m.Spectral.Rolloff = 7200
	m.Spectral.Flatness = 0.210
	m.Spectral.Kurtosis = 9.7
	m.Spectral.Skewness = 0.82
	m.Spectral.Crest = 12.6
	m.Spectral.Slope = -1.20e-03
	m.Spectral.Decrease = -0.0123
	m.Spectral.Entropy = 0.365
	m.Spectral.Flux = 0.0820
	m.NoiseProfile = &processor.NoiseProfile{
		Start:              6 * time.Second,
		Duration:           4 * time.Second,
		MeasuredNoiseFloor: -58.4,
		PeakLevel:          -44.0,
		CrestFactor:        14.4,
		Entropy:            0.365,
		SpectralFlatness:   0.210,
		SpectralKurtosis:   9.7,
		WasRefined:         true,
		OriginalStart:      5 * time.Second,
		OriginalDuration:   8 * time.Second,
	}
	m.SilenceCandidates = []processor.SilenceCandidateMetrics{
		{
			Region:      processor.SilenceRegion{Start: 5 * time.Second, Duration: 8 * time.Second},
			RMSLevel:    -58.4,
			PeakLevel:   -44.0,
			CrestFactor: 14.4,
			Spectral: processor.SpectralMetrics{
				Entropy:  0.365,
				Flatness: 0.210,
				Kurtosis: 9.7,
				Centroid: 2450,
			},
			Score: 0.910,
		},
		{
			Region:      processor.SilenceRegion{Start: 40 * time.Second, Duration: 3 * time.Second},
			RMSLevel:    -56.1,
			CrestFactor: 10.2,
			Spectral:    processor.SpectralMetrics{Entropy: 0.620},
			Score:       0.420,
		},
		{
			Region:           processor.SilenceRegion{Start: 70 * time.Second, Duration: 2 * time.Second},
			TransientWarning: "rejected: digital silence",
			Score:            0,
		},
	}
	m.SpeechCandidates = []processor.SpeechCandidateMetrics{
		{
			Region:      processor.SpeechRegion{Start: 15 * time.Second, Duration: 60 * time.Second},
			RMSLevel:    -33.8,
			PeakLevel:   -9.0,
			CrestFactor: 12.8,
			Spectral: processor.SpectralMetrics{
				Centroid: 2450,
				Kurtosis: 9.7,
				Rolloff:  7200,
			},
			VoicingDensity: 0.76,
			Score:          0.88,
		},
		{
			Region:      processor.SpeechRegion{Start: 90 * time.Second, Duration: 20 * time.Second},
			RMSLevel:    -35.2,
			CrestFactor: 11.0,
			Spectral:    processor.SpectralMetrics{Centroid: 3100},
			Score:       0.55,
		},
		{
			Region: processor.SpeechRegion{Start: 115 * time.Second, Duration: 5 * time.Second},
			Score:  0,
		},
	}
	m.SpeechProfile = &m.SpeechCandidates[0]
	return m
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

	config := processor.DefaultEffectiveFilterConfig()
	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config)
	output := buf.String()

	if !strings.Contains(output, "Voice-activated recording detected") {
		t.Error("expected 'Voice-activated recording detected' in output for voice-activated recording with no elected candidate")
	}
}

func TestDisplayAnalysisResults_FullOutputFixture(t *testing.T) {
	m := makeFullAnalysisMeasurements()
	config := processor.DefaultEffectiveFilterConfig()
	config.NoiseRemove.CompandEnabled = true
	config.NoiseRemove.CompandThreshold = -53
	config.NoiseRemove.CompandExpansion = 7
	config.Deesser.Intensity = 0.35
	config.LA2A.Threshold = -21
	config.LA2A.Ratio = 2.5
	config.DS201HighPass.Frequency = 85
	config.DS201Gate.Threshold = processor.DbToLinear(-51.2)
	config.DS201Gate.Ratio = 3.0
	timings := AnalysisTimings{
		Analysis:     2*time.Minute + 3*time.Second,
		Adaptation:   1500 * time.Millisecond,
		ReportOutput: 250 * time.Millisecond,
	}

	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/full-fixture.wav", &audio.Metadata{
		Duration:   3672.0,
		SampleRate: 48000,
		Channels:   2,
	}, m, config, timings)

	const want = `======================================================================
ANALYSIS: full-fixture.wav
======================================================================
Duration:    1h 1m 12s
Sample Rate: 48000 Hz
Channels:    stereo

LOUDNESS
  Integrated:     -18.4 LUFS
  True Peak:      -2.1 dBTP
  Loudness Range: 7.8 LU

DYNAMICS
  RMS Level:      -28.2 dBFS
  Peak Level:     -7.4 dBFS
  Dynamic Range:  20.8 dB
  Crest Factor:   12.8 dB (speech)

SILENCE DETECTION
  Threshold:      -57.4 dB (-58.4 dBFS room tone estimate + 1 dB)
  Candidates:     3 evaluated
  Displayed:      elected + 1 chronological (1 omitted)

  #1: 8.0s at 5.0s (elected)
      Refined:     4.0s at 6.0s (golden sub-region)
      Score:       0.910
      RMS Level:   -58.4 dBFS
      Peak Level:  -44.0 dBFS
      Crest:       14.4 dB
      Entropy:     0.365 (mixed voiced/unvoiced)
      Flatness:    0.210 (very tonal, strong harmonics)
      Kurtosis:    9.7 (leptokurtic, clear harmonics)
      Centroid:    2450 Hz

  #2: 3.0s at 40.0s (score: 0.420)
      RMS: -56.1 dBFS, Crest: 10.2 dB, Entropy: 0.620 (disordered, fricatives)

  Rejected:       1 digital silence

SPEECH DETECTION
  Candidates:     3 evaluated
  Displayed:      elected + 1 chronological (1 omitted)

  #1: 60.0s at 15.0s (elected)
      Score:       0.88
      RMS Level:   -33.8 dBFS
      Crest:       12.8 dB
      Centroid:    2450 Hz (forward, clear)
      Kurtosis:    9.7 (leptokurtic, clear harmonics)
      Voicing:     76%

  #2: 20.0s at 1m 30s (score: 0.55)
      RMS: -35.2 dBFS, Crest: 11.0 dB, Centroid: 3100 Hz (bright, articulate)

  Rejected:       1 zero score

DERIVED MEASUREMENTS
  Noise Floor:    -58.4 dBFS (from elected silence)
  Gate Baseline:  -52.4 dB (noise floor + margin)
  NR Headroom:    24.6 dB (noise-to-speech gap)

FILTER ADAPTATION
  Highpass:       85 Hz (from spectral analysis)
  Lowpass:        16000 Hz
  Gate Threshold: -51.2 dB (with breath reduction)
  Gate Ratio:     3.0:1
  NR Threshold:   -53 dB
  NR Expansion:   7 dB
  De-esser:       35% intensity
  LA-2A Thresh:   -21 dB
  LA-2A Ratio:    2.5:1
SPECTRAL SUMMARY
  Centroid:       2450 Hz (forward, clear)
  Spread:         1800 Hz (moderate, natural speech)
  Rolloff:        7200 Hz (good articulation)
  Flatness:       0.210 (very tonal, strong harmonics)
  Kurtosis:       9.7 (leptokurtic, clear harmonics)
  Skewness:       0.82 (LF emphasis with HF tail (typical voice))
  Crest:          12.6 (moderate peaks)
  Slope:          -1.20e-03 (very steep slope, dark/warm)
  Decrease:       -0.0123 (strong bass emphasis)
  Entropy:        0.365 (mixed voiced/unvoiced)
  Flux:           0.0820 (high variation, transients)

RECORDING TIPS
  ✓ Your recording setup looks good. No issues detected.

ANALYSIS TIMINGS
  Analysis:      2m 3s
  Adaptation:    1.5s
  Report Output: 0.2s
`
	if got := buf.String(); got != want {
		t.Fatalf("DisplayAnalysisResults output mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestDisplayAnalysisResultsWithDiagnostics_UsesEffectiveConfigAndDiagnostics(t *testing.T) {
	m := makeFullAnalysisMeasurements()
	config := processor.DefaultEffectiveFilterConfig()
	config.DS201LowPass.Enabled = false
	config.Deesser.Intensity = 0.62
	config.DS201Gate.Threshold = processor.DbToLinear(-48.2)
	config.DS201Gate.Ratio = 3.5
	config.LA2A.Threshold = -26
	config.LA2A.Ratio = 4.2
	effective := config
	diagnostics := &processor.AdaptiveDiagnostics{
		DS201LPReason:               "rolloff/centroid gap",
		DS201GateClampReason:        "quiet speech ceiling",
		DS201GateThresholdUnclamped: -44.5,
		LA2AHighCrestActive:         true,
		LA2AHighCrestDeficit:        5.6,
		LA2AHighCrestSeverity:       0.78,
	}

	var buf bytes.Buffer
	DisplayAnalysisResultsWithDiagnostics(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, effective, diagnostics)
	output := buf.String()

	for _, want := range []string{
		"Lowpass:        disabled (rolloff/centroid gap)",
		"Gate Threshold: -48.2 dB (with breath reduction)",
		"Gate Ratio:     3.5:1",
		"Gate Clamp:     quiet speech ceiling (unclamped -44.5 dB)",
		"De-esser:       62% intensity",
		"LA-2A Thresh:   -26 dB",
		"LA-2A Ratio:    4.2:1",
		"LA-2A Crest:    high-crest override active (deficit 5.6 dB, severity 0.78)",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("analysis output missing %q", want)
		}
	}

	for _, stale := range []string{
		"stale config lowpass reason",
		"stale config gate clamp",
	} {
		if strings.Contains(output, stale) {
			t.Errorf("analysis output used stale config diagnostics %q", stale)
		}
	}
}

func TestDisplayAnalysisResults_VoiceActivated_NoSilence(t *testing.T) {
	m := makeMinimalMeasurements()
	m.VoiceActivated = true
	// No SilenceCandidates, no NoiseProfile

	config := processor.DefaultEffectiveFilterConfig()
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

	config := processor.DefaultEffectiveFilterConfig()
	var buf bytes.Buffer
	DisplayAnalysisResults(&buf, "/tmp/test.wav", makeMinimalMetadata(), m, config)
	output := buf.String()

	if strings.Contains(output, "Voice-activated") {
		t.Error("expected no 'Voice-activated' in output for normal recording")
	}
}

func TestDisplayAnalysisResults_CompanderDisabled(t *testing.T) {
	m := makeMinimalMeasurements()
	config := processor.DefaultEffectiveFilterConfig()
	config.NoiseRemove.CompandEnabled = false

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
	config := processor.DefaultEffectiveFilterConfig()
	config.NoiseRemove.CompandEnabled = true
	config.NoiseRemove.CompandThreshold = -55
	config.NoiseRemove.CompandExpansion = 6

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
	config := processor.DefaultEffectiveFilterConfig()
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
	config := processor.DefaultEffectiveFilterConfig()
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

	config := processor.DefaultEffectiveFilterConfig()
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

	config := processor.DefaultEffectiveFilterConfig()
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

	config := processor.DefaultEffectiveFilterConfig()
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

	config := processor.DefaultEffectiveFilterConfig()
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
