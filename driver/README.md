# Driver source

The kernel module `minisforum_n5_it5571` is not developed here. It is upstream
[`ltdstudio/minisforum-n5-it5571`](https://github.com/ltdstudio/minisforum-n5-it5571)
(GPL-2.0-only), kept as an unchanged copy plus a series of patches on top.

```
driver/upstream/         byte-identical copy of the upstream commit (paths as upstream)
  ORIGIN                 repo, tag, commit, fetch date
  SHA256SUMS             sha256 of every file in upstream/ except ORIGIN and itself
driver/patches/series    patches in apply order ("#" and blank lines ignored)
driver/patches/*.patch   one change per patch, DEP-3 header
```

`series` lists `0001`–`0008` (version `0.2.1+sr2`: `sr1` plus `0008`, a read-only
DMI entry for `N5 PRO` / `F8NAB`). `sr1` had `0001`–`0007` (code of `~rc3`; `rc2` changed
`0004`/`0006`: PCIe is handed back at 255 instead of 0x31 on `pwm4_enable=2` and on
unbind/unload, and stays touched for `.shutdown`; `rc3` corrects the HDD statement in
the `0006` comments, code unchanged); the released package `0.2.1-1`
shipped upstream `v0.2.1` (`feb6d9a`) unchanged.

## How it is built and checked

- `packaging/build-deb.sh` verifies `upstream/` against `SHA256SUMS` and checks that the
  file set equals both the `SHA256SUMS` list and the `files:` list in `ORIGIN`. It copies
  the tree, applies `series` in order with
  `patch -p1 --forward --no-backup-if-mismatch --fuzz=0` (a reject or needed fuzz fails
  the build) and packages `driver-prototype/minisforum_n5_it5571.c` and `Makefile`. The
  DKMS source dir gets `UPSTREAM_COMMIT` and `PATCHES` (the applied patch list).
- The build fails if `driver/patches/` holds a file that `series` does not list (or the
  other way round), if `series` lists a name twice or a name with characters outside
  `A-Za-z0-9._+-`, and if the series adds, removes or
  changes any file other than those two (it would not reach the package).
- CI (`.github/workflows/package.yml`, job `upstream-check`) fetches `UPSTREAM_TAG` from
  the upstream repository, fails unless it resolves to `UPSTREAM_COMMIT`, and compares
  the upstream file set (`IMPORT` in the workflow: the `.c`, `Makefile`, `LICENSE`,
  `LICENSES/`) with `upstream/`, `SHA256SUMS` and `ORIGIN`, then every file byte by byte
  and every blob id in `ORIGIN`. A dropped license file fails there.
- `packaging/test/test-deb.sh` checks, while `series` is empty, that the installed
  `/usr/src/minisforum-n5-it5571-<ver>/` sources equal `upstream/`.
- Versions: `packaging/UPSTREAM` (table there). A non-empty `series` requires
  `FORK_VERSION` (`sr1`, ...), and the series must set `MODULE_VERSION` to the DKMS
  `PACKAGE_VERSION`; the build checks both.

## Adding a patch

1. Work in a scratch copy: `cp -a driver/upstream /tmp/a && cp -a driver/upstream /tmp/b`,
   apply the existing series to both (`for p in $(grep -v '^#' driver/patches/series); do patch -d /tmp/a -p1 < driver/patches/$p; done`, same for `/tmp/b`), edit `/tmp/b`.
2. `cd /tmp && diff -urN a b > NNNN-short-name.patch` (paths `a/driver-prototype/...`,
   `b/driver-prototype/...`, so `-p1` applies). Number in apply order: `0001-`, `0002-`, ...
3. Put the DEP-3 header (below) above the diff, copy the file to `driver/patches/`,
   append its name to `series`.
4. A patch that changes a file for the first time also adds the dated change notice
   to that file's header (GPL-2.0 section 2(a), below).
5. Set `FORK_VERSION`/`FORK_RC` in `packaging/UPSTREAM`, run `packaging/build-deb.sh`
   and `packaging/test/test-deb.sh` (Debian 13 container).

Never edit `driver/upstream/` to change the driver; the build refuses a copy that does
not match `SHA256SUMS`.

### DEP-3 header

[DEP-3](https://dep-team.pages.debian.net/deps/dep3/) fields, one patch per change:

```
Description: <one line summary>
 <longer explanation: what was wrong, what the patch changes, how it was tested;
 finding ID from docs/DRIVER-REVIEW-CHECKLIST.md / befunde if there is one>
Author: SirRenix <7069603+SirRenix@users.noreply.github.com>
Origin: vendor
Forwarded: not-needed
Last-Update: YYYY-MM-DD
---
```

`Forwarded: not-needed` for board- or Debian-specific changes, `Forwarded: no` for general
fixes that upstream could use. No pull requests are opened upstream; a general fix gets a
note in the upstream issue at most.

### GPL-2.0 section 2(a)

Modified files must carry a prominent notice that they were changed, and when. The
first patch that touches a file adds a line to its header comment, for example

```
 * Modified 2026-10-xx by SirRenix for the Debian/Proxmox DKMS package
 * minisforum-n5-it5571-dkms; changes: driver/patches/ in
 * https://github.com/SirRenix/minisforum-n5pro-fan-proxmox
```

and later patches update the date. The SPDX line and upstream copyright stay as they are.

## Importing a new upstream base

1. Pick the upstream tag and its commit (`git ls-remote <repo> 'refs/tags/<tag>^{}'`).
2. Replace the files in `driver/upstream/` with that commit's files, byte-exact (GitHub
   contents API or `git archive`; no line-ending conversion; `.gitattributes` marks
   the folder `-text`). Same file set unless upstream moved or added a needed file;
   then also adjust `IMPORT` in `.github/workflows/package.yml`.
3. Regenerate `SHA256SUMS` (`cd driver/upstream && sha256sum <files> > SHA256SUMS`) and
   update `ORIGIN` (tag, commit, date, blob ids).
4. `packaging/UPSTREAM`: `UPSTREAM_TAG`, `UPSTREAM_COMMIT`, `PKG_REV=1`, `FORK_VERSION=sr1`
   with patches (empty without), `FORK_RC` as needed; `PACKAGE_VERSION` in
   `deploy/dkms.conf` to the new upstream version.
5. Re-apply each patch (`patch --fuzz=0` must succeed); refresh or drop patches that
   upstream made obsolete, update their `Last-Update`.
6. Build, `test-deb.sh`, and compare `modinfo srcversion` with the previous build only
   where no source change is expected.

## Delta review

Every change to the shipped source (new patch, changed patch, new upstream base) is
reviewed against the last reviewed state, not from scratch and not skipped. Checklist:
[`docs/DRIVER-REVIEW-CHECKLIST.md`](../docs/DRIVER-REVIEW-CHECKLIST.md); last full review:
[`befunde/p3-review-20261009.md`](../befunde/p3-review-20261009.md) (upstream `feb6d9a`,
empty series, `.c` sha256
`e8676ed3820c3db72b6f9821f0a0af12245eb6290380817d390952e67cb25a74`).

1. Baseline: the reviewed state is the patched source of the last review report
   (upstream commit, `series`, sha256 of the patched `minisforum_n5_it5571.c` and
   `Makefile`). Rebuild it as in "Adding a patch" step 1 (`/tmp/a`).
2. Diff: build the new patched tree (`/tmp/b`) and run
   `diff -urN /tmp/a /tmp/b > delta.diff`. Record its sha256 and the new source sha256.
3. Map every hunk to checklist items by what it touches: DMI table, parameters, probe,
   write gate → A; port access, handshake, timeouts → B; mutex, remove, unbind,
   teardown → C; hwmon ops, attributes, visibility, errno → D; error paths, restore,
   shutdown, PM → E; Makefile, build flags, `MODULE_VERSION` → F; anything reachable
   from userspace (sysfs, debugfs, parameters, new ports) → G; comments, header,
   licence, docs → H. A hunk that fits nothing is mapped to every group it could
   affect. Patch 0003 (Super-I/O access, debugfs) reopens B6 and G5.
4. Re-run every mapped item with the checklist's method and evidence rules, plus
   F1–F8c always (F8c stays "open until a second device exists").
5. Critical and high items need first reviewer and second reader, as in the full
   review; STRITTIG, medium FAIL and N/A go to the operator for a decision.
6. Write `befunde/delta-review-<version>-<YYYYMMDD>.md`: baseline and new sha256,
   `delta.diff` sha256, hunk-to-item map, result per item; unmapped items carry over
   from the baseline report with a reference.
7. Release gate: no tag and no release asset for a source change without a delta-review
   report whose result meets the checklist exit criterion (§6). The report then
   becomes the baseline for the next delta.
