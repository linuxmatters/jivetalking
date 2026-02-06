// Package logging handles generation of analysis reports for processed audio files

package logging

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// ============================================================================
// Spectral Characteristic Interpretation Functions
// ============================================================================
// These functions interpret spectral measurements and return human-readable
// descriptions of audio characteristics. Based on MATLAB Audio Toolbox
// documentation and standard audio analysis conventions.

// interpretCentroid describes spectral "brightness" based on centre of gravity.
// Reference: Grey & Gordon (1978) JASA; Peeters (2003) CUIDADO; librosa.
//
// Centroid is the "centre of gravity" of the spectrum - where spectral energy is concentrated.
//
// Reference values for speech:
// - Male voiced speech: 500-2500 Hz
// - Female voiced speech: 800-3500 Hz
// - Unvoiced consonants: 3000-8000+ Hz
//
// Higher centroid indicates brighter voice; useful for de-esser tuning.
func interpretCentroid(hz float64) string {
	switch {
	case hz < 500:
		return "dark, muffled"
	case hz < 1500:
		return "warm, present"
	case hz < 2500:
		return "forward, clear"
	case hz < 4000:
		return "bright, articulate"
	case hz < 6000:
		return "very bright, sibilant"
	default:
		return "extremely bright, fricatives or HF noise"
	}
}

// interpretSpread describes the spectral bandwidth around the centroid.
// Spread is the standard deviation of the spectrum around the centroid.
// It represents "instantaneous bandwidth" and indicates tonal dominance.
//
// Pure vowels show narrow spread; consonants and noise show wide spread.
// Low spread indicates tone dominance; high spread indicates broadband content.
func interpretSpread(hz float64) string {
	switch {
	case hz < 800:
		return "narrow, clean voiced"
	case hz < 2000:
		return "moderate, natural speech"
	case hz < 3500:
		return "wide, mixed voiced/unvoiced"
	default:
		return "very wide, broadband"
	}
}

// interpretSkewness describes the spectral distribution asymmetry.
// Positive skewness: energy concentrated below centroid (bass-heavy with HF tail).
// Negative skewness: energy concentrated above centroid (HF concentrated, sibilant-like).
//
// Voiced speech typically shows positive skewness (0.5-2.0); fricatives may show negative.
func interpretSkewness(skew float64) string {
	switch {
	case skew < -0.5:
		return "HF emphasis, fricatives/sibilants"
	case skew < 0.5:
		return "symmetric distribution"
	case skew < 2.5:
		return "LF emphasis with HF tail (typical voice)"
	default:
		return "very strong LF bias"
	}
}

// interpretKurtosis describes the spectral peakiness.
// Kurtosis measures how peaked vs flat the spectrum is; indicates harmonic clarity vs noise.
// Higher values: peaked/tonal spectrum with dominant frequencies.
// Lower values: flatter spectrum, more noise-like.
// Reference: Gaussian distribution has kurtosis=3.
//
// Healthy voiced speech typically 5-10; pathological or noisy voice trends toward 3.
func interpretKurtosis(kurt float64) string {
	switch {
	case kurt < 2.0:
		return "platykurtic, noise-dominant"
	case kurt < 3.0:
		return "slightly platykurtic, noisy/fricative"
	case kurt < 3.5:
		return "mesokurtic (Gaussian reference)"
	case kurt < 5.0:
		return "moderately leptokurtic, mixed"
	case kurt < 10.0:
		return "leptokurtic, clear harmonics"
	default:
		return "highly leptokurtic, excellent harmonics"
	}
}

// interpretEntropy describes spectral randomness/order.
// FFmpeg aspectralstats outputs normalised entropy (divided by log(size)).
// Values range 0-1 where 0=pure tone, 1=white noise.
// Reference: Misra et al. (2004) ICASSP; Essentia Entropy algorithm.
func interpretEntropy(entropy float64) string {
	switch {
	case entropy < 0.15:
		return "highly ordered, pure tone/clean vowel"
	case entropy < 0.30:
		return "clean voiced speech"
	case entropy < 0.50:
		return "mixed voiced/unvoiced"
	case entropy < 0.70:
		return "disordered, fricatives"
	case entropy < 0.85:
		return "unvoiced consonants"
	default:
		return "noise-dominant"
	}
}

// interpretFlatness describes tonality vs noisiness (Wiener entropy).
// Ratio of geometric mean to arithmetic mean. 0=pure tone, 1=white noise.
// Reference: MPEG-7 AudioSpectralFlatness; Johnston (1988); Dubnov (2004).
//
// Clean voiced speech 0.1-0.3; breathy voice 0.3-0.5; fricatives 0.4-0.7.
func interpretFlatness(flatness float64) string {
	switch {
	case flatness < 0.1:
		return "pure tone, maximum tonality"
	case flatness < 0.25:
		return "very tonal, strong harmonics"
	case flatness < 0.4:
		return "tonal with some noise, clean voiced"
	case flatness < 0.6:
		return "mixed tonal and noise"
	default:
		return "noise-like, breathy/unvoiced"
	}
}

// interpretCrest describes the peak-to-average ratio of the spectrum.
// Crest factor = max(spectrum) / mean(spectrum), expressed as LINEAR RATIO (not dB).
// To convert to dB: crest_dB = 20 * log10(crest).
// Reference: Peeters (2003) CUIDADO project; Essentia Crest algorithm.
//
// Typical values:
//   - White noise: ~3-5 (peaks barely exceed mean)
//   - Moderate peaks: 5-15 (some structure)
//   - Speech range: 20-60 (clear harmonic structure)
//   - Dominant peaks: >60 (excellent harmonic clarity)
func interpretCrest(crest float64) string {
	switch {
	case crest < 5:
		return "flat spectrum, noise-like"
	case crest < 15:
		return "moderate peaks"
	case crest < 30:
		return "strong peaks"
	case crest < 60:
		return "very strong peaks (speech range)"
	default:
		return "dominant peaks, excellent harmonic clarity"
	}
}

// interpretFlux describes frame-to-frame spectral variation.
// Flux measures how much the spectrum changes between frames.
// Low flux: stable/sustained sound (held notes, steady speech).
// High flux: dynamic/changing sound (transients, varied speech).
//
// Vowels show low flux; plosives (p,t,k,b,d,g) show high flux spikes.
func interpretFlux(flux float64) string {
	switch {
	case flux < 0.001:
		return "very stable, sustained"
	case flux < 0.005:
		return "stable, continuous"
	case flux < 0.02:
		return "natural articulation"
	default:
		return "high variation, transients"
	}
}

// interpretDecrease describes low-frequency weighted spectral slope.
// FFmpeg computes: sum((mag[k] - mag[0]) / k) / sum(mag[k])
// Positive values indicate spectrum decreases from low to high frequencies (typical for speech).
// Negative values indicate rising spectrum (unusual, HF emphasis).
// Reference: Peeters (2003) CUIDADO project; Essentia Decrease algorithm.
func interpretDecrease(decrease float64) string {
	switch {
	case decrease < 0:
		return "strong bass emphasis"
	case decrease < 0.05:
		return "balanced, typical voice"
	case decrease < 0.10:
		return "moderate decrease, typical speech"
	default:
		return "strong HF content, bright"
	}
}

// interpretRolloff describes effective bandwidth via 85% energy threshold.
// Returns Hz below which 85% of spectral energy resides.
// Reference: Peeters (2003) CUIDADO; librosa spectral_rolloff.
func interpretRolloff(hz float64) string {
	switch {
	case hz < 2000:
		return "over-filtered"
	case hz < 4000:
		return "dark, LF-dominant"
	case hz < 6000:
		return "typical voiced speech"
	case hz < 8000:
		return "good articulation"
	case hz < 12000:
		return "bright, airy"
	default:
		return "very bright, check sibilance"
	}
}

