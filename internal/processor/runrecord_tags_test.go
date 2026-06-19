package processor

import (
	"encoding/json"
	"math"
	"testing"
	"time"
)

// collectJSONKeys walks a decoded JSON tree and returns the set of every object
// key encountered at any depth. Used to assert §8.4 key presence/absence across
// the nested domain record without hard-coding the tree shape.
func collectJSONKeys(v any, into map[string]bool) {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			into[key] = true
			collectJSONKeys(child, into)
		}
	case []any:
		for _, child := range node {
			collectJSONKeys(child, into)
		}
	}
}

// populatedAudioMeasurements builds an in-memory AudioMeasurements covering every
// domain block plus an elected speech profile and noise profile, so the marshal
// exercises the full §8.1 key surface without needing testdata/.
func populatedAudioMeasurements() *AudioMeasurements {
	spectral := SpectralMetrics{
		Mean: 1, Variance: 2, Centroid: 2000, Spread: 400, Skewness: 1,
		Kurtosis: 4, Entropy: 0.4, Flatness: 0.6, Crest: 8, Flux: 0.04,
		Slope: -0.2, Decrease: 0.12, Rolloff: 7000, Found: true,
	}

	m := &AudioMeasurements{Spectral: spectral}

	m.Loudness = InputLoudnessMetrics{
		LoudnessMetrics: LoudnessMetrics{MomentaryLoudness: -17, ShortTermLoudness: -16.5, SamplePeak: -1.2},
		InputI:          -18, InputTP: -1, InputLRA: 7, InputThresh: -28, TargetOffset: -2,
	}
	m.Dynamics = DynamicsMetrics{
		DynamicRange: 12, RMSLevel: -22, PeakLevel: -3, RMSTrough: -45, RMSPeak: -18,
		DCOffset: 0.001, FlatFactor: 0, CrestFactor: 14, ZeroCrossingsRate: 0.05,
		ZeroCrossings: 1000, MaxDifference: 0.2, MinDifference: 0, MeanDifference: 0.01,
		RMSDifference: 0.02, Entropy: 0.7, MinLevel: -90, MaxLevel: -3,
		NoiseFloorCount: 500, BitDepth: 16, NumberOfSamples: 480000,
	}
	m.Noise = NoiseMetrics{
		Floor: -60, FloorSource: "vad_percentile", FloorPrescan: -58, FloorAstats: -62,
		RoomToneDetectLevel: -59, VoiceActivated: false, ReductionHeadroom: 38,
	}

	sampleBlock := RegionSample{
		RMSLevel: -20, PeakLevel: -3, CrestFactor: 13, Spectral: spectral,
		MomentaryLUFS: -19, ShortTermLUFS: -18, TruePeak: -2, SamplePeak: -2.5,
	}
	electedRoomTone := sampleBlock
	m.Regions = RegionMetrics{
		SpeechRegions: []SpeechRegion{{Start: 30 * time.Second, End: 40 * time.Second, Duration: 10 * time.Second}},
		SpeechCandidates: []SpeechCandidateMetrics{{
			Region:       SpeechRegion{Start: 30 * time.Second, End: 40 * time.Second, Duration: 10 * time.Second},
			RegionSample: sampleBlock, VoicingDensity: 0.8,
			BodyBandRMS: -25, SibBandRMS: -30, BandsMeasured: true, Score: 7,
		}},
		NoiseProfile: &NoiseProfile{
			Start: 2 * time.Second, Duration: 10 * time.Second,
			MeasuredNoiseFloor: -60, PeakLevel: -50, CrestFactor: 10, Entropy: 0.5,
			SpectralMean: 1.2, SpectralVariance: 2.4, SpectralCentroid: 1500,
			SpectralSpread: 350, SpectralSkewness: 0.8, SpectralKurtosis: 3,
			SpectralEntropy: 0.55, SpectralFlatness: 0.4, SpectralCrest: 7.5,
			SpectralFlux: 0.03, SpectralSlope: -0.25, SpectralDecrease: 0.11,
			SpectralRolloff: 6500,
		},
	}
	m.Regions.SpeechProfile = &m.Regions.SpeechCandidates[0]
	// Elected room-tone sample measured from the elected low-cluster region; backs
	// regions.room_tone.samples.input on the record.
	m.Regions.ElectedRoomToneSample = &electedRoomTone

	return m
}

