// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"
	"math"
	"strconv"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// AudioMeasurements contains the measurements from Pass 1 analysis
// Uses ebur128 (LUFS/LRA), astats (dynamic range/noise floor), and aspectralstats (spectral analysis)
type AudioMeasurements struct {
	InputI       float64 `json:"input_i"`       // Integrated loudness (LUFS)
	InputTP      float64 `json:"input_tp"`      // True peak (dBTP)
	InputLRA     float64 `json:"input_lra"`     // Loudness range (LU)
	InputThresh  float64 `json:"input_thresh"`  // Threshold level
	TargetOffset float64 `json:"target_offset"` // Offset for normalization
	NoiseFloor   float64 `json:"noise_floor"`   // Measured noise floor from astats (dBFS)

	// Spectral analysis for adaptive de-esser frequency targeting
	SpectralCentroid float64 `json:"spectral_centroid"` // Average spectral centroid (Hz) - where energy is concentrated
	SpectralRolloff  float64 `json:"spectral_rolloff"`  // Average spectral rolloff (Hz) - high-frequency energy dropoff point

	// Time-domain statistics from astats for adaptive processing
	DynamicRange float64 `json:"dynamic_range"` // Measured dynamic range (dB)
	RMSLevel     float64 `json:"rms_level"`     // Overall RMS level (dBFS)
	PeakLevel    float64 `json:"peak_level"`    // Overall peak level (dBFS)
	RMSTrough    float64 `json:"rms_trough"`    // RMS level of quietest segments - best noise floor indicator (dBFS)
}

