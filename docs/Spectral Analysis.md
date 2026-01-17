# Audio spectral analysis metrics for voice characterization

**These 13 spectral metrics form a complete toolkit for analyzing vocal timbre, quality, and characteristics.** Each metric captures a different aspect of the frequency spectrum—from where energy concentrates (centroid) to how peaked versus flat the distribution is (flatness, kurtosis) to how quickly the spectrum changes over time (flux). Understanding what each metric measures and what typical values indicate enables data-driven audio processing decisions for voice recordings.

The example values from your male voice recording reveal interesting characteristics: a bright spectral profile (centroid at **5785 Hz**), moderately high flatness (**0.619**), and strong spectral peaks (crest of **38.7**). This combination suggests either measurement during consonant-heavy speech segments, a naturally bright vocal timbre, or significant high-frequency content from room acoustics or recording characteristics.

## First-order statistics: spectral mean and variance

**Spectral mean** measures the average magnitude across all frequency bins in the spectrum. Unlike spectral centroid (which is frequency-weighted), this metric captures overall spectral energy level. Your value of **9.70e-06** indicates normalized amplitude representation typical for moderate-level speech. This metric is highly dependent on recording level, normalization method, and FFT parameters, making it most useful as a relative comparison tool rather than an absolute measure.

**Spectral variance** quantifies dispersion of magnitude values around the spectral mean. Your value of **2.92e-08** (squared units from variance calculation) indicates some spectral variability—energy is not uniformly distributed across frequencies. Higher variance typically correlates with clear harmonic structure where distinct peaks stand out from the spectral floor; lower variance suggests noise-like signals with uniform energy distribution. For voice, moderate variance indicates healthy harmonic content.

| Metric | Characterization for your values |
|--------|--------------------------------|
| Spectral Mean (9.70e-06) | Typical normalized amplitude for moderate speech level |
| Spectral Variance (2.92e-08) | Harmonic content present; spectrum shows meaningful variation |

## Centroid and spread reveal brightness and bandwidth

**Spectral centroid** represents the "center of gravity" of the spectrum—the frequency around which spectral energy balances. Calculated as the frequency-weighted sum normalized by total energy: μ₁ = Σ(fₖ × sₖ) / Σ(sₖ). This metric directly correlates with perceived brightness. Typical voiced male speech shows centroid values between **500–2500 Hz** for sustained vowels, while unvoiced consonants (fricatives like "s" and "f") push centroids to **3000–8000+ Hz**.

Your centroid of **5785 Hz** is notably high for sustained voiced speech, suggesting either measurement during consonant-heavy segments, a naturally bright vocal quality, or significant high-frequency content in the recording. For comparison:

- **Bass instruments**: 100–500 Hz
- **Voiced male speech**: 500–2500 Hz
- **Voiced female speech**: 800–3500 Hz
- **Unvoiced fricatives**: 3000–8000+ Hz

**Spectral spread** (second spectral moment) measures the standard deviation around the centroid—essentially the "instantaneous bandwidth." Your value of **5157 Hz** indicates broad spectral distribution. Pure tones show spread near zero; voiced speech typically ranges **500–2000 Hz**; noise-like signals exhibit very high spread. Wide spread often indicates noise components, unvoiced speech content, or recordings with broad-spectrum characteristics like room ambiance.

**Processing implications**: A high centroid with high spread suggests evaluating for excessive brightness. Consider gentle high-frequency cuts around 5–8 kHz if the voice sounds harsh. If thin, boost warmth at 200–400 Hz. Target male vocal presence at 3–5 kHz.

## Skewness and kurtosis describe spectral shape

**Spectral skewness** (third moment) measures asymmetry around the centroid. A value of zero indicates perfect symmetry; positive skewness means energy concentrates in lower frequencies with a tail extending toward higher frequencies; negative skewness indicates the opposite pattern.

Your value of **1.132** (moderately positive) indicates typical voiced male speech characteristics—strong energy around the fundamental frequency and lower harmonics, with a tail toward higher frequencies. This positive skewness distinguishes voiced speech from fricatives (which often show negative skewness due to high-frequency noise concentration). In phonetic analysis, skewness helps differentiate alveolar /s/ (negative skew) from palato-alveolar /ʃ/ (positive skew).

**Spectral kurtosis** (fourth moment) measures "peakedness" or how concentrated versus flat the spectral distribution is. The Gaussian reference value is **3**; values above 3 (leptokurtic) indicate peaked distributions with prominent spectral features; values below 3 (platykurtic) indicate flatter distributions.

Your kurtosis of **5.781** clearly indicates leptokurtic distribution—the spectrum has **distinct peaks rather than uniform energy spread**. This is excellent for voiced speech: clear harmonic structure, good periodicity, and low noise contamination. For comparison:

| Kurtosis value | Distribution type | Audio characteristics |
|----------------|-------------------|----------------------|
| < 3 | Platykurtic (flat) | Noise-dominant, fricatives |
| ≈ 3 | Mesokurtic (Gaussian) | White noise reference |
| 3–5 | Moderately leptokurtic | Mixed tonal and noise content |
| > 5 | Highly leptokurtic | Clear harmonics, good voice quality |

**Voice quality insight**: High kurtosis combined with positive skewness suggests healthy voice production with strong fundamental frequency presence and well-defined harmonics. Pathological voices or noisy recordings would show kurtosis trending toward 3 (flatter) as noise contaminates the spectrum.

## Information-theoretic metrics: entropy, flatness, and crest

**Spectral entropy** applies Shannon entropy to the frequency domain, measuring disorder in power distribution. FFmpeg's aspectralstats normalises entropy by dividing by log(N), producing values in the 0–1 range where 0 indicates a pure tone (all energy in one bin) and 1 indicates white noise (uniform distribution). Voiced speech typically shows entropy of **0.08–0.30**; mixed voiced/unvoiced content reaches **0.3–0.5**; fricatives and noise approach **0.7–1.0**.

Reference: Misra, H. et al. (2004). "Spectral entropy based feature for robust ASR." ICASSP'04.

**Spectral flatness** (Wiener entropy, tonality coefficient) is the ratio of geometric mean to arithmetic mean of the spectrum. It ranges from 0 (pure tone, all energy in one frequency) to 1 (white noise, uniform distribution). This MPEG-7 standard descriptor directly measures tonality versus noisiness.

| Signal type | Typical flatness | Interpretation |
|-------------|------------------|----------------|
| Pure tone | ~0.0 | Maximum tonality |
| Clean voiced speech | 0.05–0.25 | Strong harmonic peaks |
| Mixed speech content | 0.2–0.4 | Voiced with consonants |
| Unvoiced speech | 0.4–0.7 | Noise-like fricatives |
| White noise | ~1.0 | Flat distribution |

Your flatness of **0.619** is higher than typical voiced speech, suggesting either significant noise/aspiration content, measurement during unvoiced segments, or a breathy vocal quality. Clean voiced speech typically shows flatness of **0.1–0.3**.

**Spectral crest factor** is the inverse of flatness conceptually—the ratio of peak power to mean power. Higher values indicate more prominent spectral peaks (tonal content); lower values indicate flatter spectra (noise-like). Your crest of **38.744** is high, indicating significant spectral peaks despite the elevated flatness.

Note: Spectral crest is expressed as a **linear ratio** (not dB). To convert: crest_dB = 20 × log₁₀(crest). A crest of 100 equals 40 dB.

**Combined interpretation**: High flatness (0.62) with high crest (38.7) together suggest either averaging across varied phonemes (both tonal vowels creating peaks and noisy consonants creating flatness) or breathy voice quality where harmonic peaks coexist with aspiration noise.

## Temporal and shape metrics for dynamic analysis

**Spectral flux** measures how rapidly the spectrum changes between consecutive frames. Calculated as the Euclidean distance between successive spectra, it detects transients, onsets, and phoneme transitions. Your value of **0.001053** indicates a very stable spectrum—consistent with sustained voiced speech like a vowel rather than consonant transitions.

| Flux pattern | Indicates | Processing consideration |
|--------------|-----------|-------------------------|
| High/spiky | Transients, plosives (p,t,k,b,d,g) | May need transient shaping |
| Low/stable | Sustained vowels, steady phonation | Standard compression appropriate |
| Highly variable | Natural speech dynamics | Adaptive processing beneficial |

**Spectral slope** measures the overall tilt of the spectrum using linear regression across frequency bins. Voice signals naturally show negative slope because harmonic energy decreases at higher frequencies. The glottal source produces approximately **-12 dB/octave** slope; after lip radiation (+6 dB), modal speech typically shows **-6 dB/octave**.

Your slope of **-2.33e-05** (small negative coefficient) indicates the expected downward tilt from low to high frequencies. Interpretation depends on the computation method:

- **Steeper (more negative) slope**: Darker, warmer sound; breathy voice; relaxed vocal folds
- **Shallower (less negative) slope**: Brighter, sharper sound; pressed voice; potential strain
- **Near zero**: Very bright, possibly harsh

**Spectral decrease** measures the rate of spectral amplitude decline from the first frequency bin, with more weight given to lower frequencies. FFmpeg computes this as Σ((mag[k] - mag[0]) / k) / Σ(mag[k]). Positive values indicate the spectrum decreases from low to high frequencies (typical for speech); values near zero indicate a flat spectrum; negative values indicate rising spectrum (unusual). Empirical measurements show male podcast speech typically ranges **0.002–0.10**.

Reference: Peeters, G. (2003). CUIDADO project report, IRCAM.

