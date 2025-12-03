# Loudness Normalization Analysis & Recommendations

## Current Issue
Processed audio from all three test files exhibits:
- **Hot/slightly distorted output** - audible harshness
- **Clipping at points** - digital distortion
- **Over-aggressive processing** - despite close level matching between presenters

## Root Cause Analysis

### Current Processing Chain
```
highpass → adeclick → afftdn → agate → acompressor → dynaudnorm → alimiter
```

### The Problem: Dynaudnorm Adaptive Tuning

The current adaptive tuning for `dynaudnorm` is **too aggressive**:

1. **Target RMS Conversion** - Converting LUFS to linear RMS value:
   ```go
   targetLUFS := config.TargetI                               // -16.0 LUFS
   targetDBFS := targetLUFS + 23.0                            // ~+7.0 dBFS (!!)
   config.DynaudnormTargetRMS = math.Pow(10, targetDBFS/20.0) // Very hot target
   ```
   **Issue**: The +23dB LUFS→dBFS conversion is approximate and creates an overly hot target

2. **Maximum Gain Based on Quiet Input**:
   ```go
   if measurements.InputI < -40.0 {
       config.DynaudnormMaxGain = 25.0 // Allow very high gain
   ```
   **Issue**: 25x gain (28dB) is excessive and can amplify noise/artifacts

3. **Aggressive Compression**:
   ```go
   if measurements.InputLRA > 15.0 {
       config.DynaudnormCompress = 7.0 // Mild compression
   ```
   **Issue**: Even "mild" compression (7.0) on highly dynamic content causes pumping

4. **No Noise Floor Protection**:
   - Threshold is derived from noise floor but may still normalize quiet passages too aggressively
   - Creates distortion when trying to match hot RMS targets

---

## Recommended Solution: Loudnorm + Dynaudnorm Combination

### Strategy
Use **both** filters in sequence with complementary roles:
1. **Loudnorm** - Gentle, standards-compliant LUFS normalization
2. **Dynaudnorm** - Fine-tuning for consistent perceived loudness across segments

### New Filter Order
```
highpass → adeclick → afftdn → agate → acompressor → deesser → loudnorm → dynaudnorm → alimiter
```

**Key change**: Deesser moved BEFORE loudnorm to prevent amplification of sibilance problems.

---

## Filter Configuration Recommendations

### 1. Loudnorm (Primary Normalization)

**Purpose**: Provide accurate, gentle LUFS-based normalization using EBU R128 standard

**Recommended Settings**:
```go
loudnormFilter := fmt.Sprintf(
    "loudnorm=I=-18.0:TP=-2.0:LRA=11.0:"+
        "measured_I=%.2f:measured_TP=%.2f:measured_LRA=%.2f:"+
        "measured_thresh=%.2f:offset=%.2f:"+
        "linear=false:print_format=summary",
    measurements.InputI, measurements.InputTP, measurements.InputLRA,
    measurements.InputThresh, measurements.TargetOffset,
)
```

**Key Parameters**:
- **I=-18.0 LUFS** (instead of -16.0)
  - Rationale: Gentler target leaves headroom for dynaudnorm fine-tuning
  - Prevents loudnorm from being too aggressive
  - -18.0 is still podcast-appropriate (Spotify uses -14 to -18)

- **TP=-2.0 dBTP** (instead of -0.3)
  - Rationale: More conservative true peak ceiling
  - Prevents inter-sample peaks before dynaudnorm
  - Final limiting happens at alimiter (-1.5 dBTP)

- **LRA=11.0 LU** (instead of 7.0)
  - Rationale: Wider loudness range preserves natural dynamics
  - Prevents "squashing" before dynaudnorm
  - Still within broadcast standards (7-20 LU)

- **linear=false** (dynamic mode)
  - Rationale: Adapts to source material rather than forcing linear scaling
  - Better for varied podcast content
  - Prevents distortion on narrow-LRA sources

**Benefits**:
- Standards-compliant LUFS targeting
- Gentle, musical processing
- Accurate measurements from Pass 1
- Leaves headroom for fine-tuning

---

### 2. Dynaudnorm (Fine-Tuning)

**Purpose**: Smooth out remaining loudness variations between segments/speakers

