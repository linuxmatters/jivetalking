---

# Audio Spectral Analysis Reference for Voice Characterisation

**A complete reference for interpreting audio metrics in vocal processing, with specific ranges for spoken word and singing, enabling quality assessment of audio processing.**

This document provides authoritative definitions, typical value ranges, and perceptual interpretations for audio metrics used in adaptive voice processing. Each metric includes specific target ranges for spoken word (podcast/broadcast speech) and singing, enabling determination of whether processing has damaged or enhanced vocal quality.

---

## Level Metrics

### RMS Level

**Definition:** Root Mean Square level - the average power of the audio signal, representing perceived loudness more accurately than peak values.

| Range | Interpretation | Quality Indicator |
|-------|----------------|-------------------|
| > -12 dBFS | Very hot, likely clipping | ⚠️ Overprocessed |
| -18 to -12 dBFS | Hot, broadcast-ready | ✓ Good for podcasts |
| -24 to -18 dBFS | Moderate, typical recording | ✓ Normal range |
| -36 to -24 dBFS | Quiet, needs gain | Monitor |
| < -36 dBFS | Very quiet, problematic | ⚠️ Too low |

**Vocal Targets:**
- **Spoken word:** -20 to -16 dBFS (targeting -18 LUFS final output)
- **Singing:** -18 to -12 dBFS (higher dynamic range)

---

### Peak Level

**Definition:** The maximum instantaneous amplitude of the audio signal.

| Range | Interpretation | Quality Indicator |
|-------|----------------|-------------------|
| > 0 dBFS | Clipped, digital distortion | ❌ Damaged |
| -1 to 0 dBFS | At limit, risk of inter-sample peaks | ⚠️ Monitor |
| -6 to -1 dBFS | Healthy headroom | ✓ Good |
| -12 to -6 dBFS | Conservative headroom | ✓ Safe |
| < -12 dBFS | Excessive headroom | Underutilised |

**Vocal Targets:**
- **Spoken word:** -3 to -1 dBFS (before loudnorm)
- **Singing:** -6 to -1 dBFS (preserve transients)

---

### Crest Factor

**Definition:** The ratio of peak level to RMS level, measured in dB. Indicates dynamic range and transient content.

| Crest Factor | Interpretation | Quality Indicator |
|--------------|----------------|-------------------|
| < 6 dB | Heavily compressed, brickwalled | ⚠️ Overprocessed |
| 6-9 dB | Moderate compression | Monitor context |
| 9-12 dB | Well-balanced mix | ✓ Optimal for speech |
| 12-15 dB | Natural dynamics, sparse sections | ✓ Good |
| 15-18 dB | High dynamics, transient-heavy | Normal for percussion |
| > 18 dB | Extreme dynamics | May need compression |

**Vocal Targets:**
- **Spoken word:** 9-14 dB (natural articulation with controlled dynamics)
- **Singing:** 10-16 dB (preserve emotional dynamics)

**Quality Assessment:** Crest factors below 6 dB indicate over-limiting. Values above 18 dB may indicate insufficient level control.

---

## Loudness Metrics (EBU R128 / ITU-R BS.1770)

### Momentary LUFS

**Definition:** Integrated loudness over a 400ms window; captures short-term loudness fluctuations.

| Range | Interpretation | Quality Indicator |
|-------|----------------|-------------------|
| > -10 LUFS | Very loud transient | ⚠️ Peak loudness |
| -16 to -10 LUFS | Loud speech/emphasis | Normal for stressed speech |
| -23 to -16 LUFS | Normal speech level | ✓ Target range |
| -30 to -23 LUFS | Quiet passages | Normal variation |
| < -30 LUFS | Very quiet/pause | Normal for inter-phrase |

**Vocal Targets:**
- **Spoken word:** -20 to -14 LUFS (momentary peaks)
- **Singing:** -18 to -8 LUFS (wider dynamic range)

---

### Short-term LUFS

**Definition:** Integrated loudness over a 3-second window; indicates perceived loudness of phrases.

