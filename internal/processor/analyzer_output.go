// Package processor handles audio analysis and processing
package processor

import (
	"fmt"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// regionMeasurements holds the common measurement results from analysing an
// output audio region. Both silence and speech region measurement functions
// share this intermediate type before mapping to their specific candidate types.
type regionMeasurements struct {
	RMSLevel        float64
	PeakLevel       float64
	CrestFactor     float64
	Spectral        SpectralMetrics
	MomentaryLUFS   float64
	ShortTermLUFS   float64
	TruePeak        float64
	SamplePeak      float64
	FramesProcessed int64
}

// MeasureOutputSilenceRegion analyses the elected silence region in the output file
// to capture comprehensive metrics for before/after comparison and adaptive tuning.
//
// The region parameter should use the same Start/Duration as the NoiseProfile
// from Pass 1 analysis. Returns nil if the region cannot be measured.
//
// Returns full SilenceCandidateMetrics with all amplitude, spectral, and loudness measurements.
func MeasureOutputSilenceRegion(outputPath string, region SilenceRegion) (*SilenceCandidateMetrics, error) {
	// Open the processed audio file
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open output file: %w", err)
	}
	defer reader.Close()

	return measureOutputSilenceRegionFromReader(reader, region)
}

// measureOutputRegionFromReader measures amplitude, spectral, and loudness
// metrics for a time region in an already-opened audio file. This is the
// shared implementation behind measureOutputSilenceRegionFromReader and
// measureOutputSpeechRegionFromReader.
func measureOutputRegionFromReader(reader *audio.Reader, start, duration time.Duration) (*regionMeasurements, error) {
	if start < 0 {
		return nil, fmt.Errorf("invalid region: negative start time")
	}
	if duration <= 0 {
		return nil, fmt.Errorf("invalid region: non-positive duration")
	}

	filterSpec := fmt.Sprintf(
		"atrim=start=%f:duration=%f,asetpts=PTS-STARTPTS,astats=metadata=1:measure_perchannel=0,aspectralstats=measure=all,ebur128=metadata=1:peak=sample+true",
		start.Seconds(),
		duration.Seconds(),
	)

	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(reader.GetDecoderContext(), filterSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to create analysis filter graph: %w", err)
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	var rmsLevel float64
	var peakLevel float64
	var crestFactor float64
	var momentaryLUFS float64
	var shortTermLUFS float64
	var truePeak float64
	var samplePeak float64
	var rmsLevelFound bool
	var framesProcessed int64

	var spectralAcc SpectralMetrics
	var spectralFrameCount int64

	extractMeasurements := func(_ *ffmpeg.AVFrame, filteredFrame *ffmpeg.AVFrame) (FrameAction, error) {
		if metadata := filteredFrame.Metadata(); metadata != nil {
			if value, ok := getFloatMetadata(metadata, metaKeyOverallRMSLevel); ok {
				rmsLevel = value
				rmsLevelFound = true
			}
			if value, ok := getFloatMetadata(metadata, metaKeyOverallPeakLevel); ok {
				peakLevel = value
			}
			if value, ok := getFloatMetadata(metadata, metaKeyOverallCrestFactor); ok {
				crestFactor = value
			}

			sm := extractSpectralMetrics(metadata)
			if sm.Found {
				spectralAcc.add(sm)
				spectralFrameCount++
			}

			if value, ok := getFloatMetadata(metadata, metaKeyEbur128M); ok {
				momentaryLUFS = value
			}
			if value, ok := getFloatMetadata(metadata, metaKeyEbur128S); ok {
				shortTermLUFS = value
			}
			if value, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
				truePeak = value
			}
			if value, ok := getFloatMetadata(metadata, metaKeyEbur128SamplePeak); ok {
				samplePeak = value
			}
		}

		framesProcessed++
		return FrameDiscard, nil
	}

	lenientHandler := func(err error) error { return nil }
	_ = runFilterGraph(reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnPushError: lenientHandler,
		OnPullError: lenientHandler,
		OnFrame:     extractMeasurements,
	})

	if framesProcessed == 0 {
		return nil, fmt.Errorf("no frames processed in region")
	}

	var avg SpectralMetrics
	if spectralFrameCount > 0 {
		avg = spectralAcc.average(float64(spectralFrameCount))
	}

	debugLog("  Frames processed: %d", framesProcessed)
	debugLog("  Spectral frames: %d", spectralFrameCount)
	debugLog("  Final ebur128 values:")
	debugLog("    momentaryLUFS: %f", momentaryLUFS)
	debugLog("    shortTermLUFS: %f", shortTermLUFS)
	debugLog("    truePeak: %f", truePeak)
	debugLog("    samplePeak: %f", samplePeak)
	debugLog("  Final astats values:")
	debugLog("    rmsLevel: %f (found: %v)", rmsLevel, rmsLevelFound)
	debugLog("    peakLevel: %f", peakLevel)
	debugLog("  Averaged spectral values:")
	debugLog("    spectralCentroid: %f", avg.Centroid)
	debugLog("    spectralRolloff: %f", avg.Rolloff)

	ebur128Valid := momentaryLUFS != 0.0 || shortTermLUFS != 0.0 || truePeak != 0.0
	if !ebur128Valid {
		debugLog("Warning: ebur128 measurements not captured (insufficient duration or warmup time)")
	}

	if crestFactor == 0.0 && rmsLevelFound && peakLevel != 0 {
		crestFactor = peakLevel - rmsLevel
	}

	result := &regionMeasurements{
		RMSLevel:        rmsLevel,
		PeakLevel:       peakLevel,
		CrestFactor:     crestFactor,
		Spectral:        avg,
		MomentaryLUFS:   momentaryLUFS,
		ShortTermLUFS:   shortTermLUFS,
		TruePeak:        linearRatioToDB(truePeak),
		SamplePeak:      linearRatioToDB(samplePeak),
		FramesProcessed: framesProcessed,
	}

	if !rmsLevelFound {
		result.RMSLevel = -60.0 // Conservative fallback
	}

	return result, nil
}

