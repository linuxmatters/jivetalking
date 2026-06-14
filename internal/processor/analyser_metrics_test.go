package processor

import (
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
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

	spectral := acc.finalizeSpectral()

	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"Mean", spectral.Mean, 10.0},
		{"Variance", spectral.Variance, 20.0},
		{"Centroid", spectral.Centroid, 3000.0},
		{"Spread", spectral.Spread, 500.0},
		{"Skewness", spectral.Skewness, 2.0},
		{"Kurtosis", spectral.Kurtosis, 4.0},
		{"Entropy", spectral.Entropy, 0.7},
		{"Flatness", spectral.Flatness, 0.3},
		{"Crest", spectral.Crest, 5.0},
		{"Flux", spectral.Flux, 1.0},
		{"Slope", spectral.Slope, -0.02},
		{"Decrease", spectral.Decrease, 0.4},
		{"Rolloff", spectral.Rolloff, 8000.0},
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

// newMetadataDict builds an *ffmpeg.AVDictionary from key/value string pairs and
// registers its cleanup. Keys absent from the map are absent from the dict, so a
// caller can model a frame that "misses" a key. The values are raw decimal text,
// exactly as FFmpeg's astats/ebur128 filters emit them into frame metadata.
func newMetadataDict(t *testing.T, pairs map[string]string) *ffmpeg.AVDictionary {
	t.Helper()

	var dict *ffmpeg.AVDictionary
	for k, v := range pairs {
		key := ffmpeg.ToCStr(k)
		value := ffmpeg.ToCStr(v)
		if _, err := ffmpeg.AVDictSet(&dict, key, value, 0); err != nil {
			key.Free()
			value.Free()
			ffmpeg.AVDictFree(&dict)
			t.Fatalf("AVDictSet(%q) error = %v", k, err)
		}
		key.Free()
		value.Free()
	}
	t.Cleanup(func() { ffmpeg.AVDictFree(&dict) })
	return dict
}

