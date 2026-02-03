package main

import (
	"fmt"
	"os"
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

// version is set via ldflags at build time
// Local dev builds: "dev"
// Release builds: git tag (e.g. "0.1.0")
var version = "dev"

// CLI defines the command-line interface
type CLI struct {
	Version      bool     `short:"v" help:"Show version information"`
	Debug        bool     `short:"d" help:"Enable debug logging to jivetalking-debug.log"`
	Logs         bool     `help:"Save detailed analysis logs"`
	AnalysisOnly bool     `short:"a" help:"Run analysis only (Pass 1), display results, skip processing"`
	Files        []string `arg:"" name:"files" help:"Audio files to process" type:"existingfile" optional:""`
}

func main() {
	// Suppress FFmpeg info/verbose logging to keep console clean
	// This prevents astats and other filters from printing summaries to stderr
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

	// Handle version flag
	if cliArgs.Version {
		cli.PrintVersion(version)
		os.Exit(0)
	}

	// Validate input
	if len(cliArgs.Files) == 0 {
		cli.PrintError("No input files specified")
		ctx.PrintUsage(false)
		os.Exit(1)
	}

	// Create default filter configuration
	config := processor.DefaultFilterConfig()

	// Open debug log file if --debug flag is set
	var debugLog *os.File
	if cliArgs.Debug {
		debugLog, _ = os.Create("jivetalking-debug.log")
		defer debugLog.Close()
	}
	log := func(format string, args ...interface{}) {
		if debugLog != nil {
			fmt.Fprintf(debugLog, format+"\n", args...)
		}
	}

	// Set the processor package's debug log function to use the same log
	processor.DebugLog = log

	// Handle analysis-only mode: run Pass 1 and display results, skip TUI
	if cliArgs.AnalysisOnly {
		runAnalysisOnly(cliArgs.Files, log)
		return
	}

	// Create the Bubbletea UI model
	model := ui.NewModel(cliArgs.Files)

	// Start the TUI
	p := tea.NewProgram(model, tea.WithAltScreen())

	// Start processing in background
	go func() {
		for i, inputPath := range cliArgs.Files {
			fileStartTime := time.Now()

			// Signal file start
			log("[MAIN] Sending FileStartMsg for file %d: %s", i, inputPath)
			p.Send(ui.FileStartMsg{
				FileIndex: i,
				FileName:  inputPath,
			})

			// Create progress handler
			ph := &progressHandler{
				p:   p,
				log: log,
			}

			// Process the audio file
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
			pass2Time := time.Since(pass2Start) - ph.pass1Time - ph.pass3Time - ph.pass4Time

			// Get file metadata for logging
			var metadata *audio.Metadata
			if cliArgs.Logs {
				reader, meta, err := audio.OpenAudioFile(inputPath)
				if err == nil {
					metadata = meta
					reader.Close()
				}
			}

			// Generate analysis report if --logs flag is set
			if cliArgs.Logs && metadata != nil {
				reportData := logging.ReportData{
					InputPath:    inputPath,
					OutputPath:   result.OutputPath,
					StartTime:    fileStartTime,
					EndTime:      time.Now(),
					Pass1Time:    ph.pass1Time,
					Pass2Time:    pass2Time,
					Pass3Time:    ph.pass3Time,
					Pass4Time:    ph.pass4Time,
					Result:       result,
					SampleRate:   metadata.SampleRate,
					Channels:     metadata.Channels,
					DurationSecs: metadata.Duration,
				}
				if err := logging.GenerateReport(reportData); err != nil {
					log("[MAIN] Failed to generate log file: %v", err)
				}
			}

			// Signal file complete with actual data
			log("[MAIN] Sending FileCompleteMsg for file %d", i)
			p.Send(ui.FileCompleteMsg{
				FileIndex:  i,
				InputLUFS:  result.InputLUFS,
				OutputLUFS: result.OutputLUFS,
				NoiseFloor: result.NoiseFloor,
				OutputPath: result.OutputPath,
			})
		}

		// Signal all complete
		log("[MAIN] Sending AllCompleteMsg")
		p.Send(ui.AllCompleteMsg{})
	}()

	// Run the program
	if _, err := p.Run(); err != nil {
		cli.PrintError(fmt.Sprintf("UI error: %v", err))
		os.Exit(1)
	}
}

// progressHandler handles progress updates from the processor
type progressHandler struct {
	p          *tea.Program
	log        func(string, ...interface{})
	pass1Start time.Time
	pass1Time  time.Duration
	pass3Start time.Time
	pass3Time  time.Duration
	pass4Start time.Time
	pass4Time  time.Duration
}

func (ph *progressHandler) callback(pass int, passName string, progress float64, level float64, measurements *processor.AudioMeasurements) {
	ph.log("[MAIN] Sending ProgressMsg: Pass %d (%s), Progress %.1f%%, Level %.1f dB", pass, passName, progress*100, level)

	// Track pass timing
	if pass == 1 && progress == 0.0 {
		ph.pass1Start = time.Now()
	} else if pass == 1 && progress == 1.0 {
		ph.pass1Time = time.Since(ph.pass1Start)
	} else if pass == 3 && progress == 0.0 {
		ph.pass3Start = time.Now()
	} else if pass == 3 && progress == 1.0 {
		ph.pass3Time = time.Since(ph.pass3Start)
	} else if pass == 4 && progress == 0.0 {
		ph.pass4Start = time.Now()
	} else if pass == 4 && progress == 1.0 {
		ph.pass4Time = time.Since(ph.pass4Start)
	}

	ph.p.Send(ui.ProgressMsg{
		Pass:         pass,
		PassName:     passName,
		Progress:     progress,
		Level:        level,
		Measurements: measurements,
	})
}

// runAnalysisOnly performs Pass 1 analysis on each file with a progress UI,
// then displays results to console. Skips full 4-pass processing.
func runAnalysisOnly(files []string, log func(string, ...interface{})) {
	config := processor.DefaultFilterConfig()

	// Check if we have a TTY for the progress UI
	hasTTY := isTTY()

	for i, inputPath := range files {
		// Add separator between multiple files
		if i > 0 {
			fmt.Println()
		}

		log("[ANALYSIS] Starting analysis for %s", inputPath)

		// Get file metadata for duration/sample rate display
		reader, metadata, err := audio.OpenAudioFile(inputPath)
		if err != nil {
			cli.PrintError(fmt.Sprintf("Failed to open %s: %v", inputPath, err))
			continue
		}
		reader.Close()

		var measurements *processor.AudioMeasurements
		var adaptedConfig *processor.FilterChainConfig
		var analysisErr error

		if hasTTY {
			// Run with TUI progress display
			measurements, adaptedConfig, analysisErr = runAnalysisWithTUI(inputPath, config, log)
		} else {
			// Fallback: run without TUI (for non-interactive environments)
			log("[ANALYSIS] No TTY available, running without progress UI")
			fmt.Printf("Analysing: %s\n", inputPath)
			measurements, adaptedConfig, analysisErr = processor.AnalyzeOnly(inputPath, config, nil)
		}

		if analysisErr != nil {
			cli.PrintError(fmt.Sprintf("Analysis failed for %s: %v", inputPath, analysisErr))
			continue
		}

		log("[ANALYSIS] Analysis complete for %s", inputPath)

		// Display results to console
		logging.DisplayAnalysisResults(os.Stdout, inputPath, metadata, measurements, adaptedConfig)
	}
}

// isTTY checks if stdout is connected to a terminal
func isTTY() bool {
	fileInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fileInfo.Mode() & os.ModeCharDevice) != 0
}

