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
│   ├── adaptive.go         # AdaptConfig() - derives effective filter settings + diagnostics from Pass 1 measurements; tune steps incl. tuneNoiseReduction() (afftdn enable + measured nf + measured custom band-noise profile via buildAfftdnBandNoise/useCustomAfftdnProfile)
│   ├── adaptive_speech_gate.go  # tuneSpeechGate() - speech-gate threshold/ratio/range/attack/release/knee/detection tuning (voiced-anchored threshold + narrow-gap depth step)
│   ├── advice.go           # GainAdvice() - input-gain advice from input true peak (Clipping/Hot/Quiet/Fine vs -6 dBTP); GainAdviceResult.Message()
│   ├── analyser.go         # AnalyseAudio() - Pass 1: ebur128 + astats + aspectralstats; calls detectVoiceActivity()
│   ├── analyser_vad.go         # detectVoiceActivity() - unified voice-activity detector (histogram + Otsu split + percentile floor, hysteresis runs, adaptive gap-tolerance, spectral veto); elects SpeechProfile + NoiseProfile, sets Noise.VoiceActivated; deriveGateStatistics() - gate-window percentiles (VoicedLowPercentile/NoiseHighPercentile/GateSeparationDB)
│   ├── analyser_candidates_shared.go  # Shared sliding-window refinement (refineToSubregion), interval accumulation, scoreSpeechIntervalWindow, levelVariance
│   ├── analyser_candidates_speech.go  # Speech-candidate scoring (scoreSpeechCandidateGrounded - SNR-primary + saturating duration adequacy + consistency tie-break) and election (findBestSpeechRegion, highest-score)
│   ├── analyser_noise_seed.go  # Pre-scan noise-floor seed estimators (Noise.FloorPrescan, anchors the VAD split clamp) + golden-window bounds
│   ├── analyser_bands.go       # Region-scoped sibilant/body band RMS for de-esser intensity; runs its 2 band decodes as bounded goroutines via runBandMeasurements
│   ├── analyser_noise_bands.go # measureNoiseBands() - Pass 1 15-band RMS spectrum of the elected room-tone region (band centres 80 Hz to 24 kHz); sets NoiseProfile.BandNoise + BandsMeasured for afftdn's custom noise profile; runs its 15 band decodes as bounded goroutines via runBandMeasurements
│   ├── analyser_band_runner.go # runBandMeasurements() - shared bounded-goroutine runner for the post-loop band decodes; bandMeasureSem package semaphore (buffered channel sized runtime.NumCPU(), shared across all files and both band functions); bandProgressTracker + pure bandPhaseProgress progress helpers
│   ├── analyser_metrics.go     # IntervalSample, SpectralMetrics, per-250ms metric accumulation
│   ├── analyser_output.go      # MeasureOutputRegions() - before/after region comparison
│   ├── encoder.go          # Output file encoder wrapper
│   ├── filters.go          # BaseFilterConfig, EffectiveFilterConfig, AdaptiveDiagnostics, filter builders, BuildFilterSpec(), DefaultFilterConfig()
│   ├── frame_processor.go  # runFilterGraph(), FrameLoopConfig - shared filter graph execution
│   ├── normalise.go        # ApplyNormalisation() - Pass 3/4: loudnorm measurement + application
│   ├── processor.go        # ProcessAudio(), AnalyseOnlyDetailed() - pass orchestration
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

**Data flow (processing):** `main.go` resolves worker count via `resolveJobs()` (number of input files, capped at `NumCPU`, floored at 1; no flag), creates a cancellable `ctx`, then launches `runWorkerPool()` (`pool.go`) → up to `jobs` files run concurrently, each a goroutine bounded by a semaphore, taking a `CloneForWorker()` config copy and `FileIndex`-routed TUI messages → `ProcessAudio(ctx, …)` → Pass 1 (`AnalyseAudio`) → `AdaptConfig()` → Pass 2 (filter chain) → Pass 3/4 (`ApplyNormalisation`) → `report.WriteMarkdownReport()` renders an always-on Markdown report (`<name>-LUFS-NN-processed.md`) from the run's `RunRecord` → sends `ui.*Msg` to TUI via `tea.Program.Send()`. After `WaitGroup` drains, the pool sends `ui.AllCompleteMsg`. `cancel()` fires after `p.Run()` returns; `runFilterGraph` checks `ctx.Err()` each frame so in-flight workers abort and run deferred temp cleanup.

