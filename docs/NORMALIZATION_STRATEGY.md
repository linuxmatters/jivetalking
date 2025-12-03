# Normalization Strategy for Jivetalking

**Date:** 2025-11-06
**Status:** Post-loudnorm removal, investigating speechnorm integration

## Executive Summary

Jivetalking's normalization approach has evolved from a failed loudnorm-based strategy to a dynaudnorm-only approach, with speechnorm identified as a promising addition to solve input level disparity issues.

---

## The loudnorm Failure (Phase 1)

### What We Tried
Re-enabled loudnorm with "gentle" settings as primary LUFS normalizer:
- Target: -18 LUFS (gentler than -16)
- True Peak: -2.0 dBTP
- LRA: 11.0 LU (wider range)
- dynaudnorm as secondary fine-tuning with conservative settings

### Why It Failed Spectacularly

**The fundamental problem:** loudnorm is LINEAR GAIN - it applies whatever gain needed to hit target LUFS regardless of source quality or content.

**Evidence from test files:**

| Source | Input LUFS | loudnorm Gain | Result |
|--------|-----------|---------------|---------|
| Mark | -30.3 | +14.3 dB | Hot, room noise audible, limiter crushing |
| Martin | -31.1 | +15.1 dB | Hot, room noise audible, limiter crushing |
| Popey | -46.1 | **+30.1 dB** | Squashed into silence, completely destroyed |

**What went wrong:**
1. **Noise amplification:** 30dB of gain means noise floor -88dB → -58dB (very audible)
2. **Cascading problems:** loudnorm applies massive gain → dynaudnorm tries to normalize the now-loud noise → limiter crushes everything
3. **Pumping/breathing:** Adaptive filters trying to normalize amplified noise created artifacts
4. **Mathematical precision ≠ quality:** Hitting "-16.0 LUFS ✓" exactly is worthless if audio is destroyed

**User verdict:** *"loudnorm hasn't once helped with anything in the project"* - eradicated from codebase.

### Lesson Learned
**METHOD > MATHEMATICS**

Same adjustment value (+15dB) but completely different results:
- **loudnorm's +15dB:** Linear gain amplifier → amplifies everything including noise proportionally
- **dynaudnorm's +15dB:** Adaptive frame-by-frame → amplifies speech peaks intelligently, manages noise

---

## Current Approach: dynaudnorm Only

### Why dynaudnorm Works Better

From FFmpeg documentation:
> "the Dynamic Audio Normalizer achieves this goal WITHOUT applying dynamic range compressing. It will retain 100% of the dynamic range WITHIN each section"

**Key characteristics:**
- Works on 500ms frames (temporal smoothing)
- Uses Gaussian filter (31-frame window = smooth transitions)
- Adaptive local normalization (not global linear gain)
- Preserves dynamics within each section
- Prevents pumping through temporal averaging

**Configuration (conservative):**
```
framelen=500       # 500ms balanced window
filtersize=31      # Gaussian smoothing (default)
peakvalue=0.95     # 5% headroom
maxgain=5.0        # Conservative 5x max (not 10-25x)
targetrms=0.0      # No RMS targeting (peak-based only)
compress=0.0       # No compression (acompressor handles this)
threshold=0.0      # Normalize all frames
```

### Results After loudnorm Removal

**Same test files, dramatically different quality:**

| Metric | Mark | Martin | Popey |
|--------|------|--------|-------|
| Processing time | 1m 27s (27% faster) | 1m 20s (29% faster) | 1m 25s (27% faster) |
| Audio quality | "sounds good" | "sounds good" | "sounds good" |
| Waveform | Clean, natural dynamics | Clean, natural dynamics | Consistent, not squashed |

**User assessment:** *"The audio sounds good"* - validation that adaptive normalization > linear gain.

---

## The Remaining Problem: Input Level Disparity

### The Issue

Despite better quality, there's still disparity between sources with very different input levels:

