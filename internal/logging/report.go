// Package logging handles generation of analysis reports for processed audio files

package logging

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// linearToDb converts linear amplitude to decibels
func linearToDb(linear float64) float64 {
	if linear <= 0 {
		return -100.0 // Effectively -infinity
	}
	return 20.0 * math.Log10(linear)
}

// formatComparison returns "(unchanged)" if values match within tolerance, otherwise "(was X unit)"
func formatComparison(output, input float64, unit string, decimals int) string {
	// Use tolerance based on decimal places shown
	tolerance := math.Pow(10, -float64(decimals)) * 0.5
	if math.Abs(output-input) < tolerance {
		return "(unchanged)"
	}
	format := fmt.Sprintf("(was %%.%df %s)", decimals, unit)
	return fmt.Sprintf(format, input)
}

// formatComparisonNoUnit returns "(unchanged)" if values match within tolerance, otherwise "(was X)"
func formatComparisonNoUnit(output, input float64, decimals int) string {
	tolerance := math.Pow(10, -float64(decimals)) * 0.5
	if math.Abs(output-input) < tolerance {
		return "(unchanged)"
	}
	format := fmt.Sprintf("(was %%.%df)", decimals)
	return fmt.Sprintf(format, input)
}

// ReportData contains all the information needed to generate an analysis report
type ReportData struct {
	InputPath    string
	OutputPath   string
	StartTime    time.Time
	EndTime      time.Time
	Pass1Time    time.Duration
	Pass2Time    time.Duration
	Result       *processor.ProcessingResult
	SampleRate   int
	Channels     int
	DurationSecs float64 // Duration in seconds
}

