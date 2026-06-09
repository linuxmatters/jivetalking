// Package processor handles audio analysis and processing
package processor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

const loudnormCaptureTestJSON = `{"input_i":"-23.0","input_tp":"-4.0","input_lra":"5.0","input_thresh":"-33.0","output_i":"-16.0","output_tp":"-2.0","output_lra":"5.0","output_thresh":"-26.0","normalization_type":"linear","target_offset":"0.0"}`

// injectLoudnormStatsViaRun swaps loudnormRunFilterGraph for a no-op that writes
// body into the per-call .loudnorm-*.tmp.json stats file the loudnorm pass
// created next to inputPath. Setup is stubbed to a nil filter graph so the
// post-free AVFilterGraphFree is a no-op that writes nothing, and the run hook
// (which fires after setup, before that free) is the sole writer of the stats
// file. parseLoudnormStatsFile then reads the controlled fixture. The optional
// onRun hook runs before the stats write for tests that drive progress.
func injectLoudnormStatsViaRun(
	t *testing.T,
	inputPath, body string,
	onRun func(config FrameLoopConfig) error,
) {
	t.Helper()

	replaceApplyLoudnormSetupFilterGraph(t, func(
		*ffmpeg.AVCodecContext,
		string,
	) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
		return nil, nil, nil, nil
	})

	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		_ context.Context,
		_ *audio.Reader,
		_, _ *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		if onRun != nil {
			if err := onRun(config); err != nil {
				return err
			}
		}
		writeLoudnormStatsForInput(t, inputPath, body)
		return nil
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})
}

func TestMeasureWithLoudnormReturnsOpenError(t *testing.T) {
	_, err := measureWithLoudnorm(context.Background(), "/does/not/exist.wav", defaultNormalisationTestConfig(), "", nil)
	if err == nil {
		t.Fatal("measureWithLoudnorm() error = nil, want open error")
	}
	if !strings.Contains(err.Error(), "failed to open input") {
		t.Fatalf("measureWithLoudnorm() error = %q, want open context", err.Error())
	}
}

func TestMeasureWithLoudnormReturnsSetupError(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)

	_, err := measureWithLoudnorm(context.Background(), testFile, defaultNormalisationTestConfig(), "not_a_real_filter", nil)
	if err == nil {
		t.Fatal("measureWithLoudnorm() error = nil, want setup error")
	}
	if !strings.Contains(err.Error(), "failed to create filter graph") {
		t.Fatalf("measureWithLoudnorm() error = %q, want filter graph context", err.Error())
	}
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestMeasureWithLoudnormLoopErrorTakesPrecedenceOverStats(t *testing.T) {
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 0.2,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -18.0,
		Dir:          t.TempDir(),
	})
	defer cleanupTestAudio(t, testFile)

	runErr := errors.New("injected frame loop failure")
	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		_ context.Context,
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return runErr
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	_, err := measureWithLoudnorm(context.Background(), testFile, defaultNormalisationTestConfig(), "", nil)
	if !errors.Is(err, runErr) {
		t.Fatalf("measureWithLoudnorm() error = %v, want wrapped run error", err)
	}
	if !strings.Contains(err.Error(), "loudnorm measurement loop failed") {
		t.Fatalf("measureWithLoudnorm() error = %q, want measurement loop context", err.Error())
	}
	if strings.Contains(err.Error(), "failed to read loudnorm stats file") {
		t.Fatalf("measureWithLoudnorm() error = %q, want loop error precedence over stats-file error", err.Error())
	}
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestMeasureWithLoudnormEmptyStatsFileFails(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	injectLoudnormStatsViaRun(t, testFile, "", nil)

	_, err := measureWithLoudnorm(context.Background(), testFile, defaultNormalisationTestConfig(), "", nil)
	if err == nil {
		t.Fatal("measureWithLoudnorm() error = nil, want missing JSON error")
	}
	if !strings.Contains(err.Error(), "failed to read loudnorm stats file") {
		t.Fatalf("measureWithLoudnorm() error = %q, want stats-file context", err.Error())
	}
	if !strings.Contains(err.Error(), "no JSON found in loudnorm stats file") {
		t.Fatalf("measureWithLoudnorm() error = %q, want missing JSON context", err.Error())
	}
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestMeasureWithLoudnormMalformedStatsFileFails(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	injectLoudnormStatsViaRun(t, testFile, "{not-json}", nil)

	_, err := measureWithLoudnorm(context.Background(), testFile, defaultNormalisationTestConfig(), "", nil)
	if err == nil {
		t.Fatal("measureWithLoudnorm() error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), "failed to read loudnorm stats file") {
		t.Fatalf("measureWithLoudnorm() error = %q, want stats-file context", err.Error())
	}
	if !strings.Contains(err.Error(), "failed to parse loudnorm JSON") {
		t.Fatalf("measureWithLoudnorm() error = %q, want malformed JSON context", err.Error())
	}
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestMeasureWithLoudnormParsesStatsFile(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	injectLoudnormStatsViaRun(t, testFile, loudnormCaptureTestJSON, nil)

	measurement, err := measureWithLoudnorm(context.Background(), testFile, defaultNormalisationTestConfig(), "", nil)
	if err != nil {
		t.Fatalf("measureWithLoudnorm() error = %v", err)
	}

	want := loudnormMeasurementFromFixture(t, loudnormCaptureTestJSON)
	if *measurement != *want {
		t.Fatalf("measureWithLoudnorm() = %+v, want %+v (live parse of fixture)", *measurement, *want)
	}
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestMeasureWithLoudnormRejectsInvalidNumericField(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)

	badJSON := strings.Replace(loudnormCaptureTestJSON, `"input_i":"-23.0"`, `"input_i":"not-a-number"`, 1)
	injectLoudnormStatsViaRun(t, testFile, badJSON, nil)

	_, err := measureWithLoudnorm(context.Background(), testFile, defaultNormalisationTestConfig(), "", nil)
	if err == nil {
		t.Fatal("measureWithLoudnorm() error = nil, want invalid numeric field error")
	}
	if !strings.Contains(err.Error(), `invalid loudnorm input_i value "not-a-number"`) {
		t.Fatalf("measureWithLoudnorm() error = %q, want input_i parse context", err.Error())
	}
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestMeasureWithLoudnormStatsFileYieldsFiniteMeasurement(t *testing.T) {
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 0.5,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -18.0,
		Dir:          t.TempDir(),
	})
	t.Cleanup(func() {
		cleanupTestAudio(t, testFile)
	})

	injectLoudnormStatsViaRun(t, testFile, loudnormCaptureTestJSON, nil)

	measurement, err := measureWithLoudnorm(context.Background(), testFile, defaultNormalisationTestConfig(), "", nil)
	if err != nil {
		t.Fatalf("measureWithLoudnorm() error = %v", err)
	}
	if measurement == nil {
		t.Fatal("measureWithLoudnorm() measurement = nil, want measurement from stats file")
	}

	values := map[string]float64{
		"InputI":       measurement.InputI,
		"InputTP":      measurement.InputTP,
		"InputLRA":     measurement.InputLRA,
		"InputThresh":  measurement.InputThresh,
		"TargetOffset": measurement.TargetOffset,
	}
	for name, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			t.Fatalf("measurement.%s = %v, want finite value from stats file", name, value)
		}
	}
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestMeasureWithLoudnormProgressCadenceCapsAt099(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	injectLoudnormStatsViaRun(t, testFile, loudnormCaptureTestJSON, func(config FrameLoopConfig) error {
		frame := ffmpeg.AVFrameAlloc()
		defer ffmpeg.AVFrameFree(&frame)
		frame.SetNbSamples(44100)

		for range 100 {
			config.OnInputFrame(frame)
		}
		return nil
	})

	var events []loudnormProgressEvent
	_, err := measureWithLoudnorm(context.Background(), testFile, defaultNormalisationTestConfig(), "", func(update ProgressUpdate) {
		events = append(events, loudnormProgressEvent{pass: update.Pass, passName: update.PassName, progress: update.Progress})
	})
	if err != nil {
		t.Fatalf("measureWithLoudnorm() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("progress events = %+v, want one cadence event", events)
	}
	requireLoudnormProgressEvent(t, events[0], loudnormProgressEvent{
		pass:     PassMeasuring,
		passName: "Measuring",
		progress: 0.99,
	})
	requireNoLoudnormStatsFiles(t, testFile)
}

// requireNoLoudnormStatsFiles asserts the per-call .loudnorm-*.tmp.json stats
// file was unlinked, so no stats residue survives success or error (proposal AC
// 5). The deferred unlink in measureWithLoudnorm/applyLoudnormAndMeasure removes
// it on every path.
func requireNoLoudnormStatsFiles(t *testing.T, inputPath string) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(filepath.Dir(inputPath), ".loudnorm-*.tmp.json"))
	if err != nil {
		t.Fatalf("glob loudnorm stats files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("loudnorm stats files remain: %v", matches)
	}
}

// injectPass4StatsViaRun stubs the Pass 4 apply graph so the per-call
// .loudnorm-*.tmp.json stats file holds the controlled body when
// applyLoudnormAndMeasure reads it post-free. Setup returns a nil filter graph
// (so the post-free AVFilterGraphFree writes nothing), the encoder is a no-op
// fake, and the run hook drives one ebur128-metadata frame through OnFrame so
// the output accumulators populate (non-nil finalMeasurements) before writing
// body into the stats file. The caller still supplies a rename stub.
func injectPass4StatsViaRun(t *testing.T, inputPath, body string) {
	t.Helper()

	replaceApplyLoudnormSetupFilterGraph(t, func(
		*ffmpeg.AVCodecContext,
		string,
	) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
		return nil, nil, nil, nil
	})
	replaceApplyLoudnormCreateEncoder(t, func(
		string,
		*audio.Metadata,
		*ffmpeg.AVFilterContext,
	) (loudnormOutputEncoder, error) {
		return &loudnormTestEncoder{}, nil
	})

	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		_ context.Context,
		_ *audio.Reader,
		_, _ *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		frame := newEbur128MetadataFrame(t)
		defer ffmpeg.AVFrameFree(&frame)
		if err := config.OnFrame(frame, frame); err != nil {
			return err
		}
		writeLoudnormStatsForInput(t, inputPath, body)
		return nil
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})
}

