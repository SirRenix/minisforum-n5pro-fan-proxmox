#!/usr/bin/env bash
# Phase 5 — Modul READ-ONLY laden. experimental_write wird NICHT gesetzt,
# d.h. das N5-Pro-Profil exportiert nur Temperaturen und RPM, keine PWM-Writes.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
KO="$ROOT/build/minisforum_n5_it5571.ko"
OUT="$ROOT/befunde/raw"
mkdir -p "$OUT"
STAMP="$(date +%Y%m%d-%H%M%S)"
LOG="$OUT/05-load-$STAMP.txt"

[[ $EUID -eq 0 ]] || { echo "Bitte als root ausfuehren."; exit 1; }
[[ -f "$KO" ]] || { echo "Modul fehlt. Erst scripts/04-build-module.sh."; exit 1; }

FORCE=""
[[ "${1:-}" == "--force" ]] && FORCE="force=1"

exec > >(tee "$LOG") 2>&1
echo "=== 05-load-readonly  $(date -Is) ==="

# Kein dmesg -C: der Ring-Buffer eines Produktivhosts wird nicht geloescht.
# Stattdessen Zeitmarke setzen und danach nur neuere Zeilen zeigen.
echo "n5pro-ec: 05-load-readonly start $STAMP" > /dev/kmsg
MARK="n5pro-ec: 05-load-readonly start $STAMP"
dmesg_since() { dmesg | sed -n "/$MARK/,\$p"; }

echo "--- insmod $FORCE ---"
if ! insmod "$KO" $FORCE; then
    echo
    echo "Laden fehlgeschlagen. Kernel-Meldungen:"
    dmesg_since | tail -20
    cat <<'EOF'

Haeufige Ursachen:
  -ENODEV  DMI-Match greift nicht -> mit --force erneut versuchen (read-only)
           oder n5_dmi_table[] patchen, siehe docs/DMI-PATCH.md
  -EBUSY   0x62/0x66 sind von acpi_ec reserviert. Pruefen mit
           grep -E '0062|0066' /proc/ioports
           In dem Fall bleibt vorerst nur der Userspace-Pfad aus Phase 3.
EOF
    exit 1
fi

echo "geladen."
echo
echo "--- dmesg (seit Zeitmarke) ---"
dmesg_since | grep -i minisforum || dmesg_since | tail -15

echo
echo "--- hwmon-Knoten ---"
HW=""
for h in /sys/class/hwmon/hwmon*; do
    [[ -e "$h/name" ]] || continue
    if [[ "$(cat "$h/name")" == "minisforum_n5_it5571" ]]; then HW="$h"; fi
done

if [[ -z "$HW" ]]; then
    echo "Kein hwmon-Knoten des Treibers gefunden."
    exit 1
fi

echo "Pfad: $HW"
for f in "$HW"/temp*_label "$HW"/temp*_input "$HW"/fan*_label \
         "$HW"/fan*_input "$HW"/pwm* ; do
    [[ -e "$f" ]] || continue
    printf '  %-24s = %s\n' "$(basename "$f")" "$(cat "$f" 2>/dev/null || echo '<n/a>')"
done

echo
if ls "$HW"/pwm[1-4] >/dev/null 2>&1; then
    echo "HINWEIS: pwm-Knoten sind sichtbar. Trotzdem in dieser Phase NICHT beschreiben."
else
    echo "pwm-Knoten sind wie erwartet ausgeblendet (experimentelles Profil, read-only)."
fi

echo
echo "--- Plausibilitaet gegen Referenzsensoren ---"
command -v sensors >/dev/null && sensors 2>/dev/null | grep -i -A3 k10temp || true

cat <<EOF

Modul entladen mit:  scripts/99-restore.sh
Log: $LOG
EOF
