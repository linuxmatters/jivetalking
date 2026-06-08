package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/logging"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

func TestOpenDebugLog_DisabledReturnsNilWithoutCreatingFile(t *testing.T) {
	t.Chdir(t.TempDir())

	originalCreate := createDebugLogFile
	t.Cleanup(func() {
		createDebugLogFile = originalCreate
	})

	createDebugLogFile = func(string) (*os.File, error) {
		t.Fatal("createDebugLogFile should not be called when debug logging is disabled")
		return nil, nil
	}

	logFile, err := openDebugLog(false)
	if err != nil {
		t.Fatalf("openDebugLog(false) error = %v, want nil", err)
	}
	if logFile != nil {
		t.Fatalf("openDebugLog(false) file = %v, want nil", logFile)
	}
	if _, err := os.Stat(debugLogPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("debug log stat error = %v, want os.ErrNotExist", err)
	}
}

func TestOpenDebugLog_EnabledCreatesLogFile(t *testing.T) {
	t.Chdir(t.TempDir())

	logFile, err := openDebugLog(true)
	if err != nil {
		t.Fatalf("openDebugLog(true) error = %v, want nil", err)
	}
	if logFile == nil {
		t.Fatal("openDebugLog(true) file = nil, want open file")
	}
	if _, err := logFile.WriteString("debug line\n"); err != nil {
		t.Fatalf("write debug log: %v", err)
	}
	if err := logFile.Close(); err != nil {
		t.Fatalf("close debug log: %v", err)
	}

	contents, err := os.ReadFile(debugLogPath)
	if err != nil {
		t.Fatalf("read debug log: %v", err)
	}
	if string(contents) != "debug line\n" {
		t.Fatalf("debug log contents = %q, want %q", contents, "debug line\n")
	}
}

func TestOpenDebugLog_CreateFailureIncludesPath(t *testing.T) {
	sentinel := errors.New("create failed")
	originalCreate := createDebugLogFile
	t.Cleanup(func() {
		createDebugLogFile = originalCreate
	})

	var gotPath string
	createDebugLogFile = func(path string) (*os.File, error) {
		gotPath = path
		return nil, sentinel
	}

	logFile, err := openDebugLog(true)
	if logFile != nil {
		t.Fatalf("openDebugLog(true) file = %v, want nil", logFile)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("openDebugLog(true) error = %v, want sentinel wrapped", err)
	}
	if gotPath != debugLogPath {
		t.Fatalf("createDebugLogFile path = %q, want %q", gotPath, debugLogPath)
	}
	if !strings.Contains(err.Error(), debugLogPath) {
		t.Fatalf("openDebugLog(true) error = %q, want path %q", err, debugLogPath)
	}
}

func TestProgressCallbackBoundariesUseProcessorEvent(t *testing.T) {
	progressCallbackType := reflect.TypeFor[processor.ProgressCallback]()
	progressUpdateType := reflect.TypeFor[processor.ProgressUpdate]()

	depsType := reflect.TypeFor[analysisOnlyDeps]()
	analyzeDetailed, ok := depsType.FieldByName("analyzeDetailed")
	if !ok {
		t.Fatal("analysisOnlyDeps has no analyzeDetailed field")
	}
	if analyzeDetailed.Type.Kind() != reflect.Func {
		t.Fatalf("analysisOnlyDeps.analyzeDetailed = %s, want func", analyzeDetailed.Type)
	}
	if analyzeDetailed.Type.NumIn() != 4 {
		t.Fatalf("analysisOnlyDeps.analyzeDetailed has %d parameters, want 4", analyzeDetailed.Type.NumIn())
	}
	if analyzeDetailed.Type.In(3) != progressCallbackType {
		t.Fatalf("analysisOnlyDeps.analyzeDetailed progress callback = %s, want %s",
			analyzeDetailed.Type.In(3), progressCallbackType)
	}

	callbackType := reflect.TypeOf((&progressHandler{}).callback)
	if callbackType.NumIn() != 1 {
		t.Fatalf("progressHandler.callback has %d parameters, want 1", callbackType.NumIn())
	}
	if callbackType.In(0) != progressUpdateType {
		t.Fatalf("progressHandler.callback parameter = %s, want %s",
			callbackType.In(0), progressUpdateType)
	}
}

