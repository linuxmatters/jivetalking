# Jivetalking CPU Performance Overview

## Scope

This review covers CPU-bound performance on the primary audio processing path in Jivetalking.

Included:

- Full processing mode: `ProcessAudio`, `AnalyzeAudio`, `processWithFilters`, `ApplyNormalisation`, and output region measurement.
- Analysis-only mode where it shares the same CPU-heavy analysis path.
- FFmpeg filter graph CPU use, repeated decode/filter passes, per-frame metadata extraction, per-sample Go loops, and CPU-relevant test affordances.

Excluded:

- I/O-only changes, network, build-time work, TUI polish, debug logging, text report formatting unless profiling says otherwise, and memory-only changes.
- Metric collection for reports and future GUI product data remains in scope when it consumes CPU on the processing path.
- Changes that trade audio quality for speed without profile data and listening validation.
- Broad pipeline redesign.

## Methodology

This is a static CPU-path audit of the Go code and local FFmpeg filter options.

No runtime CPU profile was captured for this document. Treat the impact ratings as candidate priorities. Do not implement CPU optimisations until a profile confirms the bottleneck on representative recordings.

The product framing has changed since the first review. Detailed reports are likely to become always-on because the metrics are useful now and will become product data for a future GUI app. The optimisation target is therefore no longer "avoid full analysis unless `--logs` is requested". The target is "collect the required default metric set as cheaply as possible, then reserve deeper diagnostic fields for an explicit diagnostic tier".

Files inspected:

- `cmd/jivetalking/main.go`
- `internal/audio/reader.go`
- `internal/processor/processor.go`
- `internal/processor/frame_processor.go`
- `internal/processor/filters.go`
- `internal/processor/analyzer.go`
- `internal/processor/analyzer_metrics.go`
- `internal/processor/analyzer_candidates.go`
- `internal/processor/analyzer_output.go`
- `internal/processor/normalise.go`
- `internal/processor/encoder.go`
- existing processor tests and `justfile`

Local FFmpeg help confirmed that `astats` and `aspectralstats` both support selecting measured fields, and that `aspectralstats` uses slice threading.

## CPU-Critical Paths

### Processing Mode

`ProcessAudio` currently performs these CPU-heavy stages for each input file:

1. Pass 1, `AnalyzeAudio`
   - Decodes the input.
   - Runs `downmix -> astats -> aspectralstats -> ebur128`.
   - `ebur128` calculates true peak and upsamples internally.
   - Go scans input samples for interval RMS and peak.
   - Go extracts and parses frame metadata for all analysis metrics.

2. Pass 2, `processWithFilters`
   - Decodes the input again.
   - Runs `downmix -> highpass -> lowpass -> anlmdn -> compand -> agate -> acompressor -> deesser -> astats -> aspectralstats -> ebur128 -> resample`.
   - Encodes FLAC output.
   - Extracts output analysis metadata on each filtered frame.

3. Pass 2 region measurement
   - `MeasureOutputRegions` reopens the processed file.
   - It measures silence and speech regions with separate filter graph runs when both profiles exist.
   - The shared reader seeks back to zero for the speech region, so this can decode the processed file twice.

4. Pass 3, `measureWithLoudnorm`
   - Decodes the processed file.
   - Runs optional `volume -> alimiter`, then `loudnorm` in measurement mode.

5. Pass 4, `applyLoudnormAndMeasure`
   - Decodes the processed file again.
   - Runs optional `volume -> alimiter -> loudnorm -> adeclick -> astats -> aspectralstats -> ebur128 -> resample`.
   - Encodes FLAC output again.

6. Pass 4 region measurement
   - Repeats the post-output region measurement pattern on the final file.

### Analysis-Only Mode

`AnalyzeOnly` runs Pass 1 and `AdaptConfig`.

Its CPU cost is dominated by decode, `astats`, `aspectralstats`, `ebur128`, Go sample scanning, and per-frame metadata parsing.

### Go Hot Loops

The Go-side CPU candidates are smaller than the FFmpeg filter costs, but they run once per frame:

- `frameSumSquaresAndPeak` scans every sample in a frame.
- `AnalyzeAudio` calls `calculateFrameLevel` and then `intervalAcc.addFrameRMSAndPeak`; both call `frameSumSquaresAndPeak`, so Pass 1 scans the same input frame samples twice.
- `getFloatMetadata` performs repeated dictionary lookups and `strconv.ParseFloat` calls for many metadata keys on every analysis frame.
- Candidate scoring works over 250 ms intervals. This is cheap compared with FFmpeg filters for normal podcast durations.

## Findings

### 1. Post-processing region measurement re-decodes output files

**Expected Impact:** Remove up to four extra output-file decode/filter traversals per processed file when both silence and speech profiles exist. This becomes normal-path CPU work if reports and GUI metrics are always-on. The saving should be clearly measurable on long recordings and may be human-perceptible for batch processing.

**Evidence:**

- `ProcessAudio` calls `MeasureOutputRegions` after Pass 2.
- `applyLoudnormAndMeasure` calls `MeasureOutputRegions` again after Pass 4.
- `MeasureOutputRegions` measures silence first, seeks to the beginning, then measures speech.
- `measureOutputRegionFromReader` builds an `atrim -> astats -> aspectralstats -> ebur128` graph and drives it through `runFilterGraph`.

**Implementation Plan:**

- M: Add a small region accumulator that can consume already-filtered frames during Pass 2 and Pass 4.
- S: Use frame timestamps to decide whether a frame overlaps the elected silence or speech region.
- M: For each selected region, accumulate raw RMS/peak from frame samples and average per-frame spectral/loudness metadata.
- S: Keep `MeasureOutputRegions` as a fallback for tests or debug comparison during rollout.
- S: Compare old and new region metrics on existing fixtures.

**Risk Assessment:** Medium. Audio output is unchanged, but metric parity needs careful tolerance checks because `atrim`-scoped `astats` and streaming accumulators may differ at region boundaries.

**Effort Estimate:** M

**Impact Rating:** 8/10

**Measurement:**

- Run full processing with the always-on default metric set on 10, 30, and 60 minute fixtures.
- Until metrics become default, use `--logs` as the proxy for that baseline.
- Compare total wall time, pass timings, and CPU profile before and after.
- Check region RMS, peak, centroid, LUFS, true peak, and sample peak deltas against existing `MeasureOutputRegions`.

### 2. Full output analysis needs a default metric contract

**Expected Impact:** Reduce CPU in Pass 2 and Pass 4 by replacing all-field output analysis with a defined default app/report metric set plus an optional diagnostic tier. This should be material on normal episode-length files because it limits FFT-heavy and true-peak analysis to fields the default product path actually uses.

**Evidence:**

- `ProcessAudio` sets `config.OutputAnalysisEnabled = true` unconditionally.
- Pass 2 filter order includes `FilterAnalysis` before resampling.
- `buildAnalysisFilter` uses `astats=metadata=1:measure_perchannel=all`, `aspectralstats=...:measure=all`, and `ebur128=...:peak=sample+true`.
- Pass 4 `buildLoudnormFilterSpec` appends `astats`, `aspectralstats`, `ebur128`, and `aformat` after `loudnorm`.
- `LoudnormStats` already includes `output_i` and `output_tp`, so final filename data can come from loudnorm JSON if validated.

**Implementation Plan:**

- S: Define a default app/report metric contract covering processing decisions, output naming, user-facing report fields, and future GUI data.
- S: Define a diagnostic tier for deeper before/after comparisons, validation, and developer investigations.
- M: In normal processing, collect the default metric contract instead of every available `astats`, `aspectralstats`, and `ebur128` field.
- M: Use diagnostic metrics only when an explicit diagnostic path, comparison run, or test requires them.
- S: Parse and validate `LoudnormStats.OutputI` and `OutputTP` for final LUFS and true peak where Pass 4 currently relies on the trailing `ebur128` accumulator.
- M: Keep a strict comparison mode until loudnorm JSON and validation `ebur128` agree within tolerance across fixtures.

**Risk Assessment:** Medium. The audio samples should remain unchanged, but report and GUI data become a product contract. Narrowing full analysis must preserve required default fields and keep diagnostic parity available.

