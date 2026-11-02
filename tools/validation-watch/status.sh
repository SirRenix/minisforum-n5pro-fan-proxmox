#!/bin/bash
# status.sh - print the current state of the run (counts per criterion,
# sensor statistics over the whole run, recent events). No mail, no writes.
set -euo pipefail
self=$(dirname "$(readlink -f "$0")")
if [ -x "$self/n5vw-report" ]; then exec "$self/n5vw-report" --status; fi
exec "$self/bin/n5vw-report" --status
