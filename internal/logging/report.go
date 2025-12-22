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

// interpretCentroid describes the spectral "brightness" based on centroid frequency.
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
		return "very dark, bass-heavy"
	case hz < 1500:
		return "warm, full-bodied"
	case hz < 2500:
		return "balanced, natural voice"
	case hz < 4000:
		return "present, forward"
	case hz < 6000:
		return "bright, crisp"
	default:
		return "very bright, potentially harsh"
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
	case hz < 500:
		return "narrow, tonal, focused"
	case hz < 1500:
		return "moderate bandwidth, typical voiced"
	case hz < 3000:
		return "wide bandwidth, mixed content"
	default:
		return "very wide, noise-like or broadband"
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
		return "HF concentrated, sibilant-like"
	case skew < 0.5:
		return "symmetric distribution"
	case skew < 1.5:
		return "LF emphasis with HF tail (typical voice)"
	default:
		return "strongly bass-concentrated"
	}
}

// interpretKurtosis describes the spectral peakiness.
// Kurtosis measures how peaked vs flat the spectrum is; indicates harmonic clarity vs noise.
// Higher values: peaked/tonal spectrum with dominant frequencies.
// Lower values: flatter spectrum, more noise-like.
//
// Healthy voiced speech typically 4-8; pathological or noisy voice trends toward 3.
func interpretKurtosis(kurt float64) string {
	switch {
	case kurt < 2.5:
		return "flat, noise-dominated"
	case kurt < 3.5:
		return "Gaussian-like, mixed content"
	case kurt < 5.0:
		return "moderately peaked, good harmonics"
	case kurt < 8.0:
		return "clearly peaked, strong harmonics"
	default:
		return "highly peaked, very tonal"
	}
}

// interpretEntropy describes spectral randomness/order.
// Entropy measures disorder in spectral energy distribution (0 to 1 normalised).
// Lower entropy: ordered/predictable spectrum (tonal content, harmonic structure).
// Higher entropy: random/unpredictable spectrum (noise, complex transients).
//
// Note: If values appear outside 0-1 range, the metric may be unnormalised (raw bits).
func interpretEntropy(entropy float64) string {
	switch {
	case entropy < 0.3:
		return "highly ordered, clear pitch"
	case entropy < 0.5:
		return "moderately ordered, typical voiced"
	case entropy < 0.7:
		return "mixed order, voiced with noise"
	case entropy < 0.9:
		return "disordered, noise-like"
	default:
		return "highly disordered, approaching white noise"
	}
}

// interpretFlatness describes the tonality vs noise character.
// Flatness is the ratio of geometric mean to arithmetic mean of spectrum (0 to 1).
// Values near 0: tonal/harmonic content with spectral peaks.
// Values near 1: flat/white noise spectrum.
//
// Clean voiced speech 0.1-0.3; breathy voice 0.3-0.5; fricatives 0.4-0.7.
func interpretFlatness(flatness float64) string {
	switch {
	case flatness < 0.1:
		return "highly tonal, pure harmonics"
	case flatness < 0.25:
		return "tonal with some noise, clean voiced"
	case flatness < 0.4:
		return "moderate tonality, typical speech"
	case flatness < 0.6:
		return "mixed tonal/noise, breathy content"
	default:
		return "noise-dominant, very breathy"
	}
}

// interpretCrest describes the peak-to-average ratio of the spectrum.
// Higher crest: spectrum has prominent peaks (tonal, resonant).
// Lower crest: spectrum is more uniform (noise-like, broadband).
//
// Higher crest indicates clearer harmonics; low crest suggests noise contamination.
func interpretCrest(crest float64) string {
	switch {
	case crest < 10:
		return "flat spectrum, noise-like"
	case crest < 25:
		return "moderate peaks, mixed content"
	case crest < 40:
		return "prominent peaks, clear harmonics"
	case crest < 60:
		return "strong peaks, very tonal"
	default:
		return "extremely peaked, dominant frequency"
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
		return "very stable, sustained phonation"
	case flux < 0.01:
		return "stable, steady speech"
	case flux < 0.05:
		return "moderate variation, natural articulation"
	case flux < 0.2:
		return "high variation, transients/plosives"
	default:
		return "rapid change, onsets/consonant bursts"
	}
}

// interpretDecrease describes low-frequency weighted spectral decrease.
// Similar to slope but emphasises low-frequency behaviour.
// Used for characterising bass/warmth in the signal.
//
// Male voices typically show more negative decrease than female voices.
func interpretDecrease(decrease float64) string {
	switch {
	case decrease < -0.05:
		return "strong bass concentration, warm foundation"
	case decrease < -0.02:
		return "moderate LF emphasis, typical male voice"
	case decrease < 0:
		return "balanced low-frequency content"
	default:
		return "weak bass, thin, lacking body"
	}
}

// interpretRolloff describes the effective bandwidth of the signal.
// Rolloff is the frequency below which 85-95% of spectral energy exists.
// Higher rolloff indicates more high-frequency content; useful for sibilance detection.
func interpretRolloff(hz float64) string {
	switch {
	case hz < 3000:
		return "dark, muffled, heavy filtering"
	case hz < 5000:
		return "warm, controlled high frequencies"
	case hz < 8000:
		return "balanced brightness, natural speech"
	case hz < 12000:
		return "bright, airy, good articulation"
	default:
		return "very bright, significant sibilance"
	}
}

