// Package processor handles audio analysis and processing
package processor

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestCalculateLinearModeTarget(t *testing.T) {
	// Note: calculateLinearModeTarget includes a 0.1 dB safety margin to ensure
	// we stay safely within linear mode bounds, accounting for floating point
	// precision differences between Go and FFmpeg's internal calculations.
	const margin = 0.1

	tests := []struct {
		name               string
		measuredI          float64
		measuredTP         float64
		desiredI           float64
		targetTP           float64
		wantEffectiveI     float64
		wantOffset         float64
		wantLinearPossible bool
	}{
		{
			name:       "linear mode requires target adjustment - peak limited",
			measuredI:  -20.0,
			measuredTP: -5.0, // 3.5 dB headroom to target TP
			desiredI:   -16.0,
			targetTP:   -1.5,
			// max linear: -1.5 - (-5.0) + (-20.0) - 0.1 = -16.6 LUFS (with margin)
			// desired -16.0 > -16.6 (louder than max), so adjustment needed
			wantEffectiveI:     -16.5 - margin,
			wantOffset:         3.5 - margin, // -16.6 - (-20) = 3.4 dB gain
			wantLinearPossible: false,
		},
		{
			name:               "linear mode requires target adjustment - severely peak limited",
			measuredI:          -20.0,
			measuredTP:         -2.0, // Only 0.5 dB headroom
			desiredI:           -16.0,
			targetTP:           -1.5,
			wantEffectiveI:     -19.5 - margin, // max linear: -1.5 - (-2.0) + (-20.0) - 0.1 = -19.6
			wantOffset:         0.5 - margin,   // -19.6 - (-20) = 0.4 dB gain
			wantLinearPossible: false,
		},
		{
			name:       "already at target with headroom",
			measuredI:  -16.0,
			measuredTP: -3.0,
			desiredI:   -16.0,
			targetTP:   -1.5,
			// max linear: -1.5 - (-3.0) + (-16.0) - 0.1 = -14.6 LUFS (louder than desired)
			// desired -16.0 <= -14.6, so achievable
			wantEffectiveI:     -16.0,
			wantOffset:         0.0,
			wantLinearPossible: true,
		},
		{
			name:       "needs attenuation - always achievable",
			measuredI:  -12.0,
			measuredTP: -1.0,
			desiredI:   -16.0,
			targetTP:   -1.5,
			// max linear: -1.5 - (-1.0) + (-12.0) - 0.1 = -12.6 LUFS
			// desired -16.0 < -12.6 (quieter), so achievable
			wantEffectiveI:     -16.0,
			wantOffset:         -4.0, // -16 - (-12) = -4 dB
			wantLinearPossible: true,
		},
		{
			name:       "large boost with headroom",
			measuredI:  -26.0,
			measuredTP: -10.0, // 8.5 dB headroom
			desiredI:   -16.0,
			targetTP:   -1.5,
			// max linear: -1.5 - (-10.0) + (-26.0) - 0.1 = -17.6 LUFS
			// desired -16.0 > -17.6 (louder than max), so adjustment needed
			wantEffectiveI:     -17.5 - margin,
			wantOffset:         8.5 - margin, // -17.6 - (-26) = 8.4 dB
			wantLinearPossible: false,
		},
		{
			name:               "typical podcast scenario - target adjustment needed",
			measuredI:          -24.88,
			measuredTP:         -5.04,
			desiredI:           -16.0,
			targetTP:           -2.0,
			wantEffectiveI:     -21.84 - margin, // max linear: -2.0 - (-5.04) + (-24.88) - 0.1 = -21.94
			wantOffset:         3.04 - margin,   // -21.94 - (-24.88) = 2.94 dB
			wantLinearPossible: false,
		},
		{
			name:       "generous headroom allows full target",
			measuredI:  -30.0,
			measuredTP: -18.0, // Lots of headroom
			desiredI:   -16.0,
			targetTP:   -1.5,
			// max linear: -1.5 - (-18.0) + (-30.0) - 0.1 = -13.6 LUFS
			// desired -16.0 < -13.6 (quieter than max), so achievable
			wantEffectiveI:     -16.0,
			wantOffset:         14.0, // -16 - (-30) = 14 dB
			wantLinearPossible: true,
		},
		{
			name:       "post-gain I - Anna values with clamped ceiling",
			measuredI:  -36.5, // postGainI = -43.4 + 6.9 deficit
			measuredTP: -24.0, // re-derived ceiling
			desiredI:   -16.0,
			targetTP:   -2.0,
			// max linear: -2.0 - (-24.0) + (-36.5) - 0.1 = -14.6 LUFS
			// desired -16.0 <= -14.6, so achievable
			wantEffectiveI:     -16.0,
			wantOffset:         20.5, // -16.0 - (-36.5) = 20.5 dB
			wantLinearPossible: true,
		},
		{
			name:       "post-gain I - extremely quiet, still cannot reach target",
			measuredI:  -40.0, // postGainI after deficit, still very quiet
			measuredTP: -24.0, // re-derived ceiling at minimum
			desiredI:   -16.0,
			targetTP:   -2.0,
			// max linear: -2.0 - (-24.0) + (-40.0) - 0.1 = -18.1 LUFS
			// desired -16.0 > -18.1, so clamped
			wantEffectiveI:     -18.0 - margin,
			wantOffset:         22.0 - margin, // -18.1 - (-40.0) = 21.9 dB
			wantLinearPossible: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			effectiveI, offset, linearPossible := calculateLinearModeTarget(
				tt.measuredI, tt.measuredTP, tt.desiredI, tt.targetTP)

			if math.Abs(effectiveI-tt.wantEffectiveI) > 0.01 {
				t.Errorf("effectiveI = %.2f, want %.2f", effectiveI, tt.wantEffectiveI)
			}
			if math.Abs(offset-tt.wantOffset) > 0.01 {
				t.Errorf("offset = %.2f, want %.2f", offset, tt.wantOffset)
			}
			if linearPossible != tt.wantLinearPossible {
				t.Errorf("linearPossible = %v, want %v", linearPossible, tt.wantLinearPossible)
			}
		})
	}
}

