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
	Version         bool     `short:"v" help:"Show version information"`
	Debug           bool     `short:"d" help:"Enable debug logging to jivetalking-debug.log"`
	Logs            bool     `help:"Save detailed analysis logs"`
	BreathReduction bool     `help:"Enable breath reduction in gate" default:"true" negatable:""`
	Files           []string `arg:"" name:"files" help:"Audio files to process" type:"existingfile" optional:""`
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
	config.BreathReductionEnabled = cliArgs.BreathReduction

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
