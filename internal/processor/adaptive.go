// Package processor handles audio analysis and processing
package processor

import (
	"math"
)

// Adaptive tuning constants for audio processing.
// These thresholds and limits control how filters adapt to input measurements.
const (
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
	spectralDecreaseVeryWarm = -0.08 // Below: very warm voice, needs maximum LF protection
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
	silenceEntropyTonal = 0.5 // Below: tonal noise (hum), bandreject better than highpass

	// Spectral centroid thresholds (Hz) for voice brightness classification
	centroidBright     = 6000.0 // Above: bright voice
	centroidNormal     = 4000.0 // Above: normal voice, below: dark voice
	centroidVeryBright = 7000.0 // Threshold for de-esser intensity

	// Spectral rolloff thresholds (Hz) for HF content classification
	rolloffNoSibilance = 6000.0  // Below: no sibilance expected
	rolloffLimited     = 8000.0  // Below: limited HF extension
	rolloffExtensive   = 12000.0 // Above: extensive HF content

	// LUFS gap threshold for adaptive processing intensity
	lufsGapExtreme = 25.0 // dB - extreme gap, gate needs special handling

	// Gentle gate mode: for extreme LUFS gap + low LRA
	// Very quiet recordings with uniform levels cause the gate's soft expansion
	// to apply varying gain reduction across similar speech levels, creating
	// volume modulation ("hunting"). Use gentler parameters to prevent this.
	ds201GateGentleLRAThreshold = 10.0 // LU - below this with extreme LUFS gap triggers gentle mode
	ds201GateGentleRatio        = 1.2  // Minimal gain variation in expansion zone
	ds201GateGentleKnee         = 2.0  // Sharper transition reduces hunting

	// De-esser intensity levels
	deessIntensityBright = 0.6 // Bright voice base intensity
	deessIntensityNormal = 0.5 // Normal voice base intensity
	deessIntensityDark   = 0.4 // Dark voice base intensity
	deessIntensityMax    = 0.8 // Maximum intensity limit
	deessIntensityMin    = 0.3 // Minimum before disabling

	// DS201 Gate tuning constants
	// Threshold calculation: ensures sufficient gap above noise for effective soft expansion
	ds201GateThresholdMinDB       = -80.0 // dB - minimum threshold (allows speech guard to protect quiet content)
	ds201GateThresholdMaxDB       = -25.0 // dB - never gate above this (would cut speech)
	ds201GateCrestFactorThreshold = 20.0  // dB - above this, use peak reference instead of RMS
	ds201GateTargetReductionDB    = 12.0  // dB - target noise reduction from soft expander
	ds201GateTargetThresholdDB    = -40.0 // dB - target threshold for clean recordings (quiet speech/breath level)

	// Aggression-based threshold positioning
	// Aggression: 0.0 = at quietSpeech, 1.0 = at speechRMS

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
	ds201GateAttackUltraFast  = 0.5  // ms - 500µs for extreme transients
	ds201GateAttackFast       = 7.0  // ms - for sharp transients
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

	// DS201 Low-Pass filter tuning
	ds201LPDefaultFreq = 16000.0 // Hz - default cutoff (preserves all audible content)

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

	// Default fallback values for sanitization
	ds201DefaultHPFreq        = 80.0
	defaultDeessIntensity     = 0.0
	defaultLA2ARatio          = 3.0   // LA-2A baseline ratio
	defaultLA2AThreshold      = -18.0 // Moderate threshold
	ds201DefaultGateThreshold = 0.01  // -40dBFS

	// ==========================================================================
	// DS201 Low-Pass Content Detection Thresholds
	// ==========================================================================
	// These thresholds classify content as speech, music, or mixed based on
	// spectral characteristics. The lowpass filter is only enabled for speech
	// content when HF noise indicators are present.

	// Speech characteristics: peaked, tonal, stable
	lpContentKurtosisSpeech = 6.0   // Above: energy peaked at voice harmonics
	lpContentFlatnessSpeech = 0.45  // Below: tonal, not noise-like
	lpContentFluxSpeech     = 0.003 // Below: stable sustained phonation
	lpContentCrestSpeech    = 30.0  // Above: dominant voice peaks

	// Music characteristics: spread, uniform, varied
	lpContentKurtosisMusic = 5.0   // Below: energy spread across instruments
	lpContentFlatnessMusic = 0.55  // Above: more uniform spectral energy
	lpContentFluxMusic     = 0.005 // Above: rhythmic variation
	lpContentCrestMusic    = 25.0  // Below: multiple sources averaging out

	// Content type decision threshold
	lpContentScoreThreshold = 3 // Score needed to classify as speech or music

	// Speech HF noise detection thresholds
	lpRolloffEnableThreshold = 14000 // Hz - enable lowpass when rolloff > this
	lpRolloffHeadroom        = 2000  // Hz - cutoff = rolloff + this value (per spec)
	lpRolloffDarkVoice       = 8000  // Hz - disable if rolloff < this (voice already dark)
	lpZCRHigh                = 0.10  // Above: high zero crossings (HF noise indicator)
	lpZCRCentroidThreshold   = 4000  // Hz - ZCR trigger only valid if centroid below this
	lpZCRCutoff              = 12000 // Hz - cutoff when ZCR trigger fires (per spec)
)

