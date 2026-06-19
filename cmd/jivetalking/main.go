package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/alecthomas/kong"
	"github.com/charmbracelet/colorprofile"
	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/cli"
	"github.com/linuxmatters/jivetalking/internal/processor"
	"github.com/linuxmatters/jivetalking/internal/report"
	"github.com/linuxmatters/jivetalking/internal/ui"
	"github.com/mattn/go-runewidth"
)

// version is injected via ldflags at build time. Local dev builds keep "dev";
// release builds carry the git tag (e.g. "0.1.0").
var version = "dev"

const debugLogPath = "jivetalking-debug.log"

// createDebugLogFile is a package var over os.Create so tests can substitute a
// fake and exercise openDebugLog without touching the filesystem.
var createDebugLogFile = os.Create

// CLI defines the command-line interface parsed by kong.
type CLI struct {
	Version      bool     `short:"v" help:"Show version information"`
	Debug        bool     `short:"d" help:"Enable debug logging to jivetalking-debug.log"`
	AnalysisOnly bool     `short:"a" help:"Run analysis only (Pass 1), display results, skip processing"`
	Diagnostics  bool     `name:"diagnostics" help:"Write bulk diagnostic artefacts for sweeps and quality comparison: the .intervals.jsonl and .candidates.jsonl sidecars plus before/after spectrogram PNGs (whole-file and elected room-tone/speech regions). Adds extra FFmpeg passes. Off by default." default:"false"`
	Files        []string `arg:"" name:"files" help:"Audio files to process" type:"existingfile" optional:""`
}

// resolveJobs derives the worker count from the number of input files, capped
// at numCPU so we never spawn more workers than CPUs, floored at 1. numCPU is a
// parameter so the function is pure and table-testable.
func resolveJobs(numFiles, numCPU int) int {
	return max(1, min(numFiles, numCPU))
}

func main() {
	// Suppress FFmpeg info/verbose logging so astats and other filters do not
	// print summaries to stderr and clutter the console.
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogError)

	// Pin display-width measurement to non-East-Asian so the peak-marker
	// superscript '·' decimal separator (U+00B7, East-Asian ambiguous) measures
	// as one column under any locale. Without this it widens to two columns under
	// a CJK locale and shifts the peak-marker arrow off its column.
	runewidth.DefaultCondition.EastAsianWidth = false

	cliArgs := &CLI{}
	ctx := kong.Parse(
		cliArgs,
		kong.Name("jivetalking"),
		kong.Description("Professional podcast audio pre-processor"),
		kong.UsageOnError(),
		kong.Vars{
			"version": version,
		},
		kong.Help(cli.StyledHelpPrinter()),
	)

	if cliArgs.Version {
		cli.PrintVersion(version)
		os.Exit(0)
	}

	if len(cliArgs.Files) == 0 {
		cli.PrintError("No input files specified")
		_ = ctx.PrintUsage(false)
		os.Exit(1)
	}

	config := processor.DefaultFilterConfig()

	debugLog, err := openDebugLog(cliArgs.Debug)
	if err != nil {
		cli.PrintError(err.Error())
		os.Exit(1)
	}
	if debugLog != nil {
		defer debugLog.Close()
	}
	sink := newDebugSink(debugLog)
	log := func(format string, args ...any) {
		sink.Logf(format, args...)
	}

	// Route the filter chain's debug output through the same serialised sink.
	config.SetLogger(log)

	if cliArgs.AnalysisOnly {
		runAnalysisOnly(cliArgs.Files, config, log, resolveJobs(len(cliArgs.Files), runtime.NumCPU()), cliArgs.Diagnostics)
		return
	}

	model := ui.NewModel(cliArgs.Files)

	p := tea.NewProgram(model)
	reportWarnings := make(chan string, len(cliArgs.Files))

	runCtx, cancel := context.WithCancel(context.Background())

	jobs := resolveJobs(len(cliArgs.Files), runtime.NumCPU())

	env := poolEnv{
		ctx:       runCtx,
		p:         p,
		files:     cliArgs.Files,
		base:      config,
		sharedLog: log,
		jobs:      jobs,
	}
	poolDone := launchWorkerPool(env, cliArgs.Diagnostics, reportWarnings, defaultWorkerPoolDeps())

	finalModel, runErr := p.Run()

	// p.Run() blocks until tea.Quit, fired on q/ctrl+c and on AllCompleteMsg.
	// Fire cancel() unconditionally: a no-op on natural completion (workers
	// already finished), and on user quit it stops in-flight workers via
	// ctx.Done() so their deferred temp cleanup runs and wg.Wait() completes.
	cancel()

	// Wait for the pool to fully unwind before exiting on either path. After a
	// user quit p.Run() returns immediately while in-flight workers still need
	// to observe ctx.Done(), abort ProcessAudio, run their deferred temp
	// cleanup, and call wg.Done(); exiting first would leave temp dotfiles and
	// violate the no-residue-on-cancel guarantee. No deadlock: post-Run
	// tea.Program.Send is a non-blocking no-op, so the pool's FileComplete/
	// AllComplete sends do not block, and the acquire-time ctx.Done() select
	// lets not-yet-started workers exit at once.
	<-poolDone
	close(reportWarnings)

	if runErr != nil {
		cli.PrintError(fmt.Sprintf("UI error: %v", runErr))
		if debugLog != nil {
			debugLog.Close()
		}
		os.Exit(1) //nolint:gocritic // exitAfterDefer: debugLog explicitly closed above
	}

	// Persist the completion summary to the normal screen so it survives the
	// alt-screen restore on exit. Only on natural completion (Done == true); an
	// early user quit (q/ctrl+c) leaves Done == false and must skip the print to
	// avoid a misleading "complete" summary. A non-ui.Model also skips.
	if m, ok := finalModel.(ui.Model); ok && m.Done {
		fmt.Fprintln(colorprofile.NewWriter(os.Stdout, os.Environ()), ui.FinalSummary(m))
	}

	for warning := range reportWarnings {
		cli.PrintWarning(warning)
	}
}

