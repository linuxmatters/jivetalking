package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// Spinner frames for indeterminate progress
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// AnalysisModel is the Bubbletea model for analysis-only mode
type AnalysisModel struct {
	// File being analysed
	FileName string
	FilePath string

	// Progress tracking
	Progress  float64 // 0.0 to 1.0
	Level     float64 // Current audio level in dB
	StartTime time.Time

	// Spinner state
	spinnerIndex int

	// Results (populated when complete)
	Measurements *processor.AudioMeasurements
	Config       *processor.FilterChainConfig
	Error        error
	Done         bool

	// Terminal dimensions
	Width  int
	Height int
}

// AnalysisStartMsg signals analysis has started
type AnalysisStartMsg struct {
	FileName string
	FilePath string
}

// AnalysisProgressMsg signals progress update
type AnalysisProgressMsg struct {
	Progress float64
	Level    float64
}

// AnalysisCompleteMsg signals analysis has completed
type AnalysisCompleteMsg struct {
	Measurements *processor.AudioMeasurements
	Config       *processor.FilterChainConfig
	Error        error
}

// tickMsg is sent for spinner/timer animation
type tickMsg time.Time

// NewAnalysisModel creates a new analysis UI model
func NewAnalysisModel() AnalysisModel {
	return AnalysisModel{
		StartTime: time.Now(),
	}
}

// Init initializes the model
func (m AnalysisModel) Init() tea.Cmd {
	return tickCmd()
}

// tickCmd returns a command that sends a tick message every 100ms
func tickCmd() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// Update handles messages and updates the model
func (m AnalysisModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}

	case tea.WindowSizeMsg:
		m.Width = msg.Width
		m.Height = msg.Height

	case tickMsg:
		if !m.Done {
			// Advance spinner
			m.spinnerIndex = (m.spinnerIndex + 1) % len(spinnerFrames)
			return m, tickCmd()
		}
		return m, nil

	case AnalysisStartMsg:
		m.FileName = filepath.Base(msg.FilePath)
		m.FilePath = msg.FilePath
		m.StartTime = time.Now()
		return m, nil

	case AnalysisProgressMsg:
		m.Progress = msg.Progress
		m.Level = msg.Level
		return m, nil

	case AnalysisCompleteMsg:
		m.Measurements = msg.Measurements
		m.Config = msg.Config
		m.Error = msg.Error
		m.Done = true
		return m, tea.Quit
	}

	return m, nil
}

// View renders the UI
func (m AnalysisModel) View() string {
	if m.Width == 0 {
		return "Initializing..."
	}

	var b strings.Builder

	// Header
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#A40000")).
		Render("Jivetalking")

	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888")).
		Italic(true).
		Render("Analysis Mode")

	b.WriteString(title + " " + subtitle)
	b.WriteString("\n\n")

	if m.FileName == "" {
		b.WriteString("Waiting...")
		return b.String()
	}

	// File being analysed
	fileStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Bold(true)

	b.WriteString("Analysing: ")
	b.WriteString(fileStyle.Render(m.FileName))
	b.WriteString("\n\n")

	// Progress bar with spinner
	elapsed := time.Since(m.StartTime)
	spinnerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#A40000"))
	spinner := spinnerStyle.Render(spinnerFrames[m.spinnerIndex])

	if m.Progress > 0 && m.Progress < 1.0 {
		// Determinate progress bar with spinner
		b.WriteString(spinner)
		b.WriteString(" ")
		b.WriteString(renderAnalysisProgressBar(m.Progress, 40, elapsed))
	} else if !m.Done {
		// Indeterminate spinner
		b.WriteString(spinner)
		b.WriteString(" Processing...")
		b.WriteString(fmt.Sprintf(" [%s]", formatElapsed(elapsed)))
	}

	b.WriteString("\n")

	// Show audio level if available
	if m.Level != 0 && !m.Done {
		b.WriteString(fmt.Sprintf("\nLevel: %.1f dB", m.Level))
	}

	return b.String()
}

// renderAnalysisProgressBar renders a progress bar with percentage and elapsed time
func renderAnalysisProgressBar(progress float64, width int, elapsed time.Duration) string {
	filled := int(progress * float64(width))
	empty := width - filled

	// Use Unicode box drawing characters for a cleaner look
	filledStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#A40000"))
	emptyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#444444"))

	bar := filledStyle.Render(strings.Repeat("━", filled)) +
		emptyStyle.Render(strings.Repeat("━", empty))

	percentage := int(progress * 100)

	return fmt.Sprintf("%s %3d%% [%s]", bar, percentage, formatElapsed(elapsed))
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
