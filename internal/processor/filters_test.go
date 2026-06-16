package processor

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

// newTestBaseConfig creates a minimal BaseFilterConfig for testing.
// All filters are disabled by default - enable only what you need for each test.
// This isolates tests from application default configuration changes.
func newTestBaseConfig() *BaseFilterConfig {
	defaults := assembleFilterDefaults(
		DownmixConfig{Enabled: false},
		AnalysisConfig{Enabled: false},
		ResampleConfig{Enabled: false, SampleRate: 44100, Format: "s16", FrameSize: 4096},
		RumbleHighPassConfig{
			Enabled:   false,
			Frequency: 80.0,
			Poles:     2,
			Width:     0.707,
			Mix:       1.0,
			Transform: "tdii",
		},
		BandlimitLowPassConfig{
			Enabled:   false,
			Frequency: 16000.0,
			Poles:     2,
			Width:     0.707,
			Mix:       1.0,
		},
		NoiseReductionConfig{
			Enabled:              false,
			Strength:             0.00001,
			PatchSec:             0.006,
			ResearchSec:          0.0058,
			Smooth:               11.0,
			AfftdnEnabled:        true,
			AfftdnNoiseReduction: 12,
			AfftdnNoiseType:      "w",
			AfftdnTrackNoise:     true,
		},
		SpeechGateConfig{
			Enabled:   false,
			Threshold: 0.01,
			Ratio:     2.0,
			Attack:    20,
			Release:   250,
			Range:     0.0625,
			Knee:      2.828,
			Makeup:    1.0,
			Detection: "rms",
		},
		LevellingCompressorConfig{
			Enabled:   false,
			Threshold: -20,
			Ratio:     2.5,
			Attack:    15,
			Release:   80,
			Makeup:    0,
			Knee:      2.5,
			Mix:       1.0,
		},
		DeesserConfig{Enabled: false, Intensity: 0.5, Amount: 0.5, Frequency: 0.5},
		AdeclickConfig{Enabled: true, Threshold: 2.0, Window: 55.0, Overlap: 50.0, Method: "s"},
		LoudnormConfig{Enabled: true, TargetI: -16.0, TargetTP: -1.5, TargetLRA: 11.0, DualMono: true, Linear: true},
	)
	defaults.FilterOrder = Pass2FilterOrder
	return &BaseFilterConfig{filterConfigDefaults: defaults}
}

// newTestConfig creates a minimal effective config for builder and tuner tests
// that operate after seed assembly.
func newTestConfig() *EffectiveFilterConfig {
	return deriveEffectiveFilterConfig(newTestBaseConfig())
}

func TestDefaultFilterConfigComposesTypedDefaults(t *testing.T) {
	config := DefaultFilterConfig()

	if config.Downmix != defaultDownmixConfig() {
		t.Errorf("Downmix = %+v, want %+v", config.Downmix, defaultDownmixConfig())
	}
	if config.Analysis != defaultAnalysisConfig() {
		t.Errorf("Analysis = %+v, want %+v", config.Analysis, defaultAnalysisConfig())
	}
	if config.Resample != defaultResampleConfig() {
		t.Errorf("Resample = %+v, want %+v", config.Resample, defaultResampleConfig())
	}
	if config.RumbleHighPass != defaultRumbleHighPassConfig() {
		t.Errorf("RumbleHighPass = %+v, want %+v", config.RumbleHighPass, defaultRumbleHighPassConfig())
	}
	if config.BandlimitLowPass != defaultBandlimitLowPassConfig() {
		t.Errorf("BandlimitLowPass = %+v, want %+v", config.BandlimitLowPass, defaultBandlimitLowPassConfig())
	}
	if config.NoiseReduction != defaultNoiseReductionConfig() {
		t.Errorf("NoiseReduction = %+v, want %+v", config.NoiseReduction, defaultNoiseReductionConfig())
	}
	if config.SpeechGate != defaultSpeechGateConfig() {
		t.Errorf("SpeechGate = %+v, want %+v", config.SpeechGate, defaultSpeechGateConfig())
	}
	if config.LevellingCompressor != defaultLevellingCompressorConfig() {
		t.Errorf("LevellingCompressor = %+v, want %+v", config.LevellingCompressor, defaultLevellingCompressorConfig())
	}
	if config.Deesser != defaultDeesserConfig() {
		t.Errorf("Deesser = %+v, want %+v", config.Deesser, defaultDeesserConfig())
	}
	if config.Adeclick != defaultAdeclickConfig() {
		t.Errorf("Adeclick = %+v, want %+v", config.Adeclick, defaultAdeclickConfig())
	}
	if config.Loudnorm != defaultLoudnormConfig() {
		t.Errorf("Loudnorm = %+v, want %+v", config.Loudnorm, defaultLoudnormConfig())
	}
}