**Recommended Settings** (Conservative, Non-Adaptive):
```go
dynaudnormFilter := fmt.Sprintf(
    "dynaudnorm=f=500:g=31:p=0.95:m=5.0:r=0.0:t=0.0:n=1:c=0:b=0:s=0.0",
    // f=500:   Frame length 500ms (default, balanced)
    // g=31:    Gaussian filter size 31 (default, smooth)
    // p=0.95:  Peak target 0.95 (default, 5% headroom)
    // m=5.0:   Max gain 5x (reduced from 10x, less aggressive)
    // r=0.0:   No RMS targeting (disabled, let loudnorm handle this)
    // t=0.0:   Normalize all frames (but see gain staging safety check)
    // n=1:     Coupled channels (mono so no effect)
    // c=0:     No DC correction
    // b=0:     Standard boundary mode
    // s=0.0:   No compression (preserve dynamics - acompressor handles this)
)
```

**Note on `s` parameter (compression)**:
- Kept at `s=0.0` to avoid double-compression with acompressor
- While `s=3.0` (light compression) might help even out vocal characteristics, it conflicts with goal of reducing aggression
- Can be tested as optional parameter if needed

**Gain Staging Safety Check**:
```go
// Prevent cascading gain from loudnorm + dynaudnorm
loudnormGain := math.Abs(measurements.TargetOffset)  // Actual needed gain from Pass 1
dynaudnormGainDB := 20 * math.Log10(config.DynaudnormMaxGain)
totalGain := loudnormGain + dynaudnormGainDB

// Safety limit: if total potential gain exceeds 30dB, reduce dynaudnorm's contribution
if totalGain > 30.0 {
    // Calculate safe maximum for dynaudnorm based on loudnorm's workload
    config.DynaudnormMaxGain = math.Max(3.0, math.Pow(10, (30.0-loudnormGain)/20.0))
}
```

**Key Changes from Current**:
- **Removed RMS targeting** (`r=0.0`) - loudnorm already handled LUFS
- **Reduced max gain** (`m=5.0` instead of adaptive 10-25) - prevents over-amplification
- **Safety-limited max gain** - backs off if loudnorm doing heavy lifting
- **No compression** (`s=0.0`) - acompressor already handled this
- **Conservative** - fixed parameters, minimal adaptive behavior

**Benefits**:
- Smooths remaining level variations
- Doesn't try to re-normalize (loudnorm did that)
- Gentle, transparent processing
- No risk of over-amplification or cascading gain

---

### 3. Acompressor (Pre-Normalization)

**Purpose**: Control dynamic range BEFORE normalization to prevent distortion

#### Current vs Recommended

| Parameter | Current | Recommended | Rationale |
|-----------|---------|-------------|-----------|
| **threshold** | -20 dB | **-18 dB** | Higher threshold = less compression on normal speech |
| **ratio** | 2.5:1 | **3.0:1** | Slightly more ratio to control peaks better |
| **attack** | 15 ms | **20 ms** | Slower attack preserves transients |
| **release** | 80 ms | **100 ms** | Slower release sounds more natural |
| **makeup** | 3 dB | **2 dB** | Less makeup (loudnorm will handle gain) |
| **knee** | 2.5 | **3.0** | Softer knee for smoother compression |
| **detection** | RMS | **RMS** | Keep RMS for smooth, musical compression |
| **mix** | 1.0 | **0.85** | Parallel compression (15% dry signal) |

**Recommended Configuration**:
```go
acompressorFilter := fmt.Sprintf(
    "acompressor=threshold=%.6f:ratio=3.0:attack=20:release=100:"+
        "makeup=%.2f:knee=3.0:detection=rms:mix=0.85",
    dbToLinear(-18.0), // -18 dB threshold
    dbToLinear(2.0),   // 2 dB makeup
)
```

**Adaptive Tuning Improvements**:

Based on measurements from Pass 1:

