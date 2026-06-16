package processor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
)

// WriteRunRecord marshals the run record to indented JSON (via the NaN/±Inf-safe
// MarshalRunRecord) and writes it to path. Sibling to file_write.go. The record
// is a side artefact: callers treat a write failure as non-fatal and keep the
// processed audio, which is the product.
func WriteRunRecord(record *RunRecord, path string) error {
	data, err := MarshalRunRecord(record)
	if err != nil {
		return fmt.Errorf("failed to marshal run record: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write run record to %s: %w", path, err)
	}
	return nil
}

// sidecarBase derives the shared sidecar basename from the .json record path by
// trimming the trailing ".json" so the sidecars sit beside the record with a
// matching stem (e.g. <name>-LUFS-NN-processed.json -> <name>-LUFS-NN-processed,
// then .intervals.jsonl / .candidates.jsonl).
func sidecarBase(recordPath string) string {
	return strings.TrimSuffix(recordPath, ".json")
}

// IntervalsSidecarPath returns the .intervals.jsonl path for a given record path.
func IntervalsSidecarPath(recordPath string) string {
	return sidecarBase(recordPath) + ".intervals.jsonl"
}

// CandidatesSidecarPath returns the .candidates.jsonl path for a given record path.
func CandidatesSidecarPath(recordPath string) string {
	return sidecarBase(recordPath) + ".candidates.jsonl"
}

// candidateSidecarLine is one tagged line in the .candidates.jsonl sidecar: the
// kind ("speech") plus the candidate's full metrics. The metrics are embedded by
// reference so the line carries the candidate's existing JSON shape (region,
// region-sample, scoring, bands) without copying values.
type candidateSidecarLine struct {
	Kind   string `json:"kind"`
	Speech *SpeechCandidateMetrics
}

// MarshalJSON flattens the kind tag and the candidate metrics into one object.
// The candidate is swept through sanitiseValue first so any non-finite float
// (NaN, +Inf, -Inf) serialises to JSON null, mirroring the run-record convention
// (MarshalRunRecord). Without this, encoding/json errors on non-finite floats and
// aborts the .candidates.jsonl sidecar on digitally-silent (voice-gated) audio.
func (l candidateSidecarLine) MarshalJSON() ([]byte, error) {
	var metrics any
	if l.Speech != nil {
		metrics = sanitiseValue(reflect.ValueOf(l.Speech))
	}
	raw, err := json.Marshal(metrics)
	if err != nil {
		return nil, err
	}
	// Splice the kind tag in front of the metric object's fields.
	prefix := fmt.Sprintf(`{"kind":%q,`, l.Kind)
	if len(raw) >= 2 && raw[0] == '{' {
		return append([]byte(prefix), raw[1:]...), nil
	}
	// Metric serialised to something other than an object (should not happen);
	// fall back to a nested object so the line stays valid JSON.
	return json.Marshal(struct {
		Kind    string `json:"kind"`
		Metrics any    `json:"metrics"`
	}{Kind: l.Kind, Metrics: metrics})
}

// WriteIntervalsSidecar streams the full per-250ms IntervalSamples series to a
// .jsonl sidecar, one JSON object per line in order. Uses a buffered streaming
// writer (one line at a time), never a giant in-memory array marshal. Each line
// is the IntervalSample's own JSON (its MarshalJSON flattens the spectral block
// to spectral_* keys). Like WriteRunRecord, a write failure is non-fatal to the
// caller: the audio is the product. count(lines) == len(samples).
func WriteIntervalsSidecar(samples []IntervalSample, path string) error {
	return writeSidecarFile("intervals", path, func(w io.Writer) error {
		return streamIntervals(w, samples)
	})
}

// writeSidecarFile owns the sidecar file lifecycle: create the file, defer a
// close that surfaces a flush failure only when the stream itself succeeded (so
// a successful return means the bytes landed), and run the caller's streaming
// write. label distinguishes the sidecar in error messages ("intervals" vs
// "candidates"), keeping the two writers' wording distinct.
func writeSidecarFile(label, path string, write func(io.Writer) error) (err error) {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create %s sidecar %s: %w", label, path, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close %s sidecar %s: %w", label, path, cerr)
		}
	}()

	if err := write(f); err != nil {
		return fmt.Errorf("failed to write %s sidecar %s: %w", label, path, err)
	}
	return nil
}

// streamIntervals writes the interval series to w one JSON object per line via a
// buffered streaming encoder (no giant in-memory array). Factored out so the file
// writer and the unit tests exercise the same streaming path. Each line uses
// IntervalSample.MarshalJSON to flatten the spectral block to spectral_* keys.
func streamIntervals(w io.Writer, samples []IntervalSample) error {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw) // Encoder writes one object + newline per Encode call.
	for i := range samples {
		if err := enc.Encode(samples[i]); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// WriteCandidatesSidecar streams the speech candidate array to a .jsonl sidecar,
// one candidate per line tagged with its kind, preserving the array's order. Uses
// a buffered streaming writer (one line at a time). A write failure is non-fatal
// to the caller. count(lines) == len(speech).
func WriteCandidatesSidecar(speech []SpeechCandidateMetrics, path string) error {
	return writeSidecarFile("candidates", path, func(w io.Writer) error {
		return streamCandidates(w, speech)
	})
}

// streamCandidates writes the speech candidate array to w, one tagged JSON object
// per line, via a buffered streaming encoder. Factored out so the file writer and
// the unit tests share the same streaming path.
func streamCandidates(w io.Writer, speech []SpeechCandidateMetrics) error {
	bw := bufio.NewWriter(w)
	enc := json.NewEncoder(bw)
	for i := range speech {
		if err := enc.Encode(candidateSidecarLine{Kind: "speech", Speech: &speech[i]}); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// WriteRunRecordSidecars writes both the intervals and candidates sidecars for a
// record at recordPath, pulling the full series off the supplied measurements.
// Returns the first error encountered; callers treat any failure as non-fatal
// (the audio is the product, sidecars are a side artefact). measurements may be
// nil (no Pass-1 data), in which case empty sidecars are written so the file set
// stays consistent.
func WriteRunRecordSidecars(measurements *AudioMeasurements, recordPath string) error {
	var samples []IntervalSample
	var speech []SpeechCandidateMetrics
	if measurements != nil {
		samples = measurements.Regions.IntervalSamples
		speech = measurements.Regions.SpeechCandidates
	}

	if err := WriteIntervalsSidecar(samples, IntervalsSidecarPath(recordPath)); err != nil {
		return err
	}
	return WriteCandidatesSidecar(speech, CandidatesSidecarPath(recordPath))
}
