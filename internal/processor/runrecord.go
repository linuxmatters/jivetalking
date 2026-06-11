package processor

import (
	"encoding/json"
	"math"
	"path/filepath"
	"reflect"
	"strings"
	"time"
)

// schemaVersion is the §8.4 root version of the run record. Bumped on any
// breaking field rename/restructure while the artefact is internal (§7.1).
const schemaVersion = 1

// RunRecord is the §8.1 top-level run record: one serialisable JSON document per
// file per run. It is a thin assembly point that embeds the cleaned domain
// structs BY REFERENCE from a ProcessingResult (§9.2) - no value copy, so the
// record never drifts from its source. Absent stages and processing blocks are
// nil-pointer + omitempty, so an analysis-only (Pass-1-only) record drops
// `filtered`/`final`/`filters`/`normalisation` rather than null-filling them
// (§9.1 call 3).
type RunRecord struct {
	SchemaVersion int            `json:"schema_version"`
	Run           RunProvenance  `json:"run"`
	Loudness      LoudnessDomain `json:"loudness"`
	Dynamics      DynamicsDomain `json:"dynamics"`
	Spectral      SpectralDomain `json:"spectral"`
	Noise         *NoiseMetrics  `json:"noise,omitempty"`
	Regions       *RegionsBlock  `json:"regions,omitempty"`
	Filters       *FiltersBlock  `json:"filters,omitempty"`
	// Normalisation wraps the source *NormalisationResult so the record presents
	// region_measurement_s (seconds) and the §8.4 numeric loudnorm_measured block
	// (see normalisationRecord); the source struct is untouched.
	Normalisation *normalisationRecord `json:"normalisation,omitempty"`

	// IntervalSummary holds the per-250ms RMS distribution and gap summary (task
	// 3.1). The full per-interval series lives in the .intervals.jsonl sidecar; the
	// summary stays inline. nil + omitempty drops it when no intervals exist.
	IntervalSummary *IntervalSummary `json:"interval_summary,omitempty"`
}

// RunProvenance is the §8.1 `run` block: identity + provenance for the run.
type RunProvenance struct {
	InputFile    string  `json:"input_file"`
	ProcessedAt  string  `json:"processed_at"` // RFC3339 timestamp captured at record build
	DurationS    float64 `json:"duration_s"`
	SampleRateHz int     `json:"sample_rate_hz"`
	Channels     int     `json:"channels"`
}

// LoudnessDomain is the §8.1 `loudness` block: the EBU R128 target plus one
// loudness snapshot per stage. Input and output loudness are different Go types
// (InputLoudnessMetrics vs OutputLoudnessMetrics) but tag-compatible, so the
// per-stage struct marshals to one consistent shape.
type LoudnessDomain struct {
	TargetILUFS float64        `json:"target_i_lufs"`
	Stages      LoudnessStages `json:"stages"`
}

// LoudnessStages holds the three loudness snapshots by reference. filtered/final
// are nil in analysis-only mode and drop via omitempty.
type LoudnessStages struct {
	Input    *InputLoudnessMetrics  `json:"input,omitempty"`
	Filtered *OutputLoudnessMetrics `json:"filtered,omitempty"`
	Final    *OutputLoudnessMetrics `json:"final,omitempty"`
}

// DynamicsDomain is the §8.1 `dynamics` block: one astats snapshot per stage.
type DynamicsDomain struct {
	Stages DynamicsStages `json:"stages"`
}

// DynamicsStages holds the three dynamics snapshots by reference.
type DynamicsStages struct {
	Input    *DynamicsMetrics `json:"input,omitempty"`
	Filtered *DynamicsMetrics `json:"filtered,omitempty"`
	Final    *DynamicsMetrics `json:"final,omitempty"`
}

// SpectralDomain is the §8.1 `spectral` block: whole-file averaged
// aspectralstats per stage. The Spectral field is json:"-" on the measurement
// structs, so the record assembles pointers to those values here.
type SpectralDomain struct {
	Stages SpectralStages `json:"stages"`
}

// SpectralStages holds the three spectral snapshots by reference.
type SpectralStages struct {
	Input    *SpectralMetrics `json:"input,omitempty"`
	Filtered *SpectralMetrics `json:"filtered,omitempty"`
	Final    *SpectralMetrics `json:"final,omitempty"`
}

// FiltersBlock is the §8.1 `filters` block: the adapted EffectiveFilterConfig
// plus the report-only AdaptiveDiagnostics. The gate Threshold/Range are
// honest-dB here (converted from the linear amplitudes the DSP consumes); see
// newFiltersBlock.
type FiltersBlock struct {
	EffectiveFilterConfig
	Diagnostics *AdaptiveDiagnostics `json:"diagnostics,omitempty"`
}

