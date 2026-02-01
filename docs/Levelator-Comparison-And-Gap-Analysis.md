# Levelator Comparison and Gap Analysis

A detailed technical comparison between The Levelator and Jivetalking, analysing capabilities, gaps, and lessons for podcast audio processing.

---

## 1. Levelator Technical Deep-Dive

### Overview

The Levelator was a landmark audio processing tool created by Bruce and Malcolm Sharpe for The Conversations Network (founded by Doug Kaye). First released in 2006, it became the de facto standard for podcast audio levelling before its eventual discontinuation in 2012 (with brief revival attempts in 2015 and 2020).

### Core Algorithm

The Levelator was explicitly **not** a compressor, normaliser, or limiter—although it contained elements of all three. As stated on the official website:

> "It's software that runs on Windows, OS X (universal binary), or Linux (Ubuntu) that adjusts the audio levels _within_ your podcast or other audio file for variations from one speaker to the next, for example. It's not a compressor, normalizer or limiter although it contains all three. It's much more than those tools, and it's much simpler to use."

### Processing Stages

The Levelator employed a **multi-pass, look-ahead architecture**:

1. **Analysis Pass:** Generated a "loudness map" of the entire file by performing multiple passes over the audio in large and small chunks
2. **Silence Detection:** Identified and excluded silent segments from calculations
3. **Iterative Normalisation:** Applied iterative calculations to avoid peak clipping while increasing loudness
4. **Level Adjustment:** Applied level corrections based on the loudness map

### Technical Specifications

| Parameter | Value |
|-----------|-------|
| **Target RMS Level** | -18.0 dB |
| **Silence Definition** | No subsegments of 50ms or more with RMS > -44.0 dB |
| **Peak Output Level** | -1.0 dB |
| **Acceptable Variance** | ±1 dB |
| **Frequency Weighting** | None (flat RMS measurement) |

### Silence Handling Philosophy

The Levelator's approach to silence was revolutionary and remains instructive:

> "We first isolate segments that are silent and remove them from the calculations. We define silence as audio segments which have no subsegments of 50 ms or more where the RMS is greater than -44.0dB. We then compute the RMS value of the remaining segments and normalize them to our target RMS level of -18.0dB."

This addressed a fundamental problem in spoken-word audio: different applications reported wildly different RMS values for the same file because they handled silence differently. As Doug Kaye noted:

> "If you're only dealing with simple continuous waveforms, calculating and measuring RMS values is easy and standardized. But once you enter the world of spoken-word recordings, it gets a lot more complex... Each application has a different way of excluding segments of silence from the RMS calculation."

### Historical Evolution

| Version | Date | Key Changes |
|---------|------|-------------|
| 0.1 (beta) | 2006 | Initial release |
| 1.1.0 | 2006-2007 | New interface, improved algorithms, all sample rates supported |
| 1.2.1 | 2007 | Rebranding to Conversations Network |
| 1.3.0 | 2007 | Bug fixes, Ubuntu Feisty support |
| 1.4.0 | 2009 | Filename spaces support, very short file processing, accessibility improvements |
| 1.4.1 | 2009 | Task bar integration, "phone home" for updates |
| 2.0.3 | 2010 | Major algorithm improvements, reduced unnatural volume adjustments |
| 2.1.2 | 2015 | El Capitan compatibility update |

### License and Availability Status

**Current Status:** Discontinued

The Levelator was proprietary, closed-source software. The license stated:

> "You may install The Levelator® on a single computer system and use it as-is, in whole and without modification for commercial or non-commercial purposes... You may not copy (other than for backup purposes), share, distribute, sell, reverse engineer, decompile, disassemble, use standalone components of or create a derivative work of The Levelator®."

The software is no longer officially available, though archived copies circulate in podcasting communities. The Conversations Network shut down operations at the end of 2012, though limited revival releases occurred in 2015 (for El Capitan) and 2020 (brief return).

---

## 2. Jivetalking Current Capabilities Summary

### Architecture Overview

Jivetalking is a Go CLI tool for podcast audio preprocessing using embedded FFmpeg. It transforms raw voice recordings into broadcast-ready audio at -18 LUFS through a four-pass adaptive processing pipeline.

