#!/bin/bash
# retro-k1-warning.sh - retro-check of an F8b run for kernel WARN() splats
# (TESTSPEC 0.4 #15): runs 1 and 2 used a K1_PATTERN without "WARNING:";
# from the next watcher run on K1 contains it. This script lists, for the
# window of a finished (or running) run, every kernel journal line matching
# "WARNING:|cut here" and says which of them the run's K1 pattern did not
# catch. Read-only: journalctl only; writes one raw file (+ .sha256) to --out.
#
# Usage:
#   retro-k1-warning.sh --label LABEL --run-dir DIR [--out DIR]
#   retro-k1-warning.sh --label LABEL --since TIME --until TIME [--out DIR]
#
#   --run-dir  run directory of the watcher (/var/lib/n5-validation/<run-id>);
#              window = START_EPOCH .. START_EPOCH + DURATION_DAYS (capped at
#              now), K1 pattern of the run from its config.snapshot
#   --since/--until  window as journalctl accepts it ("2026-10-08 22:25:44",
#              "@1760000000"); K1 pattern of runs 1/2 (built in below)
#   --out      directory for k1-retro-warning-<label>-<UTC>.txt (default .)
#
# Exit: 0 no line outside the run's K1 pattern, 1 such lines found (they
# are F8b hits the run did not record: annotate or count them), 2 usage or
# journal error, 3 the journal does not reach back to the window start
# (result incomplete).
set -euo pipefail

RETRO_VERSION=0.1.0
# K1_PATTERN as shipped for runs 1 and 2 (before TESTSPEC 0.4 #15)
K1_RUN12='Oops|BUG:|kernel BUG at|general protection|Call Trace|INFO: task .* blocked for more than|hung_task|soft lockup|hard LOCKUP|rcu.*stall|watchdog: |Hardware Error|mce:|Machine check|AER:|PCIe Bus Error|nvme.*(timeout|reset)|minisforum.*(timed? ?out|fail|error)'
RETRO_PATTERN='WARNING:|cut here'

usage() { sed -n '9,23p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 2; }
die() { printf 'retro-k1-warning: %s\n' "$*" >&2; exit 2; }

LABEL='' RUN_DIR='' SINCE='' UNTIL='' OUT=.
while [ $# -gt 0 ]; do
    case "$1" in
        --label) LABEL=${2:-}; shift 2 ;;
        --run-dir) RUN_DIR=${2:-}; shift 2 ;;
        --since) SINCE=${2:-}; shift 2 ;;
        --until) UNTIL=${2:-}; shift 2 ;;
        --out) OUT=${2:-}; shift 2 ;;
        -h|--help) usage ;;
        *) usage ;;
    esac
done
[[ "$LABEL" =~ ^[A-Za-z0-9._-]+$ ]] || die "--label is required ([A-Za-z0-9._-])"
if [ ! -d "$OUT" ] || [ ! -w "$OUT" ]; then die "--out $OUT is not a writable directory"; fi
command -v journalctl >/dev/null || die "journalctl not found"

K1_RUN=$K1_RUN12
K1_SOURCE="built-in pattern of runs 1/2"
if [ -n "$RUN_DIR" ]; then
    [ -z "$SINCE$UNTIL" ] || die "--run-dir and --since/--until exclude each other"
    snap=$RUN_DIR/config.snapshot
    [ -r "$snap" ] || die "$snap not readable"
    # values only, never source the file
    get() { sed -n "s/^$1=//p" "$snap" | tail -n 1 | sed "s/^['\"]//; s/['\"]\$//"; }
    start=$(get START_EPOCH) days=$(get DURATION_DAYS) pat=$(get K1_PATTERN)
    [[ "$start" =~ ^[0-9]+$ ]] || die "START_EPOCH missing in $snap"
    [[ "$days" =~ ^[0-9]+$ ]] || die "DURATION_DAYS missing in $snap"
    end=$((start + days * 86400))
    now=$(date +%s)
    [ "$end" -le "$now" ] || end=$now
    SINCE="@$start" UNTIL="@$end"
    if [ -n "$pat" ]; then K1_RUN=$pat; K1_SOURCE="K1_PATTERN of $snap"; fi
else
    if [ -z "$SINCE" ] || [ -z "$UNTIL" ]; then die "give --run-dir, or --since and --until"; fi
fi

