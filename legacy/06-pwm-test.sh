#!/usr/bin/env bash
# Phase 6 — PWM-Test, EIN Kanal, schreibend.
#
# Validierung ist messtechnisch: bei jedem Schritt werden ALLE drei Tachos
# protokolliert. Nur der Tacho des beschriebenen Kanals darf sich bewegen.
# Bewegt sich ein anderer, ist die Kanalzuordnung des Profils falsch.
# pwm4 (PCIe) hat keinen Tacho: dort ist der Befund "kein Tacho aendert sich".
#
# Ablauf: 100 % -> 85 % -> 70 % -> 55 %, je 45 s, mit Temperaturueberwachung.
# Bei Abbruch (Ctrl-C), Fehler, Temperaturgrenze oder Stillstand irgendeines
# Luefters wird automatisch die EC-Automatik wiederhergestellt und danach
# geprueft, ob die Drehzahlen auf das Ausgangsniveau zurueckkehren.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KO="$ROOT/build/minisforum_n5_it5571.ko"
BIN="$ROOT/bin"
OUT="$ROOT/befunde/raw"
mkdir -p "$OUT"
STAMP="$(date +%Y%m%d-%H%M%S)"

CH="${1:-}"
MAXTEMP="${MAXTEMP:-85}"   # °C, Abbruchschwelle CPU
MAXNVME="${MAXNVME:-70}"   # °C, Abbruchschwelle NVMe
TOL="${TOL:-150}"          # RPM, Toleranz fuer "zurueck auf Ausgangsniveau"

usage() {
cat <<'EOF'
Aufruf:  06-pwm-test.sh <1|2|3|4>

  1 = CPU-Luefter        (TACH1)
  2 = SSD-Luefter        (TACH2)
  3 = HDD-Luftergruppe   (TACH3, zwei Luefter an DCR3+DCR4)
  4 = PCIe-Luefter       (kein Tacho)

Reihenfolge: 4 (risikoaermster Einstieg), dann 1, 2, zuletzt 3.
EOF
}

[[ "$CH" =~ ^[1-4]$ ]] || { usage; exit 2; }
[[ $EUID -eq 0 ]] || { echo "Bitte als root ausfuehren."; exit 1; }
[[ -f "$KO" ]] || { echo "Modul fehlt. Erst scripts/04-build-module.sh."; exit 1; }
[[ -x "$BIN/n5_fan" ]] || { echo "bin/n5_fan fehlt. Erst scripts/02-build-tools.sh."; exit 1; }

LOG="$OUT/06-pwmtest-ch$CH-$STAMP.txt"
exec > >(tee "$LOG") 2>&1

cat <<EOF
=== 06-pwm-test  Kanal $CH  $(date -Is) ===

Dieses Skript SCHREIBT in den EC. Es kann einen Luefter verlangsamen oder
stoppen. Auf diesem Host liegen produktive ZFS-Pools.

Voraussetzungen, die du jetzt bestaetigst:
  - Wartungsfenster: keine Scrubs, Backups oder grossen Transfers
  - kein anderer Regler (fancontrol o. ae.) aktiv
  - Rueckfallweg zum Geraet bekannt (Konsole/KVM), falls rmmod nicht reicht

EOF
read -r -p 'Tippe exakt  TEST STARTEN  zum Fortfahren: ' CONF
[[ "$CONF" == "TEST STARTEN" ]] || { echo "Abgebrochen."; exit 1; }

# --- Ausgangsdrehzahlen VOR dem Laden (EC-Automatik, Userspace-Tool) ---------
echo
echo "--- Ausgangszustand (EC-Automatik, vor dem Laden) ---"
BASE="$("$BIN/n5_fan" rpm)"
echo "$BASE"
B1="$(echo "$BASE" | awk '/TACH1/{print $2}')"
B2="$(echo "$BASE" | awk '/TACH2/{print $2}')"
B3="$(echo "$BASE" | awk '/TACH3/{print $2}')"
# I2EC-Snapshot der echten PWM-Register (IT5571: 0x1801 CTR, 0x1802-0x1809 DCR0-7).
# Ohne diesen Vergleichswert ist "EC-Automatik wiederhergestellt" nicht
# beweisbar — Lehre aus Kanal 3 am 14.09.2026.
if [[ -x "$BIN/n5_i2ec_read" ]]; then
    echo "I2EC 0x1800:"; "$BIN/n5_i2ec_read" 0x1800 16
fi

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
EN="$HW/pwm${CH}_enable"
[[ -w "$PWM" ]] || { echo "$PWM nicht beschreibbar."; rmmod minisforum_n5_it5571; exit 1; }

fan() { cat "$HW/fan${1}_input" 2>/dev/null || echo n/a; }

