// Package ui provides the Bubbletea terminal user interface for jivetalking
package ui

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/harmonica"
	"github.com/linuxmatters/jivetalking/internal/cli"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// defaultProgressWidth is the fallback bar width before a WindowSizeMsg arrives.
const defaultProgressWidth = 40

// minProgressWidth floors the bar so it stays usable on narrow terminals.
const minProgressWidth = 10

// maxProgressWidth caps the bar so it does not sprawl on wide terminals.
const maxProgressWidth = 80

// processingBarOverhead is the horizontal chrome around the processing-view
// progress bar: RoundedBorder (2 cols) + Padding(0,1) (2 cols) plus a 2-col
// safety margin so the box and its percentage label never reach the edge.
const processingBarOverhead = 6

// analysisBarOverhead is the horizontal chrome around the analysis-view progress
// bar: a 3-col leading indent, the " [MM:SS]" elapsed suffix (~8 cols), plus a
// 2-col safety margin.
const analysisBarOverhead = 13

// progressWidthFor clamps the bar width derived from a terminal width and its
// surrounding chrome into the supported range.
func progressWidthFor(termWidth, overhead int) int {
	w := termWidth - overhead
	if w < minProgressWidth {
		return minProgressWidth
	}
	if w > maxProgressWidth {
		return maxProgressWidth
	}
	return w
}

// meterFPS is the spring step rate for the eased audio level meter (~60fps).
const meterFPS = 60

// meterStartDB is the initial eased position for a file's meter, matching the
// PeakLevel silence floor so the meter eases up from silence on first sample.
const meterStartDB = -60.0

// meterTickMsg drives the spring step for the eased audio level meter. The loop
// is self-scheduling while any file is active and stops once m.Done is set.
type meterTickMsg struct{}

// meterState holds the harmonica spring smoothing state for a single file's
// audio level meter. It is parallel to Model.Files (keyed by file index) so the
// routed FileProgress data contract stays free of presentation-only state.
type meterState struct {
	pos float64 // eased display position in dB
	vel float64 // spring velocity
}

// newProgressModel builds the shared gradient progress bar used by both the
// processing and analysis models.
func newProgressModel() progress.Model {
	// Solid brand-red fill. A 2-stop dark-red gradient blends through blue in
	// CIELAB space (lipgloss.Blend1D), so a single colour keeps the fill clean.
	p := progress.New(
		progress.WithColors(cli.ColorRed),
	)
	p.EmptyColor = cli.ColorFill
	p.SetWidth(defaultProgressWidth)
	return p
}

// FileStatus represents the processing state of a single file
type FileStatus int

const (
	StatusQueued FileStatus = iota
	StatusAnalyzing
	StatusProcessing
	StatusNormalising
	StatusComplete
	StatusError
)

// FileProgress tracks progress for a single audio file
type FileProgress struct {
	InputPath  string
	OutputPath string
	Status     FileStatus

	// Phase tracking
	CurrentPass processor.PassNumber
	PassName    string

	// Progress tracking (percentage-based)
	Progress    float64 // 0.0 to 1.0
	StartTime   time.Time
	ElapsedTime time.Duration

	// Analysis results (from Pass 1)
	Measurements *processor.AudioMeasurements

	// Processing statistics
	CurrentLevel float64 // Current audio level in dB
	PeakLevel    float64 // Peak level seen so far

	// Completion results
	InputLUFS  float64
	OutputLUFS float64
	NoiseFloor float64

	// Error tracking
	Error error
}

// Model is the Bubbletea model for the processing UI
type Model struct {
	// File queue
	Files          []FileProgress
	TotalFiles     int
	CompletedFiles int
	FailedFiles    int

	// Global state
	StartTime time.Time
	Done      bool

	// Progress bar (owned by Update; rendered via ViewAs)
	progress progress.Model

	// Eased audio level meter state, parallel to Files (keyed by file index).
	// Owned and mutated only in Update; never touched by pool workers.
	meters []meterState
	spring harmonica.Spring

	// Terminal dimensions
	Width  int
	Height int
}

// NewModel creates a new UI model with the given input files
func NewModel(inputFiles []string) Model {
	files := make([]FileProgress, len(inputFiles))
	meters := make([]meterState, len(inputFiles))
	for i, path := range inputFiles {
		files[i] = FileProgress{
			InputPath: path,
			Status:    StatusQueued,
			PeakLevel: -60.0, // Initialize to silence threshold
		}
		meters[i] = meterState{pos: meterStartDB}
	}

	return Model{
		Files:      files,
		TotalFiles: len(inputFiles),
		StartTime:  time.Now(),
		progress:   newProgressModel(),
		meters:     meters,
		// Gentle under-damped spring: eases toward target without hard snapping.
		spring: harmonica.NewSpring(harmonica.FPS(meterFPS), 6.0, 0.7),
	}
}

