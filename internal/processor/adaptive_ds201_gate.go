package processor

const (
	// LUFS gap threshold for adaptive processing intensity
	lufsGapExtreme = 25.0 // dB - extreme gap, gate needs special handling

	// Gentle gate mode: for extreme LUFS gap + low LRA
	// Very quiet recordings with uniform levels cause the gate's soft expansion
	// to apply varying gain reduction across similar speech levels, creating
	// volume modulation ("hunting"). Use gentler parameters to prevent this.
	ds201GateGentleLRAThreshold = 10.0 // LU - below this with extreme LUFS gap triggers gentle mode
	ds201GateGentleRatio        = 1.2  // Minimal gain variation in expansion zone
	ds201GateGentleKnee         = 2.0  // Sharper transition reduces hunting

	// Threshold calculation: ensures sufficient gap above noise for effective soft expansion
	ds201GateThresholdMinDB       = -80.0 // dB - minimum threshold (allows speech guard to protect quiet content)
	ds201GateThresholdMaxDB       = -25.0 // dB - never gate above this (would cut speech)
	ds201GateCrestFactorThreshold = 20.0  // dB - above this, use peak reference instead of RMS
	ds201GateTargetReductionDB    = 12.0  // dB - target noise reduction from soft expander
	ds201GateTargetThresholdDB    = -40.0 // dB - target threshold for clean recordings (quiet speech/breath level)

	// Base aggression tiers (by separation in dB)
	ds201GateAggressionSepTight    = 10.0 // dB - below: minimal separation, conservative
	ds201GateAggressionSepModerate = 15.0 // dB - moderate separation
	ds201GateAggressionSepGood     = 20.0 // dB - good separation

	ds201GateAggressionTight     = 0.30 // For separation < 10 dB
	ds201GateAggressionModLow    = 0.35 // Base for 10-15 dB separation
	ds201GateAggressionModScale  = 0.02 // Per-dB scale in moderate tier
	ds201GateAggressionGoodLow   = 0.45 // Base for 15-20 dB separation
	ds201GateAggressionGoodScale = 0.02 // Per-dB scale in good tier
	ds201GateAggressionWide      = 0.55 // For separation >= 20 dB

	// LRA adjustment to aggression
	ds201GateAggressionLRAThreshold = 12.0  // LU - above: start reducing aggression
	ds201GateAggressionLRAScale     = 0.015 // Reduction per LU above threshold

	// Aggression clamps
	ds201GateAggressionMin = 0.25 // Never too conservative
	ds201GateAggressionMax = 0.60 // Never too aggressive

	// Safety margins
	ds201GateThresholdSpeechMargin = 10.0 // dB - minimum gap below speech RMS
	ds201GateThresholdNoiseMargin  = 5.0  // dB - room for soft expander action

	// Ratio: based on LRA (loudness range)
	ds201GateLRAWide     = 15.0 // LU - above: wide dynamics, gentle ratio
	ds201GateLRAModerate = 10.0 // LU - above: moderate dynamics
	ds201GateRatioGentle = 1.5  // For wide LRA (preserve expression)
	ds201GateRatioMod    = 2.0  // For moderate LRA
	ds201GateRatioTight  = 2.5  // For narrow LRA (tighter control OK)

	// Attack: based on MaxDifference (transient indicator)
	// Fast transients need fast attack to avoid clipping word onsets
	ds201GateMaxDiffHigh      = 25.0 // % - sharp transients
	ds201GateMaxDiffMod       = 10.0 // % - moderate transients
	ds201GateMaxDiffExtreme   = 40.0 // % - threshold for ultra-fast attack
	ds201GateCrestExtreme     = 40.0 // dB - threshold for ultra-fast attack
	ds201GateAttackUltraFast  = 10.0 // ms - minimum attack to avoid click artifacts
	ds201GateAttackFast       = 10.0 // ms - for sharp transients (minimum to avoid clicks)
	ds201GateAttackMod        = 12.0 // ms - standard speech
	ds201GateAttackSlow       = 17.0 // ms - soft onsets
	ds201GateFluxDynamicThres = 0.05 // SpectralFlux threshold for dynamic content

	// Release: based on flux, ZCR, and noise character
	// No hold parameter exists - release must compensate
	ds201GateFluxLow          = 0.01 // Low flux threshold
	ds201GateZCRLow           = 0.08 // Low zero crossings rate
	ds201GateFluxHigh         = 0.05 // High flux threshold
	ds201GateReleaseSustained = 300  // ms - for sustained speech (was 400)
	ds201GateReleaseMod       = 250  // ms - standard (was 300)
	ds201GateReleaseDynamic   = 180  // ms - for dynamic content (was 200)
	ds201GateReleaseHoldComp  = 50   // ms - compensation for lack of hold parameter
	ds201GateReleaseTonalComp = 75   // ms - extra for tonal bleed (hide pump)
	ds201GateReleaseMin       = 150  // ms - minimum release
	ds201GateReleaseMax       = 600  // ms - maximum release (increased for low LRA)

	// Adaptive release based on noise entropy (higher entropy = more broadband = faster release)
	// Tonal noise (low entropy) needs slow release to hide pumping artifacts
	// Broadband/mixed noise (higher entropy) benefits from faster release to cut noise quickly
	ds201GateReleaseEntropyVeryTonal = 0.10 // Below: very tonal (pure hum/bleed) - slowest
	ds201GateReleaseEntropyTonal     = 0.12 // Below: tonal noise - slow release
	ds201GateReleaseEntropyMixed     = 0.16 // Below: mixed character - moderate release
	// Above 0.16: broadband-ish noise - faster release OK
	ds201GateReleaseEntropyReduce = 100 // ms - reduction for broadband-ish noise

	// LRA-based release extension (low dynamic range = more pumping risk)
	// When speech has narrow loudness range (<12 LU), gate opens/closes rapidly
	// on similar-level segments, causing audible pumping. Longer release helps.
	ds201GateReleaseLRALow       = 10.0 // LU - below: low dynamic range, extend release
	ds201GateReleaseLRAVeryLow   = 8.0  // LU - below: very low LRA, maximum extension
	ds201GateReleaseLRAExtension = 100  // ms - extension for low LRA audio
	ds201GateReleaseLRAMaxExt    = 150  // ms - maximum extension for very low LRA

	// Range: based on silence entropy and noise floor
	// Tonal noise sounds worse when hard-gated - gentler range hides pumping
	ds201GateEntropyTonal     = 0.3 // Below: tonal noise (bleed/hum)
	ds201GateEntropyMixed     = 0.6 // Below: mixed noise
	ds201GateRangeTonalDB     = -16 // dB - gentle for tonal noise
	ds201GateRangeMixedDB     = -21 // dB - moderate for mixed
	ds201GateRangeBroadbandDB = -27 // dB - aggressive for broadband
	ds201GateRangeCleanBoost  = -6  // dB - extra depth for very clean
	ds201GateRangeMinDB       = -36 // dB - minimum (deepest)
	ds201GateRangeMaxDB       = -12 // dB - maximum (gentlest)

	// Knee: based on spectral crest
	ds201GateSpectralCrestHigh = 35.0 // High crest threshold
	ds201GateSpectralCrestMod  = 20.0 // Moderate crest threshold
	ds201GateKneeSoft          = 5.0  // For dynamic content with prominent peaks
	ds201GateKneeMod           = 3.0  // Standard
	ds201GateKneeSharp         = 2.0  // For less dynamic content

	// Detection: based on silence entropy and crest factor
	ds201GateSilenceCrestThreshold = 25.0 // dB - above: use RMS (noise has spikes)
	ds201GateEntropyClean          = 0.7  // Above: can use peak detection

	ds201DefaultGateThreshold = 0.01 // -40dBFS
)

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
func tuneDS201Gate(config *EffectiveFilterConfig, diagnostics *AdaptiveDiagnostics, measurements *AudioMeasurements) {
	if diagnostics != nil {
		diagnostics.DS201GateGentleMode = false
		diagnostics.DS201GateAggression = 0
		diagnostics.DS201GateDynamicRange = 0
		diagnostics.DS201GateQuietSpeechEstimate = 0
		diagnostics.DS201GateSpeechSeparation = 0
		diagnostics.DS201GateSpeechHeadroom = 0
		diagnostics.DS201GateThresholdUnclamped = 0
		diagnostics.DS201GateClampReason = ""
	}

	// Extract silence sample characteristics for gate tuning
	var silenceEntropy, silenceCrest, silencePeak float64

	if measurements.NoiseProfile != nil {
		silenceEntropy = measurements.NoiseProfile.Entropy
		silenceCrest = measurements.NoiseProfile.CrestFactor
		silencePeak = measurements.NoiseProfile.PeakLevel
	} else {
		// NoiseProfile unavailable - use conservative defaults for broadband noise
		// Entropy 0.65 triggers broadband range (-27 dB) for effective gating
		// Without silence analysis, assume typical room noise characteristics
		silenceEntropy = 0.65
		silenceCrest = 15.0 // Moderate crest, use RMS detection
		silencePeak = 0     // Will fall back to NoiseFloor for threshold
	}

	// Calculate LUFS gap for threshold decision
	lufsGap := config.TargetI - measurements.InputI
	if lufsGap < 0 {
		lufsGap = 0
	}

	// 2. Ratio: based on LRA (loudness range) - soft expander approach
	// Calculate ratio FIRST since threshold depends on it
	config.DS201GateRatio = calculateDS201GateRatio(measurements.InputLRA)

	// Extract speech measurements (zero values if no profile)
	var speechRMS, speechCrest float64
	if measurements.SpeechProfile != nil {
		speechRMS = measurements.SpeechProfile.RMSLevel
		speechCrest = measurements.SpeechProfile.CrestFactor
	}

	// 1. Threshold: sits above noise/bleed peaks, below quiet speech
	// Gap is derived from ratio to achieve target reduction
	config.DS201GateThreshold = calculateDS201GateThreshold(
		measurements.NoiseFloor,
		silencePeak,
		silenceCrest,
		config.DS201GateRatio,
		lufsGap,
		measurements.InputLRA,
		speechRMS,
		speechCrest,
	)

	// Track threshold calculation diagnostics
	if measurements.SpeechProfile != nil && diagnostics != nil {
		quietSpeech := measurements.SpeechProfile.RMSLevel - measurements.SpeechProfile.CrestFactor
		separation := quietSpeech - measurements.NoiseFloor

		// Calculate aggression for diagnostics
		aggression := calculateAggression(separation, measurements.InputLRA)
		dynamicRange := measurements.SpeechProfile.CrestFactor

		// Calculate unclamped threshold for diagnostics
		thresholdUnclamped := quietSpeech + (dynamicRange * aggression)

		// Determine clamp reason
		noiseFloorLimit := measurements.NoiseFloor + ds201GateThresholdNoiseMargin
		speechRMSLimit := measurements.SpeechProfile.RMSLevel - ds201GateThresholdSpeechMargin
		actualThreshold := LinearToDb(config.DS201GateThreshold)

		var clampReason string
		switch {
		case thresholdUnclamped < noiseFloorLimit && actualThreshold >= noiseFloorLimit:
			clampReason = "noise_floor"
		case thresholdUnclamped > speechRMSLimit && actualThreshold <= speechRMSLimit:
			clampReason = "speech_rms"
		default:
			clampReason = "none"
		}

		diagnostics.DS201GateAggression = aggression
		diagnostics.DS201GateDynamicRange = dynamicRange
		diagnostics.DS201GateQuietSpeechEstimate = quietSpeech
		diagnostics.DS201GateSpeechSeparation = separation
		diagnostics.DS201GateThresholdUnclamped = thresholdUnclamped
		diagnostics.DS201GateClampReason = clampReason
		diagnostics.DS201GateSpeechHeadroom = quietSpeech - actualThreshold
	}

	// 3. Attack: based on MaxDifference, SpectralFlux, and SpectralCrest
	// DS201-inspired: supports sub-millisecond attack for transient preservation
	config.DS201GateAttack = calculateDS201GateAttack(
		measurements.MaxDifference,
		measurements.SpectralFlux,
		measurements.SpectralCrest,
	)

	// 4. Release: based on flux, ZCR, noise character (entropy), and LRA
	// Includes +50ms compensation for lack of Hold parameter
	// Higher entropy = more broadband noise = faster release to cut noise quickly
	// Low LRA = narrow dynamics = extend release to prevent pumping
	config.DS201GateRelease = calculateDS201GateRelease(
		measurements.SpectralFlux,
		measurements.ZeroCrossingsRate,
		silenceEntropy,
		measurements.InputLRA,
	)

	// 5. Range: based on silence entropy and noise floor
	rangeDB := calculateDS201GateRangeDB(silenceEntropy, measurements.NoiseFloor)

	// Clamp range and convert to linear
	rangeDB = clamp(rangeDB, float64(ds201GateRangeMinDB), float64(ds201GateRangeMaxDB))
	config.DS201GateRange = DbToLinear(rangeDB)

	// 6. Knee: based on spectral crest - soft knee for natural transitions
	config.DS201GateKnee = calculateDS201GateKnee(measurements.SpectralCrest)

	// 7. Detection: RMS for bleed, peak for clean
	config.DS201GateDetection = calculateDS201GateDetection(silenceEntropy, silenceCrest)

	// Note: Makeup gain left at default (1.0 unity) - loudnorm handles all level adjustment

	// Gentle gate mode override: for extreme LUFS gap + low LRA
	// Very quiet recordings with uniform levels cause the gate's soft expansion
	// to apply varying gain reduction across similar speech levels, creating
	// volume modulation ("hunting"). Override to gentler parameters.
	if lufsGap >= lufsGapExtreme && measurements.InputLRA < ds201GateGentleLRAThreshold {
		config.DS201GateRatio = ds201GateGentleRatio
		config.DS201GateKnee = ds201GateGentleKnee
		if diagnostics != nil {
			diagnostics.DS201GateGentleMode = true
		}
	}
}

