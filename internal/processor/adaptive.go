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
	highpassBoostModerate   = 10.0  // Hz - added when silence sample shows LF noise
	highpassBoostAggressive = 20.0  // Hz - added for noisy silence sample (> -55 dBFS)

	// Spectral decrease thresholds for LF voice content protection
	spectralDecreaseVeryWarm = -0.08 // Below: very warm voice, needs maximum LF protection
	spectralDecreaseWarm     = -0.05 // Below: warm voice with significant LF body
	spectralDecreaseBalanced = 0.0   // Near zero: balanced voice
	// Above 0: thin voice, highpass safe

	// Spectral skewness threshold for LF emphasis detection
	// Positive skewness indicates energy concentrated in lower frequencies
	// Used as secondary protection when spectral decrease alone doesn't catch warm voices
	spectralSkewnessLFEmphasis = 1.0 // Above: significant LF emphasis, needs gentle HPF

	// Silence sample noise floor thresholds for highpass boost decision
	silenceNoiseFloorClean = -70.0 // dBFS - very clean, no boost needed
	silenceNoiseFloorNoisy = -55.0 // dBFS - noisy, may need boost

	// Silence entropy threshold for noise character
	silenceEntropyTonal = 0.5 // Below: tonal noise (hum), bandreject better than highpass

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

	// Gate threshold safety bounds (applied after data-driven calculation)
	gateThresholdMinDB = -70.0 // dB - professional studio floor
	gateThresholdMaxDB = -25.0 // dB - never gate above this (would cut speech)

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

	// Bleed gate parameters
	// Catches bleed/crosstalk that was amplified by speechnorm/dynaudnorm
	// Threshold is calculated from: predicted_output_bleed = silence_peak_level + worst_case_gain
	bleedGateMarginDB          = 6.0   // dB above predicted bleed to set threshold (safety margin)
	bleedGateEnableThresholdDB = -40.0 // dBFS - only enable if predicted output bleed is above this
	bleedGateMinThresholdDB    = -50.0 // dBFS - minimum threshold (never gate below this)
	bleedGateMaxThresholdDB    = -20.0 // dBFS - maximum threshold (never gate above this, would cut speech)
	bleedGateDefaultRatio      = 4.0   // Gentler than pre-gate (suppress rather than cut)
	bleedGateDefaultAttack     = 15.0  // ms - faster than pre-gate
	bleedGateDefaultRelease    = 200.0 // ms - smooth release
	bleedGateDefaultRange      = 0.125 // -18dB reduction (less aggressive than pre-gate)
	bleedGateDefaultKnee       = 3.0   // Soft knee

	// Mains hum filter parameters
	humEntropyThreshold   = 0.7  // Below this = tonal noise detected (hum/buzz)
	humFreq50Hz           = 50.0 // UK/EU mains fundamental frequency
	humFreq60Hz           = 60.0 // US mains fundamental frequency (TODO: make configurable)
	humDefaultHarmonics   = 4    // Filter fundamental + 3 harmonics (50, 100, 150, 200 Hz)
	humWarmVoiceHarmonics = 2    // For warm voices: fundamental + 1 harmonic (50, 100 Hz)
	humDefaultWidth       = 1.0  // Hz - default notch width (1Hz wide at each harmonic)
	humWideWidth          = 2.0  // Hz - wider notch for stronger hum (more aggressive)
	humNarrowWidth        = 0.5  // Hz - narrower notch for pure tonal hum (more surgical)
	humWarmVoiceWidth     = 0.5  // Hz - narrow notch for warm voices
	humVeryWarmVoiceWidth = 0.3  // Hz - very narrow notch for very warm voices (safe with 2 harmonics)
	humMixDefault         = 1.0  // Full wet signal (100% filtered)
	humMixWarmVoice       = 0.8  // Reduced mix for warm voices (80% filtered, 20% dry)
	humMixVeryWarmVoice   = 0.7  // Further reduced for very warm voices (70% filtered, 30% dry)
	// Voice protection thresholds - reduce harmonics when voice has strong LF content
	humSkewnessWarm     = 1.0   // Above this = warm voice, reduce harmonics to protect fundamentals
	humDecreaseWarm     = -0.02 // Below this = warm voice, reduce harmonics
	humDecreaseVeryWarm = -0.1  // Below this = very warm voice (e.g., deep male), extra protection

	// RNN denoise (arnndn) parameters
	// Primary pass thresholds - enable for moderate noise sources
	arnnDnLufsGapModerate    = 15.0  // dB - LUFS gap triggering primary arnndn
	arnnDnNoiseFloorModerate = -55.0 // dBFS - noise floor triggering primary arnndn
	arnnDnMixDefault         = 0.8   // Mix ratio for arnndn (0.8 = 80% filtered, 20% original)
	// Dual-pass thresholds - enable for high-noise sources (e.g., SM7B with high gain)
	arnnDnLufsGapAggressive    = 25.0  // dB - LUFS gap triggering dual-pass
	arnnDnNoiseFloorAggressive = -45.0 // dBFS - noise floor triggering dual-pass
	arnnDnMix2Default          = 0.7   // Reduced mix for second pass (artifact reduction)

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
	defaultHumFrequency   = 50.0 // UK mains
	defaultHumHarmonics   = 4
	defaultHumWidth       = 1.0 // Hz
	defaultArnnDnMix2     = 0.7
)

