package cli

import (
	"os"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"
)

// Palette is the single source of adaptive colours for both the cli and ui
// packages. Each value is a compat.AdaptiveColor, which satisfies image/color's
// Color interface and resolves Light/Dark variants at render time from the
// terminal background detected globally by the compat package. Use these
// instead of bespoke lipgloss.Color literals.
var (
	// ColorRed is the Jivetalking brand red (errors, titles, peak zone).
	ColorRed = compat.AdaptiveColor{Light: lipgloss.Color("#A40000"), Dark: lipgloss.Color("#A40000")}
	// ColorRedDim is the dark end of the progress gradient.
	ColorRedDim = compat.AdaptiveColor{Light: lipgloss.Color("#5A0000"), Dark: lipgloss.Color("#5A0000")}
	// ColorMuted is the muted grey for labels and secondary borders.
	ColorMuted = compat.AdaptiveColor{Light: lipgloss.Color("#888888"), Dark: lipgloss.Color("#888888")}
	// ColorText is the primary value text colour.
	ColorText = compat.AdaptiveColor{Light: lipgloss.Color("#1A1A1A"), Dark: lipgloss.Color("#FFFFFF")}
	// ColorOrange is the warning / caution zone colour.
	ColorOrange = compat.AdaptiveColor{Light: lipgloss.Color("#FFA500"), Dark: lipgloss.Color("#FFA500")}
	// ColorGreen is the success / safe zone colour.
	ColorGreen = compat.AdaptiveColor{Light: lipgloss.Color("#00AA00"), Dark: lipgloss.Color("#00AA00")}
	// ColorCyan is the accent colour used in help output.
	ColorCyan = compat.AdaptiveColor{Light: lipgloss.Color("#00AAAA"), Dark: lipgloss.Color("#00AAAA")}
	// ColorFill is the empty/unfilled fill colour for meters and progress.
	ColorFill = compat.AdaptiveColor{Light: lipgloss.Color("#CCCCCC"), Dark: lipgloss.Color("#444444")}
)

// Color palette aliases retained for the cli styles below.
var (
	primaryColor = ColorRed
	mutedColor   = ColorMuted
	textColor    = ColorText
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
			Foreground(ColorOrange)

	// Key-value pair styles
	KeyStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	ValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(textColor)
)

// PrintVersion prints version information
func PrintVersion(version string) {
	lipgloss.Println(TitleStyle.Render("Jivetalking 🕺"))
	lipgloss.Printf("%s %s\n", KeyStyle.Render("Version:"), ValueStyle.Render(version))
	lipgloss.Println()
}

// PrintError prints an error message
func PrintError(message string) {
	lipgloss.Fprintf(os.Stderr, "%s %s\n", ErrorStyle.Render("Error:"), message)
}

// PrintWarning prints a warning message
func PrintWarning(message string) {
	lipgloss.Fprintf(os.Stderr, "%s %s\n", WarningStyle.Render("Warning:"), message)
}
