#!/bin/bash
# annotate.sh - mark events as explained (e.g. a planned reboot).
#
# usage:
#   annotate.sh --list                     unexplained events with their numbers
#   annotate.sh --event N[,M...] TEXT...   explain exactly these events (any key)
#   annotate.sh [--at TIME] KEYS TEXT...   explain events of KEYS (K2..K6, K8)
#                                          within +-ANNOT_WINDOW (default 600 s)
#                                          of TIME (default now)
#   annotate.sh --down FROM TO TEXT...     planned downtime: excluded from the
#                                          K8 coverage, explains K2/K5 events
#                                          from FROM to TO + ANNOT_WINDOW
# K1 (kernel log) and K7 (taint) can only be explained per event (--event).
# TIME/FROM/TO: anything "date -d" accepts. Appends one line to
# <run dir>/annotations.txt.
set -euo pipefail

self=$(dirname "$(readlink -f "$0")")
lib=$self/n5vw-lib.sh
[ -r "$lib" ] || lib=$self/bin/n5vw-lib.sh
# shellcheck source=bin/n5vw-lib.sh
. "$lib"
vw_load_config

usage() { sed -n '2,17p' "$0"; exit 2; }
iso() { date -d "$1" -Iseconds 2>/dev/null || { echo "unparsable time: $1" >&2; exit 2; }; }

mode=key at=now
case "${1:-}" in
    --list)
        vw_event_table open 0 | cut -d' ' -f2- | grep . || echo "keine unerklaerten Ereignisse"
        exit 0 ;;
    --event) mode=event; [ $# -ge 3 ] || usage; refs=$2; shift 2 ;;
    --down) mode=down; [ $# -ge 4 ] || usage; from=$(iso "$2"); to=$(iso "$3"); shift 3 ;;
    --at) [ $# -ge 4 ] || usage; at=$2; shift 2 ;;
    ''|-h|--help) usage ;;
esac

case "$mode" in
    event)
        tok=
        IFS=, read -r -a ra <<< "$refs"
        for r in "${ra[@]}"; do
            r=${r#\#}
            case "$r" in ''|*[!0-9]*) echo "invalid event number: $r" >&2; exit 2 ;; esac
            if ! awk -v id="#$r" '$4 == id { f = 1 } END { exit !f }' "$EVENTS" 2>/dev/null; then
                echo "no event #$r in $EVENTS" >&2; exit 2
            fi
            tok="${tok:+$tok,}#$r"
        done
        line="$(date -Iseconds) $tok"
        ;;
    down)
        if [ "$(date -d "$to" +%s)" -lt "$(date -d "$from" +%s)" ]; then echo "TO before FROM" >&2; exit 2; fi
        line="$from DOWN $to"
        ;;
    key)
        [ $# -ge 2 ] || usage
        keys=$1; shift
        case ",$keys," in *,,*) echo "invalid keys: $keys" >&2; exit 2 ;; esac
        IFS=, read -r -a ka <<< "$keys"
        for k in "${ka[@]}"; do
            case " $N5VW_KEYS " in *" $k "*) ;; *) echo "unknown key: $k" >&2; exit 2 ;; esac
            case " $ANNOT_EXPLICIT_ONLY " in
                *" $k "*) echo "$k can only be explained per event: annotate.sh --event <nr> ..." >&2; exit 2 ;;
            esac
        done
        line="$(iso "$at") $keys"
        ;;
esac
[ $# -ge 1 ] || usage
text="$*"
text=${text//$'\n'/ }
mkdir -p "$RUN_DIR"
printf '%s %s\n' "$line" "$text" >> "$ANNOT"
echo "annotated in $ANNOT:"
tail -n 1 "$ANNOT"
