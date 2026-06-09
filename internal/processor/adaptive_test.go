package processor

import (
	"math"
	"reflect"
	"testing"
)

func TestAdaptConfigReturnsEffectiveConfig(t *testing.T) {
	measurements := &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{
			Spectral: SpectralMetrics{Centroid: 5000, Decrease: -0.12, Skewness: 1.2, Kurtosis: 4.0, Flux: 0.01}, DynamicRange: 32.0,

			PeakLevel: -8.0,
		},
		InputI:     -28.0,
		InputTP:    -4.0,
		InputLRA:   9.0,
		NoiseFloor: -60.0,
		NoiseProfile: &NoiseProfile{
			MeasuredNoiseFloor: -50.0,
			Entropy:            0.8,
		},
	}

	base := newTestBaseConfig()
	base.FilterOrder = []FilterID{FilterDeesser, FilterAnalysis}
	base.DS201HighPass.Enabled = true
	base.DS201HighPass.Frequency = 95.0
	base.Loudnorm.TargetI = -18.0

	effective, diagnostics := AdaptConfig(base, measurements)
	if effective == nil {
		t.Fatal("AdaptConfig returned nil")
	}
	if diagnostics == nil {
		t.Fatal("AdaptConfig returned nil diagnostics")
	}
	assertNoStaleEffectiveConfigFields(t)

	if !reflect.DeepEqual(base.FilterOrder, []FilterID{FilterDeesser, FilterAnalysis}) {
		t.Errorf("base FilterOrder = %v, want unchanged custom order", base.FilterOrder)
	}
	if base.DS201HighPass.Frequency != 95.0 {
		t.Errorf("base DS201HighPass.Frequency = %.1f, want unchanged 95.0", base.DS201HighPass.Frequency)
	}
	if base.Loudnorm.TargetI != -18.0 {
		t.Errorf("base Loudnorm.TargetI = %.1f, want unchanged -18.0", base.Loudnorm.TargetI)
	}

	if !reflect.DeepEqual(effective.FilterOrder, base.FilterOrder) {
		t.Errorf("effective FilterOrder = %v, want copied base order %v", effective.FilterOrder, base.FilterOrder)
	}
	effective.FilterOrder[0] = FilterDownmix
	if base.FilterOrder[0] == FilterDownmix {
		t.Fatal("effective FilterOrder mutation changed base FilterOrder")
	}
	if effective.DS201HighPass.Frequency == 95.0 {
		t.Fatal("effective DS201HighPass.Frequency did not adapt from base seed value")
	}
	if diagnostics.DS201LPReason != "20.5 kHz band-limit (always on)" {
		t.Errorf("diagnostics DS201LPReason = %q, want 20.5 kHz band-limit (always on)", diagnostics.DS201LPReason)
	}
	assertNoStaleEffectiveConfigFields(t)
}

func TestAdaptConfigOrderIndependence(t *testing.T) {
	sharedSeed := newOrderIndependenceSeed()
	fileA := orderIndependenceWarmNoProfileMeasurements()
	fileB := orderIndependenceBrightSpeechMeasurements()

	firstEffective, firstDiagnostics := AdaptConfig(sharedSeed, fileA)
	if firstEffective == nil {
		t.Fatal("AdaptConfig returned nil for file A")
	}
	if firstDiagnostics == nil {
		t.Fatal("AdaptConfig returned nil diagnostics for file A")
	}
	if !firstDiagnostics.DS201GateGentleMode {
		t.Fatal("file A setup failed: expected gentle gate mode")
	}
	if !firstDiagnostics.LA2AHighCrestActive {
		t.Fatal("file A setup failed: expected high-crest diagnostics active")
	}

	afterA, afterADiagnostics := AdaptConfig(sharedSeed, fileB)
	alone, aloneDiagnostics := AdaptConfig(newOrderIndependenceSeed(), fileB)
	if afterA == nil || alone == nil {
		t.Fatal("AdaptConfig returned nil for file B")
	}
	if afterADiagnostics == nil || aloneDiagnostics == nil {
		t.Fatal("AdaptConfig returned nil diagnostics for file B")
	}

	assertOrderIndependentAdaptiveFields(t, afterA, alone)
	assertOrderIndependentAdaptiveDiagnostics(t, afterADiagnostics, aloneDiagnostics)
}

func TestAdaptConfigFilterSpecBehaviourBaseline(t *testing.T) {
	tests := []struct {
		name         string
		measurements *AudioMeasurements
		want         string
	}{
		{
			name:         "warm voice without noise profile",
			measurements: orderIndependenceWarmNoProfileMeasurements(),
			want: "highpass=f=60:poles=1:width_type=q:width=0.500:normalize=1:a=tdii:m=0.80," +
				"lowpass=f=20500:poles=2:width_type=q:width=0.707:normalize=1," +
				"anlmdn=s=0.00001:p=0.0060:r=0.0058:m=11," +
				"afftdn=nr=12:nt=w:tn=1," +
				"agate=threshold=0.012589:ratio=1.2:attack=17.00:release=400:range=0.0447:knee=2.0:detection=rms:makeup=1.0," +
				"acompressor=threshold=0.011839:ratio=4.9:attack=10:release=340:makeup=1.00:knee=5.9:detection=rms:mix=0.93",
		},
		{
			name:         "bright speech with noise profile",
			measurements: orderIndependenceBrightSpeechMeasurements(),
			want: "highpass=f=110:poles=2:width_type=q:width=0.707:normalize=1:a=tdii," +
				"lowpass=f=20500:poles=2:width_type=q:width=0.707:normalize=1," +
				"anlmdn=s=0.00001:p=0.0060:r=0.0058:m=11," +
				"afftdn=nr=12:nt=w:tn=1," +
				"agate=threshold=0.019953:ratio=2.0:attack=10.00:release=250:range=0.0447:knee=5.0:detection=rms:makeup=1.0," +
				"acompressor=threshold=0.050119:ratio=3.0:attack=10:release=150:makeup=1.00:knee=4.0:detection=rms:mix=0.93",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newOrderIndependenceSeed()
			got, diagnostics := AdaptConfig(config, tt.measurements)
			if got == nil {
				t.Fatal("AdaptConfig returned nil")
			}
			if diagnostics == nil {
				t.Fatal("AdaptConfig returned nil diagnostics")
			}

			spec := got.BuildFilterSpec()
			if spec != tt.want {
				t.Errorf("BuildFilterSpec() = %q, want %q", spec, tt.want)
			}
		})
	}
}

func TestAdaptConfigSeedParameterOwnershipBoundary(t *testing.T) {
	typ := reflect.TypeOf(AdaptConfig)
	if typ.NumIn() != 2 {
		t.Fatalf("AdaptConfig has %d parameters, want 2", typ.NumIn())
	}

	assertSeedConfigTypeCannotOwnPerFileState(t, typ.In(0))
}

func newOrderIndependenceSeed() *BaseFilterConfig {
	config := newTestBaseConfig()
	config.DS201HighPass.Enabled = true
	config.DS201LowPass.Enabled = true
	config.NoiseRemove.Enabled = true
	config.DS201Gate.Enabled = true
	config.LA2A.Enabled = true
	config.Loudnorm.TargetTP = -2.0
	return config
}

func orderIndependenceWarmNoProfileMeasurements() *AudioMeasurements {
	return &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{
			Spectral: SpectralMetrics{Centroid: 6500, Decrease: -0.12, Skewness: 1.6, Kurtosis: 4.0, Flatness: 0.62, Flux: 0.008, Crest: 20.0, Rolloff: 18000}, DynamicRange: 90.0,
			PeakLevel: -10.0,
		},
		InputI:     -42.1,
		InputTP:    -4.9,
		InputLRA:   6.0,
		NoiseFloor: -58.0,
	}
}

func orderIndependenceBrightSpeechMeasurements() *AudioMeasurements {
	return &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{
			Spectral: SpectralMetrics{Centroid: 5000, Decrease: 0.0, Skewness: 0.0, Kurtosis: 9.0, Flatness: 0.38, Flux: 0.002, Crest: 45.0, Rolloff: 15000}, DynamicRange: 32.0,
			PeakLevel:         -6.0,
			ZeroCrossingsRate: 0.05,
		},
		InputI:     -20.0,
		InputTP:    -2.5,
		InputLRA:   12.0,
		NoiseFloor: -60.0,
		NoiseProfile: &NoiseProfile{
			MeasuredNoiseFloor: -60.0,
			PeakLevel:          -45.0,
			CrestFactor:        15.0,
			Entropy:            0.8,
		},
		SpeechProfile: &SpeechCandidateMetrics{
			RMSLevel:    -24.0,
			CrestFactor: 12.0,
			Spectral: SpectralMetrics{
				Centroid: 5000,
				Decrease: 0.0,
				Skewness: 0.0,
				Kurtosis: 9.0,
				Flux:     0.002,
				Rolloff:  15000,
			},
		},
	}
}