func openDebugLog(enabled bool) (*os.File, error) {
	if !enabled {
		return nil, nil
	}

	logFile, err := createDebugLogFile(debugLogPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open debug log %s: %w", debugLogPath, err)
	}
	return logFile, nil
}

type analysisOnlyDeps struct {
	stdout              io.Writer
	hasTTY              func() bool
	openMetadata        func(string) (*audio.Metadata, error)
	analyse             func(context.Context, string, *processor.BaseFilterConfig, processor.ProgressCallback) (*processor.AnalysisResult, error)
	printError          func(string)
	writeMarkdownReport func(*processor.RunRecord, report.Timings, string) error
	writeRunRecord      func(*processor.RunRecord, string) error
	writeSidecars       func(*processor.AudioMeasurements, string) error
}

func defaultAnalysisOnlyDeps() analysisOnlyDeps {
	pool := defaultAnalysisPoolDeps()
	return analysisOnlyDeps{
		stdout:              os.Stdout,
		hasTTY:              isTTY,
		openMetadata:        pool.openMetadata,
		analyse:             pool.analyse,
		printError:          cli.PrintError,
		writeMarkdownReport: report.WriteMarkdownReport,
		writeRunRecord:      processor.WriteRunRecord,
		writeSidecars:       processor.WriteRunRecordSidecars,
	}
}

func openAudioMetadata(inputPath string) (*audio.Metadata, error) {
	reader, metadata, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		return nil, err
	}
	reader.Close()
	return metadata, nil
}

// timings assembles the complete report.Timings for a processed file: the four
// per-pass durations plus the real-time factor (audio duration / total
// wall-clock from fileStart to now). The factor is omitted (left zero) when the
// input duration is unknown.
func (ph *progressHandler) timings(pass2Time time.Duration, fileStart time.Time, result *processor.ProcessingResult) report.Timings {
	t := report.Timings{
		Pass1: ph.pass1Time,
		Pass2: pass2Time,
		Pass3: ph.pass3Time,
		Pass4: ph.pass4Time,
	}
	if result.InputMetadata.DurationSecs > 0 {
		totalTime := time.Since(fileStart)
		audioDuration := time.Duration(result.InputMetadata.DurationSecs * float64(time.Second))
		t.RealTimeFactor = float64(audioDuration) / float64(totalTime)
	}
	return t
}

