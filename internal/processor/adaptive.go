// Package processor handles audio analysis and processing
package processor

import (
	"math"
)

// ContentType classifies audio content for adaptive filter tuning.
type ContentType int

const (
	// ContentSpeech indicates speech-dominant content (podcast, voice recording).
	// Lowpass may enable if HF noise indicators are present.
	ContentSpeech ContentType = iota

	// ContentMusic indicates music-dominant content (bumpers, stings, jingles).
	// Lowpass is always disabled to preserve full spectrum.
	ContentMusic

	// ContentMixed indicates unclear or mixed content (speech over music bed).
	// Conservative approach: lowpass disabled to avoid audible HF loss.
	ContentMixed
)

// String returns a human-readable name for the content type.
func (c ContentType) String() string {
	switch c {
	case ContentSpeech:
		return "speech"
	case ContentMusic:
		return "music"
	case ContentMixed:
		return "mixed"
	default:
		return "unknown"
	}
}

// detectContentType classifies audio content based on spectral measurements.
// Returns ContentSpeech, ContentMusic, or ContentMixed.
//
// Speech characteristics:
//   - High kurtosis (>6): energy peaked at voice harmonics
//   - Lower flatness (<0.45): tonal, not noise-like
//   - Low flux (<0.003): stable sustained phonation
//   - High crest (>30): dominant voice peaks
//
// Music characteristics:
//   - Low kurtosis (<5): energy spread across instruments
//   - Higher flatness (>0.55): more uniform spectral energy
//   - Higher flux (>0.005): rhythmic variation
//   - Lower crest (<25): multiple sources averaging out
func detectContentType(m *AudioMeasurements) ContentType {
	speechScore := 0
	musicScore := 0

	// Kurtosis: speech is peaked, music is spread
	if m.Spectral.Kurtosis > lpContentKurtosisSpeech {
		speechScore++
	} else if m.Spectral.Kurtosis < lpContentKurtosisMusic {
		musicScore++
	}

	// Flatness: speech is tonal, music is flatter
	if m.Spectral.Flatness < lpContentFlatnessSpeech {
		speechScore++
	} else if m.Spectral.Flatness > lpContentFlatnessMusic {
		musicScore++
	}

	// Flux: speech is stable, music varies
	if m.Spectral.Flux < lpContentFluxSpeech {
		speechScore++
	} else if m.Spectral.Flux > lpContentFluxMusic {
		musicScore++
	}

	// Crest: speech has dominant peaks
	if m.Spectral.Crest > lpContentCrestSpeech {
		speechScore++
	} else if m.Spectral.Crest < lpContentCrestMusic {
		musicScore++
	}

	// Decision: require threshold score to classify definitively
	if speechScore >= lpContentScoreThreshold {
		return ContentSpeech
	}
	if musicScore >= lpContentScoreThreshold {
		return ContentMusic
	}
	return ContentMixed
}

// AdaptConfig tunes all filter parameters based on Pass 1 measurements.
// This is the main entry point for adaptive configuration.
// It returns per-file effective config and diagnostics without mutating the caller's base seed.
func AdaptConfig(config *BaseFilterConfig, measurements *AudioMeasurements) (*EffectiveFilterConfig, *AdaptiveDiagnostics) {
	effectiveConfig := deriveEffectiveFilterConfig(config)
	if effectiveConfig == nil {
		return nil, nil
	}
	diagnostics := &AdaptiveDiagnostics{}

	// Tune each filter adaptively based on measurements
	// Order matters: gate threshold calculated BEFORE denoise filters
	tuneDS201HighPass(effectiveConfig, measurements)             // Composite: highpass + hum notch
	tuneDS201LowPass(effectiveConfig, diagnostics, measurements) // Ultrasonic rejection (adaptive)

	// NoiseRemove: anlmdn + compand (primary noise reduction)
	tuneNoiseRemove(effectiveConfig, measurements)

	tuneDS201Gate(effectiveConfig, diagnostics, measurements) // DS201-style soft expander gate
	tuneDeesser(effectiveConfig, measurements)
	tuneLA2ACompressor(effectiveConfig, diagnostics, measurements)
	// tuneVolumaxLimiter removed - limiter moved to Pass 4, tuned from Pass 3 measurements

	// Final safety checks
	sanitizeConfig(effectiveConfig)

	return effectiveConfig, diagnostics
}