// newEbur128MetadataFrame allocates a frame whose metadata carries an ebur128
// integrated-loudness key, so extractOutputFrameMetadata marks ebur128Found and
// finalizeOutputMeasurements returns a non-nil OutputMeasurements. Used to give
// nil-graph apply tests populated output measurements without a real graph.
func newEbur128MetadataFrame(t *testing.T) *ffmpeg.AVFrame {
	t.Helper()

	frame := ffmpeg.AVFrameAlloc()
	key := ffmpeg.ToCStr("lavfi.r128.I")
	defer key.Free()
	value := ffmpeg.ToCStr("-16.0")
	defer value.Free()

	metadata := frame.Metadata()
	if _, err := ffmpeg.AVDictSet(&metadata, key, value, 0); err != nil {
		ffmpeg.AVFrameFree(&frame)
		t.Fatalf("AVDictSet() error = %v", err)
	}
	frame.SetMetadata(metadata)
	return frame
}

// writeFixtureStatsFile writes body to a fresh sibling stats path and returns it
// so a test can compare against the live parseLoudnormStatsFile output rather than
// a re-typed expectation.
func writeFixtureStatsFile(t *testing.T, body string) string {
	t.Helper()

	statsPath, err := createSiblingStatsPath(filepath.Join(t.TempDir(), "fixture.flac"), "loudnorm")
	if err != nil {
		t.Fatalf("createSiblingStatsPath() error = %v", err)
	}
	if err := os.WriteFile(statsPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return statsPath
}

// loudnormMeasurementFromFixture parses body through the live parseLoudnormStatsFile
// over a sibling temp file, then converts the string fields to a LoudnormMeasurement
// the same way measureWithLoudnorm does. Tests compare measureWithLoudnorm's result
// against this live parse rather than a re-typed expectation.
func loudnormMeasurementFromFixture(t *testing.T, body string) *LoudnormMeasurement {
	t.Helper()

	statsPath, err := createSiblingStatsPath(filepath.Join(t.TempDir(), "fixture.flac"), "loudnorm")
	if err != nil {
		t.Fatalf("createSiblingStatsPath() error = %v", err)
	}
	if err := os.WriteFile(statsPath, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	stats, err := parseLoudnormStatsFile(statsPath)
	if err != nil {
		t.Fatalf("parseLoudnormStatsFile() error = %v", err)
	}

	parseFloat := func(name, value string) float64 {
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			t.Fatalf("parse %s value %q: %v", name, value, err)
		}
		return parsed
	}

	return &LoudnormMeasurement{
		InputI:       parseFloat("input_i", stats.InputI),
		InputTP:      parseFloat("input_tp", stats.InputTP),
		InputLRA:     parseFloat("input_lra", stats.InputLRA),
		InputThresh:  parseFloat("input_thresh", stats.InputThresh),
		TargetOffset: parseFloat("target_offset", stats.TargetOffset),
	}
}

// writeLoudnormStatsForInput writes body into the per-call loudnorm stats file
// that measureWithLoudnorm created next to inputPath (.loudnorm-*.tmp.json),
// overwriting whatever the real frameless graph emitted. This mirrors what
// FFmpeg 8.1's loudnorm uninit() does on graph free, so the stats_file readback
// observes the controlled fixture. Call it from inside the free seam.
func writeLoudnormStatsForInput(t *testing.T, inputPath, body string) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(filepath.Dir(inputPath), ".loudnorm-*.tmp.json"))
	if err != nil {
		t.Fatalf("glob loudnorm stats file: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("loudnorm stats file count = %d, want 1 (%v)", len(matches), matches)
	}
	if err := os.WriteFile(matches[0], []byte(body), 0o600); err != nil {
		t.Fatalf("write loudnorm stats file: %v", err)
	}
}

func replaceApplyLoudnormSetupFilterGraph(
	t *testing.T,
	setup func(*ffmpeg.AVCodecContext, string) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error),
) {
	t.Helper()

	oldSetup := loudnormSetupFilterGraph
	loudnormSetupFilterGraph = setup
	t.Cleanup(func() {
		loudnormSetupFilterGraph = oldSetup
	})
}

func replaceApplyLoudnormCreateEncoder(
	t *testing.T,
	create func(string, *audio.Metadata, *ffmpeg.AVFilterContext) (loudnormOutputEncoder, error),
) {
	t.Helper()

	oldCreate := loudnormCreateEncoder
	loudnormCreateEncoder = create
	t.Cleanup(func() {
		loudnormCreateEncoder = oldCreate
	})
}

func replaceApplyLoudnormRename(t *testing.T, rename func(string, string) error) {
	t.Helper()

	oldRename := loudnormRename
	loudnormRename = rename
	t.Cleanup(func() {
		loudnormRename = oldRename
	})
}

func replaceLoudnormRunFilterGraph(
	t *testing.T,
	run func(context.Context, *audio.Reader, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, FrameLoopConfig) error,
) {
	t.Helper()

	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = run
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})
}

type loudnormTestEncoder struct {
	writeFrame func(*ffmpeg.AVFrame) error
	flush      func() error
	close      func() error
	flushErr   error
	closeErr   error
	closed     bool
	closeN     int
}

func (e *loudnormTestEncoder) WriteFrame(frame *ffmpeg.AVFrame) error {
	if e.writeFrame != nil {
		return e.writeFrame(frame)
	}
	return nil
}

func (e *loudnormTestEncoder) Flush() error {
	if e.flush != nil {
		return e.flush()
	}
	return e.flushErr
}

func (e *loudnormTestEncoder) Close() error {
	e.closeN++
	if e.close != nil {
		return e.close()
	}
	if e.closed {
		return nil
	}
	e.closed = true
	return e.closeErr
}

func defaultNormalisationTestConfig() *EffectiveFilterConfig {
	return DefaultEffectiveFilterConfig()
}

func loudnormApplicationTestConfig() *EffectiveFilterConfig {
	config := defaultNormalisationTestConfig()
	config.Adeclick.Enabled = false
	return config
}

func loudnormApplicationTestMeasurement() *LoudnormMeasurement {
	return &LoudnormMeasurement{
		InputI:       -23.0,
		InputTP:      -4.0,
		InputLRA:     5.0,
		InputThresh:  -33.0,
		TargetOffset: 0.0,
	}
}

func generateLoudnormApplicationTestAudio(t *testing.T) string {
	t.Helper()

	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 0.2,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -18.0,
		Dir:          t.TempDir(),
	})
	t.Cleanup(func() {
		cleanupTestAudio(t, testFile)
	})
	return testFile
}

type loudnormProgressEvent struct {
	pass     PassNumber
	passName string
	progress float64
}

func requireLoudnormProgressEvent(t *testing.T, got loudnormProgressEvent, want loudnormProgressEvent) {
	t.Helper()

	if got.pass != want.pass {
		t.Fatalf("progress pass = %d, want %d", got.pass, want.pass)
	}
	if got.passName != want.passName {
		t.Fatalf("progress passName = %q, want %q", got.passName, want.passName)
	}
	if math.Abs(got.progress-want.progress) > 0.000001 {
		t.Fatalf("progress value = %.6f, want %.6f", got.progress, want.progress)
	}
}

func oldFixedLoudnormTempPath(inputPath string) string {
	return strings.TrimSuffix(inputPath, filepath.Ext(inputPath)) + ".loudnorm.tmp.flac"
}

func requireLoudnormTempPath(t *testing.T, inputPath, tempPath string) {
	t.Helper()

	if filepath.Dir(tempPath) != filepath.Dir(inputPath) {
		t.Fatalf("temp file dir = %q, want %q", filepath.Dir(tempPath), filepath.Dir(inputPath))
	}
	base := filepath.Base(tempPath)
	if !strings.HasPrefix(base, ".loudnorm-") || !strings.HasSuffix(base, ".tmp.flac") {
		t.Fatalf("temp file basename = %q, want .loudnorm-*.tmp.flac", base)
	}
	if tempPath == oldFixedLoudnormTempPath(inputPath) {
		t.Fatalf("temp file path = %q, want non-fixed loudnorm temp path", tempPath)
	}
}

func requireNoLoudnormTempFiles(t *testing.T, inputPath string) {
	t.Helper()

	matches, err := filepath.Glob(filepath.Join(filepath.Dir(inputPath), ".loudnorm-*.tmp.flac"))
	if err != nil {
		t.Fatalf("failed to glob loudnorm temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("loudnorm temp files remain: %v", matches)
	}
	if _, err := os.Stat(oldFixedLoudnormTempPath(inputPath)); !os.IsNotExist(err) {
		t.Fatalf("old fixed loudnorm temp stat error = %v, want not exist", err)
	}
}

func applyLoudnormTest(
	t *testing.T,
	inputPath string,
) (*loudnormApplicationResult, error) {
	t.Helper()

	return applyLoudnormAndMeasure(context.Background(), loudnormApplicationRequest{
		inputPath:   inputPath,
		config:      loudnormApplicationTestConfig(),
		measurement: loudnormApplicationTestMeasurement(),
	}, nil)
}

func TestApplyLoudnormAndMeasureReturnsOpenError(t *testing.T) {
	_, err := applyLoudnormTest(t, "/does/not/exist.wav")
	if err == nil {
		t.Fatal("applyLoudnormAndMeasure() error = nil, want open error")
	}
	if !strings.Contains(err.Error(), "failed to open input") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want open context", err.Error())
	}
}

