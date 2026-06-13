package main

import (
	"context"
	"sync"

	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/processor"
	"github.com/linuxmatters/jivetalking/internal/ui"
)

// analysisSlot is one file's analysis output. The pool pre-allocates a
// []analysisSlot of len(files) and each worker writes only its own index slot,
// so no slot is shared. Collapsing the former parallel results/metas/errs slices
// into one slot struct keeps the three values that always travel together (same
// length, same index) in one place. Ordered reads happen in the caller after the
// pool returns.
type analysisSlot struct {
	result *processor.AnalysisResult
	meta   *audio.Metadata
	err    error
}

// analysisPoolDeps injects the analysis-pool's two seams so tests can substitute
// fakes to observe concurrency without running real FFmpeg or mutating package
// state, mirroring workerPoolDeps in pool.go. analyse is the per-file analysis
// entry point; openMetadata reads source provenance. Production callers use
// defaultAnalysisPoolDeps().
type analysisPoolDeps struct {
	analyse      func(context.Context, string, *processor.BaseFilterConfig, processor.ProgressCallback) (*processor.AnalysisResult, error)
	openMetadata func(string) (*audio.Metadata, error)
}

func defaultAnalysisPoolDeps() analysisPoolDeps {
	return analysisPoolDeps{
		analyse:      processor.AnalyseOnlyDetailed,
		openMetadata: openAudioMetadata,
	}
}

// runAnalysisPool analyses files concurrently under a bounded worker pool
// sharing one tea.Program. A buffered semaphore of size env.jobs caps in-flight
// workers; a sync.WaitGroup tracks completion. Each worker owns its file index,
// a per-file prefixed logger, and a per-worker config clone, mirroring
// runWorkerPool in pool.go. After all workers finish it sends ui.AllCompleteMsg.
// With env.jobs == 1 the observable outcome matches the serial path.
//
// The caller pre-allocates slots to len(env.files); each worker writes only its
// own slots[i], so no slot is shared. Ordered printing happens in the caller
// after this returns.
//
// When env.p is nil the same body runs the no-TTY path with no p.Send calls:
// every p.Send is gated by a p != nil check.
//
// On cancellation a not-yet-started worker skips its work via the ctx.Done()
// select at acquire, while an in-flight worker aborts mid-frame because ctx is
// threaded into deps.analyse. Either way wg.Done() fires so wg.Wait() returns
// and ui.AllCompleteMsg is sent.
func runAnalysisPool(env poolEnv, slots []analysisSlot, deps analysisPoolDeps) {
	sem := make(chan struct{}, env.jobs)
	var wg sync.WaitGroup

	for i, inputPath := range env.files {
		wg.Add(1)
		go func(i int, inputPath string) {
			// Register wg.Done() before the acquire select so a worker skipped
			// on a cancelled ctx still decrements the WaitGroup; otherwise
			// wg.Wait() hangs.
			defer wg.Done()

			// Acquire the semaphore, but bail out if ctx is already cancelled so
			// a not-yet-started worker skips its work cleanly. Only release the
			// slot when this branch actually took one.
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-env.ctx.Done():
				return
			}

			wlog := withFilePrefix(inputPath, env.sharedLog)

			if env.p != nil {
				wlog("[ANALYSIS-POOL] Sending AnalysisStartMsg for file %d: %s", i, inputPath)
				env.p.Send(ui.AnalysisStartMsg{
					FileIndex: i,
					FileName:  inputPath,
					FilePath:  inputPath,
				})
			}

			meta, err := deps.openMetadata(inputPath)
			slots[i].meta = meta
			if err != nil {
				wlog("[ANALYSIS-POOL] openMetadata failed: %v", err)
				slots[i].err = err
				if env.p != nil {
					env.p.Send(ui.AnalysisCompleteMsg{
						FileIndex: i,
						Error:     err,
					})
				}
				return
			}

			clone := env.base.CloneForWorker(wlog)

			var cb processor.ProgressCallback
			if env.p != nil {
				cb = func(update processor.ProgressUpdate) {
					wlog("[ANALYSIS-POOL] Progress: Pass %d (%s), %.1f%%, Level %.1f dB", update.Pass, update.PassName, update.Progress*100, update.Level)
					env.p.Send(ui.AnalysisProgressMsg{
						FileIndex: i,
						Progress:  update.Progress,
						Level:     update.Level,
					})
				}
			}

			wlog("[ANALYSIS-POOL] Starting AnalyseOnlyDetailed for %s", inputPath)
			slots[i].result, slots[i].err = deps.analyse(env.ctx, inputPath, clone, cb)

			if env.p != nil {
				wlog("[ANALYSIS-POOL] Sending AnalysisCompleteMsg for file %d", i)
				env.p.Send(ui.AnalysisCompleteMsg{
					FileIndex: i,
					Result:    slots[i].result,
					Error:     slots[i].err,
				})
			}
		}(i, inputPath)
	}

	wg.Wait()

	if env.p != nil {
		env.sharedLog("[ANALYSIS-POOL] Sending AllCompleteMsg")
		env.p.Send(ui.AllCompleteMsg{})
	}
}
