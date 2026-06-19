// Package processor handles audio analysis and processing
package processor

import (
	"math"
	"strconv"
	"strings"
)

// AdaptConfig tunes all filter parameters based on Pass 1 measurements.
// This is the main entry point for adaptive configuration.
// It returns per-file effective config and diagnostics without mutating the caller's base seed.
func AdaptConfig(config *BaseFilterConfig, measurements *AudioMeasurements) (*EffectiveFilterConfig, *AdaptiveDiagnostics) {
	effectiveConfig := deriveEffectiveFilterConfig(config)
	if effectiveConfig == nil {
		return nil, nil
	}
	diagnostics := &AdaptiveDiagnostics{}

	// Tune each filter adaptively based on measurements
	// Order matters: gate threshold calculated BEFORE denoise filters
	// The rumble highpass is fixed (80 Hz, 12 dB/oct) from defaultRumbleHighPassConfig; no tuning step.
	tuneBandlimitLowPass(effectiveConfig, diagnostics, measurements) // Unconditional 20.5 kHz band-limit

	// NoiseReduction (anlmdn + afftdn): anlmdn is fixed from spike validation and
	// afftdn nr is fixed at 12 to avoid warble. afftdn has two adaptations: it is
	// dropped on voice-activated captures, and otherwise its nf tracks the measured
	// noise floor with track_noise off.
	tuneNoiseReduction(effectiveConfig, diagnostics, measurements)

	tuneSpeechGate(effectiveConfig, diagnostics, measurements) // Soft expander gate cleaning inter-speech gaps
	tuneDeesser(effectiveConfig, measurements)
	tuneLevellingCompressor(effectiveConfig, measurements)
	// The limiter lives in Pass 4 and is tuned from Pass 3 measurements, not here.

	// Final safety checks
	sanitizeConfig(effectiveConfig)

	return effectiveConfig, diagnostics
}

// afftdn's nf parameter accepts a noise floor in [-80, -20] dB. The measured
// Noise.Floor (clamped to [-90, -30] in the analyser) is re-clamped to this range.
const (
	afftdnNoiseFloorMinDB = -80.0
	afftdnNoiseFloorMaxDB = -20.0
)

// Measured custom afftdn profile gates. The custom spectral shape (nt=custom:bn)
// is used only when the room-tone band measurement is trustworthy: a clear
// speech/noise gap so the elected room tone is genuine ambience, and a flat
// (noise-like) room-tone spectrum so the measured shape describes broadband
// noise rather than tonal bleed. Both thresholds are corpus-derived.
const (
	// afftdnCustomMinSeparationDB is the minimum gate separation (voiced p10 minus
	// noise p95) for the custom profile. Below it the room tone may be contaminated
	// by speech, so the measured shape is untrustworthy.
	afftdnCustomMinSeparationDB = 12.0
	// afftdnCustomMinFlatness is the minimum room-tone spectral flatness for the
	// custom profile. Below it the room tone is tonal (hum, resonance) rather than
	// broadband, so a measured shape risks over-fitting tonal peaks.
	afftdnCustomMinFlatness = 0.45
)

// afftdnBandShapeClipDB bounds each emitted bn value; afftdn clips bn to
// [-24, +24] dB internally, so the builder matches that range.
const afftdnBandShapeClipDB = 24.0

