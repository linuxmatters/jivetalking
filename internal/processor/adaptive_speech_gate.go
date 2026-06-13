package processor

const (
	// LUFS gap threshold for adaptive processing intensity
	lufsGapExtreme = 25.0 // dB - extreme gap, gate needs special handling

	// Gentle gate mode: for extreme LUFS gap + low LRA
	// Very quiet recordings with uniform levels cause the gate's soft expansion
	// to apply varying gain reduction across similar speech levels, creating
	// volume modulation ("hunting"). Use gentler parameters to prevent this.
	speechGateGentleLRAThreshold = 10.0 // LU - below this with extreme LUFS gap triggers gentle mode
	speechGateGentleRatio        = 1.2  // Minimal gain variation in expansion zone
	speechGateGentleKnee         = 2.0  // Sharper transition reduces hunting

	// Threshold calculation: ensures sufficient gap above noise for effective soft expansion
	speechGateThresholdMinDB       = -80.0 // dB - minimum threshold (allows speech guard to protect quiet content)
	speechGateThresholdMaxDB       = -25.0 // dB - never gate above this (would cut speech)
	speechGateCrestFactorThreshold = 20.0  // dB - above this, use peak reference instead of RMS
	speechGateTargetReductionDB    = 12.0  // dB - target noise reduction from soft expander
	speechGateTargetThresholdDB    = -40.0 // dB - target threshold for clean recordings (quiet speech/breath level)

	// Base aggression tiers (by separation in dB)
	speechGateAggressionSepTight    = 10.0 // dB - below: minimal separation, conservative
	speechGateAggressionSepModerate = 15.0 // dB - moderate separation
	speechGateAggressionSepGood     = 20.0 // dB - good separation

	speechGateAggressionTight     = 0.30 // For separation < 10 dB
	speechGateAggressionModLow    = 0.35 // Base for 10-15 dB separation
	speechGateAggressionModScale  = 0.02 // Per-dB scale in moderate tier
	speechGateAggressionGoodLow   = 0.45 // Base for 15-20 dB separation
	speechGateAggressionGoodScale = 0.02 // Per-dB scale in good tier
	speechGateAggressionWide      = 0.55 // For separation >= 20 dB

	// LRA adjustment to aggression
	speechGateAggressionLRAThreshold = 12.0  // LU - above: start reducing aggression
	speechGateAggressionLRAScale     = 0.015 // Reduction per LU above threshold

	// Aggression clamps
	speechGateAggressionMin = 0.25 // Never too conservative
	speechGateAggressionMax = 0.60 // Never too aggressive

	// Safety margins
	speechGateThresholdSpeechMargin = 10.0 // dB - minimum gap below speech RMS
	speechGateThresholdNoiseMargin  = 5.0  // dB - room for soft expander action

	// Ratio: based on LRA (loudness range)
	speechGateLRAWide     = 15.0 // LU - above: wide dynamics, gentle ratio
	speechGateLRAModerate = 10.0 // LU - above: moderate dynamics
	speechGateRatioGentle = 1.5  // For wide LRA (preserve expression)
	speechGateRatioMod    = 2.0  // For moderate LRA
	speechGateRatioTight  = 2.5  // For narrow LRA (tighter control OK)

	// Attack: fixed 10 ms. Speech never produces transients fast enough to need
	// a shorter attack, so a single floor value is correct and is not adapted.
	speechGateAttackMS = 10.0 // ms - fixed minimum attack (prevents click artifacts on gate open)

	// Release: based on speech sustain (flux/ZCR) and LRA.
	// No hold parameter exists - release must compensate.
	speechGateFluxLow          = 0.01 // Low flux threshold (sustained-speech test)
	speechGateZCRLow           = 0.08 // Low zero crossings rate (sustained-speech test)
	speechGateReleaseSustained = 300  // ms - for sustained speech
	speechGateReleaseMod       = 250  // ms - standard
	speechGateReleaseHoldComp  = 50   // ms - compensation for lack of hold parameter
	speechGateReleaseTonalComp = 75   // ms - extra to hide pumping on tonal bleed
	speechGateReleaseMin       = 150  // ms - minimum release
	speechGateReleaseMax       = 600  // ms - maximum release (increased for low LRA)

	// LRA-based release extension (low dynamic range = more pumping risk)
	// When speech has narrow loudness range (<10 LU), gate opens/closes rapidly
	// on similar-level segments, causing audible pumping. Longer release helps.
	speechGateReleaseLRALow       = 10.0 // LU - below: low dynamic range, extend release
	speechGateReleaseLRAVeryLow   = 8.0  // LU - below: very low LRA, maximum extension
	speechGateReleaseLRAExtension = 100  // ms - extension for low LRA audio
	speechGateReleaseLRAMaxExt    = 150  // ms - maximum extension for very low LRA

	// Range: driven by noise floor alone. Clean recordings (floor below
	// -70 dBFS) take a deeper range; everything else takes the gentle range.
	// The user may tune these by ear after auditioning.
	speechGateRangeCleanFloorDB = -70.0 // dBFS - below this the recording counts as clean
	speechGateRangeCleanDB      = -22.0 // dB - depth for clean recordings
	speechGateRangeStandardDB   = -16.0 // dB - depth for everything else
	speechGateRangeMinDB        = -36.0 // dB - minimum (deepest)
	speechGateRangeMaxDB        = -12.0 // dB - maximum (gentlest)

	// Knee: fixed. Spectral crest is the wrong signal to key it off (per the
	// de-esser/compressor reviews), so the knee is a single value. Gentle mode is the
	// only override. The user may tune this by ear.
	speechGateKneeFixed = 3.0 // standard knee for all content

	// Detection: fixed RMS, the safe choice for speech and tonal bleed. A peak
	// branch would need room-tone entropy > 0.7, which podcast speech never
	// reaches, so RMS is the only mode used.

	speechGateDefaultThreshold = 0.01 // -40dBFS
)

