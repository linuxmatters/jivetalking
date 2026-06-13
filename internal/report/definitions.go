package report

// Objective metric catalogue for the Markdown report.
//
// Source of record: docs/Spectral-Metrics-Reference.md (objective definitions,
// units, computation, and source filter for every metric Jivetalking emits).
// Glosses here are transcribed from that reference and cross-checked against the
// audio-metrics skill SKILL.md for the precise ffmpeg computation. Map keys are
// the exact RunRecord JSON field names (AUDIO-MEASUREMENTS.md §8.4 unit-suffix
// convention); confirm key strings against an emitted record before editing.
//
// Risk: definition drift. If a metric's computation, unit, or key changes in the
// reference or the record schema, update the matching entry here. The
// required-key test (definitions_test.go) fails when a renderer-needed key has no
// definition, but it cannot detect a stale gloss, keep this file aligned with
// the reference by hand.
//
// Glosses are OBJECTIVE: what the metric is and, in brief, how it is computed. No
// interpretation, no quality verdict, no range-to-meaning mapping.

// Definition describes one metric for the report: a human-readable label, the
// unit string rendered alongside the value, and a one-line objective gloss.
type Definition struct {
	Label string
	Unit  string
	Gloss string
}

// Definitions maps a RunRecord JSON field name to its objective definition. The
// same key is reused across stages (input/filtered/final) and scopes (whole-file,
// region samples), so spectral and dynamics keys appear once here and the
// renderer looks them up per cell.
var Definitions = map[string]Definition{
	// -------------------------------------------------------------------------
	// Loudness (ebur128 + loudnorm)
	// -------------------------------------------------------------------------
	"integrated_lufs": {
		Label: "Integrated loudness",
		Unit:  "LUFS",
		Gloss: "Gated programme loudness over the whole input, BS.1770 K-weighted mean-square with two-stage gating.",
	},
	"true_peak_dbtp": {
		Label: "True peak",
		Unit:  "dBTP",
		Gloss: "Inter-sample peak of the libswresample-oversampled signal.",
	},
	"lra_lu": {
		Label: "Loudness range",
		Unit:  "LU",
		Gloss: "Statistical spread of the 3 s short-term loudness distribution (lra_high minus lra_low).",
	},
	"sample_peak_dbfs": {
		Label: "Sample peak",
		Unit:  "dBFS",
		Gloss: "Largest digital sample without oversampling, 20*log10(sample_peak).",
	},
	"momentary_lufs": {
		Label: "Momentary loudness",
		Unit:  "LUFS",
		Gloss: "BS.1770 loudness over a 400 ms sliding window.",
	},
	"short_term_lufs": {
		Label: "Short-term loudness",
		Unit:  "LUFS",
		Gloss: "BS.1770 loudness over a 3 s sliding window.",
	},
	"thresh_lufs": {
		Label: "Gating threshold",
		Unit:  "LUFS",
		Gloss: "Relative gating threshold, -10 LU below the absolute-gated loudness mean.",
	},
	"target_offset_db": {
		Label: "Target offset",
		Unit:  "LU",
		Gloss: "Residual gap to the target integrated loudness, target_i minus output_i.",
	},

	// -------------------------------------------------------------------------
	// Dynamics (astats, time domain)
	// -------------------------------------------------------------------------
	"rms_level_dbfs": {
		Label: "RMS level",
		Unit:  "dBFS",
		Gloss: "RMS amplitude of the samples, 20*log10(sqrt(sum(x^2)/N)).",
	},
	"peak_level_dbfs": {
		Label: "Peak level",
		Unit:  "dBFS",
		Gloss: "Largest absolute sample, 20*log10(max(|min|,|max|)).",
	},
	"crest_factor_astats_db": {
		Label: "Crest factor",
		Unit:  "dB",
		Gloss: "Time-domain peak-to-RMS ratio (peak/RMS), stored converted linear to dB.",
	},
	"crest_factor_db": {
		Label: "Crest factor",
		Unit:  "dB",
		Gloss: "Region-scoped time-domain peak-to-RMS ratio (peak/RMS), stored converted linear to dB.",
	},
	"dynamic_range_db": {
		Label: "Dynamic range",
		Unit:  "dB",
		Gloss: "Span between loudest and quietest non-zero sample, 20*log10(2*max(|min|,|max|)/min_nonzero).",
	},
	"min_level_dbfs": {
		Label: "Min level",
		Unit:  "dBFS",
		Gloss: "Smallest signed sample, converted to dBFS.",
	},
	"max_level_dbfs": {
		Label: "Max level",
		Unit:  "dBFS",
		Gloss: "Largest signed sample, converted to dBFS.",
	},
	"rms_peak_dbfs": {
		Label: "RMS peak",
		Unit:  "dBFS",
		Gloss: "Maximum per-window RMS over the short measurement window.",
	},
	"rms_trough_dbfs": {
		Label: "RMS trough",
		Unit:  "dBFS",
		Gloss: "Minimum per-window RMS over the short measurement window.",
	},
	"flat_factor": {
		Label: "Flat factor",
		Unit:  "",
		Gloss: "Run-length flatness at the min/max levels, 20*log10((min_runs+max_runs)/(min_count+max_count)).",
	},
	"dc_offset": {
		Label: "DC offset",
		Unit:  "",
		Gloss: "Mean sample amplitude, sum(x)/N.",
	},
	"zero_crossings_rate": {
		Label: "Zero-crossings rate",
		Unit:  "",
		Gloss: "Fraction of sample pairs that change sign, zero_crossings/N.",
	},
	"zero_crossings_count": {
		Label: "Zero-crossings count",
		Unit:  "count",
		Gloss: "Number of sample pairs that change sign.",
	},
	"bit_depth": {
		Label: "Bit depth",
		Unit:  "bits",
		Gloss: "Effective bit depth estimated from the sample data.",
	},
	"entropy": {
		Label: "Entropy",
		Unit:  "",
		Gloss: "Magnitude-weighted spectral entropy, -sum(mag*ln(mag+eps))/ln(N); for astats stages, the sample-value distribution entropy.",
	},
	"noise_floor_count": {
		Label: "Noise-floor count",
		Unit:  "count",
		Gloss: "Number of samples at or below the measured noise-floor level (astats).",
	},
	"number_of_samples": {
		Label: "Number of samples",
		Unit:  "count",
		Gloss: "Count of samples in the measured stream (astats).",
	},
	"max_difference": {
		Label: "Max difference",
		Unit:  "",
		Gloss: "Largest absolute difference between two consecutive samples (astats).",
	},
	"min_difference": {
		Label: "Min difference",
		Unit:  "",
		Gloss: "Smallest absolute difference between two consecutive samples (astats).",
	},
	"mean_difference": {
		Label: "Mean difference",
		Unit:  "",
		Gloss: "Mean absolute difference between consecutive samples (astats).",
	},
	"rms_difference": {
		Label: "RMS difference",
		Unit:  "",
		Gloss: "RMS of the absolute differences between consecutive samples (astats).",
	},

	// -------------------------------------------------------------------------
	// Spectral (aspectralstats, the 13 fields)
	// -------------------------------------------------------------------------
	"mean": {
		Label: "Spectral mean",
		Unit:  "",
		Gloss: "Arithmetic mean of the magnitude bins, sum(mag[n])/size.",
	},
	"variance": {
		Label: "Spectral variance",
		Unit:  "",
		Gloss: "Population variance of the magnitudes about the mean, sum((mag[n]-mean)^2)/size.",
	},
	"centroid_hz": {
		Label: "Spectral centroid",
		Unit:  "Hz",
		Gloss: "Magnitude-weighted mean frequency of the spectrum, sum(mag[n]*f[n])/sum(mag[n]).",
	},
	"spread_hz": {
		Label: "Spectral spread",
		Unit:  "Hz",
		Gloss: "Magnitude-weighted standard deviation of frequency about the centroid.",
	},
	"skewness": {
		Label: "Spectral skewness",
		Unit:  "",
		Gloss: "Third standardised spectral moment about the centroid.",
	},
	"kurtosis": {
		Label: "Spectral kurtosis",
		Unit:  "",
		Gloss: "Fourth standardised (Pearson) spectral moment about the centroid; not excess kurtosis.",
	},
	"flatness": {
		Label: "Spectral flatness",
		Unit:  "",
		Gloss: "Geometric mean over arithmetic mean of the magnitudes, a 0-1 linear ratio.",
	},
	"crest": {
		Label: "Spectral crest",
		Unit:  "",
		Gloss: "Peak magnitude bin over mean magnitude bin, max(mag[n])/mean(mag[n]).",
	},
	"flux": {
		Label: "Spectral flux",
		Unit:  "",
		Gloss: "L2 distance between this frame's and the previous frame's magnitude spectrum.",
	},
	"slope": {
		Label: "Spectral slope",
		Unit:  "",
		Gloss: "Linear-regression slope of magnitude against normalised bin index.",
	},
	"decrease": {
		Label: "Spectral decrease",
		Unit:  "",
		Gloss: "Relative spectral decrease from the first bin, sum((mag[n]-mag[0])/n)/sum(mag[n]).",
	},
	"rolloff_hz": {
		Label: "Spectral roll-off",
		Unit:  "Hz",
		Gloss: "Frequency below which 85% of the cumulative magnitude lies.",
	},

	// -------------------------------------------------------------------------
	// Noise (input-only noise domain)
	// -------------------------------------------------------------------------
	"floor_dbfs": {
		Label: "Noise floor",
		Unit:  "dBFS",
		Gloss: "Elected noise floor; overwritten by the room-tone RMS when a profile is elected.",
	},
	"floor_source": {
		Label: "Floor source",
		Unit:  "",
		Gloss: "Origin of the elected floor: astats, rms_estimate, ebur128_estimate, or silence_profile.",
	},
	"floor_prescan_dbfs": {
		Label: "Pre-scan floor",
		Unit:  "dBFS",
		Gloss: "Noise floor estimated from the per-interval data, feeding room-tone detection.",
	},
	"floor_astats_dbfs": {
		Label: "astats floor",
		Unit:  "dBFS",
		Gloss: "FFmpeg astats noise-floor estimate, the minimum local peak over the sliding window.",
	},
	"room_tone_detect_level_dbfs": {
		Label: "Room-tone detect level",
		Unit:  "dBFS",
		Gloss: "Adaptive threshold below which an interval is treated as a room-tone candidate.",
	},
	"voice_activated": {
		Label: "Voice activated",
		Unit:  "",
		Gloss: "True when at least 95% of room-tone candidates are digital silence.",
	},
	"reduction_headroom_db": {
		Label: "Reduction headroom",
		Unit:  "dB",
		Gloss: "Gap in dB between the noise floor and quiet speech.",
	},

	// -------------------------------------------------------------------------
	// Regions: elected profile bounds and election-only fields
	// -------------------------------------------------------------------------
	"measured_floor_dbfs": {
		Label: "Measured floor",
		Unit:  "dBFS",
		Gloss: "RMS level of the elected room-tone region.",
	},
	"start_s": {
		Label: "Start",
		Unit:  "s",
		Gloss: "Start time of the elected region from the input origin.",
	},
	"duration_s": {
		Label: "Duration",
		Unit:  "s",
		Gloss: "Length of the elected region.",
	},
	"spectral_centroid_hz": {
		Label: "Spectral centroid",
		Unit:  "Hz",
		Gloss: "Magnitude-weighted mean frequency of the elected region's spectrum.",
	},
	"spectral_flatness": {
		Label: "Spectral flatness",
		Unit:  "",
		Gloss: "Geometric over arithmetic mean of the elected region's magnitudes, a 0-1 ratio.",
	},
	"spectral_kurtosis": {
		Label: "Spectral kurtosis",
		Unit:  "",
		Gloss: "Fourth standardised spectral moment of the elected region.",
	},
	"voicing_density": {
		Label: "Voicing density",
		Unit:  "",
		Gloss: "Proportion of voiced intervals over the elected speech region, 0-1.",
	},
	"speech_band_body_rms_dbfs": {
		Label: "Body-band RMS",
		Unit:  "dBFS",
		Gloss: "RMS over the 1-3 kHz vocal-presence band of the elected speech region.",
	},
	"speech_band_sib_rms_dbfs": {
		Label: "Sibilant-band RMS",
		Unit:  "dBFS",
		Gloss: "RMS over the 6-9 kHz sibilant band of the elected speech region.",
	},
	"score": {
		Label: "Score",
		Unit:  "",
		Gloss: "Composite candidate-ranking score of the elected region.",
	},

	// -------------------------------------------------------------------------
	// Interval summary (per-250ms RMS distribution + gap)
	// -------------------------------------------------------------------------
	"interval_count": {
		Label: "Interval count",
		Unit:  "count",
		Gloss: "Number of 250 ms intervals sampled over the input.",
	},
	"largest_gap_db": {
		Label: "Largest gap",
		Unit:  "dB",
		Gloss: "Biggest jump between adjacent sorted interval RMS values, the room-tone/speech boundary signal.",
	},
	"rms_dist_min_dbfs": {
		Label: "RMS min",
		Unit:  "dBFS",
		Gloss: "Lowest interval RMS above digital silence.",
	},
	"rms_dist_p10_dbfs": {
		Label: "RMS p10",
		Unit:  "dBFS",
		Gloss: "10th-percentile interval RMS above digital silence.",
	},
	"rms_dist_p25_dbfs": {
		Label: "RMS p25",
		Unit:  "dBFS",
		Gloss: "25th-percentile interval RMS above digital silence.",
	},
	"rms_dist_p50_dbfs": {
		Label: "RMS p50",
		Unit:  "dBFS",
		Gloss: "Median interval RMS above digital silence.",
	},
	"rms_dist_p75_dbfs": {
		Label: "RMS p75",
		Unit:  "dBFS",
		Gloss: "75th-percentile interval RMS above digital silence.",
	},
	"rms_dist_p90_dbfs": {
		Label: "RMS p90",
		Unit:  "dBFS",
		Gloss: "90th-percentile interval RMS above digital silence.",
	},
	"rms_dist_max_dbfs": {
		Label: "RMS max",
		Unit:  "dBFS",
		Gloss: "Highest interval RMS above digital silence.",
	},
}

