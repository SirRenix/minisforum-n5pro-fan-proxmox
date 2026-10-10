#!/bin/bash
# f8a-snapshot.sh - read-only state snapshot for the F8a maintenance window
# (run card blocks A/F, M1/T6).
#
# Usage:
#   f8a-snapshot.sh LABEL [--yes] [--require-mode A|B] [--raw-path-file FILE]
#   f8a-snapshot.sh --diff FILE_A FILE_B
#
# Mode A (driver module NOT loaded): EC RAM 0x00-0xFF twice (ec_probe dump),
#   I2EC 0x1800-0x182F (n5_i2ec_read 0x1800 48), TACH1-3 (n5_fan rpm).
#   These research tools do userspace port I/O; they are run only while the
#   module is unloaded and n5-fangov is stopped (checklist C2).
# Mode B (module loaded): never runs a research tool. hwmon values (pwm and
#   pwm_enable are the driver's cache, not the EC state) and the debugfs dcr
#   file (I2EC 0x1800-0x180F) if the module was loaded with debug_regs=1.
# Both modes: identity (date, uptime, boot_id, kernel, DMI, lsmod, ioports,
#   module parameters, independent temperatures, n5-fangov state).
#
# Writes nothing to /sys and nothing to the EC. Output:
#   /root/n5-mw1/raw/f8a-snapshot-<label>-<UTC>.txt plus .sha256
# Exit: 0 = complete; 3 = mode A incomplete (a research tool failed, or a
# dump/I2EC/TACH section is missing or short; the file is still written);
# 1 = refused or aborted; 2 = usage.
#
# --diff prints the EC RAM bytes that differ between the first dumps of two
# mode-A files (temperature bytes 0x04 0x05 0x06 0x09 excluded), the state
# of the curve-table regions 0x10-0x27, 0x30-0x38, 0x70-0x81 (TESTSPEC C1)
# and changed I2EC bytes. Exit 0 = curve tables identical, 1 = changed,
# 2 = input error. --diff needs no root and touches nothing.
set -Eeuo pipefail

F8A_DIR=$(dirname "$(readlink -f "$0")")
# shellcheck source=f8a-lib.sh
. "$F8A_DIR/f8a-lib.sh"

F8A_TEMP_OFFSETS=' 4 5 6 9 '
F8A_CURVE_REGIONS='16:40 48:57 112:130'   # 0x10-0x27 0x30-0x38 0x70-0x81, end exclusive

usage() {
    sed -n '4,6p' "$0" | sed 's/^# //' >&2
    exit 2
}

# ---- parsing --------------------------------------------------------------

extract_rows() {
    # extract_rows FILE BEGIN_MARKER END_MARKER ADDR_HEX_DIGITS
    # prints "<offset_dec> <byte>" for every byte of the hex rows in the
    # section ("00: 00 0E ..." or "1800: 00 ff ...")
    local re='^'
    local i
    for ((i = 0; i < $4; i++)); do re="${re}[0-9A-Fa-f]"; done
    re="$re:\$"
    awk -v b="$2" -v e="$3" -v re="$re" -v al="$4" '
        function hex(s,   i, v, c) {
            v = 0; s = tolower(s)
            for (i = 1; i <= length(s); i++) {
                c = index("0123456789abcdef", substr(s, i, 1))
                if (!c) return -1
                v = v * 16 + c - 1
            }
            return v
        }
        $0 == b { on = 1; next }
        $0 == e { on = 0; next }
        on && $1 ~ re {
            base = hex(substr($1, 1, al))
            for (i = 2; i <= NF; i++) print base + i - 2, tolower($i)
        }' "$1"
}

declare -a RA=() RB=()

load_ecram() {
    # load_ecram FILE N ARRAYNAME - returns 1 if the dump is missing/short
    local -n __arr=$3
    local off val n=0
    __arr=()
    while read -r off val; do
        if [ "$off" -ge 0 ] && [ "$off" -le 255 ]; then __arr[off]=$val; n=$((n + 1)); fi
    done < <(extract_rows "$1" "=== BEGIN EC_RAM $2 ===" "=== END EC_RAM $2 ===" 2)
    [ "$n" -ge 256 ]
}

hx() { printf '0x%02x' "$1"; }

