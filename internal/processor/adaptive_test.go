package processor

import (
	"math"
	"reflect"
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
	if !firstDiagnostics.SpeechGateGentleMode {
		t.Fatal("file A setup failed: expected gentle gate mode")
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
				"afftdn=nr=12:nt=w:tn=1," +
				"agate=threshold=0.012589:ratio=1.2:attack=10.00:release=575:range=0.1585:knee=2.0:detection=rms:makeup=1.0," +
				"acompressor=threshold=0.031623:ratio=3.0:attack=10:release=200:makeup=1.00:knee=4.0:detection=rms:mix=1.00",
		},
		{
			name:         "bright speech with noise profile",
			measurements: orderIndependenceBrightSpeechMeasurements(),
			want: "highpass=f=80:poles=2:width_type=q:width=0.707:normalize=1:a=tdii," +
				"lowpass=f=20500:poles=2:width_type=q:width=0.707:normalize=1," +
				"anlmdn=s=0.00001:p=0.0060:r=0.0058:m=11," +
				"afftdn=nr=12:nt=w:tn=1," +
				"agate=threshold=0.019953:ratio=2.0:attack=10.00:release=425:range=0.1585:knee=3.0:detection=rms:makeup=1.0," +
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
		{"SpeechGateGentleMode", got.SpeechGateGentleMode, want.SpeechGateGentleMode},
		{"SpeechGateAggression", got.SpeechGateAggression, want.SpeechGateAggression},
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
				Spectral: SpectralMetrics{Kurtosis: tt.kurtosis, Flatness: tt.flatness, Flux: tt.flux, Crest: tt.crest},
			}

			got := detectContentType(m)

			if got != tt.want {
				t.Errorf("detectContentType() = %v, want %v [%s]", got, tt.want, tt.desc)
			}
		})
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
		// Attack collapsed to a fixed 10ms floor; transient/flux inputs no longer matter.
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
		// Knee collapsed to a fixed value; spectral crest no longer matters.
		// Gentle mode overrides it; that case is covered separately.
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

	t.Run("range based on noise floor", func(t *testing.T) {
		// Range is driven by the noise floor alone:
		// floor < -70 dBFS → clean range (-22 dB), else standard range (-16 dB).
		tests := []struct {
			name        string
			noiseFloor  float64
			wantRangeDB float64
			desc        string
		}{
			{"clean floor - deeper range", -85.0, speechGateRangeCleanDB, "very clean recording, deeper range"},
			{"clean boundary - deeper range", -70.1, speechGateRangeCleanDB, "just below clean threshold"},
			{"standard floor - gentle range", -62.0, speechGateRangeStandardDB, "noisier floor, standard range"},
			{"standard boundary", -70.0, speechGateRangeStandardDB, "at threshold counts as standard"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					Noise: NoiseMetrics{Floor: tt.noiseFloor},
					Regions: RegionMetrics{
						NoiseProfile: &NoiseProfile{
							PeakLevel:   tt.noiseFloor + 5,
							CrestFactor: 10.0,
							Entropy:     0.005,
						},
					},
				}

				tuneSpeechGateForTest(config, measurements)

				actualDB := linearToDB(config.SpeechGate.Range)
				diff := actualDB - tt.wantRangeDB
				if diff < 0 {
					diff = -diff
				}
				if diff > 0.5 {
					t.Errorf("SpeechGate.Range = %.1f dB, want %.1f dB [%s]",
						actualDB, tt.wantRangeDB, tt.desc)
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

	t.Run("release based on speech sustain", func(t *testing.T) {
		// Release no longer keys off room-tone entropy. A fixed +50ms hold
		// compensation and +75ms tonal allowance are always applied. The only
		// content split is sustained speech vs standard:
		// - Sustained (flux < 0.01 AND zcr < 0.08): 300 + 50 + 75 = 425ms
		// - Standard (otherwise):                   250 + 50 + 75 = 375ms
		tests := []struct {
			name        string
			flux        float64
			zcr         float64
			wantRelease float64
			desc        string
		}{
			{"sustained speech", 0.005, 0.05, 425, "low flux + low zcr → sustained release"},
			{"standard speech", 0.02, 0.20, 375, "active speech → standard release"},
			{"flux high but zcr high", 0.005, 0.20, 375, "zcr disqualifies sustained → standard"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					Spectral: SpectralMetrics{Flux: tt.flux},
					Dynamics: DynamicsMetrics{ZeroCrossingsRate: tt.zcr},
					Noise:    NoiseMetrics{Floor: -55.0},
					Loudness: InputLoudnessMetrics{InputLRA: 15.0}, // Above LRA threshold (10 LU): no extension
					Regions: RegionMetrics{
						NoiseProfile: &NoiseProfile{
							PeakLevel:   -50.0,
							CrestFactor: 15.0,
							Entropy:     0.005,
						},
					},
				}

				tuneSpeechGateForTest(config, measurements)

				if config.SpeechGate.Release != tt.wantRelease {
					t.Errorf("SpeechGate.Release = %.1f ms, want %.1f ms [%s]",
						config.SpeechGate.Release, tt.wantRelease, tt.desc)
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
		// speechGateReleaseLRALow = 10.0 LU (below: extend release)
		// speechGateReleaseLRAVeryLow = 8.0 LU (below: maximum extension)
		// speechGateReleaseLRAExtension = 100ms (extension for low LRA)
		// speechGateReleaseLRAMaxExt = 150ms (max extension for very low LRA)

		// Standard-tier base release (flux 0.02 → 250 + 50 hold + 75 tonal = 375ms),
		// then LRA extension on top.
		tests := []struct {
			name        string
			lra         float64
			wantRelease float64
			desc        string
		}{
			{"wide LRA - no extension", 16.0, 375, "wide dynamics don't need release extension"},
			{"moderate LRA - no extension", 12.0, 375, "moderate dynamics don't need release extension"},
			{"low LRA - partial extension", 9.0, 425, "375 + 50% of 100ms extension"},
			{"very low LRA - maximum extension", 7.0, 525, "375 + 150ms max extension"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				config := newTestConfig()
				measurements := &AudioMeasurements{
					Spectral: SpectralMetrics{Flux: 0.02}, // Standard tier

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

				if config.SpeechGate.Release != tt.wantRelease {
					t.Errorf("SpeechGate.Release = %.1f ms (LRA=%.1f LU), want %.1f ms [%s]",
						config.SpeechGate.Release, tt.lra, tt.wantRelease, tt.desc)
				}
			})
		}
	})

	t.Run("populates diagnostics with speech metrics", func(t *testing.T) {
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
				SpeechProfile: &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: -35.0, CrestFactor: 10.0}},
			},
		})

		if !diagnostics.SpeechGateGentleMode {
			t.Fatal("expected first tuning to enable gentle mode")
		}
		if diagnostics.SpeechGateAggression == 0 ||
			diagnostics.SpeechGateDynamicRange == 0 ||
			diagnostics.SpeechGateQuietSpeechEstimate == 0 ||
			diagnostics.SpeechGateSpeechSeparation == 0 ||
			diagnostics.SpeechGateThresholdUnclamped == 0 ||
			diagnostics.SpeechGateClampReason == "" {
			t.Fatalf("expected tuning to populate gate diagnostics: %+v", diagnostics)
		}
		if config.SpeechGate.Ratio != speechGateGentleRatio || config.SpeechGate.Knee != speechGateGentleKnee {
			t.Fatalf("expected gentle mode to tune builder values, ratio=%.1f knee=%.1f",
				config.SpeechGate.Ratio, config.SpeechGate.Knee)
		}
		assertNoStaleEffectiveConfigFields(t)
	})

	t.Run("fresh diagnostics without speech metrics", func(t *testing.T) {
		config := newTestConfig()
		diagnostics := tuneSpeechGateForTest(config, &AudioMeasurements{
			Spectral: SpectralMetrics{Flux: 0.02},
			Loudness: InputLoudnessMetrics{InputI: -20.0, InputLRA: 16.0},
			Noise:    NoiseMetrics{Floor: -55.0},
		})

		if diagnostics.SpeechGateGentleMode {
			t.Error("diagnostics SpeechGateGentleMode = true, want false")
		}
		if diagnostics.SpeechGateAggression != 0 ||
			diagnostics.SpeechGateDynamicRange != 0 ||
			diagnostics.SpeechGateQuietSpeechEstimate != 0 ||
			diagnostics.SpeechGateSpeechSeparation != 0 ||
			diagnostics.SpeechGateSpeechHeadroom != 0 ||
			diagnostics.SpeechGateThresholdUnclamped != 0 ||
			diagnostics.SpeechGateClampReason != "" {
			t.Errorf("fresh gate diagnostics populated without speech metrics: %+v", diagnostics)
		}
	})
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
		Dynamics: DynamicsMetrics{PeakLevel: -6.0},
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
		Regions: RegionMetrics{SpeechProfile: &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: -10.0}}},
	}

	tuneLevellingCompressorThreshold(config, measurements)

	if math.Abs(config.LevellingCompressor.Threshold-levellingCompressorThresholdMax) > 0.001 {
		t.Errorf("LevellingCompressor.Threshold = %.3f, want %.3f (clamp ceiling)", config.LevellingCompressor.Threshold, levellingCompressorThresholdMax)
	}
}

func TestTuneLevellingCompressorThresholdSpeechRMSClampedLow(t *testing.T) {
	config := newTestConfig()
	// Very quiet speech: RMS -60 + 9 = -51, below the -45 floor -> clamps to -45.
	measurements := &AudioMeasurements{
		Regions: RegionMetrics{SpeechProfile: &SpeechCandidateMetrics{RegionSample: RegionSample{RMSLevel: -60.0}}},
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
