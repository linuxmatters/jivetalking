package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/linuxmatters/jivetalking/internal/logging"
	"github.com/linuxmatters/jivetalking/internal/processor"
	"github.com/linuxmatters/jivetalking/internal/ui"
)

// poolProcessAudio is the processing entry point, a package var so tests can
// substitute a fake to observe concurrency without running real FFmpeg. It
// defaults to the real processor call, mirroring the loudnormRunFilterGraph
// seam idiom in internal/processor/normalise.go.
var poolProcessAudio = processor.ProcessAudio

// launchWorkerPool starts runWorkerPool in a goroutine and returns a channel
// closed once the pool has fully unwound. Callers block on the channel after
// cancelling the context so all workers' deferred temp cleanup runs before the
// process exits, giving the no-residue-on-cancel guarantee. Keeping the launch
// and join in one helper makes the wiring unit-testable apart from main().
func launchWorkerPool(ctx context.Context, p *tea.Program, files []string, base *processor.BaseFilterConfig, sharedLog func(string, ...any), jobs int, reportWarnings chan<- string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		runWorkerPool(ctx, p, files, base, sharedLog, jobs, reportWarnings)
		close(done)
	}()
	return done
}

// runWorkerPool processes files concurrently under a bounded worker pool sharing
// one tea.Program. A buffered semaphore of size jobs caps in-flight workers; a
// sync.WaitGroup tracks completion. Each worker owns its file index, a per-file
// prefixed logger, and a per-worker config clone, mirroring the serial loop's
// per-file body. After all workers finish it sends ui.AllCompleteMsg. With
// jobs == 1 the observable outcome matches the serial path.
//
// On cancellation a not-yet-started worker skips its work via the ctx.Done()
// select at acquire, while an in-flight worker aborts mid-frame because ctx is
// threaded into ProcessAudio. Either way wg.Done() fires so wg.Wait() returns
// and ui.AllCompleteMsg is sent.
func runWorkerPool(ctx context.Context, p *tea.Program, files []string, base *processor.BaseFilterConfig, sharedLog func(string, ...any), jobs int, reportWarnings chan<- string) {
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

			fileStartTime := time.Now()

			wlog := withFilePrefix(inputPath, sharedLog)

			wlog("[POOL] Sending FileStartMsg for file %d: %s", i, inputPath)
			p.Send(ui.FileStartMsg{
				FileIndex: i,
				FileName:  inputPath,
			})

			ph := &progressHandler{
				p:         p,
				log:       wlog,
				fileIndex: i,
			}

			clone := base.CloneForWorker(wlog)

			pass2Start := time.Now()
			wlog("[POOL] Starting ProcessAudio for %s", inputPath)
			result, err := poolProcessAudio(ctx, inputPath, clone, ph.callback)
			if err != nil {
				wlog("[POOL] ProcessAudio failed: %v", err)
				p.Send(ui.FileCompleteMsg{
					FileIndex: i,
					Error:     err,
				})
				return
			}

			// ProcessAudio runs all four passes; isolate Pass 2 by subtracting the
			// passes the progress handler timed directly.
			pass2Time := time.Since(pass2Start) - ph.pass1Time - ph.pass3Time - ph.pass4Time

			reportData := buildProcessingReportData(inputPath, fileStartTime, ph.timings(pass2Time), result)
			if err := logging.GenerateReport(reportData); err != nil {
				wlog("[POOL] Failed to generate log file: %v", err)
				reportWarnings <- fmt.Sprintf("Report was not written for %s: %v", inputPath, err)
			}

			wlog("[POOL] Sending FileCompleteMsg for file %d", i)
			p.Send(ui.FileCompleteMsg{
				FileIndex:  i,
				InputLUFS:  result.InputLUFS,
				OutputLUFS: result.OutputLUFS,
				NoiseFloor: result.NoiseFloor,
				OutputPath: result.OutputPath,
			})
		}(i, inputPath)
	}

	wg.Wait()

	sharedLog("[POOL] Sending AllCompleteMsg")
	p.Send(ui.AllCompleteMsg{})
}
