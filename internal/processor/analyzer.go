// Package processor handles audio analysis and processing
package processor

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/csnewman/ffmpeg-go"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// LoudnormMeasurements contains the measurements from loudnorm first-pass analysis
type LoudnormMeasurements struct {
	InputI       float64 `json:"input_i"`       // Integrated loudness (LUFS)
	InputTP      float64 `json:"input_tp"`      // True peak (dBTP)
	InputLRA     float64 `json:"input_lra"`     // Loudness range (LU)
	InputThresh  float64 `json:"input_thresh"`  // Threshold level
	TargetOffset float64 `json:"target_offset"` // Offset for normalization
	NoiseFloor   float64 `json:"noise_floor"`   // Estimated noise floor (dB)
}

// loudnormJSON is a helper struct for unmarshaling FFmpeg's JSON output
// FFmpeg outputs numeric values as strings, e.g., "input_i": "-31.10"
type loudnormJSON struct {
	InputI       string `json:"input_i"`
	InputTP      string `json:"input_tp"`
	InputLRA     string `json:"input_lra"`
	InputThresh  string `json:"input_thresh"`
	TargetOffset string `json:"target_offset"`
}

// AnalyzeAudio performs Pass 1: loudnorm analysis to get input measurements
// This is required for accurate two-pass loudness normalization.
//
// Implementation note: The loudnorm filter outputs its measurements via av_log()
// only when the filter is destroyed (in its uninit() function). Therefore, we must
// explicitly free the filter graph BEFORE attempting to extract measurements.
func AnalyzeAudio(filename string, targetI, targetTP, targetLRA float64, progressCallback func(pass int, passName string, progress float64, measurements *LoudnormMeasurements)) (*LoudnormMeasurements, error) {
	// Set up log capture to extract loudnorm JSON output
	capture := &logCapture{}

	// Save current log level and set to INFO to capture loudnorm output
	oldLevel, _ := ffmpeg.AVLogGetLevel()
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogInfo)
	ffmpeg.AVLogSetCallback(capture.callback)
	defer func() {
		ffmpeg.AVLogSetCallback(nil)
		ffmpeg.AVLogSetLevel(oldLevel)
	}()

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

	// Create filter graph for loudnorm analysis
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := createLoudnormFilterGraph(
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

	for {
		frame, err := reader.ReadFrame()
		if err != nil {
			return nil, fmt.Errorf("failed to read frame: %w", err)
		}
		if frame == nil {
			break // EOF
		}

		// Send periodic progress updates based on frame count
		if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
			progress := float64(frameCount) / estimatedTotalFrames
			if progress > 1.0 {
				progress = 1.0
			}
			progressCallback(1, "Analyzing", progress, nil)
		}
		frameCount++

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			return nil, fmt.Errorf("failed to add frame to filter: %w", err)
		}

		// Pull filtered frames (we don't need them, just processing for measurements)
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				return nil, fmt.Errorf("failed to get filtered frame: %w", err)
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
		ffmpeg.AVFrameUnref(filteredFrame)
	}

	// CRITICAL: Free the filter graph to trigger uninit() which outputs the JSON measurements via av_log
	// The loudnorm filter only outputs its measurements when being destroyed
	ffmpeg.AVFilterGraphFree(&filterGraph)
	filterFreed = true

	// Extract measurements from captured logs (now available after uninit)
	measurements, err := capture.extractMeasurements()
	if err != nil {
		return nil, fmt.Errorf("failed to extract measurements: %w", err)
	}

	// Estimate noise floor based on loudnorm input threshold
	// The input_thresh represents the threshold below which audio is considered silence
	// For podcast audio, the noise floor is typically 10-15 dB below this threshold
	// This provides a good starting estimate for afftdn's automatic tracking (tn=1)
	// which will adapt to the actual noise floor during Pass 2 processing
	measurements.NoiseFloor = measurements.InputThresh - 15.0

	return measurements, nil
}