// ContentType classifies audio content for adaptive filter tuning.
type ContentType int

const (
	// ContentSpeech indicates speech-dominant content (podcast, voice recording).
	// Lowpass may enable if HF noise indicators are present.
	ContentSpeech ContentType = iota

	// ContentMusic indicates music-dominant content (bumpers, stings, jingles).
	// Lowpass is always disabled to preserve full spectrum.
	ContentMusic

	// ContentMixed indicates unclear or mixed content (speech over music bed).
	// Conservative approach: lowpass disabled to avoid audible HF loss.
	ContentMixed
)

// String returns a human-readable name for the content type.
func (c ContentType) String() string {
	switch c {
	case ContentSpeech:
		return "speech"
	case ContentMusic:
		return "music"
	case ContentMixed:
		return "mixed"
	default:
		return "unknown"
	}
}

// detectContentType classifies audio content based on spectral measurements.
// Returns ContentSpeech, ContentMusic, or ContentMixed.
//
// Speech characteristics:
//   - High kurtosis (>6): energy peaked at voice harmonics
//   - Lower flatness (<0.45): tonal, not noise-like
//   - Low flux (<0.003): stable sustained phonation
//   - High crest (>30): dominant voice peaks
//
// Music characteristics:
//   - Low kurtosis (<5): energy spread across instruments
//   - Higher flatness (>0.55): more uniform spectral energy
//   - Higher flux (>0.005): rhythmic variation
//   - Lower crest (<25): multiple sources averaging out
func detectContentType(m *AudioMeasurements) ContentType {
	speechScore := 0
	musicScore := 0

	// Kurtosis: speech is peaked, music is spread
	if m.SpectralKurtosis > lpContentKurtosisSpeech {
		speechScore++
	} else if m.SpectralKurtosis < lpContentKurtosisMusic {
		musicScore++
	}

	// Flatness: speech is tonal, music is flatter
	if m.SpectralFlatness < lpContentFlatnessSpeech {
		speechScore++
	} else if m.SpectralFlatness > lpContentFlatnessMusic {
		musicScore++
	}

	// Flux: speech is stable, music varies
	if m.SpectralFlux < lpContentFluxSpeech {
		speechScore++
	} else if m.SpectralFlux > lpContentFluxMusic {
		musicScore++
	}

	// Crest: speech has dominant peaks
	if m.SpectralCrest > lpContentCrestSpeech {
		speechScore++
	} else if m.SpectralCrest < lpContentCrestMusic {
		musicScore++
	}

	// Decision: require threshold score to classify definitively
	if speechScore >= lpContentScoreThreshold {
		return ContentSpeech
	}
	if musicScore >= lpContentScoreThreshold {
		return ContentMusic
	}
	return ContentMixed
}