// interpretSlope describes the overall spectral tilt (linear regression coefficient).
// Modal speech approximately -6 dB/octave; breathy voice steeper; pressed voice shallower.
func interpretSlope(slope float64) string {
	switch {
	case slope < -5e-05:
		return "steep negative slope, dark/warm"
	case slope < -1e-05:
		return "moderate slope, balanced brightness"
	case slope < 0:
		return "shallow slope, bright/energetic"
	default:
		return "positive slope, very bright"
	}
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
// 5. Spectral Characteristics - four-column table with interpretations
// 6. Noise Floor Analysis - three-column table
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

	// Spectral Characteristics Table (Input → Filtered → Final → Interpretation)
	writeSpectralTable(f, inputMeasurements, filteredMeasurements, finalMeasurements)

	// Noise Floor Analysis Table
	var inputNoise *processor.NoiseProfile
	if inputMeasurements != nil {
		inputNoise = inputMeasurements.NoiseProfile
	}
	writeNoiseFloorTable(f, inputNoise, getFilteredNoise(data.Result), getFinalNoise(data.Result))

	return nil
}

// formatNormalisationResult outputs the loudnorm normalisation pass details
func formatNormalisationResult(f *os.File, result *processor.NormalisationResult, config *processor.FilterChainConfig) {
	writeSection(f, "Pass 3: Loudnorm Measurement")

	if result == nil || !config.LoudnormEnabled {
		fmt.Fprintln(f, "Status: DISABLED")
		return
	}

	if result.Skipped {
		fmt.Fprintln(f, "Status: SKIPPED")
		return
	}

	fmt.Fprintln(f, "Status: APPLIED")
	fmt.Fprintln(f, "")
	fmt.Fprintln(f, "Pre-normalisation (Pass 2 output):")
	fmt.Fprintf(f, "  Integrated loudness: %.1f LUFS\n", result.InputLUFS)
	fmt.Fprintf(f, "  True peak:           %.1f dBTP\n", result.InputTP)
	fmt.Fprintln(f, "")

	writeSection(f, "Pass 4: Loudnorm Normalisation")
	fmt.Fprintln(f, "")
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
	fmt.Fprintf(f, "  Offset:     %+.2f dB\n", result.GainApplied)

	// Display loudnorm measurement (from Pass 3, used for Pass 4 parameters)
	fmt.Fprintln(f, "")
	fmt.Fprintln(f, "Loudnorm measurement (from Pass 3):")
	fmt.Fprintf(f, "  Input I:         %.2f LUFS\n", result.InputLUFS)
	fmt.Fprintf(f, "  Input TP:        %.2f dBTP\n", result.InputTP)
	fmt.Fprintf(f, "  Target Offset:   %.2f dB (from loudnorm, used in Pass 4)\n", result.GainApplied)

	// Display loudnorm filter's second pass stats (parsed from JSON output)
	if result.LoudnormStats != nil {
		stats := result.LoudnormStats
		fmt.Fprintln(f, "")
		fmt.Fprintln(f, "Loudnorm second pass diagnostics:")
		fmt.Fprintf(f, "  Input I:         %s LUFS\n", stats.InputI)
		fmt.Fprintf(f, "  Input TP:        %s dBTP\n", stats.InputTP)
		fmt.Fprintf(f, "  Input LRA:       %s LU\n", stats.InputLRA)
		fmt.Fprintf(f, "  Input Thresh:    %s LUFS\n", stats.InputThresh)
		fmt.Fprintf(f, "  Output I:        %s LUFS\n", stats.OutputI)
		fmt.Fprintf(f, "  Output TP:       %s dBTP\n", stats.OutputTP)
		fmt.Fprintf(f, "  Output LRA:      %s LU\n", stats.OutputLRA)
		fmt.Fprintf(f, "  Output Thresh:   %s LUFS\n", stats.OutputThresh)
		fmt.Fprintf(f, "  Norm Type:       %s\n", stats.NormalizationType)
		fmt.Fprintf(f, "  Target Offset:   %s dB\n", stats.TargetOffset)
	}

	fmt.Fprintln(f, "")
	fmt.Fprintln(f, "Post-normalisation:")
	fmt.Fprintf(f, "  Integrated loudness: %.1f LUFS\n", result.OutputLUFS)
	fmt.Fprintf(f, "  True peak:           %.1f dBTP\n", result.OutputTP)

	fmt.Fprintln(f, "")
	// Calculate deviation from effective target (what loudnorm was actually targeting)
	effectiveDeviation := math.Abs(result.OutputLUFS - result.EffectiveTargetI)
	if result.WithinTarget {
		if result.LinearModeForced {
			// Target was adjusted to preserve linear mode
			requestedDeviation := math.Abs(result.OutputLUFS - result.RequestedTargetI)
			fmt.Fprintf(f, "Result: ✓ Linear mode preserved (%.2f LU from effective target, %.2f LU from requested)\n",
				effectiveDeviation, requestedDeviation)
		} else {
			fmt.Fprintf(f, "Result: ✓ Within target (deviation: %.2f LU)\n", effectiveDeviation)
		}
	} else {
		fmt.Fprintf(f, "Result: ⚠ Outside tolerance (deviation: %.2f LU)\n", effectiveDeviation)
	}
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
	case processor.FilterDC1Declick:
		formatDC1DeclickFilter(f, cfg, prefix)
	case processor.FilterDNS1500:
		formatDNS1500Filter(f, cfg, m, prefix)
	case processor.FilterDolbySR:
		formatDolbySRFilter(f, cfg, m, prefix)
	case processor.FilterArnndn:
		formatArnndnFilter(f, cfg, m, prefix)
	case processor.FilterDS201Gate:
		formatDS201GateFilter(f, cfg, m, prefix)
	case processor.FilterLA2ACompressor:
		formatLA2ACompressorFilter(f, cfg, m, prefix)
	case processor.FilterDeesser:
		formatDeesserFilter(f, cfg, m, prefix)
	case processor.FilterUREI1176:
		formatUREI1176Filter(f, cfg, m, prefix)
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

		// Show noise character if tonal (explains why no boost)
		if m.NoiseProfile != nil && m.NoiseProfile.Entropy < 0.5 {
			fmt.Fprintf(f, "        Note: no LF boost (tonal noise, entropy %.3f — DS201 hum filter handles it)\n", m.NoiseProfile.Entropy)
		}
	}

	// DS201HighPass is composite: also show hum notch filter details if harmonics > 0
	if cfg.DS201HumHarmonics > 0 {
		formatDS201HumFilterInternal(f, cfg, m, prefix)
	}
}

