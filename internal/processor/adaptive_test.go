package processor

import (
	"math"
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
			wantIntensity: 0.6,  // not < 8000, falls to default (normal HF)
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

func TestTuneGateThreshold(t *testing.T) {
	// Constants from adaptive.go for reference:
	// gateOffsetClean    = 10.0 dB (above noise floor for clean recordings)
	// gateOffsetTypical  = 8.0 dB  (above noise floor for typical podcasts)
	// gateOffsetNoisy    = 6.0 dB  (above noise floor for noisy recordings)
	// gateThresholdMinDB = -70.0 dB (professional studio clean)
	// gateThresholdMaxDB = -25.0 dB (very noisy environment)
	// noiseFloorClean    = -60.0 dBFS
	// noiseFloorTypical  = -50.0 dBFS
	// noiseFloorNoisy    = -40.0 dBFS
	// dbToLinear(db) = 10^(db/20)

	tests := []struct {
		name              string
		noiseFloor        float64 // dBFS input
		wantThresholdDB   float64 // expected threshold in dB (before linear conversion)
		wantThresholdDesc string  // description of expected behaviour
	}{
		// Clean recording tier (noise floor < -60 dBFS) - uses 10dB offset
		{
			name:              "professional studio, very clean",
			noiseFloor:        -70,
			wantThresholdDB:   -60, // -70 + 10, but clamped to -70 min? No: -60 > -70, OK
			wantThresholdDesc: "clean offset applied",
		},
		{
			name:              "clean home studio",
			noiseFloor:        -65,
			wantThresholdDB:   -55, // -65 + 10
			wantThresholdDesc: "clean offset applied",
		},
		{
			name:              "boundary: exactly at noiseFloorClean",
			noiseFloor:        -60,
			wantThresholdDB:   -52, // -60 + 8 (not < -60, so uses typical offset)
			wantThresholdDesc: "typical offset (boundary)",
		},

		// Typical podcast tier (noise floor -60 to -50 dBFS) - uses 8dB offset
		{
			name:              "typical podcast recording",
			noiseFloor:        -55,
			wantThresholdDB:   -47, // -55 + 8
			wantThresholdDesc: "typical offset applied",
		},
		{
			name:              "boundary: exactly at noiseFloorTypical",
			noiseFloor:        -50,
			wantThresholdDB:   -44, // -50 + 6 (not < -50, so uses noisy offset)
			wantThresholdDesc: "noisy offset (boundary)",
		},

		// Noisy recording tier (noise floor >= -50 dBFS) - uses 6dB offset
		{
			name:              "noisy home recording",
			noiseFloor:        -45,
			wantThresholdDB:   -39, // -45 + 6
			wantThresholdDesc: "noisy offset applied",
		},
		{
			name:              "very noisy room",
			noiseFloor:        -35,
			wantThresholdDB:   -29, // -35 + 6
			wantThresholdDesc: "noisy offset applied",
		},

		// Clamping behaviour
		{
			name:              "extreme noise - clamped to max",
			noiseFloor:        -20,
			wantThresholdDB:   -25, // -20 + 6 = -14, clamped to gateThresholdMaxDB (-25)
			wantThresholdDesc: "clamped to max threshold",
		},
		{
			name:              "extremely clean - clamped to min",
			noiseFloor:        -85,
			wantThresholdDB:   -70, // -85 + 10 = -75, clamped to gateThresholdMinDB (-70)
			wantThresholdDesc: "clamped to min threshold",
		},

		// Edge cases
		{
			name:              "boundary: exactly produces max threshold",
			noiseFloor:        -31,
			wantThresholdDB:   -25, // -31 + 6 = -25, exactly at max
			wantThresholdDesc: "at max boundary",
		},
		{
			name:              "boundary: exactly produces min threshold",
			noiseFloor:        -80,
			wantThresholdDB:   -70, // -80 + 10 = -70, exactly at min
			wantThresholdDesc: "at min boundary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			config := DefaultFilterConfig()
			measurements := &AudioMeasurements{
				NoiseFloor: tt.noiseFloor,
			}

			// Execute
			tuneGateThreshold(config, measurements)

			// Calculate expected linear value from expected dB
			wantLinear := dbToLinear(tt.wantThresholdDB)

			// Verify with tolerance for floating point
			tolerance := 0.0001
			diff := config.GateThreshold - wantLinear
			if diff < 0 {
				diff = -diff
			}
			if diff > tolerance {
				// Convert actual back to dB for clearer error message
				actualDB := linearToDB(config.GateThreshold)
				t.Errorf("GateThreshold = %.6f (%.1f dB), want %.6f (%.1f dB) [noiseFloor=%.1f dB, %s]",
					config.GateThreshold, actualDB, wantLinear, tt.wantThresholdDB, tt.noiseFloor, tt.wantThresholdDesc)
			}
		})
	}
}

