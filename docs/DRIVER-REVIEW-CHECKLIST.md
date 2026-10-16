# Driver Review Checklist — minisforum_n5_it5571

**Version 0.3 (2026-10-08), approved by the operator on 2026-10-08.**
Items are written independently of the current code; they describe what must be checked,
not what was found.

## 1. Purpose and scope

- Subject: the out-of-tree hwmon driver `minisforum_n5_it5571` (ITE IT5571 EC, Minisforum
  N5 family) as shipped in the DKMS package, exact upstream commit stated in the review header.
- Goal: decide whether the driver is fit for permanent use on a production Proxmox host
  with a userspace fan regulator writing `pwm*` via hwmon and `experimental_write=1`.
- Covered subsystems: platform driver + probe/remove, DMI match table, module parameters
  (`force`, `experimental_write`), raw port I/O to the ACPI-EC-style ports 0x62/0x66 and the
  PMC2 ports 0x68/0x6c, the EC command handshake, the driver mutex, hwmon_ops /
  `is_visible`, devm-managed hwmon registration, build metadata, DKMS packaging.
- Out of scope: the userspace regulator's control logic, EC firmware behaviour (covered by
  TESTSPEC.md), hardware validation runs.

## 2. How to use

For every item record:

| Field | Content |
|---|---|
| Result | `PASS` / `FAIL` / `N/A` (N/A needs a one-line reason and operator approval) |
| Evidence | `file:line` (or tool output reference under `befunde/raw/`) for PASS and FAIL alike |
| Severity | only for FAIL: the item's suggested severity (column "Sev"); a deviation needs a one-line reason |
| Note | short, factual; proposed fix if obvious |

Severity is defined by impact on the production host:

| Severity | Impact |
|---|---|
| critical | Data written to unknown/unintended hardware (wrong machine, wrong port, wrong EC register); fans can end up stopped or stuck low without the regulator noticing; kernel oops, panic or hard hang. |
| high | Wrong channel addressed or reported; fan stuck at a fixed value after an error, unload or userspace crash; error silently swallowed so userspace believes a write succeeded; unbounded wait in a path reachable from sysfs. |
| medium | Wrong or misleading value/error code visible to userspace, ABI deviation from hwmon documentation, race without hardware impact, missing cleanup on a rare path. |
| low | Style, checkpatch warnings without functional effect, documentation gaps, metadata inconsistencies. |

Review against the exact source the package builds (record commit hash and file checksum).
Dynamic tests on the production host run with the normal kernel and only as defined in
F8a (maintenance window) and F8b (observation under real operation). Debug-kernel analysis
(F8c) and the optional part of A8 run on a test kernel/machine only, never on the
production host. Wording of dynamic results: "no error observed in N days/runs under these
conditions", never "error-free".

## 3. Roles

| Role | Duty |
|---|---|
| Reviewer | Performs every item, records result and evidence, proposes severity. |
| Operator | Approves every `N/A` and every "justified"/"accepted" decision (e.g. F1, F3); decides every `medium` FAIL (fix / accept / defer). |
| Second reader | Independently confirms every `critical` or `high` verdict (PASS or FAIL) against the recorded evidence before it counts. |

## 4. Checklist

"Sev" = suggested severity if FAIL.

### A — Load, probe, DMI gating, write-enable gating

