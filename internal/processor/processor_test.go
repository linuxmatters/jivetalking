package processor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
)

// TestGenerateLUFSOutputPath verifies the final LUFS-tagged output path is always FLAC.
func TestGenerateLUFSOutputPath(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"lowercase wav", "/tmp/foo.wav", "/tmp/foo-LUFS-16-processed.flac"},
		{"uppercase WAV", "/tmp/foo.WAV", "/tmp/foo-LUFS-16-processed.flac"},
		{"flac input", "/tmp/foo.flac", "/tmp/foo-LUFS-16-processed.flac"},
		{"mp3 input", "/tmp/foo.mp3", "/tmp/foo-LUFS-16-processed.flac"},
		{"no extension", "/tmp/foo", "/tmp/foo-LUFS-16-processed.flac"},
		{"multi-dot", "/tmp/foo.bar.wav", "/tmp/foo.bar-LUFS-16-processed.flac"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := generateLUFSOutputPath(tc.input, 16)
			if got != tc.want {
				t.Errorf("generateLUFSOutputPath(%q, 16) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCreateSiblingTempPath(t *testing.T) {
	dir := t.TempDir()
	targetPath := filepath.Join(dir, "presenter.wav")

	first, err := createSiblingTempPath(targetPath, "processing")
	if err != nil {
		t.Fatalf("createSiblingTempPath() first call failed: %v", err)
	}
	defer os.Remove(first)

	second, err := createSiblingTempPath(targetPath, "processing")
	if err != nil {
		t.Fatalf("createSiblingTempPath() second call failed: %v", err)
	}
	defer os.Remove(second)

	if first == second {
		t.Fatalf("createSiblingTempPath() returned duplicate path %q", first)
	}

	for _, tempPath := range []string{first, second} {
		if filepath.Dir(tempPath) != dir {
			t.Errorf("temp dir = %q, want %q", filepath.Dir(tempPath), dir)
		}
		if base := filepath.Base(tempPath); !strings.Contains(base, "processing") {
			t.Errorf("temp basename = %q, want marker %q", base, "processing")
		}
		if ext := filepath.Ext(tempPath); ext != ".flac" {
			t.Errorf("temp extension = %q, want .flac", ext)
		}
		if !strings.HasSuffix(tempPath, ".tmp.flac") {
			t.Errorf("temp path = %q, want .tmp.flac suffix", tempPath)
		}

		info, err := os.Stat(tempPath)
		if err != nil {
			t.Fatalf("temp path %q was not reserved: %v", tempPath, err)
		}
		if info.Size() != 0 {
			t.Errorf("temp path %q size = %d, want 0", tempPath, info.Size())
		}
	}
}

func TestRenameNoClobberPublishesSource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.flac")
	dst := filepath.Join(dir, "output.flac")
	want := []byte("published audio")

	if err := os.WriteFile(src, want, 0o600); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	if err := renameNoClobber(src, dst); err != nil {
		t.Fatalf("renameNoClobber() failed: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("destination bytes = %q, want %q", got, want)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Errorf("source stat error = %v, want not exist", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read publish directory: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("publish directory entries = %d, want only destination", len(entries))
	}
	if entries[0].Name() != filepath.Base(dst) {
		t.Fatalf("publish directory entry = %q, want %q", entries[0].Name(), filepath.Base(dst))
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatalf("failed to stat published destination: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("published destination is zero bytes, want source bytes without reservation file")
	}
}

func TestRenameNoClobberRefusesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.flac")
	dst := filepath.Join(dir, "output.flac")
	sourceBytes := []byte("new audio")
	existingBytes := []byte("existing audio")

	if err := os.WriteFile(src, sourceBytes, 0o600); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}
	if err := os.WriteFile(dst, existingBytes, 0o600); err != nil {
		t.Fatalf("failed to write destination: %v", err)
	}

	err := renameNoClobber(src, dst)
	if err == nil {
		t.Fatal("renameNoClobber() succeeded, want destination-exists error")
	}
	if !errors.Is(err, ErrOutputExists) {
		t.Fatalf("renameNoClobber() error = %v, want ErrOutputExists", err)
	}

	var existsErr *DestinationExistsError
	if !errors.As(err, &existsErr) {
		t.Fatalf("renameNoClobber() error = %T, want DestinationExistsError", err)
	}
	if existsErr.Path != dst {
		t.Errorf("DestinationExistsError.Path = %q, want %q", existsErr.Path, dst)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if !bytes.Equal(got, existingBytes) {
		t.Errorf("destination bytes = %q, want preserved %q", got, existingBytes)
	}

	got, err = os.ReadFile(src)
	if err != nil {
		t.Fatalf("failed to read source after failed publish: %v", err)
	}
	if !bytes.Equal(got, sourceBytes) {
		t.Errorf("source bytes = %q, want preserved %q", got, sourceBytes)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read publish directory: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("publish directory entries = %d, want source and destination only", len(entries))
	}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatalf("failed to stat %q: %v", entry.Name(), err)
		}
		if info.Size() == 0 {
			t.Fatalf("publish directory contains zero-byte entry %q", entry.Name())
		}
	}
}

