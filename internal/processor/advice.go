package processor

import (
	"fmt"
	"math"
)

// GainAdviceKind is the single outcome of input-gain advice. The advice keys off
// ONE binding constraint (input true peak) against ONE target, so the direction
// is never ambiguous: it never says "raise" and "lower" in the same breath the
// way the scrapped per-metric advice did.
type GainAdviceKind int

const (
	// GainFine: the input true peak sits in the well-set band; leave the gain be.
	GainFine GainAdviceKind = iota
	// GainQuiet: the input is wasting headroom; raise the gain.
	GainQuiet
	// GainHot: the input is hotter than the true-peak ceiling; lower the gain.
	GainHot
	// GainClipping: the input touched or crossed 0 dBTP; the distortion is baked
	// into the source and cannot be recovered. Lower the gain on the next take.
	GainClipping
)

// Gain-advice anchors (dBTP), grounded on the 51-file corpus sweep (2026-06-12),
// corpus and absolute-standard anchored. The target aligns with the Recording
// Headroom full-mark so the advice and the score agree on "healthy headroom".
const (
	// gainAdviceTargetTP is the peak we steer toward: -6 dBTP, the Recording
	// Headroom full-mark (corpus p25, healthy headroom).
	gainAdviceTargetTP = -6.0
	// gainAdviceHotTP is the true-peak ceiling: above it the capture is too hot
	// (inter-sample red line, the absolute standard).
	gainAdviceHotTP = -1.0
	// gainAdviceQuietTP is the floor of the well-set band: below it the capture
	// wastes headroom and should come up.
	gainAdviceQuietTP = -12.0
)

// GainAdviceResult is the unit-testable outcome of GainAdvice: the Kind, the
// InputTP it was derived from, and DeltaDB, the SIGNED gain change in dB
// (negative = lower, positive = raise, 0 = fine). Message() formats the prose
// from these three fields so the UI layer carries no advice logic.
type GainAdviceResult struct {
	Kind    GainAdviceKind
	InputTP float64
	DeltaDB float64
}

// GainAdvice derives input-gain advice from the input true peak alone. It is a
// pure function of one number, by design: average loudness and output metrics
// never influence it, so a high-crest capture (peaks healthy at -6 dBTP but a
// very quiet average) correctly returns Fine, NOT "turn up".
//
// Four outcomes against the -6 dBTP target:
//   - inputTP >= 0              -> Clipping: lower by round(inputTP - target)
//   - -1 < inputTP < 0          -> Hot:      lower by round(inputTP - target)
//   - inputTP < -12             -> Quiet:    raise by round(target - inputTP)
//   - -12 <= inputTP <= -1      -> Fine:    leave it
func GainAdvice(inputTP float64) GainAdviceResult {
	switch {
	case inputTP >= 0:
		return GainAdviceResult{
			Kind:    GainClipping,
			InputTP: inputTP,
			DeltaDB: -math.Round(inputTP - gainAdviceTargetTP),
		}
	case inputTP > gainAdviceHotTP:
		return GainAdviceResult{
			Kind:    GainHot,
			InputTP: inputTP,
			DeltaDB: -math.Round(inputTP - gainAdviceTargetTP),
		}
	case inputTP < gainAdviceQuietTP:
		return GainAdviceResult{
			Kind:    GainQuiet,
			InputTP: inputTP,
			DeltaDB: math.Round(gainAdviceTargetTP - inputTP),
		}
	default:
		return GainAdviceResult{
			Kind:    GainFine,
			InputTP: inputTP,
			DeltaDB: 0,
		}
	}
}

// Message renders the human-readable advice line from the Kind, InputTP, and
// DeltaDB as three period-separated parts: Interpretation. Level. Advice. The
// "㏈TP" glyph matches the rest of the TUI, and the peak keeps its sign. No em
// dashes, brackets, or check marks: the Clipping word plus the full red bar
// carry the severity, so the line stays plain prose. Fine reassures rather than
// going silent; Clipping is the strongest interpretation word.
func (r GainAdviceResult) Message() string {
	switch r.Kind {
	case GainClipping:
		return fmt.Sprintf("Clipping. Peaks at %+.1f ㏈TP. Lower input gain ~%.0f ㏈.",
			r.InputTP, math.Abs(r.DeltaDB))
	case GainHot:
		return fmt.Sprintf("Hot. Peaks at %+.1f ㏈TP. Lower input gain ~%.0f ㏈.",
			r.InputTP, math.Abs(r.DeltaDB))
	case GainQuiet:
		return fmt.Sprintf("Quiet. Peaks at %+.1f ㏈TP. Raise input gain ~%.0f ㏈.",
			r.InputTP, math.Abs(r.DeltaDB))
	default:
		return fmt.Sprintf("Level well set. Peaks at %+.1f ㏈TP. No action required.", r.InputTP)
	}
}