func assertOrderIndependentAdaptiveFields(t *testing.T, got, want *EffectiveFilterConfig) {
	t.Helper()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"DS201HighPass.Frequency", got.DS201HighPass.Frequency, want.DS201HighPass.Frequency},
		{"DS201HighPass.Poles", got.DS201HighPass.Poles, want.DS201HighPass.Poles},
		{"DS201HighPass.Width", got.DS201HighPass.Width, want.DS201HighPass.Width},
		{"DS201HighPass.Mix", got.DS201HighPass.Mix, want.DS201HighPass.Mix},
		{"DS201HighPass.Transform", got.DS201HighPass.Transform, want.DS201HighPass.Transform},
		{"NoiseRemove.AfftdnEnabled", got.NoiseRemove.AfftdnEnabled, want.NoiseRemove.AfftdnEnabled},
		{"NoiseRemove.AfftdnNoiseReduction", got.NoiseRemove.AfftdnNoiseReduction, want.NoiseRemove.AfftdnNoiseReduction},
		{"DS201LowPass.Enabled", got.DS201LowPass.Enabled, want.DS201LowPass.Enabled},
		{"DS201LowPass.Frequency", got.DS201LowPass.Frequency, want.DS201LowPass.Frequency},
		{"LA2A.Threshold", got.LA2A.Threshold, want.LA2A.Threshold},
		{"LA2A.Ratio", got.LA2A.Ratio, want.LA2A.Ratio},
		{"LA2A.Release", got.LA2A.Release, want.LA2A.Release},
		{"LA2A.Knee", got.LA2A.Knee, want.LA2A.Knee},
	}

	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func assertOrderIndependentAdaptiveDiagnostics(t *testing.T, got, want *AdaptiveDiagnostics) {
	t.Helper()

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"DS201LPReason", got.DS201LPReason, want.DS201LPReason},
		{"DS201GateGentleMode", got.DS201GateGentleMode, want.DS201GateGentleMode},
		{"DS201GateAggression", got.DS201GateAggression, want.DS201GateAggression},
		{"DS201GateDynamicRange", got.DS201GateDynamicRange, want.DS201GateDynamicRange},
		{"DS201GateQuietSpeechEstimate", got.DS201GateQuietSpeechEstimate, want.DS201GateQuietSpeechEstimate},
		{"DS201GateSpeechSeparation", got.DS201GateSpeechSeparation, want.DS201GateSpeechSeparation},
		{"DS201GateSpeechHeadroom", got.DS201GateSpeechHeadroom, want.DS201GateSpeechHeadroom},
		{"DS201GateThresholdUnclamped", got.DS201GateThresholdUnclamped, want.DS201GateThresholdUnclamped},
		{"DS201GateClampReason", got.DS201GateClampReason, want.DS201GateClampReason},
		{"LA2AHighCrestActive", got.LA2AHighCrestActive, want.LA2AHighCrestActive},
		{"LA2AHighCrestDeficit", got.LA2AHighCrestDeficit, want.LA2AHighCrestDeficit},
		{"LA2AHighCrestSeverity", got.LA2AHighCrestSeverity, want.LA2AHighCrestSeverity},
		{"LA2AHighCrestProjectedTP", got.LA2AHighCrestProjectedTP, want.LA2AHighCrestProjectedTP},
	}

	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
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
		noiseProfile     *NoiseProfile // room-tone region noise profile characteristics
		wantFreqMin      float64       // minimum expected frequency
		wantFreqMax      float64       // maximum expected frequency
		wantPoles        int           // expected poles (0 = don't check, 1 = gentle, 2 = standard)
		wantWidth        float64       // expected Q (0 = don't check, uses Butterworth 0.707 default)
		wantMix          float64       // expected mix (0 = don't check, uses 1.0 default)
		wantDisabled     bool          // expect highpass to be disabled entirely (legacy, now rarely used)
	}{
		// Voice brightness classification (no noise profile - base frequencies only)
		// Thresholds: centroidNormal=2500 Hz, centroidBright=4000 Hz
		{
			name:             "dark voice, no noise profile",
			centroid:         2000, // below centroidNormal (2500)
			spectralDecrease: 0.0,
			noiseProfile:     nil,
			wantFreqMin:      60, // highpassMinFreq
			wantFreqMax:      60,
		},
		{
			name:             "normal voice, no noise profile",
			centroid:         3500, // between centroidNormal (2500) and centroidBright (4000)
			spectralDecrease: 0.0,
			noiseProfile:     nil,
			wantFreqMin:      80, // highpassDefaultFreq
			wantFreqMax:      80,
		},
		{
			name:             "bright voice, no noise profile",
			centroid:         5000, // above centroidBright (4000)
			spectralDecrease: 0.0,
			noiseProfile:     nil,
			wantFreqMin:      100, // highpassBrightFreq
			wantFreqMax:      100,
		},

		// Clean room-tone region (< -70 dBFS) - no boost regardless of entropy
		{
			name:             "bright voice, clean room tone, broadband noise",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-75.0, 0.8), // clean, broadband
			wantFreqMin:      100,                          // no boost - too clean (bright base)
			wantFreqMax:      100,
		},

		// Moderate noise (> -70 dBFS) with broadband character - moderate boost
		{
			name:             "bright voice, moderate noise, broadband",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-65.0, 0.7), // moderate, broadband
			wantFreqMin:      110,                          // 100 + 10 boost
			wantFreqMax:      110,
		},

		// Noisy room tone (> -55 dBFS) with broadband character - aggressive boost
		{
			name:             "bright voice, noisy room tone, broadband",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-50.0, 0.8), // noisy, broadband
			wantFreqMin:      120,                          // 100 + 20 boost (capped at max)
			wantFreqMax:      120,
		},

		// Tonal noise (low entropy) - no boost, bandreject handles it
		{
			name:             "bright voice, noisy room tone, tonal (hum)",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-50.0, 0.25), // noisy but tonal (below 0.30 threshold)
			wantFreqMin:      100,                           // no boost - tonal noise (bright base)
			wantFreqMax:      100,
		},

		// Warm voice protection (spectral decrease < -0.05, tiered at -0.10)
		{
			name:             "warm voice, noisy broadband - capped at 80Hz",
			centroid:         5000,
			spectralDecrease: -0.06, // warm voice (between -0.05 and -0.10)
			noiseProfile:     makeNoiseProfile(-50.0, 0.8),
			wantFreqMin:      80, // would be 120, but capped at 80Hz due to warm voice
			wantFreqMax:      80,
			wantPoles:        0, // standard slope (don't check, defaults to 2)
		},
		{
			name:             "very warm voice - gentle highpass",
			centroid:         5000,                         // bright voice base = 100Hz
			spectralDecrease: -0.12,                        // very warm voice (< -0.10)
			noiseProfile:     makeNoiseProfile(-50.0, 0.8), // broadband noise
			wantFreqMin:      60,                           // highpassVeryWarmFreq
			wantFreqMax:      60,
			wantPoles:        1,   // gentle 6dB/oct
			wantWidth:        0.5, // gentle Q
			wantMix:          0.8, // 80% wet
		},
		{
			name:             "very warm dark voice - gentle highpass",
			centroid:         2000,                         // dark voice base = 60Hz
			spectralDecrease: -0.15,                        // very warm (< -0.10)
			noiseProfile:     makeNoiseProfile(-45.0, 0.9), // broadband noise
			wantFreqMin:      60,                           // highpassVeryWarmFreq
			wantFreqMax:      60,
			wantPoles:        1,   // gentle 6dB/oct
			wantWidth:        0.5, // gentle Q
			wantMix:          0.8, // 80% wet
		},

		// Bright voice with warm spectral decrease (unusual but possible)
		{
			name:             "very bright voice, warm characteristics - capped at 80Hz",
			centroid:         5000,
			spectralDecrease: -0.06, // warm despite bright centroid (between -0.05 and -0.10)
			noiseProfile:     makeNoiseProfile(-50.0, 0.8),
			wantFreqMin:      80, // would be 120, but capped at 80Hz due to warm voice
			wantFreqMax:      80,
			wantPoles:        0, // standard slope (don't check)
		},
		{
			name:             "very bright voice, very warm characteristics - gentle highpass",
			centroid:         5000,
			spectralDecrease: -0.12,                        // very warm despite bright centroid (< -0.10)
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
			centroid:         5785,                           // bright centroid (> 4000)
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
			wantFreqMin:      100,                          // bright voice (centroid 5000 > 4000)
			wantFreqMax:      100,
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
			centroid:         6500,                         // > 4000 centroidBright threshold
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
			wantFreqMin:      80, // DefaultFilterConfig().DS201HighPass.Frequency
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
			centroid:         2500, // exactly at centroidNormal threshold
			spectralDecrease: 0.0,
			noiseProfile:     nil,
			wantFreqMin:      60, // dark voice (not > centroidNormal)
			wantFreqMax:      60,
		},
		{
			name:             "boundary: exactly at centroidBright",
			centroid:         4000, // exactly at centroidBright threshold
			spectralDecrease: 0.0,
			noiseProfile:     nil,
			wantFreqMin:      80, // normal voice (not > centroidBright)
			wantFreqMax:      80,
		},
		{
			name:             "boundary: exactly at roomToneEntropyTonal",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-50.0, 0.30), // exactly at threshold
			wantFreqMin:      120,                           // broadband (>= 0.30), gets boost (bright base 100 + 20)
			wantFreqMax:      120,
		},
		{
			name:             "boundary: exactly at roomToneNoiseFloorClean",
			centroid:         5000,
			spectralDecrease: 0.0,
			noiseProfile:     makeNoiseProfile(-70.0, 0.8), // exactly at clean threshold
			wantFreqMin:      100,                          // no boost (not > -70), bright base
			wantFreqMax:      100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup: create test config and measurements
			config := newTestConfig()
			// Start with highpass enabled to test tuning behavior
			config.DS201HighPass.Enabled = true
			measurements := &AudioMeasurements{
				BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Centroid: tt.centroid, Decrease: tt.spectralDecrease, Skewness: tt.spectralSkewness}},
				NoiseProfile:     tt.noiseProfile,
			}

			// Execute
			tuneDS201HighPass(config, measurements)

			// Verify disabled state (legacy - now rarely used)
			if tt.wantDisabled {
				if config.DS201HighPass.Enabled {
					t.Errorf("DS201HighPass.Enabled = true, want false")
				}
				return // no further checks needed for disabled
			}

			// Verify enabled (warm voices now use gentle settings instead of disabling)
			if !config.DS201HighPass.Enabled {
				t.Errorf("DS201HighPass.Enabled = false, want true")
			}

			// Verify frequency
			if config.DS201HighPass.Frequency < tt.wantFreqMin || config.DS201HighPass.Frequency > tt.wantFreqMax {
				t.Errorf("DS201HighPass.Frequency = %.1f Hz, want [%.1f, %.1f] Hz",
					config.DS201HighPass.Frequency, tt.wantFreqMin, tt.wantFreqMax)
			}

			// Verify poles (slope) if specified
			if tt.wantPoles > 0 && config.DS201HighPass.Poles != tt.wantPoles {
				t.Errorf("DS201HighPass.Poles = %d, want %d", config.DS201HighPass.Poles, tt.wantPoles)
			}

			// Verify width (Q) if specified
			if tt.wantWidth > 0 && config.DS201HighPass.Width != tt.wantWidth {
				t.Errorf("DS201HighPass.Width = %.3f, want %.3f", config.DS201HighPass.Width, tt.wantWidth)
			}

			// Verify mix if specified
			if tt.wantMix > 0 && config.DS201HighPass.Mix != tt.wantMix {
				t.Errorf("DS201HighPass.Mix = %.2f, want %.2f", config.DS201HighPass.Mix, tt.wantMix)
			}
		})
	}

	t.Run("resets adaptive baseline after warm tuning", func(t *testing.T) {
		config := newTestConfig()
		config.DS201HighPass.Enabled = true

		tuneDS201HighPass(config, &AudioMeasurements{
			BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Centroid: 5000, Decrease: -0.12}},
			NoiseProfile:     makeNoiseProfile(-50.0, 0.8),
		})

		if config.DS201HighPass.Poles != 1 || config.DS201HighPass.Width != ds201HPWarmWidth || config.DS201HighPass.Mix != ds201HPVeryWarmMix {
			t.Fatalf("warm tuning did not enter gentle baseline: poles=%d width=%.3f mix=%.2f",
				config.DS201HighPass.Poles, config.DS201HighPass.Width, config.DS201HighPass.Mix)
		}

		tuneDS201HighPass(config, &AudioMeasurements{
			BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Centroid: 5000}},
		})

		if config.DS201HighPass.Frequency != ds201HPBrightFreq {
			t.Errorf("DS201HighPass.Frequency = %.1f, want %.1f", config.DS201HighPass.Frequency, ds201HPBrightFreq)
		}
		if config.DS201HighPass.Poles != ds201HPDefaultPoles {
			t.Errorf("DS201HighPass.Poles = %d, want %d", config.DS201HighPass.Poles, ds201HPDefaultPoles)
		}
		if config.DS201HighPass.Width != ds201HPDefaultWidth {
			t.Errorf("DS201HighPass.Width = %.3f, want %.3f", config.DS201HighPass.Width, ds201HPDefaultWidth)
		}
		if config.DS201HighPass.Mix != ds201HPDefaultMix {
			t.Errorf("DS201HighPass.Mix = %.2f, want %.2f", config.DS201HighPass.Mix, ds201HPDefaultMix)
		}
		if config.DS201HighPass.Transform != ds201HPDefaultTransform {
			t.Errorf("DS201HighPass.Transform = %q, want %q", config.DS201HighPass.Transform, ds201HPDefaultTransform)
		}
	})
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
				BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Kurtosis: tt.kurtosis, Flatness: tt.flatness, Flux: tt.flux, Crest: tt.crest}},
			}

			got := detectContentType(m)

			if got != tt.want {
				t.Errorf("detectContentType() = %v, want %v [%s]", got, tt.want, tt.desc)
			}
		})
	}
}

