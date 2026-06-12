package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/linuxmatters/jivetalking/internal/processor"
	"github.com/linuxmatters/jivetalking/internal/report"
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
func launchWorkerPool(ctx context.Context, p *tea.Program, files []string, base *processor.BaseFilterConfig, sharedLog func(string, ...any), jobs int, diagnostics bool, reportWarnings chan<- string) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		runWorkerPool(ctx, p, files, base, sharedLog, jobs, diagnostics, reportWarnings)
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
//
// diagnostics gates the bulk diagnostic artefacts (the .jsonl sidecars and, from
// T4.1, the spectrogram PNGs). When false the always-on set (.flac/.md/.json)
// still writes; only the opt-in sidecars are skipped.
func runWorkerPool(ctx context.Context, p *tea.Program, files []string, base *processor.BaseFilterConfig, sharedLog func(string, ...any), jobs int, diagnostics bool, reportWarnings chan<- string) {
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup

	// Spectrogram renders run in background goroutines off the file-worker critical
	// path (proposal §4). specSem bounds them to the jobs budget shared across ALL
	// files - one pool-level semaphore, never one unbounded goroutine per PNG, so
	// ffmpeg is not oversubscribed beyond the worker budget. specWG tracks every
	// render so the pool waits for all PNGs before AllCompleteMsg / program exit.
	specSem := make(chan struct{}, jobs)
	var specWG sync.WaitGroup

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

			// Build the run record once and reuse it for both the Markdown report
			// and the .json: NewRunRecord(result) reads the same in-memory result,
			// so building it twice would be wasted work.
			rec := processor.NewRunRecord(result)

			outputStem := strings.TrimSuffix(result.OutputPath, filepath.Ext(result.OutputPath))

			// Attach the spectrogram path list SYNCHRONOUSLY, before the .md/.json
			// write, so both carry the links. This is pure string work (no ffmpeg);
			// the PNGs themselves render in background goroutines launched after the
			// writes. Off path: no list, no goroutines (proposal §4).
			if diagnostics {
				rec.Spectrograms = processor.DeriveSpectrogramImages(rec, outputStem, processor.ProcessingSpectrogramStages)
			}

			// Write the Markdown report beside the processed audio: swap the output
			// extension for .md → <name>-LUFS-NN-processed.md. A write failure is
			// non-fatal: the processed audio and the .json are the product, the
			// report a side artefact.
			mdPath := outputStem + ".md"
			if err := report.WriteMarkdownReport(rec, buildProcessingTimings(fileStartTime, ph.timings(pass2Time), result), mdPath); err != nil {
				wlog("[POOL] Failed to write Markdown report: %v", err)
				reportWarnings <- fmt.Sprintf("Report was not written for %s: %v", inputPath, err)
			}

			// Emit the run record beside the .md. The .json path is derived from
			// OutputPath exactly as the .md is, so they sit together. A write
			// failure is non-fatal, matching the report write above.
			recordPath := outputStem + ".json"
			if err := processor.WriteRunRecord(rec, recordPath); err != nil {
				wlog("[POOL] Failed to write run record: %v", err)
				reportWarnings <- fmt.Sprintf("Run record was not written for %s: %v", inputPath, err)
			}

			// Stream the full interval and candidate series to .jsonl sidecars
			// beside the record (§8.5 call 2 / §9.3): the bulk data the summary
			// stands in for. Opt-in behind --diagnostics; the always-on .json
			// carries the inline summaries, so .md/.json stay populated when off.
			// Same non-fatal contract as the record above.
			if diagnostics {
				if err := processor.WriteRunRecordSidecars(result.Measurements, recordPath); err != nil {
					wlog("[POOL] Failed to write run record sidecars: %v", err)
					reportWarnings <- fmt.Sprintf("Run record sidecars were not written for %s: %v", inputPath, err)
				}
			}

			// Launch the spectrogram renders in background goroutines, OFF the
			// critical path: the .md/.json/sidecars are written and FileCompleteMsg
			// fires below without waiting for any PNG. Each render is bounded by the
			// pool-level specSem (shared across files, sized to the jobs budget) and
			// tracked on specWG, which the pool waits on before AllCompleteMsg so the
			// process does not exit until every PNG lands. Render failure is
			// non-fatal (reportWarnings); ctx cancellation aborts + cleans partials
			// inside generateSpectrogram. The list is empty when --diagnostics is off,
			// so the loop launches nothing.
			destDir := filepath.Dir(result.OutputPath)
			for _, img := range rec.Spectrograms {
				specWG.Add(1)
				go func(img processor.SpectrogramImage) {
					// Register specWG.Done() before the acquire select so a render
					// skipped on a cancelled ctx still decrements specWG; otherwise
					// specWG.Wait() hangs.
					defer specWG.Done()

					select {
					case specSem <- struct{}{}:
						defer func() { <-specSem }()
					case <-ctx.Done():
						return
					}

					if err := processor.RenderSpectrogramImage(ctx, img, rec, inputPath, result.OutputPath, destDir); err != nil {
						wlog("[POOL] Failed to render spectrogram %s: %v", img.Path, err)
						reportWarnings <- fmt.Sprintf("Spectrogram %s was not written for %s: %v", img.Path, inputPath, err)
					}
				}(img)
			}

			finalNoiseFloor, _ := processor.FinalNoiseFloor(result)

			// Surface the final-output true peak and loudness range from NormResult for
			// the done-box before→after rows. Read-only: both are measured by ebur128 on
			// the final output during normalisation, never recomputed here. NormResult is
			// nil when normalisation was disabled/skipped; FinalMeasurements may also be
			// nil, so guard both and leave the value at 0 (the UI gates the row).
			var outputTP, outputLRA float64
			if result.NormResult != nil {
				outputTP = result.NormResult.OutputTP
				if fm := result.NormResult.FinalMeasurements; fm != nil {
					outputLRA = fm.Loudness.OutputLRA
				}
			}

			// Confirm the Limiter row at completion. The row already lit during Pass 4
			// (progressHandler resends the summary with the ceiling on the Pass-4-start
			// update), so this is a harmless final confirmation with the same ceiling
			// from the authoritative NormResult. ph.summary already carries the Pass-4
			// limiter merge, so WithLimiter here re-applies the identical value.
			// State-change only; no per-frame work.
			p.Send(ui.AdaptedSummaryMsg{
				FileIndex: i,
				Summary:   ph.summary.WithLimiter(result.NormResult),
			})

			wlog("[POOL] Sending FileCompleteMsg for file %d", i)
			p.Send(ui.FileCompleteMsg{
				FileIndex:        i,
				InputLUFS:        result.InputLUFS,
				OutputLUFS:       result.OutputLUFS,
				FinalNoiseFloor:  finalNoiseFloor,
				OutputTP:         outputTP,
				OutputLRA:        outputLRA,
				OutputPath:       result.OutputPath,
				Quality:          processor.ComputeQualityScore(result),
				RecordingQuality: processor.ComputeRecordingScore(result.Measurements),
				ProcessingTime:   time.Since(fileStartTime),
			})
		}(i, inputPath)
	}

	wg.Wait()

	// Gate program exit on the spectrogram renders: every file's per-file
	// FileCompleteMsg has already fired (wg drained), so the file-worker TUI is not
	// held up; only AllCompleteMsg (which quits the program) waits here, so the
	// process does not exit until every PNG lands. On a user ctx cancel the
	// in-flight renders abort and clean their partials, then specWG drains. With
	// --diagnostics off nothing was launched, so this returns at once.
	specWG.Wait()

	sharedLog("[POOL] Sending AllCompleteMsg")
	p.Send(ui.AllCompleteMsg{})
}