| ID | Check | Sev |
|---|---|---|
| A1 | The driver binds only on DMI-matched systems; the match table entries are specific (vendor + product, optionally board) and not wildcard-broad. | critical |
| A2 | `force` cannot enable writes on a non-matched system; its effect (read-only) is enforced in code, not only documented. | critical |
| A3 | `experimental_write=0` makes every write path return an error before any port access; no write command reaches the EC. | critical |
| A4 | Record the sysfs permissions of `experimental_write` and `force`. FAIL if a parameter is writable at runtime (≠ 0444) while the code evaluates it only at probe, or if `is_visible` and the write path use different conditions for write enablement. | high |
| A5 | Probe performs no EC writes; any probe-time EC reads are bounded and failures abort probe cleanly. | high |
| A6 | Probe error paths release every resource acquired so far (regions, allocations) in reverse order. | medium |
| A7 | Module init/exit: platform device and driver registration/unregistration are symmetric, including the init error path. | medium |
| A8 | PASS if, on failed `request_region` or DMI mismatch, probe/init aborts with `dev_err`/`pr_err` (including errno and port) before any `outb`. Evidence by code path; optionally by loading with the port held (dummy `request_region` on a test kernel, not on the production host). | high |
| A9 | Autoload: record whether `MODULE_DEVICE_TABLE(dmi, …)` causes automatic loading on matched systems. PASS if the autoload behaviour is documented in the README and consistent with A1; FAIL if the module autoloads on systems outside the match table. | medium |
| A10 | Provenance of `experimental_write=1`: record every place that sets it (package `modprobe.d` file, install script, kernel cmdline). PASS if each source is listed in the README/package docs with the risk statement (H3); FAIL if the package enables writes without documenting it. | high |
| A11 | The module creates its own `platform_device` in `module_init`: PASS if a second load attempt, an ID collision with an existing device and loading with `force=1` each fail cleanly or behave as documented, without a second instance touching the ports. | medium |
| A12 | `force` permission: FAIL if `force` is writable at runtime (≠ 0444) or can be changed after probe in a way that alters gating. | high |
| A13 | `force` has a `MODULE_PARM_DESC` stating "read-only, bypasses DMI match"; FAIL if missing or misleading. | low |

### B — Port I/O and EC handshake

| ID | Check | Sev |
|---|---|---|
| B1 | Compute the worst-case wait per IBF/OBF status poll and per complete sysfs access (all sub-transactions, e.g. a multi-register write). FAIL if > 100 ms per status poll or if any wait is unbounded. Report the per-access worst case. | high |
| B2 | Timeouts return a distinct error (e.g. `-ETIMEDOUT`/`-EIO`) that propagates to the hwmon callback and to userspace. | high |
| B3 | No busy-wait or sleeping call in atomic context; delays match context (`udelay` vs `usleep_range`/`msleep`). | critical |
| B4 | Every port touched is reserved via `request_region` (or devm variant) before use; no access to unreserved ports. | high |
| B5 | PASS only if (a) it is shown that `acpi_ec` does not hold the ports (`/proc/ioports`, `PNP0C09` `_STA`), (b) DSDT/SSDTs are searched for `SystemIO` and `EmbeddedControl` OperationRegions and `_Qxx`/`_TMP`/`_GLK` accesses to 0x62/0x66/0x68/0x6c with result "none" or a named serialisation (ACPI Global Lock), and (c) the residual risk of SMM/SMI firmware access is stated. Otherwise FAIL. | high |
| B6 | Super-I/O config ports (0x2e/0x4e), if used at all, are accessed via `request_muxed_region` with enter/exit sequences; N/A if not used. | high |
| B7 | After a timeout/error mid-transaction, the next transaction first resynchronises (drain OBF by reading the data port, wait IBF=0) or the device is marked failed. FAIL if neither. | high |
| B8 | Command bytes and register addresses are named constants with a stated source (datasheet, ACPI dump, measurement). | low |
| B9 | Read-back after write (if any) is used to verify, and a mismatch is reported, not ignored. | medium |
| B10 | Firmware contention beyond `acpi_ec`: PASS if, based on B5, either the ACPI Global Lock is shown not to be required (no `_GLK`, no AML access to the ports) or the driver takes it around each transaction; and SMM/SMI access to the ports is either excluded with a named source or accepted by the operator as residual risk. FAIL if the Global Lock is required and not taken. | high |
| B11 | Long-term conflict indicator: timeout/error counts from driver log (`dmesg`/journal) and regulator log over at least 7 days of production use are recorded. FAIL if any unexplained EC timeout or handshake error occurs; PASS with count 0 or each event explained. Evidence is taken from the F8b runs (no separate measurement). | medium |