func TestTuneDS201LowPass(t *testing.T) {
	// The DS201 low-pass is an unconditional 20.5 kHz band-limit: always enabled
	// at 20500 Hz with 12 dB/oct (poles=2), regardless of content type or HF-noise
	// measurements. These cases span speech, music, mixed, dark-voice, ultrasonic,
	// and HF-noise measurement profiles to prove no adaptive branch survives.
	tests := []struct {
		name     string
		kurtosis float64
		flatness float64
		flux     float64
		crest    float64
		rolloff  float64
		centroid float64
		slope    float64
		zcr      float64
		desc     string
	}{
		{
			name:     "clean podcast speech",
			kurtosis: 9.2, flatness: 0.38, flux: 0.002, crest: 45.0,
			rolloff: 8809, centroid: 3736, slope: -5.66e-05, zcr: 0.052,
			desc: "typical podcast speech profile",
		},
		{
			name:     "speech with ultrasonic content",
			kurtosis: 8.0, flatness: 0.40, flux: 0.002, crest: 40.0,
			rolloff: 15000, centroid: 5000, slope: -3e-05, zcr: 0.05,
			desc: "high rolloff no longer down-tunes the cutoff",
		},
		{
			name:     "music sting",
			kurtosis: 3.5, flatness: 0.61, flux: 0.008, crest: 18.0,
			rolloff: 16000, centroid: 5500, slope: -2e-05, zcr: 0.08,
			desc: "music profile still emits the band-limit",
		},
		{
			name:     "speech over music bed",
			kurtosis: 5.2, flatness: 0.52, flux: 0.004, crest: 27.0,
			rolloff: 12000, centroid: 4200, slope: -2e-05, zcr: 0.06,
			desc: "mixed content still emits the band-limit",
		},
		{
			name:     "dark voice - already limited HF",
			kurtosis: 7.5, flatness: 0.42, flux: 0.002, crest: 35.0,
			rolloff: 7000, centroid: 3500, slope: -8e-06, zcr: 0.05,
			desc: "dark voice still emits the band-limit",
		},
		{
			name:     "speech with HF noise pattern",
			kurtosis: 8.0, flatness: 0.38, flux: 0.002, crest: 40.0,
			rolloff: 9000, centroid: 3500, slope: -4e-05, zcr: 0.12,
			desc: "high ZCR with low centroid no longer triggers a down-tune",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestConfig()
			diagnostics := &AdaptiveDiagnostics{}
			m := &AudioMeasurements{
				BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Kurtosis: tt.kurtosis, Flatness: tt.flatness, Flux: tt.flux, Crest: tt.crest, Rolloff: tt.rolloff, Centroid: tt.centroid, Slope: tt.slope}, ZeroCrossingsRate: tt.zcr},
			}

			tuneDS201LowPass(config, diagnostics, m)

			if !config.DS201LowPass.Enabled {
				t.Errorf("DS201LowPass.Enabled = false, want true [%s]", tt.desc)
			}
			if config.DS201LowPass.Frequency != ds201LPBandLimitFreq {
				t.Errorf("DS201LowPass.Frequency = %.0f Hz, want %.0f Hz [%s]",
					config.DS201LowPass.Frequency, ds201LPBandLimitFreq, tt.desc)
			}
			if config.DS201LowPass.Poles != 2 {
				t.Errorf("DS201LowPass.Poles = %d, want 2 [%s]", config.DS201LowPass.Poles, tt.desc)
			}
			if config.DS201LowPass.Mix != 1.0 {
				t.Errorf("DS201LowPass.Mix = %.2f, want 1.0 [%s]", config.DS201LowPass.Mix, tt.desc)
			}
			if diagnostics.DS201LPReason != "20.5 kHz band-limit (always on)" {
				t.Errorf("DS201LPReason = %q, want 20.5 kHz band-limit (always on) [%s]",
					diagnostics.DS201LPReason, tt.desc)
			}

			assertNoStaleEffectiveConfigFields(t)
		})
	}
}