// interpretSlope describes the overall spectral tilt (linear regression coefficient).
// Modal speech approximately -6 dB/octave; breathy voice steeper; pressed voice shallower.
func interpretSlope(slope float64) string {
	switch {
	case slope < -5e-04:
		return "very steep slope, dark/warm"
	case slope < -2e-04:
		return "steep slope, warm character"
	case slope < -5e-05:
		return "moderate slope, balanced"
	case slope < 0:
		return "shallow slope, bright/energetic"
	default:
		return "positive slope, very bright"
	}
}

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

// =============================================================================
// Report Section Formatting Helpers
// =============================================================================

// writeSection writes a section header with title and dashed underline.
// The underline length matches the title length.
func writeSection(f *os.File, title string) {
	fmt.Fprintln(f, title)
	fmt.Fprintln(f, strings.Repeat("-", len(title)))
}

// ReportData contains all the information needed to generate an analysis report
type ReportData struct {
	InputPath    string
	OutputPath   string
	StartTime    time.Time
	EndTime      time.Time
	Pass1Time    time.Duration
	Pass2Time    time.Duration
	Pass3Time    time.Duration // Loudnorm measurement pass (may be 0 if skipped)
	Pass4Time    time.Duration // Loudnorm application pass (may be 0 if skipped)
	Result       *processor.ProcessingResult
	SampleRate   int
	Channels     int
	DurationSecs float64 // Duration in seconds
}

// GenerateReport creates a detailed analysis report and saves it alongside the output file.
// The report filename will be <output>-processed.log
//
// Report structure (Phase 3 restructure):
// 1. Header - file info and timestamp
// 2. Processing Summary - pass timings
// 3. Filter Chain Applied - adaptive parameters
// 4. Loudness Measurements - three-column table (Input/Filtered/Final)
// 5. Noise Floor Analysis - three-column table
// 6. Speech Region Analysis - three-column table with interpretations
// 7. Diagnostic sections - detailed debug info
func GenerateReport(data ReportData) error {
	// Generate report filename: presenter1-processed.flac → presenter1-processed.log
	logPath := strings.TrimSuffix(data.OutputPath, filepath.Ext(data.OutputPath)) + ".log"

	// Create report file
	f, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer f.Close()

	// === Main Report Sections (consolidated tables) ===

	// Header
	writeReportHeader(f, data)

	// Processing Summary (timings)
	writeProcessingSummary(f, data)

	// Prepare measurements for use in multiple sections
	var inputMeasurements *processor.AudioMeasurements
	var filteredMeasurements *processor.OutputMeasurements
	var finalMeasurements *processor.OutputMeasurements
	if data.Result != nil {
		inputMeasurements = data.Result.Measurements
		filteredMeasurements = data.Result.FilteredMeasurements
		finalMeasurements = getFinalMeasurements(data.Result)
	}

	// Silence Detection (analysis that informs filter chain decisions)
	writeDiagnosticSilence(f, inputMeasurements)

	// Speech Detection (for adaptive tuning)
	writeDiagnosticSpeech(f, inputMeasurements)

	// Filter Chain Applied
	if data.Result != nil && data.Result.Config != nil {
		writeFilterChainApplied(f, data.Result.Config, data.Result.Measurements)
	}

	// Peak Limiter (Pass 4 pre-limiting before loudnorm)
	if data.Result != nil && data.Result.NormResult != nil {
		writeDiagnosticPeakLimiter(f, data.Result.NormResult, data.Result.Config)
	}

	// Loudnorm (follows filter chain as it's the final processing stage)
	if data.Result != nil && data.Result.Config != nil {
		writeDiagnosticLoudnorm(f, data.Result.NormResult, data.Result.Config)
	}

	// Loudness Measurements Table (Input → Filtered → Final)
	writeLoudnessTable(f, inputMeasurements, filteredMeasurements, finalMeasurements)

	// Extract normalisation result for gain-dependent metric compensation
	var normResult *processor.NormalisationResult
	if data.Result != nil {
		normResult = data.Result.NormResult
	}

	// Noise Floor Analysis Table
	writeNoiseFloorTable(f, inputMeasurements, filteredMeasurements, getFinalMeasurements(data.Result), normResult)

	// Speech Region Analysis Table
	writeSpeechRegionTable(f, inputMeasurements, filteredMeasurements, getFinalMeasurements(data.Result), normResult)

	return nil
}

// loudnormModeString converts linear bool to readable mode string
func loudnormModeString(linear bool) string {
	if linear {
		return "Linear (target adjusted to prevent dynamic fallback)"
	}
	return "Dynamic"
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}

	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60

	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}

	hours := minutes / 60
	minutes = minutes % 60
	return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
}

// channelName returns a human-readable channel name
func channelName(channels int) string {
	switch channels {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	default:
		return fmt.Sprintf("%d channels", channels)
	}
}

// formatFilterChain generates the filter chain section of the report.
// Iterates over filters in chain order, showing enabled/disabled status,
// key parameters, and adaptive rationale for each filter.
func formatFilterChain(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements) {
	fmt.Fprintln(f, "Filter Chain (in processing order)")
	fmt.Fprintln(f, "------------------------------------")

	for i, filterID := range cfg.FilterOrder {
		prefix := fmt.Sprintf("%2d. ", i+1)
		formatFilter(f, filterID, cfg, m, prefix)
	}
}

// formatFilter outputs details for a single filter
func formatFilter(f *os.File, filterID processor.FilterID, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	switch filterID {
	case processor.FilterDownmix:
		formatDownmixFilter(f, cfg, prefix)
	case processor.FilterAnalysis:
		formatAnalysisFilter(f, cfg, prefix)
	case processor.FilterResample:
		formatResampleFilter(f, cfg, prefix)
	case processor.FilterDS201HighPass:
		formatDS201HighpassFilter(f, cfg, m, prefix)
	case processor.FilterDS201LowPass:
		formatDS201LowPassFilter(f, cfg, m, prefix)
	case processor.FilterNoiseRemove:
		formatNoiseRemoveFilter(f, cfg, m, prefix)
	case processor.FilterDS201Gate:
		formatDS201GateFilter(f, cfg, m, prefix)
	case processor.FilterLA2ACompressor:
		formatLA2ACompressorFilter(f, cfg, m, prefix)
	case processor.FilterDeesser:
		formatDeesserFilter(f, cfg, m, prefix)
	case processor.FilterVolumax:
		formatVolumaxFilter(f, cfg, m, prefix)
	default:
		fmt.Fprintf(f, "%s%s: (unknown filter)\n", prefix, filterID)
	}
}

// formatDS201HighpassFilter outputs DS201-inspired highpass filter details
func formatDS201HighpassFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DS201HPEnabled {
		fmt.Fprintf(f, "%sDS201 highpass: DISABLED\n", prefix)
		return
	}

	// Show slope (6dB/oct for gentle, 12dB/oct for standard)
	slope := "12dB/oct"
	if cfg.DS201HPPoles == 1 {
		slope = "6dB/oct"
	}

	// Build header with all relevant parameters
	header := fmt.Sprintf("%sDS201 highpass: %.0f Hz cutoff (%s", prefix, cfg.DS201HPFreq, slope)

	// Show Q if not default Butterworth
	if cfg.DS201HPWidth > 0 && cfg.DS201HPWidth != 0.707 {
		header += fmt.Sprintf(", Q=%.2f", cfg.DS201HPWidth)
	}

	// Show transform if specified
	if cfg.DS201HPTransform == "tdii" {
		header += ", tdii"
	} else if cfg.DS201HPTransform != "" {
		header += ", " + cfg.DS201HPTransform
	}

	header += ")"
	fmt.Fprintln(f, header)

	// Show adaptive rationale
	if m != nil && m.SpectralCentroid > 0 {
		voiceType := "normal"
		if m.SpectralCentroid > 6000 {
			voiceType = "bright"
		} else if m.SpectralCentroid < 4000 {
			voiceType = "dark/warm"
		}
		fmt.Fprintf(f, "        Rationale: %s voice (centroid %.0f Hz)\n", voiceType, m.SpectralCentroid)

		// Show warm voice protection if applicable (using mix)
		if cfg.DS201HPMix > 0 && cfg.DS201HPMix < 1.0 {
			reason := "warm voice"
			if m.SpectralDecrease < -0.08 {
				reason = "very warm voice"
			} else if m.SpectralSkewness > 1.0 {
				reason = "LF emphasis"
			}
			fmt.Fprintf(f, "        Mix: %.0f%% (%s — blending filtered with dry signal)\n", cfg.DS201HPMix*100, reason)
		}

		// Show why low frequency was chosen for warm voices
		if cfg.DS201HPFreq <= 40 {
			fmt.Fprintf(f, "        Frequency: %.0f Hz (subsonic only — protecting bass foundation)\n", cfg.DS201HPFreq)
		}

		// Show gentle slope explanation
		if cfg.DS201HPPoles == 1 {
			fmt.Fprintf(f, "        Slope: 6dB/oct (gentle rolloff — preserving warmth)\n")
		}
	}
}