// AnalyzeAudio performs Pass 1: ebur128 + astats + aspectralstats analysis to get measurements
// This is required for adaptive processing in Pass 2.
//
// Implementation note: ebur128 and astats write measurements to frame metadata with lavfi.r128.*
// and lavfi.astats.Overall.* keys respectively. We extract these from the last processed frames.
func AnalyzeAudio(filename string, targetI, targetTP, targetLRA float64, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements)) (*AudioMeasurements, error) {
	// Open audio file
	reader, metadata, err := audio.OpenAudioFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio file: %w", err)
	}
	defer reader.Close()

	// Get total duration for progress calculation
	totalDuration := metadata.Duration
	sampleRate := float64(metadata.SampleRate)

	// Calculate total frames estimate (duration * sample_rate / samples_per_frame)
	// For FLAC, typical frame size is 4096 samples
	samplesPerFrame := 4096.0
	estimatedTotalFrames := (totalDuration * sampleRate) / samplesPerFrame

	// Create filter graph for Pass 1 analysis (astats + aspectralstats + ebur128)
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := createAnalysisFilterGraph(
		reader.GetDecoderContext(),
		targetI, targetTP, targetLRA,
		true, // first pass (analysis only)
		nil,  // no measurements yet
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create filter graph: %w", err)
	}
	// NOTE: filterGraph is explicitly freed at the end (not in defer) to ensure
	// measurements are output via av_log before we try to extract them.
	// On error paths, we still free it immediately
	var filterFreed bool
	defer func() {
		if !filterFreed && filterGraph != nil {
			ffmpeg.AVFilterGraphFree(&filterGraph)
		}
	}()

	// Process all frames through the filter
	filteredFrame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&filteredFrame)

	// Track frames for periodic progress updates
	frameCount := 0
	updateInterval := 100 // Send progress update every N frames
	currentLevel := 0.0

	// Accumulators for spectral statistics
	var spectralCentroidSum float64
	var spectralRolloffSum float64
	var spectralFrameCount int

	// Accumulators for astats measurements (will extract from last frame metadata)
	var astatsDynamicRange, astatsRMSLevel, astatsPeakLevel, astatsRMSTrough float64
	var astatsFound bool

	// Accumulators for ebur128 measurements (will extract from last frame metadata)
	// ebur128 writes cumulative measurements to frame metadata with lavfi.r128.* keys
	var ebur128InputI, ebur128InputTP, ebur128InputLRA float64
	var ebur128Found bool

	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			return nil, fmt.Errorf("failed to read frame: %w", err)
		}
		if frame == nil {
			break // EOF
		}

		// Calculate audio level from frame
		currentLevel = calculateFrameLevel(frame)

		// Send periodic progress updates based on frame count
		if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
			progress := float64(frameCount) / estimatedTotalFrames
			if progress > 1.0 {
				progress = 1.0
			}
			progressCallback(1, "Analyzing", progress, currentLevel, nil)
		}
		frameCount++

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			return nil, fmt.Errorf("failed to add frame to filter: %w", err)
		}

		// Pull filtered frames and extract spectral metadata
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				return nil, fmt.Errorf("failed to get filtered frame: %w", err)
			}

			// Extract spectral statistics from frame metadata
			metadata := filteredFrame.Metadata()
			if metadata != nil {
				// Get spectral centroid if available
				// For mono audio, spectral stats are under channel .1
				if centroidEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr("lavfi.aspectralstats.1.centroid"), nil, 0); centroidEntry != nil {
					if centroidValue, err := strconv.ParseFloat(centroidEntry.Value().String(), 64); err == nil {
						spectralCentroidSum += centroidValue
						spectralFrameCount++
					}
				}
				// Get spectral rolloff if available
				if rolloffEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr("lavfi.aspectralstats.1.rolloff"), nil, 0); rolloffEntry != nil {
					if rolloffValue, err := strconv.ParseFloat(rolloffEntry.Value().String(), 64); err == nil {
						spectralRolloffSum += rolloffValue
					}
				}

				// Extract astats measurements (cumulative, so we just get the latest)
				// For mono audio, stats are under channel .1; for stereo, use .Overall or average channels
				// Since podcast audio is typically mono, we check channel 1 first, then Overall as fallback

				dynamicRangeKey := "lavfi.astats.1.Dynamic_range"
				if dynamicRangeEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr(dynamicRangeKey), nil, 0); dynamicRangeEntry != nil {
					if value, err := strconv.ParseFloat(dynamicRangeEntry.Value().String(), 64); err == nil {
						astatsDynamicRange = value
						astatsFound = true
					}
				}
				if dynamicRangeEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr(dynamicRangeKey), nil, 0); dynamicRangeEntry != nil {
					if value, err := strconv.ParseFloat(dynamicRangeEntry.Value().String(), 64); err == nil {
						astatsDynamicRange = value
					}
				}

				rmsKey := "lavfi.astats.1.RMS_level"
				if rmsEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr(rmsKey), nil, 0); rmsEntry != nil {
					if value, err := strconv.ParseFloat(rmsEntry.Value().String(), 64); err == nil {
						astatsRMSLevel = value
					}
				}

				peakKey := "lavfi.astats.1.Peak_level"
				if peakEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr(peakKey), nil, 0); peakEntry != nil {
					if value, err := strconv.ParseFloat(peakEntry.Value().String(), 64); err == nil {
						astatsPeakLevel = value
					}
				}

				// Extract RMS_trough - RMS level of quietest segments (best noise floor indicator for speech)
				// In speech audio, quiet inter-word periods contain primarily ambient/electronic noise
				rmsTroughKey := "lavfi.astats.1.RMS_trough"
				if rmsTroughEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr(rmsTroughKey), nil, 0); rmsTroughEntry != nil {
					if value, err := strconv.ParseFloat(rmsTroughEntry.Value().String(), 64); err == nil {
						astatsRMSTrough = value
					}
				}

				// Extract ebur128 measurements (cumulative loudness analysis)
				// ebur128 provides: M.* (momentary), S.* (short-term), I (integrated), LRA, sample_peak, true_peak
				// We need the integrated loudness measurements for normalization
				if integratedEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr("lavfi.r128.I"), nil, 0); integratedEntry != nil {
					if value, err := strconv.ParseFloat(integratedEntry.Value().String(), 64); err == nil {
						ebur128InputI = value
						ebur128Found = true
					}
				}
				if truePeakEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr("lavfi.r128.true_peak"), nil, 0); truePeakEntry != nil {
					if value, err := strconv.ParseFloat(truePeakEntry.Value().String(), 64); err == nil {
						ebur128InputTP = value
					}
				}
				if lraEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr("lavfi.r128.LRA"), nil, 0); lraEntry != nil {
					if value, err := strconv.ParseFloat(lraEntry.Value().String(), 64); err == nil {
						ebur128InputLRA = value
					}
				}
				// Note: ebur128 doesn't provide threshold directly
				// We'll calculate it from the integrated loudness if needed
			}

			ffmpeg.AVFrameUnref(filteredFrame)
		}
	}

	// Flush the filter graph
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err != nil {
		return nil, fmt.Errorf("failed to flush filter: %w", err)
	}

	// Pull remaining frames
	for {
		if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return nil, fmt.Errorf("failed to get filtered frame: %w", err)
		}

		// Extract spectral statistics from remaining frames
		metadata := filteredFrame.Metadata()
		if metadata != nil {
			// Use channel 1 keys for mono audio (same as main loop)
			if centroidEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr("lavfi.aspectralstats.1.centroid"), nil, 0); centroidEntry != nil {
				if centroidValue, err := strconv.ParseFloat(centroidEntry.Value().String(), 64); err == nil {
					spectralCentroidSum += centroidValue
					spectralFrameCount++
				}
			}
			if rolloffEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr("lavfi.aspectralstats.1.rolloff"), nil, 0); rolloffEntry != nil {
				if rolloffValue, err := strconv.ParseFloat(rolloffEntry.Value().String(), 64); err == nil {
					spectralRolloffSum += rolloffValue
				}
			}

			// Extract astats measurements from remaining frames
			// Use channel 1 keys for mono audio (same as main loop)

			dynamicRangeKey := "lavfi.astats.1.Dynamic_range"
			if dynamicRangeEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr(dynamicRangeKey), nil, 0); dynamicRangeEntry != nil {
				if value, err := strconv.ParseFloat(dynamicRangeEntry.Value().String(), 64); err == nil {
					astatsDynamicRange = value
					astatsFound = true
				}
			}
			if dynamicRangeEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr(dynamicRangeKey), nil, 0); dynamicRangeEntry != nil {
				if value, err := strconv.ParseFloat(dynamicRangeEntry.Value().String(), 64); err == nil {
					astatsDynamicRange = value
				}
			}

			rmsKey := "lavfi.astats.1.RMS_level"
			if rmsEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr(rmsKey), nil, 0); rmsEntry != nil {
				if value, err := strconv.ParseFloat(rmsEntry.Value().String(), 64); err == nil {
					astatsRMSLevel = value
				}
			}

			peakKey := "lavfi.astats.1.Peak_level"
			if peakEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr(peakKey), nil, 0); peakEntry != nil {
				if value, err := strconv.ParseFloat(peakEntry.Value().String(), 64); err == nil {
					astatsPeakLevel = value
				}
			}

			rmsTroughKey := "lavfi.astats.1.RMS_trough"
			if rmsTroughEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr(rmsTroughKey), nil, 0); rmsTroughEntry != nil {
				if value, err := strconv.ParseFloat(rmsTroughEntry.Value().String(), 64); err == nil {
					astatsRMSTrough = value
				}
			}

			// Extract ebur128 measurements from remaining frames
			if integratedEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr("lavfi.r128.I"), nil, 0); integratedEntry != nil {
				if value, err := strconv.ParseFloat(integratedEntry.Value().String(), 64); err == nil {
					ebur128InputI = value
					ebur128Found = true
				}
			}
			if truePeakEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr("lavfi.r128.true_peak"), nil, 0); truePeakEntry != nil {
				if value, err := strconv.ParseFloat(truePeakEntry.Value().String(), 64); err == nil {
					ebur128InputTP = value
				}
			}
			if lraEntry := ffmpeg.AVDictGet(metadata, ffmpeg.ToCStr("lavfi.r128.LRA"), nil, 0); lraEntry != nil {
				if value, err := strconv.ParseFloat(lraEntry.Value().String(), 64); err == nil {
					ebur128InputLRA = value
				}
			}
		}

		ffmpeg.AVFrameUnref(filteredFrame)
	}

	// Free the filter graph
	ffmpeg.AVFilterGraphFree(&filterGraph)
	filterFreed = true

	// Create measurements struct and populate from metadata
	measurements := &AudioMeasurements{}

	// Populate ebur128 loudness measurements from metadata
	if ebur128Found {
		measurements.InputI = ebur128InputI
		measurements.InputTP = ebur128InputTP
		measurements.InputLRA = ebur128InputLRA
		// Calculate threshold based on integrated loudness (ebur128 doesn't provide this directly)
		// Threshold is typically around 10 LU below the integrated loudness
		measurements.InputThresh = ebur128InputI - 10.0
		// Target offset for normalization (difference between measured and target)
		measurements.TargetOffset = targetI - ebur128InputI
	} else {
		return nil, fmt.Errorf("ebur128 measurements not found in metadata")
	}

	// Calculate average spectral statistics
	if spectralFrameCount > 0 {
		measurements.SpectralCentroid = spectralCentroidSum / float64(spectralFrameCount)
		measurements.SpectralRolloff = spectralRolloffSum / float64(spectralFrameCount)
	}

	// Store astats measurements (if captured)
	if astatsFound {
		measurements.DynamicRange = astatsDynamicRange
		measurements.RMSLevel = astatsRMSLevel
		measurements.PeakLevel = astatsPeakLevel
		measurements.RMSTrough = astatsRMSTrough
	}

	// Derive noise floor using three-tier approach based on audio engineering best practices:
	// Tier 1 (Primary): RMS_trough from astats - most accurate
	//   - Measures RMS level during quietest segments (inter-word silence in speech)
	//   - These quiet periods contain primarily room noise, HVAC, electronics noise
	//   - Directly represents the actual noise floor of the recording environment
	// Tier 2 (Secondary): Estimate from RMS_level - 15dB
	//   - Based on typical speech crest factor where quiet segments are 12-18dB below average RMS
	//   - Reasonable approximation when RMS_trough unavailable
	// Tier 3 (Tertiary): Estimate from ebur128 InputThresh with loudness-based offset
	//   - Fallback for when astats data is completely unavailable
	//   - Uses integrated loudness to infer likely noise floor characteristics

	if astatsRMSTrough != 0 && !math.IsInf(astatsRMSTrough, -1) {
		// Tier 1: Use RMS_trough (best - actual measurement of quiet segments)
		measurements.NoiseFloor = astatsRMSTrough
	} else if astatsRMSLevel != 0 && !math.IsInf(astatsRMSLevel, -1) {
		// Tier 2: Estimate from overall RMS level
		// Typical speech has quiet segments 12-18dB below average RMS; use 15dB as balanced estimate
		measurements.NoiseFloor = astatsRMSLevel - 15.0
	} else {
		// Tier 3: Estimate from ebur128 integrated loudness threshold
		// Louder recordings typically have better SNR (lower relative noise floor)
		var noiseFloorOffset float64
		if measurements.InputI > -20 {
			noiseFloorOffset = 18.0 // Professional: very low noise floor
		} else if measurements.InputI > -30 {
			noiseFloorOffset = 12.0 // Typical podcast: moderate noise floor
		} else {
			noiseFloorOffset = 8.0 // Quiet source: higher relative noise
		}
		measurements.NoiseFloor = measurements.InputThresh - noiseFloorOffset
	}

	// Safety clamp: -90dB (digital silence) to -30dB (very noisy environment)
	// Prevents extreme values while allowing wide range of recording quality
	if measurements.NoiseFloor < -90.0 {
		measurements.NoiseFloor = -90.0
	} else if measurements.NoiseFloor > -30.0 {
		measurements.NoiseFloor = -30.0
	}

	return measurements, nil
}

