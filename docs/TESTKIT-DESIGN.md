# n5-ectest — design

Status: design draft 2026-10-10 (revised after review), before implementation. Written against
TESTSPEC 0.3; everything 0.3 did not cover is listed in §12. These items are decided in
TESTSPEC **0.4** (operator decision 2026-10-17, changelog 0.4), which applies: where this design
differs from TESTSPEC 0.4, the spec wins. Where the tool knowingly differs from the spec version it
names, the bundle records it in `spec_deviations` (§8).

## 1. Goal and scope

One static Go binary `n5-ectest` that runs the TESTSPEC tests on any N5-family box, so results
from other users are comparable without questions: same raw format (§8 `samples.csv`), same
criteria, same report. It depends on the driver `minisforum_n5_it5571` only, never on n5-fangov.
Runs on Proxmox VE and Debian/Ubuntu (x86-64, systemd, kernel ≥ 6.1).

Read-only by default. `map`/`handback` write only through hwmon sysfs, interactive, operator at
the device (G5), only on the boards of §4 and only where the driver exposes `pwmN`.

## 2. Layout

Two Go modules in the driver repo:

- `github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5`, tags `n5/vX.Y.Z`: the shared
  packages `n5driver`, `writerlock`, `hwstatus`, `identity`. Neutral: n5-fangov and n5-ectest
  both import it, neither binds the other's release cycle.
- `github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest`, tags `ectest/vX.Y.Z`: the tool.

Neither tag scheme clashes with the package tags `v0.2.1-*`.

```
go/n5/  n5driver/ hwstatus/ writerlock/   exported, moved from n5-fangov; hwstatus results.tsv by
                                          `go generate` from ../../befunde/RESULTS.md
        identity/       DMI, kernel, driver, lockdown, OS, container, redaction (identify.go)
ectest/cmd/n5-ectest/   main, one file per subcommand, guardian (§7)
  internal/spec/        verdict rules T1/T2/T3/G4, aggregation, SpecVersion  (validate/eval.go)
  internal/runner/      Env, gates G1–G6, limits, restore, procedures      (validate/runner.go)
  internal/bundle/ sim/ (validate/bundle.go, sim.go); replay only under testdata (§11)
  internal/session/     G5 (cmd/n5-fangov/session.go)   internal/regulator/  G1 (§6)
  internal/pwmw/        the only pwm/enable write path (§7)
  internal/rawec/       /dev/port, port allowlist, raw guard (§5) — phase 3 only
  internal/snapshot/ logger/ report/ sysroot/ (all paths below N5ECTEST_ROOT)
  contrib/systemd/      n5-ectest-log@.service, n5-ectest-check@.{service,timer}
  testdata/             replay, EC RAM image, golden port trace, fake trees
```

Deps: stdlib, `BurntSushi/toml` (n5-fangov stop values, §7). Static amd64. No image library.

### What n5-fangov keeps and drops

| n5-fangov | After the move |
|---|---|
| `internal/validate`, `cmd/.../validate*.go`, `session.go` | frozen until phase 2, then deleted; `n5-fangov validate` prints "moved to n5-ectest (driver repo)", exit 2 |
| `internal/n5driver`, `internal/hwstatus`, `writerlock_unix.go` | imported from `go/n5` at a pinned `n5/v*` tag; `gen-hwstatus.sh` deleted |
| `identify` | stays as command, calls `go/n5/identity` |
| `test` (generic channel test, setup) | stays (operator decision 10.10.). It remains a second writing step sequence next to `map`; that is accepted, not merged |

Phase 1: `go/n5` and n5-ectest from copies, n5-fangov untouched and `validate` frozen (window 1
may still need it). The copy-drift check is a local script run through `remote-go.ps1`, not CI
(n5-fangov is another repo). Phase 2 (n5-ectest green on the device): n5-fangov imports `go/n5`
(locally `go.work`) and drops `validate`.

## 3. Subcommands

| Command | Writes | TESTSPEC | Needs |
|---|---|---|---|
| `identify [--json]` | no | T0 fields, hwstatus | — |
| `snapshot LABEL [--mode B\|A] [--boot cold\|warm]` | no | T6, G6(b), C1 input | mode B (default): module loaded; mode A: explicit, §5 |
| `diff A B` | no (offline) | C1 | — (reads f8a and n5-ectest files) |
| `map pwmN [--boot …] [--restore …] [--i-am-at-the-console]` | pwmN | T0, G1–G6, T1 | §4 write table, `pwmN` exposed, `dcr` readable |
| `handback pwmN […]` | pwmN | T0, G1–G6, T2, T3 (3 runs, H and M) | as `map`; pwm4: `N/A`, writes nothing |
| `log [--duration 7d] [--interval 30s] [--mail-to ADDR] [--run-id ID]` | no | F8b K1–K7, daily summary, T4/T5 raw data (`--interval 5s`) | root (debugfs) or hwmon only |
| `log check RUN` | no | F8b K5 from outside the sampler (§9) | — |
| `log annotate RUN (--event N \| --down FROM TO) TEXT` | no | F8b annotations | — |
| `report DIR` | no (offline) | verdicts (§1 rule 4), HTML | — |
| `restore [--from DIR]` | pwmN | §4 restore by hand | same checks as the guardian (§7) |
| `version` | no | — | — |

