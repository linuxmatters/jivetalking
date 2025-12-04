package processor

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestBuildFilterSpec(t *testing.T) {
	// Test complete filter chain generation with default config
	t.Run("default config produces valid filter chain", func(t *testing.T) {
		config := DefaultFilterConfig()
		spec := config.BuildFilterSpec()

		// Should not be empty
		if spec == "" {
			t.Fatal("BuildFilterSpec returned empty string")
		}

		// Output format filters should always be present
		if !strings.Contains(spec, "aformat=sample_rates=44100") {
			t.Error("Missing aformat output filter")
		}
		if !strings.Contains(spec, "asetnsamples=n=4096") {
			t.Error("Missing asetnsamples output filter")
		}
	})

	t.Run("typical podcast config includes all core filters", func(t *testing.T) {
		config := DefaultFilterConfig()
		config.Measurements = &AudioMeasurements{
			InputI:     -30,
			NoiseFloor: -50,
		}
		// Enable a de-esser for this test
		config.DeessIntensity = 0.5

		spec := config.BuildFilterSpec()

		// Verify required filters are present
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
		config := DefaultFilterConfig()
		config.Measurements = &AudioMeasurements{
			InputI:     -25,
			NoiseFloor: -55,
		}

		spec := config.BuildFilterSpec()

		if strings.Contains(spec, "NaN") {
			t.Errorf("Filter spec contains NaN: %s", spec)
		}
	})

	t.Run("no Inf values in filter spec", func(t *testing.T) {
		config := DefaultFilterConfig()
		config.Measurements = &AudioMeasurements{
			InputI:     -25,
			NoiseFloor: -55,
		}

		spec := config.BuildFilterSpec()

		if strings.Contains(spec, "Inf") || strings.Contains(spec, "inf") {
			t.Errorf("Filter spec contains Inf: %s", spec)
		}
	})

	t.Run("disabled filters are excluded", func(t *testing.T) {
		config := DefaultFilterConfig()
		config.HighpassEnabled = false
		config.AfftdnEnabled = false
		config.GateEnabled = false
		config.CompEnabled = false
		config.DeessEnabled = false
		config.SpeechnormEnabled = false
		config.DynaudnormEnabled = false
		config.LimiterEnabled = false
		config.ArnnDnEnabled = false

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

		// But output format should still be present
		if !strings.Contains(spec, "aformat=sample_rates=44100") {
			t.Error("Missing aformat output filter even with all processing disabled")
		}
	})

	t.Run("de-esser excluded when intensity is zero", func(t *testing.T) {
		config := DefaultFilterConfig()
		config.DeessEnabled = true
		config.DeessIntensity = 0.0 // Disabled by intensity

		spec := config.BuildFilterSpec()

		if strings.Contains(spec, "deesser=") {
			t.Error("De-esser should not appear when intensity is 0")
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
			config := DefaultFilterConfig()
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
			config := DefaultFilterConfig()
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
		config := DefaultFilterConfig()
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
			config := DefaultFilterConfig()
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
		config := DefaultFilterConfig()
		config.GateEnabled = false

		spec := config.buildAgateFilter()
		if spec != "" {
			t.Errorf("buildAgateFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestBuildAcompressorFilter(t *testing.T) {
	t.Run("typical podcast compression", func(t *testing.T) {
		config := DefaultFilterConfig()
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
		config := DefaultFilterConfig()
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
			config := DefaultFilterConfig()
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
		config := DefaultFilterConfig()
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
		config := DefaultFilterConfig()
		config.LimiterEnabled = true
		config.LimiterCeiling = 0.95

		spec := config.buildAlimiterFilter()

		if !strings.Contains(spec, "limit=0.95") {
			t.Errorf("buildAlimiterFilter() = %q, want to contain limit=0.95", spec)
		}
	})

	t.Run("disabled returns empty", func(t *testing.T) {
		config := DefaultFilterConfig()
		config.LimiterEnabled = false

		spec := config.buildAlimiterFilter()
		if spec != "" {
			t.Errorf("buildAlimiterFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestBuildSpeechnormFilter(t *testing.T) {
	t.Run("default speech normalization", func(t *testing.T) {
		config := DefaultFilterConfig()
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
		config := DefaultFilterConfig()
		config.SpeechnormEnabled = false

		spec := config.buildSpeechnormFilter()
		if spec != "" {
			t.Errorf("buildSpeechnormFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestBuildDynaudnormFilter(t *testing.T) {
	t.Run("default dynaudnorm", func(t *testing.T) {
		config := DefaultFilterConfig()
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
		config := DefaultFilterConfig()
		config.DynaudnormEnabled = false

		spec := config.buildDynaudnormFilter()
		if spec != "" {
			t.Errorf("buildDynaudnormFilter() = %q, want empty when disabled", spec)
		}
	})
}

func TestFilterOrderRespected(t *testing.T) {
	config := DefaultFilterConfig()
	// Enable filters that appear at start and end
	config.HighpassEnabled = true
	config.AfftdnEnabled = true
	config.LimiterEnabled = true
	config.DeessIntensity = 0.5 // Enable de-esser

	spec := config.BuildFilterSpec()

	// Find positions of key filters
	highpassPos := strings.Index(spec, "highpass=")
	afftdnPos := strings.Index(spec, "afftdn=")
	limiterPos := strings.Index(spec, "alimiter=")
	aformatPos := strings.Index(spec, "aformat=")

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