func TestRenameNoClobberConcurrentPublishRace(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "output.flac")

	const publisherCount = 12
	sources := make([]string, publisherCount)
	sourceBytes := make([][]byte, publisherCount)
	for i := range publisherCount {
		sources[i] = filepath.Join(dir, "source-"+string(rune('a'+i))+".flac")
		sourceBytes[i] = []byte(strings.Repeat(string(rune('A'+i)), 64))
		if err := os.WriteFile(sources[i], sourceBytes[i], 0o600); err != nil {
			t.Fatalf("failed to write source %d: %v", i, err)
		}
	}

	start := make(chan struct{})
	errs := make([]error, publisherCount)
	var wg sync.WaitGroup
	wg.Add(publisherCount)
	for i := range publisherCount {
		go func(index int) {
			defer wg.Done()
			<-start
			errs[index] = renameNoClobber(sources[index], dst)
		}(i)
	}

	close(start)
	wg.Wait()

	successIndex := -1
	var successBytes []byte
	for i, err := range errs {
		if err == nil {
			if successIndex != -1 {
				t.Fatalf("publishers %d and %d both succeeded, want exactly one success", successIndex, i)
			}
			successIndex = i
			successBytes = sourceBytes[i]
			continue
		}
		if !errors.Is(err, ErrOutputExists) {
			t.Fatalf("publisher %d error = %v, want ErrOutputExists", i, err)
		}
	}
	if successIndex == -1 {
		t.Fatal("no publisher succeeded, want exactly one success")
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("destination is zero bytes after concurrent publish")
	}
	if !bytes.Equal(got, successBytes) {
		t.Fatalf("destination bytes = %q, want complete source bytes from publisher %d", got, successIndex)
	}

	for i, source := range sources {
		got, err := os.ReadFile(source)
		if i == successIndex {
			if !os.IsNotExist(err) {
				t.Fatalf("winning source stat/read error = %v, want not exist", err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("failed to read losing source %d: %v", i, err)
		}
		if !bytes.Equal(got, sourceBytes[i]) {
			t.Fatalf("losing source %d bytes = %q, want preserved %q", i, got, sourceBytes[i])
		}
	}
}

func TestRenameNoClobberRefusesStaleZeroByteDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.flac")
	dst := filepath.Join(dir, "output.flac")
	sourceBytes := []byte("new audio")

	if err := os.WriteFile(src, sourceBytes, 0o600); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}
	if err := os.WriteFile(dst, nil, 0o600); err != nil {
		t.Fatalf("failed to write stale destination: %v", err)
	}

	err := renameNoClobber(src, dst)
	if err == nil {
		t.Fatal("renameNoClobber() succeeded, want destination-exists error")
	}
	if !errors.Is(err, ErrOutputExists) {
		t.Fatalf("renameNoClobber() error = %v, want ErrOutputExists", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read stale destination: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("stale destination bytes = %q, want unchanged zero-byte file", got)
	}

	got, err = os.ReadFile(src)
	if err != nil {
		t.Fatalf("failed to read source after stale destination collision: %v", err)
	}
	if !bytes.Equal(got, sourceBytes) {
		t.Fatalf("source bytes = %q, want preserved %q", got, sourceBytes)
	}
}

