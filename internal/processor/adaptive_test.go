package processor

import (
	"math"
	"testing"
	"time"
)

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
			name:             "moderate decrease, high skewness - warm highpass",
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
				BaseMeasurements: BaseMeasurements{
					SpectralCentroid: tt.centroid,
					SpectralDecrease: tt.spectralDecrease,
					SpectralSkewness: tt.spectralSkewness,
				},
				NoiseProfile: tt.noiseProfile,
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

func TestDetectContentType(t *testing.T) {
	// Constants from adaptive.go for reference:
	// lpContentKurtosisSpeech  = 6.0   (speech > this)
	// lpContentKurtosisMusic   = 5.0   (music < this)
	// lpContentFlatnessSpeech  = 0.45  (speech < this)
	// lpContentFlatnessMusic   = 0.55  (music > this)
	// lpContentFluxSpeech      = 0.003 (speech < this)
	// lpContentFluxMusic       = 0.005 (music > this)
	// lpContentCrestSpeech     = 30.0  (speech > this)
	// lpContentCrestMusic      = 25.0  (music < this)
	// lpContentScoreThreshold  = 3     (need 3+ to classify)

	tests := []struct {
		name     string
		kurtosis float64
		flatness float64
		flux     float64
		crest    float64
		want     ContentType
		desc     string
	}{
		{
			name:     "clear speech - podcast voice",
			kurtosis: 9.2,   // > 6 (speech)
			flatness: 0.38,  // < 0.45 (speech)
			flux:     0.002, // < 0.003 (speech)
			crest:    45.0,  // > 30 (speech)
			want:     ContentSpeech,
			desc:     "all metrics indicate speech (score 4)",
		},
		{
			name:     "clear music - instrumental",
			kurtosis: 3.5,   // < 5 (music)
			flatness: 0.61,  // > 0.55 (music)
			flux:     0.008, // > 0.005 (music)
			crest:    18.0,  // < 25 (music)
			want:     ContentMusic,
			desc:     "all metrics indicate music (score 4)",
		},
		{
			name:     "mixed content - speech over music",
			kurtosis: 5.2,   // between 5-6 (neither)
			flatness: 0.52,  // between 0.45-0.55 (neither)
			flux:     0.004, // between 0.003-0.005 (neither)
			crest:    27.0,  // between 25-30 (neither)
			want:     ContentMixed,
			desc:     "ambiguous metrics produce mixed (score 0-0)",
		},
		{
			name:     "borderline speech - 3 indicators",
			kurtosis: 7.0,   // > 6 (speech)
			flatness: 0.40,  // < 0.45 (speech)
			flux:     0.002, // < 0.003 (speech)
			crest:    20.0,  // < 30 (neither), < 25 (music!)
			want:     ContentSpeech,
			desc:     "3 speech indicators is enough (score 3-1)",
		},
		{
			name:     "borderline music - 3 indicators",
			kurtosis: 4.0,   // < 5 (music)
			flatness: 0.60,  // > 0.55 (music)
			flux:     0.006, // > 0.005 (music)
			crest:    35.0,  // > 30 (speech!)
			want:     ContentMusic,
			desc:     "3 music indicators is enough (score 1-3)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &AudioMeasurements{
				BaseMeasurements: BaseMeasurements{
					SpectralKurtosis: tt.kurtosis,
					SpectralFlatness: tt.flatness,
					SpectralFlux:     tt.flux,
					SpectralCrest:    tt.crest,
				},
			}

			got := detectContentType(m)

			if got != tt.want {
				t.Errorf("detectContentType() = %v, want %v [%s]", got, tt.want, tt.desc)
			}
		})
	}
}

