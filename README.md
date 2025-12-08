# Jivetalking 🕺

*Professional podcast audio preprocessing—broadcast-quality results with zero audio engineering knowledge required*

---

## What It Does

Raw microphone recordings are messy: room rumble, background hiss, awkward silences, inconsistent volume, harsh sibilance. Jivetalking fixes all of this automatically, transforming raw voice recordings into broadcast-ready audio at the **-16 LUFS podcast standard**.

```bash
jivetalking presenter1.flac presenter2.flac presenter3.flac
```

That's it. No configuration, no knobs to tweak, no audio knowledge required.

---

## Design Philosophy

| Principle | Implementation |
|-----------|----------------|
| **Best outcome by default** | Professional results with zero configuration |
| **Quality over speed** | Two-pass processing for measurement-driven accuracy |
| **Transparency over depth** | Every filter prioritises natural sound |
| **Adaptive everything** | Parameters tune automatically to your specific recording |

---

## The Filter Chain

Jivetalking's processing pipeline draws inspiration from legendary studio hardware: the **Drawmer DS201** noise gate, **Dolby SR** noise reduction, and **Teletronix LA-2A** optical compressor. Each filter in the chain prepares the audio for the next.

### Pass 1: Analysis

Measures your audio's characteristics to drive adaptive processing:

- Integrated loudness, true peak, loudness range (EBU R128)
- Noise floor and silence profile
- [Spectral characteristics](docs/Spectral%20Analysis.md) (centroid, rolloff, kurtosis, skewness)
- Dynamic range and transient sharpness

### Pass 2: Adaptive Processing

| Filter | Inspiration | What It Does |
|--------|-------------|--------------|
| **High-pass** | DS201 side-chain | Removes subsonic rumble (60–100 Hz, adaptive to voice) |
| **Low-pass** | DS201 side-chain | Removes ultrasonic content that triggers false processing |
| **Noise reduction** | Dolby SR | 15-band Bark-scale spectral denoising with voice protection |
| **RNN denoise** | — | AI-based cleanup for complex noise patterns |
| **Gate** | DS201 expander | Soft expansion (2:1–4:1) for natural inter-phrase cleanup |
| **Compressor** | LA-2A | Programme-dependent optical compression with ~10ms attack |
| **De-esser** | — | Tames sibilance (adaptive intensity based on spectral rolloff) |
| **Normalisation** | — | Two-stage loudness targeting to -16 LUFS |
| **Limiter** | — | Brick-wall safety net for true peak compliance |

### Why This Order Matters

Each filter prepares audio for the next:

1. **Rumble removal before spectral analysis** — prevents low-frequency artifacts from confusing noise profiling
2. **Denoising before gating** — lowers the noise floor so the gate threshold can be set optimally
3. **Gating before compression** — removes silence before dynamics processing amplifies room tone
4. **Compression before de-essing** — compression emphasises sibilance; de-essing corrects it
5. **Normalisation last** — sees the fully processed signal for accurate loudness targeting
6. **Limiter as safety net** — catches any rare peaks from upstream processing

### Why Adaptive Matters

A dark-voiced narrator doesn't need aggressive de-essing. Pre-compressed audio doesn't need heavy compression. Clean studio recordings need different gating than noisy home offices.

Jivetalking measures your specific audio and adapts every filter automatically. The DS201-inspired gate tunes its threshold to your measured noise floor. The Dolby SR-inspired denoiser protects voice frequencies based on your spectral characteristics. The LA-2A-inspired compressor adjusts ratio and release based on your dynamic range.

---

## Workflow

```
┌─────────────────────────────────────────────────────────────┐
│  1. Record                                                  │
│     Each presenter records individually → export as FLAC    │
└─────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────┐
│  2. Process                                                 │
│     $ jivetalking *.flac                                    │
│     Output: *-processed.flac (level-matched at -16 LUFS)    │
└─────────────────────────────────────────────────────────────┘
                              ↓
┌─────────────────────────────────────────────────────────────┐
│  3. Edit                                                    │
│     Import processed files into Audacity                    │
│     Files already level-matched—just cut, arrange, export   │
└─────────────────────────────────────────────────────────────┘
```

---

## Installation

Single binary with embedded FFmpeg—no external dependencies.

```bash
# Download the latest release for your platform
# Linux (amd64), macOS (amd64, arm64)
```

---

## Development

```bash
# Clone with submodules (ffmpeg-statigo provides embedded FFmpeg)
git clone --recursive https://github.com/linuxmatters/jivetalking
cd jivetalking

# Or initialise submodules after cloning
just setup

# Build
just build

# Run tests
just test
```

### Project Structure

```
cmd/jivetalking/main.go     # CLI entry point (Kong + Bubbletea)
internal/
├── audio/                  # FFmpeg demuxer/decoder wrapper
├── processor/
│   ├── analyzer.go         # Pass 1: ebur128 + astats + spectral analysis
│   ├── processor.go        # Pass 2: adaptive filter chain execution
│   ├── filters.go          # Filter chain configuration and building
│   └── adaptive.go         # Measurement-driven parameter tuning
└── ui/                     # Bubbletea TUI model and views
```

### Design Documentation

- [FilterGate: Drawmer DS201](docs/FilterGate-Drawmer%20DS201.md) — Soft expander gate with adaptive threshold
- [FilterNoise: Dolby SR](docs/FilterNoise-Dolby%20SR.md) — 15-band voice-protective noise reduction
- [FilterCompressor: LA-2A](docs/FilterCompressor-Teletronix%20LA-2A.md) — Programme-dependent optical compression

---

## License

GPLv3 (required by FFmpeg)
