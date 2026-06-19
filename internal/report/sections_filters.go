package report

import (
	"strings"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// This file holds the filter-chain and normalisation renderers: the Pass-2
// filter prose/param tables, the adaptive diagnostics block, and the Pass-3/4
// Peak Limiter + Loudnorm numbers. They share the flat paramRow shape (static
// descriptive labels keyed to configuration values, not metric-definition
// glosses), so they live together on the filter/normalisation change axis.

// paramRow is one Parameter | Value row for the filter and normalisation tables.
// Those blocks render configuration values keyed by static descriptive labels
// (fixed-design statements, not metric-definition glosses), so they use this
// flat shape rather than the metricRow/Definitions machinery the loudness,
// dynamics, and spectral tables use.
type paramRow struct {
	label string
	value string
}

// renderParamTable builds a Parameter | Value table from param rows.
func renderParamTable(rows []paramRow) string {
	body := make([][]string, 0, len(rows))
	for _, r := range rows {
		body = append(body, []string{r.label, r.value})
	}
	return mdTable([]string{"Parameter", "Value"}, body)
}

// =============================================================================
// Filter Chain
// =============================================================================

// renderFilters renders the Pass-2 filter chain in PROCESSING ORDER (downmix →
// high-pass → low-pass → noise removal → gate → levelling compressor → de-esser), one
// Parameter/Value sub-table per filter, plus the adaptive diagnostics block. Each
// filter's heading carries the factual fixed-design label (e.g. "Rumble high-pass:
// 80 Hz, 12 dB/oct") as a STATIC descriptive statement, not a per-file verdict;
// the rows carry the per-file parameters off filters.<filter>.*. The gate
// threshold_db/range_db are already dB-converted at record assembly
// (newFiltersBlock), so they render as-is. Reads only rec.Filters. Returns the
// empty string when the record carries no filters block (analysis-only).
func renderFilters(rec *processor.RunRecord) string {
	f := rec.Filters
	if f == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Filter Chain\n\n")

	b.WriteString("### Downmix\n\n")
	b.WriteString("Stereo-to-mono downmix using FFmpeg's standard downmix matrix.\n\n")

	b.WriteString("### Rumble high-pass\n\n")
	b.WriteString("Removes subsonic rumble before the gate. Fixed corner, 2-pole Butterworth (12 dB/oct), non-adaptive.\n\n")
	b.WriteString(renderParamTable([]paramRow{
		{"Enabled", boolCell(f.RumbleHighPass.Enabled)},
		{"Frequency (Hz)", formatMetric(f.RumbleHighPass.Frequency, 0)},
		{"Poles", formatInt(f.RumbleHighPass.Poles)},
		{"Width (Q)", formatMetric(f.RumbleHighPass.Width, 3)},
		{"Mix", formatMetric(f.RumbleHighPass.Mix, 2)},
		{"Transform", stringCell(f.RumbleHighPass.Transform)},
	}))
	b.WriteString("\n")

	b.WriteString("### Band-limit low-pass\n\n")
	b.WriteString("Unconditional 20.5 kHz band-limit (2-pole, 12 dB/oct), giving the encoder a consistent bandwidth. Non-adaptive.\n\n")
	b.WriteString(renderParamTable([]paramRow{
		{"Enabled", boolCell(f.BandlimitLowPass.Enabled)},
		{"Frequency (Hz)", formatMetric(f.BandlimitLowPass.Frequency, 0)},
		{"Poles", formatInt(f.BandlimitLowPass.Poles)},
		{"Width (Q)", formatMetric(f.BandlimitLowPass.Width, 3)},
		{"Mix", formatMetric(f.BandlimitLowPass.Mix, 2)},
		{"Transform", stringCell(f.BandlimitLowPass.Transform)},
	}))
	b.WriteString("\n")

	b.WriteString("### Noise removal\n\n")
	b.WriteString("anlmdn Non-Local Means denoiser at the source rate, followed by an afftdn FFT spectral denoise tail.\n\n")
	b.WriteString(renderParamTable([]paramRow{
		{"Enabled", boolCell(f.NoiseReduction.Enabled)},
		{"Strength (s)", formatMetric(f.NoiseReduction.Strength, 5)},
		{"Patch (s)", formatMetric(f.NoiseReduction.PatchSec, 4)},
		{"Research (s)", formatMetric(f.NoiseReduction.ResearchSec, 4)},
		{"Smooth (m)", formatMetric(f.NoiseReduction.Smooth, 0)},
		{"afftdn enabled", boolCell(f.NoiseReduction.AfftdnEnabled)},
		{"afftdn noise reduction (dB)", formatMetric(f.NoiseReduction.AfftdnNoiseReduction, 0)},
		{"afftdn noise floor (dB)", afftdnNoiseFloorCell(f.NoiseReduction.AfftdnNoiseFloor)},
		{"afftdn noise type", stringCell(f.NoiseReduction.AfftdnNoiseType)},
		{"afftdn band noise", stringCell(f.NoiseReduction.AfftdnBandNoise)},
		{"afftdn track noise", boolCell(f.NoiseReduction.AfftdnTrackNoise)},
	}))
	b.WriteString("\n")

	b.WriteString("### Speech gate\n\n")
	b.WriteString("Soft expander for inter-speech cleanup. Threshold and range are adapted per file; the threshold and range values below are in dB.\n\n")
	b.WriteString(renderParamTable([]paramRow{
		{"Enabled", boolCell(f.SpeechGate.Enabled)},
		{"Threshold (dB)", formatMetric(f.SpeechGate.Threshold, 2)},
		{"Ratio", formatMetric(f.SpeechGate.Ratio, 1)},
		{"Attack (ms)", formatMetric(f.SpeechGate.Attack, 0)},
		{"Release (ms)", formatMetric(f.SpeechGate.Release, 0)},
		{"Range (dB)", formatMetric(f.SpeechGate.Range, 2)},
		{"Knee", formatMetric(f.SpeechGate.Knee, 1)},
		{"Makeup", formatMetric(f.SpeechGate.Makeup, 1)},
		{"Detection", stringCell(f.SpeechGate.Detection)},
	}))
	b.WriteString("\n")

	b.WriteString("### Levelling compressor\n\n")
	b.WriteString("Gentle levelling. Threshold is speech-RMS-relative (adapted per file); ratio, attack, release, knee are fixed.\n\n")
	b.WriteString(renderParamTable([]paramRow{
		{"Enabled", boolCell(f.LevellingCompressor.Enabled)},
		{"Threshold (dB)", formatMetric(f.LevellingCompressor.Threshold, 2)},
		{"Ratio", formatMetric(f.LevellingCompressor.Ratio, 1)},
		{"Attack (ms)", formatMetric(f.LevellingCompressor.Attack, 0)},
		{"Release (ms)", formatMetric(f.LevellingCompressor.Release, 0)},
		{"Makeup (dB)", formatMetric(f.LevellingCompressor.Makeup, 1)},
		{"Knee", formatMetric(f.LevellingCompressor.Knee, 1)},
		{"Mix", formatMetric(f.LevellingCompressor.Mix, 2)},
	}))
	b.WriteString("\n")

	b.WriteString("### De-esser\n\n")
	b.WriteString("Sibilance reduction. Intensity is adapted from the speech-region sibilant-band excess; amount and frequency are fixed (FFmpeg deesser 0-1 normalised params).\n\n")
	b.WriteString(renderParamTable([]paramRow{
		{"Enabled", boolCell(f.Deesser.Enabled)},
		{"Intensity (i)", formatMetric(f.Deesser.Intensity, 2)},
		{"Amount (m)", formatMetric(f.Deesser.Amount, 2)},
		{"Frequency (f)", formatMetric(f.Deesser.Frequency, 2)},
	}))
	b.WriteString("\n")

	b.WriteString(renderFilterDiagnostics(f.Diagnostics))

	return b.String()
}

// renderFilterDiagnostics renders the adaptive-adaptation rationale from
// filters.diagnostics.* as objective values (separation, clamp reason, gate
// depth, etc.) - no verdicts. Returns the empty string when no diagnostics
// block is present.
func renderFilterDiagnostics(d *processor.AdaptiveDiagnostics) string {
	if d == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("### Adaptation diagnostics\n\n")
	b.WriteString(renderParamTable([]paramRow{
		{"Low-pass reason", stringCell(d.BandlimitLPReason)},
		{"Gate dynamic range (dB)", formatMetric(d.SpeechGateDynamicRange, 2)},
		{"Quiet-speech estimate (dBFS)", formatMetricDB(d.SpeechGateQuietSpeechEstimate, 2)},
		{"Speech separation (dB)", formatMetric(d.SpeechGateSpeechSeparation, 2)},
		{"Speech headroom (dB)", formatMetric(d.SpeechGateSpeechHeadroom, 2)},
		{"Gate threshold unclamped (dB)", formatMetric(d.SpeechGateThresholdUnclamped, 2)},
		{"Clamp reason", stringCell(d.SpeechGateClampReason)},
		{"Gate depth (dB)", formatMetric(d.SpeechGateDepthDB, 2)},
		{"afftdn enabled", boolCell(d.AfftdnEnabled)},
		{"afftdn noise floor (dB)", afftdnNoiseFloorCell(d.AfftdnNoiseFloorDB)},
		{"afftdn noise type", stringCell(d.AfftdnNoiseType)},
		{"afftdn disable reason", stringCell(d.AfftdnDisableReason)},
	}))
	return b.String()
}

// afftdnNoiseFloorCell renders the afftdn nf value, showing the placeholder when
// unset (zero or non-negative). A set floor is always negative. The value is the
// VAD momentary-LUFS percentile floor, re-clamped to afftdn's [-80, -20] dB range.
func afftdnNoiseFloorCell(floor float64) string {
	if floor >= 0 {
		return placeholder
	}
	return formatMetric(floor, 2)
}

// =============================================================================
// Normalisation (Peak Limiter + Loudnorm)
// =============================================================================

// renderNormalisation renders the Pass 3/4 Peak Limiter and Loudnorm numbers from
// normalisation.*. Reads the wrapped *NormalisationResult via the record's
// Result() read seam (the wrapper type is unexported, like the elected-profile
// seams in renderRegions). Returns the empty string when the record carries no
// normalisation block (analysis-only).
//
// within_target is rendered as a RAW SIGNED LU deviation number, NOT a
// "✓ Within target / ⚠ Outside tolerance" boolean: it
// is output_integrated_lufs (the loudnorm-measured output) minus
// normalisation.effective_target_lufs (EffectiveTargetI), precomputed at record
// assembly (LoudnormParsed.TargetDeviation) and formatted via formatMetricSigned.
// No glyph, no boolean.
func renderNormalisation(rec *processor.RunRecord) string {
	r := rec.Normalisation.Result()
	if r == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Peak Limiter\n\n")
	b.WriteString("Transparent limiter that creates true-peak headroom so loudnorm reaches the target in linear mode. Pre-gain raises very quiet recordings before limiting.\n\n")
	b.WriteString(renderParamTable([]paramRow{
		{"Enabled", boolCell(r.LimiterEnabled)},
		{"Ceiling (dBTP)", formatMetricDB(r.LimiterCeiling, 2)},
		{"Gain required (dB)", formatMetric(r.LimiterGain, 2)},
		{"Filtered true peak (dBTP)", formatMetricDB(r.LimiterFilteredTP, 2)},
		{"Pre-gain (dB)", formatMetric(r.PreGainDB, 2)},
		{"Ceiling clamped", boolCell(r.LimiterClamped)},
	}))
	b.WriteString("\n")

	b.WriteString("## Loudnorm\n\n")
	b.WriteString("EBU R128 loudness normalisation in linear mode using the Pass-3 measured input statistics.\n\n")
	rows := []paramRow{
		{"Requested target (LUFS)", formatMetricLUFS(r.RequestedTargetI, 2)},
		{"Effective target (LUFS)", formatMetricLUFS(r.EffectiveTargetI, 2)},
		{"Gain applied (dB)", formatMetric(r.GainApplied, 2)},
		{"Linear mode forced", boolCell(r.LinearModeForced)},
		{"Input loudness (LUFS)", formatMetricLUFS(r.InputLUFS, 2)},
		{"Input true peak (dBTP)", formatMetricDB(r.InputTP, 2)},
		{"Output loudness (LUFS)", formatMetricLUFS(r.OutputLUFS, 2)},
		{"Output true peak (dBTP)", formatMetricDB(r.OutputTP, 2)},
	}
	if m := r.LoudnormParsed; m != nil {
		rows = append(rows,
			paramRow{"Measured input integrated (LUFS)", loudnormValueCell(m.InputI, fmtLUFS)},
			paramRow{"Measured input true peak (dBTP)", loudnormValueCell(m.InputTP, fmtPeakDB)},
			paramRow{"Measured input LRA (LU)", loudnormValueCell(m.InputLRA, fmtSpectral)},
			paramRow{"Measured input threshold (LUFS)", loudnormValueCell(m.InputThresh, fmtLUFS)},
			paramRow{"Measured output integrated (LUFS)", loudnormValueCell(m.OutputI, fmtLUFS)},
			paramRow{"Measured output true peak (dBTP)", loudnormValueCell(m.OutputTP, fmtPeakDB)},
			paramRow{"Measured output LRA (LU)", loudnormValueCell(m.OutputLRA, fmtSpectral)},
			paramRow{"Measured output threshold (LUFS)", loudnormValueCell(m.OutputThresh, fmtLUFS)},
			paramRow{"Normalisation type", stringCell(r.LoudnormStats.NormalizationType)},
		)
	}
	// Raw signed LU deviation from the effective target: the loudnorm-measured
	// output integrated loudness minus the effective target. A signed number,
	// never a boolean or glyph. Precomputed at record assembly (LoudnormParsed).
	rows = append(rows, paramRow{"Deviation from target (LU)", targetDeviationCell(r)})
	b.WriteString(renderParamTable(rows))

	return b.String()
}

// targetDeviationCell renders the raw signed LU deviation of the loudnorm-measured
// output integrated loudness from the effective target (output_integrated_lufs
// minus effective_target_lufs), precomputed at record assembly. Returns the
// placeholder when the deviation is unavailable (no stats or an unparseable
// measured output), so the cell never fabricates a deviation.
func targetDeviationCell(r *processor.NormalisationResult) string {
	if r.LoudnormParsed == nil || !r.LoudnormParsed.TargetDeviation.OK {
		return placeholder
	}
	return formatMetricSigned(r.LoudnormParsed.TargetDeviation.Value, 2)
}

// loudnormValueCell formats a typed loudnorm measurement through the given metric
// rule, returning the placeholder when the value did not parse (graceful: the cell
// never fabricates a value).
func loudnormValueCell(v processor.LoudnormValue, format metricFormat) string {
	if !v.OK {
		return placeholder
	}
	return formatByRule(v.Value, format, 2)
}
