package processor

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProcessAudio tests the complete two-pass processing pipeline
func TestProcessAudio(t *testing.T) {
	// Use a test file from testdata
	testFile := filepath.Join("..", "..", "testdata", "LMP-69-martin.flac")

	// Check if test file exists
	if _, err := os.Stat(testFile); os.IsNotExist(err) {
		t.Skipf("Test file not found: %s", testFile)
	}

	// Create isolated test config with minimal filters for integration test
	// This ensures the test doesn't break when application defaults change
	config := newTestConfig()
	config.DownmixEnabled = true
	config.AnalysisEnabled = true
	config.ResampleEnabled = true
	config.DS201HPEnabled = true // Basic processing
	config.LimiterEnabled = true // Safety limiter

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
	outputFile := filepath.Join("..", "..", "testdata", "LMP-69-martin-processed.flac")
	if _, err := os.Stat(outputFile); os.IsNotExist(err) {
		t.Fatalf("Output file not created: %s", outputFile)
	}

	// Clean up output file
	defer os.Remove(outputFile)

	t.Logf("Successfully processed audio file")
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
	config.LimiterEnabled = true

	filterSpec = config.BuildFilterSpec()
	t.Logf("Pass 2 filter spec: %s", filterSpec)

	// Should contain enabled filters
	if filterSpec == "" {
		t.Error("Filter spec is empty")
	}
}
