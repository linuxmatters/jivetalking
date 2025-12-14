# UREI 1176-Inspired Adaptive Limiter

## Executive Summary

The **UREI 1176 Peak Limiter** (1967) revolutionised dynamics processing as the first fully solid-state peak limiter, introducing microsecond-level attack times that remain unmatched. Its distinctive character comes from:

1. **Ultra-fast FET gain reduction** — 20µs to 800µs attack, catching peaks that tube limiters miss
2. **Program-dependent release** — fast recovery after transients, slower during sustained compression
3. **Soft-knee compression** — natural, musical response even at high ratios
4. **Fixed threshold architecture** — compression controlled via input gain, not threshold adjustment
5. **Harmonic enhancement** — Class A output stage and transformers add "bright, forward" character

The 1176 complements the LA-2A perfectly in a vocal chain: where the LA-2A provides gentle optical levelling, the 1176 catches the peaks that slip through with surgical precision. For podcast limiting, we implement the 1176's philosophy as a final safety limiter.

---

## The 1176's Place in the Processing Chain

The classic professional vocal chain is: **1176 → LA-2A → Pultec**

For Jivetalking's podcast processing:
```
DS201Gate → LA2ACompressor → [EQ/DeEss] → 1176Limiter
     ↓              ↓                            ↓
  Gate noise    Level control              Catch peaks
```

The 1176 as limiter serves a different purpose than the LA-2A as compressor:

| Processor | Role | Character | Target |
|-----------|------|-----------|--------|
| LA-2A | Levelling | Gentle, warming, program-dependent | Overall dynamics |
| 1176 | Peak limiting | Fast, punchy, transient control | Peak protection |

---

## Hardware 1176 Characteristics

### Attack: Lightning-Fast FET Response

The 1176's FET (Field Effect Transistor) gain reduction is the key to its character. Unlike optical (LA-2A) or VCA compressors, the FET responds in microseconds.

| Setting | Attack Time | Character |
|---------|-------------|-----------|
| 1 (slowest) | 800µs (0.8ms) | Allows transient through, "punch" |
| 4 (medium) | ~200µs | Balanced |
| 7 (fastest) | 20µs (0.02ms) | Catches everything, can sound "flat" |

**Note:** The 1176's attack/release knobs work **backwards** — higher numbers = faster times.

**For vocals:** Engineers typically use slower attack (setting 1-3) to preserve consonant transients and maintain intelligibility. Greg Wells famously uses "slowest attack, fastest release" for vocal presence.

### Release: Program-Dependent Two-Stage

The 1176 pioneered program-dependent release, similar to but faster than the LA-2A's optical behaviour:

> "After a transient, it is desirable to have a fast release to avoid prolonged dropouts. However, while in a continued state of heavy compression, it is better to have a longer release time to reduce the pumping and harmonic distortion caused by repetitive attack-release cycles."
> — Universal Audio 1176 Manual

| Setting | Release Time | Use Case |
|---------|--------------|----------|
| 1 (slowest) | 1100ms (1.1s) | Smooth, sustained signals |
| 4 (medium) | ~300ms | General purpose |
| 7 (fastest) | 50ms | Aggressive, rhythmic content |

The program-dependent mechanism has three components:
- **Fast release time** — effective after transients
- **Slow release time** — engaged during sustained compression
- **Transition time** — how long signal must be compressed before slow release engages

### Ratio: Soft-Knee with Threshold Shift

The 1176's ratio buttons don't simply change ratio — they also shift threshold and knee characteristics:

| Ratio | Threshold Behaviour | Knee | Use Case |
|-------|---------------------|------|----------|
| 4:1 | Lowest threshold, engages early | Softest | Gentle compression, vocals |
| 8:1 | Higher threshold | Medium | General limiting |
| 12:1 | Higher still | Firmer | Aggressive limiting |
| 20:1 | Highest threshold | Firmest | Peak limiting, safety |
| All-In | ~12:1-20:1, altered response | Plateau | Distortion/effect (not for podcast) |

**Critical insight:** At higher ratios, the threshold rises, meaning **the 4:1 ratio may actually compress more of the signal** than 20:1 at the same input level because it engages earlier.

### Input-Controlled Threshold

