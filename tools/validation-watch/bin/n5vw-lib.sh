#!/bin/bash
# n5vw-lib.sh - shared functions for the n5-validation watcher (F8b).
#
# Sourced by n5vw-sample, n5vw-check, n5vw-report, n5vw-failmail, annotate.sh,
# status.sh. Everything here is READ-ONLY with respect to hardware: only
# sysfs/procfs reads, journalctl and systemctl show. No writes to /sys, no
# port I/O, no modprobe, no n5-fangov commands.
#
# Environment overrides (mainly for dry runs and tests):
#   N5VW_CONF         config file            (default /etc/n5-validation.conf)
#   N5VW_DATA_ROOT    data root              (overrides DATA_ROOT)
#   N5VW_RUN_ID       run id                 (overrides RUN_ID)
#   N5VW_START_EPOCH  run start, epoch secs  (overrides START_EPOCH)
#   N5VW_DRY_RUN=1    print mails to stdout instead of calling sendmail,
#                     never touch systemd units
#   N5VW_LOCKED=1     caller already holds the run lock (internal)

# CSV header of samples.csv. pwm values are the driver's CACHED values
# (what was last written / reported by the driver), not a live EC readback.
# shellcheck disable=SC2034  # used by n5vw-sample
N5VW_CSV_HEADER='time_iso,epoch,boot_id,uptime_s,load1,fan1_rpm,fan2_rpm,fan3_rpm,ec_temp1_cpu_c,ec_temp2_system_c,ec_temp3_board_c,ec_temp4_ambient_c,pwm1_cached,pwm2_cached,pwm3_cached,pwm4_cached,pwm1_enable_cached,pwm2_enable_cached,pwm3_enable_cached,pwm4_enable_cached,k10_tctl_c,nvme_max_c,hdd_max_c,ec_read_errors,fangov_state,fangov_nrestarts'

N5VW_KEYS='K1 K2 K3 K4 K5 K6 K7 K8'

vw_log() {
    # "<5>" = notice; stderr ends up in the journal of the calling unit
    # (the units use LogLevelMax=notice, plain info lines would be dropped)
    printf '<5>n5vw: %s\n' "$*" >&2
}

vw_err() {
    # "<3>" = syslog priority err (systemd SyslogLevelPrefix)
    printf '<3>n5vw: %s\n' "$*" >&2
}

# Globals set here are consumed by the scripts that source this file.
# shellcheck disable=SC2034
vw_load_config() {
    # defaults
    RUN_ID=baseline-20261008
    DURATION_DAYS=7
    DATA_ROOT=/var/lib/n5-validation
    MAIL_TO=root
    SAMPLE_INTERVAL=30
    CHECK_INTERVAL=300
    START_EPOCH=
    TAINT_START=
    BOOT_ID_START=
    FANGOV_UNIT=n5-fangov.service
    EC_HWMON_NAME=minisforum_n5_it5571
    TH_TCTL=85
    TH_NVME=70
    TH_HDD=50
    TH_EC_CPU=90
    GAP_MAX=90
    CHECK_GAP_MAX=660
    COVER_MIN=99
    DATA_MAX_MB=200
    ROOT_MIN_FREE_GB=5
    ALERT_COOLDOWN=3600
    ANNOT_WINDOW=600
    ANNOT_EXPLICIT_ONLY='K1 K7'
    K1_MAX_PER_RUN=300
    K1_PATTERN='Oops|BUG:|kernel BUG at|general protection|Call Trace|INFO: task .* blocked for more than|hung_task|soft lockup|hard LOCKUP|rcu.*stall|watchdog: |Hardware Error|mce:|Machine check|AER:|PCIe Bus Error|nvme.*(timeout|reset)|minisforum.*(timed? ?out|fail|error)'
    # Known benign boot lines that the pattern above would hit.
    K1_IGNORE='NMI watchdog: Enabled|minisforum_n5_it5571: module verification failed|AER: enabled with IRQ'
    # Ignored only when the line belongs to an earlier boot (printed at
    # shutdown); inside the running boot it is a real signal.
    K1_IGNORE_PREVBOOT='watchdog: watchdog[0-9]+: watchdog did not stop'

    N5VW_CONF=${N5VW_CONF:-/etc/n5-validation.conf}
    if [ -r "$N5VW_CONF" ]; then
        # shellcheck disable=SC1090
        . "$N5VW_CONF"
    fi
    RUN_ID=${N5VW_RUN_ID:-$RUN_ID}
    DATA_ROOT=${N5VW_DATA_ROOT:-$DATA_ROOT}
    START_EPOCH=${N5VW_START_EPOCH:-$START_EPOCH}
    DRY_RUN=${N5VW_DRY_RUN:-0}
    RUN_DIR=$DATA_ROOT/$RUN_ID
    SAMPLES=$RUN_DIR/samples.csv
    EVENTS=$RUN_DIR/events.log
    CHECKS=$RUN_DIR/checks.log
    STATE=$RUN_DIR/state
    ANNOT=$RUN_DIR/annotations.txt
    HOST=${HOSTNAME%%.*}
    [ -n "$HOST" ] || HOST=unknown
    if [ -z "$START_EPOCH" ]; then
        # no recorded start (dry run without config): first sample or now
        START_EPOCH=$(vw_first_sample_epoch)
        [ -n "$START_EPOCH" ] || START_EPOCH=$EPOCHSECONDS
    fi
    END_EPOCH=$((START_EPOCH + DURATION_DAYS * 86400))
}