**Mark (-30.3 LUFS):**
- Compression: Ratio 2.0:1, Threshold -16dB, Makeup +1dB
- dynaudnorm applies: ~15dB adaptive gain
- Result: Good balance

**Popey (-46.1 LUFS):**
- Compression: Ratio 2.0:1, Threshold -16dB, Makeup +1dB ← **SAME!**
- dynaudnorm applies: ~30dB adaptive gain ← **DOUBLE!**
- Result: Different tonal character despite "sounding good"

### Root Cause Analysis

**Problem 1: Makeup gain ignores input level**

Current logic (processor.go lines 166-176):
```go
if measurements.DynamicRange > 30.0 {
    config.CompMakeup = 1.0  // Based ONLY on dynamic range
} else if measurements.DynamicRange > 20.0 {
    config.CompMakeup = 2.0
} else {
    config.CompMakeup = 3.0
}
```

Both Mark (DR: 91.2) and Popey (DR: 81.5) get `CompMakeup = 1.0` despite **16dB input difference**.

**Problem 2: Compressor sees raw input levels**

Current chain order:
```
highpass → adeclick → denoise → gate → compress → deess → dynaudnorm → limit
```

For Popey at -46 LUFS:
- Compression barely engages (signal too quiet to hit -16dB threshold)
- +1dB makeup is insufficient
- dynaudnorm has to do heroic 30dB gain to compensate

For Mark at -30 LUFS:
- Compression works normally
- +1dB makeup is reasonable
- dynaudnorm does moderate 15dB gain

**The fundamental issue:** We're treating compression as if all sources start at the same level, but they don't.

---

## Proposed Solution: speechnorm Integration

### What is speechnorm?

Speech-optimized FFmpeg filter that works on waveform half-cycles (zero crossings):

**Characteristics:**
- **Cycle-level operation:** Works on individual waveform cycles (microseconds)
- **Expand/compress logic:** Expands quiet half-cycles, compresses loud ones based on threshold
- **Adaptive ramping:** raise/fall parameters prevent sudden gain changes
- **Speech-specific:** Designed for speech content (in the name!)
- **Peak or RMS:** Can target peak values OR RMS (potential LUFS approximation)

**Key difference from dynaudnorm:**
- **speechnorm:** Fast, local, waveform-cycle based
- **dynaudnorm:** Slow, temporal, 500ms frame based

### Recommended Placement: BEFORE acompressor

**Proposed chain:**
```
highpass → adeclick → denoise → gate → SPEECHNORM → compress → deess → dynaudnorm → limit
```

### How This Solves the Disparity

**Stage 1: speechnorm (local normalization)**
- Expands Popey's quiet half-cycles UP (local boost)
- Compresses Mark's louder half-cycles DOWN slightly
- Result: Both sources exit at **more similar levels**

**Stage 2: acompressor (dynamics control)**
- Now sees similar input levels from all sources
- Can apply consistent compression strategy
- Makeup gain works predictably

**Stage 3: dynaudnorm (temporal smoothing)**
- Both sources already closer together
- Less heroic gain needed (maybe 15-20dB vs 15-30dB)
- More consistent final character

### Starting Parameters

```
speechnorm=p=0.95:e=3.0:c=2.0:t=0.10:r=0.001:f=0.001:l=1
```

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| `p` (peak) | 0.95 | Peak target (matches dynaudnorm) |
| `e` (expansion) | 3.0 | 3x max expansion (more than 2x default, helps quiet sources) |
| `c` (compression) | 2.0 | 2x compression (conservative, matches comp ratio) |
| `t` (threshold) | 0.10 | 10% threshold (expand below, compress above) |
| `r` (raise) | 0.001 | Slow raise rate (smooth, prevents artifacts) |
| `f` (fall) | 0.001 | Slow fall rate (smooth, prevents artifacts) |
| `l` (link) | 1 | Linked channels (mono anyway, but explicit) |

### Alternative Placements Considered

**Option 1:** AFTER compression, BEFORE dynaudnorm
```
... → compress → deess → speechnorm → dynaudnorm → limit
```
- Two-stage normalization: local then temporal
- Works on already-compressed signal
- Use if pre-compression placement doesn't help

