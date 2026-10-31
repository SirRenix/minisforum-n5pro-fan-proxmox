# Changelog

All notable changes to this repository. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

Versions are those of the DKMS package `minisforum-n5-it5571-dkms`; the mapping of git tag,
Debian version, DKMS `PACKAGE_VERSION` and `MODULE_VERSION` is in
[`packaging/UPSTREAM`](packaging/UPSTREAM). `srN` is our downstream revision on an upstream
base, `~rcN` a pre-release that sorts before it. The patch series is
[`driver/patches/`](driver/README.md); each patch has a DEP-3 header with reason, finding
IDs and test status. Finding IDs (`A1`, `B4`, `D6`, `F-1`, …) refer to the review checklist
and reports under [`docs/`](docs/) and [`befunde/`](befunde/BEFUNDE.md).

`n5-ectest` (`ectest/`) is versioned and tagged separately (`ectest/vX.Y.Z`); its entries
are listed here until it has its own changelog.

## [Unreleased] — 0.2.1+sr1

First downstream revision of upstream `v0.2.1` (`feb6d9a`). Current pre-release: `0.2.1+sr1~rc3`.
Built and statically checked (W=1, sparse, smatch, checkpatch on Proxmox kernel headers
7.0.14-20-pve and 7.0.12-1-pve). `~rc2` tested on the reference host in maintenance window 1
(2026-10-17, `befunde/BEFUNDE.md`) and in F8b run 2 (7 days, PASS); `~rc3` differs from `~rc2`
only in comments and `MODULE_VERSION`. T1–T3 on hardware are still open. The hardware tests per
patch are listed in each DEP-3 header and in [`docs/TESTSPEC.md`](docs/TESTSPEC.md).

### Driver patches

- `0001` Downstream marking: GPL-2.0 2(a) change notice, `MODULE_VERSION` = DKMS
  `PACKAGE_VERSION` (was `0.2.0` in a `0.2.1` package, F6), parameter texts of `force` and
  `experimental_write` state what read-only means and the write risk (A13, H3 in part).
- `0002` PWM writes only with DMI `sys_vendor` "Micro Computer (HK) Tech Limited" and either
  the upstream-validated N5 / F8NAA profile or N5 PRO / F8NAA with `experimental_write=1`.
  `force=1` and N5 Air are always read-only; the N5 PRO DMI entry also matches the vendor
  (A1, A2, A11, A13; A9 mitigated).
- `0003` Optional read-only debugfs file `minisforum_n5_it5571/dcr` (module parameter
  `debug_regs`, default off, vendor-gated): I2EC 0x1800–0x180f, the EC duty registers (D4).
- `0004` Remove ordering: ports via devres, restore runs after hwmon/debugfs are gone,
  late callbacks get `-ENODEV`; a failed restore is retried on the next unbind. PCIe
  (`pwm4`) is handed back at duty 255 instead of command 0x31, which left DCR5 at the last
  written value (B4, C4, C5, C7, C8, E1; audit F-1, F-2).
- `0005` Every EC/PMC2 status wait bounded at 100 ms, 1000 ms budget per sysfs access,
  resync after a timeout, every timeout logged rate-limited (B1, C9, B7; makes B11/K1
  observable; E3 in part).
- `0006` `.shutdown` hook: touched CPU/SSD channels back to EC automatic, HDD and PCIe to
  255, so no channel stays low across a warm reboot or poweroff (E4, E9).
- `0007` hwmon ABI: `fanN_min` removed, tach value 0xffff reported as `-ENODATA` instead of
  65535 rpm, `fan4_*` hidden (PCIe has no tach) (D6, D8, D12).

`rc2` (2026-10-16) changes `0004`/`0006` against `rc1`: PCIe hand-back at 255 on
`pwm4_enable=2` and on unbind/unload, and `pwm4` stays touched for `.shutdown`
(operator decision 2026-10-16).

