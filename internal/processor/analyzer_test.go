package processor

import (
	"context"
	"encoding/json"
	"math"
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

func TestAudioMeasurementsJSON_PreservesFlatSpectralFields(t *testing.T) {
	measurements := AudioMeasurements{}
	measurements.Spectral = flatSpectralMetricsFixture()

	data, err := json.Marshal(measurements)
	if err != nil {
		t.Fatalf("Marshal() failed: %v", err)
	}
	assertFlatSpectralJSONKeys(t, data)

	var decoded AudioMeasurements
	if err := json.Unmarshal(flatSpectralJSONFixture(), &decoded); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}
	assertSpectralValues(t, decoded.Spectral)
}

func TestAudioMeasurementsJSON_PreservesInputFields(t *testing.T) {
	measurements := AudioMeasurements{
		BaseMeasurements: BaseMeasurements{
			DynamicRange: 14.5,
			Spectral:     flatSpectralMetricsFixture(),
		},
		InputI:                 -23.4,
		InputTP:                -1.2,
		InputLRA:               5.6,
		InputThresh:            -34.0,
		TargetOffset:           1.1,
		NoiseFloor:             -62.0,
		NoiseFloorSource:       "astats",
		PreScanNoiseFloor:      -63.0,
		RoomToneDetectLevel:    -55.0,
		RoomToneRegions:        []RoomToneRegion{{Start: time.Second, End: 2 * time.Second, Duration: time.Second}},
		RoomToneCandidates:     []RoomToneCandidateMetrics{{RMSLevel: -58.0, Spectral: flatSpectralMetricsFixture()}},
		IntervalSamples:        []IntervalSample{{Timestamp: 250 * time.Millisecond, RMSLevel: -48.0, Spectral: flatSpectralMetricsFixture()}},
		SpeechRegions:          []SpeechRegion{{Start: 3 * time.Second, End: 4 * time.Second, Duration: time.Second}},
		SpeechProfile:          &SpeechCandidateMetrics{RMSLevel: -24.0, Spectral: flatSpectralMetricsFixture()},
		VoiceActivated:         true,
		NoiseProfile:           &NoiseProfile{Start: time.Second, Duration: time.Second, MeasuredNoiseFloor: -62.0},
		SuggestedGateThreshold: 0.025,
		NoiseReductionHeadroom: 12.5,
	}

	data, err := json.Marshal(measurements)
	if err != nil {
		t.Fatalf("Marshal() failed: %v", err)
	}
	assertJSONField(t, data, "input_i")
	assertJSONField(t, data, "noise_floor_source")
	assertJSONField(t, data, "interval_samples")
	assertJSONField(t, data, "speech_profile")
	assertJSONField(t, data, "noise_profile")
	assertJSONField(t, data, "room_tone_detect_level")
	assertJSONField(t, data, "room_tone_regions")
	assertJSONField(t, data, "room_tone_candidates")
	assertJSONFieldAbsent(t, data, "silence_detect_level")
	assertJSONFieldAbsent(t, data, "silence_regions")
	assertJSONFieldAbsent(t, data, "silence_candidates")

	var decoded AudioMeasurements
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}
	if decoded.InputI != measurements.InputI {
		t.Errorf("InputI = %v, want %v", decoded.InputI, measurements.InputI)
	}
	if decoded.NoiseFloorSource != measurements.NoiseFloorSource {
		t.Errorf("NoiseFloorSource = %q, want %q", decoded.NoiseFloorSource, measurements.NoiseFloorSource)
	}
	if len(decoded.IntervalSamples) != 1 {
		t.Fatalf("IntervalSamples length = %d, want 1", len(decoded.IntervalSamples))
	}
	if !decoded.IntervalSamples[0].Spectral.Found {
		t.Fatal("expected interval sample spectral metrics to preserve Found")
	}
	if decoded.SpeechProfile == nil {
		t.Fatal("expected SpeechProfile to round-trip")
	}
	if decoded.NoiseProfile == nil {
		t.Fatal("expected NoiseProfile to round-trip")
	}
	if !decoded.VoiceActivated {
		t.Fatal("expected VoiceActivated to round-trip")
	}
}

func TestOutputMeasurementsJSON_PreservesFlatSpectralFields(t *testing.T) {
	measurements := OutputMeasurements{}
	measurements.Spectral = flatSpectralMetricsFixture()

	data, err := json.Marshal(measurements)
	if err != nil {
		t.Fatalf("Marshal() failed: %v", err)
	}
	assertFlatSpectralJSONKeys(t, data)

	var decoded OutputMeasurements
	if err := json.Unmarshal(flatSpectralJSONFixture(), &decoded); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}
	assertSpectralValues(t, decoded.Spectral)
}

func TestOutputMeasurementsJSON_PreservesOutputFields(t *testing.T) {
	measurements := OutputMeasurements{
		BaseMeasurements: BaseMeasurements{
			DynamicRange: 10.5,
			Spectral:     flatSpectralMetricsFixture(),
		},
		OutputI:              -16.0,
		OutputTP:             -1.0,
		OutputLRA:            4.0,
		OutputThresh:         -26.0,
		TargetOffset:         0.3,
		LoudnormInputI:       -18.0,
		LoudnormInputTP:      -1.5,
		LoudnormInputLRA:     5.0,
		LoudnormInputThresh:  -28.0,
		LoudnormTargetOffset: 1.2,
		LoudnormMeasured:     true,
		RoomToneSample:       &RoomToneCandidateMetrics{RMSLevel: -58.0, Spectral: flatSpectralMetricsFixture()},
		SpeechSample:         &SpeechCandidateMetrics{RMSLevel: -22.0, Spectral: flatSpectralMetricsFixture()},
	}

	data, err := json.Marshal(measurements)
	if err != nil {
		t.Fatalf("Marshal() failed: %v", err)
	}
	assertJSONField(t, data, "output_i")
	assertJSONField(t, data, "loudnorm_input_i")
	assertJSONField(t, data, "loudnorm_measured")
	assertJSONField(t, data, "room_tone_sample")
	assertJSONField(t, data, "speech_sample")
	assertJSONFieldAbsent(t, data, "silence_sample")

	var decoded OutputMeasurements
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}
	if decoded.OutputI != measurements.OutputI {
		t.Errorf("OutputI = %v, want %v", decoded.OutputI, measurements.OutputI)
	}
	if decoded.LoudnormInputI != measurements.LoudnormInputI {
		t.Errorf("LoudnormInputI = %v, want %v", decoded.LoudnormInputI, measurements.LoudnormInputI)
	}
	if !decoded.LoudnormMeasured {
		t.Fatal("expected LoudnormMeasured to round-trip")
	}
	if decoded.RoomToneSample == nil {
		t.Fatal("expected RoomToneSample to round-trip")
	}
	if decoded.SpeechSample == nil {
		t.Fatal("expected SpeechSample to round-trip")
	}
}

func TestBaseMeasurements_HasNoFlatSpectralPrimitiveFields(t *testing.T) {
	assertNoStaleSpectralPrimitiveFields[BaseMeasurements](t)
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

func assertJSONField(t *testing.T, data []byte, field string) {
	t.Helper()

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}
	if _, ok := object[field]; !ok {
		t.Errorf("missing JSON field %q in %s", field, string(data))
	}
}

func assertJSONFieldAbsent(t *testing.T, data []byte, field string) {
	t.Helper()

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}
	if _, ok := object[field]; ok {
		t.Errorf("unexpected JSON field %q in %s", field, string(data))
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

		measurements, err := AnalyzeAudio(context.Background(), testFile, config, progressCallback)
		if err != nil {
			t.Fatalf("AnalyzeAudio failed: %v", err)
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
		if expectedOutput < config.Loudnorm.TargetI-2 || expectedOutput > config.Loudnorm.TargetI+2 {
			t.Logf("Warning: Target offset might not achieve target (expected ~%.1f, got %.2f)",
				config.Loudnorm.TargetI, expectedOutput)
		}
	})
}

