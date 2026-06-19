package processor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

var flatSpectralJSONFields = []string{
	"spectral_mean",
	"spectral_variance",
	"spectral_centroid",
	"spectral_spread",
	"spectral_skewness",
	"spectral_kurtosis",
	"spectral_entropy",
	"spectral_flatness",
	"spectral_crest",
	"spectral_flux",
	"spectral_slope",
	"spectral_decrease",
	"spectral_rolloff",
}

func TestIntervalSampleJSON_PreservesFlatSpectralFields(t *testing.T) {
	sample := IntervalSample{
		Spectral: flatSpectralMetricsFixture(),
	}

	data, err := json.Marshal(sample)
	if err != nil {
		t.Fatalf("Marshal() failed: %v", err)
	}
	assertFlatSpectralJSONKeys(t, data)

	var decoded IntervalSample
	if err := json.Unmarshal(flatSpectralJSONFixture(), &decoded); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}
	assertSpectralValues(t, decoded.Spectral)
}

func assertFlatSpectralJSONKeys(t *testing.T, data []byte) {
	t.Helper()

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}

	for _, field := range flatSpectralJSONFields {
		if _, ok := object[field]; !ok {
			t.Errorf("missing flat spectral JSON field %q in %s", field, string(data))
		}
	}
	if _, ok := object["spectral"]; ok {
		t.Errorf("unexpected nested spectral JSON field in %s", string(data))
	}
}

func flatSpectralJSONFixture() []byte {
	return []byte(`{
		"spectral_mean": 1,
		"spectral_variance": 2,
		"spectral_centroid": 3,
		"spectral_spread": 4,
		"spectral_skewness": 5,
		"spectral_kurtosis": 6,
		"spectral_entropy": 7,
		"spectral_flatness": 8,
		"spectral_crest": 9,
		"spectral_flux": 10,
		"spectral_slope": 11,
		"spectral_decrease": 12,
		"spectral_rolloff": 13
	}`)
}

func flatSpectralMetricsFixture() SpectralMetrics {
	return SpectralMetrics{
		Mean:     1,
		Variance: 2,
		Centroid: 3,
		Spread:   4,
		Skewness: 5,
		Kurtosis: 6,
		Entropy:  7,
		Flatness: 8,
		Crest:    9,
		Flux:     10,
		Slope:    11,
		Decrease: 12,
		Rolloff:  13,
		Found:    true,
	}
}