// AdaptConfig tunes all filter parameters based on Pass 1 measurements.
// This is the main entry point for adaptive configuration.
// It updates config in-place based on the audio characteristics measured in analysis.
func AdaptConfig(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Store measurements reference
	config.Measurements = measurements
	config.NoiseFloor = measurements.NoiseFloor

	// Set noise profile path and duration if available (enables precise afftdn sample_noise mode)
	if measurements.NoiseProfile != nil && measurements.NoiseProfile.FilePath != "" {
		config.NoiseProfilePath = measurements.NoiseProfile.FilePath
		config.NoiseProfileDuration = measurements.NoiseProfile.Duration
	}

	// Calculate LUFS gap once - used by multiple tuning functions
	lufsGap := calculateLUFSGap(config.TargetI, measurements.InputI)

	// Tune each filter adaptively based on measurements
	// Order matters: gate threshold calculated BEFORE denoise filters
	tuneHighpassFreq(config, measurements, lufsGap)
	tuneHumFilter(config, measurements) // Notch filter for mains hum (entropy-based)
	tuneNoiseReduction(config, measurements, lufsGap)
	tuneArnndn(config, measurements, lufsGap) // RNN denoise (LUFS gap + noise floor based)
	tuneGateThreshold(config, measurements)   // Gate threshold before denoise in chain
	tuneDeesser(config, measurements)
	tuneCompression(config, measurements)
	tuneDynaudnorm(config)
	tuneSpeechnorm(config, measurements, lufsGap)
	tuneBleedGate(config, measurements, lufsGap) // Bleed gate for amplified bleed/crosstalk

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
// - Spectral decrease (LF voice content - protects warm voices)
// - Silence sample noise floor (actual LF noise level)
// - Silence sample entropy (noise character - tonal vs broadband)
//
// Strategy:
// - Lower centroid (darker voice) → lower cutoff to preserve warmth
// - Higher centroid (brighter voice) → higher cutoff, safe for rumble removal
// - Negative spectral decrease (warm voice) → cap cutoff to protect LF body
// - Tonal noise (low entropy) → don't boost, let bandreject handle hum
// - Only boost cutoff if silence sample shows actual broadband LF noise
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
				boostAmount = highpassBoostAggressive
			case silenceNoiseFloor > silenceNoiseFloorClean:
				// Moderate noise - gentle boost
				shouldBoost = true
				boostAmount = highpassBoostModerate
			}
		}
	}

	// Apply boost if warranted by noise characteristics
	if shouldBoost {
		config.HighpassFreq = baseFreq + boostAmount
	} else {
		config.HighpassFreq = baseFreq
	}

	// Protect warm voices with significant LF body
	// Two independent triggers for disabling highpass:
	// 1. Spectral decrease < -0.08 (very warm voice with strong bass)
	// 2. Spectral skewness > 1.0 (significant LF emphasis/bass character)
	if measurements.SpectralDecrease < spectralDecreaseVeryWarm {
		// Very warm voice (e.g. Popey -0.095, Martin -0.238)
		// Strong bass foundation that any HPF will damage
		config.HighpassEnabled = false
		return
	} else if measurements.SpectralSkewness > spectralSkewnessLFEmphasis {
		// Significant LF emphasis (e.g. Mark: skewness 1.132)
		// Voice has bass character that even gentle HPF removes
		// All three presenters have skewness > 1.0
		config.HighpassEnabled = false
		return
	} else if measurements.SpectralDecrease < spectralDecreaseWarm {
		// Warm voice - cap at default with gentle slope to preserve body
		if config.HighpassFreq > highpassDefaultFreq {
			config.HighpassFreq = highpassDefaultFreq
		}
		config.HighpassPoles = 1 // Gentle 6dB/oct slope
	}

	// Final cap at maximum to avoid affecting voice fundamentals
	if config.HighpassFreq > highpassMaxFreq {
		config.HighpassFreq = highpassMaxFreq
	}
}

