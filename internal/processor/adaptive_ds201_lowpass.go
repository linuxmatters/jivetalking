package processor

const (
	// DS201 Low-Pass filter tuning
	ds201LPDefaultFreq = 16000.0 // Hz - default cutoff (preserves all audible content)

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
func tuneDS201LowPass(config *EffectiveFilterConfig, diagnostics *AdaptiveDiagnostics, m *AudioMeasurements) {
	// Start disabled - only enable when we detect clear benefit
	config.DS201LowPass.Enabled = false
	config.DS201LowPass.Frequency = ds201LPDefaultFreq
	if diagnostics == nil {
		diagnostics = &AdaptiveDiagnostics{}
	}

	// Detect content type
	contentType := detectContentType(m)
	diagnostics.DS201LPContentType = contentType

	// Calculate rolloff/centroid ratio for logging
	rolloff := m.Spectral.Rolloff
	centroid := m.Spectral.Centroid
	if m.SpeechProfile != nil {
		rolloff = preferSpeechMetric(rolloff, m.SpeechProfile.Spectral.Rolloff)
		centroid = preferSpeechMetric(centroid, m.SpeechProfile.Spectral.Centroid)
	}
	if centroid > 0 {
		diagnostics.DS201LPRolloffRatio = rolloff / centroid
	}

	switch contentType {
	case ContentMusic:
		// Music: preserve full spectrum
		diagnostics.DS201LPReason = "music content detected"
		return

	case ContentMixed:
		// Mixed content: disable to be safe
		diagnostics.DS201LPReason = "mixed content, conservative"
		return

	case ContentSpeech:
		// Speech: check for HF noise indicators
		tuneDS201LowPassForSpeech(config, diagnostics, m)
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
func tuneDS201LowPassForSpeech(config *EffectiveFilterConfig, diagnostics *AdaptiveDiagnostics, m *AudioMeasurements) {
	// Default: DISABLED per spec — only activate when measurements indicate benefit
	config.DS201LowPass.Enabled = false
	config.DS201LowPass.Frequency = ds201LPDefaultFreq
	config.DS201LowPass.Poles = 1 // 6dB/oct - gentle
	config.DS201LowPass.Mix = 1.0
	diagnostics.DS201LPReason = "no HF issues detected"

	// Prefer speech-specific spectral metrics when available.
	// Full-file averages are diluted by silence in multi-track recordings.
	rolloff := m.Spectral.Rolloff
	centroid := m.Spectral.Centroid
	if m.SpeechProfile != nil {
		rolloff = preferSpeechMetric(rolloff, m.SpeechProfile.Spectral.Rolloff)
		centroid = preferSpeechMetric(centroid, m.SpeechProfile.Spectral.Centroid)
	}

	// Condition 1: Voice already dark (rolloff < 8kHz)
	// No benefit from lowpass — would only remove wanted content
	if rolloff < lpRolloffDarkVoice {
		diagnostics.DS201LPReason = "voice already dark (rolloff < 8kHz)"
		return
	}

	// Condition 2: High rolloff (> 14kHz) — ultrasonic content present
	// Enable at rolloff + 2kHz to clean up ultrasonics while preserving audible content
	if rolloff > lpRolloffEnableThreshold {
		cutoff := rolloff + lpRolloffHeadroom
		// Clamp to reasonable maximum
		if cutoff > 20000 {
			cutoff = 20000
		}
		config.DS201LowPass.Enabled = true
		config.DS201LowPass.Frequency = cutoff
		config.DS201LowPass.Poles = 1 // 6dB/oct - very gentle for ultrasonic cleanup
		config.DS201LowPass.Mix = 1.0
		diagnostics.DS201LPReason = "ultrasonic cleanup (rolloff > 14kHz)"
		return
	}

	// Condition 3: High ZCR with low centroid (HF noise, not sibilance)
	// Sibilance has high ZCR AND high centroid; noise has high ZCR with low centroid
	if m.ZeroCrossingsRate > lpZCRHigh && centroid < lpZCRCentroidThreshold {
		config.DS201LowPass.Enabled = true
		config.DS201LowPass.Frequency = lpZCRCutoff
		config.DS201LowPass.Poles = 1 // 6dB/oct - gentle
		config.DS201LowPass.Mix = 0.8 // Blend with dry for transparency
		diagnostics.DS201LPReason = "high ZCR with low centroid (HF noise)"
		return
	}

	// No triggers fired — keep disabled
}
