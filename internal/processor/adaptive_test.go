package processor

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestAdaptConfigReturnsEffectiveConfig(t *testing.T) {
	measurements := &AudioMeasurements{
		Spectral: SpectralMetrics{Centroid: 5000, Decrease: -0.12, Skewness: 1.2, Kurtosis: 4.0, Flux: 0.01},
		Dynamics: DynamicsMetrics{
			DynamicRange: 32.0,
			PeakLevel:    -8.0,
		},
		Loudness: InputLoudnessMetrics{
			InputI:   -28.0,
			InputTP:  -4.0,
			InputLRA: 9.0,
		},
		Noise: NoiseMetrics{Floor: -60.0},
		Regions: RegionMetrics{
			NoiseProfile: &NoiseProfile{
				MeasuredNoiseFloor: -50.0,
				Entropy:            0.8,
			},
		},
	}

	base := newTestBaseConfig()
	base.FilterOrder = []FilterID{FilterDeesser, FilterAnalysis}
	base.RumbleHighPass.Enabled = true
	base.RumbleHighPass.Frequency = 95.0
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
	if base.RumbleHighPass.Frequency != 95.0 {
		t.Errorf("base RumbleHighPass.Frequency = %.1f, want unchanged 95.0", base.RumbleHighPass.Frequency)
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
	// The rumble high-pass is fixed and non-adaptive: the effective config carries
	// the seed frequency through unchanged (no tuning step overwrites it).
	if effective.RumbleHighPass.Frequency != 95.0 {
		t.Errorf("effective RumbleHighPass.Frequency = %.1f, want seed 95.0 passed through unchanged", effective.RumbleHighPass.Frequency)
	}
	if diagnostics.BandlimitLPReason != "20.5 kHz band-limit (always on)" {
		t.Errorf("diagnostics BandlimitLPReason = %q, want 20.5 kHz band-limit (always on)", diagnostics.BandlimitLPReason)
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
			want: "highpass=f=80:poles=2:width_type=q:width=0.707:normalize=1:a=tdii," +
				"lowpass=f=20500:poles=2:width_type=q:width=0.707:normalize=1," +
				"anlmdn=s=0.00001:p=0.0060:r=0.0058:m=11," +
				"afftdn=nr=12:nt=w:tn=0:nf=-58," +
				"agate=threshold=0.019953:ratio=2.0:attack=5.00:release=200:range=0.1995:knee=3.0:detection=rms:makeup=1.0," +
				"acompressor=threshold=0.031623:ratio=3.0:attack=10:release=200:makeup=1.00:knee=4.0:detection=rms:mix=1.00",
		},
		{
			name:         "bright speech with noise profile",
			measurements: orderIndependenceBrightSpeechMeasurements(),
			want: "highpass=f=80:poles=2:width_type=q:width=0.707:normalize=1:a=tdii," +
				"lowpass=f=20500:poles=2:width_type=q:width=0.707:normalize=1," +
				"anlmdn=s=0.00001:p=0.0060:r=0.0058:m=11," +
				"afftdn=nr=12:nt=w:tn=0:nf=-60," +
				"agate=threshold=0.010000:ratio=2.0:attack=5.00:release=200:range=0.1995:knee=3.0:detection=rms:makeup=1.0," +
				"acompressor=threshold=0.177828:ratio=3.0:attack=10:release=200:makeup=1.00:knee=4.0:detection=rms:mix=1.00",
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
	config.RumbleHighPass.Enabled = true
	config.BandlimitLowPass.Enabled = true
	config.NoiseReduction.Enabled = true
	config.SpeechGate.Enabled = true
	config.LevellingCompressor.Enabled = true
	config.Loudnorm.TargetTP = -2.0
	return config
}

func orderIndependenceWarmNoProfileMeasurements() *AudioMeasurements {
	return &AudioMeasurements{
		Spectral: SpectralMetrics{Centroid: 6500, Decrease: -0.12, Skewness: 1.6, Kurtosis: 4.0, Flatness: 0.62, Flux: 0.008, Crest: 20.0, Rolloff: 18000},
		Dynamics: DynamicsMetrics{
			DynamicRange: 90.0,
			PeakLevel:    -10.0,
		},
		Loudness: InputLoudnessMetrics{
			InputI:   -42.1,
			InputTP:  -4.9,
			InputLRA: 6.0,
		},
		Noise: NoiseMetrics{Floor: -58.0},
	}
}

func orderIndependenceBrightSpeechMeasurements() *AudioMeasurements {
	return &AudioMeasurements{
		Spectral: SpectralMetrics{Centroid: 5000, Decrease: 0.0, Skewness: 0.0, Kurtosis: 9.0, Flatness: 0.38, Flux: 0.002, Crest: 45.0, Rolloff: 15000},
		Dynamics: DynamicsMetrics{
			DynamicRange:      32.0,
			PeakLevel:         -6.0,
			RMSLevel:          -30.0, // full-file RMS below speech RMS, so the threshold floor stays inert
			ZeroCrossingsRate: 0.05,
		},
		Loudness: InputLoudnessMetrics{
			InputI:   -20.0,
			InputTP:  -2.5,
			InputLRA: 12.0,
		},
		Noise: NoiseMetrics{Floor: -60.0},
		Regions: RegionMetrics{
			NoiseProfile: &NoiseProfile{
				MeasuredNoiseFloor: -60.0,
				PeakLevel:          -45.0,
				CrestFactor:        15.0,
				Entropy:            0.8,
			},
			// Wide voiced gap (21 dB): voiced p10 -34, noise p95 -55. The gate
			// threshold lands at voiced p10 minus the 6 dB speech margin (-40 dB).
			VoicedLowPercentile: -34.0,
			NoiseHighPercentile: -55.0,
			GateSeparationDB:    21.0,
			SpeechProfile: &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: -24.0, CrestFactor: 12.0, Spectral: SpectralMetrics{
				Centroid: 5000,
				Decrease: 0.0,
				Skewness: 0.0,
				Kurtosis: 9.0,
				Flux:     0.002,
				Rolloff:  15000,
			}}},
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
		{"RumbleHighPass.Frequency", got.RumbleHighPass.Frequency, want.RumbleHighPass.Frequency},
		{"RumbleHighPass.Poles", got.RumbleHighPass.Poles, want.RumbleHighPass.Poles},
		{"RumbleHighPass.Width", got.RumbleHighPass.Width, want.RumbleHighPass.Width},
		{"RumbleHighPass.Mix", got.RumbleHighPass.Mix, want.RumbleHighPass.Mix},
		{"RumbleHighPass.Transform", got.RumbleHighPass.Transform, want.RumbleHighPass.Transform},
		{"NoiseReduction.AfftdnEnabled", got.NoiseReduction.AfftdnEnabled, want.NoiseReduction.AfftdnEnabled},
		{"NoiseReduction.AfftdnNoiseReduction", got.NoiseReduction.AfftdnNoiseReduction, want.NoiseReduction.AfftdnNoiseReduction},
		{"BandlimitLowPass.Enabled", got.BandlimitLowPass.Enabled, want.BandlimitLowPass.Enabled},
		{"BandlimitLowPass.Frequency", got.BandlimitLowPass.Frequency, want.BandlimitLowPass.Frequency},
		{"LevellingCompressor.Threshold", got.LevellingCompressor.Threshold, want.LevellingCompressor.Threshold},
		{"LevellingCompressor.Ratio", got.LevellingCompressor.Ratio, want.LevellingCompressor.Ratio},
		{"LevellingCompressor.Release", got.LevellingCompressor.Release, want.LevellingCompressor.Release},
		{"LevellingCompressor.Knee", got.LevellingCompressor.Knee, want.LevellingCompressor.Knee},
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
		{"BandlimitLPReason", got.BandlimitLPReason, want.BandlimitLPReason},
		{"SpeechGateDepthDB", got.SpeechGateDepthDB, want.SpeechGateDepthDB},
		{"SpeechGateDynamicRange", got.SpeechGateDynamicRange, want.SpeechGateDynamicRange},
		{"SpeechGateQuietSpeechEstimate", got.SpeechGateQuietSpeechEstimate, want.SpeechGateQuietSpeechEstimate},
		{"SpeechGateSpeechSeparation", got.SpeechGateSpeechSeparation, want.SpeechGateSpeechSeparation},
		{"SpeechGateSpeechHeadroom", got.SpeechGateSpeechHeadroom, want.SpeechGateSpeechHeadroom},
		{"SpeechGateThresholdUnclamped", got.SpeechGateThresholdUnclamped, want.SpeechGateThresholdUnclamped},
		{"SpeechGateClampReason", got.SpeechGateClampReason, want.SpeechGateClampReason},
	}

	for _, tt := range tests {
		if !reflect.DeepEqual(tt.got, tt.want) {
			t.Errorf("%s = %v, want %v", tt.name, tt.got, tt.want)
		}
	}
}

func TestTuneBandlimitLowPass(t *testing.T) {
	// The band-limit low-pass is an unconditional 20.5 kHz band-limit: always enabled
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
				Spectral: SpectralMetrics{Kurtosis: tt.kurtosis, Flatness: tt.flatness, Flux: tt.flux, Crest: tt.crest, Rolloff: tt.rolloff, Centroid: tt.centroid, Slope: tt.slope},
				Dynamics: DynamicsMetrics{ZeroCrossingsRate: tt.zcr},
			}

			tuneBandlimitLowPass(config, diagnostics, m)

			if !config.BandlimitLowPass.Enabled {
				t.Errorf("BandlimitLowPass.Enabled = false, want true [%s]", tt.desc)
			}
			if config.BandlimitLowPass.Frequency != bandlimitLPFreq {
				t.Errorf("BandlimitLowPass.Frequency = %.0f Hz, want %.0f Hz [%s]",
					config.BandlimitLowPass.Frequency, bandlimitLPFreq, tt.desc)
			}
			if config.BandlimitLowPass.Poles != 2 {
				t.Errorf("BandlimitLowPass.Poles = %d, want 2 [%s]", config.BandlimitLowPass.Poles, tt.desc)
			}
			if config.BandlimitLowPass.Mix != 1.0 {
				t.Errorf("BandlimitLowPass.Mix = %.2f, want 1.0 [%s]", config.BandlimitLowPass.Mix, tt.desc)
			}
			if diagnostics.BandlimitLPReason != "20.5 kHz band-limit (always on)" {
				t.Errorf("BandlimitLPReason = %q, want 20.5 kHz band-limit (always on) [%s]",
					diagnostics.BandlimitLPReason, tt.desc)
			}

			assertNoStaleEffectiveConfigFields(t)
		})
	}
}

