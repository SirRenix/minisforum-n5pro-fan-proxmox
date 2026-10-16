# validation-watch — long-term observation of the N5 Pro EC driver

A small, read-only watcher that records sensor data of `minisforum_n5_it5571`
and checks the kernel log, the fan regulator, the host and itself for signs of
trouble over a fixed period (default 7 days). Problems are reported by mail as
they happen; a report is mailed every morning and a final verdict at the end.

It is meant for real operation on a production host: run one period with the
current driver as a baseline, a second period with a changed driver, and
compare the two.

## What it does and does not do

- Reads sysfs/procfs, `journalctl` and `systemctl show`. Nothing else.
- Never writes to `/sys`, never does port I/O, never loads or unloads modules,
  never talks to the fan regulator. The only `systemctl` write is disabling
  its own timers at the end of the run.
- Reading `fanN_input`/`tempN_input` makes the driver talk to the EC. The
  sample interval is 30 s on purpose; do not lower it.
- Runs as three systemd timers plus an `OnFailure=` handler, all with
  `Nice=10`, `IOSchedulingClass=idle`, `MemoryMax=64M`, `CPUQuota=10%`,
  `ProtectSystem=strict`, `PrivateTmp=yes`, `NoNewPrivileges=yes`. Write
  access only to the data directory (drop-in `data-root.conf`, written by
  `install.sh`, with `RequiresMountsFor=`) and `/var/spool/postfix` for the
  mail-sending units. Sample and check units use `LogLevelMax=notice`, so
  the 30-s timer does not flood the journal; failures stay visible.

## Criteria

| Key | Checked | Threshold |
|---|---|---|
| K1 | kernel journal (all boots, `_TRANSPORT=kernel`) since the last check | `Oops`, `BUG:`, `kernel BUG at`, `general protection`, `Call Trace`, `INFO: task … blocked for more than`, `hung_task`, `soft lockup`, `hard LOCKUP`, `rcu…stall`, `watchdog: `, `Hardware Error`, `mce:`, `Machine check`, `AER:`, `PCIe Bus Error`, `nvme…(timeout\|reset)`, `minisforum…(timeout\|timed out\|fail\|error)`; known benign boot lines are ignored (`K1_IGNORE`); `watchdog did not stop` only when it belongs to an earlier boot (printed at shutdown) |
| K2 | `boot_id` changed (baseline = boot at install) | any reboot, planned or not; evidence includes the last journal lines of the previous boot and the content of pstore |
| K3 | fan regulator unit | not `active`, `NRestarts` increased, or journal lines with priority ≤ warning |
| K4 | samples since the last check | fan1..fan3 = 0 RPM in two consecutive samples; Tctl ≥ 85 °C; NVMe ≥ 70 °C; HDD ≥ 50 °C; EC CPU ≥ 90 °C; any failed EC read; EC hwmon missing in two consecutive samples |
| K5 | sampler timing | newest sample older than 3 × 30 s, a gap > 90 s between two samples (freeze indicator), or the clock stepping backwards |
| K6 | disk | run directory > 200 MB, or root fs < 5 GB free |
| K7 | `/proc/sys/kernel/tainted` | value differs from the value at start |
| K8 | the watcher itself | a watcher unit failed (`OnFailure=` mail), the journal could not be read (fallback to `--since`), `samples.csv` shrank; in the reports: sample or check coverage below 99 % of the run (planned downtime excluded) |

Temperature limits, fan stalls and "unit inactive" count as **episodes**: one
event when the condition starts, the next only after it has cleared. K1 counts
one event per matching log line, at most 300 per check run (the rest is one
summary event). **EC read errors (K4) count per sample**, not as episodes: a
persistently failing EC read gives 2,880 events per day; this is intended.

Upstream 0.2.1 does **not** log EC timeouts: `n5_wait_status()` only returns
`-ETIMEDOUT` to the caller, so they are visible only as `ec_read_errors` in the
samples (K4); the K1 `minisforum` pattern only hits probe failures and "failed to
restore PWM channel" on remove. From `0.2.1+sr1` on (patch 0005) every timeout is
also a kernel warning "EC timeout: port …", which K1 matches, as it matches
"failed to restore" (the sr1 shutdown warning "not set to" does not match and is
only in the journal of the previous boot). The warning is rate
limited (suppressed lines appear as "callbacks suppressed"), so the K1 count is a
lower bound; K4 stays the per-sample count.
A kernel panic never reaches the journal; after the reboot it shows up as K2
(with pstore content, if any) and K5.

Every event has a number (`#N`, its line in `events.log`). Alert mails:
subject `[n5-validation] ALERT <key> <short>`, at most one per key per hour;
events within that hour are sent as one summary afterwards. If `sendmail`
fails, the message is queued in `<run dir>/outbox/` and retried on the next
check. A failing watcher unit sends `[n5-validation] ALERT K8 Dienst <unit>
fehlgeschlagen` (at most one per unit per hour).

**Heartbeat:** the daily report at 07:00 is the only positive sign of life.
If it is missing, the watcher is not running (or mail is broken). Watch for
it from outside (e.g. a mail rule or Home Assistant/Pulse check at 07:30).

## Install

Copy this directory to the host and run as root:

    sudo ./install.sh --run-id baseline-20261008
    sudo ./install.sh --run-id patched-YYYYMMDD --duration-days 7   # second run
    sudo ./install.sh --run-id ... --data-root /mnt/ssd/n5-validation  # other disk

