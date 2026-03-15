# Jivetalking 🕺

Raw microphone recordings into broadcast-ready audio in one command. No configuration, and no surprises.

```bash
jivetalking presenter1.flac presenter2.flac
```

Your files emerge at -16 LUFS, a common podcast target, with room rumble, background hiss, clicks, and harsh sibilance sorted automatically. Everything needed is embedded in the binary. This is not how audio tools usually work, and that is rather the point.

---

## The Four-Pass Pipeline

Jivetalking treats audio processing as measurement science, not guesswork. It analyses your recording first, then adapts every filter parameter to match. A dark-voiced narrator gets gentler de-essing. Pre-compressed audio gets lighter compression. Noisy home offices get different treatment than clean studios.

### Pass 1: Analysis

Measures everything that matters:

- **Loudness:** Integrated LUFS, true peak, loudness range (EBU R128)
- **Noise profile:** Floor level and spectral signature
- **Speech characteristics:** RMS level, crest factor, and spectral traits when speech is detected
- **Dynamic behaviour:** Kurtosis and spectral flux for transient analysis

### Pass 2: Adaptive Processing

Filter chain inspired by studio legends, tuned to your specific audio:

| Filter | Hardware Inspiration | What It Does |
|--------|---------------------|--------------|
| **Highpass** | Drawmer DS201 | Removes subsonic rumble (60-120 Hz, adaptive to spectral content) |
| **Noise reduction** | Non-Local Means | Adaptive anlmdn denoiser; compand residual suppression added when a noise profile is available |
| **Gate** | DS201 expander | Soft expansion for natural inter-phrase cleanup; breath reduction option positions threshold between noise floor and quiet speech level |
| **Compressor** | Teletronix LA-2A | Programme-dependent optical compression; ratio and release adapt to kurtosis and flux. High-crest override pushes ratio, threshold, release, and knee when predicted limiter ceiling deficit is positive |
| **De-esser** | — | Adaptive intensity (0.0-0.6) based on spectral centroid and rolloff |

### Pass 3 & 4: Loudness Normalisation

Two-stage EBU R128 normalisation with a CBS Volumax-inspired twist:

1. **Pre-gain** (when needed) applies static gain to raise very quiet recordings whose ideal limiter ceiling falls below the alimiter's -24.0 dBTP minimum, closing the deficit so the limiter can operate at a viable ceiling
2. **Limiter** creates headroom by reducing true peaks
3. **Loudnorm** applies linear gain to reach -16 LUFS without clipping or dynamic processing

This order matters. The limiter provides breathing room so loudnorm can use its transparent linear mode rather than falling back to dynamic compression. When the limiter ceiling is clamped, the pre-gain volume filter raises the signal first so the limiter can use a re-derived ceiling instead of the clamped minimum.

### Why This Order Matters

Each filter prepares audio for the next:

1. **Rumble removal before denoising** — prevents low-frequency artifacts confusing noise profiling
2. **Denoising before gating** — lowers noise floor so gate threshold can be optimal
3. **Gating before compression** — removes silence before dynamics processing amplifies room tone
4. **Compression before de-essing** — compression emphasises sibilance; de-essing corrects it
5. **Normalisation last** — sees fully processed signal for accurate loudness targeting

---

## Installation

Single binary. Zero external dependencies. FFmpeg is embedded via ffmpeg-statigo.

### bin (Recommended)

