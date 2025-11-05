# Jivetalking: Podcast Audio Preprocessor
## Specification & Implementation Plan

**Version:** 1.0
**Date:** 5 November 2025
**Status:** Planning Phase

---

## Executive Summary

**Jivetalking** is a professional podcast audio preprocessor that transforms raw voice recordings into broadcast-ready audio files optimized for editing in Audacity. It processes spoken word audio through a scientifically-tuned filter chain to achieve -16 LUFS podcast standard with zero audio processing knowledge required from the user.

**Design Philosophy:**
- **Best outcome by default** - Professional results with no configuration
- **Zero audio knowledge required** - Just run and get perfect results
- **Quality over speed** - Two-pass processing for maximum accuracy
- **Beautiful UX** - Bubbletea terminal interface with live feedback
- **Single responsibility** - Does one thing exceptionally well

---

## User Workflow

```
┌─────────────────────────────────────────────────────────────┐
│ Step 1: Record in Audacity                                  │
│   • Each presenter records individually                     │
│   • Export as FLAC (lossless, recommended)                  │
│   • Raw files: presenter1.flac, presenter2.flac, etc.       │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│ Step 2: Run Jivetalking                                     │
│   $ jivetalking presenter1.flac presenter2.flac presenter3  │
│   • Sequential processing with live preview                 │
│   • Output: presenter1-processed.flac, presenter2-proce...  │
│   • All files normalized to -16 LUFS (level-matched)        │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│ Step 3: Edit in Audacity                                    │
│   • Import processed files                                  │
│   • Files already level-matched and clean                   │
│   • Only editing needed: cut, arrange, crossfade            │
│   • Zero audio processing required                          │
│   • Export final: episode-final.flac                        │
└─────────────────────────────────────────────────────────────┘
                            ↓
┌─────────────────────────────────────────────────────────────┐
│ Step 4: Visualize with Jivefire                             │
│   $ jivefire episode-final.flac                             │
│   • Generate video from final audio                         │
└─────────────────────────────────────────────────────────────┘
```

---

## Technical Specification

### 1. Command Line Interface

**Basic Usage:**
```bash
jivetalking presenter1.flac presenter2.flac presenter3.flac
```

**Flags:**
```bash
jivetalking [flags] <input-files...>

Flags:
  --config, -c <path>    Path to TOML config (optional)
  --logs                 Save detailed analysis logs
  --version, -v          Show version
  --help, -h             Show help
```

**Input:**
- Accepts 1 or more audio files
- Supported formats: FLAC, WAV (via ffmpeg-go demuxer API)
- Files processed **sequentially** (one at a time)

**Output:**
- Format: FLAC (preferred) or WAV (fallback if FLAC encoding unavailable)
- Naming: `<basename>-processed.<ext>` (e.g., `presenter1-processed.flac`)
- Location: Same directory as source file

### 2. Audio Processing Pipeline

**Filter Chain (The One Preset™):**
```
Input Audio
    ↓
[Pass 1: Analysis]
    ├─ Measure input loudness (integrated LUFS, true peak, LRA)
    ├─ Analyze noise profile
    └─ Calculate dynamic range
    ↓
[Pass 2: Processing]
    ├─ FFT Noise Reduction (afftdn)
    ├─ Audio Gate (agate)
    ├─ Dynamic Range Compression (acompressor)
    └─ Loudness Normalization (loudnorm, two-pass)
    ↓
Output: -16 LUFS, broadcast-ready audio
```

**Default Filter Parameters:**
```toml
# Scientifically-tuned for spoken word podcast audio
# Users should not need to change these unless they have specific needs

[noise-reduction]
# FFT-based noise reduction (afftdn)
noise_floor = -25        # dB, noise floor level
noise_reduction = 0.02   # Reduction amount (0.0-1.0)

[gate]
# Remove silence and low-level noise (agate)
threshold = 0.003        # Activation threshold
ratio = 4.0             # Reduction ratio
attack = 5              # Attack time (ms)
release = 100           # Release time (ms)

[compression]
# Even out dynamics (acompressor)
threshold = -18         # dB, compression threshold
ratio = 4.0             # Compression ratio
attack = 20             # Attack time (ms)
release = 100           # Release time (ms)
makeup_gain = 8         # dB, post-compression gain

[loudness]
# EBU R128 loudness normalization (loudnorm)
integrated = -16        # LUFS target (podcast standard)
true_peak = -1.5        # dBTP, peak ceiling
lra = 11                # LU, loudness range target
```