func TestTuneDeesser(t *testing.T) {
	// New engagement model (adaptive_deesser.go):
	//   sibilanceExcess = SpeechProfile.SibBandRMS - SpeechProfile.BodyBandRMS  (dB)
	//   excess < -6           -> i = 0.0  (OFF)
	//   -6 .. -3              -> ramp i 0.0 -> 0.6
	//   -3 ..  0              -> ramp i 0.6 -> 0.85
	//   >  0                  -> i = 0.85 (cap)
	//
	// Breakpoints/endpoints: deessExcessOffDB=-6, deessExcessMidDB=-3,
	// deessExcessMaxDB=0, deessIntensityMid=0.6, deessIntensityMax=0.85.

	tests := []struct {
		name          string
		body          float64 // SpeechProfile.BodyBandRMS (dBFS)
		sib           float64 // SpeechProfile.SibBandRMS (dBFS)
		hasProfile    bool
		bandsMeasured bool // SpeechProfile.BandsMeasured
		wantIntensity float64
		tolerance     float64
	}{
		// No speech profile -> guard keeps de-esser OFF regardless of bands.
		{
			name:          "no speech profile - OFF",
			hasProfile:    false,
			wantIntensity: 0.0,
			tolerance:     0.0,
		},
		// OFF segment: excess well below -6 dB (clean conversational voice).
		{
			name:          "clean voice, large body excess - OFF",
			body:          -20.0,
			sib:           -40.0, // excess -20 dB
			hasProfile:    true,
			bandsMeasured: true,
			wantIntensity: 0.0,
			tolerance:     0.0,
		},
		{
			name:          "boundary: exactly at OFF bar (-6)",
			body:          -20.0,
			sib:           -26.0, // excess -6 dB
			hasProfile:    true,
			bandsMeasured: true,
			wantIntensity: 0.0, // not < -6, frac=0 -> 0.0
			tolerance:     0.0,
		},
		// Lower ramp midpoint: excess -4.5 dB -> frac 0.5 -> 0.30.
		{
			name:          "lower ramp midpoint (-4.5)",
			body:          -20.0,
			sib:           -24.5, // excess -4.5 dB
			hasProfile:    true,
			bandsMeasured: true,
			wantIntensity: 0.30, // 0.5 * 0.6
			tolerance:     0.001,
		},
		// Mid breakpoint: excess -3 dB -> i = 0.6.
		{
			name:          "mid breakpoint (-3)",
			body:          -20.0,
			sib:           -23.0, // excess -3 dB
			hasProfile:    true,
			bandsMeasured: true,
			wantIntensity: 0.6,
			tolerance:     0.001,
		},
		// Upper ramp midpoint: excess -1.5 dB -> frac 0.5 -> 0.725.
		{
			name:          "upper ramp midpoint (-1.5)",
			body:          -20.0,
			sib:           -21.5, // excess -1.5 dB
			hasProfile:    true,
			bandsMeasured: true,
			wantIntensity: 0.725, // 0.6 + 0.5*(0.85-0.6)
			tolerance:     0.001,
		},
		// Cap bar: excess exactly 0 -> cap 0.85.
		{
			name:          "cap bar (0)",
			body:          -20.0,
			sib:           -20.0, // excess 0 dB
			hasProfile:    true,
			bandsMeasured: true,
			wantIntensity: 0.85,
			tolerance:     0.001,
		},
		// Above cap bar: excess positive -> cap 0.85.
		{
			name:          "above cap (sibilant rivals body)",
			body:          -20.0,
			sib:           -16.0, // excess +4 dB
			hasProfile:    true,
			bandsMeasured: true,
			wantIntensity: 0.85,
			tolerance:     0.001,
		},
		// Regression guard: profile present but bands not measured (both fields 0).
		// The unmeasured-bands guard must keep the de-esser OFF, NOT engage at the
		// 0.85 cap from a spurious 0 dB excess.
		{
			name:          "unmeasured bands (profile present, BandsMeasured false) -> OFF",
			body:          0.0,
			sib:           0.0,
			hasProfile:    true,
			bandsMeasured: false,
			wantIntensity: 0.0,
			tolerance:     0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestConfig()
			config.Deesser.Intensity = 0.0
			measurements := &AudioMeasurements{}
			if tt.hasProfile {
				measurements.Regions.SpeechProfile = &SpeechCandidateMetrics{
					BodyBandRMS:   tt.body,
					SibBandRMS:    tt.sib,
					BandsMeasured: tt.bandsMeasured,
				}
			}

			tuneDeesser(config, measurements)

			diff := config.Deesser.Intensity - tt.wantIntensity
			if diff < 0 {
				diff = -diff
			}
			if diff > tt.tolerance {
				t.Errorf("Deesser.Intensity = %.3f, want %.3f (+/-%.3f) [body=%.1f, sib=%.1f, excess=%.1f]",
					config.Deesser.Intensity, tt.wantIntensity, tt.tolerance, tt.body, tt.sib, tt.sib-tt.body)
			}
		})
	}
}

