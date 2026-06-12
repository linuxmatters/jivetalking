package ui

import (
	"time"

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
	// OutputTP is the post-normalisation true peak (dBTP), measured by ebur128 on
	// the final output (NormResult.OutputTP). Paired with Summary.TruePeakDBTP it
	// drives the done-box True peak before→after row.
	OutputTP float64
	// OutputLRA is the post-normalisation loudness range (LU), measured by ebur128
	// on the final output (NormResult.FinalMeasurements.Loudness.OutputLRA). Paired
	// with Summary.InputLRA it drives the done-box Dynamics before→after row.
	OutputLRA  float64
	OutputPath string
	Quality    processor.QualityScore
	// ProcessingTime is the total wall-clock time across all four passes; it drives
	// the done-box Time row. FileProgress.ElapsedTime cannot be used because it
	// resets per pass.
	ProcessingTime time.Duration
	Error          error
}

// AdaptedSummaryMsg carries the filter-chain status view-model for a single file,
// routed by FileIndex. It is a state-change message, not a per-frame update: the
// pool sends it at Pass-2 start (chain + analysis rows; limiter pending) and again
// at completion (limiter ceiling). The boxes re-render only on receipt, never on
// the meter tick.
type AdaptedSummaryMsg struct {
	FileIndex int
	Summary   AdaptedSummary
}

// AllCompleteMsg indicates all files have been processed
type AllCompleteMsg struct{}
