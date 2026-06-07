package ui

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// ansiRE strips ANSI escape sequences so view assertions match on plain text.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

func TestAnalysisProgressMsgIndexRouting(t *testing.T) {
	m := NewAnalysisModel([]string{"a.wav", "b.wav"})
	before := m.Files[0]

	updated, _ := m.Update(AnalysisProgressMsg{FileIndex: 1, Progress: 0.75, Level: -12.5})
	m = updated.(AnalysisModel)

	if m.Files[1].Progress != 0.75 {
		t.Errorf("Files[1].Progress = %v, want 0.75", m.Files[1].Progress)
	}
	if m.Files[1].Level != -12.5 {
		t.Errorf("Files[1].Level = %v, want -12.5", m.Files[1].Level)
	}
	if m.Files[0] != before {
		t.Errorf("Files[0] changed: got %+v, want %+v", m.Files[0], before)
	}
}

func TestAnalysisWindowSizeMsgPreservesRoutedFiles(t *testing.T) {
	m := NewAnalysisModel([]string{"a.wav", "b.wav"})

	// Route progress before any resize: the seeded default width makes ViewAs safe.
	updated, _ := m.Update(AnalysisProgressMsg{FileIndex: 1, Progress: 0.75, Level: -12.5})
	m = updated.(AnalysisModel)
	want := append([]analysisFileState(nil), m.Files...)
	_ = m.progress.ViewAs(m.Files[1].Progress)

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(AnalysisModel)

	for i := range want {
		if m.Files[i] != want[i] {
			t.Errorf("Files[%d] changed after WindowSizeMsg: got %+v, want %+v", i, m.Files[i], want[i])
		}
	}
	_ = m.progress.ViewAs(m.Files[1].Progress)
}

func TestAnalysisCompleteMsgCounts(t *testing.T) {
	m := NewAnalysisModel([]string{"a.wav", "b.wav"})

	// Success increments CompletedFiles, not FailedFiles.
	updated, _ := m.Update(AnalysisCompleteMsg{FileIndex: 0})
	m = updated.(AnalysisModel)
	if m.CompletedFiles != 1 {
		t.Errorf("CompletedFiles = %d, want 1", m.CompletedFiles)
	}
	if m.FailedFiles != 0 {
		t.Errorf("FailedFiles = %d, want 0", m.FailedFiles)
	}
	if !m.Files[0].Done {
		t.Error("Files[0].Done = false, want true")
	}

	// Error increments FailedFiles and records the error on the slot.
	wantErr := errors.New("boom")
	updated, _ = m.Update(AnalysisCompleteMsg{FileIndex: 1, Error: wantErr})
	m = updated.(AnalysisModel)
	if m.FailedFiles != 1 {
		t.Errorf("FailedFiles = %d, want 1", m.FailedFiles)
	}
	if m.CompletedFiles != 1 {
		t.Errorf("CompletedFiles = %d, want 1 (unchanged)", m.CompletedFiles)
	}
	if !errors.Is(m.Files[1].Err, wantErr) {
		t.Errorf("Files[1].Err = %v, want %v", m.Files[1].Err, wantErr)
	}
}

func TestAnalysisQuitOnlyOnAllComplete(t *testing.T) {
	m := NewAnalysisModel([]string{"a.wav", "b.wav"})

	// Per-file completion must NOT quit.
	updated, cmd := m.Update(AnalysisCompleteMsg{FileIndex: 0})
	m = updated.(AnalysisModel)
	if isQuitCmd(cmd) {
		t.Error("per-file AnalysisCompleteMsg returned a quit cmd, want non-quit")
	}

	updated, cmd = m.Update(AnalysisCompleteMsg{FileIndex: 1, Error: errors.New("boom")})
	m = updated.(AnalysisModel)
	if isQuitCmd(cmd) {
		t.Error("failed per-file AnalysisCompleteMsg returned a quit cmd, want non-quit")
	}

	// AllCompleteMsg must quit and mark the model done.
	updated, cmd = m.Update(AllCompleteMsg{})
	m = updated.(AnalysisModel)
	if !isQuitCmd(cmd) {
		t.Error("AllCompleteMsg did not return a quit cmd, want quit")
	}
	if !m.Done {
		t.Error("Done = false after AllCompleteMsg, want true")
	}
}

func TestAnalysisInitReturnsNil(t *testing.T) {
	m := NewAnalysisModel([]string{"a.wav"})

	if cmd := m.Init(); cmd != nil {
		t.Errorf("Init returned non-nil cmd %T, want nil (spinner removed)", cmd())
	}
}

