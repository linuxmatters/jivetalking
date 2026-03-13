package processor

import (
	"math"
	"strconv"
	"time"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// IntervalSample contains all measurements for a 250ms audio window.
// Captures comprehensive metrics from astats, aspectralstats, and ebur128 for
// silence detection, adaptive filter tuning, and post-hoc analysis.
type IntervalSample struct {
	Timestamp time.Duration `json:"timestamp"` // Start of this interval

	// ─── Amplitude metrics (calculated per-interval from raw samples) ───────────
	RMSLevel  float64 `json:"rms_level"`  // dBFS, RMS level calculated from raw frame samples
	PeakLevel float64 `json:"peak_level"` // dBFS, peak level (max tracked per interval)

	// ─── aspectralstats spectral metrics (valid per-window from FFmpeg) ─────────
	SpectralMean     float64 `json:"spectral_mean"`     // Average magnitude
	SpectralVariance float64 `json:"spectral_variance"` // Magnitude spread
	SpectralCentroid float64 `json:"spectral_centroid"` // Hz - "brightness", speech 300-3000 Hz
	SpectralSpread   float64 `json:"spectral_spread"`   // Hz - frequency bandwidth
	SpectralSkewness float64 `json:"spectral_skewness"` // Distribution asymmetry
	SpectralKurtosis float64 `json:"spectral_kurtosis"` // Distribution peakedness
	SpectralEntropy  float64 `json:"spectral_entropy"`  // 0-1 - speech has lower entropy than noise
	SpectralFlatness float64 `json:"spectral_flatness"` // 0-1 - high = noise-like, low = tonal
	SpectralCrest    float64 `json:"spectral_crest"`    // Spectral peakiness
	SpectralFlux     float64 `json:"spectral_flux"`     // Rate of spectral change (transitions)
	SpectralSlope    float64 `json:"spectral_slope"`    // High-frequency roll-off rate
	SpectralDecrease float64 `json:"spectral_decrease"` // High-frequency energy decay
	SpectralRolloff  float64 `json:"spectral_rolloff"`  // Hz - frequency below which 85% energy lies

	// ─── ebur128 loudness metrics (windowed measurements) ───────────────────────
	MomentaryLUFS float64 `json:"momentary_lufs"`  // LUFS - 400ms window loudness
	ShortTermLUFS float64 `json:"short_term_lufs"` // LUFS - 3s window loudness
	TruePeak      float64 `json:"true_peak"`       // dBTP - true peak level (max tracked)
	SamplePeak    float64 `json:"sample_peak"`     // dBFS - sample peak level (max tracked)
}

// intervalAccumulator holds accumulated values for a 250ms interval window.
// Values are aggregated appropriately: sums for averaging, min/max for extremes.
type intervalAccumulator struct {
	frameCount int // Number of frames in this interval

	// ─── Raw sample RMS accumulator (for accurate per-interval silence detection) ─
	// These are calculated directly from frame samples, not from astats metadata,
	// because astats with reset=0 provides cumulative stats, not per-interval.
	rawSumSquares  float64 // Sum of squared sample values (normalized -1 to 1)
	rawSampleCount int64   // Total sample count for this interval
	rawPeakAbs     float64 // Maximum absolute sample value (linear, 0.0-1.0) for this interval

	// ─── Peak tracking (max per interval, from astats metadata) ─────────────────
	peakMax float64 // Maximum peak level from astats (dBFS) - cumulative, less accurate

	// ─── aspectralstats accumulators (valid per-window from FFmpeg) ─────────────
	spectralMeanSum     float64
	spectralVarianceSum float64
	spectralCentroidSum float64
	spectralSpreadSum   float64
	spectralSkewnessSum float64
	spectralKurtosisSum float64
	spectralEntropySum  float64
	spectralFlatnessSum float64
	spectralCrestSum    float64
	spectralFluxSum     float64
	spectralSlopeSum    float64
	spectralDecreaseSum float64
	spectralRolloffSum  float64

	// ─── ebur128 accumulators (windowed measurements) ───────────────────────────
	momentaryLUFSSum float64
	shortTermLUFSSum float64
	truePeakMax      float64 // Maximum true peak
	samplePeakMax    float64 // Maximum sample peak
}

// intervalFrameMetrics holds per-frame metrics extracted from FFmpeg metadata.
// Only includes metrics that are valid per-window (not cumulative astats).
type intervalFrameMetrics struct {
	// Peak tracking (used for max tracking)
	PeakLevel float64

	// aspectralstats (valid per-window)
	SpectralMean     float64
	SpectralVariance float64
	SpectralCentroid float64
	SpectralSpread   float64
	SpectralSkewness float64
	SpectralKurtosis float64
	SpectralEntropy  float64
	SpectralFlatness float64
	SpectralCrest    float64
	SpectralFlux     float64
	SpectralSlope    float64
	SpectralDecrease float64
	SpectralRolloff  float64

	// ebur128 (windowed measurements)
	MomentaryLUFS float64
	ShortTermLUFS float64
	TruePeak      float64
	SamplePeak    float64
}

// add accumulates a frame's metrics into the interval.
func (a *intervalAccumulator) add(m intervalFrameMetrics) {
	// Peak levels: keep maximum
	if a.frameCount == 0 || m.PeakLevel > a.peakMax {
		a.peakMax = m.PeakLevel
	}
	if a.frameCount == 0 || m.TruePeak > a.truePeakMax {
		a.truePeakMax = m.TruePeak
	}
	if a.frameCount == 0 || m.SamplePeak > a.samplePeakMax {
		a.samplePeakMax = m.SamplePeak
	}

	// aspectralstats sums for averaging (valid per-window measurements)
	a.spectralMeanSum += m.SpectralMean
	a.spectralVarianceSum += m.SpectralVariance
	a.spectralCentroidSum += m.SpectralCentroid
	a.spectralSpreadSum += m.SpectralSpread
	a.spectralSkewnessSum += m.SpectralSkewness
	a.spectralKurtosisSum += m.SpectralKurtosis
	a.spectralEntropySum += m.SpectralEntropy
	a.spectralFlatnessSum += m.SpectralFlatness
	a.spectralCrestSum += m.SpectralCrest
	a.spectralFluxSum += m.SpectralFlux
	a.spectralSlopeSum += m.SpectralSlope
	a.spectralDecreaseSum += m.SpectralDecrease
	a.spectralRolloffSum += m.SpectralRolloff

	// ebur128 sums for averaging (windowed measurements)
	a.momentaryLUFSSum += m.MomentaryLUFS
	a.shortTermLUFSSum += m.ShortTermLUFS

	a.frameCount++
}

// frameSumSquaresAndPeak calculates sum of squared sample values, sample count, and peak from an audio frame.
// Handles S16, FLT, S32, and DBL sample formats, normalizing to [-1.0, 1.0] range.
// Returns sumSquares, sampleCount, peakAbsolute, and ok (false if format is unsupported or frame is invalid).
func frameSumSquaresAndPeak(frame *ffmpeg.AVFrame) (sumSquares float64, sampleCount int64, peakAbs float64, ok bool) {
	if frame == nil || frame.NbSamples() == 0 {
		return 0, 0, 0, false
	}

	sampleFmt := frame.Format()
	nbSamples := frame.NbSamples()
	nbChannels := frame.ChLayout().NbChannels()

	dataPtr := frame.Data().Get(0)
	if dataPtr == nil {
		return 0, 0, 0, false
	}

	// Guard against planar formats with multiple channels: Data().Get(0) returns
	// only plane 0 (channel 0), so slicing nbSamples*nbChannels would read out of bounds.
	isPlanar := false
	switch ffmpeg.AVSampleFormat(sampleFmt) {
	case ffmpeg.AVSampleFmtS16P, ffmpeg.AVSampleFmtFltp, ffmpeg.AVSampleFmtS32P, ffmpeg.AVSampleFmtDblp:
		isPlanar = true
	}
	if isPlanar && nbChannels > 1 {
		return 0, 0, 0, false
	}

	switch ffmpeg.AVSampleFormat(sampleFmt) {
	case ffmpeg.AVSampleFmtS16, ffmpeg.AVSampleFmtS16P:
		samples := unsafe.Slice((*int16)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample) / 32768.0
			sumSquares += normalized * normalized
			sampleCount++
			absVal := math.Abs(normalized)
			if absVal > peakAbs {
				peakAbs = absVal
			}
		}
		return sumSquares, sampleCount, peakAbs, true

	case ffmpeg.AVSampleFmtFlt, ffmpeg.AVSampleFmtFltp:
		samples := unsafe.Slice((*float32)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample)
			sumSquares += normalized * normalized
			sampleCount++
			absVal := math.Abs(normalized)
			if absVal > peakAbs {
				peakAbs = absVal
			}
		}
		return sumSquares, sampleCount, peakAbs, true

	case ffmpeg.AVSampleFmtS32, ffmpeg.AVSampleFmtS32P:
		samples := unsafe.Slice((*int32)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample) / 2147483648.0
			sumSquares += normalized * normalized
			sampleCount++
			absVal := math.Abs(normalized)
			if absVal > peakAbs {
				peakAbs = absVal
			}
		}
		return sumSquares, sampleCount, peakAbs, true

	case ffmpeg.AVSampleFmtDbl, ffmpeg.AVSampleFmtDblp:
		samples := unsafe.Slice((*float64)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			sumSquares += sample * sample
			sampleCount++
			absVal := math.Abs(sample)
			if absVal > peakAbs {
				peakAbs = absVal
			}
		}
		return sumSquares, sampleCount, peakAbs, true

	default:
		return 0, 0, 0, false
	}
}

