package processor

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
	"time"
)

// populatedOutputMeasurements builds an OutputMeasurements covering the
// loudness/dynamics/spectral domain blocks for the filtered/final stages.
func populatedOutputMeasurements() *OutputMeasurements {
	spectral := SpectralMetrics{
		Mean: 1, Variance: 2, Centroid: 2100, Spread: 410, Skewness: 1,
		Kurtosis: 4, Entropy: 0.4, Flatness: 0.6, Crest: 8, Flux: 0.04,
		Slope: -0.2, Decrease: 0.12, Rolloff: 7100, Found: true,
	}
	return &OutputMeasurements{
		Spectral: spectral,
		Loudness: OutputLoudnessMetrics{
			LoudnessMetrics: LoudnessMetrics{MomentaryLoudness: -16.5, ShortTermLoudness: -16, SamplePeak: -1.1},
			OutputI:         -16, OutputTP: -1.5, OutputLRA: 6, OutputThresh: -26, TargetOffset: -1,
		},
		Dynamics: DynamicsMetrics{
			DynamicRange: 11, RMSLevel: -20, PeakLevel: -2, RMSTrough: -42, RMSPeak: -17,
			CrestFactor: 13, BitDepth: 16, NumberOfSamples: 480000,
		},
	}
}

// populatedProcessingResult builds a full ProcessingResult exercising every
// RunRecord stage and processing block, in-memory (no testdata/).
func populatedProcessingResult() *ProcessingResult {
	cfg := EffectiveFilterConfig(DefaultFilterConfig().filterConfigDefaults)
	// Gate Threshold/Range are stored LINEAR on the live config; set realistic
	// linear amplitudes so the dB conversion in the filters block is exercised.
	cfg.SpeechGate.Threshold = Decibels(-45).LinearAmplitude().Float64()
	cfg.SpeechGate.Range = Decibels(-22).LinearAmplitude().Float64()

	final := populatedOutputMeasurements()

	return &ProcessingResult{
		OutputPath:           "/tmp/episode-LUFS-16-processed.flac",
		Measurements:         populatedAudioMeasurements(),
		Config:               &cfg,
		Diagnostics:          &AdaptiveDiagnostics{SpeechGateDynamicRange: 0.4, SpeechGateClampReason: "none"},
		FilteredMeasurements: populatedOutputMeasurements(),
		NormResult: &NormalisationResult{
			InputLUFS: -18, OutputLUFS: -16, EffectiveTargetI: -16,
			FinalMeasurements:     final,
			RegionMeasurementTime: 1500 * time.Millisecond,
			LoudnormStats: &LoudnormStats{
				InputI: "-18.5", InputTP: "-1.2", InputLRA: "7.3", InputThresh: "-28.9",
				OutputI: "-16.0", OutputTP: "-1.5", OutputLRA: "6.1", OutputThresh: "-26.4",
				TargetOffset: "-0.3", NormalizationType: "linear",
			},
		},
		InputMetadata: InputMetadata{SampleRate: 48000, Channels: 1, DurationSecs: 600},
	}
}

func marshalRecordTree(t *testing.T, r *RunRecord) (map[string]any, []byte) {
	t.Helper()
	raw, err := MarshalRunRecord(r)
	if err != nil {
		t.Fatalf("MarshalRunRecord error: %v", err)
	}
	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("decode marshalled record: %v\n%s", err, raw)
	}
	return tree, raw
}