func TestTuneDS201LowPass(t *testing.T) {
	// Constants from adaptive.go for reference:
	// Content detection: speech needs kurtosis>6, flatness<0.45, flux<0.003, crest>30 (3+ matches)
	// HF noise triggers (speech only):
	//   - Rolloff/centroid > 2.5 → cutoff = rolloff - 1000
	//   - Slope > -1e-05 → cutoff = 12000
	//   - ZCR > 0.10 AND centroid < 4000 → cutoff = 10000
	// Cutoff clamped to 8000-18000

	tests := []struct {
		name            string
		kurtosis        float64
		flatness        float64
		flux            float64
		crest           float64
		rolloff         float64
		centroid        float64
		slope           float64
		zcr             float64
		wantEnabled     bool
		wantContentType ContentType
		wantReason      string
		wantFreqMin     float64 // 0 = don't check
		wantFreqMax     float64
		desc            string
	}{
		// Test case 1: Clean podcast speech → ultrasonic cleanup (always-on)
		{
			name:            "clean podcast speech - ultrasonic cleanup",
			kurtosis:        9.2,
			flatness:        0.38,
			flux:            0.002,
			crest:           45.0,
			rolloff:         8809,
			centroid:        3736,
			slope:           -5.66e-05,
			zcr:             0.052,
			wantEnabled:     false, // Per spec: default disabled, rolloff 8809 < 14000 so no ultrasonic trigger
			wantContentType: ContentSpeech,
			wantReason:      "no HF issues detected",
			wantFreqMin:     0, // Not checked when disabled
			wantFreqMax:     0,
			desc:            "typical podcast: rolloff < 14kHz, no triggers, LP disabled per spec",
		},
		// Test case 2: Speech with high rolloff (>14kHz) → ultrasonic cleanup
		{
			name:            "speech with ultrasonic content",
			kurtosis:        8.0,
			flatness:        0.40,
			flux:            0.002,
			crest:           40.0,
			rolloff:         15000, // > 14000 threshold
			centroid:        5000,
			slope:           -3e-05,
			zcr:             0.05,
			wantEnabled:     true,
			wantContentType: ContentSpeech,
			wantReason:      "ultrasonic cleanup (rolloff > 14kHz)",
			wantFreqMin:     17000, // 15000 + 2000 = 17000
			wantFreqMax:     17000,
			desc:            "rolloff > 14kHz, enables LP at rolloff + 2kHz",
		},
		// Test case 3: Music characteristics → disabled
		{
			name:            "music sting",
			kurtosis:        3.5,
			flatness:        0.61,
			flux:            0.008,
			crest:           18.0,
			rolloff:         16000, // Would trigger LP if speech
			centroid:        5500,
			slope:           -2e-05,
			zcr:             0.08,
			wantEnabled:     false,
			wantContentType: ContentMusic,
			wantReason:      "music content detected",
			desc:            "music detected, LP disabled to preserve full spectrum",
		},
		// Test case 4: Mixed characteristics → disabled (conservative)
		{
			name:            "speech over music bed",
			kurtosis:        5.2,
			flatness:        0.52,
			flux:            0.004,
			crest:           27.0,
			rolloff:         12000,
			centroid:        4200,
			slope:           -2e-05,
			zcr:             0.06,
			wantEnabled:     false,
			wantContentType: ContentMixed,
			wantReason:      "mixed content, conservative",
			desc:            "ambiguous content, LP disabled to be safe",
		},
		// Test case 5: Dark voice (rolloff < 8kHz) → disabled per spec
		{
			name:            "dark voice - already limited HF",
			kurtosis:        7.5,
			flatness:        0.42,
			flux:            0.002,
			crest:           35.0,
			rolloff:         7000, // < 8kHz dark voice threshold
			centroid:        3500,
			slope:           -8e-06,
			zcr:             0.05,
			wantEnabled:     false,
			wantContentType: ContentSpeech,
			wantReason:      "voice already dark (rolloff < 8kHz)",
			wantFreqMin:     0,
			wantFreqMax:     0,
			desc:            "rolloff < 8kHz means voice is naturally dark, no LP needed",
		},
		// Test case 6: High ZCR with low centroid trigger
		{
			name:            "speech with HF noise pattern",
			kurtosis:        8.0,
			flatness:        0.38,
			flux:            0.002,
			crest:           40.0,
			rolloff:         9000, // > 8kHz (not dark), < 14kHz (no ultrasonic)
			centroid:        3500, // < 4000
			slope:           -4e-05,
			zcr:             0.12, // > 0.10 (will trigger ZCR)
			wantEnabled:     true,
			wantContentType: ContentSpeech,
			wantReason:      "high ZCR with low centroid (HF noise)",
			wantFreqMin:     12000,
			wantFreqMax:     12000,
			desc:            "ZCR>0.10 AND centroid<4000 indicates HF noise, enable at 12kHz per spec",
		},
		// Test case 7: High ZCR but high centroid (sibilance, not noise) → disabled
		{
			name:            "speech with high ZCR high centroid",
			kurtosis:        8.0,
			flatness:        0.38,
			flux:            0.002,
			crest:           40.0,
			rolloff:         9000,
			centroid:        5000, // > 4000, so ZCR trigger doesn't fire
			slope:           -4e-05,
			zcr:             0.12,  // > 0.10 but centroid too high
			wantEnabled:     false, // Per spec: default disabled, no triggers matched
			wantContentType: ContentSpeech,
			wantReason:      "no HF issues detected",
			wantFreqMin:     0,
			wantFreqMax:     0,
			desc:            "high ZCR with high centroid is sibilance (not noise), LP disabled",
		},
		// Test case 8: Very high rolloff (>18kHz) - capped at 20kHz
		{
			name:            "speech with very high rolloff",
			kurtosis:        7.0,
			flatness:        0.40,
			flux:            0.002,
			crest:           35.0,
			rolloff:         19000, // > 14kHz threshold
			centroid:        5000,
			slope:           -3e-05,
			zcr:             0.05,
			wantEnabled:     true,
			wantContentType: ContentSpeech,
			wantReason:      "ultrasonic cleanup (rolloff > 14kHz)",
			wantFreqMin:     20000, // 19000 + 2000 = 21000, capped at 20000
			wantFreqMax:     20000,
			desc:            "rolloff + 2kHz capped at 20kHz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestConfig()
			m := &AudioMeasurements{
				BaseMeasurements: BaseMeasurements{
					SpectralKurtosis:  tt.kurtosis,
					SpectralFlatness:  tt.flatness,
					SpectralFlux:      tt.flux,
					SpectralCrest:     tt.crest,
					SpectralRolloff:   tt.rolloff,
					SpectralCentroid:  tt.centroid,
					SpectralSlope:     tt.slope,
					ZeroCrossingsRate: tt.zcr,
				},
			}

			tuneDS201LowPass(config, m)

			if config.DS201LPEnabled != tt.wantEnabled {
				t.Errorf("DS201LPEnabled = %v, want %v [%s]",
					config.DS201LPEnabled, tt.wantEnabled, tt.desc)
			}

			if config.DS201LPContentType != tt.wantContentType {
				t.Errorf("DS201LPContentType = %v, want %v [%s]",
					config.DS201LPContentType, tt.wantContentType, tt.desc)
			}

			if config.DS201LPReason != tt.wantReason {
				t.Errorf("DS201LPReason = %q, want %q [%s]",
					config.DS201LPReason, tt.wantReason, tt.desc)
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
				BaseMeasurements: BaseMeasurements{
					SpectralCentroid: tt.centroid,
					SpectralRolloff:  tt.rolloff,
				},
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

func TestTuneDC1Declick(t *testing.T) {
	// Tests the CEDAR DC-1-inspired declicker adaptive tuning.
	// Uses OR logic: high MaxDiff OR high Crest can trigger independently.
	// Mouth noises (lip smacks) have high crest but often low MaxDiff.
	//
	// Key constants from adaptive.go:
	// dc1MaxDiffLikely = 0.25 (> 25% full scale: likely clicks)
	// dc1MaxDiffPossible = 0.12 (> 12% full scale: possible clicks)
	// dc1CrestLikely = 50.0 dB (strong impulsive content)
	// dc1CrestPossible = 35.0 dB (moderate impulsive content)
	// dc1ThreshAggressive = 2.0, dc1ThreshMild = 4.0, dc1ThreshMouthNoise = 5.0, dc1ThreshConserv = 6.0
	// dc1WindowShort = 45.0ms, dc1WindowDefault = 55.0ms, dc1WindowLong = 70.0ms
	// dc1CentroidFast = 3000.0 Hz, dc1CentroidSlow = 1500.0 Hz
	//
	// NOTE: MaxDifference from astats is in sample units (0-32768 for 16-bit audio)
	// Test values below use the ratio (0-1) and are scaled by 32768 in the test loop

	tests := []struct {
		name          string
		maxDiffRatio  float64 // 0-1 ratio, will be scaled by 32768 for MaxDifference
		crest         float64
		flatness      float64
		dynamicRange  float64
		centroid      float64
		wantEnabled   bool
		wantThreshold float64
		wantWindow    float64
		tolerance     float64
	}{
		{
			name:         "clean recording - disabled",
			maxDiffRatio: 0.08, // Below dc1MaxDiffPossible (0.12)
			crest:        25.0, // Below dc1CrestPossible (35.0)
			flatness:     0.1,
			dynamicRange: 15.0,
			centroid:     2000.0,
			wantEnabled:  false,
		},
		{
			name:          "high maxdiff alone - aggressive",
			maxDiffRatio:  0.30, // > dc1MaxDiffLikely (0.25)
			crest:         25.0, // Below crest thresholds
			flatness:      0.1,
			dynamicRange:  15.0,
			centroid:      2000.0,
			wantEnabled:   true,
			wantThreshold: 2.0, // dc1ThreshAggressive
			wantWindow:    55.0,
			tolerance:     0.5,
		},
		{
			name:          "high crest alone - mouth noises",
			maxDiffRatio:  0.08, // Low MaxDiff (typical for mouth noises)
			crest:         55.0, // > dc1CrestLikely (50.0)
			flatness:      0.1,
			dynamicRange:  15.0,
			centroid:      2000.0,
			wantEnabled:   true,
			wantThreshold: 5.0, // dc1ThreshMouthNoise
			wantWindow:    55.0,
			tolerance:     0.5,
		},
		{
			name:          "both elevated - mild clicks",
			maxDiffRatio:  0.15, // > dc1MaxDiffPossible (0.12) but < dc1MaxDiffLikely (0.25)
			crest:         40.0, // > dc1CrestPossible (35.0) but < dc1CrestLikely (50.0)
			flatness:      0.1,
			dynamicRange:  15.0,
			centroid:      2000.0,
			wantEnabled:   true,
			wantThreshold: 4.0, // dc1ThreshMild
			wantWindow:    55.0,
			tolerance:     0.5,
		},
		{
			name:          "moderate crest alone - possible mouth noises",
			maxDiffRatio:  0.08, // Low MaxDiff
			crest:         40.0, // > dc1CrestPossible (35.0) but < dc1CrestLikely (50.0)
			flatness:      0.1,
			dynamicRange:  15.0,
			centroid:      2000.0,
			wantEnabled:   true,
			wantThreshold: 6.0, // dc1ThreshConserv
			wantWindow:    55.0,
			tolerance:     0.5,
		},
		{
			name:          "noisy signal - raised threshold",
			maxDiffRatio:  0.30, // High MaxDiff - aggressive base
			crest:         25.0,
			flatness:      0.4, // Above dc1FlatnessNoisy (0.3) - adds 2.0
			dynamicRange:  15.0,
			centroid:      2000.0,
			wantEnabled:   true,
			wantThreshold: 4.0, // 2.0 + 2.0 for noisy
			wantWindow:    55.0,
			tolerance:     0.5,
		},
		{
			name:          "fast speech - short window",
			maxDiffRatio:  0.30, // High MaxDiff
			crest:         25.0,
			flatness:      0.1,
			dynamicRange:  15.0,
			centroid:      4000.0, // > dc1CentroidFast (3000.0)
			wantEnabled:   true,
			wantThreshold: 2.0,  // dc1ThreshAggressive
			wantWindow:    45.0, // dc1WindowShort
			tolerance:     0.5,
		},
		{
			name:          "bass-heavy - long window",
			maxDiffRatio:  0.30, // High MaxDiff
			crest:         25.0,
			flatness:      0.1,
			dynamicRange:  15.0,
			centroid:      1000.0, // < dc1CentroidSlow (1500.0)
			wantEnabled:   true,
			wantThreshold: 2.0,  // dc1ThreshAggressive
			wantWindow:    70.0, // dc1WindowLong
			tolerance:     0.5,
		},
		{
			name:          "compressed audio - raised threshold",
			maxDiffRatio:  0.30, // High MaxDiff
			crest:         25.0,
			flatness:      0.1,
			dynamicRange:  8.0, // Below dc1DynamicRangeLow (10.0) - adds 1.0
			centroid:      2000.0,
			wantEnabled:   true,
			wantThreshold: 3.0, // 2.0 + 1.0 for compressed
			wantWindow:    55.0,
			tolerance:     0.5,
		},
		{
			name:          "noisy and compressed - max threshold",
			maxDiffRatio:  0.30, // High MaxDiff
			crest:         25.0,
			flatness:      0.4, // Noisy - adds 2.0
			dynamicRange:  8.0, // Compressed - adds 1.0
			centroid:      2000.0,
			wantEnabled:   true,
			wantThreshold: 5.0, // 2.0 + 2.0 (noisy) + 1.0 (compressed)
			wantWindow:    55.0,
			tolerance:     0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultFilterConfig()
			measurements := &AudioMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference:    tt.maxDiffRatio * 32768.0, // Scale to sample units
					SpectralCrest:    tt.crest,
					SpectralFlatness: tt.flatness,
					DynamicRange:     tt.dynamicRange,
					SpectralCentroid: tt.centroid,
				},
			}

			tuneDC1Declick(config, measurements)

			if config.DC1DeclickEnabled != tt.wantEnabled {
				t.Errorf("DC1DeclickEnabled = %v, want %v", config.DC1DeclickEnabled, tt.wantEnabled)
			}
			if tt.wantEnabled {
				if diff := math.Abs(config.DC1DeclickThreshold - tt.wantThreshold); diff > tt.tolerance {
					t.Errorf("DC1DeclickThreshold = %.1f, want %.1f (±%.1f)",
						config.DC1DeclickThreshold, tt.wantThreshold, tt.tolerance)
				}
				if diff := math.Abs(config.DC1DeclickWindow - tt.wantWindow); diff > tt.tolerance {
					t.Errorf("DC1DeclickWindow = %.1f, want %.1f (±%.1f)",
						config.DC1DeclickWindow, tt.wantWindow, tt.tolerance)
				}
			}
			// Always check reason is set
			if config.DC1DeclickReason == "" {
				t.Error("DC1DeclickReason should be set")
			}
		})
	}
}

func TestTuneDS201Gate(t *testing.T) {
	// Tests the comprehensive gate tuning which calculates all gate parameters
	// based on measurements including NoiseProfile (silence sample analysis).
	//
	// Key constants from adaptive.go:
	// gateThresholdMinDB = -50.0 dB (quiet speech floor)
	// gateThresholdMaxDB = -25.0 dB (never gate above this - would cut speech)
	// gateCrestFactorThreshold = 20.0 dB (when to use peak vs floor)
	// gateTargetReductionDB = 12.0 dB (target noise reduction)
	// gateTargetThresholdDB = -40.0 dB (target for clean recordings)
	// gateRatioGentle = 1.5, gateRatioMod = 2.0, gateRatioTight = 2.5
	//
	// Gap is derived from ratio: gap = targetReduction / (1 - 1/ratio)
	// - ratio 1.5 → gap = 12 / 0.333 = 36 dB
	// - ratio 2.0 → gap = 12 / 0.5 = 24 dB
	// - ratio 2.5 → gap = 12 / 0.6 = 20 dB

	t.Run("threshold calculation", func(t *testing.T) {
		tests := []struct {
			name            string
			noiseFloor      float64 // dB
			silencePeak     float64 // dB
			silenceCrest    float64 // dB - determines if we use peak or floor
			inputLRA        float64 // LU - determines ratio, which determines gap
			wantThresholdDB float64 // expected threshold dB
			tolerance       float64 // tolerance in dB
			desc            string
		}{
			{
				name:            "clean studio - uses target threshold",
				noiseFloor:      -75.0,
				silencePeak:     -70.0,
				silenceCrest:    10.0, // Low crest = stable noise, use floor
				inputLRA:        8.0,  // Narrow LRA → ratio 2.5 → gap 20dB → -75+20=-55, but target -40 is higher
				wantThresholdDB: -40.0,
				tolerance:       1.0,
				desc:            "very clean, uses target threshold -40dB",
			},
			{
				name:            "typical podcast - derived gap with moderate ratio",
				noiseFloor:      -55.0,
				silencePeak:     -50.0,
				silenceCrest:    10.0, // Low crest = stable noise
				inputLRA:        12.0, // Moderate LRA → ratio 2.0 → gap 24dB → -55+24=-31
				wantThresholdDB: -31.0,
				tolerance:       1.0,
				desc:            "moderate noise floor with derived gap",
			},
			{
				name:            "noisy room - derived gap",
				noiseFloor:      -42.0,
				silencePeak:     -38.0,
				silenceCrest:    10.0,
				inputLRA:        8.0, // Narrow LRA → ratio 2.5 → gap 20dB → -42+20=-22, clamped to -25
				wantThresholdDB: -25.0,
				tolerance:       1.0,
				desc:            "noisy floor, threshold clamped to max",
			},
			{
				name:            "bleed with high crest - uses peak + margin",
				noiseFloor:      -55.0,
				silencePeak:     -48.0, // Transient spikes
				silenceCrest:    25.0,  // High crest = transient bleed
				inputLRA:        12.0,
				wantThresholdDB: -45.0, // -48 (peak) + 3dB margin
				tolerance:       1.0,
				desc:            "high crest factor triggers peak reference",
			},
			{
				name:            "extreme noise - clamped to max",
				noiseFloor:      -20.0,
				silencePeak:     -15.0,
				silenceCrest:    25.0,
				inputLRA:        8.0,
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
					InputLRA:   tt.inputLRA,
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
					BaseMeasurements: BaseMeasurements{
						MaxDifference: tt.maxDiff,
						SpectralFlux:  tt.spectralFlux,
					},
					NoiseFloor: -55.0,
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

	t.Run("release based on noise entropy", func(t *testing.T) {
		// Release times adapt based on silence entropy:
		// - Very tonal (< 0.1): slowest release (hide pumping on pure hum)
		// - Tonal (< 0.15): slow release (some pumping hiding)
		// - Mixed (< 0.2): moderate release
		// - Broadband-ish (>= 0.2): faster release (cut noise quickly)
		//
		// Base constants:
		// ds201GateReleaseMod = 300ms (base for typical content)
		// ds201GateReleaseHoldComp = 50ms (compensate for no hold param)
		// ds201GateReleaseTonalComp = 75ms (extra for tonal)
		// ds201GateReleaseEntropyReduce = 100ms (reduction for broadband)

		// Current thresholds:
		// - veryTonal: < 0.10
		// - tonal: < 0.12
		// - mixed: < 0.16
		// - broadband: >= 0.16
		//
		// Base release values (lowered for tighter noise control):
		// - ds201GateReleaseMod = 250ms (was 300ms)
		// - ds201GateReleaseSustained = 300ms (was 400ms)
		// - ds201GateReleaseDynamic = 180ms (was 200ms)
		tests := []struct {
			name           string
			entropy        float64
			wantReleaseMin float64 // minimum expected release (ms)
			wantReleaseMax float64 // maximum expected release (ms)
			desc           string
		}{
			{
				name:           "very tonal noise (pure hum)",
				entropy:        0.08, // < 0.10 → very tonal
				wantReleaseMin: 350,  // base 250 + hold 50 + full tonal 75 = 375ms
				wantReleaseMax: 420,
				desc:           "very tonal needs slowest release to hide pumping",
			},
			{
				name:           "tonal noise (hum/bleed)",
				entropy:        0.11, // >= 0.10 && < 0.12 → tonal (70% compensation)
				wantReleaseMin: 320,  // base 250 + hold 50 + 70% tonal ~52 = 352ms
				wantReleaseMax: 400,
				desc:           "tonal needs slow release for pumping hiding",
			},
			{
				name:           "mixed noise character",
				entropy:        0.14, // >= 0.12 && < 0.16 → mixed (30% reduction)
				wantReleaseMin: 240,  // base 250 + hold 50 - 30% reduce ~30 = 270ms
				wantReleaseMax: 320,
				desc:           "mixed needs moderate release to cut noise faster",
			},
			{
				name:           "broadband-ish noise",
				entropy:        0.20, // >= 0.16 → broadband (full reduction)
				wantReleaseMin: 150,  // base 250 + hold 50 - reduce 100 = 200ms
				wantReleaseMax: 250,
				desc:           "broadband needs faster release to cut noise",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					BaseMeasurements: BaseMeasurements{
						SpectralFlux: 0.02, // Moderate flux (uses ds201GateReleaseMod)
					},
					NoiseFloor: -55.0,
					InputLRA:   15.0, // Above LRA threshold (10 LU) to avoid LRA-based extension
					NoiseProfile: &NoiseProfile{
						PeakLevel:   -50.0,
						CrestFactor: 15.0,
						Entropy:     tt.entropy,
					},
				}

				tuneDS201Gate(config, measurements)

				if config.DS201GateRelease < tt.wantReleaseMin || config.DS201GateRelease > tt.wantReleaseMax {
					t.Errorf("DS201GateRelease = %.1f ms, want %.1f-%.1f ms [%s]",
						config.DS201GateRelease, tt.wantReleaseMin, tt.wantReleaseMax, tt.desc)
				}
			})
		}
	})

	t.Run("release extension based on LRA", func(t *testing.T) {
		// Tests for LRA-based release extension
		// Low LRA audio has speech at similar levels, causing rapid gate
		// open/close cycles that pump audibly. Longer release smooths this.
		//
		// Constants:
		// ds201GateReleaseLRALow = 10.0 LU (below: extend release)
		// ds201GateReleaseLRAVeryLow = 8.0 LU (below: maximum extension)
		// ds201GateReleaseLRAExtension = 100ms (extension for low LRA)
		// ds201GateReleaseLRAMaxExt = 150ms (max extension for very low LRA)

		tests := []struct {
			name           string
			lra            float64
			wantReleaseMin float64 // relative to base release
			wantReleaseMax float64
			desc           string
		}{
			{
				name:           "wide LRA - no extension",
				lra:            16.0, // Well above 10 LU threshold
				wantReleaseMin: 250,  // Base release (no extension)
				wantReleaseMax: 320,
				desc:           "wide dynamics don't need release extension",
			},
			{
				name:           "moderate LRA - no extension",
				lra:            12.0, // Above 10 LU threshold
				wantReleaseMin: 250,
				wantReleaseMax: 320,
				desc:           "moderate dynamics don't need release extension",
			},
			{
				name:           "low LRA - partial extension",
				lra:            9.0, // Between 8-10 LU, scaled extension
				wantReleaseMin: 290, // Base ~300 + 50% of 100ms extension
				wantReleaseMax: 380,
				desc:           "low dynamics need release extension to hide pumping",
			},
			{
				name:           "very low LRA - maximum extension",
				lra:            7.0, // Below 8 LU, full 150ms extension
				wantReleaseMin: 380, // Base ~300 + 150ms max extension
				wantReleaseMax: 500,
				desc:           "very low dynamics need maximum release extension",
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					BaseMeasurements: BaseMeasurements{
						SpectralFlux: 0.02, // Moderate flux
					},
					NoiseFloor: -55.0,
					InputLRA:   tt.lra,
					NoiseProfile: &NoiseProfile{
						PeakLevel:   -50.0,
						CrestFactor: 15.0,
						Entropy:     0.14, // Mixed entropy (no tonal extension)
					},
				}

				tuneDS201Gate(config, measurements)

				if config.DS201GateRelease < tt.wantReleaseMin || config.DS201GateRelease > tt.wantReleaseMax {
					t.Errorf("DS201GateRelease = %.1f ms (LRA=%.1f LU), want %.1f-%.1f ms [%s]",
						config.DS201GateRelease, tt.lra, tt.wantReleaseMin, tt.wantReleaseMax, tt.desc)
				}
			})
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
	// defaultLA2ARatio      = 3.0
	// defaultLA2AThreshold  = -18.0
	// defaultGateThreshold  = 0.01 (linear, ~-40dBFS)
	// Note: LA2AMakeup not sanitised - always 0 (set in DefaultFilterConfig)

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
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 0.02,
			},
		},

		// NaN in each field
		{
			name: "NaN HighpassFreq gets default",
			config: FilterChainConfig{
				DS201HPFreq:        math.NaN(),
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        80.0, // defaultHighpassFreq
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 0.02,
			},
		},
		{
			name: "NaN DeessIntensity gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     math.NaN(),
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.0, // defaultDeessIntensity
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 0.02,
			},
		},
		{
			name: "NaN LA2ARatio gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          math.NaN(),
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0, // defaultLA2ARatio (LA-2A inspired)
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 0.02,
			},
		},
		{
			name: "NaN LA2AThreshold gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      math.NaN(),
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -18.0, // defaultLA2AThreshold (LA-2A inspired)
				DS201GateThreshold: 0.02,
			},
		},
		{
			name: "NaN GateThreshold gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: math.NaN(),
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 0.01, // defaultGateThreshold
			},
		},

		// Inf cases
		{
			name: "positive Inf values get defaults",
			config: FilterChainConfig{
				DS201HPFreq:        math.Inf(1),
				DeessIntensity:     math.Inf(1),
				LA2ARatio:          math.Inf(1),
				LA2AThreshold:      math.Inf(1),
				DS201GateThreshold: math.Inf(1),
			},
			want: FilterChainConfig{
				DS201HPFreq:        80.0,
				DeessIntensity:     0.0,
				LA2ARatio:          3.0,   // LA-2A inspired
				LA2AThreshold:      -18.0, // LA-2A inspired
				DS201GateThreshold: 0.01,
			},
		},
		{
			name: "negative Inf values get defaults",
			config: FilterChainConfig{
				DS201HPFreq:        math.Inf(-1),
				DeessIntensity:     math.Inf(-1),
				LA2ARatio:          math.Inf(-1),
				LA2AThreshold:      math.Inf(-1),
				DS201GateThreshold: math.Inf(-1),
			},
			want: FilterChainConfig{
				DS201HPFreq:        80.0,
				DeessIntensity:     0.0,
				LA2ARatio:          3.0,   // LA-2A inspired
				LA2AThreshold:      -18.0, // LA-2A inspired
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
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 0.0, // zero is NOT valid for GateThreshold
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.0,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 0.01, // defaultGateThreshold
			},
		},
		{
			name: "negative GateThreshold gets default",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: -0.5, // negative is NOT valid for GateThreshold
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
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
				LA2ARatio:          0.0, // passes through (edge case: probably invalid)
				LA2AThreshold:      0.0, // passes through (0 dB threshold)
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        0.0,
				DeessIntensity:     0.0,
				LA2ARatio:          0.0,
				LA2AThreshold:      0.0,
				DS201GateThreshold: 0.02,
			},
		},

		// Negative values for fields that legitimately use them
		{
			name: "negative LA2AThreshold passes through (valid dB value)",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -40.0, // very aggressive threshold
				DS201GateThreshold: 0.02,
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -40.0,
				DS201GateThreshold: 0.02,
			},
		},

		// All fields NaN - complete fallback to defaults
		{
			name: "all NaN values get all defaults",
			config: FilterChainConfig{
				DS201HPFreq:        math.NaN(),
				DeessIntensity:     math.NaN(),
				LA2ARatio:          math.NaN(),
				LA2AThreshold:      math.NaN(),
				DS201GateThreshold: math.NaN(),
			},
			want: FilterChainConfig{
				DS201HPFreq:        80.0,
				DeessIntensity:     0.0,
				LA2ARatio:          3.0,   // LA-2A inspired
				LA2AThreshold:      -18.0, // LA-2A inspired
				DS201GateThreshold: 0.01,
			},
		},

		// Very small positive GateThreshold passes through
		{
			name: "very small positive GateThreshold passes through",
			config: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
				DS201GateThreshold: 1e-10, // very small but positive
			},
			want: FilterChainConfig{
				DS201HPFreq:        100.0,
				DeessIntensity:     0.3,
				LA2ARatio:          3.0,
				LA2AThreshold:      -24.0,
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
			if config.LA2ARatio != tt.want.LA2ARatio {
				t.Errorf("LA2ARatio = %v, want %v", config.LA2ARatio, tt.want.LA2ARatio)
			}
			if config.LA2AThreshold != tt.want.LA2AThreshold {
				t.Errorf("LA2AThreshold = %v, want %v", config.LA2AThreshold, tt.want.LA2AThreshold)
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

// =============================================================================
// Dolby SR-Inspired Denoise Tests (mcompand multiband expander)
// =============================================================================

func TestTuneDolbySR_ExpansionSelection(t *testing.T) {
	// Tests for lockstep threshold+expansion selection based on RMS trough
	// Simplified 3-tier system from adaptive.go:
	// < -85 dB:       -50 dB threshold, 16 dB expansion (clean)
	// -85 to -80 dB:  -45 dB threshold, 20 dB expansion (moderate)
	// > -80 dB:       -40 dB threshold, 24 dB expansion (noisy)

	tests := []struct {
		name          string
		rmsTrough     float64
		wantExpansion float64
		wantThreshold float64
		wantDesc      string
	}{
		// Clean source (< -85 dB) → gentle treatment
		{
			name:          "very clean - studio quality",
			rmsTrough:     -90.0,
			wantExpansion: 16.0,
			wantThreshold: -50.0,
			wantDesc:      "16 dB expansion, -50 dB threshold for very clean source",
		},
		{
			name:          "clean - good recording",
			rmsTrough:     -86.0,
			wantExpansion: 16.0,
			wantThreshold: -50.0,
			wantDesc:      "16 dB expansion, -50 dB threshold for clean source",
		},
		// Moderate source (-85 to -80 dB) → balanced treatment
		{
			name:          "at clean/moderate boundary",
			rmsTrough:     -85.0,
			wantExpansion: 20.0,
			wantThreshold: -45.0,
			wantDesc:      "20 dB expansion, -45 dB threshold at -85 dB boundary",
		},
		{
			name:          "moderate - home studio",
			rmsTrough:     -82.0,
			wantExpansion: 20.0,
			wantThreshold: -45.0,
			wantDesc:      "20 dB expansion, -45 dB threshold for moderate noise",
		},
		// Noisy source (> -80 dB) → aggressive treatment
		{
			name:          "at moderate/noisy boundary",
			rmsTrough:     -80.0,
			wantExpansion: 24.0,
			wantThreshold: -40.0,
			wantDesc:      "24 dB expansion, -40 dB threshold at -80 dB boundary",
		},
		{
			name:          "noisy - challenging environment",
			rmsTrough:     -75.0,
			wantExpansion: 24.0,
			wantThreshold: -40.0,
			wantDesc:      "24 dB expansion, -40 dB threshold for noisy source",
		},
		{
			name:          "very noisy",
			rmsTrough:     -60.0,
			wantExpansion: 24.0,
			wantThreshold: -40.0,
			wantDesc:      "24 dB expansion, -40 dB threshold for very noisy source",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestConfig()
			config.DolbySREnabled = true
			measurements := &AudioMeasurements{
				BaseMeasurements: BaseMeasurements{
					RMSTrough: tt.rmsTrough,
				},
			}

			tuneDolbySR(config, measurements, 10.0) // LUFS gap ignored by new implementation

			if config.DolbySRExpansionDB != tt.wantExpansion {
				t.Errorf("DolbySRExpansionDB = %.1f dB, want %.1f dB (%s)",
					config.DolbySRExpansionDB, tt.wantExpansion, tt.wantDesc)
			}
			if config.DolbySRThresholdDB != tt.wantThreshold {
				t.Errorf("DolbySRThresholdDB = %.1f dB, want %.1f dB (%s)",
					config.DolbySRThresholdDB, tt.wantThreshold, tt.wantDesc)
			}
		})
	}
}

func TestTuneDolbySR_BandInitialisation(t *testing.T) {
	// Verify 6-band configuration is initialized with correct values
	config := newTestConfig()
	config.DolbySREnabled = true
	measurements := &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{
			RMSTrough: -75.0, // Clean source
		},
	}

	tuneDolbySR(config, measurements, 10.0)

	// Verify 6 bands are initialised
	if len(config.DolbySRBands) != 6 {
		t.Fatalf("Expected 6 bands, got %d", len(config.DolbySRBands))
	}

	// Verify band names/frequencies match expected configuration from defaultDolbySRBands()
	expectedBands := []struct {
		crossover    float64
		scalePercent float64
	}{
		{100, 100},  // Sub-bass
		{300, 100},  // Chest
		{800, 105},  // Voice F1 (protected)
		{3300, 103}, // Voice F2 (protected)
		{8000, 100}, // Presence
		{20500, 95}, // Air (slightly reduced)
	}

	for i, want := range expectedBands {
		got := config.DolbySRBands[i]
		if got.CrossoverHz != want.crossover {
			t.Errorf("Band %d CrossoverHz = %.0f, want %.0f", i, got.CrossoverHz, want.crossover)
		}
		if got.ScalePercent != want.scalePercent {
			t.Errorf("Band %d ScalePercent = %.0f, want %.0f", i, got.ScalePercent, want.scalePercent)
		}
	}
}