**Effort Estimate:** M

**Impact Rating:** 7/10

**Measurement:**

- Profile Pass 2 and Pass 4 with current all-field analysis, the proposed default metric contract, and the diagnostic tier.
- Record CPU samples attributed to `aspectralstats`, `astats`, `ebur128`, metadata extraction, and FLAC encode.
- Verify final LUFS, true peak, output filename, default report fields, and GUI-bound metric fields.

### 3. Pass 2 and Pass 4 request more metadata than the default metric contract may need

**Expected Impact:** Reduce per-frame FFmpeg analysis CPU and Go metadata parsing in the output paths while keeping always-on report and app metrics. This matters if metrics become part of the normal product path.

**Evidence:**

- `astats` supports selecting measured fields.
- `aspectralstats` supports selecting measured fields.
- The code parses many fields every frame through `getFloatMetadata`.
- Pass 2 and Pass 4 need enough loudness, peak, spectral, and region data to satisfy the default report and future GUI contract. Full all-field spectral comparison is mainly diagnostic unless a field is explicitly promoted into that contract.

**Implementation Plan:**

- S: Inventory fields consumed by adaptive tuning, limiter decisions, filename generation, console output, default reports, future GUI views, and diagnostic reports.
- M: Split analysis builders by pass and purpose.
- S: Use `astats` selected fields instead of `measure_perchannel=all` where the default contract does not need every field.
- S: Use `aspectralstats` selected fields where the default contract needs spectral data, and keep `measure=all` for the diagnostic tier.
- S: Add regression tests for filter specs and report field presence.

**Risk Assessment:** Low to medium. The main risk is accidentally removing a metric used by adaptive tuning or reports. Pass 1 should stay conservative until profiling proves a narrower metric set preserves decisions.

**Effort Estimate:** M

**Impact Rating:** 6/10

**Measurement:**

- Use `ffmpeg` `abench` around analysis subchains or CPU profiles around Jivetalking passes.
- Compare processing time and default report metrics with `measure=all` versus selected fields.
- Measure diagnostic-tier runtime separately from default metric collection.
- Assert adaptive config remains identical on representative fixtures before changing Pass 1.

### 4. Pass 1 scans input frame samples twice

**Expected Impact:** Reduce Go CPU in analysis-only mode and Pass 1. This is smaller than filter-pass reductions, but it is deterministic and low risk if profiles show Go-side sample scanning above noise.

**Evidence:**

- `AnalyzeAudio` calls `calculateFrameLevel(inputFrame)`.
- It then calls `intervalAcc.addFrameRMSAndPeak(inputFrame)`.
- Both paths call `frameSumSquaresAndPeak`, which iterates all samples in the frame.

**Implementation Plan:**

- XS: Add a helper returning display level plus interval sum-of-squares, sample count, and peak from one `frameSumSquaresAndPeak` call.
- S: Use the same result for progress level and interval accumulation in Pass 1.
- XS: Keep Pass 2 and Pass 4 unchanged unless profiles show VU level calculation is meaningful CPU cost.

**Risk Assessment:** Low. The change reuses the same calculation and should preserve existing level and interval values.

**Effort Estimate:** S

**Impact Rating:** 5/10

**Measurement:**

- Add a benchmark for `AnalyzeAudio` on a synthetic 5 to 10 minute file.
- Run `go test -bench AnalyzeAudio -cpuprofile cpu.out ./internal/processor`.
- Confirm fewer samples in `frameSumSquaresAndPeak` and no metric drift.

## Recommendations

1. Profile before changing code.
   - Use representative real fixtures, not the current 3 second integration test.
   - Capture full processing with always-on default metrics and analysis-only separately.
   - Record pass timings already available through the existing progress/report path.

2. Prioritise streaming region measurement.
   - It removes repeated work without changing the audio processing chain.
   - It has the clearest CPU waste pattern in the current code.
   - Always-on metrics make the repeated region traversal part of the normal path.

3. Define the default app/report metric contract.
   - Keep required report and future GUI metrics in the normal path.
   - Move developer-only comparisons and deep validation fields into a diagnostic tier.

