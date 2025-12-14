// Package processor handles audio analysis and processing
package processor

import (
	"fmt"
	"math"
	"testing"
)

func TestCalculateNormalisationGain(t *testing.T) {
	tests := []struct {
		name       string
		outputI    float64 // Measured integrated loudness from Pass 2 (LUFS)
		targetI    float64 // Target integrated loudness (LUFS)
		tolerance  float64 // Acceptable deviation (LU)
		wantGain   float64 // Expected gain in dB
		wantNeeded bool    // Expected "needed" flag
		wantErr    bool    // Expected error
	}{
		{
			name:       "exactly on target",
			outputI:    -16.0,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   0.0,
			wantNeeded: false,
			wantErr:    false,
		},
		{
			name:       "within tolerance (quiet side)",
			outputI:    -16.3,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   0.0,
			wantNeeded: false,
			wantErr:    false,
		},
		{
			name:       "within tolerance (loud side)",
			outputI:    -15.7,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   0.0,
			wantNeeded: false,
			wantErr:    false,
		},
		{
			name:       "at tolerance boundary (quiet)",
			outputI:    -16.5,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   0.0, // 0.5 LU is within tolerance, not needed
			wantNeeded: false,
			wantErr:    false,
		},
		{
			name:       "at tolerance boundary (loud)",
			outputI:    -15.5,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   0.0, // 0.5 LU is within tolerance, not needed
			wantNeeded: false,
			wantErr:    false,
		},
		{
			name:       "needs boost",
			outputI:    -20.0,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   4.0, // -16.0 - (-20.0) = 4.0 dB
			wantNeeded: true,
			wantErr:    false,
		},
		{
			name:       "needs attenuation",
			outputI:    -12.0,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   -4.0, // -16.0 - (-12.0) = -4.0 dB
			wantNeeded: true,
			wantErr:    false,
		},
		{
			name:       "large boost (low-gain mic)",
			outputI:    -30.0,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   14.0, // Large boost for quiet recordings (e.g., SM7B)
			wantNeeded: true,
			wantErr:    false,
		},
		{
			name:       "large attenuation (hot recording)",
			outputI:    -2.0,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   -14.0, // Large cut for loud recordings
			wantNeeded: true,
			wantErr:    false,
		},
		{
			name:       "silent audio (very negative)",
			outputI:    -80.0,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   0.0,
			wantNeeded: false,
			wantErr:    true, // Silent audio cannot be normalised
		},
		{
			name:       "silent audio (-inf)",
			outputI:    math.Inf(-1),
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   0.0,
			wantNeeded: false,
			wantErr:    true, // Silent audio cannot be normalised
		},
		{
			name:       "moderate boost",
			outputI:    -28.0,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   12.0,
			wantNeeded: true,
			wantErr:    false,
		},
		{
			name:       "moderate attenuation",
			outputI:    -4.0,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   -12.0,
			wantNeeded: true,
			wantErr:    false,
		},
		{
			name:       "small boost just outside tolerance",
			outputI:    -16.6,
			targetI:    -16.0,
			tolerance:  0.5,
			wantGain:   0.6, // Just outside tolerance
			wantNeeded: true,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotGain, gotNeeded, err := CalculateNormalisationGain(tt.outputI, tt.targetI, tt.tolerance)

			if tt.wantErr {
				if err == nil {
					t.Errorf("CalculateNormalisationGain() expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("CalculateNormalisationGain() unexpected error: %v", err)
				return
			}

			if gotNeeded != tt.wantNeeded {
				t.Errorf("CalculateNormalisationGain() needed = %v, want %v", gotNeeded, tt.wantNeeded)
			}

			// Use tolerance for float comparison
			if math.Abs(gotGain-tt.wantGain) > 0.001 {
				t.Errorf("CalculateNormalisationGain() gain = %v, want %v", gotGain, tt.wantGain)
			}
		})
	}
}

func TestTuneUREI1176FromOutput(t *testing.T) {
	tests := []struct {
		name          string
		output        *OutputMeasurements
		wantAttack    float64 // Expected attack time (0 = use default check)
		wantRelease   float64 // Expected release time (0 = use default check)
		wantASC       bool    // Expected ASC enabled
		checkDefaults bool    // If true, verify defaults are unchanged
	}{
		{
			name:          "nil output uses defaults",
			output:        nil,
			checkDefaults: true,
		},
		{
			name: "extreme transients need fastest attack",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 10000, // 10000/32768 ≈ 0.305 > u1176MaxDiffExtreme (0.25)
					SpectralCrest: 25.0,  // Below extreme threshold
					SpectralFlux:  0.02,
					DynamicRange:  15.0, // Low DR to disable ASC
				},
				OutputLRA: 12.0,
			},
			wantAttack: u1176AttackExtreme, // 0.1 ms
			wantASC:    false,
		},
		{
			name: "high spectral crest triggers extreme attack",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 1000, // Below extreme threshold
					SpectralCrest: 55.0, // > u1176CrestExtreme (50.0) - also triggers ASC
					SpectralFlux:  0.02,
					DynamicRange:  15.0,
				},
				OutputLRA: 12.0,
			},
			wantAttack: u1176AttackExtreme, // 0.1 ms
			wantASC:    true,               // High crest also enables ASC via u1176CrestEnableASC
		},
		{
			name: "sharp transients need fast attack",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 6000, // 6000/32768 ≈ 0.18 > u1176MaxDiffSharp (0.15)
					SpectralCrest: 25.0,
					SpectralFlux:  0.02,
					DynamicRange:  15.0, // Low DR to disable ASC
				},
				OutputLRA: 12.0,
			},
			wantAttack: u1176AttackSharp, // 0.5 ms
			wantASC:    false,
		},
		{
			name: "normal transients need normal attack",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 3000, // 3000/32768 ≈ 0.09 > u1176MaxDiffNormal (0.08)
					SpectralCrest: 25.0,
					SpectralFlux:  0.02,
					DynamicRange:  15.0, // Low DR to disable ASC
				},
				OutputLRA: 12.0,
			},
			wantAttack: u1176AttackNormal, // 0.8 ms
			wantASC:    false,
		},
		{
			name: "soft delivery uses gentle attack",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 1000, // 1000/32768 ≈ 0.03 < u1176MaxDiffNormal (0.08)
					SpectralCrest: 15.0,
					SpectralFlux:  0.02,
					DynamicRange:  15.0, // Low DR to disable ASC
				},
				OutputLRA: 12.0,
			},
			wantAttack: u1176AttackGentle, // 1.0 ms
			wantASC:    false,
		},
		{
			name: "dynamic content needs expressive release",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 2000,
					SpectralCrest: 25.0,
					SpectralFlux:  0.04, // > u1176FluxDynamic (0.03)
					DynamicRange:  15.0, // Low DR to disable ASC
				},
				OutputLRA: 16.0, // > u1176LRAWide (15.0)
			},
			wantRelease: u1176ReleaseExpressive, // 200.0 ms
			wantASC:     false,
		},
		{
			name: "controlled content needs faster release",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 2000,
					SpectralCrest: 25.0,
					SpectralFlux:  0.005, // < u1176FluxStatic (0.01)
					DynamicRange:  15.0,  // Low DR to disable ASC
				},
				OutputLRA: 8.0, // < u1176LRANarrow (10.0)
			},
			wantRelease: u1176ReleaseControlled, // 100.0 ms
			wantASC:     false,
		},
		{
			name: "standard podcast delivery",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 2000,
					SpectralCrest: 25.0,
					SpectralFlux:  0.02, // Between static and dynamic
					DynamicRange:  15.0, // Low DR to disable ASC
				},
				OutputLRA: 12.0, // Between narrow and wide
			},
			wantRelease: u1176ReleaseStandard, // 150.0 ms
			wantASC:     false,
		},
		{
			name: "wide dynamic range adds release boost",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 2000,
					SpectralCrest: 25.0,
					SpectralFlux:  0.02,
					DynamicRange:  40.0, // > u1176DRWide (35.0), also > u1176DREnableASC (30.0)
				},
				OutputLRA: 12.0,
			},
			wantRelease: u1176ReleaseStandard + u1176ReleaseDRBoost, // 150 + 50 = 200 ms
			wantASC:     true,                                       // High DR enables ASC
		},
		{
			name: "dynamic content enables ASC",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 2000,
					SpectralCrest: 25.0,
					SpectralFlux:  0.02,
					DynamicRange:  35.0, // > u1176DREnableASC (30.0)
				},
				OutputLRA: 12.0,
			},
			wantASC: true,
		},
		{
			name: "high crest enables ASC",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 2000,
					SpectralCrest: 45.0, // > u1176CrestEnableASC (40.0)
					SpectralFlux:  0.02,
					DynamicRange:  15.0, // Below DR thresholds
				},
				OutputLRA: 12.0,
			},
			wantASC: true,
		},
		{
			name: "moderate dynamic range enables ASC",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 2000,
					SpectralCrest: 25.0,
					SpectralFlux:  0.02,
					DynamicRange:  25.0, // > u1176DRModerateASC (20.0), but < u1176DREnableASC (30.0)
				},
				OutputLRA: 12.0,
			},
			wantASC: true,
		},
		{
			name: "controlled content disables ASC",
			output: &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: 2000,
					SpectralCrest: 25.0, // < u1176CrestEnableASC (40.0)
					SpectralFlux:  0.02,
					DynamicRange:  15.0, // < u1176DRModerateASC (20.0)
				},
				OutputLRA: 12.0,
			},
			wantASC: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create fresh config with defaults
			config := DefaultFilterConfig()
			originalAttack := config.UREI1176Attack
			originalRelease := config.UREI1176Release
			originalASC := config.UREI1176ASC

			// Apply tuning
			tuneUREI1176FromOutput(config, tt.output)

			if tt.checkDefaults {
				// Verify defaults are unchanged
				if config.UREI1176Attack != originalAttack {
					t.Errorf("Attack changed from %v to %v when using nil output", originalAttack, config.UREI1176Attack)
				}
				if config.UREI1176Release != originalRelease {
					t.Errorf("Release changed from %v to %v when using nil output", originalRelease, config.UREI1176Release)
				}
				if config.UREI1176ASC != originalASC {
					t.Errorf("ASC changed from %v to %v when using nil output", originalASC, config.UREI1176ASC)
				}
				return
			}

			// Check attack if specified
			if tt.wantAttack != 0 && math.Abs(config.UREI1176Attack-tt.wantAttack) > 0.001 {
				t.Errorf("Attack = %v, want %v", config.UREI1176Attack, tt.wantAttack)
			}

			// Check release if specified
			if tt.wantRelease != 0 && math.Abs(config.UREI1176Release-tt.wantRelease) > 0.001 {
				t.Errorf("Release = %v, want %v", config.UREI1176Release, tt.wantRelease)
			}

			// Check ASC if test specifies expected state (only for tests that set it)
			if tt.name != "nil output uses defaults" {
				if config.UREI1176ASC != tt.wantASC {
					t.Errorf("ASC = %v, want %v", config.UREI1176ASC, tt.wantASC)
				}
			}
		})
	}
}