func TestTuneSpeechGate(t *testing.T) {
	// Tests the comprehensive gate tuning which calculates all gate parameters
	// based on measurements including NoiseProfile (extracted from the elected
	// room-tone region). The threshold subtests below have no SpeechProfile, so
	// they exercise the legacy noise-floor threshold path.
	//
	// Key constants from adaptive_speech_gate.go:
	// speechGateThresholdMinDB = -80.0 dB (minimum threshold)
	// speechGateThresholdMaxDB = -25.0 dB (never gate above this - would cut speech)
	// speechGateCrestFactorThreshold = 20.0 dB (when to use peak vs floor)
	// speechGateTargetReductionDB = 12.0 dB (target noise reduction)
	// speechGateTargetThresholdDB = -40.0 dB (target for clean recordings)
	// speechGateRatioGentle = 1.5 (wide LRA), speechGateRatioMod = 2.0 (cap, all else)
	//
	// Gap is derived from ratio: gap = targetReduction / (1 - 1/ratio)
	// - ratio 1.5 → gap = 12 / 0.333 = 36 dB
	// - ratio 2.0 → gap = 12 / 0.5 = 24 dB

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
				inputLRA:        8.0,  // Narrow LRA → ratio 2.0 (cap) → gap 24dB → -75+24=-51, but target -40 is higher
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
				inputLRA:        8.0, // Narrow LRA → ratio 2.0 (cap) → gap 24dB → -42+24=-18, clamped to -25
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
					Noise:    NoiseMetrics{Floor: tt.noiseFloor},
					Loudness: InputLoudnessMetrics{InputLRA: tt.inputLRA},
					Regions: RegionMetrics{
						NoiseProfile: &NoiseProfile{
							PeakLevel:   tt.roomTonePeak,
							CrestFactor: tt.roomToneCrest,
							Entropy:     0.5, // Moderate entropy
						},
					},
				}

				tuneSpeechGateForTest(config, measurements)

				actualDB := linearToDB(config.SpeechGate.Threshold)
				diff := actualDB - tt.wantThresholdDB
				if diff < 0 {
					diff = -diff
				}
				if diff > tt.tolerance {
					t.Errorf("SpeechGate.Threshold = %.1f dB, want %.1f dB ±%.1f [%s]",
						actualDB, tt.wantThresholdDB, tt.tolerance, tt.desc)
				}
			})
		}
	})

	t.Run("ratio based on LRA", func(t *testing.T) {
		// LRA threshold: gateLRAWide=15 LU
		// Ratios: gateRatioGentle=1.5 (wide LRA), gateRatioMod=2.0 (cap, all else)
		// The gate is a soft expander; ratio never exceeds 2.0:1.
		tests := []struct {
			name      string
			lra       float64
			wantRatio float64
			desc      string
		}{
			{"wide dynamics", 18.0, 1.5, "gentle ratio for expressive speech"},
			{"moderate dynamics", 12.0, 2.0, "capped ratio"},
			{"narrow dynamics", 6.0, 2.0, "capped at 2.0:1, never tighter"},
			{"at wide boundary", 15.0, 2.0, "boundary is exclusive, takes the cap"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					Noise:    NoiseMetrics{Floor: -55.0},
					Loudness: InputLoudnessMetrics{InputLRA: tt.lra},
				}

				tuneSpeechGateForTest(config, measurements)

				if config.SpeechGate.Ratio != tt.wantRatio {
					t.Errorf("SpeechGate.Ratio = %.1f, want %.1f [%s]", config.SpeechGate.Ratio, tt.wantRatio, tt.desc)
				}
			})
		}
	})

	t.Run("attack is fixed", func(t *testing.T) {
		// Attack collapsed to a fixed 5ms floor; transient/flux inputs no longer matter.
		tests := []struct {
			name         string
			maxDiff      float64
			spectralFlux float64
		}{
			{"sharp transients", 0.3, 1.0},
			{"gentle low flux", 0.05, 0.02},
			{"moderate flux", 0.15, 0.1},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					Spectral: SpectralMetrics{Flux: tt.spectralFlux},
					Dynamics: DynamicsMetrics{MaxDifference: tt.maxDiff},
					Noise:    NoiseMetrics{Floor: -55.0},
				}

				tuneSpeechGateForTest(config, measurements)

				if config.SpeechGate.Attack != speechGateAttackMS {
					t.Errorf("SpeechGate.Attack = %.1f ms, want fixed %.1f ms", config.SpeechGate.Attack, speechGateAttackMS)
				}
			})
		}
	})

	t.Run("detection is fixed rms", func(t *testing.T) {
		// Detection collapsed to fixed RMS; room-tone entropy/crest no longer matter.
		tests := []struct {
			name            string
			roomToneEntropy float64
			roomToneCrest   float64
		}{
			{"tonal noise", 0.2, 10.0},
			{"transient bleed", 0.5, 28.0},
			{"would-be-clean recording", 0.8, 8.0},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					Noise: NoiseMetrics{Floor: -55.0},
					Regions: RegionMetrics{
						NoiseProfile: &NoiseProfile{
							PeakLevel:   -55.0,
							CrestFactor: tt.roomToneCrest,
							Entropy:     tt.roomToneEntropy,
						},
					},
				}

				tuneSpeechGateForTest(config, measurements)

				if config.SpeechGate.Detection != "rms" {
					t.Errorf("SpeechGate.Detection = %q, want fixed \"rms\"", config.SpeechGate.Detection)
				}
			})
		}
	})

	t.Run("knee is fixed", func(t *testing.T) {
		// Knee collapsed to a single fixed value; spectral crest no longer matters
		// and there is no override (no gentle-mode override exists).
		tests := []struct {
			name  string
			crest float64
		}{
			{"high crest", 40.0},
			{"moderate crest", 25.0},
			{"low crest", 10.0},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					Spectral: SpectralMetrics{Crest: tt.crest},
					Noise:    NoiseMetrics{Floor: -55.0},
					Loudness: InputLoudnessMetrics{InputLRA: 15.0},
				}

				tuneSpeechGateForTest(config, measurements)

				if config.SpeechGate.Knee != speechGateKneeFixed {
					t.Errorf("SpeechGate.Knee = %.1f, want fixed %.1f", config.SpeechGate.Knee, speechGateKneeFixed)
				}
			})
		}
	})

	t.Run("range is fixed depth, reduced on narrow gap", func(t *testing.T) {
		// Range emits two fixed depths only. A wide gap takes the moderate fixed
		// depth (14 dB); a narrow gap takes the gentler fixed depth (8 dB). Neither
		// is ever 0 (full mute). The narrow-gap signal comes from the threshold
		// step: it fires when separation < speechMargin + noiseMargin (12 dB).
		tests := []struct {
			name        string
			separation  float64
			wantDepthDB float64
			desc        string
		}{
			{"wide gap - fixed moderate depth", 21.0, speechGateDepthFixedDB, "clear separation, full 14 dB depth"},
			{"narrow gap - reduced depth", 8.0, speechGateDepthNarrowDB, "narrow separation, gentler 8 dB depth"},
			{"boundary - just narrow", 11.9, speechGateDepthNarrowDB, "below the 12 dB narrow threshold"},
			{"boundary - just wide", 12.0, speechGateDepthFixedDB, "at the 12 dB threshold counts as wide"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				// VoicedLowPercentile placed so the noise high percentile follows the
				// separation under test; a SpeechProfile must be elected for the
				// voiced-anchored path that produces the narrow-gap signal.
				voicedLow := -34.0
				measurements := &AudioMeasurements{
					Regions: RegionMetrics{
						SpeechProfile:       &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: -20.0}},
						VoicedLowPercentile: voicedLow,
						NoiseHighPercentile: voicedLow - tt.separation,
						GateSeparationDB:    tt.separation,
					},
				}

				tuneSpeechGateForTest(config, measurements)

				actualDB := linearToDB(config.SpeechGate.Range)
				// Range is a negative dB attenuation, so the depth magnitude is its
				// absolute value.
				actualDepthDB := -actualDB
				diff := actualDepthDB - tt.wantDepthDB
				if diff < 0 {
					diff = -diff
				}
				if diff > 0.5 {
					t.Errorf("SpeechGate.Range = %.1f dB depth, want %.1f dB [%s]",
						actualDepthDB, tt.wantDepthDB, tt.desc)
				}
				if config.SpeechGate.Range <= 0 {
					t.Errorf("SpeechGate.Range = %.4f linear, must never be 0 (full mute) [%s]",
						config.SpeechGate.Range, tt.desc)
				}
			})
		}
	})

	t.Run("handles nil NoiseProfile gracefully", func(t *testing.T) {
		config := newTestConfig()
		measurements := &AudioMeasurements{
			Noise:    NoiseMetrics{Floor: -55.0},
			Loudness: InputLoudnessMetrics{InputLRA: 12.0},
			// NoiseProfile is nil
		}

		// Should not panic
		tuneSpeechGateForTest(config, measurements)

		// Should still calculate threshold from noise floor
		thresholdDB := linearToDB(config.SpeechGate.Threshold)
		if thresholdDB < -70 || thresholdDB > -25 {
			t.Errorf("SpeechGate.Threshold = %.1f dB, want within bounds [-70, -25]", thresholdDB)
		}

		// Detection should default to RMS when no profile
		if config.SpeechGate.Detection != "rms" {
			t.Errorf("SpeechGate.Detection = %q, want 'rms' (default for missing profile)", config.SpeechGate.Detection)
		}
	})

	t.Run("release is fixed regardless of flux, ZCR, and LRA", func(t *testing.T) {
		// Release is fixed at speechGateReleaseFixedMS (200 ms) with the hold folded
		// in. The former flux/ZCR sustain split and LRA extension are gone, so the
		// emitted release no longer varies with those inputs.
		tests := []struct {
			name string
			flux float64
			zcr  float64
			lra  float64
		}{
			{"sustained speech, wide LRA", 0.005, 0.05, 15.0},
			{"standard speech, wide LRA", 0.02, 0.20, 15.0},
			{"sustained speech, very low LRA", 0.005, 0.05, 7.0},
			{"standard speech, low LRA", 0.02, 0.20, 9.0},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					Spectral: SpectralMetrics{Flux: tt.flux},
					Dynamics: DynamicsMetrics{ZeroCrossingsRate: tt.zcr},
					Noise:    NoiseMetrics{Floor: -55.0},
					Loudness: InputLoudnessMetrics{InputLRA: tt.lra},
					Regions: RegionMetrics{
						NoiseProfile: &NoiseProfile{
							PeakLevel:   -50.0,
							CrestFactor: 15.0,
							Entropy:     0.005,
						},
					},
				}

				tuneSpeechGateForTest(config, measurements)

				if config.SpeechGate.Release != speechGateReleaseFixedMS {
					t.Errorf("SpeechGate.Release = %.1f ms, want %.1f ms (fixed)",
						config.SpeechGate.Release, speechGateReleaseFixedMS)
				}
			})
		}
	})

	t.Run("populates diagnostics from voiced statistics", func(t *testing.T) {
		// Voiced p10 -35, noise p95 -62, wide separation 27 dB. The threshold lands
		// at voiced p10 minus the 6 dB speech margin (-41 dB); the gap is wide so
		// the narrow-gap signal stays false.
		config := newTestConfig()
		diagnostics := tuneSpeechGateForTest(config, &AudioMeasurements{
			Spectral: SpectralMetrics{Flux: 0.02, Crest: 20.0},
			Loudness: InputLoudnessMetrics{InputI: -48.0, InputLRA: 6.0},
			Noise:    NoiseMetrics{Floor: -70.0},
			Regions: RegionMetrics{
				NoiseProfile: &NoiseProfile{
					PeakLevel:   -65.0,
					CrestFactor: 12.0,
					Entropy:     0.5,
				},
				VoicedLowPercentile: -35.0,
				NoiseHighPercentile: -62.0,
				GateSeparationDB:    27.0,
				SpeechProfile:       &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: -35.0, CrestFactor: 10.0}},
			},
		})

		if diagnostics.SpeechGateDepthDB != speechGateDepthFixedDB {
			t.Errorf("wide gap: SpeechGateDepthDB = %.1f, want fixed %.1f", diagnostics.SpeechGateDepthDB, speechGateDepthFixedDB)
		}
		if diagnostics.SpeechGateNarrowGap {
			t.Error("wide gap: SpeechGateNarrowGap = true, want false")
		}
		if diagnostics.SpeechGateQuietSpeechEstimate != -35.0 {
			t.Errorf("SpeechGateQuietSpeechEstimate = %.1f, want voiced p10 -35.0", diagnostics.SpeechGateQuietSpeechEstimate)
		}
		if diagnostics.SpeechGateSpeechSeparation != 27.0 {
			t.Errorf("SpeechGateSpeechSeparation = %.1f, want separation 27.0", diagnostics.SpeechGateSpeechSeparation)
		}
		if diagnostics.SpeechGateThresholdUnclamped != -35.0-speechGateThresholdSpeechMarginDB {
			t.Errorf("SpeechGateThresholdUnclamped = %.1f, want %.1f", diagnostics.SpeechGateThresholdUnclamped, -35.0-speechGateThresholdSpeechMarginDB)
		}
		if diagnostics.SpeechGateClampReason != "none" {
			t.Errorf("SpeechGateClampReason = %q, want \"none\" on a wide gap", diagnostics.SpeechGateClampReason)
		}
		if config.SpeechGate.Knee != speechGateKneeFixed {
			t.Errorf("SpeechGate.Knee = %.1f, want fixed %.1f (no gentle override)", config.SpeechGate.Knee, speechGateKneeFixed)
		}
		assertNoStaleEffectiveConfigFields(t)
	})

	t.Run("fresh diagnostics without speech metrics", func(t *testing.T) {
		// No SpeechProfile: the voiced-anchored diagnostics stay zero and the legacy
		// threshold path runs.
		config := newTestConfig()
		diagnostics := tuneSpeechGateForTest(config, &AudioMeasurements{
			Spectral: SpectralMetrics{Flux: 0.02},
			Loudness: InputLoudnessMetrics{InputI: -20.0, InputLRA: 16.0},
			Noise:    NoiseMetrics{Floor: -55.0},
		})

		if diagnostics.SpeechGateDepthDB != speechGateDepthFixedDB {
			t.Errorf("no profile: SpeechGateDepthDB = %.1f, want fixed %.1f", diagnostics.SpeechGateDepthDB, speechGateDepthFixedDB)
		}
		if diagnostics.SpeechGateNarrowGap {
			t.Error("diagnostics SpeechGateNarrowGap = true, want false without a profile")
		}
		if diagnostics.SpeechGateDynamicRange != 0 ||
			diagnostics.SpeechGateQuietSpeechEstimate != 0 ||
			diagnostics.SpeechGateSpeechSeparation != 0 ||
			diagnostics.SpeechGateSpeechHeadroom != 0 ||
			diagnostics.SpeechGateThresholdUnclamped != 0 ||
			diagnostics.SpeechGateClampReason != "" {
			t.Errorf("fresh gate diagnostics populated without speech metrics: %+v", diagnostics)
		}
	})
}

