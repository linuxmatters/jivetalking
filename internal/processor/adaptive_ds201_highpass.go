package processor

const (
	ds201DefaultHPFreq = 80.0

	// DS201 High-Pass frequency tuning
	ds201HPMinFreq         = 60.0  // Hz - dark/warm voice cutoff
	ds201HPDefaultFreq     = 80.0  // Hz - normal voice cutoff
	ds201HPBrightFreq      = 100.0 // Hz - bright voice cutoff
	ds201HPMaxFreq         = 120.0 // Hz - maximum to preserve voice fundamentals
	ds201HPBoostModerate   = 10.0  // Hz - added when silence sample shows LF noise
	ds201HPBoostAggressive = 20.0  // Hz - added for noisy silence sample (> -55 dBFS)

	// DS201 High-Pass warm voice protection parameters
	// Instead of disabling highpass for warm voices, we use gentler settings
	ds201HPWarmFreq      = 70.0 // Hz - slightly reduced cutoff for warm voices
	ds201HPVeryWarmFreq  = 60.0 // Hz - minimum cutoff for very warm voices
	ds201HPWarmWidth     = 0.5  // Q - gentler rolloff than Butterworth (0.707)
	ds201HPVeryWarmWidth = 0.5  // Q - gentler rolloff for very warm voices
	ds201HPWarmMix       = 0.9  // Wet/dry mix for warm voices (90% filtered)
	ds201HPVeryWarmMix   = 0.8  // Wet/dry mix for very warm voices (80% filtered)

	// Spectral decrease thresholds for LF voice content protection
	spectralDecreaseVeryWarm = -0.10 // Below: very warm voice, needs maximum LF protection
	spectralDecreaseWarm     = -0.05 // Below: warm voice with significant LF body
	// Above 0: thin voice, highpass safe

	// Spectral skewness threshold for LF emphasis detection
	// Positive skewness indicates energy concentrated in lower frequencies
	// Used as secondary protection when spectral decrease alone doesn't catch warm voices
	spectralSkewnessLFEmphasis = 1.0 // Above: significant LF emphasis, needs gentle HPF

	// Silence sample noise floor thresholds for highpass boost decision
	silenceNoiseFloorClean = -70.0 // dBFS - very clean, no boost needed
	silenceNoiseFloorNoisy = -55.0 // dBFS - noisy, may need boost

	// Silence entropy threshold for noise character
	silenceEntropyTonal = 0.30 // Below: tonal noise (hum), bandreject better than highpass

	// Spectral centroid thresholds (Hz) for voice brightness classification
	centroidBright = 4000.0 // Above: bright voice
	centroidNormal = 2500.0 // Above: normal voice, below: dark voice
)