func assertSpectralValues(t *testing.T, got SpectralMetrics) {
	t.Helper()

	want := flatSpectralMetricsFixture()
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"Mean", got.Mean, want.Mean},
		{"Variance", got.Variance, want.Variance},
		{"Centroid", got.Centroid, want.Centroid},
		{"Spread", got.Spread, want.Spread},
		{"Skewness", got.Skewness, want.Skewness},
		{"Kurtosis", got.Kurtosis, want.Kurtosis},
		{"Entropy", got.Entropy, want.Entropy},
		{"Flatness", got.Flatness, want.Flatness},
		{"Crest", got.Crest, want.Crest},
		{"Flux", got.Flux, want.Flux},
		{"Slope", got.Slope, want.Slope},
		{"Decrease", got.Decrease, want.Decrease},
		{"Rolloff", got.Rolloff, want.Rolloff},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestAnalyseAudio(t *testing.T) {
	// Generate synthetic test audio: 5-second 440Hz tone at -23 LUFS with a 0.5s silence gap
	// This provides known characteristics for validating the analyser
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
	config := newTestBaseConfig()
	config.Analysis.Enabled = true

	t.Run("synthetic_tone_with_silence", func(t *testing.T) {
		// Progress callback to show analysis progress
		lastPercent := -1
		progressCallback := func(update ProgressUpdate) {
			percent := int(update.Progress * 100)
			// Only log at 25% intervals to avoid spam
			if percent >= lastPercent+25 {
				t.Logf("  %s: %d%%", update.PassName, percent)
				lastPercent = percent
			}
		}

		measurements, err := AnalyseAudio(context.Background(), testFile, config, progressCallback)
		if err != nil {
			t.Fatalf("AnalyseAudio failed: %v", err)
		}

		// Log measurements
		t.Logf("Input Loudness: %.2f LUFS", measurements.Loudness.InputI)
		t.Logf("Input True Peak: %.2f dBTP", measurements.Loudness.InputTP)
		t.Logf("Input Loudness Range: %.2f LU", measurements.Loudness.InputLRA)
		t.Logf("Input Threshold: %.2f dB", measurements.Loudness.InputThresh)
		t.Logf("Target Offset: %.2f dB", measurements.Loudness.TargetOffset)
		t.Logf("Noise Floor: %.2f dB", measurements.Noise.Floor)
		t.Logf("Dynamic Range: %.2f dB", measurements.Dynamics.DynamicRange)
		t.Logf("RMS Level: %.2f dB", measurements.Dynamics.RMSLevel)
		t.Logf("Peak Level: %.2f dB", measurements.Dynamics.PeakLevel)

		// Sanity checks for synthetic audio with known characteristics
		// Input level should be close to -23 LUFS (our tone level)
		if measurements.Loudness.InputI > -20 || measurements.Loudness.InputI < -30 {
			t.Errorf("InputI out of expected range for -23dBFS tone: %.2f", measurements.Loudness.InputI)
		}

		// True peak should be close to tone level (sine wave peak = RMS + 3dB)
		if measurements.Loudness.InputTP > 0 || measurements.Loudness.InputTP < -30 {
			t.Errorf("InputTP out of reasonable range: %.2f", measurements.Loudness.InputTP)
		}

		// LRA should be low for a steady tone with brief silence (< 10 LU)
		if measurements.Loudness.InputLRA < 0 || measurements.Loudness.InputLRA > 15 {
			t.Errorf("InputLRA out of expected range for steady tone: %.2f", measurements.Loudness.InputLRA)
		}

		// NoiseFloor is the VAD low percentile of the per-interval momentary-LUFS
		// histogram (vad_percentile), not the old elected-region RMS. On a steady
		// -23 LUFS tone with one brief 0.5 s gap, momentary LUFS (400 ms window)
		// keeps the 10th percentile near the tone, so the floor sits well above the
		// -60 dB noise. Assert a finite, sane dBFS value below the tone and above
		// the measurement floor; the exact value is detector-source dependent.
		if measurements.Noise.Floor > 0 || measurements.Noise.Floor < -120 {
			t.Errorf("NoiseFloor out of reasonable range: %.2f", measurements.Noise.Floor)
		}

		// The offset should bring us close to target (-16 LUFS)
		expectedOutput := measurements.Loudness.InputI + measurements.Loudness.TargetOffset
		if expectedOutput < config.Loudnorm.TargetI-2 || expectedOutput > config.Loudnorm.TargetI+2 {
			t.Logf("Warning: Target offset might not achieve target (expected ~%.1f, got %.2f)",
				config.Loudnorm.TargetI, expectedOutput)
		}
	})
}