### C — Locking and concurrency

| ID | Check | Sev |
|---|---|---|
| C1 | Each complete EC transaction (command + address + data + status waits) runs under one lock hold; no partial transactions outside the lock. | critical |
| C2 | Concurrent sysfs reads and writes (multiple readers + regulator writer) cannot interleave on the ports. | critical |
| C3 | Cached state (mode, last pwm) is read and updated under the same lock as the hardware access it mirrors. | medium |
| C4 | `remove()` ordering vs devm-managed hwmon: no hwmon callback can run against freed or released resources (regions, data) after/while `remove()` executes. | critical |
| C5 | Any hardware action in `remove()` (e.g. restoring automatic mode) happens while ports are still reserved and under the lock. | high |
| C6 | Mutex is initialised before the hwmon device is registered; no lock use in paths that can run before init. | high |
| C7 | sysfs `unbind`/`bind` of the platform driver: PASS if unbind runs the same restore path as unload (E1), rebinding reprobes cleanly, and no port access occurs between unbind and rebind. | high |
| C8 | Unload (`modprobe -r`) while the regulator holds hwmon files open or is writing: PASS if unload either blocks until callbacks finish or open file accesses return an error, with no oops and fans restored per E1. Evidence by code path and F8a test. | high |
| C9 | Latency under the mutex: compute the longest lock hold (a multi-transaction write with every sub-step at timeout). FAIL if it exceeds the regulator's write interval or blocks readers beyond the B1 per-access bound without being documented. | medium |

### D — sysfs / hwmon ABI

| ID | Check | Sev |
|---|---|---|
| D1 | `pwmN` accepts 0–255 per hwmon ABI; out-of-range input is rejected (`-EINVAL`) or clamped as documented; conversion to EC units is correct at both ends. | high |
| D2 | `pwmN_enable` modes match hwmon semantics (0/1/2 or documented subset); unsupported values return `-EINVAL`. | medium |
| D3 | Writing `pwmN` while not in manual mode has documented behaviour (switch, reject with `-EBUSY`, or ignore) and that behaviour is consistent. | medium |
| D4 | Tabulate for every readable attribute whether it is read live from the EC or cached. FAIL if `pwmN` or `pwmN_enable` return a cache that can differ from the EC register after a mode change or EC self-regulation and this is not documented in the README. | medium |
| D5 | Channel index → EC register/command mapping is explicit and identical for read and write; labels (`fanN_label`, `pwmN`) match physical fans. | high |
| D6 | `is_visible` exposes only attributes that are implemented; write permission bits are absent when writes are disabled. | medium |
| D7 | Table of all errno values per callback with cause. FAIL if a hardware error returns 0 or `-EINVAL`, if the same condition returns different errno in different callbacks, or if the table is missing from the README. | medium |
| D8 | Name the EC sentinel values (e.g. 0x00, 0xFF, tach 0/0xFFFF). FAIL if they are reported as temperature/RPM instead of `-ENODATA`/`-EIO`. Channels without tach: `fanN` hidden or documented. | medium |
| D9 | README/docs describe the same attributes, ranges and modes the code implements. | low |
| D10 | Multi-byte atomicity: for every value read in more than one EC transaction (e.g. tach low/high byte), PASS if both bytes are read under one lock hold and tearing (byte rollover between reads) is prevented or detected (re-read until stable). | medium |
| D11 | RPM conversion: formula, divisor and clock constant are stated with source; division by zero for tach 0 is impossible. FAIL if the formula is undocumented or can divide by zero. | medium |
| D12 | Stopped fan vs channel without tach: PASS if the two cases are distinguishable to userspace (`fanN_fault`/`fanN_alarm`, hidden attribute, or documented value). FAIL if both report the same value without documentation. | medium |

### E — Failure behaviour (what happens to the fans)

