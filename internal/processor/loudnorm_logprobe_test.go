package processor

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivetalking/internal/audio"
)

// logProbeRecord captures one av_log callback invocation.
type logProbeRecord struct {
	phase    string
	level    int
	itemName string
	rawPtr   uintptr
	msg      string
}

// findProbeAudioFile locates any audio file under testdata/ for the probe.
// Returns "" if none exists.
func findProbeAudioFile() string {
	// Prefer the small fixture if present (fast), else any flac/wav.
	candidates := []string{"fixture-5m.flac"}
	for _, name := range candidates {
		p := filepath.Join("..", "..", "testdata", name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	matches, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "*.flac"))
	wavs, _ := filepath.Glob(filepath.Join("..", "..", "testdata", "*.wav"))
	matches = append(matches, wavs...)
	if len(matches) == 0 {
		return ""
	}
	sort.Strings(matches)
	return matches[0]
}

// TestLoudnormLogProbe answers an empirical question for the parallelism design:
// does any filter in the analysis tail emit at AV_LOG_INFO during the
// frame-processing loop, or only at graph teardown (AVFilterGraphFree)?
//
// It installs a context-aware av_log callback at AV_LOG_INFO level, runs the
// production analysis tail (astats, aspectralstats, ebur128 with metadata=1)
// over a real file, tags each log line with a phase marker ("loop" vs "free"),
// and reports INFO-line counts per phase per originating filter.
//
// Verdict rule: only-at-free => a single mutex around AVFilterGraphFree suffices
// (Option 1). Any mid-loop INFO emission => a context-filtered callback registry
// is required (Option 3).
func TestLoudnormLogProbe(t *testing.T) {
	inputPath := findProbeAudioFile()
	if inputPath == "" {
		t.Skip("no audio file found under testdata/; drop a .flac (e.g. testdata/fixture-5m.flac) to run the probe")
	}

	var (
		mu      sync.Mutex
		phase   = "setup"
		records []logProbeRecord
	)

	callback := func(ctx *ffmpeg.LogCtx, level int, msg string) {
		var (
			itemName string
			rawPtr   uintptr
		)
		if ctx != nil {
			rawPtr = uintptr(ctx.RawPtr())
			if class := ctx.Class(); class != nil {
				itemName = class.ItemName()(ctx.RawPtr()).String()
			}
		}
		mu.Lock()
		records = append(records, logProbeRecord{
			phase:    phase,
			level:    level,
			itemName: itemName,
			rawPtr:   rawPtr,
			msg:      msg,
		})
		mu.Unlock()
	}

	prevLevel, _ := ffmpeg.AVLogGetLevel()
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogInfo)
	ffmpeg.AVLogSetCallback(callback)
	defer func() {
		ffmpeg.AVLogSetCallback(nil)
		ffmpeg.AVLogSetLevel(prevLevel)
	}()

	reader, _, err := audio.OpenAudioFile(inputPath)
	if err != nil {
		t.Fatalf("failed to open audio file %q: %v", inputPath, err)
	}
	defer reader.Close()

	// Production analysis tail, verbatim, after a mono downmix. Matches
	// filters.go buildAnalysisFilter() / buildDownmixFilter() composition.
	filterSpec := "aformat=channel_layouts=mono," +
		"astats=metadata=1:measure_perchannel=all," +
		"aspectralstats=win_size=2048:win_func=hann:measure=all," +
		"ebur128=metadata=1:peak=sample+true:dualmono=true"

	filterGraph, bufferSrcCtx, bufferSinkCtx, err := setupFilterGraph(reader.GetDecoderContext(), filterSpec)
	if err != nil {
		t.Fatalf("failed to set up filter graph: %v", err)
	}

	mu.Lock()
	phase = "loop"
	mu.Unlock()

	if err := runFilterGraph(reader, bufferSrcCtx, bufferSinkCtx, FrameLoopConfig{
		OnReadError: func(err error) error { return err },
		OnPushError: func(err error) error { return err },
		OnPullError: func(err error) error { return err },
	}); err != nil {
		ffmpeg.AVFilterGraphFree(&filterGraph)
		t.Fatalf("filter graph run failed: %v", err)
	}

	mu.Lock()
	phase = "free"
	mu.Unlock()

	ffmpeg.AVFilterGraphFree(&filterGraph)

	mu.Lock()
	phase = "done"
	snapshot := make([]logProbeRecord, len(records))
	copy(snapshot, records)
	mu.Unlock()

	// Tally INFO-level lines per phase, broken down by item name.
	type key struct {
		phase    string
		itemName string
	}
	infoCounts := map[key]int{}
	loopContextPtrs := map[string]struct{}{}
	var loopNilContextLines int

	for _, r := range snapshot {
		// FFmpeg encodes level low byte; normalise like the binding does.
		lvl := r.level
		if lvl >= 0 {
			lvl &= 0xff
		}
		if lvl != ffmpeg.AVLogInfo {
			continue
		}
		name := r.itemName
		if name == "" {
			name = "<global/nil-ctx>"
		}
		infoCounts[key{r.phase, name}]++
		if r.phase == "loop" {
			if r.itemName == "" {
				loopNilContextLines++
			} else {
				loopContextPtrs[r.itemName] = struct{}{}
			}
		}
	}

	t.Logf("PROBE input file: %s", inputPath)
	t.Logf("PROBE total av_log records captured (all levels/phases): %d", len(snapshot))

	loopTotal := 0
	freeTotal := 0
	keys := make([]key, 0, len(infoCounts))
	for k := range infoCounts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].phase != keys[j].phase {
			return keys[i].phase < keys[j].phase
		}
		return keys[i].itemName < keys[j].itemName
	})
	for _, k := range keys {
		t.Logf("PROBE INFO phase=%-5s item=%-22s count=%d", k.phase, k.itemName, infoCounts[k])
		switch k.phase {
		case "loop":
			loopTotal += infoCounts[k]
		case "free":
			freeTotal += infoCounts[k]
		}
	}

	t.Logf("PROBE SUMMARY: INFO lines during loop=%d, during free=%d", loopTotal, freeTotal)
	t.Logf("PROBE SUMMARY: loop INFO lines with non-nil context: %d distinct item(s) %v; nil-context lines=%d",
		len(loopContextPtrs), keysOf(loopContextPtrs), loopNilContextLines)

	if loopTotal > 0 {
		t.Logf("PROBE VERDICT: Option 3 required (context-filtered registry) - %d INFO line(s) emitted mid-loop", loopTotal)
	} else {
		t.Logf("PROBE VERDICT: Option 1 viable (graph-free mutex) - zero INFO lines mid-loop; all %d INFO line(s) at graph-free", freeTotal)
	}
}

func keysOf(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
