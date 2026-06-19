package ui

import (
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// AdaptedSummary is the presentation-only view-model behind the two filter-chain
// status boxes (Filter Chain + Analysis). It is derived from the per-file
// EffectiveFilterConfig, AdaptiveDiagnostics and AudioMeasurements after Pass 1 /
// AdaptConfig, with the limiter ceiling filled in at completion. It holds resolved
// display values only, never live signal: Pass 2 is one FFmpeg invocation, so the
// values do not change within a pass. Built in the pool, carried to the model via
// AdaptedSummaryMsg, and rendered without touching the processor.
type AdaptedSummary struct {
	// ChainReady is set once the chain + analysis rows are known (post-AdaptConfig,
	// Pass 2 start). Until then the boxes render dim/pending.
	ChainReady bool

	// Filter Chain rows.
	DownmixMono  bool    // downmix to mono enabled
	SampleRate   int     // output sample rate (Hz), e.g. 44100
	HighPassHz   float64 // Rumble high-pass corner (Hz)
	LowPassHz    float64 // Band-limit low-pass corner (Hz)
	DenoiseNLM   bool    // anlmdn stage active
	DenoiseFFT   bool    // afftdn stage active
	GateThreshDB float64 // Speech gate threshold (dB, from linear)
	CompThreshDB float64 // Levelling compressor adapted threshold (dB)
	DeesserOn    bool    // de-esser engaged (Intensity > 0)
	DeesserI     float64 // de-esser intensity

	// LimiterReady is set at completion when the limiter ceiling is known. Until
	// then the Limiter row renders pending (⋯).
	LimiterReady   bool    // completion data has arrived (limiter no longer pending)
	LimiterEnabled bool    // pre-limiting applied
	LimiterCeiling float64 // ceiling (dBTP), valid only when LimiterEnabled

	// Analysis rows.
	HasSpeech     bool    // a SpeechProfile was elected (Voice avg available)
	VoiceAvgDB    float64 // SpeechProfile RMS (dBFS)
	HasNoiseFloor bool    // an input room-tone floor was measured (NoiseFloorDB valid)
	NoiseFloorDB  float64 // input room-tone RMS floor (astats dBFS); matches the done-box "before"
	SeparationDB  float64 // voice / noise separation (dB) = VoiceAvgDB - NoiseFloorDB, same axis
	InputLRA      float64 // input loudness range (LU)
	GateRatio     float64 // Speech gate ratio (x:1)
	TruePeakDBTP  float64 // input true peak (dBTP)
	HasSibilance  bool    // speech bands measured (sibilance available)
	SibilanceDB   float64 // SibBandRMS - BodyBandRMS (dB)
	GateDepthDB   float64 // Speech gate attenuation depth (positive dB)
	InputLUFS     float64 // input integrated loudness (LUFS)
}

// NewAdaptedSummary builds the chain + analysis portion of the summary from the
// post-AdaptConfig state. It reads the effective config, diagnostics and Pass-1
// measurements; it performs no measurement and mutates none of them. The limiter
// portion is filled later via WithLimiter at completion.
func NewAdaptedSummary(cfg *processor.EffectiveFilterConfig, diag *processor.AdaptiveDiagnostics, m *processor.AudioMeasurements) AdaptedSummary {
	s := AdaptedSummary{ChainReady: true}
	if cfg == nil || m == nil {
		// Defensive: without config or measurements there is nothing to light. Leave
		// ChainReady false so the boxes stay pending rather than show zeroed rows.
		s.ChainReady = false
		return s
	}

	// Filter Chain.
	s.DownmixMono = cfg.Downmix.Enabled
	s.SampleRate = cfg.Resample.SampleRate
	s.HighPassHz = cfg.RumbleHighPass.Frequency
	s.LowPassHz = cfg.BandlimitLowPass.Frequency
	s.DenoiseNLM = cfg.NoiseReduction.Enabled
	s.DenoiseFFT = cfg.NoiseReduction.AfftdnEnabled
	s.GateThreshDB = processor.LinearToDb(cfg.SpeechGate.Threshold)
	s.CompThreshDB = cfg.LevellingCompressor.Threshold
	s.DeesserI = cfg.Deesser.Intensity
	s.DeesserOn = cfg.Deesser.Intensity > 0

	// Analysis. Noise floor is the input room-tone RMS (astats dBFS), the same
	// canonical value the done-box "before" shows (processor.InputRoomToneFloorDB),
	// NOT the K-weighted momentary-LUFS VAD floor (m.Noise.Floor); the latter stays
	// internal (VAD split, Recording score, afftdn seed). Sourcing both surfaces
	// from one resolver guarantees live-box == done-box for a given file.
	s.NoiseFloorDB, s.HasNoiseFloor = processor.InputRoomToneFloorDB(m)
	s.InputLRA = m.Loudness.InputLRA
	s.GateRatio = cfg.SpeechGate.Ratio
	s.TruePeakDBTP = m.Loudness.InputTP
	s.InputLUFS = m.Loudness.InputI

	if sp := m.Regions.SpeechProfile; sp != nil {
		s.HasSpeech = true
		s.VoiceAvgDB = sp.RMSLevel
		// SNR Gap on one axis: speech RMS minus the input room-tone RMS floor, so
		// the number and the separation bar agree and there is no axis mix. A
		// separation against an absent floor is meaningless, so it needs BOTH a
		// SpeechProfile and a measured floor; the renderer gates the row on the
		// same pair (HasSpeech && HasNoiseFloor).
		if s.HasNoiseFloor {
			s.SeparationDB = s.VoiceAvgDB - s.NoiseFloorDB
		}
		if sp.BandsMeasured {
			s.HasSibilance = true
			// Source the same band excess the de-esser uses, so box and report
			// never drift (processor.SpeechCandidateMetrics.SibilanceExcessDB).
			s.SibilanceDB = sp.SibilanceExcessDB()
		}
	}

	if diag != nil {
		s.GateDepthDB = diag.SpeechGateDepthDB
	}

	return s
}

// WithLimiter fills in the limiter portion from the completion NormResult and
// returns the updated summary. A nil normResult marks the limiter known but
// disabled (no normalisation ran).
func (s AdaptedSummary) WithLimiter(normResult *processor.NormalisationResult) AdaptedSummary {
	s.LimiterReady = true
	if normResult != nil {
		s.LimiterEnabled = normResult.LimiterEnabled
		s.LimiterCeiling = normResult.LimiterCeiling
	}
	return s
}

// WithLimiterProgress fills in the limiter portion from the Pass-4-start snapshot
// and returns the updated summary. This lights the Limiter row WHILE the file is
// still processing, the moment the ceiling is computed, rather than at completion.
// A nil snapshot marks the limiter known but disabled. The ceiling carried here is
// the same value the final NormResult reports, so WithLimiter at completion is a
// harmless confirming no-op.
func (s AdaptedSummary) WithLimiterProgress(p *processor.LimiterProgress) AdaptedSummary {
	s.LimiterReady = true
	if p != nil {
		s.LimiterEnabled = p.Enabled
		s.LimiterCeiling = p.Ceiling
	}
	return s
}
