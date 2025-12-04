// Package processor handles audio analysis and processing
package processor

import "math"

// Adaptive tuning constants for audio processing.
// These thresholds and limits control how filters adapt to input measurements.
const (
	// Highpass frequency tuning
	highpassMinFreq         = 60.0  // Hz - dark/warm voice cutoff
	highpassDefaultFreq     = 80.0  // Hz - normal voice cutoff
	highpassBrightFreq      = 100.0 // Hz - bright voice cutoff
	highpassMaxFreq         = 120.0 // Hz - maximum to preserve voice fundamentals
	highpassBoostModerate   = 20.0  // Hz - added for moderate noise reduction needs
	highpassBoostAggressive = 40.0  // Hz - added for heavy noise reduction needs

	// Spectral centroid thresholds (Hz) for voice brightness classification
	centroidBright     = 6000.0 // Above: bright voice
	centroidNormal     = 4000.0 // Above: normal voice, below: dark voice
	centroidVeryBright = 7000.0 // Threshold for de-esser intensity

	// Spectral rolloff thresholds (Hz) for HF content classification
	rolloffNoSibilance = 6000.0  // Below: no sibilance expected
	rolloffLimited     = 8000.0  // Below: limited HF extension
	rolloffExtensive   = 12000.0 // Above: extensive HF content

	// LUFS gap thresholds for adaptive processing intensity
	lufsGapModerate   = 15.0 // dB - moderate gain required
	lufsGapAggressive = 25.0 // dB - aggressive processing needed

	// Noise reduction (afftdn) parameters
	noiseReductionBase = 12.0 // dB - baseline for clean recordings
	noiseReductionMin  = 6.0  // dB - minimum (always some reduction)
	noiseReductionMax  = 40.0 // dB - maximum (afftdn stability limit)

	// De-esser intensity levels
	deessIntensityBright = 0.6 // Bright voice base intensity
	deessIntensityNormal = 0.5 // Normal voice base intensity
	deessIntensityDark   = 0.4 // Dark voice base intensity
	deessIntensityMax    = 0.8 // Maximum intensity limit
	deessIntensityMin    = 0.3 // Minimum before disabling

	// Gate threshold parameters
	gateOffsetClean    = 10.0  // dB above noise floor for clean recordings
	gateOffsetTypical  = 8.0   // dB above noise floor for typical podcasts
	gateOffsetNoisy    = 6.0   // dB above noise floor for noisy recordings
	gateThresholdMinDB = -70.0 // dB - professional studio (clean)
	gateThresholdMaxDB = -25.0 // dB - very noisy environment

	// Noise floor quality thresholds
	noiseFloorClean   = -60.0 // dBFS - very clean recording
	noiseFloorTypical = -50.0 // dBFS - typical podcast
	noiseFloorNoisy   = -40.0 // dBFS - noisy recording (for compression mix)

	// Compression parameters
	compDynamicRangeHigh = 30.0 // dB - very dynamic content
	compDynamicRangeMod  = 20.0 // dB - moderately dynamic
	compLRAWide          = 15.0 // LU - wide loudness range
	compLRAModerate      = 10.0 // LU - moderate loudness range

	// Compression ratios
	compRatioDynamic    = 2.0 // For very dynamic content
	compRatioModerate   = 3.0 // For typical podcasts
	compRatioCompressed = 4.0 // For already compressed content

	// Compression thresholds (dB)
	compThresholdDynamic    = -16.0
	compThresholdModerate   = -18.0
	compThresholdCompressed = -20.0

	// Compression makeup gain (dB)
	compMakeupDynamic    = 1.0
	compMakeupModerate   = 2.0
	compMakeupCompressed = 3.0

	// Compression timing (ms)
	compAttackFast  = 15
	compAttackMed   = 20
	compAttackSlow  = 25
	compReleaseFast = 80
	compReleaseMed  = 100
	compReleaseSlow = 150

	// Compression mix factors
	compMixClean    = 0.95 // Clean recordings - more compression OK
	compMixModerate = 0.85 // Moderate quality
	compMixNoisy    = 0.75 // Noisy - gentler to mask pumping
	compMixAdjust   = 0.10 // Mix adjustment for dynamic range

	// Dynaudnorm fixed parameters
	dynaudnormFrameLen   = 500  // ms - balanced frame length
	dynaudnormFilterSize = 31   // Gaussian filter size
	dynaudnormPeakValue  = 0.95 // 5% headroom
	dynaudnormMaxGain    = 5.0  // Conservative max gain
	dynaudnormTargetRMS  = 0.0  // Peak-based only
	dynaudnormCompress   = 0.0  // No compression
	dynaudnormThreshold  = 0.0  // Normalize all frames

	// Speechnorm parameters
	speechnormMaxExpansion       = 10.0  // Maximum 10x (20dB) expansion
	speechnormExpansionThreshold = 8.0   // Expansion level triggering denoise
	speechnormPeakTarget         = 0.95  // Headroom for limiter
	speechnormSmoothingFast      = 0.001 // Fast response time

	// RNN/NLM denoise parameters
	arnnDnMixDefault    = 0.8     // Full filtering when enabled
	anlmDnStrengthMin   = 0.0     // Minimum strength
	anlmDnStrengthMax   = 0.01    // Maximum strength
	anlmDnStrengthScale = 0.00001 // Scaling factor for expansion

	// LUFS to RMS conversion constant
	// Rough conversion: LUFS ≈ -23 + 20*log10(RMS)
	lufsRmsOffset = 23.0

	// Default fallback values for sanitization
	defaultHighpassFreq   = 80.0
	defaultDeessIntensity = 0.0
	defaultNoiseReduction = 12.0
	defaultCompRatio      = 2.5
	defaultCompThreshold  = -20.0
	defaultCompMakeup     = 3.0
	defaultGateThreshold  = 0.01 // -40dBFS
)

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
		// No spectral analysis available - keep default
		return
	}

	// Determine base frequency from spectral centroid
	var baseFreq float64
	switch {
	case measurements.SpectralCentroid > centroidBright:
		// Bright voice with high-frequency energy concentration
		// Safe to use higher cutoff - voice energy is well above 100Hz
		baseFreq = highpassBrightFreq
	case measurements.SpectralCentroid > centroidNormal:
		// Normal voice with balanced frequency distribution
		// Use standard cutoff for podcast speech
		baseFreq = highpassDefaultFreq
	default:
		// Dark/warm voice with low-frequency energy concentration
		// Use lower cutoff to preserve voice warmth and body
		baseFreq = highpassMinFreq
	}

	// Boost cutoff for heavy noise reduction needs (removes low-frequency room noise)
	switch {
	case lufsGap > lufsGapAggressive:
		// Very quiet source needing aggressive processing
		config.HighpassFreq = baseFreq + highpassBoostAggressive
	case lufsGap > lufsGapModerate:
		// Moderately quiet source
		config.HighpassFreq = baseFreq + highpassBoostModerate
	default:
		// Normal source
		config.HighpassFreq = baseFreq
	}

	// Cap at maximum to avoid affecting voice fundamentals
	if config.HighpassFreq > highpassMaxFreq {
		config.HighpassFreq = highpassMaxFreq
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
		config.NoiseReduction = noiseReductionBase
		return
	}

	// Add the LUFS gap to noise reduction
	adaptiveReduction := noiseReductionBase + lufsGap

	// Clamp to reasonable limits (afftdn stability)
	config.NoiseReduction = clamp(adaptiveReduction, noiseReductionMin, noiseReductionMax)
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
	case measurements.SpectralCentroid > centroidVeryBright:
		baseIntensity = deessIntensityBright // Bright voice
	case measurements.SpectralCentroid > centroidBright:
		baseIntensity = deessIntensityNormal // Normal voice
	default:
		baseIntensity = deessIntensityDark // Dark voice
	}

	// Refine based on spectral rolloff (HF extension)
	switch {
	case measurements.SpectralRolloff < rolloffNoSibilance:
		// Very limited HF content - no sibilance expected
		config.DeessIntensity = 0.0

	case measurements.SpectralRolloff < rolloffLimited:
		// Limited HF extension - reduce intensity
		config.DeessIntensity = baseIntensity * 0.7
		if config.DeessIntensity < deessIntensityMin {
			config.DeessIntensity = 0.0 // Skip if too low
		}

	case measurements.SpectralRolloff > rolloffExtensive:
		// Extensive HF content - likely sibilance
		config.DeessIntensity = math.Min(baseIntensity*1.2, deessIntensityMax)

	default:
		// Normal HF extension (8-12 kHz)
		config.DeessIntensity = baseIntensity
	}
}