func TestBuildFilterSpec(t *testing.T) {
	t.Run("empty config produces empty filter spec", func(t *testing.T) {
		config := newTestConfig()
		spec := config.BuildFilterSpec()

		// With all filters disabled, spec should be empty
		if spec != "" {
			t.Errorf("BuildFilterSpec with all disabled should return empty, got: %s", spec)
		}
	})

	t.Run("resample enabled produces output format filters", func(t *testing.T) {
		config := newTestConfig()
		config.Resample.Enabled = true

		spec := config.BuildFilterSpec()

		// Output format filters should be present when Resample.Enabled
		if !strings.Contains(spec, "aformat=sample_rates=44100") {
			t.Error("Missing aformat output filter")
		}
		if !strings.Contains(spec, "asetnsamples=n=4096") {
			t.Error("Missing asetnsamples output filter")
		}

		// Processing filters should NOT be present when disabled
		processingFilters := []string{"highpass=", "anlmdn=", "agate=", "acompressor=", "alimiter="}
		for _, pf := range processingFilters {
			if strings.Contains(spec, pf) {
				t.Errorf("Disabled filter %q should not appear in spec", pf)
			}
		}
	})

	t.Run("default pass 2 chain omits pass 4 adeclick", func(t *testing.T) {
		spec := deriveEffectiveFilterConfig(DefaultFilterConfig()).BuildFilterSpec()

		if strings.Contains(spec, "adeclick=") {
			t.Errorf("BuildFilterSpec() emitted Pass 4 adeclick in Pass 2 chain: %s", spec)
		}
	})

	t.Run("enabled filters appear in spec", func(t *testing.T) {
		config := newTestConfig()
		// Enable specific filters for this test
		config.RumbleHighPass.Enabled = true
		config.SpeechGate.Enabled = true
		config.LevellingCompressor.Enabled = true
		config.Deesser.Enabled = true
		config.Resample.Enabled = true // Required for output format filters

		spec := config.BuildFilterSpec()

		// Verify enabled filters are present
		requiredFilters := []struct {
			prefix string
			name   string
		}{
			{"highpass=f=", "highpass"},
			{"agate=threshold=", "agate"},
			{"acompressor=threshold=", "acompressor"},
			{"deesser=i=", "deesser"},
			{"aformat=sample_rates=44100", "aformat (output)"},
		}

		for _, rf := range requiredFilters {
			if !strings.Contains(spec, rf.prefix) {
				t.Errorf("Missing %s filter (expected prefix: %q)", rf.name, rf.prefix)
			}
		}
	})

	t.Run("no NaN values in filter spec", func(t *testing.T) {
		config := newTestConfig()
		// Enable all filters to maximize coverage
		config.RumbleHighPass.Enabled = true
		config.SpeechGate.Enabled = true
		config.LevellingCompressor.Enabled = true
		config.Deesser.Enabled = true

		spec := config.BuildFilterSpec()

		if strings.Contains(spec, "NaN") {
			t.Errorf("Filter spec contains NaN: %s", spec)
		}
	})

	t.Run("no Inf values in filter spec", func(t *testing.T) {
		config := newTestConfig()
		// Enable all filters to maximize coverage
		config.RumbleHighPass.Enabled = true
		config.SpeechGate.Enabled = true
		config.LevellingCompressor.Enabled = true
		config.Deesser.Enabled = true

		spec := config.BuildFilterSpec()

		if strings.Contains(spec, "Inf") || strings.Contains(spec, "inf") {
			t.Errorf("Filter spec contains Inf: %s", spec)
		}
	})

	t.Run("disabled filters are excluded", func(t *testing.T) {
		config := newTestConfig()
		// All filters already disabled by newTestConfig()

		spec := config.BuildFilterSpec()

		// Should only contain output format filters
		if strings.Contains(spec, "highpass=") {
			t.Error("Disabled highpass filter present in spec")
		}
		if strings.Contains(spec, "afftdn=") {
			t.Error("Disabled afftdn filter present in spec")
		}
		if strings.Contains(spec, "agate=") {
			t.Error("Disabled agate filter present in spec")
		}
		if strings.Contains(spec, "acompressor=") {
			t.Error("Disabled acompressor filter present in spec")
		}
		if strings.Contains(spec, "alimiter=") {
			t.Error("Disabled alimiter filter present in spec")
		}

		// With Resample.Enabled=false (from newTestConfig), no aformat should be present
		// This is intentional - infrastructure filters are now controlled by flags
		if strings.Contains(spec, "aformat=sample_rates=44100") {
			t.Error("aformat should not appear when Resample.Enabled=false")
		}
	})

	t.Run("de-esser excluded when intensity is zero", func(t *testing.T) {
		config := newTestConfig()
		config.Deesser.Enabled = true
		config.Deesser.Intensity = 0.0 // Disabled by intensity

		spec := config.BuildFilterSpec()

		if strings.Contains(spec, "deesser=") {
			t.Error("De-esser should not appear when intensity is 0")
		}
	})

	t.Run("aformat appears after analysis filters when both enabled", func(t *testing.T) {
		config := newTestConfig()
		config.Analysis.Enabled = true
		config.Resample.Enabled = true
		// Use Pass2FilterOrder which has Analysis before Resample
		config.FilterOrder = Pass2FilterOrder

		spec := config.BuildFilterSpec()

		// Should contain ebur128 analysis filter
		if !strings.Contains(spec, "ebur128=") {
			t.Fatal("Missing ebur128 filter when Analysis.Enabled=true")
		}

		// ebur128 converts to f64 internally - aformat must come after to convert back to s16
		// The spec should have: ebur128=...,aformat=...,asetnsamples=...
		ebur128Idx := strings.Index(spec, "ebur128=")
		aformatIdx := strings.Index(spec, "aformat=sample_rates=44100")
		asetnsamplesIdx := strings.Index(spec, "asetnsamples=")

		if aformatIdx < ebur128Idx {
			t.Errorf("aformat must appear AFTER ebur128 (ebur128 outputs f64)\nSpec: %s", spec)
		}
		if asetnsamplesIdx < aformatIdx {
			t.Errorf("asetnsamples must appear AFTER aformat\nSpec: %s", spec)
		}
	})
}