// buildAfftdnBandNoise turns a per-band RMS vector into the afftdn bn string: each
// band is expressed RELATIVE to the band mean (white noise = all zeros), clipped
// to afftdn's [-24, +24] dB range, formatted "%.1f" and joined by "|". Returns
// the empty string for an empty input so the caller falls back to the white path.
//
// The top band (centre 24000 Hz) sits above the 20.5 kHz band-limit and at or
// above Nyquist for 48 kHz audio, so it has no measurable noise and comes back
// non-finite (NaN/Inf). Such bands must not poison the mean or emit a NaN/Inf
// token (afftdn would reject it). The mean is taken over finite bands only, and a
// non-finite band is emitted as 0.0 (flat, the white reference for a band with no
// measurable noise). If no band is finite there is no shape to emit, so return
// empty and let the caller fall back to the white path.
func buildAfftdnBandNoise(bands []float64) string {
	if len(bands) == 0 {
		return ""
	}

	var sum float64
	var finite int
	for _, v := range bands {
		if isFinite(v) {
			sum += v
			finite++
		}
	}
	if finite == 0 {
		return ""
	}
	mean := sum / float64(finite)

	parts := make([]string, len(bands))
	for i, v := range bands {
		if !isFinite(v) {
			parts[i] = strconv.FormatFloat(0.0, 'f', 1, 64)
			continue
		}
		shape := v - mean
		shape = max(-afftdnBandShapeClipDB, min(afftdnBandShapeClipDB, shape))
		parts[i] = strconv.FormatFloat(shape, 'f', 1, 64)
	}
	return strings.Join(parts, "|")
}

// useCustomAfftdnProfile reports whether the measured room-tone spectrum is
// trustworthy enough to drive afftdn's custom noise model: a NoiseProfile with
// all bands measured, a wide enough speech/noise gap, and a flat enough (noise-
// like) room-tone spectrum.
func useCustomAfftdnProfile(measurements *AudioMeasurements) bool {
	profile := measurements.Regions.NoiseProfile
	if profile == nil || !profile.BandsMeasured {
		return false
	}
	if measurements.Regions.GateSeparationDB < afftdnCustomMinSeparationDB {
		return false
	}
	return profile.Spectral.Flatness >= afftdnCustomMinFlatness
}

// tuneNoiseReduction adapts the afftdn FFT denoise tail to Pass 1 measurements.
// Two behaviours: drop afftdn on voice-activated captures (the gated capture floor
// is already 0 dB silence, so spectral denoise has nothing useful to do and only
// risks warble), and otherwise pin afftdn's nf to the measured noise floor with
// track_noise off so it holds a static floor rather than adapting frame to frame.
// anlmdn and the fixed afftdn nr are untouched.
func tuneNoiseReduction(config *EffectiveFilterConfig, diagnostics *AdaptiveDiagnostics, measurements *AudioMeasurements) {
	if config == nil || measurements == nil {
		return
	}

	if measurements.Noise.VoiceActivated {
		config.NoiseReduction.AfftdnEnabled = false
		diagnostics.AfftdnEnabled = false
		diagnostics.AfftdnDisableReason = "voice_activated"
		return
	}

	diagnostics.AfftdnEnabled = config.NoiseReduction.AfftdnEnabled

	// Guard: a zero floor means unmeasured. Leave the defaults (afftdn on,
	// track_noise on, nf unset) as a safe fallback.
	if measurements.Noise.Floor == 0 {
		return
	}

	floor := max(afftdnNoiseFloorMinDB, min(afftdnNoiseFloorMaxDB, measurements.Noise.Floor))
	config.NoiseReduction.AfftdnNoiseFloor = floor
	config.NoiseReduction.AfftdnTrackNoise = false
	diagnostics.AfftdnNoiseFloorDB = floor

	// Measured custom noise profile: when the room-tone band spectrum is
	// trustworthy, emit the measured spectral shape (nt=custom:bn) instead of
	// white. nf (the absolute level, set above) and nr (the depth) still stack on
	// top; bn carries only the shape. Otherwise the white path stands.
	config.NoiseReduction.AfftdnNoiseType = "w"
	if useCustomAfftdnProfile(measurements) {
		if bn := buildAfftdnBandNoise(measurements.Regions.NoiseProfile.BandNoise); bn != "" {
			config.NoiseReduction.AfftdnNoiseType = "custom"
			config.NoiseReduction.AfftdnBandNoise = bn
		}
	}
	diagnostics.AfftdnNoiseType = config.NoiseReduction.AfftdnNoiseType
}