func TestTuneDeesser(t *testing.T) {
	// Constants from adaptive.go for reference:
	// centroidVeryBright = 6000 Hz
	// centroidBright     = 4000 Hz
	// rolloffNoSibilance = 4000 Hz
	// rolloffLimited     = 8000 Hz
	// rolloffExtensive   = 12000 Hz
	// deessIntensityBright = 0.6
	// deessIntensityNormal = 0.5
	// deessIntensityDark   = 0.4
	// deessIntensityMax    = 0.8
	// deessIntensityMin    = 0.3

	tests := []struct {
		name          string
		centroid      float64                 // spectral centroid (Hz)
		rolloff       float64                 // spectral rolloff (Hz)
		wantIntensity float64                 // expected de-esser intensity
		tolerance     float64                 // acceptable tolerance for floating point
		speechProfile *SpeechCandidateMetrics // nil = no speech profile
	}{
		// Full adaptive logic (both centroid and rolloff available)
		// Very bright voice (centroid > 6000) with extensive HF (rolloff > 12000)
		{
			name:          "very bright voice, extensive HF",
			centroid:      7500,
			rolloff:       14000,
			wantIntensity: 0.72, // 0.6 * 1.2 = 0.72, below max 0.8
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},
		// Bright voice (centroid 4000-6000) with extensive HF
		{
			name:          "bright voice, extensive HF",
			centroid:      5000,
			rolloff:       14000,
			wantIntensity: 0.6, // 0.5 * 1.2 = 0.6
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},
		// Dark voice (centroid < 4000) with limited HF (rolloff 4000-8000)
		// Dark voice base is 0.4, limited HF applies 0.7 factor = 0.28
		// But 0.28 < deessIntensityMin (0.3), so it gets disabled
		{
			name:          "dark voice, limited HF - disabled below min",
			centroid:      3500,
			rolloff:       7000,
			wantIntensity: 0.0, // 0.4 * 0.7 = 0.28 < 0.3 min, disabled
			tolerance:     0.0,
			speechProfile: &SpeechCandidateMetrics{},
		},
		// Bright voice with limited HF - above min threshold
		{
			name:          "bright voice, limited HF",
			centroid:      5000,
			rolloff:       7000,
			wantIntensity: 0.35, // 0.5 * 0.7 = 0.35 > 0.3 min
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},
		// No HF content (rolloff < 4000) - disabled regardless of centroid
		{
			name:          "very bright voice, no HF content",
			centroid:      7500,
			rolloff:       3500,
			wantIntensity: 0.0, // disabled due to no sibilance expected
			tolerance:     0.0,
			speechProfile: &SpeechCandidateMetrics{},
		},
		{
			name:          "bright voice, no HF content",
			centroid:      5000,
			rolloff:       3500,
			wantIntensity: 0.0,
			tolerance:     0.0,
			speechProfile: &SpeechCandidateMetrics{},
		},
		// Normal HF extension (8000-12000)
		{
			name:          "very bright voice, normal HF",
			centroid:      7500,
			rolloff:       10000,
			wantIntensity: 0.6, // base intensity, no modifier
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},
		{
			name:          "bright voice, normal HF",
			centroid:      5000,
			rolloff:       10000,
			wantIntensity: 0.5,
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},
		{
			name:          "dark voice, normal HF",
			centroid:      3500,
			rolloff:       10000,
			wantIntensity: 0.4,
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},

		// Limited HF with intensity below minimum - should disable
		{
			name:          "dark voice, limited HF, below min threshold",
			centroid:      3500, // dark voice, base 0.4
			rolloff:       7500, // limited HF, * 0.7 = 0.28 < 0.3 min
			wantIntensity: 0.0,  // disabled because 0.28 < deessIntensityMin
			tolerance:     0.0,
			speechProfile: &SpeechCandidateMetrics{},
		},

		// Centroid-only fallback (rolloff = 0)
		{
			name:          "very bright voice, no rolloff data",
			centroid:      7500,
			rolloff:       0,
			wantIntensity: 0.6, // deessIntensityBright
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},
		{
			name:          "bright voice, no rolloff data",
			centroid:      5000,
			rolloff:       0,
			wantIntensity: 0.5, // deessIntensityNormal
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},
		{
			name:          "dark voice, no rolloff data",
			centroid:      3500,
			rolloff:       0,
			wantIntensity: 0.4, // deessIntensityDark
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
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
			centroid:      6000, // exactly at threshold
			rolloff:       10000,
			wantIntensity: 0.5, // not > 6000, so uses deessIntensityNormal
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},
		{
			name:          "boundary: exactly at centroidBright",
			centroid:      4000,
			rolloff:       10000,
			wantIntensity: 0.4, // not > 4000, so uses deessIntensityDark
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},
		{
			name:          "boundary: exactly at rolloffLimited",
			centroid:      7500,
			rolloff:       8000, // exactly at threshold
			wantIntensity: 0.6,  // not < 8000, falls to default (normal HF)
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},
		{
			name:          "boundary: exactly at rolloffExtensive",
			centroid:      7500,
			rolloff:       12000, // exactly at threshold
			wantIntensity: 0.6,   // not > 12000, falls to default (normal HF)
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},

		// Max capping
		{
			name:          "intensity capped at max",
			centroid:      7500,  // very bright, base 0.6
			rolloff:       15000, // extensive, * 1.2 = 0.72
			wantIntensity: 0.72,  // below max 0.8, so not capped
			tolerance:     0.01,
			speechProfile: &SpeechCandidateMetrics{},
		},

		// No speech profile - guard clause disables de-esser
		{
			name:          "no speech profile - deesser disabled",
			centroid:      7500,  // Would normally trigger bright de-essing
			rolloff:       12000, // Would normally trigger extensive HF
			wantIntensity: 0.0,   // But no speech profile = disabled
			tolerance:     0.0,
			speechProfile: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			config := newTestConfig()
			// Start with deesser disabled - tuneDeesser should set intensity based on spectral data
			config.Deesser.Intensity = 0.0
			measurements := &AudioMeasurements{
				BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Centroid: tt.centroid, Rolloff: tt.rolloff}},
				SpeechProfile:    tt.speechProfile,
			}

			// Execute
			tuneDeesser(config, measurements)

			// Verify
			diff := config.Deesser.Intensity - tt.wantIntensity
			if diff < 0 {
				diff = -diff
			}
			if diff > tt.tolerance {
				t.Errorf("Deesser.Intensity = %.3f, want %.3f (±%.3f) [centroid=%.0f, rolloff=%.0f]",
					config.Deesser.Intensity, tt.wantIntensity, tt.tolerance, tt.centroid, tt.rolloff)
			}
		})
	}
}

