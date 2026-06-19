package processor

const (
	defaultDeessIntensity = 0.0

	// De-esser engagement breakpoints on the sibilance excess signal (dB),
	// where excess = SpeechProfile.SibBandRMS - SpeechProfile.BodyBandRMS.
	//
	//	excess < -6           → OFF (i = 0.0)
	//	-6 .. -3              → ramp i 0.0 → 0.6
	//	-3 ..  0              → ramp i 0.6 → 0.85
	//	>  0                  → cap  (i = 0.85)
	deessExcessOffDB = -6.0 // Below this, sibilant band sits well under body: no de-essing
	deessExcessMidDB = -3.0 // Ramp knee between gentle and firm engagement
	deessExcessMaxDB = 0.0  // At/above this, sibilant band rivals body: full engagement

	// De-esser intensity ramp endpoints. The af_deesser i parameter is a
	// 5th-power law (pow(i,5)), so the mid breakpoint lands the ramp in the
	// audibly-active part of the curve.
	deessIntensityMid = 0.6  // Intensity at deessExcessMidDB
	deessIntensityMax = 0.85 // Ceiling at/above deessExcessMaxDB
)

// SibilanceExcessDB is the speech-region sibilance excess in dB: the sibilant-band
// RMS minus the body-band RMS (both dBFS, same astats axis). It is the single
// source of the de-esser engagement signal, shared with the UI summary so the box
// and the de-esser tuner never drift. Both bands must be measured (BandsMeasured)
// for the value to mean anything; callers gate on that.
func (m *SpeechCandidateMetrics) SibilanceExcessDB() float64 {
	return m.SibBandRMS - m.BodyBandRMS
}

// tuneDeesser sets de-esser intensity from the speech-region sibilance excess
// (sibilant-band RMS minus body-band RMS). It requires a SpeechProfile with both
// bands measured; full-file metrics are diluted by silence/noise and produce
// false positives, and unmeasured bands read as a spurious 0 dB excess, so
// without measured bands the de-esser stays OFF.
//
// Mapping (sibilanceExcess in dB):
//
//	excess < -6           → i = 0.0  (OFF)
//	-6 .. -3              → linear ramp i 0.0 → 0.6
//	-3 ..  0              → linear ramp i 0.6 → 0.85
//	>  0                  → i = 0.85 (cap)
func tuneDeesser(config *EffectiveFilterConfig, measurements *AudioMeasurements) {
	if measurements.Regions.SpeechProfile == nil || !measurements.Regions.SpeechProfile.BandsMeasured {
		config.Deesser.Intensity = 0.0
		return
	}

	sibilanceExcess := measurements.Regions.SpeechProfile.SibilanceExcessDB()

	switch {
	case sibilanceExcess < deessExcessOffDB:
		config.Deesser.Intensity = 0.0
	case sibilanceExcess < deessExcessMidDB:
		// Ramp 0.0 → deessIntensityMid across [deessExcessOffDB, deessExcessMidDB].
		frac := (sibilanceExcess - deessExcessOffDB) / (deessExcessMidDB - deessExcessOffDB)
		config.Deesser.Intensity = frac * deessIntensityMid
	case sibilanceExcess < deessExcessMaxDB:
		// Ramp deessIntensityMid → deessIntensityMax across [deessExcessMidDB, deessExcessMaxDB].
		frac := (sibilanceExcess - deessExcessMidDB) / (deessExcessMaxDB - deessExcessMidDB)
		config.Deesser.Intensity = deessIntensityMid + frac*(deessIntensityMax-deessIntensityMid)
	default:
		config.Deesser.Intensity = deessIntensityMax
	}
}