`rc3` (2026-10-17) changes only comments in `0006` (header and `n5_shutdown()`): the HDD
channel is no longer described as unregulated after 0x2b ("fixed 0x57"). After 0x2b the EC
ramps the duty away from the written value (measured, maintenance window 1, Z2) towards a
target RPM chosen from EC 0x06 (firmware analysis M5, derived); a reaction to temperature is
not measured. HDD 255 at shutdown is kept as a precaution (audit-20261016 #27). `0001`
sets `MODULE_VERSION` `0.2.1+sr1~rc3`. Code otherwise identical to `rc2`
([`befunde/raw/rc3-build-20261017.txt`](befunde/raw/rc3-build-20261017.txt)).

### Packaging

- Driver source in the repository: `driver/upstream/` is a byte-identical copy of
  `UPSTREAM_COMMIT`, checked against `driver/upstream/SHA256SUMS` at build time and against
  the upstream tag on GitHub in CI; `packaging/build-deb.sh` applies `driver/patches/series`.
- Version scheme `FORK_VERSION`/`FORK_RC` in `packaging/UPSTREAM`, computed by
  `packaging/version.sh` (shared by `build-deb.sh`, `test-deb.sh` and the release
  workflow). `build-deb.sh` refuses a build whose `MODULE_VERSION` differs from the DKMS
  `PACKAGE_VERSION`. A tag with `-rcN` is published as a GitHub pre-release.

### Validation

- [`docs/TESTSPEC.md`](docs/TESTSPEC.md) 0.3 (2026-10-09): criteria fixed before
  maintenance window 1 — EC automatic decoded (rpm control, table inputs), T4/T5 on the
  tach target band, PCIe `N/A` for T2–T5, F8a/F8b kernel-log hits judged by one rule.
  Changelog of the spec itself: end of the document.
- Driver review checklist 0.3 and the P3 review report (`befunde/p3-review-20261009.md`).
- F8b baseline (7 days, unpatched driver `0.2.1-1`): PASS, summary in
  [`befunde/BEFUNDE.md`](befunde/BEFUNDE.md).
- `tools/f8a/`: maintenance-window scripts (snapshot, stress, PCIe hand-back check) with
  a fake-kernel test suite.
- f8a 0.2.4: `f8a-snapshot.sh` (read-only) also runs on N5 PRO/F8NAB, N5/F8NAA, N5A and
  N5 AIR/F8NAB (driver DMI table plus F8NAB from upstream issues #6/#8); an unknown board
  refuses before anything is read, without override; a different `sys_vendor` only warns.
  `f8a-stress.sh` and `f8a-pcie.sh` stay on N5 PRO/F8NAA with the measured vendor. Mode A
  checks all preconditions before the first read and names each: research tools missing
  (build hint), ACPI EC owning 0x62/0x66, Super-I/O driver loaded (new, also per research
  call), kernel lockdown (new, also per call). `--bin-dir`, fallback `<repo>/bin`; header
  with board, BIOS, EC firmware and profile; tool sha256 logged. README section "Snapshot on
  another N5 board"; 274 container tests.

### n5-ectest (`ectest/`, 0.1.0, not yet released)

- New test toolset that runs the tests of TESTSPEC 0.3 on N5-family boxes: `identify`,
  `snapshot`, long-term `log`, `map` and `handback` at the device, offline HTML `report`,
  `diff`, `restore`. Read-only except `map` and `handback`. Workflow `ectest.yml`.
- Shared Go packages (driver state, hardware status, writer lock) as module `go/n5`.
- TESTSPEC 0.4 (2026-10-31): after an abort on a temperature limit the channel of that sensor
  (Tctl → pwm1, NVMe → pwm2, HDD → pwm3) is set to 255 and stays there, only in a run that
  wrote; `restore_values` names the reason, the guardian state file records 255 (§4, #13).
  G6 reads the curve tables through the driver's debugfs `ecram` (0.2.1+sr2) and reports
  PASS/FAIL, tables in `meta.json` → `ec_ram_tables`; without `ecram` PARTIAL (§3, #9). T1:
  a table input of another channel that crossed a step is INCONCLUSIVE also when every other
  tach stays within ±50 rpm (§5.1, #24); the order of the verdict rules follows §5.1.
  `spec_deviations` no longer list #13 and G6; open: the rule-4 conditions #1/#4 and, for
  `handback`, T4/T5. `go/n5/n5driver`: `ECRAMPath`, `ParseECRAM`.

### Repository

- `CHANGELOG.md`, `SECURITY.md`, `.gitleaks.toml` (infrastructure rules as in n5-fangov).
- `/dist/` ignored; release packages come from CI only.

## [0.2.1-1] — 2026-09-26

First DKMS package. Upstream `v0.2.1` (`feb6d9a`) unchanged (`modinfo` version `0.2.0`).

### Added

- `minisforum-n5-it5571-dkms` built by `packaging/build-deb.sh`, upstream commit pinned and
  verified; installation test in Debian 13; release workflow publishes the `.deb` and its
  sha256.
- `postinst` takes over a hand installation from `deploy/install.sh`.
- Module options (`experimental_write=1`) and autoload under `/usr/lib/modprobe.d/` and
  `/usr/lib/modules-load.d/`; a file of the same name in `/etc/` overrides either.
- `Recommends: proxmox-default-headers`: `proxmox-headers-<series>` satisfies the header
  dependency but stops at its kernel series.

### Changed

- `deploy/install.sh` / `uninstall.sh` handle the driver only; the Bash regulator `n5-fand`
  moved to `legacy/` (replaced by [n5-fangov](https://github.com/SirRenix/n5-fangov)).

[Unreleased]: https://github.com/SirRenix/minisforum-n5pro-fan-proxmox/compare/v0.2.1-1...HEAD
[0.2.1-1]: https://github.com/SirRenix/minisforum-n5pro-fan-proxmox/releases/tag/v0.2.1-1