func TestAudioMeasurementsJSON_HasCanonicalKeys(t *testing.T) {
	data, err := json.Marshal(populatedAudioMeasurements())
	if err != nil {
		t.Fatalf("Marshal() failed: %v", err)
	}

	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}
	keys := map[string]bool{}
	collectJSONKeys(tree, keys)

	wantPresent := []string{
		// domain containers
		"loudness", "dynamics", "noise", "regions",
		// loudness (§8.4 suffixes)
		"integrated_lufs", "true_peak_dbtp", "lra_lu", "thresh_lufs", "target_offset_db",
		"momentary_lufs", "short_term_lufs", "sample_peak_dbfs",
		// dynamics
		"rms_level_dbfs", "peak_level_dbfs", "dynamic_range_db", "crest_factor_astats_db",
		"rms_trough_dbfs", "rms_peak_dbfs", "dc_offset", "flat_factor",
		"zero_crossings_rate", "zero_crossings_count", "min_level_dbfs", "max_level_dbfs",
		"bit_depth", "number_of_samples", "noise_floor_count", "entropy",
		// noise
		"floor_dbfs", "floor_source", "floor_prescan_dbfs", "floor_astats_dbfs",
		"reduction_headroom_db", "room_tone_detect_level_dbfs", "voice_activated",
		// spectral suffixes (region/profile spectral blocks)
		"centroid_hz", "spread_hz", "rolloff_hz",
		// regions
		"speech_regions", "speech_candidates", "speech_profile", "noise_profile",
		// regions gate statistics
		"voiced_low_percentile_dbfs", "noise_high_percentile_dbfs", "gate_separation_db",
		// region-sample / candidate fields
		"crest_factor_db", "speech_band_body_rms_dbfs", "speech_band_sib_rms_dbfs",
		// noise profile fields (full 13-metric spectral set)
		"measured_floor_dbfs", "spectral_centroid_hz",
		"spectral_mean", "spectral_variance", "spectral_spread_hz",
		"spectral_skewness", "spectral_entropy", "spectral_crest",
		"spectral_flux", "spectral_slope", "spectral_decrease", "spectral_rolloff_hz",
	}
	for _, key := range wantPresent {
		if !keys[key] {
			t.Errorf("missing canonical §8.4 key %q", key)
		}
	}

	wantAbsent := []string{
		// legacy un-suffixed loudness/dynamics keys
		"input_i", "input_tp", "input_lra", "input_thresh",
		"rms_level", "peak_level", "dynamic_range", "crest_factor",
		"target_offset", "momentary_loudness", "short_term_loudness", "sample_peak",
		"floor", "floor_prescan", "floor_astats", "reduction_headroom",
		"room_tone_detect_level", "min_level", "max_level", "zero_crossings",
		// legacy flat spectral keys (the old un-suffixed SpectralMetrics marshal).
		// Note: NoiseProfile keeps its own distinct contamination fields covering
		// the full 13-metric set (spectral_mean / spectral_flatness /
		// spectral_kurtosis / spectral_centroid_hz / spectral_flux / ...), so those
		// suffixed keys are deliberately NOT asserted absent. The bare
		// spectral_spread / spectral_rolloff (no _hz) stay asserted absent because
		// NoiseProfile emits the _hz-suffixed forms.
		"spectral_centroid", "spectral_spread", "spectral_rolloff",
		// removed silence/suggestion plumbing
		"suggested_gate_threshold", "measured_noise_floor",
	}
	for _, key := range wantAbsent {
		if keys[key] {
			t.Errorf("legacy key %q must not appear in the record", key)
		}
	}
}