// linearToDB converts linear amplitude to dB for test error messages
func linearToDB(linear float64) float64 {
	if linear <= 0 {
		return -1000 // avoid math.Log10(0) = -Inf
	}
	return 20 * math.Log10(linear)
}

func TestSanitizeFloat(t *testing.T) {
	// Tests for the sanitizeFloat helper function
	// Returns default value for NaN and Inf, otherwise returns original value

	const defaultVal = 42.0

	tests := []struct {
		name     string
		input    float64
		want     float64
		wantDesc string
	}{
		// NaN cases
		{
			name:     "NaN returns default",
			input:    math.NaN(),
			want:     defaultVal,
			wantDesc: "NaN should be replaced with default",
		},

		// Inf cases
		{
			name:     "positive Inf returns default",
			input:    math.Inf(1),
			want:     defaultVal,
			wantDesc: "+Inf should be replaced with default",
		},
		{
			name:     "negative Inf returns default",
			input:    math.Inf(-1),
			want:     defaultVal,
			wantDesc: "-Inf should be replaced with default",
		},

		// Valid values pass through unchanged
		{
			name:     "zero passes through",
			input:    0.0,
			want:     0.0,
			wantDesc: "zero is valid and should pass through",
		},
		{
			name:     "negative value passes through",
			input:    -25.5,
			want:     -25.5,
			wantDesc: "negative values are valid (e.g., dB thresholds)",
		},
		{
			name:     "positive value passes through",
			input:    80.0,
			want:     80.0,
			wantDesc: "positive values are valid",
		},
		{
			name:     "very small positive passes through",
			input:    1e-10,
			want:     1e-10,
			wantDesc: "small positive values are valid",
		},
		{
			name:     "very large positive passes through",
			input:    1e10,
			want:     1e10,
			wantDesc: "large positive values are valid (clamping is separate)",
		},
		{
			name:     "very small negative passes through",
			input:    -1e-10,
			want:     -1e-10,
			wantDesc: "small negative values are valid",
		},
		{
			name:     "very large negative passes through",
			input:    -1e10,
			want:     -1e10,
			wantDesc: "large negative values are valid (clamping is separate)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFloat(tt.input, defaultVal)

			// Handle NaN comparison specially
			if math.IsNaN(tt.want) {
				if !math.IsNaN(got) {
					t.Errorf("sanitizeFloat() = %v, want NaN - %s", got, tt.wantDesc)
				}
				return
			}

			if got != tt.want {
				t.Errorf("sanitizeFloat() = %v, want %v - %s", got, tt.want, tt.wantDesc)
			}
		})
	}
}

