// Package logging handles candidate display and rejection summaries for reports.

package logging

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

const candidateDisplayLimit = 10

type candidateDisplayEntry[T any] struct {
	index     int
	candidate T
}

func writeReportSilenceCandidateMetrics(f io.Writer, index int, c processor.SilenceCandidateMetrics, elected bool) {
	if !elected {
		writeReportCompactSilenceCandidateRow(f, index, c)
		return
	}

	electedLabel := ""
	if elected {
		electedLabel = ", elected"
	}
	fmt.Fprintf(f, "  Candidate %d:       %.1fs at %.1fs (score: %.3f%s)\n",
		index+1, c.Region.Duration.Seconds(), c.Region.Start.Seconds(), c.Score, electedLabel)

	if c.WasRefined {
		fmt.Fprintf(f, "    Refined:         %.1fs at %.1fs -> %.1fs at %.1fs (golden sub-region)\n",
			c.OriginalDuration.Seconds(),
			c.OriginalStart.Seconds(),
			c.Region.Duration.Seconds(),
			c.Region.Start.Seconds())
	}

	fmt.Fprintf(f, "    Amplitude:\n")
	fmt.Fprintf(f, "      RMS Level:     %.1f dBFS\n", c.RMSLevel)
	fmt.Fprintf(f, "      Peak Level:    %.1f dBFS\n", c.PeakLevel)
	fmt.Fprintf(f, "      Crest Factor:  %.1f dB\n", c.CrestFactor)
	fmt.Fprintf(f, "    Spectral:\n")
	fmt.Fprintf(f, "      Centroid:      %.0f Hz (%s)\n", c.Spectral.Centroid, interpretCentroid(c.Spectral.Centroid))
	fmt.Fprintf(f, "      Spread:        %.0f Hz\n", c.Spectral.Spread)
	fmt.Fprintf(f, "      Rolloff:       %.0f Hz\n", c.Spectral.Rolloff)
	fmt.Fprintf(f, "      Flatness:      %.3f (%s)\n", c.Spectral.Flatness, interpretFlatness(c.Spectral.Flatness))
	fmt.Fprintf(f, "      Entropy:       %.3f (%s)\n", c.Spectral.Entropy, interpretEntropy(c.Spectral.Entropy))
	fmt.Fprintf(f, "      Kurtosis:      %.1f (%s)\n", c.Spectral.Kurtosis, interpretKurtosis(c.Spectral.Kurtosis))
	fmt.Fprintf(f, "      Skewness:      %.2f\n", c.Spectral.Skewness)
	fmt.Fprintf(f, "      Flux:          %.4f\n", c.Spectral.Flux)
	fmt.Fprintf(f, "      Slope:         %.2e\n", c.Spectral.Slope)
	fmt.Fprintf(f, "    Loudness:\n")
	fmt.Fprintf(f, "      Momentary:     %.1f LUFS\n", c.MomentaryLUFS)
	fmt.Fprintf(f, "      Short-term:    %.1f LUFS\n", c.ShortTermLUFS)
	fmt.Fprintf(f, "      True Peak:     %.1f dBTP\n", c.TruePeak)
}

func writeReportCompactSilenceCandidateRow(f io.Writer, index int, c processor.SilenceCandidateMetrics) {
	fmt.Fprintf(f, "  Candidate %d:       %.1fs at %.1fs (score: %.3f)\n",
		index+1, c.Region.Duration.Seconds(), c.Region.Start.Seconds(), c.Score)
	fmt.Fprintf(f, "    RMS: %.1f dBFS, Crest: %.1f dB, Entropy: %.3f (%s)\n",
		c.RMSLevel, c.CrestFactor, c.Spectral.Entropy, interpretEntropy(c.Spectral.Entropy))
}

// writeReportRejectionSummary outputs a compact summary of rejected silence candidates to the report file.
func writeReportRejectionSummary(f *os.File, candidates []processor.SilenceCandidateMetrics) {
	writeReportCandidateRejectionSummary(f, silenceRejectionSummary(candidates))
}