// createAnalysisFilterGraph creates an AVFilterGraph for Pass 1 analysis
// Uses astats, aspectralstats, and ebur128 filters to extract measurements
func createAnalysisFilterGraph(
	decCtx *ffmpeg.AVCodecContext,
	targetI, targetTP, targetLRA float64,
	firstPass bool,
	measurements *AudioMeasurements,
) (*ffmpeg.AVFilterGraph, *ffmpeg.AVFilterContext, *ffmpeg.AVFilterContext, error) {

	filterGraph := ffmpeg.AVFilterGraphAlloc()
	if filterGraph == nil {
		return nil, nil, nil, fmt.Errorf("failed to allocate filter graph")
	}

	// Get abuffer and abuffersink filters
	bufferSrc := ffmpeg.AVFilterGetByName(ffmpeg.GlobalCStr("abuffer"))
	bufferSink := ffmpeg.AVFilterGetByName(ffmpeg.GlobalCStr("abuffersink"))
	if bufferSrc == nil || bufferSink == nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("abuffer or abuffersink filter not found")
	}

	// Get channel layout description
	layoutPtr := ffmpeg.AllocCStr(64)
	defer layoutPtr.Free()

	if _, err := ffmpeg.AVChannelLayoutDescribe(decCtx.ChLayout(), layoutPtr, 64); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to get channel layout: %w", err)
	}

	// Create abuffer source
	pktTimebase := decCtx.PktTimebase()
	args := fmt.Sprintf(
		"time_base=%d/%d:sample_rate=%d:sample_fmt=%s:channel_layout=%s",
		pktTimebase.Num(), pktTimebase.Den(),
		decCtx.SampleRate(),
		ffmpeg.AVGetSampleFmtName(decCtx.SampleFmt()).String(),
		layoutPtr.String(),
	)

	argsC := ffmpeg.ToCStr(args)
	defer argsC.Free()

	var bufferSrcCtx *ffmpeg.AVFilterContext
	if _, err := ffmpeg.AVFilterGraphCreateFilter(
		&bufferSrcCtx,
		bufferSrc,
		ffmpeg.GlobalCStr("in"),
		argsC,
		nil,
		filterGraph,
	); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to create abuffer: %w", err)
	}

	// Create abuffersink
	var bufferSinkCtx *ffmpeg.AVFilterContext
	if _, err := ffmpeg.AVFilterGraphCreateFilter(
		&bufferSinkCtx,
		bufferSink,
		ffmpeg.GlobalCStr("out"),
		nil,
		nil,
		filterGraph,
	); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to create abuffersink: %w", err)
	}

	// Build filter string
	var filterSpec string
	if firstPass {
		// First pass: Analysis only - extract loudness measurements, spectral statistics, and time-domain stats
		// astats provides noise floor and dynamic range measurements for adaptive gate and compression
		// aspectralstats measures spectral centroid and rolloff for adaptive de-esser targeting
		// ebur128 provides integrated loudness (LUFS), true peak, and LRA via metadata
		// Note: reset=0 (default) allows astats to accumulate statistics across all frames for Overall measurements
		// ebur128 metadata=1 writes per-frame loudness data to frame metadata (lavfi.r128.* keys)
		filterSpec = fmt.Sprintf("astats=metadata=1:measure_overall=Noise_floor+Dynamic_range+RMS_level+Peak_level,aspectralstats=win_size=2048:win_func=hann:measure=centroid+rolloff,ebur128=metadata=1:target=%.0f",
			targetI)
	} else {
		// Second pass: Not used anymore
		// Using dynaudnorm for normalization in Pass 2 processing (not analysis)
		filterSpec = ""
	}

	// Parse filter graph
	outputs := ffmpeg.AVFilterInoutAlloc()
	inputs := ffmpeg.AVFilterInoutAlloc()
	defer ffmpeg.AVFilterInoutFree(&outputs)
	defer ffmpeg.AVFilterInoutFree(&inputs)

	outputs.SetName(ffmpeg.ToCStr("in"))
	outputs.SetFilterCtx(bufferSrcCtx)
	outputs.SetPadIdx(0)
	outputs.SetNext(nil)

	inputs.SetName(ffmpeg.ToCStr("out"))
	inputs.SetFilterCtx(bufferSinkCtx)
	inputs.SetPadIdx(0)
	inputs.SetNext(nil)

	filterSpecC := ffmpeg.ToCStr(filterSpec)
	defer filterSpecC.Free()

	if _, err := ffmpeg.AVFilterGraphParsePtr(filterGraph, filterSpecC, &inputs, &outputs, nil); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to parse filter graph: %w", err)
	}

	// Configure filter graph
	if _, err := ffmpeg.AVFilterGraphConfig(filterGraph, nil); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		return nil, nil, nil, fmt.Errorf("failed to configure filter graph: %w", err)
	}

	return filterGraph, bufferSrcCtx, bufferSinkCtx, nil
}
