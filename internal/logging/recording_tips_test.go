package logging

import (
	"strings"
	"testing"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

func TestWrapText(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxWidth int
		indent   string
		want     string
	}{
		{
			name:     "short_text_no_wrap",
			text:     "Hello world",
			maxWidth: 20,
			indent:   "  ",
			want:     "Hello world",
		},
		{
			name:     "long_text_wraps",
			text:     "Try moving closer to your microphone for better results",
			maxWidth: 30,
			indent:   "  ",
			want:     "Try moving closer to your\n  microphone for better results",
		},
		{
			name:     "single_long_word",
			text:     "supercalifragilisticexpialidocious",
			maxWidth: 10,
			indent:   "  ",
			want:     "supercalifragilisticexpialidocious",
		},
		{
			name:     "empty_input",
			text:     "",
			maxWidth: 20,
			indent:   "  ",
			want:     "",
		},
		{
			name:     "exact_fit",
			text:     "exactly twenty chars",
			maxWidth: 20,
			indent:   "  ",
			want:     "exactly twenty chars",
		},
		{
			name:     "multiple_wraps",
			text:     "one two three four five six seven eight nine ten",
			maxWidth: 15,
			indent:   "    ",
			want:     "one two three\n    four five six\n    seven eight\n    nine ten",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := wrapText(tt.text, tt.maxWidth, tt.indent)
			if got != tt.want {
				t.Errorf("wrapText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTipLevelTooQuiet(t *testing.T) {
	tests := []struct {
		name          string
		inputI        float64
		inputTP       float64
		speechProfile *processor.SpeechCandidateMetrics
		wantTip       bool
		wantRuleID    string
		wantGain      string // substring to check in message, empty to skip
	}{
		// InputI fallback (no SpeechProfile)
		{"very quiet -35 LUFS fallback", -35.0, -20.0, nil, true, "level_too_quiet", "17 dB"},
		{"boundary -30 LUFS fallback", -30.0, -20.0, nil, false, "", ""},
		{"moderately quiet -28 LUFS fallback", -28.0, -20.0, nil, false, "", ""},
		{"normal -20 LUFS fallback", -20.0, -20.0, nil, false, "", ""},
		// Speech-aware path
		{
			name:          "speech RMS too quiet -45 dBFS",
			inputI:        -20.0, // InputI would not trigger
			inputTP:       -30.0,
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -45.0},
			wantTip:       true,
			wantRuleID:    "level_too_quiet",
			wantGain:      "21 dB", // -24.0 - (-45.0) = 21
		},
		{
			name:          "speech RMS at boundary -42 dBFS no tip",
			inputI:        -35.0, // InputI would trigger fallback
			inputTP:       -20.0,
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -42.0},
			wantTip:       false,
		},
		{
			name:          "speech RMS acceptable -38 dBFS suppresses InputI",
			inputI:        -35.0, // InputI would trigger fallback
			inputTP:       -20.0,
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -38.0},
			wantTip:       false,
		},
		{
			name:          "speech RMS good -30 dBFS no tip",
			inputI:        -35.0,
			inputTP:       -20.0,
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -30.0},
			wantTip:       false,
		},
		// Gain clamping: peaks near ceiling
		{
			name:       "gain clamped by peak headroom",
			inputI:     -35.0,
			inputTP:    -4.0,
			wantTip:    true,
			wantRuleID: "level_too_quiet",
			wantGain:   "3 dB", // maxSafeGain = -1.0 - (-4.0) = 3.0, gainNeeded = 17 clamped to 3
		},
		{
			name:       "gain clamped with accounting note",
			inputI:     -35.0,
			inputTP:    -6.0,
			wantTip:    true,
			wantRuleID: "level_too_quiet",
			wantGain:   "accounting for", // clamped from 17 to 5
		},
		{
			name:       "peaks near ceiling switches to crest message",
			inputI:     -35.0,
			inputTP:    -0.5,
			wantTip:    true,
			wantRuleID: "level_too_quiet",
			wantGain:   "plosives", // maxSafeGain = -1.0 - (-0.5) = -0.5, < 2.0 → crest message
		},
		{
			name:          "speech RMS clamped by peak headroom",
			inputI:        -20.0,
			inputTP:       -4.0,
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -45.0},
			wantTip:       true,
			wantRuleID:    "level_too_quiet",
			wantGain:      "3 dB", // gainNeeded=21, maxSafeGain=3, clamped to 3
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.InputI = tt.inputI
			m.InputTP = tt.inputTP
			m.SpeechProfile = tt.speechProfile
			tip := tipLevelTooQuiet(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipLevelTooQuiet() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil {
				if tip.RuleID != tt.wantRuleID {
					t.Errorf("RuleID = %q, want %q", tip.RuleID, tt.wantRuleID)
				}
				if tt.wantGain != "" && !strings.Contains(tip.Message, tt.wantGain) {
					t.Errorf("Message %q should contain %q", tip.Message, tt.wantGain)
				}
			}
		})
	}
}