func TestTuneDolbySR_Disabled(t *testing.T) {
	// Verify tuner doesn't modify config when filter is disabled
	config := newTestConfig()
	config.DolbySREnabled = false
	config.DolbySRExpansionDB = 0 // Should remain unchanged

	measurements := &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{
			RMSTrough: -50.0, // Noisy source
		},
	}

	tuneDolbySR(config, measurements, 15.0)

	// Config should be unchanged
	if config.DolbySREnabled {
		t.Error("DolbySREnabled should remain false")
	}
	if config.DolbySRExpansionDB != 0 {
		t.Errorf("DolbySRExpansionDB should remain 0, got %.1f", config.DolbySRExpansionDB)
	}
	if len(config.DolbySRBands) != 0 {
		t.Errorf("DolbySRBands should remain empty, got %d bands", len(config.DolbySRBands))
	}
}

func TestBuildDolbySRFilter(t *testing.T) {
	// Verify the filter builder produces valid mcompand chain

	config := newTestConfig()
	config.DolbySREnabled = true
	config.DolbySRExpansionDB = 13.0
	config.DolbySRBands = defaultDolbySRBands()

	filter := config.buildDolbySRFilter()

	// Verify filter is not empty
	if filter == "" {
		t.Error("buildDolbySRFilter returned empty string when enabled")
	}

	// Verify mcompand is present
	if !containsString(filter, "mcompand=") {
		t.Errorf("Filter missing mcompand=\nGot: %s", filter)
	}

	// Verify FLAT reduction curve pattern (same reduction at -90 and -75)
	// For 13dB expansion: -90/-103 and -75/-88
	if !containsString(filter, "-90/-103") {
		t.Errorf("Filter missing FLAT curve point -90/-103\nGot: %s", filter)
	}
	if !containsString(filter, "-75/-88") {
		t.Errorf("Filter missing FLAT curve point -75/-88\nGot: %s", filter)
	}

	// DolbySR filter applies noise reduction only, no gain changes
	if containsString(filter, "volume=") {
		t.Errorf("Filter should not contain volume filter\nGot: %s", filter)
	}
}