The 1176 has no threshold knob — threshold is fixed relative to the circuit's operating level. The **Input** control simultaneously:
1. Sets input gain (how hot the signal enters)
2. Controls compression amount (hotter signal = more peaks above threshold)

This design means: **more input = more compression**, not just more volume.

---

## FFmpeg alimiter Characteristics

The `alimiter` filter provides lookahead limiting with these parameters:

| Parameter | Default | Range | Notes |
|-----------|---------|-------|-------|
| `level_in` | 1.0 | — | Input gain multiplier |
| `level_out` | 1.0 | — | Output gain multiplier |
| `limit` | 1.0 | 0.0-1.0 | Ceiling (1.0 = 0dBFS) |
| `attack` | 5ms | — | Lookahead/attack time |
| `release` | 50ms | — | Release time |
| `asc` | 0 | 0/1 | Auto Soft Clipping mode |
| `asc_level` | 0.5 | 0.0-1.0 | ASC release influence |
| `level` | 1 | 0/1 | Auto-level output |
| `latency` | 0 | 0/1 | Compensate lookahead delay |

### Mapping 1176 to alimiter

| 1176 Concept | alimiter Equivalent | Notes |
|--------------|---------------------|-------|
| Attack 20µs-800µs | attack 0.1-5ms | FFmpeg's minimum is slower |
| Release 50-1100ms | release 50-1100ms | Direct mapping possible |
| Soft knee | asc mode | ASC provides gentler limiting |
| Program-dependent | asc_level | Influences release behaviour |
| Ratio | N/A | alimiter is ∞:1 (true limiter) |
| Input gain | level_in | Pre-limiter gain |

**Key limitation:** FFmpeg's alimiter is a true limiter (∞:1 ratio), not a variable-ratio compressor. We embrace this for its role as a final safety net.

---

## Available Audio Measurements for Tuning

| Measurement | Relevance to Limiting |
|-------------|----------------------|
| **Peak Level** | Primary reference — how much limiting is needed |
| **True Peak** | Inter-sample peaks — safety margin |
| **Integrated LUFS** | Overall loudness — makeup gain calculation |
| **Dynamic Range** | Peak-to-floor — limiting intensity |
| **Max Difference** | Transient sharpness — attack time tuning |
| **Spectral Flux** | Frame-to-frame change — release tuning |
| **Spectral Crest** | Peak-to-RMS in spectrum — transient density |
| **Loudness Range (LRA)** | Macro-dynamics — overall limiting strategy |
| **Noise Floor** | Background level — prevent noise boosting |

---

## 1176-Inspired Limiter Design

### 1. Ceiling (Limit): Conservative True Peak Safety

The 1176 hardware outputs line level with headroom. For podcast delivery, we target broadcast-safe levels.

**Proposal:** Set ceiling based on target format:
```
limit = -1.0 dBTP (default podcast safety)
      = -2.0 dBTP (streaming platforms like Spotify)
      = -0.5 dBTP (when subsequent processing follows)
```

This is configurable rather than measurement-driven, as it's a delivery standard.

### 2. Attack: Transient-Aware Fast Response

The 1176's magic is catching peaks while preserving attack character. We adapt attack based on transient measurements.

**Proposal:** Use **MaxDifference** and **SpectralCrest** to tune attack:

| Condition | Attack Time | Rationale |
|-----------|-------------|-----------|
| MaxDiff > 25% OR Crest > 50dB | 0.1ms | Extreme transients (plosives) — 1176-fast |
| MaxDiff > 15% OR Crest > 35dB | 0.5ms | Sharp consonants — balanced |
| MaxDiff > 8% | 0.8ms | Normal speech — preserve attack |
| Soft delivery | 1.0ms | Gentle content — minimal limiting |

**Note:** Even our "fast" 0.1ms is slower than the 1176's 20µs, but FFmpeg's lookahead compensates — it "sees" the peak coming.

### 3. Release: Program-Dependent Approximation

The 1176's two-stage release prevents pumping on sustained signals. We approximate this using ASC (Auto Soft Clipping) mode.

**Proposal:** Base release on **SpectralFlux** and **LRA**, use ASC for adaptation:

