// Package processor handles audio analysis and processing
package processor

import (
	"fmt"
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
	lufsGapExtreme    = 25.0 // dB - extreme gap, gate needs special handling

	// Gentle gate mode: for extreme LUFS gap + low LRA
	// Very quiet recordings with uniform levels cause the gate's soft expansion
	// to apply varying gain reduction across similar speech levels, creating
	// volume modulation ("hunting"). Use gentler parameters to prevent this.
	ds201GateGentleLRAThreshold = 10.0 // LU - below this with extreme LUFS gap triggers gentle mode
	ds201GateGentleRatio        = 1.2  // Minimal gain variation in expansion zone
	ds201GateGentleKnee         = 2.0  // Sharper transition reduces hunting
	ds201GateGentleMakeup       = 1.0  // Unity gain - no makeup

	// De-esser intensity levels
	deessIntensityBright = 0.6 // Bright voice base intensity
	deessIntensityNormal = 0.5 // Normal voice base intensity
	deessIntensityDark   = 0.4 // Dark voice base intensity
	deessIntensityMax    = 0.8 // Maximum intensity limit
	deessIntensityMin    = 0.3 // Minimum before disabling

	// DC1 Declick tuning constants (CEDAR DC-1-inspired)
	// Enables conditionally based on impulsive content indicators
	// Uses OR logic: high MaxDiff OR high Crest can trigger (mouth noises have high crest but low MaxDiff)
	//
	// Enabling thresholds - based on MaxDifference (sample-to-sample jumps)
	dc1MaxDiffLikely   = 0.25 // > 25% full scale: likely clicks present
	dc1MaxDiffPossible = 0.12 // > 12% full scale: possible mild clicks/pops

	// Enabling thresholds - based on SpectralCrest (impulsive energy)
	// Mouth noises (lip smacks, pops) show high crest even with low MaxDiff
	dc1CrestLikely   = 50.0 // > 50 dB: strong impulsive content (likely mouth noises)
	dc1CrestPossible = 35.0 // > 35 dB: moderate impulsive content

	// Threshold adaptation (detection sensitivity, 1-8)
	dc1ThreshAggressive = 2.0 // For obvious click damage
	dc1ThreshMild       = 4.0 // For mild mouth clicks
	dc1ThreshMouthNoise = 5.0 // For subtle lip smacks (high crest, low MaxDiff)
	dc1ThreshConserv    = 6.0 // For clean with transients

	// Threshold adjustments based on spectral characteristics
	dc1FlatnessNoisy   = 0.3  // SpectralFlatness > 0.3: noisy signal, raise threshold
	dc1DynamicRangeLow = 10.0 // DynamicRange < 10dB: compressed audio, raise threshold

	// Window adaptation (40-80 ms)
	dc1WindowShort   = 45.0   // For fast speech, plosives (high centroid)
	dc1WindowDefault = 55.0   // Default balanced
	dc1WindowLong    = 70.0   // For bass-heavy content (low centroid)
	dc1CentroidFast  = 3000.0 // Hz - above: use shorter window
	dc1CentroidSlow  = 1500.0 // Hz - below: consider longer window

	// DNS-1500 adaptive tuning constants (CEDAR DNS-1500-inspired)
	// Primary noise reduction filter using learned noise profile from detected silence

	// Target noise floor after reduction (dBFS)
	// This is the goal - we calculate reduction needed to reach it
	dns1500TargetNoiseFloor = -70.0

	// Noise reduction limits (dB)
	dns1500NRMin = 6.0  // Minimum reduction (avoids artefacts on clean material)
	dns1500NRMax = 30.0 // Maximum reduction (avoids hollow/underwater sound)

	// Adaptivity tuning from InputLRA (loudness range)
	// High LRA = dynamic material = slower adaptation to avoid pumping
	// Low LRA = uniform material = faster adaptation is safe
	dns1500AdaptivityFast     = 0.3  // Fast adaptation for uniform material
	dns1500AdaptivityModerate = 0.5  // Default balanced
	dns1500AdaptivitySlow     = 0.7  // Slow adaptation for dynamic material
	dns1500LRAFastThresh      = 6.0  // LU - below: faster adaptation safe
	dns1500LRASlowThresh      = 15.0 // LU - above: slower adaptation needed

	// Gain smoothing tuning from noise character
	// High flatness = broadband noise = needs smoothing to reduce musical artefacts
	// Low flatness = tonal noise (hum) = less smoothing preserves precision
	dns1500GainSmoothMin      = 0   // No smoothing for tonal noise
	dns1500GainSmoothMax      = 20  // Heavy smoothing for broadband
	dns1500GainSmoothModerate = 8   // Moderate smoothing baseline
	dns1500FlatnessSmooth     = 0.5 // Above: broadband noise, needs smoothing
	dns1500EntropySmooth      = 0.7 // Above: random noise, needs smoothing

	// Residual floor headroom (dB above measured noise floor)
	dns1500ResidualHeadroom = 12.0

	// Voice protection for noisy sources
	// When noise floor is high, limit NR aggressiveness to preserve speech
	dns1500NoisySourceThreshold  = -55.0 // dBFS - above this, apply voice protection
	dns1500NRMaxNoisy            = 15.0  // Max NR for noisy sources (vs 30 normal)
	dns1500GainSmoothNoisy       = 15    // Higher smoothing for noisy sources
	dns1500ResidualHeadroomNoisy = 8.0   // Less headroom = higher residual floor

	// DS201 Gate coordination with DNS-1500
	// When DNS-1500 is active, the gate sees post-NR audio, so we adjust threshold/range
	// to account for the reduced noise floor after NR processing
	ds201GateDNS1500RangeBoostDB  = -6.0 // Extra range when DNS-1500 reduces noise (more aggressive gating OK)
	ds201GateDNS1500ThresholdDrop = 6.0  // dB - lower threshold by this much post-NR (noise peaks are gone)

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

	// Makeup gain: based on LUFS gap to target
	// Applies gentle gain recovery post-gate, avoiding normaliser artifacts
	// Conservative approach: only a fraction of gap, capped to avoid clipping
	ds201GateMakeupLUFSScale  = 0.25 // Apply 25% of LUFS gap as makeup
	ds201GateMakeupMinGapLUFS = 8.0  // Only apply makeup if gap > 8 LU
	ds201GateMakeupMaxDB      = 4.0  // Cap at 4 dB (~1.58 linear) to avoid clipping
	ds201GateMakeupTPHeadroom = -3.0 // Skip makeup if true peak already > -3 dBTP

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

	// LA-2A Makeup Gain: LUFS-based gain staging (like DS201 gate)
	// Rather than estimating gain reduction, use LUFS gap to target for
	// consistent loudness alignment across the processing chain.
	la2aMakeupLUFSScale           = 0.35 // Apply 35% of LUFS gap as makeup (more than gate's 25%)
	la2aMakeupMinGapLUFS          = 6.0  // Only apply makeup if gap > 6 LU (lower threshold than gate)
	la2aMakeupMaxDB               = 6.0  // dB maximum makeup (compressor can handle more than gate)
	la2aMakeupTPHeadroom          = -2.0 // Skip makeup if true peak already > -2 dBTP
	la2aMakeupMin                 = 0.0  // dB minimum makeup (can be zero)
	la2aMakeupMax                 = 6.0  // dB maximum makeup (legacy, for clamp)
	la2aMakeupDolbySRCompensation = 1.3  // dB - compensates for Linkwitz-Riley crossover level loss when DolbySR enabled

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

	// ==========================================================================
	// UREI 1176-Inspired Limiter Parameters
	// ==========================================================================
	// The UREI 1176 Peak Limiter (1967) pioneered microsecond-level attack times
	// and program-dependent release. As a safety limiter, we implement its
	// philosophy using FFmpeg's alimiter with adaptive attack/release/ASC.
	// Key characteristics:
	// - Ultra-fast FET attack (20µs-800µs) — we use 0.1-1.0ms (FFmpeg minimum)
	// - Program-dependent two-stage release — approximated via ASC mode
	// - Soft-knee via ASC — releases to average attenuation, not unity
	// ==========================================================================

	// UREI 1176-inspired attack times (FFmpeg minimum ~0.1ms vs 1176's 20µs)
	// Lookahead compensates for the slower digital attack
	u1176AttackExtreme = 0.1 // ms - extreme transients (plosives)
	u1176AttackSharp   = 0.5 // ms - sharp consonants
	u1176AttackNormal  = 0.8 // ms - standard speech
	u1176AttackGentle  = 1.0 // ms - soft delivery

	// Transient detection thresholds (MaxDifference as fraction 0-1)
	u1176MaxDiffExtreme = 0.25 // 25% - extreme transients trigger fastest attack
	u1176MaxDiffSharp   = 0.15 // 15% - sharp transients
	u1176MaxDiffNormal  = 0.08 // 8% - normal speech threshold

	// Spectral crest thresholds (dB) - secondary transient indicator
	u1176CrestExtreme = 50.0 // dB - extremely peaked spectrum
	u1176CrestSharp   = 35.0 // dB - notably peaked

	// UREI 1176-inspired release times (matching hardware range 50-1100ms)
	u1176ReleaseExpressive = 200.0 // ms - wide dynamics (high flux + wide LRA)
	u1176ReleaseStandard   = 150.0 // ms - typical podcast delivery
	u1176ReleaseControlled = 100.0 // ms - narrow dynamics (low flux + narrow LRA)
	u1176ReleaseDRBoost    = 50.0  // ms - added for very wide dynamic range

	// Release tuning thresholds
	u1176FluxDynamic = 0.03 // Above: dynamic/expressive content
	u1176FluxStatic  = 0.01 // Below: controlled/monotone content
	u1176LRAWide     = 15.0 // LU - above: wide loudness range
	u1176LRANarrow   = 10.0 // LU - below: narrow loudness range
	u1176DRWide      = 35.0 // dB - above: add release boost

	// ASC (Auto Soft Clipping) - approximates 1176 program-dependent release
	// When enabled, limiter releases to average attenuation rather than unity
	u1176ASCDynamic    = 0.7 // For dynamic content (wide DR or high crest)
	u1176ASCModerate   = 0.5 // For moderate content
	u1176ASCControlled = 0.3 // For controlled content (narrow DR)
	u1176ASCNoisyBoost = 0.2 // Additional for noisy recordings (masks pumping)

	// ASC enabling thresholds
	u1176DREnableASC    = 30.0  // dB - above: enable ASC with dynamic level
	u1176DRModerateASC  = 20.0  // dB - above: enable ASC with moderate level
	u1176CrestEnableASC = 40.0  // dB - above: enable ASC (prominent peaks)
	u1176NoiseFloorASC  = -50.0 // dBFS - above: boost ASC to mask pumping

	// Ceiling (dBTP) - hardcoded podcast standard for now
	u1176CeilingPodcast = -1.0 // dBTP - podcast/broadcast safety

	// Gain staging (configurable, default unity)
	u1176InputLevelDefault  = 1.0 // Linear - unity gain
	u1176OutputLevelDefault = 1.0 // Linear - unity gain

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

	// ==========================================================================
	// Dolby SR mcompand Expansion Tuning
	// ==========================================================================
	// Dolby SR mcompand adaptive tuning constants.
	// Validated across 3 presenters (Mark, Martin, Popey).
	// Uses RMS trough (noise floor) to select threshold AND expansion in lockstep.
	//
	// Simplified tuning (threshold + expansion based on trough):
	//   < -85 dB:       -50 dB threshold, 16 dB expansion (clean)
	//   -85 to -80 dB:  -45 dB threshold, 20 dB expansion (moderate)
	//   > -80 dB:       -40 dB threshold, 24 dB expansion (noisy)
	dolbySRThresholdClean    = -50.0 // dB - default for clean sources
	dolbySRThresholdModerate = -45.0 // dB - raised for moderate noise
	dolbySRThresholdNoisy    = -40.0 // dB - raised for noisy sources

	dolbySRExpansionClean    = 16.0 // dB - gentle treatment for clean sources
	dolbySRExpansionModerate = 20.0 // dB - balanced treatment
	dolbySRExpansionNoisy    = 24.0 // dB - aggressive for noisy sources

	// Trough thresholds for tier selection
	dolbySRTroughClean    = -85.0 // dBFS - below: clean source
	dolbySRTroughModerate = -80.0 // dBFS - below: moderate noise
	// Above -80 dB: noisy source
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
	tuneDC1Declick(config, measurements)             // CEDAR DC-1 inspired declicker

	// Noise reduction: DNS-1500 primary, DolbySR fallback
	// If DolbySR is pre-enabled (forced by caller), skip DNS-1500 entirely
	if config.DolbySREnabled {
		// DolbySR forced on - skip DNS-1500, just tune DolbySR
		tuneDolbySR(config, measurements, lufsGap)
	} else {
		// Normal flow: try DNS-1500 first, fall back to DolbySR
		tuneDNS1500(config, measurements)

		if config.DNS1500Enabled {
			config.DolbySREnabled = false
		} else {
			// Fall back to DolbySR when no silence detected for DNS-1500
			config.DolbySREnabled = true
			tuneDolbySR(config, measurements, lufsGap)
		}
	}

	tuneArnndn(config, measurements, lufsGap) // RNN denoise (LUFS gap + noise floor based)
	tuneDS201Gate(config, measurements)       // DS201-style soft expander gate
	tuneDeesser(config, measurements)
	tuneLA2ACompressor(config, measurements)
	tuneUREI1176Limiter(config, measurements) // 1176-inspired safety limiter

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

// tuneDC1Declick conditionally enables and adapts the CEDAR DC-1-inspired declicker.
//
// The DC-1 philosophy: most recordings don't need declicking. Only enable when
// Pass 1 measurements suggest impulsive content (clicks, pops, mouth noises).
//
// Decision tree:
//   - MaxDifference > 0.35 AND SpectralCrest > 30dB → Enable (likely clicks)
//   - MaxDifference > 0.25 AND SpectralCrest > 25dB → Enable with higher threshold (mild)
//   - Otherwise → Disable (clean recording)
//
// When enabled, adapts:
//   - Threshold: based on SpectralCrest, SpectralFlatness, DynamicRange
//   - Window: based on SpectralCentroid (fast content → shorter windows)
func tuneDC1Declick(config *FilterChainConfig, measurements *AudioMeasurements) {
	// MaxDifference from astats is in sample units (0-32768 for 16-bit audio)
	// Normalize to 0-1 ratio for comparison with thresholds
	maxDiff := measurements.MaxDifference / 32768.0
	crest := measurements.SpectralCrest

	// Decision: enable when impulsive content detected
	// Uses OR logic: high MaxDiff OR high Crest can trigger independently
	// Mouth noises (lip smacks) have high crest but often low MaxDiff
	switch {
	case maxDiff > dc1MaxDiffLikely:
		// Strong transient indicator - likely clicks/pops
		config.DC1DeclickEnabled = true
		config.DC1DeclickThreshold = dc1ThreshAggressive
		config.DC1DeclickReason = fmt.Sprintf("likely clicks (MaxDiff=%.1f%%)",
			maxDiff*100)

	case crest > dc1CrestLikely:
		// High spectral crest alone - likely mouth noises
		// Use conservative settings since crest can be high in clean speech too
		config.DC1DeclickEnabled = true
		config.DC1DeclickThreshold = dc1ThreshMouthNoise
		config.DC1DeclickReason = fmt.Sprintf("mouth noises (Crest=%.1fdB)",
			crest)

	case maxDiff > dc1MaxDiffPossible && crest > dc1CrestPossible:
		// Both indicators elevated - mild clicks
		config.DC1DeclickEnabled = true
		config.DC1DeclickThreshold = dc1ThreshMild
		config.DC1DeclickReason = fmt.Sprintf("possible clicks (MaxDiff=%.1f%%, Crest=%.1fdB)",
			maxDiff*100, crest)

	case crest > dc1CrestPossible:
		// Moderate crest alone - possible subtle mouth noises
		config.DC1DeclickEnabled = true
		config.DC1DeclickThreshold = dc1ThreshConserv
		config.DC1DeclickReason = fmt.Sprintf("possible mouth noises (Crest=%.1fdB)",
			crest)

	default:
		// Clean recording - skip declicking
		config.DC1DeclickEnabled = false
		config.DC1DeclickReason = fmt.Sprintf("clean recording (MaxDiff=%.1f%%, Crest=%.1fdB)",
			maxDiff*100, crest)
		return
	}

	// Set default values for non-adaptive parameters (ensures valid filter spec)
	config.DC1DeclickOverlap = 75.0 // 75% overlap (good quality)
	config.DC1DeclickAROrder = 2.0  // 2% AR model order (low CPU)
	config.DC1DeclickBurst = 2.0    // 2% burst handling (standard)
	config.DC1DeclickMethod = "s"   // overlap-save (better quality)

	// Threshold refinement based on spectral characteristics
	if measurements.SpectralFlatness > dc1FlatnessNoisy {
		// Noisy signal - raise threshold to avoid false positives
		config.DC1DeclickThreshold = math.Min(config.DC1DeclickThreshold+2.0, dc1ThreshConserv)
		config.DC1DeclickReason += "; +threshold (noisy)"
	}
	if measurements.DynamicRange < dc1DynamicRangeLow {
		// Compressed audio - raise threshold to protect dynamics
		config.DC1DeclickThreshold = math.Min(config.DC1DeclickThreshold+1.0, dc1ThreshConserv)
		config.DC1DeclickReason += "; +threshold (compressed)"
	}

	// Window adaptation based on content type
	switch {
	case measurements.SpectralCentroid > dc1CentroidFast:
		// Fast speech/plosives - shorter window preserves transients
		config.DC1DeclickWindow = dc1WindowShort
	case measurements.SpectralCentroid < dc1CentroidSlow:
		// Bass-heavy content - longer window for better LF reconstruction
		config.DC1DeclickWindow = dc1WindowLong
	default:
		config.DC1DeclickWindow = dc1WindowDefault
	}
}

// tuneDNS1500 adapts CEDAR DNS-1500-inspired noise reduction based on Pass 1 measurements.
//
// Enable decision:
// - Requires valid NoiseProfile with silence region (duration > 0)
// - If no silence detected, filter stays disabled → DolbySR fallback
//
// Noise reduction calculation:
// - Target noise floor is dns1500TargetNoiseFloor (-70 dBFS)
// - Reduction = MeasuredNoiseFloor - TargetNoiseFloor
// - Clamped to [dns1500NRMin, dns1500NRMax] to avoid artefacts
//
// Adaptivity from InputLRA:
// - Low LRA (< 6 LU): uniform material, fast adaptation safe
// - High LRA (> 15 LU): dynamic material, slow adaptation to avoid pumping
//
// Gain smoothing from noise character:
// - High SpectralFlatness: broadband noise → higher smoothing
// - Low SpectralFlatness: tonal noise (hum) → lower smoothing
func tuneDNS1500(config *FilterChainConfig, measurements *AudioMeasurements) {
	// ─── Enable decision: require valid NoiseProfile ────────────────────────────
	if measurements.NoiseProfile == nil || measurements.NoiseProfile.Duration == 0 {
		config.DNS1500Enabled = false
		return
	}

	config.DNS1500Enabled = true
	np := measurements.NoiseProfile

	// ─── Silence timing from NoiseProfile ───────────────────────────────────────
	config.DNS1500SilenceStart = np.Start.Seconds()
	config.DNS1500SilenceEnd = np.Start.Seconds() + np.Duration.Seconds()

	// ─── Noise floor from measured value ────────────────────────────────────────
	config.DNS1500NoiseFloor = clamp(np.MeasuredNoiseFloor, -80.0, -20.0)

	// ─── Detect noisy source for voice protection ──────────────────────────────
	// High noise floor indicates noisy recording environment - limit NR to protect vocals
	isNoisySource := np.MeasuredNoiseFloor > dns1500NoisySourceThreshold

	// ─── Noise reduction: gap between measured and target ───────────────────────
	// Calculate how much reduction is needed to reach target noise floor
	noiseGap := np.MeasuredNoiseFloor - dns1500TargetNoiseFloor

	// Cap NR for noisy sources to prevent eating into vocals
	maxNR := dns1500NRMax
	if isNoisySource {
		maxNR = dns1500NRMaxNoisy
	}
	config.DNS1500NoiseReduce = clamp(noiseGap, dns1500NRMin, maxNR)

	// ─── Adaptivity from InputLRA (loudness range) ──────────────────────────────
	// High LRA = dynamic material = slower adaptation to avoid pumping
	// Low LRA = uniform material = faster adaptation is safe
	switch {
	case measurements.InputLRA < dns1500LRAFastThresh:
		config.DNS1500Adaptivity = dns1500AdaptivityFast
	case measurements.InputLRA > dns1500LRASlowThresh:
		config.DNS1500Adaptivity = dns1500AdaptivitySlow
	default:
		config.DNS1500Adaptivity = dns1500AdaptivityModerate
	}

	// ─── Gain smoothing from noise character ────────────────────────────────────
	// Use NoiseProfile spectral flatness if available, else overall measurement
	flatness := np.SpectralFlatness
	if flatness == 0 {
		flatness = measurements.SpectralFlatness // Fallback to overall
	}

	// High flatness/entropy = broadband noise = needs smoothing
	// Low flatness = tonal noise (hum) = less smoothing
	if flatness > dns1500FlatnessSmooth || np.Entropy > dns1500EntropySmooth {
		// Scale smoothing with flatness intensity
		t := (flatness - dns1500FlatnessSmooth) / (1.0 - dns1500FlatnessSmooth)
		config.DNS1500GainSmooth = int(lerp(
			float64(dns1500GainSmoothModerate),
			float64(dns1500GainSmoothMax),
			clamp(t, 0, 1),
		))
	} else {
		// Tonal noise: minimal smoothing for precision
		config.DNS1500GainSmooth = dns1500GainSmoothMin
	}

	// Noisy sources get additional smoothing to prevent musical artefacts
	if isNoisySource && config.DNS1500GainSmooth < dns1500GainSmoothNoisy {
		config.DNS1500GainSmooth = dns1500GainSmoothNoisy
	}

	// ─── Residual floor relative to measured noise ──────────────────────────────
	// Noisy sources use less headroom = higher residual floor = more voice preserved
	headroom := dns1500ResidualHeadroom
	if isNoisySource {
		headroom = dns1500ResidualHeadroomNoisy
	}
	config.DNS1500ResidFloor = clamp(
		np.MeasuredNoiseFloor+headroom,
		-80.0, -20.0,
	)

	// ─── Track noise always enabled ─────────────────────────────────────────────
	config.DNS1500TrackNoise = true
}

// tuneDolbySR adapts the Dolby SR-inspired 6-band mcompand expander based on measurements.
//
// Simplified strategy: threshold and expansion are tuned in lockstep based on RMS trough.
// Noisier sources get both raised threshold (catches more noise) and deeper expansion.
//
// Tier selection (based on RMS trough):
//
//	< -85 dB:       -50 dB threshold, 16 dB expansion (clean)
//	-85 to -80 dB:  -45 dB threshold, 20 dB expansion (moderate)
//	> -80 dB:       -40 dB threshold, 24 dB expansion (noisy)
func tuneDolbySR(config *FilterChainConfig, measurements *AudioMeasurements, _ float64) {
	if !config.DolbySREnabled {
		return
	}

	// Initialize with validated 6-band voice-protective configuration
	config.DolbySRBands = defaultDolbySRBands()

	// Select threshold AND expansion in lockstep based on RMS trough
	noiseFloor := measurements.RMSTrough
	switch {
	case noiseFloor < dolbySRTroughClean:
		// Clean source: gentle treatment
		config.DolbySRThresholdDB = dolbySRThresholdClean // -50 dB
		config.DolbySRExpansionDB = dolbySRExpansionClean // 16 dB
	case noiseFloor < dolbySRTroughModerate:
		// Moderate noise: balanced treatment
		config.DolbySRThresholdDB = dolbySRThresholdModerate // -45 dB
		config.DolbySRExpansionDB = dolbySRExpansionModerate // 20 dB
	default:
		// Noisy source: aggressive treatment
		config.DolbySRThresholdDB = dolbySRThresholdNoisy // -40 dB
		config.DolbySRExpansionDB = dolbySRExpansionNoisy // 24 dB
	}

	// Makeup gain is fixed (Linkwitz-Riley crossover compensation)
	// Already set to default in DefaultFilterConfig
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

	// Calculate LUFS gap for threshold decision
	lufsGap := config.TargetI - measurements.InputI
	if lufsGap < 0 {
		lufsGap = 0
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
		lufsGap,
	)

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

	// ─── DNS-1500 coordination: adjust gate for post-NR noise characteristics ────
	// When DNS-1500 is active on a noisy source, the gate sees post-NR audio.
	// We can be more aggressive since DNS-1500 has already reduced the noise floor.
	if config.DNS1500Enabled && measurements.NoiseProfile != nil {
		np := measurements.NoiseProfile

		// For noisy sources (floor > -55 dBFS), DNS-1500 caps NR at 15 dB
		// The gate should account for this reduction in bleed/noise peaks
		if np.MeasuredNoiseFloor > dns1500NoisySourceThreshold {
			// Boost range (more negative = deeper gating)
			// Post-NR, we can gate more aggressively without cutting into clean audio
			rangeDB += ds201GateDNS1500RangeBoostDB

			// Recalculate threshold for post-NR conditions
			// The bleed peaks will be reduced by ~dns1500NRMaxNoisy dB
			// So we can lower the threshold to catch the attenuated bleed
			estimatedPostNRPeak := silencePeak - config.DNS1500NoiseReduce // Post-NR peak is lower (NR reduces it)
			postNRThreshold := estimatedPostNRPeak + ds201GateDNS1500ThresholdDrop

			// Convert to linear and use the lower (more aggressive) threshold
			postNRThresholdLinear := DbToLinear(clamp(postNRThreshold, ds201GateThresholdMinDB, ds201GateThresholdMaxDB))
			if postNRThresholdLinear < config.DS201GateThreshold {
				config.DS201GateThreshold = postNRThresholdLinear
			}
		}
	}

	// Clamp range and convert to linear
	rangeDB = clamp(rangeDB, float64(ds201GateRangeMinDB), float64(ds201GateRangeMaxDB))
	config.DS201GateRange = DbToLinear(rangeDB)

	// 6. Knee: based on spectral crest - soft knee for natural transitions
	config.DS201GateKnee = calculateDS201GateKnee(measurements.SpectralCrest)

	// 7. Detection: RMS for bleed, peak for clean
	config.DS201GateDetection = calculateDS201GateDetection(silenceEntropy, silenceCrest)

	// 8. Makeup: always unity - gain staging consolidated in LA2A compressor
	// See tuneLA2AMakeup for the compensating gain calculation.
	config.DS201GateMakeup = 1.0

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
//
// Special case for extreme LUFS gaps (>25 dB):
// Very quiet recordings have exaggerated crest factors due to the low signal level.
// Small peaks in silence appear large relative to the quiet RMS. Using peak-reference
// in this case places the threshold too close to quiet speech, causing modulation.
// Instead, use noise-floor-based threshold which is more reliable for quiet recordings.
func calculateDS201GateThreshold(noiseFloorDB, silencePeakDB, silenceCrestDB, ratio, lufsGap float64) float64 {
	var thresholdDB float64

	// For extreme LUFS gaps, skip peak-reference approach
	// Very quiet recordings have exaggerated crest factors; noise floor is more reliable
	usePeakReference := silenceCrestDB > ds201GateCrestFactorThreshold &&
		silencePeakDB != 0 &&
		lufsGap < lufsGapExtreme

	if usePeakReference {
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

	return DbToLinear(thresholdDB)
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

// tuneLA2AMakeup sets makeup gain based on LUFS gap to target.
//
// This uses LUFS-based gain staging rather than estimating gain reduction from
// compression parameters. This provides:
// - More consistent loudness alignment across the processing chain
// - Better integration with downstream normalisation stages
// - Predictable behaviour regardless of input dynamics
//
// Gain staging consolidation:
// - When DolbySR is enabled, adds 1.3 dB to compensate for Linkwitz-Riley crossover loss
// - When DS201Gate is enabled (not in gentle mode), adds the gate's calculated makeup
//
// This consolidates gain staging in a single location rather than spreading it across filters.
func tuneLA2AMakeup(config *FilterChainConfig, measurements *AudioMeasurements) {
	makeup := calculateLA2AMakeup(
		measurements.InputI,
		measurements.InputTP,
		config.TargetI,
	)

	// Add DolbySR crossover compensation when enabled
	if config.DolbySREnabled {
		makeup += la2aMakeupDolbySRCompensation
	}

	// Add DS201 gate makeup compensation when gate is enabled (and not in gentle mode)
	// Gentle mode is for extreme LUFS gaps where makeup would cause issues
	if config.DS201GateEnabled && !config.DS201GateGentleMode {
		gateLinear := calculateDS201GateMakeup(
			measurements.InputI,
			measurements.InputTP,
			config.TargetI,
		)
		// Convert linear to dB (gate returns linear, LA2A uses dB)
		if gateLinear > 1.0 {
			makeup += LinearToDb(gateLinear)
		}
	}

	config.LA2AMakeup = makeup
}

// calculateDS201GateMakeup determines post-gate makeup gain based on LUFS gap.
//
// Rather than relying on later normalisation stages which can introduce artifacts,
// this provides gentle gain recovery immediately after gating. The makeup is:
// - A fraction of the LUFS gap to target (conservative approach)
// - Only applied if the gap exceeds a minimum threshold
// - Capped to avoid clipping (considers true peak headroom)
// - Returned as linear gain (1.0 = unity, 2.0 = +6dB)
//
// This helps quiet recordings come up without the pumping/breathing artifacts
// that dynamic normalisers can introduce.
func calculateDS201GateMakeup(inputLUFS, inputTP, targetLUFS float64) float64 {
	// Calculate LUFS gap to target
	lufsGap := targetLUFS - inputLUFS
	if lufsGap < 0 {
		lufsGap = 0 // Audio is already louder than target
	}

	// Skip makeup if gap is small (audio is already close to target)
	if lufsGap < ds201GateMakeupMinGapLUFS {
		return 1.0
	}

	// Skip makeup if true peak is already high (no headroom)
	if inputTP > ds201GateMakeupTPHeadroom {
		return 1.0
	}

	// Calculate makeup as fraction of gap
	makeupDB := lufsGap * ds201GateMakeupLUFSScale

	// Cap to maximum to avoid clipping
	if makeupDB > ds201GateMakeupMaxDB {
		makeupDB = ds201GateMakeupMaxDB
	}

	// Also limit based on true peak headroom
	// If TP is -5 dBTP, we have ~5 dB headroom before clipping at -0.3 dBTP
	tpHeadroom := -0.3 - inputTP // How much room before target TP
	if makeupDB > tpHeadroom {
		makeupDB = tpHeadroom
	}

	// Don't apply negative makeup
	if makeupDB < 0 {
		return 1.0
	}

	// Convert dB to linear for agate's makeup parameter
	return DbToLinear(makeupDB)
}

// calculateLA2AMakeup determines post-compression makeup gain based on LUFS gap.
//
// Similar to DS201 gate makeup but slightly more aggressive since:
// - Compressor is earlier in chain (more stages to refine afterwards)
// - Compressor reduces dynamics, so peaks are less likely to clip
// - Limiter at end of chain provides safety net
//
// Returns makeup gain in dB (not linear, unlike DS201 gate which returns linear).
func calculateLA2AMakeup(inputLUFS, inputTP, targetLUFS float64) float64 {
	// Calculate LUFS gap to target
	lufsGap := targetLUFS - inputLUFS
	if lufsGap < 0 {
		lufsGap = 0 // Audio is already louder than target
	}

	// Skip makeup if gap is small (audio is already close to target)
	if lufsGap < la2aMakeupMinGapLUFS {
		return la2aMakeupMin
	}

	// Skip makeup if true peak is already high (no headroom)
	if inputTP > la2aMakeupTPHeadroom {
		return la2aMakeupMin
	}

	// Calculate makeup as fraction of gap
	makeupDB := lufsGap * la2aMakeupLUFSScale

	// Cap to maximum to avoid over-driving
	if makeupDB > la2aMakeupMaxDB {
		makeupDB = la2aMakeupMaxDB
	}

	// Also limit based on true peak headroom
	// If TP is -5 dBTP, we have ~5 dB headroom before clipping at -0.3 dBTP
	// But compressor reduces peaks, so we can be slightly more aggressive
	tpHeadroom := -0.3 - inputTP
	if makeupDB > tpHeadroom {
		makeupDB = tpHeadroom
	}

	// Don't apply negative makeup
	if makeupDB < la2aMakeupMin {
		return la2aMakeupMin
	}

	return makeupDB
}

// ==========================================================================
// UREI 1176-Inspired Limiter Tuning Functions
// ==========================================================================

// tuneUREI1176Limiter adapts the UREI 1176-inspired limiter based on Pass 1 measurements.
// The 1176's character comes from:
// - Ultra-fast FET attack catching peaks while preserving attack character
// - Program-dependent two-stage release (approximated via ASC)
// - Soft-knee behaviour at high ratios (ASC provides averaging)
//
// As a safety limiter at chain end, it catches peaks that slip through earlier processing.
// Loudness normalisation is handled upstream; this focuses purely on peak protection.
func tuneUREI1176Limiter(config *FilterChainConfig, measurements *AudioMeasurements) {
	tuneUREI1176Attack(config, measurements)
	tuneUREI1176Release(config, measurements)
	tuneUREI1176ASC(config, measurements)
	// Ceiling and gain levels use defaults (configurable via Option C)
}

// tuneUREI1176Attack sets attack time based on transient characteristics.
// The 1176's FET responds in microseconds; FFmpeg's minimum is ~0.1ms but
// lookahead compensates by "seeing" peaks coming.
//
// Uses MaxDifference (sample-to-sample jumps) and SpectralCrest (impulsive energy)
// to detect transient sharpness:
// - Extreme transients (plosives): 0.1ms attack
// - Sharp consonants: 0.5ms attack
// - Normal speech: 0.8ms attack
// - Soft delivery: 1.0ms attack
func tuneUREI1176Attack(config *FilterChainConfig, m *AudioMeasurements) {
	// MaxDifference from astats is in sample units (0-32768 for 16-bit audio)
	// Normalize to 0-1 ratio for comparison with thresholds
	maxDiff := m.MaxDifference / 32768.0

	// Check for extreme transients (plosives, hard consonants)
	if maxDiff > u1176MaxDiffExtreme || m.SpectralCrest > u1176CrestExtreme {
		config.UREI1176Attack = u1176AttackExtreme
		return
	}

	// Sharp transients
	if maxDiff > u1176MaxDiffSharp || m.SpectralCrest > u1176CrestSharp {
		config.UREI1176Attack = u1176AttackSharp
		return
	}

	// Normal transients
	if maxDiff > u1176MaxDiffNormal {
		config.UREI1176Attack = u1176AttackNormal
		return
	}

	// Soft delivery - minimal limiting needed
	config.UREI1176Attack = u1176AttackGentle
}

// tuneUREI1176Release sets release time based on dynamics and flux.
// The 1176's two-stage release (fast after transients, slow during sustained compression)
// is approximated by combining base release with ASC mode.
//
// Uses SpectralFlux (frame-to-frame change) and InputLRA (macro-dynamics):
// - High flux + wide LRA (expressive): 200ms release
// - Moderate: 150ms release
// - Low flux + narrow LRA (controlled): 100ms release
// - Very wide dynamic range: +50ms boost
func tuneUREI1176Release(config *FilterChainConfig, m *AudioMeasurements) {
	var baseRelease float64

	// Classify based on flux and LRA
	switch {
	case m.SpectralFlux > u1176FluxDynamic && m.InputLRA > u1176LRAWide:
		// Expressive delivery - preserve dynamics with longer release
		baseRelease = u1176ReleaseExpressive
	case m.SpectralFlux < u1176FluxStatic && m.InputLRA < u1176LRANarrow:
		// Controlled delivery - quick response OK
		baseRelease = u1176ReleaseControlled
	default:
		// Standard podcast delivery
		baseRelease = u1176ReleaseStandard
	}

	// Add recovery time for very wide dynamic range
	if m.DynamicRange > u1176DRWide {
		baseRelease += u1176ReleaseDRBoost
	}

	config.UREI1176Release = baseRelease
}

// tuneUREI1176ASC enables Auto Soft Clipping to approximate program-dependent release.
// When enabled, the limiter releases to an average attenuation level rather than unity,
// mimicking the 1176's behaviour of "staying in compression" during loud passages.
//
// Uses DynamicRange and SpectralCrest to determine ASC parameters:
// - Wide DR (>30dB) or high crest (>40dB): ASC 0.7 (dynamic content)
// - Moderate DR (>20dB): ASC 0.5 (standard smoothing)
// - Narrow DR: ASC off (direct limiting fine)
// - Noisy recordings: boost ASC to mask pumping artefacts
func tuneUREI1176ASC(config *FilterChainConfig, m *AudioMeasurements) {
	// Enable ASC for dynamic content
	switch {
	case m.DynamicRange > u1176DREnableASC || m.SpectralCrest > u1176CrestEnableASC:
		config.UREI1176ASC = true
		config.UREI1176ASCLevel = u1176ASCDynamic
	case m.DynamicRange > u1176DRModerateASC:
		config.UREI1176ASC = true
		config.UREI1176ASCLevel = u1176ASCModerate
	default:
		config.UREI1176ASC = false
		config.UREI1176ASCLevel = 0
		return // Early return - no ASC, no noise boost
	}

	// Boost ASC for noisy recordings (helps mask pumping artefacts)
	if m.NoiseFloor > u1176NoiseFloorASC {
		config.UREI1176ASCLevel = math.Min(1.0, config.UREI1176ASCLevel+u1176ASCNoisyBoost)
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
