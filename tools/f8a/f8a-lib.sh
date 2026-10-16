#!/bin/bash
# f8a-lib.sh - shared functions of the F8a maintenance-window helpers.
#
# Sourced by f8a-snapshot.sh, f8a-stress.sh and f8a-pcie.sh; not meant to be
# executed. Target: Minisforum N5 Pro (DMI "N5 PRO" / F8NAA), driver
# minisforum_n5_it5571 0.2.1+sr1~rc1, run as root by an operator who is at
# the device (TESTSPEC G5).
#
# Safety rules enforced here:
# - DMI product, board and sys_vendor must match exactly, otherwise refuse.
# - n5-fangov must be "inactive" or "failed" (no second pwm writer, G1); any
#   other answer, including an unreadable state, refuses.
# - Research tools (userspace port I/O via ioperm, bypassing the driver lock)
#   only while the driver module is NOT loaded, no driver hwmon device exists
#   and none of the ports 0x2e/0x2f/0x62/0x66/0x68/0x6c is reserved in
#   /proc/ioports (checklist C2), and only with an allowlist of read-only
#   argument sets. "ec_probe write" cannot be run through this library.
# - Duty values below 100 are never written.
# - Limits from sources independent of the EC driver: k10temp Tctl >= 85 C,
#   NVMe >= 70 C, drivetemp >= 50 C abort; a tach of 0 rpm on fan1..fan3
#   aborts (TESTSPEC section 4; pwm4 has no tach).
#
# Test overrides (used by test/run-tests.sh only):
#   F8A_ROOT      prefix for /sys, /proc, /root, /etc, /usr/lib, /run. When
#                 set, modprobe, journalctl, systemctl and F8A_BIN_DIR must
#                 resolve inside F8A_ROOT, otherwise every script refuses.
#   F8A_BIN_DIR   directory of the research tools; honoured only when
#                 F8A_ROOT is set, otherwise always /root/n5pro-ec/bin.
#   F8A_T_*       shortened waits; honoured only when F8A_ROOT is set.

# shellcheck disable=SC2034  # globals are used by the sourcing scripts
F8A_VERSION=0.2.0
F8A_MODULE=minisforum_n5_it5571
F8A_HWMON_NAME=minisforum_n5_it5571
F8A_FANGOV_UNIT=n5-fangov.service
F8A_DMI_PRODUCT='N5 PRO'
F8A_DMI_BOARD='F8NAA'
F8A_DMI_VENDOR='Micro Computer (HK) Tech Limited'
# limits in millidegrees Celsius
F8A_LIM_TCTL=85000
F8A_LIM_NVME=70000
F8A_LIM_HDD=50000
F8A_PWM_MIN=100
# Kernel-log lines that count as a hit. "[minisforum_n5_it5571]" matches a
# stack frame of the module (Call Trace lines of an Oops/WARNING in it).
F8A_KLOG_PATTERN='Oops|BUG:|kernel BUG|WARNING:|Call Trace|general protection|hung_task|blocked for more than|soft lockup|LOCKUP|rcu.*stall|EC timeout|Unknown symbol|minisforum.*(fail|error|timeout|timed out|not created|not set to)|\[minisforum_n5_it5571\]'
# A hit is attributed to the driver when the line names it (dev_* messages,
# module loader, stack frames) or is an "EC timeout" line (patch 0005).
# Other hits (e.g. a ZFS hung task) are FAIL too ("unexplained foreign hit")
# until the operator annotates a cause without driver involvement with
# evidence (TESTSPEC 0.3 5a); the attribution only names which kind it is.
F8A_KLOG_DRIVER='minisforum|EC timeout'
# Printed once per boot on the first load of an unsigned module; not a fault.
F8A_KLOG_IGNORE='minisforum_n5_it5571: module verification failed'
# ports the driver and the research tools use (SIO index/data, EC, PMC2)
F8A_PORTS_RE='^[[:space:]]*(002e|002f|0062|0066|0068|006c)-'

F8A_ROOT=${F8A_ROOT:-}
if [ -n "$F8A_ROOT" ]; then
    F8A_BIN_DIR=${F8A_BIN_DIR:-/root/n5pro-ec/bin}
else
    # never taken from the environment on the real host
    F8A_BIN_DIR=/root/n5pro-ec/bin
fi
F8A_SYS=$F8A_ROOT/sys
F8A_PROC=$F8A_ROOT/proc
F8A_OUT_DIR=$F8A_ROOT/root/n5-mw1/raw
F8A_DEBUGFS_DIR=$F8A_SYS/kernel/debug/$F8A_MODULE
F8A_DRIVER_DIR=$F8A_SYS/bus/platform/drivers/$F8A_MODULE
F8A_LOG=
F8A_TEE_PID=
F8A_ERRNO=

# ---- messages -----------------------------------------------------------

f8a_now_utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }

f8a_say() { printf '%s %s\n' "$(date -u +%H:%M:%S)" "$*"; }