func TestRenameNoClobberPreservesSourceAfterLinkFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.flac")
	dst := filepath.Join(dir, "output.flac")
	linkErr := errors.New("injected link failure")

	if err := os.WriteFile(src, []byte("new audio"), 0o600); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	oldLink := processorLink
	processorLink = func(_, _ string) error {
		return linkErr
	}
	t.Cleanup(func() {
		processorLink = oldLink
	})

	err := renameNoClobber(src, dst)
	if err == nil {
		t.Fatal("renameNoClobber() succeeded, want injected link error")
	}
	if !errors.Is(err, linkErr) {
		t.Fatalf("renameNoClobber() error = %v, want injected link error", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("destination stat error = %v, want not exist", err)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source was not preserved after failed publish: %v", err)
	}
}

func TestRenameNoClobberReportsSourceCleanupFailureAfterPublish(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source.flac")
	dst := filepath.Join(dir, "output.flac")
	want := []byte("published audio")
	removeErr := errors.New("injected remove failure")

	if err := os.WriteFile(src, want, 0o600); err != nil {
		t.Fatalf("failed to write source: %v", err)
	}

	oldRemove := processorRemove
	processorRemove = func(path string) error {
		if path != src {
			t.Fatalf("processorRemove(%q), want %q", path, src)
		}
		return removeErr
	}
	t.Cleanup(func() {
		processorRemove = oldRemove
	})

	err := renameNoClobber(src, dst)
	if err == nil {
		t.Fatal("renameNoClobber() succeeded, want source cleanup error")
	}
	if !errors.Is(err, removeErr) {
		t.Fatalf("renameNoClobber() error = %v, want injected remove error", err)
	}
	if errors.Is(err, ErrOutputExists) {
		t.Fatalf("renameNoClobber() error = %v, want non-collision cleanup error", err)
	}
	if !strings.Contains(err.Error(), src) {
		t.Fatalf("renameNoClobber() error = %v, want source path %q", err, src)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("destination bytes = %q, want %q", got, want)
	}

	got, err = os.ReadFile(src)
	if err != nil {
		t.Fatalf("failed to read source after cleanup failure: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("source bytes = %q, want preserved %q", got, want)
	}
}

func TestEffectiveConfigFilterOrderIsolation(t *testing.T) {
	base := newTestBaseConfig()
	base.FilterOrder = []FilterID{FilterAnalysis, FilterDeesser}
	base.Analysis.SilenceScanDuration = 1500 * time.Millisecond

	first, firstDiagnostics := AdaptConfig(base, &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Centroid: 5000}},
	})
	second, secondDiagnostics := AdaptConfig(base, &AudioMeasurements{
		BaseMeasurements: BaseMeasurements{Spectral: SpectralMetrics{Centroid: 2000}},
	})
	if first == nil || second == nil {
		t.Fatal("AdaptConfig returned nil")
	}
	if firstDiagnostics == nil || secondDiagnostics == nil {
		t.Fatal("AdaptConfig returned nil diagnostics")
	}

	first.FilterOrder[0] = FilterDownmix
	first.Analysis.SilenceScanDuration = 250 * time.Millisecond

	if reflect.DeepEqual(first.FilterOrder, base.FilterOrder) {
		t.Fatal("test setup failed: first effective FilterOrder did not change")
	}
	if !reflect.DeepEqual(base.FilterOrder, []FilterID{FilterAnalysis, FilterDeesser}) {
		t.Errorf("base FilterOrder = %v, want unchanged custom order", base.FilterOrder)
	}
	if !reflect.DeepEqual(second.FilterOrder, []FilterID{FilterAnalysis, FilterDeesser}) {
		t.Errorf("second effective FilterOrder = %v, want independent copy", second.FilterOrder)
	}
	if base.Analysis.SilenceScanDuration != 1500*time.Millisecond {
		t.Errorf("base Analysis.SilenceScanDuration = %v, want unchanged 1.5s",
			base.Analysis.SilenceScanDuration)
	}
	if second.Analysis.SilenceScanDuration != 1500*time.Millisecond {
		t.Errorf("second effective Analysis.SilenceScanDuration = %v, want independent copy",
			second.Analysis.SilenceScanDuration)
	}
}

func TestProcessorSeedParameterOwnershipBoundary(t *testing.T) {
	tests := []struct {
		name       string
		fn         any
		configArg  int
		parameters int
	}{
		{
			name:       "ProcessAudio",
			fn:         ProcessAudio,
			configArg:  1,
			parameters: 3,
		},
		{
			name:       "AnalyzeOnlyDetailed",
			fn:         AnalyzeOnlyDetailed,
			configArg:  1,
			parameters: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typ := reflect.TypeOf(tt.fn)
			if typ.NumIn() != tt.parameters {
				t.Fatalf("%s has %d parameters, want %d", tt.name, typ.NumIn(), tt.parameters)
			}

			assertSeedConfigTypeCannotOwnPerFileState(t, typ.In(tt.configArg))
		})
	}
}