### 3. Two-Pass Loudnorm Implementation

**Why Two-Pass:**
- Single-pass: Fast but less accurate (±0.5 LUFS variance)
- Two-pass: Slower but precise (±0.1 LUFS variance)
- For podcast production: Accuracy is critical for level-matching

**Implementation:**

**Pass 1: Analysis Phase**
```go
// Measure input audio characteristics
measurements := analyzeAudio(inputFile)
// Returns: input_i, input_tp, input_lra, input_thresh, target_offset
```

**Pass 2: Processing Phase**
```go
// Apply processing with Pass 1 measurements
filterChain := buildFilterChain(measurements, config)
// Filter chain uses measurements for precise normalization
processAudio(inputFile, outputFile, filterChain)
```

**FFmpeg Filter String (Pass 2):**
```
afftdn=nf=-25:nr=0.02,
agate=threshold=0.003:ratio=4:attack=5:release=100,
acompressor=threshold=-18dB:ratio=4:attack=20:release=100:makeup=8dB,
loudnorm=I=-16:TP=-1.5:LRA=11:
         measured_I={input_i}:
         measured_TP={input_tp}:
         measured_LRA={input_lra}:
         measured_thresh={input_thresh}:
         offset={target_offset}:
         linear=true:
         print_format=summary
```

### 4. User Interface (Bubbletea + Lipgloss)

**Multi-File Processing View:**
```
┌─────────────────────────────────────────────────────────────┐
│ Jivetalking - Podcast Audio Preprocessor                    │
│ Processing 3 files                                          │
└─────────────────────────────────────────────────────────────┘

 ✓ presenter1.flac → presenter1-processed.flac
   Input: -23.4 LUFS | Output: -16.0 LUFS | Δ +7.4 dB

 ⚙ presenter2.flac → presenter2-processed.flac
   ┌────────────────────────────────────────────────────────┐
   │ Pass 1/2: Analyzing Audio                              │
   │ ████████████████░░░░░░░░░░░░░░░░░░░░░░░░ 45%           │
   │                                                        │
   │ ⏱  Elapsed: 8.2s | Remaining: ~9.1s                   │
   │ 📊 Current Level: -21.3 dB | Peak: -3.2 dB             │
   └────────────────────────────────────────────────────────┘

 ○ presenter3.flac
   Queued...

┌─────────────────────────────────────────────────────────────┐
│ Overall Progress: 1/3 complete                              │
└─────────────────────────────────────────────────────────────┘
```

**Single File Processing Detail:**
```
┌─────────────────────────────────────────────────────────────┐
│ Processing: presenter2.flac                                 │
│ 📁 Size: 45.2 MB | ⏱ Duration: 1h 23m 45s                  │
└─────────────────────────────────────────────────────────────┘

Pass 1: Analysis Complete ✓
────────────────────────────────────────────────────────────
  Input Loudness:    -23.4 LUFS
  True Peak:         -3.2 dBTP
  Loudness Range:    8.7 LU
  Noise Floor:       -45 dB

Pass 2: Processing
────────────────────────────────────────────────────────────
  ✓ Noise Reduction  (-45 dB noise floor removed)
  ✓ Gate Applied     (silence removed, -40 dB threshold)
  ⚙ Compressing      (dynamics: 12 dB → 6 dB range)
  ○ Normalizing      (target: -16.0 LUFS)

  ████████████████████░░░░░░░░░░░░░░░░░░░░░░ 68%
  ⏱  19.2s elapsed | ~9.1s remaining

┌─────────────────────────────────────────────────────────────┐
│ 🎙 Live Audio Level: -18.3 dB                               │
│ ▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓▓░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░░   │
└─────────────────────────────────────────────────────────────┘
```

