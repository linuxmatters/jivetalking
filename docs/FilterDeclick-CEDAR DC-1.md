# CEDAR DC-1 — The First Digital Declicker

> *"We don't repair the audio. We reconstruct what should have been there."*
> — CEDAR Audio engineering philosophy

## The Legend

In 1988, a Cambridge startup called CEDAR Audio set out to solve a problem that had plagued archivists since the dawn of recorded sound: how to remove clicks and pops from vinyl and shellac recordings without destroying the music beneath them. The result, unveiled in 1992, was the **CEDAR DC-1**—the world's first dedicated digital declicker.

Before CEDAR, restoration engineers had two choices: live with the damage, or apply so much broadband filtering that the cure was worse than the disease. The DC-1 changed everything. By detecting impulsive noise (clicks lasting microseconds to milliseconds) and reconstructing the underlying audio using autoregressive interpolation, it could surgically remove a pop from the middle of a violin phrase without any audible artefact.

The DC-1 became an instant fixture in broadcast houses, film studios, and national archives worldwide. The BBC, Abbey Road, and the Library of Congress all adopted CEDAR technology. When historic recordings needed restoration—Churchill's wartime speeches, early jazz masters, classical performances on fragile 78s—CEDAR was the tool of choice.

## The Autoregressive Solution

CEDAR's breakthrough was understanding that clicks are **impulsive anomalies** against a **predictable background**. Most audio signals have temporal correlation—what comes next is related to what came before. A click violates this relationship explosively.

### Detection: Finding the Damage

The DC-1's detection algorithm examines the prediction error: how much does each sample deviate from what the surrounding samples suggest it should be?

```
Prediction = AR Model(recent samples)
Error = Actual Sample − Prediction
If Error > Threshold → Mark as click
```

This elegantly distinguishes clicks from legitimate transients. A kick drum is loud but *follows* the audio trajectory. A click arrives *orthogonally* to it—an isolated spike bearing no relationship to its neighbours.

| Signal Type | Prediction Error | Detected? |
|-------------|------------------|-----------|
| Click/pop | Extreme (anomalous) | Yes |
| Kick drum | High (but contextual) | No |
| Sustained note | Low | No |
| Background noise | Low-moderate | No |

### Reconstruction: Filling the Hole

Once a click is detected, CEDAR replaces the corrupted samples using autoregressive (AR) interpolation. The algorithm estimates what *should* have been there based on surrounding undamaged audio.

```
Damaged:     ○ ○ ○ ● ● ● ○ ○ ○   (● = click)
              ↓   ↓       ↓   ↓
AR Model:   uses these to predict
              ↓
Repaired:    ○ ○ ○ ○ ○ ○ ○ ○ ○   (seamless)
```

The AR model order determines reconstruction quality. Higher orders capture more complex signals but risk instability. CEDAR's original DC-1 used carefully tuned model orders optimised for 44.1kHz audio.

### The Sensitivity Trade-off

The DC-1 provided a single "sensitivity" control that balanced detection aggressiveness:

| Sensitivity | Detection Behaviour | Best For |
|-------------|---------------------|----------|
| Low | Only obvious clicks | Pristine recordings with rare damage |
| Medium | Standard clicks and pops | Typical vinyl restoration |
| High | Subtle crackle, micro-clicks | Heavily damaged shellac, 78s |

Too aggressive, and transients get smoothed. Too gentle, and clicks survive. CEDAR's engineers spent years tuning the sweet spot.

---

## The CEDAR Legacy

The DC-1 was superseded by CEDAR's "Series 2" platform in 1994, then by software plugins in the 2000s. Modern CEDAR tools offer "better impulsive noise detection" and integrated dehissing, but the core algorithm—detect anomalies, interpolate repairs—remains conceptually unchanged.

CEDAR's influence extends beyond their own products. The same autoregressive principles now appear in FFmpeg's `adeclick` filter, open-source restoration tools, and countless DAW plugins. Every time a podcaster removes a mouth click or an archivist rescues a crackling 78, CEDAR's pioneering work echoes.

---

## FFmpeg's `adeclick` Filter

FFmpeg's `adeclick` filter implements autoregressive interpolation—directly analogous to CEDAR's DC-1 algorithm.

**Filter specification:**
```
adeclick=window=<ms>:overlap=<pct>:arorder=<pct>:threshold=<n>:burst=<pct>:method=<s|a>
```

| Parameter | Range | Default | Purpose |
|-----------|-------|---------|---------|
| `window` | 10–100 ms | 55 | Analysis window size |
| `overlap` | 50–95% | 75 | Window overlap (higher = more CPU, better results) |
| `arorder` | 0–25% | 2 | AR model order as percentage of window |
| `threshold` | 1–100 | 2 | Detection sensitivity (lower = more aggressive) |
| `burst` | 0–10% | 2 | Maximum burst length as percentage of window |
| `method` | `s` or `a` | `s` | `s` = overlap-save, `a` = overlap-add |

**How parameters map to DC-1:**

| DC-1 Control | `adeclick` Equivalent |
|--------------|----------------------|
| Sensitivity | `threshold` (inverted: lower = more sensitive) |
| — | `window` (larger windows handle longer clicks) |
| — | `arorder` (higher = better reconstruction, more CPU) |

**Algorithm:**
1. Analyse audio in overlapping windows
2. Build AR model from surrounding samples
3. Identify samples with excessive prediction error
4. Reconstruct corrupted samples via AR interpolation
5. Blend repaired segments using overlap-add/save

