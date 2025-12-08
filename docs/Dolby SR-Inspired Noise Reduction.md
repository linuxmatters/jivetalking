# Dolby SR-Inspired Adaptive Noise Reduction

This document describes Jivetalking's noise reduction implementation, inspired by the legendary **Dolby SR (Spectral Recording)** system—the professional broadcast and film industry standard from 1986 until the digital transition.

## The Dolby SR System

Dolby SR revolutionised noise reduction by achieving **24dB of noise reduction** that engineers described as "sounding like nothing at all." Its secret wasn't aggressive processing—it was *intelligent restraint*.

### Key Dolby SR Characteristics

| Parameter | SR Specification | Notes |
|-----------|------------------|-------|
| **Noise Reduction** | 24dB (HF), 16dB (LF) | Frequency-dependent depth |
| **Dynamic Range** | 90–95dB | From analog tape |
| **Companders** | 10 simultaneous | 5 fixed-band + 5 sliding-band |
| **Primary Crossover** | 800Hz | With 200Hz–3kHz overlap |
| **Threshold Levels** | -30, -48, -62 dB | Three-stage staggering |
| **Attack (fast)** | 4–8ms | Transient-preserving |
| **Release (smooth)** | 80–300ms | Artifact-preventing |

### Dolby SR Innovations

1. **Least Treatment Principle** — Low-level signals remain fully boosted until a dominant signal appears. SR never processes more than necessary, which is why it sounds transparent.

2. **Action Substitution** — Fixed and sliding band companders operate simultaneously. The system seamlessly substitutes whichever provides better noise reduction at each frequency, eliminating the "mid-band modulation effect" that plagued Dolby A.

3. **Spectral Skewing** — Desensitisation networks at frequency extremes (12kHz LP, 40Hz HP) prevent response errors from causing mistracking.

4. **Anti-Saturation** — HF networks above 4kHz (reaching 10dB at 15kHz) prevent tape saturation from distorting transients.

5. **Modulation Control** — Prevents fixed bands from reacting to out-of-band signals, stopping bass from affecting treble processing.

## Why SR Eliminated Artifacts

Earlier Dolby systems suffered from audible artifacts:

| System | Problem | Cause |
|--------|---------|-------|
| **Dolby A** | Cross-band pumping | Bass drum triggers gain reduction across entire low-pass band |
| **Dolby B** | Noise "breathing" | Single sliding band loses NR below dominant frequency |

SR solved both through dual-mode operation: fixed bands provide a stable "floor" that prevents pumping, while sliding bands dynamically track spectral content without affecting other frequencies. The result: no audible processing artifacts on program material.

## Jivetalking's Dolby SR-Inspired Implementation

We implement SR's philosophy using FFmpeg 8.0's noise reduction filters, with adaptations optimised for podcast speech processing. Since the DS201HighPass and DS201Gate filters handle frequency shaping and inter-phrase cleanup, this filter focuses purely on **spectral noise reduction**.

### Architecture: Multi-Band Spectral Processing

Dolby SR's 10 companders operate across different frequency ranges with band-specific thresholds, ratios, and time constants. We replicate this using FFmpeg's `acrossover` filter to split audio into four bands, apply tailored `afftdn` processing to each, then recombine.

```
[Input from DS201LowPass]
           │
           ▼
    ┌──────────────────────────────────────────────────┐
    │              acrossover                          │
    │         split=200 1500 6000                      │
    │              order=4th                           │
    └──┬──────────┬──────────┬──────────┬─────────────┘
       │          │          │          │
    [LOW]      [MID]      [HIGH]      [AIR]
    <200Hz    200-1500   1500-6000    >6000Hz
       │          │          │          │
       ▼          ▼          ▼          ▼
  ┌─────────┐ ┌─────────┐ ┌─────────┐ ┌─────────┐
  │ afftdn  │ │ afftdn  │ │ afftdn  │ │ afftdn  │
  │ nr=14   │ │ nr=10   │ │ nr=8    │ │ nr=5    │
  │ gs=14   │ │ gs=10   │ │ gs=6    │ │ gs=3    │
  │ rf=-34  │ │ rf=-38  │ │ rf=-40  │ │ rf=-42  │
  │ ad=0.85 │ │ ad=0.70 │ │ ad=0.55 │ │ ad=0.45 │
  └────┬────┘ └────┬────┘ └────┬────┘ └────┬────┘
       │          │          │          │
       └──────────┴──────────┴──────────┘
                       │
                       ▼
                 amix inputs=4
                       │
                       ▼
              [Output to DS201Gate]
```

