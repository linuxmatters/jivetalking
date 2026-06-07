package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

func TestProgressMsgIndexRouting(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav"})

	updated, _ := m.Update(ProgressMsg{FileIndex: 0, Pass: processor.PassAnalysis, Progress: 0.25})
	m = updated.(Model)
	updated, _ = m.Update(ProgressMsg{FileIndex: 1, Pass: processor.PassProcessing, Progress: 0.75})
	m = updated.(Model)

	if m.Files[0].Progress != 0.25 {
		t.Errorf("Files[0].Progress = %v, want 0.25", m.Files[0].Progress)
	}
	if m.Files[0].CurrentPass != processor.PassAnalysis {
		t.Errorf("Files[0].CurrentPass = %v, want PassAnalysis", m.Files[0].CurrentPass)
	}
	if m.Files[0].Status != StatusAnalyzing {
		t.Errorf("Files[0].Status = %v, want StatusAnalyzing", m.Files[0].Status)
	}

	if m.Files[1].Progress != 0.75 {
		t.Errorf("Files[1].Progress = %v, want 0.75", m.Files[1].Progress)
	}
	if m.Files[1].CurrentPass != processor.PassProcessing {
		t.Errorf("Files[1].CurrentPass = %v, want PassProcessing", m.Files[1].CurrentPass)
	}
	if m.Files[1].Status != StatusProcessing {
		t.Errorf("Files[1].Status = %v, want StatusProcessing", m.Files[1].Status)
	}
}

func TestFileCompleteMsgIndexRouting(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav"})

	// Put index 0 mid-process.
	updated, _ := m.Update(ProgressMsg{FileIndex: 0, Pass: processor.PassProcessing, Progress: 0.5})
	m = updated.(Model)
	before := m.Files[0]

	updated, _ = m.Update(FileCompleteMsg{FileIndex: 1, InputLUFS: -23, OutputLUFS: -16, OutputPath: "b-out.wav"})
	m = updated.(Model)

	if m.Files[1].Status != StatusComplete {
		t.Errorf("Files[1].Status = %v, want StatusComplete", m.Files[1].Status)
	}
	if m.Files[1].OutputPath != "b-out.wav" {
		t.Errorf("Files[1].OutputPath = %q, want b-out.wav", m.Files[1].OutputPath)
	}
	if m.Files[0] != before {
		t.Errorf("Files[0] changed: got %+v, want %+v", m.Files[0], before)
	}
}

func TestUpdateOutOfRangeSafety(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav"})
	want := append([]FileProgress(nil), m.Files...)
	wantMeters := append([]meterState(nil), m.meters...)

	indices := []int{-1, len(m.Files)}
	for _, idx := range indices {
		updated, _ := m.Update(ProgressMsg{FileIndex: idx, Pass: processor.PassProcessing, Progress: 0.9})
		m = updated.(Model)
		updated, _ = m.Update(FileCompleteMsg{FileIndex: idx, OutputPath: "x"})
		m = updated.(Model)
	}

	for i := range want {
		if m.Files[i] != want[i] {
			t.Errorf("Files[%d] changed after out-of-range messages: got %+v, want %+v", i, m.Files[i], want[i])
		}
	}
	for i := range wantMeters {
		if m.meters[i] != wantMeters[i] {
			t.Errorf("meters[%d] changed after out-of-range messages: got %+v, want %+v", i, m.meters[i], wantMeters[i])
		}
	}
	if m.CompletedFiles != 0 || m.FailedFiles != 0 {
		t.Errorf("counts changed: completed=%d failed=%d, want 0/0", m.CompletedFiles, m.FailedFiles)
	}
}

func TestWindowSizeMsgPreservesRoutedFiles(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav"})

	// Route progress before any resize: the seeded default width makes ViewAs safe.
	updated, _ := m.Update(ProgressMsg{FileIndex: 0, Pass: processor.PassProcessing, Progress: 0.5})
	m = updated.(Model)
	want := append([]FileProgress(nil), m.Files...)
	_ = m.progress.ViewAs(m.Files[0].Progress)

	updated, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = updated.(Model)

	if m.Width != 120 || m.Height != 40 {
		t.Errorf("dimensions not stored: Width=%d Height=%d, want 120/40", m.Width, m.Height)
	}
	for i := range want {
		if m.Files[i] != want[i] {
			t.Errorf("Files[%d] changed after WindowSizeMsg: got %+v, want %+v", i, m.Files[i], want[i])
		}
	}
	_ = m.progress.ViewAs(m.Files[0].Progress)
}

