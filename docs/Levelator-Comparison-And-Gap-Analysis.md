# Levelator Comparison and Gap Analysis

A from-scratch technical comparison between The Levelator and the current build of Jivetalking. It re-researches Levelator from primary sources, re-states Jivetalking's current pipeline from the code, and judges, fairly, where each tool leads. This is a full rewrite of the original comparison, re-researched from primary sources in June 2026.

Central question for this revision: does Jivetalking's current design (the unified VAD, the four-pass pre-calculated normaliser, and true-peak levelling) close the gap on Levelator's distinctive "medium-term levelling"? The short answer is below in Section 4.3.

---

## 1. Levelator Technical Deep-Dive

### Overview

The Levelator was created by Bruce and Malcolm Sharpe for The Conversations Network (founded by Doug Kaye). It launched in beta in September 2006 and became the default podcast levelling tool of its era [1][6]. It describes itself this way [3]:

> "It's not a compressor, normalizer or limiter although it contains all three. It's much more than those tools, and it's much simpler to use."

### Core algorithm

The Levelator is an offline, whole-file, multi-pass processor with infinite look-ahead [2]:

> "The Levelator processes an entire audio file, not a continuous stream, so we have the advantage of infinite lookahead and the ability to make multiple passes over the data in large and small chunks."

Its loudness target is **-18.0 dB RMS**, measured with **no frequency weighting** [2]:

> "The Conversations Network standard for loudness is based on the RMS measurements described above with no frequency weighting. We (and others) have experimented with several weighting schemes, but we found them to be no more effective."

This predates and rejects the K-weighting later standardised in ITU-R BS.1770 [11]. Levelator's RMS is plain unweighted energy in dBFS, not LUFS.

### Processing stages

1. **Silence exclusion.** Silent segments are removed before the RMS is computed [2].
2. **Loudness map.** Multiple passes over the file, in large and small chunks, build a map of where the level changes [2][3].
3. **Iterative, peak-safe normalisation.** Loudness is raised toward the target in iterative steps that back off to avoid peak clipping [2].
4. **Level adjustment.** Medium-term level corrections are applied from the map.

### Technical specifications

| Parameter | Value | Source |
|-----------|-------|--------|
| Target level | -18.0 dB RMS (unweighted) | [2] |
| Frequency weighting | None (flat RMS) | [2] |
| Silence definition | No subsegment of 50 ms or more with RMS above -44.0 dB | [2] |
| Silence threshold behaviour | Applied iteratively, after a pre-normalisation toward the target | [2] |
| Acceptable variance | Plus or minus 1 dB on final RMS | [2] |
| Peak output level | -1.0 dB sample peak (not true peak) | [2][15] |
| Input formats | PCM only (WAV, AIFF); no video, no lossy | [5] |
| Output | Same format as input, with `.output` added to the name | [5][15] |

### Silence handling philosophy

The silence rule exists because pauses dominate spoken-word duration and corrupt a naive RMS reading [2]:

> "We define silence as audio segments which have no subsegments of 50 ms or more where the RMS is greater than -44.0dB. We then compute the RMS value of the remaining segments and normalize them to our target RMS level of -18.0dB."

Doug Kaye framed it as the root of cross-tool disagreement [5]:

> "Each application has a different way of excluding segments of silence from the RMS calculation... Is one recording half as loud as another because the speaker in the first one pauses twice as long between words?"

One detail the older comparison missed: the -44.0 dB threshold is **adaptive**, not a fixed absolute gate. It is applied only after the file is first pre-normalised toward the target, because "-44.0dB is not reasonable if the audio before normalization is already very quiet... This requires an iterative calculation" [2].

### The distinctive feature: medium-term levelling

This is Levelator's reason to exist. It sits between the two tools that already existed [3]:

> "[A normaliser] affects primarily the short-term (transient) sounds and the long-term overall loudness of the file. It doesn't make the kind of adjustments that a skilled audio engineer can perform... riding the levels up and down to compensate for medium-term variations."

