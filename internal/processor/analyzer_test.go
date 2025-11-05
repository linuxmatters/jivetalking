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
		t.Fatalf("Failed to read testdata directory: %v", err)
	}

	// Filter for audio files (.flac, .wav, .mp3)
	var audioFiles []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(entry.Name()))
		if ext == ".flac" || ext == ".wav" || ext == ".mp3" {
			audioFiles = append(audioFiles, filepath.Join(testdataDir, entry.Name()))
		}
	}

	if len(audioFiles) == 0 {
		t.Skip("No audio files found in testdata directory")
	}

	// Use podcast standard targets
	targetI := -16.0
	targetTP := -1.5
	targetLRA := 11.0

	// Analyze each audio file
	for _, filename := range audioFiles {
		t.Run(filepath.Base(filename), func(t *testing.T) {
			t.Logf("Analyzing: %s", filepath.Base(filename))

			measurements, err := AnalyzeAudio(filename, targetI, targetTP, targetLRA)
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

			// Sanity checks
			if measurements.InputI > 0 || measurements.InputI < -100 {
				t.Errorf("InputI out of reasonable range: %.2f", measurements.InputI)
			}

			if measurements.InputTP > 0 || measurements.InputTP < -100 {
				t.Errorf("InputTP out of reasonable range: %.2f", measurements.InputTP)
			}

			if measurements.InputLRA < 0 || measurements.InputLRA > 50 {
				t.Errorf("InputLRA out of reasonable range: %.2f", measurements.InputLRA)
			}

			if measurements.NoiseFloor > -20 || measurements.NoiseFloor < -80 {
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