// meterTick schedules the next spring step for the eased audio level meter.
func meterTick() tea.Cmd {
	return tea.Tick(time.Second/meterFPS, func(time.Time) tea.Msg {
		return meterTickMsg{}
	})
}

// fileActive reports whether a file is still being worked on and therefore its
// meter should keep easing.
func fileActive(s FileStatus) bool {
	switch s {
	case StatusAnalyzing, StatusProcessing, StatusNormalising:
		return true
	default:
		return false
	}
}

// anyActive reports whether at least one file is still active, gating the tick
// loop so it terminates once processing finishes.
func (m Model) anyActive() bool {
	for i := range m.Files {
		if fileActive(m.Files[i].Status) {
			return true
		}
	}
	return false
}

// Init initializes the model and starts the meter tick loop.
func (m Model) Init() tea.Cmd {
	return meterTick()
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height
		if msg.Width > 0 {
			m.progress.SetWidth(progressWidthFor(msg.Width, processingBarOverhead))
		}

	case ProgressMsg:
		// Update the current file's progress
		if msg.FileIndex >= 0 && msg.FileIndex < len(m.Files) {
			m.Files[msg.FileIndex] = updateFileProgress(m.Files[msg.FileIndex], msg)
		}
		return m, nil

	case FileStartMsg:
		// Start processing next file
		if msg.FileIndex >= 0 && msg.FileIndex < len(m.Files) {
			m.Files[msg.FileIndex].Status = StatusAnalyzing
			m.Files[msg.FileIndex].StartTime = time.Now()
		}
		return m, nil

	case FileCompleteMsg:
		// Mark file as complete
		if msg.FileIndex >= 0 && msg.FileIndex < len(m.Files) {
			m.Files[msg.FileIndex].Status = StatusComplete
			m.Files[msg.FileIndex].InputLUFS = msg.InputLUFS
			m.Files[msg.FileIndex].OutputLUFS = msg.OutputLUFS
			m.Files[msg.FileIndex].NoiseFloor = msg.NoiseFloor
			m.Files[msg.FileIndex].OutputPath = msg.OutputPath
			m.Files[msg.FileIndex].Error = msg.Error

			if msg.Error != nil {
				m.Files[msg.FileIndex].Status = StatusError
				m.FailedFiles++
			} else {
				m.CompletedFiles++
			}
		}
		return m, nil

	case meterTickMsg:
		// Step each active file's meter spring toward its target level, then
		// re-schedule only while work remains. Stop once m.Done is set or no
		// file is active, guaranteeing the loop terminates on AllCompleteMsg.
		if m.Done {
			return m, nil
		}
		for i := range m.Files {
			if !fileActive(m.Files[i].Status) {
				continue
			}
			if i >= len(m.meters) {
				continue
			}
			target := m.Files[i].CurrentLevel
			m.meters[i].pos, m.meters[i].vel = m.spring.Update(
				m.meters[i].pos, m.meters[i].vel, target)
		}
		if !m.anyActive() {
			return m, nil
		}
		return m, meterTick()

	case AllCompleteMsg:
		// All files processed
		m.Done = true
		return m, tea.Quit
	}

	return m, nil
}

// View renders the UI
func (m Model) View() tea.View {
	// Debug: Show basic info even before window size is set
	if m.Width == 0 {
		view := tea.NewView(fmt.Sprintf("Initializing...\nFiles: %d\n", len(m.Files)))
		view.AltScreen = true
		return view
	}

	// Build the view based on current state
	var view tea.View
	if m.Done {
		view = tea.NewView(renderCompletionSummary(m))
	} else {
		view = tea.NewView(renderProcessingView(m))
	}
	view.AltScreen = true
	return view
}

// updateFileProgress updates a FileProgress based on a ProgressMsg
func updateFileProgress(fp FileProgress, msg ProgressMsg) FileProgress {
	// Reset the start time when transitioning to a new pass
	if msg.Pass != fp.CurrentPass {
		fp.StartTime = time.Now()
	}

	fp.Progress = msg.Progress
	fp.CurrentPass = msg.Pass
	fp.PassName = msg.PassName
	fp.ElapsedTime = time.Since(fp.StartTime)

	if msg.Measurements != nil {
		fp.Measurements = msg.Measurements
	}

	if msg.Level != 0 {
		fp.CurrentLevel = msg.Level
		if msg.Level > fp.PeakLevel {
			fp.PeakLevel = msg.Level
		}
	}

	// Update status based on pass
	switch msg.Pass {
	case processor.PassAnalysis:
		fp.Status = StatusAnalyzing
	case processor.PassProcessing:
		fp.Status = StatusProcessing
	case processor.PassMeasuring, processor.PassNormalising:
		fp.Status = StatusNormalising
	}

	return fp
}