### Why Four Bands?

| Band | Frequency | Speech Content | Noise Type | NR Strategy |
|------|-----------|----------------|------------|-------------|
| **LOW** | <200Hz | Chest resonance, fundamentals | HVAC rumble, traffic | Aggressive NR, slow adaptation |
| **MID** | 200–1500Hz | Core intelligibility, formants | Broadband hiss | Moderate NR, standard adaptation |
| **HIGH** | 1500–6000Hz | Consonants, sibilance | Tape hiss, preamp | Gentle NR, fast adaptation |
| **AIR** | >6000Hz | Breathiness, presence | HF self-noise | Minimal NR, fastest adaptation |

This mirrors SR's approach: **different frequency bands need different treatment**. Bass content has slower natural dynamics (voice fundamentals sustain), so we use slower adaptation (0.85) and more smoothing (gs=14). High frequencies contain fast transients (consonants, sibilance), so we use faster adaptation (0.45) and less smoothing (gs=3) to preserve detail.

### Band-Specific Parameter Rationale

**LOW Band (<200Hz)**
- **Aggressive NR (14dB)**: Voice has strong fundamental energy here; noise is typically HVAC/traffic rumble with less perceptual importance
- **High smoothing (gs=14)**: Slow dynamics mean we can smooth heavily without losing detail
- **Higher residual floor (-34dB)**: Some LF energy preserved to avoid "thin" sound
- **Slow adaptivity (0.85)**: LF noise is typically stationary; fast adaptation causes pumping

**MID Band (200–1500Hz)**
- **Moderate NR (10dB)**: Core voice intelligibility lives here—must balance cleanup against damage
- **Standard smoothing (gs=10)**: SR-style moderate smoothing
- **Standard residual floor (-38dB)**: Balanced preservation
- **Standard adaptivity (0.70)**: Follows SR's program-dependent behaviour

**HIGH Band (1500–6000Hz)**
- **Gentle NR (8dB)**: Consonants and sibilance are fragile; over-processing creates lisp/muffled speech
- **Lower smoothing (gs=6)**: Preserve transient detail for intelligibility
- **Lower residual floor (-40dB)**: Can go deeper without damaging voice character
- **Faster adaptivity (0.55)**: Consonants are transient; need quick response

**AIR Band (>6000Hz)**
- **Minimal NR (5dB)**: Breathiness and "air" define voice presence; aggressive NR sounds dull
- **Minimal smoothing (gs=3)**: Maximum transient preservation
- **Lowest residual floor (-42dB)**: HF noise is less objectionable than HF damage
- **Fastest adaptivity (0.45)**: Sibilants and breath sounds are extremely transient

### Design Philosophy: Transparency Over Depth

Following SR's Least Treatment Principle, we prioritise:

1. **Transparency** — No audible artifacts, even at the cost of leaving some noise
2. **Gain-stage tolerance** — Processed audio must survive 10–15dB of subsequent normalisation
3. **Voice preservation** — Minor HF rolloff acceptable; metallic artifacts are not
4. **Adaptivity** — Parameters adjust to measured noise characteristics

### Primary Implementation: Multi-Band afftdn

The complete filter chain for the 4-band architecture:

```
acrossover=split=200 1500 6000:order=4th[low][mid][high][air];
[low]afftdn=nr=14:nf=-50:tn=enabled:gs=14:rf=-34:ad=0.85:nt=v[low_dn];
[mid]afftdn=nr=10:nf=-50:tn=enabled:gs=10:rf=-38:ad=0.70:nt=w[mid_dn];
[high]afftdn=nr=8:nf=-50:tn=enabled:gs=6:rf=-40:ad=0.55:nt=w[high_dn];
[air]afftdn=nr=5:nf=-50:tn=enabled:gs=3:rf=-42:ad=0.45:nt=s[air_dn];
[low_dn][mid_dn][high_dn][air_dn]amix=inputs=4:weights=1 1 1 1
```