// formatDS201LowPassFilter outputs DS201-inspired low-pass filter details
func formatDS201LowPassFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DS201LPEnabled {
		// Show reason for being disabled (pass-through mode)
		if cfg.DS201LPReason != "" {
			fmt.Fprintf(f, "%sDS201 lowpass: DISABLED (%s)\n", prefix, cfg.DS201LPReason)
		} else {
			fmt.Fprintf(f, "%sDS201 lowpass: DISABLED\n", prefix)
		}
		return
	}

	// Show slope (6dB/oct for gentle, 12dB/oct for standard)
	slope := "12dB/oct"
	if cfg.DS201LPPoles == 1 {
		slope = "6dB/oct"
	}

	// Build header with all relevant parameters
	header := fmt.Sprintf("%sDS201 lowpass: %.0f Hz cutoff (%s", prefix, cfg.DS201LPFreq, slope)

	// Show Q if not default Butterworth
	if cfg.DS201LPWidth > 0 && cfg.DS201LPWidth != 0.707 {
		header += fmt.Sprintf(", Q=%.2f", cfg.DS201LPWidth)
	}

	// Show transform if specified
	if cfg.DS201LPTransform == "tdii" {
		header += ", tdii"
	} else if cfg.DS201LPTransform != "" {
		header += ", " + cfg.DS201LPTransform
	}

	// Show mix if not full wet
	if cfg.DS201LPMix > 0 && cfg.DS201LPMix < 1.0 {
		header += fmt.Sprintf(", mix %.0f%%", cfg.DS201LPMix*100)
	}

	header += ")"
	fmt.Fprintln(f, header)

	// Show rationale
	if cfg.DS201LPReason != "" {
		fmt.Fprintf(f, "        Rationale: %s\n", cfg.DS201LPReason)
	}

	// Show content type detection metrics
	if m != nil {
		fmt.Fprintf(f, "        Content type: %s (kurtosis %.1f, flatness %.3f, flux %.4f)\n",
			cfg.DS201LPContentType.String(), m.SpectralKurtosis, m.SpectralFlatness, m.SpectralFlux)

		// Show the triggering metric details
		switch cfg.DS201LPReason {
		case "rolloff/centroid gap":
			fmt.Fprintf(f, "        Rolloff/centroid ratio: %.2f > 2.5 (rolloff %.0f Hz, centroid %.0f Hz)\n",
				cfg.DS201LPRolloffRatio, m.SpectralRolloff, m.SpectralCentroid)
		case "flat spectral slope":
			fmt.Fprintf(f, "        Spectral slope: %.2e > -1e-05 (unusual HF emphasis)\n", m.SpectralSlope)
		case "high ZCR with low centroid":
			fmt.Fprintf(f, "        ZCR: %.4f > 0.10, centroid %.0f Hz < 4000 Hz (HF noise pattern)\n",
				m.ZeroCrossingsRate, m.SpectralCentroid)
		}
	}
}

// formatNoiseRemoveFilter outputs NoiseRemove (anlmdn + compand) filter details
// Uses Non-Local Means denoiser followed by compand for residual suppression
func formatNoiseRemoveFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.NoiseRemoveEnabled {
		fmt.Fprintf(f, "%snoiseremove: DISABLED\n", prefix)
		return
	}

	// Header: filter name and algorithm
	fmt.Fprintf(f, "%snoiseremove: anlmdn + compand (Non-Local Means denoiser)\n", prefix)

	// anlmdn parameters (fixed from spike validation)
	fmt.Fprintf(f, "        anlmdn: s=%.5f, p=%.4fs, r=%.4fs, m=%.0f\n",
		cfg.NoiseRemoveStrength,
		cfg.NoiseRemovePatchSec,
		cfg.NoiseRemoveResearchSec,
		cfg.NoiseRemoveSmooth)

	// compand parameters and rationale - show noise floor source
	if m != nil && m.NoiseProfile != nil && m.NoiseProfile.MeasuredNoiseFloor < 0 {
		fmt.Fprintf(f, "        noise floor: %.1f dBFS (from silence regions)\n",
			m.NoiseProfile.MeasuredNoiseFloor)
		fmt.Fprintf(f, "        compand: threshold %.0f dB (floor + 5dB), expansion %.0f dB\n",
			cfg.NoiseRemoveCompandThreshold,
			cfg.NoiseRemoveCompandExpansion)
	} else {
		fmt.Fprintf(f, "        compand: threshold %.0f dB, expansion %.0f dB (defaults - no noise profile)\n",
			cfg.NoiseRemoveCompandThreshold,
			cfg.NoiseRemoveCompandExpansion)
	}
	fmt.Fprintf(f, "        timing: attack %.0fms, decay %.0fms, knee %.0f dB\n",
		cfg.NoiseRemoveCompandAttack*1000,
		cfg.NoiseRemoveCompandDecay*1000,
		cfg.NoiseRemoveCompandKnee)
}