// tuneDeesserCentroidOnly provides fallback when rolloff is unavailable
func tuneDeesserCentroidOnly(config *FilterChainConfig, measurements *AudioMeasurements) {
	switch {
	case measurements.SpectralCentroid > centroidVeryBright:
		config.DeessIntensity = deessIntensityBright
	case measurements.SpectralCentroid > centroidBright:
		config.DeessIntensity = deessIntensityNormal
	default:
		config.DeessIntensity = deessIntensityDark
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
	case measurements.NoiseFloor < noiseFloorClean:
		// Very clean recording - larger margin avoids false triggers
		gateOffsetDB = gateOffsetClean
	case measurements.NoiseFloor < noiseFloorTypical:
		// Typical podcast recording
		gateOffsetDB = gateOffsetTypical
	default:
		// Noisy recording - smaller margin preserves more speech
		gateOffsetDB = gateOffsetNoisy
	}

	// Calculate threshold: noise floor + offset, convert to linear
	gateThresholdDB := measurements.NoiseFloor + gateOffsetDB
	config.GateThreshold = dbToLinear(gateThresholdDB)

	// Safety limits for extreme cases
	minThresholdLinear := dbToLinear(gateThresholdMinDB)
	maxThresholdLinear := dbToLinear(gateThresholdMaxDB)

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
	case measurements.DynamicRange > compDynamicRangeHigh:
		// Very dynamic content (expressive delivery)
		config.CompRatio = compRatioDynamic
		config.CompThreshold = compThresholdDynamic
		config.CompMakeup = compMakeupDynamic

	case measurements.DynamicRange > compDynamicRangeMod:
		// Moderately dynamic (typical podcast)
		config.CompRatio = compRatioModerate
		config.CompThreshold = compThresholdModerate
		config.CompMakeup = compMakeupModerate

	default:
		// Already compressed/consistent
		config.CompRatio = compRatioCompressed
		config.CompThreshold = compThresholdCompressed
		config.CompMakeup = compMakeupCompressed
	}
}