// formatDS201HumFilterInternal outputs DS201-inspired hum notch filter details (called from DS201HighPass composite)
func formatDS201HumFilterInternal(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	// Note: enabled check is done by caller (formatDS201HighpassFilter)

	// Build the header with width and transform info
	transformInfo := ""
	if cfg.DS201HumTransform == "tdii" {
		transformInfo = ", tdii"
	} else if cfg.DS201HumTransform != "" {
		transformInfo = ", " + cfg.DS201HumTransform
	}
	fmt.Fprintf(f, "%sDS201 hum filter: %.0f Hz + %d harmonics (%.1f Hz wide%s)\n",
		prefix, cfg.DS201HumFrequency, cfg.DS201HumHarmonics, cfg.DS201HumWidth, transformInfo)

	if m != nil && m.NoiseProfile != nil {
		fmt.Fprintf(f, "        Rationale: tonal noise detected (entropy %.3f < 0.7)\n", m.NoiseProfile.Entropy)

		// Explain reduced harmonics for warm voices
		if cfg.DS201HumHarmonics <= 2 {
			isWarmSkewness := m.SpectralSkewness > 1.0
			isWarmDecrease := m.SpectralDecrease < -0.02
			if isWarmSkewness || isWarmDecrease {
				reason := ""
				if isWarmSkewness && isWarmDecrease {
					reason = fmt.Sprintf("skewness %.2f, decrease %.3f", m.SpectralSkewness, m.SpectralDecrease)
				} else if isWarmSkewness {
					reason = fmt.Sprintf("skewness %.2f", m.SpectralSkewness)
				} else {
					reason = fmt.Sprintf("decrease %.3f", m.SpectralDecrease)
				}
				fmt.Fprintf(f, "        Harmonics: reduced to %d (warm voice: %s — protecting vocal fundamentals)\n",
					cfg.DS201HumHarmonics, reason)
			}
		}

		// Explain notch width choice (always show for completeness)
		if cfg.DS201HumWidth <= 0.3 {
			fmt.Fprintf(f, "        Width: %.1f Hz (very narrow — warm voice protection)\n", cfg.DS201HumWidth)
		} else if cfg.DS201HumWidth < 1.0 {
			fmt.Fprintf(f, "        Width: %.1f Hz (narrow surgical notch — very tonal hum)\n", cfg.DS201HumWidth)
		} else {
			fmt.Fprintf(f, "        Width: %.1f Hz (standard surgical notch)\n", cfg.DS201HumWidth)
		}

		// Explain transform type
		if cfg.DS201HumTransform == "tdii" {
			fmt.Fprintf(f, "        Transform: TDII (transposed direct form II — best floating-point accuracy)\n")
		} else if cfg.DS201HumTransform != "" && cfg.DS201HumTransform != "di" {
			fmt.Fprintf(f, "        Transform: %s\n", cfg.DS201HumTransform)
		}

		// Explain mix if not full wet
		if cfg.DS201HumMix > 0 && cfg.DS201HumMix < 1.0 {
			mixReason := "warm voice"
			if cfg.DS201HumMix <= 0.7 {
				mixReason = "very warm voice"
			}
			fmt.Fprintf(f, "        Mix: %.0f%% (%s — blending filtered with dry signal)\n", cfg.DS201HumMix*100, mixReason)
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

// formatDC1DeclickFilter outputs CEDAR DC-1-inspired declicker filter details
func formatDC1DeclickFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.DC1DeclickEnabled {
		if cfg.DC1DeclickReason != "" {
			fmt.Fprintf(f, "%sDC1 Declick: DISABLED (%s)\n", prefix, cfg.DC1DeclickReason)
		} else {
			fmt.Fprintf(f, "%sDC1 Declick: DISABLED\n", prefix)
		}
		return
	}

	method := "overlap-save"
	if cfg.DC1DeclickMethod == "a" {
		method = "overlap-add"
	}
	fmt.Fprintf(f, "%sDC1 Declick: ENABLED\n", prefix)
	fmt.Fprintf(f, "        Threshold: %.0f (1=aggressive, 8=conservative)\n", cfg.DC1DeclickThreshold)
	fmt.Fprintf(f, "        Window: %.0fms, Overlap: %.0f%%, AR Order: %.0f%%\n", cfg.DC1DeclickWindow, cfg.DC1DeclickOverlap, cfg.DC1DeclickAROrder)
	fmt.Fprintf(f, "        Method: %s\n", method)
	if cfg.DC1DeclickReason != "" {
		fmt.Fprintf(f, "        Reason: %s\n", cfg.DC1DeclickReason)
	}
}

// formatDNS1500Filter outputs CEDAR DNS-1500-inspired noise reduction filter details
// Uses afftdn with inline noise learning via asendcmd during detected silence
func formatDNS1500Filter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DNS1500Enabled {
		if cfg.DolbySREnabled {
			fmt.Fprintf(f, "%sDNS-1500: DISABLED (DolbySR forced on)\n", prefix)
		} else {
			fmt.Fprintf(f, "%sDNS-1500: DISABLED (no silence detected, using DolbySR fallback)\n", prefix)
		}
		return
	}

	// Header with key parameters
	fmt.Fprintf(f, "%sDNS-1500: Noise Reduction (inline noise learning)\n", prefix)
	fmt.Fprintf(f, "        Noise Reduction: %.1f dB\n", cfg.DNS1500NoiseReduce)
	fmt.Fprintf(f, "        Noise Floor: %.1f dB\n", cfg.DNS1500NoiseFloor)
	fmt.Fprintf(f, "        Track Noise: %v\n", cfg.DNS1500TrackNoise)
	fmt.Fprintf(f, "        Adaptivity: %.2f\n", cfg.DNS1500Adaptivity)
	fmt.Fprintf(f, "        Gain Smooth: %d\n", cfg.DNS1500GainSmooth)
	fmt.Fprintf(f, "        Residual Floor: %.1f dB\n", cfg.DNS1500ResidFloor)
	fmt.Fprintf(f, "        Silence Window: %.3fs – %.3fs\n", cfg.DNS1500SilenceStart, cfg.DNS1500SilenceEnd)

	// Show compand (residual crusher) parameters
	fmt.Fprintf(f, "        Compand: threshold %.0f dB, expansion %.0f dB\n",
		cfg.DNS1500CompandThreshold, cfg.DNS1500CompandExpansion)
	fmt.Fprintf(f, "          Timing: attack %.0fms, decay %.0fms, knee %.0f dB\n",
		cfg.DNS1500CompandAttack*1000, cfg.DNS1500CompandDecay*1000, cfg.DNS1500CompandKnee)
	fmt.Fprintf(f, "          Curve: FLAT (uniform expansion below threshold)\n")

	// Show adaptive rationale
	if m != nil && m.NoiseProfile != nil {
		np := m.NoiseProfile

		// Noise character description
		var noiseChar string
		switch {
		case np.SpectralFlatness > 0.6:
			noiseChar = "broadband (hiss)"
		case np.SpectralFlatness > 0.4:
			noiseChar = "mixed"
		default:
			noiseChar = "tonal (hum/bleed)"
		}

		// LRA-based adaptivity description
		var adaptivityDesc string
		switch {
		case m.InputLRA < 6.0:
			adaptivityDesc = "fast (uniform material)"
		case m.InputLRA > 15.0:
			adaptivityDesc = "slow (dynamic material)"
		default:
			adaptivityDesc = "moderate"
		}

		// Calculate effective floor for rationale display
		effectiveFloor := np.MeasuredNoiseFloor - cfg.DNS1500NoiseReduce

		fmt.Fprintf(f, "        Rationale: measured floor %.1f dBFS, %s noise → %.1f dB reduction\n",
			np.MeasuredNoiseFloor, noiseChar, cfg.DNS1500NoiseReduce)
		fmt.Fprintf(f, "        Adaptivity: LRA %.1f LU → %s adaptation\n", m.InputLRA, adaptivityDesc)
		fmt.Fprintf(f, "        Compand: effective floor %.1f dBFS (post-afftdn) → %.0f dB expansion to -80 target\n",
			effectiveFloor, cfg.DNS1500CompandExpansion)
		fmt.Fprintf(f, "        DNS-1500 philosophy: learn noise from silence, track continuously\n")
	}
}

// formatDolbySRFilter outputs Dolby SR-inspired denoise filter details
// Uses 6-band mcompand multiband expander with FLAT reduction curve
func formatDolbySRFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DolbySREnabled {
		fmt.Fprintf(f, "%sDolby SR: DISABLED\n", prefix)
		return
	}

	// Header: filter name and expansion level
	fmt.Fprintf(f, "%sDolby SR: Noise Reduction (6-band voice-protective expander)\n", prefix)
	fmt.Fprintf(f, "        Expansion: %.0f dB (FLAT reduction curve)\n", cfg.DolbySRExpansionDB)
	fmt.Fprintf(f, "        Threshold: %.0f dB\n", cfg.DolbySRThresholdDB)

	// Show 6-band configuration
	if len(cfg.DolbySRBands) == 6 {
		fmt.Fprintf(f, "        Bands:\n")
		bandNames := []string{"Sub-bass", "Chest", "Voice F1", "Voice F2", "Presence", "Air"}
		for i, band := range cfg.DolbySRBands {
			bandExp := cfg.DolbySRExpansionDB * band.ScalePercent / 100.0
			fmt.Fprintf(f, "          %s (0-%d Hz): %.0fdB, %.0fms/%.0fms, knee %.0f\n",
				bandNames[i], int(band.CrossoverHz), bandExp,
				band.Attack*1000, band.Decay*1000, band.SoftKnee)
		}
	}

	// Show adaptive rationale
	if m != nil {
		// Classify source based on RMS trough (aligned with lockstep threshold+expansion tiers)
		// Tiers: < -85 dB (clean), -85 to -80 dB (moderate), > -80 dB (noisy)
		var severityDesc string
		switch {
		case m.RMSTrough < -85:
			severityDesc = "clean"
		case m.RMSTrough < -80:
			severityDesc = "moderate"
		default:
			severityDesc = "noisy"
		}

		// Lockstep rationale (threshold + expansion tuned together)
		var thresholdRationale string
		switch {
		case m.RMSTrough < -85:
			thresholdRationale = "gentle treatment"
		case m.RMSTrough < -80:
			thresholdRationale = "balanced treatment"
		default:
			thresholdRationale = "aggressive treatment"
		}

		fmt.Fprintf(f, "        Rationale: %s source (trough %.1f dBFS) → %.0f dB expansion, %.0f dB threshold (%s)\n",
			severityDesc, m.RMSTrough, cfg.DolbySRExpansionDB, cfg.DolbySRThresholdDB, thresholdRationale)

		// SR philosophy note
		fmt.Fprintf(f, "        SR philosophy: 6-band voice-protective, FLAT curve (gate handles silence)\n")
	}
}

