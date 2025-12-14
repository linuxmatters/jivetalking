// Package ui provides the Bubbletea terminal user interface for jivetalking
package ui

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

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
	CurrentPass int // 1, 2, or 3
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
	CurrentIndex   int
	TotalFiles     int
	CompletedFiles int
	FailedFiles    int

	// Global state
	StartTime time.Time
	Done      bool

	// Terminal dimensions
	Width  int
	Height int
}

// NewModel creates a new UI model with the given input files
func NewModel(inputFiles []string) Model {
	files := make([]FileProgress, len(inputFiles))
	for i, path := range inputFiles {
		files[i] = FileProgress{
			InputPath: path,
			Status:    StatusQueued,
			PeakLevel: -60.0, // Initialize to silence threshold
		}
	}

	return Model{
		Files:        files,
		CurrentIndex: -1, // No file processing yet
		TotalFiles:   len(inputFiles),
		StartTime:    time.Now(),
	}
}

// Init initializes the model
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles messages and updates the model
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height

	case ProgressMsg:
		// Update the current file's progress
		if m.CurrentIndex >= 0 && m.CurrentIndex < len(m.Files) {
			m.Files[m.CurrentIndex] = updateFileProgress(m.Files[m.CurrentIndex], msg)
		}
		return m, nil

	case FileStartMsg:
		// Start processing next file
		m.CurrentIndex = msg.FileIndex
		m.Files[m.CurrentIndex].Status = StatusAnalyzing
		m.Files[m.CurrentIndex].StartTime = time.Now()
		return m, nil

	case FileCompleteMsg:
		// Mark file as complete
		if m.CurrentIndex >= 0 && m.CurrentIndex < len(m.Files) {
			m.Files[m.CurrentIndex].Status = StatusComplete
			m.Files[m.CurrentIndex].InputLUFS = msg.InputLUFS
			m.Files[m.CurrentIndex].OutputLUFS = msg.OutputLUFS
			m.Files[m.CurrentIndex].NoiseFloor = msg.NoiseFloor
			m.Files[m.CurrentIndex].Error = msg.Error

			if msg.Error != nil {
				m.Files[m.CurrentIndex].Status = StatusError
				m.FailedFiles++
			} else {
				m.CompletedFiles++
			}
		}
		return m, nil

	case AllCompleteMsg:
		// All files processed
		m.Done = true
		return m, tea.Quit
	}

	return m, nil
}

// View renders the UI
func (m Model) View() string {
	// Debug: Show basic info even before window size is set
	if m.Width == 0 {
		return fmt.Sprintf("Initializing...\nFiles: %d\nCurrent: %d\n", len(m.Files), m.CurrentIndex)
	}

	// Build the view based on current state
	if m.Done {
		return renderCompletionSummary(m)
	}

	return renderProcessingView(m)
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
	case 1:
		fp.Status = StatusAnalyzing
	case 2:
		fp.Status = StatusProcessing
	case 3:
		fp.Status = StatusNormalising
	}

	return fp
}