// GenerateReport creates a detailed analysis report and saves it alongside the output file
// The report filename will be <output>-processed.log
func GenerateReport(data ReportData) error {
	// Generate report filename: presenter1-processed.flac → presenter1-processed.log
	logPath := strings.TrimSuffix(data.OutputPath, filepath.Ext(data.OutputPath)) + ".log"

	// Create report file
	f, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer f.Close()

	// Write report content
	fmt.Fprintln(f, "Jivetalking Analysis Report")
	fmt.Fprintln(f, "============================")
	fmt.Fprintf(f, "File: %s\n", filepath.Base(data.InputPath))
	fmt.Fprintf(f, "Processed: %s\n", data.EndTime.Format("2006-01-02 15:04:05 MST"))
	fmt.Fprintf(f, "Duration: %s\n", formatDuration(time.Duration(data.DurationSecs*float64(time.Second))))
	fmt.Fprintln(f, "")

	// Pass 1: Input Analysis
	fmt.Fprintln(f, "Pass 1: Input Analysis")
	fmt.Fprintln(f, "----------------------")
	if data.Result != nil && data.Result.Measurements != nil {
		m := data.Result.Measurements
		fmt.Fprintf(f, "Integrated Loudness: %.1f LUFS\n", m.InputI)
		fmt.Fprintf(f, "True Peak:           %.1f dBTP\n", m.InputTP)
		fmt.Fprintf(f, "Loudness Range:      %.1f LU\n", m.InputLRA)
		fmt.Fprintf(f, "Noise Floor:         %.1f dB (measured)\n", m.NoiseFloor)
		fmt.Fprintf(f, "Dynamic Range:       %.1f dB\n", m.DynamicRange)
		fmt.Fprintf(f, "RMS Level:           %.1f dBFS\n", m.RMSLevel)
		fmt.Fprintf(f, "Peak Level:          %.1f dBFS\n", m.PeakLevel)
		fmt.Fprintf(f, "Spectral Centroid:   %.0f Hz\n", m.SpectralCentroid)
		fmt.Fprintf(f, "Spectral Rolloff:    %.0f Hz\n", m.SpectralRolloff)

		// Additional signal quality measurements
		if m.DCOffset != 0 {
			fmt.Fprintf(f, "DC Offset:           %.6f\n", m.DCOffset)
		}
		if m.FlatFactor > 0 {
			fmt.Fprintf(f, "Flat Factor:         %.1f (clipping indicator)\n", m.FlatFactor)
		}
		if m.ZeroCrossingsRate > 0 {
			fmt.Fprintf(f, "Zero Crossings Rate: %.4f\n", m.ZeroCrossingsRate)
		}
		if m.MaxDifference > 0 {
			// Convert to percentage of full scale for readability
			// Max difference is in sample units; 32768 is full scale for 16-bit
			maxDiffPercent := (m.MaxDifference / 32768.0) * 100.0
			fmt.Fprintf(f, "Max Difference:      %.1f%% FS (transient indicator)\n", maxDiffPercent)
		}

		// Silence sample details (used for noise profile extraction)
		if m.NoiseProfile != nil {
			fmt.Fprintf(f, "Silence Sample:      %.1fs at %.1fs\n",
				m.NoiseProfile.Duration.Seconds(),
				m.NoiseProfile.Start.Seconds())
			fmt.Fprintf(f, "  Noise Floor:       %.1f dBFS (RMS)\n", m.NoiseProfile.MeasuredNoiseFloor)
			fmt.Fprintf(f, "  Peak Level:        %.1f dBFS\n", m.NoiseProfile.PeakLevel)
			fmt.Fprintf(f, "  Crest Factor:      %.1f dB\n", m.NoiseProfile.CrestFactor)
			if m.NoiseProfile.Entropy > 0 {
				// Classify noise type based on entropy
				noiseType := "broadband (hiss)"
				if m.NoiseProfile.Entropy < 0.7 {
					noiseType = "tonal (hum/buzz)"
				} else if m.NoiseProfile.Entropy < 0.9 {
					noiseType = "mixed"
				}
				fmt.Fprintf(f, "  Noise Character:   %s (entropy %.3f)\n", noiseType, m.NoiseProfile.Entropy)
			}
			if m.NoiseProfile.ExtractionWarning != "" {
				fmt.Fprintf(f, "  Warning:           %s\n", m.NoiseProfile.ExtractionWarning)
			}
		} else if len(m.SilenceRegions) > 0 {
			// Show first silence region even if profile extraction failed
			r := m.SilenceRegions[0]
			fmt.Fprintf(f, "Silence Detected:    %.1fs at %.1fs (no profile extracted)\n",
				r.Duration.Seconds(), r.Start.Seconds())
		}
	}
	fmt.Fprintf(f, "Sample Rate:         %d Hz\n", data.SampleRate)
	fmt.Fprintf(f, "Channels:            %d (%s)\n", data.Channels, channelName(data.Channels))
	fmt.Fprintln(f, "")

	// Pass 2: Filter Chain (in processing order)
	if data.Result != nil && data.Result.Config != nil {
		formatFilterChain(f, data.Result.Config, data.Result.Measurements)
		fmt.Fprintln(f, "")
	}

	// Pass 2: Output Analysis
	fmt.Fprintln(f, "Pass 2: Output Analysis")
	fmt.Fprintln(f, "-----------------------")
	if data.Result != nil {
		fmt.Fprintf(f, "Output File:         %s\n", filepath.Base(data.OutputPath))

		if data.Result.OutputMeasurements != nil {
			om := data.Result.OutputMeasurements
			m := data.Result.Measurements // Input measurements for comparison

			if m != nil {
				fmt.Fprintf(f, "Integrated Loudness: %.1f LUFS %s\n", om.OutputI, formatComparison(om.OutputI, m.InputI, "LUFS", 1))
				fmt.Fprintf(f, "True Peak:           %.1f dBTP %s\n", om.OutputTP, formatComparison(om.OutputTP, m.InputTP, "dBTP", 1))
				fmt.Fprintf(f, "Loudness Range:      %.1f LU %s\n", om.OutputLRA, formatComparison(om.OutputLRA, m.InputLRA, "LU", 1))
				fmt.Fprintf(f, "Dynamic Range:       %.1f dB %s\n", om.DynamicRange, formatComparison(om.DynamicRange, m.DynamicRange, "dB", 1))
				fmt.Fprintf(f, "RMS Level:           %.1f dBFS %s\n", om.RMSLevel, formatComparison(om.RMSLevel, m.RMSLevel, "dBFS", 1))
				fmt.Fprintf(f, "Peak Level:          %.1f dBFS %s\n", om.PeakLevel, formatComparison(om.PeakLevel, m.PeakLevel, "dBFS", 1))
				fmt.Fprintf(f, "Spectral Centroid:   %.0f Hz %s\n", om.SpectralCentroid, formatComparison(om.SpectralCentroid, m.SpectralCentroid, "Hz", 0))
				fmt.Fprintf(f, "Spectral Rolloff:    %.0f Hz %s\n", om.SpectralRolloff, formatComparison(om.SpectralRolloff, m.SpectralRolloff, "Hz", 0))
			} else {
				fmt.Fprintf(f, "Integrated Loudness: %.1f LUFS\n", om.OutputI)
				fmt.Fprintf(f, "True Peak:           %.1f dBTP\n", om.OutputTP)
				fmt.Fprintf(f, "Loudness Range:      %.1f LU\n", om.OutputLRA)
				fmt.Fprintf(f, "Dynamic Range:       %.1f dB\n", om.DynamicRange)
				fmt.Fprintf(f, "RMS Level:           %.1f dBFS\n", om.RMSLevel)
				fmt.Fprintf(f, "Peak Level:          %.1f dBFS\n", om.PeakLevel)
				fmt.Fprintf(f, "Spectral Centroid:   %.0f Hz\n", om.SpectralCentroid)
				fmt.Fprintf(f, "Spectral Rolloff:    %.0f Hz\n", om.SpectralRolloff)
			}
			if om.ZeroCrossingsRate > 0 {
				if m != nil && m.ZeroCrossingsRate > 0 {
					fmt.Fprintf(f, "Zero Crossings Rate: %.4f %s\n", om.ZeroCrossingsRate, formatComparisonNoUnit(om.ZeroCrossingsRate, m.ZeroCrossingsRate, 4))
				} else {
					fmt.Fprintf(f, "Zero Crossings Rate: %.4f\n", om.ZeroCrossingsRate)
				}
			}
			if om.MaxDifference > 0 {
				maxDiffPercent := (om.MaxDifference / 32768.0) * 100.0
				if m != nil && m.MaxDifference > 0 {
					inputMaxDiffPercent := (m.MaxDifference / 32768.0) * 100.0
					fmt.Fprintf(f, "Max Difference:      %.1f%% FS %s\n", maxDiffPercent, formatComparison(maxDiffPercent, inputMaxDiffPercent, "% FS", 1))
				} else {
					fmt.Fprintf(f, "Max Difference:      %.1f%% FS (transient indicator)\n", maxDiffPercent)
				}
			}

			// Show silence sample comparison (same region as Pass 1)
			if om.SilenceSample != nil && data.Result.Measurements != nil && data.Result.Measurements.NoiseProfile != nil {
				ss := om.SilenceSample
				np := data.Result.Measurements.NoiseProfile
				fmt.Fprintf(f, "Silence Sample:      %.1fs at %.1fs\n", ss.Duration.Seconds(), ss.Start.Seconds())

				// Noise Floor with delta if changed
				if math.Abs(ss.NoiseFloor-np.MeasuredNoiseFloor) < 0.05 {
					fmt.Fprintf(f, "  Noise Floor:       %.1f dBFS (unchanged)\n", ss.NoiseFloor)
				} else {
					fmt.Fprintf(f, "  Noise Floor:       %.1f dBFS (was %.1f dBFS, %+.1f dB)\n",
						ss.NoiseFloor, np.MeasuredNoiseFloor, ss.NoiseFloor-np.MeasuredNoiseFloor)
				}

				// Peak Level with delta if changed
				if math.Abs(ss.PeakLevel-np.PeakLevel) < 0.05 {
					fmt.Fprintf(f, "  Peak Level:        %.1f dBFS (unchanged)\n", ss.PeakLevel)
				} else {
					fmt.Fprintf(f, "  Peak Level:        %.1f dBFS (was %.1f dBFS, %+.1f dB)\n",
						ss.PeakLevel, np.PeakLevel, ss.PeakLevel-np.PeakLevel)
				}

				// Crest Factor
				if math.Abs(ss.CrestFactor-np.CrestFactor) < 0.05 {
					fmt.Fprintf(f, "  Crest Factor:      %.1f dB (unchanged)\n", ss.CrestFactor)
				} else {
					fmt.Fprintf(f, "  Crest Factor:      %.1f dB %s\n", ss.CrestFactor, formatComparison(ss.CrestFactor, np.CrestFactor, "dB", 1))
				}

				if ss.Entropy > 0 {
					// Classify noise type based on entropy
					noiseType := "broadband (hiss)"
					if ss.Entropy < 0.7 {
						noiseType = "tonal (hum/buzz)"
					} else if ss.Entropy < 0.9 {
						noiseType = "mixed"
					}
					// Show with comparison to input
					inputNoiseType := "broadband (hiss)"
					if np.Entropy < 0.7 {
						inputNoiseType = "tonal (hum/buzz)"
					} else if np.Entropy < 0.9 {
						inputNoiseType = "mixed"
					}
					if noiseType == inputNoiseType && math.Abs(ss.Entropy-np.Entropy) < 0.0005 {
						fmt.Fprintf(f, "  Noise Character:   %s (unchanged)\n", noiseType)
					} else if noiseType == inputNoiseType {
						fmt.Fprintf(f, "  Noise Character:   %s (entropy %.3f, was %.3f)\n", noiseType, ss.Entropy, np.Entropy)
					} else {
						fmt.Fprintf(f, "  Noise Character:   %s (was %s)\n", noiseType, inputNoiseType)
					}
				}
			}
		} else {
			fmt.Fprintln(f, "Note: Output measurements not available")
		}
	}
	fmt.Fprintln(f, "")

	// Processing Time
	fmt.Fprintln(f, "Processing Time")
	fmt.Fprintln(f, "---------------")
	fmt.Fprintf(f, "Pass 1 (Analysis):   %s\n", formatDuration(data.Pass1Time))
	fmt.Fprintf(f, "Pass 2 (Processing): %s\n", formatDuration(data.Pass2Time))
	totalTime := data.EndTime.Sub(data.StartTime)
	fmt.Fprintf(f, "Total Time:          %s\n", formatDuration(totalTime))

	if data.DurationSecs > 0 {
		audioDuration := time.Duration(data.DurationSecs * float64(time.Second))
		rtf := float64(audioDuration) / float64(totalTime)
		fmt.Fprintf(f, "Real-time Factor:    %.0fx\n", rtf)
	}
	fmt.Fprintln(f, "")

	return nil
}

