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

// sendWarning delivers a non-fatal warning to the reportWarnings channel without
// ever blocking the sending worker. The channel is buffered to len(files) and is
// only drained after the pool fully unwinds, yet a single file emits several
// warnings (report, run record, sidecars, one per spectrogram), so a burst of
// failures could fill the buffer and block a worker - which would stall
// wg.Wait()/specWG.Wait() and deadlock the run. The warnings are best-effort
// diagnostics, so dropping one under saturation is the safe trade.
func sendWarning(ch chan<- string, msg string) {
	select {
	case ch <- msg:
	default:
	}
}

// launchSpectrogramRenders schedules each image in imgs as a bounded background
// render goroutine, the shared pattern for both the processing pool and the
// analysis-only path. Each goroutine registers specWG.Done() BEFORE the acquire
// select so a render skipped on a cancelled ctx still decrements specWG;
// otherwise specWG.Wait() hangs. It then acquires a specSem slot or bails on
// ctx.Done() (a not-yet-started render skips cleanly, releasing the slot only on
// the branch that took one), and invokes render(ctx, img). render owns the
// RenderSpectrogramImage call (it varies by caller: the source/output paths and
// the run record differ between processing and analysis-only); on a non-fatal
// error it returns it and onError surfaces the warning. ctx cancellation aborts
// the render and cleans partial PNGs inside generateSpectrogram. imgs is empty
// when --diagnostics is off, so nothing is launched.
func launchSpectrogramRenders(
	ctx context.Context,
	imgs []processor.SpectrogramImage,
	specSem chan struct{},
	specWG *sync.WaitGroup,
	render func(context.Context, processor.SpectrogramImage) error,
	onError func(processor.SpectrogramImage, error),
) {
	for _, img := range imgs {
		specWG.Add(1)
		go func(img processor.SpectrogramImage) {
			defer specWG.Done()

			select {
			case specSem <- struct{}{}:
				defer func() { <-specSem }()
			case <-ctx.Done():
				return
			}

			if err := render(ctx, img); err != nil {
				onError(img, err)
			}
		}(img)
	}
}

// poolEnv bundles the environment both pools share: the cancellable ctx, the
// shared tea.Program (nil on the no-TTY analysis path), the input files, the
// caller-owned config seed each worker clones, the shared logger, and the worker
// budget. Grouping the common prefix keeps the two pool signatures short and
// stops same-typed args sitting adjacent where a caller could transpose them.
type poolEnv struct {
	ctx       context.Context
	p         *tea.Program
	files     []string
	base      *processor.BaseFilterConfig
	sharedLog func(string, ...any)
	jobs      int
}

// workerPoolDeps injects the pool's processing entry point so tests can
// substitute a fake to observe concurrency without running real FFmpeg or
// mutating package state, following the analysisOnlyDeps pattern in main.go.
// Production callers use defaultWorkerPoolDeps().
type workerPoolDeps struct {
	processAudio func(context.Context, string, *processor.BaseFilterConfig, processor.ProgressCallback) (*processor.ProcessingResult, error)
}

func defaultWorkerPoolDeps() workerPoolDeps {
	return workerPoolDeps{processAudio: processor.ProcessAudio}
}

// launchWorkerPool starts runWorkerPool in a goroutine and returns a channel
// closed once the pool has fully unwound. Callers block on the channel after
// cancelling the context so all workers' deferred temp cleanup runs before the
// process exits, giving the no-residue-on-cancel guarantee. Keeping the launch
// and join in one helper makes the wiring unit-testable apart from main().
func launchWorkerPool(env poolEnv, diagnostics bool, reportWarnings chan<- string, deps workerPoolDeps) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		runWorkerPool(env, diagnostics, reportWarnings, deps)
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
// diagnostics gates the bulk diagnostic artefacts (the .jsonl sidecars and the
// spectrogram PNGs). When false the always-on set (.flac/.md/.json) still
// writes; only the opt-in sidecars are skipped.
func runWorkerPool(env poolEnv, diagnostics bool, reportWarnings chan<- string, deps workerPoolDeps) {
	sem := make(chan struct{}, env.jobs)
	var wg sync.WaitGroup

	// Spectrogram renders run in background goroutines off the file-worker critical
	// path. specSem bounds them to the jobs budget shared across ALL files - one
	// pool-level semaphore, never one unbounded goroutine per PNG, so ffmpeg is not
	// oversubscribed beyond the worker budget. specWG tracks every render so the
	// pool waits for all PNGs before AllCompleteMsg / program exit.
	specSem := make(chan struct{}, env.jobs)
	var specWG sync.WaitGroup
	render := processingRenderScheduler{sem: specSem, wg: &specWG}

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

			fileStartTime := time.Now()

			wlog := withFilePrefix(inputPath, env.sharedLog)

			wlog("[POOL] Sending FileStartMsg for file %d: %s", i, inputPath)
			env.p.Send(ui.FileStartMsg{
				FileIndex: i,
				FileName:  inputPath,
			})

			ph := &progressHandler{
				p:         env.p,
				log:       wlog,
				fileIndex: i,
			}

			clone := env.base.CloneForWorker(wlog)

			pass2Start := time.Now()
			wlog("[POOL] Starting ProcessAudio for %s", inputPath)
			result, err := deps.processAudio(env.ctx, inputPath, clone, ph.callback)
			if err != nil {
				wlog("[POOL] ProcessAudio failed: %v", err)
				env.p.Send(ui.FileCompleteMsg{
					FileIndex: i,
					Error:     err,
				})
				return
			}

			// ProcessAudio runs all four passes; isolate Pass 2 by subtracting the
			// passes the progress handler timed directly.
			pass2Time := time.Since(pass2Start) - ph.pass1Time - ph.pass3Time - ph.pass4Time

			emitProcessingReport(env, inputPath, result, ph, fileStartTime, pass2Time, diagnostics, reportWarnings, render)
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

	env.sharedLog("[POOL] Sending AllCompleteMsg")
	env.p.Send(ui.AllCompleteMsg{})
}

