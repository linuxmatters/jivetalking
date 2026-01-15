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

		result := findBestSpeechRegion(regions, intervals)

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

		result := findBestSpeechRegion([]SpeechRegion{}, intervals)

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

		result := findBestSpeechRegion(regions, intervals)

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
				CrestFactor:      15.0, // Ideal
				SpectralCentroid: 1500.0,
			},
			wantMin: 0.8,
			wantMax: 1.0,
		},
		{
			name: "quiet speech",
			metrics: &SpeechCandidateMetrics{
				Region:           SpeechRegion{Duration: 30 * time.Second},
				RMSLevel:         -28.0,
				CrestFactor:      15.0,
				SpectralCentroid: 1500.0,
			},
			wantMin: 0.3,
			wantMax: 0.7,
		},
		{
			name: "wrong centroid",
			metrics: &SpeechCandidateMetrics{
				Region:           SpeechRegion{Duration: 60 * time.Second},
				RMSLevel:         -15.0,
				CrestFactor:      15.0,
				SpectralCentroid: 8000.0, // Outside voice range
			},
			wantMin: 0.4,
			wantMax: 0.7,
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