func TestGetFloatMetadata_ParsesValueFromCBytes(t *testing.T) {
	dict := newMetadataDict(t, map[string]string{
		"lavfi.astats.1.Dynamic_range": "42.5",
		"lavfi.astats.1.RMS_level":     "-23.456789",
		"lavfi.astats.1.Min_level":     "-0.5",
	})

	cases := []struct {
		name string
		key  *ffmpeg.CStr
		want float64
	}{
		{"Dynamic_range", metaKeyDynamicRange, 42.5},
		{"RMS_level", metaKeyRMSLevel, -23.456789},
		{"Min_level", metaKeyMinLevel, -0.5},
	}
	for _, c := range cases {
		got, ok := getFloatMetadata(dict, c.key)
		if !ok {
			t.Errorf("%s: ok = false, want true", c.name)
			continue
		}
		// Bit-identical to the strconv.ParseFloat the previous String() path fed.
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGetFloatMetadata_MissingKeyReportsNotFound(t *testing.T) {
	dict := newMetadataDict(t, map[string]string{
		"lavfi.astats.1.RMS_level": "-20.0",
	})

	if value, ok := getFloatMetadata(dict, metaKeyDynamicRange); ok {
		t.Errorf("missing key: got (%v, true), want (_, false)", value)
	}
}

func TestGetFloatMetadata_UnparseableValueReportsNotFound(t *testing.T) {
	dict := newMetadataDict(t, map[string]string{
		"lavfi.astats.1.Dynamic_range": "not-a-number",
	})

	if value, ok := getFloatMetadata(dict, metaKeyDynamicRange); ok {
		t.Errorf("unparseable value: got (%v, true), want (_, false)", value)
	}
}

// TestExtractAstatsMetadata_LatestFoundPerKeyWins is the core semantic guard for
// task 1.2: astats values are cumulative ("latest non-missing wins"), so a late
// frame that misses a key must NOT clobber the value an earlier frame supplied,
// and the per-key found flag (astatsFound) must reflect any frame that supplied
// Dynamic_range.
func TestExtractAstatsMetadata_LatestFoundPerKeyWins(t *testing.T) {
	var acc baseMetadataAccumulators

	// Frame 1: supplies Dynamic_range and RMS_level.
	acc.extractAstatsMetadata(newMetadataDict(t, map[string]string{
		"lavfi.astats.1.Dynamic_range": "40.0",
		"lavfi.astats.1.RMS_level":     "-25.0",
		"lavfi.astats.1.RMS_trough":    "-60.0",
	}), optionalFloat{})

	// Frame 2 (later): supplies a newer RMS_level but MISSES Dynamic_range and
	// RMS_trough. The earlier values for the missed keys must be retained, and
	// the newer RMS_level must win.
	acc.extractAstatsMetadata(newMetadataDict(t, map[string]string{
		"lavfi.astats.1.RMS_level": "-22.0",
	}), optionalFloat{})

	if acc.astatsDynamicRange != 40.0 {
		t.Errorf("astatsDynamicRange = %v, want 40.0 (earlier frame retained)", acc.astatsDynamicRange)
	}
	if acc.astatsRMSLevel != -22.0 {
		t.Errorf("astatsRMSLevel = %v, want -22.0 (later frame wins)", acc.astatsRMSLevel)
	}
	if acc.astatsRMSTrough != -60.0 {
		t.Errorf("astatsRMSTrough = %v, want -60.0 (earlier frame retained)", acc.astatsRMSTrough)
	}
	if !acc.astatsFound {
		t.Error("astatsFound = false, want true (Dynamic_range was supplied)")
	}
}

// TestExtractAstatsMetadata_FoundFlagStaysFalseWithoutDynamicRange confirms the
// found flag tracks Dynamic_range specifically, matching the pre-change rule.
func TestExtractAstatsMetadata_FoundFlagStaysFalseWithoutDynamicRange(t *testing.T) {
	var acc baseMetadataAccumulators

	acc.extractAstatsMetadata(newMetadataDict(t, map[string]string{
		"lavfi.astats.1.RMS_level": "-20.0",
	}), optionalFloat{})

	if acc.astatsFound {
		t.Error("astatsFound = true, want false (no Dynamic_range supplied)")
	}
}

// TestExtractAstatsMetadata_AppliesConversions guards the dB conversions that sit
// on top of the raw parse (Crest_factor, Min_level, Max_level), so the no-copy
// parse change cannot silently shift a converted field.
func TestExtractAstatsMetadata_AppliesConversions(t *testing.T) {
	var acc baseMetadataAccumulators

	acc.extractAstatsMetadata(newMetadataDict(t, map[string]string{
		"lavfi.astats.1.Crest_factor": "10.0",
		"lavfi.astats.1.Min_level":    "-0.5",
		"lavfi.astats.1.Max_level":    "0.5",
	}), optionalFloat{})

	if want := linearRatioToDB(10.0); acc.astatsCrestFactor != want {
		t.Errorf("astatsCrestFactor = %v, want %v", acc.astatsCrestFactor, want)
	}
	if want := linearSampleToDBFS(-0.5); acc.astatsMinLevel != want {
		t.Errorf("astatsMinLevel = %v, want %v", acc.astatsMinLevel, want)
	}
	if want := linearSampleToDBFS(0.5); acc.astatsMaxLevel != want {
		t.Errorf("astatsMaxLevel = %v, want %v", acc.astatsMaxLevel, want)
	}
}

// BenchmarkGetFloatMetadata exercises the per-key parse on the hot Pass 1 path.
// The no-copy CStr view should report fewer allocs/op than the prior String()
// path. Run: go test -bench=BenchmarkGetFloatMetadata -benchmem.
func BenchmarkGetFloatMetadata(b *testing.B) {
	var dict *ffmpeg.AVDictionary
	key := ffmpeg.ToCStr("lavfi.astats.1.RMS_level")
	value := ffmpeg.ToCStr("-23.456789")
	if _, err := ffmpeg.AVDictSet(&dict, key, value, 0); err != nil {
		key.Free()
		value.Free()
		b.Fatalf("AVDictSet() error = %v", err)
	}
	key.Free()
	value.Free()
	b.Cleanup(func() { ffmpeg.AVDictFree(&dict) })

	b.ReportAllocs()
	var sink float64
	for b.Loop() {
		v, _ := getFloatMetadata(dict, metaKeyRMSLevel)
		sink += v
	}
	_ = sink
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