func TestTipLevelQuiet(t *testing.T) {
	tests := []struct {
		name          string
		inputI        float64
		inputTP       float64
		speechProfile *processor.SpeechCandidateMetrics
		wantTip       bool
		wantRuleID    string
		wantGain      string
	}{
		// InputI fallback (no SpeechProfile)
		{"very quiet handled by too_quiet fallback", -35.0, -30.0, nil, false, "", ""},
		{"boundary -30 LUFS triggers quiet fallback", -30.0, -30.0, nil, true, "level_quiet", "12 dB"},
		{"moderately quiet -28 LUFS fallback", -28.0, -30.0, nil, true, "level_quiet", "10 dB"},
		{"boundary -24 LUFS no tip fallback", -24.0, -30.0, nil, false, "", ""},
		{"normal -20 LUFS fallback", -20.0, -30.0, nil, false, "", ""},
		// Speech-aware path
		{
			name:          "speech RMS too quiet for level_quiet handled by too_quiet",
			inputI:        -20.0,
			inputTP:       -30.0,
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -45.0},
			wantTip:       false, // < -42 is level_too_quiet territory
		},
		{
			name:          "speech RMS moderately quiet -40 dBFS",
			inputI:        -20.0, // InputI would not trigger
			inputTP:       -30.0,
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -40.0},
			wantTip:       true,
			wantRuleID:    "level_quiet",
			wantGain:      "16 dB", // -24.0 - (-40.0) = 16
		},
		{
			name:          "speech RMS at boundary -42 dBFS triggers quiet",
			inputI:        -20.0,
			inputTP:       -30.0,
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -42.0},
			wantTip:       true,
			wantRuleID:    "level_quiet",
			wantGain:      "18 dB", // -24.0 - (-42.0) = 18
		},
		{
			name:          "speech RMS at boundary -36 dBFS no tip",
			inputI:        -28.0, // InputI would trigger fallback
			inputTP:       -30.0,
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -36.0},
			wantTip:       false,
		},
		{
			name:          "speech RMS acceptable -34 dBFS suppresses InputI",
			inputI:        -28.0, // InputI would trigger fallback
			inputTP:       -30.0,
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -34.0},
			wantTip:       false,
		},
		// Gain clamping: peaks near ceiling
		{
			name:       "gain clamped by peak headroom",
			inputI:     -28.0,
			inputTP:    -4.0,
			wantTip:    true,
			wantRuleID: "level_quiet",
			wantGain:   "3 dB", // maxSafeGain = 3.0, gainNeeded = 10 clamped to 3
		},
		{
			name:       "peaks near ceiling switches to crest message",
			inputI:     -28.0,
			inputTP:    -0.5,
			wantTip:    true,
			wantRuleID: "level_quiet",
			wantGain:   "plosives", // maxSafeGain = -1.0 - (-0.5) = -0.5, < 2.0 → crest message
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.InputI = tt.inputI
			m.InputTP = tt.inputTP
			m.SpeechProfile = tt.speechProfile
			tip := tipLevelQuiet(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipLevelQuiet() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil {
				if tip.RuleID != tt.wantRuleID {
					t.Errorf("RuleID = %q, want %q", tip.RuleID, tt.wantRuleID)
				}
				if tt.wantGain != "" && !strings.Contains(tip.Message, tt.wantGain) {
					t.Errorf("Message %q should contain %q", tip.Message, tt.wantGain)
				}
			}
		})
	}
}

