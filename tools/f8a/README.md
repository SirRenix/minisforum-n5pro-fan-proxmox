# f8a — maintenance-window helpers (TESTSPEC 0.3 F8a, M1, M2, T6)

Three scripts for the driver validation window on an N5 Pro (`N5 PRO` / `F8NAA`,
driver `minisforum_n5_it5571` 0.2.1+sr1~rc2). Run as root, with `n5-fangov`
stopped. The writing scripts refuse on any other DMI product/board/vendor; the
read-only snapshot also runs on the other N5-family boards
([Snapshot on another N5 board](#snapshot-on-another-n5-board)). All refuse unless
`systemctl is-active n5-fangov` answers `inactive` or `failed` (an unreadable
state refuses too).

| Script | Writes? | Run card | Who |
|---|---|---|---|
| `f8a-snapshot.sh LABEL [--yes]` | no | A4, F3, F4 (M1/T6) | remote or console |
| `f8a-snapshot.sh --diff A B` | no (offline) | F1, C1 | anyone |
| `f8a-stress.sh load\|unbind [--cycles N]` | pwm1/pwm2, module reload | E1, E2 | operator at the console |
| `f8a-pcie.sh` | pwm4, module reload | F1 (M2) | operator at the console |

The writing scripts need an interactive terminal and the typed script name.
**Start them inside `tmux`** (KVM console on tty1, or SSH into a tmux session): a lost
SSH session sends SIGHUP; the scripts then still restore and write their log,
but you lose the live output, and a second session could start a second writer.

Install on the host (run card V5):

```bash
scp -r tools/f8a n5host:/tmp/ && ssh n5host sudo cp -r /tmp/f8a /root/n5-mw1/f8a
ssh n5host sudo bash -n /root/n5-mw1/f8a/f8a-*.sh
```

Every run writes `/root/f8a-raw/<script>-<label>-<UTC>.txt` plus `.sha256` (other directory:
`F8A_OUT_DIR=/path`);
the host name is replaced by `n5host`. Copy both to `befunde/raw/`.

Common limits (sources independent of the EC driver): k10temp Tctl ≥ 85 °C,
NVMe ≥ 70 °C, drivetemp ≥ 50 °C, and fan1..fan3 at 0 rpm (pwm4 has no tach;
a 0 is re-read twice, 1 s apart) abort. A duty below 100 is never written.
The writing scripts run a watcher that checks the temperatures every second,
also while the script waits in `modprobe` or `unbind`; it cannot interrupt a
hung kernel call, it prints `!!! LIMIT (watcher)` every 5 s — then act by hand
(run card R3/R4).

Kernel log check (since script start, `journalctl -k --since @start`):
`Oops|BUG:|kernel BUG|WARNING:|Call Trace|general protection|hung_task|blocked
for more than|soft lockup|LOCKUP|rcu.*stall|EC timeout|Unknown symbol|
minisforum.*(fail|error|timeout|timed out|not created|not set to)|[minisforum_n5_it5571]`.
The one-time "module verification failed" line of an unsigned module is
ignored. Every hit aborts the run and is FAIL (TESTSPEC 0.3 5a, same rule as
F8b): hits that name the driver (or a stack frame of it, or `EC timeout`) give
`FAIL (N kernel-log hit(s) attributed to the driver)` (mixed with foreign hits: `…; M unexplained foreign hit(s) - …)`); other hits (e.g. a ZFS
hung task) give `FAIL (unexplained foreign hit - annotate with evidence or
rerun: …)`. Only an annotation in the run record (the lines, a cause without
driver involvement, the evidence) removes that FAIL; the step is then aborted
for a reason outside the driver and is rerun. A hit is never INCONCLUSIVE. An
unreadable journal gives INCONCLUSIVE.

## f8a-snapshot.sh

```bash
bash /root/n5-mw1/f8a/f8a-snapshot.sh cold          # interactive
bash /root/n5-mw1/f8a/f8a-snapshot.sh warm --yes    # without a terminal (read-only, allowed)
```

- **Mode A** (module not loaded): `ec_probe dump` twice (EC RAM 0x00–0xFF),
  `n5_i2ec_read 0x1800 48`, `n5_fan rpm` from `/root/n5pro-ec/bin` (if it
  exists, else `<repo>/bin`; `--bin-dir DIR` overrides). Only these
  argument sets are allowed; `ec_probe write` cannot be called. Before the
  first read and again before each call: the module is not loaded, no driver
  hwmon device exists, none of 0x2e/0x2f/0x62/0x66/0x68/0x6c is reserved in
  `/proc/ioports`, no Super-I/O driver (`it87`, `nct6775`, `nct6683`,
  `w83627*`, `f71882fg`, …) is loaded, kernel lockdown is `none`, and
  `n5-fangov` is inactive (checklist C2). The tool directory and the sha256 of
  each tool are logged.
- **Mode B** (module loaded): no research tool. hwmon `fan*`, `temp*`, `pwm*`,
  `pwm*_enable` (the pwm values are the driver's cache, not the EC state) and
  `/sys/kernel/debug/minisforum_n5_it5571/dcr` if the module was loaded with
  `debug_regs=1`, otherwise "dcr unavailable".
- Both: a header with board, BIOS and EC firmware and the profile
  (`reference` or `other N5-family board`); date, uptime, boot_id, kernel, DMI incl. `bios_*` and
  `ec_firmware_release`, lsmod, `/proc/ioports` (2e/2f/62/66/68/6c), loaded
  Super-I/O drivers, lockdown state, module
  parameters, modprobe.d lines and the effective options (`modprobe -c`),
  Tctl/NVMe/HDD, `n5-fangov` state.

Expected (mode A): two dumps that differ at most in 0x04/0x05/0x06/0x09
("EC RAM bytes changed ... (none)"), I2EC rows `1800:`, `1810:`, `1820:`,
three tach lines, last line `RESULT: complete`. Exit 3 and
`RESULT: INCOMPLETE (...)` if a research tool failed or a section is short;
the file is still written.

```bash
bash /root/n5-mw1/f8a/f8a-snapshot.sh --diff before.txt after.txt
```

Compares dump 1 of two mode-A files: changed EC RAM bytes without the
temperature offsets, the curve regions 0x10–0x27, 0x30–0x38, 0x70–0x81
(`identical` / `CHANGED at ...`), changed I2EC bytes. Exit 0 = curve tables
identical, 1 = changed, 2 = input error (e.g. a mode-B file).

Proves: the state at that moment, as read by the tools. Does not prove: anything
about the EC between two snapshots; mode B shows no EC RAM at all.

## Snapshot on another N5 board

For owners of another N5-family box who want to send us a register snapshot.
`f8a-snapshot.sh` is read-only: it writes nothing to `/sys` and sends the EC only
read commands plus the index/address writes those reads need (Super-I/O index, I2EC
address, PMC2 read subcommand 0xd5, EC read command 0x80); it writes no EC RAM and no
EC register. It runs on these DMI product/board pairs (exact strings,
`/sys/class/dmi/id/product_name` and `board_name`): N5 PRO/F8NAA, N5 PRO/F8NAB,
N5/F8NAA, N5A/F8NAB, N5 AIR/F8NAB. Any other board refuses before anything is
read and prints its DMI strings — send those instead. There is no override:
on an unknown board the tools would send N5-specific commands to unknown
firmware. `f8a-stress.sh` and `f8a-pcie.sh` refuse on every board except
N5 PRO/F8NAA.

Requirements (mode A, the useful one; any Linux with bash, systemd and coreutils, e.g. Ubuntu):

- root; `gcc` and `git` to build the research tools;
- Secure Boot off, or `/sys/kernel/security/lockdown` shows `[none]` (lockdown
  blocks `ioperm()`);
- none of 0x62/0x66/0x68/0x6c/0x2e/0x2f in `/proc/ioports`. `EC data`/`EC cmd`
  on 0x62/0x66 means the kernel ACPI EC driver owns the EC; the snapshot then
  refuses mode A — send us the refusal line;
- no Super-I/O driver loaded (`lsmod | grep -E 'it87|nct67|w83627|f71882'`;
  `modprobe -r` it) and no fan daemon running (`fancontrol`, `n5-fangov`);
- the driver `minisforum_n5_it5571` not loaded (`modprobe -r`); if it is
  loaded, the snapshot runs mode B (hwmon values only, no EC RAM).

`n5-fangov`, Proxmox and `/root/n5pro-ec` are not needed. If systemd does not
know `n5-fangov`, it counts as inactive.

```bash
git clone https://github.com/SirRenix/minisforum-n5pro-fan-proxmox.git n5pro-ec
cd n5pro-ec && git checkout release-sr1
bash scripts/02-build-tools.sh       # clones ltdstudio/minisforum-n5-it5571, builds ./bin
sudo bash tools/f8a/f8a-snapshot.sh myboard --bin-dir "$PWD/bin"
```

Reads (mode A): EC RAM 0x00–0xFF twice, I2EC 0x1800–0x182F, TACH1–3, plus DMI
(board, BIOS, EC firmware), kernel, lsmod, `/proc/ioports`, lockdown,
temperatures. It takes a few seconds. Exit 0 = complete, 3 = incomplete (the
file is still written), 1 = refused (the line says why).

Output: `/root/f8a-raw/f8a-snapshot-myboard-<UTC>.txt` and `.sha256`. The
host name is replaced by `n5host`; no serial numbers, MAC or IP addresses
are read. Send both files (e.g. attached to the GitHub issue). Look through
them before you send them.

## f8a-stress.sh (E1/E2)

```bash
systemctl is-active n5-fangov                 # inactive
modprobe -r minisforum_n5_it5571; modprobe minisforum_n5_it5571 debug_regs=1
tmux new -s f8a
bash /root/n5-mw1/f8a/f8a-stress.sh load      # 50x modprobe -r / modprobe ... debug_regs=1
bash /root/n5-mw1/f8a/f8a-stress.sh unbind    # 50x unbind / bind of the platform device
```

Needs the module loaded with `debug_regs=1` and `experimental_write=1`, the
latter also effective in `modprobe -c` (an override file with
`experimental_write=0` refuses), pwm1..4_enable = 2, fan1..3 > 0 rpm and all
three temperature sources present. A reader loop reads all
`fan*_input`/`temp*_input`; a writer loop sets pwm1 and pwm2 to manual and
cycles 255/200/150/100 — through every teardown. The writer is held only while
the driver comes back, so the line recorded per cycle is the fresh probe state:

```
cycle 7/50: pwm1..4_enable=2 2 2 2 fan1=2003 fan2=2236 fan3=1656 dcr=[1800: 00 ff 00 55 4a 57 57 8c ...] klog_hits=0 Tctl=... writer_err=ENODEV=12
```

Abort on a kernel-log hit, enable ≠ 2, a fan at 0 rpm, failed
modprobe/unbind/bind, `n5-fangov` no longer inactive (checked every cycle), or
a limit. On every exit — normal, abort, Ctrl-C, SIGTERM, SIGHUP — the loops
stop, the module is loaded (`debug_regs=1`) and bound, pwm1/pwm2_enable = 2,
the summary and `.sha256` are written. A second Ctrl-C during the restore is
ignored. SIGKILL cannot be handled: the loops and the watcher then end by
themselves within a second, pwm1/pwm2 stay as last written — set them back by
hand (`echo 2 > .../pwm1_enable`, run card R3).

The summary gives cycles done, writer/reader errors per errno (ENODEV/EBUSY/
ENOENT expected during teardown), kernel-log hits (all / attributed to the
driver), max temperatures and a step result:

- `PASS` — all cycles, 0 kernel-log hits, enable = 2 and fan1..3 > 0 after
  every reload, no unexpected errno;
- `FAIL` — any kernel-log hit (attributed to the driver, or an unexplained
  foreign hit until annotated with evidence), wrong enable state, fan at 0 rpm,
  failed reload, unexpected errno;
- `INCONCLUSIVE` — stopped for a reason outside the driver (limit, Ctrl-C,
  hangup, `n5-fangov` started, unreadable journal).

Proves: under this load and these N cycles no kernel-log hit and the expected
state after each reload. Does not prove: absence of the teardown race (the
window is exercised, not proven absent); EC behaviour (the cycle line shows DCR,
judging it is up to the operator); `kill -9` of n5-fangov (E3), fan unplugging
(E4) and latency (E5) are not covered.

## f8a-pcie.sh (F1, M2)

```bash
systemctl is-active n5-fangov                 # inactive
modprobe -r minisforum_n5_it5571              # touched channels back (EC automatic; PCIe 255)
tmux new -s f8a
bash /root/n5-mw1/f8a/f8a-pcie.sh
```

If the module is loaded at the start, all pwm1..4_enable must be 2 (unloading
would otherwise drop a manual channel, e.g. the failsafe pwm3 = 140, to the EC
default for the rest of the window).

Sequence: (a) unload the module if loaded → `f8a-snapshot.sh pcie-before`
(mode A); (b) `modprobe ... debug_regs=1`, `pwm4_enable=1` (driver sets 255),
`pwm4=167` (0xA7), dcr raw, settled and at 10 s (logged); (c) `pwm4_enable=2` (driver
0.2.1+sr1~rc2 and later: hand-back at 255, no 0x31; operator decision 2 of 2026-10-16),
dcr at +0 s (raw, logged) and at +1, +60, +300 s (judged); (d) unload → `f8a-snapshot.sh pcie-after`; (e) `--diff` and the DCR5
(I2EC 0x1807) series in the summary. Duration about 6 min. Kernel log, limits,
`n5-fangov` and (while loaded) fan1..3 are checked after every step and every
2 s of waiting. The script holds `/run/n5-fangov/writer.lock` while it runs and
refuses to start when another pwm writer (n5-ectest `map`/`handback`,
n5-fangov ≥ 0.5) holds it; `f8a-stress.sh` does the same. Before any `modprobe`
in the restore the script waits until no research tool process runs and
`/run/n5-ectest.lock` (n5-ectest raw mode A) is free; `n5-ectest log` does not
block it. On exit pwm4 is never left manual; the module is
reloaded normally (no `debug_regs`) only if it was loaded at the start.

Settle (TESTSPEC 0.4 §2.1 and §5.3 F1, #21/#22; f8a 0.2.3): the EC takes a pwm4
write and the hand-back over within about 0.2 s (MW1 Z1, 2026-10-17); f8a 0.2.1
judged a read taken at the moment of the write and reported F1 FAIL
(`c hand-back +0s=a7`) for a correct driver. The `+0s raw` reading is logged,
not judged. Judged are the hand-back points +1 s (the settle time of pwm4), +60 s
and +300 s, each read once at or after its time: DCR5 must be `ff`. f8a 0.2.2
judged instead a point polled every 0.2 s from 1 s until it showed `ff` (up to
5 s); 0.2.3 judges +1 s as written in 0.4. The `settled` point of (b) is logged
only.

Expected output: the DCR5 series (`b1 write settled +1.0s  a7` …,
`c hand-back +0s raw  ..`, `c hand-back +1s  ff`, `c hand-back +60s  ff`,
`c hand-back +300s  ff`),
`hand-back check (decision 2: pwm4_enable=2 -> DCR5 ff): ok at 3 points`,
`C1 curve tables ...: identical` or `CHANGED at 0x..`, and
`F1 run result: COMPLETE` (exit 0). `INCONCLUSIVE` with exit 3 if a snapshot
is incomplete, `FAIL` on any kernel-log hit (driver hit or unexplained foreign
hit, as above), a fan at 0 rpm, a failed driver operation, or a DCR5 other than
`ff` at a hand-back point (exit 1; a driver before `0.2.1+sr1~rc2` sends 0x31 and
leaves `a7`).

Proves (M2, PCIe): whether the curve tables differ between before the write and
after hand-back + unload, and whether the driver hand-back sets DCR5 to 0xff and
keeps it there for 300 s. Does not prove M3 (what DCR5 does after 0x31): the
driver no longer sends 0x31 on pwm4, so M3 needs the research tools with the
module unloaded and a temperature step across a curve threshold or a reboot
(run card F2–F4). Also
not visible: the EC RAM state directly after the write — that dump would need
the research tools beside the loaded driver (forbidden, C2) — and a curve
change that the EC reverts on the hand-back.

## Tests

`test/run-tests.sh` runs all scripts against a fake sysfs/procfs/debugfs tree
with stub `modprobe` (incl. `-c`), `journalctl` (honours `--since`),
`systemctl` and research tools (`F8A_ROOT`, `F8A_BIN_DIR`; with `F8A_ROOT` set
the scripts refuse unless every one of these resolves inside it; without it
`F8A_BIN_DIR` is ignored). Interactive runs use `script(1)` for a pty. In test
mode every sysfs write is recorded in `F8A_ROOT/writes.log`.

```bash
docker run --rm -v "$PWD:/src:ro" debian:trixie bash /src/test/run-tests.sh
docker run --rm -v "$PWD:/mnt:ro" -w /mnt koalaman/shellcheck:stable -x -P SCRIPTDIR \
  f8a-*.sh test/run-tests.sh test/fakek.sh test/stubs/*
```

Covered: guard refusals (DMI, n5-fangov active/activating/unknown, no tty,
module loaded or ports reserved for research tools, allowlist, duty < 100,
unknown board for the snapshot, N5 PRO/F8NAB and a foreign sys_vendor
refused by stress and pcie, ACPI EC on 0x62/0x66, Super-I/O driver loaded,
lockdown, research tools missing, `--bin-dir` outside the test root),
snapshot on N5 PRO/F8NAB (modes A and B, header, tool hashes), N5/F8NAA,
N5A/F8NAB, N5 AIR/F8NAB, `--bin-dir` and the `<repo>/bin` fallback,
missing temperature source, fan at 0 rpm, modprobe.d override, pcie with a
manual channel, `F8A_BIN_DIR` outside test mode); kernel-log pattern and
driver attribution; snapshot A/B, incomplete snapshot; diff; stress load/unbind
with injected ENODEV and exact write set; aborts on a driver Oops, a foreign
hit (FAIL, not INCONCLUSIVE; also in pcie), temperature (also seen by the watcher during a blocked `modprobe -r`), a
stalled fan, `n5-fangov` starting, an unreadable journal; Ctrl-C (SIGINT to the
process group), hangup (pty closed), SIGTERM and SIGKILL of stress; pcie call
order (no research tool while the module is loaded, never `ec_probe write`,
only pwm4 written), pcie hand-back check (a fake driver model sets DCR5 like rc2; an rc1-style 0x31 hand-back that leaves a7 is FAIL, exit 1; a DCR5 that follows the write within 0.3 s is not judged at +0 s and is ff at +1 s; one that follows after 1.5 s is FAIL at +1 s), Ctrl-C in step (b), SIGTERM during a research tool (the
module is reloaded only after the tool ended), research-tool failure.
Not covered: the real driver, real EC timing; the fake unbind runs
asynchronously, the real one synchronously.