// formatDS201GateFilter outputs DS201-inspired gate filter details
func formatDS201GateFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DS201GateEnabled {
		fmt.Fprintf(f, "%sDS201 gate: DISABLED\n", prefix)
		return
	}

	thresholdDB := processor.LinearToDb(cfg.DS201GateThreshold)
	rangeDB := processor.LinearToDb(cfg.DS201GateRange)

	detection := cfg.DS201GateDetection
	if detection == "" {
		detection = "rms"
	}

	// Show mode indicator if gentle mode is active
	modeNote := ""
	if cfg.DS201GateGentleMode {
		modeNote = " [gentle mode]"
	}

	fmt.Fprintf(f, "%sDS201 gate: threshold %.1f dB, ratio %.1f:1, detection %s%s\n", prefix, thresholdDB, cfg.DS201GateRatio, detection, modeNote)
	fmt.Fprintf(f, "        Timing: attack %.2fms, release %.0fms (soft expander)\n", cfg.DS201GateAttack, cfg.DS201GateRelease)
	fmt.Fprintf(f, "        Range: %.1f dB reduction, knee %.1f\n", rangeDB, cfg.DS201GateKnee)

	// Show rationale based on measurements
	if m != nil {
		var rationale []string

		// Threshold rationale - must match logic in calculateDS201GateThreshold
		// Peak reference is used when: crest > 20 AND peak != 0 AND lufsGap < 25
		lufsGap := cfg.TargetI - m.InputI
		if lufsGap < 0 {
			lufsGap = 0
		}
		usePeakRef := m.NoiseProfile != nil &&
			m.NoiseProfile.CrestFactor > 20 &&
			m.NoiseProfile.PeakLevel != 0 &&
			lufsGap < 25

		if usePeakRef {
			rationale = append(rationale, fmt.Sprintf("peak ref %.1f dB (crest %.1f dB)", m.NoiseProfile.PeakLevel, m.NoiseProfile.CrestFactor))
		} else if lufsGap >= 25 && m.NoiseProfile != nil && m.NoiseProfile.CrestFactor > 20 {
			rationale = append(rationale, fmt.Sprintf("noise floor %.1f dB (extreme LUFS gap %.0f dB, ignoring crest)", m.NoiseFloor, lufsGap))
		} else {
			rationale = append(rationale, fmt.Sprintf("noise floor %.1f dB", m.NoiseFloor))
		}

		// Ratio rationale
		if m.InputLRA > 0 {
			lraType := "moderate"
			if m.InputLRA > 15 {
				lraType = "wide"
			} else if m.InputLRA < 10 {
				lraType = "narrow"
			}
			rationale = append(rationale, fmt.Sprintf("LRA %.1f LU (%s)", m.InputLRA, lraType))
		}

		// Noise character for range/detection and release
		// Thresholds: very tonal < 0.10, tonal < 0.12, mixed < 0.16, broadband >= 0.16
		if m.NoiseProfile != nil {
			entropy := m.NoiseProfile.Entropy
			if entropy < 0.10 {
				rationale = append(rationale, fmt.Sprintf("very tonal (entropy %.2f, slow release)", entropy))
			} else if entropy < 0.12 {
				rationale = append(rationale, fmt.Sprintf("tonal (entropy %.2f)", entropy))
			} else if entropy < 0.16 {
				rationale = append(rationale, fmt.Sprintf("mixed (entropy %.2f, faster release)", entropy))
			} else {
				rationale = append(rationale, fmt.Sprintf("broadband-ish (entropy %.2f, fast release)", entropy))
			}
		}

		// Gentle mode rationale - for extreme LUFS gap + low LRA recordings
		if cfg.DS201GateGentleMode {
			rationale = append(rationale, "gentle mode (extreme LUFS gap + low LRA)")
		}

		if len(rationale) > 0 {
			fmt.Fprintf(f, "        Rationale: %s\n", strings.Join(rationale, ", "))
		}

		// Show aggression-based threshold calculation
		if cfg.DS201GateAggression > 0 {
			fmt.Fprintf(f, "        Aggression: %.2f (separation %.1f dB)\n",
				cfg.DS201GateAggression, cfg.DS201GateSpeechSeparation)
			fmt.Fprintf(f, "        Quiet speech: %.1f dB, Dynamic range: %.1f dB\n",
				cfg.DS201GateQuietSpeechEstimate, cfg.DS201GateDynamicRange)
			if cfg.DS201GateClampReason != "none" {
				fmt.Fprintf(f, "        Clamped by: %s (unclamped: %.1f dB)\n",
					cfg.DS201GateClampReason, cfg.DS201GateThresholdUnclamped)
			}
			fmt.Fprintf(f, "        Headroom above quiet speech: %.1f dB\n",
				-cfg.DS201GateSpeechHeadroom) // Negative because threshold is above quiet speech
		}
	}
}

// formatLA2ACompressorFilter outputs LA-2A Compressor filter details
func formatLA2ACompressorFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.LA2AEnabled {
		fmt.Fprintf(f, "%sLA-2A Compressor: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sLA-2A Compressor: threshold %.0f dB, ratio %.1f:1\n", prefix, cfg.LA2AThreshold, cfg.LA2ARatio)
	fmt.Fprintf(f, "        Timing: attack %.0fms, release %.0fms\n", cfg.LA2AAttack, cfg.LA2ARelease)
	fmt.Fprintf(f, "        Mix: %.0f%%, knee %.1f\n", cfg.LA2AMix*100, cfg.LA2AKnee)

	// Show rationale with measurement sources
	if m != nil && m.DynamicRange > 0 {
		dynamicsType := "moderate"
		if m.DynamicRange > 30 {
			dynamicsType = "expressive (preserving transients)"
		} else if m.DynamicRange < 20 {
			dynamicsType = "already compressed"
		}
		fmt.Fprintf(f, "        Rationale: DR %.1f dB (%s), LRA %.1f LU\n", m.DynamicRange, dynamicsType, m.InputLRA)

		// Show kurtosis and flux with sources (used for ratio and release tuning)
		kurtosis := m.SpectralKurtosis
		flux := m.SpectralFlux
		kurtosisSource := "full-file"
		fluxSource := "full-file"
		if m.SpeechProfile != nil {
			if m.SpeechProfile.SpectralKurtosis > 0 {
				kurtosis = m.SpeechProfile.SpectralKurtosis
				kurtosisSource = "speech region"
			}
			if m.SpeechProfile.SpectralFlux > 0 {
				flux = m.SpeechProfile.SpectralFlux
				fluxSource = "speech region"
			}
		}
		fmt.Fprintf(f, "        spectral kurtosis: %.1f (%s)\n", kurtosis, kurtosisSource)
		fmt.Fprintf(f, "        spectral flux: %.4f (%s)\n", flux, fluxSource)
	}
}

// formatDeesserFilter outputs deesser filter details
func formatDeesserFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DeessEnabled {
		fmt.Fprintf(f, "%sdeesser: DISABLED\n", prefix)
		return
	}
	if cfg.DeessIntensity == 0 {
		// Enabled but intensity is 0 - adaptive tuning determined no de-essing needed
		fmt.Fprintf(f, "%sdeesser: inactive: no sibilance detected\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sdeesser: intensity %.0f%%, amount %.0f%%, freq %.0f%%\n",
		prefix, cfg.DeessIntensity*100, cfg.DeessAmount*100, cfg.DeessFreq*100)

	// Show rationale with measurement source
	if m != nil && m.SpectralCentroid > 0 {
		// Determine which values were used and their sources
		centroid := m.SpectralCentroid
		rolloff := m.SpectralRolloff
		centroidSource := "full-file"
		rolloffSource := "full-file"
		if m.SpeechProfile != nil {
			if m.SpeechProfile.SpectralCentroid > 0 {
				centroid = m.SpeechProfile.SpectralCentroid
				centroidSource = "speech region"
			}
			if m.SpeechProfile.SpectralRolloff > 0 {
				rolloff = m.SpeechProfile.SpectralRolloff
				rolloffSource = "speech region"
			}
		}

		voiceType := "normal"
		if centroid > 7000 {
			voiceType = "very bright"
		} else if centroid > 6000 {
			voiceType = "bright"
		}
		fmt.Fprintf(f, "        Rationale: %s voice\n", voiceType)
		fmt.Fprintf(f, "        spectral centroid: %.0f Hz (%s)\n", centroid, centroidSource)
		fmt.Fprintf(f, "        spectral rolloff: %.0f Hz (%s)\n", rolloff, rolloffSource)
	}
}

// formatVolumaxFilter outputs CBS Volumax-inspired limiter filter details with rationale
func formatVolumaxFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.VolumaxEnabled {
		fmt.Fprintf(f, "%sCBS Volumax limiter: DISABLED\n", prefix)
		return
	}

	// Header with ceiling
	fmt.Fprintf(f, "%sCBS Volumax limiter: ceiling %.1f dBTP\n", prefix, cfg.VolumaxCeiling)

	// Timing with classification
	attackClass := classifyVolumaxAttack(cfg.VolumaxAttack)
	releaseClass := classifyVolumaxRelease(cfg.VolumaxRelease)
	fmt.Fprintf(f, "        Timing: attack %.1fms (%s), release %.0fms (%s)\n",
		cfg.VolumaxAttack, attackClass, cfg.VolumaxRelease, releaseClass)

	// ASC mode
	if cfg.VolumaxASC {
		fmt.Fprintf(f, "        ASC: enabled (level %.2f) — program-dependent release\n", cfg.VolumaxASCLevel)
	} else {
		fmt.Fprintln(f, "        ASC: disabled — direct limiting")
	}

	// Gain staging (only show if not unity)
	if cfg.VolumaxInputLevel != 1.0 || cfg.VolumaxOutputLevel != 1.0 {
		inputDB := processor.LinearToDb(cfg.VolumaxInputLevel)
		outputDB := processor.LinearToDb(cfg.VolumaxOutputLevel)
		fmt.Fprintf(f, "        Gain: input %.1f dB, output %.1f dB\n", inputDB, outputDB)
	}

	// Rationale from measurements
	if m != nil {
		// Normalize MaxDifference to percentage (from sample units 0-32768)
		maxDiffPct := (m.MaxDifference / 32768.0) * 100
		fmt.Fprintf(f, "        Rationale: MaxDiff %.1f%%, Crest %.1f dB, DR %.1f dB, Flux %.4f\n",
			maxDiffPct, m.SpectralCrest, m.DynamicRange, m.SpectralFlux)
	}
}