// TestCalculateSpeechGateThreshold covers the voiced-p10-anchored placement: the
// threshold lands at voiced p10 minus the speech margin, the narrow-gap signal
// flips at separation = speechMargin + noiseMargin (12 dB), and a crossed (narrow)
// gap keeps the threshold on the speech side rather than raising it to clear the
// loud noise.
func TestCalculateSpeechGateThreshold(t *testing.T) {
	const narrowGapBoundary = speechGateThresholdSpeechMarginDB + speechGateThresholdNoiseMarginDB // 12 dB

	t.Run("threshold is voiced p10 minus speech margin", func(t *testing.T) {
		tests := []struct {
			name       string
			voicedP10  float64
			separation float64
			wantThdDB  float64
		}{
			{"wide gap", -34.0, 26.0, -34.0 - speechGateThresholdSpeechMarginDB},
			{"moderate gap", -40.0, 18.0, -40.0 - speechGateThresholdSpeechMarginDB},
			{"narrow gap stays on speech side", -42.0, 8.0, -42.0 - speechGateThresholdSpeechMarginDB},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				threshold, _ := calculateSpeechGateThreshold(tt.voicedP10, tt.separation)
				gotDB := linearToDB(threshold)
				if math.Abs(gotDB-tt.wantThdDB) > 0.01 {
					t.Errorf("threshold = %.2f dB, want voiced p10 minus margin %.2f dB", gotDB, tt.wantThdDB)
				}
			})
		}
	})

	t.Run("narrow-gap signal flips at the margin sum", func(t *testing.T) {
		tests := []struct {
			name       string
			separation float64
			wantNarrow bool
		}{
			{"very narrow", 8.0, true},
			{"just below boundary", narrowGapBoundary - 0.1, true},
			{"at boundary is wide", narrowGapBoundary, false},
			{"wide", 26.0, false},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, narrowGap := calculateSpeechGateThreshold(-34.0, tt.separation)
				if narrowGap != tt.wantNarrow {
					t.Errorf("narrowGap = %v, want %v at separation %.1f dB", narrowGap, tt.wantNarrow, tt.separation)
				}
			})
		}
	})

	t.Run("crossed gap does not raise threshold to clear noise", func(t *testing.T) {
		// Narrow gap: noise p95 (-46) plus the noise margin (-40) sits ABOVE the
		// speech-side placement (voiced p10 -42 minus speech margin = -48). The
		// threshold must stay at the speech-side value, not rise toward the noise.
		voicedP10 := -42.0
		noiseP95 := -46.0
		separation := voicedP10 - noiseP95 // 4 dB
		threshold, narrowGap := calculateSpeechGateThreshold(voicedP10, separation)
		if !narrowGap {
			t.Fatalf("expected narrow gap at separation %.1f dB", separation)
		}
		gotDB := linearToDB(threshold)
		wantDB := voicedP10 - speechGateThresholdSpeechMarginDB // -48
		if math.Abs(gotDB-wantDB) > 0.01 {
			t.Errorf("threshold = %.2f dB, want speech-side %.2f dB (must not rise to clear noise)", gotDB, wantDB)
		}
		noiseClearDB := noiseP95 + speechGateThresholdNoiseMarginDB // -40
		if gotDB >= noiseClearDB {
			t.Errorf("threshold %.2f dB rose to clear noise %.2f dB; must resolve to the speech side", gotDB, noiseClearDB)
		}
	})
}