func TestCalculateLimiterCeiling(t *testing.T) {
	// Minimum ceiling is -24.0 dBTP (alimiter limit=0.0625)
	const minCeiling = -24.0

	tests := []struct {
		name        string
		measuredI   float64
		measuredTP  float64
		targetI     float64
		targetTP    float64
		wantCeiling float64
		wantNeeded  bool
		wantClamped bool
	}{
		{
			name:       "limiting needed - typical podcast",
			measuredI:  -24.9,
			measuredTP: -5.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-24.9) = 8.9 dB
			// projected TP = -5.0 + 8.9 = 3.9 dBTP (exceeds -2.0)
			// ceiling = -2.0 - 8.9 - 1.5 = -12.4 dBTP
			wantCeiling: -12.4,
			wantNeeded:  true,
			wantClamped: false,
		},
		{
			name:       "limiting needed - loud peaks",
			measuredI:  -20.0,
			measuredTP: -3.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-20.0) = 4.0 dB
			// projected TP = -3.0 + 4.0 = 1.0 dBTP (exceeds -2.0)
			// ceiling = -2.0 - 4.0 - 1.5 = -7.5 dBTP
			wantCeiling: -7.5,
			wantNeeded:  true,
			wantClamped: false,
		},
		{
			name:       "no limiting needed - quiet peaks",
			measuredI:  -20.0,
			measuredTP: -10.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-20.0) = 4.0 dB
			// projected TP = -10.0 + 4.0 = -6.0 dBTP (under -2.0)
			wantCeiling: 0,
			wantNeeded:  false,
			wantClamped: false,
		},
		{
			name:       "no limiting needed - needs attenuation",
			measuredI:  -12.0,
			measuredTP: -1.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-12.0) = -4.0 dB (attenuation)
			// projected TP = -1.0 + (-4.0) = -5.0 dBTP (under -2.0)
			wantCeiling: 0,
			wantNeeded:  false,
			wantClamped: false,
		},
		{
			name:       "exactly at boundary - no limiting",
			measuredI:  -20.0,
			measuredTP: -6.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-20.0) = 4.0 dB
			// projected TP = -6.0 + 4.0 = -2.0 dBTP (exactly at target)
			wantCeiling: 0,
			wantNeeded:  false,
			wantClamped: false,
		},
		{
			name:       "very quiet audio - clamped to minimum",
			measuredI:  -43.0,
			measuredTP: -20.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-43.0) = 27.0 dB
			// projected TP = -20.0 + 27.0 = 7.0 dBTP (exceeds -2.0)
			// calculated ceiling = -2.0 - 27.0 - 1.5 = -30.5 dBTP
			// but -30.5 < -24.0, so clamped to -24.0 dBTP
			wantCeiling: minCeiling,
			wantNeeded:  true,
			wantClamped: true,
		},
		{
			name:       "just under minimum - clamped",
			measuredI:  -38.0,
			measuredTP: -15.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-38.0) = 22.0 dB
			// projected TP = -15.0 + 22.0 = 7.0 dBTP (exceeds -2.0)
			// calculated ceiling = -2.0 - 22.0 - 1.5 = -25.5 dBTP
			// -25.5 < -24.0, so clamped to -24.0 dBTP
			wantCeiling: minCeiling,
			wantNeeded:  true,
			wantClamped: true,
		},
		{
			name:       "just above minimum - not clamped",
			measuredI:  -35.0,
			measuredTP: -15.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-35.0) = 19.0 dB
			// projected TP = -15.0 + 19.0 = 4.0 dBTP (exceeds -2.0)
			// ceiling = -2.0 - 19.0 - 1.5 = -22.5 dBTP (above -24.0)
			wantCeiling: -22.5,
			wantNeeded:  true,
			wantClamped: false,
		},
		{
			name:       "Anna exact values - clamped with verifiable deficit",
			measuredI:  -43.2,
			measuredTP: -18.6,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-43.2) = 27.2 dB
			// projected TP = -18.6 + 27.2 = 8.6 dBTP (exceeds -2.0)
			// idealCeiling = -2.0 - 27.2 - 1.5 = -30.7 dBTP
			// -30.7 < -24.0, so clamped to -24.0 dBTP
			// deficit = minLimiterCeilingDB - idealCeiling = -24.0 - (-30.7) = 6.7 dB
			wantCeiling: minCeiling,
			wantNeeded:  true,
			wantClamped: true,
		},
		{
			name:       "exact clamping boundary - ceiling equals minimum exactly",
			measuredI:  -36.5,
			measuredTP: -15.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-36.5) = 20.5 dB
			// projected TP = -15.0 + 20.5 = 5.5 dBTP (exceeds -2.0)
			// ceiling = -2.0 - 20.5 - 1.5 = -24.0 dBTP (exactly minLimiterCeilingDB)
			// Not clamped: ceiling < minLimiterCeilingDB is false when equal.
			// deficit = 0 (no pre-gain needed at the boundary)
			wantCeiling: minCeiling,
			wantNeeded:  true,
			wantClamped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ceiling, needed, clamped := calculateLimiterCeiling(
				tt.measuredI, tt.measuredTP, tt.targetI, tt.targetTP)

			if needed != tt.wantNeeded {
				t.Errorf("needed = %v, want %v", needed, tt.wantNeeded)
			}
			if clamped != tt.wantClamped {
				t.Errorf("clamped = %v, want %v", clamped, tt.wantClamped)
			}
			if needed && math.Abs(ceiling-tt.wantCeiling) > 0.01 {
				t.Errorf("ceiling = %.2f dBTP, want %.2f dBTP", ceiling, tt.wantCeiling)
			}

			// Verify deficit arithmetic independently for clamped cases.
			// deficit = minLimiterCeilingDB - (targetTP - gainRequired - safetyMarginDB)
			if clamped {
				gainRequired := tt.targetI - tt.measuredI
				idealCeiling := tt.targetTP - gainRequired - safetyMarginDB
				deficit := minLimiterCeilingDB - idealCeiling
				if deficit <= 0 {
					t.Errorf("deficit should be positive when clamped, got %.2f", deficit)
				}
				// Verify the ideal ceiling is below the minimum (confirms clamping)
				if idealCeiling >= minLimiterCeilingDB {
					t.Errorf("idealCeiling = %.2f should be below minLimiterCeilingDB (%.2f) when clamped",
						idealCeiling, minLimiterCeilingDB)
				}
			}
		})
	}
}

