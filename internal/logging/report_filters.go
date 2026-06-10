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
		formatDS201HighpassFilter(f, cfg, prefix)
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
func formatDS201HighpassFilter(f *os.File, cfg *processor.EffectiveFilterConfig, prefix string) {
	highpass := cfg.DS201HighPass
	if !highpass.Enabled {
		fmt.Fprintf(f, "%sDS201 highpass: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sDS201 highpass: %.0f Hz cutoff (12dB/oct Butterworth, tdii) — fixed corner below the vocal fundamental, removes subsonic rumble\n", prefix, highpass.Frequency)
}

// formatDS201LowPassFilter outputs DS201-inspired low-pass filter details
func formatDS201LowPassFilter(f *os.File, cfg *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics, _ *processor.AudioMeasurements, prefix string) {
	lowpass := cfg.DS201LowPass
	if !lowpass.Enabled {
		fmt.Fprintf(f, "%sDS201 lowpass: DISABLED\n", prefix)
		return
	}

	// Fixed 20.5 kHz / 12dB/oct Butterworth band-limit (always on, no adaptation).
	header := fmt.Sprintf("%sDS201 lowpass: %.0f Hz cutoff (12dB/oct Butterworth", prefix, lowpass.Frequency)
	if lowpass.Transform != "" {
		header += ", " + lowpass.Transform
	}
	header += ")"
	fmt.Fprintln(f, header)

	if diagnostics != nil && diagnostics.DS201LPReason != "" {
		fmt.Fprintf(f, "        Rationale: %s\n", diagnostics.DS201LPReason)
	}
}

// formatNoiseRemoveFilter outputs NoiseRemove (anlmdn + afftdn) filter details
// Uses Non-Local Means denoiser followed by an FFT spectral denoiser
func formatNoiseRemoveFilter(f *os.File, cfg *processor.EffectiveFilterConfig, m *processor.AudioMeasurements, prefix string) {
	noiseRemove := cfg.NoiseRemove
	if !noiseRemove.Enabled {
		fmt.Fprintf(f, "%snoiseremove: DISABLED\n", prefix)
		return
	}

	// Header: filter name and algorithm
	fmt.Fprintf(f, "%snoiseremove: anlmdn + afftdn (Non-Local Means + FFT spectral denoiser)\n", prefix)

	// anlmdn parameters (matrix spike defaults: r_min + m_strict at source rate)
	fmt.Fprintf(f, "        anlmdn: s=%.5f, p=%.4fs, r=%.4fs, m=%.0f\n",
		noiseRemove.Strength,
		noiseRemove.PatchSec,
		noiseRemove.ResearchSec,
		noiseRemove.Smooth)

	// Noise floor context from the elected room tone, when available.
	if m != nil && m.NoiseProfile != nil && m.NoiseProfile.MeasuredNoiseFloor < 0 {
		fmt.Fprintf(f, "        noise floor: %.1f dBFS (from room tone)\n",
			m.NoiseProfile.MeasuredNoiseFloor)
	}

	// afftdn parameters - fixed nr (not adaptive)
	if noiseRemove.AfftdnEnabled {
		fmt.Fprintf(f, "        afftdn: nr=%g dB (fixed), nt=%s, tn=%t\n",
			noiseRemove.AfftdnNoiseReduction,
			noiseRemove.AfftdnNoiseType,
			noiseRemove.AfftdnTrackNoise)
	}
}

// formatDS201GateFilter outputs DS201-inspired gate filter details
func formatDS201GateFilter(f *os.File, cfg *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics, m *processor.AudioMeasurements, prefix string) {
	gate := cfg.DS201Gate
	if !gate.Enabled {
		fmt.Fprintf(f, "%sDS201 gate: DISABLED\n", prefix)
		return
	}

	thresholdDB := processor.LinearToDb(gate.Threshold)
	rangeDB := processor.LinearToDb(gate.Range)

	detection := gate.Detection
	if detection == "" {
		detection = "rms"
	}

	// Show mode indicator if gentle mode is active
	modeNote := ""
	if diagnostics != nil && diagnostics.DS201GateGentleMode {
		modeNote = " [gentle mode]"
	}

	fmt.Fprintf(f, "%sDS201 gate: threshold %.1f dB, ratio %.1f:1, detection %s (fixed)%s\n", prefix, thresholdDB, gate.Ratio, detection, modeNote)
	fmt.Fprintf(f, "        Timing: attack %.2fms (fixed), release %.0fms (soft expander)\n", gate.Attack, gate.Release)
	fmt.Fprintf(f, "        Range: %.1f dB reduction, knee %.1f (fixed)\n", rangeDB, gate.Knee)

	// Show rationale based on measurements
	if m != nil {
		var rationale []string

		// Threshold rationale - must match logic in calculateDS201GateThreshold.
		// Peak reference is used by the low-separation guard when:
		// crest > 20 AND peak != 0 AND lufsGap < 25.
		lufsGap := cfg.Loudnorm.TargetI - m.InputI
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

		// Range rationale: driven by the noise floor (clean recordings go deeper).
		if m.NoiseFloor < -70 {
			rationale = append(rationale, fmt.Sprintf("clean floor %.1f dB (deeper range)", m.NoiseFloor))
		} else {
			rationale = append(rationale, fmt.Sprintf("floor %.1f dB (standard range)", m.NoiseFloor))
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
func formatLA2ACompressorFilter(f *os.File, cfg *processor.EffectiveFilterConfig, _ *processor.AdaptiveDiagnostics, m *processor.AudioMeasurements, prefix string) {
	la2a := cfg.LA2A
	if !la2a.Enabled {
		fmt.Fprintf(f, "%sLA-2A Compressor: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sLA-2A Compressor: threshold %.1f dB, ratio %.1f:1\n", prefix, la2a.Threshold, la2a.Ratio)
	fmt.Fprintf(f, "        Timing: attack %.0fms, release %.0fms (fixed)\n", la2a.Attack, la2a.Release)
	fmt.Fprintf(f, "        Mix: %.0f%%, knee %.1f, makeup %.0f dB (fixed)\n", la2a.Mix*100, la2a.Knee, la2a.Makeup)

	// Only the threshold adapts. Show its source.
	if m != nil && m.SpeechProfile != nil {
		fmt.Fprintf(f, "        Threshold: speech RMS %.1f dBFS + %.0f dB offset\n",
			m.SpeechProfile.RMSLevel, processor.LA2AThresholdSpeechOffsetDB)
	} else if m != nil {
		fmt.Fprintf(f, "        Threshold: peak %.1f dBFS - 20 dB (no speech profile)\n", m.PeakLevel)
	}
}

// formatDeesserFilter outputs deesser filter details
func formatDeesserFilter(f *os.File, cfg *processor.EffectiveFilterConfig, m *processor.AudioMeasurements, prefix string) {
	deesser := cfg.Deesser
	if !deesser.Enabled {
		fmt.Fprintf(f, "%sdeesser: DISABLED\n", prefix)
		return
	}
	if deesser.Intensity == 0 {
		switch {
		case m == nil || m.SpeechProfile == nil:
			fmt.Fprintf(f, "%sdeesser: inactive: no speech profile (full-file metrics unreliable)\n", prefix)
		case !m.SpeechProfile.BandsMeasured:
			fmt.Fprintf(f, "%sdeesser: OFF: speech-band RMS not measured (region too short for astats)\n", prefix)
		default:
			excess := m.SpeechProfile.SibBandRMS - m.SpeechProfile.BodyBandRMS
			fmt.Fprintf(f, "%sdeesser: OFF: sibilance excess %.1f dB (sib %.1f - body %.1f dBFS)\n",
				prefix, excess, m.SpeechProfile.SibBandRMS, m.SpeechProfile.BodyBandRMS)
		}
		return
	}

	fmt.Fprintf(f, "%sdeesser: intensity %.0f%%, amount %.0f%%, freq %.0f%%\n",
		prefix, deesser.Intensity*100, deesser.Amount*100, deesser.Frequency*100)

	// Show rationale: the band-excess engagement signal from the speech region.
	if m != nil && m.SpeechProfile != nil {
		excess := m.SpeechProfile.SibBandRMS - m.SpeechProfile.BodyBandRMS
		fmt.Fprintf(f, "        Rationale: sibilance excess %.1f dB (speech region)\n", excess)
		fmt.Fprintf(f, "        sibilant band (6-9 kHz): %.1f dBFS\n", m.SpeechProfile.SibBandRMS)
		fmt.Fprintf(f, "        body band (1-3 kHz): %.1f dBFS\n", m.SpeechProfile.BodyBandRMS)
	}
}

// formatDownmixFilter outputs downmix filter details
func formatDownmixFilter(f *os.File, cfg *processor.EffectiveFilterConfig, prefix string) {
	if !cfg.Downmix.Enabled {
		fmt.Fprintf(f, "%sdownmix: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sdownmix: stereo → mono (FFmpeg builtin)\n", prefix)
}

// formatAnalysisFilter outputs analysis filter details
func formatAnalysisFilter(f *os.File, cfg *processor.EffectiveFilterConfig, prefix string) {
	if !cfg.Analysis.Enabled {
		fmt.Fprintf(f, "%sanalysis: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sanalysis: collect audio measurements (ebur128 + astats + aspectralstats)\n", prefix)
}

// formatResampleFilter outputs resample filter details
func formatResampleFilter(f *os.File, cfg *processor.EffectiveFilterConfig, prefix string) {
	resample := cfg.Resample
	if !resample.Enabled {
		fmt.Fprintf(f, "%sresample: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sresample: %d Hz %s mono, %d samples/frame\n",
		prefix, resample.SampleRate, resample.Format, resample.FrameSize)
}