| Condition | Release Time | ASC Level | Rationale |
|-----------|--------------|-----------|-----------|
| High Flux (>0.03) + Wide LRA (>15 LU) | 200ms | 0.7 | Expressive — fast recovery, ASC smooths |
| Moderate Flux + Moderate LRA | 150ms | 0.5 | Standard podcast delivery |
| Low Flux (<0.01) + Narrow LRA (<10 LU) | 100ms | 0.3 | Controlled — quick response |
| Very wide DR (>35dB) | +50ms | +0.2 | Heavy peaks need longer recovery |

**ASC Mode:** When enabled, `asc=1`, the limiter releases to an average reduction level rather than zero, mimicking the 1176's behaviour of "staying in compression" during loud passages.

### 4. Input Level: Gain Staging from Measurements

Unlike the 1176's input-as-threshold design, we use input level to optimise signal entering the limiter.

**Proposal:** Calculate input gain from **Peak Level** and **Integrated LUFS**:

```
headroom_needed = limit_ceiling - true_peak
if headroom_needed < 0:
    level_in = 10^(headroom_needed / 20)  // Reduce to prevent clipping
else:
    level_in = 1.0  // No input gain needed
```

For significantly quiet content (Integrated LUFS < -30):
```
boost = min(6.0, -24 - integrated_lufs)  // Gentle boost, max 6dB
level_in = 10^(boost / 20)
```

### 5. Output Level: Makeup for Target Loudness

**Proposal:** Calculate output level to approach target loudness:

```
target_lufs = -16.0  // Podcast standard, configurable
current_lufs = integrated_lufs
makeup_db = min(target_lufs - current_lufs, 12.0)  // Cap at 12dB

if makeup_db > 0:
    level_out = 10^(makeup_db / 20)
else:
    level_out = 1.0  // Don't reduce if already loud enough
```

**Note:** This is gentle makeup; the final loudness normalisation pass handles precise LUFS targeting.

### 6. ASC (Auto Soft Clipping): 1176 Program-Dependency

The ASC feature is FFmpeg's closest equivalent to the 1176's program-dependent release. When enabled, instead of releasing to unity gain, the limiter releases to an average attenuation level.

**Proposal:** Enable ASC based on **Dynamic Range** and **Spectral Crest**:

| Condition | ASC | ASC Level | Rationale |
|-----------|-----|-----------|-----------|
| DR > 30dB OR Crest > 40dB | Enabled | 0.6-0.8 | Dynamic content benefits from smooth recovery |
| DR 20-30dB | Enabled | 0.4-0.6 | Moderate smoothing |
| DR < 20dB | Disabled | — | Already compressed, direct limiting fine |
| Noise Floor > -50dB | Enabled | +0.2 | ASC helps mask pumping artefacts |

---

## Implementation Plan

### New Constants

```go
const (
    // 1176-inspired attack times (FFmpeg minimum ~0.1ms vs 1176's 0.02ms)
    u1176AttackExtremeTrans = 0.1   // ms - plosives, extreme peaks
    u1176AttackSharpTrans   = 0.5   // ms - sharp consonants
    u1176AttackNormal       = 0.8   // ms - standard speech
    u1176AttackGentle       = 1.0   // ms - soft delivery
    u1176MaxDiffExtreme     = 0.25  // 25% - extreme transients
    u1176MaxDiffSharp       = 0.15  // 15% - sharp transients
    u1176MaxDiffNormal      = 0.08  // 8% - normal threshold
    u1176CrestExtreme       = 50.0  // dB - extremely peaked
    u1176CrestSharp         = 35.0  // dB - notably peaked

    // 1176-inspired release times (matching hardware range)
    u1176ReleaseExpressive = 200  // ms - wide dynamics
    u1176ReleaseStandard   = 150  // ms - typical podcast
    u1176ReleaseControlled = 100  // ms - narrow dynamics
    u1176ReleaseHeavyBoost = 50   // ms - added for heavy DR
    u1176FluxDynamic       = 0.03 // Above: dynamic/expressive
    u1176FluxStatic        = 0.01 // Below: controlled/monotone

    // ASC (Auto Soft Clipping) - approximates program-dependent release
    u1176ASCLevelDynamic    = 0.7  // For dynamic content
    u1176ASCLevelModerate   = 0.5  // For standard content
    u1176ASCLevelControlled = 0.3  // For controlled content
    u1176ASCNoisyBoost      = 0.2  // Additional for noisy recordings
    u1176DynamicRangeWide   = 30.0 // dB - above: enable ASC
    u1176DynamicRangeMod    = 20.0 // dB - above: moderate ASC

    // Ceiling options (dBTP)
    u1176CeilingPodcast   = -1.0  // Standard podcast safety
    u1176CeilingStreaming = -2.0  // Spotify/Apple Music
    u1176CeilingChain     = -0.5  // When more processing follows

    // Gain limits
    u1176InputBoostMax  = 6.0   // dB maximum input boost
    u1176MakeupMax      = 12.0  // dB maximum makeup gain
    u1176TargetLUFS     = -16.0 // Target loudness (configurable)
    u1176QuietThreshold = -30.0 // LUFS below: apply input boost
)
```