func TestBuildLoudnormFilterSpec_PreGain(t *testing.T) {
	tests := []struct {
		name             string
		inputI           float64
		inputTP          float64
		inputLRA         float64
		inputThresh      float64
		targetOffset     float64
		wantVolumeFilter bool    // (a)/(b): volume filter present or absent
		wantDeficit      float64 // (c): expected deficit in dB (0 when no pre-gain)
		wantClamped      bool
	}{
		{
			name:         "clamped - very quiet audio (Anna-like)",
			inputI:       -43.2,
			inputTP:      -18.6,
			inputLRA:     8.0,
			inputThresh:  -53.0,
			targetOffset: -2.5,
			// gain = -16.0 - (-43.2) = 27.2
			// idealCeiling = -2.0 - 27.2 - 1.5 = -30.7
			// deficit = -24.0 - (-30.7) = 6.7
			wantVolumeFilter: true,
			wantDeficit:      6.7,
			wantClamped:      true,
		},
		{
			name:         "not clamped - typical podcast (Marius-like)",
			inputI:       -24.9,
			inputTP:      -5.0,
			inputLRA:     6.0,
			inputThresh:  -35.0,
			targetOffset: -0.5,
			// gain = -16.0 - (-24.9) = 8.9
			// idealCeiling = -2.0 - 8.9 - 1.5 = -12.4 (above -24.0)
			wantVolumeFilter: false,
			wantDeficit:      0.0,
			wantClamped:      false,
		},
		{
			name:         "clamped - moderate deficit",
			inputI:       -38.0,
			inputTP:      -15.0,
			inputLRA:     7.0,
			inputThresh:  -48.0,
			targetOffset: -1.0,
			// gain = -16.0 - (-38.0) = 22.0
			// idealCeiling = -2.0 - 22.0 - 1.5 = -25.5
			// deficit = -24.0 - (-25.5) = 1.5
			wantVolumeFilter: true,
			wantDeficit:      1.5,
			wantClamped:      true,
		},
		{
			name:         "no limiter needed - quiet peaks",
			inputI:       -20.0,
			inputTP:      -10.0,
			inputLRA:     5.0,
			inputThresh:  -30.0,
			targetOffset: 0.0,
			// gain = -16.0 - (-20.0) = 4.0
			// projectedTP = -10.0 + 4.0 = -6.0 (under -2.0, no limiter)
			wantVolumeFilter: false,
			wantDeficit:      0.0,
			wantClamped:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultFilterConfig()
			measurement := &LoudnormMeasurement{
				InputI:       tt.inputI,
				InputTP:      tt.inputTP,
				InputLRA:     tt.inputLRA,
				InputThresh:  tt.inputThresh,
				TargetOffset: tt.targetOffset,
			}

			// Pre-compute values (caller's responsibility after Task 2.2)
			ceiling, needsLimiting, clamped := calculateLimiterCeiling(
				tt.inputI, tt.inputTP, config.LoudnormTargetI, config.LoudnormTargetTP,
			)
			preGainDB, reDerivedCeiling := calculatePreGain(
				tt.inputI, config.LoudnormTargetI, config.LoudnormTargetTP,
			)
			if clamped {
				ceiling = reDerivedCeiling
			}

			filterSpec := buildLoudnormFilterSpec(config, measurement, preGainDB, ceiling, needsLimiting)

			// (a)/(b): Check volume filter presence
			hasVolume := strings.Contains(filterSpec, "volume=")
			if hasVolume != tt.wantVolumeFilter {
				t.Errorf("volume filter present = %v, want %v\nfilterSpec: %s", hasVolume, tt.wantVolumeFilter, filterSpec)
			}

			// Check clamped value from pre-computation
			if clamped != tt.wantClamped {
				t.Errorf("clamped = %v, want %v", clamped, tt.wantClamped)
			}

			// (c): Check deficit value from pre-computation
			if math.Abs(preGainDB-tt.wantDeficit) > 0.01 {
				t.Errorf("preGainDB = %.2f, want %.2f", preGainDB, tt.wantDeficit)
			}

			// (new): Verify measurement.InputI and measurement.InputTP are passed
			// directly to loudnorm as measured_I and measured_TP (no adjustment)
			wantDirectI := fmt.Sprintf("measured_I=%.2f", tt.inputI)
			if !strings.Contains(filterSpec, wantDirectI) {
				t.Errorf("loudnorm should pass measurement.InputI directly as measured_I=%q\nfilterSpec: %s", wantDirectI, filterSpec)
			}
			wantDirectTP := fmt.Sprintf("measured_TP=%.2f", tt.inputTP)
			if !strings.Contains(filterSpec, wantDirectTP) {
				t.Errorf("loudnorm should pass measurement.InputTP directly as measured_TP=%q\nfilterSpec: %s", wantDirectTP, filterSpec)
			}

			if tt.wantVolumeFilter {
				// (c): Verify deficit value in the filter string
				wantVolumeStr := fmt.Sprintf("volume=%.1fdB", tt.wantDeficit)
				if !strings.Contains(filterSpec, wantVolumeStr) {
					t.Errorf("filter spec missing %q\nfilterSpec: %s", wantVolumeStr, filterSpec)
				}

				// (d): Re-derived ceiling used for alimiter
				reDerivedLinear := math.Pow(10, reDerivedCeiling/20.0)
				wantLimit := fmt.Sprintf("limit=%.6f", reDerivedLinear)
				if !strings.Contains(filterSpec, wantLimit) {
					t.Errorf("alimiter should use re-derived ceiling (limit=%.6f), not found\nfilterSpec: %s", reDerivedLinear, filterSpec)
				}

				// Verify volume filter appears before alimiter in the chain
				volumeIdx := strings.Index(filterSpec, "volume=")
				alimiterIdx := strings.Index(filterSpec, "alimiter=")
				if alimiterIdx == -1 {
					t.Error("alimiter filter missing from spec when clamped")
				} else if volumeIdx > alimiterIdx {
					t.Error("volume filter must appear before alimiter")
				}
			} else {
				hasLimiter := strings.Contains(filterSpec, "alimiter=")
				if hasLimiter != needsLimiting {
					t.Errorf("alimiter present = %v, want %v\nfilterSpec: %s", hasLimiter, needsLimiting, filterSpec)
				}
			}
		})
	}
}

