package cli

import (
	"bytes"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
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
		helpFlagStyle.Render("--debug") +
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

// titleColors extracts the distinct RGB foreground triples from a styled
// string. Letters carry a bold prefix (1;38;2;r;g;b), so match the 38;2;r;g;b
// foreground regardless of any leading SGR attributes.
func titleColors(s string) [][3]int {
	var out [][3]int
	seen := map[[3]int]bool{}
	for seg := range strings.SplitSeq(s, "\x1b[") {
		_, after, found := strings.Cut(seg, "38;2;")
		if !found {
			continue
		}
		body, _, _ := strings.Cut(after, "m")
		parts := strings.Split(body, ";")
		if len(parts) < 3 {
			continue
		}
		var c [3]int
		ok := true
		for i := range 3 {
			n, err := strconv.Atoi(parts[i])
			if err != nil {
				ok = false
				break
			}
			c[i] = n
		}
		if ok && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// TestRenderTitleIsGradient confirms the shared wordmark is drawn as a
// multi-colour per-letter gradient (more than one distinct foreground) and
// never uses the brand red foreground.
func TestRenderTitleIsGradient(t *testing.T) {
	title := RenderTitle()

	if !strings.Contains(ansi.Strip(title), "Jivetalking") {
		t.Fatalf("title missing wordmark: %q", title)
	}

	colors := titleColors(title)
	if len(colors) < 2 {
		t.Errorf("expected a multi-colour title gradient, got %d colours: %v", len(colors), colors)
	}
	// Brand red (#A40000 -> 164,0,0) must not colour the title.
	if slices.Contains(colors, [3]int{164, 0, 0}) {
		t.Errorf("title contains brand red 164,0,0:\n%q", title)
	}
}

// TestRenderTitleDownsamplesNoColor confirms the wordmark emits zero colour SGR
// codes when written through a NoTTY (NO_COLOR-equivalent) profile, matching the
// colorprofile-aware output path used by PrintVersion.
func TestRenderTitleDownsamplesNoColor(t *testing.T) {
	out := renderThrough(colorprofile.NoTTY, RenderTitle())
	if strings.Contains(out, "\x1b[") {
		t.Errorf("NoTTY profile left escape sequences: %q", out)
	}
	if !strings.Contains(out, "Jivetalking") {
		t.Errorf("NoTTY profile dropped wordmark: %q", out)
	}
}

func TestStyledOutputPreservesTruecolor(t *testing.T) {
	styled := helpTitleStyle.Render("Jivetalking")
	out := renderThrough(colorprofile.TrueColor, styled)
	if !strings.Contains(out, "38;2;") {
		t.Errorf("TrueColor profile dropped truecolor: %q", out)
	}
}