// TestTuneSpeechGateNewBasis is an integration-style check over in-memory
// AudioMeasurements that drives the whole tuneSpeechGate body. It asserts the
// basis end to end: a wide-gap profile case (full fixed depth,
// voiced-anchored threshold), a narrow-gap profile case (reduced fixed depth,
// threshold still on the speech side), and a no-profile case (the legacy safety
// path, which cannot place an in-speech threshold because there is no voiced
// population). It also pins the fixed parameters (attack, release, knee,
// detection) and confirms the emitted gate depth surfaces on the diagnostic.
func TestTuneSpeechGateNewBasis(t *testing.T) {
	t.Run("wide gap with profile: full depth, voiced-anchored threshold", func(t *testing.T) {
		config := newTestConfig()
		voicedP10 := -34.0
		diag := tuneSpeechGateForTest(config, &AudioMeasurements{
			Loudness: InputLoudnessMetrics{InputI: -20.0, InputLRA: 12.0},
			Noise:    NoiseMetrics{Floor: -60.0},
			Regions: RegionMetrics{
				SpeechProfile:       &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: -24.0}},
				VoicedLowPercentile: voicedP10,
				NoiseHighPercentile: -60.0,
				GateSeparationDB:    26.0,
			},
		})

		wantThdDB := voicedP10 - speechGateThresholdSpeechMarginDB
		if gotDB := linearToDB(config.SpeechGate.Threshold); math.Abs(gotDB-wantThdDB) > 0.01 {
			t.Errorf("threshold = %.2f dB, want voiced p10 minus margin %.2f dB", gotDB, wantThdDB)
		}
		if depthDB := -linearToDB(config.SpeechGate.Range); math.Abs(depthDB-speechGateDepthFixedDB) > 0.5 {
			t.Errorf("range depth = %.2f dB, want full %.2f dB on a wide gap", depthDB, speechGateDepthFixedDB)
		}
		if diag.SpeechGateNarrowGap {
			t.Error("SpeechGateNarrowGap = true, want false on a wide gap")
		}
		assertFixedGateParams(t, config)
		if diag.SpeechGateDepthDB != speechGateDepthFixedDB {
			t.Errorf("SpeechGateDepthDB = %.1f, want full %.1f on a wide gap", diag.SpeechGateDepthDB, speechGateDepthFixedDB)
		}
	})

	t.Run("narrow gap with profile: reduced depth, threshold on speech side", func(t *testing.T) {
		config := newTestConfig()
		voicedP10 := -42.0
		separation := 6.0 // below the 12 dB narrow-gap boundary
		diag := tuneSpeechGateForTest(config, &AudioMeasurements{
			Loudness: InputLoudnessMetrics{InputI: -30.0, InputLRA: 9.0},
			Noise:    NoiseMetrics{Floor: -48.0},
			Regions: RegionMetrics{
				SpeechProfile:       &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: -28.0}},
				VoicedLowPercentile: voicedP10,
				NoiseHighPercentile: voicedP10 - separation,
				GateSeparationDB:    separation,
			},
		})

		if !diag.SpeechGateNarrowGap {
			t.Fatalf("expected narrow gap at separation %.1f dB", separation)
		}
		// Threshold stays on the speech side (voiced p10 minus margin), never raised
		// toward the noise.
		wantThdDB := voicedP10 - speechGateThresholdSpeechMarginDB
		if gotDB := linearToDB(config.SpeechGate.Threshold); math.Abs(gotDB-wantThdDB) > 0.01 {
			t.Errorf("threshold = %.2f dB, want speech-side %.2f dB on a narrow gap", gotDB, wantThdDB)
		}
		if depthDB := -linearToDB(config.SpeechGate.Range); math.Abs(depthDB-speechGateDepthNarrowDB) > 0.5 {
			t.Errorf("range depth = %.2f dB, want reduced %.2f dB on a narrow gap", depthDB, speechGateDepthNarrowDB)
		}
		if config.SpeechGate.Range <= 0 {
			t.Error("SpeechGate.Range = 0, must never be a full mute")
		}
		assertFixedGateParams(t, config)
		if diag.SpeechGateDepthDB != speechGateDepthNarrowDB {
			t.Errorf("SpeechGateDepthDB = %.1f, want reduced %.1f on a narrow gap", diag.SpeechGateDepthDB, speechGateDepthNarrowDB)
		}
	})

	t.Run("no profile: legacy safety path cannot place an in-speech threshold", func(t *testing.T) {
		config := newTestConfig()
		// No SpeechProfile, so the legacy noise-floor path runs. With no voiced
		// population there is nothing to clip; the threshold is anchored to the noise
		// floor and clamped to the global limits, so it cannot land inside speech.
		diag := tuneSpeechGateForTest(config, &AudioMeasurements{
			Loudness: InputLoudnessMetrics{InputI: -22.0, InputLRA: 14.0},
			Noise:    NoiseMetrics{Floor: -55.0},
		})

		gotDB := linearToDB(config.SpeechGate.Threshold)
		if gotDB < speechGateThresholdMinDB || gotDB > speechGateThresholdMaxDB {
			t.Errorf("legacy threshold = %.2f dB, want within global limits [%.1f, %.1f]",
				gotDB, speechGateThresholdMinDB, speechGateThresholdMaxDB)
		}
		// The voiced-anchored diagnostics stay zero on the no-profile path.
		if diag.SpeechGateNarrowGap || diag.SpeechGateQuietSpeechEstimate != 0 || diag.SpeechGateSpeechSeparation != 0 {
			t.Errorf("no-profile path populated voiced diagnostics: %+v", diag)
		}
		if diag.SpeechGateDepthDB != speechGateDepthFixedDB {
			t.Errorf("SpeechGateDepthDB = %.1f, want full %.1f on the no-profile path", diag.SpeechGateDepthDB, speechGateDepthFixedDB)
		}
		assertFixedGateParams(t, config)
	})
}

// assertFixedGateParams checks the gate parameters that are fixed under the new
// basis: attack 5 ms, release 200 ms, knee 3.0, detection rms.
func assertFixedGateParams(t *testing.T, config *EffectiveFilterConfig) {
	t.Helper()
	if config.SpeechGate.Attack != speechGateAttackMS {
		t.Errorf("Attack = %.2f ms, want fixed %.2f ms", config.SpeechGate.Attack, speechGateAttackMS)
	}
	if config.SpeechGate.Release != speechGateReleaseFixedMS {
		t.Errorf("Release = %.1f ms, want fixed %.1f ms", config.SpeechGate.Release, speechGateReleaseFixedMS)
	}
	if config.SpeechGate.Knee != speechGateKneeFixed {
		t.Errorf("Knee = %.1f, want fixed %.1f", config.SpeechGate.Knee, speechGateKneeFixed)
	}
	if config.SpeechGate.Detection != "rms" {
		t.Errorf("Detection = %q, want fixed \"rms\"", config.SpeechGate.Detection)
	}
}

