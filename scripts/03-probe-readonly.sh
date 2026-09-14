#!/usr/bin/env bash
# Phase 3 — Read-only-Probe des IT5571 EC.
# Liest ausschliesslich. Kein einziger Schreibzugriff.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BIN="$ROOT/bin"
OUT="$ROOT/befunde/raw"
mkdir -p "$OUT"
STAMP="$(date +%Y%m%d-%H%M%S)"
LOG="$OUT/03-probe-$STAMP.txt"

[[ $EUID -eq 0 ]] || { echo "Bitte als root ausfuehren."; exit 1; }
[[ -x "$BIN/n5_fan" ]] || { echo "Tools fehlen. Erst scripts/02-build-tools.sh."; exit 1; }

exec > >(tee "$LOG") 2>&1

echo "=== 03-probe-readonly  $(date -Is) ==="
echo "Hinweis: Userspace greift hier per ioperm direkt auf die Ports zu und"
echo "umgeht damit die Kernel-Reservierung durch acpi_ec. Ein Aufruf pro Zeit,"
echo "keine Dauerschleifen parallel zu anderen EC-Nutzern."

echo
echo "--- Super-I/O ---"
"$BIN/sio_probe" || echo "sio_probe fehlgeschlagen"

echo
echo "--- PMC-Discovery ---"
"$BIN/n5_fan" probe || echo "n5_fan probe fehlgeschlagen"

echo
echo "--- Lüfterstatus (PWM-Sollwert + TACH1-3) ---"
"$BIN/n5_fan" status || echo "n5_fan status fehlgeschlagen"

echo
echo "--- EC-RAM Dump #1 ---"
"$BIN/ec_probe" dump || echo "ec_probe dump fehlgeschlagen"

echo
echo "--- Erwartete Temperaturbytes einzeln ---"
for reg in 0x09 0x04 0x05 0x06 0x34; do
    printf '  EC[%s] = ' "$reg"
    "$BIN/ec_probe" read "$reg" 2>&1 | tail -1
done
echo "  (0x09 CPU, 0x04 System, 0x05 Board, 0x06 Ambient, 0x34 PWM-Sollwert in %)"

echo
echo "--- 60 s Last auf einem Kern, danach zweiter Dump ---"
echo "Zweck: welche Bytes bewegen sich? Bestaetigt die Offset-Zuordnung."
timeout 60 bash -c 'while :; do :; done' &
LOADPID=$!
sleep 60
wait $LOADPID 2>/dev/null || true

echo
echo "--- Lüfterstatus nach Last ---"
"$BIN/n5_fan" status || true

echo
echo "--- EC-RAM Dump #2 ---"
"$BIN/ec_probe" dump || true

echo
echo "--- Referenzwerte zum Abgleich ---"
command -v sensors >/dev/null && sensors 2>/dev/null | grep -i -A3 k10temp || true
for d in /dev/nvme?n1; do
    [[ -e "$d" ]] || continue
    command -v smartctl >/dev/null && \
      echo "$d: $(smartctl -A "$d" 2>/dev/null | grep -i -m1 'Temperature:')"
done

cat <<EOF

=== Auswertung, die jetzt ansteht ===
1. Sind die Werte aus 0x09/0x04/0x05/0x06 plausibel (20-90 °C)?
2. Folgt 0x09 der k10temp-Kurve zwischen Dump 1 und Dump 2?
3. Liefern TACH1-3 Werte im Bereich 400-6000 RPM, und steigen sie unter Last?
4. Welche weiteren Bytes haben sich zwischen den Dumps geaendert?

Ergebnis in befunde/BEFUNDE.md eintragen. Log: $LOG
EOF
