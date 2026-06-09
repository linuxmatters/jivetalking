// Package processor handles audio analysis and processing
package processor

import (
	"math"
)

// ContentType classifies audio content for adaptive filter tuning.
type ContentType int

const (
	// ContentSpeech indicates speech-dominant content (podcast, voice recording).
	ContentSpeech ContentType = iota

	// ContentMusic indicates music-dominant content (bumpers, stings, jingles).
	ContentMusic

	// ContentMixed indicates unclear or mixed content (speech over music bed).
	ContentMixed
)

const (
	// Content-classification thresholds used by detectContentType.
	// These classify content as speech, music, or mixed from spectral characteristics.

	// Speech characteristics: peaked, tonal, stable
	lpContentKurtosisSpeech = 6.0   // Above: energy peaked at voice harmonics
	lpContentFlatnessSpeech = 0.45  // Below: tonal, not noise-like
	lpContentFluxSpeech     = 0.003 // Below: stable sustained phonation
	lpContentCrestSpeech    = 30.0  // Above: dominant voice peaks

	// Music characteristics: spread, uniform, varied
	lpContentKurtosisMusic = 5.0   // Below: energy spread across instruments
	lpContentFlatnessMusic = 0.55  // Above: more uniform spectral energy
	lpContentFluxMusic     = 0.005 // Above: rhythmic variation
	lpContentCrestMusic    = 25.0  // Below: multiple sources averaging out

	// Content type decision threshold
	lpContentScoreThreshold = 3 // Score needed to classify as speech or music
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
	tuneDS201LowPass(effectiveConfig, diagnostics, measurements) // Unconditional 20.5 kHz band-limit

	// NoiseRemove (anlmdn + afftdn) has no adaptive tuning: anlmdn is fixed from
	// spike validation and afftdn nr is fixed at 12 to avoid warble.

	tuneDS201Gate(effectiveConfig, diagnostics, measurements) // DS201-style soft expander gate
	tuneDeesser(effectiveConfig, measurements)
	tuneLA2ACompressor(effectiveConfig, diagnostics, measurements, config.logger)
	// The limiter lives in Pass 4 and is tuned from Pass 3 measurements, not here.

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
	config.Frequency = sanitizeFloat(config.Frequency, ds201LPBandLimitFreq)
	config.Width = sanitizeFloat(config.Width, 0.707)
	config.Mix = sanitizeFloat(config.Mix, 1.0)
}

func sanitizeNoiseRemoveConfig(config *NoiseRemoveConfig) {
	defaults := defaultNoiseRemoveConfig()
	config.Strength = sanitizeFloat(config.Strength, defaults.Strength)
	config.PatchSec = sanitizeFloat(config.PatchSec, defaults.PatchSec)
	config.ResearchSec = sanitizeFloat(config.ResearchSec, defaults.ResearchSec)
	config.Smooth = sanitizeFloat(config.Smooth, defaults.Smooth)
	config.AfftdnNoiseReduction = sanitizeFloat(config.AfftdnNoiseReduction, defaults.AfftdnNoiseReduction)
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
