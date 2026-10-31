#!/bin/sh
# drift-check.sh - phase-1 copy-drift check (TESTKIT-DESIGN 2): fails when
# one of the n5-fangov sources that were copied into go/n5 and ectest changed
# since the copy (drift-baseline.sha256). Then port the change and renew the
# baseline. Local check, not CI (n5-fangov is another repository):
#   pwsh ectest/tools/remote-go.ps1 -Id drift -BuildHost <host> -FangovPath ..\n5-fangov -Cmd "sh ectest/tools/drift-check.sh n5-fangov"
set -eu
base=$(cd "$(dirname "$0")" && pwd)/drift-baseline.sha256
cd "$1"
if grep -v '^#' "$base" | sha256sum -c --quiet -; then
	echo "drift check: n5-fangov sources unchanged since the copy"
else
	echo "drift check: n5-fangov changed a copied file (above): port the change to go/n5 / ectest, then renew drift-baseline.sha256" >&2
	exit 1
fi
