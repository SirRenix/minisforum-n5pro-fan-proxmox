#!/usr/bin/env bash
# Installation test of the DKMS package in a Debian 13 container (CI and
# tools): a hand installation of 0.2.0 (deploy/install.sh as of 09/2026) is
# taken over, the module is built for the installed headers, an active
# regulator is stopped before 'dkms remove' and started after the load
# (stub systemctl); a stale restart record, a blacklisted module (load
# with modprobe -b, also checked against the real kmod), an already loaded
# module, an early postinst failure and an aborted upgrade (new preinst
# fails) are covered; no modules-load.d file is shipped; removal cleans up. Run as root in debian:trixie from the repo
# root, after build-deb.sh.
set -euo pipefail
PKG=minisforum-n5-it5571
. packaging/version.sh
DEB=dist/${PKG}-dkms_${DEBVER}_all.deb
[ -f "$DEB" ] || { echo "FAIL: $DEB missing (packaging/build-deb.sh)" >&2; exit 1; }
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
out=$(apt-get install -y "./$DEB" 2>&1 | grep -E "^$PKG|Setting up" || true)
echo "$out"
echo "$out" | grep -q "/usr/lib/modules-load.d" && fail "postinst names a /usr/lib/modules-load.d file the package does not ship"
echo "$out" | grep -q "removed the hand-installed /etc/modules-load.d/$PKG.conf; udev loads" || fail "hand-installed modules-load.d copy not reported as removed"
st=$(dkms status -m $PKG)
echo "$st"
echo "$st" | grep -q "$PKG/$VER, $K, .*: installed" || fail "not installed for $K"
echo "$st" | grep -q "$PKG/0.2.0" && fail "hand installation 0.2.0 still registered"
[ -d /usr/src/$PKG-0.2.0 ] && fail "/usr/src/$PKG-0.2.0 left behind"
[ -e /etc/modprobe.d/$PKG.conf ] && fail "legacy /etc/modprobe.d copy left behind"
[ -e /etc/modules-load.d/$PKG.conf ] && fail "legacy /etc/modules-load.d copy left behind"
grep -q 'experimental_write=1' /usr/lib/modprobe.d/$PKG.conf || fail "modprobe option missing"
[ -e /usr/lib/modules-load.d/$PKG.conf ] && fail "package ships /usr/lib/modules-load.d/$PKG.conf (udev loads by DMI alias, #37)"
modinfo -k "$K" minisforum_n5_it5571 | grep -E '^(filename|version):'
[ "$(cat /usr/src/$PKG-$VER/UPSTREAM_COMMIT)" = "$UPSTREAM_COMMIT" ] || fail "UPSTREAM_COMMIT"
if [ -z "$(sed -e 's/#.*//' -e '/^[[:space:]]*$/d' driver/patches/series)" ]; then
	for f in minisforum_n5_it5571.c Makefile; do
		want=$(sha256sum <driver/upstream/driver-prototype/$f | cut -d' ' -f1)
		got=$(sha256sum </usr/src/$PKG-$VER/$f | cut -d' ' -f1)
		[ "$got" = "$want" ] || fail "/usr/src/$PKG-$VER/$f differs from driver/upstream (series is empty)"
		echo "$f = driver/upstream ($got)"
	done
fi

echo "--- real kmod: modprobe -b honours a blacklist and exits 0 without loading; by name it ignores it (#50)"
echo 'blacklist minisforum_n5_it5571' >/etc/modprobe.d/zz-test-blacklist.conf
modprobe -c | grep -qx 'blacklist minisforum_n5_it5571' || fail "modprobe -c does not list the blacklist"
modprobe -b -S "$K" minisforum_n5_it5571 || fail "modprobe -b of a blacklisted module did not exit 0"
lsmod | grep -q '^minisforum_n5_it5571 ' && fail "modprobe -b loaded a blacklisted module"
modprobe -S "$K" minisforum_n5_it5571 2>/dev/null && fail "modprobe by name succeeded in the container (expected an insert attempt and its failure)"
echo "modprobe -b: exit 0, not loaded; by name: insert attempted (fails here, no N5 board / no CAP_SYS_MODULE)"
rm -f /etc/modprobe.d/zz-test-blacklist.conf

