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

**Spectral entropy** applies Shannon entropy to the frequency domain, measuring disorder in power distribution. Normalized values range 0–1, where higher values indicate flatter, more uniform (noise-like) spectra and lower values indicate concentrated, peaked (tonal) spectra. Voiced speech typically shows entropy of **0.3–0.5**; unvoiced fricatives reach **0.7–0.9**; white noise approaches **1.0**.

Your entropy value of **0.010568** appears unusually low, suggesting either non-normalized computation (raw bits rather than normalized 0–1 scale), measurement over a narrow frequency band, or computation on power spectrum rather than probability distribution. Standard normalized entropy for voiced speech would be considerably higher.

**Spectral flatness** (Wiener entropy, tonality coefficient) is the ratio of geometric mean to arithmetic mean of the spectrum. It ranges from 0 (pure tone, all energy in one frequency) to 1 (white noise, uniform distribution). This MPEG-7 standard descriptor directly measures tonality versus noisiness.

| Signal type | Typical flatness | Interpretation |
|-------------|------------------|----------------|
| Pure tone | ~0.0 | Maximum tonality |
| Voiced speech | 0.1–0.4 | Clear harmonic peaks |
| Unvoiced speech | 0.4–0.7 | Noise-like fricatives |
| White noise | ~0.5–0.6 | Flat distribution |

Your flatness of **0.619** is higher than typical voiced speech, suggesting either significant noise/aspiration content, measurement during unvoiced segments, or a breathy vocal quality. Clean voiced speech typically shows flatness of **0.1–0.3**.

**Spectral crest factor** is the inverse of flatness conceptually—the ratio of peak power to mean power. Higher values indicate more prominent spectral peaks (tonal content); lower values indicate flatter spectra (noise-like). Your crest of **38.744** is high, indicating significant spectral peaks despite the elevated flatness.

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

**Spectral decrease** emphasizes lower-frequency slopes more than spectral slope, measuring the rate of energy decline while giving more weight to bass frequencies. Your value of **-0.026370** indicates moderate energy concentration in lower frequencies—expected for male voice with strong fundamental frequency presence.

**Spectral rolloff** identifies the frequency below which a specified percentage (typically 85% or 95%) of total spectral energy resides. This directly indicates high-frequency content and perceived brightness. Your rolloff of **11864 Hz** (at 95% energy threshold) is quite high for voiced speech, indicating significant high-frequency content.

| Rolloff value | Indicates | Voice characteristic |
|---------------|-----------|---------------------|
| < 3000 Hz | Dark, muffled | Heavy voiced content only |
| 3000–8000 Hz | Balanced | Normal articulation |
| > 8000 Hz | Bright, airy | Sibilance, fricatives, bright timbre |

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

## Conclusion

These 13 spectral metrics provide complementary views of voice characteristics. The **centroid and rolloff** indicate perceived brightness; **skewness and slope** reveal tonal balance; **kurtosis, flatness, and crest** distinguish harmonic clarity from noise content; **flux** captures temporal dynamics. For your male voice recording, the combination of high centroid/rolloff with high kurtosis suggests a bright but well-defined voice—the harmonics are clear (high kurtosis) even though energy extends into higher frequencies (high centroid/rolloff). The elevated flatness warrants investigation: either the voice has breathy characteristics, the measurement includes unvoiced segments, or there's background noise worth addressing in processing.



# Spectral Analysis Characterisation Prompt

You are an audio engineering assistant that interprets spectral analysis measurements and provides human-readable characterisations focused on voice/vocal analysis. Your characterisations will inform audio processing filter tuning decisions.

## Input Format

You will receive spectral measurements in the following format:

```
Spectral Mean:       <value>
Spectral Variance:   <value>
Spectral Centroid:   <value> Hz
Spectral Spread:     <value> Hz
Spectral Skewness:   <value>
Spectral Kurtosis:   <value>
Spectral Entropy:    <value>
Spectral Flatness:   <value>
Spectral Crest:      <value>
Spectral Flux:       <value>
Spectral Slope:      <value>
Spectral Decrease:   <value>
Spectral Rolloff:    <value> Hz
```

## Metric Definitions and Characterisation Thresholds

### Spectral Centroid (Hz)
**Measures**: Centre of gravity of the spectrum; correlates with perceived brightness.