// tuneCompressionTiming sets attack and release based on loudness range
func tuneCompressionTiming(config *FilterChainConfig, measurements *AudioMeasurements) {
	switch {
	case measurements.InputLRA > compLRAWide:
		// Wide loudness range - preserve transients
		config.CompAttack = compAttackSlow
		config.CompRelease = compReleaseSlow

	case measurements.InputLRA > compLRAModerate:
		// Moderate range
		config.CompAttack = compAttackMed
		config.CompRelease = compReleaseMed

	default:
		// Narrow range - tighter control
		config.CompAttack = compAttackFast
		config.CompRelease = compReleaseFast
	}
}

// tuneCompressionMix sets wet/dry mix based on noise floor and dynamic range
func tuneCompressionMix(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Noise floor indicates recording quality (artifact audibility)
	var mixFactor float64
	switch {
	case measurements.NoiseFloor < noiseFloorTypical:
		mixFactor = compMixClean // Clean - can use more compression
	case measurements.NoiseFloor < noiseFloorNoisy:
		mixFactor = compMixModerate // Moderate quality
	default:
		mixFactor = compMixNoisy // Noisy - gentler to mask pumping
	}

	// Adjust based on dynamic range (content characteristics)
	switch {
	case measurements.DynamicRange > compDynamicRangeHigh:
		// Very dynamic - preserve more dry signal
		config.CompMix = mixFactor - compMixAdjust
	case measurements.DynamicRange > compDynamicRangeMod:
		// Moderate dynamics
		config.CompMix = mixFactor
	default:
		// Already compressed - can use more wet
		config.CompMix = math.Min(1.0, mixFactor+compMixAdjust)
	}
}