// TestRunRecordNoiseProfileSpectralFields confirms the RunRecord noise-profile
// node carries all 13 contamination-detection spectral fields (entropy plus the
// full spectral set) with the values measured on the NoiseProfile. No consumer
// reads them yet, but they are the measurement spine the JSON artefact must
// expose, so they must reach regions.room_tone.elected unchanged rather than
// being dropped on the way to the record.
func TestRunRecordNoiseProfileSpectralFields(t *testing.T) {
	rec := NewAnalysisRunRecord("/tmp/episode.flac", populatedAudioMeasurements())
	tree, raw := marshalRecordTree(t, rec)

	regions, ok := tree["regions"].(map[string]any)
	if !ok {
		t.Fatal("missing regions block")
	}
	roomTone, ok := regions["room_tone"].(map[string]any)
	if !ok {
		t.Fatal("missing regions.room_tone block")
	}
	elected, ok := roomTone["elected"].(map[string]any)
	if !ok {
		t.Fatalf("missing regions.room_tone.elected noise profile\n%s", raw)
	}

	// Values come from populatedAudioMeasurements' NoiseProfile fixture.
	cases := []struct {
		key  string
		want float64
	}{
		{"entropy", 0.5},
		{"spectral_mean", 1.2},
		{"spectral_variance", 2.4},
		{"spectral_centroid_hz", 1500},
		{"spectral_spread_hz", 350},
		{"spectral_skewness", 0.8},
		{"spectral_kurtosis", 3},
		{"spectral_entropy", 0.55},
		{"spectral_flatness", 0.4},
		{"spectral_crest", 7.5},
		{"spectral_flux", 0.03},
		{"spectral_slope", -0.25},
		{"spectral_decrease", 0.11},
		{"spectral_rolloff_hz", 6500},
	}
	for _, c := range cases {
		got, present := elected[c.key]
		if !present {
			t.Errorf("regions.room_tone.elected missing %q", c.key)
			continue
		}
		num, isNum := got.(float64)
		if !isNum {
			t.Errorf("%q = %v (%T), want number", c.key, got, got)
			continue
		}
		if math.Abs(num-c.want) > 0.001 {
			t.Errorf("%q = %v, want %v", c.key, num, c.want)
		}
	}
}

// TestRunRecordNoiseProfileSpectralFieldsZeroValued confirms the 13 spectral
// fields keep a stable key set when a metric averages to exactly 0.0. With
// `,omitempty` removed from their tags, a zero-valued spectral field must still
// serialise into regions.room_tone.elected rather than dropping out, so the key
// set is identical file-to-file regardless of measured values.
func TestRunRecordNoiseProfileSpectralFieldsZeroValued(t *testing.T) {
	m := populatedAudioMeasurements()
	// Zero out a representative spread: variance, skewness, flux, and decrease.
	// Each would have dropped under the old `,omitempty` tags.
	m.Regions.NoiseProfile.SpectralVariance = 0
	m.Regions.NoiseProfile.SpectralSkewness = 0
	m.Regions.NoiseProfile.SpectralFlux = 0
	m.Regions.NoiseProfile.SpectralDecrease = 0

	rec := NewAnalysisRunRecord("/tmp/episode.flac", m)
	tree, raw := marshalRecordTree(t, rec)

	regions, ok := tree["regions"].(map[string]any)
	if !ok {
		t.Fatal("missing regions block")
	}
	roomTone, ok := regions["room_tone"].(map[string]any)
	if !ok {
		t.Fatal("missing regions.room_tone block")
	}
	elected, ok := roomTone["elected"].(map[string]any)
	if !ok {
		t.Fatalf("missing regions.room_tone.elected noise profile\n%s", raw)
	}

	// All 13 spectral keys must be present even when the value is 0.0.
	spectralKeys := []string{
		"spectral_mean", "spectral_variance", "spectral_centroid_hz",
		"spectral_spread_hz", "spectral_skewness", "spectral_kurtosis",
		"spectral_entropy", "spectral_flatness", "spectral_crest",
		"spectral_flux", "spectral_slope", "spectral_decrease", "spectral_rolloff_hz",
	}
	for _, key := range spectralKeys {
		if _, present := elected[key]; !present {
			t.Errorf("regions.room_tone.elected missing %q when value is zero\n%s", key, raw)
		}
	}

	// The zeroed fields must serialise as a numeric 0, not drop or null.
	for _, key := range []string{"spectral_variance", "spectral_skewness", "spectral_flux", "spectral_decrease"} {
		got, present := elected[key]
		if !present {
			t.Errorf("zero-valued %q dropped from record", key)
			continue
		}
		num, isNum := got.(float64)
		if !isNum {
			t.Errorf("%q = %v (%T), want numeric 0", key, got, got)
			continue
		}
		if num != 0 {
			t.Errorf("%q = %v, want 0", key, num)
		}
	}
}

