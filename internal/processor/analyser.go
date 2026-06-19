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
//   - MeasuredNoiseFloor → elected noise floor (Noise.Floor), which drives the
//     VAD split, the Recording-score cleanliness axis, and the afftdn nf seed.
//   - CrestFactor/PeakLevel → peak-reference input to the legacy speech gate
//     threshold path (calculateSpeechGateThresholdLegacy via adaptive_speech_gate.go),
//     reached only when no SpeechProfile is elected.
//
// Entropy and the spectral fields are measured here for the report and
// contamination detection; the current adaptive gate uses fixed release and
// range, so it does not key either on room-tone entropy or spectral metrics.
//
// Note: The room tone region is also re-measured in Pass 2 and Pass 4 for
// before/after comparison of noise reduction effectiveness.
type NoiseProfile struct {
	Start              time.Duration `json:"start"`                        // Start time of room tone region used (time.Duration ns)
	Duration           time.Duration `json:"duration"`                     // Duration of extracted sample (time.Duration ns)
	MeasuredNoiseFloor float64       `json:"measured_floor_dbfs"`          // Elected noise floor; seeded as astats RMS, overwritten by detectVoiceActivity with the momentary-LUFS percentile floor
	PeakLevel          float64       `json:"peak_level_dbfs"`              // dBFS, peak level in room tone (transient noise indicator)
	CrestFactor        float64       `json:"crest_factor_db"`              // Peak - RMS in dB (high = impulsive noise, low = steady noise)
	Entropy            float64       `json:"entropy"`                      // Signal randomness (1.0 = white noise, lower = tonal noise like hum)
	ExtractionWarning  string        `json:"extraction_warning,omitempty"` // Warning message if extraction had issues

	// Spectral characteristics for contamination detection (added during candidate
	// evaluation). The full 13-metric aspectralstats set averaged over the elected
	// room-tone region, mirroring RegionSample's Spectral embed. The custom
	// NoiseProfile.MarshalJSON flattens this to the spectral_* tags (json:"-" keeps
	// the default marshal from emitting the SpectralMetrics own tags). The astats
	// Entropy field above stays DISTINCT from Spectral.Entropy (different axis).
	Spectral SpectralMetrics `json:"-"`

	// Room-tone band noise: per-band RMS (dBFS) over the elected room-tone region,
	// one value per afftdn fixed band (see afftdnBandCentresHz). Feeds the measured
	// custom afftdn noise profile (bn) in tuneNoiseReduction. BandNoise is the raw
	// per-band RMS; BandsMeasured mirrors the SpeechProfile convention and is true
	// only when every band measured successfully.
	BandNoise     []float64 `json:"band_noise_dbfs,omitempty"`     // Per-band RMS (dBFS) across the afftdn fixed bands
	BandsMeasured bool      `json:"band_noise_measured,omitempty"` // True only when all afftdn bands measured successfully

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
	Floor               float64 `json:"floor_dbfs"`                  // Elected noise floor; under the VAD it is the momentary-LUFS p10 (vad_percentile source), so the value is on the momentary-LUFS axis
	FloorSource         string  `json:"floor_source"`                // Source of Floor: "astats" / "rms_estimate" / "ebur128_estimate" / "vad_percentile"
	FloorPrescan        float64 `json:"floor_prescan_dbfs"`          // Pre-scan noise floor seed estimated from interval data, on the momentary-LUFS axis (anchors the VAD split clamp)
	FloorAstats         float64 `json:"floor_astats_dbfs"`           // FFmpeg astats noise floor estimate (dBFS)
	RoomToneDetectLevel float64 `json:"room_tone_detect_level_dbfs"` // Adaptive room tone detection threshold, derived from the momentary-LUFS-axis seed
	VoiceActivated      bool    `json:"voice_activated"`             // True when the floored (digital-silence) interval fraction is high (platform-gated capture signature)
	ReductionHeadroom   float64 `json:"reduction_headroom_db"`       // dB gap between noise and quiet speech
}