f8a_warn() { printf '%s WARNING(f8a): %s\n' "$(date -u +%H:%M:%S)" "$*" >&2; }

f8a_die() {
    printf '%s REFUSED/ABORT: %s\n' "$(date -u +%H:%M:%S)" "$*" >&2
    exit 1
}

f8a_section() { printf '\n=== %s ===\n' "$*"; }

f8a_rd() {
    # f8a_rd FILE VAR - first line of FILE into VAR, empty on any error
    local __l=
    { IFS= read -r __l < "$1"; } 2>/dev/null || true
    printf -v "$2" '%s' "$__l"
}

f8a_test_mode() { [ -n "$F8A_ROOT" ]; }

f8a_t() {
    # f8a_t NAME DEFAULT - a wait time; the F8A_T_<NAME> override is
    # honoured only in test mode, so a stray variable cannot shorten a
    # measurement on the real host.
    local v='' n="F8A_T_$1"
    if f8a_test_mode; then
        v=${!n:-}
    fi
    printf '%s' "${v:-$2}"
}

# ---- guards -------------------------------------------------------------

f8a_guard_test_mode() {
    # With F8A_ROOT set every command that could touch hardware must be a
    # stub inside F8A_ROOT; a half-configured test environment on the real
    # host would otherwise read fake sysfs but run the real modprobe.
    local c p
    f8a_test_mode || return 0
    case "$F8A_ROOT" in /*) ;; *) f8a_die "F8A_ROOT must be absolute" ;; esac
    for c in modprobe journalctl systemctl; do
        p=$(command -v "$c" 2>/dev/null || true)
        case "$p" in
            "$F8A_ROOT"/*) ;;
            *) f8a_die "test mode (F8A_ROOT set) but '$c' resolves to '${p:-nothing}', not a stub in F8A_ROOT" ;;
        esac
    done
    case "$F8A_BIN_DIR" in
        "$F8A_ROOT"/*) ;;
        *) f8a_die "test mode (F8A_ROOT set) but F8A_BIN_DIR is outside F8A_ROOT" ;;
    esac
}

f8a_require_root() {
    [ "$(id -u)" -eq 0 ] || f8a_die "must run as root"
}

f8a_guard_dmi() {
    local p b v
    f8a_rd "$F8A_SYS/class/dmi/id/product_name" p
    f8a_rd "$F8A_SYS/class/dmi/id/board_name" b
    f8a_rd "$F8A_SYS/class/dmi/id/sys_vendor" v
    if [ "$p" != "$F8A_DMI_PRODUCT" ] || [ "$b" != "$F8A_DMI_BOARD" ] || [ "$v" != "$F8A_DMI_VENDOR" ]; then
        f8a_die "DMI mismatch: product='$p' board='$b' sys_vendor='$v' (expected '$F8A_DMI_PRODUCT'/'$F8A_DMI_BOARD'/'$F8A_DMI_VENDOR')"
    fi
}

f8a_fangov_state() {
    local s
    s=$(systemctl is-active "$F8A_FANGOV_UNIT" 2>/dev/null || true)
    printf '%s' "${s:-unknown}"
}

f8a_fangov_ok() {
    # f8a_fangov_ok [VAR] - 0 only if n5-fangov is inactive or failed; the
    # state seen goes to VAR. "unknown" (systemctl unusable) is not ok.
    local __s
    __s=$(f8a_fangov_state)
    [ -z "${1:-}" ] || printf -v "$1" '%s' "$__s"
    case "$__s" in
        inactive|failed) return 0 ;;
    esac
    return 1
}

f8a_guard_fangov() {
    local s
    f8a_fangov_ok s || f8a_die "$F8A_FANGOV_UNIT is '$s' (only inactive/failed is accepted); stop it first (systemctl stop $F8A_FANGOV_UNIT)"
}

f8a_guard_tty() {
    [ -t 0 ] || f8a_die "stdin is not a terminal; this script must be started interactively at the console"
}

f8a_confirm() {
    # f8a_confirm NAME - the operator has to type the script name
    local ans=
    printf 'Type %s and press Enter to start (anything else cancels):\n' "$1"
    IFS= read -r ans || ans=
    [ "$ans" = "$1" ] || f8a_die "not confirmed, nothing done"
}

f8a_guard_common() {
    f8a_guard_test_mode
    f8a_require_root
    f8a_guard_dmi
    f8a_guard_fangov
}

# ---- module, hwmon, platform device ----------------------------------

f8a_module_loaded() {
    local l
    [ -r "$F8A_PROC/modules" ] || return 1
    while IFS= read -r l; do
        [ "${l%% *}" = "$F8A_MODULE" ] && return 0
    done < "$F8A_PROC/modules"
    return 1
}

f8a_module_param() {
    # f8a_module_param NAME - current value (Y/N/...) or empty
    local v
    f8a_rd "$F8A_SYS/module/$F8A_MODULE/parameters/$1" v
    printf '%s' "$v"
}

f8a_hwmon_dir() {
    # print the hwmon dir of the EC driver; resolved by name on every call
    local d n
    for d in "$F8A_SYS"/class/hwmon/hwmon*; do
        f8a_rd "$d/name" n
        if [ "$n" = "$F8A_HWMON_NAME" ]; then printf '%s\n' "$d"; return 0; fi
    done
    return 1
}

f8a_wait_hwmon() {
    # f8a_wait_hwmon SECONDS - wait until the hwmon dir exists
    local i n=$(( $1 * 10 ))
    for ((i = 0; i < n; i++)); do
        f8a_hwmon_dir >/dev/null && return 0
        sleep 0.1
    done
    return 1
}

f8a_modprobe_opt() {
    # f8a_modprobe_opt NAME VAR - effective value of a module option as
    # modprobe will pass it: the last "NAME=" over all "options" lines of
    # the module in "modprobe -c" (later assignments override earlier ones,
    # so an override file with NAME=0 wins). Empty if not set.
    local __l __m __w __v=
    local -a __f
    while IFS= read -r __l; do
        read -r -a __f <<< "$__l"
        [ "${__f[0]:-}" = options ] || continue
        __m=${__f[1]:-}
        [ "${__m//-/_}" = "$F8A_MODULE" ] || continue
        for __w in "${__f[@]:2}"; do
            case "$__w" in "$1="*) __v=${__w#*=} ;; esac
        done
    done < <(modprobe -c 2>/dev/null || true)
    printf -v "$2" '%s' "$__v"
}

f8a_modprobe_opt_true() {
    # f8a_modprobe_opt_true NAME - effective option value is a true bool
    local v
    f8a_modprobe_opt "$1" v
    case "$v" in 1|Y|y|true|on) return 0 ;; esac
    return 1
}

f8a_platform_device() {
    # Print the platform device bound to the driver. It must also exist in
    # /sys/bus/platform/devices. Fails unless there is exactly one.
    local e n found=
    [ -d "$F8A_DRIVER_DIR" ] || return 1
    for e in "$F8A_DRIVER_DIR"/*; do
        [ -L "$e" ] || continue
        n=${e##*/}
        [ "$n" = module ] && continue
        [ -e "$F8A_SYS/bus/platform/devices/$n" ] || continue
        [ -z "$found" ] || return 1
        found=$n
    done
    [ -n "$found" ] || return 1
    printf '%s\n' "$found"
}

