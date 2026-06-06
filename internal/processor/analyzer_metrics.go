package processor

import (
	"encoding/json"
	"math"
	"strconv"
	"time"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// IntervalSample contains all measurements for a 250ms audio window.
// Captures comprehensive metrics from astats, aspectralstats, and ebur128 for
// room tone detection, adaptive filter tuning, and post-hoc analysis.
type IntervalSample struct {
	Timestamp time.Duration `json:"timestamp"` // Start of this interval

	// ─── Amplitude metrics (calculated per-interval from raw samples) ───────────
	RMSLevel  float64 `json:"rms_level"`  // dBFS, RMS level calculated from raw frame samples
	PeakLevel float64 `json:"peak_level"` // dBFS, peak level (max tracked per interval)

	// ─── aspectralstats spectral metrics (valid per-window from FFmpeg) ─────────
	Spectral SpectralMetrics `json:"-"` // Kept flat in JSON by custom marshal helpers

	// ─── ebur128 loudness metrics (windowed measurements) ───────────────────────
	MomentaryLUFS float64 `json:"momentary_lufs"`  // LUFS - 400ms window loudness
	ShortTermLUFS float64 `json:"short_term_lufs"` // LUFS - 3s window loudness
	TruePeak      float64 `json:"true_peak"`       // dBTP - true peak level (max tracked)
	SamplePeak    float64 `json:"sample_peak"`     // dBFS - sample peak level (max tracked)
}

type intervalSampleJSON struct {
	Timestamp time.Duration `json:"timestamp"`

	RMSLevel  float64 `json:"rms_level"`
	PeakLevel float64 `json:"peak_level"`

	SpectralMean     float64 `json:"spectral_mean"`
	SpectralVariance float64 `json:"spectral_variance"`
	SpectralCentroid float64 `json:"spectral_centroid"`
	SpectralSpread   float64 `json:"spectral_spread"`
	SpectralSkewness float64 `json:"spectral_skewness"`
	SpectralKurtosis float64 `json:"spectral_kurtosis"`
	SpectralEntropy  float64 `json:"spectral_entropy"`
	SpectralFlatness float64 `json:"spectral_flatness"`
	SpectralCrest    float64 `json:"spectral_crest"`
	SpectralFlux     float64 `json:"spectral_flux"`
	SpectralSlope    float64 `json:"spectral_slope"`
	SpectralDecrease float64 `json:"spectral_decrease"`
	SpectralRolloff  float64 `json:"spectral_rolloff"`

	MomentaryLUFS float64 `json:"momentary_lufs"`
	ShortTermLUFS float64 `json:"short_term_lufs"`
	TruePeak      float64 `json:"true_peak"`
	SamplePeak    float64 `json:"sample_peak"`
}

// MarshalJSON preserves the flat spectral_* JSON contract while the Go model
// carries interval spectral data as a SpectralMetrics value.
func (s IntervalSample) MarshalJSON() ([]byte, error) {
	return json.Marshal(intervalSampleJSON{
		Timestamp: s.Timestamp,

		RMSLevel:  s.RMSLevel,
		PeakLevel: s.PeakLevel,

		SpectralMean:     s.Spectral.Mean,
		SpectralVariance: s.Spectral.Variance,
		SpectralCentroid: s.Spectral.Centroid,
		SpectralSpread:   s.Spectral.Spread,
		SpectralSkewness: s.Spectral.Skewness,
		SpectralKurtosis: s.Spectral.Kurtosis,
		SpectralEntropy:  s.Spectral.Entropy,
		SpectralFlatness: s.Spectral.Flatness,
		SpectralCrest:    s.Spectral.Crest,
		SpectralFlux:     s.Spectral.Flux,
		SpectralSlope:    s.Spectral.Slope,
		SpectralDecrease: s.Spectral.Decrease,
		SpectralRolloff:  s.Spectral.Rolloff,

		MomentaryLUFS: s.MomentaryLUFS,
		ShortTermLUFS: s.ShortTermLUFS,
		TruePeak:      s.TruePeak,
		SamplePeak:    s.SamplePeak,
	})
}

// UnmarshalJSON accepts the legacy flat spectral_* JSON contract.
func (s *IntervalSample) UnmarshalJSON(data []byte) error {
	var decoded intervalSampleJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	s.Timestamp = decoded.Timestamp
	s.RMSLevel = decoded.RMSLevel
	s.PeakLevel = decoded.PeakLevel
	s.Spectral = SpectralMetrics{
		Mean:     decoded.SpectralMean,
		Variance: decoded.SpectralVariance,
		Centroid: decoded.SpectralCentroid,
		Spread:   decoded.SpectralSpread,
		Skewness: decoded.SpectralSkewness,
		Kurtosis: decoded.SpectralKurtosis,
		Entropy:  decoded.SpectralEntropy,
		Flatness: decoded.SpectralFlatness,
		Crest:    decoded.SpectralCrest,
		Flux:     decoded.SpectralFlux,
		Slope:    decoded.SpectralSlope,
		Decrease: decoded.SpectralDecrease,
		Rolloff:  decoded.SpectralRolloff,
	}
	s.MomentaryLUFS = decoded.MomentaryLUFS
	s.ShortTermLUFS = decoded.ShortTermLUFS
	s.TruePeak = decoded.TruePeak
	s.SamplePeak = decoded.SamplePeak

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Spectral.Found = intervalJSONHasSpectral(raw)

	return nil
}

func intervalJSONHasSpectral(raw map[string]json.RawMessage) bool {
	for _, key := range []string{
		"spectral_mean",
		"spectral_variance",
		"spectral_centroid",
		"spectral_spread",
		"spectral_skewness",
		"spectral_kurtosis",
		"spectral_entropy",
		"spectral_flatness",
		"spectral_crest",
		"spectral_flux",
		"spectral_slope",
		"spectral_decrease",
		"spectral_rolloff",
	} {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	return false
}

type baseMeasurementsJSON struct {
	SpectralMean     float64 `json:"spectral_mean"`
	SpectralVariance float64 `json:"spectral_variance"`
	SpectralCentroid float64 `json:"spectral_centroid"`
	SpectralSpread   float64 `json:"spectral_spread"`
	SpectralSkewness float64 `json:"spectral_skewness"`
	SpectralKurtosis float64 `json:"spectral_kurtosis"`
	SpectralEntropy  float64 `json:"spectral_entropy"`
	SpectralFlatness float64 `json:"spectral_flatness"`
	SpectralCrest    float64 `json:"spectral_crest"`
	SpectralFlux     float64 `json:"spectral_flux"`
	SpectralSlope    float64 `json:"spectral_slope"`
	SpectralDecrease float64 `json:"spectral_decrease"`
	SpectralRolloff  float64 `json:"spectral_rolloff"`

	DynamicRange float64 `json:"dynamic_range"`
	RMSLevel     float64 `json:"rms_level"`
	PeakLevel    float64 `json:"peak_level"`
	RMSTrough    float64 `json:"rms_trough"`
	RMSPeak      float64 `json:"rms_peak"`

	DCOffset          float64 `json:"dc_offset"`
	FlatFactor        float64 `json:"flat_factor"`
	CrestFactor       float64 `json:"crest_factor"`
	ZeroCrossingsRate float64 `json:"zero_crossings_rate"`
	ZeroCrossings     float64 `json:"zero_crossings"`
	MaxDifference     float64 `json:"max_difference"`
	MinDifference     float64 `json:"min_difference"`
	MeanDifference    float64 `json:"mean_difference"`
	RMSDifference     float64 `json:"rms_difference"`
	Entropy           float64 `json:"entropy"`
	MinLevel          float64 `json:"min_level"`
	MaxLevel          float64 `json:"max_level"`
	AstatsNoiseFloor  float64 `json:"astats_noise_floor"`
	NoiseFloorCount   float64 `json:"noise_floor_count"`
	BitDepth          float64 `json:"bit_depth"`
	NumberOfSamples   float64 `json:"number_of_samples"`

	MomentaryLoudness float64 `json:"momentary_loudness"`
	ShortTermLoudness float64 `json:"short_term_loudness"`
	SamplePeak        float64 `json:"sample_peak"`
}

// MarshalJSON preserves the flat spectral_* JSON contract while the Go model
// carries shared spectral data as a SpectralMetrics value.
func (b BaseMeasurements) MarshalJSON() ([]byte, error) {
	return json.Marshal(b.toJSON())
}

// UnmarshalJSON accepts the legacy flat spectral_* JSON contract.
func (b *BaseMeasurements) UnmarshalJSON(data []byte) error {
	var decoded baseMeasurementsJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	b.fromJSON(decoded)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	b.Spectral.Found = flatJSONHasSpectral(raw)

	return nil
}

func (b BaseMeasurements) toJSON() baseMeasurementsJSON {
	return baseMeasurementsJSON{
		SpectralMean:     b.Spectral.Mean,
		SpectralVariance: b.Spectral.Variance,
		SpectralCentroid: b.Spectral.Centroid,
		SpectralSpread:   b.Spectral.Spread,
		SpectralSkewness: b.Spectral.Skewness,
		SpectralKurtosis: b.Spectral.Kurtosis,
		SpectralEntropy:  b.Spectral.Entropy,
		SpectralFlatness: b.Spectral.Flatness,
		SpectralCrest:    b.Spectral.Crest,
		SpectralFlux:     b.Spectral.Flux,
		SpectralSlope:    b.Spectral.Slope,
		SpectralDecrease: b.Spectral.Decrease,
		SpectralRolloff:  b.Spectral.Rolloff,

		DynamicRange: b.DynamicRange,
		RMSLevel:     b.RMSLevel,
		PeakLevel:    b.PeakLevel,
		RMSTrough:    b.RMSTrough,
		RMSPeak:      b.RMSPeak,

		DCOffset:          b.DCOffset,
		FlatFactor:        b.FlatFactor,
		CrestFactor:       b.CrestFactor,
		ZeroCrossingsRate: b.ZeroCrossingsRate,
		ZeroCrossings:     b.ZeroCrossings,
		MaxDifference:     b.MaxDifference,
		MinDifference:     b.MinDifference,
		MeanDifference:    b.MeanDifference,
		RMSDifference:     b.RMSDifference,
		Entropy:           b.Entropy,
		MinLevel:          b.MinLevel,
		MaxLevel:          b.MaxLevel,
		AstatsNoiseFloor:  b.AstatsNoiseFloor,
		NoiseFloorCount:   b.NoiseFloorCount,
		BitDepth:          b.BitDepth,
		NumberOfSamples:   b.NumberOfSamples,

		MomentaryLoudness: b.MomentaryLoudness,
		ShortTermLoudness: b.ShortTermLoudness,
		SamplePeak:        b.SamplePeak,
	}
}

func (b *BaseMeasurements) fromJSON(decoded baseMeasurementsJSON) {
	b.Spectral = SpectralMetrics{
		Mean:     decoded.SpectralMean,
		Variance: decoded.SpectralVariance,
		Centroid: decoded.SpectralCentroid,
		Spread:   decoded.SpectralSpread,
		Skewness: decoded.SpectralSkewness,
		Kurtosis: decoded.SpectralKurtosis,
		Entropy:  decoded.SpectralEntropy,
		Flatness: decoded.SpectralFlatness,
		Crest:    decoded.SpectralCrest,
		Flux:     decoded.SpectralFlux,
		Slope:    decoded.SpectralSlope,
		Decrease: decoded.SpectralDecrease,
		Rolloff:  decoded.SpectralRolloff,
	}

	b.DynamicRange = decoded.DynamicRange
	b.RMSLevel = decoded.RMSLevel
	b.PeakLevel = decoded.PeakLevel
	b.RMSTrough = decoded.RMSTrough
	b.RMSPeak = decoded.RMSPeak

	b.DCOffset = decoded.DCOffset
	b.FlatFactor = decoded.FlatFactor
	b.CrestFactor = decoded.CrestFactor
	b.ZeroCrossingsRate = decoded.ZeroCrossingsRate
	b.ZeroCrossings = decoded.ZeroCrossings
	b.MaxDifference = decoded.MaxDifference
	b.MinDifference = decoded.MinDifference
	b.MeanDifference = decoded.MeanDifference
	b.RMSDifference = decoded.RMSDifference
	b.Entropy = decoded.Entropy
	b.MinLevel = decoded.MinLevel
	b.MaxLevel = decoded.MaxLevel
	b.AstatsNoiseFloor = decoded.AstatsNoiseFloor
	b.NoiseFloorCount = decoded.NoiseFloorCount
	b.BitDepth = decoded.BitDepth
	b.NumberOfSamples = decoded.NumberOfSamples

	b.MomentaryLoudness = decoded.MomentaryLoudness
	b.ShortTermLoudness = decoded.ShortTermLoudness
	b.SamplePeak = decoded.SamplePeak
}

// MarshalJSON preserves the flat spectral_* JSON contract from BaseMeasurements
// while retaining all input-specific analysis fields.
func (m AudioMeasurements) MarshalJSON() ([]byte, error) {
	object, err := baseMeasurementObject(m.BaseMeasurements)
	if err != nil {
		return nil, err
	}

	if err := setJSONField(object, "input_i", m.InputI); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "input_tp", m.InputTP); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "input_lra", m.InputLRA); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "input_thresh", m.InputThresh); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "target_offset", m.TargetOffset); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "noise_floor", m.NoiseFloor); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "noise_floor_source", m.NoiseFloorSource); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "prescan_noise_floor", m.PreScanNoiseFloor); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "room_tone_detect_level", m.RoomToneDetectLevel); err != nil {
		return nil, err
	}
	if err := setJSONFieldOmitEmptySlice(object, "room_tone_regions", m.RoomToneRegions); err != nil {
		return nil, err
	}
	if err := setJSONFieldOmitEmptySlice(object, "interval_samples", m.IntervalSamples); err != nil {
		return nil, err
	}
	if err := setJSONFieldOmitEmptySlice(object, "room_tone_candidates", m.RoomToneCandidates); err != nil {
		return nil, err
	}
	if err := setJSONFieldOmitEmptySlice(object, "speech_regions", m.SpeechRegions); err != nil {
		return nil, err
	}
	if err := setJSONFieldOmitEmptySlice(object, "speech_candidates", m.SpeechCandidates); err != nil {
		return nil, err
	}
	if err := setJSONFieldOmitEmptyPtr(object, "speech_profile", m.SpeechProfile); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "voice_activated", m.VoiceActivated); err != nil {
		return nil, err
	}
	if err := setJSONFieldOmitEmptyPtr(object, "noise_profile", m.NoiseProfile); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "suggested_gate_threshold", m.SuggestedGateThreshold); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "noise_reduction_headroom", m.NoiseReductionHeadroom); err != nil {
		return nil, err
	}

	return json.Marshal(object)
}