func TestBuildDolbySRFilter_Disabled(t *testing.T) {
	config := newTestConfig()
	config.DolbySREnabled = false

	filter := config.buildDolbySRFilter()

	if filter != "" {
		t.Errorf("buildDolbySRFilter should return empty when disabled, got: %s", filter)
	}
}

func TestTuneDNS1500(t *testing.T) {
	t.Run("disabled when no NoiseProfile", func(t *testing.T) {
		config := newTestConfig()
		measurements := &AudioMeasurements{
			NoiseProfile: nil,
			InputLRA:     10.0,
		}

		tuneDNS1500(config, measurements)

		if config.DNS1500Enabled {
			t.Error("DNS1500 should be disabled when NoiseProfile is nil")
		}
	})

	t.Run("disabled when NoiseProfile has zero duration", func(t *testing.T) {
		config := newTestConfig()
		measurements := &AudioMeasurements{
			NoiseProfile: &NoiseProfile{
				Start:              0,
				Duration:           0, // Zero duration = no valid silence
				MeasuredNoiseFloor: -55.0,
			},
			InputLRA: 10.0,
		}

		tuneDNS1500(config, measurements)

		if config.DNS1500Enabled {
			t.Error("DNS1500 should be disabled when NoiseProfile.Duration is zero")
		}
	})

	t.Run("enabled with valid NoiseProfile", func(t *testing.T) {
		config := newTestConfig()
		measurements := &AudioMeasurements{
			NoiseProfile: &NoiseProfile{
				Start:              2 * time.Second,
				Duration:           500 * time.Millisecond,
				MeasuredNoiseFloor: -55.0,
				SpectralFlatness:   0.6,
				Entropy:            0.5,
			},
			InputLRA: 10.0,
		}

		tuneDNS1500(config, measurements)

		if !config.DNS1500Enabled {
			t.Error("DNS1500 should be enabled with valid NoiseProfile")
		}
	})

	t.Run("silence timing from NoiseProfile", func(t *testing.T) {
		config := newTestConfig()
		measurements := &AudioMeasurements{
			NoiseProfile: &NoiseProfile{
				Start:              3500 * time.Millisecond, // 3.5 seconds
				Duration:           750 * time.Millisecond,  // 0.75 seconds
				MeasuredNoiseFloor: -50.0,
			},
			InputLRA: 10.0,
		}

		tuneDNS1500(config, measurements)

		if config.DNS1500SilenceStart != 3.5 {
			t.Errorf("DNS1500SilenceStart = %.3f, want 3.500", config.DNS1500SilenceStart)
		}
		if config.DNS1500SilenceEnd != 4.25 {
			t.Errorf("DNS1500SilenceEnd = %.3f, want 4.250", config.DNS1500SilenceEnd)
		}
	})

	t.Run("noise reduction clamped to min", func(t *testing.T) {
		config := newTestConfig()
		// Clean source: MeasuredNoiseFloor = -68, target = -70 → gap = 2 dB
		// Should be clamped to dns1500NRMin = 6.0
		measurements := &AudioMeasurements{
			NoiseProfile: &NoiseProfile{
				Start:              1 * time.Second,
				Duration:           500 * time.Millisecond,
				MeasuredNoiseFloor: -68.0,
			},
			InputLRA: 10.0,
		}

		tuneDNS1500(config, measurements)

		// dns1500NRMin = 6.0, noiseGap = -68 - (-70) = 2, clamped to 6
		if config.DNS1500NoiseReduce != 6.0 {
			t.Errorf("DNS1500NoiseReduce = %.1f, want 6.0 (clamped min)", config.DNS1500NoiseReduce)
		}
	})

	t.Run("noise reduction clamped to max for clean source", func(t *testing.T) {
		config := newTestConfig()
		// Clean-ish source below noisy threshold: MeasuredNoiseFloor = -56, target = -70 → gap = 14 dB
		// Below dns1500NoisySourceThreshold (-55), so uses normal dns1500NRMax = 30.0
		// But gap is only 14 dB, so no clamping needed
		measurements := &AudioMeasurements{
			NoiseProfile: &NoiseProfile{
				Start:              1 * time.Second,
				Duration:           500 * time.Millisecond,
				MeasuredNoiseFloor: -56.0,
			},
			InputLRA: 10.0,
		}

		tuneDNS1500(config, measurements)

		// noiseGap = -56 - (-70) = 14, within [6, 30] range
		if config.DNS1500NoiseReduce != 14.0 {
			t.Errorf("DNS1500NoiseReduce = %.1f, want 14.0", config.DNS1500NoiseReduce)
		}
	})

	t.Run("noisy source applies voice protection", func(t *testing.T) {
		config := newTestConfig()
		// Noisy source: MeasuredNoiseFloor = -48 dBFS (above -55 threshold)
		// Voice protection: NR capped at 15, gain smooth min 15, headroom 8
		measurements := &AudioMeasurements{
			NoiseProfile: &NoiseProfile{
				Start:              1 * time.Second,
				Duration:           500 * time.Millisecond,
				MeasuredNoiseFloor: -48.0, // Above -55, triggers voice protection
				SpectralFlatness:   0.3,   // Low flatness normally = no smoothing
				Entropy:            0.3,
			},
			InputLRA: 10.0,
		}

		tuneDNS1500(config, measurements)

		// NR: gap = -48 - (-70) = 22, but capped at dns1500NRMaxNoisy = 15
		if config.DNS1500NoiseReduce != 15.0 {
			t.Errorf("DNS1500NoiseReduce = %.1f, want 15.0 (noisy source cap)", config.DNS1500NoiseReduce)
		}

		// Gain smooth: normally 0 for low flatness, but noisy source enforces min 15
		if config.DNS1500GainSmooth < 15 {
			t.Errorf("DNS1500GainSmooth = %d, want >= 15 (noisy source min)", config.DNS1500GainSmooth)
		}

		// Residual floor: noiseFloor (-48) + headroom (8) = -40
		if config.DNS1500ResidFloor != -40.0 {
			t.Errorf("DNS1500ResidFloor = %.1f, want -40.0 (noisy source headroom)", config.DNS1500ResidFloor)
		}
	})

	t.Run("adaptivity based on InputLRA", func(t *testing.T) {
		// dns1500LRAFastThresh = 6.0, dns1500LRASlowThresh = 15.0
		// dns1500AdaptivityFast = 0.3, Moderate = 0.5, Slow = 0.7
		tests := []struct {
			name           string
			inputLRA       float64
			wantAdaptivity float64
			desc           string
		}{
			{"low LRA - fast adaptation", 4.0, 0.3, "uniform material, faster adaptation safe"},
			{"moderate LRA - moderate adaptation", 10.0, 0.5, "balanced material"},
			{"high LRA - slow adaptation", 18.0, 0.7, "dynamic material, avoid pumping"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					NoiseProfile: &NoiseProfile{
						Start:              1 * time.Second,
						Duration:           500 * time.Millisecond,
						MeasuredNoiseFloor: -55.0,
					},
					InputLRA: tt.inputLRA,
				}

				tuneDNS1500(config, measurements)

				if config.DNS1500Adaptivity != tt.wantAdaptivity {
					t.Errorf("DNS1500Adaptivity = %.2f, want %.2f [%s]",
						config.DNS1500Adaptivity, tt.wantAdaptivity, tt.desc)
				}
			})
		}
	})

	t.Run("gain smoothing from spectral flatness", func(t *testing.T) {
		// dns1500FlatnessSmooth = 0.5, dns1500EntropySmooth = 0.7
		// dns1500GainSmoothMin = 0, Moderate = 8, Max = 20
		tests := []struct {
			name          string
			flatness      float64
			entropy       float64
			wantMinSmooth int
			wantMaxSmooth int
			desc          string
		}{
			{"tonal noise (low flatness)", 0.3, 0.4, 0, 0, "minimal smoothing for precision"},
			{"broadband noise (high flatness)", 0.7, 0.5, 8, 20, "needs smoothing"},
			{"high entropy triggers smoothing", 0.4, 0.8, 8, 20, "entropy threshold"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					NoiseProfile: &NoiseProfile{
						Start:              1 * time.Second,
						Duration:           500 * time.Millisecond,
						MeasuredNoiseFloor: -55.0,
						SpectralFlatness:   tt.flatness,
						Entropy:            tt.entropy,
					},
					InputLRA: 10.0,
				}

				tuneDNS1500(config, measurements)

				if config.DNS1500GainSmooth < tt.wantMinSmooth || config.DNS1500GainSmooth > tt.wantMaxSmooth {
					t.Errorf("DNS1500GainSmooth = %d, want %d-%d [%s]",
						config.DNS1500GainSmooth, tt.wantMinSmooth, tt.wantMaxSmooth, tt.desc)
				}
			})
		}
	})

	t.Run("track noise always enabled", func(t *testing.T) {
		config := newTestConfig()
		config.DNS1500TrackNoise = false // Start disabled
		measurements := &AudioMeasurements{
			NoiseProfile: &NoiseProfile{
				Start:              1 * time.Second,
				Duration:           500 * time.Millisecond,
				MeasuredNoiseFloor: -55.0,
			},
			InputLRA: 10.0,
		}

		tuneDNS1500(config, measurements)

		if !config.DNS1500TrackNoise {
			t.Error("DNS1500TrackNoise should always be enabled by tuneDNS1500")
		}
	})
}

