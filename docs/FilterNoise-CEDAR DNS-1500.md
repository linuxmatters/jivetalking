# CEDAR DNS-1500 — The Dialogue Noise Suppressor

*Capturing the adaptive brilliance of the hardware that won an Academy Award and became the de facto standard wherever speech meets noise*

---

## The Legend of the DNS

In 2000, a small company in Cambridge released a product that would transform location audio, film post-production, and broadcast forever. The **CEDAR DNS1000 Dialogue Noise Suppressor** did something no previous tool had achieved: it removed background noise from speech *in real-time* without introducing the metallic artifacts, phasiness, or "underwater" coloration that plagued every competing approach.

Seven years later, the Academy of Motion Picture Arts and Sciences honoured CEDAR's engineers with a **Technical Achievement Award**—an Oscar—recognising that the DNS had fundamentally changed what was possible in dialogue restoration. The DNS-1500 (2007-2019) refined everything the DNS1000 pioneered, adding 96kHz support and improved 2-channel performance while maintaining near-zero latency.

Walk into any film dubbing stage, sports broadcast facility, or news centre today, and you'll find DNS units. The BBC relies on them. NPR relies on them. Every major Hollywood post house has banks of them. When dialogue needs saving—whether an interview recorded beside a busy road, a courtroom drama shot next to an HVAC unit, or a live broadcast from a noisy stadium—the DNS is the tool that makes it work.

### Why CEDAR Changed Everything

Before CEDAR, noise reduction meant compromise. FFT-based spectral subtraction introduced "musical noise"—twinkling, chirping artifacts that made processed audio sound worse than the original. Single-band expanders pumped and breathed. Multiband expanders could achieve transparency but offered no way to learn the actual noise character of each recording.

CEDAR's insight was **adaptive spectral profiling**: continuously identify the noise, then suppress only what you've identified. The DNS doesn't guess what noise sounds like—it *learns* it from your specific recording, then tracks it as conditions change.

| Pre-DNS Approach | Problem | DNS Solution |
|-----------------|---------|--------------|
| Static FFT subtraction | Musical noise, phasiness | Adaptive profiling matches actual noise |
| Manual threshold setting | Wrong for varying conditions | LEARN mode continuously adapts |
| Broadband suppression | Voice affected with noise | Per-band attenuation preserves speech |

The DNS earned its Oscar because it made the impossible routine. Documentaries recorded in factories. Interviews in war zones. Dialogue captured in locations so noisy that ADR seemed inevitable—yet made broadcast-ready by a single rack unit.

---

## The LEARN Philosophy

The DNS's most revolutionary feature was its **LEARN** mode. Rather than requiring engineers to manually analyse noise profiles during silence, LEARN continuously identifies noise *even while speech is present*. The system distinguishes between the spectral signature of human voice and the signature of background contamination, adapting in real-time as conditions change.

This matters because real-world noise isn't static. Air conditioning cycles. Traffic ebbs and flows. Equipment hums drift. A noise profile captured at the start of a recording may be completely wrong five minutes later. LEARN solves this by never stopping its analysis.

### The 6-Band Architecture

The DNS1500 and its successors (DNS2000, DNS3000, DNS 8D) all share a core architecture: **6 frequency bands**, each with independent Attenuation and Bias controls. This isn't arbitrary—it matches how human hearing perceives noise across the frequency spectrum.

| Band | Approximate Range | Purpose |
|------|------------------|---------|
| 1 | Sub-bass (~100 Hz) | Rumble, traffic, HVAC |
| 2 | Bass (~100-300 Hz) | Room tone, male voice fundamentals |
| 3 | Low-mid (~300-800 Hz) | Voice F1 formants, intelligibility foundation |
| 4 | Mid (~800-3300 Hz) | Voice F2 formants, critical intelligibility |
| 5 | Presence (~3300-8000 Hz) | Consonants, sibilance, clarity |
| 6 | Air (~8000-16000 Hz) | Breath, brightness, "air" |

CEDAR's manual is explicit: *"the DNS 8D is not a six-band device; the six control bands were chosen as the best compromise between the amount of control needed and the complexity of the system."* The 6-band interface controls a more sophisticated underlying algorithm, but provides the right granularity for practical use.

### Attenuation vs Bias

The DNS offers two controls per band:

- **Attenuation**: How much of the detected noise to remove (0-100%)
- **Bias**: Adjust what the detector *considers* noise (+/- from learned baseline)

This separation is brilliant. Attenuation is the "how much" knob. Bias is the "what counts" knob. If the DNS is eating voice, lower the Bias—it will detect less as noise. If noise is leaking through, raise the Bias—it will catch more.

For broadcast, CEDAR recommends **conservative settings**: leave Bias at zero, use only enough Attenuation to solve the problem. The DNS philosophy aligns perfectly with our implementation principle: **transparency over depth**.

---

## Jivetalking's Implementation

Jivetalking captures the DNS philosophy through FFmpeg's **`afftdn`** (FFT Denoise) filter—a spectral processing tool that operates on similar principles to CEDAR's original DSP.

### Conditional Architecture: DNS-1500 vs Dolby SR

Jivetalking uses **one noise reduction stage, not two**. The choice between DNS-1500 and Dolby SR is made automatically based on whether we can learn a noise profile:

```
IF silence_region_found:
    Enable DNS-1500 (with learned profile)
    Disable Dolby SR
ELSE:
    Disable DNS-1500 (no profile = no-op)
    Enable Dolby SR (profile-free fallback)
```

**Why not both?** Both filters target the same problem—noise floor reduction. Stacking them risks over-processing, which violates our "transparency over depth" principle. One job, one tool.

| Condition | Active Filter | Rationale |
|-----------|--------------|-----------|
| Good silence region detected | **DNS-1500** | Learned profile enables precise, targeted removal |
| No usable silence region | **Dolby SR** | Expansion-based approach needs no profile |

The silence region acts as a **quality gate**. If we found clean room tone, we have reliable noise characterisation—DNS is the superior choice. If we didn't, we're guessing—fall back to the profile-free expander approach that can't go wrong from a bad profile.

### The LEARN Implementation

Jivetalking implements CEDAR-style noise learning through **inline sampling** using FFmpeg's `asendcmd` filter. This approach learns the noise profile *during* Pass 2 processing, eliminating the need for separate WAV extraction:

1. **Pass 1 identifies silence region** through 250ms interval sampling with spectral analysis
2. **Pass 2 triggers inline learning** via `asendcmd` at the silence timestamps
3. **`afftdn` learns and applies** the profile in a single filter graph pass

**Why inline learning?** The filter chain `HP → LP → afftdn` processes audio sequentially. When `afftdn` receives the `sn=start` command during the silence region, it samples noise that has already passed through HP/LP filtering—automatic spectral alignment without separate extraction.

```
asendcmd=c='[0.5] afftdn@dns1500 sn start; [1.2] afftdn@dns1500 sn stop',
afftdn@dns1500=nr=12:nf=-55:tn=1:ad=0.5:gs=8
```

Combined with `track_noise=1`, the filter continues adapting after initial learning—mimicking the DNS's continuous LEARN mode for changing conditions.

If no suitable silence region exists, DNS-1500 stays disabled and Dolby SR activates as fallback.

### Design Philosophy

| DNS Principle | Jivetalking Implementation |
|--------------|---------------------------|
| LEARN from recording | Inline noise sampling via `asendcmd` during detected silence |
| 6-band control | 15-band `afftdn` with measurement-driven profile |
| Conservative attenuation | 6–30 dB reduction calculated from `MeasuredNoiseFloor - TargetNF` |
| Preserve voice character | Adaptivity (0.3–0.7) derived from `InputLRA` |
| Near-zero artifacts | Gain smoothing (0–20) derived from `SpectralFlatness` |

### Why afftdn?

FFmpeg's `afftdn` filter implements adaptive spectral noise reduction with:

- **Inline noise profiling**: `sn=start/stop` via `asendcmd` learns from actual recording
- **Continuous tracking**: `track_noise` adapts to changing conditions after learning
- **15 frequency bands**: More granular than DNS's 6-band interface
- **Adaptivity control**: Smooths gain changes to prevent musical noise
- **Gain smoothing**: Spatial smoothing across frequency bins reduces artefacts

Combined with `asendcmd` for timestamped commands, we achieve true inline learning: the noise profile is captured and applied in a single filter graph pass. The filter chain `HP → LP → afftdn` ensures the sampled noise is spectrally aligned without separate WAV extraction.

