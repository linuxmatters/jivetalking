package report

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/linuxmatters/jivetalking/internal/processor"
)

// fullLoudnessRecord builds an in-memory record with all three loudness stages
// populated (input + filtered + final) plus dynamics and spectral input stages.
// Values are arbitrary but distinct so the tests can pin them.
func fullLoudnessRecord() *processor.RunRecord {
	return &processor.RunRecord{
		Run: processor.RunProvenance{
			InputFile:    "EP83-mark.flac",
			ProcessedAt:  "2026-06-11T17:20:55+01:00",
			DurationS:    125.5,
			SampleRateHz: 44100,
			Channels:     1,
		},
		Loudness: processor.LoudnessDomain{
			TargetILUFS: -16.0,
			Stages: processor.LoudnessStages{
				Input: &processor.InputLoudnessMetrics{
					LoudnessMetrics: processor.LoudnessMetrics{
						MomentaryLoudness: -20.5,
						ShortTermLoudness: -18.2,
						SamplePeak:        -6.23,
					},
					InputI:       -35.22,
					InputTP:      -6.21,
					InputLRA:     15.01,
					InputThresh:  -45.22,
					TargetOffset: 19.22,
				},
				Filtered: &processor.OutputLoudnessMetrics{
					OutputI:   -25.1,
					OutputTP:  -19.95,
					OutputLRA: 9.3,
				},
				Final: &processor.OutputLoudnessMetrics{
					OutputI:   -16.05,
					OutputTP:  -2.51,
					OutputLRA: 7.1,
				},
			},
		},
		Dynamics: processor.DynamicsDomain{
			Stages: processor.DynamicsStages{
				Input: &processor.DynamicsMetrics{
					RMSLevel:     -44.46,
					PeakLevel:    -6.22,
					CrestFactor:  128.54,
					DynamicRange: 90.10,
					MinLevel:     -6.22,
					MaxLevel:     -7.61,
					RMSPeak:      -16.14,
					RMSTrough:    -87.59,
					Entropy:      0.2357,
					BitDepth:     14,
				},
			},
		},
		Spectral: processor.SpectralDomain{
			Stages: processor.SpectralStages{
				Input: &processor.SpectralMetrics{
					Mean:     6.89e-06,
					Variance: 6.24e-09,
					Centroid: 7073.31,
					Spread:   5254.60,
					Skewness: 0.85,
					Kurtosis: 5.16,
					Entropy:  0.0086,
					Flatness: 0.656,
					Crest:    31.74,
					Flux:     0.00064,
					Slope:    -1.5e-05,
					Decrease: -0.0091,
					Rolloff:  13092.45,
				},
			},
		},
	}
}

// pass1OnlyRecord builds an analysis-only record: input stages only, no
// filtered/final.
func pass1OnlyRecord() *processor.RunRecord {
	rec := fullLoudnessRecord()
	rec.Loudness.Stages.Filtered = nil
	rec.Loudness.Stages.Final = nil
	rec.Dynamics.Stages.Filtered = nil
	rec.Dynamics.Stages.Final = nil
	rec.Spectral.Stages.Filtered = nil
	rec.Spectral.Stages.Final = nil
	return rec
}

func TestRenderHeader(t *testing.T) {
	got := renderHeader(fullLoudnessRecord())
	for _, want := range []string{
		"# Audio Processing Report",
		"## Run",
		"EP83-mark.flac",
		"2026-06-11T17:20:55+01:00",
		"44.1 kHz",
		"mono",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("header missing %q\n%s", want, got)
		}
	}
}

func TestRenderProcessingSummaryZeroOmitted(t *testing.T) {
	if got := renderProcessingSummary(Timings{}); got != "" {
		t.Errorf("zero Timings must render empty, got %q", got)
	}
}

