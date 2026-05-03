package processor

import "math"

const (
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

	// LA-2A hot input protection
	// When input true peak >= -1 dBTP, the signal is already loud enough that
	// aggressive compression crushes dynamics unnecessarily. The downstream
	// limiter handles peak control, so the compressor can afford to be gentler.
	la2aHotInputTPThreshold       = -1.0 // dBTP: above this, start backing off
	la2aHotInputTPSevere          = -0.5 // dBTP: above this, maximum backoff
	la2aHotInputRatioReduction    = 1.0  // Maximum ratio reduction (e.g. 3.5 → 2.5)
	la2aHotInputHeadroomReduction = 5.0  // Maximum headroom reduction in dB

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

	// LA-2A High-crest override constants
	la2aHighCrestMaxDeficit = 6.0 // dB, deficit at which severity reaches 1.0 (beyond ~6 dB, pre-gain handles it)

	defaultLA2ARatio     = 3.0   // LA-2A baseline ratio
	defaultLA2AThreshold = -18.0 // Moderate threshold
)

// la2aOverrides carries override floor values for LA-2A sub-tuners.
// Zero value means "no override" for that parameter.
type la2aOverrides struct {
	ThresholdFloor float64 // dB, sub-tuners must not set threshold above this (more negative = lower)
	RatioFloor     float64 // sub-tuners must not set ratio below this
	ReleaseFloor   float64 // ms, sub-tuners must not set release below this
	KneeFloor      float64 // sub-tuners must not set knee below this
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
func tuneLA2ACompressor(config *EffectiveFilterConfig, diagnostics *AdaptiveDiagnostics, measurements *AudioMeasurements) {
	overrides := applyHighCrestOverrides(config, diagnostics, measurements)
	tuneLA2AAttack(config, measurements)
	tuneLA2ARelease(config, measurements, overrides)
	tuneLA2ARatio(config, measurements, overrides)
	tuneLA2AThreshold(config, measurements, overrides)
	tuneLA2AKnee(config, measurements, overrides)
	tuneLA2AMix(config, measurements)
	// Note: Makeup gain left at default (0 dB unity) - loudnorm handles all level adjustment
}

// applyHighCrestOverrides predicts whether Pass 4's calculateLimiterCeiling will
// clamp and, when it will, returns override floors that push the LA-2A toward
// more aggressive compression. The deficit calculation mirrors
// calculateLimiterCeiling() in normalise.go using Pass 1 measurements as a
// forward estimate.
//
// Diagnostic fields are always populated (active or not) when diagnostics is non-nil.
// Returns zero-value la2aOverrides when no overrides are needed (deficit <= 0).
func applyHighCrestOverrides(config *EffectiveFilterConfig, diagnostics *AdaptiveDiagnostics, measurements *AudioMeasurements) la2aOverrides {
	if measurements.SpeechProfile == nil {
		debugLog("high-crest: SpeechProfile is nil, using full-file InputI/InputTP for deficit calculation")
	}

	// Deficit calculation
	gainRequired := NormTargetLUFS - measurements.InputI
	projectedTP := measurements.InputTP + gainRequired
	idealCeiling := config.LoudnormTargetTP - gainRequired - safetyMarginDB
	deficit := minLimiterCeilingDB - idealCeiling

	if diagnostics != nil {
		diagnostics.LA2AHighCrestActive = false
		diagnostics.LA2AHighCrestDeficit = deficit
		diagnostics.LA2AHighCrestSeverity = 0
		diagnostics.LA2AHighCrestProjectedTP = projectedTP
	}

	if deficit <= 0 {
		return la2aOverrides{}
	}

	severity := clamp(deficit/la2aHighCrestMaxDeficit, 0.0, 1.0)
	if diagnostics != nil {
		diagnostics.LA2AHighCrestActive = true
		diagnostics.LA2AHighCrestSeverity = severity
	}

	return la2aOverrides{
		ThresholdFloor: lerp(-18.0, -40.0, severity),
		RatioFloor:     lerp(3.0, 5.0, severity),
		ReleaseFloor:   lerp(200.0, 350.0, severity),
		KneeFloor:      lerp(4.0, 6.0, severity),
	}
}

// tuneLA2AAttack sets attack time based on transient characteristics.
// LA-2A has fixed 10ms attack - we allow slight variation for extreme cases.
// MaxDifference indicates transient sharpness (% of full scale).
func tuneLA2AAttack(config *EffectiveFilterConfig, measurements *AudioMeasurements) {
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
func tuneLA2ARelease(config *EffectiveFilterConfig, measurements *AudioMeasurements, overrides la2aOverrides) {
	// Prefer speech-specific flux for timing decisions
	flux := measurements.SpectralFlux
	if measurements.SpeechProfile != nil {
		flux = preferSpeechMetric(flux, measurements.SpeechProfile.Spectral.Flux)
	}

	// Prefer speech-specific skewness for warm voice detection
	var speechSkewness float64
	if measurements.SpeechProfile != nil {
		speechSkewness = measurements.SpeechProfile.Spectral.Skewness
	}
	skewness := preferSpeechMetricSigned(measurements.SpectralSkewness, speechSkewness, measurements.SpeechProfile != nil)

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
	if flux > 0 {
		switch {
		case flux > la2aFluxDynamic:
			// Dynamic/expressive content - add release time
			release = math.Max(release, la2aReleaseExpressive)
		case flux < la2aFluxStatic:
			// Static/monotone content - can use shorter release
			release = math.Min(release, la2aReleaseCompact)
		}
	}

	// Warm voices (positive skewness = bass-concentrated) get extra release
	// This preserves the body and warmth that LA-2A is known for
	if skewness > la2aSkewnessWarm {
		release += la2aReleaseWarmBoost
	}

	// Heavy compression (large LUFS gap) triggers slower release
	// LA-2A's T4 cell releases slower after sustained heavy compression
	if measurements.InputI < 0 {
		lufsGap := NormTargetLUFS - measurements.InputI // Distance to LUFS target
		if lufsGap > 16.0 {
			release += la2aReleaseHeavyBoost
		}
	}

	// Enforce high-crest override floor when active
	if overrides.ReleaseFloor != 0 {
		release = math.Max(release, overrides.ReleaseFloor)
	}

	config.LA2ARelease = release
}

// tuneLA2ARatio sets compression ratio to emulate T4 optical cell behaviour.
// LA-2A's ratio is nominally 3:1 but varies with signal strength.
// We use spectral kurtosis and dynamic range to approximate this:
// - Peaked/tonal content (high kurtosis) = gentler ratio, preserve character
// - Flat/noise-like content (low kurtosis) = firmer ratio, more levelling
//
// Kurtosis reference: Gaussian distribution has kurtosis=3.
// Speech typically ranges 5-10 (leptokurtic, clear harmonics).
func tuneLA2ARatio(config *EffectiveFilterConfig, measurements *AudioMeasurements, overrides la2aOverrides) {
	// Prefer speech-specific kurtosis for harmonic structure
	kurtosis := measurements.SpectralKurtosis
	if measurements.SpeechProfile != nil {
		kurtosis = preferSpeechMetric(kurtosis, measurements.SpeechProfile.Spectral.Kurtosis)
	}

	// Start with LA-2A baseline ratio
	ratio := la2aRatioBase

	// Adjust based on spectral kurtosis (peakedness)
	if kurtosis > 0 {
		switch {
		case kurtosis > la2aKurtosisHighPeak:
			// Highly peaked harmonics - gentler ratio preserves character
			ratio = la2aRatioPeaked
		case kurtosis < la2aKurtosisLowPeak:
			// Flat spectrum - firmer ratio for consistent levelling
			ratio = la2aRatioFlat
		}
	}

	// Very wide dynamic range needs extra control
	if measurements.DynamicRange > la2aDynamicRangeWide {
		ratio += la2aRatioDynamicBoost
	}

	// Hot input protection: back off ratio when true peak is high.
	// The limiter downstream handles peak control, so the compressor
	// can afford to be gentler on already-loud material.
	if measurements.InputTP >= la2aHotInputTPThreshold {
		severity := (measurements.InputTP - la2aHotInputTPThreshold) /
			(la2aHotInputTPSevere - la2aHotInputTPThreshold)
		severity = clamp(severity, 0.0, 1.0)
		severity = math.Sqrt(severity)
		ratio -= la2aHotInputRatioReduction * severity
	}

	// Clamp to reasonable range
	ratio = clamp(ratio, 2.0, 5.0)

	// Enforce high-crest override floor when active
	if overrides.RatioFloor != 0 {
		ratio = math.Max(ratio, overrides.RatioFloor)
		ratio = clamp(ratio, 2.0, 5.0)
	}

	config.LA2ARatio = ratio
}

// tuneLA2AThreshold sets threshold relative to RMS level.
// LA-2A's Peak Reduction knob effectively sets threshold relative to signal.
// We calculate threshold as peak level minus headroom, where headroom determines depth.
func tuneLA2AThreshold(config *EffectiveFilterConfig, measurements *AudioMeasurements, overrides la2aOverrides) {
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

	// Hot input protection: reduce headroom (raise threshold) for loud inputs.
	// When peaks are already near 0 dBTP, less compression depth is needed
	// because the limiter downstream handles peak control.
	if measurements.InputTP >= la2aHotInputTPThreshold {
		severity := (measurements.InputTP - la2aHotInputTPThreshold) /
			(la2aHotInputTPSevere - la2aHotInputTPThreshold)
		severity = clamp(severity, 0.0, 1.0)
		severity = math.Sqrt(severity)
		headroom -= la2aHotInputHeadroomReduction * severity
	}

	// Calculate threshold relative to peak level
	// threshold = peak - headroom
	// e.g., peak -5dB with 15dB headroom → threshold -20dB
	threshold := measurements.PeakLevel - headroom

	// Clamp to safe range
	threshold = clamp(threshold, la2aThresholdMin, la2aThresholdMax)

	// Enforce high-crest override floor when active (lower threshold = more compression)
	if overrides.ThresholdFloor != 0 {
		threshold = math.Min(threshold, overrides.ThresholdFloor)
	}

	config.LA2AThreshold = threshold
}

// tuneLA2AKnee sets knee softness to emulate T4 optical cell.
// The T4 provides an inherently soft knee - one of LA-2A's defining characteristics.
// We adapt based on voice character (spectral centroid and skewness).
func tuneLA2AKnee(config *EffectiveFilterConfig, measurements *AudioMeasurements, overrides la2aOverrides) {
	// Start with standard LA-2A soft knee
	knee := la2aKneeNormal

	// Prefer speech-specific spectral metrics when available.
	// Full-file averages are diluted by silence in multi-track recordings.
	hasSpeech := measurements.SpeechProfile != nil
	centroid := measurements.SpectralCentroid
	if hasSpeech {
		centroid = preferSpeechMetric(centroid, measurements.SpeechProfile.Spectral.Centroid)
	}
	var speechSkewness float64
	if hasSpeech {
		speechSkewness = measurements.SpeechProfile.Spectral.Skewness
	}
	skewness := preferSpeechMetricSigned(measurements.SpectralSkewness, speechSkewness, hasSpeech)

	// Adjust based on spectral centroid (voice brightness)
	if centroid > 0 {
		switch {
		case centroid < la2aCentroidDark:
			// Dark/warm voice - extra soft knee preserves warmth
			knee = la2aKneeDark
		case centroid > la2aCentroidBright:
			// Bright voice - slightly firmer knee
			knee = la2aKneeBright
		}
	}

	// Warm/bass-concentrated voices get extra soft knee
	if skewness > la2aSkewnessWarm {
		knee += la2aKneeWarmBoost
	}

	// Clamp to FFmpeg's range
	knee = clamp(knee, 1.0, 8.0)

	// Enforce high-crest override floor when active
	if overrides.KneeFloor != 0 {
		knee = math.Max(knee, overrides.KneeFloor)
		knee = clamp(knee, 1.0, 8.0)
	}

	config.LA2AKnee = knee
}

// tuneLA2AMix sets wet/dry mix.
// Real LA-2A is 100% wet (no parallel compression).
// We allow slight dry signal for problematic recordings to mask artefacts.
func tuneLA2AMix(config *EffectiveFilterConfig, measurements *AudioMeasurements) {
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