func TestBuildFilterSpecBehaviourBaseline(t *testing.T) {
	tests := []struct {
		name   string
		config *EffectiveFilterConfig
		want   string
	}{
		{
			name:   "default pass 2 chain",
			config: DefaultEffectiveFilterConfig(),
			want: "aformat=channel_layouts=mono," +
				"highpass=f=80:poles=2:width_type=q:width=0.707:normalize=1:a=tdii," +
				"lowpass=f=20500:poles=2:width_type=q:width=0.707:normalize=1:a=tdii," +
				"anlmdn=s=0.00001:p=0.0060:r=0.0020:m=3," +
				"afftdn=nr=12:nt=w:tn=1," +
				"agate=threshold=0.010000:ratio=2.0:attack=5.00:release=200:range=0.1995:knee=3.0:detection=rms:makeup=1.0," +
				"acompressor=threshold=0.125893:ratio=3.0:attack=10:release=200:makeup=1.00:knee=4.0:detection=rms:mix=1.00," +
				"astats=metadata=1:measure_perchannel=all," +
				"aspectralstats=win_size=2048:win_func=hann:measure=all," +
				"ebur128=metadata=1:peak=sample+true:dualmono=true:target=-16," +
				"aformat=sample_rates=44100:channel_layouts=mono:sample_fmts=s16,asetnsamples=n=4096",
		},
		{
			name: "low-pass disabled",
			config: func() *EffectiveFilterConfig {
				config := newTestConfig()
				config.BandlimitLowPass.Enabled = false
				config.FilterOrder = []FilterID{FilterBandlimitLowPass}
				return config
			}(),
			want: "",
		},
		{
			name: "low-pass enabled",
			config: func() *EffectiveFilterConfig {
				config := newTestConfig()
				config.BandlimitLowPass.Enabled = true
				config.BandlimitLowPass.Frequency = 14500.0
				config.BandlimitLowPass.Poles = 1
				config.BandlimitLowPass.Width = 0.5
				config.BandlimitLowPass.Mix = 0.75
				config.BandlimitLowPass.Transform = "zdf"
				config.FilterOrder = []FilterID{FilterBandlimitLowPass}
				return config
			}(),
			want: "lowpass=f=14500:poles=1:width_type=q:width=0.500:normalize=1:a=zdf:m=0.75",
		},
		{
			name: "gate tuned",
			config: func() *EffectiveFilterConfig {
				config := newTestConfig()
				config.SpeechGate.Enabled = true
				config.SpeechGate.Threshold = 0.003162
				config.SpeechGate.Ratio = 3.5
				config.SpeechGate.Attack = 10.5
				config.SpeechGate.Release = 425
				config.SpeechGate.Range = 0.0316
				config.SpeechGate.Knee = 4.5
				config.SpeechGate.Detection = "peak"
				config.SpeechGate.Makeup = 1.2
				config.FilterOrder = []FilterID{FilterSpeechGate}
				return config
			}(),
			want: "agate=threshold=0.003162:ratio=3.5:attack=10.50:release=425:range=0.0316:knee=4.5:detection=peak:makeup=1.2",
		},
		{
			name: "levelling compressor high-crest tuned values",
			config: func() *EffectiveFilterConfig {
				config := newTestConfig()
				config.LevellingCompressor.Enabled = true
				config.LevellingCompressor.Threshold = -30.0
				config.LevellingCompressor.Ratio = 4.0
				config.LevellingCompressor.Attack = 10
				config.LevellingCompressor.Release = 60
				config.LevellingCompressor.Makeup = 0
				config.LevellingCompressor.Knee = 6.0
				config.LevellingCompressor.Mix = 0.85
				config.FilterOrder = []FilterID{FilterLevellingCompressor}
				return config
			}(),
			want: "acompressor=threshold=0.031623:ratio=4.0:attack=10:release=60:makeup=1.00:knee=6.0:detection=rms:mix=0.85",
		},
		{
			name: "noise-remove afftdn disabled",
			config: func() *EffectiveFilterConfig {
				config := newTestConfig()
				config.NoiseReduction.Enabled = true
				config.NoiseReduction.AfftdnEnabled = false
				config.FilterOrder = []FilterID{FilterNoiseReduction}
				return config
			}(),
			want: "anlmdn=s=0.00001:p=0.0060:r=0.0058:m=11",
		},
		{
			name: "noise-remove afftdn enabled",
			config: func() *EffectiveFilterConfig {
				config := newTestConfig()
				config.NoiseReduction.Enabled = true
				config.NoiseReduction.AfftdnEnabled = true
				config.FilterOrder = []FilterID{FilterNoiseReduction}
				return config
			}(),
			want: "anlmdn=s=0.00001:p=0.0060:r=0.0058:m=11," +
				"afftdn=nr=12:nt=w:tn=1",
		},
		{
			name: "de-esser disabled",
			config: func() *EffectiveFilterConfig {
				config := newTestConfig()
				config.Deesser.Enabled = true
				config.Deesser.Intensity = 0
				config.FilterOrder = []FilterID{FilterDeesser}
				return config
			}(),
			want: "",
		},
		{
			name: "de-esser enabled",
			config: func() *EffectiveFilterConfig {
				config := newTestConfig()
				config.Deesser.Enabled = true
				config.Deesser.Intensity = 0.6
				config.Deesser.Amount = 0.4
				config.Deesser.Frequency = 0.7
				config.FilterOrder = []FilterID{FilterDeesser}
				return config
			}(),
			want: "deesser=i=0.60:m=0.40:f=0.70",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.config.BuildFilterSpec()
			if got != tt.want {
				t.Errorf("BuildFilterSpec() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDefaultFilterConfigSeedOwnershipBoundary(t *testing.T) {
	assertSeedConfigTypeCannotOwnPerFileState(t, reflect.TypeOf(DefaultFilterConfig()))
}

func assertSeedConfigTypeCannotOwnPerFileState(t *testing.T, typ reflect.Type) {
	t.Helper()

	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		t.Fatalf("seed config type = %s, want struct or pointer to struct", typ)
	}

	for _, name := range perFileStateFieldNames() {
		if field, ok := typ.FieldByName(name); ok {
			t.Errorf("seed config type %s owns per-file state field %s of type %s", typ.Name(), name, field.Type)
		}
	}
}

func perFileStateFieldNames() []string {
	return []string{
		"Pass",
		"Measurements",
		"OutputAnalysisEnabled",
		"BandlimitLPReason",
		"SpeechGateDepthDB",
		"SpeechGateDynamicRange",
		"SpeechGateQuietSpeechEstimate",
		"SpeechGateSpeechSeparation",
		"SpeechGateSpeechHeadroom",
		"SpeechGateThresholdUnclamped",
		"SpeechGateClampReason",
	}
}

func TestBuildRumbleHighpassFilter(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		freq    float64
		wantIn  string
	}{
		{
			// HP is a fixed 80 Hz corner (no adaptation); only this and the
			// disabled case are reachable in production.
			name:    "fixed 80 Hz corner",
			enabled: true,
			freq:    80.0,
			wantIn:  "highpass=f=80:",
		},
		{
			name:    "disabled returns empty",
			enabled: false,
			freq:    80.0,
			wantIn:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestConfig()
			config.RumbleHighPass.Enabled = tt.enabled
			config.RumbleHighPass.Frequency = tt.freq

			spec := config.buildRumbleHighpassFilter()

			if !tt.enabled {
				if spec != "" {
					t.Errorf("buildHighpassFilter() = %q, want empty when disabled", spec)
				}
				return
			}

			if !strings.Contains(spec, tt.wantIn) {
				t.Errorf("buildHighpassFilter() = %q, want to contain %q", spec, tt.wantIn)
			}
		})
	}
}

func TestBuildSpeechGateFilter(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		threshold float64
		wantIn    string
	}{
		{
			name:      "typical threshold",
			enabled:   true,
			threshold: 0.01, // -40dB
			wantIn:    "agate=threshold=0.010",
		},
		{
			name:      "quiet environment threshold",
			enabled:   true,
			threshold: 0.001, // -60dB
			wantIn:    "agate=threshold=0.001",
		},
		{
			name:      "noisy environment threshold",
			enabled:   true,
			threshold: 0.05, // ~-26dB
			wantIn:    "agate=threshold=0.050",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestConfig()
			config.SpeechGate.Enabled = tt.enabled
			config.SpeechGate.Threshold = tt.threshold

			spec := config.buildSpeechGateFilter()

			if !strings.Contains(spec, tt.wantIn) {
				t.Errorf("buildSpeechGateFilter() = %q, want to contain %q", spec, tt.wantIn)
			}

			// Verify detection mode is RMS (important for speech)
			if !strings.Contains(spec, "detection=rms") {
				t.Error("buildSpeechGateFilter() should use RMS detection for speech")
			}
		})
	}

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.SpeechGate.Enabled = false

		spec := config.buildSpeechGateFilter()
		if spec != "" {
			t.Errorf("buildSpeechGateFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestBuildBandlimitLowPassFilter(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		freq    float64
		wantIn  string
	}{
		{
			name:    "ultrasonic rejection",
			enabled: true,
			freq:    16000.0,
			wantIn:  "lowpass=f=16000:",
		},
		{
			name:    "HF noise filter",
			enabled: true,
			freq:    12000.0,
			wantIn:  "lowpass=f=12000:",
		},
		{
			name:    "high rolloff adjustment",
			enabled: true,
			freq:    14500.0,
			wantIn:  "lowpass=f=14500:",
		},
		{
			name:    "disabled returns empty",
			enabled: false,
			freq:    16000.0,
			wantIn:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestConfig()
			config.BandlimitLowPass.Enabled = tt.enabled
			config.BandlimitLowPass.Frequency = tt.freq

			spec := config.buildBandlimitLowPassFilter()

			if !tt.enabled {
				if spec != "" {
					t.Errorf("buildBandlimitLowPassFilter() = %q, want empty when disabled", spec)
				}
				return
			}

			if !strings.Contains(spec, tt.wantIn) {
				t.Errorf("buildBandlimitLowPassFilter() = %q, want to contain %q", spec, tt.wantIn)
			}
		})
	}
}