func rankedSilenceCandidateEntries(measurements *processor.AudioMeasurements) (*candidateDisplayEntry[processor.SilenceCandidateMetrics], []candidateDisplayEntry[processor.SilenceCandidateMetrics]) {
	if measurements == nil || len(measurements.SilenceCandidates) == 0 {
		return nil, nil
	}

	return rankedCandidateEntries(
		measurements.SilenceCandidates,
		func(c processor.SilenceCandidateMetrics) bool { return isSelectedSilenceCandidate(c, measurements) },
		func(c processor.SilenceCandidateMetrics) time.Duration { return c.Region.Start },
		func(c processor.SilenceCandidateMetrics) float64 { return c.Score },
	)
}

func isSelectedSilenceCandidate(c processor.SilenceCandidateMetrics, measurements *processor.AudioMeasurements) bool {
	if measurements == nil || measurements.NoiseProfile == nil {
		return false
	}
	if measurements.NoiseProfile.WasRefined {
		return c.Region.Start == measurements.NoiseProfile.OriginalStart
	}
	return c.Region.Start == measurements.NoiseProfile.Start
}

func writeReportSpeechCandidateMetrics(f io.Writer, index int, c processor.SpeechCandidateMetrics, elected bool) {
	if !elected {
		writeReportCompactSpeechCandidateRow(f, index, c)
		return
	}

	electedLabel := ""
	if elected {
		electedLabel = ", elected"
	}
	fmt.Fprintf(f, "  Candidate %d:       %.1fs at %.1fs (score: %.3f%s)\n",
		index+1, c.Region.Duration.Seconds(), c.Region.Start.Seconds(), c.Score, electedLabel)

	if c.WasRefined {
		fmt.Fprintf(f, "    Refined:         %.1fs at %.1fs -> %.1fs at %.1fs (golden sub-region)\n",
			c.OriginalDuration.Seconds(),
			c.OriginalStart.Seconds(),
			c.Region.Duration.Seconds(),
			c.Region.Start.Seconds())
	}

	fmt.Fprintf(f, "    Amplitude:\n")
	fmt.Fprintf(f, "      RMS Level:     %.1f dBFS\n", c.RMSLevel)
	fmt.Fprintf(f, "      Peak Level:    %.1f dBFS\n", c.PeakLevel)
	fmt.Fprintf(f, "      Crest Factor:  %.1f dB\n", c.CrestFactor)
	fmt.Fprintf(f, "    Spectral:\n")
	fmt.Fprintf(f, "      Centroid:      %.0f Hz (%s)\n", c.Spectral.Centroid, interpretCentroid(c.Spectral.Centroid))
	fmt.Fprintf(f, "      Spread:        %.0f Hz\n", c.Spectral.Spread)
	fmt.Fprintf(f, "      Rolloff:       %.0f Hz\n", c.Spectral.Rolloff)
	fmt.Fprintf(f, "      Flatness:      %.3f (%s)\n", c.Spectral.Flatness, interpretFlatness(c.Spectral.Flatness))
	fmt.Fprintf(f, "      Entropy:       %.3f (%s)\n", c.Spectral.Entropy, interpretEntropy(c.Spectral.Entropy))
	fmt.Fprintf(f, "      Kurtosis:      %.1f (%s)\n", c.Spectral.Kurtosis, interpretKurtosis(c.Spectral.Kurtosis))
	fmt.Fprintf(f, "      Skewness:      %.2f\n", c.Spectral.Skewness)
	fmt.Fprintf(f, "      Flux:          %.4f\n", c.Spectral.Flux)
	fmt.Fprintf(f, "      Slope:         %.2e\n", c.Spectral.Slope)
	if c.VoicingDensity > 0 {
		fmt.Fprintf(f, "    Voicing Density: %.1f%%\n", c.VoicingDensity*100)
	}
	fmt.Fprintf(f, "    Loudness:\n")
	fmt.Fprintf(f, "      Momentary:     %.1f LUFS\n", c.MomentaryLUFS)
	fmt.Fprintf(f, "      Short-term:    %.1f LUFS\n", c.ShortTermLUFS)
	fmt.Fprintf(f, "      True Peak:     %.1f dBTP\n", c.TruePeak)
}