func TestRenderOverallProgressFooter(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav", "c.wav"})

	// One complete, one failed, one in progress.
	updated, _ := m.Update(FileCompleteMsg{FileIndex: 0, OutputPath: "a-out.wav"})
	m = updated.(Model)
	updated, _ = m.Update(FileCompleteMsg{FileIndex: 1, Error: errors.New("boom")})
	m = updated.(Model)
	updated, _ = m.Update(ProgressMsg{FileIndex: 2, Pass: processor.PassProcessing, Progress: 0.4})
	m = updated.(Model)

	footer := renderOverallProgress(m)

	if !strings.Contains(footer, "3") {
		t.Errorf("footer missing total count 3: %q", footer)
	}
	if !strings.Contains(footer, "1 complete") {
		t.Errorf("footer missing complete count: %q", footer)
	}
	if !strings.Contains(footer, "1 failed") {
		t.Errorf("footer missing failed count: %q", footer)
	}
	if strings.Contains(strings.ToLower(footer), "file 3 of") || strings.Contains(strings.ToLower(footer), "of 3") {
		t.Errorf("footer must not contain a 'file N of M' cursor: %q", footer)
	}
}

func TestInitStartsMeterTick(t *testing.T) {
	m := NewModel([]string{"a.wav"})

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned nil cmd, want a meter tick cmd")
	}
	if _, ok := cmd().(meterTickMsg); !ok {
		t.Errorf("Init() cmd yielded %T, want meterTickMsg", cmd())
	}
}

func TestMeterTickStepsSpringWithoutMutatingRoutedFields(t *testing.T) {
	m := NewModel([]string{"a.wav"})

	// Make the file active and give the meter a target above its start floor.
	updated, _ := m.Update(ProgressMsg{FileIndex: 0, Pass: processor.PassProcessing, Progress: 0.5, Level: -12})
	m = updated.(Model)

	wantProgress := m.Files[0].Progress
	wantStatus := m.Files[0].Status
	startPos := m.meters[0].pos
	target := m.Files[0].CurrentLevel

	updated, cmd := m.Update(meterTickMsg{})
	m = updated.(Model)

	if cmd == nil {
		t.Error("meterTickMsg returned nil cmd while a file is active, want re-scheduled tick")
	}
	// Eased: the meter moves toward the target but must not snap to it.
	if m.meters[0].pos <= startPos {
		t.Errorf("meters[0].pos = %v, want > start %v (should ease toward target)", m.meters[0].pos, startPos)
	}
	if m.meters[0].pos >= target {
		t.Errorf("meters[0].pos = %v, want < target %v (must be eased, not instantaneous)", m.meters[0].pos, target)
	}
	// Routed data contract must stay untouched by presentation-only stepping.
	if m.Files[0].Progress != wantProgress {
		t.Errorf("Files[0].Progress = %v, want unchanged %v", m.Files[0].Progress, wantProgress)
	}
	if m.Files[0].Status != wantStatus {
		t.Errorf("Files[0].Status = %v, want unchanged %v", m.Files[0].Status, wantStatus)
	}
}

func TestMeterTickStopsAfterAllComplete(t *testing.T) {
	m := NewModel([]string{"a.wav"})

	// Activate the file so a tick would normally re-schedule.
	updated, _ := m.Update(ProgressMsg{FileIndex: 0, Pass: processor.PassProcessing, Progress: 0.5, Level: -12})
	m = updated.(Model)

	updated, _ = m.Update(AllCompleteMsg{})
	m = updated.(Model)
	if !m.Done {
		t.Fatal("AllCompleteMsg did not set m.Done")
	}
	posBefore := m.meters[0].pos

	updated, cmd := m.Update(meterTickMsg{})
	m = updated.(Model)

	if cmd != nil {
		t.Error("meterTickMsg returned non-nil cmd after m.Done, want loop termination")
	}
	if m.meters[0].pos != posBefore {
		t.Errorf("meters[0].pos = %v, want unchanged %v after Done", m.meters[0].pos, posBefore)
	}
}
