#!/bin/bash
# f8a-stress.sh - F8a driver teardown stress (run card block E, E1/E2).
#
# Usage: f8a-stress.sh load|unbind [--cycles N]     (default 50 cycles)
#
#   load    each cycle: modprobe -r, then modprobe minisforum_n5_it5571 debug_regs=1
#   unbind  each cycle: write the device name to the platform driver's
#           unbind file, then to its bind file
#
# Run it inside tmux at the console: a lost SSH session or terminal sends
# SIGHUP; the script then restores and logs, but you lose the live output.
#
# Preconditions (refused otherwise): root, DMI N5 PRO/F8NAA/vendor, n5-fangov
# inactive or failed, interactive terminal, module loaded with debug_regs=1
# and experimental_write=1, experimental_write=1 effective in "modprobe -c"
# (also after overrides), pwm1..4_enable=2, all three independent
# temperature sources present and below the limits.
#
# While cycling, a reader loop reads every fan*_input/temp*_input of the
# driver's hwmon device (resolved by name each time) and a writer loop sets
# pwm1 and pwm2 (only these) to manual and cycles 255,200,150,100. The
# writer keeps running through every teardown; ENODEV/EBUSY/ENOENT there
# are expected and counted per errno. The writer is held only while the
# driver comes back up, so the state recorded right after each reload/rebind
# is the fresh probe state: pwm1..4_enable must read 2 and fan1..fan3 > 0.
# A watcher checks the temperature limits every second, also while the
# script waits in modprobe/unbind.
#
# After every cycle: pwm1..4_enable, fan1..3_input, dcr, kernel-log hits
# since the start, n5-fangov state. Abort on any kernel-log hit, wrong
# enable state, a tach of 0 rpm, failed modprobe/bind, n5-fangov no longer
# inactive, or a temperature limit (Tctl >= 85, NVMe >= 70, HDD >= 50 C).
# Exit (normal, abort, Ctrl-C, SIGTERM, SIGHUP): loops stopped, module loaded
# (debug_regs=1) and bound, pwm1_enable=pwm2_enable=2, summary, .sha256.
# SIGKILL cannot be handled: the loops then end on their own within a
# second, the fans stay as last written (check by hand, run card R3).
#
# The teardown race window is exercised, not proven absent.
set -Eeuo pipefail

F8A_DIR=$(dirname "$(readlink -f "$0")")
# shellcheck source=f8a-lib.sh
. "$F8A_DIR/f8a-lib.sh"

NAME=f8a-stress.sh
usage() { sed -n '4,9p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }

MODE='' CYCLES=50
while [ $# -gt 0 ]; do
    case "$1" in
        load|unbind) [ -z "$MODE" ] || usage; MODE=$1 ;;
        --cycles) shift; CYCLES=${1:-} ;;
        *) usage ;;
    esac
    shift
done
[ -n "$MODE" ] || usage
case "$CYCLES" in ''|*[!0-9]*) usage ;; esac
if [ "$CYCLES" -lt 1 ] || [ "$CYCLES" -gt 500 ]; then usage; fi

CYCLE_PAUSE=$(f8a_t CYCLE 2)        # writer/reader time per cycle (s)
UP_TIMEOUT=$(f8a_t UP 15)           # wait for hwmon after reload (s)

# ---- preconditions --------------------------------------------------------

