# Usage

In-depth guide to driving Jivetalking and reading what it produces. For the basic flag table and first-run examples, see the README. For the pipeline itself, see [Pipeline.md](Pipeline.md).

## Quality Ratings

When a file finishes, the completion box shows two star ratings:

```
Recording   ★★☆☆☆  Fair
Processed   ★★★★★  Excellent
```

**Recording** grades your source capture, the raw audio you fed in. This is the one that varies, and the one you can act on. **Processed** grades the output against the -16 LUFS broadcast target, and it is usually five stars, because hitting that target is jivetalking's job and it reliably does. Side by side, the pair tells the story: we took your two-star capture to a five-star master.

The Recording score looks at three things, in plain terms:

- **Clean:** low background hiss and a healthy gap between your voice and the room
- **Headroom:** no clipping; a capture recorded too hot scores zero here
- **Level:** recorded at a sensible loudness, without wild swings

Scores run 1 to 5 stars (Poor, Fair, Good, Great, Excellent). The scale is grounded on a real podcast corpus, so the stars mean something rather than being plucked from the air.

A low Recording star is a hint to improve the capture next time: record in a quieter room, back the gain off so peaks do not clip, and get the level up if it is too quiet. Either way, jivetalking still rescues the file to a broadcast-ready master.

## Analysis-Only Mode

Pass `-a` to run only Pass 1 analysis. It writes a Markdown analysis report (`<input>-analysis.md`) next to each input and shows the Recording stars plus gain advice on screen, without producing any processed audio. Useful for quickly understanding what jivetalking sees in your recordings, diagnosing setup problems, or checking whether a file needs processing at all.

The analysis report covers:

- **Loudness & dynamics**: integrated LUFS, true peak, loudness range, crest factor
- **Room tone & speech detection**: candidate regions scored and elected for noise profiling and speech-aware metrics; voice-activated recording detected automatically (Riverside, Zencastr)
- **Derived measurements**: noise floor, gate baseline, noise-to-speech headroom
- **Filter adaptation**: the exact parameters jivetalking would apply, including highpass frequency, gate threshold, NR settings, de-esser intensity, and levelling-compressor configuration
- **Spectral summary**: full spectral characterisation with objective metric definitions

### Gain advice

Analysis mode is the place to fix your capture before you commit to a take. Alongside the Recording stars, each file gets a single-line verdict and a five-cell thermometer bar that fills with your input true peak, running cyan (quiet) through green (well set) to red (clipping):

```
Recording  ★★☆☆☆  Fair
Gain       ▰▱▱▱▱  Quiet. Peaks at -14.2 ㏈TP. Raise input gain ~8 ㏈.
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
- **Interval sidecars** `<name>.intervals.jsonl` and `<name>.candidates.jsonl`, the raw 250 ms interval samples and scored room-tone candidates. The report's inline summaries cover the common case, so these are only needed for deep analysis.
