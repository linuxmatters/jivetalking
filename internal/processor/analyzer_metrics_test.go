package processor

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

const spectralTestEpsilon = 1e-9

var staleSpectralPrimitiveFields = []string{
	"SpectralMean",
	"SpectralVariance",
	"SpectralCentroid",
	"SpectralSpread",
	"SpectralSkewness",
	"SpectralKurtosis",
	"SpectralEntropy",
	"SpectralFlatness",
	"SpectralCrest",
	"SpectralFlux",
	"SpectralSlope",
	"SpectralDecrease",
	"SpectralRolloff",
}

func TestFinalizeSpectral_ZeroFrameCount(t *testing.T) {
	acc := &baseMetadataAccumulators{}
	result := acc.finalizeSpectral()

	if result != (SpectralMetrics{}) {
		t.Errorf("expected zero-value SpectralMetrics, got %+v", result)
	}
}

func TestFinalizeSpectral_AveragesCorrectly(t *testing.T) {
	acc := &baseMetadataAccumulators{}
	acc.accumulateSpectral(SpectralMetrics{
		Mean:     2.0,
		Variance: 4.0,
		Centroid: 1000.0,
		Spread:   200.0,
		Skewness: 1.0,
		Kurtosis: 2.0,
		Entropy:  0.25,
		Flatness: 0.10,
		Crest:    1.0,
		Flux:     0.5,
		Slope:    -0.005,
		Decrease: 0.1,
		Rolloff:  2000.0,
		Found:    true,
	})
	acc.accumulateSpectral(SpectralMetrics{
		Mean:     8.0,
		Variance: 16.0,
		Centroid: 2000.0,
		Spread:   400.0,
		Skewness: 3.0,
		Kurtosis: 6.0,
		Entropy:  1.25,
		Flatness: 0.40,
		Crest:    5.0,
		Flux:     1.5,
		Slope:    -0.015,
		Decrease: 0.3,
		Rolloff:  6000.0,
		Found:    true,
	})

	result := acc.finalizeSpectral()

	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"Mean", result.Mean, 5.0},
		{"Variance", result.Variance, 10.0},
		{"Centroid", result.Centroid, 1500.0},
		{"Spread", result.Spread, 300.0},
		{"Skewness", result.Skewness, 2.0},
		{"Kurtosis", result.Kurtosis, 4.0},
		{"Entropy", result.Entropy, 0.75},
		{"Flatness", result.Flatness, 0.25},
		{"Crest", result.Crest, 3.0},
		{"Flux", result.Flux, 1.0},
		{"Slope", result.Slope, -0.01},
		{"Decrease", result.Decrease, 0.2},
		{"Rolloff", result.Rolloff, 4000.0},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > spectralTestEpsilon {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestFinalizeSpectral_AssignsBaseSpectral(t *testing.T) {
	acc := &baseMetadataAccumulators{}
	for range 3 {
		acc.accumulateSpectral(SpectralMetrics{
			Mean:     10.0,
			Variance: 20.0,
			Centroid: 3000.0,
			Spread:   500.0,
			Skewness: 2.0,
			Kurtosis: 4.0,
			Entropy:  0.7,
			Flatness: 0.3,
			Crest:    5.0,
			Flux:     1.0,
			Slope:    -0.02,
			Decrease: 0.4,
			Rolloff:  8000.0,
			Found:    true,
		})
	}

	var bm BaseMeasurements
	bm.Spectral = acc.finalizeSpectral()

	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"Mean", bm.Spectral.Mean, 10.0},
		{"Variance", bm.Spectral.Variance, 20.0},
		{"Centroid", bm.Spectral.Centroid, 3000.0},
		{"Spread", bm.Spectral.Spread, 500.0},
		{"Skewness", bm.Spectral.Skewness, 2.0},
		{"Kurtosis", bm.Spectral.Kurtosis, 4.0},
		{"Entropy", bm.Spectral.Entropy, 0.7},
		{"Flatness", bm.Spectral.Flatness, 0.3},
		{"Crest", bm.Spectral.Crest, 5.0},
		{"Flux", bm.Spectral.Flux, 1.0},
		{"Slope", bm.Spectral.Slope, -0.02},
		{"Decrease", bm.Spectral.Decrease, 0.4},
		{"Rolloff", bm.Spectral.Rolloff, 8000.0},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > spectralTestEpsilon {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestSpectralAccumulator_ZeroFrameCount(t *testing.T) {
	var acc SpectralAccumulator

	if acc.Found() {
		t.Fatal("expected Found to be false before adding spectral metrics")
	}
	if got := acc.Count(); got != 0 {
		t.Fatalf("Count() = %d, want 0", got)
	}
	if got := acc.Average(); got != (SpectralMetrics{}) {
		t.Fatalf("Average() = %+v, want zero-value SpectralMetrics", got)
	}
}

func TestSpectralAccumulator_MixedFoundAndUnfound(t *testing.T) {
	var acc SpectralAccumulator

	acc.Add(SpectralMetrics{
		Mean:     100.0,
		Variance: 200.0,
		Found:    false,
	})
	acc.Add(SpectralMetrics{
		Mean:     10.0,
		Variance: 20.0,
		Found:    true,
	})

	if !acc.Found() {
		t.Fatal("expected Found to be true after adding found spectral metrics")
	}
	if got := acc.Count(); got != 1 {
		t.Fatalf("Count() = %d, want 1", got)
	}

	average := acc.Average()
	if !average.Found {
		t.Fatal("expected averaged SpectralMetrics to preserve Found")
	}
	if average.Mean != 10.0 {
		t.Errorf("Mean = %v, want 10.0", average.Mean)
	}
	if average.Variance != 20.0 {
		t.Errorf("Variance = %v, want 20.0", average.Variance)
	}
}

func TestSpectralAccumulator_AveragesAllFields(t *testing.T) {
	var acc SpectralAccumulator
	acc.Add(SpectralMetrics{
		Mean:     2.0,
		Variance: 4.0,
		Centroid: 1000.0,
		Spread:   200.0,
		Skewness: 1.0,
		Kurtosis: 2.0,
		Entropy:  0.2,
		Flatness: 0.4,
		Crest:    6.0,
		Flux:     0.02,
		Slope:    -0.10,
		Decrease: 0.06,
		Rolloff:  5000.0,
		Found:    true,
	})
	acc.Add(SpectralMetrics{
		Mean:     6.0,
		Variance: 12.0,
		Centroid: 3000.0,
		Spread:   600.0,
		Skewness: 3.0,
		Kurtosis: 6.0,
		Entropy:  0.6,
		Flatness: 0.8,
		Crest:    10.0,
		Flux:     0.06,
		Slope:    -0.30,
		Decrease: 0.18,
		Rolloff:  9000.0,
		Found:    true,
	})

	result := acc.Average()

	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"Mean", result.Mean, 4.0},
		{"Variance", result.Variance, 8.0},
		{"Centroid", result.Centroid, 2000.0},
		{"Spread", result.Spread, 400.0},
		{"Skewness", result.Skewness, 2.0},
		{"Kurtosis", result.Kurtosis, 4.0},
		{"Entropy", result.Entropy, 0.4},
		{"Flatness", result.Flatness, 0.6},
		{"Crest", result.Crest, 8.0},
		{"Flux", result.Flux, 0.04},
		{"Slope", result.Slope, -0.20},
		{"Decrease", result.Decrease, 0.12},
		{"Rolloff", result.Rolloff, 7000.0},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > spectralTestEpsilon {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestBaseMetadataAccumulators_UsesSingleSpectralAccumulator(t *testing.T) {
	accType := reflect.TypeFor[baseMetadataAccumulators]()
	var spectralFields []reflect.StructField
	for field := range accType.Fields() {
		if strings.HasPrefix(field.Name, "spectral") {
			spectralFields = append(spectralFields, field)
		}
	}

	if len(spectralFields) != 1 {
		t.Fatalf("baseMetadataAccumulators spectral field count = %d, want 1", len(spectralFields))
	}
	if spectralFields[0].Type != reflect.TypeFor[SpectralAccumulator]() {
		t.Fatalf("spectral field type = %v, want SpectralAccumulator", spectralFields[0].Type)
	}
}

func TestIntervalSample_UsesSingleSpectralMetricsField(t *testing.T) {
	sampleType := reflect.TypeFor[IntervalSample]()
	var spectralFields []reflect.StructField
	for field := range sampleType.Fields() {
		if strings.HasPrefix(field.Name, "Spectral") {
			spectralFields = append(spectralFields, field)
		}
	}

	if len(spectralFields) != 1 {
		t.Fatalf("IntervalSample spectral field count = %d, want 1", len(spectralFields))
	}
	if spectralFields[0].Name != "Spectral" {
		t.Fatalf("spectral field name = %s, want Spectral", spectralFields[0].Name)
	}
	if spectralFields[0].Type != reflect.TypeFor[SpectralMetrics]() {
		t.Fatalf("spectral field type = %v, want SpectralMetrics", spectralFields[0].Type)
	}
}

func TestIntervalSample_HasNoFlatSpectralPrimitiveFields(t *testing.T) {
	assertNoStaleSpectralPrimitiveFields[IntervalSample](t)
}

func TestIntervalFrameMetrics_UsesSingleSpectralMetricsField(t *testing.T) {
	metricsType := reflect.TypeFor[intervalFrameMetrics]()
	var spectralFields []reflect.StructField
	for field := range metricsType.Fields() {
		if strings.HasPrefix(field.Name, "Spectral") {
			spectralFields = append(spectralFields, field)
		}
	}

	if len(spectralFields) != 1 {
		t.Fatalf("intervalFrameMetrics spectral field count = %d, want 1", len(spectralFields))
	}
	if spectralFields[0].Name != "Spectral" {
		t.Fatalf("spectral field name = %s, want Spectral", spectralFields[0].Name)
	}
	if spectralFields[0].Type != reflect.TypeFor[SpectralMetrics]() {
		t.Fatalf("spectral field type = %v, want SpectralMetrics", spectralFields[0].Type)
	}
}

func assertNoStaleSpectralPrimitiveFields[T any](t *testing.T) {
	t.Helper()

	targetType := reflect.TypeFor[T]()
	for _, fieldName := range staleSpectralPrimitiveFields {
		if field, ok := targetType.FieldByName(fieldName); ok {
			t.Errorf("%s has stale flat spectral field %s with type %v", targetType.Name(), field.Name, field.Type)
		}
	}
}

func TestIntervalAccumulatorFinalize_WritesAveragedSpectralMetrics(t *testing.T) {
	acc := &intervalAccumulator{}
	acc.add(intervalFrameMetrics{
		Spectral: SpectralMetrics{
			Mean:     2.0,
			Variance: 4.0,
			Centroid: 1000.0,
			Spread:   200.0,
			Skewness: 1.0,
			Kurtosis: 2.0,
			Entropy:  0.2,
			Flatness: 0.4,
			Crest:    6.0,
			Flux:     0.02,
			Slope:    -0.10,
			Decrease: 0.06,
			Rolloff:  5000.0,
			Found:    true,
		},
	})
	acc.add(intervalFrameMetrics{
		Spectral: SpectralMetrics{
			Mean:     6.0,
			Variance: 12.0,
			Centroid: 3000.0,
			Spread:   600.0,
			Skewness: 3.0,
			Kurtosis: 6.0,
			Entropy:  0.6,
			Flatness: 0.8,
			Crest:    10.0,
			Flux:     0.06,
			Slope:    -0.30,
			Decrease: 0.18,
			Rolloff:  9000.0,
			Found:    true,
		},
	})

	result := acc.finalize(time.Second)

	if !result.Spectral.Found {
		t.Fatal("expected averaged interval spectral metrics to preserve Found")
	}
	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"Mean", result.Spectral.Mean, 4.0},
		{"Variance", result.Spectral.Variance, 8.0},
		{"Centroid", result.Spectral.Centroid, 2000.0},
		{"Spread", result.Spectral.Spread, 400.0},
		{"Skewness", result.Spectral.Skewness, 2.0},
		{"Kurtosis", result.Spectral.Kurtosis, 4.0},
		{"Entropy", result.Spectral.Entropy, 0.4},
		{"Flatness", result.Spectral.Flatness, 0.6},
		{"Crest", result.Spectral.Crest, 8.0},
		{"Flux", result.Spectral.Flux, 0.04},
		{"Slope", result.Spectral.Slope, -0.20},
		{"Decrease", result.Spectral.Decrease, 0.12},
		{"Rolloff", result.Spectral.Rolloff, 7000.0},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > spectralTestEpsilon {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

// TestAudioMeasurementsUnmarshal_AcceptsLegacySilenceKeys verifies that a payload
// containing only the legacy silence_* keys (no room_tone_* keys) populates the
// renamed room-tone Go fields identically to a new-key payload would.
func TestAudioMeasurementsUnmarshal_AcceptsLegacySilenceKeys(t *testing.T) {
	payload := []byte(`{
		"silence_detect_level": -55.0,
		"silence_regions": [{"start": 1000000000, "end": 2000000000, "duration": 1000000000}],
		"silence_candidates": [{"rms_level": -58.0}]
	}`)

	var got AudioMeasurements
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}

	if got.RoomToneDetectLevel != -55.0 {
		t.Errorf("RoomToneDetectLevel = %v, want -55.0", got.RoomToneDetectLevel)
	}
	if len(got.RoomToneRegions) != 1 {
		t.Fatalf("RoomToneRegions length = %d, want 1", len(got.RoomToneRegions))
	}
	if got.RoomToneRegions[0].Start != time.Second {
		t.Errorf("RoomToneRegions[0].Start = %v, want %v", got.RoomToneRegions[0].Start, time.Second)
	}
	if got.RoomToneRegions[0].End != 2*time.Second {
		t.Errorf("RoomToneRegions[0].End = %v, want %v", got.RoomToneRegions[0].End, 2*time.Second)
	}
	if got.RoomToneRegions[0].Duration != time.Second {
		t.Errorf("RoomToneRegions[0].Duration = %v, want %v", got.RoomToneRegions[0].Duration, time.Second)
	}
	if len(got.RoomToneCandidates) != 1 {
		t.Fatalf("RoomToneCandidates length = %d, want 1", len(got.RoomToneCandidates))
	}
	if got.RoomToneCandidates[0].RMSLevel != -58.0 {
		t.Errorf("RoomToneCandidates[0].RMSLevel = %v, want -58.0", got.RoomToneCandidates[0].RMSLevel)
	}
}

// TestAudioMeasurementsUnmarshal_AcceptsNewRoomToneKeys is a regression test for
// task 2.1 that a payload containing only new room_tone_* keys populates the
// renamed Go fields.
func TestAudioMeasurementsUnmarshal_AcceptsNewRoomToneKeys(t *testing.T) {
	payload := []byte(`{
		"room_tone_detect_level": -55.0,
		"room_tone_regions": [{"start": 1000000000, "end": 2000000000, "duration": 1000000000}],
		"room_tone_candidates": [{"rms_level": -58.0}]
	}`)

	var got AudioMeasurements
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}

	if got.RoomToneDetectLevel != -55.0 {
		t.Errorf("RoomToneDetectLevel = %v, want -55.0", got.RoomToneDetectLevel)
	}
	if len(got.RoomToneRegions) != 1 {
		t.Fatalf("RoomToneRegions length = %d, want 1", len(got.RoomToneRegions))
	}
	if got.RoomToneRegions[0].Start != time.Second {
		t.Errorf("RoomToneRegions[0].Start = %v, want %v", got.RoomToneRegions[0].Start, time.Second)
	}
	if len(got.RoomToneCandidates) != 1 {
		t.Fatalf("RoomToneCandidates length = %d, want 1", len(got.RoomToneCandidates))
	}
	if got.RoomToneCandidates[0].RMSLevel != -58.0 {
		t.Errorf("RoomToneCandidates[0].RMSLevel = %v, want -58.0", got.RoomToneCandidates[0].RMSLevel)
	}
}

// TestAudioMeasurementsUnmarshal_NewKeysWinOverLegacy verifies that when both
// the legacy silence_* keys and the new room_tone_* keys are present with
// different values, the new keys win deterministically.
func TestAudioMeasurementsUnmarshal_NewKeysWinOverLegacy(t *testing.T) {
	payload := []byte(`{
		"silence_detect_level": -55.0,
		"room_tone_detect_level": -42.0,
		"silence_regions": [{"start": 1000000000, "end": 2000000000, "duration": 1000000000}],
		"room_tone_regions": [{"start": 3000000000, "end": 5000000000, "duration": 2000000000}],
		"silence_candidates": [{"rms_level": -58.0}],
		"room_tone_candidates": [{"rms_level": -61.0}, {"rms_level": -62.0}]
	}`)

	var got AudioMeasurements
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}

	if got.RoomToneDetectLevel != -42.0 {
		t.Errorf("RoomToneDetectLevel = %v, want -42.0 (new key wins)", got.RoomToneDetectLevel)
	}
	if len(got.RoomToneRegions) != 1 {
		t.Fatalf("RoomToneRegions length = %d, want 1 (from new key)", len(got.RoomToneRegions))
	}
	if got.RoomToneRegions[0].Start != 3*time.Second {
		t.Errorf("RoomToneRegions[0].Start = %v, want %v (new key wins)", got.RoomToneRegions[0].Start, 3*time.Second)
	}
	if got.RoomToneRegions[0].Duration != 2*time.Second {
		t.Errorf("RoomToneRegions[0].Duration = %v, want %v (new key wins)", got.RoomToneRegions[0].Duration, 2*time.Second)
	}
	if len(got.RoomToneCandidates) != 2 {
		t.Fatalf("RoomToneCandidates length = %d, want 2 (from new key)", len(got.RoomToneCandidates))
	}
	if got.RoomToneCandidates[0].RMSLevel != -61.0 {
		t.Errorf("RoomToneCandidates[0].RMSLevel = %v, want -61.0 (new key wins)", got.RoomToneCandidates[0].RMSLevel)
	}
}

// TestAudioMeasurementsJSON_LegacyRoundTrip verifies that marshal-then-unmarshal
// preserves the room-tone values. Marshal emits only new keys; unmarshal reads
// them back identically.
func TestAudioMeasurementsJSON_LegacyRoundTrip(t *testing.T) {
	original := AudioMeasurements{
		RoomToneDetectLevel: -55.0,
		RoomToneRegions: []RoomToneRegion{
			{Start: time.Second, End: 2 * time.Second, Duration: time.Second},
		},
		RoomToneCandidates: []RoomToneCandidateMetrics{
			{RMSLevel: -58.0, Spectral: flatSpectralMetricsFixture()},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() failed: %v", err)
	}

	var decoded AudioMeasurements
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}

	if decoded.RoomToneDetectLevel != original.RoomToneDetectLevel {
		t.Errorf("RoomToneDetectLevel = %v, want %v", decoded.RoomToneDetectLevel, original.RoomToneDetectLevel)
	}
	if len(decoded.RoomToneRegions) != len(original.RoomToneRegions) {
		t.Fatalf("RoomToneRegions length = %d, want %d", len(decoded.RoomToneRegions), len(original.RoomToneRegions))
	}
	if decoded.RoomToneRegions[0] != original.RoomToneRegions[0] {
		t.Errorf("RoomToneRegions[0] = %+v, want %+v", decoded.RoomToneRegions[0], original.RoomToneRegions[0])
	}
	if len(decoded.RoomToneCandidates) != len(original.RoomToneCandidates) {
		t.Fatalf("RoomToneCandidates length = %d, want %d", len(decoded.RoomToneCandidates), len(original.RoomToneCandidates))
	}
	if decoded.RoomToneCandidates[0].RMSLevel != original.RoomToneCandidates[0].RMSLevel {
		t.Errorf("RoomToneCandidates[0].RMSLevel = %v, want %v", decoded.RoomToneCandidates[0].RMSLevel, original.RoomToneCandidates[0].RMSLevel)
	}
}

// TestOutputMeasurementsUnmarshal_AcceptsLegacySilenceSample verifies that a
// payload containing only the legacy silence_sample key populates RoomToneSample
// identically to a new-key payload would.
func TestOutputMeasurementsUnmarshal_AcceptsLegacySilenceSample(t *testing.T) {
	payload := []byte(`{"silence_sample": {"rms_level": -58.0}}`)

	var got OutputMeasurements
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}

	if got.RoomToneSample == nil {
		t.Fatal("RoomToneSample is nil; expected to be populated from legacy silence_sample")
	}
	if got.RoomToneSample.RMSLevel != -58.0 {
		t.Errorf("RoomToneSample.RMSLevel = %v, want -58.0", got.RoomToneSample.RMSLevel)
	}
}