compare_ecram() {
    # compare_ecram - compares RA and RB; prints changes; sets CURVE_CHANGED
    local o n_other=0 reg lo hi changed
    CURVE_CHANGED=0
    echo "EC RAM bytes changed (temperature offsets 0x04 0x05 0x06 0x09 excluded):"
    for ((o = 0; o < 256; o++)); do
        case "$F8A_TEMP_OFFSETS" in *" $o "*) continue ;; esac
        if [ "${RA[o]}" != "${RB[o]}" ]; then
            printf '  %s: %s -> %s\n' "$(hx "$o")" "${RA[o]}" "${RB[o]}"
            n_other=$((n_other + 1))
        fi
    done
    [ "$n_other" -gt 0 ] || echo "  (none)"
    echo "curve-table regions (TESTSPEC C1):"
    for reg in $F8A_CURVE_REGIONS; do
        lo=${reg%%:*}; hi=${reg##*:}
        changed=
        for ((o = lo; o < hi; o++)); do
            if [ "${RA[o]}" != "${RB[o]}" ]; then changed="$changed $(hx "$o")"; fi
        done
        if [ -z "$changed" ]; then
            printf '  %s-%s: identical\n' "$(hx "$lo")" "$(hx $((hi - 1)))"
        else
            printf '  %s-%s: CHANGED at%s\n' "$(hx "$lo")" "$(hx $((hi - 1)))" "$changed"
            CURVE_CHANGED=1
        fi
    done
    if [[ " ${RA[*]} ${RB[*]} " == *'??'* ]]; then
        echo "  NOTE: a dump contains '??' (EC read timeout); comparison of those bytes is meaningless"
    fi
}

do_diff() {
    local a=$1 b=$2 off val
    local -A ia=() ib=()
    [ -r "$a" ] || { echo "cannot read $a" >&2; exit 2; }
    [ -r "$b" ] || { echo "cannot read $b" >&2; exit 2; }
    load_ecram "$a" 1 RA || { echo "no complete EC RAM dump 1 in $a (mode-B file?)" >&2; exit 2; }
    load_ecram "$b" 1 RB || { echo "no complete EC RAM dump 1 in $b (mode-B file?)" >&2; exit 2; }
    echo "diff A=${a##*/}"
    echo "     B=${b##*/}"
    compare_ecram
    echo "I2EC 0x1800-0x182F bytes changed (0x1803 DCR1 CPU, 0x1804 DCR2 SSD, 0x1805/0x1806 DCR3/4 HDD, 0x1807 DCR5 PCIe):"
    while read -r off val; do ia[$off]=$val; done < <(extract_rows "$a" "=== BEGIN I2EC ===" "=== END I2EC ===" 4)
    while read -r off val; do ib[$off]=$val; done < <(extract_rows "$b" "=== BEGIN I2EC ===" "=== END I2EC ===" 4)
    if [ "${#ia[@]}" -eq 0 ] || [ "${#ib[@]}" -eq 0 ]; then
        echo "  (I2EC section missing in one file)"
    else
        local n=0
        for off in $(printf '%s\n' "${!ia[@]}" | sort -n); do
            if [ "${ia[$off]}" != "${ib[$off]:-}" ]; then
                printf '  0x%04x: %s -> %s\n' "$off" "${ia[$off]}" "${ib[$off]:-missing}"
                n=$((n + 1))
            fi
        done
        [ "$n" -gt 0 ] || echo "  (none)"
    fi
    if [ "$CURVE_CHANGED" = 1 ]; then
        echo "C1 curve tables 0x10-0x27, 0x30-0x38, 0x70-0x81: CHANGED"
        exit 1
    fi
    echo "C1 curve tables 0x10-0x27, 0x30-0x38, 0x70-0x81: identical"
    exit 0
}

# ---- argument parsing -----------------------------------------------------

LABEL='' YES=0 REQ_MODE='' PATH_FILE=''
[ $# -ge 1 ] || usage
if [ "$1" = --diff ]; then
    [ $# -eq 3 ] || usage
    do_diff "$2" "$3"
fi
while [ $# -gt 0 ]; do
    case "$1" in
        --yes) YES=1 ;;
        --require-mode) shift; REQ_MODE=${1:-} ;;
        --raw-path-file) shift; PATH_FILE=${1:-} ;;
        -*) usage ;;
        *) [ -z "$LABEL" ] || usage; LABEL=$1 ;;
    esac
    shift
done
case "$LABEL" in ''|*[!A-Za-z0-9._-]*) echo "label must be [A-Za-z0-9._-]+" >&2; exit 2 ;; esac
case "$REQ_MODE" in ''|A|B) ;; *) usage ;; esac

f8a_guard_common
if [ "$YES" != 1 ]; then f8a_guard_tty; fi

if f8a_module_loaded || f8a_hwmon_dir >/dev/null; then MODE=B; else MODE=A; fi
if [ -n "$REQ_MODE" ] && [ "$REQ_MODE" != "$MODE" ]; then
    f8a_die "mode $REQ_MODE required, but the module state gives mode $MODE (module loaded: $(f8a_module_loaded && echo yes || echo no))"
fi

WORK=''
INCOMPLETE=''
finish() {
    local rc=$?
    f8a_cleanup_signals
    set +e
    [ -z "$WORK" ] || rm -rf "$WORK"
    if [ -n "$INCOMPLETE" ]; then
        echo "RESULT: INCOMPLETE ($INCOMPLETE)"
        [ "$rc" -ne 0 ] || rc=3
    elif [ "$rc" -eq 0 ]; then
        echo "RESULT: complete"
    fi
    f8a_log_finish
    if [ -n "$PATH_FILE" ] && [ -n "$F8A_LOG" ]; then printf '%s\n' "$F8A_LOG" > "$PATH_FILE"; fi
    exit "$rc"
}
trap finish EXIT
# A trapped signal waits for the running research tool to finish (bash
# runs the trap after the foreground command), so the caller never loads
# the driver while a tool is still doing port I/O.
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'exit 129' HUP
f8a_log_start f8a-snapshot "$LABEL"
f8a_say "f8a-snapshot $F8A_VERSION label=$LABEL mode=$MODE"
if [ "$MODE" = A ]; then
    echo "mode A: driver module not loaded; research tools (userspace port I/O) are used"