func TestAnalyzeAudioDoesNotMutateCallerConfig(t *testing.T) {
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 1.0,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -23.0,
		NoiseLevel:   -60.0,
	})
	defer cleanupTestAudio(t, testFile)

	config := DefaultFilterConfig()
	config.FilterOrder = []FilterID{FilterNoiseRemove, FilterAnalysis}
	config.Analysis.RoomToneScanDuration = 250 * time.Millisecond

	originalOrder := append([]FilterID(nil), config.FilterOrder...)
	originalRoomToneScanDuration := config.Analysis.RoomToneScanDuration

	if _, err := AnalyzeAudio(context.Background(), testFile, config, nil); err != nil {
		t.Fatalf("AnalyzeAudio failed: %v", err)
	}

	if len(config.FilterOrder) != len(originalOrder) {
		t.Fatalf("FilterOrder length = %d, want %d", len(config.FilterOrder), len(originalOrder))
	}
	for i := range originalOrder {
		if config.FilterOrder[i] != originalOrder[i] {
			t.Errorf("FilterOrder[%d] = %q, want %q", i, config.FilterOrder[i], originalOrder[i])
		}
	}
	if config.Analysis.RoomToneScanDuration != originalRoomToneScanDuration {
		t.Errorf("Analysis.RoomToneScanDuration = %v, want %v", config.Analysis.RoomToneScanDuration, originalRoomToneScanDuration)
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
		candidate     *RoomToneRegion
		intervals     []IntervalSample
		wantStart     time.Duration
		wantDuration  time.Duration
		wantUnchanged bool // If true, expect original region returned
		desc          string
	}{
		{
			name: "short candidate - no refinement needed",
			candidate: &RoomToneRegion{
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
			candidate: &RoomToneRegion{
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
			candidate: &RoomToneRegion{
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
			candidate: &RoomToneRegion{
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
			candidate: &RoomToneRegion{
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
			candidate: &RoomToneRegion{
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
// Two-Pass Room Tone Candidate Selection Tests
// ============================================================================

// makeRoomToneTestIntervals creates interval samples for testing findBestRoomToneRegion.
// All intervals share the same spectral/amplitude values for predictable scoring.
func makeRoomToneTestIntervals(start time.Duration, duration time.Duration, rms, peak, centroid, flatness, kurtosis, flux float64) []IntervalSample {
	count := int(duration / (250 * time.Millisecond))
	intervals := make([]IntervalSample, count)
	for i := range intervals {
		intervals[i] = IntervalSample{
			Timestamp: start + time.Duration(i)*250*time.Millisecond,
			RMSLevel:  rms,
			PeakLevel: peak,
			Spectral: SpectralMetrics{
				Centroid: centroid,
				Flatness: flatness,
				Kurtosis: kurtosis,
				Flux:     flux,
			},
		}
	}
	return intervals
}

func TestFindBestRoomToneRegion_HighestScoreWinsAfterDip(t *testing.T) {
	// Three candidates with a scoring dip in the middle (B). Under a single-pass
	// algorithm the dip from A to B would trigger early termination, electing A.
	// The two-pass algorithm scores all candidates first and then selects the
	// highest, so C should win.
	//
	// Amplitude now rewards proximity to the candidate-population RMS median
	// (clusterP50). With RMS {A:-78, B:-50, C:-66} the median is C's -66, so C
	// sits exactly on the cluster and earns the top amplitude term; A is a quiet
	// outlier (12 dB below the cluster) and B is the loud dip.

	regions := []RoomToneRegion{
		{Start: 0, End: 10 * time.Second, Duration: 10 * time.Second},
		{Start: 15 * time.Second, End: 25 * time.Second, Duration: 10 * time.Second},
		{Start: 30 * time.Second, End: 40 * time.Second, Duration: 10 * time.Second},
	}

	// Candidate A intervals: quiet outlier, far below the cluster median
	intervalsA := makeRoomToneTestIntervals(0, 10*time.Second,
		-78.0, -73.0, 100.0, 0.9, 1.0, 0.0)
	// Candidate B intervals: the dip (loud, voice-range centroid, poor spectral)
	intervalsB := makeRoomToneTestIntervals(15*time.Second, 10*time.Second,
		-50.0, -45.0, 2375.0, 0.3, 5.0, 0.005)
	// Candidate C intervals: on the cluster median, strong spectral - highest
	intervalsC := makeRoomToneTestIntervals(30*time.Second, 10*time.Second,
		-66.0, -61.0, 100.0, 0.9, 1.0, 0.0)

	allIntervals := append(append(intervalsA, intervalsB...), intervalsC...)

	result := findBestRoomToneRegion(regions, allIntervals, nil)

	if result.BestRegion == nil {
		t.Fatal("expected a best region to be selected")
	}
	if result.BestRegion.Start != 30*time.Second {
		t.Errorf("BestRegion.Start = %v, want 30s (highest-scoring candidate C)", result.BestRegion.Start)
	}
	if len(result.Candidates) != 3 {
		t.Errorf("len(Candidates) = %d, want 3 (all three scored)", len(result.Candidates))
	}

	// Log scores for debugging
	for _, c := range result.Candidates {
		t.Logf("candidate start=%v score=%.4f", c.Region.Start, c.Score)
	}
}

func TestFindBestRoomToneRegion_EarlierCandidatePreferredWithinTolerance(t *testing.T) {
	// Two candidates with scores differing by < 0.02 (within selectionTolerance).
	// The earlier candidate should be preferred.

	regions := []RoomToneRegion{
		{Start: 5 * time.Second, End: 15 * time.Second, Duration: 10 * time.Second},
		{Start: 30 * time.Second, End: 40 * time.Second, Duration: 10 * time.Second},
	}

	// Candidate A intervals: ~0.85
	intervalsA := makeRoomToneTestIntervals(5*time.Second, 10*time.Second,
		-68.0, -63.0, 100.0, 0.8, 2.0, 0.001)
	// Candidate B intervals: ~0.86 (slightly higher, within tolerance)
	intervalsB := makeRoomToneTestIntervals(30*time.Second, 10*time.Second,
		-69.0, -64.0, 100.0, 0.85, 1.5, 0.0)

	allIntervals := make([]IntervalSample, 0, len(intervalsA)+len(intervalsB))
	allIntervals = append(allIntervals, intervalsA...)
	allIntervals = append(allIntervals, intervalsB...)

	result := findBestRoomToneRegion(regions, allIntervals, nil)

	if result.BestRegion == nil {
		t.Fatal("expected a best region to be selected")
	}

	// Log scores to verify the gap is within tolerance
	for _, c := range result.Candidates {
		t.Logf("candidate start=%v score=%.4f", c.Region.Start, c.Score)
	}

	if result.BestRegion.Start != 5*time.Second {
		t.Errorf("BestRegion.Start = %v, want 5s (earlier candidate preferred within tolerance)", result.BestRegion.Start)
	}
}

func TestFindBestRoomToneRegion_OnClusterCandidateClearsThresholdDespitePoorSpectral(t *testing.T) {
	// Under proximity-to-cluster-P50 amplitude scoring, the candidate sitting on the
	// candidate-population median always earns the full amplitude term (1.0). Two poor
	// candidates that the former monotonic reward scored below minAcceptableScore (0.3)
	// now both sit AT the cluster median (identical RMS), so the amplitude term alone
	// (0.30) clears the threshold and the earliest is elected via the primary branch.
	//
	// This documents a deliberate consequence of the amplitude change: with proximity
	// scoring there is always an on-cluster survivor scoring >= amplitudeScoreWeight, so
	// the "all surviving candidates below minAcceptableScore" state is no longer reachable
	// for non-rejected candidates. Structural rejection (crosstalk / digital silence) is
	// covered separately by TestFindBestRoomToneRegion_AllRejectedCandidatesDoNotFallback.

	regions := []RoomToneRegion{
		{Start: 0, End: 10 * time.Second, Duration: 10 * time.Second},
		{Start: 15 * time.Second, End: 25 * time.Second, Duration: 10 * time.Second},
	}

	// Both candidates identical and poor on spectral/stability: high RMS, voice-range
	// centroid, flatness 0.0, kurtosis 9.0 (below crosstalk threshold 10), high flux 0.03.
	// Both sit at clusterP50 (-40.5) so amplitude = 1.0; score = 0.30 + spec + dur + stab.
	intervalsA := makeRoomToneTestIntervals(0, 10*time.Second,
		-40.5, -35.5, 2375.0, 0.0, 9.0, 0.03)
	intervalsB := makeRoomToneTestIntervals(15*time.Second, 10*time.Second,
		-40.5, -35.5, 2375.0, 0.0, 9.0, 0.03)

	allIntervals := make([]IntervalSample, 0, len(intervalsA)+len(intervalsB))
	allIntervals = append(allIntervals, intervalsA...)
	allIntervals = append(allIntervals, intervalsB...)

	result := findBestRoomToneRegion(regions, allIntervals, nil)

	if result.BestRegion == nil {
		t.Fatal("expected the on-cluster candidate to clear minAcceptableScore and be elected")
	}
	if result.BestRegion.Start != 0 {
		t.Errorf("BestRegion.Start = %v, want 0 (earliest on-cluster candidate within tolerance)", result.BestRegion.Start)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("len(Candidates) = %d, want 2", len(result.Candidates))
	}
	for _, c := range result.Candidates {
		if c.Score < minAcceptableScore {
			t.Errorf("candidate start=%v score=%.4f, want >= minAcceptableScore %.1f (amplitude proximity = 1.0)", c.Region.Start, c.Score, minAcceptableScore)
		}
	}

	// Log scores for debugging
	for _, c := range result.Candidates {
		t.Logf("candidate start=%v score=%.4f", c.Region.Start, c.Score)
	}
}

func TestFindBestRoomToneRegion_AllRejectedCandidatesDoNotFallback(t *testing.T) {
	regions := []RoomToneRegion{
		{Start: 0, End: 10 * time.Second, Duration: 10 * time.Second},
		{Start: 15 * time.Second, End: 25 * time.Second, Duration: 10 * time.Second},
	}

	intervalsA := makeRoomToneTestIntervals(0, 10*time.Second,
		-120.0, -120.0, 100.0, 0.8, 2.0, 0.0)
	intervalsB := makeRoomToneTestIntervals(15*time.Second, 10*time.Second,
		-120.0, -120.0, 100.0, 0.8, 2.0, 0.0)

	allIntervals := make([]IntervalSample, 0, len(intervalsA)+len(intervalsB))
	allIntervals = append(allIntervals, intervalsA...)
	allIntervals = append(allIntervals, intervalsB...)

	result := findBestRoomToneRegion(regions, allIntervals, nil)

	if result.BestRegion != nil {
		t.Fatalf("BestRegion = %+v, want nil for all rejected candidates", result.BestRegion)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("len(Candidates) = %d, want 2", len(result.Candidates))
	}
	for _, c := range result.Candidates {
		if c.Score != 0.0 {
			t.Errorf("candidate start=%v score=%.4f, want 0.0", c.Region.Start, c.Score)
		}
	}
}

func TestFindBestRoomToneRegion_NoFallbackBranchNeeded(t *testing.T) {
	// Documents that the primary election branch always elects when any candidate
	// survives the structural gates, so the former lower fallback branch was dead.
	//
	// clusterP50 is the median RMS of gate-surviving candidates, and medianFloat64
	// returns an actual member of that set. That member scores calculateAmplitudeScore
	// = 1.0 (dev = 0), contributing amplitudeScoreWeight (0.30) >= minAcceptableScore
	// (0.30). So at least one survivor always clears the primary branch's threshold.
	//
	// The candidates here have varied RMS around a cluster (median -68), all clean
	// enough to survive the gates. The earliest-within-tolerance pick is elected by
	// the primary branch; no fallback is consulted.

	regions := []RoomToneRegion{
		{Start: 0, End: 10 * time.Second, Duration: 10 * time.Second},
		{Start: 15 * time.Second, End: 25 * time.Second, Duration: 10 * time.Second},
		{Start: 30 * time.Second, End: 40 * time.Second, Duration: 10 * time.Second},
	}

	// Varied RMS around the cluster median (-68). All share strong spectral/stability
	// metrics so they survive the gates and score comparably (within selectionTolerance),
	// making the earliest the elected pick.
	intervalsA := makeRoomToneTestIntervals(0, 10*time.Second,
		-67.0, -62.0, 100.0, 0.9, 1.0, 0.0)
	intervalsB := makeRoomToneTestIntervals(15*time.Second, 10*time.Second,
		-68.0, -63.0, 100.0, 0.9, 1.0, 0.0)
	intervalsC := makeRoomToneTestIntervals(30*time.Second, 10*time.Second,
		-69.0, -64.0, 100.0, 0.9, 1.0, 0.0)

	allIntervals := make([]IntervalSample, 0, len(intervalsA)+len(intervalsB)+len(intervalsC))
	allIntervals = append(allIntervals, intervalsA...)
	allIntervals = append(allIntervals, intervalsB...)
	allIntervals = append(allIntervals, intervalsC...)

	result := findBestRoomToneRegion(regions, allIntervals, nil)

	if result.BestRegion == nil {
		t.Fatal("expected the primary branch to elect a region when candidates survive the gates")
	}
	if len(result.Candidates) != 3 {
		t.Fatalf("len(Candidates) = %d, want 3", len(result.Candidates))
	}

	maxScore := 0.0
	for _, c := range result.Candidates {
		if c.Score > maxScore {
			maxScore = c.Score
		}
		t.Logf("candidate start=%v score=%.4f", c.Region.Start, c.Score)
	}

	// The elected region must equal the earliest candidate within selectionTolerance
	// of the maximum score - the same pick the primary loop computes.
	var wantStart time.Duration
	for _, c := range result.Candidates {
		if c.Score >= maxScore-selectionTolerance && c.Score >= minAcceptableScore {
			wantStart = c.Region.Start
			break
		}
	}
	if result.BestRegion.Start != wantStart {
		t.Errorf("BestRegion.Start = %v, want %v (earliest within tolerance, primary branch)", result.BestRegion.Start, wantStart)
	}
}

func TestFindBestRoomToneRegion_SingleAcceptableCandidateElected(t *testing.T) {
	// A single region with an acceptable score should be elected.

	regions := []RoomToneRegion{
		{Start: 0, End: 10 * time.Second, Duration: 10 * time.Second},
	}

	// Acceptable score: RMS -65, centroid outside voice range, good flatness
	intervals := makeRoomToneTestIntervals(0, 10*time.Second,
		-65.0, -60.0, 100.0, 0.8, 2.0, 0.0)

	result := findBestRoomToneRegion(regions, intervals, nil)

	if result.BestRegion == nil {
		t.Fatal("expected a best region to be selected")
	}
	if result.BestRegion.Start != 0 {
		t.Errorf("BestRegion.Start = %v, want 0", result.BestRegion.Start)
	}
	if len(result.Candidates) != 1 {
		t.Errorf("len(Candidates) = %d, want 1", len(result.Candidates))
	}

	t.Logf("single candidate score=%.4f", result.Candidates[0].Score)
}

func TestFindBestRoomToneRegion_LaterCandidateWinsWhenGapExceedsTolerance(t *testing.T) {
	// Two candidates where the score gap far exceeds selectionTolerance (0.02).
	// The later, higher-scoring candidate should win.

	regions := []RoomToneRegion{
		{Start: 5 * time.Second, End: 15 * time.Second, Duration: 10 * time.Second},
		{Start: 30 * time.Second, End: 40 * time.Second, Duration: 10 * time.Second},
	}

	// Candidate A intervals: quiet outlier, 12 dB below the cluster median (amplitude 0)
	intervalsA := makeRoomToneTestIntervals(5*time.Second, 10*time.Second,
		-72.0, -67.0, 100.0, 0.8, 2.0, 0.0)
	// Candidate B intervals: on the cluster median - top amplitude, strong spectral
	intervalsB := makeRoomToneTestIntervals(30*time.Second, 10*time.Second,
		-60.0, -55.0, 100.0, 0.9, 1.0, 0.0)

	allIntervals := make([]IntervalSample, 0, len(intervalsA)+len(intervalsB))
	allIntervals = append(allIntervals, intervalsA...)
	allIntervals = append(allIntervals, intervalsB...)

	result := findBestRoomToneRegion(regions, allIntervals, nil)

	if result.BestRegion == nil {
		t.Fatal("expected a best region to be selected")
	}

	// Log scores to verify the gap exceeds tolerance
	for _, c := range result.Candidates {
		t.Logf("candidate start=%v score=%.4f", c.Region.Start, c.Score)
	}

	if result.BestRegion.Start != 30*time.Second {
		t.Errorf("BestRegion.Start = %v, want 30s (later candidate wins when gap exceeds tolerance)", result.BestRegion.Start)
	}
}

func TestFindBestRoomToneRegion_LateCandidateDiscoverable(t *testing.T) {
	// A high-scoring candidate placed at 50% of totalDuration (1800s into a 3600s recording)
	// is correctly elected when it is the only candidate above minAcceptableScore.
	// Selection evaluates candidates by score regardless of temporal position, with no
	// excludeFirstSeconds, silenceSearchPercent, or candidateCutoffPercent gate.

	regions := []RoomToneRegion{
		{Start: 1800 * time.Second, End: 1810 * time.Second, Duration: 10 * time.Second},
	}

	// High-scoring room tone candidate: low RMS, low centroid, high flatness, low kurtosis, no flux
	intervals := makeRoomToneTestIntervals(1800*time.Second, 10*time.Second,
		-75.0, -70.0, 100.0, 0.9, 1.0, 0.0)

	result := findBestRoomToneRegion(regions, intervals, nil)

	if result.BestRegion == nil {
		t.Fatal("expected candidate at t=1800s to be elected")
	}
	if result.BestRegion.Start != 1800*time.Second {
		t.Errorf("BestRegion.Start = %v, want 1800s", result.BestRegion.Start)
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("len(Candidates) = %d, want 1", len(result.Candidates))
	}

	t.Logf("late candidate score=%.4f", result.Candidates[0].Score)
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

func TestSpeechScore(t *testing.T) {
	tests := []struct {
		name         string
		interval     IntervalSample
		rmsP50       float64
		speechRMSMin float64
		wantMin      float64
		wantMax      float64
	}{
		{
			name: "typical speech",
			interval: IntervalSample{
				RMSLevel: -18.0,
				Spectral: SpectralMetrics{
					Centroid: 1500.0, // Voice range
					Entropy:  0.5,
				},
			},
			rmsP50:       -20.0,
			speechRMSMin: -40.0,
			wantMin:      0.4,
			wantMax:      1.0,
		},
		{
			name: "silence (too quiet)",
			interval: IntervalSample{
				RMSLevel: -50.0,
				Spectral: SpectralMetrics{
					Centroid: 1500.0,
					Entropy:  0.5,
				},
			},
			rmsP50:       -20.0,
			speechRMSMin: -40.0,
			wantMin:      0.0,
			wantMax:      0.0,
		},
		{
			name: "noise (wrong centroid)",
			interval: IntervalSample{
				RMSLevel: -18.0,
				Spectral: SpectralMetrics{
					Centroid: 8000.0, // Outside voice range
					Entropy:  0.9,    // High entropy
				},
			},
			rmsP50:       -20.0,
			speechRMSMin: -40.0,
			wantMin:      0.0,
			wantMax:      0.4,
		},
		{
			name: "at RMS minimum threshold",
			interval: IntervalSample{
				RMSLevel: -40.0, // Exactly at speechRMSMinimum
				Spectral: SpectralMetrics{
					Centroid: 1500.0,
					Entropy:  0.5,
				},
			},
			rmsP50:       -45.0,
			speechRMSMin: -40.0,
			wantMin:      0.3,
			wantMax:      0.85,
		},
		{
			name: "just below RMS minimum",
			interval: IntervalSample{
				RMSLevel: -40.1, // Just below threshold
				Spectral: SpectralMetrics{
					Centroid: 1500.0,
					Entropy:  0.5,
				},
			},
			rmsP50:       -45.0,
			speechRMSMin: -40.0,
			wantMin:      0.0,
			wantMax:      0.0,
		},
		{
			name: "low entropy structured speech",
			interval: IntervalSample{
				RMSLevel: -18.0,
				Spectral: SpectralMetrics{
					Centroid: 2000.0,
					Entropy:  0.2, // Very structured
				},
			},
			rmsP50:       -20.0,
			speechRMSMin: -40.0,
			wantMin:      0.5,
			wantMax:      1.0,
		},
		{
			name: "adaptive threshold rejects quiet interval",
			interval: IntervalSample{
				RMSLevel: -35.0,
				Spectral: SpectralMetrics{
					Centroid: 1500.0,
					Entropy:  0.5,
				},
			},
			rmsP50:       -20.0,
			speechRMSMin: -32.0, // Adaptive threshold above interval RMS
			wantMin:      0.0,
			wantMax:      0.0,
		},
		{
			name: "default threshold admits same interval",
			interval: IntervalSample{
				RMSLevel: -35.0,
				Spectral: SpectralMetrics{
					Centroid: 1500.0,
					Entropy:  0.5,
				},
			},
			rmsP50:       -40.0,
			speechRMSMin: -40.0, // Default threshold below interval RMS
			wantMin:      0.01,
			wantMax:      1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := speechScore(tt.interval, tt.rmsP50, tt.speechRMSMin)
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("speechScore() = %.2f, want [%.2f, %.2f]", score, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestComputeSpeechRMSMinimum(t *testing.T) {
	tests := []struct {
		name       string
		rmsLevel   float64
		noiseFloor float64
		want       float64
	}{
		{
			name:       "RMS-12 wins over NoiseFloor+6",
			rmsLevel:   -20.0,
			noiseFloor: -55.0,
			want:       -32.0,
		},
		{
			name:       "NoiseFloor+6 wins over RMS-12",
			rmsLevel:   -20.0,
			noiseFloor: -35.0,
			want:       -29.0,
		},
		{
			name:       "fallback on zero measurements",
			rmsLevel:   0.0,
			noiseFloor: 0.0,
			want:       -40.0,
		},
		{
			name:       "fallback on -Inf noise floor",
			rmsLevel:   -20.0,
			noiseFloor: math.Inf(-1),
			want:       -40.0,
		},
		{
			name:       "fallback on -Inf RMS level",
			rmsLevel:   math.Inf(-1),
			noiseFloor: -50.0,
			want:       -40.0,
		},
		{
			name:       "Anna scenario from proposal",
			rmsLevel:   -48.7,
			noiseFloor: -70.0,
			want:       -60.7,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeSpeechRMSMinimum(tt.rmsLevel, tt.noiseFloor)
			if math.Abs(got-tt.want) > 0.001 {
				t.Errorf("computeSpeechRMSMinimum(%v, %v) = %v, want %v", tt.rmsLevel, tt.noiseFloor, got, tt.want)
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
				Timestamp: startTime + time.Duration(i)*250*time.Millisecond,
				RMSLevel:  rms,
				Spectral: SpectralMetrics{
					Centroid: 1500.0,
					Entropy:  0.3, // Low entropy for better scores (~0.65 entropyScore)
				},
			}
		}
		return intervals
	}

	t.Run("finds 30s speech region", func(t *testing.T) {
		// 10s room tone + 2.5min speech (600 intervals)
		// With 25% above threshold, this gives ~150 speech intervals (above 120 minimum)
		// Search starts at 12s, so we lose 8 intervals, leaving 592 → ~148 speech intervals
		roomToneIntervals := makeVariedSpeechIntervals(0, 40, false)            // 10s room tone
		speechIntervals := makeVariedSpeechIntervals(10*time.Second, 600, true) // 2.5min speech
		intervals := make([]IntervalSample, 0, len(roomToneIntervals)+len(speechIntervals))
		intervals = append(intervals, roomToneIntervals...)
		intervals = append(intervals, speechIntervals...)

		candidates := findSpeechCandidatesFromIntervals(intervals, 10*time.Second, false, -20.0, -55.0)

		if len(candidates) == 0 {
			t.Fatal("expected at least one speech candidate")
		}
		if candidates[0].Duration < 30*time.Second {
			t.Errorf("duration %v < 30s minimum", candidates[0].Duration)
		}
	})

	t.Run("no candidates for short speech", func(t *testing.T) {
		// 10s room tone + 4s speech (too short - only 16 intervals, below 40 minimum).
		// findSpeechCandidatesFromIntervals needs >= minimumSpeechIntervals samples,
		// so pad with room tone after the speech to clear the length guard while
		// keeping the speech run itself under the 10s gate.
		roomToneIntervals := makeVariedSpeechIntervals(0, 40, false)
		speechIntervals := makeVariedSpeechIntervals(10*time.Second, 16, true) // 4s speech
		tailIntervals := makeVariedSpeechIntervals(14*time.Second, 40, false)  // room-tone tail
		intervals := make([]IntervalSample, 0, len(roomToneIntervals)+len(speechIntervals)+len(tailIntervals))
		intervals = append(intervals, roomToneIntervals...)
		intervals = append(intervals, speechIntervals...)
		intervals = append(intervals, tailIntervals...)

		candidates := findSpeechCandidatesFromIntervals(intervals, 10*time.Second, false, -20.0, -55.0)

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

		candidates := findSpeechCandidatesFromIntervals(intervals, 5*time.Second, false, -20.0, -55.0)

		if len(candidates) == 0 {
			t.Fatal("expected speech candidate bridging pause")
		}
		// Combined region should be substantial
		if candidates[0].Duration < 100*time.Second {
			t.Errorf("expected bridged duration >100s, got %v", candidates[0].Duration)
		}
	})

	t.Run("respects room-tone end boundary", func(t *testing.T) {
		// Speech before and after the elected room-tone region end - only speech after should be detected
		// Need 480+ intervals for late speech to have enough high-scoring intervals
		earlyIntervals := makeVariedSpeechIntervals(0, 200, true)             // 50s speech at start
		lateIntervals := makeVariedSpeechIntervals(60*time.Second, 500, true) // 125s speech later
		intervals := make([]IntervalSample, 0, len(earlyIntervals)+len(lateIntervals))
		intervals = append(intervals, earlyIntervals...)
		intervals = append(intervals, lateIntervals...)

		// Search starts at 50s (after early speech ends)
		candidates := findSpeechCandidatesFromIntervals(intervals, 50*time.Second, false, -20.0, -55.0)

		if len(candidates) == 0 {
			t.Fatal("expected speech candidate after room-tone region end")
		}
		// First candidate should start after 50s + 2s buffer = 52s
		if candidates[0].Start < 52*time.Second {
			t.Errorf("speech start %v should be after room-tone region end + buffer (52s)", candidates[0].Start)
		}
	})

	t.Run("insufficient intervals returns nil", func(t *testing.T) {
		// Only 30 intervals (7.5s) - less than minimum 40 (10s)
		intervals := makeVariedSpeechIntervals(0, 30, true)

		candidates := findSpeechCandidatesFromIntervals(intervals, 0, false, -20.0, -55.0)

		if candidates != nil {
			t.Errorf("expected nil for insufficient intervals, got %d candidates", len(candidates))
		}
	})

	t.Run("long pause breaks speech region", func(t *testing.T) {
		// Speech with 3s pause in middle (exceeds 2s tolerance, should break).
		// Each segment is 8s (32 intervals), below the 10s / 40-interval gate, so
		// neither qualifies alone once the pause splits the run.
		speech1 := makeVariedSpeechIntervals(10*time.Second, 32, true) // 8s
		pause := makeVariedSpeechIntervals(18*time.Second, 12, false)  // 3s pause (> tolerance)
		speech2 := makeVariedSpeechIntervals(21*time.Second, 32, true) // 8s more
		intervals := append(append(speech1, pause...), speech2...)

		candidates := findSpeechCandidatesFromIntervals(intervals, 5*time.Second, false, -20.0, -55.0)

		// Neither segment alone meets the 10s minimum, so no candidates expected
		if len(candidates) != 0 {
			t.Errorf("expected no candidates (pause breaks region), got %d", len(candidates))
		}
	})

	t.Run("voice-activated bridges digital silence gap", func(t *testing.T) {
		// Two speech segments separated by a 7.5s digital silence gap (30 intervals).
		// Default tolerance (8 intervals / 2s) would split; widened (40 / 10s) bridges.
		speech1 := makeVariedSpeechIntervals(10*time.Second, 300, true)         // 75s speech
		gap := makeVariedSpeechIntervals(85*time.Second, 30, false)             // 7.5s digital silence
		speech2 := makeVariedSpeechIntervals(92500*time.Millisecond, 300, true) // 75s speech
		intervals := append(append(speech1, gap...), speech2...)

		// Default tolerance: gap exceeds 2s, splits into two regions
		defaultCandidates := findSpeechCandidatesFromIntervals(intervals, 5*time.Second, false, -20.0, -55.0)
		// Widened tolerance: gap within 10s, bridges into one region
		widenedCandidates := findSpeechCandidatesFromIntervals(intervals, 5*time.Second, true, -20.0, -55.0)

		if len(defaultCandidates) < 2 {
			t.Fatalf("expected default tolerance to split gap into 2+ regions, got %d", len(defaultCandidates))
		}

		if len(widenedCandidates) != 1 {
			t.Fatalf("expected widened tolerance to bridge gap into 1 region, got %d", len(widenedCandidates))
		}

		if widenedCandidates[0].Duration < 140*time.Second {
			t.Errorf("expected bridged duration >140s, got %v", widenedCandidates[0].Duration)
		}
	})

	t.Run("voice-activated does not bridge gaps beyond 10s", func(t *testing.T) {
		// Two speech segments separated by a 12.5s gap (50 intervals).
		// Even widened tolerance (40 intervals) should not bridge this.
		speech1 := makeVariedSpeechIntervals(10*time.Second, 300, true)         // 75s speech
		gap := makeVariedSpeechIntervals(85*time.Second, 50, false)             // 12.5s gap
		speech2 := makeVariedSpeechIntervals(97500*time.Millisecond, 300, true) // 75s speech
		intervals := append(append(speech1, gap...), speech2...)

		candidates := findSpeechCandidatesFromIntervals(intervals, 5*time.Second, true, -20.0, -55.0)

		if len(candidates) < 2 {
			t.Errorf("expected gap >10s to split even with widened tolerance, got %d candidates", len(candidates))
		}
	})
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
	t.Run("selects longest qualifying candidate", func(t *testing.T) {
		// Create intervals covering multiple regions
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
		// Should select the 50s region (longest)
		if result.BestRegion.Duration != 50*time.Second {
			t.Errorf("selected duration = %v, want 50s", result.BestRegion.Duration)
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
	regions := []SpeechRegion{
		{Start: 0, End: 30 * time.Second, Duration: 30 * time.Second},
		{Start: 35 * time.Second, End: 65 * time.Second, Duration: 30 * time.Second},
	}

	makePoorSpeechIntervals := func(start time.Duration, duration time.Duration, rolloff float64) []IntervalSample {
		count := int(duration / (250 * time.Millisecond))
		intervals := make([]IntervalSample, count)
		for i := range intervals {
			intervals[i] = IntervalSample{
				Timestamp: start + time.Duration(i)*250*time.Millisecond,
				RMSLevel:  -35.0,
				PeakLevel: -10.0,
				Spectral: SpectralMetrics{
					Kurtosis: 2.0,
					Centroid: 8000.0,
					Rolloff:  rolloff,
					Flux:     0.05,
				},
			}
		}
		return intervals
	}

	lowScoreIntervals := makePoorSpeechIntervals(0, 30*time.Second, 12000.0)
	higherScoreIntervals := makePoorSpeechIntervals(35*time.Second, 30*time.Second, 6000.0)
	intervals := make([]IntervalSample, 0, len(lowScoreIntervals)+len(higherScoreIntervals))
	intervals = append(intervals, lowScoreIntervals...)
	intervals = append(intervals, higherScoreIntervals...)

	result := findBestSpeechRegion(regions, intervals, nil, nil)

	if result.BestRegion == nil {
		t.Fatal("expected fallback BestRegion when speech candidates exist below threshold")
	}
	if result.BestRegion.Start != 35*time.Second {
		t.Errorf("BestRegion.Start = %v, want 35s (highest-scored fallback candidate)", result.BestRegion.Start)
	}
	if len(result.Candidates) != 2 {
		t.Fatalf("len(Candidates) = %d, want 2", len(result.Candidates))
	}

	const minAcceptableSpeechScore = 0.3
	for _, c := range result.Candidates {
		if c.Score >= minAcceptableSpeechScore {
			t.Errorf("candidate start=%v score=%.4f, want below speech threshold %.1f", c.Region.Start, c.Score, minAcceptableSpeechScore)
		}
	}
	if result.Candidates[1].Score <= result.Candidates[0].Score {
		t.Errorf("expected second candidate score %.4f to exceed first %.4f", result.Candidates[1].Score, result.Candidates[0].Score)
	}
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
				Region:         SpeechRegion{Duration: 60 * time.Second},
				RMSLevel:       -15.0,
				CrestFactor:    12.0, // Ideal (crestFactorIdeal)
				Spectral:       SpectralMetrics{Centroid: 1500.0, Rolloff: 6000.0, Flux: 0.003},
				VoicingDensity: 0.75, // High voiced content (above 0.6 threshold)
			},
			wantMin: 0.8,
			wantMax: 1.0,
		},
		{
			name: "quiet speech",
			metrics: &SpeechCandidateMetrics{
				Region:         SpeechRegion{Duration: 30 * time.Second},
				RMSLevel:       -28.0,
				CrestFactor:    12.0, // Ideal
				Spectral:       SpectralMetrics{Centroid: 1500.0, Rolloff: 6000.0, Flux: 0.003},
				VoicingDensity: 0.75, // High voiced content
			},
			wantMin: 0.3,
			wantMax: 0.8,
		},
		{
			name: "wrong centroid",
			metrics: &SpeechCandidateMetrics{
				Region:         SpeechRegion{Duration: 60 * time.Second},
				RMSLevel:       -15.0,
				CrestFactor:    12.0, // Ideal
				Spectral:       SpectralMetrics{Centroid: 8000.0, Rolloff: 6000.0, Flux: 0.003},
				VoicingDensity: 0.75, // High voiced content
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
								Centroid: 6000.0,  // Outside voice range
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
								Centroid: 6000.0,  // Outside voice range
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
		resultGood := findBestSpeechRegion(regions, intervals, noiseProfileGood, nil)
		if resultGood.BestRegion == nil {
			t.Fatal("expected a best region to be selected with good SNR")
		}

		// Create noise profile with noise floor at -30 dBFS
		// SNR margin = -20 - (-30) = 10 dB < 20 dB minSNRMargin
		noiseProfileBad := &NoiseProfile{
			MeasuredNoiseFloor: -30.0, // 10 dB margin - should be penalised
		}

		// Run with poor SNR margin
		resultBad := findBestSpeechRegion(regions, intervals, noiseProfileBad, nil)
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
		resultNoProfile := findBestSpeechRegion(regions, intervals, nil, nil)
		if resultNoProfile.BestRegion == nil {
			t.Fatal("expected a best region to be selected without noise profile")
		}

		// Create a "good" noise profile for comparison
		// SNR margin = -20 - (-40) = 20 dB = exactly minSNRMargin
		noiseProfileGood := &NoiseProfile{
			MeasuredNoiseFloor: -40.0, // 20 dB margin - passes
		}
		resultWithProfile := findBestSpeechRegion(regions, intervals, noiseProfileGood, nil)

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

		// Verify region is captured correctly
		if metrics.Region.Start != roomToneRegion.Start {
			t.Errorf("Region start mismatch: got %v, want %v", metrics.Region.Start, roomToneRegion.Start)
		}
		if metrics.Region.Duration != roomToneRegion.Duration {
			t.Errorf("Region duration mismatch: got %v, want %v", metrics.Region.Duration, roomToneRegion.Duration)
		}

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

// ============================================================================
// Stability Score Tests
// ============================================================================

func TestCalculateStabilityScore(t *testing.T) {
	tests := []struct {
		name      string
		intervals []IntervalSample
		wantMin   float64
		wantMax   float64
		desc      string
	}{
		{
			name:      "empty list returns neutral",
			intervals: []IntervalSample{},
			wantMin:   0.5,
			wantMax:   0.5,
			desc:      "insufficient data returns 0.5 neutral score",
		},
		{
			name: "single interval returns neutral",
			intervals: []IntervalSample{
				{RMSLevel: -50.0, Spectral: SpectralMetrics{Flux: 0.0}},
			},
			wantMin: 0.5,
			wantMax: 0.5,
			desc:    "insufficient data (need >= 2) returns 0.5 neutral score",
		},
		{
			name: "uniform intervals - perfect stability",
			intervals: []IntervalSample{
				{RMSLevel: -50.0, Spectral: SpectralMetrics{Flux: 0.0}},
				{RMSLevel: -50.0, Spectral: SpectralMetrics{Flux: 0.0}},
				{RMSLevel: -50.0, Spectral: SpectralMetrics{Flux: 0.0}},
				{RMSLevel: -50.0, Spectral: SpectralMetrics{Flux: 0.0}},
			},
			wantMin: 0.99,
			wantMax: 1.0,
			desc:    "zero variance, zero flux = perfect stability score",
		},
		{
			name: "uniform RMS with low flux - high stability",
			intervals: []IntervalSample{
				{RMSLevel: -60.0, Spectral: SpectralMetrics{Flux: 0.001}},
				{RMSLevel: -60.0, Spectral: SpectralMetrics{Flux: 0.002}},
				{RMSLevel: -60.0, Spectral: SpectralMetrics{Flux: 0.001}},
				{RMSLevel: -60.0, Spectral: SpectralMetrics{Flux: 0.002}},
			},
			wantMin: 0.90,
			wantMax: 1.0,
			desc:    "zero RMS variance, low flux = near-perfect stability",
		},
		{
			name: "variable RMS - lower stability",
			intervals: []IntervalSample{
				{RMSLevel: -45.0, Spectral: SpectralMetrics{Flux: 0.0}},
				{RMSLevel: -55.0, Spectral: SpectralMetrics{Flux: 0.0}},
				{RMSLevel: -48.0, Spectral: SpectralMetrics{Flux: 0.0}},
				{RMSLevel: -52.0, Spectral: SpectralMetrics{Flux: 0.0}},
			},
			wantMin: 0.35,
			wantMax: 0.45,
			desc:    "10dB spread in RMS (variance ~14.5 dB²) exceeds 9 dB² threshold, only flux contributes",
		},
		{
			name: "high spectral flux - lower stability",
			intervals: []IntervalSample{
				{RMSLevel: -50.0, Spectral: SpectralMetrics{Flux: 0.03}},
				{RMSLevel: -50.0, Spectral: SpectralMetrics{Flux: 0.04}},
				{RMSLevel: -50.0, Spectral: SpectralMetrics{Flux: 0.025}},
				{RMSLevel: -50.0, Spectral: SpectralMetrics{Flux: 0.035}},
			},
			wantMin: 0.0,
			wantMax: 0.65,
			desc:    "zero RMS variance but flux > 0.02 threshold reduces score",
		},
		{
			name: "variable RMS and high flux - poor stability",
			intervals: []IntervalSample{
				{RMSLevel: -45.0, Spectral: SpectralMetrics{Flux: 0.03}},
				{RMSLevel: -55.0, Spectral: SpectralMetrics{Flux: 0.04}},
				{RMSLevel: -48.0, Spectral: SpectralMetrics{Flux: 0.025}},
				{RMSLevel: -52.0, Spectral: SpectralMetrics{Flux: 0.035}},
			},
			wantMin: 0.0,
			wantMax: 0.5,
			desc:    "both high RMS variance and high flux = poor stability",
		},
		{
			name: "extreme variance - near-zero stability",
			intervals: []IntervalSample{
				{RMSLevel: -30.0, Spectral: SpectralMetrics{Flux: 0.05}},
				{RMSLevel: -70.0, Spectral: SpectralMetrics{Flux: 0.08}},
				{RMSLevel: -35.0, Spectral: SpectralMetrics{Flux: 0.06}},
				{RMSLevel: -65.0, Spectral: SpectralMetrics{Flux: 0.07}},
			},
			wantMin: 0.0,
			wantMax: 0.2,
			desc:    "40dB spread and very high flux = minimal stability",
		},
		{
			name: "two intervals - minimum valid input",
			intervals: []IntervalSample{
				{RMSLevel: -50.0, Spectral: SpectralMetrics{Flux: 0.005}},
				{RMSLevel: -50.0, Spectral: SpectralMetrics{Flux: 0.005}},
			},
			wantMin: 0.85,
			wantMax: 1.0,
			desc:    "two identical intervals should score high",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := calculateStabilityScore(tt.intervals)

			// Verify score is bounded 0-1
			if score < 0.0 || score > 1.0 {
				t.Errorf("score %.3f outside [0, 1] range", score)
			}

			// Verify score is within expected range
			if score < tt.wantMin || score > tt.wantMax {
				t.Errorf("calculateStabilityScore() = %.3f, want [%.2f, %.2f] (%s)",
					score, tt.wantMin, tt.wantMax, tt.desc)
			}
		})
	}
}

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
		reader.GetDecoderContext(),
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
		reader.GetDecoderContext(),
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

func TestFindBestRoomToneRegion_BoundaryTransientSurvivesAfterRefinement(t *testing.T) {
	// An 18s candidate where the first 3s contain a boundary transient (speech-to-room-tone
	// transition) and the remaining 15s are clean room tone. Without pre-scoring refinement,
	// the spiking transient intervals would trip the RMS-dispersion gate and the candidate
	// would score 0.0. With refinement, the golden sub-region trims the transient before scoring.

	region := RoomToneRegion{
		Start:    10 * time.Second,
		End:      28 * time.Second,
		Duration: 18 * time.Second,
	}
	regions := []RoomToneRegion{region}

	// First 3s: boundary transient - high peak (simulating speech tail), elevated RMS
	transientIntervals := makeRoomToneTestIntervals(
		10*time.Second, 3*time.Second,
		-50.0, -10.0, // RMS -50, peak -10: crest factor 40 dB per interval
		100.0, 0.8, 2.0, 0.0,
	)

	// Remaining 15s: clean room tone - low RMS, low peak, good spectral characteristics
	cleanIntervals := makeRoomToneTestIntervals(
		13*time.Second, 15*time.Second,
		-70.0, -65.0, // RMS -70, peak -65: crest factor 5 dB per interval
		100.0, 0.9, 1.0, 0.0,
	)

	allIntervals := make([]IntervalSample, 0, len(transientIntervals)+len(cleanIntervals))
	allIntervals = append(allIntervals, transientIntervals...)
	allIntervals = append(allIntervals, cleanIntervals...)

	result := findBestRoomToneRegion(regions, allIntervals, nil)

	if result.BestRegion == nil {
		t.Fatal("expected candidate to be elected after pre-scoring refinement trims boundary transient")
	}

	// Verify the candidate was refined
	if len(result.Candidates) == 0 {
		t.Fatal("expected candidates to be populated")
	}

	foundRefined := false
	for _, c := range result.Candidates {
		if c.WasRefined {
			foundRefined = true

			if c.OriginalStart != 10*time.Second {
				t.Errorf("OriginalStart = %v, want 10s", c.OriginalStart)
			}
			if c.OriginalDuration != 18*time.Second {
				t.Errorf("OriginalDuration = %v, want 18s", c.OriginalDuration)
			}

			// Refined region should exclude the transient at 10-13s
			if c.Region.Start < 13*time.Second {
				t.Errorf("Refined region start %v overlaps transient zone (before 13s)", c.Region.Start)
			}

			// Refinement should trim the transient, leaving a low crest on the clean sub-region
			if c.CrestFactor > 25.0 {
				t.Errorf("Refined crest factor = %.1f dB, want < 25.0 dB", c.CrestFactor)
			}

			// Score should be above minAcceptableScore (0.3) since the clean section is good
			if c.Score < 0.3 {
				t.Errorf("Score = %.4f, want >= 0.3 (candidate should pass after refinement)", c.Score)
			}

			break
		}
	}

	if !foundRefined {
		t.Error("expected WasRefined=true for candidate with boundary transient")
	}
}

// makeRoomToneIntervalsFromRMS builds a contiguous interval sequence from explicit
// per-interval RMS levels, so tests can construct regions with internal RMS dispersion
// (a spiking interval) or perfectly steady regions. Peak is set 5 dB above each RMS;
// spectral fields use clean room-tone defaults (low centroid, high flatness, low kurtosis).
func makeRoomToneIntervalsFromRMS(start time.Duration, rmsLevels []float64) []IntervalSample {
	intervals := make([]IntervalSample, len(rmsLevels))
	for i, rms := range rmsLevels {
		intervals[i] = IntervalSample{
			Timestamp: start + time.Duration(i)*250*time.Millisecond,
			RMSLevel:  rms,
			PeakLevel: rms + 5.0,
			Spectral: SpectralMetrics{
				Centroid: 100.0,
				Flatness: 0.9,
				Kurtosis: 1.0,
				Flux:     0.0,
			},
		}
	}
	return intervals
}

func TestFindBestRoomToneRegion_ProximityElectsClusterNotQuietOutlier(t *testing.T) {
	// Regression for the popey-EP83 defect: a quiet bright outlier (~-79) must not be
	// elected over a representative cluster (~-74). With proximity-to-cluster-P50
	// amplitude scoring, the cluster candidates sit on the population median and the
	// quiet outlier is penalised for departing from it by more than roomToneAmplitudeDecayDB.

	// Regions: two cluster members (~-74) and one quiet bright outlier (~-79).
	regions := []RoomToneRegion{
		{Start: 0, End: 10 * time.Second, Duration: 10 * time.Second},
		{Start: 15 * time.Second, End: 25 * time.Second, Duration: 10 * time.Second},
		{Start: 30 * time.Second, End: 40 * time.Second, Duration: 10 * time.Second},
	}

	// Two on-cluster candidates at -74 (steady) define clusterP50 = -74.
	clusterA := makeRoomToneTestIntervals(0, 10*time.Second,
		-74.0, -69.0, 100.0, 0.9, 1.0, 0.0)
	clusterB := makeRoomToneTestIntervals(15*time.Second, 10*time.Second,
		-74.0, -69.0, 100.0, 0.9, 1.0, 0.0)
	// Quiet outlier at -79, 5 dB below the cluster (within the 6 dB decay span but
	// strictly farther from P50 than the on-cluster members, which sit at dev 0).
	outlier := makeRoomToneTestIntervals(30*time.Second, 10*time.Second,
		-79.0, -74.0, 100.0, 0.9, 1.0, 0.0)

	allIntervals := make([]IntervalSample, 0, len(clusterA)+len(clusterB)+len(outlier))
	allIntervals = append(allIntervals, clusterA...)
	allIntervals = append(allIntervals, clusterB...)
	allIntervals = append(allIntervals, outlier...)

	result := findBestRoomToneRegion(regions, allIntervals, nil)

	if result.BestRegion == nil {
		t.Fatal("expected a best region to be selected")
	}
	for _, c := range result.Candidates {
		t.Logf("candidate start=%v rms=%.1f score=%.4f", c.Region.Start, c.RMSLevel, c.Score)
	}

	// The elected region must be a cluster member (0s or 15s), never the quiet outlier (30s).
	if result.BestRegion.Start == 30*time.Second {
		t.Errorf("BestRegion.Start = 30s (quiet outlier elected), want a cluster member at 0s or 15s")
	}

	// The outlier must score strictly lower than the on-cluster candidates.
	var clusterScore, outlierScore float64
	for _, c := range result.Candidates {
		if c.Region.Start == 0 {
			clusterScore = c.Score
		}
		if c.Region.Start == 30*time.Second {
			outlierScore = c.Score
		}
	}
	if outlierScore >= clusterScore {
		t.Errorf("outlier score %.4f >= cluster score %.4f, want outlier strictly lower", outlierScore, clusterScore)
	}
}

func TestExceedsRMSDispersion_SpikingIntervalRejected(t *testing.T) {
	// A region with one interval spiking well beyond 4*MAD from the region RMS median
	// must trip the dispersion gate. Steady -74 floor with a single -50 spike interval.
	rmsLevels := []float64{-74, -74, -74, -74, -50, -74, -74, -74, -74, -74}
	intervals := makeRoomToneIntervalsFromRMS(0, rmsLevels)

	maxDev, exceeds := exceedsRMSDispersion(intervals)
	if !exceeds {
		t.Errorf("exceedsRMSDispersion = false (maxDev %.2f MADs), want true for a spiking interval", maxDev)
	}
	if maxDev <= roomToneDispersionMADs {
		t.Errorf("maxDev = %.2f MADs, want > %.1f", maxDev, roomToneDispersionMADs)
	}
}

func TestExceedsRMSDispersion_SteadyRegionPasses(t *testing.T) {
	// A perfectly steady region has MAD ~= 0; the epsilon floor must keep it passing
	// rather than rejecting any tiny variation. Tests both a flat region and one with
	// negligible jitter within the epsilon floor.
	t.Run("perfectly flat", func(t *testing.T) {
		intervals := makeRoomToneIntervalsFromRMS(0,
			[]float64{-74, -74, -74, -74, -74, -74, -74, -74})
		if maxDev, exceeds := exceedsRMSDispersion(intervals); exceeds {
			t.Errorf("exceedsRMSDispersion = true (maxDev %.2f MADs), want false for a flat region", maxDev)
		}
	})

	t.Run("negligible jitter", func(t *testing.T) {
		// Sub-epsilon dispersion: 0.01 dB jitter, far below roomToneDispersionMADEpsilon.
		intervals := makeRoomToneIntervalsFromRMS(0,
			[]float64{-74.00, -74.01, -74.00, -73.99, -74.00, -74.01, -74.00, -73.99})
		if maxDev, exceeds := exceedsRMSDispersion(intervals); exceeds {
			t.Errorf("exceedsRMSDispersion = true (maxDev %.2f MADs), want false for negligible jitter", maxDev)
		}
	})
}

func TestFindBestRoomToneRegion_QuietHighCrestRegionAccepted(t *testing.T) {
	// A representative quiet region with high crest factor (RMS -74, peak -44, crest 30)
	// was formerly hard-rejected by the fixed silenceCrestFactorMax = 25 dB cap. The
	// dispersion gate replaces the cap: because every interval shares the same RMS, the
	// region is steady (MAD ~= 0) and must now be ACCEPTED despite the high crest.

	region := RoomToneRegion{Start: 0, End: 10 * time.Second, Duration: 10 * time.Second}
	regions := []RoomToneRegion{region}

	// Uniform RMS -74 with peak -44 on every interval: crest 30 dB, but zero RMS dispersion.
	intervals := makeRoomToneTestIntervals(0, 10*time.Second,
		-74.0, -44.0, 100.0, 0.9, 1.0, 0.0)

	result := findBestRoomToneRegion(regions, intervals, nil)

	if result.BestRegion == nil {
		t.Fatal("expected the steady high-crest region to be accepted (dispersion gate, not crest cap)")
	}
	if len(result.Candidates) != 1 {
		t.Fatalf("len(Candidates) = %d, want 1", len(result.Candidates))
	}

	c := result.Candidates[0]
	t.Logf("candidate rms=%.1f crest=%.1f score=%.4f warning=%q", c.RMSLevel, c.CrestFactor, c.Score, c.TransientWarning)

	if c.CrestFactor <= 25.0 {
		t.Errorf("CrestFactor = %.1f dB, want > 25 dB (the region the old cap would reject)", c.CrestFactor)
	}
	if c.Score <= 0.0 {
		t.Errorf("Score = %.4f, want > 0 (steady region must not be rejected)", c.Score)
	}
}

// inBandCrestCandidate builds a measured candidate that is in the voice centroid band
// and below the kurtosis crosstalk threshold, so the population-relative crest-outlier
// test is the sole crosstalk discriminator. RMS is kept above the digital-silence floor
// so the candidate enters the crest population.
func inBandCrestCandidate(rms, crest float64) *RoomToneCandidateMetrics {
	return &RoomToneCandidateMetrics{
		RMSLevel:    rms,
		PeakLevel:   rms + crest,
		CrestFactor: crest,
		Spectral: SpectralMetrics{
			Centroid: 3000.0, // inside voiceCentroidMin..voiceCentroidMax
			Kurtosis: 5.0,    // below crosstalkKurtosisThreshold (10)
			Flatness: 0.5,
		},
	}
}

func TestIsLikelyCrosstalk_PopulationRelativeCrestOutlier(t *testing.T) {
	// The fixed 15 dB crest cap was replaced by a population-relative crest-outlier gate:
	// reject an in-band candidate only when its crest exceeds the candidate-population
	// crest median by more than roomToneCrosstalkCrestMADs (4) MADs. A quiet broadband
	// region whose crest is high only because it is quiet - but not an outlier for its
	// population - must NOT be flagged; a genuine crest outlier must be flagged.

	// Population: a tight cluster of in-band crests around ~30 dB (a typical quiet-room-tone
	// population where crest is elevated by quietness, not contamination), plus one extreme
	// outlier. Crest values: 28, 29, 30, 30, 31, 32 and an outlier at 60.
	measured := []*RoomToneCandidateMetrics{
		inBandCrestCandidate(-70, 28),
		inBandCrestCandidate(-70, 29),
		inBandCrestCandidate(-70, 30),
		inBandCrestCandidate(-70, 30),
		inBandCrestCandidate(-70, 31),
		inBandCrestCandidate(-70, 32),
		inBandCrestCandidate(-70, 60), // genuine outlier
	}

	crestMedian, crestMAD := computeCrestOutlierStats(measured)
	limit := crestMedian + roomToneCrosstalkCrestMADs*crestMAD
	t.Logf("crestMedian=%.2f crestMAD=%.2f limit=%.2f", crestMedian, crestMAD, limit)

	t.Run("quiet high-crest non-outlier accepted", func(t *testing.T) {
		// A quiet broadband region with crest 32 dB (above the median, not a population
		// outlier, well under the old 15 dB cap). Must NOT be flagged crosstalk.
		c := inBandCrestCandidate(-74, 32)
		if c.CrestFactor <= crestMedian {
			t.Fatalf("test setup: crest %.1f should be above median %.1f", c.CrestFactor, crestMedian)
		}
		if isLikelyCrosstalk(c, crestMedian, crestMAD, nil) {
			t.Errorf("isLikelyCrosstalk = true for quiet in-band crest %.1f dB (median %.1f, limit %.1f), want false",
				c.CrestFactor, crestMedian, limit)
		}
	})

	t.Run("genuine crest outlier flagged", func(t *testing.T) {
		// Crest 60 dB sits far beyond median + 4*MAD. Must be flagged crosstalk.
		c := inBandCrestCandidate(-70, 60)
		if c.CrestFactor <= limit {
			t.Fatalf("test setup: outlier crest %.1f should exceed limit %.1f", c.CrestFactor, limit)
		}
		if !isLikelyCrosstalk(c, crestMedian, crestMAD, nil) {
			t.Errorf("isLikelyCrosstalk = false for crest outlier %.1f dB (median %.1f, limit %.1f), want true",
				c.CrestFactor, crestMedian, limit)
		}
	})
}

func TestComputeCrestOutlierStats_UniformPopulationEpsilonGuard(t *testing.T) {
	// A uniform-crest population has MAD ~= 0. Without the epsilon floor, any above-median
	// candidate would be flagged (median + 4*0 = median). The floor must keep an above-median
	// candidate passing unless it is a genuine outlier.
	measured := []*RoomToneCandidateMetrics{
		inBandCrestCandidate(-70, 30),
		inBandCrestCandidate(-70, 30),
		inBandCrestCandidate(-70, 30),
		inBandCrestCandidate(-70, 30),
		inBandCrestCandidate(-70, 30),
	}

	crestMedian, crestMAD := computeCrestOutlierStats(measured)
	if crestMAD < roomToneCrosstalkCrestMADEpsilon {
		t.Errorf("crestMAD = %.4f, want >= epsilon %.4f (floor applied)", crestMAD, roomToneCrosstalkCrestMADEpsilon)
	}

	// A candidate marginally above the uniform median must NOT be flagged: median 30, MAD
	// floored to 0.05, limit = 30 + 4*0.05 = 30.2. Crest 30.1 is above median but under limit.
	c := inBandCrestCandidate(-74, 30.1)
	if isLikelyCrosstalk(c, crestMedian, crestMAD, nil) {
		t.Errorf("isLikelyCrosstalk = true for crest %.2f in uniform population (median %.2f, MAD %.4f), want false",
			c.CrestFactor, crestMedian, crestMAD)
	}
}

func TestDetectVoiceActivated(t *testing.T) {
	// Helper to build a candidate slice with a given number of digital silence
	// and non-digital-silence candidates.
	makeCandidates := func(digitalSilence, other int, otherWarning string) []RoomToneCandidateMetrics {
		candidates := make([]RoomToneCandidateMetrics, 0, digitalSilence+other)
		for range digitalSilence {
			candidates = append(candidates, RoomToneCandidateMetrics{
				TransientWarning: "rejected: digital silence (RMS -120.0 dBFS)",
				Score:            0.0,
			})
		}
		for range other {
			candidates = append(candidates, RoomToneCandidateMetrics{
				TransientWarning: otherWarning,
				Score:            0.0,
			})
		}
		return candidates
	}

	tests := []struct {
		name       string
		candidates []RoomToneCandidateMetrics
		want       bool
	}{
		{
			name:       "100% digital silence (Anna-style)",
			candidates: makeCandidates(386, 0, ""),
			want:       true,
		},
		{
			name:       "99.8% digital silence, 1 crosstalk (Patrick-style)",
			candidates: makeCandidates(427, 1, "rejected: crosstalk detected"),
			want:       true,
		},
		{
			name:       "85% digital silence, below threshold (Marius-style)",
			candidates: makeCandidates(255, 45, ""),
			want:       false,
		},
		{
			name:       "no candidates",
			candidates: nil,
			want:       false,
		},
		{
			name: "candidates with scores pulling ratio down",
			// 90 digital silence + 10 scored candidates = 90% < 95% threshold
			candidates: func() []RoomToneCandidateMetrics {
				cs := makeCandidates(90, 10, "")
				for i := 90; i < 100; i++ {
					cs[i].Score = 0.5
				}
				return cs
			}(),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectVoiceActivated(tt.candidates)
			if got != tt.want {
				t.Errorf("detectVoiceActivated() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAnalyzeAudio_RoomToneScanDuration(t *testing.T) {
	// Deterministic synthetic input for the silence-scan-duration cap.
	//
	// Layout (85 seconds total):
	//   [0,  8 s)   tone + noise floor (32 intervals at ~-23 dBFS RMS)
	//   [8, 55 s)   silence with -60 dBFS noise floor (188 intervals; >= the
	//               32-interval / 8 s minimum used by the silence pipeline)
	//   [55, 85 s)  tone + noise floor (120 intervals at ~-23 dBFS RMS)
	//
	// The silence gap inherits the configured noise floor (see generateTestAudio)
	// so the silence pipeline scores it as real room tone rather than rejecting
	// it as digital silence (RMSLevel <= -115 dBFS).
	//
	// The 188 / 152 split (silence > tone) is required so the median RMS used
	// by roomToneScore falls in the silence range, which in turn lets
	// estimateNoiseFloorAndThreshold pin the silence threshold near the noise
	// floor. With a tone-dominated split the median sits at the tone level,
	// every interval scores 1.0, and the top-20% selection becomes order-
	// dependent on Go's unstable sort.
	opts := TestAudioOptions{
		DurationSecs: 85.0,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -23.0,
		NoiseLevel:   -60.0,
	}
	opts.SilenceGap.Start = 8.0
	opts.SilenceGap.Duration = 47.0

	testFile := generateTestAudio(t, opts)
	defer cleanupTestAudio(t, testFile)

	// Loudness fields are derived from ebur128 metadata, which is bit-stable
	// frame-by-frame for identical input. Allow a small tolerance to absorb
	// any non-determinism in floating-point reductions across runs.
	const loudnessTolerance = 0.01

	baselineConfig := newTestBaseConfig()
	baselineConfig.Analysis.Enabled = true

	baseline, err := AnalyzeAudio(context.Background(), testFile, baselineConfig, nil)
	if err != nil {
		t.Fatalf("baseline AnalyzeAudio failed: %v", err)
	}
	if baseline.NoiseProfile == nil {
		t.Fatalf("baseline NoiseProfile is nil; the silence pipeline did not elect a region "+
			"for the synthetic input (regions=%d)", len(baseline.RoomToneRegions))
	}

	abs := func(x float64) float64 {
		if x < 0 {
			return -x
		}
		return x
	}

	assertLoudnessMatches := func(t *testing.T, got *AudioMeasurements) {
		t.Helper()
		if d := abs(got.InputI - baseline.InputI); d > loudnessTolerance {
			t.Errorf("InputI = %.4f, baseline = %.4f, diff = %.4f (tolerance %.4f)",
				got.InputI, baseline.InputI, d, loudnessTolerance)
		}
		if d := abs(got.InputTP - baseline.InputTP); d > loudnessTolerance {
			t.Errorf("InputTP = %.4f, baseline = %.4f, diff = %.4f (tolerance %.4f)",
				got.InputTP, baseline.InputTP, d, loudnessTolerance)
		}
		if d := abs(got.InputLRA - baseline.InputLRA); d > loudnessTolerance {
			t.Errorf("InputLRA = %.4f, baseline = %.4f, diff = %.4f (tolerance %.4f)",
				got.InputLRA, baseline.InputLRA, d, loudnessTolerance)
		}
		if d := abs(got.NoiseFloor - baseline.NoiseFloor); d > loudnessTolerance {
			t.Errorf("NoiseFloor = %.4f, baseline = %.4f, diff = %.4f (tolerance %.4f)",
				got.NoiseFloor, baseline.NoiseFloor, d, loudnessTolerance)
		}
	}

	t.Run("zero_matches_baseline", func(t *testing.T) {
		// AC1 / AC5: explicit zero is identical to flag absent (no cap).
		config := newTestBaseConfig()
		config.Analysis.Enabled = true
		config.Analysis.RoomToneScanDuration = 0

		got, err := AnalyzeAudio(context.Background(), testFile, config, nil)
		if err != nil {
			t.Fatalf("AnalyzeAudio failed: %v", err)
		}

		assertLoudnessMatches(t, got)

		if !regionsEqual(got.RoomToneRegions, baseline.RoomToneRegions) {
			t.Errorf("RoomToneRegions = %+v, baseline = %+v",
				got.RoomToneRegions, baseline.RoomToneRegions)
		}
		if !speechRegionsEqual(got.SpeechRegions, baseline.SpeechRegions) {
			t.Errorf("SpeechRegions = %+v, baseline = %+v",
				got.SpeechRegions, baseline.SpeechRegions)
		}
		if !noiseProfilesEqual(got.NoiseProfile, baseline.NoiseProfile) {
			t.Errorf("NoiseProfile = %+v, baseline = %+v",
				got.NoiseProfile, baseline.NoiseProfile)
		}
	})

	t.Run("cap_excludes_silence_beyond_it", func(t *testing.T) {
		// AC6: with a cap of 2 s, the silence at 8-55 s falls outside the silence
		// pipeline's view, so no candidates are formed and no profile is extracted.
		// 2 s lands cleanly between the 1.75 s and 2.0 s interval start times.
		config := newTestBaseConfig()
		config.Analysis.Enabled = true
		config.Analysis.RoomToneScanDuration = 2 * time.Second

		got, err := AnalyzeAudio(context.Background(), testFile, config, nil)
		if err != nil {
			t.Fatalf("AnalyzeAudio failed: %v", err)
		}

		if len(got.RoomToneRegions) != 0 {
			t.Errorf("len(RoomToneRegions) = %d, want 0 (cap should exclude silence at 8-55 s)",
				len(got.RoomToneRegions))
		}
		if got.NoiseProfile != nil {
			t.Errorf("NoiseProfile = %+v, want nil", got.NoiseProfile)
		}
	})

	t.Run("cap_longer_than_silence_matches_uncapped", func(t *testing.T) {
		// AC7: cap at 60 s comfortably covers the silence at 8-55 s, so the
		// silence pipeline elects the same region as the uncapped run and
		// produces the same NoiseProfile placement.
		config := newTestBaseConfig()
		config.Analysis.Enabled = true
		config.Analysis.RoomToneScanDuration = 60 * time.Second

		got, err := AnalyzeAudio(context.Background(), testFile, config, nil)
		if err != nil {
			t.Fatalf("AnalyzeAudio failed: %v", err)
		}

		if got.NoiseProfile == nil {
			t.Fatalf("NoiseProfile is nil; expected the same region as the uncapped run "+
				"(baseline.NoiseProfile.Start = %v)", baseline.NoiseProfile.Start)
		}
		if got.NoiseProfile.Start != baseline.NoiseProfile.Start {
			t.Errorf("NoiseProfile.Start = %v, baseline = %v",
				got.NoiseProfile.Start, baseline.NoiseProfile.Start)
		}
		if got.NoiseProfile.Duration != baseline.NoiseProfile.Duration {
			t.Errorf("NoiseProfile.Duration = %v, baseline = %v",
				got.NoiseProfile.Duration, baseline.NoiseProfile.Duration)
		}
	})

	t.Run("other_measurements_unaffected_by_cap", func(t *testing.T) {
		// AC8: with the cap engaged (cap > silence end so the silence pipeline
		// elects the same region), whole-file measurements (loudness, true peak,
		// LRA, speech regions, interval samples) match the uncapped run because
		// only the silence pipeline reads the capped slice.
		config := newTestBaseConfig()
		config.Analysis.Enabled = true
		config.Analysis.RoomToneScanDuration = 60 * time.Second

		got, err := AnalyzeAudio(context.Background(), testFile, config, nil)
		if err != nil {
			t.Fatalf("AnalyzeAudio failed: %v", err)
		}

		assertLoudnessMatches(t, got)

		if !speechRegionsEqual(got.SpeechRegions, baseline.SpeechRegions) {
			t.Errorf("SpeechRegions = %+v, baseline = %+v",
				got.SpeechRegions, baseline.SpeechRegions)
		}
		if len(got.IntervalSamples) != len(baseline.IntervalSamples) {
			t.Errorf("len(IntervalSamples) = %d, baseline = %d",
				len(got.IntervalSamples), len(baseline.IntervalSamples))
		}
	})
}

// regionsEqual returns true when two RoomToneRegion slices match element-wise.
func regionsEqual(a, b []RoomToneRegion) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// speechRegionsEqual returns true when two SpeechRegion slices match element-wise.
func speechRegionsEqual(a, b []SpeechRegion) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// noiseProfilesEqual returns true when two NoiseProfile pointers refer to
// equivalent values (or are both nil).
func noiseProfilesEqual(a, b *NoiseProfile) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// buildConversationalIntervals shapes intervals like a sparse, conversational
// speaker (mark, popey): speech runs separated by pauses longer than the
// 8-interval (2s) tolerance, so no single run reaches the old 120-interval
// (30s) wall. Each speech run scores above speechScoreThreshold via the same
// loud/median/quiet RMS pattern the other speech tests use.
func buildConversationalIntervals(runLen, gapLen, runs int) []IntervalSample {
	var intervals []IntervalSample
	t := time.Duration(0)
	appendBlock := func(count int, isSpeech bool) {
		for range count {
			rms := -50.0
			centroid := 1500.0
			entropy := 0.6
			if isSpeech {
				// Voiced speech: above the gap-dominated median, centred in the
				// voice band with low entropy so every interval clears
				// speechScoreThreshold on its own (centroid + entropy alone
				// reach 0.5, independent of amplitude).
				rms = -14.0
				centroid = 2350.0 // voiceMid -> centroidScore 1.0
				entropy = 0.0     // -> entropyScore 1.0
			}
			intervals = append(intervals, IntervalSample{
				Timestamp: t,
				RMSLevel:  rms,
				Spectral: SpectralMetrics{
					Centroid: centroid,
					Entropy:  entropy,
				},
			})
			t += 250 * time.Millisecond
		}
	}
	for r := range runs {
		appendBlock(runLen, true)
		if r < runs-1 {
			appendBlock(gapLen, false)
		}
	}
	return intervals
}

// TestFindSpeechCandidatesFromIntervals_ConversationalSpeakerElects guards the
// detection-ep83 fix: lowering minimumSpeechIntervals to 40 (10s) lets sparse,
// conversational presenters elect a speech candidate. Runs of ~45 speech
// intervals separated by >2s pauses never sustained 30 unbroken seconds, so the
// old 120-interval gate yielded zero candidates and the SpeechProfile fell back
// to whole-file metrics.
func TestFindSpeechCandidatesFromIntervals_ConversationalSpeakerElects(t *testing.T) {
	// Three 45-interval speech runs (11.25s each) split by 10-interval (2.5s)
	// pauses that exceed the 8-interval default tolerance. No run reaches the
	// old 120 wall, but each clears the new 40-interval gate.
	const runLen = 45
	intervals := buildConversationalIntervals(runLen, 10, 3)

	candidates := findSpeechCandidatesFromIntervals(intervals, 0, false, -20.0, -55.0)

	if len(candidates) == 0 {
		t.Fatal("expected >=1 speech candidate for conversational speaker at 40-interval gate")
	}

	// Document the wall: each individual run (45 intervals / 11.25s) is well under
	// the old 120-interval (30s) threshold, so the pre-fix gate elected nothing.
	const oldGate = 120
	if runLen >= oldGate {
		t.Fatalf("test invariant broken: run length %d should be below the old gate %d", runLen, oldGate)
	}
}
