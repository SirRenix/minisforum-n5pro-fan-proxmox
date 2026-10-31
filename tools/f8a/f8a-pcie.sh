#!/bin/bash
# f8a-pcie.sh - M2 for the PCIe channel: do the EC curve tables change when
# pwm4 is written, and does the driver's hand-back leave DCR5 at 0xff?
# (run card block F, F1)
#
# Usage: f8a-pcie.sh
#
# Run it inside tmux at the console (see f8a-stress.sh).
#
# Sequence:
#  (a) module unloaded (unloaded here if it was loaded) ->
#      f8a-snapshot.sh pcie-before (mode A: EC RAM 2x, I2EC, TACH)
#  (b) modprobe minisforum_n5_it5571 debug_regs=1; pwm4_enable=1 (driver
#      starts manual mode at 255), pwm4=167 (0xA7); dcr immediately (raw),
#      settled (see below) and after 10 s
#  (c) pwm4_enable=2 (hand-back; driver 0.2.1+sr1~rc2 and later write 255
#      as curve points 0x33-0x39 + 0x30 and send no 0x31, operator decision
#      2 of 2026-10-16); dcr immediately (+0 s, raw) and at +1, +60, +300 s;
#      expected DCR5 = 0xff
#  (d) modprobe -r; f8a-snapshot.sh pcie-after (mode A)
#  (e) diff of before/after (curve tables, I2EC) and the DCR5 time series
#
# Settle (TESTSPEC 0.4 sections 2.1 and 5.3 F1, #21/#22): the EC takes a pwm4 write
# and the hand-back over within about 0.2 s (MW1 Z1, 2026-10-17); a read at
# +0 s may still show the old value. The "+0s raw" readings are logged, never
# judged. Judged are the hand-back points +1 s, +60 s and +300 s (each read
# once, at or after its time): DCR5 must be 0xff. In (b) the "settled" point
# (first poll every 0.2 s at >= 1 s after the write showing the target, else
# the poll at 5 s) is logged only.
#
# What it decides: M2 for PCIe (curve tables byte-identical, TESTSPEC C1)
# and whether the hand-back sets DCR5 to 0xff and keeps it there (decision
# 2; any other value at a judged (c) point is FAIL). It does NOT decide M3, what
# DCR5 does after 0x31: the driver no longer sends 0x31 on pwm4, so that
# question needs the research tools with the module unloaded (F2-F4).
#
# An EC RAM dump directly after the write is not possible: it would need
# the research tools beside the loaded driver (forbidden, checklist C2).
# The after-dump therefore follows the hand-back and the unload.
#
# Only pwm4 is written. Exit (normal, abort, Ctrl-C, SIGTERM, SIGHUP): pwm4
# is never left manual; the module is loaded normally (no debug_regs) only
# if it was loaded at the start, otherwise it stays unloaded.
# Abort: kernel-log hit, temperature limit (Tctl >= 85, NVMe >= 70,
# HDD >= 50 C), a tach of 0 rpm on fan1..3, n5-fangov no longer inactive,
# failed modprobe, write or research tool.
set -Eeuo pipefail

F8A_DIR=$(dirname "$(readlink -f "$0")")
# shellcheck source=f8a-lib.sh
. "$F8A_DIR/f8a-lib.sh"

