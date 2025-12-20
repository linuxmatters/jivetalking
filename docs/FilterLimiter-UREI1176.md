# UREI 1176-Inspired Peak Limiter for Loudnorm Headroom

## Executive Summary

The **UREI 1176 Peak Limiter** (1967) revolutionised dynamics processing as the first fully solid-state peak limiter, introducing microsecond-level attack times that remain unmatched. Its distinctive character comes from:

1. **Ultra-fast FET gain reduction** — 20µs to 800µs attack, catching peaks that tube limiters miss
2. **Program-dependent release** — fast recovery after transients, slower during sustained compression
3. **Soft-knee compression** — natural, musical response even at high ratios
4. **Fixed threshold architecture** — compression controlled via input gain, not threshold adjustment

**In Jivetalking**, the 1176-inspired limiter serves a specific purpose: **creating headroom for loudnorm's linear mode** by reducing peaks before loudnorm applies gain. This prevents loudnorm from falling back to dynamic mode (which causes audible pumping and noise floor elevation).

---

## The 1176 at 20:1: True Peak Limiting

The 1176's 20:1 ratio mode was designed specifically for peak limiting:

> "The 20:1 ratio is typically used when peak-limiting is desired"
> — UA 1176 Classic Limiter Collection Manual

> "With 20:1 ratio and fast attack and release settings, the 2-1176 was an outstanding limiter for male vocals, providing a really firm lid without squashing the sound."
> — UA 2-1176 Dual Manual

**Key characteristics at 20:1:**
- **Highest threshold** — only catches the loudest peaks
- **Firmest knee** — decisive peak control
- **Minimal coloration** — affects <5% of audio (peak transients only)

This is exactly what we need: transparent peak reduction to create headroom, not dynamic shaping.

---

## Role in Jivetalking's Processing Chain

### Position: Pass 4, Before Loudnorm

```
Pass 1: Analysis → Pass 2: Processing → Pass 3: Loudnorm Measurement → Pass 4: [Limiter] → Loudnorm
```

The limiter operates **after** Pass 3 measurement but **before** loudnorm application. This allows us to calculate the exact ceiling needed based on Pass 3's measurements.

### Purpose: Enable Loudnorm Linear Mode

Loudnorm in linear mode applies consistent gain without adaptive EQ. For linear mode to work:

```
measured_TP + gain_required ≤ target_TP
```

When this condition fails, loudnorm falls back to dynamic mode. The limiter ensures the condition is met by reducing `measured_TP` to an appropriate ceiling.

**Example:**
- Pass 3 measures: LUFS = -24.9, TP = -5.0 dBTP
- Target: LUFS = -16.0, TP = -2.0 dBTP
- Gain required: 8.9 dB
- Projected TP after gain: -5.0 + 8.9 = **+3.9 dBTP** (exceeds target!)
- Required ceiling: -2.0 - 8.9 - 0.5 (margin) = **-11.4 dBTP**

The limiter reduces peaks from -5.0 to -11.4 dBTP, allowing loudnorm to apply 8.9 dB linear gain.

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

## 1176-Inspired Peak Limiter Design (Pass 4)

The limiter's role is simple and focused: **reduce peaks to a calculated ceiling** so loudnorm can apply linear gain. This is the 1176 at 20:1 — transparent peak limiting, not dynamic shaping.

### 1. Ceiling: Adaptive from Pass 3 Measurements

Unlike the original design doc's fixed ceiling options, we calculate the **exact ceiling needed** for loudnorm linear mode:

```go
func calculateLimiterCeiling(measured_I, measured_TP, target_I, target_TP float64) (ceiling float64, needed bool) {
    gainRequired := target_I - measured_I
    projectedTP := measured_TP + gainRequired

    // No limiting needed if linear mode already possible
    if projectedTP <= target_TP {
        return 0, false
    }

    // Calculate ceiling: target_TP - gainRequired - safetyMargin
    // Safety margin (0.5 dB) accounts for loudnorm measurement precision
    ceiling = target_TP - gainRequired - 0.5

    return ceiling, true
}
```

### 2. Attack: 1176 Fastest (20:1 Peak Limiting Mode)

For peak limiting, the 1176 was used at its fastest attack to catch transients cleanly:

> "With ultra-fast attack times as low as 20 microseconds, the 1176 can act as a true peak limiter to catch initial transients"
> — Vintage King

