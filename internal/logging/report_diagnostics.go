package logging

import (
	"fmt"
	"math"
	"os"
	"slices"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// writeDiagnosticRoomTone outputs detailed room tone detection diagnostics.
func writeDiagnosticRoomTone(f *os.File, measurements *processor.AudioMeasurements) {
	if measurements == nil {
		return
	}

	writeSection(f, "Diagnostic: Room Tone Detection")

	// Show adaptive room tone detection threshold if different from default
	if measurements.RoomToneDetectLevel != 0 && measurements.RoomToneDetectLevel != -50.0 {
		fmt.Fprintf(f, "Room Tone Threshold: %.1f dB (from %.1f dB noise floor estimate)\n",
			measurements.RoomToneDetectLevel, measurements.PreScanNoiseFloor)
	}

	// Interval sampling summary with RMSLevel distribution analysis
	if len(measurements.IntervalSamples) > 0 {
		fmt.Fprintf(f, "Interval Samples:    %d × 250ms windows analysed\n", len(measurements.IntervalSamples))

		// Calculate and display RMSLevel distribution for room tone detection debugging
		rmsValues := make([]float64, 0, len(measurements.IntervalSamples))
		for _, interval := range measurements.IntervalSamples {
			if interval.RMSLevel > -120 { // Exclude digital silence
				rmsValues = append(rmsValues, interval.RMSLevel)
			}
		}
		if len(rmsValues) >= 10 {
			sorted := make([]float64, len(rmsValues))
			copy(sorted, rmsValues)
			slices.Sort(sorted)

			fmt.Fprintf(f, "  RMSLevel Dist:     min %.1f, p10 %.1f, p25 %.1f, p50 %.1f, p75 %.1f, p90 %.1f, max %.1f dBFS\n",
				sorted[0],
				sorted[len(sorted)/10],
				sorted[len(sorted)/4],
				sorted[len(sorted)/2],
				sorted[len(sorted)*3/4],
				sorted[len(sorted)*9/10],
				sorted[len(sorted)-1])

			// Find largest gap for room tone / speech boundary detection
			var largestGap float64
			var gapIndex int
			for i := 1; i < len(sorted); i++ {
				gap := sorted[i] - sorted[i-1]
				if gap > largestGap {
					largestGap = gap
					gapIndex = i
				}
			}
			if gapIndex > 0 && gapIndex < len(sorted) {
				fmt.Fprintf(f, "  Largest Gap:       %.1f dB between %.1f and %.1f dBFS (%d intervals below)\n",
					largestGap, sorted[gapIndex-1], sorted[gapIndex], gapIndex)
			}
		}
	}

	// Room tone candidates (ranked display of evaluated candidates with scores)
	//nolint:gocritic // ifElseChain: complex display branches with different condition types
	if len(measurements.RoomToneCandidates) > 0 {
		fmt.Fprintf(f, "Room Tone Candidates:  %d evaluated\n", len(measurements.RoomToneCandidates))
		if measurements.VoiceActivated {
			fmt.Fprintf(f, "Voice-Activated:     yes (digital silence fraction >= 95%%)\n")
		}
		electedCandidate, displayCandidates := rankedRoomToneCandidateEntries(measurements)
		writeCandidateDisplaySummary(f, len(measurements.RoomToneCandidates), electedCandidate != nil, len(displayCandidates))
		if electedCandidate != nil {
			entry := *electedCandidate
			writeReportRoomToneCandidateMetrics(f, entry.index, entry.candidate, true)
		}
		for _, entry := range displayCandidates {
			writeReportRoomToneCandidateMetrics(f, entry.index, entry.candidate, false)
		}

		// Rejection summary for zero-scored candidates
		writeReportRejectionSummary(f, measurements.RoomToneCandidates)
	} else if measurements.NoiseProfile != nil {
		fmt.Fprintf(f, "Room Tone Sample:    %.1fs at %.1fs\n",
			measurements.NoiseProfile.Duration.Seconds(),
			measurements.NoiseProfile.Start.Seconds())
		fmt.Fprintf(f, "  Noise Floor:       %.1f dBFS (RMS)\n", measurements.NoiseProfile.MeasuredNoiseFloor)
		fmt.Fprintf(f, "  Peak Level:        %.1f dBFS\n", measurements.NoiseProfile.PeakLevel)
		fmt.Fprintf(f, "  Crest Factor:      %.1f dB\n", measurements.NoiseProfile.CrestFactor)
	} else if len(measurements.RoomToneRegions) > 0 {
		r := measurements.RoomToneRegions[0]
		fmt.Fprintf(f, "Room Tone Detected:  %.1fs at %.1fs (no profile extracted)\n",
			r.Duration.Seconds(), r.Start.Seconds())
	} else {
		fmt.Fprintf(f, "Room Tone Candidates:  NONE FOUND\n")
		if measurements.VoiceActivated {
			fmt.Fprintf(f, "Voice-Activated:     yes (digital silence fraction >= 95%%)\n")
		}
		fmt.Fprintf(f, "  No room tone regions detected in audio. Noise profiling unavailable.\n")
	}

	fmt.Fprintln(f, "")
}

// writeDiagnosticSpeech outputs detailed speech detection diagnostics.
func writeDiagnosticSpeech(f *os.File, measurements *processor.AudioMeasurements) {
	if measurements == nil {
		return
	}

	// Only output section if speech detection was attempted
	if len(measurements.SpeechRegions) == 0 && measurements.SpeechProfile == nil {
		return
	}

	writeSection(f, "Diagnostic: Speech Detection")

	// Show speech candidates summary
	//nolint:gocritic // ifElseChain: complex display branches with different condition types
	if len(measurements.SpeechCandidates) > 0 {
		fmt.Fprintf(f, "Speech Candidates:   %d evaluated\n", len(measurements.SpeechCandidates))
		electedCandidate, displayCandidates := rankedSpeechCandidateEntries(measurements)
		writeCandidateDisplaySummary(f, len(measurements.SpeechCandidates), electedCandidate != nil, len(displayCandidates))

		if electedCandidate != nil {
			entry := *electedCandidate
			writeReportSpeechCandidateMetrics(f, entry.index, entry.candidate, true)
		}
		for _, entry := range displayCandidates {
			writeReportSpeechCandidateMetrics(f, entry.index, entry.candidate, false)
		}
		writeReportSpeechRejectionSummary(f, measurements.SpeechCandidates)
	} else if measurements.SpeechProfile != nil {
		// Profile exists but no candidates list (shouldn't happen, but handle gracefully)
		profile := measurements.SpeechProfile
		fmt.Fprintf(f, "Elected Speech:      %.1fs at %.1fs\n",
			profile.Region.Duration.Seconds(), profile.Region.Start.Seconds())
		fmt.Fprintf(f, "  RMS Level:         %.1f dBFS\n", profile.RMSLevel)
		fmt.Fprintf(f, "  Peak Level:        %.1f dBFS\n", profile.PeakLevel)
		fmt.Fprintf(f, "  Crest Factor:      %.1f dB\n", profile.CrestFactor)
		fmt.Fprintf(f, "  Centroid:          %.0f Hz\n", profile.Spectral.Centroid)
	} else if len(measurements.SpeechRegions) > 0 {
		fmt.Fprintf(f, "Speech Regions:      %d detected\n", len(measurements.SpeechRegions))
		fmt.Fprintf(f, "  No candidate met quality threshold for speech profiling.\n")
	} else {
		fmt.Fprintf(f, "Speech Candidates:   NONE FOUND\n")
		fmt.Fprintf(f, "  No speech regions detected (file may be too short or all room tone).\n")
	}

	fmt.Fprintln(f, "")
}

// writeDiagnosticPeakLimiter outputs the Pass 4 pre-limiting diagnostics.
// The peak limiter creates headroom before loudnorm so it can apply full linear gain.
func writeDiagnosticPeakLimiter(f *os.File, result *processor.NormalisationResult, config *processor.EffectiveFilterConfig) {
	if result == nil || result.Skipped {
		return
	}

	writeSection(f, "Diagnostic: Peak Limiter")

	if !result.LimiterEnabled {
		fmt.Fprintln(f, "Status: BYPASSED")
		fmt.Fprintln(f, "")
		fmt.Fprintln(f, "No pre-limiting required. Loudnorm can apply full linear gain without")
		fmt.Fprintln(f, "exceeding the target true peak ceiling.")
		fmt.Fprintln(f, "")
		projectedTP := result.InputTP + result.LimiterGain
		fmt.Fprintf(f, "Projected TP:    %.1f dBTP (gain %.1f dB applied to %.1f dBTP peaks)\n",
			projectedTP, result.LimiterGain, result.InputTP)
		fmt.Fprintf(f, "Target TP:       %.1f dBTP\n", config.Loudnorm.TargetTP)
		fmt.Fprintf(f, "Headroom:        %.1f dB\n", config.Loudnorm.TargetTP-projectedTP)
		fmt.Fprintln(f, "")
		return
	}

	fmt.Fprintln(f, "Status: ACTIVE")
	fmt.Fprintln(f, "")
	fmt.Fprintln(f, "Pre-limiting applied to create headroom for loudnorm's linear gain.")
	fmt.Fprintln(f, "Without limiting, loudnorm would either clip or reduce the target LUFS.")
	fmt.Fprintln(f, "")

	// Calculate projected TP without limiting
	projectedTPWithoutLimiter := result.InputTP + result.LimiterGain

	fmt.Fprintln(f, "Problem:")
	fmt.Fprintf(f, "  Input TP:          %.1f dBTP (peaks from Pass 2 filtered audio)\n", result.InputTP)
	fmt.Fprintf(f, "  Gain Required:     %+.1f dB (to reach %.1f LUFS from %.1f LUFS)\n",
		result.LimiterGain, config.Loudnorm.TargetI, result.InputLUFS)
	fmt.Fprintf(f, "  Projected TP:      %.1f dBTP (would exceed %.1f dBTP target by %.1f dB)\n",
		projectedTPWithoutLimiter, config.Loudnorm.TargetTP,
		projectedTPWithoutLimiter-config.Loudnorm.TargetTP)
	fmt.Fprintln(f, "")

	fmt.Fprintln(f, "Solution (CBS Volumax-inspired peak limiting):")
	fmt.Fprintf(f, "  Limiter Ceiling:   %.1f dBTP\n", result.LimiterCeiling)
	fmt.Fprintf(f, "  Peak Reduction:    %.1f dB (from %.1f to %.1f dBTP)\n",
		result.InputTP-result.LimiterCeiling, result.InputTP, result.LimiterCeiling)
	fmt.Fprintln(f, "")

	if result.PreGainDB > 0 {
		// idealCeiling = minLimiterCeilingDB - deficit, where deficit = PreGainDB
		idealCeiling := processor.MinLimiterCeilingDB - result.PreGainDB
		postGainTP := result.InputTP + result.PreGainDB

		fmt.Fprintln(f, "Pre-gain (ceiling deficit compensation):")
		fmt.Fprintf(f, "  Original Ceiling:    %.1f dBTP (clamped to alimiter minimum)\n", processor.MinLimiterCeilingDB)
		fmt.Fprintf(f, "  Ideal Ceiling:       %.1f dBTP\n", idealCeiling)
		fmt.Fprintf(f, "  Deficit:             %.1f dB\n", result.PreGainDB)
		fmt.Fprintf(f, "  Pre-gain Applied:    +%.1f dB (volume filter before alimiter)\n", result.PreGainDB)
		fmt.Fprintf(f, "  Re-derived Ceiling:  %.1f dBTP (post-gain, used for alimiter)\n", result.LimiterCeiling)
		fmt.Fprintf(f, "  Post-gain TP:        %.1f dBTP (projected)\n", postGainTP)
		fmt.Fprintln(f, "")
	}

	if result.Pass3FilterPrefix != "" {
		fmt.Fprintln(f, "Pass 3 measurement:")
		fmt.Fprintf(f, "  Prefix applied:    %s -> loudnorm\n", result.Pass3FilterPrefix)
		fmt.Fprintln(f, "  Pass 3 measures the post-limiter signal so loudnorm receives accurate")
		fmt.Fprintln(f, "  measured_I and measured_TP values in Pass 4.")
		fmt.Fprintln(f, "")
	}

	fmt.Fprintln(f, "Filter parameters:")
	fmt.Fprintln(f, "  Attack:    5 ms     (gentle - preserves transient shape)")
	fmt.Fprintln(f, "  Release:   100 ms   (smooth recovery, eliminates pumping)")
	fmt.Fprintln(f, "  ASC:       enabled  (Auto Soft Clipping for program-dependent smoothing)")
	fmt.Fprintln(f, "  ASC Level: 0.8      (high smoothing - Volumax characteristic)")
	fmt.Fprintln(f, "")

	fmt.Fprintln(f, "Rationale:")
	fmt.Fprintln(f, "  The CBS Volumax was the broadcast standard for transparent limiting.")
	fmt.Fprintln(f, "  Gentle attack preserves transients; smooth release is essentially inaudible.")
	fmt.Fprintln(f, "  Only peaks above the ceiling are affected (typically <5% of audio).")
	fmt.Fprintln(f, "")
}

// writeDiagnosticLoudnorm outputs detailed loudnorm normalisation diagnostics.
func writeDiagnosticLoudnorm(f *os.File, result *processor.NormalisationResult, config *processor.EffectiveFilterConfig) {
	if result == nil || !config.Loudnorm.Enabled {
		return
	}

	writeSection(f, "Diagnostic: Loudnorm")

	if result.Skipped {
		fmt.Fprintln(f, "Status: SKIPPED (already within target)")
		fmt.Fprintln(f, "")
		return
	}

	fmt.Fprintln(f, "Loudnorm configuration:")
	if result.LinearModeForced {
		fmt.Fprintf(f, "  Target I:   %.1f LUFS (adjusted from %.1f to preserve linear mode)\n",
			result.EffectiveTargetI, result.RequestedTargetI)
	} else {
		fmt.Fprintf(f, "  Target I:   %.1f LUFS\n", config.Loudnorm.TargetI)
	}
	fmt.Fprintf(f, "  Target TP:  %.1f dBTP\n", config.Loudnorm.TargetTP)
	fmt.Fprintf(f, "  Target LRA: %.1f LU\n", config.Loudnorm.TargetLRA)
	fmt.Fprintf(f, "  Mode:       %s\n", loudnormModeString(config.Loudnorm.Linear))
	fmt.Fprintf(f, "  Dual mono:  %v\n", config.Loudnorm.DualMono)
	fmt.Fprintf(f, "  Gain:       %+.2f dB\n", result.GainApplied)

	// Display loudnorm filter's unique diagnostic info (I/TP/LRA values are in Loudness table)
	if result.LoudnormStats != nil {
		stats := result.LoudnormStats
		fmt.Fprintln(f, "")
		fmt.Fprintln(f, "FFmpeg diagnostics:")
		fmt.Fprintf(f, "  Input Thresh:    %s LUFS\n", stats.InputThresh)
		fmt.Fprintf(f, "  Output Thresh:   %s LUFS\n", stats.OutputThresh)
		fmt.Fprintf(f, "  Norm Type:       %s\n", stats.NormalizationType)
		fmt.Fprintf(f, "  Target Offset:   %s dB\n", stats.TargetOffset)
	}

	fmt.Fprintln(f, "")
	effectiveDeviation := math.Abs(result.OutputLUFS - result.EffectiveTargetI)
	if result.WithinTarget {
		if result.LinearModeForced {
			requestedDeviation := math.Abs(result.OutputLUFS - result.RequestedTargetI)
			fmt.Fprintf(f, "Result: ✓ Linear mode preserved (%.2f LU from effective target, %.2f LU from requested)\n",
				effectiveDeviation, requestedDeviation)
		} else {
			fmt.Fprintf(f, "Result: ✓ Within target (deviation: %.2f LU)\n", effectiveDeviation)
		}
	} else {
		fmt.Fprintf(f, "Result: ⚠ Outside tolerance (deviation: %.2f LU)\n", effectiveDeviation)
	}

	fmt.Fprintln(f, "")
}
