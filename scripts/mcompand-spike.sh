#!/usr/bin/env bash
# mcompand-spike.sh - Compand tuning for post-anlmdn residual suppression
#
# Tests gentler compand settings appropriate for use AFTER anlmdn has done
# the heavy noise reduction. Compand's role is now:
# - Residual noise suppression in silence regions
# - Breath noise attenuation between speech
# - NOT primary noise reduction (anlmdn does that)
#
# Compares two approaches across three presenters:
# 1. compand     - Single-band baseline (control)
# 2. mcompand    - FFmpeg's multiband with voice-protective scaling
#
# UPDATED 2024-12-24: Reduced expansion levels (4/6/8 dB) and lower thresholds
# for post-anlmdn tuning. Original aggressive settings (8/12/16 dB, -40/-45/-50 dB)
# were for compand-only noise reduction.

#
# Usage: ./scripts/mcompand-spike.sh [--level] [duration_seconds]
#   --level: Add subtle acompressor+speechnorm for listening evaluation
# Default duration: 300 (5 minutes)

set -euo pipefail

# Configuration
TESTDATA_DIR="testdata"
OUTPUT_DIR="testdata/mcompand-sweep"
PRESENTERS=("mark" "martin" "popey")
EXPANSION_LEVELS=(4 6 8)
LEVEL_OUTPUT=false
DURATION=300

# Parse CLI arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --level)
            LEVEL_OUTPUT=true
            shift
            ;;
        *)
            DURATION="$1"
            shift
            ;;
    esac
done

# Perceptual thresholds
THRESH_RMS=0.5        # ±0.5 dB
THRESH_CENTROID=10    # ±10%
THRESH_ROLLOFF=10     # ±10%
THRESH_FLATNESS=0.16  # ±0.16 absolute

# Colours for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m' # No colour

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

# Measure audio characteristics using FFmpeg
# Returns: rms_db trough_db centroid rolloff flatness
measure_audio() {
    local input="$1"
    local duration="$2"

    # Get astats via metadata output (RMS, trough)
    local astats
    astats=$(ffmpeg -hide_banner -i "$input" -t "$duration" \
        -af "aformat=channel_layouts=mono,astats=metadata=1,ametadata=print:file=-" \
        -f null - 2>&1)

    # Parse RMS level (overall, last value)
    local rms_db
    rms_db=$(echo "$astats" | grep -oP 'astats\.Overall\.RMS_level=\K[-0-9.]+' | tail -1)

    # Parse RMS trough (minimum RMS, indicates noise floor)
    local trough_db
    trough_db=$(echo "$astats" | grep -oP 'astats\.1\.RMS_trough=\K[-0-9.]+' | tail -1)

    # Get spectral stats via metadata (centroid, rolloff, flatness)
    local spectral
    spectral=$(ffmpeg -hide_banner -i "$input" -t "$duration" \
        -af "aformat=channel_layouts=mono,aspectralstats=measure=centroid+rolloff+flatness,ametadata=print:file=-" \
        -f null - 2>&1)

    # Parse spectral stats - calculate mean values
    local centroid rolloff flatness
    centroid=$(echo "$spectral" | grep -oP 'aspectralstats\.1\.centroid=\K[0-9.]+' | \
        awk '{s+=$1; n++} END {if(n>0) print s/n; else print 0}')
    rolloff=$(echo "$spectral" | grep -oP 'aspectralstats\.1\.rolloff=\K[0-9.]+' | \
        awk '{s+=$1; n++} END {if(n>0) print s/n; else print 0}')
    flatness=$(echo "$spectral" | grep -oP 'aspectralstats\.1\.flatness=\K[0-9.]+' | \
        awk '{s+=$1; n++} END {if(n>0) print s/n; else print 0}')

    echo "$rms_db $trough_db $centroid $rolloff $flatness"
}