// sanitizeConfig ensures no NaN or Inf values remain after adaptive tuning.
func sanitizeConfig(config *EffectiveFilterConfig) {
	sanitizeDS201HighPassConfig(&config.DS201HighPass)
	sanitizeDS201LowPassConfig(&config.DS201LowPass)
	sanitizeNoiseRemoveConfig(&config.NoiseRemove)
	sanitizeDS201GateConfig(&config.DS201Gate)
	sanitizeLA2AConfig(&config.LA2A)
	sanitizeDeesserConfig(&config.Deesser)
}

func sanitizeDS201HighPassConfig(config *DS201HighPassConfig) {
	config.Frequency = sanitizeFloat(config.Frequency, ds201DefaultHPFreq)
	config.Width = sanitizeFloat(config.Width, 0.707)
	config.Mix = sanitizeFloat(config.Mix, 1.0)
}

func sanitizeDS201LowPassConfig(config *DS201LowPassConfig) {
	config.Frequency = sanitizeFloat(config.Frequency, ds201LPDefaultFreq)
	config.Width = sanitizeFloat(config.Width, 0.707)
	config.Mix = sanitizeFloat(config.Mix, 1.0)
}

func sanitizeNoiseRemoveConfig(config *NoiseRemoveConfig) {
	defaults := defaultNoiseRemoveConfig()
	config.Strength = sanitizeFloat(config.Strength, defaults.Strength)
	config.PatchSec = sanitizeFloat(config.PatchSec, defaults.PatchSec)
	config.ResearchSec = sanitizeFloat(config.ResearchSec, defaults.ResearchSec)
	config.Smooth = sanitizeFloat(config.Smooth, defaults.Smooth)
	config.CompandThreshold = sanitizeFloat(config.CompandThreshold, defaults.CompandThreshold)
	config.CompandExpansion = sanitizeFloat(config.CompandExpansion, defaults.CompandExpansion)
	config.CompandAttack = sanitizeFloat(config.CompandAttack, defaults.CompandAttack)
	config.CompandDecay = sanitizeFloat(config.CompandDecay, defaults.CompandDecay)
	config.CompandKnee = sanitizeFloat(config.CompandKnee, defaults.CompandKnee)
}

func sanitizeDS201GateConfig(config *DS201GateConfig) {
	defaults := defaultDS201GateConfig()
	if math.IsNaN(config.Threshold) || math.IsInf(config.Threshold, 0) || config.Threshold <= 0 {
		config.Threshold = ds201DefaultGateThreshold
	}
	config.Ratio = sanitizeFloat(config.Ratio, defaults.Ratio)
	config.Attack = sanitizeFloat(config.Attack, defaults.Attack)
	config.Release = sanitizeFloat(config.Release, defaults.Release)
	config.Range = sanitizeFloat(config.Range, defaults.Range)
	config.Knee = sanitizeFloat(config.Knee, defaults.Knee)
	config.Makeup = sanitizeFloat(config.Makeup, defaults.Makeup)
}

func sanitizeLA2AConfig(config *LA2AConfig) {
	defaults := defaultLA2AConfig()
	config.Ratio = sanitizeFloat(config.Ratio, defaultLA2ARatio)
	config.Threshold = sanitizeFloat(config.Threshold, defaultLA2AThreshold)
	config.Attack = sanitizeFloat(config.Attack, defaults.Attack)
	config.Release = sanitizeFloat(config.Release, defaults.Release)
	config.Makeup = sanitizeFloat(config.Makeup, defaults.Makeup)
	config.Knee = sanitizeFloat(config.Knee, defaults.Knee)
	config.Mix = sanitizeFloat(config.Mix, defaults.Mix)
}

func sanitizeDeesserConfig(config *DeesserConfig) {
	defaults := defaultDeesserConfig()
	config.Intensity = sanitizeFloat(config.Intensity, defaultDeessIntensity)
	config.Amount = sanitizeFloat(config.Amount, defaults.Amount)
	config.Frequency = sanitizeFloat(config.Frequency, defaults.Frequency)
}
