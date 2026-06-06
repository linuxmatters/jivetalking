package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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

// TestRunWorkerPool_ConcurrentRaceClean drives runWorkerPool with jobs >= 2 over
// two distinct fixture copies under one shared tea.Program and one shared
// debugSink-backed logger. Run under -race it asserts no data race on the shared
// tea.Program (p.Send), the shared debugSink, or the per-worker config clones,
// and that each input produces its <name>-LUFS-NN-processed.<ext> output plus a
// matching .log report.
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
	go runWorkerPool(context.Background(), p, files, base, sharedLog, 2, reportWarnings)

	if _, err := p.Run(); err != nil {
		t.Fatalf("p.Run() error = %v", err)
	}

	close(reportWarnings)
	for warning := range reportWarnings {
		t.Errorf("report warning: %s", warning)
	}

	// Each distinct input must produce its own <name>-LUFS-NN-processed.<ext>
	// output plus a matching .log report.
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

// assertProcessedOutput asserts the input produced a sibling output matching
// <name>-LUFS-NN-processed.<ext> and a matching .log report.
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

	logPath := strings.TrimSuffix(matches[0], ext) + ".log"
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("report log not found for %s: %v", inputPath, err)
	}
}

// inflightFake substitutes poolProcessAudio to observe pool concurrency without
// real FFmpeg. It tracks live in-flight workers and the high-water mark, sleeps
// briefly to create overlap opportunity, records each processed path exactly
// once, then returns an error so runWorkerPool takes the FileCompleteMsg{Error}
// branch (no report/output path needed to drive the pool end-to-end).
type inflightFake struct {
	live    atomic.Int32
	maxSeen atomic.Int32

	mu        sync.Mutex
	processed []string
}

func (f *inflightFake) fn(_ context.Context, inputPath string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.ProcessingResult, error) {
	cur := f.live.Add(1)
	for {
		old := f.maxSeen.Load()
		if cur <= old || f.maxSeen.CompareAndSwap(old, cur) {
			break
		}
	}

	time.Sleep(5 * time.Millisecond)

	f.mu.Lock()
	f.processed = append(f.processed, inputPath)
	f.mu.Unlock()

	f.live.Add(-1)
	return nil, errors.New("inflightFake: synthetic error to drive pool error branch")
}

// installInflightFake swaps poolProcessAudio for the fake and restores it after
// the test. The seam var is test-only state; restoration keeps tests isolated.
func installInflightFake(t *testing.T, f *inflightFake) {
	t.Helper()
	orig := poolProcessAudio
	poolProcessAudio = f.fn
	t.Cleanup(func() { poolProcessAudio = orig })
}

// recordingModel is a headless tea.Model that captures pool messages and quits
// on ui.AllCompleteMsg, letting tests observe FileCompleteMsg/AllCompleteMsg
// deterministically without touching the production rendering model.
type recordingModel struct {
	mu           *sync.Mutex
	fileComplete *int
	allComplete  *bool
}

func (m recordingModel) Init() tea.Cmd { return nil }

func (m recordingModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case ui.FileCompleteMsg:
		m.mu.Lock()
		*m.fileComplete++
		m.mu.Unlock()
	case ui.AllCompleteMsg:
		m.mu.Lock()
		*m.allComplete = true
		m.mu.Unlock()
		return m, tea.Quit
	}
	return m, nil
}

func (m recordingModel) View() tea.View { return tea.NewView("") }

// runPoolWithFake drives runWorkerPool over n synthetic file paths under a
// headless recording program, returning the fake plus observed completion
// counts. It reuses 6.1's headless tea.Program setup (no renderer, nil input).
func runPoolWithFake(t *testing.T, jobs, n int) (*inflightFake, int, bool) {
	t.Helper()

	fake := &inflightFake{}
	installInflightFake(t, fake)

	dir := t.TempDir()
	files := make([]string, n)
	for i := range files {
		files[i] = filepath.Join(dir, "fake-"+string(rune('a'+i))+".flac")
	}

	var mu sync.Mutex
	fileComplete := 0
	allComplete := false
	model := recordingModel{mu: &mu, fileComplete: &fileComplete, allComplete: &allComplete}
	p := tea.NewProgram(model, tea.WithoutRenderer(), tea.WithInput(nil))

	base := processor.DefaultFilterConfig()
	reportWarnings := make(chan string, n)

	go runWorkerPool(context.Background(), p, files, base, func(string, ...any) {}, jobs, reportWarnings)

	if _, err := p.Run(); err != nil {
		t.Fatalf("p.Run() error = %v", err)
	}

	close(reportWarnings)

	mu.Lock()
	defer mu.Unlock()
	return fake, fileComplete, allComplete
}

// TestRunWorkerPool_InFlightBoundedToOne asserts jobs == 1 holds in-flight
// workers to a single concurrent ProcessAudio call. The fake records the
// high-water in-flight mark across 5 files; with jobs == 1 it must never exceed
// 1, proving serial execution under the pool.
func TestRunWorkerPool_InFlightBoundedToOne(t *testing.T) {
	fake, fileComplete, allComplete := runPoolWithFake(t, 1, 5)

	if got := fake.maxSeen.Load(); got != 1 {
		t.Fatalf("max in-flight with jobs=1 = %d, want 1", got)
	}
	if fileComplete != 5 {
		t.Fatalf("FileCompleteMsg count = %d, want 5", fileComplete)
	}
	if !allComplete {
		t.Fatal("AllCompleteMsg did not fire")
	}
}

