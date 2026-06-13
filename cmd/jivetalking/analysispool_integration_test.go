//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

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
		// is the production analysisPoolAnalyse's job and is covered separately by
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
// processor.AnalyseOnlyDetailed and the REAL openAudioMetadata opener. Unlike the
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

	// Pin the seam to the REAL analyse path. installAnalysisFake save/restores it,
	// so a parallel seam-swapping test cannot leak its fake into this run.
	installAnalysisFake(t, processor.AnalyseOnlyDetailed)

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