`--dry-run` (simulator) as in `validate`, marked "not a result under TESTSPEC" (§1 rule 7);
timing flags only with `--dry-run`. Replay runs only in tests (§11), not as a user flag.

Exit codes: 0 done, 1 refused/aborted, 3 incomplete snapshot, 64 usage. `diff` uses exactly the
codes of TESTSPEC §5.3: 0 identical, 1 CHANGED, 2 incomplete input; usage 64. Bundle dir
`<YYYYMMDD-HHMM>-<board>-<bios>-<test>/`, `<test>` = `validate-pwmN` for map/handback (0.3 §8
unchanged), `log-<run-id>`, `snapshot-<label>`.

Build order: (1) `identify`, `snapshot` B, `map`/`handback` with guardian and `restore`,
`report` HTML; (2) `log`; (3) `rawec` and mode A, only after window 1. Until (3) exists, f8a is
the mode-A path (§10).

## 4. Boards and driver states

n5-ectest enforces its own table; it does not rely on the driver allowlist alone, because
upstream 0.2.1 with `force=1 experimental_write=1` opens PMC2 writes on any board and
`docs/DMI-PATCH.md` lets users add boards to the driver.

| DMI product / board | Read-only (mode B) | Raw mode A | Writing commands |
|---|---|---|---|
| `N5 PRO` / `F8NAA` | yes | yes, with the conditions below | yes, with the conditions below |
| `N5 PRO` / `F8NAB` | yes | yes, with the conditions below | gate failure (not in the write table) |
| `N5` / `F8NAA`, `N5A`·`N5 AIR` / `F8NAB` | yes | no (outside the order) | gate failure (outside TESTSPEC scope) |
| anything else | `identify`, `diff`, `report` only | no | no |

**Write table** — all must hold, else gate failure:

- DMI `product_name=N5 PRO`, `board_name=F8NAA`;
- `sys_vendor` equals the string measured for patch 0003;
- driver module version contains `+sr1`;
- module parameter `force=N` (`force=Y` always refuses writing tests, whatever the driver allows);
- `dcr` readable. Second line of defence, independent of the above: without `dcr` T1–T3 write
  nothing (TESTSPEC §5.1);
- `pwmN` exposed by the driver;
- not in a container (§5).

A failed board/driver condition is not a pre-run refusal: TESTSPEC §3 knows only two of those.
`map`/`handback` write a bundle with T0 and the writing tests `INCONCLUSIVE ("not run: gate
board: <reason>")` or `("not run: pwm not exposed: <n5driver reason>")`; nothing is written.

**Mode A table** — all must hold: `product_name=N5 PRO`, board `F8NAA` or `F8NAB`; `sys_vendor`
equals the patch-0003 string (same reasoning as patch 0003: every I2EC read writes the SIO index
and address registers); chip ID `0x5571`. PMC2 tach reads in mode A only when
`ec_firmware_release` = 0.14 (subcommands 0x14…0x19 are measured on EC 0.14 only); else the
TACH section reads `not read (EC firmware not measured)`.

Still useful without writes: `identify`, `snapshot` (B: tachs, EC temps, `dcr`; A: EC RAM,
I2EC, tachs), `log` (T4/T5 data, K1–K7), `report`. No `dcr` (no `debug_regs=1`, or sr1 refuses
it on `sys_vendor`) → DCR columns empty.

## 5. Raw EC reads (mode A): native `/dev/port`, research tools retired

**Decision:** n5-ectest reads EC RAM, I2EC and tachs itself through `/dev/port` (pread/pwrite of
one byte at offset = port). The upstream C tools stay only as cross-check. Mode A runs only with
an explicit `--mode A`; there is no automatic fallback into port I/O. Built in phase 3 (§3).

Why: (1) users would otherwise have to clone upstream, patch `inb_p`/`outb_p` for glibc and build
a binary that also contains `ec_probe write` and blind `cmd`; the allowlist in `f8a-lib.sh` only
guards our wrapper, not the binary. (2) In Go the write paths do not exist, and a port-level
allowlist makes that testable. (3) One static binary, no `ioperm`, no cgo. Cost: new protocol
code — covered by fake-port tests, a golden trace (§11) and the byte-compare (§10). Alternative
without `/dev/port`: §12 item 9.

Protocols (identical to the research tools, measured 2026-09-14):