func TestTipLevelTooHot(t *testing.T) {
	tests := []struct {
		name       string
		inputTP    float64
		inputI     float64
		wantTip    bool
		wantRuleID string
		wantMsg    string // substring to check (empty to skip)
	}{
		{"clipping +0.5 dBTP", 0.5, -18.0, true, "level_clipping", ""},
		{"boundary 0.0 dBTP near clipping", 0.0, -18.0, true, "level_near_clipping", ""},
		{"near clipping -0.5 dBTP", -0.5, -18.0, true, "level_near_clipping", ""},
		{"boundary -1.0 dBTP no tip", -1.0, -18.0, false, "", ""},
		{"safe -3.0 dBTP", -3.0, -18.0, false, "", ""},
		// Context-aware clipping tests
		{
			name:       "clipping while very quiet compound message",
			inputTP:    0.5,
			inputI:     -35.0,
			wantTip:    true,
			wantRuleID: "level_clipping",
			wantMsg:    "otherwise very quiet",
		},
		{
			name:       "clipping with normal level computed reduction",
			inputTP:    2.0,
			inputI:     -18.0,
			wantTip:    true,
			wantRuleID: "level_clipping",
			wantMsg:    "5 dB", // reduction = 2.0 + 3.0 = 5.0
		},
		{
			name:       "near clipping small reduction says slightly",
			inputTP:    -0.8,
			inputI:     -18.0,
			wantTip:    true,
			wantRuleID: "level_near_clipping",
			wantMsg:    "slightly", // reduction = -0.8 + 3.0 = 2.2 < 3.0
		},
		{
			name:       "near clipping larger reduction gives number",
			inputTP:    0.0,
			inputI:     -18.0,
			wantTip:    true,
			wantRuleID: "level_near_clipping",
			wantMsg:    "3 dB", // reduction = 0.0 + 3.0 = 3.0
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.InputTP = tt.inputTP
			m.InputI = tt.inputI
			tip := tipLevelTooHot(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipLevelTooHot() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil {
				if tip.RuleID != tt.wantRuleID {
					t.Errorf("RuleID = %q, want %q", tip.RuleID, tt.wantRuleID)
				}
				if tt.wantMsg != "" && !strings.Contains(tip.Message, tt.wantMsg) {
					t.Errorf("Message %q should contain %q", tip.Message, tt.wantMsg)
				}
			}
		})
	}
}

func TestTipBackgroundNoise(t *testing.T) {
	tests := []struct {
		name             string
		astatsNoiseFloor float64
		noiseProfile     *processor.NoiseProfile // nil to test fallback
		wantTip          bool
		wantRuleID       string
	}{
		{
			name:             "high noise with NoiseProfile",
			astatsNoiseFloor: -50.0,
			noiseProfile:     &processor.NoiseProfile{MeasuredNoiseFloor: -42.0},
			wantTip:          true,
			wantRuleID:       "background_noise_high",
		},
		{
			name:             "moderate noise with NoiseProfile",
			astatsNoiseFloor: -60.0,
			noiseProfile:     &processor.NoiseProfile{MeasuredNoiseFloor: -52.0},
			wantTip:          true,
			wantRuleID:       "background_noise_moderate",
		},
		{
			name:             "clean with NoiseProfile",
			astatsNoiseFloor: -50.0,
			noiseProfile:     &processor.NoiseProfile{MeasuredNoiseFloor: -68.0},
			wantTip:          false,
			wantRuleID:       "",
		},
		{
			name:             "nil NoiseProfile fallback to astats high",
			astatsNoiseFloor: -42.0,
			noiseProfile:     nil,
			wantTip:          true,
			wantRuleID:       "background_noise_high",
		},
		{
			name:             "nil NoiseProfile fallback to astats clean",
			astatsNoiseFloor: -68.0,
			noiseProfile:     nil,
			wantTip:          false,
			wantRuleID:       "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.AstatsNoiseFloor = tt.astatsNoiseFloor
			m.NoiseProfile = tt.noiseProfile
			tip := tipBackgroundNoise(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipBackgroundNoise() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil && tip.RuleID != tt.wantRuleID {
				t.Errorf("RuleID = %q, want %q", tip.RuleID, tt.wantRuleID)
			}
		})
	}
}