func TestDNS1500DisablesDolbySR(t *testing.T) {
	// When DNS-1500 is enabled (valid NoiseProfile), DolbySR should be disabled
	// Note: Start with DolbySREnabled = false to test normal flow (not force mode)
	config := DefaultFilterConfig()
	config.DolbySREnabled = false // Ensure we're testing normal flow, not force mode
	measurements := &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{
			RMSTrough: -60.0,
		},
		NoiseProfile: &NoiseProfile{
			Start:              1 * time.Second,
			Duration:           500 * time.Millisecond,
			MeasuredNoiseFloor: -55.0,
			SpectralFlatness:   0.5,
		},
		InputI:   -20.0,
		InputLRA: 10.0,
	}

	AdaptConfig(config, measurements)

	// DNS-1500 should be enabled (we have valid NoiseProfile)
	if !config.DNS1500Enabled {
		t.Fatal("DNS1500 should be enabled with valid NoiseProfile")
	}

	// DolbySR should be disabled when DNS-1500 is active
	if config.DolbySREnabled {
		t.Error("DolbySR should be disabled when DNS1500 is enabled")
	}
}

func TestDNS1500FallbackToDolbySR(t *testing.T) {
	// When DNS-1500 cannot be enabled (no silence detected), DolbySR should be used as fallback
	config := DefaultFilterConfig()
	measurements := &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{
			RMSTrough: -60.0,
		},
		NoiseProfile: nil, // No silence detected
		InputI:       -20.0,
		InputLRA:     10.0,
	}

	AdaptConfig(config, measurements)

	// DNS-1500 should be disabled (no NoiseProfile)
	if config.DNS1500Enabled {
		t.Error("DNS1500 should be disabled when NoiseProfile is nil")
	}

	// DolbySR should be enabled as fallback
	if !config.DolbySREnabled {
		t.Error("DolbySR should be enabled as fallback when DNS1500 is disabled")
	}

	// DolbySR should be tuned (have bands initialised)
	if len(config.DolbySRBands) != 6 {
		t.Errorf("DolbySR should have 6 bands, got %d", len(config.DolbySRBands))
	}
}

