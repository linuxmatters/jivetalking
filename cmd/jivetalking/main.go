package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/alecthomas/kong"
	tea "github.com/charmbracelet/bubbletea"
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

var errCancelledByUser = errors.New("cancelled by user")

const debugLogPath = "jivetalking-debug.log"

var createDebugLogFile = os.Create

// CLI defines the command-line interface parsed by kong.
type CLI struct {
	Version              bool          `short:"v" help:"Show version information"`
	Debug                bool          `short:"d" help:"Enable debug logging to jivetalking-debug.log"`
	AnalysisOnly         bool          `short:"a" help:"Run analysis only (Pass 1), display results, skip processing"`
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

func main() {
	// Suppress FFmpeg info/verbose logging so astats and other filters do not
	// print summaries to stderr and clutter the console.
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogError)

	cliArgs := &CLI{}
	ctx := kong.Parse(cliArgs,
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
		runAnalysisOnly(cliArgs.Files, config, log)
		return
	}

	model := ui.NewModel(cliArgs.Files)

	p := tea.NewProgram(model, tea.WithAltScreen())
	reportWarnings := make(chan string, len(cliArgs.Files))

	go func() {
		for i, inputPath := range cliArgs.Files {
			fileStartTime := time.Now()

			log("[MAIN] Sending FileStartMsg for file %d: %s", i, inputPath)
			p.Send(ui.FileStartMsg{
				FileIndex: i,
				FileName:  inputPath,
			})

			ph := &progressHandler{
				p:         p,
				log:       log,
				fileIndex: i,
			}

			pass2Start := time.Now()
			log("[MAIN] Starting ProcessAudio for %s", inputPath)
			result, err := processor.ProcessAudio(inputPath, config, ph.callback)
			if err != nil {
				log("[MAIN] ProcessAudio failed: %v", err)
				p.Send(ui.FileCompleteMsg{
					FileIndex: i,
					Error:     err,
				})
				continue
			}
			// ProcessAudio runs all four passes; isolate Pass 2 by subtracting the
			// passes the progress handler timed directly.
			pass2Time := time.Since(pass2Start) - ph.pass1Time - ph.pass3Time - ph.pass4Time

			reportData := buildProcessingReportData(inputPath, fileStartTime, ph.timings(pass2Time), result)
			if err := logging.GenerateReport(reportData); err != nil {
				log("[MAIN] Failed to generate log file: %v", err)
				reportWarnings <- fmt.Sprintf("Report was not written for %s: %v", inputPath, err)
			}

			log("[MAIN] Sending FileCompleteMsg for file %d", i)
			p.Send(ui.FileCompleteMsg{
				FileIndex:  i,
				InputLUFS:  result.InputLUFS,
				OutputLUFS: result.OutputLUFS,
				NoiseFloor: result.NoiseFloor,
				OutputPath: result.OutputPath,
			})
		}

		log("[MAIN] Sending AllCompleteMsg")
		p.Send(ui.AllCompleteMsg{})
	}()

	if _, err := p.Run(); err != nil {
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
	runWithTUI      func(string, *processor.BaseFilterConfig, func(string, ...any)) (*processor.AnalysisResult, error)
	analyzeDetailed func(string, *processor.BaseFilterConfig, processor.ProgressCallback) (*processor.AnalysisResult, error)
	displayResults  func(io.Writer, string, *audio.Metadata, *processor.AudioMeasurements, *processor.EffectiveFilterConfig, *processor.AdaptiveDiagnostics, ...logging.AnalysisTimings)
	printError      func(string)
}

func defaultAnalysisOnlyDeps() analysisOnlyDeps {
	return analysisOnlyDeps{
		stdout:          os.Stdout,
		hasTTY:          isTTY,
		openMetadata:    openAudioMetadata,
		runWithTUI:      runAnalysisWithTUI,
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
		Measurements: update.Measurements,
	})
}

// runAnalysisOnly performs Pass 1 analysis on each file with a progress UI,
// then displays results to console. Skips full 4-pass processing.
func runAnalysisOnly(files []string, config *processor.BaseFilterConfig, log func(string, ...any)) {
	runAnalysisOnlyWithDeps(files, config, log, defaultAnalysisOnlyDeps())
}

func runAnalysisOnlyWithDeps(files []string, config *processor.BaseFilterConfig, log func(string, ...any), deps analysisOnlyDeps) {
	hasTTY := deps.hasTTY()

	for i, inputPath := range files {
		// Blank line separates consecutive file reports.
		if i > 0 {
			fmt.Fprintln(deps.stdout)
		}

		log("[ANALYSIS] Starting analysis for %s", inputPath)

		// Metadata supplies the duration and sample rate shown in the report.
		metadata, err := deps.openMetadata(inputPath)
		if err != nil {
			deps.printError(fmt.Sprintf("Failed to open %s: %v", inputPath, err))
			continue
		}

		var analysisResult *processor.AnalysisResult
		var analysisErr error

		if hasTTY {
			analysisResult, analysisErr = deps.runWithTUI(inputPath, config, log)
		} else {
			// No terminal: skip the progress UI for non-interactive environments.
			log("[ANALYSIS] No TTY available, running without progress UI")
			fmt.Fprintf(deps.stdout, "Analysing: %s\n", filepath.Base(inputPath))
			analysisResult, analysisErr = deps.analyzeDetailed(inputPath, config, nil)
		}

		if analysisErr != nil {
			if errors.Is(analysisErr, errCancelledByUser) {
				// User pressed Ctrl+C - exit immediately, don't process remaining files
				return
			}
			deps.printError(fmt.Sprintf("Analysis failed for %s: %v", inputPath, analysisErr))
			continue
		}

		log("[ANALYSIS] Analysis complete for %s", inputPath)

		timings := logging.AnalysisTimings{
			Analysis:   analysisResult.AnalysisDuration,
			Adaptation: analysisResult.AdaptationDuration,
		}
		deps.displayResults(deps.stdout, inputPath, metadata, analysisResult.Measurements, analysisResult.Config, analysisResult.Diagnostics, timings)
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

// runAnalysisWithTUI runs analysis with the Bubbletea progress UI.
func runAnalysisWithTUI(inputPath string, config *processor.BaseFilterConfig, log func(string, ...any)) (*processor.AnalysisResult, error) {
	model := ui.NewAnalysisModel()

	// Run without the alt screen so the report stays on screen after exit.
	p := tea.NewProgram(model)

	go func(path string) {
		p.Send(ui.AnalysisStartMsg{
			FileName: path,
			FilePath: path,
		})

		progressCallback := func(update processor.ProgressUpdate) {
			log("[ANALYSIS] Progress: Pass %d (%s), %.1f%%, Level %.1f dB", update.Pass, update.PassName, update.Progress*100, update.Level)
			p.Send(ui.AnalysisProgressMsg{
				Progress: update.Progress,
				Level:    update.Level,
			})
		}

		result, err := processor.AnalyzeOnlyDetailed(path, config, progressCallback)

		p.Send(ui.AnalysisCompleteMsg{
			Result: result,
			Error:  err,
		})
	}(inputPath)

	finalModel, err := p.Run()
	if err != nil {
		return nil, fmt.Errorf("UI error: %w", err)
	}

	analysisModel, ok := finalModel.(ui.AnalysisModel)
	if !ok {
		return nil, fmt.Errorf("unexpected model type")
	}

	if analysisModel.Error != nil {
		return nil, analysisModel.Error
	}

	// A TUI exit before Done means the user cancelled mid-analysis.
	if !analysisModel.Done {
		return nil, errCancelledByUser
	}

	return analysisModel.Result, nil
}
