package processor

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

// newTestConfig creates a minimal FilterChainConfig for testing.
// All filters are disabled by default - enable only what you need for each test.
// This isolates tests from application default configuration changes.
func newTestConfig() *FilterChainConfig {
	return &FilterChainConfig{
		// Infrastructure filters (disabled by default for isolated tests)
		DownmixEnabled:     false,
		AnalysisEnabled:    false,
		ResampleEnabled:    false,
		ResampleSampleRate: 44100,
		ResampleFormat:     "s16",
		ResampleFrameSize:  4096,

		// Processing filters (all disabled by default)
		DS201HPEnabled:     false,
		NoiseRemoveEnabled: false,
		DS201GateEnabled:   false,
		LA2AEnabled:        false,
		DeessEnabled:       false,
		VolumaxEnabled:     false,

		// Sensible defaults for parameters (used when filter is enabled)
		DS201HPFreq:        80.0,
		DS201HPPoles:       2,     // 12dB/oct standard Butterworth
		DS201HPWidth:       0.707, // Butterworth
		DS201HPMix:         1.0,   // Full wet
		DS201HPTransform:   "tdii",
		DS201GateThreshold: 0.01,
		DS201GateRatio:     2.0,
		DS201GateAttack:    20,
		DS201GateRelease:   250,
		DS201GateRange:     0.0625,
		DS201GateKnee:      2.828,
		DS201GateMakeup:    1.0,
		LA2AThreshold:      -20,
		LA2ARatio:          2.5,
		LA2AAttack:         15,
		LA2ARelease:        80,
		LA2AMakeup:         0, // Unity gain - loudnorm handles all level adjustment
		LA2AKnee:           2.5,
		LA2AMix:            1.0,
		DeessIntensity:     0.5,
		DeessAmount:        0.5,
		DeessFreq:          0.5,
		TargetI:            -16.0,
		TargetTP:           -0.3,
		TargetLRA:          7.0,

		VolumaxCeiling:     -1.0,
		VolumaxAttack:      5.0,
		VolumaxRelease:     100.0,
		VolumaxASC:         true,
		VolumaxASCLevel:    0.8,
		VolumaxInputLevel:  1.0,
		VolumaxOutputLevel: 1.0,

		// NoiseRemove defaults (anlmdn + compand)
		NoiseRemoveStrength:         0.00001,
		NoiseRemovePatchSec:         0.006,
		NoiseRemoveResearchSec:      0.0058,
		NoiseRemoveSmooth:           11.0,
		NoiseRemoveCompandThreshold: -50.0,
		NoiseRemoveCompandExpansion: 10.0,
		NoiseRemoveCompandAttack:    0.005,
		NoiseRemoveCompandDecay:     0.100,
		NoiseRemoveCompandKnee:      6.0,

		// Loudnorm defaults (Pass 3)
		LoudnormEnabled:   true,
		LoudnormTargetI:   -16.0,
		LoudnormTargetTP:  -1.5,
		LoudnormTargetLRA: 11.0,
		LoudnormDualMono:  true,
		LoudnormLinear:    true,

		// Adeclick defaults (Pass 4)
		AdeclickEnabled:   true,
		AdeclickThreshold: 1.5,
		AdeclickWindow:    55.0,
		AdeclickOverlap:   75.0,

		FilterOrder: Pass2FilterOrder,
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
		config.ResampleEnabled = true

		spec := config.BuildFilterSpec()

		// Output format filters should be present when ResampleEnabled
		if !strings.Contains(spec, "aformat=sample_rates=44100") {
			t.Error("Missing aformat output filter")
		}
		if !strings.Contains(spec, "asetnsamples=n=4096") {
			t.Error("Missing asetnsamples output filter")
		}

		// Processing filters should NOT be present when disabled
		processingFilters := []string{"highpass=", "afftdn=", "agate=", "acompressor=", "alimiter="}
		for _, pf := range processingFilters {
			if strings.Contains(spec, pf) {
				t.Errorf("Disabled filter %q should not appear in spec", pf)
			}
		}
	})

	t.Run("enabled filters appear in spec", func(t *testing.T) {
		config := newTestConfig()
		// Enable specific filters for this test
		config.DS201HPEnabled = true
		config.DS201GateEnabled = true
		config.LA2AEnabled = true
		config.DeessEnabled = true
		config.VolumaxEnabled = true
		config.ResampleEnabled = true // Required for output format filters

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
			// alimiter (Volumax) moved to Pass 3 for peak protection after gain normalisation
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
		config.DS201HPEnabled = true
		config.DS201GateEnabled = true
		config.LA2AEnabled = true
		config.DeessEnabled = true
		config.VolumaxEnabled = true

		spec := config.BuildFilterSpec()

		if strings.Contains(spec, "NaN") {
			t.Errorf("Filter spec contains NaN: %s", spec)
		}
	})

	t.Run("no Inf values in filter spec", func(t *testing.T) {
		config := newTestConfig()
		// Enable all filters to maximize coverage
		config.DS201HPEnabled = true
		config.DS201GateEnabled = true
		config.LA2AEnabled = true
		config.DeessEnabled = true
		config.VolumaxEnabled = true

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

		// With ResampleEnabled=false (from newTestConfig), no aformat should be present
		// This is intentional - infrastructure filters are now controlled by flags
		if strings.Contains(spec, "aformat=sample_rates=44100") {
			t.Error("aformat should not appear when ResampleEnabled=false")
		}
	})

	t.Run("de-esser excluded when intensity is zero", func(t *testing.T) {
		config := newTestConfig()
		config.DeessEnabled = true
		config.DeessIntensity = 0.0 // Disabled by intensity

		spec := config.BuildFilterSpec()

		if strings.Contains(spec, "deesser=") {
			t.Error("De-esser should not appear when intensity is 0")
		}
	})

	t.Run("aformat appears after analysis filters when both enabled", func(t *testing.T) {
		config := newTestConfig()
		config.AnalysisEnabled = true
		config.ResampleEnabled = true
		// Use Pass2FilterOrder which has Analysis before Resample
		config.FilterOrder = Pass2FilterOrder

		spec := config.BuildFilterSpec()

		// Should contain ebur128 analysis filter
		if !strings.Contains(spec, "ebur128=") {
			t.Fatal("Missing ebur128 filter when AnalysisEnabled=true")
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

func TestBuildDS201HighpassFilter(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		freq    float64
		wantIn  string
	}{
		{
			name:    "default frequency",
			enabled: true,
			freq:    80.0,
			wantIn:  "highpass=f=80:",
		},
		{
			name:    "dark voice frequency",
			enabled: true,
			freq:    60.0,
			wantIn:  "highpass=f=60:",
		},
		{
			name:    "bright voice frequency",
			enabled: true,
			freq:    120.0,
			wantIn:  "highpass=f=120:",
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
			config.DS201HPEnabled = tt.enabled
			config.DS201HPFreq = tt.freq

			spec := config.buildDS201HighpassFilter()

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

func TestBuildDS201GateFilter(t *testing.T) {
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
			config.DS201GateEnabled = tt.enabled
			config.DS201GateThreshold = tt.threshold

			spec := config.buildDS201GateFilter()

			if !strings.Contains(spec, tt.wantIn) {
				t.Errorf("buildDS201GateFilter() = %q, want to contain %q", spec, tt.wantIn)
			}

			// Verify detection mode is RMS (important for speech)
			if !strings.Contains(spec, "detection=rms") {
				t.Error("buildDS201GateFilter() should use RMS detection for speech")
			}
		})
	}

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.DS201GateEnabled = false

		spec := config.buildDS201GateFilter()
		if spec != "" {
			t.Errorf("buildDS201GateFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestBuildDS201LowPassFilter(t *testing.T) {
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
			config.DS201LPEnabled = tt.enabled
			config.DS201LPFreq = tt.freq

			spec := config.buildDS201LowPassFilter()

			if !tt.enabled {
				if spec != "" {
					t.Errorf("buildDS201LowPassFilter() = %q, want empty when disabled", spec)
				}
				return
			}

			if !strings.Contains(spec, tt.wantIn) {
				t.Errorf("buildDS201LowPassFilter() = %q, want to contain %q", spec, tt.wantIn)
			}
		})
	}
}

func TestBuildLA2ACompressorFilter(t *testing.T) {
	t.Run("typical podcast compression", func(t *testing.T) {
		config := newTestConfig()
		config.LA2AEnabled = true
		config.LA2AThreshold = -20.0
		config.LA2ARatio = 2.5

		spec := config.buildLA2ACompressorFilter()

		wantIn := []string{
			"acompressor=threshold=",
			"ratio=2.5",
			"detection=rms",
		}

		for _, want := range wantIn {
			if !strings.Contains(spec, want) {
				t.Errorf("buildLA2ACompressorFilter() = %q, want to contain %q", spec, want)
			}
		}

		// Threshold should be converted to linear (not raw dB)
		// -20dB in linear is 0.1, so we should NOT see "threshold=-20"
		if strings.Contains(spec, "threshold=-") {
			t.Error("buildLA2ACompressorFilter() should convert threshold to linear, not use raw dB")
		}
	})

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.LA2AEnabled = false

		spec := config.buildLA2ACompressorFilter()
		if spec != "" {
			t.Errorf("buildLA2ACompressorFilter() = %q, want empty when disabled", spec)
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
			config.DeessEnabled = tt.enabled
			config.DeessIntensity = tt.intensity

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

func TestBuildNoiseRemoveFilter(t *testing.T) {
	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.NoiseRemoveEnabled = false

		spec := config.buildNoiseRemoveFilter()
		if spec != "" {
			t.Errorf("buildNoiseRemoveFilter() = %q, want empty when disabled", spec)
		}
	})

	t.Run("enabled produces anlmdn+compand chain", func(t *testing.T) {
		config := newTestConfig()
		config.NoiseRemoveEnabled = true

		spec := config.buildNoiseRemoveFilter()

		// Must contain anlmdn filter
		if !strings.Contains(spec, "anlmdn=") {
			t.Errorf("buildNoiseRemoveFilter() missing anlmdn filter, got: %s", spec)
		}

		// Must contain compand filter
		if !strings.Contains(spec, "compand=") {
			t.Errorf("buildNoiseRemoveFilter() missing compand filter, got: %s", spec)
		}

		// anlmdn must come before compand
		anlmdnIdx := strings.Index(spec, "anlmdn=")
		compandIdx := strings.Index(spec, "compand=")
		if compandIdx < anlmdnIdx {
			t.Errorf("compand must come after anlmdn in filter chain\nGot: %s", spec)
		}
	})

	t.Run("anlmdn parameters formatted correctly", func(t *testing.T) {
		config := newTestConfig()
		config.NoiseRemoveEnabled = true
		config.NoiseRemoveStrength = 0.00001
		config.NoiseRemovePatchSec = 0.006
		config.NoiseRemoveResearchSec = 0.0058
		config.NoiseRemoveSmooth = 11.0

		spec := config.buildNoiseRemoveFilter()

		expected := []string{
			"s=0.00001", // strength
			"p=0.0060",  // patch
			"r=0.0058",  // research
			"m=11",      // smooth
		}

		for _, e := range expected {
			if !strings.Contains(spec, e) {
				t.Errorf("buildNoiseRemoveFilter() missing %q\nGot: %s", e, spec)
			}
		}
	})

	t.Run("compand parameters include threshold and expansion", func(t *testing.T) {
		config := newTestConfig()
		config.NoiseRemoveEnabled = true
		config.NoiseRemoveCompandThreshold = -50.0
		config.NoiseRemoveCompandExpansion = 10.0
		config.NoiseRemoveCompandAttack = 0.005
		config.NoiseRemoveCompandDecay = 0.100
		config.NoiseRemoveCompandKnee = 6.0

		spec := config.buildNoiseRemoveFilter()

		expected := []string{
			"attacks=0.005",
			"decays=0.100",
			"soft-knee=6.0",
			"-50/-50", // threshold point in curve
		}

		for _, e := range expected {
			if !strings.Contains(spec, e) {
				t.Errorf("buildNoiseRemoveFilter() missing %q\nGot: %s", e, spec)
			}
		}

		// Verify FLAT curve expansion: -90 should map to (-90 - expansion) = -100
		if !strings.Contains(spec, "-90/-100") {
			t.Errorf("buildNoiseRemoveFilter() missing FLAT curve point -90/-100 for 10dB expansion\nGot: %s", spec)
		}
		if !strings.Contains(spec, "-75/-85") {
			t.Errorf("buildNoiseRemoveFilter() missing FLAT curve point -75/-85 for 10dB expansion\nGot: %s", spec)
		}
	})

	t.Run("different expansion levels", func(t *testing.T) {
		expansions := []float64{6.0, 15.0, 40.0}
		for _, exp := range expansions {
			config := newTestConfig()
			config.NoiseRemoveEnabled = true
			config.NoiseRemoveCompandThreshold = -55.0
			config.NoiseRemoveCompandExpansion = exp

			spec := config.buildNoiseRemoveFilter()

			// Verify expansion is applied to curve points
			// -90 should map to (-90 - exp)
			expectedPoint := fmt.Sprintf("-90/%.0f", -90-exp)
			if !strings.Contains(spec, expectedPoint) {
				t.Errorf("buildNoiseRemoveFilter() with %.0fdB expansion missing curve point %s\nGot: %s", exp, expectedPoint, spec)
			}
		}
	})
}

func TestBuildVolumaxFilter(t *testing.T) {
	t.Run("typical podcast limiter", func(t *testing.T) {
		config := newTestConfig()
		config.VolumaxEnabled = true
		config.VolumaxCeiling = -1.0
		config.VolumaxAttack = 5.0
		config.VolumaxRelease = 100.0
		config.VolumaxASC = true
		config.VolumaxASCLevel = 0.8

		spec := config.buildVolumaxFilter()

		wantIn := []string{
			"alimiter=",
			"limit=",      // dBTP ceiling converted to linear
			"attack=5",    // attack in ms
			"release=100", // release in ms
			"asc=1",       // ASC enabled for program-dependent release
			"asc_level=0.8",
		}

		for _, want := range wantIn {
			if !strings.Contains(spec, want) {
				t.Errorf("buildVolumaxFilter() = %q, want to contain %q", spec, want)
			}
		}
	})

	t.Run("ASC disabled", func(t *testing.T) {
		config := newTestConfig()
		config.VolumaxEnabled = true
		config.VolumaxASC = false

		spec := config.buildVolumaxFilter()

		if !strings.Contains(spec, "asc=0") {
			t.Errorf("buildVolumaxFilter() = %q, want to contain asc=0", spec)
		}
	})

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.VolumaxEnabled = false

		spec := config.buildVolumaxFilter()
		if spec != "" {
			t.Errorf("buildVolumaxFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestBuildAdeclickFilter(t *testing.T) {
	t.Run("default parameters", func(t *testing.T) {
		config := newTestConfig()
		config.AdeclickEnabled = true
		config.AdeclickThreshold = 1.5
		config.AdeclickWindow = 55.0
		config.AdeclickOverlap = 75.0

		spec := config.buildAdeclickFilter()

		wantIn := []string{
			"adeclick=",
			"t=1.5",
			"w=55",
			"o=75",
		}

		for _, want := range wantIn {
			if !strings.Contains(spec, want) {
				t.Errorf("buildAdeclickFilter() = %q, want to contain %q", spec, want)
			}
		}
	})

	t.Run("custom parameters", func(t *testing.T) {
		config := newTestConfig()
		config.AdeclickEnabled = true
		config.AdeclickThreshold = 2.0
		config.AdeclickWindow = 100.0
		config.AdeclickOverlap = 50.0

		spec := config.buildAdeclickFilter()

		if !strings.Contains(spec, "t=2.0") {
			t.Errorf("buildAdeclickFilter() = %q, want to contain t=2.0", spec)
		}
		if !strings.Contains(spec, "w=100") {
			t.Errorf("buildAdeclickFilter() = %q, want to contain w=100", spec)
		}
		if !strings.Contains(spec, "o=50") {
			t.Errorf("buildAdeclickFilter() = %q, want to contain o=50", spec)
		}
	})

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.AdeclickEnabled = false

		spec := config.buildAdeclickFilter()
		if spec != "" {
			t.Errorf("buildAdeclickFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestFilterOrderRespected(t *testing.T) {
	config := newTestConfig()
	// Enable filters that appear at start and end
	config.DS201HPEnabled = true
	config.DS201GateEnabled = true
	// Volumax moved to Pass 3 for peak protection after gain normalisation
	config.DeessEnabled = true
	config.DeessIntensity = 0.5
	config.ResampleEnabled = true // Required for aformat output filter
	config.FilterOrder = Pass2FilterOrder

	spec := config.BuildFilterSpec()

	// Find positions of key filters
	highpassPos := strings.Index(spec, "highpass=")
	gatePos := strings.Index(spec, "agate=")
	deesserPos := strings.Index(spec, "deesser=")
	aformatPos := strings.Index(spec, "aformat=sample_rates=")

	// Verify order: highpass < gate < deesser < aformat
	// Note: alimiter (Volumax) is now in Pass 3, not Pass 2
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

// Tests for infrastructure filters (Downmix, Analysis, Resample)

func TestBuildDownmixFilter(t *testing.T) {
	t.Run("enabled returns aformat mono", func(t *testing.T) {
		config := newTestConfig()
		config.DownmixEnabled = true

		result := config.buildDownmixFilter()

		if result != "aformat=channel_layouts=mono" {
			t.Errorf("buildDownmixFilter() = %q, want %q", result, "aformat=channel_layouts=mono")
		}
	})

	t.Run("disabled returns empty string", func(t *testing.T) {
		config := newTestConfig()
		config.DownmixEnabled = false

		result := config.buildDownmixFilter()

		if result != "" {
			t.Errorf("buildDownmixFilter() = %q, want empty string", result)
		}
	})
}

func TestBuildAnalysisFilter(t *testing.T) {
	t.Run("enabled returns astats+aspectralstats+ebur128 chain", func(t *testing.T) {
		config := newTestConfig()
		config.AnalysisEnabled = true
		config.TargetI = -16.0

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

	t.Run("uses configured TargetI", func(t *testing.T) {
		config := newTestConfig()
		config.AnalysisEnabled = true
		config.TargetI = -14.0

		result := config.buildAnalysisFilter()

		if !strings.Contains(result, "target=-14") {
			t.Errorf("buildAnalysisFilter() should use TargetI=-14, got %q", result)
		}
	})

	t.Run("disabled returns empty string", func(t *testing.T) {
		config := newTestConfig()
		config.AnalysisEnabled = false

		result := config.buildAnalysisFilter()

		if result != "" {
			t.Errorf("buildAnalysisFilter() = %q, want empty string", result)
		}
	})
}

func TestBuildResampleFilter(t *testing.T) {
	t.Run("enabled returns aformat+asetnsamples with default params", func(t *testing.T) {
		config := newTestConfig()
		config.ResampleEnabled = true
		config.ResampleSampleRate = 44100
		config.ResampleFormat = "s16"
		config.ResampleFrameSize = 4096

		result := config.buildResampleFilter()

		expected := "aformat=sample_rates=44100:channel_layouts=mono:sample_fmts=s16,asetnsamples=n=4096"
		if result != expected {
			t.Errorf("buildResampleFilter() = %q, want %q", result, expected)
		}
	})

	t.Run("uses configured parameters", func(t *testing.T) {
		config := newTestConfig()
		config.ResampleEnabled = true
		config.ResampleSampleRate = 48000
		config.ResampleFormat = "s32"
		config.ResampleFrameSize = 2048

		result := config.buildResampleFilter()

		expected := "aformat=sample_rates=48000:channel_layouts=mono:sample_fmts=s32,asetnsamples=n=2048"
		if result != expected {
			t.Errorf("buildResampleFilter() = %q, want %q", result, expected)
		}
	})

	t.Run("disabled returns empty string", func(t *testing.T) {
		config := newTestConfig()
		config.ResampleEnabled = false

		result := config.buildResampleFilter()

		if result != "" {
			t.Errorf("buildResampleFilter() = %q, want empty string", result)
		}
	})
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
			FilterDS201HighPass, // Composite: includes hum notch filters
			FilterDS201LowPass,
			FilterNoiseRemove,
			FilterDS201Gate,
			FilterLA2ACompressor,
			FilterDeesser,
			// FilterVolumax moved to Pass 3
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

	t.Run("Volumax not in Pass2FilterOrder", func(t *testing.T) {
		// Volumax has been moved to Pass 3 for peak protection after gain normalisation
		for _, id := range Pass2FilterOrder {
			if id == FilterVolumax {
				t.Errorf("FilterVolumax should not be in Pass2FilterOrder (moved to Pass 3)")
			}
		}
	})
}
