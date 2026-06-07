package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/kong"
	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/cli"
	"github.com/linuxmatters/jivetalking/internal/logging"
	"github.com/linuxmatters/jivetalking/internal/processor"
	"github.com/linuxmatters/jivetalking/internal/ui"
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
	RoomToneScanDuration time.Duration `name:"room-tone-scan-duration" help:"Cap room-tone-candidate scan to the first DURATION of input (e.g. 30s, 1m30s). Faster on long files at the cost of coverage; loudness, true peak, LRA, spectral, and speech analysis remain whole-file. Fewer room-tone candidates also reach voice-activated detection when capped. 0s means scan the whole file." placeholder:"DURATION" default:"0s"`
	SilenceScanDuration  time.Duration `name:"silence-scan-duration" help:"[deprecated alias for --room-tone-scan-duration] Cap room-tone-candidate scan to the first DURATION of input. Supplying both flags with different non-zero values is rejected. 0s means scan the whole file." placeholder:"DURATION" default:"0s"`
	Jobs                 int           `name:"jobs" help:"Number of files to process concurrently. 0 means auto (min(4, NumCPU)); an explicit value is honoured with no upper cap." default:"0"`
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

// resolveJobs resolves the effective worker count from the --jobs flag value
// and the available CPU count. The flag default of 0 (unset) selects auto mode:
// min(4, numCPU). Any other value is explicit, clamped to a floor of 1 and
// honoured with no upper cap, so --jobs above NumCPU is respected. numCPU is a
// parameter so the function is pure and table-testable.
func resolveJobs(jobs, numCPU int) int {
	if jobs == 0 {
		return min(4, numCPU)
	}
	return max(1, jobs)
}

func main() {
	// Suppress FFmpeg info/verbose logging so astats and other filters do not
	// print summaries to stderr and clutter the console.
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogError)

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
		runAnalysisOnly(cliArgs.Files, config, log, resolveJobs(cliArgs.Jobs, runtime.NumCPU()))
		return
	}

	model := ui.NewModel(cliArgs.Files)

	p := tea.NewProgram(model)
	reportWarnings := make(chan string, len(cliArgs.Files))

	runCtx, cancel := context.WithCancel(context.Background())

	jobs := resolveJobs(cliArgs.Jobs, runtime.NumCPU())

	poolDone := launchWorkerPool(runCtx, p, cliArgs.Files, config, log, jobs, reportWarnings)

	_, runErr := p.Run()

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

func buildProcessingReportData(inputPath string, fileStartTime time.Time, timings logging.ProcessingTimings, result *processor.ProcessingResult) logging.ReportData {
	return logging.ReportData{
		InputPath:    inputPath,
		OutputPath:   result.OutputPath,
		StartTime:    fileStartTime,
		EndTime:      time.Now(),
		Timings:      timings,
		Result:       result,
		SampleRate:   result.InputMetadata.SampleRate,
		Channels:     result.InputMetadata.Channels,
		DurationSecs: result.InputMetadata.DurationSecs,
	}
}

type analysisOnlyDeps struct {
	stdout          io.Writer
	hasTTY          func() bool
	openMetadata    func(string) (*audio.Metadata, error)
	analyzeDetailed func(context.Context, string, *processor.BaseFilterConfig, processor.ProgressCallback) (*processor.AnalysisResult, error)
	displayResults  func(io.Writer, string, *audio.Metadata, *processor.AudioMeasurements, *processor.EffectiveFilterConfig, *processor.AdaptiveDiagnostics, ...logging.AnalysisTimings)
	printError      func(string)
}

func defaultAnalysisOnlyDeps() analysisOnlyDeps {
	return analysisOnlyDeps{
		stdout:          os.Stdout,
		hasTTY:          isTTY,
		openMetadata:    openAudioMetadata,
		analyzeDetailed: processor.AnalyzeOnlyDetailed,
		displayResults:  logging.DisplayAnalysisResultsWithDiagnostics,
		printError:      cli.PrintError,
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

func (ph *progressHandler) timings(pass2Time time.Duration) logging.ProcessingTimings {
	return logging.ProcessingTimings{
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
func runAnalysisOnly(files []string, config *processor.BaseFilterConfig, log func(string, ...any), jobs int) {
	runAnalysisOnlyWithDeps(files, config, log, jobs, defaultAnalysisOnlyDeps())
}

func runAnalysisOnlyWithDeps(files []string, config *processor.BaseFilterConfig, log func(string, ...any), jobs int, deps analysisOnlyDeps) {
	results := make([]*processor.AnalysisResult, len(files))
	metas := make([]*audio.Metadata, len(files))
	errs := make([]error, len(files))

	runCtx, cancel := context.WithCancel(context.Background())

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

	// Print reports in input order after the pool completes. A worker cancelled
	// at the acquire select leaves both results[i] and errs[i] nil; a worker
	// cancelled mid-analysis sets errs[i] to a context.Canceled-wrapped error.
	// Both cases are skipped: a user who quit should get no error spew. Real
	// errors still print so per-file failures stay isolated. The printed flag
	// drives the inter-report blank line so skipped files cannot emit a stray
	// leading blank; with no skips it matches the previous i > 0 logic exactly.
	printed := false
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

		// Blank line separates consecutive printed file reports.
		if printed {
			fmt.Fprintln(deps.stdout)
		}
		printed = true

		timings := logging.AnalysisTimings{
			Analysis:   results[i].AnalysisDuration,
			Adaptation: results[i].AdaptationDuration,
		}
		deps.displayResults(deps.stdout, files[i], metas[i], results[i].Measurements, results[i].Config, results[i].Diagnostics, timings)
	}
}

// isTTY reports whether stdout is connected to a terminal.
func isTTY() bool {
	fileInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}
