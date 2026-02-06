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
		name       string
		inputI     float64
		wantTip    bool
		wantRuleID string
		wantGain   string // substring to check in message, empty to skip
	}{
		{"very quiet -35 LUFS", -35.0, true, "level_too_quiet", "17 dB"},
		{"boundary -30 LUFS", -30.0, false, "", ""},
		{"moderately quiet -28 LUFS", -28.0, false, "", ""},
		{"normal -20 LUFS", -20.0, false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.InputI = tt.inputI
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
		name       string
		inputI     float64
		wantTip    bool
		wantRuleID string
		wantGain   string
	}{
		{"very quiet handled by too_quiet", -35.0, false, "", ""},
		{"boundary -30 LUFS triggers quiet", -30.0, true, "level_quiet", "12 dB"},
		{"moderately quiet -28 LUFS", -28.0, true, "level_quiet", "10 dB"},
		{"boundary -24 LUFS no tip", -24.0, false, "", ""},
		{"normal -20 LUFS", -20.0, false, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.InputI = tt.inputI
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
		wantTip    bool
		wantRuleID string
	}{
		{"clipping +0.5 dBTP", 0.5, true, "level_clipping"},
		{"boundary 0.0 dBTP near clipping", 0.0, true, "level_near_clipping"},
		{"near clipping -0.5 dBTP", -0.5, true, "level_near_clipping"},
		{"boundary -1.0 dBTP no tip", -1.0, false, ""},
		{"safe -3.0 dBTP", -3.0, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.InputTP = tt.inputTP
			tip := tipLevelTooHot(m, nil)
			if (tip != nil) != tt.wantTip {
				t.Errorf("tipLevelTooHot() returned tip=%v, want tip=%v", tip != nil, tt.wantTip)
			}
			if tip != nil {
				if tip.RuleID != tt.wantRuleID {
					t.Errorf("RuleID = %q, want %q", tip.RuleID, tt.wantRuleID)
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
		wantTip          bool
	}{
		{"very warm spectral decrease", -0.15, 1.0, true},
		{"warm with high skewness", -0.07, 3.0, true},
		{"warm without skewness", -0.07, 1.5, false},
		{"normal spectral decrease", -0.03, 1.0, false},
		{"boundary decrease -0.10 fires", -0.101, 0.0, true},
		{"boundary decrease -0.05 with skew", -0.051, 2.6, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.SpectralDecrease = tt.spectralDecrease
			m.SpectralSkewness = tt.spectralSkewness
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
		name        string
		inputLRA    float64
		crestFactor float64
		wantTip     bool
	}{
		{"very wide LRA", 20.0, 12.0, true},
		{"wide LRA with high crest", 15.0, 20.0, true},
		{"wide LRA with normal crest", 15.0, 12.0, false},
		{"normal LRA", 10.0, 12.0, false},
		{"boundary LRA 18 no tip", 18.0, 12.0, false},
		{"boundary LRA 14 with crest 18 no tip", 14.0, 18.0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.InputLRA = tt.inputLRA
			m.CrestFactor = tt.crestFactor
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
		name        string
		crestFactor float64
		wantTip     bool
	}{
		{"heavily compressed", 4.0, true},
		{"boundary crest 6 no tip", 6.0, false},
		{"crest zero unmeasured", 0, false},
		{"normal crest", 12.0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &processor.AudioMeasurements{}
			m.CrestFactor = tt.crestFactor
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
