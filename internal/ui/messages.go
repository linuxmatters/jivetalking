package ui

import (
	"github.com/linuxmatters/jivetalking/internal/processor"
)

// ProgressMsg represents a progress update from the processor
type ProgressMsg struct {
	Pass         int     // 1 or 2
	PassName     string  // "Analyzing" or "Processing"
	Progress     float64 // 0.0 to 1.0
	Level        float64 // Current audio level in dB
	Measurements *processor.LoudnormMeasurements
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
	NoiseFloor float64
	OutputPath string
	Error      error
}

// AllCompleteMsg indicates all files have been processed
type AllCompleteMsg struct{}