4. Narrow FFmpeg metric selection only after the default contract exists.
   - This avoids treating product metrics as incidental logging.

5. Fold the duplicate Pass 1 sample scan into one calculation.
   - This is small, local, and easy to verify.

## Suggested Benchmarks And Measurements

### Baseline Runs

Use at least three fixture lengths:

- Short: 3 to 5 minutes.
- Typical: 30 to 60 minutes.
- Long: 90 minutes or more.

Capture:

- Total elapsed time.
- User CPU time.
- System CPU time.
- Pass 1, Pass 2, Pass 3, and Pass 4 timings.
- Output LUFS and true peak.
- Region metric deltas.
- Runtime with current all-field metrics.
- Runtime with the proposed default metric contract.
- Runtime with the diagnostic tier enabled.
- Runtime of text report generation separately from metric collection, if profiling suggests it is visible.

### Go Benchmarks

Add benchmarks under `internal/processor`:

- `BenchmarkAnalyzeAudioSynthetic5m`
- `BenchmarkProcessAudioDefaultSynthetic5m`
- `BenchmarkProcessAudioFixture`
- `BenchmarkMeasureOutputRegions`

Run:

```bash
go test -run '^$' -bench 'Benchmark(AnalyzeAudio|ProcessAudio|MeasureOutputRegions)' -benchmem -cpuprofile cpu.out ./internal/processor
go tool pprof cpu.out
```

Add narrower benchmarks or sub-benchmarks for:

- Default metric collection.
- Selected `astats` and `aspectralstats` fields.
- Diagnostic-tier all-field analysis.
- `MeasureOutputRegions`.
- Text report generation, only if CPU profiles show report formatting above noise.

### CLI-Level Timing

Run the built binary so CGO and version-injected build settings match normal use:

```bash
just build
/usr/bin/time -v ./jivetalking --analysis-only testdata/LMP-72-martin.flac
/usr/bin/time -v ./jivetalking testdata/LMP-72-martin.flac
/usr/bin/time -v ./jivetalking --logs testdata/LMP-72-martin.flac
```

Treat the always-on report or metric path as the baseline. Until the CLI default changes, the `--logs` run is the closest proxy for always-on default metrics. Keep text report generation separate from metric collection when profiling report-related cost.

### FFmpeg Filter Timing

For temporary experiments, insert FFmpeg `abench` around candidate subchains:

- Pass 2 analysis block.
- Pass 4 validation analysis block.
- `anlmdn` and `compand`.
- `aspectralstats`.
- `ebur128`.

Remove `abench` instrumentation before merging.

## Exclusions

### Multi-file parallel processing

Processing multiple files concurrently may reduce wall-clock time for batches, but it does not reduce CPU consumed per file. It also increases instantaneous CPU demand. Excluded from this CPU-reduction plan.

### `anlmdn` parameter changes

`anlmdn` is likely CPU-heavy, but the current settings are documented as spike-validated. Changing patch, research, or smoothing parameters would trade audio quality for CPU. Excluded until profiling and listening tests justify it.

### FLAC compression level

The encoder uses FLAC compression level 5. Lowering it could reduce encode CPU, but it trades CPU for larger files and more downstream I/O. Excluded until CPU profiles show FLAC encoding is a major share of processing time.

### `--silence-scan-duration` as a primary CPU fix

The flag caps the silence-candidate slice used after Pass 1 collection. It does not reduce full-file decode, `astats`, `aspectralstats`, or `ebur128` work. It can reduce candidate scoring on very long files, but this is unlikely to reach impact 5 on normal inputs.

### Filter string construction and small allocations

Filter specs are built once per pass. String construction, builder maps, and config formatting are not meaningful CPU targets for the primary audio path.

### TUI and logging

Progress messages, Bubbletea rendering, debug logging, and text report formatting are outside scope unless a CPU profile shows they materially affect processing runs. The current update interval is already coarse.

Metric collection is not logging. Always-on report and GUI metrics are part of the CPU-critical processing path when they require FFmpeg analysis filters, frame metadata parsing, or region traversal.
