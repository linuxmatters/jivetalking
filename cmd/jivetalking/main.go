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

var createDebugLogFile = os.Create

// CLI defines the command-line interface parsed by kong.
type CLI struct {
	Version              bool          `short:"v" help:"Show version information"`
	Debug                bool          `short:"d" help:"Enable debug logging to jivetalking-debug.log"`
	AnalysisOnly         bool          `short:"a" help:"Run analysis only (Pass 1), display results, skip processing"`
	Diagnostics          bool          `name:"diagnostics" help:"Write bulk diagnostic artefacts for sweeps and quality comparison: the .intervals.jsonl and .candidates.jsonl sidecars plus before/after spectrogram PNGs (whole-file and elected room-tone/speech regions). Adds extra FFmpeg passes. Off by default." default:"false"`
	RoomToneScanDuration time.Duration `name:"room-tone-scan-duration" help:"Cap room-tone-candidate scan to the first DURATION of input (e.g. 30s, 1m30s). Faster on long files at the cost of coverage; loudness, true peak, LRA, spectral, and speech analysis remain whole-file. Fewer room-tone candidates also reach voice-activated detection when capped. 0s means scan the whole file." placeholder:"DURATION" default:"0s"`
	SilenceScanDuration  time.Duration `name:"silence-scan-duration" help:"[deprecated alias for --room-tone-scan-duration] Cap room-tone-candidate scan to the first DURATION of input. Supplying both flags with different non-zero values is rejected. 0s means scan the whole file." placeholder:"DURATION" default:"0s"`
	Files                []string      `arg:"" name:"files" help:"Audio files to process" type:"existingfile" optional:""`
}