func TestTipMainsHum(t *testing.T) {
	tests := []struct {
		name         string
		noiseProfile *processor.NoiseProfile
		wantTip      bool
	}{
		{
			name: "tonal noise detected",
			noiseProfile: &processor.NoiseProfile{
				MeasuredNoiseFloor: -55.0,
				Entropy:            0.15,
				SpectralFlatness:   0.10,
			},
			wantTip: true,
		},
		{
			name: "high entropy no hum",
			noiseProfile: &processor.NoiseProfile{
				MeasuredNoiseFloor: -55.0,
				Entropy:            0.50,
				SpectralFlatness:   0.10,
			},
			wantTip: false,
		},
		{
			name: "high flatness no hum",
			noiseProfile: &processor.NoiseProfile{
				MeasuredNoiseFloor: -55.0,
				Entropy:            0.15,
				SpectralFlatness:   0.50,
			},
			wantTip: false,
		},
		{
			name: "noise at audibility boundary fires",
			noiseProfile: &processor.NoiseProfile{
				MeasuredNoiseFloor: -65.0,
				Entropy:            0.15,
				SpectralFlatness:   0.10,
			},
			wantTip: true,
		},
		{
			name: "noise below audibility gate suppressed",
			noiseProfile: &processor.NoiseProfile{
				MeasuredNoiseFloor: -68.0,
				Entropy:            0.15,
				SpectralFlatness:   0.10,
			},
			wantTip: false,
		},
		{
			name:         "nil NoiseProfile",
			noiseProfile: nil,
			wantTip:      false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.NoiseProfile = tt.noiseProfile
			tip := tipMainsHum(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipMainsHum() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil && tip.RuleID != "mains_hum" {
				t.Errorf("RuleID = %q, want %q", tip.RuleID, "mains_hum")
			}
		})
	}
}

func TestTipTooFarFromMic(t *testing.T) {
	tests := []struct {
		name          string
		speechProfile *processor.SpeechCandidateMetrics
		noiseProfile  *processor.NoiseProfile
		headroom      float64
		wantTip       bool
	}{
		{
			name:          "too far low headroom and quiet speech",
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -35.0},
			noiseProfile:  &processor.NoiseProfile{MeasuredNoiseFloor: -50.0},
			headroom:      12.0,
			wantTip:       true,
		},
		{
			name:          "good headroom",
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -35.0},
			noiseProfile:  &processor.NoiseProfile{MeasuredNoiseFloor: -60.0},
			headroom:      20.0,
			wantTip:       false,
		},
		{
			name:          "loud speech",
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -22.0},
			noiseProfile:  &processor.NoiseProfile{MeasuredNoiseFloor: -50.0},
			headroom:      12.0,
			wantTip:       false,
		},
		{
			name:          "nil SpeechProfile",
			speechProfile: nil,
			noiseProfile:  &processor.NoiseProfile{MeasuredNoiseFloor: -50.0},
			headroom:      12.0,
			wantTip:       false,
		},
		{
			name:          "nil NoiseProfile",
			speechProfile: &processor.SpeechCandidateMetrics{RMSLevel: -35.0},
			noiseProfile:  nil,
			headroom:      12.0,
			wantTip:       false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.SpeechProfile = tt.speechProfile
			m.NoiseProfile = tt.noiseProfile
			m.NoiseReductionHeadroom = tt.headroom
			tip := tipTooFarFromMic(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipTooFarFromMic() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil && tip.RuleID != "too_far_from_mic" {
				t.Errorf("RuleID = %q, want %q", tip.RuleID, "too_far_from_mic")
			}
		})
	}
}