func TestApplyLoudnormAndMeasureSetupErrorRemovesTempAndStats(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)

	setupErr := errors.New("injected setup failure")
	replaceApplyLoudnormSetupFilterGraph(t, func(
		*ffmpeg.AVCodecContext,
		string,
	) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
		return nil, nil, nil, setupErr
	})

	_, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, setupErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want wrapped setup error", err)
	}
	if !strings.Contains(err.Error(), "failed to create filter graph") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want filter graph context", err.Error())
	}
	requireNoLoudnormTempFiles(t, testFile)
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureEncoderCreationErrorRemovesTemp(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)

	createErr := errors.New("injected encoder creation failure")
	var tempPath string
	replaceApplyLoudnormCreateEncoder(t, func(
		outputPath string,
		_ *audio.Metadata,
		_ *ffmpeg.AVFilterContext,
	) (loudnormOutputEncoder, error) {
		tempPath = outputPath
		requireLoudnormTempPath(t, testFile, outputPath)
		return nil, createErr
	})

	_, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, createErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want wrapped encoder creation error", err)
	}
	if !strings.Contains(err.Error(), "failed to create encoder") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want encoder context", err.Error())
	}
	if tempPath == "" {
		t.Fatal("encoder was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after encoder creation failure", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureLoopErrorRemovesTemp(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	encoder := &loudnormTestEncoder{}
	var tempPath string
	replaceApplyLoudnormCreateEncoder(t, func(
		outputPath string,
		_ *audio.Metadata,
		_ *ffmpeg.AVFilterContext,
	) (loudnormOutputEncoder, error) {
		tempPath = outputPath
		requireLoudnormTempPath(t, testFile, outputPath)
		return encoder, nil
	})

	runErr := errors.New("injected application loop failure")
	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		_ context.Context,
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return runErr
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	_, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, runErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want loop error", err)
	}
	if encoder.closeN != 1 {
		t.Fatalf("encoder close calls = %d, want 1", encoder.closeN)
	}
	if tempPath == "" {
		t.Fatal("encoder was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after loop failure", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureFlushErrorRemovesTemp(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)

	flushErr := errors.New("injected flush failure")
	encoder := &loudnormTestEncoder{flushErr: flushErr}
	var tempPath string
	replaceApplyLoudnormCreateEncoder(t, func(
		outputPath string,
		_ *audio.Metadata,
		_ *ffmpeg.AVFilterContext,
	) (loudnormOutputEncoder, error) {
		tempPath = outputPath
		requireLoudnormTempPath(t, testFile, outputPath)
		return encoder, nil
	})

	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		_ context.Context,
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return nil
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	_, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, flushErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want wrapped flush error", err)
	}
	if !strings.Contains(err.Error(), "failed to flush encoder") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want flush context", err.Error())
	}
	if encoder.closeN != 1 {
		t.Fatalf("encoder close calls = %d, want 1", encoder.closeN)
	}
	if tempPath == "" {
		t.Fatal("encoder was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after flush failure", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureCloseErrorRemovesTemp(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)

	closeErr := errors.New("injected close failure")
	encoder := &loudnormTestEncoder{closeErr: closeErr}
	var tempPath string
	replaceApplyLoudnormCreateEncoder(t, func(
		outputPath string,
		_ *audio.Metadata,
		_ *ffmpeg.AVFilterContext,
	) (loudnormOutputEncoder, error) {
		tempPath = outputPath
		requireLoudnormTempPath(t, testFile, outputPath)
		return encoder, nil
	})

	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		_ context.Context,
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return nil
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	_, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, closeErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want wrapped close error", err)
	}
	if !strings.Contains(err.Error(), "failed to close encoder") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want close context", err.Error())
	}
	if encoder.closeN != 1 {
		t.Fatalf("encoder close calls = %d, want 1", encoder.closeN)
	}
	if tempPath == "" {
		t.Fatal("encoder was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after close failure", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureRenameErrorRemovesTemp(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	encoder := &loudnormTestEncoder{}
	replaceApplyLoudnormCreateEncoder(t, func(
		string,
		*audio.Metadata,
		*ffmpeg.AVFilterContext,
	) (loudnormOutputEncoder, error) {
		return encoder, nil
	})

	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		_ context.Context,
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return nil
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	renameErr := errors.New("injected rename failure")
	var tempPath string
	replaceApplyLoudnormRename(t, func(oldPath, newPath string) error {
		tempPath = oldPath
		requireLoudnormTempPath(t, testFile, oldPath)
		if newPath != testFile {
			t.Fatalf("rename target = %q, want %q", newPath, testFile)
		}
		return renameErr
	})

	_, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, renameErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want wrapped rename error", err)
	}
	if !strings.Contains(err.Error(), "failed to rename output") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want rename context", err.Error())
	}
	if encoder.closeN != 1 {
		t.Fatalf("encoder close calls = %d, want 1", encoder.closeN)
	}
	if tempPath == "" {
		t.Fatal("rename was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after rename failure", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureSuccessParsesStatsFile(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)

	// Write the fixture into the per-call stats file from the run hook (after
	// graph setup, before the post-free readback), so applyLoudnormAndMeasure
	// parses the controlled fixture as its Pass 4 LoudnormStats.
	injectPass4StatsViaRun(t, testFile, loudnormCaptureTestJSON)

	var tempPath string
	replaceApplyLoudnormRename(t, func(oldPath, newPath string) error {
		tempPath = oldPath
		requireLoudnormTempPath(t, testFile, oldPath)
		return os.Rename(oldPath, newPath)
	})

	result, err := applyLoudnormTest(t, testFile)
	if err != nil {
		t.Fatalf("applyLoudnormAndMeasure() error = %v", err)
	}
	if result == nil {
		t.Fatal("loudnorm application result = nil")
	}
	stats := result.loudnormStats
	if stats == nil {
		t.Fatal("loudnorm stats = nil, want parsed stats")
	}

	want, err := parseLoudnormStatsFile(writeFixtureStatsFile(t, loudnormCaptureTestJSON))
	if err != nil {
		t.Fatalf("parseLoudnormStatsFile() error = %v", err)
	}
	if *stats != *want {
		t.Fatalf("loudnorm stats = %+v, want %+v (live parse of fixture)", *stats, *want)
	}

	finalMeasurements := result.finalMeasurements
	if finalMeasurements == nil {
		t.Fatal("final measurements = nil, want measurements")
	}
	if math.IsNaN(result.finalLUFS) {
		t.Fatal("final LUFS is NaN")
	}
	if tempPath == "" {
		t.Fatal("rename was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after successful rename", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureMissingPass4StatsReturnsNilStatsWithoutError(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)

	// Empty the per-call stats file so the post-free readback fails: nil stats,
	// no error (the report's Norm Type diagnostic is omitted).
	injectPass4StatsViaRun(t, testFile, "")

	replaceApplyLoudnormRename(t, func(oldPath, newPath string) error {
		requireLoudnormTempPath(t, testFile, oldPath)
		return os.Rename(oldPath, newPath)
	})

	result, err := applyLoudnormTest(t, testFile)
	if err != nil {
		t.Fatalf("applyLoudnormAndMeasure() error = %v", err)
	}
	if result == nil {
		t.Fatal("loudnorm application result = nil")
	}
	if result.loudnormStats != nil {
		t.Fatalf("loudnorm stats = %+v, want nil", result.loudnormStats)
	}
	if result.finalMeasurements == nil {
		t.Fatal("final measurements = nil, want measurements")
	}
	requireNoLoudnormTempFiles(t, testFile)
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureMalformedPass4StatsReturnsNilStatsWithoutError(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)

	// Malformed stats so the post-free readback fails to parse: nil stats, no error.
	injectPass4StatsViaRun(t, testFile, "{not-json}")

	replaceApplyLoudnormRename(t, func(oldPath, newPath string) error {
		requireLoudnormTempPath(t, testFile, oldPath)
		return os.Rename(oldPath, newPath)
	})

	result, err := applyLoudnormTest(t, testFile)
	if err != nil {
		t.Fatalf("applyLoudnormAndMeasure() error = %v", err)
	}
	if result == nil {
		t.Fatal("loudnorm application result = nil")
	}
	if result.loudnormStats != nil {
		t.Fatalf("loudnorm stats = %+v, want nil", result.loudnormStats)
	}
	if result.finalMeasurements == nil {
		t.Fatal("final measurements = nil, want measurements")
	}
	requireNoLoudnormTempFiles(t, testFile)
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestApplyNormalisationProgressCadenceGuard(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	replaceApplyLoudnormSetupFilterGraph(t, func(
		*ffmpeg.AVCodecContext,
		string,
	) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
		return nil, nil, nil, nil
	})
	replaceApplyLoudnormCreateEncoder(t, func(
		string,
		*audio.Metadata,
		*ffmpeg.AVFilterContext,
	) (loudnormOutputEncoder, error) {
		return &loudnormTestEncoder{}, nil
	})
	replaceApplyLoudnormRename(t, func(oldPath, _ string) error {
		return os.Remove(oldPath)
	})

	var runCalls int
	replaceLoudnormRunFilterGraph(t, func(
		_ context.Context,
		_ *audio.Reader,
		_, _ *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		runCalls++
		frame := ffmpeg.AVFrameAlloc()
		defer ffmpeg.AVFrameFree(&frame)
		frame.SetNbSamples(44100)

		for range 100 {
			config.OnInputFrame(frame)
			if runCalls == 2 {
				if err := config.OnFrame(frame, frame); err != nil {
					return err
				}
			}
		}
		// Each pass (Pass 3 measure, Pass 4 apply) writes its own per-call stats
		// file; feed the fixture so the Pass 3 measurement parse succeeds.
		writeLoudnormStatsForInput(t, testFile, loudnormCaptureTestJSON)
		return nil
	})

	var events []loudnormProgressEvent
	_, err := ApplyNormalisation(
		context.Background(),
		testFile,
		defaultNormalisationTestConfig(),
		&OutputMeasurements{OutputI: -20.0, OutputTP: -10.0},
		nil,
		func(update ProgressUpdate) {
			events = append(events, loudnormProgressEvent{pass: update.Pass, passName: update.PassName, progress: update.Progress})
		},
		nil,
	)
	if err != nil {
		t.Fatalf("ApplyNormalisation() error = %v", err)
	}
	if runCalls != 2 {
		t.Fatalf("loudnorm run calls = %d, want 2", runCalls)
	}

	want := []loudnormProgressEvent{
		{pass: PassMeasuring, passName: "Measuring", progress: 0.0},
		{pass: PassMeasuring, passName: "Measuring", progress: 0.99},
		{pass: PassMeasuring, passName: "Measuring", progress: 1.0},
		{pass: PassNormalising, passName: "Normalising", progress: 0.0},
		{pass: PassNormalising, passName: "Normalising", progress: 0.99},
		{pass: PassNormalising, passName: "Normalising", progress: 1.0},
	}
	if len(events) != len(want) {
		t.Fatalf("progress events = %+v, want %+v", events, want)
	}
	for i := range want {
		requireLoudnormProgressEvent(t, events[i], want[i])
	}
	requireNoLoudnormTempFiles(t, testFile)
	requireNoLoudnormStatsFiles(t, testFile)
}

func TestCalculateLinearModeTarget(t *testing.T) {
	// Note: calculateLinearModeTarget includes a 0.1 dB safety margin to ensure
	// we stay safely within linear mode bounds, accounting for floating point
	// precision differences between Go and FFmpeg's internal calculations.
	const margin = 0.1

	tests := []struct {
		name               string
		measuredI          float64
		measuredTP         float64
		desiredI           float64
		targetTP           float64
		wantEffectiveI     float64
		wantOffset         float64
		wantLinearPossible bool
	}{
		{
			name:       "linear mode requires target adjustment - peak limited",
			measuredI:  -20.0,
			measuredTP: -5.0, // 3.5 dB headroom to target TP
			desiredI:   -16.0,
			targetTP:   -1.5,
			// max linear: -1.5 - (-5.0) + (-20.0) - 0.1 = -16.6 LUFS (with margin)
			// desired -16.0 > -16.6 (louder than max), so adjustment needed
			wantEffectiveI:     -16.5 - margin,
			wantOffset:         3.5 - margin, // -16.6 - (-20) = 3.4 dB gain
			wantLinearPossible: false,
		},
		{
			name:               "linear mode requires target adjustment - severely peak limited",
			measuredI:          -20.0,
			measuredTP:         -2.0, // Only 0.5 dB headroom
			desiredI:           -16.0,
			targetTP:           -1.5,
			wantEffectiveI:     -19.5 - margin, // max linear: -1.5 - (-2.0) + (-20.0) - 0.1 = -19.6
			wantOffset:         0.5 - margin,   // -19.6 - (-20) = 0.4 dB gain
			wantLinearPossible: false,
		},
		{
			name:       "already at target with headroom",
			measuredI:  -16.0,
			measuredTP: -3.0,
			desiredI:   -16.0,
			targetTP:   -1.5,
			// max linear: -1.5 - (-3.0) + (-16.0) - 0.1 = -14.6 LUFS (louder than desired)
			// desired -16.0 <= -14.6, so achievable
			wantEffectiveI:     -16.0,
			wantOffset:         0.0,
			wantLinearPossible: true,
		},
		{
			name:       "needs attenuation - always achievable",
			measuredI:  -12.0,
			measuredTP: -1.0,
			desiredI:   -16.0,
			targetTP:   -1.5,
			// max linear: -1.5 - (-1.0) + (-12.0) - 0.1 = -12.6 LUFS
			// desired -16.0 < -12.6 (quieter), so achievable
			wantEffectiveI:     -16.0,
			wantOffset:         -4.0, // -16 - (-12) = -4 dB
			wantLinearPossible: true,
		},
		{
			name:       "large boost with headroom",
			measuredI:  -26.0,
			measuredTP: -10.0, // 8.5 dB headroom
			desiredI:   -16.0,
			targetTP:   -1.5,
			// max linear: -1.5 - (-10.0) + (-26.0) - 0.1 = -17.6 LUFS
			// desired -16.0 > -17.6 (louder than max), so adjustment needed
			wantEffectiveI:     -17.5 - margin,
			wantOffset:         8.5 - margin, // -17.6 - (-26) = 8.4 dB
			wantLinearPossible: false,
		},
		{
			name:               "typical podcast scenario - target adjustment needed",
			measuredI:          -24.88,
			measuredTP:         -5.04,
			desiredI:           -16.0,
			targetTP:           -2.0,
			wantEffectiveI:     -21.84 - margin, // max linear: -2.0 - (-5.04) + (-24.88) - 0.1 = -21.94
			wantOffset:         3.04 - margin,   // -21.94 - (-24.88) = 2.94 dB
			wantLinearPossible: false,
		},
		{
			name:       "generous headroom allows full target",
			measuredI:  -30.0,
			measuredTP: -18.0, // Lots of headroom
			desiredI:   -16.0,
			targetTP:   -1.5,
			// max linear: -1.5 - (-18.0) + (-30.0) - 0.1 = -13.6 LUFS
			// desired -16.0 < -13.6 (quieter than max), so achievable
			wantEffectiveI:     -16.0,
			wantOffset:         14.0, // -16 - (-30) = 14 dB
			wantLinearPossible: true,
		},
		{
			name:       "post-gain I - Anna values with clamped ceiling",
			measuredI:  -36.5, // postGainI = -43.4 + 6.9 deficit
			measuredTP: -24.0, // re-derived ceiling
			desiredI:   -16.0,
			targetTP:   -2.0,
			// max linear: -2.0 - (-24.0) + (-36.5) - 0.1 = -14.6 LUFS
			// desired -16.0 <= -14.6, so achievable
			wantEffectiveI:     -16.0,
			wantOffset:         20.5, // -16.0 - (-36.5) = 20.5 dB
			wantLinearPossible: true,
		},
		{
			name:       "post-gain I - extremely quiet, still cannot reach target",
			measuredI:  -40.0, // postGainI after deficit, still very quiet
			measuredTP: -24.0, // re-derived ceiling at minimum
			desiredI:   -16.0,
			targetTP:   -2.0,
			// max linear: -2.0 - (-24.0) + (-40.0) - 0.1 = -18.1 LUFS
			// desired -16.0 > -18.1, so clamped
			wantEffectiveI:     -18.0 - margin,
			wantOffset:         22.0 - margin, // -18.1 - (-40.0) = 21.9 dB
			wantLinearPossible: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			effectiveI, offset, linearPossible := calculateLinearModeTarget(
				tt.measuredI, tt.measuredTP, tt.desiredI, tt.targetTP)

			if math.Abs(effectiveI-tt.wantEffectiveI) > 0.01 {
				t.Errorf("effectiveI = %.2f, want %.2f", effectiveI, tt.wantEffectiveI)
			}
			if math.Abs(offset-tt.wantOffset) > 0.01 {
				t.Errorf("offset = %.2f, want %.2f", offset, tt.wantOffset)
			}
			if linearPossible != tt.wantLinearPossible {
				t.Errorf("linearPossible = %v, want %v", linearPossible, tt.wantLinearPossible)
			}
		})
	}
}

func TestCalculateLimiterCeiling(t *testing.T) {
	// Minimum ceiling is -24.0 dBTP (alimiter limit=0.0625)
	const minCeiling = -24.0

	tests := []struct {
		name        string
		measuredI   float64
		measuredTP  float64
		targetI     float64
		targetTP    float64
		wantCeiling float64
		wantNeeded  bool
		wantClamped bool
	}{
		{
			name:       "limiting needed - typical podcast",
			measuredI:  -24.9,
			measuredTP: -5.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-24.9) = 8.9 dB
			// projected TP = -5.0 + 8.9 = 3.9 dBTP (exceeds -2.0)
			// ceiling = -2.0 - 8.9 - 1.5 = -12.4 dBTP
			wantCeiling: -12.4,
			wantNeeded:  true,
			wantClamped: false,
		},
		{
			name:       "limiting needed - loud peaks",
			measuredI:  -20.0,
			measuredTP: -3.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-20.0) = 4.0 dB
			// projected TP = -3.0 + 4.0 = 1.0 dBTP (exceeds -2.0)
			// ceiling = -2.0 - 4.0 - 1.5 = -7.5 dBTP
			wantCeiling: -7.5,
			wantNeeded:  true,
			wantClamped: false,
		},
		{
			name:       "no limiting needed - quiet peaks",
			measuredI:  -20.0,
			measuredTP: -10.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-20.0) = 4.0 dB
			// projected TP = -10.0 + 4.0 = -6.0 dBTP (under -2.0)
			wantCeiling: 0,
			wantNeeded:  false,
			wantClamped: false,
		},
		{
			name:       "no limiting needed - needs attenuation",
			measuredI:  -12.0,
			measuredTP: -1.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-12.0) = -4.0 dB (attenuation)
			// projected TP = -1.0 + (-4.0) = -5.0 dBTP (under -2.0)
			wantCeiling: 0,
			wantNeeded:  false,
			wantClamped: false,
		},
		{
			name:       "exactly at boundary - no limiting",
			measuredI:  -20.0,
			measuredTP: -6.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-20.0) = 4.0 dB
			// projected TP = -6.0 + 4.0 = -2.0 dBTP (exactly at target)
			wantCeiling: 0,
			wantNeeded:  false,
			wantClamped: false,
		},
		{
			name:       "very quiet audio - clamped to minimum",
			measuredI:  -43.0,
			measuredTP: -20.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-43.0) = 27.0 dB
			// projected TP = -20.0 + 27.0 = 7.0 dBTP (exceeds -2.0)
			// calculated ceiling = -2.0 - 27.0 - 1.5 = -30.5 dBTP
			// but -30.5 < -24.0, so clamped to -24.0 dBTP
			wantCeiling: minCeiling,
			wantNeeded:  true,
			wantClamped: true,
		},
		{
			name:       "just under minimum - clamped",
			measuredI:  -38.0,
			measuredTP: -15.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-38.0) = 22.0 dB
			// projected TP = -15.0 + 22.0 = 7.0 dBTP (exceeds -2.0)
			// calculated ceiling = -2.0 - 22.0 - 1.5 = -25.5 dBTP
			// -25.5 < -24.0, so clamped to -24.0 dBTP
			wantCeiling: minCeiling,
			wantNeeded:  true,
			wantClamped: true,
		},
		{
			name:       "just above minimum - not clamped",
			measuredI:  -35.0,
			measuredTP: -15.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-35.0) = 19.0 dB
			// projected TP = -15.0 + 19.0 = 4.0 dBTP (exceeds -2.0)
			// ceiling = -2.0 - 19.0 - 1.5 = -22.5 dBTP (above -24.0)
			wantCeiling: -22.5,
			wantNeeded:  true,
			wantClamped: false,
		},
		{
			name:       "Anna exact values - clamped with verifiable deficit",
			measuredI:  -43.2,
			measuredTP: -18.6,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-43.2) = 27.2 dB
			// projected TP = -18.6 + 27.2 = 8.6 dBTP (exceeds -2.0)
			// idealCeiling = -2.0 - 27.2 - 1.5 = -30.7 dBTP
			// -30.7 < -24.0, so clamped to -24.0 dBTP
			// deficit = minLimiterCeilingDB - idealCeiling = -24.0 - (-30.7) = 6.7 dB
			wantCeiling: minCeiling,
			wantNeeded:  true,
			wantClamped: true,
		},
		{
			name:       "exact clamping boundary - ceiling equals minimum exactly",
			measuredI:  -36.5,
			measuredTP: -15.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// gain = -16.0 - (-36.5) = 20.5 dB
			// projected TP = -15.0 + 20.5 = 5.5 dBTP (exceeds -2.0)
			// ceiling = -2.0 - 20.5 - 1.5 = -24.0 dBTP (exactly minLimiterCeilingDB)
			// Not clamped: ceiling < minLimiterCeilingDB is false when equal.
			// deficit = 0 (no pre-gain needed at the boundary)
			wantCeiling: minCeiling,
			wantNeeded:  true,
			wantClamped: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ceiling, needed, clamped := calculateLimiterCeiling(
				tt.measuredI, tt.measuredTP, tt.targetI, tt.targetTP)

			if needed != tt.wantNeeded {
				t.Errorf("needed = %v, want %v", needed, tt.wantNeeded)
			}
			if clamped != tt.wantClamped {
				t.Errorf("clamped = %v, want %v", clamped, tt.wantClamped)
			}
			if needed && math.Abs(ceiling-tt.wantCeiling) > 0.01 {
				t.Errorf("ceiling = %.2f dBTP, want %.2f dBTP", ceiling, tt.wantCeiling)
			}

			// Verify deficit arithmetic independently for clamped cases.
			// deficit = minLimiterCeilingDB - (targetTP - gainRequired - safetyMarginDB)
			if clamped {
				gainRequired := tt.targetI - tt.measuredI
				idealCeiling := tt.targetTP - gainRequired - safetyMarginDB
				deficit := minLimiterCeilingDB - idealCeiling
				if deficit <= 0 {
					t.Errorf("deficit should be positive when clamped, got %.2f", deficit)
				}
				// Verify the ideal ceiling is below the minimum (confirms clamping)
				if idealCeiling >= minLimiterCeilingDB {
					t.Errorf("idealCeiling = %.2f should be below minLimiterCeilingDB (%.2f) when clamped",
						idealCeiling, minLimiterCeilingDB)
				}
			}
		})
	}
}