func TestProcessorResultsExposeEffectiveConfigAndDiagnostics(t *testing.T) {
	effectiveConfigType := reflect.TypeFor[*EffectiveFilterConfig]()
	diagnosticsType := reflect.TypeFor[*AdaptiveDiagnostics]()

	tests := []struct {
		name string
		typ  reflect.Type
	}{
		{
			name: "AnalysisResult",
			typ:  reflect.TypeFor[AnalysisResult](),
		},
		{
			name: "ProcessingResult",
			typ:  reflect.TypeFor[ProcessingResult](),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configField, ok := tt.typ.FieldByName("Config")
			if !ok {
				t.Fatalf("%s has no Config field", tt.name)
			}
			if configField.Type != effectiveConfigType {
				t.Fatalf("%s.Config = %s, want %s", tt.name, configField.Type, effectiveConfigType)
			}

			diagnosticsField, ok := tt.typ.FieldByName("Diagnostics")
			if !ok {
				t.Fatalf("%s has no Diagnostics field", tt.name)
			}
			if diagnosticsField.Type != diagnosticsType {
				t.Fatalf("%s.Diagnostics = %s, want %s", tt.name, diagnosticsField.Type, diagnosticsType)
			}
		})
	}
}

// TestProcessAudio tests the complete three-pass processing pipeline
func TestProcessAudio(t *testing.T) {
	// Generate synthetic test audio: 3-second 440Hz tone at -18 dBFS input level
	// (needs to be loud enough for normalisation to be within ±12 dB of -16 LUFS target)
	// Short duration for fast test execution
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 3.0,
		SampleRate:   44100,
		ToneFreq:     440.0, // A4 note
		ToneLevel:    -18.0, // Near broadcast level (-16 LUFS target)
		NoiseLevel:   -55.0, // Moderate background noise
		SilenceGap: struct {
			Start    float64
			Duration float64
		}{
			Start:    1.0, // Brief silence at 1 second
			Duration: 0.3, // 300ms silence gap for noise profiling
		},
	})
	defer cleanupTestAudio(t, testFile)

	// Create isolated test config with minimal filters for integration test
	// This ensures the test doesn't break when application defaults change
	config := newTestBaseConfig()
	config.Downmix.Enabled = true
	config.Analysis.Enabled = true
	config.Resample.Enabled = true
	config.DS201HighPass.Enabled = true // Basic processing
	config.DS201HighPass.Frequency = 95.0
	baseFilterOrder := append([]FilterID(nil), config.FilterOrder...)

	// Process the audio with a no-op progress callback
	result, err := ProcessAudio(testFile, config, func(pass PassNumber, passName string, progress float64, level float64, measurements *AudioMeasurements) {
		// No-op for tests
	})
	if err != nil {
		t.Fatalf("ProcessAudio failed: %v", err)
	}

	// Verify output file was created
	if result.OutputPath == "" {
		t.Fatal("ProcessAudio returned empty output path")
	}

	if _, err := os.Stat(result.OutputPath); os.IsNotExist(err) {
		t.Fatalf("Output file not created: %s", result.OutputPath)
	}

	// Clean up output file (cleanupTestAudio handles this but be explicit)
	defer os.Remove(result.OutputPath)

	// Verify the output extension is .flac regardless of input extension
	if ext := filepath.Ext(result.OutputPath); ext != ".flac" {
		t.Errorf("Output extension = %q, want %q (path: %s)", ext, ".flac", result.OutputPath)
	}

	// Verify output metadata is readable through the supported audio metadata API.
	reader, outputMetadata, err := audio.OpenAudioFile(result.OutputPath)
	if err != nil {
		t.Fatalf("Failed to reopen output file: %v", err)
	}
	defer reader.Close()
	if outputMetadata.SampleRate != config.Resample.SampleRate {
		t.Errorf("Output sample rate = %d, want %d", outputMetadata.SampleRate, config.Resample.SampleRate)
	}
	if outputMetadata.Channels != 1 {
		t.Errorf("Output channels = %d, want 1", outputMetadata.Channels)
	}

	// Verify the file starts with the FLAC magic bytes ("fLaC")
	header, err := os.ReadFile(result.OutputPath)
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}
	if len(header) < 4 || !bytes.Equal(header[:4], []byte{0x66, 0x4C, 0x61, 0x43}) {
		t.Errorf("Output magic bytes = %x, want fLaC (66 4C 61 43)", header[:min(4, len(header))])
	}

	// Verify measurements are populated
	if result.Measurements == nil {
		t.Error("ProcessAudio returned nil measurements")
	}
	if !reflect.DeepEqual(config.FilterOrder, baseFilterOrder) {
		t.Errorf("base FilterOrder = %v, want %v", config.FilterOrder, baseFilterOrder)
	}
	if config.DS201HighPass.Frequency != 95.0 {
		t.Errorf("base DS201HighPass.Frequency = %.1f, want unchanged 95.0", config.DS201HighPass.Frequency)
	}
	if result.Config == nil {
		t.Fatal("ProcessAudio returned nil config")
	}
	if result.Diagnostics == nil {
		t.Fatal("ProcessAudio returned nil diagnostics")
	}
	assertNoStaleEffectiveConfigFields(t)
	if !reflect.DeepEqual(result.Config.FilterOrder, Pass2FilterOrder) {
		t.Errorf("result config FilterOrder = %v, want %v", result.Config.FilterOrder, Pass2FilterOrder)
	}
	if result.Config.DS201HighPass.Frequency == 95.0 {
		t.Fatal("result config DS201HighPass.Frequency did not adapt from base seed value")
	}
	result.Config.FilterOrder[0] = FilterDeesser
	if config.FilterOrder[0] == FilterDeesser {
		t.Fatal("result config FilterOrder mutation changed base FilterOrder")
	}

	// Verify report-needed input metadata is populated from the processing path
	if result.InputMetadata.SampleRate != 44100 {
		t.Errorf("InputMetadata.SampleRate = %d, want 44100", result.InputMetadata.SampleRate)
	}
	if result.InputMetadata.Channels != 1 {
		t.Errorf("InputMetadata.Channels = %d, want 1", result.InputMetadata.Channels)
	}
	if result.InputMetadata.DurationSecs < 2.9 || result.InputMetadata.DurationSecs > 3.1 {
		t.Errorf("InputMetadata.DurationSecs = %.3f, want about 3.0", result.InputMetadata.DurationSecs)
	}

	// Verify filtered measurements are populated (Pass 2 output analysis)
	if result.FilteredMeasurements == nil {
		t.Error("ProcessAudio returned nil FilteredMeasurements")
	} else {
		// Verify silence sample measurements are captured in Pass 2 output (if NoiseProfile exists)
		if result.Measurements != nil && result.Measurements.NoiseProfile != nil {
			t.Logf("NoiseProfile exists: Start=%v, Duration=%v", result.Measurements.NoiseProfile.Start, result.Measurements.NoiseProfile.Duration)
			if result.FilteredMeasurements.SilenceSample == nil {
				t.Error("FilteredMeasurements.SilenceSample is nil despite NoiseProfile existing")
			} else {
				t.Logf("Pass 2 silence sample RMS: %.2f dBFS", result.FilteredMeasurements.SilenceSample.RMSLevel)
				t.Logf("Pass 2 silence sample spectral centroid: %.1f Hz", result.FilteredMeasurements.SilenceSample.Spectral.Centroid)
			}
		} else {
			t.Logf("NoiseProfile is nil - skipping silence sample validation")
		}

		// Verify speech sample measurements are captured in Pass 2 output (if SpeechProfile exists)
		if result.Measurements != nil && result.Measurements.SpeechProfile != nil {
			t.Logf("SpeechProfile exists: Region=%v", result.Measurements.SpeechProfile.Region)
			if result.FilteredMeasurements.SpeechSample == nil {
				t.Error("FilteredMeasurements.SpeechSample is nil despite SpeechProfile existing")
			} else {
				t.Logf("Pass 2 speech sample RMS: %.2f dBFS", result.FilteredMeasurements.SpeechSample.RMSLevel)
				t.Logf("Pass 2 speech sample spectral centroid: %.1f Hz", result.FilteredMeasurements.SpeechSample.Spectral.Centroid)
			}
		} else {
			t.Logf("SpeechProfile is nil - skipping speech sample validation")
		}

		if (result.FilteredMeasurements.SilenceSample != nil || result.FilteredMeasurements.SpeechSample != nil) &&
			result.RegionTimings.FilteredOutput <= 0 {
			t.Error("RegionTimings.FilteredOutput was not captured despite filtered region measurements")
		}
	}

	// Verify final measurements are populated (Pass 4 output analysis after normalisation)
	if result.NormResult != nil && result.NormResult.FinalMeasurements != nil {
		// Verify silence sample measurements are captured in Pass 4 output (if NoiseProfile exists)
		if result.Measurements != nil && result.Measurements.NoiseProfile != nil {
			if result.NormResult.FinalMeasurements.SilenceSample == nil {
				t.Error("FinalMeasurements.SilenceSample is nil despite NoiseProfile existing")
			} else {
				t.Logf("Pass 4 silence sample RMS: %.2f dBFS", result.NormResult.FinalMeasurements.SilenceSample.RMSLevel)
				t.Logf("Pass 4 silence sample spectral centroid: %.1f Hz", result.NormResult.FinalMeasurements.SilenceSample.Spectral.Centroid)
			}
		}

		// Verify speech sample measurements are captured in Pass 4 output (if SpeechProfile exists)
		if result.Measurements != nil && result.Measurements.SpeechProfile != nil {
			if result.NormResult.FinalMeasurements.SpeechSample == nil {
				t.Error("FinalMeasurements.SpeechSample is nil despite SpeechProfile existing")
			} else {
				t.Logf("Pass 4 speech sample RMS: %.2f dBFS", result.NormResult.FinalMeasurements.SpeechSample.RMSLevel)
				t.Logf("Pass 4 speech sample spectral centroid: %.1f Hz", result.NormResult.FinalMeasurements.SpeechSample.Spectral.Centroid)
			}
		}
	} else {
		t.Logf("NormResult or FinalMeasurements is nil - skipping Pass 4 validation")
	}

	if result.NormResult != nil && result.NormResult.FinalMeasurements != nil &&
		(result.NormResult.FinalMeasurements.SilenceSample != nil || result.NormResult.FinalMeasurements.SpeechSample != nil) &&
		result.RegionTimings.FinalOutput <= 0 {
		t.Error("RegionTimings.FinalOutput was not captured despite final region measurements")
	}

	// Log results
	t.Logf("Input LUFS: %.2f", result.InputLUFS)
	t.Logf("Output LUFS: %.2f", result.OutputLUFS)
	t.Logf("Noise Floor: %.2f", result.NoiseFloor)
	t.Logf("Output: %s", result.OutputPath)
}