// resolveRoomToneScanDuration validates the room-tone scan duration flags and
// returns the effective duration to use. The new --room-tone-scan-duration
// flag is primary; --silence-scan-duration is a deprecated alias that emits a
// one-line notice to deprecationOut when used. Supplying both flags with
// different non-zero values is rejected. Negative values are rejected for
// either flag.
func resolveRoomToneScanDuration(roomTone, silence time.Duration, deprecationOut io.Writer) (time.Duration, error) {
	if roomTone < 0 {
		return 0, fmt.Errorf("--room-tone-scan-duration must be >= 0, got %s", roomTone)
	}
	if silence < 0 {
		return 0, fmt.Errorf("--silence-scan-duration must be >= 0, got %s", silence)
	}
	if roomTone != 0 && silence != 0 && roomTone != silence {
		return 0, fmt.Errorf("--room-tone-scan-duration (%s) and --silence-scan-duration (%s) conflict; supply only one", roomTone, silence)
	}
	if silence != 0 && deprecationOut != nil {
		fmt.Fprintln(deprecationOut, "warning: --silence-scan-duration is deprecated; use --room-tone-scan-duration")
	}
	if roomTone != 0 {
		return roomTone, nil
	}
	return silence, nil
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
		kong.Help(cli.StyledHelpPrinter(kong.HelpOptions{Compact: true})),
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

	scanDuration, err := resolveRoomToneScanDuration(cliArgs.RoomToneScanDuration, cliArgs.SilenceScanDuration, os.Stderr)
	if err != nil {
		cli.PrintError(err.Error())
		os.Exit(1)
	}

	config := processor.DefaultFilterConfig()
	config.Analysis.RoomToneScanDuration = scanDuration

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

	poolDone := launchWorkerPool(runCtx, p, cliArgs.Files, config, log, jobs, cliArgs.Diagnostics, reportWarnings)

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

	if err := runErr; err != nil {
		cli.PrintError(fmt.Sprintf("UI error: %v", err))
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

	for {
		select {
		case warning := <-reportWarnings:
			cli.PrintWarning(warning)
		default:
			return
		}
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

// buildProcessingTimings completes the report.Timings for a processed file by
// pairing the per-pass durations with the real-time factor (audio duration /
// total wall-clock from fileStartTime to now). Mirrors the legacy report's RTF
// maths: total time spans the whole file's processing, the factor is omitted
// (left zero) when the input duration is unknown.
func buildProcessingTimings(fileStartTime time.Time, timings report.Timings, result *processor.ProcessingResult) report.Timings {
	if result.InputMetadata.DurationSecs > 0 {
		totalTime := time.Since(fileStartTime)
		audioDuration := time.Duration(result.InputMetadata.DurationSecs * float64(time.Second))
		timings.RealTimeFactor = float64(audioDuration) / float64(totalTime)
	}
	return timings
}

type analysisOnlyDeps struct {
	stdout              io.Writer
	hasTTY              func() bool
	openMetadata        func(string) (*audio.Metadata, error)
	analyzeDetailed     func(context.Context, string, *processor.BaseFilterConfig, processor.ProgressCallback) (*processor.AnalysisResult, error)
	printError          func(string)
	writeMarkdownReport func(*processor.RunRecord, report.Timings, string) error
	writeRunRecord      func(*processor.RunRecord, string) error
	writeSidecars       func(*processor.AudioMeasurements, string) error
}

func defaultAnalysisOnlyDeps() analysisOnlyDeps {
	return analysisOnlyDeps{
		stdout:              os.Stdout,
		hasTTY:              isTTY,
		openMetadata:        openAudioMetadata,
		analyzeDetailed:     processor.AnalyzeOnlyDetailed,
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

func (ph *progressHandler) timings(pass2Time time.Duration) report.Timings {
	return report.Timings{
		Pass1: ph.pass1Time,
		Pass2: pass2Time,
		Pass3: ph.pass3Time,
		Pass4: ph.pass4Time,
	}
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
}

// runAnalysisOnly performs Pass 1 analysis on each file under a bounded worker
// pool, then displays results to console in input order. Skips full 4-pass
// processing.
func runAnalysisOnly(files []string, config *processor.BaseFilterConfig, log func(string, ...any), jobs int, diagnostics bool) {
	runAnalysisOnlyWithDeps(files, config, log, jobs, diagnostics, defaultAnalysisOnlyDeps())
}

// runAnalysisOnlyWithDeps drives the analysis-only path with injected
// dependencies for testing. diagnostics gates the bulk diagnostic artefacts (the
// .jsonl sidecars and, from T4.2, the input-only spectrogram PNGs). When false
// the always-on set (.md/.json) still writes; only the opt-in sidecars skip.
func runAnalysisOnlyWithDeps(files []string, config *processor.BaseFilterConfig, log func(string, ...any), jobs int, diagnostics bool, deps analysisOnlyDeps) {
	results := make([]*processor.AnalysisResult, len(files))
	metas := make([]*audio.Metadata, len(files))
	errs := make([]error, len(files))

	runCtx, cancel := context.WithCancel(context.Background())

	// Spectrogram renders run in background goroutines off the post-pool report
	// loop, mirroring the processing pool (T4.1). specSem bounds them to the jobs
	// budget shared across ALL files - one semaphore, never one unbounded goroutine
	// per PNG. specWG tracks every render so the function waits for all input-only
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

	if deps.hasTTY() {
		model := ui.NewAnalysisModel(files)
		p := tea.NewProgram(model)

		poolDone := make(chan struct{})
		go func() {
			runAnalysisPool(runCtx, p, files, config, log, jobs, results, metas, errs, deps.openMetadata)
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

		runAnalysisPool(runCtx, nil, files, config, log, jobs, results, metas, errs, deps.openMetadata)

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
	noTTY := !deps.hasTTY()
	for i := range files {
		if errs[i] != nil {
			if errors.Is(errs[i], context.Canceled) {
				continue
			}
			deps.printError(fmt.Sprintf("Analysis failed for %s: %v", files[i], errs[i]))
			continue
		}

		if results[i] == nil {
			continue // cancelled before analysis ran
		}

		// Emit the Pass-1-only run record beside the analysis report. The .json
		// path is derived from AnalysisReportPath by swapping the .md extension, so
		// both share the <stem>-<ext>-analysis basename. metas[i] supplies
		// provenance (sample rate, channels) that the Pass-1 record cannot carry on
		// its own.
		reportPath := report.AnalysisReportPath(files[i])
		recordPath := strings.TrimSuffix(reportPath, filepath.Ext(reportPath)) + ".json"
		record := processor.NewAnalysisRunRecord(files[i], results[i].Measurements)
		if metas[i] != nil {
			record.Run.SampleRateHz = metas[i].SampleRate
			record.Run.Channels = metas[i].Channels
			if metas[i].Duration > 0 {
				record.Run.DurationS = metas[i].Duration
			}
		}

		// Attach the input-only spectrogram path list SYNCHRONOUSLY, before the
		// .md/.json write, so both carry the links. AnalysisSpectrogramStages is the
		// single input stage (no output exists in this mode, decision #5), so the list
		// is whole-file + elected regions, up to 3, all input-stage. Pure string work
		// (no ffmpeg); the PNGs render in background goroutines launched after the
		// writes. Off path: no list, no goroutines.
		stem := strings.TrimSuffix(reportPath, filepath.Ext(reportPath))
		if diagnostics {
			record.Spectrograms = processor.DeriveSpectrogramImages(record, stem, processor.AnalysisSpectrogramStages)
		}

		// Render the Markdown report from the same Pass-1-only record. Analysis and
		// Adaptation are the only timings available (no Pass 1-4); the Processing
		// Summary renders just those two non-zero rows. A write failure is
		// non-fatal, matching the run record and sidecars below.
		timings := report.Timings{
			Analysis:   results[i].AnalysisDuration,
			Adaptation: results[i].AdaptationDuration,
		}
		if err := deps.writeMarkdownReport(record, timings, reportPath); err != nil {
			deps.printError(fmt.Sprintf("Failed to write analysis report for %s: %v", files[i], err))
			continue
		}

		if err := deps.writeRunRecord(record, recordPath); err != nil {
			deps.printError(fmt.Sprintf("Failed to write analysis run record for %s: %v", files[i], err))
		}

		// Stream the Pass-1 interval and candidate series to .jsonl sidecars
		// beside the analysis record (§8.5 call 2 / §9.3). Opt-in behind
		// --diagnostics; the always-on .json carries the inline summaries.
		// Non-fatal, matching the record write above.
		if diagnostics {
			if err := deps.writeSidecars(results[i].Measurements, recordPath); err != nil {
				deps.printError(fmt.Sprintf("Failed to write analysis run record sidecars for %s: %v", files[i], err))
			}
		}

		// Launch the input-only spectrogram renders in background goroutines, OFF the
		// report loop: the .md/.json/sidecars are written and the loop moves on without
		// waiting for any PNG. Each render is bounded by specSem (shared across files,
		// sized to the jobs budget) and tracked on specWG, which the deferred wait drains
		// before the function returns so every input-only PNG lands. The source is always
		// the INPUT file (stage input); outputPath is unused for the input stage, so "".
		// Render failure is non-fatal (deps.printError, matching this path's other write
		// failures); specCtx cancellation aborts + cleans partials inside
		// generateSpectrogram. The list is empty when --diagnostics is off, so the loop
		// launches nothing.
		destDir := filepath.Dir(reportPath)
		for _, img := range record.Spectrograms {
			specWG.Add(1)
			go func(img processor.SpectrogramImage, inputPath string) {
				// Register specWG.Done() before the acquire select so a render skipped
				// on a cancelled specCtx still decrements specWG; otherwise the deferred
				// specWG.Wait() hangs.
				defer specWG.Done()

				select {
				case specSem <- struct{}{}:
					defer func() { <-specSem }()
				case <-specCtx.Done():
					return
				}

				if err := processor.RenderSpectrogramImage(specCtx, img, record, inputPath, "", destDir); err != nil {
					deps.printError(fmt.Sprintf("Failed to render analysis spectrogram %s for %s: %v", img.Path, inputPath, err))
				}
			}(img, files[i])
		}

		if noTTY {
			printAnalysisConfirmation(deps.stdout, files[i], reportPath)
		}
	}
}

// printAnalysisConfirmation writes a single styled confirmation line
// "🗸 <source-basename> → <report-basename>" through a colour-aware writer so the
// green check downgrades cleanly on non-colour stdout.
func printAnalysisConfirmation(w io.Writer, inputPath, reportPath string) {
	icon := lipgloss.NewStyle().Foreground(cli.ColorGreen).Render("🗸")
	cw := colorprofile.NewWriter(w, os.Environ())
	fmt.Fprintf(cw, "%s %s → %s\n", icon, filepath.Base(inputPath), filepath.Base(reportPath))
}

// isTTY reports whether stdout is connected to a terminal.
func isTTY() bool {
	fileInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}
