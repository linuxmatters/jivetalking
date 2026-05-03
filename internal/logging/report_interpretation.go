package logging

// ============================================================================
// Spectral Characteristic Interpretation Functions
// ============================================================================
// These functions interpret spectral measurements and return human-readable
// descriptions of audio characteristics. Based on MATLAB Audio Toolbox
// documentation and standard audio analysis conventions.

// interpretCentroid describes spectral "brightness" based on centre of gravity.
// Reference: Grey & Gordon (1978) JASA; Peeters (2003) CUIDADO; librosa.
//
// Centroid is the "centre of gravity" of the spectrum - where spectral energy is concentrated.
//
// Reference values for speech:
// - Male voiced speech: 500-2500 Hz
// - Female voiced speech: 800-3500 Hz
// - Unvoiced consonants: 3000-8000+ Hz
//
// Higher centroid indicates brighter voice; useful for de-esser tuning.
func interpretCentroid(hz float64) string {
	switch {
	case hz < 500:
		return "dark, muffled"
	case hz < 1500:
		return "warm, present"
	case hz < 2500:
		return "forward, clear"
	case hz < 4000:
		return "bright, articulate"
	case hz < 6000:
		return "very bright, sibilant"
	default:
		return "extremely bright, fricatives or HF noise"
	}
}

// interpretSpread describes the spectral bandwidth around the centroid.
// Spread is the standard deviation of the spectrum around the centroid.
// It represents "instantaneous bandwidth" and indicates tonal dominance.
//
// Pure vowels show narrow spread; consonants and noise show wide spread.
// Low spread indicates tone dominance; high spread indicates broadband content.
func interpretSpread(hz float64) string {
	switch {
	case hz < 800:
		return "narrow, clean voiced"
	case hz < 2000:
		return "moderate, natural speech"
	case hz < 3500:
		return "wide, mixed voiced/unvoiced"
	default:
		return "very wide, broadband"
	}
}

// interpretSkewness describes the spectral distribution asymmetry.
// Positive skewness: energy concentrated below centroid (bass-heavy with HF tail).
// Negative skewness: energy concentrated above centroid (HF concentrated, sibilant-like).
//
// Voiced speech typically shows positive skewness (0.5-2.0); fricatives may show negative.
func interpretSkewness(skew float64) string {
	switch {
	case skew < -0.5:
		return "HF emphasis, fricatives/sibilants"
	case skew < 0.5:
		return "symmetric distribution"
	case skew < 2.5:
		return "LF emphasis with HF tail (typical voice)"
	default:
		return "very strong LF bias"
	}
}

// interpretKurtosis describes the spectral peakiness.
// Kurtosis measures how peaked vs flat the spectrum is; indicates harmonic clarity vs noise.
// Higher values: peaked/tonal spectrum with dominant frequencies.
// Lower values: flatter spectrum, more noise-like.
// Reference: Gaussian distribution has kurtosis=3.
//
// Healthy voiced speech typically 5-10; pathological or noisy voice trends toward 3.
func interpretKurtosis(kurt float64) string {
	switch {
	case kurt < 2.0:
		return "platykurtic, noise-dominant"
	case kurt < 3.0:
		return "slightly platykurtic, noisy/fricative"
	case kurt < 3.5:
		return "mesokurtic (Gaussian reference)"
	case kurt < 5.0:
		return "moderately leptokurtic, mixed"
	case kurt < 10.0:
		return "leptokurtic, clear harmonics"
	default:
		return "highly leptokurtic, excellent harmonics"
	}
}

// interpretEntropy describes spectral randomness/order.
// FFmpeg aspectralstats outputs normalised entropy (divided by log(size)).
// Values range 0-1 where 0=pure tone, 1=white noise.
// Reference: Misra et al. (2004) ICASSP; Essentia Entropy algorithm.
func interpretEntropy(entropy float64) string {
	switch {
	case entropy < 0.15:
		return "highly ordered, pure tone/clean vowel"
	case entropy < 0.30:
		return "clean voiced speech"
	case entropy < 0.50:
		return "mixed voiced/unvoiced"
	case entropy < 0.70:
		return "disordered, fricatives"
	case entropy < 0.85:
		return "unvoiced consonants"
	default:
		return "noise-dominant"
	}
}

