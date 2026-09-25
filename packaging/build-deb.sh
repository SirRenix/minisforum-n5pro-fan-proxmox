#!/usr/bin/env bash
# Builds dist/minisforum-n5-it5571-dkms_<ver>-<rev>_all.deb from the pinned
# upstream tag (packaging/UPSTREAM). Needs git and dpkg-deb; no root, no kernel
# headers (DKMS builds on the target). Usage: packaging/build-deb.sh
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=UPSTREAM
. "$ROOT/packaging/UPSTREAM"
PKG=minisforum-n5-it5571
VER=${UPSTREAM_TAG#v}
DEBVER="$VER-$PKG_REV"
OUT="$ROOT/dist"
W="$(mktemp -d)"
trap 'rm -rf "$W"' EXIT

# deploy/dkms.conf is the one DKMS config (install.sh uses it too)
dv=$(sed -n 's/^PACKAGE_VERSION="\(.*\)"$/\1/p' "$ROOT/deploy/dkms.conf")
[ "$dv" = "$VER" ] || { echo "deploy/dkms.conf PACKAGE_VERSION=$dv, upstream tag $UPSTREAM_TAG" >&2; exit 1; }

git clone -q --depth 1 --branch "$UPSTREAM_TAG" "$UPSTREAM_REPO" "$W/up" 2>/dev/null
got=$(git -C "$W/up" rev-parse HEAD)
[ "$got" = "$UPSTREAM_COMMIT" ] || { echo "upstream $UPSTREAM_TAG is $got, expected $UPSTREAM_COMMIT" >&2; exit 1; }

P="$W/pkg"
S="$P/usr/src/$PKG-$VER"
DOC="$P/usr/share/doc/$PKG-dkms"
install -d "$S" "$P/usr/lib/modprobe.d" "$P/usr/lib/modules-load.d" "$DOC" "$P/DEBIAN"
install -m 0644 "$W/up/driver-prototype/minisforum_n5_it5571.c" "$W/up/driver-prototype/Makefile" "$ROOT/deploy/dkms.conf" "$S/"
echo "$UPSTREAM_COMMIT" >"$S/UPSTREAM_COMMIT"
chmod 0644 "$S/UPSTREAM_COMMIT"
install -m 0644 "$ROOT/deploy/$PKG.modprobe.conf" "$P/usr/lib/modprobe.d/$PKG.conf"
install -m 0644 "$ROOT/deploy/$PKG.modules-load.conf" "$P/usr/lib/modules-load.d/$PKG.conf"
install -m 0644 "$ROOT/packaging/debian/copyright" "$DOC/copyright"

for s in postinst prerm; do
	sed "s/@VERSION@/$VER/" "$ROOT/packaging/debian/$s" >"$P/DEBIAN/$s"
	chmod 0755 "$P/DEBIAN/$s"
done
size=$(du -sk --exclude=DEBIAN "$P" | cut -f1)
sed -e "s/@DEBVER@/$DEBVER/" -e "s/@SIZE@/$size/" -e "s/@TAG@/$UPSTREAM_TAG/" -e "s/@COMMIT@/${UPSTREAM_COMMIT:0:7}/" \
	"$ROOT/packaging/debian/control.in" >"$P/DEBIAN/control"
(cd "$P" && find . -type f -not -path "./DEBIAN/*" | sed 's|^\./||' | LC_ALL=C sort | xargs md5sum >DEBIAN/md5sums)
chmod 0644 "$P/DEBIAN/md5sums" "$P/DEBIAN/control"

mkdir -p "$OUT"
deb="$OUT/${PKG}-dkms_${DEBVER}_all.deb"
dpkg-deb --build --root-owner-group "$P" "$deb" >/dev/null
(cd "$OUT" && sha256sum "${deb##*/}" >"${deb##*/}.sha256")
echo "built $deb"
dpkg-deb -I "$deb" | grep -E '^ (Package|Version|Depends):'
