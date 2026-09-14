#!/usr/bin/env bash
# Phase 4 — Kernelmodul gegen den laufenden PVE-Kernel bauen. Laedt nichts.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/upstream/driver-prototype"
BUILD="$ROOT/build"
KREL="$(uname -r)"

[[ -d "$SRC" ]] || { echo "Repo fehlt. Erst scripts/02-build-tools.sh."; exit 1; }

if [[ ! -d "/lib/modules/$KREL/build" ]]; then
    echo "Kernel-Header fehlen fuer $KREL."
    echo "  apt install -y pve-headers-$KREL build-essential"
    exit 1
fi

rm -rf "$BUILD"
mkdir -p "$BUILD"
cp "$SRC/minisforum_n5_it5571.c" "$SRC/Makefile" "$BUILD/"

# --- DMI-Match pruefen -------------------------------------------------------
PROD="$(cat /sys/class/dmi/id/product_name 2>/dev/null || true)"
BOARD="$(cat /sys/class/dmi/id/board_name 2>/dev/null || true)"
echo "DMI dieses Systems: product_name=[$PROD] board_name=[$BOARD]"

if grep -q "\"$PROD\"" "$BUILD/minisforum_n5_it5571.c" && \
   grep -q "\"$BOARD\"" "$BUILD/minisforum_n5_it5571.c"; then
    echo "-> DMI-Strings sind in n5_dmi_table[] enthalten. Kein Patch noetig."
else
    echo "-> ACHTUNG: [$PROD]/[$BOARD] steht NICHT in n5_dmi_table[]."
    echo "   Der Probe wird mit -ENODEV scheitern."
    echo "   Zwei Optionen:"
    echo "     a) sauber: einen Eintrag in n5_dmi_table[] ergaenzen"
    echo "        (Vorlage in docs/DMI-PATCH.md), Profil .validated = false"
    echo "     b) vorlaeufig: read-only laden mit  force=1  (kein PWM!)"
fi

echo
echo "--- Bauen fuer $KREL ---"
make -C "$BUILD" KDIR="/lib/modules/$KREL/build"

echo
echo "--- modinfo ---"
modinfo "$BUILD/minisforum_n5_it5571.ko"

VM="$(modinfo -F vermagic "$BUILD/minisforum_n5_it5571.ko" | awk '{print $1}')"
if [[ "$VM" == "$KREL" ]]; then
    echo
    echo "vermagic [$VM] passt zum laufenden Kernel. OK."
else
    echo
    echo "WARNUNG: vermagic [$VM] != uname -r [$KREL]. NICHT laden."
    exit 1
fi

echo
echo "Gebaut: $BUILD/minisforum_n5_it5571.ko"
echo "Weiter mit: scripts/05-load-readonly.sh"
