package processor

// tuneNoiseRemove adjusts compand parameters based on measured noise floor.
// Uses the noise profile extracted from the elected room-tone region for
// accurate noise characterisation.
//
// The anlmdn parameters (strength, patch, research, smooth) are kept constant from spike validation.
// Compand parameters adapt to the measured noise floor:
// - Threshold: 5dB above noise floor (catches breaths but not speech)
// - Expansion: scales with noise severity (gentle for clean, aggressive for noisy)
//
// anlmdn stays constant because spike testing validated these parameters:
// - strength: 0.00001 (minimum)
// - patch: 6ms (context window)
// - research: 2.0ms / 0.0020s (r_min search window)
// - smooth: 3 (m_strict weight smoothing)
func tuneNoiseRemove(config *EffectiveFilterConfig, m *AudioMeasurements) {
	if !config.NoiseRemove.Enabled {
		return
	}

	// Without a noise profile, the compand has no calibration data.
	// The anlmdn denoiser is self-adapting and handles in-speech noise
	// without a reference profile. Disable the compand to avoid the
	// blind fallback risking attenuation of quiet speech.
	if m.NoiseProfile == nil || m.NoiseProfile.MeasuredNoiseFloor >= 0 {
		config.NoiseRemove.CompandEnabled = false
		return
	}

	// Re-enable compand (may have been disabled by a previous file in the same run)
	config.NoiseRemove.CompandEnabled = true

	noiseFloor := m.NoiseProfile.MeasuredNoiseFloor

	// Threshold: 5dB above noise floor (catches breaths but not speech)
	threshold := noiseFloor + 5.0
	// Clamp to reasonable range
	threshold = clamp(threshold, -70.0, -40.0)

	// Expansion: scale with noise severity
	expansion := scaleExpansion(noiseFloor)

	config.NoiseRemove.CompandThreshold = threshold
	config.NoiseRemove.CompandExpansion = expansion

	// attack, decay, knee stay constant (validated in spike testing)
}

// preferSpeechMetric returns speech-specific measurement if available,
// otherwise falls back to full-file measurement.
func preferSpeechMetric(fullFile, speechProfile float64) float64 {
	if speechProfile > 0 {
		return speechProfile
	}
	return fullFile
}

// preferSpeechMetricSigned returns speech-specific measurement if speech data
// exists, otherwise falls back to full-file measurement. Unlike preferSpeechMetric,
// this variant uses an explicit flag rather than checking value > 0, making it
// safe for metrics that can legitimately be zero or negative (e.g. SpectralDecrease,
// SpectralSkewness).
func preferSpeechMetricSigned(fullFile, speechValue float64, hasSpeech bool) float64 {
	if hasSpeech {
		return speechValue
	}
	return fullFile
}

// scaleExpansion returns expansion depth based on noise severity.
// Noisier recordings need more aggressive expansion to suppress residuals.
func scaleExpansion(noiseFloor float64) float64 {
	switch {
	case noiseFloor > -45.0:
		return 12.0 // Very noisy - aggressive
	case noiseFloor > -55.0:
		return 8.0 // Moderate noise
	case noiseFloor > -65.0:
		return 6.0 // Typical
	default:
		return 4.0 // Very clean - gentle
	}
}