func TestBuildLoudnormFilterSpec_DoesNotMutateConfig(t *testing.T) {
	config := DefaultFilterConfig()
	config.ResampleEnabled = false

	measurement := &LoudnormMeasurement{
		InputI:       -24.0,
		InputTP:      -5.0,
		InputLRA:     6.0,
		InputThresh:  -34.0,
		TargetOffset: -0.5,
	}

	_ = buildLoudnormFilterSpec(config, measurement, 0, -1.0, false)

	if config.ResampleEnabled {
		t.Error("buildLoudnormFilterSpec mutated config.ResampleEnabled")
	}
}

func TestPreGainCeilingRederivation(t *testing.T) {
	// Validates the mathematical invariant: applying the deficit as pre-gain
	// converts a clamped scenario into a non-clamped scenario, with the
	// re-derived ceiling landing at or near minLimiterCeilingDB.

	tests := []struct {
		name       string
		measuredI  float64
		measuredTP float64
		targetI    float64
		targetTP   float64
	}{
		{
			name:       "Anna-like - very quiet, large deficit",
			measuredI:  -43.2,
			measuredTP: -18.6,
			targetI:    -16.0,
			targetTP:   -2.0,
		},
		{
			name:       "moderate deficit - just below clamping",
			measuredI:  -38.0,
			measuredTP: -15.0,
			targetI:    -16.0,
			targetTP:   -2.0,
		},
		{
			name:       "extreme quiet - large gain required",
			measuredI:  -50.0,
			measuredTP: -25.0,
			targetI:    -16.0,
			targetTP:   -2.0,
		},
		{
			name:       "different target TP",
			measuredI:  -40.0,
			measuredTP: -16.0,
			targetI:    -16.0,
			targetTP:   -1.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Step 1: original values should be clamped
			origCeiling, origNeeded, origClamped := calculateLimiterCeiling(
				tt.measuredI, tt.measuredTP, tt.targetI, tt.targetTP)

			if !origNeeded {
				t.Fatal("expected limiter to be needed for original values")
			}
			if !origClamped {
				t.Fatal("expected original ceiling to be clamped")
			}
			if math.Abs(origCeiling-minLimiterCeilingDB) > 0.01 {
				t.Fatalf("clamped ceiling = %.2f, want %.2f", origCeiling, minLimiterCeilingDB)
			}

			// Step 2: calculate deficit
			gainRequired := tt.targetI - tt.measuredI
			idealCeiling := tt.targetTP - gainRequired - safetyMarginDB
			deficit := minLimiterCeilingDB - idealCeiling

			if deficit <= 0 {
				t.Fatalf("deficit should be positive for clamped scenario, got %.2f", deficit)
			}

			// Step 3: apply deficit as pre-gain and re-derive ceiling
			postGainI := tt.measuredI + deficit
			postGainTP := tt.measuredTP + deficit

			newCeiling, newNeeded, newClamped := calculateLimiterCeiling(
				postGainI, postGainTP, tt.targetI, tt.targetTP)

			if !newNeeded {
				t.Error("expected limiter to still be needed after pre-gain")
			}
			if newClamped {
				t.Error("expected re-derived ceiling to NOT be clamped after pre-gain")
			}

			// Step 4: re-derived ceiling should land at minLimiterCeilingDB
			if math.Abs(newCeiling-minLimiterCeilingDB) > 0.01 {
				t.Errorf("re-derived ceiling = %.2f dBTP, want %.2f dBTP (minLimiterCeilingDB)",
					newCeiling, minLimiterCeilingDB)
			}
		})
	}
}