func TestBuildLevellingCompressorFilter(t *testing.T) {
	t.Run("typical podcast compression", func(t *testing.T) {
		config := newTestConfig()
		config.LevellingCompressor.Enabled = true
		config.LevellingCompressor.Threshold = -20.0
		config.LevellingCompressor.Ratio = 2.5

		spec := config.buildLevellingCompressorFilter()

		wantIn := []string{
			"acompressor=threshold=",
			"ratio=2.5",
			"detection=rms",
		}

		for _, want := range wantIn {
			if !strings.Contains(spec, want) {
				t.Errorf("buildLevellingCompressorFilter() = %q, want to contain %q", spec, want)
			}
		}

		// Threshold should be converted to linear (not raw dB)
		// -20dB in linear is 0.1, so we should NOT see "threshold=-20"
		if strings.Contains(spec, "threshold=-") {
			t.Error("buildLevellingCompressorFilter() should convert threshold to linear, not use raw dB")
		}
	})

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.LevellingCompressor.Enabled = false

		spec := config.buildLevellingCompressorFilter()
		if spec != "" {
			t.Errorf("buildLevellingCompressorFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestBuildDeesserFilter(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		intensity float64
		wantIn    string
		wantEmpty bool
	}{
		{
			name:      "moderate de-essing",
			enabled:   true,
			intensity: 0.5,
			wantIn:    "deesser=i=0.50",
		},
		{
			name:      "aggressive de-essing",
			enabled:   true,
			intensity: 0.8,
			wantIn:    "deesser=i=0.80",
		},
		{
			name:      "disabled via flag",
			enabled:   false,
			intensity: 0.5,
			wantEmpty: true,
		},
		{
			name:      "disabled via zero intensity",
			enabled:   true,
			intensity: 0.0,
			wantEmpty: true,
		},
		{
			name:      "disabled via negative intensity",
			enabled:   true,
			intensity: -0.1,
			wantEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestConfig()
			config.Deesser.Enabled = tt.enabled
			config.Deesser.Intensity = tt.intensity

			spec := config.buildDeesserFilter()

			if tt.wantEmpty {
				if spec != "" {
					t.Errorf("buildDeesserFilter() = %q, want empty", spec)
				}
				return
			}

			if !strings.Contains(spec, tt.wantIn) {
				t.Errorf("buildDeesserFilter() = %q, want to contain %q", spec, tt.wantIn)
			}
		})
	}
}

func TestBuildNoiseReductionFilter(t *testing.T) {
	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.NoiseReduction.Enabled = false

		spec := config.buildNoiseReductionFilter()
		if spec != "" {
			t.Errorf("buildNoiseReductionFilter() = %q, want empty when disabled", spec)
		}
	})

	t.Run("enabled produces anlmdn+afftdn chain", func(t *testing.T) {
		config := newTestConfig()
		config.NoiseReduction.Enabled = true

		spec := config.buildNoiseReductionFilter()

		// Must contain anlmdn filter
		if !strings.Contains(spec, "anlmdn=") {
			t.Errorf("buildNoiseReductionFilter() missing anlmdn filter, got: %s", spec)
		}

		// Must contain afftdn filter
		if !strings.Contains(spec, "afftdn=") {
			t.Errorf("buildNoiseReductionFilter() missing afftdn filter, got: %s", spec)
		}

		// Must NOT contain compand
		if strings.Contains(spec, "compand=") {
			t.Errorf("buildNoiseReductionFilter() must not contain compand, got: %s", spec)
		}

		// anlmdn must come before afftdn
		anlmdnIdx := strings.Index(spec, "anlmdn=")
		afftdnIdx := strings.Index(spec, "afftdn=")
		if afftdnIdx < anlmdnIdx {
			t.Errorf("afftdn must come after anlmdn in filter chain\nGot: %s", spec)
		}
	})

	t.Run("anlmdn parameters formatted correctly", func(t *testing.T) {
		config := newTestConfig()
		config.NoiseReduction.Enabled = true
		config.NoiseReduction.Strength = 0.00001
		config.NoiseReduction.PatchSec = 0.006
		config.NoiseReduction.ResearchSec = 0.0058
		config.NoiseReduction.Smooth = 11.0

		spec := config.buildNoiseReductionFilter()

		expected := []string{
			"s=0.00001", // strength
			"p=0.0060",  // patch
			"r=0.0058",  // research
			"m=11",      // smooth
		}

		for _, e := range expected {
			if !strings.Contains(spec, e) {
				t.Errorf("buildNoiseReductionFilter() missing %q\nGot: %s", e, spec)
			}
		}
	})

	t.Run("afftdn clause fixed at nr=12", func(t *testing.T) {
		config := newTestConfig()
		config.NoiseReduction.Enabled = true
		config.NoiseReduction.AfftdnEnabled = true

		spec := config.buildNoiseReductionFilter()

		if !strings.Contains(spec, "afftdn=nr=12:nt=w:tn=1") {
			t.Errorf("buildNoiseReductionFilter() missing fixed afftdn clause\nGot: %s", spec)
		}
	})

	t.Run("afftdn disabled produces anlmdn-only spec", func(t *testing.T) {
		config := newTestConfig()
		config.NoiseReduction.Enabled = true
		config.NoiseReduction.AfftdnEnabled = false

		spec := config.buildNoiseReductionFilter()

		if !strings.Contains(spec, "anlmdn=") {
			t.Errorf("afftdn-disabled spec missing anlmdn, got: %s", spec)
		}
		if strings.Contains(spec, "afftdn=") {
			t.Errorf("afftdn-disabled spec should not contain afftdn, got: %s", spec)
		}
		if strings.Contains(spec, "compand=") {
			t.Errorf("afftdn-disabled spec should not contain compand, got: %s", spec)
		}
	})

	t.Run("anlmdn appears before afftdn", func(t *testing.T) {
		config := newTestConfig()
		config.NoiseReduction.Enabled = true
		config.NoiseReduction.AfftdnEnabled = true

		spec := config.buildNoiseReductionFilter()

		assertFullbenchSpecContains(t, spec, []string{"anlmdn=", "afftdn="})
		assertFullbenchSpecOrder(t, spec, []string{"anlmdn=", "afftdn="})
		if strings.Contains(spec, "aformat=sample_rates=") {
			t.Errorf("noise-removal sub-block should not emit any aformat sample-rate clauses\nSpec: %s", spec)
		}
	})
}

