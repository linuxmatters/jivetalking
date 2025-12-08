# Drawmer DS201 — The Gate That Changed Everything

> *"I could solve his problem."*
> — Ivor Drawmer, watching a frustrated engineer battle cymbal bleed on gated toms

## The Legend

In 1982, a self-taught electronics engineer from a tiny Channel Island with no cars and no streetlights revolutionised professional audio. Ivor Drawmer had been a session keyboardist—the kind who carried a soldering iron on stage and once sawed a Hammond organ in half for easier transport. That resourcefulness led him to invent **frequency-conscious gating**.

The story goes: Ivor was waiting to record a keyboard overdub while an engineer wrestled with cymbal bleed on tom mics. Every crash triggered the gates, unleashing a gnashing mess of unwanted sound. Ivor returned the next day with a circuit board, two dangling jacks, and a solution. By filtering the side-chain to hear only low frequencies—which toms have plenty of and cymbals almost none—the gate became surgical.

That prototype became the **Drawmer DS201 Dual Noise Gate**. Over forty years later, it remains in production and installed in virtually every major recording studio, broadcast facility, and live venue worldwide.

## DS201 Specifications

| Parameter | Range | Innovation |
|-----------|-------|------------|
| **Attack** | 10µs–1s | Microsecond response preserves natural transients |
| **Hold** | 2ms–2s | Keeps gate open after signal drops |
| **Decay** | 2ms–4s | Smooth fade-out after hold expires |
| **Range** | 0–90dB | Full mute to subtle reduction |
| **HP Filter** | 25Hz–4kHz | Side-chain high-pass |
| **LP Filter** | 250Hz–35kHz | Side-chain low-pass |

The DS201's genius was the **four-stage envelope** (Attack → Hold → Decay → Range) combined with **frequency-conscious triggering**. An engineer could make the gate deaf to rumble, blind to cymbals, and responsive only to the frequencies that mattered.

## Our Implementation

Jivetalking honours the DS201's philosophy while adapting it for podcast speech. Where the DS201 excels at surgical drum gating, we've tuned our implementation for the gentler demands of the human voice.

### Frequency-Conscious Filtering

FFmpeg lacks native side-chain filtering, so we apply frequency shaping to the audio path before gating—achieving the same result through different means:

| DS201 Feature | Jivetalking Equivalent |
|---------------|------------------------|
| HP side-chain (25Hz–4kHz) | High-pass filter (60–120Hz adaptive) + mains hum notch |
| LP side-chain (250Hz–35kHz) | Low-pass filter (8–16kHz adaptive, disabled by default) |
| Key Listen | Pass 1 [spectral analysis](Spectral%20Analysis.md) guides all decisions |

The high-pass removes subsonic rumble that would hold a gate open. The notch filter surgically removes 50/60Hz mains hum and up to four harmonics. The low-pass—enabled only when spectral analysis detects ultrasonic noise—prevents false triggers from high-frequency interference.

### Soft Expander Philosophy

Here we intentionally depart from the DS201's hard gate mode. For drums, complete silence between hits is desirable. For speech, it sounds unnatural—listeners expect room tone between phrases.

| Aspect | DS201 Hard Gate | Jivetalking |
|--------|-----------------|-------------|
| **Ratio** | ∞:1 (complete mute) | 1.5:1–2.5:1 (soft expansion) |
| **Knee** | Sharp | Soft (2–5dB) |
| **Range** | Up to 90dB | 12–36dB |
| **Character** | Absolute silence | Natural fade preserving room tone |

### Adaptive Parameters

The DS201 requires manual adjustment. We measure your audio in Pass 1 and tune every parameter automatically:

| Parameter | Adaptation Logic | Range |
|-----------|------------------|-------|
| **Threshold** | Noise floor + headroom for severity | -50dB to -25dB |
| **Ratio** | Loudness range (expressive → gentle) | 1.5:1–2.5:1 |
| **Attack** | Transient sharpness indicators | 0.5–17ms |
| **Release** | Spectral flux + noise character | 150–500ms |
| **Range** | Silence entropy (tonal → gentle) | -12dB to -36dB |
| **Knee** | Spectral crest (dynamic → soft) | 2–5dB |
| **Detection** | Noise character | RMS or Peak |

### Attack: Preserving Transients

The DS201's 10µs attack is legendary for preserving the crack of a snare or the punch of a kick. Speech transients are gentler but still matter—the "P" in "podcast" needs its plosive intact.

| Transient Type | Attack Time | Detection |
|----------------|-------------|-----------|
| Extreme plosives | 0.5ms | MaxDifference >40% or SpectralCrest >40dB |
| Sharp consonants | 7ms | MaxDifference >25% |
| Normal speech | 12ms | Moderate transients |
| Soft delivery | 17ms | MaxDifference <10% |

### Hold Compensation

The DS201's dedicated Hold parameter keeps the gate open briefly after signal drops—essential for preventing chatter on decaying toms. FFmpeg's `agate` lacks this control, so we compensate through release timing:

- **Baseline**: +50ms added to release (simulates short hold)
- **Tonal noise**: +75ms additional (hides pumping on hum/bleed)
- **Result**: 150–500ms effective release vs DS201's 2ms–4s decay

## Design Decisions

| Feature | DS201 | Jivetalking | Rationale |
|---------|-------|-------------|-----------|
| Frequency filtering | Side-chain | Audio path | FFmpeg limitation; same result |
| Ultra-fast attack | 10µs | 500µs | Sufficient for speech transients |
| Hold parameter | Native | Release compensation | FFmpeg limitation; effective workaround |
| Hard gate | Available | Never used | Unnatural for speech |
| Manual tuning | Required | Automatic | Zero-knowledge operation |

## The Drawmer Legacy

Ivor Drawmer received the APRS Lifetime Technical Achievement Award, presented by Sir George Martin. His innovations—frequency-conscious gating, program-adaptive dynamics, spectral enhancement—shaped how every competitor designs signal processors. The DS201 alone has been in continuous production for over forty years.

Not bad for a self-taught pianist from a sleepy island in the English Channel.

---

*References: [Drawmer DS201](https://drawmer.com/products/pro-series/ds201.php) • [Drawmer Company History](https://drawmer.com/company.php) • FFmpeg [agate](https://ffmpeg.org/ffmpeg-filters.html#agate), [highpass](https://ffmpeg.org/ffmpeg-filters.html#highpass), [lowpass](https://ffmpeg.org/ffmpeg-filters.html#lowpass), [bandreject](https://ffmpeg.org/ffmpeg-filters.html#bandreject)*
