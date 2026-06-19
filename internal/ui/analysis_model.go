package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/progress"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/linuxmatters/jivetalking/internal/cli"
	"github.com/linuxmatters/jivetalking/internal/processor"
	"github.com/linuxmatters/jivetalking/internal/report"
)

// analysisFileState tracks analysis progress and results for a single file
type analysisFileState struct {
	FileName string
	Progress float64 // 0.0 to 1.0
	Level    float64 // Current audio level, raw decode dBFS axis (not a VAD output)
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
	// ElapsedTime is frozen from time.Since(StartTime) in Update on each progress
	// message, so View stays a pure function of model state (no clock read at
	// render). Mirrors FileProgress.ElapsedTime in the processing model.
	ElapsedTime time.Duration
	Done        bool

	// Progress bar (owned by Update; rendered via ViewAs)
	progress progress.Model

	// Terminal dimensions
	Width  int
	Height int
}

// The analysis-only message set lives here beside its model; the processing-mode
// set lives in messages.go. Both are routed by FileIndex into the owning model's
// pre-allocated per-file slots, so concurrent pool workers never share state.

// AnalysisStartMsg signals analysis has started for the file at FileIndex.
type AnalysisStartMsg struct {
	FileIndex int
	FileName  string
	FilePath  string
}

// AnalysisProgressMsg carries a progress (0.0-1.0) and current level update for
// the file at FileIndex.
type AnalysisProgressMsg struct {
	FileIndex int
	Progress  float64
	Level     float64
}

// AnalysisCompleteMsg signals analysis has completed for the file at FileIndex,
// carrying the result or an error.
type AnalysisCompleteMsg struct {
	FileIndex int
	Result    *processor.AnalysisResult
	Error     error
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
		progress:   newProgressModel(),
	}
}

// Init initializes the model
func (m AnalysisModel) Init() tea.Cmd {
	return nil
}

// Update handles messages and updates the model
func (m AnalysisModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if handled, cmd := handleCommonMsg(msg, &m.Width, &m.Height, &m.Done, &m.progress, analysisBarOverhead); handled {
		return m, cmd
	}

	switch msg := msg.(type) {
	case AnalysisStartMsg:
		if msg.FileIndex >= 0 && msg.FileIndex < len(m.Files) {
			m.Files[msg.FileIndex].FileName = filepath.Base(msg.FilePath)
		}
		return m, nil

	case AnalysisProgressMsg:
		m.ElapsedTime = time.Since(m.StartTime)
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
	}

	return m, nil
}

// View renders the UI
func (m AnalysisModel) View() tea.View {
	if m.Width == 0 {
		return tea.NewView("Initializing...")
	}

	var b strings.Builder

	// Header (title only), then the status box directly beneath it.
	b.WriteString(cli.RenderTitle())
	b.WriteString("\n\n")

	statusBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cli.ColorMuted).
		Padding(0, 1).
		Render(fmt.Sprintf("Analysing %d files, %d complete, %d failed",
			m.TotalFiles, m.CompletedFiles, m.FailedFiles))
	b.WriteString(statusBox)
	b.WriteString("\n\n")

	if len(m.Files) == 0 {
		b.WriteString("Waiting...")
		return tea.NewView(b.String())
	}

	fileStyle := lipgloss.NewStyle().
		Foreground(cli.ColorText).
		Bold(true)
	activeIcon := lipgloss.NewStyle().Foreground(cli.ColorOrange).Render("∿")
	doneStyle := lipgloss.NewStyle().Foreground(cli.ColorGreen)
	errorStyle := lipgloss.NewStyle().Foreground(cli.ColorRed)
	elapsed := m.ElapsedTime

	for i := range m.Files {
		f := &m.Files[i]

		switch {
		case f.Done && f.Err != nil:
			icon := errorStyle.Render("✗")
			fmt.Fprintf(&b, " %s %s\n   Error: %v\n", icon, fileStyle.Render(f.FileName), f.Err)
		case f.Done:
			icon := doneStyle.Render("🗸")
			logName := filepath.Base(report.AnalysisReportPath(f.FileName))
			fmt.Fprintf(&b, " %s %s → %s\n", icon, fileStyle.Render(f.FileName), logName)
			if f.Result != nil && f.Result.Measurements != nil {
				b.WriteString(renderAnalysisVerdict(f.Result.Measurements))
			}
		default:
			fmt.Fprintf(&b, " %s %s\n", activeIcon, fileStyle.Render(f.FileName))
			fmt.Fprintf(&b, "   %s [%s]\n", m.progress.ViewAs(f.Progress), formatElapsed(elapsed))
			if f.Level != 0 {
				fmt.Fprintf(&b, "   Level: %.1f ㏈\n", f.Level)
			}
		}

		b.WriteString("\n")
	}

	return tea.NewView(b.String())
}

// renderAnalysisVerdict renders the two light-touch verdict lines shown under a
// completed analysis row: the Recording capture stars + label, and the one-lever
// gain advice. Both are pure functions of the Pass-1 INPUT measurements
// (ComputeRecordingScore and GainAdvice), so the analysis-only mode reuses the
// same Recording score the processing done box shows. The .md report stays
// verdict-free; these lines live only in the TUI/console.
func renderAnalysisVerdict(m *processor.AudioMeasurements) string {
	starStyle := lipgloss.NewStyle().Foreground(cli.ColorOrange)
	labelStyle := lipgloss.NewStyle().Foreground(cli.ColorMuted)

	rec := processor.ComputeRecordingScore(m)
	advice := processor.GainAdvice(m.Loudness.InputTP)

	var b strings.Builder
	fmt.Fprintf(&b, "   %s  %s  %s\n",
		labelStyle.Render("Recording"), starStyle.Render(QualityStars(rec.Stars)), rec.Label)
	fmt.Fprintf(&b, "   %s  %s  %s\n",
		labelStyle.Render("Gain     "), GainBar(m.Loudness.InputTP), advice.Message())
	return b.String()
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
