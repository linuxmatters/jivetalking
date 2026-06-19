package cli

import (
	"strings"
	"testing"
)

// TestWriteHelpSectionRendersRows asserts the shared section writer produces the
// header, the two-space indent, the styled label, the two-space help separator,
// and a trailing newline per row, matching the prior per-section render loops.
func TestWriteHelpSectionRendersRows(t *testing.T) {
	var sb strings.Builder
	writeHelpSection(&sb, "Flags:", helpFlagStyle, []helpRow{
		{label: "-h, --help", help: "Show context-sensitive help."},
		{label: "--debug", help: ""},
	})

	got := sb.String()
	want := "\n" +
		helpSectionStyle.Render("Flags:") + "\n" +
		"  " + helpFlagStyle.Render("-h, --help") + "  Show context-sensitive help.\n" +
		"  " + helpFlagStyle.Render("--debug") + "\n"

	if got != want {
		t.Errorf("writeHelpSection mismatch:\ngot:  %q\nwant: %q", got, want)
	}
}

// TestWriteHelpSectionEmptyRowsWritesNothing confirms an empty row slice yields no
// output, matching the prior len(rows) > 0 guard around each section.
func TestWriteHelpSectionEmptyRowsWritesNothing(t *testing.T) {
	var sb strings.Builder
	writeHelpSection(&sb, "Arguments:", helpArgStyle, nil)
	if got := sb.String(); got != "" {
		t.Errorf("expected no output for empty rows, got %q", got)
	}
}
