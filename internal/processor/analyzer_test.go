package processor

import (
	"testing"
)

func TestAnalyzeAudio(t *testing.T) {
	// Generate synthetic test audio: 5-second 440Hz tone at -23 LUFS with a 0.5s silence gap
	// This provides known characteristics for validating the analyzer
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 5.0,
		SampleRate:   44100,
		ToneFreq:     440.0, // A4 note
		ToneLevel:    -23.0, // Typical podcast raw level
		NoiseLevel:   -60.0, // Light background noise
		SilenceGap: struct {
			Start    float64
			Duration float64
		}{
			Start:    2.0, // Silence at 2 seconds
			Duration: 0.5, // 0.5 second silence gap
		},
	})
	defer cleanupTestAudio(t, testFile)

	// Use test config with podcast standard targets
	config := newTestConfig()
	config.AnalysisEnabled = true

	t.Run("synthetic_tone_with_silence", func(t *testing.T) {
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

		measurements, err := AnalyzeAudio(testFile, config, progressCallback)
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
		t.Logf("Peak Level: %.2f dB", measurements.PeakLevel)

		// Sanity checks for synthetic audio with known characteristics
		// Input level should be close to -23 LUFS (our tone level)
		if measurements.InputI > -20 || measurements.InputI < -30 {
			t.Errorf("InputI out of expected range for -23dBFS tone: %.2f", measurements.InputI)
		}

		// True peak should be close to tone level (sine wave peak = RMS + 3dB)
		if measurements.InputTP > 0 || measurements.InputTP < -30 {
			t.Errorf("InputTP out of reasonable range: %.2f", measurements.InputTP)
		}

		// LRA should be low for a steady tone with brief silence (< 10 LU)
		if measurements.InputLRA < 0 || measurements.InputLRA > 15 {
			t.Errorf("InputLRA out of expected range for steady tone: %.2f", measurements.InputLRA)
		}

		// NoiseFloor should detect the silence gap or low noise floor
		// With -60dB noise and 0.5s silence, floor should be well below -40dB
		if measurements.NoiseFloor > -35 || measurements.NoiseFloor < -120 {
			t.Errorf("NoiseFloor out of reasonable range: %.2f", measurements.NoiseFloor)
		}

		// The offset should bring us close to target (-16 LUFS)
		expectedOutput := measurements.InputI + measurements.TargetOffset
		if expectedOutput < config.TargetI-2 || expectedOutput > config.TargetI+2 {
			t.Logf("Warning: Target offset might not achieve target (expected ~%.1f, got %.2f)",
				config.TargetI, expectedOutput)
		}
	})
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
			result := calculateAdaptiveDS201GateThreshold(tt.noiseFloor, tt.rmsTrough)

			if result < tt.wantMin || result > tt.wantMax {
				t.Errorf("calculateAdaptiveDS201GateThreshold(%.1f, %.1f) = %.1f dB, want %.1f to %.1f dB [%s]",
					tt.noiseFloor, tt.rmsTrough, result, tt.wantMin, tt.wantMax, tt.desc)
			}
		})
	}
}
