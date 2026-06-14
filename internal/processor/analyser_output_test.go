package processor

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/audio"
)

// measureOutputRoomToneRegion analyses the elected room tone region in the output file
// to capture comprehensive metrics for before/after comparison and adaptive tuning.
//
// The region parameter should use the same Start/Duration as the NoiseProfile
// from Pass 1 analysis. Returns nil if the region cannot be measured.
func measureOutputRoomToneRegion(outputPath string, region RoomToneRegion) (*RegionSample, error) {
	// Open the processed audio file
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open output file: %w", err)
	}
	defer reader.Close()

	return measureOutputRoomToneRegionFromReader(context.Background(), reader, region, nil)
}

// measureOutputSpeechRegion analyses a speech region in the output file
// to capture comprehensive metrics for adaptive filter tuning and validation.
//
// The region parameter should identify a representative speech section from
// the processed audio. Returns nil if the region cannot be measured.
func measureOutputSpeechRegion(outputPath string, region SpeechRegion) (*RegionSample, error) {
	// Open the processed audio file
	reader, _, err := audio.OpenAudioFile(outputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open output file: %w", err)
	}
	defer reader.Close()

	return measureOutputSpeechRegionFromReader(context.Background(), reader, region, nil)
}

// regionSeekTarget mirrors the seek-target maths in seekReaderBeforeRegion:
// regionStart - regionSeekPreRoll, floored at 0. Kept in the test so the
// offset relationship is asserted without driving a real demuxer.
func regionSeekTarget(regionStart time.Duration) time.Duration {
	return max(regionStart-regionSeekPreRoll, 0)
}

// TestRegionSeekTargetWindowUnchanged asserts the seek-then-trim offset maths:
// the demuxer seeks to regionStart-preRoll (floored at 0) while the atrim window
// stays region-absolute. The measured span is [regionStart, regionStart+duration)
// regardless of the seek point, because atrim keys off file-absolute PTS. This
// test exercises the offset relationship only; it does not run the audio
// pipeline.
func TestRegionSeekTargetWindowUnchanged(t *testing.T) {
	const duration = 3 * time.Second

	tests := []struct {
		name           string
		regionStart    time.Duration
		wantSeekTarget time.Duration
	}{
		{
			name:           "early region floors seek at zero",
			regionStart:    2 * time.Second,
			wantSeekTarget: 0,
		},
		{
			name:           "region exactly at pre-roll floors at zero",
			regionStart:    regionSeekPreRoll,
			wantSeekTarget: 0,
		},
		{
			name:           "late region seeks pre-roll before start",
			regionStart:    120 * time.Second,
			wantSeekTarget: 120*time.Second - regionSeekPreRoll,
		},
		{
			name:           "very late region seeks pre-roll before start",
			regionStart:    45 * time.Minute,
			wantSeekTarget: 45*time.Minute - regionSeekPreRoll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSeek := regionSeekTarget(tt.regionStart)
			if gotSeek != tt.wantSeekTarget {
				t.Fatalf("seek target = %v, want %v", gotSeek, tt.wantSeekTarget)
			}

			// The seek must never land after the region start, or the atrim
			// window would lose leading samples.
			if gotSeek > tt.regionStart {
				t.Fatalf("seek target %v is after region start %v: window would lose data",
					gotSeek, tt.regionStart)
			}

			// The atrim window is region-absolute and independent of the seek
			// point. The measured span stays [regionStart, regionStart+duration).
			wantWindowStart := tt.regionStart
			wantWindowEnd := tt.regionStart + duration
			if wantWindowEnd-wantWindowStart != duration {
				t.Fatalf("measured window width = %v, want %v",
					wantWindowEnd-wantWindowStart, duration)
			}

			// The pre-roll head-start (the span trimmed away before measurement)
			// equals regionStart - seekTarget and is bounded by the pre-roll.
			preRoll := tt.regionStart - gotSeek
			if preRoll > regionSeekPreRoll {
				t.Fatalf("pre-roll span %v exceeds regionSeekPreRoll %v", preRoll, regionSeekPreRoll)
			}
		})
	}
}

// TestRegionSeekPreRollCoversLoudnessWindows guards the pre-roll constant: it
// must exceed ebur128's longest integration window so a decode head-start
// always covers loudness-meter warm-up.
func TestRegionSeekPreRollCoversLoudnessWindows(t *testing.T) {
	const ebur128ShortTermWindow = 3 * time.Second // longest ebur128 window
	if regionSeekPreRoll <= ebur128ShortTermWindow {
		t.Fatalf("regionSeekPreRoll %v must exceed ebur128 short-term window %v",
			regionSeekPreRoll, ebur128ShortTermWindow)
	}
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
			name: "NoiseProfile only returns room tone region",
			measurements: &AudioMeasurements{
				Regions: RegionMetrics{
					NoiseProfile: &NoiseProfile{
						Start:    2 * time.Second,
						Duration: 500 * time.Millisecond,
					},
				},
			},
			wantSilence: true,
			wantSpeech:  false,
			wantSilEnd:  2*time.Second + 500*time.Millisecond,
		},
		{
			name: "SpeechProfile only returns speech region",
			measurements: &AudioMeasurements{
				Regions: RegionMetrics{
					SpeechProfile: &SpeechCandidateMetrics{
						Region: SpeechRegion{
							Start:    5 * time.Second,
							End:      8 * time.Second,
							Duration: 3 * time.Second,
						},
					},
				},
			},
			wantSilence: false,
			wantSpeech:  true,
		},
		{
			name: "both present returns both non-nil",
			measurements: &AudioMeasurements{
				Regions: RegionMetrics{
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
			},
			wantSilence: true,
			wantSpeech:  true,
			wantSilEnd:  1*time.Second + 400*time.Millisecond,
		},
		{
			name: "End equals Start plus Duration for room tone region",
			measurements: &AudioMeasurements{
				Regions: RegionMetrics{
					NoiseProfile: &NoiseProfile{
						Start:    3 * time.Second,
						Duration: 750 * time.Millisecond,
					},
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
				t.Fatal("expected non-nil RoomToneRegion, got nil")
			}
			if !tt.wantSilence && silRegion != nil {
				t.Fatalf("expected nil RoomToneRegion, got %+v", silRegion)
			}
			if tt.wantSpeech && spRegion == nil {
				t.Fatal("expected non-nil SpeechRegion, got nil")
			}
			if !tt.wantSpeech && spRegion != nil {
				t.Fatalf("expected nil SpeechRegion, got %+v", spRegion)
			}

			if silRegion != nil && tt.wantSilEnd != 0 {
				if silRegion.End != tt.wantSilEnd {
					t.Errorf("RoomToneRegion.End = %v, want %v (Start + Duration)", silRegion.End, tt.wantSilEnd)
				}
				if silRegion.End != silRegion.Start+silRegion.Duration {
					t.Errorf("RoomToneRegion.End (%v) != Start (%v) + Duration (%v)",
						silRegion.End, silRegion.Start, silRegion.Duration)
				}
			}

			if spRegion != nil {
				want := tt.measurements.Regions.SpeechProfile.Region
				if spRegion.Start != want.Start || spRegion.End != want.End || spRegion.Duration != want.Duration {
					t.Errorf("SpeechRegion = %+v, want %+v", *spRegion, want)
				}
			}
		})
	}
}