// IntervalSummary is the §8.1 `interval_summary` block: the RMS distribution and
// largest-gap summary derived from the full per-250ms IntervalSamples series. The
// full series itself lands in the .intervals.jsonl sidecar (§8.5 call 2); only
// this summary is inline. The values match the `.log` diagnostic for the same
// file (computed by the same maths, intervalRMSSummary).
type IntervalSummary struct {
	Count int `json:"count"`

	// RMS distribution over intervals above digital silence (dBFS). Present only
	// when at least 10 such intervals exist (the .log threshold); nil otherwise.
	RMS *RMSDistribution `json:"rms_distribution,omitempty"`

	// LargestGapDB is the biggest jump between adjacent sorted RMS values (dB),
	// the room-tone/speech boundary signal from the diagnostic. Present with RMS.
	LargestGapDB *float64 `json:"largest_gap_db,omitempty"`
}

// RMSDistribution holds the sorted-percentile RMS spread (dBFS) printed by the
// .log "RMSLevel Dist" line, matching its index selection exactly.
type RMSDistribution struct {
	Min float64 `json:"min_dbfs"`
	P10 float64 `json:"p10_dbfs"`
	P25 float64 `json:"p25_dbfs"`
	P50 float64 `json:"p50_dbfs"`
	P75 float64 `json:"p75_dbfs"`
	P90 float64 `json:"p90_dbfs"`
	Max float64 `json:"max_dbfs"`
}

// RegionsBlock is the §8.1 `regions` block in its nested shape: room-tone and
// speech, each carrying the elected profile, a candidate summary (count + elected
// score), and the per-stage before/after samples. Assembly-side type only - it
// points into the live ProcessingResult data BY REFERENCE (§9.2), never copying
// values. The full candidate arrays and interval series are NOT inline; they live
// in the .candidates.jsonl / .intervals.jsonl sidecars (§8.5 call 2, §9.3).
type RegionsBlock struct {
	RoomTone RoomToneRegionRecord `json:"room_tone"`
	Speech   SpeechRegionRecord   `json:"speech"`
}

// RoomToneRegionRecord is the §8.1 `regions.room_tone` nested block: the elected
// room-tone profile, a candidate summary (full array → sidecar), and the
// per-stage region samples. All fields reference the live RegionMetrics / output
// measurements.
type RoomToneRegionRecord struct {
	// Elected wraps the source *NoiseProfile so its time bounds emit as _s floats
	// (§8.4); nil + omitempty drops it when no profile was elected.
	Elected           *noiseProfileRecord `json:"elected,omitempty"`
	CandidatesSummary *CandidatesSummary  `json:"candidates_summary,omitempty"`
	Samples           RegionSamples       `json:"samples"`
}

// ElectedProfile returns the elected room-tone NoiseProfile for read-only
// consumers of the record (e.g. the Markdown renderer), or nil when no profile was
// elected. The Elected wrapper type is unexported (it owns the JSON shape), so this
// is the named read seam onto the underlying measurement.
func (r RoomToneRegionRecord) ElectedProfile() *NoiseProfile {
	return r.Elected.Profile()
}

// SpeechRegionRecord is the §8.1 `regions.speech` nested block: the elected
// speech profile, a candidate summary (full array → sidecar), and the per-stage
// region samples.
type SpeechRegionRecord struct {
	// Elected wraps the source *SpeechCandidateMetrics so its region time bounds
	// emit as _s floats (§8.4); nil + omitempty drops it when no speech profile was
	// elected.
	Elected           *speechProfileRecord `json:"elected,omitempty"`
	CandidatesSummary *CandidatesSummary   `json:"candidates_summary,omitempty"`
	Samples           RegionSamples        `json:"samples"`
}

// ElectedProfile returns the elected speech SpeechCandidateMetrics for read-only
// consumers of the record (e.g. the Markdown renderer), or nil when no profile was
// elected. Mirrors RoomToneRegionRecord.ElectedProfile.
func (s SpeechRegionRecord) ElectedProfile() *SpeechCandidateMetrics {
	return s.Elected.Profile()
}

// CandidatesSummary is the inline stand-in for a full candidate array (§9.3): the
// number evaluated and the elected candidate's score. The elected candidate's
// full metrics already live in regions.<kind>.elected; the full scored array is
// streamed to the .candidates.jsonl sidecar. ElectedScore is a pointer so an
// unelected kind drops it (omitempty) rather than emitting a misleading 0.
type CandidatesSummary struct {
	EvaluatedCount int      `json:"evaluated_count"`
	ElectedScore   *float64 `json:"elected_score,omitempty"`
}

