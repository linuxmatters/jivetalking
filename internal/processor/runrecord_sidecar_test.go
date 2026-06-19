package processor

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

// TestRunRecord_IntervalSummaryInlineSeriesAbsent asserts the interval split: the
// inline record carries interval_summary (count + RMS distribution + largest gap)
// and never the full interval_samples series.
func TestRunRecord_IntervalSummaryInlineSeriesAbsent(t *testing.T) {
	m := populatedAudioMeasurements()
	m.Regions.IntervalSamples = syntheticIntervals(20)

	rec := NewAnalysisRunRecord("/tmp/episode.flac", m)
	tree, raw := marshalRecordTree(t, rec)

	summary, ok := tree["interval_summary"].(map[string]any)
	if !ok {
		t.Fatal("missing interval_summary block")
	}
	if summary["count"].(float64) != 20 {
		t.Errorf("interval_summary.count = %v, want 20", summary["count"])
	}
	dist, ok := summary["rms_distribution"].(map[string]any)
	if !ok {
		t.Fatal("missing interval_summary.rms_distribution")
	}
	for _, key := range []string{"min_dbfs", "p10_dbfs", "p25_dbfs", "p50_dbfs", "p75_dbfs", "p90_dbfs", "max_dbfs"} {
		if _, present := dist[key]; !present {
			t.Errorf("rms_distribution missing %q", key)
		}
	}
	if _, present := summary["largest_gap_db"]; !present {
		t.Error("interval_summary missing largest_gap_db")
	}

	// The full series must be absent everywhere in the record.
	if bytes.Contains(raw, []byte("interval_samples")) {
		t.Error("record must not inline interval_samples (sidecar only)")
	}
}

// TestNewIntervalSummary_MatchesReportMaths asserts the percentile/gap selection
// uses the integer-index maths the inline summary reports verbatim.
func TestNewIntervalSummary_MatchesReportMaths(t *testing.T) {
	// 11 distinct RMS values above the -120 silence floor.
	vals := []float64{-70, -68, -66, -64, -62, -40, -38, -36, -34, -32, -30}
	samples := make([]IntervalSample, 0, len(vals)+1)
	samples = append(samples, IntervalSample{RMSLevel: -130}) // digital silence, excluded
	for _, v := range vals {
		samples = append(samples, IntervalSample{RMSLevel: v})
	}

	s := newIntervalSummary(samples)
	if s.Count != len(samples) {
		t.Errorf("Count = %d, want %d (includes silence interval)", s.Count, len(samples))
	}
	if s.RMS == nil {
		t.Fatal("RMS distribution nil, want populated (>=10 non-silence intervals)")
	}
	// sorted = vals (already ascending); n = 11. Index selection per report.
	want := RMSDistribution{
		Min: vals[0], P10: vals[11/10], P25: vals[11/4], P50: vals[11/2],
		P75: vals[11*3/4], P90: vals[11*9/10], Max: vals[len(vals)-1],
	}
	if *s.RMS != want {
		t.Errorf("RMS distribution = %+v, want %+v", *s.RMS, want)
	}
	// Largest gap is the 22 dB jump from -62 to -40.
	if s.LargestGapDB == nil || *s.LargestGapDB != 22 {
		t.Errorf("largest gap = %v, want 22", s.LargestGapDB)
	}
}

// TestNewIntervalSummary_BelowThresholdDropsDistribution asserts the threshold
// rule: fewer than 10 non-silence intervals yields count only, no distribution/gap.
func TestNewIntervalSummary_BelowThresholdDropsDistribution(t *testing.T) {
	samples := syntheticIntervals(5)
	s := newIntervalSummary(samples)
	if s == nil || s.Count != 5 {
		t.Fatalf("summary = %+v, want count 5", s)
	}
	if s.RMS != nil || s.LargestGapDB != nil {
		t.Error("distribution/gap must drop below 10 non-silence intervals")
	}
}