| Range | Interpretation | Quality Indicator |
|-------|----------------|-------------------|
| > -12 LUFS | Very loud | ⚠️ Check limiting |
| -16 to -12 LUFS | Loud, energetic | Normal for emphasis |
| -20 to -16 LUFS | Moderate, conversational | ✓ Podcast target |
| -24 to -20 LUFS | Quiet, intimate | ✓ Broadcast target |
| < -24 LUFS | Very quiet | May need gain |

**Vocal Targets:**
- **Spoken word (podcast):** -18 to -14 LUFS
- **Spoken word (broadcast):** -25 to -21 LUFS
- **Singing:** -20 to -10 LUFS

---

### True Peak

**Definition:** The maximum level of the reconstructed continuous waveform, accounting for inter-sample peaks.

| Range | Interpretation | Quality Indicator |
|-------|----------------|-------------------|
| > 0 dBTP | Clipping, inter-sample overs | ❌ Damaged |
| -0.5 to 0 dBTP | At limit | ⚠️ Risk of codec clipping |
| -1 to -0.5 dBTP | Tight headroom | Acceptable |
| -3 to -1 dBTP | Safe headroom | ✓ EBU R128 compliant |
| < -3 dBTP | Conservative | ✓ Very safe |

**Standards:**
- **EBU R128:** ≤ -1 dBTP (production), tolerance ±0.3 dB
- **Apple Podcasts:** ≤ -1 dBTP
- **AES Streaming:** ≤ -1 dBTP

---

### Sample Peak

**Definition:** The maximum digital sample value in the signal.

| Range | Interpretation | Quality Indicator |
|-------|----------------|-------------------|
| 0 dBFS | Full scale, at limit | ⚠️ No headroom |
| -1 to 0 dBFS | Near limit | Monitor true peak |
| -3 to -1 dBFS | Safe | ✓ Good |
| < -6 dBFS | Conservative | ✓ Plenty of headroom |

**Note:** Sample peak underestimates true peak by typically 0.5-3 dB for complex signals.

---

## Spectral Shape Metrics

### Spectral Mean

**Definition:** The arithmetic mean of spectral magnitudes across all frequency bins. Highly dependent on normalisation method and FFT parameters.

| Relative Level | Interpretation | Quality Indicator |
|----------------|----------------|-------------------|
| Higher than baseline | Increased spectral energy | Monitor for noise |
| At baseline | Typical level for content | ✓ Normal |
| Lower than baseline | Reduced energy | Check for filtering |

**Usage:** Primarily useful as a relative comparison metric within the same recording or between similar recordings using identical analysis parameters.

---

### Spectral Variance

**Definition:** The variance of magnitude values around the spectral mean; indicates spectral energy distribution uniformity.

| Relative Level | Interpretation | Quality Indicator |
|----------------|----------------|-------------------|
| High variance | Distinct spectral structure, peaks | ✓ Good harmonic content |
| Moderate variance | Mixed tonal and noise | Normal speech |
| Low variance | Uniform energy, noise-like | ⚠️ Check for noise |

**Vocal Targets:**
- **Spoken word:** Moderate to high variance indicates clear harmonic structure
- **Singing:** High variance indicates strong formants and harmonics

---

### Spectral Centroid

**Definition:** The "centre of gravity" of the spectrum - the frequency around which spectral energy balances. Directly correlates with perceived brightness.

| Centroid (Hz) | Interpretation | Quality Indicator |
|---------------|----------------|-------------------|
| < 500 Hz | Dark, muffled | ⚠️ Possible low-pass filtering |
| 500-1500 Hz | Warm, present | ✓ Male voiced speech |
| 1500-2500 Hz | Forward, clear | ✓ Female voiced speech |
| 2500-4000 Hz | Bright, articulate | ✓ Good articulation |
| 4000-6000 Hz | Very bright, sibilant | Consonant content present |
| > 6000 Hz | Extremely bright | ⚠️ Fricatives or HF noise |