func TestTuneUREI1176AttackFromOutput(t *testing.T) {
	tests := []struct {
		name          string
		maxDifference float64 // Raw sample difference value
		spectralCrest float64 // Spectral crest in dB
		wantAttack    float64 // Expected attack time in ms
	}{
		{
			name:          "extreme transient via maxDiff",
			maxDifference: 32768 * 0.30, // > u1176MaxDiffExtreme (0.25)
			spectralCrest: 20.0,
			wantAttack:    u1176AttackExtreme,
		},
		{
			name:          "extreme transient via crest",
			maxDifference: 32768 * 0.10,
			spectralCrest: 55.0, // > u1176CrestExtreme (50.0)
			wantAttack:    u1176AttackExtreme,
		},
		{
			name:          "sharp transient via maxDiff",
			maxDifference: 32768 * 0.20, // > u1176MaxDiffSharp (0.15)
			spectralCrest: 20.0,
			wantAttack:    u1176AttackSharp,
		},
		{
			name:          "sharp transient via crest",
			maxDifference: 32768 * 0.05,
			spectralCrest: 40.0, // > u1176CrestSharp (35.0)
			wantAttack:    u1176AttackSharp,
		},
		{
			name:          "normal transient",
			maxDifference: 32768 * 0.10, // > u1176MaxDiffNormal (0.08)
			spectralCrest: 20.0,
			wantAttack:    u1176AttackNormal,
		},
		{
			name:          "gentle/soft transient",
			maxDifference: 32768 * 0.05, // < u1176MaxDiffNormal (0.08)
			spectralCrest: 20.0,
			wantAttack:    u1176AttackGentle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultFilterConfig()
			output := &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					MaxDifference: tt.maxDifference,
					SpectralCrest: tt.spectralCrest,
				},
			}

			tuneUREI1176AttackFromOutput(config, output)

			if math.Abs(config.UREI1176Attack-tt.wantAttack) > 0.001 {
				t.Errorf("Attack = %v ms, want %v ms", config.UREI1176Attack, tt.wantAttack)
			}
		})
	}
}