func TestProcessAudioFinalCollisionPreservesOutputAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 1.5,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -18.0,
		NoiseLevel:   -55.0,
		Dir:          dir,
	})
	defer cleanupTestAudio(t, testFile)

	config := newTestBaseConfig()
	config.Downmix.Enabled = true
	config.Analysis.Enabled = true
	config.Resample.Enabled = true
	config.Loudnorm.Enabled = false
	config.Adeclick.Enabled = false

	var tempPaths []string
	oldCreateSiblingTempPath := processorCreateSiblingTempPath
	processorCreateSiblingTempPath = func(targetPath, marker string) (string, error) {
		tempPath, err := oldCreateSiblingTempPath(targetPath, marker)
		if err == nil && marker == "processing" {
			tempPaths = append(tempPaths, tempPath)
		}
		return tempPath, err
	}
	t.Cleanup(func() {
		processorCreateSiblingTempPath = oldCreateSiblingTempPath
	})

	firstResult, err := ProcessAudio(testFile, config, nil)
	if err != nil {
		t.Fatalf("first ProcessAudio() failed: %v", err)
	}
	if firstResult.OutputPath == "" {
		t.Fatal("first ProcessAudio() returned empty output path")
	}

	existingBytes, err := os.ReadFile(firstResult.OutputPath)
	if err != nil {
		t.Fatalf("failed to read first output: %v", err)
	}

	secondResult, err := ProcessAudio(testFile, config, nil)
	if err == nil {
		if secondResult != nil && secondResult.OutputPath != "" {
			_ = os.Remove(secondResult.OutputPath)
		}
		t.Fatal("second ProcessAudio() succeeded, want final output collision")
	}
	if !errors.Is(err, ErrOutputExists) {
		t.Fatalf("second ProcessAudio() error = %v, want ErrOutputExists", err)
	}
	if !strings.Contains(err.Error(), firstResult.OutputPath) {
		t.Fatalf("second ProcessAudio() error = %v, want final path %q", err, firstResult.OutputPath)
	}
	if count := strings.Count(err.Error(), firstResult.OutputPath); count != 1 {
		t.Fatalf("second ProcessAudio() error contains final path %d times, want 1: %v", count, err)
	}

	gotBytes, err := os.ReadFile(firstResult.OutputPath)
	if err != nil {
		t.Fatalf("failed to read preserved output: %v", err)
	}
	if !bytes.Equal(gotBytes, existingBytes) {
		t.Fatal("existing final output bytes changed after collision")
	}

	assertNoProcessingTempFiles(t, dir)

	if len(tempPaths) != 2 {
		t.Fatalf("recorded processing temp paths = %d, want 2: %v", len(tempPaths), tempPaths)
	}
	if tempPaths[0] == tempPaths[1] {
		t.Fatalf("ProcessAudio() reused processing temp path %q", tempPaths[0])
	}
	for _, tempPath := range tempPaths {
		if filepath.Dir(tempPath) != dir {
			t.Errorf("processing temp dir = %q, want %q", filepath.Dir(tempPath), dir)
		}
		if base := filepath.Base(tempPath); !strings.HasPrefix(base, ".processing-") || !strings.HasSuffix(base, ".tmp.flac") {
			t.Errorf("processing temp basename = %q, want .processing-*.tmp.flac", base)
		}
		if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
			t.Errorf("processing temp stat error = %v, want not exist after publish/collision", err)
		}
	}
}

