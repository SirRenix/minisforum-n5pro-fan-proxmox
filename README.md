# N5 Pro EC / IT5571 — fan driver package and validation

DKMS package of the community driver
[`ltdstudio/minisforum-n5-it5571`](https://github.com/ltdstudio/minisforum-n5-it5571) for
the Minisforum N5 Pro, plus the validation data of its experimental N5 Pro profile.
The regulator on top is [n5-fangov](https://github.com/SirRenix/n5-fangov).

## Install (Proxmox VE 9 / Debian 13)

Download `minisforum-n5-it5571-dkms_<ver>_all.deb` and its `.sha256` from the
[latest release](https://github.com/SirRenix/minisforum-n5pro-fan-proxmox/releases/latest), then as root:

```
sha256sum -c minisforum-n5-it5571-dkms_*_all.deb.sha256
apt install ./minisforum-n5-it5571-dkms_*_all.deb
```

The package pulls `dkms` and the kernel header meta-package — on Proxmox VE
`proxmox-default-headers`, which follows the default kernel series like
`proxmox-default-kernel`; on plain Debian `linux-headers-amd64` — builds the module for
every kernel with headers and rebuilds it for each new kernel. A host that has only
`proxmox-headers-<series>` gets no headers for the next series: `apt install proxmox-default-headers`. It sets `experimental_write=1` (without it the
`pwm*` nodes stay hidden; risk: [Write access](#write-access-and-its-risk)) in `/usr/lib/modprobe.d/` and loads the module at boot via
`/usr/lib/modules-load.d/` ([Autoload](#autoload)); a file of the same name in `/etc/` overrides either.
Loading writes nothing to the EC; `pwm*_enable` then reads `2`, a value the driver
assumes, not one it read from the EC ([Attributes](#hwmon-attributes)). Only a regulator
writes. A hand installation from `deploy/install.sh` is taken over.

Check: `dkms status minisforum-n5-it5571` lists every kernel as `installed`,
`ls /sys/class/hwmon/*/pwm1` finds the node. Remove: `apt remove minisforum-n5-it5571-dkms`.

The driver source lives in [`driver/`](driver/README.md): `driver/upstream/` is a
byte-identical copy of upstream `v0.2.1` (commit `feb6d9a`, checked against GitHub in
CI and against its `SHA256SUMS` at build time), `driver/patches/` holds our patch series.
Package `0.2.1-1` ships upstream unchanged. Packages from `0.2.1+sr1~rc1` on apply the
series `0001`–`0007`; `modinfo -F version minisforum_n5_it5571` shows which one runs
(`0.2.0` = upstream as shipped, `0.2.1+sr1…` = patched). The current pre-release is
`sr1~rc3` (`rc1` plus the PCIe hand-back at 255 from `rc2`, below; `rc3` corrects
only comments in `0006`, the code equals `rc2` apart from the version string); hardware
validation on the N5 Pro is pending.
Versions and tags: [`packaging/UPSTREAM`](packaging/UPSTREAM). The release workflow
builds the package and tests the installation in Debian 13.

Unless marked otherwise, the driver behaviour below is the same in upstream `0.2.1`
and in `sr1`. "0.2.1:" marks upstream behaviour that `sr1` changes; "sr1" marks the
patched behaviour (patch number in brackets, `driver/patches/`).

## Supported hardware

| Product / board (DMI `product_name` / `board_name`) | BIOS / EC firmware | Status | PWM writes with this package |
|---|---|---|---|
| `N5 PRO` / `F8NAA` | 1.05 / 0.14 | tested on one host, validation in progress ([`docs/TESTSPEC.md`](docs/TESTSPEC.md), [`befunde/`](befunde/BEFUNDE.md)) | yes (package sets `experimental_write=1`); sr1: only with `sys_vendor` `Micro Computer (HK) Tech Limited`, which the DMI entry itself requires (0002) |
| `N5` / `F8NAA` | upstream: EC firmware V0.14 | validated upstream (Unraid 7.3.2, Linux 6.18); not tested here | 0.2.1: yes, validated profile writes without any option; sr1: only if `sys_vendor` is `Micro Computer (HK) Tech Limited` – the N5's vendor string is **not measured**, on a mismatch the N5 binds read-only (0002) |
| `N5A` or `N5 AIR` / `F8NAB` | – | experimental, read-only in this downstream | 0.2.1: **yes**, the package option is global; sr1: no (0002) |
| `N5 PRO` / `F8NAB` | – | experimental, read-only in this downstream | 0.2.1: no DMI entry, loads only with `force=1` (then writable, see below); sr1: `force=1` binds read-only (0002) |
| anything else | – | not supported | 0.2.1: with `force=1` writable; sr1: no |

The channel map and the command set belong to the board firmware, not to the IT5571
chip: the same chip on another board can wire or interpret the channels differently.
sr1 (0002) allows writes only with `sys_vendor` `Micro Computer (HK) Tech Limited`
and either the upstream-validated `N5` / `F8NAA` profile (no option needed) or
`N5 PRO` / `F8NAA` with `experimental_write=1`. The `N5 PRO` DMI entry also matches
that vendor, so an `N5 PRO` / `F8NAA` board from another vendor binds only with
`force=1`, and then read-only. The `N5` and `N5 Air` entries match without vendor
(their vendor string is not measured): on a vendor mismatch they bind read-only.
N5 Air and `force=1` are always read-only. Read-only means no PWM write: every
temperature or fan read still sends EC read commands (0x80 to 0x66; 0xd5 plus
subcommand to 0x6c/0x68). Other vendors' boards with this EC are not excluded in
principle, but they need their own validation and a patch; the package does not
write there.
The EC firmware version is readable without port access:
`cat /sys/class/dmi/id/ec_firmware_release`.

## Test your board

```
sudo n5-ectest identify
sudo n5-ectest snapshot now
```

`n5-ectest` runs the tests of [`docs/TESTSPEC.md`](docs/TESTSPEC.md) on any N5-family box:
identity, snapshots, a long-term log, channel mapping and hand-back (at the device), and an
offline HTML report of every result bundle. Read-only by default. Install, commands and
what to post: [`ectest/README.md`](ectest/README.md).

## Write access and its risk

Upstream marks the N5 Pro profile experimental and hides its `pwm*` nodes unless the
module parameter `experimental_write=1` is set. Where it is set:

| Source | File | Set by |
|---|---|---|
| this package | `/usr/lib/modprobe.d/minisforum-n5-it5571.conf` | `apt install` |
| hand installation | `/etc/modprobe.d/minisforum-n5-it5571.conf` (overrides the package file) | `deploy/install.sh` |
| PWM test (legacy, replaced by `n5-ectest map`) | `insmod … experimental_write=1` | `legacy/06-pwm-test.sh` |
| kernel command line | `minisforum_n5_it5571.experimental_write=1` | not used by this project |

Active value: `cat /sys/module/minisforum_n5_it5571/parameters/experimental_write`.
Read-only operation: `options minisforum_n5_it5571 experimental_write=0` in
`/etc/modprobe.d/minisforum-n5-it5571.conf`, then reload the module ([Updating](#updating-and-unloading)).

With writes enabled:

- A write goes straight to the fan output. `0` stops a fan. The driver has no lower
  limit, no temperature check and no watchdog; a wrong value or a writer that dies in
  manual mode can overheat CPU, SSDs or disks.
- The N5 Pro channel map is verified on one host (N5 PRO / F8NAA, BIOS 1.05, EC 0.14).
  Other BIOS or EC versions are untested.
- `pwmN_enable=2` does not restore EC regulation on every channel. CPU (0x21)
  regulates again. HDD (0x2b): the EC moves the duty away from the written value in a
  ramp; a reaction to temperature is not shown (details under "Without regulator,
  shutdown, suspend"). A regulator therefore parks the HDD channel on a fixed safe value. PCIe has no EC automatic: 0.2.1 sends
  0x31 and DCR5 keeps the last written value (also `0`); sr1 (0004, from `rc2`) writes
  255 instead and sends no 0x31.
- 0.2.1: the option is global. It also enables writes on the N5 Air profile, and with
  `force=1` on any system without a DMI match. Never combine `force=1` with 0.2.1.
  The validated N5 / F8NAA profile writes without the option. sr1 (0002): the option
  takes effect only on `N5 PRO` / `F8NAA` with the vendor above; `force=1` and N5 Air
  are read-only.
- One writer per `pwmN`. Two regulators on the same channel overwrite each other.

## Autoload

- The module carries DMI aliases from `MODULE_DEVICE_TABLE`:
  `dmi*:pn*N5*:rn*F8NAA*:`, `dmi*:pn*N5PRO*:rn*F8NAA*:` (sr1:
  `dmi*:svn*MicroComputer(HK)TechLimited*:pn*N5PRO*:rn*F8NAA*:`), `dmi*:pn*N5A*:rn*F8NAB*:`,
  `dmi*:pn*N5AIR*:rn*F8NAB*:` (`modinfo minisforum_n5_it5571`). They are glob patterns
  over the DMI modalias (spaces removed): udev loads the module on any system whose
  product name contains `N5` and whose board name contains `F8NAA` or `F8NAB`.
- The package also loads it at every boot via `/usr/lib/modules-load.d/`, on every
  host, matching or not. On an N5 / N5 Pro this is redundant to the alias load:
  removing or emptying that file does **not** stop the module from loading.
- The gate is the probe: exact match on product and board name. Without a match and
  without `force=1` the probe fails with `-ENODEV` ("unsupported system" in the kernel
  log); the module stays loaded, reserves no port, registers no hwmon device and does
  no port I/O. 0.2.1 does not check `sys_vendor`; sr1 (0002) checks it in the
  `N5 PRO` match and in the write gate.
- Disable autoload: only `blacklist minisforum_n5_it5571` in `/etc/modprobe.d/` does
  it. It stops the alias load and the boot load (`systemd-modules-load` honours the kmod
  blacklist, systemd 257: "Module '…' is deny-listed (by kmod)"); only a manual
  `modprobe minisforum_n5_it5571` still loads it. An empty
  `/etc/modules-load.d/minisforum-n5-it5571.conf` on top of the blacklist only removes
  the boot load attempt and its log line; on its own it disables nothing.

## hwmon attributes

| Attribute | Mode | Read from | Value |
|---|---|---|---|
| `temp1..4_input` | 0444 | EC live: EC RAM 0x09 / 0x04 / 0x05 / 0x06 | m°C; raw byte as signed 8 bit × 1000; raw `0x00` and `0xff` → `-ENODATA` |
| `temp1..4_label` | 0444 | constant | `CPU Temp`, `System Temp`, `Board Temp`, `Ambient Temp` |
| `fan1..3_input` | 0444 | EC live: PMC2 high/low/high byte | RPM; CPU / SSD / HDD group; raw `0xffff` is reported as 65535 (sr1: `-ENODATA`, 0007) |
| `fan4_input` | 0444 | constant `0` | PCIe header has no tachometer; `0` is not a stall (sr1: hidden, 0007) |
| `fan1..4_label` | 0444 | constant | `CPU Fan`, `SSD Fan`, `HDD Fan`, `PCIe Fan (no tach)` (sr1: `fan4_label` hidden, 0007) |
| `fan1..4_min` | 0644 | driver memory | 0.2.1: 0–65535; stored only, no effect on the EC, no alarm, writable even read-only. sr1: removed (0007) |
| `pwm1..4` | 0644, only with writes enabled | **cache** | 0–255 = duty register (DCR) value 1:1; write only in mode 1, else `-EBUSY`; sr1: read `-ENODATA` while the EC mode is unknown after a failed restore (0004) |
| `pwm1..4_enable` | 0644, only with writes enabled | **cache** | `0` full speed (writes 255), `1` manual (entering it writes 255 first), `2` EC automatic (hand-back command); sr1: `pwm4_enable=2` writes 255 instead of 0x31 (`pwm4` then reads 255); as `pwmN` for `-ENODATA` |

Channels: `pwm1`/`fan1` CPU (DCR1, TACH1), `pwm2`/`fan2` SSD (DCR2, TACH2), `pwm3`/`fan3`
HDD group (DCR3 + DCR4 together, TACH3), `pwm4` PCIe (DCR5, no tachometer).

**`pwmN` and `pwmN_enable` are cached, not read from the EC.** They return what the
driver last wrote successfully. After loading they read `255` / `2` whatever the EC
does (the driver writes nothing at load). In mode `2` the EC regulates `pwm1`..`pwm3` on
its own while `pwmN` keeps showing the last written value; `pwm4` (PCIe) has no EC
automatic: 0.2.1 leaves DCR5 at the last value, sr1 holds it at 255 and `pwm4` reads 255. After a failed write the cache keeps the
old value although the EC may have taken part of a multi-register update. An unbind
sends the automatic command for every touched channel; the EC stays in manual mode
only if that restore fails. sr1 (0004): PCIe is handed back at 255 instead (no
0x31), on unbind as on `pwm4_enable=2`; after `pwm4_enable=2` the channel stays
"touched", so unbind and `.shutdown` write 255 again. 0.2.1: a following bind resets the cache to `255` / `2`
regardless. sr1 (0004): after a failed restore a bind marks that channel "mode
unknown", `pwmN`/`pwmN_enable` read `-ENODATA` until `pwmN_enable` is written; the
mark survives unbind/bind, not a module reload. The live duty is in the DCR
registers: sr1 adds the read-only debugfs file `<debugfs>/minisforum_n5_it5571/dcr`
(0003, mode 0400), read under the driver lock. It exists only with the module
parameter `debug_regs=1` (default off) on a matched board with the vendor above;
otherwise the parameter is ignored with a warning. It is for validation only: never
put `debug_regs` into `modprobe.d`. Without it the live duty is readable only with
`n5_i2ec_read` with the module unloaded ([Port tools](#port-tools-and-the-loaded-module)).

```
modprobe minisforum_n5_it5571 debug_regs=1     # reload order: Updating and unloading
cat /sys/kernel/debug/minisforum_n5_it5571/dcr # 1800: <16 bytes>, 0x1802.. = DCR0..7
```

**RPM.** The EC firmware computes the RPM and the driver only reads it:
`rpm = (high << 8) | low`, sampled high-low-high and retried up to 4 times against
torn values. Firmware formula: `RPM = 2,156,250 / tach_count`, from the EC clock
9.2 MHz / 128 = 71,875 Hz tach sample clock and 2 pulses per revolution
(`60 × 71,875 / (tach_count × 2)`). The driver divides nothing, so it cannot divide by
zero. What the firmware reports for a count of 0 is not documented; upstream reports
`TACH2=0` in one measurement (`RESULTS.en.md`). Source: upstream `docs/research/N5-PWM-RPM-REVERSE-ENGINEERING.zh-CN.md`
and `docs/publication/RESULTS.en.md` at `feb6d9a`, not vendored; excerpt in
[`befunde/raw/p3-d-upstream-docs-20261009.txt`](befunde/raw/p3-d-upstream-docs-20261009.txt).

## Error codes

| Attribute / step | errno | Cause |
|---|---|---|
| any write | `-EINVAL`, `-ERANGE` | not a decimal number or out of `long` range (hwmon core, before the driver) |
| `tempN_input` | `-ETIMEDOUT` | EC status poll ran out (see [Timing](#timing)) |
| | `-ENODATA` | raw value `0x00` or `0xff` (sensor not valid) |
| `fan1..3_input` | `-ETIMEDOUT` | PMC2 status poll ran out |
| | `-EAGAIN` | high byte changed in all 4 attempts (no consistent sample) |
| | sr1: `-ENODATA` | raw `0xffff` (0007) |
| `fan4_input`, labels, `pwmN`, `pwmN_enable` (read) | – | no EC access; 0.2.1: never fail |
| `pwmN`, `pwmN_enable` (read) | sr1: `-ENODATA` | EC mode unknown after a failed restore (0004) |
| `fanN_min` (write) | `-EINVAL` | 0.2.1 only: value outside 0–65535 (sr1: attribute removed) |
| `pwmN` (write) | `-EINVAL` | value outside 0–255 |
| | `-EBUSY` | `pwmN_enable` is not `1` |
| | `-ETIMEDOUT` | EC handshake ran out; the EC may hold a partial update (CPU and PCIe write 4 curve points) |
| | `-EPERM` | writes disabled (node is hidden then, practically unreachable) |
| `pwmN_enable` (write) | `-EINVAL` | value other than 0, 1, 2 |
| | `-ETIMEDOUT` | EC handshake ran out; mode in the cache unchanged. Where the mode change writes 255 (`0`, `1`; sr1 also `pwm4_enable=2`), the EC may hold a partial update as for `pwmN` (CPU and PCIe write 4 curve points) |
| any EC access | sr1: `-ENODEV` | teardown (unbind, unload) or `.shutdown` has started; no port access after the restore (0004, 0006) |
| | sr1: `-ETIMEDOUT` | also when the 1 s access budget is used up (0005) |
| `dcr` (debugfs read) | sr1: `-EBUSY` | Super-I/O 0x2e/0x2f held by another driver (`request_muxed_region`, 0003) |
| | sr1: `-ENODEV` | read after `.shutdown` (on unbind/unload debugfs is removed before the restore sets `dead`, so practically unreachable there) |
| | sr1: `-EIO` | file opened before unbind, read after it (debugfs core, removed dentry); `open()` after unbind/unload: `-ENOENT`. While `dcr` is open, `modprobe -r` fails with "Module is in use" (debugfs open holds a module reference); unbind is not blocked |
| probe (kernel log) | `-ENODEV` | no DMI match and no `force=1` |
| | `-EBUSY` | one of the ports 0x62/0x66/0x68/0x6c is reserved by another driver (e.g. active `acpi_ec`) |
| | `-ENOMEM`, hwmon error | allocation or registration failed |

0.2.1 logs no timeouts; a timeout is visible only as the errno in the reader (the
regulator log). sr1 (0005) logs every timeout as a rate-limited kernel warning
(`EC timeout: port 0x.. status 0x.. waiting for IBF/OBF=…, command 0x.. 0x..`;
suppressed messages are counted as "callbacks suppressed"). After a failed write the
driver does **not** write 255 on its own, neither version: the cache keeps the last
confirmed value, the error goes to the caller, and the EC may hold part of the
update. Falling back to a safe value is the regulator's job (n5-fangov: all channels
255 and an alarm after two write errors); the driver alone does not guarantee that a
fan is never left lower than before.

## Timing

0.2.1: each EC handshake step polls the status register up to 5000 times with
20–50 µs sleeps: 100–255 ms before `-ETIMEDOUT`. All loops are bounded. Worst case per
access, every step at its limit (derived from the code, not measured;
[`befunde/raw/p3-b1-worstcase-20261009.txt`](befunde/raw/p3-b1-worstcase-20261009.txt)):

| Access | Handshake steps | Worst case |
|---|---|---|
| `tempN_input` | 3 | 0.77 s |
| `fan1..3_input` | up to 48 | 12.2 s, then `-EAGAIN` |
| `pwm1`/`pwm4` write, `enable` 0 or 1 on them | 19 | 4.85 s |
| `pwm2`/`pwm3` write, `enable` 0 or 1 on them | 7 | 1.79 s |
| `pwmN_enable=2` | 3 | 0.77 s |
| module unload | up to 12 | 3.06 s |

All EC accesses share one mutex: a read waits for a write in progress, so one sysfs
read can block for the sum (e.g. 4.85 s + 12.2 s). This exceeds a 2 s regulator
interval and the default 10 s. A regulator has to tolerate slow or failing reads and
must not treat one timeout as a sensor failure.

sr1 (0005), derived from the code, not measured:

| Bound | Value |
|---|---|
| one status wait (IBF/OBF) | ≤ 100 ms (`N5_WAIT_TIMEOUT_US`) |
| one sysfs access, all waits together | ≤ 1 s budget (`N5_ACCESS_BUDGET_MS`), then `-ETIMEDOUT` |
| resync after a timeout (next transaction on that interface) | 10–20 ms sleep, drain, wait for IBF=0 |
| `fan1..3_input` | up to 4 high-low-high attempts within the 1 s budget, then `-ETIMEDOUT` or `-EAGAIN` |
| mutex hold per access | ≤ ~1.02 s plus scheduling delay |
| read waiting behind a write | ≤ ~2.04 s (two accesses) |
| unbind/unload restore, `.shutdown` | 1 s budget per touched channel, ≤ ~4.1 s for four |

A regulator interval of 2 s is therefore just met in the worst case.

## Without regulator, shutdown, suspend

- **No driver-side watchdog or timeout.** A value written in mode `1` or `0` stays in
  the EC until someone writes again, the module is unloaded (unload hands every
  channel it touched back with the automatic command; sr1: PCIe at 255, 0004) or
  the EC is reset. If the
  regulator dies or hangs, its service manager has to set a safe state (n5-fangov:
  `WatchdogSec=60`, `ExecStopPost` failsafe CPU/SSD automatic, HDD 140). If the host
  hangs or panics, nothing runs and the EC keeps the last values.
- 0.2.1: **no `.shutdown`**. On reboot or power-off the driver does nothing; manual
  values stay until the BIOS/EC reinitialises them. Upstream reports all four channels
  back in EC automatic after a reboot on the N5. On the N5 Pro (measured 2026-10-17,
  one cold and one warm boot, `befunde/BEFUNDE.md` §Wartungsfenster 1): CPU and SSD
  come up in EC automatic, HDD at a fixed boot duty of 100 (0x64), PCIe at 0 after a
  cold boot but at its last value after a warm boot.
- sr1 (0006): `.shutdown` handles every channel the module touched before reboot or
  power-off: CPU (`pwm1`) and SSD (`pwm2`) go back to EC automatic (0x21, 0x2e), HDD
  (`pwm3`) and PCIe (`pwm4`) are set to 255. PCIe: after 0x31 the EC keeps the last
  value. HDD, as a precaution: after 0x2b the EC lowers DCR3 and DCR4 from the written
  value in small steps, alternating between the two (measured on the reference host
  2026-10-17: from 0xb4 (180) to 0x72/0x6a, fan3 2736 to 2039 rpm, within 15 s, not
  yet settled at the end of the log; `befunde/raw/z2-dcr34-hdd-20261017T133518Z.txt`).
  According to the EC firmware (V0.14, derived, not measured) the target is a fixed
  RPM chosen from EC RAM 0x06 (ambient), not from the disk temperatures. End value,
  target RPM and a reaction to temperature are not measured. A PCIe channel handed
  back with `pwm4_enable=2` counts as touched (0004), rc1 skipped it here. A failed
  step is logged and leaves that channel as it is. Across a warm reboot PCIe keeps its
  last value until the regulator sets it, also if the module is missing for the new
  kernel (measured 2026-10-17, one warm boot: 0xff). HDD does not: BIOS/EC set it to 100
  (0x64) during the boot (same measurement, and after one cold boot), below n5-fangov's
  stop duty 140, until the regulator takes over; the 255 does not carry n5-fangov's HDD
  stop failsafe across a reboot. Whether `.shutdown` itself wrote 255 is not visible in
  that data.
  On power-off HDD/PCIe spin up briefly before the power goes.
- No PM callbacks in either version. The host is a server and does not suspend;
  suspend and resume are not supported (no resume handler, cache and EC can differ
  afterwards).

## Updating and unloading

`apt` upgrades and kernel updates build the module for every kernel with headers but
never reload a loaded module (`postinst` only prints a note); the new build takes
effect at the next boot. On upgrade the package (from sr1 on) stops an active
`n5-fangov` (or legacy `n5-fand`) before `dkms remove`, records it in
`/run/minisforum-n5-it5571-dkms.restart` and starts it again after the build and
load; if the module is not loaded, it stays stopped. `apt remove` stops the regulator
and leaves it stopped. The record holds only what ran at this upgrade (a stale one from
an interrupted run is discarded). If `postinst` fails before the regulator runs again
(`dkms add`, a temporary file), it starts the regulator anyway and exits non-zero;
`dpkg --configure -a` finishes the installation. If an upgrade is rolled back (the new
package's `preinst` or unpacking fails), the installed version registers and builds its
module again and starts the regulator (`postinst abort-upgrade`). An upgrade from
0.2.1-1 still runs the old `prerm`/`postinst`: it does not stop the regulator, and after
a rolled-back upgrade from it check `dkms status` (`apt install --reinstall` repairs a
missing entry). The module holds no reference while a regulator uses it, and
the n5-fangov unit only orders itself `After=` module loading: `modprobe -r` succeeds
while the regulator writes, and 0.2.1 can then access released ports (fixed in sr1,
0004). Reload by hand in this order:

```
systemctl stop n5-fangov            # failsafe: CPU/SSD automatic, HDD 140
modprobe -r minisforum_n5_it5571    # hands touched channels back (EC automatic; sr1: PCIe 255)
modprobe minisforum_n5_it5571
systemctl start n5-fangov
```

After the unload the HDD channel leaves the failsafe value (it was handed back with
0x2b; the EC ramps it towards its own target, see `.shutdown` above) until the
regulator runs again. A touched PCIe channel runs at 255 after the unload (sr1, 0004); 0.2.1 sends
0x31 and DCR5 keeps the last written value. `scripts/99-restore.sh` stops a running regulator itself.

## EC not responding

Symptoms: every `temp*`/`fan*` read returns `-ETIMEDOUT` or `-EAGAIN`, writes fail,
the regulator raises sensor and write alarms. The fans keep whatever duty the EC last
applied; the driver cannot change them.

- Thermal fallback: the CPU's own thermal protection (throttling, critical shutdown)
  does not depend on the EC. Disk and NVMe temperatures via `drivetemp`, `nvme` or
  `smartctl` do not either. If they climb, shut the host down.
- Recovery: stop the regulator, unload the module, do not run port tools against the
  EC. Then a **full power-off**: shut down, disconnect power until the device is dark,
  power on. A warm reboot does not reset the EC completely (2026-10-17: PCIe kept its
  last value; HDD was set to 100 by the boot); do not rely on it.

## Port tools and the loaded module

The upstream research tools (`n5_fan`, `ec_probe`, `n5_i2ec_read`, `sio_probe`) access
the ports with `ioperm()` and bypass the driver's lock. `n5_fan` and `ec_probe` use the
driver's ports 0x62/0x66/0x68/0x6c; an interleaved transaction returns wrong values or
leaves the EC mid-command. `n5_i2ec_read` and `sio_probe` use Super-I/O 0x2e/0x2f but
talk to the same EC. **Never run any of them while the module is loaded:**
`systemctl stop n5-fangov`, `modprobe -r minisforum_n5_it5571` first.
`scripts/03-probe-readonly.sh` and the baseline step of `legacy/06-pwm-test.sh` do not
check this.

## Secure Boot, lockdown, taint

- DKMS signs the module with its own key (`/var/lib/dkms/mok.key`, `mok.pub`). With
  Secure Boot on, the kernel loads it only after that key is enrolled as MOK
  (`mokutil --import /var/lib/dkms/mok.pub`, reboot, confirm in the MOK manager).
  Without enrolment the module does not load: no fan nodes, the regulator's start
  check fails, the fans stay on the EC/BIOS curve.
- Reference host: Secure Boot off, `module.sig_enforce=N`, lockdown `none`; module
  signed with the DKMS key, key not enrolled. The kernel logs "module verification
  failed: signature and/or required key missing" and loads it anyway.
- Lockdown does not restrict the module's own port I/O. It blocks `ioperm()`/`iopl()`
  for the userspace tools. It does **not** block the sr1 debugfs file `dcr`: debugfs
  lets 0400 files without write/ioctl/mmap through in both lockdown modes (Linux 7.0
  `fs/debugfs/file.c`, `debugfs_locked_down()`). Not measured here, the reference host
  runs without lockdown.
- Taint: loading sets `O` (out-of-tree, 4096) and `E` (unverified signature, 8192); on
  the reference host `/proc/sys/kernel/tainted` reads 12289 (plus `P` from ZFS). The
  taint stays until reboot, also after unloading. Kernel and Proxmox developers may
  decline bug reports from a tainted kernel: reproduce a kernel problem without this
  module (blacklist it, reboot) before reporting, and state the taint value.

## Command and register sources

No ACPI source exists: BIOS 1.05 has `PNP0C09` with `status=0`, no EC region and no
`_Qxx` methods for these commands ([`befunde/raw/p3-acpi-20261009.txt`](befunde/raw/p3-acpi-20261009.txt)).
No vendor tool is known. "Upstream" = reverse engineering of F8NAA EC firmware V0.14
by the upstream author; only the tachometer commands are documented there, the write
commands come from the code (comments) and upstream's validation on the N5.

| Access | Command / register | Source | Measured here (N5 PRO, [`befunde/BEFUNDE.md`](befunde/BEFUNDE.md)) |
|---|---|---|---|
| EC RAM read | ports 0x62/0x66, command 0x80 | ACPI specification (standard EC read) | Phase 3/5: reads work, no ACPI driver on the ports |
| temperatures | EC RAM 0x09 CPU, 0x04 System, 0x05 Board, 0x06 Ambient | upstream (source header) | Phase 3: only these bytes change under load, CPU follows `k10temp` |
| fan command family | PMC2 ports 0x68/0x6c, command 0xd5 + subcommand | upstream research doc | Phase 3: `n5_fan probe` finds PMC2 at 0x68/0x6c, chip ID 0x5571 |
| TACH1/2/3 | 0x18/0x19, 0x16/0x17, 0x14/0x15 (low/high) | upstream research doc (firmware RPM cache, divisor) | Phase 5: equal to `n5_fan status`; Phase 6: follow `pwm` |
| CPU write / auto | 0x23, 0x25, 0x27, 0x29 + action 0x20 / 0x21 | upstream code, validated on N5 | Phase 6: only fan1 reacts; after 0x21 DCR1 regulates under load |
| SSD write / auto | 0x2f + action 0x2d / 0x2e | upstream code, validated on N5 | Phase 6: only fan2 reacts; after 0x2e RPM back, regulation not checked |
| HDD write / auto | 0x2c + action 0x2a / 0x2b | upstream code, validated on N5 | Phase 6: only fan3 reacts, DCR3 = DCR4; after 0x2b leaves written value, curve vs. default open |
| PCIe write / auto | 0x33, 0x35, 0x37, 0x39 + action 0x30 / 0x31 | upstream code, physically validated on N5 | Phase 6: no fan on the header, DCR5 follows; after 0x31 stays on the written value ([`p1-ec-tabellen`](befunde/p1-ec-tabellen-20261008.md) §3: not separable from "curve points overwritten"; since M5 derived: no EC automatic for DCR5, [`BEFUNDE.md`](befunde/BEFUNDE.md)); sr1 sends no 0x31, hand-back = 255 (0004) |
| live duty (I2EC) | Super-I/O 0x2e/0x2f, XDATA 0x1801 CTR, 0x1802–0x1809 DCR0–7 | upstream research doc (IT5571 PWM block at 0x1800) | basis of every DCR statement in `befunde/`; 0.2.1: not used by the module; sr1: read-only debugfs `dcr` with `debug_regs=1` (0003) |

## License

GPL-2.0, see [`LICENSE`](LICENSE). The upstream driver source keeps its own license
files: [`driver/upstream/LICENSE`](driver/upstream/LICENSE) and `driver/upstream/LICENSES/`.
Changes: [`CHANGELOG.md`](CHANGELOG.md). Security reports: [`SECURITY.md`](SECURITY.md).

---

## Validierung (deutsch)

Sicherheitsregeln, EC-Protokoll und Phasenplan: [`docs/VALIDIERUNG.md`](docs/VALIDIERUNG.md) —
vor dem ersten Skript lesen. Ergebnisse: [`befunde/BEFUNDE.md`](befunde/BEFUNDE.md),
Upstream-Issue: [ltdstudio/minisforum-n5-it5571#7](https://github.com/ltdstudio/minisforum-n5-it5571/issues/7).

### Aufsetzen

```
scp -r n5pro-ec root@<pve-host>:/root/
ssh root@<pve-host>
cd /root/n5pro-ec && chmod +x scripts/*.sh
```

Voraussetzungen: `build-essential git lm-sensors smartmontools dkms proxmox-headers-$(uname -r)`.

### Ablauf

`03` und `06` rufen Port-Tools (`ioperm`) auf und prüfen nicht, ob das Modul geladen
ist. Vorher `systemctl stop n5-fangov` und `modprobe -r minisforum_n5_it5571`
(Grund: [Port tools](#port-tools-and-the-loaded-module)).

```
./scripts/01-baseline.sh          # DMI, ioports, Referenztemperaturen   lesend
./scripts/02-build-tools.sh       # Repo klonen, Userspace-Tools bauen   lesend
./scripts/03-probe-readonly.sh    # EC-Dump vor/nach Last                lesend
./scripts/04-build-module.sh      # Modul gegen laufenden Kernel bauen   lesend
./scripts/05-load-readonly.sh     # Modul laden, PWM gesperrt            lesend
```

Danach `befunde/BEFUNDE.md` ausfüllen und bewerten. Erst wenn die Werte
plausibel sind und du im Wartungsfenster am Gerät sitzt:

```
./legacy/06-pwm-test.sh 4         # ein Kanal, schreibend (abgeloest durch n5-ectest map, ectest/)
```

Jederzeit:

```
./scripts/99-restore.sh           # Regler stoppen, zurück auf EC/BIOS-Automatik, Modul entladen
./scripts/99-restore.sh --restart # dasselbe, danach Modul laden und Regler wieder starten
```

### Treiber ohne Paket

`deploy/install.sh` baut den Treiber aus dem Upstream-Klon (`02-build-tools.sh`) als
DKMS-Modul und legt Optionen und Autoload nach `/etc/`. Das ist der Weg für Entwicklung
am Treiber; im Betrieb das Paket nehmen. `deploy/uninstall.sh` entfernt es wieder.
Beide verweigern, wenn das Paket installiert ist.

### Struktur

```
driver/                Treiberquelle: upstream/ (unverändert feb6d9a) + patches/
packaging/             DKMS-Paket: Versionen, Build, Installationstest
deploy/                dkms.conf, Modul-Optionen, Autoload, install.sh/uninstall.sh
docs/VALIDIERUNG.md    EC-Protokoll, Sicherheitsregeln, Phasenplan
docs/TESTSPEC.md       Testspezifikation (T0–T6, F8a/F8b)
docs/TESTKIT-DESIGN.md Entwurf von n5-ectest
docs/DMI-PATCH.md      falls der BIOS-DMI-String nicht exakt passt
docs/BETRIEB.md        Betrieb mit n5-fand (archiviert)
ectest/                n5-ectest: Test-Toolset (Go), README, systemd-Units
go/n5/                 gemeinsames Go-Modul (n5driver, hwstatus, writerlock, identity)
tools/f8a/             F8a-Skripte (Snapshot, Stress, PCIe) mit Container-Tests
tools/validation-watch/ F8b-Watcher (Lauf 1/2)
scripts/               die Phasenskripte 01–05 und 99-restore
befunde/BEFUNDE.md     Ergebnistabellen
befunde/raw/           Rohlogs
legacy/                n5-fand (Bash-Regler, abgelöst durch n5-fangov), sein Uninstaller,
                       06-pwm-test.sh (abgelöst durch n5-ectest map)
upstream/              Klon von ltdstudio/minisforum-n5-it5571 (02 legt ihn an)
build/, build-tools/, bin/   lokale Bauprodukte der Phasenskripte
```

Treiberdetail, das man wissen muss: `pwmN` ist nur beschreibbar, wenn
`pwmN_enable=1` (manuell) gesetzt ist — sonst `-EBUSY`. Der Wechsel auf 1
setzt intern zuerst 255. `pwmN_enable=2` gibt an die EC-Automatik zurück.
Auf dem N5 Pro (BIOS 1.05) regelt der EC den **HDD-Kanal** danach wieder: gemessen
(17.10.2026, Z2) fährt er DCR3/DCR4 abwechselnd in Stufen vom Schreibwert herunter
(0xb4 → 0x72/0x6a in 15 s, fan3 2736 → 2039 rpm, nicht eingeschwungen); laut
Firmware 0.14 (abgeleitet, M5) auf eine Solldrehzahl ±100 rpm aus einer Tabelle an
EC 0x06 (< 34 °C 1200 rpm, 34–99 °C 1600 rpm). Offen ist, ob und wie das auf die
Plattentemperatur reagiert. Der PCIe-Kanal bleibt nach 0x31 auf dem Schreibwert
(keine EC-Automatik für DCR5); sr1 schreibt bei `pwm4_enable=2` und beim Entladen
deshalb 255 statt 0x31 (0004). Der CPU-Kanal regelt nach 0x21 nachweislich weiter. Ein Regler setzt den
HDD-Kanal beim Stoppen deshalb auf einen festen sicheren Wert.

### n5-fand (archiviert)

Der Bash-Regler `n5-fand` ist seit 15.09.2026 durch
[n5-fangov](https://github.com/SirRenix/n5-fangov) abgelöst und liegt nur noch in
`legacy/`; [`docs/BETRIEB.md`](docs/BETRIEB.md) beschreibt ihn. Entfernen:
`legacy/uninstall.sh` (nimmt auch eine Handinstallation des Treibers mit, nicht das Paket).