**Fixed setting:** `attack = 0.1ms` (FFmpeg's practical minimum with lookahead)

This is **not** the "grit" setting (which requires fast attack AND fast release on sustained signals). For brief peak transients, fast attack is transparent.

### 3. Release: 1176 Fastest (50ms)

The 1176's fastest release (50ms) provides quick recovery after peak limiting:

| Release | 1176 Setting | Character |
|---------|--------------|-----------|
| 50ms | 7 (fastest) | Quick recovery, no pumping on speech |
| 100-200ms | 4-5 (medium) | Would cause audible "release tail" |

**Fixed setting:** `release = 50ms`

For peak limiting (affecting only transients), fast release is ideal. The ASC mode prevents any pumping that might occur.

### 4. ASC: Program-Dependent Release Approximation

ASC (Auto Soft Clipping) is **always enabled** for peak limiting. It prevents the abrupt release that can cause artifacts:

**Fixed settings:** `asc = 1`, `asc_level = 0.5`

### 5. Input/Output Levels: Unity

The limiter's sole job is peak reduction. Loudnorm handles all level adjustment:

**Fixed settings:** `level_in = 1.0`, `level_out = 1.0`

---

## Implementation Plan

### Simplified Constants

```go
const (
    // 1176 at 20:1 peak limiting mode
    limiterAttack   = 0.1   // ms - fastest practical attack
    limiterRelease  = 50.0  // ms - 1176 fastest release
    limiterASCLevel = 0.5   // Moderate program-dependent release

    // Ceiling calculation
    limiterSafetyMargin = 0.5 // dB - accounts for measurement precision
)
```

### Ceiling Calculation Function

```go
// calculateLimiterCeiling determines the ceiling needed for loudnorm linear mode.
// Returns the ceiling in dBTP and whether limiting is needed at all.
//
// Parameters come from Pass 3 loudnorm measurement:
//   - measured_I: Integrated loudness (LUFS)
//   - measured_TP: True peak (dBTP)
//   - target_I: Target loudness (LUFS), typically -16.0
//   - target_TP: Target true peak (dBTP), typically -2.0
func calculateLimiterCeiling(measured_I, measured_TP, target_I, target_TP float64) (ceiling float64, needed bool) {
    // Calculate gain that loudnorm needs to apply
    gainRequired := target_I - measured_I

    // Project where true peak will end up after gain
    projectedTP := measured_TP + gainRequired

    // If projected TP is within target, no limiting needed
    if projectedTP <= target_TP {
        return 0, false
    }

    // Calculate ceiling that allows linear mode:
    // ceiling + gainRequired = target_TP (with safety margin)
    ceiling = target_TP - gainRequired - limiterSafetyMargin

    return ceiling, true
}
```

### Filter String Generation

```go
// buildPass4LimiterFilter generates the alimiter filter for Pass 4.
// Only called when limiting is needed (ceiling calculation returned needed=true).
func buildPass4LimiterFilter(ceiling float64) string {
    // Convert dBTP to linear (0.0-1.0)
    ceilingLinear := math.Pow(10, ceiling/20.0)

    return fmt.Sprintf(
        "alimiter=limit=%.6f:attack=%.1f:release=%.1f:level_in=1:level_out=1:asc=1:asc_level=%.2f:level=0:latency=1",
        ceilingLinear,
        limiterAttack,
        limiterRelease,
        limiterASCLevel,
    )
}
```

### Integration in normalise.go

The limiter is added to the Pass 4 filter chain conditionally:

```go
func buildLoudnormFilterSpec(config *FilterChainConfig, measurement *LoudnormMeasurement) string {
    var filters []string

    // 1. Limiter (if needed for linear mode)
    ceiling, needsLimiting := calculateLimiterCeiling(
        measurement.InputI,
        measurement.InputTP,
        config.LoudnormTargetI,
        config.LoudnormTargetTP,
    )
    if needsLimiting {
        filters = append(filters, buildPass4LimiterFilter(ceiling))
    }

    // 2. Loudnorm (second pass with linear=true)
    // ... existing loudnorm filter spec ...

    // 3. Analysis filters (astats, aspectralstats, ebur128)
    // ... existing analysis filters ...

    return strings.Join(filters, ",")
}
```

---

## Why This Approach Works

| Aspect | Design Decision | Rationale |
|--------|-----------------|-----------|
| **Ceiling** | Adaptive from measurements | Exact headroom needed, not over-limiting |
| **Attack** | Fixed 0.1ms (fastest) | 1176 peak limiting mode; catches all transients |
| **Release** | Fixed 50ms (fastest) | Quick recovery; no audible "release tail" |
| **ASC** | Always enabled | Prevents pumping; 1176 program-dependent character |
| **Levels** | Unity | Loudnorm handles all level adjustment |

---

## Comparison: Peak Limiting vs Original "Safety Net" Design

The original design doc described the 1176 as a **chain-end safety limiter** with adaptive parameters. The new role is simpler:

| Aspect | Original Design | Pass 4 Peak Limiting |
|--------|-----------------|---------------------|
| **Purpose** | Catch peaks after all processing | Create headroom for loudnorm |
| **Position** | End of Pass 2 chain | Before loudnorm in Pass 4 |
| **Ceiling** | Fixed (-1.0 to -2.0 dBTP) | Adaptive (calculated from measurements) |
| **Attack** | Adaptive (0.1-1.0ms) | Fixed 0.1ms (1176 peak limiting mode) |
| **Release** | Adaptive (100-200ms) | Fixed 50ms (1176 fastest) |
| **Input/Output** | Adaptive gain staging | Unity (loudnorm handles levels) |

---

## Edge Cases

| Scenario | Behaviour |
|----------|-----------|
| **Audio already quiet enough** | Limiting skipped; `projectedTP <= target_TP` |
| **Very quiet audio (large gain needed)** | Ceiling may be low (e.g., -20 dBTP); only affects brief transients |
| **Already at target LUFS** | No gain needed; limiting skipped |
| **Hot recording (TP near 0)** | Aggressive limiting; acceptable tradeoff vs dynamic mode pumping |
| **Noisy recording** | ASC handles it gracefully; quick release prevents pumping |

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
