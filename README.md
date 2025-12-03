# Jivetalking 🕺

**Professional podcast audio pre-processor that transforms raw voice recordings into broadcast-ready audio.**

Jivetalking processes spoken word audio through a scientifically-tuned filter chain (noise reduction, gate, compression, loudness normalization) to achieve the -16 LUFS podcast standard. Zero audio processing knowledge required – just run and get perfect results.

## Quick Start

```bash
jivetalking presenter1.flac presenter2.flac presenter3.flac
```

## What Jivetalking Does

Raw microphone recordings are messy: room rumble, background hiss, awkward silences, inconsistent volume, and harsh sibilance. Jivetalking fixes all of this automatically.

### The Processing Pipeline

Jivetalking uses a **two-pass architecture**. Pass 1 analyzes your audio (loudness, noise floor, dynamic range, spectral characteristics), then Pass 2 applies adaptive processing tuned to your specific recording.

#### Pass 1: Analysis
- Measures integrated loudness, true peak, and loudness range
- Analyzes noise floor and dynamic range for adaptive gating and compression
- Examines spectral content (centroid and rolloff) for intelligent high-pass and de-essing

#### Pass 2: Adaptive Processing

1. **High-pass filter** (60-100Hz, adaptive) – Removes low-frequency rumble. Cutoff adapts to voice characteristics: lower for warm voices, higher for bright voices
2. **Click/pop removal** – Eliminates mouth clicks and plosive artifacts using autoregressive modeling
3. **Noise reduction** – Intelligently removes background hiss while preserving voice clarity using adaptive FFT spectral subtraction
4. **Noise gate** (adaptive threshold) – Cuts dead air and room tone. Threshold adapts to measured noise floor (typically 6-10dB above)
5. **Compression** (adaptive ratio/threshold) – Smooths volume inconsistencies. Settings adapt to dynamic range: gentle for expressive content, aggressive for already-compressed audio
6. **De-esser** (adaptive intensity) – Tames harsh sibilance. Intensity adapts to spectral characteristics: disabled for voices lacking high-frequency content, increased for bright sibilant voices
7. **Loudness normalisation** – Two-pass EBU R128 normalization to -16 LUFS podcast standard (Spotify, Apple Podcasts, YouTube)
8. **True peak limiter** – Safety net preventing clipping or distortion

**Why this order matters:** Each filter prepares the audio for the next. Rumble removal happens before spectral analysis to prevent artifacts. Compression happens before de-essing because it emphasizes sibilance. Loudness normalization happens last so it sees the fully processed signal, with the limiter catching any rare peaks.

**Why adaptive matters:** A dark-voiced narrator doesn't need aggressive de-essing. Pre-compressed audio doesn't need heavy compression. Clean studio recordings need different gating than noisy home offices. Jivetalking measures your specific audio and adapts every filter automatically.

**The result:** Broadcast-quality audio that sounds professional on any platform, from laptop speakers to studio monitors. Your voice, just cleaner and more consistent.

## Development Setup

```bash
# Clone with submodules
git clone --recursive https://github.com/linuxmatters/jivetalking

# Or if already cloned
just setup
```

## Specification

Here is the detailed project specification and implementation plan:

- [SPECIFICATION.md](docs/SPECIFICATION.md)