### Four-Pass Processing Pipeline

| Pass | Purpose | Description |
|------|---------|-------------|
| **Pass 1** | Analysis | Measures LUFS, true peak, LRA, noise floor, spectral characteristics via ebur128 + astats + aspectralstats |
| **Pass 2** | Processing | Applies adaptive filter chain tuned to Pass 1 measurements |
| **Pass 3** | Measuring | Runs loudnorm in measurement mode to get input stats |
| **Pass 4** | Normalising | Applies loudnorm with linear mode; CBS Volumax-inspired limiter creates headroom |

### Pass 2 Filter Chain

```
downmix → ds201_highpass → ds201_lowpass → noiseremove → ds201_gate → la2a_compressor → deesser → analysis → resample
```

**Filter chain rationale:**
- **Rumble removal** (highpass) before noise reduction
- **Denoising** before gating
- **Compression** before de-essing (compression emphasises sibilance)

### Adaptive Processing Features

| Filter | Adaptive Parameters | Basis |
|--------|---------------------|-------|
| **DS201 Highpass** | Frequency (60-120Hz), poles, mix | Spectral centroid, spectral decrease, noise floor |
| **DS201 Lowpass** | Cutoff frequency, enable/disable | Content type detection (speech/music/mixed), rolloff, ZCR |
| **NoiseRemove** | Compand threshold, expansion depth | Measured noise floor + 5 dB, noise severity |
| **DS201 Gate** | Threshold, ratio, attack, release, range, knee | LRA, noise floor, quiet speech estimate, spectral flux, entropy |
| **LA-2A Compressor** | Threshold, ratio, attack, release, knee, mix | Kurtosis, flux, dynamic range, spectral centroid |
| **De-esser** | Intensity (0.0-0.6) | Spectral centroid + rolloff |

### Target Specifications

| Parameter | Target |
|-----------|--------|
| **Target Loudness** | -18 LUFS (EBU R128) |
| **True Peak Ceiling** | -1.0 to -2.0 dBTP |
| **Output Format** | 44.1kHz, 16-bit, mono |
| **Loudness Range** | Up to 20 LU (prevents dynamic mode fallback) |

### Speech-Aware Processing

Jivetalking employs speech profile extraction for adaptive tuning:

- **Silence detection:** Uses 250ms interval sampling with spectral analysis
- **Speech region detection:** Finds representative speech segments (30s+ duration)
- **Golden sub-region refinement:** Identifies cleanest sub-windows for noise/speech profiling
- **Speech metrics:** RMS level, crest factor, spectral centroid, kurtosis, flux for each profile

### User Interface

- CLI with Kong flags
- Bubbletea TUI for progress display
- Drag-and-drop not supported (command-line focused)

---

## 3. Detailed Comparison Matrix