// formatDuration formats a duration in a human-readable way
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}

	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60

	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}

	hours := minutes / 60
	minutes = minutes % 60
	return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
}

// channelName returns a human-readable channel name
func channelName(channels int) string {
	switch channels {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	default:
		return fmt.Sprintf("%d channels", channels)
	}
}

// formatFilterChain generates the filter chain section of the report.
// Iterates over filters in chain order, showing enabled/disabled status,
// key parameters, and adaptive rationale for each filter.
func formatFilterChain(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements) {
	fmt.Fprintln(f, "Filter Chain (in processing order)")
	fmt.Fprintln(f, "------------------------------------")

	for i, filterID := range cfg.FilterOrder {
		prefix := fmt.Sprintf("%2d. ", i+1)
		formatFilter(f, filterID, cfg, m, prefix)
	}
}

// formatFilter outputs details for a single filter
func formatFilter(f *os.File, filterID processor.FilterID, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	switch filterID {
	case processor.FilterDownmix:
		formatDownmixFilter(f, cfg, prefix)
	case processor.FilterAnalysis:
		formatAnalysisFilter(f, cfg, prefix)
	case processor.FilterSilenceDetect:
		formatSilenceDetectFilter(f, cfg, prefix)
	case processor.FilterResample:
		formatResampleFilter(f, cfg, prefix)
	case processor.FilterHighpass:
		formatHighpassFilter(f, cfg, m, prefix)
	case processor.FilterBandreject:
		formatBandrejectFilter(f, cfg, m, prefix)
	case processor.FilterAdeclick:
		formatAdeclickFilter(f, cfg, prefix)
	case processor.FilterAfftdn:
		formatAfftdnFilter(f, cfg, m, prefix)
	case processor.FilterArnndn:
		formatArnndnFilter(f, cfg, m, prefix)
	case processor.FilterAgate:
		formatAgateFilter(f, cfg, m, prefix)
	case processor.FilterAcompressor:
		formatAcompressorFilter(f, cfg, m, prefix)
	case processor.FilterDeesser:
		formatDeesserFilter(f, cfg, m, prefix)
	case processor.FilterSpeechnorm:
		formatSpeechnormFilter(f, cfg, m, prefix)
	case processor.FilterDynaudnorm:
		formatDynaudnormFilter(f, cfg, prefix)
	case processor.FilterBleedGate:
		formatBleedGateFilter(f, cfg, m, prefix)
	case processor.FilterAlimiter:
		formatAlimiterFilter(f, cfg, prefix)
	default:
		fmt.Fprintf(f, "%s%s: (unknown filter)\n", prefix, filterID)
	}
}

