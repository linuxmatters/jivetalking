package processor

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"time"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// IntervalSample contains all measurements for a 250ms audio window.
// Captures comprehensive metrics from astats, aspectralstats, and ebur128 for
// room tone detection, adaptive filter tuning, and post-hoc analysis.
type IntervalSample struct {
	Timestamp time.Duration `json:"timestamp"` // Start of this interval

	// ─── Amplitude metrics (calculated per-interval from raw samples) ───────────
	RMSLevel  float64 `json:"rms_level"`  // dBFS, RMS level calculated from raw frame samples
	PeakLevel float64 `json:"peak_level"` // dBFS, peak level (max tracked per interval)

	// ─── aspectralstats spectral metrics (valid per-window from FFmpeg) ─────────
	Spectral SpectralMetrics `json:"-"` // Kept flat in JSON by custom marshal helpers

	// ─── ebur128 loudness metrics (windowed measurements) ───────────────────────
	MomentaryLUFS float64 `json:"momentary_lufs"`  // LUFS - 400ms window loudness
	ShortTermLUFS float64 `json:"short_term_lufs"` // LUFS - 3s window loudness
	TruePeak      float64 `json:"true_peak"`       // dBTP - true peak level (max tracked)
	SamplePeak    float64 `json:"sample_peak"`     // dBFS - sample peak level (max tracked)
}

type intervalSampleJSON struct {
	Timestamp time.Duration `json:"timestamp"`

	RMSLevel  float64 `json:"rms_level"`
	PeakLevel float64 `json:"peak_level"`

	SpectralMean     float64 `json:"spectral_mean"`
	SpectralVariance float64 `json:"spectral_variance"`
	SpectralCentroid float64 `json:"spectral_centroid"`
	SpectralSpread   float64 `json:"spectral_spread"`
	SpectralSkewness float64 `json:"spectral_skewness"`
	SpectralKurtosis float64 `json:"spectral_kurtosis"`
	SpectralEntropy  float64 `json:"spectral_entropy"`
	SpectralFlatness float64 `json:"spectral_flatness"`
	SpectralCrest    float64 `json:"spectral_crest"`
	SpectralFlux     float64 `json:"spectral_flux"`
	SpectralSlope    float64 `json:"spectral_slope"`
	SpectralDecrease float64 `json:"spectral_decrease"`
	SpectralRolloff  float64 `json:"spectral_rolloff"`

	MomentaryLUFS float64 `json:"momentary_lufs"`
	ShortTermLUFS float64 `json:"short_term_lufs"`
	TruePeak      float64 `json:"true_peak"`
	SamplePeak    float64 `json:"sample_peak"`
}

// MarshalJSON preserves the flat spectral_* JSON contract while the Go model
// carries interval spectral data as a SpectralMetrics value. Non-finite float
// fields (NaN, +Inf, -Inf) serialise to JSON null via the shared sanitiseValue
// sweep, mirroring the run-record convention (MarshalRunRecord). Without this,
// encoding/json errors on non-finite floats and aborts the .intervals.jsonl
// sidecar mid-stream on digitally-silent (voice-gated) audio. Finite values are
// byte-identical to a raw marshal.
func (s IntervalSample) MarshalJSON() ([]byte, error) {
	flat := intervalSampleJSON{
		Timestamp: s.Timestamp,

		RMSLevel:  s.RMSLevel,
		PeakLevel: s.PeakLevel,

		SpectralMean:     s.Spectral.Mean,
		SpectralVariance: s.Spectral.Variance,
		SpectralCentroid: s.Spectral.Centroid,
		SpectralSpread:   s.Spectral.Spread,
		SpectralSkewness: s.Spectral.Skewness,
		SpectralKurtosis: s.Spectral.Kurtosis,
		SpectralEntropy:  s.Spectral.Entropy,
		SpectralFlatness: s.Spectral.Flatness,
		SpectralCrest:    s.Spectral.Crest,
		SpectralFlux:     s.Spectral.Flux,
		SpectralSlope:    s.Spectral.Slope,
		SpectralDecrease: s.Spectral.Decrease,
		SpectralRolloff:  s.Spectral.Rolloff,

		MomentaryLUFS: s.MomentaryLUFS,
		ShortTermLUFS: s.ShortTermLUFS,
		TruePeak:      s.TruePeak,
		SamplePeak:    s.SamplePeak,
	}
	return json.Marshal(sanitiseValue(reflect.ValueOf(flat)))
}