This maps cleanly to DNS behaviour. The main advantage: we can be selective about *which* silence region to learn from—Pass 1's spectral analysis identifies the cleanest room tone, rejecting regions contaminated by crosstalk or transients.

### Prerequisites

DNS-1500 requires a valid `NoiseProfile` from Pass 1 silence detection. The profile must include:

| Field | Purpose |
|-------|--------|
| `Start` | Timestamp for `sn=start` command |
| `Duration` | Calculate `sn=stop` timestamp |
| `MeasuredNoiseFloor` | Set `nf` parameter, calculate `nr` |
| `SpectralFlatness` | Determine gain smoothing level |
| `Entropy` | Secondary smoothing decision |

**Spectral alignment** happens automatically: the filter chain `HP → LP → afftdn` processes sequentially, so when `afftdn` samples noise via `sn=start/stop`, it receives HP/LP-filtered audio. No separate WAV extraction needed.

---

## Adaptive Behaviour

All DNS-1500 parameters are derived from Pass 1 measurements—no magic numbers in the tuning logic.

### Parameter Derivation

| Parameter | Source Measurement | Formula/Logic |
|-----------|-------------------|---------------|
| `nr` (noise reduction) | `NoiseProfile.MeasuredNoiseFloor` | `MeasuredNF - TargetNF (-70 dBFS)`, clamped to 6–30 dB |
| `nf` (noise floor) | `NoiseProfile.MeasuredNoiseFloor` | Direct, clamped to -80 to -20 dB |
| `ad` (adaptivity) | `InputLRA` | < 6 LU → 0.3, 6–15 LU → 0.5, > 15 LU → 0.7 |
| `gs` (gain smooth) | `NoiseProfile.SpectralFlatness` | < 0.5 → 0, ≥ 0.5 → 8–20 (scaled) |
| `rf` (residual floor) | `NoiseProfile.MeasuredNoiseFloor` | `MeasuredNF + 12 dB` headroom |

### Noise Reduction Calculation

The reduction amount is the gap between measured noise and target:

```
nr = MeasuredNoiseFloor - TargetNoiseFloor
   = -55 dBFS - (-70 dBFS)
   = 15 dB reduction needed
```

Clamped to `[6, 30]` dB to avoid artefacts on clean material or hollow sound on very noisy material.

### Adaptivity from Dynamic Range

| InputLRA | Adaptivity | Rationale |
|----------|------------|----------|
| < 6 LU | 0.3 (fast) | Uniform material, fast tracking safe |
| 6–15 LU | 0.5 (moderate) | Balanced |
| > 15 LU | 0.7 (slow) | Dynamic material, avoid pumping |

### Gain Smoothing from Noise Character

| SpectralFlatness | GainSmooth | Rationale |
|-----------------|------------|----------|
| < 0.5 | 0 | Tonal noise (hum), precision needed |
| ≥ 0.5 | 8–20 | Broadband noise, smooth to reduce musical artefacts |

High `Entropy` (> 0.7) also triggers smoothing—random noise benefits from spatial averaging.

### Continuous Tracking

`track_noise=1` is always enabled, allowing `afftdn` to adapt as conditions change after initial learning. Combined with the adaptivity setting, this mimics the DNS's continuous LEARN mode.

---

## Configuration

### FilterChainConfig Fields

| Field | Type | Range | Default | Derived From |
|-------|------|-------|---------|-------------|
| `DNS1500Enabled` | bool | — | false | `NoiseProfile != nil` |
| `DNS1500NoiseReduce` | float64 | 6–30 dB | 12 | `MeasuredNF - TargetNF` |
| `DNS1500NoiseFloor` | float64 | −80 to −20 dB | −50 | `NoiseProfile.MeasuredNoiseFloor` |
| `DNS1500Adaptivity` | float64 | 0.3–0.7 | 0.5 | `InputLRA` |
| `DNS1500TrackNoise` | bool | — | true | Always enabled |
| `DNS1500GainSmooth` | int | 0–20 | 0 | `NoiseProfile.SpectralFlatness` |
| `DNS1500ResidFloor` | float64 | −80 to −20 dB | −38 | `MeasuredNF + 12 dB` |
| `DNS1500SilenceStart` | float64 | — | 0.0 | `NoiseProfile.Start` |
| `DNS1500SilenceEnd` | float64 | — | 0.0 | `NoiseProfile.Start + Duration` |