- **EC RAM** (ACPI EC, `ec_probe dump`): wait IBF(0x66 bit1)=0 → `0x80`→0x66 → wait IBF=0 →
  offset→0x62 → wait OBF(bit0)=1 → read 0x62. Poll every 10 µs, budget 200 ms per wait; a
  timeout gives `??` for that byte (as `ec_probe`). Dump 0x00–0xFF twice.
- **I2EC** (`n5_i2ec_read`): per byte `0x2e`→0x2e, `0x11`→0x2f, `0x2f`→0x2e, addr_hi→0x2f;
  same with `0x10`/addr_lo; `0x2e`→0x2e, `0x12`→0x2f, `0x2f`→0x2e, read 0x2f. Range fixed to
  0x1800–0x182F (0x1800 C0CPRS, 0x1801 CTR, DCRn at 0x1802+n: DCR0–7 = 0x1802–0x1809,
  DCR1–5 = 0x1803–0x1807).
- **Tachs** (`n5_fan rpm`): PMC2 at the fixed ports 0x68/0x6c (as the kernel module; no LDN
  discovery): drain OBF, wait IBF=0, `0xd5`→0x6c, wait IBF=0, sub→0x68, wait OBF, read 0x68;
  high/low/high with subcommands (0x19,0x18) (0x17,0x16) (0x15,0x14), 4 tries against a torn read.
- **Chip ID** before the first I2EC/PMC2 access: `0x87 0x87`→0x2e, index 0x20/0x21, `0xaa`→0x2e
  (index writes only, no LDN write); must read 0x5571, else raw mode is refused.

Every deviation from the C tools is listed in the README and in the golden trace (§11), with
file and line of the C source; known candidates: OBF drain before `0x80`, poll interval, wait
budget, the 4-try tach loop.

**Port allowlist** (`rawec`, enforced on every pwrite, violation = panic before the syscall):
0x66 ← `0x80` only; 0x62 ← offset only directly after `0x80`; 0x6c ← `0xd5` only; 0x68 ←
{0x14…0x19} only directly after `0xd5`; 0x2e ← {0x2e, 0x2f, 0x20, 0x21, 0x87, 0xaa}; 0x2f ←
{0x10, 0x11, 0x12} after index 0x2e, address bytes after index 0x2f only while D2EC register
0x10/0x11 is selected — a write while 0x12 (I2EC data) is selected is impossible. `WR_EC 0x81`
and every PMC2 write subcommand are unreachable.

**Raw guard.** The guard locks, not only checks:

- Held for the whole raw run: `/run/n5-fangov/writer.lock` (created if n5-fangov is absent; held,
  so n5-fangov cannot start during the run) and the own instance lock `/run/n5-ectest.lock`.
- Checked once before the run: euid 0 and `CAP_SYS_RAWIO` in `CapEff`; `/dev/port` exists;
  `/sys/kernel/security/lockdown` = `[none]` (integrity/confidentiality block `/dev/port` the same
  as `ioperm`: mode A is then unavailable, said so); not in a container (`container=` in
  `/proc/1/environ`, `/run/systemd/container`: a privileged LXC with `/dev/port` shows the host's
  `/proc/modules` and `/proc/ioports`, so the guard is not reliable there); mode-A table of §4;
  none of 0x2e/0x2f/0x62/0x66/0x68/0x6c in `/proc/ioports`; f8a not running (`f8a_research_busy`
  conditions: research tools, f8a scripts).
- Checked before **every** EC/PMC2/I2EC transaction (cheap reads of `/proc`): module
  `minisforum_n5_it5571` not in `/proc/modules` (also not `Loading`) and no driver hwmon device;
  no SIO driver loaded: `it87`, `it87_wdt`, `gpio_it87`, `nct6775`, `nct6683`, `w83627hf`,
  `w83627ehf`, `f71882fg`, `f71808e_wdt`, `sch5627`, `sch56xx_common`, `smsc47*`, `parport_pc`
  (these use `request_muxed_region` only briefly and are not visible in `/proc/ioports`); no
  package or module activity: `/var/lib/dpkg/lock-frontend` not held, no `unattended-upgr`,
  `dkms`, `modprobe`, `insmod`, `rmmod` process (by `comm`). The driver package's `postinst`
  loads the module itself, so apt/DKMS runs are a real collision path.
- A failed per-transaction check stops the block; the block is marked suspect and the snapshot
  `INCOMPLETE`. Signals are blocked during a block, the SIO exit (`0xaa`) runs in a `defer`.
- For the run, `/run/modprobe.d/n5-ectest.conf` with `install minisforum_n5_it5571 /bin/false`
  closes the load race instead of only checking it (removed at the end, also on a signal).
- The port allowlist is the only port write path, built like `pwmw` (panic before the syscall):
  the "reads" of mode A write command and address bytes to 0x66/0x62/0x2e/0x2f.
- The busy check for running f8a tools excludes the own process.

n5-ectest never loads or unloads modules; the operator does (`modprobe -r`). The f8a side gets
the mirror check: `f8a_research_busy` reports a held `/run/n5-ectest.lock` (taken only by mode A).

