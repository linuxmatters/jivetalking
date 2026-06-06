package main

import (
	"context"
	"errors"
	"os"
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

// installAnalysisFake swaps analysisPoolAnalyze for fn and restores it after the
// test, mirroring pool_test.go's installInflightFake save/restore so tests stay
// isolated from each other and from main_test.go's swaps.
func installAnalysisFake(t *testing.T, fn func(context.Context, string, *processor.BaseFilterConfig, processor.ProgressCallback) (*processor.AnalysisResult, error)) {
	t.Helper()
	orig := analysisPoolAnalyze
	analysisPoolAnalyze = fn
	t.Cleanup(func() { analysisPoolAnalyze = orig })
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

// TestRunAnalysisPool_CancellationAbortsPromptly drives runAnalysisPool with a
// ctx cancelled mid-run and proves two things deterministically: queued workers
// skip via the acquire select, and the pool unwinds promptly once in-flight
// workers abort. jobs < file count (jobs=2, files=6) guarantees the remaining 4
// workers are still queued at the acquire select when ctx is cancelled.
//
// Determinism hinges on holding the semaphore slots occupied across the skip
// assertion. The fake signals an entry channel, then blocks on a release gate,
// returning only after the gate opens. Blocking on the gate alone (not ctx.Done())
// keeps the slots held while ctx is cancelled, which is what makes the skip
// deterministic: were the in-flight fake to release on ctx.Done(), it would free
// its slot the instant cancel fired and a queued worker could win that freed slot
// before observing ctx.Done(). The test waits for the jobs entries via a barrier
// (no sleep), so the queued workers are provably stuck at the acquire select with
// no free slot. Cancelling ctx then leaves each queued worker's select with
// sem-send still blocked (slots held by the gated in-flight workers) and
// ctx.Done() ready, forcing the ctx.Done() skip branch. With slots held, started
// can never exceed jobs: every queued worker skips, never running the fake. The
// test asserts started == jobs while the gate is still closed, then opens the
// gate so the in-flight workers return and the pool unwinds.
//
// Prompt return is then asserted via the done channel against a bounded 2s
// deadline: every worker's wg.Done() must fire so wg.Wait() returns.
//
// Driven with p == nil so no real terminal is needed. Production sends
// ui.AllCompleteMsg after wg.Wait() unconditionally when p != nil; that TTY path
// is covered by the model quit tests (1.5 / 5.1). [AC5 / 5.6]
func TestRunAnalysisPool_CancellationAbortsPromptly(t *testing.T) {
	const n = 6
	const jobs = 2 // < n so the other n-jobs workers stay queued at the acquire select

	var started atomic.Int32
	entered := make(chan struct{}, n)
	gate := make(chan struct{})

	installAnalysisFake(t, func(ctx context.Context, _ string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.AnalysisResult, error) {
		started.Add(1)
		entered <- struct{}{}
		// Hold the semaphore slot until the gate opens so queued workers cannot
		// acquire a freed slot during the skip assertion. Blocking on the gate
		// alone (not ctx.Done()) keeps the slots occupied while ctx is cancelled,
		// making the queued workers' skip deterministic. The in-flight ctx abort
		// is the production analysisPoolAnalyze's job and is covered separately by
		// the seam returning context.Canceled; here the gate stands in so the slot
		// stays held across the assertion.
		<-gate
		return nil, ctx.Err()
	})

	files := makeAnalysisFiles(t, n)
	results := make([]*processor.AnalysisResult, len(files))
	metas := make([]*audio.Metadata, len(files))
	errs := make([]error, len(files))
	base := processor.DefaultFilterConfig()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runAnalysisPool(ctx, nil, files, base, func(string, ...any) {}, jobs, results, metas, errs, stubOpenMetadata(t))
		close(done)
	}()

	// Wait until exactly the jobs in-flight workers have entered the fake. The
	// queued workers are now provably stuck at the acquire select (no free slot).
	for range jobs {
		<-entered
	}

	cancel()

	// The in-flight workers hold their slots (still in the gated select), so a
	// queued worker's acquire select cannot send on sem and must take the
	// ctx.Done() skip branch. Confirm no further entry appears: started stays at
	// jobs because every queued worker skips without running the fake. A short
	// settle gives any erroneously-admitted worker time to bump the counter; the
	// gate stays closed throughout so a correct skip cannot be undone.
	if extra := waitForExtraEntry(entered, 100*time.Millisecond); extra {
		t.Fatalf("a queued worker entered the fake after cancel; it must skip via the acquire select (started=%d)", started.Load())
	}
	if got := started.Load(); got != jobs {
		t.Fatalf("started = %d, want %d (queued workers must skip via the acquire select, never running the fake)", got, jobs)
	}

	// Release the gated in-flight workers so they return and the pool unwinds.
	close(gate)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runAnalysisPool did not return promptly after cancel (every wg.Done() must fire so wg.Wait() unwinds)")
	}
}

