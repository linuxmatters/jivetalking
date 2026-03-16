package processor

import (
	"fmt"

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