// TestOutputMeasurementsUnmarshal_AcceptsNewRoomToneSample is a regression test
// for task 2.1 that a payload containing only the new room_tone_sample key
// populates RoomToneSample.
func TestOutputMeasurementsUnmarshal_AcceptsNewRoomToneSample(t *testing.T) {
	payload := []byte(`{"room_tone_sample": {"rms_level": -58.0}}`)

	var got OutputMeasurements
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}

	if got.RoomToneSample == nil {
		t.Fatal("RoomToneSample is nil; expected to be populated from new room_tone_sample")
	}
	if got.RoomToneSample.RMSLevel != -58.0 {
		t.Errorf("RoomToneSample.RMSLevel = %v, want -58.0", got.RoomToneSample.RMSLevel)
	}
}

// TestOutputMeasurementsUnmarshal_NewKeyWinsOverLegacy verifies that when both
// silence_sample and room_tone_sample are present with different values, the
// new room_tone_sample wins deterministically.
func TestOutputMeasurementsUnmarshal_NewKeyWinsOverLegacy(t *testing.T) {
	payload := []byte(`{
		"silence_sample": {"rms_level": -58.0},
		"room_tone_sample": {"rms_level": -62.0}
	}`)

	var got OutputMeasurements
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}

	if got.RoomToneSample == nil {
		t.Fatal("RoomToneSample is nil; expected to be populated from room_tone_sample")
	}
	if got.RoomToneSample.RMSLevel != -62.0 {
		t.Errorf("RoomToneSample.RMSLevel = %v, want -62.0 (new key wins)", got.RoomToneSample.RMSLevel)
	}
}

// TestOutputMeasurementsJSON_LegacyRoundTrip verifies that marshal-then-unmarshal
// preserves the room-tone sample. Marshal emits only the new key; unmarshal reads
// it back identically.
func TestOutputMeasurementsJSON_LegacyRoundTrip(t *testing.T) {
	original := OutputMeasurements{
		RoomToneSample: &RoomToneCandidateMetrics{
			RMSLevel: -58.0,
			Spectral: flatSpectralMetricsFixture(),
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() failed: %v", err)
	}

	var decoded OutputMeasurements
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}

	if decoded.RoomToneSample == nil {
		t.Fatal("expected RoomToneSample to round-trip")
	}
	if decoded.RoomToneSample.RMSLevel != original.RoomToneSample.RMSLevel {
		t.Errorf("RoomToneSample.RMSLevel = %v, want %v", decoded.RoomToneSample.RMSLevel, original.RoomToneSample.RMSLevel)
	}
}