f8a_device_bound() { [ -L "$F8A_DRIVER_DIR/$1" ]; }

f8a_read_dcr() {
    # f8a_read_dcr VAR - "1800: xx xx ..." into VAR; returns 1 with a reason
    # in VAR when the debugfs file is not there or not readable
    local __out
    if [ ! -e "$F8A_DEBUGFS_DIR/dcr" ]; then
        printf -v "$1" '%s' "dcr unavailable (no $F8A_DEBUGFS_DIR/dcr: module without debug_regs=1, or debugfs not mounted)"
        return 1
    fi
    if __out=$(timeout 10 cat "$F8A_DEBUGFS_DIR/dcr" 2>&1); then
        printf -v "$1" '%s' "${__out%%$'\n'*}"
        return 0
    fi
    printf -v "$1" '%s' "dcr read failed: ${__out//$'\n'/ }"
    return 1
}

f8a_dcr_byte() {
    # f8a_dcr_byte LINE INDEX - byte at 0x1800+INDEX from a dcr/i2ec line
    # ("1800: b0 b1 ..."); empty if not present
    local -a f
    read -r -a f <<< "$1"
    [ "${f[0]:-}" = "1800:" ] || return 0
    printf '%s' "${f[$(($2 + 1))]:-}"
}

# ---- independent temperatures --------------------------------------------

F8A_TCTL='' F8A_NVME='' F8A_HDD=''
F8A_MAX_TCTL='' F8A_MAX_NVME='' F8A_MAX_HDD=''
F8A_LIMIT_REASON=

f8a__max_of_type() {
    # f8a__max_of_type HWMON_NAME VAR - max over all temp*_input of all
    # hwmon devices with that name (millidegrees), empty if none
    local d n f v m=
    for d in "$F8A_SYS"/class/hwmon/hwmon*; do
        f8a_rd "$d/name" n
        [ "$n" = "$1" ] || continue
        for f in "$d"/temp*_input; do
            [ -e "$f" ] || continue
            f8a_rd "$f" v
            case "$v" in ''|*[!0-9-]*) continue ;; esac
            if [ -z "$m" ] || [ "$v" -gt "$m" ]; then m=$v; fi
        done
    done
    printf -v "$2" '%s' "$m"
}

