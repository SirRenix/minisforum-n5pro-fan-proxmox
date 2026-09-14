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

echo "--- n5-fand + CLI ---"
install -m 0755 "$ROOT/deploy/n5-fand"          /usr/local/sbin/n5-fand
install -m 0755 "$ROOT/deploy/n5-fand-failsafe" /usr/local/sbin/n5-fand-failsafe
install -m 0755 "$ROOT/deploy/n5-fand-alert"    /usr/local/sbin/n5-fand-alert
install -m 0755 "$ROOT/deploy/n5-fand-onfailure" /usr/local/sbin/n5-fand-onfailure
install -m 0755 "$ROOT/deploy/n5fan"            /usr/local/bin/n5fan
if [[ -f /etc/n5-fand.conf ]]; then
    echo "  /etc/n5-fand.conf existiert — nicht ueberschrieben (Vorlage: deploy/n5-fand.conf)"
else
    install -m 0644 "$ROOT/deploy/n5-fand.conf" /etc/n5-fand.conf
fi
install -m 0644 "$ROOT/deploy/n5-fand.service"           /etc/systemd/system/n5-fand.service
install -m 0644 "$ROOT/deploy/n5-fand-onfailure.service" /etc/systemd/system/n5-fand-onfailure.service
if [[ -d /etc/pve ]]; then
    # pmxcfs erlaubt kein chmod -> cp statt install
    mkdir -p /etc/pve/notification-templates/default
    cp "$ROOT/deploy/pve-notification/n5-fand-subject.txt.hbs" /etc/pve/notification-templates/default/
    cp "$ROOT/deploy/pve-notification/n5-fand-body.txt.hbs"    /etc/pve/notification-templates/default/
    echo "  PVE-Notification-Template installiert (Alarme -> Proxmox-Benachrichtigungen)"
fi
systemctl daemon-reload
systemctl enable n5-fand.service

cat <<EOF

Installiert. Naechste Schritte:
  systemctl restart n5-fand && n5fan status
  n5fan check                  # Selbstcheck
Nach einem Kernel-Update baut DKMS automatisch (AUTOINSTALL). Kontrolle:
  dkms status $PKG
EOF