f8a_guard_common
f8a_guard_tty
# the pwm writer lock: n5-ectest and n5-fangov (>= 0.5) cannot write beside this script
f8a_writer_lock "$NAME"
f8a_module_loaded || f8a_die "$F8A_MODULE is not loaded; load it with: modprobe $F8A_MODULE debug_regs=1"
[ "$(f8a_module_param debug_regs)" = Y ] || f8a_die "module not loaded with debug_regs=1 (modprobe -r $F8A_MODULE; modprobe $F8A_MODULE debug_regs=1)"
[ "$(f8a_module_param experimental_write)" = Y ] || f8a_die "module loaded without experimental_write=1"
f8a_modprobe_opt experimental_write EXPW
f8a_modprobe_opt_true experimental_write || f8a_die "experimental_write is '${EXPW:-unset}' in the effective modprobe configuration (modprobe -c); a reload would come up read-only"
HW=$(f8a_hwmon_dir) || f8a_die "hwmon device $F8A_HWMON_NAME not found"
[ -e "$HW/pwm1" ] && [ -e "$HW/pwm2" ] || f8a_die "pwm1/pwm2 not visible (driver read-only?)"
DEV=$(f8a_platform_device) || f8a_die "cannot determine the platform device bound to $F8A_MODULE (expected exactly one in $F8A_DRIVER_DIR that also exists in /sys/bus/platform/devices)"
f8a_read_dcr DCR0 || f8a_die "$DCR0"
f8a_check_limits require || f8a_die "temperature precondition: $F8A_LIMIT_REASON"
for i in 1 2 3 4; do
    f8a_sysfs_read "$HW/pwm${i}_enable" e || e="error-$F8A_ERRNO"
    [ "$e" = 2 ] || f8a_die "pwm${i}_enable is '$e', expected 2 (EC automatic) before the stress run"
done
f8a_check_tachs "$HW" || f8a_die "before the stress run: $F8A_TACH_REASON"

cat <<EOF
f8a-stress $F8A_VERSION, mode $MODE, $CYCLES cycles, device $DEV
Per cycle: $( [ "$MODE" = load ] && echo "modprobe -r $F8A_MODULE; modprobe $F8A_MODULE debug_regs=1" || echo "echo $DEV > $F8A_DRIVER_DIR/unbind; echo $DEV > $F8A_DRIVER_DIR/bind" )
Parallel: reader (fan*_input, temp*_input), writer (pwm1/pwm2 manual, 255/200/150/100; pwm3/pwm4 untouched),
          limit watcher (1 s).
Abort: kernel-log hit, pwmN_enable != 2 or fan1..3 = 0 rpm after reload, failed reload,
       n5-fangov started, Tctl>=85 NVMe>=70 HDD>=50 C.
Now: $(f8a_temps_line), $F8A_TACH_LINE, dcr: $DCR0
Run this inside tmux. Ctrl-C stops and restores.
EOF
f8a_confirm "$NAME"

# ---- run --------------------------------------------------------------

START=$EPOCHSECONDS
WORK=$(mktemp -d)
MAIN_PID=$$
READER_PID='' WRITER_PID=''
DONE=0 ABORT='' RESULT_HINT='' STATE_BAD=0 KHITS=0 KDRV=0 KLINES='' CLEANED=0

reader_loop() {
    trap - EXIT INT TERM ERR
    set +e
    local d f v
    while [ ! -e "$WORK/stop" ]; do
        # main script gone (SIGKILL): end instead of running as an orphan
        kill -0 "$MAIN_PID" 2>/dev/null || exit 0
        d=$(f8a_hwmon_dir) || { sleep 0.05; continue; }
        for f in "$d"/fan*_input "$d"/temp*_input; do
            [ -e "$f" ] || [ -e "$f.errno" ] || continue
            if f8a_sysfs_read "$f" v; then
                echo ok >> "$WORK/reader.ok"
            else
                echo "$F8A_ERRNO ${f##*/}" >> "$WORK/reader.err"
            fi
        done
        sleep 0.05
    done
}

writer_loop() {
    trap - EXIT INT TERM ERR
    set +e
    local d ch i=0 vals=(255 200 150 100)
    while [ ! -e "$WORK/stop" ]; do
        # main script gone (SIGKILL): never keep writing as an orphan
        kill -0 "$MAIN_PID" 2>/dev/null || exit 0
        if [ -e "$WORK/pause" ]; then
            : > "$WORK/paused"
            sleep 0.05
            continue
        fi
        d=$(f8a_hwmon_dir) || { echo "no-hwmon" >> "$WORK/writer.nohwmon"; sleep 0.05; continue; }
        for ch in 1 2; do
            if f8a_sysfs_write "$d/pwm${ch}_enable" 1; then
                echo ok >> "$WORK/writer.ok"
            else
                echo "$F8A_ERRNO pwm${ch}_enable" >> "$WORK/writer.err"
            fi
            if f8a_write_pwm "$d" "$ch" "${vals[i % 4]}"; then
                echo ok >> "$WORK/writer.ok"
            else
                echo "$F8A_ERRNO pwm$ch" >> "$WORK/writer.err"
            fi
        done
        i=$((i + 1))
        sleep 0.1
    done
}

