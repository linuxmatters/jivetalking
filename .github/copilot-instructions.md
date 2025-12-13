# Jivetalking Copilot Instructions

## Project Overview

Go CLI tool for podcast audio preprocessing. Transforms raw voice recordings into broadcast-ready audio at -16 LUFS using a two-pass adaptive filter chain. Uses [Charm Bubbletea](https://github.com/charmbracelet/bubbletea) for the TUI and embeds FFmpeg via the `ffmpeg-statigo` submodule.

## Architecture

```
cmd/jivetalking/main.go     # CLI entry, Kong flags, starts TUI + processing goroutine
internal/
├── audio/reader.go         # FFmpeg demuxer/decoder wrapper
├── processor/
│   ├── analyzer.go         # Pass 1: ebur128 + astats + aspectralstats analysis
│   ├── processor.go        # Pass 2: adaptive filter chain execution
│   ├── filters.go          # FilterChainConfig, BuildFilterSpec(), defaults
│   └── models/cb.rnnn      # Embedded RNN model for arnndn noise reduction
├── ui/                     # Bubbletea model, views, messages
└── cli/                    # Help styling, version output
```

**Data flow:** `main.go` → spawns goroutine → `ProcessAudio()` → Pass 1 (`AnalyzeAudio`) → Pass 2 (applies filters) → sends `ui.*Msg` to TUI via `tea.Program.Send()`.

## Critical Build Rules

**Always use `just build`**—never `go build` directly. The justfile handles:
1. Checking ffmpeg-statigo submodule is initialised with library downloaded
2. CGO_ENABLED=1 with version injection via `-ldflags`

Setup from scratch: `just setup` (initialises submodule, downloads static FFmpeg library).

## Key Commands

| Command | Purpose |
|---------|---------|
| `just setup` | Initialise submodule, download ffmpeg-statigo library |
| `just build` | Build binary with version info |
| `just test` | Run tests |

## FFmpeg Integration

Uses `github.com/linuxmatters/ffmpeg-statigo` as a **git submodule** in `third_party/ffmpeg-statigo/`. The `go.mod` replace directive points there.

Key patterns from ffmpeg-statigo:
- All FFmpeg types prefixed with `AV*` (e.g., `AVCodecContext`, `AVFrame`)
- Use `ffmpeg.ToCStr()` for C string conversion—call `.Free()` when done
- Wrap FFmpeg return codes with `WrapErr()` to convert to Go errors
- Check `AVErrorEOF` and `EAgain` for stream processing loops

## Audio Processing Pipeline

**Two-pass architecture:**
1. **Pass 1 (Analysis):** Measures LUFS, true peak, LRA, noise floor, spectral characteristics
2. **Pass 2 (Processing):** Applies adaptive filter chain tuned to measurements

**Filter chain order (intentional):**
```
highpass → adeclick → dolby-sr-single → arnndn → agate → acompressor → deesser → alimiter
```

Each filter prepares audio for the next. Rumble removal before spectral analysis. Compression before de-essing (compression emphasises sibilance). Limiter provides final brick-wall safety.

## Adaptive Processing Patterns

Filter parameters adapt based on Pass 1 measurements. See `adaptive.go`:

- **Highpass frequency:** 60-120Hz based on spectral centroid and LUFS gap
- **Dolby SR Single:** Adaptive afftdn (2-6dB) for subtle noise reduction
- **De-esser intensity:** 0.0-0.6 based on spectral centroid + rolloff
- **Gate threshold:** Derived from measured noise floor

## Testing

Tests require audio files in `testdata/` (gitignored). The processor tests skip gracefully if files are missing:
```go
if _, err := os.Stat(testFile); os.IsNotExist(err) {
    t.Skipf("Test file not found: %s", testFile)
}
```

## TUI Message Protocol

Processing goroutine communicates with Bubbletea via typed messages:
- `ui.FileStartMsg` — signals file processing started
- `ui.ProgressMsg` — pass number, progress (0.0-1.0), current level, measurements
- `ui.FileCompleteMsg` — processing finished with result
- `ui.FileErrorMsg` — processing failed with error

## Environment

- NixOS development shell via `flake.nix`
- Fish shell for terminal commands
- CGO required (`CGO_ENABLED=1`)
- Tools: `just`, `ffmpeg` (for testing), `mediainfo`, `vhs` (for demo recordings)