// UnmarshalJSON accepts the legacy flat spectral_* JSON contract.
func (s *IntervalSample) UnmarshalJSON(data []byte) error {
	var decoded intervalSampleJSON
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	s.Timestamp = decoded.Timestamp
	s.RMSLevel = decoded.RMSLevel
	s.PeakLevel = decoded.PeakLevel
	s.Spectral = SpectralMetrics{
		Mean:     decoded.SpectralMean,
		Variance: decoded.SpectralVariance,
		Centroid: decoded.SpectralCentroid,
		Spread:   decoded.SpectralSpread,
		Skewness: decoded.SpectralSkewness,
		Kurtosis: decoded.SpectralKurtosis,
		Entropy:  decoded.SpectralEntropy,
		Flatness: decoded.SpectralFlatness,
		Crest:    decoded.SpectralCrest,
		Flux:     decoded.SpectralFlux,
		Slope:    decoded.SpectralSlope,
		Decrease: decoded.SpectralDecrease,
		Rolloff:  decoded.SpectralRolloff,
	}
	s.MomentaryLUFS = decoded.MomentaryLUFS
	s.ShortTermLUFS = decoded.ShortTermLUFS
	s.TruePeak = decoded.TruePeak
	s.SamplePeak = decoded.SamplePeak

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	s.Spectral.Found = hasSpectralKeys(raw)

	return nil
}

// intervalSpectralJSONKeys is the single source of truth for the flat spectral_*
// JSON keys on an interval sample. The intervalSampleJSON marshal/unmarshal tags
// must match this list, and hasSpectralKeys probes exactly these keys.
var intervalSpectralJSONKeys = []string{
	"spectral_mean",
	"spectral_variance",
	"spectral_centroid",
	"spectral_spread",
	"spectral_skewness",
	"spectral_kurtosis",
	"spectral_entropy",
	"spectral_flatness",
	"spectral_crest",
	"spectral_flux",
	"spectral_slope",
	"spectral_decrease",
	"spectral_rolloff",
}

func hasSpectralKeys(raw map[string]json.RawMessage) bool {
	for _, key := range intervalSpectralJSONKeys {
		if _, ok := raw[key]; ok {
			return true
		}
	}
	return false
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

	// ─── aspectralstats accumulators (valid per-window from FFmpeg) ─────────────
	spectralSum   SpectralMetrics
	spectralFound bool

	// ─── ebur128 accumulators (windowed measurements) ───────────────────────────
	momentaryLUFSSum float64
	shortTermLUFSSum float64
	truePeakMax      float64 // Maximum true peak
	samplePeakMax    float64 // Maximum sample peak
}

// optionalFloat carries a pre-fetched metadata value together with its found
// flag. fetched=false means the value was not pre-fetched and the consumer must
// fetch it itself, preserving the original fetch-at-call-site behaviour.
type optionalFloat struct {
	value   float64
	ok      bool
	fetched bool
}

