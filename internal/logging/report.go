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
	case hz < 800:
		return "narrow, tonal, focused"
	case hz < 2000:
		return "moderate bandwidth, typical voiced"
	case hz < 3500:
		return "wide bandwidth, natural speech"
	default:
		return "very wide, mixed voiced/unvoiced"
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
	case skew < 2.5:
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
// FFmpeg aspectralstats outputs normalised entropy (divided by log(size)).
// Values range 0-1 where 0=pure tone, 1=white noise.
// Reference: Misra et al. (2004) ICASSP; Essentia Entropy algorithm.
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

// interpretFlatness describes tonality vs noisiness (Wiener entropy).
// Ratio of geometric mean to arithmetic mean. 0=pure tone, 1=white noise.
// Reference: MPEG-7 AudioSpectralFlatness; Johnston (1988); Dubnov (2004).
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
// Crest factor = max(spectrum) / mean(spectrum), expressed as LINEAR RATIO (not dB).
// To convert to dB: crest_dB = 20 * log10(crest).
// Reference: Peeters (2003) CUIDADO project; Essentia Crest algorithm.
//
// Typical values:
//   - White noise: ~3-5 (peaks barely exceed mean)
//   - Distributed harmonics: 8-25 (multiple peaks)
//   - Clear harmonics: 25-100 (prominent peaks)
//   - Single dominant frequency: >100 (extreme peakiness)
func interpretCrest(crest float64) string {
	switch {
	case crest < 8:
		return "flat spectrum, noise-like"
	case crest < 25:
		return "moderate peaks, distributed harmonics"
	case crest < 50:
		return "prominent peaks, clear harmonics"
	case crest < 100:
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

// interpretDecrease describes low-frequency weighted spectral slope.
// FFmpeg computes: sum((mag[k] - mag[0]) / k) / sum(mag[k])
// Positive values indicate spectrum decreases from low to high frequencies (typical for speech).
// Negative values indicate rising spectrum (unusual, HF emphasis).
// Reference: Peeters (2003) CUIDADO project; Essentia Decrease algorithm.
func interpretDecrease(decrease float64) string {
	switch {
	case decrease < 0:
		return "rising spectrum, HF emphasis"
	case decrease < 0.05:
		return "flat/balanced spectrum"
	case decrease < 0.10:
		return "moderate decrease, typical speech"
	default:
		return "strong decrease, LF emphasis"
	}
}

// interpretRolloff describes effective bandwidth via 85% energy threshold.
// Returns Hz below which 85% of spectral energy resides.
// Reference: Peeters (2003) CUIDADO; librosa spectral_rolloff.
func interpretRolloff(hz float64) string {
	switch {
	case hz < 2000:
		return "dark, muffled, heavy filtering"
	case hz < 4000:
		return "warm, controlled high frequencies"
	case hz < 7000:
		return "balanced brightness, natural speech"
	case hz < 11000:
		return "bright, airy, good articulation"
	default:
		return "very bright, significant sibilance"
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

	// Noise Floor Analysis Table
	writeNoiseFloorTable(f, inputMeasurements, filteredMeasurements, getFinalMeasurements(data.Result))

	// Speech Region Analysis Table
	writeSpeechRegionTable(f, inputMeasurements, filteredMeasurements, getFinalMeasurements(data.Result))

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
	case processor.FilterNoiseRemove:
		formatNoiseRemoveFilter(f, cfg, m, prefix)
	case processor.FilterDC1Declick:
		formatDC1DeclickFilter(f, cfg, prefix)
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

	// compand parameters (adaptive)
	fmt.Fprintf(f, "        compand: threshold %.0f dB, expansion %.0f dB\n",
		cfg.NoiseRemoveCompandThreshold,
		cfg.NoiseRemoveCompandExpansion)
	fmt.Fprintf(f, "        timing: attack %.0fms, decay %.0fms, knee %.0f dB\n",
		cfg.NoiseRemoveCompandAttack*1000,
		cfg.NoiseRemoveCompandDecay*1000,
		cfg.NoiseRemoveCompandKnee)

	// Show adaptive rationale if noise profile available
	if m != nil && m.NoiseProfile != nil && m.NoiseProfile.Duration > 0 {
		fmt.Fprintf(f, "        Rationale: noise floor %.1f dB → target -90 dB (%.0f dB expansion)\n",
			m.NoiseProfile.MeasuredNoiseFloor,
			cfg.NoiseRemoveCompandExpansion)
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

// writeNoiseFloorTable outputs a three-column comparison table for noise floor metrics.
// Columns: Input (Pass 1 elected silence candidate), Filtered (Pass 2 SilenceSample), Final (Pass 4 SilenceSample)
func writeNoiseFloorTable(f *os.File, inputMeasurements *processor.AudioMeasurements, filteredMeasurements *processor.OutputMeasurements, finalMeasurements *processor.OutputMeasurements) {
	writeSection(f, "Noise Floor Analysis")

	// Skip if no input measurements or noise profile
	if inputMeasurements == nil || inputMeasurements.NoiseProfile == nil {
		fmt.Fprintln(f, "No silence detected in input — noise profiling unavailable")
		fmt.Fprintln(f, "")
		return
	}

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

	// Spectral Mean
	inputMean := math.NaN()
	filteredMean := math.NaN()
	finalMean := math.NaN()
	if inputNoise != nil {
		inputMean = inputNoise.SpectralMean
	}
	if filteredNoise != nil {
		filteredMean = filteredNoise.SpectralMean
	}
	if finalNoise != nil {
		finalMean = finalNoise.SpectralMean
	}
	table.AddRow("Spectral Mean",
		[]string{
			formatMetric(inputMean, 6),
			formatMetricSpectral(filteredMean, 6, filteredIsDigitalSilence),
			formatMetricSpectral(finalMean, 6, finalIsDigitalSilence),
		}, "", "")

	// Spectral Variance
	inputVar := math.NaN()
	filteredVar := math.NaN()
	finalVar := math.NaN()
	if inputNoise != nil {
		inputVar = inputNoise.SpectralVariance
	}
	if filteredNoise != nil {
		filteredVar = filteredNoise.SpectralVariance
	}
	if finalNoise != nil {
		finalVar = finalNoise.SpectralVariance
	}
	table.AddRow("Spectral Variance",
		[]string{
			formatMetric(inputVar, 6),
			formatMetricSpectral(filteredVar, 6, filteredIsDigitalSilence),
			formatMetricSpectral(finalVar, 6, finalIsDigitalSilence),
		}, "", "")

	// Spectral Centroid
	inputCentroid := math.NaN()
	filteredCentroid := math.NaN()
	finalCentroid := math.NaN()
	if inputNoise != nil {
		inputCentroid = inputNoise.SpectralCentroid
	}
	if filteredNoise != nil {
		filteredCentroid = filteredNoise.SpectralCentroid
	}
	if finalNoise != nil {
		finalCentroid = finalNoise.SpectralCentroid
	}
	table.AddRow("Spectral Centroid",
		[]string{
			formatMetric(inputCentroid, 0),
			formatMetricSpectral(filteredCentroid, 0, filteredIsDigitalSilence),
			formatMetricSpectral(finalCentroid, 0, finalIsDigitalSilence),
		}, "Hz", "")

	// Spectral Spread
	inputSpread := math.NaN()
	filteredSpread := math.NaN()
	finalSpread := math.NaN()
	if inputNoise != nil {
		inputSpread = inputNoise.SpectralSpread
	}
	if filteredNoise != nil {
		filteredSpread = filteredNoise.SpectralSpread
	}
	if finalNoise != nil {
		finalSpread = finalNoise.SpectralSpread
	}
	table.AddRow("Spectral Spread",
		[]string{
			formatMetric(inputSpread, 0),
			formatMetricSpectral(filteredSpread, 0, filteredIsDigitalSilence),
			formatMetricSpectral(finalSpread, 0, finalIsDigitalSilence),
		}, "Hz", "")

	// Spectral Skewness
	inputSkew := math.NaN()
	filteredSkew := math.NaN()
	finalSkew := math.NaN()
	if inputNoise != nil {
		inputSkew = inputNoise.SpectralSkewness
	}
	if filteredNoise != nil {
		filteredSkew = filteredNoise.SpectralSkewness
	}
	if finalNoise != nil {
		finalSkew = finalNoise.SpectralSkewness
	}
	table.AddRow("Spectral Skewness",
		[]string{
			formatMetric(inputSkew, 3),
			formatMetricSpectral(filteredSkew, 3, filteredIsDigitalSilence),
			formatMetricSpectral(finalSkew, 3, finalIsDigitalSilence),
		}, "", "")

	// Spectral Kurtosis
	inputKurt := math.NaN()
	filteredKurt := math.NaN()
	finalKurt := math.NaN()
	if inputNoise != nil {
		inputKurt = inputNoise.SpectralKurtosis
	}
	if filteredNoise != nil {
		filteredKurt = filteredNoise.SpectralKurtosis
	}
	if finalNoise != nil {
		finalKurt = finalNoise.SpectralKurtosis
	}
	table.AddRow("Spectral Kurtosis",
		[]string{
			formatMetric(inputKurt, 3),
			formatMetricSpectral(filteredKurt, 3, filteredIsDigitalSilence),
			formatMetricSpectral(finalKurt, 3, finalIsDigitalSilence),
		}, "", "")

	// Spectral Entropy
	inputEntropy := math.NaN()
	filteredEntropy := math.NaN()
	finalEntropy := math.NaN()
	if inputNoise != nil {
		inputEntropy = inputNoise.SpectralEntropy
	} else {
		inputEntropy = noiseProfile.Entropy // Fall back to NoiseProfile
	}
	if filteredNoise != nil {
		filteredEntropy = filteredNoise.SpectralEntropy
	}
	if finalNoise != nil {
		finalEntropy = finalNoise.SpectralEntropy
	}
	table.AddRow("Spectral Entropy",
		[]string{
			formatMetric(inputEntropy, 6),
			formatMetricSpectral(filteredEntropy, 6, filteredIsDigitalSilence),
			formatMetricSpectral(finalEntropy, 6, finalIsDigitalSilence),
		}, "", "")

	// Spectral Flatness
	inputFlat := math.NaN()
	filteredFlat := math.NaN()
	finalFlat := math.NaN()
	if inputNoise != nil {
		inputFlat = inputNoise.SpectralFlatness
	}
	if filteredNoise != nil {
		filteredFlat = filteredNoise.SpectralFlatness
	}
	if finalNoise != nil {
		finalFlat = finalNoise.SpectralFlatness
	}
	table.AddRow("Spectral Flatness",
		[]string{
			formatMetric(inputFlat, 6),
			formatMetricSpectral(filteredFlat, 6, filteredIsDigitalSilence),
			formatMetricSpectral(finalFlat, 6, finalIsDigitalSilence),
		}, "", "")

	// Spectral Crest
	inputSpectralCrest := math.NaN()
	filteredSpectralCrest := math.NaN()
	finalSpectralCrest := math.NaN()
	if inputNoise != nil {
		inputSpectralCrest = inputNoise.SpectralCrest
	}
	if filteredNoise != nil {
		filteredSpectralCrest = filteredNoise.SpectralCrest
	}
	if finalNoise != nil {
		finalSpectralCrest = finalNoise.SpectralCrest
	}
	table.AddRow("Spectral Crest",
		[]string{
			formatMetric(inputSpectralCrest, 3),
			formatMetricSpectral(filteredSpectralCrest, 3, filteredIsDigitalSilence),
			formatMetricSpectral(finalSpectralCrest, 3, finalIsDigitalSilence),
		}, "", "")

	// Spectral Flux
	inputFlux := math.NaN()
	filteredFlux := math.NaN()
	finalFlux := math.NaN()
	if inputNoise != nil {
		inputFlux = inputNoise.SpectralFlux
	}
	if filteredNoise != nil {
		filteredFlux = filteredNoise.SpectralFlux
	}
	if finalNoise != nil {
		finalFlux = finalNoise.SpectralFlux
	}
	table.AddRow("Spectral Flux",
		[]string{
			formatMetric(inputFlux, 6),
			formatMetricSpectral(filteredFlux, 6, filteredIsDigitalSilence),
			formatMetricSpectral(finalFlux, 6, finalIsDigitalSilence),
		}, "", "")

	// Spectral Slope
	inputSlope := math.NaN()
	filteredSlope := math.NaN()
	finalSlope := math.NaN()
	if inputNoise != nil {
		inputSlope = inputNoise.SpectralSlope
	}
	if filteredNoise != nil {
		filteredSlope = filteredNoise.SpectralSlope
	}
	if finalNoise != nil {
		finalSlope = finalNoise.SpectralSlope
	}
	table.AddRow("Spectral Slope",
		[]string{
			formatMetric(inputSlope, 9),
			formatMetricSpectral(filteredSlope, 9, filteredIsDigitalSilence),
			formatMetricSpectral(finalSlope, 9, finalIsDigitalSilence),
		}, "", "")

	// Spectral Decrease
	inputDecrease := math.NaN()
	filteredDecrease := math.NaN()
	finalDecrease := math.NaN()
	if inputNoise != nil {
		inputDecrease = inputNoise.SpectralDecrease
	}
	if filteredNoise != nil {
		filteredDecrease = filteredNoise.SpectralDecrease
	}
	if finalNoise != nil {
		finalDecrease = finalNoise.SpectralDecrease
	}
	table.AddRow("Spectral Decrease",
		[]string{
			formatMetric(inputDecrease, 6),
			formatMetricSpectral(filteredDecrease, 6, filteredIsDigitalSilence),
			formatMetricSpectral(finalDecrease, 6, finalIsDigitalSilence),
		}, "", "")

	// Spectral Rolloff
	inputRolloff := math.NaN()
	filteredRolloff := math.NaN()
	finalRolloff := math.NaN()
	if inputNoise != nil {
		inputRolloff = inputNoise.SpectralRolloff
	}
	if filteredNoise != nil {
		filteredRolloff = filteredNoise.SpectralRolloff
	}
	if finalNoise != nil {
		finalRolloff = finalNoise.SpectralRolloff
	}
	table.AddRow("Spectral Rolloff",
		[]string{
			formatMetric(inputRolloff, 0),
			formatMetricSpectral(filteredRolloff, 0, filteredIsDigitalSilence),
			formatMetricSpectral(finalRolloff, 0, finalIsDigitalSilence),
		}, "Hz", "")

	// ========== LOUDNESS METRICS ==========

	// Momentary LUFS - use special formatting for values below measurement floor
	inputMomentary := math.NaN()
	filteredMomentary := math.NaN()
	finalMomentary := math.NaN()
	if inputNoise != nil {
		inputMomentary = inputNoise.MomentaryLUFS
	}
	if filteredNoise != nil {
		filteredMomentary = filteredNoise.MomentaryLUFS
	}
	if finalNoise != nil {
		finalMomentary = finalNoise.MomentaryLUFS
	}
	table.AddRow("Momentary LUFS",
		[]string{
			formatMetricLUFS(inputMomentary, 1),
			formatMetricLUFS(filteredMomentary, 1),
			formatMetricLUFS(finalMomentary, 1),
		},
		"LUFS", "")

	// Short-term LUFS
	inputShortTerm := math.NaN()
	filteredShortTerm := math.NaN()
	finalShortTerm := math.NaN()
	if inputNoise != nil {
		inputShortTerm = inputNoise.ShortTermLUFS
	}
	if filteredNoise != nil {
		filteredShortTerm = filteredNoise.ShortTermLUFS
	}
	if finalNoise != nil {
		finalShortTerm = finalNoise.ShortTermLUFS
	}
	table.AddRow("Short-term LUFS",
		[]string{
			formatMetricLUFS(inputShortTerm, 1),
			formatMetricLUFS(filteredShortTerm, 1),
			formatMetricLUFS(finalShortTerm, 1),
		},
		"LUFS", "")

	// True Peak - values are now stored in dB (converted during measurement)
	inputTP := math.NaN()
	filteredTP := math.NaN()
	finalTP := math.NaN()
	if inputNoise != nil {
		inputTP = inputNoise.TruePeak
	}
	if filteredNoise != nil {
		filteredTP = filteredNoise.TruePeak
	}
	if finalNoise != nil {
		finalTP = finalNoise.TruePeak
	}
	table.AddRow("True Peak",
		[]string{
			formatMetricDB(inputTP, 1),
			formatMetricDB(filteredTP, 1),
			formatMetricDB(finalTP, 1),
		},
		"dBTP", "")

	// Sample Peak - values are now stored in dB (converted during measurement)
	inputSP := math.NaN()
	filteredSP := math.NaN()
	finalSP := math.NaN()
	if inputNoise != nil {
		inputSP = inputNoise.SamplePeak
	}
	if filteredNoise != nil {
		filteredSP = filteredNoise.SamplePeak
	}
	if finalNoise != nil {
		finalSP = finalNoise.SamplePeak
	}
	table.AddRow("Sample Peak",
		[]string{
			formatMetricDB(inputSP, 1),
			formatMetricDB(filteredSP, 1),
			formatMetricDB(finalSP, 1),
		},
		"dBFS", "")

	// Character (interpretation row) - based on entropy
	// For digital silence, show "silent" instead of attempting to characterise non-existent noise
	getNoiseCharacter := func(entropy float64, isDigSilence bool) string {
		if isDigSilence {
			return "silent"
		}
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
	inputChar := getNoiseCharacter(inputEntropy, false) // Input is never digital silence (we have real noise)
	filteredChar := getNoiseCharacter(filteredEntropy, filteredIsDigitalSilence)
	finalChar := getNoiseCharacter(finalEntropy, finalIsDigitalSilence)
	table.AddRow("Character", []string{inputChar, filteredChar, finalChar}, "", "")

	fmt.Fprint(f, table.String())
	fmt.Fprintln(f, "")
}

// writeSpeechRegionTable outputs a three-column comparison table for speech region metrics.
// Columns: Input (Pass 1 speech profile), Filtered (Pass 2 SpeechSample), Final (Pass 4 SpeechSample)
func writeSpeechRegionTable(f *os.File, inputMeasurements *processor.AudioMeasurements, filteredMeasurements *processor.OutputMeasurements, finalMeasurements *processor.OutputMeasurements) {
	writeSection(f, "Speech Region Analysis")

	// Skip if no input measurements or speech profile
	if inputMeasurements == nil || inputMeasurements.SpeechProfile == nil {
		fmt.Fprintln(f, "No speech profile available")
		fmt.Fprintln(f, "")
		return
	}

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

	// Spectral Mean
	inputMean := math.NaN()
	filteredMean := math.NaN()
	finalMean := math.NaN()
	if inputSpeech != nil {
		inputMean = inputSpeech.SpectralMean
	}
	if filteredSpeech != nil {
		filteredMean = filteredSpeech.SpectralMean
	}
	if finalSpeech != nil {
		finalMean = finalSpeech.SpectralMean
	}
	table.AddMetricRow("Spectral Mean", inputMean, filteredMean, finalMean, 6, "", "")

	// Spectral Variance
	inputVar := math.NaN()
	filteredVar := math.NaN()
	finalVar := math.NaN()
	if inputSpeech != nil {
		inputVar = inputSpeech.SpectralVariance
	}
	if filteredSpeech != nil {
		filteredVar = filteredSpeech.SpectralVariance
	}
	if finalSpeech != nil {
		finalVar = finalSpeech.SpectralVariance
	}
	table.AddMetricRow("Spectral Variance", inputVar, filteredVar, finalVar, 6, "", "")

	// Spectral Centroid
	inputCentroid := math.NaN()
	filteredCentroid := math.NaN()
	finalCentroid := math.NaN()
	if inputSpeech != nil {
		inputCentroid = inputSpeech.SpectralCentroid
	}
	if filteredSpeech != nil {
		filteredCentroid = filteredSpeech.SpectralCentroid
	}
	if finalSpeech != nil {
		finalCentroid = finalSpeech.SpectralCentroid
	}
	table.AddMetricRow("Spectral Centroid", inputCentroid, filteredCentroid, finalCentroid, 0, "Hz", interpretCentroid(finalCentroid))

	// Spectral Spread
	inputSpread := math.NaN()
	filteredSpread := math.NaN()
	finalSpread := math.NaN()
	if inputSpeech != nil {
		inputSpread = inputSpeech.SpectralSpread
	}
	if filteredSpeech != nil {
		filteredSpread = filteredSpeech.SpectralSpread
	}
	if finalSpeech != nil {
		finalSpread = finalSpeech.SpectralSpread
	}
	table.AddMetricRow("Spectral Spread", inputSpread, filteredSpread, finalSpread, 0, "Hz", interpretSpread(finalSpread))

	// Spectral Skewness
	inputSkew := math.NaN()
	filteredSkew := math.NaN()
	finalSkew := math.NaN()
	if inputSpeech != nil {
		inputSkew = inputSpeech.SpectralSkewness
	}
	if filteredSpeech != nil {
		filteredSkew = filteredSpeech.SpectralSkewness
	}
	if finalSpeech != nil {
		finalSkew = finalSpeech.SpectralSkewness
	}
	table.AddMetricRow("Spectral Skewness", inputSkew, filteredSkew, finalSkew, 3, "", interpretSkewness(finalSkew))

	// Spectral Kurtosis
	inputKurt := math.NaN()
	filteredKurt := math.NaN()
	finalKurt := math.NaN()
	if inputSpeech != nil {
		inputKurt = inputSpeech.SpectralKurtosis
	}
	if filteredSpeech != nil {
		filteredKurt = filteredSpeech.SpectralKurtosis
	}
	if finalSpeech != nil {
		finalKurt = finalSpeech.SpectralKurtosis
	}
	table.AddMetricRow("Spectral Kurtosis", inputKurt, filteredKurt, finalKurt, 3, "", interpretKurtosis(finalKurt))

	// Spectral Entropy
	inputEntropy := math.NaN()
	filteredEntropy := math.NaN()
	finalEntropy := math.NaN()
	if inputSpeech != nil {
		inputEntropy = inputSpeech.SpectralEntropy
	}
	if filteredSpeech != nil {
		filteredEntropy = filteredSpeech.SpectralEntropy
	}
	if finalSpeech != nil {
		finalEntropy = finalSpeech.SpectralEntropy
	}
	table.AddMetricRow("Spectral Entropy", inputEntropy, filteredEntropy, finalEntropy, 6, "", interpretEntropy(finalEntropy))

	// Spectral Flatness
	inputFlat := math.NaN()
	filteredFlat := math.NaN()
	finalFlat := math.NaN()
	if inputSpeech != nil {
		inputFlat = inputSpeech.SpectralFlatness
	}
	if filteredSpeech != nil {
		filteredFlat = filteredSpeech.SpectralFlatness
	}
	if finalSpeech != nil {
		finalFlat = finalSpeech.SpectralFlatness
	}
	table.AddMetricRow("Spectral Flatness", inputFlat, filteredFlat, finalFlat, 6, "", interpretFlatness(finalFlat))

	// Spectral Crest
	inputSpectralCrest := math.NaN()
	filteredSpectralCrest := math.NaN()
	finalSpectralCrest := math.NaN()
	if inputSpeech != nil {
		inputSpectralCrest = inputSpeech.SpectralCrest
	}
	if filteredSpeech != nil {
		filteredSpectralCrest = filteredSpeech.SpectralCrest
	}
	if finalSpeech != nil {
		finalSpectralCrest = finalSpeech.SpectralCrest
	}
	table.AddMetricRow("Spectral Crest", inputSpectralCrest, filteredSpectralCrest, finalSpectralCrest, 3, "", interpretCrest(finalSpectralCrest))

	// Spectral Flux
	inputFlux := math.NaN()
	filteredFlux := math.NaN()
	finalFlux := math.NaN()
	if inputSpeech != nil {
		inputFlux = inputSpeech.SpectralFlux
	}
	if filteredSpeech != nil {
		filteredFlux = filteredSpeech.SpectralFlux
	}
	if finalSpeech != nil {
		finalFlux = finalSpeech.SpectralFlux
	}
	table.AddMetricRow("Spectral Flux", inputFlux, filteredFlux, finalFlux, 6, "", interpretFlux(finalFlux))

	// Spectral Slope
	inputSlope := math.NaN()
	filteredSlope := math.NaN()
	finalSlope := math.NaN()
	if inputSpeech != nil {
		inputSlope = inputSpeech.SpectralSlope
	}
	if filteredSpeech != nil {
		filteredSlope = filteredSpeech.SpectralSlope
	}
	if finalSpeech != nil {
		finalSlope = finalSpeech.SpectralSlope
	}
	table.AddMetricRow("Spectral Slope", inputSlope, filteredSlope, finalSlope, 9, "", interpretSlope(finalSlope))

	// Spectral Decrease
	inputDecrease := math.NaN()
	filteredDecrease := math.NaN()
	finalDecrease := math.NaN()
	if inputSpeech != nil {
		inputDecrease = inputSpeech.SpectralDecrease
	}
	if filteredSpeech != nil {
		filteredDecrease = filteredSpeech.SpectralDecrease
	}
	if finalSpeech != nil {
		finalDecrease = finalSpeech.SpectralDecrease
	}
	table.AddMetricRow("Spectral Decrease", inputDecrease, filteredDecrease, finalDecrease, 6, "", interpretDecrease(finalDecrease))

	// Spectral Rolloff
	inputRolloff := math.NaN()
	filteredRolloff := math.NaN()
	finalRolloff := math.NaN()
	if inputSpeech != nil {
		inputRolloff = inputSpeech.SpectralRolloff
	}
	if filteredSpeech != nil {
		filteredRolloff = filteredSpeech.SpectralRolloff
	}
	if finalSpeech != nil {
		finalRolloff = finalSpeech.SpectralRolloff
	}
	table.AddMetricRow("Spectral Rolloff", inputRolloff, filteredRolloff, finalRolloff, 0, "Hz", interpretRolloff(finalRolloff))

	// ========== LOUDNESS METRICS ==========

	// Momentary LUFS
	inputMomentary := math.NaN()
	filteredMomentary := math.NaN()
	finalMomentary := math.NaN()
	if inputSpeech != nil {
		inputMomentary = inputSpeech.MomentaryLUFS
	}
	if filteredSpeech != nil {
		filteredMomentary = filteredSpeech.MomentaryLUFS
	}
	if finalSpeech != nil {
		finalMomentary = finalSpeech.MomentaryLUFS
	}
	table.AddMetricRow("Momentary LUFS", inputMomentary, filteredMomentary, finalMomentary, 1, "LUFS", "")

	// Short-term LUFS
	inputShortTerm := math.NaN()
	filteredShortTerm := math.NaN()
	finalShortTerm := math.NaN()
	if inputSpeech != nil {
		inputShortTerm = inputSpeech.ShortTermLUFS
	}
	if filteredSpeech != nil {
		filteredShortTerm = filteredSpeech.ShortTermLUFS
	}
	if finalSpeech != nil {
		finalShortTerm = finalSpeech.ShortTermLUFS
	}
	table.AddMetricRow("Short-term LUFS", inputShortTerm, filteredShortTerm, finalShortTerm, 1, "LUFS", "")

	// True Peak
	inputTP := math.NaN()
	filteredTP := math.NaN()
	finalTP := math.NaN()
	if inputSpeech != nil {
		inputTP = inputSpeech.TruePeak
	}
	if filteredSpeech != nil {
		filteredTP = filteredSpeech.TruePeak
	}
	if finalSpeech != nil {
		finalTP = finalSpeech.TruePeak
	}
	table.AddMetricRow("True Peak", inputTP, filteredTP, finalTP, 1, "dBTP", "")

	// Sample Peak
	inputSP := math.NaN()
	filteredSP := math.NaN()
	finalSP := math.NaN()
	if inputSpeech != nil {
		inputSP = inputSpeech.SamplePeak
	}
	if filteredSpeech != nil {
		filteredSP = filteredSpeech.SamplePeak
	}
	if finalSpeech != nil {
		finalSP = finalSpeech.SamplePeak
	}
	table.AddMetricRow("Sample Peak", inputSP, filteredSP, finalSP, 1, "dBFS", "")

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
		}
		return "very bright"
	}
	inputSpeechChar := getSpeechCharacter(inputCentroid, inputEntropy)
	filteredSpeechChar := getSpeechCharacter(filteredCentroid, filteredEntropy)
	finalSpeechChar := getSpeechCharacter(finalCentroid, finalEntropy)
	table.AddRow("Character", []string{inputSpeechChar, filteredSpeechChar, finalSpeechChar}, "", "")

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
			isSelected := measurements.SpeechProfile != nil &&
				c.Region.Start == measurements.SpeechProfile.Region.Start

			if isSelected {
				fmt.Fprintf(f, "  Candidate %d:       %.1fs at %.1fs (score: %.3f) [SELECTED]\n",
					i+1, c.Region.Duration.Seconds(), c.Region.Start.Seconds(), c.Score)
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
func getFilteredNoise(result *processor.ProcessingResult) *processor.SilenceCandidateMetrics {
	if result == nil || result.FilteredMeasurements == nil {
		return nil
	}
	return result.FilteredMeasurements.SilenceSample
}

// getFinalNoise safely extracts final noise profile from the result.
func getFinalNoise(result *processor.ProcessingResult) *processor.SilenceCandidateMetrics {
	if result == nil || result.NormResult == nil || result.NormResult.FinalMeasurements == nil {
		return nil
	}
	return result.NormResult.FinalMeasurements.SilenceSample
}