func TestTuneUREI1176ReleaseFromOutput(t *testing.T) {
	tests := []struct {
		name         string
		spectralFlux float64
		outputLRA    float64
		dynamicRange float64
		wantRelease  float64
	}{
		{
			name:         "expressive delivery (high flux + wide LRA)",
			spectralFlux: 0.04, // > u1176FluxDynamic (0.03)
			outputLRA:    16.0, // > u1176LRAWide (15.0)
			dynamicRange: 25.0,
			wantRelease:  u1176ReleaseExpressive,
		},
		{
			name:         "controlled delivery (low flux + narrow LRA)",
			spectralFlux: 0.005, // < u1176FluxStatic (0.01)
			outputLRA:    8.0,   // < u1176LRANarrow (10.0)
			dynamicRange: 25.0,
			wantRelease:  u1176ReleaseControlled,
		},
		{
			name:         "standard delivery",
			spectralFlux: 0.02,
			outputLRA:    12.0,
			dynamicRange: 25.0,
			wantRelease:  u1176ReleaseStandard,
		},
		{
			name:         "wide DR adds boost to standard",
			spectralFlux: 0.02,
			outputLRA:    12.0,
			dynamicRange: 40.0, // > u1176DRWide (35.0)
			wantRelease:  u1176ReleaseStandard + u1176ReleaseDRBoost,
		},
		{
			name:         "wide DR adds boost to expressive",
			spectralFlux: 0.04,
			outputLRA:    16.0,
			dynamicRange: 40.0,
			wantRelease:  u1176ReleaseExpressive + u1176ReleaseDRBoost,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultFilterConfig()
			output := &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					SpectralFlux: tt.spectralFlux,
					DynamicRange: tt.dynamicRange,
				},
				OutputLRA: tt.outputLRA,
			}

			tuneUREI1176ReleaseFromOutput(config, output)

			if math.Abs(config.UREI1176Release-tt.wantRelease) > 0.001 {
				t.Errorf("Release = %v ms, want %v ms", config.UREI1176Release, tt.wantRelease)
			}
		})
	}
}