Note: The `nf` (noise floor) parameter is set from Pass 1 measurements and applies identically to all bands. The band-specific parameters (`nr`, `gs`, `rf`, `ad`) are what differentiate the processing.

### Fallback Implementation: Single-Stage afftdn

For simpler deployments or when multi-band overhead is undesirable, a single-stage approach approximates SR behaviour with weighted-average parameters:

```
afftdn=nr=10:nf=-50:tn=enabled:gs=8:rf=-38:ad=0.70:nt=w
```

This trades per-band optimisation for simplicity. The `gs=8` value is higher than your current simple filter's `gs=5`, which should reduce metallic ringing.

### afftdn Parameter Reference

The `afftdn` filter provides the closest approximation to SR's spectral analysis approach. Key parameters mapped to SR concepts:

| afftdn Parameter | SR Equivalent | Purpose |
|------------------|---------------|---------|
| `nr` (noise reduction) | Compander depth | Amount of gain reduction applied |
| `nf` (noise floor) | Threshold levels | Where processing begins |
| `tn` (track noise) | Action substitution | Adaptive response to changing noise |
| `gs` (gain smooth) | Time constants | Artifact prevention via spectral averaging |
| `rf` (residual floor) | Least Treatment | Preserves signal below noise floor |
| `ad` (adaptivity) | Program-dependent | How fast gains adjust per bin |
| `nt` (noise type) | Spectral profile | Noise model selection |

#### Why afftdn Over anlmdn or afwtdn?

| Filter | Strengths | Weaknesses | SR Alignment |
|--------|-----------|------------|--------------|
| **afftdn** | Spectral control, gain smoothing, noise tracking | Can produce "musical noise" without care | ✓ Best match for spectral band processing |
| **afwtdn** | Excellent transient preservation | Less precise spectral control | Partial (wavelet ≈ multi-band) |
| **anlmdn** | Temporal coherence | No spectral band control, computationally heavy | ✗ Wrong approach for SR emulation |

### Recommended afftdn Configuration

```
afftdn=nr=<adaptive>:nf=<measured>:tn=enabled:gs=<adaptive>:rf=<adaptive>:ad=<adaptive>:nt=<selected>
```

#### Parameter Rationale

**Noise Reduction (nr)** — SR achieved 24dB at HF, 16dB at LF. Without a noise sample, we target much more conservative values:

| Condition | nr Value | Rationale |
|-----------|----------|-----------|
| Clean source (floor < -65dB) | 6–8dB | Minimal intervention |
| Moderate noise (-55 to -65dB) | 10–14dB | Standard podcast cleanup |
| Noisy source (> -55dB) | 14–18dB | Aggressive but capped |
| **Maximum** | 18dB | Hard cap to prevent voice damage |

**Gain Smoothing (gs)** — This is the **critical artifact-prevention parameter**. SR used 80–300ms time constants; `gs` spreads gain changes across neighbouring frequency bins:

| Condition | gs Value | Rationale |
|-----------|----------|-----------|
| Clean source | 3–5 | Minimal smoothing preserves detail |
| Moderate noise | 8–12 | Standard SR-style smoothing |
| Severe noise | 12–18 | Maximum artifact prevention |
| Metallic ringing detected | +5 | Increase if artifacts appear |

Higher `gs` values trade detail for smoothness—exactly SR's philosophy.

**Residual Floor (rf)** — SR's Least Treatment Principle means never fully removing noise. The residual floor preserves signal components that might be mistaken for noise:

| Condition | rf Value | Rationale |
|-----------|----------|-----------|
| Clean source | -40dB | Preserve room tone |
| Moderate noise | -36dB | Standard podcast processing |
| Noisy source | -32dB | More aggressive but leaves natural floor |

**Adaptivity (ad)** — Controls how fast per-bin gains adjust. SR used different time constants for different stages:

| Condition | ad Value | Rationale |
|-----------|----------|-----------|
| Stable noise (low flux) | 0.8–0.9 | Slow adaptation, stable processing |
| Dynamic noise (high flux) | 0.5–0.6 | Faster adaptation to changes |
| Default | 0.7 | SR-style moderate adaptivity |

**Noise Type (nt)** — SR used spectral analysis to identify noise characteristics. We map to afftdn's noise models:

| Spectral Profile | nt Value | Detection |
|------------------|----------|-----------|
| High flatness, high entropy | `w` (white) | Broadband hiss (HVAC, fans) |
| Strong LF, steep slope | `v` (vinyl) | Rumble, hum, room resonance |
| High centroid, high rolloff | `s` (shellac) | Tape hiss, preamp noise |

### Alternative Implementation: afwtdn (Wavelet-Based)

For recordings where afftdn produces stubborn artifacts, the wavelet-based `afwtdn` offers different characteristics:

```
afwtdn=sigma=<adaptive>:levels=10:softness=<adaptive>:percent=<adaptive>
```

#### Why Consider afwtdn?

Wavelets provide **time-frequency localisation** that FFT cannot match. This makes afwtdn potentially superior for:
- Preserving sharp transients (plosives, consonants)
- Handling non-stationary noise
- Avoiding "musical noise" entirely

| afwtdn Parameter | SR Equivalent | Purpose |
|------------------|---------------|---------|
| `sigma` | Compander depth | Denoising strength (dB notation: e.g., `-40dB`) |
| `levels` | Band count | Wavelet decomposition depth |
| `softness` | Knee/transition | Thresholding gentleness |
| `percent` | Least Treatment | Partial denoising (never 100%) |

#### Recommended afwtdn Configuration

| Condition | sigma | softness | percent |
|-----------|-------|----------|---------|
| Clean source | -50dB | 3 | 70% |
| Moderate noise | -42dB | 5 | 80% |
| Noisy source | -35dB | 7 | 85% |

The `percent` parameter directly implements SR's Least Treatment Principle—always leave some noise rather than risk artifacts.

### Hybrid Approach: afftdn + afwtdn

For maximum quality, chain both filters with complementary settings:

```
afftdn=nr=8:nf=-50:tn=enabled:gs=10:rf=-38:ad=0.7,afwtdn=sigma=-48dB:softness=4:percent=75
```

This achieves:
- **afftdn**: Broadband noise floor reduction with spectral tracking
- **afwtdn**: Transient preservation and residual cleanup

The combined reduction is additive but with diminishing returns—two 8dB reductions don't yield 16dB. However, the different processing approaches mean artifacts that might appear in one are often masked by the other.

## Adaptive Tuning from Measurements

All parameters adapt based on Pass 1 audio measurements. The multi-band architecture allows per-band adjustments.

### Per-Band Noise Reduction Tuning

```go
// tuneDolbySRBands adapts per-band NR based on noise severity and LUFS gap
func tuneDolbySRBands(cfg *DolbySRDenoiseConfig, measurements *AudioMeasurements, lufsGap float64) {
    // Base multiplier from noise floor severity
    var nrMultiplier float64
    switch {
    case measurements.NoiseFloor < dolbySRFloorClean:
        nrMultiplier = 0.6 // Clean source: reduce all bands
    case measurements.NoiseFloor > dolbySRFloorNoisy:
        nrMultiplier = 1.3 // Noisy source: increase all bands
    default:
        nrMultiplier = 1.0 // Standard
    }

    // LUFS adjustment (subsequent gain amplifies noise)
    lufsAdjust := lufsGap * dolbySRLUFSScaleFactor
    if lufsAdjust > 4.0 {
        lufsAdjust = 4.0 // Cap per-band LUFS boost
    }

    // Apply to each band with caps
    cfg.LowBand.NoiseReduction = clamp(
        dolbySRNRLow*nrMultiplier+lufsAdjust,
        dolbySRNRMin, dolbySRNRMax,
    )
    cfg.MidBand.NoiseReduction = clamp(
        dolbySRNRMid*nrMultiplier+lufsAdjust*0.8, // Less boost for mid
        dolbySRNRMin, dolbySRNRMax,
    )
    cfg.HighBand.NoiseReduction = clamp(
        dolbySRNRHigh*nrMultiplier+lufsAdjust*0.5, // Even less for high
        dolbySRNRMin, dolbySRNRMax,
    )
    cfg.AirBand.NoiseReduction = clamp(
        dolbySRNRAir*nrMultiplier+lufsAdjust*0.3, // Minimal for air
        dolbySRNRMin, dolbySRNRMax,
    )
}
```

