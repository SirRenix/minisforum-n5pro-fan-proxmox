# shellcheck shell=sh
# shellcheck disable=SC2034 # the variables are used by the scripts that source this file
# Sourced (sh or bash) by build-deb.sh, test-deb.sh and the release workflow:
# reads packaging/UPSTREAM below ${ROOT:-.} and sets
#   BASE_VER  upstream version (UPSTREAM_TAG without "v")       0.2.1
#   VER       DKMS PACKAGE_VERSION, /usr/src/<pkg>-<VER>        0.2.1 | 0.2.1+sr1~rc1
#   DEBVER    Debian package version                            0.2.1-1 | 0.2.1+sr1~rc1-1
#   GIT_TAG   the only release tag the workflow accepts         v0.2.1-1 | v0.2.1-sr1-rc1
# Mapping table: packaging/UPSTREAM. An invalid UPSTREAM ends the sourcing
# shell with exit 1, with or without "set -e".
# shellcheck disable=SC1091 # path depends on ROOT
. "${ROOT:-.}/packaging/UPSTREAM"

_vfail=
version_fail() { echo "packaging/UPSTREAM: $*" >&2; _vfail=1; }

BASE_VER=${UPSTREAM_TAG#v}
echo "$BASE_VER" | grep -Eq '^[0-9]+(\.[0-9]+)*$' || version_fail "UPSTREAM_TAG=$UPSTREAM_TAG is not v<digits.digits>"
echo "$PKG_REV" | grep -Eq '^[1-9][0-9]*$' || version_fail "PKG_REV=$PKG_REV"
if [ -z "${FORK_VERSION:-}" ]; then
	[ -z "${FORK_RC:-}" ] || version_fail "FORK_RC=$FORK_RC needs FORK_VERSION"
	VER=$BASE_VER
	DEBVER="$VER-$PKG_REV"
	GIT_TAG="v$BASE_VER-$PKG_REV"
else
	echo "$FORK_VERSION" | grep -Eq '^sr[1-9][0-9]*$' || version_fail "FORK_VERSION=$FORK_VERSION is not srN"
	[ -z "${FORK_RC:-}" ] || echo "$FORK_RC" | grep -Eq '^rc[1-9][0-9]*$' || version_fail "FORK_RC=$FORK_RC is not rcN"
	[ "$PKG_REV" = 1 ] || version_fail "PKG_REV=$PKG_REV: with FORK_VERSION set, a change is a new srN/rcN and PKG_REV stays 1"
	VER="$BASE_VER+$FORK_VERSION${FORK_RC:+~$FORK_RC}"
	DEBVER="$VER-1"
	GIT_TAG="v$BASE_VER-$FORK_VERSION${FORK_RC:+-$FORK_RC}"
fi
[ -z "$_vfail" ] || exit 1
unset _vfail
