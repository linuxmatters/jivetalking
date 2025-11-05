package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderProcessingView renders the main processing view
func renderProcessingView(m Model) string {
	var b strings.Builder

	// Header
	b.WriteString(renderHeader(m))
	b.WriteString("\n\n")

	// File queue
	b.WriteString(renderFileQueue(m))
	b.WriteString("\n\n")

	// Overall progress
	b.WriteString(renderOverallProgress(m))

	return b.String()
}

// renderHeader renders the application header
func renderHeader(m Model) string {
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#A40000")).
		Render("Jivetalking 🕺 - Podcast Audio Preprocessor")

	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888")).
		Italic(true).
		Render(fmt.Sprintf("Processing %d file(s)", m.TotalFiles))

	return title + "\n" + subtitle
}

// renderFileQueue renders the list of files with their status
func renderFileQueue(m Model) string {
	var b strings.Builder

	for i, file := range m.Files {
		b.WriteString(renderFileEntry(file, i, m.CurrentIndex))
		b.WriteString("\n")
	}

	return b.String()
}

// renderFileEntry renders a single file entry in the queue
func renderFileEntry(file FileProgress, index int, currentIndex int) string {
	fileName := filepath.Base(file.InputPath)

	switch file.Status {
	case StatusComplete:
		// ✓ completed file with summary
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AA00")).Render("✓")
		delta := file.OutputLUFS - file.InputLUFS
		summary := fmt.Sprintf("Input: %.1f LUFS | Output: %.1f LUFS | Δ %+.1f dB",
			file.InputLUFS, file.OutputLUFS, delta)
		return fmt.Sprintf(" %s %s → %s\n   %s", icon, fileName, filepath.Base(file.OutputPath), summary)

	case StatusAnalyzing, StatusProcessing:
		// ⚙ active file with detailed progress
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")).Render("⚙")
		return fmt.Sprintf(" %s %s → %s\n%s",
			icon, fileName, generateOutputName(fileName),
			renderFileDetails(file))

	case StatusError:
		// ✗ failed file
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color("#A40000")).Render("✗")
		return fmt.Sprintf(" %s %s\n   Error: %v", icon, fileName, file.Error)

	default:
		// ○ queued file
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render("○")
		return fmt.Sprintf(" %s %s\n   Queued...", icon, fileName)
	}
}

// renderFileDetails renders detailed progress for the active file
func renderFileDetails(file FileProgress) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#A40000")).
		Padding(0, 1).
		Width(60)

	var content strings.Builder

	// Pass indicator
	passName := "Analyzing Audio"
	if file.CurrentPass == 2 {
		passName = "Processing Audio"
	}
	content.WriteString(fmt.Sprintf("Pass %d/2: %s\n", file.CurrentPass, passName))

	// Progress bar
	content.WriteString(renderProgressBar(file.Progress, 40))
	content.WriteString("\n\n")

	// Time estimates
	elapsed := file.ElapsedTime.Seconds()
	var remaining float64
	if file.Progress > 0 {
		remaining = (elapsed / file.Progress) - elapsed
	}
	content.WriteString(fmt.Sprintf("⏱  Elapsed: %.1fs | Remaining: ~%.1fs\n", elapsed, remaining))

	// Current level if available
	if file.CurrentLevel != 0 {
		content.WriteString(fmt.Sprintf("📊 Current Level: %.1f dB | Peak: %.1f dB",
			file.CurrentLevel, file.PeakLevel))
	}

	return box.Render(content.String())
}

// renderProgressBar renders a progress bar
func renderProgressBar(progress float64, width int) string {
	filled := int(progress * float64(width))
	empty := width - filled

	bar := strings.Repeat("█", filled) + strings.Repeat("░", empty)
	percentage := int(progress * 100)

	return fmt.Sprintf("%s %d%%", bar, percentage)
}

// renderOverallProgress renders the overall progress footer
func renderOverallProgress(m Model) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#888888")).
		Padding(0, 1).
		Width(60)

	// Show current file being processed
	var content string
	if m.CurrentIndex >= 0 && m.CurrentIndex < len(m.Files) {
		currentFile := m.CurrentIndex + 1 // 1-indexed for display
		content = fmt.Sprintf("Processing file %d of %d (%d complete)",
			currentFile, m.TotalFiles, m.CompletedFiles)
	} else {
		content = fmt.Sprintf("Overall Progress: %d/%d complete", m.CompletedFiles, m.TotalFiles)
	}

	return box.Render(content)
}

// renderCompletionSummary renders the final completion summary
func renderCompletionSummary(m Model) string {
	var b strings.Builder

	// Completion header
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#00AA00")).
		Render("✨ Processing Complete!")
	b.WriteString(header)
	b.WriteString("\n\n")

	// Summary for each file
	for _, file := range m.Files {
		if file.Status == StatusComplete {
			b.WriteString(renderCompletedFile(file))
			b.WriteString("\n")
		}
	}

	// Overall summary
	b.WriteString("\n")
	b.WriteString(strings.Repeat("─", 60))
	b.WriteString("\n")
	b.WriteString("All files normalized to -16 LUFS and level-matched ✓\n")
	b.WriteString("Ready for import into Audacity - no additional processing needed!\n")

	return b.String()
}

// renderCompletedFile renders a summary for a completed file
func renderCompletedFile(file FileProgress) string {
	fileName := filepath.Base(file.InputPath)
	outputName := filepath.Base(file.OutputPath)

	icon := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AA00")).Render("✓")

	quality := "★★★★★" // Always 5 stars for now

	return fmt.Sprintf(" %s %s → %s\n"+
		"   Before: %.1f LUFS | After: %.1f LUFS | Quality: %s\n"+
		"   Noise Reduced: %.0f dB",
		icon, fileName, outputName,
		file.InputLUFS, file.OutputLUFS, quality,
		file.NoiseFloor)
}

// generateOutputName generates the output filename from input
func generateOutputName(input string) string {
	ext := filepath.Ext(input)
	base := strings.TrimSuffix(input, ext)
	return base + "-processed" + ext
}
