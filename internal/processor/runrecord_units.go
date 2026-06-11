package processor

import (
	"encoding/json"
	"reflect"
	"strconv"
	"time"
)

// This file holds the §8.4 unit-honesty conversions applied at record assembly
// (representation only). The source domain structs are never mutated: durations
// stay time.Duration (other code consumes them) and LoudnormStats keeps its
// FFmpeg string-parse shape. The record-facing types here present seconds (_s
// float) and §8.4 numeric loudnorm fields instead.

// sanitisedSourceMap reflects a source struct value into the same generic tree
// MarshalRunRecord produces (json tags, omitempty, embedding, and non-finite
// float64 -> null all honoured), returning it as an editable map. The unit
// wrappers below build on this so their conversions inherit the NaN/±Inf sweep
// rather than re-implementing it; nil source returns nil. A non-struct source
// (defensive) yields nil so the caller drops the field.
func sanitisedSourceMap(source any) map[string]any {
	if source == nil {
		return nil
	}
	v := reflect.ValueOf(source)
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return nil
	}
	return sanitiseStruct(v)
}

// durationKeySeconds replaces an integer-nanosecond key in a sanitised map with a
// seconds-suffixed key carrying the float seconds value. The ns value is read
// back from the sanitised map (so omitempty drops already-absent keys), converted
// via time.Duration.Seconds(), and the old key removed. Missing source key is a
// no-op (the field was empty and dropped).
func durationKeySeconds(m map[string]any, nsKey, secKey string) {
	raw, ok := m[nsKey]
	delete(m, nsKey)
	if !ok || raw == nil {
		return
	}
	ns, ok := toInt64(raw)
	if !ok {
		return
	}
	m[secKey] = time.Duration(ns).Seconds()
}

// toInt64 coerces a sanitised JSON-tree value to int64 nanoseconds. sanitiseValue
// returns a time.Duration via its default case (Kind Int64 is unhandled there, so
// the concrete time.Duration passes through v.Interface()); the int64/int/float64
// forms are handled defensively for any other numeric origin.
func toInt64(v any) (int64, bool) {
	switch n := v.(type) {
	case time.Duration:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case float64:
		return int64(n), true
	default:
		return 0, false
	}
}

// noiseProfileRecord wraps the elected room-tone NoiseProfile for the record,
// presenting its time bounds as _s floats (§8.4) while reading every other field
// straight off the source by reflection (no drift). The source NoiseProfile is
// untouched.
type noiseProfileRecord struct {
	src *NoiseProfile
}

// MarshalJSON sanitises the source NoiseProfile then swaps its ns duration keys
// for _s seconds keys. Non-finite floats inside the profile are already nulled by
// the shared sanitiser.
func (p noiseProfileRecord) MarshalJSON() ([]byte, error) {
	m := sanitisedSourceMap(p.src)
	if m == nil {
		return []byte("null"), nil
	}
	durationKeySeconds(m, "start", "start_s")
	durationKeySeconds(m, "duration", "duration_s")
	durationKeySeconds(m, "original_start", "original_start_s")
	durationKeySeconds(m, "original_duration", "original_duration_s")
	return json.Marshal(m)
}

// Profile exposes the wrapped NoiseProfile for read-only consumers of the record
// (e.g. the Markdown renderer in internal/report) that need the elected room-tone
// metrics off rec.Regions.RoomTone.Elected. The wrapper and its source pointer are
// unexported so the JSON marshalling stays representation-controlled; this
// accessor is the read seam. Returns nil when no profile is wrapped.
func (p *noiseProfileRecord) Profile() *NoiseProfile {
	if p == nil {
		return nil
	}
	return p.src
}

// speechProfileRecord wraps the elected speech candidate for the record. Its
// nested region (start/end/duration) and refinement bounds become _s floats; all
// other candidate fields (region-sample, bands, voicing, score) pass through the
// shared sanitiser unchanged. The source SpeechCandidateMetrics is untouched.
type speechProfileRecord struct {
	src *SpeechCandidateMetrics
}