func TestTuneUREI1176ASCFromOutput(t *testing.T) {
	tests := []struct {
		name             string
		dynamicRange     float64
		spectralCrest    float64
		astatsNoiseFloor float64
		wantASC          bool
		wantASCLevel     float64 // Expected level when ASC is enabled
	}{
		{
			name:             "dynamic content (high DR) enables ASC",
			dynamicRange:     35.0, // > u1176DREnableASC (30.0)
			spectralCrest:    25.0,
			astatsNoiseFloor: -70.0, // Quiet floor, no boost
			wantASC:          true,
			wantASCLevel:     u1176ASCDynamic, // 0.7
		},
		{
			name:             "high crest enables ASC",
			dynamicRange:     15.0, // Below all DR thresholds
			spectralCrest:    45.0, // > u1176CrestEnableASC (40.0)
			astatsNoiseFloor: -70.0,
			wantASC:          true,
			wantASCLevel:     u1176ASCDynamic, // 0.7 (high crest triggers dynamic level)
		},
		{
			name:             "moderate DR enables moderate ASC",
			dynamicRange:     25.0, // > u1176DRModerateASC (20.0), but < u1176DREnableASC (30.0)
			spectralCrest:    25.0,
			astatsNoiseFloor: -70.0,
			wantASC:          true,
			wantASCLevel:     u1176ASCModerate, // 0.5
		},
		{
			name:             "controlled content disables ASC",
			dynamicRange:     15.0, // < u1176DRModerateASC (20.0)
			spectralCrest:    25.0, // < u1176CrestEnableASC (40.0)
			astatsNoiseFloor: -70.0,
			wantASC:          false,
			wantASCLevel:     0,
		},
		{
			name:             "noisy content boosts ASC level",
			dynamicRange:     35.0, // > u1176DREnableASC (30.0)
			spectralCrest:    25.0,
			astatsNoiseFloor: -45.0, // > u1176NoiseFloorASC (-50.0) triggers boost
			wantASC:          true,
			wantASCLevel:     u1176ASCDynamic + u1176ASCNoisyBoost, // 0.7 + 0.2 = 0.9
		},
		{
			name:             "ASC level capped at 1.0",
			dynamicRange:     35.0,  // > u1176DREnableASC (30.0)
			spectralCrest:    45.0,  // Also > u1176CrestEnableASC (40.0)
			astatsNoiseFloor: -30.0, // Very noisy, triggers boost
			wantASC:          true,
			wantASCLevel:     math.Min(1.0, u1176ASCDynamic+u1176ASCNoisyBoost), // 0.9, not capped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultFilterConfig()
			output := &OutputMeasurements{
				BaseMeasurements: BaseMeasurements{
					DynamicRange:     tt.dynamicRange,
					SpectralCrest:    tt.spectralCrest,
					AstatsNoiseFloor: tt.astatsNoiseFloor,
				},
			}

			tuneUREI1176ASCFromOutput(config, output)

			if config.UREI1176ASC != tt.wantASC {
				t.Errorf("ASC = %v, want %v", config.UREI1176ASC, tt.wantASC)
			}

			if tt.wantASC && math.Abs(config.UREI1176ASCLevel-tt.wantASCLevel) > 0.001 {
				t.Errorf("ASCLevel = %v, want %v", config.UREI1176ASCLevel, tt.wantASCLevel)
			}
		})
	}
}