vw_first_sample_epoch() {
    local line
    [ -r "${SAMPLES:-}" ] || return 0
    line=$(sed -n '2p' "$SAMPLES" 2>/dev/null || true)
    line=${line#*,}
    printf '%s' "${line%%,*}"
}

vw_iso() {
    # vw_iso [epoch] -> local ISO 8601 time (prints; use vw_iso_v in loops)
    printf '%(%Y-%m-%dT%H:%M:%S%z)T' "${1:-$EPOCHSECONDS}"
}

vw_iso_v() {
    # vw_iso_v VAR [epoch] - like vw_iso, into VAR, no fork
    printf -v "$1" '%(%Y-%m-%dT%H:%M:%S%z)T' "${2:-$EPOCHSECONDS}"
}

vw_rd() {
    # vw_rd FILE VAR - read first line of FILE into VAR, empty on any error
    # (missing file, EIO/ETIMEDOUT from the driver). No fork.
    local __line=
    { IFS= read -r __line < "$1"; } 2>/dev/null || true
    printf -v "$2" '%s' "$__line"
}

vw_hwmon_dirs() {
    # vw_hwmon_dirs NAME - print all hwmon dirs whose "name" equals NAME.
    # Resolved on every call because hwmon indices change across boots.
    local d n
    for d in /sys/class/hwmon/hwmon*; do
        vw_rd "$d/name" n
        if [ "$n" = "$1" ]; then printf '%s\n' "$d"; fi
    done
}

vw_mdeg() {
    # vw_mdeg MILLIDEG VAR - millidegrees -> degrees with one decimal; empty stays empty
    local __m=$1 __s=
    case "$__m" in
        ''|*[!0-9-]*) printf -v "$2" '%s' ''; return 0 ;;
    esac
    if [ "${__m#-}" != "$__m" ]; then __s=-; __m=${__m#-}; fi
    printf -v "$2" '%s%d.%d' "$__s" $((__m / 1000)) $(((__m % 1000) / 100))
}

vw_lock() {
    # vw_lock - serialise check/report/final on RUN_DIR/.lock (fd 9).
    # The sampler does not take the lock: it only appends one line and must
    # not be delayed by a long check run.
    [ "${N5VW_LOCKED:-0}" = 1 ] && return 0
    exec 9>>"$RUN_DIR/.lock"
    if ! flock -w 150 9; then
        vw_err "run lock $RUN_DIR/.lock busy for 150 s, giving up"
        exit 1
    fi
    export N5VW_LOCKED=1
}

vw_disable_timers() {
    if [ "$DRY_RUN" = 1 ]; then
        echo "DRY-RUN: would disable n5vw-sample.timer n5vw-check.timer n5vw-report.timer"
        return 0
    fi
    systemctl disable --now n5vw-sample.timer n5vw-check.timer n5vw-report.timer >&2 || {
        vw_err "could not disable timers"
        return 1
    }
}

# ---- state file (key=value, parsed, never sourced) -------------------------

declare -gA VW_STATE=()

vw_state_load() {
    local k v
    VW_STATE=()
    [ -r "$STATE" ] || return 0
    while IFS='=' read -r k v; do
        case "$k" in ''|'#'*) continue ;; esac
        VW_STATE[$k]=$v
    done < "$STATE"
}

vw_state_save() {
    local k tmp=$STATE.tmp
    {
        for k in "${!VW_STATE[@]}"; do printf '%s=%s\n' "$k" "${VW_STATE[$k]}"; done
    } > "$tmp"
    mv -f "$tmp" "$STATE"
}

