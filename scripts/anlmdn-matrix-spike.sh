#!/usr/bin/env bash
# anlmdn-matrix-spike.sh — focused 6-variant anlmdn parameter matrix
#
# Maps the speed/quality frontier of FFmpeg's anlmdn filter across two
# voice fixtures with different noise profiles. All variants run at the
# fixtures' native sample rate (no 32 kHz cap variant — dropped after the
# previous spike informed Melody's analysis). The chain is anlmdn-only:
# no compand, gate, compressor, de-esser or analysis filters.
#
# Picks are grounded in libavfilter/af_anlmdn.c — see matrix.md for the
# source-line citations. The matrix anchors v031_baseline (the 0.3.1
# reference), re-tests the m_strict free-quality lever on broadband noise,
# probes r_half + LUT pairings to ask whether tighter or looser smoothing
# composes with a smaller search window, and pushes r to the FFmpeg AVOption
# floor (r=2 ms, line 71 of af_anlmdn.c) to bound the speedup envelope.
#
# Fixtures:
#   - testdata/LMP-68-mark.flac   constant audible RFI hum (multi-band)
#   - testdata/LMP-69-martin.flac audible room noise (broadband)
#
# Per-run artefacts: processed FLAC, full-band spectrogram, 0–2000 Hz zoom
# spectrogram (the radio-interference band on LMP-68; useful low-band view
# on LMP-69), wall-clock timing, metrics JSON, and the literal filter
# string used.
#
# A reference phase before the variant runs renders spectrograms + metrics
# for any pre-existing .bench/anlmdn-fix-validation/after/*processed.flac
# (LMP-68 only; LMP-69 has no fix-validation reference) so Melody can
# compare matrix output against the existing fix-validation corpus
# without re-running any pipeline.
#
# Prerequisites:
#   - system ffmpeg + ffprobe on PATH (no embedded ffmpeg; pure shell)
#   - testdata/LMP-68-mark.flac and testdata/LMP-69-martin.flac
#
# Usage:
#   bash scripts/anlmdn-matrix-spike.sh           # 5-minute window, with confirmation
#   bash scripts/anlmdn-matrix-spike.sh -y        # 5-minute window, skip confirmation
#   bash scripts/anlmdn-matrix-spike.sh -n        # dry run: print matrix + plan
#   bash scripts/anlmdn-matrix-spike.sh 60        # process only first 60 s per fixture
#   bash scripts/anlmdn-matrix-spike.sh -y 1790   # full file, skip confirmation

set -euo pipefail

# ============================================================================
# Configuration
# ============================================================================

# Fixtures — both must exist or the script aborts at startup.
declare -a FIXTURES=(
	"testdata/LMP-68-mark.flac"
	"testdata/LMP-69-martin.flac"
)

OUTPUT_ROOT=".bench/anlmdn-matrix-spike"
FIX_VALIDATION_ROOT=".bench/anlmdn-fix-validation"

# Matrix entries — one per row.
# Format: name|s|p_sec|r_sec|m|question|expected
#
# Six picks focused on the speed/quality frontier identified by the
# previous spike. v031_baseline is the required reference; m_strict
# re-tests the free-quality lever on broadband noise; r_half is the
# established speed candidate; r_half_m_strict and r_half_m_relax test
# whether tighter or looser LUT compensates for a smaller search; r_min
# pushes r to the FFmpeg AVOption floor (af_anlmdn.c line 71).
declare -a MATRIX=(
	"v031_baseline|0.00001|0.0060|0.0058|11|Quality/speed reference; does it hold on the room-noise fixture too?|The 0.3.1 reference (filters.go:314-317). Required anchor."
	"m_strict|0.00001|0.0060|0.0058|3|Free quality lever — re-verify on the room-noise fixture.|Established win on RFI; need to confirm no broadband damage."
	"r_half|0.00001|0.0060|0.0029|11|Speed candidate baseline; confirm 1.96× holds on the second fixture.|Established speed win with measurable residue on RFI."
	"r_half_m_strict|0.00001|0.0060|0.0029|3|Does tighter LUT compensate for smaller search? Hypothesis: wins on RFI/mains hum, neutral or worse on room noise.|Tonal noise needs sharp rejection of distant patches."
	"r_half_m_relax|0.00001|0.0060|0.0029|30|Does looser LUT (more averaging) compensate? Hypothesis: wins on room noise, worse on RFI.|Broadband noise benefits from more patches contributing."
	"r_min|0.00001|0.0060|0.0020|11|Speed floor; r=2 ms is FFmpeg's hard minimum (af_anlmdn.c AVOption table line 71).|Establishes the upper bound of speedup; tells us if r_half is the practical limit."
)