func TestBuildAdeclickFilter(t *testing.T) {
	t.Run("default config emits production clause", func(t *testing.T) {
		config := DefaultEffectiveFilterConfig()

		spec := config.buildAdeclickFilter()

		const want = "adeclick=t=1.7:w=55:o=50:m=s"
		if spec != want {
			t.Errorf("buildAdeclickFilter() = %q, want %q", spec, want)
		}
	})

	t.Run("custom parameters", func(t *testing.T) {
		config := newTestConfig()
		config.Adeclick.Enabled = true
		config.Adeclick.Threshold = 2.0
		config.Adeclick.Window = 100.0
		config.Adeclick.Overlap = 50.0
		config.Adeclick.Method = "s"

		spec := config.buildAdeclickFilter()

		wantIn := []string{
			"adeclick=",
			"t=2.0",
			"w=100",
			"o=50",
			"m=s",
		}
		for _, want := range wantIn {
			if !strings.Contains(spec, want) {
				t.Errorf("buildAdeclickFilter() = %q, want to contain %q", spec, want)
			}
		}
	})

	t.Run("empty method omits m segment", func(t *testing.T) {
		config := newTestConfig()
		config.Adeclick.Enabled = true
		config.Adeclick.Threshold = 2.0
		config.Adeclick.Window = 55.0
		config.Adeclick.Overlap = 50.0
		config.Adeclick.Method = ""

		spec := config.buildAdeclickFilter()

		const want = "adeclick=t=2.0:w=55:o=50"
		if spec != want {
			t.Errorf("buildAdeclickFilter() = %q, want %q", spec, want)
		}
		if strings.Contains(spec, ":m=") {
			t.Errorf("buildAdeclickFilter() = %q, must omit :m= segment when method is empty", spec)
		}
	})

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.Adeclick.Enabled = false

		spec := config.buildAdeclickFilter()
		if spec != "" {
			t.Errorf("buildAdeclickFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestFilterOrderRespected(t *testing.T) {
	config := newTestConfig()
	// Enable filters that appear at start and end
	config.RumbleHighPass.Enabled = true
	config.SpeechGate.Enabled = true
	config.Deesser.Enabled = true
	config.Deesser.Intensity = 0.5
	config.Resample.Enabled = true // Required for aformat output filter
	config.FilterOrder = Pass2FilterOrder

	spec := config.BuildFilterSpec()

	// Find positions of key filters
	highpassPos := strings.Index(spec, "highpass=")
	gatePos := strings.Index(spec, "agate=")
	deesserPos := strings.Index(spec, "deesser=")
	aformatPos := strings.Index(spec, "aformat=sample_rates=")

	// Verify order: highpass < gate < deesser < aformat
	if highpassPos >= gatePos {
		t.Errorf("highpass (pos %d) should come before agate (pos %d)", highpassPos, gatePos)
	}
	if gatePos >= deesserPos {
		t.Errorf("agate (pos %d) should come before deesser (pos %d)", gatePos, deesserPos)
	}
	if deesserPos >= aformatPos {
		t.Errorf("deesser (pos %d) should come before aformat (pos %d)", deesserPos, aformatPos)
	}
}

func TestDeriveAdaptiveFilterResultDeepCopiesFilterOrder(t *testing.T) {
	base := DefaultFilterConfig()
	base.FilterOrder = []FilterID{FilterDeesser, FilterAnalysis}
	base.Analysis.RoomToneScanDuration = 2 * time.Second
	base.Resample.SampleRate = 48000

	adaptive := deriveAdaptiveFilterResult(base)
	if adaptive == nil {
		t.Fatal("deriveAdaptiveFilterResult returned nil")
	}
	if !reflect.DeepEqual(adaptive.FilterOrder, base.FilterOrder) {
		t.Errorf("FilterOrder = %v, want %v", adaptive.FilterOrder, base.FilterOrder)
	}

	adaptive.FilterOrder[0] = FilterDownmix
	if base.FilterOrder[0] == FilterDownmix {
		t.Fatal("adaptive FilterOrder mutation changed base FilterOrder")
	}
	if adaptive.Analysis.RoomToneScanDuration != base.Analysis.RoomToneScanDuration {
		t.Errorf("Analysis.RoomToneScanDuration = %s, want %s",
			adaptive.Analysis.RoomToneScanDuration, base.Analysis.RoomToneScanDuration)
	}
	if adaptive.Resample.SampleRate != base.Resample.SampleRate {
		t.Errorf("Resample.SampleRate = %d, want %d",
			adaptive.Resample.SampleRate, base.Resample.SampleRate)
	}
	adaptive.Resample.SampleRate = 32000
	if base.Resample.SampleRate == 32000 {
		t.Fatal("adaptive typed Resample mutation changed base Resample")
	}
}

func TestCloneFilterDefaultsCopiesTypedFamilies(t *testing.T) {
	base := DefaultFilterConfig()
	base.Analysis.RoomToneScanDuration = 3 * time.Second
	base.NoiseReduction.AfftdnEnabled = false
	base.FilterOrder = []FilterID{FilterAnalysis, FilterDeesser}

	clone := cloneFilterDefaults(&base.filterConfigDefaults)
	if clone.Analysis.RoomToneScanDuration != base.Analysis.RoomToneScanDuration {
		t.Errorf("Analysis.RoomToneScanDuration = %s, want %s",
			clone.Analysis.RoomToneScanDuration, base.Analysis.RoomToneScanDuration)
	}
	if clone.NoiseReduction.AfftdnEnabled != base.NoiseReduction.AfftdnEnabled {
		t.Errorf("NoiseReduction.AfftdnEnabled = %v, want %v",
			clone.NoiseReduction.AfftdnEnabled, base.NoiseReduction.AfftdnEnabled)
	}
	if !reflect.DeepEqual(clone.FilterOrder, base.FilterOrder) {
		t.Errorf("FilterOrder = %v, want %v", clone.FilterOrder, base.FilterOrder)
	}

	clone.FilterOrder[0] = FilterDownmix
	if base.FilterOrder[0] == FilterDownmix {
		t.Fatal("clone FilterOrder mutation changed base FilterOrder")
	}
}

func TestAssembleEffectiveFilterConfig(t *testing.T) {
	base := DefaultFilterConfig()
	base.FilterOrder = []FilterID{FilterDeesser, FilterAnalysis}
	base.Loudnorm.TargetI = -18.0
	base.Analysis.RoomToneScanDuration = 2 * time.Second

	adaptive := deriveAdaptiveFilterResult(base)
	adaptive.RumbleHighPass.Frequency = 65.0
	adaptive.NoiseReduction.AfftdnEnabled = false
	adaptive.FilterOrder = []FilterID{FilterDownmix}

	effective := assembleEffectiveFilterConfig(base, adaptive)
	if effective == nil {
		t.Fatal("assembleEffectiveFilterConfig returned nil")
	}
	if effective.RumbleHighPass.Frequency != adaptive.RumbleHighPass.Frequency {
		t.Errorf("RumbleHighPass.Frequency = %.1f, want adaptive %.1f",
			effective.RumbleHighPass.Frequency, adaptive.RumbleHighPass.Frequency)
	}
	if effective.NoiseReduction.AfftdnEnabled {
		t.Error("NoiseReduction.AfftdnEnabled = true, want adaptive false")
	}
	if effective.Loudnorm.TargetI != base.Loudnorm.TargetI {
		t.Errorf("Loudnorm.TargetI = %.1f, want base %.1f", effective.Loudnorm.TargetI, base.Loudnorm.TargetI)
	}
	if effective.Analysis.RoomToneScanDuration != base.Analysis.RoomToneScanDuration {
		t.Errorf("Analysis.RoomToneScanDuration = %s, want base %s",
			effective.Analysis.RoomToneScanDuration, base.Analysis.RoomToneScanDuration)
	}
	if !reflect.DeepEqual(effective.FilterOrder, base.FilterOrder) {
		t.Errorf("FilterOrder = %v, want base order %v", effective.FilterOrder, base.FilterOrder)
	}

	effective.FilterOrder[0] = FilterDownmix
	if base.FilterOrder[0] == FilterDownmix {
		t.Fatal("effective FilterOrder mutation changed base FilterOrder")
	}
	if adaptive.FilterOrder[0] != FilterDownmix {
		t.Fatal("effective FilterOrder mutation changed adaptive FilterOrder")
	}

	assertNoStaleEffectiveConfigFields(t)
}

func TestDeriveEffectiveFilterConfig(t *testing.T) {
	base := DefaultFilterConfig()
	base.FilterOrder = []FilterID{FilterDeesser, FilterAnalysis}
	base.Loudnorm.TargetI = -18.0
	base.Analysis.RoomToneScanDuration = 2 * time.Second
	base.NoiseReduction.AfftdnNoiseReduction = 9.0

	derived := deriveEffectiveFilterConfig(base)
	if derived == nil {
		t.Fatal("deriveEffectiveFilterConfig returned nil")
	}
	assertNoStaleEffectiveConfigFields(t)
	if !reflect.DeepEqual(derived.FilterOrder, base.FilterOrder) {
		t.Errorf("FilterOrder = %v, want %v", derived.FilterOrder, base.FilterOrder)
	}
	derived.FilterOrder[0] = FilterDownmix
	if base.FilterOrder[0] == FilterDownmix {
		t.Error("derived FilterOrder mutation changed base FilterOrder")
	}

	if derived.Loudnorm.TargetI != base.Loudnorm.TargetI {
		t.Errorf("Loudnorm.TargetI = %.1f, want %.1f", derived.Loudnorm.TargetI, base.Loudnorm.TargetI)
	}
	if derived.Analysis.RoomToneScanDuration != base.Analysis.RoomToneScanDuration {
		t.Errorf("Analysis.RoomToneScanDuration = %s, want %s",
			derived.Analysis.RoomToneScanDuration, base.Analysis.RoomToneScanDuration)
	}
	if derived.NoiseReduction.AfftdnNoiseReduction != base.NoiseReduction.AfftdnNoiseReduction {
		t.Errorf("NoiseReduction.AfftdnNoiseReduction = %.1f, want %.1f",
			derived.NoiseReduction.AfftdnNoiseReduction, base.NoiseReduction.AfftdnNoiseReduction)
	}

	if !reflect.DeepEqual(base.FilterOrder, []FilterID{FilterDeesser, FilterAnalysis}) ||
		base.Loudnorm.TargetI != -18.0 ||
		base.Analysis.RoomToneScanDuration != 2*time.Second ||
		base.NoiseReduction.AfftdnNoiseReduction != 9.0 {
		t.Error("deriveEffectiveFilterConfig mutated caller-owned defaults")
	}
}

func assertNoStaleEffectiveConfigFields(t *testing.T) {
	t.Helper()

	for _, typ := range []reflect.Type{
		reflect.TypeFor[filterConfigDefaults](),
		reflect.TypeFor[EffectiveFilterConfig](),
	} {
		assertNoStaleConfigFields(t, typ)
	}
}

func assertNoStaleConfigFields(t *testing.T, configType reflect.Type) {
	t.Helper()

	for _, field := range staleFlatConfigFieldNames() {
		if _, ok := configType.FieldByName(field); ok {
			t.Errorf("%s contains stale field %s", configType.Name(), field)
		}
	}
}

func staleFlatConfigFieldNames() []string {
	return []string{
		"Pass",
		"Measurements",
		"OutputAnalysisEnabled",
		"DownmixEnabled",
		"AnalysisEnabled",
		"SilenceScanDuration",
		"ResampleEnabled",
		"ResampleSampleRate",
		"ResampleFormat",
		"ResampleFrameSize",
		"RumbleHPEnabled",
		"RumbleHPFreq",
		"RumbleHPPoles",
		"RumbleHPWidth",
		"RumbleHPMix",
		"RumbleHPTransform",
		"BandlimitLPEnabled",
		"BandlimitLPFreq",
		"BandlimitLPPoles",
		"BandlimitLPWidth",
		"BandlimitLPMix",
		"BandlimitLPTransform",
		"NoiseReductionEnabled",
		"NoiseReductionStrength",
		"NoiseReductionPatchSec",
		"NoiseReductionResearchSec",
		"NoiseReductionSmooth",
		"SpeechGateEnabled",
		"SpeechGateThreshold",
		"SpeechGateRatio",
		"SpeechGateAttack",
		"SpeechGateRelease",
		"SpeechGateRange",
		"SpeechGateKnee",
		"SpeechGateMakeup",
		"SpeechGateDetection",
		"LevellingCompressorEnabled",
		"LevellingCompressorThreshold",
		"LevellingCompressorRatio",
		"LevellingCompressorAttack",
		"LevellingCompressorRelease",
		"LevellingCompressorMakeup",
		"LevellingCompressorKnee",
		"LevellingCompressorMix",
		"DeessEnabled",
		"DeessIntensity",
		"DeessAmount",
		"DeessFreq",
		"AdeclickEnabled",
		"AdeclickThreshold",
		"AdeclickWindow",
		"AdeclickOverlap",
		"AdeclickMethod",
		"TargetI",
		"TargetTP",
		"TargetLRA",
		"LoudnormEnabled",
		"LoudnormTargetI",
		"LoudnormTargetTP",
		"LoudnormTargetLRA",
		"LoudnormDualMono",
		"LoudnormLinear",
		"BandlimitLPReason",
		"SpeechGateClampReason",
	}
}

func TestCloneForWorkerIsolatesStateAcrossClones(t *testing.T) {
	base := DefaultFilterConfig()
	base.FilterOrder = []FilterID{FilterDownmix, FilterAnalysis, FilterResample}

	var sinkA, sinkB []string
	cloneA := base.CloneForWorker(func(format string, args ...any) {
		sinkA = append(sinkA, fmt.Sprintf(format, args...))
	})
	cloneB := base.CloneForWorker(func(format string, args ...any) {
		sinkB = append(sinkB, fmt.Sprintf(format, args...))
	})

	// (a) FilterOrder independence: clones and base must not share a backing array.
	baseOrderBefore := append([]FilterID(nil), base.FilterOrder...)
	cloneBOrderBefore := append([]FilterID(nil), cloneB.FilterOrder...)

	cloneA.FilterOrder[0] = FilterDeesser                                      // overwrite element
	cloneA.FilterOrder = append(cloneA.FilterOrder, FilterLevellingCompressor) // grow slice

	if !reflect.DeepEqual(base.FilterOrder, baseOrderBefore) {
		t.Errorf("clone A FilterOrder mutation changed base: got %v, want %v",
			base.FilterOrder, baseOrderBefore)
	}
	if !reflect.DeepEqual(cloneB.FilterOrder, cloneBOrderBefore) {
		t.Errorf("clone A FilterOrder mutation changed clone B: got %v, want %v",
			cloneB.FilterOrder, cloneBOrderBefore)
	}

	// Mutating clone B must not bleed into clone A or the base either.
	cloneA.FilterOrder = append([]FilterID(nil), cloneA.FilterOrder...)
	cloneAOrderBefore := append([]FilterID(nil), cloneA.FilterOrder...)
	cloneB.FilterOrder[1] = FilterSpeechGate
	if !reflect.DeepEqual(base.FilterOrder, baseOrderBefore) {
		t.Errorf("clone B FilterOrder mutation changed base: got %v, want %v",
			base.FilterOrder, baseOrderBefore)
	}
	if !reflect.DeepEqual(cloneA.FilterOrder, cloneAOrderBefore) {
		t.Errorf("clone B FilterOrder mutation changed clone A: got %v, want %v",
			cloneA.FilterOrder, cloneAOrderBefore)
	}

	// (b) Logger independence: each clone's logger writes only to its own sink.
	cloneA.logger.Logf("from A %d", 1)
	if got := len(sinkA); got != 1 {
		t.Fatalf("clone A logger wrote %d lines to sink A, want 1", got)
	}
	if got := len(sinkB); got != 0 {
		t.Fatalf("clone A logger leaked %d lines into sink B, want 0", got)
	}

	cloneB.logger.Logf("from B %d", 2)
	if got := len(sinkB); got != 1 {
		t.Fatalf("clone B logger wrote %d lines to sink B, want 1", got)
	}
	if got := len(sinkA); got != 1 {
		t.Fatalf("clone B logger leaked into sink A: sink A now has %d lines, want 1", got)
	}

	if sinkA[0] != "from A 1" {
		t.Errorf("sink A = %q, want %q", sinkA[0], "from A 1")
	}
	if sinkB[0] != "from B 2" {
		t.Errorf("sink B = %q, want %q", sinkB[0], "from B 2")
	}
}

func TestDbToLinear(t *testing.T) {
	// Test 6 from PLAN.md: dB/Linear conversion accuracy
	tests := []struct {
		name       string
		db         float64
		wantLinear float64
		tolerance  float64
	}{
		{"0dB equals unity", 0, 1.0, 0.0001},
		{"-6dB approximately halves", -6, 0.5012, 0.001},
		{"-20dB equals 0.1", -20, 0.1, 0.001},
		{"-40dB equals 0.01", -40, 0.01, 0.0001},
		{"-60dB equals 0.001", -60, 0.001, 0.00001},
		{"+6dB approximately doubles", 6, 1.995, 0.001},
		{"+20dB equals 10.0", 20, 10.0, 0.01},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DbToLinear(tt.db)
			diff := math.Abs(got - tt.wantLinear)
			if diff > tt.tolerance {
				t.Errorf("DbToLinear(%.1f) = %.6f, want %.6f (±%.6f)",
					tt.db, got, tt.wantLinear, tt.tolerance)
			}
		})
	}
}

