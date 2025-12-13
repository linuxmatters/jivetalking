package processor

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// TestAudioOptions configures the synthetic audio to generate
type TestAudioOptions struct {
	DurationSecs float64 // Total duration in seconds
	SampleRate   int     // Sample rate (default: 44100)
	ToneFreq     float64 // Sine wave frequency in Hz (0 = no tone)
	ToneLevel    float64 // Tone level in dBFS (e.g., -23.0)
	NoiseLevel   float64 // White noise level in dBFS (0 = no noise, -60 = quiet noise)
	SilenceGap   struct {
		Start    float64 // Start time of silence gap in seconds
		Duration float64 // Duration of silence gap in seconds
	}
}

// generateTestAudio creates a synthetic WAV audio file for testing.
// The generated audio can include a sine wave tone, white noise, and silence gaps.
// Returns the path to the temporary file (caller must clean up with os.Remove).
func generateTestAudio(t *testing.T, opts TestAudioOptions) string {
	t.Helper()

	// Set defaults
	if opts.SampleRate == 0 {
		opts.SampleRate = 44100
	}
	if opts.DurationSecs == 0 {
		opts.DurationSecs = 5.0
	}

	// Calculate total samples
	totalSamples := int(opts.DurationSecs * float64(opts.SampleRate))

	// Generate samples (16-bit signed PCM)
	samples := make([]int16, totalSamples)

	// Convert dBFS to linear amplitude (0 dBFS = 1.0 = max int16)
	toneAmp := 0.0
	if opts.ToneFreq > 0 && opts.ToneLevel < 0 {
		toneAmp = math.Pow(10.0, opts.ToneLevel/20.0)
	}

	noiseAmp := 0.0
	if opts.NoiseLevel < 0 {
		noiseAmp = math.Pow(10.0, opts.NoiseLevel/20.0)
	}

	// Calculate silence gap sample range
	silenceStart := int(opts.SilenceGap.Start * float64(opts.SampleRate))
	silenceEnd := int((opts.SilenceGap.Start + opts.SilenceGap.Duration) * float64(opts.SampleRate))

	// Simple LCG random number generator for deterministic noise
	// (avoids importing math/rand and seeding complexity)
	rngState := uint32(12345)
	nextRandom := func() float64 {
		// LCG parameters from Numerical Recipes
		rngState = rngState*1664525 + 1013904223
		// Convert to -1.0 to 1.0 range
		return (float64(rngState)/float64(0xFFFFFFFF))*2.0 - 1.0
	}

	maxInt16 := float64(math.MaxInt16)

	for i := 0; i < totalSamples; i++ {
		// Check if we're in the silence gap
		if i >= silenceStart && i < silenceEnd && opts.SilenceGap.Duration > 0 {
			samples[i] = 0
			continue
		}

		var sample float64

		// Add tone
		if toneAmp > 0 {
			t := float64(i) / float64(opts.SampleRate)
			sample += toneAmp * math.Sin(2.0*math.Pi*opts.ToneFreq*t)
		}

		// Add noise
		if noiseAmp > 0 {
			sample += noiseAmp * nextRandom()
		}

		// Clamp to [-1, 1] and convert to int16
		if sample > 1.0 {
			sample = 1.0
		} else if sample < -1.0 {
			sample = -1.0
		}
		samples[i] = int16(sample * maxInt16)
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "jivetalking-test-*.wav")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpPath := tmpFile.Name()

	// Write WAV header and data
	if err := writeWAV(tmpFile, samples, opts.SampleRate); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		t.Fatalf("failed to write WAV file: %v", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		t.Fatalf("failed to close temp file: %v", err)
	}

	return tmpPath
}

// writeWAV writes a mono 16-bit WAV file
func writeWAV(f *os.File, samples []int16, sampleRate int) error {
	// WAV header constants
	const (
		numChannels   = 1
		bitsPerSample = 16
	)

	byteRate := sampleRate * numChannels * bitsPerSample / 8
	blockAlign := numChannels * bitsPerSample / 8
	dataSize := len(samples) * 2 // 2 bytes per sample (16-bit)
	fileSize := 36 + dataSize    // Total file size minus 8 bytes for RIFF header

	// RIFF header
	if _, err := f.Write([]byte("RIFF")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(fileSize)); err != nil {
		return err
	}
	if _, err := f.Write([]byte("WAVE")); err != nil {
		return err
	}

	// fmt subchunk
	if _, err := f.Write([]byte("fmt ")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(16)); err != nil { // Subchunk size
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(1)); err != nil { // Audio format (PCM)
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(numChannels)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(sampleRate)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(byteRate)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(blockAlign)); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint16(bitsPerSample)); err != nil {
		return err
	}

	// data subchunk
	if _, err := f.Write([]byte("data")); err != nil {
		return err
	}
	if err := binary.Write(f, binary.LittleEndian, uint32(dataSize)); err != nil {
		return err
	}

	// Write samples
	for _, sample := range samples {
		if err := binary.Write(f, binary.LittleEndian, sample); err != nil {
			return err
		}
	}

	return nil
}

// cleanupTestAudio removes a test audio file and its processed output (if any)
func cleanupTestAudio(t *testing.T, path string) {
	t.Helper()
	if path == "" {
		return
	}

	// Remove the input file
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Logf("warning: failed to remove test file %s: %v", path, err)
	}

	// Also try to remove any processed output file
	ext := filepath.Ext(path)
	base := path[:len(path)-len(ext)]
	processedPath := base + "-processed" + ext
	if err := os.Remove(processedPath); err != nil && !os.IsNotExist(err) {
		// Not an error - processed file may not exist
	}

	// Try FLAC processed output too (in case WAV input produced FLAC output)
	processedFlac := base + "-processed.flac"
	if processedFlac != processedPath {
		if err := os.Remove(processedFlac); err != nil && !os.IsNotExist(err) {
			// Not an error
		}
	}
}