func TestBuildLoudnormFilterSpec_PreGain(t *testing.T) {
	tests := []struct {
		name             string
		inputI           float64
		inputTP          float64
		inputLRA         float64
		inputThresh      float64
		targetOffset     float64
		wantVolumeFilter bool    // (a)/(b): volume filter present or absent
		wantDeficit      float64 // (c): expected deficit in dB (0 when no pre-gain)
		wantClamped      bool
	}{
		{
			name:         "clamped - very quiet audio (Anna-like)",
			inputI:       -43.2,
			inputTP:      -18.6,
			inputLRA:     8.0,
			inputThresh:  -53.0,
			targetOffset: -2.5,
			// gain = -16.0 - (-43.2) = 27.2
			// idealCeiling = -2.0 - 27.2 - 1.5 = -30.7
			// deficit = -24.0 - (-30.7) = 6.7
			wantVolumeFilter: true,
			wantDeficit:      6.7,
			wantClamped:      true,
		},
		{
			name:         "not clamped - typical podcast (Marius-like)",
			inputI:       -24.9,
			inputTP:      -5.0,
			inputLRA:     6.0,
			inputThresh:  -35.0,
			targetOffset: -0.5,
			// gain = -16.0 - (-24.9) = 8.9
			// idealCeiling = -2.0 - 8.9 - 1.5 = -12.4 (above -24.0)
			wantVolumeFilter: false,
			wantDeficit:      0.0,
			wantClamped:      false,
		},
		{
			name:         "clamped - moderate deficit",
			inputI:       -38.0,
			inputTP:      -15.0,
			inputLRA:     7.0,
			inputThresh:  -48.0,
			targetOffset: -1.0,
			// gain = -16.0 - (-38.0) = 22.0
			// idealCeiling = -2.0 - 22.0 - 1.5 = -25.5
			// deficit = -24.0 - (-25.5) = 1.5
			wantVolumeFilter: true,
			wantDeficit:      1.5,
			wantClamped:      true,
		},
		{
			name:         "no limiter needed - quiet peaks",
			inputI:       -20.0,
			inputTP:      -10.0,
			inputLRA:     5.0,
			inputThresh:  -30.0,
			targetOffset: 0.0,
			// gain = -16.0 - (-20.0) = 4.0
			// projectedTP = -10.0 + 4.0 = -6.0 (under -2.0, no limiter)
			wantVolumeFilter: false,
			wantDeficit:      0.0,
			wantClamped:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := defaultNormalisationTestConfig()
			measurement := &LoudnormMeasurement{
				InputI:       tt.inputI,
				InputTP:      tt.inputTP,
				InputLRA:     tt.inputLRA,
				InputThresh:  tt.inputThresh,
				TargetOffset: tt.targetOffset,
			}

			// Pre-compute values (the caller's responsibility)
			ceiling, needsLimiting, clamped := calculateLimiterCeiling(
				tt.inputI, tt.inputTP, config.Loudnorm.TargetI, config.Loudnorm.TargetTP,
			)
			preGainDB, reDerivedCeiling := calculatePreGain(
				tt.inputI, config.Loudnorm.TargetI, config.Loudnorm.TargetTP,
			)
			if clamped {
				ceiling = reDerivedCeiling
			}

			filterSpec := buildLoudnormFilterSpec(config, measurement, preGainDB, ceiling, needsLimiting, 48000, "")

			// (a)/(b): Check volume filter presence
			hasVolume := strings.Contains(filterSpec, "volume=")
			if hasVolume != tt.wantVolumeFilter {
				t.Errorf("volume filter present = %v, want %v\nfilterSpec: %s", hasVolume, tt.wantVolumeFilter, filterSpec)
			}

			// Check clamped value from pre-computation
			if clamped != tt.wantClamped {
				t.Errorf("clamped = %v, want %v", clamped, tt.wantClamped)
			}

			// (c): Check deficit value from pre-computation
			if math.Abs(preGainDB-tt.wantDeficit) > 0.01 {
				t.Errorf("preGainDB = %.2f, want %.2f", preGainDB, tt.wantDeficit)
			}

			// (new): Verify measurement.InputI and measurement.InputTP are passed
			// directly to loudnorm as measured_I and measured_TP (no adjustment)
			wantDirectI := fmt.Sprintf("measured_I=%.2f", tt.inputI)
			if !strings.Contains(filterSpec, wantDirectI) {
				t.Errorf("loudnorm should pass measurement.InputI directly as measured_I=%q\nfilterSpec: %s", wantDirectI, filterSpec)
			}
			wantDirectTP := fmt.Sprintf("measured_TP=%.2f", tt.inputTP)
			if !strings.Contains(filterSpec, wantDirectTP) {
				t.Errorf("loudnorm should pass measurement.InputTP directly as measured_TP=%q\nfilterSpec: %s", wantDirectTP, filterSpec)
			}

			if tt.wantVolumeFilter {
				// (c): Verify deficit value in the filter string
				wantVolumeStr := fmt.Sprintf("volume=%.1fdB", tt.wantDeficit)
				if !strings.Contains(filterSpec, wantVolumeStr) {
					t.Errorf("filter spec missing %q\nfilterSpec: %s", wantVolumeStr, filterSpec)
				}

				// (d): Re-derived ceiling used for alimiter
				reDerivedLinear := math.Pow(10, reDerivedCeiling/20.0)
				wantLimit := fmt.Sprintf("limit=%.6f", reDerivedLinear)
				if !strings.Contains(filterSpec, wantLimit) {
					t.Errorf("alimiter should use re-derived ceiling (limit=%.6f), not found\nfilterSpec: %s", reDerivedLinear, filterSpec)
				}

				// Verify volume filter appears before alimiter in the chain
				volumeIdx := strings.Index(filterSpec, "volume=")
				alimiterIdx := strings.Index(filterSpec, "alimiter=")
				if alimiterIdx == -1 {
					t.Error("alimiter filter missing from spec when clamped")
				} else if volumeIdx > alimiterIdx {
					t.Error("volume filter must appear before alimiter")
				}
			} else {
				hasLimiter := strings.Contains(filterSpec, "alimiter=")
				if hasLimiter != needsLimiting {
					t.Errorf("alimiter present = %v, want %v\nfilterSpec: %s", hasLimiter, needsLimiting, filterSpec)
				}
			}
		})
	}
}