| ID | Check | Sev |
|---|---|---|
| E1 | On module unload the fans are returned to EC automatic control (or the documented safe state); this is unconditional and not dependent on userspace. | high |
| E2 | If the userspace regulator crashes or stops, the last written manual pwm persists or not — behaviour is stated; any driver-side watchdog/timeout is documented. | high |
| E3 | On EC timeout or I/O error during a write, the fan is not left at a lower value than before; the error is reported. | high |
| E4 | Suspend/resume and shutdown/reboot behaviour is defined (PM ops or explicit statement that none exist and what the EC does). | medium |
| E5 | No code path writes pwm 0 / fan stop unless explicitly requested by userspace; minimum duty limits, if any, are documented. | critical |
| E6 | A failed probe or a failed `remove()` hardware action is logged at a visible level. | medium |
| E7 | DKMS update ordering: PASS if the package stops the regulator before `modprobe -r` and starts it after the new module is loaded (systemd `Requires`/`After` or install script), and the order is documented. FAIL if the module can be unloaded while the regulator keeps writing. | high |
| E8 | EC hang after a wrong command: the recovery path (cold boot / full power-off, not warm reboot, if that is what the EC needs) and the thermal fallback while the EC does not respond are documented. FAIL if undocumented. | high |
| E9 | DKMS build failure after a kernel update: behaviour is documented (module missing → fans in EC automatic, regulator without target). PASS if the regulator detects the missing hwmon device and alerts, and fans are not left in manual mode across the reboot. | high |

### F — Build, kernel API, dynamic analysis

| ID | Check | Sev |
|---|---|---|
| F1 | `checkpatch.pl` clean or every remaining warning justified. | low |
| F2 | `sparse` (`C=2`) clean. | medium |
| F3 | `smatch` clean or warnings justified. | medium |
| F4 | `W=1` build without any warning originating from `minisforum_n5_it5571.c`; output stored under `befunde/raw/`. | low |
| F5 | No deprecated or removed kernel APIs (e.g. old `hwmon_device_register`, `.remove` signature returning int on current kernels); builds on running and fallback kernel. | medium |
| F6 | `MODULE_LICENSE`, `MODULE_AUTHOR`, `MODULE_DESCRIPTION`, `MODULE_VERSION`, `MODULE_DEVICE_TABLE` present; `MODULE_VERSION` matches the release/package version. | low |
| F7 | DKMS config (`dkms.conf`) builds exactly the reviewed source; no build-time patching. | high |
| F8a | Stress test on the production host with the normal kernel, in a maintenance window, operator present: load/unload loop 50× with parallel hwmon reads and regulator-style writes; `kill -9` of the regulator; one cold and one warm boot. After every step record the kernel log since the step and the fan state (pwm mode, tach, EC duty if available). PASS if the kernel log shows no oops, `BUG`, `WARNING`, hung task, soft lockup or EC timeout and the fans are in the expected state after every step; FAIL otherwise. Aborted runs are reported with the number of completed iterations. | high |
| F8b | 7-day observation on the production host under real operation, watcher with fixed criteria K1–K7 and mail alerts (TESTSPEC §5a). Run 1 = baseline with the currently packaged driver, run 2 = 7 days after the patched driver is installed; both are compared. PASS if no criterion fires without explanation in a complete 7-day run; FAIL if a criterion fires and is attributable to the driver; a run with sampler gaps or unexplained but not attributable events is INCONCLUSIVE and repeated. Result wording: "no error observed in 7 days under these conditions". Covers B11. | high |
| F8c | Dynamic analysis on a test kernel with `PROVE_LOCKING`/lockdep, KASAN and kmemleak enabled (never on the production host): load/unload loop (≥ 100), concurrency stress (parallel `sensors` reads + regulator-style writes), timeout fault injection. PASS if no lockdep splat, KASAN report, kmemleak entry or oops; FAIL otherwise. N/A is allowed only as "open until a second device exists", with operator approval; the open status is listed in the review report. | medium |

### G — Security

