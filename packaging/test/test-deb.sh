#!/usr/bin/env bash
# Installation test of the DKMS package in a Debian 13 container (CI and
# tools): a hand installation of 0.2.0 (deploy/install.sh as of 09/2026) is
# taken over, the module is built for the installed headers, removal cleans
# up. Run as root in debian:trixie from the repo root, after build-deb.sh.
set -euo pipefail
PKG=minisforum-n5-it5571
. packaging/UPSTREAM
VER=${UPSTREAM_TAG#v}
DEB=$(ls dist/${PKG}-dkms_*_all.deb)
fail() { echo "FAIL: $*" >&2; exit 1; }

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq dkms linux-headers-amd64 >/dev/null
K=$(ls /lib/modules | head -1)
[ -e "/lib/modules/$K/build" ] || fail "no headers in the container"
echo "headers: $K"

echo "--- hand installation 0.2.0, as deploy/install.sh left it"
mkdir -p /usr/src/$PKG-0.2.0
dpkg-deb --fsys-tarfile "$DEB" | tar -xO ./usr/src/$PKG-$VER/minisforum_n5_it5571.c >/usr/src/$PKG-0.2.0/minisforum_n5_it5571.c
dpkg-deb --fsys-tarfile "$DEB" | tar -xO ./usr/src/$PKG-$VER/Makefile >/usr/src/$PKG-0.2.0/Makefile
sed 's/^PACKAGE_VERSION=.*/PACKAGE_VERSION="0.2.0"/' deploy/dkms.conf >/usr/src/$PKG-0.2.0/dkms.conf
dkms add -m $PKG -v 0.2.0 >/dev/null
dkms install -m $PKG -v 0.2.0 -k "$K" >/dev/null
install -m 0644 packaging/test/legacy-modprobe.conf /etc/modprobe.d/$PKG.conf
install -m 0644 packaging/test/legacy-modules-load.conf /etc/modules-load.d/$PKG.conf
dkms status

echo "--- apt install the package"
apt-get install -y "./$DEB" 2>&1 | grep -E "^$PKG|Setting up" || true
st=$(dkms status -m $PKG)
echo "$st"
echo "$st" | grep -q "$PKG/$VER, $K, .*: installed" || fail "not installed for $K"
echo "$st" | grep -q "$PKG/0.2.0" && fail "hand installation 0.2.0 still registered"
[ -d /usr/src/$PKG-0.2.0 ] && fail "/usr/src/$PKG-0.2.0 left behind"
[ -e /etc/modprobe.d/$PKG.conf ] && fail "legacy /etc/modprobe.d copy left behind"
[ -e /etc/modules-load.d/$PKG.conf ] && fail "legacy /etc/modules-load.d copy left behind"
grep -q 'experimental_write=1' /usr/lib/modprobe.d/$PKG.conf || fail "modprobe option missing"
grep -qx "$PKG" /usr/lib/modules-load.d/$PKG.conf 2>/dev/null || grep -qx 'minisforum_n5_it5571' /usr/lib/modules-load.d/$PKG.conf || fail "modules-load entry missing"
modinfo -k "$K" minisforum_n5_it5571 | grep -E '^(filename|version):'
[ "$(cat /usr/src/$PKG-$VER/UPSTREAM_COMMIT)" = "$UPSTREAM_COMMIT" ] || fail "UPSTREAM_COMMIT"

echo "--- an edited /etc copy is kept on reinstall"
echo 'options minisforum_n5_it5571 experimental_write=1 force=0' >/etc/modprobe.d/$PKG.conf
apt-get install -y --reinstall "./$DEB" 2>&1 | grep -E "^$PKG" || true
[ -e /etc/modprobe.d/$PKG.conf ] || fail "edited /etc copy removed"
dkms status -m $PKG | grep -q "$VER, $K, .*: installed" || fail "not installed after reinstall"

echo "--- remove"
apt-get remove -y ${PKG}-dkms 2>&1 | grep -E "^$PKG" || true
[ -z "$(dkms status -m $PKG)" ] || fail "dkms still lists $PKG after remove"
[ -e "/lib/modules/$K/updates/dkms/minisforum_n5_it5571.ko" ] && fail ".ko left behind"
[ -e "/lib/modules/$K/updates/dkms/minisforum_n5_it5571.ko.xz" ] && fail ".ko.xz left behind"
echo "PASS"