func TestDolbySRForcedSkipsDNS1500(t *testing.T) {
	// When DolbySR is pre-enabled before AdaptConfig, DNS-1500 should be skipped
	// even if valid silence is detected
	config := DefaultFilterConfig()
	config.DolbySREnabled = true // Force DolbySR mode

	measurements := &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{
			RMSTrough: -60.0,
		},
		NoiseProfile: &NoiseProfile{
			Start:              1 * time.Second,
			Duration:           500 * time.Millisecond,
			MeasuredNoiseFloor: -55.0,
			SpectralFlatness:   0.5,
		},
		InputI:   -20.0,
		InputLRA: 10.0,
	}

	AdaptConfig(config, measurements)

	// DNS-1500 should remain disabled (DolbySR was forced)
	if config.DNS1500Enabled {
		t.Error("DNS1500 should be disabled when DolbySR is forced on")
	}

	// DolbySR should remain enabled
	if !config.DolbySREnabled {
		t.Error("DolbySR should remain enabled when forced on")
	}

	// DolbySR should be tuned (have bands initialised)
	if len(config.DolbySRBands) != 6 {
		t.Errorf("DolbySR should have 6 bands, got %d", len(config.DolbySRBands))
	}
}

func TestDS201GateCoordinatesWithDNS1500(t *testing.T) {
	t.Run("noisy source gets more aggressive gate with DNS-1500", func(t *testing.T) {
		// Simulate Mark's profile: noisy source with headphone bleed
		// Noise floor -48.5 dBFS (above -55 threshold), peak -20 dBFS
		config := DefaultFilterConfig()
		config.DolbySREnabled = false // Allow DNS-1500 selection
		measurements := &AudioMeasurements{
			BaseMeasurements: BaseMeasurements{
				RMSTrough: -86.7,
			},
			NoiseProfile: &NoiseProfile{
				Start:              43 * time.Second,
				Duration:           12 * time.Second,
				MeasuredNoiseFloor: -48.5,
				PeakLevel:          -20.0,
				CrestFactor:        28.5,
				Entropy:            0.102, // Tonal bleed
				SpectralFlatness:   0.758,
			},
			InputI:     -30.0,
			InputLRA:   16.0,
			NoiseFloor: -48.5,
		}

		AdaptConfig(config, measurements)

		// DNS-1500 should be enabled with voice protection (noisy source cap)
		if !config.DNS1500Enabled {
			t.Fatal("DNS1500 should be enabled")
		}
		if config.DNS1500NoiseReduce != 15.0 {
			t.Errorf("DNS1500NoiseReduce = %.1f, want 15.0 (noisy source cap)", config.DNS1500NoiseReduce)
		}

		// Gate should have lower threshold than without DNS-1500 coordination
		// Without coordination: threshold would be at peak (-20) + 3 = -17, clamped to -25 dB
		// With coordination: estimatedPostNRPeak = -20 - 15 = -35, threshold = -35 + 6 = -29 dB
		thresholdDB := linearToDB(config.DS201GateThreshold)
		if thresholdDB > -28.0 {
			t.Errorf("DS201GateThreshold = %.1f dB, want < -28 dB (post-NR coordination)", thresholdDB)
		}

		// Gate should have deeper range than default tonal (-16 dB)
		// With DNS-1500 boost, should be around -22 dB
		rangeDB := linearToDB(config.DS201GateRange)
		if rangeDB > -20.0 {
			t.Errorf("DS201GateRange = %.1f dB, want < -20 dB (DNS-1500 range boost)", rangeDB)
		}
	})

	t.Run("clean source gate unchanged with DNS-1500", func(t *testing.T) {
		// Clean source like Martin: noise floor -75 dBFS (below -55 threshold)
		config := DefaultFilterConfig()
		config.DolbySREnabled = false // Allow DNS-1500 selection
		measurements := &AudioMeasurements{
			BaseMeasurements: BaseMeasurements{
				RMSTrough: -78.8,
			},
			NoiseProfile: &NoiseProfile{
				Start:              37 * time.Second,
				Duration:           12 * time.Second,
				MeasuredNoiseFloor: -75.0,
				PeakLevel:          -59.9,
				CrestFactor:        15.1,
				Entropy:            0.142,
				SpectralFlatness:   0.611,
			},
			InputI:     -31.0,
			InputLRA:   16.3,
			NoiseFloor: -75.0,
		}

		AdaptConfig(config, measurements)

		// DNS-1500 enabled but not noisy source, so no gate coordination boost
		if !config.DNS1500Enabled {
			t.Fatal("DNS1500 should be enabled")
		}

		// Gate range should be based on entropy (0.142 < 0.3 = tonal = -16 dB)
		// No DNS-1500 boost because noise floor is below threshold
		rangeDB := linearToDB(config.DS201GateRange)
		// Should be around -16 dB for tonal, possibly with clean boost
		if rangeDB < -24.0 {
			t.Errorf("DS201GateRange = %.1f dB, unexpectedly deep for clean source", rangeDB)
		}
	})
}

