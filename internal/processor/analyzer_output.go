// Package processor handles audio analysis and processing
package processor

import (
	"fmt"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// outputRegionAnalysisFilterFormat is the fmt.Sprintf format string for the
// output-region analysis filter graph in measureOutputRegionFromReader. The
// %f verbs take the region start and duration in seconds. Hoisted to a
// package-level constant so guard tests can assert the metadata flags against
// live source without re-typing the filter string.
const outputRegionAnalysisFilterFormat = "atrim=start=%f:duration=%f,asetpts=PTS-STARTPTS,astats=metadata=1:measure_perchannel=0,aspectralstats=measure=all,ebur128=metadata=1:peak=sample+true"

// regionMeasurements holds the common measurement results from analysing an
// output audio region. Both room tone and speech region measurement functions
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

// measureOutputRegionFromReader measures amplitude, spectral, and loudness
// metrics for a time region in an already-opened audio file. This is the
// shared implementation behind measureOutputRoomToneRegionFromReader and
// measureOutputSpeechRegionFromReader.
func measureOutputRegionFromReader(reader *audio.Reader, start, duration time.Duration, log debugLogger) (*regionMeasurements, error) {
	if start < 0 {
		return nil, fmt.Errorf("invalid region: negative start time")
	}
	if duration <= 0 {
		return nil, fmt.Errorf("invalid region: non-positive duration")
	}

	filterSpec := fmt.Sprintf(
		outputRegionAnalysisFilterFormat,
		start.Seconds(),
		duration.Seconds(),
	)

	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(reader.GetDecoderContext(), filterSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to create analysis filter graph: %w", err)
	}
	defer freeFilterGraphLocked(&filterGraph)

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

	extractMeasurements := func(_ *ffmpeg.AVFrame, filteredFrame *ffmpeg.AVFrame) error {
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
		return nil
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

	log.Logf("  Frames processed: %d", framesProcessed)
	log.Logf("  Spectral frames: %d", spectralFrameCount)
	log.Logf("  Final ebur128 values:")
	log.Logf("    momentaryLUFS: %f", momentaryLUFS)
	log.Logf("    shortTermLUFS: %f", shortTermLUFS)
	log.Logf("    truePeak: %f", truePeak)
	log.Logf("    samplePeak: %f", samplePeak)
	log.Logf("  Final astats values:")
	log.Logf("    rmsLevel: %f (found: %v)", rmsLevel, rmsLevelFound)
	log.Logf("    peakLevel: %f", peakLevel)
	log.Logf("  Averaged spectral values:")
	log.Logf("    spectralCentroid: %f", avg.Centroid)
	log.Logf("    spectralRolloff: %f", avg.Rolloff)

	ebur128Valid := momentaryLUFS != 0.0 || shortTermLUFS != 0.0 || truePeak != 0.0
	if !ebur128Valid {
		log.Logf("Warning: ebur128 measurements not captured (insufficient duration or warmup time)")
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

// measureOutputRoomToneRegionFromReader measures a room tone region and maps
// the result to RoomToneCandidateMetrics.
func measureOutputRoomToneRegionFromReader(reader *audio.Reader, region RoomToneRegion, log debugLogger) (*RoomToneCandidateMetrics, error) {
	log.Logf("=== measureOutputRoomToneRegion: start=%.3fs, duration=%.3fs ===",
		region.Start.Seconds(), region.Duration.Seconds())

	result, err := measureOutputRegionFromReader(reader, region.Start, region.Duration, log)
	if err != nil {
		return nil, err
	}

	log.Logf("=== measureOutputRoomToneRegion SUMMARY ===")

	return &RoomToneCandidateMetrics{
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

// extractRegionPair builds optional RoomToneRegion and SpeechRegion pointers
// from AudioMeasurements profiles. Returns (nil, nil) when both profiles are absent.
func extractRegionPair(m *AudioMeasurements) (*RoomToneRegion, *SpeechRegion) {
	var roomToneRegion *RoomToneRegion
	var spRegion *SpeechRegion
	if m.NoiseProfile != nil {
		roomToneRegion = &RoomToneRegion{
			Start:    m.NoiseProfile.Start,
			End:      m.NoiseProfile.Start + m.NoiseProfile.Duration,
			Duration: m.NoiseProfile.Duration,
		}
	}
	if m.SpeechProfile != nil {
		spRegion = &SpeechRegion{
			Start:    m.SpeechProfile.Region.Start,
			End:      m.SpeechProfile.Region.End,
			Duration: m.SpeechProfile.Region.Duration,
		}
	}
	return roomToneRegion, spRegion
}

// MeasureOutputRegions measures both room tone and speech regions from the same
// output file in a single open/close cycle. This avoids redundant file opens,
// demuxing, and decoding that would occur if room tone and speech regions were
// measured in separate passes.
//
// Either region parameter may be nil to skip that measurement. Returns nil for
// any skipped or failed measurement (non-fatal - matches existing behaviour).
func MeasureOutputRegions(outputPath string, roomToneRegion *RoomToneRegion, speechRegion *SpeechRegion, log debugLogger) (*RoomToneCandidateMetrics, *SpeechCandidateMetrics) {
	if roomToneRegion == nil && speechRegion == nil {
		return nil, nil
	}

	// Open the output file once for both measurements
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		log.Logf("Warning: Failed to open output file for region measurements: %v", err)
		return nil, nil
	}
	defer reader.Close()

	// Measure room tone region first (if requested)
	var roomToneMetrics *RoomToneCandidateMetrics
	if roomToneRegion != nil {
		roomToneMetrics, err = measureOutputRoomToneRegionFromReader(reader, *roomToneRegion, log)
		if err != nil {
			log.Logf("Warning: Failed to measure room tone region: %v", err)
			// Non-fatal - continue to speech measurement
		}
	}

	// Seek back to the beginning before measuring the speech region
	if speechRegion != nil {
		if roomToneRegion != nil {
			// Only need to seek if we already read through the file for room tone
			if err := reader.SeekTo(0); err != nil {
				log.Logf("Warning: Failed to seek for speech region measurement: %v", err)
				return roomToneMetrics, nil
			}
		}

		speechMetrics, err := measureOutputSpeechRegionFromReader(reader, *speechRegion, log)
		if err != nil {
			log.Logf("Warning: Failed to measure speech region: %v", err)
			return roomToneMetrics, nil
		}
		return roomToneMetrics, speechMetrics
	}

	return roomToneMetrics, nil
}

// measureOutputSpeechRegionFromReader measures a speech region and maps
// the result to SpeechCandidateMetrics.
func measureOutputSpeechRegionFromReader(reader *audio.Reader, region SpeechRegion, log debugLogger) (*SpeechCandidateMetrics, error) {
	log.Logf("=== measureOutputSpeechRegion: start=%.3fs, duration=%.3fs ===",
		region.Start.Seconds(), region.Duration.Seconds())

	result, err := measureOutputRegionFromReader(reader, region.Start, region.Duration, log)
	if err != nil {
		return nil, err
	}

	log.Logf("=== measureOutputSpeechRegion SUMMARY ===")

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
