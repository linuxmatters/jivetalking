package logging

import (
	"fmt"
	"sort"
	"strings"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// RecordingTip represents a single piece of actionable recording advice
// derived from audio analysis measurements.
type RecordingTip struct {
	Priority int    // Higher = more important (1-10)
	Message  string // Human-readable advice (1-2 sentences)
	RuleID   string // Identifier for testing/logging (e.g., "level_too_quiet")
}

// MaxRecordingTips is the maximum number of tips to return.
const MaxRecordingTips = 5

// GenerateRecordingTips analyses audio measurements and returns prioritised
// recording improvement suggestions.
func GenerateRecordingTips(m *processor.AudioMeasurements, config *processor.FilterChainConfig) []RecordingTip {
	if m == nil {
		return nil
	}

	var tips []RecordingTip
	firedRules := make(map[string]bool)

	rules := []func(*processor.AudioMeasurements, *processor.FilterChainConfig) *RecordingTip{
		tipLevelTooHot,
		tipLevelTooQuiet,
		tipLevelQuiet,
		tipBackgroundNoise,
		tipMainsHum,
		tipTooFarFromMic,
		tipProximityEffect,
		tipSibilance,
		tipDynamicRange,
		tipOverCompressed,
		tipPoorSNR,
	}

	for _, rule := range rules {
		if tip := rule(m, config); tip != nil {
			tips = append(tips, *tip)
			firedRules[tip.RuleID] = true
		}
	}

	// Apply mutual exclusion
	tips = applyExclusions(tips, firedRules)

	// Sort by priority (descending)
	sort.Slice(tips, func(i, j int) bool {
		return tips[i].Priority > tips[j].Priority
	})

	// Cap at maximum
	if len(tips) > MaxRecordingTips {
		tips = tips[:MaxRecordingTips]
	}

	return tips
}

// applyExclusions removes tips that are redundant when a more specific tip
// has already fired. For example, "level_quiet" is suppressed when
// "too_far_from_mic" fires because the latter already implies the former.
func applyExclusions(tips []RecordingTip, fired map[string]bool) []RecordingTip {
	var result []RecordingTip
	for _, tip := range tips {
		switch tip.RuleID {
		case "level_too_quiet", "level_quiet":
			if fired["level_clipping"] || fired["level_near_clipping"] || fired["too_far_from_mic"] {
				continue
			}
		case "poor_snr":
			if fired["too_far_from_mic"] {
				continue
			}
		}
		result = append(result, tip)
	}
	return result
}

// wrapText wraps text at word boundaries to fit within maxWidth columns.
// Continuation lines are prefixed with indent.
func wrapText(text string, maxWidth int, indent string) string {
	words := strings.Fields(text)
	var lines []string
	currentLine := ""

	for _, word := range words {
		if currentLine == "" {
			currentLine = word
		} else if len(currentLine)+1+len(word) <= maxWidth {
			currentLine += " " + word
		} else {
			lines = append(lines, currentLine)
			currentLine = word
		}
	}
	if currentLine != "" {
		lines = append(lines, currentLine)
	}

	return strings.Join(lines, "\n"+indent)
}

// tipLevelTooQuiet fires when recording level is very quiet.
// Uses SpeechProfile.RMSLevel when available (speech RMS < -42 dBFS),
// falling back to InputI < -30 LUFS when no speech profile exists.
// Gain target is -24 dBFS for speech RMS, -18 LUFS for InputI fallback.
func tipLevelTooQuiet(m *processor.AudioMeasurements, _ *processor.FilterChainConfig) *RecordingTip {
	if m.SpeechProfile != nil {
		speechRMS := m.SpeechProfile.RMSLevel
		if speechRMS >= -42.0 {
			return nil
		}
		gainNeeded := -24.0 - speechRMS
		return &RecordingTip{
			Priority: 10,
			RuleID:   "level_too_quiet",
			Message:  fmt.Sprintf("Your microphone gain is too low - try increasing it by about %.0f dB.", gainNeeded),
		}
	}
	// Fallback: no speech profile, use integrated LUFS
	if m.InputI >= -30.0 {
		return nil
	}
	gainNeeded := -18.0 - m.InputI
	return &RecordingTip{
		Priority: 10,
		RuleID:   "level_too_quiet",
		Message:  fmt.Sprintf("Your microphone gain is too low - try increasing it by about %.0f dB.", gainNeeded),
	}
}

// tipLevelQuiet fires when recording level is moderately quiet.
// Uses SpeechProfile.RMSLevel when available (speech RMS between -42 and -36 dBFS),
// falling back to InputI between -30 and -24 LUFS when no speech profile exists.
// Gain target is -24 dBFS for speech RMS, -18 LUFS for InputI fallback.
func tipLevelQuiet(m *processor.AudioMeasurements, _ *processor.FilterChainConfig) *RecordingTip {
	if m.SpeechProfile != nil {
		speechRMS := m.SpeechProfile.RMSLevel
		if speechRMS < -42.0 || speechRMS >= -36.0 {
			return nil
		}
		gainNeeded := -24.0 - speechRMS
		return &RecordingTip{
			Priority: 8,
			RuleID:   "level_quiet",
			Message:  fmt.Sprintf("Your recording is a bit quiet - increasing your microphone gain by about %.0f dB would improve quality.", gainNeeded),
		}
	}
	// Fallback: no speech profile, use integrated LUFS
	if m.InputI < -30.0 || m.InputI >= -24.0 {
		return nil
	}
	gainNeeded := -18.0 - m.InputI
	return &RecordingTip{
		Priority: 8,
		RuleID:   "level_quiet",
		Message:  fmt.Sprintf("Your recording is a bit quiet - increasing your microphone gain by about %.0f dB would improve quality.", gainNeeded),
	}
}

// tipLevelTooHot fires when true peak approaches or exceeds 0 dBTP.
// InputTP > 0.0 means actual clipping; > -1.0 means dangerously close.
func tipLevelTooHot(m *processor.AudioMeasurements, _ *processor.FilterChainConfig) *RecordingTip {
	if m.InputTP <= -1.0 {
		return nil
	}
	if m.InputTP > 0.0 {
		return &RecordingTip{
			Priority: 10,
			RuleID:   "level_clipping",
			Message:  "Your recording is clipping - turn your microphone gain down by 6-10 dB to prevent distortion.",
		}
	}
	return &RecordingTip{
		Priority: 9,
		RuleID:   "level_near_clipping",
		Message:  "Your recording is very close to clipping - turn your microphone gain down by 3-6 dB to give yourself some headroom.",
	}
}

// tipBackgroundNoise fires when the noise floor is elevated.
// Uses NoiseProfile.MeasuredNoiseFloor when available, falling back to AstatsNoiseFloor.
// Thresholds align with adaptive.go: -45 dBFS (la2aNoiseFloorNoisy), -55 dBFS (midpoint).
func tipBackgroundNoise(m *processor.AudioMeasurements, _ *processor.FilterChainConfig) *RecordingTip {
	noiseFloor := m.AstatsNoiseFloor
	if m.NoiseProfile != nil {
		noiseFloor = m.NoiseProfile.MeasuredNoiseFloor
	}

	if noiseFloor > -45.0 {
		return &RecordingTip{
			Priority: 9,
			RuleID:   "background_noise_high",
			Message:  fmt.Sprintf("Background noise is high (%.0f dBFS) - try turning off fans, air conditioning, or other appliances before recording.", noiseFloor),
		}
	}
	if noiseFloor > -55.0 {
		return &RecordingTip{
			Priority: 6,
			RuleID:   "background_noise_moderate",
			Message:  fmt.Sprintf("Background noise is slightly elevated (%.0f dBFS) - if possible, turn off any fans or appliances nearby.", noiseFloor),
		}
	}
	return nil
}

// tipMainsHum fires when silence regions show tonal noise characteristics.
// Requires NoiseProfile with low entropy (< 0.30, matching silenceEntropyTonal in adaptive.go),
// low flatness (< 0.3, confirming tonal character), and audible noise (> -65 dBFS).
func tipMainsHum(m *processor.AudioMeasurements, _ *processor.FilterChainConfig) *RecordingTip {
	if m.NoiseProfile == nil {
		return nil
	}
	np := m.NoiseProfile
	if np.Entropy >= 0.30 || np.SpectralFlatness >= 0.3 || np.MeasuredNoiseFloor < -65.0 {
		return nil
	}
	return &RecordingTip{
		Priority: 7,
		RuleID:   "mains_hum",
		Message:  "There's a constant low-frequency hum in your recording - check for nearby power supplies, monitors, or chargers and move them further from your microphone.",
	}
}

// tipTooFarFromMic fires when speech level is low and SNR is poor,
// indicating the speaker is too far from the microphone.
// Requires both SpeechProfile and NoiseProfile to be present.
// Thresholds: NoiseReductionHeadroom < 15 dB (below minSNRMargin of 20 dB in analyzer.go)
// AND SpeechProfile.RMSLevel < -30 dBFS.
func tipTooFarFromMic(m *processor.AudioMeasurements, _ *processor.FilterChainConfig) *RecordingTip {
	if m.SpeechProfile == nil || m.NoiseProfile == nil {
		return nil
	}
	if m.NoiseReductionHeadroom >= 15.0 || m.SpeechProfile.RMSLevel >= -30.0 {
		return nil
	}
	return &RecordingTip{
		Priority: 8,
		RuleID:   "too_far_from_mic",
		Message:  "You sound quite far from your microphone. Try moving closer - about a hand's width (15-20cm) from the mic is ideal for most setups.",
	}
}

// tipProximityEffect fires when spectral analysis indicates bass boost
// from being too close to a directional microphone.
// Thresholds from adaptive.go: spectralDecreaseVeryWarm = -0.10,
// spectralDecreaseWarm = -0.05. Skewness > 2.5 is tip-specific (stricter
// than adaptive.go's spectralSkewnessLFEmphasis = 1.0).
func tipProximityEffect(m *processor.AudioMeasurements, _ *processor.FilterChainConfig) *RecordingTip {
	decrease := m.SpectralDecrease
	skewness := m.SpectralSkewness
	if m.SpeechProfile != nil {
		decrease = m.SpeechProfile.SpectralDecrease
		skewness = m.SpeechProfile.SpectralSkewness
	}

	veryWarm := decrease < -0.10
	warmWithSkew := decrease < -0.05 && skewness > 2.5

	if !veryWarm && !warmWithSkew {
		return nil
	}
	return &RecordingTip{
		Priority: 5,
		RuleID:   "proximity_effect",
		Message:  "Your voice sounds quite boomy - you may be too close to the microphone. Try moving back slightly or angling the mic to one side.",
	}
}

// tipSibilance fires when the adaptive de-esser was set to high intensity,
// confirmed by bright speech spectral characteristics.
// Checks FilterChainConfig.DeessIntensity > 0.5 (deessIntensityNormal in adaptive.go),
// speech centroid > 4000 Hz (centroidBright), and speech rolloff > 10000 Hz.
// Prefers SpeechProfile metrics when available, falling back to full-file metrics.
func tipSibilance(m *processor.AudioMeasurements, config *processor.FilterChainConfig) *RecordingTip {
	if config == nil || config.DeessIntensity <= 0.5 {
		return nil
	}

	centroid := m.SpectralCentroid
	rolloff := m.SpectralRolloff
	if m.SpeechProfile != nil {
		if m.SpeechProfile.SpectralCentroid > 0 {
			centroid = m.SpeechProfile.SpectralCentroid
		}
		if m.SpeechProfile.SpectralRolloff > 0 {
			rolloff = m.SpeechProfile.SpectralRolloff
		}
	}

	if centroid <= 4000.0 || rolloff <= 10000.0 {
		return nil
	}
	return &RecordingTip{
		Priority: 4,
		RuleID:   "sibilance",
		Message:  "Your recording has noticeable sibilance (harsh 's' and 'sh' sounds). Try angling your microphone slightly off-axis - point it at your chin rather than directly at your mouth.",
	}
}

// tipDynamicRange fires when the loudness range is very wide (InputLRA > 18 LU),
// indicating inconsistent speaking volume or microphone distance.
func tipDynamicRange(m *processor.AudioMeasurements, _ *processor.FilterChainConfig) *RecordingTip {
	if m.InputLRA <= 18.0 {
		return nil
	}
	return &RecordingTip{
		Priority: 5,
		RuleID:   "dynamic_range",
		Message:  "Your speaking volume varies quite a lot. Try to maintain a consistent distance from your microphone and a steady speaking level.",
	}
}

// tipOverCompressed fires when the crest factor is extremely low,
// indicating aggressive AGC or prior processing has damaged the audio.
// Threshold: CrestFactor < 6 dB (brickwalled per Spectral-Metrics-Reference.md).
// CrestFactor == 0 is treated as unmeasured and skipped.
func tipOverCompressed(m *processor.AudioMeasurements, _ *processor.FilterChainConfig) *RecordingTip {
	crest := m.CrestFactor
	if m.SpeechProfile != nil && m.SpeechProfile.CrestFactor > 0 {
		crest = m.SpeechProfile.CrestFactor
	}

	if crest >= 6.0 || crest == 0 {
		return nil
	}
	return &RecordingTip{
		Priority: 6,
		RuleID:   "over_compressed",
		Message:  "Your recording sounds heavily compressed, possibly by automatic gain control. If your microphone software has an 'AGC' or 'auto-level' setting, try turning it off and setting the gain manually.",
	}
}

// tipPoorSNR fires when the noise-to-speech gap is critically small.
// Threshold: NoiseReductionHeadroom < 10 dB (half of minSNRMargin 20 dB).
// NoiseReductionHeadroom == 0 is treated as unmeasured and skipped.
func tipPoorSNR(m *processor.AudioMeasurements, _ *processor.FilterChainConfig) *RecordingTip {
	if m.NoiseReductionHeadroom >= 10.0 || m.NoiseReductionHeadroom == 0 {
		return nil
	}
	return &RecordingTip{
		Priority: 7,
		RuleID:   "poor_snr",
		Message:  "The gap between your voice and the background noise is very small. Move closer to your microphone and reduce background noise if possible.",
	}
}