// tuneNoiseReduction adapts FFT noise reduction based on measurements.
//
// Key insight: If we apply 30dB of gain later (via speechnorm/dynaudnorm),
// we need to remove 30dB of noise NOW, or it will be amplified with speech.
//
// Data-driven strategy using NoiseReductionHeadroom:
// - NoiseReductionHeadroom = gap between RMS level (speech) and noise floor
// - Larger headroom means cleaner recording = more aggressive NR is safe
// - Smaller headroom means noise is close to speech = be conservative
//
// Combined approach:
// 1. Use LUFS gap to determine how much gain will be applied later
// 2. Scale by headroom factor: high headroom allows more reduction
// 3. Clamp to 6-40dB (afftdn stability limits)
func tuneNoiseReduction(config *FilterChainConfig, measurements *AudioMeasurements, lufsGap float64) {
	if measurements.InputI == 0.0 {
		// Fallback if no LUFS measurement
		config.NoiseReduction = noiseReductionBase
		return
	}

	// Start with base reduction plus LUFS gap (the gain we'll apply later)
	adaptiveReduction := noiseReductionBase + lufsGap

	// Adjust based on noise reduction headroom (data-driven)
	// This tells us how much "room" we have between speech and noise
	if measurements.NoiseReductionHeadroom > 0 {
		// Scale factor based on headroom:
		// - Headroom < 15dB: noisy recording, reduce NR intensity (scale 0.7)
		// - Headroom 15-30dB: typical, use calculated value (scale 1.0)
		// - Headroom > 30dB: clean recording, can be more aggressive (scale 1.2)
		var headroomScale float64
		switch {
		case measurements.NoiseReductionHeadroom < 15.0:
			// Noisy recording - be conservative to avoid speech artifacts
			headroomScale = 0.7
		case measurements.NoiseReductionHeadroom < 30.0:
			// Typical recording - use calculated value
			headroomScale = 1.0
		default:
			// Clean recording - can be more aggressive
			headroomScale = 1.2
		}
		adaptiveReduction *= headroomScale
	}

	// Clamp to reasonable limits (afftdn stability)
	config.NoiseReduction = clamp(adaptiveReduction, noiseReductionMin, noiseReductionMax)
}