func TestRenderProcessingSummaryPopulated(t *testing.T) {
	got := renderProcessingSummary(Timings{
		Pass1:          2 * time.Second,
		Pass2:          90 * time.Second,
		RealTimeFactor: 12.5,
	})
	for _, want := range []string{
		"## Processing Summary",
		"Pass 1 (analysis)",
		"Pass 2 (filter chain)",
		"Real-time factor",
		"12.5x",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q\n%s", want, got)
		}
	}
}

func TestRenderLoudnessFullStages(t *testing.T) {
	got := renderLoudness(fullLoudnessRecord())
	for _, want := range []string{
		"## Loudness",
		"| Metric | Definition | Input | Filtered | Final |",
		"Integrated loudness",
		"True peak",
		"(LUFS)",
		"(dBTP)",
		"-35.22", // input integrated
		"-25.10", // filtered integrated
		"-16.05", // final integrated
		"+19.22", // signed target offset
	} {
		if !strings.Contains(got, want) {
			t.Errorf("loudness missing %q\n%s", want, got)
		}
	}
}

func TestRenderLoudnessDefinitionPerRow(t *testing.T) {
	got := renderLoudness(fullLoudnessRecord())
	// Every loudness metric row must carry a definition gloss (criterion 4).
	for _, key := range []string{
		"integrated_lufs", "true_peak_dbtp", "lra_lu", "thresh_lufs",
		"momentary_lufs", "short_term_lufs", "sample_peak_dbfs", "target_offset_db",
	} {
		d, ok := DefinitionFor(key)
		if !ok {
			t.Fatalf("missing definition for %q", key)
		}
		// The renderer escapes cell content (mdTable), so compare against the
		// escaped gloss; a gloss with a literal pipe (e.g. |min|,|max|) renders
		// backslash-escaped.
		if !strings.Contains(got, escapeCell(d.Gloss)) {
			t.Errorf("loudness output missing gloss for %q: %q", key, d.Gloss)
		}
	}
}

func TestRenderDynamicsAndSpectralDefinitions(t *testing.T) {
	dyn := renderDynamics(fullLoudnessRecord())
	for _, key := range []string{
		"rms_level_dbfs", "peak_level_dbfs", "crest_factor_astats_db",
		"dynamic_range_db", "flat_factor", "bit_depth", "entropy",
	} {
		d, _ := DefinitionFor(key)
		if !strings.Contains(dyn, escapeCell(d.Gloss)) {
			t.Errorf("dynamics missing gloss for %q", key)
		}
	}

	spec := renderSpectral(fullLoudnessRecord())
	for _, key := range []string{
		"mean", "variance", "centroid_hz", "spread_hz", "skewness",
		"kurtosis", "flatness", "crest", "flux", "slope", "decrease", "rolloff_hz",
	} {
		d, _ := DefinitionFor(key)
		if !strings.Contains(spec, escapeCell(d.Gloss)) {
			t.Errorf("spectral missing gloss for %q", key)
		}
	}
}

func TestRenderPass1OnlyOmitsStageColumns(t *testing.T) {
	rec := pass1OnlyRecord()
	for _, got := range []string{renderLoudness(rec), renderDynamics(rec), renderSpectral(rec)} {
		if !strings.Contains(got, "| Metric | Definition | Input |") {
			t.Errorf("pass-1-only table must have Input-only header\n%s", got)
		}
		if strings.Contains(got, "Filtered") || strings.Contains(got, "Final") {
			t.Errorf("pass-1-only table must omit Filtered/Final columns\n%s", got)
		}
	}
}

func TestRenderNaNLeafPlaceholder(t *testing.T) {
	rec := pass1OnlyRecord()
	rec.Dynamics.Stages.Input.RMSTrough = math.NaN()
	got := renderDynamics(rec)
	// The RMS trough row must render the placeholder for the NaN leaf.
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "RMS trough") {
			if !strings.Contains(line, "| "+placeholder+" |") {
				t.Errorf("NaN leaf must render placeholder %q in row: %q", placeholder, line)
			}
			return
		}
	}
	t.Fatalf("RMS trough row not found\n%s", got)
}

