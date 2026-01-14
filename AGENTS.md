# AGENTS.md

## Project overview

Go CLI tool for podcast audio preprocessing using embedded FFmpeg. Transforms raw voice recordings into broadcast-ready audio at -18 LUFS through a four-pass adaptive processing pipeline. Uses [Charm Bubbletea](https://github.com/charmbracelet/bubbletea) for the TUI.

## Setup commands

- Enter development shell: `nix develop` (or let direnv activate automatically)
- Initialise ffmpeg-statigo submodule: `just setup`
- Download FFmpeg static libraries: `cd third_party/ffmpeg-statigo && go run ./cmd/download-lib`

## Build and test commands

- **Build binary:** `just build` (never use `go build` directly—requires CGO + version injection)
- **Run tests:** `just test`
- **Clean artifacts:** `just clean`
- **Install to ~/.local/bin:** `just install`

## Architecture

```
cmd/jivetalking/main.go     # CLI entry, Kong flags, starts TUI + processing goroutine
internal/
├── audio/reader.go         # FFmpeg demuxer/decoder wrapper
├── processor/
│   ├── analyzer.go         # Pass 1: ebur128 + astats + aspectralstats analysis
│   ├── processor.go        # Pass 2: adaptive filter chain execution
│   └── filters.go          # FilterChainConfig, BuildFilterSpec(), defaults
├── ui/                     # Bubbletea model, views, messages
└── cli/                    # Help styling, version output
```

**Data flow:** `main.go` spawns goroutine → `ProcessAudio()` → Pass 1 (`AnalyzeAudio`) → Pass 2 (applies filters) → sends `ui.*Msg` to TUI via `tea.Program.Send()`.

## Audio processing pipeline

**Four-pass architecture:**

1. **Pass 1 (Analysis):** Measures LUFS, true peak, LRA, noise floor, spectral characteristics
2. **Pass 2 (Processing):** Applies adaptive filter chain tuned to measurements
3. **Pass 3 (Measuring):** Runs loudnorm in measurement mode to get input stats
4. **Pass 4 (Normalising):** Applies loudnorm with linear mode; UREI 1176-inspired limiter creates headroom for full linear gain

**Filter chain order (Pass 2):**
```
highpass → noiseremove → gate → adeclick → acompressor → deesser
```

Each filter prepares audio for the next. Rumble removal before noise reduction. Denoising before gating. Compression before de-essing (compression emphasises sibilance).

**Normalisation (Pass 3/4):**
```
alimiter (peak reduction) → loudnorm (linear mode)
```

The limiter creates headroom so loudnorm can apply full linear gain to reach -18 LUFS without clipping or falling back to dynamic mode.

## Code style

- **Build requirement:** Always use `just build`—never `go build` directly (requires CGO_ENABLED=1 + ldflags)
- **FFmpeg types:** All prefixed with `AV*` (e.g., `AVCodecContext`, `AVFrame`)
- **C strings:** Use `ffmpeg.ToCStr()` and call `.Free()` when done
- **Error handling:** Wrap FFmpeg return codes with `WrapErr()` to convert to Go errors
- **Stream processing:** Check `AVErrorEOF` and `EAgain` for processing loops
- **Submodule:** Uses `github.com/linuxmatters/ffmpeg-statigo` in `third_party/ffmpeg-statigo/` (go.mod replace directive points there)

## Testing instructions

- Run `just test` before committing
- Tests require audio files in `testdata/` (gitignored)
- Processor tests skip gracefully if files missing:
  ```go
  if _, err := os.Stat(testFile); os.IsNotExist(err) {
      t.Skipf("Test file not found: %s", testFile)
  }
  ```

## TUI message protocol

Processing goroutine communicates via typed messages:

- `ui.FileStartMsg` — file processing started
- `ui.ProgressMsg` — pass number, progress (0.0-1.0), current level, measurements
- `ui.FileCompleteMsg` — processing finished with result
- `ui.FileErrorMsg` — processing failed with error

## Adaptive processing

Filter parameters adapt based on Pass 1 measurements (see `adaptive.go`):

- **Highpass frequency:** 60-120Hz based on spectral centroid and LUFS gap
- **NoiseRemove compand:** Threshold and expansion derived from measured noise floor
- **De-esser intensity:** 0.0-0.6 based on spectral centroid + rolloff
- **Gate threshold:** Derived from measured noise floor

## Release workflow

- **Create release:** `just release X.Y.Z` (validates format, checks uncommitted changes, creates annotated tag)
- **Preview changelog:** `just changelog`
- **List releases:** `just releases`
- **Check version:** `just version`
- **Publish:** `git push origin X.Y.Z` (triggers GitHub Actions workflow)

GitHub Actions automatically builds binaries for linux-amd64, linux-arm64, darwin-amd64, darwin-arm64 and creates GitHub release with changelog.

## PR/commit guidelines

- Use Conventional Commits format
- Run `just test` before committing
- Version is injected at build time via ldflags from git tags
