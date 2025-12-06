# DS201-Inspired Frequency-Conscious Gate

This document describes Jivetalking's gate implementation, inspired by the legendary **Drawmer DS201 Dual Noise Gate**—the industry-standard hardware gate since 1982.

## The Drawmer DS201

The DS201 pioneered several innovations that became standard in professional gating:

### Key DS201 Characteristics

| Parameter | DS201 Range | Notes |
|-----------|-------------|-------|
| **Attack** | 10µs – 1 sec | Microsecond-level attack preserves natural transients |
| **Hold** | 2ms – 2 sec | Keeps gate open after signal drops below threshold |
| **Decay** | 2ms – 4 sec | Smooth fade-out rate after hold expires |
| **Threshold** | +20dB to -54dB | Wide range for various signal levels |
| **Range** | Up to 90dB | Full mute capability |
| **HP Filter** | 25Hz – 4kHz | Side-chain high-pass for frequency-conscious gating |
| **LP Filter** | 250Hz – 35kHz | Side-chain low-pass for frequency-conscious gating |

### DS201 Innovations

1. **Frequency-conscious gating** — Variable HP/LP filters on the side-chain allow the gate to respond only to specific frequency ranges, preventing false triggers from bleed or rumble.

2. **Ultra-fast attack** — Opens in microseconds to preserve the natural attack of transients, crucial for drums and percussive speech consonants.

3. **Four-stage envelope** — Attack, Hold, Decay, Range provides precise control over gate behaviour.

4. **Key Listen** — Monitor the filtered side-chain to tune filter settings.

## Jivetalking's DS201-Inspired Implementation

We implement the DS201's philosophy using FFmpeg's filters, with adaptations optimised for spoken word.

### Architecture: Frequency-Conscious Filtering

Rather than side-chain filtering (which FFmpeg doesn't support), we apply frequency filtering to the audio path before gating:

```
DS201HighPass → DS201LowPass → [denoise] → DS201Gate
     ↓              ↓                          ↓
  HP + Hum       LP filter              Soft expander
   notch        (adaptive)            (speech-optimised)
```

This achieves the same goal: the gate sees a frequency-filtered signal, preventing false triggers from:
- **Low-frequency rumble** (handled by high-pass)
- **Mains hum** (handled by notch filters at 50/60Hz harmonics)
- **Ultrasonic noise** (handled by low-pass)

### DS201HighPass: Combined HP + Hum Rejection

Bundles two DS201-inspired filters:

1. **High-pass filter** — Removes subsonic rumble that could hold the gate open
   - Adaptive frequency: 60–120Hz based on voice character
   - Protects warm voices with gentler slopes and mix blending

2. **Mains hum notch** — Surgical removal of tonal hum
   - 50Hz (UK/EU) or 60Hz (US) fundamental
   - Up to 4 harmonics with 0.3–2Hz notch width
   - Enabled adaptively when silence entropy indicates tonal noise

### DS201LowPass: Ultrasonic Rejection

Removes high-frequency content that could cause false triggers:

- **Adaptive tuning** based on SpectralRolloff:
  - Rolloff < 8kHz → disabled (voice already dark)
  - Rolloff > 14kHz → enabled at rolloff + 2kHz
  - High ZCR + low centroid → possible HF noise, enable at 12kHz
- **Conservative approach** — never cuts below 8kHz to preserve sibilance and air
- **Default: disabled** — only activates when measurements indicate benefit

### DS201Gate: Soft Expander

Here we intentionally depart from the DS201's hard gate behaviour.

#### Why a Soft Expander?

The DS201 offers two modes:
- **Hard gate** — Ultra-fast, clean cut, ideal for drums
- **Soft gate** — Gentler expansion for vocals and mixes

For podcast speech, we exclusively use a soft expander approach:

| Aspect | DS201 Hard Gate | Jivetalking DS201Gate |
|--------|-----------------|----------------------|
| **Ratio** | ∞:1 (complete mute) | 1.5:1 – 2.5:1 (gentle reduction) |
| **Knee** | Sharp | Soft (2–5 dB) |
| **Range** | Up to 90dB | -12dB to -36dB |
| **Character** | Absolute silence | Natural fade |

**Rationale:** Hard gating on speech creates unnatural "pumping" artifacts. A soft expander reduces noise between phrases while maintaining the natural room tone that listeners expect.

#### Adaptive Attack Timing

The DS201's microsecond attack is legendary for preserving transients. We implement adaptive attack based on measured transient characteristics:

| Condition | Attack Time | Use Case |
|-----------|-------------|----------|
| MaxDifference > 40% OR SpectralCrest > 40dB | 0.5ms | Extreme transients (plosives) |
| MaxDifference > 25% OR SpectralCrest > 30dB | 3ms | Sharp consonants |
| MaxDifference > 10% | 7ms | Normal speech |
| Soft delivery | 15ms | Gentle fade-in |

**Measurements used:**
- `MaxDifference` — Sample-to-sample change, catches plosives ("P", "T", "K")
- `SpectralCrest` — Spectral peak-to-RMS ratio, indicates transient energy
- `SpectralFlux` — Frame-to-frame spectral change, biases toward faster attack

#### Hold Compensation

The DS201 has a dedicated Hold parameter; FFmpeg's `agate` does not. We compensate by:
- Adding 50ms to the release time as baseline hold compensation
- Adding 75ms extra for tonal noise (hides pumping artifacts)
- Release range: 150–500ms (vs DS201's 2ms–4s decay)

#### Adaptive Parameters

All gate parameters adapt to Pass 1 measurements:

| Parameter | Adaptation Logic |
|-----------|------------------|
| **Threshold** | Based on noise floor + silence peak, with headroom for noise severity |
| **Ratio** | Based on LRA (wide dynamics → gentle ratio to preserve expression) |
| **Attack** | Based on MaxDifference + SpectralCrest + SpectralFlux |
| **Release** | Based on SpectralFlux + ZCR + noise character |
| **Range** | Based on silence entropy (tonal → gentle, broadband → aggressive) |
| **Knee** | Based on SpectralCrest (dynamic → soft knee) |
| **Detection** | RMS for tonal bleed, peak for clean recordings |

## Comparison Summary

| Feature | DS201 | Jivetalking |
|---------|-------|-------------|
| **Frequency-conscious filtering** | ✅ HP/LP on side-chain | ✅ HP/LP on audio path |
| **Ultra-fast attack** | ✅ 10µs minimum | ✅ 500µs minimum |
| **Hold parameter** | ✅ 2ms–2s | ⚠️ Compensated via release |
| **Hard gate mode** | ✅ | ❌ (soft expander only) |
| **Soft gate mode** | ✅ | ✅ (always) |
| **Adaptive parameters** | ❌ (manual) | ✅ (measurement-driven) |
| **Detection modes** | Not specified | ✅ RMS/Peak adaptive |

## References

- [Drawmer DS201 Product Page](https://drawmer.com/products/pro-series/ds201.php)
- [Drawmer DS201 Operator's Manual](https://drawmer.com/uploads/manuals/ds201_operators_manual.pdf)
- FFmpeg [agate filter documentation](https://ffmpeg.org/ffmpeg-filters.html#agate)
- FFmpeg [highpass](https://ffmpeg.org/ffmpeg-filters.html#highpass) / [lowpass](https://ffmpeg.org/ffmpeg-filters.html#lowpass) / [bandreject](https://ffmpeg.org/ffmpeg-filters.html#bandreject) filter documentation