vw_state_get() {
    # vw_state_get KEY [DEFAULT]
    printf '%s' "${VW_STATE[$1]:-${2:-}}"
}

# ---- events and alerts ------------------------------------------------------

vw_events_init() {
    # Event numbers are the line numbers of events.log (append-only).
    EV_SEQ=0
    if [ -r "$EVENTS" ]; then EV_SEQ=$(wc -l < "$EVENTS"); fi
    mkdir -p "$RUN_DIR/alerts"
}

vw_event() {
    # vw_event KEY EPOCH DETAIL [EVIDENCE...]
    # Appends "<epoch> <iso> <key> #<n> <detail>" to events.log and queues
    # evidence for the alert mail. No forks (called in loops).
    local key=$1 ep=${2%%.*} detail=$3 adir=$RUN_DIR/alerts iso e
    shift 3
    detail=${detail//$'\n'/ }
    EV_SEQ=$((EV_SEQ + 1))
    vw_iso_v iso "$ep"
    printf '%s %s %s #%s %s\n' "$ep" "$iso" "$key" "$EV_SEQ" "$detail" >> "$EVENTS"
    [ -s "$adir/$key.short" ] || printf '%s\n' "$detail" > "$adir/$key.short"
    {
        printf '#%s %s %s\n' "$EV_SEQ" "$iso" "$detail"
        for e in "$@"; do printf '    %s\n' "$e"; done
    } >> "$adir/$key.pending"
    printf '%s\n' "$EV_SEQ" >> "$adir/$key.count"
}

vw_process_alerts() {
    # Send at most one mail per key per ALERT_COOLDOWN. Events that arrive
    # during the cooldown are collected and sent as one summary afterwards.
    local adir=$RUN_DIR/alerts key last n short subj ids
    [ -d "$adir" ] || return 0
    for key in $N5VW_KEYS; do
        [ -s "$adir/$key.pending" ] || continue
        vw_rd "$adir/$key.last_sent" last
        last=${last:-0}
        if [ $((EPOCHSECONDS - last)) -lt "$ALERT_COOLDOWN" ]; then continue; fi
        n=$(wc -l < "$adir/$key.count")
        ids=$(sed 's/^/#/' "$adir/$key.count" | tr '\n' ' ')
        vw_rd "$adir/$key.short" short
        short=${short:0:80}
        subj="[n5-validation] ALERT $key $short"
        if [ "$n" -gt 1 ]; then subj="$subj (+$((n - 1)) weitere)"; fi
        {
            printf 'Lauf: %s auf %s\n' "$RUN_ID" "$HOST"
            printf 'Kriterium: %s - %s\n' "$key" "$(vw_key_desc "$key")"
            printf 'Ereignisse seit der letzten Meldung zu %s: %s\n' "$key" "$n"
            printf 'Ereignisnummern: %s\n' "${ids:0:400}"
            if [ "$last" -gt 0 ]; then
                printf 'Letzte Meldung zu %s: %s (Sperrfrist %s s)\n' "$key" "$(vw_iso "$last")" "$ALERT_COOLDOWN"
            fi
            printf '\nBelege (hoechstens 50 Zeilen, juengste zuletzt):\n\n'
            tail -n 50 "$adir/$key.pending"
            printf '\nGeplant/erklaert? Pro Ereignis annotieren:\n'
            printf '  /usr/local/lib/n5-validation/annotate.sh --event <Nr>[,<Nr>] "<Erklaerung>"\n'
            printf 'Offene Ereignisse: /usr/local/lib/n5-validation/annotate.sh --list\n'
            printf 'Stand: /usr/local/lib/n5-validation/status.sh\n'
        } | vw_mail "$subj"
        printf '%s\n' "$EPOCHSECONDS" > "$adir/$key.last_sent"
        : > "$adir/$key.pending"
        : > "$adir/$key.count"
        rm -f "$adir/$key.short"
    done
}

vw_key_desc() {
    case "$1" in
        K1) echo "Kernel-Log: Oops/BUG/Call Trace/hung task/soft lockup/RCU stall/watchdog/MCE/AER/NVMe-Reset/EC-Fehler" ;;
        K2) echo "boot_id gewechselt (Neustart, geplant oder nicht)" ;;
        K3) echo "n5-fangov nicht aktiv, NRestarts gestiegen oder Journal >= warning" ;;
        K4) echo "Luefter 0 RPM (2 Samples), Temperaturgrenze, EC-Lesefehler oder EC-hwmon fehlt" ;;
        K5) echo "Sampler-Luecke > ${GAP_MAX:-90} s, Uhrsprung rueckwaerts oder letztes Sample veraltet" ;;
        K6) echo "Datenverzeichnis > ${DATA_MAX_MB:-200} MB oder Root-FS < ${ROOT_MIN_FREE_GB:-5} GB frei" ;;
        K7) echo "Kernel-Taint seit Start veraendert" ;;
        K8) echo "Watcher selbst: Dienst fehlgeschlagen, Journal unlesbar, Abdeckung < ${COVER_MIN:-99} %" ;;
        *)  echo "?" ;;
    esac
}

