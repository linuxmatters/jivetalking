package processor

import (
	"strings"
	"testing"
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

		spec := buildLoudnormFilterSpec(config, measurement, measurement.TargetOffset, limiterPlan{ceilingDB: -1.0}, 48000, "")
		assertBothFlags(t, "buildLoudnormFilterSpec()", spec)
	})

	t.Run("outputRegionAnalysisFilterFormat", func(t *testing.T) {
		assertBothFlags(t, "outputRegionAnalysisFilterFormat", outputRegionAnalysisFilterFormat)
	})
}
