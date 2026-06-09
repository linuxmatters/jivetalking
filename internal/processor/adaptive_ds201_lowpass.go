package processor

const (
	// DS201 Low-Pass filter tuning
	ds201LPBandLimitFreq = 20500.0 // Hz - unconditional band-limit ceiling (above audibility; gives a consistent bandwidth into downstream AAC/Opus/MP3 encoders)
)

// tuneDS201LowPass sets the DS201-inspired low-pass filter to an unconditional
// 20.5 kHz band-limit for all content.
//
// The low-pass sits in circuit at a fixed 20.5 kHz ceiling (12 dB/oct) regardless
// of content or measurements. 20.5 kHz is at the top of human hearing, so the
// band-limit is audibly transparent on voice, music, and singing; it only removes
// inaudible ultrasonics that the downstream lossy encoders discard anyway. There is
// no content detection and no adaptive tuning.
//
// Sample-rate assumption: a 20.5 kHz cutoff needs the working Nyquist above 20.5 kHz
// (source rate >= ~41 kHz). Podcast sources are 44.1/48 kHz, so the band-limit is
// always valid in practice. The source rate is not carried on AudioMeasurements here,
// so no per-file Nyquist guard is applied.
func tuneDS201LowPass(config *EffectiveFilterConfig, diagnostics *AdaptiveDiagnostics, _ *AudioMeasurements) {
	config.DS201LowPass.Enabled = true
	config.DS201LowPass.Frequency = ds201LPBandLimitFreq
	config.DS201LowPass.Poles = 2 // 12dB/oct - a real ceiling that attenuates before Nyquist
	config.DS201LowPass.Mix = 1.0
	if diagnostics == nil {
		diagnostics = &AdaptiveDiagnostics{}
	}
	diagnostics.DS201LPReason = "20.5 kHz band-limit (always on)"
}