// waitForExtraEntry reports whether another worker signals the entered channel
// within d, used to detect a queued worker erroneously running the fake after
// cancel instead of skipping at the acquire select.
func waitForExtraEntry(entered <-chan struct{}, d time.Duration) bool {
	select {
	case <-entered:
		return true
	case <-time.After(d):
		return false
	}
}

// TestRunAnalysisPool_ConcurrentRaceClean drives runAnalysisPool with jobs >= 2
// over two distinct REAL fixture copies through the REAL
// processor.AnalyzeOnlyDetailed and the REAL openAudioMetadata opener. Unlike the
// seam-based 2.3 tests, it runs actual concurrent FFmpeg analysis so `-race`
// observes the genuine concurrent paths: the shared debugSink-backed logger
// (whole-line atomic writes), the per-worker CloneForWorker config clones, and the
// pre-allocated results/metas/errs slots each worker writes only its own slot of.
// It drives with p == nil (no tea.Program, no TTY) to keep the race test focused
// on the pool internals. After the run every slot must be populated
// (results[i] != nil, errs[i] == nil, metas[i] != nil). [AC2 / 5.1]
func TestRunAnalysisPool_ConcurrentRaceClean(t *testing.T) {
	src := findPoolTestAudio(t)
	if src == "" {
		t.Skip("no audio file found under testdata/; drop a .flac (e.g. testdata/fixture-5m.flac) to run this test")
	}
	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Skipf("testdata audio not found: %s", src)
	}

	// Pin the seam to the REAL analyze path. installAnalysisFake save/restores it,
	// so a parallel seam-swapping test cannot leak its fake into this run.
	installAnalysisFake(t, processor.AnalyzeOnlyDetailed)

	ext := filepath.Ext(src)
	dir := t.TempDir()
	files := []string{
		copyFixtureTo(t, src, dir, "analysis-a"+ext),
		copyFixtureTo(t, src, dir, "analysis-b"+ext),
	}

	// Shared debugSink backs the shared logger; every worker writes whole lines
	// through it concurrently, exercising the sink's serialisation under -race.
	sinkFile, err := os.CreateTemp(dir, "debug-*.log")
	if err != nil {
		t.Fatalf("create debug sink file: %v", err)
	}
	t.Cleanup(func() { sinkFile.Close() })
	sink := newDebugSink(sinkFile)
	sharedLog := sink.Logf

	base := processor.DefaultFilterConfig()
	base.SetLogger(sharedLog)

	results := make([]*processor.AnalysisResult, len(files))
	metas := make([]*audio.Metadata, len(files))
	errs := make([]error, len(files))

	// jobs == len(files) so both real analyses run concurrently, forcing
	// concurrent sink writes, CloneForWorker calls, and slot writes. p == nil so
	// no real terminal is needed. openAudioMetadata is the production opener used
	// by defaultAnalysisOnlyDeps.
	runAnalysisPool(context.Background(), nil, files, base, sharedLog, len(files), results, metas, errs, openAudioMetadata)

	for i := range files {
		if errs[i] != nil {
			t.Fatalf("errs[%d] = %v, want nil", i, errs[i])
		}
		if results[i] == nil {
			t.Fatalf("results[%d] = nil, want populated", i)
		}
		if metas[i] == nil {
			t.Fatalf("metas[%d] = nil, want populated", i)
		}
	}
}
