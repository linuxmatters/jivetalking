package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/linuxmatters/jivetalking/internal/cli"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// analysisFileState tracks analysis progress and results for a single file
type analysisFileState struct {
	FileName string
	Progress float64 // 0.0 to 1.0
	Level    float64 // Current audio level in dB
	Done     bool
	Err      error
	Result   *processor.AnalysisResult
}

// AnalysisModel is the Bubbletea model for analysis-only mode
type AnalysisModel struct {
	// File queue
	Files          []analysisFileState
	TotalFiles     int
	CompletedFiles int
	FailedFiles    int

	// Global state
	StartTime time.Time
	Done      bool

	// Spinner state
	spinner spinner.Model

	// Progress bar (owned by Update; rendered via ViewAs)
	progress progress.Model

	// Terminal dimensions
	Width  int
	Height int
}

// AnalysisStartMsg signals analysis has started
type AnalysisStartMsg struct {
	FileIndex int
	FileName  string
	FilePath  string
}

// AnalysisProgressMsg signals progress update
type AnalysisProgressMsg struct {
	FileIndex int
	Progress  float64
	Level     float64
}

// AnalysisCompleteMsg signals analysis has completed
type AnalysisCompleteMsg struct {
	FileIndex    int
	Result       *processor.AnalysisResult
	Measurements *processor.AudioMeasurements
	Config       *processor.EffectiveFilterConfig
	Error        error
}

// NewAnalysisModel creates a new analysis UI model with the given input files
func NewAnalysisModel(files []string) AnalysisModel {
	states := make([]analysisFileState, len(files))
	for i, path := range files {
		states[i] = analysisFileState{
			FileName: filepath.Base(path),
		}
	}

	return AnalysisModel{
		Files:      states,
		TotalFiles: len(files),
		StartTime:  time.Now(),
		spinner:    spinner.New(),
		progress:   newProgressModel(),
	}
}

// Init initializes the model
func (m AnalysisModel) Init() tea.Cmd {
	return m.spinner.Tick
}

// Update handles messages and updates the model
func (m AnalysisModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			m.progress.SetWidth(progressWidthFor(msg.Width, analysisBarOverhead))
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case AnalysisStartMsg:
		if msg.FileIndex >= 0 && msg.FileIndex < len(m.Files) {
			m.Files[msg.FileIndex].FileName = filepath.Base(msg.FilePath)
		}
		return m, nil

	case AnalysisProgressMsg:
		if msg.FileIndex >= 0 && msg.FileIndex < len(m.Files) {
			m.Files[msg.FileIndex].Progress = msg.Progress
			m.Files[msg.FileIndex].Level = msg.Level
		}
		return m, nil

	case AnalysisCompleteMsg:
		if msg.FileIndex >= 0 && msg.FileIndex < len(m.Files) {
			m.Files[msg.FileIndex].Result = msg.Result
			m.Files[msg.FileIndex].Err = msg.Error
			m.Files[msg.FileIndex].Done = true

			if msg.Error != nil {
				m.FailedFiles++
			} else {
				m.CompletedFiles++
			}
		}
		return m, nil

	case AllCompleteMsg:
		m.Done = true
		return m, tea.Quit
	}

	return m, nil
}

// View renders the UI
func (m AnalysisModel) View() tea.View {
	if m.Width == 0 {
		return tea.NewView("Initializing...")
	}

	var b strings.Builder

	// Header
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(cli.ColorRed).
		Render("Jivetalking")

	subtitle := lipgloss.NewStyle().
		Foreground(cli.ColorMuted).
		Italic(true).
		Render("Analysis Mode")

	b.WriteString(title + " " + subtitle)
	b.WriteString("\n\n")

	if len(m.Files) == 0 {
		b.WriteString("Waiting...")
		return tea.NewView(b.String())
	}

	fileStyle := lipgloss.NewStyle().
		Foreground(cli.ColorText).
		Bold(true)
	spinnerStyle := lipgloss.NewStyle().Foreground(cli.ColorRed)
	doneStyle := lipgloss.NewStyle().Foreground(cli.ColorGreen)
	errorStyle := lipgloss.NewStyle().Foreground(cli.ColorRed)
	spinnerView := spinnerStyle.Render(m.spinner.View())
	elapsed := time.Since(m.StartTime)

	for i := range m.Files {
		f := &m.Files[i]

		switch {
		case f.Done && f.Err != nil:
			icon := errorStyle.Render("✗")
			fmt.Fprintf(&b, " %s %s\n   Error: %v\n", icon, fileStyle.Render(f.FileName), f.Err)
		case f.Done:
			icon := doneStyle.Render("🗸")
			fmt.Fprintf(&b, " %s %s\n   Analysed\n", icon, fileStyle.Render(f.FileName))
		default:
			fmt.Fprintf(&b, " %s %s\n", spinnerView, fileStyle.Render(f.FileName))
			fmt.Fprintf(&b, "   %s [%s]\n", m.progress.ViewAs(f.Progress), formatElapsed(elapsed))
			if f.Level != 0 {
				fmt.Fprintf(&b, "   Level: %.1f dB\n", f.Level)
			}
		}

		b.WriteString("\n")
	}

	footer := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cli.ColorMuted).
		Padding(0, 1).
		Render(fmt.Sprintf("Analysing %d files, %d complete, %d failed",
			m.TotalFiles, m.CompletedFiles, m.FailedFiles))
	b.WriteString(footer)

	return tea.NewView(b.String())
}

// formatElapsed formats elapsed time as MM:SS or HH:MM:SS
func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second

	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}