func TestDbToLinearFormula(t *testing.T) {
	// Verify the formula is correct: 10^(dB/20)
	// This is the standard amplitude conversion
	testCases := []float64{0, -3, -6, -12, -20, -40, -60, 3, 6, 12, 20}

	for _, db := range testCases {
		t.Run(fmt.Sprintf("%.0fdB", db), func(t *testing.T) {
			got := DbToLinear(db)
			want := math.Pow(10, db/20.0)
			if math.Abs(got-want) > 0.0000001 {
				t.Errorf("DbToLinear(%.1f) = %.10f, want %.10f (exact formula)", db, got, want)
			}
		})
	}
}

func TestDecibelLinearAmplitudeWrappers(t *testing.T) {
	t.Run("dB to linear delegates to DbToLinear", func(t *testing.T) {
		testCases := []struct {
			name string
			db   float64
		}{
			{"levelling compressor threshold", -20.0},
			{"levelling compressor makeup", 3.0},
			{"speech gate threshold", -40.0},
			{"speech gate range", -27.0},
			{"limiter ceiling", -12.4},
		}

		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				got := Decibels(tt.db).LinearAmplitude().Float64()
				want := DbToLinear(tt.db)
				if math.Abs(got-want) > 0.0000001 {
					t.Errorf("Decibels(%.1f).LinearAmplitude() = %.10f, want %.10f", tt.db, got, want)
				}
			})
		}
	})

	t.Run("linear to dB delegates to LinearToDb", func(t *testing.T) {
		testCases := []struct {
			name   string
			linear float64
		}{
			{"speech gate diagnostics threshold", 0.01},
			{"speech gate diagnostics range", 0.044668},
			{"silent floor", 0.0},
		}

		for _, tt := range testCases {
			t.Run(tt.name, func(t *testing.T) {
				got := LinearAmplitude(tt.linear).Decibels().Float64()
				want := LinearToDb(tt.linear)
				if math.Abs(got-want) > 0.0000001 {
					t.Errorf("LinearAmplitude(%.6f).Decibels() = %.10f, want %.10f", tt.linear, got, want)
				}
			})
		}
	})
}