func TestTipProximityEffect(t *testing.T) {
	tests := []struct {
		name             string
		spectralDecrease float64
		spectralSkewness float64
		speechProfile    *processor.SpeechCandidateMetrics
		wantTip          bool
	}{
		{"very warm spectral decrease", -0.15, 1.0, nil, true},
		{"warm with high skewness", -0.07, 3.0, nil, true},
		{"warm without skewness", -0.07, 1.5, nil, false},
		{"normal spectral decrease", -0.03, 1.0, nil, false},
		{"boundary decrease -0.10 fires", -0.101, 0.0, nil, true},
		{"boundary decrease -0.05 with skew", -0.051, 2.6, nil, true},
		{
			name:             "speech profile overrides full-file no tip",
			spectralDecrease: -0.15,
			spectralSkewness: 1.0,
			speechProfile:    &processor.SpeechCandidateMetrics{SpectralDecrease: -0.03, SpectralSkewness: 0.5},
			wantTip:          false,
		},
		{
			name:             "speech profile triggers when full-file would not",
			spectralDecrease: -0.03,
			spectralSkewness: 0.5,
			speechProfile:    &processor.SpeechCandidateMetrics{SpectralDecrease: -0.15, SpectralSkewness: 1.0},
			wantTip:          true,
		},
		{
			name:             "nil speech profile uses full-file",
			spectralDecrease: -0.15,
			spectralSkewness: 1.0,
			speechProfile:    nil,
			wantTip:          true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.SpectralDecrease = tt.spectralDecrease
			m.SpectralSkewness = tt.spectralSkewness
			m.SpeechProfile = tt.speechProfile
			tip := tipProximityEffect(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipProximityEffect() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil && tip.RuleID != "proximity_effect" {
				t.Errorf("RuleID = %q, want %q", tip.RuleID, "proximity_effect")
			}
		})
	}
}

func TestTipSibilance(t *testing.T) {
	tests := []struct {
		name          string
		config        *processor.FilterChainConfig
		centroid      float64
		rolloff       float64
		speechProfile *processor.SpeechCandidateMetrics
		wantTip       bool
	}{
		{
			name:     "high de-esser bright speech",
			config:   &processor.FilterChainConfig{DeessIntensity: 0.6},
			centroid: 4500.0,
			rolloff:  11000.0,
			wantTip:  true,
		},
		{
			name:     "low de-esser",
			config:   &processor.FilterChainConfig{DeessIntensity: 0.3},
			centroid: 4500.0,
			rolloff:  11000.0,
			wantTip:  false,
		},
		{
			name:     "de-esser at boundary no tip",
			config:   &processor.FilterChainConfig{DeessIntensity: 0.5},
			centroid: 4500.0,
			rolloff:  11000.0,
			wantTip:  false,
		},
		{
			name:     "nil config",
			config:   nil,
			centroid: 4500.0,
			rolloff:  11000.0,
			wantTip:  false,
		},
		{
			name:     "dark voice no sibilance",
			config:   &processor.FilterChainConfig{DeessIntensity: 0.6},
			centroid: 3000.0,
			rolloff:  11000.0,
			wantTip:  false,
		},
		{
			name:     "low rolloff no sibilance",
			config:   &processor.FilterChainConfig{DeessIntensity: 0.6},
			centroid: 4500.0,
			rolloff:  9000.0,
			wantTip:  false,
		},
		{
			name:     "speech profile overrides full-file",
			config:   &processor.FilterChainConfig{DeessIntensity: 0.6},
			centroid: 3000.0,
			rolloff:  9000.0,
			speechProfile: &processor.SpeechCandidateMetrics{
				SpectralCentroid: 4500.0,
				SpectralRolloff:  11000.0,
			},
			wantTip: true,
		},
		{
			name:     "speech profile zero values use full-file",
			config:   &processor.FilterChainConfig{DeessIntensity: 0.6},
			centroid: 4500.0,
			rolloff:  11000.0,
			speechProfile: &processor.SpeechCandidateMetrics{
				SpectralCentroid: 0,
				SpectralRolloff:  0,
			},
			wantTip: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.SpectralCentroid = tt.centroid
			m.SpectralRolloff = tt.rolloff
			m.SpeechProfile = tt.speechProfile
			tip := tipSibilance(m, tt.config)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipSibilance() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil && tip.RuleID != "sibilance" {
				t.Errorf("RuleID = %q, want %q", tip.RuleID, "sibilance")
			}
		})
	}
}

