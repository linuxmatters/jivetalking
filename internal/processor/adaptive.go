// Package processor handles audio analysis and processing
package processor

import "math"

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

	// De-esser intensity levels
	deessIntensityBright = 0.6 // Bright voice base intensity
	deessIntensityNormal = 0.5 // Normal voice base intensity
	deessIntensityDark   = 0.4 // Dark voice base intensity
	deessIntensityMax    = 0.8 // Maximum intensity limit
	deessIntensityMin    = 0.3 // Minimum before disabling

	// DS201 Gate tuning constants
	// Threshold calculation: ensures sufficient gap above noise for effective soft expansion
	ds201GateThresholdMinDB       = -50.0 // dB - minimum threshold (quiet speech floor)
	ds201GateThresholdMaxDB       = -25.0 // dB - never gate above this (would cut speech)
	ds201GateCrestFactorThreshold = 20.0  // dB - above this, use peak reference instead of RMS
	ds201GateTargetReductionDB    = 12.0  // dB - target noise reduction from soft expander
	ds201GateTargetThresholdDB    = -40.0 // dB - target threshold for clean recordings (quiet speech/breath level)

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
	ds201GateReleaseMax       = 500  // ms - maximum release

	// Adaptive release based on noise entropy (higher entropy = more broadband = faster release)
	// Tonal noise (low entropy) needs slow release to hide pumping artifacts
	// Broadband/mixed noise (higher entropy) benefits from faster release to cut noise quickly
	// Reference: Mark's entropy ~0.09 (very tonal), Martin's ~0.14 (mixed)
	ds201GateReleaseEntropyVeryTonal = 0.10 // Below: very tonal (pure hum/bleed) - slowest
	ds201GateReleaseEntropyTonal     = 0.12 // Below: tonal noise - slow release
	ds201GateReleaseEntropyMixed     = 0.16 // Below: mixed character - moderate release
	// Above 0.16: broadband-ish noise - faster release OK
	ds201GateReleaseEntropyReduce = 100 // ms - reduction for broadband-ish noise

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
	ds201LPMinFreq     = 8000.0  // Hz - minimum cutoff (never filter below this)
	ds201LPHeadroom    = 2000.0  // Hz - headroom above rolloff when setting cutoff

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

	// Mix limits
	arnnDnMixMin = 0.1 // Minimum mix (below this, filter has negligible effect)
	arnnDnMixMax = 0.8 // Maximum mix (above risks artifacts)

	// LUFS to RMS conversion constant
	// Rough conversion: LUFS ≈ -23 + 20*log10(RMS)
	lufsRmsOffset = 23.0

	// Default fallback values for sanitization
	ds201DefaultHPFreq        = 80.0
	defaultDeessIntensity     = 0.0
	defaultLA2ARatio          = 3.0   // LA-2A baseline ratio
	defaultLA2AThreshold      = -18.0 // Moderate threshold
	defaultLA2AMakeup         = 2.0   // Conservative makeup
	defaultLA2AAttack         = 10.0  // LA-2A fixed attack
	defaultLA2ARelease        = 200.0 // LA-2A two-stage release approximation
	defaultLA2AKnee           = 4.0   // LA-2A T4 optical cell soft knee
	ds201DefaultGateThreshold = 0.01  // -40dBFS
	defaultHumFrequency       = 50.0  // UK mains
	defaultHumHarmonics       = 4
	defaultHumWidth           = 1.0 // Hz

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

	// ==========================================================================
	// Dolby SR-Inspired Single-Stage Denoise (afftdn)
	// ==========================================================================
	// Key SR principles honoured:
	// - Least Treatment: never remove 100% of noise
	// - Transparency over depth: conservative parameters on each stage
	// ==========================================================================

	// Noise floor severity thresholds for processing intensity
	// Expanded range for better differentiation between clean/noisy sources
	// Mark (-80 dBFS) should get minimal, Martin (-72 dBFS) should get moderate
	dolbySRFloorClean    = -80.0 // dBFS - below: minimal processing (studio quality)
	dolbySRFloorModerate = -65.0 // dBFS - standard processing (home office)
	dolbySRFloorNoisy    = -55.0 // dBFS - above: aggressive processing (noisy environment)

	// afftdn limits (subtle but effective: DS201 gate handles silence, this polishes under speech)
	// Slightly higher ceiling for noisy sources, but still conservative
	dolbySRNRMin = 2.0 // dB - barely perceptible (for clean sources)
	dolbySRNRMax = 6.0 // dB - moderate ceiling for noisy sources
	dolbySRGSMin = 10  // Higher minimum smoothing (hide gain changes)
	dolbySRGSMax = 20  // Much higher smoothing (very slow, transparent)

	// Warm voice detection for NR boost (noise hides more in dark/warm voices)
	// Low centroid + high skewness + negative decrease = warm voice needing extra NR
	dolbySRWarmCentroid  = 4000.0 // Hz - below: warm/dark voice
	dolbySRWarmSkewness  = 1.5    // Above: bass-concentrated energy
	dolbySRWarmDecrease  = -0.1   // Below: strong LF emphasis
	dolbySRWarmNRBoost   = 1.0    // dB - extra NR for warm voices (subtle)
	dolbySRVeryWarmBoost = 0.5    // dB - additional boost for very warm (skewness > 1.8)
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

	// Calculate LUFS gap once - used by multiple tuning functions
	lufsGap := calculateLUFSGap(config.TargetI, measurements.InputI)

	// Tune each filter adaptively based on measurements
	// Order matters: gate threshold calculated BEFORE denoise filters
	tuneDS201HighPass(config, measurements, lufsGap) // Composite: highpass + hum notch
	tuneDS201LowPass(config, measurements)           // Ultrasonic rejection (adaptive)
	tuneDolbySR(config, measurements, lufsGap)       // Dolby SR-inspired denoise (uses afftdn internally)
	tuneArnndn(config, measurements, lufsGap)        // RNN denoise (LUFS gap + noise floor based)
	tuneDS201Gate(config, measurements)              // DS201-style soft expander gate
	tuneDeesser(config, measurements)
	tuneLA2ACompressor(config, measurements)
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
func tuneDS201HighPass(config *FilterChainConfig, measurements *AudioMeasurements, lufsGap float64) {
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
		// Very warm voice (e.g. Popey -0.095, Martin -0.238)
		// Use minimal settings: 30Hz cutoff, gentle Q, 50% mix
		config.DS201HPFreq = ds201HPVeryWarmFreq
		config.DS201HPWidth = ds201HPVeryWarmWidth
		config.DS201HPMix = ds201HPVeryWarmMix
		config.DS201HPPoles = 1 // Gentle 6dB/oct slope
	} else if measurements.SpectralSkewness > spectralSkewnessLFEmphasis {
		// Significant LF emphasis (e.g. Mark: skewness 1.132)
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

	// --- Hum notch tuning (part of DS201HighPass composite) ---
	// Uses entropy to detect tonal noise (hum) vs broadband noise
	tuneDS201HumNotch(config, measurements)
}