// calculateAggression determines how aggressively to position threshold
// between quiet speech and speech RMS.
// Returns 0.0-1.0 where:
//   - 0.0 = threshold at quiet speech (very conservative)
//   - 1.0 = threshold at speech RMS (very aggressive, would gate speech)
//
// Uses separation (quietSpeech - noiseFloor) as primary factor,
// with LRA adjustment for dynamic content.
func calculateAggression(separation, lra float64) float64 {
	var baseAggression float64

	switch {
	case separation < ds201GateAggressionSepTight:
		// Tight separation: conservative positioning
		baseAggression = ds201GateAggressionTight
	case separation < ds201GateAggressionSepModerate:
		// Moderate separation: scale 0.35-0.45
		t := separation - ds201GateAggressionSepTight
		baseAggression = ds201GateAggressionModLow + (t * ds201GateAggressionModScale)
	case separation < ds201GateAggressionSepGood:
		// Good separation: scale 0.45-0.55
		t := separation - ds201GateAggressionSepModerate
		baseAggression = ds201GateAggressionGoodLow + (t * ds201GateAggressionGoodScale)
	default:
		// Excellent separation: maximum aggression
		baseAggression = ds201GateAggressionWide
	}

	// LRA adjustment: higher LRA = more dynamic content = reduce aggression
	// to preserve quiet expressive moments
	lraAdjustment := 0.0
	if lra > ds201GateAggressionLRAThreshold {
		lraAdjustment = (lra - ds201GateAggressionLRAThreshold) * ds201GateAggressionLRAScale
	}

	return clamp(baseAggression-lraAdjustment, ds201GateAggressionMin, ds201GateAggressionMax)
}

