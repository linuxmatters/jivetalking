package processor

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestParseLoudnormStatsFileParsesFixture proves the file-read parse yields the
// correct LoudnormStats field values for the known loudnormCaptureTestJSON body.
// The stats file is the sole source of loudnorm measurements, so this asserts the
// file parse against the fixture's known values.
func TestParseLoudnormStatsFileParsesFixture(t *testing.T) {
	statsPath, err := createSiblingStatsPath(filepath.Join(t.TempDir(), "output.flac"), "loudnorm")
	if err != nil {
		t.Fatalf("createSiblingStatsPath() error = %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(statsPath) })

	if err := os.WriteFile(statsPath, []byte(loudnormCaptureTestJSON), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stats, err := parseLoudnormStatsFile(statsPath)
	if err != nil {
		t.Fatalf("parseLoudnormStatsFile() error = %v", err)
	}

	want := LoudnormStats{
		InputI:            "-23.0",
		InputTP:           "-4.0",
		InputLRA:          "5.0",
		InputThresh:       "-33.0",
		OutputI:           "-16.0",
		OutputTP:          "-2.0",
		OutputLRA:         "5.0",
		OutputThresh:      "-26.0",
		NormalizationType: "linear",
		TargetOffset:      "0.0",
	}
	if *stats != want {
		t.Fatalf("parseLoudnormStatsFile() = %+v, want %+v", *stats, want)
	}
}

// TestParseLoudnormStatsFileConcurrentIsolation proves the per-graph stats file
// gives each concurrent measurement its own correct stats with no cross-path
// collision. Two distinct temp paths hold deliberately different bodies; two
// goroutines parse them concurrently and each must read back its own values.
// This satisfies proposal AC 6 without needing the live loudnorm graph and runs
// clean under -race.
func TestParseLoudnormStatsFileConcurrentIsolation(t *testing.T) {
	const bodyA = `{"input_i":"-23.0","input_tp":"-4.0","input_lra":"5.0","input_thresh":"-33.0","output_i":"-16.0","output_tp":"-2.0","output_lra":"5.0","output_thresh":"-26.0","normalization_type":"linear","target_offset":"0.0"}`
	const bodyB = `{"input_i":"-11.5","input_tp":"-1.2","input_lra":"9.3","input_thresh":"-21.0","output_i":"-16.0","output_tp":"-2.0","output_lra":"9.3","output_thresh":"-26.0","normalization_type":"dynamic","target_offset":"1.7"}`

	pathA, err := createSiblingStatsPath(filepath.Join(t.TempDir(), "a.flac"), "loudnorm")
	if err != nil {
		t.Fatalf("createSiblingStatsPath(A) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(pathA) })

	pathB, err := createSiblingStatsPath(filepath.Join(t.TempDir(), "b.flac"), "loudnorm")
	if err != nil {
		t.Fatalf("createSiblingStatsPath(B) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(pathB) })

	if pathA == pathB {
		t.Fatalf("stats paths collided: both = %s", pathA)
	}

	if err := os.WriteFile(pathA, []byte(bodyA), 0o600); err != nil {
		t.Fatalf("WriteFile(A) error = %v", err)
	}
	if err := os.WriteFile(pathB, []byte(bodyB), 0o600); err != nil {
		t.Fatalf("WriteFile(B) error = %v", err)
	}

	type result struct {
		stats *LoudnormStats
		err   error
	}

	var (
		wg         sync.WaitGroup
		resA, resB result
	)

	wg.Add(2)
	go func() {
		defer wg.Done()
		resA.stats, resA.err = parseLoudnormStatsFile(pathA)
	}()
	go func() {
		defer wg.Done()
		resB.stats, resB.err = parseLoudnormStatsFile(pathB)
	}()
	wg.Wait()

	if resA.err != nil {
		t.Fatalf("parseLoudnormStatsFile(A) error = %v", resA.err)
	}
	if resB.err != nil {
		t.Fatalf("parseLoudnormStatsFile(B) error = %v", resB.err)
	}

	if resA.stats.InputI != "-23.0" || resA.stats.InputTP != "-4.0" || resA.stats.NormalizationType != "linear" {
		t.Fatalf("path A read wrong stats: got InputI=%q InputTP=%q NormalizationType=%q; want -23.0/-4.0/linear (collision?)",
			resA.stats.InputI, resA.stats.InputTP, resA.stats.NormalizationType)
	}
	if resB.stats.InputI != "-11.5" || resB.stats.InputTP != "-1.2" || resB.stats.NormalizationType != "dynamic" {
		t.Fatalf("path B read wrong stats: got InputI=%q InputTP=%q NormalizationType=%q; want -11.5/-1.2/dynamic (collision?)",
			resB.stats.InputI, resB.stats.InputTP, resB.stats.NormalizationType)
	}
}

func TestParseLoudnormStatsFileMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.tmp.json")

	if _, err := parseLoudnormStatsFile(missing); err == nil {
		t.Fatal("parseLoudnormStatsFile() on missing file: want error, got nil")
	}
}

func TestParseLoudnormStatsFileEmptyFile(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty.tmp.json")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	if _, err := parseLoudnormStatsFile(empty); err == nil {
		t.Fatal("parseLoudnormStatsFile() on empty file: want error, got nil")
	}
}