// formatHighpassFilter outputs highpass filter details
func formatHighpassFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.HighpassEnabled {
		fmt.Fprintf(f, "%shighpass: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%shighpass: %.0f Hz cutoff\n", prefix, cfg.HighpassFreq)

	// Show adaptive rationale
	if m != nil && m.SpectralCentroid > 0 {
		voiceType := "normal"
		if m.SpectralCentroid > 6000 {
			voiceType = "bright"
		} else if m.SpectralCentroid < 4000 {
			voiceType = "dark/warm"
		}
		fmt.Fprintf(f, "        Rationale: %s voice (centroid %.0f Hz)", voiceType, m.SpectralCentroid)

		// Show LUFS gap boost if applicable
		if m.InputI != 0 && cfg.TargetI-m.InputI > 15 {
			fmt.Fprintf(f, ", +boost for %.0f dB LUFS gap", cfg.TargetI-m.InputI)
		}
		fmt.Fprintln(f, "")
	}
}

// formatBandrejectFilter outputs bandreject (hum notch) filter details
func formatBandrejectFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.HumFilterEnabled {
		fmt.Fprintf(f, "%sbandreject: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sbandreject: %.0f Hz + %d harmonics (Q=%.0f)\n",
		prefix, cfg.HumFrequency, cfg.HumHarmonics, cfg.HumQ)

	if m != nil && m.NoiseProfile != nil {
		fmt.Fprintf(f, "        Rationale: tonal noise detected (entropy %.3f < 0.7)\n", m.NoiseProfile.Entropy)
	}
}

