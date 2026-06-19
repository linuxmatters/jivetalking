package report

import (
	"strconv"
	"strings"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

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

// This file holds the per-domain section renderers: Header, Processing Summary,
// Loudness, Dynamics, Spectral, and the rest. Each is a pure func(...) string
// reading ONLY the run record (and Timings for the summary) - no
// AudioMeasurements, no .json re-read, no internal/logging.
//
// The metric tables share one shape: column 0 Metric (label), column 1 the
// objective definition gloss + unit, then one value column per present stage
// (Input/Filtered/Final). The Filtered and Final columns are omitted when their
// stage pointer is nil (analysis-only / Pass-1-only records carry Input only).
//
// Input and output stages are DIFFERENT Go types (e.g. loudness input is
// *InputLoudnessMetrics, filtered/final are *OutputLoudnessMetrics) that share
// JSON field names. Each metric row pulls its value from whichever stage struct
// is present via a small per-stage closure, so the type split stays local to the
// row definition and the table builder sees only formatted strings.

// metricFormat selects which formatMetric* rule a row uses, matching the unit
// semantics the legacy .log formatters applied per metric class.
type metricFormat int

const (
	fmtDB       metricFormat = iota // dB / dBFS levels: "< -120" digital-silence floor
	fmtLUFS                         // LUFS loudness: "< -70" measurement floor
	fmtPeakDB                       // dBTP true peak: dB scale, digital-silence floor
	fmtSpectral                     // dimensionless / Hz spectral + astats values
	fmtSigned                       // explicit-sign values (target offset)
)

// metricRow defines one table row: the RunRecord field key (for its definition)
// the formatting rule, and per-stage value getters. A getter returns the metric
// value and whether the stage carries it; a nil getter (or a getter reporting
// false) leaves the cell as the placeholder. The value-vs-stage-type split lives
// in the getter closures the section renderers supply.
type metricRow struct {
	key    string
	format metricFormat
	input  func() (float64, bool)
	filt   func() (float64, bool)
	final  func() (float64, bool)
}

// stageColumns reports which of Filtered/Final any row populates, so a renderer
// can omit absent stage columns wholesale (analysis-only carries Input only).
func stageColumns(rows []metricRow) (hasFiltered, hasFinal bool) {
	for i := range rows {
		if rows[i].filt != nil {
			hasFiltered = true
		}
		if rows[i].final != nil {
			hasFinal = true
		}
	}
	return hasFiltered, hasFinal
}

// stageGetter builds a metricRow value getter for one stage struct: a nil stage
// yields a nil getter (formatCell renders the placeholder), otherwise the getter
// reads the metric off the stage and reports it present. It folds the nil-guarded
// closure factory the section renderers share across their stage types.
func stageGetter[T any](s *T, f func(*T) float64) func() (float64, bool) {
	if s == nil {
		return nil
	}
	return func() (float64, bool) { return f(s), true }
}

// formatCell formats one stage value through the row's metric rule, returning the
// placeholder when the stage is absent (getter nil or reporting false).
func formatCell(getter func() (float64, bool), format metricFormat) string {
	if getter == nil {
		return placeholder
	}
	value, ok := getter()
	if !ok {
		return placeholder
	}
	decimals := 2
	if format == fmtSpectral {
		decimals = 4
	}
	return formatByRule(value, format, decimals)
}

// formatByRule dispatches a value to the formatter named by the metric rule,
// passing the caller-chosen decimal count. It holds the single format->formatter
// mapping shared by formatCell and parseLoudnormCell; the callers own the decimal
// count (formatCell uses 4 for spectral, 2 otherwise; parseLoudnormCell uses 2).
func formatByRule(value float64, format metricFormat, decimals int) string {
	switch format {
	case fmtDB, fmtPeakDB:
		return formatMetricDB(value, decimals)
	case fmtLUFS:
		return formatMetricLUFS(value, decimals)
	case fmtSpectral:
		return formatMetric(value, decimals)
	case fmtSigned:
		return formatMetricSigned(value, decimals)
	default:
		return formatMetric(value, decimals)
	}
}

// renderMetricTable builds a metric table: Metric | Definition | Input
// [| Filtered] [| Final]. The Filtered/Final columns are omitted when no row
// populates them. Each row's second column carries the definition gloss and unit
// from Definitions, so every metric row is self-describing.
func renderMetricTable(rows []metricRow) string {
	hasFiltered, hasFinal := stageColumns(rows)

	headers := []string{"Metric", "Definition", "Input"}
	if hasFiltered {
		headers = append(headers, "Filtered")
	}
	if hasFinal {
		headers = append(headers, "Final")
	}

	body := make([][]string, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		cells := []string{metricLabel(row.key), metricDefinition(row.key), formatCell(row.input, row.format)}
		if hasFiltered {
			cells = append(cells, formatCell(row.filt, row.format))
		}
		if hasFinal {
			cells = append(cells, formatCell(row.final, row.format))
		}
		body = append(body, cells)
	}

	return mdTable(headers, body)
}

// metricLabel returns the human-readable label for a key, falling back to the raw
// key when no definition exists (a missing definition is caught by the
// required-key test, not here).
func metricLabel(key string) string {
	if d, ok := DefinitionFor(key); ok {
		return d.Label
	}
	return key
}

// metricDefinition returns the objective gloss with its unit appended in
// parentheses, e.g. "Gated programme loudness... (LUFS)". Unit-less metrics omit
// the parenthetical. Every loudness/dynamics/spectral row carries this gloss.
func metricDefinition(key string) string {
	d, ok := DefinitionFor(key)
	if !ok {
		return placeholder
	}
	if d.Unit == "" {
		return d.Gloss
	}
	return d.Gloss + " (" + d.Unit + ")"
}

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
		valueRow("floor_dbfs", formatMetricDB(n.Floor, 2)),
		{metricLabel("floor_source"), metricDefinition("floor_source"), stringCell(n.FloorSource)},
		valueRow("floor_prescan_dbfs", formatMetricDB(n.FloorPrescan, 2)),
		valueRow("floor_astats_dbfs", formatMetricDB(n.FloorAstats, 2)),
		valueRow("room_tone_detect_level_dbfs", formatMetricDB(n.RoomToneDetectLevel, 2)),
		{metricLabel("voice_activated"), metricDefinition("voice_activated"), boolCell(n.VoiceActivated)},
		valueRow("reduction_headroom_db", formatMetric(n.ReductionHeadroom, 2)),
	}

	var b strings.Builder
	b.WriteString("## Noise Floor\n\n")
	b.WriteString(mdTable([]string{"Metric", "Definition", "Value"}, rows))
	return b.String()
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
		valueRow("voiced_low_percentile_dbfs", formatMetricDB(g.VoicedLowPercentile, 2)),
		valueRow("noise_high_percentile_dbfs", formatMetricDB(g.NoiseHighPercentile, 2)),
		valueRow("gate_separation_db", formatMetric(g.SeparationDB, 2)),
	}

	var b strings.Builder
	b.WriteString("### Gate Statistics\n\n")
	b.WriteString(mdTable([]string{"Metric", "Definition", "Value"}, rows))
	b.WriteString("\n")
	return b.String()
}