// UnmarshalJSON accepts the legacy flat spectral_* JSON contract and all
// input-specific analysis fields. It also accepts legacy silence_* keys
// (silence_detect_level, silence_regions, silence_candidates) so that
// reports generated by older releases continue to parse. When both the
// preferred room_tone_* keys and the legacy silence_* keys are present
// with different values, the new room_tone_* keys win deterministically.
func (m *AudioMeasurements) UnmarshalJSON(data []byte) error {
	if err := m.BaseMeasurements.UnmarshalJSON(data); err != nil {
		return err
	}

	var decoded audioMeasurementsJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var legacy audioMeasurementsLegacyJSON
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	m.InputI = decoded.InputI
	m.InputTP = decoded.InputTP
	m.InputLRA = decoded.InputLRA
	m.InputThresh = decoded.InputThresh
	m.TargetOffset = decoded.TargetOffset
	m.NoiseFloor = decoded.NoiseFloor
	m.NoiseFloorSource = decoded.NoiseFloorSource
	m.PreScanNoiseFloor = decoded.PreScanNoiseFloor

	// Precedence: new room_tone_* keys win over legacy silence_* keys when
	// both are present. Legacy keys are only applied when the new key is
	// absent from the payload.
	if _, ok := raw["room_tone_detect_level"]; ok {
		m.RoomToneDetectLevel = decoded.RoomToneDetectLevel
	} else {
		m.RoomToneDetectLevel = legacy.SilenceDetectLevel
	}
	if _, ok := raw["room_tone_regions"]; ok {
		m.RoomToneRegions = decoded.RoomToneRegions
	} else {
		m.RoomToneRegions = legacy.SilenceRegions
	}
	if _, ok := raw["room_tone_candidates"]; ok {
		m.RoomToneCandidates = decoded.RoomToneCandidates
	} else {
		m.RoomToneCandidates = legacy.SilenceCandidates
	}

	m.IntervalSamples = decoded.IntervalSamples
	m.SpeechRegions = decoded.SpeechRegions
	m.SpeechCandidates = decoded.SpeechCandidates
	m.SpeechProfile = decoded.SpeechProfile
	m.VoiceActivated = decoded.VoiceActivated
	m.NoiseProfile = decoded.NoiseProfile
	m.SuggestedGateThreshold = decoded.SuggestedGateThreshold
	m.NoiseReductionHeadroom = decoded.NoiseReductionHeadroom

	return nil
}

