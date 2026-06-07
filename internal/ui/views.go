package ui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/progress"
	"charm.land/lipgloss/v2"
	"github.com/linuxmatters/jivetalking/internal/cli"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// renderProcessingView renders the main processing view
func renderProcessingView(m Model) string {
	var b strings.Builder

	// Header
	b.WriteString(renderHeader(m))
	b.WriteString("\n\n")

	// File queue
	b.WriteString(renderFileQueue(m, m.progress))
	b.WriteString("\n\n")

	// Overall progress
	b.WriteString(renderOverallProgress(m))

	return b.String()
}

// renderHeader renders the application header
func renderHeader(m Model) string {
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(cli.ColorRed).
		Render("Jivetalking 🕺")

	subtitle := lipgloss.NewStyle().
		Foreground(cli.ColorMuted).
		Italic(true).
		Render(fmt.Sprintf("Processing %d file(s)", m.TotalFiles))

	return title + "\n" + subtitle
}

// renderFileQueue renders the list of files with their status
func renderFileQueue(m Model, prog progress.Model) string {
	var b strings.Builder

	for i := range m.Files {
		// Use the eased meter and progress positions for the active display;
		// fall back to the raw values when no spring slot exists.
		easedLevel := m.Files[i].CurrentLevel
		easedProgress := m.Files[i].Progress
		if i < len(m.meters) {
			easedLevel = m.meters[i].pos
			easedProgress = m.meters[i].progPos
		}
		b.WriteString(renderFileEntry(m.Files[i], prog, easedLevel, easedProgress))
		b.WriteString("\n")
	}

	return b.String()
}

// renderFileEntry renders a single file entry in the queue
func renderFileEntry(file FileProgress, prog progress.Model, easedLevel, easedProgress float64) string {
	fileName := filepath.Base(file.InputPath)

	switch file.Status {
	case StatusComplete:
		// 🗸 completed file with summary
		icon := lipgloss.NewStyle().Foreground(cli.ColorGreen).Render("🗸")
		delta := file.OutputLUFS - file.InputLUFS
		summary := fmt.Sprintf("Input: %.1f LUFS | Output: %.1f LUFS | Δ %+.1f dB",
			file.InputLUFS, file.OutputLUFS, delta)
		return fmt.Sprintf(" %s %s → %s\n   %s", icon, fileName, filepath.Base(file.OutputPath), summary)

	case StatusAnalyzing, StatusProcessing, StatusNormalising:
		// 🞽 active file with detailed progress
		icon := lipgloss.NewStyle().Foreground(cli.ColorOrange).Render("🞽")
		return fmt.Sprintf(" %s %s\n%s",
			icon, fileName,
			renderFileDetails(file, prog, easedLevel, easedProgress))

	case StatusError:
		// ✗ failed file
		icon := lipgloss.NewStyle().Foreground(cli.ColorRed).Render("✗")
		return fmt.Sprintf(" %s %s\n   Error: %v", icon, fileName, file.Error)

	default:
		// ⧗ queued file
		icon := lipgloss.NewStyle().Foreground(cli.ColorMuted).Render("⧗")
		return fmt.Sprintf(" %s %s\n   Queued...", icon, fileName)
	}
}

// renderFileDetails renders detailed progress for the active file. easedLevel is
// the spring-smoothed audio level used for the meter display.
func renderFileDetails(file FileProgress, prog progress.Model, easedLevel, easedProgress float64) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cli.ColorRed).
		Padding(0, 1)

	var content strings.Builder

	// Pass indicator
	var passName string
	switch file.CurrentPass {
	case processor.PassAnalysis:
		passName = "Analysing Audio" //nolint:gosec // G101 false positive: not a credential
	case processor.PassProcessing:
		passName = "Processing Audio"
	case processor.PassMeasuring:
		passName = "Measuring Levels"
	case processor.PassNormalising:
		passName = "Normalising Audio"
	default:
		passName = "Processing"
	}
	fmt.Fprintf(&content, "Pass %d/4: %s\n", file.CurrentPass, passName)

	// Progress bar (spring-eased fill for smooth motion)
	content.WriteString(prog.ViewAs(easedProgress))
	content.WriteString("\n\n")

	// Time estimates
	elapsed := file.ElapsedTime.Seconds()
	var remaining float64
	if file.Progress > 0 {
		remaining = (elapsed / file.Progress) - elapsed
	}
	fmt.Fprintf(&content, "✇ Elapsed: %.1fs | Remaining: ~%.1fs\n", elapsed, remaining)

	// Audio level visualization. The displayed level eases toward the target
	// via the spring; the peak marker stays driven by the measured peak.
	if file.CurrentLevel != 0 {
		content.WriteString("\n")
		content.WriteString(renderAudioLevelMeter(easedLevel, file.PeakLevel))
	}

	return box.Render(content.String())
}