**Completion Summary:**
```
┌─────────────────────────────────────────────────────────────┐
│ ✨ Processing Complete!                                     │
└─────────────────────────────────────────────────────────────┘

 ✓ presenter1.flac → presenter1-processed.flac
   Before: -23.4 LUFS | After: -16.0 LUFS | Quality: ★★★★★
   Noise Reduced: -8 dB | Dynamic Range: 12→6 dB

 ✓ presenter2.flac → presenter2-processed.flac
   Before: -21.8 LUFS | After: -16.0 LUFS | Quality: ★★★★★
   Noise Reduced: -6 dB | Dynamic Range: 14→7 dB

 ✓ presenter3.flac → presenter3-processed.flac
   Before: -25.1 LUFS | After: -16.0 LUFS | Quality: ★★★★★
   Noise Reduced: -7 dB | Dynamic Range: 15→8 dB

────────────────────────────────────────────────────────────
All files normalized to -16 LUFS and level-matched ✓
Ready for import into Audacity - no additional processing needed!

📁 Output: 3 files | 💾 Total: 142.7 MB | ⏱ Total Time: 2m 34s
```

### 5. Configuration File (Optional)

**Location:** `jivetalking.toml` (current directory) or via `--config` flag

```toml
# Jivetalking Configuration
# Only needed if you want to tune the default parameters
# Most users will never need this file

[noise-reduction]
noise_floor = -25        # dB, lower = more aggressive
noise_reduction = 0.02   # 0.0-1.0, higher = more reduction

[gate]
threshold = 0.003        # Lower = more aggressive (removes more)
ratio = 4.0             # Higher = more reduction
attack = 5              # ms, how fast gate opens
release = 100           # ms, how fast gate closes

[compression]
threshold = -18         # dB, lower = more compression
ratio = 4.0             # Higher = more compression
attack = 20             # ms, how fast compressor responds
release = 100           # ms, how fast compressor releases
makeup_gain = 8         # dB, compensate for reduced peaks

[loudness]
integrated = -16        # LUFS target (don't change for podcasts!)
true_peak = -1.5        # dBTP, peak ceiling
lra = 11                # LU, loudness range target

[output]
format = "flac"         # "flac" or "wav"
```

### 6. Analysis Logs (--logs flag)

**When enabled:** Saves detailed processing logs alongside output files

**Example:** `presenter1-processed.log`
```
Jivetalking Analysis Report
============================
File: presenter1.flac
Processed: 2025-11-05 12:45:23 GMT
Duration: 1h 23m 45s

Pass 1: Input Analysis
----------------------
Integrated Loudness: -23.4 LUFS
True Peak:           -3.2 dBTP
Loudness Range:      8.7 LU
Noise Floor:         -45 dB
Dynamic Range:       12.3 dB
Sample Rate:         48000 Hz
Bit Depth:           24-bit
Channels:            1 (mono)

Pass 2: Processing Applied
---------------------------
Noise Reduction:
  - Noise floor: -45 dB
  - Reduction: -8.2 dB average
  - Method: FFT spectral subtraction

Gate:
  - Threshold: 0.003
  - Activated: 342 times
  - Total silence removed: 3m 12s

Compression:
  - Input dynamic range: 12.3 dB
  - Output dynamic range: 6.1 dB
  - Gain reduction: 6.2 dB average
  - Makeup gain: +8 dB

Loudness Normalization:
  - Input: -23.4 LUFS
  - Target: -16.0 LUFS
  - Adjustment: +7.4 dB
  - True peak: -1.5 dBTP (compliant)
  - Final LRA: 6.8 LU

Output Analysis
---------------
Integrated Loudness: -16.0 LUFS (✓ target achieved)
True Peak:           -1.5 dBTP (✓ compliant)
Loudness Range:      6.8 LU (✓ within target)
Quality Score:       ★★★★★ (excellent)

Processing Time
---------------
Pass 1 (Analysis):   8.2s
Pass 2 (Processing): 34.7s
Total Time:          42.9s
Real-time Factor:    116x (2m 34s processing time for 5h 0m audio)
```

