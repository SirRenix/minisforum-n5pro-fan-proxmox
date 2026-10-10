#!/bin/bash
# f8a-pcie.sh - M2 for the PCIe channel: do the EC curve tables change when
# pwm4 is written, and what does DCR5 show after the hand-back?
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
#      starts manual mode at 255), pwm4=167 (0xA7); dcr immediately and
#      after 10 s
#  (c) pwm4_enable=2 (hand-back, PMC2 0x31); dcr after 0, 60, 300 s
#  (d) modprobe -r; f8a-snapshot.sh pcie-after (mode A)
#  (e) diff of before/after (curve tables, I2EC) and the DCR5 time series
#
# What it decides: M2 for PCIe (curve tables byte-identical, TESTSPEC C1)
# and whether DCR5 stays at 0xA7 after 0x31. It does NOT decide M3: DCR5 =
# 0xA7 after 0x31 is predicted both by "0x31 has no effect" and by "the
# internal curve now outputs the written value"; separating them needs a
# temperature step across a curve threshold or a reboot (F2-F4).
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
SERIES=$(f8a_t SERIES '0 60 300')
UP_TIMEOUT=$(f8a_t UP 15)

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
(b) modprobe $F8A_MODULE debug_regs=1; pwm4_enable=1 (255), pwm4=$PCIE_VALUE (0xA7); dcr at 0 and ${HOLD} s
(c) pwm4_enable=2; dcr at: $SERIES s
(d) modprobe -r; snapshot pcie-after
(e) diff before/after, DCR5 series
End state: module $( [ "$WAS_LOADED" = 1 ] && echo "loaded normally (modprobe.d options, no debug_regs)" || echo "unloaded" ), pwm4 EC automatic.
Duration about $(( HOLD + ${SERIES##* } + 60 )) s. Now: $(f8a_temps_line)
Run this inside tmux. Ctrl-C stops and restores.
EOF
f8a_confirm "$NAME"

START=$EPOCHSECONDS
WORK=$(mktemp -d)
ABORT='' RESULT_HINT='' CLEANED=0 STEP=start BEFORE='' AFTER='' DATA_GAP=''
declare -a SER_T=() SER_V=()

record_dcr() {
    # record_dcr TAG - read dcr, log it, append DCR5 to the series
    local d d5
    if f8a_read_dcr d; then d5=$(f8a_dcr_byte "$d" 7); else d5=n/a; DATA_GAP="${DATA_GAP:+$DATA_GAP; }dcr unreadable at $1"; fi
    SER_T+=("$1")
    SER_V+=("$d5")
    f8a_say "dcr $1: $d  (DCR5=$d5)"
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
    if [ "$n" != 0 ]; then printf '%s\n' "$lines"; abort "kernel-log hit ($n, driver: $drv) after step $1" "$( [ "$drv" -gt 0 ] && echo FAIL || true)"; fi
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

summary() {
    local i n lines drv kv drc=0 result
    f8a_section "summary"
    echo "DCR5 (I2EC 0x1807, PCIe) time series:"
    for i in "${!SER_T[@]}"; do printf '  %-24s %s\n' "${SER_T[$i]}" "${SER_V[$i]}"; done
    echo "written value: $PCIE_VALUE = 0xa7; EC auto value before the run: see pcie-before I2EC 0x1807"
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
    echo "NOTE: this run decides M2 (curve tables, DCR5 after 0x31), not M3: DCR5 = 0xa7 after 0x31"
    echo "fits both \"0x31 has no effect\" and \"internal curve = written value\"; M3 needs a"
    echo "temperature step across a curve threshold or a reboot (run card F2-F4)."
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
    elif [ -n "$kv" ]; then
        result=$kv
    elif [ -n "$ABORT" ]; then
        result="INCONCLUSIVE ($ABORT, step $STEP)"
    elif [ -n "$DATA_GAP" ]; then
        result="INCONCLUSIVE ($DATA_GAP)"
    else
        result="COMPLETE (sequence a-e done, data complete; read the C1 line and the DCR5 series above)"
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
f8a_say "pwm4=$PCIE_VALUE written"
record_dcr "b1 write +0s"
wait_checked "$HOLD"
record_dcr "b2 write +${HOLD}s"
check_step b loaded

# (c)
STEP=c
f8a_section "(c) hand-back pwm4_enable=2"
f8a_sysfs_write "$HW/pwm4_enable" 2 || abort "pwm4_enable=2 failed ($F8A_ERRNO)" FAIL
f8a_say "pwm4_enable=2 written (PMC2 0x31)"
prev=0
for t in $SERIES; do
    wait_checked $((t - prev))
    prev=$t
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
