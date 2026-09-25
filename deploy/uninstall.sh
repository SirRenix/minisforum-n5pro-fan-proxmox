#!/usr/bin/env bash
# Removes what deploy/install.sh installed: the DKMS module and the two /etc
# files. The package is removed with `apt remove minisforum-n5-it5571-dkms`.
# Stop the regulator first; it hands the channels back or sets its safe state.
# Note: after any write the EC regulates the HDD channel again only after a
# cold boot (findings phase 6).
set -u
[[ $EUID -eq 0 ]] || { echo "run as root"; exit 1; }
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
PKG="minisforum-n5-it5571"
VER=$(sed -n 's/^PACKAGE_VERSION="\(.*\)"$/\1/p' "$ROOT/deploy/dkms.conf")
if dpkg-query -W -f='${Status}' "$PKG-dkms" 2>/dev/null | grep -q "install ok installed"; then
    echo "$PKG-dkms is installed as a package: apt remove $PKG-dkms"
    exit 1
fi

echo "--- DKMS ---"
dkms remove -m "$PKG" -v "$VER" --all 2>/dev/null && echo "  $PKG/$VER removed"
rm -rf "/usr/src/$PKG-$VER"

echo "--- options + autoload ---"
rm -f "/etc/modules-load.d/$PKG.conf" "/etc/modprobe.d/$PKG.conf"

echo
echo "Done. A loaded module stays until 'modprobe -r minisforum_n5_it5571' or the next boot."
echo "The Bash regulator n5-fand has its own uninstaller: legacy/uninstall.sh."