// frameLoudnessMetrics holds the ebur128 quad and astats Peak_level fetched
// once per frame in the Pass 1 OnFrame callback. The same metadata dictionary
// is consumed by both extractFrameMetadata and extractIntervalFrameMetrics, so
// these keys are fetched once here and shared to avoid duplicate AVDictGet +
// ParseFloat work. Each field carries its found flag to reproduce the exact
// missing-key handling of the original per-consumer fetches: a present value is
// applied, a missing value is skipped (accumulator) or treated as zero
// (interval metrics) exactly as before.
type frameLoudnessMetrics struct {
	momentary       float64 // lavfi.r128.M (raw)
	momentaryFound  bool
	shortTerm       float64 // lavfi.r128.S (raw)
	shortTermFound  bool
	truePeak        float64 // lavfi.r128.true_peak (raw, pre-conversion)
	truePeakFound   bool
	samplePeak      float64 // lavfi.r128.sample_peak (raw, pre-conversion)
	samplePeakFound bool
	peakLevel       float64 // lavfi.astats.1.Peak_level (raw)
	peakLevelFound  bool
}

// extractFrameLoudnessMetrics fetches the ebur128 quad and astats Peak_level
// once from the frame metadata. Raw values match the original getFloatMetadata
// returns; dB conversion stays at the consumer call sites to preserve exact
// behaviour.
func extractFrameLoudnessMetrics(metadata *ffmpeg.AVDictionary) frameLoudnessMetrics {
	var m frameLoudnessMetrics
	m.momentary, m.momentaryFound = getFloatMetadata(metadata, metaKeyEbur128M)
	m.shortTerm, m.shortTermFound = getFloatMetadata(metadata, metaKeyEbur128S)
	m.truePeak, m.truePeakFound = getFloatMetadata(metadata, metaKeyEbur128TruePeak)
	m.samplePeak, m.samplePeakFound = getFloatMetadata(metadata, metaKeyEbur128SamplePeak)
	m.peakLevel, m.peakLevelFound = getFloatMetadata(metadata, metaKeyPeakLevel)
	return m
}

// intervalFrameMetrics holds per-frame metrics extracted from FFmpeg metadata.
// Only includes metrics that are valid per-window (not cumulative astats).
type intervalFrameMetrics struct {
	// Peak tracking (used for max tracking)
	PeakLevel float64

	// aspectralstats (valid per-window)
	Spectral SpectralMetrics

	// ebur128 (windowed measurements)
	MomentaryLUFS float64
	ShortTermLUFS float64
	TruePeak      float64
	SamplePeak    float64
}

// add accumulates a frame's metrics into the interval.
func (a *intervalAccumulator) add(m intervalFrameMetrics) {
	// Peak levels: keep maximum
	if a.frameCount == 0 || m.TruePeak > a.truePeakMax {
		a.truePeakMax = m.TruePeak
	}
	if a.frameCount == 0 || m.SamplePeak > a.samplePeakMax {
		a.samplePeakMax = m.SamplePeak
	}

	// aspectralstats sums for averaging (valid per-window measurements)
	a.spectralSum.add(m.Spectral)
	if m.Spectral.Found {
		a.spectralFound = true
	}

	// ebur128 sums for averaging (windowed measurements)
	a.momentaryLUFSSum += m.MomentaryLUFS
	a.shortTermLUFSSum += m.ShortTermLUFS

	a.frameCount++
}