// AdaptConfig tunes all filter parameters based on Pass 1 measurements.
// This is the main entry point for adaptive configuration.
// It updates config in-place based on the audio characteristics measured in analysis.
func AdaptConfig(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Store measurements reference
	config.Measurements = measurements

	// Tune each filter adaptively based on measurements
	// Order matters: gate threshold calculated BEFORE denoise filters
	tuneDS201HighPass(config, measurements) // Composite: highpass + hum notch
	tuneDS201LowPass(config, measurements)  // Ultrasonic rejection (adaptive)

	// NoiseRemove: anlmdn + compand (primary noise reduction)
	tuneNoiseRemove(config, measurements)

	tuneDS201Gate(config, measurements) // DS201-style soft expander gate
	tuneDeesser(config, measurements)
	tuneLA2ACompressor(config, measurements)
	// tuneUREI1176Limiter removed - 1176 moved to Pass 3, tuned from OutputMeasurements

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
// - Low entropy (< 0.7) indicates periodic/tonal noise → enable hum removal
// - High entropy indicates broadband noise → skip notch filter
// - Voice-aware: reduces harmonics for warm voices to protect vocal fundamentals
func tuneDS201HighPass(config *FilterChainConfig, measurements *AudioMeasurements) {
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
		baseFreq = ds201HPBrightFreq
	case measurements.SpectralCentroid > centroidNormal:
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
	config.DS201HPTransform = "tdii"

	// Protect warm voices with significant LF body
	// Instead of disabling highpass, we use gentler settings:
	// - Lower frequency (subsonic only)
	// - Lower Q (gentler rolloff)
	// - Reduced mix (blend filtered with dry signal)
	//
	// This removes subsonic rumble while preserving bass character.
	if measurements.SpectralDecrease < spectralDecreaseVeryWarm {
		// Very warm voice
		// Use minimal settings: 30Hz cutoff, gentle Q, 50% mix
		config.DS201HPFreq = ds201HPVeryWarmFreq
		config.DS201HPWidth = ds201HPVeryWarmWidth
		config.DS201HPMix = ds201HPVeryWarmMix
		config.DS201HPPoles = 1 // Gentle 6dB/oct slope
	} else if measurements.SpectralSkewness > spectralSkewnessLFEmphasis {
		// Significant LF emphasis
		// Use warm settings: 40Hz cutoff, gentle Q, 70% mix
		config.DS201HPFreq = ds201HPWarmFreq
		config.DS201HPWidth = ds201HPWarmWidth
		config.DS201HPMix = ds201HPWarmMix
		config.DS201HPPoles = 1 // Gentle 6dB/oct slope
	} else if measurements.SpectralDecrease < spectralDecreaseWarm {
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

// tuneDS201LowPass adapts the DS201-inspired low-pass filter based on content type
// and spectral measurements.
//
// Content-aware approach:
//   - Music: Always disabled - preserve full spectrum including cymbals, air, synth harmonics
//   - Mixed: Disabled - conservative approach to avoid audible HF loss
//   - Speech: Enabled only when HF noise indicators are present
//
// The DS201's LP filter prevents false gate triggers from ultrasonic noise.
// Since we filter the audio path (not true sidechain), we must be conservative
// to avoid audible HF loss.
func tuneDS201LowPass(config *FilterChainConfig, m *AudioMeasurements) {
	// Start disabled - only enable when we detect clear benefit
	config.DS201LPEnabled = false
	config.DS201LPFreq = ds201LPDefaultFreq

	// Detect content type
	contentType := detectContentType(m)
	config.DS201LPContentType = contentType

	// Calculate rolloff/centroid ratio for logging
	if m.SpectralCentroid > 0 {
		config.DS201LPRolloffRatio = m.SpectralRolloff / m.SpectralCentroid
	}

	switch contentType {
	case ContentMusic:
		// Music: preserve full spectrum
		config.DS201LPReason = "music content detected"
		return

	case ContentMixed:
		// Mixed content: disable to be safe
		config.DS201LPReason = "mixed content, conservative"
		return

	case ContentSpeech:
		// Speech: check for HF noise indicators
		tuneDS201LowPassForSpeech(config, m)
	}
}

// tuneDS201LowPassForSpeech checks HF noise indicators and enables lowpass if warranted.
// Only called when content type is speech.
//
// Default behaviour: DISABLED — only activates when measurements indicate benefit.
// Per DS201-INSPIRED-GATE.md spec:
//
// Trigger conditions (in priority order):
//  1. Rolloff < 8kHz → disabled (voice already dark)
//  2. Rolloff > 14kHz → enabled at rolloff + 2kHz (ultrasonic cleanup)
//  3. High ZCR (>0.10) with low centroid (<4000) → possible HF noise, enable at 12kHz
//
// Constraints:
//   - Never cut below 8kHz (sibilance lives at 4-8kHz, air/presence at 8-12kHz)
//   - Conservative approach — preserves natural voice character
func tuneDS201LowPassForSpeech(config *FilterChainConfig, m *AudioMeasurements) {
	// Default: DISABLED per spec — only activate when measurements indicate benefit
	config.DS201LPEnabled = false
	config.DS201LPFreq = ds201LPDefaultFreq
	config.DS201LPPoles = 1 // 6dB/oct - gentle
	config.DS201LPMix = 1.0
	config.DS201LPReason = "no HF issues detected"

	// Condition 1: Voice already dark (rolloff < 8kHz)
	// No benefit from lowpass — would only remove wanted content
	if m.SpectralRolloff < lpRolloffDarkVoice {
		config.DS201LPReason = "voice already dark (rolloff < 8kHz)"
		return
	}

	// Condition 2: High rolloff (> 14kHz) — ultrasonic content present
	// Enable at rolloff + 2kHz to clean up ultrasonics while preserving audible content
	if m.SpectralRolloff > lpRolloffEnableThreshold {
		cutoff := m.SpectralRolloff + lpRolloffHeadroom
		// Clamp to reasonable maximum
		if cutoff > 20000 {
			cutoff = 20000
		}
		config.DS201LPEnabled = true
		config.DS201LPFreq = cutoff
		config.DS201LPPoles = 1 // 6dB/oct - very gentle for ultrasonic cleanup
		config.DS201LPMix = 1.0
		config.DS201LPReason = "ultrasonic cleanup (rolloff > 14kHz)"
		return
	}

	// Condition 3: High ZCR with low centroid (HF noise, not sibilance)
	// Sibilance has high ZCR AND high centroid; noise has high ZCR with low centroid
	if m.ZeroCrossingsRate > lpZCRHigh && m.SpectralCentroid < lpZCRCentroidThreshold {
		config.DS201LPEnabled = true
		config.DS201LPFreq = lpZCRCutoff
		config.DS201LPPoles = 1 // 6dB/oct - gentle
		config.DS201LPMix = 0.8 // Blend with dry for transparency
		config.DS201LPReason = "high ZCR with low centroid (HF noise)"
		return
	}

	// No triggers fired — keep disabled
}

// tuneNoiseRemove adjusts compand parameters based on measured noise floor.
// Uses silence region measurements for accurate noise characterisation.
//
// The anlmdn parameters (strength, patch, research, smooth) are kept constant from spike validation.
// Compand parameters adapt to the measured noise floor:
// - Threshold: 5dB above noise floor (catches breaths but not speech)
// - Expansion: scales with noise severity (gentle for clean, aggressive for noisy)
//
// anlmdn remains constant because spike testing validated these parameters:
// - strength: 0.00001 (minimum)
// - patch: 6ms (context window)
// - research: 5.8ms (search window)
// - smooth: 11 (weight smoothing)
func tuneNoiseRemove(config *FilterChainConfig, m *AudioMeasurements) {
	if !config.NoiseRemoveEnabled {
		return
	}

	// Default values (fallback if no noise profile)
	threshold := -55.0
	expansion := 6.0

	if m.NoiseProfile != nil && m.NoiseProfile.MeasuredNoiseFloor < 0 {
		noiseFloor := m.NoiseProfile.MeasuredNoiseFloor

		// Threshold: 5dB above noise floor (catches breaths but not speech)
		threshold = noiseFloor + 5.0
		// Clamp to reasonable range
		threshold = clamp(threshold, -70.0, -40.0)

		// Expansion: scale with noise severity
		expansion = scaleExpansion(noiseFloor)
	}

	config.NoiseRemoveCompandThreshold = threshold
	config.NoiseRemoveCompandExpansion = expansion

	// attack, decay, knee stay constant (validated in spike testing)
}

// preferSpeechMetric returns speech-specific measurement if available,
// otherwise falls back to full-file measurement.
func preferSpeechMetric(fullFile, speechProfile float64) float64 {
	if speechProfile > 0 {
		return speechProfile
	}
	return fullFile
}

// scaleExpansion returns expansion depth based on noise severity.
// Noisier recordings need more aggressive expansion to suppress residuals.
func scaleExpansion(noiseFloor float64) float64 {
	switch {
	case noiseFloor > -45.0:
		return 12.0 // Very noisy - aggressive
	case noiseFloor > -55.0:
		return 8.0 // Moderate noise
	case noiseFloor > -65.0:
		return 6.0 // Typical
	default:
		return 4.0 // Very clean - gentle
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
	// Prefer speech-specific measurements for sibilance detection
	centroid := measurements.SpectralCentroid
	rolloff := measurements.SpectralRolloff
	if measurements.SpeechProfile != nil {
		centroid = preferSpeechMetric(centroid, measurements.SpeechProfile.SpectralCentroid)
		rolloff = preferSpeechMetric(rolloff, measurements.SpeechProfile.SpectralRolloff)
	}

	// Determine baseline intensity from centroid
	var baseIntensity float64
	switch {
	case centroid > centroidVeryBright:
		baseIntensity = deessIntensityBright // Bright voice
	case centroid > centroidBright:
		baseIntensity = deessIntensityNormal // Normal voice
	default:
		baseIntensity = deessIntensityDark // Dark voice
	}

	// Refine based on spectral rolloff (HF extension)
	switch {
	case rolloff < rolloffNoSibilance:
		// Very limited HF content - no sibilance expected
		config.DeessIntensity = 0.0

	case rolloff < rolloffLimited:
		// Limited HF extension - reduce intensity
		config.DeessIntensity = baseIntensity * 0.7
		if config.DeessIntensity < deessIntensityMin {
			config.DeessIntensity = 0.0 // Skip if too low
		}

	case rolloff > rolloffExtensive:
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
func tuneDS201Gate(config *FilterChainConfig, measurements *AudioMeasurements) {
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
	if measurements.SpeechProfile != nil {
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
		if thresholdUnclamped < noiseFloorLimit && actualThreshold >= noiseFloorLimit {
			clampReason = "noise_floor"
		} else if thresholdUnclamped > speechRMSLimit && actualThreshold <= speechRMSLimit {
			clampReason = "speech_rms"
		} else {
			clampReason = "none"
		}

		config.DS201GateAggression = aggression
		config.DS201GateDynamicRange = dynamicRange
		config.DS201GateQuietSpeechEstimate = quietSpeech
		config.DS201GateSpeechSeparation = separation
		config.DS201GateThresholdUnclamped = thresholdUnclamped
		config.DS201GateClampReason = clampReason
		config.DS201GateSpeechHeadroom = quietSpeech - actualThreshold
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
		config.DS201GateGentleMode = true
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
// Fast transients need fast attack to avoid clipping word onsets.
// DS201-inspired: supports sub-millisecond attack (0.5ms+) for transient preservation.
// MaxDifference is expressed as a fraction (0.0-1.0), convert to percentage.
func calculateDS201GateAttack(maxDiff, spectralFlux, spectralCrest float64) float64 {
	// MaxDifference is 0.0-1.0 fraction, convert to percentage for comparison
	maxDiffPercent := maxDiff * 100.0

	// DS201-inspired attack tiers with ultra-fast capability
	// Sub-millisecond attack preserves hard transients without click artifacts
	var baseAttack float64
	switch {
	case maxDiffPercent > ds201GateMaxDiffExtreme || spectralCrest > ds201GateCrestExtreme:
		// Extreme transients - 500µs for pristine attack preservation
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
//   - Tonal noise (entropy < 0.15): slow release - some pumping hiding needed
//   - Mixed noise (entropy < 0.2): moderate release
//   - Broadband-ish (entropy >= 0.2): faster release - cut noise quickly without pumping risk
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
	// Note: Makeup gain left at default (0 dB unity) - loudnorm handles all level adjustment
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
	// Prefer speech-specific flux for timing decisions
	flux := measurements.SpectralFlux
	if measurements.SpeechProfile != nil {
		flux = preferSpeechMetric(flux, measurements.SpeechProfile.SpectralFlux)
	}

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
	if measurements.SpectralSkewness > la2aSkewnessWarm {
		release += la2aReleaseWarmBoost
	}

	// Heavy compression (large LUFS gap) triggers slower release
	// LA-2A's T4 cell releases slower after sustained heavy compression
	if measurements.InputI < 0 {
		lufsGap := -18.0 - measurements.InputI // Distance to -18 LUFS target
		if lufsGap > 16.0 {
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
	// Prefer speech-specific kurtosis for harmonic structure
	kurtosis := measurements.SpectralKurtosis
	if measurements.SpeechProfile != nil {
		kurtosis = preferSpeechMetric(kurtosis, measurements.SpeechProfile.SpectralKurtosis)
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

// sanitizeConfig ensures no NaN or Inf values remain after adaptive tuning
func sanitizeConfig(config *FilterChainConfig) {
	// DS201-inspired highpass filter
	config.DS201HPFreq = sanitizeFloat(config.DS201HPFreq, ds201DefaultHPFreq)
	config.DS201HPWidth = sanitizeFloat(config.DS201HPWidth, 0.707) // Butterworth default
	config.DS201HPMix = sanitizeFloat(config.DS201HPMix, 1.0)       // Full wet default

	// DS201-inspired lowpass filter
	config.DS201LPFreq = sanitizeFloat(config.DS201LPFreq, ds201LPDefaultFreq)
	config.DS201LPWidth = sanitizeFloat(config.DS201LPWidth, 0.707) // Butterworth default
	config.DS201LPMix = sanitizeFloat(config.DS201LPMix, 1.0)       // Full wet default

	// De-esser intensity
	config.DeessIntensity = sanitizeFloat(config.DeessIntensity, defaultDeessIntensity)

	// LA-2A compressor
	config.LA2ARatio = sanitizeFloat(config.LA2ARatio, defaultLA2ARatio)
	config.LA2AThreshold = sanitizeFloat(config.LA2AThreshold, defaultLA2AThreshold)
	// Note: LA2AMakeup not sanitised - always 0 (set in DefaultFilterConfig)

	// DS201-inspired gate threshold needs additional check for zero/negative
	if math.IsNaN(config.DS201GateThreshold) || math.IsInf(config.DS201GateThreshold, 0) || config.DS201GateThreshold <= 0 {
		config.DS201GateThreshold = ds201DefaultGateThreshold
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

// lerp performs linear interpolation between a and b based on t (0-1).
// When t=0, returns a. When t=1, returns b.
func lerp(a, b, t float64) float64 {
	return a + (b-a)*t
}