func TestRunRecord_FullShape(t *testing.T) {
	rec := NewRunRecord(populatedProcessingResult())
	tree, _ := marshalRecordTree(t, rec)

	// (a) schema_version is an int at root.
	sv, ok := tree["schema_version"]
	if !ok {
		t.Fatal("missing schema_version at root")
	}
	if f, isNum := sv.(float64); !isNum || f != 1 || f != math.Trunc(f) {
		t.Fatalf("schema_version not an int 1: %v (%T)", sv, sv)
	}

	// (b) loudness.stages.{input,filtered,final} present.
	loud, ok := tree["loudness"].(map[string]any)
	if !ok {
		t.Fatal("missing loudness block")
	}
	stages, ok := loud["stages"].(map[string]any)
	if !ok {
		t.Fatal("missing loudness.stages")
	}
	for _, stage := range []string{"input", "filtered", "final"} {
		if _, ok := stages[stage].(map[string]any); !ok {
			t.Errorf("loudness.stages.%s absent or wrong type", stage)
		}
	}

	// filters and normalisation blocks present on a full record.
	if _, ok := tree["filters"].(map[string]any); !ok {
		t.Error("missing filters block on full record")
	}
	if _, ok := tree["normalisation"].(map[string]any); !ok {
		t.Error("missing normalisation block on full record")
	}

	// run provenance is sourced from InputMetadata.
	run := tree["run"].(map[string]any)
	if run["sample_rate_hz"].(float64) != 48000 {
		t.Errorf("sample_rate_hz = %v, want 48000", run["sample_rate_hz"])
	}
	if run["channels"].(float64) != 1 {
		t.Errorf("channels = %v, want 1", run["channels"])
	}
	if run["input_file"].(string) != "episode-LUFS-16-processed.flac" {
		t.Errorf("input_file = %v, want basename", run["input_file"])
	}
}

func TestRunRecord_AnalysisOnlyDropsProcessingBlocks(t *testing.T) {
	rec := NewAnalysisRunRecord("/tmp/episode.flac", populatedAudioMeasurements())
	tree, raw := marshalRecordTree(t, rec)

	loud := tree["loudness"].(map[string]any)
	stages := loud["stages"].(map[string]any)
	if _, ok := stages["input"]; !ok {
		t.Error("analysis-only record missing loudness.stages.input")
	}

	// (c) filtered/final/filters/normalisation DROP (nil + omitempty, not null).
	if _, ok := stages["filtered"]; ok {
		t.Error("analysis-only record must drop loudness.stages.filtered")
	}
	if _, ok := stages["final"]; ok {
		t.Error("analysis-only record must drop loudness.stages.final")
	}
	if _, ok := tree["filters"]; ok {
		t.Error("analysis-only record must drop filters block")
	}
	if _, ok := tree["normalisation"]; ok {
		t.Error("analysis-only record must drop normalisation block")
	}

	// Assert true omission, not a null value, at the JSON byte level.
	for _, key := range []string{`"filters"`, `"normalisation"`} {
		if bytes.Contains(raw, []byte(key)) {
			t.Errorf("key %s present in analysis-only JSON; want absent", key)
		}
	}
}

func TestRunRecord_NonFiniteFloatSerialisesAsNull(t *testing.T) {
	m := populatedAudioMeasurements()
	m.Dynamics.RMSLevel = math.NaN()
	m.Loudness.InputTP = math.Inf(1)

	rec := NewAnalysisRunRecord("/tmp/episode.flac", m)
	raw, err := MarshalRunRecord(rec)
	if err != nil {
		t.Fatalf("MarshalRunRecord must not error on non-finite floats: %v", err)
	}

	var tree map[string]any
	if err := json.Unmarshal(raw, &tree); err != nil {
		t.Fatalf("decode: %v\n%s", err, raw)
	}

	dyn := tree["dynamics"].(map[string]any)["stages"].(map[string]any)["input"].(map[string]any)
	if dyn["rms_level_dbfs"] != nil {
		t.Errorf("NaN rms_level_dbfs = %v, want null", dyn["rms_level_dbfs"])
	}
	loud := tree["loudness"].(map[string]any)["stages"].(map[string]any)["input"].(map[string]any)
	if loud["true_peak_dbtp"] != nil {
		t.Errorf("+Inf true_peak_dbtp = %v, want null", loud["true_peak_dbtp"])
	}
}

