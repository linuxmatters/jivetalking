# Dolby SR (Spectral Recording) Adaptive Noise Reduction

*Capturing the philosophy of the professional broadcast standard that engineers described as "sounding like nothing at all"*

---

## The Legend of Dolby SR

In 1986, Dolby Laboratories introduced Spectral Recording—a noise reduction system so sophisticated it achieved **24dB of noise reduction** while remaining virtually inaudible. For two decades, Dolby SR defined professional broadcast and film audio, running through nearly every major motion picture and television production until the digital transition.

Where earlier noise reduction systems (Dolby A, Dolby B, dbx) suffered from audible pumping, breathing, and cross-band modulation, SR's genius lay in *intelligent restraint*. The system's guiding philosophy was revolutionary: **treat the signal only as much as absolutely necessary**.

### The "Least Treatment" Principle

Dolby SR's defining innovation was its refusal to over-process. Low-level signals remained fully boosted until a dominant signal appeared. The system dynamically scaled its intervention based on what the audio actually needed—never more. This principle produced noise reduction that engineers described as "hearing nothing different, just less noise."

### Technical Architecture

SR employed 10 simultaneous companders (5 fixed-band + 5 sliding-band) operating across strategically chosen frequency ranges. The dual-mode operation solved the artifacts that plagued earlier systems:

| Earlier System | Problem | SR Solution |
|---------------|---------|-------------|
| Dolby A | Cross-band pumping | Fixed bands provide stable "floor" |
| Dolby B | Noise "breathing" | Sliding bands track without affecting neighbours |

The result: transparent noise reduction on even the most demanding programme material.

---

## Jivetalking's Implementation

Jivetalking honours Dolby SR's philosophy through FFmpeg's `afftdn` filter with a **15-band Bark-scale voice-protective profile**. Rather than mimicking SR's analogue compander topology, we capture its principles: psychoacoustic frequency weighting, conservative treatment depths, and absolute prioritisation of transparency over aggressive noise removal.

### Design Philosophy

The DS201 gate handles silence cleanup. This filter's role is narrower but equally critical: **polish the noise floor under speech** without introducing artifacts or changing the voice character.

| Design Constraint | Implementation |
|------------------|----------------|
| Least Treatment | Residual floor preserves natural room tone |
| Voice Protection | Reduced NR in formant frequencies (172–2756 Hz) |
| Artifact Prevention | High gain smoothing (10–20) hides all gain changes |
| Transparency | Slow adaptivity (0.3–0.5) prevents audible modulation |

### The 15-Band Bark-Scale Profile

Human hearing doesn't weight all frequencies equally. The Bark scale maps to the ear's critical bands—perceptual frequency groupings that determine masking thresholds. Our voice-protective profile applies differentiated noise reduction across these bands:

| Band | Frequency | Content | Scale |
|------|-----------|---------|-------|
| 0–1 | 20–100 Hz | Sub-bass, bass | 1.0 |
| 2 | 100–172 Hz | Chest resonance | 0.7 |
| 3–5 | 172–600 Hz | Formants, fundamentals | 0.4–0.5 |
| 6–7 | 600–1350 Hz | Core intelligibility | 0.5 |
| 8–9 | 1350–2756 Hz | Upper formants, consonants | 0.6–0.7 |
| 10–11 | 2756–5500 Hz | Sibilance | 0.8–0.9 |
| 12–14 | 5500–16000 Hz | Air, breath, consonant detail | 1.0 |

**Scale interpretation:** 1.0 = full noise reduction; lower values = voice protection (reduced NR to preserve character).

The critical speech intelligibility range (172–2756 Hz) receives maximum protection. Sibilance bands get light protection to avoid emphasising harshness. Sub-bass and ultra-HF receive full treatment—noise hides there without affecting perception.

---

## Adaptive Behaviour

Pass 1 analysis drives all parameter tuning. The filter adapts to both source quality and voice characteristics.

