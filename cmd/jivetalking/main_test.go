package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

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
		analyzeDetailed: func(path string, cfg *processor.BaseFilterConfig, progress func(processor.PassNumber, string, float64, float64, *processor.AudioMeasurements)) (*processor.AnalysisResult, error) {
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
	resultConfigs[0].DS201HPFreq = 60.0
	resultConfigs[1].DS201HPFreq = 100.0
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
		analyzeDetailed: func(path string, cfg *processor.BaseFilterConfig, progress func(processor.PassNumber, string, float64, float64, *processor.AudioMeasurements)) (*processor.AnalysisResult, error) {
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
	if baseConfig.DS201HPFreq == resultConfigs[0].DS201HPFreq || baseConfig.DS201HPFreq == resultConfigs[1].DS201HPFreq {
		t.Fatal("test setup failed: result configs should differ from the shared base seed")
	}
}

func makeAnalysisOnlyTestMeasurements() *processor.AudioMeasurements {
	return &processor.AudioMeasurements{
		BaseMeasurements: processor.BaseMeasurements{
			RMSLevel:     -24,
			PeakLevel:    -6,
			DynamicRange: 18,
		},
		InputI:             -23,
		InputTP:            -1,
		InputLRA:           6,
		NoiseFloor:         -50,
		NoiseFloorSource:   "rms_estimate",
		PreScanNoiseFloor:  -50,
		SilenceDetectLevel: -45,
	}
}