// TestRegionSampleJSON_HasNoElectionFields confirms the bare RegionSample used for
// the Pass 2/4 output samples (regions.<kind>.samples.<stage>) carries only
// amplitude/spectral/loudness keys, no scoring/stability/voicing/band keys that
// would read as a real measurement when stale-zero (§8.2 last row).
func TestRegionSampleJSON_HasNoElectionFields(t *testing.T) {
	sample := RegionSample{
		RMSLevel: -20, PeakLevel: -3, CrestFactor: 13,
		Spectral:      SpectralMetrics{Centroid: 2000, Found: true},
		MomentaryLUFS: -19, ShortTermLUFS: -18, TruePeak: -2, SamplePeak: -2.5,
	}
	data, err := json.Marshal(sample)
	if err != nil {
		t.Fatalf("Marshal() failed: %v", err)
	}

	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}
	keys := map[string]bool{}
	collectJSONKeys(tree, keys)

	for _, key := range []string{
		"score", "stability_score", "voicing_density",
		"speech_band_body_rms_dbfs", "speech_band_sib_rms_dbfs",
		"speech_bands_measured", "transient_warning",
	} {
		if keys[key] {
			t.Errorf("RegionSample must not emit election key %q", key)
		}
	}

	for _, key := range []string{
		"rms_level_dbfs", "peak_level_dbfs", "crest_factor_db",
		"momentary_lufs", "short_term_lufs", "true_peak_dbtp", "sample_peak_dbfs",
		"centroid_hz",
	} {
		if !keys[key] {
			t.Errorf("RegionSample missing measurement key %q", key)
		}
	}
}

// jsonKeySet marshals v and returns the set of every object key at any depth.
func jsonKeySet(t *testing.T, v any) map[string]bool {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal() failed: %v", err)
	}
	var tree any
	if err := json.Unmarshal(data, &tree); err != nil {
		t.Fatalf("Unmarshal() failed: %v", err)
	}
	keys := map[string]bool{}
	collectJSONKeys(tree, keys)
	return keys
}

// TestEffectiveFilterConfigJSON_HasCanonicalKeys marshals a populated
// EffectiveFilterConfig (the §8.1 `filters` block) and asserts the §8.4 keys for
// the six adaptive sub-configs while excluding the orchestration configs and the
// plumbing FilterOrder.
func TestEffectiveFilterConfigJSON_HasCanonicalKeys(t *testing.T) {
	cfg := EffectiveFilterConfig{
		RumbleHighPass:      RumbleHighPassConfig{Enabled: true, Frequency: 80, Poles: 2, Width: 0.707, Mix: 1.0, Transform: "tdii"},
		BandlimitLowPass:    BandlimitLowPassConfig{Enabled: true, Frequency: 20500, Poles: 2, Width: 0.707, Mix: 1.0, Transform: "tdii"},
		NoiseReduction:      NoiseReductionConfig{Enabled: true, Strength: 0.002, PatchSec: 0.02, ResearchSec: 0.06, Smooth: 11, AfftdnEnabled: true, AfftdnNoiseReduction: 12, AfftdnNoiseType: "custom", AfftdnBandNoise: "0.0|1.0", AfftdnTrackNoise: true},
		SpeechGate:          SpeechGateConfig{Enabled: true, Threshold: 0.01, Ratio: 2.0, Attack: 10, Release: 250, Range: 0.05, Knee: 3.0, Makeup: 1.0, Detection: "rms"},
		LevellingCompressor: LevellingCompressorConfig{Enabled: true, Threshold: -18, Ratio: 3.0, Attack: 10, Release: 200, Makeup: 0, Knee: 4.0, Mix: 1.0},
		Deesser:             DeesserConfig{Enabled: true, Intensity: 0.6, Amount: 0.5, Frequency: 0.8},
		// Orchestration configs + FilterOrder must be excluded from the record.
		Downmix:     DownmixConfig{Enabled: true},
		Adeclick:    AdeclickConfig{Enabled: true, Threshold: 2.0},
		FilterOrder: []FilterID{FilterDownmix, FilterSpeechGate},
	}

	keys := jsonKeySet(t, cfg)

	for _, key := range []string{
		// sub-config containers
		"rumble_highpass", "bandlimit_lowpass", "noise_reduction", "speech_gate", "levelling_compressor", "deesser",
		// gate (§8.4)
		"threshold_db", "ratio", "attack_ms", "release_ms", "range_db", "knee", "makeup", "detection",
		// levelling_compressor
		"makeup_db",
		// hp/lp
		"frequency_hz", "poles_count", "width", "mix", "transform",
		// noise_reduction
		"strength", "patch_s", "research_s", "smooth",
		"afftdn_noise_reduction_db", "afftdn_noise_type", "afftdn_track_noise", "afftdn_band_noise",
		// deesser
		"intensity", "amount", "frequency",
	} {
		if !keys[key] {
			t.Errorf("missing canonical §8.4 filters key %q", key)
		}
	}

	for _, key := range []string{
		// orchestration configs + plumbing excluded
		"downmix", "analysis", "resample", "adeclick", "loudnorm",
		"filter_order", "FilterOrder",
		// legacy camel / no-unit forms
		"Threshold", "Ratio", "Attack", "Release", "Range", "Frequency",
		"threshold", "attack", "release", "frequency_hz_corner",
	} {
		if keys[key] {
			t.Errorf("filters block must not emit key %q", key)
		}
	}
}

