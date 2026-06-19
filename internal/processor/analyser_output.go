// Package processor handles audio analysis and processing
package processor

import (
	"context"
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

// regionSeekPreRoll is the head-start the demuxer seeks before a region's start
// so decoding skips the pre-region span instead of running from frame 0.
//
// Why this never shifts the measured values: atrim is the FIRST filter in the
// graph and keys off each frame's file-absolute PTS (ReadFrame sets the frame
// PTS from the demuxer's best-effort timestamp; the abuffer source uses the
// stream packet time base). Seeking changes where DECODING begins, not the PTS
// the frames carry, so atrim=start=region.Start selects the exact same absolute
// samples regardless of the seek point. astats, aspectralstats, and ebur128 all
// sit after atrim, so they only ever see the windowed frames - the pre-roll span
// is discarded before it reaches any measurement filter. The measured window is
// therefore byte-identical to the from-frame-0 path for any seek at or before
// region.Start.
//
// The pre-roll's only job is to guarantee the seek lands at or before
// region.Start. AVFormatSeekFile (flags=0) seeks BACKWARD to a keyframe at or
// before the requested timestamp, so the effective decode start is already <=
// the seek target; the pre-roll adds further slack. 5s comfortably exceeds
// ebur128's longest integration window (3s short-term, 400ms momentary) so even
// if a future change moved a measurement filter ahead of atrim, the warm-up
// would still be covered. Decoder/filter warm-up before atrim is free here:
// those frames are trimmed away.
const regionSeekPreRoll = 5 * time.Second

// seekReaderBeforeRegion seeks the demuxer to regionStart-regionSeekPreRoll
// (floored at 0) so the pre-region span is skipped before the atrim window is
// decoded. The atrim start stays region-absolute and unchanged; see
// regionSeekPreRoll for why this preserves byte-identical measurements. A seek
// failure is non-fatal - decoding simply continues from the current position,
// and atrim still selects the correct window.
func seekReaderBeforeRegion(reader *audio.Reader, regionStart time.Duration, log debugLogger) {
	seekTarget := max(regionStart-regionSeekPreRoll, 0)
	seekTS := seekTarget.Microseconds() // AV_TIME_BASE is microseconds
	if err := reader.SeekTo(seekTS); err != nil {
		log.Logf("Warning: failed to seek before region (start=%.3fs, target=%.3fs): %v; decoding from current position",
			regionStart.Seconds(), seekTarget.Seconds(), err)
	}
}

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

// toRegionSample maps the measured region metrics to a bare RegionSample
// (amplitude/spectral/loudness only). FramesProcessed is a measurement-internal
// counter and is not carried onto the sample. Both output region wrappers share
// this so the eight-field copy lives in one place.
func (r *regionMeasurements) toRegionSample() *RegionSample {
	return &RegionSample{
		RMSLevel:      r.RMSLevel,
		PeakLevel:     r.PeakLevel,
		CrestFactor:   r.CrestFactor,
		Spectral:      r.Spectral,
		MomentaryLUFS: r.MomentaryLUFS,
		ShortTermLUFS: r.ShortTermLUFS,
		TruePeak:      r.TruePeak,
		SamplePeak:    r.SamplePeak,
	}
}

// measureOutputRegionFromReader measures amplitude, spectral, and loudness
// metrics for a time region in an already-opened audio file. This is the
// shared implementation behind measureOutputRoomToneRegionFromReader and
// measureOutputSpeechRegionFromReader.
func measureOutputRegionFromReader(ctx context.Context, reader *audio.Reader, start, duration time.Duration, log debugLogger) (*regionMeasurements, error) {
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

	// Skip the pre-region span: seek the demuxer near the region before decoding
	// rather than decoding from frame 0 and letting atrim discard everything
	// ahead of start. The atrim window stays region-absolute, so the measured
	// span is unchanged (see regionSeekPreRoll).
	seekReaderBeforeRegion(reader, start, log)

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
	_ = runFilterGraph(ctx, reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
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
// the result to a bare RegionSample (amplitude/spectral/loudness only). Output
// re-measure never scores or elects, so no candidate scoring/band
// fields are produced.
func measureOutputRoomToneRegionFromReader(ctx context.Context, reader *audio.Reader, region RoomToneRegion, log debugLogger) (*RegionSample, error) {
	log.Logf("=== measureOutputRoomToneRegion: start=%.3fs, duration=%.3fs ===",
		region.Start.Seconds(), region.Duration.Seconds())

	result, err := measureOutputRegionFromReader(ctx, reader, region.Start, region.Duration, log)
	if err != nil {
		return nil, err
	}

	log.Logf("=== measureOutputRoomToneRegion SUMMARY ===")

	return result.toRegionSample(), nil
}

// extractRegionPair builds optional RoomToneRegion and SpeechRegion pointers
// from AudioMeasurements profiles. Returns (nil, nil) when both profiles are absent.
func extractRegionPair(m *AudioMeasurements) (*RoomToneRegion, *SpeechRegion) {
	var roomToneRegion *RoomToneRegion
	var spRegion *SpeechRegion
	if m.Regions.NoiseProfile != nil {
		roomToneRegion = &RoomToneRegion{
			Start:    m.Regions.NoiseProfile.Start,
			End:      m.Regions.NoiseProfile.Start + m.Regions.NoiseProfile.Duration,
			Duration: m.Regions.NoiseProfile.Duration,
		}
	}
	if m.Regions.SpeechProfile != nil {
		spRegion = &SpeechRegion{
			Start:    m.Regions.SpeechProfile.Region.Start,
			End:      m.Regions.SpeechProfile.Region.End,
			Duration: m.Regions.SpeechProfile.Region.Duration,
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
func MeasureOutputRegions(ctx context.Context, outputPath string, roomToneRegion *RoomToneRegion, speechRegion *SpeechRegion, log debugLogger) (*RegionSample, *RegionSample) {
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
	var roomToneMetrics *RegionSample
	if roomToneRegion != nil {
		roomToneMetrics, err = measureOutputRoomToneRegionFromReader(ctx, reader, *roomToneRegion, log)
		if err != nil {
			log.Logf("Warning: Failed to measure room tone region: %v", err)
			// Non-fatal - continue to speech measurement
		}
	}

	// Measure the speech region. No explicit seek-back is needed here:
	// measureOutputRegionFromReader seeks the demuxer near the region start
	// itself (seekReaderBeforeRegion), which repositions the reader regardless
	// of where the room-tone pass left it.
	if speechRegion != nil {
		speechMetrics, err := measureOutputSpeechRegionFromReader(ctx, reader, *speechRegion, log)
		if err != nil {
			log.Logf("Warning: Failed to measure speech region: %v", err)
			return roomToneMetrics, nil
		}
		return roomToneMetrics, speechMetrics
	}

	return roomToneMetrics, nil
}

// measureOutputSpeechRegionFromReader measures a speech region and maps
// the result to a bare RegionSample (amplitude/spectral/loudness only). Output
// re-measure never scores or elects, so no candidate scoring/band fields are
// produced.
func measureOutputSpeechRegionFromReader(ctx context.Context, reader *audio.Reader, region SpeechRegion, log debugLogger) (*RegionSample, error) {
	log.Logf("=== measureOutputSpeechRegion: start=%.3fs, duration=%.3fs ===",
		region.Start.Seconds(), region.Duration.Seconds())

	result, err := measureOutputRegionFromReader(ctx, reader, region.Start, region.Duration, log)
	if err != nil {
		return nil, err
	}

	log.Logf("=== measureOutputSpeechRegion SUMMARY ===")

	return result.toRegionSample(), nil
}