// formatArnndnFilter outputs arnndn filter details
func formatArnndnFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.ArnnDnEnabled {
		fmt.Fprintf(f, "%sarnndn: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sarnndn: mix %.0f%%\n", prefix, cfg.ArnnDnMix*100)

	// Show rationale with adjustments
	if m != nil {
		var adjustments []string

		// Base mix explanation (matches thresholds in adaptive.go calculateArnnDnMix)
		var baseMixDesc string
		switch {
		case m.NoiseFloor > -50:
			baseMixDesc = "noisy"
		case m.NoiseFloor > -65:
			baseMixDesc = "moderate"
		case m.NoiseFloor > -75:
			baseMixDesc = "fairly clean"
		default:
			baseMixDesc = "very clean"
		}

		fmt.Fprintf(f, "        Base: %s (noise floor %.1f dBFS)\n", baseMixDesc, m.NoiseFloor)

		// Adjustments applied
		if m.SpectralKurtosis > 8 {
			adjustments = append(adjustments, fmt.Sprintf("-10%% kurtosis %.1f", m.SpectralKurtosis))
		}
		// MaxDifference is in sample units (0-32768 for 16-bit); normalise to 0-1
		maxDiffNorm := m.MaxDifference / 32768.0
		if maxDiffNorm > 0.25 {
			adjustments = append(adjustments, fmt.Sprintf("-10%% transients %.0f%%", maxDiffNorm*100))
		}
		if m.InputLRA > 15 {
			adjustments = append(adjustments, fmt.Sprintf("-5%% LRA %.1f LU", m.InputLRA))
		}
		if m.SpectralFlatness > 0.5 {
			adjustments = append(adjustments, fmt.Sprintf("+10%% flatness %.2f", m.SpectralFlatness))
		}
		if m.NoiseProfile != nil && m.NoiseProfile.Entropy > 0.5 {
			adjustments = append(adjustments, fmt.Sprintf("+10%% entropy %.2f", m.NoiseProfile.Entropy))
		}

		if len(adjustments) > 0 {
			fmt.Fprintf(f, "        Adjustments: %s\n", joinWithComma(adjustments))
		}
	}
}