// formatAdeclickFilter outputs adeclick filter details
func formatAdeclickFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.AdeclickEnabled {
		fmt.Fprintf(f, "%sadeclick: DISABLED\n", prefix)
		return
	}

	method := "overlap-save"
	if cfg.AdeclickMethod == "a" {
		method = "overlap-add"
	}
	fmt.Fprintf(f, "%sadeclick: %s method\n", prefix, method)
}

// formatAfftdnFilter outputs afftdn filter details
func formatAfftdnFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.AfftdnEnabled {
		fmt.Fprintf(f, "%safftdn: DISABLED\n", prefix)
		return
	}

	// Method: profile-based or tracking
	method := "adaptive tracking"
	if cfg.NoiseProfilePath != "" {
		method = fmt.Sprintf("noise profile (%.2fs sample)", cfg.NoiseProfileDuration.Seconds())
	}

	fmt.Fprintf(f, "%safftdn: %.1f dB reduction, floor %.1f dB\n",
		prefix, cfg.NoiseReduction, cfg.NoiseFloor)
	fmt.Fprintf(f, "        Method: %s\n", method)

	// Show adaptive rationale
	if m != nil && m.InputI != 0 {
		lufsGap := cfg.TargetI - m.InputI
		fmt.Fprintf(f, "        Rationale: %.1f dB LUFS gap", lufsGap)

		if m.NoiseReductionHeadroom > 0 {
			quality := "typical"
			if m.NoiseReductionHeadroom < 15 {
				quality = "noisy (conservative NR)"
			} else if m.NoiseReductionHeadroom > 30 {
				quality = "clean (aggressive NR)"
			}
			fmt.Fprintf(f, ", headroom %.1f dB (%s)", m.NoiseReductionHeadroom, quality)
		}
		fmt.Fprintln(f, "")
	}
}

// formatArnndnFilter outputs arnndn filter details
func formatArnndnFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.ArnnDnEnabled {
		fmt.Fprintf(f, "%sarnndn: DISABLED\n", prefix)
		return
	}

	if cfg.ArnnDnDualPass {
		fmt.Fprintf(f, "%sarnndn: DUAL PASS (mix %.0f%% + %.0f%%)\n",
			prefix, cfg.ArnnDnMix*100, cfg.ArnnDnMix2*100)
		fmt.Fprintln(f, "        Mode: aggressive (high-noise source)")
	} else {
		fmt.Fprintf(f, "%sarnndn: mix %.0f%%\n", prefix, cfg.ArnnDnMix*100)
	}

	// Show rationale
	if m != nil && m.InputI != 0 {
		lufsGap := cfg.TargetI - m.InputI
		fmt.Fprintf(f, "        Rationale: LUFS gap %.1f dB, noise floor %.1f dB\n", lufsGap, m.NoiseFloor)
	}
}