A compressor handles the short term, a normaliser handles the long-term whole-file loudness, and the gap between them, the medium term, is what a human fixes by riding the fader. The Levelator automates that by building the loudness map offline, which beats both real-time AGC and a human reacting live [3]:

> "Software can do better by performing multiple passes over the audio, generating a loudness map of where the volume changes."

The use case is speaker-to-speaker and section-to-section level variation: panel discussions with mixed mic distances, or sentences recorded months apart and assembled into one piece [2][3].

⚠️ CAVEAT: the **segment size, crossfade behaviour, and anti-pumping method of the medium-term correction are not published**. The Levelator is closed source, and the official page calls its public account "a drastic simplification" [2]. Version 2.0.3 (January 2010) named "a reduction in certain unnatural volume adjustments" [8], the only public hint that artefacts were tuned out, with no mechanism given.

### History, licence, and current status

📌 KEY CORRECTION to the earlier document: **The Levelator is not discontinued.** It is maintained on the Mac App Store.

| Era | State | Source |
|-----|-------|--------|
| 2006 to 2010 | 0.1 beta through 2.1.1; cross-platform (Windows, macOS, Ubuntu) | [4][6][8] |
| 2012 | The Conversations Network shut down ("Mission Accomplished", Doug Kaye); assets to the Internet Archive | [13][18] |
| 2015 | 2.1.2, macOS only, fixes El Capitan; 32-bit | [9] |
| 2019 | 32-bit 2.1.2 dies on macOS Catalina | [19][20] |
| 2020 | 3.0.2, 64-bit, returns on the Mac App Store | [11][8] |
| 2021 to date | **3.0.3 on the Mac App Store, free, requires macOS 12 or later, universal binary (Apple Silicon)** | [1] |

The licence remains proprietary freeware: single computer, no modification, no redistribution, no reverse engineering, no derivative works [12]. It bundles libsndfile under the LGPL [12].

⚠️ CAVEAT on platforms: the living product is **macOS only**. Windows is frozen at 2.1.1 (a 2010 build that still runs on Windows 11 per a user report) and Linux is frozen at the Ubuntu 7.10 x86 package [3-hist]. There is no maintained Levelator for Linux or Windows, and no source has been reopened.

### Feature limits

The Levelator is levelling-only. It has **no noise reduction, no de-essing, no gating or expansion, no high-pass or low-pass filtering, no true-peak limiting, and no speech-versus-music classification** [5][9-recv][10-recv]. A user guide states it plainly: "If your audio has buzz or hum, or popping or crackling, Levelator will [...] not remove that" [10-recv]. Modern successors (Auphonic, iZotope RX) add exactly those missing stages [9-recv][11-recv].

---

## 2. Jivetalking Current Capabilities

This section describes the current branch, not a historical release. Code citations are `file:line`.

### Architecture

Jivetalking is a Go CLI tool using embedded FFmpeg. It transforms raw voice recordings into podcast-ready audio at -16 LUFS through a **four-pass, pre-calculated** pipeline.

| Pass | Purpose | Where |
|------|---------|-------|
| 1 Analysis | Measures LUFS, true peak, LRA, noise floor, spectral metrics, and runs the unified VAD | `processor.go:41`, `analyser.go` |
| 2 Processing | Applies the adaptive filter chain tuned to Pass 1 | `processor.go:137`, `filters.go:58` |
| 3 Measuring | Runs loudnorm in measure-only mode to capture input stats (JSON to a stats file) | `normalise.go:226` |
| 4 Normalising | Applies the pre-computed gains and the limiters | `normalise.go:912` |

📌 KEY: like Levelator, Jivetalking has infinite look-ahead. It measures the whole file before it renders, and the gains are fixed before audio flows. Pass 3 measures, Pass 4 applies (`normalise.go:257`, `normalise.go:912`).