func TestRunAnalysisOnlyWithDeps_NonTTYOmitsBenchPath(t *testing.T) {
	inputPath := ".bench/analysis/input/sample.wav"
	config := processor.DefaultFilterConfig()
	var output bytes.Buffer

	analyze := func(_ context.Context, path string, cfg *processor.BaseFilterConfig, progress processor.ProgressCallback) (*processor.AnalysisResult, error) {
		if path != inputPath {
			t.Fatalf("analyzeDetailed path = %q, want %q", path, inputPath)
		}
		effective, diagnostics := processor.AdaptConfig(cfg, makeAnalysisOnlyTestMeasurements())
		return &processor.AnalysisResult{
			Measurements:       makeAnalysisOnlyTestMeasurements(),
			Config:             effective,
			Diagnostics:        diagnostics,
			AnalysisDuration:   2 * time.Second,
			AdaptationDuration: 100 * time.Millisecond,
		}, nil
	}
	origAnalyze := analysisPoolAnalyze
	analysisPoolAnalyze = analyze
	t.Cleanup(func() { analysisPoolAnalyze = origAnalyze })

	logs := newLogCapture()
	runAnalysisOnlyWithDeps([]string{inputPath}, config, func(string, ...any) {}, 1, analysisOnlyDeps{
		stdout: &output,
		hasTTY: func() bool {
			return false
		},
		openMetadata: func(path string) (*audio.Metadata, error) {
			if path != inputPath {
				t.Fatalf("openMetadata path = %q, want %q", path, inputPath)
			}
			return &audio.Metadata{
				Duration:   120,
				SampleRate: 48000,
				Channels:   1,
			}, nil
		},
		analyzeDetailed: analyze,
		displayResults:  logging.DisplayAnalysisResultsWithDiagnostics,
		printError: func(message string) {
			t.Fatalf("printError called: %s", message)
		},
		createLog: logs.create,
	})

	got := output.String()
	// stdout carries the banner plus the one-line confirmation, never the
	// report body or any benchmark path.
	if strings.Contains(got, "ANALYSIS: sample.wav") {
		t.Fatalf("report body leaked to stdout instead of the log file:\n%s", got)
	}
	if strings.Contains(got, ".bench/") {
		t.Fatalf("analysis-only stdout leaked benchmark path:\n%s", got)
	}
	for _, want := range []string{
		"Analysing 1 files…",
		"🗸 sample.wav → sample-wav-analysis.log",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("analysis-only stdout missing %q:\n%s", want, got)
		}
	}

	// The full report lands in <source-name>-analysis.log beside the source.
	logPath := ".bench/analysis/input/sample-wav-analysis.log"
	report, ok := logs.content(logPath)
	if !ok {
		t.Fatalf("no analysis log written at %q (have %v)", logPath, logs.logs)
	}
	for _, want := range []string{
		"ANALYSIS: sample.wav",
		"ANALYSIS TIMINGS",
		"Analysis:",
		"Adaptation:",
		"Report Output:",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("analysis log missing %q:\n%s", want, report)
		}
	}
}

