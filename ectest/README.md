# n5-ectest

Test toolset for the fan EC (ITE IT5571) of the Minisforum N5 family. It runs the tests of
[`docs/TESTSPEC.md`](../docs/TESTSPEC.md) (version 0.4), logs for days, and writes result
bundles in one raw format with an HTML report, so results from different boxes compare
without questions.

One static binary. Read-only, except `map` and `handback`, which you run at the device.

## Install

```
V=0.1.0   # the release you install
curl -fLO https://github.com/SirRenix/minisforum-n5pro-fan-proxmox/releases/download/ectest/v$V/n5-ectest-$V-linux-amd64.tar.gz
curl -fLO https://github.com/SirRenix/minisforum-n5pro-fan-proxmox/releases/download/ectest/v$V/n5-ectest.sha256
sha256sum -c n5-ectest.sha256 --ignore-missing
tar xzf n5-ectest-$V-linux-amd64.tar.gz && cd n5-ectest-$V-linux-amd64
sudo install -m 0755 n5-ectest /usr/local/bin/n5-ectest
sudo install -D -m 0644 README.md /usr/local/share/doc/n5-ectest/README.md
sudo n5-ectest identify
```

The tarball holds the binary, this README and `contrib/systemd/` (units for `log`). The
binary alone is also attached to the release. To build it yourself:
`cd ectest && CGO_ENABLED=0 go build -trimpath -o n5-ectest ./cmd/n5-ectest`.

