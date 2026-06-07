package ui

import (
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// ProgressMsg represents a progress update from the processor
type ProgressMsg struct {
	FileIndex    int
	Pass         processor.PassNumber
	PassName     string
	Progress     float64
	Level        float64
	Duration     float64 // total audio length, seconds
	Measurements *processor.AudioMeasurements
}

// FileStartMsg indicates a new file has started processing
type FileStartMsg struct {
	FileIndex int
	FileName  string
}

// FileCompleteMsg indicates a file has finished processing
type FileCompleteMsg struct {
	FileIndex  int
	InputLUFS  float64
	OutputLUFS float64
	// FinalNoiseFloor is the output room-tone noise floor in dBFS (lower = cleaner),
	// the same quantity the quality score rewards, so the done box's Noise row and
	// the star count move together.
	FinalNoiseFloor float64
	OutputPath      string
	Quality         processor.QualityScore
	Error           error
}

// AllCompleteMsg indicates all files have been processed
type AllCompleteMsg struct{}
