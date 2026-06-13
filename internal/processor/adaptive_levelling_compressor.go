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
	// The sweep (.bench/la2a-threshold-sweep/) picked +9 dB as the
	// least-treatment-but-genuine offset: it gives consistent ~2.5-4.4 dB
	// gain-reduction depth / output crest in the 8-12 dB sweet spot across the
	// corpus's level spread.
	levellingCompressorThresholdSpeechOffsetDB = 9.0

	// LevellingCompressorThresholdSpeechOffsetDB exposes the speech-RMS threshold offset for reporting.
	LevellingCompressorThresholdSpeechOffsetDB = levellingCompressorThresholdSpeechOffsetDB

	// Threshold clamp bounds (dBFS). Preserve the prior sane range.
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
func tuneLevellingCompressor(config *EffectiveFilterConfig, _ *AdaptiveDiagnostics, measurements *AudioMeasurements, _ debugLogger) {
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
// With a SpeechProfile, threshold = speech RMS + offset, so the compressor
// engages on programme material at a consistent depth regardless of the file's
// peak/silence distribution. Without one (full-file metrics unreliable), it
// falls back to the legacy peak-relative estimate (peak - 20 dB). Both paths are
// clamped to [levellingCompressorThresholdMin, levellingCompressorThresholdMax].
func tuneLevellingCompressorThreshold(config *EffectiveFilterConfig, measurements *AudioMeasurements) {
	var threshold float64

	if measurements.Regions.SpeechProfile != nil {
		threshold = measurements.Regions.SpeechProfile.RMSLevel + levellingCompressorThresholdSpeechOffsetDB
	} else {
		if math.IsNaN(measurements.Dynamics.PeakLevel) || math.IsInf(measurements.Dynamics.PeakLevel, 0) {
			config.LevellingCompressor.Threshold = defaultLevellingCompressorThreshold
			return
		}
		threshold = measurements.Dynamics.PeakLevel - levellingCompressorFallbackPeakHeadroomDB
	}

	config.LevellingCompressor.Threshold = max(levellingCompressorThresholdMin, min(threshold, levellingCompressorThresholdMax))
}