func TestTuneDS201Gate(t *testing.T) {
	// Tests the comprehensive gate tuning which calculates all gate parameters
	// based on measurements including NoiseProfile (extracted from the elected
	// room-tone region).
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
			roomTonePeak    float64 // dB
			roomToneCrest   float64 // dB - determines if we use peak or floor
			inputLRA        float64 // LU - determines ratio, which determines gap
			wantThresholdDB float64 // expected threshold dB
			tolerance       float64 // tolerance in dB
			desc            string
		}{
			{
				name:            "clean studio - uses target threshold",
				noiseFloor:      -75.0,
				roomTonePeak:    -70.0,
				roomToneCrest:   10.0, // Low crest = stable noise, use floor
				inputLRA:        8.0,  // Narrow LRA → ratio 2.5 → gap 20dB → -75+20=-55, but target -40 is higher
				wantThresholdDB: -40.0,
				tolerance:       1.0,
				desc:            "very clean, uses target threshold -40dB",
			},
			{
				name:            "typical podcast - derived gap with moderate ratio",
				noiseFloor:      -55.0,
				roomTonePeak:    -50.0,
				roomToneCrest:   10.0, // Low crest = stable noise
				inputLRA:        12.0, // Moderate LRA → ratio 2.0 → gap 24dB → -55+24=-31
				wantThresholdDB: -31.0,
				tolerance:       1.0,
				desc:            "moderate noise floor with derived gap",
			},
			{
				name:            "noisy room - derived gap",
				noiseFloor:      -42.0,
				roomTonePeak:    -38.0,
				roomToneCrest:   10.0,
				inputLRA:        8.0, // Narrow LRA → ratio 2.5 → gap 20dB → -42+20=-22, clamped to -25
				wantThresholdDB: -25.0,
				tolerance:       1.0,
				desc:            "noisy floor, threshold clamped to max",
			},
			{
				name:            "bleed with high crest - uses peak + margin",
				noiseFloor:      -55.0,
				roomTonePeak:    -48.0, // Transient spikes
				roomToneCrest:   25.0,  // High crest = transient bleed
				inputLRA:        12.0,
				wantThresholdDB: -45.0, // -48 (peak) + 3dB margin
				tolerance:       1.0,
				desc:            "high crest factor triggers peak reference",
			},
			{
				name:            "extreme noise - clamped to max",
				noiseFloor:      -20.0,
				roomTonePeak:    -15.0,
				roomToneCrest:   25.0,
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
						PeakLevel:   tt.roomTonePeak,
						CrestFactor: tt.roomToneCrest,
						Entropy:     0.5, // Moderate entropy
					},
				}

				tuneDS201GateForTest(config, measurements)

				actualDB := linearToDB(config.DS201Gate.Threshold)
				diff := actualDB - tt.wantThresholdDB
				if diff < 0 {
					diff = -diff
				}
				if diff > tt.tolerance {
					t.Errorf("DS201Gate.Threshold = %.1f dB, want %.1f dB ±%.1f [%s]",
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

				tuneDS201GateForTest(config, measurements)

				if config.DS201Gate.Ratio != tt.wantRatio {
					t.Errorf("DS201Gate.Ratio = %.1f, want %.1f [%s]", config.DS201Gate.Ratio, tt.wantRatio, tt.desc)
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
			{"fast transients", 0.3, 1.0, 10.0, 1.0, "fast attack (minimum 10ms) for sharp transients"},
			{"slow transients no flux", 0.05, 0.02, 17.0, 1.0, "slow attack 17ms for gentle speech with low flux"},
			{"moderate with flux", 0.15, 0.1, 10.0, 1.0, "moderate attack (12*0.8=9.6, clamped to 10ms) with dynamic flux"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Flux: tt.spectralFlux}, MaxDifference: tt.maxDiff},
					NoiseFloor:       -55.0,
				}

				tuneDS201GateForTest(config, measurements)

				diff := config.DS201Gate.Attack - tt.wantAttackMS
				if diff < 0 {
					diff = -diff
				}
				if diff > tt.tolerance {
					t.Errorf("DS201Gate.Attack = %.1f ms, want %.1f ms ±%.1f [%s]",
						config.DS201Gate.Attack, tt.wantAttackMS, tt.tolerance, tt.desc)
				}
			})
		}
	})

	t.Run("detection mode based on noise character", func(t *testing.T) {
		// Detection uses RMS for tonal or spiky noise, peak for clean
		// gateEntropyTonal = 0.3, gateRoomToneCrestThreshold = 25.0
		// gateEntropyClean = 0.7 (above this + crest < 15 = peak)
		tests := []struct {
			name            string
			roomToneEntropy float64
			roomToneCrest   float64
			wantDetection   string
			desc            string
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
						CrestFactor: tt.roomToneCrest,
						Entropy:     tt.roomToneEntropy,
					},
				}

				tuneDS201GateForTest(config, measurements)

				if config.DS201Gate.Detection != tt.wantDetection {
					t.Errorf("DS201Gate.Detection = %q, want %q [%s]",
						config.DS201Gate.Detection, tt.wantDetection, tt.desc)
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

				tuneDS201GateForTest(config, measurements)

				actualDB := linearToDB(config.DS201Gate.Range)
				diff := actualDB - tt.wantRangeDB
				if diff < 0 {
					diff = -diff
				}
				if diff > tt.tolerance {
					t.Errorf("DS201Gate.Range = %.1f dB, want %.1f dB ±%.1f [%s]",
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
		tuneDS201GateForTest(config, measurements)

		// Should still calculate threshold from noise floor
		thresholdDB := linearToDB(config.DS201Gate.Threshold)
		if thresholdDB < -70 || thresholdDB > -25 {
			t.Errorf("DS201Gate.Threshold = %.1f dB, want within bounds [-70, -25]", thresholdDB)
		}

		// Detection should default to RMS when no profile
		if config.DS201Gate.Detection != "rms" {
			t.Errorf("DS201Gate.Detection = %q, want 'rms' (default for missing profile)", config.DS201Gate.Detection)
		}
	})

	t.Run("release based on noise entropy", func(t *testing.T) {
		// Release times adapt based on room-tone entropy:
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
		// Base release values (tuned for tight noise control):
		// - ds201GateReleaseMod = 250ms
		// - ds201GateReleaseSustained = 300ms
		// - ds201GateReleaseDynamic = 180ms
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
					BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Flux: 0.02}}, // Moderate flux (uses ds201GateReleaseMod)

					NoiseFloor: -55.0,
					InputLRA:   15.0, // Above LRA threshold (10 LU) to avoid LRA-based extension
					NoiseProfile: &NoiseProfile{
						PeakLevel:   -50.0,
						CrestFactor: 15.0,
						Entropy:     tt.entropy,
					},
				}

				tuneDS201GateForTest(config, measurements)

				if config.DS201Gate.Release < tt.wantReleaseMin || config.DS201Gate.Release > tt.wantReleaseMax {
					t.Errorf("DS201Gate.Release = %.1f ms, want %.1f-%.1f ms [%s]",
						config.DS201Gate.Release, tt.wantReleaseMin, tt.wantReleaseMax, tt.desc)
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
					BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Flux: 0.02}}, // Moderate flux

					NoiseFloor: -55.0,
					InputLRA:   tt.lra,
					NoiseProfile: &NoiseProfile{
						PeakLevel:   -50.0,
						CrestFactor: 15.0,
						Entropy:     0.14, // Mixed entropy (no tonal extension)
					},
				}

				tuneDS201GateForTest(config, measurements)

				if config.DS201Gate.Release < tt.wantReleaseMin || config.DS201Gate.Release > tt.wantReleaseMax {
					t.Errorf("DS201Gate.Release = %.1f ms (LRA=%.1f LU), want %.1f-%.1f ms [%s]",
						config.DS201Gate.Release, tt.lra, tt.wantReleaseMin, tt.wantReleaseMax, tt.desc)
				}
			})
		}
	})

	t.Run("populates diagnostics with speech metrics", func(t *testing.T) {
		config := newTestConfig()
		diagnostics := tuneDS201GateForTest(config, &AudioMeasurements{
			BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Flux: 0.02, Crest: 20.0}},
			InputI:           -48.0,
			InputLRA:         6.0,
			NoiseFloor:       -70.0,
			NoiseProfile: &NoiseProfile{
				PeakLevel:   -65.0,
				CrestFactor: 12.0,
				Entropy:     0.5,
			},
			SpeechProfile: &SpeechCandidateMetrics{
				RMSLevel:    -35.0,
				CrestFactor: 10.0,
			},
		})

		if !diagnostics.DS201GateGentleMode {
			t.Fatal("expected first tuning to enable gentle mode")
		}
		if diagnostics.DS201GateAggression == 0 ||
			diagnostics.DS201GateDynamicRange == 0 ||
			diagnostics.DS201GateQuietSpeechEstimate == 0 ||
			diagnostics.DS201GateSpeechSeparation == 0 ||
			diagnostics.DS201GateThresholdUnclamped == 0 ||
			diagnostics.DS201GateClampReason == "" {
			t.Fatalf("expected tuning to populate gate diagnostics: %+v", diagnostics)
		}
		if config.DS201Gate.Ratio != ds201GateGentleRatio || config.DS201Gate.Knee != ds201GateGentleKnee {
			t.Fatalf("expected gentle mode to tune builder values, ratio=%.1f knee=%.1f",
				config.DS201Gate.Ratio, config.DS201Gate.Knee)
		}
		assertNoStaleEffectiveConfigFields(t)
	})

	t.Run("fresh diagnostics without speech metrics", func(t *testing.T) {
		config := newTestConfig()
		diagnostics := tuneDS201GateForTest(config, &AudioMeasurements{
			BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Flux: 0.02}},
			InputI:           -20.0,
			InputLRA:         16.0,
			NoiseFloor:       -55.0,
		})

		if diagnostics.DS201GateGentleMode {
			t.Error("diagnostics DS201GateGentleMode = true, want false")
		}
		if diagnostics.DS201GateAggression != 0 ||
			diagnostics.DS201GateDynamicRange != 0 ||
			diagnostics.DS201GateQuietSpeechEstimate != 0 ||
			diagnostics.DS201GateSpeechSeparation != 0 ||
			diagnostics.DS201GateSpeechHeadroom != 0 ||
			diagnostics.DS201GateThresholdUnclamped != 0 ||
			diagnostics.DS201GateClampReason != "" {
			t.Errorf("fresh gate diagnostics populated without speech metrics: %+v", diagnostics)
		}
	})
}

func tuneDS201GateForTest(config *EffectiveFilterConfig, measurements *AudioMeasurements) *AdaptiveDiagnostics {
	diagnostics := &AdaptiveDiagnostics{}
	tuneDS201Gate(config, diagnostics, measurements)
	return diagnostics
}

// linearToDB converts linear amplitude to dB for test error messages
func linearToDB(linear float64) float64 {
	if linear <= 0 {
		return -1000 // avoid math.Log10(0) = -Inf
	}
	return 20 * math.Log10(linear)
}

func TestPreferSpeechMetric(t *testing.T) {
	tests := []struct {
		name          string
		fullFile      float64
		speechProfile float64
		want          float64
	}{
		{"speech profile available", 1000.0, 1500.0, 1500.0},
		{"speech profile zero", 1000.0, 0.0, 1000.0},
		{"speech profile negative", 1000.0, -1.0, 1000.0},
		{"both zero", 0.0, 0.0, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := preferSpeechMetric(tt.fullFile, tt.speechProfile)
			if got != tt.want {
				t.Errorf("preferSpeechMetric(%v, %v) = %v, want %v",
					tt.fullFile, tt.speechProfile, got, tt.want)
			}
		})
	}
}

