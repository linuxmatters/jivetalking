package processor

// preferSpeechMetric returns speech-specific measurement if available,
// otherwise falls back to full-file measurement.
func preferSpeechMetric(fullFile, speechProfile float64) float64 {
	if speechProfile > 0 {
		return speechProfile
	}
	return fullFile
}

// preferSpeechMetricSigned returns speech-specific measurement if speech data
// exists, otherwise falls back to full-file measurement. Unlike preferSpeechMetric,
// this variant uses an explicit flag rather than checking value > 0, making it
// safe for metrics that can legitimately be zero or negative (e.g. SpectralDecrease,
// SpectralSkewness).
func preferSpeechMetricSigned(fullFile, speechValue float64, hasSpeech bool) float64 {
	if hasSpeech {
		return speechValue
	}
	return fullFile
}