// frameSumSquaresAndPeak calculates sum of squared sample values, sample count, and peak from an audio frame.
// Handles S16, FLT, S32, and DBL sample formats (both interleaved and planar), normalizing to [-1.0, 1.0] range.
// For planar multi-channel formats, iterates each plane separately via Data().Get(ch).
// Returns sumSquares, sampleCount, peakAbsolute, and ok (false if format is unsupported or frame is invalid).
func frameSumSquaresAndPeak(frame *ffmpeg.AVFrame) (sumSquares float64, sampleCount int64, peakAbs float64, ok bool) {
	if frame == nil || frame.NbSamples() == 0 {
		return 0, 0, 0, false
	}

	sampleFmt := ffmpeg.AVSampleFormat(frame.Format()) //nolint:gosec // AVSampleFormat values fit in int32
	nbSamples := frame.NbSamples()
	nbChannels := frame.ChLayout().NbChannels()

	// Determine if the format is planar (one plane per channel)
	isPlanar := false
	switch sampleFmt {
	case ffmpeg.AVSampleFmtS16P, ffmpeg.AVSampleFmtFltp, ffmpeg.AVSampleFmtS32P, ffmpeg.AVSampleFmtDblp:
		isPlanar = true
	}

	// For interleaved formats, all samples are in plane 0 with nbSamples*nbChannels elements.
	// For planar formats, each channel has its own plane with nbSamples elements.
	planes := 1
	samplesPerPlane := nbSamples * nbChannels
	if isPlanar {
		planes = nbChannels
		samplesPerPlane = nbSamples
	}

	for plane := 0; plane < planes; plane++ {
		dataPtr := frame.Data().Get(uintptr(plane))
		if dataPtr == nil {
			return 0, 0, 0, false
		}

		switch sampleFmt {
		case ffmpeg.AVSampleFmtS16, ffmpeg.AVSampleFmtS16P:
			samples := unsafe.Slice((*int16)(dataPtr), samplesPerPlane)
			for _, sample := range samples {
				normalized := float64(sample) / 32768.0
				sumSquares += normalized * normalized
				sampleCount++
				absVal := math.Abs(normalized)
				if absVal > peakAbs {
					peakAbs = absVal
				}
			}

		case ffmpeg.AVSampleFmtFlt, ffmpeg.AVSampleFmtFltp:
			samples := unsafe.Slice((*float32)(dataPtr), samplesPerPlane)
			for _, sample := range samples {
				normalized := float64(sample)
				sumSquares += normalized * normalized
				sampleCount++
				absVal := math.Abs(normalized)
				if absVal > peakAbs {
					peakAbs = absVal
				}
			}

		case ffmpeg.AVSampleFmtS32, ffmpeg.AVSampleFmtS32P:
			samples := unsafe.Slice((*int32)(dataPtr), samplesPerPlane)
			for _, sample := range samples {
				normalized := float64(sample) / 2147483648.0
				sumSquares += normalized * normalized
				sampleCount++
				absVal := math.Abs(normalized)
				if absVal > peakAbs {
					peakAbs = absVal
				}
			}

		case ffmpeg.AVSampleFmtDbl, ffmpeg.AVSampleFmtDblp:
			samples := unsafe.Slice((*float64)(dataPtr), samplesPerPlane)
			for _, sample := range samples {
				sumSquares += sample * sample
				sampleCount++
				absVal := math.Abs(sample)
				if absVal > peakAbs {
					peakAbs = absVal
				}
			}

		default:
			return 0, 0, 0, false
		}
	}

	return sumSquares, sampleCount, peakAbs, true
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
		sample.Spectral = a.spectralSum.average(n)
		sample.Spectral.Found = a.spectralFound

		// ebur128 averages (windowed measurements)
		sample.MomentaryLUFS = a.momentaryLUFSSum / n
		sample.ShortTermLUFS = a.shortTermLUFSSum / n
	}

	return sample
}

// reset clears the accumulator for the next interval.
func (a *intervalAccumulator) reset() {
	*a = intervalAccumulator{
		truePeakMax:   -120.0,
		samplePeakMax: -120.0,
	}
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
	// ebur128 metadata keys
	metaKeyEbur128I            = ffmpeg.GlobalCStr("lavfi.r128.I")
	metaKeyEbur128M            = ffmpeg.GlobalCStr("lavfi.r128.M")
	metaKeyEbur128S            = ffmpeg.GlobalCStr("lavfi.r128.S")
	metaKeyEbur128TruePeak     = ffmpeg.GlobalCStr("lavfi.r128.true_peak")
	metaKeyEbur128SamplePeak   = ffmpeg.GlobalCStr("lavfi.r128.sample_peak")
	metaKeyEbur128LRA          = ffmpeg.GlobalCStr("lavfi.r128.LRA")
	metaKeyEbur128TargetThresh = ffmpeg.GlobalCStr("lavfi.r128.target_threshold")
)

