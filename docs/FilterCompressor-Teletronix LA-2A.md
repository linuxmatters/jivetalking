# Teletronix LA-2A Leveling Amplifier

*The quality target: gentle, programme-aware levelling. The implementation: FFmpeg's `acompressor` with speech-RMS-relative threshold.*

---

## The Legend of the LA-2A

In 1965, Jim Lawrence founded Teletronix Engineering and introduced a compressor that would become synonymous with vocal recording: the LA-2A Leveling Amplifier. Decades later, engineers still reach for LA-2As—original units now command five-figure prices—whenever a voice needs warmth without aggression, control without constraint.

The LA-2A's character comes from its **T4 electro-optical attenuator**: an electroluminescent panel paired with a cadmium sulfide photoresistor. Light output changes with the input signal; photoresistor conductance follows the light. This indirect coupling produces inherently smooth gain changes with two-stage programme-dependent release behaviour.

| Characteristic | T4 Behaviour | Sonic Result |
|---------------|--------------|--------------|
| Attack | ~10 ms fixed | Transients pass through naturally |
| Release (initial) | 60 ms to 50% | Quick recovery from peaks |
| Release (full) | 1–15 seconds | Graceful return, no pumping |
| Ratio | Programme-dependent | Harder compression on louder signals |
| Knee | Inherently soft | Gradual onset, musical transition |

---

## Jivetalking's Implementation

Jivetalking uses FFmpeg's `acompressor` filter (`af_sidechaincompress.c`) to apply gentle, consistent levelling in the spirit of the LA-2A. **This is not a faithful emulation.** `acompressor` is a single-pole-release, fixed-ratio RMS compressor. It structurally cannot reproduce the T4 cell's two-stage programme-dependent release or level-dependent ratio. The LA-2A is the quality target — gentle levelling, least treatment, preserve natural speech character — not the DSP model.

### Design Philosophy

The filter serves one purpose: even out the upper-half loudness variation in speech without audible compression artefacts. A render sweep across the real corpus showed that ratio, attack, release, knee, and mix all collapsed to a single value regardless of source character. Adaptive tuning of those parameters was theatre. The one parameter that genuinely varies across presenters and recording setups is the threshold: it must track the actual speech level to engage consistently.

### Parameters

All parameters are fixed except the threshold.

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Ratio | 3.0 | Gentle levelling; sweep showed no benefit from adaptive range |
| Attack | 10 ms | Matches the T4's fixed attack; lets word onsets through |
| Release | 200 ms | Clean recovery without pumping for typical speech patterns |
| Knee | 4.0 | Soft onset; matches the T4's inherently gradual compression |
| Mix | 1.0 | Full wet; consistent with the LA-2A's no-parallel-path design |
| Makeup | 0 dB | Unity; loudnorm handles final level in Pass 3/4 |
| Detection | RMS | Matches how the photoresistor responds to sustained level |

### Threshold Adaptation

Threshold is the only adaptive parameter:

```
threshold = SpeechProfile.RMSLevel + 9 dB   (when SpeechProfile elected)
threshold = PeakLevel − 20 dB               (fallback, no SpeechProfile)
```

Speech-RMS + 9 dB places the threshold just above the bulk of speech energy, engaging compression on the upper half of the dynamic range. Across the real corpus, this delivers ~2.5-4.4 dB compression depth and an output crest factor in the 8-12 dB sweet spot — consistent across the wide input-level spread that made peak-relative thresholding unreliable (depth swung 6.6-24 dB under the old `peak − 20` primary).

The `peak − 20` path is retained as a fallback for files where speech election fails, not as a standard path.

**Removed adaptations:** kurtosis/flux-based ratio and release, centroid-based knee, mix-by-noise-floor, and the high-crest override. All were confirmed theatre on the corpus: the high-crest override (which pushed ratio toward 5.0 for predicted limiter ceiling deficits) was beaten on LRA and true-peak by the plain fixed-3.0 / speechRMS+9 combination, and the downstream `alimiter` owns peak control regardless (output -1.1 to -2.9 dBTP).

---

## Configuration

`BaseFilterConfig` stores caller-owned LA-2A defaults. `AdaptConfig()` copies those defaults into an `EffectiveFilterConfig`, sets the speech-RMS-relative threshold from Pass 1 measurements, and returns `AdaptiveDiagnostics`.

| Field | Type | Default | Purpose |
|-------|------|---------|---------|
| `LA2AEnabled` | bool | true | Enable/disable filter |
| `LA2AThreshold` | float64 | −18 dB | Compression threshold (overwritten by `AdaptConfig`) |
| `LA2ARatio` | float64 | 3.0 | Fixed compression ratio |
| `LA2AAttack` | float64 | 10 ms | Fixed attack time |
| `LA2ARelease` | float64 | 200 ms | Fixed release time |
| `LA2AMakeup` | float64 | 0 dB | Unity gain |
| `LA2AKnee` | float64 | 4.0 | Fixed knee softness |
| `LA2AMix` | float64 | 1.0 | Fixed wet/dry mix |

### FFmpeg Filter Specification

`LA2AThreshold` is stored in dB and converted to FFmpeg's linear scale before building the filter string. Example at threshold −18 dB:

```
acompressor=threshold=0.125893:ratio=3.0:attack=10:release=200:makeup=1.00:knee=4.0:detection=rms:mix=1.00
```

---

## Pipeline Integration

```
DS201 Gate → NoiseRemove → LA-2A (levelling) → De-esser → Loudnorm (Pass 3/4)
```

The LA-2A operates on already-gated, already-denoised audio. Its job is narrow: reduce the loudness variation between quiet and loud speech passages before the normalisation passes lock in the final level. Peak control belongs to the downstream `alimiter` (Volumax) in Pass 4.

---

## References

- [Teletronix Engineering, LA-2A Leveling Amplifier Manual (1965)](https://tile.loc.gov/storage-services/master/mbrs/recording_preservation/manuals/Teletronix%20Model%20LA-2A%20Leveling%20Amplifier.pdf)
- [Universal Audio, "LA-2A Leveling Amplifier" reissue documentation](https://media.uaudio.com/assetlibrary/l/a/la-2a_manual.pdf)
- Dennis Fink, "The History of the LA-2A" (Mix Magazine, 2003)
- FFmpeg source: [`libavfilter/af_sidechaincompress.c`](https://github.com/FFmpeg/FFmpeg/blob/master/libavfilter/af_sidechaincompress.c) — single-pole-release RMS compressor implementation
- FFmpeg Documentation: [`acompressor` filter](https://ffmpeg.org/ffmpeg-filters.html#acompressor)
- https://github.com/aim-qmul/4a2a