// TestRunRecord_CandidatesSummaryInlineArraysAbsent asserts the candidate split:
// speech carries a candidates_summary (count + elected score), room tone carries
// none, and neither kind inlines the full candidate array.
func TestRunRecord_CandidatesSummaryInlineArraysAbsent(t *testing.T) {
	rec := NewRunRecord(populatedProcessingResult())
	tree, raw := marshalRecordTree(t, rec)

	regions := tree["regions"].(map[string]any)

	// Speech carries a candidates_summary with an evaluated_count.
	speech := regions["speech"].(map[string]any)
	cs, ok := speech["candidates_summary"].(map[string]any)
	if !ok {
		t.Error("regions.speech missing candidates_summary")
	} else if _, present := cs["evaluated_count"]; !present {
		t.Error("regions.speech.candidates_summary missing evaluated_count")
	}

	// Room tone carries no candidates_summary.
	if _, present := regions["room_tone"].(map[string]any)["candidates_summary"]; present {
		t.Error("regions.room_tone must not carry a candidates_summary")
	}

	// Neither kind inlines the full candidate array.
	for _, kind := range []string{"room_tone", "speech"} {
		if _, present := regions[kind].(map[string]any)["candidates"]; present {
			t.Errorf("regions.%s must not inline full candidates array", kind)
		}
	}

	// Speech elected score is present (SpeechProfile aliases an evaluated candidate).
	spcs := regions["speech"].(map[string]any)["candidates_summary"].(map[string]any)
	if _, present := spcs["elected_score"]; !present {
		t.Error("speech candidates_summary missing elected_score")
	}

	if bytes.Contains(raw, []byte("speech_candidates")) {
		t.Error("record must not inline the full candidate arrays")
	}
}

// TestWriteIntervalsSidecar_OneLinePerSample asserts the streaming writer emits
// exactly N lines for N intervals, each a valid JSON object with the flattened
// spectral_* keys (IntervalSample.MarshalJSON).
func TestWriteIntervalsSidecar_OneLinePerSample(t *testing.T) {
	samples := syntheticIntervals(7)
	var buf bytes.Buffer
	if err := streamIntervals(&buf, samples); err != nil {
		t.Fatalf("write intervals: %v", err)
	}

	lines := nonEmptyLines(buf.String())
	if len(lines) != len(samples) {
		t.Fatalf("line count = %d, want %d", len(lines), len(samples))
	}
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line %d invalid JSON: %v\n%s", i, err, line)
		}
		if _, ok := obj["spectral_mean"]; !ok {
			t.Errorf("line %d missing flattened spectral_mean key", i)
		}
	}
}

// TestWriteCandidatesSidecar_TaggedLines asserts the candidates sidecar emits one
// speech line per candidate, each tagged with kind, total M lines.
func TestWriteCandidatesSidecar_TaggedLines(t *testing.T) {
	sp := []SpeechCandidateMetrics{{Score: 9}, {Score: 8}}

	var buf bytes.Buffer
	if err := streamCandidates(&buf, sp); err != nil {
		t.Fatalf("write candidates: %v", err)
	}

	lines := nonEmptyLines(buf.String())
	if len(lines) != len(sp) {
		t.Fatalf("line count = %d, want %d", len(lines), len(sp))
	}
	wantKinds := []string{"speech", "speech"}
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line %d invalid JSON: %v\n%s", i, err, line)
		}
		if obj["kind"] != wantKinds[i] {
			t.Errorf("line %d kind = %v, want %v", i, obj["kind"], wantKinds[i])
		}
		// The candidate's own fields are spliced in alongside the kind tag.
		if _, ok := obj["score"]; !ok {
			t.Errorf("line %d missing candidate score field", i)
		}
	}
}