// createLoudnormFilterGraph creates an AVFilterGraph for loudnorm processing
func createLoudnormFilterGraph(
	decCtx *ffmpeg.AVCodecContext,
	targetI, targetTP, targetLRA float64,
	firstPass bool,
	measurements *LoudnormMeasurements,
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
		// First pass: Analysis only - extract loudnorm measurements
		// Note: Noise floor will be handled in Pass 2 by afftdn's automatic tracking (tn=1)
		filterSpec = fmt.Sprintf("loudnorm=I=%.1f:TP=%.1f:LRA=%.1f:print_format=json",
			targetI, targetTP, targetLRA)
	} else {
		// Second pass: use measurements from first pass
		filterSpec = fmt.Sprintf(
			"loudnorm=I=%.1f:TP=%.1f:LRA=%.1f:"+
				"measured_I=%.2f:measured_TP=%.2f:measured_LRA=%.2f:"+
				"measured_thresh=%.2f:offset=%.2f:"+
				"linear=true:print_format=summary",
			targetI, targetTP, targetLRA,
			measurements.InputI, measurements.InputTP, measurements.InputLRA,
			measurements.InputThresh, measurements.TargetOffset,
		)
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

// logCapture captures FFmpeg logs to extract loudnorm JSON measurements
type logCapture struct {
	mu           sync.Mutex
	allLogs      strings.Builder
	measurements *LoudnormMeasurements
}

func (lc *logCapture) callback(ctx *ffmpeg.LogCtx, level int, msg string) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	// Accumulate all log output
	lc.allLogs.WriteString(msg)
}

func (lc *logCapture) extractMeasurements() (*LoudnormMeasurements, error) {
	lc.mu.Lock()
	defer lc.mu.Unlock()

	// Look for JSON block in accumulated logs
	// loudnorm outputs JSON with this pattern:
	// [Parsed_loudnorm_0 @ 0x...] {
	//   "input_i" : "-31.10",
	//   ...
	// }

	logs := lc.allLogs.String()

	// Debug: Check if we got any logs at all
	if len(logs) == 0 {
		return nil, fmt.Errorf("no logs captured - log callback may not be working")
	}

	// Find JSON block - look for { followed by "input_i"
	startIdx := strings.Index(logs, "{")
	if startIdx == -1 {
		return nil, fmt.Errorf("no JSON block found in logs (captured %d bytes)", len(logs))
	}

	// Find matching closing brace
	endIdx := strings.Index(logs[startIdx:], "}")
	if endIdx == -1 {
		return nil, fmt.Errorf("incomplete JSON block in logs")
	}

	jsonStr := logs[startIdx : startIdx+endIdx+1]

	// Parse JSON (FFmpeg outputs numeric values as strings)
	var raw loudnormJSON
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w (json: %s)", err, jsonStr)
	}

	// Convert string values to float64
	m := &LoudnormMeasurements{}
	var err error

	if m.InputI, err = strconv.ParseFloat(raw.InputI, 64); err != nil {
		return nil, fmt.Errorf("failed to parse input_i: %w", err)
	}
	if m.InputTP, err = strconv.ParseFloat(raw.InputTP, 64); err != nil {
		return nil, fmt.Errorf("failed to parse input_tp: %w", err)
	}
	if m.InputLRA, err = strconv.ParseFloat(raw.InputLRA, 64); err != nil {
		return nil, fmt.Errorf("failed to parse input_lra: %w", err)
	}
	if m.InputThresh, err = strconv.ParseFloat(raw.InputThresh, 64); err != nil {
		return nil, fmt.Errorf("failed to parse input_thresh: %w", err)
	}
	if m.TargetOffset, err = strconv.ParseFloat(raw.TargetOffset, 64); err != nil {
		return nil, fmt.Errorf("failed to parse target_offset: %w", err)
	}

	return m, nil
}