# Colours for output
RED=$'\033[0;31m'
GREEN=$'\033[0;32m'
YELLOW=$'\033[0;33m'
CYAN=$'\033[0;36m'
BOLD=$'\033[1m'
NC=$'\033[0m'

# ============================================================================
# Helper functions
# ============================================================================

log_info() { echo -e "${YELLOW}▶${NC} $1"; }
log_success() { echo -e "${GREEN}✓${NC} $1"; }
log_warn() { echo -e "${YELLOW}!${NC} $1"; }
log_error() { echo -e "${RED}✗${NC} $1"; }

log_header() {
	echo -e "${CYAN}═══════════════════════════════════════════════════════════════${NC}"
	echo -e "${CYAN}  $1${NC}"
	echo -e "${CYAN}═══════════════════════════════════════════════════════════════${NC}"
}

require_cmd() {
	local cmd="$1"
	if ! command -v "$cmd" >/dev/null 2>&1; then
		log_error "Required command not found on PATH: $cmd"
		exit 1
	fi
}

# Detect the fixture's sample rate via ffprobe.
detect_sample_rate() {
	local file="$1"
	local sr
	sr=$(ffprobe -v error -select_streams a:0 \
		-show_entries stream=sample_rate \
		-of csv=p=0 "$file" 2>/dev/null | tr -d '\r\n')
	if [[ -z "$sr" || "$sr" == "0" ]]; then
		log_error "Could not detect sample rate of $file"
		exit 1
	fi
	echo "$sr"
}

# Detect the fixture's audio duration in seconds (float) via ffprobe.
detect_duration() {
	local file="$1"
	local dur
	dur=$(ffprobe -v error -select_streams a:0 \
		-show_entries stream=duration \
		-of csv=p=0 "$file" 2>/dev/null | tr -d '\r\n')
	if [[ -z "$dur" || "$dur" == "N/A" ]]; then
		dur=$(ffprobe -v error -show_entries format=duration \
			-of csv=p=0 "$file" 2>/dev/null | tr -d '\r\n')
	fi
	if [[ -z "$dur" ]]; then
		echo "0"
	else
		echo "$dur"
	fi
}

# Build the anlmdn-only filter chain. All runs are at source rate now —
# no cap argument, no aformat sample-rate switching. Mono + fltp are still
# pinned so anlmdn sees a known sample format.
#   $1: s, $2: p (sec), $3: r (sec), $4: m
build_filter() {
	local s="$1" p="$2" r="$3" m="$4"
	printf 'aformat=channel_layouts=mono:sample_fmts=fltp,anlmdn=s=%s:p=%s:r=%s:m=%s' \
		"$s" "$p" "$r" "$m"
}

# Render a spectrogram PNG via showspectrumpic.
#   $1: input audio, $2: output png, $3: optional stop frequency (Hz, 0 = none)
render_spectrogram() {
	local input="$1" output="$2" stop_hz="${3:-0}"
	local spec="showspectrumpic=s=1920x1080:legend=1"
	if [[ "$stop_hz" != "0" ]]; then
		spec="${spec}:stop=${stop_hz}"
	fi
	ffmpeg -y -hide_banner -loglevel error \
		-i "$input" \
		-lavfi "$spec" \
		"$output"
}

