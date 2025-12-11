# Teletronix LA-2A Leveling Amplifier

*Emulating the optical compressor that defined the sound of recorded voice for six decades*

---

## The Legend of the LA-2A

In 1965, Jim Lawrence founded Teletronix Engineering and introduced a compressor that would become synonymous with vocal recording: the LA-2A Leveling Amplifier. Decades later, engineers still reach for LA-2As—original units now command five-figure prices—whenever a voice needs warmth without aggression, control without constraint.

The LA-2A's magic lies in its **T4 electro-optical attenuator**: an electroluminescent panel paired with a cadmium sulfide photoresistor. Light output changes with the input signal; photoresistor conductance follows the light. This indirect coupling creates inherently smooth gain changes that electronic circuits struggle to replicate.

Bill Putnam, founder of Universal Audio and the man who acquired Teletronix in the 1960s, described the LA-2A's character simply: it "treats your signal lovingly."

### What Makes the T4 Cell Special

The T4's response isn't linear or instantaneous—and that's the point. The photoresistor's conductance changes occur on a molecular level, creating attack and release curves that no capacitor-based design can match:

| Characteristic | T4 Behaviour | Sonic Result |
|---------------|--------------|--------------|
| Attack | ~10ms fixed | Transients pass through naturally |
| Release (initial) | 60ms to 50% | Quick recovery from peaks |
| Release (full) | 1–15 seconds | Graceful return, no pumping |
| Ratio | Programme-dependent | Harder compression on louder signals |
| Knee | Inherently soft | Gradual onset, musical transition |

The two-stage release is the LA-2A's signature: short peaks recover quickly, sustained compression releases slowly. This creates the "levelling" effect—consistent output without the mechanical feel of VCA compression.

---

## Jivetalking's Implementation

Jivetalking captures the LA-2A's programme-dependent behaviour through FFmpeg's `acompressor` filter with **[spectral-adaptive parameter tuning](Spectral%20Analysis.md)**. Pass 1 measurements drive every parameter, approximating the way the T4 cell naturally responds to different programme material.

### Design Philosophy

| T4 Characteristic | Implementation Strategy |
|-------------------|------------------------|
| Fixed 10ms attack | Base 10ms with ±2ms transient adaptation |
| Two-stage release | LRA + spectral flux determine release time |
| Programme-dependent ratio | Spectral kurtosis modulates 2.5–3.5:1 range |
| Soft knee | Centroid-adaptive knee (3.5–5.0) |
| Warmth on dark voices | Skewness detection triggers knee + release boost |

---

## Adaptive Parameter Tuning

### Attack: Preserving Consonant "Pluck"

The LA-2A's fixed 10ms attack is slow enough to let word onsets through—critical for podcast intelligibility. We honour this baseline with minimal adaptation for extreme cases:

| Transient Character | MaxDifference | Attack |
|--------------------|---------------|--------|
| Sharp transients | > 25% | 8 ms |
| Normal speech | 10–25% | 10 ms |
| Soft delivery | < 10% | 12 ms |

### Release: Approximating Two-Stage Recovery

The T4 cell's two-stage release gives the LA-2A its breathing room. We approximate this by scaling release time with loudness range and spectral flux:

| Source Character | LRA | Spectral Flux | Release |
|-----------------|-----|---------------|---------|
| Expressive speech | > 14 LU | > 0.025 | 300 ms |
| Standard podcast | 8–14 LU | 0.008–0.025 | 200 ms |
| Compressed delivery | < 8 LU | < 0.008 | 150 ms |

**Warm voice boost:** Dark voices (skewness > 1.5) add 30ms release to preserve body.
**Heavy compression boost:** Large LUFS gap (>15dB) adds 50ms, mimicking the T4's slower recovery after sustained compression.

### Ratio: The Levelling Character

Real LA-2As don't have a fixed ratio—the T4 cell compresses harder on louder signals. We approximate this programme-dependency using spectral kurtosis (peaked vs. flat spectrum):