func writeReportCompactSpeechCandidateRow(f io.Writer, index int, c processor.SpeechCandidateMetrics) {
	fmt.Fprintf(f, "  Candidate %d:       %.1fs at %.1fs (score: %.3f)\n",
		index+1, c.Region.Duration.Seconds(), c.Region.Start.Seconds(), c.Score)
	fmt.Fprintf(f, "    RMS: %.1f dBFS, Crest: %.1f dB, Centroid: %.0f Hz (%s)\n",
		c.RMSLevel, c.CrestFactor, c.Spectral.Centroid, interpretCentroid(c.Spectral.Centroid))
}

func rankedSpeechCandidateEntries(measurements *processor.AudioMeasurements) (*candidateDisplayEntry[processor.SpeechCandidateMetrics], []candidateDisplayEntry[processor.SpeechCandidateMetrics]) {
	if measurements == nil || len(measurements.SpeechCandidates) == 0 {
		return nil, nil
	}

	return rankedCandidateEntries(
		measurements.SpeechCandidates,
		func(c processor.SpeechCandidateMetrics) bool { return isSelectedSpeechCandidate(c, measurements) },
		func(c processor.SpeechCandidateMetrics) time.Duration { return c.Region.Start },
		func(c processor.SpeechCandidateMetrics) float64 { return c.Score },
	)
}

func rankedCandidateEntries[T any](candidates []T, isSelected func(T) bool, start func(T) time.Duration, score func(T) float64) (*candidateDisplayEntry[T], []candidateDisplayEntry[T]) {
	entries := make([]candidateDisplayEntry[T], 0, len(candidates))
	var elected *candidateDisplayEntry[T]
	for i, c := range candidates {
		entry := candidateDisplayEntry[T]{index: i, candidate: c}
		if isSelected(c) {
			electedEntry := entry
			elected = &electedEntry
			continue
		}
		if score(c) == 0.0 {
			continue
		}
		entries = append(entries, entry)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		leftStart := start(entries[i].candidate)
		rightStart := start(entries[j].candidate)
		if leftStart == rightStart {
			return entries[i].index < entries[j].index
		}
		return leftStart < rightStart
	})

	return elected, selectCandidateEntries(entries)
}

func selectCandidateEntries[T any](entries []candidateDisplayEntry[T]) []candidateDisplayEntry[T] {
	displayCount := min(candidateDisplayLimit, len(entries))

	displayed := make([]candidateDisplayEntry[T], displayCount)
	copy(displayed, entries[:displayCount])
	return displayed
}

func isSelectedSpeechCandidate(c processor.SpeechCandidateMetrics, measurements *processor.AudioMeasurements) bool {
	if measurements == nil || measurements.SpeechProfile == nil {
		return false
	}
	if c.WasRefined {
		return c.OriginalStart == measurements.SpeechProfile.OriginalStart
	}
	return c.Region.Start == measurements.SpeechProfile.Region.Start
}

func writeCandidateDisplaySummary(f *os.File, totalCandidates int, hasElected bool, displayedCandidates int) {
	summary := candidateDisplaySummary(totalCandidates, hasElected, displayedCandidates)
	if summary == "" {
		return
	}
	fmt.Fprintf(f, "Displayed:           %s\n", summary)
}

func candidateDisplaySummary(totalCandidates int, hasElected bool, displayedCandidates int) string {
	if totalCandidates <= 0 {
		return ""
	}

	electedCount := 0
	if hasElected {
		electedCount = 1
	}
	if displayedCandidates < 0 {
		displayedCandidates = 0
	}
	omitted := totalCandidates - displayedCandidates
	omitted -= electedCount
	if omitted < 0 {
		omitted = 0
	}

	if hasElected && displayedCandidates == 0 {
		return fmt.Sprintf("elected (%d omitted)", omitted)
	}

	displayLabel := fmt.Sprintf("%d chronological", displayedCandidates)
	if omitted > 0 && displayedCandidates == candidateDisplayLimit {
		displayLabel = fmt.Sprintf("top %d chronological", candidateDisplayLimit)
	}
	if hasElected {
		return fmt.Sprintf("elected + %s (%d omitted)", displayLabel, omitted)
	}
	return fmt.Sprintf("%s (%d omitted)", displayLabel, omitted)
}

