// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

const loudnormCaptureTestJSON = `{"input_i":"-23.0","input_tp":"-4.0","input_lra":"5.0","input_thresh":"-33.0","output_i":"-16.0","output_tp":"-2.0","output_lra":"5.0","output_thresh":"-26.0","normalization_type":"linear","target_offset":"0.0"}`

func replaceLoudnormLogOps(
	t *testing.T,
	getLevel func() (int, error),
	setLevel func(int),
	setCallback func(ffmpeg.LogCallback),
) {
	t.Helper()

	oldGetLevel := loudnormAVLogGetLevel
	oldSetLevel := loudnormAVLogSetLevel
	oldSetCallback := loudnormAVLogSetCallback

	loudnormAVLogGetLevel = getLevel
	loudnormAVLogSetLevel = setLevel
	loudnormAVLogSetCallback = setCallback

	t.Cleanup(func() {
		loudnormAVLogGetLevel = oldGetLevel
		loudnormAVLogSetLevel = oldSetLevel
		loudnormAVLogSetCallback = oldSetCallback
	})
}

func TestLoudnormCaptureLogOperationSeams(t *testing.T) {
	var calls []string
	replaceLoudnormLogOps(t,
		func() (int, error) {
			calls = append(calls, "get-level")
			return 7, nil
		},
		func(level int) {
			calls = append(calls, fmt.Sprintf("set-level-%d", level))
		},
		func(callback ffmpeg.LogCallback) {
			if callback == nil {
				calls = append(calls, "set-callback-nil")
				return
			}
			calls = append(calls, "set-callback-capture")
		},
	)

	startLoudnormCapture()
	loudnormLogCallback(nil, ffmpeg.AVLogInfo, loudnormCaptureTestJSON)
	stats, err := stopLoudnormCapture()
	if err != nil {
		t.Fatalf("stopLoudnormCapture() error = %v", err)
	}
	if stats.InputI != "-23.0" {
		t.Fatalf("stats.InputI = %q, want -23.0", stats.InputI)
	}

	wantCalls := []string{
		"get-level",
		fmt.Sprintf("set-level-%d", ffmpeg.AVLogInfo),
		"set-callback-capture",
		"set-callback-nil",
		"set-level-7",
	}
	if strings.Join(calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestLoudnormCaptureSerialisesFullLifecycle(t *testing.T) {
	replaceLoudnormLogOps(t,
		func() (int, error) { return 7, nil },
		func(int) {},
		func(ffmpeg.LogCallback) {},
	)

	startLoudnormCapture()

	secondStarted := make(chan struct{})
	go func() {
		startLoudnormCapture()
		close(secondStarted)
	}()

	select {
	case <-secondStarted:
		t.Fatal("second capture started before first capture stopped")
	case <-time.After(50 * time.Millisecond):
	}

	loudnormLogCallback(nil, ffmpeg.AVLogInfo, loudnormCaptureTestJSON)
	if _, err := stopLoudnormCapture(); err != nil {
		t.Fatalf("first stopLoudnormCapture() error = %v", err)
	}

	select {
	case <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second capture did not start after first capture stopped")
	}

	loudnormLogCallback(nil, ffmpeg.AVLogInfo, loudnormCaptureTestJSON)
	if _, err := stopLoudnormCapture(); err != nil {
		t.Fatalf("second stopLoudnormCapture() error = %v", err)
	}
}

func TestLoudnormCaptureSessionStopDiscardAfterStopDoesNotDoubleStop(t *testing.T) {
	var callbackStops int
	replaceLoudnormLogOps(t,
		func() (int, error) { return 7, nil },
		func(int) {},
		func(callback ffmpeg.LogCallback) {
			if callback == nil {
				callbackStops++
			}
		},
	)

	capture := beginLoudnormCapture()
	defer capture.StopDiscard()

	loudnormLogCallback(nil, ffmpeg.AVLogInfo, loudnormCaptureTestJSON)
	if _, err := capture.Stop(); err != nil {
		t.Fatalf("capture.Stop() error = %v", err)
	}

	capture.StopDiscard()

	if callbackStops != 1 {
		t.Fatalf("stop callback count = %d, want 1", callbackStops)
	}
}

func TestLoudnormCaptureSessionSerialisesFullLifecycle(t *testing.T) {
	replaceLoudnormLogOps(t,
		func() (int, error) { return 7, nil },
		func(int) {},
		func(ffmpeg.LogCallback) {},
	)

	first := beginLoudnormCapture()
	defer first.StopDiscard()

	secondStarted := make(chan *loudnormCaptureSession, 1)
	go func() {
		secondStarted <- beginLoudnormCapture()
	}()

	select {
	case second := <-secondStarted:
		second.StopDiscard()
		t.Fatal("second capture started before first capture stopped")
	case <-time.After(50 * time.Millisecond):
	}

	loudnormLogCallback(nil, ffmpeg.AVLogInfo, loudnormCaptureTestJSON)
	if _, err := first.Stop(); err != nil {
		t.Fatalf("first capture.Stop() error = %v", err)
	}

	var second *loudnormCaptureSession
	select {
	case second = <-secondStarted:
	case <-time.After(time.Second):
		t.Fatal("second capture did not start after first capture stopped")
	}
	defer second.StopDiscard()

	loudnormLogCallback(nil, ffmpeg.AVLogInfo, loudnormCaptureTestJSON)
	if _, err := second.Stop(); err != nil {
		t.Fatalf("second capture.Stop() error = %v", err)
	}
}

func TestLoudnormCaptureSessionMalformedJSONRestoresLogOps(t *testing.T) {
	var (
		callbackStops int
		levels        []int
	)
	replaceLoudnormLogOps(t,
		func() (int, error) { return 7, nil },
		func(level int) {
			levels = append(levels, level)
		},
		func(callback ffmpeg.LogCallback) {
			if callback == nil {
				callbackStops++
			}
		},
	)

	capture := beginLoudnormCapture()
	defer capture.StopDiscard()

	loudnormLogCallback(nil, ffmpeg.AVLogInfo, "{not-json}")
	_, err := capture.Stop()
	if err == nil {
		t.Fatal("capture.Stop() error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), "failed to parse loudnorm JSON") {
		t.Fatalf("capture.Stop() error = %q, want malformed JSON context", err.Error())
	}
	if callbackStops != 1 {
		t.Fatalf("stop callback count = %d, want 1", callbackStops)
	}

	gotLevels := fmt.Sprint(levels)
	wantLevels := fmt.Sprintf("[%d 7]", ffmpeg.AVLogInfo)
	if gotLevels != wantLevels {
		t.Fatalf("log levels = %s, want %s", gotLevels, wantLevels)
	}
}

func TestCaptureLoudnormGraphFinalisationSuccess(t *testing.T) {
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, true)

	var graph *ffmpeg.AVFilterGraph
	stats, err := captureLoudnormGraphFinalisation(&graph)
	if err != nil {
		t.Fatalf("captureLoudnormGraphFinalisation() error = %v", err)
	}
	if stats.InputI != "-23.0" {
		t.Fatalf("stats.InputI = %q, want -23.0", stats.InputI)
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	if recorder.freeCount() != 1 {
		t.Fatalf("graph free count = %d, want 1", recorder.freeCount())
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
}

func TestCaptureLoudnormGraphFinalisationMalformedJSON(t *testing.T) {
	recorder := installLoudnormCleanupRecorder(t)

	oldFree := loudnormAVFilterGraphFree
	loudnormAVFilterGraphFree = func(graph **ffmpeg.AVFilterGraph) {
		recorder.recordFree()
		loudnormLogCallback(nil, ffmpeg.AVLogInfo, "{not-json}")
		if graph != nil && *graph != nil {
			oldFree(graph)
		}
	}
	t.Cleanup(func() {
		loudnormAVFilterGraphFree = oldFree
	})

	var graph *ffmpeg.AVFilterGraph
	_, err := captureLoudnormGraphFinalisation(&graph)
	if err == nil {
		t.Fatal("captureLoudnormGraphFinalisation() error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), "failed to capture loudnorm graph finalisation") {
		t.Fatalf("captureLoudnormGraphFinalisation() error = %q, want graph finalisation context", err.Error())
	}
	if !strings.Contains(err.Error(), "failed to parse loudnorm JSON") {
		t.Fatalf("captureLoudnormGraphFinalisation() error = %q, want malformed JSON context", err.Error())
	}
	if recorder.freeCount() != 1 {
		t.Fatalf("graph free count = %d, want 1", recorder.freeCount())
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
}

func TestCaptureLoudnormGraphFinalisationMissingJSON(t *testing.T) {
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, false)

	var graph *ffmpeg.AVFilterGraph
	_, err := captureLoudnormGraphFinalisation(&graph)
	if err == nil {
		t.Fatal("captureLoudnormGraphFinalisation() error = nil, want missing JSON error")
	}
	if !strings.Contains(err.Error(), "failed to capture loudnorm graph finalisation") {
		t.Fatalf("captureLoudnormGraphFinalisation() error = %q, want graph finalisation context", err.Error())
	}
	if !strings.Contains(err.Error(), "no JSON found in loudnorm output") {
		t.Fatalf("captureLoudnormGraphFinalisation() error = %q, want missing JSON context", err.Error())
	}
	if recorder.freeCount() != 1 {
		t.Fatalf("graph free count = %d, want 1", recorder.freeCount())
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
}

func TestCaptureLoudnormGraphFinalisationSerialisesGlobalCapture(t *testing.T) {
	var (
		mu             sync.Mutex
		order          []string
		getLevelCalls  int
		graphFreeCalls int
	)
	record := func(value string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, value)
	}
	orderString := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(order, ",")
	}

	replaceLoudnormLogOps(t,
		func() (int, error) {
			mu.Lock()
			defer mu.Unlock()
			getLevelCalls++
			order = append(order, fmt.Sprintf("get-level-%d", getLevelCalls))
			return 20 + getLevelCalls, nil
		},
		func(level int) {
			if level == ffmpeg.AVLogInfo {
				record("set-level-info")
				return
			}
			record(fmt.Sprintf("restore-level-%d", level))
		},
		func(callback ffmpeg.LogCallback) {
			if callback == nil {
				record("set-callback-nil")
				return
			}
			record("set-callback-capture")
		},
	)

	firstFreeEntered := make(chan struct{})
	releaseFirstFree := make(chan struct{})

	oldFree := loudnormAVFilterGraphFree
	loudnormAVFilterGraphFree = func(graph **ffmpeg.AVFilterGraph) {
		mu.Lock()
		graphFreeCalls++
		call := graphFreeCalls
		order = append(order, fmt.Sprintf("free-%d", call))
		mu.Unlock()

		if call == 1 {
			close(firstFreeEntered)
			<-releaseFirstFree
		}

		jsonOutput := strings.Replace(loudnormCaptureTestJSON, `"input_i":"-23.0"`, fmt.Sprintf(`"input_i":"-%d.0"`, 20+call), 1)
		loudnormLogCallback(nil, ffmpeg.AVLogInfo, jsonOutput)
		if graph != nil && *graph != nil {
			oldFree(graph)
		}
	}
	t.Cleanup(func() {
		loudnormAVFilterGraphFree = oldFree
	})

	type captureResult struct {
		stats *LoudnormStats
		err   error
	}
	firstDone := make(chan captureResult, 1)
	secondDone := make(chan captureResult, 1)

	var firstGraph *ffmpeg.AVFilterGraph
	go func() {
		stats, err := captureLoudnormGraphFinalisation(&firstGraph)
		firstDone <- captureResult{stats: stats, err: err}
	}()

	select {
	case <-firstFreeEntered:
	case <-time.After(time.Second):
		t.Fatal("first graph free did not start")
	}

	var secondGraph *ffmpeg.AVFilterGraph
	go func() {
		stats, err := captureLoudnormGraphFinalisation(&secondGraph)
		secondDone <- captureResult{stats: stats, err: err}
	}()

	select {
	case result := <-secondDone:
		t.Fatalf("second capture completed before first capture stopped: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}

	wantBlockedOrder := "get-level-1,set-level-info,set-callback-capture,free-1"
	if gotOrder := orderString(); gotOrder != wantBlockedOrder {
		t.Fatalf("order while first graph free is blocked = %s, want %s", gotOrder, wantBlockedOrder)
	}

	close(releaseFirstFree)

	firstResult := <-firstDone
	if firstResult.err != nil {
		t.Fatalf("first captureLoudnormGraphFinalisation() error = %v", firstResult.err)
	}
	if firstResult.stats.InputI != "-21.0" {
		t.Fatalf("first stats.InputI = %q, want -21.0", firstResult.stats.InputI)
	}

	var secondResult captureResult
	select {
	case secondResult = <-secondDone:
	case <-time.After(time.Second):
		t.Fatal("second capture did not complete after first capture stopped")
	}
	if secondResult.err != nil {
		t.Fatalf("second captureLoudnormGraphFinalisation() error = %v", secondResult.err)
	}
	if secondResult.stats.InputI != "-22.0" {
		t.Fatalf("second stats.InputI = %q, want -22.0", secondResult.stats.InputI)
	}

	wantOrder := strings.Join([]string{
		"get-level-1",
		"set-level-info",
		"set-callback-capture",
		"free-1",
		"set-callback-nil",
		"restore-level-21",
		"get-level-2",
		"set-level-info",
		"set-callback-capture",
		"free-2",
		"set-callback-nil",
		"restore-level-22",
	}, ",")
	if gotOrder := orderString(); gotOrder != wantOrder {
		t.Fatalf("serialised order = %s, want %s", gotOrder, wantOrder)
	}
}

func TestMeasureWithLoudnormDoesNotStartCaptureOnOpenError(t *testing.T) {
	var callbackStarts, callbackStops int
	replaceLoudnormLogOps(t,
		func() (int, error) { return 7, nil },
		func(int) {},
		func(callback ffmpeg.LogCallback) {
			if callback == nil {
				callbackStops++
				return
			}
			callbackStarts++
		},
	)

	_, err := measureWithLoudnorm("/does/not/exist.wav", defaultNormalisationTestConfig(), "", nil)
	if err == nil {
		t.Fatal("measureWithLoudnorm() error = nil, want open error")
	}
	if callbackStarts != 0 {
		t.Fatalf("capture callback install count = %d, want 0", callbackStarts)
	}
	if callbackStops != 0 {
		t.Fatalf("capture callback stop count = %d, want 0", callbackStops)
	}
}

func TestMeasureWithLoudnormDoesNotStartCaptureOnSetupError(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)

	var callbackStarts, callbackStops int
	replaceLoudnormLogOps(t,
		func() (int, error) { return 7, nil },
		func(int) {},
		func(callback ffmpeg.LogCallback) {
			if callback == nil {
				callbackStops++
				return
			}
			callbackStarts++
		},
	)

	_, err := measureWithLoudnorm(testFile, defaultNormalisationTestConfig(), "not_a_real_filter", nil)
	if err == nil {
		t.Fatal("measureWithLoudnorm() error = nil, want setup error")
	}
	if !strings.Contains(err.Error(), "failed to create filter graph") {
		t.Fatalf("measureWithLoudnorm() error = %q, want filter graph context", err.Error())
	}
	if callbackStarts != 0 {
		t.Fatalf("capture callback install count = %d, want 0", callbackStarts)
	}
	if callbackStops != 0 {
		t.Fatalf("capture callback stop count = %d, want 0", callbackStops)
	}
}

func TestMeasureWithLoudnormLoopErrorFreesGraphBeforeStoppingCapture(t *testing.T) {
	testFile := generateTestAudio(t, TestAudioOptions{
		DurationSecs: 0.2,
		SampleRate:   44100,
		ToneFreq:     440.0,
		ToneLevel:    -18.0,
		Dir:          t.TempDir(),
	})
	defer cleanupTestAudio(t, testFile)

	recorder := installLoudnormCleanupRecorder(t)

	oldFree := loudnormAVFilterGraphFree
	loudnormAVFilterGraphFree = func(graph **ffmpeg.AVFilterGraph) {
		recorder.recordFree()
		loudnormLogCallback(nil, ffmpeg.AVLogInfo, "{not-json}")
		oldFree(graph)
	}
	t.Cleanup(func() {
		loudnormAVFilterGraphFree = oldFree
	})

	runErr := errors.New("injected frame loop failure")
	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return runErr
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	_, err := measureWithLoudnorm(testFile, defaultNormalisationTestConfig(), "", nil)
	if !errors.Is(err, runErr) {
		t.Fatalf("measureWithLoudnorm() error = %v, want wrapped run error", err)
	}
	if !strings.Contains(err.Error(), "loudnorm measurement loop failed") {
		t.Fatalf("measureWithLoudnorm() error = %q, want measurement loop context", err.Error())
	}
	if strings.Contains(err.Error(), "failed to capture loudnorm measurements") {
		t.Fatalf("measureWithLoudnorm() error = %q, want loop error precedence over capture error", err.Error())
	}

	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	if recorder.freeCount() != 1 {
		t.Fatalf("graph free count = %d, want 1", recorder.freeCount())
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
}

func TestMeasureWithLoudnormSuccessfulLoopRequiresCapturedJSON(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, false)

	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return nil
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	_, err := measureWithLoudnorm(testFile, defaultNormalisationTestConfig(), "", nil)
	if err == nil {
		t.Fatal("measureWithLoudnorm() error = nil, want missing JSON error")
	}
	if !strings.Contains(err.Error(), "failed to capture loudnorm measurements") {
		t.Fatalf("measureWithLoudnorm() error = %q, want capture context", err.Error())
	}
	if !strings.Contains(err.Error(), "no JSON found in loudnorm output") {
		t.Fatalf("measureWithLoudnorm() error = %q, want missing JSON context", err.Error())
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
}

func TestMeasureWithLoudnormSuccessfulLoopRejectsMalformedJSON(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)

	oldFree := loudnormAVFilterGraphFree
	loudnormAVFilterGraphFree = func(graph **ffmpeg.AVFilterGraph) {
		recorder.recordFree()
		loudnormLogCallback(nil, ffmpeg.AVLogInfo, "{not-json}")
		oldFree(graph)
	}
	t.Cleanup(func() {
		loudnormAVFilterGraphFree = oldFree
	})

	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return nil
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	_, err := measureWithLoudnorm(testFile, defaultNormalisationTestConfig(), "", nil)
	if err == nil {
		t.Fatal("measureWithLoudnorm() error = nil, want malformed JSON error")
	}
	if !strings.Contains(err.Error(), "failed to capture loudnorm measurements") {
		t.Fatalf("measureWithLoudnorm() error = %q, want capture context", err.Error())
	}
	if !strings.Contains(err.Error(), "failed to parse loudnorm JSON") {
		t.Fatalf("measureWithLoudnorm() error = %q, want malformed JSON context", err.Error())
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
}

func TestMeasureWithLoudnormSuccessfulLoopParsesCapturedJSON(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, true)

	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return nil
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	measurement, err := measureWithLoudnorm(testFile, defaultNormalisationTestConfig(), "", nil)
	if err != nil {
		t.Fatalf("measureWithLoudnorm() error = %v", err)
	}
	if measurement.InputI != -23.0 {
		t.Fatalf("measurement.InputI = %.1f, want -23.0", measurement.InputI)
	}
	if measurement.InputTP != -4.0 {
		t.Fatalf("measurement.InputTP = %.1f, want -4.0", measurement.InputTP)
	}
	if measurement.InputLRA != 5.0 {
		t.Fatalf("measurement.InputLRA = %.1f, want 5.0", measurement.InputLRA)
	}
	if measurement.InputThresh != -33.0 {
		t.Fatalf("measurement.InputThresh = %.1f, want -33.0", measurement.InputThresh)
	}
	if measurement.TargetOffset != 0.0 {
		t.Fatalf("measurement.TargetOffset = %.1f, want 0.0", measurement.TargetOffset)
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
}

func TestMeasureWithLoudnormSuccessfulLoopRejectsInvalidNumericField(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)

	oldFree := loudnormAVFilterGraphFree
	loudnormAVFilterGraphFree = func(graph **ffmpeg.AVFilterGraph) {
		recorder.recordFree()
		loudnormLogCallback(nil, ffmpeg.AVLogInfo, strings.Replace(loudnormCaptureTestJSON, `"input_i":"-23.0"`, `"input_i":"not-a-number"`, 1))
		oldFree(graph)
	}
	t.Cleanup(func() {
		loudnormAVFilterGraphFree = oldFree
	})

	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return nil
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	_, err := measureWithLoudnorm(testFile, defaultNormalisationTestConfig(), "", nil)
	if err == nil {
		t.Fatal("measureWithLoudnorm() error = nil, want invalid numeric field error")
	}
	if !strings.Contains(err.Error(), `invalid loudnorm input_i value "not-a-number"`) {
		t.Fatalf("measureWithLoudnorm() error = %q, want input_i parse context", err.Error())
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
}

func TestMeasureWithLoudnormGeneratedAudioCapturesGraphFinalisationJSON(t *testing.T) {
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

	measurement, err := measureWithLoudnorm(testFile, defaultNormalisationTestConfig(), "", nil)
	if err != nil {
		t.Fatalf("measureWithLoudnorm() error = %v", err)
	}
	if measurement == nil {
		t.Fatal("measureWithLoudnorm() measurement = nil, want captured loudnorm measurement")
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
			t.Fatalf("measurement.%s = %v, want finite value from graph-finalisation JSON", name, value)
		}
	}
}

type loudnormCleanupRecorder struct {
	mu     sync.Mutex
	order  []string
	levels []int
	stops  int
}

func (r *loudnormCleanupRecorder) recordFree() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.order = append(r.order, "free")
}

func (r *loudnormCleanupRecorder) recordLevel(level int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.levels = append(r.levels, level)
}

func (r *loudnormCleanupRecorder) recordStop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops++
	r.order = append(r.order, "stop")
}

func (r *loudnormCleanupRecorder) freeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	var total int
	for _, entry := range r.order {
		if entry == "free" {
			total++
		}
	}
	return total
}

func (r *loudnormCleanupRecorder) orderString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.order, ",")
}

