package logging

import (
	"fmt"
	"math"
	"os"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// =============================================================================
// Three-Column Metric Table Helpers
// =============================================================================
// These helpers eliminate repetition in writeNoiseFloorTable and
// writeSpeechRegionTable, which both display Input/Filtered/Final columns
// for the same set of spectral and loudness metrics.

// threeColMetricSpec describes a single metric row to be rendered into a
// three-column comparison table. The caller pre-extracts the three float64
// values from whatever source types are in use.
type threeColMetricSpec struct {
	label       string               // display label († suffix added automatically when gain-normalised)
	vals        [3]float64           // input, filtered, final
	decimals    int                  // formatting precision
	unit        string               // unit suffix (e.g. "Hz", "LUFS")
	gainScaling int                  // 0=none, 1=linear, 2=squared (for normaliseForGain)
	interpret   func(float64) string // optional interpretation of final value; nil = no interpretation
}

// noiseFloorFormatter identifies which formatter to use for each value column
// in the noise-floor table. Spectral metrics use formatMetricSpectral
// (showing "n/a" for digital silence); loudness metrics use specialised
// formatters (formatMetricLUFS or formatMetricDB).
type noiseFloorFormatter int

const (
	nfFmtSpectral noiseFloorFormatter = iota // formatMetric for input, formatMetricSpectral for filtered/final
	nfFmtLUFS                                // formatMetricLUFS for all three columns
	nfFmtDB                                  // formatMetricDB for all three columns
)

// addNoiseFloorMetricRows appends metric rows to a noise-floor table.
// For spectral metrics (nfFmtSpectral), input uses formatMetric and
// filtered/final use formatMetricSpectral with digital silence handling.
// For loudness metrics, the appropriate specialised formatter is used.
func addNoiseFloorMetricRows(table *MetricTable, specs []threeColMetricSpec, fmtMode noiseFloorFormatter, gainNormalise bool, effectiveGainDB float64, filteredIsDigitalSilence, finalIsDigitalSilence bool) {
	for _, s := range specs {
		input, filtered, final := s.vals[0], s.vals[1], s.vals[2]

		// Apply gain normalisation to final value
		if s.gainScaling > 0 && gainNormalise && !finalIsDigitalSilence {
			final = normaliseForGain(final, effectiveGainDB, s.gainScaling)
		}

		// Add † suffix for gain-normalised metrics
		label := s.label
		if s.gainScaling > 0 && gainNormalise {
			label = s.label + " †"
		}

		// Format values according to the formatter mode
		var fmtInput, fmtFiltered, fmtFinal string
		switch fmtMode {
		case nfFmtSpectral:
			fmtInput = formatMetric(input, s.decimals)
			fmtFiltered = formatMetricSpectral(filtered, s.decimals, filteredIsDigitalSilence)
			fmtFinal = formatMetricSpectral(final, s.decimals, finalIsDigitalSilence)
		case nfFmtLUFS:
			fmtInput = formatMetricLUFS(input, s.decimals)
			fmtFiltered = formatMetricLUFS(filtered, s.decimals)
			fmtFinal = formatMetricLUFS(final, s.decimals)
		case nfFmtDB:
			fmtInput = formatMetricDB(input, s.decimals)
			fmtFiltered = formatMetricDB(filtered, s.decimals)
			fmtFinal = formatMetricDB(final, s.decimals)
		}

		table.AddRow(label, []string{fmtInput, fmtFiltered, fmtFinal}, s.unit, "")
	}
}

// addSpeechMetricRows appends metric rows to a speech-region table.
// All values use AddMetricRow (formatMetric internally) with optional
// interpretation of the final value.
func addSpeechMetricRows(table *MetricTable, specs []threeColMetricSpec, gainNormalise bool, effectiveGainDB float64) {
	for _, s := range specs {
		input, filtered, final := s.vals[0], s.vals[1], s.vals[2]

		// Apply gain normalisation to final value
		if s.gainScaling > 0 && gainNormalise {
			final = normaliseForGain(final, effectiveGainDB, s.gainScaling)
		}

		// Add † suffix for gain-normalised metrics
		label := s.label
		if s.gainScaling > 0 && gainNormalise {
			label = s.label + " †"
		}

		// Compute interpretation from the (possibly gain-normalised) final value
		var interp string
		if s.interpret != nil {
			interp = s.interpret(final)
		}

		table.AddMetricRow(label, input, filtered, final, s.decimals, s.unit, interp)
	}
}

// valOr returns the field value from a source, or math.NaN() if the source is nil.
// This is a convenience for building threeColMetricSpec slices concisely.
func valOr[T any](src *T, field func(*T) float64) float64 {
	if src == nil {
		return math.NaN()
	}
	return field(src)
}

// writeLoudnessTable outputs a three-column comparison table for loudness metrics.
// Columns: Input (Pass 1), Filtered (Pass 2), Final (Pass 4)
func writeLoudnessTable(f *os.File, input *processor.AudioMeasurements, filtered *processor.OutputMeasurements, final *processor.OutputMeasurements) {
	writeSection(f, "Loudness Measurements")

	table := NewMetricTable()

	v := func(inputField func(*processor.AudioMeasurements) float64, outputField func(*processor.OutputMeasurements) float64) [3]float64 {
		return [3]float64{
			valOr(input, inputField),
			valOr(filtered, outputField),
			valOr(final, outputField),
		}
	}

	specs := []threeColMetricSpec{
		{"Integrated Loudness", v(func(m *processor.AudioMeasurements) float64 { return m.InputI }, func(m *processor.OutputMeasurements) float64 { return m.OutputI }), 1, "LUFS", 0, nil},
		{"True Peak", v(func(m *processor.AudioMeasurements) float64 { return m.InputTP }, func(m *processor.OutputMeasurements) float64 { return m.OutputTP }), 1, "dBTP", 0, nil},
		{"Loudness Range", v(func(m *processor.AudioMeasurements) float64 { return m.InputLRA }, func(m *processor.OutputMeasurements) float64 { return m.OutputLRA }), 1, "LU", 0, nil},
		{"Sample Peak", v(func(m *processor.AudioMeasurements) float64 { return m.SamplePeak }, func(m *processor.OutputMeasurements) float64 { return m.SamplePeak }), 1, "dBFS", 0, nil},
		{"Momentary Loudness", v(func(m *processor.AudioMeasurements) float64 { return m.MomentaryLoudness }, func(m *processor.OutputMeasurements) float64 { return m.MomentaryLoudness }), 1, "LUFS", 0, nil},
		{"Short-term Loudness", v(func(m *processor.AudioMeasurements) float64 { return m.ShortTermLoudness }, func(m *processor.OutputMeasurements) float64 { return m.ShortTermLoudness }), 1, "LUFS", 0, nil},
	}
	for _, s := range specs {
		table.AddMetricRow(s.label, s.vals[0], s.vals[1], s.vals[2], s.decimals, s.unit, "")
	}

	fmt.Fprint(f, table.String())
	fmt.Fprintln(f, "")
}

// writeNoiseFloorTable outputs a three-column comparison table for noise floor metrics.
// Columns: Input (Pass 1 elected silence candidate), Filtered (Pass 2 SilenceSample), Final (Pass 4 SilenceSample)
func writeNoiseFloorTable(f *os.File, inputMeasurements *processor.AudioMeasurements, filteredMeasurements *processor.OutputMeasurements, finalMeasurements *processor.OutputMeasurements, normResult *processor.NormalisationResult) {
	writeSection(f, "Noise Floor Analysis")

	// Skip if no input measurements or noise profile
	if inputMeasurements == nil || inputMeasurements.NoiseProfile == nil {
		fmt.Fprintln(f, "No silence detected in input — noise profiling unavailable")
		fmt.Fprintln(f, "")
		return
	}

	// Compute effective normalisation gain for spectral metric compensation
	var effectiveGainDB float64
	if normResult != nil && !normResult.Skipped {
		effectiveGainDB = normResult.OutputLUFS - normResult.InputLUFS
	}
	gainNormalise := effectiveGainDB != 0

	// Find the elected silence candidate in SilenceCandidates by matching Region.Start to NoiseProfile.Start
	// NoiseProfile only has ~10 fields, but we need the full 20+ field SilenceCandidateMetrics
	var inputNoise *processor.SilenceCandidateMetrics
	noiseProfile := inputMeasurements.NoiseProfile

	// Handle refined regions - match against OriginalStart if refined, otherwise match against Start
	targetStart := noiseProfile.Start
	if noiseProfile.WasRefined {
		targetStart = noiseProfile.OriginalStart
	}

	for i := range inputMeasurements.SilenceCandidates {
		if inputMeasurements.SilenceCandidates[i].Region.Start == targetStart {
			inputNoise = &inputMeasurements.SilenceCandidates[i]
			break
		}
	}

	// Fall back to NoiseProfile fields if candidate not found (shouldn't happen, but be defensive)
	if inputNoise == nil {
		fmt.Fprintln(f, "Warning: Could not find matching silence candidate — using NoiseProfile data")
		fmt.Fprintln(f, "")
	}

	// Extract filtered and final silence samples
	var filteredNoise *processor.SilenceCandidateMetrics
	var finalNoise *processor.SilenceCandidateMetrics
	if filteredMeasurements != nil {
		filteredNoise = filteredMeasurements.SilenceSample
	}
	if finalMeasurements != nil {
		finalNoise = finalMeasurements.SilenceSample
	}

	table := NewMetricTable()

	// ========== AMPLITUDE METRICS ==========

	// RMS Level (noise floor)
	var inputRMS float64
	if inputNoise != nil {
		inputRMS = inputNoise.RMSLevel
	} else {
		inputRMS = noiseProfile.MeasuredNoiseFloor
	}
	filteredRMS := math.NaN()
	finalRMS := math.NaN()
	if filteredNoise != nil {
		filteredRMS = filteredNoise.RMSLevel
	}
	if finalNoise != nil {
		finalRMS = finalNoise.RMSLevel
	}

	// Check if filtered/final are digital silence (complete noise elimination)
	filteredIsDigitalSilence := isDigitalSilence(filteredRMS)
	finalIsDigitalSilence := isDigitalSilence(finalRMS)

	// Use special formatting for dB values that handles digital silence
	table.AddRow("RMS Level",
		[]string{
			formatMetricDB(inputRMS, 1),
			formatMetricDB(filteredRMS, 1),
			formatMetricDB(finalRMS, 1),
		},
		"dBFS", "")

	// Noise Reduction Delta (input - filtered/final, positive = reduction achieved)
	// For digital silence, show "> 60 dB" since we can't calculate exact reduction
	formatNoiseReduction := func(inputVal, outputVal float64, isDigSilence bool) string {
		if math.IsNaN(inputVal) || math.IsNaN(outputVal) {
			return MissingValue
		}
		if isDigSilence {
			// Can't calculate exact reduction when output is digital zero
			// Show as "> X dB" where X is the minimum reduction (input - threshold)
			minReduction := inputVal - DigitalSilenceThreshold
			if minReduction > 60 {
				return "> 60"
			}
			return fmt.Sprintf("> %.0f", minReduction)
		}
		delta := inputVal - outputVal
		if delta >= 0 {
			return fmt.Sprintf("+%.1f", delta)
		}
		return fmt.Sprintf("%.1f", delta)
	}

	var reductionInterp string
	if filteredIsDigitalSilence || finalIsDigitalSilence {
		reductionInterp = "noise eliminated"
	} else if !math.IsNaN(inputRMS) && !math.IsNaN(filteredRMS) {
		filteredDelta := inputRMS - filteredRMS
		switch {
		case filteredDelta < 0:
			reductionInterp = "noise increased"
		case filteredDelta < 3:
			reductionInterp = "minimal reduction"
		case filteredDelta < 10:
			reductionInterp = "good reduction"
		default:
			reductionInterp = "excellent reduction"
		}
	}

	table.AddRow("Noise Reduction",
		[]string{
			MissingValue,
			formatNoiseReduction(inputRMS, filteredRMS, filteredIsDigitalSilence),
			formatNoiseReduction(inputRMS, finalRMS, finalIsDigitalSilence),
		},
		"dB", reductionInterp)

	// Peak Level
	var inputPeak float64
	if inputNoise != nil {
		inputPeak = inputNoise.PeakLevel
	} else {
		inputPeak = noiseProfile.PeakLevel
	}
	filteredPeak := math.NaN()
	finalPeak := math.NaN()
	if filteredNoise != nil {
		filteredPeak = filteredNoise.PeakLevel
	}
	if finalNoise != nil {
		finalPeak = finalNoise.PeakLevel
	}
	table.AddRow("Peak Level",
		[]string{
			formatMetricDB(inputPeak, 1),
			formatMetricDB(filteredPeak, 1),
			formatMetricDB(finalPeak, 1),
		},
		"dBFS", "")

	// Crest Factor (undefined for digital silence - no peak or RMS to compare)
	var inputCrest float64
	if inputNoise != nil {
		inputCrest = inputNoise.CrestFactor
	} else {
		inputCrest = noiseProfile.CrestFactor
	}
	filteredCrest := math.NaN()
	finalCrest := math.NaN()
	if filteredNoise != nil && !filteredIsDigitalSilence {
		filteredCrest = filteredNoise.CrestFactor
	}
	if finalNoise != nil && !finalIsDigitalSilence {
		finalCrest = finalNoise.CrestFactor
	}
	table.AddMetricRow("Crest Factor", inputCrest, filteredCrest, finalCrest, 1, "dB", "")

	// ========== SPECTRAL METRICS ==========
	// For digital silence, spectral metrics are undefined (no signal to analyse).
	// Show "n/a" instead of misleading zeros or arbitrary values.

	// Entropy input has a special fallback to NoiseProfile when candidate not found
	inputEntropy := valOr(inputNoise, func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Entropy })
	if inputNoise == nil {
		inputEntropy = noiseProfile.Entropy
	}
	filteredEntropy := valOr(filteredNoise, func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Entropy })
	finalEntropy := valOr(finalNoise, func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Entropy })

	v := func(field func(*processor.SilenceCandidateMetrics) float64) [3]float64 {
		return [3]float64{
			valOr(inputNoise, field),
			valOr(filteredNoise, field),
			valOr(finalNoise, field),
		}
	}

	addNoiseFloorMetricRows(table, []threeColMetricSpec{
		{"Spectral Mean", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Mean }), 6, "", 1, nil},
		{"Spectral Variance", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Variance }), 6, "", 2, nil},
		{"Spectral Centroid", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Centroid }), 0, "Hz", 0, nil},
		{"Spectral Spread", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Spread }), 0, "Hz", 0, nil},
		{"Spectral Skewness", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Skewness }), 3, "", 0, nil},
		{"Spectral Kurtosis", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Kurtosis }), 3, "", 0, nil},
		{"Spectral Entropy", [3]float64{inputEntropy, filteredEntropy, finalEntropy}, 6, "", 0, nil},
		{"Spectral Flatness", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Flatness }), 6, "", 0, nil},
		{"Spectral Crest", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Crest }), 3, "", 0, nil},
		{"Spectral Flux", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Flux }), 6, "", 2, nil},
		{"Spectral Slope", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Slope }), 9, "", 1, nil},
		{"Spectral Decrease", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Decrease }), 6, "", 0, nil},
		{"Spectral Rolloff", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.Spectral.Rolloff }), 0, "Hz", 0, nil},
	}, nfFmtSpectral, gainNormalise, effectiveGainDB, filteredIsDigitalSilence, finalIsDigitalSilence)

	// ========== LOUDNESS METRICS ==========

	addNoiseFloorMetricRows(table, []threeColMetricSpec{
		{"Momentary LUFS", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.MomentaryLUFS }), 1, "LUFS", 0, nil},
		{"Short-term LUFS", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.ShortTermLUFS }), 1, "LUFS", 0, nil},
	}, nfFmtLUFS, gainNormalise, effectiveGainDB, filteredIsDigitalSilence, finalIsDigitalSilence)

	addNoiseFloorMetricRows(table, []threeColMetricSpec{
		{"True Peak", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.TruePeak }), 1, "dBTP", 0, nil},
		{"Sample Peak", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SamplePeak }), 1, "dBFS", 0, nil},
	}, nfFmtDB, gainNormalise, effectiveGainDB, filteredIsDigitalSilence, finalIsDigitalSilence)

	// Character (interpretation row) - based on entropy
	// For digital silence, show "silent" instead of attempting to characterise non-existent noise
	getNoiseCharacter := func(entropy float64, isDigSilence bool) string {
		if isDigSilence {
			return "silent"
		}
		if math.IsNaN(entropy) {
			return MissingValue
		}
		switch {
		case entropy < 0.3:
			return "very tonal"
		case entropy < 0.5:
			return "tonal"
		case entropy < 0.7:
			return "mixed"
		default:
			return "broadband"
		}
	}
	inputChar := getNoiseCharacter(inputEntropy, false) // Input is never digital silence (we have real noise)
	filteredChar := getNoiseCharacter(filteredEntropy, filteredIsDigitalSilence)
	finalChar := getNoiseCharacter(finalEntropy, finalIsDigitalSilence)
	table.AddRow("Character", []string{inputChar, filteredChar, finalChar}, "", "")

	fmt.Fprint(f, table.String())
	if gainNormalise {
		fmt.Fprintf(f, "† Final values gain-normalised (÷ %.1f dB) for cross-stage comparison\n", effectiveGainDB)
	}
	fmt.Fprintln(f, "")
}

