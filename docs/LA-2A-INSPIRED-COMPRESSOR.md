# LA-2A-Inspired Adaptive Compressor Tuning

## Executive Summary

The LA-2A Leveling Amplifier is legendary for its **gentle, program-dependent optical compression** that "treats your signal lovingly" (Bill Putnam Jr.). Its unique character comes from:

1. **Fixed 10ms attack** — slow enough to preserve transients
2. **Two-stage program-dependent release** — 60ms initial (50% release), then 1-15 seconds for full release
3. **Soft, variable compression ratio** — the T4 optical cell naturally varies ratio based on signal strength
4. **Tube warmth** — adds body and presence, "fattens" the low-mids

The current compressor implementation uses simple tiered logic based on dynamic range and LRA. This proposal describes how to leverage the **richer spectral measurements now available** to create LA-2A-inspired adaptive tuning.

---

## Available Audio Measurements

| Measurement | What It Tells Us | Relevance to Compression |
|-------------|------------------|-------------------------|
| **Integrated LUFS** | Overall loudness | How much makeup gain is needed |
| **Loudness Range (LRA)** | Transient dynamics | Attack/release character |
| **Dynamic Range** | Peak-to-floor ratio | How much compression is needed |
| **RMS Level** | Average amplitude | Threshold reference point |
| **Peak Level** | Maximum amplitude | Headroom for makeup |
| **Spectral Centroid** | Brightness | Voice character for knee tuning |
| **Spectral Spread** | Frequency distribution width | Dynamic vs. tonal content |
| **Spectral Skewness** | Bass/treble concentration | Voice warmth (LA-2A loves warm voices) |
| **Spectral Kurtosis** | Peaked vs. flat spectrum | Tonal vs. noise-like |
| **Spectral Flux** | Frame-to-frame change | Speech stability |
| **Spectral Decrease** | Bass concentration strength | Voice body/foundation |
| **Spectral Rolloff** | HF extension | Articulation character |
| **Max Difference** | Transient sharpness | Attack time tuning |
| **Zero Crossings Rate** | Signal complexity | Speech pacing |
| **Noise Floor** | Background noise | Mix (parallel compression) decision |
| **Silence Entropy** | Noise character | Artefact masking potential |

---

## LA-2A Characteristics to Emulate

### 1. Attack Time: Fixed at ~10ms
The LA-2A's 10ms attack allows transients through, preserving the "pluck" of consonants and word onsets. This is critical for podcast intelligibility.

**Proposal:** Use 10-12ms as the baseline, with **MaxDifference** (transient indicator) allowing slight variation:
- **MaxDiff > 25%**: Sharp transients → 8ms (faster to catch peaks without overshoot)
- **MaxDiff 10-25%**: Normal speech → 10ms (LA-2A baseline)
- **MaxDiff < 10%**: Soft delivery → 12ms (even gentler)

### 2. Release Time: Two-Stage Program-Dependent
This is the **soul of the LA-2A**. It releases quickly for short peaks (60ms to 50%), then slowly decays (1-15 seconds) for sustained signals.

FFmpeg's acompressor only has a single release parameter, so we need to approximate this behaviour. The key insight is that the **duration and strength** of compression affects how "held down" the audio feels.

**Proposal:** Use a combination of measurements to set release:

| Signal Characteristic | Release Time | Rationale |
|----------------------|--------------|-----------|
| Wide LRA (>15 LU) + High Flux (>0.03) | 250-300ms | Expressive speech needs room to breathe |
| Moderate LRA (10-15 LU) | 150-200ms | Standard podcast delivery |
| Narrow LRA (<10 LU) + Low Flux (<0.01) | 100-150ms | Compressed/monotone allows faster recovery |
| High Dynamic Range (>30dB) | +50ms | Heavy peaks need longer recovery |

**Additional LA-2A-style behaviour:** When the LUFS gap is large (>15dB gain needed), increase release by 25-50ms to approximate the "slowed release after heavy compression" character.

### 3. Ratio: Soft and Variable (Effectively 3:1-4:1)
The LA-2A's ratio is nominally 3:1 in "Compress" mode, but the T4 cell makes it program-dependent—it compresses harder on louder signals. This creates the "levelling" character rather than hard limiting.

**Proposal:** Base ratio on **Dynamic Range** and **Spectral Kurtosis**:

| Content Type | Ratio | Detection |
|--------------|-------|-----------|
| Highly peaked/tonal (Kurtosis >10) | 2.5:1 | Preserve character |
| Standard speech (Kurtosis 5-10) | 3:1 | LA-2A baseline |
| Flat/noise-like (Kurtosis <5) | 3.5:1 | More consistent levelling |
| Very wide dynamic range (>35dB) | +0.5:1 | Extra control |

### 4. Threshold: Reference to RMS
The LA-2A's "Peak Reduction" knob effectively sets threshold relative to the incoming signal level.

**Proposal:** Set threshold relative to **RMS Level** (not fixed dB):
```
threshold = RMS_level + offset
```

Where offset is determined by desired compression depth:
- Light levelling: offset = -6dB (compress peaks only)
- Standard LA-2A: offset = -10dB (moderate levelling)
- Heavy levelling: offset = -14dB (aggressive control)

Select based on **Dynamic Range**:
- DR > 30dB → heavy levelling (offset -12dB to -14dB)
- DR 20-30dB → standard (offset -8dB to -10dB)
- DR < 20dB → light (offset -4dB to -6dB)

### 5. Knee: Very Soft
The T4 optical cell provides an inherently soft knee. FFmpeg's acompressor supports knee from 1.0-8.0.