echo "--- stubs: systemctl, dkms, modprobe and lsmod log their calls (update order, E7)"
# no systemd in the container: a stub systemctl keeps fake unit states in
# $UNITS_DIR; dkms and modprobe are wrapped to log the order. modprobe -b of
# the module "loads" it by creating $LOADED, which the lsmod stub reports
# (no N5 board here); with $BLOCKED it behaves as kmod with a blacklist:
# exit 0, nothing loaded.
CALLS=/tmp/e7-calls
LOADED=/tmp/e7-loaded
BLOCKED=/tmp/e7-blacklisted
UNITS_DIR=/tmp/e7-units
STATE=/run/$PKG-dkms.restart
REAL_DKMS=$(command -v dkms)
REAL_MODPROBE=$(command -v modprobe)
# apt runs dpkg with DPkg::Path (default without /usr/local/sbin)
STUBPATH=(-o DPkg::Path=/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin)
mkdir -p "$UNITS_DIR"
cat >/usr/local/sbin/systemctl <<EOF
#!/bin/sh
echo "systemctl \$*" >>$CALLS
[ "\$1" = is-active ] && [ "\$2" = --quiet ] && set -- is-active "\$3"
case "\$1" in
is-active) [ -e "$UNITS_DIR/\$2" ] ;;
stop) rm -f "$UNITS_DIR/\$2" ;;
start) touch "$UNITS_DIR/\$2" ;;
*) exit 0 ;;
esac
EOF
cat >/usr/local/sbin/dkms <<EOF
#!/bin/sh
echo "dkms \$*" >>$CALLS
[ "\$1" = add ] && [ -e /tmp/e7-fail-add ] && exit 1
exec $REAL_DKMS "\$@"
EOF
cat >/usr/local/sbin/modprobe <<EOF
#!/bin/sh
echo "modprobe \$*" >>$CALLS
[ "\$*" = minisforum_n5_it5571 ] && { echo "stub: modprobe by name" >&2; exit 1; }
if [ "\$*" = "-b minisforum_n5_it5571" ]; then
	[ -e $BLOCKED ] || touch $LOADED
	exit 0
fi
exec $REAL_MODPROBE "\$@"
EOF
cat >/usr/local/sbin/lsmod <<EOF
#!/bin/sh
echo "Module                  Size  Used by"
[ -e $LOADED ] && echo "minisforum_n5_it5571   16384  0"
exit 0
EOF
chmod 0755 /usr/local/sbin/systemctl /usr/local/sbin/dkms /usr/local/sbin/modprobe /usr/local/sbin/lsmod
# line of the first/last call matching an ERE, 0 if none
first() { grep -nE "$1" "$CALLS" | head -1 | cut -d: -f1 | grep . || echo 0; }
last() { grep -nE "$1" "$CALLS" | tail -1 | cut -d: -f1 | grep . || echo 0; }

echo "--- an edited /etc copy is kept on reinstall; an active n5-fangov is stopped and started around it"
echo 'options minisforum_n5_it5571 experimental_write=1 force=0' >/etc/modprobe.d/$PKG.conf
: >"$CALLS"
rm -f "$LOADED" "$BLOCKED"
touch "$UNITS_DIR/n5-fangov.service"
apt-get "${STUBPATH[@]}" install -y --reinstall "./$DEB" 2>&1 | grep -E "^$PKG" || true
[ -e /etc/modprobe.d/$PKG.conf ] || fail "edited /etc copy removed"
dkms status -m $PKG | grep -q "$VER, $K, .*: installed" || fail "not installed after reinstall"
cat "$CALLS"
stop=$(first '^systemctl stop n5-fangov\.service$')
rm_=$(first "^dkms remove -m $PKG ")
inst=$(last "^dkms install -m $PKG ")
load=$(last '^modprobe -b minisforum_n5_it5571$')
start=$(first '^systemctl start n5-fangov\.service$')
echo "order: stop=$stop remove=$rm_ install=$inst load=$load start=$start"
[ "$stop" -gt 0 ] || fail "n5-fangov not stopped on upgrade"
[ "$rm_" -gt "$stop" ] || fail "dkms remove not after stopping n5-fangov"
[ "$inst" -gt "$rm_" ] || fail "dkms install not after dkms remove"
[ "$load" -gt "$inst" ] || fail "module not loaded after dkms install"
[ "$start" -gt "$load" ] || fail "n5-fangov not started after the module load"
[ "$(grep -c '^systemctl stop ' "$CALLS")" -eq 1 ] || fail "stopped more than n5-fangov"
[ "$(grep -c '^systemctl start ' "$CALLS")" -eq 1 ] || fail "started more than n5-fangov"
[ -e "$UNITS_DIR/n5-fangov.service" ] || fail "n5-fangov not active after upgrade"
[ -e "$STATE" ] && fail "$STATE left behind"

