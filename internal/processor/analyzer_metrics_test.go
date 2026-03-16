package processor

import (
	"math"
	"testing"
)

const spectralTestEpsilon = 1e-9

func TestFinalizeSpectral_ZeroFrameCount(t *testing.T) {
	acc := &baseMetadataAccumulators{}
	result := acc.finalizeSpectral()

	if result != (SpectralMetrics{}) {
		t.Errorf("expected zero-value SpectralMetrics, got %+v", result)
	}
}

func TestFinalizeSpectral_AveragesCorrectly(t *testing.T) {
	acc := &baseMetadataAccumulators{
		spectralMeanSum:     10.0,
		spectralVarianceSum: 20.0,
		spectralCentroidSum: 3000.0,
		spectralSpreadSum:   600.0,
		spectralSkewnessSum: 4.0,
		spectralKurtosisSum: 8.0,
		spectralEntropySum:  1.5,
		spectralFlatnessSum: 0.5,
		spectralCrestSum:    6.0,
		spectralFluxSum:     2.0,
		spectralSlopeSum:    -0.02,
		spectralDecreaseSum: 0.4,
		spectralRolloffSum:  8000.0,
		spectralFrameCount:  2,
	}

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

func TestWriteSpectralTo_MapsAllFields(t *testing.T) {
	sm := SpectralMetrics{
		Mean:     1.0,
		Variance: 2.0,
		Centroid: 3.0,
		Spread:   4.0,
		Skewness: 5.0,
		Kurtosis: 6.0,
		Entropy:  7.0,
		Flatness: 8.0,
		Crest:    9.0,
		Flux:     10.0,
		Slope:    11.0,
		Decrease: 12.0,
		Rolloff:  13.0,
	}

	var bm BaseMeasurements
	sm.writeSpectralTo(&bm)

	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"SpectralMean", bm.SpectralMean, 1.0},
		{"SpectralVariance", bm.SpectralVariance, 2.0},
		{"SpectralCentroid", bm.SpectralCentroid, 3.0},
		{"SpectralSpread", bm.SpectralSpread, 4.0},
		{"SpectralSkewness", bm.SpectralSkewness, 5.0},
		{"SpectralKurtosis", bm.SpectralKurtosis, 6.0},
		{"SpectralEntropy", bm.SpectralEntropy, 7.0},
		{"SpectralFlatness", bm.SpectralFlatness, 8.0},
		{"SpectralCrest", bm.SpectralCrest, 9.0},
		{"SpectralFlux", bm.SpectralFlux, 10.0},
		{"SpectralSlope", bm.SpectralSlope, 11.0},
		{"SpectralDecrease", bm.SpectralDecrease, 12.0},
		{"SpectralRolloff", bm.SpectralRolloff, 13.0},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > spectralTestEpsilon {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestFinalizeSpectral_WriteSpectralTo_Chained(t *testing.T) {
	acc := &baseMetadataAccumulators{
		spectralMeanSum:     30.0,
		spectralVarianceSum: 60.0,
		spectralCentroidSum: 9000.0,
		spectralSpreadSum:   1500.0,
		spectralSkewnessSum: 6.0,
		spectralKurtosisSum: 12.0,
		spectralEntropySum:  2.1,
		spectralFlatnessSum: 0.9,
		spectralCrestSum:    15.0,
		spectralFluxSum:     3.0,
		spectralSlopeSum:    -0.06,
		spectralDecreaseSum: 1.2,
		spectralRolloffSum:  24000.0,
		spectralFrameCount:  3,
	}

	var bm BaseMeasurements
	acc.finalizeSpectral().writeSpectralTo(&bm)

	checks := []struct {
		name string
		got  float64
		want float64
	}{
		{"SpectralMean", bm.SpectralMean, 10.0},
		{"SpectralVariance", bm.SpectralVariance, 20.0},
		{"SpectralCentroid", bm.SpectralCentroid, 3000.0},
		{"SpectralSpread", bm.SpectralSpread, 500.0},
		{"SpectralSkewness", bm.SpectralSkewness, 2.0},
		{"SpectralKurtosis", bm.SpectralKurtosis, 4.0},
		{"SpectralEntropy", bm.SpectralEntropy, 0.7},
		{"SpectralFlatness", bm.SpectralFlatness, 0.3},
		{"SpectralCrest", bm.SpectralCrest, 5.0},
		{"SpectralFlux", bm.SpectralFlux, 1.0},
		{"SpectralSlope", bm.SpectralSlope, -0.02},
		{"SpectralDecrease", bm.SpectralDecrease, 0.4},
		{"SpectralRolloff", bm.SpectralRolloff, 8000.0},
	}
	for _, c := range checks {
		if math.Abs(c.got-c.want) > spectralTestEpsilon {
			t.Errorf("%s: got %v, want %v", c.name, c.got, c.want)
		}
	}
}