// joinWithComma joins string slice with comma separator
func joinWithComma(items []string) string {
	result := ""
	for i, item := range items {
		if i > 0 {
			result += ", "
		}
		result += item
	}
	return result
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

	// Show rationale
	if m != nil && m.DynamicRange > 0 {
		dynamicsType := "moderate"
		if m.DynamicRange > 30 {
			dynamicsType = "expressive (preserving transients)"
		} else if m.DynamicRange < 20 {
			dynamicsType = "already compressed"
		}
		fmt.Fprintf(f, "        Rationale: DR %.1f dB (%s), LRA %.1f LU\n", m.DynamicRange, dynamicsType, m.InputLRA)
	}
}

// formatDeesserFilter outputs deesser filter details
func formatDeesserFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DeessEnabled || cfg.DeessIntensity == 0 {
		fmt.Fprintf(f, "%sdeesser: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sdeesser: intensity %.0f%%, amount %.0f%%, freq %.0f%%\n",
		prefix, cfg.DeessIntensity*100, cfg.DeessAmount*100, cfg.DeessFreq*100)

	// Show rationale
	if m != nil && m.SpectralCentroid > 0 {
		voiceType := "normal"
		if m.SpectralCentroid > 7000 {
			voiceType = "very bright"
		} else if m.SpectralCentroid > 6000 {
			voiceType = "bright"
		}
		fmt.Fprintf(f, "        Rationale: %s voice (centroid %.0f Hz, rolloff %.0f Hz)\n",
			voiceType, m.SpectralCentroid, m.SpectralRolloff)
	}
}