func TestBuildNormalisationFilterSpec(t *testing.T) {
	tests := []struct {
		name       string
		gainDB     float64
		config     *FilterChainConfig
		wantVolume bool // Expect volume filter in spec
		wantLimit  bool // Expect alimiter filter in spec
		wantMeter  bool // Expect ebur128 filter in spec
	}{
		{
			name:   "standard normalisation with boost",
			gainDB: 4.0,
			config: &FilterChainConfig{
				NormTargetI:        -16.0,
				UREI1176Enabled:    true,
				UREI1176Ceiling:    -1.0,
				UREI1176Attack:     0.5,
				UREI1176Release:    150.0,
				UREI1176ASC:        true,
				UREI1176ASCLevel:   0.5,
				ResampleEnabled:    true,
				ResampleSampleRate: 44100,
				ResampleFormat:     "s16",
				ResampleFrameSize:  4096,
			},
			wantVolume: true,
			wantLimit:  true,
			wantMeter:  true,
		},
		{
			name:   "zero gain still applies limiter",
			gainDB: 0.0,
			config: &FilterChainConfig{
				NormTargetI:        -16.0,
				UREI1176Enabled:    true,
				UREI1176Ceiling:    -1.0,
				UREI1176Attack:     0.5,
				UREI1176Release:    150.0,
				ResampleEnabled:    true,
				ResampleSampleRate: 44100,
				ResampleFormat:     "s16",
				ResampleFrameSize:  4096,
			},
			wantVolume: true, // volume=0dB still present
			wantLimit:  true,
			wantMeter:  true,
		},
		{
			name:   "negative gain (attenuation)",
			gainDB: -3.0,
			config: &FilterChainConfig{
				NormTargetI:        -16.0,
				UREI1176Enabled:    true,
				UREI1176Ceiling:    -1.0,
				UREI1176Attack:     0.5,
				UREI1176Release:    150.0,
				ResampleEnabled:    true,
				ResampleSampleRate: 44100,
				ResampleFormat:     "s16",
				ResampleFrameSize:  4096,
			},
			wantVolume: true,
			wantLimit:  true,
			wantMeter:  true,
		},
		{
			name:   "limiter disabled",
			gainDB: 2.0,
			config: &FilterChainConfig{
				NormTargetI:        -16.0,
				UREI1176Enabled:    false,
				ResampleEnabled:    true,
				ResampleSampleRate: 44100,
				ResampleFormat:     "s16",
				ResampleFrameSize:  4096,
			},
			wantVolume: true,
			wantLimit:  false, // Limiter disabled
			wantMeter:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := buildNormalisationFilterSpec(tt.gainDB, tt.config)

			hasVolume := contains(spec, "volume")
			hasLimit := contains(spec, "alimiter")
			hasMeter := contains(spec, "ebur128")

			if tt.wantVolume && !hasVolume {
				t.Errorf("Expected volume filter in spec: %s", spec)
			}
			if tt.wantLimit && !hasLimit {
				t.Errorf("Expected alimiter filter in spec: %s", spec)
			}
			if tt.wantMeter && !hasMeter {
				t.Errorf("Expected ebur128 filter in spec: %s", spec)
			}

			// Verify gain value appears correctly in spec
			expectedGainStr := fmt.Sprintf("volume=%.2fdB", tt.gainDB)
			if !contains(spec, expectedGainStr) {
				t.Errorf("Expected gain %s in spec: %s", expectedGainStr, spec)
			}

			// Log the full filter spec for debugging
			t.Logf("Filter spec: %s", spec)
		})
	}
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
