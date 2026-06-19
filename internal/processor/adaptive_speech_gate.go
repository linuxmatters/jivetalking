package processor

const (
	// LUFS gap threshold used only by the no-profile legacy threshold path: above
	// this gap the peak-reference branch is disabled (the recording is too quiet
	// for a room-tone peak to be a trustworthy threshold anchor). Anti-hunting
	// comes from the narrow-separation depth reduction, not a gentle-mode override.
	lufsGapExtreme = 25.0 // dB - extreme gap, disables the legacy peak-reference branch

	// Threshold calculation: ensures sufficient gap above noise for effective soft expansion
	speechGateThresholdMinDB       = -80.0 // dB - minimum threshold (allows speech guard to protect quiet content)
	speechGateThresholdMaxDB       = -25.0 // dB - never gate above this (would cut speech)
	speechGateCrestFactorThreshold = 20.0  // dB - above this, use peak reference instead of RMS
	speechGateTargetReductionDB    = 12.0  // dB - target noise reduction from soft expander
	speechGateTargetThresholdDB    = -40.0 // dB - target threshold for clean recordings (quiet speech/breath level)

	// Voiced-anchored threshold placement. The threshold is pinned a fixed margin
	// below the voiced-speech low percentile (p10), the soft edge of speech measured
	// over the elected region. Sitting below that edge means the gate never attenuates
	// a voiced word, even its quietest tail. The noise margin is the clearance above
	// the noise high percentile (p95) the threshold would like to keep; it detects a
	// narrow gap ONLY (when the speech-side placement cannot also clear the loud
	// noise). On a narrow gap the threshold stays on the speech side and depth backs
	// off rather than raising the threshold into the voice.
	speechGateThresholdSpeechMarginDB = 6.0 // dB - how far below voiced p10 the threshold sits (soft-word safety margin)
	speechGateThresholdNoiseMarginDB  = 6.0 // dB - clearance above noise p95 used only to detect a narrow gap

	// Ratio: based on LRA (loudness range)
	speechGateLRAWide     = 15.0 // LU - above: wide dynamics, gentle ratio
	speechGateRatioGentle = 1.5  // For wide LRA (preserve expression)
	speechGateRatioMod    = 2.0  // Cap: a soft expander, never a hard gate

	// Attack: fixed 5 ms. Consonant onsets carry the plosive and fricative attack,
	// and that energy lands in the first 15 to 70 ms of a word. The gate must open
	// within a few ms or it shaves that attack and dulls intelligibility, so the
	// floor sits at the safe-fast end of the 3 to 5 ms band rather than at 10 ms.
	speechGateAttackMS = 5.0 // ms - fixed fast attack (opens before the consonant onset is shaved)

	// Release: fixed 200 ms, with the hold folded in. agate has no hold parameter,
	// so the release alone holds the gate open across the short gaps inside speech;
	// 200 ms is long enough to ride those gaps without pumping and short enough to
	// close cleanly at word ends. A single fixed value, not a stacked flux/ZCR/LRA
	// sum.
	speechGateReleaseFixedMS = 200.0 // ms - fixed release (hold folded in)

	// Range: fixed 14 dB of attenuation, the midpoint of the 12 to 15 dB
	// transparent band (moderate depth, never a full mute, so the floor under
	// speech stays natural rather than pumping to silence).
	speechGateDepthFixedDB = 14.0 // dB - fixed attenuation depth (transparent band midpoint)

	// Range on a narrow gap: a gentler fixed depth. A narrow gap means little
	// headroom between the quietest voiced speech and the loud noise, so gating
	// the full depth would pump the floor. Back off to a shallower fixed cut
	// (never a full mute, never 0). Two fixed levels only, normal and narrow, not
	// proportional to separation.
	speechGateDepthNarrowDB = 8.0 // dB - reduced attenuation depth on a narrow gap

	// Knee: fixed, within the 3 to 10 dB band. Spectral crest is the wrong signal
	// to key it off, so the knee is a single value. A soft knee stands in for the
	// hysteresis agate lacks: it smooths the
	// open/close boundary so the gate does not chatter on level wobble near the
	// threshold. There is no override; the knee is the same for all content.
	speechGateKneeFixed = 3.0 // standard knee for all content (3 to 10 dB band)

	// Detection: fixed RMS, the safe choice for speech and tonal bleed. A peak
	// branch would need room-tone entropy > 0.7, which podcast speech never
	// reaches, so RMS is the only mode used.

	speechGateDefaultThreshold = 0.01 // -40dBFS
)