func TestPreferSpeechMetricSigned(t *testing.T) {
	tests := []struct {
		name        string
		fullFile    float64
		speechValue float64
		hasSpeech   bool
		want        float64
	}{
		{"speech available positive", 1000.0, 1500.0, true, 1500.0},
		{"speech available negative", -0.05, -0.12, true, -0.12},
		{"speech available zero", -0.05, 0.0, true, 0.0},
		{"no speech falls back", 1000.0, 0.0, false, 1000.0},
		{"no speech with negative fallback", -0.05, 0.0, false, -0.05},
		{"both zero with speech", 0.0, 0.0, true, 0.0},
		{"both zero without speech", 0.0, 0.0, false, 0.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := preferSpeechMetricSigned(tt.fullFile, tt.speechValue, tt.hasSpeech)
			if got != tt.want {
				t.Errorf("preferSpeechMetricSigned(%v, %v, %v) = %v, want %v",
					tt.fullFile, tt.speechValue, tt.hasSpeech, got, tt.want)
			}
		})
	}
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
	t.Run("valid typed config passes through unchanged", func(t *testing.T) {
		config := EffectiveFilterConfig{
			DS201HighPass: DS201HighPassConfig{Frequency: 100.0, Width: 0.5, Mix: 0.8},
			DS201LowPass:  DS201LowPassConfig{Frequency: 14000.0, Width: 0.7, Mix: 0.9},
			NoiseRemove: NoiseRemoveConfig{
				Strength:             0.00001,
				PatchSec:             0.006,
				ResearchSec:          0.0058,
				Smooth:               11.0,
				AfftdnNoiseReduction: 12.0,
			},
			DS201Gate: DS201GateConfig{
				Threshold: 0.02,
				Ratio:     2.0,
				Attack:    12,
				Release:   250,
				Range:     0.0625,
				Knee:      3.0,
				Makeup:    1.0,
			},
			LA2A:    LA2AConfig{Threshold: -24.0, Ratio: 3.0, Attack: 10, Release: 200, Makeup: 0, Knee: 4.0, Mix: 1.0},
			Deesser: DeesserConfig{Intensity: 0.3, Amount: 0.5, Frequency: 0.5},
		}
		want := config

		sanitizeConfig(&config)

		if !reflect.DeepEqual(config, want) {
			t.Errorf("sanitizeConfig changed valid typed config:\ngot  %+v\nwant %+v", config, want)
		}
	})

	t.Run("typed family non-finite values get defaults", func(t *testing.T) {
		config := EffectiveFilterConfig{
			DS201HighPass: DS201HighPassConfig{Frequency: math.NaN(), Width: math.Inf(1), Mix: math.Inf(-1)},
			DS201LowPass:  DS201LowPassConfig{Frequency: math.Inf(1), Width: math.NaN(), Mix: math.Inf(-1)},
			NoiseRemove: NoiseRemoveConfig{
				Strength:             math.NaN(),
				PatchSec:             math.Inf(1),
				ResearchSec:          math.Inf(-1),
				Smooth:               math.NaN(),
				AfftdnNoiseReduction: math.Inf(1),
			},
			DS201Gate: DS201GateConfig{
				Threshold: math.NaN(),
				Ratio:     math.Inf(1),
				Attack:    math.Inf(-1),
				Release:   math.NaN(),
				Range:     math.Inf(1),
				Knee:      math.Inf(-1),
				Makeup:    math.NaN(),
			},
			LA2A: LA2AConfig{
				Threshold: math.NaN(),
				Ratio:     math.Inf(1),
				Attack:    math.Inf(-1),
				Release:   math.NaN(),
				Makeup:    math.Inf(1),
				Knee:      math.Inf(-1),
				Mix:       math.NaN(),
			},
			Deesser: DeesserConfig{Intensity: math.NaN(), Amount: math.Inf(1), Frequency: math.Inf(-1)},
		}

		sanitizeConfig(&config)

		if config.DS201HighPass.Frequency != ds201DefaultHPFreq || config.DS201HighPass.Width != 0.707 || config.DS201HighPass.Mix != 1.0 {
			t.Errorf("DS201HighPass sanitised to %+v, want frequency %.1f width 0.707 mix 1.0", config.DS201HighPass, ds201DefaultHPFreq)
		}
		if config.DS201LowPass.Frequency != ds201LPBandLimitFreq || config.DS201LowPass.Width != 0.707 || config.DS201LowPass.Mix != 1.0 {
			t.Errorf("DS201LowPass sanitised to %+v, want frequency %.1f width 0.707 mix 1.0", config.DS201LowPass, ds201LPBandLimitFreq)
		}

		// sanitizeConfig only repairs the non-finite float fields (Strength,
		// PatchSec, ResearchSec, Smooth, AfftdnNoiseReduction); the boolean and
		// string afftdn fields keep the zero values from the input literal.
		defaultNoise := defaultNoiseRemoveConfig()
		defaultNoise.Enabled = false
		defaultNoise.AfftdnEnabled = false
		defaultNoise.AfftdnNoiseType = ""
		defaultNoise.AfftdnTrackNoise = false
		if config.NoiseRemove != defaultNoise {
			t.Errorf("NoiseRemove sanitised to %+v, want %+v", config.NoiseRemove, defaultNoise)
		}

		defaultGate := defaultDS201GateConfig()
		defaultGate.Enabled = false
		defaultGate.Detection = ""
		if config.DS201Gate != defaultGate {
			t.Errorf("DS201Gate sanitised to %+v, want %+v", config.DS201Gate, defaultGate)
		}

		defaultLA2A := defaultLA2AConfig()
		defaultLA2A.Enabled = false
		if config.LA2A != defaultLA2A {
			t.Errorf("LA2A sanitised to %+v, want %+v", config.LA2A, defaultLA2A)
		}

		defaultDeesser := defaultDeesserConfig()
		defaultDeesser.Enabled = false
		if config.Deesser != defaultDeesser {
			t.Errorf("Deesser sanitised to %+v, want %+v", config.Deesser, defaultDeesser)
		}
	})

	t.Run("gate threshold keeps existing zero and negative clamp behaviour", func(t *testing.T) {
		for _, threshold := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0.0, -0.5} {
			config := EffectiveFilterConfig{DS201Gate: DS201GateConfig{Threshold: threshold}}

			sanitizeConfig(&config)

			if config.DS201Gate.Threshold != ds201DefaultGateThreshold {
				t.Errorf("DS201Gate.Threshold for input %v = %v, want %v", threshold, config.DS201Gate.Threshold, ds201DefaultGateThreshold)
			}
		}
	})

	t.Run("zero values for non-gate typed fields pass through", func(t *testing.T) {
		config := EffectiveFilterConfig{
			DS201HighPass: DS201HighPassConfig{Frequency: 0.0, Width: 0.0, Mix: 0.0},
			Deesser:       DeesserConfig{Intensity: 0.0},
			LA2A:          LA2AConfig{Ratio: 0.0, Threshold: 0.0},
			DS201Gate:     DS201GateConfig{Threshold: 1e-10},
		}

		sanitizeConfig(&config)

		if config.DS201HighPass.Frequency != 0.0 || config.DS201HighPass.Width != 0.0 || config.DS201HighPass.Mix != 0.0 {
			t.Errorf("DS201HighPass zero values changed: %+v", config.DS201HighPass)
		}
		if config.Deesser.Intensity != 0.0 {
			t.Errorf("Deesser.Intensity = %v, want 0.0", config.Deesser.Intensity)
		}
		if config.LA2A.Ratio != 0.0 || config.LA2A.Threshold != 0.0 {
			t.Errorf("LA2A zero values changed: %+v", config.LA2A)
		}
		if config.DS201Gate.Threshold != 1e-10 {
			t.Errorf("DS201Gate.Threshold = %v, want 1e-10", config.DS201Gate.Threshold)
		}
	})

	t.Run("negative LA2A threshold passes through", func(t *testing.T) {
		config := EffectiveFilterConfig{
			LA2A:      LA2AConfig{Threshold: -40.0, Ratio: 3.0},
			DS201Gate: DS201GateConfig{Threshold: 0.02},
		}

		sanitizeConfig(&config)

		if config.LA2A.Threshold != -40.0 {
			t.Errorf("LA2A.Threshold = %v, want -40.0", config.LA2A.Threshold)
		}
	})
}

func TestTuneLA2AThresholdAcceptsZeroDBPeak(t *testing.T) {
	config := newTestConfig()
	measurements := &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{
			DynamicRange: 35.0,
			PeakLevel:    0.0,
		},
		InputTP: -2.0,
	}

	tuneLA2AThreshold(config, measurements, la2aOverrides{})

	if math.Abs(config.LA2A.Threshold-(-20.0)) > 0.001 {
		t.Errorf("LA2A.Threshold = %.3f, want -20.000", config.LA2A.Threshold)
	}
}

func TestTuneLA2AThresholdFallsBackForInvalidPeak(t *testing.T) {
	config := newTestConfig()
	measurements := &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{
			DynamicRange: 35.0,
			PeakLevel:    math.NaN(),
		},
		InputTP: -2.0,
	}

	tuneLA2AThreshold(config, measurements, la2aOverrides{})

	if math.Abs(config.LA2A.Threshold-defaultLA2AThreshold) > 0.001 {
		t.Errorf("LA2A.Threshold = %.3f, want %.3f", config.LA2A.Threshold, defaultLA2AThreshold)
	}
}

func TestClamp(t *testing.T) {
	// Tests for the min/max builtin clamp pattern
	// max(lo, min(val, hi)) returns val constrained to [lo, hi]

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
			min:  0.0, // minDeesser.Intensity
			max:  0.6, // maxDeesser.Intensity
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
			got := max(tt.min, min(tt.val, tt.max))
			if got != tt.want {
				t.Errorf("max(%v, min(%v, %v)) = %v, want %v",
					tt.min, tt.val, tt.max, got, tt.want)
			}
		})
	}
}