### The unified voice-activity detector (VAD)

Pass 1 runs one detector that produces every speech and noise output the later filters consume (`analyser_vad.go`).

- **One axis.** All level decisions sit on the K-weighted momentary-LUFS scale (`analyser_vad.go:51`), chosen because momentary loudness is steadier across a brief breath than 250 ms RMS and is the BS.1770 foreground signal.
- **One split.** A per-250 ms level histogram (1 dB bins) is split with Otsu's method, clamped between the noise floor plus 2 dB and the 75th percentile (`analyser_vad.go:259`, `analyser_vad.go:332`).
- **Noise floor.** A low percentile (p10) of the non-silent level set, anchored to a pre-scan seed (`analyser_vad.go:311`, `analyser_vad.go:320`).
- **Speech election.** Hysteresis-built runs with adaptive gap tolerance (2 s to 10 s) and a spectral veto (vocal-band centroid 200 Hz to 6 kHz, structured-spectrum entropy below 0.70). The elected speech region is the highest-scoring run of at least 10 s, scored SNR-primary (`analyser_candidates_speech.go:216`).
- **Gate statistics.** Voiced p10, noise p95, and their difference (GateSeparationDB) come from the same split (`analyser_vad.go:220`).

**Voice-activated detection.** Platform-gated captures (Riverside, Zencastr) crush the gaps between phrases to digital silence. Jivetalking flags these when the floored fraction (windows at or below -115 dBFS) is at least 0.20 (`analyser_vad.go:698`, `analyser_vad.go:779`). A recent fix counts non-finite (NaN) momentary windows as floored, because FFmpeg's ebur128 reports digital silence as NaN on macOS arm64 and as -inf or finite-low on Linux; counting non-finite as floored makes the detection give the same answer on both platforms (`analyser_vad.go:708`). When voice-activated, the FFT denoiser is dropped to avoid warble on true silence.

### Noise reduction, gate, compressor, de-esser

Pass 2 chain order (`filters.go:58`, AGENTS.md):

```
downmix -> rumble highpass -> bandlimit lowpass -> noise reduction -> speech gate -> levelling compressor -> de-esser -> analysis -> resample
```

| Filter | Adapts | Basis |
|--------|--------|-------|
| Rumble high-pass | No (fixed 80 Hz, 12 dB/oct) | Below every vocal fundamental |
| Band-limit low-pass | No (fixed 20.5 kHz) | Consistent encoder bandwidth |
| Noise reduction | `afftdn` enable, floor `nf`, profile `nt` | `anlmdn` fixed; `afftdn` dropped on voice-activated, else `nf` pinned to the measured floor, and given a measured 15-band noise profile on a trustworthy room tone (`adaptive.go`) |
| Speech gate | Threshold, ratio, depth | Threshold = voiced p10 minus 6 dB; ratio 1.5 to 2.0 from LRA; depth 14 dB, cut to 8 dB on a narrow gap (`adaptive_speech_gate.go`) |
| Levelling compressor | Threshold only | `max(SpeechProfile.RMSLevel, Dynamics.RMSLevel) + 9 dB` (`adaptive_levelling_compressor.go:91`) |
| De-esser | Intensity `i` (0.0 to 0.85) | Speech-region sibilant-band excess, 6 to 9 kHz vs 1 to 3 kHz |

### Normalisation and peak levelling (Pass 3/4)

Pass 4 order (`normalise.go:1205`):

```
volume (pre-gain, when needed) -> alimiter (levelling limiter) -> loudnorm (linear) -> aresample -> adeclick -> alimiter (brickwall) -> astats -> aspectralstats -> ebur128
```

