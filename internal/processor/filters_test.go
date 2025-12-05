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
		DownmixEnabled:        false,
		AnalysisEnabled:       false,
		SilenceDetectEnabled:  false,
		SilenceDetectLevel:    -50.0,
		SilenceDetectDuration: 0.5,
		ResampleEnabled:       false,
		ResampleSampleRate:    44100,
		ResampleFormat:        "s16",
		ResampleFrameSize:     4096,

		// Processing filters (all disabled by default)
		HighpassEnabled:   false,
		HumFilterEnabled:  false,
		AdeclickEnabled:   false,
		AfftdnEnabled:     false,
		GateEnabled:       false,
		CompEnabled:       false,
		DeessEnabled:      false,
		DynaudnormEnabled: false,
		SpeechnormEnabled: false,
		ArnnDnEnabled:     false,
		BleedGateEnabled:  false,
		LimiterEnabled:    false,

		// Sensible defaults for parameters (used when filter is enabled)
		HighpassFreq:   80.0,
		HumFrequency:   50.0,
		HumHarmonics:   4,
		HumQ:           30.0,
		AdeclickMethod: "s",
		NoiseFloor:     -50.0,
		NoiseReduction: 12.0,
		NoiseTrack:     true,
		GateThreshold:  0.01,
		GateRatio:      2.0,
		GateAttack:     20,
		GateRelease:    250,
		GateRange:      0.0625,
		GateKnee:       2.828,
		GateMakeup:     1.0,
		CompThreshold:  -20,
		CompRatio:      2.5,
		CompAttack:     15,
		CompRelease:    80,
		CompMakeup:     3,
		CompKnee:       2.5,
		CompMix:        1.0,
		DeessIntensity: 0.5,
		DeessAmount:    0.5,
		DeessFreq:      0.5,
		TargetI:        -16.0,
		TargetTP:       -0.3,
		TargetLRA:      7.0,

		DynaudnormFrameLen:   500,
		DynaudnormFilterSize: 31,
		DynaudnormPeakValue:  0.95,
		DynaudnormMaxGain:    10.0,

		SpeechnormPeak:        0.95,
		SpeechnormExpansion:   3.0,
		SpeechnormCompression: 2.0,
		SpeechnormThreshold:   0.10,
		SpeechnormRaise:       0.001,
		SpeechnormFall:        0.001,

		ArnnDnMix:  0.8,
		ArnnDnMix2: 0.7,

		BleedGateThreshold: 0.01,
		BleedGateRatio:     4.0,
		BleedGateAttack:    15,
		BleedGateRelease:   200,
		BleedGateRange:     0.125,
		BleedGateKnee:      3.0,

		LimiterCeiling: 0.84,
		LimiterAttack:  5.0,
		LimiterRelease: 50.0,

		FilterOrder: DefaultFilterOrder,
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
		config.HighpassEnabled = true
		config.AfftdnEnabled = true
		config.GateEnabled = true
		config.CompEnabled = true
		config.DeessEnabled = true
		config.SpeechnormEnabled = true
		config.DynaudnormEnabled = true
		config.LimiterEnabled = true
		config.ResampleEnabled = true // Required for output format filters

		spec := config.BuildFilterSpec()

		// Verify enabled filters are present
		requiredFilters := []struct {
			prefix string
			name   string
		}{
			{"highpass=f=", "highpass"},
			{"afftdn=nf=", "afftdn (noise reduction)"},
			{"agate=threshold=", "agate"},
			{"acompressor=threshold=", "acompressor"},
			{"deesser=i=", "deesser"},
			{"speechnorm=", "speechnorm"},
			{"dynaudnorm=", "dynaudnorm"},
			{"alimiter=", "alimiter"},
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
		config.HighpassEnabled = true
		config.AfftdnEnabled = true
		config.GateEnabled = true
		config.CompEnabled = true
		config.DeessEnabled = true
		config.SpeechnormEnabled = true
		config.DynaudnormEnabled = true
		config.LimiterEnabled = true

		spec := config.BuildFilterSpec()

		if strings.Contains(spec, "NaN") {
			t.Errorf("Filter spec contains NaN: %s", spec)
		}
	})

	t.Run("no Inf values in filter spec", func(t *testing.T) {
		config := newTestConfig()
		// Enable all filters to maximize coverage
		config.HighpassEnabled = true
		config.AfftdnEnabled = true
		config.GateEnabled = true
		config.CompEnabled = true
		config.DeessEnabled = true
		config.SpeechnormEnabled = true
		config.DynaudnormEnabled = true
		config.LimiterEnabled = true

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

func TestBuildHighpassFilter(t *testing.T) {
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
			config.HighpassEnabled = tt.enabled
			config.HighpassFreq = tt.freq

			spec := config.buildHighpassFilter()

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

func TestBuildAfftdnFilter(t *testing.T) {
	tests := []struct {
		name           string
		enabled        bool
		noiseFloor     float64
		noiseReduction float64
		wantIn         []string
	}{
		{
			name:           "light noise reduction",
			enabled:        true,
			noiseFloor:     -50.0,
			noiseReduction: 12.0,
			wantIn:         []string{"afftdn=nf=-50.0", "nr=12.0"},
		},
		{
			name:           "moderate noise reduction",
			enabled:        true,
			noiseFloor:     -45.0,
			noiseReduction: 24.0,
			wantIn:         []string{"afftdn=nf=-45.0", "nr=24.0"},
		},
		{
			name:           "aggressive noise reduction",
			enabled:        true,
			noiseFloor:     -40.0,
			noiseReduction: 35.0,
			wantIn:         []string{"afftdn=nf=-40.0", "nr=35.0"},
		},
		{
			name:           "noise floor clamped to min (-80)",
			enabled:        true,
			noiseFloor:     -100.0, // below -80 limit
			noiseReduction: 12.0,
			wantIn:         []string{"afftdn=nf=-80.0"}, // clamped
		},
		{
			name:           "noise floor clamped to max (-20)",
			enabled:        true,
			noiseFloor:     -10.0, // above -20 limit
			noiseReduction: 12.0,
			wantIn:         []string{"afftdn=nf=-20.0"}, // clamped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := newTestConfig()
			config.AfftdnEnabled = tt.enabled
			config.NoiseFloor = tt.noiseFloor
			config.NoiseReduction = tt.noiseReduction

			spec := config.buildAfftdnFilter()

			if !tt.enabled {
				if spec != "" {
					t.Errorf("buildAfftdnFilter() = %q, want empty", spec)
				}
				return
			}

			for _, want := range tt.wantIn {
				if !strings.Contains(spec, want) {
					t.Errorf("buildAfftdnFilter() = %q, want to contain %q", spec, want)
				}
			}
		})
	}

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.AfftdnEnabled = false

		spec := config.buildAfftdnFilter()
		if spec != "" {
			t.Errorf("buildAfftdnFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestBuildAgateFilter(t *testing.T) {
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
			config.GateEnabled = tt.enabled
			config.GateThreshold = tt.threshold

			spec := config.buildAgateFilter()

			if !strings.Contains(spec, tt.wantIn) {
				t.Errorf("buildAgateFilter() = %q, want to contain %q", spec, tt.wantIn)
			}

			// Verify detection mode is RMS (important for speech)
			if !strings.Contains(spec, "detection=rms") {
				t.Error("buildAgateFilter() should use RMS detection for speech")
			}
		})
	}

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.GateEnabled = false

		spec := config.buildAgateFilter()
		if spec != "" {
			t.Errorf("buildAgateFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestBuildAcompressorFilter(t *testing.T) {
	t.Run("typical podcast compression", func(t *testing.T) {
		config := newTestConfig()
		config.CompEnabled = true
		config.CompThreshold = -20.0
		config.CompRatio = 2.5

		spec := config.buildAcompressorFilter()

		wantIn := []string{
			"acompressor=threshold=",
			"ratio=2.5",
			"detection=rms",
		}

		for _, want := range wantIn {
			if !strings.Contains(spec, want) {
				t.Errorf("buildAcompressorFilter() = %q, want to contain %q", spec, want)
			}
		}

		// Threshold should be converted to linear (not raw dB)
		// -20dB in linear is 0.1, so we should NOT see "threshold=-20"
		if strings.Contains(spec, "threshold=-") {
			t.Error("buildAcompressorFilter() should convert threshold to linear, not use raw dB")
		}
	})

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.CompEnabled = false

		spec := config.buildAcompressorFilter()
		if spec != "" {
			t.Errorf("buildAcompressorFilter() = %q, want empty when disabled", spec)
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

func TestBuildAlimiterFilter(t *testing.T) {
	t.Run("typical podcast limiter", func(t *testing.T) {
		config := newTestConfig()
		config.LimiterEnabled = true
		config.LimiterCeiling = 0.98 // -0.17dBFS

		spec := config.buildAlimiterFilter()

		wantIn := []string{
			"alimiter=",
			"limit=0.98",
			"asc=1", // ASC enabled for smooth limiting
		}

		for _, want := range wantIn {
			if !strings.Contains(spec, want) {
				t.Errorf("buildAlimiterFilter() = %q, want to contain %q", spec, want)
			}
		}
	})

	t.Run("conservative ceiling", func(t *testing.T) {
		config := newTestConfig()
		config.LimiterEnabled = true
		config.LimiterCeiling = 0.95

		spec := config.buildAlimiterFilter()

		if !strings.Contains(spec, "limit=0.95") {
			t.Errorf("buildAlimiterFilter() = %q, want to contain limit=0.95", spec)
		}
	})

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.LimiterEnabled = false

		spec := config.buildAlimiterFilter()
		if spec != "" {
			t.Errorf("buildAlimiterFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestBuildSpeechnormFilter(t *testing.T) {
	t.Run("default speech normalization", func(t *testing.T) {
		config := newTestConfig()
		config.SpeechnormEnabled = true
		config.SpeechnormPeak = 0.95
		config.SpeechnormExpansion = 12.5

		spec := config.buildSpeechnormFilter()

		wantIn := []string{"speechnorm=p=0.95", "e=12.5"}

		for _, want := range wantIn {
			if !strings.Contains(spec, want) {
				t.Errorf("buildSpeechnormFilter() = %q, want to contain %q", spec, want)
			}
		}
	})

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.SpeechnormEnabled = false

		spec := config.buildSpeechnormFilter()
		if spec != "" {
			t.Errorf("buildSpeechnormFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestBuildDynaudnormFilter(t *testing.T) {
	t.Run("default dynaudnorm", func(t *testing.T) {
		config := newTestConfig()
		config.DynaudnormEnabled = true
		config.DynaudnormFrameLen = 500
		config.DynaudnormMaxGain = 10.0

		spec := config.buildDynaudnormFilter()

		wantIn := []string{"dynaudnorm=f=500", "m=10.0"}

		for _, want := range wantIn {
			if !strings.Contains(spec, want) {
				t.Errorf("buildDynaudnormFilter() = %q, want to contain %q", spec, want)
			}
		}
	})

	t.Run("disabled returns empty", func(t *testing.T) {
		config := newTestConfig()
		config.DynaudnormEnabled = false

		spec := config.buildDynaudnormFilter()
		if spec != "" {
			t.Errorf("buildDynaudnormFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestFilterOrderRespected(t *testing.T) {
	config := newTestConfig()
	// Enable filters that appear at start and end
	config.HighpassEnabled = true
	config.AfftdnEnabled = true
	config.LimiterEnabled = true
	config.DeessEnabled = true
	config.DeessIntensity = 0.5
	config.ResampleEnabled = true // Required for aformat output filter
	config.FilterOrder = Pass2FilterOrder

	spec := config.BuildFilterSpec()

	// Find positions of key filters
	highpassPos := strings.Index(spec, "highpass=")
	afftdnPos := strings.Index(spec, "afftdn=")
	limiterPos := strings.Index(spec, "alimiter=")
	aformatPos := strings.Index(spec, "aformat=sample_rates=")

	// Verify order: highpass < afftdn < limiter < aformat
	if highpassPos >= afftdnPos {
		t.Errorf("highpass (pos %d) should come before afftdn (pos %d)", highpassPos, afftdnPos)
	}
	if afftdnPos >= limiterPos {
		t.Errorf("afftdn (pos %d) should come before alimiter (pos %d)", afftdnPos, limiterPos)
	}
	if limiterPos >= aformatPos {
		t.Errorf("alimiter (pos %d) should come before aformat (pos %d)", limiterPos, aformatPos)
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
			got := dbToLinear(tt.db)
			diff := math.Abs(got - tt.wantLinear)
			if diff > tt.tolerance {
				t.Errorf("dbToLinear(%.1f) = %.6f, want %.6f (±%.6f)",
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
			got := dbToLinear(db)
			want := math.Pow(10, db/20.0)
			if math.Abs(got-want) > 0.0000001 {
				t.Errorf("dbToLinear(%.1f) = %.10f, want %.10f (exact formula)", db, got, want)
			}
		})
	}
}

// Tests for infrastructure filters (Downmix, Analysis, SilenceDetect, Resample)

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
		if !strings.Contains(result, "measure_perchannel=Noise_floor+Dynamic_range+RMS_level+Peak_level") {
			t.Error("buildAnalysisFilter() missing detailed astats measurements")
		}
		if !strings.Contains(result, "aspectralstats=win_size=2048") {
			t.Error("buildAnalysisFilter() missing aspectralstats filter")
		}
		if !strings.Contains(result, "measure=centroid+rolloff") {
			t.Error("buildAnalysisFilter() missing spectral measurements")
		}
		if !strings.Contains(result, "ebur128=metadata=1:peak=true") {
			t.Error("buildAnalysisFilter() missing ebur128 filter")
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

func TestBuildSilenceDetectFilter(t *testing.T) {
	t.Run("enabled returns silencedetect with threshold and duration", func(t *testing.T) {
		config := newTestConfig()
		config.SilenceDetectEnabled = true
		config.SilenceDetectLevel = -50.0
		config.SilenceDetectDuration = 0.5

		result := config.buildSilenceDetectFilter()

		expected := "silencedetect=noise=-50dB:duration=0.50"
		if result != expected {
			t.Errorf("buildSilenceDetectFilter() = %q, want %q", result, expected)
		}
	})

	t.Run("uses configured parameters", func(t *testing.T) {
		config := newTestConfig()
		config.SilenceDetectEnabled = true
		config.SilenceDetectLevel = -40.0
		config.SilenceDetectDuration = 1.0

		result := config.buildSilenceDetectFilter()

		expected := "silencedetect=noise=-40dB:duration=1.00"
		if result != expected {
			t.Errorf("buildSilenceDetectFilter() = %q, want %q", result, expected)
		}
	})

	t.Run("disabled returns empty string", func(t *testing.T) {
		config := newTestConfig()
		config.SilenceDetectEnabled = false

		result := config.buildSilenceDetectFilter()

		if result != "" {
			t.Errorf("buildSilenceDetectFilter() = %q, want empty string", result)
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
		expected := []FilterID{FilterDownmix, FilterAnalysis, FilterSilenceDetect}

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

	t.Run("ends with SilenceDetect", func(t *testing.T) {
		last := Pass1FilterOrder[len(Pass1FilterOrder)-1]
		if last != FilterSilenceDetect {
			t.Errorf("Pass1FilterOrder should end with FilterSilenceDetect, got %q", last)
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
			FilterHighpass,
			FilterBandreject,
			FilterAdeclick,
			FilterAfftdn,
			FilterArnndn,
			FilterAgate,
			FilterAcompressor,
			FilterDeesser,
			FilterSpeechnorm,
			FilterDynaudnorm,
			FilterBleedGate,
			FilterAlimiter,
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

	t.Run("Alimiter comes before Analysis", func(t *testing.T) {
		var alimiterIdx, analysisIdx int
		for i, id := range Pass2FilterOrder {
			if id == FilterAlimiter {
				alimiterIdx = i
			}
			if id == FilterAnalysis {
				analysisIdx = i
			}
		}
		if alimiterIdx >= analysisIdx {
			t.Errorf("FilterAlimiter (idx %d) should come before FilterAnalysis (idx %d)",
				alimiterIdx, analysisIdx)
		}
	})
}

func TestDefaultFilterOrder(t *testing.T) {
	t.Run("equals Pass2FilterOrder", func(t *testing.T) {
		if len(DefaultFilterOrder) != len(Pass2FilterOrder) {
			t.Fatalf("DefaultFilterOrder length %d != Pass2FilterOrder length %d",
				len(DefaultFilterOrder), len(Pass2FilterOrder))
		}

		for i := range DefaultFilterOrder {
			if DefaultFilterOrder[i] != Pass2FilterOrder[i] {
				t.Errorf("DefaultFilterOrder[%d] = %q, Pass2FilterOrder[%d] = %q",
					i, DefaultFilterOrder[i], i, Pass2FilterOrder[i])
			}
		}
	})
}