// RegionMetrics is the input-only regions domain block (8.1). It holds the
// per-250ms interval samples, the detected speech regions and scored speech
// candidates, the extracted noise profile, and the elected speech profile.
type RegionMetrics struct {
	IntervalSamples  []IntervalSample         `json:"interval_samples,omitempty"`  // Per-interval measurements
	SpeechRegions    []SpeechRegion           `json:"speech_regions,omitempty"`    // Detected speech regions
	SpeechCandidates []SpeechCandidateMetrics `json:"speech_candidates,omitempty"` // All evaluated candidates with scores
	SpeechProfile    *SpeechCandidateMetrics  `json:"speech_profile,omitempty"`    // Elected best speech candidate (pointer into SpeechCandidates)
	NoiseProfile     *NoiseProfile            `json:"noise_profile,omitempty"`     // Metrics from elected room tone region; nil if extraction failed

	// Gate statistics on the VAD level axis (dBFS-relative momentary LUFS). These
	// anchor the speech-gate threshold and depth in Phase 4; written from the
	// elected region's voiced and noise interval populations during Pass 1.
	VoicedLowPercentile float64 `json:"voiced_low_percentile_dbfs"` // Voiced-speech low percentile (p10) over in-region intervals at or above the clamped Otsu split passing the spectral veto (dBFS-relative momentary LUFS)
	NoiseHighPercentile float64 `json:"noise_high_percentile_dbfs"` // Noise high percentile (p95) over below-split intervals (dBFS-relative momentary LUFS)
	GateSeparationDB    float64 `json:"gate_separation_db"`         // Separation between VoicedLowPercentile and NoiseHighPercentile (dB)

	// ElectedRoomToneSample is the RegionSample measured from the elected room-tone
	// (low-cluster) region. NoiseProfile is a slimmer struct without a RegionSample,
	// so the record cannot reach the elected region's bare amplitude/spectral/loudness
	// sample through it. This captures that already-computed sample at election (no
	// new measurement, no DSP change) so the run record can wire
	// regions.room_tone.samples.input. json:"-" keeps it out of the flat
	// RegionMetrics marshalling; only the record assembly reads it.
	ElectedRoomToneSample *RegionSample `json:"-"`
}

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
	collection, err := collectAnalysisFrames(ctx, filename, config, PassAnalysis, progressCallback)
	if err != nil {
		return nil, err
	}

	intervals := collection.intervals

	measurements, err := buildInputMeasurements(filename, collection, config)
	if err != nil {
		return nil, err
	}

	// Unified Pass 1 voice-activity detector: one bimodal split feeds both the
	// elected SpeechProfile and the NoiseProfile / Noise.Floor. The pre-scan floor
	// anchors the split clamp; the hop and axis are the single configurable choices.
	// It must finish before either band function runs, because it elects the
	// speech and room-tone regions that both band functions go on to measure.
	detectVoiceActivity(measurements, intervals, measurements.Noise.FloorPrescan, analysisIntervalHop, axisMomentaryLUFS, config.logger)

	// Post-loop band phase: the main decode loop is capped at BandPhaseProgressStart
	// (0.95); the two band functions drive 0.95..1.0 by reporting each completed
	// band decode through one shared tracker (atomic counter, monotonic, clamped to
	// 1.0). The total is the combined speech + noise band budget, so a band function
	// that early-returns still drains its share via drainBandProgress and the phase
	// reaches 1.0. The functions run sequentially (speech then noise) but each fans
	// its own bands across cores under the shared semaphore.
	bandTotal := len(speechBandPlan) + len(afftdnBandCentresHz)
	tracker := newBandProgressTracker(progressCallback, measurements.Duration, bandTotal)

	// Measure body/sibilant band RMS over the elected speech region for the
	// de-esser engagement signal. Region-scoped decode (no asplit/multi-sink
	// support in the analysis graph); non-fatal on failure.
	measureSpeechBands(ctx, filename, measurements, tracker.report, config.logger)

	// Measure the 15-band room-tone spectrum for the measured custom afftdn noise
	// profile (nt=custom:bn=...). Region-scoped, non-fatal on failure (the
	// white-noise afftdn path stands in when bands are unavailable).
	measureNoiseBands(ctx, filename, measurements, tracker.report, config.logger)

	assignInputMeasurementSuggestions(measurements)

	return measurements, nil
}

