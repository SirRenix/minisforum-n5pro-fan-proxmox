#!/bin/bash
# install.sh - install and start the n5-validation watcher (F8b).
#
# usage: sudo ./install.sh [--run-id ID] [--duration-days N] [--mail-to ADDR]
#                          [--data-root DIR] [--force]
#
# Idempotent: running it again with the same run id keeps the recorded start
# time, taint and boot baseline (the run continues), also after uninstall.sh
# (values come from <run dir>/config.snapshot then). A different run id starts
# a new run; this is refused while the previous run is unfinished unless
# --force. A run id whose run has finished (final.done) is always refused.
# The START mail is sent from inside n5vw-check.service, i.e. under the same
# sandbox as the alert mails.
set -euo pipefail

SRC=$(dirname "$(readlink -f "$0")")
LIB_DIR=/usr/local/lib/n5-validation
UNIT_DIR=/etc/systemd/system
CONF=/etc/n5-validation.conf
TIMERS=(n5vw-sample.timer n5vw-check.timer n5vw-report.timer)

run_id=baseline-20261008
duration=7
mail_to=root
data_root=/var/lib/n5-validation
force=0
while [ $# -gt 0 ]; do
    case "$1" in
        --run-id) run_id=$2; shift 2 ;;
        --duration-days) duration=$2; shift 2 ;;
        --mail-to) mail_to=$2; shift 2 ;;
        --data-root) data_root=${2%/}; shift 2 ;;
        --force) force=1; shift ;;
        -h|--help) sed -n '2,14p' "$0"; exit 0 ;;
        *) echo "unknown option: $1" >&2; exit 2 ;;
    esac
done
case "$run_id" in
    ''|*[!A-Za-z0-9._-]*) echo "invalid run id: $run_id" >&2; exit 2 ;;
esac
case "$duration" in
    ''|*[!0-9]*) echo "invalid duration: $duration" >&2; exit 2 ;;
esac
case "$mail_to" in
    ''|*[!A-Za-z0-9@._+-]*) echo "invalid mail address: $mail_to" >&2; exit 2 ;;
esac
# data root: absolute, plain characters (it goes into unit files), not on a
# volatile or system path
case "$data_root" in
    /*) ;;
    *) echo "data root must be absolute: $data_root" >&2; exit 2 ;;
esac
case "$data_root" in
    *[!A-Za-z0-9._/-]*|*/../*|*/..|*//*) echo "unsupported data root path: $data_root" >&2; exit 2 ;;