### New Tuning Functions

```go
// tune1176Limiter adapts limiting to emulate 1176 peak limiting character.
// Uses spectral measurements to inform transient-aware, program-dependent behaviour.
func tune1176Limiter(config *FilterChainConfig, measurements *AudioMeasurements) {
    tune1176Ceiling(config, measurements)
    tune1176Attack(config, measurements)
    tune1176Release(config, measurements)
    tune1176ASC(config, measurements)
    tune1176InputLevel(config, measurements)
    tune1176OutputLevel(config, measurements)
}

// tune1176Attack sets attack time based on transient characteristics.
// Emulates the 1176's ability to catch peaks while preserving attack character.
func tune1176Attack(config *FilterChainConfig, m *AudioMeasurements) {
    // Check for extreme transients
    if m.MaxDifference > u1176MaxDiffExtreme || m.SpectralCrest > u1176CrestExtreme {
        config.LimiterAttack = u1176AttackExtremeTrans
        return
    }

    // Sharp transients
    if m.MaxDifference > u1176MaxDiffSharp || m.SpectralCrest > u1176CrestSharp {
        config.LimiterAttack = u1176AttackSharpTrans
        return
    }

    // Normal transients
    if m.MaxDifference > u1176MaxDiffNormal {
        config.LimiterAttack = u1176AttackNormal
        return
    }

    // Soft delivery
    config.LimiterAttack = u1176AttackGentle
}

// tune1176Release sets release time with ASC for program-dependent behaviour.
// Approximates the 1176's two-stage release mechanism.
func tune1176Release(config *FilterChainConfig, m *AudioMeasurements) {
    var baseRelease float64

    // Base release on flux and LRA
    if m.SpectralFlux > u1176FluxDynamic && m.LoudnessRange > 15.0 {
        baseRelease = u1176ReleaseExpressive
    } else if m.SpectralFlux < u1176FluxStatic && m.LoudnessRange < 10.0 {
        baseRelease = u1176ReleaseControlled
    } else {
        baseRelease = u1176ReleaseStandard
    }

    // Add recovery time for very wide dynamic range
    if m.DynamicRange > 35.0 {
        baseRelease += u1176ReleaseHeavyBoost
    }

    config.LimiterRelease = baseRelease
}

// tune1176ASC enables Auto Soft Clipping to approximate program-dependent release.
// When enabled, limiter releases to average attenuation rather than unity.
func tune1176ASC(config *FilterChainConfig, m *AudioMeasurements) {
    // Enable ASC for dynamic content
    if m.DynamicRange > u1176DynamicRangeWide || m.SpectralCrest > 40.0 {
        config.LimiterASC = true
        config.LimiterASCLevel = u1176ASCLevelDynamic
    } else if m.DynamicRange > u1176DynamicRangeMod {
        config.LimiterASC = true
        config.LimiterASCLevel = u1176ASCLevelModerate
    } else {
        config.LimiterASC = false
        config.LimiterASCLevel = 0
        return
    }

    // Boost ASC for noisy recordings (helps mask pumping)
    if m.NoiseFloor > -50.0 {
        config.LimiterASCLevel = min(1.0, config.LimiterASCLevel + u1176ASCNoisyBoost)
    }
}
```

### Filter String Generation

