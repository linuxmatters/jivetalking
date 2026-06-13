package processor

import "slices"

// newIntervalSummary derives the inline interval_summary from the full per-250ms
// IntervalSamples series. This is the sole owner of the RMS-distribution and
// largest-gap maths; the inline summary is the canonical diagnostic. Returns nil
// for an empty series so omitempty drops the block.
//
// Summary contract:
//   - Count = len(samples).
//   - The RMS distribution and largest gap are populated only when at least 10
//     intervals sit above digital silence (RMSLevel > -120); below that RMS and
//     LargestGapDB stay nil.
//   - percentile indices use integer index selection (len/10, len/4, len/2,
//     len*3/4, len*9/10), not interpolation.
func newIntervalSummary(samples []IntervalSample) *IntervalSummary {
	if len(samples) == 0 {
		return nil
	}

	summary := &IntervalSummary{Count: len(samples)}

	// Exclude digital silence via the RMSLevel > -120 filter.
	rmsValues := make([]float64, 0, len(samples))
	for _, interval := range samples {
		if interval.RMSLevel > -120 {
			rmsValues = append(rmsValues, interval.RMSLevel)
		}
	}
	if len(rmsValues) < 10 {
		return summary
	}

	sorted := make([]float64, len(rmsValues))
	copy(sorted, rmsValues)
	slices.Sort(sorted)

	summary.RMS = &RMSDistribution{
		Min: sorted[0],
		P10: sorted[len(sorted)/10],
		P25: sorted[len(sorted)/4],
		P50: sorted[len(sorted)/2],
		P75: sorted[len(sorted)*3/4],
		P90: sorted[len(sorted)*9/10],
		Max: sorted[len(sorted)-1],
	}

	// Largest jump between adjacent sorted RMS values (the room-tone/speech
	// boundary signal), reported in dB.
	var largestGap float64
	for i := 1; i < len(sorted); i++ {
		if gap := sorted[i] - sorted[i-1]; gap > largestGap {
			largestGap = gap
		}
	}
	summary.LargestGapDB = &largestGap

	return summary
}