echo "--- inactive regulator: nothing is started"
: >"$CALLS"
rm -f "$UNITS_DIR"/*
apt-get "${STUBPATH[@]}" install -y --reinstall "./$DEB" 2>&1 | grep -E "^$PKG" || true
grep -qE '^systemctl (stop|start) ' "$CALLS" && fail "stop/start without an active regulator"
[ -e "$STATE" ] && fail "$STATE left behind"

echo "--- a stale record from an interrupted sequence does not start a stopped regulator"
: >"$CALLS"
rm -f "$UNITS_DIR"/*
echo n5-fangov.service >"$STATE"
apt-get "${STUBPATH[@]}" install -y --reinstall "./$DEB" 2>&1 | grep -E "^$PKG" || true
grep -qE '^systemctl start ' "$CALLS" && fail "stale $STATE started a regulator"
[ -e "$STATE" ] && fail "$STATE left behind"

echo "--- blacklisted (modprobe -b exits 0, nothing loaded): reported as blacklisted, regulator not started"
echo 'blacklist minisforum_n5_it5571' >/etc/modprobe.d/zz-test-blacklist.conf
rm -f "$LOADED"
touch "$BLOCKED" "$UNITS_DIR/n5-fangov.service"
: >"$CALLS"
out=$(apt-get "${STUBPATH[@]}" install -y --reinstall "./$DEB" 2>&1 | grep -E "^$PKG" || true)
echo "$out"
grep -q '^modprobe -b minisforum_n5_it5571$' "$CALLS" || fail "postinst did not call modprobe -b"
echo "$out" | grep -q "blacklisted" || fail "blacklist not reported"
echo "$out" | grep -q "module loaded" && fail "reported as loaded although blacklisted"
grep -q '^systemctl start ' "$CALLS" && fail "regulator started without a loaded module"
[ -e "$STATE" ] && fail "$STATE left behind (blacklisted)"
rm -f "$BLOCKED" /etc/modprobe.d/zz-test-blacklist.conf

echo "--- module already loaded (reference host case): no modprobe of the module, regulator started again"
touch "$LOADED"
: >"$CALLS"
touch "$UNITS_DIR/n5-fangov.service"
out=$(apt-get "${STUBPATH[@]}" install -y --reinstall "./$DEB" 2>&1 | grep -E "^$PKG" || true)
echo "$out"
echo "$out" | grep -q "module already loaded" || fail "already-loaded branch not taken"
grep -qE '^modprobe (-b )?minisforum_n5_it5571$' "$CALLS" && fail "module loaded again although lsmod lists it"
[ "$(first '^systemctl start n5-fangov\.service$')" -gt "$(last "^dkms install -m $PKG ")" ] || fail "n5-fangov not started after the build (already loaded)"
[ -e "$UNITS_DIR/n5-fangov.service" ] || fail "n5-fangov not active (already loaded)"

echo "--- postinst fails early (dkms add): the regulator is started again, then dpkg --configure repairs"
: >"$CALLS"
touch /tmp/e7-fail-add
apt-get "${STUBPATH[@]}" install -y --reinstall "./$DEB" >/tmp/e7-early.log 2>&1 && fail "postinst did not fail with a failing dkms add"
grep -E "^$PKG" /tmp/e7-early.log || true
grep -q '^systemctl start n5-fangov\.service$' "$CALLS" || fail "n5-fangov stays stopped after an early postinst failure"
[ -e "$UNITS_DIR/n5-fangov.service" ] || fail "n5-fangov not active after an early postinst failure"
[ -e "$STATE" ] && fail "$STATE left behind after an early failure"
rm -f /tmp/e7-fail-add
dpkg --configure -a >/dev/null
dkms status -m $PKG | grep -q "$VER, $K, .*: installed" || fail "not installed after dpkg --configure -a"

echo "--- aborted upgrade (new preinst fails, module loaded as on the reference host): DKMS entry and module are back, regulator started"
FAKE=/tmp/e7-fake
rm -rf "$FAKE" && mkdir -p "$FAKE/DEBIAN"
cat >"$FAKE/DEBIAN/control" <<EOF
Package: ${PKG}-dkms
Version: ${VER}-99
Architecture: all
Maintainer: test <test@example.invalid>
Description: test package whose preinst fails
 test
EOF
printf '#!/bin/sh\nexit 1\n' >"$FAKE/DEBIAN/preinst"
chmod 0755 "$FAKE/DEBIAN/preinst"
dpkg-deb --build --root-owner-group "$FAKE" /tmp/e7-fake.deb >/dev/null
: >"$CALLS"
touch "$UNITS_DIR/n5-fangov.service"
dpkg -i /tmp/e7-fake.deb >/tmp/e7-abort.log 2>&1 && fail "fake upgrade did not fail"
grep -E "^$PKG|abort" /tmp/e7-abort.log || true
[ "$(dpkg-query -W -f='${db:Status-Abbrev} ${Version}' ${PKG}-dkms)" = "ii  $DEBVER" ] || fail "package state after the aborted upgrade: $(dpkg-query -W -f='${db:Status-Abbrev} ${Version}' ${PKG}-dkms)"
dkms status -m $PKG | grep -q "$VER, $K, .*: installed" || fail "no DKMS entry after abort-upgrade"
ls /lib/modules/"$K"/updates/dkms/minisforum_n5_it5571.ko* >/dev/null 2>&1 || fail "no module file after abort-upgrade"
[ "$(first "^dkms remove -m $PKG ")" -gt 0 ] || fail "the old prerm did not run dkms remove (scenario not reached)"
[ "$(first '^systemctl start n5-fangov\.service$')" -gt "$(last "^dkms install -m $PKG ")" ] || fail "n5-fangov not started after the rebuild"
[ -e "$STATE" ] && fail "$STATE left behind after abort-upgrade"
rm -f "$LOADED"

echo "--- remove; an active legacy n5-fand is stopped before dkms remove and stays stopped"
rm -f "$UNITS_DIR"/*
: >"$CALLS"
touch "$UNITS_DIR/n5-fand.service"
apt-get "${STUBPATH[@]}" remove -y ${PKG}-dkms 2>&1 | grep -E "^$PKG" || true
cat "$CALLS"
stop=$(first '^systemctl stop n5-fand\.service$')
rm_=$(first "^dkms remove -m $PKG ")
echo "order: stop=$stop remove=$rm_"
[ "$stop" -gt 0 ] || fail "n5-fand not stopped on remove"
[ "$rm_" -gt "$stop" ] || fail "dkms remove not after stopping n5-fand"
grep -q '^systemctl start ' "$CALLS" && fail "a regulator was started on remove"
[ -e "$STATE" ] && fail "$STATE written on remove"
rm -f /usr/local/sbin/systemctl /usr/local/sbin/dkms /usr/local/sbin/modprobe /usr/local/sbin/lsmod
[ -z "$(dkms status -m $PKG)" ] || fail "dkms still lists $PKG after remove"
[ -e "/lib/modules/$K/updates/dkms/minisforum_n5_it5571.ko" ] && fail ".ko left behind"
[ -e "/lib/modules/$K/updates/dkms/minisforum_n5_it5571.ko.xz" ] && fail ".ko.xz left behind"
echo "PASS"