```go
// build1176LimiterFilter generates the FFmpeg alimiter filter string.
func build1176LimiterFilter(config *FilterChainConfig) string {
    ceiling := math.Pow(10, config.LimiterCeiling/20.0) // dBTP to linear

    params := []string{
        fmt.Sprintf("limit=%.6f", ceiling),
        fmt.Sprintf("attack=%.1f", config.LimiterAttack),
        fmt.Sprintf("release=%.1f", config.LimiterRelease),
        fmt.Sprintf("level_in=%.4f", config.LimiterInputLevel),
        fmt.Sprintf("level_out=%.4f", config.LimiterOutputLevel),
        "level=0", // Disable auto-level, we control output
        "latency=1", // Compensate lookahead delay
    }

    if config.LimiterASC {
        params = append(params,
            "asc=1",
            fmt.Sprintf("asc_level=%.2f", config.LimiterASCLevel),
        )
    } else {
        params = append(params, "asc=0")
    }

    return "alimiter=" + strings.Join(params, ":")
}
```

---

## Integration with Processing Chain

### Filter Order

```
DS201HighPass → DS201LowPass → [denoise] → DS201Gate →
LA2ACompressor → [EQ] → [DeEss] → LoudnessNorm → 1176Limiter
```

The 1176 limiter sits last in the chain. This mirrors the professional chain where the 1176 catches any peaks that slip through earlier processing.

### Interaction with LA-2A Compressor

The LA-2A-inspired compressor handles levelling; the 1176-inspired limiter handles peaks:

| LA-2A Compressor | 1176 Limiter |
|------------------|--------------|
| Soft ratio (2.5-3.5:1) | Hard limit (∞:1) |
| Slow attack (8-12ms) | Fast attack (0.1-3ms) |
| Program-dependent release | ASC-assisted release |
| Gentle levelling | Peak protection |

**Gain staging:** The LA-2A's makeup gain should leave headroom for the limiter. Cap LA-2A makeup at 6dB and let the limiter's output stage handle final level.

---

## Expected Outcomes

| Scenario | Current Behaviour | Proposed 1176 Behaviour |
|----------|-------------------|------------------------|
| Sharp plosives (MaxDiff > 25%) | Generic limiting | 0.1ms attack catches peaks cleanly |
| Expressive delivery (high Flux, wide LRA) | Fixed release | 200ms release + ASC 0.7 preserves dynamics |
| Monotone delivery (low Flux, narrow LRA) | Same processing | 100ms release, ASC off, quick response |
| Very wide DR (>35dB) | May pump | Extended release (+50ms) smooths recovery |
| Noisy recording | Same limiting | ASC boost (+0.2) masks pumping artefacts |
| Quiet input (LUFS < -30) | Under-limited | Input boost (up to 6dB) before limiting |

---

## The 1176's Sonic Character (What We Can't Fully Replicate)

For transparency, here's what the hardware 1176 provides that FFmpeg's alimiter cannot:

1. **Harmonic enhancement** — The Class A output stage and transformers add pleasant harmonic distortion, described as "bright and forward." FFmpeg limiting is transparent.

2. **True microsecond attack** — 20µs vs our 100µs minimum. The 1176 catches inter-sample peaks that digital limiters with lookahead handle differently.

3. **FET gain reduction character** — The non-linear FET response creates subtle wave-shaping. FFmpeg's limiting is mathematically precise.

4. **Variable ratio** — The 1176's ratios (4:1 to 20:1) provide graduated control; alimiter is ∞:1 only.

**Our philosophy:** We embrace alimiter's precision and lookahead for what it does well — clean, transparent peak protection — while using the 1176's attack/release timing and ASC to approximate its program-dependent musicality.

---

## References

- [Universal Audio 1176LN Manual](https://media.uaudio.com/assetlibrary/1/1/1176ln_manual.pdf)
- [UA 1176 Classic Limiter Collection Tips](https://www.uaudio.com/blogs/ua/1176-collection-tips)
- [All Buttons In: Investigation into 1176 usage](https://www.arpjournal.com/asarpwp/all-buttons-in-an-investigation-into-the-use-of-the-1176-fet-compressor-in-popular-music-production/)
- [FFmpeg alimiter documentation](https://ffmpeg.org/ffmpeg-filters.html#alimiter)
- [1176 Wikipedia](https://en.wikipedia.org/wiki/1176_Peak_Limiter)
