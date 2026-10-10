# Test specification — N5 Pro EC fan behaviour

Version **0.3** (2026-10-09). Status: fixed before maintenance window 1; operator-approved.
Results are run against 0.3.

This document fixes **how** a claim about the N5 Pro EC (ITE IT5571) is tested and **what counts
as pass or fail** — before the measurement. Every published result names the spec version it was
run against. Raw data of every run is published unchanged (only hostname and serials redacted).

## 1. Rules

1. Criteria are fixed before a run. If a run shows that a criterion is wrong, the spec gets a new
   version with a changelog entry and a reason. Old results keep their old version; they are
   re-run, not re-interpreted.
2. Verdicts are `PASS`, `FAIL` or `INCONCLUSIVE` (with reason). Only `PASS`/`FAIL` may be quoted
   in docs, code comments or issues. T6 has no verdict; its result is `REPORTED` (values only).
   A test that does not apply to a channel is `N/A` with reason (PCIe: T2–T5, §6).
3. A claim is worded as observed: "on board X, BIOS Y, test T vZ: PASS (n runs)" — never
   "the EC always/never …".
4. Verdicts are computed by the tool from the raw data, not read off by hand. The tool for
   G1–G6, T0–T3, T6 is `n5-fangov validate` (its `SpecVersion` names this version); the tools for
   C1, T6 snapshots with the module unloaded and F8a are the scripts in `tools/f8a/`. T4/T5 have
   no tool yet: `validate` reports them `INCONCLUSIVE` (§5); they are run by hand per §5.5, and a
   result evaluated from such a log says "evaluated by hand".
5. A board/BIOS combination is **validated** when T1, T2, T3 pass on at least two devices (for
   PCIe T1 only: T2/T3 are `N/A`).
6. Where the tool knowingly differs from the spec version it names, every result bundle lists the
   difference in `meta.json` → `spec_deviations` (empty list = none). A non-empty list is quoted
   with the verdict.
7. Results of `validate --dry-run` (simulated EC) and `validate --replay` (logs from before this
   spec: no timestamp of the auto command, one direction only, so never PASS) are not results under
   this spec.

## 2. What is recorded in every run

**Identity** (`meta.json`, T0): DMI `product_name`, `board_name`, `board_version`,
`bios_version`, `bios_date`; EC firmware version = DMI `ec_firmware_release` (SMBIOS; see §7);
kernel; driver version, srcversion and module parameters; tool version; spec version; boot type
(cold/warm, stated by the operator: `--boot`) and uptime.

**Environment** (per sample): EC RAM 0x09 CPU, 0x04 system, 0x05 board, 0x06 ambient; max HDD
temperature (drivetemp); max NVMe temperature; k10temp Tctl; load average.
Reference rig only: room temperature and humidity from a sensor at the air intake (placement
documented with a photo). Room values are context, never a pass criterion; columns stay empty
when not recorded.

**Primary measurand:** the EC duty registers via I2EC `0x1802–0x1809` (DCR1 CPU, DCR2 SSD,
DCR3 and DCR4 HDD, DCR5 PCIe). With the driver loaded they are read through its debugfs file `dcr`
(driver `0.2.1+sr1`, module parameter `debug_regs=1`); with the module unloaded through
`n5_i2ec_read` (`tools/f8a/f8a-snapshot.sh`, mode A). The registers judged for a channel are
DCR1 (pwm1), DCR2 (pwm2), **DCR3 and DCR4** (pwm3) and DCR5 (pwm4). For pwm3 both registers must
meet a criterion; when they disagree, the disagreement is reported and the result is not PASS
(§5.1, §5.2; operator decision 2026-10-09). **Secondary:** tach `fan1–3` (fan1 CPU, fan2 SSD,
fan3 = tach of DCR3; DCR4 has its own tach in the EC, 0x88E0/0x88E1, which the driver does not
read; pwm4/PCIe has no tach). The tach lags the duty by seconds to minutes and is never the sole
basis of a T1–T3 verdict. **T4/T5** are judged on the quantity the EC automatic controls: the tach
within its target band (§6).

**Sampling:** every 5 s, whole run, no gaps.

## 3. Preconditions (gate)

A writing test (T1, T2, T3) does not start unless G1–G5 are met, each measured and logged. G6 is
recorded but does not block (see below). Two refusals come before the run and leave no result
bundle: a running or restarting `n5-fangov` daemon (G1) and, for a writing test, a failed G5
(input not from a local terminal, or the confirmation not typed); the tool then exits with an
error and writes nothing. Every other failed gate is reported in the bundle and the writing tests
are reported `INCONCLUSIVE` ("not run: gate …"); nothing is written. A run without a writing test
(T0, T6, C1) checks G1, G2 and G6; G3, G4 and G5 are then `SKIPPED` (operator decision
2026-10-09: the gates protect writes, a read-only run needs none of them). G3 and G4 are also
`SKIPPED` when the duty registers are not readable: T1–T3 then write nothing anyway (§5.1).