# Capture metrics JSON for an audio file using ebur128 + astats + aspectralstats.
#   $1: input audio, $2: output JSON path
capture_metrics() {
	local input="$1" output="$2"
	local raw
	raw=$(ffmpeg -hide_banner -nostats -i "$input" \
		-af "aformat=channel_layouts=mono,ebur128=peak=true,astats=metadata=1,aspectralstats=measure=centroid+rolloff+flatness,ametadata=print:file=-" \
		-f null - 2>&1) || true

	local lufs_i lufs_tp
	lufs_i=$(printf '%s\n' "$raw" | awk '/Integrated loudness:/{found=1; next} found && /I:/{print $2; exit}')
	lufs_tp=$(printf '%s\n' "$raw" | awk '/True peak:/{found=1; next} found && /Peak:/{print $2; exit}')
	[[ -z "$lufs_i" ]] && lufs_i="0"
	[[ -z "$lufs_tp" ]] && lufs_tp="0"

	local rms_db peak_db
	rms_db=$(printf '%s\n' "$raw" | grep -oP 'astats\.Overall\.RMS_level=\K[-0-9.]+' | tail -1)
	peak_db=$(printf '%s\n' "$raw" | grep -oP 'astats\.Overall\.Peak_level=\K[-0-9.]+' | tail -1)
	[[ -z "$rms_db" ]] && rms_db="0"
	[[ -z "$peak_db" ]] && peak_db="0"

	local centroid rolloff flatness
	centroid=$(printf '%s\n' "$raw" | grep -oP 'aspectralstats\.1\.centroid=\K[0-9.]+' |
		awk '{sum+=$1; n++} END {if(n>0) printf "%.2f", sum/n; else print 0}')
	rolloff=$(printf '%s\n' "$raw" | grep -oP 'aspectralstats\.1\.rolloff=\K[0-9.]+' |
		awk '{sum+=$1; n++} END {if(n>0) printf "%.2f", sum/n; else print 0}')
	flatness=$(printf '%s\n' "$raw" | grep -oP 'aspectralstats\.1\.flatness=\K[0-9.]+' |
		awk '{sum+=$1; n++} END {if(n>0) printf "%.6f", sum/n; else print 0}')
	[[ -z "$centroid" ]] && centroid="0"
	[[ -z "$rolloff" ]] && rolloff="0"
	[[ -z "$flatness" ]] && flatness="0"

	local samples duration sr
	sr=$(ffprobe -v error -select_streams a:0 -show_entries stream=sample_rate -of csv=p=0 "$input" | tr -d '\r\n')
	duration=$(ffprobe -v error -select_streams a:0 -show_entries stream=duration -of csv=p=0 "$input" | tr -d '\r\n')
	[[ -z "$duration" || "$duration" == "N/A" ]] &&
		duration=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$input" | tr -d '\r\n')
	[[ -z "$duration" ]] && duration="0"
	[[ -z "$sr" ]] && sr="0"
	samples=$(awk -v d="$duration" -v s="$sr" 'BEGIN {printf "%d", d * s}')

	cat >"$output" <<JSON
{
  "input": "${input}",
  "sample_rate_hz": ${sr},
  "duration_seconds": ${duration},
  "total_samples": ${samples},
  "lufs_i": ${lufs_i},
  "lufs_tp": ${lufs_tp},
  "rms_db": ${rms_db},
  "peak_db": ${peak_db},
  "spectral_centroid_hz": ${centroid},
  "spectral_rolloff_hz": ${rolloff},
  "spectral_flatness": ${flatness}
}
JSON
}

# Read a single numeric field from a metrics JSON file. Crude parser
# (avoid the jq dependency); matches "field": <number>.
metrics_field() {
	local file="$1" field="$2"
	grep -oE "\"${field}\"[[:space:]]*:[[:space:]]*-?[0-9.]+" "$file" 2>/dev/null |
		head -1 |
		sed -E 's/.*:[[:space:]]*//'
}

# Extract the fixture stem from its path:
#   testdata/LMP-68-mark.flac -> LMP-68-mark
fixture_stem() {
	local path="$1"
	local base="${path##*/}"
	echo "${base%.*}"
}