Install with [bin](https://github.com/marcosnils/bin), a GitHub-aware binary manager:

```bash
bin install github.com/linuxmatters/jivetalking
```

This picks the correct platform and architecture, drops the binary into `~/.local/bin/`, and handles updates via `bin update`. No root required, no path wrangling.

### Manual Download

Fetch from the [releases page](https://github.com/linuxmatters/jivetalking/releases):

```bash
# Linux amd64
chmod +x jivetalking-linux-amd64
mv jivetalking-linux-amd64 ~/.local/bin/jivetalking

# Linux arm64
chmod +x jivetalking-linux-arm64
mv jivetalking-linux-arm64 ~/.local/bin/jivetalking

# macOS Intel
chmod +x jivetalking-darwin-amd64
mv jivetalking-darwin-amd64 ~/.local/bin/jivetalking

# macOS Apple Silicon
chmod +x jivetalking-darwin-arm64
mv jivetalking-darwin-arm64 ~/.local/bin/jivetalking
```

---

## Usage

```bash
jivetalking [flags] <files...>
```

### Flags

| Flag | Description |
|------|-------------|
| `-v, --version` | Show version and exit |
| `-a, --analysis-only` | Run analysis only (Pass 1), display results, skip processing |
| `-d, --debug` | Enable debug logging to `jivetalking-debug.log` |
| `--logs` | Save detailed analysis reports alongside output |


### Examples

```bash
# Process multiple presenters
jivetalking presenter1.flac presenter2.flac presenter3.flac

# Inspect recordings without processing
jivetalking -a presenter1.flac presenter2.flac

# Debug a problematic recording
jivetalking -d --logs troublesome-recording.flac

# Process all FLAC files in directory
jivetalking *.flac
```

### Analysis-Only Mode

Pass `-a` to run only Pass 1 analysis, printing a detailed report to the console without creating any output files. Useful for quickly understanding what jivetalking sees in your recordings, diagnosing setup problems, or checking whether a file needs processing at all.

The report covers:

- **Loudness & dynamics** — integrated LUFS, true peak, loudness range, crest factor
- **Silence & speech detection** — candidate regions scored and elected for noise profiling and speech-aware metrics; voice-activated recording detected automatically (Riverside, Zencastr)
- **Derived measurements** — noise floor, gate baseline, noise-to-speech headroom
- **Filter adaptation** — the exact parameters jivetalking would apply: highpass frequency, gate threshold, NR settings, de-esser intensity, LA-2A configuration
- **Spectral summary** — full spectral characterisation with human-readable interpretations
- **Recording tips** — actionable advice based on your measurements (e.g. "increase your microphone gain by 14 dB" or "your recording is clipping")

Example output (trimmed):

```
======================================================================
ANALYSIS: presenter1.flac
======================================================================
Duration:    5m 23s
Sample Rate: 48000 Hz
Channels:    mono

LOUDNESS
  Integrated:     -32.4 LUFS
  True Peak:      -8.1 dBTP
  Loudness Range: 18.2 LU

DERIVED MEASUREMENTS
  Noise Floor:    -52.3 dBFS (from elected silence)
  Gate Baseline:  -46.0 dB (noise floor + margin)
  NR Headroom:    19.9 dB (noise-to-speech gap)

FILTER ADAPTATION
  Highpass:       80 Hz (from spectral analysis)
  Gate Threshold: -40.2 dB (with breath reduction)
  Gate Ratio:     2.0:1
  NR Threshold:   -47 dB
  NR Expansion:   8 dB
  De-esser:       32% intensity
  LA-2A Thresh:   -28 dB
  LA-2A Ratio:    3.2:1

RECORDING TIPS
  ⚠ Your recording is a bit quiet - increasing your microphone gain
    by about 14 dB would improve quality
```

Output files are named with the measured LUFS value: `recording.flac` becomes `recording-LUFS-16-processed.flac`.

---

## The Typical Workflow

```
Record → Process → Edit → Finalise
  │         │         │         │
  │         │         │         └─ Export at -16 LUFS (dual-mono)
  │         │         │
  │         │         └─ Import to Audacity, top/tail, mix to mono
  │         │
  │         └─ $ jivetalking *.flac (-16 LUFS, matched levels)
  │
  └─ Each presenter records separately, exports FLAC
```

**Include 10-15 seconds of silence somewhere in your recording.** Just sit quietly and let the room breathe - at the start, between sections, or at the end. Jivetalking scans the entire file to find the cleanest quiet section for building a noise profile, which drives the adaptive noise reduction in Pass 2. Without a clean quiet section, the NR compander is disabled entirely and only the self-adapting spectral denoiser runs.

---

## Development

Requires Go, Nix, and a tolerance for CGO.

```bash
# Enter development shell (FFmpeg dependencies provided)
nix develop

# Initialise submodules (ffmpeg-statigo provides embedded FFmpeg)
just setup

# Download static FFmpeg libraries
cd third_party/ffmpeg-statigo && go run ./cmd/download-lib

# Build (never use go build directly - requires CGO + version injection)
just build

# Run tests
just test

# Install to ~/.local/bin
just install
```

### Project Structure

```
cmd/jivetalking/main.go     # CLI entry, Kong flags, Bubbletea TUI
internal/
├── audio/reader.go         # FFmpeg demuxer/decoder wrapper
├── processor/
│   ├── analyzer.go         # Pass 1: ebur128 + astats + aspectralstats
│   ├── processor.go        # Pass 2: adaptive filter chain execution
│   ├── filters.go          # FilterChainConfig, BuildFilterSpec()
│   └── adaptive.go         # Measurement-driven parameter tuning
├── ui/                     # Bubbletea model, views, messages
└── cli/                    # Help styling, version output
```

### Design Documentation

- [Gate: Drawmer DS201](docs/FilterGate-Drawmer%20DS201.md) — Soft expander with adaptive thresholding
- [Compressor: LA-2A](docs/FilterCompressor-Teletronix%20LA-2A.md) — Programme-dependent optical compression
- [Limiter: CBS Volumax](docs/FilterLimiter-CBS-Volumax.md) — Transparent broadcast limiting
- [Spectral Metrics Reference](docs/Spectral-Metrics-Reference.md) — How measurements drive adaptation

See [AGENTS.md](AGENTS.md) for complete development guidelines, architecture details, and contribution standards.

---

## Contributing

```bash
# Run tests before committing
just test
```

- Follow [Conventional Commits](https://www.conventionalcommits.org/) format
- Use `just build` for any releases (CGO + version injection required)
- GitHub Actions builds binaries for linux-amd64, linux-arm64, darwin-amd64, darwin-arm64 automatically