| ID | Check | Sev |
|---|---|---|
| G1 | All sysfs input parsed with `kstrto*`/hwmon core; no unchecked indexes derived from user input. | high |
| G2 | Writable nodes are root-only (0644 or stricter); no world-writable attribute. | high |
| G3 | No arbitrary register read/write interface (sysfs, debugfs, ioctl) that would turn the module into a generic port-I/O primitive. | critical |
| G4 | Kernel lockdown / Secure Boot implications stated: raw port I/O from a signed module, MOK signing via DKMS. | medium |
| G5 | debugfs, if present, is read-only or gated behind a parameter that is off by default; N/A if absent. | medium |
| G6 | Log messages contain no unbounded user-controlled data and cannot be triggered at high rate from userspace (ratelimited). | low |

### H — Documentation and provenance

| ID | Check | Sev |
|---|---|---|
| H1 | Source of every EC command/register (ACPI tables, vendor tool, measurement) is documented. | medium |
| H2 | Supported models and BIOS versions are listed; untested models are marked experimental. | medium |
| H3 | Risk statement for `experimental_write` is present in README and parameter description. | medium |
| H4 | License headers (SPDX) present and consistent with `MODULE_LICENSE`. | low |
| H5 | Reviewed commit, package version and file checksum are recorded in the review report. | medium |
| H6 | Kernel taint: the taint flags set by loading (`O` out-of-tree, `E` unsigned) are recorded from `/proc/sys/kernel/tainted`, and the README states the consequence for kernel bug reports. FAIL if undocumented. | low |
| H7 | Packaged source == pinned upstream commit: checksum of every packaged source file matches the file at the pinned commit. FAIL on any mismatch not covered by a documented patch. | high |
| H8 | Future versions: PASS if a delta-review procedure is defined (diff against the last reviewed commit, re-run of every item touched by the diff plus F1–F8c). FAIL if a version update would require either a full re-review or no review. | medium |

## 5. Tool commands

Run on a build host with the target kernel headers (e.g. inside the DKMS source dir):

```sh
KDIR=/lib/modules/$(uname -r)/build
perl $KDIR/scripts/checkpatch.pl --no-tree -f minisforum_n5_it5571.c
make -C $KDIR M=$PWD C=2 modules                 # sparse; needs `sparse` installed
make -C $KDIR M=$PWD W=1 modules                 # extra warnings (F4)
make -C $KDIR M=$PWD C=2 CHECK="smatch -p=kernel" modules   # smatch
modinfo ./minisforum_n5_it5571.ko                # F6 metadata, A9 alias lines
sha256sum minisforum_n5_it5571.c                 # H5, H7
```

Repeat the build items (F2–F5) for the fallback kernel's headers. Store tool output
under `befunde/raw/` with date and kernel version. F8a and F8b need no debug kernel; they
run on the production kernel (`CONFIG_DETECT_HUNG_TASK`, `CONFIG_SOFTLOCKUP_DETECTOR`, persistent
journal), F8b with the watcher in `tools/validation-watch/`. F8c needs a test kernel built
with `CONFIG_PROVE_LOCKING`, `CONFIG_KASAN` and `CONFIG_DEBUG_KMEMLEAK`.

## 6. Exit criterion

The review is complete when every item has a result with evidence, and:

- every `critical`/`high` verdict is confirmed by the second reader,
- no `critical` or `high` item is open (FAIL without fix or approved mitigation), and
- every `medium` item has an operator decision (fix / accept with reason / defer with owner).

`low` items are listed but do not block.

## Changelog

- 0.1-draft: initial draft.
- 0.2-draft: items made checkable after independent review; added firmware/ACPI contention, dynamic analysis, unbind/unload, autoload/params, multi-byte atomicity, latency, EC hang recovery, taint/DKMS failure, diff review, per-item severity, roles.
- 0.3: F8 split into F8a (stress in maintenance window, production kernel), F8b (7-day observation), F8c (debug kernel, open); operator approval.
