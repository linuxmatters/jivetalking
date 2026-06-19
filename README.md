# Jivetalking 🕺

Raw microphone recordings into broadcast-ready audio in one command. No configuration, and no surprises.

```bash
jivetalking LMP-81s-mark.flac LMP-81s-martin.flac LMP-81s-popey.flac
```

Your files emerge at -16 LUFS / -1 dBTP, the loudness standard for spoken-word podcasts, with room rumble, background hiss, clicks, and harsh sibilance sorted automatically. Multiple files process in parallel, each with its own TUI progress row. Everything needed is embedded in the binary. This is not how audio tools usually work, and that is rather the point.

## Example Output

<div align="center"><img alt="Jivetalking Demo" src=".github/jivetalking.gif" width="620" /></div>

---

## The Typical Workflow

```
Record → Process → Edit → Export
  │         │         │        │
  │         │         │        └─ Export
  │         │         │
  │         │         └─ Import to Audacity, top/tail, edit
  │         │
  │         └─ $ jivetalking *.flac (-16 LUFS, matched levels)
  │
  └─ Each presenter records separately, exports FLAC
```

**Include 10-15 seconds of room tone somewhere in your recording.** Just sit quietly and let the room breathe - at the start, between sections, or at the end. Jivetalking scans the entire file to find the cleanest quiet stretch and builds a noise profile from it, used by the noise-reduction and gate stages in Pass 2. The `anlmdn → afftdn` noise reduction runs regardless, so recordings without a clean room-tone section are still denoised.

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

## The Four-Pass Pipeline

Jivetalking treats audio processing as measurement science, not guesswork. It analyses your recording first, then adapts every filter to match. A dark-voiced narrator gets gentler de-essing, pre-compressed audio gets lighter compression, and a noisy home office gets different treatment than a clean studio.

Four passes carry a raw recording to a broadcast-ready master:

1. **Analyse:** measure loudness, noise floor, and speech; detect the room tone.
2. **Process:** run the adapted filter chain.
3. **Measure:** read the processed signal back so normalisation has accurate numbers.
4. **Normalise:** set the final loudness to -16 LUFS / -1 dBTP.

The Pass 2 filter chain, each stage handing the next a cleaner signal:

```text
downmix → rumble high-pass → band-limit low-pass → noise reduction → speech gate → levelling compressor → de-esser → analysis → resample
```

For the full walkthrough, see **[docs/Pipeline.md](docs/Pipeline.md)**: what each stage does, why it sits where it does, how the adaptive tuning works, and how normalisation reaches -16 LUFS honestly, with a diagram.

---

## Quality Ratings

When a file finishes, the completion box shows two star ratings: **Recording** (your source capture, the one that varies) and **Processed** (the output against the -16 LUFS target, almost always five stars). The pair tells the story: a two-star capture taken to a five-star master.

```
Jivetalking 🕺

╭──────────────────────────────────────────╮
│ Processing 3 files, 3 complete, 0 failed │
╰──────────────────────────────────────────╯

 🗸 LMP-83-mark-LUFS-16-processed.flac
╭──────────────────────────────────────────╮
│ Time        02:31  ·  ⚡ 19.0×           │
│ Loudness    -35.2 → -16.1 LUFS  Δ +19.1  │
│ True peak    -6.2 →  -1.7 ㏈TP  Δ  +4.5  │
│ Dynamics     15.0 →  13.3 LU    Δ  -1.7  │
│ Noise floor < -96 ㏈                     │
│ Recording   ★★★★☆  Great                 │
│ Processed   ★★★★★  Excellent             │
╰──────────────────────────────────────────╯
 🗸 LMP-83-martin-LUFS-16-processed.flac
╭──────────────────────────────────────────╮
│ Time        02:38  ·  ⚡ 18.1×           │
│ Loudness    -27.8 → -16.0 LUFS  Δ +11.8  │
│ True peak    -4.5 →  -1.8 ㏈TP  Δ  +2.7  │
│ Dynamics     14.7 →  12.0 LU    Δ  -2.7  │
│ Noise floor -91 ㏈                       │
│ Recording   ★★★★☆  Great                 │
│ Processed   ★★★★★  Excellent             │
╰──────────────────────────────────────────╯
 🗸 LMP-83-popey-LUFS-16-processed.flac
╭──────────────────────────────────────────╮
│ Time        02:43  ·  ⚡ 17.6×           │
│ Loudness    -29.8 → -16.0 LUFS  Δ +13.8  │
│ True peak    -0.1 →  -1.3 ㏈TP  Δ  -1.2  │
│ Dynamics     12.3 →   8.9 LU    Δ  -3.4  │
│ Noise floor -86 ㏈                       │
│ Recording   ★★☆☆☆  Fair                  │
│ Processed   ★★★★★  Excellent             │
╰──────────────────────────────────────────╯
```

See **[docs/Usage.md](docs/Usage.md#quality-ratings)** for the three axes behind the Recording score and what a low star is telling you to fix.

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
| `--diagnostics` | Write extra diagnostic artefacts: before/after spectrogram PNGs plus `.intervals.jsonl`/`.candidates.jsonl` sidecars. Adds extra FFmpeg passes. Off by default |


### Examples

```bash
# Process multiple presenters in parallel (worker count tracks file count)
jivetalking presenter1.flac presenter2.flac presenter3.flac

# Inspect recordings without processing
jivetalking -a presenter1.flac presenter2.flac

# Debug a problematic recording
jivetalking -d troublesome-recording.flac

# Process all FLAC files in directory
jivetalking *.flac

# Emit before/after spectrograms and interval sidecars
jivetalking --diagnostics presenter1.flac
```

Processing always writes a Markdown report next to each processed output. For example, `recording-LUFS-16-processed.flac` gets `recording-LUFS-16-processed.md`. The report is empirical: every measurement and the exact adapted filter parameters, with objective metric definitions and no quality verdicts. Analysis-only runs write `<input>-analysis.md` instead.

### Diagnostics

`--diagnostics` writes before/after spectrogram PNGs and `.intervals.jsonl` / `.candidates.jsonl` sidecars beside the report, for sweeps and side-by-side comparison. It changes no DSP, so the processed audio is byte-identical with the flag on or off.

See **[docs/Usage.md](docs/Usage.md#diagnostics)** for the spectrogram naming scheme and sidecar formats.

### Analysis-Only Mode

Pass `--analysis-only` to run Pass 1 only. It writes `<input>-analysis.md` next to each input and shows the Recording stars plus a one-line gain verdict on screen, without producing any audio. Useful for checking a capture before you commit to a take.

See **[docs/Usage.md](docs/Usage.md#analysis-only-mode)** for what the report covers and how to read the gain-advice thermometer.

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

The full source layout, architecture, and contribution standards live in [AGENTS.md](AGENTS.md).

### Design Documentation

- [Usage Guide](docs/Usage.md): driving Jivetalking in depth: quality ratings, analysis-only mode, and diagnostics
- [Audio Pipeline](docs/Pipeline.md): how and why the processing pipeline is built and tuned, with a diagram
- [The hardware that taught me](docs/Inspiration.md): the influences and heritage behind jivetalking's processing approach
- [Spectral Metrics Reference](docs/Spectral-Metrics-Reference.md): how measurements drive adaptation
- [Normalisation Tuning](docs/Normalisation-Tuning.md): why the loudnorm and limiter constants hold their corpus-derived values