func tuneSpeechGateForTest(config *EffectiveFilterConfig, measurements *AudioMeasurements) *AdaptiveDiagnostics {
	diagnostics := &AdaptiveDiagnostics{}
	tuneSpeechGate(config, diagnostics, measurements)
	return diagnostics
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
	t.Run("valid typed config passes through unchanged", func(t *testing.T) {
		config := EffectiveFilterConfig{
			RumbleHighPass:   RumbleHighPassConfig{Frequency: 100.0, Width: 0.5, Mix: 0.8},
			BandlimitLowPass: BandlimitLowPassConfig{Frequency: 14000.0, Width: 0.7, Mix: 0.9},
			NoiseReduction: NoiseReductionConfig{
				Strength:             0.00001,
				PatchSec:             0.006,
				ResearchSec:          0.0058,
				Smooth:               11.0,
				AfftdnNoiseReduction: 12.0,
			},
			SpeechGate: SpeechGateConfig{
				Threshold: 0.02,
				Ratio:     2.0,
				Attack:    12,
				Release:   250,
				Range:     0.0625,
				Knee:      3.0,
				Makeup:    1.0,
			},
			LevellingCompressor: LevellingCompressorConfig{Threshold: -24.0, Ratio: 3.0, Attack: 10, Release: 200, Makeup: 0, Knee: 4.0, Mix: 1.0},
			Deesser:             DeesserConfig{Intensity: 0.3, Amount: 0.5, Frequency: 0.5},
		}
		want := config

		sanitizeConfig(&config)

		if !reflect.DeepEqual(config, want) {
			t.Errorf("sanitizeConfig changed valid typed config:\ngot  %+v\nwant %+v", config, want)
		}
	})

	t.Run("typed family non-finite values get defaults", func(t *testing.T) {
		config := EffectiveFilterConfig{
			RumbleHighPass:   RumbleHighPassConfig{Frequency: math.NaN(), Width: math.Inf(1), Mix: math.Inf(-1)},
			BandlimitLowPass: BandlimitLowPassConfig{Frequency: math.Inf(1), Width: math.NaN(), Mix: math.Inf(-1)},
			NoiseReduction: NoiseReductionConfig{
				Strength:             math.NaN(),
				PatchSec:             math.Inf(1),
				ResearchSec:          math.Inf(-1),
				Smooth:               math.NaN(),
				AfftdnNoiseReduction: math.Inf(1),
			},
			SpeechGate: SpeechGateConfig{
				Threshold: math.NaN(),
				Ratio:     math.Inf(1),
				Attack:    math.Inf(-1),
				Release:   math.NaN(),
				Range:     math.Inf(1),
				Knee:      math.Inf(-1),
				Makeup:    math.NaN(),
			},
			LevellingCompressor: LevellingCompressorConfig{
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

		if config.RumbleHighPass.Frequency != rumbleHPDefaultFreq || config.RumbleHighPass.Width != 0.707 || config.RumbleHighPass.Mix != 1.0 {
			t.Errorf("RumbleHighPass sanitised to %+v, want frequency %.1f width 0.707 mix 1.0", config.RumbleHighPass, rumbleHPDefaultFreq)
		}
		if config.BandlimitLowPass.Frequency != bandlimitLPFreq || config.BandlimitLowPass.Width != 0.707 || config.BandlimitLowPass.Mix != 1.0 {
			t.Errorf("BandlimitLowPass sanitised to %+v, want frequency %.1f width 0.707 mix 1.0", config.BandlimitLowPass, bandlimitLPFreq)
		}

		// sanitizeConfig only repairs the non-finite float fields (Strength,
		// PatchSec, ResearchSec, Smooth, AfftdnNoiseReduction); the boolean and
		// string afftdn fields keep the zero values from the input literal.
		defaultNoise := defaultNoiseReductionConfig()
		defaultNoise.Enabled = false
		defaultNoise.AfftdnEnabled = false
		defaultNoise.AfftdnNoiseType = ""
		defaultNoise.AfftdnTrackNoise = false
		if config.NoiseReduction != defaultNoise {
			t.Errorf("NoiseReduction sanitised to %+v, want %+v", config.NoiseReduction, defaultNoise)
		}

		defaultGate := defaultSpeechGateConfig()
		defaultGate.Enabled = false
		defaultGate.Detection = ""
		if config.SpeechGate != defaultGate {
			t.Errorf("SpeechGate sanitised to %+v, want %+v", config.SpeechGate, defaultGate)
		}

		defaultLevellingCompressor := defaultLevellingCompressorConfig()
		defaultLevellingCompressor.Enabled = false
		if config.LevellingCompressor != defaultLevellingCompressor {
			t.Errorf("LevellingCompressor sanitised to %+v, want %+v", config.LevellingCompressor, defaultLevellingCompressor)
		}

		defaultDeesser := defaultDeesserConfig()
		defaultDeesser.Enabled = false
		if config.Deesser != defaultDeesser {
			t.Errorf("Deesser sanitised to %+v, want %+v", config.Deesser, defaultDeesser)
		}
	})

	t.Run("gate threshold keeps existing zero and negative clamp behaviour", func(t *testing.T) {
		for _, threshold := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 0.0, -0.5} {
			config := EffectiveFilterConfig{SpeechGate: SpeechGateConfig{Threshold: threshold}}

			sanitizeConfig(&config)

			if config.SpeechGate.Threshold != speechGateDefaultThreshold {
				t.Errorf("SpeechGate.Threshold for input %v = %v, want %v", threshold, config.SpeechGate.Threshold, speechGateDefaultThreshold)
			}
		}
	})

	t.Run("zero values for non-gate typed fields pass through", func(t *testing.T) {
		config := EffectiveFilterConfig{
			RumbleHighPass:      RumbleHighPassConfig{Frequency: 0.0, Width: 0.0, Mix: 0.0},
			Deesser:             DeesserConfig{Intensity: 0.0},
			LevellingCompressor: LevellingCompressorConfig{Ratio: 0.0, Threshold: 0.0},
			SpeechGate:          SpeechGateConfig{Threshold: 1e-10},
		}

		sanitizeConfig(&config)

		if config.RumbleHighPass.Frequency != 0.0 || config.RumbleHighPass.Width != 0.0 || config.RumbleHighPass.Mix != 0.0 {
			t.Errorf("RumbleHighPass zero values changed: %+v", config.RumbleHighPass)
		}
		if config.Deesser.Intensity != 0.0 {
			t.Errorf("Deesser.Intensity = %v, want 0.0", config.Deesser.Intensity)
		}
		if config.LevellingCompressor.Ratio != 0.0 || config.LevellingCompressor.Threshold != 0.0 {
			t.Errorf("LevellingCompressor zero values changed: %+v", config.LevellingCompressor)
		}
		if config.SpeechGate.Threshold != 1e-10 {
			t.Errorf("SpeechGate.Threshold = %v, want 1e-10", config.SpeechGate.Threshold)
		}
	})

	t.Run("negative LevellingCompressor threshold passes through", func(t *testing.T) {
		config := EffectiveFilterConfig{
			LevellingCompressor: LevellingCompressorConfig{Threshold: -40.0, Ratio: 3.0},
			SpeechGate:          SpeechGateConfig{Threshold: 0.02},
		}

		sanitizeConfig(&config)

		if config.LevellingCompressor.Threshold != -40.0 {
			t.Errorf("LevellingCompressor.Threshold = %v, want -40.0", config.LevellingCompressor.Threshold)
		}
	})
}

func TestTuneLevellingCompressorThresholdSpeechRMSAnchor(t *testing.T) {
	config := newTestConfig()
	measurements := &AudioMeasurements{
		Dynamics: DynamicsMetrics{PeakLevel: -6.0, RMSLevel: -32.0}, // full-file RMS below speech RMS, floor inert
		Regions:  RegionMetrics{SpeechProfile: &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: -24.0}}},
	}

	tuneLevellingCompressorThreshold(config, measurements)

	want := -24.0 + levellingCompressorThresholdSpeechOffsetDB // -15.0
	if math.Abs(config.LevellingCompressor.Threshold-want) > 0.001 {
		t.Errorf("LevellingCompressor.Threshold = %.3f, want %.3f (speech RMS + offset)", config.LevellingCompressor.Threshold, want)
	}
}

func TestTuneLevellingCompressorThresholdSpeechRMSClampedHigh(t *testing.T) {
	config := newTestConfig()
	// Loud speech: RMS -10 + 9 = -1, above the -6 ceiling -> clamps to -6.
	measurements := &AudioMeasurements{
		Dynamics: DynamicsMetrics{RMSLevel: -20.0}, // full-file RMS below speech RMS, floor inert
		Regions:  RegionMetrics{SpeechProfile: &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: -10.0}}},
	}

	tuneLevellingCompressorThreshold(config, measurements)

	if math.Abs(config.LevellingCompressor.Threshold-levellingCompressorThresholdMax) > 0.001 {
		t.Errorf("LevellingCompressor.Threshold = %.3f, want %.3f (clamp ceiling)", config.LevellingCompressor.Threshold, levellingCompressorThresholdMax)
	}
}

func TestTuneLevellingCompressorThresholdSpeechRMSClampedLow(t *testing.T) {
	config := newTestConfig()
	// Very quiet speech: RMS -60 + 9 = -51, below the -45 floor -> clamps to -45.
	// NaN full-file RMS keeps the floor out so this tests the low clamp alone.
	measurements := &AudioMeasurements{
		Dynamics: DynamicsMetrics{RMSLevel: math.NaN()},
		Regions:  RegionMetrics{SpeechProfile: &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: -60.0}}},
	}

	tuneLevellingCompressorThreshold(config, measurements)

	if math.Abs(config.LevellingCompressor.Threshold-levellingCompressorThresholdMin) > 0.001 {
		t.Errorf("LevellingCompressor.Threshold = %.3f, want %.3f (clamp floor)", config.LevellingCompressor.Threshold, levellingCompressorThresholdMin)
	}
}

func TestTuneLevellingCompressorThresholdPeakFallbackNoProfile(t *testing.T) {
	config := newTestConfig()
	measurements := &AudioMeasurements{
		Dynamics: DynamicsMetrics{PeakLevel: -6.0},
	}

	tuneLevellingCompressorThreshold(config, measurements)

	want := -6.0 - levellingCompressorFallbackPeakHeadroomDB // -26.0
	if math.Abs(config.LevellingCompressor.Threshold-want) > 0.001 {
		t.Errorf("LevellingCompressor.Threshold = %.3f, want %.3f (peak fallback)", config.LevellingCompressor.Threshold, want)
	}
}

func TestTuneLevellingCompressorThresholdAcceptsZeroDBPeak(t *testing.T) {
	config := newTestConfig()
	measurements := &AudioMeasurements{
		Dynamics: DynamicsMetrics{PeakLevel: 0.0},
	}

	tuneLevellingCompressorThreshold(config, measurements)

	if math.Abs(config.LevellingCompressor.Threshold-(-20.0)) > 0.001 {
		t.Errorf("LevellingCompressor.Threshold = %.3f, want -20.000", config.LevellingCompressor.Threshold)
	}
}

func TestTuneLevellingCompressorThresholdFallsBackForInvalidPeak(t *testing.T) {
	config := newTestConfig()
	measurements := &AudioMeasurements{
		Dynamics: DynamicsMetrics{PeakLevel: math.NaN()},
	}

	tuneLevellingCompressorThreshold(config, measurements)

	if math.Abs(config.LevellingCompressor.Threshold-defaultLevellingCompressorThreshold) > 0.001 {
		t.Errorf("LevellingCompressor.Threshold = %.3f, want %.3f", config.LevellingCompressor.Threshold, defaultLevellingCompressorThreshold)
	}
}

