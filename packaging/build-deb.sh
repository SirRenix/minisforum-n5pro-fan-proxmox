#!/usr/bin/env bash
# Builds dist/minisforum-n5-it5571-dkms_<DEBVER>_all.deb from driver/upstream/
# (verified against its SHA256SUMS) plus the patch series driver/patches/series.
# Version from packaging/UPSTREAM via packaging/version.sh. Needs dpkg-deb,
# patch and sha256sum; no network, no root, no kernel headers (DKMS builds on
# the target). Usage: packaging/build-deb.sh
# KEEP_SRC=<dir>: also copy the packaged DKMS source tree there (for checks).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=version.sh
. "$ROOT/packaging/version.sh"
PKG=minisforum-n5-it5571
MOD=minisforum_n5_it5571
UP="$ROOT/driver/upstream"
PATCHDIR="$ROOT/driver/patches"
OUT="$ROOT/dist"
W="$(mktemp -d)"
trap 'rm -rf "$W"' EXIT
die() { echo "build-deb: $*" >&2; exit 1; }

# 1. driver/upstream is exactly what SHA256SUMS lists, unchanged, and the file
#    set is the one in ORIGIN ("files:"); CI checks ORIGIN against the upstream
#    tree, so a file dropped from all three places still fails there
(cd "$UP" && sha256sum --strict --quiet -c SHA256SUMS) || die "driver/upstream does not match its SHA256SUMS"
listed=$(sed 's/^[0-9a-f]*  //' "$UP/SHA256SUMS" | LC_ALL=C sort)
present=$(cd "$UP" && find . -type f ! -name SHA256SUMS ! -name ORIGIN | sed 's|^\./||' | LC_ALL=C sort)
origin=$(sed -n 's/^  \([^ ]*\) *blob [0-9a-f]\{40\}$/\1/p' "$UP/ORIGIN" | LC_ALL=C sort)
[ "$listed" = "$present" ] || die "driver/upstream has files not in SHA256SUMS (or misses some)"
[ "$origin" = "$present" ] || die "driver/upstream file set differs from the files: list in ORIGIN"
grep -q "^commit: *$UPSTREAM_COMMIT\$" "$UP/ORIGIN" || die "driver/upstream/ORIGIN is not commit $UPSTREAM_COMMIT"

# 2. deploy/dkms.conf is the template; its PACKAGE_VERSION is the upstream
#    version (deploy/install.sh uses it as is), the package gets $VER
dv=$(sed -n 's/^PACKAGE_VERSION="\(.*\)"$/\1/p' "$ROOT/deploy/dkms.conf")
[ "$dv" = "$BASE_VER" ] || die "deploy/dkms.conf PACKAGE_VERSION=$dv, upstream tag $UPSTREAM_TAG"

# 3. copy and apply the patch series in order; any reject or fuzz fails.
#    series = one plain file name per line; every other file in driver/patches/
#    must be listed there (nothing is skipped silently)
B="$W/src"
cp -a "$UP" "$B"
patches=
while IFS= read -r line || [ -n "$line" ]; do
	line=${line%%#*}
	line=$(printf '%s' "$line" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')
	[ -n "$line" ] || continue
	case "$line" in
	*[!A-Za-z0-9._+-]* | -* | .* | series) die "series: invalid entry \"$line\" (allowed: A-Z a-z 0-9 . _ + -)" ;;
	esac
	printf '%s\n' "$patches" | grep -qxF -- "$line" && die "series lists $line twice"
	[ -f "$PATCHDIR/$line" ] || die "series lists $line, missing in driver/patches/"
	patches="$patches${patches:+
}$line"
done <"$PATCHDIR/series"
inseries=$(printf '%s\n' "$patches" | sed '/^$/d' | LC_ALL=C sort)
indir=$(cd "$PATCHDIR" && find . -mindepth 1 ! -name series | sed 's|^\./||' | LC_ALL=C sort)
[ "$inseries" = "$indir" ] || die "driver/patches/ and series differ: in dir [$indir], in series [$inseries]"