// addFrameRMSAndPeak accumulates RMS and peak from raw frame samples for accurate per-interval measurement.
// This bypasses astats metadata (which is cumulative) to get true per-interval RMS and peak.
func (a *intervalAccumulator) addFrameRMSAndPeak(frame *ffmpeg.AVFrame) {
	if ss, count, peak, ok := frameSumSquaresAndPeak(frame); ok {
		a.rawSumSquares += ss
		a.rawSampleCount += count
		if peak > a.rawPeakAbs {
			a.rawPeakAbs = peak
		}
	}
}

// finalize converts accumulated values to an IntervalSample.
func (a *intervalAccumulator) finalize(timestamp time.Duration) IntervalSample {
	// PeakLevel: Use raw sample calculation for accurate per-interval measurement
	// This is calculated directly from frame samples, not from astats metadata,
	// because astats with reset=0 provides cumulative stats, not per-interval.
	var peakLevelDB float64
	if a.rawPeakAbs > 0 {
		peakLevelDB = 20.0 * math.Log10(a.rawPeakAbs)
	} else {
		peakLevelDB = -120.0
	}

	sample := IntervalSample{
		Timestamp: timestamp,

		// Max values
		PeakLevel:  peakLevelDB,
		TruePeak:   a.truePeakMax,
		SamplePeak: a.samplePeakMax,
	}

	// RMS Level: Use raw sample calculation for accurate per-interval measurement
	// This is calculated directly from frame samples, not from astats metadata,
	// because astats with reset=0 provides cumulative stats, not per-interval.
	if a.rawSampleCount > 0 {
		rms := math.Sqrt(a.rawSumSquares / float64(a.rawSampleCount))
		if rms < 0.00001 { // Equivalent to < -100 dB
			sample.RMSLevel = -120.0
		} else {
			sample.RMSLevel = 20.0 * math.Log10(rms)
		}
	} else {
		sample.RMSLevel = -120.0
	}

	if a.frameCount > 0 {
		n := float64(a.frameCount)

		// aspectralstats averages (valid per-window measurements)
		sample.SpectralMean = a.spectralMeanSum / n
		sample.SpectralVariance = a.spectralVarianceSum / n
		sample.SpectralCentroid = a.spectralCentroidSum / n
		sample.SpectralSpread = a.spectralSpreadSum / n
		sample.SpectralSkewness = a.spectralSkewnessSum / n
		sample.SpectralKurtosis = a.spectralKurtosisSum / n
		sample.SpectralEntropy = a.spectralEntropySum / n
		sample.SpectralFlatness = a.spectralFlatnessSum / n
		sample.SpectralCrest = a.spectralCrestSum / n
		sample.SpectralFlux = a.spectralFluxSum / n
		sample.SpectralSlope = a.spectralSlopeSum / n
		sample.SpectralDecrease = a.spectralDecreaseSum / n
		sample.SpectralRolloff = a.spectralRolloffSum / n

		// ebur128 averages (windowed measurements)
		sample.MomentaryLUFS = a.momentaryLUFSSum / n
		sample.ShortTermLUFS = a.shortTermLUFSSum / n
	}

	return sample
}