With `--diagnostics`, each worker attaches the deterministic before/after PNG path list to the `RunRecord` synchronously (`DeriveSpectrogramImages`, pure string work) **before** the `.md`/`.json` write, so the report carries resolving image links; the actual `showspectrumpic` renders run in **bounded background goroutines** off the critical path (`RenderSpectrogramImage`), sharing the pool's semaphore budget and tracked by a `sync.WaitGroup` that gates program exit. Renders honour `ctx` (abort + remove partial PNGs on cancellation) and are non-fatal (a failed render surfaces a warning; audio/`.json`/`.md` still land). The flag touches no DSP, so the `.flac` output is byte-identical with it on or off.

**Data flow (analysis-only):** `main.go` → `runAnalysisOnly()` → `runAnalysisOnlyWithDeps()` (passes `jobs` from `resolveJobs()`) → spawns `runAnalysisPool()` (`analysispool.go`) with a buffered semaphore of size `jobs` and a `sync.WaitGroup`. Up to `jobs` files analyse concurrently; each worker owns its index slot in pre-allocated `results`, `metas`, and `errs` slices (no sharing), calls `CloneForWorker()` for an independent config copy, and sends `ui.AnalysisStartMsg` / `ui.AnalysisProgressMsg` / `ui.AnalysisCompleteMsg` keyed by `FileIndex`. After `wg.Wait()` drains, the pool sends `ui.AllCompleteMsg`. Two branches: TTY path launches a `tea.Program` (using `ui.NewAnalysisModel`) in a goroutine alongside the pool; no-TTY path prints an up-front banner then calls the pool synchronously with `p == nil` (all `p.Send` calls are gated). After the pool returns, `runAnalysisOnlyWithDeps()` prints results in input order, skipping cancelled or nil slots, and `report.WriteMarkdownReport()` writes an `<input>-analysis.md` report (path from `report.AnalysisReportPath`) per file from the Pass-1-only `RunRecord`. With `--diagnostics`, an input-only PNG list (no "after" stage; no output file exists) is derived synchronously onto the record and rendered under the same bounded-semaphore / `WaitGroup` / `ctx` discipline as processing. The TUI (`renderAnalysisVerdict` in `analysis_model.go`) and no-TTY console (`printAnalysisConfirmation` in `main.go`) each show the Recording stars (`ComputeRecordingScore`) plus a gain-advice line (`GainAdvice` + the 5-cell `ui.GainBar` thermometer); both are display-only and the `.md` report stays verdict-free. No processed audio is produced.

## Audio processing pipeline

> Plain-language how-and-why of the pipeline (for engineers and creators) lives in `docs/Pipeline.md`. This section is the developer/agent spec: pass order, filter chain, and the exact stage parameters.

**Four-pass architecture:**