// TestRunWorkerPool_BoundHonouredForN asserts jobs == 3 caps in-flight workers
// at 3 over 8 files while still reaching real concurrency (>1), proving the
// semaphore both bounds and permits parallelism.
func TestRunWorkerPool_BoundHonouredForN(t *testing.T) {
	fake, fileComplete, allComplete := runPoolWithFake(t, 3, 8)

	maxSeen := fake.maxSeen.Load()
	if maxSeen < 2 || maxSeen > 3 {
		t.Fatalf("max in-flight with jobs=3 = %d, want in (1,3]", maxSeen)
	}
	if fileComplete != 8 {
		t.Fatalf("FileCompleteMsg count = %d, want 8", fileComplete)
	}
	if !allComplete {
		t.Fatal("AllCompleteMsg did not fire")
	}
}

// isolationFake substitutes poolProcessAudio so exactly one designated input
// path errors while every sibling succeeds. Successful calls return a
// ProcessingResult whose OutputPath sits next to the (synthetic) input so the
// pool's GenerateReport call writes its .log without a report warning. It mirrors
// AC4: one failing input must leave siblings unaffected.
type isolationFake struct {
	failPath string
}

func (f *isolationFake) fn(_ context.Context, inputPath string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.ProcessingResult, error) {
	if inputPath == f.failPath {
		return nil, errors.New("isolationFake: synthetic unreadable input")
	}
	// Derive a sibling output path so GenerateReport writes its .log cleanly.
	outputPath := strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + "-LUFS-16-processed" + filepath.Ext(inputPath)
	if err := os.WriteFile(outputPath, []byte("synthetic"), 0o600); err != nil {
		return nil, err
	}
	return &processor.ProcessingResult{
		OutputPath: outputPath,
		InputLUFS:  -23.0,
		OutputLUFS: -16.0,
		NoiseFloor: -60.0,
	}, nil
}

// isolationModel records per-file completion detail: how many FileCompleteMsg
// arrived, which file indices carried an Error, and whether AllCompleteMsg fired.
// It is a richer sibling of recordingModel for AC4 assertions.
type isolationModel struct {
	mu          *sync.Mutex
	completed   *int
	erroredIdx  *map[int]bool
	allComplete *bool
}

func (m isolationModel) Init() tea.Cmd { return nil }

func (m isolationModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case ui.FileCompleteMsg:
		m.mu.Lock()
		*m.completed++
		if v.Error != nil {
			(*m.erroredIdx)[v.FileIndex] = true
		}
		m.mu.Unlock()
	case ui.AllCompleteMsg:
		m.mu.Lock()
		*m.allComplete = true
		m.mu.Unlock()
		return m, tea.Quit
	}
	return m, nil
}

func (m isolationModel) View() tea.View { return tea.NewView("") }