// TestRunRecord_RegionsNestedShape asserts the §8.1 nested regions structure on a
// full (processing) record: room_tone and speech each carry elected, candidates,
// and samples; the elected profiles are present; the speech input sample wires
// from SpeechProfile.RegionSample; and filtered/final samples wire from the
// output measurements.
func TestRunRecord_RegionsNestedShape(t *testing.T) {
	result := populatedProcessingResult()
	// Wire output region samples on both filtered and final stages so the
	// before/after sample plumbing is exercised.
	rtSample := &RegionSample{RMSLevel: -55, PeakLevel: -45, CrestFactor: 10}
	spSample := &RegionSample{RMSLevel: -19, PeakLevel: -2, CrestFactor: 17}
	result.FilteredMeasurements.RoomToneSample = rtSample
	result.FilteredMeasurements.SpeechSample = spSample
	result.NormResult.FinalMeasurements.RoomToneSample = rtSample
	result.NormResult.FinalMeasurements.SpeechSample = spSample

	rec := NewRunRecord(result)
	tree, _ := marshalRecordTree(t, rec)

	regions, ok := tree["regions"].(map[string]any)
	if !ok {
		t.Fatal("missing nested regions block")
	}

	// Old flat keys must NOT appear directly under regions.
	for _, flat := range []string{
		"speech_candidates", "noise_profile",
		"speech_profile", "speech_regions", "interval_samples",
	} {
		if _, present := regions[flat]; present {
			t.Errorf("regions must not emit old flat key %q under nested shape", flat)
		}
	}

	rt, ok := regions["room_tone"].(map[string]any)
	if !ok {
		t.Fatal("missing regions.room_tone")
	}
	sp, ok := regions["speech"].(map[string]any)
	if !ok {
		t.Fatal("missing regions.speech")
	}

	// Full candidate arrays moved to the sidecar (§9.3); the inline record keeps
	// elected + samples for both kinds, plus a candidates_summary for speech only
	// (room tone carries no candidate summary).
	for _, key := range []string{"elected", "samples"} {
		if _, present := rt[key]; !present {
			t.Errorf("regions.room_tone missing %q", key)
		}
		if _, present := sp[key]; !present {
			t.Errorf("regions.speech missing %q", key)
		}
	}
	if _, present := sp["candidates_summary"]; !present {
		t.Error("regions.speech missing \"candidates_summary\"")
	}
	if _, present := rt["candidates_summary"]; present {
		t.Error("regions.room_tone must not emit \"candidates_summary\"")
	}
	// The full candidate arrays must NOT be inline.
	if _, present := rt["candidates"]; present {
		t.Error("regions.room_tone must not inline the full candidates array (sidecar)")
	}
	if _, present := sp["candidates"]; present {
		t.Error("regions.speech must not inline the full candidates array (sidecar)")
	}

	// Elected profiles populated.
	if _, ok := rt["elected"].(map[string]any); !ok {
		t.Error("regions.room_tone.elected absent or wrong type")
	}
	if _, ok := sp["elected"].(map[string]any); !ok {
		t.Error("regions.speech.elected absent or wrong type")
	}

	// Speech input sample populated from SpeechProfile.RegionSample.
	spSamples := sp["samples"].(map[string]any)
	spInput, ok := spSamples["input"].(map[string]any)
	if !ok {
		t.Fatal("regions.speech.samples.input absent; want populated from SpeechProfile.RegionSample")
	}
	if _, ok := spInput["rms_level_dbfs"]; !ok {
		t.Error("regions.speech.samples.input missing measurement key rms_level_dbfs")
	}
	// The input sample must not carry election fields (slimmer schema, §8.2).
	for _, key := range []string{"score", "stability_score", "voicing_density"} {
		if _, present := spInput[key]; present {
			t.Errorf("regions.speech.samples.input must not emit election key %q", key)
		}
	}

	// Filtered/final samples populate on a full record.
	for _, kind := range []string{"room_tone", "speech"} {
		samples := regions[kind].(map[string]any)["samples"].(map[string]any)
		for _, stage := range []string{"filtered", "final"} {
			if _, ok := samples[stage].(map[string]any); !ok {
				t.Errorf("regions.%s.samples.%s absent on full record", kind, stage)
			}
		}
	}

	// Room-tone input sample is populated from the elected candidate's RegionSample
	// captured at election.
	rtSamples := rt["samples"].(map[string]any)
	rtInput, ok := rtSamples["input"].(map[string]any)
	if !ok {
		t.Fatal("regions.room_tone.samples.input absent; want populated from ElectedRoomToneSample")
	}
	if _, ok := rtInput["rms_level_dbfs"]; !ok {
		t.Error("regions.room_tone.samples.input missing measurement key rms_level_dbfs")
	}
	// The input sample carries only measurement fields (slimmer RegionSample schema),
	// never election fields.
	for _, key := range []string{"score", "stability_score", "transient_warning"} {
		if _, present := rtInput[key]; present {
			t.Errorf("regions.room_tone.samples.input must not emit election key %q", key)
		}
	}
}