// TestRenderNoInterpretationTokens grep-asserts the value renderers emit no
// interpretation tokens (criterion 5): no verdicts, no range-to-meaning words.
func TestRenderNoInterpretationTokens(t *testing.T) {
	rec := fullLoudnessRecord()
	out := renderHeader(rec) + renderLoudness(rec) + renderDynamics(rec) + renderSpectral(rec)
	for _, banned := range []string{"warm", "bright", "tonal", "broadband", "good", "Character", "✓", "⚠", "❌"} {
		if strings.Contains(out, banned) {
			t.Errorf("rendered output contains interpretation token %q", banned)
		}
	}
}

// regionsRecord builds an analysis-only record via the real processor assembly
// path (NewAnalysisRunRecord) so the unexported elected-profile wrappers and the
// nested regions block are populated exactly as production builds them. It carries
// an elected room-tone NoiseProfile, an elected SpeechProfile, both candidate
// arrays, the noise block, and a per-250ms interval series (>= 10 above silence so
// the RMS distribution and largest gap are present).
func regionsRecord() *processor.RunRecord {
	noise := processor.NoiseProfile{
		Start:              7 * time.Second,
		Duration:           10 * time.Second,
		MeasuredNoiseFloor: -84.58,
		PeakLevel:          -71.22,
		CrestFactor:        13.36,
		Entropy:            0.0011,
		Spectral: processor.SpectralMetrics{
			Centroid: 8707.02,
			Flatness: 0.8246,
			Kurtosis: 1.835,
		},
	}
	speech := processor.SpeechCandidateMetrics{
		Region: processor.SpeechRegion{
			Start:    1467 * time.Second,
			End:      1527 * time.Second,
			Duration: 60 * time.Second,
		},
		RegionSample: processor.RegionSample{
			RMSLevel:      -45.37,
			PeakLevel:     -15.46,
			CrestFactor:   29.91,
			MomentaryLUFS: -40.93,
			ShortTermLUFS: -36.88,
			TruePeak:      -13.15,
			SamplePeak:    -13.15,
			Spectral:      processor.SpectralMetrics{Centroid: 3348.05, Flatness: 0.255, Kurtosis: 12.80, Found: true},
		},
		VoicingDensity: 0.856,
		BodyBandRMS:    -48.05,
		SibBandRMS:     -55.87,
		BandsMeasured:  true,
		Score:          0.65,
	}

	intervals := make([]processor.IntervalSample, 0, 20)
	for i := range 20 {
		intervals = append(intervals, processor.IntervalSample{RMSLevel: -86.0 + float64(i)*3})
	}

	m := &processor.AudioMeasurements{
		Noise: processor.NoiseMetrics{
			Floor:               -84.58,
			FloorSource:         "vad_percentile",
			FloorPrescan:        -83.60,
			FloorAstats:         math.NaN(),
			RoomToneDetectLevel: -82.60,
			VoiceActivated:      false,
			ReductionHeadroom:   40.12,
		},
		Regions: processor.RegionMetrics{
			NoiseProfile:        &noise,
			SpeechProfile:       &speech,
			SpeechCandidates:    []processor.SpeechCandidateMetrics{speech, {Score: 0.4}},
			IntervalSamples:     intervals,
			VoicedLowPercentile: -34.20,
			NoiseHighPercentile: -78.50,
			GateSeparationDB:    44.30,
		},
		Duration: 2856.9,
	}
	// Wire the elected room-tone sample directly (NoiseProfile carries no
	// RegionSample); it backs regions.room_tone.samples.input.
	electedRoomTone := processor.RegionSample{RMSLevel: -84.58, PeakLevel: -71.22}
	m.Regions.ElectedRoomToneSample = &electedRoomTone

	return processor.NewAnalysisRunRecord("LMP-83-mark.flac", m)
}