// tuneSpeechGate adapts the noise gate parameters based on Pass 1 measurements.
//
// Parameters are tuned as follows:
//   - Threshold: voiced-anchored placement a fixed margin below the voiced p10 when
//     a SpeechProfile is elected; the legacy noise-floor path is the no-profile
//     safety fallback
//   - Ratio: based on LRA (wide dynamics = gentle ratio)
//   - Release: fixed 200 ms, with the hold folded in (agate has no hold parameter)
//   - Range: fixed moderate depth, reduced to a gentler fixed depth on a narrow gap
//   - Attack: fixed 5 ms (opens before the consonant onset is shaved)
//   - Knee: fixed 3.0
//   - Detection: fixed RMS (safe for speech and tonal bleed)
//   - Makeup: 1.0 (loudness normalisation handles level compensation)
//
// Room-tone peak/crest are read from the noise profile extracted from the
// elected room-tone region and feed only the no-profile legacy threshold path.
func tuneSpeechGate(config *EffectiveFilterConfig, diagnostics *AdaptiveDiagnostics, measurements *AudioMeasurements) {
	if diagnostics != nil {
		diagnostics.SpeechGateDynamicRange = 0
		diagnostics.SpeechGateQuietSpeechEstimate = 0
		diagnostics.SpeechGateSpeechSeparation = 0
		diagnostics.SpeechGateSpeechHeadroom = 0
		diagnostics.SpeechGateThresholdUnclamped = 0
		diagnostics.SpeechGateClampReason = ""
		diagnostics.SpeechGateNarrowGap = false
	}

	// Room-tone peak/crest feed only the no-profile legacy threshold path
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

	// 1. Threshold: voiced-anchored placement when a SpeechProfile is elected,
	// otherwise the legacy noise-floor safety path. The voiced path pins the
	// threshold a fixed margin below the voiced p10, so words never clip, and reports
	// a narrow-gap signal that the depth step consumes.
	var narrowGap bool
	if measurements.Regions.SpeechProfile != nil {
		threshold, gap := calculateSpeechGateThreshold(
			measurements.Regions.VoicedLowPercentile,
			measurements.Regions.GateSeparationDB,
		)
		narrowGap = gap
		config.SpeechGate.Threshold = threshold

		if diagnostics != nil {
			actualThreshold := LinearAmplitude(config.SpeechGate.Threshold).Decibels().Float64()
			diagnostics.SpeechGateNarrowGap = narrowGap
			diagnostics.SpeechGateQuietSpeechEstimate = measurements.Regions.VoicedLowPercentile
			diagnostics.SpeechGateSpeechSeparation = measurements.Regions.GateSeparationDB
			diagnostics.SpeechGateThresholdUnclamped = measurements.Regions.VoicedLowPercentile - speechGateThresholdSpeechMarginDB
			diagnostics.SpeechGateSpeechHeadroom = measurements.Regions.VoicedLowPercentile - actualThreshold
			if narrowGap {
				diagnostics.SpeechGateClampReason = "narrow_gap"
			} else {
				diagnostics.SpeechGateClampReason = "none"
			}
		}
	} else {
		// No SpeechProfile: voiced statistics are unmeasurable, so fall back to the
		// legacy noise-floor-based threshold (the no-profile safety path).
		config.SpeechGate.Threshold = calculateSpeechGateThresholdLegacy(
			noiseContext{floor: measurements.Noise.Floor, roomTonePeak: roomTonePeak, roomToneCrest: roomToneCrest},
			config.SpeechGate.Ratio,
			lufsGap,
		)
	}

	// 3. Attack: fixed 5 ms (opens before the consonant onset is shaved)
	config.SpeechGate.Attack = speechGateAttackMS

	// 4. Release: fixed 200 ms with the hold folded in (see speechGateReleaseFixedMS).
	config.SpeechGate.Release = calculateSpeechGateRelease()

	// 5. Range: fixed moderate depth, reduced to a gentler fixed depth on a narrow
	// gap. depthDB is a positive attenuation depth, so negate it for the config's
	// linear-amplitude range.
	depthDB := calculateSpeechGateRangeDB(narrowGap)
	config.SpeechGate.Range = Decibels(-depthDB).LinearAmplitude().Float64()
	if diagnostics != nil {
		diagnostics.SpeechGateDepthDB = depthDB
	}

	// 6. Knee: fixed
	config.SpeechGate.Knee = speechGateKneeFixed

	// 7. Detection: fixed RMS (safe for speech and tonal bleed)
	config.SpeechGate.Detection = "rms"

	// Note: Makeup gain left at default (1.0 unity) - loudnorm handles all level adjustment
	//
	// Anti-hunting: there is no gentle-mode override. Hunting on uniform quiet
	// recordings is prevented by the narrow-separation depth reduction in
	// calculateSpeechGateRangeDB: a shallow gap takes a gentler fixed depth instead
	// of the full cut, so a single signal (separation) governs it.
}

// noiseContext bundles the noise-floor and room-tone references the threshold
// maths reads. floor is the full-file noise floor (dBFS); roomTonePeak and
// roomToneCrest describe the noise profile extracted from the elected room-tone
// region.
type noiseContext struct {
	floor         float64
	roomTonePeak  float64
	roomToneCrest float64
}

