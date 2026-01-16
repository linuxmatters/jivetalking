package processor

import (
	"os"
	"testing"
)

// TestProcessAudio tests the complete three-pass processing pipeline
func TestProcessAudio(t *testing.T) {
	// Generate synthetic test audio: 3-second 440Hz tone at -18 LUFS
	// (needs to be loud enough for normalisation to be within ±12 dB of -16 LUFS)
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
	config.DS201HPEnabled = true  // Basic processing
	config.UREI1176Enabled = true // UREI 1176-style limiter

	// Process the audio with a no-op progress callback
	result, err := ProcessAudio(testFile, config, func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements) {
		// No-op for tests
	})
	if err != nil {
		t.Fatalf("ProcessAudio failed: %v", err)
	}

	// Verify we got a valid result
	if result == nil {
		t.Fatal("ProcessAudio returned nil result")
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

	// Verify measurements are populated
	if result.Measurements == nil {
		t.Error("ProcessAudio returned nil measurements")
	}

	// Verify filtered measurements are populated (Pass 2 output analysis)
	if result.FilteredMeasurements == nil {
		t.Error("ProcessAudio returned nil FilteredMeasurements")
	} else {
		// Verify silence sample measurements are captured (if NoiseProfile exists)
		if result.Measurements.NoiseProfile != nil {
			t.Logf("NoiseProfile exists: Start=%v, Duration=%v", result.Measurements.NoiseProfile.Start, result.Measurements.NoiseProfile.Duration)
			if result.FilteredMeasurements.SilenceSample == nil {
				t.Error("FilteredMeasurements.SilenceSample is nil despite NoiseProfile existing")
			} else {
				t.Logf("Silence sample RMS: %.2f dBFS", result.FilteredMeasurements.SilenceSample.RMSLevel)
				t.Logf("Silence sample spectral centroid: %.1f Hz", result.FilteredMeasurements.SilenceSample.SpectralCentroid)
			}
		} else {
			t.Logf("NoiseProfile is nil - skipping silence sample validation")
		}

		// Verify speech sample measurements are captured (if SpeechProfile exists)
		if result.Measurements.SpeechProfile != nil {
			t.Logf("SpeechProfile exists: Region=%v", result.Measurements.SpeechProfile.Region)
			if result.FilteredMeasurements.SpeechSample == nil {
				t.Error("FilteredMeasurements.SpeechSample is nil despite SpeechProfile existing")
			} else {
				t.Logf("Speech sample RMS: %.2f dBFS", result.FilteredMeasurements.SpeechSample.RMSLevel)
				t.Logf("Speech sample spectral centroid: %.1f Hz", result.FilteredMeasurements.SpeechSample.SpectralCentroid)
			}
		} else {
			t.Logf("SpeechProfile is nil - skipping speech sample validation")
		}
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
	config.UREI1176Enabled = true

	filterSpec = config.BuildFilterSpec()
	t.Logf("Pass 2 filter spec: %s", filterSpec)

	// Should contain enabled filters
	if filterSpec == "" {
		t.Error("Filter spec is empty")
	}
}
