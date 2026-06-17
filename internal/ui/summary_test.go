package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
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
	m.Noise.Floor = -85 // internal momentary-LUFS floor; must NOT drive the display
	m.Regions.ElectedRoomToneSample = &processor.RegionSample{RMSLevel: -68}
	m.Loudness.InputLRA = 8.2
	m.Loudness.InputTP = -3.2
	m.Loudness.InputI = -24.3
	m.Regions.SpeechProfile = &processor.SpeechCandidateMetrics{
		BodyBandRMS:   -30,
		SibBandRMS:    -34,
		BandsMeasured: true,
	}
	m.Regions.SpeechProfile.RMSLevel = -20.9

	diag := &processor.AdaptiveDiagnostics{SpeechGateDepthDB: 14}

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
	// Displayed floor is the room-tone RMS (-68), not the momentary-LUFS floor (-85).
	if s.NoiseFloorDB != -68 {
		t.Errorf("noise floor = %v, want -68 (input room-tone RMS, not momentary-LUFS)", s.NoiseFloorDB)
	}
	if s.SeparationDB != -20.9-(-68) {
		t.Errorf("separation = %v, want %v", s.SeparationDB, -20.9-(-68.0))
	}
	if !s.HasSibilance || s.SibilanceDB != -34-(-30) {
		t.Errorf("sibilance mapping wrong: has=%v db=%v (want %v)", s.HasSibilance, s.SibilanceDB, -34.0-(-30.0))
	}
	if s.GateDepthDB != 14 {
		t.Errorf("gate depth should follow the diagnostic: got %v, want 14", s.GateDepthDB)
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
	m.Noise.Floor = -85 // internal floor; must not appear in the box
	m.Regions.ElectedRoomToneSample = &processor.RegionSample{RMSLevel: -60}

	s := NewAdaptedSummary(cfg, nil, m)

	if !s.ChainReady {
		t.Fatal("chain should be ready even without speech")
	}
	if s.HasSpeech || s.HasSibilance {
		t.Errorf("no SpeechProfile should leave speech rows unavailable")
	}
	// Floor is the elected room-tone RMS (-60), the astats-RMS axis.
	if s.NoiseFloorDB != -60 {
		t.Errorf("noise floor should map to room-tone RMS: %v, want -60", s.NoiseFloorDB)
	}
}

// TestLiveBoxFloorMatchesDoneBoxFloor locks the bug-fix invariant: the live
// Analysis box "Noise floor" (summary NoiseFloorDB) equals the done-box "before"
// (processor.InputNoiseFloor) for the same measurements, on the astats RMS axis,
// never the internal momentary-LUFS floor. Both surfaces read the one resolver,
// so when an elected sample exists they agree on its value, and when it is
// absent they both report no floor (neither leaks the momentary-LUFS field).
func TestLiveBoxFloorMatchesDoneBoxFloor(t *testing.T) {
	cases := []struct {
		name      string
		m         *processor.AudioMeasurements
		wantFloor bool
	}{
		{
			name: "elected room-tone sample",
			m: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.Noise.Floor = -85 // internal; must be ignored by both surfaces
				m.Regions.ElectedRoomToneSample = &processor.RegionSample{RMSLevel: -73}
				m.Regions.NoiseProfile = &processor.NoiseProfile{MeasuredNoiseFloor: -73}
				return m
			}(),
			wantFloor: true,
		},
		{
			name: "no elected sample, momentary-LUFS field present",
			m: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.Noise.Floor = -85
				// Only the momentary-LUFS field is set; neither surface may use it.
				m.Regions.NoiseProfile = &processor.NoiseProfile{MeasuredNoiseFloor: -70}
				return m
			}(),
			wantFloor: false,
		},
	}

	cfg := &processor.EffectiveFilterConfig{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			live := NewAdaptedSummary(cfg, nil, tc.m)
			done, ok := processor.InputNoiseFloor(&processor.ProcessingResult{Measurements: tc.m})
			if ok != tc.wantFloor {
				t.Fatalf("done-box InputNoiseFloor: ok = %v, want %v", ok, tc.wantFloor)
			}
			// The live box must carry the same measured/unmeasured state the
			// done-box derives from the one resolver.
			if live.HasNoiseFloor != ok {
				t.Fatalf("live-box HasNoiseFloor = %v, want %v (must match done-box)", live.HasNoiseFloor, ok)
			}
			if ok && live.NoiseFloorDB != done {
				t.Errorf("live-box floor %v != done-box floor %v (must be identical)", live.NoiseFloorDB, done)
			}
			if live.NoiseFloorDB == tc.m.Noise.Floor {
				t.Errorf("live-box floor used the internal momentary-LUFS floor %v", tc.m.Noise.Floor)
			}

			plain := ansi.Strip(renderAnalysisBox(live, 0))
			if ok {
				if !strings.Contains(plain, fmt.Sprintf("%.0f %s", done, unitDB)) {
					t.Errorf("measured floor should render its value:\n%s", plain)
				}
			} else {
				// Unmeasured: live box matches the done-box "n/a" convention and
				// never shows a plausible-looking 0 dB or a separation row.
				if !strings.Contains(plain, "Noise floor  n/a") {
					t.Errorf("unmeasured floor should render n/a (done-box convention):\n%s", plain)
				}
				if strings.Contains(plain, "Noise floor  0 "+unitDB) {
					t.Errorf("unmeasured floor must not render a bogus 0 dB:\n%s", plain)
				}
				done := doneBoxNoiseFloorRow(live.NoiseFloorDB, 0, live.HasNoiseFloor, false)
				if done != "n/a" {
					t.Errorf("done-box renders %q for the same unmeasured floor; live box must match its n/a state", done)
				}
			}
		})
	}
}