// sanitizeConfig ensures no NaN or Inf values remain after adaptive tuning.
func sanitizeConfig(config *EffectiveFilterConfig) {
	sanitizeBiquadConfig(&config.RumbleHighPass, rumbleHPDefaultFreq)
	sanitizeBiquadConfig(&config.BandlimitLowPass, bandlimitLPFreq)
	sanitizeNoiseReductionConfig(&config.NoiseReduction)
	sanitizeSpeechGateConfig(&config.SpeechGate)
	sanitizeLevellingCompressorConfig(&config.LevellingCompressor)
	sanitizeDeesserConfig(&config.Deesser)
}

func sanitizeBiquadConfig(config *BiquadFilterConfig, defaultFreq float64) {
	config.Frequency = sanitizeFloat(config.Frequency, defaultFreq)
	config.Width = sanitizeFloat(config.Width, 0.707)
	config.Mix = sanitizeFloat(config.Mix, 1.0)
}

func sanitizeNoiseReductionConfig(config *NoiseReductionConfig) {
	defaults := defaultNoiseReductionConfig()
	config.Strength = sanitizeFloat(config.Strength, defaults.Strength)
	config.PatchSec = sanitizeFloat(config.PatchSec, defaults.PatchSec)
	config.ResearchSec = sanitizeFloat(config.ResearchSec, defaults.ResearchSec)
	config.Smooth = sanitizeFloat(config.Smooth, defaults.Smooth)
	config.AfftdnNoiseReduction = sanitizeFloat(config.AfftdnNoiseReduction, defaults.AfftdnNoiseReduction)
	// AfftdnNoiseFloor must never carry NaN/Inf into the afftdn format string.
	// The default is the unset zero value, which omits nf=.
	config.AfftdnNoiseFloor = sanitizeFloat(config.AfftdnNoiseFloor, defaults.AfftdnNoiseFloor)
	// A "custom" noise type with no band shape would emit nt=custom with no bn,
	// which afftdn rejects; revert to white so the builder stays well-formed.
	if config.AfftdnNoiseType == "custom" && config.AfftdnBandNoise == "" {
		config.AfftdnNoiseType = "w"
	}
}

func sanitizeSpeechGateConfig(config *SpeechGateConfig) {
	defaults := defaultSpeechGateConfig()
	if math.IsNaN(config.Threshold) || math.IsInf(config.Threshold, 0) || config.Threshold <= 0 {
		config.Threshold = speechGateDefaultThreshold
	}
	config.Ratio = sanitizeFloat(config.Ratio, defaults.Ratio)
	config.Attack = sanitizeFloat(config.Attack, defaults.Attack)
	config.Release = sanitizeFloat(config.Release, defaults.Release)
	config.Range = sanitizeFloat(config.Range, defaults.Range)
	config.Knee = sanitizeFloat(config.Knee, defaults.Knee)
	config.Makeup = sanitizeFloat(config.Makeup, defaults.Makeup)
}

func sanitizeLevellingCompressorConfig(config *LevellingCompressorConfig) {
	defaults := defaultLevellingCompressorConfig()
	config.Ratio = sanitizeFloat(config.Ratio, defaults.Ratio)
	config.Threshold = sanitizeFloat(config.Threshold, defaultLevellingCompressorThreshold)
	config.Attack = sanitizeFloat(config.Attack, defaults.Attack)
	config.Release = sanitizeFloat(config.Release, defaults.Release)
	config.Makeup = sanitizeFloat(config.Makeup, defaults.Makeup)
	config.Knee = sanitizeFloat(config.Knee, defaults.Knee)
	config.Mix = sanitizeFloat(config.Mix, defaults.Mix)
}

func sanitizeDeesserConfig(config *DeesserConfig) {
	defaults := defaultDeesserConfig()
	config.Intensity = sanitizeFloat(config.Intensity, defaultDeessIntensity)
	config.Amount = sanitizeFloat(config.Amount, defaults.Amount)
	config.Frequency = sanitizeFloat(config.Frequency, defaults.Frequency)
}