// runAnalysisWithTUI runs analysis with the Bubbletea progress UI
func runAnalysisWithTUI(inputPath string, config *processor.FilterChainConfig, log func(string, ...interface{})) (*processor.AudioMeasurements, *processor.FilterChainConfig, error) {
	// Create the analysis UI model
	model := ui.NewAnalysisModel()

	// Start the TUI (not in alt screen so output remains visible)
	p := tea.NewProgram(model)

	// Run analysis in background goroutine
	go func(path string) {
		// Signal analysis start
		p.Send(ui.AnalysisStartMsg{
			FileName: path,
			FilePath: path,
		})

		// Create progress callback that sends updates to TUI
		progressCallback := func(pass int, passName string, progress float64, level float64, measurements *processor.AudioMeasurements) {
			log("[ANALYSIS] Progress: Pass %d (%s), %.1f%%, Level %.1f dB", pass, passName, progress*100, level)
			p.Send(ui.AnalysisProgressMsg{
				Progress: progress,
				Level:    level,
			})
		}

		// Run analysis-only with progress callback
		measurements, adaptedConfig, err := processor.AnalyzeOnly(path, config, progressCallback)

		// Signal completion
		p.Send(ui.AnalysisCompleteMsg{
			Measurements: measurements,
			Config:       adaptedConfig,
			Error:        err,
		})
	}(inputPath)

	// Run the TUI until analysis completes
	finalModel, err := p.Run()
	if err != nil {
		return nil, nil, fmt.Errorf("UI error: %w", err)
	}

	// Get the final model state
	analysisModel, ok := finalModel.(ui.AnalysisModel)
	if !ok {
		return nil, nil, fmt.Errorf("unexpected model type")
	}

	// Check for analysis error
	if analysisModel.Error != nil {
		return nil, nil, analysisModel.Error
	}

	return analysisModel.Measurements, analysisModel.Config, nil
}