### Gain Smoothing Adaptation

```go
// tuneDolbySRSmoothing adapts gain smoothing based on noise character
func tuneDolbySRSmoothing(cfg *DolbySRDenoiseConfig, measurements *AudioMeasurements) {
    // Tonal noise (low entropy) needs more smoothing to hide pumping
    var gsBoost int
    if measurements.NoiseProfile != nil && measurements.NoiseProfile.Entropy < 0.3 {
        gsBoost = 4 // Tonal noise: boost all bands
    }

    // Noisy sources need more smoothing (more artifact risk)
    if measurements.NoiseFloor > dolbySRFloorNoisy {
        gsBoost += 2
    }

    // Apply boost with per-band caps
    cfg.LowBand.GainSmooth = min(dolbySRGSLow+gsBoost, 20)
    cfg.MidBand.GainSmooth = min(dolbySRGSMid+gsBoost, 16)
    cfg.HighBand.GainSmooth = min(dolbySRGSHigh+gsBoost, 10)
    cfg.AirBand.GainSmooth = min(dolbySRGSAir+gsBoost, 6) // Keep air responsive
}
```

### Residual Floor Adaptation

```go
// tuneDolbySRResidual adapts residual floor based on noise character
func tuneDolbySRResidual(cfg *DolbySRDenoiseConfig, measurements *AudioMeasurements) {
    // Base adjustment from noise floor
    var rfAdjust float64
    switch {
    case measurements.NoiseFloor < dolbySRFloorClean:
        rfAdjust = -2.0 // Clean: can go deeper
    case measurements.NoiseFloor > dolbySRFloorNoisy:
        rfAdjust = 4.0 // Noisy: higher floor prevents artifacts
    }

    // Tonal noise needs higher floor (prevents pumping)
    if measurements.NoiseProfile != nil && measurements.NoiseProfile.Entropy < 0.3 {
        rfAdjust += 3.0
    }

    // Apply to each band
    cfg.LowBand.ResidualFloor = clamp(dolbySRRFLow+rfAdjust, -45.0, -28.0)
    cfg.MidBand.ResidualFloor = clamp(dolbySRRFMid+rfAdjust, -45.0, -32.0)
    cfg.HighBand.ResidualFloor = clamp(dolbySRRFHigh+rfAdjust, -48.0, -35.0)
    cfg.AirBand.ResidualFloor = clamp(dolbySRRFAir+rfAdjust, -50.0, -38.0)
}
```

### Noise Type Selection Per Band

```go
// tuneDolbySRNoiseTypes selects noise model per band based on spectral profile
func tuneDolbySRNoiseTypes(cfg *DolbySRDenoiseConfig, measurements *AudioMeasurements) {
    // LOW band: usually rumble/hum → vinyl, but white if broadband
    if measurements.SpectralFlatness > 0.7 {
        cfg.LowBand.NoiseType = "w"
    } else {
        cfg.LowBand.NoiseType = "v" // Default: vinyl for LF-weighted
    }

    // MID band: usually broadband → white
    cfg.MidBand.NoiseType = "w"

    // HIGH band: check for tape hiss characteristics
    if measurements.SpectralCentroid > 5000 && measurements.SpectralRolloff > 8000 {
        cfg.HighBand.NoiseType = "s" // Shellac for HF-weighted
    } else {
        cfg.HighBand.NoiseType = "w"
    }

    // AIR band: usually HF noise → shellac
    if measurements.SpectralRolloff > 10000 {
        cfg.AirBand.NoiseType = "s"
    } else {
        cfg.AirBand.NoiseType = "w"
    }
}
```

### Complete Tuning Function