// reset clears the accumulator for the next interval.
func (a *intervalAccumulator) reset() {
	a.frameCount = 0

	// Raw sample RMS and peak
	a.rawSumSquares = 0
	a.rawSampleCount = 0
	a.rawPeakAbs = 0

	// Peak tracking (astats metadata)
	a.peakMax = -120.0

	// aspectralstats
	a.spectralMeanSum = 0
	a.spectralVarianceSum = 0
	a.spectralCentroidSum = 0
	a.spectralSpreadSum = 0
	a.spectralSkewnessSum = 0
	a.spectralKurtosisSum = 0
	a.spectralEntropySum = 0
	a.spectralFlatnessSum = 0
	a.spectralCrestSum = 0
	a.spectralFluxSum = 0
	a.spectralSlopeSum = 0
	a.spectralDecreaseSum = 0
	a.spectralRolloffSum = 0

	// ebur128
	a.momentaryLUFSSum = 0
	a.shortTermLUFSSum = 0
	a.truePeakMax = -120.0
	a.samplePeakMax = -120.0
}

// Cached metadata keys for frame extraction - avoids per-frame C string allocations
// These use GlobalCStr which maintains an internal cache, so identical strings share the same CStr
var (
	// aspectralstats metadata keys (all measurements)
	metaKeySpectralMean     = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.mean")
	metaKeySpectralVariance = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.variance")
	metaKeySpectralCentroid = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.centroid")
	metaKeySpectralSpread   = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.spread")
	metaKeySpectralSkewness = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.skewness")
	metaKeySpectralKurtosis = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.kurtosis")
	metaKeySpectralEntropy  = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.entropy")
	metaKeySpectralFlatness = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.flatness")
	metaKeySpectralCrest    = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.crest")
	metaKeySpectralFlux     = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.flux")
	metaKeySpectralSlope    = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.slope")
	metaKeySpectralDecrease = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.decrease")
	metaKeySpectralRolloff  = ffmpeg.GlobalCStr("lavfi.aspectralstats.1.rolloff")

	// astats per-channel metadata keys (channel .1 for mono after downmix)
	metaKeyDynamicRange      = ffmpeg.GlobalCStr("lavfi.astats.1.Dynamic_range")
	metaKeyRMSLevel          = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_level")
	metaKeyPeakLevel         = ffmpeg.GlobalCStr("lavfi.astats.1.Peak_level")
	metaKeyRMSTrough         = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_trough")
	metaKeyRMSPeak           = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_peak")
	metaKeyDCOffset          = ffmpeg.GlobalCStr("lavfi.astats.1.DC_offset")
	metaKeyFlatFactor        = ffmpeg.GlobalCStr("lavfi.astats.1.Flat_factor")
	metaKeyCrestFactor       = ffmpeg.GlobalCStr("lavfi.astats.1.Crest_factor")
	metaKeyZeroCrossingsRate = ffmpeg.GlobalCStr("lavfi.astats.1.Zero_crossings_rate")
	metaKeyZeroCrossings     = ffmpeg.GlobalCStr("lavfi.astats.1.Zero_crossings")
	metaKeyMaxDifference     = ffmpeg.GlobalCStr("lavfi.astats.1.Max_difference")
	metaKeyMinDifference     = ffmpeg.GlobalCStr("lavfi.astats.1.Min_difference")
	metaKeyMeanDifference    = ffmpeg.GlobalCStr("lavfi.astats.1.Mean_difference")
	metaKeyRMSDifference     = ffmpeg.GlobalCStr("lavfi.astats.1.RMS_difference")
	metaKeyEntropy           = ffmpeg.GlobalCStr("lavfi.astats.1.Entropy")
	metaKeyMinLevel          = ffmpeg.GlobalCStr("lavfi.astats.1.Min_level")
	metaKeyMaxLevel          = ffmpeg.GlobalCStr("lavfi.astats.1.Max_level")
	metaKeyNoiseFloor        = ffmpeg.GlobalCStr("lavfi.astats.1.Noise_floor")
	metaKeyNoiseFloorCount   = ffmpeg.GlobalCStr("lavfi.astats.1.Noise_floor_count")
	metaKeyBitDepth          = ffmpeg.GlobalCStr("lavfi.astats.1.Bit_depth")
	metaKeyNumberOfSamples   = ffmpeg.GlobalCStr("lavfi.astats.1.Number_of_samples")

	// astats overall metadata keys (used with measure_perchannel=0)
	metaKeyOverallRMSLevel    = ffmpeg.GlobalCStr("lavfi.astats.Overall.RMS_level")
	metaKeyOverallPeakLevel   = ffmpeg.GlobalCStr("lavfi.astats.Overall.Peak_level")
	metaKeyOverallCrestFactor = ffmpeg.GlobalCStr("lavfi.astats.Overall.Crest_factor")
	metaKeyOverallEntropy     = ffmpeg.GlobalCStr("lavfi.astats.Overall.Entropy")

	// ebur128 metadata keys
	metaKeyEbur128I            = ffmpeg.GlobalCStr("lavfi.r128.I")
	metaKeyEbur128M            = ffmpeg.GlobalCStr("lavfi.r128.M")
	metaKeyEbur128S            = ffmpeg.GlobalCStr("lavfi.r128.S")
	metaKeyEbur128TruePeak     = ffmpeg.GlobalCStr("lavfi.r128.true_peak")
	metaKeyEbur128SamplePeak   = ffmpeg.GlobalCStr("lavfi.r128.sample_peak")
	metaKeyEbur128LRA          = ffmpeg.GlobalCStr("lavfi.r128.LRA")
	metaKeyEbur128TargetThresh = ffmpeg.GlobalCStr("lavfi.r128.target_threshold")

	// Silence detection metadata keys (from silencedetect filter)
	// For mono audio these are lavfi.silence_start.1, lavfi.silence_end.1, lavfi.silence_duration.1
	metaKeySilenceStart    = ffmpeg.GlobalCStr("lavfi.silence_start")
	metaKeySilenceStart1   = ffmpeg.GlobalCStr("lavfi.silence_start.1")
	metaKeySilenceEnd      = ffmpeg.GlobalCStr("lavfi.silence_end")
	metaKeySilenceEnd1     = ffmpeg.GlobalCStr("lavfi.silence_end.1")
	metaKeySilenceDuration = ffmpeg.GlobalCStr("lavfi.silence_duration")
	metaKeySilenceDur1     = ffmpeg.GlobalCStr("lavfi.silence_duration.1")
)

