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

// meterWidth is the cell width of the audio level meter. The progress bar caps
// its rendered total at this width so its right edge aligns with the meter.
const meterWidth = 40

// defaultProgressWidth is the fallback bar width before a WindowSizeMsg arrives.
const defaultProgressWidth = meterWidth

// minProgressWidth floors the bar so it stays usable on narrow terminals.
const minProgressWidth = 10

// maxProgressWidth caps the bar's rendered total (fill + percentage label) so it
// aligns with the meterWidth-cell audio level meter rather than sprawling.
const maxProgressWidth = meterWidth

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

// meterFloorDB is the audio level meter's silence floor in dB: the bottom of the
// rendered dB range, the meter spring start position, and the initial PeakLevel.
// Shared so the meter display (views.go) and these start values never drift apart.
const meterFloorDB = -70.0

// meterTickMsg drives the spring step for the eased audio level meter. The loop
// is self-scheduling while any file is active and stops once m.Done is set.
type meterTickMsg struct{}

// meterState holds the harmonica spring smoothing state for a single file's
// audio level meter and its progress bar. It is parallel to Model.Files (keyed
// by file index) so the routed FileProgress data contract stays free of
// presentation-only state.
type meterState struct {
	pos     float64 // eased meter display position in dB
	vel     float64 // meter spring velocity
	progPos float64 // eased progress display position (0.0-1.0)
	progVel float64 // progress spring velocity
	peakPos float64 // eased peak-hold marker position in dB
	peakVel float64 // peak spring velocity
}

// newProgressModel builds the shared gradient progress bar used by both the
// processing and analysis models.
func newProgressModel() progress.Model {
	// Sky-blue to indigo gradient. WithScaled blends the two stops across the
	// filled portion only, so the gradient is always visible regardless of fill.
	// The CIELAB path between these endpoints stays vivid (no muddy midpoint).
	p := progress.New(
		progress.WithColors(cli.ColorSkyBlue, cli.ColorIndigo),
		progress.WithScaled(true),
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

	// Duration is the total audio length in seconds (constant per file; the
	// first non-zero value is kept). Drives the realtime-speed badge.
	Duration float64

	// Analysis results (from Pass 1)
	Measurements *processor.AudioMeasurements

	// Processing statistics
	CurrentLevel float64 // Current audio level in dB
	PeakLevel    float64 // Peak level seen so far

	// Completion results
	InputLUFS  float64
	OutputLUFS float64
	// FinalNoiseFloor is the output room-tone noise floor in dBFS (lower = cleaner),
	// shown in the done box and aligned with the quality score's noise component.
	FinalNoiseFloor float64
	Quality         processor.QualityScore

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

	// Eased audio level meter and progress bar state, parallel to Files (keyed
	// by file index). Owned and mutated only in Update; never touched by pool
	// workers.
	meters         []meterState
	spring         harmonica.Spring // eases the audio level meter
	progressSpring harmonica.Spring // eases the progress bar fill
	peakSpring     harmonica.Spring // eases the peak-hold marker

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
			PeakLevel: meterFloorDB, // Initialize to silence threshold
		}
		meters[i] = meterState{pos: meterFloorDB, peakPos: meterFloorDB}
	}

	return Model{
		Files:      files,
		TotalFiles: len(inputFiles),
		StartTime:  time.Now(),
		progress:   newProgressModel(),
		meters:     meters,
		// Gentle under-damped spring: eases toward target without hard snapping.
		spring: harmonica.NewSpring(harmonica.FPS(meterFPS), 6.0, 0.7),
		// Snappier critically-damped spring for the bar fill: smooth motion that
		// tracks progress promptly without overshoot.
		progressSpring: harmonica.NewSpring(harmonica.FPS(meterFPS), 10.0, 1.0),
		// Critically-damped spring for the peak marker: damping ratio 1.0 guarantees
		// a monotonic, no-overshoot approach so the eased marker/label never report a
		// value louder than the measured peak-hold.
		peakSpring: harmonica.NewSpring(harmonica.FPS(meterFPS), 8.0, 1.0),
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
		if msg.FileIndex >= 0 && msg.FileIndex < len(m.Files) {
			m.Files[msg.FileIndex] = updateFileProgress(m.Files[msg.FileIndex], msg)
		}
		return m, nil

	case FileStartMsg:
		if msg.FileIndex >= 0 && msg.FileIndex < len(m.Files) {
			m.Files[msg.FileIndex].Status = StatusAnalyzing
			m.Files[msg.FileIndex].StartTime = time.Now()
		}
		return m, nil

	case FileCompleteMsg:
		if msg.FileIndex >= 0 && msg.FileIndex < len(m.Files) {
			m.Files[msg.FileIndex].Status = StatusComplete
			m.Files[msg.FileIndex].InputLUFS = msg.InputLUFS
			m.Files[msg.FileIndex].OutputLUFS = msg.OutputLUFS
			m.Files[msg.FileIndex].FinalNoiseFloor = msg.FinalNoiseFloor
			m.Files[msg.FileIndex].OutputPath = msg.OutputPath
			m.Files[msg.FileIndex].Quality = msg.Quality
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
			m.meters[i].progPos, m.meters[i].progVel = m.progressSpring.Update(
				m.meters[i].progPos, m.meters[i].progVel, m.Files[i].Progress)
			m.meters[i].peakPos, m.meters[i].peakVel = m.peakSpring.Update(
				m.meters[i].peakPos, m.meters[i].peakVel, m.Files[i].PeakLevel)
		}
		if !m.anyActive() {
			return m, nil
		}
		return m, meterTick()

	case AllCompleteMsg:
		m.Done = true
		return m, tea.Quit
	}

	return m, nil
}

// View renders the UI
func (m Model) View() tea.View {
	// Render a placeholder until the first WindowSizeMsg sets m.Width.
	if m.Width == 0 {
		view := tea.NewView(fmt.Sprintf("Initializing...\nFiles: %d\n", len(m.Files)))
		view.AltScreen = true
		return view
	}

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

	// Duration is constant per file; keep the first non-zero value seen.
	if msg.Duration > 0 && fp.Duration == 0 {
		fp.Duration = msg.Duration
	}

	if msg.Measurements != nil {
		fp.Measurements = msg.Measurements
	}

	if msg.Level != 0 {
		fp.CurrentLevel = msg.Level
		if msg.Level > fp.PeakLevel {
			fp.PeakLevel = msg.Level
		}
	}

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
