package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

// renderThrough writes the styled string through a colorprofile.Writer pinned to
// the given profile, mirroring the CLI output layer (help.go / styles.go), and
// returns the downsampled bytes.
func renderThrough(profile colorprofile.Profile, styled string) string {
	var buf bytes.Buffer
	w := &colorprofile.Writer{Forward: &buf, Profile: profile}
	if _, err := w.Write([]byte(styled)); err != nil {
		panic(err)
	}
	return buf.String()
}

func TestStyledOutputDownsamplesNoTruecolorLeak(t *testing.T) {
	styled := helpTitleStyle.Render("Jivetalking") +
		helpFlagStyle.Render("--jobs") +
		ErrorStyle.Render("Error:")

	if !strings.Contains(styled, "38;2;") {
		t.Fatalf("precondition failed: styles did not emit truecolor; got %q", styled)
	}

	cases := []struct {
		name    string
		profile colorprofile.Profile
	}{
		{"NoTTY", colorprofile.NoTTY},
		{"ASCII", colorprofile.ASCII},
		{"ANSI", colorprofile.ANSI},
		{"ANSI256", colorprofile.ANSI256},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := renderThrough(tc.profile, styled)
			if strings.Contains(out, "38;2;") {
				t.Errorf("%s profile leaked truecolor: %q", tc.name, out)
			}
		})
	}
}

func TestStyledOutputStripsColorButKeepsTextWhenNoTTY(t *testing.T) {
	styled := ErrorStyle.Render("Error:")
	out := renderThrough(colorprofile.NoTTY, styled)
	if strings.Contains(out, "\x1b[") {
		t.Errorf("NoTTY profile left escape sequences: %q", out)
	}
	if !strings.Contains(out, "Error:") {
		t.Errorf("NoTTY profile dropped text: %q", out)
	}
}

func TestStyledOutputPreservesTruecolor(t *testing.T) {
	styled := helpTitleStyle.Render("Jivetalking")
	out := renderThrough(colorprofile.TrueColor, styled)
	if !strings.Contains(out, "38;2;") {
		t.Errorf("TrueColor profile dropped truecolor: %q", out)
	}
}
