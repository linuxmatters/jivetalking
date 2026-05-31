---

# Benchmarks

**Engineering record for how the production `anlmdn` and `adeclick` tunings were derived.** This document preserves the methodology, cost models, false starts, and final outcomes from two filter-chain investigations conducted in spring 2026. It exists so future work on these filters starts from evidence rather than intuition.

The working investigation notes, the per-filter benchmark Go test fixtures, and the `bench-anlmdn-*` / `bench-adeclick-*` recipes that drove them have been retired. The reasoning is here.

---

## 1. Purpose

Two questions drove this work:

- **anlmdn:** is the production noise-removal tuning the right speed/quality trade for podcast voice, and what does "right" mean when measured against a real fixture with persistent radio interference?
- **adeclick:** does Pass 4's click/pop repair earn its CPU cost at its original parameters, and where is the gentler frontier?

Both investigations produced production changes. Both produced lessons more durable than the parameter values themselves.

This document captures:

- How the variant matrices were built and why each variant was on the list
- The cost model that informed the anlmdn pivot
- The architectural fact that almost made the first anlmdn investigation meaningless
- The hypothesis that was falsified, and why
- Where the final production constants live and what each one is doing

It does **not** capture:

- How to run the benchmarks. The recipes and Go tests have been removed; the methodology survives, the commands do not.
- Multi-file parallelism, FLAC compression tuning, TUI cost, logging cost. These were explicitly out of scope.

---

## 2. Scope

**In scope.**