f8a_read_temps() {
    # sets F8A_TCTL F8A_NVME F8A_HDD (millidegrees, empty = no source) and
    # the maxima seen so far
    local d n l v lab
    F8A_TCTL=
    for d in "$F8A_SYS"/class/hwmon/hwmon*; do
        f8a_rd "$d/name" n
        [ "$n" = k10temp ] || continue
        for l in "$d"/temp*_label; do
            [ -e "$l" ] || continue
            f8a_rd "$l" lab
            if [ "$lab" = Tctl ]; then
                f8a_rd "${l%_label}_input" v
                case "$v" in ''|*[!0-9-]*) ;; *) F8A_TCTL=$v ;; esac
            fi
        done
    done
    f8a__max_of_type nvme F8A_NVME
    f8a__max_of_type drivetemp F8A_HDD
    if [ -n "$F8A_TCTL" ] && { [ -z "$F8A_MAX_TCTL" ] || [ "$F8A_TCTL" -gt "$F8A_MAX_TCTL" ]; }; then F8A_MAX_TCTL=$F8A_TCTL; fi
    if [ -n "$F8A_NVME" ] && { [ -z "$F8A_MAX_NVME" ] || [ "$F8A_NVME" -gt "$F8A_MAX_NVME" ]; }; then F8A_MAX_NVME=$F8A_NVME; fi
    if [ -n "$F8A_HDD" ] && { [ -z "$F8A_MAX_HDD" ] || [ "$F8A_HDD" -gt "$F8A_MAX_HDD" ]; }; then F8A_MAX_HDD=$F8A_HDD; fi
    return 0
}