// RegionSamples is the §8.1 `regions.<kind>.samples` block: the bare
// before/after region measurement per stage (§8.2 last row, slimmer schema). All
// three are pointers with omitempty - analysis-only drops filtered/final, and a
// missing input drops too.
type RegionSamples struct {
	Input    *RegionSample `json:"input,omitempty"`
	Filtered *RegionSample `json:"filtered,omitempty"`
	Final    *RegionSample `json:"final,omitempty"`
}

// NewRunRecord assembles the full §8.1 record from a completed ProcessingResult.
// All measurement blocks are taken BY REFERENCE from the result's pointer fields
// (§9.2): no deep copy, so the record cannot drift from its source. The only
// value transform is the gate linear→dB conversion in the filters block, which
// is representation-only and does not touch the DSP-consumed config.
func NewRunRecord(result *ProcessingResult) *RunRecord {
	rec := newPass1Record(result.Measurements)

	if fm := result.FilteredMeasurements; fm != nil {
		rec.Loudness.Stages.Filtered = &fm.Loudness
		rec.Dynamics.Stages.Filtered = &fm.Dynamics
		rec.Spectral.Stages.Filtered = &fm.Spectral
		// Pass 2 before/after region samples (task 1.2), referenced not copied.
		if rec.Regions != nil {
			rec.Regions.RoomTone.Samples.Filtered = fm.RoomToneSample
			rec.Regions.Speech.Samples.Filtered = fm.SpeechSample
		}
	}

	if result.NormResult != nil {
		if fm := result.NormResult.FinalMeasurements; fm != nil {
			rec.Loudness.Stages.Final = &fm.Loudness
			rec.Dynamics.Stages.Final = &fm.Dynamics
			rec.Spectral.Stages.Final = &fm.Spectral
			// Pass 4 before/after region samples (task 1.2), referenced not copied.
			if rec.Regions != nil {
				rec.Regions.RoomTone.Samples.Final = fm.RoomToneSample
				rec.Regions.Speech.Samples.Final = fm.SpeechSample
			}
		}
		rec.Normalisation = &normalisationRecord{src: result.NormResult}
	}

	if result.Config != nil {
		rec.Filters = newFiltersBlock(result.Config, result.Diagnostics)
	}

	// Provenance not carried by AudioMeasurements: source sample rate / channels.
	rec.Run.InputFile = filepath.Base(result.OutputPath)
	rec.Run.SampleRateHz = result.InputMetadata.SampleRate
	rec.Run.Channels = result.InputMetadata.Channels
	if result.InputMetadata.DurationSecs > 0 {
		rec.Run.DurationS = result.InputMetadata.DurationSecs
	}

	return rec
}

// NewAnalysisRunRecord assembles a Pass-1-only (analysis-only) record from just
// the input measurements. The filtered/final stages, filters, and normalisation
// blocks stay nil and drop from the JSON via omitempty (§9.1 call 3). The
// caller supplies the input basename (analysis-only has no OutputPath).
func NewAnalysisRunRecord(inputFile string, m *AudioMeasurements) *RunRecord {
	rec := newPass1Record(m)
	rec.Run.InputFile = filepath.Base(inputFile)
	return rec
}

// newPass1Record builds the input-stage record shared by both constructors. All
// input blocks are referenced off the supplied AudioMeasurements (no copy).
func newPass1Record(m *AudioMeasurements) *RunRecord {
	rec := &RunRecord{
		SchemaVersion: schemaVersion,
		Run: RunProvenance{
			ProcessedAt: time.Now().Format(time.RFC3339),
		},
		Loudness: LoudnessDomain{TargetILUFS: targetILUFS},
	}

	if m == nil {
		return rec
	}

	rec.Loudness.Stages.Input = &m.Loudness
	rec.Dynamics.Stages.Input = &m.Dynamics
	rec.Spectral.Stages.Input = &m.Spectral
	rec.Noise = &m.Noise
	rec.Regions = newRegionsBlock(&m.Regions)
	rec.IntervalSummary = newIntervalSummary(m.Regions.IntervalSamples)
	rec.Run.DurationS = m.Duration

	return rec
}

