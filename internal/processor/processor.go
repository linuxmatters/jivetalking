// Package processor handles audio analysis and processing
package processor

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/csnewman/ffmpeg-go"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// ProcessAudio performs complete two-pass audio processing:
// - Pass 1: Analyze audio to get measurements and noise floor estimate
// - Pass 2: Process audio through complete filter chain (afftdn → agate → acompressor → dynaudnorm → alimiter)
//
// The output file will be named <basename>-processed.<ext> in the same directory as the input
// If progressCallback is not nil, it will be called with progress updates
func ProcessAudio(inputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements)) (*ProcessingResult, error) {
	// Pass 1: Analysis
	// (printf output suppressed for UI compatibility)

	if progressCallback != nil {
		progressCallback(1, "Analyzing", 0.0, 0.0, nil)
	}

	measurements, err := AnalyzeAudio(inputPath, config.TargetI, config.TargetTP, config.TargetLRA, progressCallback)
	if err != nil {
		return nil, fmt.Errorf("Pass 1 failed: %w", err)
	}

	if progressCallback != nil {
		progressCallback(1, "Analyzing", 1.0, 0.0, measurements)
	}

	// Update config with measurements and noise floor for Pass 2
	config.Measurements = measurements
	config.NoiseFloor = measurements.NoiseFloor

	// Adaptively set highpass frequency based on spectral centroid AND noise reduction needs
	// Lower spectral centroid (darker/warmer voice) = use lower cutoff to preserve warmth
	// Higher spectral centroid (brighter voice) = use higher cutoff to remove more rumble
	// Heavy noise reduction needed = use higher cutoff to remove low-frequency room noise

	// First, calculate LUFS gap to determine noise reduction needs
	var lufsGap float64
	if measurements.InputI != 0.0 {
		lufsGap = config.TargetI - measurements.InputI
	}

	if measurements.SpectralCentroid > 0 {
		var baseFreq float64
		if measurements.SpectralCentroid > 6000 {
			// Bright voice with high-frequency energy concentration
			// Safe to use higher cutoff - voice energy is well above 100Hz
			baseFreq = 100.0
		} else if measurements.SpectralCentroid > 4000 {
			// Normal voice with balanced frequency distribution
			// Use standard cutoff for podcast speech
			baseFreq = 80.0
		} else {
			// Dark/warm voice with low-frequency energy concentration
			// Use lower cutoff to preserve voice warmth and body
			baseFreq = 60.0
		}

		// If heavy noise reduction is needed (>25dB gain), increase highpass frequency
		// to aggressively remove low-frequency room noise (HVAC, rumble)
		if lufsGap > 25.0 {
			// Very quiet source needing aggressive processing
			// Boost highpass by 40Hz to remove more room noise
			config.HighpassFreq = baseFreq + 40.0
		} else if lufsGap > 15.0 {
			// Moderately quiet source
			// Boost highpass by 20Hz
			config.HighpassFreq = baseFreq + 20.0
		} else {
			// Normal source
			config.HighpassFreq = baseFreq
		}

		// Cap at 120Hz maximum to avoid affecting voice fundamentals
		if config.HighpassFreq > 120.0 {
			config.HighpassFreq = 120.0
		}
	}
	// If no spectral analysis available (SpectralCentroid == 0), keep default 80Hz

	// Adaptively set noise reduction based on upcoming gain/expansion
	// Key insight: If we're going to apply 30dB of gain later (via speechnorm/dynaudnorm),
	// we need to remove 30dB of noise NOW, or it will be amplified along with the speech.
	//
	// Strategy:
	// 1. Calculate LUFS gap (how much gain will be needed)
	// 2. Set noise reduction to: base_reduction + LUFS_gap
	// 3. This keeps final noise floor below -60dB after expansion
	if measurements.InputI != 0.0 {
		// Calculate how much gain will be applied in normalization
		lufsGap := config.TargetI - measurements.InputI

		// Base noise reduction (for recordings already near target)
		baseReduction := 12.0 // dB - standard for clean recordings

		// Add the LUFS gap to noise reduction
		// If we need 30dB of gain, remove an extra 30dB of noise
		adaptiveReduction := baseReduction + lufsGap

		// Clamp to reasonable limits:
		// - Min 6dB: always do some noise reduction
		// - Max 40dB: afftdn becomes unstable beyond this
		if adaptiveReduction < 6.0 {
			adaptiveReduction = 6.0
		} else if adaptiveReduction > 40.0 {
			adaptiveReduction = 40.0
		}

		config.NoiseReduction = adaptiveReduction
	} else {
		// Fallback if no LUFS measurement
		config.NoiseReduction = 12.0
	}

	// Adaptively set de-esser intensity based on spectral analysis
	// Uses both spectral centroid (energy concentration) and rolloff (high-frequency extension)
	// to intelligently detect likelihood of harsh sibilance
	if measurements.SpectralCentroid > 0 && measurements.SpectralRolloff > 0 {
		// Start with centroid-based baseline
		var baseIntensity float64
		if measurements.SpectralCentroid > 7000 {
			baseIntensity = 0.6 // Bright voice baseline
		} else if measurements.SpectralCentroid > 6000 {
			baseIntensity = 0.5 // Normal voice baseline
		} else {
			baseIntensity = 0.4 // Dark voice baseline
		}

		// Refine based on spectral rolloff (high-frequency extension)
		// Rolloff indicates where HF content actually extends to
		if measurements.SpectralRolloff < 6000 {
			// Very limited high-frequency content - unlikely to have sibilance
			// Skip deesser entirely for dark/warm voices with no HF extension
			config.DeessIntensity = 0.0
		} else if measurements.SpectralRolloff < 8000 {
			// Limited HF extension - reduce intensity
			// Even bright voices may not need much deessing if HF content drops off early
			config.DeessIntensity = baseIntensity * 0.7 // Reduce by 30%
			if config.DeessIntensity < 0.3 {
				config.DeessIntensity = 0.0 // Skip if too low
			}
		} else if measurements.SpectralRolloff > 12000 {
			// Extensive high-frequency content - likely to have sibilance
			// Increase intensity even for moderate centroid values
			config.DeessIntensity = baseIntensity * 1.2 // Increase by 20%
			if config.DeessIntensity > 0.8 {
				config.DeessIntensity = 0.8 // Cap at 0.8 for safety
			}
		} else {
			// Normal HF extension (8-12 kHz) - use baseline
			config.DeessIntensity = baseIntensity
		}
	} else if measurements.SpectralCentroid > 0 {
		// Fallback: only centroid available (no rolloff measurement)
		if measurements.SpectralCentroid > 7000 {
			config.DeessIntensity = 0.6
		} else if measurements.SpectralCentroid > 6000 {
			config.DeessIntensity = 0.5
		} else {
			config.DeessIntensity = 0.4
		}
	}
	// If no spectral analysis available, keep default 0.0 (disabled)

	// Adaptively set gate threshold based on measured noise floor from astats RMS_trough
	// RMS_trough measures the RMS level during quietest segments (inter-word silence)
	// providing accurate noise floor for the recording environment
	//
	// Gate threshold = noise floor + offset (dB above noise to avoid cutting into speech)
	// Offset strategy based on recording quality:
	// - Clean recordings (< -60dB): 10dB offset for safety margin
	// - Moderate recordings (-60 to -50dB): 8dB offset for balance
	// - Noisy recordings (> -50dB): 6dB offset to preserve more speech
	var gateOffsetDB float64
	if measurements.NoiseFloor < -60.0 {
		// Very clean recording - use larger margin to avoid false triggers
		gateOffsetDB = 10.0
	} else if measurements.NoiseFloor < -50.0 {
		// Typical podcast recording - standard margin
		gateOffsetDB = 8.0
	} else {
		// Noisy recording - use smaller margin to preserve more speech
		gateOffsetDB = 6.0
	}

	// Calculate gate threshold: noise floor + offset (in dB), then convert to linear
	gateThresholdDB := measurements.NoiseFloor + gateOffsetDB
	config.GateThreshold = math.Pow(10, gateThresholdDB/20.0)

	// Safety limits: prevent extremes while allowing wide range of recording quality
	// Min -70dB: handles very clean studio recordings (RMS_trough can be -80 to -90dB)
	// Max -25dB: prevents over-aggressive gating in noisy environments
	const minThresholdDB = -70.0
	const maxThresholdDB = -25.0
	minThresholdLinear := math.Pow(10, minThresholdDB/20.0)
	maxThresholdLinear := math.Pow(10, maxThresholdDB/20.0)

	if config.GateThreshold < minThresholdLinear {
		config.GateThreshold = minThresholdLinear // -70dBFS minimum (professional studio)
	} else if config.GateThreshold > maxThresholdLinear {
		config.GateThreshold = maxThresholdLinear // -25dBFS maximum (noisy environment)
	}

	// Adaptively set compression based on measured dynamic range from astats
	// Dynamic range indicates how much variation exists between loud and quiet parts
	// This informs how aggressively we should compress to even out the levels
	if measurements.DynamicRange > 0 {
		if measurements.DynamicRange > 30.0 {
			// Very dynamic content (expressive delivery)
			config.CompRatio = 2.0       // Gentle ratio
			config.CompThreshold = -16.0 // Higher threshold
			config.CompMakeup = 1.0      // Minimal makeup
		} else if measurements.DynamicRange > 20.0 {
			// Moderately dynamic (typical podcast)
			config.CompRatio = 3.0
			config.CompThreshold = -18.0
			config.CompMakeup = 2.0
		} else {
			// Already compressed/consistent
			config.CompRatio = 4.0       // Stronger ratio for peaks
			config.CompThreshold = -20.0 // Lower threshold
			config.CompMakeup = 3.0      // More makeup
		}
	}

	// Adaptive attack/release based on loudness range
	if measurements.InputLRA > 15.0 {
		// Wide loudness range = preserve transients
		config.CompAttack = 25   // Slower attack
		config.CompRelease = 150 // Slower release
	} else if measurements.InputLRA > 10.0 {
		// Moderate range
		config.CompAttack = 20
		config.CompRelease = 100
	} else {
		// Narrow range = tighter control
		config.CompAttack = 15
		config.CompRelease = 80
	}

	// Adaptive parallel compression mix based on recording quality AND dynamic range
	var mixFactor float64

	// Noise floor indicates recording quality (affects artifact audibility)
	if measurements.NoiseFloor < -50 {
		mixFactor = 0.95 // Clean recording baseline - can use more compression
	} else if measurements.NoiseFloor < -40 {
		mixFactor = 0.85 // Moderate quality
	} else {
		mixFactor = 0.75 // Noisy - gentler processing to mask pumping artifacts
	}

	// Dynamic range indicates content characteristics (affects how much compression needed)
	if measurements.DynamicRange > 30 {
		// Very dynamic - preserve more dry signal
		config.CompMix = mixFactor - 0.10
	} else if measurements.DynamicRange > 20 {
		// Moderate dynamics
		config.CompMix = mixFactor
	} else {
		// Already compressed - can use more
		config.CompMix = math.Min(1.0, mixFactor+0.10)
	}
	// If no dynamic range measurement available, keep defaults (ratio: 2.5, threshold: -20dB)

	// Phase 1: Conservative dynaudnorm configuration
	// Remove aggressive adaptive tuning that caused distortion/clipping
	// Use fixed conservative values with gain staging safety check

	// Fixed conservative dynaudnorm parameters
	config.DynaudnormFrameLen = 500      // 500ms frames (default, balanced)
	config.DynaudnormFilterSize = 31     // Gaussian filter size 31 (default, smooth)
	config.DynaudnormPeakValue = 0.95    // Peak target 0.95 (default, 5% headroom)
	config.DynaudnormMaxGain = 5.0       // Max gain 5x (conservative, prevents over-amplification)
	config.DynaudnormTargetRMS = 0.0     // No RMS targeting (peak-based normalization only)
	config.DynaudnormCompress = 0.0      // No compression (acompressor handles this)
	config.DynaudnormThreshold = 0.0     // Normalize all frames
	config.DynaudnormChannels = false    // Coupled channels
	config.DynaudnormDCCorrect = false   // No DC correction
	config.DynaudnormAltBoundary = false // Standard boundary mode

	// Adaptive speechnorm configuration based on input LUFS
	// speechnorm uses RMS targeting for more consistent LUFS-based normalization
	// Calculate expansion needed to reach target LUFS, disable compression (acompressor already handled it)
	if measurements.InputI != 0.0 {
		// Calculate LUFS gap from input to target (-16 LUFS)
		lufsGap := config.TargetI - measurements.InputI

		// Convert dB gap to linear expansion factor: expansion = 10^(gap/20)
		// This gives us the multiplicative factor needed to close the gap
		expansion := math.Pow(10, lufsGap/20.0)

		// CAP EXPANSION at 10x (20dB) to preserve audio quality
		// For very quiet sources (>20dB gap), accept higher output LUFS rather than
		// applying extreme expansion that degrades audio quality
		// Example: -46 LUFS input would need 31.9x expansion to reach -16 LUFS,
		// but we cap at 10x, resulting in ~-26 LUFS output with better quality
		const maxExpansion = 10.0 // 20dB maximum gain

		// Clamp to reasonable range (1.0-10.0)
		if expansion < 1.0 {
			expansion = 1.0
		} else if expansion > maxExpansion {
			expansion = maxExpansion
		}
		config.SpeechnormExpansion = expansion

		// Enable arnndn (RNN denoise) for heavily uplifted audio
		// When speechnorm applies significant gain (≥8x / 18dB), it amplifies any noise
		// that afftdn didn't catch. arnndn provides neural network-based "mop up" of
		// this amplified noise AFTER expansion but BEFORE final normalization.
		// Threshold: 8.0x expansion (18dB gain) - targets truly problematic cases
		if expansion >= 8.0 {
			config.ArnnDnEnabled = true
			config.ArnnDnMix = 0.8 // Full noise removal
		} else {
			config.ArnnDnEnabled = false
		}

		// Enable anlmdn (Non-Local Means denoise) for heavily uplifted audio
		// Patch-based denoising as alternative to RNN approach - better at preserving
		// texture and detail while removing noise. Positioned after arnndn for
		// additional cleanup if needed.
		// Strength scales with expansion: more gain = more aggressive denoising
		if expansion >= 8.0 {
			config.AnlmDnEnabled = true
			// Adaptive strength based on expansion level
			// 8x expansion → 0.0001, 10x expansion → 0.001
			// Formula: strength = 0.00001 * expansion^2
			config.AnlmDnStrength = 0.00001 * expansion * expansion
			// Clamp to reasonable range
			if config.AnlmDnStrength > 0.01 {
				config.AnlmDnStrength = 0.01 // Max strength for quality preservation
			}
		} else {
			config.AnlmDnEnabled = false
		}

		// RMS targeting for LUFS consistency
		// Target RMS calculated from desired output LUFS
		// Rough conversion: LUFS ≈ -23 + 20*log10(RMS) for speech
		// -16 LUFS ≈ RMS of 0.14
		targetRMS := math.Pow(10, (config.TargetI+23)/20.0)
		if targetRMS < 0.0 {
			targetRMS = 0.0
		} else if targetRMS > 1.0 {
			targetRMS = 1.0
		}
		config.SpeechnormRMS = targetRMS

		// Disable compression - set threshold to 0.0 so all audio gets expanded, never compressed
		// After acompressor, we only want expansion toward target, not compression
		config.SpeechnormThreshold = 0.0
		config.SpeechnormCompression = 1.0 // 1.0 = no compression effect

		// Peak value should leave headroom for limiter
		config.SpeechnormPeak = 0.95

		// Fast response for speech (default values work well)
		config.SpeechnormRaise = 0.001
		config.SpeechnormFall = 0.001
	}

	// Safety checks: ensure no NaN or Inf values
	if math.IsNaN(config.HighpassFreq) || math.IsInf(config.HighpassFreq, 0) {
		config.HighpassFreq = 80.0
	}
	if math.IsNaN(config.DeessIntensity) || math.IsInf(config.DeessIntensity, 0) {
		config.DeessIntensity = 0.0
	}
	if math.IsNaN(config.GateThreshold) || math.IsInf(config.GateThreshold, 0) || config.GateThreshold <= 0 {
		config.GateThreshold = 0.01 // -40dBFS default
	}
	if math.IsNaN(config.CompRatio) || math.IsInf(config.CompRatio, 0) {
		config.CompRatio = 2.5
	}
	if math.IsNaN(config.CompThreshold) || math.IsInf(config.CompThreshold, 0) {
		config.CompThreshold = -20.0
	}
	if math.IsNaN(config.CompMakeup) || math.IsInf(config.CompMakeup, 0) {
		config.CompMakeup = 3.0
	}

	// Pass 2: Processing
	// (printf output suppressed for UI compatibility)

	if progressCallback != nil {
		progressCallback(2, "Processing", 0.0, 0.0, measurements)
	}

	// Generate output filename: input.flac → input-processed.flac
	outputPath := generateOutputPath(inputPath)

	if err := processWithFilters(inputPath, outputPath, config, progressCallback, measurements); err != nil {
		return nil, fmt.Errorf("Pass 2 failed: %w", err)
	}

	if progressCallback != nil {
		progressCallback(2, "Processing", 1.0, 0.0, measurements)
	}

	// Return the processing result
	// Note: OutputLUFS is no longer guaranteed to match TargetI
	// Dynaudnorm provides adaptive normalization but doesn't target specific LUFS values
	result := &ProcessingResult{
		OutputPath:   outputPath,
		InputLUFS:    measurements.InputI,
		OutputLUFS:   0.0, // Not measured - would require third pass analysis
		NoiseFloor:   measurements.NoiseFloor,
		Measurements: measurements,
		Config:       config, // Include config for logging adaptive parameters
	}

	return result, nil
}