f8a_c() {
    # f8a_c MILLIDEG - "47.5" or "n/a"
    case "${1:-}" in
        ''|*[!0-9-]*) printf 'n/a' ;;
        *) printf '%d.%d' $(($1 / 1000)) $(((${1#-} % 1000) / 100)) ;;
    esac
}

f8a_temps_line() {
    printf 'Tctl=%s NVMe_max=%s HDD_max=%s' "$(f8a_c "$F8A_TCTL")" "$(f8a_c "$F8A_NVME")" "$(f8a_c "$F8A_HDD")"
}

f8a_check_limits() {
    # f8a_check_limits [require] - read temps; return 1 with F8A_LIMIT_REASON
    # if a limit is reached, or (with "require") if a source is missing
    F8A_LIMIT_REASON=
    f8a_read_temps
    if [ "${1:-}" = require ]; then
        [ -n "$F8A_TCTL" ] || F8A_LIMIT_REASON="no k10temp Tctl value"
        [ -n "$F8A_NVME" ] || F8A_LIMIT_REASON="${F8A_LIMIT_REASON:+$F8A_LIMIT_REASON; }no NVMe temperature"
        [ -n "$F8A_HDD" ] || F8A_LIMIT_REASON="${F8A_LIMIT_REASON:+$F8A_LIMIT_REASON; }no drivetemp temperature"
    fi
    if [ -n "$F8A_TCTL" ] && [ "$F8A_TCTL" -ge "$F8A_LIM_TCTL" ]; then F8A_LIMIT_REASON="${F8A_LIMIT_REASON:+$F8A_LIMIT_REASON; }Tctl $(f8a_c "$F8A_TCTL") >= $(f8a_c "$F8A_LIM_TCTL")"; fi
    if [ -n "$F8A_NVME" ] && [ "$F8A_NVME" -ge "$F8A_LIM_NVME" ]; then F8A_LIMIT_REASON="${F8A_LIMIT_REASON:+$F8A_LIMIT_REASON; }NVMe $(f8a_c "$F8A_NVME") >= $(f8a_c "$F8A_LIM_NVME")"; fi
    if [ -n "$F8A_HDD" ] && [ "$F8A_HDD" -ge "$F8A_LIM_HDD" ]; then F8A_LIMIT_REASON="${F8A_LIMIT_REASON:+$F8A_LIMIT_REASON; }HDD $(f8a_c "$F8A_HDD") >= $(f8a_c "$F8A_LIM_HDD")"; fi
    [ -z "$F8A_LIMIT_REASON" ]
}

F8A_TACH_REASON=
F8A_TACH_LINE=

f8a_check_tachs() {
    # f8a_check_tachs HWMON_DIR - fan1..fan3 (the channels with a tach) must
    # read > 0 rpm while the EC or a write >= 100 drives them (TESTSPEC 4).
    # A 0 or an unreadable value is re-read up to two times, 1 s apart.
    # Returns 1 with F8A_TACH_REASON; F8A_TACH_LINE holds the last values.
    local try ch v line='' bad=''
    F8A_TACH_REASON=
    for try in 1 2 3; do
        line=
        bad=
        for ch in 1 2 3; do
            if f8a_sysfs_read "$1/fan${ch}_input" v; then
                case "$v" in
                    ''|*[!0-9]*) bad="${bad:+$bad, }fan${ch}_input='$v'" ;;
                    0) bad="${bad:+$bad, }fan${ch}_input=0 rpm" ;;
                esac
            else
                v="err-$F8A_ERRNO"
                bad="${bad:+$bad, }fan${ch}_input unreadable ($F8A_ERRNO)"
            fi
            line="$line fan${ch}=$v"
        done
        [ -n "$bad" ] || break
        [ "$try" -eq 3 ] || sleep "$(f8a_t TACH_RETRY 1)"
    done
    F8A_TACH_LINE=${line# }
    [ -z "$bad" ] && return 0
    F8A_TACH_REASON="tach check: $bad (3 reads, 1 s apart)"
    return 1
}

# ---- background limit watcher ---------------------------------------------
#
# Checks the temperature limits every second while the main script may be
# blocked in modprobe, unbind or a wait. It cannot interrupt a hung kernel
# call; it writes WORK/limit (checked by the main script at every step) and
# repeats a loud warning every 5 s so that the operator acts (run card R3/R4).

F8A_WATCH_PID=

f8a_watch_start() {
    # f8a_watch_start WORKDIR
    local w=$1 main=$$
    (
        trap - EXIT INT TERM ERR
        set +e
        w_warned=-100 w_round=0
        while [ ! -e "$w/stop" ]; do
            # the main script is gone (SIGKILL): do not linger
            kill -0 "$main" 2>/dev/null || exit 0
            if ! f8a_check_limits require; then
                [ -e "$w/limit" ] || printf '%s\n' "$F8A_LIMIT_REASON" > "$w/limit"
                if [ $((w_round - w_warned)) -ge 5 ]; then
                    printf '%s !!! LIMIT (watcher): %s - script stops at its next step; if it hangs: run card R3/R4\n' "$(date -u +%H:%M:%S)" "$F8A_LIMIT_REASON" >&2
                    w_warned=$w_round
                fi
            fi
            printf '%s %s %s\n' "${F8A_MAX_TCTL:-}" "${F8A_MAX_NVME:-}" "${F8A_MAX_HDD:-}" > "$w/tmax.tmp" && mv -f "$w/tmax.tmp" "$w/tmax"
            w_round=$((w_round + 1))
            sleep "$(f8a_t WATCH 1)"
        done
    ) &
    F8A_WATCH_PID=$!
}

f8a_watch_stop() {
    # f8a_watch_stop WORKDIR - stop the watcher, merge its maxima
    local a b c
    [ -n "$F8A_WATCH_PID" ] || return 0
    : > "$1/stop"
    kill "$F8A_WATCH_PID" 2>/dev/null || true
    wait "$F8A_WATCH_PID" 2>/dev/null || true
    F8A_WATCH_PID=
    if [ -r "$1/tmax" ] && read -r a b c < "$1/tmax"; then
        if [ -n "$a" ] && { [ -z "$F8A_MAX_TCTL" ] || [ "$a" -gt "$F8A_MAX_TCTL" ]; }; then F8A_MAX_TCTL=$a; fi
        if [ -n "$b" ] && { [ -z "$F8A_MAX_NVME" ] || [ "$b" -gt "$F8A_MAX_NVME" ]; }; then F8A_MAX_NVME=$b; fi
        if [ -n "$c" ] && { [ -z "$F8A_MAX_HDD" ] || [ "$c" -gt "$F8A_MAX_HDD" ]; }; then F8A_MAX_HDD=$c; fi
    fi
    return 0
}

f8a_watch_tripped() {
    # f8a_watch_tripped WORKDIR VAR - 0 (and the reason in VAR) if the
    # watcher saw a limit
    local __r=
    [ -e "$1/limit" ] || return 1
    __r=$(cat "$1/limit" 2>/dev/null || true)
    printf -v "$2" '%s' "${__r:-limit}"
    return 0
}

# ---- kernel log -----------------------------------------------------------

f8a_klog_hits() {
    # f8a_klog_hits SINCE_EPOCH - print matching kernel log lines since then.
    # Returns 2 if the kernel log cannot be read (that is never "0 hits").
    local out
    if ! out=$(journalctl -k --since "@$1" --no-pager -o short-iso-precise 2>&1); then
        printf 'journalctl failed: %s\n' "${out//$'\n'/ }"
        return 2
    fi
    printf '%s\n' "$out" | grep -E -- "$F8A_KLOG_PATTERN" | grep -v -E -- "$F8A_KLOG_IGNORE" || true
    return 0
}

f8a_klog_count() {
    # f8a_klog_count SINCE_EPOCH VAR_COUNT VAR_LINES [VAR_DRIVER]
    # COUNT = all hits, DRIVER = hits attributed to the driver
    # (F8A_KLOG_DRIVER). Both -1 and return 2 if the log is unreadable.
    local __o __rc=0 __d=0
    __o=$(f8a_klog_hits "$1") || __rc=$?
    if [ "$__rc" -ne 0 ]; then
        printf -v "$2" '%s' "-1"
        printf -v "$3" '%s' "$__o"
        [ -z "${4:-}" ] || printf -v "$4" '%s' "-1"
        return 2
    fi
    if [ -z "$__o" ]; then
        printf -v "$2" '%s' 0
    else
        printf -v "$2" '%s' "$(printf '%s\n' "$__o" | wc -l)"
        __d=$(printf '%s\n' "$__o" | grep -c -E -- "$F8A_KLOG_DRIVER" || true)
    fi
    printf -v "$3" '%s' "$__o"
    [ -z "${4:-}" ] || printf -v "$4" '%s' "${__d:-0}"
    return 0
}

f8a_klog_verdict() {
    # f8a_klog_verdict COUNT DRIVER - prints "" (no hit), "FAIL ..." or
    # "INCONCLUSIVE (kernel log unreadable)" for the kernel-log part of a
    # step result. TESTSPEC 0.3 5a (as F8b): every hit is FAIL; a hit
    # without driver reference stays FAIL until the operator annotates a
    # cause without driver involvement with evidence. A hit is never
    # INCONCLUSIVE.
    if [ "$1" = -1 ]; then
        printf 'INCONCLUSIVE (kernel log unreadable)'
    elif [ "${2:-0}" -gt 0 ] && [ "$1" -gt "$2" ]; then
        printf 'FAIL (%s kernel-log hit(s) attributed to the driver; %s unexplained foreign hit(s) - annotate with evidence or rerun)' "$2" "$(($1 - $2))"
    elif [ "${2:-0}" -gt 0 ]; then
        printf 'FAIL (%s kernel-log hit(s) attributed to the driver)' "$2"
    elif [ "$1" -gt 0 ]; then
        printf 'FAIL (unexplained foreign hit - annotate with evidence or rerun: %s kernel-log hit(s) without driver reference)' "$1"
    fi
}

# ---- sysfs access with errno ---------------------------------------------

f8a_errno_name() {
    local m
    case "$1" in
        *"No such device or address"*) printf 'ENXIO' ;;
        *"No such device"*) printf 'ENODEV' ;;
        *"Device or resource busy"*) printf 'EBUSY' ;;
        *"No such file or directory"*) printf 'ENOENT' ;;
        *"No data available"*) printf 'ENODATA' ;;
        *"Invalid argument"*) printf 'EINVAL' ;;
        *"Operation not permitted"*) printf 'EPERM' ;;
        *"Permission denied"*) printf 'EACCES' ;;
        *"timed out"*) printf 'ETIMEDOUT' ;;
        *"Input/output error"*) printf 'EIO' ;;
        *"Resource temporarily unavailable"*) printf 'EAGAIN' ;;
        *"Operation not supported"*) printf 'EOPNOTSUPP' ;;
        *) m=${1##*: }; m=${m//[^A-Za-z0-9]/_}; printf 'OTHER(%s)' "${m:0:40}" ;;
    esac
}