// progressHandler relays processor progress updates to the TUI and records
// per-pass timings from the start/end progress boundaries.
type progressHandler struct {
	p          *tea.Program
	log        func(string, ...any)
	fileIndex  int
	pass1Start time.Time
	pass1Time  time.Duration
	pass3Start time.Time
	pass3Time  time.Duration
	pass4Start time.Time
	pass4Time  time.Duration

	// summary is the filter-chain status view-model, built from the Pass-2 start
	// update (chain + analysis rows). The pool reads it back at completion to merge
	// the limiter ceiling before the final AdaptedSummaryMsg.
	summary ui.AdaptedSummary
}

func (ph *progressHandler) callback(update processor.ProgressUpdate) {
	ph.log("[MAIN] Sending ProgressMsg: Pass %d (%s), Progress %.1f%%, Level %.1f dB", update.Pass, update.PassName, update.Progress*100, update.Level)

	// Progress 0.0 marks a pass start, 1.0 marks its end; bracket each pass to
	// measure its wall-clock duration.
	switch {
	case update.Pass == processor.PassAnalysis && update.Progress == 0.0:
		ph.pass1Start = time.Now()
	case update.Pass == processor.PassAnalysis && update.Progress == 1.0:
		ph.pass1Time = time.Since(ph.pass1Start)
	case update.Pass == processor.PassMeasuring && update.Progress == 0.0:
		ph.pass3Start = time.Now()
	case update.Pass == processor.PassMeasuring && update.Progress == 1.0:
		ph.pass3Time = time.Since(ph.pass3Start)
	case update.Pass == processor.PassNormalising && update.Progress == 0.0:
		ph.pass4Start = time.Now()
	case update.Pass == processor.PassNormalising && update.Progress == 1.0:
		ph.pass4Time = time.Since(ph.pass4Start)
	}

	ph.p.Send(ui.ProgressMsg{
		FileIndex:    ph.fileIndex,
		Pass:         update.Pass,
		PassName:     update.PassName,
		Progress:     update.Progress,
		Level:        update.Level,
		Duration:     update.Duration,
		Measurements: update.Measurements,
	})

	// At Pass-2 start the update carries the post-AdaptConfig config + diagnostics.
	// Build the filter-chain status summary (chain + analysis rows; limiter pending)
	// and send it as a one-off state-change message. Retain it on the handler so the
	// pool can merge the limiter ceiling at completion. Off path (Config nil): no
	// summary, no message.
	if update.Config != nil {
		ph.summary = ui.NewAdaptedSummary(update.Config, update.Diagnostics, update.Measurements)
		ph.p.Send(ui.AdaptedSummaryMsg{
			FileIndex: ph.fileIndex,
			Summary:   ph.summary,
		})
	}

	// At Pass-4 start the update carries the just-computed limiter ceiling. Merge it
	// into the retained summary and resend so the Limiter row lights to its ceiling
	// (or settles to OFF) WHILE the file is still processing, not only at completion.
	// Read-only surfacing: the ceiling is the same value the final NormResult reports.
	if update.Limiter != nil {
		ph.summary = ph.summary.WithLimiterProgress(update.Limiter)
		ph.p.Send(ui.AdaptedSummaryMsg{
			FileIndex: ph.fileIndex,
			Summary:   ph.summary,
		})
	}
}

// runAnalysisOnly performs Pass 1 analysis on each file under a bounded worker
// pool, then displays results to console in input order. Skips full 4-pass
// processing.
func runAnalysisOnly(files []string, config *processor.BaseFilterConfig, log func(string, ...any), jobs int, diagnostics bool) {
	runAnalysisOnlyWithDeps(files, config, log, jobs, diagnostics, defaultAnalysisOnlyDeps())
}