func TestRenderNoiseFloor(t *testing.T) {
	got := renderNoiseFloor(regionsRecord())
	for _, want := range []string{
		"## Noise Floor",
		"| Metric | Definition | Value |",
		"Noise floor",
		"-84.58",         // floor_dbfs
		"vad_percentile", // floor_source string
		"Reduction headroom",
		"40.12",
		"no", // voice_activated bool
	} {
		if !strings.Contains(got, want) {
			t.Errorf("noise floor missing %q\n%s", want, got)
		}
	}
	// FloorAstats is NaN -> placeholder, never a fabricated number.
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "astats floor") && !strings.Contains(line, "| "+placeholder+" |") {
			t.Errorf("NaN astats floor must render placeholder: %q", line)
		}
	}
	// DROPPED rows: no Character / SNR / Noise Reduction verdict.
	for _, banned := range []string{"Character", "SNR", "Noise Reduction", "Floor-Speech"} {
		if strings.Contains(got, banned) {
			t.Errorf("noise floor contains dropped row %q\n%s", banned, got)
		}
	}
}

func TestRenderRegionsElected(t *testing.T) {
	got := renderRegions(regionsRecord())
	for _, want := range []string{
		"## Regions",
		"### Room Tone",
		"### Speech",
		"**Elected profile**",
		"Measured floor",
		"-84.58", // room-tone measured floor
		"Voicing density",
		"0.85", // speech voicing density (4dp -> 0.8560)
		"Sibilant-band RMS",
		"-55.87",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("regions missing %q\n%s", want, got)
		}
	}
	// Every elected metric row carries a definition gloss (criterion 4).
	for _, key := range []string{
		"measured_floor_dbfs", "spectral_flatness", "voicing_density", "speech_band_sib_rms_dbfs",
	} {
		d, ok := DefinitionFor(key)
		if !ok {
			t.Fatalf("missing definition for %q", key)
		}
		if !strings.Contains(got, d.Gloss) {
			t.Errorf("regions output missing gloss for %q", key)
		}
	}
}

func TestRenderGateStatistics(t *testing.T) {
	got := renderRegions(regionsRecord())
	for _, want := range []string{
		"### Gate Statistics",
		"Voiced low percentile",
		"-34.20",
		"Noise high percentile",
		"-78.50",
		"Gate separation",
		"44.30",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("gate statistics missing %q\n%s", want, got)
		}
	}
	// Every gate-statistic row carries its definition gloss.
	for _, key := range []string{
		"voiced_low_percentile_dbfs", "noise_high_percentile_dbfs", "gate_separation_db",
	} {
		d, ok := DefinitionFor(key)
		if !ok {
			t.Fatalf("missing definition for %q", key)
		}
		if !strings.Contains(got, d.Gloss) {
			t.Errorf("gate statistics output missing gloss for %q", key)
		}
	}
}

func TestRenderSpeechCandidateCountOnly(t *testing.T) {
	got := renderRegions(regionsRecord())
	if !strings.Contains(got, "**Candidates**") {
		t.Fatalf("candidates summary heading missing\n%s", got)
	}
	if !strings.Contains(got, "Evaluated count") || !strings.Contains(got, "| 2 |") {
		t.Errorf("speech candidate COUNT (2) must render\n%s", got)
	}
	if !strings.Contains(got, "0.6500") {
		t.Errorf("elected speech score must render\n%s", got)
	}
	// NO ranked candidate list: the second candidate's score (0.4000) must not appear
	// as a candidate row, and no per-candidate gloss tokens leak in.
	for _, banned := range []string{"0.4000", "Candidate 1", "Candidate 2", "Rank"} {
		if strings.Contains(got, banned) {
			t.Errorf("ranked candidate list must NOT appear, found %q\n%s", banned, got)
		}
	}
}

