//go:build integration

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/linuxmatters/jivetalking/internal/processor"
	"github.com/linuxmatters/jivetalking/internal/ui"
)

// findPoolTestAudio locates a real testdata audio file for the pool tests,
// mirroring the processor tests' skip-if-missing convention. It returns "" when
// no audio is present so callers can t.Skipf (project convention: skip, never
// fail, when testdata/ audio is absent).
func findPoolTestAudio(t *testing.T) string {
	t.Helper()

	preferred := filepath.Join("..", "..", "testdata", "fixture-5m.flac")
	if _, err := os.Stat(preferred); err == nil {
		return preferred
	}

	matches, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "*.flac"))
	if len(matches) == 0 {
		matches, _ = filepath.Glob(filepath.Join("..", "..", "testdata", "*.wav"))
	}
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// copyFixtureTo copies src into dir under name, returning the destination path.
// Each worker needs a DISTINCT input file so it writes its own per-input output
// without cross-worker contention.
func copyFixtureTo(t *testing.T, src, dir, name string) string {
	t.Helper()

	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read fixture %s: %v", src, err)
	}
	dst := filepath.Join(dir, name)
	if err := os.WriteFile(dst, data, 0o600); err != nil { // #nosec G703 -- test fixture copy into a t.TempDir() with a test-controlled name.
		t.Fatalf("write fixture copy %s: %v", dst, err)
	}
	return dst
}

// assertProcessedOutput asserts the input produced a sibling output matching
// <name>-LUFS-NN-processed.<ext> and a matching .md report.
func assertProcessedOutput(t *testing.T, inputPath, ext string) {
	t.Helper()

	dir := filepath.Dir(inputPath)
	base := strings.TrimSuffix(filepath.Base(inputPath), ext)

	pattern := filepath.Join(dir, base+"-LUFS-*-processed"+ext)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	if len(matches) != 1 {
		entries, _ := filepath.Glob(filepath.Join(dir, "*"))
		t.Fatalf("output for %s: matched %d files for %q, want 1; dir contents: %v",
			inputPath, len(matches), pattern, entries)
	}

	mdPath := strings.TrimSuffix(matches[0], ext) + ".md"
	if _, err := os.Stat(mdPath); err != nil {
		t.Fatalf("report not found for %s: %v", inputPath, err)
	}
}

// TestRunWorkerPool_ConcurrentRaceClean drives runWorkerPool with jobs >= 2 over
// two distinct fixture copies under one shared tea.Program and one shared
// debugSink-backed logger. Run under -race it asserts no data race on the shared
// tea.Program (p.Send), the shared debugSink, or the per-worker config clones,
// and that each input produces its <name>-LUFS-NN-processed.<ext> output plus a
// matching .md report.
func TestRunWorkerPool_ConcurrentRaceClean(t *testing.T) {
	src := findPoolTestAudio(t)
	if src == "" {
		t.Skip("no audio file found under testdata/; drop a .flac (e.g. testdata/fixture-5m.flac) to run this test")
	}
	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Skipf("testdata audio not found: %s", src)
	}

	ext := filepath.Ext(src)
	dir := t.TempDir()
	files := []string{
		copyFixtureTo(t, src, dir, "worker-a"+ext),
		copyFixtureTo(t, src, dir, "worker-b"+ext),
	}

	// Shared debugSink backs the shared logger; every worker writes whole lines
	// through it concurrently, exercising the sink's serialisation under -race.
	sinkFile, err := os.CreateTemp(dir, "debug-*.log")
	if err != nil {
		t.Fatalf("create debug sink file: %v", err)
	}
	t.Cleanup(func() { sinkFile.Close() })
	sink := newDebugSink(sinkFile)
	sharedLog := sink.Logf

	base := processor.DefaultFilterConfig()
	base.SetLogger(sharedLog)

	model := ui.NewModel(files)
	// Headless program: no renderer, no input reader, so it runs cleanly under
	// `go test` with no TTY. It still quits on ui.AllCompleteMsg.
	p := tea.NewProgram(model, tea.WithoutRenderer(), tea.WithInput(nil))

	reportWarnings := make(chan string, len(files))

	// jobs == 2 so both workers run concurrently, forcing concurrent p.Send,
	// sink writes, and CloneForWorker calls.
	go runWorkerPool(context.Background(), p, files, base, sharedLog, 2, false, reportWarnings)

	if _, err := p.Run(); err != nil {
		t.Fatalf("p.Run() error = %v", err)
	}

	close(reportWarnings)
	for warning := range reportWarnings {
		t.Errorf("report warning: %s", warning)
	}

	// Each distinct input must produce its own <name>-LUFS-NN-processed.<ext>
	// output plus a matching .md report.
	for _, inputPath := range files {
		assertProcessedOutput(t, inputPath, ext)
	}
}