// runAnalysisOnlyWithDeps drives the analysis-only path with injected
// dependencies for testing. diagnostics gates the bulk diagnostic artefacts (the
// .jsonl sidecars and the input-only spectrogram PNGs). When false the always-on
// set (.md/.json) still writes; only the opt-in sidecars skip.
func runAnalysisOnlyWithDeps(files []string, config *processor.BaseFilterConfig, log func(string, ...any), jobs int, diagnostics bool, deps analysisOnlyDeps) {
	slots := make([]analysisSlot, len(files))

	poolDeps := analysisPoolDeps{
		analyse:      deps.analyse,
		openMetadata: deps.openMetadata,
	}

	runCtx, cancel := context.WithCancel(context.Background())

	// Spectrogram renders run in background goroutines off the post-pool report
	// loop, mirroring the processing pool. specSem bounds them to the jobs budget
	// shared across ALL files - one semaphore, never one unbounded goroutine per
	// PNG. specWG tracks every render so the function waits for all input-only
	// PNGs before it returns. specCtx is a FRESH context (not runCtx): runCtx is
	// already cancelled by the unconditional cancel() below by the time the report
	// loop runs, so renders launched there must not inherit that cancellation;
	// specCancel fires on return and specWG.Wait() ensures partials are cleaned.
	specSem := make(chan struct{}, jobs)
	var specWG sync.WaitGroup
	specCtx, specCancel := context.WithCancel(context.Background())
	defer func() {
		specWG.Wait()
		specCancel()
	}()

	tty := deps.hasTTY()

	if tty {
		model := ui.NewAnalysisModel(files)
		p := tea.NewProgram(model)

		env := poolEnv{ctx: runCtx, p: p, files: files, base: config, sharedLog: log, jobs: jobs}
		poolDone := make(chan struct{})
		go func() {
			runAnalysisPool(env, slots, poolDeps)
			close(poolDone)
		}()

		if _, err := p.Run(); err != nil {
			deps.printError(fmt.Sprintf("UI error: %v", err))
		}

		// Fire cancel() unconditionally: a no-op on natural completion (workers
		// already finished), and on user quit it stops in-flight workers via
		// ctx.Done() so wg.Wait() completes.
		cancel()

		// Join the pool goroutine before reading results/metas/errs. On user
		// quit p.Run() returns before the pool drains, so waiting here prevents
		// a data race on the shared result slices.
		<-poolDone
	} else {
		// No terminal: one up-front banner, then the pool runs synchronously.
		log("[ANALYSIS] No TTY available, running without progress UI")
		fmt.Fprintf(deps.stdout, "Analysing %d files…\n", len(files))

		env := poolEnv{ctx: runCtx, p: nil, files: files, base: config, sharedLog: log, jobs: jobs}
		runAnalysisPool(env, slots, poolDeps)

		cancel()
	}

	// Write each file's report to <source-name>-analysis.md in input order
	// after the pool completes. A worker cancelled at the acquire select leaves
	// both results[i] and errs[i] nil; a worker cancelled mid-analysis sets
	// errs[i] to a context.Canceled-wrapped error. Both cases are skipped: a
	// user who quit should get no error spew. Real errors still print so
	// per-file failures stay isolated.
	//
	// In no-TTY mode the post-loop prints the one-line confirmation to stdout;
	// in TTY mode the persisted analysis TUI already shows it, so we skip the
	// stdout print to avoid doubling up. The .md report is written in both modes.
	render := analysisRenderScheduler{ctx: specCtx, sem: specSem, wg: &specWG}
	noTTY := !tty
	for i := range files {
		if slots[i].err != nil {
			if errors.Is(slots[i].err, context.Canceled) {
				continue
			}
			deps.printError(fmt.Sprintf("Analysis failed for %s: %v", files[i], slots[i].err))
			continue
		}

		if slots[i].result == nil {
			continue // cancelled before analysis ran
		}

		emitAnalysisReport(files[i], slots[i].result, slots[i].meta, diagnostics, noTTY, deps, render)
	}
}

// analysisRenderScheduler bundles the background spectrogram-render state shared
// across the report loop: the fresh specCtx (not the cancelled run ctx), the
// jobs-sized semaphore bounding concurrent renders, and the WaitGroup the caller
// drains before returning so every input-only PNG lands.
type analysisRenderScheduler struct {
	ctx context.Context
	sem chan struct{}
	wg  *sync.WaitGroup
}

