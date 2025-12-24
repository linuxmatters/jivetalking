#!/usr/bin/env bash
# anlmdn-spike.sh - Test FFmpeg anlmdn (Non-Local Means) denoiser
#
# Unlike afftdn which requires a noise profile, anlmdn works by finding similar
# patterns in the audio and averaging them to reduce noise. It's more suitable
# for stationary noise and doesn't require silence detection.
#
# Parameters:
# - strength (s): 0.00001 to 10000, default 0.00001 (higher = more reduction)
# - patch (p): 1-100ms, default 2ms (context window for similarity)
# - research (r): 2-300ms, default 6ms (search radius for similar patches)
# - smooth (m): 1-1000, default 11 (smoothing factor for weights)
#
# Usage: ./scripts/anlmdn-spike.sh [duration_seconds]
#   Default duration: 900 (15 minutes)

set -euo pipefail

# Configuration
TESTDATA_DIR="testdata"
OUTPUT_DIR="testdata/anlmdn-spike"
DURATION="${1:-900}"

# Presenter configurations (same as afftdn-spike)
declare -a PRESENTERS=(
    "mark|31.208|41.208|-82.2|-83.6"
    "martin|25.078|35.078|-76.0|-79.6"
    "popey|25.078|35.078|-72.7|-75.5"
)