func TestSanitizeConfig(t *testing.T) {
	// Tests for sanitizeConfig which sanitizes all tunable parameters in FilterChainConfig
	// Uses defaults from adaptive.go:
	// defaultHighpassFreq   = 80.0
	// defaultDeessIntensity = 0.0
	// defaultNoiseReduction = 12.0
	// defaultCompRatio      = 2.5
	// defaultCompThreshold  = -20.0
	// defaultCompMakeup     = 3.0
	// defaultGateThreshold  = 0.01 (linear, ~-40dBFS)

	tests := []struct {
		name   string
		config FilterChainConfig // input config
		want   FilterChainConfig // expected after sanitization
	}{
		// Clean config should pass through unchanged
		{
			name: "valid config passes through unchanged",
			config: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
			want: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
		},

		// NaN in each field
		{
			name: "NaN HighpassFreq gets default",
			config: FilterChainConfig{
				HighpassFreq:   math.NaN(),
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
			want: FilterChainConfig{
				HighpassFreq:   80.0, // defaultHighpassFreq
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
		},
		{
			name: "NaN DeessIntensity gets default",
			config: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: math.NaN(),
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
			want: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.0, // defaultDeessIntensity
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
		},
		{
			name: "NaN NoiseReduction gets default",
			config: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: math.NaN(),
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
			want: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 12.0, // defaultNoiseReduction
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
		},
		{
			name: "NaN CompRatio gets default",
			config: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      math.NaN(),
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
			want: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      2.5, // defaultCompRatio
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
		},
		{
			name: "NaN CompThreshold gets default",
			config: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  math.NaN(),
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
			want: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -20.0, // defaultCompThreshold
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
		},
		{
			name: "NaN CompMakeup gets default",
			config: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     math.NaN(),
				GateThreshold:  0.02,
			},
			want: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     3.0, // defaultCompMakeup
				GateThreshold:  0.02,
			},
		},
		{
			name: "NaN GateThreshold gets default",
			config: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  math.NaN(),
			},
			want: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.01, // defaultGateThreshold
			},
		},

		// Inf cases
		{
			name: "positive Inf values get defaults",
			config: FilterChainConfig{
				HighpassFreq:   math.Inf(1),
				DeessIntensity: math.Inf(1),
				NoiseReduction: math.Inf(1),
				CompRatio:      math.Inf(1),
				CompThreshold:  math.Inf(1),
				CompMakeup:     math.Inf(1),
				GateThreshold:  math.Inf(1),
			},
			want: FilterChainConfig{
				HighpassFreq:   80.0,
				DeessIntensity: 0.0,
				NoiseReduction: 12.0,
				CompRatio:      2.5,
				CompThreshold:  -20.0,
				CompMakeup:     3.0,
				GateThreshold:  0.01,
			},
		},
		{
			name: "negative Inf values get defaults",
			config: FilterChainConfig{
				HighpassFreq:   math.Inf(-1),
				DeessIntensity: math.Inf(-1),
				NoiseReduction: math.Inf(-1),
				CompRatio:      math.Inf(-1),
				CompThreshold:  math.Inf(-1),
				CompMakeup:     math.Inf(-1),
				GateThreshold:  math.Inf(-1),
			},
			want: FilterChainConfig{
				HighpassFreq:   80.0,
				DeessIntensity: 0.0,
				NoiseReduction: 12.0,
				CompRatio:      2.5,
				CompThreshold:  -20.0,
				CompMakeup:     3.0,
				GateThreshold:  0.01,
			},
		},

		// GateThreshold special cases: zero and negative get default
		// (other fields allow zero/negative values)
		{
			name: "zero GateThreshold gets default",
			config: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.0, // zero is valid for DeessIntensity
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.0, // zero is NOT valid for GateThreshold
			},
			want: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.0,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.01, // defaultGateThreshold
			},
		},
		{
			name: "negative GateThreshold gets default",
			config: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  -0.5, // negative is NOT valid for GateThreshold
			},
			want: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  0.01, // defaultGateThreshold
			},
		},

		// Zero values for other fields pass through
		// (sanitizeFloat doesn't treat zero specially)
		{
			name: "zero values for non-GateThreshold fields pass through",
			config: FilterChainConfig{
				HighpassFreq:   0.0, // passes through (edge case: probably invalid, but sanitize doesn't clamp)
				DeessIntensity: 0.0, // valid: de-essing disabled
				NoiseReduction: 0.0, // passes through (edge case: no reduction)
				CompRatio:      0.0, // passes through (edge case: probably invalid)
				CompThreshold:  0.0, // passes through (0 dB threshold)
				CompMakeup:     0.0, // passes through (0 dB makeup)
				GateThreshold:  0.02,
			},
			want: FilterChainConfig{
				HighpassFreq:   0.0,
				DeessIntensity: 0.0,
				NoiseReduction: 0.0,
				CompRatio:      0.0,
				CompThreshold:  0.0,
				CompMakeup:     0.0,
				GateThreshold:  0.02,
			},
		},

		// Negative values for fields that legitimately use them
		{
			name: "negative CompThreshold passes through (valid dB value)",
			config: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -40.0, // very aggressive threshold
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
			want: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -40.0,
				CompMakeup:     4.0,
				GateThreshold:  0.02,
			},
		},

		// All fields NaN - complete fallback to defaults
		{
			name: "all NaN values get all defaults",
			config: FilterChainConfig{
				HighpassFreq:   math.NaN(),
				DeessIntensity: math.NaN(),
				NoiseReduction: math.NaN(),
				CompRatio:      math.NaN(),
				CompThreshold:  math.NaN(),
				CompMakeup:     math.NaN(),
				GateThreshold:  math.NaN(),
			},
			want: FilterChainConfig{
				HighpassFreq:   80.0,
				DeessIntensity: 0.0,
				NoiseReduction: 12.0,
				CompRatio:      2.5,
				CompThreshold:  -20.0,
				CompMakeup:     3.0,
				GateThreshold:  0.01,
			},
		},

		// Very small positive GateThreshold passes through
		{
			name: "very small positive GateThreshold passes through",
			config: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  1e-10, // very small but positive
			},
			want: FilterChainConfig{
				HighpassFreq:   100.0,
				DeessIntensity: 0.3,
				NoiseReduction: 18.0,
				CompRatio:      3.0,
				CompThreshold:  -24.0,
				CompMakeup:     4.0,
				GateThreshold:  1e-10,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy to avoid mutating test data
			config := tt.config
			sanitizeConfig(&config)

			// Check each field
			if config.HighpassFreq != tt.want.HighpassFreq {
				t.Errorf("HighpassFreq = %v, want %v", config.HighpassFreq, tt.want.HighpassFreq)
			}
			if config.DeessIntensity != tt.want.DeessIntensity {
				t.Errorf("DeessIntensity = %v, want %v", config.DeessIntensity, tt.want.DeessIntensity)
			}
			if config.NoiseReduction != tt.want.NoiseReduction {
				t.Errorf("NoiseReduction = %v, want %v", config.NoiseReduction, tt.want.NoiseReduction)
			}
			if config.CompRatio != tt.want.CompRatio {
				t.Errorf("CompRatio = %v, want %v", config.CompRatio, tt.want.CompRatio)
			}
			if config.CompThreshold != tt.want.CompThreshold {
				t.Errorf("CompThreshold = %v, want %v", config.CompThreshold, tt.want.CompThreshold)
			}
			if config.CompMakeup != tt.want.CompMakeup {
				t.Errorf("CompMakeup = %v, want %v", config.CompMakeup, tt.want.CompMakeup)
			}
			if config.GateThreshold != tt.want.GateThreshold {
				t.Errorf("GateThreshold = %v, want %v", config.GateThreshold, tt.want.GateThreshold)
			}
		})
	}
}

