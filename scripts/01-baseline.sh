#!/usr/bin/env bash
# Phase 1 — Baseline erfassen. Reiner Lesezugriff, kein EC-Zugriff.
set -euo pipefail

OUT="$(cd "$(dirname "$0")/.." && pwd)/befunde/raw"
mkdir -p "$OUT"
STAMP="$(date +%Y%m%d-%H%M%S)"
LOG="$OUT/01-baseline-$STAMP.txt"

[[ $EUID -eq 0 ]] || { echo "Bitte als root ausfuehren."; exit 1; }

exec > >(tee "$LOG") 2>&1

echo "=== 01-baseline  $(date -Is) ==="

echo
echo "--- Kernel / Distribution ---"
uname -a
cat /etc/os-release | grep -E '^(PRETTY_NAME|VERSION)='
pveversion 2>/dev/null || echo "pveversion nicht vorhanden"

echo
echo "--- DMI (entscheidend fuer das DMI-Match des Treibers) ---"
for f in sys_vendor product_name product_version board_vendor board_name \
         board_version bios_vendor bios_version bios_date; do
    printf '%-16s = [%s]\n' "$f" "$(cat "/sys/class/dmi/id/$f" 2>/dev/null || echo '<n/a>')"
done
echo
echo "Treiber erwartet exakt: product_name=[N5 PRO]  board_name=[F8NAA]"

echo
echo "--- Port-Belegung 0x62/0x66 (ACPI EC) und 0x68/0x6c (PMC2) ---"
grep -inE '006[0-9a-f]|0062|0066|0068|006c|002e' /proc/ioports || true
echo
echo "Vollstaendige ioports-Zeilen im Bereich 0x00-0xff:"
# mawk (Debian/PVE-Default) kennt kein strtonum(); Bereich per Praefix filtern.
grep -E '^ *00[0-9a-f]{2}-' /proc/ioports
echo
echo "ACPI-EC-Geraet (PNP0C09) status: $(cat /sys/bus/acpi/devices/PNP0C09:00/status 2>/dev/null || echo '<kein PNP0C09:00>')"
echo "  (0 = in der DSDT deaktiviert -> acpi_ec reserviert 0x62/0x66 nicht)"

echo
echo "--- ACPI EC Treiberstatus ---"
ls -l /sys/bus/acpi/devices/ 2>/dev/null | grep -i -E 'PNP0C09|EC' || echo "kein PNP0C09 gefunden"
dmesg 2>/dev/null | grep -i -E 'ACPI: EC|acpi_ec|embedded controller' | tail -20 || true

echo
echo "--- Bereits geladene hwmon-/Sensor-Module ---"
lsmod | grep -E 'it87|nct6|coretemp|k10temp|acpi_power|hwmon|minisforum' || echo "keine relevanten Module"

echo
echo "--- Vorhandene hwmon-Knoten ---"
for h in /sys/class/hwmon/hwmon*; do
    [[ -e "$h/name" ]] || continue
    printf '%s -> %s\n' "$h" "$(cat "$h/name")"
    ls "$h" | tr '\n' ' ' | sed 's/^/    /'; echo
done

echo
echo "--- sensors (falls installiert) ---"
command -v sensors >/dev/null && sensors || echo "lm-sensors nicht installiert (apt install lm-sensors)"

echo
echo "--- Referenztemperaturen fuer den spaeteren Plausibilitaetscheck ---"
echo "[NVMe]"
for d in /dev/nvme?n1; do
    [[ -e "$d" ]] || continue
    command -v smartctl >/dev/null && \
      echo "$d: $(smartctl -A "$d" 2>/dev/null | grep -i -m1 'Temperature:' || echo '?')"
done
echo "[SATA]"
for d in /dev/sd?; do
    [[ -e "$d" ]] || continue
    command -v smartctl >/dev/null && \
      echo "$d: $(smartctl -A -d sat "$d" 2>/dev/null | grep -i -m1 'Temperature_Celsius' || echo '?')"
done
echo "[CPU]"
command -v sensors >/dev/null && sensors 2>/dev/null | grep -i -A2 k10temp || true

echo
echo "--- Header fuer den Modulbau ---"
dpkg -l | grep -E 'proxmox-headers|pve-headers|linux-headers' | awk '{print "  " $2, $3}' || echo "KEINE Header installiert"
ls -d "/lib/modules/$(uname -r)/build" 2>/dev/null || echo "FEHLT: /lib/modules/$(uname -r)/build"

echo
echo "=== fertig. Log: $LOG ==="