since_ep=$(date -d "$SINCE" +%s 2>/dev/null) || die "--since '$SINCE' not understood by date(1)"
until_ep=$(date -d "$UNTIL" +%s 2>/dev/null) || die "--until '$UNTIL' not understood by date(1)"
[ "$since_ep" -lt "$until_ep" ] || die "window is empty ($SINCE .. $UNTIL)"

utc=$(date -u +%Y%m%dT%H%M%SZ)
f=$OUT/k1-retro-warning-$LABEL-$utc.txt
err=$(mktemp)
trap 'rm -f "$err"' EXIT

# earliest kernel line in the journal: the check is complete only if the
# journal reaches back to the window start
first=$(journalctl -q --no-pager -o short-unix _TRANSPORT=kernel 2>"$err" | head -n 1 || true)
first_ep=${first%% *}
first_ep=${first_ep%%.*}

# the window, host name removed (field 2 of short-unix)
lines=$(journalctl -q --no-pager -o short-unix _TRANSPORT=kernel --since="@$since_ep" --until="@$until_ep" 2>"$err") \
    || die "journalctl failed: $(head -c 200 "$err")"
hits=$(printf '%s\n' "$lines" | awk -v pat="$RETRO_PATTERN" -v k1="$K1_RUN" '
    {
        if ($1 ~ /^[0-9]+(\.[0-9]+)?$/) { ep = $1; sub(/\..*/, "", ep); msg = $0; sub(/^[^ ]+ [^ ]+ /, "", msg) }
        else msg = $0
        sub(/^kernel: /, "", msg)
    }
    msg ~ pat { printf "%s\t%s\t%s\n", ep, (msg ~ k1 ? "in-run-K1" : "NEW"), msg }')

n_all=0 n_new=0
[ -z "$hits" ] || n_all=$(printf '%s\n' "$hits" | wc -l)
[ -z "$hits" ] || n_new=$(printf '%s\n' "$hits" | awk -F'\t' '$2 == "NEW"' | wc -l)
n_lines=0
[ -z "$lines" ] || n_lines=$(printf '%s\n' "$lines" | wc -l)

complete=yes
if ! [[ "$first_ep" =~ ^[0-9]+$ ]] || [ "$first_ep" -gt "$since_ep" ]; then complete=no; fi

{
    printf 'tool: retro-k1-warning.sh %s (TESTSPEC 0.4 #15)\n' "$RETRO_VERSION"
    printf 'label: %s\n' "$LABEL"
    printf 'date_utc: %s\n' "$utc"
    printf 'window: %s .. %s (%s .. %s UTC)\n' "$since_ep" "$until_ep" \
        "$(date -u -d "@$since_ep" '+%F %T')" "$(date -u -d "@$until_ep" '+%F %T')"
    printf 'journal: kernel lines in the window %s; earliest kernel line in the journal %s; complete=%s\n' \
        "$n_lines" "${first_ep:-none}" "$complete"
    printf 'retro pattern: %s\n' "$RETRO_PATTERN"
    printf 'run K1 pattern (%s): %s\n' "$K1_SOURCE" "$K1_RUN"
    echo '--- boots (journalctl --list-boots) ---'
    journalctl --list-boots --no-pager 2>/dev/null | sed 's/ \{2,\}/ /g' || echo '(not available)'
    echo '--- hits: epoch, ISO UTC, in-run-K1|NEW, kernel line (host name removed) ---'
    if [ -n "$hits" ]; then
        while IFS=$'\t' read -r ep cls msg; do
            printf '%s %s %s %s\n' "$ep" "$(date -u -d "@$ep" '+%FT%TZ')" "$cls" "$msg"
        done <<< "$hits"
    else
        echo '(none)'
    fi
    echo '---'
    printf 'RESULT %s: %s line(s) matching "%s", %s of them not caught by the run K1 pattern (NEW); journal complete=%s\n' \
        "$LABEL" "$n_all" "$RETRO_PATTERN" "$n_new" "$complete"
} > "$f"
( cd "$(dirname "$f")" && sha256sum "$(basename "$f")" > "$(basename "$f").sha256" )

tail -n 1 "$f"
echo "raw file: $f (+ .sha256); check it for addresses before publishing"
if [ "$complete" != yes ]; then exit 3; fi
if [ "$n_new" -gt 0 ]; then exit 1; fi
exit 0