func TestClamp(t *testing.T) {
	// Tests for the clamp helper function
	// clamp(val, min, max) returns val constrained to [min, max]

	tests := []struct {
		name string
		val  float64
		min  float64
		max  float64
		want float64
	}{
		// Value within range
		{
			name: "value within range passes through",
			val:  50.0,
			min:  0.0,
			max:  100.0,
			want: 50.0,
		},
		{
			name: "value at min boundary passes through",
			val:  0.0,
			min:  0.0,
			max:  100.0,
			want: 0.0,
		},
		{
			name: "value at max boundary passes through",
			val:  100.0,
			min:  0.0,
			max:  100.0,
			want: 100.0,
		},

		// Value below min
		{
			name: "value below min clamped to min",
			val:  -10.0,
			min:  0.0,
			max:  100.0,
			want: 0.0,
		},
		{
			name: "value far below min clamped to min",
			val:  -1000.0,
			min:  0.0,
			max:  100.0,
			want: 0.0,
		},

		// Value above max
		{
			name: "value above max clamped to max",
			val:  150.0,
			min:  0.0,
			max:  100.0,
			want: 100.0,
		},
		{
			name: "value far above max clamped to max",
			val:  1e10,
			min:  0.0,
			max:  100.0,
			want: 100.0,
		},

		// Negative ranges
		{
			name: "negative range - value within",
			val:  -25.0,
			min:  -40.0,
			max:  -10.0,
			want: -25.0,
		},
		{
			name: "negative range - value below min",
			val:  -50.0,
			min:  -40.0,
			max:  -10.0,
			want: -40.0,
		},
		{
			name: "negative range - value above max",
			val:  0.0,
			min:  -40.0,
			max:  -10.0,
			want: -10.0,
		},

		// Single-point range (min == max)
		{
			name: "single point range - value equals",
			val:  42.0,
			min:  42.0,
			max:  42.0,
			want: 42.0,
		},
		{
			name: "single point range - value below",
			val:  10.0,
			min:  42.0,
			max:  42.0,
			want: 42.0,
		},
		{
			name: "single point range - value above",
			val:  100.0,
			min:  42.0,
			max:  42.0,
			want: 42.0,
		},

		// Real-world audio processing ranges
		{
			name: "highpass freq clamping - below min",
			val:  30.0,
			min:  60.0,  // minHighpassFreq
			max:  120.0, // maxHighpassFreq
			want: 60.0,
		},
		{
			name: "highpass freq clamping - above max",
			val:  200.0,
			min:  60.0,
			max:  120.0,
			want: 120.0,
		},
		{
			name: "noise reduction clamping - below min",
			val:  2.0,
			min:  6.0,  // noiseReductionMin
			max:  40.0, // noiseReductionMax
			want: 6.0,
		},
		{
			name: "noise reduction clamping - above max",
			val:  60.0,
			min:  6.0,
			max:  40.0,
			want: 40.0,
		},
		{
			name: "deess intensity clamping - below min",
			val:  -0.1,
			min:  0.0, // minDeessIntensity
			max:  0.6, // maxDeessIntensity
			want: 0.0,
		},
		{
			name: "deess intensity clamping - above max",
			val:  0.9,
			min:  0.0,
			max:  0.6,
			want: 0.6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clamp(tt.val, tt.min, tt.max)
			if got != tt.want {
				t.Errorf("clamp(%v, %v, %v) = %v, want %v",
					tt.val, tt.min, tt.max, got, tt.want)
			}
		})
	}
}