| Dimension | Levelator | Jivetalking |
|-----------|-----------|-------------|
| **Processing Approach** | Multi-pass file-based batch processing | Multi-pass file-based batch processing |
| **Processing Paradigm** | "Leveling"—medium-term variation correction | Full pipeline: denoise → gate → compress → de-ess → normalise |
| **Look-ahead Capability** | Yes—infinite look-ahead via multiple passes | Yes—infinite look-ahead via Pass 1 analysis |
| **Target Loudness Standard** | -18 dB RMS (custom RMS calculation) | -18 LUFS (EBU R128 standard) |
| **Silence Detection** | Fixed: 50ms subsegments > -44 dB | Adaptive: spectral analysis + room tone scoring |
| **Noise Reduction** | None | anlmdn (Non-Local Means) + compand |
| **Dynamics Processing** | Implicit in leveling algorithm | Explicit LA-2A-style compressor + CBS Volumax limiter |
| **Gating/Expansion** | None | DS201-inspired soft expander (2:1-4:1 ratio) |
| **Highpass Filtering** | None | Adaptive 60-120Hz with warm voice protection |
| **Lowpass Filtering** | None | Adaptive 16kHz+ with content-aware disabling |
| **De-essing** | None | Adaptive intensity 0.0-0.6 based on spectral analysis |
| **Content Type Detection** | None | Speech vs Music vs Mixed classification |
| **Adaptive Parameters** | Minimal (one-size-fits-all) | Extensive per-filter tuning based on 20+ metrics |
| **Parameter Control** | None—fully automatic | None—fully automatic (but tunable via source) |
| **Output Formats** | WAV, AIFF | Any FFmpeg-supported format (default: FLAC → FLAC) |
| **Output Sample Rate** | Matches input | 44.1kHz standardised |
| **Output Bit Depth** | Matches input | 16-bit standardised |
| **Output Channels** | Matches input | Mono (downmix enabled by default) |
| **User Interface** | GUI (drag-and-drop) | CLI/TUI (command-line) |
| **Platform Support** | Windows, macOS, Linux (32-bit) | Linux, macOS, Windows (64-bit, via Go/FFmpeg) |
| **Open Source** | No—proprietary, discontinued | Yes—open source, actively maintained |
| **License** | Proprietary (free for use) | Open source (license not specified in research) |
| **Commercial Use** | Initially non-commercial only, later permitted | Permitted |
| **Breath Reduction** | None | Yes—via adaptive gate threshold positioning |
| **Plosive Reduction** | None | Implicit via AutoEQ design patterns |
| **True Peak Limiting** | None (-1.0 dB sample peak) | Yes (-1.0 to -2.0 dBTP via alimiter) |
| **Loudness Range Control** | None | Monitored and adapted to (LRA affects gate ratio) |
| **Processing Speed** | Not specified | ~20-30x realtime (typical FFmpeg performance) |
| **Video Support** | No | No |
| **Multitrack Support** | No | No (single file only) |

---

## 4. Gap Analysis

### Capabilities Levelator Had That Jivetalking Lacks

#### 1. **Simplicity of User Experience**

**Gap:** Levelator's drag-and-drop GUI was remarkably simple. Users dropped a file, and seconds later received a processed file with "-leveled" appended to the filename.

**Jivetalking Status:** Requires command-line usage: `jivetalking file.flac`

**Impact:** Higher barrier to entry for non-technical users.

#### 2. **Platform-Native GUI**

**Gap:** Levelator provided native GUI applications for each platform with proper file associations, Dock icons, task bar integration.

**Jivetalking Status:** Terminal-based TUI only.

**Impact:** Less approachable for users unfamiliar with command-line tools.

#### 3. **Medium-Term "Leveling" Approach**

**Gap:** Levelator's unique contribution was correcting "medium-term" loudness variations—neither the short-term transients handled by compressors nor the long-term overall loudness handled by normalisers. This "riding the fader" effect compensated for speakers at different volumes within a recording.

**Jivetalking Status:** Jivetalking applies consistent filter parameters across the entire file. While the LA-2A compressor provides some program-dependent gain variation, it does not explicitly segment the file and apply different corrections to different time regions.

**Quote from Levelator docs:**
> "Software can do better by performing multiple passes over the audio, generating a loudness map of where the volume changes."

**Impact:** Levelator may handle highly variable speaker levels (e.g., panel discussions, Q&A sessions) more transparently than Jivetalking's uniform processing.

#### 4. **Standardised RMS-Based Output**

**Gap:** Levelator targeted -18 dB RMS using a consistent, documented silence-exclusion method.

**Jivetalking Status:** Targets -18 LUFS (EBU R128), which is a different measurement standard. While LUFS correlates with perceived loudness better than RMS, some users may expect RMS-normalised output.

**Impact:** Output levels may differ from user expectations if migrating from Levelator.

#### 5. **Cross-Platform Consistency (Historical)**

**Gap:** Levelator provided 32-bit binaries for all platforms from a single codebase (Python/wxPython for Linux, native for Windows/macOS).

**Jivetalking Status:** Requires Go toolchain and FFmpeg libraries; Nix environment for development.

**Impact:** End users must trust the build process or obtain pre-built binaries.

### Capabilities Jivetalking Has That Levelator Lacked

1. **Noise Reduction:** Non-Local Means denoising with adaptive compand
2. **Gating:** Soft expander for inter-speech cleanup
3. **True Peak Limiting:** Prevents inter-sample peaks
4. **De-essing:** Automatic sibilance control
5. **Content Detection:** Distinguishes speech from music
6. **Spectral Analysis:** 15+ spectral metrics for adaptive tuning
7. **Speech Profiling:** Golden sub-region extraction for representative metrics
8. **Standardised Output:** Consistent 44.1kHz/16-bit/mono
9. **Open Source:** Fully auditable and extensible