// formatAgateFilter outputs agate filter details
func formatAgateFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.GateEnabled {
		fmt.Fprintf(f, "%sagate: DISABLED\n", prefix)
		return
	}

	thresholdDB := linearToDb(cfg.GateThreshold)
	rangeDB := linearToDb(cfg.GateRange)

	fmt.Fprintf(f, "%sagate: threshold %.1f dB, ratio %.1f:1\n", prefix, thresholdDB, cfg.GateRatio)
	fmt.Fprintf(f, "        Timing: attack %.0fms, release %.0fms\n", cfg.GateAttack, cfg.GateRelease)
	fmt.Fprintf(f, "        Range: %.1f dB reduction, knee %.1f\n", rangeDB, cfg.GateKnee)

	// Show rationale
	if m != nil {
		fmt.Fprintf(f, "        Rationale: noise floor %.1f dB + margin\n", m.NoiseFloor)
	}
}

// formatAcompressorFilter outputs acompressor filter details
func formatAcompressorFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.CompEnabled {
		fmt.Fprintf(f, "%sacompressor: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sacompressor: threshold %.0f dB, ratio %.1f:1\n", prefix, cfg.CompThreshold, cfg.CompRatio)
	fmt.Fprintf(f, "        Timing: attack %.0fms, release %.0fms\n", cfg.CompAttack, cfg.CompRelease)
	fmt.Fprintf(f, "        Makeup: %+.0f dB, mix %.0f%%, knee %.1f\n", cfg.CompMakeup, cfg.CompMix*100, cfg.CompKnee)

	// Show rationale
	if m != nil && m.DynamicRange > 0 {
		dynamicsType := "moderate"
		if m.DynamicRange > 30 {
			dynamicsType = "expressive (preserving transients)"
		} else if m.DynamicRange < 20 {
			dynamicsType = "already compressed"
		}
		fmt.Fprintf(f, "        Rationale: DR %.1f dB (%s), LRA %.1f LU\n", m.DynamicRange, dynamicsType, m.InputLRA)
	}
}

// formatDeesserFilter outputs deesser filter details
func formatDeesserFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.DeessEnabled || cfg.DeessIntensity == 0 {
		fmt.Fprintf(f, "%sdeesser: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sdeesser: intensity %.0f%%, amount %.0f%%, freq %.0f%%\n",
		prefix, cfg.DeessIntensity*100, cfg.DeessAmount*100, cfg.DeessFreq*100)

	// Show rationale
	if m != nil && m.SpectralCentroid > 0 {
		voiceType := "normal"
		if m.SpectralCentroid > 7000 {
			voiceType = "very bright"
		} else if m.SpectralCentroid > 6000 {
			voiceType = "bright"
		}
		fmt.Fprintf(f, "        Rationale: %s voice (centroid %.0f Hz, rolloff %.0f Hz)\n",
			voiceType, m.SpectralCentroid, m.SpectralRolloff)
	}
}

// formatSpeechnormFilter outputs speechnorm filter details
func formatSpeechnormFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.SpeechnormEnabled {
		fmt.Fprintf(f, "%sspeechnorm: DISABLED\n", prefix)
		return
	}

	expansionDB := 20.0 * math.Log10(cfg.SpeechnormExpansion)
	fmt.Fprintf(f, "%sspeechnorm: expansion %.1fx (%.1f dB), peak %.2f\n",
		prefix, cfg.SpeechnormExpansion, expansionDB, cfg.SpeechnormPeak)

	if cfg.SpeechnormRMS > 0 {
		fmt.Fprintf(f, "        RMS target: %.3f\n", cfg.SpeechnormRMS)
	}

	fmt.Fprintf(f, "        Smoothing: raise %.3f, fall %.3f\n", cfg.SpeechnormRaise, cfg.SpeechnormFall)

	// Show rationale
	if m != nil && m.InputI != 0 {
		lufsGap := cfg.TargetI - m.InputI
		fmt.Fprintf(f, "        Rationale: input %.1f LUFS, gap %.1f dB to target %.1f LUFS\n",
			m.InputI, lufsGap, cfg.TargetI)

		// Warn if expansion was capped
		if lufsGap > 20.0 {
			expectedLUFS := m.InputI + expansionDB
			fmt.Fprintf(f, "        Note: expansion capped at 10x (20 dB) for quality — expected output ~%.1f LUFS\n", expectedLUFS)
		}
	}
}