1. **Pass 1 (Analysis):** Measures LUFS, true peak, LRA, noise floor, spectral characteristics; a unified voice-activity detector (`detectVoiceActivity` in `analyser_vad.go`) splits the per-250ms interval level histogram with Otsu's method, elects the `SpeechProfile` (hysteresis-built runs, adaptive gap-tolerance, spectral veto) and the `NoiseProfile` (longest below-split run) from that one split, and sets the noise floor from a low percentile of the level set (this overwrites the astats-seeded `Noise.Floor`/`NoiseProfile.MeasuredNoiseFloor` with a momentary-LUFS value, see `## Measurement axes`); `deriveGateStatistics` also derives the gate-window statistics from the same split and axis (`VoicedLowPercentile` = voiced p10, `NoiseHighPercentile` = noise p95, `GateSeparationDB` = their difference), exposed in the run-record JSON under `regions.gate_statistics` and in the report. After the main decode loop, the 17 band decodes (2 speech via `measureSpeechBands` + 15 noise via `measureNoiseBands`) run as bounded goroutines through `runBandMeasurements` (package `bandMeasureSem` sized `runtime.NumCPU()`); each band opens its own reader and writes only its own slot, so measured RMS values are bit-identical to the former serial path (only scheduling changed) and a per-band failure isolates to a zero/non-measured slot instead of failing the whole measurement. Pass 1 progress reserves 0.0..0.95 for the decode loop (`BandPhaseProgressStart`) and 0.95..1.0 for the band phase, emitting a `ProgressUpdate` per completed band under the `Analysing frequency bands` phase (no-TTY passes a nil callback, so the tracker no-ops)
2. **Pass 2 (Processing):** Applies adaptive filter chain tuned to measurements; output measured for before/after comparison
3. **Pass 3 (Measuring):** Optionally prepends `volume` (pre-gain) + `alimiter` (levelling limiter) when limiting is active, then runs loudnorm in measurement mode (JSON written to a per-call `stats_file`, read back after graph free) to get input stats for linear mode; measures the post-limiter signal so `measured_I`/`measured_TP` are accurate
4. **Pass 4 (Normalising):** Applies `volume` (pre-gain, when ceiling clamped) + `alimiter` (levelling limiter) + `loudnorm` (linear mode) + `aresample` (source rate) + `adeclick` + `alimiter` (final-stage brickwall); pre-gain raises very quiet recordings so the alimiter can use a viable ceiling; the prefix `alimiter` creates headroom so loudnorm achieves full linear gain to reach -16 LUFS; ceiling is derived as `targetTP − gainRequired`; loudnorm targets its own per-file internal TP (`loudnormInternalTargetTP` = projected post-gain peak + `linearSafetyMargin` + `measurementCushionDB`, with the emitted `TP=` clamped to FFmpeg's `[-9, 0]` range), while the final-stage brickwall `alimiter` (pinned to `targetTP − brickwallTruePeakHeadroomDB`) owns true-peak delivery; output lands at the canonical -16 LUFS / -1 dBTP. `linearSafetyMargin = 0.1` (numeric Go-vs-FFmpeg agreement) and `measurementCushionDB = 0.2` (Go-vs-FFmpeg measurement disagreement) are the only static loudnorm-internal margins; the per-file derivation makes the linear-mode cap in `calculateLinearModeTarget` inert by construction, so every file reaches full -16 LUFS in linear mode

**Filter chain order (Pass 2):**
```
downmix → rumble_highpass → bandlimit_lowpass → noise_reduction (anlmdn at source rate, r=0.0020, m=3 → afftdn FFT spectral denoise, fixed nr=12, adaptive enable + nf + measured custom band-noise shape) → speech_gate → levelling_compressor → deesser → analysis → resample
```

Order rationale: downmix to mono first; HP/LP removes frequency extremes before gate (the high-pass/low-pass side-chain pattern); denoising before gating (lowers noise floor for gate); compression before de-essing (compression emphasises sibilance); analysis measures processed signal; final resample standardises output format last.

**Noise removal default:** Production runs `anlmdn → afftdn`. `anlmdn` runs at the source sample rate with `r=0.0020` (`r_min`) and `m=3` (`m_strict`); `afftdn` (FFT spectral denoise, default `nr=12:nt=w:tn=1`; `tn`, `nf`, and `nt` adapt, see below) follows as the residual-suppression stage. No sample-rate cap or exit restore - downstream filters (gate, levelling compressor, de-esser, analysis) operate at the source rate throughout. The matrix spike at `.bench/anlmdn-matrix-spike` validated the anlmdn path against the previous 32 kHz cap default (`r=0.0045`, `m=11`) at ~35 % faster Pass 2 with metric-equivalent quality; in that context the 0.3.1 historical path is `anlmdn_legacy_default`. afftdn replaced the former `compand` residual-suppression stage: sweeps at `.bench/noiseblock-ep83` and `.bench/afftdn-ep83` showed `anlmdn → afftdn` matches or beats `anlmdn → compand` on under-speech noise across all three test stems while keeping gaps clean with less floor modulation. The compand was a blunt downward expander that resolved to its gentlest 4 dB expansion on every stem and added floor pumping. `nr` is FIXED at 12 (not adaptive): a per-presenter sweep showed the noisiest voice must be capped at ~12 to avoid warble. afftdn is adaptive in three ways (`tuneNoiseReduction` in `adaptive.go`; `nr` and the whole anlmdn stage stay fixed): it is DROPPED when `Noise.VoiceActivated` is true (chain becomes anlmdn-only, TUI Denoise row reads "NLM" not "NLM+FFT") because voice-activated captures have digital-silence gaps with no floor for afftdn to lower and `track_noise` warbles on true silence; otherwise its `nf` is pinned to the measured noise floor (`Noise.Floor`, momentary-LUFS axis, re-clamped to afftdn's [-80, -20] dB) with `track_noise` OFF (`tn=0`), holding a static floor instead of self-tracking (floor ~1 dB deeper on average, speech identical, no added warble); and on a trustworthy room-tone region afftdn runs `nt=custom` with a measured per-band shape `bn` instead of the flat white model (see below). VoiceActivated is the only disable.

**Adeclick default:** Production uses `adeclick=t=1.7:w=55:o=50:m=s` (spline interpolation, halved overlap vs prior default) for ~75% Pass 4 runtime reduction at metric-parity quality; the gentle limiter attack keeps source clicks below the relaxed threshold. In benchmark context, refer to the production path as `adeclick_current_t_1_7_w_55_o_50_m_s`. No legacy variant is retained in the matrix. Note: adeclick runs at the source sample rate via an `aresample` inserted before it; loudnorm emits at 192 kHz when it falls back to dynamic mode (linear mode preserves the source rate), and running adeclick at that rate quadrupled its sample count - the dominant Pass 4 cost on long files until the resample was added.

**Normalisation (Pass 3/4):**
```
Pass 3: [volume (pre-gain, when clamped) → alimiter (levelling limiter)] → loudnorm (measure-only, print_format=json, stats_file) → reads back LoudnormStats JSON from the stats file
Pass 4: volume (pre-gain, when clamped) → alimiter (levelling limiter, peak reduction) → loudnorm (linear mode, input stats from Pass 3) → aresample (source rate) → adeclick → alimiter (final-stage brickwall, source rate) → astats → aspectralstats → ebur128
```

> The corpus derivation for the loudnorm and limiter tuning constants (`brickwallTruePeakHeadroomDB`, `measurementCushionDB`, `linearSafetyMargin`, the `loudnormTP`/`minLimiterCeiling` bounds) lives in `docs/Normalisation-Tuning.md`. The code comments in `normalise.go` point there; do not re-inline the rationale.

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
- **Never use `testdata/` files in Go tests.** `go test` must be hermetic: exercise pure functions, in-memory `RunRecord`s and fixtures, and registry/filter lookups that need no audio file. Do not add tests that decode or process `testdata/` audio files - synthetically generated audio written to `t.TempDir()` (e.g. `generateTestAudio`) is permitted - and do not add `findProbeAudioFile`/`findPoolTestAudio`/`copyFixtureTo`/`requireTestdata`-style helpers. Reasons:
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

> For why each adaptation exists, in plain audio terms, see `docs/Pipeline.md`. This section is the spec: which knobs adapt, the functions and constants that drive them, and the corpus justification for the fixed values.

`AdaptConfig()` in `adaptive.go` derives per-file filter state from Pass 1 `AudioMeasurements`: it accepts caller-owned `BaseFilterConfig` defaults, returns `EffectiveFilterConfig` for filter building, and returns `AdaptiveDiagnostics` for report-only adaptation explanations. Do not reintroduce `FilterChainConfig` or store pass execution state in config; use `ProcessingFilterContext` for pass-local state. Each pool worker calls `BaseFilterConfig.CloneForWorker()` (shallow copy + deep-copy `FilterOrder` + per-worker logger) so concurrent workers share no mutable config or logger.

- **Rumble high-pass:** Fixed 80 Hz, 12 dB/oct (2-pole Butterworth), mix 1.0, non-adaptive. 80 Hz sits below every vocal fundamental (lowest measured male F0 ~91 Hz; female ~165+ Hz) and removes subsonic rumble before the gate. No content detection, no notch; tonal hum is left alone since a highpass cannot remove it
- **Band-limit low-pass:** Unconditional 20.5 kHz band-limit (12 dB/oct) for all content, giving downstream AAC/Opus/MP3 encoders a consistent bandwidth. Not adaptive: no content detection and no HF-noise tuning. 20.5 kHz is at the top of human hearing, so the band-limit is audibly transparent and only removes inaudible ultrasonics the lossy encoders discard anyway
- **Noise reduction (afftdn):** `tuneNoiseReduction` adapts the afftdn tail only; anlmdn and afftdn's fixed `nr=12` are untouched. Three adaptations: (1) afftdn is DISABLED when `Noise.VoiceActivated` (`AfftdnEnabled=false`, the chain is anlmdn-only) - voice-activated captures gate to digital silence (flatness ~0.01), so afftdn has no floor to lower and `track_noise` warbles on true silence; this is the only disable condition. (2) Otherwise `AfftdnNoiseFloor` is set from the measured `Noise.Floor` (momentary-LUFS axis), re-clamped to afftdn's [-80, -20] dB (`afftdnNoiseFloorMinDB`/`afftdnNoiseFloorMaxDB`), with `track_noise` OFF (`tn=0`) so afftdn holds the static measured floor instead of self-tracking (floor ~1 dB deeper on average, speech identical, no added warble). A zero `Noise.Floor` (unmeasured) leaves the defaults (afftdn on, `tn=1`, `nf` unset). (3) Custom noise profile: when the room-tone band measurement is trustworthy (`useCustomAfftdnProfile`), `AfftdnNoiseType` becomes `"custom"` and `AfftdnBandNoise` carries the measured shape, emitting `nt=custom:bn=...`; otherwise `nt=w` (white) stands. `nf` (absolute level) and `nr` (depth) still stack on top of `bn`; `bn` carries only the shape. The custom path needs ALL of: NOT voice-activated (afftdn must be on); `GateSeparationDB >= 12 dB` (`afftdnCustomMinSeparationDB`, below it the room tone may be speech-contaminated); room-tone `SpectralFlatness >= 0.45` (`afftdnCustomMinFlatness`, below it the floor is tonal and a measured shape over-fits peaks); and `NoiseProfile.BandsMeasured`. `bn` is built by `buildAfftdnBandNoise` from `measureNoiseBands`'s 15-band room-tone RMS spectrum (band centres 80 Hz to 24 kHz, `afftdnBandCentresHz`) as a RELATIVE shape `bn[i] = clip(bandLevel[i] - mean, +-24 dB)` (`afftdnBandShapeClipDB`); white is all-zeros. The 24 kHz top band sits above the 20.5 kHz band-limit and Nyquist so it is unmeasurable; non-finite bands are excluded from the mean and emitted as `0.0` (flat), never NaN. `BandsMeasured` requires >= 10 of 15 finite bands (`afftdnMinFiniteBands`), else white fallback; an empty `bn` also reverts to white (`sanitizeNoiseReductionConfig`). Known limitation: `measureNoiseBands` reads the raw room-tone region, so sub-80 Hz energy the rumble high-pass later removes still shows in the low bands, wasting shaping budget on empty bands; it cannot regress (validated) and is a future refinement (measure through the pre-afftdn high-pass/low-pass). Corpus A/B vs the white+nf path: 36 improved / 14 unchanged / 0 regressed, no warble (e.g. BF-08-stephen floor down ~7 dB); of 55 stems, 50 custom, 2 white fallback on low separation (LMP-81s-martin, LMP-81s-popey), 3 disabled (voice-activated). Diagnostics `afftdn_enabled`, `afftdn_noise_floor_db`, `afftdn_disable_reason`, `afftdn_noise_type`, `afftdn_band_noise` carry the decision to the report
- **Speech gate threshold:** Voiced-anchored in `calculateSpeechGateThreshold`: `threshold = VoicedLowPercentile - speechGateThresholdSpeechMarginDB` (6 dB below the voiced p10, the soft edge of speech), so the gate never attenuates a word. It returns a narrow-gap flag, set when that speech-side placement cannot also clear the loud noise (`GateSeparationDB < speechGateThresholdSpeechMarginDB + speechGateThresholdNoiseMarginDB`, i.e. separation < 12 dB); on a narrow gap the threshold stays on the speech side (never raised into the voice) and the flag feeds the depth step. Clamped [-80, -25] dB. The old aggression maths, `calculateAggression`, the aggression tiers, and the separation-based legacy split are gone. `calculateSpeechGateThresholdLegacy` (noise floor plus a ratio-based gap, peak reference for high-crest room tone) survives only as the no-`SpeechProfile` safety path (voiced statistics are unmeasurable without a profile); selection is structural, not numeric
- **Speech gate ratio:** LRA-based (`calculateSpeechGateRatio`): 1.5 for wide LRA (>15 LU, `speechGateRatioGentle`), otherwise 2.0 (`speechGateRatioMod`, the cap; a soft expander, never tighter). The former 2.5 tier is gone
- **Speech gate range (depth):** Fixed 14 dB (`speechGateDepthFixedDB`, the transparent-band midpoint), reduced to 8 dB (`speechGateDepthNarrowDB`) on a narrow gap so a shallow gate window does not pump the floor; never a full mute. Two fixed levels only (`calculateSpeechGateRangeDB`), never proportional to separation. The former noise-floor tiers (-22/-16 dB) and entropy tiers are gone
- **Speech gate attack:** Fixed 5 ms (`speechGateAttackMS`); opens before the consonant onset is shaved
- **Speech gate release:** Fixed 200 ms (`speechGateReleaseFixedMS`, `calculateSpeechGateRelease`), with the hold folded in (`agate` has no hold parameter): long enough to ride intra-syllable dips without pumping, short enough to close cleanly at word ends. The former stacked LRA/flux/ZCR compensation (250-600 ms) is gone
- **Speech gate knee:** Fixed 3.0 (`speechGateKneeFixed`, named constant for tuning by ear); a soft knee stands in for the hysteresis `agate` lacks, smoothing the open/close boundary so the gate does not chatter on level wobble. No override; the same for all content
- **Speech gate detection:** Fixed RMS (safe for speech and tonal bleed). The former peak branch needed room-tone entropy > 0.7, which the corpus never reaches
- **Anti-hunting:** No gentle mode. The narrow-gap depth reduction (one signal, separation) prevents hunting on uniform quiet recordings; the former gentle-mode override (extreme LUFS gap + low LRA forcing ratio 1.2 and knee 2.0) is deleted
- **Levelling compressor:** Fixed params: ratio 3.0, attack 10 ms, release 200 ms, knee 4.0, mix 1.0, makeup 0 dB. One genuine adaptation: `threshold = max(SpeechProfile.RMSLevel, Dynamics.RMSLevel) + 9 dB` (clamped), falling back to `PeakLevel − 20 dB` when no `SpeechProfile` is elected. The full-file overall RMS floor (`Dynamics.RMSLevel`, same dBFS axis, raises-only, measurement-only) stops an anomalously quiet speech election from dragging the threshold too low; a NaN/Inf full-file RMS falls back to the raw speech RMS. Speech-RMS-relative threshold engages compression consistently on the upper half of speech across the corpus's wide input-level spread (depth ~2.5-4.4 dB, output crest in the 8-12 dB range); peak−20 is the fallback only. All other params are fixed: ratio/attack/release/knee/mix collapsed to a single value across the real corpus on review; kurtosis, flux, centroid, and the high-crest override were removed as theatre. Note: FFmpeg's `acompressor` is a single-pole-release RMS compressor (`af_sidechaincompress.c`); it levels gently rather than reproducing any vintage optical-compressor behaviour
- **De-esser intensity:** Only `i` adapts; `m` and `f` are fixed. Engagement is driven by the speech-region band excess `sibilanceExcess = SpeechProfile.SibBandRMS - BodyBandRMS` (dB), where the sibilant band is 6-9 kHz and the body band is 1-3 kHz, both measured over the elected speech region in Pass 1 (`analyser_bands.go`, region-scoped `highpass,lowpass,astats` decode). Mapping: `< -6 dB → i=0.0` (OFF); `-6..-3 → ramp 0.0→0.6`; `-3..0 → ramp 0.6→0.85`; `> 0 → i=0.85` (cap). Requires a `SpeechProfile`; without one the de-esser stays OFF (full-file metrics are unreliable). Fixed params: `f=0.80` sets the attenuator corner at ~7.5 kHz so it acts on the sibilant band rather than vocal presence (per `af_deesser.c`, `f` maps to the split-band corner; the prior `f=0.5` corner sat at ~2 kHz); `m=0.50` caps the maximum cut depth (~12 dB, `af_deesser.c maxdess`). Note `i` follows a 5th-power law (`pow(i,5)`) in `af_deesser.c`, so the ramp endpoints are chosen to land in the audibly-active part of the curve.

**Speech-aware metrics:** Filters processing speech content prefer `SpeechProfile` measurements (speech-only regions) over full-file analysis. Graceful fallback when speech metrics unavailable.

## Measurement axes

`AudioMeasurements` carries level and floor fields on THREE axes. They look alike (all dB-ish) but are not interchangeable. The trap: `AnalyseAudio` sets them in two phases - `buildInputMeasurements` first seeds astats-RMS values, then `detectVoiceActivity` OVERWRITES `Noise.Floor` and `NoiseProfile.MeasuredNoiseFloor` with the VAD momentary-LUFS percentile floor. So `MeasuredNoiseFloor` reads like an astats RMS field (it is seeded as `avgRMS`) but the elected value is momentary-LUFS.

| Axis | Fields |
| ---- | ------ |
| K-weighted momentary-LUFS (the VAD level axis) | `Noise.Floor`, `Noise.FloorPrescan`, `Noise.RoomToneDetectLevel`, `NoiseProfile.MeasuredNoiseFloor` (overwritten), `SpeechProfile.MomentaryLUFS`, any `RegionSample.MomentaryLUFS`, gate stats `VoicedLowPercentile` / `NoiseHighPercentile` / `GateSeparationDB` |
| Unweighted astats RMS dBFS | `Regions.ElectedRoomToneSample.RMSLevel`, `SpeechProfile.RMSLevel`, generic `RegionSample.RMSLevel` (input/filtered/final samples), `Dynamics.RMSLevel` / `Dynamics.PeakLevel`, `Noise.FloorAstats` |
| ebur128 BS.1770 integrated/true-peak/range | `Loudness.InputI` (LUFS) / `Loudness.InputTP` (dBTP) / `Loudness.InputLRA` (LU) |

Rules:

- **Never subtract or compare across axes.** An SNR or separation must use both operands on ONE axis. Correct: the `recording.go` cleanliness SNR is `SpeechProfile.MomentaryLUFS - NoiseProfile.MeasuredNoiseFloor` (both momentary-LUFS). Subtracting `SpeechProfile.RMSLevel` (astats) from the momentary-LUFS floor is the historical bug that miscalibrated the Recording star score.
- **Keep a given user-facing number on ONE axis everywhere it appears.** The DISPLAYED noise floor and SNR (TUI live Analysis box; TUI done-box before/after) are unweighted astats RMS dBFS, sourced from the room-tone `RegionSample.RMSLevel` (`ElectedRoomToneSample` for input, the output room-tone sample for after); both route through one resolver so they cannot diverge.
- **The momentary-LUFS floor is INTERNAL.** `Noise.Floor` / `MeasuredNoiseFloor` drive the VAD split, the Recording-score cleanliness axis, and the afftdn `nf` seed, and it is the report's `noise.floor_dbfs`. The report's `measured_floor_dbfs` equals `noise.floor_dbfs` (same momentary-LUFS `floor`); the room-tone samples' `rms_level_dbfs` is the astats-RMS room tone, a DIFFERENT number for the same region. Do not conflate them.
- When adding any consumer that reads a floor or level, check its axis in the table above first and keep the comparison single-axis.

## Spectral metrics reference

When working on audio analysis code (especially `internal/processor/analyser.go`):

- Consult `docs/Spectral-Metrics-Reference.md` (aligned with the `audio-metrics` skill) for what each metric is - definition, ffmpeg computation, units, range, source filter - when reading or producing audio measurements. It is an objective reference, not a source of thresholds or quality verdicts
- Threshold values and scoring constants live in the code, justified against the validation corpus per the "no theatre - meaningful, exercised adaptation or a fixed correct value" principle and the bit-exact validation sweeps; do not derive them from documented "good ranges"

For how and why the pipeline is built and tuned the way it is, in plain audio terms, see `docs/Pipeline.md`. For the influences behind the approach, see `docs/Inspiration.md` - the classic audio devices that shaped jivetalking's quality bar, named honestly as inspiration, not as circuits being reproduced.

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