### Noise Floor Severity Response

| Source Quality | Noise Floor | NR Amount | Residual Floor |
|---------------|-------------|-----------|----------------|
| Studio clean | < −80 dBFS | 2 dB | −26 dB |
| Home office | −80 to −65 dBFS | 3–4 dB | −28 to −30 dB |
| Noisy environment | > −55 dBFS | 5–6 dB | −30 to −32 dB |

Conservative throughout. The DS201 gate handles heavy lifting during silence; this filter only polishes under speech.

### [Spectral Adaptation](Spectral%20Analysis.md)

| Measurement | Adaptation |
|-------------|------------|
| High centroid (>2500 Hz) | Reduce HF band NR—preserve consonant detail |
| Low centroid (<1500 Hz) | Increase LF band NR—bass-heavy source |
| Very clean (<−80 dB floor) | Extra voice band protection |
| Very noisy (>−55 dB floor) | Relax voice protection—accept minimal coloration |

### Warm Voice Detection

Dark, warm voices (typical of bass presenters) mask noise less effectively in lower frequencies. The filter detects warm voices via spectral indicators:

- Low centroid (<4000 Hz)
- High skewness (>1.5)
- Strong bass emphasis (negative spectral decrease)

Warm voices receive a subtle NR boost (+1.0 dB, +0.5 dB for very warm) applied safely because the bass masks any processing artifacts.

---

## Configuration

### FilterChainConfig Fields

| Field | Type | Range | Default | Purpose |
|-------|------|-------|---------|---------|
| `DolbySREnabled` | bool | — | true | Enable/disable filter |
| `DolbySRNoiseFloor` | float64 | −80 to −20 dB | −50 dB | Measured noise floor |
| `DolbySRNoiseReduction` | float64 | 2–6 dB | 8 dB | Base NR amount |
| `DolbySRGainSmooth` | int | 10–20 | 8 | Gain change smoothing |
| `DolbySRResidualFloor` | float64 | −32 to −26 dB | −38 dB | Least Treatment floor |
| `DolbySRAdaptivity` | float64 | 0.3–0.5 | 0.70 | Gain adaptation speed |
| `DolbySRNoiseType` | string | "c" | "c" | Custom (enables band profile) |
| `DolbySRBandProfile` | []float64 | 15 elements | Voice-protective | Bark-scale NR scales |

### FFmpeg Filter Specification

```
afftdn=nf=-50.0:nr=4.0:tn=enabled:gs=15:rf=-30.0:ad=0.4:nt=c:bn='4.0 4.0 2.8 2.0 1.6 1.6 2.0 2.0 2.4 2.8 3.2 3.6 4.0 4.0 4.0'
```

**Parameter breakdown:**
- `nf`: Noise floor from measurements
- `nr`: Base noise reduction (scaled per-band via `bn`)
- `tn=enabled`: Enable noise tracking (no sample required)
- `gs`: Gain smoothing—higher = slower, more transparent
- `rf`: Residual floor—Least Treatment threshold
- `ad`: Adaptivity—how fast gains adjust
- `nt=c`: Custom noise type enables `bn` parameter
- `bn`: 15-band Bark-scale profile (base NR × band scale)

---

## Pipeline Integration

```
DS201 Gate (silence cleanup) → Dolby SR (spectral polish) → LA-2A (dynamics)
```

**Division of responsibility:**
- **DS201 Gate:** Inter-phrase noise reduction via soft expansion
- **Dolby SR:** Under-speech noise floor polishing
- **LA-2A:** Dynamic range control and warmth

The filters operate on complementary domains. The gate works during silence and transitions. Dolby SR works on continuous noise under speech. This separation prevents over-processing and preserves natural dynamics.

---

## References

- Dolby Laboratories, "The Dolby SR Process" (1986)
- Ray Dolby, "Spectral Recording: A New Approach to Professional Noise Reduction" (1987)
- FFmpeg Documentation: `afftdn` filter