type audioMeasurementsJSON struct {
	InputI           float64 `json:"input_i"`
	InputTP          float64 `json:"input_tp"`
	InputLRA         float64 `json:"input_lra"`
	InputThresh      float64 `json:"input_thresh"`
	TargetOffset     float64 `json:"target_offset"`
	NoiseFloor       float64 `json:"noise_floor"`
	NoiseFloorSource string  `json:"noise_floor_source"`

	PreScanNoiseFloor   float64 `json:"prescan_noise_floor"`
	RoomToneDetectLevel float64 `json:"room_tone_detect_level"`

	RoomToneRegions    []RoomToneRegion           `json:"room_tone_regions,omitempty"`
	IntervalSamples    []IntervalSample           `json:"interval_samples,omitempty"`
	RoomToneCandidates []RoomToneCandidateMetrics `json:"room_tone_candidates,omitempty"`
	SpeechRegions      []SpeechRegion             `json:"speech_regions,omitempty"`
	SpeechCandidates   []SpeechCandidateMetrics   `json:"speech_candidates,omitempty"`
	SpeechProfile      *SpeechCandidateMetrics    `json:"speech_profile,omitempty"`

	VoiceActivated bool          `json:"voice_activated"`
	NoiseProfile   *NoiseProfile `json:"noise_profile,omitempty"`

	SuggestedGateThreshold float64 `json:"suggested_gate_threshold"`
	NoiseReductionHeadroom float64 `json:"noise_reduction_headroom"`
}

// audioMeasurementsLegacyJSON mirrors the legacy silence_* JSON keys that
// older releases emitted. It is read only as a fallback when the new
// room_tone_* keys are absent from the payload (see precedence note in
// AudioMeasurements.UnmarshalJSON).
type audioMeasurementsLegacyJSON struct {
	SilenceDetectLevel float64                    `json:"silence_detect_level"`
	SilenceRegions     []RoomToneRegion           `json:"silence_regions,omitempty"`
	SilenceCandidates  []RoomToneCandidateMetrics `json:"silence_candidates,omitempty"`
}

// MarshalJSON preserves the flat spectral_* JSON contract from BaseMeasurements
// while retaining all output-specific measurement fields.
func (m OutputMeasurements) MarshalJSON() ([]byte, error) {
	object, err := baseMeasurementObject(m.BaseMeasurements)
	if err != nil {
		return nil, err
	}

	if err := setJSONField(object, "output_i", m.OutputI); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "output_tp", m.OutputTP); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "output_lra", m.OutputLRA); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "output_thresh", m.OutputThresh); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "target_offset", m.TargetOffset); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "loudnorm_input_i", m.LoudnormInputI); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "loudnorm_input_tp", m.LoudnormInputTP); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "loudnorm_input_lra", m.LoudnormInputLRA); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "loudnorm_input_thresh", m.LoudnormInputThresh); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "loudnorm_target_offset", m.LoudnormTargetOffset); err != nil {
		return nil, err
	}
	if err := setJSONField(object, "loudnorm_measured", m.LoudnormMeasured); err != nil {
		return nil, err
	}
	if err := setJSONFieldOmitEmptyPtr(object, "room_tone_sample", m.RoomToneSample); err != nil {
		return nil, err
	}
	if err := setJSONFieldOmitEmptyPtr(object, "speech_sample", m.SpeechSample); err != nil {
		return nil, err
	}

	return json.Marshal(object)
}

