// Package logging handles filter-chain report formatting.

package logging

import (
	"fmt"
	"os"
	"strings"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// writeFilterChainApplied outputs the filter chain section.
func writeFilterChainApplied(f *os.File, config *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics, measurements *processor.AudioMeasurements) {
	formatFilterChain(f, config, diagnostics, measurements)
	fmt.Fprintln(f, "")
}

// formatFilterChain generates the filter chain section of the report.
// Iterates over filters in chain order, showing enabled/disabled status,
// key parameters, and adaptive rationale for each filter.
func formatFilterChain(f *os.File, cfg *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics, m *processor.AudioMeasurements) {
	fmt.Fprintln(f, "Filter Chain (in processing order)")
	fmt.Fprintln(f, "------------------------------------")

	for i, filterID := range cfg.FilterOrder {
		prefix := fmt.Sprintf("%2d. ", i+1)
		formatFilter(f, filterID, cfg, diagnostics, m, prefix)
	}
}

// formatFilter outputs details for a single filter
func formatFilter(f *os.File, filterID processor.FilterID, cfg *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics, m *processor.AudioMeasurements, prefix string) {
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
		formatDS201LowPassFilter(f, cfg, diagnostics, m, prefix)
	case processor.FilterNoiseRemove:
		formatNoiseRemoveFilter(f, cfg, m, prefix)
	case processor.FilterDS201Gate:
		formatDS201GateFilter(f, cfg, diagnostics, m, prefix)
	case processor.FilterLA2ACompressor:
		formatLA2ACompressorFilter(f, cfg, diagnostics, m, prefix)
	case processor.FilterDeesser:
		formatDeesserFilter(f, cfg, m, prefix)
	default:
		fmt.Fprintf(f, "%s%s: (unknown filter)\n", prefix, filterID)
	}
}