`install.sh` is idempotent. With the same run id it keeps the recorded start
time, taint and boot baseline (the run continues), also after `uninstall.sh`
(the values are read back from `config.snapshot`). It refuses a run id whose
run has finished. It stops the timers while replacing the scripts, writes
`/etc/n5-validation.conf`, installs scripts to `/usr/local/lib/n5-validation/`,
units and the `data-root.conf` drop-ins to `/etc/systemd/system/`, starts
`n5vw-sample.timer` (30 s), runs the first check synchronously and enables
`n5vw-check.timer` (5 min) and `n5vw-report.timer` (07:00).

The first check run sends `[n5-validation] START <run-id>` from inside the
sandboxed unit, so its arrival proves the mail path of the alerts.

After the period the check run sends `[n5-validation] ABSCHLUSS <run-id>: PASS|FAIL`
and disables the three timers (retried by every later check run if that
fails). Data stays.

## Data

`/var/lib/n5-validation/<run-id>/` (or `--data-root`):

| File | Content |
|---|---|
| `samples.csv` | one row per 30 s |
| `events.log` | `<epoch> <ISO time> <key> #<n> <detail>` per event |
| `checks.log` | epoch of every check run (K8 coverage) |
| `annotations.txt` | see below |
| `state` | journal cursors, last boot id, last checked sample line |
| `alerts/` | rate-limit state and pending evidence per key |
| `reports/` | `day-N.txt`, `final.txt` |
| `config.snapshot` | copy of the config at install time |

CSV header:

    time_iso,epoch,boot_id,uptime_s,load1,fan1_rpm,fan2_rpm,fan3_rpm,
    ec_temp1_cpu_c,ec_temp2_system_c,ec_temp3_board_c,ec_temp4_ambient_c,
    pwm1_cached,pwm2_cached,pwm3_cached,pwm4_cached,
    pwm1_enable_cached,pwm2_enable_cached,pwm3_enable_cached,pwm4_enable_cached,
    k10_tctl_c,nvme_max_c,hdd_max_c,ec_read_errors,fangov_state,fangov_nrestarts

(one line in the file). `pwm*` are the values cached by the driver, not an EC
readback. `ec_read_errors` counts fan/temp attributes of the EC driver that
could not be read in that sample; it is empty when the hwmon device is missing.
A missing sensor gives an empty field; the row is still written.

Expected size (measured row length 165–180 bytes): 2,880 rows/day ≈ 0.5 MB/day,
≈ 3.5–3.7 MB per 7-day run, plus ~3 KB/day `checks.log` and a few KB of events and
reports. Writes are one appended line every 30 s. A separate disk is not
needed for this volume.

## Planned reboots and other explained events

List the unexplained events with their numbers, then explain them **per
event**:

    sudo /usr/local/lib/n5-validation/annotate.sh --list
    sudo /usr/local/lib/n5-validation/annotate.sh --event 12,13 "planned reboot, kernel update"

Before a planned maintenance window you can declare the downtime; it is
excluded from the K8 coverage and explains K2/K5 events from its start until
10 min after its end:

    sudo /usr/local/lib/n5-validation/annotate.sh --down "2026-10-10 03:00" "2026-10-10 03:20" "planned reboot"

A key annotation (`annotate.sh --at TIME K2,K5 text`) explains events of those
keys within ±10 min (`ANNOT_WINDOW`). **K1 and K7 can only be explained per
event** — a shutdown oops or a freeze right after a planned reboot must not
disappear in a time window. The final report lists every event counted as
explained together with the annotation that covers it.

Verdict per criterion: PASS only if there were no events or every event is
explained; K8 additionally needs ≥ 99 % sample and check coverage.

## Status, stop, remove

    sudo /usr/local/lib/n5-validation/status.sh       # counts, coverage, statistics, open events
    sudo systemctl disable --now n5vw-sample.timer n5vw-check.timer n5vw-report.timer
    sudo ./uninstall.sh            # remove watcher, keep data
    sudo ./uninstall.sh --purge    # remove watcher and all data

## Dry run

    export N5VW_DRY_RUN=1 N5VW_DATA_ROOT=/tmp/n5vw-dry/data N5VW_CONF=/nonexistent
    sudo -E bin/n5vw-sample; sudo -E bin/n5vw-check; sudo -E bin/n5vw-report --daily

`N5VW_DRY_RUN=1` prints mails to stdout instead of calling `sendmail` and
never touches systemd units. Other overrides: `N5VW_RUN_ID`, `N5VW_START_EPOCH`.

Measured on the target host (N5 Pro, 2026-10-08): one sample takes about
0.05–0.06 s wall time and ~8 MB RSS; one check about 0.04 s.

## Limitations

- Absence of observed errors is not proof of absence. A PASS means "no
  unexplained error observed in N days under these conditions", nothing more.
- A hard freeze stops the watcher too. It shows up afterwards as K2 + K5 and
  whatever the previous boot managed to write to the journal.
- EC timeouts are only visible as failed reads in the samples (see above).
- If `/etc/n5-validation.conf` is removed, the units are skipped by their
  condition without a failure; only the missing daily report shows it.
- Thresholds are fixed per run; change them only between runs, otherwise the
  runs are not comparable.
- No lockdep/KASAN/kmemleak coverage — that needs a debug kernel.