# ---- mail -------------------------------------------------------------------

vw_mail() {
    # vw_mail SUBJECT  (body on stdin). Never fails the caller: on error the
    # message is queued in RUN_DIR/outbox and retried by vw_flush_outbox.
    local subj=$1 body msg f
    body=$(cat)
    # subject stays 7-bit: drop control and non-ASCII bytes
    subj=$(printf '%s' "$subj" | LC_ALL=C tr -cd '\040-\176')
    msg=$(printf 'To: %s\nFrom: root@%s\nSubject: %s\nMIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8\nContent-Transfer-Encoding: 8bit\n\n%s\n' \
        "$MAIL_TO" "$HOST" "$subj" "$body")
    if [ "$DRY_RUN" = 1 ]; then
        printf '===== DRY-RUN MAIL (not sent) =====\n%s\n===== END MAIL =====\n' "$msg"
        return 0
    fi
    if printf '%s\n' "$msg" | /usr/sbin/sendmail "$MAIL_TO" 2>/dev/null; then
        vw_log "mail sent: $subj"
        return 0
    fi
    mkdir -p "$RUN_DIR/outbox" 2>/dev/null || true
    f=$RUN_DIR/outbox/$EPOCHSECONDS-$$-$RANDOM.eml
    printf '%s\n' "$msg" > "$f" 2>/dev/null || vw_err "outbox not writable, mail lost: $subj"
    vw_err "sendmail failed, queued for retry: $subj"
    return 0
}

vw_flush_outbox() {
    local f
    [ "$DRY_RUN" = 1 ] && return 0
    [ -d "$RUN_DIR/outbox" ] || return 0
    for f in "$RUN_DIR"/outbox/*.eml; do
        [ -e "$f" ] || continue
        if /usr/sbin/sendmail "$MAIL_TO" < "$f" 2>/dev/null; then
            rm -f "$f"
            vw_log "queued mail sent: ${f##*/}"
        else
            vw_err "sendmail still failing, $(find "$RUN_DIR/outbox" -name '*.eml' | wc -l) mail(s) queued"
            return 0
        fi
    done
}

# ---- annotations ------------------------------------------------------------
#
# annotations.txt, one per line:
#   <ISO> #12,#15 <text>          explains exactly events 12 and 15 (any key)
#   <ISO> K2,K5 <text>            explains K2/K5 events within +-ANNOT_WINDOW
#                                 of <ISO>; never K1/K7 (ANNOT_EXPLICIT_ONLY)
#   <ISO> DOWN <ISO-end> <text>   planned downtime: excluded from the K8
#                                 coverage, explains K2/K5 events in
#                                 [<ISO>, <ISO-end> + ANNOT_WINDOW]

vw_annotations_parsed() {
    # print "EPOCH<TAB>TOKENS<TAB>EPOCH_END<TAB>RAW" per annotation line
    local ts tok rest ep ep2 raw
    [ -r "$ANNOT" ] || return 0
    while IFS= read -r raw; do
        read -r ts tok rest <<< "$raw"
        case "$ts" in ''|'#'*) continue ;; esac
        ep=$(date -d "$ts" +%s 2>/dev/null || true)
        if [ -z "$ep" ]; then vw_err "annotation with unparsable time ignored: $ts"; continue; fi
        ep2=-
        if [ "$tok" = DOWN ]; then
            ep2=$(date -d "${rest%% *}" +%s 2>/dev/null || true)
            if [ -z "$ep2" ]; then vw_err "DOWN annotation with unparsable end ignored: $raw"; continue; fi
        fi
        printf '%s\t%s\t%s\t%s\n' "$ep" "$tok" "$ep2" "$raw"
    done < "$ANNOT"
}