f8a__inject() {
    # Test hook, inert unless F8A_ROOT is set: a file "<path>.errno" in the
    # fake tree makes the access fail with that message (fake teardown).
    f8a_test_mode || return 1
    local msg
    # the fake teardown may remove the file between test and read; then
    # the real access below runs and reports ENOENT like a vanished sysfs file
    msg=$(cat "$1.errno" 2>/dev/null) || return 1
    [ -n "$msg" ] || return 1
    F8A_ERRNO=$(f8a_errno_name "$msg")
    return 0
}

f8a_sysfs_read() {
    # f8a_sysfs_read PATH VAR - value into VAR; on failure returns 1 and
    # sets F8A_ERRNO
    local __o
    F8A_ERRNO=
    if f8a__inject "$1"; then printf -v "$2" '%s' ''; return 1; fi
    if __o=$(cat -- "$1" 2>&1); then
        printf -v "$2" '%s' "$__o"
        return 0
    fi
    F8A_ERRNO=$(f8a_errno_name "$__o")
    printf -v "$2" '%s' ''
    return 1
}

f8a_sysfs_write() {
    # f8a_sysfs_write PATH VALUE - on failure returns 1 and sets F8A_ERRNO
    local __o
    F8A_ERRNO=
    # test hook, inert unless F8A_ROOT is set: record every write attempt
    if f8a_test_mode; then printf '%s %s\n' "${1#"$F8A_ROOT"}" "$2" >> "$F8A_ROOT/writes.log" 2>/dev/null || true; fi
    if f8a__inject "$1"; then return 1; fi
    if __o=$( { printf '%s\n' "$2" > "$1"; } 2>&1 ); then return 0; fi
    F8A_ERRNO=$(f8a_errno_name "$__o")
    return 1
}

f8a_write_pwm() {
    # f8a_write_pwm HWMON_DIR CHANNEL VALUE - never below F8A_PWM_MIN
    case "$3" in ''|*[!0-9]*) f8a_die "pwm value '$3' is not a number" ;; esac
    if [ "$3" -lt "$F8A_PWM_MIN" ] || [ "$3" -gt 255 ]; then
        f8a_die "refusing pwm$2=$3: duty below $F8A_PWM_MIN is never written"
    fi
    f8a_sysfs_write "$1/pwm$2" "$3"
}

# ---- research tools (module NOT loaded only) -----------------------------