else
    echo "mode B: driver module loaded; research tools are NOT used (checklist C2)"
fi
f8a_identity

gap() { INCOMPLETE="${INCOMPLETE:+$INCOMPLETE; }$*"; }

if [ "$MODE" = A ]; then
    WORK=$(mktemp -d)
    # Each tool's output goes to a work file first (parsed below) and is
    # then copied into the log unchanged, between the section markers.
    for n in 1 2; do
        {
            echo "=== BEGIN EC_RAM $n ==="
            f8a_research ec_probe dump || gap "ec_probe dump $n failed"
            echo "=== END EC_RAM $n ==="
        } > "$WORK/ecram$n"
        echo
        cat "$WORK/ecram$n"
    done
    echo
    {
        echo "=== BEGIN I2EC ==="
        f8a_research n5_i2ec_read 0x1800 48 || gap "n5_i2ec_read failed"
        echo "=== END I2EC ==="
    } > "$WORK/i2ec"
    cat "$WORK/i2ec"
    n_i2ec=$(extract_rows "$WORK/i2ec" "=== BEGIN I2EC ===" "=== END I2EC ===" 4 | wc -l)
    [ "$n_i2ec" -ge 48 ] || gap "I2EC section has $n_i2ec of 48 bytes"
    f8a_section "TACH (n5_fan rpm)"
    f8a_research n5_fan rpm > "$WORK/tach" || gap "n5_fan rpm failed"
    cat "$WORK/tach"
    [ "$(grep -cE '^TACH[123]: [0-9]+ RPM' "$WORK/tach")" = 3 ] || gap "TACH1..3 not all present"
    f8a_section "EC RAM dump 1 vs dump 2 (same snapshot)"
    if load_ecram "$WORK/ecram1" 1 RA && load_ecram "$WORK/ecram2" 2 RB; then
        compare_ecram
        printf 'EC temperatures from dump 1: CPU(0x09)=%d System(0x04)=%d Board(0x05)=%d Ambient(0x06)=%d\n' \
            "0x${RA[9]}" "0x${RA[4]}" "0x${RA[5]}" "0x${RA[6]}" 2>/dev/null || echo "EC temperatures: not decodable"
    else
        echo "dump 1 or dump 2 incomplete; no comparison"
        gap "EC RAM dump 1 or 2 incomplete"
    fi
else
    hw=$(f8a_hwmon_dir || true)
    f8a_section "hwmon $F8A_HWMON_NAME (${hw:-missing})"
    if [ -n "$hw" ]; then
        for i in 1 2 3 4; do
            f8a_rd "$hw/fan${i}_label" lab
            if f8a_sysfs_read "$hw/fan${i}_input" v; then :; else v="error ${F8A_ERRNO}"; fi
            printf 'fan%s_input=%s (%s)\n' "$i" "$v" "${lab:-no label}"
        done
        for i in 1 2 3 4; do
            f8a_rd "$hw/temp${i}_label" lab
            if f8a_sysfs_read "$hw/temp${i}_input" v; then :; else v="error ${F8A_ERRNO}"; fi
            printf 'temp%s_input=%s (%s)\n' "$i" "$v" "${lab:-no label}"
        done
        for i in 1 2 3 4; do
            if [ -e "$hw/pwm$i" ]; then
                if f8a_sysfs_read "$hw/pwm$i" v; then :; else v="error ${F8A_ERRNO}"; fi
                if f8a_sysfs_read "$hw/pwm${i}_enable" e; then :; else e="error ${F8A_ERRNO}"; fi
                printf 'pwm%s=%s pwm%s_enable=%s (driver cache, not EC state)\n' "$i" "$v" "$i" "$e"
            else
                printf 'pwm%s: not visible (read-only driver profile)\n' "$i"
            fi
        done
    fi
    f8a_section "dcr (debugfs, I2EC 0x1800-0x180F)"
    dcr=''
    if f8a_read_dcr dcr; then
        echo "$dcr"
        echo "DCR1(CPU)=$(f8a_dcr_byte "$dcr" 3) DCR2(SSD)=$(f8a_dcr_byte "$dcr" 4) DCR3/4(HDD)=$(f8a_dcr_byte "$dcr" 5)/$(f8a_dcr_byte "$dcr" 6) DCR5(PCIe)=$(f8a_dcr_byte "$dcr" 7)"
    else
        echo "$dcr"
    fi
fi

f8a_section "independent temperatures (end)"
f8a_read_temps
f8a_temps_line; echo
f8a_say "snapshot done"
