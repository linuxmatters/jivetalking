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

	// Attack: fixed. Every corpus stem produces the 10ms floor; the transient
	// tiers above it never fire on speech, so attack is no longer adaptive.
	ds201GateAttackMS = 10.0 // ms - fixed minimum attack (prevents click artifacts on gate open)

	// Release: based on speech sustain (flux/ZCR) and LRA.
	// No hold parameter exists - release must compensate.
	ds201GateFluxLow          = 0.01 // Low flux threshold (sustained-speech test)
	ds201GateZCRLow           = 0.08 // Low zero crossings rate (sustained-speech test)
	ds201GateReleaseSustained = 300  // ms - for sustained speech
	ds201GateReleaseMod       = 250  // ms - standard
	ds201GateReleaseHoldComp  = 50   // ms - compensation for lack of hold parameter
	ds201GateReleaseTonalComp = 75   // ms - extra to hide pumping on tonal bleed
	ds201GateReleaseMin       = 150  // ms - minimum release
	ds201GateReleaseMax       = 600  // ms - maximum release (increased for low LRA)

	// LRA-based release extension (low dynamic range = more pumping risk)
	// When speech has narrow loudness range (<10 LU), gate opens/closes rapidly
	// on similar-level segments, causing audible pumping. Longer release helps.
	ds201GateReleaseLRALow       = 10.0 // LU - below: low dynamic range, extend release
	ds201GateReleaseLRAVeryLow   = 8.0  // LU - below: very low LRA, maximum extension
	ds201GateReleaseLRAExtension = 100  // ms - extension for low LRA audio
	ds201GateReleaseLRAMaxExt    = 150  // ms - maximum extension for very low LRA

	// Range: driven by noise floor alone. Clean recordings (floor below
	// -70 dBFS) take a deeper range; everything else takes the gentle range.
	// The user may tune these by ear after auditioning.
	ds201GateRangeCleanFloorDB = -70.0 // dBFS - below this the recording counts as clean
	ds201GateRangeCleanDB      = -22.0 // dB - depth for clean recordings
	ds201GateRangeStandardDB   = -16.0 // dB - depth for everything else
	ds201GateRangeMinDB        = -36.0 // dB - minimum (deepest)
	ds201GateRangeMaxDB        = -12.0 // dB - maximum (gentlest)

	// Knee: fixed. The spectral-crest signal it used to key off is the wrong
	// signal (per the de-esser/LA-2A reviews). The user may tune this by ear.
	ds201GateKneeFixed = 3.0 // standard knee for all content

	// Detection: fixed RMS. RMS is the safe choice for speech and tonal bleed;
	// the peak branch needed entropy > 0.7, which the corpus never reaches.

	ds201DefaultGateThreshold = 0.01 // -40dBFS
)