// interpretFlatness describes tonality vs noisiness (Wiener entropy).
// Ratio of geometric mean to arithmetic mean. 0=pure tone, 1=white noise.
// Reference: MPEG-7 AudioSpectralFlatness; Johnston (1988); Dubnov (2004).
//
// Clean voiced speech 0.1-0.3; breathy voice 0.3-0.5; fricatives 0.4-0.7.
func interpretFlatness(flatness float64) string {
	switch {
	case flatness < 0.1:
		return "pure tone, maximum tonality"
	case flatness < 0.25:
		return "very tonal, strong harmonics"
	case flatness < 0.4:
		return "tonal with some noise, clean voiced"
	case flatness < 0.6:
		return "mixed tonal and noise"
	default:
		return "noise-like, breathy/unvoiced"
	}
}

// interpretCrest describes the peak-to-average ratio of the spectrum.
// Crest factor = max(spectrum) / mean(spectrum), expressed as LINEAR RATIO (not dB).
// To convert to dB: crest_dB = 20 * log10(crest).
// Reference: Peeters (2003) CUIDADO project; Essentia Crest algorithm.
//
// Typical values:
//   - White noise: ~3-5 (peaks barely exceed mean)
//   - Moderate peaks: 5-15 (some structure)
//   - Speech range: 20-60 (clear harmonic structure)
//   - Dominant peaks: >60 (excellent harmonic clarity)
func interpretCrest(crest float64) string {
	switch {
	case crest < 5:
		return "flat spectrum, noise-like"
	case crest < 15:
		return "moderate peaks"
	case crest < 30:
		return "strong peaks"
	case crest < 60:
		return "very strong peaks (speech range)"
	default:
		return "dominant peaks, excellent harmonic clarity"
	}
}

// interpretFlux describes frame-to-frame spectral variation.
// Flux measures how much the spectrum changes between frames.
// Low flux: stable/sustained sound (held notes, steady speech).
// High flux: dynamic/changing sound (transients, varied speech).
//
// Vowels show low flux; plosives (p,t,k,b,d,g) show high flux spikes.
func interpretFlux(flux float64) string {
	switch {
	case flux < 0.001:
		return "very stable, sustained"
	case flux < 0.005:
		return "stable, continuous"
	case flux < 0.02:
		return "natural articulation"
	default:
		return "high variation, transients"
	}
}

// interpretDecrease describes low-frequency weighted spectral slope.
// FFmpeg computes: sum((mag[k] - mag[0]) / k) / sum(mag[k])
// Positive values indicate spectrum decreases from low to high frequencies (typical for speech).
// Negative values indicate rising spectrum (unusual, HF emphasis).
// Reference: Peeters (2003) CUIDADO project; Essentia Decrease algorithm.
func interpretDecrease(decrease float64) string {
	switch {
	case decrease < 0:
		return "strong bass emphasis"
	case decrease < 0.05:
		return "balanced, typical voice"
	case decrease < 0.10:
		return "moderate decrease, typical speech"
	default:
		return "strong HF content, bright"
	}
}

// interpretRolloff describes effective bandwidth via 85% energy threshold.
// Returns Hz below which 85% of spectral energy resides.
// Reference: Peeters (2003) CUIDADO; librosa spectral_rolloff.
func interpretRolloff(hz float64) string {
	switch {
	case hz < 2000:
		return "over-filtered"
	case hz < 4000:
		return "dark, LF-dominant"
	case hz < 6000:
		return "typical voiced speech"
	case hz < 8000:
		return "good articulation"
	case hz < 12000:
		return "bright, airy"
	default:
		return "very bright, check sibilance"
	}
}

// interpretSlope describes the overall spectral tilt (linear regression coefficient).
// Modal speech approximately -6 dB/octave; breathy voice steeper; pressed voice shallower.
func interpretSlope(slope float64) string {
	switch {
	case slope < -5e-04:
		return "very steep slope, dark/warm"
	case slope < -2e-04:
		return "steep slope, warm character"
	case slope < -5e-05:
		return "moderate slope, balanced"
	case slope < 0:
		return "shallow slope, bright/energetic"
	default:
		return "positive slope, very bright"
	}
}
