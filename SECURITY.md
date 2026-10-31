# Security policy

This repository ships a kernel module (DKMS package `minisforum-n5-it5571-dkms`) that
writes to the embedded controller of the Minisforum N5 family, plus test tools that run as
root. A wrong write can stop a fan. Reports about anything that lets a write reach the EC
where it should not — on an unsupported board, with `force=1`, in read-only mode, after
unload — or that leaves a fan lower than intended are welcome.

## Reporting a vulnerability

- **Preferred:** GitHub private vulnerability reporting on this repository —
  *Security → Report a vulnerability*. The report stays private until a fix is out.
- Do **not** open a public issue or pull request for a security problem.
- Include: package version (`dpkg -s minisforum-n5-it5571-dkms`), module version
  (`modinfo -F version minisforum_n5_it5571`), DMI product / board / vendor, BIOS and EC
  firmware (`cat /sys/class/dmi/id/ec_firmware_release`), module parameters, steps to
  reproduce, and what happens to the fans.
  Please do not include your real addresses, host names or serial numbers; replace them
  with documentation values (`192.0.2.x`, `n5host`).

This is a one-person project done in my spare time. I read reports as soon as I can and
take confirmed problems seriously, but I cannot promise response or fix times. If a problem
is serious and you do not hear back, run the module read-only
(`options minisforum_n5_it5571 experimental_write=0`, see README *Write access and its
risk*) or unload it: the EC then regulates on its own.
Fixed versions are listed in `CHANGELOG.md`.

## Supported versions

| Version | Supported |
|---|---|
| latest release / pre-release on the releases page | fixes go here |
| anything older | no — update |

## Scope notes

- Upstream `0.2.1` (package `0.2.1-1`) is shipped unchanged; its known issues (writes on
  any board with `force=1` and `experimental_write=1`, among others) are fixed by the
  patch series from `0.2.1+sr1` on (`CHANGELOG.md`). Issues in upstream code that the
  series does not touch: report them upstream at
  [ltdstudio/minisforum-n5-it5571](https://github.com/ltdstudio/minisforum-n5-it5571) too.
- The module needs root to load and its sysfs write nodes are root-only. A report that
  needs root is in scope only if the driver does something its documented gate forbids
  (README *Supported hardware*, patch `0002`).
- `debug_regs=1` is a validation switch and reads Super-I/O ports; it is not meant for
  `modprobe.d`.
- The regulator [n5-fangov](https://github.com/SirRenix/n5-fangov) has its own policy.

## What this repository keeps out

No infrastructure data is accepted in commits: addresses, host or domain names, serial
numbers, MAC addresses, certificates or tokens. Raw data under `befunde/raw/` is published
with the host name replaced by `n5host` and serials removed. `.gitleaks.toml` holds the
rules; run `gitleaks git --config .gitleaks.toml .` before a push.