**Residual risk (not closable in userspace):** a module load or package script that starts in
the microseconds between the per-transaction check and the port access, and EC/SIO access by
AML or SMM firmware. Neither is detectable from userspace; the double EC RAM dump and the
`INCOMPLETE` marking are the only signal.

Mode B (module loaded) never touches ports: hwmon `fan*`/`temp*`/`pwm*` (cache, labelled) and
debugfs `dcr` (0400; readable under lockdown per patch 0003 — to be confirmed on a locked host).

## 6. Regulator detection (G1)

Per TESTSPEC G1, checked before the run and at every sample: (a) systemd `ActiveState` of
`n5-fangov` (also `activating`/`reloading` = running → refusal before the run, no bundle),
`n5-fand`, `fancontrol`, `fan2go`, `coolercontrold`, `thinkfan`; (b) processes by exact `comm`
or basename of `argv[0]` with the same names (non-systemd starts; never a substring of the
command line — `vim fancontrol.conf` is not a hit), plus the pwm-writing f8a scripts
`f8a-pcie.sh` and `f8a-stress.sh` (by `comm`, or `argv[1]` of `bash`/`sh`); (c) `/proc/*/fd` symlinks into
`/sys/devices/…/hwmon/hwmon*/pwm*` (persistent holders); (d) the writer lock — taken by the
parent (§7) and held for the whole writing run (dir created if n5-fangov is absent); at every
sample the lock is verified (`stat` inode of the path = `fstat` inode of the held fd), because
postrm and `uninstall.sh` delete `/run/n5-fangov`; (e) at every sample `pwmN`/`pwmN_enable` of
all channels against the expected value — the expected value is set **before** the own write,
so the own write is never seen as foreign; a change n5-ectest did not make is a foreign write
(catches `echo >` writers such as `fancontrol`, which hold no fd). (a) n5-fangov running =
refusal; any other hit = gate G1 failed → writing tests `INCONCLUSIVE`, nothing written; during
a run → abort per §4.

## 7. Safety, restore, guardian

Limits exactly TESTSPEC §4: Tctl ≥ 85 °C, NVMe max ≥ 70 °C, HDD max ≥ 50 °C, fan1–3 = 0 rpm
with channel duty > 0 (duty unknown counts as > 0), a limit sensor with a gate-window value missing
in 3 consecutive samples, another writer.

**One write path** (`internal/pwmw`): every pwm write of every command, abort path, the guardian
and `restore` goes through one function. `pwmN` < 100 → panic before the syscall; `pwmN_enable`
only {1, 2}. Before each write it re-resolves the device (below). The handback value is written
as `M = max(100, min(A+48, 200))`.

Restore value per channel, in this order: `--restore pwmN=auto|D` (recorded; D < 100 is refused
at argument parsing); the `stop` of `/etc/n5-fangov/config.toml` `[[channel]]` if the file exists
(as `validate`; a value < 100 is raised to 100 and recorded in `restore_warnings`); otherwise the
default of §12 item 2, recorded in `spec_deviations`. pwm4 is always 255 (operator decision
2026-10-16: DCR5 has no EC automatic; `auto` from the config or n5-fangov's built-in value would
leave the last duty standing); a configured `stop` other than 255 is recorded in
`spec_deviations`, and `--restore` takes only `pwm4=255`. Listed in `meta.json` → `restore_values`;
read-back mismatch → `restore_warnings`, failure → `restore_failed`. `restore --from DIR` checks
the file values the same way; it runs only when the run state in `<run>/n5-ectest/` is not open,
never replaces that state, and writes `events.log`/`SHA256SUMS` only in DIR (never in a
`bundle_dir` named inside the file, never through a symlink). `restore --discard` gives up an open
state that cannot be restored (device changed): it shows `pwmN_enable`/`pwmN`, needs `DISCARD`
typed at a terminal and writes nothing to the EC.

**Device identity:** `restore.json` stores hwmon `name` (`minisforum_n5_it5571`), the real device
path (`/sys/devices/…`, not `hwmonN`) and the module `srcversion`. The parent and `restore`
re-resolve the device and compare all three before every write: amdgpu has its own
`pwm1`/`pwm1_enable`, and hwmon numbering changes on a module reload. Module reloaded (other
`srcversion` or device path) → nothing written, reported.