// formatDynaudnormFilter outputs dynaudnorm filter details
func formatDynaudnormFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.DynaudnormEnabled {
		fmt.Fprintf(f, "%sdynaudnorm: DISABLED\n", prefix)
		return
	}

	fmt.Fprintf(f, "%sdynaudnorm: frame %dms, filter size %d\n",
		prefix, cfg.DynaudnormFrameLen, cfg.DynaudnormFilterSize)
	fmt.Fprintf(f, "        Peak: %.2f, max gain %.1fx\n", cfg.DynaudnormPeakValue, cfg.DynaudnormMaxGain)
	fmt.Fprintln(f, "        Mode: conservative fixed parameters (prevents artifacts)")
}

// formatBleedGateFilter outputs bleed gate filter details
func formatBleedGateFilter(f *os.File, cfg *processor.FilterChainConfig, m *processor.AudioMeasurements, prefix string) {
	if !cfg.BleedGateEnabled {
		fmt.Fprintf(f, "%sbleedgate: DISABLED\n", prefix)
		return
	}

	thresholdDB := linearToDb(cfg.BleedGateThreshold)
	rangeDB := linearToDb(cfg.BleedGateRange)

	fmt.Fprintf(f, "%sbleedgate: threshold %.1f dB, ratio %.1f:1\n", prefix, thresholdDB, cfg.BleedGateRatio)
	fmt.Fprintf(f, "        Timing: attack %.0fms, release %.0fms\n", cfg.BleedGateAttack, cfg.BleedGateRelease)
	fmt.Fprintf(f, "        Range: %.1f dB reduction, knee %.1f\n", rangeDB, cfg.BleedGateKnee)

	// Show rationale
	if m != nil && m.NoiseProfile != nil {
		np := m.NoiseProfile
		hasBleed := np.CrestFactor > 15.0 || (np.PeakLevel-np.MeasuredNoiseFloor) > 20.0
		if hasBleed {
			fmt.Fprintf(f, "        Rationale: bleed detected (crest %.1f dB, peak-floor %.1f dB)\n",
				np.CrestFactor, np.PeakLevel-np.MeasuredNoiseFloor)
		} else {
			fmt.Fprintf(f, "        Rationale: noise amplification (predicted output above -40 dB)\n")
		}
	}
}

// formatAlimiterFilter outputs alimiter filter details
func formatAlimiterFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.LimiterEnabled {
		fmt.Fprintf(f, "%salimiter: DISABLED\n", prefix)
		return
	}

	ceilingDB := linearToDb(cfg.LimiterCeiling)
	fmt.Fprintf(f, "%salimiter: ceiling %.1f dBFS\n", prefix, ceilingDB)
	fmt.Fprintf(f, "        Timing: attack %.0fms, release %.0fms\n", cfg.LimiterAttack, cfg.LimiterRelease)
	fmt.Fprintln(f, "        Mode: brick-wall safety limiter")
}

// formatDownmixFilter outputs downmix filter details
func formatDownmixFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.DownmixEnabled {
		fmt.Fprintf(f, "%sdownmix: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sdownmix: stereo → mono (FFmpeg builtin)\n", prefix)
}

// formatAnalysisFilter outputs analysis filter details
func formatAnalysisFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.AnalysisEnabled {
		fmt.Fprintf(f, "%sanalysis: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sanalysis: collect audio measurements (ebur128 + astats + aspectralstats)\n", prefix)
}

// formatSilenceDetectFilter outputs silence detection filter details
func formatSilenceDetectFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.SilenceDetectEnabled {
		fmt.Fprintf(f, "%ssilencedetect: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%ssilencedetect: threshold %.0f dB, min duration %.2fs\n",
		prefix, cfg.SilenceDetectLevel, cfg.SilenceDetectDuration)
}

// formatResampleFilter outputs resample filter details
func formatResampleFilter(f *os.File, cfg *processor.FilterChainConfig, prefix string) {
	if !cfg.ResampleEnabled {
		fmt.Fprintf(f, "%sresample: DISABLED\n", prefix)
		return
	}
	fmt.Fprintf(f, "%sresample: %d Hz %s mono, %d samples/frame\n",
		prefix, cfg.ResampleSampleRate, cfg.ResampleFormat, cfg.ResampleFrameSize)
}
