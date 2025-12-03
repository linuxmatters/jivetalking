// Package processor handles audio analysis and processing
package processor

import "math"

// AdaptConfig tunes all filter parameters based on Pass 1 measurements.
// This is the main entry point for adaptive configuration.
// It updates config in-place based on the audio characteristics measured in analysis.
func AdaptConfig(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Store measurements reference
	config.Measurements = measurements
	config.NoiseFloor = measurements.NoiseFloor

	// Calculate LUFS gap once - used by multiple tuning functions
	lufsGap := calculateLUFSGap(config.TargetI, measurements.InputI)

	// Tune each filter adaptively based on measurements
	tuneHighpassFreq(config, measurements, lufsGap)
	tuneNoiseReduction(config, measurements, lufsGap)
	tuneDeesser(config, measurements)
	tuneGateThreshold(config, measurements)
	tuneCompression(config, measurements)
	tuneDynaudnorm(config)
	tuneSpeechnorm(config, measurements, lufsGap)

	// Final safety checks
	sanitizeConfig(config)
}

// calculateLUFSGap returns the dB difference between target and input LUFS.
// Returns 0.0 if input is not measured.
func calculateLUFSGap(targetI, inputI float64) float64 {
	if inputI != 0.0 {
		return targetI - inputI
	}
	return 0.0
}

// tuneHighpassFreq adapts highpass filter cutoff frequency based on:
// - Spectral centroid (voice brightness/warmth)
// - LUFS gap (noise reduction needs)
//
// Strategy:
// - Lower centroid (darker voice) → lower cutoff to preserve warmth
// - Higher centroid (brighter voice) → higher cutoff, safe for rumble removal
// - Heavy noise reduction needed → boost cutoff to remove low-frequency room noise
func tuneHighpassFreq(config *FilterChainConfig, measurements *AudioMeasurements, lufsGap float64) {
	if measurements.SpectralCentroid <= 0 {
		// No spectral analysis available - keep default 80Hz
		return
	}

	// Determine base frequency from spectral centroid
	var baseFreq float64
	switch {
	case measurements.SpectralCentroid > 6000:
		// Bright voice with high-frequency energy concentration
		// Safe to use higher cutoff - voice energy is well above 100Hz
		baseFreq = 100.0
	case measurements.SpectralCentroid > 4000:
		// Normal voice with balanced frequency distribution
		// Use standard cutoff for podcast speech
		baseFreq = 80.0
	default:
		// Dark/warm voice with low-frequency energy concentration
		// Use lower cutoff to preserve voice warmth and body
		baseFreq = 60.0
	}

	// Boost cutoff for heavy noise reduction needs (removes low-frequency room noise)
	switch {
	case lufsGap > 25.0:
		// Very quiet source needing aggressive processing
		config.HighpassFreq = baseFreq + 40.0
	case lufsGap > 15.0:
		// Moderately quiet source
		config.HighpassFreq = baseFreq + 20.0
	default:
		// Normal source
		config.HighpassFreq = baseFreq
	}

	// Cap at 120Hz maximum to avoid affecting voice fundamentals
	if config.HighpassFreq > 120.0 {
		config.HighpassFreq = 120.0
	}
}

// tuneNoiseReduction adapts FFT noise reduction based on upcoming gain.
//
// Key insight: If we apply 30dB of gain later (via speechnorm/dynaudnorm),
// we need to remove 30dB of noise NOW, or it will be amplified with speech.
//
// Strategy:
// 1. Base reduction (12dB) for recordings already near target
// 2. Add LUFS gap to account for upcoming amplification
// 3. Clamp to 6-40dB (afftdn stability limits)
func tuneNoiseReduction(config *FilterChainConfig, measurements *AudioMeasurements, lufsGap float64) {
	if measurements.InputI == 0.0 {
		// Fallback if no LUFS measurement
		config.NoiseReduction = 12.0
		return
	}

	const (
		baseReduction = 12.0 // Standard for clean recordings
		minReduction  = 6.0  // Always do some noise reduction
		maxReduction  = 40.0 // afftdn becomes unstable beyond this
	)

	// Add the LUFS gap to noise reduction
	adaptiveReduction := baseReduction + lufsGap

	// Clamp to reasonable limits
	config.NoiseReduction = clamp(adaptiveReduction, minReduction, maxReduction)
}