NAME=f8a-pcie.sh
[ $# -eq 0 ] || { sed -n '6p' "$0" | sed 's/^# //' >&2; exit 2; }

PCIE_VALUE=167
HOLD=$(f8a_t HOLD 10)
# judged points, seconds after the hand-back write (TESTSPEC 0.4 F1: +1 s =
# the settle time of pwm4, +60 s, +300 s); +0 s is logged only
SERIES=$(f8a_t SERIES '1 60 300')
UP_TIMEOUT=$(f8a_t UP 15)
# (b) logged "settled" point: first poll at >= SETTLE_MIN s showing the
# written value, else the poll at SETTLE_TIMEOUT s (not judged)
SETTLE_MIN=$(f8a_t SETTLE_MIN 1)
SETTLE_TIMEOUT=$(f8a_t SETTLE_TIMEOUT 5)
SETTLE_POLL=0.2

f8a_guard_common
f8a_guard_tty
# the pwm writer lock: n5-ectest and n5-fangov (>= 0.5) cannot write beside this script
f8a_writer_lock "$NAME"
f8a_check_limits require || f8a_die "temperature precondition: $F8A_LIMIT_REASON"
[ -r "$F8A_DIR/f8a-snapshot.sh" ] || f8a_die "f8a-snapshot.sh not found next to this script"
f8a_modprobe_opt experimental_write EXPW
f8a_modprobe_opt_true experimental_write || f8a_die "experimental_write is '${EXPW:-unset}' in the effective modprobe configuration (modprobe -c); pwm4 would not be writable"
WAS_LOADED=0
if f8a_module_loaded; then
    WAS_LOADED=1
    HW=$(f8a_hwmon_dir) || f8a_die "module loaded but hwmon device missing"
    # Unloading in (a) sends the automatic command on every channel; a
    # manual channel (e.g. pwm3=140 from the n5-fangov failsafe) would drop
    # to the EC default for the rest of the window.
    for i in 1 2 3 4; do
        f8a_sysfs_read "$HW/pwm${i}_enable" e || e="error-$F8A_ERRNO"
        [ "$e" = 2 ] || f8a_die "pwm${i}_enable is '$e', expected 2 on all channels before the run (unloading would hand a manual channel to the EC default)"
    done
    f8a_check_tachs "$HW" || f8a_die "before the run: $F8A_TACH_REASON"
fi

cat <<EOF
f8a-pcie $F8A_VERSION (M2, PCIe). Module loaded now: $( [ "$WAS_LOADED" = 1 ] && echo yes || echo no )
(a) $( [ "$WAS_LOADED" = 1 ] && echo "modprobe -r $F8A_MODULE, then " )snapshot pcie-before (research tools, module unloaded)
(b) modprobe $F8A_MODULE debug_regs=1; pwm4_enable=1 (255), pwm4=$PCIE_VALUE (0xA7); dcr raw, settled, ${HOLD} s (logged, not judged)
(c) pwm4_enable=2 (hand-back = 255); dcr +0 s raw (logged, not judged); judged DCR5 = 0xff at: $SERIES s
(d) modprobe -r; snapshot pcie-after
(e) diff before/after, DCR5 series
End state: module $( [ "$WAS_LOADED" = 1 ] && echo "loaded normally (modprobe.d options, no debug_regs)" || echo "unloaded" ), pwm4 at 255 (DCR5 0xff; PCIe has no EC automatic).
Duration about $(( HOLD + ${SERIES##* } + 60 )) s. Now: $(f8a_temps_line)
Run this inside tmux. Ctrl-C stops and restores.
EOF
f8a_confirm "$NAME"

START=$EPOCHSECONDS
WORK=$(mktemp -d)
ABORT='' RESULT_HINT='' CLEANED=0 STEP=start BEFORE='' AFTER='' DATA_GAP='' HB_POINTS=0 HB_BAD=''
declare -a SER_T=() SER_V=()

record_dcr() {
    # record_dcr TAG - read dcr, log it, append DCR5 to the series
    local d d5
    if f8a_read_dcr d; then d5=$(f8a_dcr_byte "$d" 7); else d5=n/a; DATA_GAP="${DATA_GAP:+$DATA_GAP; }dcr unreadable at $1"; fi
    SER_T+=("$1")
    SER_V+=("$d5")
    f8a_say "dcr $1: $d  (DCR5=$d5)"
}

now_us() { printf '%s' "${EPOCHREALTIME/[.,]/}"; }

sec_to_us() {
    # sec_to_us SECONDS - "1", "0.5", "1.25" -> microseconds
    local i=${1%%.*} f=''
    [ "$1" = "$i" ] || f=${1#*.}
    f="${f}000000"
    printf '%s' $(( 10#${i:-0} * 1000000 + 10#${f:0:6} ))
}

record_settled() {
    # record_settled TAG TARGET T0_US - poll DCR5 every SETTLE_POLL s; the
    # recorded (judged) point is the first poll at >= SETTLE_MIN s after the
    # write at T0_US that shows TARGET, else the poll at SETTLE_TIMEOUT s
    local tgt=${2,,} min max el d d5
    min=$(sec_to_us "$SETTLE_MIN") max=$(sec_to_us "$SETTLE_TIMEOUT")
    while :; do
        el=$(( $(now_us) - $3 ))
        if [ "$el" -ge "$min" ]; then
            if f8a_read_dcr d; then d5=$(f8a_dcr_byte "$d" 7); else d5=n/a; fi
            if [ "${d5,,}" = "$tgt" ] || [ "$el" -ge "$max" ]; then break; fi
        fi
        sleep "$SETTLE_POLL"
    done
    [ "$d5" != n/a ] || DATA_GAP="${DATA_GAP:+$DATA_GAP; }dcr unreadable at $1 settled"
    [ "${d5,,}" = "$tgt" ] || f8a_say "DCR5 not at $tgt after ${SETTLE_TIMEOUT} s"
    SER_T+=("$1 settled +$((el / 1000000)).$((el / 100000 % 10))s")
    SER_V+=("$d5")
    f8a_say "dcr ${SER_T[-1]}: $d  (DCR5=$d5)"
}

abort() { ABORT=$1; RESULT_HINT=${2:-}; f8a_say "ABORT: $1"; exit 1; }

check_tachs_loaded() {
    # check_tachs_loaded WHERE - fan1..3 > 0 via the driver (module loaded)
    local hw
    hw=$(f8a_hwmon_dir) || abort "hwmon device missing ($1)" FAIL
    f8a_check_tachs "$hw" || abort "$1: $F8A_TACH_REASON" FAIL
}

check_snapshot_tachs() {
    # check_snapshot_tachs FILE - TACH1..3 of a mode-A snapshot must be > 0
    local t
    t=$(grep -E '^TACH[123]: ' "$1" || true)
    if printf '%s\n' "$t" | grep -qE '^TACH[123]: 0 RPM'; then
        abort "tach 0 rpm in ${1##*/}: ${t//$'\n'/; }" FAIL
    fi
    f8a_say "tach (research tool): ${t//$'\n'/; }"
}

check_step() {
    # check_step NAME [loaded] - kernel log, n5-fangov, limits (and tachs
    # via the driver when it is loaded) after a step
    local n lines drv s r
    f8a_klog_count "$START" n lines drv || abort "kernel log unreadable: $lines"
    if [ "$n" != 0 ]; then printf '%s\n' "$lines"; abort "kernel-log hit ($n, driver: $drv) after step $1" "$(if [ "$drv" -gt 0 ]; then echo FAIL; fi)"; fi
    f8a_fangov_ok s || abort "$F8A_FANGOV_UNIT is '$s' after step $1; a second pwm writer invalidates the run"
    if f8a_watch_tripped "$WORK" r; then abort "temperature limit (watcher) in step $1: $r"; fi
    f8a_check_limits require || abort "temperature limit after step $1: $F8A_LIMIT_REASON"
    if [ "${2:-}" = loaded ]; then check_tachs_loaded "step $1"; fi
    f8a_say "step $1 ok: klog_hits=0 $(f8a_temps_line)${2:+ $F8A_TACH_LINE}"
}

wait_checked() {
    # wait_checked SECONDS - sleep in 2 s slices with limit and tach checks
    local left=$1 s r
    while [ "$left" -gt 0 ]; do
        s=2; [ "$left" -ge 2 ] || s=$left
        sleep "$s"
        left=$((left - s))
        if f8a_watch_tripped "$WORK" r; then abort "temperature limit (watcher) during step $STEP: $r"; fi
        f8a_check_limits require || abort "temperature limit during step $STEP: $F8A_LIMIT_REASON"
        check_tachs_loaded "during step $STEP"
    done
}

unload() {
    timeout -k 5 60 modprobe -r "$F8A_MODULE" || abort "modprobe -r failed (rc $?)" FAIL
    ! f8a_module_loaded || abort "module still loaded after modprobe -r" FAIL
}

snapshot() {
    # snapshot LABEL VAR - mode-A snapshot; rc 3 (a research tool failed or
    # a section is incomplete) is a data gap, anything else aborts
    local rc=0 __f
    bash "$F8A_DIR/f8a-snapshot.sh" "$1" --yes --require-mode A --raw-path-file "$WORK/$1" || rc=$?
    __f=$(cat "$WORK/$1" 2>/dev/null || true)
    if [ "$rc" -eq 3 ] && [ -n "$__f" ]; then
        DATA_GAP="${DATA_GAP:+$DATA_GAP; }snapshot $1 incomplete (research tool failed, see its file)"
        f8a_warn "snapshot $1 incomplete"
    elif [ "$rc" -ne 0 ] || [ -z "$__f" ]; then
        abort "snapshot $1 failed (rc $rc)"
    fi
    printf -v "$2" '%s' "$__f"
    check_snapshot_tachs "$__f"
}

restore() {
    local hw e
    f8a_section "restore"
    hw=$(f8a_hwmon_dir || true)
    if [ -n "$hw" ]; then
        f8a_sysfs_read "$hw/pwm4_enable" e || e="error-$F8A_ERRNO"
        if [ "$e" != 2 ]; then
            f8a_say "pwm4_enable=$e -> 2"
            f8a_sysfs_write "$hw/pwm4_enable" 2 || f8a_warn "pwm4_enable=2 failed ($F8A_ERRNO); unloading hands it back"
        fi
    fi
    # never load the driver while a research tool still does port I/O (C2)
    f8a_wait_research_idle 130 || f8a_warn "a research tool is still running after 130 s; not touching the module - check by hand (run card R3)"
    if f8a_research_busy >/dev/null; then
        printf 'final: module loaded=%s (left as is, research tool still running)\n' "$(f8a_module_loaded && echo yes || echo no)"
        return 0
    fi
    if [ "$WAS_LOADED" = 1 ]; then
        if f8a_module_loaded && [ "$(f8a_module_param debug_regs)" = Y ]; then
            timeout -k 5 60 modprobe -r "$F8A_MODULE" || f8a_warn "modprobe -r failed (rc $?)"
        fi
        if ! f8a_module_loaded; then
            f8a_say "modprobe $F8A_MODULE (normal options from modprobe.d)"
            timeout -k 5 60 modprobe "$F8A_MODULE" || f8a_warn "modprobe failed (rc $?)"
        fi
        if f8a_wait_hwmon "$UP_TIMEOUT"; then
            hw=$(f8a_hwmon_dir)
            f8a_sysfs_read "$hw/pwm4_enable" e || e="error-$F8A_ERRNO"
            printf 'final: module loaded, debug_regs=%s, pwm4_enable=%s\n' "$(f8a_module_param debug_regs)" "$e"
        else
            f8a_warn "module not back - check by hand (run card R3)"
        fi
    else
        if f8a_module_loaded; then
            timeout -k 5 60 modprobe -r "$F8A_MODULE" || f8a_warn "modprobe -r failed (rc $?)"
        fi
        printf 'final: module loaded=%s\n' "$(f8a_module_loaded && echo yes || echo no)"
    fi
}

handback_check() {
    # handback_check - sets HB_POINTS (number of judged (c) points) and
    # HB_BAD ("TAG=VALUE, ..." for every judged (c) point with DCR5 neither
    # ff nor n/a); the "+0s raw" reading before the EC took the write over is
    # not judged (TESTSPEC 0.4 F1, #22)
    local i
    HB_POINTS=0 HB_BAD=''
    for i in "${!SER_T[@]}"; do
        case "${SER_T[$i]}" in
            *" raw") ;;
            "c hand-back"*)
                HB_POINTS=$((HB_POINTS + 1))
                case "${SER_V[$i],,}" in
                    ff|n/a) ;;
                    *) HB_BAD="${HB_BAD:+$HB_BAD, }${SER_T[$i]}=${SER_V[$i]}" ;;
                esac
                ;;
        esac
    done
}

summary() {
    local i n lines drv kv drc=0 result
    f8a_section "summary"
    echo "DCR5 (I2EC 0x1807, PCIe) time series:"
    for i in "${!SER_T[@]}"; do printf '  %-30s %s\n' "${SER_T[$i]}" "${SER_V[$i]}"; done
    echo "written value: $PCIE_VALUE = 0xa7; DCR5 before the run: see pcie-before I2EC 0x1807"
    echo "'+0s raw' = read right after the write, before the EC took it over (~0.2 s, MW1 Z1): logged, not judged;"
    echo "judged (TESTSPEC 0.4 F1): DCR5 = ff at c hand-back +$(printf '%s' "$SERIES" | sed 's/ /, +/g') s; b 'settled' logged only"
    handback_check
    if [ "$HB_POINTS" = 0 ]; then
        echo "hand-back check (decision 2: pwm4_enable=2 -> DCR5 ff): not reached"
    elif [ -n "$HB_BAD" ]; then
        echo "hand-back check (decision 2: pwm4_enable=2 -> DCR5 ff): FAIL ($HB_BAD)"
    else
        echo "hand-back check (decision 2: pwm4_enable=2 -> DCR5 ff): ok at $HB_POINTS points (n/a = data gap)"
    fi
    if [ -n "$BEFORE" ] && [ -n "$AFTER" ]; then
        echo "--- f8a-snapshot.sh --diff (before vs after) ---"
        bash "$F8A_DIR/f8a-snapshot.sh" --diff "$BEFORE" "$AFTER" || drc=$?
        [ "$drc" -ne 2 ] || DATA_GAP="${DATA_GAP:+$DATA_GAP; }diff not possible (input error)"
    else
        echo "no before/after pair (aborted before step d)"
    fi
    echo "NOTE: an EC RAM dump directly after the pwm4 write is impossible without running the"
    echo "research tools beside the loaded driver (forbidden, checklist C2). The after-dump follows"
    echo "the hand-back (pwm4_enable=2) and the unload; a curve change that the EC reverts on the"
    echo "hand-back would not be seen."
    echo "NOTE: this run decides M2 (curve tables) and the driver hand-back (DCR5 ff), not M3: from"
    echo "driver 0.2.1+sr1~rc2 on, pwm4 sends no 0x31, so what DCR5 does after 0x31 needs the research"
    echo "tools with the module unloaded and a temperature step or a reboot (run card F2-F4)."
    f8a_klog_count "$START" n lines drv || true
    printf 'kernel-log hits since start: %s (attributed to the driver: %s)\n' "$n" "$drv"
    [ -z "$lines" ] || printf '%s\n' "$lines"
    printf 'max temps: Tctl=%s NVMe=%s HDD=%s\n' "$(f8a_c "$F8A_MAX_TCTL")" "$(f8a_c "$F8A_MAX_NVME")" "$(f8a_c "$F8A_MAX_HDD")"
    [ -z "$ABORT" ] || printf 'abort: %s (step %s)\n' "$ABORT" "$STEP"
    [ -z "$DATA_GAP" ] || printf 'data gap: %s\n' "$DATA_GAP"
    kv=$(f8a_klog_verdict "$n" "$drv")
    # every kernel-log hit is FAIL (TESTSPEC 5a); the kernel-log text names
    # whether it is a driver hit or an unexplained foreign hit
    if [[ "$kv" == FAIL* ]]; then
        result=$kv
        [ -z "$ABORT" ] || result="$result; abort: $ABORT (step $STEP)"
    elif [ "$RESULT_HINT" = FAIL ]; then
        result="FAIL ($ABORT)"
    elif [ -n "$HB_BAD" ]; then
        result="FAIL (hand-back: DCR5 not ff after pwm4_enable=2: $HB_BAD)"
        [ -z "$ABORT" ] || result="$result; abort: $ABORT (step $STEP)"
    elif [ -n "$kv" ]; then
        result=$kv
    elif [ -n "$ABORT" ]; then
        result="INCONCLUSIVE ($ABORT, step $STEP)"
    elif [ -n "$DATA_GAP" ]; then
        result="INCONCLUSIVE ($DATA_GAP)"
    else
        result="COMPLETE (sequence a-e done, data complete, hand-back DCR5 ff; read the C1 line and the DCR5 series above)"
    fi
    printf 'F1 run result: %s\n' "$result"
}

cleanup() {
    local rc=$?
    [ "$CLEANED" = 0 ] || return 0
    CLEANED=1
    f8a_cleanup_signals
    set +e
    f8a_watch_stop "$WORK"
    restore
    summary
    rm -rf "$WORK"
    f8a_log_finish
    if [ "$rc" -eq 0 ] && [ -n "$HB_BAD" ]; then rc=1; fi
    if [ "$rc" -eq 0 ] && [ -n "$DATA_GAP" ]; then rc=3; fi
    exit "$rc"
}

trap cleanup EXIT
trap 'ABORT="interrupted (SIGINT)"; exit 130' INT
trap 'ABORT="terminated (SIGTERM)"; exit 143' TERM
trap 'ABORT="hangup (SIGHUP, terminal or SSH session lost)"; exit 129' HUP

f8a_log_start f8a-pcie m2
f8a_identity
f8a_watch_start "$WORK"

# (a)
STEP=a
f8a_section "(a) module unloaded, snapshot pcie-before"
if f8a_module_loaded; then f8a_say "modprobe -r $F8A_MODULE"; unload; fi
check_step a-unloaded
snapshot pcie-before BEFORE
if [ -n "$DATA_GAP" ]; then abort "before-snapshot incomplete; M2 needs a complete reference"; fi

# (b)
STEP=b
f8a_section "(b) load with debug_regs=1, pwm4 manual $PCIE_VALUE"
timeout -k 5 60 modprobe "$F8A_MODULE" debug_regs=1 || abort "modprobe failed (rc $?)" FAIL
f8a_wait_hwmon "$UP_TIMEOUT" || abort "hwmon device not back" FAIL
HW=$(f8a_hwmon_dir)
f8a_sysfs_read "$HW/pwm4_enable" e || e="error-$F8A_ERRNO"
f8a_say "after load: pwm4_enable=$e"
[ "$e" = 2 ] || abort "pwm4_enable '$e' after load, expected 2" FAIL
check_step b-loaded loaded
record_dcr "b0 after load"
f8a_sysfs_write "$HW/pwm4_enable" 1 || abort "pwm4_enable=1 failed ($F8A_ERRNO)" FAIL
f8a_say "pwm4_enable=1 written (driver sets 255 first)"
f8a_write_pwm "$HW" 4 "$PCIE_VALUE" || abort "pwm4=$PCIE_VALUE failed ($F8A_ERRNO)" FAIL
T0=$(now_us)
f8a_say "pwm4=$PCIE_VALUE written"
record_dcr "b1 write +0s raw"
record_settled "b1 write" "$(printf '%02x' "$PCIE_VALUE")" "$T0"
wait_checked $(( HOLD - ($(now_us) - T0) / 1000000 ))
record_dcr "b2 write +${HOLD}s"
check_step b loaded

# (c)
STEP=c
f8a_section "(c) hand-back pwm4_enable=2"
f8a_sysfs_write "$HW/pwm4_enable" 2 || abort "pwm4_enable=2 failed ($F8A_ERRNO)" FAIL
T0=$(now_us)
f8a_say "pwm4_enable=2 written (hand-back at 255, no 0x31)"
record_dcr "c hand-back +0s raw"
for t in $SERIES; do
    [ "$t" -gt 0 ] || continue
    wait_checked $(( t - ($(now_us) - T0) / 1000000 ))
    record_dcr "c hand-back +${t}s"
done
check_step c loaded

# (d)
STEP=d
f8a_section "(d) unload, snapshot pcie-after"
unload
check_step d-unloaded
snapshot pcie-after AFTER
STEP=e
f8a_say "sequence complete; (e) diff and series in the summary below"