restore() {
    local rc=$?
    trap - EXIT INT TERM
    echo
    echo "--- Wiederherstellung: EC-Automatik ---"
    # pwmN_enable=2 ist der explizite Rueckgabepfad des Treibers
    # (n5_set_auto_locked). rmmod macht dasselbe fuer alle angefassten
    # Kanaele — doppelt haelt besser.
    if echo 2 > "$EN" 2>/dev/null; then
        echo "pwm${CH}_enable=2 (Automatik) gesetzt, Treiber meldet ok."
    else
        echo "!! pwm${CH}_enable=2 fehlgeschlagen — rmmod uebernimmt."
    fi
    sleep 2
    rmmod minisforum_n5_it5571 2>/dev/null || true
    echo "Modul entladen. Warte 20 s, dann Kontrolle gegen Ausgangsniveau (+-$TOL RPM):"
    sleep 20
    local A A1 A2 A3 ok=1
    A="$("$BIN/n5_fan" rpm)"
    echo "$A"
    if [[ -x "$BIN/n5_i2ec_read" ]]; then
        echo "I2EC 0x1800 (mit Ausgangs-Snapshot vergleichen — DCR des Kanals muss zurueck sein):"
        "$BIN/n5_i2ec_read" 0x1800 16
    fi
    A1="$(echo "$A" | awk '/TACH1/{print $2}')"
    A2="$(echo "$A" | awk '/TACH2/{print $2}')"
    A3="$(echo "$A" | awk '/TACH3/{print $2}')"
    for i in 1 2 3; do
        local b a d
        b="B$i"; a="A$i"; b="${!b}"; a="${!a}"
        d=$(( a > b ? a - b : b - a ))
        if (( d > TOL )); then
            echo "  !! TACH$i: $a RPM, Ausgang $b RPM (Delta $d) — NICHT zurueck."
            ok=0
        else
            echo "  TACH$i: $a RPM, Ausgang $b RPM (Delta $d) ok"
        fi
    done
    if (( ok )); then
        echo "EC-Automatik wiederhergestellt: JA"
    else
        echo "EC-Automatik wiederhergestellt: NEIN — beobachten, ggf. Kaltstart."
    fi
    echo "Log: $LOG"
    exit $rc
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
echo "Modul geladen mit experimental_write=1. Sichtbare pwm-Knoten:"
ls "$HW"/pwm* | xargs -n1 basename | tr '\n' ' '; echo
printf 'Ausgang (hwmon): pwm%s=%s pwm%s_enable=%s  fan1=%s fan2=%s fan3=%s  CPU=%s C  NVMe_max=%s C\n' \
    "$CH" "$(cat "$PWM")" "$CH" "$(cat "$EN")" "$(fan 1)" "$(fan 2)" "$(fan 3)" "$(cputemp)" "$(nvmetemp)"

# Treiber verlangt pwmN_enable=1 (manuell) vor jedem pwmN-Write, sonst -EBUSY.
# Der Wechsel auf 1 setzt intern zuerst 255 — das ist bereits der erste
# EC-Write dieses Kanals.
echo
echo "--- pwm${CH}_enable = 1 (manuell, startet bei 255) ---"
echo 1 > "$EN"
sleep 5
printf '  danach: pwm%s=%s enable=%s  fan1=%s fan2=%s fan3=%s\n' \
    "$CH" "$(cat "$PWM")" "$(cat "$EN")" "$(fan 1)" "$(fan 2)" "$(fan 3)"

for DUTY in 255 217 179 140; do
    PCT=$(( DUTY * 100 / 255 ))
    echo
    echo "--- pwm$CH = $DUTY (~${PCT}%) fuer 45 s ---"
    echo "$DUTY" > "$PWM"
    for i in $(seq 1 9); do
        sleep 5
        C="$(cputemp)"; N="$(nvmetemp)"
        F1="$(fan 1)"; F2="$(fan 2)"; F3="$(fan 3)"
        printf '  t+%02ds  fan1=%-5s fan2=%-5s fan3=%-5s  CPU=%s C  EC-CPU=%s C  NVMe=%s C\n' \
            $((i*5)) "$F1" "$F2" "$F3" "$C" "$(ectemp)" "$N"
        if [[ "$C" -ge "$MAXTEMP" ]]; then
            echo "  !! CPU >= ${MAXTEMP} C — Abbruch."; exit 1
        fi
        if [[ "$N" -ge "$MAXNVME" ]]; then
            echo "  !! NVMe >= ${MAXNVME} C — Abbruch."; exit 1
        fi
        for k in 1 2 3; do
            v="F$k"
            if [[ "${!v}" == "0" ]]; then
                echo "  !! fan$k steht (RPM=0) — Abbruch."; exit 1
            fi
        done
    done
done

echo
echo "Testreihe durchgelaufen. Auswertung anhand der Tacho-Spalten:"
echo "  - nur fan$CH darf sich zwischen den Stufen bewegt haben (bei Kanal 4: keiner)"
echo "  - bewegte sich ein anderer Tacho, ist die Zuordnung falsch"
echo "Ergebnis in befunde/BEFUNDE.md eintragen."
