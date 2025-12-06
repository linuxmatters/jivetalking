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
			config := newTestConfig()
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

func TestTuneDS201HighPass(t *testing.T) {
	// Helper to create noise profile with given characteristics
	makeNoiseProfile := func(noiseFloor, entropy float64) *NoiseProfile {
		return &NoiseProfile{
			MeasuredNoiseFloor: noiseFloor,
			Entropy:            entropy,
		}
	}

	tests := []struct {
		name             string
		centroid         float64       // spectral centroid (Hz)
		spectralDecrease float64       // spectral decrease (negative = warm voice)
		spectralSkewness float64       // spectral skewness (positive = LF emphasis)
		noiseProfile     *NoiseProfile // silence sample characteristics
		wantFreqMin      float64       // minimum expected frequency
		wantFreqMax      float64       // maximum expected frequency
		wantPoles        int           // expected poles (0 = don't check, 1 = gentle, 2 = standard)
		wantWidth        float64       // expected Q (0 = don't check, uses Butterworth 0.707 default)
		wantMix          float64       // expected mix (0 = don't check, uses 1.0 default)
		wantDisabled     bool          // expect highpass to be disabled entirely (legacy, now rarely used)
	}{
		// Voice brightness classification (no noise profile - base frequencies only)
		{
			name:             "dark voice, no noise profile",
			centroid:         3500, // below centroidNormal (4000)
			spectralDecrease: 0.0,
			noiseProfile:     nil,
			wantFreqMin:      60, // highpassMinFreq
			wantFreqMax:      60,
		},
		{
			name:             "normal voice, no noise profile",
			centroid:         5000, // between centroidNormal (4000) and centroidBright (6000)
			spectralDecrease: 0.0,
			noiseProfile:     nil,
			wantFreqMin:      80, // highpassDefaultFreq
			wantFreqMax:      80,
		},
		{
			name:             "bright voice, no noise profile",
			centroid:         7000, // above centroidBright (6000)
			spectralDecrease: 0.0,
			noiseProfile:     nil,
			wantFreqMin:      100, // highpassBrightFreq
			wantFreqMax:      100,
		},

		// Clean silence sample (< -70 dBFS) - no boost regardless of entropy
		{
			name:             "normal voice, clean silence, broadband noise",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-75.0, 0.8), // clean, broadband
			wantFreqMin:      80,                           // no boost - too clean
			wantFreqMax:      80,
		},

		// Moderate noise (> -70 dBFS) with broadband character - moderate boost
		{
			name:             "normal voice, moderate noise, broadband",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-65.0, 0.7), // moderate, broadband
			wantFreqMin:      90,                           // 80 + 10 boost
			wantFreqMax:      90,
		},

		// Noisy silence (> -55 dBFS) with broadband character - aggressive boost
		{
			name:             "normal voice, noisy silence, broadband",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-50.0, 0.8), // noisy, broadband
			wantFreqMin:      100,                          // 80 + 20 boost
			wantFreqMax:      100,
		},

		// Tonal noise (low entropy) - no boost, bandreject handles it
		{
			name:             "normal voice, noisy silence, tonal (hum)",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-50.0, 0.3), // noisy but tonal
			wantFreqMin:      80,                           // no boost - tonal noise
			wantFreqMax:      80,
		},

		// Warm voice protection (spectral decrease < -0.05, tiered at -0.08)
		{
			name:             "warm voice, noisy broadband - capped at 80Hz",
			centroid:         5000,
			spectralDecrease: -0.06, // warm voice (between -0.05 and -0.08)
			noiseProfile:     makeNoiseProfile(-50.0, 0.8),
			wantFreqMin:      80, // would be 100, but capped at 80Hz due to warm voice
			wantFreqMax:      80,
			wantPoles:        0, // standard slope (don't check, defaults to 2)
		},
		{
			name:             "very warm voice - gentle highpass",
			centroid:         5000,                         // normal voice base = 80Hz
			spectralDecrease: -0.095,                       // very warm voice (< -0.08)
			noiseProfile:     makeNoiseProfile(-50.0, 0.8), // broadband noise
			wantFreqMin:      60,                           // highpassVeryWarmFreq
			wantFreqMax:      60,
			wantPoles:        1,   // gentle 6dB/oct
			wantWidth:        0.5, // gentle Q
			wantMix:          0.8, // 80% wet
		},
		{
			name:             "very warm dark voice - gentle highpass",
			centroid:         3500,                         // dark voice base = 60Hz
			spectralDecrease: -0.15,                        // very warm (< -0.08)
			noiseProfile:     makeNoiseProfile(-45.0, 0.9), // broadband noise
			wantFreqMin:      60,                           // highpassVeryWarmFreq
			wantFreqMax:      60,
			wantPoles:        1,   // gentle 6dB/oct
			wantWidth:        0.5, // gentle Q
			wantMix:          0.8, // 80% wet
		},

		// Bright voice with warm spectral decrease (unusual but possible)
		{
			name:             "bright voice, warm characteristics - capped at 80Hz",
			centroid:         7000,
			spectralDecrease: -0.06, // warm despite bright centroid (between -0.05 and -0.08)
			noiseProfile:     makeNoiseProfile(-50.0, 0.8),
			wantFreqMin:      80, // would be 120, but capped at 80Hz due to warm voice
			wantFreqMax:      80,
			wantPoles:        0, // standard slope (don't check)
		},
		{
			name:             "bright voice, very warm characteristics - gentle highpass",
			centroid:         7000,
			spectralDecrease: -0.10,                        // very warm despite bright centroid (< -0.08)
			noiseProfile:     makeNoiseProfile(-50.0, 0.8), // broadband noise
			wantFreqMin:      60,                           // highpassVeryWarmFreq
			wantFreqMax:      60,
			wantPoles:        1,   // gentle 6dB/oct
			wantWidth:        0.5, // gentle Q
			wantMix:          0.8, // 80% wet
		},

		// Skewness-based protection (moderate decrease but LF emphasis)
		{
			name:             "Mark's voice profile - moderate decrease, high skewness - warm highpass",
			centroid:         5785,                           // bright centroid
			spectralDecrease: -0.026,                         // moderate decrease (between -0.05 and 0)
			spectralSkewness: 1.132,                          // LF emphasis (> 1.0)
			noiseProfile:     makeNoiseProfile(-80.0, 0.076), // tonal noise
			wantFreqMin:      70,                             // highpassWarmFreq for LF emphasis
			wantFreqMax:      70,
			wantPoles:        1,   // gentle 6dB/oct
			wantWidth:        0.5, // gentle Q
			wantMix:          0.9, // 90% wet
		},
		{
			name:             "moderate decrease, low skewness - standard slope",
			centroid:         5000,
			spectralDecrease: -0.03,                        // moderate decrease
			spectralSkewness: 0.8,                          // NOT LF emphasis (< 1.0)
			noiseProfile:     makeNoiseProfile(-75.0, 0.3), // clean, tonal - no boost
			wantFreqMin:      80,
			wantFreqMax:      80,
			wantPoles:        2,
			wantDisabled:     false, // skewness < 1.0, highpass at normal settings
		},
		{
			name:             "balanced decrease, high skewness - warm highpass",
			centroid:         4500,
			spectralDecrease: -0.01,                        // balanced (between -0.05 and 0)
			spectralSkewness: 1.5,                          // strong LF emphasis
			noiseProfile:     makeNoiseProfile(-75.0, 0.3), // clean, tonal - no boost
			wantFreqMin:      70,                           // highpassWarmFreq for LF emphasis
			wantFreqMax:      70,
			wantPoles:        1,   // gentle 6dB/oct
			wantWidth:        0.5, // gentle Q
			wantMix:          0.9, // 90% wet
		},
		{
			name:             "thin voice, high skewness - skewness still triggers warm protection",
			centroid:         6500,                         // > 6000 centroidBright threshold
			spectralDecrease: 0.02,                         // thin voice (> 0)
			spectralSkewness: 1.2,                          // > 1.0 triggers warm protection regardless of decrease
			noiseProfile:     makeNoiseProfile(-75.0, 0.3), // clean, tonal - no boost
			wantFreqMin:      70,                           // highpassWarmFreq (skewness overrides centroid)
			wantFreqMax:      70,
			wantPoles:        1,   // gentle 6dB/oct
			wantWidth:        0.5, // gentle Q
			wantMix:          0.9, // 90% wet
		},

		// Edge cases
		{
			name:             "no spectral data - keeps default",
			centroid:         0, // triggers early return
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-50.0, 0.8),
			wantFreqMin:      80, // DefaultFilterConfig().DS201HPFreq
			wantFreqMax:      80,
		},
		{
			name:             "negative centroid - keeps default",
			centroid:         -100,
			spectralDecrease: 0.0,
			noiseProfile:     nil,
			wantFreqMin:      80,
			wantFreqMax:      80,
		},
		{
			name:             "boundary: exactly at centroidNormal",
			centroid:         4000, // exactly at centroidNormal threshold
			spectralDecrease: 0.0,
			noiseProfile:     nil,
			wantFreqMin:      60, // dark voice (not > centroidNormal)
			wantFreqMax:      60,
		},
		{
			name:             "boundary: exactly at centroidBright",
			centroid:         6000, // exactly at centroidBright threshold
			spectralDecrease: 0.0,
			noiseProfile:     nil,
			wantFreqMin:      80, // normal voice (not > centroidBright)
			wantFreqMax:      80,
		},
		{
			name:             "boundary: exactly at silenceEntropyTonal",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-50.0, 0.5), // exactly at threshold
			wantFreqMin:      100,                          // broadband (>= 0.5), gets boost
			wantFreqMax:      100,
		},
		{
			name:             "boundary: exactly at silenceNoiseFloorClean",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-70.0, 0.8), // exactly at clean threshold
			wantFreqMin:      80,                           // no boost (not > -70)
			wantFreqMax:      80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup: create test config and measurements
			config := newTestConfig()
			// Start with highpass enabled to test tuning behavior
			config.DS201HPEnabled = true
			measurements := &AudioMeasurements{
				SpectralCentroid: tt.centroid,
				SpectralDecrease: tt.spectralDecrease,
				SpectralSkewness: tt.spectralSkewness,
				NoiseProfile:     tt.noiseProfile,
			}

			// Execute (lufsGap is no longer used for highpass tuning)
			tuneDS201HighPass(config, measurements, 0.0)

			// Verify disabled state (legacy - now rarely used)
			if tt.wantDisabled {
				if config.DS201HPEnabled {
					t.Errorf("DS201HPEnabled = true, want false")
				}
				return // no further checks needed for disabled
			}

			// Verify enabled (warm voices now use gentle settings instead of disabling)
			if !config.DS201HPEnabled {
				t.Errorf("DS201HPEnabled = false, want true")
			}

			// Verify frequency
			if config.DS201HPFreq < tt.wantFreqMin || config.DS201HPFreq > tt.wantFreqMax {
				t.Errorf("DS201HPFreq = %.1f Hz, want [%.1f, %.1f] Hz",
					config.DS201HPFreq, tt.wantFreqMin, tt.wantFreqMax)
			}

			// Verify poles (slope) if specified
			if tt.wantPoles > 0 && config.DS201HPPoles != tt.wantPoles {
				t.Errorf("DS201HPPoles = %d, want %d", config.DS201HPPoles, tt.wantPoles)
			}

			// Verify width (Q) if specified
			if tt.wantWidth > 0 && config.DS201HPWidth != tt.wantWidth {
				t.Errorf("DS201HPWidth = %.3f, want %.3f", config.DS201HPWidth, tt.wantWidth)
			}

			// Verify mix if specified
			if tt.wantMix > 0 && config.DS201HPMix != tt.wantMix {
				t.Errorf("DS201HPMix = %.2f, want %.2f", config.DS201HPMix, tt.wantMix)
			}
		})
	}
}