func TestRenderRegionSamplesStages(t *testing.T) {
	rec := regionsRecord()
	got := renderRegions(rec)
	// Analysis-only: Input present, Filtered/Final absent for the SPEECH samples.
	if !strings.Contains(got, "**Samples**") {
		t.Fatalf("samples heading missing\n%s", got)
	}
	// Speech samples.input is wired from the elected profile, so an Input-only
	// header must appear; Filtered/Final columns are omitted in analysis-only.
	if !strings.Contains(got, "| Metric | Definition | Input |") {
		t.Errorf("region samples must render Input-only header in analysis-only mode\n%s", got)
	}

	// Room-tone samples.input IS wired here (ElectedRoomToneSample set), so it
	// renders a value. Now drop it to exercise the known nil-input gap.
	rec.Regions.RoomTone.Samples.Input = nil
	got2 := renderRegions(rec)
	// With every room-tone sample stage nil, the room-tone samples table degrades to
	// placeholder cells (no stage column populated) rather than crashing.
	if !strings.Contains(got2, "**Samples**") {
		t.Errorf("room-tone samples table must still render with nil input\n%s", got2)
	}
}

func TestRenderRegionSamplesNilInputPlaceholder(t *testing.T) {
	rec := regionsRecord()
	// Force a room-tone-only record with nil input sample and no other stages.
	rec.Regions.Speech.Samples = processor.RegionSamples{}
	rec.Regions.RoomTone.Samples = processor.RegionSamples{Input: nil}
	got := renderRegionSamples(rec.Regions.RoomTone.Samples)
	// Header still present; cells are the placeholder for the absent input.
	if !strings.Contains(got, "Input") {
		t.Fatalf("samples header missing\n%s", got)
	}
	for line := range strings.SplitSeq(got, "\n") {
		if strings.Contains(line, "RMS level") && !strings.Contains(line, "| "+placeholder+" |") {
			t.Errorf("nil room-tone input must render placeholder cell: %q", line)
		}
	}
}

func TestRenderIntervalSummary(t *testing.T) {
	got := renderIntervalSummary(regionsRecord())
	for _, want := range []string{
		"## Interval Summary",
		"Interval count",
		"| 20 |", // count
		"RMS p50",
		"RMS p90",
		"Largest gap",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("interval summary missing %q\n%s", want, got)
		}
	}
	// Percentile rows carry definitions (criterion 4).
	for _, key := range []string{"rms_dist_p50_dbfs", "largest_gap_db"} {
		d, _ := DefinitionFor(key)
		if !strings.Contains(got, d.Gloss) {
			t.Errorf("interval summary missing gloss for %q", key)
		}
	}
}

func TestRenderIntervalSummaryNilOmitted(t *testing.T) {
	rec := regionsRecord()
	rec.IntervalSummary = nil
	if got := renderIntervalSummary(rec); got != "" {
		t.Errorf("nil interval summary must render empty, got %q", got)
	}
}

// TestRenderSpectrogramsProcessing: a processing record (whole+roomtone+speech,
// before/after) renders a ## Spectrograms section with image links and both
// Before and After columns, using the record's relative basenames.
func TestRenderSpectrogramsProcessing(t *testing.T) {
	rec := &processor.RunRecord{
		Spectrograms: []processor.SpectrogramImage{
			{Kind: processor.SpectrogramKindWhole, Stage: processor.SpectrogramStageBefore, Path: "ep-LUFS-16-processed.spectrogram-whole-before.png"},
			{Kind: processor.SpectrogramKindWhole, Stage: processor.SpectrogramStageAfter, Path: "ep-LUFS-16-processed.spectrogram-whole-after.png"},
			{Kind: processor.SpectrogramKindRoomTone, Stage: processor.SpectrogramStageBefore, Path: "ep-LUFS-16-processed.spectrogram-roomtone-before.png"},
			{Kind: processor.SpectrogramKindRoomTone, Stage: processor.SpectrogramStageAfter, Path: "ep-LUFS-16-processed.spectrogram-roomtone-after.png"},
			{Kind: processor.SpectrogramKindSpeech, Stage: processor.SpectrogramStageBefore, Path: "ep-LUFS-16-processed.spectrogram-speech-before.png"},
			{Kind: processor.SpectrogramKindSpeech, Stage: processor.SpectrogramStageAfter, Path: "ep-LUFS-16-processed.spectrogram-speech-after.png"},
		},
	}
	got := renderSpectrograms(rec)
	for _, want := range []string{
		"## Spectrograms",
		"| Region | Before | After |",
		"Whole file",
		"Room tone",
		"Speech",
		"![whole before](ep-LUFS-16-processed.spectrogram-whole-before.png)",
		"![whole after](ep-LUFS-16-processed.spectrogram-whole-after.png)",
		"![roomtone before](ep-LUFS-16-processed.spectrogram-roomtone-before.png)",
		"![speech after](ep-LUFS-16-processed.spectrogram-speech-after.png)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("spectrograms missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "Input") {
		t.Errorf("processing spectrograms must not render an Input column\n%s", got)
	}
	// Paths are relative basenames (no directory component) straight from the record.
	if strings.Contains(got, "/") {
		t.Errorf("spectrogram links must use relative basenames, found a path separator\n%s", got)
	}
}

