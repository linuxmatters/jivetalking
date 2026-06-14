package ui

import (
	"testing"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// TestNewAdaptedSummaryFromConfig builds the summary from in-memory processor
// types (no audio) and confirms it maps the effective config, diagnostics and
// measurements onto the view-model exactly as the boxes expect.
func TestNewAdaptedSummaryFromConfig(t *testing.T) {
	cfg := &processor.EffectiveFilterConfig{}
	cfg.Downmix.Enabled = true
	cfg.Resample.SampleRate = 44100
	cfg.RumbleHighPass.Frequency = 80
	cfg.BandlimitLowPass.Frequency = 20500
	cfg.NoiseReduction.Enabled = true
	cfg.NoiseReduction.AfftdnEnabled = true
	cfg.SpeechGate.Threshold = 0.0078 // linear ≈ -42.1 dB
	cfg.SpeechGate.Ratio = 2.0
	cfg.LevellingCompressor.Threshold = -11.9
	cfg.Deesser.Intensity = 0

	m := &processor.AudioMeasurements{}
	m.Noise.Floor = -68
	m.Loudness.InputLRA = 8.2
	m.Loudness.InputTP = -3.2
	m.Loudness.InputI = -24.3
	m.Regions.SpeechProfile = &processor.SpeechCandidateMetrics{
		BodyBandRMS:   -30,
		SibBandRMS:    -34,
		BandsMeasured: true,
	}
	m.Regions.SpeechProfile.RMSLevel = -20.9

	diag := &processor.AdaptiveDiagnostics{SpeechGateGentleMode: true}

	s := NewAdaptedSummary(cfg, diag, m)

	if !s.ChainReady {
		t.Fatal("ChainReady should be set when config + measurements present")
	}
	if !s.DownmixMono || s.SampleRate != 44100 {
		t.Errorf("mix mapping wrong: mono=%v rate=%d", s.DownmixMono, s.SampleRate)
	}
	if s.HighPassHz != 80 || s.LowPassHz != 20500 {
		t.Errorf("HP/LP mapping wrong: hp=%v lp=%v", s.HighPassHz, s.LowPassHz)
	}
	if !s.DenoiseNLM || !s.DenoiseFFT {
		t.Errorf("denoise mapping wrong: nlm=%v fft=%v", s.DenoiseNLM, s.DenoiseFFT)
	}
	if got := s.GateThreshDB; got > -41 || got < -43 {
		t.Errorf("gate threshold dB out of range: %v (want ≈ -42.1)", got)
	}
	if s.CompThreshDB != -11.9 {
		t.Errorf("comp threshold = %v, want -11.9", s.CompThreshDB)
	}
	if s.DeesserOn {
		t.Errorf("de-esser should be OFF at intensity 0")
	}
	if !s.HasSpeech || s.VoiceAvgDB != -20.9 {
		t.Errorf("voice avg mapping wrong: hasSpeech=%v avg=%v", s.HasSpeech, s.VoiceAvgDB)
	}
	if s.SeparationDB != -20.9-(-68) {
		t.Errorf("separation = %v, want %v", s.SeparationDB, -20.9-(-68.0))
	}
	if !s.HasSibilance || s.SibilanceDB != -34-(-30) {
		t.Errorf("sibilance mapping wrong: has=%v db=%v (want %v)", s.HasSibilance, s.SibilanceDB, -34.0-(-30.0))
	}
	if !s.GentleMode {
		t.Errorf("gentle mode should follow the diagnostic")
	}
	if s.InputLUFS != -24.3 {
		t.Errorf("loudness mapping wrong: in=%v", s.InputLUFS)
	}
	// Limiter not yet known.
	if s.LimiterReady {
		t.Errorf("limiter should be pending before WithLimiter")
	}
}

// TestNewAdaptedSummaryNoSpeech confirms that without a SpeechProfile the
// speech-dependent fields stay unset (so the box dims them) while the chain still
// lights.
func TestNewAdaptedSummaryNoSpeech(t *testing.T) {
	cfg := &processor.EffectiveFilterConfig{}
	cfg.Resample.SampleRate = 48000
	m := &processor.AudioMeasurements{}
	m.Noise.Floor = -60

	s := NewAdaptedSummary(cfg, nil, m)

	if !s.ChainReady {
		t.Fatal("chain should be ready even without speech")
	}
	if s.HasSpeech || s.HasSibilance {
		t.Errorf("no SpeechProfile should leave speech rows unavailable")
	}
	if s.NoiseFloorDB != -60 {
		t.Errorf("noise floor should still map: %v", s.NoiseFloorDB)
	}
}

// TestNewAdaptedSummaryNilGuards confirms nil config or measurements yields a
// not-ready summary (boxes stay pending) rather than panicking.
func TestNewAdaptedSummaryNilGuards(t *testing.T) {
	if NewAdaptedSummary(nil, nil, &processor.AudioMeasurements{}).ChainReady {
		t.Errorf("nil config should not be chain-ready")
	}
	if NewAdaptedSummary(&processor.EffectiveFilterConfig{}, nil, nil).ChainReady {
		t.Errorf("nil measurements should not be chain-ready")
	}
}

// TestWithLimiter confirms the limiter merge keeps the existing chain rows and
// fills the ceiling, and that a nil NormResult marks the limiter known-disabled.
func TestWithLimiter(t *testing.T) {
	base := litSummary()

	enabled := base.WithLimiter(&processor.NormalisationResult{LimiterDiagnostics: processor.LimiterDiagnostics{LimiterEnabled: true, LimiterCeiling: -2.8}})
	if !enabled.LimiterReady || !enabled.LimiterEnabled || enabled.LimiterCeiling != -2.8 {
		t.Errorf("WithLimiter(enabled) wrong: %+v", enabled)
	}
	// Chain rows preserved.
	if enabled.GateThreshDB != base.GateThreshDB || enabled.VoiceAvgDB != base.VoiceAvgDB {
		t.Errorf("WithLimiter must preserve chain + analysis rows")
	}

	disabled := base.WithLimiter(nil)
	if !disabled.LimiterReady || disabled.LimiterEnabled {
		t.Errorf("WithLimiter(nil) should mark the limiter known-disabled: %+v", disabled)
	}
}

// TestWithLimiterProgress confirms the Pass-4 limiter snapshot resolves the
// Limiter row (ceiling or OFF) while preserving chain + analysis rows. This is the
// path that lights the row DURING processing, not at completion.
func TestWithLimiterProgress(t *testing.T) {
	base := litSummary()

	enabled := base.WithLimiterProgress(&processor.LimiterProgress{Enabled: true, Ceiling: -2.8})
	if !enabled.LimiterReady || !enabled.LimiterEnabled || enabled.LimiterCeiling != -2.8 {
		t.Errorf("WithLimiterProgress(enabled) wrong: %+v", enabled)
	}
	if enabled.GateThreshDB != base.GateThreshDB || enabled.VoiceAvgDB != base.VoiceAvgDB {
		t.Errorf("WithLimiterProgress must preserve chain + analysis rows")
	}

	off := base.WithLimiterProgress(&processor.LimiterProgress{Enabled: false})
	if !off.LimiterReady || off.LimiterEnabled {
		t.Errorf("WithLimiterProgress(disabled) should mark the limiter known-disabled: %+v", off)
	}

	nilSnap := base.WithLimiterProgress(nil)
	if !nilSnap.LimiterReady || nilSnap.LimiterEnabled {
		t.Errorf("WithLimiterProgress(nil) should mark the limiter known-disabled: %+v", nilSnap)
	}
}

// TestAdaptedSummaryMsgUpdate confirms the model stores the summary from
// AdaptedSummaryMsg, routed by FileIndex, and ignores out-of-range indices.
func TestAdaptedSummaryMsgUpdate(t *testing.T) {
	m := NewModel([]string{"a.wav", "b.wav"})

	updated, _ := m.Update(AdaptedSummaryMsg{FileIndex: 1, Summary: litSummary()})
	m = updated.(Model)

	if !m.Files[1].Summary.ChainReady {
		t.Errorf("summary not stored for file 1")
	}
	if m.Files[0].Summary.ChainReady {
		t.Errorf("file 0 summary should be untouched")
	}

	// Out-of-range index is a no-op (no panic).
	updated, _ = m.Update(AdaptedSummaryMsg{FileIndex: 99, Summary: litSummary()})
	_ = updated.(Model)
}