func TestRunAnalysisOnlyWithDeps_UsesPerFileResultConfig(t *testing.T) {
	files := []string{"first.wav", "second.wav"}
	baseConfig := processor.DefaultFilterConfig()
	var output bytes.Buffer
	firstEffective, _ := processor.AdaptConfig(processor.DefaultFilterConfig(), makeAnalysisOnlyTestMeasurements())
	secondEffective, _ := processor.AdaptConfig(processor.DefaultFilterConfig(), makeAnalysisOnlyTestMeasurements())
	resultConfigs := []*processor.EffectiveFilterConfig{
		firstEffective,
		secondEffective,
	}
	resultConfigs[0].DS201HighPass.Frequency = 60.0
	resultConfigs[1].DS201HighPass.Frequency = 100.0
	secondFilterOrder := append([]processor.FilterID(nil), resultConfigs[1].FilterOrder...)
	resultDiagnostics := []*processor.AdaptiveDiagnostics{
		{DS201LPReason: "first"},
		{DS201LPReason: "second"},
	}

	var analyzedConfigs []*processor.BaseFilterConfig
	var displayedConfigs []*processor.EffectiveFilterConfig
	var displayedDiagnostics []*processor.AdaptiveDiagnostics

	fileIndex := map[string]int{files[0]: 0, files[1]: 1}
	var mu sync.Mutex
	analyze := func(_ context.Context, path string, cfg *processor.BaseFilterConfig, progress processor.ProgressCallback) (*processor.AnalysisResult, error) {
		mu.Lock()
		analyzedConfigs = append(analyzedConfigs, cfg)
		mu.Unlock()

		index := fileIndex[path]
		return &processor.AnalysisResult{
			Measurements:       makeAnalysisOnlyTestMeasurements(),
			Config:             resultConfigs[index],
			Diagnostics:        resultDiagnostics[index],
			AnalysisDuration:   2 * time.Second,
			AdaptationDuration: 100 * time.Millisecond,
		}, nil
	}
	origAnalyze := analysisPoolAnalyze
	analysisPoolAnalyze = analyze
	t.Cleanup(func() { analysisPoolAnalyze = origAnalyze })

	runAnalysisOnlyWithDeps(files, baseConfig, func(string, ...any) {}, 1, analysisOnlyDeps{
		stdout: &output,
		hasTTY: func() bool {
			return false
		},
		openMetadata: func(path string) (*audio.Metadata, error) {
			return &audio.Metadata{
				Duration:   120,
				SampleRate: 48000,
				Channels:   1,
			}, nil
		},
		analyzeDetailed: analyze,
		displayResults: func(w io.Writer, inputPath string, metadata *audio.Metadata, measurements *processor.AudioMeasurements, config *processor.EffectiveFilterConfig, diagnostics *processor.AdaptiveDiagnostics, timings ...logging.AnalysisTimings) {
			displayedConfigs = append(displayedConfigs, config)
			displayedDiagnostics = append(displayedDiagnostics, diagnostics)
			if len(displayedConfigs) == 1 {
				config.FilterOrder[0] = processor.FilterAnalysis
			}
		},
		printError: func(message string) {
			t.Fatalf("printError called: %s", message)
		},
		createLog: newLogCapture().create,
	})

	if len(analyzedConfigs) != len(files) {
		t.Fatalf("analyzed config count = %d, want %d", len(analyzedConfigs), len(files))
	}
	if analyzedConfigs[0] == baseConfig || analyzedConfigs[1] == baseConfig {
		t.Fatal("analysis-only did not pass per-worker config clones to analysis calls")
	}
	if len(displayedConfigs) != len(resultConfigs) {
		t.Fatalf("displayed config count = %d, want %d", len(displayedConfigs), len(resultConfigs))
	}
	for i := range resultConfigs {
		if displayedConfigs[i] != resultConfigs[i] {
			t.Fatalf("displayed config %d = %p, want AnalysisResult.Config %p", i, displayedConfigs[i], resultConfigs[i])
		}
		if displayedDiagnostics[i] != resultDiagnostics[i] {
			t.Fatalf("displayed diagnostics %d = %p, want AnalysisResult.Diagnostics %p", i, displayedDiagnostics[i], resultDiagnostics[i])
		}
	}
	if !reflect.DeepEqual(resultConfigs[1].FilterOrder, secondFilterOrder) {
		t.Fatalf("second result config FilterOrder = %v, want unaffected %v", resultConfigs[1].FilterOrder, secondFilterOrder)
	}
	if baseConfig.DS201HighPass.Frequency == resultConfigs[0].DS201HighPass.Frequency ||
		baseConfig.DS201HighPass.Frequency == resultConfigs[1].DS201HighPass.Frequency {
		t.Fatal("test setup failed: result configs should differ from the shared base seed")
	}
}