// renderRoomToneElected renders the elected room-tone NoiseProfile metrics as a
// Metric | Definition | Value table. Returns a short note when no profile was
// elected. Reads the wrapped *NoiseProfile via the record's Profile() read seam.
func renderRoomToneElected(p *processor.NoiseProfile) string {
	if p == nil {
		return "_No room-tone profile elected._\n\n"
	}

	rows := [][]string{
		valueRow("start_s", formatFloat(p.Start.Seconds(), 2)),
		valueRow("duration_s", formatFloat(p.Duration.Seconds(), 2)),
		valueRow("measured_floor_dbfs", formatMetricDB(p.MeasuredNoiseFloor, 2)),
		valueRow("peak_level_dbfs", formatMetricDB(p.PeakLevel, 2)),
		valueRow("crest_factor_db", formatMetric(p.CrestFactor, 2)),
		valueRow("entropy", formatMetric(p.Entropy, 4)),
		valueRow("spectral_centroid_hz", formatMetric(p.SpectralCentroid, 2)),
		valueRow("spectral_flatness", formatMetric(p.SpectralFlatness, 4)),
		valueRow("spectral_kurtosis", formatMetric(p.SpectralKurtosis, 4)),
	}

	var b strings.Builder
	b.WriteString("**Elected profile**\n\n")
	b.WriteString(mdTable([]string{"Metric", "Definition", "Value"}, rows))
	b.WriteString("\n")
	return b.String()
}

