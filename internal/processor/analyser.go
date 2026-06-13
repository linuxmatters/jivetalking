// Package processor handles audio analysis and processing
package processor

import (
	stdcontext "context"
	"fmt"
	"math"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// debugLogger is a config-borne debug logging function threaded through the
// processor.
type debugLogger func(format string, args ...any)

// Logf writes to the logger if non-nil, otherwise does nothing.
func (l debugLogger) Logf(format string, args ...any) {
	if l != nil {
		l(format, args...)
	}
}

// RoomToneRegion represents a detected room tone period in the audio.
// This is the elected quiet ambient region used for noise profiling and
// before/after comparison; it is distinct from true digital silence.
type RoomToneRegion struct {
	Start    time.Duration `json:"start"`
	End      time.Duration `json:"end"`
	Duration time.Duration `json:"duration"`
}

// NoiseProfile contains measurements from the elected room tone region.
// These measurements serve as a reference baseline for adaptive filter tuning:
//   - MeasuredNoiseFloor → elected noise floor (Noise.Floor) feeding the DS201
//     gate threshold and de-esser/quality references.
//   - CrestFactor/PeakLevel → peak-reference input to the legacy DS201 gate
//     threshold path (calculateDS201GateThresholdLegacy via adaptive_ds201_gate.go),
//     reached only when noise-to-speech separation is too small for the aggression maths.
//
// Entropy and the spectral fields are measured here for the report and
// contamination detection; the current adaptive gate keys its release/range on
// flux, LRA, and noise floor, not on room-tone entropy.
//
// Note: The room tone region is also re-measured in Pass 2 and Pass 4 for
// before/after comparison of noise reduction effectiveness.
type NoiseProfile struct {
	Start              time.Duration `json:"start"`                        // Start time of room tone region used (time.Duration ns)
	Duration           time.Duration `json:"duration"`                     // Duration of extracted sample (time.Duration ns)
	MeasuredNoiseFloor float64       `json:"measured_floor_dbfs"`          // dBFS, RMS level of room tone (average noise)
	PeakLevel          float64       `json:"peak_level_dbfs"`              // dBFS, peak level in room tone (transient noise indicator)
	CrestFactor        float64       `json:"crest_factor_db"`              // Peak - RMS in dB (high = impulsive noise, low = steady noise)
	Entropy            float64       `json:"entropy"`                      // Signal randomness (1.0 = white noise, lower = tonal noise like hum)
	ExtractionWarning  string        `json:"extraction_warning,omitempty"` // Warning message if extraction had issues

	// Spectral characteristics for contamination detection (added during candidate evaluation)
	SpectralCentroid float64 `json:"spectral_centroid_hz,omitempty"` // Hz, where energy is concentrated (voice range: 300-4000 Hz)
	SpectralFlatness float64 `json:"spectral_flatness,omitempty"`    // 0-1, noise-like vs tonal (higher = more noise-like)
	SpectralKurtosis float64 `json:"spectral_kurtosis,omitempty"`    // Peakiness (high = peaked harmonics like speech)

	// Golden sub-region refinement info (populated when a long candidate is refined)
	OriginalStart    time.Duration `json:"original_start,omitempty"`    // Original candidate start before refinement (time.Duration ns)
	OriginalDuration time.Duration `json:"original_duration,omitempty"` // Original candidate duration before refinement (time.Duration ns)
	WasRefined       bool          `json:"was_refined,omitempty"`       // True if region was refined from a longer candidate
}

// RegionSample holds the bare per-region measurement subset shared by the room
// tone and speech candidate structs (and reused for the Pass 2/4 output region
// samples). It carries only amplitude, spectral, and loudness measurements; the
// candidate structs embed it and add their own scoring/stability/bands fields.
// Embedded anonymously so Go field promotion keeps every existing field access
// (`m.RMSLevel`, `m.Spectral.Centroid`) and the default-marshalled JSON identical.
type RegionSample struct {
	// Amplitude metrics
	RMSLevel    float64 `json:"rms_level_dbfs"`  // dBFS, average level
	PeakLevel   float64 `json:"peak_level_dbfs"` // dBFS, max peak level across region
	CrestFactor float64 `json:"crest_factor_db"` // Peak - RMS in dB

	// Spectral metrics (averaged across region)
	Spectral SpectralMetrics `json:"spectral"`

	// Loudness metrics (averaged/max across region)
	MomentaryLUFS float64 `json:"momentary_lufs"`   // LUFS, average momentary loudness
	ShortTermLUFS float64 `json:"short_term_lufs"`  // LUFS, average short-term loudness
	TruePeak      float64 `json:"true_peak_dbtp"`   // dBTP, max true peak across region
	SamplePeak    float64 `json:"sample_peak_dbfs"` // dBFS, max sample peak across region
}

// RoomToneCandidateMetrics contains measurements for evaluating room tone region candidates.
// These metrics are collected before final selection to enable multi-metric scoring.
// Includes all measurements available from IntervalSample for future filter tuning.
type RoomToneCandidateMetrics struct {
	Region RoomToneRegion `json:"region"` // The room tone region being evaluated

	// Shared amplitude/spectral/loudness subset (promoted by anonymous embed).
	RegionSample

	// Warning flags (populated during scoring)
	TransientWarning string `json:"transient_warning,omitempty"` // Warning if danger zone signature detected

	// Scoring (computed after measurement)
	Score float64 `json:"score"` // Composite score for candidate ranking

	// StabilityScore measures the temporal consistency of the room tone region (0-1).
	// Higher scores indicate more stable measurements across the region, suggesting
	// intentionally-recorded room tone rather than accidental gaps between speech.
	// Calculated from RMS variance and average spectral flux across intervals.
	StabilityScore float64 `json:"stability_score"`

	// Refinement metadata (populated when pre-scoring refinement trims the candidate)
	OriginalStart    time.Duration `json:"original_start,omitempty"`    // Original candidate start before refinement
	OriginalDuration time.Duration `json:"original_duration,omitempty"` // Original candidate duration before refinement
	WasRefined       bool          `json:"was_refined,omitempty"`       // True if region was refined from a longer candidate

	// intervals holds the constituent 250 ms interval samples for this candidate.
	// Unexported and excluded from the report JSON contract; used by the RMS-dispersion
	// (MAD) transient gate during scoring to reject regions with spiking sub-intervals.
	intervals []IntervalSample
}

// SpeechRegion represents a detected continuous speech period in the audio.
// Used for extracting representative speech measurements for adaptive tuning.
type SpeechRegion struct {
	Start    time.Duration `json:"start"`
	End      time.Duration `json:"end"`
	Duration time.Duration `json:"duration"`
}

// SpeechCandidateMetrics contains measurements for evaluating speech region candidates.
// These metrics characterise typical speech levels for adaptive filter tuning.
// Includes all measurements available from IntervalSample for future filter tuning.
type SpeechCandidateMetrics struct {
	Region SpeechRegion `json:"region"` // The speech region being evaluated

	// Shared amplitude/spectral/loudness subset (promoted by anonymous embed).
	RegionSample

	// Stability metrics (populated during measurement)
	VoicingDensity float64 `json:"voicing_density,omitempty"` // Proportion of voiced intervals (0-1)

	// Speech-region band RMS (populated for the elected SpeechProfile only).
	// Body = 1-3 kHz vocal presence, Sibilant = 6-9 kHz "s"/"sh" energy (both dBFS).
	// The de-esser engages on the band excess (Sibilant - Body); see adaptive_deesser.go.
	BodyBandRMS   float64 `json:"speech_band_body_rms_dbfs,omitempty"` // dBFS, 1-3 kHz RMS over the speech region
	SibBandRMS    float64 `json:"speech_band_sib_rms_dbfs,omitempty"`  // dBFS, 6-9 kHz RMS over the speech region
	BandsMeasured bool    `json:"speech_bands_measured,omitempty"`     // True only when both body and sibilant bands measured successfully

	// Scoring
	Score float64 `json:"score"` // Composite score for candidate ranking

	// Golden sub-region refinement info (populated when a long candidate is refined)
	OriginalStart    time.Duration `json:"original_start,omitempty"`    // Original candidate start before refinement
	OriginalDuration time.Duration `json:"original_duration,omitempty"` // Original candidate duration before refinement
	WasRefined       bool          `json:"was_refined,omitempty"`       // True if region was refined from a longer candidate
}

// LoudnessMetrics holds the ebur128 windowed-loudness measurements shared by the
// input and output stages (momentary/short-term/sample-peak). Reused as the base
// of the input/output stage-specific loudness blocks (8.1 loudness domain).
type LoudnessMetrics struct {
	MomentaryLoudness float64 `json:"momentary_lufs"`   // Momentary loudness (400ms window, LUFS)
	ShortTermLoudness float64 `json:"short_term_lufs"`  // Short-term loudness (3s window, LUFS)
	SamplePeak        float64 `json:"sample_peak_dbfs"` // Sample peak (dBFS)
}

// InputLoudnessMetrics is the Pass-1 loudness domain block: the shared windowed
// loudness (embedded) plus the input-only integrated/true-peak/LRA/threshold and
// the a-priori target offset (config.TargetI - InputI).
type InputLoudnessMetrics struct {
	LoudnessMetrics // shared windowed loudness, promoted flat into this stage block

	InputI       float64 `json:"integrated_lufs"`  // Integrated loudness (LUFS)
	InputTP      float64 `json:"true_peak_dbtp"`   // True peak (dBTP)
	InputLRA     float64 `json:"lra_lu"`           // Loudness range (LU)
	InputThresh  float64 `json:"thresh_lufs"`      // Threshold level (LUFS)
	TargetOffset float64 `json:"target_offset_db"` // Offset for normalization (config.TargetI - InputI)
}

// DynamicsMetrics holds the astats time-domain measurements shared by the input
// and output stages (8.1 dynamics domain). The astats noise-floor estimate is not
// here: it lives in the input-only NoiseMetrics block (8.2, floor_astats).
type DynamicsMetrics struct {
	DynamicRange float64 `json:"dynamic_range_db"` // Measured dynamic range (dB)
	RMSLevel     float64 `json:"rms_level_dbfs"`   // Overall RMS level (dBFS)
	PeakLevel    float64 `json:"peak_level_dbfs"`  // Overall peak level (dBFS)
	RMSTrough    float64 `json:"rms_trough_dbfs"`  // RMS level of quietest segments (dBFS)
	RMSPeak      float64 `json:"rms_peak_dbfs"`    // RMS level of loudest segments (dBFS)

	DCOffset          float64 `json:"dc_offset"`              // Mean amplitude displacement from zero
	FlatFactor        float64 `json:"flat_factor"`            // Consecutive samples at peak (clipping indicator)
	CrestFactor       float64 `json:"crest_factor_astats_db"` // Peak-to-RMS ratio in dB (astats Crest_factor, converted from linear)
	ZeroCrossingsRate float64 `json:"zero_crossings_rate"`    // Zero crossing rate (low=bass, high=noise/sibilance)
	ZeroCrossings     float64 `json:"zero_crossings_count"`   // Total zero crossings
	MaxDifference     float64 `json:"max_difference"`         // Largest sample-to-sample change (clicks/pops indicator)
	MinDifference     float64 `json:"min_difference"`         // Smallest sample-to-sample change
	MeanDifference    float64 `json:"mean_difference"`        // Average sample-to-sample change
	RMSDifference     float64 `json:"rms_difference"`         // RMS of sample-to-sample changes
	Entropy           float64 `json:"entropy"`                // Signal randomness (1.0 = white noise, lower = structured)
	MinLevel          float64 `json:"min_level_dbfs"`         // dBFS, minimum sample level (converted from linear)
	MaxLevel          float64 `json:"max_level_dbfs"`         // dBFS, maximum sample level (converted from linear)
	NoiseFloorCount   float64 `json:"noise_floor_count"`      // Number of samples in noise floor measurement
	BitDepth          float64 `json:"bit_depth"`              // Effective bit depth
	NumberOfSamples   float64 `json:"number_of_samples"`      // Total samples processed
}

// NoiseMetrics is the input-only noise domain block (8.1/8.2). It holds the
// canonical elected noise floor (Floor + FloorSource), the two distinct floor
// estimates kept separate (prescan, astats), the adaptive room-tone detect level,
// the voice-activated flag, and the noise-reduction headroom.
type NoiseMetrics struct {
	Floor               float64 `json:"floor_dbfs"`                  // Derived noise floor (dBFS), three-tier; overwritten by room-tone profile if elected
	FloorSource         string  `json:"floor_source"`                // Source of Floor: "astats" / "rms_estimate" / "ebur128_estimate" / "silence_profile"
	FloorPrescan        float64 `json:"floor_prescan_dbfs"`          // Noise floor estimated from interval data (dBFS)
	FloorAstats         float64 `json:"floor_astats_dbfs"`           // FFmpeg astats noise floor estimate (dBFS)
	RoomToneDetectLevel float64 `json:"room_tone_detect_level_dbfs"` // Adaptive room tone detection threshold (dBFS)
	VoiceActivated      bool    `json:"voice_activated"`             // True when >= 95% of room tone candidates are digital silence
	ReductionHeadroom   float64 `json:"reduction_headroom_db"`       // dB gap between noise and quiet speech
}

// RegionMetrics is the input-only regions domain block (8.1). It holds the
// per-250ms interval samples, the detected room-tone/speech regions and scored
// candidates, the extracted noise profile, and the elected speech profile.
type RegionMetrics struct {
	RoomToneRegions    []RoomToneRegion           `json:"room_tone_regions,omitempty"`    // Detected room tone regions
	IntervalSamples    []IntervalSample           `json:"interval_samples,omitempty"`     // Per-interval measurements
	RoomToneCandidates []RoomToneCandidateMetrics `json:"room_tone_candidates,omitempty"` // All evaluated candidates with scores
	SpeechRegions      []SpeechRegion             `json:"speech_regions,omitempty"`       // Detected speech regions
	SpeechCandidates   []SpeechCandidateMetrics   `json:"speech_candidates,omitempty"`    // All evaluated candidates with scores
	SpeechProfile      *SpeechCandidateMetrics    `json:"speech_profile,omitempty"`       // Elected best speech candidate (pointer into SpeechCandidates)
	NoiseProfile       *NoiseProfile              `json:"noise_profile,omitempty"`        // Metrics from elected room tone region; nil if extraction failed

	// ElectedRoomToneSample is the embedded RegionSample of the elected room-tone
	// candidate (the candidate whose region matches NoiseProfile). NoiseProfile is a
	// slimmer struct without a RegionSample, so the record cannot reach the elected
	// candidate's bare amplitude/spectral/loudness sample through it. This captures
	// that already-computed sample at election (no new measurement, no DSP change) so
	// the run record can wire regions.room_tone.samples.input. json:"-" keeps it out
	// of the flat RegionMetrics marshalling; only the record assembly reads it.
	ElectedRoomToneSample *RegionSample `json:"-"`
}

// BaseMeasurements contains fields shared between input (Pass 1) and output (Pass 2) measurements.
// Embedded in OutputMeasurements; AudioMeasurements carries the equivalent data
// in nested domain blocks (Loudness/Dynamics/Noise/Regions), and the output side
// uses the shared LoudnessMetrics/DynamicsMetrics types.
type BaseMeasurements struct {
	// Spectral analysis from aspectralstats (all measurements averaged across frames)
	Spectral SpectralMetrics `json:"-"` // Kept flat in JSON by custom marshal helpers

	// Time-domain statistics from astats
	DynamicRange float64 `json:"dynamic_range"` // Measured dynamic range (dB)
	RMSLevel     float64 `json:"rms_level"`     // Overall RMS level (dBFS)
	PeakLevel    float64 `json:"peak_level"`    // Overall peak level (dBFS)
	RMSTrough    float64 `json:"rms_trough"`    // RMS level of quietest segments (dBFS)
	RMSPeak      float64 `json:"rms_peak"`      // RMS level of loudest segments (dBFS)

	// Additional astats measurements
	DCOffset          float64 `json:"dc_offset"`           // Mean amplitude displacement from zero
	FlatFactor        float64 `json:"flat_factor"`         // Consecutive samples at peak (clipping indicator)
	CrestFactor       float64 `json:"crest_factor"`        // Peak-to-RMS ratio in dB (converted from linear)
	ZeroCrossingsRate float64 `json:"zero_crossings_rate"` // Zero crossing rate (low=bass, high=noise/sibilance)
	ZeroCrossings     float64 `json:"zero_crossings"`      // Total zero crossings
	MaxDifference     float64 `json:"max_difference"`      // Largest sample-to-sample change (clicks/pops indicator)
	MinDifference     float64 `json:"min_difference"`      // Smallest sample-to-sample change
	MeanDifference    float64 `json:"mean_difference"`     // Average sample-to-sample change
	RMSDifference     float64 `json:"rms_difference"`      // RMS of sample-to-sample changes
	Entropy           float64 `json:"entropy"`             // Signal randomness (1.0 = white noise, lower = structured)
	MinLevel          float64 `json:"min_level"`           // dBFS, minimum sample level (converted from linear)
	MaxLevel          float64 `json:"max_level"`           // dBFS, maximum sample level (converted from linear)
	AstatsNoiseFloor  float64 `json:"astats_noise_floor"`  // FFmpeg astats noise floor estimate (dBFS)
	NoiseFloorCount   float64 `json:"noise_floor_count"`   // Number of samples in noise floor measurement
	BitDepth          float64 `json:"bit_depth"`           // Effective bit depth
	NumberOfSamples   float64 `json:"number_of_samples"`   // Total samples processed

	// ebur128 momentary/short-term loudness
	MomentaryLoudness float64 `json:"momentary_loudness"`  // Momentary loudness (400ms window, LUFS)
	ShortTermLoudness float64 `json:"short_term_loudness"` // Short-term loudness (3s window, LUFS)
	SamplePeak        float64 `json:"sample_peak"`         // Sample peak (dBFS)
}

// Stage identifies one of the three measurement snapshots the pipeline produces
// of the same regions/loudness/dynamics/spectral blocks. The stage value keys the
// per-stage maps assembled in RunRecord.
type Stage string

const (
	StageInput    Stage = "input"    // Pass 1, raw input measurements
	StageFiltered Stage = "filtered" // Pass 2 output, post filter-chain, pre-normalisation
	StageFinal    Stage = "final"    // Pass 4 output, post-loudnorm
)

// AudioMeasurements contains the measurements from Pass 1 analysis.
// Uses ebur128 (LUFS/LRA), astats (dynamic range/noise floor), and aspectralstats (spectral analysis).
// Room tone detection runs in Go using 250ms interval sampling rather than an FFmpeg silence filter.
type AudioMeasurements struct {
	// Spectral analysis from aspectralstats (whole-file averaged). Kept directly
	// accessible at measurements.Spectral so DSP/adaptive reads (m.Spectral.Flux,
	// .Kurtosis, ...) keep resolving; the spectral.stages grouping is assembled at
	// the RunRecord level.
	Spectral SpectralMetrics `json:"-"`

	// Domain blocks (8.1). Loudness/Dynamics use the shared metric types; Noise and
	// Regions are input-only.
	Loudness InputLoudnessMetrics `json:"loudness"`
	Dynamics DynamicsMetrics      `json:"dynamics"`
	Noise    NoiseMetrics         `json:"noise"`
	Regions  RegionMetrics        `json:"regions"`

	// Duration is the total audio length in seconds, captured at file open. It is
	// in-memory UI plumbing only and excluded from the report JSON contract.
	Duration float64 `json:"-"`

	// SuggestedGateThreshold is an early gate-threshold estimate (linear amplitude).
	// Not part of the §8.1 domain skeleton (display-only "Gate Baseline"); excluded
	// from the record JSON.
	SuggestedGateThreshold float64 `json:"-"`
}

// OutputLoudnessMetrics is the Filtered/Final-stage loudness domain block: the
// shared windowed loudness (embedded) plus the output-specific
// integrated/true-peak/LRA/threshold and the pre-limiter target offset from the
// loudnorm measurement. Mirrors InputLoudnessMetrics for the output side (8.1).
//
// The output TargetOffset is the pre-limiter offset loudnorm reports during Pass
// 2/3 measurement; it is DISTINCT from the Pass-1 InputLoudnessMetrics.TargetOffset
// (the a-priori config.TargetI - InputI gap) and must not be merged (8.2).
type OutputLoudnessMetrics struct {
	LoudnessMetrics // shared windowed loudness, promoted flat into this stage block

	OutputI      float64 `json:"integrated_lufs"`  // Integrated loudness (LUFS)
	OutputTP     float64 `json:"true_peak_dbtp"`   // True peak (dBTP)
	OutputLRA    float64 `json:"lra_lu"`           // Loudness range (LU)
	OutputThresh float64 `json:"thresh_lufs"`      // Gating threshold (LUFS) - for loudnorm
	TargetOffset float64 `json:"target_offset_db"` // Pre-limiter offset (dB) - from loudnorm measurement
}

// OutputLoudnormMeasurement groups the loudnorm first-pass (measurement mode)
// values captured during Pass 2/3. Kept together for the later §8.1 normalisation
// block. Distinct from normalise.go's LoudnormMeasurement (parsed Pass-3 floats).
type OutputLoudnormMeasurement struct {
	LoudnormInputI       float64 `json:"input_integrated_lufs"` // Loudnorm's measured integrated loudness (LUFS)
	LoudnormInputTP      float64 `json:"input_true_peak_dbtp"`  // Loudnorm's measured true peak (dBTP)
	LoudnormInputLRA     float64 `json:"input_lra_lu"`          // Loudnorm's measured loudness range (LU)
	LoudnormInputThresh  float64 `json:"input_thresh_lufs"`     // Loudnorm's measured threshold (LUFS)
	LoudnormTargetOffset float64 `json:"target_offset_db"`      // Loudnorm's calculated offset for second pass
	LoudnormMeasured     bool    `json:"measured"`              // True if loudnorm measurement was captured
}

// OutputMeasurements contains the measurements from Pass 2 (Filtered) / Pass 4
// (Final) output analysis. Mirrors the AudioMeasurements domain grouping: shared
// LoudnessMetrics/DynamicsMetrics types plus a directly-accessible Spectral, with
// the loudnorm-measurement fields grouped for the §8.1 normalisation block.
// Does not include room tone detection or noise profile fields (those are input-only).
type OutputMeasurements struct {
	// Spectral analysis from aspectralstats (whole-file averaged). Kept directly
	// accessible at FilteredMeasurements.Spectral; the spectral.stages grouping is
	// assembled at the RunRecord level.
	Spectral SpectralMetrics `json:"-"`

	// Domain blocks (8.1). Loudness/Dynamics use the shared metric types.
	Loudness OutputLoudnessMetrics `json:"loudness"`
	Dynamics DynamicsMetrics       `json:"dynamics"`

	// Loudnorm measurement from Pass 2 analysis chain, grouped for the §8.1
	// normalisation block. These come from loudnorm's first pass (measurement mode,
	// without linear=true) and feed the application pass in Pass 3.
	Loudnorm OutputLoudnormMeasurement `json:"loudnorm"`

	// Room tone region analysis (same region as Pass 1, for noise reduction comparison).
	// Bare RegionSample: the output re-measure only sets amplitude/spectral/loudness, so
	// scoring/stability/band fields are structurally absent (no stale-zero values).
	RoomToneSample *RegionSample `json:"room_tone_sample,omitempty"` // Measurements from same room tone region

	// Speech region analysis (same region as Pass 1, for processing comparison).
	// Bare RegionSample: see RoomToneSample.
	SpeechSample *RegionSample `json:"speech_sample,omitempty"` // Measurements from same speech region
}

// AnalyseAudio performs Pass 1: ebur128 + astats + aspectralstats analysis to get measurements
// This is required for adaptive processing in Pass 2.
//
// Implementation note: ebur128 and astats write measurements to frame metadata with lavfi.r128.*
// and lavfi.astats.Overall.* keys respectively. We extract these from the last processed frames.
//
// The noise floor and silence threshold are computed from interval data after the full pass,
// avoiding the need for a separate pre-scan phase.
func AnalyseAudio(ctx stdcontext.Context, filename string, config *BaseFilterConfig, progressCallback ProgressCallback) (*AudioMeasurements, error) {
	// Default fallback threshold if interval analysis yields insufficient data
	const defaultNoiseFloor = -50.0

	analysisContext := &ProcessingFilterContext{Pass: PassAnalysis}
	collection, err := collectAnalysisFrames(ctx, filename, config, analysisContext, progressCallback)
	if err != nil {
		return nil, err
	}

	intervals := collection.intervals
	silenceIntervals := collection.silenceIntervals
	silMedians := collection.silenceMedians

	measurements, err := buildInputMeasurements(filename, collection, config, defaultNoiseFloor)
	if err != nil {
		return nil, err
	}

	noiseSelection := selectNoiseProfile(measurements, intervals, silenceIntervals, silMedians, config.logger)
	selectSpeechProfile(measurements, intervals, noiseSelection, config.logger)

	// Measure body/sibilant band RMS over the elected speech region for the
	// de-esser engagement signal. Region-scoped second decode (no asplit/multi-sink
	// support in the analysis graph); non-fatal on failure.
	measureSpeechBands(ctx, filename, measurements, config.logger)

	assignInputMeasurementSuggestions(measurements)

	return measurements, nil
}

type noiseProfileSelection struct {
	roomToneResult *findBestRoomToneRegionResult
	noiseProfile   *NoiseProfile
}

func selectNoiseProfile(measurements *AudioMeasurements, intervals, silenceIntervals []IntervalSample, silMedians silenceMedians, log debugLogger) noiseProfileSelection {
	measurements.Regions.RoomToneRegions = findRoomToneCandidatesFromIntervals(silenceIntervals, measurements.Noise.RoomToneDetectLevel, silMedians)

	roomToneResult := findBestRoomToneRegion(measurements.Regions.RoomToneRegions, silenceIntervals, log)
	measurements.Regions.RoomToneCandidates = roomToneResult.Candidates
	measurements.Noise.VoiceActivated = detectVoiceActivated(roomToneResult.Candidates)

	selection := noiseProfileSelection{roomToneResult: roomToneResult}
	if roomToneResult.BestRegion == nil {
		return selection
	}

	originalRegion := roomToneResult.BestRegion
	refinedRegion := refineToGoldenSubregion(originalRegion, intervals)
	wasRefined := refinedRegion.Start != originalRegion.Start || refinedRegion.Duration != originalRegion.Duration

	profile := extractNoiseProfileFromIntervals(refinedRegion, silenceIntervals)
	if profile == nil {
		return selection
	}

	selection.noiseProfile = profile
	measurements.Regions.NoiseProfile = profile

	// Capture the elected candidate's already-computed RegionSample for the run
	// record (regions.room_tone.samples.input). The elected candidate is the one in
	// Candidates whose Region matches the elected BestRegion (the same region the
	// NoiseProfile is built from). Pure read of an existing measurement; no scoring,
	// election, or DSP value changes - audio stays bit-exact.
	for i := range roomToneResult.Candidates {
		c := &roomToneResult.Candidates[i]
		if c.Region.Start == originalRegion.Start && c.Region.Duration == originalRegion.Duration {
			measurements.Regions.ElectedRoomToneSample = &c.RegionSample
			break
		}
	}

	if wasRefined {
		profile.WasRefined = true
		profile.OriginalStart = originalRegion.Start
		profile.OriginalDuration = originalRegion.Duration
	}

	if profile.MeasuredNoiseFloor != 0 && !math.IsInf(profile.MeasuredNoiseFloor, -1) {
		measurements.Noise.Floor = profile.MeasuredNoiseFloor
		measurements.Noise.FloorSource = "silence_profile"
	}

	return selection
}

func selectSpeechProfile(measurements *AudioMeasurements, intervals []IntervalSample, noiseSelection noiseProfileSelection, log debugLogger) {
	speechSearchStart := 30 * time.Second
	switch {
	case noiseSelection.roomToneResult != nil && noiseSelection.roomToneResult.BestRegion != nil:
		speechSearchStart = noiseSelection.roomToneResult.BestRegion.End
	case len(measurements.Regions.RoomToneRegions) > 0:
		speechSearchStart = measurements.Regions.RoomToneRegions[0].End
	}

	measurements.Regions.SpeechRegions = findSpeechCandidatesFromIntervals(intervals, speechSearchStart, measurements.Noise.VoiceActivated, measurements.Dynamics.RMSLevel, measurements.Noise.Floor)

	speechResult := findBestSpeechRegion(measurements.Regions.SpeechRegions, intervals, noiseSelection.noiseProfile, log)
	measurements.Regions.SpeechCandidates = speechResult.Candidates

	if speechResult.BestRegion == nil {
		return
	}

	for i := range speechResult.Candidates {
		if speechResult.Candidates[i].Region.Start == speechResult.BestRegion.Start {
			measurements.Regions.SpeechProfile = &speechResult.Candidates[i]
			return
		}
	}
}

func buildInputMeasurements(filename string, collection *analysisFrameCollection, config *BaseFilterConfig, defaultNoiseFloor float64) (*AudioMeasurements, error) {
	acc := collection.accumulators

	noiseFloorEstimate, silenceThreshold, ok := estimateNoiseFloorAndThreshold(collection.silenceIntervals, collection.silenceMedians)
	if !ok {
		noiseFloorEstimate = defaultNoiseFloor
		silenceThreshold = calculateAdaptiveSilenceThreshold(defaultNoiseFloor)
	}

	measurements := &AudioMeasurements{
		Duration: collection.totalDuration,
	}
	measurements.Noise.FloorPrescan = noiseFloorEstimate
	measurements.Noise.RoomToneDetectLevel = silenceThreshold
	measurements.Regions.IntervalSamples = collection.intervals

	if !acc.ebur128Found {
		return nil, fmt.Errorf("ebur128 measurements not found in metadata for file: %s", filename)
	}

	measurements.Loudness.InputI = acc.ebur128InputI
	measurements.Loudness.InputTP = acc.ebur128InputTP
	measurements.Loudness.InputLRA = acc.ebur128InputLRA
	measurements.Loudness.InputThresh = acc.ebur128InputI - 10.0
	measurements.Loudness.TargetOffset = config.Loudnorm.TargetI - acc.ebur128InputI
	measurements.Loudness.MomentaryLoudness = acc.ebur128InputM
	measurements.Loudness.ShortTermLoudness = acc.ebur128InputS
	measurements.Loudness.SamplePeak = acc.ebur128InputSP

	measurements.Spectral = acc.finalizeSpectral()
	assignAstatsMeasurements(measurements, acc)
	assignInputNoiseFloor(measurements, acc)

	return measurements, nil
}

func assignAstatsMeasurements(measurements *AudioMeasurements, acc *metadataAccumulators) {
	if !acc.astatsFound {
		return
	}

	measurements.Dynamics.DynamicRange = acc.astatsDynamicRange
	measurements.Dynamics.RMSLevel = acc.astatsRMSLevel
	measurements.Dynamics.PeakLevel = acc.astatsPeakLevel
	measurements.Dynamics.RMSTrough = acc.astatsRMSTrough
	measurements.Dynamics.RMSPeak = acc.astatsRMSPeak
	measurements.Dynamics.DCOffset = acc.astatsDCOffset
	measurements.Dynamics.FlatFactor = acc.astatsFlatFactor
	measurements.Dynamics.CrestFactor = acc.astatsCrestFactor
	measurements.Dynamics.ZeroCrossingsRate = acc.astatsZeroCrossingsRate
	measurements.Dynamics.ZeroCrossings = acc.astatsZeroCrossings
	measurements.Dynamics.MaxDifference = acc.astatsMaxDifference
	measurements.Dynamics.MinDifference = acc.astatsMinDifference
	measurements.Dynamics.MeanDifference = acc.astatsMeanDifference
	measurements.Dynamics.RMSDifference = acc.astatsRMSDifference
	measurements.Dynamics.Entropy = acc.astatsEntropy
	measurements.Dynamics.MinLevel = acc.astatsMinLevel
	measurements.Dynamics.MaxLevel = acc.astatsMaxLevel
	measurements.Noise.FloorAstats = acc.astatsNoiseFloor
	measurements.Dynamics.NoiseFloorCount = acc.astatsNoiseFloorCount
	measurements.Dynamics.BitDepth = acc.astatsBitDepth
	measurements.Dynamics.NumberOfSamples = acc.astatsNumberOfSamples
}

func assignInputNoiseFloor(measurements *AudioMeasurements, acc *metadataAccumulators) {
	switch {
	case acc.astatsRMSTrough != 0 && !math.IsInf(acc.astatsRMSTrough, -1):
		measurements.Noise.Floor = acc.astatsRMSTrough
		measurements.Noise.FloorSource = "astats"
	case acc.astatsRMSLevel != 0 && !math.IsInf(acc.astatsRMSLevel, -1):
		measurements.Noise.Floor = acc.astatsRMSLevel - 15.0
		measurements.Noise.FloorSource = "rms_estimate"
	default:
		var noiseFloorOffset float64
		switch {
		case measurements.Loudness.InputI > -20:
			noiseFloorOffset = 18.0
		case measurements.Loudness.InputI > -30:
			noiseFloorOffset = 12.0
		default:
			noiseFloorOffset = 8.0
		}
		measurements.Noise.Floor = measurements.Loudness.InputThresh - noiseFloorOffset
		measurements.Noise.FloorSource = "ebur128_estimate"
	}

	if measurements.Noise.Floor < -90.0 {
		measurements.Noise.Floor = -90.0
	} else if measurements.Noise.Floor > -30.0 {
		measurements.Noise.Floor = -30.0
	}
}

func assignInputMeasurementSuggestions(measurements *AudioMeasurements) {
	gateThresholdDB := calculateAdaptiveDS201GateThreshold(measurements.Noise.Floor, measurements.Dynamics.RMSTrough)
	measurements.SuggestedGateThreshold = math.Pow(10, gateThresholdDB/20.0)

	if measurements.Dynamics.RMSLevel != 0 && measurements.Noise.Floor != 0 {
		measurements.Noise.ReductionHeadroom = measurements.Dynamics.RMSLevel - measurements.Noise.Floor
		if measurements.Noise.ReductionHeadroom < 0 {
			measurements.Noise.ReductionHeadroom = 0
		}
		if measurements.Noise.ReductionHeadroom > 60 {
			measurements.Noise.ReductionHeadroom = 60
		}
		return
	}

	switch {
	case measurements.Loudness.InputI > -20:
		measurements.Noise.ReductionHeadroom = 40.0
	case measurements.Loudness.InputI > -30:
		measurements.Noise.ReductionHeadroom = 25.0
	default:
		measurements.Noise.ReductionHeadroom = 15.0
	}
}

type analysisFrameCollection struct {
	accumulators     *metadataAccumulators
	intervals        []IntervalSample
	silenceIntervals []IntervalSample
	silenceMedians   silenceMedians
	totalDuration    float64 // total audio length, seconds (from input metadata)
}

func collectAnalysisFrames(ctx stdcontext.Context, filename string, config *BaseFilterConfig, context *ProcessingFilterContext, progressCallback ProgressCallback) (*analysisFrameCollection, error) {
	reader, metadata, err := audio.OpenAudioFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer reader.Close()

	totalDuration := metadata.Duration
	sampleRate := float64(metadata.SampleRate)
	samplesPerFrame := 4096.0
	estimatedTotalFrames := (totalDuration * sampleRate) / samplesPerFrame

	filterGraph, bufferSrcCtx, bufferSinkCtx, err := createAnalysisFilterGraph(
		reader.GetDecoderContext(),
		config,
		context,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create filter graph: %w", err)
	}
	var filterFreed bool
	defer func() {
		if !filterFreed && filterGraph != nil {
			ffmpeg.AVFilterGraphFree(&filterGraph)
		}
	}()

	frameCount := 0
	updateInterval := 100
	currentLevel := 0.0

	acc := &metadataAccumulators{}

	const intervalDuration = 250 * time.Millisecond
	var intervals []IntervalSample
	var silenceIntervals []IntervalSample
	var intervalAcc intervalAccumulator
	var intervalStartTime time.Duration

	var inputSamplesProcessed int64
	inputSampleRate := float64(reader.GetDecoderContext().SampleRate())

	if err := runFilterGraph(ctx, reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnReadError: func(err error) error {
			return fmt.Errorf("failed to read frame: %w", err)
		},
		OnPushError: func(err error) error {
			return fmt.Errorf("failed to add frame to filter: %w", err)
		},
		OnPullError: func(err error) error {
			return fmt.Errorf("failed to get filtered frame: %w", err)
		},
		OnInputFrame: func(inputFrame *ffmpeg.AVFrame) {
			currentLevel = calculateFrameLevel(inputFrame)

			inputFrameTime := time.Duration(float64(inputSamplesProcessed) / inputSampleRate * float64(time.Second))
			inputSamplesProcessed += int64(inputFrame.NbSamples())
			intervalAcc.addFrameRMSAndPeak(inputFrame)

			if inputFrameTime-intervalStartTime >= intervalDuration {
				finalised := intervalAcc.finalize(intervalStartTime)
				intervals = append(intervals, finalised)
				if config.Analysis.RoomToneScanDuration > 0 && intervalStartTime < config.Analysis.RoomToneScanDuration {
					silenceIntervals = append(silenceIntervals, finalised)
				}
				intervalStartTime = inputFrameTime
				intervalAcc.reset()
			}

			if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
				progress := float64(frameCount) / estimatedTotalFrames
				if progress > 1.0 {
					progress = 1.0
				}
				progressCallback(ProgressUpdate{
					Pass:     context.Pass,
					PassName: "Analysing",
					Progress: progress,
					Level:    currentLevel,
					Duration: totalDuration,
				})
			}
			frameCount++
		},
		OnFrame: func(_, filteredFrame *ffmpeg.AVFrame) error {
			metadata := filteredFrame.Metadata()
			spectral := extractSpectralMetrics(metadata)
			loudness := extractFrameLoudnessMetrics(metadata)

			extractFrameMetadata(metadata, acc, spectral, loudness)
			intervalAcc.add(extractIntervalFrameMetrics(spectral, loudness))

			return nil
		},
	}); err != nil {
		return nil, err
	}

	if intervalAcc.rawSampleCount > 0 {
		finalised := intervalAcc.finalize(intervalStartTime)
		intervals = append(intervals, finalised)
		if config.Analysis.RoomToneScanDuration > 0 && intervalStartTime < config.Analysis.RoomToneScanDuration {
			silenceIntervals = append(silenceIntervals, finalised)
		}
	}

	if config.Analysis.RoomToneScanDuration == 0 {
		silenceIntervals = intervals
	}

	ffmpeg.AVFilterGraphFree(&filterGraph)
	filterFreed = true

	return &analysisFrameCollection{
		accumulators:     acc,
		intervals:        intervals,
		silenceIntervals: silenceIntervals,
		silenceMedians:   computeSilenceMedians(silenceIntervals),
		totalDuration:    totalDuration,
	}, nil
}

