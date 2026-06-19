package report

import (
	"strconv"
	"strings"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// This file holds the per-domain section renderers: Header, Processing Summary,
// Loudness, Dynamics, Spectral, Noise Floor, Regions, and Interval Summary. Each
// is a pure func(...) string reading ONLY the run record (and Timings for the
// summary) - no AudioMeasurements, no .json re-read, no internal/logging. The
// metric-table engine lives in metricrow.go; the filter/normalisation and
// spectrogram renderers live in sections_filters.go and
// sections_spectrograms.go.

// =============================================================================
// Header
// =============================================================================

// renderHeader renders the run provenance block: input file, processed-at, audio
// duration, sample rate, and channel layout. Reads only rec.Run.
func renderHeader(rec *processor.RunRecord) string {
	var b strings.Builder
	b.WriteString("# Audio Processing Report\n\n")
	b.WriteString("## Run\n\n")

	rows := [][]string{
		{"Input file", rec.Run.InputFile},
		{"Processed at", rec.Run.ProcessedAt},
		{"Duration", formatDuration(durationFromSeconds(rec.Run.DurationS))},
		{"Sample rate", formatSampleRate(rec.Run.SampleRateHz)},
		{"Channels", channelName(rec.Run.Channels)},
	}
	b.WriteString(mdTable([]string{"Field", "Value"}, rows))
	return b.String()
}

// =============================================================================
// Processing Summary
// =============================================================================

// renderProcessingSummary renders the pass durations, adaptation time, and
// real-time factor. It reads ONLY timings; the record carries no run timing.
// Returns the empty string when timings is the zero value (analysis-only mode has
// no processing timings), so the orchestrator omits the section entirely.
func renderProcessingSummary(timings Timings) string {
	if timings == (Timings{}) {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Processing Summary\n\n")

	rows := make([][]string, 0, 7)
	addDurationRow := func(label string, d time.Duration) {
		if d > 0 {
			rows = append(rows, []string{label, formatDuration(d)})
		}
	}
	addDurationRow("Pass 1 (analysis)", timings.Pass1)
	addDurationRow("Pass 2 (filter chain)", timings.Pass2)
	addDurationRow("Pass 3 (loudnorm measure)", timings.Pass3)
	addDurationRow("Pass 4 (loudnorm apply)", timings.Pass4)
	addDurationRow("Analysis", timings.Analysis)
	addDurationRow("Adaptation", timings.Adaptation)
	if timings.RealTimeFactor > 0 {
		rows = append(rows, []string{"Real-time factor", formatFloat(timings.RealTimeFactor, 1) + "x"})
	}

	b.WriteString(mdTable([]string{"Stage", "Duration"}, rows))
	return b.String()
}

// =============================================================================
// Loudness
// =============================================================================

// renderLoudness renders the EBU R128 loudness table, one row per loudness
// metric, with Input/Filtered/Final value columns (absent stages omitted). The
// input stage is *InputLoudnessMetrics; filtered/final are *OutputLoudnessMetrics
// (different Go types, same JSON keys), so each row's getters read the value off
// whichever stage struct is present.
func renderLoudness(rec *processor.RunRecord) string {
	in := rec.Loudness.Stages.Input
	filt := rec.Loudness.Stages.Filtered
	final := rec.Loudness.Stages.Final

	rows := []metricRow{
		{
			key: "integrated_lufs", format: fmtLUFS,
			input: stageGetter(in, func(m *processor.InputLoudnessMetrics) float64 { return m.InputI }),
			filt:  stageGetter(filt, func(m *processor.OutputLoudnessMetrics) float64 { return m.OutputI }),
			final: stageGetter(final, func(m *processor.OutputLoudnessMetrics) float64 { return m.OutputI }),
		},
		{
			key: "true_peak_dbtp", format: fmtPeakDB,
			input: stageGetter(in, func(m *processor.InputLoudnessMetrics) float64 { return m.InputTP }),
			filt:  stageGetter(filt, func(m *processor.OutputLoudnessMetrics) float64 { return m.OutputTP }),
			final: stageGetter(final, func(m *processor.OutputLoudnessMetrics) float64 { return m.OutputTP }),
		},
		{
			key: "lra_lu", format: fmtSpectral,
			input: stageGetter(in, func(m *processor.InputLoudnessMetrics) float64 { return m.InputLRA }),
			filt:  stageGetter(filt, func(m *processor.OutputLoudnessMetrics) float64 { return m.OutputLRA }),
			final: stageGetter(final, func(m *processor.OutputLoudnessMetrics) float64 { return m.OutputLRA }),
		},
		{
			key: "thresh_lufs", format: fmtLUFS,
			input: stageGetter(in, func(m *processor.InputLoudnessMetrics) float64 { return m.InputThresh }),
			filt:  stageGetter(filt, func(m *processor.OutputLoudnessMetrics) float64 { return m.OutputThresh }),
			final: stageGetter(final, func(m *processor.OutputLoudnessMetrics) float64 { return m.OutputThresh }),
		},
		{
			key: "momentary_lufs", format: fmtLUFS,
			input: stageGetter(in, func(m *processor.InputLoudnessMetrics) float64 { return m.MomentaryLoudness }),
			filt:  stageGetter(filt, func(m *processor.OutputLoudnessMetrics) float64 { return m.MomentaryLoudness }),
			final: stageGetter(final, func(m *processor.OutputLoudnessMetrics) float64 { return m.MomentaryLoudness }),
		},
		{
			key: "short_term_lufs", format: fmtLUFS,
			input: stageGetter(in, func(m *processor.InputLoudnessMetrics) float64 { return m.ShortTermLoudness }),
			filt:  stageGetter(filt, func(m *processor.OutputLoudnessMetrics) float64 { return m.ShortTermLoudness }),
			final: stageGetter(final, func(m *processor.OutputLoudnessMetrics) float64 { return m.ShortTermLoudness }),
		},
		{
			key: "sample_peak_dbfs", format: fmtDB,
			input: stageGetter(in, func(m *processor.InputLoudnessMetrics) float64 { return m.SamplePeak }),
			filt:  stageGetter(filt, func(m *processor.OutputLoudnessMetrics) float64 { return m.SamplePeak }),
			final: stageGetter(final, func(m *processor.OutputLoudnessMetrics) float64 { return m.SamplePeak }),
		},
		{
			key: "target_offset_db", format: fmtSigned,
			input: stageGetter(in, func(m *processor.InputLoudnessMetrics) float64 { return m.TargetOffset }),
			filt:  stageGetter(filt, func(m *processor.OutputLoudnessMetrics) float64 { return m.TargetOffset }),
			final: stageGetter(final, func(m *processor.OutputLoudnessMetrics) float64 { return m.TargetOffset }),
		},
	}

	var b strings.Builder
	b.WriteString("## Loudness\n\n")
	b.WriteString(renderMetricTable(rows))
	return b.String()
}

// =============================================================================
// Dynamics
// =============================================================================

// renderDynamics renders the astats time-domain table. All three stages share the
// *DynamicsMetrics Go type (input/filtered/final), so the getters read the same
// fields off whichever stage is present.
func renderDynamics(rec *processor.RunRecord) string {
	in := rec.Dynamics.Stages.Input
	filt := rec.Dynamics.Stages.Filtered
	final := rec.Dynamics.Stages.Final

	row := func(key string, format metricFormat, f func(*processor.DynamicsMetrics) float64) metricRow {
		return metricRow{
			key: key, format: format,
			input: stageGetter(in, f), filt: stageGetter(filt, f), final: stageGetter(final, f),
		}
	}

	rows := []metricRow{
		row("rms_level_dbfs", fmtDB, func(m *processor.DynamicsMetrics) float64 { return m.RMSLevel }),
		row("peak_level_dbfs", fmtDB, func(m *processor.DynamicsMetrics) float64 { return m.PeakLevel }),
		row("crest_factor_astats_db", fmtSpectral, func(m *processor.DynamicsMetrics) float64 { return m.CrestFactor }),
		row("dynamic_range_db", fmtSpectral, func(m *processor.DynamicsMetrics) float64 { return m.DynamicRange }),
		row("min_level_dbfs", fmtDB, func(m *processor.DynamicsMetrics) float64 { return m.MinLevel }),
		row("max_level_dbfs", fmtDB, func(m *processor.DynamicsMetrics) float64 { return m.MaxLevel }),
		row("rms_peak_dbfs", fmtDB, func(m *processor.DynamicsMetrics) float64 { return m.RMSPeak }),
		row("rms_trough_dbfs", fmtDB, func(m *processor.DynamicsMetrics) float64 { return m.RMSTrough }),
		row("flat_factor", fmtSpectral, func(m *processor.DynamicsMetrics) float64 { return m.FlatFactor }),
		row("dc_offset", fmtSpectral, func(m *processor.DynamicsMetrics) float64 { return m.DCOffset }),
		row("zero_crossings_rate", fmtSpectral, func(m *processor.DynamicsMetrics) float64 { return m.ZeroCrossingsRate }),
		row("bit_depth", fmtSpectral, func(m *processor.DynamicsMetrics) float64 { return m.BitDepth }),
		row("entropy", fmtSpectral, func(m *processor.DynamicsMetrics) float64 { return m.Entropy }),
	}

	var b strings.Builder
	b.WriteString("## Dynamics\n\n")
	b.WriteString(renderMetricTable(rows))
	return b.String()
}

// =============================================================================
// Spectral
// =============================================================================

// renderSpectral renders the aspectralstats table (the 13 spectral metrics). All
// three stages share the *SpectralMetrics Go type.
func renderSpectral(rec *processor.RunRecord) string {
	in := rec.Spectral.Stages.Input
	filt := rec.Spectral.Stages.Filtered
	final := rec.Spectral.Stages.Final

	row := func(key string, f func(*processor.SpectralMetrics) float64) metricRow {
		return metricRow{
			key: key, format: fmtSpectral,
			input: stageGetter(in, f), filt: stageGetter(filt, f), final: stageGetter(final, f),
		}
	}

	rows := []metricRow{
		row("mean", func(m *processor.SpectralMetrics) float64 { return m.Mean }),
		row("variance", func(m *processor.SpectralMetrics) float64 { return m.Variance }),
		row("centroid_hz", func(m *processor.SpectralMetrics) float64 { return m.Centroid }),
		row("spread_hz", func(m *processor.SpectralMetrics) float64 { return m.Spread }),
		row("skewness", func(m *processor.SpectralMetrics) float64 { return m.Skewness }),
		row("kurtosis", func(m *processor.SpectralMetrics) float64 { return m.Kurtosis }),
		row("entropy", func(m *processor.SpectralMetrics) float64 { return m.Entropy }),
		row("flatness", func(m *processor.SpectralMetrics) float64 { return m.Flatness }),
		row("crest", func(m *processor.SpectralMetrics) float64 { return m.Crest }),
		row("flux", func(m *processor.SpectralMetrics) float64 { return m.Flux }),
		row("slope", func(m *processor.SpectralMetrics) float64 { return m.Slope }),
		row("decrease", func(m *processor.SpectralMetrics) float64 { return m.Decrease }),
		row("rolloff_hz", func(m *processor.SpectralMetrics) float64 { return m.Rolloff }),
	}

	var b strings.Builder
	b.WriteString("## Spectral\n\n")
	b.WriteString(renderMetricTable(rows))
	return b.String()
}

// =============================================================================
// Noise Floor
// =============================================================================

// renderNoiseFloor renders the input-only noise domain block: the elected floor
// and its source, the two distinct floor estimates (prescan, astats), the
// adaptive room-tone detect level, the voice-activated flag, and the reduction
// headroom. Reads only rec.Noise. Raw measured values only - no "Noise Reduction"
// delta, no "Floor-Speech SNR", no "Character", no per-row verdict.
// Returns the empty string when the record carries no noise block (defensive;
// analysis and processing records both populate it).
func renderNoiseFloor(rec *processor.RunRecord) string {
	n := rec.Noise
	if n == nil {
		return ""
	}

	rows := [][]string{
		metricValueRow("floor_dbfs", n.Floor),
		{metricLabel("floor_source"), metricDefinition("floor_source"), stringCell(n.FloorSource)},
		metricValueRow("floor_prescan_dbfs", n.FloorPrescan),
		metricValueRow("floor_astats_dbfs", n.FloorAstats),
		metricValueRow("room_tone_detect_level_dbfs", n.RoomToneDetectLevel),
		{metricLabel("voice_activated"), metricDefinition("voice_activated"), boolCell(n.VoiceActivated)},
		// reduction_headroom_db (unit "dB") renders through formatMetric, not the
		// formatMetricDB its unit would select; keep it explicit.
		valueRow("reduction_headroom_db", formatMetric(n.ReductionHeadroom, 2)),
	}

	return renderValueTable("## Noise Floor\n\n", rows)
}

// =============================================================================
// Regions (room-tone + speech)
// =============================================================================

// renderRegions renders the room-tone and speech region blocks. For each kind it
// emits (a) the elected profile metrics, (b) for speech, a candidate summary
// (evaluated count + elected score ONLY - the full ranked array lives in the
// .candidates.jsonl sidecar, never inline; room tone carries no candidate
// summary), and (c) the per-stage Input/Filtered/Final region samples (absent
// stages omitted exactly like the loudness/dynamics tables).
//
// Record field paths (the densest record area):
//   - rec.Regions.RoomTone.Elected.Profile()  -> *processor.NoiseProfile
//   - rec.Regions.RoomTone.Samples.{Input,Filtered,Final} -> *processor.RegionSample
//   - rec.Regions.Speech.Elected.Profile()     -> *processor.SpeechCandidateMetrics
//   - rec.Regions.Speech.CandidatesSummary     -> *processor.CandidatesSummary
//   - rec.Regions.Speech.Samples.{Input,Filtered,Final}   -> *processor.RegionSample
//
// Room-tone Samples.Input may be nil (the elected NoiseProfile has no embedded
// RegionSample; the input sample is wired only when the elected candidate's
// RegionSample was captured at election). A nil input renders the placeholder for
// every cell, matching the absent-stage convention. Reads only rec.Regions.
// Returns the empty string when the record carries no regions block.
func renderRegions(rec *processor.RunRecord) string {
	if rec.Regions == nil {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Regions\n\n")

	b.WriteString("### Room Tone\n\n")
	b.WriteString(renderRoomToneElected(rec.Regions.RoomTone.ElectedProfile()))
	b.WriteString(renderRegionSamples(rec.Regions.RoomTone.Samples))

	b.WriteString("### Speech\n\n")
	b.WriteString(renderSpeechElected(rec.Regions.Speech.ElectedProfile()))
	b.WriteString(renderCandidatesSummary(rec.Regions.Speech.CandidatesSummary))
	b.WriteString(renderRegionSamples(rec.Regions.Speech.Samples))

	b.WriteString(renderGateStatistics(rec.Regions.GateStatistics))

	return b.String()
}

// renderGateStatistics renders the gate-window measurements derived from the one
// Pass 1 VAD split: the voiced-speech low percentile, the noise high percentile,
// and their separation. The two percentiles are on the VAD level axis (momentary
// LUFS); the separation is a dB difference. Returns the empty string when the
// record carries no gate statistics.
func renderGateStatistics(g *processor.GateStatistics) string {
	if g == nil {
		return ""
	}

	rows := [][]string{
		metricValueRow("voiced_low_percentile_dbfs", g.VoicedLowPercentile),
		metricValueRow("noise_high_percentile_dbfs", g.NoiseHighPercentile),
		// gate_separation_db (unit "dB") renders through formatMetric; keep it explicit.
		valueRow("gate_separation_db", formatMetric(g.SeparationDB, 2)),
	}

	return renderValueTable("### Gate Statistics\n\n", rows)
}

// renderRoomToneElected renders the elected room-tone NoiseProfile metrics as a
// Metric | Definition | Value table. Returns a short note when no profile was
// elected. Reads the wrapped *NoiseProfile via the record's Profile() read seam.
func renderRoomToneElected(p *processor.NoiseProfile) string {
	if p == nil {
		return "_No room-tone profile elected._\n\n"
	}

	rows := [][]string{
		// start_s / duration_s (unit "s") render through formatFloat; keep them explicit.
		valueRow("start_s", formatFloat(p.Start.Seconds(), 2)),
		valueRow("duration_s", formatFloat(p.Duration.Seconds(), 2)),
		metricValueRow("measured_floor_dbfs", p.MeasuredNoiseFloor),
		metricValueRow("peak_level_dbfs", p.PeakLevel),
		// crest_factor_db (unit "dB") renders through formatMetric; keep it explicit.
		valueRow("crest_factor_db", formatMetric(p.CrestFactor, 2)),
		metricValueRow("entropy", p.Entropy),
		metricValueRow("spectral_centroid_hz", p.Spectral.Centroid),
		metricValueRow("spectral_flatness", p.Spectral.Flatness),
		metricValueRow("spectral_kurtosis", p.Spectral.Kurtosis),
	}

	return renderValueTable("**Elected profile**\n\n", rows)
}

// renderSpeechElected renders the elected speech-candidate metrics (region length,
// amplitude/loudness, band RMS, voicing, score) as a Metric | Definition | Value
// table. Returns a short note when no speech profile was elected.
func renderSpeechElected(p *processor.SpeechCandidateMetrics) string {
	if p == nil {
		return "_No speech profile elected._\n\n"
	}

	rows := [][]string{
		// duration_s (unit "s") renders through formatFloat; keep it explicit.
		valueRow("duration_s", formatFloat(p.Region.Duration.Seconds(), 2)),
		metricValueRow("rms_level_dbfs", p.RMSLevel),
		metricValueRow("peak_level_dbfs", p.PeakLevel),
		// crest_factor_db (unit "dB") renders through formatMetric; keep it explicit.
		valueRow("crest_factor_db", formatMetric(p.CrestFactor, 2)),
		metricValueRow("momentary_lufs", p.MomentaryLUFS),
		metricValueRow("short_term_lufs", p.ShortTermLUFS),
		metricValueRow("true_peak_dbtp", p.TruePeak),
		metricValueRow("sample_peak_dbfs", p.SamplePeak),
		metricValueRow("speech_band_body_rms_dbfs", p.BodyBandRMS),
		metricValueRow("speech_band_sib_rms_dbfs", p.SibBandRMS),
		metricValueRow("voicing_density", p.VoicingDensity),
		metricValueRow("score", p.Score),
	}

	return renderValueTable("**Elected profile**\n\n", rows)
}

// renderCandidatesSummary renders the bare candidate summary: the evaluated count
// and the elected candidate's score. The full ranked candidate array is NOT inline
// (it streams to the .candidates.jsonl sidecar); this renderer emits count +
// elected only, with no per-candidate entropy/flatness/kurtosis gloss. Returns the
// empty string when no candidates were evaluated.
func renderCandidatesSummary(s *processor.CandidatesSummary) string {
	if s == nil {
		return ""
	}

	rows := [][]string{
		{"Evaluated count", "Number of region candidates evaluated.", formatInt(s.EvaluatedCount)},
	}
	if s.ElectedScore != nil {
		rows = append(rows, []string{metricLabel("score"), metricDefinition("score"), formatMetric(*s.ElectedScore, 4)})
	}

	var b strings.Builder
	b.WriteString("**Candidates**\n\n")
	b.WriteString(mdTable([]string{"Metric", "Definition", "Value"}, rows))
	b.WriteString("\n")
	return b.String()
}

// renderRegionSamples renders the per-stage Input/Filtered/Final region samples
// (amplitude, loudness, the 13 spectral metrics). All three stages share the
// *processor.RegionSample Go type, so the metricRow getters read the same fields
// off whichever stage is present - reusing stageColumns/renderMetricTable so an
// absent stage's column is omitted (analysis-only carries Input only) and a nil
// stage (e.g. room-tone Samples.Input) renders the placeholder.
func renderRegionSamples(s processor.RegionSamples) string {
	in, filt, final := s.Input, s.Filtered, s.Final

	row := func(key string, format metricFormat, f func(*processor.RegionSample) float64) metricRow {
		return metricRow{
			key: key, format: format,
			input: stageGetter(in, f), filt: stageGetter(filt, f), final: stageGetter(final, f),
		}
	}
	spec := func(key string, f func(*processor.SpectralMetrics) float64) metricRow {
		return row(key, fmtSpectral, func(rs *processor.RegionSample) float64 { return f(&rs.Spectral) })
	}

	rows := []metricRow{
		row("rms_level_dbfs", fmtDB, func(rs *processor.RegionSample) float64 { return rs.RMSLevel }),
		row("peak_level_dbfs", fmtDB, func(rs *processor.RegionSample) float64 { return rs.PeakLevel }),
		row("crest_factor_db", fmtSpectral, func(rs *processor.RegionSample) float64 { return rs.CrestFactor }),
		row("momentary_lufs", fmtLUFS, func(rs *processor.RegionSample) float64 { return rs.MomentaryLUFS }),
		row("short_term_lufs", fmtLUFS, func(rs *processor.RegionSample) float64 { return rs.ShortTermLUFS }),
		row("true_peak_dbtp", fmtPeakDB, func(rs *processor.RegionSample) float64 { return rs.TruePeak }),
		row("sample_peak_dbfs", fmtDB, func(rs *processor.RegionSample) float64 { return rs.SamplePeak }),
		spec("mean", func(m *processor.SpectralMetrics) float64 { return m.Mean }),
		spec("variance", func(m *processor.SpectralMetrics) float64 { return m.Variance }),
		spec("centroid_hz", func(m *processor.SpectralMetrics) float64 { return m.Centroid }),
		spec("spread_hz", func(m *processor.SpectralMetrics) float64 { return m.Spread }),
		spec("skewness", func(m *processor.SpectralMetrics) float64 { return m.Skewness }),
		spec("kurtosis", func(m *processor.SpectralMetrics) float64 { return m.Kurtosis }),
		spec("entropy", func(m *processor.SpectralMetrics) float64 { return m.Entropy }),
		spec("flatness", func(m *processor.SpectralMetrics) float64 { return m.Flatness }),
		spec("crest", func(m *processor.SpectralMetrics) float64 { return m.Crest }),
		spec("flux", func(m *processor.SpectralMetrics) float64 { return m.Flux }),
		spec("slope", func(m *processor.SpectralMetrics) float64 { return m.Slope }),
		spec("decrease", func(m *processor.SpectralMetrics) float64 { return m.Decrease }),
		spec("rolloff_hz", func(m *processor.SpectralMetrics) float64 { return m.Rolloff }),
	}

	var b strings.Builder
	b.WriteString("**Samples**\n\n")
	b.WriteString(renderMetricTable(rows))
	b.WriteString("\n")
	return b.String()
}

// =============================================================================
// Interval Summary
// =============================================================================

// renderIntervalSummary renders the per-250ms interval summary: the interval
// count, the RMS distribution percentiles, and the largest adjacent gap. The full
// per-interval series lives in the .intervals.jsonl sidecar; only this summary is
// inline. The RMS distribution and gap are present only when at least 10 intervals
// sit above digital silence (they are nil otherwise, so those rows drop). Reads
// only rec.IntervalSummary. Returns the empty string when no summary exists.
func renderIntervalSummary(rec *processor.RunRecord) string {
	s := rec.IntervalSummary
	if s == nil {
		return ""
	}

	rows := [][]string{
		{metricLabel("interval_count"), metricDefinition("interval_count"), formatInt(s.Count)},
	}
	if s.RMS != nil {
		rows = append(rows,
			metricValueRow("rms_dist_min_dbfs", s.RMS.Min),
			metricValueRow("rms_dist_p10_dbfs", s.RMS.P10),
			metricValueRow("rms_dist_p25_dbfs", s.RMS.P25),
			metricValueRow("rms_dist_p50_dbfs", s.RMS.P50),
			metricValueRow("rms_dist_p75_dbfs", s.RMS.P75),
			metricValueRow("rms_dist_p90_dbfs", s.RMS.P90),
			metricValueRow("rms_dist_max_dbfs", s.RMS.Max),
		)
	}
	if s.LargestGapDB != nil {
		// largest_gap_db (unit "dB") renders through formatMetric; keep it explicit.
		rows = append(rows, valueRow("largest_gap_db", formatMetric(*s.LargestGapDB, 2)))
	}

	return renderValueTable("## Interval Summary\n\n", rows)
}

// =============================================================================
// Region/summary cell helpers
// =============================================================================

// renderValueTable builds a single-stage Metric | Definition | Value table under
// the given heading, with a trailing blank line. It owns the header literal, the
// builder, and the newline the five single-stage renderers (noise floor, gate
// statistics, room-tone/speech elected, interval summary) shared verbatim.
func renderValueTable(heading string, rows [][]string) string {
	var b strings.Builder
	b.WriteString(heading)
	b.WriteString(mdTable([]string{"Metric", "Definition", "Value"}, rows))
	b.WriteString("\n")
	return b.String()
}

// valueRow builds a three-cell Metric | Definition | Value row for a single-stage
// table, looking up the label and gloss from Definitions by key.
func valueRow(key, value string) []string {
	return []string{metricLabel(key), metricDefinition(key), value}
}

// metricValueRow builds a single-stage value row, formatting the float through
// formatByRule keyed off the key's catalogued Unit so the formatter choice is not
// re-encoded per call site. It covers the unit classes whose single-stage cells
// route cleanly through formatByRule: dBFS/dBTP (formatMetricDB), LUFS
// (formatMetricLUFS), and Hz / unit-less (formatMetric). Decimals follow the unit
// (4 for unit-less spectral ratios, 2 otherwise), matching every existing cell.
// Rows on other units (plain "dB" through formatMetric, "s" through formatFloat)
// keep their explicit formatter at the call site; routing them here would change
// the rendered bytes.
func metricValueRow(key string, value float64) []string {
	format, decimals := unitMetricFormat(key)
	return valueRow(key, formatByRule(value, format, decimals))
}

// unitMetricFormat maps a metric key's catalogued Unit to the formatByRule rule
// and decimal count for its single-stage cell. It is defined only for the unit
// classes metricValueRow routes; an unhandled unit panics so a new single-stage
// row cannot silently mis-format (the caller picks an explicit formatter instead).
func unitMetricFormat(key string) (metricFormat, int) {
	d, _ := DefinitionFor(key)
	switch d.Unit {
	case "dBFS":
		return fmtDB, 2
	case "dBTP":
		return fmtPeakDB, 2
	case "LUFS":
		return fmtLUFS, 2
	case "Hz":
		return fmtSpectral, 2
	case "":
		return fmtSpectral, 4
	default:
		panic("report: metricValueRow: unrouted unit " + d.Unit + " for key " + key)
	}
}

// stringCell renders a categorical string value, the placeholder when empty.
func stringCell(s string) string {
	if s == "" {
		return placeholder
	}
	return s
}

// boolCell renders a boolean flag as "yes"/"no" (objective, not a verdict).
func boolCell(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// formatInt renders an integer count cell.
func formatInt(n int) string {
	return strconv.Itoa(n)
}
