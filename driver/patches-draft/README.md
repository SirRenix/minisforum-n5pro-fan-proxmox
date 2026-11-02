# Draft patches

Patches here are **drafts**: not in `driver/patches/series`, not built by
`packaging/build-deb.sh`, not in any package. The build ignores this folder
(it only checks `driver/patches/`).

Numbering: the draft numbers below date from before sr2. Since `0.2.1+sr2`,
`driver/patches/0008-dmi-n5pro-f8nab.patch` is the released `0008`; a draft adopted later
gets the next free number and is rebased onto the sr2 series.

| Patch | Purpose | Status |
|---|---|---|
| `0008-debugfs-ecram.patch` | read-only debugfs file `ecram`: EC RAM 0x00-0xff through the driver's locked EC read path | draft 2026-10-16, revised after review (r2); build/static analysis clean, never run on hardware. Operator decision 2026-10-17: regenerated on rc2/rc3 only when sr2 starts, not before |
| `0009-hwmon-read-cache.patch` | `temp*_input`/`fan*_input` from a per-input cache, EC refresh at most once per second per input; EC timeouts cached for the same 1 s (audit-20261016 #28) | draft r2 2026-10-17, untested on the device; see [0009](#0009-hwmon-read-cache) |
| `rc3/0006-shutdown.patch` | replacement for `driver/patches/0006-shutdown.patch` (rc3): HDD statement in header and `n5_shutdown` comment corrected (#27); comment text only | **adopted 2026-10-17** into `driver/patches/` (`0.2.1+sr1~rc3`, [`rc3-build-20261017.txt`](../../befunde/raw/rc3-build-20261017.txt)); this copy is kept as the reviewed draft |

## 0008-debugfs-ecram

Operator decision 2026-10-10 on [`docs/TESTKIT-DESIGN.md`](../../docs/TESTKIT-DESIGN.md)
§12 item 9: EC RAM snapshots without userspace port I/O. Today a C1/T6 snapshot needs
mode A (module unloaded, `ec_probe dump` via `ioperm()`); with this patch the same
256 bytes come from the loaded driver.

- Base: `0001`-`0007` (`0.2.1+sr1~rc1`, patched source sha256 `c093187f…`), applies with
  `patch -p1 --fuzz=0`. Patched source sha256
  `106d013cf8363ee4b83b6c69231dea6d80a33b687d6546d2953120aaf1ac5cb7`; patch file sha256
  `999eb0e9721719abf7d05e7c23863b623ec83af41c8193f20cc80b293a39dc96`.
- Measurement build: `modinfo` version `0.2.1+sr2~rc1` (the draft sets it, so it is
  not mistaken for the sr1 build), srcversion `427A532316F6BC67BB30F37` (7.0.14-20-pve
  and 7.0.12-1-pve). A different srcversion is a different draft.
- File `<debugfs>/minisforum_n5_it5571/ecram`, mode 0400, read only, not seekable. Gate
  as `dcr` (0003): `debug_regs=1` (default off), matched board profile and DMI
  `sys_vendor` "Micro Computer (HK) Tech Limited"; `force=1` or another vendor: no
  file, warning.
- Access: 256 x `n5_ec_read_locked()` (command 0x80 + address on 0x62/0x66), one byte
  per `data->lock` hold (`mutex_lock_killable`), 0005 wait bound per byte (100 ms per
  status wait, resync) and 0005 per-access budget.
- Dump budget `N5_ECRAM_BUDGET_MS` = 2000 ms. A byte is started only while at least
  `N5_ECRAM_BYTE_US` = 3 x 100 ms + 2 x 10 ms = 320 ms of it are left, so the budget
  never cuts a started transaction. Otherwise the dump ends before any port access;
  kernel log (ratelimited): `ecram dump aborted before EC RAM 0xNN: less than 320000 us
  of the 2000 ms dump budget left` — no "EC timeout"/"timeout" in the text. Whether
  TESTSPEC K1 ("any `minisforum` timeout/error line") counts this line: **operator
  decision, open.**
- Not seekable: `lseek()` and `pread()` give `-ESPIPE` (with `DEFINE_SHOW_ATTRIBUTE`
  they started a whole new dump). `show` runs once per open; partial sequential reads
  come from its buffer.
- Output: 16 rows `00: xx xx …` (lower case, 832 bytes). `tools/f8a` `extract_rows … 2`
  parses the rows, but `load_ecram()` reads them only between `=== BEGIN EC_RAM n ===` and
  `=== END EC_RAM n ===` (`f8a-snapshot.sh`); a consumer must write that wrapper.
- Fairness: N parallel `ecram` readers (root only) make an hwmon access wait for up to
  N byte holds (N x 320 ms) instead of one.

### Error codes (`ecram` read)

All or nothing: the first failed byte ends the dump, nothing is printed. No `??` bytes
as in `ec_probe`.

| Situation | errno | Source |
|---|---|---|
| status wait ran out inside a transaction | `-ETIMEDOUT` | driver (0005 kernel warning "EC timeout …", command 0x80 + address) |
| less than 320 ms dump budget left before a byte | `-ETIMEDOUT` | driver (warning "ecram dump aborted …", no port access) |
| read running when unbind/unload starts | `-ENODEV` | driver (`stopping`, checked under the lock before each byte) |
| read after `.shutdown` | `-ENODEV` | driver (`dead`) |
| fatal signal (also while waiting for the lock) | `-EINTR` | driver |
| fd opened before unbind, read after it | `-EIO` | debugfs core (`debugfs_file_get()` on the removed dentry) |
| `open()` after unbind/unload | `-ENOENT` | path gone |
| `lseek()`, `pread()` | `-ESPIPE` | `nonseekable_open()` |
| `modprobe -r` while `ecram` or `dcr` is open | "Module is in use" | debugfs open holds a module reference (`.owner = THIS_MODULE`); not a driver error. Unbind is not blocked. |

`-ENODEV` from `dead` is practically unreachable on unbind/unload: debugfs is removed
before the restore action sets `dead`. The same holds for `dcr`, whose `-ENODEV` row in
the top-level `README.md` was corrected accordingly.

### Teardown and timing (C8/C9)

Order unchanged from 0003/0004: debugfs devres action (registered after hwmon, runs
first), hwmon, restore + `dead`, port regions. New in r2: the debugfs action gets
`data`, sets `data->stopping` (`WRITE_ONCE`) and then calls
`debugfs_remove_recursive()`, which waits for running readers.

| Bound (code, not measured) | Value |
|---|---|
| lock hold per `ecram` byte | ≤ 3 × 100 ms + 20 ms resync = 320 ms |
| one dump, wall time | ≤ 2 s + one foreign lock hold (≤ 1 s + 20 ms) + scheduling |
| wait in `debugfs_remove_recursive()` (unbind/unload) | ≤ byte in progress (320 ms) + foreign holds queued ahead of the reader (each ≤ 1 s + 20 ms, one in the normal case) ≈ 1.34 s + scheduling; first draft without `stopping`: up to a whole dump, ≈ 3.3 s |
| restore afterwards | ≤ 4 s, only for channels written since the bind (none with n5-fangov stopped) |

Build evidence: [`befunde/raw/p8-ecram-build-r2-20261016.txt`](../../befunde/raw/p8-ecram-build-r2-20261016.txt)
(first draft: [`p8-ecram-build-20261016.txt`](../../befunde/raw/p8-ecram-build-20261016.txt), superseded).

### Measure on the device before adopting

Prüfvorschrift first: criteria below are fixed before the run, not after.
n5-fangov stopped, writer lock free, no other EC reader. Every M-E protocol records
`modinfo -F version` and `modinfo -F srcversion` of the loaded module (mandatory;
expected `0.2.1+sr2~rc1` / `427A532316F6BC67BB30F37`, otherwise the run does not count).

| # | Step | PASS criterion |
|---|---|---|
| M-E1 | module unloaded: `f8a-snapshot.sh ecram-a1 --yes` (mode A, 2 dumps); load sr2 build with `debug_regs=1`; `cat …/ecram` 3x (to files); unload; `f8a-snapshot.sh ecram-a2 --yes` | every `ecram` byte equals the mode-A byte at the same offset, except 0x04 0x05 0x06 0x09 and offsets that already differ between the mode-A dumps (live bytes, list them); curve tables 0x10-0x27, 0x30-0x38, 0x70-0x81 byte-identical in all five dumps |
| M-E2 | `ecram` vs. `ecram` (3 reads) | same as `f8a-snapshot.sh` "dump 1 vs dump 2": only live bytes differ |
| M-E3 | 20x `time cat …/ecram` | every read complete (256 bytes, 16 rows); max wall time recorded; max < 500 ms, otherwise the 2000 ms budget is re-evaluated before adoption |
| M-E4 | `journalctl -k` over M-E1..M-E6 | no "EC timeout", no "ecram dump aborted", no warning of the driver besides the expected `debug_regs=1` info line |
| M-E5 | `/proc/ioports` during a read loop | 0x62/0x66/0x68/0x6c held by the driver as before; no 0x2e/0x2f while only `ecram` is read |
| M-E6 | `ecram` read loop + hwmon reader loop (all `fan*_input`/`temp*_input`) for 5 min | no read error in either loop; fan/temp values plausible (as F8b sampler). Also covers H1: the mode-A read sequence repeated while the driver is loaded |
| M-E7a | `ecram` read loop (new `cat` per read) during 10x unbind/bind via `/sys/bus/platform/drivers/minisforum_n5_it5571/{unbind,bind}` (device `minisforum_n5_it5571`), each `echo` timed; once additionally: open `ecram` on fd 3 (`exec 3<…/ecram`), unbind, read fd 3, close, bind | reader errors only `ENODEV` (read running at unbind), `ENOENT` (open while unbound) and, for the held fd, `EIO`; no oops, no hung task; after each bind: `ecram` present, `pwm1..4_enable=2`; every unbind ≤ 1.5 s (bound ≈ 1.34 s, no channel written) |
| M-E7b | `ecram` read loop during 10x `modprobe -r` / `modprobe … debug_regs=1`; `modprobe -r` retried (up to 20x, 0.5 s apart) while it reports "Module is in use" | "in use" refusals counted, not a FAIL; each cycle unloads within the retries; reader errors only `ENODEV`/`ENOENT`; no oops, no hung task; `pwm1..4_enable=2` after each reload. `f8a-stress.sh load` is not usable here: it aborts on the first failed `modprobe -r` |
| M-E8 | load without `debug_regs` | no `minisforum_n5_it5571` directory under debugfs |

Not measurable on the reference host (code review only): `force=1` / vendor mismatch gate,
`-EINTR`, behaviour with an EC that does not answer (F8c), the budget abort (needs
a slow EC).

Consequence for the spec (TESTSPEC 0.4, §12 item 9): mode A could be replaced for C1
and G6(b); T6 "before load" still needs a boot without the module (or mode A), and
"probe writes nothing" must then be shown another way.

### Checklist items affected (`docs/DRIVER-REVIEW-CHECKLIST.md`)

| Item | Effect | To re-check |
|---|---|---|
| G3 | new userspace-reachable read interface; fixed range 0x00-0xff, no offset/length input, not seekable, no write (0x81 unused) | code: no generic port-I/O primitive |
| G5 | second debugfs file, same parameter gate, 0400 | code + M-E8 |
| G6 | both new warnings ratelimited | code |
| B6 | no new Super-I/O access (`ecram` uses only 0x62/0x66) | code + M-E5 |
| C1 | each byte is one full transaction under `data->lock`; budget abort only before the command byte | code |
| C2 | `ecram` and hwmon interleave only between whole transactions; N readers delay hwmon by up to N byte holds | code + M-E6 |
| C3 | no cache involved (N/A for `ecram`) | — |
| C4, C5 | debugfs devres action runs before restore and region release; sets `stopping`, waits for the reader | code + M-E7a/b |
| C6 | file created after `mutex_init` and hwmon registration | code |
| C7, C8 | unbind/unload with a running or open `ecram` reader: `ENODEV`/`EIO`/`ENOENT`, unload refused while open | M-E7a/b |
| C9 | lock hold per byte ≤ 320 ms; dump ≤ 2 s + one foreign hold; teardown wait ≈ 1.34 s + restore ≤ 4 s | code + M-E3 + M-E7a |
| E1 | restore path unchanged; `ecram` has no write path | code + M-E7a/b |
| H1 | same read sequence as `ec_probe dump` in mode A; new is only the repetition while the driver and regulator run | M-E6 |
| H5 | `MODULE_VERSION` `0.2.1+sr2~rc1` in the draft | `modinfo` in every M-E protocol |

### Adopting

1. Measurements above with the sr2 rc build; results in `befunde/BEFUNDE.md`.
2. Move the patch to `driver/patches/`, append to `series`; `FORK_VERSION=sr2`,
   `FORK_RC` in `packaging/UPSTREAM`. Move the `MODULE_VERSION` hunk from 0008 to
   0001 (0001 sets it, as for sr1) and set `PACKAGE_VERSION` in `deploy/dkms.conf` to
   the same version.
3. Top-level `README.md`: add `ecram` to the debugfs paragraph (`debug_regs=1`,
   currently `dcr` only), to the error-code table (rows from "Error codes" above) and to
   the Timing section (bounds from "Teardown and timing" above).
4. Delta review against the sr1 review (`driver/README.md`, "Delta review"):
   hunks map to G, C, E, H; report `befunde/delta-review-<version>-<date>.md`.
5. TESTSPEC 0.4 and the tools (f8a mode B / n5-ectest snapshot) read `ecram`; f8a
   comparisons need the `=== BEGIN EC_RAM n ===` / `=== END EC_RAM n ===` wrapper
   around the 16 rows. Operator decides whether K1 counts "ecram dump aborted".

## 0009-hwmon-read-cache

**Status: draft r2 2026-10-17 (r1 2026-10-16), untested on the device.** Target sr2 (with 0008).
r2 adds the operator decision of 2026-10-17: EC timeouts are cached for 1 s (see Design).
Source: audit-20261016 BACKLOG #28, c-review rc2 DOS-001 (reviewer finding, not measured).

**Purpose.** `temp*_input` and `fan*_input` are 0444. Before, every read took
`data->lock` and did live EC/PMC2 I/O (temperature: one EC RAM read; tachometer: up to
4 x 3 PMC2 reads), without cache or minimum interval. Any local user (also a container that
sees the host's sysfs) could loop on these files: the regulator's `pwm`/`pwm_enable` writes
and the unbind restore queued behind every reader, and the EC got a continuous command
stream.

**Design.**

- Per input (temp1-temp4, fan1-fan3): result of the last refresh (raw value or error
  code), `jiffies` timestamp, `valid` flag in `struct n5_data` (hwmon
  `valid`/`last_updated` pattern). All under `data->lock`.
- Read within `N5_CACHE_MS` = 1000 ms after the last cached refresh of that input:
  served from the cache, no port I/O. Otherwise exactly the EC access of before (same
  transaction, same 0005 access budget); the timestamp is taken after the access.
- Refresh per input, not per group: a group refresh would hold the lock for 4 EC reads
  or 36 PMC2 reads, longer than one uncached read today.
- Errors (r2, operator decision 2026-10-17): a refresh ending in `-ETIMEDOUT` (status
  wait ran out, EC not answering) is cached for `N5_CACHE_MS`; reads in that window get
  `-ETIMEDOUT` from the cache, without port I/O and without a new "EC timeout" kernel
  line. `-EAGAIN` (torn tachometer read after 4 attempts, EC answered) is not cached,
  the next read tries again. The old value is never served after a failed refresh. EC
  sentinels (0x00/0xff, 0xffff) are successful answers: cached raw, still reported as
  `-ENODATA` (0007).
- `dead` is checked before the cache: after unbind/unload or `.shutdown` every read is
  `-ENODEV`. The cache lives in devm-allocated data and starts empty on every bind.
- `mutex_lock_killable()` for temp/fan reads only (unprivileged readers that may wait
  behind a refresh of up to 1 s can be killed; returns `-EINTR`, nothing touched before
  the lock). `pwm`/`pwm_enable` reads and all writes keep `mutex_lock()`, unchanged.
- No version change in 0009 (`MODULE_VERSION` from 0008, `0.2.1+sr2~rc1`).

**Base.** `0001`-`0007` rc2 (`48f47ff8…`) plus 0008. **0008 as drafted does not apply
to rc2** (made on rc1: its date hunk is already in rc2's 0001, its `MODULE_VERSION`
context is `sr1~rc1`); the functional hunks apply unchanged. The build used a rebased
0008 (plain diff, in the build protocol). Before adoption 0008 must be regenerated on
rc3 (operator decision 2026-10-17: at the start of sr2, not before). r2 applies on rc3 + 0008
and on rc2 + 0008 (on rc3 the rebased 0008 needs its `MODULE_VERSION` context line set to
`sr1~rc3`, done in the r2 build).

**Build evidence** ([`befunde/raw/p9-draft-build-20261016.txt`](../../befunde/raw/p9-draft-build-20261016.txt)):
patch sha256 `189751a9771d07bc52c3b9a3cda204006e254a6d05ba4dbca324003b8b456dc7`, applies
with `--fuzz=0`; patched source `c811befb…`, srcversion `14208792D282DBB30FCF9F5`
(7.0.14-20-pve and 7.0.12-1-pve). Plain, W=1, sparse, smatch: no message; checkpatch
`-f` 0/0, `--strict` 6 CHECKs as rc2. Userspace harness (fake EC): 50/50 cache checks
PASS (ASan/UBSan clean), negative control without 0009 fails exactly the cache checks;
the H8 pwm/restore/shutdown harness still 55/55.

**Build evidence r2** ([`befunde/raw/rc3-build-20261017.txt`](../../befunde/raw/rc3-build-20261017.txt),
sections B/H): patch sha256 `47f2baf0d23e5258a758a5beb9fc70df4299568e049f6b923bd2e7849d82779b`
(the run used `2a7bb3d3…`, which differs only in the DEP-3 "Tested:" line), applies with
`--fuzz=0` on rc3 + 0008 and rc2 + 0008; rc3 + 0008 + 0009 source `44f8a1a0…`, srcversion
`BE1F7D34ED33754EC9DC453` (both kernels). Plain, W=1, sparse, smatch: no message; checkpatch
`-f` 0/0, `--strict` 6 CHECKs. Harness h9 r2 (new: `-ETIMEDOUT` cached incl. a 1000-read flood
without port I/O, `-EAGAIN` from a torn tachometer read not cached): 58/58, ASan/UBSan clean;
negative control on r1 fails exactly the 4 error-cache checks; H8 harness 55/55.

**Risk.**

| Risk | Assessment |
|---|---|
| stale values | fan RPM/temperature up to 1 s + access time old. n5-fangov samples at ≥ 2 s, n5-ectest at 5 s (`SampleS`); the F8b sampler at 30 s. A tool that reads RPM directly after a pwm write sees the old value for up to 1 s (the cache is not invalidated on writes; T1 holds 60 s per step). |
| flood while the EC does not answer | r2: `-ETIMEDOUT` cached 1 s, so each input does at most one timed-out refresh per 1 s + access time, independent of the number of readers (r1: every read was a refresh). With all 7 inputs in a loop the lock can still be busy for up to 7 timed-out accesses per window |
| cached error | a reader (also n5-fangov) can get `-ETIMEDOUT` up to 1 s after the EC answers again; same error code as an uncached timeout, so a reader cannot tell the two apart |
| regulator wait | ≤ one refresh per expired input (≤ 7, each as long as one uncached read before) plus cache hits without I/O; before: one EC access per queued reader, unbounded |
| `-EINTR` | new error code for temp/fan reads, only for a process that is being killed |
| hwmon semantics | values in a `sensors` run are no longer from one instant per input (they were not before either) |

**Test idea (n5-ectest / TESTSPEC, Prüfvorschrift before the run).** With the sr2 rc build
(`modinfo -F srcversion` recorded), n5-fangov stopped unless stated.

| # | Step | PASS criterion |
|---|---|---|
| M-C1 | read `fan1_input` at 20 Hz for 60 s (`n5-ectest` raw logger or a shell loop with timestamps) | value changes at most once per ≥ 1.0 s window; no read error |
| M-C2 | 16 unprivileged reader loops on all `temp*_input`/`fan*_input` for 5 min; in parallel `pwm1_enable=1`, then `pwm1` 255/217/179 every 2 s, each `echo` timed | max write latency ≤ 1.1 s and not above the same run on rc2 (baseline run, same host, same loops); no "EC timeout" in the kernel log |
| M-C3 | same load, EC command rate: count `n5_ec_read_locked`/`n5_pmc2_read` with ftrace function tracing if not inlined, otherwise skip | ≤ 4 EC reads/s and ≤ 3 x 12 PMC2 reads/s from hwmon (+ writes) |
| M-C4 | F8b-style sampler and `n5-fangov validate` T0/T1 for one channel | results as with rc2 (T1 tach step ≥ 200 rpm still measured; 60 s hold ≫ 1 s cache) |
| M-C5 | unbind during reader loops; read after unbind; after reboot journal of the previous boot | reads `-ENODEV`/`ENOENT` only; `.shutdown` behaviour unchanged (T6) |

Not measurable on the reference host (code review + harness only): `-EINTR` of a killed
waiting reader, error path with an EC that does not answer.

**Checklist** (`docs/DRIVER-REVIEW-CHECKLIST.md`): C1/C2 unchanged (one transaction per
lock hold), C3 (cache only under `data->lock`), C9 (lock hold per read ≤ before), G5
unchanged (modes/visibility), H5 (no version change in 0009).

## rc3/0006-shutdown

Replacement for `driver/patches/0006-shutdown.patch` for rc3 (BACKLOG #27). The header and
the `n5_shutdown` comment said the EC does not regulate HDD after 0x2b ("fixed 0x57"). Per
M5 (`befunde/BEFUNDE.md`, derived from EC firmware, measured values fit) the EC regulates
the HDD group to a target RPM from EC 0x06 (ambient). HDD 255 at shutdown stays, now
justified as a precaution; PCIe reasoning unchanged. Comment text only, same line count.
Evidence in the same protocol: applies with `--fuzz=0` and `git apply`, rc3 source
`8948ec45…`, every changed line a comment line, comment-stripped sources identical to
rc2, all checkers clean (that build kept rc2's `MODULE_VERSION`, hence srcversion
`E9CF5951A3238F3B9930797` = rc2 there).

Adopted 2026-10-17 ([`rc3-build-20261017.txt`](../../befunde/raw/rc3-build-20261017.txt)):
copied to `driver/patches/0006-shutdown.patch`, `FORK_RC=rc3`, 0001 `MODULE_VERSION`
`0.2.1+sr1~rc3` and change-notice date 2026-10-17 (0002 context). Source `e6468d64…`;
srcversion `D1E4D4D188041A61EC3D872` — not equal to rc2, because modpost hashes string
literals and `MODULE_VERSION` changed; disassembly identical to rc2. Top-level `README.md`
HDD paragraph updated to M5 + Z2 at the same time.