// calculateDS201GateThresholdLegacy uses the original noise-floor-based approach
// when SpeechProfile is unavailable.
func calculateDS201GateThresholdLegacy(
	noiseFloorDB, silencePeakDB, silenceCrestDB float64,
	ratio, lufsGap float64,
) float64 {
	var thresholdDB float64

	usePeakReference := silenceCrestDB > ds201GateCrestFactorThreshold &&
		silencePeakDB != 0 &&
		lufsGap < lufsGapExtreme

	if usePeakReference {
		thresholdDB = silencePeakDB + 3.0
	} else {
		minGapDB := ds201GateTargetReductionDB / (1.0 - 1.0/ratio)
		minGapThreshold := noiseFloorDB + minGapDB
		thresholdDB = max(minGapThreshold, ds201GateTargetThresholdDB)
	}

	thresholdDB = clamp(thresholdDB, ds201GateThresholdMinDB, ds201GateThresholdMaxDB)

	return DbToLinear(thresholdDB)
}

// calculateDS201GateThreshold determines threshold ensuring sufficient gap above noise
// for effective soft expansion using aggression-based positioning when SpeechProfile
// is available, falling back to legacy noise-floor-based approach otherwise.
//
// Aggression-based approach:
//   - Threshold = quietSpeech + (dynamicRange × aggression)
//   - Aggression scales with noise-to-speech separation and LRA
//   - Safety clamps ensure threshold stays between noise floor and speech RMS
//
// Legacy approach (no SpeechProfile):
//   - Threshold derived from noise floor + ratio-based gap
//   - Peak reference used for high-crest noise (bleed, transients)
func calculateDS201GateThreshold(
	noiseFloorDB, silencePeakDB, silenceCrestDB float64,
	ratio, lufsGap, lra float64,
	speechRMS, speechCrest float64,
) float64 {
	// Primary path: aggression-based positioning (requires SpeechProfile)
	if speechRMS < 0 && speechCrest > 0 {
		quietSpeechEstimate := speechRMS - speechCrest
		dynamicRange := speechCrest // Distance from quiet to RMS
		separation := quietSpeechEstimate - noiseFloorDB

		// Fall back to legacy if separation is too tight for reliable aggression
		if separation < 5.0 {
			return calculateDS201GateThresholdLegacy(
				noiseFloorDB, silencePeakDB, silenceCrestDB,
				ratio, lufsGap,
			)
		}

		aggression := calculateAggression(separation, lra)

		// Position threshold above quiet speech by fraction of dynamic range
		thresholdDB := quietSpeechEstimate + (dynamicRange * aggression)

		// Safety constraints
		noiseFloorLimit := noiseFloorDB + ds201GateThresholdNoiseMargin
		speechRMSLimit := speechRMS - ds201GateThresholdSpeechMargin

		if thresholdDB < noiseFloorLimit {
			thresholdDB = noiseFloorLimit
		} else if thresholdDB > speechRMSLimit {
			thresholdDB = speechRMSLimit
		}

		// Additional safety: respect global limits
		thresholdDB = clamp(thresholdDB, ds201GateThresholdMinDB, ds201GateThresholdMaxDB)

		return DbToLinear(thresholdDB)
	}

	// Fallback: legacy noise-floor-based approach (no SpeechProfile)
	return calculateDS201GateThresholdLegacy(
		noiseFloorDB, silencePeakDB, silenceCrestDB,
		ratio, lufsGap,
	)
}

