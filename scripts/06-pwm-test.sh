#!/usr/bin/env bash
# Phase 6 — PWM-Test, EIN Kanal, schreibend.
#
#   NUR im Wartungsfenster, NUR mit physischem Zugang zum Geraet,
#   NUR wenn du daneben sitzt und hoerst, was die Luefter machen.
#
# Ablauf: 100 % -> 85 % -> 70 % -> 55 %, je 45 s, mit Temperaturueberwachung.
# Bei Abbruch (Ctrl-C), Fehler oder Temperaturgrenze wird automatisch die
# EC-Automatik wiederhergestellt.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KO="$ROOT/build/minisforum_n5_it5571.ko"
OUT="$ROOT/befunde/raw"
mkdir -p "$OUT"
STAMP="$(date +%Y%m%d-%H%M%S)"

CH="${1:-}"
MAXTEMP="${MAXTEMP:-85}"   # °C, Abbruchschwelle CPU
MAXNVME="${MAXNVME:-70}"   # °C, Abbruchschwelle NVMe

usage() {
cat <<'EOF'
Aufruf:  06-pwm-test.sh <1|2|3|4>

  1 = CPU-Luefter
  2 = SSD-Luefter
  3 = HDD-Luftergruppe
  4 = PCIe-Luefter (kein Tacho, RPM bleibt 0)

Beginne mit Kanal 4, falls dort gar kein Luefter haengt — risikoaermster
Einstieg zur Verifikation, dass Schreibzugriffe ueberhaupt ankommen.
Danach Kanal 1. Kanal 3 zuletzt, der kuehlt die Platten.
EOF
}

[[ "$CH" =~ ^[1-4]$ ]] || { usage; exit 2; }
[[ $EUID -eq 0 ]] || { echo "Bitte als root ausfuehren."; exit 1; }
[[ -f "$KO" ]] || { echo "Modul fehlt. Erst scripts/04-build-module.sh."; exit 1; }

LOG="$OUT/06-pwmtest-ch$CH-$STAMP.txt"
exec > >(tee "$LOG") 2>&1

cat <<EOF
=== 06-pwm-test  Kanal $CH  $(date -Is) ===

Dieses Skript SCHREIBT in den EC. Es kann einen Luefter verlangsamen oder
stoppen. Auf diesem Host liegen produktive ZFS-Pools.

Voraussetzungen, die du jetzt bestaetigst:
  - physischer Zugang zum Geraet vorhanden
  - Wartungsfenster, keine laufenden Scrubs oder grossen Transfers
  - du bleibst waehrend des Tests am Geraet

EOF
read -r -p 'Tippe exakt  ICH BIN AM GERAET  zum Fortfahren: ' CONF
[[ "$CONF" == "ICH BIN AM GERAET" ]] || { echo "Abgebrochen."; exit 1; }

# --- Modul mit Schreibfreigabe neu laden -------------------------------------
rmmod minisforum_n5_it5571 2>/dev/null || true
insmod "$KO" experimental_write=1 || {
    echo "Laden mit experimental_write=1 fehlgeschlagen."; dmesg | tail -15; exit 1; }

HW=""
for h in /sys/class/hwmon/hwmon*; do
    [[ -e "$h/name" ]] || continue
    [[ "$(cat "$h/name")" == "minisforum_n5_it5571" ]] && HW="$h"
done
[[ -n "$HW" ]] || { echo "hwmon-Knoten nicht gefunden."; rmmod minisforum_n5_it5571; exit 1; }

PWM="$HW/pwm$CH"
FAN="$HW/fan${CH}_input"
[[ -w "$PWM" ]] || { echo "$PWM nicht beschreibbar."; rmmod minisforum_n5_it5571; exit 1; }

restore() {
    echo
    echo "--- Wiederherstellung: EC-Automatik ---"
    echo 255 > "$PWM" 2>/dev/null || true
    sleep 2
    rmmod minisforum_n5_it5571 2>/dev/null || true
    echo "Modul entladen, EC/BIOS hat wieder die Kontrolle."
    echo "Log: $LOG"
}
trap restore EXIT INT TERM

# Abbruchschwelle unabhaengig vom Pruefling: k10temp (Tctl) ist Referenz,
# temp1_input des Testmoduls nur Fallback.
K10=""
for h in /sys/class/hwmon/hwmon*; do
    [[ -e "$h/name" ]] || continue
    [[ "$(cat "$h/name")" == "k10temp" ]] && K10="$h"
done
cputemp() {
    local t
    if [[ -n "$K10" && -r "$K10/temp1_input" ]]; then
        t="$(cat "$K10/temp1_input" 2>/dev/null || echo 0)"
    else
        t="$(cat "$HW/temp1_input" 2>/dev/null || echo 0)"
    fi
    echo $(( t / 1000 ))
}
ectemp() {
    local t
    t="$(cat "$HW/temp1_input" 2>/dev/null || echo 0)"
    echo $(( t / 1000 ))
}
nvmetemp() {
    local m=0 v
    for d in /dev/nvme?n1; do
        [[ -e "$d" ]] || continue
        v="$(smartctl -A "$d" 2>/dev/null | awk -F: '/^Temperature:/{gsub(/[^0-9]/,"",$2); print $2; exit}')"
        [[ -n "${v:-}" && "$v" -gt "$m" ]] && m="$v"
    done
    echo "$m"
}

echo
echo "Ausgangszustand:"
printf '  pwm%s=%s  fan%s=%s RPM  CPU=%s C  NVMe_max=%s C\n' \
    "$CH" "$(cat "$PWM")" "$CH" "$(cat "$FAN" 2>/dev/null || echo n/a)" \
    "$(cputemp)" "$(nvmetemp)"

for DUTY in 255 217 179 140; do
    PCT=$(( DUTY * 100 / 255 ))
    echo
    echo "--- pwm$CH = $DUTY (~${PCT}%) fuer 45 s ---"
    echo "$DUTY" > "$PWM"
    for i in $(seq 1 9); do
        sleep 5
        C="$(cputemp)"; N="$(nvmetemp)"
        R="$(cat "$FAN" 2>/dev/null || echo n/a)"
        printf '  t+%02ds  RPM=%-6s CPU(k10temp)=%s C  EC-CPU=%s C  NVMe=%s C\n' \
            $((i*5)) "$R" "$C" "$(ectemp)" "$N"
        if [[ "$C" -ge "$MAXTEMP" ]]; then
            echo "  !! CPU >= ${MAXTEMP} C — Abbruch."; exit 1
        fi
        if [[ "$N" -ge "$MAXNVME" ]]; then
            echo "  !! NVMe >= ${MAXNVME} C — Abbruch."; exit 1
        fi
        if [[ "$CH" != "4" && "$R" == "0" ]]; then
            echo "  !! Luefter steht (RPM=0) — Abbruch."; exit 1
        fi
    done
done

echo
echo "Testreihe durchgelaufen. Beobachtet:"
echo "  - hat sich die Drehzahl hoerbar/messbar geaendert?"
echo "  - war es der erwartete Luefter?"
echo "Ergebnis in befunde/BEFUNDE.md eintragen."