func TestApplyHighCrestOverrides(t *testing.T) {
	// Helper for float comparison within tolerance
	assertClose := func(t *testing.T, name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.4f, want %.4f ±%.4f", name, got, want, tol)
		}
	}

	t.Run("deficit and severity calculation", func(t *testing.T) {
		tests := []struct {
			name            string
			inputI          float64
			inputTP         float64
			speechProfile   *SpeechCandidateMetrics
			wantActive      bool
			wantDeficit     float64
			wantSeverity    float64
			wantProjectedTP float64
		}{
			{
				name:            "Anna-like: quiet input, high crest",
				inputI:          -39.1,
				inputTP:         -4.9,
				speechProfile:   &SpeechCandidateMetrics{},
				wantActive:      true,
				wantDeficit:     2.6,
				wantSeverity:    0.433,
				wantProjectedTP: 18.2,
			},
			{
				name:            "Marius-like: normal input, no deficit",
				inputI:          -20.2,
				inputTP:         -2.5,
				speechProfile:   &SpeechCandidateMetrics{},
				wantActive:      false,
				wantDeficit:     -16.3,
				wantSeverity:    0.0,
				wantProjectedTP: 1.7,
			},
			{
				name:            "Patrick-like: moderate input, no deficit",
				inputI:          -26.8,
				inputTP:         -6.4,
				speechProfile:   &SpeechCandidateMetrics{},
				wantActive:      false,
				wantDeficit:     -9.7,
				wantSeverity:    0.0,
				wantProjectedTP: 4.4,
			},
			{
				name:            "deficit exactly zero: boundary, no override",
				inputI:          -36.5,
				inputTP:         -5.0,
				speechProfile:   &SpeechCandidateMetrics{},
				wantActive:      false,
				wantDeficit:     0.0,
				wantSeverity:    0.0,
				wantProjectedTP: 15.5,
			},
			{
				name:            "deficit 8: severity clamped (beyond maxDeficit 6)",
				inputI:          -44.5,
				inputTP:         -5.0,
				speechProfile:   &SpeechCandidateMetrics{},
				wantActive:      true,
				wantDeficit:     8.0,
				wantSeverity:    1.0,
				wantProjectedTP: 23.5,
			},
			{
				name:            "deficit beyond 6: severity clamped to 1.0",
				inputI:          -50.0,
				inputTP:         -3.0,
				speechProfile:   &SpeechCandidateMetrics{},
				wantActive:      true,
				wantDeficit:     13.5,
				wantSeverity:    1.0,
				wantProjectedTP: 31.0,
			},
			{
				name:            "SpeechProfile nil: deficit still computed",
				inputI:          -39.1,
				inputTP:         -4.9,
				speechProfile:   nil,
				wantActive:      true,
				wantDeficit:     2.6,
				wantSeverity:    0.433,
				wantProjectedTP: 18.2,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				// Use DefaultFilterConfig target TP (-2.0) to match proposal calculations
				config.Loudnorm.TargetTP = -2.0
				diagnostics := &AdaptiveDiagnostics{}
				measurements := &AudioMeasurements{
					InputI:        tt.inputI,
					InputTP:       tt.inputTP,
					SpeechProfile: tt.speechProfile,
				}

				overrides := applyHighCrestOverrides(config, diagnostics, measurements, nil)

				if diagnostics.LA2AHighCrestActive != tt.wantActive {
					t.Errorf("LA2AHighCrestActive = %v, want %v", diagnostics.LA2AHighCrestActive, tt.wantActive)
				}
				assertClose(t, "LA2AHighCrestDeficit", diagnostics.LA2AHighCrestDeficit, tt.wantDeficit, 0.01)
				assertClose(t, "LA2AHighCrestSeverity", diagnostics.LA2AHighCrestSeverity, tt.wantSeverity, 0.001)
				assertClose(t, "LA2AHighCrestProjectedTP", diagnostics.LA2AHighCrestProjectedTP, tt.wantProjectedTP, 0.01)
				assertNoStaleEffectiveConfigFields(t)

				// When inactive, overrides must be zero-valued
				if !tt.wantActive {
					if overrides.ThresholdFloor != 0 || overrides.RatioFloor != 0 ||
						overrides.ReleaseFloor != 0 || overrides.KneeFloor != 0 {
						t.Errorf("inactive case returned non-zero overrides: %+v", overrides)
					}
				}
			})
		}
	})

	t.Run("override floor values", func(t *testing.T) {
		tests := []struct {
			name               string
			inputI             float64
			inputTP            float64
			wantThresholdFloor float64
			wantRatioFloor     float64
			wantReleaseFloor   float64
			wantKneeFloor      float64
		}{
			{
				name:               "Anna-like: severity ~0.433, moderate overrides",
				inputI:             -39.1,
				inputTP:            -4.9,
				wantThresholdFloor: -27.53, // lerp(-18, -40, 0.433)
				wantRatioFloor:     3.87,   // lerp(3, 5, 0.433)
				wantReleaseFloor:   265.0,  // lerp(200, 350, 0.433)
				wantKneeFloor:      4.87,   // lerp(4, 6, 0.433)
			},
			{
				name:               "maximum severity: full override values",
				inputI:             -44.5,
				inputTP:            -5.0,
				wantThresholdFloor: -40.0,
				wantRatioFloor:     5.0,
				wantReleaseFloor:   350.0,
				wantKneeFloor:      6.0,
			},
			{
				name:               "deficit 4.0: severity 0.667 (below maxDeficit 6.0)",
				inputI:             -40.5, // gainRequired=24.5, idealCeiling=-2-24.5-1.5=-28, deficit=4.0
				inputTP:            -5.0,
				wantThresholdFloor: -32.67, // lerp(-18, -40, 0.667)
				wantRatioFloor:     4.33,   // lerp(3, 5, 0.667)
				wantReleaseFloor:   300.0,  // lerp(200, 350, 0.667)
				wantKneeFloor:      5.33,   // lerp(4, 6, 0.667)
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				config.Loudnorm.TargetTP = -2.0
				diagnostics := &AdaptiveDiagnostics{}
				measurements := &AudioMeasurements{
					InputI:        tt.inputI,
					InputTP:       tt.inputTP,
					SpeechProfile: &SpeechCandidateMetrics{},
				}

				overrides := applyHighCrestOverrides(config, diagnostics, measurements, nil)

				assertClose(t, "ThresholdFloor", overrides.ThresholdFloor, tt.wantThresholdFloor, 0.01)
				assertClose(t, "RatioFloor", overrides.RatioFloor, tt.wantRatioFloor, 0.01)
				assertClose(t, "ReleaseFloor", overrides.ReleaseFloor, tt.wantReleaseFloor, 0.01)
				assertClose(t, "KneeFloor", overrides.KneeFloor, tt.wantKneeFloor, 0.01)
				assertNoStaleEffectiveConfigFields(t)
			})
		}
	})

	t.Run("severity scales linearly with deficit", func(t *testing.T) {
		// Acceptance criterion 5: severity is linear from 0 to 1 over deficit 0 to 6
		// With Loudnorm.TargetTP=-2.0, deficit=0 when gainRequired=20.5, i.e. InputI=-36.5
		// Each 1.0 dB decrease in InputI adds 1.0 dB to deficit
		deficits := []struct {
			inputI       float64
			wantDeficit  float64
			wantSeverity float64
		}{
			{-36.5, 0.0, 0.0},
			{-37.5, 1.0, 0.167},
			{-38.5, 2.0, 0.333},
			{-39.5, 3.0, 0.5},
			{-40.5, 4.0, 0.667},
			{-42.5, 6.0, 1.0},
			{-44.5, 8.0, 1.0},  // clamped at 1.0
			{-46.5, 10.0, 1.0}, // clamped at 1.0
		}

		for _, tt := range deficits {
			config := newTestConfig()
			config.Loudnorm.TargetTP = -2.0
			diagnostics := &AdaptiveDiagnostics{}
			measurements := &AudioMeasurements{
				InputI:        tt.inputI,
				InputTP:       -5.0,
				SpeechProfile: &SpeechCandidateMetrics{},
			}

			applyHighCrestOverrides(config, diagnostics, measurements, nil)

			assertClose(t, "deficit", diagnostics.LA2AHighCrestDeficit, tt.wantDeficit, 0.01)
			assertClose(t, "severity", diagnostics.LA2AHighCrestSeverity, tt.wantSeverity, 0.001)
			assertNoStaleEffectiveConfigFields(t)
		}
	})

	t.Run("diagnostic fields populated for all recordings", func(t *testing.T) {
		// Acceptance criterion 6: fields populated even when inactive
		config := newTestConfig()
		config.Loudnorm.TargetTP = -2.0
		diagnostics := &AdaptiveDiagnostics{}
		measurements := &AudioMeasurements{
			InputI:        -20.2,
			InputTP:       -2.5,
			SpeechProfile: &SpeechCandidateMetrics{},
		}

		applyHighCrestOverrides(config, diagnostics, measurements, nil)

		if diagnostics.LA2AHighCrestActive {
			t.Error("expected LA2AHighCrestActive=false for Marius-like input")
		}
		if diagnostics.LA2AHighCrestDeficit == 0 && diagnostics.LA2AHighCrestProjectedTP == 0 {
			t.Error("diagnostic fields should be populated even when inactive")
		}
		assertClose(t, "deficit", diagnostics.LA2AHighCrestDeficit, -16.3, 0.01)
		assertClose(t, "projectedTP", diagnostics.LA2AHighCrestProjectedTP, 1.7, 0.01)
		assertNoStaleEffectiveConfigFields(t)
	})
}