**Guardian:** writing commands re-exec as a worker; the parent waits. The **parent** takes the
writer lock and passes the fd to the worker (`ExtraFiles`), so a SIGKILLed worker does not
release it and n5-fangov cannot start before the restore. Parent and worker catch SIGINT, SIGHUP,
SIGTERM and SIGQUIT (Ctrl-\; never the Go default dump-and-exit), the parent forwards them; both
ignore SIGTSTP/SIGTTIN/SIGTTOU (a stopped worker would leave the channel manual without limit
checks) and SIGPIPE. The adopted lock fd is close-on-exec. Before the first write the worker
writes `restore.json` (atomic: tmp, fsync, rename); right after the bundle is created (before any
write) it writes a provisional `meta.json` (provenance, gates so far, restore values,
`"provisional": true`), replaced at the end. Worker gone without "restored" marker (SIGKILL,
runtime fatal, OOM) → parent restores from it first, then writes any output, logs to
`events.log`, exit 1. An unreadable state file blocks the next run (fail closed). Panics recovered
in the worker (as `validate`). Both killed → channel stays manual: `n5-ectest restore` by hand.
G5 inside `tmux`: the chain ends at the tmux server, so the attached clients (`tmux list-clients`)
are judged: any remote client = remote, console only if every client has a local console.

## 8. Provenance and raw format

`meta.json` of every bundle (snapshot and log included) carries a `provenance` block: tool name,
version, commit, Go version; driver module version, `srcversion`, all parameters, DKMS package
version (`dpkg-query`), taint; kernel release and version; DMI `product_name`, `board_name`,
`board_version`, `sys_vendor`, `bios_version`, `bios_date`, `bios_release`,
`ec_firmware_release`; OS (`/etc/os-release` ID, VERSION_ID); lockdown; container; `--boot`,
uptime, boot_id; spec version; `spec_deviations`; the sha256 of the running binary; the commit
carries `-dirty` for a build from a changed tree. `events.log` names tool + version + driver
version on every write and auto command, so EC and driver statements stay separable.

`spec_deviations` lists every behaviour 0.3 does not define that the run used, at minimum: tool
name (§12 item 1), restore default without n5-fangov config (§12 item 2), redaction beyond
hostname/serials (§12 item 8).

`samples.csv` = TESTSPEC §8 columns exactly, empty cells for unknown. DCR columns map
`dcr1…dcr5` to I2EC 0x1803…0x1807. Logger extras go to `aux.csv` keyed by `t_iso`: boot_id,
uptime, monotonic time, pwm/enable cache, `ec_read_errors`, regulator state and restart counter.
Snapshot files keep the f8a text markers (`=== BEGIN EC_RAM n ===`, `00:` / `1800:` rows,
`TACHn: … RPM`) plus the provenance header, so `diff` and `f8a-snapshot.sh --diff` read both.
`SHA256SUMS` in every bundle, over every regular file including subdirectories; `<file>.sha256`
only beside single snapshot files outside a bundle. Directory names use UTC like every time in
the bundle.

**Redaction** (always on): hostname → `n5host` everywhere; DMI serials/UUID removed; regex
redaction of MAC addresses, IPv4/IPv6 addresses, mail addresses (systemd instance units such as
`getty@tty1.service` excluded) and serial-number patterns in every text output,
kernel-log lines included (e.g. `r8169 … MAC address`); `SSH_CONNECTION`, user names never reach
output (G5 records ancestor `comm` and tty type only). TESTSPEC 0.3 §1 names only hostname and
serials; the rest is recorded as deviation (§12 item 8).

## 9. Logger and report

**`log`** replaces the core of `validation-watch` (sampler + check): interval 30 s default (each
sample makes the driver talk to the EC), 5 s for T4/T5 data, ends at `--duration`.

- Every debugfs/sysfs read runs in a goroutine with a timeout; a timeout counts as
  `ec_read_errors` and is a K5 candidate. A hung read never stops sampling, checks or mail.
- The sample loop sends `sd_notify WATCHDOG=1`; the unit sets `WatchdogSec=`.
- Independent outside check: `n5-ectest-check@.timer` runs `log check RUN` every 300 s; it reads
  only the state file and the CSV age and mails K5 when the gap exceeds 660 s (the watcher's
  `CHECK_INTERVAL`/`CHECK_GAP_MAX`).
- K5 is measured on monotonic time plus `boot_id`, not on wall clock (NTP steps).
- K1–K7 online with the watcher's thresholds and K1 pattern (a test compares the Go constants
  with `n5-validation.conf.example`); K1 from `journalctl -k -o json` + cursor (fallback
  `/dev/kmsg`); episodes, 1 mail/key/h, annotations as the watcher.
- **Daily summary** mail (F8b): samples, coverage, K1–K7 counts, episodes, annotations.
- **Mail optional:** `sendmail` (or `--mail-cmd`, e.g. msmtp) if present, else the outbox
  `<run>.outbox/` beside the run directory (the queued mails carry the recipient; the run directory
  is what gets posted) + stdout — no MTA needed. One file per queued mail (sequence number,
  `O_EXCL`). The command is executed without a shell; subject and recipient in argv, body only
  on stdin; no log lines in argv. `meta.json` records the mail path, never the address.
- One mail attempt (≤ 30 s) per sampling pass: after a failure the rest of the pass queues
  without trying; `WATCHDOG=1` before each attempt; the state is saved before any mail.
