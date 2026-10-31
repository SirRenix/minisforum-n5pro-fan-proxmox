#!/usr/bin/env bash
# NOTFALL / AUFRAEUMEN - stoppt den Regler, gibt die Luefter an die EC/BIOS-Automatik
# zurueck und entlaedt das Modul.
#   99-restore.sh            Regler bleibt danach gestoppt (Befehle zum Wiederanlauf
#                            stehen am Ende der Ausgabe)
#   99-restore.sh --restart  danach Modul wieder laden und den vorher aktiven Regler
#                            wieder starten (= sauberes Neuladen in der richtigen Reihenfolge)
# Reihenfolge: Regler stoppen -> pwmN_enable=2 -> rmmod. Nie rmmod bei laufendem
# Regler: das Modul haelt keine Referenz, ein Write waehrend remove() kann in 0.2.1
# auf bereits freigegebene Ports gehen.
set -uo pipefail

[[ $EUID -eq 0 ]] || { echo "Bitte als root ausfuehren."; exit 1; }

RESTART=0
case "${1:-}" in
    "") ;;
    --restart) RESTART=1 ;;
    *) echo "Aufruf: $0 [--restart]"; exit 1 ;;
esac

MOD=minisforum_n5_it5571
echo "=== 99-restore  $(date -Is) ==="

echo "--- Regler stoppen ---"
# n5-fangov (aktuell) und n5-fand (Vorgaenger, legacy/). ExecStopPost setzt dabei
# den Failsafe (CPU/SSD Automatik, HDD fester Wert); danach uebernimmt dieses Skript.
# Jede installierte Unit wird gestoppt, nicht nur "active": n5-fangov hat
# Restart=always/RestartSec=5; im Zustand "activating (auto-restart)" wuerde sie
# sonst zwischen pwmN_enable=2 und rmmod wieder anlaufen. Fuer --restart gemerkt
# wird nur, was lief oder gerade anlief.
STOPPED=()
for u in n5-fangov.service n5-fand.service; do
    [[ "$(systemctl show -p LoadState --value "$u" 2>/dev/null)" == loaded ]] || continue
    st=$(systemctl is-active "$u" 2>/dev/null)
    if systemctl stop "$u"; then
        case "$st" in
            active|activating|reloading)
                echo "  $u gestoppt (war $st)"
                STOPPED+=("$u") ;;
        esac
    else
        echo "  $u stoppen FEHLGESCHLAGEN - Abbruch, Modul bleibt geladen."
        exit 1
    fi
done
(( ${#STOPPED[@]} )) || echo "  kein Regler aktiv"

HW=""
for h in /sys/class/hwmon/hwmon*; do
    [[ -e "$h/name" ]] || continue
    [[ "$(cat "$h/name")" == "$MOD" ]] && HW="$h"
done

if [[ -n "$HW" ]]; then
    echo "--- Alle PWM-Kanaele explizit an die EC-Automatik zurueckgeben ---"
    # pwmN_enable=2 -> n5_set_auto_locked(). Ein direktes 'echo 255 > pwmN'
    # scheitert im Automatikmodus mit -EBUSY.
    for i in 1 2 3 4; do
        [[ -w "$HW/pwm${i}_enable" ]] || continue
        if echo 2 > "$HW/pwm${i}_enable" 2>/dev/null; then
            echo "  pwm${i}_enable=2 ok"
        else
            echo "  pwm${i}_enable=2 FEHLGESCHLAGEN"
        fi
    done
    sleep 2
fi

echo "--- Modul entladen ---"
# Kein 'lsmod | grep -q': grep -q schliesst die Pipe, lsmod bekommt SIGPIPE,
# unter pipefail gilt die Pipeline dann als fehlgeschlagen.
if [[ -d /sys/module/$MOD ]]; then
    rmmod "$MOD" && echo "  entladen"
else
    echo "  war nicht geladen"
fi

echo
echo "--- Kontrolle ---"
if [[ -d /sys/module/$MOD ]]; then
    echo "  WARNUNG: noch geladen"
else
    echo "  sauber"
fi
grep -E '006[268c]' /proc/ioports && echo "  WARNUNG: Ports noch reserviert" || echo "  Ports 0x62/0x66/0x68/0x6c frei"
dmesg | grep -i minisforum | tail -5

if (( RESTART )); then
    echo
    echo "--- Wiederanlauf ---"
    if ! modprobe "$MOD"; then
        echo "  modprobe $MOD FEHLGESCHLAGEN - Regler bleibt gestoppt."
        exit 1
    fi
    echo "  Modul geladen"
    for u in "${STOPPED[@]}"; do
        if systemctl start "$u"; then
            echo "  $u gestartet"
        else
            echo "  $u starten FEHLGESCHLAGEN - systemctl status $u"
        fi
    done
elif (( ${#STOPPED[@]} )); then
    echo
    echo "Regler bleibt gestoppt. Wiederanlauf:"
    echo "  modprobe $MOD"
    for u in "${STOPPED[@]}"; do echo "  systemctl start $u"; done
fi

cat <<'EOF'

Der Treiber gibt beim Entladen jeden Kanal, den er angefasst hat, mit dem
Automatik-Befehl zurueck. CPU (0x21) regelt danach nachweislich; der HDD-Kanal
(0x2b) verlaesst den Schreibwert, ob eine EC-Kurve oder ein fester Default folgt,
ist offen; PCIe (0x31) bleibt auf dem letzten Schreibwert.
Laeuft ein Luefter danach falsch oder antwortet der EC nicht: vollstaendig
ausschalten (Strom trennen, bis das Geraet dunkel ist), dann einschalten. Ob ein
warmer Reboot den EC zuruecksetzt, ist nicht gemessen (TESTSPEC T6).
EOF