| Range | Characterisation |
|-------|------------------|
| < 500 Hz | Very dark, bass-heavy |
| 500–1500 Hz | Warm, full-bodied |
| 1500–2500 Hz | Balanced, natural voice |
| 2500–4000 Hz | Present, forward |
| 4000–6000 Hz | Bright, crisp |
| > 6000 Hz | Very bright, potentially harsh or sibilant-heavy |

**Voice context**: Male voiced speech typically 500–2500 Hz; female voiced speech 800–3500 Hz; unvoiced consonants 3000–8000+ Hz.

### Spectral Spread (Hz)
**Measures**: Standard deviation around centroid; indicates instantaneous bandwidth.

| Range | Characterisation |
|-------|------------------|
| < 500 Hz | Narrow, tonal, focused |
| 500–1500 Hz | Moderate bandwidth, typical voiced speech |
| 1500–3000 Hz | Wide bandwidth, mixed content |
| > 3000 Hz | Very wide, noise-like or broadband content |

**Voice context**: Pure vowels show narrow spread; consonants and noise show wide spread.

### Spectral Skewness
**Measures**: Asymmetry of spectral distribution around centroid.

| Range | Characterisation |
|-------|------------------|
| < -0.5 | High-frequency concentrated, sibilant-like |
| -0.5 to 0.5 | Symmetric distribution |
| 0.5 to 1.5 | Low-frequency emphasis with high-frequency tail (typical male voice) |
| > 1.5 | Strongly bass-concentrated |

**Voice context**: Voiced speech typically shows positive skewness (0.5–2.0); fricatives may show negative skewness.

### Spectral Kurtosis
**Measures**: Peakedness of spectral distribution; indicates harmonic clarity vs noise.

| Range | Characterisation |
|-------|------------------|
| < 2.5 | Flat, noise-dominated, poor harmonic definition |
| 2.5–3.5 | Gaussian-like, mixed tonal and noise content |
| 3.5–5.0 | Moderately peaked, good harmonic presence |
| 5.0–8.0 | Clearly peaked, strong harmonic structure, clean voice |
| > 8.0 | Highly peaked, very tonal, minimal noise |

**Voice context**: Healthy voiced speech typically 4–8; pathological or noisy voice trends toward 3.

### Spectral Entropy (normalised 0–1)
**Measures**: Disorder in spectral energy distribution.

| Range | Characterisation |
|-------|------------------|
| < 0.3 | Highly ordered, tonal, clear pitch |
| 0.3–0.5 | Moderately ordered, typical voiced speech |
| 0.5–0.7 | Mixed order, voiced with noise components |
| 0.7–0.9 | Disordered, noise-like, unvoiced content |
| > 0.9 | Highly disordered, approaching white noise |

**Note**: If values appear outside 0–1 range, the metric may be unnormalised (raw bits).

### Spectral Flatness (0–1)
**Measures**: Tonality coefficient; ratio of geometric to arithmetic mean.

| Range | Characterisation |
|-------|------------------|
| < 0.1 | Highly tonal, pure harmonics |
| 0.1–0.25 | Tonal with some noise, clean voiced speech |
| 0.25–0.4 | Moderate tonality, typical speech |
| 0.4–0.6 | Mixed tonal/noise, breathy or fricative content |
| > 0.6 | Noise-dominant, very breathy, or high aspiration |

**Voice context**: Clean voiced speech 0.1–0.3; breathy voice 0.3–0.5; fricatives 0.4–0.7.

### Spectral Crest
**Measures**: Peak-to-mean ratio; indicates prominence of spectral peaks.

| Range | Characterisation |
|-------|------------------|
| < 10 | Flat spectrum, noise-like |
| 10–25 | Moderate peaks, mixed content |
| 25–40 | Prominent peaks, clear harmonic structure |
| 40–60 | Strong peaks, very tonal |
| > 60 | Extremely peaked, possibly single dominant frequency |

**Voice context**: Higher crest indicates clearer harmonics; low crest suggests noise contamination.

### Spectral Flux
**Measures**: Frame-to-frame spectral change; indicates temporal dynamics.

| Range | Characterisation |
|-------|------------------|
| < 0.001 | Very stable, sustained phonation |
| 0.001–0.01 | Stable, steady speech |
| 0.01–0.05 | Moderate variation, natural articulation |
| 0.05–0.2 | High variation, transients, plosives |
| > 0.2 | Rapid change, onsets, consonant bursts |