hold_writer() {
    # hold_writer - returns when the writer is idle at the top of its loop
    local i
    rm -f "$WORK/paused"
    : > "$WORK/pause"
    for ((i = 0; i < 100; i++)); do
        [ -e "$WORK/paused" ] && return 0
        sleep 0.05
    done
    return 1
}

release_writer() { rm -f "$WORK/pause" "$WORK/paused"; }

count_by_errno() {
    # count_by_errno FILE - "ENODEV=12 ENOENT=3" (by errno, all attributes)
    [ -s "$1" ] || { printf 'none'; return 0; }
    awk '{ c[$1]++ } END { for (k in c) printf "%s=%d ", k, c[k] }' "$1" | sed 's/ $//'
}

unexpected_errnos() {
    # unexpected_errnos FILE TOLERATED... - lines whose errno is not tolerated
    local f=$1
    shift
    [ -s "$f" ] || return 0
    awk -v tol=" $* " 'index(tol, " " $1 " ") == 0' "$f"
}

stop_loops() {
    : > "$WORK/stop"
    release_writer
    local p
    for p in $READER_PID $WRITER_PID; do
        [ -n "$p" ] || continue
        for _ in 1 2 3 4 5 6 7 8 9 10; do kill -0 "$p" 2>/dev/null || break; sleep 0.1; done
        kill "$p" 2>/dev/null || true
        wait "$p" 2>/dev/null || true
    done
    READER_PID='' WRITER_PID=''
    f8a_watch_stop "$WORK"
}

restore() {
    local i ch e ok d
    echo
    f8a_section "restore"
    if ! f8a_module_loaded; then
        f8a_say "module not loaded -> modprobe $F8A_MODULE debug_regs=1"
        timeout -k 5 60 modprobe "$F8A_MODULE" debug_regs=1 || f8a_warn "modprobe failed (rc $?)"
    fi
    if f8a_module_loaded && ! f8a_device_bound "$DEV"; then
        f8a_say "device $DEV not bound -> bind"
        f8a_sysfs_write "$F8A_DRIVER_DIR/bind" "$DEV" || f8a_warn "bind failed: $F8A_ERRNO"
    fi
    if f8a_wait_hwmon "$UP_TIMEOUT"; then
        HW=$(f8a_hwmon_dir)
        for ch in 1 2; do
            ok=0
            for i in 1 2 3; do
                if f8a_sysfs_write "$HW/pwm${ch}_enable" 2; then ok=1; break; fi
                f8a_warn "pwm${ch}_enable=2 failed ($F8A_ERRNO), retry $i"
                sleep 0.5
            done
            [ "$ok" = 1 ] || f8a_warn "pwm${ch} NOT handed back to EC automatic"
        done
        for i in 1 2 3 4; do
            f8a_sysfs_read "$HW/pwm${i}_enable" e || e="error-$F8A_ERRNO"
            printf 'final pwm%s_enable=%s\n' "$i" "$e"
        done
        d=''
        f8a_read_dcr d || true
        printf 'final dcr: %s\n' "$d"
        f8a_check_tachs "$HW" || f8a_warn "after restore: $F8A_TACH_REASON (run card R3)"
        printf 'final tach: %s\n' "$F8A_TACH_LINE"
    else
        f8a_warn "hwmon device not back; pwm1/pwm2 could not be set to 2 - check by hand (run card R3)"
    fi
}

