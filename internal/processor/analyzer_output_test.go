package processor

import (
	"fmt"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
)

// measureOutputSilenceRegion analyses the elected silence region in the output file
// to capture comprehensive metrics for before/after comparison and adaptive tuning.
//
// The region parameter should use the same Start/Duration as the NoiseProfile
// from Pass 1 analysis. Returns nil if the region cannot be measured.
func measureOutputSilenceRegion(outputPath string, region SilenceRegion) (*SilenceCandidateMetrics, error) {
	// Open the processed audio file
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open output file: %w", err)
	}
	defer reader.Close()

	return measureOutputSilenceRegionFromReader(reader, region)
}

// measureOutputSpeechRegion analyses a speech region in the output file
// to capture comprehensive metrics for adaptive filter tuning and validation.
//
// The region parameter should identify a representative speech section from
// the processed audio. Returns nil if the region cannot be measured.
func measureOutputSpeechRegion(outputPath string, region SpeechRegion) (*SpeechCandidateMetrics, error) {
	// Open the processed audio file
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open output file: %w", err)
	}
	defer reader.Close()

	return measureOutputSpeechRegionFromReader(reader, region)
}

func TestExtractRegionPair(t *testing.T) {
	tests := []struct {
		name         string
		measurements *AudioMeasurements
		wantSilence  bool
		wantSpeech   bool
		wantSilEnd   time.Duration // expected End = Start + Duration
	}{
		{
			name:         "both profiles absent returns nil pair",
			measurements: &AudioMeasurements{},
			wantSilence:  false,
			wantSpeech:   false,
		},
		{
			name: "NoiseProfile only returns silence region",
			measurements: &AudioMeasurements{
				NoiseProfile: &NoiseProfile{
					Start:    2 * time.Second,
					Duration: 500 * time.Millisecond,
				},
			},
			wantSilence: true,
			wantSpeech:  false,
			wantSilEnd:  2*time.Second + 500*time.Millisecond,
		},
		{
			name: "SpeechProfile only returns speech region",
			measurements: &AudioMeasurements{
				SpeechProfile: &SpeechCandidateMetrics{
					Region: SpeechRegion{
						Start:    5 * time.Second,
						End:      8 * time.Second,
						Duration: 3 * time.Second,
					},
				},
			},
			wantSilence: false,
			wantSpeech:  true,
		},
		{
			name: "both present returns both non-nil",
			measurements: &AudioMeasurements{
				NoiseProfile: &NoiseProfile{
					Start:    1 * time.Second,
					Duration: 400 * time.Millisecond,
				},
				SpeechProfile: &SpeechCandidateMetrics{
					Region: SpeechRegion{
						Start:    10 * time.Second,
						End:      13 * time.Second,
						Duration: 3 * time.Second,
					},
				},
			},
			wantSilence: true,
			wantSpeech:  true,
			wantSilEnd:  1*time.Second + 400*time.Millisecond,
		},
		{
			name: "End equals Start plus Duration for silence region",
			measurements: &AudioMeasurements{
				NoiseProfile: &NoiseProfile{
					Start:    3 * time.Second,
					Duration: 750 * time.Millisecond,
				},
			},
			wantSilence: true,
			wantSpeech:  false,
			wantSilEnd:  3*time.Second + 750*time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			silRegion, spRegion := extractRegionPair(tt.measurements)

			if tt.wantSilence && silRegion == nil {
				t.Fatal("expected non-nil SilenceRegion, got nil")
			}
			if !tt.wantSilence && silRegion != nil {
				t.Fatalf("expected nil SilenceRegion, got %+v", silRegion)
			}
			if tt.wantSpeech && spRegion == nil {
				t.Fatal("expected non-nil SpeechRegion, got nil")
			}
			if !tt.wantSpeech && spRegion != nil {
				t.Fatalf("expected nil SpeechRegion, got %+v", spRegion)
			}

			if silRegion != nil && tt.wantSilEnd != 0 {
				if silRegion.End != tt.wantSilEnd {
					t.Errorf("SilenceRegion.End = %v, want %v (Start + Duration)", silRegion.End, tt.wantSilEnd)
				}
				if silRegion.End != silRegion.Start+silRegion.Duration {
					t.Errorf("SilenceRegion.End (%v) != Start (%v) + Duration (%v)",
						silRegion.End, silRegion.Start, silRegion.Duration)
				}
			}

			if spRegion != nil {
				want := tt.measurements.SpeechProfile.Region
				if spRegion.Start != want.Start || spRegion.End != want.End || spRegion.Duration != want.Duration {
					t.Errorf("SpeechRegion = %+v, want %+v", *spRegion, want)
				}
			}
		})
	}
}
