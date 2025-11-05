# Jivetalking 🕺

**Professional podcast audio pre-processor that transforms raw voice recordings into broadcast-ready audio.**

Jivetalking processes spoken word audio through a scientifically-tuned filter chain (noise reduction, gate, compression, loudness normalization) to achieve the -16 LUFS podcast standard. Zero audio processing knowledge required – just run and get perfect results.

## Quick Start

```bash
jivetalking presenter1.flac presenter2.flac presenter3.flac
```

## What Jivetalking Does

Raw microphone recordings are messy: room rumble, background hiss, awkward silences, inconsistent volume, and harsh sibilance. Jivetalking fixes all of this automatically.

**The Processing Pipeline:**

1. **High-pass filter** (80Hz) – Removes low-frequency rumble from HVAC, traffic, and handling noise that muddies your audio
2. **Noise reduction** – Intelligently removes background hiss while preserving voice clarity using adaptive spectral analysis
3. **Silence gate** – Cuts dead air and room tone during pauses, keeping your podcast tight and professional
4. **Compression** – Smooths out volume inconsistencies so whispers and emphasis have similar energy levels
5. **De-esser** – Tames harsh 's' and 'sh' sounds that can pierce listeners' ears, especially on headphones
6. **Loudness normalisation** – Matches the -16 LUFS podcast standard used by Spotify, Apple Podcasts, and YouTube
7. **True peak limiter** – Safety net preventing any clipping or distortion in the final output

**Why this order matters:** Each filter prepares the audio for the next. Rumble removal happens before spectral analysis to prevent artefacts. Compression happens before de-essing because it emphasises sibilance. Loudness normalisation happens last so it sees the fully processed signal, with the limiter catching any rare peaks.

**The result:** Broadcast-quality audio that sounds professional on any platform, from laptop speakers to studio monitors. Your voice, just cleaner and more consistent.

## Specification

Here is the detailed project specification and implementation plan:

- [SPECIFICATION.md](docs/SPECIFICATION.md)
