---

# Benchmarks

**Reference for the `just bench-*` recipes: processor microbenchmarks, full CLI runs, and the focused `anlmdn` and `adeclick` variant matrices used to validate filter-chain changes.**

Benchmark artefacts live under `.bench/`. They are separate from the per-file `.log` reports written alongside processed audio.

---

## Processor Package Benchmarks

Go benchmarks against the `internal/processor` package. Useful for measuring the cost of filter-graph construction, adaptation, and analysis paths in isolation.

```bash
# Processor package benchmarks
just bench

# Processor benchmarks with CPU profile at .bench/cpu.out
just bench-profile
```

---

## Full CLI Benchmarks

End-to-end runs of the `jivetalking` binary against a copied input file. These exercise the full four-pass pipeline (or just Pass 1 for analysis-only) and are the closest measure of real-world processing time.

```bash
# Full CLI processing benchmark against a copied input file
just bench-cli FILE

# Analysis-only CLI benchmark against a copied input file
just bench-analysis FILE
```

---

## Noise Removal (`anlmdn`) Variant Matrix

Compares candidate `anlmdn` configurations against the production default. Set `JIVETALKING_BENCH_FIXTURE` to an absolute path; the recipes do not resolve relative paths.

```bash
# Focused anlmdn variant benchmarks
JIVETALKING_BENCH_FIXTURE="$(pwd -P)/testdata/fixture-5m.flac" just bench-anlmdn

# Capture processed outputs, filter specs, metrics, validation reports, and timing
just bench-anlmdn-capture

# Profile the production anlmdn variant, anlmdn_sr_32000_best_r
JIVETALKING_BENCH_FIXTURE="$(pwd -P)/testdata/fixture-5m.flac" just bench-anlmdn-profile

# Profile the legacy default for comparison
JIVETALKING_ANLMDN_PROFILE_VARIANT=anlmdn_legacy_default \
  JIVETALKING_BENCH_FIXTURE="$(pwd -P)/testdata/fixture-5m.flac" \
  just bench-anlmdn-profile
```

`anlmdn_sr_32000_best_r` is the production default. It caps the signal to 32 kHz before `anlmdn`, uses `r=0.0045`, and restores the normal 44.1 kHz mono output path afterwards. `anlmdn_legacy_default` keeps the old uncapped path with `r=0.0058` for comparison.

---

## Click Removal (`adeclick`) Variant Matrix

Compares the current production `adeclick` clause against a no-adeclick reference and three threshold/window/overlap candidates. Capture writes per-variant artefacts to `.bench/adeclick/<variant>/`.

```bash
# Focused Pass 4 adeclick variant benchmarks
JIVETALKING_BENCH_FIXTURE="$(pwd -P)/testdata/fixture-5m.flac" just bench-adeclick

# Capture per-variant processed outputs, filter specs, metrics, validation reports, and timing
just bench-adeclick-capture

# Profile the production adeclick variant, adeclick_current_t_2_0_w_55_o_50_m_s
JIVETALKING_BENCH_FIXTURE="$(pwd -P)/testdata/fixture-5m.flac" just bench-adeclick-profile

# Profile the without_adeclick reference for comparison
JIVETALKING_ADECLICK_PROFILE_VARIANT=without_adeclick \
  JIVETALKING_BENCH_FIXTURE="$(pwd -P)/testdata/fixture-5m.flac" \
  just bench-adeclick-profile
```

The adeclick benchmark matrix compares the current production `adeclick=t=2.0:w=55:o=50:m=s` clause against `without_adeclick` and three threshold/window/overlap candidates.

---

## Environment Variables

| Variable | Used by | Purpose |
|----------|---------|---------|
| `JIVETALKING_BENCH_FIXTURE` | All `bench-anlmdn-*` and `bench-adeclick-*` recipes | Absolute path to the input audio fixture |
| `JIVETALKING_ANLMDN_PROFILE_VARIANT` | `bench-anlmdn-profile` | Selects which anlmdn variant to profile (default: `anlmdn_sr_32000_best_r`) |
| `JIVETALKING_ADECLICK_PROFILE_VARIANT` | `bench-adeclick-profile` | Selects which adeclick variant to profile (default: `adeclick_current_t_2_0_w_55_o_50_m_s`) |

---

## Output Layout

```
.bench/
├── cpu.out                      # Processor CPU profile (just bench-profile)
├── anlmdn/<variant>/            # Per-variant capture artefacts (just bench-anlmdn-capture)
└── adeclick/<variant>/          # Per-variant capture artefacts (just bench-adeclick-capture)
```

Each capture directory contains processed output audio, the resolved filter spec, post-processing metrics, the validation report, and timing data for the variant.
