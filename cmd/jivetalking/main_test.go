package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/processor"
	"github.com/linuxmatters/jivetalking/internal/report"
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
	analyse, ok := depsType.FieldByName("analyse")
	if !ok {
		t.Fatal("analysisOnlyDeps has no analyse field")
	}
	if analyse.Type.Kind() != reflect.Func {
		t.Fatalf("analysisOnlyDeps.analyse = %s, want func", analyse.Type)
	}
	if analyse.Type.NumIn() != 4 {
		t.Fatalf("analysisOnlyDeps.analyse has %d parameters, want 4", analyse.Type.NumIn())
	}
	if analyse.Type.In(3) != progressCallbackType {
		t.Fatalf("analysisOnlyDeps.analyse progress callback = %s, want %s",
			analyse.Type.In(3), progressCallbackType)
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

	analyse := func(_ context.Context, path string, cfg *processor.BaseFilterConfig, progress processor.ProgressCallback) (*processor.AnalysisResult, error) {
		if path != inputPath {
			t.Fatalf("analyse path = %q, want %q", path, inputPath)
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

	reports := newReportCapture()
	runAnalysisOnlyWithDeps([]string{inputPath}, config, func(string, ...any) {}, 1, false, analysisOnlyDeps{
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
		analyse: analyse,
		printError: func(message string) {
			t.Fatalf("printError called: %s", message)
		},
		writeMarkdownReport: reports.write,
		writeRunRecord:      func(*processor.RunRecord, string) error { return nil },
		writeSidecars:       func(*processor.AudioMeasurements, string) error { return nil },
	})

	got := output.String()
	// stdout carries the banner plus the one-line confirmation, never the
	// report body or any benchmark path.
	if strings.Contains(got, "# Audio Processing Report") {
		t.Fatalf("report body leaked to stdout instead of the report file:\n%s", got)
	}
	if strings.Contains(got, ".bench/") {
		t.Fatalf("analysis-only stdout leaked benchmark path:\n%s", got)
	}
	for _, want := range []string{
		"Analysing 1 files…",
		"🗸 sample.wav → sample-wav-analysis.md",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("analysis-only stdout missing %q:\n%s", want, got)
		}
	}

	// The full report lands in <source-name>-analysis.md beside the source.
	reportPath := ".bench/analysis/input/sample-wav-analysis.md"
	report, ok := reports.content(reportPath)
	if !ok {
		t.Fatalf("no analysis report written at %q (have %v)", reportPath, reports.reports)
	}
	for _, want := range []string{
		"# Audio Processing Report",
		"| Input file | sample.wav |",
		"## Processing Summary",
		"| Analysis | 2.0s |",
		"| Adaptation | 0.1s |",
	} {
		if !strings.Contains(report, want) {
			t.Fatalf("analysis report missing %q:\n%s", want, report)
		}
	}
}

// TestRunAnalysisOnlyWithDeps_DiagnosticsGatesSidecars proves the diagnostics
// gate on the analysis-only path: with --diagnostics off the .jsonl sidecar write is
// skipped while the .md report and .json record still write; with it on the
// sidecar write fires exactly once, beside the record. The deps stubs record
// each write so the test asserts the gate without touching the filesystem.
func TestRunAnalysisOnlyWithDeps_DiagnosticsGatesSidecars(t *testing.T) {
	inputPath := "stem.wav"
	config := processor.DefaultFilterConfig()

	analyse := func(_ context.Context, _ string, cfg *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.AnalysisResult, error) {
		effective, diagnostics := processor.AdaptConfig(cfg, makeAnalysisOnlyTestMeasurements())
		return &processor.AnalysisResult{
			Measurements:       makeAnalysisOnlyTestMeasurements(),
			Config:             effective,
			Diagnostics:        diagnostics,
			AnalysisDuration:   2 * time.Second,
			AdaptationDuration: 100 * time.Millisecond,
		}, nil
	}

	reportPath := report.AnalysisReportPath(inputPath)
	wantRecordPath := strings.TrimSuffix(reportPath, filepath.Ext(reportPath)) + ".json"

	run := func(diagnostics bool) (reportWritten, recordWritten bool, sidecarPaths []string) {
		reports := newReportCapture()
		deps := analysisOnlyDeps{
			stdout: io.Discard,
			hasTTY: func() bool { return false },
			openMetadata: func(string) (*audio.Metadata, error) {
				return &audio.Metadata{Duration: 120, SampleRate: 48000, Channels: 1}, nil
			},
			analyse: analyse,
			// The synthetic input (stem.wav) does not exist on disk, so the
			// diagnostics-on spectrogram renders fail to open it and surface a
			// non-fatal "Failed to render analysis spectrogram" message. That is
			// correct behaviour (render failure is non-fatal in this path); this test
			// only asserts the sidecar gate, so tolerate those messages and fatal on
			// any other unexpected printError.
			printError: func(message string) {
				if strings.Contains(message, "Failed to render analysis spectrogram") {
					return
				}
				t.Fatalf("printError called: %s", message)
			},
			writeMarkdownReport: reports.write,
			writeRunRecord: func(_ *processor.RunRecord, path string) error {
				if path == wantRecordPath {
					recordWritten = true
				}
				return nil
			},
			writeSidecars: func(_ *processor.AudioMeasurements, path string) error {
				sidecarPaths = append(sidecarPaths, path)
				return nil
			},
		}
		runAnalysisOnlyWithDeps([]string{inputPath}, config, func(string, ...any) {}, 1, diagnostics, deps)
		_, reportWritten = reports.content(reportPath)
		return reportWritten, recordWritten, sidecarPaths
	}

	// Flag OFF: no sidecar write, but the .md report and .json record still land.
	reportWritten, recordWritten, sidecarPaths := run(false)
	if len(sidecarPaths) != 0 {
		t.Fatalf("diagnostics off: writeSidecars called %d times, want 0 (paths %v)", len(sidecarPaths), sidecarPaths)
	}
	if !reportWritten {
		t.Fatal("diagnostics off: .md report not written (must stay always-on)")
	}
	if !recordWritten {
		t.Fatal("diagnostics off: .json record not written (must stay always-on)")
	}

	// Flag ON: sidecar write fires once, beside the record.
	reportWritten, recordWritten, sidecarPaths = run(true)
	if len(sidecarPaths) != 1 || sidecarPaths[0] != wantRecordPath {
		t.Fatalf("diagnostics on: sidecar writes = %v, want exactly [%q]", sidecarPaths, wantRecordPath)
	}
	if !reportWritten || !recordWritten {
		t.Fatalf("diagnostics on: always-on artefacts missing (report=%v record=%v)", reportWritten, recordWritten)
	}
}

func TestRunAnalysisOnlyWithDeps_PassesPerWorkerConfigClones(t *testing.T) {
	files := []string{"first.wav", "second.wav"}
	baseConfig := processor.DefaultFilterConfig()
	var output bytes.Buffer
	firstEffective, _ := processor.AdaptConfig(processor.DefaultFilterConfig(), makeAnalysisOnlyTestMeasurements())
	secondEffective, _ := processor.AdaptConfig(processor.DefaultFilterConfig(), makeAnalysisOnlyTestMeasurements())
	resultConfigs := []*processor.EffectiveFilterConfig{
		firstEffective,
		secondEffective,
	}

	var analysedConfigs []*processor.BaseFilterConfig

	fileIndex := map[string]int{files[0]: 0, files[1]: 1}
	var mu sync.Mutex
	analyse := func(_ context.Context, path string, cfg *processor.BaseFilterConfig, progress processor.ProgressCallback) (*processor.AnalysisResult, error) {
		mu.Lock()
		analysedConfigs = append(analysedConfigs, cfg)
		mu.Unlock()

		index := fileIndex[path]
		return &processor.AnalysisResult{
			Measurements:       makeAnalysisOnlyTestMeasurements(),
			Config:             resultConfigs[index],
			Diagnostics:        &processor.AdaptiveDiagnostics{},
			AnalysisDuration:   2 * time.Second,
			AdaptationDuration: 100 * time.Millisecond,
		}, nil
	}

	reports := newReportCapture()
	runAnalysisOnlyWithDeps(files, baseConfig, func(string, ...any) {}, 1, false, analysisOnlyDeps{
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
		analyse: analyse,
		printError: func(message string) {
			t.Fatalf("printError called: %s", message)
		},
		writeMarkdownReport: reports.write,
		writeRunRecord:      func(*processor.RunRecord, string) error { return nil },
		writeSidecars:       func(*processor.AudioMeasurements, string) error { return nil },
	})

	if len(analysedConfigs) != len(files) {
		t.Fatalf("analysed config count = %d, want %d", len(analysedConfigs), len(files))
	}
	// Each worker must receive its own config clone, never the shared base seed,
	// so concurrent workers share no mutable config.
	if analysedConfigs[0] == baseConfig || analysedConfigs[1] == baseConfig {
		t.Fatal("analysis-only did not pass per-worker config clones to analysis calls")
	}
	if analysedConfigs[0] == analysedConfigs[1] {
		t.Fatal("analysis-only reused one config clone across workers")
	}
	// Both files produce a Markdown report named after the source.
	for _, f := range files {
		reportPath := report.AnalysisReportPath(f)
		if _, ok := reports.content(reportPath); !ok {
			t.Fatalf("no analysis report written at %q", reportPath)
		}
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
	analyse := func(_ context.Context, path string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.AnalysisResult, error) { //nolint:unparam // signature must match processor.AnalyseOnlyDetailed
		index, ok := fileIndex[path]
		if !ok {
			t.Fatalf("analyse unexpected path %q", path)
		}

		// Later indices sleep less, so under concurrency they complete first.
		delay := time.Duration(len(files)-index) * 20 * time.Millisecond
		time.Sleep(delay)

		measurements := makeAnalysisOnlyTestMeasurements()
		measurements.Loudness.InputI -= float64(index)
		measurements.Noise.Floor -= float64(index)

		effective, diagnostics := processor.AdaptConfig(baseConfig, measurements)
		return &processor.AnalysisResult{
			Measurements:       measurements,
			Config:             effective,
			Diagnostics:        diagnostics,
			AnalysisDuration:   2 * time.Second,
			AdaptationDuration: 100 * time.Millisecond,
		}, nil
	}

	run := func(jobs int) (string, *reportCapture) {
		var output bytes.Buffer
		reports := newReportCapture()
		runAnalysisOnlyWithDeps(files, baseConfig, func(string, ...any) {}, jobs, false, analysisOnlyDeps{
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
			analyse:             analyse,
			writeMarkdownReport: reports.write,
			printError: func(message string) {
				t.Fatalf("printError called: %s", message)
			},
			writeRunRecord: func(*processor.RunRecord, string) error { return nil },
			writeSidecars:  func(*processor.AudioMeasurements, string) error { return nil },
		})
		return output.String(), reports
	}

	parallel, parallelReports := run(4)
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

	// Each file's full report lands in its own <name>-analysis.md.
	for _, f := range files {
		reportPath := report.AnalysisReportPath(f)
		report, ok := parallelReports.content(reportPath)
		if !ok {
			t.Fatalf("no analysis report at %q", reportPath)
		}
		if !strings.Contains(report, "| Input file | "+f+" |") {
			t.Fatalf("report %q missing input-file row for %q:\n%s", reportPath, f, report)
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
	analyse := func(_ context.Context, path string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.AnalysisResult, error) {
		index, ok := fileIndex[path]
		if !ok {
			t.Fatalf("analyse unexpected path %q", path)
		}

		delay := time.Duration(len(files)-index) * 20 * time.Millisecond
		time.Sleep(delay)

		measurements := makeAnalysisOnlyTestMeasurements()
		measurements.Loudness.InputI -= float64(index)

		effective, diagnostics := processor.AdaptConfig(baseConfig, measurements)
		return &processor.AnalysisResult{
			Measurements:       measurements,
			Config:             effective,
			Diagnostics:        diagnostics,
			AnalysisDuration:   2 * time.Second,
			AdaptationDuration: 100 * time.Millisecond,
		}, nil
	}

	reports := newReportCapture()
	runAnalysisOnlyWithDeps(files, baseConfig, func(string, ...any) {}, len(files), false, analysisOnlyDeps{
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
		analyse:             analyse,
		writeMarkdownReport: reports.write,
		printError: func(message string) {
			t.Fatalf("printError called: %s", message)
		},
		writeRunRecord: func(*processor.RunRecord, string) error { return nil },
		writeSidecars:  func(*processor.AudioMeasurements, string) error { return nil },
	})

	got := output.String()

	// stdout starts with the up-front banner (byte-for-byte: single U+2026
	// ellipsis, trailing newline), matching the production main.go no-TTY
	// branch.
	banner := "Analysing 3 files…\n"
	if !strings.HasPrefix(got, banner) {
		t.Fatalf("output does not start with banner %q:\n%s", banner, got)
	}

	// No per-file "Analysing: <file>" line from the old serial format, and no
	// report body on stdout (the report now lives in the .md file).
	if strings.Contains(got, "Analysing: ") {
		t.Fatalf("output contains the removed per-file %q line:\n%s", "Analysing: ", got)
	}
	if strings.Contains(got, "# Audio Processing Report") {
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

	// Each file's full report lands in its own <name>-analysis.md.
	for _, f := range files {
		reportPath := report.AnalysisReportPath(f)
		report, ok := reports.content(reportPath)
		if !ok {
			t.Fatalf("no analysis report at %q", reportPath)
		}
		if !strings.Contains(report, "| Input file | "+f+" |") {
			t.Fatalf("report %q missing input-file row for %q:\n%s", reportPath, f, report)
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
	analyse := func(_ context.Context, path string, _ *processor.BaseFilterConfig, _ processor.ProgressCallback) (*processor.AnalysisResult, error) {
		index, ok := fileIndex[path]
		if !ok {
			t.Fatalf("analyse unexpected path %q", path)
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

	var printErrMu sync.Mutex
	var printErrors []string

	reports := newReportCapture()
	runAnalysisOnlyWithDeps(files, baseConfig, func(string, ...any) {}, 4, false, analysisOnlyDeps{
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
		analyse:             analyse,
		writeMarkdownReport: reports.write,
		printError: func(message string) {
			printErrMu.Lock()
			printErrors = append(printErrors, message)
			printErrMu.Unlock()
		},
		writeRunRecord: func(*processor.RunRecord, string) error { return nil },
		writeSidecars:  func(*processor.AudioMeasurements, string) error { return nil },
	})

	// The failing file reports exactly one error naming the file and "boom".
	if len(printErrors) != 1 {
		t.Fatalf("printError calls = %d (%v), want exactly 1", len(printErrors), printErrors)
	}
	if msg := printErrors[0]; !strings.Contains(msg, files[failIndex]) || !strings.Contains(msg, "boom") {
		t.Fatalf("printError message = %q, want it to mention %q and %q", msg, files[failIndex], "boom")
	}

	// The good siblings each get a report; the failing file gets none (no report
	// written, no confirmation).
	for _, f := range []string{files[0], files[2]} {
		report, ok := reports.content(report.AnalysisReportPath(f))
		if !ok {
			t.Fatalf("missing analysis report for sibling %q", f)
		}
		if !strings.Contains(report, "| Input file | "+f+" |") {
			t.Fatalf("report for sibling %q missing input-file row:\n%s", f, report)
		}
	}
	if _, ok := reports.content(report.AnalysisReportPath(files[failIndex])); ok {
		t.Fatalf("failing file %q must not produce an analysis report", files[failIndex])
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

// reportCapture records Markdown report writes keyed by the requested report
// path, rendering each record so tests assert report content without touching
// the filesystem.
type reportCapture struct {
	mu      sync.Mutex
	reports map[string]string
}

func newReportCapture() *reportCapture {
	return &reportCapture{reports: make(map[string]string)}
}

func (c *reportCapture) write(rec *processor.RunRecord, timings report.Timings, path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reports[path] = report.RenderMarkdown(rec, timings)
	return nil
}

func (c *reportCapture) content(path string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	body, ok := c.reports[path]
	return body, ok
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
		Dynamics: processor.DynamicsMetrics{
			RMSLevel:     -24,
			PeakLevel:    -6,
			DynamicRange: 18,
		},
		Loudness: processor.InputLoudnessMetrics{
			InputI:   -23,
			InputTP:  -1,
			InputLRA: 6,
		},
		Noise: processor.NoiseMetrics{
			Floor:               -50,
			FloorSource:         "rms_estimate",
			FloorPrescan:        -50,
			RoomToneDetectLevel: -45,
		},
	}
}