// measureOutputSilenceRegionFromReader measures a silence region and maps
// the result to SilenceCandidateMetrics.
func measureOutputSilenceRegionFromReader(reader *audio.Reader, region SilenceRegion) (*SilenceCandidateMetrics, error) {
	debugLog("=== MeasureOutputSilenceRegion: start=%.3fs, duration=%.3fs ===",
		region.Start.Seconds(), region.Duration.Seconds())

	result, err := measureOutputRegionFromReader(reader, region.Start, region.Duration)
	if err != nil {
		return nil, err
	}

	debugLog("=== MeasureOutputSilenceRegion SUMMARY ===")

	return &SilenceCandidateMetrics{
		Region:        region,
		RMSLevel:      result.RMSLevel,
		PeakLevel:     result.PeakLevel,
		CrestFactor:   result.CrestFactor,
		Spectral:      result.Spectral,
		MomentaryLUFS: result.MomentaryLUFS,
		ShortTermLUFS: result.ShortTermLUFS,
		TruePeak:      result.TruePeak,
		SamplePeak:    result.SamplePeak,
	}, nil
}

// MeasureOutputRegions measures both silence and speech regions from the same
// output file in a single open/close cycle. This avoids redundant file opens,
// demuxing, and decoding that would occur when calling MeasureOutputSilenceRegion
// and MeasureOutputSpeechRegion independently.
//
// Either region parameter may be nil to skip that measurement. Returns nil for
// any skipped or failed measurement (non-fatal - matches existing behaviour).
func MeasureOutputRegions(outputPath string, silenceRegion *SilenceRegion, speechRegion *SpeechRegion) (*SilenceCandidateMetrics, *SpeechCandidateMetrics) {
	if silenceRegion == nil && speechRegion == nil {
		return nil, nil
	}

	// Open the output file once for both measurements
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		debugLog("Warning: Failed to open output file for region measurements: %v", err)
		return nil, nil
	}
	defer reader.Close()

	// Measure silence region first (if requested)
	var silenceMetrics *SilenceCandidateMetrics
	if silenceRegion != nil {
		silenceMetrics, err = measureOutputSilenceRegionFromReader(reader, *silenceRegion)
		if err != nil {
			debugLog("Warning: Failed to measure silence region: %v", err)
			// Non-fatal - continue to speech measurement
		}
	}

	// Seek back to the beginning before measuring the speech region
	if speechRegion != nil {
		if silenceRegion != nil {
			// Only need to seek if we already read through the file for silence
			if err := reader.SeekTo(0); err != nil {
				debugLog("Warning: Failed to seek for speech region measurement: %v", err)
				return silenceMetrics, nil
			}
		}

		speechMetrics, err := measureOutputSpeechRegionFromReader(reader, *speechRegion)
		if err != nil {
			debugLog("Warning: Failed to measure speech region: %v", err)
			return silenceMetrics, nil
		}
		return silenceMetrics, speechMetrics
	}

	return silenceMetrics, nil
}

// MeasureOutputSpeechRegion analyses a speech region in the output file
// to capture comprehensive metrics for adaptive filter tuning and validation.
//
// The region parameter should identify a representative speech section from
// the processed audio. Returns nil if the region cannot be measured.
//
// Returns full SpeechCandidateMetrics with all amplitude, spectral, and loudness measurements.
func MeasureOutputSpeechRegion(outputPath string, region SpeechRegion) (*SpeechCandidateMetrics, error) {
	// Open the processed audio file
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open output file: %w", err)
	}
	defer reader.Close()

	return measureOutputSpeechRegionFromReader(reader, region)
}

// measureOutputSpeechRegionFromReader measures a speech region and maps
// the result to SpeechCandidateMetrics.
func measureOutputSpeechRegionFromReader(reader *audio.Reader, region SpeechRegion) (*SpeechCandidateMetrics, error) {
	debugLog("=== MeasureOutputSpeechRegion: start=%.3fs, duration=%.3fs ===",
		region.Start.Seconds(), region.Duration.Seconds())

	result, err := measureOutputRegionFromReader(reader, region.Start, region.Duration)
	if err != nil {
		return nil, err
	}

	debugLog("=== MeasureOutputSpeechRegion SUMMARY ===")

	return &SpeechCandidateMetrics{
		Region:        region,
		RMSLevel:      result.RMSLevel,
		PeakLevel:     result.PeakLevel,
		CrestFactor:   result.CrestFactor,
		Spectral:      result.Spectral,
		MomentaryLUFS: result.MomentaryLUFS,
		ShortTermLUFS: result.ShortTermLUFS,
		TruePeak:      result.TruePeak,
		SamplePeak:    result.SamplePeak,
	}, nil
}
