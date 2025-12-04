package processor

import (
	"testing"
)

func TestTuneNoiseReduction(t *testing.T) {
	// Constants from adaptive.go for reference:
	// noiseReductionBase = 12.0 dB
	// noiseReductionMin  = 6.0 dB
	// noiseReductionMax  = 40.0 dB

	tests := []struct {
		name          string
		inputI        float64 // measured input LUFS
		targetI       float64 // target LUFS (typically -16)
		wantReduction float64 // expected noise reduction in dB
	}{
		// Normal scaling: base (12) + LUFS gap
		{
			name:          "near target - minimal boost",
			inputI:        -18,
			targetI:       -16,
			wantReduction: 14, // 12 + 2
		},
		{
			name:          "moderate gap",
			inputI:        -26,
			targetI:       -16,
			wantReduction: 22, // 12 + 10
		},
		{
			name:          "typical podcast gap",
			inputI:        -30,
			targetI:       -16,
			wantReduction: 26, // 12 + 14
		},

		// Clamping behaviour
		{
			name:          "very quiet source - clamped to max",
			inputI:        -46,
			targetI:       -16,
			wantReduction: 40, // 12 + 30 = 42, clamped to noiseReductionMax (40)
		},
		{
			name:          "extremely quiet source - clamped to max",
			inputI:        -60,
			targetI:       -16,
			wantReduction: 40, // 12 + 44 = 56, clamped to 40
		},
		{
			name:          "loud source - negative gap uses base only",
			inputI:        -12,
			targetI:       -16,
			wantReduction: 8, // 12 + (-4) = 8, above min
		},
		{
			name:          "very loud source - clamped to min",
			inputI:        -6,
			targetI:       -16,
			wantReduction: 6, // 12 + (-10) = 2, clamped to noiseReductionMin (6)
		},

		// Edge cases
		{
			name:          "no LUFS measurement - fallback to base",
			inputI:        0, // triggers fallback
			targetI:       -16,
			wantReduction: 12, // noiseReductionBase
		},
		{
			name:          "exact target - base only",
			inputI:        -16,
			targetI:       -16,
			wantReduction: 12, // 12 + 0
		},

		// Boundary: exactly at max before clamping
		{
			name:          "boundary: exactly 28dB gap",
			inputI:        -44,
			targetI:       -16,
			wantReduction: 40, // 12 + 28 = 40, exactly at max
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			config := DefaultFilterConfig()
			config.TargetI = tt.targetI
			measurements := &AudioMeasurements{
				InputI: tt.inputI,
			}

			// Calculate LUFS gap as done in AdaptConfig
			lufsGap := calculateLUFSGap(tt.targetI, tt.inputI)

			// Execute
			tuneNoiseReduction(config, measurements, lufsGap)

			// Verify
			if config.NoiseReduction != tt.wantReduction {
				t.Errorf("NoiseReduction = %.1f dB, want %.1f dB (inputI=%.1f, targetI=%.1f, gap=%.1f)",
					config.NoiseReduction, tt.wantReduction, tt.inputI, tt.targetI, lufsGap)
			}
		})
	}
}