---

## 5. Recommendations

### High Priority

#### 1. **Segment-Based Adaptive Processing (The "Levelator Feature")**

**Recommendation:** Implement time-segmented processing that can apply different filter intensities to different portions of the audio based on local loudness characteristics.

**Rationale:** This was Levelator's unique contribution. While Jivetalking's adaptive filters tune to global file characteristics, they don't vary over time within the file.

**Technical Approach:**
- Divide file into segments (e.g., 5-10 seconds)
- Calculate local loudness for each segment
- Apply varying compression/normalisation per segment
- Use crossfade windows to avoid audible transitions

**Feasibility:** Medium—requires significant architectural changes but builds on existing Pass 1 analysis infrastructure.

**Status:** **Should implement**—this is the core "magic" of Levelator that users miss.

#### 2. **Drag-and-Drop GUI Wrapper**

**Recommendation:** Create a simple GUI wrapper (e.g., using Fyne, Wails, or native file watchers) that allows drag-and-drop processing.

**Rationale:** Levelator's accessibility was key to its adoption. Many podcasters are not comfortable with command-line tools.

**Technical Approach:**
- File watcher/drop target monitors for audio files
- Automatically runs `jivetalking` on dropped files
- Shows simple progress dialog
- Opens output folder on completion

**Feasibility:** High—wrapper around existing CLI functionality.

**Status:** **Should implement**—significant usability improvement with minimal core changes.

### Medium Priority

#### 3. **LUFS-to-RMS Output Option**

**Recommendation:** Provide optional RMS-based normalisation target for users migrating from Levelator.

**Rationale:** Some users may have established workflows expecting -18 dB RMS output.

**Technical Approach:**
- Add `--target-rms` flag
- Use FFmpeg's `volumedetect` filter for RMS measurement
- Calculate gain adjustment to reach target RMS

**Feasibility:** High—FFmpeg supports this natively.

**Status:** **Maybe implement**—nice to have for migration compatibility.

#### 4. **Output File Naming Convention**

**Recommendation:** Append "-leveled" or similar suffix to output files (configurable).

**Rationale:** Levelator's naming convention (`filename-leveled.wav`) was clear and predictable.

**Feasibility:** Trivial.

**Status:** **Should implement**—one-line change, improves usability.

### Low Priority

#### 5. **Breath Reduction Enhancement**

**Recommendation:** Review Levelator's approach to breath handling and consider implementing dedicated breath detection/reduction.

**Rationale:** Levelator was praised for not introducing "breathing" artifacts. Jivetalking has breath reduction via adaptive gating, but it may not be as refined.

**Feasibility:** Medium—requires research into breath detection algorithms.

**Status:** **Maybe implement**—current solution may be sufficient.

#### 6. **Multi-Format Batch Processing**

**Recommendation:** Support processing multiple files with different input formats simultaneously.

**Rationale:** Levelator could handle multiple files sequentially.

**Feasibility:** Already supported via `jivetalking file1.wav file2.flac file3.mp3`.

**Status:** **Already implemented**—verify documentation covers this.

---

## 6. Historical Context & Lessons

### Why Levelator Was Popular

1. **It Solved a Real Problem:** Before Levelator, podcasters had to manually adjust levels or use complex DAW workflows. Levelator provided "magic" in seconds.

2. **It Was Free:** In an era when audio processing tools cost hundreds of pounds, Levelator was free (initially non-commercial, later unrestricted).

3. **It Was Simple:** One file in, one file out. No parameters, no settings, no learning curve.

4. **It Worked:** Results were consistently good for the target use case (spoken word, varying levels).

5. **It Had Evangelists:** Doug Kaye, podcasting communities, and tech blogs spread the word.

### Why It Was Discontinued

1. **Organisational Shutdown:** The Conversations Network ceased operations at the end of 2012. Doug Kaye declared "mission accomplished" and shut down the non-profit.

2. **Closed Source:** The proprietary codebase could not be easily transferred or open-sourced.

