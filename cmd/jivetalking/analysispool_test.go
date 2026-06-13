package main

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// analysisIndexFromPath parses the trailing integer of a synthetic "fileN" path
// so the seam fake and the stub openMetadata can recover a worker's index from
// the only argument they receive (the input path). runAnalysisPool passes the
// path, not the index, so the index is encoded in the filename instead.
func analysisIndexFromPath(t *testing.T, path string) int {
	t.Helper()
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	digits := strings.TrimPrefix(base, "file")
	idx, err := strconv.Atoi(digits)
	if err != nil {
		t.Fatalf("parse index from %q: %v", path, err)
	}
	return idx
}

// makeAnalysisFiles builds n distinct synthetic input paths named file0..fileN-1
// under a temp dir. The trailing number carries each worker's index for the
// fake and stub openMetadata to read back.
func makeAnalysisFiles(t *testing.T, n int) []string {
	t.Helper()
	dir := t.TempDir()
	files := make([]string, n)
	for i := range files {
		files[i] = filepath.Join(dir, "file"+strconv.Itoa(i)+".wav")
	}
	return files
}

// stubOpenMetadata returns metadata whose SampleRate encodes the path's index
// (48000+index), letting tests assert metas[i] landed in slot i. It never opens
// a real file.
func stubOpenMetadata(t *testing.T) func(string) (*audio.Metadata, error) {
	return func(path string) (*audio.Metadata, error) {
		idx := analysisIndexFromPath(t, path)
		return &audio.Metadata{
			Duration:   120,
			SampleRate: 48000 + idx,
			Channels:   1,
		}, nil
	}
}

// installAnalysisFake swaps analysisPoolAnalyse for fn and restores it after the
// test so tests stay isolated from each other and from main_test.go's swaps.
func installAnalysisFake(t *testing.T, fn func(context.Context, string, *processor.BaseFilterConfig, processor.ProgressCallback) (*processor.AnalysisResult, error)) {
	t.Helper()
	orig := analysisPoolAnalyse
	analysisPoolAnalyse = fn
	t.Cleanup(func() { analysisPoolAnalyse = orig })
}

// inflightAnalysisFake observes pool concurrency without real FFmpeg. It tracks
// live in-flight workers and the high-water mark (compare-and-update), sleeps to
// create overlap opportunity, then returns a per-index sentinel AnalysisResult
// whose AdaptationDuration encodes the index. It mirrors pool_test.go's
// inflightFake atomic.Int32 high-water idiom.
type inflightAnalysisFake struct {
	t       *testing.T
	live    atomic.Int32
	maxSeen atomic.Int32
}

func (f *inflightAnalysisFake) fn(_ context.Context, inputPath string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.AnalysisResult, error) {
	cur := f.live.Add(1)
	for {
		old := f.maxSeen.Load()
		if cur <= old || f.maxSeen.CompareAndSwap(old, cur) {
			break
		}
	}

	time.Sleep(5 * time.Millisecond)

	f.live.Add(-1)
	idx := analysisIndexFromPath(f.t, inputPath)
	return &processor.AnalysisResult{AdaptationDuration: time.Duration(idx)}, nil
}

// TestRunAnalysisPool_InFlightBoundedToJobs asserts jobs == 3 caps in-flight
// analysis workers at 3 over 8 files while still reaching real concurrency (>1),
// proving the semaphore both bounds and permits parallelism. Drives the pool
// with p == nil (no real tea.Program). [AC3 / 2.3(a)]
func TestRunAnalysisPool_InFlightBoundedToJobs(t *testing.T) {
	const n = 8
	const jobs = 3

	fake := &inflightAnalysisFake{t: t}
	installAnalysisFake(t, fake.fn)

	files := makeAnalysisFiles(t, n)
	results := make([]*processor.AnalysisResult, len(files))
	metas := make([]*audio.Metadata, len(files))
	errs := make([]error, len(files))
	base := processor.DefaultFilterConfig()

	runAnalysisPool(context.Background(), nil, files, base, func(string, ...any) {}, jobs, results, metas, errs, stubOpenMetadata(t))

	maxSeen := fake.maxSeen.Load()
	if maxSeen < 2 || maxSeen > jobs {
		t.Fatalf("max in-flight with jobs=%d = %d, want in (1,%d]", jobs, maxSeen, jobs)
	}
	for i := range files {
		if errs[i] != nil {
			t.Fatalf("errs[%d] = %v, want nil", i, errs[i])
		}
		if results[i] == nil {
			t.Fatalf("results[%d] = nil, want populated", i)
		}
	}
}