// UnmarshalJSON accepts the legacy flat spectral_* JSON contract and all
// output-specific measurement fields. It also accepts the legacy silence_sample
// key so that reports generated by older releases continue to parse. When both
// the preferred room_tone_sample key and the legacy silence_sample key are
// present with different values, the new room_tone_sample key wins
// deterministically.
func (m *OutputMeasurements) UnmarshalJSON(data []byte) error {
	if err := m.BaseMeasurements.UnmarshalJSON(data); err != nil {
		return err
	}

	var decoded outputMeasurementsJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var legacy outputMeasurementsLegacyJSON
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	m.OutputI = decoded.OutputI
	m.OutputTP = decoded.OutputTP
	m.OutputLRA = decoded.OutputLRA
	m.OutputThresh = decoded.OutputThresh
	m.TargetOffset = decoded.TargetOffset
	m.LoudnormInputI = decoded.LoudnormInputI
	m.LoudnormInputTP = decoded.LoudnormInputTP
	m.LoudnormInputLRA = decoded.LoudnormInputLRA
	m.LoudnormInputThresh = decoded.LoudnormInputThresh
	m.LoudnormTargetOffset = decoded.LoudnormTargetOffset
	m.LoudnormMeasured = decoded.LoudnormMeasured

	// Precedence: new room_tone_sample key wins over legacy silence_sample
	// when both are present. Legacy key applies only when the new key is
	// absent from the payload.
	if _, ok := raw["room_tone_sample"]; ok {
		m.RoomToneSample = decoded.RoomToneSample
	} else {
		m.RoomToneSample = legacy.SilenceSample
	}

	m.SpeechSample = decoded.SpeechSample

	return nil
}

type outputMeasurementsJSON struct {
	OutputI      float64 `json:"output_i"`
	OutputTP     float64 `json:"output_tp"`
	OutputLRA    float64 `json:"output_lra"`
	OutputThresh float64 `json:"output_thresh"`
	TargetOffset float64 `json:"target_offset"`

	LoudnormInputI       float64 `json:"loudnorm_input_i"`
	LoudnormInputTP      float64 `json:"loudnorm_input_tp"`
	LoudnormInputLRA     float64 `json:"loudnorm_input_lra"`
	LoudnormInputThresh  float64 `json:"loudnorm_input_thresh"`
	LoudnormTargetOffset float64 `json:"loudnorm_target_offset"`
	LoudnormMeasured     bool    `json:"loudnorm_measured"`

	RoomToneSample *RoomToneCandidateMetrics `json:"room_tone_sample,omitempty"`
	SpeechSample   *SpeechCandidateMetrics   `json:"speech_sample,omitempty"`
}

// outputMeasurementsLegacyJSON mirrors the legacy silence_sample JSON key that
// older releases emitted. It is read only as a fallback when the new
// room_tone_sample key is absent from the payload (see precedence note in
// OutputMeasurements.UnmarshalJSON).
type outputMeasurementsLegacyJSON struct {
	SilenceSample *RoomToneCandidateMetrics `json:"silence_sample,omitempty"`
}