// TestRenderSpectrogramsAnalysisOnly: an input-only record renders a single Input
// column with no Before/After.
func TestRenderSpectrogramsAnalysisOnly(t *testing.T) {
	rec := &processor.RunRecord{
		Spectrograms: []processor.SpectrogramImage{
			{Kind: processor.SpectrogramKindWhole, Stage: processor.SpectrogramStageInput, Path: "show-analysis.spectrogram-whole-input.png"},
			{Kind: processor.SpectrogramKindSpeech, Stage: processor.SpectrogramStageInput, Path: "show-analysis.spectrogram-speech-input.png"},
		},
	}
	got := renderSpectrograms(rec)
	for _, want := range []string{
		"## Spectrograms",
		"| Region | Input |",
		"![whole input](show-analysis.spectrogram-whole-input.png)",
		"![speech input](show-analysis.spectrogram-speech-input.png)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("analysis-only spectrograms missing %q\n%s", want, got)
		}
	}
	for _, banned := range []string{"Before", "After", "Room tone"} {
		if strings.Contains(got, banned) {
			t.Errorf("analysis-only spectrograms must not render %q\n%s", banned, got)
		}
	}
}

// TestRenderSpectrogramsEmpty: an empty slice renders "" so the orchestrator emits
// no ## Spectrograms heading.
func TestRenderSpectrogramsEmpty(t *testing.T) {
	if got := renderSpectrograms(&processor.RunRecord{}); got != "" {
		t.Errorf("empty Spectrograms must render \"\", got %q", got)
	}
	if got := RenderMarkdown(fullLoudnessRecord(), Timings{}); strings.Contains(got, "## Spectrograms") {
		t.Errorf("record with no spectrograms must not emit a Spectrograms heading\n%s", got)
	}
}

// TestRenderSpectrogramsNoFFmpegToken grep-asserts the rendered output carries no
// ffmpeg/exec reference (the renderer is a pure record consumer, criterion A11).
func TestRenderSpectrogramsNoFFmpegToken(t *testing.T) {
	rec := &processor.RunRecord{
		Spectrograms: []processor.SpectrogramImage{
			{Kind: processor.SpectrogramKindWhole, Stage: processor.SpectrogramStageInput, Path: "show.spectrogram-whole-input.png"},
		},
	}
	got := renderSpectrograms(rec)
	for _, banned := range []string{"ffmpeg", "showspectrumpic", "exec"} {
		if strings.Contains(got, banned) {
			t.Errorf("spectrogram render output contains %q", banned)
		}
	}
}

// TestRenderRegionsNoDroppedTokens grep-asserts the 1.5 sections drop the legacy
// gain-normalised columns, Character row, and verdict tokens (criterion 5).
func TestRenderRegionsNoDroppedTokens(t *testing.T) {
	rec := regionsRecord()
	out := renderNoiseFloor(rec) + renderRegions(rec) + renderIntervalSummary(rec)
	for _, banned := range []string{"†", "Character", "(tonal)", "(broadband)", "✓", "⚠", "❌", "SNR"} {
		if strings.Contains(out, banned) {
			t.Errorf("1.5 output contains dropped token %q", banned)
		}
	}
}
