# Usage

In-depth guide to driving Jivetalking and reading what it produces. For the basic flag table and first-run examples, see the README. For the pipeline itself, see [Pipeline.md](Pipeline.md).

## Quality Ratings

When a file finishes, the completion box shows two star ratings:

```
Jivetalking 🕺

╭──────────────────────────────────────────╮
│ Processing 3 files, 3 complete, 0 failed │
╰──────────────────────────────────────────╯

 🗸 LMP-83-mark-LUFS-16-processed.flac
╭──────────────────────────────────────────╮
│ Time        02:31  ·  ⚡ 19.0×           │
│ Loudness    -35.2 → -16.1 LUFS  Δ +19.1  │
│ True peak    -6.2 →  -1.7 ㏈TP  Δ  +4.5  │
│ Dynamics     15.0 →  13.3 LU    Δ  -1.7  │
│ Noise floor < -96 ㏈                     │
│ Recording   ★★★★☆  Great                 │
│ Processed   ★★★★★  Excellent             │
╰──────────────────────────────────────────╯
 🗸 LMP-83-martin-LUFS-16-processed.flac
╭──────────────────────────────────────────╮
│ Time        02:38  ·  ⚡ 18.1×           │
│ Loudness    -27.8 → -16.0 LUFS  Δ +11.8  │
│ True peak    -4.5 →  -1.8 ㏈TP  Δ  +2.7  │
│ Dynamics     14.7 →  12.0 LU    Δ  -2.7  │
│ Noise floor -91 ㏈                       │
│ Recording   ★★★★☆  Great                 │
│ Processed   ★★★★★  Excellent             │
╰──────────────────────────────────────────╯
 🗸 LMP-83-popey-LUFS-16-processed.flac
╭──────────────────────────────────────────╮
│ Time        02:43  ·  ⚡ 17.6×           │
│ Loudness    -29.8 → -16.0 LUFS  Δ +13.8  │
│ True peak    -0.1 →  -1.3 ㏈TP  Δ  -1.2  │
│ Dynamics     12.3 →   8.9 LU    Δ  -3.4  │
│ Noise floor -86 ㏈                       │
│ Recording   ★★☆☆☆  Fair                  │
│ Processed   ★★★★★  Excellent             │
╰──────────────────────────────────────────╯
```

**Recording** grades your source capture, the raw audio you fed in. This is the one that varies, and the one you can act on. **Processed** grades the output against the -16 LUFS broadcast target, and it is usually five stars, because hitting that target is jivetalking's job and it reliably does. Side by side, the pair tells the story: we took your two-star capture to a five-star master.

The Recording score looks at three things, in plain terms:

- **Clean:** low background hiss and a healthy gap between your voice and the room
- **Headroom:** no clipping; a capture recorded too hot scores zero here
- **Level:** recorded at a sensible loudness, without wild swings

Scores run 1 to 5 stars (Poor, Fair, Good, Great, Excellent). The scale is grounded on a real podcast corpus, so the stars mean something rather than being plucked from the air.

A low Recording star is a hint to improve the capture next time: record in a quieter room, back the gain off so peaks do not clip, and get the level up if it is too quiet. Either way, jivetalking still rescues the file to a broadcast-ready master.

## Analysis-Only Mode

Pass `--analysis-only` to run only Pass 1 analysis. It writes a Markdown analysis report (`<input>-analysis.md`) next to each input and shows the Recording stars plus gain advice on screen, without producing any processed audio. Useful for quickly understanding what jivetalking sees in your recordings, diagnosing setup problems, or checking whether a file needs processing at all.

The analysis report covers:

- **Loudness & dynamics**: integrated LUFS, true peak, loudness range, crest factor
- **Room tone & speech detection**: a single voice-activity detector splits speech from silence; the best-scoring speech region is elected for speech-aware metrics and the longest quiet stretch profiles the noise floor; voice-activated recording detected automatically from the digital-silence fraction (Riverside, Zencastr)
- **Derived measurements**: noise floor, gate baseline, noise-to-speech headroom
- **Filter adaptation**: the exact parameters jivetalking would apply, including highpass frequency, gate threshold, NR settings, de-esser intensity, and levelling-compressor configuration
- **Spectral summary**: full spectral characterisation with objective metric definitions

### Gain advice

Analysis mode is the place to fix your capture before you commit to a take. Alongside the Recording stars, each file gets a single-line verdict and a five-cell thermometer bar that fills with your input true peak, running cyan (quiet) through green (well set) to red (clipping):

```
Jivetalking 🕺

╭─────────────────────────────────────────╮
│ Analysing 3 files, 3 complete, 0 failed │
╰─────────────────────────────────────────╯

 🗸 LMP-83-mark.flac → LMP-83-mark-flac-analysis.md
   Recording  ★★★★☆  Great
   Gain       ▰▰▰▱▱  Level well set. Peaks at -6.2 ㏈TP. No action required.

 🗸 LMP-83-martin.flac → LMP-83-martin-flac-analysis.md
   Recording  ★★★★☆  Great
   Gain       ▰▰▰▱▱  Level well set. Peaks at -4.5 ㏈TP. No action required.

 🗸 LMP-83-popey.flac → LMP-83-popey-flac-analysis.md
   Recording  ★★☆☆☆  Fair
   Gain       ▰▰▰▰▱  Hot. Peaks at -0.1 ㏈TP. Lower input gain ~6 ㏈.
```

The verdict reads `Interpretation. Level. Advice.` and keys off the input true peak alone, with a target of -6 ㏈TP:

| State | Input true peak | Advice |
|-------|-----------------|--------|
| **Clipping** | ≥ 0 ㏈TP | Lower input gain by the stated amount |
| **Hot** | -1 to 0 ㏈TP | Lower input gain by the stated amount |
| **Well set** | -12 to -1 ㏈TP | No action required |
| **Quiet** | < -12 ㏈TP | Raise input gain by the stated amount |

The stars and the gain advice are console-only: the Markdown report stays empirical and verdict-free.

## Diagnostics

`--diagnostics` writes extra artefacts beside the report for sweeps and before/after comparison. It changes no DSP, so the processed audio is byte-identical with the flag on or off; it only adds FFmpeg passes to render the extras. The flag emits:

- **Before/after spectrogram PNGs**, named `<name>-LUFS-NN-processed.spectrogram-<kind>-<stage>.png`. `<kind>` is `whole`, `roomtone`, or `speech`; `<stage>` is `before` or `after`. Each before/after pair shares identical dimensions and scales for an honest side-by-side. Analysis-only emits `input` spectrograms (no "after"). The Markdown report links them in a `## Spectrograms` section.
- **Interval sidecars** `<name>.intervals.jsonl` and `<name>.candidates.jsonl`, the raw 250 ms interval samples and the scored speech candidates. The report's inline summaries cover the common case, so these are only needed for deep analysis.
