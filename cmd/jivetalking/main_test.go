package main

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
	"github.com/linuxmatters/jivetalking/internal/logging"
	"github.com/linuxmatters/jivetalking/internal/processor"
)

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
			effective, _ := processor.AdaptConfig(cfg, makeAnalysisOnlyTestMeasurements())
			return &processor.AnalysisResult{
				Measurements:       makeAnalysisOnlyTestMeasurements(),
				Config:             effective,
				AnalysisDuration:   2 * time.Second,
				AdaptationDuration: 100 * time.Millisecond,
			}, nil
		},
		displayResults: logging.DisplayAnalysisResults,
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

	var analyzedConfigs []*processor.BaseFilterConfig
	var displayedConfigs []*processor.FilterChainConfig

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
				AnalysisDuration:   2 * time.Second,
				AdaptationDuration: 100 * time.Millisecond,
			}, nil
		},
		displayResults: func(w io.Writer, inputPath string, metadata *audio.Metadata, measurements *processor.AudioMeasurements, config *processor.FilterChainConfig, timings ...logging.AnalysisTimings) {
			displayedConfigs = append(displayedConfigs, config)
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
		if displayedConfigs[i] != &resultConfigs[i].FilterChainConfig {
			t.Fatalf("displayed config %d = %p, want AnalysisResult.Config %p", i, displayedConfigs[i], &resultConfigs[i].FilterChainConfig)
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