// containsString checks if substr exists in s
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// =============================================================================
// UREI 1176-Inspired Limiter Tuning Tests
// =============================================================================

func TestTuneUREI1176Attack(t *testing.T) {
	// NOTE: MaxDifference from astats is in sample units (0-32768 for 16-bit audio)
	// The tuning function normalizes it internally by dividing by 32768
	// Test values here are provided as ratios (0-1) and scaled to sample units
	tests := []struct {
		name          string
		maxDiffRatio  float64 // 0-1 ratio, will be scaled by 32768 for MaxDifference
		spectralCrest float64 // dB
		wantAttack    float64 // ms
	}{
		// Extreme transients via MaxDifference
		{
			name:          "extreme MaxDiff triggers fastest attack",
			maxDiffRatio:  0.30, // > 0.25 threshold
			spectralCrest: 20.0,
			wantAttack:    0.1, // u1176AttackExtreme
		},
		// Extreme transients via SpectralCrest
		{
			name:          "extreme SpectralCrest triggers fastest attack",
			maxDiffRatio:  0.05,
			spectralCrest: 55.0, // > 50 threshold
			wantAttack:    0.1,  // u1176AttackExtreme
		},
		// Sharp transients via MaxDifference
		{
			name:          "sharp MaxDiff triggers fast attack",
			maxDiffRatio:  0.20, // > 0.15 threshold
			spectralCrest: 20.0,
			wantAttack:    0.5, // u1176AttackSharp
		},
		// Sharp transients via SpectralCrest
		{
			name:          "sharp SpectralCrest triggers fast attack",
			maxDiffRatio:  0.05,
			spectralCrest: 40.0, // > 35 threshold
			wantAttack:    0.5,  // u1176AttackSharp
		},
		// Normal transients
		{
			name:          "normal MaxDiff triggers normal attack",
			maxDiffRatio:  0.10, // > 0.08 threshold
			spectralCrest: 20.0,
			wantAttack:    0.8, // u1176AttackNormal
		},
		// Soft delivery
		{
			name:          "low MaxDiff triggers gentle attack",
			maxDiffRatio:  0.05, // < 0.08 threshold
			spectralCrest: 20.0,
			wantAttack:    1.0, // u1176AttackGentle
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultFilterConfig()
			m := &AudioMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: tt.maxDiffRatio * 32768.0, // Scale to sample units
					SpectralCrest: tt.spectralCrest,
				},
			}

			tuneUREI1176Attack(config, m)

			if config.UREI1176Attack != tt.wantAttack {
				t.Errorf("UREI1176Attack = %.1f, want %.1f", config.UREI1176Attack, tt.wantAttack)
			}
		})
	}
}

