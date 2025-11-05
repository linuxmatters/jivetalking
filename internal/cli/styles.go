package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Color palette
var (
	primaryColor   = lipgloss.Color("#A40000") // Jivetalking red
	accentColor    = lipgloss.Color("#FFA500") // Orange/gold
	successColor   = lipgloss.Color("#00AA00") // Green
	mutedColor     = lipgloss.Color("#888888") // Gray
	highlightColor = lipgloss.Color("#FFFF00") // Yellow
	textColor      = lipgloss.Color("#FFFFFF") // White
)

// Styles
var (
	// Title style - bold red with microphone emoji
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			MarginBottom(1)

	// Subtitle style - muted gray
	SubtitleStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)

	// Section header style
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor).
			MarginTop(1).
			MarginBottom(1)

	// Success message style
	SuccessStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(successColor)

	// Error message style
	ErrorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor)

	// Highlight style for important values
	HighlightStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlightColor)

	// Key-value pair styles
	KeyStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	ValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(textColor)

	// Box style for framed content
	BoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(primaryColor).
			Padding(1, 2).
			MarginTop(1).
			MarginBottom(1)
)

// PrintBanner prints the application banner
func PrintBanner() {
	banner := TitleStyle.Render("Jivetalking 🕺")
	subtitle := SubtitleStyle.Render("Professional podcast audio preprocessor")
	fmt.Println(banner)
	fmt.Println(subtitle)
	fmt.Println()
}

// PrintVersion prints version information
func PrintVersion(version string) {
	fmt.Println(TitleStyle.Render("Jivetalking 🕺"))
	fmt.Printf("%s %s\n", KeyStyle.Render("Version:"), ValueStyle.Render(version))
	fmt.Println()
}

// PrintError prints an error message
func PrintError(message string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", ErrorStyle.Render("Error:"), message)
}

// PrintWarning prints a warning message
func PrintWarning(message string) {
	fmt.Printf("%s %s\n", HighlightStyle.Render("Warning:"), message)
}

// PrintSuccess prints a success message
func PrintSuccess(message string) {
	fmt.Printf("%s %s\n", SuccessStyle.Render("✓"), message)
}

// PrintInfo prints an informational message
func PrintInfo(key, value string) {
	fmt.Printf("%s %s\n", KeyStyle.Render(key+":"), ValueStyle.Render(value))
}

// PrintSection prints a section header
func PrintSection(title string) {
	fmt.Println(HeaderStyle.Render(title))
}

// FormatDuration formats a duration nicely
func FormatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%.0fms", d.Seconds()*1000)
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// FormatBytes formats bytes into human-readable format
func FormatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

// PrintBox prints content in a styled box
func PrintBox(content string) {
	fmt.Println(BoxStyle.Render(content))
}

// PrintProcessingSummary prints a summary in a box
func PrintProcessingSummary(files int, duration, totalSize string) {
	var b strings.Builder

	b.WriteString(SuccessStyle.Render("✓ Processing Complete!"))
	b.WriteString("\n\n")

	b.WriteString(KeyStyle.Render("Files:     "))
	b.WriteString(ValueStyle.Render(fmt.Sprintf("%d", files)))
	b.WriteString("\n")

	b.WriteString(KeyStyle.Render("Duration:  "))
	b.WriteString(ValueStyle.Render(duration))
	b.WriteString("\n")

	b.WriteString(KeyStyle.Render("Total:     "))
	b.WriteString(ValueStyle.Render(totalSize))
	b.WriteString("\n\n")

	b.WriteString(SuccessStyle.Render("All files normalized to -16 LUFS and level-matched ✓"))
	b.WriteString("\n")
	b.WriteString(SubtitleStyle.Render("Ready for import into Audacity - no additional processing needed!"))

	PrintBox(b.String())
}
