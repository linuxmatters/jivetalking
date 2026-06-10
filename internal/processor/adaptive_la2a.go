package processor

import "math"

const (
	// ==========================================================================
	// LA-2A-Inspired Compression Parameters
	// ==========================================================================
	// The Teletronix LA-2A is an optical tube compressor renowned for its gentle,
	// program-dependent character. We approximate it with FFmpeg's acompressor in
	// RMS detection mode, using fixed gentle settings (3:1 ratio, 10ms attack,
	// 200ms release, soft 4.0 knee, 100% wet, unity makeup). loudnorm downstream
	// handles all level adjustment, so makeup stays at unity.
	//
	// The only genuine adaptation is the threshold, anchored to the speech-region
	// RMS so the compressor engages on programme material rather than on a
	// peak/silence-diluted full-file reference.
	// ==========================================================================

	// LA-2A threshold offset above speech RMS.
	// The sweep (.bench/la2a-threshold-sweep/) picked +9 dB as the
	// least-treatment-but-genuine offset: it gives consistent ~2.5-4.4 dB
	// gain-reduction depth / output crest in the 8-12 dB sweet spot across the
	// corpus's level spread.
	la2aThresholdSpeechOffsetDB = 9.0

	// LA2AThresholdSpeechOffsetDB exposes the speech-RMS threshold offset for reporting.
	LA2AThresholdSpeechOffsetDB = la2aThresholdSpeechOffsetDB

	// Threshold clamp bounds (dBFS). Preserve the prior sane range.
	la2aThresholdMin = -45.0
	la2aThresholdMax = -6.0

	// Fallback headroom below peak when no SpeechProfile is available.
	la2aFallbackPeakHeadroomDB = 20.0

	defaultLA2AThreshold = -18.0 // Moderate threshold (used when peak is NaN/Inf)

	// Fixed gentle LA-2A parameters. loudnorm handles level adjustment, so makeup
	// stays at unity. These values match defaultLA2AConfig() and are enforced here
	// so the effective config is independent of any incoming base override.
	la2aFixedRatio   = 3.0
	la2aFixedAttack  = 10.0
	la2aFixedRelease = 200.0
	la2aFixedKnee    = 4.0
	la2aFixedMix     = 1.0
	la2aFixedMakeup  = 0.0
)

// tuneLA2ACompressor applies fixed gentle LA-2A style compression with a single
// genuine adaptation: the threshold.
//
// Ratio, attack, release, knee, mix and makeup are fixed in defaultLA2AConfig()
// and left untouched here. The threshold is anchored to speech-region RMS when a
// SpeechProfile exists, otherwise it falls back to a peak-relative estimate.
func tuneLA2ACompressor(config *EffectiveFilterConfig, _ *AdaptiveDiagnostics, measurements *AudioMeasurements, _ debugLogger) {
	config.LA2A.Ratio = la2aFixedRatio
	config.LA2A.Attack = la2aFixedAttack
	config.LA2A.Release = la2aFixedRelease
	config.LA2A.Knee = la2aFixedKnee
	config.LA2A.Mix = la2aFixedMix
	config.LA2A.Makeup = la2aFixedMakeup
	tuneLA2AThreshold(config, measurements)
}

// tuneLA2AThreshold sets the compressor threshold.
//
// With a SpeechProfile, threshold = speech RMS + offset, so the compressor
// engages on programme material at a consistent depth regardless of the file's
// peak/silence distribution. Without one (full-file metrics unreliable), it
// falls back to the legacy peak-relative estimate (peak - 20 dB). Both paths are
// clamped to [la2aThresholdMin, la2aThresholdMax].
func tuneLA2AThreshold(config *EffectiveFilterConfig, measurements *AudioMeasurements) {
	var threshold float64

	if measurements.SpeechProfile != nil {
		threshold = measurements.SpeechProfile.RMSLevel + la2aThresholdSpeechOffsetDB
	} else {
		if math.IsNaN(measurements.PeakLevel) || math.IsInf(measurements.PeakLevel, 0) {
			config.LA2A.Threshold = defaultLA2AThreshold
			return
		}
		threshold = measurements.PeakLevel - la2aFallbackPeakHeadroomDB
	}

	config.LA2A.Threshold = max(la2aThresholdMin, min(threshold, la2aThresholdMax))
}