// calculateSpeechGateThresholdLegacy positions the threshold from the noise floor
// (or room-tone peak for high-crest bleed). It is the no-SpeechProfile safety path
// ONLY: tuneSpeechGate calls it solely in the else branch, when no profile is
// elected and the voiced statistics are therefore unmeasurable. Speech sources
// reliably elect a profile, so this path is rare.
//
// Why the safety path is safe (research W4). The danger W4 warns about is a
// threshold that lands inside speech and clips words. That cannot happen when a
// profile exists: the voiced-anchored path (calculateSpeechGateThreshold) owns
// that case and pins the threshold below voiced p10, on the speech side, with no
// route into this function. This noise-floor maths can only place a threshold from
// the noise references, and it runs only when there is no voiced population to clip
// in the first place. There is no separation-based escape hatch from the voiced
// path into the legacy maths: there is no "separation < 5 dB" guard keying off a
// fabricated proxy. Selection is structural, not numeric.
//
// noise.roomTonePeak and noise.roomToneCrest describe the noise profile extracted
// from the elected room-tone region.
func calculateSpeechGateThresholdLegacy(noise noiseContext, ratio, lufsGap float64) float64 {
	var thresholdDB float64

	usePeakReference := noise.roomToneCrest > speechGateCrestFactorThreshold &&
		noise.roomTonePeak != 0 &&
		lufsGap < lufsGapExtreme

	if usePeakReference {
		thresholdDB = noise.roomTonePeak + 3.0
	} else {
		minGapDB := speechGateTargetReductionDB / (1.0 - 1.0/ratio)
		minGapThreshold := noise.floor + minGapDB
		thresholdDB = max(minGapThreshold, speechGateTargetThresholdDB)
	}

	thresholdDB = max(speechGateThresholdMinDB, min(thresholdDB, speechGateThresholdMaxDB))

	return Decibels(thresholdDB).LinearAmplitude().Float64()
}

// calculateSpeechGateThreshold places the gate threshold a fixed margin below the
// voiced-speech low percentile (voiced p10), the soft edge of speech measured over
// the elected region in Pass 1. Anchoring below that edge means the threshold
// always sits beneath the quietest voiced content, so the gate never attenuates a
// word (research W1/W4: the threshold must not be reachable inside speech).
//
//	threshold = VoicedLowPercentile - speechGateThresholdSpeechMarginDB
//
// It also reports whether the gap is narrow. The gap is narrow when that
// speech-side placement cannot also clear the loud noise, i.e. when
//
//	(VoicedLowPercentile - speechMargin) < (NoiseHighPercentile + noiseMargin)
//
// which is exactly GateSeparationDB < speechMargin + noiseMargin (separation =
// VoicedLowPercentile - NoiseHighPercentile), so the noise percentile enters only
// through the precomputed separation. On a narrow gap the resolution favours the
// speech side: the threshold stays at the speech-side value (it is NOT raised to
// clear the noise, so residual noise is accepted) and the returned narrowGap flag
// tells the depth step to back off. The dB threshold is converted to the config's
// linear-amplitude form with the existing Decibels helper.
//
// The threshold is clamped to the global gate limits as a final safety net.
func calculateSpeechGateThreshold(voicedLowPercentile, separation float64) (threshold float64, narrowGap bool) {
	thresholdDB := voicedLowPercentile - speechGateThresholdSpeechMarginDB

	// Narrow gap: the speech-side threshold cannot also clear the loud noise.
	// Equivalent to separation < speechMargin + noiseMargin.
	narrowGap = separation < (speechGateThresholdSpeechMarginDB + speechGateThresholdNoiseMarginDB)

	// Final safety net: respect the global gate limits. The threshold stays on the
	// speech side; we never raise it toward the noise on a narrow gap.
	thresholdDB = max(speechGateThresholdMinDB, min(thresholdDB, speechGateThresholdMaxDB))

	return Decibels(thresholdDB).LinearAmplitude().Float64(), narrowGap
}

// calculateSpeechGateRatio determines ratio based on LRA (loudness range).
// The gate is a soft expander, so the ratio is capped at 2.0:1 and never tighter.
// Wide dynamics widen toward 1.5:1 to preserve expression; everything else takes
// the 2.0:1 cap.
func calculateSpeechGateRatio(lra float64) float64 {
	if lra > speechGateLRAWide {
		return speechGateRatioGentle // Wide dynamics - preserve expression
	}
	return speechGateRatioMod // Cap at 2.0:1 - soft expander, never a hard gate
}

// calculateSpeechGateRelease returns the fixed release time. agate has no hold
// parameter, so the hold is folded into the release: a longer release holds the
// gate open through the short intra-syllable dips inside speech so it does not
// pump, while staying short enough to close cleanly at word ends. A single fixed
// value (~200 ms), not a stacked flux/ZCR/LRA sum.
func calculateSpeechGateRelease() float64 {
	return speechGateReleaseFixedMS
}

// calculateSpeechGateRangeDB returns the gate attenuation depth in dB. It emits a
// fixed moderate depth on a normal (wide) gap, and a gentler fixed depth when the
// narrow-gap signal is set (from the threshold step). A narrow gap means
// little headroom between the quietest voiced speech and the loud noise, so the
// full depth would pump the floor; the gentler depth gates more softly. Two fixed
// levels only, never proportional to separation, and never a full mute. The
// returned positive dB depth is negated by the caller when converting to the
// config's linear-amplitude range.
func calculateSpeechGateRangeDB(narrowGap bool) float64 {
	if narrowGap {
		return speechGateDepthNarrowDB
	}
	return speechGateDepthFixedDB
}