func TestClampedTargetPropagation_Arithmetic(t *testing.T) {
	// Verifies the arithmetic chain that ApplyNormalisation uses when the
	// ceiling is clamped: calculateLimiterCeiling -> deficit -> post-gain I ->
	// calculateLinearModeTarget -> buildLoudnormFilterSpec. Each function is
	// called with the same inputs ApplyNormalisation would derive, confirming
	// the full -16.0 LUFS target is preserved.
	//
	// This does not exercise ApplyNormalisation itself (which requires audio
	// files and the full FFmpeg pipeline); it validates the pure-function chain.

	tests := []struct {
		name           string
		measuredI      float64
		measuredTP     float64
		targetI        float64
		targetTP       float64
		wantEffectiveI float64
		wantLinear     bool
	}{
		{
			name:           "Anna - very quiet, clamped ceiling preserves full target",
			measuredI:      -43.4,
			measuredTP:     -19.2,
			targetI:        -16.0,
			targetTP:       -2.0,
			wantEffectiveI: -16.0,
			wantLinear:     true,
		},
		{
			name:           "Anna-like with different measurements",
			measuredI:      -43.2,
			measuredTP:     -18.6,
			targetI:        -16.0,
			targetTP:       -2.0,
			wantEffectiveI: -16.0,
			wantLinear:     true,
		},
		{
			name:       "extreme quiet - still clamped after pre-gain",
			measuredI:  -55.0,
			measuredTP: -30.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// deficit = -24.0 - (-2.0 - (-16.0 - (-55.0)) - 1.5) = -24.0 - (-42.5) = 18.5
			// postGainI = -55.0 + 18.5 = -36.5
			// re-derived ceiling = -24.0
			// maxLinear = -2.0 - (-24.0) + (-36.5) - 0.1 = -14.6
			// -16.0 <= -14.6, so full target preserved
			wantEffectiveI: -16.0,
			wantLinear:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Step 1: calculateLimiterCeiling (same as ApplyNormalisation)
			_, limiterNeeded, limiterClamped := calculateLimiterCeiling(
				tt.measuredI, tt.measuredTP, tt.targetI, tt.targetTP)

			if !limiterNeeded {
				t.Fatal("expected limiter to be needed")
			}
			if !limiterClamped {
				t.Fatal("expected ceiling to be clamped")
			}

			// Step 2: replicate the effectiveTP and effectiveMeasuredI logic
			gainRequired := tt.targetI - tt.measuredI
			idealCeiling := tt.targetTP - gainRequired - safetyMarginDB
			deficit := minLimiterCeilingDB - idealCeiling
			postGainI := tt.measuredI + deficit
			newGainRequired := tt.targetI - postGainI
			reDerivedCeiling := tt.targetTP - newGainRequired - safetyMarginDB
			effectiveTP := reDerivedCeiling
			effectiveMeasuredI := postGainI

			// Step 3: calculateLinearModeTarget with post-gain I
			effectiveTargetI, _, linearPossible := calculateLinearModeTarget(
				effectiveMeasuredI, effectiveTP, tt.targetI, tt.targetTP)

			if math.Abs(effectiveTargetI-tt.wantEffectiveI) > 0.01 {
				t.Errorf("effectiveTargetI = %.2f, want %.2f", effectiveTargetI, tt.wantEffectiveI)
			}
			if linearPossible != tt.wantLinear {
				t.Errorf("linearPossible = %v, want %v", linearPossible, tt.wantLinear)
			}

			// Step 4: verify buildLoudnormFilterSpec receives the full target
			config := DefaultFilterConfig()
			config.LoudnormTargetI = effectiveTargetI
			measurement := &LoudnormMeasurement{
				InputI:       tt.measuredI,
				InputTP:      tt.measuredTP,
				InputLRA:     8.0,
				InputThresh:  tt.measuredI - 10.0,
				TargetOffset: -2.5,
			}

			// Pre-compute values (caller's responsibility after Task 2.2)
			bCeiling, bNeeded, bClamped := calculateLimiterCeiling(
				tt.measuredI, tt.measuredTP, config.LoudnormTargetI, config.LoudnormTargetTP,
			)
			preGainDB, bReDerived := calculatePreGain(
				tt.measuredI, config.LoudnormTargetI, config.LoudnormTargetTP,
			)
			if bClamped {
				bCeiling = bReDerived
			}

			filterSpec := buildLoudnormFilterSpec(config, measurement, preGainDB, bCeiling, bNeeded)
			if !bClamped {
				t.Error("expected pre-computation to report clamped")
			}
			if math.Abs(preGainDB-deficit) > 0.01 {
				t.Errorf("preGainDB = %.2f, want deficit = %.2f", preGainDB, deficit)
			}

			// Verify the filter spec contains the expected loudnorm parameters
			if !strings.Contains(filterSpec, "loudnorm=") {
				t.Error("filter spec missing loudnorm filter")
			}
		})
	}
}