// processingRenderScheduler bundles the pool-level background spectrogram-render
// state shared across workers: the jobs-sized semaphore bounding concurrent
// renders and the WaitGroup the pool drains before AllCompleteMsg so every PNG
// lands. Renders honour env.ctx for cancellation (passed through at launch),
// mirroring analysisRenderScheduler on the analysis-only path.
type processingRenderScheduler struct {
	sem chan struct{}
	wg  *sync.WaitGroup
}

// emitProcessingReport writes one file's processing artefacts after a successful
// 4-pass run and dispatches the final TUI messages: it builds the run record,
// writes the always-on .md/.json, the opt-in .jsonl sidecars and before/after
// spectrogram PNGs under --diagnostics, then sends the limiter-confirming
// AdaptedSummaryMsg and the FileCompleteMsg. Every write failure is non-fatal and
// isolated (reportWarnings) so the remaining artefacts still emit, mirroring
// emitAnalysisReport on the analysis-only path. ph supplies the per-pass timings
// and the retained filter-chain summary captured during ProcessAudio.
func emitProcessingReport(env poolEnv, inputPath string, result *processor.ProcessingResult, ph *progressHandler, fileStartTime time.Time, pass2Time time.Duration, diagnostics bool, reportWarnings chan<- string, render processingRenderScheduler) {
	wlog := ph.log
	i := ph.fileIndex

	// Build the run record once and reuse it for both the Markdown report
	// and the .json: NewRunRecord(result) reads the same in-memory result,
	// so building it twice would be wasted work.
	rec := processor.NewRunRecord(result)

	outputStem := strings.TrimSuffix(result.OutputPath, filepath.Ext(result.OutputPath))

	// Attach the spectrogram path list SYNCHRONOUSLY, before the .md/.json
	// write, so both carry the links. This is pure string work (no ffmpeg);
	// the PNGs themselves render in background goroutines launched after the
	// writes. Off path: no list, no goroutines.
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
		sendWarning(reportWarnings, fmt.Sprintf("Report was not written for %s: %v", inputPath, err))
	}

	// Emit the run record beside the .md. The .json path is derived from
	// OutputPath exactly as the .md is, so they sit together. A write
	// failure is non-fatal, matching the report write above.
	recordPath := outputStem + ".json"
	if err := processor.WriteRunRecord(rec, recordPath); err != nil {
		wlog("[POOL] Failed to write run record: %v", err)
		sendWarning(reportWarnings, fmt.Sprintf("Run record was not written for %s: %v", inputPath, err))
	}

	// Stream the full interval and candidate series to .jsonl sidecars
	// beside the record: the bulk data the summary stands in for. Opt-in
	// behind --diagnostics; the always-on .json carries the inline summaries,
	// so .md/.json stay populated when off. Same non-fatal contract as the
	// record above.
	if diagnostics {
		if err := processor.WriteRunRecordSidecars(result.Measurements, recordPath); err != nil {
			wlog("[POOL] Failed to write run record sidecars: %v", err)
			sendWarning(reportWarnings, fmt.Sprintf("Run record sidecars were not written for %s: %v", inputPath, err))
		}
	}

	// Launch the spectrogram renders in background goroutines, OFF the
	// critical path: the .md/.json/sidecars are written and FileCompleteMsg
	// fires below without waiting for any PNG. Each render is bounded by the
	// pool-level render.sem (shared across files, sized to the jobs budget) and
	// tracked on render.wg, which the pool waits on before AllCompleteMsg so the
	// process does not exit until every PNG lands. Render failure is
	// non-fatal (reportWarnings); env.ctx cancellation aborts + cleans partials
	// inside generateSpectrogram. The list is empty when --diagnostics is off,
	// so the loop launches nothing.
	destDir := filepath.Dir(result.OutputPath)
	launchSpectrogramRenders(env.ctx, rec.Spectrograms, render.sem, render.wg,
		func(ctx context.Context, img processor.SpectrogramImage) error {
			return processor.RenderSpectrogramImage(ctx, img, rec, inputPath, result.OutputPath, destDir)
		},
		func(img processor.SpectrogramImage, err error) {
			wlog("[POOL] Failed to render spectrogram %s: %v", img.Path, err)
			sendWarning(reportWarnings, fmt.Sprintf("Spectrogram %s was not written for %s: %v", img.Path, inputPath, err))
		})

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
	env.p.Send(ui.AdaptedSummaryMsg{
		FileIndex: i,
		Summary:   ph.summary.WithLimiter(result.NormResult),
	})

	wlog("[POOL] Sending FileCompleteMsg for file %d", i)
	env.p.Send(ui.FileCompleteMsg{
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
}