func TestTuneLevellingCompressorThresholdFullFileRMSFloor(t *testing.T) {
	tests := []struct {
		name        string
		speechRMS   float64
		fullFileRMS float64
		want        float64
	}{
		{
			// Speech RMS above full-file RMS: the floor is inert, threshold tracks speech.
			name:        "speech above full-file (floor inert)",
			speechRMS:   -24.0,
			fullFileRMS: -40.0,
			want:        -24.0 + levellingCompressorThresholdSpeechOffsetDB, // -15.0
		},
		{
			// Anomalously quiet speech election: floored at the full-file RMS.
			name:        "speech below full-file (floor engaged)",
			speechRMS:   -50.0,
			fullFileRMS: -40.0,
			want:        -40.0 + levellingCompressorThresholdSpeechOffsetDB, // -31.0
		},
		{
			// NaN full-file RMS: guard falls back to the raw speech RMS.
			name:        "NaN full-file RMS falls back to speech",
			speechRMS:   -24.0,
			fullFileRMS: math.NaN(),
			want:        -24.0 + levellingCompressorThresholdSpeechOffsetDB, // -15.0
		},
		{
			// +Inf full-file RMS: guard falls back to the raw speech RMS.
			name:        "Inf full-file RMS falls back to speech",
			speechRMS:   -24.0,
			fullFileRMS: math.Inf(1),
			want:        -24.0 + levellingCompressorThresholdSpeechOffsetDB, // -15.0
		},
		{
			// Floor raises speech above the -6 ceiling: clamp applies after flooring.
			name:        "floor then clamp ceiling",
			speechRMS:   -50.0,
			fullFileRMS: -8.0, // -8 + 9 = 1, above -6 -> clamps to -6
			want:        levellingCompressorThresholdMax,
		},
		{
			// Unmeasured astats leaves RMSLevel at the 0.0 zero value; the guard must
			// treat it as absent and not floor the speech RMS up to 0 dBFS.
			name:        "zero full-file RMS (unmeasured astats) falls back to speech",
			speechRMS:   -24.0,
			fullFileRMS: 0.0,
			want:        -24.0 + levellingCompressorThresholdSpeechOffsetDB, // -15.0
		},
		{
			// -Inf full-file RMS: guard falls back to the raw speech RMS.
			name:        "negative Inf full-file RMS falls back to speech",
			speechRMS:   -24.0,
			fullFileRMS: math.Inf(-1),
			want:        -24.0 + levellingCompressorThresholdSpeechOffsetDB, // -15.0
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestConfig()
			measurements := &AudioMeasurements{
				Dynamics: DynamicsMetrics{RMSLevel: tt.fullFileRMS},
				Regions:  RegionMetrics{SpeechProfile: &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: tt.speechRMS}}},
			}

			tuneLevellingCompressorThreshold(config, measurements)

			if math.Abs(config.LevellingCompressor.Threshold-tt.want) > 0.001 {
				t.Errorf("LevellingCompressor.Threshold = %.3f, want %.3f", config.LevellingCompressor.Threshold, tt.want)
			}
		})
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

func TestTuneNoiseReduction(t *testing.T) {
	t.Run("voice-activated disables afftdn", func(t *testing.T) {
		config := &EffectiveFilterConfig{NoiseReduction: defaultNoiseReductionConfig()}
		diag := &AdaptiveDiagnostics{}
		measurements := &AudioMeasurements{Noise: NoiseMetrics{Floor: -58.0, VoiceActivated: true}}

		tuneNoiseReduction(config, diag, measurements)

		if config.NoiseReduction.AfftdnEnabled {
			t.Error("afftdn should be disabled on voice-activated captures")
		}
		if diag.AfftdnEnabled {
			t.Error("diagnostics AfftdnEnabled should be false on voice-activated captures")
		}
		if diag.AfftdnDisableReason != "voice_activated" {
			t.Errorf("AfftdnDisableReason = %q, want voice_activated", diag.AfftdnDisableReason)
		}
		if config.NoiseReduction.AfftdnNoiseFloor != 0 {
			t.Errorf("disabled afftdn should not set a noise floor, got %.2f", config.NoiseReduction.AfftdnNoiseFloor)
		}
	})

	t.Run("measured floor sets nf and turns tracking off", func(t *testing.T) {
		config := &EffectiveFilterConfig{NoiseReduction: defaultNoiseReductionConfig()}
		diag := &AdaptiveDiagnostics{}
		measurements := &AudioMeasurements{Noise: NoiseMetrics{Floor: -58.0}}

		tuneNoiseReduction(config, diag, measurements)

		if !config.NoiseReduction.AfftdnEnabled {
			t.Error("afftdn should stay enabled on a normal capture")
		}
		if config.NoiseReduction.AfftdnNoiseFloor != -58.0 {
			t.Errorf("AfftdnNoiseFloor = %.2f, want -58.0", config.NoiseReduction.AfftdnNoiseFloor)
		}
		if config.NoiseReduction.AfftdnTrackNoise {
			t.Error("AfftdnTrackNoise should be off when a static floor is set")
		}
		if diag.AfftdnNoiseFloorDB != -58.0 {
			t.Errorf("diagnostics AfftdnNoiseFloorDB = %.2f, want -58.0", diag.AfftdnNoiseFloorDB)
		}
		if !diag.AfftdnEnabled {
			t.Error("diagnostics AfftdnEnabled should be true on a normal capture")
		}
	})

	t.Run("out-of-range floor clamps into afftdn nf range", func(t *testing.T) {
		// A floor below afftdn's -80 dB minimum clamps up to -80.
		lowConfig := &EffectiveFilterConfig{NoiseReduction: defaultNoiseReductionConfig()}
		tuneNoiseReduction(lowConfig, &AdaptiveDiagnostics{}, &AudioMeasurements{Noise: NoiseMetrics{Floor: -120.0}})
		if lowConfig.NoiseReduction.AfftdnNoiseFloor != afftdnNoiseFloorMinDB {
			t.Errorf("floor below range = %.2f, want %.2f", lowConfig.NoiseReduction.AfftdnNoiseFloor, afftdnNoiseFloorMinDB)
		}

		// A floor above afftdn's -20 dB maximum clamps down to -20.
		highConfig := &EffectiveFilterConfig{NoiseReduction: defaultNoiseReductionConfig()}
		tuneNoiseReduction(highConfig, &AdaptiveDiagnostics{}, &AudioMeasurements{Noise: NoiseMetrics{Floor: -5.0}})
		if highConfig.NoiseReduction.AfftdnNoiseFloor != afftdnNoiseFloorMaxDB {
			t.Errorf("floor above range = %.2f, want %.2f", highConfig.NoiseReduction.AfftdnNoiseFloor, afftdnNoiseFloorMaxDB)
		}
	})

	t.Run("unmeasured floor leaves safe defaults", func(t *testing.T) {
		config := &EffectiveFilterConfig{NoiseReduction: defaultNoiseReductionConfig()}
		diag := &AdaptiveDiagnostics{}
		measurements := &AudioMeasurements{Noise: NoiseMetrics{Floor: 0}} // unmeasured

		tuneNoiseReduction(config, diag, measurements)

		if !config.NoiseReduction.AfftdnEnabled {
			t.Error("afftdn should stay enabled when the floor is unmeasured")
		}
		if !config.NoiseReduction.AfftdnTrackNoise {
			t.Error("track_noise should stay on when the floor is unmeasured")
		}
		if config.NoiseReduction.AfftdnNoiseFloor != 0 {
			t.Errorf("AfftdnNoiseFloor should stay unset, got %.2f", config.NoiseReduction.AfftdnNoiseFloor)
		}
	})

	t.Run("qualifying measurements elect the custom profile", func(t *testing.T) {
		config := &EffectiveFilterConfig{NoiseReduction: defaultNoiseReductionConfig()}
		diag := &AdaptiveDiagnostics{}
		measurements := &AudioMeasurements{
			Noise: NoiseMetrics{Floor: -58.0},
			Regions: RegionMetrics{
				GateSeparationDB: 15.0,
				NoiseProfile: &NoiseProfile{
					SpectralFlatness: 0.6,
					BandsMeasured:    true,
					// Mean is -60; shapes are values minus mean: -1, 0, +1.
					BandNoise: []float64{-61.0, -60.0, -59.0},
				},
			},
		}

		tuneNoiseReduction(config, diag, measurements)

		if config.NoiseReduction.AfftdnNoiseType != "custom" {
			t.Errorf("AfftdnNoiseType = %q, want custom", config.NoiseReduction.AfftdnNoiseType)
		}
		if config.NoiseReduction.AfftdnBandNoise != "-1.0|0.0|1.0" {
			t.Errorf("AfftdnBandNoise = %q, want -1.0|0.0|1.0", config.NoiseReduction.AfftdnBandNoise)
		}
		if config.NoiseReduction.AfftdnNoiseFloor != -58.0 {
			t.Errorf("custom profile must keep the measured nf, got %.2f", config.NoiseReduction.AfftdnNoiseFloor)
		}
		if config.NoiseReduction.AfftdnTrackNoise {
			t.Error("custom profile must keep track_noise off")
		}
		if diag.AfftdnNoiseType != "custom" {
			t.Errorf("diagnostics AfftdnNoiseType = %q, want custom", diag.AfftdnNoiseType)
		}
	})

	t.Run("trailing non-finite band stays custom with a well-formed bn", func(t *testing.T) {
		// The top band (above the band-limit) comes back non-finite. The profile is
		// still measured (the guard counts finite bands), and bn must elect the
		// custom path with the non-finite band flattened to 0.0, never a NaN token.
		config := &EffectiveFilterConfig{NoiseReduction: defaultNoiseReductionConfig()}
		diag := &AdaptiveDiagnostics{}
		measurements := &AudioMeasurements{
			Noise: NoiseMetrics{Floor: -58.0},
			Regions: RegionMetrics{
				GateSeparationDB: 15.0,
				NoiseProfile: &NoiseProfile{
					SpectralFlatness: 0.6,
					BandsMeasured:    true,
					// Mean over the finite bands is -60; the trailing NaN flattens to 0.0.
					BandNoise: []float64{-61.0, -60.0, -59.0, math.NaN()},
				},
			},
		}

		tuneNoiseReduction(config, diag, measurements)

		if config.NoiseReduction.AfftdnNoiseType != "custom" {
			t.Errorf("AfftdnNoiseType = %q, want custom", config.NoiseReduction.AfftdnNoiseType)
		}
		if config.NoiseReduction.AfftdnBandNoise != "-1.0|0.0|1.0|0.0" {
			t.Errorf("AfftdnBandNoise = %q, want -1.0|0.0|1.0|0.0", config.NoiseReduction.AfftdnBandNoise)
		}
		if strings.Contains(config.NoiseReduction.AfftdnBandNoise, "NaN") ||
			strings.Contains(config.NoiseReduction.AfftdnBandNoise, "Inf") {
			t.Errorf("AfftdnBandNoise = %q, must not contain a NaN or Inf token", config.NoiseReduction.AfftdnBandNoise)
		}
	})

	t.Run("all non-finite bands fall back to the white path", func(t *testing.T) {
		// A broad measurement failure (every band non-finite) yields an empty bn, so
		// tuneNoiseReduction keeps the white profile rather than emitting nt=custom.
		config := &EffectiveFilterConfig{NoiseReduction: defaultNoiseReductionConfig()}
		diag := &AdaptiveDiagnostics{}
		measurements := &AudioMeasurements{
			Noise: NoiseMetrics{Floor: -58.0},
			Regions: RegionMetrics{
				GateSeparationDB: 15.0,
				NoiseProfile: &NoiseProfile{
					SpectralFlatness: 0.6,
					BandsMeasured:    true,
					BandNoise:        []float64{math.NaN(), math.Inf(-1), math.Inf(1)},
				},
			},
		}

		tuneNoiseReduction(config, diag, measurements)

		if config.NoiseReduction.AfftdnNoiseType != "w" {
			t.Errorf("AfftdnNoiseType = %q, want w", config.NoiseReduction.AfftdnNoiseType)
		}
		if config.NoiseReduction.AfftdnBandNoise != "" {
			t.Errorf("AfftdnBandNoise = %q, want empty", config.NoiseReduction.AfftdnBandNoise)
		}
	})

	t.Run("non-qualifying measurements keep the white path", func(t *testing.T) {
		// Each case fails exactly one gate; all else qualifies.
		base := func() *AudioMeasurements {
			return &AudioMeasurements{
				Noise: NoiseMetrics{Floor: -58.0},
				Regions: RegionMetrics{
					GateSeparationDB: 15.0,
					NoiseProfile: &NoiseProfile{
						SpectralFlatness: 0.6,
						BandsMeasured:    true,
						BandNoise:        []float64{-61.0, -60.0, -59.0},
					},
				},
			}
		}

		cases := map[string]func(*AudioMeasurements){
			"bands unmeasured":   func(m *AudioMeasurements) { m.Regions.NoiseProfile.BandsMeasured = false },
			"separation too low": func(m *AudioMeasurements) { m.Regions.GateSeparationDB = 11.0 },
			"too tonal":          func(m *AudioMeasurements) { m.Regions.NoiseProfile.SpectralFlatness = 0.40 },
			"no noise profile":   func(m *AudioMeasurements) { m.Regions.NoiseProfile = nil },
		}

		for name, mutate := range cases {
			t.Run(name, func(t *testing.T) {
				config := &EffectiveFilterConfig{NoiseReduction: defaultNoiseReductionConfig()}
				diag := &AdaptiveDiagnostics{}
				m := base()
				mutate(m)

				tuneNoiseReduction(config, diag, m)

				if config.NoiseReduction.AfftdnNoiseType != "w" {
					t.Errorf("AfftdnNoiseType = %q, want w", config.NoiseReduction.AfftdnNoiseType)
				}
				if config.NoiseReduction.AfftdnBandNoise != "" {
					t.Errorf("AfftdnBandNoise = %q, want empty", config.NoiseReduction.AfftdnBandNoise)
				}
			})
		}
	})
}