func TestRunAnalysisOnlyWithDeps_OrderedOutputParityAcrossJobs(t *testing.T) {
	files := []string{"file0.wav", "file1.wav", "file2.wav", "file3.wav"}
	baseConfig := processor.DefaultFilterConfig()

	fileIndex := make(map[string]int, len(files))
	for i, f := range files {
		fileIndex[f] = i
	}

	// Deterministic per-index sentinel: distinct measurements keyed by file
	// index, and a staggered completion delay so that later-submitted files
	// finish earlier. At jobs=N all workers run concurrently, so completion
	// order != submission order; at jobs=1 it is serial. Both runs must emit
	// byte-for-byte identical, input-ordered reports.
	analyze := func(_ context.Context, path string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.AnalysisResult, error) { //nolint:unparam // signature must match processor.AnalyzeOnlyDetailed
		index, ok := fileIndex[path]
		if !ok {
			t.Fatalf("analysisPoolAnalyze unexpected path %q", path)
		}

		// Later indices sleep less, so under concurrency they complete first.
		delay := time.Duration(len(files)-index) * 20 * time.Millisecond
		time.Sleep(delay)

		measurements := makeAnalysisOnlyTestMeasurements()
		measurements.InputI -= float64(index)
		measurements.NoiseFloor -= float64(index)

		effective, diagnostics := processor.AdaptConfig(baseConfig, measurements)
		return &processor.AnalysisResult{
			Measurements:       measurements,
			Config:             effective,
			Diagnostics:        diagnostics,
			AnalysisDuration:   2 * time.Second,
			AdaptationDuration: 100 * time.Millisecond,
		}, nil
	}
	origAnalyze := analysisPoolAnalyze
	analysisPoolAnalyze = analyze
	t.Cleanup(func() { analysisPoolAnalyze = origAnalyze })

	run := func(jobs int) (string, *logCapture) {
		var output bytes.Buffer
		logs := newLogCapture()
		runAnalysisOnlyWithDeps(files, baseConfig, func(string, ...any) {}, jobs, analysisOnlyDeps{
			stdout: &output,
			hasTTY: func() bool {
				return false
			},
			openMetadata: func(path string) (*audio.Metadata, error) {
				if _, ok := fileIndex[path]; !ok {
					t.Fatalf("openMetadata unexpected path %q", path)
				}
				return &audio.Metadata{
					Duration:   120,
					SampleRate: 48000,
					Channels:   1,
				}, nil
			},
			analyzeDetailed: analyze,
			displayResults:  logging.DisplayAnalysisResultsWithDiagnostics,
			printError: func(message string) {
				t.Fatalf("printError called: %s", message)
			},
			createLog: logs.create,
		})
		return output.String(), logs
	}

	parallel, parallelLogs := run(4)
	serial, _ := run(1)

	if parallel != serial {
		t.Fatalf("jobs=4 stdout differs from jobs=1 stdout\n--- jobs=4 ---\n%s\n--- jobs=1 ---\n%s", parallel, serial)
	}

	// Confirmation lines must follow submission (input) order despite staggered
	// completion: each file's "🗸 <file> → " line appears in file0..file3 order.
	plain := stripANSI(parallel)
	lastPos := -1
	for _, f := range files {
		line := "🗸 " + f + " → "
		pos := strings.Index(plain, line)
		if pos < 0 {
			t.Fatalf("stdout missing confirmation %q:\n%s", line, plain)
		}
		if pos <= lastPos {
			t.Fatalf("confirmation for %s out of input order (pos %d <= prev %d):\n%s", f, pos, lastPos, plain)
		}
		lastPos = pos
	}

	// Each file's full report lands in its own <name>-analysis.log.
	for _, f := range files {
		logPath := logging.AnalysisLogPath(f)
		report, ok := parallelLogs.content(logPath)
		if !ok {
			t.Fatalf("no analysis log at %q", logPath)
		}
		if !strings.Contains(report, "ANALYSIS: "+f) {
			t.Fatalf("log %q missing report header for %q:\n%s", logPath, f, report)
		}
	}

	// Both runs print the identical up-front banner.
	banner := "Analysing 4 files…"
	if !strings.Contains(parallel, banner) {
		t.Fatalf("stdout missing banner %q:\n%s", banner, parallel)
	}
}