// TestUnmeasuredFloorNoSeparation confirms that with a SpeechProfile but no
// measured floor the separation is neither computed nor rendered: a gap against
// an absent floor is meaningless, so SeparationDB stays zero and the SNR Gap row
// shows the dim placeholder, not a bogus number.
func TestUnmeasuredFloorNoSeparation(t *testing.T) {
	m := &processor.AudioMeasurements{}
	m.Noise.Floor = -85 // internal; must not leak into the gap
	m.Regions.SpeechProfile = &processor.SpeechCandidateMetrics{}
	m.Regions.SpeechProfile.RMSLevel = -22 // voice present, floor absent

	s := NewAdaptedSummary(&processor.EffectiveFilterConfig{}, nil, m)

	if s.HasNoiseFloor {
		t.Fatal("HasNoiseFloor should be false with no elected room-tone sample")
	}
	if s.SeparationDB != 0 {
		t.Errorf("SeparationDB = %v, want 0 (no floor, no gap)", s.SeparationDB)
	}

	plain := ansi.Strip(renderAnalysisBox(s, 0))
	if !strings.Contains(plain, "SNR Gap") || !strings.Contains(plain, valuePending) {
		t.Errorf("SNR Gap should show the dim placeholder without a measured floor:\n%s", plain)
	}
}

// TestSeparationDBSameAxis confirms SNR Gap is VoiceAvgDB - NoiseFloorDB on one
// axis (both astats RMS), so the SNR Gap number and its bar agree and there is no
// momentary-LUFS mix.
func TestSeparationDBSameAxis(t *testing.T) {
	m := &processor.AudioMeasurements{}
	m.Noise.Floor = -85 // internal; must not enter the separation maths
	m.Regions.ElectedRoomToneSample = &processor.RegionSample{RMSLevel: -70}
	m.Regions.SpeechProfile = &processor.SpeechCandidateMetrics{}
	m.Regions.SpeechProfile.RMSLevel = -22

	s := NewAdaptedSummary(&processor.EffectiveFilterConfig{}, nil, m)

	if s.SeparationDB != s.VoiceAvgDB-s.NoiseFloorDB {
		t.Errorf("SeparationDB = %v, want VoiceAvgDB - NoiseFloorDB = %v", s.SeparationDB, s.VoiceAvgDB-s.NoiseFloorDB)
	}
	if s.SeparationDB != -22-(-70) {
		t.Errorf("SeparationDB = %v, want %v (same-axis RMS)", s.SeparationDB, -22-(-70.0))
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