// tuneDS201Gate adapts the noise gate parameters based on Pass 1 measurements.
//
// Parameters are tuned as follows:
//   - Threshold: aggression-based positioning between quiet speech and speech RMS
//     (low-separation guard falls back to the noise-floor-based path)
//   - Ratio: based on LRA (wide dynamics = gentle ratio)
//   - Release: based on speech sustain (flux/ZCR) + LRA extension + hold compensation
//   - Range: driven by noise floor (clean recordings take a deeper range)
//   - Attack: fixed 10 ms (clamp floor, prevents click artifacts)
//   - Knee: fixed 3.0
//   - Detection: fixed RMS (safe for speech and tonal bleed)
//   - Makeup: 1.0 (loudness normalisation handles level compensation)
//
// Room-tone peak/crest are read from the noise profile extracted from the
// elected room-tone region and feed only the low-separation threshold guard.
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

	// Room-tone peak/crest feed only the low-separation threshold guard
	// (NoiseProfile is extracted from the elected room-tone region).
	var roomToneCrest, roomTonePeak float64

	if measurements.NoiseProfile != nil {
		roomToneCrest = measurements.NoiseProfile.CrestFactor
		roomTonePeak = measurements.NoiseProfile.PeakLevel
	} else {
		// NoiseProfile unavailable - conservative defaults for the threshold guard.
		roomToneCrest = 15.0 // Moderate crest, no peak reference
		roomTonePeak = 0     // Falls back to NoiseFloor for threshold
	}

	// Calculate LUFS gap for threshold decision
	lufsGap := config.Loudnorm.TargetI - measurements.InputI
	if lufsGap < 0 {
		lufsGap = 0
	}

	// 2. Ratio: based on LRA (loudness range) - soft expander approach
	// Calculate ratio FIRST since threshold depends on it
	config.DS201Gate.Ratio = calculateDS201GateRatio(measurements.InputLRA)

	// Extract speech measurements (zero values if no profile)
	var speechRMS, speechCrest float64
	if measurements.SpeechProfile != nil {
		speechRMS = measurements.SpeechProfile.RMSLevel
		speechCrest = measurements.SpeechProfile.CrestFactor
	}

	// 1. Threshold: sits above noise/bleed peaks, below quiet speech
	// Gap is derived from ratio to achieve target reduction
	config.DS201Gate.Threshold = calculateDS201GateThreshold(
		measurements.NoiseFloor,
		roomTonePeak,
		roomToneCrest,
		config.DS201Gate.Ratio,
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
		actualThreshold := LinearAmplitude(config.DS201Gate.Threshold).Decibels().Float64()

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

	// 3. Attack: fixed 10ms floor (transient tiers never fire on speech)
	config.DS201Gate.Attack = ds201GateAttackMS

	// 4. Release: based on speech sustain (flux/ZCR) and LRA
	// Includes +50ms compensation for lack of Hold parameter and +75ms to hide
	// pumping on tonal bleed; low LRA extends release to prevent pumping.
	config.DS201Gate.Release = calculateDS201GateRelease(
		measurements.Spectral.Flux,
		measurements.ZeroCrossingsRate,
		measurements.InputLRA,
	)

	// 5. Range: driven by noise floor (clean recordings take a deeper range)
	rangeDB := calculateDS201GateRangeDB(measurements.NoiseFloor)

	// Clamp range and convert to linear
	rangeDB = max(ds201GateRangeMinDB, min(rangeDB, ds201GateRangeMaxDB))
	config.DS201Gate.Range = Decibels(rangeDB).LinearAmplitude().Float64()

	// 6. Knee: fixed
	config.DS201Gate.Knee = ds201GateKneeFixed

	// 7. Detection: fixed RMS (safe for speech and tonal bleed)
	config.DS201Gate.Detection = "rms"

	// Note: Makeup gain left at default (1.0 unity) - loudnorm handles all level adjustment

	// Gentle gate mode override: for extreme LUFS gap + low LRA
	// Very quiet recordings with uniform levels cause the gate's soft expansion
	// to apply varying gain reduction across similar speech levels, creating
	// volume modulation ("hunting"). Override to gentler parameters.
	if lufsGap >= lufsGapExtreme && measurements.InputLRA < ds201GateGentleLRAThreshold {
		config.DS201Gate.Ratio = ds201GateGentleRatio
		config.DS201Gate.Knee = ds201GateGentleKnee
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

	return max(ds201GateAggressionMin, min(baseAggression-lraAdjustment, ds201GateAggressionMax))
}

// calculateDS201GateThresholdLegacy positions the threshold from the noise floor
// (or room-tone peak for high-crest bleed). Since commit 098ef6c every corpus stem
// elects a SpeechProfile, so this path is reached only via the <5 dB speech-to-noise
// separation guard in calculateDS201GateThreshold, not via a missing profile.
// roomTonePeakDB and roomToneCrestDB describe the noise profile extracted from the
// elected room-tone region.
func calculateDS201GateThresholdLegacy(
	noiseFloorDB, roomTonePeakDB, roomToneCrestDB float64,
	ratio, lufsGap float64,
) float64 {
	var thresholdDB float64

	usePeakReference := roomToneCrestDB > ds201GateCrestFactorThreshold &&
		roomTonePeakDB != 0 &&
		lufsGap < lufsGapExtreme

	if usePeakReference {
		thresholdDB = roomTonePeakDB + 3.0
	} else {
		minGapDB := ds201GateTargetReductionDB / (1.0 - 1.0/ratio)
		minGapThreshold := noiseFloorDB + minGapDB
		thresholdDB = max(minGapThreshold, ds201GateTargetThresholdDB)
	}

	thresholdDB = max(ds201GateThresholdMinDB, min(thresholdDB, ds201GateThresholdMaxDB))

	return Decibels(thresholdDB).LinearAmplitude().Float64()
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
// Low-separation guard (calculateDS201GateThresholdLegacy):
//   - When speech-to-noise separation is < 5 dB the aggression maths is
//     unreliable, so fall back to the noise-floor-based path
//   - Peak reference used for high-crest noise (bleed, transients)
//
// roomTonePeakDB and roomToneCrestDB describe the noise profile extracted from
// the elected room-tone region.
func calculateDS201GateThreshold(
	noiseFloorDB, roomTonePeakDB, roomToneCrestDB float64,
	ratio, lufsGap, lra float64,
	speechRMS, speechCrest float64,
) float64 {
	// Primary path: aggression-based positioning (requires SpeechProfile)
	if speechRMS < 0 && speechCrest > 0 {
		quietSpeechEstimate := speechRMS - speechCrest
		dynamicRange := speechCrest // Distance from quiet to RMS
		separation := quietSpeechEstimate - noiseFloorDB

		// Low-separation guard: too tight for reliable aggression maths
		if separation < 5.0 {
			return calculateDS201GateThresholdLegacy(
				noiseFloorDB, roomTonePeakDB, roomToneCrestDB,
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
		thresholdDB = max(ds201GateThresholdMinDB, min(thresholdDB, ds201GateThresholdMaxDB))

		return Decibels(thresholdDB).LinearAmplitude().Float64()
	}

	// Fallback: legacy noise-floor-based approach (no SpeechProfile)
	return calculateDS201GateThresholdLegacy(
		noiseFloorDB, roomTonePeakDB, roomToneCrestDB,
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

// calculateDS201GateRelease determines release time from speech sustain and LRA.
// Compensates for lack of hold parameter by extending release (+50ms) and adds a
// fixed +75ms to hide pumping on tonal bleed.
//
// Speech-sustain split:
//   - Sustained speech (flux < 0.01 AND ZCR < 0.08): slow release
//   - Everything else: standard release
//
// LRA-based extension:
//   - Low LRA (<10 LU): speech at similar levels, gate opens/closes rapidly → pumping
//   - Very low LRA (<8 LU): maximum release extension to hide pumping
//
// The elected room tone is tonal across the corpus (entropy pinned far below the
// old tiering thresholds), so the tonal compensation is applied as a fixed term
// rather than tiered on entropy.
func calculateDS201GateRelease(spectralFlux, zcr, lra float64) float64 {
	var baseRelease float64

	if spectralFlux < ds201GateFluxLow && zcr < ds201GateZCRLow {
		// Sustained speech with low activity
		baseRelease = ds201GateReleaseSustained
	} else {
		baseRelease = ds201GateReleaseMod
	}

	// Compensate for lack of hold parameter, plus fixed tonal-bleed allowance.
	baseRelease += ds201GateReleaseHoldComp
	baseRelease += ds201GateReleaseTonalComp

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

	return max(float64(ds201GateReleaseMin), min(baseRelease, float64(ds201GateReleaseMax)))
}

// calculateDS201GateRangeDB determines maximum attenuation depth in dB from the
// noise floor. Clean recordings (floor below -70 dBFS) take the deeper clean
// range; everything else takes the gentle standard range. Returns an unclamped
// dB value for the caller to clamp.
func calculateDS201GateRangeDB(noiseFloorDB float64) float64 {
	if noiseFloorDB < ds201GateRangeCleanFloorDB {
		return ds201GateRangeCleanDB
	}
	return ds201GateRangeStandardDB
}