// TestAdaptiveDiagnosticsJSON_HasCanonicalKeys asserts the §8.4 keys for the
// retained processing-state diagnostics (§8.3): reason/clamp strings stay.
func TestAdaptiveDiagnosticsJSON_HasCanonicalKeys(t *testing.T) {
	diag := AdaptiveDiagnostics{
		BandlimitLPReason:             "20.5 kHz band-limit (always on)",
		SpeechGateDynamicRange:        14,
		SpeechGateQuietSpeechEstimate: -52,
		SpeechGateSpeechSeparation:    8,
		SpeechGateSpeechHeadroom:      3,
		SpeechGateThresholdUnclamped:  -45,
		SpeechGateClampReason:         "noise_floor",
		SpeechGateDepthDB:             14,
	}

	keys := jsonKeySet(t, diag)

	for _, key := range []string{
		"bandlimit_lowpass_reason", "dynamic_range_db",
		"quiet_speech_estimate_dbfs", "separation_db", "speech_headroom_db",
		"threshold_unclamped_db", "clamp_reason", "speech_gate_depth_db",
	} {
		if !keys[key] {
			t.Errorf("missing canonical §8.4 diagnostics key %q", key)
		}
	}

	for _, key := range []string{
		"SpeechGateAggression", "SpeechGateSpeechSeparation", "SpeechGateClampReason",
		"separation", "aggression", "aggression_index",
	} {
		if keys[key] {
			t.Errorf("diagnostics must not emit legacy key %q", key)
		}
	}
}

// TestNormalisationResultJSON_HasCanonicalKeys asserts the §8.4 keys for the
// `normalisation` block, that LoudnormStats nests under loudnorm_measured, and
// that FinalMeasurements is excluded (assembled into the loudness/dynamics/spectral
// stages instead).
func TestNormalisationResultJSON_HasCanonicalKeys(t *testing.T) {
	res := NormalisationResult{
		InputLUFS: -18, InputTP: -2, OutputLUFS: -16, OutputTP: -2,
		GainApplied: 2, WithinTarget: true, Skipped: false,
		LoudnormStats:    &LoudnormStats{InputI: "-18.0", NormalizationType: "linear", TargetOffset: "0.1"},
		RequestedTargetI: -16, EffectiveTargetI: -16,
		LinearModeForced: false, ActualNormDynamic: false,
		LimiterDiagnostics: LimiterDiagnostics{
			LimiterEnabled: true, LimiterCeiling: -2.4, LimiterGain: 6,
			LimiterFilteredTP: -1, PreGainDB: 0, LimiterClamped: false,
		},
		Pass3FilterPrefix:     "volume=...,alimiter=...",
		RegionMeasurementTime: 1234,
		FinalMeasurements:     &OutputMeasurements{},
	}

	keys := jsonKeySet(t, res)

	for _, key := range []string{
		"input_lufs", "input_dbtp", "output_lufs", "output_dbtp",
		"gain_applied_db", "within_target", "skipped", "loudnorm_measured",
		"requested_target_lufs", "effective_target_lufs", "linear_mode_forced",
		"actual_norm_dynamic", "limiter_enabled", "ceiling_dbtp", "gain_db",
		"filtered_dbtp", "pre_gain_db", "limiter_clamped", "pass3_filter_prefix",
		"region_measurement_ns",
		// LoudnormStats nested keys (unchanged FFmpeg loudnorm JSON keys)
		"input_i", "normalization_type", "target_offset",
	} {
		if !keys[key] {
			t.Errorf("missing canonical §8.4 normalisation key %q", key)
		}
	}

	for _, key := range []string{
		// FinalMeasurements excluded (no nested final-stage duplicate)
		"final_measurements", "FinalMeasurements",
		// legacy camel / no-unit forms (NormalisationResult's own fields).
		// Note: input_tp/output_tp legitimately appear inside the nested
		// loudnorm_measured block (LoudnormStats keeps FFmpeg's JSON keys), so they
		// are NOT asserted absent here.
		"InputLUFS", "LimiterCeiling", "PreGainDB", "EffectiveTargetI", "GainApplied",
		"gain_applied", "limiter_ceiling", "effective_target_i",
	} {
		if keys[key] {
			t.Errorf("normalisation block must not emit key %q", key)
		}
	}
}