func TestAnalyzeOnlyDetailedTimings(t *testing.T) {
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 2.0,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -18.0,
		NoiseLevel:   -55.0,
		SilenceGap: struct {
			Start    float64
			Duration float64
		}{
			Start:    1.0,
			Duration: 0.3,
		},
	})
	defer cleanupTestAudio(t, testFile)

	config := newTestBaseConfig()
	config.Downmix.Enabled = true
	config.Analysis.Enabled = true
	config.DS201HighPass.Enabled = true
	config.DS201HighPass.Frequency = 95.0
	baseFilterOrder := append([]FilterID(nil), config.FilterOrder...)

	result, err := AnalyzeOnlyDetailed(testFile, config, nil)
	if err != nil {
		t.Fatalf("AnalyzeOnlyDetailed failed: %v", err)
	}

	if result.Measurements == nil {
		t.Fatal("AnalyzeOnlyDetailed returned nil measurements")
	}
	if result.Config == nil {
		t.Fatal("AnalyzeOnlyDetailed returned nil config")
	}
	if result.Diagnostics == nil {
		t.Fatal("AnalyzeOnlyDetailed returned nil diagnostics")
	}
	assertNoStaleEffectiveConfigFields(t)
	if !reflect.DeepEqual(config.FilterOrder, baseFilterOrder) {
		t.Errorf("base FilterOrder = %v, want %v", config.FilterOrder, baseFilterOrder)
	}
	if config.DS201HighPass.Frequency != 95.0 {
		t.Errorf("base DS201HighPass.Frequency = %.1f, want unchanged 95.0", config.DS201HighPass.Frequency)
	}
	if result.AnalysisDuration <= 0 {
		t.Errorf("AnalysisDuration = %s, want > 0", result.AnalysisDuration)
	}
	if result.AdaptationDuration <= 0 {
		t.Errorf("AdaptationDuration = %s, want > 0", result.AdaptationDuration)
	}
}

// TestFilterChainBuilder tests the filter specification generation
func TestFilterChainBuilder(t *testing.T) {
	// Use isolated test config to avoid coupling to application defaults
	config := newTestConfig()
	config.Downmix.Enabled = true
	config.Analysis.Enabled = true
	config.Resample.Enabled = true

	// Test Pass 1 (analysis) filter spec
	filterSpec := config.BuildFilterSpec()
	t.Logf("Pass 1 filter spec: %s", filterSpec)

	// Should contain filter chain
	if filterSpec == "" {
		t.Error("Filter spec is empty")
	}

	// Enable additional filters for Pass 2 test
	config.DS201HighPass.Enabled = true

	filterSpec = config.BuildFilterSpec()
	t.Logf("Pass 2 filter spec: %s", filterSpec)

	// Should contain enabled filters
	if filterSpec == "" {
		t.Error("Filter spec is empty")
	}
}