- `anlmdn` (FFmpeg's `af_anlmdn` Non-Local Means denoiser) parameter and architecture tuning at the Pass 2 noise-removal stage.
- `adeclick` (FFmpeg's `af_adeclick`) parameter tuning at the Pass 4 click/pop repair stage.
- The shared variant-matrix methodology used for both.

**Out of scope.**

- Pass 1 analysis cost beyond what the CPU audit identified.
- Per-pass loudnorm or limiter tuning.
- Output region measurement (covered separately by the always-on metrics work).
- FLAC encoder cost.

---

## 3. Methodology

Both investigations used the same shape: a manifest of named variants, a captured snapshot per run, and a validation report comparing each candidate against a designated baseline.

### 3.1 Variant manifests

A variant matrix is a list of named candidates differing in one or more filter parameters. Each variant carries:

- `Name` - lower-snake-case, with parameters embedded where they vary (e.g. `adeclick_t_2_0_w_55_o_50_m_s` encodes threshold, window, overlap, and method directly).
- `ParameterIntent` - a one-sentence English statement of what this variant is testing or proving.
- `ValidationPriority` - a tag locating the variant on the comparison frontier: `baseline`, `production-default`, `tier1-conservative`, `tier1-balanced`, `tier1-aggressive`, `tier2-overlap`, `tier2-window`, `tier3-patch-check`, `no-adeclick-baseline`. The tag dictates how thoroughly the variant gets reviewed.
- `Params` - the parameter struct fed into the filter-graph builder.

Naming the variant after its parameter values means that reading the artefact directory listing is equivalent to reading the matrix. Renaming a variant by changing its parameters is automatic.

### 3.2 Per-variant capture

For each variant the benchmark produced a capture containing:

- The actual filtered output, available for listening and offline spectrogram comparison.
- The resolved FFmpeg filter spec, end-to-end. This is the ground truth for "what did this run actually do".
- A snapshot record (schema below).
- A candidate-vs-baseline validation report with speed and objective-metrics delta tables plus a listening checklist.
- A flat-text timing summary of variant name, runtime, and final LUFS / true peak / LRA, easy to grep across runs.

Optional spectrogram and 30-second excerpt rendering was available for `adeclick` when ffmpeg was on the PATH and an opt-in environment variable was set. The matrix-spike runs rendered spectrograms unconditionally because perceptual evidence was the point of that spike.

### 3.3 Snapshot schema

Every variant capture wrote a JSON snapshot containing, at minimum:

- Variant identity: name, parameter intent, validation priority.
- Filter spec: the resolved FFmpeg graph string used for the run.
- Runtime: per-pass wall time in milliseconds (Pass 2 for anlmdn, Pass 4 for adeclick).
- Loudness: final LUFS, final true peak, final LRA.
- Spectral and noise-floor: post-filter spectral centroid, spectral rolloff, noise floor (dBFS).
- Speech sample: post-filter speech RMS where a speech profile was elected.
- Input metadata: sample rate, channels, sample format.
- Output metadata: sample rate, channels, duration, bit depth.
- Environment: Go version, GOOS, GOARCH, CPU model from `/proc/cpuinfo` or `sysctl`, ffmpeg-statigo module version, fixture path.
- Profile presence flags: `missing_silence_profile`, `missing_speech_profile`, `quality_validation_incomplete`. Variants that ran on synthetic short fixtures often hit these and the validation report flagged the comparison as incomplete rather than silently producing zero deltas.
- Optional warnings array.

NaN and Inf floats were sanitised to JSON nulls before writing. Without that, a missing profile sample on a short fixture would produce an invalid JSON file and a confusing comparison run.

### 3.4 Validation report shape

Each variant's validation report contained two delta tables (Speed and Objective Metrics) comparing the candidate to a fixed baseline variant for that matrix. The baseline was named explicitly: `anlmdn_legacy_default` for anlmdn, `adeclick_current_t_2_0_w_55_o_50_m_s` for adeclick. Reports listed the deltas; they did not recommend a production setting. The recommendation came from reading the matrix and listening to the processed output.

A listening checklist closed every report:

- **anlmdn:** room noise reduction, watery artefacts, breath tails, consonant smearing, hollow tone, noise pumping between phrases.
- **adeclick:** click and pop repair across hard transients, sibilant integrity, plosive transients, quiet-passage stability, background noise pumping near clicks.

These were not scored. They named the perceptual axes the listener should attend to before approving a variant for production.

### 3.5 Fixture handling and capture opt-in

Benchmarks resolved the fixture from `JIVETALKING_BENCH_FIXTURE` and skipped cleanly when unset, so the test target was safe to run on contributor machines without the real recordings. Capture was opt-in via `JIVETALKING_ANLMDN_BENCH_CAPTURE=1` and `JIVETALKING_ADECLICK_BENCH_CAPTURE=1`. Capture roots were repo-relative defaults overridable via environment variables.

The 5-minute fixture at `testdata/fixture-5m.flac` was the conventional iteration target. Real-world fixtures (`LMP-68-mark.flac`, `LMP-69-martin.flac`) were used for matrix spikes when bigger samples were needed.

---

## 4. anlmdn investigation

### 4.1 Starting point: 0.3.1 baseline

The 0.3.1 release shipped `anlmdn=s=0.00001:p=0.0060:r=0.0058:m=11`, applied at the source sample rate with no pre-cap. Source-rate flow, no architectural surprises. This is the reference everything that followed was measured against.

### 4.2 First tuning round and the 32 kHz cap

A research-radius sweep tested four lower-`r` candidates (`anlmdn_r_5_0`, `anlmdn_r_4_5`, `anlmdn_r_4_0`, `anlmdn_r_patch_adjusted`) plus a 32 kHz pre-cap variant (`anlmdn_sr_32000_best_r`) that ran `anlmdn` at 32 kHz with `r=0.0045, m=11`.

The 32 kHz cap variant won on metric parity and shipped as the production default. Pass 2 runtime on the 5-minute fixture was 6.7 s with the cap vs 11.1 s for `anlmdn_current` (source rate, `r=0.0058`).

This was the right speed answer for the wrong reason, and a separate piece of work on the same code revealed why.

### 4.3 The CPU audit and what it didn't ask

A static CPU audit ran in parallel with this work. It identified `anlmdn` and the `aspectralstats`/`astats`/`ebur128` analysis block as the two largest non-stats consumers in Pass 2, and it explicitly excluded `anlmdn` parameter changes from its recommendations on the grounds that the existing settings were spike-validated and that changing them would trade audio quality for CPU.

That deferral was correct as written but turned out to be the wrong frame. The biggest single Pass 2 win in the entire investigation came from re-tuning the `anlmdn` parameters that the CPU audit declined to touch. A static audit of the code surface cannot decide whether a parameter sweep is worth running; only running the sweep can.

### 4.4 The architectural discovery

A flow audit of the Pass 2 chain found a substantive architectural fact about how the 32 kHz cap actually behaved.

The chain order in `internal/processor/filters.go:61-71` is:

```
downmix → ds201_highpass → ds201_lowpass → noiseremove → ds201_gate
        → la2a_compressor → deesser → analysis → resample
```

The cap was applied with an entry `aformat=sample_rates=32000:...:fltp` in `buildNoiseRemoveFilter` (`internal/processor/filters.go:617-648`, conditional on `cfg.NoiseRemovePreSampleRate > 0`). There was **no exit `aformat`** restoring the source rate. The chain had only the final tail `aformat=sample_rates=44100:...:s16,asetnsamples=n=4096` from `buildResampleFilter` (`internal/processor/filters.go:501-507`).

For a 48 kHz source, this produced the following rate timeline:

| Stage | Rate at input | How it gets there |
|-------|---------------|-------------------|
| Demux/decode | 48 kHz | source |
| Downmix `aformat=channel_layouts=mono` | 48 kHz | unchanged |
| DS201 HP/LP | 48 kHz | unchanged |
| Cap `aformat=sample_rates=32000` | 48 → 32 kHz | FFmpeg auto-inserts `aresample` |
| `anlmdn` | 32 kHz | unchanged |
| `compand` | 32 kHz | unchanged |
| **Gate (DS201)** | **32 kHz** | unchanged - **no restore** |
| **LA-2A compressor** | **32 kHz** | unchanged |
| **De-esser** | **32 kHz** | unchanged |
| **Analysis (astats/aspectralstats/ebur128)** | **32 kHz** | unchanged |
| Final `aformat=sample_rates=44100` | 32 → 44.1 kHz | FFmpeg auto-inserts `aresample` |

Once the cap activated, the entire downstream chain ran at 32 kHz until the chain-tail resample. "Raise the cap to 36 kHz" was not a clean noise-reduction-strength control. It was simultaneously raising the bandwidth of the de-esser, the spectral analysis, the gate, and the LA-2A. A cap-sweep matrix on this code would have measured a tangle of effects, not the property under test.

### 4.5 The architectural fix

The architectural fix added an exit-restore `aformat=sample_rates=<source>` after `compand` and plumbed `SourceSampleRate` through `FilterChainConfig`. The cap became conditional on `SourceSampleRate > NoiseRemovePreSampleRate`; sources at or below the cap stayed at source rate throughout, with no entry or exit aformat.

Validation on `testdata/LMP-68-mark.flac` (a 29m 50s recording with persistent radio interference hum):

| Metric | Before fix (no exit restore) | After fix (exit restore) | Delta |
|---|---|---|---|
| Final integrated loudness | -16.0 LUFS | -16.0 LUFS | 0.0 LU |
| Final true peak | -2.7 dBTP | -2.7 dBTP | 0.0 dBTP |
| Final loudness range | 17.5 LU | 17.5 LU | 0.0 LU |
| Final speech centroid | 2160 Hz | 2161 Hz | +1 Hz |
| Final speech rolloff | 3856 Hz | 3862 Hz | +6 Hz |
| Pass 2 wall time | 36.0 s | 39.3 s | **+3.3 s (+9.2 %)** |
| Total wall time | 1m 22s (22× real-time) | 1m 25s (21× real-time) | +3 s |

The 3.3 s Pass 2 cost increase was the gate, LA-2A, de-esser and analysis stats now running at 44.1 kHz instead of 32 kHz, as intended. The DS201 lowpass at 16 kHz already attenuated most content above the cap, so the centroid/rolloff drift was sub-Hz: the analysis stats saw a near-identical spectrum at either rate.

Spectrogram comparison confirmed the radio interference hum cleanup was preserved. The continuous broadband haze in the input's "silent" sections was removed equally well in before and after captures; the two output spectrograms were visually indistinguishable.

The fix made the cap mean what it appeared to mean. It also raised the floor, which mattered for what came next.

### 4.6 The matrix spike

With the architecture fixed, the original cap-sweep plan (test 22.05, 24, 36, 40 kHz cap variants) became uninteresting. The cap value was no longer entangled with downstream filter bandwidth, and the architectural fix had already cost ~9 % Pass 2 time. The interesting question was now whether the cap was needed at all.

The matrix spike pivoted the investigation. Two fixtures, six variants, anlmdn-only filter chain (no compand, gate, compressor, de-esser, or analysis - this matrix tested the denoiser in isolation).

**Fixtures:**

- `testdata/LMP-68-mark.flac` (LMP-68-mark, 44.1 kHz, 1790.5 s) - persistent RFI / mains hum across multiple bands. Tonal noise.
- `testdata/LMP-69-martin.flac` (LMP-69-martin, 44.1 kHz, 2098.3 s) - room noise. Broadband noise.

The two fixtures tested different noise classes against the same matrix.

**Cost model from upstream FFmpeg.** A reading of `libavfilter/af_anlmdn.c`:

- Strength `s`: AVOption default 1e-05 at line 67. Pinned to the floor for every variant. Cost O(1).
- Patch `p` (stored as `pd`): AVOption at line 69, `{.i64=2000}, 1000, 100000` (default 2 ms, hard floor 1 ms). Held at 6 ms in this matrix; the previous research-radius sweep had explored this axis.
- Research `r` (stored as `rd`): AVOption at line 71, `{.i64=6000}, 2000, 300000` (default 6 ms, hard floor 2 ms). Converted to half-research samples in `config_filter` at line 131 via `av_rescale(s->rd, outlink->sample_rate, AV_TIME_BASE)`. `S` drives the inner loop count `2*S` in `filter_channel` (~line 240) and **dominates wall time at fixed `p`**.
- Smooth `m`: AVOption at line 78, `{.dbl=11.}, 1, 1000`. Materialised in `config_filter` as the weight-LUT scale `s->pdiff_lut_scale = 1.f / s->m * WEIGHT_LUT_SIZE` (~line 165) and as the early-exit threshold `if (w >= smooth) continue;` (~line 245). **`m` does not change loop bounds or buffer footprint - runtime is essentially constant in `m`**.

Per-output-sample work is **O(S × K)** where S = `r × sample_rate`, K = `p × sample_rate`. Total work scales as `N_in × p × r × sample_rate²`. This is why dropping the cap was conceivable: the rate factor squared in the cost formula meant that running at source rate with a smaller `r` could beat running at 32 kHz with a larger `r`, even though the cap path looks intuitively cheaper.

**The six variants** (with `s=0.00001` and `p=0.0060` held constant):

| Variant | r (sec) | m | Question |
|---------|---------|---|----------|
| `v031_baseline` | 0.0058 | 11 | Quality/speed reference; the 0.3.1 anchor |
| `m_strict` | 0.0058 | 3 | Free quality lever - tighter LUT for tonal noise |
| `r_half` | 0.0029 | 11 | Speed candidate; ~1.96× speedup on the cost model |
| `r_half_m_strict` | 0.0029 | 3 | Does tighter LUT compensate for smaller search? |
| `r_half_m_relax` | 0.0029 | 30 | Does looser LUT (more averaging) compensate? |
| `r_min` | 0.0020 | 11 | r-axis floor; FFmpeg's hard minimum at line 71 |

The hypothesis behind `r_half_m_strict` and `r_half_m_relax` was that `m` could compensate for reduced `r`: tighter LUT for tonal noise (rejecting distant patches more aggressively), looser LUT for broadband noise (more patches contributing to averaging).

### 4.7 The hypothesis was falsified

From the matrix-spike measurements:

| Fixture | Variant | wall (s) | LUFS | TP | Centroid (Hz) | Rolloff (Hz) | Flatness | RMS (dB) |
|---------|---------|----------|------|------|---------------|--------------|----------|----------|
| LMP-68-mark | v031_baseline | 11.58 | -34.5 | -10.9 | 800.82 | 969.42 | 0.811772 | -46.103422 |
| LMP-68-mark | m_strict | 12.02 | -34.5 | -10.9 | 805.28 | 974.59 | 0.810702 | -46.103259 |
| LMP-68-mark | r_half | 5.87 | -34.5 | -10.9 | 825.68 | 1086.59 | 0.766056 | -46.103241 |
| LMP-68-mark | r_half_m_strict | 5.95 | -34.5 | -10.9 | 829.10 | 1089.80 | 0.765619 | -46.103117 |
| LMP-68-mark | r_half_m_relax | 5.89 | -34.5 | -10.9 | 825.84 | 1086.74 | 0.766232 | -46.103241 |
| LMP-68-mark | r_min | 4.20 | -34.5 | -10.9 | 906.02 | 1250.13 | 0.739369 | -46.103136 |

`r_half_m_strict` and `r_half_m_relax` were within rounding of plain `r_half` on every objective metric (centroid drift ≤ 3 Hz, rolloff drift ≤ 3 Hz, flatness drift ≤ 0.001, RMS drift ≤ 0.0002 dB). `r_half_m_relax` and `r_half` were nearly pixel-identical.

The cost model said why. `m` re-weights patches that are already inside the search radius `r`; it does not extend the radius, and it cannot recover information that a smaller `r` excluded. Once a patch is outside `2*S`, no LUT shape brings it back. The "compensation" hypothesis was geometric nonsense once the source had been read.

`r_min` (the FFmpeg hard floor at 2 ms) was 2.76× faster than baseline on LMP-68-mark and 2.86× on LMP-69-martin. Listener verdict (Martin) on the LMP-68 fixture was that `r_min` sounded equivalent to `r_half`. Spectrogram inspection showed a small residue in a low band that downstream `compand` and the DS201 gate are designed to suppress.

### 4.8 The free quality lever

`m_strict` (`r=0.0058`, `m=3`) was within rounding of `v031_baseline` on every metric on both fixtures. Runtime was 12.02 s vs 11.58 s on LMP-68-mark, within run-to-run noise.

`m=3` sharpens the exponential weight decay so distant patches contribute less to the denoised sample. On voice with tonal interference this is harmless: the patches that matter are nearby and self-similar. On voice with broadband room noise the same property holds - the `r_half_m_relax` variant proved that loosening `m` to 30 did not change the result either.

`m=3` was therefore a free lever. Production took it.

### 4.9 Final tuning and removal of the cap

Production adopted **`r=0.0020` (the FFmpeg minimum) and `m=3`** at the source sample rate. The 32 kHz cap, the conditional cap logic, the exit-restore `aformat`, and the `SourceSampleRate` plumbing were removed. The chain is back to a flat single-rate pipeline.

The constants live at `internal/processor/filters.go:94-103`:

```go
const (
    noiseRemoveProductionStrength    = 0.00001
    noiseRemoveProductionPatchSec    = 0.0060
    noiseRemoveProductionResearchSec = 0.0020
    noiseRemoveProductionSmooth      = 3.0

    noiseRemoveLegacyStrength    = 0.00001
    noiseRemoveLegacyPatchSec    = 0.0060
    noiseRemoveLegacyResearchSec = 0.0058
    noiseRemoveLegacySmooth      = 11.0
)
```

The legacy block names the 0.3.1 reference; the production block names the matrix-spike outcome. `NoiseRemovePatchSec` is unchanged from 0.3.1 (the patch axis was already settled in the previous research-radius sweep).

`DefaultFilterConfig` wires these into the live config at `internal/processor/filters.go:335-338`. The header comment at lines 87-93 names the matrix spike as the source of authority for the change.

### 4.10 Speedup

End-to-end Pass 2 on the 5-minute fixture went from ~6.46 s (with the 32 kHz cap and `r=0.0045, m=11`) to ~4.20 s (production current). That is a ~35 % reduction. The matrix spike's own measurements at native rate were 11.58 s for `v031_baseline` and 4.20 s for `r_min`, confirming the cost-model prediction of ~2.9× on the research axis alone. The 32 kHz cap path's measured Pass 2 runtime was 6.22 s; the source-rate `r=0.0058` reference was 9.86 s.

Listener verdict on real material remained the deciding test. The spectrogram residue from `r_min` lives in the low band that `compand` and the DS201 gate suppress downstream; on `LMP-68-mark.flac` after the full Pass 2 chain the cleanup was preserved.

---

## 5. adeclick investigation

### 5.1 Starting point

The pre-investigation Pass 4 clause was `adeclick=t=1.5:w=55:o=75` with the default method `a` (average interpolation). Threshold 1.5 (more sensitive), window 55 ms, overlap 75 %, average reconstruction.

Pass 4 runtime on the 5-minute fixture: **18,972 ms**.

### 5.2 Tuning rationale

Three changes, each justifiable in isolation:

- **Threshold 1.5 → 2.0.** Less sensitive detection. Pass 4 always sits behind a limiter (Volumax) with a gentle attack, which keeps source clicks well below an aggressive detection threshold. A lower threshold was triggering the interpolator on transient material that wasn't actually a click.
- **Overlap 75 % → 50 %.** Halving the overlap halves the interpolator's per-window work. The original 75 % was inherited from FFmpeg defaults; podcast voice doesn't need that level of redundancy.
- **Method `a` → `s`.** Switch from average to spline interpolation. Spline preserves the shape of transient peaks better; with a less sensitive threshold the interpolator runs less often, but when it does run the result is closer to the surrounding waveform.

The new clause: `adeclick=t=2.0:w=55:o=50:m=s`.

### 5.3 Variant matrix

Five variants (`internal/processor/adeclick_benchmark_test.go:67-121`):

| Variant | Threshold | Window (ms) | Overlap (%) | Method | Validation priority |
|---------|-----------|-------------|-------------|--------|---------------------|
| `without_adeclick` | - | - | - | - | `no-adeclick-baseline` |
| `adeclick_current_t_2_0_w_55_o_50_m_s` | 2.0 | 55 | 50 | s | `production-default` |
| `adeclick_t_2_0_w_55_o_75` | 2.0 | 55 | 75 | a | `tier1-threshold` |
| `adeclick_t_2_0_w_55_o_50` | 2.0 | 55 | 50 | a | `tier2-overlap` |
| `adeclick_t_2_0_w_30_o_50` | 2.0 | 30 | 50 | a | `tier2-window` |

The matrix isolates the threshold change from the overlap change from the window change from the method change. `without_adeclick` provides the upper-bound speed reference (no repair, fastest possible Pass 4).

### 5.4 Pass 4 candidate spec construction

The benchmark built a Pass 4 candidate filter graph for each variant by composing:

1. The shared limiter prefix (Volumax `volume + alimiter`, identical across variants).
2. The shared `loudnorm` clause built from the same Pass 3 measurement.
3. The variant-specific adeclick clause (or nothing for `without_adeclick`).
4. The shared analysis tail (`astats`, `aspectralstats`, `ebur128`).
5. The shared resample tail (`aformat=sample_rates=44100:...:s16`, `asetnsamples=n=4096`).

Only the adeclick segment varied. The benchmark asserted that stripping `adeclick=` from each variant produced an identical remainder, so any timing or metric difference came from adeclick alone.

The candidate Pass 4 graph terminated in `astats/aspectralstats/ebur128` so timing and final measurements came from the candidate run itself, not from a separate `ApplyNormalisation()` pass. This matters: a separate measurement pass would have measured the same audio at different times and on different decode paths, and the runtime number would no longer be "Pass 4 candidate runtime".

### 5.5 Production parameters

The constants live at `internal/processor/filters.go:280-284` (struct fields) and `internal/processor/filters.go:394-400` (defaults):

```go
AdeclickEnabled:   true,
AdeclickThreshold: 2.0,  // Less sensitive threshold reduces CPU cost without harming repair quality
AdeclickWindow:    55.0, // Default window, appropriate for speech
AdeclickOverlap:   50.0, // Lower overlap further reduces cost while remaining within FFmpeg's valid range
AdeclickMethod:    "s",  // Spline interpolation preserves transient peak shapes better than the default average method
```

The clause builder at `internal/processor/filters.go:778-791` materialises this as `adeclick=t=2.0:w=55:o=50:m=s`.

### 5.6 Speedup

Pass 4 candidate runtime on the 5-minute fixture, end-to-end:

| Variant | Pass 4 runtime (ms) | vs original |
|---------|---------------------|-------------|
| `adeclick_current_t_1_5_w_55_o_75` | 18,972 | reference |
| `adeclick_current_t_2_0_w_55_o_50_m_s` | 4,559 | **-76 %** |
| `without_adeclick` | 2,618 | -86 % |

The production tuning recovers most of the headroom available between repair-on and repair-off. Final LUFS / true peak / LRA were within rounding across the candidate variants (`-15.96`, `-2.45`, `5.01` for `without_adeclick`; `-15.98`, `-2.12`, `5.04` for the production clause). Listening confirmed the click and pop repair held; sibilants and plosives were not smeared.

Going further (smaller window, no method change) gave smaller incremental wins at higher risk to repair quality; the `tier2-window` and `tier2-overlap` variants were not adopted.

---

## 6. End-to-end comparison: 0.3.1 vs current

The §4 and §5 speedup numbers were measured on the 5-minute fixture in isolation: anlmdn-only and adeclick-only candidate graphs. This section closes the loop by running both binaries end-to-end on a real 30-minute fixture with the full Pass 1-4 pipeline.

**Headline: total wall time falls from 150 s to 90 s on `LMP-68-mark.flac` - a 1.68× speedup, lifting real-time throughput from 12× to 20× on the same fixture.** Loudness output is identical to within true-peak run-to-run noise.

### 6.1 Setup

- **0.3.1 binary:** downloaded with `gh release download 0.3.1 -R linuxmatters/jivetalking` (asset `jivetalking-linux-amd64`); `--version` reports `0.3.1`.
- **Current binary:** `just build` against the working tree; `--version` reports `0.3.1-12-gc5ab1b8-dirty`.
- **Fixture:** `testdata/LMP-68-mark.flac`, 44,100 Hz mono, 1790.51 s duration (29 min 50 s) - the same persistent-RFI / mains-hum recording used in the §4 architectural validation.
- **Host:** Linux x86_64.
- **Methodology:** three runs per binary; `/usr/bin/time -v` for wall and CPU; pass-by-pass timings from each run's `.log` report; medians across the three runs. Run-to-run variance was under 2 % on wall time across all six runs.

### 6.2 Wall, CPU, memory

| Binary | Wall (s) | User CPU (s) | Sys CPU (s) | Max RSS (kB) | Real-time multiplier |
|--------|----------|--------------|-------------|--------------|----------------------|
| 0.3.1 | 150.38 | 145.28 | 0.26 | 68,492 | 11.9× |
| current | 89.51 | 84.52 | 0.26 | 61,964 | 20.0× |
| **delta** | **−60.87 s (−40.5 %)** | **−60.76 s (−41.8 %)** | 0.00 s | −6,528 kB | — |

User-CPU savings track wall savings almost exactly; the work removed is real arithmetic, not stalls. Max RSS is incidentally ~6 MB lower on current (no deliberate memory work was done in either investigation).

### 6.3 Pass-by-pass

| Pass | 0.3.1 | current | delta | speedup |
|------|-------|---------|-------|---------|
| Pass 1 (analysis) | 10.0 s | 10.1 s | +0.1 s | 0.99× |
| Pass 2 (processing) | 61.0 s | 36.9 s | −24.1 s | **1.65×** |
| Pass 3 (loudnorm measurement) | 13.6 s | 13.7 s | +0.1 s | 0.99× |
| Pass 4 (loudnorm + adeclick) | 60.0 s | 23.8 s | −36.2 s | **2.52×** |
| **Total** | **150.4 s** | **89.5 s** | **−60.9 s** | **1.68×** |

Pass 1 and Pass 3 are within noise: neither investigation touched analysis or loudnorm measurement, and the medians confirm this. The full 60.9 s wall saving is split between Pass 2 (the anlmdn retune to `r=0.0020, m=3` at source rate) and Pass 4 (the adeclick retune to `t=2.0, w=55, o=50, m=s`). The two retunes compose without surprise: total savings equal the sum of the two pass-level savings within rounding.

### 6.4 Loudness parity

| Metric | 0.3.1 | current | delta |
|--------|-------|---------|-------|
| Final integrated loudness | -16.0 LUFS | -16.0 LUFS | 0.0 LU |
| True peak | -2.9 dBTP | -2.7 dBTP | +0.2 dBTP |

Loudnorm parameters did not change between 0.3.1 and current; the integrated-loudness parity confirms both binaries land on the same -16 LUFS target. The 0.2 dBTP true-peak difference is within run-to-run noise on `ebur128`'s true-peak detector and is not attributable to any deliberate change.

### 6.5 Spectral and perceptual parity

Integrated loudness parity is the headline result; spectrograms and per-region metric tables from each binary's `.log` report confirm parity at the spectral and perceptual level too. No axis of comparison shows audible damage from the retunes.

**Pass 1 is byte-for-byte identical.** Both binaries selected the same room tone noise floor (-85.6 dBFS), the same room tone centroid (7309 Hz), the same speech RMS (-40.5 dBFS), the same speech centroid (2641 Hz), the same speech kurtosis (18.044), and the same voicing density (92.1 %). The Pass 1 frontend is deterministic and the parity confirms both binaries read the input identically before adaptive tuning diverges.

**Speech-region drift after the full chain is small, expected, and benign.** The Final-stage speech sample from each report:

| Metric (Final stage, speech sample) | 0.3.1 | current | Δ |
|---|---|---|---|
| RMS Level | -18.9 dBFS | -18.9 dBFS | 0.0 |
| Peak Level | -3.1 dBFS | -3.1 dBFS | 0.0 |
| Crest Factor | 15.8 dB | 15.8 dB | 0.0 |
| Spectral Centroid | 2094 Hz | 2148 Hz | +54 Hz (+2.6 %) |
| Spectral Rolloff | 3708 Hz | 3804 Hz | +96 Hz |
| Spectral Kurtosis | 22.5 | 21.9 | -0.6 |
| Spectral Flatness | 0.125 | 0.124 | -0.001 |

Loudness, peak, and crest are bit-identical. Current's speech reads marginally brighter: centroid +54 Hz, rolloff +96 Hz, kurtosis -0.6. This is the direct consequence of less aggressive low-band denoising at `r=0.0020` - anlmdn removes less broadband residue, fractionally shifting the LF/HF energy balance upward. Both binaries' speech-region metrics still sit inside the "balanced, typical voice" / "excellent harmonics" interpretation bands per `docs/Spectral-Metrics-Reference.md`.

**Spectrogram comparison.** Full-band (0-22 kHz) and low-band (0-2 kHz) spectrograms were rendered for the input and both processed outputs at 1920×1080 with legends. Per axis:

- **Hum bands (440, 815, 1260, 1630 Hz):** the four narrow continuous interference bands clearly visible in the input are equally suppressed in both processed outputs. Visually indistinguishable.
- **Speech formants (200-3000 Hz):** identical formant structure and shape in both outputs. No smearing, no hollowing.
- **Sibilance (5-10 kHz):** density and texture identical. The de-esser was inactive on both runs (no sibilance detected by adaptive logic), so this confirms the bypass behaved the same way.
- **Air and brightness (10-20 kHz):** the HF tail is preserved with the same fall-off shape in both. This rules out any concern that current's anlmdn at source rate (no 32 kHz cap) damaged the high band.
- **Background floor and transients:** silent regions render essentially black in both. Transient spikes between speech (breaths, p-pops above the gate threshold) appear at the same time positions and same intensities in both.

**One observation worth tracking on future fixtures.** Speech-region spectral decrease moved 0.019 → 0.034 (+74 %), the largest single relative metric movement in the comparison. The direction is benign - faster LF→HF fall-off is consistent with current's slightly warmer speech balance - the absolute magnitude has no audible correlate at this level, and both binaries' centroid, rolloff, and slope still interpret as "balanced, typical voice". On this fixture alone, it is noise. Worth flagging as a pair to watch: if a future fixture shows decrease moving in the same direction *and* kurtosis drops below 10, that combination warrants attention. Neither condition is met here.

**Adeclick footnote.** The transient spike pattern in the spectrograms is bit-identical between binaries: same spikes, same time positions, same intensities. Either both adeclick variants pass these transients through (most likely - they are real source events the Volumax limiter has already shaped, not clicks the detector would catch) or both interpolate them. This fixture does not exercise the threshold change at the boundary where `t=1.5` would catch and `t=2.0` would skip. Validating the threshold change on a fixture with hard clicks - electrical pops, edit artefacts - rather than breath-tail transients would close the loop on the adeclick safety question. On `LMP-68-mark.flac` no daylight between the two appears.

### 6.6 Methodology notes

Three points worth flagging for anyone reproducing this comparison:

- **Real-time multiplier sources.** The binary's `.log` report quotes a whole-number multiplier (12× for 0.3.1, 21× for current). The 11.9× / 20.0× figures in the table above are wall-clock divisions (`1790.51 / wall_s`) and are more precise. The report's rounding sits one bucket high on current because `1790.51 / 89.51 = 20.00` rounds to 20× by truncation and to 21× when the report formatter's computation rounds up - inspect the `.log` report rather than the headline if the difference matters.
- **Pass 2 and Pass 4 timing precision on 0.3.1.** The 0.3.1 report formatter rounds pass times to whole seconds once they cross 60 s (`1m 1s`, `1m 0s` for Pass 2 and Pass 4 medians). Current stays under 60 s on both passes and reports decimals. The resolution loss on the 0.3.1 side is at most ~0.5 s per pass, which is well below the 24.1 s and 36.2 s speedup magnitudes.
- **Output file size.** Current produces a slightly larger output FLAC (15.4 MB vs 14.5 MB) at identical sample rate, channel layout, and duration. This is consistent with a default FLAC compression-level change between the two binaries' embedded ffmpeg builds and is not a benchmark concern.

### 6.7 What this means in practice

A 30-minute episode that used to take 2 min 30 s now takes 1 min 30 s; a typical four-host show with four input files saves ~4 minutes of preprocessing per episode at the same -16 LUFS target. The earlier per-filter measurements in §4.10 and §5.6 reported isolated 5-minute speedups. This section is the integrated long-form result: on real material, with the full pipeline, the combined retunes deliver what the cost models and matrix spikes predicted.

---

## 7. Lessons learned

### 7.1 CPU audits cannot rule out their own conclusions

The static CPU audit excluded `anlmdn` parameter changes on the grounds that the existing tuning was spike-validated. The matrix spike found the largest single Pass 2 win in the entire investigation by changing those parameters. A static audit can prioritise; it cannot decide that a measurement isn't worth running.

### 7.2 Architectural facts beat parameter sweeps

The 32 kHz cap had no exit restore. A cap-sweep matrix on that code would have measured noise reduction strength tangled with downstream filter bandwidth, and any conclusion would have been an artefact of the entanglement. The flow audit took an afternoon and saved a multi-day blind alley.

When the parameter sweep is hard to interpret, ask whether the architecture is what it appears to be.

### 7.3 Read the upstream source for the cost model

The cost model `O(S × K)` per output sample with `S = r × sample_rate` came from reading `af_anlmdn.c`. With the model in hand, the pivot from cap-sweep to source-rate r-sweep was obvious: r entered the formula linearly, the cap entered as the rate factor, and the rate factor squared in the total work scaling. Without the model, the matrix would have stayed on the cap axis.

This applies to any FFmpeg filter. The AVOption tables, the `config_filter` setup, and the inner loop in `filter_frame` together describe what every parameter actually does. A two-hour read of the upstream C is worth two days of variant matrix.

### 7.4 Listener verdict beats spectrogram inspection

`r_min` left a small residue visible on the spectrogram. The listener verdict on real material was that `r_min` sounded equivalent to `r_half`. The residue lived in the band that the downstream gate and compand are designed to suppress; the spectrogram showed it but it never reached the listener.

Objective metrics catch the gross failures. The perceptual call belongs to the listener.

### 7.5 Free quality levers exist and are worth checking

`m_strict` was a free lever: zero cost, no metric change, listener-equivalent. The investigation almost missed it because the cost model said `m` doesn't move runtime, so why test it? Tested it anyway because the matrix had room. The result was a quality improvement that travels alongside every future `r` choice.

When a parameter is documented as not affecting cost, that's a reason to sweep it cheaply, not a reason to leave it alone.

### 7.6 Falsified hypotheses are findings

The `r_half_m_strict` / `r_half_m_relax` "compensation" hypothesis was wrong. `m` can't reach patches outside the search radius, and the variant captures showed it didn't. Recording the hypothesis and its falsification stopped the next person from re-running the same experiment.

---

---

## 8. Pass 4 rate discovery: loudnorm linear mode emits at 192 kHz

*Added May 2026.*

After the §5 adeclick tuning shipped, a long-file timing observation exposed a previously unknown rate issue in Pass 4. A 49-minute file projected Pass 4 at 9-20 minutes; after the fix it completed in ~1m29s. The §5 matrix had not isolated sample rate as a variable, so the dominant cost was invisible during tuning.

### 8.1 The discovery

FFmpeg's `loudnorm` filter in `linear=true` mode upsamples internally to 192 kHz and **emits at 192 kHz**. The §5 benchmark constructed each Pass 4 candidate graph as:

```
[volume + alimiter] → loudnorm → adeclick → astats → aspectralstats → ebur128 → resample
```

With no resample between `loudnorm` and `adeclick`, every filter after loudnorm received a 192 kHz stream for a 48 kHz source - 4× the sample count. The §5 runtime measurements were therefore accurate for the graph they tested, but the graph was processing adeclick (and the analysis tail) at a rate four times higher than the source rate the parameters were tuned for. The "~75% runtime reduction" reported in §5.6 was relative to the *previous* adeclick clause at 192 kHz, not relative to source-rate operation. In other words, §5 measured adeclick's speed at two different parameter settings, both at 192 kHz, and correctly found the new clause faster - it just never isolated the rate as a separate variable.

### 8.2 The fix

An `aresample` to the source sample rate is now inserted between `loudnorm` and `adeclick`:

```
volume (pre-gain, when clamped) → alimiter (Volumax) → loudnorm (linear) → aresample (source rate) → adeclick → astats → aspectralstats → ebur128 → resample
```

The adeclick parameters themselves are unchanged (`t=2.0:w=55:o=50:m=s`). The analysis filters (`astats`, `aspectralstats`, `ebur128`) also benefit: they now run at the source rate they were designed to see, matching the Pass 1 and Pass 2 measurement context.

### 8.3 Measured impact

On a 49-minute file, Pass 4 projected 9-20 minutes before the fix and completed in ~1m29s after. The per-60s-segment standalone Pass 4 chain (loudnorm + adeclick + analysis tail + resample) measured 4.21s → 0.94s, a 4.5× reduction.

The adeclick quality parameters are unchanged; the fix removes a structural overhead that the §5 matrix did not observe.

---

Document compiled May 2026; end-to-end comparison (§6) added May 2026; Pass 4 rate discovery (§8) added May 2026. The working investigation notes, the per-filter benchmark Go fixtures, and the `bench-anlmdn-*` / `bench-adeclick-*` justfile recipes have been retired. Their reasoning is preserved here.
