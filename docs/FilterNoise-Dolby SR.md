# Dolby SR (Spectral Recording) Adaptive Noise Reduction

*Capturing the philosophy of the professional broadcast standard that engineers described as "sounding like nothing at all"*

---

## The Legend of Dolby SR

In 1986, Dolby Laboratories introduced Spectral Recording-a noise reduction system so sophisticated it achieved **24dB of noise reduction** while remaining virtually inaudible. For two decades, Dolby SR defined professional broadcast and film audio, running through nearly every major motion picture and television production until the digital transition.

Where earlier noise reduction systems (Dolby A, Dolby B, dbx) suffered from audible pumping, breathing, and cross-band modulation, SR's genius lay in *intelligent restraint*. The system's guiding philosophy was revolutionary: **treat the signal only as much as absolutely necessary**.

### The "Least Treatment" Principle

Dolby SR's defining innovation was its refusal to over-process. Low-level signals remained fully boosted until a dominant signal appeared. The system dynamically scaled its intervention based on what the audio actually needed-never more. This principle produced noise reduction that engineers described as **"hearing nothing different, just less noise."**

### Technical Architecture

SR employed 10 simultaneous companders (5 fixed-band + 5 sliding-band) operating across strategically chosen frequency ranges. The dual-mode operation solved the artifacts that plagued earlier systems:

| Earlier System | Problem | SR Solution |
|---------------|---------|-------------|
| Dolby A | Cross-band pumping | Fixed bands provide stable "floor" |
| Dolby B | Noise "breathing" | Sliding bands track without affecting neighbours |

The result: transparent noise reduction on even the most demanding programme material.

---

## Jivetalking's Implementation

Jivetalking honours Dolby SR's philosophy through FFmpeg's `mcompand` multiband compander-a direct analogue to SR's original architecture. Where spectral subtraction approaches (FFT-based denoising) can introduce metallic artifacts and "underwater" tonal coloration, **compander-based noise reduction operates on the same principles as the hardware that defined broadcast audio for two decades**.

This is not approximation. This is implementation fidelity.

### Why Companders Matter

Dolby SR was a compander system-compress on encode, expand on decode. The magic happened in the expansion stage: quiet signals (noise) got pushed down while programme material passed through unchanged. FFmpeg's `mcompand` filter provides exactly this topology: multiband expansion with per-band control over attack, decay, threshold, and transfer curve.

| Approach | Mechanism | Artifact Risk |
|----------|-----------|---------------|
| FFT spectral subtraction | Remove estimated noise spectrum | Musical noise, tonal artifacts |
| Single-band expansion | Push quiet signals down | Pumping, breathing |
| **Multiband expansion** | Per-band expansion with voice protection | **Transparent** |

### Design Philosophy

The DS201 gate handles silence cleanup. This filter's role is narrower but equally critical: **polish the noise floor under speech** without introducing artifacts or changing the voice character.

| Design Constraint | Implementation |
|------------------|----------------|
| Least Treatment | FLAT reduction curve-same attenuation at all quiet levels |
| Voice Protection | Formant bands (F1/F2) receive gentler expansion |
| Artifact Prevention | Soft-knee transfer curves eliminate harsh transitions |
| Transparency | Per-band timing matched to frequency content |

### The 6-Band Voice-Protective Architecture

Like SR's fixed-band companders, we divide the spectrum into frequency regions matched to voice content. Each band receives independent expansion tuned to its role in speech intelligibility:

| Band | Frequency | Content | Scale | Attack | Decay | Knee |
|------|-----------|---------|-------|--------|-------|------|
| 1 | 0–100 Hz | Sub-bass, rumble | 100% | 6ms | 95ms | 6 |
| 2 | 100–300 Hz | Chest resonance | 100% | 5ms | 100ms | 8 |
| 3 | 300–800 Hz | Voice F1 formants | **105%** | 5ms | 100ms | 10 |
| 4 | 800–3300 Hz | Voice F2, intelligibility | **103%** | 5ms | 100ms | **12** |
| 5 | 3300–8000 Hz | Presence, sibilance | 100% | 2ms | 85ms | 10 |
| 6 | 8000–20500 Hz | Air, breath | **95%** | 2ms | 80ms | 6 |

**Scale interpretation:** Voice formant bands (F1/F2) receive *slightly more* expansion (105%/103%) to counteract the spectral darkening inherent in multiband processing. The air band receives *slightly less* (95%) to prevent over-brightness. This voice-protective scaling preserves natural timbre while maximising noise reduction elsewhere.

**Knee rationale:** Soft-knee values increase towards the critical voice bands (F1/F2) and decrease at the extremes. The 800–3300 Hz band (voice F2) receives the widest knee (12 dB) because this is precisely where Dolby SR employed its sliding-band filters—the range is SO critical to intelligibility that any audible compander action destroys naturalness. Without sliding bands, our best protection is a very soft knee to make expansion virtually inaudible. Sub-bass and air bands use narrower knees (6 dB) as these frequencies are less perceptually sensitive to processing artifacts.

### The FLAT Reduction Curve

The breakthrough discovery: **FLAT reduction eliminates artifacts entirely**. Rather than progressive expansion (quieter signals pushed down more aggressively), every signal below threshold receives identical attenuation.

```
Input:   -90 dB   -75 dB   -50 dB   -30 dB    0 dB
Output:  -106 dB  -91 dB   -50 dB   -30 dB    0 dB
          ↓        ↓        ↓        ↓         ↓
         16 dB    16 dB    0 dB     0 dB      0 dB  (reduction)
```