func writeReportSpeechRejectionSummary(f *os.File, candidates []processor.SpeechCandidateMetrics) {
	writeReportCandidateRejectionSummary(f, speechRejectionSummary(candidates))
}

func writeReportCandidateRejectionSummary(w io.Writer, summary string) {
	fmt.Fprintf(w, "Rejected:            %s\n", summary)
}

func writeAnalysisSilenceCandidates(w io.Writer, measurements *processor.AudioMeasurements) {
	if len(measurements.SilenceCandidates) == 0 {
		writeAnalysisSilenceFallback(w, measurements)
		return
	}

	electedCandidate, displayCandidates := rankedSilenceCandidateEntries(measurements)
	writeAnalysisCandidateFlow(
		w,
		len(measurements.SilenceCandidates),
		electedCandidate,
		displayCandidates,
		func() {
			if measurements.VoiceActivated {
				fmt.Fprintln(w, "  Voice-activated recording detected")
			}
		},
		func(w io.Writer, entry candidateDisplayEntry[processor.SilenceCandidateMetrics]) {
			writeAnalysisSilenceCandidateMetrics(w, entry, measurements)
		},
		func(w io.Writer, entry candidateDisplayEntry[processor.SilenceCandidateMetrics]) {
			writeCompactAnalysisSilenceCandidateRow(w, entry.index, entry.candidate)
		},
		func() {
			writeSilenceRejectionSummary(w, measurements.SilenceCandidates)
		},
	)
}

func writeAnalysisSilenceFallback(w io.Writer, measurements *processor.AudioMeasurements) {
	if measurements.NoiseProfile != nil {
		writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
			{"Sample", fmt.Sprintf("%.1fs at %s", measurements.NoiseProfile.Duration.Seconds(), formatTimestamp(measurements.NoiseProfile.Start))},
			{"Noise Floor", fmt.Sprintf("%.1f dBFS", measurements.NoiseProfile.MeasuredNoiseFloor)},
		})
		return
	}

	fmt.Fprintln(w, "  No silence detected")
	if measurements.VoiceActivated {
		fmt.Fprintln(w, "  Voice-activated recording detected")
	}
}

func writeAnalysisSpeechCandidates(w io.Writer, measurements *processor.AudioMeasurements) {
	if len(measurements.SpeechCandidates) == 0 {
		writeAnalysisSpeechFallback(w, measurements)
		return
	}

	electedCandidate, displayCandidates := rankedSpeechCandidateEntries(measurements)
	writeAnalysisCandidateFlow(
		w,
		len(measurements.SpeechCandidates),
		electedCandidate,
		displayCandidates,
		nil,
		writeAnalysisSpeechCandidateMetrics,
		func(w io.Writer, entry candidateDisplayEntry[processor.SpeechCandidateMetrics]) {
			writeCompactAnalysisSpeechCandidateRow(w, entry.index, entry.candidate)
		},
		func() {
			writeSpeechRejectionSummary(w, measurements.SpeechCandidates)
		},
	)
}

func writeAnalysisSpeechFallback(w io.Writer, measurements *processor.AudioMeasurements) {
	if measurements.SpeechProfile != nil {
		writeAnalysisMetricRows(w, "  ", 15, []analysisMetricSpec{
			{"Sample", fmt.Sprintf("%.1fs at %s", measurements.SpeechProfile.Region.Duration.Seconds(), formatTimestamp(measurements.SpeechProfile.Region.Start))},
			{"RMS Level", fmt.Sprintf("%.1f dBFS", measurements.SpeechProfile.RMSLevel)},
		})
		return
	}

	fmt.Fprintln(w, "  No speech profile available")
}

