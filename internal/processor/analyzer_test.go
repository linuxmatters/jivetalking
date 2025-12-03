package processor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnalyzeAudio(t *testing.T) {
	// Find all audio files in testdata directory
	testdataDir := "../../testdata"
	entries, err := os.ReadDir(testdataDir)
	if err != nil {
		t.Skipf("Skipping: testdata directory not available: %v", err)
	}

	// Filter for unprocessed audio files (.flac, .wav, .mp3)
	// Skip *-processed.* files as they're output files, not test inputs
	var audioFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		// Skip processed output files
		if strings.Contains(name, "-processed") {
			continue
		}
		ext := strings.ToLower(filepath.Ext(name))
		if ext == ".flac" || ext == ".wav" || ext == ".mp3" {
			audioFiles = append(audioFiles, filepath.Join(testdataDir, name))
		}
	}

	if len(audioFiles) == 0 {
		t.Skip("No audio files found in testdata directory")
	}

	// By default, only test the first file (faster CI/local iteration)
	// Set TEST_ALL_AUDIO=1 to test all files
	if os.Getenv("TEST_ALL_AUDIO") == "" && len(audioFiles) > 1 {
		t.Logf("Testing 1 of %d files (set TEST_ALL_AUDIO=1 to test all)", len(audioFiles))
		audioFiles = audioFiles[:1]
	}

	// Use podcast standard targets
	targetI := -16.0
	targetTP := -1.5
	targetLRA := 11.0

	// Analyze each audio file
	for i, filename := range audioFiles {
		t.Run(filepath.Base(filename), func(t *testing.T) {
			t.Logf("[%d/%d] Analyzing: %s", i+1, len(audioFiles), filepath.Base(filename))

			// Progress callback to show analysis progress
			lastPercent := -1
			progressCallback := func(pass int, passName string, progress float64, level float64, m *AudioMeasurements) {
				percent := int(progress * 100)
				// Only log at 25% intervals to avoid spam
				if percent >= lastPercent+25 {
					t.Logf("  %s: %d%%", passName, percent)
					lastPercent = percent
				}
			}

			measurements, err := AnalyzeAudio(filename, targetI, targetTP, targetLRA, progressCallback)
			if err != nil {
				t.Fatalf("AnalyzeAudio failed: %v", err)
			}

			if measurements == nil {
				t.Fatal("measurements is nil")
			}

			// Log measurements
			t.Logf("Input Loudness: %.2f LUFS", measurements.InputI)
			t.Logf("Input True Peak: %.2f dBTP", measurements.InputTP)
			t.Logf("Input Loudness Range: %.2f LU", measurements.InputLRA)
			t.Logf("Input Threshold: %.2f dB", measurements.InputThresh)
			t.Logf("Target Offset: %.2f dB", measurements.TargetOffset)
			t.Logf("Noise Floor: %.2f dB", measurements.NoiseFloor)
			t.Logf("Dynamic Range: %.2f dB", measurements.DynamicRange)
			t.Logf("RMS Level: %.2f dB", measurements.RMSLevel)
			t.Logf("Peak Level: %.2f dB", measurements.PeakLevel) // Sanity checks
			if measurements.InputI > 0 || measurements.InputI < -100 {
				t.Errorf("InputI out of reasonable range: %.2f", measurements.InputI)
			}

			if measurements.InputTP > 0 || measurements.InputTP < -100 {
				t.Errorf("InputTP out of reasonable range: %.2f", measurements.InputTP)
			}

			if measurements.InputLRA < 0 || measurements.InputLRA > 50 {
				t.Errorf("InputLRA out of reasonable range: %.2f", measurements.InputLRA)
			}

			// NoiseFloor is derived from RMS_trough - can be very low for clean audio
			// -20dB would indicate extremely noisy recording
			// -120dB is silence floor (below audible range)
			if measurements.NoiseFloor > -20 || measurements.NoiseFloor < -120 {
				t.Errorf("NoiseFloor out of reasonable range: %.2f", measurements.NoiseFloor)
			}

			// The offset should bring us close to target
			expectedOutput := measurements.InputI + measurements.TargetOffset
			if expectedOutput < targetI-2 || expectedOutput > targetI+2 {
				t.Logf("Warning: Target offset might not achieve target (expected ~%.1f, got %.2f)",
					targetI, expectedOutput)
			}
		})
	}
}