// tuneDeesser adapts de-esser intensity based on spectral analysis.
// Uses both spectral centroid (energy concentration) and rolloff (HF extension)
// to detect likelihood of harsh sibilance.
//
// Strategy:
// - High centroid + high rolloff → likely sibilance, use more de-essing
// - Low rolloff → limited HF content, skip or reduce de-essing
// - Dark voice with no HF extension → disable de-esser entirely
func tuneDeesser(config *FilterChainConfig, measurements *AudioMeasurements) {
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
func tuneDeesserFull(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Determine baseline intensity from centroid
	var baseIntensity float64
	switch {
	case measurements.SpectralCentroid > 7000:
		baseIntensity = 0.6 // Bright voice
	case measurements.SpectralCentroid > 6000:
		baseIntensity = 0.5 // Normal voice
	default:
		baseIntensity = 0.4 // Dark voice
	}

	// Refine based on spectral rolloff (HF extension)
	switch {
	case measurements.SpectralRolloff < 6000:
		// Very limited HF content - no sibilance expected
		config.DeessIntensity = 0.0

	case measurements.SpectralRolloff < 8000:
		// Limited HF extension - reduce intensity
		config.DeessIntensity = baseIntensity * 0.7
		if config.DeessIntensity < 0.3 {
			config.DeessIntensity = 0.0 // Skip if too low
		}

	case measurements.SpectralRolloff > 12000:
		// Extensive HF content - likely sibilance
		config.DeessIntensity = math.Min(baseIntensity*1.2, 0.8)

	default:
		// Normal HF extension (8-12 kHz)
		config.DeessIntensity = baseIntensity
	}
}

// tuneDeesserCentroidOnly provides fallback when rolloff is unavailable
func tuneDeesserCentroidOnly(config *FilterChainConfig, measurements *AudioMeasurements) {
	switch {
	case measurements.SpectralCentroid > 7000:
		config.DeessIntensity = 0.6
	case measurements.SpectralCentroid > 6000:
		config.DeessIntensity = 0.5
	default:
		config.DeessIntensity = 0.4
	}
}

// tuneGateThreshold adapts noise gate based on measured noise floor.
//
// Gate threshold = noise floor + offset (dB above noise)
// Offset varies by recording quality:
// - Clean (<-60dB): 10dB offset for safety margin
// - Moderate (-60 to -50dB): 8dB offset for balance
// - Noisy (>-50dB): 6dB offset to preserve more speech
func tuneGateThreshold(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Determine offset based on noise floor quality
	var gateOffsetDB float64
	switch {
	case measurements.NoiseFloor < -60.0:
		// Very clean recording - larger margin avoids false triggers
		gateOffsetDB = 10.0
	case measurements.NoiseFloor < -50.0:
		// Typical podcast recording
		gateOffsetDB = 8.0
	default:
		// Noisy recording - smaller margin preserves more speech
		gateOffsetDB = 6.0
	}

	// Calculate threshold: noise floor + offset, convert to linear
	gateThresholdDB := measurements.NoiseFloor + gateOffsetDB
	config.GateThreshold = dbToLinear(gateThresholdDB)

	// Safety limits for extreme cases
	const (
		minThresholdDB = -70.0 // Professional studio (clean)
		maxThresholdDB = -25.0 // Very noisy environment
	)

	minThresholdLinear := dbToLinear(minThresholdDB)
	maxThresholdLinear := dbToLinear(maxThresholdDB)

	config.GateThreshold = clamp(config.GateThreshold, minThresholdLinear, maxThresholdLinear)
}

// tuneCompression adapts dynamics processing based on:
// - Dynamic range (how much variation in loud/quiet parts)
// - Loudness range (LRA - transient characteristics)
// - Noise floor (recording quality affects artifact audibility)
func tuneCompression(config *FilterChainConfig, measurements *AudioMeasurements) {
	tuneCompressionRatioAndThreshold(config, measurements)
	tuneCompressionTiming(config, measurements)
	tuneCompressionMix(config, measurements)
}

// tuneCompressionRatioAndThreshold sets ratio, threshold, and makeup gain
func tuneCompressionRatioAndThreshold(config *FilterChainConfig, measurements *AudioMeasurements) {
	if measurements.DynamicRange <= 0 {
		// No measurement - keep defaults
		return
	}

	switch {
	case measurements.DynamicRange > 30.0:
		// Very dynamic content (expressive delivery)
		config.CompRatio = 2.0
		config.CompThreshold = -16.0
		config.CompMakeup = 1.0

	case measurements.DynamicRange > 20.0:
		// Moderately dynamic (typical podcast)
		config.CompRatio = 3.0
		config.CompThreshold = -18.0
		config.CompMakeup = 2.0

	default:
		// Already compressed/consistent
		config.CompRatio = 4.0
		config.CompThreshold = -20.0
		config.CompMakeup = 3.0
	}
}

// tuneCompressionTiming sets attack and release based on loudness range
func tuneCompressionTiming(config *FilterChainConfig, measurements *AudioMeasurements) {
	switch {
	case measurements.InputLRA > 15.0:
		// Wide loudness range - preserve transients
		config.CompAttack = 25
		config.CompRelease = 150

	case measurements.InputLRA > 10.0:
		// Moderate range
		config.CompAttack = 20
		config.CompRelease = 100

	default:
		// Narrow range - tighter control
		config.CompAttack = 15
		config.CompRelease = 80
	}
}

// tuneCompressionMix sets wet/dry mix based on noise floor and dynamic range
func tuneCompressionMix(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Noise floor indicates recording quality (artifact audibility)
	var mixFactor float64
	switch {
	case measurements.NoiseFloor < -50:
		mixFactor = 0.95 // Clean - can use more compression
	case measurements.NoiseFloor < -40:
		mixFactor = 0.85 // Moderate quality
	default:
		mixFactor = 0.75 // Noisy - gentler to mask pumping
	}

	// Adjust based on dynamic range (content characteristics)
	switch {
	case measurements.DynamicRange > 30:
		// Very dynamic - preserve more dry signal
		config.CompMix = mixFactor - 0.10
	case measurements.DynamicRange > 20:
		// Moderate dynamics
		config.CompMix = mixFactor
	default:
		// Already compressed - can use more wet
		config.CompMix = math.Min(1.0, mixFactor+0.10)
	}
}

// tuneDynaudnorm sets conservative fixed parameters for dynaudnorm.
// Unlike other filters, dynaudnorm uses fixed values to prevent
// distortion/clipping from overly aggressive adaptive tuning.
func tuneDynaudnorm(config *FilterChainConfig) {
	config.DynaudnormFrameLen = 500      // 500ms frames (balanced)
	config.DynaudnormFilterSize = 31     // Gaussian filter (smooth)
	config.DynaudnormPeakValue = 0.95    // 5% headroom
	config.DynaudnormMaxGain = 5.0       // Conservative max gain
	config.DynaudnormTargetRMS = 0.0     // Peak-based only
	config.DynaudnormCompress = 0.0      // No compression (acompressor handles it)
	config.DynaudnormThreshold = 0.0     // Normalize all frames
	config.DynaudnormChannels = false    // Coupled channels
	config.DynaudnormDCCorrect = false   // No DC correction
	config.DynaudnormAltBoundary = false // Standard boundary mode
}

// tuneSpeechnorm adapts cycle-level normalization based on input LUFS.
// Also enables RNN/NLM denoise for heavily uplifted audio.
//
// Key features:
// - Expansion capped at 10x (20dB) for quality preservation
// - RMS targeting for LUFS consistency
// - Automatic denoise activation when expansion ≥8x
func tuneSpeechnorm(config *FilterChainConfig, measurements *AudioMeasurements, lufsGap float64) {
	if measurements.InputI == 0.0 {
		return
	}

	// Calculate expansion factor from LUFS gap
	expansion := math.Pow(10, lufsGap/20.0)

	// Cap expansion at 10x (20dB) for audio quality
	// Very quiet sources accept higher output LUFS rather than degraded quality
	const maxExpansion = 10.0
	expansion = clamp(expansion, 1.0, maxExpansion)
	config.SpeechnormExpansion = expansion

	// Enable denoise for heavily uplifted audio (≥8x / 18dB)
	tuneSpeechnormDenoise(config, expansion)

	// RMS targeting for LUFS consistency
	// Rough conversion: LUFS ≈ -23 + 20*log10(RMS)
	targetRMS := math.Pow(10, (config.TargetI+23)/20.0)
	config.SpeechnormRMS = clamp(targetRMS, 0.0, 1.0)

	// Fixed parameters for speech
	config.SpeechnormThreshold = 0.0   // Expand all audio
	config.SpeechnormCompression = 1.0 // No compression (acompressor handled it)
	config.SpeechnormPeak = 0.95       // Headroom for limiter
	config.SpeechnormRaise = 0.001     // Fast response
	config.SpeechnormFall = 0.001      // Fast response
}

// tuneSpeechnormDenoise enables RNN/NLM denoise for heavily expanded audio
func tuneSpeechnormDenoise(config *FilterChainConfig, expansion float64) {
	const expansionThreshold = 8.0 // 18dB gain

	if expansion >= expansionThreshold {
		// Enable RNN denoise (neural network mop-up)
		config.ArnnDnEnabled = true
		config.ArnnDnMix = 0.8

		// Enable NLM denoise (patch-based cleanup)
		config.AnlmDnEnabled = true
		// Adaptive strength: 8x → 0.00064, 10x → 0.001
		config.AnlmDnStrength = clamp(0.00001*expansion*expansion, 0.0, 0.01)
	} else {
		config.ArnnDnEnabled = false
		config.AnlmDnEnabled = false
	}
}

// sanitizeConfig ensures no NaN or Inf values remain after adaptive tuning
func sanitizeConfig(config *FilterChainConfig) {
	config.HighpassFreq = sanitizeFloat(config.HighpassFreq, 80.0)
	config.DeessIntensity = sanitizeFloat(config.DeessIntensity, 0.0)
	config.NoiseReduction = sanitizeFloat(config.NoiseReduction, 12.0)
	config.CompRatio = sanitizeFloat(config.CompRatio, 2.5)
	config.CompThreshold = sanitizeFloat(config.CompThreshold, -20.0)
	config.CompMakeup = sanitizeFloat(config.CompMakeup, 3.0)

	// GateThreshold needs additional check for zero/negative
	if math.IsNaN(config.GateThreshold) || math.IsInf(config.GateThreshold, 0) || config.GateThreshold <= 0 {
		config.GateThreshold = 0.01 // -40dBFS default
	}
}

// sanitizeFloat returns defaultVal if val is NaN or Inf
func sanitizeFloat(val, defaultVal float64) float64 {
	if math.IsNaN(val) || math.IsInf(val, 0) {
		return defaultVal
	}
	return val
}

// clamp restricts val to the range [min, max]
func clamp(val, min, max float64) float64 {
	if val < min {
		return min
	}
	if val > max {
		return max
	}
	return val
}