func writeAnalysisCandidateFlow[T any](
	w io.Writer,
	totalCandidates int,
	electedCandidate *candidateDisplayEntry[T],
	displayCandidates []candidateDisplayEntry[T],
	afterSummary func(),
	writeElected func(io.Writer, candidateDisplayEntry[T]),
	writeDisplayed func(io.Writer, candidateDisplayEntry[T]),
	writeRejected func(),
) {
	fmt.Fprintf(w, "  Candidates:     %d evaluated\n", totalCandidates)
	if summary := candidateDisplaySummary(totalCandidates, electedCandidate != nil, len(displayCandidates)); summary != "" {
		fmt.Fprintf(w, "  Displayed:      %s\n", summary)
	}
	if afterSummary != nil {
		afterSummary()
	}

	if electedCandidate != nil || len(displayCandidates) > 0 {
		fmt.Fprintln(w)
		if electedCandidate != nil {
			writeElected(w, *electedCandidate)
			fmt.Fprintln(w)
		}
		for _, entry := range displayCandidates {
			writeDisplayed(w, entry)
			fmt.Fprintln(w)
		}
	}

	writeRejected()
}

// writeSilenceCandidateMetrics writes the metric lines for a single silence candidate.
func writeSilenceCandidateMetrics(w io.Writer, c processor.SilenceCandidateMetrics) {
	writeAnalysisMetricRows(w, "      ", 12, []analysisMetricSpec{
		{"Score", fmt.Sprintf("%.3f", c.Score)},
		{"RMS Level", fmt.Sprintf("%.1f dBFS", c.RMSLevel)},
		{"Peak Level", fmt.Sprintf("%.1f dBFS", c.PeakLevel)},
		{"Crest", fmt.Sprintf("%.1f dB", c.CrestFactor)},
		{"Entropy", fmt.Sprintf("%.3f (%s)", c.Spectral.Entropy, interpretEntropy(c.Spectral.Entropy))},
		{"Flatness", fmt.Sprintf("%.3f (%s)", c.Spectral.Flatness, interpretFlatness(c.Spectral.Flatness))},
		{"Kurtosis", fmt.Sprintf("%.1f (%s)", c.Spectral.Kurtosis, interpretKurtosis(c.Spectral.Kurtosis))},
		{"Centroid", fmt.Sprintf("%.0f Hz", c.Spectral.Centroid)},
	})
}

func writeAnalysisSilenceCandidateMetrics(w io.Writer, entry candidateDisplayEntry[processor.SilenceCandidateMetrics], measurements *processor.AudioMeasurements) {
	c := entry.candidate
	fmt.Fprintf(w, "  #%d: %.1fs at %s (elected)\n",
		entry.index+1, c.Region.Duration.Seconds(), formatTimestamp(c.Region.Start))
	if measurements.NoiseProfile.WasRefined {
		fmt.Fprintf(w, "      Refined:     %.1fs at %s (golden sub-region)\n",
			measurements.NoiseProfile.Duration.Seconds(), formatTimestamp(measurements.NoiseProfile.Start))
	}
	writeSilenceCandidateMetrics(w, c)
}

func writeAnalysisSpeechCandidateMetrics(w io.Writer, entry candidateDisplayEntry[processor.SpeechCandidateMetrics]) {
	c := entry.candidate
	fmt.Fprintf(w, "  #%d: %.1fs at %s (elected)\n",
		entry.index+1, c.Region.Duration.Seconds(), formatTimestamp(c.Region.Start))

	if c.WasRefined {
		fmt.Fprintf(w, "      Refined:     %.1fs at %s -> %.1fs at %s (golden sub-region)\n",
			c.OriginalDuration.Seconds(),
			formatTimestamp(c.OriginalStart),
			c.Region.Duration.Seconds(),
			formatTimestamp(c.Region.Start))
	}

	rows := []analysisMetricSpec{
		{"Score", fmt.Sprintf("%.2f", c.Score)},
		{"RMS Level", fmt.Sprintf("%.1f dBFS", c.RMSLevel)},
		{"Crest", fmt.Sprintf("%.1f dB", c.CrestFactor)},
		{"Centroid", fmt.Sprintf("%.0f Hz (%s)", c.Spectral.Centroid, interpretCentroid(c.Spectral.Centroid))},
		{"Kurtosis", fmt.Sprintf("%.1f (%s)", c.Spectral.Kurtosis, interpretKurtosis(c.Spectral.Kurtosis))},
	}
	if c.VoicingDensity > 0 {
		rows = append(rows, analysisMetricSpec{"Voicing", fmt.Sprintf("%.0f%%", c.VoicingDensity*100)})
	}
	writeAnalysisMetricRows(w, "      ", 12, rows)
}