// metadataAccumulators holds all accumulator variables for frame metadata extraction.
// Spectral stats (centroid, rolloff) are averaged across all frames.
// astats and ebur128 values are cumulative, so we keep the latest.
// baseMetadataAccumulators contains fields shared between input (Pass 1) and output (Pass 2) accumulators.
// Embedded in both metadataAccumulators and outputMetadataAccumulators to avoid duplication.
type baseMetadataAccumulators struct {
	// Spectral statistics from aspectralstats (averaged across frames)
	spectralMeanSum     float64
	spectralVarianceSum float64
	spectralCentroidSum float64
	spectralSpreadSum   float64
	spectralSkewnessSum float64
	spectralKurtosisSum float64
	spectralEntropySum  float64
	spectralFlatnessSum float64
	spectralCrestSum    float64
	spectralFluxSum     float64
	spectralSlopeSum    float64
	spectralDecreaseSum float64
	spectralRolloffSum  float64
	spectralFrameCount  int

	// astats measurements (cumulative - we keep latest values)
	astatsDynamicRange      float64
	astatsRMSLevel          float64
	astatsPeakLevel         float64
	astatsRMSTrough         float64
	astatsRMSPeak           float64
	astatsDCOffset          float64
	astatsFlatFactor        float64
	astatsCrestFactor       float64
	astatsZeroCrossingsRate float64
	astatsZeroCrossings     float64
	astatsMaxDifference     float64
	astatsMinDifference     float64
	astatsMeanDifference    float64
	astatsRMSDifference     float64
	astatsEntropy           float64
	astatsMinLevel          float64
	astatsMaxLevel          float64
	astatsNoiseFloor        float64
	astatsNoiseFloorCount   float64
	astatsBitDepth          float64
	astatsNumberOfSamples   float64
	astatsFound             bool
}