func TestTuneLA2ACompressorHighCrest(t *testing.T) {
	// Helper for float comparison within tolerance
	assertClose := func(t *testing.T, name string, got, want, tol float64) {
		t.Helper()
		if math.Abs(got-want) > tol {
			t.Errorf("%s = %.4f, want %.4f ±%.4f", name, got, want, tol)
		}
	}

	// Realistic speech profile for sub-tuners to produce meaningful values.
	// Values drawn from the elected speech candidates in the analysis docs.
	speechProfile := &SpeechCandidateMetrics{
		Spectral: SpectralMetrics{
			Kurtosis: 19.0,
			Flux:     0.0003,
			Centroid: 2347,
			Skewness: 1.50,
		},
		CrestFactor: 34.3,
	}

	t.Run("Anna-like: high-crest overrides push ratio and threshold", func(t *testing.T) {
		// Anna-like: InputI=-39.1, InputTP=-4.9 -> deficit ~2.6, severity ~0.433
		// Acceptance criterion 1: ratio pushed toward ~3.87, threshold toward ~-27.5
		config := newTestConfig()
		config.Loudnorm.TargetTP = -2.0
		diagnostics := &AdaptiveDiagnostics{}
		measurements := &AudioMeasurements{
			BaseMeasurements: BaseMeasurements{
				Spectral: SpectralMetrics{Kurtosis: 5.4, Flux: 0.0003, Centroid: 968, Skewness: 1.50}, DynamicRange: 86.3,

				PeakLevel: -10.1,
			},
			InputI:        -39.1,
			InputTP:       -4.9,
			InputLRA:      16.0,
			NoiseFloor:    -63.7,
			SpeechProfile: speechProfile,
		}

		tuneLA2ACompressor(config, diagnostics, measurements, nil)

		// Diagnostic fields must be set
		if !diagnostics.LA2AHighCrestActive {
			t.Fatal("expected LA2AHighCrestActive=true for Anna-like input")
		}
		assertClose(t, "deficit", diagnostics.LA2AHighCrestDeficit, 2.6, 0.1)
		assertClose(t, "severity", diagnostics.LA2AHighCrestSeverity, 0.433, 0.01)
		assertNoStaleEffectiveConfigFields(t)

		// Override floors: ratio >= 3.8, threshold <= -27.0
		// Sub-tuners may push further, but not back above the floor.
		if config.LA2A.Ratio < 3.8 {
			t.Errorf("LA2A.Ratio = %.2f, want >= 3.8 (override floor ~3.87)", config.LA2A.Ratio)
		}
		if config.LA2A.Threshold > -27.0 {
			t.Errorf("LA2A.Threshold = %.2f, want <= -27.0 (override floor ~-27.53)", config.LA2A.Threshold)
		}
		if config.LA2A.Release < 264.0 {
			t.Errorf("LA2A.Release = %.2f, want >= 264.0 (override floor ~265.0)", config.LA2A.Release)
		}
		if config.LA2A.Knee < 4.8 {
			t.Errorf("LA2A.Knee = %.2f, want >= 4.8 (override floor ~4.87)", config.LA2A.Knee)
		}

		// Ratio must respect existing 5.0 ceiling (acceptance criterion 4)
		if config.LA2A.Ratio > 5.0 {
			t.Errorf("LA2A.Ratio = %.2f, must not exceed 5.0 ceiling", config.LA2A.Ratio)
		}
	})

	t.Run("Marius-like: no deficit, sub-tuners unmodified", func(t *testing.T) {
		// Marius-like: InputI=-20.2, InputTP=-2.5 -> deficit <= 0, no overrides
		// Acceptance criterion 2: identical output to before high-crest logic
		config := newTestConfig()
		config.Loudnorm.TargetTP = -2.0
		diagnostics := &AdaptiveDiagnostics{}
		measurements := &AudioMeasurements{
			BaseMeasurements: BaseMeasurements{
				Spectral: SpectralMetrics{Kurtosis: 12.7, Flux: 0.0018, Centroid: 1555, Skewness: 2.32}, DynamicRange: 88.9,

				PeakLevel: -7.4,
			},
			InputI:        -20.2,
			InputTP:       -2.5,
			InputLRA:      11.9,
			NoiseFloor:    -83.3,
			SpeechProfile: speechProfile,
		}

		// Run once with the full tuner chain
		tuneLA2ACompressor(config, diagnostics, measurements, nil)

		if diagnostics.LA2AHighCrestActive {
			t.Error("expected LA2AHighCrestActive=false for Marius-like input")
		}
		assertNoStaleEffectiveConfigFields(t)

		// Capture the values
		ratioWithOverrides := config.LA2A.Ratio
		thresholdWithOverrides := config.LA2A.Threshold
		releaseWithOverrides := config.LA2A.Release
		kneeWithOverrides := config.LA2A.Knee

		// Run again on a fresh config to compare (both should be identical
		// since overrides are zero-valued)
		config2 := newTestConfig()
		config2.Loudnorm.TargetTP = -2.0
		diagnostics2 := &AdaptiveDiagnostics{}
		tuneLA2ACompressor(config2, diagnostics2, measurements, nil)
		assertNoStaleEffectiveConfigFields(t)

		assertClose(t, "ratio", ratioWithOverrides, config2.LA2A.Ratio, 0.001)
		assertClose(t, "threshold", thresholdWithOverrides, config2.LA2A.Threshold, 0.001)
		assertClose(t, "release", releaseWithOverrides, config2.LA2A.Release, 0.001)
		assertClose(t, "knee", kneeWithOverrides, config2.LA2A.Knee, 0.001)
	})

	t.Run("hot-input and deficit cannot co-occur", func(t *testing.T) {
		// Hot-input protection fires when InputTP >= -1.0 dBTP.
		// Deficit > 0 requires very low InputI (well below -36.5 LUFS).
		// A recording quiet enough for deficit > 0 cannot have InputTP >= -1.0.
		// Verify this invariant: any input with InputTP >= -1.0 has deficit <= 0.
		hotInputCases := []struct {
			name    string
			inputI  float64
			inputTP float64
		}{
			{"loud recording, hot peaks", -16.0, -0.5},
			{"moderately loud, hot peaks", -20.0, -1.0},
			{"slightly quiet, hot peaks", -25.0, 0.0},
		}

		for _, tt := range hotInputCases {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				config.Loudnorm.TargetTP = -2.0
				diagnostics := &AdaptiveDiagnostics{}
				measurements := &AudioMeasurements{
					BaseMeasurements: BaseMeasurements{
						Spectral: SpectralMetrics{Kurtosis: 10.0, Flux: 0.001, Centroid: 2500, Skewness: 1.0}, DynamicRange: 60.0,

						PeakLevel: tt.inputTP,
					},
					InputI:        tt.inputI,
					InputTP:       tt.inputTP,
					InputLRA:      8.0,
					NoiseFloor:    -60.0,
					SpeechProfile: speechProfile,
				}

				tuneLA2ACompressor(config, diagnostics, measurements, nil)

				if diagnostics.LA2AHighCrestActive {
					t.Errorf("deficit > 0 (%.2f) with hot InputTP (%.1f dBTP) - invariant violated",
						diagnostics.LA2AHighCrestDeficit, tt.inputTP)
				}
				assertNoStaleEffectiveConfigFields(t)
			})
		}
	})

	t.Run("maximum severity produces aggressive but bounded parameters", func(t *testing.T) {
		// Deficit >= 4.0 -> severity 1.0 -> maximum override floors
		config := newTestConfig()
		config.Loudnorm.TargetTP = -2.0
		diagnostics := &AdaptiveDiagnostics{}
		measurements := &AudioMeasurements{
			BaseMeasurements: BaseMeasurements{
				Spectral: SpectralMetrics{Kurtosis: 5.0, Flux: 0.0003, Centroid: 2000, Skewness: 1.0}, DynamicRange: 90.0,

				PeakLevel: -5.0,
			},
			InputI:        -44.5,
			InputTP:       -5.0,
			InputLRA:      20.0,
			NoiseFloor:    -70.0,
			SpeechProfile: speechProfile,
		}

		tuneLA2ACompressor(config, diagnostics, measurements, nil)

		if !diagnostics.LA2AHighCrestActive {
			t.Fatal("expected LA2AHighCrestActive=true for maximum severity input")
		}
		assertClose(t, "severity", diagnostics.LA2AHighCrestSeverity, 1.0, 0.001)
		assertNoStaleEffectiveConfigFields(t)

		// At severity 1.0, floors are: threshold=-40, ratio=5.0, release=350, knee=6.0
		// Sub-tuners may push further but not below the floor.
		if config.LA2A.Ratio < 5.0 {
			t.Errorf("LA2A.Ratio = %.2f, want >= 5.0 at maximum severity", config.LA2A.Ratio)
		}
		if config.LA2A.Ratio > 5.0 {
			t.Errorf("LA2A.Ratio = %.2f, must not exceed 5.0 ceiling", config.LA2A.Ratio)
		}
		if config.LA2A.Threshold > -40.0 {
			t.Errorf("LA2A.Threshold = %.2f, want <= -40.0 at maximum severity", config.LA2A.Threshold)
		}
		if config.LA2A.Release < 350.0 {
			t.Errorf("LA2A.Release = %.2f, want >= 350.0 at maximum severity", config.LA2A.Release)
		}
		if config.LA2A.Knee < 6.0 {
			t.Errorf("LA2A.Knee = %.2f, want >= 6.0 at maximum severity", config.LA2A.Knee)
		}
	})
}