// classifyVolumaxAttack returns a human-readable attack classification
func classifyVolumaxAttack(attack float64) string {
	switch {
	case attack <= 2:
		return "fast (may introduce artifacts)"
	case attack <= 5:
		return "transparent"
	case attack <= 10:
		return "very gentle"
	default:
		return "slow"
	}
}

// classifyVolumaxRelease returns a human-readable release classification
func classifyVolumaxRelease(release float64) string {
	switch {
	case release >= 150:
		return "very smooth"
	case release >= 100:
		return "smooth"
	case release >= 50:
		return "moderate"
	default:
		return "fast (may pump)"
	}
}

// formatDownmixFilter outputs downmix filter details
func formatDownmixFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.DownmixEnabled {
		fmt.Fprintf(f, "%sdownmix: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sdownmix: stereo → mono (FFmpeg builtin)\n", prefix)
}

// formatAnalysisFilter outputs analysis filter details
func formatAnalysisFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.AnalysisEnabled {
		fmt.Fprintf(f, "%sanalysis: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sanalysis: collect audio measurements (ebur128 + astats + aspectralstats)\n", prefix)
}

// formatResampleFilter outputs resample filter details
func formatResampleFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.ResampleEnabled {
		fmt.Fprintf(f, "%sresample: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sresample: %d Hz %s mono, %d samples/frame\n",
		prefix, cfg.ResampleSampleRate, cfg.ResampleFormat, cfg.ResampleFrameSize)
}

// =============================================================================
// Tabular Report Section Writers (Phase 3 restructure)
// =============================================================================

// writeReportHeader outputs the report header with file info and timestamp.
func writeReportHeader(f *os.File, data ReportData) {
	fmt.Fprintln(f, "Jivetalking Analysis Report")
	fmt.Fprintln(f, "============================")
	fmt.Fprintf(f, "File: %s\n", filepath.Base(data.InputPath))
	fmt.Fprintf(f, "Processed: %s\n", data.EndTime.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(f, "Duration: %s\n", formatDuration(time.Duration(data.DurationSecs*float64(time.Second))))
	fmt.Fprintln(f, "")
}

// writeProcessingSummary outputs the processing time summary for all passes.
func writeProcessingSummary(f *os.File, data ReportData) {
	writeSection(f, "Processing Summary")

	fmt.Fprintf(f, "Pass 1 (Analysis):    %s\n", formatDuration(data.Pass1Time))
	fmt.Fprintf(f, "Pass 2 (Processing):  %s\n", formatDuration(data.Pass2Time))

	if data.Pass3Time > 0 || data.Pass4Time > 0 {
		fmt.Fprintf(f, "Pass 3 (Measuring):   %s\n", formatDuration(data.Pass3Time))
		fmt.Fprintf(f, "Pass 4 (Normalising): %s\n", formatDuration(data.Pass4Time))
	} else if data.Result != nil && data.Result.NormResult != nil && data.Result.NormResult.Skipped {
		fmt.Fprintln(f, "Pass 3 (Measuring):   skipped")
		fmt.Fprintln(f, "Pass 4 (Normalising): skipped")
	}

	totalTime := data.EndTime.Sub(data.StartTime)
	fmt.Fprintf(f, "Total:                %s", formatDuration(totalTime))

	if data.DurationSecs > 0 {
		audioDuration := time.Duration(data.DurationSecs * float64(time.Second))
		rtf := float64(audioDuration) / float64(totalTime)
		fmt.Fprintf(f, " (%.0fx real-time)", rtf)
	}
	fmt.Fprintln(f, "")
	fmt.Fprintln(f, "")
}

// writeFilterChainApplied outputs the filter chain section.
func writeFilterChainApplied(f *os.File, config *processor.FilterChainConfig, measurements *processor.AudioMeasurements) {
	formatFilterChain(f, config, measurements)
	fmt.Fprintln(f, "")
}

// writeLoudnessTable outputs a three-column comparison table for loudness metrics.
// Columns: Input (Pass 1), Filtered (Pass 2), Final (Pass 4)
func writeLoudnessTable(f *os.File, input *processor.AudioMeasurements, filtered *processor.OutputMeasurements, final *processor.OutputMeasurements) {
	writeSection(f, "Loudness Measurements")

	table := NewMetricTable()

	// Integrated Loudness
	inputI := math.NaN()
	filteredI := math.NaN()
	finalI := math.NaN()
	if input != nil {
		inputI = input.InputI
	}
	if filtered != nil {
		filteredI = filtered.OutputI
	}
	if final != nil {
		finalI = final.OutputI
	}
	table.AddMetricRow("Integrated Loudness", inputI, filteredI, finalI, 1, "LUFS", "")

	// True Peak
	inputTP := math.NaN()
	filteredTP := math.NaN()
	finalTP := math.NaN()
	if input != nil {
		inputTP = input.InputTP
	}
	if filtered != nil {
		filteredTP = filtered.OutputTP
	}
	if final != nil {
		finalTP = final.OutputTP
	}
	table.AddMetricRow("True Peak", inputTP, filteredTP, finalTP, 1, "dBTP", "")

	// Loudness Range
	inputLRA := math.NaN()
	filteredLRA := math.NaN()
	finalLRA := math.NaN()
	if input != nil {
		inputLRA = input.InputLRA
	}
	if filtered != nil {
		filteredLRA = filtered.OutputLRA
	}
	if final != nil {
		finalLRA = final.OutputLRA
	}
	table.AddMetricRow("Loudness Range", inputLRA, filteredLRA, finalLRA, 1, "LU", "")

	// Sample Peak
	inputSP := math.NaN()
	filteredSP := math.NaN()
	finalSP := math.NaN()
	if input != nil {
		inputSP = input.SamplePeak
	}
	if filtered != nil {
		filteredSP = filtered.SamplePeak
	}
	if final != nil {
		finalSP = final.SamplePeak
	}
	table.AddMetricRow("Sample Peak", inputSP, filteredSP, finalSP, 1, "dBFS", "")

	// Momentary Loudness
	inputML := math.NaN()
	filteredML := math.NaN()
	finalML := math.NaN()
	if input != nil {
		inputML = input.MomentaryLoudness
	}
	if filtered != nil {
		filteredML = filtered.MomentaryLoudness
	}
	if final != nil {
		finalML = final.MomentaryLoudness
	}
	table.AddMetricRow("Momentary Loudness", inputML, filteredML, finalML, 1, "LUFS", "")

	// Short-term Loudness
	inputSTL := math.NaN()
	filteredSTL := math.NaN()
	finalSTL := math.NaN()
	if input != nil {
		inputSTL = input.ShortTermLoudness
	}
	if filtered != nil {
		filteredSTL = filtered.ShortTermLoudness
	}
	if final != nil {
		finalSTL = final.ShortTermLoudness
	}
	table.AddMetricRow("Short-term Loudness", inputSTL, filteredSTL, finalSTL, 1, "LUFS", "")

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
	inputRMS := math.NaN()
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
		if filteredDelta < 0 {
			reductionInterp = "noise increased"
		} else if filteredDelta < 3 {
			reductionInterp = "minimal reduction"
		} else if filteredDelta < 10 {
			reductionInterp = "good reduction"
		} else {
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
	inputPeak := math.NaN()
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
	inputCrest := math.NaN()
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
	inputEntropy := valOr(inputNoise, func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralEntropy })
	if inputNoise == nil {
		inputEntropy = noiseProfile.Entropy
	}
	filteredEntropy := valOr(filteredNoise, func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralEntropy })
	finalEntropy := valOr(finalNoise, func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralEntropy })

	v := func(field func(*processor.SilenceCandidateMetrics) float64) [3]float64 {
		return [3]float64{
			valOr(inputNoise, field),
			valOr(filteredNoise, field),
			valOr(finalNoise, field),
		}
	}

	addNoiseFloorMetricRows(table, []threeColMetricSpec{
		{"Spectral Mean", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralMean }), 6, "", 1, nil},
		{"Spectral Variance", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralVariance }), 6, "", 2, nil},
		{"Spectral Centroid", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralCentroid }), 0, "Hz", 0, nil},
		{"Spectral Spread", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralSpread }), 0, "Hz", 0, nil},
		{"Spectral Skewness", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralSkewness }), 3, "", 0, nil},
		{"Spectral Kurtosis", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralKurtosis }), 3, "", 0, nil},
		{"Spectral Entropy", [3]float64{inputEntropy, filteredEntropy, finalEntropy}, 6, "", 0, nil},
		{"Spectral Flatness", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralFlatness }), 6, "", 0, nil},
		{"Spectral Crest", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralCrest }), 3, "", 0, nil},
		{"Spectral Flux", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralFlux }), 6, "", 2, nil},
		{"Spectral Slope", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralSlope }), 9, "", 1, nil},
		{"Spectral Decrease", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralDecrease }), 6, "", 0, nil},
		{"Spectral Rolloff", v(func(m *processor.SilenceCandidateMetrics) float64 { return m.SpectralRolloff }), 0, "Hz", 0, nil},
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
	inputCentroid := valOr(inputSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralCentroid })
	filteredCentroid := valOr(filteredSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralCentroid })
	finalCentroid := valOr(finalSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralCentroid })
	inputEntropy := valOr(inputSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralEntropy })
	filteredEntropy := valOr(filteredSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralEntropy })
	finalEntropy := valOr(finalSpeech, func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralEntropy })

	sv := func(field func(*processor.SpeechCandidateMetrics) float64) [3]float64 {
		return [3]float64{
			valOr(inputSpeech, field),
			valOr(filteredSpeech, field),
			valOr(finalSpeech, field),
		}
	}

	addSpeechMetricRows(table, []threeColMetricSpec{
		{"Spectral Mean", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralMean }), 6, "", 1, nil},
		{"Spectral Variance", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralVariance }), 6, "", 2, nil},
		{"Spectral Centroid", [3]float64{inputCentroid, filteredCentroid, finalCentroid}, 0, "Hz", 0, interpretCentroid},
		{"Spectral Spread", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralSpread }), 0, "Hz", 0, interpretSpread},
		{"Spectral Skewness", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralSkewness }), 3, "", 0, interpretSkewness},
		{"Spectral Kurtosis", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralKurtosis }), 3, "", 0, interpretKurtosis},
		{"Spectral Entropy", [3]float64{inputEntropy, filteredEntropy, finalEntropy}, 6, "", 0, interpretEntropy},
		{"Spectral Flatness", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralFlatness }), 6, "", 0, interpretFlatness},
		{"Spectral Crest", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralCrest }), 3, "", 0, interpretCrest},
		{"Spectral Flux", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralFlux }), 6, "", 2, interpretFlux},
		{"Spectral Slope", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralSlope }), 9, "", 1, interpretSlope},
		{"Spectral Decrease", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralDecrease }), 6, "", 0, interpretDecrease},
		{"Spectral Rolloff", sv(func(m *processor.SpeechCandidateMetrics) float64 { return m.SpectralRolloff }), 0, "Hz", 0, interpretRolloff},
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
		if centroid < 1500 {
			return "warm, full-bodied"
		} else if centroid < 2500 {
			return "balanced, natural"
		} else if centroid < 4000 {
			return "present, forward"
		} else if centroid < 6000 {
			return "bright, crisp"
		} else if centroid > 6000 {
			return "extremely bright (possible HF noise)"
		}
		return "very bright"
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

