#!/usr/bin/env bash
# afftdn-spike.sh - Validate FFmpeg afftdn noise reduction in isolation
#
# Tests whether afftdn actually removes noise when given a known silence sample.
# Uses the same silence windows identified by Jivetalking's Pass 1 analysis.
#
# This spike isolates afftdn from Jivetalking's filter chain to determine if:
# 1. afftdn works at all with our parameters
# 2. The noise profile from silence samples is being learned correctly
# 3. Noise reduction is measurable in before/after comparisons
#
# Usage: ./scripts/afftdn-spike.sh [duration_seconds]
#   Default duration: 900 (15 minutes)

set -euo pipefail

# Configuration
TESTDATA_DIR="testdata"
OUTPUT_DIR="testdata/afftdn-spike"
DURATION="${1:-900}"

# Presenter configurations (from Jivetalking logs for LMP-72)
# Format: "name|silence_start|silence_end|silence_rms|trough"
# silence_end = silence_start + duration (10s windows)
# Measurements from source audio analysis
declare -a PRESENTERS=(
    "mark|31.208|41.208|-82.2|-83.6"
    "martin|25.078|35.078|-76.0|-79.6"
    "popey|25.078|35.078|-72.7|-75.5"
)

# Sweep configurations to test
# Format: "label|nr|nf_offset|rf|ad|fo|gs|tr"
# nf_offset: added to measured silence RMS to get noise_floor
# rf: residual floor (target floor after processing)
# ad: adaptivity (0=instant, 1=slow)
# fo: floor offset multiplier (>1 = more aggressive on estimated floor)
# gs: gain smoothing radius
# tr: track_residual (0/1) - enable residual tracking
#
# Key findings from initial testing:
# - rf=-38 (default) caps noise reduction - too conservative
# - rf=-60/-70 unlocks 4-5dB more reduction
# - fo parameter might help with tonal noise (mains hum)
declare -a CONFIGS=(
    # Baseline (current Jivetalking-like)
    "baseline|6|2|-38|0.7|1.0|0|0"
    # Previous best - good reduction, decent centroid preservation
    "tuned|12|0|-70|0.5|1.2|10|0"
    # Tuned without floor_offset - isolate fo's effect
    "tuned-no-fo|12|0|-70|0.5|1.0|10|0"
    # Tuned with track_residual enabled - better tonal noise handling?
    "tuned-tr|12|0|-70|0.5|1.2|10|1"
    # Test floor_offset for mains hum (tonal noise)
    # Higher fo = more aggressive on estimated floor = better hum reduction
    "fo-high|12|0|-70|0.5|1.8|10|0"
    # Maximum aggression (risk of artifacts)
    "max-nr|18|0|-75|0.3|1.5|15|0"
    # Conservative with high fo - balance?
    "conservative-fo|8|0|-65|0.5|1.5|8|0"
    # Audacity equivalent - nr=30dB like Audacity's setting
    "audacity-30db|30|0|-70|0.6|1.2|3|0"
)

# Colours for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

# ============================================================================
# Helper Functions
# ============================================================================

log_info() {
    echo -e "${YELLOW}▶${NC} $1"
}

log_success() {
    echo -e "${GREEN}✓${NC} $1"
}

log_error() {
    echo -e "${RED}✗${NC} $1"
}

log_header() {
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}  $1${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════${NC}"
}

# Measure audio characteristics using FFmpeg
# Returns: rms_db peak_db centroid rolloff flatness
measure_audio() {
    local input="$1"
    local duration="$2"

    # Get astats via metadata output
    local astats
    astats=$(ffmpeg -hide_banner -i "$input" -t "$duration" \
        -af "aformat=channel_layouts=mono,astats=metadata=1,ametadata=print:file=-" \
        -f null - 2>&1)

    # Parse RMS level (overall, last value)
    local rms_db
    rms_db=$(echo "$astats" | grep -oP 'astats\.Overall\.RMS_level=\K[-0-9.]+' | tail -1)

    # Parse peak level
    local peak_db
    peak_db=$(echo "$astats" | grep -oP 'astats\.Overall\.Peak_level=\K[-0-9.]+' | tail -1)

    # Parse RMS trough (noise floor indicator)
    local trough_db
    trough_db=$(echo "$astats" | grep -oP 'astats\.1\.RMS_trough=\K[-0-9.]+' | tail -1)

    # Get spectral stats
    local spectral
    spectral=$(ffmpeg -hide_banner -i "$input" -t "$duration" \
        -af "aformat=channel_layouts=mono,aspectralstats=measure=centroid+rolloff+flatness,ametadata=print:file=-" \
        -f null - 2>&1)

    local centroid rolloff flatness
    centroid=$(echo "$spectral" | grep -oP 'aspectralstats\.1\.centroid=\K[0-9.]+' | \
        awk '{s+=$1; n++} END {if(n>0) print s/n; else print 0}')
    rolloff=$(echo "$spectral" | grep -oP 'aspectralstats\.1\.rolloff=\K[0-9.]+' | \
        awk '{s+=$1; n++} END {if(n>0) print s/n; else print 0}')
    flatness=$(echo "$spectral" | grep -oP 'aspectralstats\.1\.flatness=\K[0-9.]+' | \
        awk '{s+=$1; n++} END {if(n>0) print s/n; else print 0}')

    echo "$rms_db $peak_db $trough_db $centroid $rolloff $flatness"
}