// accumulateSpectral adds the given spectral measurements to the running sums.
func (b *baseMetadataAccumulators) accumulateSpectral(spectral SpectralMetrics) {
	if !spectral.Found {
		return
	}
	b.spectralMeanSum += spectral.Mean
	b.spectralVarianceSum += spectral.Variance
	b.spectralCentroidSum += spectral.Centroid
	b.spectralSpreadSum += spectral.Spread
	b.spectralSkewnessSum += spectral.Skewness
	b.spectralKurtosisSum += spectral.Kurtosis
	b.spectralEntropySum += spectral.Entropy
	b.spectralFlatnessSum += spectral.Flatness
	b.spectralCrestSum += spectral.Crest
	b.spectralFluxSum += spectral.Flux
	b.spectralSlopeSum += spectral.Slope
	b.spectralDecreaseSum += spectral.Decrease
	b.spectralRolloffSum += spectral.Rolloff
	b.spectralFrameCount++
}

// extractAstatsMetadata extracts all astats measurements from FFmpeg metadata.
// These are cumulative values, so we keep the latest from each frame.
// Includes conversions: linearRatioToDB for CrestFactor, linearSampleToDBFS for MinLevel/MaxLevel.
func (b *baseMetadataAccumulators) extractAstatsMetadata(metadata *ffmpeg.AVDictionary) {
	if value, ok := getFloatMetadata(metadata, metaKeyDynamicRange); ok {
		b.astatsDynamicRange = value
		b.astatsFound = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSLevel); ok {
		b.astatsRMSLevel = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyPeakLevel); ok {
		b.astatsPeakLevel = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSTrough); ok {
		b.astatsRMSTrough = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSPeak); ok {
		b.astatsRMSPeak = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyDCOffset); ok {
		b.astatsDCOffset = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyFlatFactor); ok {
		b.astatsFlatFactor = value
	}
	// CrestFactor: FFmpeg reports as linear ratio (peak/RMS), convert to dB
	if value, ok := getFloatMetadata(metadata, metaKeyCrestFactor); ok {
		b.astatsCrestFactor = linearRatioToDB(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyZeroCrossingsRate); ok {
		b.astatsZeroCrossingsRate = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyZeroCrossings); ok {
		b.astatsZeroCrossings = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMaxDifference); ok {
		b.astatsMaxDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMinDifference); ok {
		b.astatsMinDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMeanDifference); ok {
		b.astatsMeanDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSDifference); ok {
		b.astatsRMSDifference = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEntropy); ok {
		b.astatsEntropy = value
	}
	// MinLevel/MaxLevel: FFmpeg reports as linear sample values, convert to dBFS
	if value, ok := getFloatMetadata(metadata, metaKeyMinLevel); ok {
		b.astatsMinLevel = linearSampleToDBFS(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyMaxLevel); ok {
		b.astatsMaxLevel = linearSampleToDBFS(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyNoiseFloor); ok {
		b.astatsNoiseFloor = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyNoiseFloorCount); ok {
		b.astatsNoiseFloorCount = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyBitDepth); ok {
		b.astatsBitDepth = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyNumberOfSamples); ok {
		b.astatsNumberOfSamples = value
	}
}

// metadataAccumulators holds accumulator variables for Pass 1 frame metadata extraction.
// Uses baseMetadataAccumulators for spectral and astats fields shared with output analysis.
type metadataAccumulators struct {
	// Embed shared spectral and astats fields
	baseMetadataAccumulators

	// ebur128 measurements (cumulative - we keep latest values)
	ebur128InputI   float64
	ebur128InputM   float64 // Momentary loudness (400ms window, updates per frame)
	ebur128InputS   float64 // Short-term loudness (3s window)
	ebur128InputTP  float64
	ebur128InputSP  float64 // Sample peak
	ebur128InputLRA float64
	ebur128Found    bool

	// Silence detection (collected across frames)
	// silencedetect sets lavfi.silence_start on first frame of silence,
	// then lavfi.silence_end and lavfi.silence_duration on first frame after silence ends
	silenceRegions      []SilenceRegion
	pendingSilenceStart float64 // Pending silence start timestamp (seconds)
	hasPendingSilence   bool    // Whether we have a pending silence start
}

// getFloatMetadata extracts a float value from the metadata dictionary
func getFloatMetadata(metadata *ffmpeg.AVDictionary, key *ffmpeg.CStr) (float64, bool) {
	if entry := ffmpeg.AVDictGet(metadata, key, nil, 0); entry != nil {
		if value, err := strconv.ParseFloat(entry.Value().String(), 64); err == nil {
			return value, true
		}
	}
	return 0.0, false
}

// linearRatioToDB converts a linear ratio (e.g., Crest_factor) to decibels.
// FFmpeg's astats Crest_factor is reported as a linear ratio (peak/RMS), not in dB.
func linearRatioToDB(ratio float64) float64 {
	if ratio <= 0 {
		return -120.0 // Floor for zero/negative values
	}
	return 20 * math.Log10(ratio)
}

// linearSampleToDBFS converts a linear sample value to dBFS.
// FFmpeg's astats Min_level and Max_level are reported as linear sample values
// (typically -1.0 to +1.0 for float audio, or integer sample values).
// We normalize assuming the value represents the fraction of full scale.
func linearSampleToDBFS(sample float64) float64 {
	absVal := math.Abs(sample)
	if absVal <= 0 {
		return -120.0 // Floor for zero values
	}
	// For normalized float audio (-1.0 to +1.0), this is direct
	// For integer sample values, we need to detect and normalize
	// If abs value > 1.0, assume integer samples and normalize to 16-bit range
	if absVal > 1.0 {
		// Likely integer sample value (e.g., from 16-bit audio: -32768 to 32767)
		absVal = absVal / 32768.0
	}
	if absVal > 1.0 {
		absVal = 1.0 // Clamp to 0 dBFS max
	}
	return 20 * math.Log10(absVal)
}

// SpectralMetrics holds the 13 aspectralstats measurements extracted from FFmpeg metadata.
// These metrics characterise the frequency content of audio frames.
type SpectralMetrics struct {
	Mean     float64 `json:"mean"`     // Average spectral power
	Variance float64 `json:"variance"` // Spectral variance
	Centroid float64 `json:"centroid"` // Spectral centroid (Hz) - where energy is concentrated
	Spread   float64 `json:"spread"`   // Spectral spread (Hz) - bandwidth/fullness indicator
	Skewness float64 `json:"skewness"` // Spectral asymmetry - positive=bright, negative=dark
	Kurtosis float64 `json:"kurtosis"` // Spectral peakiness - tonal vs broadband content
	Entropy  float64 `json:"entropy"`  // Spectral randomness (0-1) - noise classification
	Flatness float64 `json:"flatness"` // Noise vs tonal ratio (0-1) - low=tonal, high=noisy
	Crest    float64 `json:"crest"`    // Spectral peak-to-RMS - transient indicator
	Flux     float64 `json:"flux"`     // Frame-to-frame spectral change
	Slope    float64 `json:"slope"`    // Spectral tilt - negative=more bass
	Decrease float64 `json:"decrease"` // Average spectral decrease
	Rolloff  float64 `json:"rolloff"`  // Spectral rolloff (Hz) - HF energy dropoff point
	Found    bool    `json:"-"`        // True if any spectral metric was extracted
}

// spectralFields returns the 13 spectral measurements from this interval as a SpectralMetrics value.
// This enables struct-level accumulation instead of 13 individual variables.
func (s *IntervalSample) spectralFields() SpectralMetrics {
	return SpectralMetrics{
		Mean:     s.SpectralMean,
		Variance: s.SpectralVariance,
		Centroid: s.SpectralCentroid,
		Spread:   s.SpectralSpread,
		Skewness: s.SpectralSkewness,
		Kurtosis: s.SpectralKurtosis,
		Entropy:  s.SpectralEntropy,
		Flatness: s.SpectralFlatness,
		Crest:    s.SpectralCrest,
		Flux:     s.SpectralFlux,
		Slope:    s.SpectralSlope,
		Decrease: s.SpectralDecrease,
		Rolloff:  s.SpectralRolloff,
		Found:    true,
	}
}

// add accumulates another SpectralMetrics into this one (element-wise sum).
func (m *SpectralMetrics) add(other SpectralMetrics) {
	m.Mean += other.Mean
	m.Variance += other.Variance
	m.Centroid += other.Centroid
	m.Spread += other.Spread
	m.Skewness += other.Skewness
	m.Kurtosis += other.Kurtosis
	m.Entropy += other.Entropy
	m.Flatness += other.Flatness
	m.Crest += other.Crest
	m.Flux += other.Flux
	m.Slope += other.Slope
	m.Decrease += other.Decrease
	m.Rolloff += other.Rolloff
}

// average returns a new SpectralMetrics with all fields divided by n.
func (m SpectralMetrics) average(n float64) SpectralMetrics {
	return SpectralMetrics{
		Mean:     m.Mean / n,
		Variance: m.Variance / n,
		Centroid: m.Centroid / n,
		Spread:   m.Spread / n,
		Skewness: m.Skewness / n,
		Kurtosis: m.Kurtosis / n,
		Entropy:  m.Entropy / n,
		Flatness: m.Flatness / n,
		Crest:    m.Crest / n,
		Flux:     m.Flux / n,
		Slope:    m.Slope / n,
		Decrease: m.Decrease / n,
		Rolloff:  m.Rolloff / n,
	}
}

// extractSpectralMetrics extracts all 13 aspectralstats measurements from FFmpeg metadata.
// Returns a SpectralMetrics struct with Found=true if at least one metric was extracted.
func extractSpectralMetrics(metadata *ffmpeg.AVDictionary) SpectralMetrics {
	var m SpectralMetrics

	if value, ok := getFloatMetadata(metadata, metaKeySpectralMean); ok {
		m.Mean = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralVariance); ok {
		m.Variance = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralCentroid); ok {
		m.Centroid = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralSpread); ok {
		m.Spread = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralSkewness); ok {
		m.Skewness = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralKurtosis); ok {
		m.Kurtosis = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralEntropy); ok {
		m.Entropy = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralFlatness); ok {
		m.Flatness = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralCrest); ok {
		m.Crest = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralFlux); ok {
		m.Flux = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralSlope); ok {
		m.Slope = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralDecrease); ok {
		m.Decrease = value
		m.Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeySpectralRolloff); ok {
		m.Rolloff = value
		m.Found = true
	}

	return m
}

// extractIntervalFrameMetrics extracts per-frame metrics for interval accumulation.
// Only collects metrics that are valid per-window (aspectralstats, ebur128 windowed).
// Excludes astats which provides cumulative values, not per-interval.
func extractIntervalFrameMetrics(metadata *ffmpeg.AVDictionary, spectral SpectralMetrics) intervalFrameMetrics {
	var m intervalFrameMetrics

	// Peak level from astats (used for max tracking, which is valid per-interval)
	m.PeakLevel, _ = getFloatMetadata(metadata, metaKeyPeakLevel)

	// aspectralstats metrics (valid per-window measurements, pre-extracted by caller)
	m.SpectralMean = spectral.Mean
	m.SpectralVariance = spectral.Variance
	m.SpectralCentroid = spectral.Centroid
	m.SpectralSpread = spectral.Spread
	m.SpectralSkewness = spectral.Skewness
	m.SpectralKurtosis = spectral.Kurtosis
	m.SpectralEntropy = spectral.Entropy
	m.SpectralFlatness = spectral.Flatness
	m.SpectralCrest = spectral.Crest
	m.SpectralFlux = spectral.Flux
	m.SpectralSlope = spectral.Slope
	m.SpectralDecrease = spectral.Decrease
	m.SpectralRolloff = spectral.Rolloff

	// ebur128 windowed measurements
	m.MomentaryLUFS, _ = getFloatMetadata(metadata, metaKeyEbur128M)
	m.ShortTermLUFS, _ = getFloatMetadata(metadata, metaKeyEbur128S)

	// ebur128 peak values are linear ratios, convert to dB
	if rawTP, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
		m.TruePeak = linearRatioToDB(rawTP)
	}
	if rawSP, ok := getFloatMetadata(metadata, metaKeyEbur128SamplePeak); ok {
		m.SamplePeak = linearRatioToDB(rawSP)
	}

	return m
}

// extractFrameMetadata extracts audio analysis metadata from a filtered frame.
// Updates accumulators with spectral, astats, and ebur128 measurements.
// Called from both the main processing loop and the flush loop.
func extractFrameMetadata(metadata *ffmpeg.AVDictionary, acc *metadataAccumulators, spectral SpectralMetrics) {
	if metadata == nil {
		return
	}

	// Accumulate pre-extracted spectral metrics (averaged across frames)
	// For mono audio, spectral stats are under channel .1
	acc.accumulateSpectral(spectral)

	// Extract astats measurements (cumulative, so we keep the latest)
	// For mono audio, stats are under channel .1
	acc.extractAstatsMetadata(metadata)

	// Extract ebur128 measurements (cumulative loudness analysis)
	// ebur128 provides: M (momentary 400ms), S (short-term 3s), I (integrated), LRA, sample_peak, true_peak
	// We need these for loudness normalization and interval-based analysis
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128I); ok {
		acc.ebur128InputI = value
		acc.ebur128Found = true
	}

	// Momentary loudness (400ms window) - useful for interval-based silence detection
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128M); ok {
		acc.ebur128InputM = value
	}

	// Short-term loudness (3s window)
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128S); ok {
		acc.ebur128InputS = value
	}

	if value, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
		acc.ebur128InputTP = linearRatioToDB(value)
	}

	if value, ok := getFloatMetadata(metadata, metaKeyEbur128SamplePeak); ok {
		acc.ebur128InputSP = linearRatioToDB(value)
	}

	if value, ok := getFloatMetadata(metadata, metaKeyEbur128LRA); ok {
		acc.ebur128InputLRA = value
	}

	// Extract silence detection metadata
	// silencedetect sets lavfi.silence_start on the first frame of a silence region,
	// then lavfi.silence_end and lavfi.silence_duration on the first frame after silence ends.
	// For mono audio, these may be suffixed with .1
	var silenceStart float64
	var hasSilenceStart bool
	if value, ok := getFloatMetadata(metadata, metaKeySilenceStart); ok {
		silenceStart = value
		hasSilenceStart = true
	} else if value, ok := getFloatMetadata(metadata, metaKeySilenceStart1); ok {
		silenceStart = value
		hasSilenceStart = true
	}

	if hasSilenceStart {
		acc.pendingSilenceStart = silenceStart
		acc.hasPendingSilence = true
	}

	// Check for silence end - this completes a silence region
	var silenceEnd, silenceDuration float64
	var hasSilenceEnd bool
	if value, ok := getFloatMetadata(metadata, metaKeySilenceEnd); ok {
		silenceEnd = value
		hasSilenceEnd = true
	} else if value, ok := getFloatMetadata(metadata, metaKeySilenceEnd1); ok {
		silenceEnd = value
		hasSilenceEnd = true
	}

	if hasSilenceEnd {
		// Get duration - try both keys
		if value, ok := getFloatMetadata(metadata, metaKeySilenceDuration); ok {
			silenceDuration = value
		} else if value, ok := getFloatMetadata(metadata, metaKeySilenceDur1); ok {
			silenceDuration = value
		}

		// Record the completed silence region
		if acc.hasPendingSilence {
			region := SilenceRegion{
				Start:    time.Duration(acc.pendingSilenceStart * float64(time.Second)),
				End:      time.Duration(silenceEnd * float64(time.Second)),
				Duration: time.Duration(silenceDuration * float64(time.Second)),
			}
			acc.silenceRegions = append(acc.silenceRegions, region)
			acc.hasPendingSilence = false
		}
	}
}