// writeDiagnosticSilence outputs detailed silence detection diagnostics.
func writeDiagnosticSilence(f *os.File, measurements *processor.AudioMeasurements) {
	if measurements == nil {
		return
	}

	writeSection(f, "Diagnostic: Silence Detection")

	// Show adaptive silence detection threshold if different from default
	if measurements.SilenceDetectLevel != 0 && measurements.SilenceDetectLevel != -50.0 {
		fmt.Fprintf(f, "Silence Threshold:   %.1f dB (from %.1f dB noise floor estimate)\n",
			measurements.SilenceDetectLevel, measurements.PreScanNoiseFloor)
	}

	// Interval sampling summary with RMSLevel distribution analysis
	if len(measurements.IntervalSamples) > 0 {
		fmt.Fprintf(f, "Interval Samples:    %d × 250ms windows analysed\n", len(measurements.IntervalSamples))

		// Calculate and display RMSLevel distribution for silence detection debugging
		rmsValues := make([]float64, 0, len(measurements.IntervalSamples))
		for _, interval := range measurements.IntervalSamples {
			if interval.RMSLevel > -120 { // Exclude digital silence
				rmsValues = append(rmsValues, interval.RMSLevel)
			}
		}
		if len(rmsValues) >= 10 {
			sorted := make([]float64, len(rmsValues))
			copy(sorted, rmsValues)
			sort.Float64s(sorted)

			fmt.Fprintf(f, "  RMSLevel Dist:     min %.1f, p10 %.1f, p25 %.1f, p50 %.1f, p75 %.1f, p90 %.1f, max %.1f dBFS\n",
				sorted[0],
				sorted[len(sorted)/10],
				sorted[len(sorted)/4],
				sorted[len(sorted)/2],
				sorted[len(sorted)*3/4],
				sorted[len(sorted)*9/10],
				sorted[len(sorted)-1])

			// Find largest gap for silence/speech boundary detection
			var largestGap float64
			var gapIndex int
			for i := 1; i < len(sorted); i++ {
				gap := sorted[i] - sorted[i-1]
				if gap > largestGap {
					largestGap = gap
					gapIndex = i
				}
			}
			if gapIndex > 0 && gapIndex < len(sorted) {
				fmt.Fprintf(f, "  Largest Gap:       %.1f dB between %.1f and %.1f dBFS (%d intervals below)\n",
					largestGap, sorted[gapIndex-1], sorted[gapIndex], gapIndex)
			}
		}
	}

	// Silence candidates (all evaluated candidates with scores)
	if len(measurements.SilenceCandidates) > 0 {
		fmt.Fprintf(f, "Silence Candidates:  %d evaluated\n", len(measurements.SilenceCandidates))
		for i, c := range measurements.SilenceCandidates {
			// Check if this candidate was selected (may have been refined, so check original start if refined)
			isSelected := false
			if measurements.NoiseProfile != nil {
				if measurements.NoiseProfile.WasRefined {
					// Compare against original candidate bounds before refinement
					isSelected = c.Region.Start == measurements.NoiseProfile.OriginalStart
				} else {
					isSelected = c.Region.Start == measurements.NoiseProfile.Start
				}
			}

			if isSelected {
				fmt.Fprintf(f, "  Candidate %d:       %.1fs at %.1fs (score: %.3f) [SELECTED]\n",
					i+1, c.Region.Duration.Seconds(), c.Region.Start.Seconds(), c.Score)

				// Show refinement details if this candidate was refined to a golden sub-region
				if measurements.NoiseProfile.WasRefined {
					fmt.Fprintf(f, "    Refined:         %.1fs at %.1fs (golden sub-region)\n",
						measurements.NoiseProfile.Duration.Seconds(),
						measurements.NoiseProfile.Start.Seconds())
				}

				fmt.Fprintf(f, "    Amplitude:\n")
				fmt.Fprintf(f, "      RMS Level:     %.1f dBFS\n", c.RMSLevel)
				fmt.Fprintf(f, "      Peak Level:    %.1f dBFS\n", c.PeakLevel)
				fmt.Fprintf(f, "      Crest Factor:  %.1f dB\n", c.CrestFactor)
				fmt.Fprintf(f, "    Spectral:\n")
				fmt.Fprintf(f, "      Centroid:      %.0f Hz (%s)\n", c.SpectralCentroid, interpretCentroid(c.SpectralCentroid))
				fmt.Fprintf(f, "      Spread:        %.0f Hz\n", c.SpectralSpread)
				fmt.Fprintf(f, "      Rolloff:       %.0f Hz\n", c.SpectralRolloff)
				fmt.Fprintf(f, "      Flatness:      %.3f (%s)\n", c.SpectralFlatness, interpretFlatness(c.SpectralFlatness))
				fmt.Fprintf(f, "      Entropy:       %.3f (%s)\n", c.SpectralEntropy, interpretEntropy(c.SpectralEntropy))
				fmt.Fprintf(f, "      Kurtosis:      %.1f (%s)\n", c.SpectralKurtosis, interpretKurtosis(c.SpectralKurtosis))
				fmt.Fprintf(f, "      Skewness:      %.2f\n", c.SpectralSkewness)
				fmt.Fprintf(f, "      Flux:          %.4f\n", c.SpectralFlux)
				fmt.Fprintf(f, "      Slope:         %.2e\n", c.SpectralSlope)
				fmt.Fprintf(f, "    Loudness:\n")
				fmt.Fprintf(f, "      Momentary:     %.1f LUFS\n", c.MomentaryLUFS)
				fmt.Fprintf(f, "      Short-term:    %.1f LUFS\n", c.ShortTermLUFS)
				fmt.Fprintf(f, "      True Peak:     %.1f dBTP\n", c.TruePeak)
			} else {
				reason := ""
				if c.Score == 0.0 {
					reason = " — rejected: too loud"
				}
				fmt.Fprintf(f, "  Candidate %d:       %.1fs at %.1fs (score: %.3f, RMS %.1f dBFS)%s\n",
					i+1, c.Region.Duration.Seconds(), c.Region.Start.Seconds(), c.Score, c.RMSLevel, reason)
			}
		}
	} else if measurements.NoiseProfile != nil {
		fmt.Fprintf(f, "Silence Sample:      %.1fs at %.1fs\n",
			measurements.NoiseProfile.Duration.Seconds(),
			measurements.NoiseProfile.Start.Seconds())
		fmt.Fprintf(f, "  Noise Floor:       %.1f dBFS (RMS)\n", measurements.NoiseProfile.MeasuredNoiseFloor)
		fmt.Fprintf(f, "  Peak Level:        %.1f dBFS\n", measurements.NoiseProfile.PeakLevel)
		fmt.Fprintf(f, "  Crest Factor:      %.1f dB\n", measurements.NoiseProfile.CrestFactor)
	} else if len(measurements.SilenceRegions) > 0 {
		r := measurements.SilenceRegions[0]
		fmt.Fprintf(f, "Silence Detected:    %.1fs at %.1fs (no profile extracted)\n",
			r.Duration.Seconds(), r.Start.Seconds())
	} else {
		fmt.Fprintf(f, "Silence Candidates:  NONE FOUND\n")
		fmt.Fprintf(f, "  No silence regions detected in audio. Noise profiling unavailable.\n")
	}

	fmt.Fprintln(f, "")
}