// TestRunAnalysisPool_SerialParityJobs1 asserts jobs == 1 holds in-flight
// analysis workers to a single concurrent call (high-water == 1), the serial
// outcome under the pool. [AC3 / 2.3(b)]
func TestRunAnalysisPool_SerialParityJobs1(t *testing.T) {
	const n = 8

	fake := &inflightAnalysisFake{t: t}
	installAnalysisFake(t, fake.fn)

	files := makeAnalysisFiles(t, n)
	results := make([]*processor.AnalysisResult, len(files))
	metas := make([]*audio.Metadata, len(files))
	errs := make([]error, len(files))
	base := processor.DefaultFilterConfig()

	runAnalysisPool(context.Background(), nil, files, base, func(string, ...any) {}, 1, results, metas, errs, stubOpenMetadata(t))

	if got := fake.maxSeen.Load(); got != 1 {
		t.Fatalf("max in-flight with jobs=1 = %d, want 1", got)
	}
	for i := range files {
		if results[i] == nil {
			t.Fatalf("results[%d] = nil, want populated", i)
		}
	}
}

// TestRunAnalysisPool_JobsAboveFileCountNoCap asserts a jobs value far above both
// the file count and NumCPU does NOT cap in-flight workers below the file count:
// jobs == 64 over 3 files must reach high-water == 3 (all three run at once). The
// semaphore of 64 leaves every worker free to start, and runAnalysisPool applies
// no NumCPU cap of its own (that is resolveJobs's job, tested in main_test.go).
//
// A sync.WaitGroup barrier sized to the file count makes the assertion
// deterministic, not timing-dependent: each worker arrives, decrements the
// barrier, then blocks on barrier.Wait(), so no worker proceeds until all three
// are simultaneously in-flight. high-water therefore reaches exactly 3 every run.
// Were the pool to cap below 3, fewer than 3 workers would enter, barrier.Wait()
// would never release, and the test would hang rather than flake. [AC3 / 5.2]
func TestRunAnalysisPool_JobsAboveFileCountNoCap(t *testing.T) {
	const n = 3
	const jobs = 64 // > n and > any realistic NumCPU

	var barrier sync.WaitGroup
	barrier.Add(n)

	fake := &inflightAnalysisFake{t: t}
	installAnalysisFake(t, func(ctx context.Context, inputPath string, base *processor.BaseFilterConfig, cb processor.ProgressCallback) (*processor.AnalysisResult, error) {
		// Mark this worker in-flight and record the high-water mark before the
		// barrier so maxSeen reflects all simultaneously-live workers.
		cur := fake.live.Add(1)
		for {
			old := fake.maxSeen.Load()
			if cur <= old || fake.maxSeen.CompareAndSwap(old, cur) {
				break
			}
		}

		// Hold every worker until all n have entered, forcing genuine overlap.
		barrier.Done()
		barrier.Wait()

		fake.live.Add(-1)
		idx := analysisIndexFromPath(t, inputPath)
		return &processor.AnalysisResult{AdaptationDuration: time.Duration(idx)}, nil
	})

	files := makeAnalysisFiles(t, n)
	results := make([]*processor.AnalysisResult, len(files))
	metas := make([]*audio.Metadata, len(files))
	errs := make([]error, len(files))
	base := processor.DefaultFilterConfig()

	runAnalysisPool(context.Background(), nil, files, base, func(string, ...any) {}, jobs, results, metas, errs, stubOpenMetadata(t))

	if got := fake.maxSeen.Load(); got != n {
		t.Fatalf("max in-flight with jobs=%d over %d files = %d, want %d (no cap below file count)", jobs, n, got, n)
	}
	for i := range files {
		if errs[i] != nil {
			t.Fatalf("errs[%d] = %v, want nil", i, errs[i])
		}
		if results[i] == nil {
			t.Fatalf("results[%d] = nil, want populated", i)
		}
	}
}