```go
// tuneDolbySRDenoise applies all adaptive tuning to the multi-band config
func tuneDolbySRDenoise(cfg *DolbySRDenoiseConfig, measurements *AudioMeasurements, lufsGap float64) {
    if !cfg.Enabled {
        return
    }

    // Disable for very clean sources (nothing to do)
    if measurements.NoiseFloor < -72.0 {
        cfg.Enabled = false
        return
    }

    // Set shared noise floor from measurements
    cfg.NoiseFloor = measurements.NoiseFloor

    // Tune each aspect
    tuneDolbySRBands(cfg, measurements, lufsGap)
    tuneDolbySRSmoothing(cfg, measurements)
    tuneDolbySRResidual(cfg, measurements)
    tuneDolbySRNoiseTypes(cfg, measurements)
}
```

## Comparison Summary

| Feature | Dolby SR | Jivetalking DolbySRDenoise |
|---------|----------|----------------------------|
| **Multi-band processing** | ✅ 10 companders | ✅ 4-band acrossover |
| **Band-specific parameters** | ✅ Different thresholds/ratios | ✅ Different nr/gs/rf/ad per band |
| **Spectral analysis** | ✅ Fixed + sliding bands | ✅ FFT-based (afftdn) |
| **Adaptive tracking** | ✅ Action substitution | ✅ `tn=enabled` per band |
| **Frequency-dependent NR** | ✅ 24dB HF, 16dB LF | ✅ 5dB air → 14dB low |
| **Time constants** | ✅ 4–300ms band-specific | ✅ ad + gs per band |
| **Least Treatment** | ✅ Core principle | ✅ Residual floor per band |
| **Maximum NR depth** | 24dB | 14dB low, 5dB air (speech-safe) |
| **Crossover architecture** | 800Hz primary | 200/1500/6000Hz speech-optimised |

## Implementation Constants

```go
const (
    // Dolby SR-inspired crossover frequencies (Hz)
    // SR used 800Hz primary crossover with 200-3kHz overlap
    // We use 4 bands optimised for speech
    dolbySRCrossoverLowMid  = 200   // Below: fundamentals, above: formants
    dolbySRCrossoverMidHigh = 1500  // Below: vowels, above: consonants
    dolbySRCrossoverHighAir = 6000  // Below: sibilance, above: air/breath
    dolbySRCrossoverOrder   = 4     // 4th order = 24dB/octave slopes

    // Per-band noise reduction (dB)
    // SR: 24dB HF, 16dB LF. We invert for speech (preserve HF detail)
    dolbySRNRLow  = 14.0 // Aggressive on LF rumble
    dolbySRNRMid  = 10.0 // Moderate on core voice
    dolbySRNRHigh = 8.0  // Gentle on consonants
    dolbySRNRAir  = 5.0  // Minimal on presence

    // Per-band gain smoothing (artifact prevention)
    // Higher = more smoothing = fewer artifacts but less detail
    dolbySRGSLow  = 14 // Slow LF dynamics tolerate heavy smoothing
    dolbySRGSMid  = 10 // SR-style moderate smoothing
    dolbySRGSHigh = 6  // Preserve consonant transients
    dolbySRGSAir  = 3  // Maximum detail preservation

    // Per-band residual floor (dB)
    // Least Treatment Principle: never fully remove noise
    dolbySRRFLow  = -34.0 // Higher floor preserves body
    dolbySRRFMid  = -38.0 // Standard processing
    dolbySRRFHigh = -40.0 // Can go deeper safely
    dolbySRRFAir  = -42.0 // Deepest for inaudible HF

    // Per-band adaptivity (0=instant, 1=slowest)
    // SR used different time constants per stage
    dolbySRAdLow  = 0.85 // Slow: LF noise is stationary
    dolbySRAdMid  = 0.70 // Standard: SR program-dependent
    dolbySRAdHigh = 0.55 // Faster: consonants are transient
    dolbySRAdAir  = 0.45 // Fastest: breath/sibilance

    // Per-band default noise types
    dolbySRNTLow  = "v" // Vinyl: LF-weighted for rumble
    dolbySRNTMid  = "w" // White: broadband default
    dolbySRNTHigh = "w" // White: broadband default
    dolbySRNTAir  = "s" // Shellac: HF-weighted for hiss

    // Single-stage fallback parameters (weighted average)
    dolbySRFallbackNR = 10.0
    dolbySRFallbackGS = 8
    dolbySRFallbackRF = -38.0
    dolbySRFallbackAd = 0.70

    // Global limits
    dolbySRNRMax          = 18.0  // Hard cap per band
    dolbySRNRMin          = 4.0   // Minimum useful reduction
    dolbySRLUFSScaleFactor = 0.20 // Conservative LUFS adjustment per band

    // Noise floor thresholds for adaptive tuning
    dolbySRFloorClean    = -65.0 // Below: minimal processing
    dolbySRFloorModerate = -55.0 // Standard processing
    dolbySRFloorNoisy    = -50.0 // Above: aggressive processing
)
```