// baseMetadataAccumulators contains fields shared between input (Pass 1) and output (Pass 2) accumulators.
// Embedded in both metadataAccumulators and outputMetadataAccumulators to avoid duplication.
// Spectral stats are averaged across frames; astats and ebur128 values are cumulative, so the latest wins.
type baseMetadataAccumulators struct {
	// Spectral statistics from aspectralstats (averaged across frames)
	spectral SpectralAccumulator

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
	b.spectral.Add(spectral)
}

// finalizeSpectral returns averaged spectral metrics from the accumulated sums.
// Returns zero-value SpectralMetrics when no spectral frames were accumulated.
func (b *baseMetadataAccumulators) finalizeSpectral() SpectralMetrics {
	return b.spectral.Average()
}

// extractAstatsMetadata extracts all astats measurements from FFmpeg metadata.
// These are cumulative values, so we keep the latest from each frame.
// Includes conversions: linearRatioToDB for CrestFactor, linearSampleToDBFS for MinLevel/MaxLevel.
// peakLevel carries the optional pre-fetched lavfi.astats.1.Peak_level so the
// Pass 1 hot loop fetches it once and shares it with extractIntervalFrameMetrics.
// When peakLevel.fetched is false (Pass 2 output path) the value is fetched here
// as before; the resulting accumulator state is identical either way.
func (b *baseMetadataAccumulators) extractAstatsMetadata(metadata *ffmpeg.AVDictionary, peakLevel optionalFloat) {
	if value, ok := getFloatMetadata(metadata, metaKeyDynamicRange); ok {
		b.astatsDynamicRange = value
		b.astatsFound = true
	}
	if value, ok := getFloatMetadata(metadata, metaKeyRMSLevel); ok {
		b.astatsRMSLevel = value
	}
	if !peakLevel.fetched {
		peakLevel.value, peakLevel.ok = getFloatMetadata(metadata, metaKeyPeakLevel)
	}
	if peakLevel.ok {
		b.astatsPeakLevel = peakLevel.value
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
}

// getFloatMetadata extracts a float value from the metadata dictionary.
//
// The Pass 1 OnFrame path calls this about 40 times per frame. Reading the value
// via CStr.String() would copy each C string onto the Go heap (about 40
// short-lived allocations per frame). Instead we view the NUL-terminated C bytes
// as a Go string with no copy and parse that. strconv.ParseFloat does not retain
// the string past the call, so the no-copy view is safe; the dictionary owns the
// backing C memory and outlives this call. The parsed float64 is bit-identical to
// the String() path because both feed the same decimal bytes to ParseFloat.
func getFloatMetadata(metadata *ffmpeg.AVDictionary, key *ffmpeg.CStr) (float64, bool) {
	entry := ffmpeg.AVDictGet(metadata, key, nil, 0)
	if entry == nil {
		return 0.0, false
	}
	ptr := entry.Value().RawPtr()
	if ptr == nil {
		return 0.0, false
	}
	if value, err := strconv.ParseFloat(cStringNoCopy(ptr), 64); err == nil {
		return value, true
	}
	return 0.0, false
}

// cStringNoCopy returns the NUL-terminated C string at ptr as a Go string that
// aliases the C memory without copying. The caller must not retain the returned
// string beyond the lifetime of the C allocation, and must not mutate the bytes.
// maxMetadataValueLen bounds the NUL scan so a missing terminator cannot run off
// into unmapped memory; FFmpeg astats/ebur128/aspectralstats values are short
// decimal numbers far below this cap.
func cStringNoCopy(ptr unsafe.Pointer) string {
	const maxMetadataValueLen = 256
	bytes := unsafe.Slice((*byte)(ptr), maxMetadataValueLen)
	n := 0
	for n < maxMetadataValueLen && bytes[n] != 0 {
		n++
	}
	return unsafe.String((*byte)(ptr), n)
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
		absVal /= 32768.0
	}
	if absVal > 1.0 {
		absVal = 1.0 // Clamp to 0 dBFS max
	}
	return 20 * math.Log10(absVal)
}