# Build VOICE-PROTECTIVE mcompand filter spec
# Returns: mcompand filter string with per-band voice protection scaling
#
# Uses FLAT reduction curve but scales expansion per band:
#   Bands: sub-bass / chest / voice-F1 / voice-F2 / presence / air
#   Scale: 100% / 100% / 105% / 103% / 100% / 95%
# Voice formant bands (F1: 300-800Hz, F2: 800-3000Hz) get minimal expansion
# to preserve speech intelligibility and natural timbre.
#
# Per-band timing (attack,decay) and soft-knee:
#   sub-bass:  0.006,0.095 sk=4  (slower - rumble rides through)
#   chest:     0.005,0.100 sk=6  (responsive - wider knee for smooth transition)
#   voice F1:  0.005,0.100 sk=6  (responsive - wider knee for smooth transition)
#   voice F2:  0.005,0.100 sk=6  (responsive - wider knee for smooth transition)
#   presence:  0.002,0.085 sk=7  (fast - transient detail)
#   air:       0.002,0.080 sk=7  (fastest - preserve sibilance/breath detail)
#
# Arguments:
#   $1 - base_expansion: expansion depth in dB
#   $2 - threshold: expansion threshold in dB (default: -50)
#
# NOTE: mcompand has a bug in gain parameter parsing (af_mcompand.c:447 uses
# p2 instead of NULL in av_strtok, so gain_dB is never extracted).
# Makeup gain must be applied via separate volume filter.
build_mcompand_filter() {
    local base_expansion="$1"

    local threshold="${2:--50}"  # Default -50 dB if not specified
    # Voice-protective band scaling (percentage of expansion value)
    # Bands: sub-bass / chest / voice-F1 / voice-F2 / presence / air
    local scales=(100 100 105 103 100 95)
    local crossovers=(100 300 800 3300 8000 20500)
    # Per-band attack,decay timing
    local attacks=(0.006 0.005 0.005 0.005 0.002 0.002)
    local decays=(0.095 0.100 0.100 0.100 0.085 0.080)
    # Per-band soft-knee (wider for voice bands)
    local knees=(6 8 10 12 10 6)

    local filter="mcompand=args="
    local i
    for i in 0 1 2 3 4 5; do
        local scale=${scales[$i]}
        local crossover=${crossovers[$i]}
        local attack=${attacks[$i]}
        local decay=${decays[$i]}
        local knee=${knees[$i]}
        local band_exp
        band_exp=$(awk -v e="$base_expansion" -v s="$scale" 'BEGIN {printf "%.0f", e * s / 100}')

        # Build FLAT reduction curve for this band
        # Points below threshold get reduction; threshold and above stay at unity
        local out90 out75
        out90=$(awk -v e="$band_exp" 'BEGIN {printf "%.0f", -90 - e}')
        out75=$(awk -v e="$band_exp" 'BEGIN {printf "%.0f", -75 - e}')
        local curve="-90/${out90}\\,-75/${out75}\\,${threshold}/${threshold}\\,-30/-30\\,0/0"

        # Add band separator if not first band
        if [[ $i -gt 0 ]]; then
            filter+=" \\| "
        fi
        filter+="${attack}\\,${decay} ${knee} ${curve} ${crossover}"
    done

    # Append makeup gain to compensate for Linkwitz-Riley crossover loss (~1.3dB)
    # NOTE: Cannot use mcompand's inline gain parameter due to FFmpeg bug
    # Use precision=double to match mcompand's 64-bit output format (AV_SAMPLE_FMT_DBLP)
    filter+=",volume=1.3dB:precision=double"
    echo "$filter"
}

# Build subtle levelling chain for listening evaluation
# Returns: filter string with gentle acompressor + speechnorm
#
# Purpose: Make it easier to hear noise artifacts without massively boosting.
# Uses conservative settings to avoid colouring the audio.
#
# acompressor: Gentle 2:1 ratio, high threshold (-24dB), slow attack/release
build_level_chain() {
    # Gentle compressor: only catch peaks, don't squash dynamics
    # threshold=0.0625 = -24dB, ratio=2, attack=50ms, release=200ms
    local comp="acompressor=threshold=0.0625:ratio=2:attack=50:release=200:makeup=3.0:knee=4"

    echo ",${comp}"
}

# Calculate adaptive threshold based on RMS trough (noise floor indicator)
# Returns: threshold in dB (-60 to -50 range)
#
# POST-ANLMDN STRATEGY (gentler than original):
#   - Clean sources (trough < -85 dB): use -60 dB threshold (catches breaths)
#   - Moderate noise (-85 to -80 dB): use -55 dB threshold
#   - Noisy sources (trough > -80 dB): use -50 dB threshold
#
# These are LOWER than original (-50/-45/-40) because anlmdn already pushed
# the noise floor down significantly. Compand now targets residual noise
# and breath sounds, not primary room noise.
calculate_adaptive_threshold() {
    local trough="$1"

    awk -v t="$trough" 'BEGIN {
        if (t < -85) {
            print -60  # Clean: low threshold for breath taming
        } else if (t < -80) {
            print -55  # Moderate: slightly raised
        } else {
            print -50  # Noisy: catch residual noise
        }
    }'
}