f8a_ports_reserved() {
    # print the /proc/ioports lines of 0x2e/0x2f/0x62/0x66/0x68/0x6c (any
    # owner); returns 1 if none is reserved
    grep -E "$F8A_PORTS_RE" "$F8A_PROC/ioports" 2>/dev/null
}

f8a_research() {
    # f8a_research TOOL ARGS... - run one allowlisted, read-only research
    # tool call. Refuses while the driver is loaded, its hwmon device exists
    # or one of its ports is reserved by anybody (C2), or n5-fangov is not
    # inactive/failed. Returns the tool's exit code (0 = ok).
    local tool=$1 rc=0 p
    shift
    case "$tool $*" in
        'ec_probe dump'|'n5_i2ec_read 0x1800 48'|'n5_fan rpm') ;;
        *) f8a_die "refusing research tool call '$tool $*' (not on the read-only allowlist)" ;;
    esac
    if f8a_module_loaded || f8a_hwmon_dir >/dev/null; then
        f8a_die "refusing '$tool $*': $F8A_MODULE is loaded (checklist C2: no userspace port I/O beside the driver)"
    fi
    if p=$(f8a_ports_reserved); then
        f8a_die "refusing '$tool $*': EC/PMC2/SIO ports are reserved in /proc/ioports (checklist C2): ${p//$'\n'/; }"
    fi
    f8a_guard_fangov
    [ -x "$F8A_BIN_DIR/$tool" ] || f8a_die "research tool $F8A_BIN_DIR/$tool not found"
    timeout -k 5 120 "$F8A_BIN_DIR/$tool" "$@" || rc=$?
    [ "$rc" -eq 0 ] || printf '(%s %s exited with %s)\n' "$tool" "$*" "$rc"
    return "$rc"
}

f8a_research_busy() {
    # print the PIDs of running research-tool processes (also a "timeout"
    # wrapping one), and "lock" when /run/n5-ectest.lock is held (n5-ectest
    # raw mode A, the only n5-ectest mode with port I/O; mutual exclusion,
    # TESTKIT-DESIGN 5). Other n5-ectest processes (log, snapshot mode B)
    # never touch the ports and do not count. Returns 1 if there are none.
    local p c found=
    for p in /proc/[0-9]*; do
        c=$(tr '\0' ' ' 2>/dev/null < "$p/cmdline") || continue
        case "$c" in
            *"$F8A_BIN_DIR/"ec_probe*|*"$F8A_BIN_DIR/"n5_fan*|*"$F8A_BIN_DIR/"n5_i2ec_read*|*"$F8A_BIN_DIR/"n5_pwm_diag*|*"$F8A_BIN_DIR/"sio_probe*)
                found="$found ${p#/proc/}"; continue ;;
        esac
    done
    if [ -e "$F8A_ROOT/run/n5-ectest.lock" ] && ! flock -n "$F8A_ROOT/run/n5-ectest.lock" true 2>/dev/null; then
        found="$found lock"
    fi
    [ -n "$found" ] || return 1
    printf '%s\n' "${found# }"
}

f8a_writer_lock() {
    # f8a_writer_lock NAME - hold <run>/n5-fangov/writer.lock, the pwm
    # writer lock of n5-fangov (>= 0.5) and n5-ectest, for the life of the
    # script (an inherited descriptor; released when the script and its
    # children exit). Dies when another pwm writer holds it.
    local d="$F8A_ROOT/run/n5-fangov" holder=
    mkdir -p "$d" || f8a_die "cannot create $d"
    exec {F8A_LOCK_FD}<>"$d/writer.lock" || f8a_die "cannot open $d/writer.lock"
    if ! flock -n "$F8A_LOCK_FD"; then
        holder=$(head -c 200 "$d/writer.lock" 2>/dev/null | tr -d '\n') || true
        f8a_die "another pwm writer holds $d/writer.lock (${holder:-unknown}); stop it first"
    fi
    printf '%s pid %s since %s\n' "$1" "$$" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" > "$d/writer.lock" || true
}

f8a_wait_research_idle() {
    # f8a_wait_research_idle SECONDS - before any modprobe in a restore path:
    # wait until no research tool runs (C2). Returns 1 on timeout.
    local i p
    for ((i = 0; i < $1 * 2; i++)); do
        p=$(f8a_research_busy) || return 0
        [ "$i" -gt 0 ] || f8a_say "waiting for research tool process(es) $p to finish before modprobe (C2)"
        sleep 0.5
    done
    return 1
}

# ---- raw log --------------------------------------------------------------

f8a__host_sed() {
    # sed expression replacing the host name (full and short) with n5host
    local full short e=
    full=$(uname -n 2>/dev/null || true)
    short=${full%%.*}
    if [ -n "$full" ]; then e="s/${full//./\\.}/n5host/g;"; fi
    if [ -n "$short" ] && [ "$short" != "$full" ]; then e="${e}s/${short}/n5host/g;"; fi
    printf '%s' "${e:-s/^//}"
}

