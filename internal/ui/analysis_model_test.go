package ui

import (
	"errors"
	"testing"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

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

func TestAnalysisInitStartsSpinner(t *testing.T) {
	m := NewAnalysisModel([]string{"a.wav"})

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned nil cmd, want spinner tick cmd")
	}
	if isQuitCmd(cmd) {
		t.Error("Init returned a quit cmd, want non-quit spinner tick")
	}
	if _, ok := cmd().(spinner.TickMsg); !ok {
		t.Errorf("Init cmd yielded %T, want spinner.TickMsg", cmd())
	}
}

func TestAnalysisSpinnerTickAdvancesWithoutQuitting(t *testing.T) {
	m := NewAnalysisModel([]string{"a.wav"})
	before := m.spinner.View()
	files := append([]analysisFileState(nil), m.Files...)

	// A zero-value TickMsg (ID 0, tag 0) is accepted by the spinner and
	// advances one frame, returning a follow-up tick cmd.
	updated, cmd := m.Update(spinner.TickMsg{})
	m = updated.(AnalysisModel)

	if isQuitCmd(cmd) {
		t.Error("spinner.TickMsg returned a quit cmd, want non-quit follow-up tick")
	}
	if cmd == nil {
		t.Error("spinner.TickMsg returned nil cmd, want follow-up tick")
	}
	if m.spinner.View() == before {
		t.Errorf("spinner view unchanged after tick: %q", before)
	}
	for i := range files {
		if m.Files[i] != files[i] {
			t.Errorf("Files[%d] changed on spinner tick: got %+v, want %+v", i, m.Files[i], files[i])
		}
	}
	if m.Done {
		t.Error("Done = true after spinner tick, want false")
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