3. **Platform Evolution:** 32-bit macOS binaries stopped working on Catalina (2019), cutting off many users.

4. **No Revenue Model:** Free software without a sustainable funding model depends entirely on volunteer maintenance.

5. **Dependency Issues:** Reliance on libsndfile for certain formats caused ongoing compatibility issues.

### What the Community Moved To

| Era | Primary Alternatives | Notes |
|-----|----------------------|-------|
| 2012-2015 | Audacity compressor, manual processing | Loss of convenience |
| 2015-2019 | Auphonic (web and desktop), Levelator 2.1.2 (brief revival) | Auphonic became the standard; paid service |
| 2019-present | Auphonic, iZotope RX, manual workflows | Catalina killed 32-bit Levelator |
| 2020 | Levelator briefly returned | Limited revival; status uncertain |

### Lessons for Jivetalking's Design

#### 1. **Simplicity Trumps Features**

Levelator succeeded because it did one thing well. Jivetalking's extensive filter chain provides better technical results but increases complexity.

**Lesson:** Maintain the "it just works" philosophy. Avoid exposing internal parameters unless absolutely necessary.

#### 2. **Open Source Is Insurance**

Levelator's closure left users stranded. Jivetalking's open-source nature prevents this.

**Lesson:** Ensure the project has multiple maintainers, clear build instructions, and minimal external dependencies that could disappear.

#### 3. **Platform Compatibility Matters**

Levelator's 32-bit nature ultimately killed it on macOS.

**Lesson:** Build for 64-bit only, use standard libraries (FFmpeg), and provide clear platform support documentation.

#### 4. **Documentation Builds Trust**

Levelator's "Loudness Algorithms" page was widely cited. Being transparent about how the tool works builds user confidence.

**Lesson:** Continue documenting the processing pipeline (as AGENTS.md does) and consider publishing an "Algorithms" document.

#### 5. **The "Magic" Is Medium-Term Leveling**

Levelator's unique contribution was not compression or normalisation—it was the "medium-term" correction that handled speakers at different volumes.

**Lesson:** Prioritise implementing segmented/time-varying processing. This is the key missing feature for Levelator refugees.

#### 6. **Target Standards, Not Arbitrary Values**

Levelator created its own RMS standard because no spoken-word standard existed. Today, EBU R128 (-18 LUFS) is widely accepted.

**Lesson:** Jivetalking's LUFS targeting is the correct modern approach. Resist pressure to support arbitrary target levels unless there's a compelling use case.

---

## References

### Primary Sources

1. The Levelator Loudness Algorithms (Archived): https://web.archive.org/web/20100316143248/http://www.conversationsnetwork.org/levelatorAlgorithm
2. The Levelator Product Page (Archived): https://web.archive.org/web/20091208170713id_/http://www.conversationsnetwork.org/%20levelator
3. Levelator Change History (Archived): https://web.archive.org/web/20091208055754/http://www.conversationsnetwork.org/levelator-change-history
4. Levelator License (Archived): https://web.archive.org/web/20100108023611/http://www.conversationsnetwork.org/levelator-license

### Secondary Sources

5. Doug Kaye on Levelator Loudness Algorithms: https://blogarithms.com/2009/03/11/the-levelator-loudness-algorithms/
6. Auphonic Singletrack Algorithms: https://auphonic.com/help/algorithms/singletrack.html
7. Levelator vs Auphonic Comparison (GoTranscript): https://gotranscript.com/public/comparing-levelator-and-auphonic-best-tools-for-podcast-audio-leveling
8. Alternatives to Levelator: https://podcastinghacks.com/alternatives-to-levelator/
9. Wikipedia—Levelator: https://en.wikipedia.org/wiki/Levelator
10. The Levelator's Return: https://www.podfeet.com/blog/2020/06/the-levelator/

### Technical Standards

11. EBU R128 (2023): Loudness Normalisation and Permitted Maximum Level of Audio Signals
12. ITU-R BS.1770: Algorithms to Measure Audio Programme Loudness and True-Peak Audio Level

---

*Report compiled: February 2026*
*Jivetalking version analyzed: Development branch (AGENTS.md dated prior to February 2026)*