- Size limits as the watcher (`DATA_MAX_MB`, `K1_MAX_PER_RUN`), also for the outbox.
- Restart-safe: state file written atomically (tmp, fsync, rename), CSV append; a gap is K5.
- K4 EC read errors and K8 "dcr not readable" are episodes (one event until they end). Kernel,
  driver version, srcversion, parameters, DKMS, BIOS, EC firmware are recorded at the start, on
  every resume and after a reboot; changes go to `meta.json` → `identity_changes`.
- Coverage reads `samples.csv` row by row (a 30-day run at 5 s fits `MemoryMax=128M`).
- A finished run started again (unit still enabled) sends `READY=1` and `STOPPING=1` and exits 0.

Unit `n5-ectest-log@.service`: `Restart=on-failure` with `StartLimitIntervalSec=1h`,
`StartLimitBurst=10`, `OnFailure=` mail (at most one per hour, with the failure count), the
watcher's sandboxing
plus `ProtectKernelTunables=yes` (sysfs read-only: the logger cannot write pwm),
`ProtectKernelModules=yes`, `CapabilityBoundingSet=` without `CAP_SYS_RAWIO`,
`DevicePolicy=closed` with `DeviceAllow=/dev/kmsg r` (no `/dev/port`).

**`report DIR`** recomputes every verdict from `samples.csv` + `events.log` + `meta.json` with
`internal/spec` and flags a mismatch with the stored `verdict.json` — test verdicts in both
directions, T2/T3 run and sub-run verdicts, and G3/G4 against `meta.json`; a provisional
`meta.json` (killed run) is exit 1 without recomputation. Runner and report compute "s after the
auto command" from the same stored ms values (`spec.SinceMS`). `report.html` is
self-contained (inline CSS + SVG, no JS, no URL): verdicts with their numbers, provenance,
`spec_deviations`; DCR1–5 with written value and phases, fan1–3, temperatures with §4 limits;
per T2/T3 sub-run DCR vs s after auto with ±16/±2 bands, 60/900 s marks, steady window; logs:
K1–K7, coverage, events. Long series reduced to min/max per pixel column.

## 10. f8a and validation-watch

| Tool | Decision | Reason |
|---|---|---|
| `f8a-snapshot.sh` | stays canonical for window 1 and until mode A has passed the byte-compare | TESTSPEC §5.3/§5.4 name it; mode A is built after window 1 (§3) |
| `f8a-stress.sh`, `f8a-pcie.sh` | stay operator-only bash, not ported | module load/unload/unbind loops are a maintainer task for the driver, not a per-board user test; they are tested and reviewed; a port would re-implement watcher, signal and kernel-log attribution with no user benefit |
| `f8a-lib.sh` | `f8a_research_busy` also detects a held `/run/n5-ectest.lock` (mode A only; `log`/mode B never touch ports); `f8a-pcie.sh` and `f8a-stress.sh` hold `/run/n5-fangov/writer.lock` while they run (`f8a_writer_lock`) | mutual exclusion with mode A (§5) and with every other pwm writer (§6) |
| `validation-watch` | stays for F8b run 2, then replaced by `log` + units | run 2 must be comparable with baseline run 1 (same sampler, thresholds) |
| `scripts/06-pwm-test.sh` | to `legacy/` | replaced by `map` |

**Mode-A cross-check** (first window after mode A exists; module unloaded, before any write):
`f8a-snapshot.sh xchk-f8a-1`, `n5-ectest snapshot xchk-go --mode A`, `f8a-snapshot.sh
xchk-f8a-2`. Criterion, fixed before the run: let V = offsets that differ between the two f8a
runs or between the two dumps inside any of the three snapshots, plus 0x04/05/06/09 and tach
jitter. PASS = every EC RAM and I2EC byte of `xchk-go` outside V equals both f8a runs; any other
difference = FAIL, no relaxing after the fact. A `??` or `INCOMPLETE` = INCONCLUSIVE, repeat.

## 11. Tests and CI

- **Fake tree:** `N5ECTEST_ROOT` (sys/proc/run/debugfs, `hwmontest`/`fakek.sh` layout); every
  sysfs write recorded; tests assert the exact write set per command. Property test: for every
  command, every abort path, the guardian and `restore`, every recorded pwm write is ≥ 100 or an
  enable write ∈ {1, 2}. Device-identity test: a second hwmon device with `pwm1` (amdgpu) and a
  renumbered driver device after reload are never written.
- **Fake port device:** an in-memory IT5571 (IBF/OBF state machine, EC RAM image from
  `befunde/raw/03-probe-20260914-160338.txt`, DCR bytes from `06-i2ec-dcr-20260914.txt`, tach
  subcommands); records the port trace. Tests: snapshot equals the f8a parse of the same data;
  every allowlist violation panics before the write; timeouts give `??`; guard refusals (module
  loaded or `Loading` mid-block, ports reserved, SIO driver loaded, dpkg lock held, container,
  lockdown, no CAP_SYS_RAWIO, wrong chip ID, writer lock held, board/vendor/EC firmware outside
  the mode-A table).