// calculateAdaptiveDS201GateThreshold computes a data-driven gate threshold based on
// the measured noise floor and RMS trough (quiet speech indicator).
//
// Strategy:
//   - The gate threshold should be above the noise floor but below quiet speech
//   - RMSTrough represents the quietest RMS segments (breaths, quiet consonants)
//   - We place the threshold at a data-driven position between noise and quiet speech
//
// Calculation:
//   - Gap = RMSTrough - NoiseFloor (how much "room" between noise and speech)
//   - If gap is small (<10dB): recording is noisy, threshold at 30% into gap
//   - If gap is moderate (10-20dB): typical, threshold at 40% into gap
//   - If gap is large (>20dB): clean recording, threshold at 50% into gap
//
// Safety bounds:
//   - Never below noise floor (would gate during silence)
//   - Never above -35dBFS (would cut quiet speech)
func calculateAdaptiveDS201GateThreshold(noiseFloor, rmsTrough float64) float64 {
	// If RMSTrough is unavailable or invalid, use a sensible fallback
	if rmsTrough == 0 || rmsTrough <= noiseFloor {
		// Fallback: 6dB above noise floor (conservative default)
		threshold := noiseFloor + 6.0
		if threshold > -35.0 {
			threshold = -35.0
		}
		return threshold
	}

	// Calculate the gap between quiet speech and noise
	gap := rmsTrough - noiseFloor

	// Determine the adaptive offset percentage based on gap size
	var offsetPercent float64
	switch {
	case gap < 10.0:
		// Noisy recording: small gap, be conservative (30% into gap)
		// This preserves more speech at the cost of some noise bleed
		offsetPercent = 0.30
	case gap < 20.0:
		// Typical recording: moderate gap (40% into gap)
		offsetPercent = 0.40
	default:
		// Clean recording: large gap, more aggressive (50% into gap)
		offsetPercent = 0.50
	}

	// Calculate threshold: noise floor + (gap * percentage)
	threshold := noiseFloor + (gap * offsetPercent)

	// Safety bounds
	if threshold < noiseFloor+3.0 {
		// Always at least 3dB above noise floor
		threshold = noiseFloor + 3.0
	}
	if threshold > -35.0 {
		// Never gate above -35dBFS (would cut quiet speech)
		threshold = -35.0
	}

	return threshold
}

// createAnalysisFilterGraph creates an AVFilterGraph for Pass 1 analysis.
// Uses astats, aspectralstats, and ebur128 filters to extract measurements.
// Silence detection runs in Go using 250ms interval sampling, not in this graph.
func createAnalysisFilterGraph(
	decCtx *ffmpeg.AVCodecContext,
	config *BaseFilterConfig,
	context *ProcessingFilterContext,
) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
	if context == nil {
		context = &ProcessingFilterContext{Pass: PassAnalysis}
	}
	if context.Pass == 0 {
		context.Pass = PassAnalysis
	}

	analysisConfig := deriveEffectiveFilterConfig(config)
	analysisConfig.FilterOrder = cloneFilterOrder(Pass1FilterOrder)

	return setupFilterGraph(decCtx, analysisConfig.BuildFilterSpec())
}
