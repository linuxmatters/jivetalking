package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// hasBlueFill reports whether any truecolor SGR sequence in the rendered bar
// carries a blue component above the red and green components, which would
// betray the bubbles default purple/blue gradient leaking into the fill.
func hasBlueFill(s string) bool {
	for seg := range strings.SplitSeq(s, "\x1b[") {
		if !strings.HasPrefix(seg, "38;2;") {
			continue
		}
		body, _, _ := strings.Cut(seg, "m")
		parts := strings.Split(body, ";")
		if len(parts) < 5 {
			continue
		}
		r, err1 := strconv.Atoi(parts[2])
		g, err2 := strconv.Atoi(parts[3])
		b, err3 := strconv.Atoi(parts[4])
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		if b > r && b > g && b > 40 {
			return true
		}
	}
	return false
}

func TestProgressFillIsBrandRed(t *testing.T) {
	p := newProgressModel()
	p.SetWidth(40)
	out := p.ViewAs(0.5)
	if hasBlueFill(out) {
		t.Errorf("progress fill contains blue bytes (default gradient leaked):\n%q", out)
	}
}

func TestProcessingProgressWidthFitsTerminal(t *testing.T) {
	for _, term := range []int{20, 40, 80, 120, 200} {
		m := NewModel([]string{"a.wav"})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: term, Height: 24})
		m = updated.(Model)

		w := m.progress.Width()
		if w < minProgressWidth || w > maxProgressWidth {
			t.Errorf("term=%d progress width %d out of [%d,%d]", term, w, minProgressWidth, maxProgressWidth)
		}

		// Box outer width = bar width + border(2) + padding(2). It must not
		// exceed the terminal unless the bar hit its minimum floor on a narrow
		// terminal.
		box := w + 4
		if box > term && w > minProgressWidth {
			t.Errorf("term=%d box width %d overflows terminal", term, box)
		}
	}
}

func TestAnalysisProgressWidthFitsTerminal(t *testing.T) {
	for _, term := range []int{20, 40, 80, 120, 200} {
		m := NewAnalysisModel([]string{"a.wav"})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: term, Height: 24})
		m = updated.(AnalysisModel)

		w := m.progress.Width()
		if w < minProgressWidth || w > maxProgressWidth {
			t.Errorf("term=%d analysis progress width %d out of [%d,%d]", term, w, minProgressWidth, maxProgressWidth)
		}
	}
}

// TestProcessingRowFitsTerminal renders the full active file detail box and
// asserts no line exceeds the terminal width.
func TestProcessingRowFitsTerminal(t *testing.T) {
	for _, term := range []int{40, 80, 120} {
		m := NewModel([]string{"recording.wav"})
		updated, _ := m.Update(tea.WindowSizeMsg{Width: term, Height: 24})
		m = updated.(Model)
		updated, _ = m.Update(ProgressMsg{FileIndex: 0, Pass: 2, PassName: "Processing", Progress: 0.5})
		m = updated.(Model)

		row := renderFileDetails(m.Files[0], m.progress, -20.0)
		for line := range strings.SplitSeq(row, "\n") {
			if w := ansi.StringWidth(line); w > term {
				t.Errorf("term=%d line width %d overflows:\n%q", term, w, line)
			}
		}
	}
}