func writeCompactAnalysisSilenceCandidateRow(w io.Writer, index int, c processor.SilenceCandidateMetrics) {
	fmt.Fprintf(w, "  #%d: %.1fs at %s (score: %.3f)\n",
		index+1, c.Region.Duration.Seconds(), formatTimestamp(c.Region.Start), c.Score)
	fmt.Fprintf(w, "      RMS: %.1f dBFS, Crest: %.1f dB, Entropy: %.3f (%s)\n",
		c.RMSLevel, c.CrestFactor, c.Spectral.Entropy, interpretEntropy(c.Spectral.Entropy))
}

func writeCompactAnalysisSpeechCandidateRow(w io.Writer, index int, c processor.SpeechCandidateMetrics) {
	fmt.Fprintf(w, "  #%d: %.1fs at %s (score: %.2f)\n",
		index+1, c.Region.Duration.Seconds(), formatTimestamp(c.Region.Start), c.Score)
	fmt.Fprintf(w, "      RMS: %.1f dBFS, Crest: %.1f dB, Centroid: %.0f Hz (%s)\n",
		c.RMSLevel, c.CrestFactor, c.Spectral.Centroid, interpretCentroid(c.Spectral.Centroid))
}

// writeSilenceRejectionSummary outputs a compact summary of rejected silence candidates.
// Groups zero-scored candidates by rejection reason extracted from TransientWarning.
func writeSilenceRejectionSummary(w io.Writer, candidates []processor.SilenceCandidateMetrics) {
	writeAnalysisCandidateRejectionSummary(w, silenceRejectionSummary(candidates))
}

func writeSpeechRejectionSummary(w io.Writer, candidates []processor.SpeechCandidateMetrics) {
	writeAnalysisCandidateRejectionSummary(w, speechRejectionSummary(candidates))
}

func writeAnalysisCandidateRejectionSummary(w io.Writer, summary string) {
	fmt.Fprintf(w, "  Rejected:       %s\n", summary)
}

func silenceRejectionSummary(candidates []processor.SilenceCandidateMetrics) string {
	reasonCounts := make(map[string]int)
	for _, c := range candidates {
		if c.Score != 0.0 {
			continue
		}
		reason := classifyRejectionReason(c.TransientWarning)
		reasonCounts[reason]++
	}

	return formatRejectionSummary(reasonCounts, []string{"digital silence", "crosstalk", "transient contamination", "too loud"})
}

func speechRejectionSummary(candidates []processor.SpeechCandidateMetrics) string {
	reasonCounts := make(map[string]int)
	for _, c := range candidates {
		if c.Score != 0.0 {
			continue
		}
		reasonCounts["zero score"]++
	}

	return formatRejectionSummary(reasonCounts, []string{"zero score"})
}

func formatRejectionSummary(reasonCounts map[string]int, order []string) string {
	if len(reasonCounts) == 0 {
		return "0"
	}

	var parts []string
	for _, reason := range order {
		if count, ok := reasonCounts[reason]; ok {
			parts = append(parts, fmt.Sprintf("%d %s", count, reason))
			delete(reasonCounts, reason)
		}
	}
	for reason, count := range reasonCounts {
		parts = append(parts, fmt.Sprintf("%d %s", count, reason))
	}

	return strings.Join(parts, ", ")
}

// classifyRejectionReason maps a TransientWarning string to a short label.
func classifyRejectionReason(warning string) string {
	switch {
	case strings.Contains(warning, "digital silence"):
		return "digital silence"
	case strings.Contains(warning, "crosstalk"):
		return "crosstalk"
	case strings.Contains(warning, "transient contamination"):
		return "transient contamination"
	case warning == "":
		return "too loud"
	default:
		return "too loud"
	}
}
