package cli

import (
	"fmt"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/charmbracelet/lipgloss"
)

// Custom help styles
var (
	helpTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("#A40000")).
			MarginBottom(1)

	helpDescStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFA500")).
			Italic(true).
			MarginBottom(1)

	helpSectionStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("#FFA500")).
				MarginTop(1)

	helpFlagStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00AA00")).
			Bold(true)

	helpArgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#00AAAA")).
			Bold(true)

	helpDefaultStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888888")).
				Italic(true)
)

// StyledHelpPrinter creates a custom help printer with Lipgloss styling
func StyledHelpPrinter(options kong.HelpOptions) func(options kong.HelpOptions, ctx *kong.Context) error {
	return func(options kong.HelpOptions, ctx *kong.Context) error {
		var sb strings.Builder

		// Title and description
		sb.WriteString(helpTitleStyle.Render("Jivetalking 🎙"))
		sb.WriteString("\n")
		sb.WriteString(helpDescStyle.Render("Professional podcast audio preprocessor"))
		sb.WriteString("\n")

		// Usage
		sb.WriteString(helpSectionStyle.Render("Usage:"))
		sb.WriteString("\n  ")
		sb.WriteString(fmt.Sprintf("%s [flags] <files> ...", ctx.Model.Name))
		sb.WriteString("\n")

		// Arguments section
		args := getArguments(ctx)
		if len(args) > 0 {
			sb.WriteString("\n")
			sb.WriteString(helpSectionStyle.Render("Arguments:"))
			sb.WriteString("\n")
			for _, arg := range args {
				sb.WriteString("  ")
				sb.WriteString(helpArgStyle.Render(arg.name))
				if arg.help != "" {
					sb.WriteString("  ")
					sb.WriteString(arg.help)
				}
				sb.WriteString("\n")
			}
		}

		// Flags section
		flags := getFlags(ctx)
		if len(flags) > 0 {
			sb.WriteString("\n")
			sb.WriteString(helpSectionStyle.Render("Flags:"))
			sb.WriteString("\n")
			for _, flag := range flags {
				sb.WriteString("  ")
				sb.WriteString(helpFlagStyle.Render(flag.flags))
				if flag.help != "" {
					sb.WriteString("  ")
					sb.WriteString(flag.help)
				}
				if flag.defaultVal != "" {
					sb.WriteString(" ")
					sb.WriteString(helpDefaultStyle.Render("(default: " + flag.defaultVal + ")"))
				}
				sb.WriteString("\n")
			}
		}

		sb.WriteString("\n")
		fmt.Fprint(ctx.Stdout, sb.String())
		return nil
	}
}

type argument struct {
	name string
	help string
}

type flag struct {
	flags      string
	help       string
	defaultVal string
}

func getArguments(ctx *kong.Context) []argument {
	var args []argument

	// Parse arguments from the model
	for _, arg := range ctx.Model.Node.Positional {
		name := arg.Summary()
		help := arg.Help
		args = append(args, argument{name: name, help: help})
	}

	return args
}

func getFlags(ctx *kong.Context) []flag {
	var flags []flag

	// Always include help flag
	flags = append(flags, flag{
		flags: "-h, --help",
		help:  "Show context-sensitive help.",
	})

	// Parse flags from the model
	for _, f := range ctx.Model.Node.Flags {
		if f.Name == "help" {
			continue // Already added
		}

		flagStr := ""
		if f.Short != 0 {
			flagStr = fmt.Sprintf("-%c, --%s", f.Short, f.Name)
		} else {
			flagStr = fmt.Sprintf("--%s", f.Name)
		}

		if !f.IsBool() && f.PlaceHolder != "" {
			flagStr += "=" + strings.ToUpper(f.PlaceHolder)
		}

		flags = append(flags, flag{
			flags:      flagStr,
			help:       f.Help,
			defaultVal: f.FormatPlaceHolder(),
		})
	}

	return flags
}