func baseMeasurementObject(base BaseMeasurements) (map[string]json.RawMessage, error) {
	data, err := base.MarshalJSON()
	if err != nil {
		return nil, err
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	return object, nil
}

func setJSONField(object map[string]json.RawMessage, name string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	object[name] = data
	return nil
}

func setJSONFieldOmitEmptySlice[T any](object map[string]json.RawMessage, name string, value []T) error {
	if len(value) == 0 {
		return nil
	}
	return setJSONField(object, name, value)
}

func setJSONFieldOmitEmptyPtr[T any](object map[string]json.RawMessage, name string, value *T) error {
	if value == nil {
		return nil
	}
	return setJSONField(object, name, value)
}

func flatJSONHasSpectral(raw map[string]json.RawMessage) bool {
	for _, key := range []string{
		"spectral_mean",
		"spectral_variance",
		"spectral_centroid",
		"spectral_spread",
		"spectral_skewness",
		"spectral_kurtosis",
		"spectral_entropy",
		"spectral_flatness",
		"spectral_crest",
		"spectral_flux",
		"spectral_slope",
		"spectral_decrease",
		"spectral_rolloff",
	} {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	return false
}

// intervalAccumulator holds accumulated values for a 250ms interval window.
// Values are aggregated appropriately: sums for averaging, min/max for extremes.
type intervalAccumulator struct {
	frameCount int // Number of frames in this interval

	// ─── Raw sample RMS accumulator (for accurate per-interval silence detection) ─
	// These are calculated directly from frame samples, not from astats metadata,
	// because astats with reset=0 provides cumulative stats, not per-interval.
	rawSumSquares  float64 // Sum of squared sample values (normalized -1 to 1)
	rawSampleCount int64   // Total sample count for this interval
	rawPeakAbs     float64 // Maximum absolute sample value (linear, 0.0-1.0) for this interval

	// ─── aspectralstats accumulators (valid per-window from FFmpeg) ─────────────
	spectralSum   SpectralMetrics
	spectralFound bool

	// ─── ebur128 accumulators (windowed measurements) ───────────────────────────
	momentaryLUFSSum float64
	shortTermLUFSSum float64
	truePeakMax      float64 // Maximum true peak
	samplePeakMax    float64 // Maximum sample peak
}

// intervalFrameMetrics holds per-frame metrics extracted from FFmpeg metadata.
// Only includes metrics that are valid per-window (not cumulative astats).
type intervalFrameMetrics struct {
	// Peak tracking (used for max tracking)
	PeakLevel float64

	// aspectralstats (valid per-window)
	Spectral SpectralMetrics

	// ebur128 (windowed measurements)
	MomentaryLUFS float64
	ShortTermLUFS float64
	TruePeak      float64
	SamplePeak    float64
}

// add accumulates a frame's metrics into the interval.
func (a *intervalAccumulator) add(m intervalFrameMetrics) {
	// Peak levels: keep maximum
	if a.frameCount == 0 || m.TruePeak > a.truePeakMax {
		a.truePeakMax = m.TruePeak
	}
	if a.frameCount == 0 || m.SamplePeak > a.samplePeakMax {
		a.samplePeakMax = m.SamplePeak
	}

	// aspectralstats sums for averaging (valid per-window measurements)
	a.spectralSum.add(m.Spectral)
	if m.Spectral.Found {
		a.spectralFound = true
	}

	// ebur128 sums for averaging (windowed measurements)
	a.momentaryLUFSSum += m.MomentaryLUFS
	a.shortTermLUFSSum += m.ShortTermLUFS

	a.frameCount++
}

// frameSumSquaresAndPeak calculates sum of squared sample values, sample count, and peak from an audio frame.
// Handles S16, FLT, S32, and DBL sample formats (both interleaved and planar), normalizing to [-1.0, 1.0] range.
// For planar multi-channel formats, iterates each plane separately via Data().Get(ch).
// Returns sumSquares, sampleCount, peakAbsolute, and ok (false if format is unsupported or frame is invalid).
func frameSumSquaresAndPeak(frame *ffmpeg.AVFrame) (sumSquares float64, sampleCount int64, peakAbs float64, ok bool) {
	if frame == nil || frame.NbSamples() == 0 {
		return 0, 0, 0, false
	}

	sampleFmt := ffmpeg.AVSampleFormat(frame.Format()) //nolint:gosec // AVSampleFormat values fit in int32
	nbSamples := frame.NbSamples()
	nbChannels := frame.ChLayout().NbChannels()

	// Determine if the format is planar (one plane per channel)
	isPlanar := false
	switch sampleFmt {
	case ffmpeg.AVSampleFmtS16P, ffmpeg.AVSampleFmtFltp, ffmpeg.AVSampleFmtS32P, ffmpeg.AVSampleFmtDblp:
		isPlanar = true
	}

	// For interleaved formats, all samples are in plane 0 with nbSamples*nbChannels elements.
	// For planar formats, each channel has its own plane with nbSamples elements.
	planes := 1
	samplesPerPlane := nbSamples * nbChannels
	if isPlanar {
		planes = nbChannels
		samplesPerPlane = nbSamples
	}

	for plane := 0; plane < planes; plane++ {
		dataPtr := frame.Data().Get(uintptr(plane))
		if dataPtr == nil {
			return 0, 0, 0, false
		}

		switch sampleFmt {
		case ffmpeg.AVSampleFmtS16, ffmpeg.AVSampleFmtS16P:
			samples := unsafe.Slice((*int16)(dataPtr), samplesPerPlane)
			for _, sample := range samples {
				normalized := float64(sample) / 32768.0
				sumSquares += normalized * normalized
				sampleCount++
				absVal := math.Abs(normalized)
				if absVal > peakAbs {
					peakAbs = absVal
				}
			}

		case ffmpeg.AVSampleFmtFlt, ffmpeg.AVSampleFmtFltp:
			samples := unsafe.Slice((*float32)(dataPtr), samplesPerPlane)
			for _, sample := range samples {
				normalized := float64(sample)
				sumSquares += normalized * normalized
				sampleCount++
				absVal := math.Abs(normalized)
				if absVal > peakAbs {
					peakAbs = absVal
				}
			}

		case ffmpeg.AVSampleFmtS32, ffmpeg.AVSampleFmtS32P:
			samples := unsafe.Slice((*int32)(dataPtr), samplesPerPlane)
			for _, sample := range samples {
				normalized := float64(sample) / 2147483648.0
				sumSquares += normalized * normalized
				sampleCount++
				absVal := math.Abs(normalized)
				if absVal > peakAbs {
					peakAbs = absVal
				}
			}

		case ffmpeg.AVSampleFmtDbl, ffmpeg.AVSampleFmtDblp:
			samples := unsafe.Slice((*float64)(dataPtr), samplesPerPlane)
			for _, sample := range samples {
				sumSquares += sample * sample
				sampleCount++
				absVal := math.Abs(sample)
				if absVal > peakAbs {
					peakAbs = absVal
				}
			}

		default:
			return 0, 0, 0, false
		}
	}

	return sumSquares, sampleCount, peakAbs, true
}

// addFrameRMSAndPeak accumulates RMS and peak from raw frame samples for accurate per-interval measurement.
// This bypasses astats metadata (which is cumulative) to get true per-interval RMS and peak.
func (a *intervalAccumulator) addFrameRMSAndPeak(frame *ffmpeg.AVFrame) {
	if ss, count, peak, ok := frameSumSquaresAndPeak(frame); ok {
		a.rawSumSquares += ss
		a.rawSampleCount += count
		if peak > a.rawPeakAbs {
			a.rawPeakAbs = peak
		}
	}
}

// finalize converts accumulated values to an IntervalSample.
func (a *intervalAccumulator) finalize(timestamp time.Duration) IntervalSample {
	// PeakLevel: Use raw sample calculation for accurate per-interval measurement
	// This is calculated directly from frame samples, not from astats metadata,
	// because astats with reset=0 provides cumulative stats, not per-interval.
	var peakLevelDB float64
	if a.rawPeakAbs > 0 {
		peakLevelDB = 20.0 * math.Log10(a.rawPeakAbs)
	} else {
		peakLevelDB = -120.0
	}

	sample := IntervalSample{
		Timestamp: timestamp,

		// Max values
		PeakLevel:  peakLevelDB,
		TruePeak:   a.truePeakMax,
		SamplePeak: a.samplePeakMax,
	}

	// RMS Level: Use raw sample calculation for accurate per-interval measurement
	// This is calculated directly from frame samples, not from astats metadata,
	// because astats with reset=0 provides cumulative stats, not per-interval.
	if a.rawSampleCount > 0 {
		rms := math.Sqrt(a.rawSumSquares / float64(a.rawSampleCount))
		if rms < 0.00001 { // Equivalent to < -100 dB
			sample.RMSLevel = -120.0
		} else {
			sample.RMSLevel = 20.0 * math.Log10(rms)
		}
	} else {
		sample.RMSLevel = -120.0
	}

	if a.frameCount > 0 {
		n := float64(a.frameCount)

		// aspectralstats averages (valid per-window measurements)
		sample.Spectral = a.spectralSum.average(n)
		sample.Spectral.Found = a.spectralFound

		// ebur128 averages (windowed measurements)
		sample.MomentaryLUFS = a.momentaryLUFSSum / n
		sample.ShortTermLUFS = a.shortTermLUFSSum / n
	}

	return sample
}

// reset clears the accumulator for the next interval.
func (a *intervalAccumulator) reset() {
	*a = intervalAccumulator{
		truePeakMax:   -120.0,
		samplePeakMax: -120.0,
	}
}

// Cached metadata keys for frame extraction - avoids per-frame C string allocations
// These use GlobalCStr which maintains an internal cache, so identical strings share the same CStr
var (
	// aspectralstats metadata keys (all measurements)
	metaKeySpectralMean     = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.mean")
	metaKeySpectralVariance = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.variance")
	metaKeySpectralCentroid = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.centroid")
	metaKeySpectralSpread   = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.spread")
	metaKeySpectralSkewness = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.skewness")
	metaKeySpectralKurtosis = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.kurtosis")
	metaKeySpectralEntropy  = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.entropy")
	metaKeySpectralFlatness = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.flatness")
	metaKeySpectralCrest    = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.crest")
	metaKeySpectralFlux     = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.flux")
	metaKeySpectralSlope    = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.slope")
	metaKeySpectralDecrease = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.decrease")
	metaKeySpectralRolloff  = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.rolloff")

	// astats per-channel metadata keys (channel .1 for mono after downmix)
	metaKeyDynamicRange      = ffmpeg.GlobalCStr("lavfi.astats.1.Dynamic_range")
	metaKeyRMSLevel          = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_level")
	metaKeyPeakLevel         = ffmpeg.GlobalCStr("lavfi.astats.1.Peak_level")
	metaKeyRMSTrough         = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_trough")
	metaKeyRMSPeak           = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_peak")
	metaKeyDCOffset          = ffmpeg.GlobalCStr("lavfi.astats.1.DC_offset")
	metaKeyFlatFactor        = ffmpeg.GlobalCStr("lavfi.astats.1.Flat_factor")
	metaKeyCrestFactor       = ffmpeg.GlobalCStr("lavfi.astats.1.Crest_factor")
	metaKeyZeroCrossingsRate = ffmpeg.GlobalCStr("lavfi.astats.1.Zero_crossings_rate")
	metaKeyZeroCrossings     = ffmpeg.GlobalCStr("lavfi.astats.1.Zero_crossings")
	metaKeyMaxDifference     = ffmpeg.GlobalCStr("lavfi.astats.1.Max_difference")
	metaKeyMinDifference     = ffmpeg.GlobalCStr("lavfi.astats.1.Min_difference")
	metaKeyMeanDifference    = ffmpeg.GlobalCStr("lavfi.astats.1.Mean_difference")
	metaKeyRMSDifference     = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_difference")
	metaKeyEntropy           = ffmpeg.GlobalCStr("lavfi.astats.1.Entropy")
	metaKeyMinLevel          = ffmpeg.GlobalCStr("lavfi.astats.1.Min_level")
	metaKeyMaxLevel          = ffmpeg.GlobalCStr("lavfi.astats.1.Max_level")
	metaKeyNoiseFloor        = ffmpeg.GlobalCStr("lavfi.astats.1.Noise_floor")
	metaKeyNoiseFloorCount   = ffmpeg.GlobalCStr("lavfi.astats.1.Noise_floor_count")
	metaKeyBitDepth          = ffmpeg.GlobalCStr("lavfi.astats.1.Bit_depth")
	metaKeyNumberOfSamples   = ffmpeg.GlobalCStr("lavfi.astats.1.Number_of_samples")

	// astats overall metadata keys (used with measure_perchannel=0)
	metaKeyOverallRMSLevel    = ffmpeg.GlobalCStr("lavfi.astats.Overall.RMS_level")
	metaKeyOverallPeakLevel   = ffmpeg.GlobalCStr("lavfi.astats.Overall.Peak_level")
	metaKeyOverallCrestFactor = ffmpeg.GlobalCStr("lavfi.astats.Overall.Crest_factor")
	// ebur128 metadata keys
	metaKeyEbur128I            = ffmpeg.GlobalCStr("lavfi.r128.I")
	metaKeyEbur128M            = ffmpeg.GlobalCStr("lavfi.r128.M")
	metaKeyEbur128S            = ffmpeg.GlobalCStr("lavfi.r128.S")
	metaKeyEbur128TruePeak     = ffmpeg.GlobalCStr("lavfi.r128.true_peak")
	metaKeyEbur128SamplePeak   = ffmpeg.GlobalCStr("lavfi.r128.sample_peak")
	metaKeyEbur128LRA          = ffmpeg.GlobalCStr("lavfi.r128.LRA")
	metaKeyEbur128TargetThresh = ffmpeg.GlobalCStr("lavfi.r128.target_threshold")
)

// baseMetadataAccumulators contains fields shared between input (Pass 1) and output (Pass 2) accumulators.
// Embedded in both metadataAccumulators and outputMetadataAccumulators to avoid duplication.
// Spectral stats are averaged across frames; astats and ebur128 values are cumulative, so the latest wins.
type baseMetadataAccumulators struct {
	// Spectral statistics from aspectralstats (averaged across frames)
	spectral SpectralAccumulator

	// astats measurements (cumulative - we keep latest values)
	astatsDynamicRange      float64
	astatsRMSLevel          float64
	astatsPeakLevel         float64
	astatsRMSTrough         float64
	astatsRMSPeak           float64
	astatsDCOffset          float64
	astatsFlatFactor        float64
	astatsCrestFactor       float64
	astatsZeroCrossingsRate float64
	astatsZeroCrossings     float64
	astatsMaxDifference     float64
	astatsMinDifference     float64
	astatsMeanDifference    float64
	astatsRMSDifference     float64
	astatsEntropy           float64
	astatsMinLevel          float64
	astatsMaxLevel          float64
	astatsNoiseFloor        float64
	astatsNoiseFloorCount   float64
	astatsBitDepth          float64
	astatsNumberOfSamples   float64
	astatsFound             bool
}

// accumulateSpectral adds the given spectral measurements to the running sums.
func (b *baseMetadataAccumulators) accumulateSpectral(spectral SpectralMetrics) {
	b.spectral.Add(spectral)
}

// finalizeSpectral returns averaged spectral metrics from the accumulated sums.
// Returns zero-value SpectralMetrics when no spectral frames were accumulated.
func (b *baseMetadataAccumulators) finalizeSpectral() SpectralMetrics {
	return b.spectral.Average()
}

// extractAstatsMetadata extracts all astats measurements from FFmpeg metadata.
// These are cumulative values, so we keep the latest from each frame.
// Includes conversions: linearRatioToDB for CrestFactor, linearSampleToDBFS for MinLevel/MaxLevel.
func (b *baseMetadataAccumulators) extractAstatsMetadata(metadata *ffmpeg.AVDictionary) {
	if value, ok := getFloatMetadata(metadata, metaKeyDynamicRange); ok {
		b.astatsDynamicRange = value
		b.astatsFound = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSLevel); ok {
		b.astatsRMSLevel = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyPeakLevel); ok {
		b.astatsPeakLevel = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSTrough); ok {
		b.astatsRMSTrough = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSPeak); ok {
		b.astatsRMSPeak = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyDCOffset); ok {
		b.astatsDCOffset = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyFlatFactor); ok {
		b.astatsFlatFactor = value
	}
	// CrestFactor: FFmpeg reports as linear ratio (peak/RMS), convert to dB
	if value, ok := getFloatMetadata(metadata, metaKeyCrestFactor); ok {
		b.astatsCrestFactor = linearRatioToDB(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyZeroCrossingsRate); ok {
		b.astatsZeroCrossingsRate = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyZeroCrossings); ok {
		b.astatsZeroCrossings = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMaxDifference); ok {
		b.astatsMaxDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMinDifference); ok {
		b.astatsMinDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMeanDifference); ok {
		b.astatsMeanDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSDifference); ok {
		b.astatsRMSDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEntropy); ok {
		b.astatsEntropy = value
	}
	// MinLevel/MaxLevel: FFmpeg reports as linear sample values, convert to dBFS
	if value, ok := getFloatMetadata(metadata, metaKeyMinLevel); ok {
		b.astatsMinLevel = linearSampleToDBFS(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMaxLevel); ok {
		b.astatsMaxLevel = linearSampleToDBFS(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyNoiseFloor); ok {
		b.astatsNoiseFloor = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyNoiseFloorCount); ok {
		b.astatsNoiseFloorCount = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyBitDepth); ok {
		b.astatsBitDepth = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyNumberOfSamples); ok {
		b.astatsNumberOfSamples = value
	}
}

// metadataAccumulators holds accumulator variables for Pass 1 frame metadata extraction.
// Uses baseMetadataAccumulators for spectral and astats fields shared with output analysis.
type metadataAccumulators struct {
	// Embed shared spectral and astats fields
	baseMetadataAccumulators

	// ebur128 measurements (cumulative - we keep latest values)
	ebur128InputI   float64
	ebur128InputM   float64 // Momentary loudness (400ms window, updates per frame)
	ebur128InputS   float64 // Short-term loudness (3s window)
	ebur128InputTP  float64
	ebur128InputSP  float64 // Sample peak
	ebur128InputLRA float64
	ebur128Found    bool
}

// getFloatMetadata extracts a float value from the metadata dictionary
func getFloatMetadata(metadata *ffmpeg.AVDictionary, key *ffmpeg.CStr) (float64, bool) {
	if entry := ffmpeg.AVDictGet(metadata, key, nil, 0); entry != nil {
		if value, err := strconv.ParseFloat(entry.Value().String(), 64); err == nil {
			return value, true
		}
	}
	return 0.0, false
}

// linearRatioToDB converts a linear ratio (e.g., Crest_factor) to decibels.
// FFmpeg's astats Crest_factor is reported as a linear ratio (peak/RMS), not in dB.
func linearRatioToDB(ratio float64) float64 {
	if ratio <= 0 {
		return -120.0 // Floor for zero/negative values
	}
	return 20 * math.Log10(ratio)
}

// linearSampleToDBFS converts a linear sample value to dBFS.
// FFmpeg's astats Min_level and Max_level are reported as linear sample values
// (typically -1.0 to +1.0 for float audio, or integer sample values).
// We normalize assuming the value represents the fraction of full scale.
func linearSampleToDBFS(sample float64) float64 {
	absVal := math.Abs(sample)
	if absVal <= 0 {
		return -120.0 // Floor for zero values
	}
	// For normalized float audio (-1.0 to +1.0), this is direct
	// For integer sample values, we need to detect and normalize
	// If abs value > 1.0, assume integer samples and normalize to 16-bit range
	if absVal > 1.0 {
		// Likely integer sample value (e.g., from 16-bit audio: -32768 to 32767)
		absVal /= 32768.0
	}
	if absVal > 1.0 {
		absVal = 1.0 // Clamp to 0 dBFS max
	}
	return 20 * math.Log10(absVal)
}

// SpectralMetrics holds the 13 aspectralstats measurements extracted from FFmpeg metadata.
// These metrics characterise the frequency content of audio frames.
type SpectralMetrics struct {
	Mean     float64 `json:"mean"`     // Average spectral power
	Variance float64 `json:"variance"` // Spectral variance
	Centroid float64 `json:"centroid"` // Spectral centroid (Hz) - where energy is concentrated
	Spread   float64 `json:"spread"`   // Spectral spread (Hz) - bandwidth/fullness indicator
	Skewness float64 `json:"skewness"` // Spectral asymmetry - positive=bright, negative=dark
	Kurtosis float64 `json:"kurtosis"` // Spectral peakiness - tonal vs broadband content
	Entropy  float64 `json:"entropy"`  // Spectral randomness (0-1) - noise classification
	Flatness float64 `json:"flatness"` // Noise vs tonal ratio (0-1) - low=tonal, high=noisy
	Crest    float64 `json:"crest"`    // Spectral peak-to-RMS - transient indicator
	Flux     float64 `json:"flux"`     // Frame-to-frame spectral change
	Slope    float64 `json:"slope"`    // Spectral tilt - negative=more bass
	Decrease float64 `json:"decrease"` // Average spectral decrease
	Rolloff  float64 `json:"rolloff"`  // Spectral rolloff (Hz) - HF energy dropoff point
	Found    bool    `json:"-"`        // True if any spectral metric was extracted
}

// SpectralAccumulator accumulates spectral measurements across frames and
// averages only frames where aspectralstats metadata was found.
type SpectralAccumulator struct {
	sum   SpectralMetrics
	count int
}

// Add accumulates a found spectral measurement and ignores frames without
// aspectralstats metadata.
func (a *SpectralAccumulator) Add(spectral SpectralMetrics) {
	if !spectral.Found {
		return
	}
	a.sum.add(spectral)
	a.count++
}

// Average returns averaged spectral measurements, or the zero value when no
// spectral metadata was accumulated.
func (a SpectralAccumulator) Average() SpectralMetrics {
	if !a.Found() {
		return SpectralMetrics{}
	}
	average := a.sum.average(float64(a.count))
	average.Found = true
	return average
}

// Count returns the number of found spectral frames accumulated.
func (a SpectralAccumulator) Count() int {
	return a.count
}

// Found reports whether at least one spectral frame was accumulated.
func (a SpectralAccumulator) Found() bool {
	return a.count > 0
}

// add accumulates another SpectralMetrics into this one (element-wise sum).
func (m *SpectralMetrics) add(other SpectralMetrics) {
	m.Mean += other.Mean
	m.Variance += other.Variance
	m.Centroid += other.Centroid
	m.Spread += other.Spread
	m.Skewness += other.Skewness
	m.Kurtosis += other.Kurtosis
	m.Entropy += other.Entropy
	m.Flatness += other.Flatness
	m.Crest += other.Crest
	m.Flux += other.Flux
	m.Slope += other.Slope
	m.Decrease += other.Decrease
	m.Rolloff += other.Rolloff
}

// average returns a new SpectralMetrics with all fields divided by n.
func (m SpectralMetrics) average(n float64) SpectralMetrics {
	return SpectralMetrics{
		Mean:     m.Mean / n,
		Variance: m.Variance / n,
		Centroid: m.Centroid / n,
		Spread:   m.Spread / n,
		Skewness: m.Skewness / n,
		Kurtosis: m.Kurtosis / n,
		Entropy:  m.Entropy / n,
		Flatness: m.Flatness / n,
		Crest:    m.Crest / n,
		Flux:     m.Flux / n,
		Slope:    m.Slope / n,
		Decrease: m.Decrease / n,
		Rolloff:  m.Rolloff / n,
	}
}

// extractSpectralMetrics extracts all 13 aspectralstats measurements from FFmpeg metadata.
// Returns a SpectralMetrics struct with Found=true if at least one metric was extracted.
func extractSpectralMetrics(metadata *ffmpeg.AVDictionary) SpectralMetrics {
	var m SpectralMetrics

	if value, ok := getFloatMetadata(metadata, metaKeySpectralMean); ok {
		m.Mean = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralVariance); ok {
		m.Variance = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralCentroid); ok {
		m.Centroid = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralSpread); ok {
		m.Spread = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralSkewness); ok {
		m.Skewness = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralKurtosis); ok {
		m.Kurtosis = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralEntropy); ok {
		m.Entropy = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralFlatness); ok {
		m.Flatness = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralCrest); ok {
		m.Crest = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralFlux); ok {
		m.Flux = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralSlope); ok {
		m.Slope = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralDecrease); ok {
		m.Decrease = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralRolloff); ok {
		m.Rolloff = value
		m.Found = true
	}

	return m
}

// extractIntervalFrameMetrics extracts per-frame metrics for interval accumulation.
// Only collects metrics that are valid per-window (aspectralstats, ebur128 windowed).
// Excludes astats which provides cumulative values, not per-interval.
func extractIntervalFrameMetrics(metadata *ffmpeg.AVDictionary, spectral SpectralMetrics) intervalFrameMetrics {
	var m intervalFrameMetrics

	// Peak level from astats (used for max tracking, which is valid per-interval)
	m.PeakLevel, _ = getFloatMetadata(metadata, metaKeyPeakLevel)

	// aspectralstats metrics (valid per-window measurements, pre-extracted by caller)
	m.Spectral = spectral

	// ebur128 windowed measurements
	m.MomentaryLUFS, _ = getFloatMetadata(metadata, metaKeyEbur128M)
	m.ShortTermLUFS, _ = getFloatMetadata(metadata, metaKeyEbur128S)

	// ebur128 peak values are linear ratios, convert to dB
	if rawTP, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
		m.TruePeak = linearRatioToDB(rawTP)
	}
	if rawSP, ok := getFloatMetadata(metadata, metaKeyEbur128SamplePeak); ok {
		m.SamplePeak = linearRatioToDB(rawSP)
	}

	return m
}

// extractFrameMetadata extracts audio analysis metadata from a filtered frame.
// Updates accumulators with spectral, astats, and ebur128 measurements.
// Called from both the main processing loop and the flush loop.
func extractFrameMetadata(metadata *ffmpeg.AVDictionary, acc *metadataAccumulators, spectral SpectralMetrics) {
	if metadata == nil {
		return
	}

	// Accumulate pre-extracted spectral metrics (averaged across frames)
	// For mono audio, spectral stats are under channel .1
	acc.accumulateSpectral(spectral)

	// Extract astats measurements (cumulative, so we keep the latest)
	// For mono audio, stats are under channel .1
	acc.extractAstatsMetadata(metadata)

	// Extract ebur128 measurements (cumulative loudness analysis)
	// ebur128 provides: M (momentary 400ms), S (short-term 3s), I (integrated), LRA, sample_peak, true_peak
	// We need these for loudness normalization and interval-based analysis
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128I); ok {
		acc.ebur128InputI = value
		acc.ebur128Found = true
	}

	// Momentary loudness (400ms window) - useful for interval-based silence detection
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128M); ok {
		acc.ebur128InputM = value
	}

	// Short-term loudness (3s window)
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128S); ok {
		acc.ebur128InputS = value
	}

	if value, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
		acc.ebur128InputTP = linearRatioToDB(value)
	}

	if value, ok := getFloatMetadata(metadata, metaKeyEbur128SamplePeak); ok {
		acc.ebur128InputSP = linearRatioToDB(value)
	}

	if value, ok := getFloatMetadata(metadata, metaKeyEbur128LRA); ok {
		acc.ebur128InputLRA = value
	}
}