// outputMetadataAccumulators holds accumulator variables for Pass 2 output measurement extraction.
// Mirrors metadataAccumulators but without silence detection fields.
// outputMetadataAccumulators holds accumulator variables for Pass 2 output measurement extraction.
// Uses baseMetadataAccumulators for spectral and astats fields shared with input analysis.
type outputMetadataAccumulators struct {
	// Embed shared spectral and astats fields
	baseMetadataAccumulators

	// ebur128 measurements (cumulative - we keep latest values)
	ebur128OutputI      float64
	ebur128OutputM      float64 // Momentary loudness
	ebur128OutputS      float64 // Short-term loudness
	ebur128OutputTP     float64
	ebur128OutputSP     float64 // Sample peak
	ebur128OutputLRA    float64
	ebur128OutputThresh float64 // Gating threshold for loudnorm
	ebur128Found        bool
}

// extractOutputFrameMetadata extracts audio analysis metadata from a Pass 2 filtered frame.
// Updates accumulators with spectral, astats, and ebur128 measurements.
// This is the output analysis counterpart to extractFrameMetadata.
func extractOutputFrameMetadata(metadata *ffmpeg.AVDictionary, acc *outputMetadataAccumulators) {
	if metadata == nil {
		return
	}

	// Extract all aspectralstats measurements (averaged across frames)
	acc.accumulateSpectral(extractSpectralMetrics(metadata))

	// Extract astats measurements (cumulative, so we keep the latest)
	acc.extractAstatsMetadata(metadata)

	// Extract ebur128 measurements
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128I); ok {
		acc.ebur128OutputI = value
		acc.ebur128Found = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128M); ok {
		acc.ebur128OutputM = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128S); ok {
		acc.ebur128OutputS = value
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128TruePeak); ok {
		acc.ebur128OutputTP = linearRatioToDB(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128SamplePeak); ok {
		acc.ebur128OutputSP = linearRatioToDB(value)
	}
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128LRA); ok {
		acc.ebur128OutputLRA = value
	}
	// Gating threshold (for loudnorm two-pass mode)
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128TargetThresh); ok {
		acc.ebur128OutputThresh = value
	}
}