// ProcessingResult contains the results of audio processing
type ProcessingResult struct {
	OutputPath   string
	InputLUFS    float64
	OutputLUFS   float64
	NoiseFloor   float64
	Measurements *AudioMeasurements
	Config       *FilterChainConfig // Contains adaptive parameters used
}

// processWithFilters performs the actual audio processing with the complete filter chain
func processWithFilters(inputPath, outputPath string, config *FilterChainConfig, progressCallback func(pass int, passName string, progress float64, level float64, measurements *AudioMeasurements), measurements *AudioMeasurements) error {
	// Open input audio file
	reader, metadata, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		return fmt.Errorf("failed to open input file: %w", err)
	}
	defer reader.Close()

	// Get total duration for progress calculation
	totalDuration := metadata.Duration
	sampleRate := float64(metadata.SampleRate)

	// Calculate total frames estimate (duration * sample_rate / samples_per_frame)
	// For FLAC, typical frame size is 4096 samples
	samplesPerFrame := 4096.0
	estimatedTotalFrames := (totalDuration * sampleRate) / samplesPerFrame

	// Create filter graph with complete processing chain
	filterGraph, bufferSrcCtx, bufferSinkCtx, err := CreateProcessingFilterGraph(
		reader.GetDecoderContext(),
		config,
	)
	if err != nil {
		return fmt.Errorf("failed to create filter graph: %w", err)
	}
	defer ffmpeg.AVFilterGraphFree(&filterGraph)

	// Create output encoder
	encoder, err := createOutputEncoder(outputPath, metadata, bufferSinkCtx)
	if err != nil {
		return fmt.Errorf("failed to create encoder: %w", err)
	}
	defer encoder.Close()

	// Allocate frames for processing
	filteredFrame := ffmpeg.AVFrameAlloc()
	defer ffmpeg.AVFrameFree(&filteredFrame)

	// Track frame count for periodic progress updates
	frameCount := 0
	currentLevel := 0.0

	// Process all frames through the filter chain
	for {
		// Read frame from input
		frame, err := reader.ReadFrame()
		if err != nil {
			return fmt.Errorf("failed to read frame: %w", err)
		}
		if frame == nil {
			break // EOF
		}

		frameCount++

		// Push frame into filter graph
		if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, frame, 0); err != nil {
			return fmt.Errorf("failed to add frame to filter: %w", err)
		}

		// Pull filtered frames and encode them
		for {
			if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				return fmt.Errorf("failed to get filtered frame: %w", err)
			}

			// Calculate audio level from FILTERED frame (shows processed audio in VU meter)
			currentLevel = calculateFrameLevel(filteredFrame)

			// Set timebase for the filtered frame
			filteredFrame.SetTimeBase(ffmpeg.AVBuffersinkGetTimeBase(bufferSinkCtx))

			// Encode and write the filtered frame
			if err := encoder.WriteFrame(filteredFrame); err != nil {
				return fmt.Errorf("failed to write frame: %w", err)
			}

			ffmpeg.AVFrameUnref(filteredFrame)
		}

		// Send periodic progress updates based on INPUT frame count (moved outside inner loop)
		// This ensures consistent update frequency between Pass 1 and Pass 2
		updateInterval := 100 // Send progress update every N frames
		if frameCount%updateInterval == 0 && progressCallback != nil && estimatedTotalFrames > 0 {
			progress := float64(frameCount) / estimatedTotalFrames
			if progress > 1.0 {
				progress = 1.0
			}
			progressCallback(2, "Processing", progress, currentLevel, measurements)
		}
	}

	// Flush the filter graph
	if _, err := ffmpeg.AVBuffersrcAddFrameFlags(bufferSrcCtx, nil, 0); err != nil {
		return fmt.Errorf("failed to flush filter: %w", err)
	}

	// Pull remaining filtered frames
	for {
		if _, err := ffmpeg.AVBuffersinkGetFrame(bufferSinkCtx, filteredFrame); err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return fmt.Errorf("failed to get filtered frame: %w", err)
		}

		filteredFrame.SetTimeBase(ffmpeg.AVBuffersinkGetTimeBase(bufferSinkCtx))

		if err := encoder.WriteFrame(filteredFrame); err != nil {
			return fmt.Errorf("failed to write frame: %w", err)
		}

		ffmpeg.AVFrameUnref(filteredFrame)
	}

	// Flush the encoder
	if err := encoder.Flush(); err != nil {
		return fmt.Errorf("failed to flush encoder: %w", err)
	}

	return nil
}

