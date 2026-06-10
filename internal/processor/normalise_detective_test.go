package processor

import (
	"strings"
	"testing"
)

// TestLoudnormFellBackToDynamic asserts the detective check fires only when
// loudnorm's reported normalization_type is dynamic (case-insensitive), emits
// the warning, and stays quiet for linear or nil stats.
func TestLoudnormFellBackToDynamic(t *testing.T) {
	cases := []struct {
		name       string
		stats      *LoudnormStats
		wantResult bool
		wantWarn   bool
	}{
		{
			name:       "dynamic fallback warns and reports true",
			stats:      &LoudnormStats{NormalizationType: "dynamic"},
			wantResult: true,
			wantWarn:   true,
		},
		{
			name:       "dynamic with mixed case and whitespace still matches",
			stats:      &LoudnormStats{NormalizationType: "  DYNAMIC  "},
			wantResult: true,
			wantWarn:   true,
		},
		{
			name:       "linear is silent",
			stats:      &LoudnormStats{NormalizationType: "linear"},
			wantResult: false,
			wantWarn:   false,
		},
		{
			name:       "nil stats is silent",
			stats:      nil,
			wantResult: false,
			wantWarn:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var logged []string
			log := debugLogger(func(format string, args ...any) {
				logged = append(logged, format)
			})

			got := loudnormFellBackToDynamic(tc.stats, "EP83.flac", log)

			if got != tc.wantResult {
				t.Errorf("loudnormFellBackToDynamic = %v, want %v", got, tc.wantResult)
			}

			warned := false
			for _, line := range logged {
				if strings.Contains(line, "DYNAMIC mode") {
					warned = true
				}
			}
			if warned != tc.wantWarn {
				t.Errorf("warning emitted = %v, want %v (logged: %v)", warned, tc.wantWarn, logged)
			}
		})
	}
}
