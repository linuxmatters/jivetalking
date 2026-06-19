package processor

import "math"

const (
	// ==========================================================================
	// Levelling compressor parameters
	// ==========================================================================
	// A gentle, programme-dependent compressor approximated with FFmpeg's
	// acompressor in RMS detection mode, using fixed gentle settings (3:1 ratio,
	// 10ms attack, 200ms release, soft 4.0 knee, 100% wet, unity makeup). loudnorm
	// downstream handles all level adjustment, so makeup stays at unity.
	//
	// The only genuine adaptation is the threshold, anchored to the speech-region
	// RMS so the compressor engages on programme material rather than on a
	// peak/silence-diluted full-file reference.
	// ==========================================================================

	// Threshold offset above speech RMS.
	// A corpus threshold-offset sweep picked +9 dB as the
	// least-treatment-but-genuine offset: it gives consistent ~2.5-4.4 dB
	// gain-reduction depth / output crest in the 8-12 dB sweet spot across the
	// corpus's level spread.
	levellingCompressorThresholdSpeechOffsetDB = 9.0

	// Threshold clamp bounds (dBFS): a sane operating range for the threshold.
	levellingCompressorThresholdMin = -45.0
	levellingCompressorThresholdMax = -6.0

	// Fallback headroom below peak when no SpeechProfile is available.
	levellingCompressorFallbackPeakHeadroomDB = 20.0

	defaultLevellingCompressorThreshold = -18.0 // Moderate threshold (used when peak is NaN/Inf)

	// Fixed gentle levelling parameters. loudnorm handles level adjustment, so
	// makeup stays at unity. These values match defaultLevellingCompressorConfig()
	// and are enforced here so the effective config is independent of any incoming
	// base override.
	levellingCompressorFixedRatio   = 3.0
	levellingCompressorFixedAttack  = 10.0
	levellingCompressorFixedRelease = 200.0
	levellingCompressorFixedKnee    = 4.0
	levellingCompressorFixedMix     = 1.0
	levellingCompressorFixedMakeup  = 0.0
)

// tuneLevellingCompressor applies fixed gentle levelling compression with a single
// genuine adaptation: the threshold.
//
// Ratio, attack, release, knee, mix and makeup are fixed in
// defaultLevellingCompressorConfig() and left untouched here. The threshold is
// anchored to speech-region RMS when a SpeechProfile exists, otherwise it falls
// back to a peak-relative estimate.
func tuneLevellingCompressor(config *EffectiveFilterConfig, measurements *AudioMeasurements) {
	config.LevellingCompressor.Ratio = levellingCompressorFixedRatio
	config.LevellingCompressor.Attack = levellingCompressorFixedAttack
	config.LevellingCompressor.Release = levellingCompressorFixedRelease
	config.LevellingCompressor.Knee = levellingCompressorFixedKnee
	config.LevellingCompressor.Mix = levellingCompressorFixedMix
	config.LevellingCompressor.Makeup = levellingCompressorFixedMakeup
	tuneLevellingCompressorThreshold(config, measurements)
}

// tuneLevellingCompressorThreshold sets the compressor threshold.
//
// With a SpeechProfile, threshold = speech RMS + offset, where the speech RMS is
// first floored at the full-file overall RMS (raises only) so an anomalously
// quiet speech election cannot drag the threshold too low. The compressor then
// engages on programme material at a consistent depth regardless of the file's
// peak/silence distribution. Without one (full-file metrics unreliable), it
// falls back to the legacy peak-relative estimate (peak - 20 dB). Both paths are
// clamped to [levellingCompressorThresholdMin, levellingCompressorThresholdMax].
func tuneLevellingCompressorThreshold(config *EffectiveFilterConfig, measurements *AudioMeasurements) {
	var threshold float64

	if measurements.Regions.SpeechProfile != nil {
		effectiveSpeechRMS := measurements.Regions.SpeechProfile.RMSLevel
		// A representative speech region cannot be quieter than the silence-diluted
		// full-file RMS; if the election is anomalously quiet (a clean but quiet
		// window), floor it at the whole-file level so the threshold is not dragged
		// too low. Same dBFS axis as the threshold; raises only, never lowers.
		// Guard against unmeasured astats: when astats is absent, Dynamics.RMSLevel
		// stays at the 0.0 zero value (the codebase's unmeasured-RMS sentinel, cf.
		// assignInputNoiseFloor), which is not NaN/Inf but would floor the speech RMS
		// up to 0 dBFS and pin the threshold to its ceiling. Any real dBFS level is
		// negative, so require a finite, sub-zero level before applying the floor.
		fullFileRMS := measurements.Dynamics.RMSLevel
		if fullFileRMS < 0 && !math.IsInf(fullFileRMS, -1) {
			effectiveSpeechRMS = max(effectiveSpeechRMS, fullFileRMS)
		}
		threshold = effectiveSpeechRMS + levellingCompressorThresholdSpeechOffsetDB
	} else {
		if math.IsNaN(measurements.Dynamics.PeakLevel) || math.IsInf(measurements.Dynamics.PeakLevel, 0) {
			config.LevellingCompressor.Threshold = defaultLevellingCompressorThreshold
			return
		}
		threshold = measurements.Dynamics.PeakLevel - levellingCompressorFallbackPeakHeadroomDB
	}

	config.LevellingCompressor.Threshold = max(levellingCompressorThresholdMin, min(threshold, levellingCompressorThresholdMax))
}