// formatDS201HighpassFilter outputs DS201-inspired highpass filter details
func formatDS201HighpassFilter(f *os.File, cfg *processor.EffectiveFilterConfig, m *processor.AudioMeasurements, prefix string) {
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
func formatDS201LowPassFilter(f *os.File, cfg *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DS201LPEnabled {
		// Show reason for being disabled (pass-through mode)
		if diagnostics != nil && diagnostics.DS201LPReason != "" {
			fmt.Fprintf(f, "%sDS201 lowpass: DISABLED (%s)\n", prefix, diagnostics.DS201LPReason)
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
	if diagnostics != nil && diagnostics.DS201LPReason != "" {
		fmt.Fprintf(f, "        Rationale: %s\n", diagnostics.DS201LPReason)
	}

	// Show content type detection metrics
	if m != nil {
		contentType := processor.ContentType(-1)
		if diagnostics != nil {
			contentType = diagnostics.DS201LPContentType
		}
		fmt.Fprintf(f, "        Content type: %s (kurtosis %.1f, flatness %.3f, flux %.4f)\n",
			contentType.String(), m.SpectralKurtosis, m.SpectralFlatness, m.SpectralFlux)

		// Show the triggering metric details
		lpReason := ""
		rolloffRatio := 0.0
		if diagnostics != nil {
			lpReason = diagnostics.DS201LPReason
			rolloffRatio = diagnostics.DS201LPRolloffRatio
		}
		switch lpReason {
		case "rolloff/centroid gap":
			fmt.Fprintf(f, "        Rolloff/centroid ratio: %.2f > 2.5 (rolloff %.0f Hz, centroid %.0f Hz)\n",
				rolloffRatio, m.SpectralRolloff, m.SpectralCentroid)
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
func formatNoiseRemoveFilter(f *os.File, cfg *processor.EffectiveFilterConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.NoiseRemoveEnabled {
		fmt.Fprintf(f, "%snoiseremove: DISABLED\n", prefix)
		return
	}

	// Header: filter name and algorithm
	fmt.Fprintf(f, "%snoiseremove: anlmdn + compand (Non-Local Means denoiser)\n", prefix)

	// anlmdn parameters (matrix spike defaults: r_min + m_strict at source rate)
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
func formatDS201GateFilter(f *os.File, cfg *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics, m *processor.AudioMeasurements, prefix string) {
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
	if diagnostics != nil && diagnostics.DS201GateGentleMode {
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

		switch {
		case usePeakRef:
			rationale = append(rationale, fmt.Sprintf("peak ref %.1f dB (crest %.1f dB)", m.NoiseProfile.PeakLevel, m.NoiseProfile.CrestFactor))
		case lufsGap >= 25 && m.NoiseProfile != nil && m.NoiseProfile.CrestFactor > 20:
			rationale = append(rationale, fmt.Sprintf("noise floor %.1f dB (extreme LUFS gap %.0f dB, ignoring crest)", m.NoiseFloor, lufsGap))
		default:
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
			switch {
			case entropy < 0.10:
				rationale = append(rationale, fmt.Sprintf("very tonal (entropy %.2f, slow release)", entropy))
			case entropy < 0.12:
				rationale = append(rationale, fmt.Sprintf("tonal (entropy %.2f)", entropy))
			case entropy < 0.16:
				rationale = append(rationale, fmt.Sprintf("mixed (entropy %.2f, faster release)", entropy))
			default:
				rationale = append(rationale, fmt.Sprintf("broadband-ish (entropy %.2f, fast release)", entropy))
			}
		}

		// Gentle mode rationale - for extreme LUFS gap + low LRA recordings
		if diagnostics != nil && diagnostics.DS201GateGentleMode {
			rationale = append(rationale, "gentle mode (extreme LUFS gap + low LRA)")
		}

		if len(rationale) > 0 {
			fmt.Fprintf(f, "        Rationale: %s\n", strings.Join(rationale, ", "))
		}

		// Show aggression-based threshold calculation
		if diagnostics != nil && diagnostics.DS201GateAggression > 0 {
			fmt.Fprintf(f, "        Aggression: %.2f (separation %.1f dB)\n",
				diagnostics.DS201GateAggression, diagnostics.DS201GateSpeechSeparation)
			fmt.Fprintf(f, "        Quiet speech: %.1f dB, Dynamic range: %.1f dB\n",
				diagnostics.DS201GateQuietSpeechEstimate, diagnostics.DS201GateDynamicRange)
			if diagnostics.DS201GateClampReason != "none" {
				fmt.Fprintf(f, "        Clamped by: %s (unclamped: %.1f dB)\n",
					diagnostics.DS201GateClampReason, diagnostics.DS201GateThresholdUnclamped)
			}
			fmt.Fprintf(f, "        Headroom above quiet speech: %.1f dB\n",
				-diagnostics.DS201GateSpeechHeadroom) // Negative because threshold is above quiet speech
		}
	}
}

// formatLA2ACompressorFilter outputs LA-2A Compressor filter details
func formatLA2ACompressorFilter(f *os.File, cfg *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics, m *processor.AudioMeasurements, prefix string) {
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
			if m.SpeechProfile.Spectral.Kurtosis > 0 {
				kurtosis = m.SpeechProfile.Spectral.Kurtosis
				kurtosisSource = "speech region"
			}
			if m.SpeechProfile.Spectral.Flux > 0 {
				flux = m.SpeechProfile.Spectral.Flux
				fluxSource = "speech region"
			}
		}
		fmt.Fprintf(f, "        spectral kurtosis: %.1f (%s)\n", kurtosis, kurtosisSource)
		fmt.Fprintf(f, "        spectral flux: %.4f (%s)\n", flux, fluxSource)
	}

	// High-crest override diagnostics
	if diagnostics != nil && diagnostics.LA2AHighCrestActive && m != nil {
		fmt.Fprintf(f, "        High-crest override: ACTIVE (deficit %.1f dB, severity %.2f)\n",
			diagnostics.LA2AHighCrestDeficit, diagnostics.LA2AHighCrestSeverity)
		gainRequired := processor.NormTargetLUFS - m.InputI
		fmt.Fprintf(f, "        Projected TP: %.1f dBTP (gain %.1f dB applied to %.1f dBTP peaks)\n",
			diagnostics.LA2AHighCrestProjectedTP, gainRequired, m.InputTP)
		idealCeiling := cfg.LoudnormTargetTP - gainRequired - 1.5
		fmt.Fprintf(f, "        Ideal ceiling: %.1f dBTP, alimiter minimum: -24.0 dBTP\n", idealCeiling)
		fmt.Fprintf(f, "        Override targets: threshold <= %.0f dB, ratio >= %.1f:1\n",
			cfg.LA2AThreshold, cfg.LA2ARatio)
	} else {
		highCrestDeficit := 0.0
		if diagnostics != nil {
			highCrestDeficit = diagnostics.LA2AHighCrestDeficit
		}
		fmt.Fprintf(f, "        High-crest override: not needed (deficit %.1f dB)\n",
			highCrestDeficit)
	}
}

// formatDeesserFilter outputs deesser filter details
func formatDeesserFilter(f *os.File, cfg *processor.EffectiveFilterConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DeessEnabled {
		fmt.Fprintf(f, "%sdeesser: DISABLED\n", prefix)
		return
	}
	if cfg.DeessIntensity == 0 {
		if m == nil || m.SpeechProfile == nil {
			fmt.Fprintf(f, "%sdeesser: inactive: no speech profile (full-file metrics unreliable)\n", prefix)
		} else {
			fmt.Fprintf(f, "%sdeesser: inactive: no sibilance detected\n", prefix)
		}
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
			if m.SpeechProfile.Spectral.Centroid > 0 {
				centroid = m.SpeechProfile.Spectral.Centroid
				centroidSource = "speech region"
			}
			if m.SpeechProfile.Spectral.Rolloff > 0 {
				rolloff = m.SpeechProfile.Spectral.Rolloff
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

// formatDownmixFilter outputs downmix filter details
func formatDownmixFilter(f *os.File, cfg *processor.EffectiveFilterConfig, prefix string) {
	if !cfg.DownmixEnabled {
		fmt.Fprintf(f, "%sdownmix: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sdownmix: stereo → mono (FFmpeg builtin)\n", prefix)
}

// formatAnalysisFilter outputs analysis filter details
func formatAnalysisFilter(f *os.File, cfg *processor.EffectiveFilterConfig, prefix string) {
	if !cfg.AnalysisEnabled {
		fmt.Fprintf(f, "%sanalysis: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sanalysis: collect audio measurements (ebur128 + astats + aspectralstats)\n", prefix)
}

// formatResampleFilter outputs resample filter details
func formatResampleFilter(f *os.File, cfg *processor.EffectiveFilterConfig, prefix string) {
	if !cfg.ResampleEnabled {
		fmt.Fprintf(f, "%sresample: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sresample: %d Hz %s mono, %d samples/frame\n",
		prefix, cfg.ResampleSampleRate, cfg.ResampleFormat, cfg.ResampleFrameSize)
}
