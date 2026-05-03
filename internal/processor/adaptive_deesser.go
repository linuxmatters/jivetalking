package processor

import "math"

const (
	defaultDeessIntensity = 0.0

	// Spectral centroid threshold for de-esser intensity
	centroidVeryBright = 6000.0

	// Spectral rolloff thresholds (Hz) for HF content classification
	rolloffNoSibilance = 4000.0  // Below: no sibilance expected
	rolloffLimited     = 8000.0  // Below: limited HF extension
	rolloffExtensive   = 12000.0 // Above: extensive HF content

	// De-esser intensity levels
	deessIntensityBright = 0.6 // Bright voice base intensity
	deessIntensityNormal = 0.5 // Normal voice base intensity
	deessIntensityDark   = 0.4 // Dark voice base intensity
	deessIntensityMax    = 0.8 // Maximum intensity limit
	deessIntensityMin    = 0.3 // Minimum before disabling
)

// tuneDeesser adapts de-esser intensity based on spectral analysis.
// Uses both spectral centroid (energy concentration) and rolloff (HF extension)
// to detect likelihood of harsh sibilance.
//
// Strategy:
// - High centroid + high rolloff → likely sibilance, use more de-essing
// - Low rolloff → limited HF content, skip or reduce de-essing
// - Dark voice with no HF extension → disable de-esser entirely
func tuneDeesser(config *EffectiveFilterConfig, measurements *AudioMeasurements) {
	// Require speech profile for reliable sibilance detection.
	// Full-file spectral metrics are diluted by silence/noise regions
	// and produce false positives for sibilance in speech-sparse recordings.
	if measurements.SpeechProfile == nil {
		config.DeessIntensity = 0.0
		return
	}

	// Both centroid and rolloff available - full adaptive logic
	if measurements.SpectralCentroid > 0 && measurements.SpectralRolloff > 0 {
		tuneDeesserFull(config, measurements)
		return
	}

	// Only centroid available - simplified fallback
	if measurements.SpectralCentroid > 0 {
		tuneDeesserCentroidOnly(config, measurements)
		return
	}

	// No spectral analysis available - keep default 0.0 (disabled)
}

// tuneDeesserFull uses both centroid and rolloff for precise de-esser tuning
func tuneDeesserFull(config *EffectiveFilterConfig, measurements *AudioMeasurements) {
	// Prefer speech-specific measurements for sibilance detection
	centroid := measurements.SpectralCentroid
	rolloff := measurements.SpectralRolloff
	if measurements.SpeechProfile != nil {
		centroid = preferSpeechMetric(centroid, measurements.SpeechProfile.Spectral.Centroid)
		rolloff = preferSpeechMetric(rolloff, measurements.SpeechProfile.Spectral.Rolloff)
	}

	// Determine baseline intensity from centroid
	var baseIntensity float64
	switch {
	case centroid > centroidVeryBright:
		baseIntensity = deessIntensityBright // Bright voice
	case centroid > centroidBright:
		baseIntensity = deessIntensityNormal // Normal voice
	default:
		baseIntensity = deessIntensityDark // Dark voice
	}

	// Refine based on spectral rolloff (HF extension)
	switch {
	case rolloff < rolloffNoSibilance:
		// Very limited HF content - no sibilance expected
		config.DeessIntensity = 0.0

	case rolloff < rolloffLimited:
		// Limited HF extension - reduce intensity
		config.DeessIntensity = baseIntensity * 0.7
		if config.DeessIntensity < deessIntensityMin {
			config.DeessIntensity = 0.0 // Skip if too low
		}

	case rolloff > rolloffExtensive:
		// Extensive HF content - likely sibilance
		config.DeessIntensity = math.Min(baseIntensity*1.2, deessIntensityMax)

	default:
		// Normal HF extension (8-12 kHz)
		config.DeessIntensity = baseIntensity
	}
}

// tuneDeesserCentroidOnly provides fallback when rolloff is unavailable
func tuneDeesserCentroidOnly(config *EffectiveFilterConfig, measurements *AudioMeasurements) {
	// Prefer speech-specific centroid when available.
	// Full-file averages are diluted by silence in multi-track recordings.
	centroid := measurements.SpectralCentroid
	if measurements.SpeechProfile != nil {
		centroid = preferSpeechMetric(centroid, measurements.SpeechProfile.Spectral.Centroid)
	}

	switch {
	case centroid > centroidVeryBright:
		config.DeessIntensity = deessIntensityBright
	case centroid > centroidBright:
		config.DeessIntensity = deessIntensityNormal
	default:
		config.DeessIntensity = deessIntensityDark
	}
}