- **loudnorm linear mode** applies one global scalar gain to reach -16 LUFS, using the Pass 3 measured stats (`normalise.go:1269`). Linear mode is the transparent choice: it preserves dynamics and avoids the pumping of loudnorm's dynamic mode. A per-file internal true-peak target is derived for each file so every file reaches full -16 LUFS (`normalise.go:563`).
- **Pre-gain** (`volume`) lifts very quiet recordings so the limiter ceiling stays viable; the ceiling is derived as `targetTP - gainRequired` (`normalise.go:407`).
- **Two limiters, two jobs.** The prefix `alimiter` (the "levelling limiter") reduces peaks to create headroom so loudnorm can apply its full linear gain (`normalise.go:446`). The final-stage `alimiter` is a brickwall pinned below the loudnorm target by an inter-sample headroom margin (corpus-derived 0.9 dB), and it owns true-peak delivery (`normalise.go:474`). The brickwall limits sample peak with enough margin to keep the oversampled true peak under target.

📌 KEY: this is the "peak levelling" the older document lacked. Levelator stops at a -1.0 dB sample-peak ceiling [2]; Jivetalking delivers a true-peak-aware -1 dBTP using an oversampled internal limiter plus the brickwall margin.

### Target output

| Parameter | Target |
|-----------|--------|
| Loudness | -16 LUFS (K-weighted, EBU R128 / BS.1770) |
| True peak | -1 dBTP |
| Format | 44.1 kHz, 16-bit, mono, FLAC by default |

---

## 3. Comparison Matrix

| Dimension | Levelator | Jivetalking |
|-----------|-----------|-------------|
| Processing model | Offline, multi-pass, whole-file | Offline, four-pass, whole-file |
| Look-ahead | Infinite (multi-pass) [2] | Infinite (Pass 1 + Pass 3 pre-calc) |
| Loudness target | -18.0 dB RMS, unweighted [2] | -16 LUFS, K-weighted |
| Silence/speech detection | Fixed 50 ms / -44 dB, iterative [2] | Adaptive unified VAD (Otsu split, spectral veto, per-file) |
| Medium-term levelling | Yes: time-windowed loudness map [3] | Partial: 200 ms-release RMS compressor, single global normalisation gain (see 4.3) |
| Noise reduction | None [10-recv] | `anlmdn` + adaptive `afftdn` with measured profile |
| Gating / expansion | None | Soft expander, voiced-anchored threshold |
| High-pass / low-pass | None | Fixed 80 Hz HP, 20.5 kHz LP |
| De-essing | None | Adaptive intensity from sibilant-band excess |
| True-peak limiting | None; -1.0 dB sample peak [2][15] | Yes; -1 dBTP, oversampled + brickwall |
| Content classification | None | Speech vs silence/noise only; no music class |
| Output format | Same as input (PCM/WAV/AIFF) [5] | Standardised 44.1 kHz / 16-bit / mono |
| Output naming | `<name>.output.<ext>` [5][15] | `<name>-LUFS-NN-processed.<ext>` |
| Interface | Drag-and-drop GUI | CLI / TUI |
| Platforms (maintained) | macOS only (App Store 3.0.3) [1] | Linux, macOS (64-bit); no Windows build |
| Platforms (frozen/dead) | Windows 2.1.1, Linux Ubuntu 7.10 [3-hist] | None |
| Open source | No (proprietary) [12] | Yes |
| Cost | Free [1] | Free |
| Speed | Whole-file, seconds | ~16x to 30x realtime (FFmpeg) |

---

## 4. Gap Analysis

### 4.1 What Jivetalking has that Levelator lacks

1. **Noise reduction.** Two-stage `anlmdn` plus adaptive `afftdn`, with a measured per-file 15-band noise profile on a clean room tone. Levelator has none [10-recv].
2. **True-peak delivery.** Oversampled internal limiter plus a brickwall pinned with inter-sample headroom. Levelator caps sample peak only [2].
3. **Adaptive, per-file silence and speech detection.** A unified VAD that elects the speech region and the room tone per file, versus one fixed -44 dB threshold.
4. **Gating, de-essing, rumble and band-limit filtering.** None of these exist in Levelator.
5. **Loudness standard.** -16 LUFS K-weighted, aligned with podcast and streaming norms, versus Levelator's unweighted -18 dB RMS.
6. **Spectral analysis and speech profiling.** 15-plus metrics drive per-file tuning.
7. **Maintained on Linux.** Levelator's Linux build is frozen at Ubuntu 7.10 and effectively dead [3-hist]. Jivetalking has no Windows build, so neither tool serves Windows with a current release.
8. **Open source.** Auditable and extensible; Levelator is closed [12].