// emitAnalysisReport writes one file's analysis artefacts after a successful
// Pass-1 run: it builds the Pass-1-only run record, runs the shared
// artefact-emission spine (emitReportArtefacts: always-on .md/.json, opt-in
// .jsonl sidecars and input-only spectrogram PNGs under --diagnostics), and (in
// no-TTY mode, when the report landed) prints the one-line stdout confirmation.
// Every write failure is non-fatal and isolated so the remaining artefacts still
// emit, matching the processing path in pool.go.
func emitAnalysisReport(inputPath string, result *processor.AnalysisResult, meta *audio.Metadata, diagnostics, noTTY bool, deps analysisOnlyDeps, render analysisRenderScheduler) {
	// Emit the Pass-1-only run record beside the analysis report. The .json
	// path is derived from AnalysisReportPath by swapping the .md extension, so
	// both share the <stem>-<ext>-analysis basename. meta supplies provenance
	// (sample rate, channels) that the Pass-1 record cannot carry on its own.
	reportPath := report.AnalysisReportPath(inputPath)
	stem := strings.TrimSuffix(reportPath, filepath.Ext(reportPath))
	record := processor.NewAnalysisRunRecord(inputPath, result.Measurements)
	if meta != nil {
		record.Run.SampleRateHz = meta.SampleRate
		record.Run.Channels = meta.Channels
		if meta.Duration > 0 {
			record.Run.DurationS = meta.Duration
		}
	}

	// AnalysisSpectrogramStages is the single input stage (no output exists in
	// this mode, decision #5), so the derived list is whole-file + elected
	// regions, up to 3, all input-stage. The source is always the INPUT file;
	// outputPath is unused for the input stage, so "". Analysis and Adaptation
	// are the only timings available (no Pass 1-4); the Processing Summary
	// renders just those two non-zero rows.
	//
	// A Markdown write failure must NOT skip the confirmation logic's siblings:
	// emitReportArtefacts keeps emitting the independent .json and sidecars on a
	// report-write failure. Only the "source → report" confirmation is
	// suppressed below, so detect the report write here.
	reportWritten := true
	emitReportArtefacts(reportArtefacts{
		rec:         record,
		stem:        stem,
		stages:      processor.AnalysisSpectrogramStages,
		sidecarMeas: result.Measurements,
		timings: report.Timings{
			Analysis:   result.AnalysisDuration,
			Adaptation: result.AdaptationDuration,
		},
		diagnostics: diagnostics,
		renderCtx:   render.ctx,
		renderSem:   render.sem,
		renderWG:    render.wg,
		render: func(ctx context.Context, img processor.SpectrogramImage) error {
			return processor.RenderSpectrogramImage(ctx, img, record, inputPath, "", filepath.Dir(reportPath))
		},
		reportErr: deps.printError,
		errMsgs: reportErrorMessages{
			inputPath:   inputPath,
			report:      "Failed to write analysis report for %s: %v",
			record:      "Failed to write analysis run record for %s: %v",
			sidecars:    "Failed to write analysis run record sidecars for %s: %v",
			spectrogram: "Failed to render analysis spectrogram %s for %s: %v",
		},
		writeMarkdown: deps.writeMarkdownReport,
		writeRecord:   deps.writeRunRecord,
		writeSidecars: deps.writeSidecars,
		onReportFail:  func() { reportWritten = false },
	})

	if noTTY && reportWritten {
		printAnalysisConfirmation(deps.stdout, inputPath, reportPath, result.Measurements)
	}
}

// printAnalysisConfirmation writes the styled confirmation line
// "🗸 <source-basename> → <report-basename>" through a colour-aware writer so the
// green check downgrades cleanly on non-colour stdout, then the two light-touch
// verdict lines (Recording stars + label, one-lever Gain advice) computed from
// the Pass-1 INPUT measurements. The .md report stays verdict-free; these lines
// live only on the console, mirroring the analysis TUI. A nil measurements
// (defensive) drops the verdict lines but still prints the confirmation.
func printAnalysisConfirmation(w io.Writer, inputPath, reportPath string, m *processor.AudioMeasurements) {
	icon := lipgloss.NewStyle().Foreground(cli.ColorGreen).Render("🗸")
	cw := colorprofile.NewWriter(w, os.Environ())
	fmt.Fprintf(cw, "%s %s → %s\n", icon, filepath.Base(inputPath), filepath.Base(reportPath))

	if m == nil {
		return
	}
	starStyle := lipgloss.NewStyle().Foreground(cli.ColorOrange)
	labelStyle := lipgloss.NewStyle().Foreground(cli.ColorMuted)
	rec := processor.ComputeRecordingScore(m)
	advice := processor.GainAdvice(m.Loudness.InputTP)
	fmt.Fprintf(cw, "   %s  %s  %s\n",
		labelStyle.Render("Recording"), starStyle.Render(ui.QualityStars(rec.Stars)), rec.Label)
	fmt.Fprintf(cw, "   %s  %s  %s\n",
		labelStyle.Render("Gain     "), ui.GainBar(m.Loudness.InputTP), advice.Message())
}

// isTTY reports whether stdout is connected to a terminal.
func isTTY() bool {
	fileInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}