// tuneSpeechGate adapts the noise gate parameters based on Pass 1 measurements.
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
func tuneSpeechGate(config *EffectiveFilterConfig, diagnostics *AdaptiveDiagnostics, measurements *AudioMeasurements) {
	if diagnostics != nil {
		diagnostics.SpeechGateGentleMode = false
		diagnostics.SpeechGateAggression = 0
		diagnostics.SpeechGateDynamicRange = 0
		diagnostics.SpeechGateQuietSpeechEstimate = 0
		diagnostics.SpeechGateSpeechSeparation = 0
		diagnostics.SpeechGateSpeechHeadroom = 0
		diagnostics.SpeechGateThresholdUnclamped = 0
		diagnostics.SpeechGateClampReason = ""
	}

	// Room-tone peak/crest feed only the low-separation threshold guard
	// (NoiseProfile is extracted from the elected room-tone region).
	var roomToneCrest, roomTonePeak float64

	if measurements.Regions.NoiseProfile != nil {
		roomToneCrest = measurements.Regions.NoiseProfile.CrestFactor
		roomTonePeak = measurements.Regions.NoiseProfile.PeakLevel
	} else {
		// NoiseProfile unavailable - conservative defaults for the threshold guard.
		roomToneCrest = 15.0 // Moderate crest, no peak reference
		roomTonePeak = 0     // Falls back to NoiseFloor for threshold
	}

	// Calculate LUFS gap for threshold decision
	lufsGap := config.Loudnorm.TargetI - measurements.Loudness.InputI
	if lufsGap < 0 {
		lufsGap = 0
	}

	// 2. Ratio: based on LRA (loudness range) - soft expander approach
	// Calculate ratio FIRST since threshold depends on it
	config.SpeechGate.Ratio = calculateSpeechGateRatio(measurements.Loudness.InputLRA)

	// Extract speech measurements (zero values if no profile)
	var speechRMS, speechCrest float64
	if measurements.Regions.SpeechProfile != nil {
		speechRMS = measurements.Regions.SpeechProfile.RMSLevel
		speechCrest = measurements.Regions.SpeechProfile.CrestFactor
	}

	// 1. Threshold: sits above noise/bleed peaks, below quiet speech
	// Gap is derived from ratio to achieve target reduction
	config.SpeechGate.Threshold = calculateSpeechGateThreshold(
		measurements.Noise.Floor,
		roomTonePeak,
		roomToneCrest,
		config.SpeechGate.Ratio,
		lufsGap,
		measurements.Loudness.InputLRA,
		speechRMS,
		speechCrest,
	)

	// Track threshold calculation diagnostics
	if measurements.Regions.SpeechProfile != nil && diagnostics != nil {
		quietSpeech := measurements.Regions.SpeechProfile.RMSLevel - measurements.Regions.SpeechProfile.CrestFactor
		separation := quietSpeech - measurements.Noise.Floor

		// Calculate aggression for diagnostics
		aggression := calculateAggression(separation, measurements.Loudness.InputLRA)
		dynamicRange := measurements.Regions.SpeechProfile.CrestFactor

		// Calculate unclamped threshold for diagnostics
		thresholdUnclamped := quietSpeech + (dynamicRange * aggression)

		// Determine clamp reason
		noiseFloorLimit := measurements.Noise.Floor + speechGateThresholdNoiseMargin
		speechRMSLimit := measurements.Regions.SpeechProfile.RMSLevel - speechGateThresholdSpeechMargin
		actualThreshold := LinearAmplitude(config.SpeechGate.Threshold).Decibels().Float64()

		var clampReason string
		switch {
		case thresholdUnclamped < noiseFloorLimit && actualThreshold >= noiseFloorLimit:
			clampReason = "noise_floor"
		case thresholdUnclamped > speechRMSLimit && actualThreshold <= speechRMSLimit:
			clampReason = "speech_rms"
		default:
			clampReason = "none"
		}

		diagnostics.SpeechGateAggression = aggression
		diagnostics.SpeechGateDynamicRange = dynamicRange
		diagnostics.SpeechGateQuietSpeechEstimate = quietSpeech
		diagnostics.SpeechGateSpeechSeparation = separation
		diagnostics.SpeechGateThresholdUnclamped = thresholdUnclamped
		diagnostics.SpeechGateClampReason = clampReason
		diagnostics.SpeechGateSpeechHeadroom = quietSpeech - actualThreshold
	}

	// 3. Attack: fixed 10ms floor (transient tiers never fire on speech)
	config.SpeechGate.Attack = speechGateAttackMS

	// 4. Release: based on speech sustain (flux/ZCR) and LRA
	// Includes +50ms compensation for lack of Hold parameter and +75ms to hide
	// pumping on tonal bleed; low LRA extends release to prevent pumping.
	config.SpeechGate.Release = calculateSpeechGateRelease(
		measurements.Spectral.Flux,
		measurements.Dynamics.ZeroCrossingsRate,
		measurements.Loudness.InputLRA,
	)

	// 5. Range: driven by noise floor (clean recordings take a deeper range)
	rangeDB := calculateSpeechGateRangeDB(measurements.Noise.Floor)

	// Clamp range and convert to linear
	rangeDB = max(speechGateRangeMinDB, min(rangeDB, speechGateRangeMaxDB))
	config.SpeechGate.Range = Decibels(rangeDB).LinearAmplitude().Float64()

	// 6. Knee: fixed
	config.SpeechGate.Knee = speechGateKneeFixed

	// 7. Detection: fixed RMS (safe for speech and tonal bleed)
	config.SpeechGate.Detection = "rms"

	// Note: Makeup gain left at default (1.0 unity) - loudnorm handles all level adjustment

	// Gentle gate mode override: for extreme LUFS gap + low LRA
	// Very quiet recordings with uniform levels cause the gate's soft expansion
	// to apply varying gain reduction across similar speech levels, creating
	// volume modulation ("hunting"). Override to gentler parameters.
	if lufsGap >= lufsGapExtreme && measurements.Loudness.InputLRA < speechGateGentleLRAThreshold {
		config.SpeechGate.Ratio = speechGateGentleRatio
		config.SpeechGate.Knee = speechGateGentleKnee
		if diagnostics != nil {
			diagnostics.SpeechGateGentleMode = true
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
	case separation < speechGateAggressionSepTight:
		// Tight separation: conservative positioning
		baseAggression = speechGateAggressionTight
	case separation < speechGateAggressionSepModerate:
		// Moderate separation: scale 0.35-0.45
		t := separation - speechGateAggressionSepTight
		baseAggression = speechGateAggressionModLow + (t * speechGateAggressionModScale)
	case separation < speechGateAggressionSepGood:
		// Good separation: scale 0.45-0.55
		t := separation - speechGateAggressionSepModerate
		baseAggression = speechGateAggressionGoodLow + (t * speechGateAggressionGoodScale)
	default:
		// Excellent separation: maximum aggression
		baseAggression = speechGateAggressionWide
	}

	// LRA adjustment: higher LRA = more dynamic content = reduce aggression
	// to preserve quiet expressive moments
	lraAdjustment := 0.0
	if lra > speechGateAggressionLRAThreshold {
		lraAdjustment = (lra - speechGateAggressionLRAThreshold) * speechGateAggressionLRAScale
	}

	return max(speechGateAggressionMin, min(baseAggression-lraAdjustment, speechGateAggressionMax))
}

// calculateSpeechGateThresholdLegacy positions the threshold from the noise floor
// (or room-tone peak for high-crest bleed). It is the low-separation guard:
// reached only when calculateSpeechGateThreshold finds < 5 dB of speech-to-noise
// separation (where the aggression maths is unreliable), or when no SpeechProfile
// is elected. Speech sources reliably elect a profile, so the separation path is
// the live one. roomTonePeakDB and roomToneCrestDB describe the noise profile
// extracted from the elected room-tone region.
func calculateSpeechGateThresholdLegacy(
	noiseFloorDB, roomTonePeakDB, roomToneCrestDB float64,
	ratio, lufsGap float64,
) float64 {
	var thresholdDB float64

	usePeakReference := roomToneCrestDB > speechGateCrestFactorThreshold &&
		roomTonePeakDB != 0 &&
		lufsGap < lufsGapExtreme

	if usePeakReference {
		thresholdDB = roomTonePeakDB + 3.0
	} else {
		minGapDB := speechGateTargetReductionDB / (1.0 - 1.0/ratio)
		minGapThreshold := noiseFloorDB + minGapDB
		thresholdDB = max(minGapThreshold, speechGateTargetThresholdDB)
	}

	thresholdDB = max(speechGateThresholdMinDB, min(thresholdDB, speechGateThresholdMaxDB))

	return Decibels(thresholdDB).LinearAmplitude().Float64()
}

// calculateSpeechGateThreshold determines threshold ensuring sufficient gap above noise
// for effective soft expansion using aggression-based positioning when SpeechProfile
// is available, falling back to legacy noise-floor-based approach otherwise.
//
// Aggression-based approach:
//   - Threshold = quietSpeech + (dynamicRange × aggression)
//   - Aggression scales with noise-to-speech separation and LRA
//   - Safety clamps ensure threshold stays between noise floor and speech RMS
//
// Low-separation guard (calculateSpeechGateThresholdLegacy):
//   - When speech-to-noise separation is < 5 dB the aggression maths is
//     unreliable, so fall back to the noise-floor-based path
//   - Peak reference used for high-crest noise (bleed, transients)
//
// roomTonePeakDB and roomToneCrestDB describe the noise profile extracted from
// the elected room-tone region.
func calculateSpeechGateThreshold(
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
			return calculateSpeechGateThresholdLegacy(
				noiseFloorDB, roomTonePeakDB, roomToneCrestDB,
				ratio, lufsGap,
			)
		}

		aggression := calculateAggression(separation, lra)

		// Position threshold above quiet speech by fraction of dynamic range
		thresholdDB := quietSpeechEstimate + (dynamicRange * aggression)

		// Safety constraints
		noiseFloorLimit := noiseFloorDB + speechGateThresholdNoiseMargin
		speechRMSLimit := speechRMS - speechGateThresholdSpeechMargin

		if thresholdDB < noiseFloorLimit {
			thresholdDB = noiseFloorLimit
		} else if thresholdDB > speechRMSLimit {
			thresholdDB = speechRMSLimit
		}

		// Additional safety: respect global limits
		thresholdDB = max(speechGateThresholdMinDB, min(thresholdDB, speechGateThresholdMaxDB))

		return Decibels(thresholdDB).LinearAmplitude().Float64()
	}

	// Fallback: legacy noise-floor-based approach (no SpeechProfile)
	return calculateSpeechGateThresholdLegacy(
		noiseFloorDB, roomTonePeakDB, roomToneCrestDB,
		ratio, lufsGap,
	)
}