func TestBuildLoudnormFilterSpec_DoesNotMutateConfig(t *testing.T) {
	config := defaultNormalisationTestConfig()
	config.Resample.Enabled = false
	config.Resample.SampleRate = 48000
	config.Resample.Format = "s32"
	config.Resample.FrameSize = 2048

	measurement := &LoudnormMeasurement{
		InputI:       -24.0,
		InputTP:      -5.0,
		InputLRA:     6.0,
		InputThresh:  -34.0,
		TargetOffset: -0.5,
	}

	filterSpec := buildLoudnormFilterSpec(config, measurement, 0, -1.0, false, 48000, "")

	if config.Resample.Enabled {
		t.Error("buildLoudnormFilterSpec mutated config.Resample.Enabled")
	}

	want := "aformat=sample_rates=48000:channel_layouts=mono:sample_fmts=s32,asetnsamples=n=2048"
	if !strings.Contains(filterSpec, want) {
		t.Errorf("buildLoudnormFilterSpec() missing required output format %q\nfilterSpec: %s", want, filterSpec)
	}
}

func TestBuildLoudnormFilterSpec_Adeclick(t *testing.T) {
	measurement := &LoudnormMeasurement{
		InputI:       -24.0,
		InputTP:      -5.0,
		InputLRA:     6.0,
		InputThresh:  -34.0,
		TargetOffset: -0.5,
	}

	t.Run("uses Pass 4 adeclick helper", func(t *testing.T) {
		config := defaultNormalisationTestConfig()

		filterSpec := buildLoudnormFilterSpec(config, measurement, 0, -1.0, false, 48000, "")

		const want = "adeclick=t=1.7:w=55:o=50:m=s"
		if !strings.Contains(filterSpec, want) {
			t.Errorf("buildLoudnormFilterSpec() missing %q\nfilterSpec: %s", want, filterSpec)
		}
	})

	t.Run("omits disabled adeclick", func(t *testing.T) {
		config := defaultNormalisationTestConfig()
		config.Adeclick.Enabled = false

		filterSpec := buildLoudnormFilterSpec(config, measurement, 0, -1.0, false, 48000, "")

		if strings.Contains(filterSpec, "adeclick=") {
			t.Errorf("buildLoudnormFilterSpec() emitted disabled adeclick\nfilterSpec: %s", filterSpec)
		}
	})
}