// Tests for infrastructure filters (Downmix, Analysis, Resample)

func TestBuildDownmixFilter(t *testing.T) {
	t.Run("enabled returns aformat mono", func(t *testing.T) {
		config := newTestConfig()
		config.Downmix.Enabled = true

		result := config.buildDownmixFilter()

		if result != "aformat=channel_layouts=mono" {
			t.Errorf("buildDownmixFilter() = %q, want %q", result, "aformat=channel_layouts=mono")
		}
	})

	t.Run("disabled returns empty string", func(t *testing.T) {
		config := newTestConfig()
		config.Downmix.Enabled = false

		result := config.buildDownmixFilter()

		if result != "" {
			t.Errorf("buildDownmixFilter() = %q, want empty string", result)
		}
	})
}

func TestBuildAnalysisFilter(t *testing.T) {
	t.Run("enabled returns astats+aspectralstats+ebur128 chain", func(t *testing.T) {
		config := newTestConfig()
		config.Analysis.Enabled = true
		config.Loudnorm.TargetI = -16.0

		result := config.buildAnalysisFilter()

		// Check for key components
		if !strings.Contains(result, "astats=metadata=1") {
			t.Error("buildAnalysisFilter() missing astats filter")
		}
		if !strings.Contains(result, "measure_perchannel=all") {
			t.Error("buildAnalysisFilter() should request all astats measurements")
		}
		if !strings.Contains(result, "aspectralstats=win_size=2048") {
			t.Error("buildAnalysisFilter() missing aspectralstats filter")
		}
		if !strings.Contains(result, "measure=all") {
			t.Error("buildAnalysisFilter() should collect all spectral measurements")
		}
		if !strings.Contains(result, "ebur128=metadata=1:peak=sample+true:dualmono=true") {
			t.Errorf("buildAnalysisFilter() missing ebur128 filter with dualmono=true, got %q", result)
		}
		if !strings.Contains(result, "target=-16") {
			t.Errorf("buildAnalysisFilter() missing target=-16, got %q", result)
		}
	})

	t.Run("uses configured Loudnorm.TargetI", func(t *testing.T) {
		config := newTestConfig()
		config.Analysis.Enabled = true
		config.Loudnorm.TargetI = -14.0

		result := config.buildAnalysisFilter()

		if !strings.Contains(result, "target=-14") {
			t.Errorf("buildAnalysisFilter() should use Loudnorm.TargetI=-14, got %q", result)
		}
	})

	t.Run("disabled returns empty string", func(t *testing.T) {
		config := newTestConfig()
		config.Analysis.Enabled = false

		result := config.buildAnalysisFilter()

		if result != "" {
			t.Errorf("buildAnalysisFilter() = %q, want empty string", result)
		}
	})
}