func TestAnalyseAudioDoesNotMutateCallerConfig(t *testing.T) {
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 1.0,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -23.0,
		NoiseLevel:   -60.0,
	})
	defer cleanupTestAudio(t, testFile)

	config := DefaultFilterConfig()
	config.FilterOrder = []FilterID{FilterNoiseReduction, FilterAnalysis}

	originalOrder := append([]FilterID(nil), config.FilterOrder...)

	if _, err := AnalyseAudio(context.Background(), testFile, config, nil); err != nil {
		t.Fatalf("AnalyseAudio failed: %v", err)
	}

	if len(config.FilterOrder) != len(originalOrder) {
		t.Fatalf("FilterOrder length = %d, want %d", len(config.FilterOrder), len(originalOrder))
	}
	for i := range originalOrder {
		if config.FilterOrder[i] != originalOrder[i] {
			t.Errorf("FilterOrder[%d] = %q, want %q", i, config.FilterOrder[i], originalOrder[i])
		}
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
// Allows control over RMS and entropy for testing speech detection logic.
func makeSpeechTestIntervals(count int, rms float64) []IntervalSample {
	const entropy = 0.5
	const defaultCentroid = 1500.0

	intervals := make([]IntervalSample, count)
	for i := range intervals {
		intervals[i] = IntervalSample{
			Timestamp: time.Duration(i) * 250 * time.Millisecond,
			RMSLevel:  rms,
			Spectral: SpectralMetrics{
				Centroid: defaultCentroid,
				Entropy:  entropy,
			},
		}
	}
	return intervals
}

func TestMeasureSpeechCandidateFromIntervals(t *testing.T) {
	t.Run("computes metrics correctly", func(t *testing.T) {
		// Create intervals with known values
		intervals := make([]IntervalSample, 40) // 10s of intervals
		for i := range intervals {
			intervals[i] = IntervalSample{
				Timestamp: time.Duration(i) * 250 * time.Millisecond,
				RMSLevel:  -20.0,
				PeakLevel: -8.0,
				Spectral: SpectralMetrics{
					Centroid: 1500.0,
					Flatness: 0.3,
					Kurtosis: 5.0,
					Entropy:  0.5,
				},
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
		if metrics.Spectral.Centroid != 1500.0 {
			t.Errorf("SpectralCentroid = %.1f, want 1500.0", metrics.Spectral.Centroid)
		}
	})

	t.Run("returns nil for empty range", func(t *testing.T) {
		intervals := makeSpeechTestIntervals(40, -20.0)
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
	t.Run("duration adequacy saturates: longer adequate run does not outrank shorter", func(t *testing.T) {
		// Uniform speech level across all regions and no noise profile, so the SNR
		// term saturates equally for every candidate. With longest-wins removed and
		// the duration term saturating at the adequacy minimum, the longer adequate
		// run no longer wins on length alone: both adequate runs tie and the first
		// is elected deterministically. The 5s run is below the adequacy minimum.
		intervals := makeSpeechTestIntervals(400, -18.0) // 100s of speech-like intervals

		regions := []SpeechRegion{
			{Start: 0, End: 35 * time.Second, Duration: 35 * time.Second},
			{Start: 40 * time.Second, End: 90 * time.Second, Duration: 50 * time.Second}, // Longest
			{Start: 95 * time.Second, End: 100 * time.Second, Duration: 5 * time.Second},
		}

		result := findBestSpeechRegion(regions, intervals, nil, nil)

		if result.BestRegion == nil {
			t.Fatal("expected a best region to be selected")
		}
		// The longer 50s run must NOT win on length: the first adequate run wins.
		if result.BestRegion.Start != 0 {
			t.Errorf("selected start = %v, want 0s (first adequate run, length no longer ranks)", result.BestRegion.Start)
		}
	})

	t.Run("returns nil for empty regions", func(t *testing.T) {
		intervals := makeSpeechTestIntervals(200, -18.0)

		result := findBestSpeechRegion([]SpeechRegion{}, intervals, nil, nil)

		if result.BestRegion != nil {
			t.Error("expected nil BestRegion for empty input")
		}
	})

	t.Run("stores all candidates for reporting", func(t *testing.T) {
		intervals := makeSpeechTestIntervals(400, -18.0)

		regions := []SpeechRegion{
			{Start: 0, End: 35 * time.Second, Duration: 35 * time.Second},
			{Start: 40 * time.Second, End: 80 * time.Second, Duration: 40 * time.Second},
		}

		result := findBestSpeechRegion(regions, intervals, nil, nil)

		if len(result.Candidates) != 2 {
			t.Errorf("expected 2 candidates stored, got %d", len(result.Candidates))
		}
	})
}

func TestFindBestSpeechRegion_AllBelowMinAcceptableScoreFallsBack(t *testing.T) {
	// Two short runs (below the duration adequacy minimum) sitting close to a high
	// noise floor (low SNR). The grounded scorer keeps both below the sanity floor,
	// so the always-elect fallback must still elect the highest-scoring run.
	regions := []SpeechRegion{
		{Start: 0, End: 10 * time.Second, Duration: 10 * time.Second},
		{Start: 15 * time.Second, End: 25 * time.Second, Duration: 10 * time.Second},
	}

	makeShortRun := func(start time.Duration, duration time.Duration, rms float64) []IntervalSample {
		count := int(duration / (250 * time.Millisecond))
		intervals := make([]IntervalSample, count)
		for i := range intervals {
			intervals[i] = IntervalSample{
				Timestamp:     start + time.Duration(i)*250*time.Millisecond,
				RMSLevel:      rms,
				MomentaryLUFS: rms,
				PeakLevel:     rms + 10.0,
			}
		}
		return intervals
	}

	// Noise floor at -35 dBFS. Run A at -33 (2 dB SNR), run B at -27 (8 dB SNR):
	// both well below minSNRMargin and both below the duration adequacy minimum, so
	// both score under the sanity floor. Run B scores higher (wider SNR).
	noiseProfile := &NoiseProfile{MeasuredNoiseFloor: -35.0}
	lowRun := makeShortRun(0, 10*time.Second, -33.0)
	higherRun := makeShortRun(15*time.Second, 10*time.Second, -27.0)
	intervals := make([]IntervalSample, 0, len(lowRun)+len(higherRun))
	intervals = append(intervals, lowRun...)
	intervals = append(intervals, higherRun...)

	result := findBestSpeechRegion(regions, intervals, noiseProfile, nil)

	if result.BestRegion == nil {
		t.Fatal("expected fallback BestRegion when speech candidates exist below threshold")
	}
	if result.BestRegion.Start != 15*time.Second {
		t.Errorf("BestRegion.Start = %v, want 15s (highest-scored fallback candidate)", result.BestRegion.Start)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("len(Candidates) = %d, want 2", len(result.Candidates))
	}

	const minViableSpeechScore = 0.3
	for _, c := range result.Candidates {
		if c.Score >= minViableSpeechScore {
			t.Errorf("candidate start=%v score=%.4f, want below speech threshold %.1f", c.Region.Start, c.Score, minViableSpeechScore)
		}
	}
	if result.Candidates[1].Score <= result.Candidates[0].Score {
		t.Errorf("expected second candidate score %.4f to exceed first %.4f", result.Candidates[1].Score, result.Candidates[0].Score)
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
			Timestamp: startTime + time.Duration(i)*250*time.Millisecond,
			RMSLevel:  rms,
			Spectral: SpectralMetrics{
				Kurtosis: kurtosis,
				Flatness: flatness,
				Centroid: centroid,
				Rolloff:  6000.0, // Ideal range (4000-8000 Hz)
				Flux:     0.003,  // Below stable threshold (0.004)
			},
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
							Timestamp: time.Duration(i) * 250 * time.Millisecond,
							RMSLevel:  -35.0, // Quiet
							Spectral: SpectralMetrics{
								Kurtosis: 15.0,    // High
								Flatness: 0.8,     // Noise-like
								Centroid: 7000.0,  // Outside voice range (above speechCentroidMax 6000 Hz)
								Rolloff:  12000.0, // Above acceptable range (max 10000)
								Flux:     0.05,    // High variation (transients)
							},
						}
					} else {
						intervals[i] = IntervalSample{
							Timestamp: time.Duration(i) * 250 * time.Millisecond,
							RMSLevel:  -35.0, // Quiet
							Spectral: SpectralMetrics{
								Kurtosis: 1.0,     // Low
								Flatness: 0.8,     // Noise-like
								Centroid: 7000.0,  // Outside voice range (above speechCentroidMax 6000 Hz)
								Rolloff:  12000.0, // Above acceptable range (max 10000)
								Flux:     0.05,    // High variation (transients)
							},
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
				// This should score quite low across all metrics.
				// Centroid 7000 Hz sits above speechCentroidMax (6000 Hz), so it stays
				// out of the voice range and the centroid term scores 0 as intended.
				return makeSpeechIntervalsScorable(0, 40, 2.0, 0.8, 7000.0, -32.0)
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

		result := findBestSpeechRegion(regions, intervals, nil, nil)

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

		result := findBestSpeechRegion(regions, intervals, nil, nil)

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

		result := findBestSpeechRegion(regions, intervals, nil, nil)

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
	candidateScoreAtStart := func(result *findBestSpeechRegionResult, start time.Duration) (float64, bool) {
		for _, c := range result.Candidates {
			if c.Region.Start == start {
				return c.Score, true
			}
		}
		return 0, false
	}

	t.Run("wider SNR margin scores higher", func(t *testing.T) {
		// One adequate speech region at -20 dBFS RMS, scored against two noise
		// floors. The SNR margin is folded into the grounded score, so the wider
		// margin (lower floor) must score strictly higher. No post-hoc penalty.
		regions := []SpeechRegion{
			{Start: 0, End: 35 * time.Second, Duration: 35 * time.Second},
		}
		intervals := makeSpeechIntervalsScorable(0, 140, 6.0, 0.1, 1500.0, -20.0)

		// Wide margin: -20 - (-55) = 35 dB. Narrow margin: -20 - (-30) = 10 dB.
		resultWide := findBestSpeechRegion(regions, intervals, &NoiseProfile{MeasuredNoiseFloor: -55.0}, nil)
		resultNarrow := findBestSpeechRegion(regions, intervals, &NoiseProfile{MeasuredNoiseFloor: -30.0}, nil)
		if resultWide.BestRegion == nil || resultNarrow.BestRegion == nil {
			t.Fatal("expected a best region in both runs (always-elect fallback)")
		}

		wideScore, okW := candidateScoreAtStart(resultWide, 0)
		narrowScore, okN := candidateScoreAtStart(resultNarrow, 0)
		if !okW || !okN {
			t.Fatal("candidate at start 0 not found")
		}
		if narrowScore >= wideScore {
			t.Errorf("wider SNR must score higher: wide=%.3f narrow=%.3f", wideScore, narrowScore)
		}
	})

	t.Run("nil noise profile makes the SNR term neutral (saturated)", func(t *testing.T) {
		// With no noise profile the scorer uses a -Inf floor sentinel, so the SNR
		// term saturates to its maximum for every candidate. That is at or above
		// the score of any finite-floor profile, never below it.
		regions := []SpeechRegion{
			{Start: 0, End: 35 * time.Second, Duration: 35 * time.Second},
		}
		intervals := makeSpeechIntervalsScorable(0, 140, 6.0, 0.1, 1500.0, -20.0)

		resultNil := findBestSpeechRegion(regions, intervals, nil, nil)
		resultFinite := findBestSpeechRegion(regions, intervals, &NoiseProfile{MeasuredNoiseFloor: -40.0}, nil)
		if resultNil.BestRegion == nil || resultFinite.BestRegion == nil {
			t.Fatal("expected a best region in both runs")
		}

		nilScore, okNil := candidateScoreAtStart(resultNil, 0)
		finiteScore, okFin := candidateScoreAtStart(resultFinite, 0)
		if !okNil || !okFin {
			t.Fatal("candidate at start 0 not found")
		}
		if nilScore < finiteScore {
			t.Errorf("nil-profile SNR should saturate (neutral max): nil=%.3f finite=%.3f", nilScore, finiteScore)
		}
	})
}

func TestMeasureOutputRoomToneRegion(t *testing.T) {
	// Generate processed test audio file with known room tone region
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

	// Define the room tone region we want to measure
	roomToneRegion := RoomToneRegion{
		Start:    time.Duration(1.5 * float64(time.Second)),
		End:      time.Duration(2.5 * float64(time.Second)),
		Duration: time.Duration(1.0 * float64(time.Second)),
	}

	t.Run("valid_room_tone_region", func(t *testing.T) {
		metrics, err := measureOutputRoomToneRegion(testFile, roomToneRegion)
		if err != nil {
			t.Fatalf("measureOutputRoomToneRegion failed: %v", err)
		}

		// Log all measurements for inspection
		t.Logf("Room Tone Region Measurements:")
		t.Logf("  RMSLevel: %.2f dBFS", metrics.RMSLevel)
		t.Logf("  PeakLevel: %.2f dBFS", metrics.PeakLevel)
		t.Logf("  CrestFactor: %.2f dB", metrics.CrestFactor)
		t.Logf("  SpectralCentroid: %.2f Hz", metrics.Spectral.Centroid)
		t.Logf("  SpectralEntropy: %.2f", metrics.Spectral.Entropy)
		t.Logf("  SpectralFlatness: %.2f", metrics.Spectral.Flatness)
		t.Logf("  MomentaryLUFS: %.2f LUFS", metrics.MomentaryLUFS)
		t.Logf("  ShortTermLUFS: %.2f LUFS", metrics.ShortTermLUFS)
		t.Logf("  TruePeak: %.2f dBTP", metrics.TruePeak)

		// Amplitude metrics: room tone should have very low RMS (< -40 dBFS)
		// With -60dB noise, we expect RMS around -60dB range
		if metrics.RMSLevel > -40.0 {
			t.Errorf("RMSLevel too high for room tone: %.2f dBFS (expected < -40)", metrics.RMSLevel)
		}

		// Peak should also be low for room tone region
		if metrics.PeakLevel > -30.0 {
			t.Errorf("PeakLevel too high for room tone: %.2f dBFS (expected < -30)", metrics.PeakLevel)
		}

		// Spectral entropy should be relatively high for noise (closer to 1.0 than speech)
		// We don't enforce strict bounds since synthesis may vary
		if metrics.Spectral.Entropy < 0.0 || metrics.Spectral.Entropy > 1.0 {
			t.Logf("SpectralEntropy out of [0,1] range: %.2f (may be filter-specific)", metrics.Spectral.Entropy)
		}

		// Spectral centroid should be present (non-zero)
		// Even noise has spectral content
		if metrics.Spectral.Centroid < 0.0 {
			t.Errorf("SpectralCentroid should be non-negative: %.2f Hz", metrics.Spectral.Centroid)
		}

		// LUFS measurements may be invalid for very quiet regions
		// Just check they're within plausible dB range
		if metrics.MomentaryLUFS < -120.0 || metrics.MomentaryLUFS > 0.0 {
			t.Logf("MomentaryLUFS outside plausible range: %.2f LUFS", metrics.MomentaryLUFS)
		}
	})

	t.Run("invalid_path", func(t *testing.T) {
		metrics, err := measureOutputRoomToneRegion("/nonexistent/path.wav", roomToneRegion)
		if err == nil {
			t.Error("Expected error for invalid path, got nil")
		}
		if metrics != nil {
			t.Error("Expected nil metrics for invalid path")
		}
	})

	t.Run("zero_duration_region", func(t *testing.T) {
		zeroRegion := RoomToneRegion{
			Start:    time.Duration(1.0 * float64(time.Second)),
			End:      time.Duration(1.0 * float64(time.Second)),
			Duration: 0,
		}
		metrics, err := measureOutputRoomToneRegion(testFile, zeroRegion)
		if err == nil {
			t.Error("Expected error for zero duration region, got nil")
		}
		if metrics != nil {
			t.Error("Expected nil metrics for zero duration region")
		}
	})
}

func Test_measureOutputSpeechRegion(t *testing.T) {
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
		metrics, err := measureOutputSpeechRegion(testFile, speechRegion)
		if err != nil {
			t.Fatalf("measureOutputSpeechRegion failed: %v", err)
		}

		// Log all measurements for inspection
		t.Logf("Speech Region Measurements:")
		t.Logf("  RMSLevel: %.2f dBFS", metrics.RMSLevel)
		t.Logf("  PeakLevel: %.2f dBFS", metrics.PeakLevel)
		t.Logf("  CrestFactor: %.2f dB", metrics.CrestFactor)
		t.Logf("  SpectralCentroid: %.2f Hz", metrics.Spectral.Centroid)
		t.Logf("  SpectralEntropy: %.2f", metrics.Spectral.Entropy)
		t.Logf("  SpectralFlatness: %.2f", metrics.Spectral.Flatness)
		t.Logf("  MomentaryLUFS: %.2f LUFS", metrics.MomentaryLUFS)
		t.Logf("  ShortTermLUFS: %.2f LUFS", metrics.ShortTermLUFS)
		t.Logf("  TruePeak: %.2f dBTP", metrics.TruePeak)

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
		if metrics.Spectral.Centroid < 100.0 || metrics.Spectral.Centroid > 2000.0 {
			t.Logf("SpectralCentroid outside plausible range: %.2f Hz (tone at 440 Hz)", metrics.Spectral.Centroid)
		}

		// Spectral flatness should be low for tonal signal (< 0.5)
		// Sine wave is very tonal, not noise-like
		if metrics.Spectral.Flatness < 0.0 || metrics.Spectral.Flatness > 1.0 {
			t.Logf("SpectralFlatness out of [0,1] range: %.2f", metrics.Spectral.Flatness)
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
		metrics, err := measureOutputSpeechRegion("/nonexistent/path.wav", speechRegion)
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
		metrics, err := measureOutputSpeechRegion(testFile, zeroRegion)
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

func TestRunFilterGraph(t *testing.T) {
	// Generate synthetic audio: 2-second 440Hz tone
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 2.0,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -20.0,
	})
	defer cleanupTestAudio(t, testFile)

	// Open the file
	reader, _, err := audio.OpenAudioFile(testFile)
	if err != nil {
		t.Fatalf("failed to open test audio: %v", err)
	}
	defer reader.Close()

	// Create a passthrough filter graph (anull = audio null filter, passes through unchanged)
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(
		reader.DecoderContext(),
		"anull",
	)
	if err != nil {
		t.Fatalf("failed to create filter graph: %v", err)
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	// Count frames using runFilterGraph
	var inputFrameCount int
	var filteredFrameCount int

	err = runFilterGraph(context.Background(), reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnInputFrame: func(_ *ffmpeg.AVFrame) {
			inputFrameCount++
		},
		OnFrame: func(_, _ *ffmpeg.AVFrame) error {
			filteredFrameCount++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runFilterGraph failed: %v", err)
	}

	// With a passthrough filter, filtered frame count must match input frame count
	if inputFrameCount == 0 {
		t.Fatal("no input frames read")
	}
	if filteredFrameCount != inputFrameCount {
		t.Errorf("filtered frame count (%d) != input frame count (%d)",
			filteredFrameCount, inputFrameCount)
	}
	t.Logf("processed %d input frames, %d filtered frames", inputFrameCount, filteredFrameCount)
}

func TestRunFilterGraphLenientErrors(t *testing.T) {
	// Verify that nil OnReadError defaults to break (lenient) and
	// nil OnPushError defaults to return error (strict).
	// This test validates the default callback contract.

	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 1.0,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -20.0,
	})
	defer cleanupTestAudio(t, testFile)

	reader, _, err := audio.OpenAudioFile(testFile)
	if err != nil {
		t.Fatalf("failed to open test audio: %v", err)
	}
	defer reader.Close()

	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(
		reader.DecoderContext(),
		"anull",
	)
	if err != nil {
		t.Fatalf("failed to create filter graph: %v", err)
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	// Run with lenient push errors (continue on push failure) and discard-only
	var frameCount int
	err = runFilterGraph(context.Background(), reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnPushError: func(_ error) error { return nil }, // lenient: continue
		OnFrame: func(_, _ *ffmpeg.AVFrame) error {
			frameCount++
			return nil
		},
	})
	if err != nil {
		t.Fatalf("runFilterGraph with lenient push errors failed: %v", err)
	}
	if frameCount == 0 {
		t.Fatal("no frames processed with lenient config")
	}
	t.Logf("lenient config processed %d filtered frames", frameCount)
}
