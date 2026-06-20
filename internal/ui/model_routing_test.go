package ui

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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
	if m.Files[0].Status != StatusAnalysing {
		t.Errorf("Files[0].Status = %v, want StatusAnalysing", m.Files[0].Status)
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

	updated, _ = m.Update(FileCompleteMsg{FileIndex: 1, CompletionResult: CompletionResult{InputLUFS: -23, OutputLUFS: -16, OutputPath: "b-out.wav"}})
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
		updated, _ = m.Update(FileCompleteMsg{FileIndex: idx, CompletionResult: CompletionResult{OutputPath: "x"}})
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
	// The resize builds and fills the viewport, so the file queue now renders
	// inside Update on the persistent model. Rendering populates the presentation
	// -only render caches (statusBoxCache / fileDetailsTitleCache) in place, so a
	// whole-struct equality would trip on those. Assert the routed DATA contract
	// survives instead (that is what this test guards); the render caches are
	// derived state and excluded by clearing them on both sides before comparing.
	for i := range want {
		got := m.Files[i]
		got.statusBoxCache = statusBoxCache{}
		got.fileDetailsTitleCache = overlayTitleCache{}
		wantData := want[i]
		wantData.statusBoxCache = statusBoxCache{}
		wantData.fileDetailsTitleCache = overlayTitleCache{}
		if got != wantData {
			t.Errorf("Files[%d] routed data changed after WindowSizeMsg: got %+v, want %+v", i, got, wantData)
		}
	}
	_ = m.progress.ViewAs(m.Files[0].Progress)
}

func TestWindowSizeMsgSizesViewport(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav"})

	// Before a resize the viewport is not built.
	if m.vpReady {
		t.Fatal("viewport ready before any WindowSizeMsg, want unbuilt")
	}

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)

	if !m.vpReady {
		t.Fatal("WindowSizeMsg did not build the viewport")
	}
	if m.vp.Width() != 100 {
		t.Errorf("viewport width = %d, want 100", m.vp.Width())
	}
	// Height must be the terminal height minus the rendered header, never the
	// full terminal height (the header stays pinned outside the viewport).
	headerHeight := lipgloss.Height(renderProcessingHeader(m))
	wantHeight := 30 - headerHeight
	if m.vp.Height() != wantHeight {
		t.Errorf("viewport height = %d, want %d (30 - header %d)", m.vp.Height(), wantHeight, headerHeight)
	}
	if m.vp.Height() >= 30 {
		t.Errorf("viewport height %d not reduced below terminal height 30; header not reserved", m.vp.Height())
	}

	// A second resize re-sizes the existing viewport rather than rebuilding.
	updated, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 20})
	m = updated.(Model)
	if m.vp.Width() != 80 {
		t.Errorf("viewport width after resize = %d, want 80", m.vp.Width())
	}
}

func TestQuitKeysStillQuitWithViewport(t *testing.T) {
	m := NewModel([]string{"a.wav"})
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = updated.(Model)

	keys := []tea.KeyPressMsg{
		{Text: "q", Code: 'q'},
		{Mod: tea.ModCtrl, Code: 'c'},
	}
	for _, key := range keys {
		_, cmd := m.Update(key)
		if cmd == nil {
			t.Fatalf("%q produced nil cmd, want tea.Quit", key.String())
		}
		if msg := cmd(); msg == nil {
			t.Errorf("%q cmd yielded nil msg, want a QuitMsg", key.String())
		} else if _, ok := msg.(tea.QuitMsg); !ok {
			t.Errorf("%q cmd yielded %T, want tea.QuitMsg", key.String(), msg)
		}
	}
}

// scrollableModel builds a processing model with enough queued files that the
// file queue overflows a short viewport, so scrolling has somewhere to go. It
// drives only real messages through Update, so the PERSISTENT viewport holds the
// content (the regression these tests guard: content was previously set on a
// throwaway copy in View and never reached the real viewport).
func scrollableModel(t *testing.T) Model {
	t.Helper()
	files := make([]string, 40)
	for i := range files {
		files[i] = "file.wav"
	}
	m := NewModel(files)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 12})
	return updated.(Model)
}

// TestViewportReceivesContentFromUpdate proves the file queue reaches the
// PERSISTENT viewport, not a throwaway copy in View. After a resize plus a
// routed message, the viewport's content height must exceed its own height so it
// is actually scrollable. This fails against the prior View-only SetContent,
// where the real viewport stayed empty (content height <= viewport height).
func TestViewportReceivesContentFromUpdate(t *testing.T) {
	m := scrollableModel(t)

	// A routed message goes through the content-refresh path in Update.
	updated, _ := m.Update(FileStartMsg{FileIndex: 0})
	m = updated.(Model)

	// TotalLineCount reflects the loaded content; a scrollable viewport has more
	// content lines than its own height.
	if got := m.vp.TotalLineCount(); got <= m.vp.Height() {
		t.Errorf("viewport content height = %d, want > viewport height %d (content did not reach the persistent viewport)", got, m.vp.Height())
	}
}

// TestScrollKeysForwardedToViewport confirms a pager key (PgDown) moves the
// viewport offset. Content is loaded purely by Update (the WindowSizeMsg refresh
// in scrollableModel), then GotoTop puts it at the top so PgDown has room to move
// down.
func TestScrollKeysForwardedToViewport(t *testing.T) {
	m := scrollableModel(t)
	m.vp.GotoTop()
	startOffset := m.vp.YOffset()

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = updated.(Model)

	if m.vp.YOffset() <= startOffset {
		t.Errorf("PgDown did not scroll the viewport: offset %d, want > %d", m.vp.YOffset(), startOffset)
	}
}

// TestMouseWheelMovesOffset confirms a wheel-up event moves the persistent
// viewport's scroll offset. Driven from the bottom-pinned state the Update path
// leaves after content load: a wheel up must DECREASE the offset. This fails on
// the old code path, where the real viewport had no content and the offset was
// pinned at zero with nowhere to move.
func TestMouseWheelMovesOffset(t *testing.T) {
	m := scrollableModel(t)

	// scrollableModel leaves the viewport bottom-pinned (content loaded on resize).
	startOffset := m.vp.YOffset()
	if startOffset == 0 {
		t.Fatalf("expected a non-zero bottom-pinned offset after content load, got 0 (content did not reach the persistent viewport)")
	}

	updated, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m = updated.(Model)

	if m.vp.YOffset() >= startOffset {
		t.Errorf("wheel up did not scroll up: offset %d, want < %d", m.vp.YOffset(), startOffset)
	}
}

func TestRenderOverallProgressFooter(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav", "c.wav"})

	// One complete, one failed, one in progress.
	updated, _ := m.Update(FileCompleteMsg{FileIndex: 0, CompletionResult: CompletionResult{OutputPath: "a-out.wav"}})
	m = updated.(Model)
	updated, _ = m.Update(FileCompleteMsg{FileIndex: 1, CompletionResult: CompletionResult{Error: errors.New("boom")}})
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