// tuneHumFilter adapts bandreject (notch) filter for mains hum removal.
//
// Strategy:
//   - Uses NoiseProfile.Entropy from Pass 1 to detect tonal noise (hum)
//   - Low entropy (< 0.7) indicates periodic/tonal noise → enable hum removal
//   - High entropy indicates broadband noise → skip notch filter (use afftdn instead)
//   - Applies notch at fundamental (50Hz default) plus harmonics
//   - Voice-aware: reduces harmonics for warm/bassy voices to protect vocal fundamentals
//     The 3rd harmonic (150Hz) and 4th harmonic (200Hz) overlap male vocal fundamentals,
//     causing a "hollow" or "metal bath" sound if filtered on warm voices.
//
// The entropy is calculated from the extracted silence sample during analysis.
// Pure tones have low entropy; random noise has high entropy.
func tuneHumFilter(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Respect user's intent: if filter is disabled, don't touch it
	if !config.HumFilterEnabled {
		return
	}

	// Check if we have noise profile with entropy measurement
	if measurements.NoiseProfile == nil {
		config.HumFilterEnabled = false
		return
	}

	// Low entropy indicates tonal/periodic noise (likely mains hum)
	if measurements.NoiseProfile.Entropy >= humEntropyThreshold {
		// High entropy = broadband noise, notch filter won't help
		config.HumFilterEnabled = false
		return
	}

	// Filter enabled - tune parameters based on voice characteristics
	config.HumFrequency = humFreq50Hz // Default to 50Hz (UK/EU mains)

	// Determine harmonic count based on voice characteristics
	// Warm/bassy voices need fewer harmonics to avoid cutting into vocal fundamentals
	isWarmVoice := measurements.SpectralSkewness > humSkewnessWarm ||
		measurements.SpectralDecrease < humDecreaseWarm

	if isWarmVoice {
		// Warm voice: only filter fundamental + 1 harmonic (50Hz, 100Hz)
		// Avoids 150Hz and 200Hz which overlap male vocal fundamentals
		config.HumHarmonics = humWarmVoiceHarmonics

		// Adjust width and mix based on how warm the voice is
		// Very warm voices (decrease < -0.1) get narrower notch and more dry signal
		if measurements.SpectralDecrease < humDecreaseVeryWarm {
			config.HumWidth = humVeryWarmVoiceWidth // 0.3Hz - very surgical
			config.HumMix = humMixVeryWarmVoice     // 70% wet
		} else {
			config.HumWidth = humWarmVoiceWidth // 0.5Hz
			config.HumMix = humMixWarmVoice     // 80% wet
		}
	} else {
		// Brighter voice: safe to filter more harmonics, full wet
		config.HumHarmonics = humDefaultHarmonics
		config.HumMix = humMixDefault

		// Adaptive width based on noise severity (only for non-warm voices)
		// Warm voices always use humWarmVoiceWidth for maximum protection
		// Lower entropy = more tonal/pure hum = can use narrower notch
		// Higher entropy (but still below threshold) = mixed noise = use wider notch
		if measurements.NoiseProfile.Entropy < 0.3 {
			// Very tonal hum - use narrow surgical notch
			config.HumWidth = humNarrowWidth
		} else if measurements.NoiseProfile.Entropy > 0.5 {
			// Borderline tonal - use wider notch to catch it
			config.HumWidth = humWideWidth
		} else {
			// Standard case
			config.HumWidth = humDefaultWidth
		}
	}
}