// writeDiagnosticSpeech outputs detailed speech detection diagnostics.
func writeDiagnosticSpeech(f *os.File, measurements *processor.AudioMeasurements) {
	if measurements == nil {
		return
	}

	// Only output section if speech detection was attempted
	if len(measurements.SpeechRegions) == 0 && measurements.SpeechProfile == nil {
		return
	}

	writeSection(f, "Diagnostic: Speech Detection")

	// Show speech candidates summary
	if len(measurements.SpeechCandidates) > 0 {
		fmt.Fprintf(f, "Speech Candidates:   %d evaluated\n", len(measurements.SpeechCandidates))

		for i, c := range measurements.SpeechCandidates {
			// Check if this candidate was selected
			// For refined regions, compare against original start since Region.Start is the refined position
			isSelected := false
			if measurements.SpeechProfile != nil {
				if c.WasRefined {
					isSelected = c.OriginalStart == measurements.SpeechProfile.OriginalStart
				} else {
					isSelected = c.Region.Start == measurements.SpeechProfile.Region.Start
				}
			}

			if isSelected {
				fmt.Fprintf(f, "  Candidate %d:       %.1fs at %.1fs (score: %.3f) [SELECTED]\n",
					i+1, c.Region.Duration.Seconds(), c.Region.Start.Seconds(), c.Score)

				// Show refinement details if this candidate was refined to a golden sub-region
				if c.WasRefined {
					fmt.Fprintf(f, "    Refined:         %.1fs at %.1fs → %.1fs at %.1fs (golden sub-region)\n",
						c.OriginalDuration.Seconds(),
						c.OriginalStart.Seconds(),
						c.Region.Duration.Seconds(),
						c.Region.Start.Seconds())
				}

				fmt.Fprintf(f, "    Amplitude:\n")
				fmt.Fprintf(f, "      RMS Level:     %.1f dBFS\n", c.RMSLevel)
				fmt.Fprintf(f, "      Peak Level:    %.1f dBFS\n", c.PeakLevel)
				fmt.Fprintf(f, "      Crest Factor:  %.1f dB\n", c.CrestFactor)
				fmt.Fprintf(f, "    Spectral:\n")
				fmt.Fprintf(f, "      Centroid:      %.0f Hz (%s)\n", c.SpectralCentroid, interpretCentroid(c.SpectralCentroid))
				fmt.Fprintf(f, "      Spread:        %.0f Hz\n", c.SpectralSpread)
				fmt.Fprintf(f, "      Rolloff:       %.0f Hz\n", c.SpectralRolloff)
				fmt.Fprintf(f, "      Flatness:      %.3f (%s)\n", c.SpectralFlatness, interpretFlatness(c.SpectralFlatness))
				fmt.Fprintf(f, "      Entropy:       %.3f (%s)\n", c.SpectralEntropy, interpretEntropy(c.SpectralEntropy))
				fmt.Fprintf(f, "      Kurtosis:      %.1f (%s)\n", c.SpectralKurtosis, interpretKurtosis(c.SpectralKurtosis))
				fmt.Fprintf(f, "      Skewness:      %.2f\n", c.SpectralSkewness)
				fmt.Fprintf(f, "      Flux:          %.4f\n", c.SpectralFlux)
				fmt.Fprintf(f, "      Slope:         %.2e\n", c.SpectralSlope)
				if c.VoicingDensity > 0 {
					fmt.Fprintf(f, "    Voicing Density: %.1f%%\n", c.VoicingDensity*100)
				}
				fmt.Fprintf(f, "    Loudness:\n")
				fmt.Fprintf(f, "      Momentary:     %.1f LUFS\n", c.MomentaryLUFS)
				fmt.Fprintf(f, "      Short-term:    %.1f LUFS\n", c.ShortTermLUFS)
				fmt.Fprintf(f, "      True Peak:     %.1f dBTP\n", c.TruePeak)
			} else {
				fmt.Fprintf(f, "  Candidate %d:       %.1fs at %.1fs (score: %.3f, RMS %.1f dBFS)\n",
					i+1, c.Region.Duration.Seconds(), c.Region.Start.Seconds(), c.Score, c.RMSLevel)
			}
		}
	} else if measurements.SpeechProfile != nil {
		// Profile exists but no candidates list (shouldn't happen, but handle gracefully)
		profile := measurements.SpeechProfile
		fmt.Fprintf(f, "Elected Speech:      %.1fs at %.1fs\n",
			profile.Region.Duration.Seconds(), profile.Region.Start.Seconds())
		fmt.Fprintf(f, "  RMS Level:         %.1f dBFS\n", profile.RMSLevel)
		fmt.Fprintf(f, "  Peak Level:        %.1f dBFS\n", profile.PeakLevel)
		fmt.Fprintf(f, "  Crest Factor:      %.1f dB\n", profile.CrestFactor)
		fmt.Fprintf(f, "  Centroid:          %.0f Hz\n", profile.SpectralCentroid)
	} else if len(measurements.SpeechRegions) > 0 {
		fmt.Fprintf(f, "Speech Regions:      %d detected\n", len(measurements.SpeechRegions))
		fmt.Fprintf(f, "  No candidate met quality threshold for speech profiling.\n")
	} else {
		fmt.Fprintf(f, "Speech Candidates:   NONE FOUND\n")
		fmt.Fprintf(f, "  No speech regions detected (file may be too short or all silence).\n")
	}

	fmt.Fprintln(f, "")
}

