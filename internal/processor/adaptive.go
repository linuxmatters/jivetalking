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

	// Highpass warm voice protection parameters
	// Instead of disabling highpass for warm voices, we use gentler settings
	highpassWarmFreq      = 70.0 // Hz - slightly reduced cutoff for warm voices
	highpassVeryWarmFreq  = 60.0 // Hz - minimum cutoff for very warm voices
	highpassWarmWidth     = 0.5  // Q - gentler rolloff than Butterworth (0.707)
	highpassVeryWarmWidth = 0.5  // Q - gentler rolloff for very warm voices
	highpassWarmMix       = 0.9  // Wet/dry mix for warm voices (90% filtered)
	highpassVeryWarmMix   = 0.8  // Wet/dry mix for very warm voices (80% filtered)

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

	// Sample-free noise reduction (afftdn_simple) parameters
	// Conservative limits to avoid voice degradation without noise profile guidance
	afftdnSimpleBase             = 8.0   // dB - conservative baseline
	afftdnSimpleMin              = 6.0   // dB - minimum useful reduction
	afftdnSimpleMax              = 10.0  // dB - conservative ceiling to avoid metallic artifacts
	afftdnSimpleCleanFloorThresh = -75.0 // dBFS - below this, source is clean enough to skip

	// De-esser intensity levels
	deessIntensityBright = 0.6 // Bright voice base intensity
	deessIntensityNormal = 0.5 // Normal voice base intensity
	deessIntensityDark   = 0.4 // Dark voice base intensity
	deessIntensityMax    = 0.8 // Maximum intensity limit
	deessIntensityMin    = 0.3 // Minimum before disabling

	// Gate tuning constants
	// Threshold calculation: sits above noise/bleed peaks, below quiet speech
	gateThresholdMinDB       = -70.0 // dB - professional studio floor
	gateThresholdMaxDB       = -25.0 // dB - never gate above this (would cut speech)
	gateCrestFactorThreshold = 20.0  // dB - above this, use peak reference instead of RMS
	gateHeadroomClean        = 3.0   // dB - headroom above reference for clean recordings
	gateHeadroomModerate     = 6.0   // dB - headroom for moderate noise
	gateHeadroomNoisy        = 10.0  // dB - headroom for noisy recordings

	// Ratio: based on LRA (loudness range)
	gateLRAWide     = 15.0 // LU - above: wide dynamics, gentle ratio
	gateLRAModerate = 10.0 // LU - above: moderate dynamics
	gateRatioGentle = 1.5  // For wide LRA (preserve expression)
	gateRatioMod    = 2.0  // For moderate LRA
	gateRatioTight  = 2.5  // For narrow LRA (tighter control OK)

	// Attack: based on MaxDifference (transient indicator)
	// Fast transients need fast attack to avoid clipping word onsets
	gateMaxDiffHigh      = 25.0 // % - sharp transients
	gateMaxDiffMod       = 10.0 // % - moderate transients
	gateAttackFast       = 7.0  // ms - for sharp transients
	gateAttackMod        = 12.0 // ms - standard speech
	gateAttackSlow       = 17.0 // ms - soft onsets
	gateFluxDynamicThres = 0.05 // SpectralFlux threshold for dynamic content

	// Release: based on flux, ZCR, and noise character
	// No hold parameter exists - release must compensate
	gateFluxLow          = 0.01 // Low flux threshold
	gateZCRLow           = 0.08 // Low zero crossings rate
	gateFluxHigh         = 0.05 // High flux threshold
	gateReleaseSustained = 400  // ms - for sustained speech
	gateReleaseMod       = 300  // ms - standard
	gateReleaseDynamic   = 200  // ms - for dynamic content
	gateReleaseHoldComp  = 50   // ms - compensation for lack of hold parameter
	gateReleaseTonalComp = 75   // ms - extra for tonal bleed (hide pump)
	gateReleaseMin       = 150  // ms - minimum release
	gateReleaseMax       = 500  // ms - maximum release

	// Range: based on silence entropy and noise floor
	// Tonal noise sounds worse when hard-gated - gentler range hides pumping
	gateEntropyTonal     = 0.3 // Below: tonal noise (bleed/hum)
	gateEntropyMixed     = 0.6 // Below: mixed noise
	gateRangeTonalDB     = -16 // dB - gentle for tonal noise
	gateRangeMixedDB     = -21 // dB - moderate for mixed
	gateRangeBroadbandDB = -27 // dB - aggressive for broadband
	gateRangeCleanBoost  = -6  // dB - extra depth for very clean
	gateRangeMinDB       = -36 // dB - minimum (deepest)
	gateRangeMaxDB       = -12 // dB - maximum (gentlest)

	// Knee: based on spectral crest
	gateSpectralCrestHigh = 35.0 // High crest threshold
	gateSpectralCrestMod  = 20.0 // Moderate crest threshold
	gateKneeSoft          = 5.0  // For dynamic content with prominent peaks
	gateKneeMod           = 3.0  // Standard
	gateKneeSharp         = 2.0  // For less dynamic content

	// Detection: based on silence entropy and crest factor
	gateSilenceCrestThreshold = 25.0 // dB - above: use RMS (noise has spikes)
	gateEntropyClean          = 0.7  // Above: can use peak detection

	// Noise floor quality thresholds
	noiseFloorClean   = -60.0 // dBFS - very clean recording
	noiseFloorTypical = -50.0 // dBFS - typical podcast
	noiseFloorNoisy   = -40.0 // dBFS - noisy recording (for compression mix)

	// ==========================================================================
	// LA-2A-Inspired Compression Parameters
	// ==========================================================================
	// The Teletronix LA-2A is an optical tube compressor renowned for its gentle,
	// program-dependent character. Key characteristics:
	// - Fixed 10ms attack (preserves transients, "pluck" of consonants)
	// - Two-stage release: 60ms initial (50%), then 1-15s for full release
	// - Soft variable ratio (~3:1) that adapts to signal strength
	// - Very soft knee from the T4 optical cell
	// - Tube warmth that "fattens" low-mids
	//
	// We approximate this behaviour using available spectral measurements.
	// ==========================================================================

	// LA-2A Attack: Fixed ~10ms baseline (preserves transients)
	// Slight variation based on MaxDifference (transient sharpness indicator)
	la2aAttackBase   = 10.0 // ms - LA-2A fixed attack time
	la2aAttackFast   = 8.0  // ms - for very sharp transients (catch peaks)
	la2aAttackSlow   = 12.0 // ms - for soft delivery (even gentler)
	la2aMaxDiffSharp = 0.25 // MaxDifference > 25% = sharp transients
	la2aMaxDiffSoft  = 0.10 // MaxDifference < 10% = soft delivery

	// LA-2A Release: Two-stage program-dependent approximation
	// Real LA-2A: 60ms to 50% release, then 1-15s for full release
	// We approximate with longer releases for expressive content
	la2aReleaseExpressive = 300.0 // ms - wide LRA + high flux (expressive speech)
	la2aReleaseStandard   = 200.0 // ms - typical podcast delivery
	la2aReleaseCompact    = 150.0 // ms - narrow LRA + low flux (compressed)
	la2aReleaseHeavyBoost = 50.0  // ms - added when heavy compression needed
	la2aFluxDynamic       = 0.025 // SpectralFlux above = dynamic/expressive
	la2aFluxStatic        = 0.008 // SpectralFlux below = static/monotone
	la2aLRAExpressive     = 14.0  // LU - above = expressive delivery
	la2aLRACompact        = 8.0   // LU - below = compressed/monotone

	// LA-2A Ratio: Soft ~3:1 baseline (T4 optical cell is program-dependent)
	// Real LA-2A varies ratio based on signal strength - we use kurtosis
	// High kurtosis = peaked harmonics (preserve character with lower ratio)
	// Low kurtosis = flat spectrum (more consistent levelling OK)
	la2aRatioBase         = 3.0  // Baseline LA-2A ratio (Compress mode)
	la2aRatioPeaked       = 2.5  // For highly peaked/tonal content
	la2aRatioFlat         = 3.5  // For flat/noise-like content
	la2aRatioDynamicBoost = 0.5  // Added for very wide dynamic range
	la2aKurtosisHighPeak  = 10.0 // Above: peaked harmonics, gentler ratio
	la2aKurtosisLowPeak   = 5.0  // Below: flat spectrum, firmer ratio
	la2aDynamicRangeWide  = 35.0 // dB - above: add ratio boost

	// LA-2A Threshold: Relative to peak level (like Peak Reduction knob)
	// LA-2A's threshold is effectively signal-relative
	// Headroom from peak level determines compression depth
	// More headroom = lower threshold = more compression
	la2aThresholdHeadroomLight = 10.0  // dB - light levelling (peaks only)
	la2aThresholdHeadroomStd   = 15.0  // dB - standard LA-2A levelling
	la2aThresholdHeadroomHeavy = 20.0  // dB - heavy levelling (aggressive control)
	la2aThresholdMin           = -40.0 // dB - minimum threshold (safety floor for very quiet)
	la2aThresholdMax           = -12.0 // dB - maximum threshold (gentle ceiling)
	la2aDynamicRangeHigh       = 30.0  // dB - above: heavy threshold
	la2aDynamicRangeMod        = 20.0  // dB - above: standard threshold

	// LA-2A Knee: Very soft (T4 optical cell provides inherent soft knee)
	// Adapt based on voice character (spectral centroid)
	la2aKneeDark       = 5.0    // For dark/warm voices (preserve warmth)
	la2aKneeNormal     = 4.0    // Standard LA-2A approximation
	la2aKneeBright     = 3.5    // For bright voices (slightly firmer)
	la2aCentroidDark   = 4000.0 // Hz - below: dark voice
	la2aCentroidBright = 6000.0 // Hz - above: bright voice

	// LA-2A Skewness adaptation (bass-concentrated voices get extra warmth)
	// Negative skewness = energy concentrated in bass (warm voice)
	la2aSkewnessWarm     = 1.5  // Above: warm/bass-heavy voice
	la2aKneeWarmBoost    = 0.5  // Added to knee for warm voices
	la2aReleaseWarmBoost = 30.0 // ms added for warm voices (preserve body)

	// LA-2A Mix: Real LA-2A is 100% wet (no parallel compression)
	// We allow slight dry signal for problematic recordings to mask artefacts
	la2aMixClean        = 1.0   // Very clean recordings (true LA-2A)
	la2aMixModerate     = 0.93  // Moderate noise (slight dry masks artefacts)
	la2aMixNoisy        = 0.85  // Noisy recordings (more dry hides pumping)
	la2aNoiseFloorClean = -65.0 // dBFS - below: clean enough for full wet
	la2aNoiseFloorNoisy = -45.0 // dBFS - above: noisy, reduce wet

	// LA-2A Makeup Gain: Compensate for gain reduction
	// Calculate from expected reduction, but be conservative
	la2aMakeupMultiplier = 0.65 // Conservative (let normalisation handle rest)
	la2aMakeupMin        = 1.0  // dB minimum makeup
	la2aMakeupMax        = 5.0  // dB maximum makeup (avoid over-driving)

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
	// Mix baseline by noise floor severity (conservative approach)
	arnnDnMixNoisy    = 0.7  // Noise floor > -50 dBFS: noisy source, aggressive cleaning
	arnnDnMixModerate = 0.5  // Noise floor -50 to -65 dBFS: moderate noise
	arnnDnMixClean    = 0.35 // Noise floor -65 to -75 dBFS: fairly clean
	arnnDnMixVClean   = 0.25 // Noise floor < -75 dBFS: very clean, gentle touch only

	// Enable/disable thresholds
	arnnDnDisableNoiseFloor = -80.0 // dBFS - extremely clean source, disable entirely
	arnnDnDisableFlatness   = 0.3   // Silence flatness threshold for disable decision
	arnnDnMinMix            = 0.10  // Below this mix, disable filter entirely

	// Mix adjustment thresholds
	arnnDnKurtosisThreshold    = 8.0  // Above: peaked harmonics reveal artifacts, reduce mix
	arnnDnMaxDiffThreshold     = 0.25 // Above (25%): sharp transients, preserve attack
	arnnDnLRAThreshold         = 15.0 // Above: wide dynamics expose artifacts in quiet passages
	arnnDnSilenceFlatThreshold = 0.5  // Above: broadband noise likely, increase mix
	arnnDnSilenceEntThreshold  = 0.5  // Above: random noise, RNN handles well, increase mix

	// Mix adjustment amounts
	arnnDnKurtosisAdjust = -0.1  // Reduce mix for peaked harmonics
	arnnDnMaxDiffAdjust  = -0.1  // Reduce mix for sharp transients
	arnnDnLRAAdjust      = -0.05 // Reduce mix for wide dynamics
	arnnDnFlatnessAdjust = 0.1   // Increase mix for broadband noise
	arnnDnEntropyAdjust  = 0.1   // Increase mix for random noise

	// afftdn interaction adjustments
	arnnDnAfftdnGentleAdjust = -0.1 // Reduce mix when afftdn is doing primary denoising
	arnnDnAfftdnAggressAdj   = -0.2 // Further reduce when afftdn is aggressive
	arnnDnAfftdnAggressThres = 20.0 // dB - afftdn reduction above this is "aggressive"

	// Mix limits
	arnnDnMixMin = 0.1 // Minimum mix (below this, filter has negligible effect)
	arnnDnMixMax = 0.8 // Maximum mix (above risks artifacts)

	// LUFS to RMS conversion constant
	// Rough conversion: LUFS ≈ -23 + 20*log10(RMS)
	lufsRmsOffset = 23.0

	// Default fallback values for sanitization
	defaultHighpassFreq   = 80.0
	defaultDeessIntensity = 0.0
	defaultNoiseReduction = 12.0
	defaultLA2ARatio      = 3.0   // LA-2A baseline ratio
	defaultLA2AThreshold  = -18.0 // Moderate threshold
	defaultLA2AMakeup     = 2.0   // Conservative makeup
	defaultLA2AAttack     = 10.0  // LA-2A fixed attack
	defaultLA2ARelease    = 200.0 // LA-2A two-stage release approximation
	defaultLA2AKnee       = 4.0   // LA-2A T4 optical cell soft knee
	defaultGateThreshold  = 0.01  // -40dBFS
	defaultHumFrequency   = 50.0  // UK mains
	defaultHumHarmonics   = 4
	defaultHumWidth       = 1.0 // Hz
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
	tuneAfftdnSimple(config, measurements, lufsGap) // Sample-free FFT denoise
	tuneArnndn(config, measurements, lufsGap)       // RNN denoise (LUFS gap + noise floor based)
	tuneGateThreshold(config, measurements)         // Gate threshold before denoise in chain
	tuneDeesser(config, measurements)
	tuneLA2ACompressor(config, measurements)
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

	// Apply boost if warranted by noise characteristics (only for non-warm voices)
	if shouldBoost {
		config.HighpassFreq = baseFreq + boostAmount
	} else {
		config.HighpassFreq = baseFreq
	}

	// Set TDII transform for all highpass (best floating-point accuracy)
	config.HighpassTransform = "tdii"

	// Protect warm voices with significant LF body
	// Instead of disabling highpass, we use gentler settings:
	// - Lower frequency (subsonic only)
	// - Lower Q (gentler rolloff)
	// - Reduced mix (blend filtered with dry signal)
	//
	// This removes subsonic rumble while preserving bass character.
	if measurements.SpectralDecrease < spectralDecreaseVeryWarm {
		// Very warm voice (e.g. Popey -0.095, Martin -0.238)
		// Use minimal settings: 30Hz cutoff, gentle Q, 50% mix
		config.HighpassFreq = highpassVeryWarmFreq
		config.HighpassWidth = highpassVeryWarmWidth
		config.HighpassMix = highpassVeryWarmMix
		config.HighpassPoles = 1 // Gentle 6dB/oct slope
		return
	} else if measurements.SpectralSkewness > spectralSkewnessLFEmphasis {
		// Significant LF emphasis (e.g. Mark: skewness 1.132)
		// Use warm settings: 40Hz cutoff, gentle Q, 70% mix
		config.HighpassFreq = highpassWarmFreq
		config.HighpassWidth = highpassWarmWidth
		config.HighpassMix = highpassWarmMix
		config.HighpassPoles = 1 // Gentle 6dB/oct slope
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

// tuneAfftdnSimple adapts the sample-free FFT noise reduction filter.
//
// This filter operates without a noise sample, using a white noise model (nt=w).
// Because it can't precisely match the actual noise profile, it uses conservative
// settings to avoid voice degradation.
//
// Strategy:
// - Base reduction from LUFS gap (how much gain will be applied later)
// - Conservative cap at 15dB (vs 40dB for profile-based afftdn)
// - If we're applying 20dB of gain later, noise needs reduction NOW
// - But without precise profile, we cap at 15dB to protect voice quality
//
// Parameters set by this function:
// - AfftdnSimpleNoiseFloor: from Pass 1 measurements
// - AfftdnSimpleNoiseReduction: conservative calculation (6-10dB)
// - AfftdnSimpleNoiseType: selected based on spectral characteristics
func tuneAfftdnSimple(config *FilterChainConfig, measurements *AudioMeasurements, lufsGap float64) {
	if !config.AfftdnSimpleEnabled {
		return
	}

	// Disable for clean sources — they don't need sample-free denoising
	// and the imprecise noise model risks introducing artifacts
	if measurements.NoiseFloor < afftdnSimpleCleanFloorThresh {
		config.AfftdnSimpleEnabled = false
		return
	}

	// Set noise floor from measurements
	config.AfftdnSimpleNoiseFloor = measurements.NoiseFloor

	// Select noise type based on spectral characteristics
	config.AfftdnSimpleNoiseType = selectAfftdnNoiseType(measurements)

	// Calculate noise reduction based on LUFS gap
	// Logic: if we're going to apply X dB of gain, we should reduce noise by
	// approximately that amount so it doesn't get amplified. But without a
	// precise noise profile, we cap conservatively.
	adaptiveReduction := afftdnSimpleBase

	// Add reduction proportional to LUFS gap (the gain we'll apply later)
	// Scale very conservatively (0.3x) to avoid metallic artifacts
	if lufsGap > 0 {
		adaptiveReduction += lufsGap * 0.3
	}

	// Clamp to conservative limits (max 10dB without a proper noise profile)
	config.AfftdnSimpleNoiseReduction = clamp(adaptiveReduction, afftdnSimpleMin, afftdnSimpleMax)
}

// selectAfftdnNoiseType chooses the optimal noise model based on spectral measurements.
//
// The afftdn filter's noise type (nt) parameter affects how it models the noise profile:
//   - "w" (white): Flat spectrum - best for broadband hiss, HVAC, fan noise
//   - "v" (vinyl): LF-weighted spectrum - best for rumble, hum, tonal LF noise
//   - "s" (shellac): HF-weighted spectrum - best for tape hiss, preamp noise
//
// Selection criteria:
//   - High spectral flatness + high entropy → broadband noise → white
//   - Strong LF emphasis (low decrease, steep negative slope) → rumble/hum → vinyl
//   - High centroid + rolloff → HF-dominant noise → shellac
//   - Otherwise → white (safe default)
func selectAfftdnNoiseType(m *AudioMeasurements) string {
	// High flatness + high entropy = broadband hiss → white
	// Flatness > 0.6 indicates relatively uniform spectral energy
	// Entropy > 0.5 indicates random/noisy rather than tonal content
	if m.SpectralFlatness > 0.6 && m.NoiseProfile != nil && m.NoiseProfile.Entropy > 0.5 {
		return "w"
	}

	// Strong LF emphasis + steep negative slope = rumble + hum → vinyl
	// SpectralDecrease < -0.1 indicates energy concentrated in lower frequencies
	// SpectralSlope < -0.00003 indicates steep high-to-low frequency rolloff
	// This pattern is typical of mains hum, HVAC rumble, and room resonance
	if m.SpectralDecrease < -0.1 && m.SpectralSlope < -0.00003 {
		return "v"
	}

	// High-frequency emphasis = tape hiss / preamp noise → shellac
	// Centroid > 6000 Hz indicates brightness/HF energy
	// Rolloff > 10000 Hz indicates significant content above 10kHz
	// This pattern is typical of analog tape hiss or preamp self-noise
	if m.SpectralCentroid > 6000 && m.SpectralRolloff > 10000 {
		return "s"
	}

	// Safe default: white noise model
	return "w"
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
// - Conservative by default: clean sources auto-disable to avoid artifact risk
// - Mix modulated based on noise floor severity + spectral characteristics
// - Accounts for afftdn interaction: reduce mix when afftdn is doing primary denoising
//
// The gate handles presenter bleed (low-entropy tonal content in silence).
// arnndn targets broadband room tone and noise under speech that the gate can't touch.
//
// Enable decision:
// - Very clean (noise floor < -75dB AND silence flatness < 0.4) → disable entirely
// - Otherwise enable with calculated mix
//
// Mix calculation:
// - Baseline from noise floor severity
// - Adjustments for: kurtosis (harmonics), transients, dynamics, flatness, entropy
// - Reduce when afftdn is active (avoid double-processing)
// - Disable if final mix < 0.15 (negligible effect)
func tuneArnndn(config *FilterChainConfig, measurements *AudioMeasurements, _ float64) {
	// Respect user's intent: if filter is disabled, don't touch it
	if !config.ArnnDnEnabled {
		return
	}

	// Get silence sample flatness for enable decision
	silenceFlatness := 0.0
	silenceEntropy := 0.0
	if measurements.NoiseProfile != nil {
		// NoiseProfile doesn't have flatness directly, but we can use entropy as proxy
		// Low entropy = tonal (bleed), high entropy = broadband (noise)
		silenceEntropy = measurements.NoiseProfile.Entropy
		// Use spectral flatness from main audio as proxy for silence flatness
		// This isn't perfect but SpectralFlatness indicates noise-like content overall
		silenceFlatness = measurements.SpectralFlatness
	}

	// Very clean source with low broadband content → disable entirely
	// Gate handles bleed; arnndn would only add artifact risk
	if measurements.NoiseFloor < arnnDnDisableNoiseFloor && silenceFlatness < arnnDnDisableFlatness {
		config.ArnnDnEnabled = false
		return
	}

	// Calculate mix based on noise floor severity and spectral characteristics
	mix := calculateArnnDnMix(measurements, silenceFlatness, silenceEntropy, config.AfftdnEnabled, config.NoiseReduction)

	// If calculated mix is negligible, disable filter
	if mix < arnnDnMinMix {
		config.ArnnDnEnabled = false
		return
	}

	config.ArnnDnMix = mix
}

// calculateArnnDnMix computes the optimal arnndn mix based on measurements.
// Returns a value between arnnDnMixMin and arnnDnMixMax.
func calculateArnnDnMix(m *AudioMeasurements, silenceFlatness, silenceEntropy float64, afftdnEnabled bool, afftdnReduction float64) float64 {
	// Baseline from noise floor severity
	var baseMix float64
	switch {
	case m.NoiseFloor > -50:
		baseMix = arnnDnMixNoisy // 0.7 - noisy source
	case m.NoiseFloor > -65:
		baseMix = arnnDnMixModerate // 0.5 - moderate noise
	case m.NoiseFloor > -75:
		baseMix = arnnDnMixClean // 0.35 - fairly clean
	default:
		baseMix = arnnDnMixVClean // 0.25 - very clean
	}

	// Adjustment: High kurtosis = peaked harmonics reveal artifacts
	if m.SpectralKurtosis > arnnDnKurtosisThreshold {
		baseMix += arnnDnKurtosisAdjust // -0.1
	}

	// Adjustment: Sharp transients = preserve consonant attacks
	// MaxDifference is in sample units (0-32768 for 16-bit); normalise to 0-1
	maxDiffNorm := m.MaxDifference / 32768.0
	if maxDiffNorm > arnnDnMaxDiffThreshold {
		baseMix += arnnDnMaxDiffAdjust // -0.1
	}

	// Adjustment: Wide dynamics = quiet passages expose warble artifacts
	if m.InputLRA > arnnDnLRAThreshold {
		baseMix += arnnDnLRAAdjust // -0.05
	}

	// Adjustment: High silence flatness = broadband noise likely during speech too
	if silenceFlatness > arnnDnSilenceFlatThreshold {
		baseMix += arnnDnFlatnessAdjust // +0.1
	}

	// Adjustment: High silence entropy = random noise, RNN handles well
	if silenceEntropy > arnnDnSilenceEntThreshold {
		baseMix += arnnDnEntropyAdjust // +0.1
	}

	// Adjustment: When afftdn is doing primary denoising, arnndn only cleans residuals
	if afftdnEnabled {
		if afftdnReduction > arnnDnAfftdnAggressThres {
			baseMix += arnnDnAfftdnAggressAdj // -0.2 for aggressive afftdn
		} else {
			baseMix += arnnDnAfftdnGentleAdjust // -0.1 for gentle afftdn
		}
	}

	return clamp(baseMix, arnnDnMixMin, arnnDnMixMax)
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

// tuneGate adapts all noise gate parameters based on Pass 1 measurements.
//
// Parameters are tuned as follows:
//   - Threshold: above silence peak (if crest > 20dB) or noise floor, with headroom
//   - Ratio: based on LRA (wide dynamics = gentle ratio)
//   - Attack: based on MaxDifference (fast transients = fast attack to avoid clipping onsets)
//   - Release: based on flux/ZCR + hold compensation (no hold param in agate)
//   - Range: based on silence entropy (tonal noise = gentle range to hide pumping)
//   - Knee: based on spectral crest (dynamic content = soft knee)
//   - Detection: RMS for tonal bleed/noisy silence, peak for clean recordings
//   - Makeup: 1.0 (loudness normalisation handles level compensation)
func tuneGate(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Determine if we have tonal noise (likely bleed/hum)
	var tonalNoise bool
	var silenceEntropy, silenceCrest, silencePeak float64

	if measurements.NoiseProfile != nil {
		silenceEntropy = measurements.NoiseProfile.Entropy
		silenceCrest = measurements.NoiseProfile.CrestFactor
		silencePeak = measurements.NoiseProfile.PeakLevel
		tonalNoise = silenceEntropy < gateEntropyTonal
	}

	// 1. Threshold: sits above noise/bleed peaks, below quiet speech
	config.GateThreshold = calculateGateThreshold(
		measurements.NoiseFloor,
		silencePeak,
		silenceCrest,
	)

	// 2. Ratio: based on LRA (loudness range)
	config.GateRatio = calculateGateRatio(measurements.InputLRA)

	// 3. Attack: based on MaxDifference (transient indicator)
	config.GateAttack = calculateGateAttack(
		measurements.MaxDifference,
		measurements.SpectralFlux,
	)

	// 4. Release: based on flux, ZCR, and noise character
	config.GateRelease = calculateGateRelease(
		measurements.SpectralFlux,
		measurements.ZeroCrossingsRate,
		tonalNoise,
	)

	// 5. Range: based on silence entropy and noise floor
	config.GateRange = calculateGateRange(
		silenceEntropy,
		measurements.NoiseFloor,
	)

	// 6. Knee: based on spectral crest
	config.GateKnee = calculateGateKnee(measurements.SpectralCrest)

	// 7. Detection: RMS for bleed, peak for clean
	config.GateDetection = calculateGateDetection(silenceEntropy, silenceCrest)

	// 8. Makeup: 1.0 (loudness normalisation handles it)
	config.GateMakeup = 1.0
}

// calculateGateThreshold determines the gate threshold based on noise characteristics.
// When silence has high crest factor (transient spikes), use peak as reference.
// Otherwise use noise floor. Add headroom based on noise severity.
func calculateGateThreshold(noiseFloorDB, silencePeakDB, silenceCrestDB float64) float64 {
	var referenceDB float64

	// Determine reference level based on crest factor
	if silenceCrestDB > gateCrestFactorThreshold && silencePeakDB != 0 {
		// Noise has transients (e.g., bleed) - use peak as reference
		referenceDB = silencePeakDB
	} else {
		// Stable noise - use floor
		referenceDB = noiseFloorDB
	}

	// Determine headroom based on reference level (higher = more noisy = more headroom)
	var headroomDB float64
	switch {
	case referenceDB < -70:
		// Very clean - tight threshold safe
		headroomDB = gateHeadroomClean
	case referenceDB < -50:
		// Moderate - standard headroom
		headroomDB = gateHeadroomModerate
	default:
		// Noisy - generous headroom to avoid cutting quiet speech
		headroomDB = gateHeadroomNoisy
	}

	thresholdDB := referenceDB + headroomDB

	// Safety limits
	thresholdDB = clamp(thresholdDB, gateThresholdMinDB, gateThresholdMaxDB)

	return dbToLinear(thresholdDB)
}

// calculateGateRatio determines ratio based on LRA (loudness range).
// Wide dynamics = gentle ratio to preserve expression.
func calculateGateRatio(lra float64) float64 {
	switch {
	case lra > gateLRAWide:
		return gateRatioGentle // Wide dynamics - preserve expression
	case lra > gateLRAModerate:
		return gateRatioMod // Moderate dynamics
	default:
		return gateRatioTight // Narrow dynamics - tighter control OK
	}
}

// calculateGateAttack determines attack time based on transient characteristics.
// Fast transients need fast attack to avoid clipping word onsets.
// MaxDifference is expressed as a fraction (0.0-1.0), convert to percentage.
func calculateGateAttack(maxDiff, spectralFlux float64) float64 {
	// MaxDifference is 0.0-1.0 fraction, convert to percentage for comparison
	maxDiffPercent := maxDiff * 100.0

	var baseAttack float64
	switch {
	case maxDiffPercent > gateMaxDiffHigh:
		baseAttack = gateAttackFast // Sharp transients - fast opening
	case maxDiffPercent > gateMaxDiffMod:
		baseAttack = gateAttackMod // Standard speech
	default:
		baseAttack = gateAttackSlow // Soft onsets - gentler OK
	}

	// Bias faster for dynamic content
	if spectralFlux > gateFluxDynamicThres {
		baseAttack *= 0.8
	}

	return clamp(baseAttack, 5.0, 25.0)
}

// calculateGateRelease determines release time based on content and noise character.
// Compensates for lack of hold parameter by extending release.
// Tonal bleed needs slower release to hide the pumping artifact.
func calculateGateRelease(spectralFlux, zcr float64, tonalNoise bool) float64 {
	var baseRelease float64

	switch {
	case spectralFlux < gateFluxLow && zcr < gateZCRLow:
		// Sustained speech with low activity
		baseRelease = gateReleaseSustained
	case spectralFlux > gateFluxHigh:
		// Dynamic content - more responsive
		baseRelease = gateReleaseDynamic
	default:
		baseRelease = gateReleaseMod
	}

	// Compensate for lack of hold parameter
	baseRelease += gateReleaseHoldComp

	// Tonal bleed needs slower release to hide pumping
	if tonalNoise {
		baseRelease += gateReleaseTonalComp
	}

	return clamp(baseRelease, float64(gateReleaseMin), float64(gateReleaseMax))
}

// calculateGateRange determines maximum attenuation depth based on noise character.
// Tonal noise (bleed, hum) sounds worse when hard-gated - use gentler range.
// Broadband noise can be gated more aggressively.
func calculateGateRange(silenceEntropy, noiseFloorDB float64) float64 {
	var rangeDB float64

	switch {
	case silenceEntropy < gateEntropyTonal:
		rangeDB = gateRangeTonalDB // Tonal - gentle
	case silenceEntropy < gateEntropyMixed:
		rangeDB = gateRangeMixedDB // Mixed - moderate
	default:
		rangeDB = gateRangeBroadbandDB // Broadband - aggressive
	}

	// Can go deeper if very clean recording
	if noiseFloorDB < -70 {
		rangeDB += gateRangeCleanBoost // More negative = deeper
	}

	rangeDB = clamp(rangeDB, float64(gateRangeMinDB), float64(gateRangeMaxDB))

	return dbToLinear(rangeDB)
}

// calculateGateKnee determines knee softness based on spectral crest.
// Dynamic content with prominent peaks benefits from softer knee.
func calculateGateKnee(spectralCrest float64) float64 {
	switch {
	case spectralCrest > gateSpectralCrestHigh:
		return gateKneeSoft // Dynamic - soft engagement
	case spectralCrest > gateSpectralCrestMod:
		return gateKneeMod // Standard
	default:
		return gateKneeSharp // Less dynamic - sharper OK
	}
}

// calculateGateDetection determines whether to use RMS or peak detection.
// RMS is safer for speech and handles tonal bleed better.
// Peak provides tighter tracking for very clean recordings.
func calculateGateDetection(silenceEntropy, silenceCrestDB float64) string {
	// Tonal noise or high crest in silence - use RMS
	if silenceEntropy < gateEntropyTonal || silenceCrestDB > gateSilenceCrestThreshold {
		return "rms"
	}

	// Very clean with low crest - can use peak for tighter tracking
	if silenceEntropy > gateEntropyClean && silenceCrestDB < 15 {
		return "peak"
	}

	// Default: RMS is safer for speech
	return "rms"
}

// tuneGateThreshold is deprecated - use tuneGate instead.
// Kept for backwards compatibility during transition.
func tuneGateThreshold(config *FilterChainConfig, measurements *AudioMeasurements) {
	tuneGate(config, measurements)
}

// tuneLA2ACompressor applies Teletronix LA-2A style optical compressor tuning.
//
// The Teletronix LA-2A is legendary for its gentle, program-dependent character:
// - Fixed 10ms attack preserves transients and consonant "pluck"
// - Two-stage release: 60ms initial, then 1-15s for full release
// - Soft variable ratio (~3:1) that adapts to signal strength
// - Very soft knee from the T4 optical cell
// - "Treats your signal lovingly" (Bill Putnam Jr.)
//
// This implementation uses spectral measurements to emulate program-dependent
// behaviour that the optical T4 cell provides naturally.
func tuneLA2ACompressor(config *FilterChainConfig, measurements *AudioMeasurements) {
	tuneLA2AAttack(config, measurements)
	tuneLA2ARelease(config, measurements)
	tuneLA2ARatio(config, measurements)
	tuneLA2AThreshold(config, measurements)
	tuneLA2AKnee(config, measurements)
	tuneLA2AMix(config, measurements)
	tuneLA2AMakeup(config, measurements)
}

// tuneLA2AAttack sets attack time based on transient characteristics.
// LA-2A has fixed 10ms attack - we allow slight variation for extreme cases.
// MaxDifference indicates transient sharpness (% of full scale).
func tuneLA2AAttack(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Default to LA-2A's fixed 10ms attack
	attack := la2aAttackBase

	// MaxDifference is stored as raw sample units (0-32768 for 16-bit audio)
	// Normalize to fraction (0.0-1.0) for comparison with thresholds
	maxDiffNorm := measurements.MaxDifference / 32768.0

	// Slight variation based on transient sharpness
	if maxDiffNorm > 0 {
		switch {
		case maxDiffNorm > la2aMaxDiffSharp:
			// Very sharp transients - slightly faster to catch peaks
			attack = la2aAttackFast
		case maxDiffNorm < la2aMaxDiffSoft:
			// Soft delivery - can be even gentler
			attack = la2aAttackSlow
		}
	}

	config.LA2AAttack = attack
}

// tuneLA2ARelease sets release time to approximate LA-2A's two-stage behaviour.
// Real LA-2A: 60ms to 50% release, then 1-15s for full release.
// The release time depends on signal duration and strength above threshold.
//
// We use LRA (loudness range) and SpectralFlux to approximate this:
// - Wide LRA + high flux = expressive speech, needs longer release
// - Narrow LRA + low flux = compressed/monotone, faster release OK
// - Warm voices (high skewness) get extra release to preserve body
func tuneLA2ARelease(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Start with standard LA-2A-style release
	release := la2aReleaseStandard

	// Adjust based on LRA (loudness dynamics)
	switch {
	case measurements.InputLRA > la2aLRAExpressive:
		// Expressive delivery - longer release preserves dynamics
		release = la2aReleaseExpressive
	case measurements.InputLRA < la2aLRACompact:
		// Compressed delivery - faster release OK
		release = la2aReleaseCompact
	}

	// Adjust based on spectral flux (frame-to-frame variation)
	if measurements.SpectralFlux > 0 {
		switch {
		case measurements.SpectralFlux > la2aFluxDynamic:
			// Dynamic/expressive content - add release time
			release = math.Max(release, la2aReleaseExpressive)
		case measurements.SpectralFlux < la2aFluxStatic:
			// Static/monotone content - can use shorter release
			release = math.Min(release, la2aReleaseCompact)
		}
	}

	// Warm voices (positive skewness = bass-concentrated) get extra release
	// This preserves the body and warmth that LA-2A is known for
	if measurements.SpectralSkewness > la2aSkewnessWarm {
		release += la2aReleaseWarmBoost
	}

	// Heavy compression (large LUFS gap) triggers slower release
	// LA-2A's T4 cell releases slower after sustained heavy compression
	if measurements.InputI < 0 {
		lufsGap := -16.0 - measurements.InputI // Distance to -16 LUFS target
		if lufsGap > 15.0 {
			release += la2aReleaseHeavyBoost
		}
	}

	config.LA2ARelease = release
}

// tuneLA2ARatio sets compression ratio to emulate T4 optical cell behaviour.
// LA-2A's ratio is nominally 3:1 but varies with signal strength.
// We use spectral kurtosis and dynamic range to approximate this:
// - Peaked/tonal content (high kurtosis) = gentler ratio, preserve character
// - Flat/noise-like content (low kurtosis) = firmer ratio, more levelling
func tuneLA2ARatio(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Start with LA-2A baseline ratio
	ratio := la2aRatioBase

	// Adjust based on spectral kurtosis (peakedness)
	if measurements.SpectralKurtosis > 0 {
		switch {
		case measurements.SpectralKurtosis > la2aKurtosisHighPeak:
			// Highly peaked harmonics - gentler ratio preserves character
			ratio = la2aRatioPeaked
		case measurements.SpectralKurtosis < la2aKurtosisLowPeak:
			// Flat spectrum - firmer ratio for consistent levelling
			ratio = la2aRatioFlat
		}
	}

	// Very wide dynamic range needs extra control
	if measurements.DynamicRange > la2aDynamicRangeWide {
		ratio += la2aRatioDynamicBoost
	}

	// Clamp to reasonable range
	config.LA2ARatio = clamp(ratio, 2.0, 5.0)
}

// tuneLA2AThreshold sets threshold relative to RMS level.
// LA-2A's Peak Reduction knob effectively sets threshold relative to signal.
// We calculate threshold as peak level minus headroom, where headroom determines depth.
func tuneLA2AThreshold(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Fallback if no peak measurement
	if measurements.PeakLevel == 0 {
		config.LA2AThreshold = defaultLA2AThreshold
		return
	}

	// Determine headroom based on dynamic range (compression depth needed)
	// More headroom = lower threshold = more compression
	var headroom float64
	switch {
	case measurements.DynamicRange > la2aDynamicRangeHigh:
		// Very dynamic - heavier compression (more headroom from peak)
		headroom = la2aThresholdHeadroomHeavy
	case measurements.DynamicRange > la2aDynamicRangeMod:
		// Moderately dynamic - standard LA-2A
		headroom = la2aThresholdHeadroomStd
	default:
		// Already compressed - light levelling
		headroom = la2aThresholdHeadroomLight
	}

	// Calculate threshold relative to peak level
	// threshold = peak - headroom
	// e.g., peak -5dB with 15dB headroom → threshold -20dB
	threshold := measurements.PeakLevel - headroom

	// Clamp to safe range
	threshold = clamp(threshold, la2aThresholdMin, la2aThresholdMax)

	config.LA2AThreshold = threshold
}

// tuneLA2AKnee sets knee softness to emulate T4 optical cell.
// The T4 provides an inherently soft knee - one of LA-2A's defining characteristics.
// We adapt based on voice character (spectral centroid and skewness).
func tuneLA2AKnee(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Start with standard LA-2A soft knee
	knee := la2aKneeNormal

	// Adjust based on spectral centroid (voice brightness)
	if measurements.SpectralCentroid > 0 {
		switch {
		case measurements.SpectralCentroid < la2aCentroidDark:
			// Dark/warm voice - extra soft knee preserves warmth
			knee = la2aKneeDark
		case measurements.SpectralCentroid > la2aCentroidBright:
			// Bright voice - slightly firmer knee
			knee = la2aKneeBright
		}
	}

	// Warm/bass-concentrated voices get extra soft knee
	if measurements.SpectralSkewness > la2aSkewnessWarm {
		knee += la2aKneeWarmBoost
	}

	// Clamp to FFmpeg's range
	config.LA2AKnee = clamp(knee, 1.0, 8.0)
}

// tuneLA2AMix sets wet/dry mix.
// Real LA-2A is 100% wet (no parallel compression).
// We allow slight dry signal for problematic recordings to mask artefacts.
func tuneLA2AMix(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Default to true LA-2A behaviour (100% wet)
	mix := la2aMixClean

	// Adjust based on noise floor (artefact masking)
	switch {
	case measurements.NoiseFloor > la2aNoiseFloorNoisy:
		// Noisy recording - dry signal masks compression artefacts
		mix = la2aMixNoisy
	case measurements.NoiseFloor > la2aNoiseFloorClean:
		// Moderate noise - slight dry signal
		mix = la2aMixModerate
	}

	config.LA2AMix = mix
}

// tuneLA2AMakeup sets makeup gain to compensate for gain reduction.
// Calculated conservatively - let downstream normalisation handle the rest.
func tuneLA2AMakeup(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Calculate expected gain reduction
	// GR ≈ (peak_level - threshold) * (1 - 1/ratio)
	if measurements.PeakLevel == 0 || config.LA2AThreshold == 0 {
		config.LA2AMakeup = la2aMakeupMin
		return
	}

	// Amount signal exceeds threshold
	overshoot := measurements.PeakLevel - config.LA2AThreshold
	if overshoot <= 0 {
		// Signal below threshold - minimal makeup
		config.LA2AMakeup = la2aMakeupMin
		return
	}

	// Expected reduction based on ratio
	reduction := overshoot * (1.0 - 1.0/config.LA2ARatio)

	// Conservative makeup (let normalisation handle the rest)
	makeup := reduction * la2aMakeupMultiplier

	// Clamp to safe range
	config.LA2AMakeup = clamp(makeup, la2aMakeupMin, la2aMakeupMax)
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
// Note: This function is deprecated - tuneArnndn now handles all arnndn tuning.
// Kept for backwards compatibility but the logic is now in tuneArnndn.
func tuneSpeechnormDenoise(config *FilterChainConfig, expansion float64) {
	// Respect user's intent: if filter is disabled, don't touch it
	if !config.ArnnDnEnabled {
		return
	}

	if expansion >= speechnormExpansionThreshold {
		// Filter stays enabled - tuneArnndn handles mix calculation
		// Just ensure it's enabled for heavily expanded audio
	} else {
		// Light expansion - let tuneArnndn decide based on noise floor
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
	config.HighpassWidth = sanitizeFloat(config.HighpassWidth, 0.707) // Butterworth default
	config.HighpassMix = sanitizeFloat(config.HighpassMix, 1.0)       // Full wet default
	config.DeessIntensity = sanitizeFloat(config.DeessIntensity, defaultDeessIntensity)
	config.NoiseReduction = sanitizeFloat(config.NoiseReduction, defaultNoiseReduction)
	config.LA2ARatio = sanitizeFloat(config.LA2ARatio, defaultLA2ARatio)
	config.LA2AThreshold = sanitizeFloat(config.LA2AThreshold, defaultLA2AThreshold)
	config.LA2AMakeup = sanitizeFloat(config.LA2AMakeup, defaultLA2AMakeup)

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