summary() {
    local rerr werr unexp_r unexp_w verdict reasons='' kv
    f8a_klog_count "$START" KHITS KLINES KDRV || true
    werr=$(count_by_errno "$WORK/writer.err")
    rerr=$(count_by_errno "$WORK/reader.err")
    unexp_w=$(unexpected_errnos "$WORK/writer.err" ENODEV EBUSY ENOENT | sort | uniq -c | tr -s ' ' | tr '\n' ';' || true)
    unexp_r=$(unexpected_errnos "$WORK/reader.err" ENODEV EBUSY ENOENT EAGAIN | sort | uniq -c | tr -s ' ' | tr '\n' ';' || true)
    f8a_section "summary"
    printf 'mode: %s  device: %s\n' "$MODE" "$DEV"
    printf 'cycles done: %s/%s\n' "$DONE" "$CYCLES"
    printf 'writer ok: %s  errors by errno: %s\n' "$( [ -s "$WORK/writer.ok" ] && wc -l < "$WORK/writer.ok" || echo 0)" "$werr"
    printf 'writer rounds without hwmon dir: %s\n' "$( [ -s "$WORK/writer.nohwmon" ] && wc -l < "$WORK/writer.nohwmon" || echo 0)"
    printf 'reader ok: %s  errors by errno: %s\n' "$( [ -s "$WORK/reader.ok" ] && wc -l < "$WORK/reader.ok" || echo 0)" "$rerr"
    [ -z "$unexp_w" ] || printf 'writer UNEXPECTED errno: %s\n' "$unexp_w"
    [ -z "$unexp_r" ] || printf 'reader UNEXPECTED errno: %s\n' "$unexp_r"
    printf 'kernel-log hits: %s (attributed to the driver: %s) in %s cycles (pattern: %s)\n' "$KHITS" "$KDRV" "$DONE" "$F8A_KLOG_PATTERN"
    [ -z "$KLINES" ] || printf '%s\n' "$KLINES"
    printf 'max temps: Tctl=%s NVMe=%s HDD=%s\n' "$(f8a_c "$F8A_MAX_TCTL")" "$(f8a_c "$F8A_MAX_NVME")" "$(f8a_c "$F8A_MAX_HDD")"
    [ -z "$ABORT" ] || printf 'abort: %s\n' "$ABORT"
    # Step result per TESTSPEC 5a F8a (this step only). Every kernel-log hit
    # is FAIL (driver hit, or a foreign hit until annotated with evidence).
    kv=$(f8a_klog_verdict "$KHITS" "$KDRV")
    case "$kv" in FAIL*) reasons=${kv#FAIL (}; reasons=${reasons%)} ;; esac
    if [ "$STATE_BAD" != 0 ]; then reasons="${reasons:+$reasons, }pwm_enable not 2 after reload"; fi
    if [ -n "$unexp_w$unexp_r" ]; then reasons="${reasons:+$reasons, }unexpected errno"; fi
    if [ "$RESULT_HINT" = FAIL ]; then reasons="${reasons:+$reasons, }$ABORT"; fi
    if [ -n "$reasons" ]; then
        verdict="FAIL ($reasons)"
    elif [ -n "$kv" ]; then
        verdict="$kv; $DONE of $CYCLES cycles completed"
    elif [ "$DONE" -lt "$CYCLES" ]; then
        verdict="INCONCLUSIVE (${ABORT:-stopped}; $DONE of $CYCLES cycles completed)"
    else
        verdict="PASS ($DONE cycles, 0 kernel-log hits, pwm1..4_enable=2 and fan1..3 > 0 rpm after every reload)"
    fi
    printf 'F8a step result (%s): %s\n' "$MODE" "$verdict"
    echo "NOTE: the teardown race window is exercised, not proven absent."
}

cleanup() {
    local rc=$?
    [ "$CLEANED" = 0 ] || return 0
    CLEANED=1
    f8a_cleanup_signals
    set +e
    stop_loops
    restore
    summary
    rm -rf "$WORK"
    f8a_log_finish
    exit "$rc"
}

trap cleanup EXIT
trap 'ABORT="interrupted (SIGINT)"; exit 130' INT
trap 'ABORT="terminated (SIGTERM)"; exit 143' TERM
trap 'ABORT="hangup (SIGHUP, terminal or SSH session lost)"; exit 129' HUP