vw_event_table() {
    # vw_event_table MODE [SINCE_EPOCH]
    #   MODE=count   "Kx total explained unexplained" per key
    #   MODE=open    unexplained events (raw events.log lines)
    #   MODE=expl    explained events: "<event line>\n      <= <annotation>"
    local mode=$1 since=${2:-0}
    if [ ! -r "$EVENTS" ]; then
        if [ "$mode" = count ]; then
            local k; for k in $N5VW_KEYS; do echo "$k 0 0 0"; done
        fi
        return 0
    fi
    vw_annotations_parsed | awk -v mode="$mode" -v since="$since" -v win="$ANNOT_WINDOW" \
        -v keys="$N5VW_KEYS" -v xonly=" $ANNOT_EXPLICIT_ONLY " '
        FILENAME == "-" {
            split($0, a, "\t"); na++
            aep[na] = a[1] + 0; araw[na] = a[4]
            if (a[2] == "DOWN") { adown[na] = 1; aend[na] = a[3] + 0; next }
            m = split(a[2], tk, ",")
            for (j = 1; j <= m; j++) {
                if (tk[j] ~ /^#[0-9]+$/) ev[substr(tk[j], 2) + 0] = na
                else akeys[na] = akeys[na] "," tk[j]
            }
            akeys[na] = akeys[na] ","
            next
        }
        $1 + 0 < since { next }
        {
            key = $3; id = $4; sub(/^#/, "", id); id += 0
            t[key]++
            by = 0
            if (id in ev) by = ev[id]
            if (!by && index(xonly, " " key " ") == 0) {
                for (i = 1; i <= na; i++) {
                    if (adown[i]) {
                        if ((key == "K2" || key == "K5") && $1 + 0 >= aep[i] && $1 + 0 <= aend[i] + win) { by = i; break }
                        continue
                    }
                    d = aep[i] - $1; if (d < 0) d = -d
                    if (d <= win && index(akeys[i], "," key ",") > 0) { by = i; break }
                }
            }
            if (by) {
                e[key]++
                if (mode == "expl") printf "%s\n      <= %s\n", $0, araw[by]
            } else if (mode == "open") print
        }
        END {
            if (mode != "count") exit
            n = split(keys, k, " ")
            for (i = 1; i <= n; i++) printf "%s %d %d %d\n", k[i], t[k[i]], e[k[i]], t[k[i]] - e[k[i]]
        }' - "$EVENTS"
}

# ---- coverage (K8) ----------------------------------------------------------

vw_down_intervals() {
    # "from:to from:to ..." of DOWN annotations
    local ep tok ep2 _raw out=
    while IFS=$'\t' read -r ep tok ep2 _raw; do
        [ "$tok" = DOWN ] && out="$out $ep:$ep2"
    done < <(vw_annotations_parsed)
    printf '%s' "${out# }"
}

vw_coverage() {
    # vw_coverage FILE FIELD INTERVAL GAPMAX FROM TO DOWN
    # Reads epochs (column FIELD, comma separated; header lines skipped) and
    # prints "<coverage %> <uncovered s> <points>". A gap between two points
    # (or FROM/TO and the nearest point) larger than GAPMAX counts as
    # uncovered for (gap - INTERVAL) seconds, minus the overlap with planned
    # downtime (DOWN intervals).
    local file=$1 field=$2 iv=$3 gmax=$4 from=$5 to=$6 down=$7
    if [ ! -r "$file" ]; then
        awk -v f="$from" -v t="$to" 'BEGIN { s = t - f; printf "%.2f %d 0\n", (s > 0 ? 0 : 100), (s > 0 ? s : 0) }'
        return 0
    fi
    awk -F, -v fld="$field" -v iv="$iv" -v gmax="$gmax" -v from="$from" -v to="$to" -v down="$down" '
        function gap(a, b,   g, ov, i, lo, hi) {
            g = b - a
            if (g <= gmax) return
            ov = 0
            for (i = 1; i <= nd; i++) {
                lo = (a > d1[i]) ? a : d1[i]; hi = (b < d2[i]) ? b : d2[i]
                if (hi > lo) ov += hi - lo
            }
            g = g - iv - ov
            if (g > 0) unc += g
        }
        BEGIN {
            nd = split(down, dd, " ")
            for (i = 1; i <= nd; i++) { split(dd[i], p, ":"); d1[i] = p[1] + 0; d2[i] = p[2] + 0 }
            prev = from
        }
        $fld !~ /^[0-9]+$/ { next }
        { ep = $fld + 0; if (ep < from || ep > to) next; pts++; if (ep > prev) gap(prev, ep); prev = ep }
        END {
            gap(prev, to)
            span = to - from
            cov = (span > 0) ? 100 * (1 - unc / span) : 100
            if (cov < 0) cov = 0
            printf "%.2f %d %d\n", cov, unc, pts
        }' "$file"
}