# Measure silence region specifically (for noise floor comparison)
# Arguments: input_file start_time end_time
measure_silence() {
    local input="$1"
    local start="$2"
    local end="$3"
    local duration
    duration=$(awk -v s="$start" -v e="$end" 'BEGIN {printf "%.3f", e - s}')

    local astats
    astats=$(ffmpeg -hide_banner -ss "$start" -i "$input" -t "$duration" \
        -af "aformat=channel_layouts=mono,astats=metadata=1,ametadata=print:file=-" \
        -f null - 2>&1)

    local rms_db
    rms_db=$(echo "$astats" | grep -oP 'astats\.Overall\.RMS_level=\K[-0-9.]+' | tail -1)

    echo "$rms_db"
}

# Build afftdn filter string with full parameter control
# Arguments: silence_start silence_end nr nf rf ad fo gs tr
# Returns: filter chain "asendcmd=...,afftdn@dns=..."
build_afftdn_filter() {
    local silence_start="$1"
    local silence_end="$2"
    local nr="$3"   # noise reduction (dB)
    local nf="$4"   # noise floor (dB)
    local rf="$5"   # residual floor (dB)
    local ad="$6"   # adaptivity (0-1)
    local fo="$7"   # floor offset multiplier
    local gs="$8"   # gain smooth radius
    local tr="$9"   # track residual (0/1)

    # Build asendcmd to trigger noise sampling at specific times
    local cmd_spec="asendcmd=c='${silence_start} afftdn@dns sn start; ${silence_end} afftdn@dns sn stop'"

    # Build afftdn filter with all tunable parameters
    local afftdn_spec="afftdn@dns=nr=${nr}:nf=${nf}:rf=${rf}:tn=1:ad=${ad}:fo=${fo}:gs=${gs}:tr=${tr}:om=o"

    echo "${cmd_spec},${afftdn_spec}"
}

# Format delta with colour coding
format_delta() {
    local delta="$1"
    local threshold="$2"
    local suffix="${3:-}"

    if [[ -z "$delta" ]]; then
        printf "    N/A"
        return
    fi

    # Check if absolute value is within threshold
    local abs_delta
    abs_delta=$(awk -v d="$delta" 'BEGIN {print (d < 0) ? -d : d}')

    if awk -v a="$abs_delta" -v t="$threshold" 'BEGIN {exit (a <= t) ? 0 : 1}'; then
        printf "${GREEN}%+.2f%s${NC}" "$delta" "$suffix"
    else
        printf "${YELLOW}%+.2f%s${NC}" "$delta" "$suffix"
    fi
}

# ============================================================================
# Main Processing
# ============================================================================

