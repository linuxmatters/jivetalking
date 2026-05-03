package processor

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func BenchmarkAnalyzeAudioSynthetic5m(b *testing.B) {
	inputPath := generateBenchmarkAudio(b, b.TempDir(), 5*time.Minute)
	defer cleanupTestAudio(b, inputPath)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		config := newTestBaseConfig()
		config.AnalysisEnabled = true
		if _, err := AnalyzeAudio(inputPath, config, nil); err != nil {
			b.Fatalf("AnalyzeAudio failed: %v", err)
		}
	}
}

func BenchmarkProcessAudioDefaultSynthetic5m(b *testing.B) {
	inputPath := generateBenchmarkAudio(b, b.TempDir(), 5*time.Minute)
	defer cleanupTestAudio(b, inputPath)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		config := DefaultFilterConfig()
		result, err := ProcessAudio(inputPath, config, nil)
		if err != nil {
			b.Fatalf("ProcessAudio failed: %v", err)
		}
		if result != nil && result.OutputPath != "" {
			if err := os.Remove(result.OutputPath); err != nil && !os.IsNotExist(err) {
				b.Fatalf("failed to remove benchmark output %s: %v", result.OutputPath, err)
			}
		}
	}
}

func BenchmarkProcessAudioManualFixture(b *testing.B) {
	fixturePath := os.Getenv("JIVETALKING_BENCH_FIXTURE")
	if fixturePath == "" {
		b.Skip("set JIVETALKING_BENCH_FIXTURE to benchmark a real local fixture")
	}

	inputPath := copyBenchmarkFixture(b, fixturePath, b.TempDir())
	defer cleanupTestAudio(b, inputPath)

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		config := DefaultFilterConfig()
		result, err := ProcessAudio(inputPath, config, nil)
		if err != nil {
			b.Fatalf("ProcessAudio failed: %v", err)
		}
		if result != nil && result.OutputPath != "" {
			if err := os.Remove(result.OutputPath); err != nil && !os.IsNotExist(err) {
				b.Fatalf("failed to remove benchmark output %s: %v", result.OutputPath, err)
			}
		}
	}
}

func BenchmarkMeasureOutputRegions(b *testing.B) {
	inputPath := generateBenchmarkAudio(b, b.TempDir(), 5*time.Minute)
	defer cleanupTestAudio(b, inputPath)

	silenceRegion := &SilenceRegion{
		Start:    30 * time.Second,
		End:      31 * time.Second,
		Duration: time.Second,
	}
	speechRegion := &SpeechRegion{
		Start:    90 * time.Second,
		End:      100 * time.Second,
		Duration: 10 * time.Second,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		MeasureOutputRegions(inputPath, silenceRegion, speechRegion)
	}
}

func generateBenchmarkAudio(tb testing.TB, dir string, duration time.Duration) string {
	tb.Helper()

	opts := TestAudioOptions{
		DurationSecs: duration.Seconds(),
		SampleRate:   44100,
		ToneFreq:     180.0,
		ToneLevel:    -18.0,
		NoiseLevel:   -58.0,
		Dir:          dir,
	}
	opts.SilenceGap.Start = 30.0
	opts.SilenceGap.Duration = 1.5

	return generateTestAudio(tb, opts)
}

func copyBenchmarkFixture(tb testing.TB, sourcePath, dir string) string {
	tb.Helper()

	source, err := os.Open(sourcePath) // #nosec G304,G703 -- local benchmark fixture path is explicitly supplied by JIVETALKING_BENCH_FIXTURE.
	if err != nil {
		tb.Fatalf("failed to open benchmark fixture: %v", err)
	}
	defer source.Close()

	destinationPath := filepath.Join(dir, filepath.Base(sourcePath))
	destination, err := os.Create(destinationPath)
	if err != nil {
		tb.Fatalf("failed to create benchmark fixture copy: %v", err)
	}
	defer destination.Close()

	if _, err := io.Copy(destination, source); err != nil {
		tb.Fatalf("failed to copy benchmark fixture: %v", err)
	}

	return destinationPath
}