// calculateDS201GateRatio determines ratio based on LRA (loudness range).
// Wide dynamics = gentle ratio to preserve expression - soft expander approach.
func calculateDS201GateRatio(lra float64) float64 {
	switch {
	case lra > ds201GateLRAWide:
		return ds201GateRatioGentle // Wide dynamics - preserve expression
	case lra > ds201GateLRAModerate:
		return ds201GateRatioMod // Moderate dynamics
	default:
		return ds201GateRatioTight // Narrow dynamics - tighter control OK
	}
}

// calculateDS201GateAttack determines attack time based on transient characteristics.
// Fast transients need fast attack to preserve word onsets, but not so fast as to click.
// Minimum 10ms prevents audible gain discontinuities when gate opens.
// MaxDifference is expressed as a fraction (0.0-1.0), convert to percentage.
func calculateDS201GateAttack(maxDiff, spectralFlux, spectralCrest float64) float64 {
	// MaxDifference is 0.0-1.0 fraction, convert to percentage for comparison
	maxDiffPercent := maxDiff * 100.0

	// Attack tiers adapted for speech (minimum 10ms to prevent click artifacts)
	// Faster attacks would cause audible gain discontinuities when gate opens
	var baseAttack float64
	switch {
	case maxDiffPercent > ds201GateMaxDiffExtreme || spectralCrest > ds201GateCrestExtreme:
		// Extreme transients - fastest attack, with 10ms floor to prevent click artifacts
		baseAttack = ds201GateAttackUltraFast
	case maxDiffPercent > ds201GateMaxDiffHigh || spectralCrest > 30.0:
		// Sharp transients - fast opening
		baseAttack = ds201GateAttackFast
	case maxDiffPercent > ds201GateMaxDiffMod:
		// Standard speech
		baseAttack = ds201GateAttackMod
	default:
		// Soft onsets - gentler OK
		baseAttack = ds201GateAttackSlow
	}

	// Bias faster for dynamic content
	if spectralFlux > ds201GateFluxDynamicThres {
		baseAttack *= 0.8
	}

	return clamp(baseAttack, ds201GateAttackUltraFast, 25.0)
}

