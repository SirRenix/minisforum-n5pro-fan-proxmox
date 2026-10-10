#!/bin/bash
# uninstall.sh - stop and remove the n5-validation watcher.
#
# usage: sudo ./uninstall.sh [--purge]
#   default  disables timers, removes units, drop-ins, scripts and
#            /etc/n5-validation.conf (a copy of the config stays in the run
#            directory as config.snapshot; install.sh with the same run id
#            continues that run); keeps all data
#   --purge  additionally deletes the data root (all runs!)
set -euo pipefail

purge=0
case "${1:-}" in
    '') ;;
    --purge) purge=1 ;;
    *) echo "usage: $0 [--purge]" >&2; exit 2 ;;
esac
if [ "$(id -u)" -ne 0 ]; then echo "must run as root" >&2; exit 1; fi

data_root=/var/lib/n5-validation
if [ -r /etc/n5-validation.conf ]; then
    v=$(sed -n 's/^DATA_ROOT=//p' /etc/n5-validation.conf | tail -n 1)
    v=${v#\'}; v=${v%\'}
    [ -n "$v" ] && data_root=$v
fi

systemctl disable --now n5vw-sample.timer n5vw-check.timer n5vw-report.timer 2>/dev/null || true
systemctl stop n5vw-sample.service n5vw-check.service n5vw-report.service 2>/dev/null || true
for u in n5vw-sample n5vw-check n5vw-report; do
    rm -f "/etc/systemd/system/$u.service" "/etc/systemd/system/$u.timer"
    rm -rf "/etc/systemd/system/$u.service.d"
done
rm -f "/etc/systemd/system/n5vw-failmail@.service"
systemctl daemon-reload
systemctl reset-failed 'n5vw-*' 2>/dev/null || true
rm -rf /usr/local/lib/n5-validation /run/n5vw-failmail
rm -f /etc/n5-validation.conf
if [ "$purge" -eq 1 ]; then
    case "$data_root" in
        /var/lib/n5-validation|*/n5-validation) rm -rf "$data_root"; echo "removed, data purged ($data_root)" ;;
        *) echo "removed; NOT purging unexpected data root $data_root - delete it by hand" >&2 ;;
    esac
else
    echo "removed, data kept in $data_root"
fi