**Proposal:** Base knee on **Spectral Centroid** (voice character):
- Dark voices (centroid <4000Hz): knee = 4.0-5.0 (very soft, preserve warmth)
- Normal voices (4000-6000Hz): knee = 3.5-4.0 (LA-2A style)
- Bright voices (>6000Hz): knee = 3.0-3.5 (slightly firmer to control sibilance)

### 6. Mix (Parallel Compression): LA-2A Was 100% Wet
The hardware LA-2A has no mix control—it's 100% wet. However, parallel compression can help with problematic recordings.

**Proposal:** Base mix on **Noise Floor** and **Silence Entropy**:
- Very clean (<-70dB) + low entropy (tonal noise): mix = 1.0 (full wet, LA-2A style)
- Moderate (-50 to -70dB): mix = 0.9-0.95 (slight dry signal masks artefacts)
- Noisy (>-50dB) + high entropy (broadband): mix = 0.8-0.85 (more dry to hide pumping)

### 7. Makeup Gain: Calculated from Expected Reduction
**Proposal:** Calculate from threshold position and ratio:
```
expected_reduction = (peak_level - threshold) * (1 - 1/ratio)
makeup = expected_reduction * 0.7  // Conservative, let limiter handle peaks
```

Cap at 6dB to avoid over-driving downstream filters.

---

## Implementation Plan

### New Constants

```go
const (
    // LA-2A-inspired attack (baseline 10ms, slight variation for transients)
    la2aAttackBase     = 10.0  // ms - LA-2A baseline
    la2aAttackFast     = 8.0   // ms - sharp transients
    la2aAttackSlow     = 12.0  // ms - soft delivery
    la2aMaxDiffSharp   = 0.25  // MaxDifference > 25% = sharp transients
    la2aMaxDiffSoft    = 0.10  // MaxDifference < 10% = soft delivery

    // LA-2A-inspired release (approximating two-stage behaviour)
    la2aReleaseExpressive = 280  // ms - wide LRA + high flux
    la2aReleaseStandard   = 180  // ms - typical podcast
    la2aReleaseCompact    = 120  // ms - narrow LRA + low flux
    la2aReleaseHeavyBoost = 50   // ms - added when heavy compression
    la2aFluxDynamic       = 0.03 // Above: dynamic/expressive
    la2aFluxStatic        = 0.01 // Below: compressed/monotone
    la2aLUFSGapHeavy      = 15.0 // dB - above: add release time

    // LA-2A-inspired ratio (soft 3:1 baseline, program-dependent)
    la2aRatioBase        = 3.0  // LA-2A baseline
    la2aRatioTonal       = 2.5  // For peaked/tonal content
    la2aRatioFlat        = 3.5  // For flat/noise-like content
    la2aKurtosisHighPeak = 10.0 // Above: peaked harmonics
    la2aKurtosisLowPeak  = 5.0  // Below: flat spectrum

    // LA-2A-inspired threshold (relative to RMS)
    la2aThresholdOffsetLight  = -6.0  // dB offset from RMS for light levelling
    la2aThresholdOffsetStd    = -10.0 // dB offset for standard LA-2A
    la2aThresholdOffsetHeavy  = -14.0 // dB offset for heavy levelling
    la2aDynamicRangeWide      = 30.0  // dB - above: heavy threshold
    la2aDynamicRangeMod       = 20.0  // dB - above: standard threshold

    // LA-2A-inspired soft knee
    la2aKneeDark   = 4.5 // For dark voices (preserve warmth)
    la2aKneeNormal = 3.8 // Standard (LA-2A approximation)
    la2aKneeBright = 3.2 // For bright voices
    la2aCentroidBright = 6000.0 // Hz
    la2aCentroidDark   = 4000.0 // Hz

    // Mix (parallel compression for problematic recordings)
    la2aMixClean    = 1.0  // Very clean recordings
    la2aMixModerate = 0.92 // Moderate noise
    la2aMixNoisy    = 0.82 // Noisy recordings
    la2aNoiseFloorClean = -70.0 // dBFS
    la2aNoiseFloorNoisy = -50.0 // dBFS

    // Makeup gain limits
    la2aMakeupMultiplier = 0.7 // Conservative makeup calculation
    la2aMakeupMax        = 6.0 // dB maximum
)
```

### New Tuning Functions

```go
// tuneLA2ACompression adapts compression to emulate LA-2A optical character.
// Uses spectral measurements to inform program-dependent behaviour.
func tuneLA2ACompression(config *FilterChainConfig, measurements *AudioMeasurements) {
    tuneLA2AAttack(config, measurements)
    tuneLA2ARelease(config, measurements)
    tuneLA2ARatio(config, measurements)
    tuneLA2AThreshold(config, measurements)
    tuneLA2AKnee(config, measurements)
    tuneLA2AMix(config, measurements)
    tuneLA2AMakeup(config, measurements)
}
```

---

## Expected Outcomes

| Scenario | Current Behaviour | Proposed LA-2A Behaviour |
|----------|-------------------|--------------------------|
| Warm voice (low centroid, negative skewness) | Generic 2.5-4:1 | Softer ratio (2.5:1), very soft knee (4.5), preserves warmth |
| Expressive delivery (wide LRA, high flux) | 150ms release | 280ms release, preserves dynamics |
| Soft speaker (low MaxDiff) | 15-25ms attack | 12ms attack (even gentler) |
| Noisy recording | Same compression | Reduced mix (0.82) masks pumping |
| High dynamic range (>30dB) | Higher ratio | Deeper threshold + 50ms release boost |