**Advantages:**
- True autoregressive interpolation (matches CEDAR's approach)
- Fine-grained control over detection/reconstruction
- Handles burst errors (multiple consecutive clicks)
- Low latency suitable for real-time use

**Limitations:**
- Higher AR orders increase CPU load significantly
- Very long clicks (>10ms) may reconstruct poorly
- Cannot remove continuous crackle

## Jivetalking Implementation

The `DC1Declick` filter wraps FFmpeg's `adeclick` with adaptive tuning based on Pass 1 measurements.

### Default Configuration

```
adeclick=window=55:overlap=75:arorder=2:threshold=6:burst=2:method=s
```

Conservative defaults that protect speech transients. The `tuneDC1Declick()` function adjusts these based on measured impulsive content.

### Adaptive Behaviour

Unlike the DS201 gate or LA-2A compressor where adaptive tuning sweeps parameters across a range, declicking benefits from *conditional enabling* with *tiered sensitivity*. Most podcast recordings have no click damage; processing them wastes CPU and risks artefacts on plosives.

#### Enabling Logic (OR-based)

The key insight for podcast audio: **mouth noises have high spectral crest but low MaxDifference**. A lip smack creates impulsive energy (high crest) without the extreme sample-to-sample jumps of vinyl damage. The enabling logic uses OR conditions to catch both scenarios:

| Condition | Threshold | Result |
|-----------|-----------|--------|
| High MaxDiff alone | >25% full scale | Enable (threshold 2) — likely clicks |
| High Crest alone | >50 dB | Enable (threshold 5) — mouth noises |
| Both elevated | MaxDiff >12% AND Crest >35 dB | Enable (threshold 4) — mild clicks |
| Moderate Crest | >35 dB | Enable (threshold 6) — possible clicks |
| Neither | Below thresholds | Disable — clean recording |

**Decision tree:**
```
IF MaxDifference > 0.25:
    → Likely clicks (threshold 2, aggressive)
ELSE IF SpectralCrest > 50dB:
    → Mouth noises (threshold 5, gentle)
ELSE IF MaxDifference > 0.12 AND SpectralCrest > 35dB:
    → Mild clicks (threshold 4)
ELSE IF SpectralCrest > 35dB:
    → Possible clicks (threshold 6, conservative)
ELSE:
    → Disable (clean recording)
```

#### Threshold Adjustments

Additional adjustments protect against false positives:

| Spectral Characteristic | Adjustment | Rationale |
|------------------------|------------|-----------|
| SpectralFlatness >0.3 | +2 (max 6) | Noisy signal, not impulsive clicks |
| DynamicRange <10 dB | +1 (max 6) | Compressed audio, protect dynamics |

#### Window Size Adaptation

Window size adapts to speech characteristics based on `SpectralCentroid`:

| Centroid | Window | Rationale |
|----------|--------|-----------|
| >3000 Hz | 45 ms | Fast speech, crisp articulation |
| 1500–3000 Hz | 55 ms | Balanced (default) |
| <1500 Hz | 70 ms | Bass-heavy, slower transitions |

### Filter Chain Position

Place `DC1Declick` **after denoising**, immediately before the gate:

```
Highpass → Lowpass → DolbySR → Arnndn → DC1Declick → Gate → Compressor → ...
```

**Rationale:**

This positioning differs from classical vinyl restoration (where declicking precedes all processing) for good reason. Podcast mouth clicks behave differently from vinyl damage:

1. **Unmasking effect**: Mouth clicks hide in background noise. When DolbySR and Arnndn reduce the noise floor, clicks become more prominent. Declicking after denoising catches these newly-exposed artifacts.

2. **Better detection contrast**: Post-denoising, clicks have higher prediction error against the quieter, cleaner signal. The AR model sees sharper anomalies.

3. **Cleaner reconstruction**: The AR interpolation uses surrounding samples to predict repairs. Denoised audio provides better "context" for accurate reconstruction.

4. **Gate protection**: The gate immediately follows declicking. Any clicks removed won't be "revealed" by subsequent processing, and the gate sees a click-free signal for open/close decisions.

**Trade-off**: Severe click damage (rare in podcast audio) could theoretically affect DolbySR/Arnndn's spectral analysis. In practice, mouth clicks are low-energy compared to vinyl pops—they pass through denoising essentially unchanged, ready for removal.

---

## Design Decisions

| Feature | CEDAR DC-1 | Jivetalking DC1Declick | Rationale |
|---------|------------|------------------------|-----------|
| Detection | AR prediction error | `adeclick` threshold | Same principle, different parameter name |
| Reconstruction | AR interpolation | `adeclick` AR model | Direct equivalent |
| Sensitivity control | Single knob | Adaptive enable + tiered threshold | Zero-knowledge operation |
| Always-on | Yes (dedicated hardware) | Conditional enable | Avoid unnecessary processing |
| Chain position | First | After denoising | Catches unmasked mouth noises |
| Real-time | Yes | Yes | `adeclick` has low latency |

---

## The CEDAR Philosophy

CEDAR's approach—detect anomalies, reconstruct from context—represents a fundamental insight about audio restoration. The DC-1 didn't fight damage with brute force filtering. It asked: *what should have been here?* And used mathematics to answer.

For podcast preprocessing, most recordings need no declicking at all. But when mouth clicks, lip smacks, or cable pops appear, the `DC1Declick` filter carries forward CEDAR's forty-year-old breakthrough: surgical repair, invisible results.