You need the driver `minisforum_n5_it5571` (DKMS package of this repository), x86-64,
systemd, kernel 6.1 or newer. Developed on Proxmox VE 9 (Debian 13); plain Debian and
Ubuntu are untested. With Secure Boot (Ubuntu) the module needs a MOK-signed build: see
[Secure Boot, lockdown, taint](../README.md#secure-boot-lockdown-taint).

The duty registers (DCR), which every writing test is judged on, need driver `0.2.1+sr1`
loaded with `debug_regs=1`:

```
sudo systemctl stop n5-fangov            # if you run it
sudo modprobe -r minisforum_n5_it5571 && sudo modprobe minisforum_n5_it5571 debug_regs=1
```

When you are done, load it without `debug_regs` and start your regulator again:

```
sudo modprobe -r minisforum_n5_it5571 && sudo modprobe minisforum_n5_it5571
sudo systemctl start n5-fangov           # if you run it
```

`debug_regs` must not stay in `modprobe.d`.

`n5-ectest` without arguments lists the commands; `n5-ectest <command> -h` shows the flags.

## What runs on your box

| Board (DMI product / board) | Commands |
|---|---|
| `N5 PRO` / `F8NAA` | all; `map`/`handback` only with the write conditions below |
| `N5 PRO` / `F8NAB`, `N5` / `F8NAA`, `N5A` or `N5 AIR` / `F8NAB` | `identify`, `snapshot`, `log`, `diff`, `report` (writing tests report a gate failure and write nothing) |
| anything else | `identify`, `diff`, `report` |

Write conditions (all of them, else nothing is written and the bundle says why): N5 PRO /
F8NAA, DMI `sys_vendor` `Micro Computer (HK) Tech Limited`, driver version with `+sr1`,
module parameter `force=N`, `dcr` readable, the channel's `pwmN` exposed, not in a container.
`sudo n5-ectest identify` shows the result per channel.

## identify

```
sudo n5-ectest identify
sudo n5-ectest identify --json
```

Collects: DMI (no serials, no UUID), BIOS and EC firmware version, kernel, taint, OS,
lockdown, container, driver version/srcversion/parameters, DKMS package version, the DCR
dump, the hardware status of your board. Takes a second. Without root it runs too, but
cannot read `dcr` and the container state and does not judge the write conditions.

Post: the plain output, with your issue.

## snapshot

```
sudo n5-ectest snapshot cold --boot cold
```

Collects (mode B, driver loaded): fan1–4 and EC temperatures from hwmon, the pwm cache,
DCR1–5 from `dcr`, Tctl/NVMe/HDD, loaded modules, reserved EC ports, module parameters,
`modprobe.d` entries. Touches no I/O port. Takes under a minute. Writes the bundle
`<YYYYMMDD-HHMM>-<board>-<bios>-snapshot-<label>/` (time in UTC) with `snapshot.txt`
(format of `tools/f8a/f8a-snapshot.sh`), T6 as `REPORTED`.

Mode A (module unloaded: EC RAM, I2EC, tachs through the ports) is not part of this
release. Use `tools/f8a/f8a-snapshot.sh LABEL` for it; `n5-ectest diff` reads its files.
Under kernel lockdown `integrity` or `confidentiality` (for example Ubuntu with Secure
Boot) mode A is impossible; C1 and the "before load" part of T6 are then `INCONCLUSIVE`.

T6 "before load" needs one boot without the module, because the package loads it at
every boot. Add to the kernel command line for that boot (GRUB: `e` at the menu):

```
module_blacklist=minisforum_n5_it5571
```

Post: the bundle as `.tar.gz` (see [What to post](#what-to-post)).

## diff

```
n5-ectest diff BEFORE AFTER
```

Compares two snapshots (files or bundle directories): EC RAM bytes, the curve tables
0x10–0x27, 0x30–0x38, 0x70–0x81 (TESTSPEC C1) and I2EC. Exit 0 = curve tables identical,
1 = CHANGED (offsets listed), 2 = incomplete input (no complete EC RAM dump, a file marked
`INCOMPLETE`, `??` in a table byte). `f8a-snapshot.sh --diff` gives the same 0/1/2 on
complete files; it does not check `INCOMPLETE` and `??`.

Post: the output, with both snapshot files.

## map

```
sudo n5-ectest map pwm3 --boot warm --out /root/runs
```

T1 channel mapping: 5 min gate window without writes, 60 s rest, then 255 → 217 → 179 →
140, 60 s each. About 11 min. Writes one channel, never a duty below 100. Type
`VALIDATE START` at the device; piped input and remote sessions are refused. Verdict from
DCR, tachs as second measure. Samples are due every 5 s from the write; a DCR sample
before the settle time (pwm1–3 10 s, pwm4 1 s; TESTSPEC §2.1) is recorded and listed in
`verdict.json` (`not_judged`), not judged. When another channel's table input (EC 0x09,
0x05, 0x06) crosses a table step by 2 °C, T1 is `INCONCLUSIVE`, not `FAIL`, also when
every other tach stays within ±50 rpm (TESTSPEC §5.1). G6 reads the curve tables through
the driver's debugfs `ecram` (0.2.1+sr2, `debug_regs=1`) before the first write
(`meta.json` → `ec_ram_tables`); without it G6 is `PARTIAL`. Bundle `…-validate-pwm3/`.

The gate window G4 (5 min) needs EC 0x04, EC 0x06 and the HDD temperature to change < 1 °C
and every limit sensor (Tctl, NVMe, HDD) to have a value; Tctl is a limit only, its noise
on a host with running guests does not block. Without the board/vendor/driver gate (G7)
the window is skipped.

Run it on the console (tty1, KVM, serial). In `tmux` started on the console it runs too:
the tool then checks the attached tmux clients, and refuses when one of them comes over
SSH. Add `--i-am-at-the-console` only when you sit at the box and the tool cannot see it
(the flag is recorded in the bundle).

Post: the bundle as `.tar.gz` and its `.report.html`.

## handback

```
sudo n5-ectest handback pwm1 --boot warm --out /root/runs
```

T2 hand-back and T3 settling: 5 min gate window, auto value A (15 min), then 3 runs of the
sub-runs H = 255 and M = min(A + 48, 200): write, hold 60 s, auto, log 17 min. About 2 h
10 min per channel. Samples come on a fixed 5-s cadence from the auto command, so the
12th sample is at 60 s; a read within ±0.5 s of its slot counts at the slot (`t_s`), the
real time is in `t_iso`. Hold samples after the settle time that differ from the written
value are listed in `verdict.json` (`hold_deviations`), not judged. A sub-run whose table
input crosses a table step by 2 °C (or the A window, then all sub-runs) is
`INCONCLUSIVE`. pwm4 has no EC automatic: T2/T3 `N/A`, nothing written. T4/T5 are
reported `INCONCLUSIVE`: the tool does not evaluate them yet (spec deviation; by hand per
TESTSPEC §5.5).

Post: the bundle as `.tar.gz` and its `.report.html`.

Both writing commands:

- Refuse when n5-fangov runs or restarts, when another pwm writer holds
  `/run/n5-fangov/writer.lock` (n5-fangov ≥ 0.5, `tools/f8a/f8a-pcie.sh`,
  `f8a-stress.sh`), or when an earlier run left a channel unrestored. Abort on
  Tctl ≥ 85 °C, NVMe ≥ 70 °C, HDD ≥ 50 °C, a tach at 0 rpm, a limit sensor lost for 3
  samples, or another pwm writer (fancontrol, fan2go, coolercontrold, thinkfan, n5-fand,
  the f8a scripts, a process holding a pwm file, a foreign write to the pwm cache, the
  writer lock removed).
- Put the channel back into its restore value at the end, on abort and on Ctrl-C, Ctrl-\,
  a hangup or SIGTERM: the `stop` of `/etc/n5-fangov/config.toml` if present, else pwm1–3
  `auto`; pwm4 always 255 (the PCIe channel has no EC automatic; a fixed `stop` other than
  255 is recorded as a spec deviation). `--restore pwmN=auto|D` overrides (recorded in
  `restore_values`; pwm4 only 255). A fixed restore duty is read back after the settle
  time. After an abort on a temperature limit the channel of that sensor (Tctl → pwm1,
  NVMe → pwm2, HDD → pwm3) is set to 255 and stays there, when the run has written
  (TESTSPEC §4); `restore_values` names it with the reason. Ctrl-Z is ignored.
- Run as two processes. If the worker is killed, the guardian restores. If both are
  killed, run `sudo n5-ectest restore`.
- Exit 0 also when a verdict is `FAIL` or `INCONCLUSIVE`: the exit code says whether the
  run went through, the verdicts are in the output and in `verdict.json`.

`--dry-run` runs the same procedure against a built-in EC model in seconds; its bundle is
marked and is not a result.

## log

```
sudo n5-ectest log --duration 7d --mail-to root
sudo n5-ectest log --interval 5s --duration 2h      # raw data for T4/T5
```

Collects every 30 s (`--interval`, 5–60 s): the TESTSPEC columns (DCR1–5, fan1–3, EC
temperatures, Tctl, NVMe, HDD, load) into `samples.csv`; boot id, uptime, pwm cache, EC
read errors, n5-fangov state into `aux.csv`. Checks K1–K7 of TESTSPEC §5a while it runs (K1 with `WARNING:`, TESTSPEC 0.4 #15)
(kernel log, reboot, n5-fangov, fans/temperatures/EC errors, gaps > 90 s, disk, taint),
mails each hit (one mail per criterion and hour), a daily summary and an end summary.
Kernel, driver version, srcversion and parameters are recorded at the start, on every
resume and after a reboot. Without `--mail-to` or a mail command the mails go to
`<run>.outbox/` beside the run directory and to stdout. Read-only. Root is needed for
`dcr` (debugfs) and for the default `--out /var/lib/n5-ectest`.

As a service, with an outside check every 5 min that mails when no sample came for 11 min
(from the tarball directory):

```
sudo install -m 0644 contrib/systemd/n5-ectest-* /etc/systemd/system/
sudo install -D -m 0644 contrib/systemd/log.env.example /etc/n5-ectest/log.env
sudo systemctl daemon-reload
sudo systemctl enable --now n5-ectest-log@run1.service n5-ectest-check@run1.timer
```

After the end of the run:

```
sudo systemctl disable n5-ectest-log@run1.service n5-ectest-check@run1.timer
```

Planned events (a reboot, a stopped service):

```
sudo n5-ectest log annotate run1 --event 12 "planned reboot, kernel update"
sudo n5-ectest log annotate run1 --down 2026-10-12T20:00:00Z 2026-10-12T21:00:00Z "maintenance window"
```

Data: `/var/lib/n5-ectest/<YYYYMMDD-HHMM>-<board>-<bios>-log-<run-id>/`. Check that
`dcr1`–`dcr5` are filled in `samples.csv`; if not, `events.log` has a K8 line: the driver
runs without `debug_regs` or the unit cannot read debugfs.

Post: the run directory as `.tar.gz` and its `.report.html` (not the `.outbox`: the queued
mails carry your address).

## report

```
n5-ectest report /root/runs/20261012-2105-F8NAA-1.05-validate-pwm3
```

Recomputes every verdict (with its runs and sub-runs) from `samples.csv`, `events.log` and
`meta.json`, compares it with `verdict.json`, recomputes G3/G4 from the gate window and
compares them with `meta.json`, checks `SHA256SUMS`, and writes `<bundle>.report.html`
beside the bundle: verdicts with their numbers, provenance, `spec_deviations`,
DCR/tach/temperature charts with the limits, per T2/T3 sub-run the DCR after the auto
command with A, the ±2/±16 bands and the 60 s / 900 s marks, for log runs the K1–K7
counts. One file, opens offline. Exit 1 on a mismatch, a checksum problem, or a run that
did not finish (killed: only the provisional `meta.json` written at the start).

## restore

```
sudo n5-ectest restore
sudo n5-ectest restore --from /root/runs/<bundle>
sudo n5-ectest restore --discard
```

Puts the channels of an interrupted run back into their restore values. Checks first that
n5-fangov is stopped, no run holds the writer lock, the values are `auto` or ≥ 100 and the
driver device is the one of the run. `--from` uses the `restore.json` of a bundle, only
when no run state is open. After a module reload the device differs and `restore` refuses;
`--discard` then shows the channels, asks you to type `DISCARD` and gives the run state up
without writing. Set the channels yourself afterwards (`pwmN_enable=2` or start
n5-fangov).

## What to post

```
tar czf 20261012-2105-F8NAA-1.05-validate-pwm3.tar.gz 20261012-2105-F8NAA-1.05-validate-pwm3/
```

Post the `.tar.gz` and the `.report.html`. Nothing else is needed: the bundle has
`meta.json` (identity, provenance with the binary's sha256, gates, restore values,
`spec_deviations`), `samples.csv` (TESTSPEC §8 columns), `events.log`, `verdict.json` and
`SHA256SUMS` (over every file, subdirectories included). Redacted in every file: host name
(`n5host`), DMI serials and UUID (never read), MAC and IP addresses, mail addresses,
`serial: …` values, `/home/<user>`; user names and `SSH_*` are never recorded. The rule
set is in `meta.json` → `redaction`.

## Results and the spec

Every bundle lists in `spec_deviations` where the tool knowingly differs from TESTSPEC 0.4
(§1 rule 6): always the open conditions of rule 4 (#1 D4 cross-check, #4 `report` against
a hand evaluation of T4/T5); for `handback` T4/T5 not evaluated; a fixed pwm4 `stop`
other than 255. Quote the list with a verdict. Dry runs and runs against a test tree
(`N5ECTEST_ROOT`) are not results.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | done; also when a verdict is `FAIL` or `INCONCLUSIVE`; `-h` |
| 1 | refused (gate before the run, not root, n5-fangov running, writer lock held), aborted (limit, other writer, signal), writing tests not run (gate failure, no `dcr`), restore failed, `report`: mismatch, checksum problem or unfinished run, `log check`: no sample for > 11 min |
| 3 | `snapshot` incomplete |
| 64 | usage |

`diff`: 0 identical, 1 CHANGED, 2 incomplete input, 64 usage.

## Development

```
pwsh ectest/tools/remote-go.ps1 -Id dev -BuildHost <docker-host> -Cmd "sh ectest/tools/ci-local.sh -race" -Fetch -Cleanup
pwsh ectest/tools/remote-go.ps1 -Id drift -BuildHost <docker-host> -FangovPath ..\n5-fangov -Cmd "sh ectest/tools/drift-check.sh n5-fangov" -Cleanup
```

Two modules: `go/n5` (shared: `n5driver`, `hwstatus`, `writerlock`, `identity`) and
`ectest`. `go generate ./...` in `go/n5` rebuilds `hwstatus/results.tsv` from
`befunde/RESULTS.md`. Tests run against a fake host tree (`internal/fake`, `N5ECTEST_ROOT`)
and the EC model of `--dry-run`; CI also feeds n5-ectest snapshots to
`f8a-snapshot.sh --diff` and runs the permission paths as `nobody`. Design:
[`docs/TESTKIT-DESIGN.md`](../docs/TESTKIT-DESIGN.md).