## Multi-Band Filter Builder

```go
// DolbySRBandConfig holds per-band afftdn parameters
type DolbySRBandConfig struct {
    NoiseReduction float64 // nr: dB of reduction
    GainSmooth     int     // gs: spectral smoothing radius
    ResidualFloor  float64 // rf: dB floor for residual
    Adaptivity     float64 // ad: 0=instant, 1=slowest
    NoiseType      string  // nt: w/v/s/c
}

// DolbySRDenoiseConfig holds the complete multi-band configuration
type DolbySRDenoiseConfig struct {
    Enabled    bool
    NoiseFloor float64 // Shared across all bands (from measurements)

    // Per-band configurations
    LowBand  DolbySRBandConfig // <200Hz
    MidBand  DolbySRBandConfig // 200-1500Hz
    HighBand DolbySRBandConfig // 1500-6000Hz
    AirBand  DolbySRBandConfig // >6000Hz

    // Crossover settings
    CrossoverLowMid  int // Hz
    CrossoverMidHigh int // Hz
    CrossoverHighAir int // Hz
    CrossoverOrder   int // Filter order (4 = 24dB/oct)
}

// buildDolbySRDenoiseFilter builds the multi-band noise reduction filter chain.
// Returns a filter_complex compatible string for use with FFmpeg.
func (cfg *DolbySRDenoiseConfig) buildDolbySRDenoiseFilter() string {
    if !cfg.Enabled {
        return ""
    }

    // Clamp noise floor to afftdn's valid range
    nf := cfg.NoiseFloor
    if nf < -80.0 {
        nf = -80.0
    } else if nf > -20.0 {
        nf = -20.0
    }

    // Build the multi-band filter chain
    return fmt.Sprintf(
        "acrossover=split=%d %d %d:order=%dth[low][mid][high][air];"+
        "[low]afftdn=nr=%.1f:nf=%.1f:tn=enabled:gs=%d:rf=%.1f:ad=%.2f:nt=%s[low_dn];"+
        "[mid]afftdn=nr=%.1f:nf=%.1f:tn=enabled:gs=%d:rf=%.1f:ad=%.2f:nt=%s[mid_dn];"+
        "[high]afftdn=nr=%.1f:nf=%.1f:tn=enabled:gs=%d:rf=%.1f:ad=%.2f:nt=%s[high_dn];"+
        "[air]afftdn=nr=%.1f:nf=%.1f:tn=enabled:gs=%d:rf=%.1f:ad=%.2f:nt=%s[air_dn];"+
        "[low_dn][mid_dn][high_dn][air_dn]amix=inputs=4:weights=1 1 1 1",
        // Crossover
        cfg.CrossoverLowMid, cfg.CrossoverMidHigh, cfg.CrossoverHighAir, cfg.CrossoverOrder,
        // Low band
        cfg.LowBand.NoiseReduction, nf, cfg.LowBand.GainSmooth,
        cfg.LowBand.ResidualFloor, cfg.LowBand.Adaptivity, cfg.LowBand.NoiseType,
        // Mid band
        cfg.MidBand.NoiseReduction, nf, cfg.MidBand.GainSmooth,
        cfg.MidBand.ResidualFloor, cfg.MidBand.Adaptivity, cfg.MidBand.NoiseType,
        // High band
        cfg.HighBand.NoiseReduction, nf, cfg.HighBand.GainSmooth,
        cfg.HighBand.ResidualFloor, cfg.HighBand.Adaptivity, cfg.HighBand.NoiseType,
        // Air band
        cfg.AirBand.NoiseReduction, nf, cfg.AirBand.GainSmooth,
        cfg.AirBand.ResidualFloor, cfg.AirBand.Adaptivity, cfg.AirBand.NoiseType,
    )
}

// NewDefaultDolbySRConfig creates a DolbySRDenoiseConfig with SR-inspired defaults
func NewDefaultDolbySRConfig() *DolbySRDenoiseConfig {
    return &DolbySRDenoiseConfig{
        Enabled:          true,
        NoiseFloor:       -50.0, // Placeholder, set from measurements
        CrossoverLowMid:  dolbySRCrossoverLowMid,
        CrossoverMidHigh: dolbySRCrossoverMidHigh,
        CrossoverHighAir: dolbySRCrossoverHighAir,
        CrossoverOrder:   dolbySRCrossoverOrder,
        LowBand: DolbySRBandConfig{
            NoiseReduction: dolbySRNRLow,
            GainSmooth:     dolbySRGSLow,
            ResidualFloor:  dolbySRRFLow,
            Adaptivity:     dolbySRAdLow,
            NoiseType:      dolbySRNTLow,
        },
        MidBand: DolbySRBandConfig{
            NoiseReduction: dolbySRNRMid,
            GainSmooth:     dolbySRGSMid,
            ResidualFloor:  dolbySRRFMid,
            Adaptivity:     dolbySRAdMid,
            NoiseType:      dolbySRNTMid,
        },
        HighBand: DolbySRBandConfig{
            NoiseReduction: dolbySRNRHigh,
            GainSmooth:     dolbySRGSHigh,
            ResidualFloor:  dolbySRRFHigh,
            Adaptivity:     dolbySRAdHigh,
            NoiseType:      dolbySRNTHigh,
        },
        AirBand: DolbySRBandConfig{
            NoiseReduction: dolbySRNRAir,
            GainSmooth:     dolbySRGSAir,
            ResidualFloor:  dolbySRRFAir,
            Adaptivity:     dolbySRAdAir,
            NoiseType:      dolbySRNTAir,
        },
    }
}
```

