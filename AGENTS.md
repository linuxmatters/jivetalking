# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

Go CLI tool for podcast audio preprocessing using embedded FFmpeg. Transforms raw voice recordings into broadcast-ready audio at -16 LUFS through a four-pass adaptive processing pipeline. Uses [Charm Bubbletea](https://github.com/charmbracelet/bubbletea) for the TUI.

## Setup commands

- Enter development shell: `nix develop` (or let direnv activate automatically)
- Initialise ffmpeg-statigo submodule and download libraries: `just setup` (fetches latest release tag, updates submodule, runs download-lib automatically)

## Build and test commands

- **Build binary:** `just build` (never use `go build` directly - requires CGO + version injection)
- **Run tests:** `just test`
- **Run linters:** `just lint` (runs `go vet`, `gocyclo`, `ineffassign`, `golangci-lint`, `actionlint`)
- **Clean artifacts:** `just clean`
- **Install to ~/.local/bin:** `just install`
- **VHS demo recording:** `just vhs`

## Architecture

```
cmd/jivetalking/main.go     # CLI entry, Kong flags, starts TUI + processing goroutine
internal/
├── audio/reader.go         # FFmpeg demuxer/decoder wrapper (Reader, Metadata, OpenAudioFile)
├── processor/
│   ├── adaptive.go         # AdaptConfig() - tunes FilterChainConfig from Pass 1 measurements
│   ├── analyzer.go         # AnalyzeAudio() - Pass 1: ebur128 + astats + aspectralstats; silence/speech detection
│   ├── analyzer_candidates.go  # Silence candidate scoring and election
│   ├── analyzer_metrics.go     # IntervalSample, SpectralMetrics, per-250ms metric accumulation
│   ├── analyzer_output.go      # MeasureOutputRegions() - before/after region comparison
│   ├── encoder.go          # Output file encoder wrapper
│   ├── filters.go          # FilterChainConfig, filter builder funcs, BuildFilterSpec(), DefaultFilterConfig()
│   ├── frame_processor.go  # runFilterGraph(), FrameLoopConfig - shared filter graph execution
│   ├── normalise.go        # ApplyNormalisation() - Pass 3/4: loudnorm measurement + application
│   └── processor.go        # ProcessAudio(), AnalyzeOnly() - pass orchestration
├── logging/
│   ├── analysis_display.go # DisplayAnalysisResults() - console output for --analysis-only mode
│   ├── recording_tips.go   # Actionable recording advice from measurements
│   ├── report.go           # GenerateReport() - detailed analysis log files (--logs)
│   └── table.go            # MetricRow, reusable multi-column table formatting (Input/Filtered/Final)
├── ui/
│   ├── analysis_model.go   # AnalysisModel - Bubbletea model for --analysis-only progress TUI
│   ├── messages.go         # ProgressMsg, FileStartMsg, FileCompleteMsg, AllCompleteMsg
│   ├── model.go            # Main processing TUI model
│   └── views.go            # TUI rendering
└── cli/                    # Help styling, version output, error formatting
```

**Data flow (processing):** `main.go` spawns goroutine → `ProcessAudio()` → Pass 1 (`AnalyzeAudio`) → `AdaptConfig()` → Pass 2 (filter chain) → Pass 3/4 (`ApplyNormalisation`) → sends `ui.*Msg` to TUI via `tea.Program.Send()`.

**Data flow (analysis-only):** `main.go` → `runAnalysisOnly()` → `AnalyzeOnly()` → Pass 1 + `AdaptConfig()` → `AnalysisModel` TUI shows progress → `DisplayAnalysisResults()` prints report to console. No output files created.

## Audio processing pipeline

**Four-pass architecture:**

1. **Pass 1 (Analysis):** Measures LUFS, true peak, LRA, noise floor, spectral characteristics; detects silence/speech regions via 250ms interval sampling
2. **Pass 2 (Processing):** Applies adaptive filter chain tuned to measurements; output measured for before/after comparison
3. **Pass 3 (Measuring):** Optionally prepends `volume` (pre-gain) + `alimiter` (Volumax) when limiting is active, then runs loudnorm in measurement mode (JSON output via FFmpeg log capture) to get input stats for linear mode; measures the post-limiter signal so `measured_I`/`measured_TP` are accurate
4. **Pass 4 (Normalising):** Applies `volume` (pre-gain, when ceiling clamped) + `alimiter` (Volumax) + `loudnorm` (linear mode) + `adeclick`; pre-gain raises very quiet recordings so the alimiter can use a viable ceiling; `alimiter` creates headroom so loudnorm achieves full linear gain to reach -16 LUFS

**Filter chain order (Pass 2):**
```
downmix → ds201_highpass → ds201_lowpass → noiseremove (anlmdn+compand) → ds201_gate → la2a_compressor → deesser → analysis → resample
```

Order rationale: downmix to mono first; HP/LP removes frequency extremes before gate (DS201 frequency-conscious side-chain pattern); denoising before gating (lowers noise floor for gate); compression before de-essing (compression emphasises sibilance); analysis measures processed signal; resample standardises output format last.

**Normalisation (Pass 3/4):**
```
Pass 3: [volume (pre-gain, when clamped) → alimiter (Volumax)] → loudnorm (measure-only, print_format=json) → captures LoudnormStats JSON
Pass 4: volume (pre-gain, when clamped) → alimiter (Volumax, peak reduction) → loudnorm (linear mode, input stats from Pass 3) → adeclick
```

**Output filename:** `<name>-LUFS-NN-processed.<ext>` where NN is the truncated (not rounded) absolute LUFS value of the final output (e.g., -26.8 LUFS produces `LUFS-26`).

## Code style

- **Build requirement:** Always use `just build` - never `go build` directly (requires `CGO_ENABLED=1` + ldflags version injection)
- **FFmpeg types:** All prefixed with `AV*` (e.g., `AVCodecContext`, `AVFrame`)
- **C strings:** Use `ffmpeg.ToCStr()` and call `.Free()` when done
- **Error handling:** Wrap FFmpeg return codes with `WrapErr()` to convert to Go errors
- **Stream processing:** Check `AVErrorEOF` and `EAgain` for processing loops
- **Submodule:** Uses `github.com/linuxmatters/ffmpeg-statigo` in `third_party/ffmpeg-statigo/` (go.mod replace directive points there)
- **Debug logging:** `processor.DebugLog` is a package-level `func(string, ...any)` set by `main.go` when `--debug` is active; use `debugLog()` (internal wrapper) inside the processor package

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

Two separate message sets exist for the two TUI modes.

**Processing mode** (`ui/messages.go`) - sent by the main processing goroutine:
- `ui.FileStartMsg` - file processing started
- `ui.ProgressMsg` - pass number, progress (0.0-1.0), current level, measurements
- `ui.FileCompleteMsg` - processing finished with result (or error in `Error` field)
- `ui.AllCompleteMsg` - all files finished

**Analysis-only mode** (`ui/analysis_model.go`) - sent by `runAnalysisWithTUI()`:
- `ui.AnalysisStartMsg` - analysis started for a file
- `ui.AnalysisProgressMsg` - progress and level update
- `ui.AnalysisCompleteMsg` - analysis finished with measurements, config, or error

## Adaptive processing

`AdaptConfig()` in `adaptive.go` tunes `FilterChainConfig` from Pass 1 `AudioMeasurements`:

- **DS201 highpass frequency:** 60-120Hz based on spectral decrease (warm/thin voice detection) and silence noise floor; warm voices use gentler Q (0.5) and reduced wet/dry mix
- **DS201 lowpass:** Enabled adaptively based on content type and HF noise indicators (rolloff/centroid ratio)
- **NoiseRemove compand:** Adaptive threshold (noise floor + 5 dB, clamped [-70, -40]); expansion scales 4-12 dB based on noise severity
- **DS201 gate threshold:** Derived from measured noise floor; with breath reduction, positioned at 60% of gap between noise floor and quiet speech level (`SpeechProfile.RMSLevel - CrestFactor`); firmer ratio (1.5x, clamped 2.0-4.0) and deeper range (+6 dB) when breath reduction active
- **LA-2A ratio/release:** Adapts based on kurtosis and flux from `SpeechProfile` when available
- **De-esser intensity:** 0.0-0.6 based on spectral centroid + rolloff; prefers `SpeechProfile` metrics

**Speech-aware metrics:** Filters processing speech content prefer `SpeechProfile` measurements (speech-only regions) over full-file analysis. Graceful fallback when speech metrics unavailable.

**Breath reduction:** Enabled by default (`--breath-reduction` / `--no-breath-reduction`). Requires `SpeechProfile` to calculate quiet speech level for adaptive gate threshold.

## Spectral metrics reference

When working on audio analysis code (especially `internal/processor/analyzer.go`):

- Consult `docs/Spectral-Metrics-Reference.md` for target ranges and quality thresholds
- Align threshold values and scoring constants with the documented ranges
- Cite the reference document when introducing new audio metric thresholds

Additional filter design references in `docs/`:
- `FilterGate-Drawmer DS201.md` - DS201 gate and HP/LP side-chain design rationale
- `FilterCompressor-Teletronix LA-2A.md` - LA-2A optical compressor emulation notes
- `FilterLimiter-CBS-Volumax.md` - Volumax-inspired transparent limiter design

## Release workflow

- **Create release:** `just release X.Y.Z` (validates format, checks uncommitted changes, creates annotated tag)
- **Preview changelog:** `just changelog`
- **List releases:** `just releases`
- **Check version:** `just version`
- **Publish:** `git push origin X.Y.Z` (triggers GitHub Actions workflow)

GitHub Actions automatically builds binaries for linux-amd64, linux-arm64, darwin-amd64, darwin-arm64 and creates GitHub release with changelog.

## PR/commit guidelines

- Use Conventional Commits format
- Run `just lint` and `just test` before committing
- Version is injected at build time via ldflags from git tags