// requiredKeys is the set of RunRecord field names the loudness, dynamics, and
// spectral sections emit and that MUST have a definition. The renderers and the
// completeness test assert every key here resolves in Definitions; a missing
// entry fails the test. Authored (not in the reference doc) keys are the astats
// *_difference, noise_floor_count, and number_of_samples fields, defined
// minimally and factually above.
var requiredKeys = []string{
	// Loudness
	"integrated_lufs",
	"true_peak_dbtp",
	"lra_lu",
	"sample_peak_dbfs",
	"momentary_lufs",
	"short_term_lufs",
	"thresh_lufs",
	"target_offset_db",

	// Dynamics
	"rms_level_dbfs",
	"peak_level_dbfs",
	"crest_factor_astats_db",
	"dynamic_range_db",
	"min_level_dbfs",
	"max_level_dbfs",
	"rms_peak_dbfs",
	"rms_trough_dbfs",
	"flat_factor",
	"dc_offset",
	"zero_crossings_rate",
	"bit_depth",
	"entropy",

	// Spectral (13)
	"mean",
	"variance",
	"centroid_hz",
	"spread_hz",
	"skewness",
	"kurtosis",
	"entropy",
	"flatness",
	"crest",
	"flux",
	"slope",
	"decrease",
	"rolloff_hz",
}

// DefinitionFor returns the objective definition for a RunRecord field name and
// whether one exists. Renderers use this to pair each value cell with its label,
// unit, and gloss.
func DefinitionFor(key string) (Definition, bool) {
	d, ok := Definitions[key]
	return d, ok
}
