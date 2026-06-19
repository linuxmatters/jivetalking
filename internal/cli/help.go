package cli

import (
	"fmt"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/kong"
	"github.com/charmbracelet/colorprofile"
)

// Help styles for the section headers, flag names, and argument names in the
// StyledHelpPrinter output.
var (
	helpDescStyle = lipgloss.NewStyle().
			Foreground(ColorOrange).
			Italic(true).
			MarginBottom(1)

	helpSectionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorOrange).
				MarginTop(1)

	helpFlagStyle = lipgloss.NewStyle().
			Foreground(ColorGreen).
			Bold(true)

	helpArgStyle = lipgloss.NewStyle().
			Foreground(ColorCyan).
			Bold(true)
)

// StyledHelpPrinter returns a Kong help printer that renders the title,
// usage, arguments, and flags with the package Lipgloss styles, writing
// through a colorprofile writer so colour downsamples to the terminal.
func StyledHelpPrinter() func(kong.HelpOptions, *kong.Context) error {
	return func(options kong.HelpOptions, ctx *kong.Context) error {
		var sb strings.Builder

		// Title and description
		sb.WriteString(RenderTitle())
		sb.WriteString("\n")
		sb.WriteString(helpDescStyle.Render("Professional podcast audio preprocessor"))
		sb.WriteString("\n")

		// Usage
		sb.WriteString(helpSectionStyle.Render("Usage:"))
		sb.WriteString("\n  ")
		fmt.Fprintf(&sb, "%s [flags] <files> ...", ctx.Model.Name)
		sb.WriteString("\n")

		// Arguments and Flags sections
		writeHelpSection(&sb, "Arguments:", helpArgStyle, getArguments(ctx))
		writeHelpSection(&sb, "Flags:", helpFlagStyle, getFlags(ctx))

		sb.WriteString("\n")
		fmt.Fprint(colorprofile.NewWriter(ctx.Stdout, os.Environ()), sb.String())
		return nil
	}
}

// helpRow is one label/help pair rendered in the Arguments or Flags section.
type helpRow struct {
	label string
	help  string
}

// writeHelpSection renders a help section (header plus label-styled rows) to sb,
// writing nothing when rows is empty. label is drawn with style, help follows
// after two spaces when present.
func writeHelpSection(sb *strings.Builder, header string, style lipgloss.Style, rows []helpRow) {
	if len(rows) == 0 {
		return
	}

	sb.WriteString("\n")
	sb.WriteString(helpSectionStyle.Render(header))
	sb.WriteString("\n")
	for _, row := range rows {
		sb.WriteString("  ")
		sb.WriteString(style.Render(row.label))
		if row.help != "" {
			sb.WriteString("  ")
			sb.WriteString(row.help)
		}
		sb.WriteString("\n")
	}
}

func getArguments(ctx *kong.Context) []helpRow {
	var args []helpRow

	for _, arg := range ctx.Model.Positional {
		args = append(args, helpRow{label: arg.Summary(), help: arg.Help})
	}

	return args
}

func getFlags(ctx *kong.Context) []helpRow {
	var flags []helpRow

	// Kong omits --help from Model.Flags, so prepend it by hand.
	flags = append(flags, helpRow{
		label: "-h, --help",
		help:  "Show context-sensitive help.",
	})

	// Parse flags from the model
	for _, f := range ctx.Model.Flags {
		if f.Name == "help" {
			continue // the help flag is prepended above
		}

		var flagStr string
		if f.Short != 0 {
			flagStr = fmt.Sprintf("-%c, --%s", f.Short, f.Name)
		} else {
			flagStr = fmt.Sprintf("--%s", f.Name)
		}

		if !f.IsBool() && f.PlaceHolder != "" {
			flagStr += "=" + strings.ToUpper(f.PlaceHolder)
		}

		flags = append(flags, helpRow{
			label: flagStr,
			help:  f.Help,
		})
	}

	return flags
}