func TestCalculatePreGain(t *testing.T) {
	tests := []struct {
		name              string
		measuredI         float64
		targetI           float64
		targetTP          float64
		wantPreGainDB     float64
		wantReDerivedCeil float64
	}{
		{
			name:      "clamped - returns positive deficit and valid re-derived ceiling",
			measuredI: -43.2,
			targetI:   -16.0,
			targetTP:  -2.0,
			// gainRequired = -16.0 - (-43.2) = 27.2
			// idealCeiling = -2.0 - 27.2 - 1.5 = -30.7
			// deficit = -24.0 - (-30.7) = 6.7
			// postGainI = -43.2 + 6.7 = -36.5
			// newGainRequired = -16.0 - (-36.5) = 20.5
			// reDerivedCeiling = -2.0 - 20.5 - 1.5 = -24.0
			wantPreGainDB:     6.7,
			wantReDerivedCeil: -24.0,
		},
		{
			name:      "not clamped - returns zeros",
			measuredI: -24.9,
			targetI:   -16.0,
			targetTP:  -2.0,
			// gainRequired = 8.9
			// idealCeiling = -2.0 - 8.9 - 1.5 = -12.4 (above -24.0)
			wantPreGainDB:     0.0,
			wantReDerivedCeil: 0.0,
		},
		{
			name:      "boundary - ideal ceiling equals minLimiterCeilingDB exactly",
			measuredI: -36.5,
			targetI:   -16.0,
			targetTP:  -2.0,
			// gainRequired = 20.5
			// idealCeiling = -2.0 - 20.5 - 1.5 = -24.0 (exactly minLimiterCeilingDB)
			wantPreGainDB:     0.0,
			wantReDerivedCeil: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preGainDB, reDerivedCeiling := calculatePreGain(tt.measuredI, tt.targetI, tt.targetTP)

			if math.Abs(preGainDB-tt.wantPreGainDB) > 0.01 {
				t.Errorf("preGainDB = %.2f, want %.2f", preGainDB, tt.wantPreGainDB)
			}
			if math.Abs(reDerivedCeiling-tt.wantReDerivedCeil) > 0.01 {
				t.Errorf("reDerivedCeiling = %.2f, want %.2f", reDerivedCeiling, tt.wantReDerivedCeil)
			}
		})
	}
}