// renderSpeechElected renders the elected speech-candidate metrics (region length,
// amplitude/loudness, band RMS, voicing, score) as a Metric | Definition | Value
// table. Returns a short note when no speech profile was elected.
func renderSpeechElected(p *processor.SpeechCandidateMetrics) string {
	if p == nil {
		return "_No speech profile elected._\n\n"
	}

	rows := [][]string{
		valueRow("duration_s", formatFloat(p.Region.Duration.Seconds(), 2)),
		valueRow("rms_level_dbfs", formatMetricDB(p.RMSLevel, 2)),
		valueRow("peak_level_dbfs", formatMetricDB(p.PeakLevel, 2)),
		valueRow("crest_factor_db", formatMetric(p.CrestFactor, 2)),
		valueRow("momentary_lufs", formatMetricLUFS(p.MomentaryLUFS, 2)),
		valueRow("short_term_lufs", formatMetricLUFS(p.ShortTermLUFS, 2)),
		valueRow("true_peak_dbtp", formatMetricDB(p.TruePeak, 2)),
		valueRow("sample_peak_dbfs", formatMetricDB(p.SamplePeak, 2)),
		valueRow("speech_band_body_rms_dbfs", formatMetricDB(p.BodyBandRMS, 2)),
		valueRow("speech_band_sib_rms_dbfs", formatMetricDB(p.SibBandRMS, 2)),
		valueRow("voicing_density", formatMetric(p.VoicingDensity, 4)),
		valueRow("score", formatMetric(p.Score, 4)),
	}

	var b strings.Builder
	b.WriteString("**Elected profile**\n\n")
	b.WriteString(mdTable([]string{"Metric", "Definition", "Value"}, rows))
	b.WriteString("\n")
	return b.String()
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
			valueRow("rms_dist_min_dbfs", formatMetricDB(s.RMS.Min, 2)),
			valueRow("rms_dist_p10_dbfs", formatMetricDB(s.RMS.P10, 2)),
			valueRow("rms_dist_p25_dbfs", formatMetricDB(s.RMS.P25, 2)),
			valueRow("rms_dist_p50_dbfs", formatMetricDB(s.RMS.P50, 2)),
			valueRow("rms_dist_p75_dbfs", formatMetricDB(s.RMS.P75, 2)),
			valueRow("rms_dist_p90_dbfs", formatMetricDB(s.RMS.P90, 2)),
			valueRow("rms_dist_max_dbfs", formatMetricDB(s.RMS.Max, 2)),
		)
	}
	if s.LargestGapDB != nil {
		rows = append(rows, valueRow("largest_gap_db", formatMetric(*s.LargestGapDB, 2)))
	}

	var b strings.Builder
	b.WriteString("## Interval Summary\n\n")
	b.WriteString(mdTable([]string{"Metric", "Definition", "Value"}, rows))
	return b.String()
}

// =============================================================================
// Region/summary cell helpers
// =============================================================================

// valueRow builds a three-cell Metric | Definition | Value row for a single-stage
// table, looking up the label and gloss from Definitions by key.
func valueRow(key, value string) []string {
	return []string{metricLabel(key), metricDefinition(key), value}
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
// is output_integrated_lufs (the loudnorm-measured output, normalisation.
// loudnorm_measured.output_integrated_lufs, parsed from LoudnormStats.OutputI)
// minus normalisation.effective_target_lufs (EffectiveTargetI), formatted via
// formatMetricSigned. No glyph, no boolean.
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
	if stats := r.LoudnormStats; stats != nil {
		rows = append(rows,
			paramRow{"Measured input integrated (LUFS)", parseLoudnormCell(stats.InputI, fmtLUFS)},
			paramRow{"Measured input true peak (dBTP)", parseLoudnormCell(stats.InputTP, fmtPeakDB)},
			paramRow{"Measured input LRA (LU)", parseLoudnormCell(stats.InputLRA, fmtSpectral)},
			paramRow{"Measured input threshold (LUFS)", parseLoudnormCell(stats.InputThresh, fmtLUFS)},
			paramRow{"Measured output integrated (LUFS)", parseLoudnormCell(stats.OutputI, fmtLUFS)},
			paramRow{"Measured output true peak (dBTP)", parseLoudnormCell(stats.OutputTP, fmtPeakDB)},
			paramRow{"Measured output LRA (LU)", parseLoudnormCell(stats.OutputLRA, fmtSpectral)},
			paramRow{"Measured output threshold (LUFS)", parseLoudnormCell(stats.OutputThresh, fmtLUFS)},
			paramRow{"Normalisation type", stringCell(stats.NormalizationType)},
		)
	}
	// Raw signed LU deviation from the effective target: the loudnorm-measured
	// output integrated loudness minus the effective target. A signed number,
	// never a boolean or glyph.
	rows = append(rows, paramRow{"Deviation from target (LU)", targetDeviationCell(r)})
	b.WriteString(renderParamTable(rows))

	return b.String()
}