esac
case "$data_root/" in
    /|/tmp/*|/var/tmp/*|/run/*|/proc/*|/sys/*|/dev/*|/usr/*|/etc/*|/boot/*)
        echo "data root not supported (volatile or system path): $data_root" >&2; exit 2 ;;
esac
if [ "$(id -u)" -ne 0 ]; then echo "must run as root" >&2; exit 1; fi
if [ ! -d "$(dirname "$data_root")" ]; then
    echo "parent of data root does not exist (not mounted?): $(dirname "$data_root")" >&2; exit 1
fi
run_dir=$data_root/$run_id

if [ -e "$run_dir/final.done" ]; then
    echo "run '$run_id' has already finished ($(cat "$run_dir/final.done")); use a new run id" >&2
    exit 1
fi

getval() {
    # getval FILE KEY - last KEY=value of a generated config, %q-quoting undone
    local v
    v=$(sed -n "s/^$2=//p" "$1" | tail -n 1)
    v=${v#\'}; v=${v%\'}
    printf '%s' "$v"
}

# existing run?
start_epoch=; taint_start=; boot_start=; resumed=0
if [ -r "$CONF" ]; then
    old_run=$(getval "$CONF" RUN_ID)
    old_root=$(getval "$CONF" DATA_ROOT)
    if [ "$old_run" = "$run_id" ]; then
        if [ -n "$old_root" ] && [ "$old_root" != "$data_root" ]; then
            echo "run '$run_id' uses data root $old_root; moving a running run is not supported" >&2
            exit 1
        fi
        start_epoch=$(getval "$CONF" START_EPOCH)
        taint_start=$(getval "$CONF" TAINT_START)
        boot_start=$(getval "$CONF" BOOT_ID_START)
        resumed=1
    elif [ -n "$old_run" ] && [ ! -e "${old_root:-$data_root}/$old_run/final.done" ] && [ "$force" -ne 1 ]; then
        echo "run '$old_run' is not finished; use --force to start '$run_id' anyway" >&2
        exit 1
    fi
fi
if [ "$resumed" -eq 0 ] && [ -r "$run_dir/config.snapshot" ]; then
    # reinstall after uninstall.sh: continue the recorded run, do not reset it
    start_epoch=$(getval "$run_dir/config.snapshot" START_EPOCH)
    taint_start=$(getval "$run_dir/config.snapshot" TAINT_START)
    boot_start=$(getval "$run_dir/config.snapshot" BOOT_ID_START)
    resumed=1
fi
[ -n "$start_epoch" ] || start_epoch=$(date +%s)
[ -n "$taint_start" ] || taint_start=$(cat /proc/sys/kernel/tainted)
[ -n "$boot_start" ] || boot_start=$(cat /proc/sys/kernel/random/boot_id)

# stop the timers while scripts are replaced (a running oneshot keeps its
# already opened script file)
systemctl stop "${TIMERS[@]}" 2>/dev/null || true

install -d -m 0750 "$data_root" "$run_dir"
if ! touch "$run_dir/.write-test" 2>/dev/null; then
    echo "data root not writable: $run_dir" >&2; exit 1
fi
rm -f "$run_dir/.write-test"

install -d -m 0755 "$LIB_DIR"
install -m 0755 "$SRC/bin/n5vw-sample" "$SRC/bin/n5vw-check" "$SRC/bin/n5vw-report" \
    "$SRC/bin/n5vw-failmail" "$SRC/annotate.sh" "$SRC/status.sh" "$LIB_DIR/"
install -m 0644 "$SRC/bin/n5vw-lib.sh" "$SRC/README.md" "$LIB_DIR/"
install -m 0644 "$SRC"/systemd/n5vw-*.service "$SRC"/systemd/n5vw-*.timer "$UNIT_DIR/"
for u in n5vw-sample n5vw-check n5vw-report; do
    install -d -m 0755 "$UNIT_DIR/$u.service.d"
    printf '# written by install.sh - data root of run %s\n[Unit]\nRequiresMountsFor=%s\n\n[Service]\nReadWritePaths=%s\n' \
        "$run_id" "$data_root" "$data_root" > "$UNIT_DIR/$u.service.d/data-root.conf"
done

tmp=$(mktemp)
{
    echo "# /etc/n5-validation.conf - written by install.sh $(date -Iseconds)"
    echo "# Sourced by the n5vw-* scripts (bash). Thresholds: see README.md."
    printf 'RUN_ID=%q\n' "$run_id"
    printf 'DURATION_DAYS=%q\n' "$duration"
    printf 'START_EPOCH=%q\n' "$start_epoch"
    printf 'TAINT_START=%q\n' "$taint_start"
    printf 'BOOT_ID_START=%q\n' "$boot_start"
    printf 'MAIL_TO=%q\n' "$mail_to"
    printf 'DATA_ROOT=%q\n' "$data_root"
    sed -n '/^# --- thresholds/,$p' "$SRC/n5-validation.conf.example"
} > "$tmp"
install -m 0644 "$tmp" "$CONF"
rm -f "$tmp"
cp -f "$CONF" "$run_dir/config.snapshot"

modver=$(cat /sys/module/minisforum_n5_it5571/version 2>/dev/null || echo "?")
modsrc=$(cat /sys/module/minisforum_n5_it5571/srcversion 2>/dev/null || echo "?")
if [ "$resumed" -eq 1 ]; then note="erneut installiert, Lauf wird fortgesetzt"; else note="neuer Lauf"; fi
{
    printf 'Langzeitbeobachtung F8b gestartet (%s).\n\n' "$note"
    printf 'Lauf:      %s\n' "$run_id"
    printf 'Start:     %s\n' "$(date -d "@$start_epoch" -Iseconds)"
    printf 'Ende:      %s (%s Tage)\n' "$(date -d "@$((start_epoch + duration * 86400))" -Iseconds)" "$duration"
    printf 'Kernel:    %s, Taint-Basis %s\n' "$(uname -r)" "$taint_start"
    printf 'Treiber:   minisforum_n5_it5571 version=%s srcversion=%s\n' "$modver" "$modsrc"
    printf 'Daten:     %s (%s)\n' "$run_dir" "$(findmnt -n -o SOURCE,TARGET -T "$run_dir" 2>/dev/null | tr -s ' ' || echo '?')"
    printf 'Taeglicher Bericht 07:00. Fehlt er, laeuft der Watcher nicht.\n'
} > "$run_dir/startmail.pending"

systemctl daemon-reload
systemctl enable --now n5vw-sample.timer
# first check run synchronously: initialises state and sends the START mail
# from inside the sandboxed unit
rc=0
systemctl start n5vw-check.service || rc=$?
systemctl enable --now n5vw-check.timer n5vw-report.timer

result=$(systemctl show n5vw-check.service -p Result --value 2>/dev/null || echo '?')
queued=$(find "$run_dir/outbox" -name '*.eml' 2>/dev/null | wc -l)
echo "installed: run $run_id ($note), start $(date -d "@$start_epoch" -Iseconds), data $run_dir"
if [ "$rc" -ne 0 ] || [ "$result" != success ]; then
    echo "WARNING: first n5vw-check run failed (rc=$rc, Result=$result):" >&2
    journalctl -u n5vw-check.service -n 20 --no-pager >&2 || true
elif [ -e "$run_dir/startmail.pending" ]; then
    echo "WARNING: START mail not processed by n5vw-check" >&2
elif [ "$queued" -gt 0 ]; then
    echo "WARNING: sendmail failed inside the unit, $queued mail(s) queued in $run_dir/outbox" >&2
else
    echo "START mail handed to sendmail from inside n5vw-check.service - check that it arrives."
fi
systemctl list-timers "${TIMERS[@]}" --no-pager || true