func TestBuildLoudnormFilterSpecSourceRateResample(t *testing.T) {
	measurement := &LoudnormMeasurement{
		InputI:       -24.0,
		InputTP:      -5.0,
		InputLRA:     6.0,
		InputThresh:  -34.0,
		TargetOffset: -0.5,
	}

	// Pins source-rate derivation against a NON-48000 rate so a future refactor
	// that hardcodes 48000 (instead of deriving from metadata) or drops the
	// param is caught. The resample must sit after loudnorm and immediately
	// before adeclick.
	t.Run("derives resample rate and orders it between loudnorm and adeclick", func(t *testing.T) {
		config := defaultNormalisationTestConfig()

		filterSpec := buildLoudnormFilterSpec(config, measurement, 0, -1.0, false, 96000, "")

		const wantResample = "aresample=96000"
		if !strings.Contains(filterSpec, wantResample) {
			t.Fatalf("buildLoudnormFilterSpec() missing %q (hardcoded 48000?)\nfilterSpec: %s", wantResample, filterSpec)
		}
		if strings.Contains(filterSpec, "aresample=48000") {
			t.Fatalf("buildLoudnormFilterSpec() emitted hardcoded aresample=48000 for 96000 source rate\nfilterSpec: %s", filterSpec)
		}

		loudnormAt := strings.Index(filterSpec, "loudnorm=")
		resampleAt := strings.Index(filterSpec, wantResample)
		adeclickAt := strings.Index(filterSpec, "adeclick=")
		if loudnormAt < 0 || resampleAt < 0 || adeclickAt < 0 {
			t.Fatalf("missing a required stage (loudnorm=%d aresample=%d adeclick=%d)\nfilterSpec: %s", loudnormAt, resampleAt, adeclickAt, filterSpec)
		}
		if loudnormAt >= resampleAt || resampleAt >= adeclickAt {
			t.Fatalf("stage order = loudnorm@%d, aresample@%d, adeclick@%d, want loudnorm < aresample < adeclick\nfilterSpec: %s", loudnormAt, resampleAt, adeclickAt, filterSpec)
		}
	})

	// Pins the sourceSampleRate <= 0 guard: no resample stage emitted, adeclick
	// still present.
	t.Run("omits resample when source rate is zero", func(t *testing.T) {
		config := defaultNormalisationTestConfig()

		filterSpec := buildLoudnormFilterSpec(config, measurement, 0, -1.0, false, 0, "")

		if strings.Contains(filterSpec, "aresample=") {
			t.Fatalf("buildLoudnormFilterSpec() emitted aresample for zero source rate\nfilterSpec: %s", filterSpec)
		}
		if !strings.Contains(filterSpec, "adeclick=") {
			t.Fatalf("buildLoudnormFilterSpec() dropped adeclick when source rate is zero\nfilterSpec: %s", filterSpec)
		}
	})
}

func TestBuildLoudnormFilterSpecIgnoresNonNormalisationFields(t *testing.T) {
	measurement := &LoudnormMeasurement{
		InputI:       -24.0,
		InputTP:      -5.0,
		InputLRA:     6.0,
		InputThresh:  -34.0,
		TargetOffset: -0.5,
	}

	base := defaultNormalisationTestConfig()
	assertNoStaleEffectiveConfigFields(t)
	controlSpec := buildLoudnormFilterSpec(base, measurement, 0, -1.0, false, 48000, "")

	withUnrelatedFilterFields := *base
	withUnrelatedFilterFields.FilterOrder = []FilterID{FilterAnalysis}
	withUnrelatedFilterFields.DS201LowPass.Frequency = 12000
	withUnrelatedFilterFields.DS201Gate.Ratio = 4.0
	withUnrelatedFilterFields.LA2A.Threshold = -30.0

	gotSpec := buildLoudnormFilterSpec(&withUnrelatedFilterFields, measurement, 0, -1.0, false, 48000, "")
	if gotSpec != controlSpec {
		t.Fatalf("buildLoudnormFilterSpec() changed when unrelated filter fields changed\ncontrol: %s\ngot:     %s", controlSpec, gotSpec)
	}
}

func TestPreGainCeilingRederivation(t *testing.T) {
	// Validates the mathematical invariant: applying the deficit as pre-gain
	// converts a clamped scenario into a non-clamped scenario, with the
	// re-derived ceiling landing at or near minLimiterCeilingDB.

	tests := []struct {
		name       string
		measuredI  float64
		measuredTP float64
		targetI    float64
		targetTP   float64
	}{
		{
			name:       "Anna-like - very quiet, large deficit",
			measuredI:  -43.2,
			measuredTP: -18.6,
			targetI:    -16.0,
			targetTP:   -2.0,
		},
		{
			name:       "moderate deficit - just below clamping",
			measuredI:  -38.0,
			measuredTP: -15.0,
			targetI:    -16.0,
			targetTP:   -2.0,
		},
		{
			name:       "extreme quiet - large gain required",
			measuredI:  -50.0,
			measuredTP: -25.0,
			targetI:    -16.0,
			targetTP:   -2.0,
		},
		{
			name:       "different target TP",
			measuredI:  -40.0,
			measuredTP: -16.0,
			targetI:    -16.0,
			targetTP:   -1.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Step 1: original values should be clamped
			origCeiling, origNeeded, origClamped := calculateLimiterCeiling(
				tt.measuredI, tt.measuredTP, tt.targetI, tt.targetTP)

			if !origNeeded {
				t.Fatal("expected limiter to be needed for original values")
			}
			if !origClamped {
				t.Fatal("expected original ceiling to be clamped")
			}
			if math.Abs(origCeiling-minLimiterCeilingDB) > 0.01 {
				t.Fatalf("clamped ceiling = %.2f, want %.2f", origCeiling, minLimiterCeilingDB)
			}

			// Step 2: calculate deficit
			gainRequired := tt.targetI - tt.measuredI
			idealCeiling := tt.targetTP - gainRequired - safetyMarginDB
			deficit := minLimiterCeilingDB - idealCeiling

			if deficit <= 0 {
				t.Fatalf("deficit should be positive for clamped scenario, got %.2f", deficit)
			}

			// Step 3: apply deficit as pre-gain and re-derive ceiling
			postGainI := tt.measuredI + deficit
			postGainTP := tt.measuredTP + deficit

			newCeiling, newNeeded, newClamped := calculateLimiterCeiling(
				postGainI, postGainTP, tt.targetI, tt.targetTP)

			if !newNeeded {
				t.Error("expected limiter to still be needed after pre-gain")
			}
			if newClamped {
				t.Error("expected re-derived ceiling to NOT be clamped after pre-gain")
			}

			// Step 4: re-derived ceiling should land at minLimiterCeilingDB
			if math.Abs(newCeiling-minLimiterCeilingDB) > 0.01 {
				t.Errorf("re-derived ceiling = %.2f dBTP, want %.2f dBTP (minLimiterCeilingDB)",
					newCeiling, minLimiterCeilingDB)
			}
		})
	}
}