// outputMetadataAccumulators holds accumulator variables for Pass 2 output measurement extraction.
// Uses baseMetadataAccumulators for spectral and astats fields shared with input analysis.
// Mirrors metadataAccumulators but without room tone detection fields.
type outputMetadataAccumulators struct {
	// Embed shared spectral and astats fields
	baseMetadataAccumulators

	// ebur128 measurements (cumulative - we keep latest values)
	ebur128OutputI      float64
	ebur128OutputM      float64 // Momentary loudness
	ebur128OutputS      float64 // Short-term loudness
	ebur128OutputTP     float64
	ebur128OutputSP     float64 // Sample peak
	ebur128OutputLRA    float64
	ebur128OutputThresh float64 // Gating threshold for loudnorm
	ebur128Found        bool
}

// extractOutputFrameMetadata extracts audio analysis metadata from a Pass 2 filtered frame.
// Updates accumulators with spectral, astats, and ebur128 measurements.
// This is the output analysis counterpart to extractFrameMetadata.
func extractOutputFrameMetadata(metadata *ffmpeg.AVDictionary, acc *outputMetadataAccumulators) {
	if metadata == nil {
		return
	}

	// Extract all aspectralstats measurements (averaged across frames)
	acc.accumulateSpectral(extractSpectralMetrics(metadata))

	// Extract astats measurements (cumulative, so we keep the latest)
	acc.extractAstatsMetadata(metadata)

	// Extract ebur128 measurements
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128I); ok {
		acc.ebur128OutputI = value
		acc.ebur128Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128M); ok {
		acc.ebur128OutputM = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128S); ok {
		acc.ebur128OutputS = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
		acc.ebur128OutputTP = linearRatioToDB(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128SamplePeak); ok {
		acc.ebur128OutputSP = linearRatioToDB(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128LRA); ok {
		acc.ebur128OutputLRA = value
	}
	// Gating threshold (for loudnorm two-pass mode)
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128TargetThresh); ok {
		acc.ebur128OutputThresh = value
	}
}

