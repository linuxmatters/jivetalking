package processor

import (
	"context"
	"strings"
	"testing"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// TestAfftdnCustomSpecParsesInFilterGraph proves the emitted custom afftdn spec
// (nt=custom:bn=<v0>|<v1>|...:nf=<floor>:tn=0) parses and runs in a real ffmpeg
// filter graph. The "|" separators inside the bn value must not break filtergraph
// parsing. Uses synthetic audio written to t.TempDir() (no testdata dependence),
// per the hermetic-suite convention.
func TestAfftdnCustomSpecParsesInFilterGraph(t *testing.T) {
	inputPath := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 1.0,
		ToneFreq:     440,
		ToneLevel:    -20.0,
		NoiseLevel:   -55.0,
		Dir:          t.TempDir(),
	})

	reader, _, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		t.Fatalf("failed to open generated audio: %v", err)
	}
	defer reader.Close()

	nr := defaultNoiseReductionConfig()
	nr.AfftdnNoiseType = "custom"
	// A full 15-value bn vector, the exact format tuneNoiseReduction emits.
	nr.AfftdnBandNoise = "0.0|1.5|-2.0|3.0|-4.0|0.5|0.0|-1.0|2.0|-3.0|1.0|0.0|-0.5|0.5|0.0"
	nr.AfftdnNoiseFloor = -55.0
	nr.AfftdnTrackNoise = false

	spec := nr.buildAfftdnFilter()
	if !strings.Contains(spec, "nt=custom:bn=0.0|1.5|") {
		t.Fatalf("unexpected spec under test: %q", spec)
	}

	graph, src, sink, err := setupFilterGraph(reader.DecoderContext(), spec)
	if err != nil {
		t.Fatalf("custom afftdn spec failed to parse/init in filter graph: %v\nspec: %s", err, spec)
	}
	defer ffmpeg.AVFilterGraphFree(&graph)

	lenient := func(error) error { return nil }
	if err := runFilterGraph(context.Background(), reader, src, sink, FrameLoopConfig{
		OnPushError: lenient,
		OnPullError: lenient,
	}); err != nil {
		t.Fatalf("custom afftdn graph failed to run: %v\nspec: %s", err, spec)
	}
}
