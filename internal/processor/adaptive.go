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
	if m.SpectralKurtosis > lpContentKurtosisSpeech {
		speechScore++
	} else if m.SpectralKurtosis < lpContentKurtosisMusic {
		musicScore++
	}

	// Flatness: speech is tonal, music is flatter
	if m.SpectralFlatness < lpContentFlatnessSpeech {
		speechScore++
	} else if m.SpectralFlatness > lpContentFlatnessMusic {
		musicScore++
	}

	// Flux: speech is stable, music varies
	if m.SpectralFlux < lpContentFluxSpeech {
		speechScore++
	} else if m.SpectralFlux > lpContentFluxMusic {
		musicScore++
	}

	// Crest: speech has dominant peaks
	if m.SpectralCrest > lpContentCrestSpeech {
		speechScore++
	} else if m.SpectralCrest < lpContentCrestMusic {
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

// sanitizeConfig ensures no NaN or Inf values remain after adaptive tuning
func sanitizeConfig(config *EffectiveFilterConfig) {
	// DS201-inspired highpass filter
	config.DS201HPFreq = sanitizeFloat(config.DS201HPFreq, ds201DefaultHPFreq)
	config.DS201HPWidth = sanitizeFloat(config.DS201HPWidth, 0.707) // Butterworth default
	config.DS201HPMix = sanitizeFloat(config.DS201HPMix, 1.0)       // Full wet default

	// DS201-inspired lowpass filter
	config.DS201LPFreq = sanitizeFloat(config.DS201LPFreq, ds201LPDefaultFreq)
	config.DS201LPWidth = sanitizeFloat(config.DS201LPWidth, 0.707) // Butterworth default
	config.DS201LPMix = sanitizeFloat(config.DS201LPMix, 1.0)       // Full wet default

	// De-esser intensity
	config.DeessIntensity = sanitizeFloat(config.DeessIntensity, defaultDeessIntensity)

	// LA-2A compressor
	config.LA2ARatio = sanitizeFloat(config.LA2ARatio, defaultLA2ARatio)
	config.LA2AThreshold = sanitizeFloat(config.LA2AThreshold, defaultLA2AThreshold)
	// Note: LA2AMakeup not sanitised - always 0 (set in DefaultFilterConfig)

	// DS201-inspired gate threshold needs additional check for zero/negative
	if math.IsNaN(config.DS201GateThreshold) || math.IsInf(config.DS201GateThreshold, 0) || config.DS201GateThreshold <= 0 {
		config.DS201GateThreshold = ds201DefaultGateThreshold
	}
}