### FFmpeg Filter Specification

Inline noise learning with `asendcmd`:

```
asendcmd=c='[0.5] afftdn@dns1500 sn start; [1.2] afftdn@dns1500 sn stop',
afftdn@dns1500=nr=15:nf=-55:tn=1:ad=0.5:gs=8:rf=-43
```

| Component | Purpose |
|-----------|--------|
| `asendcmd` | Triggers `sn start/stop` at silence timestamps |
| `@dns1500` | Instance name for `asendcmd` targeting |
| `sn start/stop` | Inline noise sampling during silence |
| `tn=1` | Continuous tracking after initial learning |

### Parameter Mapping

| DNS Control | afftdn Parameter | Jivetalking Source |
|------------|-----------------|-------------------|
| Attenuation | `nr` (noise_reduction) | `MeasuredNoiseFloor - TargetNF` |
| LEARN | `sn` (sample_noise) | `asendcmd` at `NoiseProfile.Start` |
| Tracking | `tn` (track_noise) | Always enabled |
| Band control | `bn` (band_noise) | Not used (future: per-band tuning) |
| — | `ad` (adaptivity) | `InputLRA` |
| — | `gs` (gain_smooth) | `NoiseProfile.SpectralFlatness` |
| — | `rf` (residual_floor) | `MeasuredNoiseFloor + 12 dB` |

---

## Pipeline Integration

```
Highpass → Lowpass → [DNS-1500 OR Dolby SR] → Gate → Compressor → ...
```

**Filter order (Pass 2):**

```go
Pass2FilterOrder = []FilterID{
    FilterDownmix,
    FilterDS201HighPass,
    FilterDS201LowPass,
    FilterDNS1500,        // Primary NR (when NoiseProfile exists)
    FilterDolbySR,        // Fallback NR (no NoiseProfile)
    FilterDS201Gate,
    FilterDC1Declick,
    FilterLA2ACompressor,
    FilterDeesser,
    FilterAlimiter,
    ...
}
```

**Conditional selection logic in `AdaptConfig()`:**

```go
tuneDNS1500(cfg, measurements)

if cfg.DNS1500Enabled {
    cfg.DolbySREnabled = false  // DNS-1500 active, disable fallback
} else {
    tuneDolbySR(cfg, measurements)  // No silence, use DolbySR
}
```

**Division of responsibility:**

| Stage | Role |
|-------|------|
| **DNS-1500** | Learned spectral subtraction (requires `NoiseProfile`) |
| **Dolby SR** | Profile-free multiband expansion (fallback) |
| **DS201 Gate** | Inter-phrase silence cleanup |

This conditional approach follows professional practice: use the right tool for the situation. When you have clean room tone to learn from, spectral methods excel. When you don't, expansion-based methods provide safe, artifact-free reduction without needing a reference.

---

## The CEDAR Legacy

CEDAR Audio began in 1983 as a research project at Cambridge University, funded by the British Library National Sound Archive to rescue recordings from decaying wax cylinders and shellac discs. From those academic origins came the company that would define professional audio restoration.

The DNS1000's Academy Award in 2007 was recognition that one product had genuinely moved the industry forward. Before the DNS, noisy location dialogue meant expensive ADR sessions or compromised audio. After the DNS, it meant pressing a button.

The DNS-1500 (2007-2019) carried that legacy through a decade of film and broadcast production. Its successors—the DNS 8D, DNS 2, and DNS One plug-in—continue to evolve, adding machine learning and deep neural networks. But the core philosophy remains: **learn the noise, suppress only what you've learned, preserve the voice**.

Jivetalking's DNS-1500 filter honours that philosophy. We don't guess at noise profiles. We don't apply generic curves. We learn from *your* recording, adapt to *your* conditions, and remove only what needs removing.

The Academy got it right. So should we.

---

*References: [CEDAR Audio Product History](https://cedaraudio.com/products/history/history) • [DNS 8D Owner's Manual](https://manuals.plus/cedar/dns-8d-dialogue-noise-suppressor-manual) • [FFmpeg afftdn Documentation](https://ffmpeg.org/ffmpeg-filters.html#afftdn)*