- **Golden port trace** independent of the fake model: the expected port sequence for one EC RAM
  byte, one I2EC byte, one tach read and the chip ID, transcribed by hand from the C sources with
  file and line per step; the rawec trace must match it, deviations listed (§5).
- **DCR golden test:** a known dump gives DCR1 = byte at 0x1803 … DCR5 = byte at 0x1807, equal to
  `f8a_dcr_byte` with index 3–7.
- **Redaction test:** kernel-log lines with MAC, IPv4/IPv6, serials and hostname come out
  redacted.
- **Replay** 2026-09-14 (never PASS); **simulator** map/handback end-to-end incl. aborts,
  restore, G1 change mid-run, lock path deleted mid-run, guardian after SIGKILL of the worker
  (writer lock still held by the parent).
- **Format:** CI runs `f8a-snapshot.sh --diff` (Debian container) on n5-ectest snapshots and
  checks the `diff` exit codes 0/1/2.
- **Workflow** `.github/workflows/ectest.yml` (paths `go/n5/**`, `ectest/**`,
  `befunde/RESULTS.md`, `tools/validation-watch/n5-validation.conf.example`): gofmt, vet,
  staticcheck, govulncheck, `go test -race`, the permission paths as `nobody`, `go generate` +
  `git diff --exit-code` (results.tsv current), static build + `n5-ectest version`. Tag
  `ectest/v*`: release with binary, tarball (binary, README, `contrib/systemd`) and `.sha256`.
  Actions pinned by SHA as in `package.yml`, the container image by digest.