// finalizeOutputMeasurements converts accumulated values to OutputMeasurements struct.
// Returns nil if no measurements were captured.
func finalizeOutputMeasurements(acc *outputMetadataAccumulators) *OutputMeasurements {
	if !acc.ebur128Found && !acc.astatsFound && acc.spectralFrameCount == 0 {
		return nil // No measurements captured
	}

	m := &OutputMeasurements{
		BaseMeasurements: BaseMeasurements{
			// ebur128 momentary/short-term loudness
			MomentaryLoudness: acc.ebur128OutputM,
			ShortTermLoudness: acc.ebur128OutputS,
			SamplePeak:        acc.ebur128OutputSP,

			// astats time-domain measurements
			DynamicRange:      acc.astatsDynamicRange,
			RMSLevel:          acc.astatsRMSLevel,
			PeakLevel:         acc.astatsPeakLevel,
			RMSTrough:         acc.astatsRMSTrough,
			RMSPeak:           acc.astatsRMSPeak,
			DCOffset:          acc.astatsDCOffset,
			FlatFactor:        acc.astatsFlatFactor,
			CrestFactor:       acc.astatsCrestFactor,
			ZeroCrossingsRate: acc.astatsZeroCrossingsRate,
			ZeroCrossings:     acc.astatsZeroCrossings,
			MaxDifference:     acc.astatsMaxDifference,
			MinDifference:     acc.astatsMinDifference,
			MeanDifference:    acc.astatsMeanDifference,
			RMSDifference:     acc.astatsRMSDifference,
			Entropy:           acc.astatsEntropy,
			MinLevel:          acc.astatsMinLevel,
			MaxLevel:          acc.astatsMaxLevel,
			AstatsNoiseFloor:  acc.astatsNoiseFloor,
			NoiseFloorCount:   acc.astatsNoiseFloorCount,
			BitDepth:          acc.astatsBitDepth,
			NumberOfSamples:   acc.astatsNumberOfSamples,
		},
		// Output-specific loudness measurements
		OutputI:      acc.ebur128OutputI,
		OutputTP:     acc.ebur128OutputTP,
		OutputLRA:    acc.ebur128OutputLRA,
		OutputThresh: acc.ebur128OutputThresh,
		TargetOffset: 0.0, // Will be calculated in Pass 3
	}

	// If ebur128 target_threshold metadata is missing, calculate it manually
	// according to EBU R128 standard: gating threshold = integrated loudness - 10 LU
	if m.OutputThresh == 0.0 && m.OutputI != 0.0 {
		m.OutputThresh = m.OutputI - 10.0
	}

	// Calculate average spectral statistics from aspectralstats
	if acc.spectralFrameCount > 0 {
		frameCount := float64(acc.spectralFrameCount)
		m.SpectralMean = acc.spectralMeanSum / frameCount
		m.SpectralVariance = acc.spectralVarianceSum / frameCount
		m.SpectralCentroid = acc.spectralCentroidSum / frameCount
		m.SpectralSpread = acc.spectralSpreadSum / frameCount
		m.SpectralSkewness = acc.spectralSkewnessSum / frameCount
		m.SpectralKurtosis = acc.spectralKurtosisSum / frameCount
		m.SpectralEntropy = acc.spectralEntropySum / frameCount
		m.SpectralFlatness = acc.spectralFlatnessSum / frameCount
		m.SpectralCrest = acc.spectralCrestSum / frameCount
		m.SpectralFlux = acc.spectralFluxSum / frameCount
		m.SpectralSlope = acc.spectralSlopeSum / frameCount
		m.SpectralDecrease = acc.spectralDecreaseSum / frameCount
		m.SpectralRolloff = acc.spectralRolloffSum / frameCount
	}

	return m
}
