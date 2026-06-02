package processor

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// TestMetadataModeGuard asserts that all three production analysis filter
// builders emit both astats=metadata=1 and ebur128=metadata=1. The spike that
// derived the loudnorm-capture metadata flags depends on these flags being
// present; this guard fails if any builder regresses to metadata=0.
//
// Each subtest asserts against live builder output (or the live source
// constant), never a re-typed copy of the filter string.
func TestMetadataModeGuard(t *testing.T) {
	const (
		astatsMetadata  = "astats=metadata=1"
		ebur128Metadata = "ebur128=metadata=1"
	)

	assertBothFlags := func(t *testing.T, builder, spec string) {
		t.Helper()
		if !strings.Contains(spec, astatsMetadata) {
			t.Errorf("%s missing %q\nspec: %s", builder, astatsMetadata, spec)
		}
		if !strings.Contains(spec, ebur128Metadata) {
			t.Errorf("%s missing %q\nspec: %s", builder, ebur128Metadata, spec)
		}
	}

	t.Run("buildAnalysisFilter", func(t *testing.T) {
		config := newTestConfig()
		config.Analysis.Enabled = true

		assertBothFlags(t, "buildAnalysisFilter()", config.buildAnalysisFilter())
	})

	t.Run("buildLoudnormFilterSpec", func(t *testing.T) {
		config := newTestConfig()
		measurement := &LoudnormMeasurement{
			InputI:       -24.0,
			InputTP:      -5.0,
			InputLRA:     6.0,
			InputThresh:  -34.0,
			TargetOffset: -0.5,
		}

		spec := buildLoudnormFilterSpec(config, measurement, 0, -1.0, false, 48000)
		assertBothFlags(t, "buildLoudnormFilterSpec()", spec)
	})

	t.Run("outputRegionAnalysisFilterFormat", func(t *testing.T) {
		assertBothFlags(t, "outputRegionAnalysisFilterFormat", outputRegionAnalysisFilterFormat)
	})
}

// TestLoudnormCaptureExcludesConcurrentGraphFree proves the loudnorm capture
// bracket holds graphFreeMu such that a concurrent freeFilterGraphLocked cannot
// run its graph free until the capture releases the lock.
//
// Goroutine A drives captureLoudnormGraphFinalisation with the
// loudnormAVFilterGraphFree seam swapped for a stub that signals it is inside
// the free (holding graphFreeMu) then blocks until released. Goroutine B calls
// freeFilterGraphLocked, which must block on graphFreeMu.Lock() until A's
// stopLoudnormCapture releases it. B uses ffmpeg.AVFilterGraphFree directly (not
// the seam) on a nil graph pointer; avfilter_graph_free on a pointer-to-NULL is
// a no-op, mirroring the existing nil-graph capture tests.
//
// Ordering is proven with synchronisation primitives, not sleeps: A flips
// aReleased only as it leaves the held free, and B records whether aReleased was
// already set the instant its free returns. If exclusion holds, B always
// observes the release happened first. A guard timeout fails the test rather
// than hanging the suite if exclusion is broken.
func TestLoudnormCaptureExcludesConcurrentGraphFree(t *testing.T) {
	const deadline = 5 * time.Second

	aInFree := make(chan struct{})  // A is inside the held free, holding graphFreeMu
	releaseA := make(chan struct{}) // test lets A finish the held free
	var aReleased atomic.Bool       // set just before A leaves the held free

	// Swap the loudnorm free seam for A. It feeds valid JSON so Stop() parses,
	// signals it holds the lock, then blocks until the test releases it.
	oldFree := loudnormAVFilterGraphFree
	loudnormAVFilterGraphFree = func(graph **ffmpeg.AVFilterGraph) {
		loudnormLogCallback(nil, ffmpeg.AVLogInfo, loudnormCaptureTestJSON)
		close(aInFree)
		<-releaseA
		aReleased.Store(true)
		if graph != nil && *graph != nil {
			oldFree(graph)
		}
	}
	defer func() { loudnormAVFilterGraphFree = oldFree }()

	aDone := make(chan error, 1)
	go func() {
		var graph *ffmpeg.AVFilterGraph
		_, err := captureLoudnormGraphFinalisation(&graph)
		aDone <- err
	}()

	// Wait until A holds graphFreeMu inside the free.
	select {
	case <-aInFree:
	case <-time.After(deadline):
		t.Fatal("goroutine A never entered the held free; capture bracket may not hold graphFreeMu")
	}

	bStarted := make(chan struct{})
	bDone := make(chan bool, 1) // value: whether aReleased was already set when B's free returned
	go func() {
		close(bStarted)
		var nilGraph *ffmpeg.AVFilterGraph
		freeFilterGraphLocked(&nilGraph) // blocks on graphFreeMu until A releases
		bDone <- aReleased.Load()
	}()

	// B has launched and is blocking (or about to block) on graphFreeMu.Lock().
	<-bStarted

	// Exclusion check: while A still holds the lock, B must not have completed.
	select {
	case <-bDone:
		t.Fatal("goroutine B completed its graph free while A held graphFreeMu; exclusion broken")
	default:
	}

	// Release A. Its stopLoudnormCapture unlocks graphFreeMu, letting B proceed.
	close(releaseA)

	select {
	case err := <-aDone:
		if err != nil {
			t.Fatalf("captureLoudnormGraphFinalisation() error = %v", err)
		}
	case <-time.After(deadline):
		t.Fatal("goroutine A never finished its capture after release")
	}

	select {
	case sawReleaseFirst := <-bDone:
		if !sawReleaseFirst {
			t.Fatal("goroutine B observed its free completing before A released graphFreeMu; exclusion broken")
		}
	case <-time.After(deadline):
		t.Fatal("goroutine B never acquired graphFreeMu after A released it")
	}
}
