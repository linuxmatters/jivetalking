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
}

// ProgressCallback receives processor progress events.
type ProgressCallback func(ProgressUpdate)
