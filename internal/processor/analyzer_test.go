package processor

import (
	"testing"
	"time"
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

// ============================================================================
// Golden Sub-Region Refinement Tests
// ============================================================================

// makeTestIntervals creates synthetic interval samples for testing.
// Each interval is 250ms, RMS levels are specified per interval.
func makeTestIntervals(startTime time.Duration, rmsLevels []float64) []IntervalSample {
	intervals := make([]IntervalSample, len(rmsLevels))
	for i, rms := range rmsLevels {
		intervals[i] = IntervalSample{
			Timestamp: startTime + time.Duration(i)*250*time.Millisecond,
			RMSLevel:  rms,
		}
	}
	return intervals
}

func TestRefineToGoldenSubregion(t *testing.T) {
	tests := []struct {
		name          string
		candidate     *SilenceRegion
		intervals     []IntervalSample
		wantStart     time.Duration
		wantDuration  time.Duration
		wantUnchanged bool // If true, expect original region returned
		desc          string
	}{
		{
			name: "short candidate - no refinement needed",
			candidate: &SilenceRegion{
				Start:    24 * time.Second,
				End:      34 * time.Second,
				Duration: 10 * time.Second,
			},
			intervals:     makeTestIntervals(24*time.Second, make([]float64, 40)), // 40 intervals = 10s
			wantStart:     24 * time.Second,
			wantDuration:  10 * time.Second,
			wantUnchanged: true,
			desc:          "candidates at or below 10s should pass through unchanged",
		},
		{
			name: "long candidate with uniform quality",
			candidate: &SilenceRegion{
				Start:    24 * time.Second,
				End:      44 * time.Second,
				Duration: 20 * time.Second,
			},
			intervals: makeTestIntervals(24*time.Second, func() []float64 {
				// 80 intervals at -70 dBFS (uniform)
				levels := make([]float64, 80)
				for i := range levels {
					levels[i] = -70.0
				}
				return levels
			}()),
			wantStart:    24 * time.Second, // First window when all equal
			wantDuration: 10 * time.Second,
			desc:         "uniform quality should return first 10s window",
		},
		{
			name: "long candidate with golden pocket at end",
			candidate: &SilenceRegion{
				Start:    24 * time.Second,
				End:      44 * time.Second,
				Duration: 20 * time.Second,
			},
			intervals: makeTestIntervals(24*time.Second, func() []float64 {
				// 80 intervals: first 40 at -65 dBFS, last 40 at -75 dBFS
				levels := make([]float64, 80)
				for i := range levels {
					if i < 40 {
						levels[i] = -65.0 // Noisier first half
					} else {
						levels[i] = -75.0 // Quieter second half
					}
				}
				return levels
			}()),
			wantStart:    34 * time.Second, // Should find quieter region at 34s (10s into candidate)
			wantDuration: 10 * time.Second,
			desc:         "should find the quieter 10s window in the second half",
		},
		{
			name: "candidate at recording start",
			candidate: &SilenceRegion{
				Start:    0,
				End:      15 * time.Second,
				Duration: 15 * time.Second,
			},
			intervals: makeTestIntervals(0, func() []float64 {
				// 60 intervals: first 20 at -60 dBFS, rest at -72 dBFS
				levels := make([]float64, 60)
				for i := range levels {
					if i < 20 {
						levels[i] = -60.0 // Intro noise
					} else {
						levels[i] = -72.0 // Clean room tone
					}
				}
				return levels
			}()),
			wantStart:    5 * time.Second, // 20 intervals = 5s offset to quieter region
			wantDuration: 10 * time.Second,
			desc:         "should handle candidates starting at 0s correctly",
		},
		{
			name: "insufficient intervals - returns original",
			candidate: &SilenceRegion{
				Start:    24 * time.Second,
				End:      30 * time.Second,
				Duration: 6 * time.Second,
			},
			intervals:     makeTestIntervals(24*time.Second, make([]float64, 24)), // 24 intervals = 6s < 8s minimum
			wantStart:     24 * time.Second,
			wantDuration:  6 * time.Second,
			wantUnchanged: true,
			desc:          "candidates with insufficient intervals should pass through",
		},
		{
			name:          "nil candidate",
			candidate:     nil,
			intervals:     makeTestIntervals(0, make([]float64, 80)),
			wantUnchanged: true,
			desc:          "nil input should return nil",
		},
		{
			name: "no intervals in range",
			candidate: &SilenceRegion{
				Start:    100 * time.Second,
				End:      120 * time.Second,
				Duration: 20 * time.Second,
			},
			intervals:     makeTestIntervals(0, make([]float64, 80)), // Intervals at 0-20s, candidate at 100-120s
			wantStart:     100 * time.Second,
			wantDuration:  20 * time.Second,
			wantUnchanged: true,
			desc:          "should return original when no intervals match candidate range",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := refineToGoldenSubregion(tt.candidate, tt.intervals)

			if tt.candidate == nil {
				if result != nil {
					t.Errorf("expected nil result for nil candidate, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("unexpected nil result")
			}

			if tt.wantUnchanged {
				if result.Start != tt.candidate.Start || result.Duration != tt.candidate.Duration {
					t.Errorf("expected unchanged region, got Start=%v Duration=%v (original Start=%v Duration=%v)",
						result.Start, result.Duration, tt.candidate.Start, tt.candidate.Duration)
				}
				return
			}

			if result.Start != tt.wantStart {
				t.Errorf("Start = %v, want %v [%s]", result.Start, tt.wantStart, tt.desc)
			}
			if result.Duration != tt.wantDuration {
				t.Errorf("Duration = %v, want %v [%s]", result.Duration, tt.wantDuration, tt.desc)
			}
		})
	}
}

func TestGetIntervalsInRange(t *testing.T) {
	// Create intervals from 0s to 20s (80 intervals × 250ms)
	allIntervals := makeTestIntervals(0, make([]float64, 80))

	tests := []struct {
		name      string
		start     time.Duration
		end       time.Duration
		wantCount int
		wantFirst time.Duration
		wantLast  time.Duration
	}{
		{
			name:      "full range",
			start:     0,
			end:       20 * time.Second,
			wantCount: 80,
			wantFirst: 0,
			wantLast:  19750 * time.Millisecond,
		},
		{
			name:      "middle range",
			start:     5 * time.Second,
			end:       15 * time.Second,
			wantCount: 40,
			wantFirst: 5 * time.Second,
			wantLast:  14750 * time.Millisecond,
		},
		{
			name:      "no overlap - before",
			start:     25 * time.Second,
			end:       30 * time.Second,
			wantCount: 0,
		},
		{
			name:      "partial overlap at start",
			start:     0,
			end:       2 * time.Second,
			wantCount: 8,
			wantFirst: 0,
			wantLast:  1750 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getIntervalsInRange(allIntervals, tt.start, tt.end)

			if tt.wantCount == 0 {
				if result != nil {
					t.Errorf("expected nil for no overlap, got %d intervals", len(result))
				}
				return
			}

			if len(result) != tt.wantCount {
				t.Errorf("got %d intervals, want %d", len(result), tt.wantCount)
			}

			if len(result) > 0 {
				if result[0].Timestamp != tt.wantFirst {
					t.Errorf("first timestamp = %v, want %v", result[0].Timestamp, tt.wantFirst)
				}
				if result[len(result)-1].Timestamp != tt.wantLast {
					t.Errorf("last timestamp = %v, want %v", result[len(result)-1].Timestamp, tt.wantLast)
				}
			}
		})
	}
}

func TestScoreIntervalWindow(t *testing.T) {
	tests := []struct {
		name    string
		rmsVals []float64
		wantAvg float64
		epsilon float64
	}{
		{
			name:    "uniform values",
			rmsVals: []float64{-70, -70, -70, -70},
			wantAvg: -70.0,
			epsilon: 0.001,
		},
		{
			name:    "mixed values",
			rmsVals: []float64{-60, -70, -80, -70},
			wantAvg: -70.0, // Average of -60, -70, -80, -70
			epsilon: 0.001,
		},
		{
			name:    "single value",
			rmsVals: []float64{-65.5},
			wantAvg: -65.5,
			epsilon: 0.001,
		},
		{
			name:    "empty returns zero",
			rmsVals: []float64{},
			wantAvg: 0.0,
			epsilon: 0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intervals := makeTestIntervals(0, tt.rmsVals)
			result := scoreIntervalWindow(intervals)

			diff := result - tt.wantAvg
			if diff < 0 {
				diff = -diff
			}
			if diff > tt.epsilon {
				t.Errorf("scoreIntervalWindow() = %v, want %v (±%v)", result, tt.wantAvg, tt.epsilon)
			}
		})
	}
}