// generateOutputPath creates the output filename from the input filename
// Example: /path/to/audio.flac → /path/to/audio-processed.flac
func generateOutputPath(inputPath string) string {
	dir := filepath.Dir(inputPath)
	filename := filepath.Base(inputPath)
	ext := filepath.Ext(filename)
	nameWithoutExt := strings.TrimSuffix(filename, ext)

	return filepath.Join(dir, nameWithoutExt+"-processed"+ext)
}

// Encoder wraps the audio encoding and muxing functionality
type Encoder struct {
	fmtCtx    *ffmpeg.AVFormatContext
	encCtx    *ffmpeg.AVCodecContext
	stream    *ffmpeg.AVStream
	packet    *ffmpeg.AVPacket
	streamIdx int
}

// createOutputEncoder creates an encoder for FLAC output
// TODO: Add WAV fallback if FLAC encoder is not available
func createOutputEncoder(outputPath string, metadata *audio.Metadata, bufferSinkCtx *ffmpeg.AVFilterContext) (*Encoder, error) {
	// Allocate output format context
	outputPathC := ffmpeg.ToCStr(outputPath)
	defer outputPathC.Free()

	var fmtCtx *ffmpeg.AVFormatContext
	if _, err := ffmpeg.AVFormatAllocOutputContext2(&fmtCtx, nil, nil, outputPathC); err != nil {
		return nil, fmt.Errorf("failed to allocate output context: %w", err)
	}

	// Find FLAC encoder
	codec := ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdFlac)
	if codec == nil {
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("FLAC encoder not found")
	}

	// Create stream
	stream := ffmpeg.AVFormatNewStream(fmtCtx, nil)
	if stream == nil {
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to create stream")
	}

	// Allocate encoder context
	encCtx := ffmpeg.AVCodecAllocContext3(codec)
	if encCtx == nil {
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to allocate encoder context")
	}

	// Get audio parameters from filter output (we only need sample rate, format is set to S16 via aformat filter)
	if _, err := ffmpeg.AVBuffersinkGetFormat(bufferSinkCtx); err != nil { // Verify filter output is configured
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to get sample format: %w", err)
	}

	sampleRate, err := ffmpeg.AVBuffersinkGetSampleRate(bufferSinkCtx)
	if err != nil {
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to get sample rate: %w", err)
	}

	timeBase := ffmpeg.AVBuffersinkGetTimeBase(bufferSinkCtx)

	// Configure encoder - FLAC supports S16 and S32, we use S16 which matches our aformat filter
	encCtx.SetSampleFmt(ffmpeg.AVSampleFmtS16)
	encCtx.SetSampleRate(sampleRate)

	// Get channel count from filter output and set default channel layout
	channels, err := ffmpeg.AVBuffersinkGetChannels(bufferSinkCtx)
	if err != nil {
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to get channels: %w", err)
	}

	// Set default channel layout for the encoder
	ffmpeg.AVChannelLayoutDefault(encCtx.ChLayout(), channels)

	// Set compression level for FLAC
	if codec.Id() == ffmpeg.AVCodecIdFlac {
		ffmpeg.AVOptSetInt(encCtx.RawPtr(), ffmpeg.GlobalCStr("compression_level"), 5, 0)
		// FLAC encoder requires fixed frame size - must match asetnsamples filter (4096)
		encCtx.SetFrameSize(4096)
	}

	// Set global header flag if needed by the format
	if fmtCtx.Oformat().Flags()&ffmpeg.AVFmtGlobalheader != 0 {
		encCtx.SetFlags(encCtx.Flags() | ffmpeg.AVCodecFlagGlobalHeader)
	}

	encCtx.SetTimeBase(timeBase)

	// Open encoder
	if _, err := ffmpeg.AVCodecOpen2(encCtx, codec, nil); err != nil {
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to open encoder: %w", err)
	}

	// Copy encoder parameters to stream
	if _, err := ffmpeg.AVCodecParametersFromContext(stream.Codecpar(), encCtx); err != nil {
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to copy encoder parameters: %w", err)
	}

	stream.SetTimeBase(encCtx.TimeBase())

	// Open output file
	if fmtCtx.Oformat().Flags()&ffmpeg.AVFmtNofile == 0 {
		var pb *ffmpeg.AVIOContext
		if _, err := ffmpeg.AVIOOpen(&pb, outputPathC, ffmpeg.AVIOFlagWrite); err != nil {
			ffmpeg.AVCodecFreeContext(&encCtx)
			ffmpeg.AVFormatFreeContext(fmtCtx)
			return nil, fmt.Errorf("failed to open output file: %w", err)
		}
		fmtCtx.SetPb(pb)
	}

	// Write header
	if _, err := ffmpeg.AVFormatWriteHeader(fmtCtx, nil); err != nil {
		if fmtCtx.Pb() != nil {
			ffmpeg.AVIOClose(fmtCtx.Pb())
		}
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to write header: %w", err)
	}

	packet := ffmpeg.AVPacketAlloc()
	if packet == nil {
		if fmtCtx.Pb() != nil {
			ffmpeg.AVIOClose(fmtCtx.Pb())
		}
		ffmpeg.AVCodecFreeContext(&encCtx)
		ffmpeg.AVFormatFreeContext(fmtCtx)
		return nil, fmt.Errorf("failed to allocate packet")
	}

	return &Encoder{
		fmtCtx:    fmtCtx,
		encCtx:    encCtx,
		stream:    stream,
		packet:    packet,
		streamIdx: 0,
	}, nil
}