func TestRunAnalysisOnlyWithDeps_NonTTYBannerThenOrderedReports(t *testing.T) {
	files := []string{"file0.wav", "file1.wav", "file2.wav"}
	baseConfig := processor.DefaultFilterConfig()
	var output bytes.Buffer

	fileIndex := make(map[string]int, len(files))
	for i, f := range files {
		fileIndex[f] = i
	}

	// Staggered completion: later-submitted files sleep less and finish first,
	// so with jobs >= len(files) the workers overlap and completion order !=
	// submission order. The buffered slots must still print in input order.
	analyze := func(_ context.Context, path string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.AnalysisResult, error) {
		index, ok := fileIndex[path]
		if !ok {
			t.Fatalf("analysisPoolAnalyze unexpected path %q", path)
		}

		delay := time.Duration(len(files)-index) * 20 * time.Millisecond
		time.Sleep(delay)

		measurements := makeAnalysisOnlyTestMeasurements()
		measurements.InputI -= float64(index)

		effective, diagnostics := processor.AdaptConfig(baseConfig, measurements)
		return &processor.AnalysisResult{
			Measurements:       measurements,
			Config:             effective,
			Diagnostics:        diagnostics,
			AnalysisDuration:   2 * time.Second,
			AdaptationDuration: 100 * time.Millisecond,
		}, nil
	}
	origAnalyze := analysisPoolAnalyze
	analysisPoolAnalyze = analyze
	t.Cleanup(func() { analysisPoolAnalyze = origAnalyze })

	logs := newLogCapture()
	runAnalysisOnlyWithDeps(files, baseConfig, func(string, ...any) {}, len(files), analysisOnlyDeps{
		stdout: &output,
		hasTTY: func() bool {
			return false
		},
		openMetadata: func(path string) (*audio.Metadata, error) {
			if _, ok := fileIndex[path]; !ok {
				t.Fatalf("openMetadata unexpected path %q", path)
			}
			return &audio.Metadata{
				Duration:   120,
				SampleRate: 48000,
				Channels:   1,
			}, nil
		},
		analyzeDetailed: analyze,
		displayResults:  logging.DisplayAnalysisResultsWithDiagnostics,
		printError: func(message string) {
			t.Fatalf("printError called: %s", message)
		},
		createLog: logs.create,
	})

	got := output.String()

	// stdout starts with the up-front banner (byte-for-byte: single U+2026
	// ellipsis, trailing newline), matching the production main.go no-TTY
	// branch and the 3.3 test assertion.
	banner := "Analysing 3 files…\n"
	if !strings.HasPrefix(got, banner) {
		t.Fatalf("output does not start with banner %q:\n%s", banner, got)
	}

	// No per-file "Analysing: <file>" line from the old serial format, and no
	// report body on stdout (the report now lives in the .log file).
	if strings.Contains(got, "Analysing: ") {
		t.Fatalf("output contains the removed per-file %q line:\n%s", "Analysing: ", got)
	}
	if strings.Contains(got, "ANALYSIS: ") {
		t.Fatalf("report body leaked to stdout:\n%s", got)
	}

	// Confirmation lines appear in input order despite staggered completion.
	plain := stripANSI(got)
	lastPos := -1
	for _, f := range files {
		line := "🗸 " + f + " → "
		pos := strings.Index(plain, line)
		if pos < 0 {
			t.Fatalf("stdout missing confirmation %q:\n%s", line, plain)
		}
		if pos <= lastPos {
			t.Fatalf("confirmation for %s out of input order (pos %d <= prev %d):\n%s", f, pos, lastPos, plain)
		}
		lastPos = pos
	}

	// Each file's full report lands in its own <name>-analysis.log.
	for _, f := range files {
		logPath := logging.AnalysisLogPath(f)
		report, ok := logs.content(logPath)
		if !ok {
			t.Fatalf("no analysis log at %q", logPath)
		}
		if !strings.Contains(report, "ANALYSIS: "+f) {
			t.Fatalf("log %q missing report header for %q:\n%s", logPath, f, report)
		}
	}
}