func TestBuildResampleFilter(t *testing.T) {
	t.Run("enabled returns aformat+asetnsamples with default params", func(t *testing.T) {
		config := newTestConfig()
		config.Resample.Enabled = true
		config.Resample.SampleRate = 44100
		config.Resample.Format = "s16"
		config.Resample.FrameSize = 4096

		result := config.buildResampleFilter()

		expected := "aformat=sample_rates=44100:channel_layouts=mono:sample_fmts=s16,asetnsamples=n=4096"
		if result != expected {
			t.Errorf("buildResampleFilter() = %q, want %q", result, expected)
		}
	})

	t.Run("uses configured parameters", func(t *testing.T) {
		config := newTestConfig()
		config.Resample.Enabled = true
		config.Resample.SampleRate = 48000
		config.Resample.Format = "s32"
		config.Resample.FrameSize = 2048

		result := config.buildResampleFilter()

		expected := "aformat=sample_rates=48000:channel_layouts=mono:sample_fmts=s32,asetnsamples=n=2048"
		if result != expected {
			t.Errorf("buildResampleFilter() = %q, want %q", result, expected)
		}
	})

	t.Run("disabled returns empty string", func(t *testing.T) {
		config := newTestConfig()
		config.Resample.Enabled = false

		result := config.buildResampleFilter()

		if result != "" {
			t.Errorf("buildResampleFilter() = %q, want empty string", result)
		}
	})
}

func TestBuildRequiredOutputFormatFilter(t *testing.T) {
	config := newTestConfig()
	config.Resample.Enabled = false
	config.Resample.SampleRate = 48000
	config.Resample.Format = "s32"
	config.Resample.FrameSize = 2048

	result := config.buildRequiredOutputFormatFilter()

	expected := "aformat=sample_rates=48000:channel_layouts=mono:sample_fmts=s32,asetnsamples=n=2048"
	if result != expected {
		t.Errorf("buildRequiredOutputFormatFilter() = %q, want %q", result, expected)
	}
}

func TestPass1FilterOrder(t *testing.T) {
	t.Run("includes correct filters in order", func(t *testing.T) {
		// Pass 1 now uses interval sampling for silence detection (no silencedetect filter)
		expected := []FilterID{FilterDownmix, FilterAnalysis}

		if len(Pass1FilterOrder) != len(expected) {
			t.Fatalf("Pass1FilterOrder has %d filters, want %d", len(Pass1FilterOrder), len(expected))
		}

		for i, id := range expected {
			if Pass1FilterOrder[i] != id {
				t.Errorf("Pass1FilterOrder[%d] = %q, want %q", i, Pass1FilterOrder[i], id)
			}
		}
	})

	t.Run("starts with Downmix", func(t *testing.T) {
		if Pass1FilterOrder[0] != FilterDownmix {
			t.Errorf("Pass1FilterOrder should start with FilterDownmix, got %q", Pass1FilterOrder[0])
		}
	})

	t.Run("ends with Analysis", func(t *testing.T) {
		// After removing silencedetect, Pass 1 now ends with Analysis
		last := Pass1FilterOrder[len(Pass1FilterOrder)-1]
		if last != FilterAnalysis {
			t.Errorf("Pass1FilterOrder should end with FilterAnalysis, got %q", last)
		}
	})
}

func TestPass2FilterOrder(t *testing.T) {
	t.Run("starts with Downmix", func(t *testing.T) {
		if Pass2FilterOrder[0] != FilterDownmix {
			t.Errorf("Pass2FilterOrder should start with FilterDownmix, got %q", Pass2FilterOrder[0])
		}
	})

	t.Run("ends with Resample", func(t *testing.T) {
		last := Pass2FilterOrder[len(Pass2FilterOrder)-1]
		if last != FilterResample {
			t.Errorf("Pass2FilterOrder should end with FilterResample, got %q", last)
		}
	})

	t.Run("Analysis comes before Resample", func(t *testing.T) {
		var analysisIdx, resampleIdx int
		for i, id := range Pass2FilterOrder {
			if id == FilterAnalysis {
				analysisIdx = i
			}
			if id == FilterResample {
				resampleIdx = i
			}
		}
		if analysisIdx >= resampleIdx {
			t.Errorf("FilterAnalysis (idx %d) should come before FilterResample (idx %d)",
				analysisIdx, resampleIdx)
		}
	})

	t.Run("includes all processing filters", func(t *testing.T) {
		requiredFilters := []FilterID{
			FilterDownmix,
			FilterRumbleHighPass, // fixed 80 Hz HP corner
			FilterBandlimitLowPass,
			FilterNoiseReduction,
			FilterSpeechGate,
			FilterLevellingCompressor,
			FilterDeesser,
			FilterAnalysis,
			FilterResample,
		}

		filterSet := make(map[FilterID]bool)
		for _, id := range Pass2FilterOrder {
			filterSet[id] = true
		}

		for _, required := range requiredFilters {
			if !filterSet[required] {
				t.Errorf("Pass2FilterOrder missing required filter %q", required)
			}
		}
	})

	t.Run("omits Pass 4 adeclick registry entry", func(t *testing.T) {
		for _, id := range Pass2FilterOrder {
			if id == FilterID("adeclick") {
				t.Error("Pass2FilterOrder should not include Pass 4 adeclick")
			}
		}
		if _, ok := filterBuilders[FilterID("adeclick")]; ok {
			t.Error("filterBuilders should not register Pass 4 adeclick for Pass 2")
		}
	})
}