// targetDeviationCell computes the raw signed LU deviation of the loudnorm-measured
// output integrated loudness from the effective target:
// output_integrated_lufs (parsed from LoudnormStats.OutputI) minus
// effective_target_lufs. Returns the placeholder when the measured output value is
// missing or unparseable, so the cell never fabricates a deviation.
func targetDeviationCell(r *processor.NormalisationResult) string {
	if r.LoudnormStats == nil {
		return placeholder
	}
	out, err := strconv.ParseFloat(strings.TrimSpace(r.LoudnormStats.OutputI), 64)
	if err != nil {
		return placeholder
	}
	return formatMetricSigned(out-r.EffectiveTargetI, 2)
}

// parseLoudnormCell parses a loudnorm stats string value and formats it through
// the given metric rule, returning the placeholder on a parse failure (graceful:
// the cell never fabricates a value).
func parseLoudnormCell(value string, format metricFormat) string {
	f, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return placeholder
	}
	return formatByRule(f, format, 2)
}

// =============================================================================
// Spectrograms
// =============================================================================

// spectrogramKindOrder is the stable kind order for the Spectrograms table
// (whole-file first, then the two elected regions). Each kind carries the row
// label shown in column 0.
var spectrogramKindOrder = []struct {
	kind  string
	label string
}{
	{processor.SpectrogramKindWhole, "Whole file"},
	{processor.SpectrogramKindRoomTone, "Room tone"},
	{processor.SpectrogramKindSpeech, "Speech"},
}

// spectrogramStageOrder is the stable stage (column) order. Processing records
// carry before+after; analysis-only records carry input only. A column is
// emitted only when at least one image populates it, mirroring the absent-stage
// convention the metric tables use (renderLoudness etc.).
var spectrogramStageOrder = []struct {
	stage  string
	header string
}{
	{processor.SpectrogramStageBefore, "Before"},
	{processor.SpectrogramStageAfter, "After"},
	{processor.SpectrogramStageInput, "Input"},
}

// renderSpectrograms renders the Spectrograms section from rec.Spectrograms ONLY:
// a Markdown table grouped by kind (whole -> roomtone -> speech) with one column
// per present stage. Processing records yield a Before | After pair per kind;
// analysis-only records yield a single Input column. Cells are Markdown image
// links to the record's relative basenames (![<kind> <stage>](<path>)). This
// renderer is a PURE record consumer - it reads the slice and builds Markdown,
// never calling ffmpeg/exec. An empty slice returns "" so the orchestrator emits
// no heading, matching the empty-section discipline the other renderers follow.
func renderSpectrograms(rec *processor.RunRecord) string {
	if len(rec.Spectrograms) == 0 {
		return ""
	}

	// Index the images by kind+stage for stable, order-independent lookup.
	type key struct{ kind, stage string }
	byKey := make(map[key]processor.SpectrogramImage, len(rec.Spectrograms))
	present := make(map[string]bool)
	for _, img := range rec.Spectrograms {
		byKey[key{img.Kind, img.Stage}] = img
		present[img.Stage] = true
	}

	// Columns: keep only stages that at least one image populates.
	stages := make([]struct{ stage, header string }, 0, len(spectrogramStageOrder))
	for _, s := range spectrogramStageOrder {
		if present[s.stage] {
			stages = append(stages, s)
		}
	}

	headers := make([]string, 0, len(stages)+1)
	headers = append(headers, "Region")
	for _, s := range stages {
		headers = append(headers, s.header)
	}

	body := make([][]string, 0, len(spectrogramKindOrder))
	for _, k := range spectrogramKindOrder {
		row := []string{k.label}
		any := false
		for _, s := range stages {
			img, ok := byKey[key{k.kind, s.stage}]
			if !ok {
				row = append(row, placeholder)
				continue
			}
			any = true
			row = append(row, spectrogramCell(k.kind, s.stage, img.Path))
		}
		if any {
			body = append(body, row)
		}
	}

	var b strings.Builder
	b.WriteString("## Spectrograms\n\n")
	b.WriteString(mdTable(headers, body))
	return b.String()
}

// spectrogramCell renders one Markdown image link to a relative basename:
// ![<kind> <stage>](<path>).
func spectrogramCell(kind, stage, path string) string {
	return "![" + kind + " " + stage + "](" + path + ")"
}