// newRegionsBlock restructures the flat RegionMetrics into the §8.1 nested
// regions shape (§9.4). Every field references the live RegionMetrics data
// (§9.2): elected profiles, candidate arrays, and interval samples are taken by
// reference, no value copy. The input region sample is wired per kind:
//   - speech: SpeechProfile embeds RegionSample (it is *SpeechCandidateMetrics),
//     so the input sample is &SpeechProfile.RegionSample.
//   - room-tone: the elected profile is a NoiseProfile, a slimmer struct with no
//     RegionSample. The elected room-tone candidate's RegionSample is captured at
//     election onto RegionMetrics.ElectedRoomToneSample (analyzer.go), so the input
//     sample is that pointer when present, nil (omitempty) otherwise. The filtered
//     and final room-tone samples still wire from OutputMeasurements.RoomToneSample.
func newRegionsBlock(r *RegionMetrics) *RegionsBlock {
	block := &RegionsBlock{
		RoomTone: RoomToneRegionRecord{
			CandidatesSummary: newRoomToneCandidatesSummary(r),
		},
		Speech: SpeechRegionRecord{
			CandidatesSummary: newSpeechCandidatesSummary(r),
		},
	}

	// Wrap the elected profiles so their time bounds emit as _s floats (§8.4); a
	// missing profile leaves the wrapper pointer nil so omitempty drops `elected`.
	if r.NoiseProfile != nil {
		block.RoomTone.Elected = &noiseProfileRecord{src: r.NoiseProfile}
	}
	if r.SpeechProfile != nil {
		block.Speech.Elected = &speechProfileRecord{src: r.SpeechProfile}
	}

	// Speech input sample: the elected profile embeds RegionSample by reference.
	if r.SpeechProfile != nil {
		block.Speech.Samples.Input = &r.SpeechProfile.RegionSample
	}

	// Room-tone input sample: the elected candidate's RegionSample captured at
	// election (the NoiseProfile itself has no RegionSample).
	block.RoomTone.Samples.Input = r.ElectedRoomToneSample

	return block
}

// newRoomToneCandidatesSummary builds the inline room-tone candidate summary: the
// evaluated count and the elected candidate's score. The elected room-tone
// candidate is the one whose region matches the elected NoiseProfile; the full
// scored array goes to the sidecar. Returns nil when no candidates were evaluated.
func newRoomToneCandidatesSummary(r *RegionMetrics) *CandidatesSummary {
	if len(r.RoomToneCandidates) == 0 {
		return nil
	}
	s := &CandidatesSummary{EvaluatedCount: len(r.RoomToneCandidates)}
	if r.NoiseProfile != nil {
		for i := range r.RoomToneCandidates {
			c := &r.RoomToneCandidates[i]
			if c.Region.Start == r.NoiseProfile.Start && c.Region.Duration == r.NoiseProfile.Duration {
				score := c.Score
				s.ElectedScore = &score
				break
			}
		}
	}
	return s
}

// newSpeechCandidatesSummary builds the inline speech candidate summary: the
// evaluated count and the elected candidate's score (taken off the elected
// SpeechProfile, which aliases a candidate). Returns nil when no candidates were
// evaluated.
func newSpeechCandidatesSummary(r *RegionMetrics) *CandidatesSummary {
	if len(r.SpeechCandidates) == 0 {
		return nil
	}
	s := &CandidatesSummary{EvaluatedCount: len(r.SpeechCandidates)}
	if r.SpeechProfile != nil {
		score := r.SpeechProfile.Score
		s.ElectedScore = &score
	}
	return s
}

// targetILUFS is the EBU R128 integrated-loudness target carried as a bare
// reference value in the record (§8.3), not a verdict.
const targetILUFS = -16.0

// newFiltersBlock assembles the §8.1 filters block. The DS201 gate Threshold and
// Range are stored as LINEAR amplitudes on the live config (FFmpeg agate
// consumes linear) but tagged threshold_db/range_db; emitting the linear value
// under a _db key is misleading. This converts both to honest dB on a LOCAL copy
// of the effective config, leaving the DSP-consumed *result.Config untouched
// (representation-only; audio stays bit-exact). All other filter fields are
// already correct units.
func newFiltersBlock(cfg *EffectiveFilterConfig, diag *AdaptiveDiagnostics) *FiltersBlock {
	local := *cfg // shallow copy; isolates the gate dB conversion from the DSP config
	if local.DS201Gate.Threshold > 0 {
		local.DS201Gate.Threshold = LinearToDb(local.DS201Gate.Threshold)
	}
	if local.DS201Gate.Range > 0 {
		local.DS201Gate.Range = LinearToDb(local.DS201Gate.Range)
	}
	return &FiltersBlock{EffectiveFilterConfig: local, Diagnostics: diag}
}

