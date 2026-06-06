package main

import (
	"context"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/processor"
	"github.com/linuxmatters/jivetalking/internal/ui"
)

// analysisPoolAnalyze is the analysis-only entry point, a package var so tests
// can substitute a fake to observe concurrency without running real FFmpeg. It
// defaults to the real processor call, mirroring the poolProcessAudio seam in
// cmd/jivetalking/pool.go.
var analysisPoolAnalyze = processor.AnalyzeOnlyDetailed

// runAnalysisPool analyses files concurrently under a bounded worker pool
// sharing one tea.Program. A buffered semaphore of size jobs caps in-flight
// workers; a sync.WaitGroup tracks completion. Each worker owns its file index,
// a per-file prefixed logger, and a per-worker config clone, mirroring
// runWorkerPool in pool.go. After all workers finish it sends ui.AllCompleteMsg.
// With jobs == 1 the observable outcome matches the serial path.
//
// The caller pre-allocates results, metas, and errs to len(files); each worker
// writes only its own results[i], metas[i], and errs[i] slot, so no slot is
// shared. Ordered printing happens in the caller after this returns.
//
// When p is nil the same body runs the no-TTY path with no p.Send calls: every
// p.Send is gated by a p != nil check.
//
// On cancellation a not-yet-started worker skips its work via the ctx.Done()
// select at acquire, while an in-flight worker aborts mid-frame because ctx is
// threaded into AnalyzeOnlyDetailed. Either way wg.Done() fires so wg.Wait()
// returns and ui.AllCompleteMsg is sent.
func runAnalysisPool(ctx context.Context, p *tea.Program, files []string, base *processor.BaseFilterConfig, sharedLog func(string, ...any), jobs int, results []*processor.AnalysisResult, metas []*audio.Metadata, errs []error, openMetadata func(string) (*audio.Metadata, error)) {
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup

	for i, inputPath := range files {
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
			case <-ctx.Done():
				return
			}

			wlog := withFilePrefix(inputPath, sharedLog)

			if p != nil {
				wlog("[ANALYSIS-POOL] Sending AnalysisStartMsg for file %d: %s", i, inputPath)
				p.Send(ui.AnalysisStartMsg{
					FileIndex: i,
					FileName:  inputPath,
					FilePath:  inputPath,
				})
			}

			meta, err := openMetadata(inputPath)
			metas[i] = meta
			if err != nil {
				wlog("[ANALYSIS-POOL] openMetadata failed: %v", err)
				errs[i] = err
				if p != nil {
					p.Send(ui.AnalysisCompleteMsg{
						FileIndex: i,
						Error:     err,
					})
				}
				return
			}

			clone := base.CloneForWorker(wlog)

			var cb processor.ProgressCallback
			if p != nil {
				cb = func(update processor.ProgressUpdate) {
					wlog("[ANALYSIS-POOL] Progress: Pass %d (%s), %.1f%%, Level %.1f dB", update.Pass, update.PassName, update.Progress*100, update.Level)
					p.Send(ui.AnalysisProgressMsg{
						FileIndex: i,
						Progress:  update.Progress,
						Level:     update.Level,
					})
				}
			}

			wlog("[ANALYSIS-POOL] Starting AnalyzeOnlyDetailed for %s", inputPath)
			results[i], errs[i] = analysisPoolAnalyze(ctx, inputPath, clone, cb)

			if p != nil {
				wlog("[ANALYSIS-POOL] Sending AnalysisCompleteMsg for file %d", i)
				p.Send(ui.AnalysisCompleteMsg{
					FileIndex: i,
					Result:    results[i],
					Error:     errs[i],
				})
			}
		}(i, inputPath)
	}

	wg.Wait()

	if p != nil {
		sharedLog("[ANALYSIS-POOL] Sending AllCompleteMsg")
		p.Send(ui.AllCompleteMsg{})
	}
}