**Spectral rolloff** identifies the frequency below which a specified percentage (typically 85% or 95%) of total spectral energy resides. This directly indicates high-frequency content and perceived brightness. Your rolloff of **11864 Hz** (at 95% energy threshold) is quite high for voiced speech, indicating significant high-frequency content.

| Rolloff value | Indicates | Voice characteristic |
|---------------|-----------|---------------------|
| < 3000 Hz | Dark, muffled | Heavy voiced content only |
| 3000–8000 Hz | Balanced | Normal articulation |
| > 8000 Hz | Bright, airy | Sibilance, fricatives, bright timbre |

Note: Rolloff values assume the **85% energy threshold** (FFmpeg and librosa default). Some tools use 95%, which produces higher values.

**Processing implications**: Your high rolloff suggests checking for sibilance. Male sibilance typically concentrates at 3–6 kHz; consider de-essing in this range if "s" and "t" sounds are harsh.

## Practical characterizations for your male voice recording

Based on the metric values provided, here are audio engineering characterizations:

| Metric | Value | Characterization |
|--------|-------|------------------|
| Spectral Mean | 9.70e-06 | Moderate normalized level; typical speech amplitude |
| Spectral Variance | 2.92e-08 | Good spectral variation; harmonic structure present |
| Spectral Centroid | 5785 Hz | **Bright**—energy concentrated higher than typical voiced speech; consonant presence or naturally bright timbre |
| Spectral Spread | 5157 Hz | **Wide bandwidth**—broad frequency content; possible noise floor or varied phoneme content |
| Spectral Skewness | 1.132 | **Lower-frequency emphasis**—energy concentrated in bass with high-frequency tail; typical male voiced speech pattern |
| Spectral Kurtosis | 5.781 | **Peaky spectrum**—clear harmonic structure; good voice quality indicators; low noise contamination |
| Spectral Entropy | 0.010568 | **Highly ordered** (if normalized) or requires recalibration if raw computation |
| Spectral Flatness | 0.619 | **Moderately noise-like**—higher than typical voiced speech; possible breathiness, aspiration, or unvoiced content |
| Spectral Crest | 38.744 | **Strong spectral peaks**—dominant frequencies present despite overall flatness |
| Spectral Flux | 0.001053 | **Stable**—sustained phonation; not transient content |
| Spectral Slope | -2.33e-05 | **Normal tilt**—balanced brightness; neither dark nor harsh |
| Spectral Decrease | -0.026370 | **Bass-forward**—energy concentrated in lower frequencies as expected for male voice |
| Spectral Rolloff | 11864 Hz | **Bright extension**—significant high-frequency content; potential sibilance presence |

## Recommended filter tuning approach based on these metrics

The metric profile suggests a male voice with **bright characteristics** (high centroid, high rolloff) combined with **good harmonic structure** (high kurtosis, positive skewness) but **elevated noise-like components** (high flatness). Consider:

- **De-essing**: Target 3–6 kHz range given the high rolloff and centroid; the spectral profile suggests potential sibilance
- **High-frequency management**: Gentle shelf reduction above 8 kHz if brightness is excessive; alternatively, if the high-frequency content is desirable air, preserve it
- **Low-mid presence**: The positive skewness and spectral decrease confirm good low-frequency foundation; preserve 200–400 Hz warmth
- **Clarity enhancement**: If masking occurs, slight cut at 300–400 Hz combined with presence boost at 3–5 kHz
- **Noise reduction**: The elevated flatness may indicate room tone or preamp noise; consider gentle broadband noise reduction if this flatness persists across silent segments

## References

The spectral metrics documented here follow established definitions from:

- **Peeters, G. (2003).** "A large set of audio features for sound description." CUIDADO I.S.T. Project Report, IRCAM. [Foundational reference for spectral descriptors]
- **MPEG-7 ISO/IEC 15938-4.** AudioSpectralFlatness standard descriptor.
- **Essentia Library.** MTG, Universitat Pompeu Fabra. https://essentia.upf.edu
- **librosa.** McFee et al. https://librosa.org
- **Misra, H. et al. (2004).** "Spectral entropy based feature for robust ASR." ICASSP'04.

## Conclusion

These 13 spectral metrics provide complementary views of voice characteristics. The **centroid and rolloff** indicate perceived brightness; **skewness and slope** reveal tonal balance; **kurtosis, flatness, and crest** distinguish harmonic clarity from noise content; **flux** captures temporal dynamics. For your male voice recording, the combination of high centroid/rolloff with high kurtosis suggests a bright but well-defined voice—the harmonics are clear (high kurtosis) even though energy extends into higher frequencies (high centroid/rolloff). The elevated flatness warrants investigation: either the voice has breathy characteristics, the measurement includes unvoiced segments, or there's background noise worth addressing in processing.
