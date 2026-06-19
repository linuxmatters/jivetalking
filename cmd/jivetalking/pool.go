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
		specWG.Go(func() {
			select {
			case specSem <- struct{}{}:
				defer func() { <-specSem }()
			case <-ctx.Done():
				return
			}

			if err := render(ctx, img); err != nil {
				onError(img, err)
			}
		})
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

// runBoundedPool is the shared bounded-worker-pool skeleton both pools run. A
// buffered semaphore of size env.jobs caps in-flight workers; a sync.WaitGroup
// tracks completion. For each file it spawns a worker that acquires the
// semaphore or bails on env.ctx.Done() (a not-yet-started worker skips its work
// cleanly; the slot is released only on the branch that took one), derives a
// per-file prefixed logger, and runs the caller's body with the file index,
// input path, and that logger. wg.Done() always fires - including the
// cancellation bail - so wg.Wait() returns even when ctx is cancelled.
//
// After wg.Wait() it runs the optional afterWait hook (the processing pool
// drains its spectrogram-render WaitGroup here so the process does not exit
// until every PNG lands), then sends ui.AllCompleteMsg gated on env.p != nil:
// the processing pool always has a non-nil program, while the no-TTY analysis
// path passes nil and must not call p.Send. Each body owns every other p.Send
// and self-gates on env.p, so a nil program never deadlocks a worker.
func runBoundedPool(env poolEnv, afterWait func(), body func(i int, inputPath string, wlog func(string, ...any))) {
	sem := make(chan struct{}, env.jobs)
	var wg sync.WaitGroup

	for i, inputPath := range env.files {
		wg.Go(func() {
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
			body(i, inputPath, wlog)
		})
	}

	wg.Wait()

	if afterWait != nil {
		afterWait()
	}

	if env.p != nil {
		env.sharedLog("[POOL] Sending AllCompleteMsg")
		env.p.Send(ui.AllCompleteMsg{})
	}
}

// runWorkerPool processes files concurrently under a bounded worker pool sharing
// one tea.Program. It supplies its per-file body to the shared runBoundedPool
// skeleton (which owns the semaphore, the WaitGroup, the ctx.Done() acquire-or-
// bail, and the final ui.AllCompleteMsg send). Each worker owns its file index,
// a per-file prefixed logger, and a per-worker config clone, mirroring the
// serial loop's per-file body. With jobs == 1 the observable outcome matches the
// serial path.
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
	// Spectrogram renders run in background goroutines off the file-worker critical
	// path. specSem bounds them to the jobs budget shared across ALL files - one
	// pool-level semaphore, never one unbounded goroutine per PNG, so ffmpeg is not
	// oversubscribed beyond the worker budget. specWG tracks every render so the
	// pool waits for all PNGs before AllCompleteMsg / program exit.
	specSem := make(chan struct{}, env.jobs)
	var specWG sync.WaitGroup
	render := processingRenderScheduler{sem: specSem, wg: &specWG}

	runBoundedPool(env,
		// Gate program exit on the spectrogram renders: every file's per-file
		// FileCompleteMsg has already fired (wg drained), so the file-worker TUI
		// is not held up; only AllCompleteMsg (which quits the program) waits
		// here, so the process does not exit until every PNG lands. On a user ctx
		// cancel the in-flight renders abort and clean their partials, then specWG
		// drains. With --diagnostics off nothing was launched, so this returns at
		// once.
		specWG.Wait,
		func(i int, inputPath string, wlog func(string, ...any)) {
			fileStartTime := time.Now()

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
					FileIndex:        i,
					CompletionResult: ui.CompletionResult{Error: err},
				})
				return
			}

			// ProcessAudio runs all four passes; isolate Pass 2 by subtracting the
			// passes the progress handler timed directly.
			pass2Time := time.Since(pass2Start) - ph.pass1Time - ph.pass3Time - ph.pass4Time

			emitProcessingReport(env, inputPath, result, ph, processingTimings{fileStart: fileStartTime, pass2: pass2Time}, diagnostics, reportWarnings, render)
		})
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

