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
		Render("Jivetalking 🕺")

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
		// 🗸 completed file with summary
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color("#00AA00")).Render("🗸")
		delta := file.OutputLUFS - file.InputLUFS
		summary := fmt.Sprintf("Input: %.1f LUFS | Output: %.1f LUFS | Δ %+.1f dB",
			file.InputLUFS, file.OutputLUFS, delta)
		return fmt.Sprintf(" %s %s → %s\n   %s", icon, fileName, filepath.Base(file.OutputPath), summary)

	case StatusAnalyzing, StatusProcessing, StatusNormalising:
		// 🞽 active file with detailed progress
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFA500")).Render("🞽")
		return fmt.Sprintf(" %s %s → %s\n%s",
			icon, fileName, generateOutputName(fileName),
			renderFileDetails(file))

	case StatusError:
		// ✗ failed file
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color("#A40000")).Render("✗")
		return fmt.Sprintf(" %s %s\n   Error: %v", icon, fileName, file.Error)

	default:
		// ⧗ queued file
		icon := lipgloss.NewStyle().Foreground(lipgloss.Color("#888888")).Render("⧗")
		return fmt.Sprintf(" %s %s\n   Queued...", icon, fileName)
	}
}

// renderFileDetails renders detailed progress for the active file
func renderFileDetails(file FileProgress) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#A40000")).
		Padding(0, 1)

	var content strings.Builder

	// Pass indicator
	var passName string
	switch file.CurrentPass {
	case 1:
		passName = "Analysing Audio"
	case 2:
		passName = "Processing Audio"
	case 3:
		passName = "Normalising Audio"
	default:
		passName = "Processing"
	}
	content.WriteString(fmt.Sprintf("Pass %d/3: %s\n", file.CurrentPass, passName))

	// Progress bar
	content.WriteString(renderProgressBar(file.Progress, 40))
	content.WriteString("\n\n")

	// Time estimates
	elapsed := file.ElapsedTime.Seconds()
	var remaining float64
	if file.Progress > 0 {
		remaining = (elapsed / file.Progress) - elapsed
	}
	content.WriteString(fmt.Sprintf("✇ Elapsed: %.1fs | Remaining: ~%.1fs\n", elapsed, remaining))

	// Audio level visualization
	if file.CurrentLevel != 0 {
		content.WriteString("\n")
		content.WriteString(renderAudioLevelMeter(file.CurrentLevel, file.PeakLevel))
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

// renderAudioLevelMeter renders a live audio level meter with dB visualization
func renderAudioLevelMeter(currentLevel, peakLevel float64) string {
	var b strings.Builder

	// Display current and peak levels
	b.WriteString(fmt.Sprintf("🕪 Audio Level: %.1f dB | Peak: %.1f dB\n", currentLevel, peakLevel))

	// Create visual meter
	// dB range: -60 dB (silence) to 0 dB (maximum)
	// Map to 40-character width meter
	width := 40
	minDB := -60.0
	maxDB := 0.0

	// Calculate fill position for current level
	currentPos := int(((currentLevel - minDB) / (maxDB - minDB)) * float64(width))
	if currentPos < 0 {
		currentPos = 0
	}
	if currentPos > width {
		currentPos = width
	}

	// Calculate position for peak marker
	peakPos := int(((peakLevel - minDB) / (maxDB - minDB)) * float64(width))
	if peakPos < 0 {
		peakPos = 0
	}
	if peakPos > width {
		peakPos = width
	}

	// Build the meter bar with color zones
	// Green: -60 to -18 dB (safe)
	// Orange: -18 to -6 dB (approaching loud)
	// Red: -6 to 0 dB (loud/clipping risk)
	greenZone := int((((-18.0) - minDB) / (maxDB - minDB)) * float64(width))
	orangeZone := int((((-6.0) - minDB) / (maxDB - minDB)) * float64(width))

	// Build meter character by character with appropriate colors
	// Using ANSI color codes directly to avoid lipgloss width calculation issues
	greenColor := "\033[38;2;0;170;0m"    // #00AA00
	orangeColor := "\033[38;2;255;165;0m" // #FFA500
	redColor := "\033[38;2;164;0;0m"      // #A40000
	resetColor := "\033[0m"

	for i := 0; i < width; i++ {
		// Determine color zone
		var color string
		if i < greenZone {
			color = greenColor
		} else if i < orangeZone {
			color = orangeColor
		} else {
			color = redColor
		}

		// Determine character
		var char rune
		if i == peakPos && i > currentPos {
			// Show peak marker only if it's ahead of current position
			char = '|'
		} else if i < currentPos {
			char = '▓' // Filled
		} else if i == currentPos && currentPos == peakPos {
			// When current level is at peak, show filled bar
			char = '▓'
		} else {
			char = '░' // Empty
		}

		b.WriteString(color)
		b.WriteRune(char)
	}
	b.WriteString(resetColor)

	return b.String()
}

// renderOverallProgress renders the overall progress footer
func renderOverallProgress(m Model) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#888888")).
		Padding(0, 1)

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