func TestTuneHighpassFreq(t *testing.T) {
	tests := []struct {
		name        string
		centroid    float64 // spectral centroid (Hz)
		lufsGap     float64 // target - input LUFS (dB)
		wantFreqMin float64 // minimum expected frequency
		wantFreqMax float64 // maximum expected frequency
	}{
		// Voice brightness classification (normal gain, lufsGap <= 15)
		{
			name:        "dark voice, normal gain",
			centroid:    3500, // below centroidNormal (4000)
			lufsGap:     10,   // below lufsGapModerate (15)
			wantFreqMin: 60,   // highpassMinFreq
			wantFreqMax: 60,
		},
		{
			name:        "normal voice, normal gain",
			centroid:    5000, // between centroidNormal (4000) and centroidBright (6000)
			lufsGap:     10,
			wantFreqMin: 80, // highpassDefaultFreq
			wantFreqMax: 80,
		},
		{
			name:        "bright voice, normal gain",
			centroid:    7000, // above centroidBright (6000)
			lufsGap:     10,
			wantFreqMin: 100, // highpassBrightFreq
			wantFreqMax: 100,
		},

		// LUFS gap boost (moderate: 15-25 dB gap adds 20Hz)
		{
			name:        "dark voice, moderate gain",
			centroid:    3500,
			lufsGap:     20, // between lufsGapModerate (15) and lufsGapAggressive (25)
			wantFreqMin: 80, // 60 + 20
			wantFreqMax: 80,
		},
		{
			name:        "normal voice, moderate gain",
			centroid:    5000,
			lufsGap:     20,
			wantFreqMin: 100, // 80 + 20
			wantFreqMax: 100,
		},
		{
			name:        "bright voice, moderate gain",
			centroid:    7000,
			lufsGap:     20,
			wantFreqMin: 120, // 100 + 20, capped at highpassMaxFreq
			wantFreqMax: 120,
		},

		// LUFS gap boost (aggressive: >25 dB gap adds 40Hz)
		{
			name:        "dark voice, aggressive gain",
			centroid:    3500,
			lufsGap:     30,  // above lufsGapAggressive (25)
			wantFreqMin: 100, // 60 + 40
			wantFreqMax: 100,
		},
		{
			name:        "normal voice, aggressive gain",
			centroid:    5000,
			lufsGap:     30,
			wantFreqMin: 120, // 80 + 40, capped at highpassMaxFreq
			wantFreqMax: 120,
		},
		{
			name:        "bright voice, aggressive gain",
			centroid:    7000,
			lufsGap:     30,
			wantFreqMin: 120, // 100 + 40 = 140, capped at highpassMaxFreq (120)
			wantFreqMax: 120,
		},

		// Edge cases
		{
			name:        "no spectral data - keeps default",
			centroid:    0, // triggers early return
			lufsGap:     20,
			wantFreqMin: 80, // DefaultFilterConfig().HighpassFreq
			wantFreqMax: 80,
		},
		{
			name:        "negative centroid - keeps default",
			centroid:    -100,
			lufsGap:     10,
			wantFreqMin: 80,
			wantFreqMax: 80,
		},
		{
			name:        "boundary: exactly at centroidNormal",
			centroid:    4000, // exactly at centroidNormal threshold
			lufsGap:     10,
			wantFreqMin: 60, // dark voice (not > centroidNormal)
			wantFreqMax: 60,
		},
		{
			name:        "boundary: exactly at centroidBright",
			centroid:    6000, // exactly at centroidBright threshold
			lufsGap:     10,
			wantFreqMin: 80, // normal voice (not > centroidBright)
			wantFreqMax: 80,
		},
		{
			name:        "boundary: exactly at lufsGapModerate",
			centroid:    5000,
			lufsGap:     15, // exactly at lufsGapModerate threshold
			wantFreqMin: 80, // no boost (not > lufsGapModerate)
			wantFreqMax: 80,
		},
		{
			name:        "boundary: exactly at lufsGapAggressive",
			centroid:    5000,
			lufsGap:     25,  // exactly at lufsGapAggressive threshold
			wantFreqMin: 100, // moderate boost (not > lufsGapAggressive)
			wantFreqMax: 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup: create default config and measurements
			config := DefaultFilterConfig()
			measurements := &AudioMeasurements{
				SpectralCentroid: tt.centroid,
			}

			// Execute
			tuneHighpassFreq(config, measurements, tt.lufsGap)

			// Verify
			if config.HighpassFreq < tt.wantFreqMin || config.HighpassFreq > tt.wantFreqMax {
				t.Errorf("HighpassFreq = %.1f Hz, want [%.1f, %.1f] Hz",
					config.HighpassFreq, tt.wantFreqMin, tt.wantFreqMax)
			}
		})
	}
}

