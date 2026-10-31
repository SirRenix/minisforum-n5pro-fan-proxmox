#!/usr/bin/env bash
# Phase 2 — Repo holen und die Userspace-Research-Tools bauen.
# Kein Hardwarezugriff, nur Compilieren.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/upstream"
BIN="$ROOT/bin"
# Fester Upstream-Stand: derselbe Commit wie das Paket (packaging/UPSTREAM).
# Die Research-Tools liegen nicht in driver/upstream/ (dort nur Treiber und
# Lizenzen), deshalb der Klon; nie ein beweglicher Branch.
# shellcheck source=../packaging/UPSTREAM
. "$ROOT/packaging/UPSTREAM"
REPO="$UPSTREAM_REPO"
COMMIT="$UPSTREAM_COMMIT"
[[ "$COMMIT" =~ ^[0-9a-f]{40}$ ]] || { echo "UPSTREAM_COMMIT in packaging/UPSTREAM ist kein voller Commit-Hash"; exit 1; }

mkdir -p "$BIN"

if ! command -v gcc >/dev/null || ! command -v git >/dev/null; then
    echo "gcc/git fehlen. Installieren mit:"
    echo "  apt install -y build-essential git"
    exit 1
fi

if [[ ! -d "$SRC/.git" ]]; then
    echo "--- Repo klonen ---"
    git clone --no-checkout "$REPO" "$SRC"
fi
echo "--- Upstream-Commit $COMMIT holen ---"
git -C "$SRC" fetch --depth 1 origin "$COMMIT"
git -C "$SRC" -c advice.detachedHead=false checkout --quiet --force --detach "$COMMIT"
HEAD_COMMIT=$(git -C "$SRC" rev-parse HEAD)
if [[ "$HEAD_COMMIT" != "$COMMIT" ]]; then
    echo "Upstream steht auf $HEAD_COMMIT, erwartet $COMMIT - Abbruch"
    exit 1
fi
# Treiber und Lizenzen muessen byte-gleich mit driver/upstream/ sein
if ! (cd "$SRC" && sha256sum --quiet -c "$ROOT/driver/upstream/SHA256SUMS"); then
    echo "Upstream-Dateien weichen von driver/upstream/SHA256SUMS ab - Abbruch"
    exit 1
fi

echo
echo "Stand: $HEAD_COMMIT ($UPSTREAM_TAG), geprueft gegen driver/upstream/SHA256SUMS"

echo
echo "--- Research-Tools bauen ---"
# Upstream baut mit zig/musl; dort fehlen inb_p/outb_p in sys/io.h und die
# Tools definieren sie selbst. glibc (Debian 13) hat sie bereits, mit
# vertauschter Argumentreihenfolge -> Redefinition. Baukopie mit umbenannten
# Symbolen, upstream/ bleibt unveraendert.
TOOLSRC="$ROOT/build-tools"
rm -rf "$TOOLSRC"; mkdir -p "$TOOLSRC"
cp "$SRC"/research-tools/*.c "$TOOLSRC/"
sed -i -e 's/\binb_p\b/n5_inb_p/g' -e 's/\boutb_p\b/n5_outb_p/g' "$TOOLSRC"/*.c
cd "$TOOLSRC"
for t in n5_fan ec_probe sio_probe n5_i2ec_read n5_pwm_diag; do
    if [[ -f "$t.c" ]]; then
        gcc -O2 -Wall -o "$BIN/$t" "$t.c"
        echo "  gebaut: $BIN/$t"
    else
        echo "  FEHLT im Repo: $t.c"
    fi
done

cat <<'EOF'

Gebaute Werkzeuge und was sie tun:

  n5_fan probe        PMC-Ports und ITE-Chip-ID ermitteln    (lesend)
  n5_fan status       PWM-Sollwert + TACH1-3 RPM             (lesend)
  n5_fan rpm          nur RPM                                (lesend)
  n5_fan raw 0xNN     einzelnes PMC2-Subkommando lesen       (lesend)
  ec_probe dump       komplette 256 Byte EC-RAM              (lesend)
  ec_probe read 0xNN  einzelnes EC-RAM-Byte                  (lesend)
  ec_probe watch 0xNN Register beobachten                    (lesend)
  sio_probe           Super-I/O-Identifikation               (lesend)

  ec_probe write      >>> NICHT VERWENDEN <<<

Alle brauchen root (ioperm).
Weiter mit: scripts/03-probe-readonly.sh
EOF