// Without a spinner tick loop, re-renders are driven by progress/complete
// messages. Confirm those still update state and the rendered view.
func TestAnalysisMessagesDriveViewWithoutSpinner(t *testing.T) {
	m := NewAnalysisModel([]string{"a.wav"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(AnalysisModel)

	updated, _ = m.Update(AnalysisProgressMsg{FileIndex: 0, Progress: 0.5, Level: -12.5})
	m = updated.(AnalysisModel)
	if m.Files[0].Progress != 0.5 || m.Files[0].Level != -12.5 {
		t.Errorf("progress msg did not update state: %+v", m.Files[0])
	}

	view := stripANSI(m.View().Content)
	if !strings.Contains(view, "∿") {
		t.Errorf("active row missing orange wave glyph ∿:\n%s", view)
	}
	if !strings.Contains(view, "Level: -12.5 ㏈") {
		t.Errorf("level line missing ㏈ unit:\n%s", view)
	}

	updated, _ = m.Update(AnalysisCompleteMsg{FileIndex: 0})
	m = updated.(AnalysisModel)
	if !m.Files[0].Done {
		t.Error("complete msg did not mark file done")
	}
	view = stripANSI(m.View().Content)
	if !strings.Contains(view, "Analysed") {
		t.Errorf("completed row missing 'Analysed':\n%s", view)
	}
}

// TestAnalysisViewLayout checks the header gradient title, absent subtitle, and
// status box ordering above the file list.
func TestAnalysisViewLayout(t *testing.T) {
	m := NewAnalysisModel([]string{"a.wav", "b.wav", "c.wav"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(AnalysisModel)

	// a.wav active with a level; b.wav done; c.wav errored.
	updated, _ = m.Update(AnalysisProgressMsg{FileIndex: 0, Progress: 0.4, Level: -9.0})
	m = updated.(AnalysisModel)
	updated, _ = m.Update(AnalysisCompleteMsg{FileIndex: 1})
	m = updated.(AnalysisModel)
	updated, _ = m.Update(AnalysisCompleteMsg{FileIndex: 2, Error: errors.New("boom")})
	m = updated.(AnalysisModel)

	view := stripANSI(m.View().Content)

	if !strings.Contains(view, "Jivetalking") {
		t.Errorf("view missing gradient title 'Jivetalking':\n%s", view)
	}
	if strings.Contains(view, "Analysis Mode") {
		t.Errorf("view still contains dropped subtitle 'Analysis Mode':\n%s", view)
	}

	boxIdx := strings.Index(view, "Analysing")
	listIdx := strings.Index(view, "a.wav")
	if boxIdx < 0 {
		t.Fatalf("status box text not found:\n%s", view)
	}
	if listIdx < 0 {
		t.Fatalf("file list not found:\n%s", view)
	}
	if boxIdx > listIdx {
		t.Errorf("status box (%d) renders after file list (%d), want before:\n%s", boxIdx, listIdx, view)
	}
	if !strings.Contains(view, "∿") {
		t.Errorf("active row missing ∿ glyph:\n%s", view)
	}
	if !strings.Contains(view, "🗸") {
		t.Errorf("done row missing 🗸 icon:\n%s", view)
	}
	if !strings.Contains(view, "✗") {
		t.Errorf("error row missing ✗ icon:\n%s", view)
	}
}

func TestAnalysisUpdateOutOfRangeSafety(t *testing.T) {
	m := NewAnalysisModel([]string{"a.wav", "b.wav"})
	want := append([]analysisFileState(nil), m.Files...)

	indices := []int{-1, len(m.Files)}
	for _, idx := range indices {
		updated, _ := m.Update(AnalysisStartMsg{FileIndex: idx, FilePath: "x.wav"})
		m = updated.(AnalysisModel)
		updated, _ = m.Update(AnalysisProgressMsg{FileIndex: idx, Progress: 0.9, Level: -3})
		m = updated.(AnalysisModel)
		updated, _ = m.Update(AnalysisCompleteMsg{FileIndex: idx, Error: errors.New("boom")})
		m = updated.(AnalysisModel)
	}

	for i := range want {
		if m.Files[i] != want[i] {
			t.Errorf("Files[%d] changed after out-of-range messages: got %+v, want %+v", i, m.Files[i], want[i])
		}
	}
	if m.CompletedFiles != 0 || m.FailedFiles != 0 {
		t.Errorf("counts changed: completed=%d failed=%d, want 0/0", m.CompletedFiles, m.FailedFiles)
	}
}

// isQuitCmd reports whether cmd, when invoked, yields a tea.QuitMsg.
func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}