### 4.2 What Levelator has that Jivetalking lacks

1. **A drag-and-drop GUI.** Levelator's accessibility drove its adoption: drop a file, get `<name>.output.wav` seconds later [15][16-recv]. Jivetalking is CLI/TUI.
2. **Time-windowed medium-term levelling (the loudness map).** See 4.3. This is the one genuine algorithmic capability Jivetalking does not fully replicate.
3. **Format-preserving output.** Levelator returns the input's format, rate, and channel count [5]. Jivetalking standardises to 44.1 kHz / 16-bit / mono by design, which is a deliberate choice, not a gap, but it differs from Levelator's behaviour.

### 4.3 Does Jivetalking close the medium-term levelling gap?

This is the crux of the revision. The honest verdict: **the gap is much narrower than before, and partly closed on the mechanism itself, but not fully equivalent.**

**What now closes part of the gap:**

- **The levelling compressor is a real medium-term control.** It is FFmpeg `acompressor` in RMS mode, ratio 3.0, attack 10 ms, **release 200 ms**, soft knee 4.0 (`adaptive_levelling_compressor.go:39`). The 200 ms release is the classic medium-term constant: it holds gain reduction across intra-phrase dips and evens the loud-to-quiet swing of a delivery, rather than chasing transients.
- **Its threshold is programme-dependent.** It is pinned to the elected speech RMS plus 9 dB, not to a peak or a silence-diluted full-file average (`adaptive_levelling_compressor.go:91`). A louder passage crosses the threshold more and is pulled down more. For content above the threshold, this reduces both intra-speaker and inter-speaker level differences, bounded by the 3:1 ratio.
- **Pre-calculated, look-ahead design.** Like Levelator, the gains are computed from a full-file measurement before the render, not reacted to live.

**What keeps part of the gap open:**

- **No time-windowed loudness map.** Jivetalking measures one integrated loudness for the file and loudnorm linear mode applies **one global gain** (`normalise.go:873`, `normalise.go:1269`). It does not allocate different gains to different time regions. Levelator's signature is exactly that per-region gain (riding the fader) [3].
- **Slow inter-speaker drift survives.** If speaker A sits 6 dB hotter than speaker B across a conversation, the single global gain moves both together, so the difference persists. The compressor smooths it for the parts above threshold, but does not level it out region by region. The 200 ms release tracks phrase-level dynamics, not the sentence-to-section drift the loudness map targets.
- **The compressor is a reactive filter, not a map.** Its threshold and time constants are fixed once per file. It approximates medium-term levelling; it does not reproduce a planned per-region gain curve.

**Net:** in overall output quality and feature coverage Jivetalking now clearly leads, including the true-peak levelling and the pre-calculated normaliser. On the specific medium-term levelling magic, Jivetalking covers the faster half (phrase-level dynamics, via the 200 ms compressor with a speech-relative threshold) and leaves the slower half (sentence-to-section inter-speaker gain riding) to a single global gain. The gap is real but small, and it is the last structural difference rather than a quality deficit.

---

## 5. Recommendations

### High priority

1. **Optional time-segmented gain riding (the true Levelator feature).** Build a per-region loudness map from the Pass 1 interval data (already measured at 250 ms) and apply a slow, bounded gain envelope before the global normalise, with limits and smoothing to avoid pumping. This is the one capability that would fully close 4.3. Feasibility: medium; the measurement spine already exists. Status: the only remaining algorithmic gap, worth a design spike.