// reportArtefacts carries the per-mode parts of the artefact-emission spine that
// emitReportArtefacts runs identically for both pools: the already-built run
// record, the path stem the .md/.json derive from, the spectrogram stages
// constant, the sidecar measurements, the report.Timings, the render context +
// pool-level render scheduler, the render closure (varies: output path vs ""),
// and a single error-reporting callback (the reportWarnings send on the
// processing path, deps.printError on the analysis-only path). errMsgs supplies
// the four per-artefact warning templates so each mode keeps its own wording.
type reportArtefacts struct {
	rec         *processor.RunRecord
	stem        string
	stages      []string
	sidecarMeas *processor.AudioMeasurements
	timings     report.Timings
	diagnostics bool

	renderCtx context.Context
	renderSem chan struct{}
	renderWG  *sync.WaitGroup
	render    func(context.Context, processor.SpectrogramImage) error

	// Write seams. The processing path passes the concrete report/processor
	// functions; the analysis-only path passes its injected deps so tests can
	// substitute fakes. onReportFail (optional) fires when the .md write fails,
	// letting the analysis path suppress its "source → report" confirmation.
	writeMarkdown func(*processor.RunRecord, report.Timings, string) error
	writeRecord   func(*processor.RunRecord, string) error
	writeSidecars func(*processor.AudioMeasurements, string) error
	onReportFail  func()

	reportErr func(string)
	errMsgs   reportErrorMessages
}

// reportErrorMessages holds the four artefact-write warning templates. report,
// record, and sidecars take (inputPath, err); spectrogram takes (img.Path,
// inputPath, err). Each mode supplies its own wording so emitReportArtefacts can
// format identical messages to the pre-extraction code.
type reportErrorMessages struct {
	inputPath   string
	report      string
	record      string
	sidecars    string
	spectrogram string
}

// emitReportArtefacts runs the shared artefact-emission spine for both pools:
// derive the spectrogram path list under diagnostics (pure string work, before
// the writes so the .md/.json carry resolving links), write the always-on .md
// and .json, conditionally write the opt-in .jsonl sidecars, then launch the
// bounded background spectrogram renders. The derive-then-write-then-launch
// order is load-bearing and kept intact. Every write failure is non-fatal and
// isolated through a.reportErr so the remaining artefacts still emit.
func emitReportArtefacts(a reportArtefacts) {
	// Attach the spectrogram path list SYNCHRONOUSLY, before the .md/.json
	// write, so both carry the links. This is pure string work (no ffmpeg);
	// the PNGs themselves render in background goroutines launched after the
	// writes. Off path: no list, no goroutines.
	if a.diagnostics {
		a.rec.Spectrograms = processor.DeriveSpectrogramImages(a.rec, a.stem, a.stages)
	}

	// Write the Markdown report beside the audio/source. A write failure is
	// non-fatal: the .json (and, on the processing path, the audio) is the
	// product, the report a side artefact.
	mdPath := a.stem + ".md"
	if err := a.writeMarkdown(a.rec, a.timings, mdPath); err != nil {
		a.reportErr(fmt.Sprintf(a.errMsgs.report, a.errMsgs.inputPath, err))
		if a.onReportFail != nil {
			a.onReportFail()
		}
	}

	// Emit the run record beside the .md. The .json path shares the stem, so
	// they sit together. A write failure is non-fatal, matching the report
	// write above.
	recordPath := a.stem + ".json"
	if err := a.writeRecord(a.rec, recordPath); err != nil {
		a.reportErr(fmt.Sprintf(a.errMsgs.record, a.errMsgs.inputPath, err))
	}

	// Stream the full interval and candidate series to .jsonl sidecars beside
	// the record: the bulk data the summary stands in for. Opt-in behind
	// --diagnostics; the always-on .json carries the inline summaries, so
	// .md/.json stay populated when off. Same non-fatal contract as the record
	// above.
	if a.diagnostics {
		if err := a.writeSidecars(a.sidecarMeas, recordPath); err != nil {
			a.reportErr(fmt.Sprintf(a.errMsgs.sidecars, a.errMsgs.inputPath, err))
		}
	}

	// Launch the spectrogram renders in background goroutines, OFF the critical
	// path: the .md/.json/sidecars are written and the caller proceeds without
	// waiting for any PNG. Each render is bounded by the pool-level semaphore
	// (shared across files, sized to the jobs budget) and tracked on the
	// WaitGroup the caller drains before exit so every PNG lands. Render failure
	// is non-fatal; renderCtx cancellation aborts + cleans partials inside
	// generateSpectrogram. The list is empty when --diagnostics is off, so the
	// loop launches nothing.
	launchSpectrogramRenders(a.renderCtx, a.rec.Spectrograms, a.renderSem, a.renderWG,
		a.render,
		func(img processor.SpectrogramImage, err error) {
			a.reportErr(fmt.Sprintf(a.errMsgs.spectrogram, img.Path, a.errMsgs.inputPath, err))
		})
}