func buildInputMeasurements(filename string, collection *analysisFrameCollection, config *BaseFilterConfig) (*AudioMeasurements, error) {
	acc := collection.accumulators

	noiseFloorEstimate, silenceThreshold, ok := estimateNoiseFloorAndThreshold(collection.silenceIntervals, collection.silenceMedians)
	if !ok {
		// No measurable room tone (fully gated / voice-activated capture): seed the
		// detector with the low vadLevelFloorDB sentinel, not defaultNoiseFloor. A
		// -50 seed would raise the split clamp lower bound (to about -44) and the
		// percentileFloor anchor, collapsing the speech/noise separation on these
		// captures. The sentinel keeps clampSplit's lower bound and percentileFloor's
		// anchor inert, so Otsu places the split alone and the reported Noise.Floor
		// falls to the honest momentary p10. RoomToneDetectLevel derives from the
		// same seed (clamped to silenceMinThreshold), so it stays sensible.
		noiseFloorEstimate = vadLevelFloorDB
		silenceThreshold = calculateAdaptiveSilenceThreshold(vadLevelFloorDB)
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

// Noise-floor fallback estimator anchors (dB). These tune ONLY the fallback
// estimators that run when astats provides no usable trough; the normal path
// overwrites Noise.Floor with the VAD percentile floor.
const (
	// noiseFloorRMSEstimateOffsetDB drops below the overall RMS level to guess a
	// floor when only an RMS level (no trough) is available.
	noiseFloorRMSEstimateOffsetDB = 15.0
	// noiseFloorThreshOffsetLoudDB / Mid / QuietDB sit below the ebur128 input
	// threshold to guess a floor; a louder capture sits further above its floor.
	noiseFloorThreshOffsetLoudDB  = 18.0
	noiseFloorThreshOffsetMidDB   = 12.0
	noiseFloorThreshOffsetQuietDB = 8.0
	// noiseFloorClampMinDB / MaxDB bound the estimated floor.
	noiseFloorClampMinDB = -90.0
	noiseFloorClampMaxDB = -30.0
)

// Reduction-headroom fallback tiers (dB), used when no measured RMS/floor pair
// is available.
const (
	reductionHeadroomLoudDB  = 40.0
	reductionHeadroomMidDB   = 25.0
	reductionHeadroomQuietDB = 15.0
)

// loudnessTier classifies a capture by its integrated input loudness against the
// shared -20/-30 LUFS ladder, so the noise-floor and reduction-headroom fallback
// estimators read one ordering instead of two copies.
type loudnessTier int

const (
	loudnessTierLoud  loudnessTier = iota // InputI > -20 LUFS
	loudnessTierMid                       // -30 < InputI <= -20 LUFS
	loudnessTierQuiet                     // InputI <= -30 LUFS
)

const (
	loudnessTierLoudThresholdLUFS = -20.0
	loudnessTierMidThresholdLUFS  = -30.0
)

func classifyLoudnessTier(inputI float64) loudnessTier {
	switch {
	case inputI > loudnessTierLoudThresholdLUFS:
		return loudnessTierLoud
	case inputI > loudnessTierMidThresholdLUFS:
		return loudnessTierMid
	default:
		return loudnessTierQuiet
	}
}

func assignInputNoiseFloor(measurements *AudioMeasurements, acc *metadataAccumulators) {
	switch {
	case acc.astatsRMSTrough != 0 && !math.IsInf(acc.astatsRMSTrough, -1):
		measurements.Noise.Floor = acc.astatsRMSTrough
		measurements.Noise.FloorSource = "astats"
	case acc.astatsRMSLevel != 0 && !math.IsInf(acc.astatsRMSLevel, -1):
		measurements.Noise.Floor = acc.astatsRMSLevel - noiseFloorRMSEstimateOffsetDB
		measurements.Noise.FloorSource = "rms_estimate"
	default:
		var noiseFloorOffset float64
		switch classifyLoudnessTier(measurements.Loudness.InputI) {
		case loudnessTierLoud:
			noiseFloorOffset = noiseFloorThreshOffsetLoudDB
		case loudnessTierMid:
			noiseFloorOffset = noiseFloorThreshOffsetMidDB
		default:
			noiseFloorOffset = noiseFloorThreshOffsetQuietDB
		}
		measurements.Noise.Floor = measurements.Loudness.InputThresh - noiseFloorOffset
		measurements.Noise.FloorSource = "ebur128_estimate"
	}

	measurements.Noise.Floor = max(noiseFloorClampMinDB, min(noiseFloorClampMaxDB, measurements.Noise.Floor))
}

func assignInputMeasurementSuggestions(measurements *AudioMeasurements) {
	if measurements.Dynamics.RMSLevel != 0 && measurements.Noise.Floor != 0 {
		measurements.Noise.ReductionHeadroom = measurements.Dynamics.RMSLevel - measurements.Noise.Floor
		measurements.Noise.ReductionHeadroom = max(0, min(60, measurements.Noise.ReductionHeadroom))
		return
	}

	switch classifyLoudnessTier(measurements.Loudness.InputI) {
	case loudnessTierLoud:
		measurements.Noise.ReductionHeadroom = reductionHeadroomLoudDB
	case loudnessTierMid:
		measurements.Noise.ReductionHeadroom = reductionHeadroomMidDB
	default:
		measurements.Noise.ReductionHeadroom = reductionHeadroomQuietDB
	}
}

type analysisFrameCollection struct {
	accumulators     *metadataAccumulators
	intervals        []IntervalSample
	silenceIntervals []IntervalSample
	silenceMedians   silenceMedians
	totalDuration    float64 // total audio length, seconds (from input metadata)
}

func collectAnalysisFrames(ctx stdcontext.Context, filename string, config *BaseFilterConfig, pass PassNumber, progressCallback ProgressCallback) (*analysisFrameCollection, error) {
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
		reader.DecoderContext(),
		config,
		pass,
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

	var intervals []IntervalSample
	var intervalAcc intervalAccumulator
	var intervalStartTime time.Duration

	var inputSamplesProcessed int64
	inputSampleRate := float64(reader.DecoderContext().SampleRate())

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

			if inputFrameTime-intervalStartTime >= analysisIntervalHop {
				finalised := intervalAcc.finalize(intervalStartTime)
				intervals = append(intervals, finalised)
				intervalStartTime = inputFrameTime
				intervalAcc.reset()
			}

			if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
				// Cap the main-decode-loop progress at BandPhaseProgressStart;
				// the post-loop band phase drives the remaining span to 1.0. Scale
				// the frame ratio by the cap, still clamped, so the bar advances
				// smoothly into the band phase instead of hitting 1.0 then freezing.
				progress := (float64(frameCount) / estimatedTotalFrames) * BandPhaseProgressStart
				if progress > BandPhaseProgressStart {
					progress = BandPhaseProgressStart
				}
				progressCallback(ProgressUpdate{
					Pass:     pass,
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
	}

	ffmpeg.AVFilterGraphFree(&filterGraph)
	filterFreed = true

	return &analysisFrameCollection{
		accumulators:     acc,
		intervals:        intervals,
		silenceIntervals: intervals,
		silenceMedians:   computeSilenceMedians(intervals),
		totalDuration:    totalDuration,
	}, nil
}

// createAnalysisFilterGraph creates an AVFilterGraph for Pass 1 analysis.
// Uses astats, aspectralstats, and ebur128 filters to extract measurements.
// Silence detection runs in Go using 250ms interval sampling, not in this graph.
func createAnalysisFilterGraph(
	decCtx *ffmpeg.AVCodecContext,
	config *BaseFilterConfig,
	_ PassNumber,
) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
	analysisConfig := deriveEffectiveFilterConfig(config)
	analysisConfig.FilterOrder = cloneFilterOrder(Pass1FilterOrder)

	return setupFilterGraph(decCtx, analysisConfig.BuildFilterSpec())
}
