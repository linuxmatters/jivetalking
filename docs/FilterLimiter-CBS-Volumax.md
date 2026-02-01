# CBS Volumax — The Invisible Limiter

*"The best limiter is one you never hear."*
— Design philosophy of CBS Laboratories' audio processing division

---

## The Legend

In the early 1960s, FM radio faced a crisis. The medium's high-fidelity promise was being betrayed by overmodulation - that harsh, distorted sound when peaks exceeded the transmitter's capacity. Aggressive limiters existed, but they created their own problems: audible pumping, dulled transients, and a processed character that ruined the transparency FM was supposed to deliver.

CBS Laboratories, the research arm of the Columbia Broadcasting System in Stamford, Connecticut, took on the challenge. Under the leadership of Dr. Peter Goldmark - the inventor of the LP record - the team developed something revolutionary: a limiter that worked so transparently that engineers forgot it was there.

The **CBS Volumax Audio Peak Limiter** was introduced in 1961 and immediately became the broadcast industry standard. Where competitors grabbed and squeezed audio aggressively, the Volumax employed gentle program-dependent limiting that preserved the natural character of speech and music. The FM version was particularly innovative: it processed the pre-emphasised high frequencies in a separate sidechain, preventing random high-frequency transients from triggering the entire limiter.

Both the Volumax and its companion AGC, the CBS Audimax, were considered "the gold standard for audio processing used in the AM/FM and Television Broadcasting industry." Radio stations trusted them with everything from classical music to rock and roll, knowing the Volumax would never betray itself to listeners.

---

## The Volumax Philosophy

The Volumax embodied a fundamentally different approach to limiting than studio units like the UREI 1176:

| Aspect | UREI 1176 | CBS Volumax |
|--------|-----------|-------------|
| **Design goal** | Character, punch, presence | Transparency, invisibility |
| **Attack** | Aggressive (20µs-800µs) | Gentle (several milliseconds) |
| **Release** | Fast, rhythmic recovery | Smooth, program-dependent |
| **Sonic signature** | "Forward," harmonically rich | "Clean," neutral |
| **Use case** | Studio tracking, creative effect | Broadcast protection, final safety |
| **Trigger behaviour** | Catches every peak decisively | Responds only when necessary |

The 1176 was designed to be heard - its FET gain reduction added harmonic content, its fast attack created punch, its quick release gave rhythmic energy. Engineers reached for it precisely because it added character.

The Volumax was designed to be invisible. Its job was to prevent overmodulation without the listener ever knowing it had intervened. This required:

- **Gentle attack times** that preserved transient shape
- **Smooth release curves** that eliminated pumping artifacts
- **Program-dependent behaviour** that adapted to the audio content
- **Frequency-conscious limiting** (on FM version) that prevented false triggering

---

## Technical Characteristics

| Parameter | CBS Volumax | Jivetalking Implementation |
|-----------|-------------|---------------------------|
| **Attack** | ~5ms (gentle) | 5ms |
| **Release** | ~100-200ms (smooth) | 100ms |
| **Limiting** | Soft, program-dependent | ASC enabled, level 0.8 |
| **Character** | Transparent | Transparent |
| **Purpose** | Prevent overmodulation | Create headroom for loudnorm |

The Volumax achieved its transparency through several innovations:

**Gentle Attack (5ms+):** Unlike the 1176's 20µs attack that catches every transient, the Volumax allowed natural transients to pass. This preserved the shape of consonants in speech and the attack of musical instruments.

**Program-Dependent Release:** The Volumax didn't release at a fixed rate. It adapted to the programme material, releasing quickly after brief peaks but slowly during sustained loudness. This prevented the pumping artifacts that fast-release limiters create.

**Soft Limiting Characteristics:** Rather than the 1176's firm knee at 20:1, the Volumax employed softer gain reduction that gradually increased as peaks exceeded threshold. The result was natural-sounding limiting that ears couldn't detect.

---

## Jivetalking's Implementation

Jivetalking uses CBS Volumax-inspired limiting in Pass 4 to create headroom for loudnorm's linear mode. The limiter ensures loudnorm can apply full gain without clipping or falling back to dynamic mode.

### The Linear Mode Problem

FFmpeg's loudnorm filter has two modes:

| Mode | Behaviour | Sound Quality |
|------|-----------|---------------|
| **Dynamic** | Applies varying gain with EQ compensation | Can pump, elevate noise floor |
| **Linear** | Applies consistent gain throughout | Transparent, artefact-free |