2. **Drag-and-drop GUI wrapper.** A thin wrapper (file watcher or small native app) that runs `jivetalking` on dropped files would match Levelator's accessibility, which was the real driver of its adoption. Feasibility: high; no core change.

### Medium priority

3. **Document the pipeline as an "Algorithms" page.** Levelator's algorithm page is still cited 18 years on. Jivetalking has the material in AGENTS.md, Pipeline.md, and Normalisation-Tuning.md; a public-facing version would build the same trust.

4. **Position against the real Levelator status.** Levelator survives only on macOS, and its Linux build is dead. Jivetalking's maintained Linux build is a concrete advantage to state plainly for Levelator refugees on Linux. Neither tool has a current Windows release.

### Low priority

5. **Reconsider format-preserving output as an option.** A flag to keep input rate and channels would ease migration for users who expect Levelator's behaviour, without changing the -16 LUFS default.

---

## 6. Historical Context and Lessons

### Why Levelator mattered

It solved a real problem (uneven spoken-word levels), it was free, and it was simple: one file in, one file out, no settings [3][16-recv]. Reviewers called it "magic" [16-recv]. In a direct A/B against Auphonic, levelling quality was close, with Auphonic ahead only on the extras Levelator never had, such as noise reduction [9-recv].

### What actually happened to it

Not a clean death. The Conversations Network shut down in 2012 [13], the 32-bit macOS build died on Catalina in 2019 [19], and a 64-bit Mac App Store version revived it in 2020 and continues as 3.0.3 today [1]. Windows and Linux were left behind at 2010-era builds [3-hist]. The lesson stands but sharpened: closed source plus single-platform maintenance stranded most of the user base, even though the macOS line survived.

### Lessons for Jivetalking

1. **Simplicity drove adoption.** Keep the "it just works" default. The GUI wrapper (Recommendation 2) is the highest-leverage usability move.
2. **Open source is the insurance Levelator never had.** Its Linux and Windows users had no recourse. Jivetalking's open licence and maintained Linux and macOS builds remove that risk for those platforms.
3. **The medium term is still the prize.** Levelator's one irreplaceable trick was time-windowed levelling. Jivetalking now approximates it well with a programme-relative compressor; a real loudness-map mode would finish the job.
4. **Target a real standard.** Jivetalking's -16 LUFS is the correct modern choice over an unweighted in-house RMS target.

---

## Sources