func (r *loudnormCleanupRecorder) stopCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stops
}

func (r *loudnormCleanupRecorder) levelString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprint(r.levels)
}

func installLoudnormCleanupRecorder(t *testing.T) *loudnormCleanupRecorder {
	t.Helper()

	recorder := &loudnormCleanupRecorder{}
	replaceLoudnormLogOps(t,
		func() (int, error) { return 7, nil },
		func(level int) {
			recorder.recordLevel(level)
		},
		func(callback ffmpeg.LogCallback) {
			if callback == nil {
				recorder.recordStop()
			}
		},
	)
	return recorder
}

func requireLoudnormCaptureStoppedOnce(t *testing.T, recorder *loudnormCleanupRecorder) {
	t.Helper()

	if recorder.stopCount() != 1 {
		t.Fatalf("capture stop count = %d, want 1", recorder.stopCount())
	}

	wantLevels := fmt.Sprintf("[%d 7]", ffmpeg.AVLogInfo)
	if gotLevels := recorder.levelString(); gotLevels != wantLevels {
		t.Fatalf("log levels = %s, want %s", gotLevels, wantLevels)
	}
}

func replaceApplyLoudnormGraphFree(t *testing.T, recorder *loudnormCleanupRecorder, emitJSON bool) {
	t.Helper()

	oldFree := loudnormAVFilterGraphFree
	loudnormAVFilterGraphFree = func(graph **ffmpeg.AVFilterGraph) {
		recorder.recordFree()
		if emitJSON {
			loudnormLogCallback(nil, ffmpeg.AVLogInfo, loudnormCaptureTestJSON)
		}
		if graph != nil && *graph != nil {
			oldFree(graph)
		}
	}
	t.Cleanup(func() {
		loudnormAVFilterGraphFree = oldFree
	})
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
	config.AdeclickEnabled = false
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
) (float64, float64, *OutputMeasurements, *LoudnormStats, time.Duration, error) {
	t.Helper()

	return applyLoudnormAndMeasure(
		inputPath,
		loudnormApplicationTestConfig(),
		loudnormApplicationTestMeasurement(),
		nil,
		0,
		0,
		false,
		nil,
	)
}