func TestBuildPreLimiterPrefix(t *testing.T) {
	tests := []struct {
		name          string
		preGainDB     float64
		ceiling       float64
		needsLimiting bool
		wantEmpty     bool
		wantVolume    bool
		wantAlimiter  bool
	}{
		{
			name:          "clamped - volume and alimiter",
			preGainDB:     6.7,
			ceiling:       -24.0,
			needsLimiting: true,
			wantEmpty:     false,
			wantVolume:    true,
			wantAlimiter:  true,
		},
		{
			name:          "needed but not clamped - alimiter only",
			preGainDB:     0.0,
			ceiling:       -12.4,
			needsLimiting: true,
			wantEmpty:     false,
			wantVolume:    false,
			wantAlimiter:  true,
		},
		{
			name:          "not needed - empty string",
			preGainDB:     0.0,
			ceiling:       0.0,
			needsLimiting: false,
			wantEmpty:     true,
			wantVolume:    false,
			wantAlimiter:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPreLimiterPrefix(tt.preGainDB, tt.ceiling, tt.needsLimiting)

			if tt.wantEmpty {
				if result != "" {
					t.Errorf("expected empty string, got %q", result)
				}
				return
			}

			hasVolume := strings.Contains(result, "volume=")
			if hasVolume != tt.wantVolume {
				t.Errorf("volume present = %v, want %v\nresult: %s", hasVolume, tt.wantVolume, result)
			}

			hasAlimiter := strings.Contains(result, "alimiter=")
			if hasAlimiter != tt.wantAlimiter {
				t.Errorf("alimiter present = %v, want %v\nresult: %s", hasAlimiter, tt.wantAlimiter, result)
			}

			// (d): volume appears before alimiter when both present
			if hasVolume && hasAlimiter {
				volumeIdx := strings.Index(result, "volume=")
				alimiterIdx := strings.Index(result, "alimiter=")
				if volumeIdx > alimiterIdx {
					t.Error("volume must appear before alimiter")
				}
			}

			// Verify correct volume value when present
			if tt.wantVolume {
				wantVolumeStr := fmt.Sprintf("volume=%.1fdB", tt.preGainDB)
				if !strings.Contains(result, wantVolumeStr) {
					t.Errorf("expected %q in result %q", wantVolumeStr, result)
				}
			}

			// Verify correct ceiling in alimiter when present
			if tt.wantAlimiter {
				limiterLinear := math.Pow(10, tt.ceiling/20.0)
				wantLimit := fmt.Sprintf("limit=%.6f", limiterLinear)
				if !strings.Contains(result, wantLimit) {
					t.Errorf("expected %q in result %q", wantLimit, result)
				}
			}
		})
	}
}