// TestRunRecord_RegionsAnalysisOnlyDropsSamples asserts the before/after samples
// drop in an analysis-only record (no FilteredMeasurements / NormResult): the
// nested elected/candidates stay, but filtered/final samples are omitted.
func TestRunRecord_RegionsAnalysisOnlyDropsSamples(t *testing.T) {
	rec := NewAnalysisRunRecord("/tmp/episode.flac", populatedAudioMeasurements())
	tree, _ := marshalRecordTree(t, rec)

	regions := tree["regions"].(map[string]any)
	for _, kind := range []string{"room_tone", "speech"} {
		block := regions[kind].(map[string]any)
		if _, ok := block["elected"].(map[string]any); !ok {
			t.Errorf("regions.%s.elected must stay in analysis-only record", kind)
		}
		samples, ok := block["samples"].(map[string]any)
		if !ok {
			continue // samples object dropped entirely is acceptable when empty
		}
		for _, stage := range []string{"filtered", "final"} {
			if _, present := samples[stage]; present {
				t.Errorf("analysis-only record must drop regions.%s.samples.%s", kind, stage)
			}
		}
	}

	// Speech input sample still wires from the elected profile in analysis-only.
	spSamples, ok := regions["speech"].(map[string]any)["samples"].(map[string]any)
	if !ok {
		t.Fatal("regions.speech.samples absent; want input populated in analysis-only")
	}
	if _, ok := spSamples["input"].(map[string]any); !ok {
		t.Error("regions.speech.samples.input must populate from SpeechProfile in analysis-only")
	}
}

// TestRunRecord_RegionDurationsAreSeconds asserts region time bounds emit as _s
// float seconds, not raw nanoseconds, and the ns keys are absent.
func TestRunRecord_RegionDurationsAreSeconds(t *testing.T) {
	rec := NewRunRecord(populatedProcessingResult())
	tree, _ := marshalRecordTree(t, rec)

	regions := tree["regions"].(map[string]any)

	rtElected := regions["room_tone"].(map[string]any)["elected"].(map[string]any)
	// NoiseProfile Start=2s, Duration=10s -> seconds floats under _s keys.
	if got, ok := rtElected["duration_s"].(float64); !ok || got != 10 {
		t.Errorf("room_tone.elected.duration_s = %v (ok=%v), want 10", rtElected["duration_s"], ok)
	}
	if got, ok := rtElected["start_s"].(float64); !ok || got != 2 {
		t.Errorf("room_tone.elected.start_s = %v (ok=%v), want 2", rtElected["start_s"], ok)
	}
	// Raw ns keys must be gone.
	for _, ns := range []string{"start", "duration"} {
		if _, present := rtElected[ns]; present {
			t.Errorf("room_tone.elected must not emit raw ns key %q", ns)
		}
	}

	spRegion := regions["speech"].(map[string]any)["elected"].(map[string]any)["region"].(map[string]any)
	if got, ok := spRegion["duration_s"].(float64); !ok || got != 10 {
		t.Errorf("speech.elected.region.duration_s = %v (ok=%v), want 10", spRegion["duration_s"], ok)
	}
	if got, ok := spRegion["end_s"].(float64); !ok || got != 40 {
		t.Errorf("speech.elected.region.end_s = %v (ok=%v), want 40", spRegion["end_s"], ok)
	}
	for _, ns := range []string{"start", "end", "duration"} {
		if _, present := spRegion[ns]; present {
			t.Errorf("speech.elected.region must not emit raw ns key %q", ns)
		}
	}

	// normalisation.region_measurement_s (1.5s) replaces region_measurement_ns.
	norm := tree["normalisation"].(map[string]any)
	if got, ok := norm["region_measurement_s"].(float64); !ok || got != 1.5 {
		t.Errorf("normalisation.region_measurement_s = %v (ok=%v), want 1.5", norm["region_measurement_s"], ok)
	}
	if _, present := norm["region_measurement_ns"]; present {
		t.Error("normalisation must not emit raw region_measurement_ns")
	}
}