func TestRunAnalysisOnlyWithDeps_FailureIsolation(t *testing.T) {
	files := []string{"file0.wav", "file1.wav", "file2.wav"}
	baseConfig := processor.DefaultFilterConfig()
	var output bytes.Buffer

	const failIndex = 1
	boom := errors.New("boom")

	fileIndex := make(map[string]int, len(files))
	for i, f := range files {
		fileIndex[f] = i
	}

	// One input fails with a plain (non-cancellation) error; the siblings
	// return valid sentinels. A real error must not be suppressed by the
	// cancellation filter, so the failing file reports through printError.
	analyze := func(_ context.Context, path string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.AnalysisResult, error) {
		index, ok := fileIndex[path]
		if !ok {
			t.Fatalf("analysisPoolAnalyze unexpected path %q", path)
		}
		if index == failIndex {
			return nil, boom
		}
		effective, diagnostics := processor.AdaptConfig(baseConfig, makeAnalysisOnlyTestMeasurements())
		return &processor.AnalysisResult{
			Measurements:       makeAnalysisOnlyTestMeasurements(),
			Config:             effective,
			Diagnostics:        diagnostics,
			AnalysisDuration:   2 * time.Second,
			AdaptationDuration: 100 * time.Millisecond,
		}, nil
	}
	origAnalyze := analysisPoolAnalyze
	analysisPoolAnalyze = analyze
	t.Cleanup(func() { analysisPoolAnalyze = origAnalyze })

	var printErrMu sync.Mutex
	var printErrors []string

	logs := newLogCapture()
	runAnalysisOnlyWithDeps(files, baseConfig, func(string, ...any) {}, 4, analysisOnlyDeps{
		stdout: &output,
		hasTTY: func() bool {
			return false
		},
		openMetadata: func(path string) (*audio.Metadata, error) {
			if _, ok := fileIndex[path]; !ok {
				t.Fatalf("openMetadata unexpected path %q", path)
			}
			return &audio.Metadata{
				Duration:   120,
				SampleRate: 48000,
				Channels:   1,
			}, nil
		},
		analyzeDetailed: analyze,
		displayResults:  logging.DisplayAnalysisResultsWithDiagnostics,
		printError: func(message string) {
			printErrMu.Lock()
			printErrors = append(printErrors, message)
			printErrMu.Unlock()
		},
		createLog: logs.create,
	})

	// The failing file reports exactly one error naming the file and "boom".
	if len(printErrors) != 1 {
		t.Fatalf("printError calls = %d (%v), want exactly 1", len(printErrors), printErrors)
	}
	if msg := printErrors[0]; !strings.Contains(msg, files[failIndex]) || !strings.Contains(msg, "boom") {
		t.Fatalf("printError message = %q, want it to mention %q and %q", msg, files[failIndex], "boom")
	}

	// The good siblings each get a log with their report; the failing file gets
	// none (no log created, no confirmation).
	for _, f := range []string{files[0], files[2]} {
		report, ok := logs.content(logging.AnalysisLogPath(f))
		if !ok {
			t.Fatalf("missing analysis log for sibling %q", f)
		}
		if !strings.Contains(report, "ANALYSIS: "+f) {
			t.Fatalf("log for sibling %q missing report header:\n%s", f, report)
		}
	}
	if _, ok := logs.content(logging.AnalysisLogPath(files[failIndex])); ok {
		t.Fatalf("failing file %q must not produce an analysis log", files[failIndex])
	}

	plain := stripANSI(output.String())
	if strings.Contains(plain, files[failIndex]+" → ") {
		t.Fatalf("failing file %q must not print a confirmation:\n%s", files[failIndex], plain)
	}

	// The run completed (no early abort): both good siblings confirmed in order.
	pos0 := strings.Index(plain, "🗸 "+files[0]+" → ")
	pos2 := strings.Index(plain, "🗸 "+files[2]+" → ")
	if pos0 < 0 || pos2 < 0 || pos2 <= pos0 {
		t.Fatalf("sibling confirmations out of input order (pos0=%d, pos2=%d):\n%s", pos0, pos2, plain)
	}
}

// ansiRE strips ANSI escape sequences so stdout assertions match plain text.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string {
	return ansiRE.ReplaceAllString(s, "")
}

// logCapture records analysis log writes keyed by the requested log path,
// letting tests assert report content without touching the filesystem.
type logCapture struct {
	mu   sync.Mutex
	logs map[string]*bytes.Buffer
}

