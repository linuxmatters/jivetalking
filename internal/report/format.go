package report

import (
	"math"
	"strconv"
)

// placeholder is the stable token rendered for any non-finite float leaf
// (NaN, +Inf, -Inf). Every section renderer formats float cells through
// formatFloat so the placeholder is uniform across the report. Locked here;
// downstream numeric formatters (mdtable.go, 1.2) build on top of this rule.
const placeholder = "-"

// formatFloat renders a float64 leaf to a cell string. Non-finite values
// (NaN, +Inf, -Inf) map to the stable placeholder; finite values format with
// the given number of decimal places.
func formatFloat(v float64, decimals int) string {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return placeholder
	}
	return strconv.FormatFloat(v, 'f', decimals, 64)
}