func TestTipDynamicRange(t *testing.T) {
	tests := []struct {
		name     string
		inputLRA float64
		wantTip  bool
	}{
		{"very wide LRA", 20.0, true},
		{"normal LRA", 10.0, false},
		{"boundary LRA 18 no tip", 18.0, false},
		{"just above boundary", 18.1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.InputLRA = tt.inputLRA
			tip := tipDynamicRange(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipDynamicRange() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil && tip.RuleID != "dynamic_range" {
				t.Errorf("RuleID = %q, want %q", tip.RuleID, "dynamic_range")
			}
		})
	}
}

func TestTipOverCompressed(t *testing.T) {
	tests := []struct {
		name          string
		crestFactor   float64
		speechProfile *processor.SpeechCandidateMetrics
		wantTip       bool
	}{
		{"heavily compressed", 4.0, nil, true},
		{"boundary crest 6 no tip", 6.0, nil, false},
		{"crest zero unmeasured", 0, nil, false},
		{"normal crest", 12.0, nil, false},
		{
			name:          "speech crest overrides full-file no tip",
			crestFactor:   4.0,
			speechProfile: &processor.SpeechCandidateMetrics{CrestFactor: 12.0},
			wantTip:       false,
		},
		{
			name:          "speech crest triggers compressed",
			crestFactor:   12.0,
			speechProfile: &processor.SpeechCandidateMetrics{CrestFactor: 4.0},
			wantTip:       true,
		},
		{
			name:          "speech crest zero uses full-file",
			crestFactor:   4.0,
			speechProfile: &processor.SpeechCandidateMetrics{CrestFactor: 0},
			wantTip:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.CrestFactor = tt.crestFactor
			m.SpeechProfile = tt.speechProfile
			tip := tipOverCompressed(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipOverCompressed() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil && tip.RuleID != "over_compressed" {
				t.Errorf("RuleID = %q, want %q", tip.RuleID, "over_compressed")
			}
		})
	}
}

func TestTipPoorSNR(t *testing.T) {
	tests := []struct {
		name     string
		headroom float64
		wantTip  bool
	}{
		{"poor SNR", 8.0, true},
		{"boundary headroom 10 no tip", 10.0, false},
		{"headroom zero unmeasured", 0, false},
		{"good headroom", 25.0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.NoiseReductionHeadroom = tt.headroom
			tip := tipPoorSNR(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipPoorSNR() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil && tip.RuleID != "poor_snr" {
				t.Errorf("RuleID = %q, want %q", tip.RuleID, "poor_snr")
			}
		})
	}
}

func TestTipHighCrestFactor(t *testing.T) {
	tests := []struct {
		name          string
		crestFactor   float64
		speechProfile *processor.SpeechCandidateMetrics
		wantTip       bool
	}{
		{"high crest factor fires", 25.0, nil, true},
		{"boundary crest 20 no tip", 20.0, nil, false},
		{"just above boundary", 20.1, nil, true},
		{"normal crest", 12.0, nil, false},
		{"crest zero unmeasured", 0, nil, false},
		{
			name:          "speech crest overrides full-file fires",
			crestFactor:   12.0,
			speechProfile: &processor.SpeechCandidateMetrics{CrestFactor: 25.0},
			wantTip:       true,
		},
		{
			name:          "speech crest overrides full-file no tip",
			crestFactor:   25.0,
			speechProfile: &processor.SpeechCandidateMetrics{CrestFactor: 12.0},
			wantTip:       false,
		},
		{
			name:          "speech crest zero uses full-file",
			crestFactor:   25.0,
			speechProfile: &processor.SpeechCandidateMetrics{CrestFactor: 0},
			wantTip:       true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.CrestFactor = tt.crestFactor
			m.SpeechProfile = tt.speechProfile
			tip := tipHighCrestFactor(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipHighCrestFactor() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil && tip.RuleID != "high_crest_factor" {
				t.Errorf("RuleID = %q, want %q", tip.RuleID, "high_crest_factor")
			}
		})
	}
}