# Write the matrix design to matrix.md so Melody can read it standalone.
write_matrix_doc() {
	local file="$1"
	local fixtures_inline="$2"
	{
		cat <<'MD'
# anlmdn matrix spike

Focused 6-variant matrix mapping the speed/quality frontier of FFmpeg's
`anlmdn` (Non-Local Means) audio denoiser across two voice fixtures with
different noise profiles. All variants run at the fixtures' native sample
rate — the 32 kHz cap variant from the previous spike has been dropped.
The chain is anlmdn-only: no compand, gate, compressor, de-esser or
analysis filters.

Picks are grounded in a reading of `libavfilter/af_anlmdn.c` — see the
"Source-code rationale" section below.

MD
		echo "Fixtures:"
		echo
		printf '%s' "$fixtures_inline"
		echo
		cat <<'MD'

## Variants

| Name | s | p (sec) | r (sec) | m | Question it answers | Rationale |
|------|---|---------|---------|---|---------------------|-----------|
MD
		local row name s p r m question expected
		for row in "${MATRIX[@]}"; do
			IFS='|' read -r name s p r m question expected <<<"$row"
			printf "| %s | %s | %s | %s | %s | %s | %s |\n" \
				"$name" "$s" "$p" "$r" "$m" "$question" "$expected"
		done
		cat <<'MD'

## 0.3.1 reference

Source: `internal/processor/filters.go` at tag `0.3.1`, lines 314–317:

- `NoiseRemoveStrength: 0.00001`
- `NoiseRemovePatchSec: 0.006`
- `NoiseRemoveResearchSec: 0.0058`
- `NoiseRemoveSmooth: 11.0`

These constants pre-date the `noiseRemoveLegacy*` / `noiseRemoveProduction*`
rename and the conditional 32 kHz pre-anlmdn cap that became the production
default in commits `2b16185` / `7060f4c` ("adopt 32k pre-anlmdn production
default").

## Source-code rationale

Lines below cite FFmpeg `master` (`libavfilter/af_anlmdn.c`).

**Strength `s` (alias `a`).** Pinned to the 1e-05 floor (the AVOption
default at line 67) for every variant; cost O(1).

**Patch `p` (stored as `pd`).** AVOption at line 69:
`{.i64=2000}, 1000, 100000` — default 2 ms, hard floor 1 ms. Held at
6 ms for every variant in this matrix; the previous spike already
explored this axis so the focus here is on `r` and `m`.

**Research `r` (stored as `rd`).** AVOption at line 71:
`{.i64=6000}, 2000, 300000` — default 6 ms, hard floor 2 ms.
Converted to half-research samples in `config_filter`:
`newS = av_rescale(s->rd, outlink->sample_rate, AV_TIME_BASE);` (line
131). `S` drives the inner loop count `2*S` in `filter_channel`
(~line 240) and so dominates wall time at fixed `p`. Three operating
points in the matrix: 5.8 ms (baseline), 2.9 ms (`r_half`), and 2.0 ms
(`r_min`, the AVOption floor).

**Smooth `m`.** AVOption at line 78: `{.dbl=11.}, 1, 1000`.
Materialised in `config_filter` as the weight-LUT scale
(`s->pdiff_lut_scale = 1.f / s->m * WEIGHT_LUT_SIZE`, ~line 165) and
in the hot path as the early-exit threshold `if (w >= smooth) continue;`
(~line 245). `m` does **not** change loop bounds or buffer footprint —
runtime is essentially constant in `m`. Lower `m` sharpens the
exponential decay (less averaging, more selective denoising); higher
`m` flattens it (more averaging, broader denoising). Three operating
points in the matrix: 11 (baseline), 3 (`m_strict`), and 30
(`r_half_m_relax`).

### Cost model

Per output sample anlmdn does O(S × K) float work; total work scales
as `N_in × p × r × sample_rate²`. With `p` fixed at 6 ms across all six
variants, expected runtime ranks as:

- `v031_baseline`, `m_strict` (r=5.8 ms): slowest, equal
- `r_half`, `r_half_m_strict`, `r_half_m_relax` (r=2.9 ms): ~1.96× faster
- `r_min` (r=2.0 ms): ~2.9× faster than baseline

`m_strict` and the two `r_half_m_*` variants exist to test whether
runtime really is invariant in `m` (the source says it should be) and
whether shifting `m` away from 11 changes how denoising interacts with
tonal vs broadband noise.

### Pick rationale

- **`v031_baseline`** — the production reference. Required anchor.
- **`m_strict`** — re-tests the previously identified "free quality
  lever" on the new room-noise fixture; if it damages broadband
  denoising we want to know before tightening `m` in production.
- **`r_half`** — the established speed candidate from the previous
  spike. Re-runs on both fixtures to confirm the 1.96× speedup.
- **`r_half_m_strict`** — combination probe. Hypothesis: tighter LUT
  compensates the smaller search on tonal noise (RFI/mains hum) by
  rejecting distant patches more aggressively; neutral or worse on
  broadband room noise.
- **`r_half_m_relax`** — opposite combination probe. Hypothesis:
  looser LUT compensates the smaller search on broadband noise (more
  patches contribute, averaging works) but is worse on tonal noise.
- **`r_min`** — pushes `r` to the AVOption floor (2 ms, line 71).
  Establishes the upper-bound speedup of the research axis and tells
  us whether `r_half` is the practical operating limit or whether
  there's still useful ground beyond it.
MD
	} >"$file"
}

# ============================================================================
# Argument parsing
# ============================================================================

DRY_RUN=0
ASSUME_YES=0
# Default to a 5-minute window per fixture. Both fixtures carry continuous
# noise (RFI on LMP-68, room noise on LMP-69) so 5 minutes is plenty of
# signal. Pass an explicit duration argument to override.
DURATION="300"

while (($# > 0)); do
	case "$1" in
	-n | --dry-run) DRY_RUN=1 ;;
	-y | --yes) ASSUME_YES=1 ;;
	-h | --help)
		sed -n '2,40p' "$0"
		exit 0
		;;
	--)
		shift
		DURATION="${1:-}"
		break
		;;
	-*)
		log_error "Unknown flag: $1"
		exit 2
		;;
	*)
		DURATION="$1"
		;;
	esac
	shift