**Vocal Targets:**
- **Spoken word (male):** 800-2000 Hz (sustained vowels)
- **Spoken word (female):** 1200-2800 Hz (sustained vowels)
- **Singing (male):** 1000-2500 Hz (with singer's formant: 2500-3500 Hz boost)
- **Singing (female):** 1500-3500 Hz

**Quality Assessment:** Centroid significantly above these ranges may indicate sibilance issues; significantly below may indicate over-filtering or dull processing.

---

### Spectral Spread

**Definition:** Standard deviation around the centroid - the "instantaneous bandwidth" of the spectrum.

| Spread (Hz) | Interpretation | Quality Indicator |
|-------------|----------------|-------------------|
| < 500 Hz | Very narrow, pure tone | Unusual for voice |
| 500-1500 Hz | Narrow, clean voiced | ✓ Clean vowels |
| 1500-2500 Hz | Moderate, natural speech | ✓ Normal articulation |
| 2500-4000 Hz | Wide, mixed content | Mixed voiced/unvoiced |
| > 4000 Hz | Very wide, broadband | ⚠️ Noise or fricatives |

**Vocal Targets:**
- **Spoken word:** 1000-2500 Hz
- **Singing:** 800-2000 Hz (cleaner vowel sustain)

**Quality Assessment:** Excessive spread may indicate noise contamination or room ambience.

---

### Spectral Skewness

**Definition:** Third-order moment measuring asymmetry around the centroid. Indicates energy distribution bias.

| Skewness | Interpretation | Quality Indicator |
|----------|----------------|-------------------|
| < -0.5 | Negative skew, HF emphasis | Fricatives, sibilants |
| -0.5 to 0 | Slight negative, balanced bright | Articulate speech |
| 0 to 0.5 | Slight positive, typical speech | ✓ Normal modal voice |
| 0.5 to 1.5 | Positive skew, LF emphasis with HF tail | ✓ Typical male voice |
| 1.5 to 2.5 | Strong positive, bass-forward | Full-bodied voice |
| > 2.5 | Very strong LF bias | ⚠️ Possible masking |

**Vocal Targets:**
- **Spoken word (male):** 0.8-2.0 (positive skew expected)
- **Spoken word (female):** 0.3-1.5
- **Singing:** 0.5-2.0 (depends on register)

**Quality Assessment:** Positive skewness is normal for voiced speech due to strong fundamental and lower harmonics with a tail toward higher frequencies.

---

### Spectral Kurtosis

**Definition:** Fourth-order moment measuring "peakedness" of the spectrum. Reference: Gaussian distribution = 3.

| Kurtosis | Distribution Type | Interpretation | Quality Indicator |
|----------|-------------------|----------------|-------------------|
| < 2 | Platykurtic (very flat) | Noise-dominant | ⚠️ Poor voice quality |
| 2-3 | Slightly platykurtic | Noisy or fricative | Unvoiced content |
| ≈ 3 | Mesokurtic (Gaussian) | White noise reference | Mixed content |
| 3-5 | Moderately leptokurtic | Mixed tonal and noise | Transition content |
| 5-10 | Leptokurtic (peaky) | Clear harmonics | ✓ Good voice quality |
| > 10 | Highly leptokurtic | Very strong peaks | ✓ Excellent harmonics |

**Vocal Targets:**
- **Spoken word:** 4-12 (clear harmonic structure)
- **Singing:** 6-15 (strong fundamental and harmonics)

**Quality Assessment:** High kurtosis combined with positive skewness indicates healthy voice production with clear harmonic structure. Values trending toward 3 indicate noise contamination.

---

### Spectral Entropy

**Definition:** Shannon entropy applied to the frequency domain, normalised to 0-1 range. Measures disorder in power distribution.

| Entropy | Interpretation | Quality Indicator |
|---------|----------------|-------------------|
| 0.0-0.15 | Highly ordered, clear pitch | ✓ Pure tone, clean vowel |
| 0.15-0.30 | Ordered, good harmonic content | ✓ Clean voiced speech |
| 0.30-0.50 | Moderate order, mixed content | Mixed voiced/unvoiced |
| 0.50-0.70 | Disordered, noisy | Fricatives, aspiration |
| 0.70-0.85 | Highly disordered | Unvoiced consonants |
| 0.85-1.0 | Near-white noise | ⚠️ Noise-dominant |

**Vocal Targets:**
- **Spoken word (voiced):** 0.08-0.30
- **Spoken word (mixed):** 0.20-0.50
- **Singing (sustained):** 0.05-0.25

**Quality Assessment:** Entropy provides excellent voiced/unvoiced discrimination. Clean speech should show low entropy during voiced segments.

---

### Spectral Flatness

**Definition:** Ratio of geometric mean to arithmetic mean of the spectrum (Wiener entropy). MPEG-7 standard descriptor for tonality. Range: 0 (pure tone) to 1 (white noise).

| Flatness | Flatness (dB) | Interpretation | Quality Indicator |
|----------|---------------|----------------|-------------------|
| 0.0-0.05 | < -26 dB | Pure tone, maximum tonality | Single harmonic |
| 0.05-0.15 | -26 to -16 dB | Very tonal, strong harmonics | ✓ Clean voiced vowels |
| 0.15-0.30 | -16 to -10 dB | Tonal with some noise | ✓ Clean voiced speech |
| 0.30-0.50 | -10 to -6 dB | Mixed tonal and noise | Mixed speech content |
| 0.50-0.70 | -6 to -3 dB | Noise-like, some tonal | Unvoiced, breathy |
| 0.70-0.90 | -3 to -1 dB | Highly noise-like | Fricatives, aspiration |
| 0.90-1.0 | > -1 dB | Near white noise | ⚠️ Noise contamination |

**Vocal Targets:**
- **Spoken word (voiced):** 0.05-0.25
- **Singing (sustained vowels):** 0.03-0.20

**Quality Assessment:** Flatness above 0.4 during sustained vowels suggests breathiness, aspiration, or noise contamination.

---

### Spectral Crest

**Definition:** Ratio of spectral peak power to mean power. Higher values indicate prominent spectral peaks (tonality); lower values indicate flatter spectra (noise-like).

| Crest (linear) | Crest (dB) | Interpretation | Quality Indicator |
|----------------|------------|----------------|-------------------|
| < 5 | < 14 dB | Flat spectrum, noise-like | ⚠️ Low harmonic content |
| 5-15 | 14-24 dB | Moderate peaks | Mixed content |
| 15-30 | 24-30 dB | Strong peaks | Good tonal content |
| 30-60 | 30-36 dB | Very strong peaks | ✓ Clear harmonics |
| > 60 | > 36 dB | Dominant peaks | ✓ Excellent harmonic clarity |

**Vocal Targets:**
- **Spoken word:** 20-60 (linear), 26-36 dB
- **Singing:** 30-100 (linear), 30-40 dB

**Quality Assessment:** Spectral crest is the inverse of flatness conceptually. High crest with moderate flatness indicates clear harmonics amidst mixed content.

---

### Spectral Flux

**Definition:** Euclidean distance between successive spectral frames; measures rate of spectral change.

| Flux (normalised) | Interpretation | Quality Indicator |
|-------------------|----------------|-------------------|
| < 0.001 | Very stable, sustained | Held vowels |
| 0.001-0.005 | Stable, continuous | ✓ Sustained phonation |
| 0.005-0.02 | Moderate variation | ✓ Natural articulation |
| 0.02-0.05 | High variation | Consonant transitions |
| > 0.05 | Very high, transient | Plosives, transients |

**Vocal Targets:**
- **Spoken word (sustained vowels):** < 0.005
- **Spoken word (natural speech):** 0.005-0.03
- **Singing (sustained notes):** < 0.003

**Quality Assessment:** Consistently high flux during sustained phonation may indicate instability or processing artefacts.

---

### Spectral Slope

**Definition:** Linear regression slope of the spectrum across frequency bins; measures spectral tilt in dB/Hz or dB/octave.

| Slope (dB/octave) | Interpretation | Quality Indicator |
|-------------------|----------------|-------------------|
| < -15 | Very steep, dark | Breathy, falsetto |
| -12 to -15 | Steep, warm | ✓ Breathy voice, falsetto |
| -6 to -12 | Moderate, typical | ✓ Modal speech (-6 dB/oct typical) |
| -3 to -6 | Shallow, bright | Pressed voice, emphasis |
| > -3 | Very shallow, harsh | ⚠️ Potential strain or harshness |

**Reference Values:**
- Glottal source: approximately -12 dB/octave
- After lip radiation (+6 dB): modal speech typically -6 dB/octave
- Loud modal register: -3 to -6 dB/octave
- Falsetto/breathy: -12 to -25 dB/octave

**Vocal Targets:**
- **Spoken word (modal):** -8 to -4 dB/octave
- **Singing (modal):** -6 to -3 dB/octave (louder produces shallower slope)
- **Singing (falsetto):** -15 to -12 dB/octave

**Quality Assessment:** Slope significantly shallower than -3 dB/octave may indicate pressed or strained voice. Slope steeper than -15 dB/octave may indicate excessive HF attenuation.

---

### Spectral Decrease

**Definition:** Rate of spectral amplitude decline from the first frequency bin, with emphasis on lower frequencies.

| Decrease | Interpretation | Quality Indicator |
|----------|----------------|-------------------|
| < -0.1 | Strong bass emphasis | Possible LF boost |
| -0.1 to 0 | Moderate bass presence | ✓ Typical male speech |
| 0 to 0.05 | Balanced decrease | ✓ Balanced voice |
| 0.05 to 0.15 | Moderate decrease | Typical speech |
| > 0.15 | Strong HF content | Bright, sibilant |

**Vocal Targets:**
- **Spoken word (male):** -0.05 to 0.05
- **Spoken word (female):** 0 to 0.08
- **Singing:** -0.03 to 0.05

---

### Spectral Rolloff

**Definition:** Frequency below which a specified percentage (typically 85% or 95%) of total spectral energy resides.

| Rolloff @ 85% (Hz) | Interpretation | Quality Indicator |
|--------------------|----------------|-------------------|
| < 2000 Hz | Very dark, muffled | ⚠️ Over-filtered |
| 2000-4000 Hz | Dark, heavy voiced | LF-dominant content |
| 4000-6000 Hz | Warm, balanced | ✓ Typical voiced speech |
| 6000-8000 Hz | Balanced, articulate | ✓ Good articulation |
| 8000-12000 Hz | Bright, airy | Good HF content |
| > 12000 Hz | Very bright | ⚠️ Check for sibilance |

**Vocal Targets (85% threshold):**
- **Spoken word (male):** 4000-8000 Hz
- **Spoken word (female):** 5000-10000 Hz
- **Singing:** 3500-8000 Hz (varies with register)

**Note:** The 95% threshold produces values approximately 1.5-2× higher.

**Quality Assessment:** Rolloff significantly below 4000 Hz indicates excessive high-frequency attenuation. Values above 12000 Hz may indicate sibilance issues requiring de-essing.

---

## Singer's Formant Considerations

For singing voice analysis, the presence and strength of the **singer's formant** (2500-3500 Hz) is a key quality indicator for trained classical voices:

| Metric | Untrained Singer | Trained Singer |
|--------|------------------|----------------|
| Spectral Centroid | 1000-2000 Hz | 1500-2500 Hz (elevated) |
| Energy at 2500-3500 Hz | Low | Prominent peak |
| Spectral Slope | -10 to -6 dB/oct | -6 to -3 dB/oct |

The singer's formant allows classical singers to project over orchestral accompaniment and is characterised by:
- Clustering of formants F3, F4, F5 in the 2500-3500 Hz region
- Elevated spectral energy that increases brightness
- Lower spectral slope (shallower decline)

---

## Summary: Quality Assessment Matrix

| Metric | Damaged (Over-processed) | Good Range | Damaged (Under-processed) |
|--------|--------------------------|------------|---------------------------|
| **Crest Factor** | < 6 dB | 9-14 dB | > 18 dB |
| **True Peak** | > 0 dBTP | -3 to -1 dBTP | < -6 dBTP (underutilised) |
| **Short-term LUFS** | > -12 LUFS | -20 to -14 LUFS | < -28 LUFS |
| **Spectral Centroid** | > 6000 Hz (harsh) | 800-4000 Hz | < 500 Hz (muffled) |
| **Spectral Flatness** | > 0.6 (noisy) | 0.05-0.30 | N/A |
| **Spectral Kurtosis** | < 3 (noise-contaminated) | 5-12 | N/A |
| **Spectral Entropy** | > 0.5 (disordered) | 0.08-0.30 | N/A |
| **Spectral Slope** | > -3 dB/oct (harsh) | -10 to -4 dB/oct | < -15 dB/oct (dull) |
| **Spectral Rolloff (85%)** | > 12 kHz (sibilant) | 4-8 kHz | < 2 kHz (filtered) |

---

## Interesting Findings

- **📌 Spectral slope is the primary loudness regulator in modal speech** - the NIH research shows that varying spectral slope from -3 to -12 dB/octave can produce approximately four doublings of perceived loudness with less than 5 dB SPL variation.

- **📌 The singer's formant (2500-3500 Hz) is a reliable marker of trained classical singing** - it emerges from clustering of F3, F4, and F5 formants and allows singers to project over orchestral accompaniment.

- **📌 Spectral kurtosis reliably distinguishes voice quality** - values above 5 indicate healthy harmonic structure, while values trending toward 3 suggest noise contamination or voice pathology.

- **⚠️ Average podcast loudness is -19 LUFS** (per Auphonic analysis), significantly louder than the EBU R128 broadcast standard of -23 LUFS, reflecting mobile listening requirements.

- **📌 Crest factors of 8-12 dB represent the "sweet spot"** for mastered content - below 6 dB indicates over-compression that may sound unnatural; above 18 dB may indicate insufficient dynamic control.

---

## Sources

1. Peeters, G. (2004). "A Large Set of Audio Features for Sound Description (Similarity and Classification) in the CUIDADO Project." IRCAM Technical Report. http://recherche.ircam.fr/anasyn/peeters/ARTICLES/Peeters_2003_cuidadoaudiofeatures.pdf

2. Titze, I.R. & Palaparthi, A. (2020). "Vocal Loudness Variation With Spectral Slope." Journal of Speech, Language, and Hearing Research, 63, 74-82. https://pmc.ncbi.nlm.nih.gov/articles/PMC7213475/

3. EBU R 128 (2023). "Loudness Normalisation and Permitted Maximum Level of Audio Signals." European Broadcasting Union. https://tech.ebu.ch/docs/r/r128.pdf

4. MathWorks. "Spectral Descriptors." Audio Toolbox Documentation. https://www.mathworks.com/help/audio/ug/spectral-descriptors.html

5. Wikipedia. "Spectral Flatness." https://en.wikipedia.org/wiki/Spectral_flatness

6. Johnston, J.D. (1988). ["Transform Coding of Audio Signals Using Perceptual Noise Criteria."](https://www.ee.columbia.edu/~dpwe/papers/Johns88-audiocoding.pdf) [IEEE Journal on Selected Areas in Communications, 6(2), 314-323.](https://ieeexplore.ieee.org/document/608)

7. Dubnov, S. (2004). ["Generalization of Spectral Flatness Measure for Non-Gaussian Linear Processes."](https://www.researchgate.net/publication/3343094_Generalization_of_Spectral_Flatness_Measure_for_Non-Gaussian_Linear_Processes) [IEEE Signal Processing Letters, 11(8), 698-701.](https://ieeexplore.ieee.org/document/1316889/)

8. Misra, H. et al. (2004). "Spectral Entropy Based Feature for Robust ASR." ICASSP'04.

9. iZotope. "What Is Crest Factor and Why Is It Important?" https://www.izotope.com/en/learn/what-is-crest-factor

10. Auphonic. "Loudness Targets for Mobile Audio, Podcasts, Radio and TV." https://auphonic.com/blog/2013/01/07/loudness-targets-mobile-audio-podcasts-radio-tv/

11. AES Technical Document AESTD1004.1.15-10. "Recommendation for Loudness of Audio Streaming and Network File Playback."

12. Apple. "Podcasts Authoring Best Practices." https://help.apple.com/itc/podcastsbestpractices/

13. Keller, P.E. et al. (2017). "Sex-Related Modulations of the Singer's Formant in Human Ensemble Singing." Frontiers in Psychology, 8:1559. https://pmc.ncbi.nlm.nih.gov/articles/PMC5603663/

14. Dixon, S. (2006). "Onset Detection Revisited." DAFx.

15. Scheirer, E. & Slaney, M. (1997). "Construction and Evaluation of a Robust Multifeature Speech/Music Discriminator." IEEE ICASSP.