## Expected Outcomes

| Scenario | Current Simple Filter | Proposed DolbySRDenoise |
|----------|----------------------|-------------------------|
| Clean recording (floor -70dB) | 6dB NR, gs=5, may skip | 4-band: 8/6/5/3dB, per-band gs, smooth |
| Standard podcast (-55dB) | 10dB NR, gs=5, slight ringing | 4-band: 14/10/8/5dB, gs=14/10/6/3, no ringing |
| Noisy recording (-45dB) | 10dB NR (capped), artifacts | 4-band: 18/14/10/6dB, boosted gs, controlled |
| Tonal noise (hum) | Generic processing | LOW band vinyl type, +4 gs all bands |
| HF hiss | Generic processing | AIR band shellac type, gentle NR |
| Post-normalisation | Noise amplified | Per-band LUFS compensation, survives 15dB gain |
| Plosives/transients | May smear | AIR/HIGH fast adaptation (0.45/0.55) preserves |
| Voice warmth | Can thin out | LOW band higher rf (-34dB) preserves body |

## Filter Chain Position

```
Input → DS201HighPass → DS201LowPass → DolbySRDenoise → DS201Gate → [Compression] → [Limiting] → Output
                                            ↑
                                      You are here
```

The DolbySRDenoise filter sits after frequency shaping (HP/LP) and before gating. This allows:
- HP filter to remove subsonic content that confuses noise estimation
- LP filter to remove ultrasonic artifacts before denoising
- Gate to clean up inter-phrase residual after denoising

## References

- Dolby SR Technical Overview (Dolby Laboratories, 1986)
- Dolby Cat.280 SR Noise Reduction Unit Manual
- FFmpeg [afftdn filter documentation](https://ffmpeg.org/ffmpeg-filters.html#afftdn)
- FFmpeg [afwtdn filter documentation](https://ffmpeg.org/ffmpeg-filters.html#afwtdn)
- Sound On Sound: [Tape Noise Reduction](https://www.soundonsound.com/techniques/tape-noise-reduction)
