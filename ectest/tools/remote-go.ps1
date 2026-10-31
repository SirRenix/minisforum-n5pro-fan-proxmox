# remote-go.ps1 -- build/test the Go modules of this repository (go/n5 and
# ectest) via Docker on a Linux host reachable by an ssh alias. No Go
# toolchain is needed on the Windows side. Copy of n5-fangov's
# tools/remote-go.ps1, adapted to the two-module layout.
#
# Usage (pwsh 7; Windows PowerShell 5.1 corrupts the binary tar pipe):
#   ectest\tools\remote-go.ps1 -Id <name> [-BuildHost <alias>] [-Cmd "<sh>"] [-Image <img>]
#                              [-Fetch] [-FetchSources] [-FangovPath <dir>] [-Cleanup]
#
# Ships go/, ectest/, befunde/RESULTS.md, tools/f8a/ and tools/validation-watch/
# to ~/gobuild/<Id>/ on the host and runs Cmd in /src (the repository root).
# Default Cmd: gofmt check, vet and test of both modules, static build.
# -race in Cmd selects golang:1.26-bookworm (cgo), else golang:1.26-alpine.
# -Fetch        copies ectest/dist/n5-ectest back (and n5-ectest.sha256 and
#               the release tarball when ci-local.sh made them).
# -FetchSources copies *.go and go/n5/hwstatus/results.tsv back (after
#               gofmt -w / go generate in Cmd).
# -FangovPath   also ships an n5-fangov checkout to /src/n5-fangov (drift check).
# -Cleanup      removes ~/gobuild/<Id> on the host at the end (through Docker,
#               the files belong to root).
param(
    [Parameter(Mandatory=$true)][string]$Id,
    [string]$BuildHost = $(if ($env:N5ECTEST_BUILD_HOST) { $env:N5ECTEST_BUILD_HOST } else { "builder" }),
    [string]$Cmd = "",
    [string]$Image = "",
    [switch]$Fetch,
    [switch]$FetchSources,
    [string]$FangovPath = "",
    [switch]$Cleanup
)
$ErrorActionPreference = "Stop"
$repo = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
if ($Id -notmatch '^[a-zA-Z0-9_-]+$') { throw "Id must be [a-zA-Z0-9_-]" }
if ($BuildHost -notmatch '^[a-zA-Z0-9_.@-]+$') { throw "BuildHost must be an ssh alias or hostname" }
$cgo = "0"
if ($Cmd -match '-race') { $cgo = "1" }
if ($Image -eq "") {
    if ($cgo -eq "1") { $Image = "golang:1.26-bookworm" } else { $Image = "golang:1.26-alpine" }
}
if ($Image -notmatch '^[a-zA-Z0-9_./:@-]+$') { throw "Image must be a plain image reference" }
$remote = "gobuild/$Id"
if ($Cmd -eq "") {
    $Cmd = "test -z `"`$(gofmt -l go ectest)`" && (cd go/n5 && go vet ./... && go test ./...) && cd ectest && go vet ./... && go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o dist/n5-ectest ./cmd/n5-ectest && ls -la dist/n5-ectest"
}
Push-Location $repo
try {
    ssh $BuildHost "rm -rf ~/$remote 2>/dev/null; mkdir -p ~/$remote"
    tar -cf - --exclude .git --exclude dist go ectest befunde/RESULTS.md tools/f8a tools/validation-watch | ssh $BuildHost "tar -xf - -C ~/$remote"
} finally { Pop-Location }
if ($FangovPath -ne "") {
    $fp = (Resolve-Path $FangovPath).Path
    Push-Location $fp
    try {
        ssh $BuildHost "mkdir -p ~/$remote/n5-fangov"
        tar -cf - --exclude .git --exclude dist . | ssh $BuildHost "tar -xf - -C ~/$remote/n5-fangov"
    } finally { Pop-Location }
}
$escaped = $Cmd.Replace("'", "'\''")
$docker = "docker run --rm --pids-limit 2048 -v `$HOME/${remote}:/src -w /src -v n5fangov-gomod:/go/pkg/mod -v n5fangov-gocache:/root/.cache/go-build -e CGO_ENABLED=$cgo $Image sh -c '${escaped}'"
ssh $BuildHost $docker
$rc = $LASTEXITCODE
if ($rc -eq 0 -and $Fetch) {
    New-Item -ItemType Directory -Force (Join-Path $repo "ectest\dist") | Out-Null
    scp -q "${BuildHost}:$remote/ectest/dist/n5-ectest" (Join-Path $repo "ectest\dist\n5-ectest")
    Write-Host "fetched -> $(Join-Path $repo 'ectest\dist\n5-ectest')"
    # ci-local.sh also writes the sha256 list and the release tarball
    ssh $BuildHost "cd ~/$remote/ectest/dist && ls n5-ectest.sha256 n5-ectest-*-linux-amd64.tar.gz 2>/dev/null" | ForEach-Object {
        scp -q "${BuildHost}:$remote/ectest/dist/$_" (Join-Path $repo "ectest\dist\$_")
        Write-Host "fetched -> $(Join-Path $repo "ectest\dist\$_")"
    }
}
if ($rc -eq 0 -and $FetchSources) {
    Push-Location $repo
    try {
        ssh $BuildHost "cd ~/$remote && find go ectest -name '*.go' -o -name results.tsv | tar -cf - -T -" | tar -xf -
        Write-Host "fetched sources (*.go, results.tsv)"
    } finally { Pop-Location }
}
if ($Cleanup) {
    ssh $BuildHost "docker run --rm -v `$HOME/gobuild:/g $Image rm -rf /g/$Id && rmdir ~/gobuild 2>/dev/null; true"
}
exit $rc
