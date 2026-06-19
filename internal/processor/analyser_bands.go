// Package processor handles audio analysis and processing
package processor

import (
	"context"
	"fmt"
	"time"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// Speech-region band edges (Hz) for the de-esser engagement signal.
//
//   - Body band (1-3 kHz) captures vocal presence/fundamentals-plus-formants.
//   - Sibilant band (6-9 kHz) captures "s"/"sh" energy.
//
// The de-esser engages on the band excess (sibilant RMS - body RMS); see
// adaptive_deesser.go.
const (
	bandBodyLowHz  = 1000.0
	bandBodyHighHz = 3000.0
	bandSibLowHz   = 6000.0
	bandSibHighHz  = 9000.0
)

// speechBandAnalysisFilterFormat is the fmt.Sprintf format string for a
// band-scoped speech-region RMS measurement. The first two %f verbs take the
// region start and duration in seconds; the next two take the band low/high
// edges in Hz. The signal is downmixed to mono, trimmed to the region,
// band-limited with 2-pole Butterworth highpass+lowpass, then measured with
// astats (Overall RMS via measure_perchannel=0).
const speechBandAnalysisFilterFormat = "aformat=channel_layouts=mono,atrim=start=%f:duration=%f,asetpts=PTS-STARTPTS,highpass=f=%f:p=2,lowpass=f=%f:p=2,astats=metadata=1:measure_perchannel=0"

// measureSpeechBandRMS measures the Overall RMS level (dBFS) of one frequency
// band over a region of an already-opened audio file. It mirrors the
// single-source/single-sink region measurement used by analyser_output.go,
// band-limiting the downmixed signal before astats. Returns the RMS in dBFS and
// ok=false when no RMS metadata was captured (e.g. region shorter than astats
// warmup).
//
// log sinks the non-fatal region-seek warning.
func measureSpeechBandRMS(ctx context.Context, reader *audio.Reader, start, duration time.Duration, lowHz, highHz float64, log debugLogger) (float64, bool, error) {
	if start < 0 {
		return 0, false, fmt.Errorf("invalid region: negative start time")
	}
	if duration <= 0 {
		return 0, false, fmt.Errorf("invalid region: non-positive duration")
	}

	filterSpec := fmt.Sprintf(
		speechBandAnalysisFilterFormat,
		start.Seconds(),
		duration.Seconds(),
		lowHz,
		highHz,
	)

	// Skip the pre-region span: seek the demuxer near the region before decoding
	// rather than decoding from frame 0 and letting atrim discard everything
	// ahead of start. The atrim window stays region-absolute, so the measured
	// span is unchanged (see regionSeekPreRoll).
	seekReaderBeforeRegion(reader, start, log)

	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(reader.GetDecoderContext(), filterSpec)
	if err != nil {
		return 0, false, fmt.Errorf("failed to create band analysis filter graph: %w", err)
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	var rmsLevel float64
	var rmsLevelFound bool

	extract := func(_ *ffmpeg.AVFrame, filteredFrame *ffmpeg.AVFrame) error {
		if metadata := filteredFrame.Metadata(); metadata != nil {
			if value, ok := getFloatMetadata(metadata, metaKeyOverallRMSLevel); ok {
				rmsLevel = value
				rmsLevelFound = true
			}
		}
		return nil
	}

	lenientHandler := func(error) error { return nil }
	if err := runFilterGraph(ctx, reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnPushError: lenientHandler,
		OnPullError: lenientHandler,
		OnFrame:     extract,
	}); err != nil {
		return 0, false, err
	}

	return rmsLevel, rmsLevelFound, nil
}

// speechBandPlan names the two speech-region bands measured in parallel. The
// fixed two-element order keeps the fan-out result-slot writes deterministic.
var speechBandPlan = [2]struct {
	lowHz, highHz float64
}{
	{bandBodyLowHz, bandBodyHighHz},
	{bandSibLowHz, bandSibHighHz},
}

// measureSpeechBands measures body-band and sibilant-band RMS over the elected
// speech region and writes them onto the SpeechProfile. It is a no-op when no
// SpeechProfile was elected. Failures are non-fatal: the band fields stay at
// their zero value and the de-esser falls back to OFF (see adaptive_deesser.go).
//
// The two bands run as bounded goroutines (runBandMeasurements): each opens its
// own audio.Reader and runs an independent filter graph, so they share no mutable
// state and write only their own result slot. The per-band graph and astats math
// are unchanged, so the measured RMS values are bit-identical to the former serial
// path. report (when non-nil) advances the post-loop progress span.
func measureSpeechBands(ctx context.Context, filename string, measurements *AudioMeasurements, report bandProgressReporter, log debugLogger) {
	if measurements == nil || measurements.Regions.SpeechProfile == nil {
		drainBandProgress(report, len(speechBandPlan))
		return
	}

	region := measurements.Regions.SpeechProfile.Region
	if region.Duration <= 0 {
		drainBandProgress(report, len(speechBandPlan))
		return
	}

	var results [len(speechBandPlan)]struct {
		rms float64
		ok  bool
	}

	runBandMeasurements(ctx, len(speechBandPlan), report, func(i int) {
		reader, _, err := audio.OpenAudioFile(filename)
		if err != nil {
			log.Logf("Warning: failed to open file for speech band %d measurement: %v", i, err)
			return
		}
		defer reader.Close()

		band := speechBandPlan[i]
		rms, ok, err := measureSpeechBandRMS(ctx, reader, region.Start, region.Duration, band.lowHz, band.highHz, log)
		if err != nil {
			log.Logf("Warning: speech band %d RMS measurement failed: %v", i, err)
			return
		}
		results[i].rms = rms
		results[i].ok = ok
	})

	body, bodyOK := results[0].rms, results[0].ok
	sib, sibOK := results[1].rms, results[1].ok
	if bodyOK {
		measurements.Regions.SpeechProfile.BodyBandRMS = body
	}
	if sibOK {
		measurements.Regions.SpeechProfile.SibBandRMS = sib
	}

	// Only treat the band excess as valid when both bands measured; a partial
	// or absent measurement otherwise reads as a spurious 0 dB excess and
	// engages the de-esser at the cap (see adaptive_deesser.go).
	measurements.Regions.SpeechProfile.BandsMeasured = bodyOK && sibOK

	log.Logf("Speech band RMS: body=%.1f dBFS (found=%v), sib=%.1f dBFS (found=%v), excess=%.1f dB, measured=%v",
		body, bodyOK, sib, sibOK, sib-body, measurements.Regions.SpeechProfile.BandsMeasured)
}

// drainBandProgress fires report n times so an early-return band function still
// advances the post-loop progress span by its full band budget; otherwise the
// phase would never reach 1.0 from the band side. No-op when report is nil.
func drainBandProgress(report bandProgressReporter, n int) {
	if report == nil {
		return
	}
	for range n {
		report()
	}
}