func TestClampedTargetPropagation_Arithmetic(t *testing.T) {
	// Verifies the arithmetic chain that ApplyNormalisation uses when the
	// ceiling is clamped: calculateLimiterCeiling -> deficit -> post-gain I ->
	// calculateLinearModeTarget -> buildLoudnormFilterSpec. Each function is
	// called with the same inputs ApplyNormalisation would derive, confirming
	// the full -16.0 LUFS target is preserved.
	//
	// This does not exercise ApplyNormalisation itself (which requires audio
	// files and the full FFmpeg pipeline); it validates the pure-function chain.

	tests := []struct {
		name           string
		measuredI      float64
		measuredTP     float64
		targetI        float64
		targetTP       float64
		wantEffectiveI float64
		wantLinear     bool
	}{
		{
			name:           "Anna - very quiet, clamped ceiling preserves full target",
			measuredI:      -43.4,
			measuredTP:     -19.2,
			targetI:        -16.0,
			targetTP:       -2.0,
			wantEffectiveI: -16.0,
			wantLinear:     true,
		},
		{
			name:           "Anna-like with different measurements",
			measuredI:      -43.2,
			measuredTP:     -18.6,
			targetI:        -16.0,
			targetTP:       -2.0,
			wantEffectiveI: -16.0,
			wantLinear:     true,
		},
		{
			name:       "extreme quiet - still clamped after pre-gain",
			measuredI:  -55.0,
			measuredTP: -30.0,
			targetI:    -16.0,
			targetTP:   -2.0,
			// deficit = -24.0 - (-2.0 - (-16.0 - (-55.0)) - 1.5) = -24.0 - (-42.5) = 18.5
			// postGainI = -55.0 + 18.5 = -36.5
			// re-derived ceiling = -24.0
			// maxLinear = -2.0 - (-24.0) + (-36.5) - 0.1 = -14.6
			// -16.0 <= -14.6, so full target preserved
			wantEffectiveI: -16.0,
			wantLinear:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Step 1: calculateLimiterCeiling (same as ApplyNormalisation)
			_, limiterNeeded, limiterClamped := calculateLimiterCeiling(
				tt.measuredI, tt.measuredTP, tt.targetI, tt.targetTP)

			if !limiterNeeded {
				t.Fatal("expected limiter to be needed")
			}
			if !limiterClamped {
				t.Fatal("expected ceiling to be clamped")
			}

			// Step 2: replicate the effectiveTP and effectiveMeasuredI logic
			gainRequired := tt.targetI - tt.measuredI
			idealCeiling := tt.targetTP - gainRequired - safetyMarginDB
			deficit := minLimiterCeilingDB - idealCeiling
			postGainI := tt.measuredI + deficit
			newGainRequired := tt.targetI - postGainI
			reDerivedCeiling := tt.targetTP - newGainRequired - safetyMarginDB
			effectiveTP := reDerivedCeiling
			effectiveMeasuredI := postGainI

			// Step 3: calculateLinearModeTarget with post-gain I
			effectiveTargetI, _, linearPossible := calculateLinearModeTarget(
				effectiveMeasuredI, effectiveTP, tt.targetI, tt.targetTP)

			if math.Abs(effectiveTargetI-tt.wantEffectiveI) > 0.01 {
				t.Errorf("effectiveTargetI = %.2f, want %.2f", effectiveTargetI, tt.wantEffectiveI)
			}
			if linearPossible != tt.wantLinear {
				t.Errorf("linearPossible = %v, want %v", linearPossible, tt.wantLinear)
			}

			// Step 4: verify buildLoudnormFilterSpec receives the full target
			config := defaultNormalisationTestConfig()
			config.Loudnorm.TargetI = effectiveTargetI
			measurement := &LoudnormMeasurement{
				InputI:       tt.measuredI,
				InputTP:      tt.measuredTP,
				InputLRA:     8.0,
				InputThresh:  tt.measuredI - 10.0,
				TargetOffset: -2.5,
			}

			// Pre-compute values (the caller's responsibility)
			bCeiling, bNeeded, bClamped := calculateLimiterCeiling(
				tt.measuredI, tt.measuredTP, config.Loudnorm.TargetI, config.Loudnorm.TargetTP,
			)
			preGainDB, bReDerived := calculatePreGain(
				tt.measuredI, config.Loudnorm.TargetI, config.Loudnorm.TargetTP,
			)
			if bClamped {
				bCeiling = bReDerived
			}

			filterSpec := buildLoudnormFilterSpec(config, measurement, preGainDB, bCeiling, bNeeded, 48000, "")
			if !bClamped {
				t.Error("expected pre-computation to report clamped")
			}
			if math.Abs(preGainDB-deficit) > 0.01 {
				t.Errorf("preGainDB = %.2f, want deficit = %.2f", preGainDB, deficit)
			}

			// Verify the filter spec contains the expected loudnorm parameters
			if !strings.Contains(filterSpec, "loudnorm=") {
				t.Error("filter spec missing loudnorm filter")
			}
		})
	}
}

func TestCalculatePreGain(t *testing.T) {
	tests := []struct {
		name              string
		measuredI         float64
		targetI           float64
		targetTP          float64
		wantPreGainDB     float64
		wantReDerivedCeil float64
	}{
		{
			name:      "clamped - returns positive deficit and valid re-derived ceiling",
			measuredI: -43.2,
			targetI:   -16.0,
			targetTP:  -2.0,
			// gainRequired = -16.0 - (-43.2) = 27.2
			// idealCeiling = -2.0 - 27.2 - 1.5 = -30.7
			// deficit = -24.0 - (-30.7) = 6.7
			// postGainI = -43.2 + 6.7 = -36.5
			// newGainRequired = -16.0 - (-36.5) = 20.5
			// reDerivedCeiling = -2.0 - 20.5 - 1.5 = -24.0
			wantPreGainDB:     6.7,
			wantReDerivedCeil: -24.0,
		},
		{
			name:      "not clamped - returns zeros",
			measuredI: -24.9,
			targetI:   -16.0,
			targetTP:  -2.0,
			// gainRequired = 8.9
			// idealCeiling = -2.0 - 8.9 - 1.5 = -12.4 (above -24.0)
			wantPreGainDB:     0.0,
			wantReDerivedCeil: 0.0,
		},
		{
			name:      "boundary - ideal ceiling equals minLimiterCeilingDB exactly",
			measuredI: -36.5,
			targetI:   -16.0,
			targetTP:  -2.0,
			// gainRequired = 20.5
			// idealCeiling = -2.0 - 20.5 - 1.5 = -24.0 (exactly minLimiterCeilingDB)
			wantPreGainDB:     0.0,
			wantReDerivedCeil: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			preGainDB, reDerivedCeiling := calculatePreGain(tt.measuredI, tt.targetI, tt.targetTP)

			if math.Abs(preGainDB-tt.wantPreGainDB) > 0.01 {
				t.Errorf("preGainDB = %.2f, want %.2f", preGainDB, tt.wantPreGainDB)
			}
			if math.Abs(reDerivedCeiling-tt.wantReDerivedCeil) > 0.01 {
				t.Errorf("reDerivedCeiling = %.2f, want %.2f", reDerivedCeiling, tt.wantReDerivedCeil)
			}
		})
	}
}

