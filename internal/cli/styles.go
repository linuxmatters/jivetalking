package cli

import (
	"os"
	"strings"

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
	// ColorCyanBright is the bright cyan start of the header letter gradient. Its
	// CIELAB path to ColorSkyBlue stays vivid (no muddy midpoint).
	ColorCyanBright = compat.AdaptiveColor{Light: lipgloss.Color("#00D4FF"), Dark: lipgloss.Color("#00D4FF")}
	// ColorMuted is the muted grey for labels and secondary borders.
	ColorMuted = compat.AdaptiveColor{Light: lipgloss.Color("#888888"), Dark: lipgloss.Color("#888888")}
	// ColorText is the primary value text colour.
	ColorText = compat.AdaptiveColor{Light: lipgloss.Color("#1A1A1A"), Dark: lipgloss.Color("#FFFFFF")}
	// ColorOrange is the warning / caution zone colour.
	ColorOrange = compat.AdaptiveColor{Light: lipgloss.Color("#FFA500"), Dark: lipgloss.Color("#FFA500")}
	// ColorGreen is the success / safe zone colour.
	ColorGreen = compat.AdaptiveColor{Light: lipgloss.Color("#00AA00"), Dark: lipgloss.Color("#00AA00")}
	// ColorYellow is the mid-warm VU-meter stop between green and orange.
	ColorYellow = compat.AdaptiveColor{Light: lipgloss.Color("#E6E600"), Dark: lipgloss.Color("#E6E600")}
	// ColorCyan is the accent colour used in help output.
	ColorCyan = compat.AdaptiveColor{Light: lipgloss.Color("#00AAAA"), Dark: lipgloss.Color("#00AAAA")}
	// ColorFill is the empty/unfilled fill colour for meters and progress.
	ColorFill = compat.AdaptiveColor{Light: lipgloss.Color("#CCCCCC"), Dark: lipgloss.Color("#444444")}
	// ColorLightGrey is the subtle light-grey for panel borders.
	ColorLightGrey = compat.AdaptiveColor{Light: lipgloss.Color("#666666"), Dark: lipgloss.Color("#CCCCCC")}
	// ColorSkyBlue is the sky-blue used for panel borders.
	ColorSkyBlue = compat.AdaptiveColor{Light: lipgloss.Color("#0284C7"), Dark: lipgloss.Color("#38BDF8")}
	// ColorIndigo is the indigo end of the progress bar gradient.
	ColorIndigo = compat.AdaptiveColor{Light: lipgloss.Color("#6366F1"), Dark: lipgloss.Color("#6366F1")}
	// ColorOrangeDim is the deep-orange trough of the peak-marker pulse.
	ColorOrangeDim = compat.AdaptiveColor{Light: lipgloss.Color("#B35F00"), Dark: lipgloss.Color("#B35F00")}
	// ColorBlue is the cold end of the gain thermometer (under-recorded peaks).
	ColorBlue = compat.AdaptiveColor{Light: lipgloss.Color("#2563EB"), Dark: lipgloss.Color("#3B82F6")}
)

// Color palette aliases retained for the cli styles below.
var (
	primaryColor = ColorRed
	mutedColor   = ColorMuted
	textColor    = ColorText
)

// Styles
var (
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

// RenderTitle returns the "Jivetalking 🕺" wordmark drawn as a per-letter
// cyan→sky-blue Blend1D gradient (bold per letter), with the 🕺 emoji appended
// outside the gradient so it keeps its own colours. Shared by the version banner
// and the processing-TUI header so both render the wordmark identically.
func RenderTitle() string {
	letters := []rune("Jivetalking")
	ramp := lipgloss.Blend1D(len(letters), ColorCyanBright, ColorSkyBlue)

	var b strings.Builder
	for i, r := range letters {
		b.WriteString(lipgloss.NewStyle().
			Bold(true).
			Foreground(ramp[i]).
			Render(string(r)))
	}
	b.WriteString(" 🕺")

	return b.String()
}

// PrintVersion prints version information
func PrintVersion(version string) {
	lipgloss.Println(RenderTitle())
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