// hasRuleID checks whether any tip in the slice has the given RuleID.
func hasRuleID(tips []RecordingTip, ruleID string) bool {
	for _, tip := range tips {
		if tip.RuleID == ruleID {
			return true
		}
	}
	return false
}

// ruleIDs extracts RuleIDs from tips for error messages.
func ruleIDs(tips []RecordingTip) []string {
	ids := make([]string, len(tips))
	for i, tip := range tips {
		ids[i] = tip.RuleID
	}
	return ids
}

func TestGenerateRecordingTips(t *testing.T) {
	tests := []struct {
		name             string
		measurements     *processor.AudioMeasurements
		config           *processor.FilterChainConfig
		wantRuleIDs      []string // these RuleIDs must be present
		excludeRuleIDs   []string // these RuleIDs must NOT be present
		checkFirstRuleID string   // if set, first tip must have this RuleID
		maxTips          int      // if > 0, verify len(tips) <= this
		wantExact        int      // if > 0, verify len(tips) == this
		wantEmpty        bool     // if true, verify tips is nil or empty
	}{
		{
			name: "mutual exclusion suppresses level_quiet and poor_snr",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -28.0
				m.InputTP = -10.0
				m.NoiseReductionHeadroom = 8.0
				m.SpeechProfile = &processor.SpeechCandidateMetrics{RMSLevel: -35.0}
				m.NoiseProfile = &processor.NoiseProfile{MeasuredNoiseFloor: -70.0}
				m.CrestFactor = 12.0
				return m
			}(),
			wantRuleIDs:    []string{"too_far_from_mic"},
			excludeRuleIDs: []string{"level_quiet", "poor_snr"},
		},
		{
			name: "implicit exclusion level_too_quiet not level_quiet",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -35.0
				m.InputTP = -10.0
				m.CrestFactor = 12.0
				return m
			}(),
			wantRuleIDs:    []string{"level_too_quiet"},
			excludeRuleIDs: []string{"level_quiet"},
		},
		{
			name: "priority ordering highest first",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -35.0
				m.InputTP = -10.0
				m.AstatsNoiseFloor = -42.0
				m.NoiseReductionHeadroom = 8.0
				m.CrestFactor = 12.0
				return m
			}(),
			checkFirstRuleID: "level_too_quiet",
		},
		{
			name: "max cap at 5 tips",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -35.0
				m.InputTP = 0.5
				m.AstatsNoiseFloor = -42.0
				m.NoiseReductionHeadroom = 8.0
				m.SpectralDecrease = -0.15
				m.InputLRA = 20.0
				m.CrestFactor = 4.0
				return m
			}(),
			maxTips: 5,
		},
		{
			name: "clean recording no tips",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -20.0
				m.InputTP = -6.0
				m.InputLRA = 8.0
				m.AstatsNoiseFloor = -70.0
				m.CrestFactor = 12.0
				m.NoiseReductionHeadroom = 25.0
				m.SpectralDecrease = -0.02
				m.SpectralSkewness = 0.5
				return m
			}(),
			wantEmpty: true,
		},
		{
			name: "mutual exclusion clipping suppresses level_too_quiet",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -35.0 // would trigger level_too_quiet
				m.InputTP = 0.5  // clipping
				m.CrestFactor = 12.0
				return m
			}(),
			wantRuleIDs:    []string{"level_clipping"},
			excludeRuleIDs: []string{"level_too_quiet", "level_quiet"},
		},
		{
			name: "mutual exclusion near_clipping suppresses level_quiet",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -28.0 // would trigger level_quiet
				m.InputTP = -0.5 // near clipping
				m.CrestFactor = 12.0
				return m
			}(),
			wantRuleIDs:    []string{"level_near_clipping"},
			excludeRuleIDs: []string{"level_too_quiet", "level_quiet"},
		},
		{
			name: "all bad recording returns exactly 5",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -35.0
				m.InputTP = 0.5
				m.AstatsNoiseFloor = -42.0
				m.NoiseReductionHeadroom = 8.0
				m.SpectralDecrease = -0.15
				m.InputLRA = 20.0
				m.CrestFactor = 4.0
				m.NoiseProfile = &processor.NoiseProfile{
					MeasuredNoiseFloor: -42.0,
					Entropy:            0.15,
					SpectralFlatness:   0.10,
				}
				return m
			}(),
			wantExact: 5,
		},
		{
			name: "regression case A: Mark quiet with peaks near ceiling",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -20.0
				m.InputTP = -4.2
				m.CrestFactor = 25.9
				m.SpeechProfile = &processor.SpeechCandidateMetrics{
					RMSLevel:    -38.5,
					CrestFactor: 25.9,
				}
				return m
			}(),
			wantRuleIDs:    []string{"level_quiet", "high_crest_factor"},
			excludeRuleIDs: []string{"level_clipping", "level_near_clipping"},
		},
		{
			name: "regression case B: Martin very quiet with moderate peaks",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -20.0
				m.InputTP = -9.8
				m.CrestFactor = 29.8
				m.SpeechProfile = &processor.SpeechCandidateMetrics{
					RMSLevel:    -41.6,
					CrestFactor: 29.8,
				}
				return m
			}(),
			wantRuleIDs:    []string{"level_quiet", "high_crest_factor"},
			excludeRuleIDs: []string{"level_clipping", "level_too_quiet"},
		},
		{
			name: "regression case C: Popey clipping while quiet",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -31.3
				m.InputTP = 0.1
				m.CrestFactor = 29.2
				m.SpeechProfile = &processor.SpeechCandidateMetrics{
					RMSLevel:    -40.2,
					CrestFactor: 29.2,
				}
				return m
			}(),
			wantRuleIDs: []string{"level_clipping", "level_quiet", "high_crest_factor"},
		},
		{
			name: "high crest factor prevents quiet suppression by clipping",
			measurements: func() *processor.AudioMeasurements {
				m := &processor.AudioMeasurements{}
				m.InputI = -35.0
				m.InputTP = 0.5
				m.CrestFactor = 25.0
				return m
			}(),
			wantRuleIDs: []string{"level_clipping", "level_too_quiet", "high_crest_factor"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tips := GenerateRecordingTips(tt.measurements, tt.config)

			if tt.wantEmpty {
				if len(tips) != 0 {
					t.Errorf("expected no tips, got %d: %v", len(tips), ruleIDs(tips))
				}
				return
			}

			for _, wantID := range tt.wantRuleIDs {
				if !hasRuleID(tips, wantID) {
					t.Errorf("expected RuleID %q in tips, got %v", wantID, ruleIDs(tips))
				}
			}

			for _, excludeID := range tt.excludeRuleIDs {
				if hasRuleID(tips, excludeID) {
					t.Errorf("RuleID %q should be excluded, got %v", excludeID, ruleIDs(tips))
				}
			}

			if tt.checkFirstRuleID != "" && len(tips) > 0 {
				if tips[0].RuleID != tt.checkFirstRuleID {
					t.Errorf("first tip RuleID = %q, want %q (tips: %v)", tips[0].RuleID, tt.checkFirstRuleID, ruleIDs(tips))
				}
			}

			if tt.maxTips > 0 && len(tips) > tt.maxTips {
				t.Errorf("got %d tips, want at most %d: %v", len(tips), tt.maxTips, ruleIDs(tips))
			}

			if tt.wantExact > 0 && len(tips) != tt.wantExact {
				t.Errorf("got %d tips, want exactly %d: %v", len(tips), tt.wantExact, ruleIDs(tips))
			}
		})
	}
}