done

# ============================================================================
# Per-fixture worker
# ============================================================================

# Process a single fixture through the matrix and append rows to summary.tsv.
# Failures of individual variants are logged but do not abort the run.
process_fixture() {
	local fixture_path="$1"
	local summary_tsv="$2"
	local stem
	stem=$(fixture_stem "$fixture_path")

	local src_rate file_duration
	src_rate=$(detect_sample_rate "$fixture_path")
	file_duration=$(detect_duration "$fixture_path")

	local duration_arg=()
	local duration_label="full file"
	if [[ -n "$DURATION" ]]; then
		duration_arg=(-t "$DURATION")
		duration_label="${DURATION} s"
	fi

	log_header "Fixture: ${stem}"
	echo "  path:           ${fixture_path}"
	echo "  sample rate:    ${src_rate} Hz"
	echo "  file duration:  ${file_duration} s"
	echo "  effective:      ${duration_label}"
	echo

	local fixture_root="${OUTPUT_ROOT}/fixtures/${stem}"
	mkdir -p "${fixture_root}/input" "${fixture_root}/variants"

	# --- Input reference: trimmed FLAC + spectrograms + metrics --------
	log_info "Rendering input reference for ${stem}..."
	local input_for_ref="$fixture_path"
	if [[ -n "$DURATION" ]]; then
		input_for_ref="${fixture_root}/input/reference-trimmed.flac"
		ffmpeg -y -hide_banner -loglevel error \
			-i "$fixture_path" -t "$DURATION" \
			-c:a flac -compression_level 8 \
			"$input_for_ref" || {
			log_error "Failed to trim ${fixture_path}; using full file for reference"
			input_for_ref="$fixture_path"
		}
	fi
	render_spectrogram "$input_for_ref" "${fixture_root}/input/spectrogram-full.png"
	render_spectrogram "$input_for_ref" "${fixture_root}/input/spectrogram-low.png" 2000
	capture_metrics "$input_for_ref" "${fixture_root}/input/metrics.json"
	log_success "input reference: ${fixture_root}/input/"

	# --- Fix-validation reference (LMP-68-mark only) --------------------
	if [[ "$stem" == "LMP-68-mark" ]]; then
		local ref_src_dir="${FIX_VALIDATION_ROOT}/after"
		local ref_dst_dir="${fixture_root}/reference/fix-validation-after"
		if [[ -d "$ref_src_dir" ]]; then
			local processed
			processed=$(find "$ref_src_dir" -maxdepth 1 -type f -name '*processed.flac' \
				-printf '%p\n' 2>/dev/null | sort | head -1)
			if [[ -n "$processed" && -f "$processed" ]]; then
				log_info "Rendering fix-validation reference: ${processed}"
				mkdir -p "$ref_dst_dir"
				render_spectrogram "$processed" "${ref_dst_dir}/spectrogram-full.png"
				render_spectrogram "$processed" "${ref_dst_dir}/spectrogram-low.png" 2000
				capture_metrics "$processed" "${ref_dst_dir}/metrics.json"
				log_success "fix-validation reference: ${ref_dst_dir}/"
			else
				log_warn "Fix-validation reference dir present but no *processed.flac found in ${ref_src_dir}; skipping"
			fi
		else
			log_warn "Fix-validation reference dir absent (${ref_src_dir}); skipping"
		fi
	else
		log_info "Fix-validation reference skipped for ${stem} (not applicable)"
	fi

	# --- Per-variant runs ----------------------------------------------
	local variant_count=${#MATRIX[@]}
	local idx=0
	local row name s p r m question expected
	for row in "${MATRIX[@]}"; do
		IFS='|' read -r name s p r m question expected <<<"$row"
		idx=$((idx + 1))
		log_header "[${stem}] [${idx}/${variant_count}] ${name}"
		echo "  question: $question"
		echo "  expected: $expected"
		echo "  params:   s=${s} p=${p}s r=${r}s m=${m}"

		local out_dir="${fixture_root}/variants/${name}"
		mkdir -p "$out_dir"

		local filter
		filter=$(build_filter "$s" "$p" "$r" "$m")
		printf '%s\n' "$filter" >"${out_dir}/filter.txt"

		local out_flac="${out_dir}/processed.flac"
		echo "  filter:   $filter"

		# A single bad cell must not abort the run. Disable errexit
		# locally around the ffmpeg invocation, log on failure, and
		# move on to the next variant.
		local t0 t1 wall_seconds
		t0=$(date +%s.%N)
		set +e
		ffmpeg -y -hide_banner -loglevel error -nostats \
			-i "$fixture_path" "${duration_arg[@]}" \
			-af "$filter" \
			-ac 1 -ar "$src_rate" \
			-c:a flac -compression_level 8 \
			"$out_flac"
		local rc=$?
		set -e
		t1=$(date +%s.%N)
		if ((rc != 0)); then
			log_error "ffmpeg failed (rc=${rc}) for ${stem}/${name}; skipping artefacts and continuing"
			echo
			continue
		fi
		wall_seconds=$(awk -v a="$t0" -v b="$t1" 'BEGIN {printf "%.2f", b - a}')
		printf 'fixture=%s\nvariant=%s\nwall_seconds=%s\nfilter=%s\nfixture_path=%s\nduration_arg=%s\n' \
			"$stem" "$name" "$wall_seconds" "$filter" "$fixture_path" "${DURATION:-full}" \
			>"${out_dir}/timing.txt"

		# Spectrograms + metrics. Wrap in errexit-off so a single
		# rendering hiccup doesn't kill the whole run.
		set +e
		render_spectrogram "$out_flac" "${out_dir}/spectrogram-full.png"
		render_spectrogram "$out_flac" "${out_dir}/spectrogram-low.png" 2000
		capture_metrics "$out_flac" "${out_dir}/metrics.json"
		set -e

		local lufs_i lufs_tp centroid rolloff flatness rms_db peak_db
		lufs_i=$(metrics_field "${out_dir}/metrics.json" lufs_i)
		lufs_tp=$(metrics_field "${out_dir}/metrics.json" lufs_tp)
		centroid=$(metrics_field "${out_dir}/metrics.json" spectral_centroid_hz)
		rolloff=$(metrics_field "${out_dir}/metrics.json" spectral_rolloff_hz)
		flatness=$(metrics_field "${out_dir}/metrics.json" spectral_flatness)
		rms_db=$(metrics_field "${out_dir}/metrics.json" rms_db)
		peak_db=$(metrics_field "${out_dir}/metrics.json" peak_db)
		[[ -z "$lufs_i" ]] && lufs_i="0"
		[[ -z "$lufs_tp" ]] && lufs_tp="0"
		[[ -z "$centroid" ]] && centroid="0"
		[[ -z "$rolloff" ]] && rolloff="0"
		[[ -z "$flatness" ]] && flatness="0"
		[[ -z "$rms_db" ]] && rms_db="0"
		[[ -z "$peak_db" ]] && peak_db="0"

		printf "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n" \
			"$stem" "$name" "$wall_seconds" \
			"$lufs_i" "$lufs_tp" "$centroid" "$rolloff" "$flatness" "$rms_db" "$peak_db" \
			>>"$summary_tsv"

		log_success "  ${name}: ${wall_seconds}s wall, LUFS ${lufs_i}, TP ${lufs_tp}, centroid ${centroid} Hz"
		echo
	done
}

# ============================================================================
# Main
# ============================================================================

main() {
	log_header "anlmdn matrix spike (source-rate, dual-fixture)"

	require_cmd ffmpeg
	require_cmd ffprobe

	# Verify both fixtures exist before doing any work.
	local missing=0
	local f
	for f in "${FIXTURES[@]}"; do
		if [[ ! -f "$f" ]]; then
			log_error "Fixture not found: $f"
			missing=1
		fi
	done
	if ((missing)); then
		log_error "One or more fixtures are missing; aborting."
		exit 1
	fi

	local variant_count=${#MATRIX[@]}
	local fixture_count=${#FIXTURES[@]}
	local total_runs=$((variant_count * fixture_count))

	# Collect per-fixture metadata for the banner.
	local fixtures_inline=""
	for f in "${FIXTURES[@]}"; do
		local sr dur stem
		sr=$(detect_sample_rate "$f")
		dur=$(detect_duration "$f")
		stem=$(fixture_stem "$f")
		fixtures_inline+="- \`${f}\` (${stem}, ${sr} Hz, ${dur} s)"$'\n'
	done

	local effective_duration_label="full file"
	if [[ -n "$DURATION" ]]; then
		effective_duration_label="${DURATION} s per fixture"
	fi

	echo
	echo "  Fixtures:"
	for f in "${FIXTURES[@]}"; do
		echo "    - $f"
	done
	echo "  Variants:          ${variant_count}"
	echo "  Total runs:        ${total_runs} (${variant_count} variants × ${fixture_count} fixtures, source rate only)"
	echo "  Effective length:  ${effective_duration_label}"
	echo "  Output root:       $OUTPUT_ROOT"
	echo

	log_header "Matrix"
	printf "  %-18s %-9s %-9s %-9s %-4s %s\n" "name" "s" "p (sec)" "r (sec)" "m" "question"
	printf "  %s\n" "──────────────────────────────────────────────────────────────────────────────────────────"
	local row name s p r m question expected
	for row in "${MATRIX[@]}"; do
		IFS='|' read -r name s p r m question expected <<<"$row"
		printf "  %-18s %-9s %-9s %-9s %-4s %s\n" "$name" "$s" "$p" "$r" "$m" "$question"
	done
	echo

	# Runtime estimate. Per-fixture model from the previous spike's wall
	# times: v031_baseline ~11.5s + m_strict ~11.5s + r_half ~5.9s
	# + r_half_m_strict ~5.9s + r_half_m_relax ~5.9s + r_min ~4.0s
	# ≈ 45 s of pure anlmdn work per fixture at the 300 s default. With
	# spectrogram and metrics overhead realistic total is 2–3 minutes;
	# we pad the upper bound to ~10 minutes for safety.
	local est_seconds_per_fixture=45
	local est_minutes_total
	est_minutes_total=$(awk -v n="$fixture_count" -v p="$est_seconds_per_fixture" \
		'BEGIN {printf "%.1f", (n * p + 60) / 60}')
	log_warn "Estimated total wall time: ~2–3 min realistic, ~10 min upper bound (anlmdn ${est_seconds_per_fixture}s × ${fixture_count} fixtures + overhead, ~${est_minutes_total} min point estimate)."
	log_warn "If this run exceeds 15 min, abort and investigate."

	if ((DRY_RUN)); then
		echo
		log_header "Dry run — planned commands"
		local dry_duration_arg=""
		if [[ -n "$DURATION" ]]; then
			dry_duration_arg="-t ${DURATION}"
		fi
		local fix
		for fix in "${FIXTURES[@]}"; do
			local stem
			stem=$(fixture_stem "$fix")
			echo
			echo "  ## Fixture: ${stem}"
			echo
			local first=1
			for row in "${MATRIX[@]}"; do
				IFS='|' read -r name s p r m question expected <<<"$row"
				local f_chain
				f_chain=$(build_filter "$s" "$p" "$r" "$m")
				if ((first)); then
					echo "  # ${stem} / ${name} (source rate; no cap, no exit-restore aformat)"
					echo "  ffmpeg -y -hide_banner -i \"$fix\" ${dry_duration_arg} -af '${f_chain}' -c:a flac -compression_level 8 \\"
					echo "    \"${OUTPUT_ROOT}/fixtures/${stem}/variants/${name}/processed.flac\""
					echo
					first=0
				else
					printf "  # %-18s %s\n" "$name" "$f_chain"
				fi
			done
		done
		echo
		echo "  Per run: showspectrumpic full + 0–2 kHz zoom, ebur128/astats/aspectralstats metrics, wall-clock timing."
		echo

		log_header "Reference phase plan"
		local ref_src_dir="${FIX_VALIDATION_ROOT}/after"
		echo "  Fix-validation reference is rendered for LMP-68-mark only."
		if [[ -d "$ref_src_dir" ]]; then
			local processed
			processed=$(find "$ref_src_dir" -maxdepth 1 -type f -name '*processed.flac' \
				-printf '%p\n' 2>/dev/null | sort | head -1)
			if [[ -n "$processed" ]]; then
				log_info "fixtures/LMP-68-mark/reference/fix-validation-after/ ← ${processed}"
			else
				log_warn "fix-validation reference dir present but no *processed.flac found in ${ref_src_dir}; will skip"
			fi
		else
			log_warn "fix-validation reference dir absent (${ref_src_dir}); will skip silently in real run"
		fi
		echo "  LMP-69-martin has no fix-validation reference — skipped by design."
		echo

		log_success "Dry run complete. Re-run without -n to execute."
		return 0
	fi

	if ! ((ASSUME_YES)); then
		echo
		read -r -p "Proceed with ${total_runs} runs (${effective_duration_label})? [y/N] " reply
		if [[ ! "$reply" =~ ^[Yy] ]]; then
			log_warn "Aborted by user."
			exit 0
		fi
	fi

	# Fresh wipe — this is a new spike; previous artefacts must not leak
	# into the new tree (matrix structure has changed, summary.tsv columns
	# have changed, fixture layer is new).
	if [[ -d "$OUTPUT_ROOT" ]]; then
		log_info "Wiping previous output tree: ${OUTPUT_ROOT}"
		rm -rf "$OUTPUT_ROOT"
	fi
	mkdir -p "${OUTPUT_ROOT}/fixtures"
	write_matrix_doc "${OUTPUT_ROOT}/matrix.md" "$fixtures_inline"

	# Initialise summary.tsv with the new fixture-aware schema.
	local summary_tsv="${OUTPUT_ROOT}/summary.tsv"
	printf "fixture\tvariant\twall_seconds\tlufs_i\tlufs_tp\tcentroid_hz\trolloff_hz\tflatness\trms_db\tpeak_db\n" \
		>"$summary_tsv"

	local run_t0 run_t1 elapsed
	run_t0=$(date +%s.%N)

	for f in "${FIXTURES[@]}"; do
		process_fixture "$f" "$summary_tsv"
	done

	run_t1=$(date +%s.%N)
	elapsed=$(awk -v a="$run_t0" -v b="$run_t1" 'BEGIN {printf "%.2f", b - a}')

	# ------------------------------------------------------------------------
	# Results summary table
	# ------------------------------------------------------------------------
	log_header "Results summary"
	echo
	printf "  %-16s %-18s %12s %10s %10s %12s %12s\n" \
		"fixture" "variant" "wall (s)" "LUFS I" "LUFS TP" "centroid" "rolloff"
	printf "  %s\n" "──────────────────────────────────────────────────────────────────────────────────────────"

	tail -n +2 "$summary_tsv" | while IFS=$'\t' read -r fixture name wall lufs_i lufs_tp centroid rolloff flatness rms_db peak_db; do
		printf "  %-16s ${YELLOW}%-18s${NC} %12s %10s %10s %12s %12s\n" \
			"$fixture" "$name" "$wall" "$lufs_i" "$lufs_tp" "$centroid" "$rolloff"
	done
	echo
	echo "  Total elapsed:   ${elapsed} s"
	echo "  Artefacts:       ${OUTPUT_ROOT}/"
	echo "  Matrix doc:      ${OUTPUT_ROOT}/matrix.md"
	echo "  Summary TSV:     ${summary_tsv}"
	echo
	log_success "Matrix spike complete."
}

main "$@"