if [ -z "$patches" ]; then
	[ -z "${FORK_VERSION:-}" ] || die "FORK_VERSION=$FORK_VERSION but driver/patches/series is empty"
else
	[ -n "${FORK_VERSION:-}" ] || die "driver/patches/series is not empty: set FORK_VERSION (packaging/UPSTREAM)"
	while IFS= read -r p; do
		echo "applying $p"
		patch -d "$B" -p1 --forward --no-backup-if-mismatch --fuzz=0 --quiet <"$PATCHDIR/$p" || die "$p does not apply cleanly"
	done <<EOF
$patches
EOF
	[ -z "$(find "$B" -name '*.rej' -o -name '*.orig')" ] || die "patch left .rej/.orig files"
	# the package ships driver-prototype/{$MOD.c,Makefile} only: a patch that
	# adds, removes or changes any other file would be lost silently
	manifest() { (cd "$1" && find . -type f ! -name SHA256SUMS ! -name ORIGIN -print0 | LC_ALL=C sort -z | xargs -0 sha256sum); }
	manifest "$UP" >"$W/m.up"
	manifest "$B" >"$W/m.patched"
	touched=$(diff "$W/m.up" "$W/m.patched" | sed -n 's/^[<>] [0-9a-f]\{64\}  \.\///p' | LC_ALL=C sort -u || true)
	while IFS= read -r f; do
		case "$f" in
		"" | "driver-prototype/$MOD.c" | driver-prototype/Makefile) ;;
		*) die "patch series touches $f; only driver-prototype/$MOD.c and Makefile are packaged" ;;
		esac
	done <<EOF
$touched
EOF
	[ -f "$B/driver-prototype/$MOD.c" ] && [ -f "$B/driver-prototype/Makefile" ] || die "patch series removed a packaged file"
	mv_=$(sed -n 's/^MODULE_VERSION("\(.*\)");$/\1/p' "$B/driver-prototype/$MOD.c")
	[ "$mv_" = "$VER" ] || die "MODULE_VERSION is \"$mv_\", PACKAGE_VERSION $VER (patch 0001 sets it)"
fi

# 4. package tree
P="$W/pkg"
S="$P/usr/src/$PKG-$VER"
DOC="$P/usr/share/doc/$PKG-dkms"
install -d "$S" "$P/usr/lib/modprobe.d" "$DOC" "$P/DEBIAN"
install -m 0644 "$B/driver-prototype/$MOD.c" "$B/driver-prototype/Makefile" "$S/"
sed "s/^PACKAGE_VERSION=.*/PACKAGE_VERSION=\"$VER\"/" "$ROOT/deploy/dkms.conf" >"$S/dkms.conf"
echo "$UPSTREAM_COMMIT" >"$S/UPSTREAM_COMMIT"
if [ -z "$patches" ]; then
	echo "# none: upstream $UPSTREAM_TAG ($UPSTREAM_COMMIT) unchanged" >"$S/PATCHES"
else
	echo "$patches" >"$S/PATCHES"
fi
chmod 0644 "$S/dkms.conf" "$S/UPSTREAM_COMMIT" "$S/PATCHES"
install -m 0644 "$ROOT/deploy/$PKG.modprobe.conf" "$P/usr/lib/modprobe.d/$PKG.conf"
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

if [ -n "${KEEP_SRC:-}" ]; then
	mkdir -p "$KEEP_SRC"
	cp -a "$S/." "$KEEP_SRC/"
fi

mkdir -p "$OUT"
deb="$OUT/${PKG}-dkms_${DEBVER}_all.deb"
dpkg-deb --build --root-owner-group "$P" "$deb" >/dev/null
(cd "$OUT" && sha256sum "${deb##*/}" >"${deb##*/}.sha256")
echo "built $deb"
dpkg-deb -I "$deb" | grep -E '^ (Package|Version|Depends):'