func TestTuneSpeechnormDenoise(t *testing.T) {
	// Tests for tuneSpeechnormDenoise which enables RNN/NLM denoise for heavily expanded audio
	// Constants from adaptive.go:
	// speechnormExpansionThreshold = 8.0 (triggers denoise activation)
	// arnnDnMixDefault             = 0.8 (full filtering when enabled)
	// anlmDnStrengthMin            = 0.0
	// anlmDnStrengthMax            = 0.01
	// anlmDnStrengthScale          = 0.00001
	//
	// AnlmDn strength formula: clamp(0.00001 * expansion^2, 0.0, 0.01)

	tests := []struct {
		name            string
		expansion       float64
		wantArnnDn      bool
		wantArnnDnMix   float64 // only checked if wantArnnDn is true
		wantAnlmDn      bool
		wantAnlmDnMin   float64 // minimum expected strength
		wantAnlmDnMax   float64 // maximum expected strength
		wantDescription string
	}{
		// Below threshold - denoise disabled
		{
			name:            "minimal expansion - denoise disabled",
			expansion:       1.0,
			wantArnnDn:      false,
			wantAnlmDn:      false,
			wantDescription: "1x expansion (0dB gain) should not enable denoise",
		},
		{
			name:            "moderate expansion - denoise disabled",
			expansion:       3.0,
			wantArnnDn:      false,
			wantAnlmDn:      false,
			wantDescription: "3x expansion (~10dB gain) still below threshold",
		},
		{
			name:            "typical podcast expansion - denoise disabled",
			expansion:       5.0,
			wantArnnDn:      false,
			wantAnlmDn:      false,
			wantDescription: "5x expansion (~14dB gain) still below threshold",
		},
		{
			name:            "just below threshold - denoise disabled",
			expansion:       7.9,
			wantArnnDn:      false,
			wantAnlmDn:      false,
			wantDescription: "7.9x is below 8.0 threshold",
		},

		// At and above threshold - denoise enabled
		{
			name:            "at threshold - denoise enabled",
			expansion:       8.0,
			wantArnnDn:      true,
			wantArnnDnMix:   0.8,
			wantAnlmDn:      true,
			wantAnlmDnMin:   0.00064, // 0.00001 * 8^2 = 0.00064
			wantAnlmDnMax:   0.00064,
			wantDescription: "exactly 8.0 threshold should enable denoise",
		},
		{
			name:            "slightly above threshold",
			expansion:       8.1,
			wantArnnDn:      true,
			wantArnnDnMix:   0.8,
			wantAnlmDn:      true,
			wantAnlmDnMin:   0.000656, // 0.00001 * 8.1^2 ≈ 0.000656
			wantAnlmDnMax:   0.000657,
			wantDescription: "8.1 expansion enables denoise with scaled strength",
		},
		{
			name:            "high expansion",
			expansion:       9.0,
			wantArnnDn:      true,
			wantArnnDnMix:   0.8,
			wantAnlmDn:      true,
			wantAnlmDnMin:   0.00080, // 0.00001 * 9^2 = 0.00081 (allow float tolerance)
			wantAnlmDnMax:   0.00082,
			wantDescription: "9x expansion (~19dB gain)",
		},
		{
			name:            "maximum capped expansion",
			expansion:       10.0,
			wantArnnDn:      true,
			wantArnnDnMix:   0.8,
			wantAnlmDn:      true,
			wantAnlmDnMin:   0.001, // 0.00001 * 10^2 = 0.001
			wantAnlmDnMax:   0.001,
			wantDescription: "10x expansion (speechnormMaxExpansion)",
		},

		// Extreme values (beyond normal operating range)
		{
			name:            "extreme expansion - clamped strength",
			expansion:       50.0,
			wantArnnDn:      true,
			wantArnnDnMix:   0.8,
			wantAnlmDn:      true,
			wantAnlmDnMin:   0.01, // clamped to anlmDnStrengthMax
			wantAnlmDnMax:   0.01, // 0.00001 * 50^2 = 0.025, clamped to 0.01
			wantDescription: "50x expansion strength clamped to max 0.01",
		},
		{
			name:            "zero expansion - denoise disabled",
			expansion:       0.0,
			wantArnnDn:      false,
			wantAnlmDn:      false,
			wantDescription: "zero expansion (edge case) below threshold",
		},
		{
			name:            "negative expansion - denoise disabled",
			expansion:       -1.0,
			wantArnnDn:      false,
			wantAnlmDn:      false,
			wantDescription: "negative expansion (edge case) below threshold",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &FilterChainConfig{}
			tuneSpeechnormDenoise(config, tt.expansion)

			// Check ArnnDn enabled state
			if config.ArnnDnEnabled != tt.wantArnnDn {
				t.Errorf("ArnnDnEnabled = %v, want %v - %s",
					config.ArnnDnEnabled, tt.wantArnnDn, tt.wantDescription)
			}

			// Check ArnnDn mix value when enabled
			if tt.wantArnnDn && config.ArnnDnMix != tt.wantArnnDnMix {
				t.Errorf("ArnnDnMix = %v, want %v - %s",
					config.ArnnDnMix, tt.wantArnnDnMix, tt.wantDescription)
			}

			// Check AnlmDn enabled state
			if config.AnlmDnEnabled != tt.wantAnlmDn {
				t.Errorf("AnlmDnEnabled = %v, want %v - %s",
					config.AnlmDnEnabled, tt.wantAnlmDn, tt.wantDescription)
			}

			// Check AnlmDn strength value when enabled
			if tt.wantAnlmDn {
				if config.AnlmDnStrength < tt.wantAnlmDnMin || config.AnlmDnStrength > tt.wantAnlmDnMax {
					t.Errorf("AnlmDnStrength = %v, want [%v, %v] - %s (expansion=%.1f)",
						config.AnlmDnStrength, tt.wantAnlmDnMin, tt.wantAnlmDnMax,
						tt.wantDescription, tt.expansion)
				}
			}
		})
	}
}