// finalizeOutputMeasurements converts accumulated values to OutputMeasurements struct.
// Returns nil if no measurements were captured.
func finalizeOutputMeasurements(acc *outputMetadataAccumulators) *OutputMeasurements {
	if !acc.ebur128Found && !acc.astatsFound && !acc.spectral.Found() {
		return nil // No measurements captured
	}

	m := &OutputMeasurements{
		BaseMeasurements: BaseMeasurements{
			Spectral: acc.finalizeSpectral(),

			// ebur128 momentary/short-term loudness
			MomentaryLoudness: acc.ebur128OutputM,
			ShortTermLoudness: acc.ebur128OutputS,
			SamplePeak:        acc.ebur128OutputSP,

			// astats time-domain measurements
			DynamicRange:      acc.astatsDynamicRange,
			RMSLevel:          acc.astatsRMSLevel,
			PeakLevel:         acc.astatsPeakLevel,
			RMSTrough:         acc.astatsRMSTrough,
			RMSPeak:           acc.astatsRMSPeak,
			DCOffset:          acc.astatsDCOffset,
			FlatFactor:        acc.astatsFlatFactor,
			CrestFactor:       acc.astatsCrestFactor,
			ZeroCrossingsRate: acc.astatsZeroCrossingsRate,
			ZeroCrossings:     acc.astatsZeroCrossings,
			MaxDifference:     acc.astatsMaxDifference,
			MinDifference:     acc.astatsMinDifference,
			MeanDifference:    acc.astatsMeanDifference,
			RMSDifference:     acc.astatsRMSDifference,
			Entropy:           acc.astatsEntropy,
			MinLevel:          acc.astatsMinLevel,
			MaxLevel:          acc.astatsMaxLevel,
			AstatsNoiseFloor:  acc.astatsNoiseFloor,
			NoiseFloorCount:   acc.astatsNoiseFloorCount,
			BitDepth:          acc.astatsBitDepth,
			NumberOfSamples:   acc.astatsNumberOfSamples,
		},
		// Output-specific loudness measurements
		OutputI:      acc.ebur128OutputI,
		OutputTP:     acc.ebur128OutputTP,
		OutputLRA:    acc.ebur128OutputLRA,
		OutputThresh: acc.ebur128OutputThresh,
		TargetOffset: 0.0, // Will be calculated in Pass 3
	}

	// If ebur128 target_threshold metadata is missing, calculate it manually
	// according to EBU R128 standard: gating threshold = integrated loudness - 10 LU
	if m.OutputThresh == 0.0 && m.OutputI != 0.0 {
		m.OutputThresh = m.OutputI - 10.0
	}

	return m
}