// TestRunWorkerPool_FailureIsolation drives the pool over several files where one
// designated input errors and the rest succeed. It asserts AC4: every sibling
// completes with no error, the failing file's FileCompleteMsg carries its Error,
// AllCompleteMsg still fires (the partial-failure run completes), and all N files
// report exactly once (no early abort). Seam-based, so no real audio is needed.
func TestRunWorkerPool_FailureIsolation(t *testing.T) {
	const n = 5
	const failIdx = 2

	dir := t.TempDir()
	files := make([]string, n)
	for i := range files {
		files[i] = filepath.Join(dir, "iso-"+string(rune('a'+i))+".flac")
	}

	fake := &isolationFake{failPath: files[failIdx]}
	orig := poolProcessAudio
	poolProcessAudio = fake.fn
	t.Cleanup(func() { poolProcessAudio = orig })

	var mu sync.Mutex
	completed := 0
	erroredIdx := map[int]bool{}
	allComplete := false
	model := isolationModel{mu: &mu, completed: &completed, erroredIdx: &erroredIdx, allComplete: &allComplete}
	p := tea.NewProgram(model, tea.WithoutRenderer(), tea.WithInput(nil))

	base := processor.DefaultFilterConfig()
	reportWarnings := make(chan string, n)

	go runWorkerPool(context.Background(), p, files, base, func(string, ...any) {}, 3, reportWarnings)

	if _, err := p.Run(); err != nil {
		t.Fatalf("p.Run() error = %v", err)
	}

	close(reportWarnings)
	for warning := range reportWarnings {
		t.Errorf("unexpected report warning: %s", warning)
	}

	mu.Lock()
	defer mu.Unlock()

	if completed != n {
		t.Fatalf("FileCompleteMsg count = %d, want %d (every file reports exactly once)", completed, n)
	}
	if !allComplete {
		t.Fatal("AllCompleteMsg did not fire on a partial-failure run")
	}
	if !erroredIdx[failIdx] {
		t.Fatalf("failing file index %d did not carry an Error in its FileCompleteMsg", failIdx)
	}
	if len(erroredIdx) != 1 {
		t.Fatalf("errored file indices = %v, want only {%d} (siblings must be unaffected)", erroredIdx, failIdx)
	}

	// Each sibling must have produced its output (proof it ran to completion).
	for i, path := range files {
		if i == failIdx {
			continue
		}
		out := strings.TrimSuffix(path, filepath.Ext(path)) + "-LUFS-16-processed" + filepath.Ext(path)
		if _, err := os.Stat(out); err != nil {
			t.Fatalf("sibling %s did not produce output: %v", path, err)
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
		runWorkerPool(ctx, p, files, base, sharedLog, 2, reportWarnings)
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

// TestRunWorkerPool_SerialParityJobs1 asserts jobs == 1 yields the serial
// outcome: every submitted file is processed exactly once, every file emits a
// FileCompleteMsg, and AllCompleteMsg fires. Parity is proven by the fake's
// per-path record matching the submission set with no duplicates or omissions.
func TestRunWorkerPool_SerialParityJobs1(t *testing.T) {
	const n = 5
	fake, fileComplete, allComplete := runPoolWithFake(t, 1, n)

	if len(fake.processed) != n {
		t.Fatalf("processed %d files, want %d", len(fake.processed), n)
	}
	seen := make(map[string]int, n)
	for _, p := range fake.processed {
		seen[p]++
	}
	for p, count := range seen {
		if count != 1 {
			t.Fatalf("file %s processed %d times, want exactly 1", p, count)
		}
	}
	if len(seen) != n {
		t.Fatalf("distinct files processed = %d, want %d", len(seen), n)
	}
	if fileComplete != n {
		t.Fatalf("FileCompleteMsg count = %d, want %d", fileComplete, n)
	}
	if !allComplete {
		t.Fatal("AllCompleteMsg did not fire")
	}
}

// TestLaunchWorkerPool_DoneClosesAfterPoolUnwinds proves main()'s wiring: the
// channel launchWorkerPool returns must stay open while workers run and close
// only after the pool fully unwinds. Were main() not to wait on it, the process
// could exit before workers' deferred temp cleanup ran. The fake gates on a
// release channel so the test observes the not-yet-closed state deterministically
// before letting the worker finish.
func TestLaunchWorkerPool_DoneClosesAfterPoolUnwinds(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once

	orig := poolProcessAudio
	poolProcessAudio = func(_ context.Context, _ string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.ProcessingResult, error) {
		once.Do(func() { close(started) })
		<-release
		return nil, errors.New("synthetic error to drive pool error branch")
	}
	t.Cleanup(func() { poolProcessAudio = orig })

	model := recordingModel{mu: &sync.Mutex{}, fileComplete: new(int), allComplete: new(bool)}
	p := tea.NewProgram(model, tea.WithoutRenderer(), tea.WithInput(nil))
	go func() {
		if _, err := p.Run(); err != nil {
			t.Errorf("p.Run() error = %v", err)
		}
	}()

	dir := t.TempDir()
	files := []string{filepath.Join(dir, "fake.flac")}
	base := processor.DefaultFilterConfig()
	reportWarnings := make(chan string, len(files))

	done := launchWorkerPool(context.Background(), p, files, base, func(string, ...any) {}, 1, reportWarnings)

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never started")
	}
	select {
	case <-done:
		t.Fatal("done closed while a worker was still in-flight")
	default:
	}

	close(release)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("done did not close after the pool unwound")
	}

	p.Quit()
	p.Wait()
}

// TestLaunchWorkerPool_DoneClosesOnPreCancelledContext proves the wait main()
// performs cannot wedge: with an already-cancelled context every worker either
// skips at the acquire-time ctx.Done() select or runs and returns, so every
// wg.Done() fires and launchWorkerPool's channel closes promptly. The fake
// returns an error so any worker that does win the acquire race takes the pool's
// error branch cleanly rather than the nil-result success path.
func TestLaunchWorkerPool_DoneClosesOnPreCancelledContext(t *testing.T) {
	orig := poolProcessAudio
	poolProcessAudio = func(_ context.Context, _ string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.ProcessingResult, error) {
		return nil, errors.New("synthetic error to drive pool error branch")
	}
	t.Cleanup(func() { poolProcessAudio = orig })

	model := recordingModel{mu: &sync.Mutex{}, fileComplete: new(int), allComplete: new(bool)}
	p := tea.NewProgram(model, tea.WithoutRenderer(), tea.WithInput(nil))
	go func() {
		if _, err := p.Run(); err != nil {
			t.Errorf("p.Run() error = %v", err)
		}
	}()

	dir := t.TempDir()
	files := []string{filepath.Join(dir, "a.flac"), filepath.Join(dir, "b.flac")}
	base := processor.DefaultFilterConfig()
	reportWarnings := make(chan string, len(files))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := launchWorkerPool(ctx, p, files, base, func(string, ...any) {}, 1, reportWarnings)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("done did not close on pre-cancelled context")
	}

	p.Quit()
	p.Wait()
}