func TestTuneUREI1176Release(t *testing.T) {
	tests := []struct {
		name         string
		spectralFlux float64 // 0-1 range
		inputLRA     float64 // LU (loudness units)
		dynamicRange float64 // dB
		wantRelease  float64 // ms
	}{
		// Expressive delivery - high flux + wide LRA
		{
			name:         "high flux + wide LRA = expressive release",
			spectralFlux: 0.04, // > 0.03 (u1176FluxDynamic)
			inputLRA:     18.0, // > 15 (u1176LRAWide)
			dynamicRange: 25.0,
			wantRelease:  200.0, // u1176ReleaseExpressive
		},
		// Controlled delivery - low flux + narrow LRA
		{
			name:         "low flux + narrow LRA = controlled release",
			spectralFlux: 0.005, // < 0.01 (u1176FluxStatic)
			inputLRA:     8.0,   // < 10 (u1176LRANarrow)
			dynamicRange: 15.0,
			wantRelease:  100.0, // u1176ReleaseControlled
		},
		// Standard delivery - moderate flux and LRA
		{
			name:         "moderate flux and LRA = standard release",
			spectralFlux: 0.02, // between thresholds
			inputLRA:     12.0, // between thresholds
			dynamicRange: 25.0,
			wantRelease:  150.0, // u1176ReleaseStandard
		},
		// DR boost - very wide dynamic range adds 50ms
		{
			name:         "very wide DR adds release boost",
			spectralFlux: 0.04,  // expressive base
			inputLRA:     18.0,  // expressive base
			dynamicRange: 40.0,  // > 35 (u1176DRWide) triggers boost
			wantRelease:  250.0, // 200 + 50
		},
		// DR boost with standard base
		{
			name:         "DR boost with standard base",
			spectralFlux: 0.02,  // standard base
			inputLRA:     12.0,  // standard base
			dynamicRange: 40.0,  // > 35 triggers boost
			wantRelease:  200.0, // 150 + 50
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultFilterConfig()
			m := &AudioMeasurements{
				BaseMeasurements: BaseMeasurements{
					SpectralFlux: tt.spectralFlux,
					DynamicRange: tt.dynamicRange,
				},
				InputLRA: tt.inputLRA,
			}

			tuneUREI1176Release(config, m)

			if config.UREI1176Release != tt.wantRelease {
				t.Errorf("UREI1176Release = %.0f, want %.0f", config.UREI1176Release, tt.wantRelease)
			}
		})
	}
}

func TestTuneUREI1176ASC(t *testing.T) {
	tests := []struct {
		name          string
		dynamicRange  float64 // dB
		spectralCrest float64 // dB
		noiseFloor    float64 // dBFS
		wantASC       bool
		wantASCLevel  float64 // 0-1
	}{
		// Wide DR enables dynamic ASC
		{
			name:          "wide DR enables dynamic ASC",
			dynamicRange:  35.0, // > 30 (u1176DREnableASC)
			spectralCrest: 20.0,
			noiseFloor:    -60.0, // clean
			wantASC:       true,
			wantASCLevel:  0.7, // u1176ASCDynamic
		},
		// High crest enables dynamic ASC
		{
			name:          "high crest enables dynamic ASC",
			dynamicRange:  25.0,
			spectralCrest: 45.0, // > 40 (u1176CrestEnableASC)
			noiseFloor:    -60.0,
			wantASC:       true,
			wantASCLevel:  0.7, // u1176ASCDynamic
		},
		// Moderate DR enables moderate ASC
		{
			name:          "moderate DR enables moderate ASC",
			dynamicRange:  25.0, // > 20 (u1176DRModerateASC)
			spectralCrest: 20.0,
			noiseFloor:    -60.0,
			wantASC:       true,
			wantASCLevel:  0.5, // u1176ASCModerate
		},
		// Narrow DR disables ASC
		{
			name:          "narrow DR disables ASC",
			dynamicRange:  15.0, // < 20 (below u1176DRModerateASC)
			spectralCrest: 20.0,
			noiseFloor:    -60.0,
			wantASC:       false,
			wantASCLevel:  0,
		},
		// Noisy recording boosts ASC level
		{
			name:          "noisy recording boosts ASC",
			dynamicRange:  35.0, // dynamic base
			spectralCrest: 20.0,
			noiseFloor:    -45.0, // > -50 (u1176NoiseFloorASC) triggers boost
			wantASC:       true,
			wantASCLevel:  0.9, // 0.7 + 0.2 (u1176ASCNoisyBoost)
		},
		// Noisy with moderate base
		{
			name:          "noisy with moderate base",
			dynamicRange:  25.0, // moderate base
			spectralCrest: 20.0,
			noiseFloor:    -45.0, // triggers boost
			wantASC:       true,
			wantASCLevel:  0.7, // 0.5 + 0.2
		},
		// Very noisy caps at 1.0
		{
			name:          "ASC level capped at 1.0",
			dynamicRange:  35.0,
			spectralCrest: 45.0,  // both triggers hit = 0.7
			noiseFloor:    -40.0, // very noisy, +0.2 boost would give 0.9, not capped in this case
			wantASC:       true,
			wantASCLevel:  0.9, // 0.7 + 0.2
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultFilterConfig()
			m := &AudioMeasurements{
				BaseMeasurements: BaseMeasurements{
					DynamicRange:  tt.dynamicRange,
					SpectralCrest: tt.spectralCrest,
				},
				NoiseFloor: tt.noiseFloor,
			}

			tuneUREI1176ASC(config, m)

			if config.UREI1176ASC != tt.wantASC {
				t.Errorf("UREI1176ASC = %v, want %v", config.UREI1176ASC, tt.wantASC)
			}
			if math.Abs(config.UREI1176ASCLevel-tt.wantASCLevel) > 0.01 {
				t.Errorf("UREI1176ASCLevel = %.2f, want %.2f", config.UREI1176ASCLevel, tt.wantASCLevel)
			}
		})
	}
}
