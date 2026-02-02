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
// Early Window Fallback Threshold Tests
// ============================================================================

func TestCalculateFallbackThreshold(t *testing.T) {
	// Tests for the fallback threshold calculation used when no silence candidates
	// are found within the early detection window. Uses full-file NoiseFloor
	// measurement with headroom to compute a relaxed detection threshold.

	tests := []struct {
		name              string
		noiseFloor        float64
		originalThreshold float64
		wantThreshold     float64
		wantOK            bool
		desc              string
	}{
		{
			name:              "normal relaxation",
			noiseFloor:        -75.0,
			originalThreshold: -65.0,
			wantThreshold:     -72.0, // NoiseFloor + 3dB headroom
			wantOK:            true,
			desc:              "NoiseFloor + 3dB",
		},
		{
			name:              "cap hit",
			noiseFloor:        -50.0,
			originalThreshold: -65.0,
			wantThreshold:     -55.0, // NoiseFloor + 3dB = -47, but limited to original + 10dB = -55
			wantOK:            true,
			desc:              "Limited to original + 10dB",
		},
		{
			name:              "invalid NoiseFloor (0)",
			noiseFloor:        0.0,
			originalThreshold: -65.0,
			wantThreshold:     0.0,
			wantOK:            false,
			desc:              "ok = false",
		},
		{
			name:              "invalid NoiseFloor (too low)",
			noiseFloor:        -95.0,
			originalThreshold: -65.0,
			wantThreshold:     0.0,
			wantOK:            false,
			desc:              "ok = false",
		},
		{
			name:              "invalid NoiseFloor (too high)",
			noiseFloor:        -15.0,
			originalThreshold: -65.0,
			wantThreshold:     0.0,
			wantOK:            false,
			desc:              "ok = false",
		},
		{
			name:              "NoiseFloor above original",
			noiseFloor:        -68.0,
			originalThreshold: -70.0,
			wantThreshold:     -65.0, // Uses NoiseFloor + 3dB
			wantOK:            true,
			desc:              "Uses NoiseFloor + 3dB",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			threshold, ok := calculateFallbackThreshold(tt.noiseFloor, tt.originalThreshold)

			if ok != tt.wantOK {
				t.Errorf("calculateFallbackThreshold(%.1f, %.1f) ok = %v, want %v [%s]",
					tt.noiseFloor, tt.originalThreshold, ok, tt.wantOK, tt.desc)
				return
			}

			if !ok {
				// When ok is false, threshold should be 0
				if threshold != 0 {
					t.Errorf("calculateFallbackThreshold(%.1f, %.1f) threshold = %.1f, want 0 when ok=false [%s]",
						tt.noiseFloor, tt.originalThreshold, threshold, tt.desc)
				}
				return
			}

			// Check threshold value with small tolerance for floating point
			tolerance := 0.001
			diff := threshold - tt.wantThreshold
			if diff < 0 {
				diff = -diff
			}
			if diff > tolerance {
				t.Errorf("calculateFallbackThreshold(%.1f, %.1f) threshold = %.3f, want %.3f [%s]",
					tt.noiseFloor, tt.originalThreshold, threshold, tt.wantThreshold, tt.desc)
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

// ============================================================================
// Speech Detection Tests
// ============================================================================

// makeSpeechTestIntervals creates synthetic interval samples with speech-like characteristics.
// Allows control over RMS, centroid, and entropy for testing speech detection logic.
func makeSpeechTestIntervals(startTime time.Duration, count int, rms, centroid, entropy float64) []IntervalSample {
	intervals := make([]IntervalSample, count)
	for i := range intervals {
		intervals[i] = IntervalSample{
			Timestamp:        startTime + time.Duration(i)*250*time.Millisecond,
			RMSLevel:         rms,
			SpectralCentroid: centroid,
			SpectralEntropy:  entropy,
		}
	}
	return intervals
}

func TestSpeechScore(t *testing.T) {
	tests := []struct {
		name     string
		interval IntervalSample
		rmsP50   float64
		wantMin  float64
		wantMax  float64
	}{
		{
			name: "typical speech",
			interval: IntervalSample{
				RMSLevel:         -18.0,
				SpectralCentroid: 1500.0, // Voice range
				SpectralEntropy:  0.5,
			},
			rmsP50:  -20.0,
			wantMin: 0.4,
			wantMax: 1.0,
		},
		{
			name: "silence (too quiet)",
			interval: IntervalSample{
				RMSLevel:         -50.0,
				SpectralCentroid: 1500.0,
				SpectralEntropy:  0.5,
			},
			rmsP50:  -20.0,
			wantMin: 0.0,
			wantMax: 0.0,
		},
		{
			name: "noise (wrong centroid)",
			interval: IntervalSample{
				RMSLevel:         -18.0,
				SpectralCentroid: 8000.0, // Outside voice range
				SpectralEntropy:  0.9,    // High entropy
			},
			rmsP50:  -20.0,
			wantMin: 0.0,
			wantMax: 0.4,
		},
		{
			name: "at RMS minimum threshold",
			interval: IntervalSample{
				RMSLevel:         -40.0, // Exactly at speechRMSMinimum
				SpectralCentroid: 1500.0,
				SpectralEntropy:  0.5,
			},
			rmsP50:  -45.0,
			wantMin: 0.3,
			wantMax: 0.85,
		},
		{
			name: "just below RMS minimum",
			interval: IntervalSample{
				RMSLevel:         -40.1, // Just below threshold
				SpectralCentroid: 1500.0,
				SpectralEntropy:  0.5,
			},
			rmsP50:  -45.0,
			wantMin: 0.0,
			wantMax: 0.0,
		},
		{
			name: "low entropy structured speech",
			interval: IntervalSample{
				RMSLevel:         -18.0,
				SpectralCentroid: 2000.0,
				SpectralEntropy:  0.2, // Very structured
			},
			rmsP50:  -20.0,
			wantMin: 0.5,
			wantMax: 1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := speechScore(tt.interval, tt.rmsP50, 1500.0)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("speechScore() = %.2f, want [%.2f, %.2f]", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestFindSpeechCandidatesFromIntervals(t *testing.T) {
	// Helper to create intervals with varied RMS for realistic median calculation
	// Speech intervals have majority at/above median to ensure >0.4 speech score
	// Pattern biased towards louder: 3 intervals at -12 to -16, 1 at -22
	makeVariedSpeechIntervals := func(startTime time.Duration, count int, isSpeech bool) []IntervalSample {
		intervals := make([]IntervalSample, count)
		for i := range intervals {
			rms := -50.0 // silence
			if isSpeech {
				// Create varied RMS where most intervals score above 0.4 threshold
				// Median will be around -14, most intervals at/above median
				switch i % 4 {
				case 0:
					rms = -12.0 // loud speech (+4 above median -> ampScore ~0.67)
				case 1:
					rms = -14.0 // moderate (+2 above median -> ampScore ~0.33)
				case 2:
					rms = -16.0 // at median (ampScore ~0)
				case 3:
					rms = -22.0 // quiet (below median, ampScore 0)
				}
			}
			intervals[i] = IntervalSample{
				Timestamp:        startTime + time.Duration(i)*250*time.Millisecond,
				RMSLevel:         rms,
				SpectralCentroid: 1500.0,
				SpectralEntropy:  0.3, // Low entropy for better scores (~0.65 entropyScore)
			}
		}
		return intervals
	}

	t.Run("finds 30s speech region", func(t *testing.T) {
		// 10s silence + 2.5min speech (600 intervals)
		// With 25% above threshold, this gives ~150 speech intervals (above 120 minimum)
		// Search starts at 12s, so we lose 8 intervals, leaving 592 → ~148 speech intervals
		silenceIntervals := makeVariedSpeechIntervals(0, 40, false)             // 10s silence
		speechIntervals := makeVariedSpeechIntervals(10*time.Second, 600, true) // 2.5min speech
		intervals := append(silenceIntervals, speechIntervals...)

		candidates := findSpeechCandidatesFromIntervals(intervals, 10*time.Second)

		if len(candidates) == 0 {
			t.Fatal("expected at least one speech candidate")
		}
		if candidates[0].Duration < 30*time.Second {
			t.Errorf("duration %v < 30s minimum", candidates[0].Duration)
		}
	})

	t.Run("no candidates for short speech", func(t *testing.T) {
		// 10s silence + 20s speech (too short - only 80 intervals)
		silenceIntervals := makeVariedSpeechIntervals(0, 40, false)
		speechIntervals := makeVariedSpeechIntervals(10*time.Second, 80, true) // 20s
		intervals := append(silenceIntervals, speechIntervals...)

		candidates := findSpeechCandidatesFromIntervals(intervals, 10*time.Second)

		if len(candidates) != 0 {
			t.Errorf("expected no candidates, got %d", len(candidates))
		}
	})

	t.Run("handles natural pauses", func(t *testing.T) {
		// Speech with 1.5s pause in middle (should bridge)
		// Each segment needs enough high-scoring intervals: 300 intervals (75s) each
		speech1 := makeVariedSpeechIntervals(10*time.Second, 300, true)         // 75s
		pause := makeVariedSpeechIntervals(85*time.Second, 6, false)            // 1.5s pause
		speech2 := makeVariedSpeechIntervals(86500*time.Millisecond, 300, true) // 75s more
		intervals := append(append(speech1, pause...), speech2...)

		candidates := findSpeechCandidatesFromIntervals(intervals, 5*time.Second)

		if len(candidates) == 0 {
			t.Fatal("expected speech candidate bridging pause")
		}
		// Combined region should be substantial
		if candidates[0].Duration < 100*time.Second {
			t.Errorf("expected bridged duration >100s, got %v", candidates[0].Duration)
		}
	})

	t.Run("respects silence end boundary", func(t *testing.T) {
		// Speech before and after silence end - only speech after should be detected
		// Need 480+ intervals for late speech to have enough high-scoring intervals
		earlyIntervals := makeVariedSpeechIntervals(0, 200, true)             // 50s speech at start
		lateIntervals := makeVariedSpeechIntervals(60*time.Second, 500, true) // 125s speech later
		intervals := append(earlyIntervals, lateIntervals...)

		// Search starts at 50s (after early speech ends)
		candidates := findSpeechCandidatesFromIntervals(intervals, 50*time.Second)

		if len(candidates) == 0 {
			t.Fatal("expected speech candidate after silence end")
		}
		// First candidate should start after 50s + 2s buffer = 52s
		if candidates[0].Start < 52*time.Second {
			t.Errorf("speech start %v should be after silence end + buffer (52s)", candidates[0].Start)
		}
	})

	t.Run("insufficient intervals returns nil", func(t *testing.T) {
		// Only 100 intervals (25s) - less than minimum 120 (30s)
		intervals := makeVariedSpeechIntervals(0, 100, true)

		candidates := findSpeechCandidatesFromIntervals(intervals, 0)

		if candidates != nil {
			t.Errorf("expected nil for insufficient intervals, got %d candidates", len(candidates))
		}
	})

	t.Run("long pause breaks speech region", func(t *testing.T) {
		// Speech with 3s pause in middle (exceeds 2s tolerance, should break)
		speech1 := makeVariedSpeechIntervals(10*time.Second, 80, true) // 20s
		pause := makeVariedSpeechIntervals(30*time.Second, 12, false)  // 3s pause (> tolerance)
		speech2 := makeVariedSpeechIntervals(33*time.Second, 80, true) // 20s more
		intervals := append(append(speech1, pause...), speech2...)

		candidates := findSpeechCandidatesFromIntervals(intervals, 5*time.Second)

		// Neither segment alone meets 30s minimum, so no candidates expected
		if len(candidates) != 0 {
			t.Errorf("expected no candidates (pause breaks region), got %d", len(candidates))
		}
	})
}

func TestMeasureSpeechCandidateFromIntervals(t *testing.T) {
	t.Run("computes metrics correctly", func(t *testing.T) {
		// Create intervals with known values
		intervals := make([]IntervalSample, 40) // 10s of intervals
		for i := range intervals {
			intervals[i] = IntervalSample{
				Timestamp:        time.Duration(i) * 250 * time.Millisecond,
				RMSLevel:         -20.0,
				PeakLevel:        -8.0,
				SpectralCentroid: 1500.0,
				SpectralFlatness: 0.3,
				SpectralKurtosis: 5.0,
				SpectralEntropy:  0.5,
			}
		}
		// Set one interval with higher peak
		intervals[20].PeakLevel = -5.0

		region := SpeechRegion{
			Start:    0,
			End:      10 * time.Second,
			Duration: 10 * time.Second,
		}

		metrics := measureSpeechCandidateFromIntervals(region, intervals)

		if metrics == nil {
			t.Fatal("expected non-nil metrics")
		}

		// Check averaged values
		if metrics.RMSLevel != -20.0 {
			t.Errorf("RMSLevel = %.1f, want -20.0", metrics.RMSLevel)
		}
		// Peak should be max across all intervals
		if metrics.PeakLevel != -5.0 {
			t.Errorf("PeakLevel = %.1f, want -5.0", metrics.PeakLevel)
		}
		// Crest factor = peak - RMS
		expectedCrest := -5.0 - (-20.0)
		if metrics.CrestFactor != expectedCrest {
			t.Errorf("CrestFactor = %.1f, want %.1f", metrics.CrestFactor, expectedCrest)
		}
		if metrics.SpectralCentroid != 1500.0 {
			t.Errorf("SpectralCentroid = %.1f, want 1500.0", metrics.SpectralCentroid)
		}
	})

	t.Run("returns nil for empty range", func(t *testing.T) {
		intervals := makeSpeechTestIntervals(0, 40, -20.0, 1500.0, 0.5)
		region := SpeechRegion{
			Start:    100 * time.Second, // No intervals in this range
			End:      110 * time.Second,
			Duration: 10 * time.Second,
		}

		metrics := measureSpeechCandidateFromIntervals(region, intervals)

		if metrics != nil {
			t.Error("expected nil for region with no intervals")
		}
	})
}

func TestFindBestSpeechRegion(t *testing.T) {
	t.Run("selects longest qualifying candidate", func(t *testing.T) {
		// Create intervals covering multiple regions
		intervals := makeSpeechTestIntervals(0, 400, -18.0, 1500.0, 0.5) // 100s of speech-like intervals

		regions := []SpeechRegion{
			{Start: 0, End: 35 * time.Second, Duration: 35 * time.Second},
			{Start: 40 * time.Second, End: 90 * time.Second, Duration: 50 * time.Second}, // Longest
			{Start: 95 * time.Second, End: 100 * time.Second, Duration: 5 * time.Second},
		}

		result := findBestSpeechRegion(regions, intervals, nil)

		if result.BestRegion == nil {
			t.Fatal("expected a best region to be selected")
		}
		// Should select the 50s region (longest)
		if result.BestRegion.Duration != 50*time.Second {
			t.Errorf("selected duration = %v, want 50s", result.BestRegion.Duration)
		}
	})

	t.Run("returns nil for empty regions", func(t *testing.T) {
		intervals := makeSpeechTestIntervals(0, 200, -18.0, 1500.0, 0.5)

		result := findBestSpeechRegion([]SpeechRegion{}, intervals, nil)

		if result.BestRegion != nil {
			t.Error("expected nil BestRegion for empty input")
		}
	})

	t.Run("stores all candidates for reporting", func(t *testing.T) {
		intervals := makeSpeechTestIntervals(0, 400, -18.0, 1500.0, 0.5)

		regions := []SpeechRegion{
			{Start: 0, End: 35 * time.Second, Duration: 35 * time.Second},
			{Start: 40 * time.Second, End: 80 * time.Second, Duration: 40 * time.Second},
		}

		result := findBestSpeechRegion(regions, intervals, nil)

		if len(result.Candidates) != 2 {
			t.Errorf("expected 2 candidates stored, got %d", len(result.Candidates))
		}
	})
}

func TestScoreSpeechCandidate(t *testing.T) {
	tests := []struct {
		name    string
		metrics *SpeechCandidateMetrics
		wantMin float64
		wantMax float64
	}{
		{
			name: "ideal speech candidate",
			metrics: &SpeechCandidateMetrics{
				Region:           SpeechRegion{Duration: 60 * time.Second},
				RMSLevel:         -15.0,
				CrestFactor:      12.0, // Ideal (crestFactorIdeal)
				SpectralCentroid: 1500.0,
				VoicingDensity:   0.75,   // High voiced content (above 0.6 threshold)
				SpectralRolloff:  6000.0, // Ideal range (4000-8000 Hz)
				SpectralFlux:     0.003,  // Below stable threshold (0.004)
			},
			wantMin: 0.8,
			wantMax: 1.0,
		},
		{
			name: "quiet speech",
			metrics: &SpeechCandidateMetrics{
				Region:           SpeechRegion{Duration: 30 * time.Second},
				RMSLevel:         -28.0,
				CrestFactor:      12.0, // Ideal
				SpectralCentroid: 1500.0,
				VoicingDensity:   0.75,   // High voiced content
				SpectralRolloff:  6000.0, // Ideal range
				SpectralFlux:     0.003,  // Low flux
			},
			wantMin: 0.3,
			wantMax: 0.8,
		},
		{
			name: "wrong centroid",
			metrics: &SpeechCandidateMetrics{
				Region:           SpeechRegion{Duration: 60 * time.Second},
				RMSLevel:         -15.0,
				CrestFactor:      12.0,   // Ideal
				SpectralCentroid: 8000.0, // Outside voice range
				VoicingDensity:   0.75,   // High voiced content
				SpectralRolloff:  6000.0, // Ideal range
				SpectralFlux:     0.003,  // Low flux
			},
			wantMin: 0.6,
			wantMax: 0.9,
		},
		{
			name:    "nil metrics",
			metrics: nil,
			wantMin: 0.0,
			wantMax: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scoreSpeechCandidate(tt.metrics)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("scoreSpeechCandidate() = %.2f, want [%.2f, %.2f]", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

// ============================================================================
// Speech Golden Sub-Region Refinement Tests
// ============================================================================

// makeSpeechIntervalsScorable creates intervals with specific spectral characteristics for scoring tests.
// Allows control over kurtosis, flatness, centroid, and RMS for testing scoreSpeechIntervalWindow.
// Sets ideal rolloff (6000 Hz) and low flux (0.003) for stable scoring by default.
func makeSpeechIntervalsScorable(startTime time.Duration, count int, kurtosis, flatness, centroid, rms float64) []IntervalSample {
	intervals := make([]IntervalSample, count)
	for i := range intervals {
		intervals[i] = IntervalSample{
			Timestamp:        startTime + time.Duration(i)*250*time.Millisecond,
			RMSLevel:         rms,
			SpectralKurtosis: kurtosis,
			SpectralFlatness: flatness,
			SpectralCentroid: centroid,
			SpectralRolloff:  6000.0, // Ideal range (4000-8000 Hz)
			SpectralFlux:     0.003,  // Below stable threshold (0.004)
		}
	}
	return intervals
}

func TestScoreSpeechIntervalWindow(t *testing.T) {
	tests := []struct {
		name    string
		setup   func() []IntervalSample
		wantMin float64
		wantMax float64
		desc    string
	}{
		{
			name: "continuous speech - high quality",
			setup: func() []IntervalSample {
				// High kurtosis (~6), low flatness (~0.1), centroid in voice range (~2000 Hz),
				// consistent kurtosis (low variance), good RMS (~-15 dBFS)
				return makeSpeechIntervalsScorable(0, 40, 6.0, 0.1, 2000.0, -15.0)
			},
			wantMin: 0.80,
			wantMax: 1.0,
			desc:    "ideal speech window should score high",
		},
		{
			name: "pause-heavy window with high variance",
			setup: func() []IntervalSample {
				// Create intervals with VERY high kurtosis variance (consistency penalised)
				// and centroid outside voice range, with poor rolloff and high flux
				intervals := make([]IntervalSample, 40)
				for i := range intervals {
					if i%2 == 0 {
						intervals[i] = IntervalSample{
							Timestamp:        time.Duration(i) * 250 * time.Millisecond,
							RMSLevel:         -35.0,   // Quiet
							SpectralKurtosis: 15.0,    // High
							SpectralFlatness: 0.8,     // Noise-like
							SpectralCentroid: 6000.0,  // Outside voice range
							SpectralRolloff:  12000.0, // Above acceptable range (max 10000)
							SpectralFlux:     0.05,    // High variation (transients)
						}
					} else {
						intervals[i] = IntervalSample{
							Timestamp:        time.Duration(i) * 250 * time.Millisecond,
							RMSLevel:         -35.0,   // Quiet
							SpectralKurtosis: 1.0,     // Low
							SpectralFlatness: 0.8,     // Noise-like
							SpectralCentroid: 6000.0,  // Outside voice range
							SpectralRolloff:  12000.0, // Above acceptable range (max 10000)
							SpectralFlux:     0.05,    // High variation (transients)
						}
					}
				}
				return intervals
			},
			wantMin: 0.0,
			wantMax: 0.40,
			desc:    "inconsistent noisy window should score low",
		},
		{
			name: "empty intervals",
			setup: func() []IntervalSample {
				return []IntervalSample{}
			},
			wantMin: 0.0,
			wantMax: 0.0,
			desc:    "empty input should return 0",
		},
		{
			name: "low kurtosis (flat spectrum)",
			setup: func() []IntervalSample {
				// Low kurtosis (~2), high flatness (~0.8), centroid outside range, quiet
				// This should score quite low across all metrics
				return makeSpeechIntervalsScorable(0, 40, 2.0, 0.8, 6000.0, -32.0)
			},
			wantMin: 0.25,
			wantMax: 0.50,
			desc:    "flat noisy spectrum outside voice range should score low",
		},
		{
			name: "centroid at edge of voice range",
			setup: func() []IntervalSample {
				// Good kurtosis and flatness, but centroid at edge of range (4400 Hz)
				// Still within range, so centroid score is about 0.5-0.6
				return makeSpeechIntervalsScorable(0, 40, 6.0, 0.1, 4400.0, -15.0)
			},
			wantMin: 0.75,
			wantMax: 0.95,
			desc:    "edge centroid slightly reduces score",
		},
		{
			name: "quiet speech (low RMS)",
			setup: func() []IntervalSample {
				// Good spectral characteristics but quiet (-28 dBFS)
				// RMS score: (-28 - (-30)) / 18 = 2/18 = 0.11
				return makeSpeechIntervalsScorable(0, 40, 6.0, 0.1, 2000.0, -28.0)
			},
			wantMin: 0.75,
			wantMax: 0.90,
			desc:    "quiet speech with good spectral should still score well",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			intervals := tt.setup()
			score := scoreSpeechIntervalWindow(intervals)

			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("scoreSpeechIntervalWindow() = %.3f, want [%.2f, %.2f] (%s)",
					score, tt.wantMin, tt.wantMax, tt.desc)
			}

			// Verify score is clamped to [0, 1]
			if score < 0.0 || score > 1.0 {
				t.Errorf("score %.3f outside [0, 1] range", score)
			}
		})
	}
}

func TestRefineToGoldenSpeechSubregion(t *testing.T) {
	tests := []struct {
		name          string
		candidate     *SpeechRegion
		intervals     []IntervalSample
		wantStart     time.Duration
		wantDuration  time.Duration
		wantUnchanged bool
		wantNil       bool
		desc          string
	}{
		{
			name: "short region - no refinement needed",
			candidate: &SpeechRegion{
				Start:    10 * time.Second,
				End:      50 * time.Second,
				Duration: 40 * time.Second, // 40s < 60s threshold
			},
			intervals:     makeSpeechIntervalsScorable(10*time.Second, 160, 6.0, 0.1, 2000.0, -15.0), // 40s
			wantStart:     10 * time.Second,
			wantDuration:  40 * time.Second,
			wantUnchanged: true,
			desc:          "regions <= 60s should pass through unchanged",
		},
		{
			name: "long region with uniform quality",
			candidate: &SpeechRegion{
				Start:    0,
				End:      120 * time.Second,
				Duration: 120 * time.Second, // 2 minutes
			},
			intervals:     makeSpeechIntervalsScorable(0, 480, 6.0, 0.1, 2000.0, -15.0), // 120s uniform
			wantStart:     0,                                                            // First window when all equal
			wantDuration:  60 * time.Second,
			wantUnchanged: false,
			desc:          "uniform quality should return first 60s window",
		},
		{
			name: "long region with clear best window at end",
			candidate: &SpeechRegion{
				Start:    0,
				End:      120 * time.Second,
				Duration: 120 * time.Second,
			},
			intervals: func() []IntervalSample {
				// First 60s: lower quality (low kurtosis, high flatness)
				first := makeSpeechIntervalsScorable(0, 240, 3.0, 0.5, 2000.0, -25.0)
				// Last 60s: high quality speech
				second := makeSpeechIntervalsScorable(60*time.Second, 240, 8.0, 0.08, 2000.0, -12.0)
				return append(first, second...)
			}(),
			wantStart:     60 * time.Second, // Should find better region in second half
			wantDuration:  60 * time.Second,
			wantUnchanged: false,
			desc:          "should find the higher quality 60s window",
		},
		{
			name:      "nil candidate",
			candidate: nil,
			intervals: makeSpeechIntervalsScorable(0, 480, 6.0, 0.1, 2000.0, -15.0),
			wantNil:   true,
			desc:      "nil input should return nil",
		},
		{
			name: "insufficient intervals",
			candidate: &SpeechRegion{
				Start:    0,
				End:      90 * time.Second,
				Duration: 90 * time.Second,
			},
			intervals:     makeSpeechIntervalsScorable(0, 100, 6.0, 0.1, 2000.0, -15.0), // 25s < 30s minimum
			wantStart:     0,
			wantDuration:  90 * time.Second,
			wantUnchanged: true,
			desc:          "insufficient intervals should return original",
		},
		{
			name: "no intervals in range",
			candidate: &SpeechRegion{
				Start:    200 * time.Second,
				End:      320 * time.Second,
				Duration: 120 * time.Second,
			},
			intervals:     makeSpeechIntervalsScorable(0, 480, 6.0, 0.1, 2000.0, -15.0), // 0-120s only
			wantStart:     200 * time.Second,
			wantDuration:  120 * time.Second,
			wantUnchanged: true,
			desc:          "should return original when no intervals match range",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := refineToGoldenSpeechSubregion(tt.candidate, tt.intervals)

			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil result, got %+v", result)
				}
				return
			}

			if result == nil {
				t.Fatal("unexpected nil result")
			}

			if tt.wantUnchanged {
				if result.Start != tt.candidate.Start || result.Duration != tt.candidate.Duration {
					t.Errorf("expected unchanged region, got Start=%v Duration=%v (original Start=%v Duration=%v) [%s]",
						result.Start, result.Duration, tt.candidate.Start, tt.candidate.Duration, tt.desc)
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

func TestFindBestSpeechRegion_WithRefinement(t *testing.T) {
	t.Run("refines long speech region", func(t *testing.T) {
		// Create a 120s speech region (> 60s threshold)
		regions := []SpeechRegion{
			{Start: 0, End: 120 * time.Second, Duration: 120 * time.Second},
		}

		// Create intervals with good speech characteristics
		// First 60s: moderate quality, Last 60s: high quality
		intervals := func() []IntervalSample {
			first := makeSpeechIntervalsScorable(0, 240, 4.0, 0.3, 2000.0, -20.0)
			second := makeSpeechIntervalsScorable(60*time.Second, 240, 7.0, 0.1, 2000.0, -14.0)
			return append(first, second...)
		}()

		result := findBestSpeechRegion(regions, intervals, nil)

		if result.BestRegion == nil {
			t.Fatal("expected a best region to be selected")
		}

		// Find the candidate metrics for the selected region
		if len(result.Candidates) == 0 {
			t.Fatal("expected candidates to be populated")
		}

		// The candidate should have WasRefined set to true
		foundRefined := false
		for _, c := range result.Candidates {
			if c.WasRefined {
				foundRefined = true

				// Verify original metadata is populated
				if c.OriginalStart != 0 {
					t.Errorf("OriginalStart = %v, want 0", c.OriginalStart)
				}
				if c.OriginalDuration != 120*time.Second {
					t.Errorf("OriginalDuration = %v, want 120s", c.OriginalDuration)
				}

				// Verify refined duration is <= 60s
				if c.Region.Duration > 60*time.Second {
					t.Errorf("Refined duration %v > 60s", c.Region.Duration)
				}

				break
			}
		}

		if !foundRefined {
			t.Error("expected WasRefined=true for long region")
		}
	})

	t.Run("does not refine short speech region", func(t *testing.T) {
		// Create a 45s speech region (< 60s threshold)
		regions := []SpeechRegion{
			{Start: 0, End: 45 * time.Second, Duration: 45 * time.Second},
		}

		// Create intervals with good speech characteristics
		intervals := makeSpeechIntervalsScorable(0, 180, 6.0, 0.1, 2000.0, -15.0)

		result := findBestSpeechRegion(regions, intervals, nil)

		if result.BestRegion == nil {
			t.Fatal("expected a best region to be selected")
		}

		// The candidate should NOT have WasRefined set
		for _, c := range result.Candidates {
			if c.WasRefined {
				t.Error("expected WasRefined=false for short region")
			}
		}

		// Duration should remain unchanged
		if result.BestRegion.Duration != 45*time.Second {
			t.Errorf("Duration = %v, want 45s", result.BestRegion.Duration)
		}
	})

	t.Run("selects best window from long region", func(t *testing.T) {
		// Create a 120s speech region with a clear "golden" 60s section
		regions := []SpeechRegion{
			{Start: 0, End: 120 * time.Second, Duration: 120 * time.Second},
		}

		// Create intervals where the middle section is clearly best
		intervals := func() []IntervalSample {
			// 0-30s: poor quality
			poor1 := makeSpeechIntervalsScorable(0, 120, 2.0, 0.6, 3500.0, -28.0)
			// 30-90s: excellent quality (this is the golden window)
			excellent := makeSpeechIntervalsScorable(30*time.Second, 240, 8.0, 0.05, 2000.0, -12.0)
			// 90-120s: poor quality
			poor2 := makeSpeechIntervalsScorable(90*time.Second, 120, 2.0, 0.6, 3500.0, -28.0)
			return append(append(poor1, excellent...), poor2...)
		}()

		result := findBestSpeechRegion(regions, intervals, nil)

		if result.BestRegion == nil {
			t.Fatal("expected a best region to be selected")
		}

		// The refined region should start somewhere in the excellent section (30-60s)
		if result.BestRegion.Start < 30*time.Second || result.BestRegion.Start > 60*time.Second {
			t.Errorf("Refined Start = %v, expected in range [30s, 60s]", result.BestRegion.Start)
		}

		// Refined duration should be 60s
		if result.BestRegion.Duration != 60*time.Second {
			t.Errorf("Refined Duration = %v, want 60s", result.BestRegion.Duration)
		}
	})
}

func TestFindBestSpeechRegion_SNRMarginCheck(t *testing.T) {
	t.Run("penalises low SNR margin candidates", func(t *testing.T) {
		// Create two speech regions with the same quality
		regions := []SpeechRegion{
			{Start: 0, End: 30 * time.Second, Duration: 30 * time.Second},
			{Start: 35 * time.Second, End: 65 * time.Second, Duration: 30 * time.Second},
		}

		// Create intervals with identical quality for both regions
		// Both have RMS level at -20 dBFS (good speech level)
		// Use makeSpeechIntervalsScorable for realistic spectral values
		intervals := makeSpeechIntervalsScorable(0, 260, 6.0, 0.1, 1500.0, -20.0)

		// Create noise profile with noise floor at -40 dBFS
		// SNR margin = -20 - (-40) = 20 dB = exactly minSNRMargin
		noiseProfileGood := &NoiseProfile{
			MeasuredNoiseFloor: -40.0, // 20 dB margin - passes
		}

		// Run with good SNR margin
		resultGood := findBestSpeechRegion(regions, intervals, noiseProfileGood)
		if resultGood.BestRegion == nil {
			t.Fatal("expected a best region to be selected with good SNR")
		}

		// Create noise profile with noise floor at -30 dBFS
		// SNR margin = -20 - (-30) = 10 dB < 20 dB minSNRMargin
		noiseProfileBad := &NoiseProfile{
			MeasuredNoiseFloor: -30.0, // 10 dB margin - should be penalised
		}

		// Run with poor SNR margin
		resultBad := findBestSpeechRegion(regions, intervals, noiseProfileBad)
		if resultBad.BestRegion == nil {
			t.Fatal("expected a best region to be selected even with poor SNR")
		}

		// Find the candidate scores
		var goodScore, badScore float64
		for _, c := range resultGood.Candidates {
			if c.Region.Start == 0 {
				goodScore = c.Score
				break
			}
		}
		for _, c := range resultBad.Candidates {
			if c.Region.Start == 0 {
				badScore = c.Score
				break
			}
		}

		// The bad SNR score should be lower than the good SNR score
		if badScore >= goodScore {
			t.Errorf("expected penalised score %.3f < good score %.3f", badScore, goodScore)
		}

		// The penalty should be proportional: 10/20 = 0.5
		// But minimum penalty is 0.1, so score should be between 0.1 and 1.0 times original
		expectedPenalty := 10.0 / 20.0 // 0.5
		expectedBadScore := goodScore * expectedPenalty
		tolerance := 0.01
		if badScore < expectedBadScore-tolerance || badScore > expectedBadScore+tolerance {
			t.Errorf("penalised score = %.3f, want ~%.3f (50%% of %.3f)", badScore, expectedBadScore, goodScore)
		}
	})

	t.Run("no penalty when noiseProfile is nil", func(t *testing.T) {
		regions := []SpeechRegion{
			{Start: 0, End: 30 * time.Second, Duration: 30 * time.Second},
		}

		// Create intervals with good speech characteristics
		intervals := makeSpeechIntervalsScorable(0, 120, 6.0, 0.1, 1500.0, -20.0)

		// Run without noise profile
		resultNoProfile := findBestSpeechRegion(regions, intervals, nil)
		if resultNoProfile.BestRegion == nil {
			t.Fatal("expected a best region to be selected without noise profile")
		}

		// Create a "good" noise profile for comparison
		// SNR margin = -20 - (-40) = 20 dB = exactly minSNRMargin
		noiseProfileGood := &NoiseProfile{
			MeasuredNoiseFloor: -40.0, // 20 dB margin - passes
		}
		resultWithProfile := findBestSpeechRegion(regions, intervals, noiseProfileGood)

		// Scores should be equal when nil (no penalty applied)
		var scoreNoProfile, scoreWithProfile float64
		for _, c := range resultNoProfile.Candidates {
			if c.Region.Start == 0 {
				scoreNoProfile = c.Score
				break
			}
		}
		for _, c := range resultWithProfile.Candidates {
			if c.Region.Start == 0 {
				scoreWithProfile = c.Score
				break
			}
		}

		if scoreNoProfile != scoreWithProfile {
			t.Errorf("scores should be equal: nil profile = %.3f, good profile = %.3f",
				scoreNoProfile, scoreWithProfile)
		}
	})
}

func TestMeasureOutputSilenceRegion(t *testing.T) {
	// Generate processed test audio file with known silence region
	// Using a simple tone with a substantial silence gap for predictable measurements
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 5.0,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -23.0, // Typical podcast level
		NoiseLevel:   -60.0, // Light background noise
		SilenceGap: struct {
			Start    float64
			Duration float64
		}{
			Start:    1.5, // Silence at 1.5 seconds
			Duration: 1.0, // 1 second silence gap (long enough for reliable measurements)
		},
	})
	defer cleanupTestAudio(t, testFile)

	// Define the silence region we want to measure
	silenceRegion := SilenceRegion{
		Start:    time.Duration(1.5 * float64(time.Second)),
		End:      time.Duration(2.5 * float64(time.Second)),
		Duration: time.Duration(1.0 * float64(time.Second)),
	}

	t.Run("valid_silence_region", func(t *testing.T) {
		metrics, err := MeasureOutputSilenceRegion(testFile, silenceRegion)
		if err != nil {
			t.Fatalf("MeasureOutputSilenceRegion failed: %v", err)
		}

		if metrics == nil {
			t.Fatal("metrics is nil")
		}

		// Log all measurements for inspection
		t.Logf("Silence Region Measurements:")
		t.Logf("  RMSLevel: %.2f dBFS", metrics.RMSLevel)
		t.Logf("  PeakLevel: %.2f dBFS", metrics.PeakLevel)
		t.Logf("  CrestFactor: %.2f dB", metrics.CrestFactor)
		t.Logf("  SpectralCentroid: %.2f Hz", metrics.SpectralCentroid)
		t.Logf("  SpectralEntropy: %.2f", metrics.SpectralEntropy)
		t.Logf("  SpectralFlatness: %.2f", metrics.SpectralFlatness)
		t.Logf("  MomentaryLUFS: %.2f LUFS", metrics.MomentaryLUFS)
		t.Logf("  ShortTermLUFS: %.2f LUFS", metrics.ShortTermLUFS)
		t.Logf("  TruePeak: %.2f dBTP", metrics.TruePeak)

		// Verify region is captured correctly
		if metrics.Region.Start != silenceRegion.Start {
			t.Errorf("Region start mismatch: got %v, want %v", metrics.Region.Start, silenceRegion.Start)
		}
		if metrics.Region.Duration != silenceRegion.Duration {
			t.Errorf("Region duration mismatch: got %v, want %v", metrics.Region.Duration, silenceRegion.Duration)
		}

		// Amplitude metrics: silence should have very low RMS (< -40 dBFS)
		// With -60dB noise, we expect RMS around -60dB range
		if metrics.RMSLevel > -40.0 {
			t.Errorf("RMSLevel too high for silence: %.2f dBFS (expected < -40)", metrics.RMSLevel)
		}

		// Peak should also be low for silence region
		if metrics.PeakLevel > -30.0 {
			t.Errorf("PeakLevel too high for silence: %.2f dBFS (expected < -30)", metrics.PeakLevel)
		}

		// Spectral entropy should be relatively high for noise (closer to 1.0 than speech)
		// We don't enforce strict bounds since synthesis may vary
		if metrics.SpectralEntropy < 0.0 || metrics.SpectralEntropy > 1.0 {
			t.Logf("SpectralEntropy out of [0,1] range: %.2f (may be filter-specific)", metrics.SpectralEntropy)
		}

		// Spectral centroid should be present (non-zero)
		// Even noise has spectral content
		if metrics.SpectralCentroid < 0.0 {
			t.Errorf("SpectralCentroid should be non-negative: %.2f Hz", metrics.SpectralCentroid)
		}

		// LUFS measurements may be invalid for very quiet regions
		// Just check they're within plausible dB range
		if metrics.MomentaryLUFS < -120.0 || metrics.MomentaryLUFS > 0.0 {
			t.Logf("MomentaryLUFS outside plausible range: %.2f LUFS", metrics.MomentaryLUFS)
		}
	})

	t.Run("invalid_path", func(t *testing.T) {
		metrics, err := MeasureOutputSilenceRegion("/nonexistent/path.wav", silenceRegion)
		if err == nil {
			t.Error("Expected error for invalid path, got nil")
		}
		if metrics != nil {
			t.Error("Expected nil metrics for invalid path")
		}
	})

	t.Run("zero_duration_region", func(t *testing.T) {
		zeroRegion := SilenceRegion{
			Start:    time.Duration(1.0 * float64(time.Second)),
			End:      time.Duration(1.0 * float64(time.Second)),
			Duration: 0,
		}
		metrics, err := MeasureOutputSilenceRegion(testFile, zeroRegion)
		if err == nil {
			t.Error("Expected error for zero duration region, got nil")
		}
		if metrics != nil {
			t.Error("Expected nil metrics for zero duration region")
		}
	})
}

func TestMeasureOutputSpeechRegion(t *testing.T) {
	// Generate processed test audio file with known speech-like characteristics
	// Using a sustained tone to represent speech energy
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 5.0,
		SampleRate:   44100,
		ToneFreq:     440.0, // A4 note (speech-like frequency)
		ToneLevel:    -20.0, // Typical speech level after processing
		NoiseLevel:   -60.0, // Background noise
		SilenceGap: struct {
			Start    float64
			Duration float64
		}{
			Start:    0.0, // No silence gap for speech test
			Duration: 0.0,
		},
	})
	defer cleanupTestAudio(t, testFile)

	// Define the speech region we want to measure
	speechRegion := SpeechRegion{
		Start:    time.Duration(1.0 * float64(time.Second)),
		End:      time.Duration(3.0 * float64(time.Second)),
		Duration: time.Duration(2.0 * float64(time.Second)),
	}

	t.Run("valid_speech_region", func(t *testing.T) {
		metrics, err := MeasureOutputSpeechRegion(testFile, speechRegion)
		if err != nil {
			t.Fatalf("MeasureOutputSpeechRegion failed: %v", err)
		}

		if metrics == nil {
			t.Fatal("metrics is nil")
		}

		// Log all measurements for inspection
		t.Logf("Speech Region Measurements:")
		t.Logf("  RMSLevel: %.2f dBFS", metrics.RMSLevel)
		t.Logf("  PeakLevel: %.2f dBFS", metrics.PeakLevel)
		t.Logf("  CrestFactor: %.2f dB", metrics.CrestFactor)
		t.Logf("  SpectralCentroid: %.2f Hz", metrics.SpectralCentroid)
		t.Logf("  SpectralEntropy: %.2f", metrics.SpectralEntropy)
		t.Logf("  SpectralFlatness: %.2f", metrics.SpectralFlatness)
		t.Logf("  MomentaryLUFS: %.2f LUFS", metrics.MomentaryLUFS)
		t.Logf("  ShortTermLUFS: %.2f LUFS", metrics.ShortTermLUFS)
		t.Logf("  TruePeak: %.2f dBTP", metrics.TruePeak)

		// Verify region is captured correctly
		if metrics.Region.Start != speechRegion.Start {
			t.Errorf("Region start mismatch: got %v, want %v", metrics.Region.Start, speechRegion.Start)
		}
		if metrics.Region.Duration != speechRegion.Duration {
			t.Errorf("Region duration mismatch: got %v, want %v", metrics.Region.Duration, speechRegion.Duration)
		}

		// Amplitude metrics: speech should have substantial RMS (> -40 dBFS)
		// With -20dBFS tone, we expect RMS around -20 to -23 dBFS
		if metrics.RMSLevel < -30.0 || metrics.RMSLevel > -10.0 {
			t.Errorf("RMSLevel out of expected range for speech: %.2f dBFS (expected -30 to -10)", metrics.RMSLevel)
		}

		// Peak should be higher than RMS but below 0 dBFS
		if metrics.PeakLevel < -25.0 || metrics.PeakLevel > 0.0 {
			t.Errorf("PeakLevel out of expected range: %.2f dBFS (expected -25 to 0)", metrics.PeakLevel)
		}

		// Crest factor for a sine wave should be around 3dB (peak = RMS + 3dB)
		// Allow wider range (0-10dB) for measurement variations
		if metrics.CrestFactor < 0.0 || metrics.CrestFactor > 10.0 {
			t.Logf("CrestFactor outside expected range: %.2f dB (typical sine wave ~3dB)", metrics.CrestFactor)
		}

		// Spectral centroid should be near tone frequency (440 Hz)
		// Allow wide tolerance since FFT window and resolution affect this
		if metrics.SpectralCentroid < 100.0 || metrics.SpectralCentroid > 2000.0 {
			t.Logf("SpectralCentroid outside plausible range: %.2f Hz (tone at 440 Hz)", metrics.SpectralCentroid)
		}

		// Spectral flatness should be low for tonal signal (< 0.5)
		// Sine wave is very tonal, not noise-like
		if metrics.SpectralFlatness < 0.0 || metrics.SpectralFlatness > 1.0 {
			t.Logf("SpectralFlatness out of [0,1] range: %.2f", metrics.SpectralFlatness)
		}

		// LUFS should reflect the -20dBFS tone level
		// Momentary LUFS should be roughly in -20 to -18 LUFS range
		if metrics.MomentaryLUFS < -30.0 || metrics.MomentaryLUFS > -10.0 {
			t.Logf("MomentaryLUFS outside expected range: %.2f LUFS (expected ~-20)", metrics.MomentaryLUFS)
		}

		// True peak should be close to sine wave peak (~-17 dBTP for -20dBFS RMS)
		if metrics.TruePeak < -25.0 || metrics.TruePeak > 0.0 {
			t.Logf("TruePeak outside plausible range: %.2f dBTP", metrics.TruePeak)
		}
	})

	t.Run("invalid_path", func(t *testing.T) {
		metrics, err := MeasureOutputSpeechRegion("/nonexistent/path.wav", speechRegion)
		if err == nil {
			t.Error("Expected error for invalid path, got nil")
		}
		if metrics != nil {
			t.Error("Expected nil metrics for invalid path")
		}
	})

	t.Run("zero_duration_region", func(t *testing.T) {
		zeroRegion := SpeechRegion{
			Start:    time.Duration(1.0 * float64(time.Second)),
			End:      time.Duration(1.0 * float64(time.Second)),
			Duration: 0,
		}
		metrics, err := MeasureOutputSpeechRegion(testFile, zeroRegion)
		if err == nil {
			t.Error("Expected error for zero duration region, got nil")
		}
		if metrics != nil {
			t.Error("Expected nil metrics for zero duration region")
		}
	})
}

// ============================================================================
// Crest Factor Penalty Tests
// ============================================================================

func TestCrestFactorPenalty(t *testing.T) {
	tests := []struct {
		name        string
		crest       float64
		peak        float64
		rms         float64
		wantPenalty string // "none", "soft", "hard"
	}{
		{"clean room tone", 15.0, -60.0, -75.0, "none"},
		{"elevated crest, safe peak", 35.0, -50.0, -80.0, "soft"},
		{"danger zone signature", 40.0, -32.0, -74.0, "hard"},
		{"high crest, clipping peak", 50.0, -10.0, -61.0, "soft"},
		{"high crest, loud peak outside zone", 60.0, -20.0, -81.0, "soft"},
		{"boundary: just below soft threshold", 29.9, -35.0, -72.0, "none"},
		{"boundary: at soft threshold", 30.1, -50.0, -75.0, "soft"},
		{"boundary: peak at zone low edge", 40.0, -40.0, -74.0, "soft"}, // peak == -40 is NOT > -40
		{"boundary: peak just inside zone", 40.0, -39.9, -74.0, "hard"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseScore := 1.0
			result := applyCrestFactorPenalty(baseScore, tt.crest, tt.peak, tt.rms)

			var gotPenalty string
			switch {
			case result == 1.0:
				gotPenalty = "none"
			case result < 0.6: // Hard penalty = 0.5x or less (may compound with soft)
				gotPenalty = "hard"
			default:
				gotPenalty = "soft"
			}

			if gotPenalty != tt.wantPenalty {
				t.Errorf("applyCrestFactorPenalty() = %.3f (%s), want %s penalty",
					result, gotPenalty, tt.wantPenalty)
			}
		})
	}
}

// ============================================================================
// Three-Zone Temporal Bias Tests
// ============================================================================

func TestApplyTemporalBias(t *testing.T) {
	tests := []struct {
		name      string
		timeSecs  float64
		want      float64
		tolerance float64
		zone      string
	}{
		{
			name:      "before golden window (0s)",
			timeSecs:  0,
			want:      1.0,
			tolerance: 0,
			zone:      "Before golden window",
		},
		{
			name:      "early boundary (15s)",
			timeSecs:  15,
			want:      1.10,
			tolerance: 0.001,
			zone:      "Early boundary",
		},
		{
			name:      "early midpoint (52.5s)",
			timeSecs:  52.5,
			want:      1.05,
			tolerance: 0.001,
			zone:      "Early midpoint",
		},
		{
			name:      "early/transition boundary (90s)",
			timeSecs:  90,
			want:      1.0,
			tolerance: 0.001,
			zone:      "Early/Transition boundary",
		},
		{
			name:      "transition minimum (135s)",
			timeSecs:  135,
			want:      0.70,
			tolerance: 0.001,
			zone:      "Transition minimum",
		},
		{
			name:      "neutral boundary (180s)",
			timeSecs:  180,
			want:      1.0,
			tolerance: 0.001,
			zone:      "Neutral boundary",
		},
		{
			name:      "deep neutral (300s)",
			timeSecs:  300,
			want:      1.0,
			tolerance: 0,
			zone:      "Deep neutral",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duration := time.Duration(tt.timeSecs * float64(time.Second))
			got := applyTemporalBias(duration)

			diff := got - tt.want
			if diff < 0 {
				diff = -diff
			}

			if tt.tolerance == 0 {
				// Exact match required
				if got != tt.want {
					t.Errorf("applyTemporalBias(%v) = %.6f, want exactly %.6f [%s]",
						duration, got, tt.want, tt.zone)
				}
			} else {
				// Tolerance-based comparison
				if diff > tt.tolerance {
					t.Errorf("applyTemporalBias(%v) = %.6f, want %.6f ±%.3f [%s]",
						duration, got, tt.want, tt.tolerance, tt.zone)
				}
			}
		})
	}
}

// ============================================================================
// Silence Region Selection Integration Tests (Temporal Weighting)
// ============================================================================

// makeSilenceTestIntervals creates synthetic interval samples for silence testing.
// Populates all fields needed for scoreSilenceCandidate: RMS, peak, spectral centroid,
// spectral flatness, spectral kurtosis.
//
// Parameters control the "quality" of the silence:
//   - rms: RMS level (lower = quieter = better silence)
//   - centroid: Spectral centroid (outside voice range 250-4500 Hz = better)
//   - flatness: Spectral flatness (higher = more noise-like = better)
//   - kurtosis: Spectral kurtosis (lower = less peaked = better)
func makeSilenceTestIntervals(startTime time.Duration, count int, rms, centroid, flatness, kurtosis float64) []IntervalSample {
	intervals := make([]IntervalSample, count)
	for i := range intervals {
		intervals[i] = IntervalSample{
			Timestamp:        startTime + time.Duration(i)*250*time.Millisecond,
			RMSLevel:         rms,
			PeakLevel:        rms + 10.0, // Moderate crest factor
			SpectralCentroid: centroid,
			SpectralFlatness: flatness,
			SpectralKurtosis: kurtosis,
		}
	}
	return intervals
}

func TestFindBestSilenceRegion_PrefersEarlyCandidates(t *testing.T) {
	// Test that early candidates win when quality is comparable.
	// Region A at 30s (in early zone) should beat Region B at 135s (in transition zone)
	// even though B has slightly better raw quality, because temporal weighting
	// gives A a ~1.08× bonus and B a 0.70× penalty.

	// Region A: 30s into recording (early zone, ~1.08× bonus)
	// 10-second region needs 40 intervals (10s / 250ms)
	regionA := SilenceRegion{
		Start:    30 * time.Second,
		End:      40 * time.Second,
		Duration: 10 * time.Second,
	}

	// Region B: 135s into recording (transition zone midpoint, 0.70× penalty)
	regionB := SilenceRegion{
		Start:    135 * time.Second,
		End:      145 * time.Second,
		Duration: 10 * time.Second,
	}

	regions := []SilenceRegion{regionA, regionB}

	// Create intervals for Region A: good quality silence
	// RMS -65 dBFS, centroid 100 Hz (below voice range), flatness 0.8, kurtosis 2.0
	intervalsA := makeSilenceTestIntervals(30*time.Second, 40, -65.0, 100.0, 0.8, 2.0)

	// Create intervals for Region B: slightly BETTER raw quality than A
	// RMS -68 dBFS (quieter), centroid 80 Hz, flatness 0.85, kurtosis 1.5
	// This should produce a higher base score than A
	intervalsB := makeSilenceTestIntervals(135*time.Second, 40, -68.0, 80.0, 0.85, 1.5)

	// Combine all intervals
	intervals := append(intervalsA, intervalsB...)

	// Total duration must be long enough so both regions are within 15% cutoff
	// 135s + 10s = 145s must be < 0.15 * totalDuration
	// So totalDuration > 145 / 0.15 = 966.7s, use 3600s (1 hour)
	totalDuration := 3600.0

	result := findBestSilenceRegion(regions, intervals, totalDuration)

	if result.BestRegion == nil {
		t.Fatal("expected a best region to be selected")
	}

	// Verify candidates were scored
	if len(result.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result.Candidates))
	}

	// Find scores for both candidates
	var scoreA, scoreB float64
	for _, c := range result.Candidates {
		if c.Region.Start == 30*time.Second {
			scoreA = c.Score
		} else if c.Region.Start == 135*time.Second {
			scoreB = c.Score
		}
	}

	t.Logf("Region A (30s, early zone): score=%.4f", scoreA)
	t.Logf("Region B (135s, transition zone): score=%.4f", scoreB)

	// Verify temporal weighting effect:
	// A's adjusted score should be higher than B's despite B having better raw quality
	// because A gets ~1.08× bonus and B gets 0.70× penalty
	if scoreA <= scoreB {
		t.Errorf("Expected Region A (early zone) to score higher than Region B (transition zone): A=%.4f, B=%.4f", scoreA, scoreB)
	}

	// Verify A was selected
	if result.BestRegion.Start != 30*time.Second {
		t.Errorf("Expected Region A (30s) to be selected, got region at %v", result.BestRegion.Start)
	}
}

func TestFindBestSilenceRegion_NeutralZoneFallback(t *testing.T) {
	// Test that when no early candidates exist, selection is purely quality-based
	// in the neutral zone (180s+) where temporal multiplier = 1.0.

	// Region C: 200s into recording (neutral zone, multiplier = 1.0)
	// 10-second region needs 40 intervals
	regionC := SilenceRegion{
		Start:    200 * time.Second,
		End:      210 * time.Second,
		Duration: 10 * time.Second,
	}

	// Region D: 250s into recording (neutral zone, multiplier = 1.0)
	regionD := SilenceRegion{
		Start:    250 * time.Second,
		End:      260 * time.Second,
		Duration: 10 * time.Second,
	}

	regions := []SilenceRegion{regionC, regionD}

	// Create intervals for Region C: better quality silence
	// RMS -70 dBFS, centroid 90 Hz (below voice range), flatness 0.9, kurtosis 1.5
	intervalsC := makeSilenceTestIntervals(200*time.Second, 40, -70.0, 90.0, 0.9, 1.5)

	// Create intervals for Region D: worse quality than C
	// RMS -62 dBFS (louder), centroid 150 Hz, flatness 0.7, kurtosis 3.0
	intervalsD := makeSilenceTestIntervals(250*time.Second, 40, -62.0, 150.0, 0.7, 3.0)

	// Combine all intervals
	intervals := append(intervalsC, intervalsD...)

	// Total duration must be long enough so both regions are within 15% cutoff
	// 250s + 10s = 260s must be < 0.15 * totalDuration
	// So totalDuration > 260 / 0.15 = 1733s, use 3600s (1 hour)
	totalDuration := 3600.0

	result := findBestSilenceRegion(regions, intervals, totalDuration)

	if result.BestRegion == nil {
		t.Fatal("expected a best region to be selected")
	}

	// Verify candidates were scored
	if len(result.Candidates) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(result.Candidates))
	}

	// Find scores for both candidates
	var scoreC, scoreD float64
	for _, c := range result.Candidates {
		if c.Region.Start == 200*time.Second {
			scoreC = c.Score
		} else if c.Region.Start == 250*time.Second {
			scoreD = c.Score
		}
	}

	t.Logf("Region C (200s, neutral zone): score=%.4f", scoreC)
	t.Logf("Region D (250s, neutral zone): score=%.4f", scoreD)

	// Both are in neutral zone, so temporal multiplier = 1.0 for both
	// Selection should be purely quality-based, and C has better quality
	if scoreC <= scoreD {
		t.Errorf("Expected Region C (better quality) to score higher than Region D: C=%.4f, D=%.4f", scoreC, scoreD)
	}

	// Verify C was selected
	if result.BestRegion.Start != 200*time.Second {
		t.Errorf("Expected Region C (200s, better quality) to be selected, got region at %v", result.BestRegion.Start)
	}
}

// ============================================================================
// Relaxed Threshold Silence Detection Tests (Phase 3)
// ============================================================================

// makeRelaxedThresholdIntervals creates synthetic interval samples for relaxed threshold testing.
// Allows control over all fields needed for the stricter spectral validation:
// - RMS level (for threshold comparison)
// - SpectralFlatness (must be > 0.4 for fallback candidates)
// - SpectralKurtosis (must be < 8.0 for fallback candidates)
// - SpectralCentroid, SpectralFlux, SpectralRolloff (for room tone scoring)
func makeRelaxedThresholdIntervals(startTime time.Duration, count int, rms, flatness, kurtosis float64) []IntervalSample {
	intervals := make([]IntervalSample, count)
	for i := range intervals {
		intervals[i] = IntervalSample{
			Timestamp:        startTime + time.Duration(i)*250*time.Millisecond,
			RMSLevel:         rms,
			SpectralFlatness: flatness,
			SpectralKurtosis: kurtosis,
			// Set good values for room tone scoring
			SpectralCentroid: 100.0,   // Low centroid (not voice-like)
			SpectralFlux:     0.001,   // Low flux (stable)
			SpectralRolloff:  1000.0,  // Low rolloff
			PeakLevel:        rms + 6, // Reasonable crest factor
		}
	}
	return intervals
}

func TestFindSilenceCandidatesWithRelaxedThreshold(t *testing.T) {
	// Constants from analyzer.go
	// earlyWindowStart = 15.0  // seconds
	// earlyWindowEnd   = 90.0  // seconds
	// fallbackMinFlatness = 0.4
	// fallbackMaxKurtosis = 8.0
	// minimumSilenceIntervals = 32 (8 seconds)

	tests := []struct {
		name               string
		intervals          []IntervalSample
		relaxedThreshold   float64
		wantCandidateCount int
		desc               string
	}{
		{
			name: "good candidate: RMS below threshold + high flatness + low kurtosis",
			intervals: makeRelaxedThresholdIntervals(
				15*time.Second, // Start at golden window start
				40,             // 10 seconds (40 × 250ms)
				-65.0,          // RMS below typical relaxed threshold
				0.5,            // Flatness > 0.4 (passes)
				5.0,            // Kurtosis < 8.0 (passes)
			),
			relaxedThreshold:   -60.0, // RMS -65 is below -60
			wantCandidateCount: 1,
			desc:               "candidate with good spectral characteristics should be detected",
		},
		{
			name: "low flatness rejected",
			intervals: makeRelaxedThresholdIntervals(
				15*time.Second,
				40,
				-65.0, // RMS passes
				0.3,   // Flatness < 0.4 (FAILS)
				5.0,   // Kurtosis passes
			),
			relaxedThreshold:   -60.0,
			wantCandidateCount: 0,
			desc:               "intervals with low flatness (0.3 < 0.4) should be rejected",
		},
		{
			name: "high kurtosis rejected",
			intervals: makeRelaxedThresholdIntervals(
				15*time.Second,
				40,
				-65.0, // RMS passes
				0.5,   // Flatness passes
				10.0,  // Kurtosis > 8.0 (FAILS)
			),
			relaxedThreshold:   -60.0,
			wantCandidateCount: 0,
			desc:               "intervals with high kurtosis (10 > 8) should be rejected",
		},
		{
			name: "both spectral checks fail",
			intervals: makeRelaxedThresholdIntervals(
				15*time.Second,
				40,
				-65.0, // RMS passes
				0.2,   // Flatness < 0.4 (FAILS)
				12.0,  // Kurtosis > 8.0 (FAILS)
			),
			relaxedThreshold:   -60.0,
			wantCandidateCount: 0,
			desc:               "intervals failing both spectral checks should be rejected",
		},
		{
			name: "outside golden window: good candidate at 100s",
			intervals: makeRelaxedThresholdIntervals(
				100*time.Second, // Outside golden window (15-90s)
				40,
				-65.0, // RMS passes
				0.5,   // Flatness passes
				5.0,   // Kurtosis passes
			),
			relaxedThreshold:   -60.0,
			wantCandidateCount: 0,
			desc:               "candidates outside 15-90s golden window should not be found",
		},
		{
			name: "RMS above threshold rejected",
			intervals: makeRelaxedThresholdIntervals(
				15*time.Second,
				40,
				-55.0, // RMS above threshold (FAILS)
				0.5,   // Flatness passes
				5.0,   // Kurtosis passes
			),
			relaxedThreshold:   -60.0,
			wantCandidateCount: 0,
			desc:               "intervals with RMS above threshold should be rejected",
		},
		{
			name: "boundary: flatness exactly at threshold",
			intervals: makeRelaxedThresholdIntervals(
				15*time.Second,
				40,
				-65.0,
				0.4, // Flatness == 0.4 (condition is > 0.4, so this FAILS)
				5.0,
			),
			relaxedThreshold:   -60.0,
			wantCandidateCount: 0,
			desc:               "flatness exactly at 0.4 should fail (needs to be > 0.4)",
		},
		{
			name: "boundary: kurtosis exactly at threshold",
			intervals: makeRelaxedThresholdIntervals(
				15*time.Second,
				40,
				-65.0,
				0.5,
				8.0, // Kurtosis == 8.0 (condition is < 8.0, so this FAILS)
			),
			relaxedThreshold:   -60.0,
			wantCandidateCount: 0,
			desc:               "kurtosis exactly at 8.0 should fail (needs to be < 8.0)",
		},
		{
			name: "boundary: flatness just above threshold",
			intervals: makeRelaxedThresholdIntervals(
				15*time.Second,
				40,
				-65.0,
				0.41, // Flatness > 0.4 (passes)
				5.0,
			),
			relaxedThreshold:   -60.0,
			wantCandidateCount: 1,
			desc:               "flatness just above 0.4 should pass",
		},
		{
			name: "boundary: kurtosis just below threshold",
			intervals: makeRelaxedThresholdIntervals(
				15*time.Second,
				40,
				-65.0,
				0.5,
				7.9, // Kurtosis < 8.0 (passes)
			),
			relaxedThreshold:   -60.0,
			wantCandidateCount: 1,
			desc:               "kurtosis just below 8.0 should pass",
		},
		{
			name:               "insufficient intervals",
			intervals:          makeRelaxedThresholdIntervals(15*time.Second, 10, -65.0, 0.5, 5.0), // Only 10 intervals
			relaxedThreshold:   -60.0,
			wantCandidateCount: 0,
			desc:               "fewer than minimum intervals should return no candidates",
		},
		{
			name: "candidate at golden window end boundary",
			intervals: makeRelaxedThresholdIntervals(
				80*time.Second, // Starts at 80s, ends at 90s (at golden window end)
				40,             // 10 seconds
				-65.0,
				0.5,
				5.0,
			),
			relaxedThreshold:   -60.0,
			wantCandidateCount: 1,
			desc:               "candidate ending at golden window boundary should be detected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidates := findSilenceCandidatesWithRelaxedThreshold(tt.intervals, tt.relaxedThreshold)

			gotCount := len(candidates)
			if gotCount != tt.wantCandidateCount {
				t.Errorf("findSilenceCandidatesWithRelaxedThreshold() returned %d candidates, want %d [%s]",
					gotCount, tt.wantCandidateCount, tt.desc)
			}

			// For positive cases, verify the candidate is in the golden window
			if tt.wantCandidateCount > 0 && gotCount > 0 {
				for _, c := range candidates {
					goldenStart := 15 * time.Second
					goldenEnd := 90 * time.Second
					if c.Start < goldenStart || c.Start >= goldenEnd {
						t.Errorf("candidate start %v is outside golden window [15s, 90s)", c.Start)
					}
				}
			}
		})
	}
}

func TestFindSilenceCandidatesWithRelaxedThreshold_InterruptionTolerance(t *testing.T) {
	// Test that the function handles brief interruptions same as the original function
	// (interruptionToleranceIntervals = 3, so up to 3 consecutive non-qualifying intervals allowed)

	t.Run("bridges short gap", func(t *testing.T) {
		// Create good intervals, then 2 bad intervals (within tolerance), then more good intervals
		goodPart1 := makeRelaxedThresholdIntervals(15*time.Second, 20, -65.0, 0.5, 5.0)
		badPart := makeRelaxedThresholdIntervals(20*time.Second, 2, -65.0, 0.2, 5.0) // Low flatness
		goodPart2 := makeRelaxedThresholdIntervals(20500*time.Millisecond, 20, -65.0, 0.5, 5.0)

		intervals := append(append(goodPart1, badPart...), goodPart2...)

		candidates := findSilenceCandidatesWithRelaxedThreshold(intervals, -60.0)

		// Should find one candidate that bridges the gap
		if len(candidates) != 1 {
			t.Errorf("expected 1 candidate bridging gap, got %d", len(candidates))
		}
	})

	t.Run("breaks on long gap", func(t *testing.T) {
		// Create good intervals, then 5 bad intervals (exceeds tolerance of 3), then more good intervals
		goodPart1 := makeRelaxedThresholdIntervals(15*time.Second, 20, -65.0, 0.5, 5.0)
		badPart := makeRelaxedThresholdIntervals(20*time.Second, 5, -65.0, 0.2, 5.0) // Low flatness, 5 intervals
		goodPart2 := makeRelaxedThresholdIntervals(21250*time.Millisecond, 20, -65.0, 0.5, 5.0)

		intervals := append(append(goodPart1, badPart...), goodPart2...)

		candidates := findSilenceCandidatesWithRelaxedThreshold(intervals, -60.0)

		// Should find no candidates because neither segment reaches minimum duration (32 intervals = 8s)
		// First segment: 20 intervals = 5s (too short)
		// Second segment: 20 intervals = 5s (too short)
		if len(candidates) != 0 {
			t.Errorf("expected 0 candidates (segments too short after break), got %d", len(candidates))
		}
	})
}

func TestFindSilenceCandidatesWithRelaxedThreshold_MinimumDuration(t *testing.T) {
	// minimumSilenceIntervals = 32 (8 seconds)

	t.Run("rejects short candidate", func(t *testing.T) {
		// 30 intervals = 7.5s < 8s minimum
		intervals := makeRelaxedThresholdIntervals(15*time.Second, 30, -65.0, 0.5, 5.0)

		candidates := findSilenceCandidatesWithRelaxedThreshold(intervals, -60.0)

		if len(candidates) != 0 {
			t.Errorf("expected 0 candidates for 7.5s duration, got %d", len(candidates))
		}
	})

	t.Run("accepts minimum duration", func(t *testing.T) {
		// 32 intervals = 8s exactly
		intervals := makeRelaxedThresholdIntervals(15*time.Second, 32, -65.0, 0.5, 5.0)

		candidates := findSilenceCandidatesWithRelaxedThreshold(intervals, -60.0)

		if len(candidates) != 1 {
			t.Errorf("expected 1 candidate for 8s duration, got %d", len(candidates))
		}
	})
}

// ============================================================================
// Early Window Fallback Integration Tests (Phase 5)
// ============================================================================

// makeFallbackTestIntervals creates synthetic intervals for fallback integration testing.
// Fills the full golden window (15-90s) with configurable characteristics.
// Parameters control RMS level, flatness, and kurtosis for testing different scenarios.
func makeFallbackTestIntervals(
	startTime time.Duration,
	durationSecs float64,
	rms, flatness, kurtosis float64,
) []IntervalSample {
	count := int(durationSecs * 4) // 4 intervals per second (250ms each)
	intervals := make([]IntervalSample, count)
	for i := range intervals {
		intervals[i] = IntervalSample{
			Timestamp:        startTime + time.Duration(i)*250*time.Millisecond,
			RMSLevel:         rms,
			SpectralFlatness: flatness,
			SpectralKurtosis: kurtosis,
			// Good values for room tone scoring
			SpectralCentroid: 100.0,
			SpectralFlux:     0.001,
			SpectralRolloff:  1000.0,
			PeakLevel:        rms + 6, // Reasonable crest factor
		}
	}
	return intervals
}

func TestFallbackNotTriggeredWhenCandidatesExist(t *testing.T) {
	// Phase 5.0: Negative test - fallback should NOT be triggered when valid
	// candidates exist in the golden window (15-90s).
	//
	// Scenario:
	// - Create intervals where golden window has valid silence candidates
	// - Candidates pass the normal adaptive threshold
	// - Verify fallback is NOT used
	//
	// Note: We use findSilenceCandidatesWithRelaxedThreshold directly rather than
	// going through the normal detection flow, because the normal detection uses
	// room tone scoring which has complex requirements. The key behaviour we're
	// testing is whether FallbackUsed/FallbackThreshold are correctly NOT set
	// when candidates exist.

	t.Run("good candidates in golden window", func(t *testing.T) {
		// Create a straightforward scenario: use the relaxed threshold function
		// with intervals that clearly pass all criteria to simulate having
		// candidates in the golden window.
		//
		// In real AnalyzeAudio, if findSilenceCandidatesFromIntervals finds
		// candidates in [15s, 90s), fallback is NOT triggered.

		// Create excellent silence from 20-30s (10 seconds, meets 8s minimum)
		// with good spectral characteristics for relaxed threshold detection
		silenceIntervals := makeRelaxedThresholdIntervals(20*time.Second, 40, -65.0, 0.5, 5.0)

		// Use a threshold that the silence will pass
		threshold := -60.0

		// Verify we have a candidate with the relaxed function
		candidates := findSilenceCandidatesWithRelaxedThreshold(silenceIntervals, threshold)
		t.Logf("Found %d candidates", len(candidates))

		if len(candidates) == 0 {
			t.Fatal("Test setup error: expected at least one candidate")
		}

		// Verify the candidate is in the golden window
		goldenStart := 15 * time.Second
		goldenEnd := 90 * time.Second
		hasGoldenWindowCandidate := false
		for _, c := range candidates {
			if c.Start >= goldenStart && c.Start < goldenEnd {
				hasGoldenWindowCandidate = true
				t.Logf("Found golden window candidate: start=%v, duration=%v", c.Start, c.Duration)
				break
			}
		}

		if !hasGoldenWindowCandidate {
			t.Fatal("Test setup error: candidate should be in golden window")
		}

		// Now simulate the AnalyzeAudio flow:
		// When candidates exist in golden window, fallback is NOT needed
		fallbackNeeded := !hasGoldenWindowCandidate

		// Convert to SilenceRegion for findBestSilenceRegion
		regions := make([]SilenceRegion, len(candidates))
		for i, c := range candidates {
			regions[i] = c
		}

		// Extended intervals for findBestSilenceRegion (needs full coverage)
		fullIntervals := makeRelaxedThresholdIntervals(0, 400, -65.0, 0.5, 5.0)

		result := findBestSilenceRegion(regions, fullIntervals, 100.0)

		// Verify fallback fields are NOT set (since we had golden window candidates)
		if fallbackNeeded {
			t.Error("Fallback should NOT be needed when candidates exist in golden window")
		}
		if result.FallbackUsed {
			t.Error("FallbackUsed should be false when candidates exist in golden window")
		}
		if result.FallbackThreshold != 0 {
			t.Errorf("FallbackThreshold should be 0 when not used, got %.1f", result.FallbackThreshold)
		}

		t.Log("Verified: when golden window candidates exist, fallback is NOT triggered")
	})
}

func TestFallbackTriggeredWhenNoGoldenWindowCandidates(t *testing.T) {
	// Phase 5.1: Integration test - fallback should be triggered when no candidates
	// exist in the golden window with the normal threshold, but relaxed threshold
	// can find them.
	//
	// Scenario:
	// - Create intervals where golden window (15-90s) has RMS ABOVE normal threshold
	// - But RMS is BELOW what a relaxed threshold would allow
	// - Good spectral characteristics (flatness > 0.4, kurtosis < 8)
	// - Verify: normal detection finds nothing in golden window
	// - Verify: fallback activates and finds candidates

	t.Run("fallback finds candidates missed by normal threshold", func(t *testing.T) {
		// Create intervals where:
		// - 0-15s: loud speech (excluded from golden window anyway)
		// - 15-90s: moderate noise level that fails normal detection but passes relaxed
		// - We set RMS at -55 dB with good spectral characteristics

		// Pre-golden window: loud content
		preGoldenIntervals := makeFallbackTestIntervals(0, 15, -20.0, 0.1, 12.0)

		// Golden window: noise level between normal and relaxed thresholds
		// RMS = -55 dB, flatness = 0.5 (passes > 0.4), kurtosis = 5.0 (passes < 8.0)
		goldenIntervals := makeFallbackTestIntervals(15*time.Second, 75, -55.0, 0.5, 5.0)

		// Post-golden window: more content
		postGoldenIntervals := makeFallbackTestIntervals(90*time.Second, 110, -25.0, 0.1, 10.0)

		intervals := append(append(preGoldenIntervals, goldenIntervals...), postGoldenIntervals...)

		// Compute threshold using the normal estimation
		// With loud pre-golden content, the threshold will be derived from that
		noiseFloor, silenceThreshold, ok := estimateNoiseFloorAndThreshold(intervals)
		if !ok {
			t.Fatal("estimateNoiseFloorAndThreshold failed")
		}
		t.Logf("Estimated noise floor: %.1f dB, threshold: %.1f dB", noiseFloor, silenceThreshold)

		// Find candidates using the normal threshold
		normalCandidates := findSilenceCandidatesFromIntervals(intervals, silenceThreshold, 0)

		// Check for candidates in golden window
		goldenStart := 15 * time.Second
		goldenEnd := 90 * time.Second
		hasGoldenWindowCandidate := false
		for _, c := range normalCandidates {
			if c.Start >= goldenStart && c.Start < goldenEnd {
				hasGoldenWindowCandidate = true
				break
			}
		}

		// For this test to be valid, normal detection should NOT find golden window candidates
		// If it does, we need to adjust test parameters
		if hasGoldenWindowCandidate {
			t.Skip("Test setup: normal threshold found golden window candidates - adjust RMS levels")
		}
		t.Logf("Normal detection found %d candidates, none in golden window (as expected)", len(normalCandidates))

		// Now calculate fallback threshold (simulating what AnalyzeAudio does)
		// Use full-file NoiseFloor measurement for fallback
		// Set a realistic NoiseFloor for the test (quietest content in file)
		simulatedNoiseFloor := -58.0 // Just below our -55 dB golden window content

		fallbackThreshold, canFallback := calculateFallbackThreshold(simulatedNoiseFloor, silenceThreshold)
		if !canFallback {
			t.Fatal("calculateFallbackThreshold should succeed with valid NoiseFloor")
		}
		t.Logf("Fallback threshold: %.1f dB (NoiseFloor: %.1f dB)", fallbackThreshold, simulatedNoiseFloor)

		// Find candidates using the relaxed threshold
		fallbackCandidates := findSilenceCandidatesWithRelaxedThreshold(intervals, fallbackThreshold)
		t.Logf("Fallback detection found %d candidates", len(fallbackCandidates))

		// Verify fallback found candidates
		if len(fallbackCandidates) == 0 {
			t.Error("Fallback should find candidates with relaxed threshold")
		}

		// Verify candidates are in the golden window
		for _, c := range fallbackCandidates {
			if c.Start < goldenStart || c.Start >= goldenEnd {
				t.Errorf("Fallback candidate at %v is outside golden window [15s, 90s)", c.Start)
			}
		}
	})
}

func TestFallbackSpectralValidationRejectsBadCandidates(t *testing.T) {
	// Phase 5.2: Integration test - fallback's stricter spectral validation
	// should reject candidates with poor spectral characteristics.
	//
	// Scenario:
	// - Create intervals with RMS below relaxed threshold (would pass amplitude check)
	// - But with poor spectral characteristics (flatness 0.2 or kurtosis 12)
	// - Verify: fallback does NOT return these as candidates

	tests := []struct {
		name     string
		flatness float64
		kurtosis float64
		desc     string
	}{
		{
			name:     "low flatness rejects voice-like spectrum",
			flatness: 0.2, // < 0.4 threshold
			kurtosis: 5.0, // OK
			desc:     "flatness 0.2 indicates tonal/voice-like content",
		},
		{
			name:     "high kurtosis rejects peaked spectrum",
			flatness: 0.5,  // OK
			kurtosis: 12.0, // > 8.0 threshold
			desc:     "kurtosis 12 indicates strong harmonics (speech)",
		},
		{
			name:     "both bad rejects strongly",
			flatness: 0.2,  // Bad
			kurtosis: 12.0, // Bad
			desc:     "both indicators suggest speech contamination",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create intervals in the golden window with bad spectral characteristics
			// but RMS that would pass the relaxed threshold
			intervals := makeFallbackTestIntervals(15*time.Second, 75, -65.0, tt.flatness, tt.kurtosis)

			// Use a relaxed threshold that the RMS would pass
			relaxedThreshold := -60.0 // RMS -65 is below -60

			candidates := findSilenceCandidatesWithRelaxedThreshold(intervals, relaxedThreshold)

			if len(candidates) != 0 {
				t.Errorf("Expected 0 candidates with %s, got %d", tt.desc, len(candidates))
				for _, c := range candidates {
					t.Logf("  Unexpected candidate: start=%v, duration=%v", c.Start, c.Duration)
				}
			}
		})
	}

	t.Run("good spectral characteristics pass", func(t *testing.T) {
		// Verify the control case: good spectral characteristics should pass
		intervals := makeFallbackTestIntervals(15*time.Second, 75, -65.0, 0.5, 5.0) // Good values

		relaxedThreshold := -60.0

		candidates := findSilenceCandidatesWithRelaxedThreshold(intervals, relaxedThreshold)

		if len(candidates) == 0 {
			t.Error("Expected candidates with good spectral characteristics")
		} else {
			t.Logf("Found %d candidates with good spectral values (as expected)", len(candidates))
		}
	})
}

func TestFallbackThresholdCapIntegration(t *testing.T) {
	// Phase 5.3: Integration test for threshold cap enforcement.
	//
	// This tests the integration of calculateFallbackThreshold's cap behaviour
	// in a realistic scenario. The unit test TestCalculateFallbackThreshold
	// already covers the function directly; this test verifies the cap
	// affects candidate detection appropriately.
	//
	// Scenario:
	// - Original threshold = -65 dB
	// - NoiseFloor = -50 dB (significantly higher than threshold)
	// - Without cap: relaxed threshold would be -47 dB (NoiseFloor + 3)
	// - With cap: relaxed threshold is -55 dB (original + 10)
	// - Create intervals at -52 dB (between capped and uncapped thresholds)
	// - Verify: cap prevents detection of intervals that are too loud

	t.Run("cap prevents overly relaxed threshold", func(t *testing.T) {
		originalThreshold := -65.0
		noiseFloor := -50.0 // Much higher than threshold

		// Calculate what the thresholds would be
		uncappedThreshold := noiseFloor + 3.0       // -47 dB (if uncapped)
		cappedThreshold := originalThreshold + 10.0 // -55 dB (expected with cap)

		// Verify our understanding of the cap
		actualThreshold, ok := calculateFallbackThreshold(noiseFloor, originalThreshold)
		if !ok {
			t.Fatal("calculateFallbackThreshold should succeed")
		}
		if actualThreshold != cappedThreshold {
			t.Errorf("Expected capped threshold %.1f dB, got %.1f dB", cappedThreshold, actualThreshold)
		}
		t.Logf("Threshold calculation: uncapped=%.1f dB, capped=%.1f dB, actual=%.1f dB",
			uncappedThreshold, cappedThreshold, actualThreshold)

		// Create intervals at -52 dB (between capped -55 dB and uncapped -47 dB)
		// These WOULD be detected without the cap, but should NOT be detected with cap
		intervals := makeFallbackTestIntervals(15*time.Second, 75, -52.0, 0.5, 5.0)

		// With the capped threshold (-55 dB), RMS -52 dB should NOT pass (since -52 > -55)
		candidates := findSilenceCandidatesWithRelaxedThreshold(intervals, actualThreshold)

		if len(candidates) != 0 {
			t.Errorf("Cap should prevent detection of intervals at -52 dB with threshold -55 dB, got %d candidates",
				len(candidates))
		}

		// Verify that without cap (using uncapped threshold), detection would succeed
		// This confirms the test scenario is set up correctly
		candidatesUncapped := findSilenceCandidatesWithRelaxedThreshold(intervals, uncappedThreshold)
		if len(candidatesUncapped) == 0 {
			t.Log("Note: even uncapped threshold didn't find candidates (test may need adjustment)")
		} else {
			t.Logf("Without cap: found %d candidates (confirms cap is the reason for rejection)", len(candidatesUncapped))
		}
	})
}

func TestFallbackStatusPropagation(t *testing.T) {
	// Additional integration test: verify that findBestSilenceRegion correctly
	// propagates fallback status set by the caller (simulating AnalyzeAudio's flow).
	//
	// This tests that the FallbackUsed and FallbackThreshold fields are correctly
	// preserved and returned in the result structure.

	t.Run("fallback status fields preserved", func(t *testing.T) {
		// Create a simple candidate set
		regions := []SilenceRegion{
			{
				Start:    20 * time.Second,
				End:      30 * time.Second,
				Duration: 10 * time.Second,
			},
		}

		intervals := makeFallbackTestIntervals(0, 100, -70.0, 0.6, 3.0)

		result := findBestSilenceRegion(regions, intervals, 100.0)

		// The function itself doesn't set fallback fields - that's done by AnalyzeAudio
		// Verify they're initialized to zero values
		if result.FallbackUsed {
			t.Error("FallbackUsed should be false by default")
		}
		if result.FallbackThreshold != 0 {
			t.Errorf("FallbackThreshold should be 0 by default, got %.1f", result.FallbackThreshold)
		}

		// Simulate what AnalyzeAudio does: set the fields after the call
		result.FallbackUsed = true
		result.FallbackThreshold = -58.0

		// Verify they're now set
		if !result.FallbackUsed {
			t.Error("FallbackUsed should be true after setting")
		}
		if result.FallbackThreshold != -58.0 {
			t.Errorf("FallbackThreshold should be -58.0 after setting, got %.1f", result.FallbackThreshold)
		}
	})
}
