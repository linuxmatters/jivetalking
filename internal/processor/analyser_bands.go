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
func measureSpeechBandRMS(ctx context.Context, reader *audio.Reader, start, duration time.Duration, lowHz, highHz float64) (float64, bool, error) {
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

// measureSpeechBands measures body-band and sibilant-band RMS over the elected
// speech region and writes them onto the SpeechProfile. It is a no-op when no
// SpeechProfile was elected. Failures are non-fatal: the band fields stay at
// their zero value and the de-esser falls back to OFF (see adaptive_deesser.go).
// The input file is opened once and the reader is seeked between the two
// band measurements, mirroring MeasureOutputRegions' single-open pattern.
func measureSpeechBands(ctx context.Context, filename string, measurements *AudioMeasurements, log debugLogger) {
	if measurements == nil || measurements.Regions.SpeechProfile == nil {
		return
	}

	region := measurements.Regions.SpeechProfile.Region
	if region.Duration <= 0 {
		return
	}

	reader, _, err := audio.OpenAudioFile(filename)
	if err != nil {
		log.Logf("Warning: failed to open file for speech band measurement: %v", err)
		return
	}
	defer reader.Close()

	body, bodyOK, err := measureSpeechBandRMS(ctx, reader, region.Start, region.Duration, bandBodyLowHz, bandBodyHighHz)
	if err != nil {
		log.Logf("Warning: body-band RMS measurement failed: %v", err)
		return
	}
	if bodyOK {
		measurements.Regions.SpeechProfile.BodyBandRMS = body
	}

	if err := reader.SeekTo(0); err != nil {
		log.Logf("Warning: failed to seek for sibilant-band measurement: %v", err)
		return
	}

	sib, sibOK, err := measureSpeechBandRMS(ctx, reader, region.Start, region.Duration, bandSibLowHz, bandSibHighHz)
	if err != nil {
		log.Logf("Warning: sibilant-band RMS measurement failed: %v", err)
		return
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