func TestTuneSpeechnorm(t *testing.T) {
	// Tests for tuneSpeechnorm which adapts cycle-level normalization and triggers denoise
	// Constants from adaptive.go:
	// speechnormMaxExpansion       = 10.0 (caps expansion)
	// speechnormPeakTarget         = 0.95
	// speechnormSmoothingFast      = 0.001
	// lufsRmsOffset                = 23.0
	//
	// Expansion formula: 10^(lufsGap/20)
	// RMS formula: clamp(10^((targetI+23)/20), 0, 1)

	tests := []struct {
		name               string
		inputI             float64 // measured input LUFS
		targetI            float64 // target LUFS
		wantExpansionMin   float64
		wantExpansionMax   float64
		wantDenoiseEnabled bool
		wantRMSApprox      float64 // expected RMS after clamping to [0, 1]
		wantDescription    string
	}{
		// Zero input LUFS - early return
		{
			name:               "zero input LUFS - early return",
			inputI:             0.0,
			targetI:            -16.0,
			wantExpansionMin:   0.0, // unchanged from default
			wantExpansionMax:   0.0,
			wantDenoiseEnabled: false,
			wantRMSApprox:      0.0, // unchanged (early return)
			wantDescription:    "zero input LUFS triggers early return, no changes",
		},

		// Normal expansion cases (below denoise threshold)
		// Note: For targetI=-16, RMS = 10^((-16+23)/20) = 10^0.35 ≈ 2.238 → clamped to 1.0
		{
			name:               "near target - minimal expansion",
			inputI:             -18.0,
			targetI:            -16.0,
			wantExpansionMin:   1.2, // 10^(2/20) ≈ 1.26
			wantExpansionMax:   1.3,
			wantDenoiseEnabled: false,
			wantRMSApprox:      1.0, // 10^(7/20) ≈ 2.238 clamped to 1.0
			wantDescription:    "2dB gap = ~1.26x expansion",
		},
		{
			name:               "typical podcast source",
			inputI:             -26.0,
			targetI:            -16.0,
			wantExpansionMin:   3.1, // 10^(10/20) ≈ 3.16
			wantExpansionMax:   3.2,
			wantDenoiseEnabled: false,
			wantRMSApprox:      1.0, // clamped
			wantDescription:    "10dB gap = ~3.16x expansion",
		},
		{
			name:               "quiet source - moderate expansion",
			inputI:             -30.0,
			targetI:            -16.0,
			wantExpansionMin:   5.0, // 10^(14/20) ≈ 5.01
			wantExpansionMax:   5.1,
			wantDenoiseEnabled: false,
			wantRMSApprox:      1.0, // clamped
			wantDescription:    "14dB gap = ~5.01x expansion",
		},

		// Near threshold
		{
			name:               "approaching threshold - just below",
			inputI:             -33.0,
			targetI:            -16.0,
			wantExpansionMin:   7.0, // 10^(17/20) ≈ 7.08
			wantExpansionMax:   7.1,
			wantDenoiseEnabled: false,
			wantRMSApprox:      1.0, // clamped
			wantDescription:    "17dB gap = ~7.08x expansion, below 8x threshold",
		},

		// At and above threshold - denoise enabled
		{
			name:               "at threshold - denoise activated",
			inputI:             -34.0,
			targetI:            -16.0,
			wantExpansionMin:   7.9, // 10^(18/20) ≈ 7.94
			wantExpansionMax:   8.0,
			wantDenoiseEnabled: false, // 7.94 still below 8.0
			wantRMSApprox:      1.0,   // clamped
			wantDescription:    "18dB gap = ~7.94x, just at/below threshold",
		},
		{
			name:               "just above threshold",
			inputI:             -34.1,
			targetI:            -16.0,
			wantExpansionMin:   8.0, // 10^(18.1/20) ≈ 8.03
			wantExpansionMax:   8.1,
			wantDenoiseEnabled: true,
			wantRMSApprox:      1.0, // clamped
			wantDescription:    "18.1dB gap = ~8.03x, triggers denoise",
		},

		// Very quiet source - capped expansion
		{
			name:               "very quiet source - expansion capped",
			inputI:             -46.0,
			targetI:            -16.0,
			wantExpansionMin:   10.0, // capped to speechnormMaxExpansion
			wantExpansionMax:   10.0,
			wantDenoiseEnabled: true,
			wantRMSApprox:      1.0, // clamped
			wantDescription:    "30dB gap = 31.6x but capped to 10x",
		},
		{
			name:               "extremely quiet source - expansion capped",
			inputI:             -60.0,
			targetI:            -16.0,
			wantExpansionMin:   10.0, // capped
			wantExpansionMax:   10.0,
			wantDenoiseEnabled: true,
			wantRMSApprox:      1.0, // clamped
			wantDescription:    "44dB gap = 158x but capped to 10x",
		},

		// Different target LUFS values
		// For targetI=-24, RMS = 10^((-24+23)/20) = 10^-0.05 ≈ 0.891 (not clamped)
		{
			name:               "broadcast target (-24 LUFS)",
			inputI:             -30.0,
			targetI:            -24.0,
			wantExpansionMin:   1.9, // 10^(6/20) ≈ 2.0
			wantExpansionMax:   2.1,
			wantDenoiseEnabled: false,
			wantRMSApprox:      0.891, // 10^((-24+23)/20) = 10^-0.05 ≈ 0.891
			wantDescription:    "6dB gap to -24 LUFS target",
		},

		// Negative LUFS gap (loud source)
		{
			name:               "loud source - minimal expansion",
			inputI:             -12.0,
			targetI:            -16.0,
			wantExpansionMin:   1.0, // clamped to minimum 1.0
			wantExpansionMax:   1.0,
			wantDenoiseEnabled: false,
			wantRMSApprox:      1.0, // clamped
			wantDescription:    "-4dB gap = 0.63x but clamped to 1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &FilterChainConfig{
				TargetI: tt.targetI,
			}
			measurements := &AudioMeasurements{
				InputI: tt.inputI,
			}
			lufsGap := tt.targetI - tt.inputI

			tuneSpeechnorm(config, measurements, lufsGap)

			// Check expansion value
			if config.SpeechnormExpansion < tt.wantExpansionMin || config.SpeechnormExpansion > tt.wantExpansionMax {
				t.Errorf("SpeechnormExpansion = %v, want [%v, %v] - %s",
					config.SpeechnormExpansion, tt.wantExpansionMin, tt.wantExpansionMax,
					tt.wantDescription)
			}

			// Check denoise activation
			if config.ArnnDnEnabled != tt.wantDenoiseEnabled || config.AnlmDnEnabled != tt.wantDenoiseEnabled {
				t.Errorf("DenoiseEnabled (ArnnDn=%v, AnlmDn=%v), want both=%v - %s",
					config.ArnnDnEnabled, config.AnlmDnEnabled, tt.wantDenoiseEnabled,
					tt.wantDescription)
			}

			// Check RMS targeting (for non-zero input)
			if tt.inputI != 0.0 && tt.wantRMSApprox > 0 {
				tolerance := 0.01
				if config.SpeechnormRMS < tt.wantRMSApprox-tolerance ||
					config.SpeechnormRMS > tt.wantRMSApprox+tolerance {
					t.Errorf("SpeechnormRMS = %v, want ~%v - %s",
						config.SpeechnormRMS, tt.wantRMSApprox, tt.wantDescription)
				}
			}

			// Verify fixed parameters
			if tt.inputI != 0.0 {
				if config.SpeechnormPeak != 0.95 {
					t.Errorf("SpeechnormPeak = %v, want 0.95", config.SpeechnormPeak)
				}
				if config.SpeechnormCompression != 1.0 {
					t.Errorf("SpeechnormCompression = %v, want 1.0", config.SpeechnormCompression)
				}
				if config.SpeechnormRaise != 0.001 {
					t.Errorf("SpeechnormRaise = %v, want 0.001", config.SpeechnormRaise)
				}
				if config.SpeechnormFall != 0.001 {
					t.Errorf("SpeechnormFall = %v, want 0.001", config.SpeechnormFall)
				}
			}
		})
	}
}