// TestRunRecord_LoudnormMeasuredNumeric asserts normalisation.loudnorm_measured is
// the §8.4 numeric sub-block, not FFmpeg's raw string keys, with normalization_type
// kept as a categorical string.
func TestRunRecord_LoudnormMeasuredNumeric(t *testing.T) {
	rec := NewRunRecord(populatedProcessingResult())
	tree, _ := marshalRecordTree(t, rec)

	ln := tree["normalisation"].(map[string]any)["loudnorm_measured"].(map[string]any)

	wantFloats := map[string]float64{
		"input_integrated_lufs": -18.5, "input_true_peak_dbtp": -1.2,
		"input_lra_lu": 7.3, "input_thresh_lufs": -28.9,
		"output_integrated_lufs": -16.0, "output_true_peak_dbtp": -1.5,
		"output_lra_lu": 6.1, "output_thresh_lufs": -26.4,
		"target_offset_db": -0.3,
	}
	for key, want := range wantFloats {
		got, ok := ln[key].(float64)
		if !ok {
			t.Errorf("loudnorm_measured.%s missing or not a number: %v", key, ln[key])
			continue
		}
		if got != want {
			t.Errorf("loudnorm_measured.%s = %v, want %v", key, got, want)
		}
	}

	// normalization_type stays a string (categorical, not a measurement).
	if nt, ok := ln["normalization_type"].(string); !ok || nt != "linear" {
		t.Errorf("loudnorm_measured.normalization_type = %v, want string \"linear\"", ln["normalization_type"])
	}

	// Raw FFmpeg string keys must be gone.
	for _, raw := range []string{"input_i", "input_tp", "output_tp", "target_offset"} {
		if _, present := ln[raw]; present {
			t.Errorf("loudnorm_measured must not emit raw FFmpeg key %q", raw)
		}
	}
}

// TestLoudnormMeasuredNumeric_GracefulParseFailure asserts an unparseable loudnorm
// string omits its field rather than crashing or fabricating a zero.
func TestLoudnormMeasuredNumeric_GracefulParseFailure(t *testing.T) {
	stats := &LoudnormStats{
		InputI: "-18.5", InputTP: "not-a-number", NormalizationType: "dynamic",
	}
	out := loudnormMeasuredNumeric(stats)
	if _, present := out["input_true_peak_dbtp"]; present {
		t.Error("unparseable input_tp must be omitted, not fabricated")
	}
	if got, ok := out["input_integrated_lufs"].(float64); !ok || got != -18.5 {
		t.Errorf("input_integrated_lufs = %v, want -18.5", out["input_integrated_lufs"])
	}
	if out["normalization_type"] != "dynamic" {
		t.Errorf("normalization_type = %v, want dynamic", out["normalization_type"])
	}
}

func TestRunRecord_GateThresholdIsDecibels(t *testing.T) {
	rec := NewRunRecord(populatedProcessingResult())
	tree, _ := marshalRecordTree(t, rec)

	gate := tree["filters"].(map[string]any)["speech_gate"].(map[string]any)

	thr, ok := gate["threshold_db"].(float64)
	if !ok {
		t.Fatalf("threshold_db missing or wrong type: %v", gate["threshold_db"])
	}
	// The live config stored ~0.0056 linear (-45 dB). The record must carry the
	// honest dB value (negative-ish), not the tiny linear amplitude.
	if thr > -1 {
		t.Errorf("threshold_db = %v, want a dB value near -45, not a linear amplitude", thr)
	}
	if thr < -90 || thr > -20 {
		t.Errorf("threshold_db = %v out of expected dB range", thr)
	}

	rng, ok := gate["range_db"].(float64)
	if !ok {
		t.Fatalf("range_db missing or wrong type: %v", gate["range_db"])
	}
	if rng > -1 {
		t.Errorf("range_db = %v, want a dB value near -22, not a linear amplitude", rng)
	}
}