This mirrors Dolby SR's "Least Treatment" principle—the floor is lowered uniformly, preserving the relative dynamics of low-level signals while eliminating steady-state noise.

---

## Adaptive Behaviour

Pass 1 analysis drives a **lockstep** threshold and expansion selection. Both parameters are tuned together based on RMS trough—noisier sources get both a raised threshold (catches more noise) and deeper expansion (pushes it down further).

### Lockstep Tuning (Threshold + Expansion)

| Source Quality | RMS Trough | Threshold | Expansion | Treatment |
|---------------|------------|-----------|-----------|----------|
| Clean | < −85 dBFS | −50 dB | 16 dB | Gentle |
| Moderate | −85 to −80 dBFS | −45 dB | 20 dB | Balanced |
| Noisy | > −80 dBFS | −40 dB | 24 dB | Aggressive |

**Design rationale:** Threshold and expansion work as a single "aggressiveness dial". Noisier sources need *both* a higher threshold (to catch noise closer to quiet speech) *and* deeper expansion (to push it further down). Tuning them in lockstep simplifies the implementation and ensures coherent behaviour across the noise severity spectrum.

### [Spectral Preservation](Spectral%20Analysis.md)

The voice-protective band scaling ensures spectral characteristics are preserved even under aggressive expansion:

| Metric | Threshold | Typical Result |
|--------|-----------|----------------|
| Centroid drift | ±10% | <5% with voice scaling |
| Rolloff drift | ±10% | <5% with voice scaling |
| RMS preservation | ±0.5 dB | Within threshold (makeup gain compensates) |

Unlike FFT-based approaches that can shift spectral centroid by 20-30%, the compander architecture preserves voice brightness and character.

---

## Configuration

### FilterChainConfig Fields

| Field | Type | Range | Default | Purpose |
|-------|------|-------|---------|--------|
| `DolbySREnabled` | bool | - | true | Enable/disable filter |
| `DolbySRExpansionDB` | float64 | 16–24 dB | 16 dB | Base expansion amount (adaptive) |
| `DolbySRThresholdDB` | float64 | −50 to −40 dB | −50 dB | Expansion threshold (adaptive) |
| `DolbySRBands` | []DolbySRBandConfig | 6 bands | Voice-protective | Per-band configuration |

### Per-Band Configuration

Each band is configured with independent timing and scaling:

| Field | Purpose |
|-------|---------|
| `CrossoverHz` | Upper frequency boundary for the band |
| `Attack` | Attack time in seconds (how fast expansion engages) |
| `Decay` | Decay time in seconds (how fast expansion releases) |
| `SoftKnee` | Knee radius in dB (smoother = more transparent) |
| `ScalePercent` | Expansion scaling (100 = base, 105 = +5% more expansion) |

### FFmpeg Filter Specification

```
mcompand=args=\
0.006,0.095 6 -90/-106,-75/-91,-50/-50,-30/-30,0/0 100 | \
0.005,0.100 8 -90/-106,-75/-91,-50/-50,-30/-30,0/0 300 | \
0.005,0.100 10 -90/-107,-75/-92,-50/-50,-30/-30,0/0 800 | \
0.005,0.100 12 -90/-106,-75/-92,-50/-50,-30/-30,0/0 3300 | \
0.002,0.085 10 -90/-106,-75/-91,-50/-50,-30/-30,0/0 8000 | \
0.002,0.080 6 -90/-105,-75/-90,-50/-50,-30/-30,0/0 20500,\
volume=1.3dB:precision=double
```

**Parameter breakdown (per band):**
- `attack,decay`: Time constants in seconds
- `soft-knee`: dB radius for smooth transitions
- `points`: Transfer curve (input/output pairs defining the expansion)
- `crossover`: Upper frequency boundary in Hz

**Makeup gain note:** FFmpeg's mcompand has a bug preventing inline gain parameters. The `volume` filter compensates for ~1.3 dB loss from Linkwitz-Riley crossover filters.

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

## Hardware vs Software: Where We Compromise

No software implementation perfectly replicates analogue hardware. Here's where Jivetalking's Dolby SR diverges from the original-and why these compromises are justified:

| Aspect | Original SR | Jivetalking | Justification |
|--------|-------------|-------------|---------------|
| Band count | 10 (5 fixed + 5 sliding) | 6 fixed | Voice-focused application needs fewer bands |
| Sliding bands | Dynamic centre frequencies | Fixed crossovers | Sliding bands solved tape speed variation-irrelevant for digital |
| Encode/decode | Two-stage compander | Expansion only | We're not encoding to tape; expansion alone reduces noise |
| Threshold | Variable per-band | Fixed -50 dB | Podcast speech has consistent dynamics; fixed threshold is reliable |
| Analogue character | Transformer/circuit coloration | Transparent | Preserving voice authenticity matters more than "warmth" |

The compromises align with our design philosophy: **transparent noise reduction for speech, not vintage character emulation**. Dolby SR's genius was invisibility-we honour that by prioritising the same outcome over slavish hardware mimicry.

---

## References

- Dolby Laboratories, "The Dolby SR Process" (1986)
- Ray Dolby, "Spectral Recording: A New Approach to Professional Noise Reduction" (1987)
- FFmpeg Documentation: `mcompand` filter