// SpectralMetrics holds the 13 aspectralstats measurements extracted from FFmpeg metadata.
// These metrics characterise the frequency content of audio frames.
type SpectralMetrics struct {
	Mean     float64 `json:"mean"`        // Average spectral power
	Variance float64 `json:"variance"`    // Spectral variance
	Centroid float64 `json:"centroid_hz"` // Spectral centroid (Hz) - where energy is concentrated
	Spread   float64 `json:"spread_hz"`   // Spectral spread (Hz) - bandwidth/fullness indicator
	Skewness float64 `json:"skewness"`    // Spectral asymmetry - positive=bright, negative=dark
	Kurtosis float64 `json:"kurtosis"`    // Spectral peakiness - tonal vs broadband content
	Entropy  float64 `json:"entropy"`     // Spectral randomness (0-1) - noise classification
	Flatness float64 `json:"flatness"`    // Noise vs tonal ratio (0-1) - low=tonal, high=noisy
	Crest    float64 `json:"crest"`       // Spectral peak-to-RMS - transient indicator
	Flux     float64 `json:"flux"`        // Frame-to-frame spectral change
	Slope    float64 `json:"slope"`       // Spectral tilt - negative=more bass
	Decrease float64 `json:"decrease"`    // Average spectral decrease
	Rolloff  float64 `json:"rolloff_hz"`  // Spectral rolloff (Hz) - HF energy dropoff point
	Found    bool    `json:"-"`           // True if any spectral metric was extracted
}

// SpectralAccumulator accumulates spectral measurements across frames and
// averages only frames where aspectralstats metadata was found.
type SpectralAccumulator struct {
	sum   SpectralMetrics
	count int
}

// Add accumulates a found spectral measurement and ignores frames without
// aspectralstats metadata.
func (a *SpectralAccumulator) Add(spectral SpectralMetrics) {
	if !spectral.Found {
		return
	}
	a.sum.add(spectral)
	a.count++
}

// Average returns averaged spectral measurements, or the zero value when no
// spectral metadata was accumulated.
func (a SpectralAccumulator) Average() SpectralMetrics {
	if !a.Found() {
		return SpectralMetrics{}
	}
	average := a.sum.average(float64(a.count))
	average.Found = true
	return average
}

