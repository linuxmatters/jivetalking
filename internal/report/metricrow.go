package report

// This file holds the metric-table engine shared by the metric-domain section
// renderers (loudness, dynamics, spectral, region samples): the per-stage row
// shape, the stage getter factory, the format-rule dispatch, and the table
// builder. The leaf section renderers in sections.go supply rows; this engine
// turns them into Markdown.
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
// mapping shared by formatCell and loudnormValueCell; the callers own the decimal
// count (formatCell uses 4 for spectral, 2 otherwise; loudnormValueCell uses 2).
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