func TestTuneDeesser(t *testing.T) {
	// Constants from adaptive.go for reference:
	// centroidVeryBright = 7000 Hz
	// centroidBright     = 6000 Hz
	// rolloffNoSibilance = 6000 Hz
	// rolloffLimited     = 8000 Hz
	// rolloffExtensive   = 12000 Hz
	// deessIntensityBright = 0.6
	// deessIntensityNormal = 0.5
	// deessIntensityDark   = 0.4
	// deessIntensityMax    = 0.8
	// deessIntensityMin    = 0.3

	tests := []struct {
		name          string
		centroid      float64 // spectral centroid (Hz)
		rolloff       float64 // spectral rolloff (Hz)
		wantIntensity float64 // expected de-esser intensity
		tolerance     float64 // acceptable tolerance for floating point
	}{
		// Full adaptive logic (both centroid and rolloff available)
		// Bright voice (centroid > 7000) with extensive HF (rolloff > 12000)
		{
			name:          "very bright voice, extensive HF",
			centroid:      7500,
			rolloff:       14000,
			wantIntensity: 0.72, // 0.6 * 1.2, capped at 0.8
			tolerance:     0.01,
		},
		// Normal-bright voice (centroid 6000-7000) with extensive HF
		{
			name:          "normal-bright voice, extensive HF",
			centroid:      6500,
			rolloff:       14000,
			wantIntensity: 0.6, // 0.5 * 1.2 = 0.6
			tolerance:     0.01,
		},
		// Dark voice (centroid < 6000) with limited HF (rolloff 6000-8000)
		// Dark voice base is 0.4, limited HF applies 0.7 factor = 0.28
		// But 0.28 < deessIntensityMin (0.3), so it gets disabled
		{
			name:          "dark voice, limited HF - disabled below min",
			centroid:      3500,
			rolloff:       7000,
			wantIntensity: 0.0, // 0.4 * 0.7 = 0.28 < 0.3 min, disabled
			tolerance:     0.0,
		},
		// Normal-bright voice with limited HF - above min threshold
		{
			name:          "normal-bright voice, limited HF",
			centroid:      6500,
			rolloff:       7000,
			wantIntensity: 0.35, // 0.5 * 0.7 = 0.35 > 0.3 min
			tolerance:     0.01,
		},
		// No HF content (rolloff < 6000) - disabled regardless of centroid
		{
			name:          "bright voice, no HF content",
			centroid:      7500,
			rolloff:       5000,
			wantIntensity: 0.0, // disabled due to no sibilance expected
			tolerance:     0.0,
		},
		{
			name:          "normal voice, no HF content",
			centroid:      5000,
			rolloff:       5500,
			wantIntensity: 0.0,
			tolerance:     0.0,
		},
		// Normal HF extension (8000-12000)
		{
			name:          "bright voice, normal HF",
			centroid:      7500,
			rolloff:       10000,
			wantIntensity: 0.6, // base intensity, no modifier
			tolerance:     0.01,
		},
		{
			name:          "normal voice, normal HF",
			centroid:      6500,
			rolloff:       10000,
			wantIntensity: 0.5,
			tolerance:     0.01,
		},
		{
			name:          "dark voice, normal HF",
			centroid:      5000,
			rolloff:       10000,
			wantIntensity: 0.4,
			tolerance:     0.01,
		},

		// Limited HF with intensity below minimum - should disable
		{
			name:          "dark voice, limited HF, below min threshold",
			centroid:      5000, // dark voice, base 0.4
			rolloff:       7500, // limited HF, * 0.7 = 0.28 < 0.3 min
			wantIntensity: 0.0,  // disabled because 0.28 < deessIntensityMin
			tolerance:     0.0,
		},

		// Centroid-only fallback (rolloff = 0)
		{
			name:          "very bright voice, no rolloff data",
			centroid:      7500,
			rolloff:       0,
			wantIntensity: 0.6, // deessIntensityBright
			tolerance:     0.01,
		},
		{
			name:          "normal-bright voice, no rolloff data",
			centroid:      6500,
			rolloff:       0,
			wantIntensity: 0.5, // deessIntensityNormal
			tolerance:     0.01,
		},
		{
			name:          "dark voice, no rolloff data",
			centroid:      5000,
			rolloff:       0,
			wantIntensity: 0.4, // deessIntensityDark
			tolerance:     0.01,
		},

		// No spectral data - keep default (0.0)
		{
			name:          "no spectral data",
			centroid:      0,
			rolloff:       0,
			wantIntensity: 0.0,
			tolerance:     0.0,
		},
		{
			name:          "negative centroid",
			centroid:      -100,
			rolloff:       10000,
			wantIntensity: 0.0,
			tolerance:     0.0,
		},

		// Boundary conditions
		{
			name:          "boundary: exactly at centroidVeryBright",
			centroid:      7000, // exactly at threshold
			rolloff:       10000,
			wantIntensity: 0.5, // not > 7000, so uses deessIntensityNormal
			tolerance:     0.01,
		},
		{
			name:          "boundary: exactly at centroidBright",
			centroid:      6000,
			rolloff:       10000,
			wantIntensity: 0.4, // not > 6000, so uses deessIntensityDark
			tolerance:     0.01,
		},
		{
			name:          "boundary: exactly at rolloffLimited",
			centroid:      7500,
			rolloff:       8000, // exactly at threshold
			wantIntensity: 0.6, // not < 8000, falls to default (normal HF)
			tolerance:     0.01,
		},
		{
			name:          "boundary: exactly at rolloffExtensive",
			centroid:      7500,
			rolloff:       12000, // exactly at threshold
			wantIntensity: 0.6,   // not > 12000, falls to default (normal HF)
			tolerance:     0.01,
		},

		// Max capping
		{
			name:          "intensity capped at max",
			centroid:      7500,  // bright, base 0.6
			rolloff:       15000, // extensive, * 1.2 = 0.72
			wantIntensity: 0.72,  // below max 0.8, so not capped
			tolerance:     0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			config := DefaultFilterConfig()
			measurements := &AudioMeasurements{
				SpectralCentroid: tt.centroid,
				SpectralRolloff:  tt.rolloff,
			}

			// Execute
			tuneDeesser(config, measurements)

			// Verify
			diff := config.DeessIntensity - tt.wantIntensity
			if diff < 0 {
				diff = -diff
			}
			if diff > tt.tolerance {
				t.Errorf("DeessIntensity = %.3f, want %.3f (±%.3f) [centroid=%.0f, rolloff=%.0f]",
					config.DeessIntensity, tt.wantIntensity, tt.tolerance, tt.centroid, tt.rolloff)
			}
		})
	}
}
