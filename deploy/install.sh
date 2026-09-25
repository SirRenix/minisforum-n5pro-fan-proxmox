#!/usr/bin/env bash
# Installs the driver from the upstream clone as DKMS module, with the module
# options and the autoload entry. The path without the package: normally
# `apt install ./minisforum-n5-it5571-dkms_<ver>_all.deb` (release asset) does
# the same and keeps it under apt's control.
# Idempotent. Run as root from the project folder after scripts/02-build-tools.sh.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC="$ROOT/upstream/driver-prototype"
PKG="minisforum-n5-it5571"
VER=$(sed -n 's/^PACKAGE_VERSION="\(.*\)"$/\1/p' "$ROOT/deploy/dkms.conf")
DK="/usr/src/$PKG-$VER"

[[ $EUID -eq 0 ]] || { echo "run as root"; exit 1; }
[[ -f "$SRC/minisforum_n5_it5571.c" ]] || { echo "upstream source missing: scripts/02-build-tools.sh"; exit 1; }
if dpkg-query -W -f='${Status}' "$PKG-dkms" 2>/dev/null | grep -q "install ok installed"; then
    echo "$PKG-dkms is installed as a package; update it with apt, not with this script"
    exit 1
fi

echo "--- DKMS source $DK ---"
mkdir -p "$DK"
cp "$SRC/minisforum_n5_it5571.c" "$SRC/Makefile" "$ROOT/deploy/dkms.conf" "$DK/"
git -C "$ROOT/upstream" rev-parse HEAD > "$DK/UPSTREAM_COMMIT"
if ! dkms status -m "$PKG" -v "$VER" | grep -q "$PKG"; then
    dkms add -m "$PKG" -v "$VER"
fi
for m in /lib/modules/*; do
    k=${m##*/}
    [[ -e "$m/build" ]] || { echo "  no headers for $k, skipped"; continue; }
    dkms install -m "$PKG" -v "$VER" -k "$k" --force
done
dkms status -m "$PKG"

echo "--- options + autoload ---"
install -m 0644 "$ROOT/deploy/$PKG.modprobe.conf"     "/etc/modprobe.d/$PKG.conf"
install -m 0644 "$ROOT/deploy/$PKG.modules-load.conf" "/etc/modules-load.d/$PKG.conf"

cat <<EOF

Installed. Load now:   modprobe minisforum_n5_it5571
Check:                 ls /sys/class/hwmon/*/pwm1 && sensors
DKMS rebuilds for every new kernel whose headers are installed (AUTOINSTALL).
Regulator: https://github.com/SirRenix/n5-fangov (the Bash predecessor n5-fand is in legacy/).
EOF