```go
// Adaptive compression based on measured dynamic range
if measurements.DynamicRange > 0 {
    if measurements.DynamicRange > 30.0 {
        // Very dynamic content (expressive delivery)
        config.CompRatio = 2.0     // Gentle ratio
        config.CompThreshold = -16.0 // Higher threshold
        config.CompMakeup = 1.0    // Minimal makeup
    } else if measurements.DynamicRange > 20.0 {
        // Moderately dynamic (typical podcast)
        config.CompRatio = 3.0
        config.CompThreshold = -18.0
        config.CompMakeup = 2.0
    } else {
        // Already compressed/consistent
        config.CompRatio = 4.0     // Stronger ratio for peaks
        config.CompThreshold = -20.0 // Lower threshold
        config.CompMakeup = 3.0    // More makeup
    }
}

// Adaptive attack/release based on loudness range
if measurements.InputLRA > 15.0 {
    // Wide loudness range = preserve transients
    config.CompAttack = 25   // Slower attack
    config.CompRelease = 150 // Slower release
} else if measurements.InputLRA > 10.0 {
    // Moderate range
    config.CompAttack = 20
    config.CompRelease = 100
} else {
    // Narrow range = tighter control
    config.CompAttack = 15
    config.CompRelease = 80
}

// Adaptive parallel compression mix based on recording quality AND dynamic range
var mixFactor float64

// Noise floor indicates recording quality (affects artifact audibility)
if measurements.NoiseFloor < -50 {
    mixFactor = 0.95  // Clean recording baseline - can use more compression
} else if measurements.NoiseFloor < -40 {
    mixFactor = 0.85  // Moderate quality
} else {
    mixFactor = 0.75  // Noisy - gentler processing to mask pumping artifacts
}

// Dynamic range indicates content characteristics (affects how much compression needed)
if measurements.DynamicRange > 30 {
    // Very dynamic - preserve more dry signal
    config.CompMix = mixFactor - 0.10
} else if measurements.DynamicRange > 20 {
    // Moderate dynamics
    config.CompMix = mixFactor
} else {
    // Already compressed - can use more
    config.CompMix = math.Min(1.0, mixFactor + 0.10)
}
```

**Benefits of Adaptive Compression**:
- Uses **actual measurements** (Dynamic Range, LRA, Noise Floor) not derived values
- Gentler on naturally expressive delivery
- Tighter control on already-compressed sources
- Parallel compression preserves naturalness and masks artifacts
- **Quality-aware mixing**: noisy recordings get gentler processing
- **Content-aware mixing**: dynamic content preserves more dry signal
- Better peak control BEFORE normalization = less distortion

---

## Implementation Recommendations

### Phase 1: Implemented Changes
1. ✅ **Re-enable loudnorm** with gentler settings (-18 LUFS, -2 TP, 11 LRA)
2. ✅ **Remove dynaudnorm adaptive tuning** - use fixed conservative values (m=5.0, r=0.0, s=0.0)
3. ✅ **Add gain staging safety check** - prevent cascading gain from loudnorm + dynaudnorm
4. ✅ **Move deesser before loudnorm** - prevent amplification of sibilance problems
5. ✅ **Implement adaptive compression mixing** - based on noise floor + dynamic range
6. ✅ **Improve adaptive compression** - better tuning based on DynamicRange and LRA

### Phase 2: Testing & Validation
1. **Process all three test files** with Phase 1 configuration
2. **Measure output levels** using ffmpeg ebur128
3. **Listen critically** for distortion, harshness, clipping
4. **Compare perceived loudness** between speakers
5. **Gather feedback** for fine-tuning

### Phase 3: Optional Refinements (if needed)
1. Test `dynaudnorm s=3.0` if additional vocal matching needed
2. Adjust compression mix ratios based on real-world results
3. Fine-tune loudnorm LRA target if dynamic range issues persist
4. Optimize gain staging threshold if over/under amplification detected

---

## Expected Results

### Before (Current Issue):
- ❌ Hot, distorted output
- ❌ Clipping at peaks
- ❌ Over-aggressive processing
- ❌ Unnatural "squashed" sound

### After (Recommended Approach):
- ✅ Clean -16 LUFS output (via gentle -18 → fine-tune)
- ✅ No clipping (proper headroom at each stage)
- ✅ Natural dynamics preserved
- ✅ Consistent loudness between speakers
- ✅ Professional broadcast quality

---

## Testing Protocol

1. **Process all three test files** with new configuration
2. **Load into Audacity** and verify:
   - Peak levels below -1.5 dBTP
   - RMS levels around -16 LUFS (use loudness meter)
   - No visible clipping
3. **Listen critically** for:
   - Natural dynamics
   - No distortion/harshness
   - Consistent volume between speakers
   - No pumping/breathing artifacts
4. **Measure with ffmpeg**:
   ```bash
   ffmpeg -i output.flac -af ebur128=framelog=verbose -f null - 2>&1 | grep "I:"
   ```
   Target: I: -16.0 LUFS ±1.0

---

## Summary

**The core issue**: Aggressive dynaudnorm RMS targeting combined with high max gain causes distortion.

**The solution**: Use loudnorm for primary LUFS normalization with gentle settings, then dynaudnorm for subtle fine-tuning only.

**The benefit**: Standards-compliant, professional-quality output without distortion.
