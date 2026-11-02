#!/bin/sh
# ci-local.sh - the checks of .github/workflows/ectest.yml without git, for
# a run through tools/remote-go.ps1 (golang:1.26-bookworm, repository root
# as working directory):
#   pwsh ectest/tools/remote-go.ps1 -Id ci -BuildHost <host> -Cmd "sh ectest/tools/ci-local.sh -race" -Fetch
set -eu
race=${1:-}
go install honnef.co/go/tools/cmd/staticcheck@v0.8.1
go install golang.org/x/vuln/cmd/govulncheck@v1.8.0
PATH=$PATH:$(go env GOPATH)/bin

echo "== gofmt"
out=$(gofmt -l go ectest)
[ -z "$out" ] || { echo "not gofmt-formatted: $out"; exit 1; }

echo "== results.tsv generated from befunde/RESULTS.md"
cp go/n5/hwstatus/results.tsv /tmp/results.tsv.before
(cd go/n5 && go generate ./...)
cmp /tmp/results.tsv.before go/n5/hwstatus/results.tsv || { echo "results.tsv is stale"; exit 1; }

for m in go/n5 ectest; do
	echo "== $m: vet, staticcheck, govulncheck, test $race"
	(cd "$m" && go vet ./... && staticcheck ./... && govulncheck ./... && go test $race -count=1 -timeout 600s ./...)
done

echo "== non-root paths (permission messages, as nobody)"
(cd go/n5 && setpriv --reuid=65534 --regid=65534 --clear-groups env HOME=/tmp GOCACHE=/tmp/gocache-nobody GOPATH=/tmp/gopath-nobody GOFLAGS=-buildvcs=false \
	go test -count=1 -run '^TestNotRootMessages$' -v ./identity) | tee /tmp/nonroot.txt
grep -q -- '--- PASS: TestNotRootMessages' /tmp/nonroot.txt

echo "== static build"
ver=${N5ECTEST_VERSION:-0.1.0-dev}
(cd ectest && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
	-ldflags="-s -w -X main.version=$ver -X main.commit=${N5ECTEST_COMMIT:-local}" \
	-o dist/n5-ectest ./cmd/n5-ectest && ./dist/n5-ectest version)
pkg=n5-ectest-$ver-linux-amd64
(cd ectest && mkdir -p "dist/$pkg/contrib" && cp dist/n5-ectest README.md "dist/$pkg/" && cp -r contrib/systemd "dist/$pkg/contrib/" &&
	tar -C dist --owner=0 --group=0 -czf "dist/$pkg.tar.gz" "$pkg" && rm -r "dist/$pkg" &&
	cd dist && sha256sum n5-ectest "$pkg.tar.gz" > n5-ectest.sha256 && cat n5-ectest.sha256)

echo "== f8a-snapshot.sh --diff on n5-ectest snapshot fixtures"
fmt=$(mktemp -d)
(cd ectest && N5ECTEST_FORMAT_OUT="$fmt" go test -count=1 -run '^TestFormatFixtures$' ./internal/snapshot >/dev/null)
fail=0
while read -r a b want; do
	rc=0; bash tools/f8a/f8a-snapshot.sh --diff "$fmt/$a" "$fmt/$b" >/dev/null || rc=$?
	rc2=0; ectest/dist/n5-ectest diff "$fmt/$a" "$fmt/$b" >/dev/null || rc2=$?
	echo "$a $b: f8a $rc, n5-ectest $rc2, expected $want"
	if [ "$rc" != "$want" ] || [ "$rc2" != "$want" ]; then fail=1; fi
done < "$fmt/expected.txt"
[ "$fail" = 0 ]
echo "== all checks passed"