// formatUREI1176Filter outputs UREI 1176-inspired limiter filter details with rationale
func formatUREI1176Filter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.UREI1176Enabled {
		fmt.Fprintf(f, "%sUREI 1176 limiter: DISABLED\n", prefix)
		return
	}

	// Header with ceiling
	fmt.Fprintf(f, "%sUREI 1176 limiter: ceiling %.1f dBTP\n", prefix, cfg.UREI1176Ceiling)

	// Timing with classification
	attackClass := classifyUREI1176Attack(cfg.UREI1176Attack)
	releaseClass := classifyUREI1176Release(cfg.UREI1176Release)
	fmt.Fprintf(f, "        Timing: attack %.1fms (%s), release %.0fms (%s)\n",
		cfg.UREI1176Attack, attackClass, cfg.UREI1176Release, releaseClass)

	// ASC mode
	if cfg.UREI1176ASC {
		fmt.Fprintf(f, "        ASC: enabled (level %.2f) — program-dependent release\n", cfg.UREI1176ASCLevel)
	} else {
		fmt.Fprintln(f, "        ASC: disabled — direct limiting")
	}

	// Gain staging (only show if not unity)
	if cfg.UREI1176InputLevel != 1.0 || cfg.UREI1176OutputLevel != 1.0 {
		inputDB := processor.LinearToDb(cfg.UREI1176InputLevel)
		outputDB := processor.LinearToDb(cfg.UREI1176OutputLevel)
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

// classifyUREI1176Attack returns a human-readable attack classification
func classifyUREI1176Attack(attack float64) string {
	switch {
	case attack <= 0.1:
		return "extreme transients"
	case attack <= 0.5:
		return "sharp consonants"
	case attack <= 0.8:
		return "normal speech"
	default:
		return "soft delivery"
	}
}

// classifyUREI1176Release returns a human-readable release classification
func classifyUREI1176Release(release float64) string {
	switch {
	case release >= 200:
		return "expressive"
	case release <= 100:
		return "controlled"
	default:
		return "standard"
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

// writeSpectralTable outputs a four-column comparison table for spectral metrics.
// Columns: Input (Pass 1), Filtered (Pass 2), Final (Pass 4), Interpretation (based on Final)
func writeSpectralTable(f *os.File, input *processor.AudioMeasurements, filtered *processor.OutputMeasurements, final *processor.OutputMeasurements) {
	writeSection(f, "Spectral Characteristics")

	// Create a custom table with interpretation column
	table := &MetricTable{
		Headers: []string{"Input", "Filtered", "Final"},
		Rows:    make([]MetricRow, 0),
	}

	// Helper to get spectral value from appropriate source (prefer final, then filtered, then input)
	getSpectralForInterp := func(inputVal, filteredVal, finalVal float64) float64 {
		if final != nil && !math.IsNaN(finalVal) {
			return finalVal
		}
		if filtered != nil && !math.IsNaN(filteredVal) {
			return filteredVal
		}
		return inputVal
	}

	// Spectral Mean (no interpretation - just a magnitude)
	inputMean := math.NaN()
	filteredMean := math.NaN()
	finalMean := math.NaN()
	if input != nil {
		inputMean = input.SpectralMean
	}
	if filtered != nil {
		filteredMean = filtered.SpectralMean
	}
	if final != nil {
		finalMean = final.SpectralMean
	}
	table.AddRow("Spectral Mean",
		[]string{formatMetric(inputMean, 6), formatMetric(filteredMean, 6), formatMetric(finalMean, 6)},
		"", "")

	// Spectral Variance (no interpretation)
	inputVar := math.NaN()
	filteredVar := math.NaN()
	finalVar := math.NaN()
	if input != nil {
		inputVar = input.SpectralVariance
	}
	if filtered != nil {
		filteredVar = filtered.SpectralVariance
	}
	if final != nil {
		finalVar = final.SpectralVariance
	}
	table.AddRow("Spectral Variance",
		[]string{formatMetric(inputVar, 6), formatMetric(filteredVar, 6), formatMetric(finalVar, 6)},
		"", "")

	// Centroid
	inputCentroid := math.NaN()
	filteredCentroid := math.NaN()
	finalCentroid := math.NaN()
	if input != nil {
		inputCentroid = input.SpectralCentroid
	}
	if filtered != nil {
		filteredCentroid = filtered.SpectralCentroid
	}
	if final != nil {
		finalCentroid = final.SpectralCentroid
	}
	interp := interpretCentroid(getSpectralForInterp(inputCentroid, filteredCentroid, finalCentroid))
	table.AddRow("Centroid",
		[]string{formatMetric(inputCentroid, 0), formatMetric(filteredCentroid, 0), formatMetric(finalCentroid, 0)},
		"Hz", interp)

	// Spread
	inputSpread := math.NaN()
	filteredSpread := math.NaN()
	finalSpread := math.NaN()
	if input != nil {
		inputSpread = input.SpectralSpread
	}
	if filtered != nil {
		filteredSpread = filtered.SpectralSpread
	}
	if final != nil {
		finalSpread = final.SpectralSpread
	}
	interp = interpretSpread(getSpectralForInterp(inputSpread, filteredSpread, finalSpread))
	table.AddRow("Spread",
		[]string{formatMetric(inputSpread, 0), formatMetric(filteredSpread, 0), formatMetric(finalSpread, 0)},
		"Hz", interp)

	// Skewness
	inputSkew := math.NaN()
	filteredSkew := math.NaN()
	finalSkew := math.NaN()
	if input != nil {
		inputSkew = input.SpectralSkewness
	}
	if filtered != nil {
		filteredSkew = filtered.SpectralSkewness
	}
	if final != nil {
		finalSkew = final.SpectralSkewness
	}
	interp = interpretSkewness(getSpectralForInterp(inputSkew, filteredSkew, finalSkew))
	table.AddRow("Skewness",
		[]string{formatMetric(inputSkew, 3), formatMetric(filteredSkew, 3), formatMetric(finalSkew, 3)},
		"", interp)

	// Kurtosis
	inputKurt := math.NaN()
	filteredKurt := math.NaN()
	finalKurt := math.NaN()
	if input != nil {
		inputKurt = input.SpectralKurtosis
	}
	if filtered != nil {
		filteredKurt = filtered.SpectralKurtosis
	}
	if final != nil {
		finalKurt = final.SpectralKurtosis
	}
	interp = interpretKurtosis(getSpectralForInterp(inputKurt, filteredKurt, finalKurt))
	table.AddRow("Kurtosis",
		[]string{formatMetric(inputKurt, 3), formatMetric(filteredKurt, 3), formatMetric(finalKurt, 3)},
		"", interp)

	// Entropy
	inputEntropy := math.NaN()
	filteredEntropy := math.NaN()
	finalEntropy := math.NaN()
	if input != nil {
		inputEntropy = input.SpectralEntropy
	}
	if filtered != nil {
		filteredEntropy = filtered.SpectralEntropy
	}
	if final != nil {
		finalEntropy = final.SpectralEntropy
	}
	interp = interpretEntropy(getSpectralForInterp(inputEntropy, filteredEntropy, finalEntropy))
	table.AddRow("Entropy",
		[]string{formatMetric(inputEntropy, 6), formatMetric(filteredEntropy, 6), formatMetric(finalEntropy, 6)},
		"", interp)

	// Flatness
	inputFlat := math.NaN()
	filteredFlat := math.NaN()
	finalFlat := math.NaN()
	if input != nil {
		inputFlat = input.SpectralFlatness
	}
	if filtered != nil {
		filteredFlat = filtered.SpectralFlatness
	}
	if final != nil {
		finalFlat = final.SpectralFlatness
	}
	interp = interpretFlatness(getSpectralForInterp(inputFlat, filteredFlat, finalFlat))
	table.AddRow("Flatness",
		[]string{formatMetric(inputFlat, 6), formatMetric(filteredFlat, 6), formatMetric(finalFlat, 6)},
		"", interp)

	// Crest
	inputCrest := math.NaN()
	filteredCrest := math.NaN()
	finalCrest := math.NaN()
	if input != nil {
		inputCrest = input.SpectralCrest
	}
	if filtered != nil {
		filteredCrest = filtered.SpectralCrest
	}
	if final != nil {
		finalCrest = final.SpectralCrest
	}
	interp = interpretCrest(getSpectralForInterp(inputCrest, filteredCrest, finalCrest))
	table.AddRow("Crest",
		[]string{formatMetric(inputCrest, 3), formatMetric(filteredCrest, 3), formatMetric(finalCrest, 3)},
		"", interp)

	// Flux
	inputFlux := math.NaN()
	filteredFlux := math.NaN()
	finalFlux := math.NaN()
	if input != nil {
		inputFlux = input.SpectralFlux
	}
	if filtered != nil {
		filteredFlux = filtered.SpectralFlux
	}
	if final != nil {
		finalFlux = final.SpectralFlux
	}
	interp = interpretFlux(getSpectralForInterp(inputFlux, filteredFlux, finalFlux))
	table.AddRow("Flux",
		[]string{formatMetric(inputFlux, 6), formatMetric(filteredFlux, 6), formatMetric(finalFlux, 6)},
		"", interp)

	// Slope
	inputSlope := math.NaN()
	filteredSlope := math.NaN()
	finalSlope := math.NaN()
	if input != nil {
		inputSlope = input.SpectralSlope
	}
	if filtered != nil {
		filteredSlope = filtered.SpectralSlope
	}
	if final != nil {
		finalSlope = final.SpectralSlope
	}
	interp = interpretSlope(getSpectralForInterp(inputSlope, filteredSlope, finalSlope))
	table.AddRow("Slope",
		[]string{formatMetric(inputSlope, 9), formatMetric(filteredSlope, 9), formatMetric(finalSlope, 9)},
		"", interp)

	// Decrease
	inputDecrease := math.NaN()
	filteredDecrease := math.NaN()
	finalDecrease := math.NaN()
	if input != nil {
		inputDecrease = input.SpectralDecrease
	}
	if filtered != nil {
		filteredDecrease = filtered.SpectralDecrease
	}
	if final != nil {
		finalDecrease = final.SpectralDecrease
	}
	interp = interpretDecrease(getSpectralForInterp(inputDecrease, filteredDecrease, finalDecrease))
	table.AddRow("Decrease",
		[]string{formatMetric(inputDecrease, 6), formatMetric(filteredDecrease, 6), formatMetric(finalDecrease, 6)},
		"", interp)

	// Rolloff
	inputRolloff := math.NaN()
	filteredRolloff := math.NaN()
	finalRolloff := math.NaN()
	if input != nil {
		inputRolloff = input.SpectralRolloff
	}
	if filtered != nil {
		filteredRolloff = filtered.SpectralRolloff
	}
	if final != nil {
		finalRolloff = final.SpectralRolloff
	}
	interp = interpretRolloff(getSpectralForInterp(inputRolloff, filteredRolloff, finalRolloff))
	table.AddRow("Rolloff",
		[]string{formatMetric(inputRolloff, 0), formatMetric(filteredRolloff, 0), formatMetric(finalRolloff, 0)},
		"Hz", interp)

	fmt.Fprint(f, table.String())
	fmt.Fprintln(f, "")
}

// writeNoiseFloorTable outputs a three-column comparison table for noise floor metrics.
// Columns: Input (Pass 1 NoiseProfile), Filtered (Pass 2 SilenceSample), Final (Pass 4 SilenceSample)
func writeNoiseFloorTable(f *os.File, inputNoise *processor.NoiseProfile, filteredNoise *processor.SilenceAnalysis, finalNoise *processor.SilenceAnalysis) {
	writeSection(f, "Noise Floor Analysis")

	// Skip if no input noise profile
	if inputNoise == nil {
		fmt.Fprintln(f, "No silence detected in input — noise profiling unavailable")
		fmt.Fprintln(f, "")
		return
	}

	table := NewMetricTable()

	// RMS Level (noise floor)
	inputRMS := inputNoise.MeasuredNoiseFloor
	filteredRMS := math.NaN()
	finalRMS := math.NaN()
	if filteredNoise != nil {
		filteredRMS = filteredNoise.NoiseFloor
	}
	if finalNoise != nil {
		finalRMS = finalNoise.NoiseFloor
	}
	table.AddMetricRow("RMS Level", inputRMS, filteredRMS, finalRMS, 1, "dBFS", "")

	// Noise Reduction Delta (input - filtered/final, positive = reduction achieved)
	// Shows how much the noise floor was lowered by processing
	filteredDelta := math.NaN()
	finalDelta := math.NaN()
	if !math.IsNaN(inputRMS) && !math.IsNaN(filteredRMS) {
		filteredDelta = inputRMS - filteredRMS // Positive = noise reduced
	}
	if !math.IsNaN(inputRMS) && !math.IsNaN(finalRMS) {
		finalDelta = inputRMS - finalRMS
	}

	// Interpret the noise reduction effectiveness
	var reductionInterp string
	if !math.IsNaN(filteredDelta) {
		if filteredDelta < 0 {
			reductionInterp = "🟖 noise increased"
		} else if filteredDelta < 3 {
			reductionInterp = "minimal reduction"
		} else if filteredDelta < 10 {
			reductionInterp = "good reduction"
		} else {
			reductionInterp = "excellent reduction"
		}
	}

	// Format with explicit sign to show direction
	formatDelta := func(delta float64) string {
		if math.IsNaN(delta) {
			return MissingValue
		}
		if delta >= 0 {
			return fmt.Sprintf("+%.1f", delta)
		}
		return fmt.Sprintf("%.1f", delta)
	}
	table.AddRow("Noise Reduction",
		[]string{MissingValue, formatDelta(filteredDelta), formatDelta(finalDelta)},
		"dB", reductionInterp)

	// Peak Level
	inputPeak := inputNoise.PeakLevel
	filteredPeak := math.NaN()
	finalPeak := math.NaN()
	if filteredNoise != nil {
		filteredPeak = filteredNoise.PeakLevel
	}
	if finalNoise != nil {
		finalPeak = finalNoise.PeakLevel
	}
	table.AddMetricRow("Peak Level", inputPeak, filteredPeak, finalPeak, 1, "dBFS", "")

	// Crest Factor
	inputCrest := inputNoise.CrestFactor
	filteredCrest := math.NaN()
	finalCrest := math.NaN()
	if filteredNoise != nil {
		filteredCrest = filteredNoise.CrestFactor
	}
	if finalNoise != nil {
		finalCrest = finalNoise.CrestFactor
	}
	table.AddMetricRow("Crest Factor", inputCrest, filteredCrest, finalCrest, 1, "dB", "")

	// Entropy
	inputEntropy := inputNoise.Entropy
	filteredEntropy := math.NaN()
	finalEntropy := math.NaN()
	if filteredNoise != nil {
		filteredEntropy = filteredNoise.Entropy
	}
	if finalNoise != nil {
		finalEntropy = finalNoise.Entropy
	}
	table.AddMetricRow("Entropy", inputEntropy, filteredEntropy, finalEntropy, 3, "", "")

	// Character (interpretation row) - based on entropy
	getNoiseCharacter := func(entropy float64) string {
		if math.IsNaN(entropy) {
			return MissingValue
		}
		if entropy < 0.7 {
			return "tonal"
		} else if entropy < 0.9 {
			return "mixed"
		}
		return "broadband"
	}
	inputChar := getNoiseCharacter(inputEntropy)
	filteredChar := getNoiseCharacter(filteredEntropy)
	finalChar := getNoiseCharacter(finalEntropy)
	table.AddRow("Character", []string{inputChar, filteredChar, finalChar}, "", "")

	fmt.Fprint(f, table.String())
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

				fmt.Fprintf(f, "    RMS Level:       %.1f dBFS\n", c.RMSLevel)
				fmt.Fprintf(f, "    Peak Level:      %.1f dBFS\n", c.PeakLevel)
				fmt.Fprintf(f, "    Crest Factor:    %.1f dB\n", c.CrestFactor)
				fmt.Fprintf(f, "    Centroid:        %.0f Hz\n", c.SpectralCentroid)
				fmt.Fprintf(f, "    Flatness:        %.3f\n", c.SpectralFlatness)
				fmt.Fprintf(f, "    Kurtosis:        %.1f\n", c.SpectralKurtosis)
				noiseType := "broadband"
				if c.Entropy < 0.7 {
					noiseType = "tonal"
				} else if c.Entropy < 0.9 {
					noiseType = "mixed"
				}
				fmt.Fprintf(f, "    Entropy:         %.3f (%s)\n", c.Entropy, noiseType)
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

	fmt.Fprintln(f, "Solution (1176-inspired peak limiting):")
	fmt.Fprintf(f, "  Limiter Ceiling:   %.1f dBTP\n", result.LimiterCeiling)
	fmt.Fprintf(f, "  Peak Reduction:    %.1f dB (from %.1f to %.1f dBTP)\n",
		result.InputTP-result.LimiterCeiling, result.InputTP, result.LimiterCeiling)
	fmt.Fprintln(f, "")

	fmt.Fprintln(f, "Filter parameters:")
	fmt.Fprintln(f, "  Attack:    0.1 ms   (fast - catches all peaks)")
	fmt.Fprintln(f, "  Release:   50 ms    (quick recovery, avoids pumping)")
	fmt.Fprintln(f, "  ASC:       enabled  (Auto Soft Clipping for natural release)")
	fmt.Fprintln(f, "  ASC Level: 0.5      (moderate smoothing for speech)")
	fmt.Fprintln(f, "")

	fmt.Fprintln(f, "Rationale:")
	fmt.Fprintln(f, "  The UREI 1176 at 20:1 ratio was the broadcast standard for peak limiting.")
	fmt.Fprintln(f, "  Fast attack provides a \"firm lid\" on peaks; quick release stays transparent.")
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

// writeDiagnosticAdaptive outputs detailed adaptive parameter diagnostics.
// This section is filled by the existing formatFilterChain function.
// For now, we just write a header - the actual content comes from writeFilterChainApplied.
func writeDiagnosticAdaptive(f *os.File, config *processor.FilterChainConfig, measurements *processor.AudioMeasurements) {
	// The filter chain section already contains adaptive rationale for each filter.
	// This function is a placeholder for additional adaptive debugging if needed.
	// Currently, all adaptive info is in writeFilterChainApplied.
}

// getFinalMeasurements safely extracts final measurements from the result.
func getFinalMeasurements(result *processor.ProcessingResult) *processor.OutputMeasurements {
	if result == nil || result.NormResult == nil {
		return nil
	}
	return result.NormResult.FinalMeasurements
}

// getFilteredNoise safely extracts filtered noise profile from the result.
func getFilteredNoise(result *processor.ProcessingResult) *processor.SilenceAnalysis {
	if result == nil || result.FilteredMeasurements == nil {
		return nil
	}
	return result.FilteredMeasurements.SilenceSample
}

// getFinalNoise safely extracts final noise profile from the result.
func getFinalNoise(result *processor.ProcessingResult) *processor.SilenceAnalysis {
	if result == nil || result.NormResult == nil || result.NormResult.FinalMeasurements == nil {
		return nil
	}
	return result.NormResult.FinalMeasurements.SilenceSample
}
