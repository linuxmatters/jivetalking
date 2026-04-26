package processor

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/linuxmatters/jivetalking/internal/audio"
)

// TestGenerateOutputPath verifies the intermediate output path is always FLAC.
func TestGenerateOutputPath(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"lowercase wav", "/tmp/foo.wav", "/tmp/foo-processed.flac"},
		{"uppercase WAV", "/tmp/foo.WAV", "/tmp/foo-processed.flac"},
		{"flac input", "/tmp/foo.flac", "/tmp/foo-processed.flac"},
		{"mp3 input", "/tmp/foo.mp3", "/tmp/foo-processed.flac"},
		{"no extension", "/tmp/foo", "/tmp/foo-processed.flac"},
		{"multi-dot", "/tmp/foo.bar.wav", "/tmp/foo.bar-processed.flac"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := generateOutputPath(tc.input)
			if got != tc.want {
				t.Errorf("generateOutputPath(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestGenerateLUFSOutputPath verifies the final LUFS-tagged output path is always FLAC.
func TestGenerateLUFSOutputPath(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"lowercase wav", "/tmp/foo.wav", "/tmp/foo-LUFS-16-processed.flac"},
		{"uppercase WAV", "/tmp/foo.WAV", "/tmp/foo-LUFS-16-processed.flac"},
		{"flac input", "/tmp/foo.flac", "/tmp/foo-LUFS-16-processed.flac"},
		{"mp3 input", "/tmp/foo.mp3", "/tmp/foo-LUFS-16-processed.flac"},
		{"no extension", "/tmp/foo", "/tmp/foo-LUFS-16-processed.flac"},
		{"multi-dot", "/tmp/foo.bar.wav", "/tmp/foo.bar-LUFS-16-processed.flac"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := generateLUFSOutputPath(tc.input, 16)
			if got != tc.want {
				t.Errorf("generateLUFSOutputPath(%q, 16) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestProcessAudio tests the complete three-pass processing pipeline
func TestProcessAudio(t *testing.T) {
	// Generate synthetic test audio: 3-second 440Hz tone at -18 dBFS input level
	// (needs to be loud enough for normalisation to be within ±12 dB of -16 LUFS target)
	// Short duration for fast test execution
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 3.0,
		SampleRate:   44100,
		ToneFreq:     440.0, // A4 note
		ToneLevel:    -18.0, // Near broadcast level (-16 LUFS target)
		NoiseLevel:   -55.0, // Moderate background noise
		SilenceGap: struct {
			Start    float64
			Duration float64
		}{
			Start:    1.0, // Brief silence at 1 second
			Duration: 0.3, // 300ms silence gap for noise profiling
		},
	})
	defer cleanupTestAudio(t, testFile)

	// Create isolated test config with minimal filters for integration test
	// This ensures the test doesn't break when application defaults change
	config := newTestConfig()
	config.DownmixEnabled = true
	config.AnalysisEnabled = true
	config.ResampleEnabled = true
	config.DS201HPEnabled = true // Basic processing
	config.VolumaxEnabled = true // CBS Volumax-style limiter

	// Process the audio with a no-op progress callback
	result, err := ProcessAudio(testFile, config, func(pass PassNumber, passName string, progress float64, level float64, measurements *AudioMeasurements) {
		// No-op for tests
	})
	if err != nil {
		t.Fatalf("ProcessAudio failed: %v", err)
	}

	// Verify output file was created
	if result.OutputPath == "" {
		t.Fatal("ProcessAudio returned empty output path")
	}

	if _, err := os.Stat(result.OutputPath); os.IsNotExist(err) {
		t.Fatalf("Output file not created: %s", result.OutputPath)
	}

	// Clean up output file (cleanupTestAudio handles this but be explicit)
	defer os.Remove(result.OutputPath)

	// Verify the output extension is .flac regardless of input extension
	if ext := filepath.Ext(result.OutputPath); ext != ".flac" {
		t.Errorf("Output extension = %q, want %q (path: %s)", ext, ".flac", result.OutputPath)
	}

	// Verify the output container is the FLAC demuxer (not WAV with FLAC payload)
	reader, _, err := audio.OpenAudioFile(result.OutputPath)
	if err != nil {
		t.Fatalf("Failed to reopen output file: %v", err)
	}
	defer reader.Close()
	if got := reader.FormatName(); got != "flac" {
		t.Errorf("Output FormatName = %q, want %q", got, "flac")
	}

	// Verify the file starts with the FLAC magic bytes ("fLaC")
	header, err := os.ReadFile(result.OutputPath)
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}
	if len(header) < 4 || !bytes.Equal(header[:4], []byte{0x66, 0x4C, 0x61, 0x43}) {
		t.Errorf("Output magic bytes = %x, want fLaC (66 4C 61 43)", header[:min(4, len(header))])
	}

	// Verify measurements are populated
	if result.Measurements == nil {
		t.Error("ProcessAudio returned nil measurements")
	}

	// Verify filtered measurements are populated (Pass 2 output analysis)
	if result.FilteredMeasurements == nil {
		t.Error("ProcessAudio returned nil FilteredMeasurements")
	} else {
		// Verify silence sample measurements are captured in Pass 2 output (if NoiseProfile exists)
		if result.Measurements != nil && result.Measurements.NoiseProfile != nil {
			t.Logf("NoiseProfile exists: Start=%v, Duration=%v", result.Measurements.NoiseProfile.Start, result.Measurements.NoiseProfile.Duration)
			if result.FilteredMeasurements.SilenceSample == nil {
				t.Error("FilteredMeasurements.SilenceSample is nil despite NoiseProfile existing")
			} else {
				t.Logf("Pass 2 silence sample RMS: %.2f dBFS", result.FilteredMeasurements.SilenceSample.RMSLevel)
				t.Logf("Pass 2 silence sample spectral centroid: %.1f Hz", result.FilteredMeasurements.SilenceSample.Spectral.Centroid)
			}
		} else {
			t.Logf("NoiseProfile is nil - skipping silence sample validation")
		}

		// Verify speech sample measurements are captured in Pass 2 output (if SpeechProfile exists)
		if result.Measurements != nil && result.Measurements.SpeechProfile != nil {
			t.Logf("SpeechProfile exists: Region=%v", result.Measurements.SpeechProfile.Region)
			if result.FilteredMeasurements.SpeechSample == nil {
				t.Error("FilteredMeasurements.SpeechSample is nil despite SpeechProfile existing")
			} else {
				t.Logf("Pass 2 speech sample RMS: %.2f dBFS", result.FilteredMeasurements.SpeechSample.RMSLevel)
				t.Logf("Pass 2 speech sample spectral centroid: %.1f Hz", result.FilteredMeasurements.SpeechSample.Spectral.Centroid)
			}
		} else {
			t.Logf("SpeechProfile is nil - skipping speech sample validation")
		}
	}

	// Verify final measurements are populated (Pass 4 output analysis after normalisation)
	if result.NormResult != nil && result.NormResult.FinalMeasurements != nil {
		// Verify silence sample measurements are captured in Pass 4 output (if NoiseProfile exists)
		if result.Measurements != nil && result.Measurements.NoiseProfile != nil {
			if result.NormResult.FinalMeasurements.SilenceSample == nil {
				t.Error("FinalMeasurements.SilenceSample is nil despite NoiseProfile existing")
			} else {
				t.Logf("Pass 4 silence sample RMS: %.2f dBFS", result.NormResult.FinalMeasurements.SilenceSample.RMSLevel)
				t.Logf("Pass 4 silence sample spectral centroid: %.1f Hz", result.NormResult.FinalMeasurements.SilenceSample.Spectral.Centroid)
			}
		}

		// Verify speech sample measurements are captured in Pass 4 output (if SpeechProfile exists)
		if result.Measurements != nil && result.Measurements.SpeechProfile != nil {
			if result.NormResult.FinalMeasurements.SpeechSample == nil {
				t.Error("FinalMeasurements.SpeechSample is nil despite SpeechProfile existing")
			} else {
				t.Logf("Pass 4 speech sample RMS: %.2f dBFS", result.NormResult.FinalMeasurements.SpeechSample.RMSLevel)
				t.Logf("Pass 4 speech sample spectral centroid: %.1f Hz", result.NormResult.FinalMeasurements.SpeechSample.Spectral.Centroid)
			}
		}
	} else {
		t.Logf("NormResult or FinalMeasurements is nil - skipping Pass 4 validation")
	}

	// Log results
	t.Logf("Input LUFS: %.2f", result.InputLUFS)
	t.Logf("Output LUFS: %.2f", result.OutputLUFS)
	t.Logf("Noise Floor: %.2f", result.NoiseFloor)
	t.Logf("Output: %s", result.OutputPath)
}

// TestFilterChainBuilder tests the filter specification generation
func TestFilterChainBuilder(t *testing.T) {
	// Use isolated test config to avoid coupling to application defaults
	config := newTestConfig()
	config.DownmixEnabled = true
	config.AnalysisEnabled = true
	config.ResampleEnabled = true

	// Test Pass 1 (analysis) filter spec
	filterSpec := config.BuildFilterSpec()
	t.Logf("Pass 1 filter spec: %s", filterSpec)

	// Should contain filter chain
	if filterSpec == "" {
		t.Error("Filter spec is empty")
	}

	// Test Pass 2 (processing) filter spec with measurements
	config.Measurements = &AudioMeasurements{
		InputI:       -23.4,
		InputTP:      -3.2,
		InputLRA:     8.7,
		InputThresh:  -45.0,
		TargetOffset: 0.5,
		NoiseFloor:   -60.0,
	}

	// Enable additional filters for Pass 2 test
	config.DS201HPEnabled = true
	config.VolumaxEnabled = true

	filterSpec = config.BuildFilterSpec()
	t.Logf("Pass 2 filter spec: %s", filterSpec)

	// Should contain enabled filters
	if filterSpec == "" {
		t.Error("Filter spec is empty")
	}
}