// calculateDS201GateRelease determines release time based on content and noise character.
// Compensates for lack of hold parameter by extending release (+50ms).
//
// Entropy-based adaptation:
//   - Very tonal noise (entropy < 0.1): slowest release - hide pumping on pure hum/bleed
//   - Tonal noise (entropy < 0.12): slow release - some pumping hiding needed
//   - Mixed noise (entropy < 0.16): moderate release
//   - Broadband-ish (entropy >= 0.16): faster release - cut noise quickly without pumping risk
//
// LRA-based extension:
//   - Low LRA (<10 LU): speech at similar levels, gate opens/closes rapidly → pumping
//   - Very low LRA (<8 LU): maximum release extension to hide pumping
//
// This allows voices with more broadband room noise to benefit from
// tighter release that cuts noise faster when speech stops, while preserving the
// slow release for tonal bleed/hum that would otherwise pump audibly.
func calculateDS201GateRelease(spectralFlux, zcr, silenceEntropy, lra float64) float64 {
	var baseRelease float64

	switch {
	case spectralFlux < ds201GateFluxLow && zcr < ds201GateZCRLow:
		// Sustained speech with low activity
		baseRelease = ds201GateReleaseSustained
	case spectralFlux > ds201GateFluxHigh:
		// Dynamic content - more responsive
		baseRelease = ds201GateReleaseDynamic
	default:
		baseRelease = ds201GateReleaseMod
	}

	// Compensate for lack of hold parameter
	baseRelease += ds201GateReleaseHoldComp

	// Entropy-based release adjustment
	// Very tonal noise needs slowest release to hide pumping artifacts
	// Higher entropy (broadband-ish) allows faster release to cut noise quickly
	switch {
	case silenceEntropy < ds201GateReleaseEntropyVeryTonal:
		// Pure tonal (hum, bleed) - maximum release time
		baseRelease += ds201GateReleaseTonalComp
	case silenceEntropy < ds201GateReleaseEntropyTonal:
		// Tonal noise - slow release, reduced compensation
		baseRelease += ds201GateReleaseTonalComp * 0.7
	case silenceEntropy < ds201GateReleaseEntropyMixed:
		// Mixed character - moderate release, slight reduction
		// Don't add tonal comp, and reduce base slightly to cut noise faster
		baseRelease -= ds201GateReleaseEntropyReduce * 0.3
	default:
		// Broadband-ish noise - faster release to cut noise quickly
		// No tonal compensation, and reduce base release
		baseRelease -= ds201GateReleaseEntropyReduce
	}

	// LRA-based release extension
	// Low dynamic range audio has speech at similar levels throughout, causing
	// the gate to open/close rapidly on adjacent segments → audible pumping.
	// Longer release smooths out these transitions.
	switch {
	case lra < ds201GateReleaseLRAVeryLow:
		// Very low LRA (<8 LU) - maximum extension
		baseRelease += ds201GateReleaseLRAMaxExt
	case lra < ds201GateReleaseLRALow:
		// Low LRA (<10 LU) - proportional extension
		// Scale from full extension at 8 LU to zero at 10 LU
		extensionScale := (ds201GateReleaseLRALow - lra) / (ds201GateReleaseLRALow - ds201GateReleaseLRAVeryLow)
		baseRelease += ds201GateReleaseLRAExtension * extensionScale
	}

	return clamp(baseRelease, float64(ds201GateReleaseMin), float64(ds201GateReleaseMax))
}