// TestBuildAfftdnBandNoise covers the bn mean-subtraction and clip maths.
func TestBuildAfftdnBandNoise(t *testing.T) {
	t.Run("empty input yields empty string", func(t *testing.T) {
		if got := buildAfftdnBandNoise(nil); got != "" {
			t.Errorf("buildAfftdnBandNoise(nil) = %q, want empty", got)
		}
	})

	t.Run("subtracts the mean and formats one decimal", func(t *testing.T) {
		// Mean of {-50, -40, -30} is -40; shapes are -10, 0, +10.
		got := buildAfftdnBandNoise([]float64{-50.0, -40.0, -30.0})
		if got != "-10.0|0.0|10.0" {
			t.Errorf("buildAfftdnBandNoise = %q, want -10.0|0.0|10.0", got)
		}
	})

	t.Run("clips shapes to afftdn [-24, +24] range", func(t *testing.T) {
		// Mean of {-100, 0} is -50; raw shapes are -50 and +50, clipped to ±24.
		got := buildAfftdnBandNoise([]float64{-100.0, 0.0})
		if got != "-24.0|24.0" {
			t.Errorf("buildAfftdnBandNoise = %q, want -24.0|24.0", got)
		}
	})

	t.Run("trailing NaN: mean over finite bands, NaN emitted as 0.0", func(t *testing.T) {
		// Mean is taken over the finite bands {-50, -40, -30} = -40; shapes are
		// -10, 0, +10. The trailing NaN (e.g. the top band above the band-limit)
		// must emit 0.0, never a NaN token.
		got := buildAfftdnBandNoise([]float64{-50.0, -40.0, -30.0, math.NaN()})
		if got != "-10.0|0.0|10.0|0.0" {
			t.Errorf("buildAfftdnBandNoise = %q, want -10.0|0.0|10.0|0.0", got)
		}
		if strings.Contains(got, "NaN") || strings.Contains(got, "Inf") {
			t.Errorf("buildAfftdnBandNoise = %q, must not contain a NaN or Inf token", got)
		}
	})

	t.Run("interior Inf band excluded from mean, emitted as 0.0", func(t *testing.T) {
		// -Inf is excluded from the mean (mean of {-50, -30} = -40); shapes are
		// -10, 0.0 (for the -Inf band), +10.
		got := buildAfftdnBandNoise([]float64{-50.0, math.Inf(-1), -30.0})
		if got != "-10.0|0.0|10.0" {
			t.Errorf("buildAfftdnBandNoise = %q, want -10.0|0.0|10.0", got)
		}
		if strings.Contains(got, "Inf") {
			t.Errorf("buildAfftdnBandNoise = %q, must not contain an Inf token", got)
		}
	})

	t.Run("finite but silent band is a real measurement", func(t *testing.T) {
		// A very low but finite floor (-120) is a legitimate measurement, included
		// in the mean. Mean of {-120, -40, -40} = -66.67; shapes clip the -120 band
		// to -24 and lift the -40 bands by +26.67, clipped to +24.
		got := buildAfftdnBandNoise([]float64{-120.0, -40.0, -40.0})
		if got != "-24.0|24.0|24.0" {
			t.Errorf("buildAfftdnBandNoise = %q, want -24.0|24.0|24.0", got)
		}
	})

	t.Run("all non-finite yields empty string for white fallback", func(t *testing.T) {
		got := buildAfftdnBandNoise([]float64{math.NaN(), math.Inf(1), math.Inf(-1)})
		if got != "" {
			t.Errorf("buildAfftdnBandNoise = %q, want empty (white fallback)", got)
		}
	})
}