// processingTimings is the timing data clump emitProcessingReport needs from the
// pool worker: fileStart marks when the worker began (feeds both the report's
// real-time factor and the FileCompleteMsg ProcessingTime), pass2 is the isolated
// Pass-2 wall-clock the worker computes by subtracting the progress-handler-timed
// passes. Bundling the pair keeps the emitProcessingReport signature short.
type processingTimings struct {
	fileStart time.Time
	pass2     time.Duration
}

// emitProcessingReport writes one file's processing artefacts after a successful
// 4-pass run and dispatches the final TUI messages: it builds the run record,
// runs the shared artefact-emission spine (emitReportArtefacts: always-on
// .md/.json, opt-in .jsonl sidecars and before/after spectrogram PNGs under
// --diagnostics), then sends the limiter-confirming AdaptedSummaryMsg and the
// FileCompleteMsg. Every write failure is non-fatal and isolated (reportWarnings)
// so the remaining artefacts still emit, mirroring emitAnalysisReport on the
// analysis-only path. ph supplies the per-pass timings and the retained
// filter-chain summary captured during ProcessAudio.
func emitProcessingReport(env poolEnv, inputPath string, result *processor.ProcessingResult, ph *progressHandler, t processingTimings, diagnostics bool, reportWarnings chan<- string, render processingRenderScheduler) {
	wlog := ph.log
	i := ph.fileIndex

	// Build the run record once and reuse it for both the Markdown report
	// and the .json: NewRunRecord(result) reads the same in-memory result,
	// so building it twice would be wasted work.
	rec := processor.NewRunRecord(result)

	outputStem := strings.TrimSuffix(result.OutputPath, filepath.Ext(result.OutputPath))
	destDir := filepath.Dir(result.OutputPath)

	emitReportArtefacts(reportArtefacts{
		rec:         rec,
		stem:        outputStem,
		stages:      processor.ProcessingSpectrogramStages,
		sidecarMeas: result.Measurements,
		timings:     ph.timings(t.pass2, t.fileStart, result),
		diagnostics: diagnostics,
		renderCtx:   env.ctx,
		renderSem:   render.sem,
		renderWG:    render.wg,
		render: func(ctx context.Context, img processor.SpectrogramImage) error {
			return processor.RenderSpectrogramImage(ctx, img, rec, inputPath, result.OutputPath, destDir)
		},
		writeMarkdown: report.WriteMarkdownReport,
		writeRecord:   processor.WriteRunRecord,
		writeSidecars: processor.WriteRunRecordSidecars,
		reportErr: func(msg string) {
			wlog("[POOL] %s", msg)
			sendWarning(reportWarnings, msg)
		},
		errMsgs: reportErrorMessages{
			inputPath:   inputPath,
			report:      "Report was not written for %s: %v",
			record:      "Run record was not written for %s: %v",
			sidecars:    "Run record sidecars were not written for %s: %v",
			spectrogram: "Spectrogram %s was not written for %s: %v",
		},
	})

	// Before->after room-tone floor pair for the done box, both on the astats RMS
	// dBFS axis. OutputNoiseFloor is the genuine Pass 4 output sample (no input
	// fallback) so an absent end shows the input figure alone, never input->input.
	finalNoiseFloor, haveFinalNoiseFloor := processor.OutputNoiseFloor(result)
	inputNoiseFloor, haveInputNoiseFloor := processor.InputNoiseFloor(result)

	// Surface the final-output true peak and loudness range from NormResult for
	// the done-box before→after rows. Read-only: both are measured by ebur128 on
	// the final output during normalisation, never recomputed here. The accessors
	// guard a nil NormResult/FinalMeasurements and return 0 when absent (the UI
	// gates the row), mirroring OutputNoiseFloor/InputNoiseFloor above.
	outputTP, _ := processor.OutputTP(result)
	outputLRA, _ := processor.OutputLRA(result)

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
		FileIndex: i,
		CompletionResult: ui.CompletionResult{
			InputLUFS:           result.InputLUFS,
			OutputLUFS:          result.OutputLUFS,
			FinalNoiseFloor:     finalNoiseFloor,
			InputNoiseFloor:     inputNoiseFloor,
			HaveFinalNoiseFloor: haveFinalNoiseFloor,
			HaveInputNoiseFloor: haveInputNoiseFloor,
			OutputTP:            outputTP,
			OutputLRA:           outputLRA,
			OutputPath:          result.OutputPath,
			Quality:             processor.ComputeQualityScore(result),
			RecordingQuality:    processor.ComputeRecordingScore(result.Measurements),
			ProcessingTime:      time.Since(t.fileStart),
		},
	})
}