// calculateSpeechGateRatio determines ratio based on LRA (loudness range).
// Wide dynamics = gentle ratio to preserve expression - soft expander approach.
func calculateSpeechGateRatio(lra float64) float64 {
	switch {
	case lra > speechGateLRAWide:
		return speechGateRatioGentle // Wide dynamics - preserve expression
	case lra > speechGateLRAModerate:
		return speechGateRatioMod // Moderate dynamics
	default:
		return speechGateRatioTight // Narrow dynamics - tighter control OK
	}
}

// calculateSpeechGateRelease determines release time from speech sustain and LRA.
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
func calculateSpeechGateRelease(spectralFlux, zcr, lra float64) float64 {
	var baseRelease float64

	if spectralFlux < speechGateFluxLow && zcr < speechGateZCRLow {
		// Sustained speech with low activity
		baseRelease = speechGateReleaseSustained
	} else {
		baseRelease = speechGateReleaseMod
	}

	// Compensate for lack of hold parameter, plus fixed tonal-bleed allowance.
	baseRelease += speechGateReleaseHoldComp
	baseRelease += speechGateReleaseTonalComp

	// LRA-based release extension
	// Low dynamic range audio has speech at similar levels throughout, causing
	// the gate to open/close rapidly on adjacent segments → audible pumping.
	// Longer release smooths out these transitions.
	switch {
	case lra < speechGateReleaseLRAVeryLow:
		// Very low LRA (<8 LU) - maximum extension
		baseRelease += speechGateReleaseLRAMaxExt
	case lra < speechGateReleaseLRALow:
		// Low LRA (<10 LU) - proportional extension
		// Scale from full extension at 8 LU to zero at 10 LU
		extensionScale := (speechGateReleaseLRALow - lra) / (speechGateReleaseLRALow - speechGateReleaseLRAVeryLow)
		baseRelease += speechGateReleaseLRAExtension * extensionScale
	}

	return max(float64(speechGateReleaseMin), min(baseRelease, float64(speechGateReleaseMax)))
}

// calculateSpeechGateRangeDB determines maximum attenuation depth in dB from the
// noise floor. Clean recordings (floor below -70 dBFS) take the deeper clean
// range; everything else takes the gentle standard range. Returns an unclamped
// dB value for the caller to clamp.
func calculateSpeechGateRangeDB(noiseFloorDB float64) float64 {
	if noiseFloorDB < speechGateRangeCleanFloorDB {
		return speechGateRangeCleanDB
	}
	return speechGateRangeStandardDB
}
