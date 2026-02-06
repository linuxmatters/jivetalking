# Jivetalking 🕺

Raw microphone recordings into broadcast-ready audio in one command. No configuration, and no surprises.

```bash
jivetalking presenter1.flac presenter2.flac
```

Your files emerge at -18 LUFS, the podcast/broadcast standard, with room rumble, background hiss, clicks, and harsh sibilance sorted automatically. Everything needed is embedded in the binary. This is not how audio tools usually work, and that is rather the point.

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
| **Noise reduction** | Non-Local Means | Adaptive anlmdn denoiser with compand residual suppression |
| **Gate** | DS201 expander | Soft expansion for natural inter-phrase cleanup; breath reduction option positions threshold between noise floor and quiet speech level |
| **Compressor** | Teletronix LA-2A | Programme-dependent optical compression; ratio and release adapt to kurtosis and flux |
| **De-esser** | — | Adaptive intensity (0.0-0.6) based on spectral centroid and rolloff |

### Pass 3 & 4: Loudness Normalisation

Two-stage EBU R128 normalisation with a CBS Volumax-inspired twist:

1. **Limiter** creates headroom by reducing true peaks
2. **Loudnorm** applies linear gain to reach -18 LUFS without clipping or dynamic processing

This order matters. The limiter provides breathing room so loudnorm can use its transparent linear mode rather than falling back to dynamic compression.

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
| `-d, --debug` | Enable debug logging to `jivetalking-debug.log` |
| `--logs` | Save detailed analysis reports alongside output |


### Examples

```bash
# Process multiple presenters
jivetalking presenter1.flac presenter2.flac presenter3.flac

# Debug a problematic recording
jivetalking -d --logs troublesome-recording.flac

# Process all FLAC files in directory
jivetalking *.flac
```

Output files are named with `-processed` suffix: `recording.flac` becomes `recording-processed.flac`.

---

## The Typical Workflow

```
Record → Process → Edit → Finalise
  │         │         │         │
  │         │         │         └─ Export at -16 LUFS (dual-mono)
  │         │         │
  │         │         └─ Import to Audacity, top/tail, mix to mono
  │         │
  │         └─ $ jivetalking *.flac (-18 LUFS, matched levels)
  │
  └─ Each presenter records separately, exports FLAC
```

**Start each recording with 10-15 seconds of silence.** Just sit quietly and let the room breathe. Jivetalking uses this room tone to build a noise profile, which drives the adaptive noise reduction in Pass 2. Without a clean quiet section near the start, the tool has nothing to calibrate against and denoising results will be noticeably worse.

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