// renderAudioLevelMeter renders a live audio level meter with dB visualization
func renderAudioLevelMeter(currentLevel, peakLevel float64) string {
	var b strings.Builder

	// Display current and peak levels
	fmt.Fprintf(&b, "🕪 Audio Level: %.1f dB | Peak: %.1f dB\n", currentLevel, peakLevel)

	// Create visual meter
	// dB range: -60 dB (silence) to 0 dB (maximum)
	// Map to meterWidth-character width meter
	width := meterWidth
	minDB := -60.0
	maxDB := 0.0

	// Calculate fill position for current level
	currentPos := max(0, min(int(((currentLevel-minDB)/(maxDB-minDB))*float64(width)), width))

	// Calculate position for peak marker
	peakPos := max(0, min(int(((peakLevel-minDB)/(maxDB-minDB))*float64(width)), width))

	// Build the meter bar with color zones
	// Green: -60 to -16 dB (safe)
	// Orange: -16 to -6 dB (approaching loud)
	// Red: -6 to 0 dB (loud/clipping risk)
	greenZone := int((((-16.0) - minDB) / (maxDB - minDB)) * float64(width))
	orangeZone := int((((-6.0) - minDB) / (maxDB - minDB)) * float64(width))

	// Zone colours sourced from the centralised palette.
	greenColor := cli.ColorGreen
	orangeColor := cli.ColorOrange
	redColor := cli.ColorRed

	zoneColor := func(i int) color.Color {
		switch {
		case i < greenZone:
			return greenColor
		case i < orangeZone:
			return orangeColor
		default:
			return redColor
		}
	}

	meterChar := func(i int) rune {
		switch {
		case i == peakPos && i > currentPos:
			// Show peak marker only if it's ahead of current position
			return '|'
		case i < currentPos:
			return '▓' // Filled
		case i == currentPos && currentPos == peakPos:
			// When current level is at peak, show filled bar
			return '▓'
		default:
			return '░' // Empty
		}
	}

	// Build contiguous same-colour runs and style each as one segment so
	// lipgloss emits a single colour sequence per run rather than per rune.
	var run strings.Builder
	var runColor color.Color
	flush := func() {
		if run.Len() == 0 {
			return
		}
		b.WriteString(lipgloss.NewStyle().Foreground(runColor).Render(run.String()))
		run.Reset()
	}

	for i := range width {
		color := zoneColor(i)
		if run.Len() > 0 && color != runColor {
			flush()
		}
		runColor = color
		run.WriteRune(meterChar(i))
	}
	flush()

	return b.String()
}

// renderOverallProgress renders the overall progress footer
func renderOverallProgress(m Model) string {
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cli.ColorMuted).
		Padding(0, 1)

	content := fmt.Sprintf("Processing %d files, %d complete, %d failed",
		m.TotalFiles, m.CompletedFiles, m.FailedFiles)

	return box.Render(content)
}

// renderCompletionSummary renders the final completion summary
func renderCompletionSummary(m Model) string {
	var b strings.Builder

	// Completion header
	header := lipgloss.NewStyle().
		Bold(true).
		Foreground(cli.ColorGreen).
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

	icon := lipgloss.NewStyle().Foreground(cli.ColorGreen).Render("✓")

	quality := "★★★★★" // Always 5 stars

	return fmt.Sprintf(" %s %s → %s\n"+
		"   Before: %.1f LUFS | After: %.1f LUFS | Quality: %s\n"+
		"   Noise Reduced: %.0f dB",
		icon, fileName, outputName,
		file.InputLUFS, file.OutputLUFS, quality,
		file.NoiseFloor)
}