**Voice context**: Vowels show low flux; plosives (p,t,k,b,d,g) show high flux spikes.

### Spectral Slope
**Measures**: Overall spectral tilt (linear regression coefficient).

| Range | Characterisation |
|-------|------------------|
| < -5e-05 | Steep negative slope, dark/warm, relaxed voice |
| -5e-05 to -1e-05 | Moderate slope, balanced brightness |
| -1e-05 to 0 | Shallow slope, bright, energetic |
| > 0 | Positive slope, very bright, potentially strained |

**Voice context**: Modal speech approximately -6 dB/octave; breathy voice steeper; pressed voice shallower.

### Spectral Decrease
**Measures**: Weighted slope emphasising lower frequencies.

| Range | Characterisation |
|-------|------------------|
| < -0.05 | Strong bass concentration, warm foundation |
| -0.05 to -0.02 | Moderate low-frequency emphasis, typical male voice |
| -0.02 to 0 | Balanced low-frequency content |
| > 0 | Weak bass, thin, lacking body |

**Voice context**: Male voices typically show more negative decrease than female voices.

### Spectral Rolloff (Hz)
**Measures**: Frequency below which 85–95% of spectral energy resides.

| Range | Characterisation |
|-------|------------------|
| < 3000 Hz | Dark, muffled, heavy filtering or pure voiced content |
| 3000–5000 Hz | Warm, controlled high frequencies |
| 5000–8000 Hz | Balanced brightness, natural speech |
| 8000–12000 Hz | Bright, airy, good articulation |
| > 12000 Hz | Very bright, significant sibilance or air |

**Voice context**: Higher rolloff indicates more high-frequency content; useful for sibilance detection.

## Output Format

For each metric, provide a single-line characterisation in the format:

```
<Metric Name>: <Value> — <Characterisation Label> (<Processing Hint>)
```

Then provide a **Summary** section with:
1. **Overall Voice Character**: 2–3 sentence description of the voice quality
2. **Processing Recommendations**: Bullet points for suggested filter tuning

## Example Output

```
Spectral Centroid: 5785 Hz — Bright, crisp (Consider de-essing 4–7 kHz if harsh)
Spectral Spread: 5157 Hz — Very wide bandwidth (Check for noise floor or mixed phonemes)
Spectral Skewness: 1.132 — Low-frequency emphasis with HF tail (Typical male voice pattern)
Spectral Kurtosis: 5.781 — Clearly peaked, strong harmonics (Good voice quality indicator)
Spectral Entropy: 0.011 — Highly ordered (Clear tonal content)
Spectral Flatness: 0.619 — Noise-dominant or breathy (Investigate aspiration or room noise)
Spectral Crest: 38.74 — Prominent spectral peaks (Clear harmonic structure despite flatness)
Spectral Flux: 0.001 — Very stable (Sustained phonation, not transient)
Spectral Slope: -2.33e-05 — Moderate slope, balanced (Neither dark nor harsh)
Spectral Decrease: -0.026 — Moderate low-frequency emphasis (Expected male voice foundation)
Spectral Rolloff: 11864 Hz — Bright, airy (Potential sibilance; check 4–8 kHz)

**Summary**

Overall Voice Character: Bright male voice with clear harmonic structure and good tonal definition. The elevated flatness alongside high kurtosis suggests either breathy quality, measurement across varied phonemes, or background noise coexisting with strong harmonics.

Processing Recommendations:
• De-essing: Target 4–7 kHz range; high rolloff and centroid indicate sibilance potential
• High-shelf: Consider gentle -2 to -3 dB above 8 kHz if brightness is excessive
• Low-mid warmth: Preserve 200–400 Hz; spectral decrease confirms good bass foundation
• Noise assessment: Elevated flatness warrants checking noise floor in silent segments
```

## Instructions

1. Parse the input spectral measurements
2. Look up each value against the characterisation thresholds
3. Generate the characterisation label and processing hint for each metric
4. Synthesise the overall voice character from the combination of metrics
5. Provide actionable processing recommendations based on the spectral profile

When metrics appear contradictory (e.g., high flatness with high kurtosis), note this explicitly and suggest possible explanations (mixed phoneme content, breathy-but-harmonic voice, noise coexisting with clear pitch).

Always frame characterisations in audio engineering terms useful for filter tuning decisions: EQ adjustments, compression settings, noise reduction, de-essing thresholds.