// TestRunAnalysisPool_OrderedSlots proves results land in submission-index slots
// regardless of completion order. The fake sleeps for an index-derived duration
// where EARLIER indices sleep LONGER, forcing later-submitted indices to finish
// first, so completion order is the reverse of submission order. With jobs >= n
// every worker runs concurrently, so the staggered delays decide completion
// order. Each slot must still carry its own index (encoded in
// AdaptationDuration and in metas' SampleRate). [AC1 / 2.3(c)]
func TestRunAnalysisPool_OrderedSlots(t *testing.T) {
	const n = 6

	var completionOrder []int
	completion := make(chan int, n)

	installAnalysisFake(t, func(_ context.Context, inputPath string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.AnalysisResult, error) {
		idx := analysisIndexFromPath(t, inputPath)
		// Earlier index sleeps longer, so index 0 finishes last and index n-1
		// finishes first: completion order is the reverse of submission order.
		time.Sleep(time.Duration(n-idx) * 10 * time.Millisecond)
		completion <- idx
		return &processor.AnalysisResult{AdaptationDuration: time.Duration(idx)}, nil
	})

	files := makeAnalysisFiles(t, n)
	results := make([]*processor.AnalysisResult, len(files))
	metas := make([]*audio.Metadata, len(files))
	errs := make([]error, len(files))
	base := processor.DefaultFilterConfig()

	// jobs >= n so all workers run concurrently and the index-derived delays
	// alone decide completion order.
	runAnalysisPool(context.Background(), nil, files, base, func(string, ...any) {}, n, results, metas, errs, stubOpenMetadata(t))

	close(completion)
	for idx := range completion {
		completionOrder = append(completionOrder, idx)
	}

	// Prove the test actually exercised out-of-order completion: the first to
	// complete must NOT be the first submitted.
	if len(completionOrder) != n {
		t.Fatalf("completion count = %d, want %d", len(completionOrder), n)
	}
	if completionOrder[0] == 0 {
		t.Fatalf("first completion was index 0; staggered delays failed to reorder completion (order=%v)", completionOrder)
	}
	if completionOrder[0] != n-1 {
		t.Fatalf("first completion = index %d, want %d (last submitted finishes first); order=%v", completionOrder[0], n-1, completionOrder)
	}

	// Despite reversed completion, each slot carries its own submission index.
	for i := range files {
		if errs[i] != nil {
			t.Fatalf("errs[%d] = %v, want nil", i, errs[i])
		}
		if results[i] == nil {
			t.Fatalf("results[%d] = nil, want populated", i)
		}
		if got := int(results[i].AdaptationDuration); got != i {
			t.Fatalf("results[%d] carries index %d, want %d (slot mismatch)", i, got, i)
		}
		if metas[i] == nil {
			t.Fatalf("metas[%d] = nil, want populated", i)
		}
		if got := metas[i].SampleRate - 48000; got != i {
			t.Fatalf("metas[%d] carries index %d, want %d (slot mismatch)", i, got, i)
		}
	}
}

// TestRunAnalysisPool_FailureIsolation drives the pool where one index errors and
// the rest succeed. It asserts errs[failIdx] is set, sibling results are non-nil
// with nil errs, and (with p == nil) runAnalysisPool returns only after ALL
// workers finish (no early abort). [AC4 / 2.3(d)]
func TestRunAnalysisPool_FailureIsolation(t *testing.T) {
	const n = 6
	const failIdx = 1

	sentinel := errors.New("analysisFake: synthetic analysis failure")

	installAnalysisFake(t, func(_ context.Context, inputPath string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.AnalysisResult, error) {
		idx := analysisIndexFromPath(t, inputPath)
		if idx == failIdx {
			return nil, sentinel
		}
		return &processor.AnalysisResult{AdaptationDuration: time.Duration(idx)}, nil
	})

	files := makeAnalysisFiles(t, n)
	results := make([]*processor.AnalysisResult, len(files))
	metas := make([]*audio.Metadata, len(files))
	errs := make([]error, len(files))
	base := processor.DefaultFilterConfig()

	runAnalysisPool(context.Background(), nil, files, base, func(string, ...any) {}, 3, results, metas, errs, stubOpenMetadata(t))

	// The failing slot carries its error.
	if !errors.Is(errs[failIdx], sentinel) {
		t.Fatalf("errs[%d] = %v, want sentinel", failIdx, errs[failIdx])
	}

	// Every sibling ran to completion (no early abort): non-nil result, nil err.
	for j := range files {
		if j == failIdx {
			continue
		}
		if errs[j] != nil {
			t.Fatalf("sibling errs[%d] = %v, want nil", j, errs[j])
		}
		if results[j] == nil {
			t.Fatalf("sibling results[%d] = nil, want populated (proof it ran)", j)
		}
		if got := int(results[j].AdaptationDuration); got != j {
			t.Fatalf("sibling results[%d] carries index %d, want %d", j, got, j)
		}
	}
}