main() {
    log_header "afftdn Parameter Sweep"
    echo ""
    echo "Duration: ${DURATION}s ($(awk -v d="$DURATION" 'BEGIN {printf "%.1f", d/60}') minutes)"
    echo "Output:   ${OUTPUT_DIR}/"
    echo ""
    echo "Configurations:"
    for cfg in "${CONFIGS[@]}"; do
        IFS='|' read -r label nr nf_offset rf ad fo gs tr <<< "$cfg"
        echo "  ${label}: nr=${nr}dB rf=${rf}dB ad=${ad} fo=${fo} gs=${gs} tr=${tr}"
    done
    echo ""

    # Create output directory
    mkdir -p "$OUTPUT_DIR"

    # Arrays to store results for summary
    # Key format: "presenter|config"
    declare -A SRC_TROUGH SRC_CENTROID SILENCE_BEFORE
    declare -A OUT_TROUGH OUT_CENTROID SILENCE_AFTER

    # ========================================================================
    # Phase 1: Process each presenter with each configuration
    # ========================================================================

    for presenter_config in "${PRESENTERS[@]}"; do
        IFS='|' read -r name silence_start silence_end silence_rms trough <<< "$presenter_config"

        local src_file="${TESTDATA_DIR}/LMP-72-${name}.flac"

        if [[ ! -f "$src_file" ]]; then
            log_error "Source file not found: $src_file"
            continue
        fi

        log_header "Processing: ${name}"
        echo "  Silence window: ${silence_start}s – ${silence_end}s"
        echo "  Silence RMS: ${silence_rms} dBFS"
        echo "  Trough: ${trough} dB"
        echo ""

        # Measure source (first DURATION seconds)
        log_info "Measuring source audio..."
        read -r src_rms _ src_trough src_centroid _ _ \
            <<< "$(measure_audio "$src_file" "$DURATION")"

        SRC_TROUGH[$name]="$src_trough"
        SRC_CENTROID[$name]="$src_centroid"

        printf "  Source: RMS %.1f dB | Trough %.1f dB | Centroid %.0f Hz\n" \
            "$src_rms" "$src_trough" "$src_centroid"

        # Measure silence region in source
        local silence_before
        silence_before=$(measure_silence "$src_file" "$silence_start" "$silence_end")
        SILENCE_BEFORE[$name]="$silence_before"
        printf "  Silence RMS: %.1f dBFS\n" "$silence_before"
        echo ""

        # Process with each configuration
        for cfg in "${CONFIGS[@]}"; do
            IFS='|' read -r label nr nf_offset rf ad fo gs tr <<< "$cfg"

            # Calculate noise floor from measured silence RMS + offset
            local nf
            nf=$(awk -v rms="$silence_rms" -v offset="$nf_offset" 'BEGIN {printf "%.1f", rms + offset}')
            # Clamp to valid range (-80 to -20)
            nf=$(awk -v nf="$nf" 'BEGIN {if(nf < -80) print -80; else if(nf > -20) print -20; else print nf}')

            local out_file="${OUTPUT_DIR}/LMP-72-${name}-${label}.flac"
            local key="${name}|${label}"

            log_info "Config: ${label}"
            echo "  nr=${nr}dB nf=${nf}dB rf=${rf}dB ad=${ad} fo=${fo} gs=${gs} tr=${tr}"

            local filter
            filter=$(build_afftdn_filter "$silence_start" "$silence_end" "$nr" "$nf" "$rf" "$ad" "$fo" "$gs" "$tr")

            if ! ffmpeg -hide_banner -loglevel error -stats \
                -i "$src_file" -t "$DURATION" \
                -af "aformat=channel_layouts=mono,${filter}" \
                -c:a flac -compression_level 8 \
                "$out_file" -y 2>&1; then
                log_error "Processing failed"
                continue
            fi
            echo ""

            # Measure output
            read -r _ _ out_trough out_centroid _ _ \
                <<< "$(measure_audio "$out_file" "$DURATION")"
            OUT_TROUGH[$key]="$out_trough"
            OUT_CENTROID[$key]="$out_centroid"

            local silence_after
            silence_after=$(measure_silence "$out_file" "$silence_start" "$silence_end")
            SILENCE_AFTER[$key]="$silence_after"

            local d_silence
            d_silence=$(awk -v a="$silence_after" -v b="$silence_before" 'BEGIN {printf "%.1f", a - b}')
            printf "  Result: Silence Δ "
            format_delta "$d_silence" "3.0" " dB"
            printf " | Trough %.1f dB → %.1f dB\n" "$src_trough" "$out_trough"
            log_success "$out_file"
            echo ""
        done
    done

    # ========================================================================
    # Phase 2: Summary Table
    # ========================================================================

    log_header "Results Summary"
    echo ""

    # Print results grouped by presenter
    for presenter_config in "${PRESENTERS[@]}"; do
        IFS='|' read -r name _ _ _ _ <<< "$presenter_config"

        if [[ -z "${SRC_TROUGH[$name]:-}" ]]; then
            continue
        fi

        echo "=== ${name} ==="
        printf "%-12s │ %12s │ %12s │ %12s\n" \
            "Config" "Silence Δ" "Trough Δ" "Centroid Δ"
        printf "─────────────┼──────────────┼──────────────┼──────────────\n"

        for cfg in "${CONFIGS[@]}"; do
            IFS='|' read -r label _ _ _ _ _ _ _ <<< "$cfg"
            local key="${name}|${label}"

            if [[ -n "${SILENCE_AFTER[$key]:-}" ]]; then
                local d_silence d_trough d_centroid
                d_silence=$(awk -v a="${SILENCE_AFTER[$key]}" -v b="${SILENCE_BEFORE[$name]}" \
                    'BEGIN {printf "%.1f", a - b}')
                d_trough=$(awk -v a="${OUT_TROUGH[$key]}" -v b="${SRC_TROUGH[$name]}" \
                    'BEGIN {printf "%.1f", a - b}')
                d_centroid=$(awk -v a="${OUT_CENTROID[$key]}" -v b="${SRC_CENTROID[$name]}" \
                    'BEGIN {if(b>0) printf "%.1f", ((a-b)/b)*100; else print 0}')

                printf "%-12s │ " "$label"
                format_delta "$d_silence" "3.0" " dB"
                printf "   │ "
                format_delta "$d_trough" "3.0" " dB"
                printf "   │ "
                format_delta "$d_centroid" "5.0" "%"
                echo ""
            fi
        done
        echo ""
    done

    echo "Interpretation:"
    echo "  Silence Δ: Negative = noise reduced in silence regions (target: < -6 dB)"
    echo "  Trough Δ:  Negative = lower noise floor overall"
    echo "  Centroid:  Should stay close to 0% (no spectral damage)"
    echo ""
    echo "Output files:"
    for presenter_config in "${PRESENTERS[@]}"; do
        IFS='|' read -r name _ _ _ _ <<< "$presenter_config"
        for cfg in "${CONFIGS[@]}"; do
            IFS='|' read -r label _ _ _ _ _ _ _ <<< "$cfg"
            echo "  ${OUTPUT_DIR}/LMP-72-${name}-${label}.flac"
        done
    done
    echo ""
    log_success "Parameter sweep complete"
}

main "$@"