---

## Implementation Plan

### Phase 1: Foundation (Week 1)

**Project Setup:**
- [x] Create repository: `linuxmatters/jivetalking`
- [x] Copy Jivefire project structure template
- [x] Set up Go modules (ffmpeg-go, bubbletea, lipgloss, kong)
- [x] Configure Nix flake for development environment
- [x] Set up GitHub Actions CI/CD

**Directory Structure:**
```
jivetalking/
├── cmd/
│   └── jivetalking/
│       └── main.go              # Entry point with Kong CLI
├── internal/
│   ├── processor/
│   │   ├── processor.go         # Main processing orchestrator
│   │   ├── analyzer.go          # Pass 1: Audio analysis
│   │   ├── filters.go           # Filter chain builder
│   │   └── encoder.go           # Output FLAC/WAV encoder
│   ├── audio/
│   │   ├── reader.go            # ffmpeg-go demuxer wrapper
│   │   ├── decoder.go           # ffmpeg-go decoder wrapper
│   │   └── metadata.go          # Audio file metadata
│   ├── config/
│   │   ├── config.go            # TOML config parser
│   │   └── defaults.go          # Default filter parameters
│   └── ui/
│       ├── processing.go        # Bubbletea processing UI
│       ├── summary.go           # Results summary UI
│       └── styles.go            # Lipgloss styling
├── docs/
│   └── SPECIFICATION.md         # This document
│
├── testdata/
│   ├── test-voice.flac          # Test audio samples
│   └── *.log                    # Expected output logs
├── go.mod
├── go.sum
├── flake.nix
├── justfile
├── LICENSE                      # GPLv3
└── README.md
```

**Deliverables:**
- [x] Compiling binary with basic CLI
- [x] File input validation

### Phase 2: Audio Processing Core (Week 2)

**Pass 1: Analysis Implementation:**
- [ ] Integrate ffmpeg-go filter graph API
- [ ] Implement loudnorm analysis (first pass)
- [ ] Extract measurements: input_i, input_tp, input_lra, input_thresh, target_offset
- [ ] Calculate noise floor estimate
- [ ] Store measurements for Pass 2

**Pass 2: Processing Implementation:**
- [ ] Build filter chain with measurements
- [ ] Implement FFmpeg filter string generation:
  - afftdn (noise reduction)
  - agate (silence removal)
  - acompressor (dynamics)
  - loudnorm (two-pass with measurements)
- [ ] Create AVFilterGraph from string
- [ ] Process audio through filter chain
- [ ] Monitor processing progress

**Audio I/O:**
- [ ] Implement ffmpeg-go demuxer for FLAC/WAV input reading
- [ ] Decode audio frames via ffmpeg-go codec API
- [ ] Implement ffmpeg-go FLAC encoder output (preferred)
- [ ] Implement ffmpeg-go WAV encoder fallback
- [ ] Preserve sample rate and bit depth where possible
- [ ] Keep audio in AVFrame format throughout pipeline (no format conversion overhead)

**Deliverables:**
- [ ] Audio format detection
- [ ] Working two-pass processing
- [ ] Accurate -16 LUFS normalization
- [ ] Output files with `-processed` suffix

### Phase 3: Configuration System (Week 3)

**TOML Parser:**
- [ ] Load config from `jivetalking.toml` or `--config` path
- [ ] Parse all filter parameters
- [ ] Validate parameter ranges
- [ ] Merge with defaults
- [ ] Document parameter meanings in comments

**Default Tuning:**
- [ ] Research optimal parameters for podcast audio
- [ ] Test with various voice types (male, female, different mics)
- [ ] Calibrate noise reduction aggressiveness
- [ ] Fine-tune compression for natural sound
- [ ] Validate against professional podcast standards

**Deliverables:**
- [ ] Working TOML config system
- [ ] Scientifically-tuned defaults
- [ ] Config validation with helpful error messages

