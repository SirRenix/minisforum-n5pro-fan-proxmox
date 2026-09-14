#!/usr/bin/env bash
# NOTFALL / AUFRAEUMEN — gibt die Luefter an die EC/BIOS-Automatik zurueck.
set -uo pipefail

[[ $EUID -eq 0 ]] || { echo "Bitte als root ausfuehren."; exit 1; }

echo "=== 99-restore  $(date -Is) ==="

HW=""
for h in /sys/class/hwmon/hwmon*; do
    [[ -e "$h/name" ]] || continue
    [[ "$(cat "$h/name")" == "minisforum_n5_it5571" ]] && HW="$h"
done

if [[ -n "$HW" ]]; then
    echo "--- Alle PWM-Kanaele auf Vollgas, bevor entladen wird ---"
    for i in 1 2 3 4; do
        [[ -w "$HW/pwm$i" ]] && { echo 255 > "$HW/pwm$i" 2>/dev/null && echo "  pwm$i=255"; }
    done
    sleep 2
fi

echo "--- Modul entladen ---"
# Kein 'lsmod | grep -q': grep -q schliesst die Pipe, lsmod bekommt SIGPIPE,
# unter pipefail gilt die Pipeline dann als fehlgeschlagen.
if [[ -d /sys/module/minisforum_n5_it5571 ]]; then
    rmmod minisforum_n5_it5571 && echo "  entladen"
else
    echo "  war nicht geladen"
fi

echo
echo "--- Kontrolle ---"
if [[ -d /sys/module/minisforum_n5_it5571 ]]; then
    echo "  WARNUNG: noch geladen"
else
    echo "  sauber"
fi
grep -E '006[268c]' /proc/ioports && echo "  WARNUNG: Ports noch reserviert" || echo "  Ports 0x62/0x66/0x68/0x6c frei"
dmesg | grep -i minisforum | tail -5

cat <<'EOF'

Der Treiber stellt beim Entladen die EC-Automatik fuer alle Kanaele wieder her,
die er angefasst hat. Falls ein Luefter danach weiterhin falsch laeuft:
Kaltstart (vollstaendig aus, kurz warten, wieder an) — dann initialisiert der
EC seine Default-Kurve neu. Ein warmer Reboot reicht dafuer nicht immer.
EOF
