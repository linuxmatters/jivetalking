package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
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

	runAnalysisOnlyWithDeps([]string{inputPath}, config, func(string, ...any) {}, analysisOnlyDeps{
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
		runWithTUI: func(string, *processor.BaseFilterConfig, func(string, ...any)) (*processor.AnalysisResult, error) {
			t.Fatal("runWithTUI should not be called for non-TTY output")
			return nil, nil
		},
		analyzeDetailed: func(_ context.Context, path string, cfg *processor.BaseFilterConfig, progress processor.ProgressCallback) (*processor.AnalysisResult, error) {
			if path != inputPath {
				t.Fatalf("analyzeDetailed path = %q, want %q", path, inputPath)
			}
			if progress != nil {
				t.Fatal("progress callback should be nil for non-TTY output")
			}
			effective, diagnostics := processor.AdaptConfig(cfg, makeAnalysisOnlyTestMeasurements())
			return &processor.AnalysisResult{
				Measurements:       makeAnalysisOnlyTestMeasurements(),
				Config:             effective,
				Diagnostics:        diagnostics,
				AnalysisDuration:   2 * time.Second,
				AdaptationDuration: 100 * time.Millisecond,
			}, nil
		},
		displayResults: logging.DisplayAnalysisResultsWithDiagnostics,
		printError: func(message string) {
			t.Fatalf("printError called: %s", message)
		},
	})

	got := output.String()
	if strings.Contains(got, ".bench/") {
		t.Fatalf("analysis-only output leaked benchmark path:\n%s", got)
	}
	for _, want := range []string{
		"Analysing: sample.wav",
		"ANALYSIS: sample.wav",
		"ANALYSIS TIMINGS",
		"Analysis:",
		"Adaptation:",
		"Report Output:",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("analysis-only output missing %q:\n%s", want, got)
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

	runAnalysisOnlyWithDeps(files, baseConfig, func(string, ...any) {}, analysisOnlyDeps{
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
		runWithTUI: func(string, *processor.BaseFilterConfig, func(string, ...any)) (*processor.AnalysisResult, error) {
			t.Fatal("runWithTUI should not be called for non-TTY output")
			return nil, nil
		},
		analyzeDetailed: func(_ context.Context, path string, cfg *processor.BaseFilterConfig, progress processor.ProgressCallback) (*processor.AnalysisResult, error) {
			if cfg != baseConfig {
				t.Fatalf("analyzeDetailed config = %p, want shared base %p", cfg, baseConfig)
			}
			analyzedConfigs = append(analyzedConfigs, cfg)

			index := len(analyzedConfigs) - 1
			return &processor.AnalysisResult{
				Measurements:       makeAnalysisOnlyTestMeasurements(),
				Config:             resultConfigs[index],
				Diagnostics:        resultDiagnostics[index],
				AnalysisDuration:   2 * time.Second,
				AdaptationDuration: 100 * time.Millisecond,
			}, nil
		},
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
	})

	if len(analyzedConfigs) != len(files) {
		t.Fatalf("analyzed config count = %d, want %d", len(analyzedConfigs), len(files))
	}
	if analyzedConfigs[0] != baseConfig || analyzedConfigs[1] != baseConfig {
		t.Fatal("analysis-only did not reuse the shared base config pointer for analysis calls")
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
		name   string
		jobs   int
		numCPU int
		want   int
	}{
		{name: "auto caps at 4 on 8 CPUs", jobs: 0, numCPU: 8, want: 4},
		{name: "auto follows CPU count below 4", jobs: 0, numCPU: 2, want: 2},
		{name: "explicit one stays one", jobs: 1, numCPU: 8, want: 1},
		{name: "explicit huge value honoured uncapped", jobs: 1000, numCPU: 8, want: 1000},
		{name: "negative floors to one", jobs: -3, numCPU: 8, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveJobs(tt.jobs, tt.numCPU); got != tt.want {
				t.Fatalf("resolveJobs(%d, %d) = %d, want %d", tt.jobs, tt.numCPU, got, tt.want)
			}
		})
	}
}

func TestCLI_JobsFlag_ParsesIntoStructField(t *testing.T) {
	fixture := makeFixtureFile(t)
	cliArgs := parseCLIArgs(t, "--jobs", "8", fixture)
	if cliArgs.Jobs != 8 {
		t.Fatalf("Jobs = %d, want 8", cliArgs.Jobs)
	}
}

func TestCLI_JobsFlag_OmittedDefaultsToZero(t *testing.T) {
	fixture := makeFixtureFile(t)
	cliArgs := parseCLIArgs(t, fixture)
	if cliArgs.Jobs != 0 {
		t.Fatalf("Jobs = %d, want 0", cliArgs.Jobs)
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