| Spectral Character | Kurtosis | Ratio |
|-------------------|----------|-------|
| Peaked/tonal harmonics | > 10 | 2.5:1 |
| Standard speech | 5–10 | 3.0:1 |
| Flat/noise-like | < 5 | 3.5:1 |

Very wide dynamic range (>35dB) adds +0.5 to the ratio for extra control.

### Threshold: Signal-Relative

The LA-2A's "Peak Reduction" knob sets threshold relative to the incoming signal. We calculate threshold from peak level minus a dynamic range-dependent headroom:

| Dynamic Range | Headroom | Compression Depth |
|--------------|----------|-------------------|
| > 30 dB | 20 dB | Heavy levelling |
| 20–30 dB | 15 dB | Standard LA-2A |
| < 20 dB | 10 dB | Light levelling |

Threshold range: −40 dB to −12 dB (clamped for safety).

### Knee: T4 Softness

The T4 cell's gradual gain changes produce an inherently soft knee. We adapt knee softness based on voice character:

| Voice Character | Spectral Centroid | Knee |
|----------------|-------------------|------|
| Dark/warm voice | < 4000 Hz | 5.0 |
| Normal voice | 4000–6000 Hz | 4.0 |
| Bright voice | > 6000 Hz | 3.5 |

Warm voices (skewness > 1.5) add +0.5 knee for extra softness.

### Mix: Honouring 100% Wet

Real LA-2As have no parallel compression—they're 100% wet. We default to full wet, reducing mix only for problematic recordings where dry signal masks artefacts:

| Recording Quality | Noise Floor | Mix |
|------------------|-------------|-----|
| Studio clean | < −65 dBFS | 1.0 |
| Home office | −65 to −45 dBFS | 0.93 |
| Noisy environment | > −45 dBFS | 0.85 |

### Makeup Gain: Conservative Compensation

Makeup gain compensates for compression but shouldn't over-drive downstream processing:

```
overshoot = peak_level − threshold
reduction = overshoot × (1 − 1/ratio)
makeup = reduction × 0.65
```

Clamped to 1–5 dB. Let normalisation handle the rest.

---

## Configuration

### FilterChainConfig Fields

| Field | Type | Range | Default | Purpose |
|-------|------|-------|---------|---------|
| `LA2AEnabled` | bool | — | true | Enable/disable filter |
| `LA2AThreshold` | float64 | −40 to −12 dB | −24 dB | Compression threshold |
| `LA2ARatio` | float64 | 2.0–5.0 | 3.0 | Compression ratio |
| `LA2AAttack` | float64 | 8–12 ms | 10 ms | Attack time |
| `LA2ARelease` | float64 | 150–300 ms | 200 ms | Release time |
| `LA2AMakeup` | float64 | 1–5 dB | 2 dB | Makeup gain |
| `LA2AKnee` | float64 | 3.5–5.0 | 4.0 | Knee softness |
| `LA2AMix` | float64 | 0.85–1.0 | 1.0 | Wet/dry mix |

### FFmpeg Filter Specification

```
acompressor=threshold=-24dB:ratio=3:attack=10:release=200:makeup=2dB:knee=4:mix=1
```

---

## Pipeline Integration

```
DS201 Gate (dynamics cleanup) → Dolby SR (noise polish) → LA-2A (levelling) → Limiter
```

**Division of responsibility:**
- **DS201 Gate:** Inter-phrase silence cleanup
- **Dolby SR:** Spectral noise floor polishing
- **LA-2A:** Dynamic range levelling with warmth
- **Limiter:** Final peak control

The LA-2A operates on already-cleaned audio, so it can focus on its strength: gentle, programme-dependent levelling that evens out volume variations while preserving the natural character of speech.

---

## References

- Teletronix Engineering, LA-2A Leveling Amplifier Manual (1965)
- Universal Audio, "LA-2A Leveling Amplifier" reissue documentation
- Dennis Fink, "The History of the LA-2A" (Mix Magazine, 2003)
- FFmpeg Documentation: `acompressor` filter
- https://github.com/aim-qmul/4a2a