func newLogCapture() *logCapture {
	return &logCapture{logs: make(map[string]*bytes.Buffer)}
}

type bufferWriteCloser struct{ *bytes.Buffer }

func (bufferWriteCloser) Close() error { return nil }

func (c *logCapture) create(path string) (io.WriteCloser, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	buf := &bytes.Buffer{}
	c.logs[path] = buf
	return bufferWriteCloser{buf}, nil
}

func (c *logCapture) content(path string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	buf, ok := c.logs[path]
	if !ok {
		return "", false
	}
	return buf.String(), true
}

func parseCLIArgs(t *testing.T, args ...string) *CLI {
	t.Helper()
	cliArgs := &CLI{}
	parser, err := kong.New(cliArgs,
		kong.Name("jivetalking"),
		kong.Exit(func(int) {
			t.Fatalf("kong.Exit called during parse with args %v", args)
		}),
	)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse(args); err != nil {
		t.Fatalf("kong.Parse(%v) error = %v", args, err)
	}
	return cliArgs
}

func makeFixtureFile(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := dir + "/in.wav"
	if err := os.WriteFile(path, []byte("dummy"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestCLI_RoomToneScanDurationFlag_ParsesIntoStructField(t *testing.T) {
	fixture := makeFixtureFile(t)
	cliArgs := parseCLIArgs(t, "--room-tone-scan-duration=30s", fixture)
	if cliArgs.RoomToneScanDuration != 30*time.Second {
		t.Fatalf("RoomToneScanDuration = %s, want 30s", cliArgs.RoomToneScanDuration)
	}
	if cliArgs.SilenceScanDuration != 0 {
		t.Fatalf("SilenceScanDuration = %s, want 0s", cliArgs.SilenceScanDuration)
	}
}

func TestCLI_SilenceScanDurationFlag_StillParses(t *testing.T) {
	fixture := makeFixtureFile(t)
	cliArgs := parseCLIArgs(t, "--silence-scan-duration=45s", fixture)
	if cliArgs.SilenceScanDuration != 45*time.Second {
		t.Fatalf("SilenceScanDuration = %s, want 45s", cliArgs.SilenceScanDuration)
	}
	if cliArgs.RoomToneScanDuration != 0 {
		t.Fatalf("RoomToneScanDuration = %s, want 0s", cliArgs.RoomToneScanDuration)
	}
}

func TestCLI_HelpOutput_MarksSilenceFlagDeprecated(t *testing.T) {
	cliArgs := &CLI{}
	var helpBuf bytes.Buffer
	parser, err := kong.New(cliArgs,
		kong.Name("jivetalking"),
		kong.Writers(&helpBuf, &helpBuf),
		kong.Exit(func(int) {}),
	)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse([]string{"--help"}); err != nil {
		t.Fatalf("kong.Parse(--help) error = %v", err)
	}
	help := helpBuf.String()
	if !strings.Contains(help, "--room-tone-scan-duration") {
		t.Fatalf("help missing --room-tone-scan-duration:\n%s", help)
	}
	if !strings.Contains(help, "--silence-scan-duration") {
		t.Fatalf("help missing --silence-scan-duration:\n%s", help)
	}
	if !strings.Contains(help, "deprecated alias") {
		t.Fatalf("help missing deprecated alias marker:\n%s", help)
	}
}

func TestResolveRoomToneScanDuration_NewFlagAlone(t *testing.T) {
	got, err := resolveRoomToneScanDuration(30*time.Second, 0, io.Discard)
	if err != nil {
		t.Fatalf("resolveRoomToneScanDuration: %v", err)
	}
	if got != 30*time.Second {
		t.Fatalf("got = %s, want 30s", got)
	}
}

func TestResolveRoomToneScanDuration_DeprecatedAliasAlone(t *testing.T) {
	var notice bytes.Buffer
	got, err := resolveRoomToneScanDuration(0, 45*time.Second, &notice)
	if err != nil {
		t.Fatalf("resolveRoomToneScanDuration: %v", err)
	}
	if got != 45*time.Second {
		t.Fatalf("got = %s, want 45s", got)
	}
	if !strings.Contains(notice.String(), "deprecated") {
		t.Fatalf("expected deprecation notice, got %q", notice.String())
	}
	if !strings.Contains(notice.String(), "--silence-scan-duration") {
		t.Fatalf("deprecation notice missing legacy flag name, got %q", notice.String())
	}
}

func TestResolveRoomToneScanDuration_BothSameValueAccepted(t *testing.T) {
	var notice bytes.Buffer
	got, err := resolveRoomToneScanDuration(30*time.Second, 30*time.Second, &notice)
	if err != nil {
		t.Fatalf("resolveRoomToneScanDuration: %v", err)
	}
	if got != 30*time.Second {
		t.Fatalf("got = %s, want 30s", got)
	}
	if !strings.Contains(notice.String(), "deprecated") {
		t.Fatalf("expected deprecation notice when legacy flag set, got %q", notice.String())
	}
}

func TestResolveRoomToneScanDuration_BothDifferentRejected(t *testing.T) {
	_, err := resolveRoomToneScanDuration(30*time.Second, 45*time.Second, io.Discard)
	if err == nil {
		t.Fatal("resolveRoomToneScanDuration with conflicting non-zero values: want error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"--room-tone-scan-duration", "--silence-scan-duration", "conflict"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("conflict error = %q, want substring %q", msg, want)
		}
	}
}

func TestResolveRoomToneScanDuration_NegativeRoomToneRejected(t *testing.T) {
	_, err := resolveRoomToneScanDuration(-1*time.Second, 0, io.Discard)
	if err == nil {
		t.Fatal("resolveRoomToneScanDuration with negative room-tone value: want error, got nil")
	}
	if !strings.Contains(err.Error(), "--room-tone-scan-duration") {
		t.Fatalf("negative error = %q, want --room-tone-scan-duration", err.Error())
	}
}

func TestResolveRoomToneScanDuration_NegativeSilenceRejected(t *testing.T) {
	_, err := resolveRoomToneScanDuration(0, -1*time.Second, io.Discard)
	if err == nil {
		t.Fatal("resolveRoomToneScanDuration with negative silence value: want error, got nil")
	}
	if !strings.Contains(err.Error(), "--silence-scan-duration") {
		t.Fatalf("negative error = %q, want --silence-scan-duration", err.Error())
	}
}

func TestResolveRoomToneScanDuration_NeitherSetReturnsZero(t *testing.T) {
	got, err := resolveRoomToneScanDuration(0, 0, io.Discard)
	if err != nil {
		t.Fatalf("resolveRoomToneScanDuration: %v", err)
	}
	if got != 0 {
		t.Fatalf("got = %s, want 0s", got)
	}
}

func TestResolveJobs(t *testing.T) {
	tests := []struct {
		name     string
		numFiles int
		numCPU   int
		want     int
	}{
		{name: "fewer files than CPUs uses file count", numFiles: 3, numCPU: 8, want: 3},
		{name: "more files than CPUs caps at CPU count", numFiles: 16, numCPU: 8, want: 8},
		{name: "files equal CPUs uses that count", numFiles: 8, numCPU: 8, want: 8},
		{name: "single file stays one", numFiles: 1, numCPU: 8, want: 1},
		{name: "zero files floors to one", numFiles: 0, numCPU: 8, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveJobs(tt.numFiles, tt.numCPU); got != tt.want {
				t.Fatalf("resolveJobs(%d, %d) = %d, want %d", tt.numFiles, tt.numCPU, got, tt.want)
			}
		})
	}
}

func makeAnalysisOnlyTestMeasurements() *processor.AudioMeasurements {
	return &processor.AudioMeasurements{
		BaseMeasurements: processor.BaseMeasurements{
			RMSLevel:     -24,
			PeakLevel:    -6,
			DynamicRange: 18,
		},
		InputI:              -23,
		InputTP:             -1,
		InputLRA:            6,
		NoiseFloor:          -50,
		NoiseFloorSource:    "rms_estimate",
		PreScanNoiseFloor:   -50,
		RoomToneDetectLevel: -45,
	}
}