// writeSpeechRegionTable outputs a three-column comparison table for speech region metrics.
// Columns: Input (Pass 1 speech profile), Filtered (Pass 2 SpeechSample), Final (Pass 4 SpeechSample)
func writeSpeechRegionTable(f *os.File, inputMeasurements *processor.AudioMeasurements, filteredMeasurements *processor.OutputMeasurements, finalMeasurements *processor.OutputMeasurements, normResult *processor.NormalisationResult) {
	writeSection(f, "Speech Region Analysis")

	// Skip if no input measurements or speech profile
	if inputMeasurements == nil || inputMeasurements.SpeechProfile == nil {
		fmt.Fprintln(f, "No speech profile available")
		fmt.Fprintln(f, "")
		return
	}

	// Compute effective normalisation gain for spectral metric compensation
	var effectiveGainDB float64
	if normResult != nil && !normResult.Skipped {
		effectiveGainDB = normResult.OutputLUFS - normResult.InputLUFS
	}
	gainNormalise := effectiveGainDB != 0

	// Extract speech samples
	inputSpeech := inputMeasurements.SpeechProfile
	var filteredSpeech *processor.SpeechCandidateMetrics
	var finalSpeech *processor.SpeechCandidateMetrics
	if filteredMeasurements != nil {
		filteredSpeech = filteredMeasurements.SpeechSample
	}
	if finalMeasurements != nil {
		finalSpeech = finalMeasurements.SpeechSample
	}

	table := NewMetricTable()

	// ========== AMPLITUDE METRICS ==========

	// RMS Level
	inputRMS := math.NaN()
	filteredRMS := math.NaN()
	finalRMS := math.NaN()
	if inputSpeech != nil {
		inputRMS = inputSpeech.RMSLevel
	}
	if filteredSpeech != nil {
		filteredRMS = filteredSpeech.RMSLevel
	}
	if finalSpeech != nil {
		finalRMS = finalSpeech.RMSLevel
	}
	table.AddMetricRow("RMS Level", inputRMS, filteredRMS, finalRMS, 1, "dBFS", "")

	// Peak Level
	inputPeak := math.NaN()
	filteredPeak := math.NaN()
	finalPeak := math.NaN()
	if inputSpeech != nil {
		inputPeak = inputSpeech.PeakLevel
	}
	if filteredSpeech != nil {
		filteredPeak = filteredSpeech.PeakLevel
	}
	if finalSpeech != nil {
		finalPeak = finalSpeech.PeakLevel
	}
	table.AddMetricRow("Peak Level", inputPeak, filteredPeak, finalPeak, 1, "dBFS", "")

	// Crest Factor
	inputCrest := math.NaN()
	filteredCrest := math.NaN()
	finalCrest := math.NaN()
	if inputSpeech != nil {
		inputCrest = inputSpeech.CrestFactor
	}
	if filteredSpeech != nil {
		filteredCrest = filteredSpeech.CrestFactor
	}
	if finalSpeech != nil {
		finalCrest = finalSpeech.CrestFactor
	}
	table.AddMetricRow("Crest Factor", inputCrest, filteredCrest, finalCrest, 1, "dB", "")

	// ========== SPECTRAL METRICS ==========

	// Extract centroid and entropy values needed by the Character row below
	inputCentroid := valOr(inputSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Centroid })
	filteredCentroid := valOr(filteredSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Centroid })
	finalCentroid := valOr(finalSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Centroid })
	inputEntropy := valOr(inputSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Entropy })
	filteredEntropy := valOr(filteredSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Entropy })
	finalEntropy := valOr(finalSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Entropy })

	sv := func(field func(*processor.SpeechCandidateMetrics) float64) [3]float64 {
		return [3]float64{
			valOr(inputSpeech, field),
			valOr(filteredSpeech, field),
			valOr(finalSpeech, field),
		}
	}

	addSpeechMetricRows(table, []threeColMetricSpec{
		{"Spectral Mean", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Mean }), 6, "", 1, nil},
		{"Spectral Variance", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Variance }), 6, "", 2, nil},
		{"Spectral Centroid", [3]float64{inputCentroid, filteredCentroid, finalCentroid}, 0, "Hz", 0, interpretCentroid},
		{"Spectral Spread", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Spread }), 0, "Hz", 0, interpretSpread},
		{"Spectral Skewness", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Skewness }), 3, "", 0, interpretSkewness},
		{"Spectral Kurtosis", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Kurtosis }), 3, "", 0, interpretKurtosis},
		{"Spectral Entropy", [3]float64{inputEntropy, filteredEntropy, finalEntropy}, 6, "", 0, interpretEntropy},
		{"Spectral Flatness", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Flatness }), 6, "", 0, interpretFlatness},
		{"Spectral Crest", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Crest }), 3, "", 0, interpretCrest},
		{"Spectral Flux", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Flux }), 6, "", 2, interpretFlux},
		{"Spectral Slope", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Slope }), 9, "", 1, interpretSlope},
		{"Spectral Decrease", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Decrease }), 6, "", 0, interpretDecrease},
		{"Spectral Rolloff", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.Spectral.Rolloff }), 0, "Hz", 0, interpretRolloff},
	}, gainNormalise, effectiveGainDB)

	// ========== LOUDNESS METRICS ==========

	addSpeechMetricRows(table, []threeColMetricSpec{
		{"Momentary LUFS", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.MomentaryLUFS }), 1, "LUFS", 0, nil},
		{"Short-term LUFS", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.ShortTermLUFS }), 1, "LUFS", 0, nil},
		{"True Peak", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.TruePeak }), 1, "dBTP", 0, nil},
		{"Sample Peak", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SamplePeak }), 1, "dBFS", 0, nil},
	}, gainNormalise, effectiveGainDB)

	// Character (interpretation row) - based on spectral centroid and entropy
	// Speech character describes voice quality: warm, balanced, bright, etc.
	getSpeechCharacter := func(centroid, entropy float64) string {
		if math.IsNaN(centroid) || math.IsNaN(entropy) {
			return MissingValue
		}
		// Combine centroid (brightness) with entropy (clarity) for character assessment
		// Low centroid + low entropy = warm, clear voice
		// High centroid + low entropy = bright, clear voice
		// High entropy = noisy/breathy regardless of centroid
		if entropy > 0.7 {
			return "noisy/breathy"
		}
		switch {
		case centroid < 1500:
			return "warm, full-bodied"
		case centroid < 2500:
			return "balanced, natural"
		case centroid < 4000:
			return "present, forward"
		case centroid < 6000:
			return "bright, crisp"
		case centroid > 6000:
			return "extremely bright (possible HF noise)"
		default:
			return "very bright"
		}
	}
	inputSpeechChar := getSpeechCharacter(inputCentroid, inputEntropy)
	filteredSpeechChar := getSpeechCharacter(filteredCentroid, filteredEntropy)
	finalSpeechChar := getSpeechCharacter(finalCentroid, finalEntropy)
	table.AddRow("Character", []string{inputSpeechChar, filteredSpeechChar, finalSpeechChar}, "", "")

	fmt.Fprint(f, table.String())
	if gainNormalise {
		fmt.Fprintf(f, "† Final values gain-normalised (÷ %.1f dB) for cross-stage comparison\n", effectiveGainDB)
	}
	fmt.Fprintln(f, "")
}