// TestIntervalSample_MarshalNonFiniteNulled asserts the NaN/±Inf guard: an
// IntervalSample carrying NaN, +Inf and -Inf in float fields marshals without
// error to valid JSON, with each non-finite field nulled (the run-record
// convention) and finite fields unchanged. This is the digitally-silent
// (voice-gated) audio case that aborts the .intervals.jsonl sidecar without the
// guard.
func TestIntervalSample_MarshalNonFiniteNulled(t *testing.T) {
	s := IntervalSample{
		Timestamp:     250 * time.Millisecond,
		RMSLevel:      math.NaN(),
		PeakLevel:     math.Inf(1),
		MomentaryLUFS: math.Inf(-1),
		ShortTermLUFS: -23.5, // finite, must survive
		TruePeak:      math.NaN(),
		SamplePeak:    -1.0, // finite, must survive
		Spectral:      SpectralMetrics{Mean: math.NaN(), Centroid: 2000, Found: true},
	}

	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("MarshalJSON returned error on non-finite fields: %v", err)
	}
	if !json.Valid(raw) {
		t.Fatalf("MarshalJSON produced invalid JSON: %s", raw)
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Non-finite fields render as JSON null.
	for _, key := range []string{"rms_level", "peak_level", "momentary_lufs", "true_peak", "spectral_mean"} {
		v, present := obj[key]
		if !present {
			t.Errorf("missing key %q", key)
			continue
		}
		if v != nil {
			t.Errorf("%q = %v, want null (non-finite)", key, v)
		}
	}

	// Finite fields are unchanged.
	if v := obj["short_term_lufs"]; v != -23.5 {
		t.Errorf("short_term_lufs = %v, want -23.5", v)
	}
	if v := obj["sample_peak"]; v != -1.0 {
		t.Errorf("sample_peak = %v, want -1.0", v)
	}
	if v := obj["spectral_centroid"]; v != 2000.0 {
		t.Errorf("spectral_centroid = %v, want 2000", v)
	}
}

// TestCandidateSidecarLine_MarshalNonFiniteNulled asserts the same guard on the
// candidates sidecar: a SpeechCandidateMetrics carrying NaN, +Inf and -Inf
// marshals without error to valid JSON, non-finite fields nulled, finite fields
// and the kind tag intact.
func TestCandidateSidecarLine_MarshalNonFiniteNulled(t *testing.T) {
	sp := SpeechCandidateMetrics{
		Score: math.NaN(),
		RegionSample: RegionSample{
			RMSLevel:    math.Inf(1),
			PeakLevel:   math.Inf(-1),
			CrestFactor: 12.0, // finite, must survive
		},
	}

	var buf bytes.Buffer
	if err := streamCandidates(&buf, []SpeechCandidateMetrics{sp}); err != nil {
		t.Fatalf("streamCandidates returned error on non-finite fields: %v", err)
	}

	lines := nonEmptyLines(buf.String())
	if len(lines) != 1 {
		t.Fatalf("line count = %d, want 1", len(lines))
	}
	if !json.Valid([]byte(lines[0])) {
		t.Fatalf("produced invalid JSON: %s", lines[0])
	}

	var obj map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &obj); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if obj["kind"] != "speech" {
		t.Errorf("kind = %v, want speech", obj["kind"])
	}
	for _, key := range []string{"score", "rms_level_dbfs", "peak_level_dbfs"} {
		v, present := obj[key]
		if !present {
			t.Errorf("missing key %q", key)
			continue
		}
		if v != nil {
			t.Errorf("%q = %v, want null (non-finite)", key, v)
		}
	}
	if v := obj["crest_factor_db"]; v != 12.0 {
		t.Errorf("crest_factor_db = %v, want 12.0", v)
	}
}

// syntheticIntervals builds n IntervalSamples with ascending RMS levels above the
// digital-silence floor, each carrying a spectral block.
func syntheticIntervals(n int) []IntervalSample {
	out := make([]IntervalSample, n)
	for i := range out {
		out[i] = IntervalSample{
			Timestamp: time.Duration(i) * 250 * time.Millisecond,
			RMSLevel:  -60 + float64(i),
			PeakLevel: -40 + float64(i),
			Spectral:  SpectralMetrics{Mean: float64(i), Centroid: 2000, Found: true},
		}
	}
	return out
}

func nonEmptyLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(strings.TrimRight(s, "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}