[1] The Levelator on the Mac App Store: https://apps.apple.com/us/app/the-levelator/id1493326487 - current listing, v3.0.3, free, macOS 12+, universal binary.
[2] The Levelator Loudness Algorithms (archived): https://web.archive.org/web/20130729204708id_/http://www.conversationsnetwork.org/levelatorAlgorithm - primary algorithm page: -18 dB RMS, no weighting, 50 ms / -44 dB silence, iterative peak-safe gain, multi-pass, infinite look-ahead, -1.0 dB peak, plus or minus 1 dB tolerance.
[3] The Levelator product page (archived): http://web.archive.org/web/20201212171812id_/http://www.conversationsnetwork.org/levelatorAlgorithm - "not a compressor, normalizer or limiter"; short/medium/long-term framing; loudness map; riding the fader.
[4] Levelator change history (archived): http://web.archive.org/web/20200717102511/http://conversationsnetwork.org:80/levelator-change-history - version chain 0.1 beta to 2.1.2.
[5] Levelator, Wikipedia: https://en.wikipedia.org/wiki/Levelator - PCM/WAV/AIFF only, same-format output, `.output` suffix, compression plus normalisation plus limiting over long and short segments.
[6] The Levelator launch, Doug Kaye (2006): https://blogarithms.com/2006/09/28/the-levelator/ - Sept 2006 beta; built by Bruce and Malcolm Sharpe.
[8] The Levelator 2.0, Doug Kaye (2010): https://blogarithms.com/2010/01/07/levelator-2-0/ - dates 2.0.3; "reduction in certain unnatural volume adjustments" via the changelog.
[9] The Levelator 2.1.2 works in El Capitan, TidBITS (2015): https://tidbits.com/2015/12/01/the-levelator-2-1-2-works-in-el-capitan/ - macOS-only 2.1.2, "no other changes".
[11] Levelator 3.0.2, MacUpdater: https://www.macupdater.com/app_updates/appinfo/com.singularsoftware.levelator/index.html - 64-bit revival, 2020-06-19.
[12] The Levelator licence (archived): http://web.archive.org/web/20120630094404/http://www.conversationsnetwork.org/levelator-license - proprietary freeware; no modify, redistribute, reverse-engineer, or derivative; libsndfile LGPL.
[13] The Conversations Network: Mission Accomplished, Doug Kaye (2012): https://blogarithms.com/2012/09/16/cn-mission-accomplished/ - shutdown end of 2012; assets to the Internet Archive.
[3-hist] The Levelator version history, VideoHelp: https://www.videohelp.com/software/The-Levelator/version-history - "Linux version is locked to Ubuntu 7.10 x86"; Windows 2.1.1 runs on Windows 11 (user review).
[15] Levelator, VO2GoGo: https://www.vo2gogo.com/levelator/ - confirms -1.0 dB output peak and `<name>.output.wav` naming.
[16-recv] The Levelator, Podfeet (2020): https://www.podfeet.com/blog/2020/06/the-levelator/ - "drag, drop, wait, done"; revival narrative.
[9-recv] Best audio levelling solution, Christopher Penn (2020): https://www.christopherspenn.com/2020/06/you-ask-i-answer-best-audio-leveling-solution-for-podcasting/ - A/B vs Auphonic; close levelling, Auphonic ahead on noise reduction.
[10-recv] Audio processing with Levelator, Church Training Academy: https://www.churchtrainingacademy.com/audio-processing-easy-levelator/ - Levelator does not remove buzz, hum, or glitches; no noise reduction.
[11-recv] iZotope RX Leveler docs: https://docs.izotope.com/rx11/en/leveler.html - modern leveller with K-weighting and a de-esser, features Levelator lacks.
[18] Levelator binaries and source, Internet Archive (2013): https://archive.org/details/conversationsnetwork_org-levelator - preserved installers and source.
[19] Levelator on Catalina, Apple StackExchange (2019): https://apple.stackexchange.com/questions/371919/levelator-working-with-catalina - 32-bit app fails on Catalina.
[20] Alternatives to Levelator, Podcasting Hacks: https://podcastinghacks.com/alternatives-to-levelator/ - confirms Catalina break; Auphonic as successor.
[bs1770] ITU-R BS.1770 (true-peak): https://www.itu.int/dms_pubrec/itu-r/rec/bs/R-REC-BS.1770-5-202311-I!!PDF-E.pdf - K-weighting and 4x-oversampled true peak; basis for sample-peak vs true-peak.

### Jivetalking code (current branch)

Citations are inline as `file:line`. Primary files: `internal/processor/analyser_vad.go` (VAD), `internal/processor/analyser_candidates_speech.go` (speech election), `internal/processor/adaptive.go`, `adaptive_levelling_compressor.go`, `adaptive_speech_gate.go` (adaptation), `internal/processor/normalise.go` (Pass 3/4), `internal/processor/filters.go` (chain), `internal/processor/processor.go` (orchestration), plus `AGENTS.md`, `docs/Pipeline.md`, `docs/Normalisation-Tuning.md`.

---

*Report compiled June 2026 from a fresh primary-source re-research of Levelator and a code-level reading of the current Jivetalking branch (unified VAD on the momentary-LUFS axis with the cross-platform NaN floored-fraction fix, four-pass pre-calculated normaliser, and true-peak brickwall levelling).*
