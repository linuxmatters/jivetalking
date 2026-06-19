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
	Level        float64 // raw decode-loop level (raw dBFS), not a VAD output
	Duration     float64 // total audio length, seconds
	Measurements *processor.AudioMeasurements
}

// FileStartMsg indicates a new file has started processing
type FileStartMsg struct {
	FileIndex int
	FileName  string
}

// CompletionResult carries the per-file completion payload shared by
// FileCompleteMsg (sent by the pool) and FileProgress (held by the model). Both
// embed it so the model can copy the whole payload in one assignment; a field
// added here reaches FileProgress without a matching copy line, and a dropped
// copy becomes a compile error rather than a silent omission.
type CompletionResult struct {
	InputLUFS  float64
	OutputLUFS float64
	// FinalNoiseFloor is the output room-tone noise floor in dBFS (lower = cleaner),
	// the same quantity the quality score rewards, so the done box's Noise row and
	// the star count move together. InputNoiseFloor is the input room-tone floor on
	// the same astats RMS dBFS axis, so the done box can show an input->output pair.
	// The Have* flags gate each end of that pair; an absent end falls back to the
	// single available value rather than a misleading 0.
	FinalNoiseFloor     float64
	InputNoiseFloor     float64
	HaveFinalNoiseFloor bool
	HaveInputNoiseFloor bool
	// OutputTP is the post-normalisation true peak (dBTP), measured by ebur128 on
	// the final output (NormResult.OutputTP). Paired with Summary.TruePeakDBTP it
	// drives the done-box True peak before→after row.
	OutputTP float64
	// OutputLRA is the post-normalisation loudness range (LU), measured by ebur128
	// on the final output (NormResult.FinalMeasurements.Loudness.OutputLRA). Paired
	// with Summary.InputLRA it drives the done-box Dynamics before→after row.
	OutputLRA  float64
	OutputPath string
	// Quality is the OUTPUT quality score (Processed), graded against spec. It
	// reliably saturates near 5 stars because the normaliser hits -16 LUFS.
	Quality processor.QualityScore
	// RecordingQuality is the INPUT capture quality score (Recording), graded from
	// Pass-1 measurements. It genuinely varies with source quality, so the pair
	// Recording -> Processed tells the value story in the done box.
	RecordingQuality processor.QualityScore
	// ProcessingTime is the total wall-clock time across all four passes; it drives
	// the done-box Time row. FileProgress.ElapsedTime cannot be used because it
	// resets per pass.
	ProcessingTime time.Duration
	Error          error
}

// FileCompleteMsg indicates a file has finished processing
type FileCompleteMsg struct {
	FileIndex int
	CompletionResult
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