### Phase 4: User Interface (Week 4)

**Bubbletea Application:**
- [ ] Multi-file queue display
- [ ] Per-file progress tracking
- [ ] Live processing statistics
- [ ] Pass 1/Pass 2 phase indicators
- [ ] Real-time audio level visualization
- [ ] Filter effect indicators (noise reduction active, etc.)

**Lipgloss Styling:**
- [ ] Professional color scheme
- [ ] Progress bars with gradients
- [ ] Status icons (✓, ⚙, ○)
- [ ] Bordered sections
- [ ] Responsive layout

**Progress Updates:**
- [ ] FFmpeg progress callback integration
- [ ] Frame-by-frame progress tracking
- [ ] Time estimates (elapsed/remaining)
- [ ] Audio level monitoring

**Deliverables:**
- ✓ Beautiful terminal UI
- ✓ Real-time feedback
- ✓ Professional appearance

### Phase 5: Analysis & Reporting (Week 5)

**Measurements Display:**
- [ ] Show input LUFS before processing
- [ ] Show output LUFS after processing
- [ ] Display delta (improvement)
- [ ] True peak compliance check
- [ ] Loudness range analysis

**Filter Effects Reporting:**
- [ ] Noise reduction: dB removed
- [ ] Gate: silence removed (time)
- [ ] Compression: dynamic range change
- [ ] Loudness: normalization adjustment

**Log Files (--logs):**
- [ ] Generate detailed analysis reports
- [ ] Save alongside output files
- [ ] Include all measurements and effects
- [ ] Format for readability
- [ ] Timestamp and metadata

**Quality Scoring:**
- [ ] Analyze output quality metrics
- [ ] Display star rating (★★★★★)
- [ ] Detect potential issues (clipping, etc.)

**Deliverables:**
- ✓ Comprehensive measurement display
- ✓ Detailed log file generation
- ✓ Quality assessment system

### Phase 6: Testing & Polish (Week 6)

**Test Coverage:**
- [ ] Unit tests for filter chain builder
- [ ] Integration tests for full pipeline
- [ ] Test with various input formats (FLAC, WAV)
- [ ] Test with mono and stereo inputs
- [ ] Test with different sample rates (44.1kHz, 48kHz, 96kHz)
- [ ] Edge cases (very quiet audio, very loud audio)

**Performance Testing:**
- [ ] Benchmark processing speed
- [ ] Memory usage profiling
- [ ] Optimize bottlenecks if needed
- [ ] Real-time factor measurement

**Documentation:**
- [ ] Complete README with examples
- [ ] User guide for TOML tuning
- [ ] Troubleshooting section
- [ ] Contribution guidelines

**Production Readiness:**
- [ ] Error handling polish
- [ ] Helpful error messages
- [ ] Version information
- [ ] Build for Linux, macOS (arm64/amd64)
- [ ] Release GitHub Actions workflow

**Deliverables:**
- ✓ Comprehensive test suite
- ✓ Professional documentation
- ✓ Production-ready binary

---

## Technical Dependencies

### Go Modules

```go
require (
    github.com/csnewman/ffmpeg-go v0.6.0      // FFmpeg bindings (audio I/O + filter graph)
    github.com/charmbracelet/bubbletea v0.x    // TUI framework
    github.com/charmbracelet/lipgloss v0.x     // Terminal styling
    github.com/alecthomas/kong v0.x            // CLI parser
    // Note: No pure Go audio decoders needed - ffmpeg-go handles all I/O
)
```

**Architecture Decision:**
- Uses ffmpeg-go for all audio I/O (reading, decoding, encoding, writing)
- Audio stays in FFmpeg's native `AVFrame` format throughout pipeline
- No format conversion overhead between decoder → filter graph → encoder
- Simpler architecture with single dependency for all audio operations
- MP3 not needed for podcast workflow (FLAC/WAV only)

### External Requirements

- **None!** Embedded FFmpeg via ffmpeg-go static libraries
- Works offline, no network dependencies
- Single binary distribution