func TestApplyLoudnormAndMeasureDoesNotStartCaptureOnOpenError(t *testing.T) {
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, false)

	_, _, _, _, _, err := applyLoudnormTest(t, "/does/not/exist.wav")
	if err == nil {
		t.Fatal("applyLoudnormAndMeasure() error = nil, want open error")
	}
	if !strings.Contains(err.Error(), "failed to open input") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want open context", err.Error())
	}
	if recorder.freeCount() != 0 {
		t.Fatalf("graph free count = %d, want 0", recorder.freeCount())
	}
	if recorder.stopCount() != 0 {
		t.Fatalf("capture stop count = %d, want 0", recorder.stopCount())
	}
}

func TestApplyLoudnormAndMeasureSetupErrorDoesNotStartCaptureOrFreeGraph(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, false)

	setupErr := errors.New("injected setup failure")
	replaceApplyLoudnormSetupFilterGraph(t, func(
		*ffmpeg.AVCodecContext,
		string,
	) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {
		return nil, nil, nil, setupErr
	})

	_, _, _, _, _, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, setupErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want wrapped setup error", err)
	}
	if !strings.Contains(err.Error(), "failed to create filter graph") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want filter graph context", err.Error())
	}
	if recorder.freeCount() != 0 {
		t.Fatalf("graph free count = %d, want 0", recorder.freeCount())
	}
	if recorder.stopCount() != 0 {
		t.Fatalf("capture stop count = %d, want 0", recorder.stopCount())
	}
	requireNoLoudnormTempFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureEncoderCreationErrorFreesGraphBeforeStoppingCapture(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, true)

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

	_, _, _, _, _, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, createErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want wrapped encoder creation error", err)
	}
	if !strings.Contains(err.Error(), "failed to create encoder") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want encoder context", err.Error())
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
	if tempPath == "" {
		t.Fatal("encoder was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after encoder creation failure", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureLoopErrorFreesGraphBeforeStoppingCapture(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, true)
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
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return runErr
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	_, _, _, _, _, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, runErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want loop error", err)
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	if encoder.closeN != 1 {
		t.Fatalf("encoder close calls = %d, want 1", encoder.closeN)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
	if tempPath == "" {
		t.Fatal("encoder was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after loop failure", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureFlushErrorFreesGraphBeforeStoppingCapture(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, true)

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
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return nil
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	_, _, _, _, _, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, flushErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want wrapped flush error", err)
	}
	if !strings.Contains(err.Error(), "failed to flush encoder") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want flush context", err.Error())
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	if encoder.closeN != 1 {
		t.Fatalf("encoder close calls = %d, want 1", encoder.closeN)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
	if tempPath == "" {
		t.Fatal("encoder was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after flush failure", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureCloseErrorFreesGraphBeforeStoppingCapture(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, true)

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
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		return nil
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	_, _, _, _, _, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, closeErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want wrapped close error", err)
	}
	if !strings.Contains(err.Error(), "failed to close encoder") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want close context", err.Error())
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	if encoder.closeN != 1 {
		t.Fatalf("encoder close calls = %d, want 1", encoder.closeN)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
	if tempPath == "" {
		t.Fatal("encoder was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after close failure", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureRenameErrorFreesGraphBeforeStoppingCapture(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, true)
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

	_, _, _, _, _, err := applyLoudnormTest(t, testFile)
	if !errors.Is(err, renameErr) {
		t.Fatalf("applyLoudnormAndMeasure() error = %v, want wrapped rename error", err)
	}
	if !strings.Contains(err.Error(), "failed to rename output") {
		t.Fatalf("applyLoudnormAndMeasure() error = %q, want rename context", err.Error())
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	if encoder.closeN != 1 {
		t.Fatalf("encoder close calls = %d, want 1", encoder.closeN)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
	if tempPath == "" {
		t.Fatal("rename was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after rename failure", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureSuccessFreesGraphBeforeStoppingCapture(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, true)
	var tempPath string
	replaceApplyLoudnormRename(t, func(oldPath, newPath string) error {
		tempPath = oldPath
		requireLoudnormTempPath(t, testFile, oldPath)
		return os.Rename(oldPath, newPath)
	})

	finalLUFS, _, finalMeasurements, stats, _, err := applyLoudnormTest(t, testFile)
	if err != nil {
		t.Fatalf("applyLoudnormAndMeasure() error = %v", err)
	}
	if stats == nil {
		t.Fatal("loudnorm stats = nil, want parsed stats")
	}
	if stats.OutputI != "-16.0" {
		t.Fatalf("stats.OutputI = %q, want -16.0", stats.OutputI)
	}
	if finalMeasurements == nil {
		t.Fatal("final measurements = nil, want measurements")
	}
	if math.IsNaN(finalLUFS) {
		t.Fatal("final LUFS is NaN")
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
	if tempPath == "" {
		t.Fatal("rename was not given a temp output path")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("temp file stat error = %v, want not exist after successful rename", err)
	}
	requireNoLoudnormTempFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureEncodingAndPublishRunOutsideCapture(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)

	var (
		mu               sync.Mutex
		captureActive    bool
		callbackStarts   int
		opsDuringCapture []string
		encoderPath      string
		renameOldPath    string
		renameNewPath    string
	)
	recordOperation := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		if captureActive {
			opsDuringCapture = append(opsDuringCapture, name)
		}
	}
	callbackStartCount := func() int {
		mu.Lock()
		defer mu.Unlock()
		return callbackStarts
	}
	operationsDuringCapture := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), opsDuringCapture...)
	}

	replaceLoudnormLogOps(t,
		func() (int, error) { return 7, nil },
		func(int) {},
		func(callback ffmpeg.LogCallback) {
			mu.Lock()
			defer mu.Unlock()
			if callback == nil {
				captureActive = false
				return
			}
			captureActive = true
			callbackStarts++
		},
	)

	graphFinalisationEntered := make(chan struct{})
	oldFree := loudnormAVFilterGraphFree
	loudnormAVFilterGraphFree = func(graph **ffmpeg.AVFilterGraph) {
		recordOperation("graph-free")
		close(graphFinalisationEntered)
		loudnormLogCallback(nil, ffmpeg.AVLogInfo, loudnormCaptureTestJSON)
		oldFree(graph)
	}
	t.Cleanup(func() {
		loudnormAVFilterGraphFree = oldFree
	})

	flushEntered := make(chan struct{})
	releaseFlush := make(chan struct{})
	encoder := &loudnormTestEncoder{
		writeFrame: func(*ffmpeg.AVFrame) error {
			recordOperation("write-frame")
			return nil
		},
		flush: func() error {
			recordOperation("flush")
			close(flushEntered)
			<-releaseFlush
			return nil
		},
		close: func() error {
			recordOperation("close")
			return nil
		},
	}
	replaceApplyLoudnormCreateEncoder(t, func(
		outputPath string,
		_ *audio.Metadata,
		_ *ffmpeg.AVFilterContext,
	) (loudnormOutputEncoder, error) {
		mu.Lock()
		encoderPath = outputPath
		mu.Unlock()
		recordOperation("create-encoder")
		return encoder, nil
	})

	oldRun := loudnormRunFilterGraph
	loudnormRunFilterGraph = func(
		reader *audio.Reader,
		bufferSrcCtx, bufferSinkCtx *ffmpeg.AVFilterContext,
		config FrameLoopConfig,
	) error {
		frame := ffmpeg.AVFrameAlloc()
		defer ffmpeg.AVFrameFree(&frame)
		return config.OnFrame(nil, frame)
	}
	t.Cleanup(func() {
		loudnormRunFilterGraph = oldRun
	})

	replaceApplyLoudnormRename(t, func(oldPath, newPath string) error {
		mu.Lock()
		renameOldPath = oldPath
		renameNewPath = newPath
		mu.Unlock()
		recordOperation("rename")
		return os.Rename(oldPath, newPath)
	})

	done := make(chan error, 1)
	go func() {
		_, _, _, _, _, err := applyLoudnormTest(t, testFile)
		done <- err
	}()

	select {
	case <-flushEntered:
	case <-time.After(time.Second):
		t.Fatal("flush did not start")
	}
	if got := callbackStartCount(); got != 0 {
		t.Fatalf("capture callback install count while flush blocked = %d, want 0", got)
	}

	close(releaseFlush)

	select {
	case <-graphFinalisationEntered:
	case <-time.After(time.Second):
		t.Fatal("graph finalisation did not start")
	}
	if got := callbackStartCount(); got != 1 {
		t.Fatalf("capture callback install count after graph finalisation starts = %d, want 1", got)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("applyLoudnormAndMeasure() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("applyLoudnormAndMeasure() did not complete")
	}

	if got := operationsDuringCapture(); strings.Join(got, ",") != "graph-free" {
		t.Fatalf("operations during capture = %v, want [graph-free]", got)
	}

	mu.Lock()
	gotEncoderPath := encoderPath
	gotRenameOldPath := renameOldPath
	gotRenameNewPath := renameNewPath
	mu.Unlock()
	requireLoudnormTempPath(t, testFile, gotEncoderPath)
	requireLoudnormTempPath(t, testFile, gotRenameOldPath)
	if gotRenameNewPath != testFile {
		t.Fatalf("rename target = %q, want %q", gotRenameNewPath, testFile)
	}
}

func TestApplyLoudnormAndMeasureMissingPass4JSONReturnsNilStatsWithoutError(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)
	replaceApplyLoudnormGraphFree(t, recorder, false)
	replaceApplyLoudnormRename(t, func(oldPath, newPath string) error {
		requireLoudnormTempPath(t, testFile, oldPath)
		return os.Rename(oldPath, newPath)
	})

	_, _, finalMeasurements, stats, _, err := applyLoudnormTest(t, testFile)
	if err != nil {
		t.Fatalf("applyLoudnormAndMeasure() error = %v", err)
	}
	if stats != nil {
		t.Fatalf("loudnorm stats = %+v, want nil", stats)
	}
	if finalMeasurements == nil {
		t.Fatal("final measurements = nil, want measurements")
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
	requireNoLoudnormTempFiles(t, testFile)
}

func TestApplyLoudnormAndMeasureMalformedPass4JSONReturnsNilStatsWithoutError(t *testing.T) {
	testFile := generateLoudnormApplicationTestAudio(t)
	recorder := installLoudnormCleanupRecorder(t)

	oldFree := loudnormAVFilterGraphFree
	loudnormAVFilterGraphFree = func(graph **ffmpeg.AVFilterGraph) {
		recorder.recordFree()
		loudnormLogCallback(nil, ffmpeg.AVLogInfo, "{not-json}")
		oldFree(graph)
	}
	t.Cleanup(func() {
		loudnormAVFilterGraphFree = oldFree
	})

	replaceApplyLoudnormRename(t, func(oldPath, newPath string) error {
		requireLoudnormTempPath(t, testFile, oldPath)
		return os.Rename(oldPath, newPath)
	})

	_, _, finalMeasurements, stats, _, err := applyLoudnormTest(t, testFile)
	if err != nil {
		t.Fatalf("applyLoudnormAndMeasure() error = %v", err)
	}
	if stats != nil {
		t.Fatalf("loudnorm stats = %+v, want nil", stats)
	}
	if finalMeasurements == nil {
		t.Fatal("final measurements = nil, want measurements")
	}
	if gotOrder := recorder.orderString(); gotOrder != "free,stop" {
		t.Fatalf("cleanup order = %s, want free,stop", gotOrder)
	}
	requireLoudnormCaptureStoppedOnce(t, recorder)
	requireNoLoudnormTempFiles(t, testFile)
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

			// Pre-compute values (caller's responsibility after Task 2.2)
			ceiling, needsLimiting, clamped := calculateLimiterCeiling(
				tt.inputI, tt.inputTP, config.LoudnormTargetI, config.LoudnormTargetTP,
			)
			preGainDB, reDerivedCeiling := calculatePreGain(
				tt.inputI, config.LoudnormTargetI, config.LoudnormTargetTP,
			)
			if clamped {
				ceiling = reDerivedCeiling
			}

			filterSpec := buildLoudnormFilterSpec(config, measurement, preGainDB, ceiling, needsLimiting)

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
	config.ResampleEnabled = false
	config.ResampleSampleRate = 48000
	config.ResampleFormat = "s32"
	config.ResampleFrameSize = 2048

	measurement := &LoudnormMeasurement{
		InputI:       -24.0,
		InputTP:      -5.0,
		InputLRA:     6.0,
		InputThresh:  -34.0,
		TargetOffset: -0.5,
	}

	filterSpec := buildLoudnormFilterSpec(config, measurement, 0, -1.0, false)

	if config.ResampleEnabled {
		t.Error("buildLoudnormFilterSpec mutated config.ResampleEnabled")
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

		filterSpec := buildLoudnormFilterSpec(config, measurement, 0, -1.0, false)

		const want = "adeclick=t=2.0:w=55:o=50:m=s"
		if !strings.Contains(filterSpec, want) {
			t.Errorf("buildLoudnormFilterSpec() missing %q\nfilterSpec: %s", want, filterSpec)
		}
	})

	t.Run("omits disabled adeclick", func(t *testing.T) {
		config := defaultNormalisationTestConfig()
		config.AdeclickEnabled = false

		filterSpec := buildLoudnormFilterSpec(config, measurement, 0, -1.0, false)

		if strings.Contains(filterSpec, "adeclick=") {
			t.Errorf("buildLoudnormFilterSpec() emitted disabled adeclick\nfilterSpec: %s", filterSpec)
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
	controlSpec := buildLoudnormFilterSpec(base, measurement, 0, -1.0, false)

	withUnrelatedFilterFields := *base
	withUnrelatedFilterFields.FilterOrder = []FilterID{FilterAnalysis}
	withUnrelatedFilterFields.DS201LPFreq = 12000
	withUnrelatedFilterFields.DS201GateRatio = 4.0
	withUnrelatedFilterFields.LA2AThreshold = -30.0

	gotSpec := buildLoudnormFilterSpec(&withUnrelatedFilterFields, measurement, 0, -1.0, false)
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
			config.LoudnormTargetI = effectiveTargetI
			measurement := &LoudnormMeasurement{
				InputI:       tt.measuredI,
				InputTP:      tt.measuredTP,
				InputLRA:     8.0,
				InputThresh:  tt.measuredI - 10.0,
				TargetOffset: -2.5,
			}

			// Pre-compute values (caller's responsibility after Task 2.2)
			bCeiling, bNeeded, bClamped := calculateLimiterCeiling(
				tt.measuredI, tt.measuredTP, config.LoudnormTargetI, config.LoudnormTargetTP,
			)
			preGainDB, bReDerived := calculatePreGain(
				tt.measuredI, config.LoudnormTargetI, config.LoudnormTargetTP,
			)
			if bClamped {
				bCeiling = bReDerived
			}

			filterSpec := buildLoudnormFilterSpec(config, measurement, preGainDB, bCeiling, bNeeded)
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