// tuneArnndn adapts RNN-based noise reduction based on measurements.
//
// Strategy:
// - Uses cb.rnnn model exclusively (optimised for speech/voice)
// - Dual-pass enabled for heavily degraded sources (high LUFS gap + high noise floor)
// - Mix adjusted based on noise floor: noisier sources get stronger processing
// - Second pass uses reduced mix (0.7 default) to avoid over-processing
//
// Thresholds:
// - Moderate: LUFS gap >15dB OR noise floor >-55dBFS → enable arnndn
// - Aggressive (dual-pass): LUFS gap >25dB AND noise floor >-45dBFS
func tuneArnndn(config *FilterChainConfig, measurements *AudioMeasurements, lufsGap float64) {
	// Respect user's intent: if filter is disabled, don't touch it
	if !config.ArnnDnEnabled {
		return
	}

	// Check if we need RNN denoising based on measurements
	needsModerateNR := lufsGap > arnnDnLufsGapModerate || measurements.NoiseFloor > arnnDnNoiseFloorModerate
	needsAggressiveNR := lufsGap > arnnDnLufsGapAggressive && measurements.NoiseFloor > arnnDnNoiseFloorAggressive

	if !needsModerateNR {
		// Clean source - disable arnndn entirely
		config.ArnnDnEnabled = false
		config.ArnnDnDualPass = false
		return
	}

	// Filter stays enabled, tune parameters

	// Set mix based on noise severity
	// Higher noise floor → stronger RNN processing (higher mix)
	if measurements.NoiseFloor > arnnDnNoiseFloorAggressive {
		config.ArnnDnMix = 0.95 // Very noisy - almost full RNN
	} else if measurements.NoiseFloor > arnnDnNoiseFloorModerate {
		config.ArnnDnMix = 0.85 // Moderately noisy
	} else {
		config.ArnnDnMix = arnnDnMixDefault // Default 0.8
	}

	// Enable dual-pass for heavily degraded sources
	if needsAggressiveNR {
		config.ArnnDnDualPass = true
		config.ArnnDnMix2 = arnnDnMix2Default // Reduced mix for second pass
	} else {
		config.ArnnDnDualPass = false
	}
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

// tuneGateThreshold adapts noise gate based on pre-calculated threshold from Pass 1.
//
// The SuggestedGateThreshold is calculated during analysis using actual measurements:
// - Noise floor (measured from silence regions or RMS trough)
// - Quiet speech level (RMS trough - quietest segments with speech)
// - The threshold is placed adaptively between noise and quiet speech
//
// This function applies safety bounds for extreme cases.
func tuneGateThreshold(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Use the data-driven threshold calculated during Pass 1 analysis
	// SuggestedGateThreshold is already in linear amplitude
	if measurements.SuggestedGateThreshold > 0 {
		config.GateThreshold = measurements.SuggestedGateThreshold
	} else {
		// Fallback if SuggestedGateThreshold not available (shouldn't happen)
		// Use a conservative threshold: noise floor + 6dB
		gateThresholdDB := measurements.NoiseFloor + 6.0
		config.GateThreshold = dbToLinear(gateThresholdDB)
	}

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

// tuneSpeechnormDenoise enables RNN denoise for heavily expanded audio.
// Only takes effect if ArnnDnEnabled is already true (respects user config).
func tuneSpeechnormDenoise(config *FilterChainConfig, expansion float64) {
	// Respect user's intent: if filter is disabled, don't touch it
	if !config.ArnnDnEnabled {
		return
	}

	if expansion >= speechnormExpansionThreshold {
		// Filter stays enabled, tune parameters
		config.ArnnDnMix = arnnDnMixDefault
	} else {
		config.ArnnDnEnabled = false
	}
}

// tuneBleedGate adapts the bleed gate based on predicted output bleed level.
//
// The bleed gate catches bleed/crosstalk that was amplified by speechnorm/dynaudnorm
// after the denoisers have run. Denoisers preserve speech-like content (which is what
// they're designed to do), but headphone bleed IS speech-like content from other speakers.
//
// Strategy:
// - Use silence sample PEAK level (not noise floor) - captures bleed bursts, not just hiss
// - Calculate worst-case gain: speechnorm normalises each half-cycle to peak target
// - For silence with bleed, this can mean 40-50dB of gain applied
// - Use crest factor to detect presence of bleed (high crest = impulsive content in silence)
// - Adjust ratio/range based on how much bleed is detected
//
// Key insight: Speechnorm applies VARIABLE gain per half-cycle. For quiet sections
// (like silence with bleed), it applies much more gain than the "expansion" factor
// suggests. The actual gain on silence can be:
//
//	silence_input_peak → target_peak (0.95 = -0.45 dBFS)
//
// Measurements used:
// - NoiseProfile.PeakLevel: captures the loudest bleed burst
// - NoiseProfile.MeasuredNoiseFloor: captures the background hiss
// - NoiseProfile.CrestFactor: high crest = impulsive content (bleed), low = steady hiss
// - NoiseProfile.Entropy: low entropy = tonal (hum), high = broadband (hiss/bleed)
func tuneBleedGate(config *FilterChainConfig, measurements *AudioMeasurements, lufsGap float64) {
	// Respect user's intent: if filter is disabled, don't touch it
	if !config.BleedGateEnabled {
		return
	}

	// Need noise profile with measurements to calculate threshold
	if measurements.NoiseProfile == nil {
		config.BleedGateEnabled = false
		return
	}

	np := measurements.NoiseProfile

	// Calculate worst-case gain: speechnorm can apply gain to bring quiet content to peak
	// The target peak is typically 0.95 (-0.45 dBFS)
	targetPeakDB := -0.45 // 20 * log10(0.95)
	if config.SpeechnormEnabled && config.SpeechnormPeak > 0 {
		targetPeakDB = 20.0 * math.Log10(config.SpeechnormPeak)
	}

	// Worst-case gain = what's needed to bring silence peak to target peak
	// This is the maximum gain speechnorm could apply to the silence content
	worstCaseGainDB := targetPeakDB - np.PeakLevel

	// Calculate predicted output level for the silence PEAK (the bleed content)
	predictedOutputPeakDB := np.PeakLevel + worstCaseGainDB

	// Calculate predicted output noise floor
	predictedOutputNoiseDB := np.MeasuredNoiseFloor + worstCaseGainDB

	// Detect bleed presence using crest factor and peak-to-floor ratio
	// Crest factor = peak - RMS; high crest means impulsive content in silence
	// For pure hiss, crest factor is ~10-12dB; for bleed it's typically 20-30dB
	peakToFloorDB := np.PeakLevel - np.MeasuredNoiseFloor
	hasSignificantBleed := np.CrestFactor > 15.0 || peakToFloorDB > 20.0

	// Determine threshold strategy based on bleed detection
	var thresholdDB float64
	if hasSignificantBleed {
		// Bleed detected - use peak-based threshold (more aggressive)
		// Set threshold to catch the amplified bleed peaks
		thresholdDB = predictedOutputPeakDB - 3.0 // 3dB below predicted peak
	} else {
		// No significant bleed - use noise floor based threshold (standard approach)
		thresholdDB = predictedOutputNoiseDB + bleedGateMarginDB
	}

	// Only enable bleed gate if predicted output would be audible
	if thresholdDB < bleedGateEnableThresholdDB {
		config.BleedGateEnabled = false
		return
	}

	// Enable bleed gate
	config.BleedGateEnabled = true

	// Clamp threshold to safety limits
	thresholdDB = clamp(thresholdDB, bleedGateMinThresholdDB, bleedGateMaxThresholdDB)

	// Convert to linear for agate filter
	config.BleedGateThreshold = dbToLinear(thresholdDB)

	// Adapt ratio and range based on bleed severity
	if hasSignificantBleed {
		// Significant bleed - use stronger settings
		config.BleedGateRatio = 6.0   // Stronger ratio for bleed
		config.BleedGateRange = 0.063 // -24dB reduction (more aggressive)
		config.BleedGateAttack = 10.0 // Faster attack to catch bleed transients
		config.BleedGateRelease = 150.0
	} else {
		// Mild bleed or just noise amplification - use gentler settings
		config.BleedGateRatio = bleedGateDefaultRatio
		config.BleedGateRange = bleedGateDefaultRange
		config.BleedGateAttack = bleedGateDefaultAttack
		config.BleedGateRelease = bleedGateDefaultRelease
	}

	config.BleedGateKnee = bleedGateDefaultKnee
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

	// Hum filter sanitization
	config.HumFrequency = sanitizeFloat(config.HumFrequency, defaultHumFrequency)
	config.HumWidth = sanitizeFloat(config.HumWidth, defaultHumWidth)
	if config.HumHarmonics < 1 || config.HumHarmonics > 8 {
		config.HumHarmonics = defaultHumHarmonics
	}

	// ArnnDn second pass mix sanitization
	config.ArnnDnMix2 = sanitizeFloat(config.ArnnDnMix2, defaultArnnDnMix2)

	// BleedGateThreshold needs additional check for zero/negative (like pre-gate)
	if math.IsNaN(config.BleedGateThreshold) || math.IsInf(config.BleedGateThreshold, 0) || config.BleedGateThreshold <= 0 {
		config.BleedGateThreshold = defaultGateThreshold // Use same default as pre-gate
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