- Local builds and the phase-1 copy-drift check: `ectest/tools/remote-go.ps1` (copy of
  n5-fangov's, Docker on a separate build host).

User docs `ectest/README.md` (command first): per command what it collects, how, how long
(snapshot < 1 min, map ≈ 11 min, handback ≈ 2 h/channel), what to post (bundle `.tar.gz`,
report). Also, command first:

- T6 "before load" needs one boot without the module, because the package loads it on every boot
  via `modules-load.d`: add `module_blacklist=minisforum_n5_it5571` to the kernel command line
  for that boot.
- Mode A is impossible under lockdown `integrity` (e.g. Ubuntu with Secure Boot); C1 and T6 in
  mode A are then `INCONCLUSIVE`.
- Run writing commands on the console; in `tmux` the attached clients decide G5 (§7).

## 12. Operator decisions for TESTSPEC 0.4 (decided 2026-10-17)

As raised before 0.4 (wording of 2026-10-10). Decisions and final wording: TESTSPEC 0.4,
changelog #1–#13 and annex "Rationale 0.4"; item 3 was dropped (2026-10-10), item 9 adopted.

1. **Tool names.** 0.3 §1 rule 4, §3 G6, §5.3, §5.4 name `n5-fangov validate` and `tools/f8a/`.
   0.4 names `n5-ectest` (map/handback/snapshot/diff). Until then results under 0.3 are run with
   `validate`/f8a; n5-ectest bundles carry `spec_deviations: ["tool n5-ectest in place of
   n5-fangov validate"]`.
2. **Restore value without n5-fangov.** §4 defines it only via the n5-fangov config. Applied now
   and recorded in `spec_deviations`: pwm1–3 `auto`, pwm4 255 (PCIe has no automatic; `auto`
   would keep 140 after T1); `--restore` overrides and is recorded. pwm4 is 255 also with an
   n5-fangov config (decision 2026-10-16, audit #5/M2).
3. **Mode-A path.** §2 and §5.3 name `n5_i2ec_read`/`f8a-snapshot.sh`; 0.4 accepts the native
   `/dev/port` read after the cross-check of §10 (or item 9 instead).
4. **T4/T5 by tool.** §1 rule 4 says "evaluated by hand". `report` can apply §5.5 as written;
   0.4 would make it the tool. Additional INCONCLUSIVE needed then: interval > 5 s, or EC RAM
   tables (snapshot) differ from §6 (other board/firmware → other bands). Until 0.4 `report`
   prints T4/T5 `INCONCLUSIVE` as `validate` does.
5. **G1 measurement.** n5-ectest adds process, fd, pwm-cache and lock-inode checks (same
   criterion). Wording only.
6. **F8b tool.** §5a names `tools/validation-watch/`; `log` replaces it from run 3 on, with
   `samples.csv` (§8 columns) + `aux.csv` instead of the watcher CSV. Run 2 stays on the watcher.
7. **Boards.** 0.3 is keyed to the N5 Pro; F8NAB and N5/N5 Air results are read-only (T0, T6, C1,
   logs). State in 0.4 whether such results go into `RESULTS.md`, and whether the board/driver
   write table of §4 belongs into §3 as its own gate (today: a failed G-condition in the bundle).
8. **Redaction.** 0.3 §1 says "only hostname and serials redacted". n5-ectest also redacts MAC
   and IP addresses, user names and `SSH_*` (kernel-log lines carry MACs). 0.4 should allow it.
9. **Alternative to mode A: driver debugfs `ecram`.** A driver patch adds a read-only debugfs file
   `ecram` (0x00–0xFF via `0x80`, under `data->lock`, bound to `sys_vendor` like `dcr`).
   Effect: no `/dev/port` in userspace at all — the raw guard, the board gate for mode A, the SIO
   blocklist and the byte-compare fall away; G6(b) and C1 become measurable with the driver
   loaded. Price: driver patch and spec change; T6 "before load" is lost, and that the probe
   writes nothing must then be shown another way. If rejected, mode A stays as in §5.
10. **Third refusal before the run.** 0.3 §3 knows two refusals without a bundle (n5-fangov
    running, G5). n5-ectest (as `validate`) also refuses without a bundle when another writer
    holds the writer lock or an earlier run left a channel unrestored (`restore.json` open or
    unreadable): a bundle would need a run that cannot be made safe. 0.4: name it, or require a
    bundle with G1 FAIL.
11. **"Every 5 s" = fixed cadence.** n5-ectest samples at anchor + k·5 s (the auto command for
    T2/T3), so read times do not add up; a sample that overruns skips the missed slots. The 12th
    sample after the auto command is due at 60 s; timer jitter puts it a few ms later, and a
    leave seen only in that sample is then "after 60.00x s" → INCONCLUSIVE. 0.4: state the
    cadence and whether sample k is judged at its due time k·5 s (e.g. within ±0.5 s).
12. **G3/G4 `SKIPPED` when the board gate failed.** Today G3/G4 are measured (5-min window) even
    when the board/driver gate already rules out any write; 0.3 §3 asks for the measurement.
    0.4 could mark them `SKIPPED` like without readable duty registers.
13. **Restore value after a temperature abort.** §4 restores to the stop value also when the run
    stopped on a temperature limit: HDD ≥ 50 °C puts pwm3 back to 140, the duty that just ran.
    Proposal: after a temperature abort the affected channel goes to 255 or EC automatic.

## 13. Review decisions (review 2026-10-10)

| # | Finding | Decision | Reason |
|---|---|---|---|
| 1 | Raw guard checks but does not lock | accepted (§5) | a modprobe from postinst/apt mid-block interleaves PMC2 transactions; lock held, per-transaction check, residual risk named |
| 2 | Mode A on boards the driver refuses | accepted (§4) | same reasoning as patch 0003; N5/N5 Air outside the order; 0x14…0x19 measured on EC 0.14 only |
| 3 | Write gate relies on the driver, upstream `force` writes | accepted (§4) | upstream 0.2.1 and DMI-PATCH.md open writes; own table, `force=Y` refuses |
| 4 | Restore to the wrong hwmon, lock dies with the worker | accepted (§7) | amdgpu has `pwm1`; parent holds the lock and passes the fd |
| 5 | "Never < 100" not enforced | accepted (§7, §11) | one write path with panic, property test over all paths |
| 6 | DCR addresses wrong | accepted (§5, §8, §11) | verified: 0x1802+n in TESTSPEC §2 and `n5driver.go` |
| 7 | Logger cannot see its own hang | accepted (§9) | F8b requires a daily summary and K5 detection independent of the sampler |
| 8 | `diff` exit codes vs §5.3 | accepted (§3) | §5.3 fixes 0/1/2; usage moved to 64 globally |
| 9 | `--mode auto` does implicit port I/O | accepted (§3, §5) | port access only by explicit choice |
| 10 | SIO blocklist short, containers | accepted (§5) | muxed-region drivers are invisible in `/proc/ioports`; LXC shows host `/proc` |
| 11 | Byte-compare not decidable, circular test basis | accepted (§10, §11) | f8a → Go → f8a with a criterion fixed beforehand; hand-transcribed golden trace |
| 12 | Four silent spec deviations | accepted (§4, §8, §9, §12) | restore default in `spec_deviations`; board as gate failure in the bundle (conforms to §3); redaction as item 8; daily mail added |
| 13 | Dependency direction, drift check | accepted (§2) | neutral module `go/n5`; drift check local; `validate` frozen; `test`/`map` overlap named |
| 14 | Scope (YAGNI) | accepted (§2, §3) | build order with mode A after window 1; `--png`, `report --replay`, `auto`, per-file `.sha256` in bundles dropped |
| 15 | Mode A hardly reachable for others | accepted (§11 README) | `module_blacklist` boot and lockdown note, command first |
| 16 | G1 details | accepted (§6) | expected value before own write; exact `comm`/argv[0]; lock inode per sample |
| 17 | Driver debugfs `ecram` instead of mode A | forwarded to the operator (§12 item 9) | needs a driver patch and a spec change; not decided in the design |