// WriteFrame encodes and writes a single audio frame
func (e *Encoder) WriteFrame(frame *ffmpeg.AVFrame) error {
	// Rescale PTS to encoder timebase if needed
	if frame.Pts() != ffmpeg.AVNoptsValue {
		frame.SetPts(
			ffmpeg.AVRescaleQ(frame.Pts(), frame.TimeBase(), e.encCtx.TimeBase()),
		)
	}

	// Send frame to encoder
	if _, err := ffmpeg.AVCodecSendFrame(e.encCtx, frame); err != nil {
		return fmt.Errorf("failed to send frame to encoder: %w", err)
	}

	// Receive encoded packets
	return e.receivePackets()
}

// Flush flushes the encoder
func (e *Encoder) Flush() error {
	// Send NULL frame to signal flush
	if _, err := ffmpeg.AVCodecSendFrame(e.encCtx, nil); err != nil {
		return fmt.Errorf("failed to flush encoder: %w", err)
	}

	return e.receivePackets()
}

// receivePackets receives and writes packets from the encoder
func (e *Encoder) receivePackets() error {
	for {
		ffmpeg.AVPacketUnref(e.packet)

		if _, err := ffmpeg.AVCodecReceivePacket(e.encCtx, e.packet); err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return fmt.Errorf("failed to receive packet: %w", err)
		}

		// Set stream index
		e.packet.SetStreamIndex(e.streamIdx)

		// Rescale timestamps
		ffmpeg.AVPacketRescaleTs(e.packet, e.encCtx.TimeBase(), e.stream.TimeBase())

		// Write packet
		if _, err := ffmpeg.AVInterleavedWriteFrame(e.fmtCtx, e.packet); err != nil {
			return fmt.Errorf("failed to write packet: %w", err)
		}
	}

	return nil
}

