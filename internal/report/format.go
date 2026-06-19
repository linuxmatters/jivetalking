package report

import (
	"math"
	"strconv"
)

// placeholder is the stable token rendered for any non-finite float leaf
// (NaN, +Inf, -Inf). Every section renderer formats float cells through
// formatFloat so the placeholder is uniform across the report; the numeric
// formatters in mdtable.go build on top of this rule.
const placeholder = "-"

// nonFiniteToken reports whether value is a non-finite float the formatters
// render as the placeholder, returning the placeholder and true when so. infSign
// selects which infinities count: 0 catches both directions, +1 catches only
// +Inf (the dB/LUFS formatters treat -Inf as a measurable digital-silence floor,
// not a placeholder). NaN always counts. Every float formatter guards through
// this so the placeholder rule stays in one place.
func nonFiniteToken(value float64, infSign int) (string, bool) {
	if math.IsNaN(value) || math.IsInf(value, infSign) {
		return placeholder, true
	}
	return "", false
}

// formatFloat renders a float64 leaf to a cell string. Non-finite values
// (NaN, +Inf, -Inf) map to the stable placeholder; finite values format with
// the given number of decimal places.
func formatFloat(v float64, decimals int) string {
	if token, ok := nonFiniteToken(v, 0); ok {
		return token
	}
	return strconv.FormatFloat(v, 'f', decimals, 64)
}