// TestProcessAudio_ConcurrentRaceClean launches >=2 ProcessAudio goroutines
// directly, each with its own CloneForWorker clone writing through one shared
// debugSink-backed logger, synchronised by a WaitGroup. Run under -race it
// proves the clones and the shared sink carry no data race independent of the
// tea.Program path, and that each input produces its processed output.
func TestProcessAudio_ConcurrentRaceClean(t *testing.T) {
	src := findPoolTestAudio(t)
	if src == "" {
		t.Skip("no audio file found under testdata/; drop a .flac (e.g. testdata/fixture-5m.flac) to run this test")
	}
	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Skipf("testdata audio not found: %s", src)
	}

	ext := filepath.Ext(src)
	dir := t.TempDir()
	files := []string{
		copyFixtureTo(t, src, dir, "direct-a"+ext),
		copyFixtureTo(t, src, dir, "direct-b"+ext),
	}

	sinkFile, err := os.CreateTemp(dir, "debug-*.log")
	if err != nil {
		t.Fatalf("create debug sink file: %v", err)
	}
	t.Cleanup(func() { sinkFile.Close() })
	sink := newDebugSink(sinkFile)
	sharedLog := sink.Logf

	base := processor.DefaultFilterConfig()
	base.SetLogger(sharedLog)

	var wg sync.WaitGroup
	errs := make([]error, len(files))
	results := make([]*processor.ProcessingResult, len(files))

	for i, inputPath := range files {
		wg.Add(1)
		go func(i int, inputPath string) {
			defer wg.Done()
			clone := base.CloneForWorker(withFilePrefix(inputPath, sharedLog))
			results[i], errs[i] = processor.ProcessAudio(context.Background(), inputPath, clone, nil)
		}(i, inputPath)
	}
	wg.Wait()

	for i, inputPath := range files {
		if errs[i] != nil {
			t.Fatalf("ProcessAudio(%s) error = %v", inputPath, errs[i])
		}
		if results[i] == nil || results[i].OutputPath == "" {
			t.Fatalf("ProcessAudio(%s) produced no output path", inputPath)
		}
		if _, err := os.Stat(results[i].OutputPath); err != nil {
			t.Fatalf("output not found for %s: %v", inputPath, err)
		}
	}
}

// TestRunWorkerPool_CancellationNoTempResidue exercises AC5 against REAL audio:
// the real processor creates the .processing-* / .loudnorm-* temp dotfiles, so
// the seam fake cannot stand in here. It copies a fixture into a dedicated input
// dir, launches the pool with jobs >= 2, cancels mid-run, waits for the pool to
// unwind, then lists the input dir to prove no temp dotfile residue remains.
// The invariant (zero temp residue) holds regardless of when cancel lands, so
// the test is robust to cancellation timing.
func TestRunWorkerPool_CancellationNoTempResidue(t *testing.T) {
	src := findPoolTestAudio(t)
	if src == "" {
		t.Skip("no audio file found under testdata/; drop a .flac (e.g. testdata/fixture-5m.flac) to run this test")
	}
	if _, err := os.Stat(src); os.IsNotExist(err) {
		t.Skipf("testdata audio not found: %s", src)
	}

	ext := filepath.Ext(src)
	// Dedicated input dir so the residue glob inspects exactly these files.
	dir := t.TempDir()
	const copies = 3
	files := make([]string, copies)
	for i := range files {
		files[i] = copyFixtureTo(t, src, dir, "cancel-"+string(rune('a'+i))+ext)
	}

	sinkFile, err := os.CreateTemp(dir, "debug-*.log")
	if err != nil {
		t.Fatalf("create debug sink file: %v", err)
	}
	t.Cleanup(func() { sinkFile.Close() })
	sink := newDebugSink(sinkFile)
	sharedLog := sink.Logf

	base := processor.DefaultFilterConfig()
	base.SetLogger(sharedLog)

	// Observe the first FileStartMsg, then cancel so at least one worker is
	// in-flight. AllCompleteMsg releases p.Run() once the pool has unwound.
	started := make(chan struct{}, copies)
	model := cancelObserverModel{started: started}
	p := tea.NewProgram(model, tea.WithoutRenderer(), tea.WithInput(nil))

	ctx, cancel := context.WithCancel(context.Background())
	reportWarnings := make(chan string, copies)

	poolDone := make(chan struct{})
	go func() {
		runWorkerPool(ctx, p, files, base, sharedLog, 2, false, reportWarnings)
		close(poolDone)
	}()

	// Cancel after the first worker has started (in-flight) or, defensively,
	// after a short delay so the test never wedges if no start arrives.
	go func() {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
		}
		cancel()
	}()

	if _, err := p.Run(); err != nil {
		t.Fatalf("p.Run() error = %v", err)
	}

	// Ensure the pool goroutine has fully returned (all wg.Done + deferred temp
	// cleanup) before listing the dir; mirror cancel's no-op on natural finish.
	cancel()
	<-poolDone

	close(reportWarnings)
	for warning := range reportWarnings {
		t.Logf("report warning (non-fatal): %s", warning)
	}

	// Hard AC5 requirement: zero temp dotfiles remain in the input dir.
	for _, pattern := range []string{
		filepath.Join(dir, ".processing-*.tmp.flac"),
		filepath.Join(dir, ".loudnorm-*.tmp.flac"),
		filepath.Join(dir, ".loudnorm-*.tmp.json"),
	} {
		residue, globErr := filepath.Glob(pattern)
		if globErr != nil {
			t.Fatalf("glob %s: %v", pattern, globErr)
		}
		if len(residue) != 0 {
			t.Fatalf("temp residue remains after cancel for %q: %v", pattern, residue)
		}
	}
}

// cancelObserverModel signals on the first FileStartMsg (to trigger a mid-run
// cancel) and quits p.Run() on AllCompleteMsg once the pool unwinds.
type cancelObserverModel struct {
	started chan<- struct{}
}

func (m cancelObserverModel) Init() tea.Cmd { return nil }

func (m cancelObserverModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case ui.FileStartMsg:
		select {
		case m.started <- struct{}{}:
		default:
		}
	case ui.AllCompleteMsg:
		return m, tea.Quit
	}
	return m, nil
}

func (m cancelObserverModel) View() tea.View { return tea.NewView("") }
