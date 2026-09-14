#!/usr/bin/env bash
# Installiert Modul (DKMS), Autoload und n5-fand auf dem Zielhost.
# Idempotent. Aufruf als root aus dem Projektordner: ./deploy/install.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/upstream/driver-prototype"
PKG="minisforum-n5-it5571"; VER="0.2.0"
DK="/usr/src/$PKG-$VER"

[[ $EUID -eq 0 ]] || { echo "als root"; exit 1; }
[[ -f "$SRC/minisforum_n5_it5571.c" ]] || { echo "upstream fehlt: scripts/02-build-tools.sh"; exit 1; }

echo "--- DKMS-Quelle $DK ---"
mkdir -p "$DK"
cp "$SRC/minisforum_n5_it5571.c" "$SRC/Makefile" "$ROOT/deploy/dkms.conf" "$DK/"
git -C "$ROOT/upstream" rev-parse HEAD > "$DK/UPSTREAM_COMMIT"
if ! dkms status "$PKG/$VER" | grep -q "$PKG"; then
    dkms add "$PKG/$VER"
fi
dkms build "$PKG/$VER" -k "$(uname -r)"
dkms install "$PKG/$VER" -k "$(uname -r)" --force
dkms status "$PKG"

echo "--- Autoload + Optionen ---"
install -m 0644 "$ROOT/deploy/$PKG.modprobe.conf"      /etc/modprobe.d/$PKG.conf
install -m 0644 "$ROOT/deploy/$PKG.modules-load.conf"  /etc/modules-load.d/$PKG.conf

echo "--- n5-fand ---"
install -m 0755 "$ROOT/deploy/n5-fand" /usr/local/sbin/n5-fand
[[ -f /etc/n5-fand.conf ]] || install -m 0644 "$ROOT/deploy/n5-fand.conf" /etc/n5-fand.conf
install -m 0644 "$ROOT/deploy/n5-fand.service" /etc/systemd/system/n5-fand.service
systemctl daemon-reload
systemctl enable n5-fand.service

cat <<EOF

Installiert. Naechste Schritte:
  modprobe -r minisforum_n5_it5571 2>/dev/null; modprobe minisforum_n5_it5571
  systemctl start n5-fand && journalctl -fu n5-fand
Nach einem Kernel-Update baut DKMS automatisch (AUTOINSTALL). Kontrolle:
  dkms status $PKG
EOF