| ID | Condition | Measured by |
|---|---|---|
| G1 | No other pwm writer: `n5-fangov` (also while restarting), `n5-fand`, `fancontrol` inactive; the n5-fangov writer lock is held by the run. Checked again at **every sample**; a writer that becomes active aborts the run | systemd state, writer lock |
| G2 | No scrub/resilver/backup | `zpool status` ("scrub in progress", "resilver in progress"; no `zpool` = met), process list: `vzdump`, `proxmox-backup-client` by process name or program in the command line (`comm` alone is cut to 15 characters), and PVE workers by their title `task UPID:…:vzdump:…` (a scheduled or GUI backup job); `zpool status` failing = not met. Host-specific backup jobs that are neither (e.g. the cold backup to USB) are excluded by the operator in the run card |
| G3 | Load average (1 min) < 1.0 | `/proc/loadavg`, first sample of the gate window; unreadable = not met |
| G4 | Temperatures stable over 5 min (max − min): CPU (k10temp Tctl) < 5 °C (operator decision 2026-10-09); system (EC 0x04) and HDD (drivetemp max) each < 1 °C; **and** every limit sensor of §4 (Tctl, NVMe, HDD) has a value in that window | gate window: 300 s of samples every 5 s without writes; a sensor without any value in the window = not met; an abort during the window = not met |
| G5 | Operator at the device (writes are never run remotely) | interactive confirmation: `VALIDATE START` typed on a terminal. Always refused when standard input is not a terminal (piped input; `--i-am-at-the-console` does not change that). Refused in a remote session (`SSH_*`, or `sshd`/`termproxy`/… among the ancestor processes), and when no local console (VT or serial login) is in the process chain, unless `--i-am-at-the-console` states that the operator sits at the device; the session is recorded |
| G6 | Register snapshot taken before the first write since boot: duty registers **and** EC RAM curve tables (0x10–0x27, 0x30–0x38, 0x70–0x81) | see below |

G3 and G4 apply at the start of a run. Load generated *by* a test (T5) is part of the procedure.