// MarshalRunRecord serialises a RunRecord to indented JSON with non-finite
// float64 leaves (NaN, +Inf, -Inf) emitted as JSON null (§9.1 call 4).
// encoding/json errors on non-finite floats, so the record cannot be
// marshalled then post-processed as a string. Instead the assembled record is
// reflected into a generic tree (honouring json tags, omitempty, embedding, and
// `-`) with non-finite float64 replaced by nil, then that tree is marshalled.
// The sweep only nulls non-finite leaves; it never reorders or re-tags fields.
func MarshalRunRecord(r *RunRecord) ([]byte, error) {
	tree := sanitiseValue(reflect.ValueOf(r))
	return json.MarshalIndent(tree, "", "  ")
}

// jsonMarshalerType is the reflect.Type of json.Marshaler, used to detect
// custom-marshalled leaves during the sanitise walk.
var jsonMarshalerType = reflect.TypeFor[json.Marshaler]()

// marshalerOf returns v as a json.Marshaler when its type (or its addressable
// pointer) implements the interface, so custom JSON shapes are preserved.
func marshalerOf(v reflect.Value) (json.Marshaler, bool) {
	if !v.IsValid() {
		return nil, false
	}
	if v.Type().Implements(jsonMarshalerType) {
		if v.Kind() == reflect.Pointer && v.IsNil() {
			return nil, false
		}
		return v.Interface().(json.Marshaler), true
	}
	if v.CanAddr() && reflect.PointerTo(v.Type()).Implements(jsonMarshalerType) {
		return v.Addr().Interface().(json.Marshaler), true
	}
	return nil, false
}

// sanitiseValue reflects v into the generic tree encoding/json would produce,
// substituting nil for any non-finite float64 leaf. It mirrors the encoding/json
// tag semantics used by RunRecord and its domain structs: json field names,
// `,omitempty`, `-`, and anonymous struct embedding (field promotion).
func sanitiseValue(v reflect.Value) any {
	// Honour custom json.Marshaler implementations (e.g. IntervalSample flattens
	// its Spectral to spectral_* keys). Marshal via the type, then re-sanitise the
	// decoded tree so any non-finite leaf inside still becomes null. encoding/json
	// errors on non-finite floats, so a failed marshal here is treated as a null
	// leaf rather than aborting the whole record.
	if m, ok := marshalerOf(v); ok {
		raw, err := m.MarshalJSON()
		if err != nil {
			return nil
		}
		var decoded any
		if json.Unmarshal(raw, &decoded) != nil {
			return nil
		}
		return decoded
	}

	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nil
		}
		return sanitiseValue(v.Elem())

	case reflect.Struct:
		// time.Duration and friends are not structs; structs handled here are the
		// record's domain types. Marshal field-by-field honouring json tags.
		return sanitiseStruct(v)

	case reflect.Map:
		if v.IsNil() {
			return nil
		}
		out := make(map[string]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[iter.Key().String()] = sanitiseValue(iter.Value())
		}
		return out

	case reflect.Slice:
		if v.IsNil() {
			return nil
		}
		fallthrough
	case reflect.Array:
		out := make([]any, v.Len())
		for i := range v.Len() {
			out[i] = sanitiseValue(v.Index(i))
		}
		return out

	case reflect.Float64, reflect.Float32:
		f := v.Float()
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return f

	default:
		return v.Interface()
	}
}

// sanitiseStruct walks an exported struct's fields, applying the same json tag
// rules encoding/json uses (field name, omitempty, `-`, anonymous embedding).
func sanitiseStruct(v reflect.Value) map[string]any {
	out := map[string]any{}
	addStructFields(v, out)
	return out
}

// addStructFields writes v's marshalled fields into out, flattening anonymous
// embedded structs the way encoding/json promotes their fields.
func addStructFields(v reflect.Value, out map[string]any) {
	t := v.Type()
	for i := range t.NumField() {
		field := t.Field(i)
		if field.PkgPath != "" { // unexported
			continue
		}
		tag := field.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" && opts == "" {
			continue
		}

		fv := v.Field(i)

		// Anonymous embedded struct (or *struct) with no explicit tag name:
		// promote its fields into the parent object, matching encoding/json.
		if field.Anonymous && name == "" {
			ev := fv
			if ev.Kind() == reflect.Pointer {
				if ev.IsNil() {
					continue
				}
				ev = ev.Elem()
			}
			if ev.Kind() == reflect.Struct {
				addStructFields(ev, out)
				continue
			}
		}

		if name == "" {
			name = field.Name
		}
		if strings.Contains(opts, "omitempty") && isEmptyValue(fv) {
			continue
		}
		out[name] = sanitiseValue(fv)
	}
}

// isEmptyValue mirrors encoding/json's omitempty emptiness test for the value
// kinds the record uses.
func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	default:
		return false
	}
}