func TestTuneDS201LowPass(t *testing.T) {
	// Constants from adaptive.go for reference:
	// ds201LPDefaultFreq = 16000.0 Hz
	// ds201LPMinFreq     = 8000.0 Hz
	// ds201LPHeadroom    = 2000.0 Hz
	// HF noise detection: ZCR > 0.15 AND centroid < 3000 Hz

	tests := []struct {
		name        string
		rolloff     float64 // spectral rolloff (Hz)
		zcr         float64 // zero crossings rate
		centroid    float64 // spectral centroid (Hz)
		wantEnabled bool
		wantFreqMin float64 // minimum expected frequency (0 = don't check)
		wantFreqMax float64 // maximum expected frequency (0 = don't check)
		desc        string
	}{
		{
			name:        "dark voice - LP disabled",
			rolloff:     6000,
			zcr:         0.05,
			centroid:    2500,
			wantEnabled: false,
			desc:        "rolloff < 8000 Hz means voice is already dark, no LP needed",
		},
		{
			name:        "normal voice - LP disabled",
			rolloff:     10000,
			zcr:         0.08,
			centroid:    5000,
			wantEnabled: false,
			desc:        "rolloff between 8000-14000 Hz with normal ZCR, no clear benefit",
		},
		{
			name:        "high rolloff - ultrasonics filtered",
			rolloff:     15000,
			zcr:         0.05,
			centroid:    5000,
			wantEnabled: true,
			wantFreqMin: 16000, // rolloff + headroom = 17000, capped at 16000
			wantFreqMax: 16000,
			desc:        "rolloff > 14000 Hz enables LP to filter ultrasonics",
		},
		{
			name:        "very high rolloff",
			rolloff:     14500,
			zcr:         0.05,
			centroid:    5000,
			wantEnabled: true,
			wantFreqMin: 16000, // 14500 + 2000 = 16500, capped at 16000
			wantFreqMax: 16000,
			desc:        "rolloff > 14000 sets cutoff at rolloff + headroom (capped at 16000)",
		},
		{
			name:        "HF noise detected",
			rolloff:     10000,
			zcr:         0.20, // > 0.15
			centroid:    2500, // < 3000
			wantEnabled: true,
			wantFreqMin: 12000,
			wantFreqMax: 12000,
			desc:        "high ZCR + low centroid pattern indicates HF noise",
		},
		{
			name:        "high ZCR but bright voice",
			rolloff:     10000,
			zcr:         0.20,
			centroid:    5000, // > 3000
			wantEnabled: false,
			desc:        "high ZCR with normal centroid is speech, not noise",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestConfig()
			measurements := &AudioMeasurements{
				SpectralRolloff:   tt.rolloff,
				ZeroCrossingsRate: tt.zcr,
				SpectralCentroid:  tt.centroid,
			}

			tuneDS201LowPass(config, measurements)

			if config.DS201LPEnabled != tt.wantEnabled {
				t.Errorf("DS201LPEnabled = %v, want %v [%s]",
					config.DS201LPEnabled, tt.wantEnabled, tt.desc)
			}

			if tt.wantEnabled && tt.wantFreqMin > 0 {
				if config.DS201LPFreq < tt.wantFreqMin || config.DS201LPFreq > tt.wantFreqMax {
					t.Errorf("DS201LPFreq = %.0f Hz, want %.0f-%.0f Hz [%s]",
						config.DS201LPFreq, tt.wantFreqMin, tt.wantFreqMax, tt.desc)
				}
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
			config := newTestConfig()
			// Start with deesser disabled - tuneDeesser should set intensity based on spectral data
			config.DeessIntensity = 0.0
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

func TestTuneDS201Gate(t *testing.T) {
	// Tests the comprehensive gate tuning which calculates all gate parameters
	// based on measurements including NoiseProfile (silence sample analysis).
	//
	// Key constants from adaptive.go:
	// gateThresholdMinDB = -70.0 dB (professional studio floor)
	// gateThresholdMaxDB = -25.0 dB (never gate above this - would cut speech)
	// gateCrestFactorThreshold = 20.0 dB (when to use peak vs floor)
	// gateHeadroomClean = 3.0 dB, gateHeadroomModerate = 6.0 dB, gateHeadroomNoisy = 10.0 dB
	// gateRatioGentle = 1.5, gateRatioMod = 2.0, gateRatioTight = 2.5

	t.Run("threshold calculation", func(t *testing.T) {
		tests := []struct {
			name            string
			noiseFloor      float64 // dB
			silencePeak     float64 // dB
			silenceCrest    float64 // dB - determines if we use peak or floor
			wantThresholdDB float64 // expected threshold dB
			tolerance       float64 // tolerance in dB
			desc            string
		}{
			{
				name:            "clean studio - uses floor + 3dB headroom",
				noiseFloor:      -75.0,
				silencePeak:     -70.0,
				silenceCrest:    10.0,  // Low crest = stable noise, use floor
				wantThresholdDB: -70.0, // Clamped to min (-70)
				tolerance:       1.0,
				desc:            "very clean, clamped to min",
			},
			{
				name:            "typical podcast - uses floor + 6dB headroom",
				noiseFloor:      -55.0,
				silencePeak:     -50.0,
				silenceCrest:    10.0,  // Low crest = stable noise
				wantThresholdDB: -49.0, // -55 + 6dB headroom (moderate: ref < -50)
				tolerance:       1.0,
				desc:            "moderate noise floor",
			},
			{
				name:            "noisy room - uses floor + 10dB headroom",
				noiseFloor:      -42.0,
				silencePeak:     -38.0,
				silenceCrest:    10.0,
				wantThresholdDB: -32.0, // -42 + 10dB headroom (noisy: ref >= -50)
				tolerance:       1.0,
				desc:            "noisy floor needs generous headroom",
			},
			{
				name:            "bleed with high crest - uses peak + headroom",
				noiseFloor:      -55.0,
				silencePeak:     -48.0, // Transient spikes
				silenceCrest:    25.0,  // High crest = transient bleed
				wantThresholdDB: -38.0, // -48 (peak) + 10dB headroom (peak >= -50)
				tolerance:       1.0,
				desc:            "high crest factor triggers peak reference",
			},
			{
				name:            "extreme noise - clamped to max",
				noiseFloor:      -20.0,
				silencePeak:     -15.0,
				silenceCrest:    25.0,
				wantThresholdDB: -25.0, // Clamped to max
				tolerance:       0.5,
				desc:            "threshold capped to avoid cutting speech",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					NoiseFloor: tt.noiseFloor,
					NoiseProfile: &NoiseProfile{
						PeakLevel:   tt.silencePeak,
						CrestFactor: tt.silenceCrest,
						Entropy:     0.5, // Moderate entropy
					},
				}

				tuneDS201Gate(config, measurements)

				actualDB := linearToDB(config.DS201GateThreshold)
				diff := actualDB - tt.wantThresholdDB
				if diff < 0 {
					diff = -diff
				}
				if diff > tt.tolerance {
					t.Errorf("DS201GateThreshold = %.1f dB, want %.1f dB ±%.1f [%s]",
						actualDB, tt.wantThresholdDB, tt.tolerance, tt.desc)
				}
			})
		}
	})

	t.Run("ratio based on LRA", func(t *testing.T) {
		// LRA thresholds: gateLRAWide=15 LU, gateLRAModerate=10 LU
		// Ratios: gateRatioGentle=1.5, gateRatioMod=2.0, gateRatioTight=2.5
		tests := []struct {
			name      string
			lra       float64
			wantRatio float64
			desc      string
		}{
			{"wide dynamics", 18.0, 1.5, "gentle ratio for expressive speech"},
			{"moderate dynamics", 12.0, 2.0, "moderate ratio"},
			{"narrow dynamics", 6.0, 2.5, "tighter ratio for compressed audio"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					NoiseFloor: -55.0,
					InputLRA:   tt.lra,
				}

				tuneDS201Gate(config, measurements)

				if config.DS201GateRatio != tt.wantRatio {
					t.Errorf("DS201GateRatio = %.1f, want %.1f [%s]", config.DS201GateRatio, tt.wantRatio, tt.desc)
				}
			})
		}
	})

	t.Run("attack based on transients", func(t *testing.T) {
		// gateMaxDiffHigh = 25%, gateMaxDiffMod = 10%
		// gateAttackFast = 7ms, gateAttackMod = 12ms, gateAttackSlow = 17ms
		// gateFluxDynamicThres = 0.05 (above: apply 0.8 multiplier)
		tests := []struct {
			name         string
			maxDiff      float64 // 0-1 range (MaxDifference)
			spectralFlux float64
			wantAttackMS float64
			tolerance    float64
			desc         string
		}{
			{"fast transients", 0.3, 1.0, 5.6, 1.0, "fast attack (7*0.8) for sharp transients with dynamic flux"},
			{"slow transients no flux", 0.05, 0.02, 17.0, 1.0, "slow attack 17ms for gentle speech with low flux"},
			{"moderate with flux", 0.15, 0.1, 9.6, 1.0, "moderate attack (12*0.8) with dynamic flux"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					NoiseFloor:    -55.0,
					MaxDifference: tt.maxDiff,
					SpectralFlux:  tt.spectralFlux,
				}

				tuneDS201Gate(config, measurements)

				diff := config.DS201GateAttack - tt.wantAttackMS
				if diff < 0 {
					diff = -diff
				}
				if diff > tt.tolerance {
					t.Errorf("DS201GateAttack = %.1f ms, want %.1f ms ±%.1f [%s]",
						config.DS201GateAttack, tt.wantAttackMS, tt.tolerance, tt.desc)
				}
			})
		}
	})

	t.Run("detection mode based on noise character", func(t *testing.T) {
		// Detection uses RMS for tonal or spiky noise, peak for clean
		// gateEntropyTonal = 0.3, gateSilenceCrestThreshold = 25.0
		// gateEntropyClean = 0.7 (above this + crest < 15 = peak)
		tests := []struct {
			name           string
			silenceEntropy float64
			silenceCrest   float64
			wantDetection  string
			desc           string
		}{
			{"tonal noise", 0.2, 10.0, "rms", "low entropy = tonal, use RMS"},
			{"transient bleed", 0.5, 28.0, "rms", "high crest > 25 = bleed spikes, use RMS"},
			{"clean recording", 0.8, 8.0, "peak", "entropy > 0.7 + crest < 15 = peak"},
			{"borderline case", 0.5, 20.0, "rms", "moderate entropy + crest, defaults to RMS"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					NoiseFloor: -55.0,
					NoiseProfile: &NoiseProfile{
						PeakLevel:   -55.0,
						CrestFactor: tt.silenceCrest,
						Entropy:     tt.silenceEntropy,
					},
				}

				tuneDS201Gate(config, measurements)

				if config.DS201GateDetection != tt.wantDetection {
					t.Errorf("DS201GateDetection = %q, want %q [%s]",
						config.DS201GateDetection, tt.wantDetection, tt.desc)
				}
			})
		}
	})

	t.Run("range based on entropy", func(t *testing.T) {
		// gateEntropyTonal=0.3, gateEntropyMixed=0.6
		// gateRangeTonalDB=-16, gateRangeMixedDB=-21, gateRangeBroadbandDB=-27
		tests := []struct {
			name        string
			entropy     float64
			noiseFloor  float64
			wantRangeDB float64
			tolerance   float64
			desc        string
		}{
			{"tonal noise - gentle range", 0.2, -55.0, -16.0, 2.0, "low entropy = tonal, gentle range"},
			{"mixed noise - moderate range", 0.5, -55.0, -21.0, 2.0, "mixed entropy, moderate range"},
			{"broadband noise - aggressive", 0.7, -55.0, -27.0, 2.0, "high entropy, aggressive range"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					NoiseFloor: tt.noiseFloor,
					NoiseProfile: &NoiseProfile{
						PeakLevel:   tt.noiseFloor + 5,
						CrestFactor: 10.0,
						Entropy:     tt.entropy,
					},
				}

				tuneDS201Gate(config, measurements)

				actualDB := linearToDB(config.DS201GateRange)
				diff := actualDB - tt.wantRangeDB
				if diff < 0 {
					diff = -diff
				}
				if diff > tt.tolerance {
					t.Errorf("DS201GateRange = %.1f dB, want %.1f dB ±%.1f [%s]",
						actualDB, tt.wantRangeDB, tt.tolerance, tt.desc)
				}
			})
		}
	})

	t.Run("handles nil NoiseProfile gracefully", func(t *testing.T) {
		config := newTestConfig()
		measurements := &AudioMeasurements{
			NoiseFloor: -55.0,
			InputLRA:   12.0,
			// NoiseProfile is nil
		}

		// Should not panic
		tuneDS201Gate(config, measurements)

		// Should still calculate threshold from noise floor
		thresholdDB := linearToDB(config.DS201GateThreshold)
		if thresholdDB < -70 || thresholdDB > -25 {
			t.Errorf("DS201GateThreshold = %.1f dB, want within bounds [-70, -25]", thresholdDB)
		}

		// Detection should default to RMS when no profile
		if config.DS201GateDetection != "rms" {
			t.Errorf("DS201GateDetection = %q, want 'rms' (default for missing profile)", config.DS201GateDetection)
		}
	})
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
	// defaultLA2ARatio      = 2.5
	// defaultLA2AThreshold  = -20.0
	// defaultLA2AMakeup     = 3.0
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
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
		},

		// NaN in each field
		{
			name: "NaN HighpassFreq gets default",
			config: FilterChainConfig{
				DS201HPFreq:        math.NaN(),
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        80.0, // defaultHighpassFreq
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
		},
		{
			name: "NaN DeessIntensity gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     math.NaN(),
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.0, // defaultDeessIntensity
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
		},
		{
			name: "NaN NoiseReduction gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     math.NaN(),
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     12.0, // defaultNoiseReduction
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
		},
		{
			name: "NaN LA2ARatio gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          math.NaN(),
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0, // defaultLA2ARatio (LA-2A inspired)
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
		},
		{
			name: "NaN LA2AThreshold gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      math.NaN(),
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -18.0, // defaultLA2AThreshold (LA-2A inspired)
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
		},
		{
			name: "NaN LA2AMakeup gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         math.NaN(),
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         2.0, // defaultLA2AMakeup (LA-2A inspired)
				DS201GateThreshold: 0.02,
			},
		},
		{
			name: "NaN GateThreshold gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: math.NaN(),
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.01, // defaultGateThreshold
			},
		},

		// Inf cases
		{
			name: "positive Inf values get defaults",
			config: FilterChainConfig{
				DS201HPFreq:        math.Inf(1),
				DeessIntensity:     math.Inf(1),
				NoiseReduction:     math.Inf(1),
				LA2ARatio:          math.Inf(1),
				LA2AThreshold:      math.Inf(1),
				LA2AMakeup:         math.Inf(1),
				DS201GateThreshold: math.Inf(1),
			},
			want: FilterChainConfig{
				DS201HPFreq:        80.0,
				DeessIntensity:     0.0,
				NoiseReduction:     12.0,
				LA2ARatio:          3.0,   // LA-2A inspired
				LA2AThreshold:      -18.0, // LA-2A inspired
				LA2AMakeup:         2.0,   // LA-2A inspired
				DS201GateThreshold: 0.01,
			},
		},
		{
			name: "negative Inf values get defaults",
			config: FilterChainConfig{
				DS201HPFreq:        math.Inf(-1),
				DeessIntensity:     math.Inf(-1),
				NoiseReduction:     math.Inf(-1),
				LA2ARatio:          math.Inf(-1),
				LA2AThreshold:      math.Inf(-1),
				LA2AMakeup:         math.Inf(-1),
				DS201GateThreshold: math.Inf(-1),
			},
			want: FilterChainConfig{
				DS201HPFreq:        80.0,
				DeessIntensity:     0.0,
				NoiseReduction:     12.0,
				LA2ARatio:          3.0,   // LA-2A inspired
				LA2AThreshold:      -18.0, // LA-2A inspired
				LA2AMakeup:         2.0,   // LA-2A inspired
				DS201GateThreshold: 0.01,
			},
		},

		// GateThreshold special cases: zero and negative get default
		// (other fields allow zero/negative values)
		{
			name: "zero GateThreshold gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.0, // zero is valid for DeessIntensity
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.0, // zero is NOT valid for GateThreshold
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.0,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.01, // defaultGateThreshold
			},
		},
		{
			name: "negative GateThreshold gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: -0.5, // negative is NOT valid for GateThreshold
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.01, // defaultGateThreshold
			},
		},

		// Zero values for other fields pass through
		// (sanitizeFloat doesn't treat zero specially)
		{
			name: "zero values for non-GateThreshold fields pass through",
			config: FilterChainConfig{
				DS201HPFreq:        0.0, // passes through (edge case: probably invalid, but sanitize doesn't clamp)
				DeessIntensity:     0.0, // valid: de-essing disabled
				NoiseReduction:     0.0, // passes through (edge case: no reduction)
				LA2ARatio:          0.0, // passes through (edge case: probably invalid)
				LA2AThreshold:      0.0, // passes through (0 dB threshold)
				LA2AMakeup:         0.0, // passes through (0 dB makeup)
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        0.0,
				DeessIntensity:     0.0,
				NoiseReduction:     0.0,
				LA2ARatio:          0.0,
				LA2AThreshold:      0.0,
				LA2AMakeup:         0.0,
				DS201GateThreshold: 0.02,
			},
		},

		// Negative values for fields that legitimately use them
		{
			name: "negative LA2AThreshold passes through (valid dB value)",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -40.0, // very aggressive threshold
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -40.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 0.02,
			},
		},

		// All fields NaN - complete fallback to defaults
		{
			name: "all NaN values get all defaults",
			config: FilterChainConfig{
				DS201HPFreq:        math.NaN(),
				DeessIntensity:     math.NaN(),
				NoiseReduction:     math.NaN(),
				LA2ARatio:          math.NaN(),
				LA2AThreshold:      math.NaN(),
				LA2AMakeup:         math.NaN(),
				DS201GateThreshold: math.NaN(),
			},
			want: FilterChainConfig{
				DS201HPFreq:        80.0,
				DeessIntensity:     0.0,
				NoiseReduction:     12.0,
				LA2ARatio:          3.0,   // LA-2A inspired
				LA2AThreshold:      -18.0, // LA-2A inspired
				LA2AMakeup:         2.0,   // LA-2A inspired
				DS201GateThreshold: 0.01,
			},
		},

		// Very small positive GateThreshold passes through
		{
			name: "very small positive GateThreshold passes through",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 1e-10, // very small but positive
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				NoiseReduction:     18.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				LA2AMakeup:         4.0,
				DS201GateThreshold: 1e-10,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy to avoid mutating test data
			config := tt.config
			sanitizeConfig(&config)

			// Check each field
			if config.DS201HPFreq != tt.want.DS201HPFreq {
				t.Errorf("DS201HPFreq = %v, want %v", config.DS201HPFreq, tt.want.DS201HPFreq)
			}
			if config.DeessIntensity != tt.want.DeessIntensity {
				t.Errorf("DeessIntensity = %v, want %v", config.DeessIntensity, tt.want.DeessIntensity)
			}
			if config.NoiseReduction != tt.want.NoiseReduction {
				t.Errorf("NoiseReduction = %v, want %v", config.NoiseReduction, tt.want.NoiseReduction)
			}
			if config.LA2ARatio != tt.want.LA2ARatio {
				t.Errorf("LA2ARatio = %v, want %v", config.LA2ARatio, tt.want.LA2ARatio)
			}
			if config.LA2AThreshold != tt.want.LA2AThreshold {
				t.Errorf("LA2AThreshold = %v, want %v", config.LA2AThreshold, tt.want.LA2AThreshold)
			}
			if config.LA2AMakeup != tt.want.LA2AMakeup {
				t.Errorf("LA2AMakeup = %v, want %v", config.LA2AMakeup, tt.want.LA2AMakeup)
			}
			if config.DS201GateThreshold != tt.want.DS201GateThreshold {
				t.Errorf("DS201GateThreshold = %v, want %v", config.DS201GateThreshold, tt.want.DS201GateThreshold)
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
	// Tests for tuneSpeechnormDenoise which is now DEPRECATED
	// arnndn tuning is now handled by tuneArnndn based on noise floor + spectral characteristics
	// tuneSpeechnormDenoise is kept for backwards compatibility but is essentially a no-op
	//
	// The only behaviour we test is that it respects the enabled state passed in

	t.Run("respects user disabled state", func(t *testing.T) {
		config := &FilterChainConfig{ArnnDnEnabled: false}
		tuneSpeechnormDenoise(config, 50.0) // High expansion would normally enable (in old code)

		if config.ArnnDnEnabled {
			t.Error("ArnnDnEnabled should stay false when user disabled it")
		}
	})

	t.Run("respects user enabled state", func(t *testing.T) {
		config := &FilterChainConfig{ArnnDnEnabled: true}
		tuneSpeechnormDenoise(config, 1.0) // Low expansion

		// With new architecture, tuneSpeechnormDenoise is a no-op
		// The enabled state should remain unchanged
		if !config.ArnnDnEnabled {
			t.Error("ArnnDnEnabled should stay true - tuneSpeechnormDenoise is now a no-op")
		}
	})
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
	//
	// Note: tuneSpeechnorm no longer controls ArnnDnEnabled - that's handled by tuneArnndn

	tests := []struct {
		name             string
		inputI           float64 // measured input LUFS
		targetI          float64 // target LUFS
		wantExpansionMin float64
		wantExpansionMax float64
		wantRMSApprox    float64 // expected RMS after clamping to [0, 1]
		wantDescription  string
	}{
		// Zero input LUFS - early return
		{
			name:             "zero input LUFS - early return",
			inputI:           0.0,
			targetI:          -16.0,
			wantExpansionMin: 0.0, // unchanged from default
			wantExpansionMax: 0.0,
			wantRMSApprox:    0.0, // unchanged (early return)
			wantDescription:  "zero input LUFS triggers early return, no changes",
		},

		// Normal expansion cases
		// Note: For targetI=-16, RMS = 10^((-16+23)/20) = 10^0.35 ≈ 2.238 → clamped to 1.0
		{
			name:             "near target - minimal expansion",
			inputI:           -18.0,
			targetI:          -16.0,
			wantExpansionMin: 1.2, // 10^(2/20) ≈ 1.26
			wantExpansionMax: 1.3,
			wantRMSApprox:    1.0, // 10^(7/20) ≈ 2.238 clamped to 1.0
			wantDescription:  "2dB gap = ~1.26x expansion",
		},
		{
			name:             "typical podcast source",
			inputI:           -26.0,
			targetI:          -16.0,
			wantExpansionMin: 3.1, // 10^(10/20) ≈ 3.16
			wantExpansionMax: 3.2,
			wantRMSApprox:    1.0, // clamped
			wantDescription:  "10dB gap = ~3.16x expansion",
		},
		{
			name:             "quiet source - moderate expansion",
			inputI:           -30.0,
			targetI:          -16.0,
			wantExpansionMin: 5.0, // 10^(14/20) ≈ 5.01
			wantExpansionMax: 5.1,
			wantRMSApprox:    1.0, // clamped
			wantDescription:  "14dB gap = ~5.01x expansion",
		},

		// Near threshold
		{
			name:             "approaching threshold - just below",
			inputI:           -33.0,
			targetI:          -16.0,
			wantExpansionMin: 7.0, // 10^(17/20) ≈ 7.08
			wantExpansionMax: 7.1,
			wantRMSApprox:    1.0, // clamped
			wantDescription:  "17dB gap = ~7.08x expansion",
		},

		// At and above old threshold (now just expansion tests)
		{
			name:             "at old threshold",
			inputI:           -34.0,
			targetI:          -16.0,
			wantExpansionMin: 7.9, // 10^(18/20) ≈ 7.94
			wantExpansionMax: 8.0,
			wantRMSApprox:    1.0, // clamped
			wantDescription:  "18dB gap = ~7.94x",
		},
		{
			name:             "just above old threshold",
			inputI:           -34.1,
			targetI:          -16.0,
			wantExpansionMin: 8.0, // 10^(18.1/20) ≈ 8.03
			wantExpansionMax: 8.1,
			wantRMSApprox:    1.0, // clamped
			wantDescription:  "18.1dB gap = ~8.03x",
		},

		// Very quiet source - capped expansion
		{
			name:             "very quiet source - expansion capped",
			inputI:           -46.0,
			targetI:          -16.0,
			wantExpansionMin: 10.0, // capped to speechnormMaxExpansion
			wantExpansionMax: 10.0,
			wantRMSApprox:    1.0, // clamped
			wantDescription:  "30dB gap = 31.6x but capped to 10x",
		},
		{
			name:             "extremely quiet source - expansion capped",
			inputI:           -60.0,
			targetI:          -16.0,
			wantExpansionMin: 10.0, // capped
			wantExpansionMax: 10.0,
			wantRMSApprox:    1.0, // clamped
			wantDescription:  "44dB gap = 158x but capped to 10x",
		},

		// Different target LUFS values
		// For targetI=-24, RMS = 10^((-24+23)/20) = 10^-0.05 ≈ 0.891 (not clamped)
		{
			name:             "broadcast target (-24 LUFS)",
			inputI:           -30.0,
			targetI:          -24.0,
			wantExpansionMin: 1.9, // 10^(6/20) ≈ 2.0
			wantExpansionMax: 2.1,
			wantRMSApprox:    0.891, // 10^((-24+23)/20) = 10^-0.05 ≈ 0.891
			wantDescription:  "6dB gap to -24 LUFS target",
		},

		// Negative LUFS gap (loud source)
		{
			name:             "loud source - minimal expansion",
			inputI:           -12.0,
			targetI:          -16.0,
			wantExpansionMin: 1.0, // clamped to minimum 1.0
			wantExpansionMax: 1.0,
			wantRMSApprox:    1.0, // clamped
			wantDescription:  "-4dB gap = 0.63x but clamped to 1.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &FilterChainConfig{
				TargetI:       tt.targetI,
				ArnnDnEnabled: true, // Start enabled - tuneSpeechnorm no longer changes this
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

			// Note: ArnnDnEnabled is no longer checked here - tuneArnndn handles that

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