func TestBuildPreLimiterPrefix(t *testing.T) {
	tests := []struct {
		name          string
		preGainDB     float64
		ceiling       float64
		needsLimiting bool
		wantEmpty     bool
		wantVolume    bool
		wantAlimiter  bool
	}{
		{
			name:          "clamped - volume and alimiter",
			preGainDB:     6.7,
			ceiling:       -24.0,
			needsLimiting: true,
			wantEmpty:     false,
			wantVolume:    true,
			wantAlimiter:  true,
		},
		{
			name:          "needed but not clamped - alimiter only",
			preGainDB:     0.0,
			ceiling:       -12.4,
			needsLimiting: true,
			wantEmpty:     false,
			wantVolume:    false,
			wantAlimiter:  true,
		},
		{
			name:          "not needed - empty string",
			preGainDB:     0.0,
			ceiling:       0.0,
			needsLimiting: false,
			wantEmpty:     true,
			wantVolume:    false,
			wantAlimiter:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildPreLimiterPrefix(tt.preGainDB, tt.ceiling, tt.needsLimiting)

			if tt.wantEmpty {
				if result != "" {
					t.Errorf("expected empty string, got %q", result)
				}
				return
			}

			hasVolume := strings.Contains(result, "volume=")
			if hasVolume != tt.wantVolume {
				t.Errorf("volume present = %v, want %v\nresult: %s", hasVolume, tt.wantVolume, result)
			}

			hasAlimiter := strings.Contains(result, "alimiter=")
			if hasAlimiter != tt.wantAlimiter {
				t.Errorf("alimiter present = %v, want %v\nresult: %s", hasAlimiter, tt.wantAlimiter, result)
			}

			// (d): volume appears before alimiter when both present
			if hasVolume && hasAlimiter {
				volumeIdx := strings.Index(result, "volume=")
				alimiterIdx := strings.Index(result, "alimiter=")
				if volumeIdx > alimiterIdx {
					t.Error("volume must appear before alimiter")
				}
			}

			// Verify correct volume value when present
			if tt.wantVolume {
				wantVolumeStr := fmt.Sprintf("volume=%.1fdB", tt.preGainDB)
				if !strings.Contains(result, wantVolumeStr) {
					t.Errorf("expected %q in result %q", wantVolumeStr, result)
				}
			}

			// Verify correct ceiling in alimiter when present
			if tt.wantAlimiter {
				limiterLinear := math.Pow(10, tt.ceiling/20.0)
				wantLimit := fmt.Sprintf("limit=%.6f", limiterLinear)
				if !strings.Contains(result, wantLimit) {
					t.Errorf("expected %q in result %q", wantLimit, result)
				}
			}
		})
	}
}

func TestLoudnormPrefixAndFilterSpecParityRepresentativeCases(t *testing.T) {
	tests := []struct {
		name           string
		outputI        float64
		outputTP       float64
		measurement    LoudnormMeasurement
		wantPass3      string
		wantPass4      string
		wantPass4Start string
	}{
		{
			name:     "non-limited",
			outputI:  -20.0,
			outputTP: -10.0,
			measurement: LoudnormMeasurement{
				InputI:       -20.0,
				InputTP:      -10.0,
				InputLRA:     5.0,
				InputThresh:  -30.0,
				TargetOffset: 0.0,
			},
			wantPass3:      "",
			wantPass4Start: "loudnorm=",
			wantPass4:      "loudnorm=I=-16.00:TP=-2.00:LRA=20.0:measured_I=-20.00:measured_TP=-10.00:measured_LRA=5.00:measured_thresh=-30.00:offset=0.00:dual_mono=true:linear=true:print_format=json,aresample=48000,adeclick=t=1.7:w=55:o=50:m=s,astats=metadata=1:measure_perchannel=all,aspectralstats=win_size=2048:win_func=hann:measure=all,ebur128=metadata=1:peak=sample+true:dualmono=true,aformat=sample_rates=44100:channel_layouts=mono:sample_fmts=s16,asetnsamples=n=4096", // #nosec G101 -- FFmpeg filter fixture, not a credential.
		},
		{
			name:     "limited",
			outputI:  -24.9,
			outputTP: -5.0,
			measurement: LoudnormMeasurement{
				InputI:       -24.9,
				InputTP:      -5.0,
				InputLRA:     6.0,
				InputThresh:  -35.0,
				TargetOffset: -0.5,
			},
			wantPass3:      "alimiter=limit=0.239883:attack=5:release=100:level_in=1:level_out=1:level=0:latency=1:asc=1:asc_level=0.8",
			wantPass4Start: "alimiter=limit=0.239883:attack=5:release=100:level_in=1:level_out=1:level=0:latency=1:asc=1:asc_level=0.8,loudnorm=",
			wantPass4:      "alimiter=limit=0.239883:attack=5:release=100:level_in=1:level_out=1:level=0:latency=1:asc=1:asc_level=0.8,loudnorm=I=-16.00:TP=-2.00:LRA=20.0:measured_I=-24.90:measured_TP=-5.00:measured_LRA=6.00:measured_thresh=-35.00:offset=-0.50:dual_mono=true:linear=true:print_format=json,aresample=48000,adeclick=t=1.7:w=55:o=50:m=s,astats=metadata=1:measure_perchannel=all,aspectralstats=win_size=2048:win_func=hann:measure=all,ebur128=metadata=1:peak=sample+true:dualmono=true,aformat=sample_rates=44100:channel_layouts=mono:sample_fmts=s16,asetnsamples=n=4096", // #nosec G101 -- FFmpeg filter fixture, not a credential.
		},
		{
			name:     "clamped pre-gain",
			outputI:  -43.2,
			outputTP: -18.6,
			measurement: LoudnormMeasurement{
				InputI:       -36.5,
				InputTP:      -24.0,
				InputLRA:     8.0,
				InputThresh:  -46.5,
				TargetOffset: -2.5,
			},
			wantPass3:      "volume=6.7dB,alimiter=limit=0.063096:attack=5:release=100:level_in=1:level_out=1:level=0:latency=1:asc=1:asc_level=0.8",
			wantPass4Start: "volume=6.7dB,alimiter=limit=0.063096:attack=5:release=100:level_in=1:level_out=1:level=0:latency=1:asc=1:asc_level=0.8,loudnorm=",
			wantPass4:      "volume=6.7dB,alimiter=limit=0.063096:attack=5:release=100:level_in=1:level_out=1:level=0:latency=1:asc=1:asc_level=0.8,loudnorm=I=-16.00:TP=-2.00:LRA=20.0:measured_I=-36.50:measured_TP=-24.00:measured_LRA=8.00:measured_thresh=-46.50:offset=-2.50:dual_mono=true:linear=true:print_format=json,aresample=48000,adeclick=t=1.7:w=55:o=50:m=s,astats=metadata=1:measure_perchannel=all,aspectralstats=win_size=2048:win_func=hann:measure=all,ebur128=metadata=1:peak=sample+true:dualmono=true,aformat=sample_rates=44100:channel_layouts=mono:sample_fmts=s16,asetnsamples=n=4096", // #nosec G101 -- FFmpeg filter fixture, not a credential.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := defaultNormalisationTestConfig()
			output := &OutputMeasurements{
				OutputI:  tt.outputI,
				OutputTP: tt.outputTP,
			}

			limiter := planLimiterForLoudnorm(output, config)
			if limiter.pass3Prefix != tt.wantPass3 {
				t.Fatalf("pass 3 prefix = %q, want %q", limiter.pass3Prefix, tt.wantPass3)
			}

			gotPass4 := buildLoudnormFilterSpec(
				config,
				&tt.measurement,
				limiter.preGainDB,
				limiter.ceilingDB,
				limiter.needed,
				48000,
				"",
			)
			if !strings.HasPrefix(gotPass4, tt.wantPass4Start) {
				t.Fatalf("pass 4 filter spec prefix = %q, want prefix %q", gotPass4, tt.wantPass4Start)
			}
			if gotPass4 != tt.wantPass4 {
				t.Fatalf("pass 4 filter spec changed\ngot:  %s\nwant: %s", gotPass4, tt.wantPass4)
			}
		})
	}
}

func TestPlanLimiterForLoudnormMatchesInlineCalculation(t *testing.T) {
	tests := []struct {
		name       string
		outputI    float64
		outputTP   float64
		wantNeeded bool
		wantClamp  bool
	}{
		{
			name:       "non-limited",
			outputI:    -20.0,
			outputTP:   -10.0,
			wantNeeded: false,
			wantClamp:  false,
		},
		{
			name:       "limited",
			outputI:    -24.9,
			outputTP:   -5.0,
			wantNeeded: true,
			wantClamp:  false,
		},
		{
			name:       "clamped",
			outputI:    -43.2,
			outputTP:   -18.6,
			wantNeeded: true,
			wantClamp:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := defaultNormalisationTestConfig()
			output := &OutputMeasurements{
				OutputI:  tt.outputI,
				OutputTP: tt.outputTP,
			}

			wantCeiling, wantNeeded, wantClamped := calculateLimiterCeiling(
				output.OutputI,
				output.OutputTP,
				config.Loudnorm.TargetI,
				config.Loudnorm.TargetTP,
			)
			wantPreGainDB, reDerivedCeiling := calculatePreGain(
				output.OutputI,
				config.Loudnorm.TargetI,
				config.Loudnorm.TargetTP,
			)
			if wantClamped {
				wantCeiling = reDerivedCeiling
			}
			wantGainDB := config.Loudnorm.TargetI - output.OutputI
			wantPrefix := buildPreLimiterPrefix(wantPreGainDB, wantCeiling, wantNeeded)

			got := planLimiterForLoudnorm(output, config)

			if got.needed != wantNeeded || got.needed != tt.wantNeeded {
				t.Fatalf("needed = %v, want inline %v and case %v", got.needed, wantNeeded, tt.wantNeeded)
			}
			if got.clamped != wantClamped || got.clamped != tt.wantClamp {
				t.Fatalf("clamped = %v, want inline %v and case %v", got.clamped, wantClamped, tt.wantClamp)
			}
			if math.Abs(got.preGainDB-wantPreGainDB) > 0.01 {
				t.Fatalf("preGainDB = %.2f, want %.2f", got.preGainDB, wantPreGainDB)
			}
			if math.Abs(got.ceilingDB-wantCeiling) > 0.01 {
				t.Fatalf("ceilingDB = %.2f, want %.2f", got.ceilingDB, wantCeiling)
			}
			if math.Abs(got.gainDB-wantGainDB) > 0.01 {
				t.Fatalf("gainDB = %.2f, want %.2f", got.gainDB, wantGainDB)
			}
			if got.pass3Prefix != wantPrefix {
				t.Fatalf("pass3Prefix = %q, want %q", got.pass3Prefix, wantPrefix)
			}
		})
	}
}
