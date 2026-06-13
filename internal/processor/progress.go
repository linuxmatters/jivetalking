package processor

// ProgressUpdate describes a processor progress event without depending on UI
// message types.
type ProgressUpdate struct {
	Pass         PassNumber
	PassName     string
	Progress     float64
	Level        float64
	Duration     float64 // total audio length, seconds
	Measurements *AudioMeasurements

	// Config and Diagnostics carry the post-AdaptConfig effective filter config and
	// adaptation diagnostics. They are populated ONLY on the Pass-2 start event
	// (Progress 0.0, PassProcessing) so the TUI can light its filter-chain status
	// boxes; every other event leaves them nil. This is read-only surfacing of the
	// already-derived config, no DSP and no AdaptConfig behaviour changes.
	Config      *EffectiveFilterConfig
	Diagnostics *AdaptiveDiagnostics

	// Limiter carries the limiter ceiling + enabled flag, populated ONLY on the
	// Pass-4 (Normalising) start event, the moment planLimiterForLoudnorm has
	// computed the ceiling. It is nil on every other event. This lets the TUI light
	// its Limiter row WHILE the file is still processing, instead of only at
	// completion. Read-only surfacing of an already-computed plan, no DSP and no
	// limiter-maths changes.
	Limiter *LimiterProgress
}

// LimiterProgress is the read-only limiter snapshot surfaced on the Pass-4 start
// ProgressUpdate so the TUI can resolve its Limiter row during processing. Mirrors
// the limiterPlan fields the final NormalisationResult also reports.
type LimiterProgress struct {
	Enabled bool    // pre-limiting will be applied (limiterPlan.needed)
	Ceiling float64 // ceiling (dBTP), valid only when Enabled
}

// ProgressCallback receives processor progress events.
type ProgressCallback func(ProgressUpdate)