// tuneDS201HumNotch configures the hum notch filter portion of DS201HighPass.
// Called internally by tuneDS201HighPass - not intended for direct use.
//
// The entropy is calculated from the extracted silence sample during analysis.
// Pure tones have low entropy; random noise has high entropy.
func tuneDS201HumNotch(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Check if we have noise profile with entropy measurement
	if measurements.NoiseProfile == nil {
		config.DS201HumHarmonics = 0 // Disable hum notches
		return
	}

	// Low entropy indicates tonal/periodic noise (likely mains hum)
	if measurements.NoiseProfile.Entropy >= humEntropyThreshold {
		// High entropy = broadband noise, notch filter won't help
		config.DS201HumHarmonics = 0 // Disable hum notches
		return
	}

	// Filter enabled - tune parameters based on voice characteristics
	config.DS201HumFrequency = humFreq50Hz // Default to 50Hz (UK/EU mains)

	// Determine harmonic count based on voice characteristics
	// Warm/bassy voices need fewer harmonics to avoid cutting into vocal fundamentals
	isWarmVoice := measurements.SpectralSkewness > humSkewnessWarm ||
		measurements.SpectralDecrease < humDecreaseWarm

	if isWarmVoice {
		// Warm voice: only filter fundamental + 1 harmonic (50Hz, 100Hz)
		// Avoids 150Hz and 200Hz which overlap male vocal fundamentals
		config.DS201HumHarmonics = humWarmVoiceHarmonics

		// Adjust width and mix based on how warm the voice is
		// Very warm voices (decrease < -0.1) get narrower notch and more dry signal
		if measurements.SpectralDecrease < humDecreaseVeryWarm {
			config.DS201HumWidth = humVeryWarmVoiceWidth // 0.3Hz - very surgical
			config.DS201HumMix = humMixVeryWarmVoice     // 70% wet
		} else {
			config.DS201HumWidth = humWarmVoiceWidth // 0.5Hz
			config.DS201HumMix = humMixWarmVoice     // 80% wet
		}
	} else {
		// Brighter voice: safe to filter more harmonics, full wet
		config.DS201HumHarmonics = humDefaultHarmonics
		config.DS201HumMix = humMixDefault

		// Adaptive width based on noise severity (only for non-warm voices)
		// Lower entropy = more tonal/pure hum = can use narrower notch
		// Higher entropy (but still below threshold) = mixed noise = use wider notch
		if measurements.NoiseProfile.Entropy < 0.3 {
			// Very tonal hum - use narrow surgical notch
			config.DS201HumWidth = humNarrowWidth
		} else if measurements.NoiseProfile.Entropy > 0.5 {
			// Borderline tonal - use wider notch to catch it
			config.DS201HumWidth = humWideWidth
		} else {
			// Standard case
			config.DS201HumWidth = humDefaultWidth
		}
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

// tuneDolbySR adapts the Dolby SR-inspired denoise filter based on measurements.
//
// Strategy:
//   - Conservative parameters (transparency over depth)
//   - Adapts to noise floor severity, spectral character, and LUFS gap
//
// afftdn tuning:
//   - Noise floor severity → NR amount (conservative: 4-12dB)
//   - LUFS gap → NR boost (upcoming gain amplifies noise)
//   - Spectral flatness/entropy → noise type selection (w/v/s)
//   - Noise character → gain smoothing (tonal needs more smoothing)
//   - Tonal vs broadband → adaptivity speed

func tuneDolbySR(config *FilterChainConfig, measurements *AudioMeasurements, lufsGap float64) {
	if !config.DolbySREnabled {
		return
	}

	// Set noise floor from measurements
	config.DolbySRNoiseFloor = measurements.NoiseFloor

	// Always use custom noise type to enable 15-band Bark-scale profile
	// The bn parameter provides voice-protective multi-band processing
	config.DolbySRNoiseType = "c"

	// Tune the 15-band profile based on spectral characteristics
	tuneDolbySRBandProfile(config, measurements)

	// Determine noise floor severity for scaling parameters
	noiseFloorSeverity := calculateNoiseFloorSeverity(measurements.NoiseFloor)

	// Get noise character indicators
	silenceEntropy := 0.5 // Default: mixed noise
	if measurements.NoiseProfile != nil {
		silenceEntropy = measurements.NoiseProfile.Entropy
	}
	isTonalNoise := silenceEntropy < 0.4 // Low entropy = tonal (hum, bleed)

	// ==========================================================================
	// afftdn tuning (spectral processing)
	// ==========================================================================

	// Base NR from noise floor severity (2-6dB range)
	// Clean sources get minimal NR, noisy sources get more
	baseNR := lerp(dolbySRNRMin, dolbySRNRMax, noiseFloorSeverity)

	// Add small NR boost for high LUFS gap (noise gets amplified by normalisation)
	// Scale very conservatively (0.1x) to avoid over-processing
	if lufsGap > 0 {
		baseNR += lufsGap * 0.1
	}

	// Warm voice boost: dark/warm voices mask noise less in lower frequencies
	// This allows slightly more aggressive NR without audible artifacts
	// Criteria: low centroid + high skewness + strong bass (negative decrease)
	isWarmVoice := measurements.SpectralCentroid < dolbySRWarmCentroid &&
		measurements.SpectralSkewness > dolbySRWarmSkewness &&
		measurements.SpectralDecrease < dolbySRWarmDecrease
	if isWarmVoice {
		baseNR += dolbySRWarmNRBoost
		// Extra boost for very warm voices (e.g., deep male voice)
		if measurements.SpectralSkewness > 1.8 {
			baseNR += dolbySRVeryWarmBoost
		}
	}

	// Clamp to hybrid-appropriate limits (allow slightly higher max for warm voices)
	maxNR := dolbySRNRMax
	if isWarmVoice {
		maxNR = dolbySRNRMax + dolbySRWarmNRBoost // Allow warm voice boost to exceed normal max
	}
	config.DolbySRNoiseReduction = clamp(baseNR, dolbySRNRMin, maxNR)

	// Gain smoothing: always use high values to hide any gain changes completely
	// Since we're only doing subtle NR, we can afford very slow response
	if isTonalNoise {
		config.DolbySRGainSmooth = dolbySRGSMax // 20: very smooth for tonal
	} else if measurements.SpectralFlatness > 0.6 {
		config.DolbySRGainSmooth = dolbySRGSMin + 4 // 14: still smooth for broadband
	} else {
		config.DolbySRGainSmooth = (dolbySRGSMin + dolbySRGSMax) / 2 // 15: balanced
	}

	// Adaptivity: always slow to prevent any audible gain changes
	// Transparency is more important than tracking dynamics
	if isTonalNoise {
		config.DolbySRAdaptivity = 0.3 // Very slow for tonal
	} else if measurements.SpectralFlatness > 0.6 {
		config.DolbySRAdaptivity = 0.5 // Still slow for broadband
	} else {
		config.DolbySRAdaptivity = 0.4 // Default: slow
	}

	// Residual floor: Least Treatment — always leave plenty of room noise
	// Higher floor = more residual noise = no audible artefacts
	// DS201 gate handles silence; this just needs to not make speech sound processed
	config.DolbySRResidualFloor = lerp(-32.0, -26.0, noiseFloorSeverity)
}

// calculateNoiseFloorSeverity returns a 0-1 value indicating noise severity.
// 0 = very clean (below dolbySRFloorClean), 1 = very noisy (above dolbySRFloorNoisy)
func calculateNoiseFloorSeverity(noiseFloor float64) float64 {
	if noiseFloor <= dolbySRFloorClean {
		return 0.0
	}
	if noiseFloor >= dolbySRFloorNoisy {
		return 1.0
	}
	// Linear interpolation between clean and noisy thresholds
	return (noiseFloor - dolbySRFloorClean) / (dolbySRFloorNoisy - dolbySRFloorClean)
}

// tuneDolbySRBandProfile adapts the 15-band Bark-scale NR profile based on spectral measurements.
// This implements voice-protective multi-band processing inspired by Dolby SR's multi-compander design.
//
// Band mapping (approximate at 48kHz, bm=1.25):
//
//	0: ~20-50Hz    sub-bass        | 8:  ~1350-2000Hz  upper F2
//	1: ~50-100Hz   bass            | 9:  ~2000-2756Hz  consonant transitions
//	2: ~100-172Hz  chest resonance | 10: ~2756-4000Hz  sibilance begins
//	3: ~172-270Hz  low formants    | 11: ~4000-5500Hz  primary sibilance
//	4: ~270-400Hz  male fundamentals | 12: ~5500-7500Hz  consonant detail
//	5: ~400-600Hz  female fundamentals | 13: ~7500-10000Hz air/breath
//	6: ~600-900Hz  F1-F2 transition  | 14: ~10000-16000Hz+ ultra-HF
//	7: ~900-1350Hz core intelligibility
//
// Strategy:
//   - Base profile protects voice bands 3-9 (172-2756Hz) with reduced NR
//   - High spectral centroid (>2500Hz): reduce HF NR to preserve consonant detail
//   - Low spectral centroid (<1500Hz): increase LF NR for bass-heavy sources
//   - Noisy sources: increase voice band NR towards uniform (accept some voice coloration)
func tuneDolbySRBandProfile(config *FilterChainConfig, measurements *AudioMeasurements) {
	// Start with default voice-protective profile (copied from DefaultFilterConfig)
	// Scale factors: 1.0 = full NR, lower = voice protection
	bandScales := []float64{
		1.0, // Band 0: sub-bass - full NR
		1.0, // Band 1: bass - full NR
		0.7, // Band 2: chest resonance - moderate protection
		0.5, // Band 3: low formants - strong protection
		0.4, // Band 4: male fundamentals - strongest protection
		0.4, // Band 5: female fundamentals - strongest protection
		0.5, // Band 6: F1-F2 transition - strong protection
		0.5, // Band 7: core intelligibility - strong protection
		0.6, // Band 8: upper F2 - moderate protection
		0.7, // Band 9: consonant transitions - moderate protection
		0.8, // Band 10: sibilance begins - light protection
		0.9, // Band 11: primary sibilance - minimal protection
		1.0, // Band 12: consonant detail - full NR
		1.0, // Band 13: air/breath - full NR
		1.0, // Band 14: ultra-HF - full NR
	}

	// Adapt based on spectral centroid (voice presence indicator)
	if measurements.SpectralCentroid > 2500 {
		// High centroid = lots of HF content (crisp consonants, sibilance)
		// Reduce HF band NR to preserve consonant detail and air
		for i := 10; i < 15; i++ {
			bandScales[i] *= 0.7
		}
	}

	if measurements.SpectralCentroid < 1500 {
		// Low centroid = bass-heavy voice or room rumble
		// Can safely increase LF band NR
		bandScales[0] = min(bandScales[0]*1.2, 1.0)
		bandScales[1] = min(bandScales[1]*1.2, 1.0)
	}

	// Noisy sources need more uniform NR (accept some voice coloration for noise reduction)
	if measurements.NoiseFloor > dolbySRFloorNoisy {
		for i := 2; i < 10; i++ {
			bandScales[i] = min(bandScales[i]+0.2, 1.0)
		}
	}

	// Very clean sources can use even more voice protection
	if measurements.NoiseFloor < dolbySRFloorClean {
		for i := 3; i < 9; i++ {
			bandScales[i] *= 0.8 // Reduce voice band NR further
		}
	}

	config.DolbySRBandProfile = bandScales
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
	mix := calculateArnnDnMix(measurements, silenceFlatness, silenceEntropy)

	// If calculated mix is negligible, disable filter
	if mix < arnnDnMinMix {
		config.ArnnDnEnabled = false
		return
	}

	config.ArnnDnMix = mix
}

// calculateArnnDnMix computes the optimal arnndn mix based on measurements.
// Returns a value between arnnDnMixMin and arnnDnMixMax.
func calculateArnnDnMix(m *AudioMeasurements, silenceFlatness, silenceEntropy float64) float64 {
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

	// 2. Ratio: based on LRA (loudness range) - soft expander approach
	// Calculate ratio FIRST since threshold depends on it
	config.DS201GateRatio = calculateDS201GateRatio(measurements.InputLRA)

	// 1. Threshold: sits above noise/bleed peaks, below quiet speech
	// Gap is derived from ratio to achieve target reduction
	config.DS201GateThreshold = calculateDS201GateThreshold(
		measurements.NoiseFloor,
		silencePeak,
		silenceCrest,
		config.DS201GateRatio,
	)

	// 3. Attack: based on MaxDifference, SpectralFlux, and SpectralCrest
	// DS201-inspired: supports sub-millisecond attack for transient preservation
	config.DS201GateAttack = calculateDS201GateAttack(
		measurements.MaxDifference,
		measurements.SpectralFlux,
		measurements.SpectralCrest,
	)

	// 4. Release: based on flux, ZCR, and noise character (including entropy)
	// Includes +50ms compensation for lack of Hold parameter
	// Higher entropy = more broadband noise = faster release to cut noise quickly
	config.DS201GateRelease = calculateDS201GateRelease(
		measurements.SpectralFlux,
		measurements.ZeroCrossingsRate,
		silenceEntropy,
	)

	// 5. Range: based on silence entropy and noise floor
	config.DS201GateRange = calculateDS201GateRange(
		silenceEntropy,
		measurements.NoiseFloor,
	)

	// 6. Knee: based on spectral crest - soft knee for natural transitions
	config.DS201GateKnee = calculateDS201GateKnee(measurements.SpectralCrest)

	// 7. Detection: RMS for bleed, peak for clean
	config.DS201GateDetection = calculateDS201GateDetection(silenceEntropy, silenceCrest)

	// 8. Makeup: 1.0 (loudness normalisation handles it)
	config.DS201GateMakeup = 1.0
}

// calculateDS201GateThreshold determines threshold ensuring sufficient gap above noise
// for effective soft expansion. The gap is derived from the ratio (which comes from LRA)
// to achieve a target reduction depth.
//
// Soft expander math: reduction = gap × (1 - 1/ratio)
// Solving for gap: gap = targetReduction / (1 - 1/ratio)
//
// Examples for 12dB target reduction:
//   - 1.5:1 ratio (wide LRA)   → gap = 12 / (1 - 1/1.5) = 36 dB
//   - 2.0:1 ratio (moderate)   → gap = 12 / (1 - 1/2.0) = 24 dB
//   - 2.5:1 ratio (narrow LRA) → gap = 12 / (1 - 1/2.5) = 20 dB
//
// This makes the gate more aggressive when dynamics are narrow (tighter ratio),
// and more conservative when dynamics are wide (gentler ratio preserves expression).
//
// Approach:
// 1. For high-crest noise (transients/bleed): threshold = silencePeak + small margin
// 2. For stable noise: threshold = max(noiseFloor + derivedGap, targetThreshold)
// 3. Clamp to [minThreshold, maxThreshold] to protect quiet speech
func calculateDS201GateThreshold(noiseFloorDB, silencePeakDB, silenceCrestDB, ratio float64) float64 {
	var thresholdDB float64

	if silenceCrestDB > ds201GateCrestFactorThreshold && silencePeakDB != 0 {
		// Noise has transients (e.g., bleed from other mics) - threshold must clear peaks
		// Use peak + small margin to ensure gate opens cleanly
		thresholdDB = silencePeakDB + 3.0
	} else {
		// Derive minimum gap from ratio to achieve target reduction
		// gap = targetReduction / (1 - 1/ratio)
		minGapDB := ds201GateTargetReductionDB / (1.0 - 1.0/ratio)
		minGapThreshold := noiseFloorDB + minGapDB

		// Use whichever is higher: the derived gap threshold or the target threshold
		// This ensures clean recordings still get effective gating
		thresholdDB = max(minGapThreshold, ds201GateTargetThresholdDB)
	}

	// Safety limits - protect quiet speech while ensuring gate can still work
	thresholdDB = clamp(thresholdDB, ds201GateThresholdMinDB, ds201GateThresholdMaxDB)

	return dbToLinear(thresholdDB)
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
// This allows voices with more broadband room noise (like Martin's) to benefit from
// tighter release that cuts noise faster when speech stops, while preserving the
// slow release for tonal bleed/hum that would otherwise pump audibly.
func calculateDS201GateRelease(spectralFlux, zcr, silenceEntropy float64) float64 {
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

	return clamp(baseRelease, float64(ds201GateReleaseMin), float64(ds201GateReleaseMax))
}

// calculateDS201GateRange determines maximum attenuation depth based on noise character.
// Tonal noise (bleed, hum) sounds worse when hard-gated - use gentler range.
// Broadband noise can be gated more aggressively.
func calculateDS201GateRange(silenceEntropy, noiseFloorDB float64) float64 {
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

	rangeDB = clamp(rangeDB, float64(ds201GateRangeMinDB), float64(ds201GateRangeMaxDB))

	return dbToLinear(rangeDB)
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
	config.LA2AMakeup = sanitizeFloat(config.LA2AMakeup, defaultLA2AMakeup)

	// DS201-inspired gate threshold needs additional check for zero/negative
	if math.IsNaN(config.DS201GateThreshold) || math.IsInf(config.DS201GateThreshold, 0) || config.DS201GateThreshold <= 0 {
		config.DS201GateThreshold = ds201DefaultGateThreshold
	}

	// DS201-inspired hum filter sanitization
	config.DS201HumFrequency = sanitizeFloat(config.DS201HumFrequency, defaultHumFrequency)
	config.DS201HumWidth = sanitizeFloat(config.DS201HumWidth, defaultHumWidth)
	if config.DS201HumHarmonics < 1 || config.DS201HumHarmonics > 8 {
		config.DS201HumHarmonics = defaultHumHarmonics
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