**Option 2:** INSTEAD OF dynaudnorm
```
... → compress → deess → speechnorm → limit
```
- Simpler chain, single normalization stage
- Use RMS parameter to approximate LUFS targeting
- Speech-optimized for speech content
- Might be faster processing

**Option 3:** AFTER dynaudnorm (polish pass)
```
... → compress → deess → dynaudnorm → speechnorm → limit
```
- Final refinement after temporal normalization
- Lowest risk but might be redundant

---

## Additional Improvements Needed

### 1. Adaptive Makeup Gain (High Priority)

Make compression makeup gain aware of input LUFS level:

```go
// Current (WRONG): Only based on dynamic range
if measurements.DynamicRange > 30.0 {
    config.CompMakeup = 1.0
}

// Proposed: Factor in input level
baselineLevel := -30.0  // "Normal" podcast level
levelDelta := measurements.InputI - baselineLevel

if measurements.InputI < -40.0 {
    // Very quiet source (like Popey)
    config.CompMakeup = 8.0 + (levelDelta * 0.2)
} else if measurements.InputI < -25.0 {
    // Normal range (like Mark/Martin)
    config.CompMakeup = 3.0 + (measurements.DynamicRange / 30.0)
} else {
    // Already loud
    config.CompMakeup = 1.0
}
```

### 2. Adaptive Compression Threshold

Lower threshold for very quiet sources:

```go
if measurements.InputI < -40.0 {
    config.CompThreshold = -24.0  // Lower threshold allows compression to engage
} else {
    config.CompThreshold = -16.0  // Standard threshold
}
```

### 3. Consider dynaudnorm compress Parameter

For very dynamic/quiet sources, enable soft-knee compression:

```go
if measurements.InputI < -40.0 && measurements.DynamicRange > 30.0 {
    config.DynaudnormCompress = 5.0  // Combine normalization WITH compression
}
```

---

## Implementation Phases

### Phase 1: Baseline Established ✅
- Removed loudnorm completely
- dynaudnorm-only approach
- Honest reporting (no fake LUFS measurements)
- Audio quality: "sounds good"

### Phase 2: Investigate speechnorm (CURRENT)
- Add speechnorm BEFORE acompressor
- Test with all three sources
- Measure disparity reduction
- Tune parameters based on results

### Phase 3: Refine Compression (PENDING)
- Implement adaptive makeup gain
- Consider adaptive threshold
- Test interaction with speechnorm

### Phase 4: Final Optimization (PENDING)
- Fine-tune all parameters based on testing
- Consider alternative speechnorm placements if needed
- Document final configuration

---

## Key Principles

1. **Quality over mathematics:** Hitting -16 LUFS exactly is worthless if audio is destroyed
2. **Adaptive > Linear:** Adaptive normalization preserves quality, linear gain amplifies garbage
3. **Context-aware processing:** Very quiet sources need different treatment than normal ones
4. **Speech-optimized for speech:** Use speech-specific tools (speechnorm) for podcast content
5. **Measure, don't assume:** Test with real content, listen critically, adjust based on results

---

## References

- [FFmpeg speechnorm filter](https://ffmpeg-graph.site/filters/speechnorm/)
- [FFmpeg dynaudnorm filter](https://ffmpeg-graph.site/filters/dynaudnorm/)
- [FFmpeg acompressor filter](https://ffmpeg-graph.site/filters/acompressor/)
- Previous analysis: `LOUDNESS_ANALYSIS.md` (Phase 1 failure documentation)

---

## Next Steps

1. ✅ Document current state and proposed solution (this document)
2. ⏳ Implement speechnorm integration with recommended placement
3. ⏳ Test with Mark, Martin, and Popey sources
4. ⏳ Analyze disparity reduction and audio quality
5. ⏳ Implement adaptive makeup gain improvements
6. ⏳ Iterate based on results

**Status:** Ready to collaborate on Phase 2 implementation.