# Check if value is within threshold (returns 0 for pass, 1 for fail)
check_threshold() {
    local delta="$1"
    local threshold="$2"

    # Handle empty values
    if [[ -z "$delta" || -z "$threshold" ]]; then
        return 1
    fi

    # Use awk with proper variable passing to avoid negative number issues
    awk -v d="$delta" -v t="$threshold" 'BEGIN {
        abs_d = (d < 0) ? -d : d
        exit (abs_d <= t) ? 0 : 1
    }'
}

# Format delta with pass/fail indicator
format_delta() {
    local delta="$1"
    local threshold="$2"
    local suffix="${3:-}"

    # Handle empty values
    if [[ -z "$delta" ]]; then
        printf "    N/A"
        return
    fi

    if check_threshold "$delta" "$threshold"; then
        printf "${GREEN}%+.2f%s${NC}" "$delta" "$suffix"
    else
        printf "${RED}%+.2f%s${NC}" "$delta" "$suffix"
    fi
}

# ============================================================================
# Main Processing
# ============================================================================

main() {
    echo "╔══════════════════════════════════════════════════════════════════╗"
    echo "║              mcompand Expansion Sweep - Post-anlmdn              ║"
    echo "╚══════════════════════════════════════════════════════════════════╝"
    echo ""
    if [[ "$LEVEL_OUTPUT" == "true" ]]; then
        echo "Duration: ${DURATION}s | Expansion levels: ${EXPANSION_LEVELS[*]} dB | Levelling: ON"
    else
        echo "Duration: ${DURATION}s | Expansion levels: ${EXPANSION_LEVELS[*]} dB | Levelling: OFF"
    fi
    echo ""

    # Create output directory
    mkdir -p "$OUTPUT_DIR"

    # Declare associative arrays for source measurements
    declare -A SRC_RMS SRC_TROUGH SRC_CENTROID SRC_ROLLOFF SRC_FLATNESS

    # ========================================================================
    # Phase 1: Measure source files
    # ========================================================================
    log_info "Phase 1: Measuring source audio..."
    echo ""

    for presenter in "${PRESENTERS[@]}"; do
        local src_file="${TESTDATA_DIR}/LMP-72-${presenter}.flac"

        if [[ ! -f "$src_file" ]]; then
            log_error "Source file not found: $src_file"
            continue
        fi

        log_info "Measuring ${presenter}..."
        read -r rms trough centroid rolloff flatness <<< "$(measure_audio "$src_file" "$DURATION")"

        SRC_RMS[$presenter]="$rms"
        SRC_TROUGH[$presenter]="$trough"
        SRC_CENTROID[$presenter]="$centroid"
        SRC_ROLLOFF[$presenter]="$rolloff"
        SRC_FLATNESS[$presenter]="$flatness"

        printf "  RMS: %.2f dB | Trough: %.1f dB | Centroid: %.0f Hz | Rolloff: %.0f Hz | Flatness: %.4f\n" \
            "$rms" "$trough" "$centroid" "$rolloff" "$flatness"
    done
    echo ""

    # ========================================================================
    # Generate single-band compand samples (control group)
    # ========================================================================
    log_info "Phase 2.1: Generating single-band compand samples (control)..."
    echo ""

    for presenter in "${PRESENTERS[@]}"; do
        local src_file="${TESTDATA_DIR}/LMP-72-${presenter}.flac"

        if [[ ! -f "$src_file" ]]; then
            continue
        fi

        log_info "${presenter} (single-band compand, threshold -55 dB)"

        for exp in "${EXPANSION_LEVELS[@]}"; do
            local out_file="${OUTPUT_DIR}/LMP-72-${presenter}-compand-exp${exp}dB.flac"

            # Build FLAT reduction curve with -55 dB threshold (post-anlmdn tuning)
            # Every point below threshold gets the SAME reduction amount
            # Points: -90, -75, -55 (threshold), -30, 0
            local out90 out75
            out90=$(awk -v e="$exp" 'BEGIN {printf "%.0f", -90 - e}')        # FLAT reduction at -90
            out75=$(awk -v e="$exp" 'BEGIN {printf "%.0f", -75 - e}')        # FLAT reduction at -75

            local filter="aformat=channel_layouts=mono,compand=attacks=0.005:decays=0.1:soft-knee=6:points=-90/${out90}|-75/${out75}|-55/-55|-30/-30|0/0"

            # Add levelling chain if requested
            if [[ "$LEVEL_OUTPUT" == "true" ]]; then
                filter+=$(build_level_chain)
            fi

            printf "  exp %2d dB: " "$exp"

            if ffmpeg -hide_banner -loglevel error -y \
                -i "$src_file" -t "$DURATION" \
                -af "$filter" \
                "$out_file" 2>/dev/null; then
                echo "    ✓"
            else
                echo "    ✗ (ffmpeg error)"
            fi
        done
    done
    echo ""

    # ========================================================================
    # Generate voice-protective mcompand samples (fixed threshold)
    # ========================================================================
    log_info "Phase 2.2: Generating voice-protective mcompand samples (fixed -55dB threshold)..."
    echo ""

    for presenter in "${PRESENTERS[@]}"; do
        local src_file="${TESTDATA_DIR}/LMP-72-${presenter}.flac"

        if [[ ! -f "$src_file" ]]; then
            continue
        fi

        log_info "${presenter} (6-band mcompand, voice-protective scaling, threshold -55dB)"

        for exp in "${EXPANSION_LEVELS[@]}"; do
            local out_file="${OUTPUT_DIR}/LMP-72-${presenter}-mcompand-exp${exp}dB.flac"
            local filter
            filter=$(build_mcompand_filter "$exp" "-55")
            # Add mono format prefix
            filter="aformat=channel_layouts=mono,${filter}"

            # Add levelling chain if requested
            if [[ "$LEVEL_OUTPUT" == "true" ]]; then
                filter+=$(build_level_chain)
            fi

            printf "  exp %2d dB: " "$exp"

            if ffmpeg -hide_banner -loglevel error -y \
                -i "$src_file" -t "$DURATION" \
                -af "$filter" \
                "$out_file" 2>/dev/null; then
                echo "    ✓"
            else
                echo "    ✗ (ffmpeg error)"
            fi
        done
    done
    echo ""

    # ========================================================================
    # Generate adaptive mcompand samples (threshold tuned to RMS trough)
    # ========================================================================
    log_info "Phase 2.3: Generating adaptive mcompand samples (threshold tuned to trough)..."
    echo ""

    for presenter in "${PRESENTERS[@]}"; do
        local src_file="${TESTDATA_DIR}/LMP-72-${presenter}.flac"

        if [[ ! -f "$src_file" ]]; then
            continue
        fi

        # Calculate adaptive threshold and scaling based on this presenter's trough
        local trough="${SRC_TROUGH[$presenter]}"
        local adaptive_threshold
        adaptive_threshold=$(calculate_adaptive_threshold "$trough")
        local scaling_desc="voice-protective"

        log_info "${presenter} (6-band mcompand, ${scaling_desc} scaling, threshold ${adaptive_threshold}dB based on trough ${trough}dB)"

        for exp in "${EXPANSION_LEVELS[@]}"; do
            local out_file="${OUTPUT_DIR}/LMP-72-${presenter}-mcompand-adaptive-exp${exp}dB.flac"
            local filter
            filter=$(build_mcompand_filter "$exp" "$adaptive_threshold")
            # Add mono format prefix
            filter="aformat=channel_layouts=mono,${filter}"

            # Add levelling chain if requested
            if [[ "$LEVEL_OUTPUT" == "true" ]]; then
                filter+=$(build_level_chain)
            fi

            printf "  exp %2d dB: " "$exp"

            if ffmpeg -hide_banner -loglevel error -y \
                -i "$src_file" -t "$DURATION" \
                -af "$filter" \
                "$out_file" 2>/dev/null; then
                echo "    ✓"
            else
                echo "    ✗ (ffmpeg error)"
            fi
        done
    done
    echo ""

    # ========================================================================
    # Phase 3: Measure and display results
    # ========================================================================

    # Helper function to measure and print results row
    print_result_row() {
        local presenter="$1"
        local exp="$2"
        local filter_type="$3"  # "compand", "mcompand", "mcompand-adaptive"
        local out_file="${OUTPUT_DIR}/LMP-72-${presenter}-${filter_type}-exp${exp}dB.flac"

        if [[ ! -f "$out_file" ]]; then
            printf "%-12s %4d │ %8s %9s %9s %9s %8s │ %s\n" \
                "$filter_type" "$exp" "N/A" "N/A" "N/A" "N/A" "N/A" "SKIP"
            return
        fi

        # Measure processed file
        local proc_rms proc_trough proc_centroid proc_rolloff proc_flatness
        read -r proc_rms proc_trough proc_centroid proc_rolloff proc_flatness \
            <<< "$(measure_audio "$out_file" "$DURATION")"

        # Calculate deltas
        local d_rms d_trough d_centroid_pct d_rolloff_pct d_flatness
        d_rms=$(awk -v p="$proc_rms" -v s="${SRC_RMS[$presenter]:-0}" 'BEGIN {printf "%.2f", p - s}')
        d_trough=$(awk -v p="$proc_trough" -v s="${SRC_TROUGH[$presenter]:-0}" 'BEGIN {printf "%.1f", p - s}')
        d_centroid_pct=$(awk -v p="$proc_centroid" -v s="${SRC_CENTROID[$presenter]:-1}" 'BEGIN {printf "%.1f", ((p - s) / s) * 100}')
        d_rolloff_pct=$(awk -v p="$proc_rolloff" -v s="${SRC_ROLLOFF[$presenter]:-1}" 'BEGIN {printf "%.1f", ((p - s) / s) * 100}')
        d_flatness=$(awk -v p="$proc_flatness" -v s="${SRC_FLATNESS[$presenter]:-0}" 'BEGIN {printf "%.4f", p - s}')

        # Determine pass/fail
        local status="PASS"
        local status_color="$GREEN"

        if ! check_threshold "$d_rms" "$THRESH_RMS"; then
            status="FAIL"
            status_color="$RED"
        elif ! check_threshold "$d_centroid_pct" "$THRESH_CENTROID"; then
            status="FAIL"
            status_color="$RED"
        elif ! check_threshold "$d_rolloff_pct" "$THRESH_ROLLOFF"; then
            status="FAIL"
            status_color="$RED"
        elif ! check_threshold "$d_flatness" "$THRESH_FLATNESS"; then
            status="FAIL"
            status_color="$RED"
        fi

        # Print row
        printf "%-12s %4d │ " "$filter_type" "$exp"
        format_delta "$d_rms" "$THRESH_RMS" " dB"
        printf " "
        format_delta "$d_trough" "999" " dB"
        printf " "
        format_delta "$d_centroid_pct" "$THRESH_CENTROID" "%"
        printf "  "
        format_delta "$d_rolloff_pct" "$THRESH_ROLLOFF" "%"
        printf "  "
        format_delta "$d_flatness" "$THRESH_FLATNESS"
        printf "  │ ${status_color}%s${NC}\n" "$status"
    }

    log_info "Phase 3: Results"
    echo ""

    for presenter in "${PRESENTERS[@]}"; do
        local trough="${SRC_TROUGH[$presenter]}"
        local adaptive_threshold
        adaptive_threshold=$(calculate_adaptive_threshold "$trough")
        local scaling_desc="voice-protective"

        echo "═══ ${presenter^^} (trough: ${trough}dB → threshold: ${adaptive_threshold}dB, scaling: ${scaling_desc}) ═══"
        printf "%-12s %4s │ %8s %9s %9s %9s %8s │ %s\n" \
            "Filter" "Exp" "Δ RMS" "Δ Trough" "Δ Cent%" "Δ Roll%" "Δ Flat" "Status"
        printf "%-12s %4s │ %8s %9s %9s %9s %8s │ %s\n" \
            "────────────" "────" "────────" "─────────" "─────────" "─────────" "────────" "──────"

        for exp in "${EXPANSION_LEVELS[@]}"; do
            print_result_row "$presenter" "$exp" "compand"
            print_result_row "$presenter" "$exp" "mcompand"
            print_result_row "$presenter" "$exp" "mcompand-adaptive"
        done
        echo ""
    done

    echo ""
    echo "Thresholds: RMS ±${THRESH_RMS}dB | Centroid ±${THRESH_CENTROID}% | Rolloff ±${THRESH_ROLLOFF}% | Flatness ±${THRESH_FLATNESS}"
    echo "Output files: ${OUTPUT_DIR}/"
    echo ""
    log_success "Sweep complete"
}

main "$@"