// calculateFrameLevel calculates the RMS (Root Mean Square) level of an audio frame in dB
// This provides accurate audio level measurement for VU meter display
func calculateFrameLevel(frame *ffmpeg.AVFrame) float64 {
	if frame == nil || frame.NbSamples() == 0 {
		return -60.0 // Silence threshold
	}

	// Get sample format to know how to interpret the data
	sampleFmt := frame.Format()
	nbSamples := frame.NbSamples()
	nbChannels := frame.ChLayout().NbChannels()

	// Get pointer to audio data (first plane for packed formats, or first channel for planar)
	dataPtr := frame.Data().Get(0)
	if dataPtr == nil {
		return -60.0
	}

	// Calculate RMS based on sample format
	// Most common formats: S16 (signed 16-bit) and FLT (32-bit float)
	var sumSquares float64
	var sampleCount int64

	switch ffmpeg.AVSampleFormat(sampleFmt) {
	case ffmpeg.AVSampleFmtS16, ffmpeg.AVSampleFmtS16P:
		// 16-bit signed integer samples
		samples := unsafe.Slice((*int16)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample) / 32768.0 // Normalize to -1.0 to 1.0
			sumSquares += normalized * normalized
			sampleCount++
		}

	case ffmpeg.AVSampleFmtFlt, ffmpeg.AVSampleFmtFltp:
		// 32-bit float samples (already normalized to -1.0 to 1.0)
		samples := unsafe.Slice((*float32)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample)
			sumSquares += normalized * normalized
			sampleCount++
		}

	case ffmpeg.AVSampleFmtS32, ffmpeg.AVSampleFmtS32P:
		// 32-bit signed integer samples
		samples := unsafe.Slice((*int32)(dataPtr), int(nbSamples)*int(nbChannels))
		for _, sample := range samples {
			normalized := float64(sample) / 2147483648.0 // Normalize to -1.0 to 1.0
			sumSquares += normalized * normalized
			sampleCount++
		}

	default:
		// Unsupported format, return neutral value
		return -30.0
	}

	if sampleCount == 0 {
		return -60.0
	}

	// Calculate RMS (Root Mean Square)
	rms := math.Sqrt(sumSquares / float64(sampleCount))

	// Convert to dB: 20 * log10(rms)
	// Add small epsilon to avoid log(0)
	if rms < 0.00001 { // Equivalent to -100 dB
		return -60.0 // Floor at -60 dB for silence
	}

	levelDB := 20.0 * math.Log10(rms)

	// Clamp to reasonable range for display (-60 dB to 0 dB)
	if levelDB < -60.0 {
		levelDB = -60.0
	} else if levelDB > 0.0 {
		levelDB = 0.0
	}

	return levelDB
}

// Close closes the encoder and output file
func (e *Encoder) Close() error {
	// Write trailer
	if _, err := ffmpeg.AVWriteTrailer(e.fmtCtx); err != nil {
		return fmt.Errorf("failed to write trailer: %w", err)
	}

	// Free resources
	ffmpeg.AVPacketFree(&e.packet)
	ffmpeg.AVCodecFreeContext(&e.encCtx)

	// Close output file
	if e.fmtCtx.Oformat().Flags()&ffmpeg.AVFmtNofile == 0 {
		if e.fmtCtx.Pb() != nil {
			if _, err := ffmpeg.AVIOClose(e.fmtCtx.Pb()); err != nil {
				return fmt.Errorf("failed to close output file: %w", err)
			}
			e.fmtCtx.SetPb(nil)
		}
	}

	ffmpeg.AVFormatFreeContext(e.fmtCtx)

	return nil
}
