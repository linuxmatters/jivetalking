// Package processor handles audio analysis and processing
package processor

import (
	"math"
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