---

## Quality Targets

### Performance

- **Real-time Factor:** >50x (1 hour audio in <72 seconds)
- **Memory Usage:** <200MB for typical podcast file
- **Two-pass overhead:** ~2x processing time (acceptable for quality)

### Accuracy

- **LUFS Precision:** ±0.1 LUFS from -16.0 target
- **Level Matching:** All processed files within ±0.2 LUFS
- **No Clipping:** True peak always ≤ -1.5 dBTP

### Quality

- **Transparency:** Processing should be inaudible on quality check
- **Natural Sound:** No pumping, no artifacts, no distortion
- **Professional Standard:** Matches commercial podcast production

---

## Success Metrics

### User Experience
- [ ] Zero-config usage works perfectly for 95% of users
- [ ] Processing time acceptable (<5 minutes for 2-hour podcast)
- [ ] UI provides confidence through clear feedback
- [ ] Output quality indistinguishable from expensive DAW processing

### Technical Excellence
- [ ] All files achieve -16.0 LUFS ±0.1 LUFS
- [ ] No audio artifacts or quality degradation
- [ ] Handles edge cases gracefully
- [ ] Comprehensive error messages guide users

### Production Readiness
- [ ] Stable on Linux and macOS
- [ ] No crashes or data loss
- [ ] Works with all common audio formats
- [ ] Professional documentation

---

## Future Enhancements (Post-MVP)

**Not in initial release, but possible later:**

1. **Batch Processing Optimizations**
   - Parallel processing option
   - Job queue management
   - Resume interrupted processing

2. **Advanced Features**
   - Custom filter chain support (for power users)
   - Multiple presets (podcast, audiobook, interview)
   - De-esser filter integration
   - Room tone matching

3. **Analysis Tools**
   - Spectral analysis visualization
   - Before/after waveform comparison
   - Quality recommendations

4. **Integration**
   - Watch folder mode
   - Plugin system for custom filters
   - REST API for automation

---

## Comparison with Alternatives

| Feature | Jivetalking | Auphonic | Adobe Podcast | Manual in Audacity |
|---------|------------|----------|---------------|-------------------|
| Cost | Free | €11-€99/mo | Free* | Free |
| Offline | ✓ Yes | ✗ No | ✗ No | ✓ Yes |
| Batch | ✓ Yes | ✓ Yes | ✗ No | Manual |
| Quality | ★★★★★ | ★★★★★ | ★★★★☆ | Skill-dependent |
| Speed | Fast | Slow (upload) | Slow (upload) | Slow (manual) |
| Learning | Zero | Minimal | Minimal | High |
| Customization | TOML | Web UI | Limited | Complete |
| Open Source | ✓ Yes | ✗ No | ✗ No | ✓ Yes |

**\*** Adobe Podcast Enhanced Speech is free but limited

---

## License & Distribution

- **License:** GPLv3 (matches Jivefire, required by FFmpeg)
- **Distribution:** Single binary with embedded FFmpeg
- **Platforms:** Linux (amd64, arm64), macOS (amd64, arm64)
- **Size:** ~50-65MB per platform (static FFmpeg libraries)

---

## Timeline

- **Week 1-2:** Foundation + Core Processing
- **Week 3-4:** Configuration + UI
- **Week 5-6:** Reporting + Polish
- **Total:** 6 weeks to production-ready v1.0

**Estimated LOC:** ~3,000 lines Go (similar to Jivefire)

---

## Conclusion

Jivetalking will provide **professional podcast audio preprocessing** with zero audio engineering knowledge required. By focusing on a single, scientifically-tuned filter chain and beautiful user experience, it will make broadcast-quality audio accessible to everyone.

The two-pass loudnorm approach ensures perfect level-matching across multiple presenters, while the comprehensive filter chain (noise reduction, gate, compression) delivers clean, professional sound ready for editing.

**Goal:** Transform raw voice recordings into broadcast-ready audio with a single command.

**Success:** When users say "I can't believe it's this easy to get professional sound."