f8a_log_start() {
    # f8a_log_start NAME LABEL - tee stdout+stderr (host name redacted) to
    # /root/n5-mw1/raw/<name>-<label>-<UTC>.txt
    local ts expr
    ts=$(date -u +%Y%m%dT%H%M%SZ)
    mkdir -p "$F8A_OUT_DIR"
    chmod 700 "$F8A_OUT_DIR" 2>/dev/null || true
    F8A_LOG=$F8A_OUT_DIR/$1-$2-$ts.txt
    [ ! -e "$F8A_LOG" ] || F8A_LOG=$F8A_OUT_DIR/$1-$2-$ts-$$.txt
    : > "$F8A_LOG"
    expr=$(f8a__host_sed)
    exec 8>&1 9>&2
    # The log pipeline shares the terminal's process group: Ctrl-C and a
    # hangup reach it too. It ignores INT/TERM/HUP (inherited by sed and
    # tee) and tee keeps writing the file when the terminal is gone, so the
    # restore and summary of the calling script are still logged.
    exec > >(trap '' INT TERM HUP; sed -u -e "$expr" | tee -i --output-error=warn -a "$F8A_LOG") 2>&1
    F8A_TEE_PID=$!
}

f8a_cleanup_signals() {
    # first thing in an EXIT handler: a second Ctrl-C, a hangup or a broken
    # pipe must not stop the restore
    trap '' INT TERM HUP PIPE
    trap - ERR
}

f8a_log_finish() {
    # close the tee, redact once more, write <file>.sha256
    local expr
    [ -n "$F8A_LOG" ] || return 0
    exec 1>&8 2>&9
    if [ -n "$F8A_TEE_PID" ]; then wait "$F8A_TEE_PID" 2>/dev/null || true; fi
    expr=$(f8a__host_sed)
    sed -i -e "$expr" "$F8A_LOG"
    (cd "${F8A_LOG%/*}" && sha256sum "${F8A_LOG##*/}" > "${F8A_LOG##*/}.sha256")
    printf 'raw log: %s (+ .sha256)\n' "$F8A_LOG"
    F8A_TEE_PID=
}

f8a_identity() {
    # date, uptime, boot, kernel, DMI, modules, ports, params, temps, n5-fangov
    local f v up bid
    f8a_section "identity"
    printf 'tool: f8a %s\n' "$F8A_VERSION"
    printf 'date_utc: %s\n' "$(f8a_now_utc)"
    f8a_rd "$F8A_PROC/uptime" up
    printf 'uptime_s: %s\n' "${up%% *}"
    f8a_rd "$F8A_PROC/sys/kernel/random/boot_id" bid
    printf 'boot_id: %s\n' "$bid"
    printf 'kernel: %s\n' "$(uname -r)"
    printf 'host: %s\n' "$(uname -n)"
    for f in product_name board_name board_version sys_vendor bios_vendor bios_version bios_date bios_release ec_firmware_release; do
        f8a_rd "$F8A_SYS/class/dmi/id/$f" v
        printf 'dmi.%s: %s\n' "$f" "$v"
    done
    f8a_section "lsmod (filtered)"
    grep -E '^(minisforum_n5_it5571|k10temp|drivetemp|nvme|it87|nct6775) ' "$F8A_PROC/modules" 2>/dev/null || echo "(none)"
    f8a_section "/proc/ioports 002e-002f/0062/0066/0068/006c"
    grep -E '^[[:space:]]*(002e|002f|0062|0066|0068|006c)-' "$F8A_PROC/ioports" 2>/dev/null || echo "(no entries: ports not reserved)"
    f8a_section "module parameters"
    if [ -d "$F8A_SYS/module/$F8A_MODULE/parameters" ]; then
        for f in "$F8A_SYS/module/$F8A_MODULE/parameters"/*; do
            [ -e "$f" ] || continue
            f8a_rd "$f" v
            printf '%s=%s\n' "${f##*/}" "$v"
        done
    else
        echo "module not loaded"
    fi
    echo "modprobe.d / modules-load.d entries:"
    grep -H -s -E "$F8A_MODULE|minisforum" "$F8A_ROOT"/etc/modprobe.d/*.conf "$F8A_ROOT"/run/modprobe.d/*.conf "$F8A_ROOT"/usr/lib/modprobe.d/*.conf "$F8A_ROOT"/etc/modules-load.d/*.conf "$F8A_ROOT"/usr/lib/modules-load.d/*.conf "$F8A_ROOT"/etc/modules 2>/dev/null || echo "(none)"
    echo "effective options (modprobe -c):"
    { modprobe -c 2>/dev/null || true; } | grep -E "^options ($F8A_MODULE|minisforum-n5-it5571) " || echo "(none)"
    f8a_section "independent temperatures (k10temp, nvme, drivetemp)"
    f8a_read_temps
    f8a_temps_line; echo
    f8a_section "n5-fangov"
    printf 'is-active: %s\n' "$(f8a_fangov_state)"
    printf 'is-enabled: %s\n' "$(systemctl is-enabled "$F8A_FANGOV_UNIT" 2>/dev/null || true)"
}
