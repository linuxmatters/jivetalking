// Package processor handles audio analysis and processing
package processor

import (
	"context"
	"math"

	"github.com/linuxmatters/jivetalking/internal/audio"
)

// afftdnBandCentresHz are the 15 FIXED band centre frequencies (Hz) afftdn uses
// for its custom noise profile (nt=custom:bn=...), verified against the ffmpeg
// 8.1 af_afftdn.c source. The emitted bn carries one dB value per band,
// positional and in this exact order.
var afftdnBandCentresHz = []float64{
	80, 125, 195, 290, 440, 660, 1000, 1500, 2250, 3350, 5000, 7500, 11200, 16000, 24000,
}

// afftdnMinFiniteBands is the minimum count of finite per-band RMS measurements
// (out of the 15 bands) for the custom afftdn profile to count as measured. Set
// to 10 so the normal case of the top one or two bands being unmeasurable (above
// the 20.5 kHz band-limit / 48 kHz Nyquist) still passes, while a broad
// measurement failure (most bands non-finite) falls back to the white profile.
const afftdnMinFiniteBands = 10

// afftdnBandEdgesHz returns the [low, high] measurement edges for one afftdn
// band. Interior edges sit at the geometric midpoint between adjacent centres
// (the perceptually natural split on a log frequency axis). Band 0's low edge
// drops one geometric step below its centre, and the last band's high edge rises
// one geometric step above its centre, so the outermost bands keep a sensible
// width rather than an open-ended edge.
func afftdnBandEdgesHz(index int) (lowHz, highHz float64) {
	centres := afftdnBandCentresHz
	last := len(centres) - 1

	if index <= 0 {
		ratio := centres[1] / centres[0]
		lowHz = centres[0] / math.Sqrt(ratio)
	} else {
		lowHz = math.Sqrt(centres[index-1] * centres[index])
	}

	if index >= last {
		ratio := centres[last] / centres[last-1]
		highHz = centres[last] * math.Sqrt(ratio)
	} else {
		highHz = math.Sqrt(centres[index] * centres[index+1])
	}

	return lowHz, highHz
}

// measureNoiseBands measures the per-band RMS (dBFS) over the elected room-tone
// region and writes them onto the NoiseProfile. It is a no-op when no
// NoiseProfile was elected or the region is empty. Failures are non-fatal:
// BandNoise stays nil and BandsMeasured stays false, so tuneNoiseReduction keeps
// the white-noise afftdn path.
//
// The 15 bands run as bounded goroutines (runBandMeasurements): each opens its
// own audio.Reader and runs an independent filter graph, band-limiting the
// downmixed region before astats, and writes only its own bands[i] slot. The
// per-band graph and astats math are unchanged, so the measured RMS values are
// bit-identical to the former serial path. report (when non-nil) advances the
// post-loop progress span.
func measureNoiseBands(ctx context.Context, filename string, measurements *AudioMeasurements, report bandProgressReporter, log debugLogger) {
	if measurements == nil || measurements.Regions.NoiseProfile == nil {
		drainBandProgress(report, len(afftdnBandCentresHz))
		return
	}

	profile := measurements.Regions.NoiseProfile
	if profile.Duration <= 0 {
		drainBandProgress(report, len(afftdnBandCentresHz))
		return
	}

	bands := make([]float64, len(afftdnBandCentresHz))
	measured := make([]bool, len(afftdnBandCentresHz))

	runBandMeasurements(ctx, len(afftdnBandCentresHz), report, func(i int) {
		reader, _, err := audio.OpenAudioFile(filename)
		if err != nil {
			log.Logf("Warning: failed to open file for noise band %d measurement: %v", i, err)
			return
		}
		defer reader.Close()

		lowHz, highHz := afftdnBandEdgesHz(i)
		rms, ok, err := measureSpeechBandRMS(ctx, reader, profile.Start, profile.Duration, lowHz, highHz, log)
		if err != nil {
			log.Logf("Warning: noise band %d RMS measurement failed: %v", i, err)
			return
		}
		bands[i] = rms
		measured[i] = ok
	})

	finite := 0
	for i := range bands {
		// A band with astats RMS reported counts as measured when its value is
		// finite. A legitimately silent band (very low RMS, e.g. -120 dBFS floor)
		// is finite and counts; only NaN/Inf is excluded. The top band (centre
		// 24000 Hz) sits above the 20.5 kHz band-limit and at or above Nyquist for
		// 48 kHz audio, so it reports a non-finite RMS as a matter of course.
		if measured[i] && isFinite(bands[i]) {
			finite++
		}
	}

	profile.BandNoise = bands
	// Require at least afftdnMinFiniteBands of the 15 bands to be finite for the
	// measurement to count. The top one or two bands being unmeasurable (above the
	// band-limit / Nyquist) is the normal case and must still count as measured, so
	// the threshold sits well below 15. If essentially all bands are non-finite the
	// measurement broadly failed and the caller falls back to the white profile.
	profile.BandsMeasured = finite >= afftdnMinFiniteBands

	log.Logf("Noise band RMS: %v dBFS, finite=%d/%d, measured=%v", bands, finite, len(bands), profile.BandsMeasured)
}