f8a_log_start f8a-stress "$MODE"
f8a_identity
f8a_section "stress $MODE, $CYCLES cycles, device $DEV"
f8a_say "start: dcr $DCR0, $F8A_TACH_LINE"
reader_loop &
READER_PID=$!
writer_loop &
WRITER_PID=$!
f8a_watch_start "$WORK"

abort() { ABORT=$1; RESULT_HINT=${2:-}; f8a_say "ABORT: $1"; exit 1; }

check_watch() {
    local r
    if f8a_watch_tripped "$WORK" r; then abort "temperature limit ($1): $r"; fi
}

check_fangov() {
    local s
    f8a_fangov_ok s || abort "$F8A_FANGOV_UNIT is '$s' ($1); a second pwm writer invalidates the run"
}

for ((c = 1; c <= CYCLES; c++)); do
    sleep "$CYCLE_PAUSE"
    check_watch "before cycle $c"
    check_fangov "before cycle $c"
    f8a_check_limits require || abort "temperature limit before cycle $c: $F8A_LIMIT_REASON"
    # teardown with reader and writer running
    if [ "$MODE" = load ]; then
        timeout -k 5 60 modprobe -r "$F8A_MODULE" || abort "modprobe -r failed in cycle $c (rc $?)" FAIL
        ! f8a_module_loaded || abort "module still loaded after modprobe -r in cycle $c" FAIL
    else
        f8a_sysfs_write "$F8A_DRIVER_DIR/unbind" "$DEV" || abort "unbind failed in cycle $c ($F8A_ERRNO)" FAIL
        for ((w = 0; w < 100; w++)); do f8a_device_bound "$DEV" || break; sleep 0.1; done
        ! f8a_device_bound "$DEV" || abort "device still bound 10 s after unbind in cycle $c" FAIL
    fi
    hold_writer || abort "writer did not pause in cycle $c"
    check_watch "after teardown in cycle $c"
    # bring-up with the writer held
    if [ "$MODE" = load ]; then
        timeout -k 5 60 modprobe "$F8A_MODULE" debug_regs=1 || abort "modprobe failed in cycle $c (rc $?)" FAIL
    else
        f8a_sysfs_write "$F8A_DRIVER_DIR/bind" "$DEV" || abort "bind failed in cycle $c ($F8A_ERRNO)" FAIL
    fi
    f8a_wait_hwmon "$UP_TIMEOUT" || abort "hwmon device not back within ${UP_TIMEOUT}s in cycle $c" FAIL
    HW=$(f8a_hwmon_dir)
    en=
    for i in 1 2 3 4; do
        f8a_sysfs_read "$HW/pwm${i}_enable" e || e="err-$F8A_ERRNO"
        en="$en $e"
        [ "$e" = 2 ] || STATE_BAD=$((STATE_BAD + 1))
    done
    tach_ok=1
    f8a_check_tachs "$HW" || tach_ok=0
    dcr=''
    f8a_read_dcr dcr || true
    f8a_klog_count "$START" KHITS KLINES KDRV || abort "kernel log unreadable: $KLINES"
    f8a_read_temps
    DONE=$c
    f8a_say "cycle $c/$CYCLES: pwm1..4_enable=${en# } $F8A_TACH_LINE dcr=[$dcr] klog_hits=$KHITS $(f8a_temps_line) writer_err=$(count_by_errno "$WORK/writer.err")"
    [ "$KHITS" = 0 ] || abort "kernel-log hit after cycle $c" "$( [ "$KDRV" -gt 0 ] && echo FAIL || true)"
    [ "$STATE_BAD" = 0 ] || abort "pwm_enable not 2 after reload in cycle $c" FAIL
    [ "$tach_ok" = 1 ] || abort "after reload in cycle $c: $F8A_TACH_REASON" FAIL
    check_watch "after cycle $c"
    check_fangov "after cycle $c"
    f8a_check_limits require || abort "temperature limit after cycle $c: $F8A_LIMIT_REASON"
    release_writer
done
f8a_say "all $CYCLES cycles done"