**G6** has two parts. (a) The duty registers at the start of the run: `validate` records them in
its first sample. (b) The curve tables before the first write since boot: they cannot be read
safely while the driver is loaded (EC RAM reads through `ioperm` bypass the driver's lock), so
they come from a mode-A snapshot (`tools/f8a/f8a-snapshot.sh`, module unloaded) taken after boot
and before the module is loaded; the run record names that snapshot. `validate` reports G6
`PARTIAL` when it has (a) — it cannot see (b), nor whether something wrote earlier in this boot —
and `FAIL` when the duty registers are not readable; in that case T1–T3 write nothing anyway
(see §5.1). G6 is the only gate that does not block (operator decision 2026-10-09: part (b) has no
safe read path with the driver loaded; C1 covers the tables, §5.3).

## 4. Safety limits

Abort when, in any sample: CPU Tctl ≥ 85 °C · NVMe (max) ≥ 70 °C · HDD (max) ≥ 50 °C · any tach
(fan1–3) = 0 rpm while the duty of its channel is > 0 (without readable duty registers the duty
counts as > 0) · a limit sensor (Tctl, NVMe, HDD) that had a value in the gate window has no value
in **3 consecutive samples** (its limit can no longer be enforced) · another pwm writer becomes
active (G1).

On abort the tool logs the reason (`events.log`, `meta.json` → `aborted`), puts every channel it
touched into its **restore value**, and reports every test that did not complete `INCONCLUSIVE`
("aborted: …"). The same restore runs at the end of every test, at the end of the run, on a signal
and after a crash of the tool, before any further output.

**Restore value** of a channel: the stop value of the n5-fangov configuration for that channel
(`auto` = the EC automatic command, or a fixed duty; on the reference host CPU/SSD `auto`, HDD
140), listed in `meta.json` → `restore_values`. It is **not necessarily the EC automatic**: a
channel with a fixed stop value is left at that duty, as the daemon's own failsafe leaves it.
This counts as equally safe (operator decision 2026-10-09). A duty below 100 is never written —
not in a test and not when restoring: a configured fixed stop duty below 100 is restored as 100.
PCIe has no EC automatic (§6); its `auto` leaves DCR5 at the last value. A restore whose
read-back register differs from the written value counts as written and is listed in
`restore_warnings`; a restore that fails is listed in `restore_failed` and printed.

## 5. Tests

| ID | Question | Procedure | PASS | FAIL | INCONCLUSIVE |
|---|---|---|---|---|---|
| T0 | Identity | Read-only dump (§2) | all fields present | — | a field missing (named) |
| T1 | Mapping | §5.1: 60 s rest reference, then one channel: 255 → 217 → 179 → 140, 60 s each | DCR equals the written value in every sample of every step (pwm3: DCR3 **and** DCR4); own tach changes ≥ 200 rpm per step in the expected direction; other tachs ±50 rpm. **Channel without tach (PCIe):** DCR equals the written value, all tachs ±50 rpm | DCR (pwm3: DCR3 or DCR4) differs from the written value, or another tach follows (§5.1) | tach noise > 50 rpm at rest; own tach changes < 200 rpm in a step; DCR not readable |
| T2 | Hand-back (leaves written value) | §5.2: auto value A first, then per run the sub-runs H = 255 and M = min(A + 48, 200): write, hold 60 s, send auto, log 1020 s | DCR differs from the written value by **≥ 16 counts** within 60 s in both sub-runs (pwm3: in DCR3 and DCR4) | DCR within ±2 of the written value in every sample of the 1020-s log in both sub-runs (pwm3: in DCR3 and DCR4) | mixed result; DCR3 and DCR4 disagree; a sub-run whose written value lies within 16 counts of A (§5.2). **PCIe: `N/A`** (no EC automatic on DCR5, §6) |
| T3 | Settling | from the T2 data, every sub-run (§5.2) | DCR constant ±2 for 120 s, reached within 15 min (pwm3: DCR3 and DCR4); settle time and final value reported | no steady state within 15 min (pwm3: in DCR3 and DCR4) | log ends before a decision; DCR3 and DCR4 disagree. **PCIe: `N/A`** |
| C1 | Curve tables unchanged | EC RAM 0x10–0x27, 0x30–0x38, 0x70–0x81 before and after a run or a bracket of runs (§5.3) | byte-identical | any difference (reported with offsets; recovery path cold boot/power-off is then measured) | snapshot missing or incomplete; `validate` alone (driver loaded) |
| T4 | Target band follows a falling input | §5.5: channel in EC automatic, the table input (§6) falls across a table step | after the input crossed the step by ≥ 2 °C, the tach settles into the new target band (target ± 100 rpm) within 10 min | the tach stays in the old target band for 10 min after the crossing | the input did not cross a step by ≥ 2 °C; see §5.5. **PCIe: `N/A`** |
| T5 | Target band follows a rising input (load) | §5.5: channel in EC automatic, load raises the table input (§6) across a table step, log until 10 min after the crossing | as T4, for the rising input | as T4 | as T4. **PCIe: `N/A`** |
| T6 | Boot state | Snapshot after cold boot and after warm boot, before any write (§5.4) | — (`REPORTED`, values only) | — | — |

T2/T3 observe the duty register (the written value is left, the duty settles). T4/T5 observe what
the EC automatic controls: the tach in the target band of the table point selected by the input
sensor (§6; operator decision 2026-10-09). `validate` does not implement T4/T5 and reports them
`INCONCLUSIVE`: for pwm3 "HDD input EC 0x06 below 34 C" when EC 0x06 stayed below 34 °C in the run
(the expected case on the reference host, §6), otherwise "not implemented, run manually per
§5.5"; for pwm4 they are `N/A`.
T1 is run once per channel. Each of T2–T5 is run **3×**; the verdict is PASS only if all three
pass, FAIL only if all three fail, otherwise INCONCLUSIVE; fewer than 3 completed runs give
INCONCLUSIVE ("n=k of 3 runs"). Channels are tested one at a time, one channel per `validate`
run.

### 5.1 T1 — procedure and evaluation

Precondition: the duty registers are readable; otherwise T1 (and T2/T3) write nothing and report
`INCONCLUSIVE` ("duty registers not readable").

1. **Rest reference:** 60 s of samples without a write; the channel stays in the state it is in
   (after `systemctl stop n5-fangov`: its stop value, see §4).
2. Manual mode for the channel (the driver sets 255), then 255, 217, 179, 140, each held 60 s.
3. Restore value for the channel (§4).

Evaluation:

- **DCR:** every sample of every step equals the written value exactly; for pwm3 this holds for
  DCR3 and for DCR4, and the samples in which the two differ are listed. A sample without DCR
  leaves this undecided ("DCR not readable"), unless another sample already differs.
- **Noise at rest:** max − min of each tach over the rest reference; > 50 rpm = noisy.
- **Other tachs:** every sample of every step within ±50 rpm of that tach's mean over the rest
  reference.
- **Own tach:** mean of the last 3 samples of each step; the mean drops by ≥ 200 rpm from each
  step to the next (255→217, 217→179, 179→140). A step whose last 3 own-tach samples are all
  unreadable has no mean and counts as "own tach < 200 rpm".
- **Verdict, in this order:** DCR differs → FAIL; another tach outside ±50 rpm and no tach noisy
  → FAIL; DCR not readable → INCONCLUSIVE; a tach noisy → INCONCLUSIVE (also when another tach
  moved: noise makes "follows" undecidable; operator decision 2026-10-09); own tach < 200 rpm in
  a step → INCONCLUSIVE; otherwise PASS.

### 5.2 T2/T3 — procedure and evaluation

Not run for **pwm4**: `N/A` ("no EC automatic on DCR5 (firmware 0.14)", §6); nothing is written.
What DCR5 does after the auto command is recorded by `f8a-pcie.sh` (§5.3, run card F1).

1. **Auto value A:** send the auto command first and log 15 min (900 s), then A = the last DCR
   sample of that window. Reason: after `systemctl stop n5-fangov` the failsafe leaves fixed
   channels at their stop duty (HDD 140), so the register before the auto command is not the
   EC's own value.
2. **Second value:** M = min(A + 48, 200), never below 100.
3. **Per run (3×), sub-runs H then M, back to back:** manual mode (driver sets 255), write the
   value, hold 60 s, send the auto command, log **1020 s** (15 min plus the 120-s T3 window, so a
   steady state that starts at 15 min can still be seen for its full 120 s and "no steady state
   within 15 min" can be decided). Times are seconds after the auto command.
4. After the last run: restore value for the channel (§4).

Per T2 run and channel this takes about 2 h (900 s + 3 × 2 × (60 s + 1020 s)).

**T2, per sub-run** (W = written value):

- PASS: the first sample with |DCR − W| ≥ 16 lies at ≤ 60 s.
- FAIL: every sample of the 1020-s log within ±2 of W.
- INCONCLUSIVE: left W by ≥ 16 only after 60 s; or moved by 3–15 counts without leaving it.
- Overrides the above: |W − A| < 16 → INCONCLUSIVE ("too close to the auto value": leaving W
  cannot be told from returning to A; operator decision 2026-10-09). This hits M for
  185 ≤ A ≤ 215 (M = 200 for A ≥ 152) and H for A ≥ 240.

**pwm3 (DCR3 and DCR4):** A is read per register; M is derived from A of DCR3. Each sub-run is
evaluated per register by the rules above and below (each with its own A). Same verdict in both
registers → that verdict; different verdicts → INCONCLUSIVE ("DCR3/DCR4 disagree", both
verdicts listed). Different final values (T3) are reported, not judged: each register regulates
on its own tach (§6).

A run is PASS when both sub-runs pass, FAIL when both fail, otherwise INCONCLUSIVE; a missing
sub-run (abort) makes the run INCONCLUSIVE.

**T3, per sub-run:** the steady window is the first 120-s window in which every sample lies within
±2 of the window's first sample; the window must lie within the log. Settle time = its start,
final value = its first sample.

- PASS: settle time ≤ 900 s.
- FAIL: settle time > 900 s, or no steady window in the full 1020-s log.
- INCONCLUSIVE: the log ends (abort) before either is decided.

Run and test verdict as for T2.

### 5.3 C1 — procedure

`validate` cannot read the curve tables while the driver is loaded and reports C1
`INCONCLUSIVE`. C1 is decided in the maintenance window with the module **unloaded**:

1. Mode-A snapshot directly before the run (`f8a-snapshot.sh LABEL`: EC RAM 0x00–0xFF twice,
   I2EC 0x1800–0x182F, tachs).
2. The run (with the module loaded as the run needs it).
3. Unload the module, mode-A snapshot directly after.
4. `f8a-snapshot.sh --diff BEFORE AFTER`: line `C1 curve tables …: identical` (exit 0) = PASS,
   `CHANGED at …` (exit 1) = FAIL with the offsets, exit 2 or an `INCOMPLETE` snapshot =
   INCONCLUSIVE.

`f8a-pcie.sh` runs this sequence for PCIe (pwm4 = 167, auto, unload) by itself. When several runs
lie between two snapshots, the result is C1 for that bracket and names the runs it covers; it is
not a per-run result. A bracket is sufficient (operator decision 2026-10-09): the tables are
written by the EC firmware from constants at EC start and are not addressed by any PMC2
subcommand the driver sends (§6), so C1 checks the firmware analysis on the device rather than a
per-write risk.

### 5.4 T6 — procedure

After a cold boot and after a warm boot, before any write: `f8a-snapshot.sh cold|warm` (mode A,
module not yet loaded: EC RAM, I2EC incl. DCR, tachs), then optionally with the module loaded
(`debug_regs=1`) `f8a-snapshot.sh LABEL-loaded` (mode B: hwmon, `dcr`) or `validate --tests T6
--boot cold|warm` (first sample, duty registers, pwm cache). `validate` cannot see whether
something wrote earlier in this boot and says so in the result.

### 5.5 T4/T5 — procedure and evaluation

Run by hand (not implemented in `validate`, §5). Not run for pwm4 (`N/A`, §6).

1. Channel in EC automatic, `n5-fangov` stopped, module loaded with `debug_regs=1`. Logged every
   5 s, without gaps, with the columns of `samples.csv` (§8); the log is the raw data of the result.
2. **T5 (rising):** from an input at least 2 °C below a step boundary S of the channel's table
   (§6), raise the input by load until it is ≥ S + 2 °C; log until 10 min after that crossing.
3. **T4 (falling):** end the load; log until 10 min after the input is ≤ S − 2 °C.
4. Limits (§4) apply throughout. Each of T4/T5 3×, aggregated as in §5.

Evaluation per crossing: t0 = the first sample beyond S by ≥ 2 °C. Old band = target of the table
point before the crossing ± 100 rpm; new band = target of the table point the input lies in,
± 100 rpm (the input may cross several steps; the band follows it). Tach = fan1 (pwm1), fan2
(pwm2), fan3 (pwm3; DCR4's own tach is not readable, DCR4 is reported only).

Adjacent bands can overlap (e.g. CPU 3300 ± 100 and 3400 ± 100 share 3300–3400 rpm; 2300/2500
and 2500/2700 share 2400 and 2600 rpm). A tach sample counts as "in the new band" or "in the old
band" only when it lies in that band and not in the other one; a sample in the overlap breaks the
60-s run for PASS and rules out FAIL, so a decision that rests on such samples is INCONCLUSIVE.

- PASS: the tach lies in the new band for 60 s (12 consecutive samples), reached no later than
  t0 + 600 s.
- FAIL: every tach sample from t0 to t0 + 600 s lies in the old band.
- INCONCLUSIVE: the input did not cross a step by ≥ 2 °C; the input returned across S before
  PASS or FAIL was decided; the tach is neither 60 s in the new band nor always in the old band; tach not
  readable. pwm3 on the reference host: expected INCONCLUSIVE ("HDD input EC 0x06 below 34 C", §6).

The DCR is recorded and its direction reported; it is not judged in T4/T5 (the duty a channel
settles at depends on the fan, §6).

## 5a. Stability (driver, production host)

Driver stability under real conditions; corresponds to DRIVER-REVIEW-CHECKLIST F8a/F8b. Runs
on the production host with the normal kernel (no debug kernel; that is checklist F8c). The
gate of §3 does not apply to F8b (real operation is the condition); F8a requires G5.

| ID | Procedure | PASS | FAIL | INCONCLUSIVE |
|---|---|---|---|---|
| F8a | Maintenance window, operator present: driver load/unload loop 50× with parallel hwmon reads and regulator-style writes; `kill -9` of `n5-fangov`; one cold and one warm boot. After each step: kernel log since the step, fan state (mode, tach, DCR if available). Steps and tools below | no Oops/BUG/WARNING/hung task/soft lockup/EC timeout in the kernel log, fans in the expected state after every step | any kernel-log hit not explained without driver involvement (below), or fans not in the expected state | run aborted for a reason outside the driver, not a kernel-log hit (number of completed iterations reported) |
| F8b | 7 days under real operation, watcher samples continuously and evaluates K1–K7; every hit is mailed immediately, a daily summary is mailed | no criterion fired in a complete 7-day run, or every hit explained without driver involvement | a hit attributable to the driver | sampler gap (K5) or a hit that is neither explained nor attributable; run is repeated |

**F8a procedure** (tools in `tools/f8a/`, usage in its README; writing scripts at the console,
inside `tmux`, with `n5-fangov` inactive):

| Step | Tool | Expected state after the step |
|---|---|---|
| Load/unload loop | `f8a-stress.sh load` — 50× `modprobe -r` / `modprobe … debug_regs=1`, reader loop over all `fan*_input`/`temp*_input`, writer loop pwm1/pwm2 manual 255/200/150/100 through every teardown | per cycle `pwm1..4_enable` = 2, fan1–3 > 0 rpm |
| Unbind/bind loop (in addition) | `f8a-stress.sh unbind` — 50× unbind/bind of the platform device under the same load | as above |
| `kill -9` | by hand: `n5-fangov` running, `kill -9` of the daemon PID (run card: 3×, 2 min apart) | failsafe through `ExecStopPost` (CPU/SSD auto, HDD 140), service restarted |
| Warm boot | by hand: `reboot` with channels manual; then `f8a-snapshot.sh warm` | shutdown path in the previous boot's journal (CPU/SSD auto, HDD/PCIe 255); host and all SATA disks back |
| Cold boot | by hand: `poweroff`, power removed, on; then `f8a-snapshot.sh cold…` | host and all SATA disks back |

**Kernel log** of a step: `journalctl -k` since the step started (for a boot: `-b` of the new
boot and `-b -1` for the shutdown), matched against the pattern list `F8A_KLOG_PATTERN` in
`tools/f8a/f8a-lib.sh` (a superset of the terms in the table; the one-time "module verification
failed" line of an unsigned module is ignored). The scripts apply it themselves; for the steps
by hand the operator runs the same match. Same rule as F8b: **every hit is FAIL**, unless the
operator documents a cause without driver involvement with evidence (annotation in the run
record: the lines, the cause, the evidence). The scripts report a hit attributed to the driver as
`FAIL (… attributed to the driver)` and a hit without driver reference as `FAIL (unexplained
foreign hit - annotate with evidence or rerun …)`; a kernel-log hit is never INCONCLUSIVE
(operator decision 2026-10-09). An annotated foreign hit removes the FAIL; the step stopped at
that hit and counts as aborted for a reason outside the driver (INCONCLUSIVE, rerun) unless all
cycles were completed. An unreadable journal, a limit (§4 values, checked every second by the
scripts' watcher), Ctrl-C/hangup or `n5-fangov` starting during a loop abort the step:
INCONCLUSIVE with the number of completed cycles.

**Watcher criteria F8b** (fixed before the run):

| K | Criterion |
|---|---|
| K1 | Kernel log: Oops, `BUG`, `WARNING`, hung task, soft lockup, RCU stall, watchdog message, any `minisforum` timeout/error line |
| K2 | Reboot (boot ID changed) |
| K3 | `n5-fangov` inactive, restart counter increased, or warnings in its journal |
| K4 | Thermal/fan: a fan with tach at 0 rpm in two consecutive samples; Tctl ≥ 85 °C; NVMe ≥ 70 °C; HDD ≥ 50 °C; EC CPU ≥ 90 °C |
| K5 | Sampler gap > 90 s |
| K6 | Disk: free space of the log target below the watcher's threshold |
| K7 | Kernel taint value changed against the start of the run |

**Runs:** run 1 = baseline with the currently packaged driver (unpatched), from 2026-10-08;
run 2 = 7 days after the patched driver is installed. The result compares both runs per
criterion (count of hits, explained/unexplained).
**Wording:** "no error observed in 7 days under these conditions" (with driver version, kernel,
dates) — never "error-free".
The watcher (sampler, evaluation, mail via the host's `sendmail` path) lives in
`tools/validation-watch/`; its raw data is stored like §8 under `befunde/runs/`.

## 6. EC automatic — control law and table inputs (decoded)

**Source:** disassembly of the EC firmware 0.14 from the BIOS image F8NAA 1.05 and the EC RAM
dumps of 2026-09-14 (`befunde/p1-ec-tabellen-20261008.md` §1–§3, §5; raw data
`befunde/raw/03-probe-20260914-160338.txt`, `befunde/raw/m5-efivars-20261009.txt`).
**Status:** derived from the firmware code, consistent with the measurements of 2026-09-14 (all
tach values in automatic lie in the target band of their table point); not verified on the
device. That the image code is the running code is derived (`ec_firmware_release` = 0.14).

**Control law.** The EC automatic regulates on **rpm**, not on duty: the table point selected by
the input sensor gives a target rpm; the EC steps the duty (DCR) by ±1 until the tach lies within
**target ± 100 rpm**. The duty a channel settles at therefore depends on the fan, not on the
table. Table format per point: `(target_rpm/100, T_upper, T_lower)` in °C (not `(T, D,
D_previous)` as assumed in 0.1/0.2).

| Channel | Table (EC RAM) | Input | Input → target rpm |
|---|---|---|---|
| pwm1 CPU, DCR1, tach fan1 | A 0x10–0x27, 8 points | EC 0x09 (CPU) | < 25 → 0 · 25–45 → 2000 · 45–55 → 2300 · 55–65 → 2500 · 65–75 → 2700 · 75–82 → 3000 · 82–87 → 3300 · > 87 → 3400 |
| pwm2 SSD, DCR2, tach fan2 | B 0x30–0x38, 2 points | EC 0x05 (board) | < 60 → 2200 · 60–99 → 2600 |
| pwm3 HDD, DCR3 (tach fan3) and DCR4 (own tach 0x88E0/E1, not read by the driver) | C 0x70–0x78 (DCR3), D 0x79–0x81 (DCR4), identical | EC 0x06 (ambient) for both | < 34 → 1200 · 34–99 → 1600 · ≥ 100 unchanged |
| pwm4 PCIe, DCR5 | — | — | **no EC automatic**: after the auto command (0x31) the EC no longer writes DCR5; the last value stays |

Step boundaries S (for §5.5): CPU 25, 45, 55, 65, 75, 82, 87 °C; SSD 60 °C; HDD 34 °C.

Further facts the tests rely on (same source and status):

- The tables are written by the EC firmware from constants at EC start (XRAM 0x8310 ff. =
  EC RAM 0x10 ff.). The PMC2 subcommands of the driver and of the BIOS write the host registers
  0x8860–0x8871, 0x887B, 0x887C; none lies in the tables → the OS cannot write the tables (C1,
  §5.3).
- An override register replaces the input when it is ≠ 0: 0x888A (CPU), 0x888B (SSD), 0x88D5
  (DCR3), 0x88E5 (DCR4). Not readable through the ACPI EC window.
- BIOS "Fan 1–4 Setting" with User Mode enabled makes the EC output the BIOS PWM value directly
  as DCR (Fan 3/4 fixed, Fan 1/2 as a 4-point curve on DCR; no rpm control in either case) until
  the auto command of the driver; with User Mode disabled the
  BIOS sends the same hand-back commands as the driver.

**HDD:** the input is the ambient sensor EC 0x06, not a disk temperature. A step needs
EC 0x06 ≥ 34 °C, a crossing by 2 °C ≥ 36 °C. EC 0x06 read 22 °C constant in all logs of
2026-09-14 and moved neither with CPU nor with disk load (measured) → T4/T5 for pwm3 are expected
INCONCLUSIVE on the reference host. **SSD:** EC 0x05 reached 32 °C under HDD read load and 28 °C under CPU
load on 2026-09-14 (measured); the step lies at 60 °C. **PCIe:** T2–T5 `N/A` for pwm4; DCR5 behaviour is recorded
only (`f8a-pcie.sh`, §5.3).

**Open on the device (M1, maintenance window 1):** the start-phase registers 0x28–0x2B, the
override registers above, the BIOS User Mode of Fan 1–4 (candidate explanation of the HDD boot
state 1656 rpm at EC 0x06 = 22 °C, where table C gives 1200).

## 7. Open points of this spec

- EC firmware version: DMI `ec_firmware_release` reads 0.14 on the reference host (SMBIOS); whether
  it is the same counter as the "EC 0.15" string of a F8NAB report is open.
- hwmon `pwmN` returns a cached value and `pwmN_enable` reports 2 after hand-back (driver source
  `feb6d9a`), so hwmon cannot show what the EC does — DCR is the only valid measurand.
- Key of a result: product, board, BIOS **and EC firmware**.
- I2EC reads while the driver is loaded (Super-I/O 0x2e/0x2f is not reserved by the driver).
- Whether DCR4 drives a fan of its own (tach 0x88E0/E1) or reads the same signal as fan3.
- T4/T5 have no tool evaluation yet (§1 rule 4, §5.5).

## 8. Raw data and publication

Per run a directory `befunde/runs/<YYYYMMDD-HHMM>-<board>-<bios>-<test>/` (`validate`:
`<test>` = `validate-pwmN`):

| File | Content |
|---|---|
| `meta.json` | identity, spec version, gate results, timing, `limit_sensors`, `restore_values`, `restore_failed`, `restore_warnings`, session (G5), `aborted`, `spec_deviations` (§1 rule 6) |
| `samples.csv` | `t_iso, t_s, phase, ch, written, dcr1, dcr2, dcr3, dcr4, dcr5, fan1, fan2, fan3, ec_cpu, ec_sys, ec_board, ec_amb, hdd_max, nvme_max, tctl, load1, room_t, room_rh`; unknown values are empty cells |
| `events.log` | every write and auto command with timestamp, gate results, operator confirmation, restores, aborts |
| `verdict.json` | verdict per test and criterion, with the numbers it was computed from |
| `SHA256SUMS` | over all files |

Raw files of the `tools/f8a/` scripts (`<script>-<label>-<UTC>.txt` plus `.sha256`) are stored
under `befunde/raw/` and linked from the run record.
Results are listed in `befunde/RESULTS.md` (board, BIOS, test, spec version, verdict, run link).
Issue comments link the commit, not a summary.

## Changelog

| Version | Date | Change | Reason |
|---|---|---|---|
| 0.1-draft | 2026-10-08 | First version; after review: T1 for channels without tach, T2 minimum distance 16 counts and second value M derived from the auto value, new C1 (curve tables unchanged), T4 reworded (no "fixed default" verdict), T4/T5 only after §6; new §5a Stability: F8a (stress in maintenance window) and F8b (7-day watcher, criteria K1–K7, baseline vs patched driver), operator decision 2026-10-08 | 2026-09-14 run judged hand-back by tach after 20 s (±150 rpm of pre-test value); window too short, wrong measurand, no pre-write snapshot. See upstream issue #7 |
| 0.2 | 2026-10-09 | (1) T1: 60-s rest reference before the steps; DCR checked in every sample; own tach = mean of the last 3 samples per step; order of the verdict rules, own tach < 200 rpm (or unreadable at the end of a step), a sample without DCR and noise with a following tach → INCONCLUSIVE (§5.1). (2) T2: A read after auto + 15 min (failsafe leaves HDD at 140); sub-runs H then M back to back; log 1020 s; FAIL needs ±2 over the whole log; written value within 16 counts of A → sub-run INCONCLUSIVE; left only after 60 s → INCONCLUSIVE; run/test aggregation incl. fewer than 3 runs (§5.2). (3) T3: definition of the steady window, FAIL also when no window in 1020 s, INCONCLUSIVE when the log ends early (§5.2). (4) §3: G6 PARTIAL and non-blocking, curve-table part from a mode-A snapshot; failed gate → writing tests INCONCLUSIVE, nothing written, except the two refusals before the run (running daemon, G5) that leave no bundle; G3/G4/G5 SKIPPED without writing test, G3/G4 also without readable duty registers; G1 checked at every sample incl. writer lock; G2/G3/G4/G5 measurement made explicit (G2: command line and PVE worker title, as `comm` cannot show `proxmox-backup-client` or a scheduled vzdump; G4: CPU = Tctl, all limit sensors present; G5: no local console without `--i-am-at-the-console` refused, piped input always refused). (5) §4: limit sensor unreadable 3 samples → abort; other writer → abort; restore value defined (configured stop value, never below 100), restore warnings/failures recorded. (6) T6 verdict `REPORTED` (§1, §5). (7) `spec_deviations` in `meta.json` (§1 rule 6, §8); dry-run/replay are no results (§1 rule 7). (8) C1 and G6 curve regions extended by 0x30–0x38; C1 procedure with `f8a-snapshot --diff`/`f8a-pcie` (module unloaded), bracket over several runs named as such (§5.3). (9) F8a procedure from `tools/f8a/` incl. unbind loop, kernel-log pattern list, any hit = FAIL (§5a). (10) T6 procedure (§5.4). (11) Identity: EC firmware = DMI `ec_firmware_release`, driver srcversion; DCR source per driver state; DCR4 recorded, not judged (§2). (12) §7 "for 0.2" list removed (integrated) | (1)–(7): the n5-fangov `validate` review (2026-10-09) found interpretations the 0.1 text did not cover (T3 needs 120 s after a start at 15 min; for 185 ≤ A ≤ 215, M = 200 lies within 16 counts of A; G6 has no safe read path with the driver loaded; restoring must not write < 100; a missing limit sensor would silently disable its limit); spec and code now agree, `spec_deviations` is empty. (8)–(10): P1 analysis 2026-10-08 found 0x30–0x38 to be a curve table (contains 0x34); the maintenance-window scripts are the only safe curve-table read path. (11): P1/BEFUNDE: `ec_firmware_release` = 0.14 is the readable source; the tool judges one register per channel |
| 0.3 | 2026-10-09 | (1) G4: Tctl span < 5 °C over the 5-min window; EC system (0x04) and HDD max < 1 °C and all limit sensors present unchanged (§3) — operator decision 2026-10-09. (2) §6 rewritten: EC automatic decoded (rpm control, target ± 100 rpm by stepping the duty; table format `(target_rpm/100, T_upper, T_lower)`; inputs CPU EC 0x09, SSD EC 0x05, HDD EC 0x06 for DCR3 and DCR4; no EC automatic for PCIe; tables not OS-writable; open on the device: 0x28–0x2B, overrides, BIOS User Mode — M1). T2/T3 stay on DCR; T4/T5 redefined on the target band of the tach as a function of the table input, PASS/FAIL/INCONCLUSIVE per §5.5 (new); HDD expected INCONCLUSIVE (input 0x06 needs ≥ 34 °C); PCIe T2–T5 `N/A`, new result `N/A` (§1 rule 2); `validate` reports T4/T5 INCONCLUSIVE with the reasons of §5, pwm4 T2/T3 `N/A`. Two relaxations follow from this and are marked as such: §1 rule 4 — T4/T5 are evaluated by hand from the log and the result says "evaluated by hand" (not computed by the tool); §1 rule 5 — PCIe is **validated** with T1 alone (T2/T3 `N/A`) — operator decision 2026-10-09. Review 2026-10-10, before any run against 0.3: overlapping target bands in §5.5 — a sample in the overlap counts for neither band (no relaxation: such a sample can neither pass nor fail). (3) F8a: every kernel-log hit is FAIL unless explained with evidence (annotation), as F8b; no INCONCLUSIVE for a hit (§5a) — operator decision 2026-10-09. (4) Restore = the configured stop value (≥ 100), not necessarily the EC automatic, equally safe (§4) — operator decision 2026-10-09. (5) pwm3: DCR3 and DCR4 both judged in T1/T2/T3, disagreement reported, never PASS (§2, §5.1, §5.2) — operator decision 2026-10-09. (6) Five relaxations against 0.1 confirmed and marked: G6 non-blocking (`PARTIAL`); G3–G5 `SKIPPED` without writing test; sub-run with \|W − A\| < 16 → INCONCLUSIVE; tach noise / co-moving tach → INCONCLUSIVE; C1 as a bracket over several runs (§3, §5.1, §5.2, §5.3) — operator decision 2026-10-09 | (1) A Tctl span < 1 °C over 5 min is a stricter start condition than the gate needs; the EC-side inputs keep < 1 °C. (2) The EC firmware 0.14 analysis (`p1-ec-tabellen-20261008.md` §1–§5) showed that the automatic controls rpm, not duty, and which sensor feeds each table; criteria on DCR alone cannot show a curve response. (3) F8a and F8b now judge kernel-log hits by the same rule. (4)–(6) accepted by the operator after the 0.2 review; (5) DCR3 and DCR4 regulate separately on their own tachs (§6), judging DCR3 alone would hide a fault of DCR4 |