// MarshalJSON sanitises the source candidate then converts its region and
// refinement durations to _s seconds keys.
func (s speechProfileRecord) MarshalJSON() ([]byte, error) {
	m := sanitisedSourceMap(s.src)
	if m == nil {
		return []byte("null"), nil
	}
	if region, ok := m["region"].(map[string]any); ok {
		durationKeySeconds(region, "start", "start_s")
		durationKeySeconds(region, "end", "end_s")
		durationKeySeconds(region, "duration", "duration_s")
	}
	durationKeySeconds(m, "original_start", "original_start_s")
	durationKeySeconds(m, "original_duration", "original_duration_s")
	return json.Marshal(m)
}

// Profile exposes the wrapped SpeechCandidateMetrics for read-only consumers of
// the record (e.g. the Markdown renderer in internal/report) that need the elected
// speech metrics off rec.Regions.Speech.Elected. The read seam mirrors
// noiseProfileRecord.Profile. Returns nil when no profile is wrapped.
func (s *speechProfileRecord) Profile() *SpeechCandidateMetrics {
	if s == nil {
		return nil
	}
	return s.src
}

// normalisationRecord wraps NormalisationResult for the record. It presents the
// region-measurement duration as region_measurement_s (float seconds, §8.4) and
// converts loudnorm_measured from FFmpeg's raw string keys to the §8.4 numeric
// sub-block. Every other field reads off the source by reflection, so the record
// cannot drift. The source NormalisationResult (and its LoudnormStats) are
// untouched - LoudnormStats stays the FFmpeg parse target.
type normalisationRecord struct {
	src *NormalisationResult
}

// MarshalJSON sanitises the source result, then (a) swaps region_measurement_ns
// for region_measurement_s seconds and (b) replaces the raw-string
// loudnorm_measured with the §8.4 numeric sub-block.
func (n normalisationRecord) MarshalJSON() ([]byte, error) {
	m := sanitisedSourceMap(n.src)
	if m == nil {
		return []byte("null"), nil
	}
	durationKeySeconds(m, "region_measurement_ns", "region_measurement_s")
	m["loudnorm_measured"] = loudnormMeasuredNumeric(n.src.LoudnormStats)
	return json.Marshal(m)
}

// Result exposes the wrapped NormalisationResult for read-only consumers of the
// record (e.g. the Markdown renderer in internal/report) that need the Pass 3/4
// limiter and loudnorm numbers off rec.Normalisation. The wrapper and its source
// pointer are unexported so the JSON marshalling stays representation-controlled;
// this accessor is the read seam, mirroring noiseProfileRecord.Profile. Returns
// nil when no result is wrapped.
func (n *normalisationRecord) Result() *NormalisationResult {
	if n == nil {
		return nil
	}
	return n.src
}

// loudnormMeasuredNumeric converts FFmpeg's string-keyed LoudnormStats into the
// §8.4 numeric sub-block: each measurement string is parsed to float64 under a
// unit-suffixed key, and normalization_type stays a string (it is categorical,
// not a measurement). A field whose string fails to parse is omitted (the reader
// sees a missing key, never a fabricated 0). Returns nil for nil stats so the
// caller emits null.
func loudnormMeasuredNumeric(stats *LoudnormStats) map[string]any {
	if stats == nil {
		return nil
	}
	out := map[string]any{}
	putParsedFloat(out, "input_integrated_lufs", stats.InputI)
	putParsedFloat(out, "input_true_peak_dbtp", stats.InputTP)
	putParsedFloat(out, "input_lra_lu", stats.InputLRA)
	putParsedFloat(out, "input_thresh_lufs", stats.InputThresh)
	putParsedFloat(out, "output_integrated_lufs", stats.OutputI)
	putParsedFloat(out, "output_true_peak_dbtp", stats.OutputTP)
	putParsedFloat(out, "output_lra_lu", stats.OutputLRA)
	putParsedFloat(out, "output_thresh_lufs", stats.OutputThresh)
	putParsedFloat(out, "target_offset_db", stats.TargetOffset)
	if stats.NormalizationType != "" {
		out["normalization_type"] = stats.NormalizationType
	}
	return out
}

// putParsedFloat parses a loudnorm string value to float64 and stores it under
// key, leaving the key absent on parse failure (graceful: omit, never crash or
// fabricate). Mirrors the existing strconv.ParseFloat usage in normalise.go.
func putParsedFloat(out map[string]any, key, value string) {
	f, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return
	}
	out[key] = f
}