# Test configurations
# Format: "label|strength|patch_sec|research_sec|smooth|compand"
# Keep strength constant at 0.00001
# compand: 0=disabled, 1=enabled (DNS-1500 style residual suppression)
declare -a CONFIGS=(
    # Default settings (best quality)
    "default|0.00001|0.002|0.006|11|0"
    # Fast config (best quality/speed balance)
    "fast|0.00001|0.006|0.0058|11|0"
    # Fast with compand (DNS-1500 inspired residual suppression)
    "fast-compand|0.00001|0.006|0.0058|11|1"
    # Minimum patch/research (fastest)
    "fastest|0.00001|0.001|0.002|11|0"
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
measure_audio() {
    local input="$1"
    local duration="$2"

    # Get astats
    local astats
    astats=$(ffmpeg -hide_banner -i "$input" -t "$duration" \
        -af "aformat=channel_layouts=mono,astats=metadata=1,ametadata=print:file=-" \
        -f null - 2>&1)

    local rms_db peak_db
    rms_db=$(echo "$astats" | grep -oP 'astats\.Overall\.RMS_level=\K[-0-9.]+' | tail -1)
    peak_db=$(echo "$astats" | grep -oP 'astats\.Overall\.Peak_level=\K[-0-9.]+' | tail -1)

    # Get full spectral stats - average all frames
    local spectral
    spectral=$(ffmpeg -hide_banner -i "$input" -t "$duration" \
        -af "aformat=channel_layouts=mono,aspectralstats=measure=centroid+rolloff+flatness,ametadata=print:file=-" \
        -f null - 2>&1)

    local centroid rolloff flatness
    centroid=$(echo "$spectral" | grep -oP 'aspectralstats\.1\.centroid=\K[0-9.]+' | \
        awk '{sum+=$1; count++} END {if(count>0) printf "%.0f", sum/count; else print 0}')
    rolloff=$(echo "$spectral" | grep -oP 'aspectralstats\.1\.rolloff=\K[0-9.]+' | \
        awk '{sum+=$1; count++} END {if(count>0) printf "%.0f", sum/count; else print 0}')
    flatness=$(echo "$spectral" | grep -oP 'aspectralstats\.1\.flatness=\K[0-9.]+' | \
        awk '{sum+=$1; count++} END {if(count>0) printf "%.4f", sum/count; else print 0}')

    # Provide defaults if missing
    [[ -z "$rms_db" ]] && rms_db="0"
    [[ -z "$peak_db" ]] && peak_db="0"
    [[ -z "$centroid" ]] && centroid="0"
    [[ -z "$rolloff" ]] && rolloff="0"
    [[ -z "$flatness" ]] && flatness="0"

    echo "$rms_db $peak_db $centroid $rolloff $flatness"
}

# Measure silence region
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

# Build anlmdn filter string with optional compand
build_anlmdn_filter() {
    local strength="$1"
    local patch_ms="$2"
    local research_ms="$3"
    local smooth="$4"
    local compand="$5"

    local filter="anlmdn=s=${strength}:p=${patch_ms}:r=${research_ms}:m=${smooth}"

    # Add DNS-1500 inspired compand for residual noise suppression
    # FLAT reduction curve: uniform 10dB expansion below -50dB threshold
    # Attack: 5ms, Decay: 100ms, Soft knee: 6dB (transparent)
    if [[ "$compand" == "1" ]]; then
        filter="${filter},compand=attacks=0.005:decays=0.100:soft-knee=6.0:points=-90/-100|-75/-85|-50/-50|-30/-30|0/0"
    fi

    echo "$filter"
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
    log_header "anlmdn Parameter Sweep"
    echo ""
    echo "Duration: ${DURATION}s ($(awk -v d="$DURATION" 'BEGIN {printf "%.1f", d/60}') minutes)"
    echo "Output:   ${OUTPUT_DIR}/"
    echo ""
    echo "Configurations:"
    for cfg in "${CONFIGS[@]}"; do
        IFS='|' read -r label strength patch research smooth compand <<< "$cfg"
        local comp_str=""
        [[ "$compand" == "1" ]] && comp_str=" +compand"
        echo "  ${label}: s=${strength} p=${patch}s r=${research}s m=${smooth}${comp_str}"
    done
    echo ""

    mkdir -p "$OUTPUT_DIR"

    declare -A SRC_CENTROID SRC_ROLLOFF SRC_FLATNESS SILENCE_BEFORE
    declare -A OUT_CENTROID OUT_ROLLOFF OUT_FLATNESS SILENCE_AFTER

    # ========================================================================
    # Phase 1: Process each presenter with each configuration
    # ========================================================================

    for presenter_config in "${PRESENTERS[@]}"; do
        IFS='|' read -r name silence_start silence_end silence_rms trough <<< "$presenter_config"

        src_file="${TESTDATA_DIR}/LMP-72-${name}.flac"

        if [[ ! -f "$src_file" ]]; then
            log_error "Source file not found: $src_file"
            continue
        fi

        log_header "Processing: ${name}"
        echo "  Silence window: ${silence_start}s – ${silence_end}s"
        echo "  Silence RMS: ${silence_rms} dBFS"
        echo "  Trough: ${trough} dB"
        echo ""

        # Measure source
        log_info "Measuring source audio..."
        read -r src_rms src_peak src_centroid src_rolloff src_flatness \
            <<< "$(measure_audio "$src_file" "$DURATION")"

        SRC_CENTROID[$name]="$src_centroid"
        SRC_ROLLOFF[$name]="$src_rolloff"
        SRC_FLATNESS[$name]="$src_flatness"

        printf "  Source: RMS %.1f dB | Peak %.1f dB | Centroid %.0f Hz | Rolloff %.0f Hz | Flatness %.4f\n" \
            "$src_rms" "$src_peak" "$src_centroid" "$src_rolloff" "$src_flatness"

        # Measure silence region
        silence_before=$(measure_silence "$src_file" "$silence_start" "$silence_end")
        SILENCE_BEFORE[$name]="$silence_before"
        printf "  Silence RMS: %.1f dBFS\n" "$silence_before"
        echo ""

        # Process with each configuration
        for cfg in "${CONFIGS[@]}"; do
            IFS='|' read -r label strength patch research smooth compand <<< "$cfg"

            out_file="${OUTPUT_DIR}/LMP-72-${name}-${label}.flac"
            key="${name}|${label}"

            log_info "Config: ${label}"
            local comp_str=""
            [[ "$compand" == "1" ]] && comp_str=" +compand"
            echo "  s=${strength} p=${patch}s r=${research}s m=${smooth}${comp_str}"

            filter=$(build_anlmdn_filter "$strength" "$patch" "$research" "$smooth" "$compand")

            if ! ffmpeg -hide_banner -loglevel error -stats \
                -i "$src_file" -t "$DURATION" \
                -af "$filter" \
                -c:a flac -compression_level 8 \
                "$out_file" 2>&1; then
                log_error "Failed to process ${name} with ${label}"
                continue
            fi

            # Measure output
            read -r _ out_peak out_centroid out_rolloff out_flatness \
                <<< "$(measure_audio "$out_file" "$DURATION")"

            OUT_CENTROID[$key]="$out_centroid"
            OUT_ROLLOFF[$key]="$out_rolloff"
            OUT_FLATNESS[$key]="$out_flatness"

            # Measure output silence
            silence_after=$(measure_silence "$out_file" "$silence_start" "$silence_end")
            SILENCE_AFTER[$key]="$silence_after"

            # Calculate deltas
            d_silence=$(awk -v a="$silence_after" -v b="$silence_before" \
                'BEGIN {printf "%.1f", a - b}')

            printf "\n  Result: Silence Δ %.2f dB | Peak %.1f dB\n" \
                "$d_silence" "$out_peak"
            log_success "$out_file"
            echo ""
        done
    done

    # ========================================================================
    # Phase 2: Results Summary
    # ========================================================================

    log_header "Results Summary"
    echo ""

    for presenter_config in "${PRESENTERS[@]}"; do
        IFS='|' read -r name _ _ _ _ <<< "$presenter_config"

        echo "=== ${name} ==="
        printf "%-16s │ %12s │ %12s │ %12s │ %12s\n" \
            "Config" "Silence Δ" "Centroid Δ" "Rolloff Δ" "Flatness Δ"
        printf "─────────────────┼──────────────┼──────────────┼──────────────┼──────────────\n"

        for cfg in "${CONFIGS[@]}"; do
            IFS='|' read -r label _ _ _ _ <<< "$cfg"
            key="${name}|${label}"

            if [[ -n "${SILENCE_AFTER[$key]:-}" ]]; then
                d_silence=$(awk -v a="${SILENCE_AFTER[$key]}" -v b="${SILENCE_BEFORE[$name]}" \
                    'BEGIN {printf "%.1f", a - b}')
                d_centroid=$(awk -v a="${OUT_CENTROID[$key]}" -v b="${SRC_CENTROID[$name]}" \
                    'BEGIN {if(b>0) printf "%.1f", ((a-b)/b)*100; else print 0}')
                d_rolloff=$(awk -v a="${OUT_ROLLOFF[$key]}" -v b="${SRC_ROLLOFF[$name]}" \
                    'BEGIN {if(b>0) printf "%.1f", ((a-b)/b)*100; else print 0}')
                d_flatness=$(awk -v a="${OUT_FLATNESS[$key]}" -v b="${SRC_FLATNESS[$name]}" \
                    'BEGIN {if(b>0) printf "%.1f", ((a-b)/b)*100; else print 0}')

                printf "%-16s │ " "$label"
                format_delta "$d_silence" "3.0" " dB"
                printf "   │ "
                format_delta "$d_centroid" "5.0" "%"
                printf "   │ "
                format_delta "$d_rolloff" "5.0" "%"
                printf "   │ "
                format_delta "$d_flatness" "10.0" "%"
                echo ""
            fi
        done
        echo ""
    done

    echo "Interpretation:"
    echo "  Silence Δ:  Negative = noise reduced in silence regions (target: < -6 dB)"
    echo "  Centroid Δ: Should stay close to 0% (spectral brightness preserved)"
    echo "  Rolloff Δ:  Should stay close to 0% (high-frequency content preserved)"
    echo "  Flatness Δ: Should stay close to 0% (spectral balance preserved)"
    echo ""
    echo "Output files:"
    for presenter_config in "${PRESENTERS[@]}"; do
        IFS='|' read -r name _ _ _ _ <<< "$presenter_config"
        for cfg in "${CONFIGS[@]}"; do
            IFS='|' read -r label _ _ _ _ <<< "$cfg"
            echo "  ${OUTPUT_DIR}/LMP-72-${name}-${label}.flac"
        done
    done
    echo ""
    log_success "Parameter sweep complete"
}

main "$@"