// tuneDynaudnorm sets conservative fixed parameters for dynaudnorm.
// Unlike other filters, dynaudnorm uses fixed values to prevent
// distortion/clipping from overly aggressive adaptive tuning.
func tuneDynaudnorm(config *FilterChainConfig) {
	config.DynaudnormFrameLen = dynaudnormFrameLen
	config.DynaudnormFilterSize = dynaudnormFilterSize
	config.DynaudnormPeakValue = dynaudnormPeakValue
	config.DynaudnormMaxGain = dynaudnormMaxGain
	config.DynaudnormTargetRMS = dynaudnormTargetRMS
	config.DynaudnormCompress = dynaudnormCompress
	config.DynaudnormThreshold = dynaudnormThreshold
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

	// Cap expansion for audio quality
	// Very quiet sources accept higher output LUFS rather than degraded quality
	expansion = clamp(expansion, 1.0, speechnormMaxExpansion)
	config.SpeechnormExpansion = expansion

	// Enable denoise for heavily uplifted audio
	tuneSpeechnormDenoise(config, expansion)

	// RMS targeting for LUFS consistency
	// Rough conversion: LUFS ≈ -23 + 20*log10(RMS)
	targetRMS := math.Pow(10, (config.TargetI+lufsRmsOffset)/20.0)
	config.SpeechnormRMS = clamp(targetRMS, 0.0, 1.0)

	// Fixed parameters for speech
	config.SpeechnormThreshold = 0.0                 // Expand all audio
	config.SpeechnormCompression = 1.0               // No compression (acompressor handled it)
	config.SpeechnormPeak = speechnormPeakTarget     // Headroom for limiter
	config.SpeechnormRaise = speechnormSmoothingFast // Fast response
	config.SpeechnormFall = speechnormSmoothingFast  // Fast response
}

// tuneSpeechnormDenoise enables RNN/NLM denoise for heavily expanded audio
func tuneSpeechnormDenoise(config *FilterChainConfig, expansion float64) {
	if expansion >= speechnormExpansionThreshold {
		// Enable RNN denoise (neural network mop-up)
		config.ArnnDnEnabled = true
		config.ArnnDnMix = arnnDnMixDefault

		// Enable NLM denoise (patch-based cleanup)
		config.AnlmDnEnabled = true
		// Adaptive strength scales with expansion squared
		config.AnlmDnStrength = clamp(anlmDnStrengthScale*expansion*expansion, anlmDnStrengthMin, anlmDnStrengthMax)
	} else {
		config.ArnnDnEnabled = false
		config.AnlmDnEnabled = false
	}
}

// sanitizeConfig ensures no NaN or Inf values remain after adaptive tuning
func sanitizeConfig(config *FilterChainConfig) {
	config.HighpassFreq = sanitizeFloat(config.HighpassFreq, defaultHighpassFreq)
	config.DeessIntensity = sanitizeFloat(config.DeessIntensity, defaultDeessIntensity)
	config.NoiseReduction = sanitizeFloat(config.NoiseReduction, defaultNoiseReduction)
	config.CompRatio = sanitizeFloat(config.CompRatio, defaultCompRatio)
	config.CompThreshold = sanitizeFloat(config.CompThreshold, defaultCompThreshold)
	config.CompMakeup = sanitizeFloat(config.CompMakeup, defaultCompMakeup)

	// GateThreshold needs additional check for zero/negative
	if math.IsNaN(config.GateThreshold) || math.IsInf(config.GateThreshold, 0) || config.GateThreshold <= 0 {
		config.GateThreshold = defaultGateThreshold
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