// writeDiagnosticPeakLimiter outputs the Pass 4 pre-limiting diagnostics.
// The peak limiter creates headroom before loudnorm so it can apply full linear gain.
func writeDiagnosticPeakLimiter(f *os.File, result *processor.NormalisationResult, config *processor.FilterChainConfig) {
	if result == nil || result.Skipped {
		return
	}

	writeSection(f, "Diagnostic: Peak Limiter")

	if !result.LimiterEnabled {
		fmt.Fprintln(f, "Status: BYPASSED")
		fmt.Fprintln(f, "")
		fmt.Fprintln(f, "No pre-limiting required. Loudnorm can apply full linear gain without")
		fmt.Fprintln(f, "exceeding the target true peak ceiling.")
		fmt.Fprintln(f, "")
		projectedTP := result.InputTP + result.LimiterGain
		fmt.Fprintf(f, "Projected TP:    %.1f dBTP (gain %.1f dB applied to %.1f dBTP peaks)\n",
			projectedTP, result.LimiterGain, result.InputTP)
		fmt.Fprintf(f, "Target TP:       %.1f dBTP\n", config.LoudnormTargetTP)
		fmt.Fprintf(f, "Headroom:        %.1f dB\n", config.LoudnormTargetTP-projectedTP)
		fmt.Fprintln(f, "")
		return
	}

	fmt.Fprintln(f, "Status: ACTIVE")
	fmt.Fprintln(f, "")
	fmt.Fprintln(f, "Pre-limiting applied to create headroom for loudnorm's linear gain.")
	fmt.Fprintln(f, "Without limiting, loudnorm would either clip or reduce the target LUFS.")
	fmt.Fprintln(f, "")

	// Calculate projected TP without limiting
	projectedTPWithoutLimiter := result.InputTP + result.LimiterGain

	fmt.Fprintln(f, "Problem:")
	fmt.Fprintf(f, "  Input TP:          %.1f dBTP (peaks from Pass 2 filtered audio)\n", result.InputTP)
	fmt.Fprintf(f, "  Gain Required:     %+.1f dB (to reach %.1f LUFS from %.1f LUFS)\n",
		result.LimiterGain, config.LoudnormTargetI, result.InputLUFS)
	fmt.Fprintf(f, "  Projected TP:      %.1f dBTP (would exceed %.1f dBTP target by %.1f dB)\n",
		projectedTPWithoutLimiter, config.LoudnormTargetTP,
		projectedTPWithoutLimiter-config.LoudnormTargetTP)
	fmt.Fprintln(f, "")

	fmt.Fprintln(f, "Solution (CBS Volumax-inspired peak limiting):")
	fmt.Fprintf(f, "  Limiter Ceiling:   %.1f dBTP\n", result.LimiterCeiling)
	fmt.Fprintf(f, "  Peak Reduction:    %.1f dB (from %.1f to %.1f dBTP)\n",
		result.InputTP-result.LimiterCeiling, result.InputTP, result.LimiterCeiling)
	fmt.Fprintln(f, "")

	fmt.Fprintln(f, "Filter parameters:")
	fmt.Fprintln(f, "  Attack:    5 ms     (gentle - preserves transient shape)")
	fmt.Fprintln(f, "  Release:   100 ms   (smooth recovery, eliminates pumping)")
	fmt.Fprintln(f, "  ASC:       enabled  (Auto Soft Clipping for program-dependent smoothing)")
	fmt.Fprintln(f, "  ASC Level: 0.8      (high smoothing - Volumax characteristic)")
	fmt.Fprintln(f, "")

	fmt.Fprintln(f, "Rationale:")
	fmt.Fprintln(f, "  The CBS Volumax was the broadcast standard for transparent limiting.")
	fmt.Fprintln(f, "  Gentle attack preserves transients; smooth release is essentially inaudible.")
	fmt.Fprintln(f, "  Only peaks above the ceiling are affected (typically <5% of audio).")
	fmt.Fprintln(f, "")
}

// writeDiagnosticLoudnorm outputs detailed loudnorm normalisation diagnostics.
func writeDiagnosticLoudnorm(f *os.File, result *processor.NormalisationResult, config *processor.FilterChainConfig) {
	if result == nil || !config.LoudnormEnabled {
		return
	}

	writeSection(f, "Diagnostic: Loudnorm")

	if result.Skipped {
		fmt.Fprintln(f, "Status: SKIPPED (already within target)")
		fmt.Fprintln(f, "")
		return
	}

	fmt.Fprintln(f, "Loudnorm configuration:")
	if result.LinearModeForced {
		fmt.Fprintf(f, "  Target I:   %.1f LUFS (adjusted from %.1f to preserve linear mode)\n",
			result.EffectiveTargetI, result.RequestedTargetI)
	} else {
		fmt.Fprintf(f, "  Target I:   %.1f LUFS\n", config.LoudnormTargetI)
	}
	fmt.Fprintf(f, "  Target TP:  %.1f dBTP\n", config.LoudnormTargetTP)
	fmt.Fprintf(f, "  Target LRA: %.1f LU\n", config.LoudnormTargetLRA)
	fmt.Fprintf(f, "  Mode:       %s\n", loudnormModeString(config.LoudnormLinear))
	fmt.Fprintf(f, "  Dual mono:  %v\n", config.LoudnormDualMono)
	fmt.Fprintf(f, "  Gain:       %+.2f dB\n", result.GainApplied)

	// Display loudnorm filter's unique diagnostic info (I/TP/LRA values are in Loudness table)
	if result.LoudnormStats != nil {
		stats := result.LoudnormStats
		fmt.Fprintln(f, "")
		fmt.Fprintln(f, "FFmpeg diagnostics:")
		fmt.Fprintf(f, "  Input Thresh:    %s LUFS\n", stats.InputThresh)
		fmt.Fprintf(f, "  Output Thresh:   %s LUFS\n", stats.OutputThresh)
		fmt.Fprintf(f, "  Norm Type:       %s\n", stats.NormalizationType)
		fmt.Fprintf(f, "  Target Offset:   %s dB\n", stats.TargetOffset)
	}

	fmt.Fprintln(f, "")
	effectiveDeviation := math.Abs(result.OutputLUFS - result.EffectiveTargetI)
	if result.WithinTarget {
		if result.LinearModeForced {
			requestedDeviation := math.Abs(result.OutputLUFS - result.RequestedTargetI)
			fmt.Fprintf(f, "Result: ✓ Linear mode preserved (%.2f LU from effective target, %.2f LU from requested)\n",
				effectiveDeviation, requestedDeviation)
		} else {
			fmt.Fprintf(f, "Result: ✓ Within target (deviation: %.2f LU)\n", effectiveDeviation)
		}
	} else {
		fmt.Fprintf(f, "Result: ⚠ Outside tolerance (deviation: %.2f LU)\n", effectiveDeviation)
	}

	fmt.Fprintln(f, "")
}

// getFinalMeasurements safely extracts final measurements from the result.
func getFinalMeasurements(result *processor.ProcessingResult) *processor.OutputMeasurements {
	if result == nil || result.NormResult == nil {
		return nil
	}
	return result.NormResult.FinalMeasurements
}