// calculateDS201GateRangeDB determines maximum attenuation depth in dB based on noise character.
// Tonal noise (bleed, hum) sounds worse when hard-gated - use gentler range.
// Broadband noise can be gated more aggressively.
// Returns unclamped dB value for further adjustment by caller.
func calculateDS201GateRangeDB(silenceEntropy, noiseFloorDB float64) float64 {
	var rangeDB float64

	switch {
	case silenceEntropy < ds201GateEntropyTonal:
		rangeDB = ds201GateRangeTonalDB // Tonal - gentle
	case silenceEntropy < ds201GateEntropyMixed:
		rangeDB = ds201GateRangeMixedDB // Mixed - moderate
	default:
		rangeDB = ds201GateRangeBroadbandDB // Broadband - aggressive
	}

	// Can go deeper if very clean recording
	if noiseFloorDB < -70 {
		rangeDB += ds201GateRangeCleanBoost // More negative = deeper
	}

	return rangeDB
}

// calculateDS201GateKnee determines knee softness based on spectral crest.
// Dynamic content with prominent peaks benefits from softer knee.
func calculateDS201GateKnee(spectralCrest float64) float64 {
	switch {
	case spectralCrest > ds201GateSpectralCrestHigh:
		return ds201GateKneeSoft // Dynamic - soft engagement
	case spectralCrest > ds201GateSpectralCrestMod:
		return ds201GateKneeMod // Standard
	default:
		return ds201GateKneeSharp // Less dynamic - sharper OK
	}
}

// calculateDS201GateDetection determines whether to use RMS or peak detection.
// RMS is safer for speech and handles tonal bleed better.
// Peak provides tighter tracking for very clean recordings.
func calculateDS201GateDetection(silenceEntropy, silenceCrestDB float64) string {
	// Tonal noise or high crest in silence - use RMS
	if silenceEntropy < ds201GateEntropyTonal || silenceCrestDB > ds201GateSilenceCrestThreshold {
		return "rms"
	}

	// Very clean with low crest - can use peak for tighter tracking
	if silenceEntropy > ds201GateEntropyClean && silenceCrestDB < 15 {
		return "peak"
	}

	// Default: RMS is safer for speech
	return "rms"
}