Linear mode only works when:
```
measured_true_peak + gain_required ≤ target_true_peak
```

When this condition fails, loudnorm falls back to dynamic mode. The Volumax-style limiter ensures the condition is always met by reducing peaks to an adaptive ceiling calculated from Pass 3 measurements.

### Why Volumax, Not 1176?

The original Jivetalking design used 1176-style limiting with aggressive 0.1ms attack and 50ms release. Testing revealed problems:

| Issue | 1176-Style | Volumax-Style |
|-------|------------|---------------|
| **Transient clicks** | Fast attack created discontinuities | Gentle attack preserves waveform |
| **Pumping on speech** | Fast release audible on sustained vowels | Smooth release inaudible |
| **Plosive handling** | Caught every "P" and "B" aggressively | Natural plosive dynamics preserved |
| **Overall character** | Slightly "processed" sound | Transparent |

For podcast preprocessing, the Volumax's design philosophy - invisibility over character - is exactly right. Listeners should hear the presenter's voice, not the processing chain.

### FFmpeg Implementation

```
alimiter=limit=<ceiling>:attack=5:release=100:level_in=1:level_out=1:level=0:latency=1:asc=1:asc_level=0.8
```

| Parameter | Value | Volumax Equivalent |
|-----------|-------|-------------------|
| `attack` | 5ms | Gentle attack, preserves transients |
| `release` | 100ms | Smooth recovery, no pumping |
| `asc` | 1 (enabled) | Program-dependent behaviour |
| `asc_level` | 0.8 | High smoothing, Volumax characteristic |
| `latency` | 1 (enabled) | Lookahead for transparent limiting |
| `level` | 0 (disabled) | No auto-makeup (loudnorm handles levels) |

### Auto Soft Clipping (ASC)

FFmpeg's `asc` parameter approximates the Volumax's program-dependent behaviour. When enabled:

- Release time adapts to programme content
- Short peaks recover quickly
- Sustained limiting releases smoothly
- The `asc_level` of 0.8 provides high smoothing (0.0 = minimum, 1.0 = maximum)

This prevents the pumping artifacts that fixed-release limiters create on speech, where consonants and vowels have very different envelope characteristics.

---

## Pipeline Integration

```
Pass 2: Processing → Pass 3: Loudnorm Measurement → Pass 4: [Volumax] → Loudnorm
```

**Division of responsibility:**

| Stage | Role |
|-------|------|
| **LA-2A Compressor (Pass 2)** | Dynamic range levelling, warmth |
| **Pass 3 Measurement** | Measures loudness, true peak, LRA |
| **CBS Volumax (Pass 4)** | Creates headroom for linear mode |
| **Loudnorm (Pass 4)** | Final loudness normalisation |

The Volumax operates after all Pass 2 processing and after Pass 3 measurement. It sees the fully processed, measured audio and applies just enough limiting to enable loudnorm's linear mode.

### Adaptive Ceiling Calculation

Unlike the original design's fixed ceiling, Jivetalking calculates the exact ceiling needed:

```go
gainRequired := targetLUFS - measuredLUFS
projectedPeak := measuredTP + gainRequired

if projectedPeak <= targetTP {
    // No limiting needed
    return
}

// Calculate ceiling that allows linear mode
ceiling := targetTP - gainRequired - safetyMargin
```

This means the limiter only reduces peaks by the minimum amount necessary - often just 1-2 dB - rather than applying a fixed ceiling that might over-limit.

---

## The Volumax Legacy

CBS Laboratories was closed in 1986, but the Volumax's influence persists. Its design philosophy - that broadcast limiting should be transparent rather than characterful - shaped how every subsequent broadcast processor approached the problem.

The Audimax and Volumax were the "gold standard" until the arrival of the Orban Optimod in the late 1970s. Even then, the Optimod inherited the Volumax's core insight: in broadcasting, the limiter should be invisible.

For podcast preprocessing, this philosophy is exactly right. The presenter's voice should reach listeners unchanged by processing artifacts. The limiter exists to prevent technical problems, not to add sonic character.

---

*References: CBS Laboratories Wikipedia • "The CBS Audimax and Volumax" (Radio World, 2005) • "Recalling the CBS Audimax 4440" (Radio World, 2018) • FFmpeg [alimiter](https://ffmpeg.org/ffmpeg-filters.html#alimiter)*