// tuneDS201HighPass adapts DS201-inspired highpass composite filter based on:
// - Spectral centroid (voice brightness/warmth)
// - Spectral decrease (LF voice content - protects warm voices)
// - Silence sample noise floor (actual LF noise level)
// - Silence sample entropy (noise character - tonal vs broadband)
//
// This is a composite tuner that configures both:
// 1. Highpass frequency and slope settings
// 2. Hum notch filter settings (when tonal noise detected)
//
// Highpass strategy:
// - Lower centroid (darker voice) → lower cutoff to preserve warmth
// - Higher centroid (brighter voice) → higher cutoff, safe for rumble removal
// - Negative spectral decrease (warm voice) → cap cutoff to protect LF body
// - Tonal noise (low entropy) → don't boost, let bandreject handle hum
// - Only boost cutoff if silence sample shows actual broadband LF noise
//
// Hum notch strategy:
// - Low entropy (< 0.30) indicates periodic/tonal noise → enable hum removal
// - High entropy indicates broadband noise → skip notch filter
// - Voice-aware: reduces harmonics for warm voices to protect vocal fundamentals
func tuneDS201HighPass(config *EffectiveFilterConfig, measurements *AudioMeasurements) {
	config.DS201HPPoles = ds201HPDefaultPoles
	config.DS201HPWidth = ds201HPDefaultWidth
	config.DS201HPMix = ds201HPDefaultMix
	config.DS201HPTransform = ds201HPDefaultTransform

	if measurements.SpectralCentroid <= 0 {
		// No spectral analysis available - keep default
		return
	}

	// Prefer speech-specific spectral metrics when available.
	// Full-file averages are diluted by silence in multi-track recordings.
	hasSpeech := measurements.SpeechProfile != nil
	centroid := measurements.SpectralCentroid
	if hasSpeech {
		centroid = preferSpeechMetric(centroid, measurements.SpeechProfile.Spectral.Centroid)
	}
	var speechDecrease, speechSkewness float64
	if hasSpeech {
		speechDecrease = measurements.SpeechProfile.Spectral.Decrease
		speechSkewness = measurements.SpeechProfile.Spectral.Skewness
	}
	decrease := preferSpeechMetricSigned(measurements.SpectralDecrease, speechDecrease, hasSpeech)
	skewness := preferSpeechMetricSigned(measurements.SpectralSkewness, speechSkewness, hasSpeech)

	// Determine base frequency from spectral centroid
	var baseFreq float64
	switch {
	case centroid > centroidBright:
		// Bright voice with high-frequency energy concentration
		// Safe to use higher cutoff - voice energy is well above 100Hz
		baseFreq = ds201HPBrightFreq
	case centroid > centroidNormal:
		// Normal voice with balanced frequency distribution
		// Use standard cutoff for podcast speech
		baseFreq = ds201HPDefaultFreq
	default:
		// Dark/warm voice with low-frequency energy concentration
		// Use lower cutoff to preserve voice warmth and body
		baseFreq = ds201HPMinFreq
	}

	// Check if we should boost cutoff based on actual noise characteristics
	// Only boost if silence sample shows broadband LF noise (not tonal hum)
	shouldBoost := false
	boostAmount := 0.0

	if measurements.NoiseProfile != nil {
		silenceNoiseFloor := measurements.NoiseProfile.MeasuredNoiseFloor
		silenceEntropy := measurements.NoiseProfile.Entropy

		// Only consider boost if noise is broadband (not tonal hum)
		// Tonal noise (low entropy) is better handled by bandreject filter
		if silenceEntropy >= silenceEntropyTonal {
			// Broadband noise - highpass can help
			switch {
			case silenceNoiseFloor > silenceNoiseFloorNoisy:
				// Noisy silence sample - aggressive boost warranted
				shouldBoost = true
				boostAmount = ds201HPBoostAggressive
			case silenceNoiseFloor > silenceNoiseFloorClean:
				// Moderate noise - gentle boost
				shouldBoost = true
				boostAmount = ds201HPBoostModerate
			}
		}
	}

	// Apply boost if warranted by noise characteristics (only for non-warm voices)
	if shouldBoost {
		config.DS201HPFreq = baseFreq + boostAmount
	} else {
		config.DS201HPFreq = baseFreq
	}

	// Set TDII transform for all highpass (best floating-point accuracy)
	config.DS201HPTransform = ds201HPDefaultTransform

	// Protect warm voices with significant LF body
	// Instead of disabling highpass, we use gentler settings:
	// - Lower frequency (subsonic only)
	// - Lower Q (gentler rolloff)
	// - Reduced mix (blend filtered with dry signal)
	//
	// This removes subsonic rumble while preserving bass character.
	switch {
	case decrease < spectralDecreaseVeryWarm:
		// Very warm voice
		// Use minimal settings: 60Hz cutoff, gentle Q, 80% mix
		config.DS201HPFreq = ds201HPVeryWarmFreq
		config.DS201HPWidth = ds201HPVeryWarmWidth
		config.DS201HPMix = ds201HPVeryWarmMix
		config.DS201HPPoles = 1 // Gentle 6dB/oct slope
	case skewness > spectralSkewnessLFEmphasis:
		// Significant LF emphasis
		// Use warm settings: 70Hz cutoff, gentle Q, 90% mix
		config.DS201HPFreq = ds201HPWarmFreq
		config.DS201HPWidth = ds201HPWarmWidth
		config.DS201HPMix = ds201HPWarmMix
		config.DS201HPPoles = 1 // Gentle 6dB/oct slope
	case decrease < spectralDecreaseWarm:
		// Warm voice - cap at default with gentle slope to preserve body
		if config.DS201HPFreq > ds201HPDefaultFreq {
			config.DS201HPFreq = ds201HPDefaultFreq
		}
		config.DS201HPPoles = 1 // Gentle 6dB/oct slope
	}

	// Final cap at maximum to avoid affecting voice fundamentals
	if config.DS201HPFreq > ds201HPMaxFreq {
		config.DS201HPFreq = ds201HPMaxFreq
	}
}
