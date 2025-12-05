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

	// Use test config with podcast standard targets
	config := newTestConfig()
	config.AnalysisEnabled = true

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

			measurements, err := AnalyzeAudio(filename, config, progressCallback)
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

			// True peak can exceed 0 dBFS due to inter-sample peaks in hot recordings
			// Allow up to +3 dBTP which is typical for unprocessed podcast audio
			if measurements.InputTP > 3 || measurements.InputTP < -100 {
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
			if expectedOutput < config.TargetI-2 || expectedOutput > config.TargetI+2 {
				t.Logf("Warning: Target offset might not achieve target (expected ~%.1f, got %.2f)",
					config.TargetI, expectedOutput)
			}
		})
	}
}

func TestCalculateAdaptiveGateThreshold(t *testing.T) {
	// Tests for the data-driven gate threshold calculation
	// The function uses noise floor and RMS trough (quiet speech) to calculate
	// an adaptive threshold positioned between noise and quiet speech.

	tests := []struct {
		name       string
		noiseFloor float64
		rmsTrough  float64
		wantMin    float64 // minimum acceptable threshold (dB)
		wantMax    float64 // maximum acceptable threshold (dB)
		desc       string
	}{
		// Normal cases with valid RMS trough measurements
		{
			name:       "clean recording, large gap",
			noiseFloor: -70,
			rmsTrough:  -40, // 30dB gap, uses 50% offset
			wantMin:    -56, // -70 + (30 * 0.5) = -55, allow tolerance
			wantMax:    -54,
			desc:       "clean recording with large headroom",
		},
		{
			name:       "typical podcast, moderate gap",
			noiseFloor: -55,
			rmsTrough:  -40, // 15dB gap, uses 40% offset
			wantMin:    -50, // -55 + (15 * 0.4) = -49, allow tolerance
			wantMax:    -48,
			desc:       "typical podcast recording",
		},
		{
			name:       "noisy recording, small gap",
			noiseFloor: -45,
			rmsTrough:  -40, // 5dB gap, uses 30% offset
			wantMin:    -44, // -45 + (5 * 0.3) = -43.5, allow tolerance
			wantMax:    -41, // But minimum offset is 3dB, so -42
			desc:       "noisy recording with limited headroom",
		},

		// Fallback cases - no RMS trough measurement
		{
			name:       "fallback: zero rmsTrough",
			noiseFloor: -55,
			rmsTrough:  0,   // No measurement
			wantMin:    -50, // -55 + 6 = -49 (fallback)
			wantMax:    -48,
			desc:       "fallback to 6dB offset",
		},
		{
			name:       "fallback: rmsTrough below noise floor",
			noiseFloor: -50,
			rmsTrough:  -60, // Invalid: below noise floor
			wantMin:    -45, // -50 + 6 = -44 (fallback)
			wantMax:    -43,
			desc:       "fallback when rmsTrough invalid",
		},

		// Safety bounds
		{
			name:       "clamped to max (-35dB)",
			noiseFloor: -30,
			rmsTrough:  -20, // Would give -25dB
			wantMin:    -36,
			wantMax:    -35, // Clamped to -35dB max
			desc:       "never gate above -35dB",
		},
		{
			name:       "minimum offset ensures above noise",
			noiseFloor: -55,
			rmsTrough:  -54, // Only 1dB gap, but minimum offset is 3dB
			wantMin:    -53, // -55 + 3 = -52
			wantMax:    -51,
			desc:       "minimum 3dB offset applied",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateAdaptiveGateThreshold(tt.noiseFloor, tt.rmsTrough)

			if result < tt.wantMin || result > tt.wantMax {
				t.Errorf("calculateAdaptiveGateThreshold(%.1f, %.1f) = %.1f dB, want %.1f to %.1f dB [%s]",
					tt.noiseFloor, tt.rmsTrough, result, tt.wantMin, tt.wantMax, tt.desc)
			}
		})
	}
}