// Found reports whether at least one spectral frame was accumulated.
func (a SpectralAccumulator) Found() bool {
	return a.count > 0
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
// loudness carries the ebur128 quad and astats Peak_level pre-fetched once in
// the OnFrame callback (see frameLoudnessMetrics). Missing keys map to the same
// zero defaults the original per-key fetches produced.
func extractIntervalFrameMetrics(spectral SpectralMetrics, loudness frameLoudnessMetrics) intervalFrameMetrics {
	var m intervalFrameMetrics

	// Peak level from astats (used for max tracking, which is valid per-interval)
	// Pre-fetched; missing key yields zero, matching the original "_, _" fetch.
	m.PeakLevel = loudness.peakLevel

	// aspectralstats metrics (valid per-window measurements, pre-extracted by caller)
	m.Spectral = spectral

	// ebur128 windowed measurements (pre-fetched; missing key yields zero)
	m.MomentaryLUFS = loudness.momentary
	m.ShortTermLUFS = loudness.shortTerm

	// ebur128 peak values are linear ratios, convert to dB
	if loudness.truePeakFound {
		m.TruePeak = linearRatioToDB(loudness.truePeak)
	}
	if loudness.samplePeakFound {
		m.SamplePeak = linearRatioToDB(loudness.samplePeak)
	}

	return m
}

// extractFrameMetadata extracts audio analysis metadata from a filtered frame.
// Updates accumulators with spectral, astats, and ebur128 measurements.
// Called from both the main processing loop and the flush loop.
// loudness carries the ebur128 quad and astats Peak_level pre-fetched once in
// the OnFrame callback so this function and extractIntervalFrameMetrics share a
// single fetch per key. Each pre-fetched key reproduces the original "set on
// found, skip on missing" accumulator semantics via its found flag.
func extractFrameMetadata(metadata *ffmpeg.AVDictionary, acc *metadataAccumulators, spectral SpectralMetrics, loudness frameLoudnessMetrics) {
	if metadata == nil {
		return
	}

	// Accumulate pre-extracted spectral metrics (averaged across frames)
	// For mono audio, spectral stats are under channel .1
	acc.accumulateSpectral(spectral)

	// Extract astats measurements (cumulative, so we keep the latest)
	// For mono audio, stats are under channel .1
	// Peak_level is pre-fetched in the OnFrame callback and shared here.
	acc.extractAstatsMetadata(metadata, optionalFloat{value: loudness.peakLevel, ok: loudness.peakLevelFound, fetched: true})

	// Extract ebur128 measurements (cumulative loudness analysis)
	// ebur128 provides: M (momentary 400ms), S (short-term 3s), I (integrated), LRA, sample_peak, true_peak
	// We need these for loudness normalization and interval-based analysis
	if value, ok := getFloatMetadata(metadata, metaKeyEbur128I); ok {
		acc.ebur128InputI = value
		acc.ebur128Found = true
	}

	// Momentary loudness (400ms window) - useful for interval-based silence detection
	if loudness.momentaryFound {
		acc.ebur128InputM = loudness.momentary
	}

	// Short-term loudness (3s window)
	if loudness.shortTermFound {
		acc.ebur128InputS = loudness.shortTerm
	}

	if loudness.truePeakFound {
		acc.ebur128InputTP = linearRatioToDB(loudness.truePeak)
	}

	if loudness.samplePeakFound {
		acc.ebur128InputSP = linearRatioToDB(loudness.samplePeak)
	}

	if value, ok := getFloatMetadata(metadata, metaKeyEbur128LRA); ok {
		acc.ebur128InputLRA = value
	}
}

// outputMetadataAccumulators holds accumulator variables for Pass 2 output measurement extraction.
// Uses baseMetadataAccumulators for spectral and astats fields shared with input analysis.
// Mirrors metadataAccumulators but without room tone detection fields.
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
	// Peak_level is not pre-fetched on the output path; extractAstatsMetadata fetches it.
	acc.extractAstatsMetadata(metadata, optionalFloat{})

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
	if !acc.ebur128Found && !acc.astatsFound && !acc.spectral.Found() {
		return nil // No measurements captured
	}

	m := &OutputMeasurements{
		Spectral: acc.finalizeSpectral(),
		Loudness: OutputLoudnessMetrics{
			LoudnessMetrics: LoudnessMetrics{
				// ebur128 momentary/short-term loudness
				MomentaryLoudness: acc.ebur128OutputM,
				ShortTermLoudness: acc.ebur128OutputS,
				SamplePeak:        acc.ebur128OutputSP,
			},
			// Output-specific loudness measurements
			OutputI:      acc.ebur128OutputI,
			OutputTP:     acc.ebur128OutputTP,
			OutputLRA:    acc.ebur128OutputLRA,
			OutputThresh: acc.ebur128OutputThresh,
			TargetOffset: 0.0, // Will be calculated in Pass 3
		},
		Dynamics: DynamicsMetrics{
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
			NoiseFloorCount:   acc.astatsNoiseFloorCount,
			BitDepth:          acc.astatsBitDepth,
			NumberOfSamples:   acc.astatsNumberOfSamples,
		},
	}

	// If ebur128 target_threshold metadata is missing, calculate it manually
	// according to EBU R128 standard: gating threshold = integrated loudness - 10 LU
	if m.Loudness.OutputThresh == 0.0 && m.Loudness.OutputI != 0.0 {
		m.Loudness.OutputThresh = m.Loudness.OutputI - 10.0
	}

	return m
}
