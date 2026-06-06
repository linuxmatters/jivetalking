package cli

import (
	"fmt"
	"os"

	"charm.land/lipgloss/v2"
)

// Color palette
var (
	primaryColor = lipgloss.Color("#A40000") // Jivetalking red
	mutedColor   = lipgloss.Color("#888888") // Gray
	textColor    = lipgloss.Color("#FFFFFF") // White
)

// Styles
var (
	// Title style - bold red with microphone emoji
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			MarginBottom(1)

	// Error message style
	ErrorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor)

	// Warning message style
	WarningStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#FFA500"))

	// Key-value pair styles
	KeyStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	ValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(textColor)
)

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
	fmt.Fprintf(os.Stderr, "%s %s\n", WarningStyle.Render("Warning:"), message)
}
