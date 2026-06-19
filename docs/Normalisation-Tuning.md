# Normalisation Tuning

Why the loudnorm and limiter constants in `internal/processor/normalise.go` hold
the values they do. Each value is corpus-derived. The maths is correct; this doc
keeps the long rationale out of the code, where it would rot.

The corpus references name validation sweeps run by hand against gitignored audio
that does not ship in the repo. They are evidence summaries, not files you can open.

## brickwallTruePeakHeadroomDB

- **Value:** 0.9 dB.
- **What it does:** The inter-sample allowance subtracted from loudnorm's TargetTP
  to set the brickwall limiter's sample-peak ceiling.
- **Why this value:** The brickwall alimiter limits SAMPLE peak, but the spec
  targets oversampled TRUE peak. The gap between them, the inter-sample excess,
  pushes realised true peak above the sample ceiling. The headroom closes that gap.
- **Corpus evidence:** Validated against the 0.5.x-vs-0.6.x corpus sweep
  (combined post-Phase-2 run). The inter-sample excess (final true_peak dBTP minus
  sample_peak dBFS) peaked at 0.817 dB on LMP-76 popey, driven up from the 0.60 dB
  the earlier 0.7 value was sized for by the Phase-2 loudness-cap relaxation (the
  former static loudnorm TP relax). The excess is positive on every file, so
  realised true peak sits above the sample ceiling on all of them. 0.9 dB covers
  the p100 of 0.817 with a ~0.08 dB safety allowance, so the sample-peak brickwall
  keeps oversampled true peak at or below the loudnorm TargetTP on the whole corpus.

## measurementCushionDB

- **Value:** 0.2 dB.
- **What it does:** The fixed measurement-disagreement cushion added to loudnorm's
  INTERNAL true-peak target (`loudnormInternalTargetTP`).
- **Why this value:** It guards the per-file projected post-gain peak against
  loudnorm's OWN internal oversampled true-peak estimate disagreeing with the
  Pass-3 measured TP. Go computes `projectedPeak` from measured_TP and measured_I.
  FFmpeg's loudnorm computes its own output-peak estimate at a different point.
  If loudnorm's estimate exceeds the projection by more than the cushion, loudnorm
  can trip to DYNAMIC mode, the failure the Pass-4 levelling limiter exists to prevent.
- **Corpus evidence:** Validated against the 0.5.x-vs-0.6.x corpus sweep
  (out-final JSON). The delta `loudnorm_output_tp minus projectedPeak` is at or
  below 0.05 dB across all 48 files, one-sided positive (loudnorm estimates its
  peak about 0.04 dB hotter on every file). 0.2 clears the measured 0.05 bias about
  4 times, leaving headroom for off-corpus material (other producers' audio) where
  the Go-vs-FFmpeg estimator gap could be larger.
- **Invariant:** This is the only static margin left in loudnorm's internal
  targeting. The variable, corpus-dependent loudness shortfall the former 0.5 relax
  constant covered is now derived per file from each file's Pass-3 measured_I and
  measured_TP (see `loudnormInternalTargetTP`), so no corpus-tuned number remains.
  It applies only to loudnorm's internal targeting; the brickwall ceiling stays at
  `TargetTP - brickwallTruePeakHeadroomDB`, unchanged.

## linearSafetyMargin

- **Value:** 0.1 dB.
- **What it does:** Keeps loudnorm safely inside linear-mode bounds in the
  `calculateLinearModeTarget` guard.
- **Why this value:** It accounts for floating-point precision differences between
  Go and FFmpeg, rounding in filter parameter passing, and measurement variance.
  `loudnormInternalTargetTP` folds the same value into the per-file internal TP, so
  the linear-mode cap is inert by construction.

## loudnormTPMaxDB / loudnormTPMinDB

- **Values:** 0.0 dBTP and -9.0 dBTP.
- **What they do:** Bound the value emitted into loudnorm's `TP=` option.
- **Why these values:** This is an engine constraint, not a tuning choice.
  FFmpeg's `af_loudnorm` rejects a TP outside [-9.0, 0.0] dBTP (`AVERROR(ERANGE)`
  at graph build, see `set_options` in `af_loudnorm.c`). The per-file
  `loudnormInternalTargetTP` is unbounded by construction, so the emitted `TP=` is
  clamped to this range. The linear-mode guard keeps the unclamped value.

## minLimiterCeilingDB

- **Value:** -24.0 dBTP.
- **What it does:** The practical minimum for FFmpeg's alimiter ceiling.
- **Why this value:** This is the alimiter engine floor, not a tuning constant.
  `limit=0.0625` equals `20*log10(0.0625)` which is about -24.08 dBTP; -24.0 sits
  just inside it with a small buffer.
