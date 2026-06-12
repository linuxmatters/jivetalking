# AGENTS.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project overview

Go CLI tool for podcast audio preprocessing using embedded FFmpeg. Transforms raw voice recordings into broadcast-ready audio at -16 LUFS through a four-pass adaptive processing pipeline. Uses the [Charm v2 suite](https://charm.land) for the TUI (bubbletea + lipgloss under the `charm.land/<pkg>/v2` import domain).

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
cmd/jivetalking/main.go          # CLI entry, Kong flags, resolveJobs(), ctx + cancel(), starts TUI; runAnalysisOnly() / runAnalysisOnlyWithDeps()
cmd/jivetalking/pool.go          # runWorkerPool() - bounded concurrent multi-file processing
cmd/jivetalking/analysispool.go  # runAnalysisPool() - bounded concurrent multi-file analysis (mirrors pool.go)
internal/
├── audio/reader.go         # FFmpeg demuxer/decoder wrapper (Reader, Metadata, OpenAudioFile)
├── processor/
│   ├── adaptive.go         # AdaptConfig() - derives effective filter settings + diagnostics from Pass 1 measurements
│   ├── advice.go           # GainAdvice() - input-gain advice from input true peak (Clipping/Hot/Quiet/Fine vs -6 dBTP); GainAdviceResult.Message()
│   ├── analyzer.go         # AnalyzeAudio() - Pass 1: ebur128 + astats + aspectralstats; room-tone/speech detection
│   ├── analyzer_candidates.go  # Room-tone candidate scoring and election
│   ├── analyzer_metrics.go     # IntervalSample, SpectralMetrics, per-250ms metric accumulation
│   ├── analyzer_output.go      # MeasureOutputRegions() - before/after region comparison
│   ├── encoder.go          # Output file encoder wrapper
│   ├── filters.go          # BaseFilterConfig, EffectiveFilterConfig, AdaptiveDiagnostics, filter builders, BuildFilterSpec(), DefaultFilterConfig()
│   ├── frame_processor.go  # runFilterGraph(), FrameLoopConfig - shared filter graph execution
│   ├── normalise.go        # ApplyNormalisation() - Pass 3/4: loudnorm measurement + application
│   ├── processor.go        # ProcessAudio(), AnalyzeOnlyDetailed() - pass orchestration
│   ├── quality.go          # ComputeQualityScore() - Processed star rating: output vs -16 LUFS spec (saturates at 5★)
│   ├── recording.go        # ComputeRecordingScore(*AudioMeasurements) - Recording star rating: source capture, 3 weighted axes (1-5★); shared by processing + analysis-only
│   ├── spectrogram.go      # frozenSpectrogramSpec, generateSpectrogram(), RenderSpectrogramImage() - audio→showspectrumpic→PNG render (--diagnostics)
│   └── spectrogram_paths.go    # SpectrogramImage, DeriveSpectrogramImages(), kind/stage constants - deterministic PNG path derivation
├── report/                 # always-on Markdown report rendered from RunRecord (replaces internal/logging)
│   ├── render.go           # RenderMarkdown() - orchestrates section order from a RunRecord
│   ├── sections.go         # per-domain section renderers (loudness, dynamics, spectral, regions, filters, normalisation, Spectrograms image-link table)
│   ├── mdtable.go          # Markdown table builder + numeric formatters
│   ├── definitions.go      # objective metric-definition catalogue (sourced from docs/Spectral-Metrics-Reference.md)
│   ├── format.go           # formatFloat() + NaN/±Inf placeholder rule
│   ├── write.go            # WriteMarkdownReport(), Timings struct
│   └── paths.go            # AnalysisReportPath() - analysis-only report path derivation
├── ui/
│   ├── analysis_model.go   # AnalysisModel - Bubbletea model for --analysis-only progress TUI
│   ├── messages.go         # ProgressMsg, FileStartMsg, FileCompleteMsg, AllCompleteMsg
│   ├── model.go            # Main processing TUI model
│   └── views.go            # TUI rendering
└── cli/                    # Help styling, version output, error formatting
```

**Data flow (processing):** `main.go` resolves worker count via `resolveJobs()` (number of input files, capped at `NumCPU`, floored at 1; no flag), creates a cancellable `ctx`, then launches `runWorkerPool()` (`pool.go`) → up to `jobs` files run concurrently, each a goroutine bounded by a semaphore, taking a `CloneForWorker()` config copy and `FileIndex`-routed TUI messages → `ProcessAudio(ctx, …)` → Pass 1 (`AnalyzeAudio`) → `AdaptConfig()` → Pass 2 (filter chain) → Pass 3/4 (`ApplyNormalisation`) → `report.WriteMarkdownReport()` renders an always-on Markdown report (`<name>-LUFS-NN-processed.md`) from the run's `RunRecord` → sends `ui.*Msg` to TUI via `tea.Program.Send()`. After `WaitGroup` drains, the pool sends `ui.AllCompleteMsg`. `cancel()` fires after `p.Run()` returns; `runFilterGraph` checks `ctx.Err()` each frame so in-flight workers abort and run deferred temp cleanup.

With `--diagnostics`, each worker attaches the deterministic before/after PNG path list to the `RunRecord` synchronously (`DeriveSpectrogramImages`, pure string work) **before** the `.md`/`.json` write, so the report carries resolving image links; the actual `showspectrumpic` renders run in **bounded background goroutines** off the critical path (`RenderSpectrogramImage`), sharing the pool's semaphore budget and tracked by a `sync.WaitGroup` that gates program exit. Renders honour `ctx` (abort + remove partial PNGs on cancellation) and are non-fatal (a failed render surfaces a warning; audio/`.json`/`.md` still land). The flag touches no DSP, so the `.flac` output is byte-identical with it on or off.

**Data flow (analysis-only):** `main.go` → `runAnalysisOnly()` → `runAnalysisOnlyWithDeps()` (passes `jobs` from `resolveJobs()`) → spawns `runAnalysisPool()` (`analysispool.go`) with a buffered semaphore of size `jobs` and a `sync.WaitGroup`. Up to `jobs` files analyse concurrently; each worker owns its index slot in pre-allocated `results`, `metas`, and `errs` slices (no sharing), calls `CloneForWorker()` for an independent config copy, and sends `ui.AnalysisStartMsg` / `ui.AnalysisProgressMsg` / `ui.AnalysisCompleteMsg` keyed by `FileIndex`. After `wg.Wait()` drains, the pool sends `ui.AllCompleteMsg`. Two branches: TTY path launches a `tea.Program` (using `ui.NewAnalysisModel`) in a goroutine alongside the pool; no-TTY path prints an up-front banner then calls the pool synchronously with `p == nil` (all `p.Send` calls are gated). After the pool returns, `runAnalysisOnlyWithDeps()` prints results in input order, skipping cancelled or nil slots, and `report.WriteMarkdownReport()` writes an `<input>-analysis.md` report (path from `report.AnalysisReportPath`) per file from the Pass-1-only `RunRecord`. With `--diagnostics`, an input-only PNG list (no "after" stage; no output file exists) is derived synchronously onto the record and rendered under the same bounded-semaphore / `WaitGroup` / `ctx` discipline as processing. The TUI (`renderAnalysisVerdict` in `analysis_model.go`) and no-TTY console (`printAnalysisConfirmation` in `main.go`) each show the Recording stars (`ComputeRecordingScore`) plus a gain-advice line (`GainAdvice` + the 5-cell `ui.GainBar` thermometer); both are display-only and the `.md` report stays verdict-free. No processed audio is produced.

## Audio processing pipeline

**Four-pass architecture:**

1. **Pass 1 (Analysis):** Measures LUFS, true peak, LRA, noise floor, spectral characteristics; detects room-tone/speech regions via 250ms interval sampling
2. **Pass 2 (Processing):** Applies adaptive filter chain tuned to measurements; output measured for before/after comparison
3. **Pass 3 (Measuring):** Optionally prepends `volume` (pre-gain) + `alimiter` (Volumax) when limiting is active, then runs loudnorm in measurement mode (JSON written to a per-call `stats_file`, read back after graph free) to get input stats for linear mode; measures the post-limiter signal so `measured_I`/`measured_TP` are accurate
4. **Pass 4 (Normalising):** Applies `volume` (pre-gain, when ceiling clamped) + `alimiter` (Volumax) + `loudnorm` (linear mode) + `aresample` (source rate) + `adeclick`; pre-gain raises very quiet recordings so the alimiter can use a viable ceiling; `alimiter` creates headroom so loudnorm achieves full linear gain to reach -16 LUFS; ceiling is derived as `targetTP − gainRequired − ceilingMarginDB` (`ceilingMarginDB = 1.4`, corpus-derived p95); a binding gain cap in `calculateLinearModeTarget` (`linearSafetyMargin = 0.1`) acts as the exact TP backstop on high-crest files, intentionally landing them below -16 LUFS rather than clipping

**Filter chain order (Pass 2):**
```
downmix → ds201_highpass → ds201_lowpass → noiseremove (anlmdn at source rate, r=0.0020, m=3 → afftdn FFT spectral denoise, fixed nr=12) → ds201_gate → la2a_compressor → deesser → analysis → resample
```

Order rationale: downmix to mono first; HP/LP removes frequency extremes before gate (DS201 frequency-conscious side-chain pattern); denoising before gating (lowers noise floor for gate); compression before de-essing (compression emphasises sibilance); analysis measures processed signal; final resample standardises output format last.

**Noise removal default:** Production runs `anlmdn → afftdn`. `anlmdn` runs at the source sample rate with `r=0.0020` (`r_min`) and `m=3` (`m_strict`); `afftdn` (FFT spectral denoise, `nr=12:nt=w:tn=1`) follows as the residual-suppression stage. No sample-rate cap or exit restore - downstream filters (gate, LA-2A, de-esser, analysis) operate at the source rate throughout. The matrix spike at `.bench/anlmdn-matrix-spike` validated the anlmdn path against the previous 32 kHz cap default (`r=0.0045`, `m=11`) at ~35 % faster Pass 2 with metric-equivalent quality; in that context the 0.3.1 historical path is `anlmdn_legacy_default`. afftdn replaced the former `compand` residual-suppression stage: sweeps at `.bench/noiseblock-ep83` and `.bench/afftdn-ep83` showed `anlmdn → afftdn` matches or beats `anlmdn → compand` on under-speech noise across all three test stems while keeping gaps clean with less floor modulation. The compand was a blunt downward expander that resolved to its gentlest 4 dB expansion on every stem and added floor pumping. `nr` is FIXED at 12 (not adaptive): a per-presenter sweep showed the noisiest voice must be capped at ~12 to avoid warble.

**Adeclick default:** Production uses `adeclick=t=2.0:w=55:o=50:m=s` (spline interpolation, halved overlap vs prior default) for ~75% Pass 4 runtime reduction at metric-parity quality; the gentle limiter attack keeps source clicks below the relaxed threshold. In benchmark context, refer to the production path as `adeclick_current_t_2_0_w_55_o_50_m_s`. No legacy variant is retained in the matrix. Note: adeclick runs at the source sample rate via an `aresample` inserted before it; loudnorm emits at 192 kHz when it falls back to dynamic mode (linear mode preserves the source rate), and running adeclick at that rate quadrupled its sample count - the dominant Pass 4 cost on long files until the resample was added.

**Normalisation (Pass 3/4):**
```
Pass 3: [volume (pre-gain, when clamped) → alimiter (Volumax)] → loudnorm (measure-only, print_format=json, stats_file) → reads back LoudnormStats JSON from the stats file
Pass 4: volume (pre-gain, when clamped) → alimiter (Volumax, peak reduction) → loudnorm (linear mode, input stats from Pass 3) → aresample (source rate) → adeclick
```

**Output filename:** `<name>-LUFS-NN-processed.<ext>` where NN is the rounded (nearest whole) absolute LUFS value of the final output (e.g., -26.8 LUFS produces `LUFS-27`). The matching always-on report is `<name>-LUFS-NN-processed.md`; analysis-only writes `<input>-analysis.md`.

**Diagnostic artefacts (`--diagnostics`, default OFF):** the flag gates three bulk diagnostic outputs written beside the `.md`/`.json`/`.flac`:
- The `.intervals.jsonl` + `.candidates.jsonl` sidecars. 📌 **Behaviour change:** these were always-on before the spectrogram feature; they are now opt-in. The `.json` run-record's inline summaries cover the OFF path (interval percentiles + largest gap; elected candidate + count/score), so the `.md`/`.json` stay fully populated without the flag.
- Before/after spectrogram PNGs, `<name>-LUFS-NN-processed.spectrogram-<kind>-<stage>.png` where `<kind>` is `whole`/`roomtone`/`speech` and `<stage>` is `before`/`after` (processing, ≤6 images: a kind's pair drops cleanly when no profile is elected) or `input` (analysis-only, ≤3 images, no "after"). Rendered via `showspectrumpic` from frozen parameters (`frozenSpectrogramSpec`: identical dimensions and dB/frequency scale across a before/after pair for honest comparison). The `## Spectrograms` report section links them.

**Report:** `internal/report` renders the always-on `.md` report from the run's `RunRecord` (never the `.json` artefact or `AudioMeasurements`), so a future `.json` → `.md` re-render is a thin adapter over `RenderMarkdown`. The report is empirical: objective metric definitions and units (sourced from `docs/Spectral-Metrics-Reference.md` via `definitions.go`), no quality verdicts or interpretive adjectives. The two star ratings are TUI-only (`renderDoneBox` in `ui/views.go`); never add them to the `.md`/`.json`. When the `RunRecord` carries spectrogram paths (`--diagnostics`), `renderSpectrograms` (`sections.go`) appends a `## Spectrograms` image-link table (before│after columns for processing, an Input column for analysis-only); the renderer reads the record only and never calls ffmpeg.

## Code style

- **Build requirement:** Always use `just build` - never `go build` directly (requires `CGO_ENABLED=1` + ldflags version injection)
- **FFmpeg types:** All prefixed with `AV*` (e.g., `AVCodecContext`, `AVFrame`)
- **C strings:** Use `ffmpeg.ToCStr()` and call `.Free()` when done
- **Error handling:** Wrap FFmpeg return codes with `WrapErr()` to convert to Go errors
- **Stream processing:** Check `AVErrorEOF` and `EAgain` for processing loops
- **Submodule:** Uses `github.com/linuxmatters/ffmpeg-statigo` in `third_party/ffmpeg-statigo/` (go.mod replace directive points there)
- **Debug logging:** `processor.DebugLog` is a package-level `func(string, ...any)` set by `main.go` when `--debug` is active; use `debugLog()` (internal wrapper) inside the processor package
- **Charm v2:** Import `charm.land/bubbletea/v2` and `charm.land/lipgloss/v2`; never `github.com/charmbracelet/...`. Models return `View() tea.View` built with `tea.NewView(...)`, not `View() string`. Match key presses on `tea.KeyPressMsg` (interface), not `tea.KeyMsg`. Set program options as View fields (`view.AltScreen = true`), not via `tea.WithAltScreen()`. `lipgloss.Color` returns `image/color.Color`.

## Testing instructions

- Run `just test` before committing
- **Never use `testdata/` files in Go tests.** `go test` must be hermetic: exercise pure functions, in-memory `RunRecord`s and fixtures, and registry/filter lookups that need no audio file. Do not add tests that decode or process audio, and do not add `findProbeAudioFile`/`findPoolTestAudio`/`copyFixtureTo`/`requireTestdata`-style helpers. Reasons:
  - **CI has no `testdata/`.** The audio files (`LMP-*.flac`, `fixture-5m.flac`) are gitignored, so any test that decodes or processes them fails or skips in CI.
  - **They are slow.** Pushing real audio through the pipeline (and rendering spectrograms) drove `cmd/jivetalking`'s `go test` to ~130s; `go test` must stay fast.
- Audio-dependent checks belong in the gitignored manual validation harness under `testdata/validation-*/bin/` (shell scripts run by hand, e.g. `spectrogram-validate.sh`), never in the Go suite

## TUI message protocol

Two separate message sets exist for the two TUI modes.

**Processing mode** (`ui/messages.go`) - sent by the main processing goroutine:
- `ui.FileStartMsg` - file processing started
- `ui.ProgressMsg` - pass number, progress (0.0-1.0), current level, measurements
- `ui.FileCompleteMsg` - processing finished with result (or error in `Error` field); carries `Quality` (Processed score) + `RecordingQuality` (Recording score) for the done box
- `ui.AllCompleteMsg` - all files finished

**Analysis-only mode** (`ui/analysis_model.go`) - sent by `runAnalysisPool()` goroutines, routed by `FileIndex`:
- `ui.AnalysisStartMsg` - analysis started; carries `FileIndex`, `FileName`, `FilePath`
- `ui.AnalysisProgressMsg` - progress (0.0-1.0) and level update; carries `FileIndex`
- `ui.AnalysisCompleteMsg` - analysis finished; carries `FileIndex`, `Result`, and `Error`
- `ui.AllCompleteMsg` - all files finished (shared with processing mode; sent once after `wg.Wait()`)

## Adaptive processing

`AdaptConfig()` in `adaptive.go` derives per-file filter state from Pass 1 `AudioMeasurements`: it accepts caller-owned `BaseFilterConfig` defaults, returns `EffectiveFilterConfig` for filter building, and returns `AdaptiveDiagnostics` for report-only adaptation explanations. Do not reintroduce `FilterChainConfig` or store pass execution state in config; use `ProcessingFilterContext` for pass-local state. Each pool worker calls `BaseFilterConfig.CloneForWorker()` (shallow copy + deep-copy `FilterOrder` + per-worker logger) so concurrent workers share no mutable config or logger.

- **DS201 highpass:** Fixed 80 Hz, 12 dB/oct (2-pole Butterworth), mix 1.0, non-adaptive. 80 Hz sits below every vocal fundamental (lowest measured male F0 ~91 Hz; female ~165+ Hz) and removes subsonic rumble before the gate. No content detection, no notch; tonal hum is left alone since a highpass cannot remove it
- **DS201 lowpass:** Unconditional 20.5 kHz band-limit (12 dB/oct) for all content, giving downstream AAC/Opus/MP3 encoders a consistent bandwidth. Not adaptive: no content detection and no HF-noise tuning. 20.5 kHz is at the top of human hearing, so the band-limit is audibly transparent and only removes inaudible ultrasonics the lossy encoders discard anyway
- **DS201 gate threshold:** Aggression-based in `calculateDS201GateThreshold`: `quietSpeech + dynamicRange × aggression`, where `quietSpeech = SpeechProfile.RMSLevel - CrestFactor`, `dynamicRange = CrestFactor`, and `aggression` (0.25-0.60) scales with separation and reduces for high LRA (`calculateAggression`); clamped between noise floor and speech RMS by safety margins. `calculateDS201GateThresholdLegacy` (noise floor plus a ratio-based gap, peak reference for high-crest room tone, clamped [-80, -25] dB) is the low-separation guard: reached only when noise-to-quiet-speech separation < 5 dB makes the aggression maths unreliable. Since commit 098ef6c every corpus stem elects a `SpeechProfile`, so the guard fires on separation, never on a missing profile
- **DS201 gate ratio:** LRA-based (`calculateDS201GateRatio`): 1.5 for wide LRA (>15 LU), 2.0 for moderate (>10 LU), 2.5 for narrow. The 2.5 tier is unexercised on the current corpus but kept (LRA is the right signal, the tier is cheap and correct)
- **DS201 gate range:** Noise-floor-driven (`calculateDS201GateRangeDB`): clean recordings (noise floor below -70 dBFS) take -22 dB depth, everything else -16 dB; clamped [-36, -12] dB. The former room-tone entropy tiers (-16/-21/-27) were dropped: elected room-tone entropy is pinned far below their thresholds on every stem, so they always collapsed to the tonal tier and only the noise floor was live. `ds201GateRangeCleanDB`/`ds201GateRangeStandardDB`/`ds201GateRangeCleanFloorDB` are named constants for tuning by ear
- **DS201 gate attack:** Fixed 10 ms (`ds201GateAttackMS`). The former transient/flux tiers never fired on speech (every stem produced the 10 ms floor), so attack is no longer adaptive
- **DS201 gate release:** `calculateDS201GateRelease` keeps the sustained-vs-standard split (sustained when `flux < 0.01 AND zcr < 0.08` → 300 ms base, else 250 ms), the +50 ms hold compensation, a fixed +75 ms tonal-bleed allowance, and the low-LRA extension (up to +150 ms below 8 LU). The former room-tone entropy tiering always resolved to the +75 ms very-tonal constant (entropy always tonal), so it was collapsed to a fixed term; the dead `flux > 0.05` dynamic tier was deleted
- **DS201 gate knee:** Fixed 3.0 (`ds201GateKneeFixed`, named constant for tuning by ear). The former spectral-crest tiering keyed off the discredited crest signal (per the de-esser/LA-2A reviews); gentle mode still overrides knee to 2.0
- **DS201 gate detection:** Fixed RMS (safe for speech and tonal bleed). The former peak branch needed room-tone entropy > 0.7, which the corpus never reaches
- **DS201 gate gentle mode:** Extreme LUFS gap (>= 25 dB) with low LRA (< 10 LU) overrides ratio to 1.2 and knee to 2.0 to prevent hunting on uniform quiet recordings (its knee override still wins over the fixed 3.0)
- **LA-2A compressor:** Fixed params: ratio 3.0, attack 10 ms, release 200 ms, knee 4.0, mix 1.0, makeup 0 dB. One genuine adaptation: `threshold = SpeechProfile.RMSLevel + 9 dB` (clamped), falling back to `PeakLevel − 20 dB` when no `SpeechProfile` is elected. Speech-RMS-relative threshold engages compression consistently on the upper half of speech across the corpus's wide input-level spread (depth ~2.5-4.4 dB, output crest in the 8-12 dB range); peak−20 is the fallback only. All other params are fixed: ratio/attack/release/knee/mix collapsed to a single value across the real corpus on review; kurtosis, flux, centroid, and the high-crest override were removed as theatre. Note: FFmpeg's `acompressor` is a single-pole-release RMS compressor (`af_sidechaincompress.c`); it cannot reproduce the LA-2A's two-stage programme-dependent T4 release or level-dependent ratio. The LA-2A is the quality inspiration (gentle levelling, least treatment), not a faithful emulation
- **De-esser intensity:** Only `i` adapts; `m` and `f` are fixed. Engagement is driven by the speech-region band excess `sibilanceExcess = SpeechProfile.SibBandRMS - BodyBandRMS` (dB), where the sibilant band is 6-9 kHz and the body band is 1-3 kHz, both measured over the elected speech region in Pass 1 (`analyzer_bands.go`, region-scoped `highpass,lowpass,astats` decode). Mapping: `< -6 dB → i=0.0` (OFF); `-6..-3 → ramp 0.0→0.6`; `-3..0 → ramp 0.6→0.85`; `> 0 → i=0.85` (cap). Requires a `SpeechProfile`; without one the de-esser stays OFF (full-file metrics are unreliable). Fixed params: `f=0.80` sets the attenuator corner at ~7.5 kHz so it acts on the sibilant band rather than vocal presence (per `af_deesser.c`, `f` maps to the split-band corner; the prior `f=0.5` corner sat at ~2 kHz); `m=0.50` caps the maximum cut depth (~12 dB, `af_deesser.c maxdess`). Note `i` follows a 5th-power law (`pow(i,5)`) in `af_deesser.c`, so the ramp endpoints are chosen to land in the audibly-active part of the curve.

**Speech-aware metrics:** Filters processing speech content prefer `SpeechProfile` measurements (speech-only regions) over full-file analysis. Graceful fallback when speech metrics unavailable.

## Spectral metrics reference

When working on audio analysis code (especially `internal/processor/analyzer.go`):

- Consult `docs/Spectral-Metrics-Reference.md` (aligned with the `audio-metrics` skill) for what each metric is - definition, ffmpeg computation, units, range, source filter - when reading or producing audio measurements. It is an objective reference, not a source of thresholds or quality verdicts
- Threshold values and scoring constants live in the code, justified against the validation corpus per the "no theatre - meaningful, exercised adaptation or a fixed correct value" principle and the bit-exact validation sweeps; do not derive them from documented "good ranges"

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
