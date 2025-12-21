// Package processor handles audio analysis and processing
package processor

import (
	"math"
	"testing"
)

func TestCalculateLinearModeTarget(t *testing.T) {
	// Note: calculateLinearModeTarget includes a 0.1 dB safety margin to ensure
	// we stay safely within linear mode bounds, accounting for floating point
	// precision differences between Go and FFmpeg's internal calculations.
	const margin = 0.1

	tests := []struct {
		name               string
		measured_I         float64
		measured_TP        float64
		desired_I          float64
		target_TP          float64
		wantEffectiveI     float64
		wantOffset         float64
		wantLinearPossible bool
	}{
		{
			name:        "linear mode requires target adjustment - peak limited",
			measured_I:  -20.0,
			measured_TP: -5.0, // 3.5 dB headroom to target TP
			desired_I:   -16.0,
			target_TP:   -1.5,
			// max linear: -1.5 - (-5.0) + (-20.0) - 0.1 = -16.6 LUFS (with margin)
			// desired -16.0 > -16.6 (louder than max), so adjustment needed
			wantEffectiveI:     -16.5 - margin,
			wantOffset:         3.5 - margin, // -16.6 - (-20) = 3.4 dB gain
			wantLinearPossible: false,
		},
		{
			name:               "linear mode requires target adjustment - severely peak limited",
			measured_I:         -20.0,
			measured_TP:        -2.0, // Only 0.5 dB headroom
			desired_I:          -16.0,
			target_TP:          -1.5,
			wantEffectiveI:     -19.5 - margin, // max linear: -1.5 - (-2.0) + (-20.0) - 0.1 = -19.6
			wantOffset:         0.5 - margin,   // -19.6 - (-20) = 0.4 dB gain
			wantLinearPossible: false,
		},
		{
			name:        "already at target with headroom",
			measured_I:  -16.0,
			measured_TP: -3.0,
			desired_I:   -16.0,
			target_TP:   -1.5,
			// max linear: -1.5 - (-3.0) + (-16.0) - 0.1 = -14.6 LUFS (louder than desired)
			// desired -16.0 <= -14.6, so achievable
			wantEffectiveI:     -16.0,
			wantOffset:         0.0,
			wantLinearPossible: true,
		},
		{
			name:        "needs attenuation - always achievable",
			measured_I:  -12.0,
			measured_TP: -1.0,
			desired_I:   -16.0,
			target_TP:   -1.5,
			// max linear: -1.5 - (-1.0) + (-12.0) - 0.1 = -12.6 LUFS
			// desired -16.0 < -12.6 (quieter), so achievable
			wantEffectiveI:     -16.0,
			wantOffset:         -4.0, // -16 - (-12) = -4 dB
			wantLinearPossible: true,
		},
		{
			name:        "large boost with headroom",
			measured_I:  -26.0,
			measured_TP: -10.0, // 8.5 dB headroom
			desired_I:   -16.0,
			target_TP:   -1.5,
			// max linear: -1.5 - (-10.0) + (-26.0) - 0.1 = -17.6 LUFS
			// desired -16.0 > -17.6 (louder than max), so adjustment needed
			wantEffectiveI:     -17.5 - margin,
			wantOffset:         8.5 - margin, // -17.6 - (-26) = 8.4 dB
			wantLinearPossible: false,
		},
		{
			name:               "typical podcast scenario - target adjustment needed",
			measured_I:         -24.88,
			measured_TP:        -5.04,
			desired_I:          -16.0,
			target_TP:          -2.0,
			wantEffectiveI:     -21.84 - margin, // max linear: -2.0 - (-5.04) + (-24.88) - 0.1 = -21.94
			wantOffset:         3.04 - margin,   // -21.94 - (-24.88) = 2.94 dB
			wantLinearPossible: false,
		},
		{
			name:        "generous headroom allows full target",
			measured_I:  -30.0,
			measured_TP: -18.0, // Lots of headroom
			desired_I:   -16.0,
			target_TP:   -1.5,
			// max linear: -1.5 - (-18.0) + (-30.0) - 0.1 = -13.6 LUFS
			// desired -16.0 < -13.6 (quieter than max), so achievable
			wantEffectiveI:     -16.0,
			wantOffset:         14.0, // -16 - (-30) = 14 dB
			wantLinearPossible: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			effectiveI, offset, linearPossible := calculateLinearModeTarget(
				tt.measured_I, tt.measured_TP, tt.desired_I, tt.target_TP)

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
	// Safety margin is 2.0 dB in the implementation (accounts for ISP creation during limiting)
	const safetyMargin = 2.0
	// Minimum ceiling is -24.0 dBTP (alimiter limit=0.0625)
	const minCeiling = -24.0

	tests := []struct {
		name        string
		measured_I  float64
		measured_TP float64
		target_I    float64
		target_TP   float64
		wantCeiling float64
		wantNeeded  bool
		wantClamped bool
	}{
		{
			name:        "limiting needed - typical podcast",
			measured_I:  -24.9,
			measured_TP: -5.0,
			target_I:    -16.0,
			target_TP:   -2.0,
			// gain = -16.0 - (-24.9) = 8.9 dB
			// projected TP = -5.0 + 8.9 = 3.9 dBTP (exceeds -2.0)
			// ceiling = -2.0 - 8.9 - 2.0 = -12.9 dBTP
			wantCeiling: -12.9,
			wantNeeded:  true,
			wantClamped: false,
		},
		{
			name:        "limiting needed - loud peaks",
			measured_I:  -20.0,
			measured_TP: -3.0,
			target_I:    -16.0,
			target_TP:   -2.0,
			// gain = -16.0 - (-20.0) = 4.0 dB
			// projected TP = -3.0 + 4.0 = 1.0 dBTP (exceeds -2.0)
			// ceiling = -2.0 - 4.0 - 2.0 = -8.0 dBTP
			wantCeiling: -8.0,
			wantNeeded:  true,
			wantClamped: false,
		},
		{
			name:        "no limiting needed - quiet peaks",
			measured_I:  -20.0,
			measured_TP: -10.0,
			target_I:    -16.0,
			target_TP:   -2.0,
			// gain = -16.0 - (-20.0) = 4.0 dB
			// projected TP = -10.0 + 4.0 = -6.0 dBTP (under -2.0)
			wantCeiling: 0,
			wantNeeded:  false,
			wantClamped: false,
		},
		{
			name:        "no limiting needed - needs attenuation",
			measured_I:  -12.0,
			measured_TP: -1.0,
			target_I:    -16.0,
			target_TP:   -2.0,
			// gain = -16.0 - (-12.0) = -4.0 dB (attenuation)
			// projected TP = -1.0 + (-4.0) = -5.0 dBTP (under -2.0)
			wantCeiling: 0,
			wantNeeded:  false,
			wantClamped: false,
		},
		{
			name:        "exactly at boundary - no limiting",
			measured_I:  -20.0,
			measured_TP: -6.0,
			target_I:    -16.0,
			target_TP:   -2.0,
			// gain = -16.0 - (-20.0) = 4.0 dB
			// projected TP = -6.0 + 4.0 = -2.0 dBTP (exactly at target)
			wantCeiling: 0,
			wantNeeded:  false,
			wantClamped: false,
		},
		{
			name:        "very quiet audio - clamped to minimum",
			measured_I:  -43.0,
			measured_TP: -20.0,
			target_I:    -16.0,
			target_TP:   -2.0,
			// gain = -16.0 - (-43.0) = 27.0 dB
			// projected TP = -20.0 + 27.0 = 7.0 dBTP (exceeds -2.0)
			// calculated ceiling = -2.0 - 27.0 - 2.0 = -31.0 dBTP
			// but -31.0 < -24.0, so clamped to -24.0 dBTP
			wantCeiling: minCeiling,
			wantNeeded:  true,
			wantClamped: true,
		},
		{
			name:        "just under minimum - clamped",
			measured_I:  -38.0,
			measured_TP: -15.0,
			target_I:    -16.0,
			target_TP:   -2.0,
			// gain = -16.0 - (-38.0) = 22.0 dB
			// projected TP = -15.0 + 22.0 = 7.0 dBTP (exceeds -2.0)
			// calculated ceiling = -2.0 - 22.0 - 2.0 = -26.0 dBTP
			// -26.0 < -24.0, so clamped to -24.0 dBTP
			wantCeiling: minCeiling,
			wantNeeded:  true,
			wantClamped: true,
		},
		{
			name:        "just above minimum - not clamped",
			measured_I:  -35.0,
			measured_TP: -15.0,
			target_I:    -16.0,
			target_TP:   -2.0,
			// gain = -16.0 - (-35.0) = 19.0 dB
			// projected TP = -15.0 + 19.0 = 4.0 dBTP (exceeds -2.0)
			// ceiling = -2.0 - 19.0 - 2.0 = -23.0 dBTP (above -24.0)
			wantCeiling: -23.0,
			wantNeeded:  true,
			wantClamped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ceiling, needed, clamped := calculateLimiterCeiling(
				tt.measured_I, tt.measured_TP, tt.target_I, tt.target_TP)

			if needed != tt.wantNeeded {
				t.Errorf("needed = %v, want %v", needed, tt.wantNeeded)
			}
			if clamped != tt.wantClamped {
				t.Errorf("clamped = %v, want %v", clamped, tt.wantClamped)
			}
			if needed && math.Abs(ceiling-tt.wantCeiling) > 0.01 {
				t.Errorf("ceiling = %.2f dBTP, want %.2f dBTP", ceiling, tt.wantCeiling)
			}
		})
	}
}
