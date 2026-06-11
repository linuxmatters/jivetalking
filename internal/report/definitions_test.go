package report

import (
	"strings"
	"testing"
)

// TestRequiredKeysHaveDefinitions pins the loudness + dynamics + spectral key set
// the renderer emits: every required key MUST resolve to a definition, so a
// missing or renamed entry fails here rather than producing a blank report cell.
func TestRequiredKeysHaveDefinitions(t *testing.T) {
	for _, key := range requiredKeys {
		if _, ok := Definitions[key]; !ok {
			t.Errorf("required key %q has no definition", key)
		}
	}
}

// TestSpectralThirteenCovered asserts all 13 aspectralstats fields are defined,
// guarding the spectral section against a dropped metric.
func TestSpectralThirteenCovered(t *testing.T) {
	spectral := []string{
		"mean", "variance", "centroid_hz", "spread_hz", "skewness",
		"kurtosis", "entropy", "flatness", "crest", "flux", "slope",
		"decrease", "rolloff_hz",
	}
	if len(spectral) != 13 {
		t.Fatalf("spectral set has %d keys, want 13", len(spectral))
	}
	for _, key := range spectral {
		if _, ok := Definitions[key]; !ok {
			t.Errorf("spectral key %q has no definition", key)
		}
	}
}

// TestDefinitionsNonEmptyLabelAndGloss asserts every catalogue entry carries a
// label and gloss. Unit may be empty (dimensionless ratios), so it is not pinned
// non-empty.
func TestDefinitionsNonEmptyLabelAndGloss(t *testing.T) {
	for key, def := range Definitions {
		if strings.TrimSpace(def.Label) == "" {
			t.Errorf("definition %q has empty label", key)
		}
		if strings.TrimSpace(def.Gloss) == "" {
			t.Errorf("definition %q has empty gloss", key)
		}
	}
}

// TestRequiredKeysCarryUnitWhereDimensioned asserts the dimensioned required
// metrics carry a non-empty unit. Dimensionless ratios (flat_factor, dc_offset,
// zero_crossings_rate, entropy, and the bare spectral moments) carry no unit by
// design, so they are excluded.
func TestRequiredKeysCarryUnitWhereDimensioned(t *testing.T) {
	dimensionless := map[string]bool{
		"flat_factor":         true,
		"dc_offset":           true,
		"zero_crossings_rate": true,
		"entropy":             true,
		"mean":                true,
		"variance":            true,
		"skewness":            true,
		"kurtosis":            true,
		"flatness":            true,
		"crest":               true,
		"flux":                true,
		"slope":               true,
		"decrease":            true,
	}
	for _, key := range requiredKeys {
		if dimensionless[key] {
			continue
		}
		def := Definitions[key]
		if strings.TrimSpace(def.Unit) == "" {
			t.Errorf("required dimensioned key %q has empty unit", key)
		}
	}
}

// TestNoRangeToMeaningTokens grep-asserts no gloss leaks a quality or
// range-to-meaning verdict. The catalogue is objective by mandate: definitions
// only, never interpretation.
func TestNoRangeToMeaningTokens(t *testing.T) {
	banned := []string{
		"warm", "bright", "good", "tonal", "broadband",
		"clean", "damaged", "harsh",
	}
	for key, def := range Definitions {
		lower := strings.ToLower(def.Gloss + " " + def.Label)
		for _, token := range banned {
			if strings.Contains(lower, token) {
				t.Errorf("definition %q contains banned range-to-meaning token %q: %q",
					key, token, def.Gloss)
			}
		}
	}
}
