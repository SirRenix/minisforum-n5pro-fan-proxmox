# Test specification — N5 Pro EC fan behaviour

Version **0.4** (2026-10-17). Status: operator-approved 2026-10-17; every open point decided as
recommended in the candidate. Applies to every run started after its publication. Results of
maintenance window 1 and earlier keep their version 0.3 (§1 rule 1). §10 lists the changes
adopted for 0.5 after maintenance window 2a (2026-11-01); they are not in force until `n5-ectest`
implements them.
Version 0.3 is kept in the Git history: branch `spec-0.3`, commit `5381b97`
(`git show 5381b97:docs/TESTSPEC.md`).

This document fixes **how** a claim about the N5 Pro EC (ITE IT5571) is tested and **what counts
as pass or fail** — before the measurement. Every published result names the spec version it was
run against. Raw data of every run is published unchanged except for the redacted fields
**[Δ #8]**: hostname, DMI serials/UUID, MAC/IPv4/IPv6/mail addresses, user names, `SSH_*`.
Redacted fields are never inputs to a verdict; the rule set is listed in `meta.json`.

**Applicability [Δ #20, #28]:** every gate and test of this specification is satisfiable on a host
in productive operation (guests running). No procedure requires stopping guests. A condition that
cannot be produced without an intervention in the hardware is listed as "not testable without
intervention" (`N/A` with that reason), not as a precondition (operator principle 2026-10-17).

Text that differs from 0.3 is tagged **[Δ #n]**; the change is listed in the changelog (0.4) and
its reason in the annex "Rationale 0.4" under the same number.

## 1. Rules

1. Criteria are fixed before a run. If a run shows that a criterion is wrong, the spec gets a new
   version with a changelog entry and a reason. Old results keep their old version; they are
   re-run, not re-interpreted.
2. Verdicts are `PASS`, `FAIL` or `INCONCLUSIVE` (with reason). Only `PASS`/`FAIL` may be quoted
   in docs, code comments or issues. T6 has no verdict; its result is `REPORTED` (values only).
   A test that does not apply to a channel is `N/A` with reason (PCIe: T2–T5, §6).
3. A claim is worded as observed: "on board X, BIOS Y, test T vZ: PASS (n runs)" — never
   "the EC always/never …".
4. **[Δ #1, #4]** Verdicts are computed by the tool from the raw data, not read off by hand. The
   tool for G1–G7, T0–T5 and T6 with the module loaded is `n5-ectest` (`map` T1, `handback`
   T2/T3, `snapshot`/`diff`, `report` incl. T4/T5 per §5.5); its `spec_version` names this
   version. C1, T6 before the module is loaded, F1 and F8a: the scripts in `tools/f8a/`.
   `n5-fangov validate` from 0.5.0-rc4 implements this version independently; its bundles are
   cross-checks for §9 (`result_role` "cross-check"), not results. Results of `validate` before
   0.5.0-rc4 are 0.3 results. A hand evaluation of T4/T5 is no
   longer a result. *Conditions before this rule applies: #1 (D4 cross-check, §9) and #4
   (`report` matches a hand evaluation of the first T4/T5 log). Until then `n5-ectest` bundles
   carry `spec_deviations` naming the open condition.*
5. A board/BIOS combination is **validated** when T1, T2, T3 pass on at least two devices (for
   PCIe T1 only: T2/T3 are `N/A`).
6. Where the tool knowingly differs from the spec version it names, every result bundle lists the
   difference in `meta.json` → `spec_deviations` (empty list = none). A non-empty list is quoted
   with the verdict.
7. Results of `validate --dry-run` (simulated EC) and `validate --replay` (logs from before this
   spec: no timestamp of the auto command, one direction only, so never PASS) are not results
   under this spec; the same holds for the corresponding modes of `n5-ectest`.

## 2. What is recorded in every run

**Identity** (`meta.json`, T0): DMI `product_name`, `board_name`, `board_version`,
`bios_version`, `bios_date`, `sys_vendor`; EC firmware version = DMI `ec_firmware_release`
(SMBIOS; see §7); kernel; driver version, srcversion and module parameters; tool version; spec
version; boot type (cold/warm, stated by the operator: `--boot`; it names the last boot of the
host, not the module load) and uptime.

**Environment** (per sample): EC RAM 0x09 CPU, 0x04 system, 0x05 board, 0x06 ambient; max HDD
temperature (drivetemp); max NVMe temperature; k10temp Tctl; load average.
Reference rig only: room temperature and humidity from a sensor at the air intake (placement
documented with a photo). Room values are context, never a pass criterion; columns stay empty
when not recorded.

**Primary measurand:** the EC duty registers via I2EC `0x1802–0x1809` (DCR1 CPU, DCR2 SSD,
DCR3 and DCR4 HDD, DCR5 PCIe). With the driver loaded they are read through its debugfs file `dcr`
(driver `0.2.1+sr1` or later, module parameter `debug_regs=1`); with the module unloaded through
`n5_i2ec_read` (`tools/f8a/f8a-snapshot.sh`, mode A). The registers judged for a channel are
DCR1 (pwm1), DCR2 (pwm2), **DCR3 and DCR4** (pwm3) and DCR5 (pwm4). For pwm3 both registers must
meet a criterion; when they disagree, the disagreement is reported and the result is not PASS
(§5.1, §5.2). **Secondary:** tach `fan1–3` (fan1 CPU, fan2 SSD, fan3 = tach of DCR3; DCR4 has its
own tach in the EC, 0x88E0/0x88E1, which the driver does not read; pwm4/PCIe has no tach). The
tach lags the duty by seconds to minutes and is never the sole basis of a T1–T3 verdict.
**T4/T5** are judged on the quantity the EC automatic controls: the tach within its target band
(§6).

**Sampling [Δ #11]:** every 5 s, whole run, no gaps. Samples are due at anchor + k·5 s (anchor =
start of the phase; for T2/T3 the auto command; for a T1 step the write). A sample read within
±0.5 s of its due time is judged at its due time; its real time is stored. A sample that overruns
skips the missed slots; a skipped slot is a gap.

### 2.1 Settle time [Δ #21]

The EC takes a written duty, and a hand-back, with a delay (measured 2026-10-17, Z1/Z2; Rationale
#21). A DCR value read before the settle time of its channel has passed is **recorded, not
judged**. This applies to every check that compares a DCR against an expected value: T1 (§5.1),
the hold phase of T2 (§5.2), F1 (§5.3), the restore read-back (§4), and any tool that verifies a
write.

| Channel | After a write | After a hand-back |
|---|---|---|
| pwm4 (DCR5) | **1 s** (measured ≤ 0.215 s) | **1 s** (measured ≤ 0.212 s; DCR5 jumps to 0xff) |
| pwm3 (DCR3, DCR4) | **10 s** (measured: the second register after ≤ 4.8 s; ×2, derived) | no fixed time: the EC ramps (below); judged only by the T2/T3 rules |
| pwm1, pwm2 (DCR1, DCR2) | **10 s** (not measured; conservative, same as pwm3) | EC automatic: judged only by the T2/T3 rules |

**HDD hand-back ramp (derived):** after the hand-back the EC moves DCR3 and DCR4 alternately in
bursts (~4 counts per 0.22 s per burst); over the 15-s log of Z2 the mean rate was ~5 counts/s per
register (DCR3 0xb4 → 0x72 in 12.6 s, DCR4 0xb4 → 0x6a in 15.0 s). By linear extrapolation a ramp
from 255 to the boot value 100 takes ~31 s, from 255 to 0 ~52 s (**derived** from one 15-s log;
the final value and the tach lag were not measured). The ramp timeout for a check that needs the
settled value is the T3 limit: steady window (§5.2) reached within 900 s, log 1020 s. Before that
a HDD register is judged only on "left the written value by ≥ 16 counts" (T2).

## 3. Preconditions (gate)

**[Δ #23] Before any gate** the tool checks that the output directory exists and is writable (or
creates it, including parents). If not, it refuses before G1: nothing is written, no bundle.

A writing test (T1, T2, T3) does not start unless G1–G5 and G7 are met, each measured and logged.
G6 is recorded but does not block (see below). **[Δ #10]** Three refusals come before the run and
leave no result bundle: (1) a running or restarting `n5-fangov` daemon (G1); (2) for a writing
test, a failed G5 (input not from a local terminal, or the confirmation not typed); (3) the writer
lock is held by another process, or an earlier run left a channel unrestored (`restore.json` open
or unreadable). The tool then exits with an error, prints the reason and writes nothing.
Every other failed gate is reported in the bundle and the writing tests are reported
`INCONCLUSIVE` ("not run: gate …"); nothing is written. A run without a writing test (T0, T6, C1)
checks G1, G2 and G6; G3, G4, G5 and G7 are then `SKIPPED`. G3 and G4 are also `SKIPPED` when
the duty registers are not readable (T1–T3 then write nothing anyway, §5.1) **[Δ #12]** or when
G7 failed.

| ID | Condition | Measured by |
|---|---|---|
| G1 | No other pwm writer: `n5-fangov` (also while restarting), `n5-fand`, `fancontrol` inactive; the writer lock is held by the run. Checked again at **every sample**; a writer that becomes active aborts the run | **[Δ #5]** systemd state of the known regulators; processes by exact `comm`/`argv[0]`; open fds on `pwm*`; `pwmN`/`pwmN_enable` against the expected value per sample; writer-lock inode per sample |
| G2 | No scrub/resilver/backup | `zpool status` ("scrub in progress", "resilver in progress"; no `zpool` = met), process list: `vzdump`, `proxmox-backup-client` by process name or program in the command line, and PVE workers by their title `task UPID:…:vzdump:…`; `zpool status` failing = not met. Host-specific backup jobs that are neither are excluded by the operator in the run card |
| G3 | Load average (1 min) < 1.0 | `/proc/loadavg`, first sample of the gate window; unreadable = not met |
| G4 | **[Δ #20]** Stable channel inputs over 5 min (max − min of the gate window): EC 0x04 (system), EC 0x06 (ambient; HDD table input) and HDD (drivetemp max) each < 1 °C; **and** every limit sensor of §4 (Tctl, NVMe, HDD) has a value in that window. **Tctl is not a stability criterion**; it is a safety limit (§4) | gate window: 300 s of samples every 5 s without writes; a sensor without any value in the window = not met; an abort during the window = not met |
| G5 | Operator at the device (writes are never run remotely) | interactive confirmation: `VALIDATE START` typed on a terminal. Always refused when standard input is not a terminal. Refused in a remote session (`SSH_*`, or `sshd`/`termproxy`/… among the ancestor processes), and when no local console (VT or serial login) is in the process chain, unless `--i-am-at-the-console` states that the operator sits at the device; the session is recorded |
| G6 | Register snapshot before the first write since boot: duty registers **and** EC RAM curve tables (0x10–0x27, 0x30–0x38, 0x70–0x81) | see below |
| G7 | **[Δ #7]** Board, vendor and driver state allow writes: board in the driver's write table, `sys_vendor` as measured for that board, module not loaded with `force` | DMI, driver parameters; failed → writing tests `INCONCLUSIVE` ("not run: gate G7"), nothing written |

G3 and G4 apply at the start of a run. Load generated *by* a test (T5) is part of the procedure.
Load that changes a table input *during* a run is handled by the crossing rules of §5.1 (T1) and
§5.2 (T2/T3) **[Δ #17, #24]**.

**G6** has two parts. (a) The duty registers at the start of the run: the tool records them in
its first sample. (b) The curve tables before the first write since boot. **[Δ #9]** With driver
`0.2.1+sr2` or later (debugfs `ecram`, `debug_regs=1`) the tool reads (b) itself and reports G6
`PASS`/`FAIL`; without `ecram` (b) comes from a mode-A snapshot (`tools/f8a/f8a-snapshot.sh`,
module unloaded) taken after boot and before the module is loaded, the run record names it, and
G6 is `PARTIAL`. G6 is `FAIL` when the duty registers are not readable (T1–T3 then write nothing).
G6 does not block; it becomes a blocking gate only by a later spec version after one window has
used `ecram`.

## 4. Safety limits

Abort when, in any sample: CPU Tctl ≥ 85 °C · NVMe (max) ≥ 70 °C · HDD (max) ≥ 50 °C · any tach
(fan1–3) = 0 rpm while the duty of its channel is > 0 (without readable duty registers the duty
counts as > 0) · a limit sensor (Tctl, NVMe, HDD) that had a value in the gate window has no value
in **3 consecutive samples** · another pwm writer becomes active (G1).

On abort the tool logs the reason (`events.log`, `meta.json` → `aborted`), puts every channel it
touched into its **restore value**, and reports every test that did not complete `INCONCLUSIVE`
("aborted: …"). The same restore runs at the end of every test, at the end of the run, on a signal
and after a crash of the tool, before any further output.

**[Δ #13]** After an abort on a temperature limit, the channel of that sensor (Tctl → pwm1,
NVMe → pwm2, HDD → pwm3) is set to 255 and stays there; other touched channels get their restore
value. Recorded in `restore_values` with the reason.

**Restore value** of a channel: the stop value of the n5-fangov configuration for that channel
(`auto` = the EC automatic command, or a fixed duty; on the reference host CPU/SSD `auto`, HDD
140), listed in `meta.json` → `restore_values`. **[Δ #2]** Without an n5-fangov configuration:
pwm1–3 the EC automatic, pwm4 255 (no EC automatic, §6). `--restore pwmN=auto|D` (D ≥ 100)
overrides; the value used is in `restore_values`. The restore value is **not necessarily the EC
automatic**: a channel with a fixed stop value is left at that duty, as the regulator's own
failsafe leaves it; this counts as equally safe. A duty below 100 is never written — not in a test
and not when restoring: a configured fixed stop duty below 100 is restored as 100. From driver
`0.2.1+sr1~rc2` the hand-back of pwm4 writes 255 (§6). A restore whose read-back register differs
from the written value **after the settle time (§2.1) [Δ #21]** counts as written and is listed in
`restore_warnings`; a restore that fails is listed in `restore_failed` and printed.

## 5. Tests

| ID | Question | Procedure | PASS | FAIL | INCONCLUSIVE |
|---|---|---|---|---|---|
| T0 | Identity | Read-only dump (§2) | all fields present | — | a field missing (named) |
| T1 | Mapping | §5.1: 60 s rest reference, then one channel: 255 → 217 → 179 → 140, 60 s each | DCR equals the written value in every **judged** sample of every step (§2.1) (pwm3: DCR3 **and** DCR4); own tach changes ≥ 200 rpm per step in the expected direction; other tachs ±50 rpm. **Channel without tach (PCIe):** DCR equals the written value, all tachs ±50 rpm | DCR (pwm3: DCR3 or DCR4) differs from the written value in a judged sample, or another tach follows (§5.1) | tach noise > 50 rpm at rest; own tach changes < 200 rpm in a step; DCR not readable; **[Δ #24]** another channel's table input crossed a step (§5.1) |
| T2 | Hand-back (leaves written value) | §5.2: auto value A first, then per run the sub-runs H = 255 and M = min(A + 48, 200): write, hold 60 s, send auto, log 1020 s | DCR differs from the written value by **≥ 16 counts** within 60 s in both sub-runs (pwm3: in DCR3 and DCR4) | DCR within ±2 of the written value in every sample of the 1020-s log in both sub-runs (pwm3: in DCR3 and DCR4) | mixed result; DCR3 and DCR4 disagree; a sub-run whose written value lies within 16 counts of A; **[Δ #17]** table input crossed a step (§5.2). **PCIe: `N/A`** |
| T3 | Settling | from the T2 data, every sub-run (§5.2) | DCR constant ±2 for 120 s, reached within 15 min (pwm3: DCR3 and DCR4); settle time and final value reported | no steady state within 15 min (pwm3: in DCR3 and DCR4) | log ends before a decision; DCR3 and DCR4 disagree; **[Δ #17]** table input crossed a step. **PCIe: `N/A`** |
| C1 | Curve tables unchanged | EC RAM 0x10–0x27, 0x30–0x38, 0x70–0x81 before and after a run or a bracket of runs (§5.3) | byte-identical | any difference (reported with offsets; recovery path cold boot/power-off is then measured) | snapshot missing or incomplete; tool alone with the driver loaded (§5.3) |
| T4 | Target band follows a falling input | §5.5 | after the input crossed the step by ≥ 2 °C, the tach settles into the new target band (target ± 100 rpm) within 10 min | the tach stays in the old target band for 10 min after the crossing | see §5.5. **PCIe: `N/A`** |
| T5 | Target band follows a rising input (load) | §5.5 | as T4, for the rising input | as T4 | as T4. **PCIe: `N/A`** |
| T6 | Boot state | Snapshot after cold boot and after warm boot, before any write (§5.4) | — (`REPORTED`, values only) | — | — |

T2/T3 observe the duty register (the written value is left, the duty settles). T4/T5 observe what
the EC automatic controls: the tach in the target band of the table point selected by the input
sensor (§6). **[Δ #4]** `n5-ectest report` evaluates T4/T5 per §5.5 from `samples.csv`; pwm3 on
the reference host is expected `INCONCLUSIVE` ("HDD input EC 0x06 below 34 C"), pwm4 `N/A`.
T1 is run once per channel. Each of T2–T5 is run **3×**; the verdict is PASS only if all three
pass, FAIL only if all three fail, otherwise INCONCLUSIVE; fewer than 3 completed runs give
INCONCLUSIVE ("n=k of 3 runs"). Channels are tested one at a time, one channel per tool run; the
three T2/T3 runs of a channel lie in one tool run (one value A).

### 5.1 T1 — procedure and evaluation

Precondition: the duty registers are readable; otherwise T1 (and T2/T3) write nothing and report
`INCONCLUSIVE` ("duty registers not readable").

1. **Rest reference:** 60 s of samples without a write; the channel stays in the state it is in
   (after `systemctl stop n5-fangov`: its stop value, see §4).
2. Manual mode for the channel (the driver sets 255), then 255, 217, 179, 140, each held 60 s.
3. Restore value for the channel (§4).

Evaluation:

- **DCR [Δ #21]:** every **judged** sample of every step — due at or after the write + settle
  time of the channel (§2.1) — equals the written value exactly; samples before that are recorded
  and listed, not judged. For pwm3 this holds for DCR3 and for DCR4, and the samples in which the
  two differ are listed. A judged sample without DCR leaves this undecided ("DCR not readable"),
  unless another judged sample already differs.
- **Noise at rest:** max − min of each tach over the rest reference; > 50 rpm = noisy.
- **Other tachs:** every sample of every step within ±50 rpm of that tach's mean over the rest
  reference.
- **[Δ #24] Table input of another channel:** the table input (§6) of each other channel with an
  EC automatic (CPU EC 0x09, SSD EC 0x05, HDD EC 0x06) is recorded; a crossing = the input lies
  ≥ 2 °C beyond a step boundary S on the other side than in the rest reference.
- **Own tach:** mean of the last 3 samples of each step; the mean drops by ≥ 200 rpm from each
  step to the next (255→217, 217→179, 179→140). A step whose last 3 own-tach samples are all
  unreadable has no mean and counts as "own tach < 200 rpm".
- **Verdict, in this order:** DCR differs in a judged sample → FAIL; another tach outside ±50 rpm
  and no tach noisy **and no table input of another channel crossed a step [Δ #24]** → FAIL;
  another tach outside ±50 rpm while that channel's table input crossed a step → INCONCLUSIVE
  ("table input of pwmN crossed a step") **[Δ #24]**; DCR not readable → INCONCLUSIVE; a tach
  noisy → INCONCLUSIVE; own tach < 200 rpm in a step → INCONCLUSIVE; a table input of another
  channel crossed a step, even with every other tach within ±50 rpm → INCONCLUSIVE ("table input
  of pwmN crossed a step") **[Δ #24, operator decision 2026-10-17]**; otherwise PASS.

Duration per channel: 5 min gate window + 60 s rest + 4 × 60 s ≈ 10 min.

### 5.2 T2/T3 — procedure and evaluation

Not run for **pwm4**: `N/A` ("no EC automatic on DCR5 (firmware 0.14)", §6); nothing is written.
What DCR5 does at a hand-back is checked by F1 (§5.3).

1. **Auto value A:** send the auto command first and log 15 min (900 s), then A = the last DCR
   sample of that window. Reason: after `systemctl stop n5-fangov` the failsafe leaves fixed
   channels at their stop duty (HDD 140), so the register before the auto command is not the
   EC's own value.
2. **Second value:** M = min(A + 48, 200), never below 100.
3. **Per run (3×), sub-runs H then M, back to back:** manual mode (driver sets 255), write the
   value, hold 60 s, send the auto command, log **1020 s** (15 min plus the 120-s T3 window).
   Times are seconds after the auto command. **[Δ #21]** In the hold phase the DCR is recorded;
   samples after the settle time of the channel that differ from W are listed in the bundle
   (`hold_deviations`), they do not change the T2/T3 verdict.
4. After the last run: restore value for the channel (§4).

Per channel this takes about 2 h 10 min (300 s gate + 900 s + 3 × 2 × (60 s + 1020 s)).

**[Δ #17] Table input crossing:** a T2/T3 sub-run is INCONCLUSIVE ("table input crossed a step")
when the channel's table input (§6) crosses a step boundary S by ≥ 2 °C between the write and the
end of the 1020-s log; the same for the 900-s window that determines A (then every sub-run of the
tool run is INCONCLUSIVE). Reason: a load episode moves the target rpm and the duty during the
log; T2/T3 would judge a target change as hand-back or settling. Known on the reference host: a
guest container raises EC 0x09 daily from ~35–38 to ~60–63 °C for about 70 min (crosses CPU steps
45 and 55 °C); the run card schedules pwm1 outside that episode.

**T2, per sub-run** (W = written value):

- PASS: the first sample with |DCR − W| ≥ 16 lies at ≤ 60 s.
- FAIL: every sample of the 1020-s log within ±2 of W.
- INCONCLUSIVE: left W by ≥ 16 only after 60 s; or moved by 3–15 counts without leaving it.
- Overrides the above: |W − A| < 16 → INCONCLUSIVE ("too close to the auto value"). This hits M
  for 185 ≤ A ≤ 215 (M = 200 for A ≥ 152) and H for A ≥ 240.

**pwm3 (DCR3 and DCR4):** A is read per register; M is derived from A of DCR3. Each sub-run is
evaluated per register by the rules above and below (each with its own A). Same verdict in both
registers → that verdict; different verdicts → INCONCLUSIVE ("DCR3/DCR4 disagree", both
verdicts listed). Different final values (T3) are reported, not judged: each register regulates
on its own tach (§6). The EC moves DCR3 and DCR4 alternately (ramp, §2.1); this is expected.

A run is PASS when both sub-runs pass, FAIL when both fail, otherwise INCONCLUSIVE; a missing
sub-run (abort) makes the run INCONCLUSIVE.

**T3, per sub-run:** the steady window is the first 120-s window in which every sample lies within
±2 of the window's first sample; the window must lie within the log. Settle time = its start,
final value = its first sample.

- PASS: settle time ≤ 900 s.
- FAIL: settle time > 900 s, or no steady window in the full 1020-s log.
- INCONCLUSIVE: the log ends (abort) before either is decided.

Run and test verdict as for T2.

### 5.3 C1 and F1 — procedure

C1 is decided with the module **unloaded** (f8a mode A):

1. Mode-A snapshot directly before the run (`f8a-snapshot.sh LABEL`: EC RAM 0x00–0xFF twice,
   I2EC 0x1800–0x182F, tachs).
2. The run (with the module loaded as the run needs it).
3. Unload the module, mode-A snapshot directly after.
4. `f8a-snapshot.sh --diff BEFORE AFTER`: line `C1 curve tables …: identical` (exit 0) = PASS,
   `CHANGED at …` (exit 1) = FAIL with the offsets, exit 2 or an `INCOMPLETE` snapshot =
   INCONCLUSIVE.

When several runs lie between two snapshots, the result is C1 for that bracket and names the runs
it covers; it is not a per-run result. A bracket is sufficient: the tables are written by the EC
firmware from constants at EC start and are not addressed by any PMC2 subcommand the driver sends
(§6). **[Δ #9]** f8a mode A is the only C1 path. The driver's `ecram` (driver loaded) becomes a
second C1 path only after a byte-compare against f8a mode A with the criterion of
`TESTKIT-DESIGN.md` §10, fixed before the compare (§9); until then a C1 from `ecram` alone is
INCONCLUSIVE.

**F1 — PCIe hand-back (`f8a-pcie.sh`) [Δ #19, #21, #22]:** snapshot `pcie-before` (mode A); load
with `debug_regs=1`; pwm4 manual 167 (0xa7), DCR read at once and after 10 s; hand-back
(`pwm4_enable=2`); DCR5 read at c +0 s, **+1 s**, +60 s, +300 s; unload; snapshot `pcie-after`;
diff. From driver `0.2.1+sr1~rc2` the hand-back (`pwm4_enable=2`, unbind, unload, `.shutdown`)
writes 255 and sends no 0x31. **Judged points:** DCR5 = 0xff at c **+1 s**, +60 s and +300 s
(settle time §2.1); the c +0 s reading is recorded, not judged. Any judged point ≠ 0xff → FAIL;
C1 of the bracket as above.

### 5.4 T6 — procedure

After a cold boot and after a warm boot, before any write: `f8a-snapshot.sh cold|warm` (mode A,
module not yet loaded: EC RAM, I2EC incl. DCR, tachs), then optionally with the module loaded
(`debug_regs=1`) `f8a-snapshot.sh LABEL-loaded` (mode B: hwmon, `dcr`) or `n5-ectest snapshot
LABEL --boot cold|warm`. The tool cannot see whether something wrote earlier in this boot and
says so in the result. To keep the module from loading at boot, a `modprobe.d` blacklist is
needed; removing the autoload file is not enough (udev loads the module through its DMI modalias;
measured 2026-10-17) **[Δ #25]**.

### 5.5 T4/T5 — procedure and evaluation

Not run for pwm4 (`N/A`, §6). Evaluated by `n5-ectest report` **[Δ #4]**.

1. Channel in EC automatic, `n5-fangov` stopped, module loaded with `debug_regs=1`. Logged every
   5 s, without gaps, with the columns of `samples.csv` (§8); the log is the raw data of the result.
2. **T5 (rising):** from an input at least 2 °C below a step boundary S of the channel's table
   (§6), raise the input by load until it is ≥ S + 2 °C; log until 10 min after that crossing.
   Load from the host's normal operation counts as load when it is recorded.
3. **T4 (falling):** end the load; log until 10 min after the input is ≤ S − 2 °C.
4. Limits (§4) apply throughout. Each of T4/T5 3×, aggregated as in §5.

Evaluation per crossing: t0 = the first sample beyond S by ≥ 2 °C. Old band = target of the table
point before the crossing ± 100 rpm; new band = target of the table point the input lies in,
± 100 rpm (the input may cross several steps; the band follows it). Tach = fan1 (pwm1), fan2
(pwm2), fan3 (pwm3; DCR4's own tach is not readable, DCR4 is reported only).

Adjacent bands can overlap (e.g. CPU 3300 ± 100 and 3400 ± 100 share 3300–3400 rpm). A tach
sample counts as "in the new band" or "in the old band" only when it lies in that band and not in
the other one; a sample in the overlap breaks the 60-s run for PASS and rules out FAIL, so a
decision that rests on such samples is INCONCLUSIVE.

- PASS: the tach lies in the new band for 60 s (12 consecutive samples), reached no later than
  t0 + 600 s.
- FAIL: every tach sample from t0 to t0 + 600 s lies in the old band.
- INCONCLUSIVE: the input did not cross a step by ≥ 2 °C; the input returned across S before
  PASS or FAIL was decided; the tach is neither 60 s in the new band nor always in the old band;
  tach not readable; **[Δ #4]** a sample interval > 5 s anywhere in the evaluated span; the EC RAM
  tables of the run's snapshot differ from §6 (other board/firmware → other bands). pwm3 on the
  reference host: expected INCONCLUSIVE ("HDD input EC 0x06 below 34 C", §6).

The DCR is recorded and its direction reported; it is not judged in T4/T5 (the duty a channel
settles at depends on the fan, §6).

## 5a. Stability (driver, production host)

Driver stability under real conditions; corresponds to DRIVER-REVIEW-CHECKLIST F8a/F8b. Runs on
the production host with the normal kernel (no debug kernel; that is checklist F8c). The gate of
§3 does not apply to F8b (real operation is the condition); F8a requires G5.

| ID | Procedure | PASS | FAIL | INCONCLUSIVE |
|---|---|---|---|---|
| F8a | Maintenance window, operator present: driver load/unload loop 50× with parallel hwmon reads and regulator-style writes; unbind/bind loop 50×; `kill -9` of the `n5-fangov` main process; one cold and one warm boot; stall detection (below). After each step: kernel log since the step, fan state (mode, tach, DCR if available) | no Oops/BUG/WARNING/hung task/soft lockup/EC timeout in the kernel log, fans in the expected state after every step | any kernel-log hit not explained without driver involvement (below), or fans not in the expected state | run aborted for a reason outside the driver, not a kernel-log hit (number of completed iterations reported) |
| F8b | 7 days under real operation, watcher samples continuously and evaluates K1–K8 **[Δ #14]**; every hit is mailed immediately, a daily summary is mailed | no criterion fired in a complete 7-day run, or every hit explained without driver involvement | a hit attributable to the driver | incomplete run (K5/K8) or a hit that is neither explained nor attributable; run is repeated |

**F8a procedure** (tools in `tools/f8a/`; writing scripts at the console, inside `tmux`, with
`n5-fangov` inactive):

| Step | Tool | Expected state after the step |
|---|---|---|
| Load/unload loop | `f8a-stress.sh load` — 50× `modprobe -r` / `modprobe … debug_regs=1`, reader loop over all `fan*_input`/`temp*_input`, writer loop pwm1/pwm2 manual 255/200/150/100 through every teardown | per cycle `pwm1..4_enable` = 2, fan1–3 > 0 rpm |
| Unbind/bind loop | `f8a-stress.sh unbind` — 50× unbind/bind of the platform device under the same load | as above. **[Δ #25]** Unbind hands back only channels the driver wrote since bind; the loop never writes pwm4, so DCR5 keeps its value from before the loop (after a cold boot 0x00) |
| `kill -9` | by hand: `n5-fangov` running, `kill -9` of the **main process only** (`systemctl kill --kill-whom=main -s KILL n5-fangov`; 3×, 2 min apart) **[Δ #25]**. Killing the whole unit also kills the `ExecStopPost` control process; then the failsafe is missing for a reason of the procedure | failsafe through `ExecStopPost` (CPU/SSD auto, HDD 140) in the journal, service restarted |
| Warm boot | by hand: `reboot` with channels manual; then `f8a-snapshot.sh warm` | host and all SATA disks back; T6-warm recorded |
| Cold boot | by hand: `poweroff`, power removed, on; then `f8a-snapshot.sh cold…` | host and all SATA disks back |
| Stall detection **[Δ #26]** | (a) **hardware stall** (a fan stops, the EC reports 0 rpm, the regulator reacts): **not testable without intervention** on a closed, running host — `N/A` with that reason; testable only when the case is open anyway (opportunistic, operator present, ≤ 60 s per fan, channels with tach only). (b) **alarm chain, simulated sensor:** the regulator against a fake sysfs tree (`N5FANGOV_SYSFS`), `fanN_input` = 0 at a duty ≥ `stall_min_duty` for `stall_cycles` cycles | (a) `N/A` ("not testable without intervention") or, when done: stall alarm for that channel, channel 255, no limit reached. (b) stall detected after `stall_cycles`, fake `pwmN` = 255, alarm delivered through the configured notification path. Reported as "alarm chain, simulated sensor" — **never** as a hardware result |

**Kernel log** of a step: `journalctl -k` since the step started (for a boot: `-b` of the new
boot and `-b -1` for the shutdown), matched against the pattern list `F8A_KLOG_PATTERN` in
`tools/f8a/f8a-lib.sh` (the one-time "module verification failed" line of an unsigned module is
ignored). The scripts apply it themselves; for the steps by hand the operator runs the same match.
**Every hit is FAIL**, unless the operator documents a cause without driver involvement with
evidence (annotation in the run record: the lines, the cause, the evidence). The scripts report a
hit attributed to the driver as `FAIL (… attributed to the driver)` and a hit without driver
reference as `FAIL (unexplained foreign hit - annotate with evidence or rerun …)`; a kernel-log
hit is never INCONCLUSIVE. An annotated foreign hit removes the FAIL; the step stopped at that hit
and counts as aborted for a reason outside the driver (INCONCLUSIVE, rerun) unless all cycles were
completed. An unreadable journal, a limit (§4 values, checked every second by the scripts'
watcher), Ctrl-C/hangup or `n5-fangov` starting during a loop abort the step: INCONCLUSIVE with the
number of completed cycles.

**Watcher criteria F8b [Δ #14]** — normative source: the thresholds of
`tools/validation-watch/n5-validation.conf.example` as shipped with the run; the run record names
the file hash.

| K | Criterion |
|---|---|
| K1 | Kernel log matches `K1_PATTERN` (Oops, `BUG:`, kernel BUG, general protection, Call Trace, blocked task, hung task, soft lockup, hard LOCKUP, RCU stall, watchdog, Hardware Error/MCE, AER/PCIe Bus Error, NVMe timeout/reset, any `minisforum` timeout/fail/error line) and not `K1_IGNORE`. **[Δ #15]** From the next watcher run after run 2: also `WARNING:`. Runs 1 and 2 (without `WARNING:`) get a retro-check (`journalctl -k` for `WARNING:\|cut here` over the run window), recorded in the run-2 record |
| K2 | Reboot (boot ID changed) |
| K3 | `n5-fangov` inactive, restart counter increased, or warnings in its journal |
| K4 | Thermal/fan/EC: a fan with tach 0 rpm in two consecutive samples; Tctl ≥ 85 °C; NVMe ≥ 70 °C; HDD ≥ 50 °C; EC CPU ≥ 90 °C; `ec_read_errors` > 0; EC hwmon missing |
| K5 | Sampler gap > 90 s; clock jump backwards; stale last sample |
| K6 | Disk: data directory > 200 MB or root FS < 5 GB free |
| K7 | Kernel taint value changed against the start of the run |
| K8 | Watcher service failed; journal unreadable; sampler or check coverage < 99 % |

**Complete run:** K5 and K8 without unexplained event (coverage ≥ 99 %, no gap > 90 s).

**Runs [Δ #6, #16, #28]:** run 1 = baseline with the packaged driver (unpatched), from
2026-10-08; run 2 = 7 days with the patched driver, from 2026-10-17. Runs 1 and 2 use
`tools/validation-watch/`; later driver runs use `n5-ectest log` (`samples.csv` per §8 + `aux.csv`,
thresholds identical to the conf file; CI compares). The comparison of run 2 against run 1 is
fixed before run 2:

1. Same watcher thresholds, 30-s sampler, 7 days, same kernel, same n5-fangov version (0.4.2);
   only the driver differs.
2. Comparison table per K: total / annotated / unexplained, run 1 vs run 2. Run 2 is PASS on its
   own (above) **and** no K has more unexplained events than in run 1.
3. B10 (SMM residual risk) is accepted only if `ec_read_errors` = 0 in both runs **and** run 2 has
   no `EC timeout` kernel line. The patched driver logs EC timeouts that 0.2.1 did not print, so
   run 2 can see more than run 1; this is stated with the result.
4. Every difference in conditions is listed with the result. Known: run 2 (2026-10-17 – 10-24)
   contains no monthly scrub (2nd Sunday), run 1 did; this is accepted and named with the result,
   and a later run with the patched driver (e.g. the regulator observation run below) covers a
   2nd Sunday. A weekly backup job (Sunday) lies in both runs; the known guest load episode (§5.2)
   lies in both runs.

A run that observes a **regulator change** with the driver unchanged (e.g. n5-fangov 0.5.0) uses
the same watcher and criteria with its own run ID; it is not a driver run and is not compared in
the driver comparison. Its result is worded as F8b ("no error observed in 7 days under these
conditions"), naming driver and regulator versions.

**Wording:** "no error observed in 7 days under these conditions" (with driver version, kernel,
dates) — never "error-free". Raw data of the watcher is stored like §8 under `befunde/runs/`.

## 6. EC automatic — control law and table inputs (decoded)

**Source:** disassembly of the EC firmware 0.14 from the BIOS image F8NAA 1.05 and the EC RAM
dumps of 2026-09-14 (`befunde/p1-ec-tabellen-20261008.md` §1–§3, §5; raw data
`befunde/raw/03-probe-20260914-160338.txt`, `befunde/raw/m5-efivars-20261009.txt`).
**Status:** derived from the firmware code, consistent with the measurements of 2026-09-14 (all
tach values in automatic lie in the target band of their table point); not verified on the
device. That the image code is the running code is derived (`ec_firmware_release` = 0.14).

**Control law.** The EC automatic regulates on **rpm**, not on duty: the table point selected by
the input sensor gives a target rpm; the EC steps the duty until the tach lies within
**target ± 100 rpm**. The duty a channel settles at therefore depends on the fan, not on the
table. Table format per point: `(target_rpm/100, T_upper, T_lower)` in °C.

| Channel | Table (EC RAM) | Input | Input → target rpm |
|---|---|---|---|
| pwm1 CPU, DCR1, tach fan1 | A 0x10–0x27, 8 points | EC 0x09 (CPU) | < 25 → 0 · 25–45 → 2000 · 45–55 → 2300 · 55–65 → 2500 · 65–75 → 2700 · 75–82 → 3000 · 82–87 → 3300 · > 87 → 3400 |
| pwm2 SSD, DCR2, tach fan2 | B 0x30–0x38, 2 points | EC 0x05 (board) | < 60 → 2200 · 60–99 → 2600 |
| pwm3 HDD, DCR3 (tach fan3) and DCR4 (own tach 0x88E0/E1, not read by the driver) | C 0x70–0x78 (DCR3), D 0x79–0x81 (DCR4), identical | EC 0x06 for both | < 34 → 1200 · 34–99 → 1600 · ≥ 100 unchanged |
| pwm4 PCIe, DCR5 | — | — | **no EC automatic**: after the auto command (0x31) the EC no longer writes DCR5; the last value stays. **[Δ #19]** From driver `0.2.1+sr1~rc2` the driver does not send 0x31 on pwm4; its hand-back writes 255 |

Step boundaries S (for §5.1, §5.2, §5.5): CPU 25, 45, 55, 65, 75, 82, 87 °C; SSD 60 °C; HDD 34 °C.

Further facts the tests rely on (same source and status):

- The tables are written by the EC firmware from constants at EC start. The PMC2 subcommands of
  the driver and of the BIOS write the host registers 0x8860–0x8871, 0x887B, 0x887C; none lies in
  the tables → the OS cannot write the tables (C1). *Measured 2026-10-17 (F1/M2): tables
  byte-identical before and after a PCIe write and hand-back.*
- An override register replaces the input when it is ≠ 0: 0x888A (CPU), 0x888B (SSD), 0x88D5
  (DCR3), 0x88E5 (DCR4). Not readable through the ACPI EC window.
- BIOS "Fan 1–4 Setting" with User Mode enabled makes the EC output the BIOS PWM value directly as
  DCR until the auto command of the driver; with User Mode disabled the BIOS sends the same
  hand-back commands as the driver.
- **[Δ #21] Register latency** (measured 2026-10-17, Z1/Z2): DCR5 takes a write and the hand-back
  within ≤ 0.22 s. DCR3 and DCR4 take a write one after the other (first after ~1.5–1.7 s, second
  after ~4.6–4.8 s). After the HDD hand-back the EC moves DCR3 and DCR4 alternately in steps
  (ramp, §2.1). DCR1/DCR2 latency not measured.
- **Boot state** (measured 2026-10-17, T6, one cold and one warm boot): BIOS/EC set DCR3/DCR4 to
  0x64 at both boots; DCR5 is 0x00 after a cold boot and keeps its last value over a warm boot.

**HDD [Δ #18]:** the input is EC 0x06, not a disk temperature. EC 0x06 (2026-10-08…15, 30-s
samples): 20–26 °C; idle EC 0x06 = room sensor − 3.9 °C (σ 0.4, reference rig); under Tctl ≈ 62 °C
it rises by ~3 K (measured) → 0x06 is not a pure intake sensor. A table step needs EC 0x06 ≥ 34 °C,
a crossing by 2 °C ≥ 36 °C, i.e. room ≈ 38 °C idle / ≈ 36 °C under load (derived). T4/T5 for
pwm3 are expected INCONCLUSIVE on the reference host. **SSD:** EC 0x05 reached 32 °C under HDD
read load and 28 °C under CPU load on 2026-09-14 (measured); the step lies at 60 °C. **PCIe:**
T2–T5 `N/A` for pwm4; DCR5 at a hand-back is checked by F1 (§5.3).

## 7. Open points of this spec

- EC firmware version: DMI `ec_firmware_release` reads 0.14 on the reference host; whether it is
  the same counter as the "EC 0.15" string of a F8NAB report is open.
- hwmon `pwmN` returns a cached value and `pwmN_enable` reports 2 after hand-back, so hwmon cannot
  show what the EC does — DCR is the only valid measurand.
- Key of a result: product, board, BIOS **and EC firmware**.
- I2EC reads while the driver is loaded: 2026-10-17 `dcr` byte-identical to `n5_i2ec_read`
  (module unloaded) and after reload; Super-I/O 0x2e/0x2f free afterwards; whether the region is
  claimed *during* the read was too short to observe. Closed with driver patch 0003 once approved.
- Whether DCR4 drives a fan of its own (tach 0x88E0/E1) or reads the same signal as fan3.
- **[Δ #21]** Write latency of DCR1/DCR2: measured 2026-11-01 (Z3, no verdict) — ≤ 0.22 s after
  a write; after hand-back the EC ramps ≈ 20 steps/s (180 → table value in 4.9 s on pwm1, 5.3 s
  on pwm2). Final value and duration of the HDD hand-back ramp: not measured. Used in §10, not in
  this version.
- **[Δ #20]** Noise of EC 0x09 at 5-s sampling under normal guest load (not measured); needed only
  if a later version wants a CPU-side stability criterion.

## 8. Raw data and publication

Per run a directory `befunde/runs/<YYYYMMDD-HHMM>-<board>-<bios>-<test>/`:

| File | Content |
|---|---|
| `meta.json` | identity, spec version, gate results, timing, `limit_sensors`, `restore_values`, `restore_failed`, `restore_warnings`, session (G5), `aborted`, `spec_deviations` (§1 rule 6), **[Δ #8]** redaction rule set, **[Δ #21]** settle times used |
| `samples.csv` | `t_iso, t_s, phase, ch, written, dcr1, dcr2, dcr3, dcr4, dcr5, fan1, fan2, fan3, ec_cpu, ec_sys, ec_board, ec_amb, hdd_max, nvme_max, tctl, load1, room_t, room_rh`; unknown values are empty cells; **[Δ #11]** `t_s` = due time, real read time in `t_iso` |
| `events.log` | every write and auto command with timestamp, gate results, operator confirmation, restores, aborts |
| `verdict.json` | verdict per test and criterion, with the numbers it was computed from; **[Δ #21]** samples recorded but not judged (settle time) listed per step |
| `SHA256SUMS` | over all files |

Raw files of the `tools/f8a/` scripts (`<script>-<label>-<UTC>.txt` plus `.sha256`) are stored
under `befunde/raw/` and linked from the run record. Results are listed in `befunde/RESULTS.md`
(board, BIOS, test, spec version, verdict, run link). **[Δ #7]** Read-only results (T0, T6, C1,
F8b) of boards outside the driver's write table go into RESULTS.md as well; they never raise the
hardware status above `experimental`. Issue comments link the commit, not a summary.

## 9. Acceptance conditions of 0.4 (before results are quoted)

- **#1 / #27 — D4 cross-check:** `n5-ectest map pwm1` and a second implementation agree on the T1
  verdict, the DCR per step and the tach means (±50 rpm), on the same host and day. Second
  implementation: `n5-fangov validate` from 0.5.0-rc4, which implements 0.4 independently with an
  empty `spec_deviations` list; its bundle is a cross-check, not a result (operator decision
  2026-10-17, replaces the frozen-0.3 variant). Fallback, if no independent 0.4 implementation is
  available: a second reader logs `dcr` and tachs
  independently during the `n5-ectest` T1 run; D4 compares the measurements and `report`
  recomputes the verdict (checks the measurement path, not the evaluation logic twice; named with
  the result).
- **#4 — T4/T5 tool:** `n5-ectest report` reproduces a hand evaluation (per §5.5) of the first
  T4/T5 log. Until then T4/T5 results are not quoted.
- **#9 — `ecram`:** byte-compare against f8a mode A before `ecram` is a C1 path (§5.3).

## 10. Changes adopted for 0.5 (not yet in force)

Operator decision 2026-11-01, after maintenance window 2a (`befunde/BEFUNDE.md` §Wartungsfenster
2a). The changes below fix gaps that window showed. They take effect, as version 0.5, only once
`n5-ectest` implements them; until then 0.4 applies unchanged and every run names 0.4. No run is
required to adopt them, and no 0.4 verdict is re-labelled (§1 rule 1).

| # | Section | 0.4 | 0.5 | Evidence |
|---|---|---|---|---|
| 29 | §3 G4 | max − min < 1 °C over 5 min per input | The EC reports whole degrees, so < 1 °C meant "no change at all". New: mean of the last 60 s − mean of the first 60 s < 1 °C **and** max − min ≤ 1 °C, per input (EC 0x04, EC 0x06, HDD) | 2026-11-01: EC 0x04 alternated 29/30 °C in every gate window without a trend; pwm2/pwm4 four times INCONCLUSIVE |
| 30 | §5.1 T1 | another tach outside ±50 rpm → FAIL ("another tach follows") | FAIL only if the **duty register of that other channel** (DCR) changes during the step. Another tach outside ±50 rpm while its DCR stays constant → `REPORTED` as airflow coupling with the deviation; the T1 verdict is decided by the other criteria | 2026-11-01 pwm3: fan2 +58 rpm at pwm3 = 255, DCR2 = 75 in every sample |
| 31 | §3 G3 | load average (1 min) < 1.0 | load average (1 min) < 0.1 × number of online CPUs (reference host, 24 threads: < 2.4) | absolute 1.0 is tight on a production host with 24 threads; 2026-11-01 met (0.1–0.75) |
| 32 | §2.1 | settle 10 s for pwm1–3, used for writes and hand-back checks alike | write checks pwm1/pwm2: 1 s (PCIe already 1 s); hand-back checks pwm1/pwm2: 12 s (255 → lowest table value at ≈ 20 steps/s, plus margin); pwm3 unchanged (10 s) | Z3 2026-11-01 (§7) |

## Changelog

### 0.4 (2026-10-17, operator decision 2026-10-17)

Candidate 2026-10-17, approved by the operator 2026-10-17 with every open point decided as
recommended. Earlier decisions carry their own date. Direction: **stricter** = fewer results reach
PASS/FAIL or a safer action; **equal** = wording, closes an undefined case or fixes a fact;
**looser** = a criterion or a record is relaxed (always an operator decision). Reason per point:
annex "Rationale 0.4".

| # | Change | Section | Direction | Decision |
|---|---|---|---|---|
| 1 | `n5-ectest` replaces `n5-fangov validate` as the tool; `validate` before 0.5.0-rc4 = 0.3 results, from rc4 an independent 0.4 cross-check | §1 rule 4 | equal | adopted with 0.4; condition D4 (§9); rc4 role operator decision 2026-10-17 |
| 2 | Restore value without n5-fangov configuration: pwm1–3 EC automatic, pwm4 255; `--restore` override | §4 | equal (closes gap) | pwm4 = 255 operator decision 2026-10-16 |
| 3 | Mode-A path natively in the tool | — | — | dropped, operator decision 2026-10-10 (superseded by #9) |
| 4 | T4/T5 evaluated by `n5-ectest report`; hand evaluation no longer a result; two new INCONCLUSIVE reasons | §1 rule 4, §5.5 | stricter | adopted with 0.4; condition `report` (§9) |
| 5 | G1 measured by process, fd, cache value and lock inode per sample | §3 G1 | equal | adopted with 0.4 |
| 6 | F8b tool: `n5-ectest log` from the next driver run after run 2 | §5a | equal | adopted with 0.4 |
| 7 | New gate G7 (board, vendor, driver state); read-only results of other boards in RESULTS.md | §3, §8 | stricter | adopted with 0.4 |
| 8 | Redaction extended to DMI UUID, MAC/IP/mail addresses, user names, `SSH_*`; rule set in `meta.json` | header, §8 | looser (record) | operator decision 2026-10-17 (a) |
| 9 | G6(b) read through driver `ecram`; C1 stays on f8a mode A until a byte-compare | §3 G6, §5.3, §9 | G6 stricter, C1 equal | `ecram` operator decision 2026-10-10; C1 operator decision 2026-10-17 (a) |
| 10 | Third refusal without bundle (foreign writer lock, unrestored channel) | §3 | looser (record) | operator decision 2026-10-17 (a) |
| 11 | Fixed 5-s cadence; sample judged at its due time within ±0.5 s; `t_s` = due time | §2, §8 | looser (timing) | operator decision 2026-10-17 (a) |
| 12 | G3/G4 `SKIPPED` when G7 failed | §3 | looser (record) | operator decision 2026-10-17 (a) |
| 13 | Channel of a tripped temperature limit restored to 255 | §4 | stricter | adopted with 0.4 |
| 14 | F8b criteria K1–K8 as run; conf file normative; "complete run" defined | §5a | stricter | adopted with 0.4 |
| 15 | K1 gets `WARNING:` from the next watcher run after run 2; retro-check of runs 1 and 2 | §5a K1 | stricter | operator decision 2026-10-17 (a) |
| 16 | F8b comparison run 1 ↔ run 2 fixed (conditions, table per K, B10 rule, differences); run 2 without monthly scrub accepted and named | §5a | stricter | (b) n5-fangov 0.4.2 operator decision 2026-10-16; (a) operator decision 2026-10-17 (a1) |
| 17 | Table-input crossing makes a T2/T3 sub-run INCONCLUSIVE | §3, §5.2 | stricter | adopted with 0.4 |
| 18 | EC 0x06 is not a pure ambient sensor (fact corrected) | §6 | equal | adopted with 0.4 |
| 19 | PCIe hand-back writes 255 from driver `0.2.1+sr1~rc2`; F1 checks DCR5 = 0xff | §5.3, §6 | equal (F1 stricter) | operator decision 2026-10-16 |
| 20 | G4 without Tctl span; stability on EC 0x04, EC 0x06, HDD; Tctl only a safety limit; applicability on a productive host | header, §3 G4 | looser (Tctl) | principle operator decision 2026-10-17; variant operator decision 2026-10-17 (a) |
| 21 | Settle time before a DCR check: PCIe 1 s, pwm1–3 10 s; HDD hand-back ramp described | §2.1, §4, §5.1, §5.2, §6, §8 | equal (removes false FAIL) | operator decision 2026-10-17 (a) |
| 22 | F1 judged at c +1/+60/+300 s; +0 s recorded only | §5.3 | looser (one point fewer) | operator decision 2026-10-17 (a); window-1 F1 FAIL stays as annotated (operator decision 2026-10-17) |
| 23 | Output directory checked before the gates | §3 | stricter | adopted with 0.4 |
| 24 | T1: another channel's table input crossing a step → INCONCLUSIVE instead of FAIL, also when every other tach stays within ±50 rpm | §5.1 | stricter | adopted with 0.4; second case operator decision 2026-10-17 |
| 25 | F8a: unbind hands back only touched channels; `kill -9` of the main process only; module blacklist for T6 | §5a, §5.4 | equal | adopted with 0.4 |
| 26 | Stall detection: hardware stall `N/A` "not testable without intervention"; alarm chain with simulated sensor | §5a | looser (hardware part not tested) | operator decision 2026-10-17 (a)+(b); replaces plan decision 2026-10-08 no. 4 |
| 27 | D4 cross-check with `n5-fangov validate` 0.5.0-rc4 as an independent 0.4 implementation (no `spec_deviations`), fallback second reader | §9 | — (procedure) | operator decision 2026-10-17 (a), fallback (b); rc4 variant operator decision 2026-10-17 |
| 28 | Separate observation run for a regulator change | §5a | equal | run operator decision 2026-10-16 |

Sources: `docs/TESTKIT-DESIGN.md` §12 items 1–13; `befunde/BEFUNDE.md` §F8b-Baseline (run
`baseline-20261008`, PASS) and §Wartungsfenster 1 (2026-10-17); `befunde/kennlinie-rack-klima.md`;
raw data `befunde/raw/*20261017*`; the operator's validation plan and the window-1 run record
(both local, not in this repo).

### 0.1-draft to 0.3

Full text of 0.3: `git show 5381b97:docs/TESTSPEC.md` (branch `spec-0.3`).

| Version | Date | Change | Reason |
|---|---|---|---|
| 0.1-draft | 2026-10-08 | First version; after review: T1 for channels without tach, T2 minimum distance 16 counts and second value M derived from the auto value, new C1 (curve tables unchanged), T4 reworded (no "fixed default" verdict), T4/T5 only after §6; new §5a Stability: F8a (stress in maintenance window) and F8b (7-day watcher, criteria K1–K7, baseline vs patched driver), operator decision 2026-10-08 | 2026-09-14 run judged hand-back by tach after 20 s (±150 rpm of pre-test value); window too short, wrong measurand, no pre-write snapshot. See upstream issue #7 |
| 0.2 | 2026-10-09 | (1) T1: 60-s rest reference before the steps; DCR checked in every sample; own tach = mean of the last 3 samples per step; order of the verdict rules, own tach < 200 rpm (or unreadable at the end of a step), a sample without DCR and noise with a following tach → INCONCLUSIVE (§5.1). (2) T2: A read after auto + 15 min (failsafe leaves HDD at 140); sub-runs H then M back to back; log 1020 s; FAIL needs ±2 over the whole log; written value within 16 counts of A → sub-run INCONCLUSIVE; left only after 60 s → INCONCLUSIVE; run/test aggregation incl. fewer than 3 runs (§5.2). (3) T3: definition of the steady window, FAIL also when no window in 1020 s, INCONCLUSIVE when the log ends early (§5.2). (4) §3: G6 PARTIAL and non-blocking, curve-table part from a mode-A snapshot; failed gate → writing tests INCONCLUSIVE, nothing written, except the two refusals before the run (running daemon, G5) that leave no bundle; G3/G4/G5 SKIPPED without writing test, G3/G4 also without readable duty registers; G1 checked at every sample incl. writer lock; G2/G3/G4/G5 measurement made explicit (G2: command line and PVE worker title, as `comm` cannot show `proxmox-backup-client` or a scheduled vzdump; G4: CPU = Tctl, all limit sensors present; G5: no local console without `--i-am-at-the-console` refused, piped input always refused). (5) §4: limit sensor unreadable 3 samples → abort; other writer → abort; restore value defined (configured stop value, never below 100), restore warnings/failures recorded. (6) T6 verdict `REPORTED` (§1, §5). (7) `spec_deviations` in `meta.json` (§1 rule 6, §8); dry-run/replay are no results (§1 rule 7). (8) C1 and G6 curve regions extended by 0x30–0x38; C1 procedure with `f8a-snapshot --diff`/`f8a-pcie` (module unloaded), bracket over several runs named as such (§5.3). (9) F8a procedure from `tools/f8a/` incl. unbind loop, kernel-log pattern list, any hit = FAIL (§5a). (10) T6 procedure (§5.4). (11) Identity: EC firmware = DMI `ec_firmware_release`, driver srcversion; DCR source per driver state; DCR4 recorded, not judged (§2). (12) §7 "for 0.2" list removed (integrated) | (1)–(7): the n5-fangov `validate` review (2026-10-09) found interpretations the 0.1 text did not cover (T3 needs 120 s after a start at 15 min; for 185 ≤ A ≤ 215, M = 200 lies within 16 counts of A; G6 has no safe read path with the driver loaded; restoring must not write < 100; a missing limit sensor would silently disable its limit); spec and code now agree, `spec_deviations` is empty. (8)–(10): P1 analysis 2026-10-08 found 0x30–0x38 to be a curve table (contains 0x34); the maintenance-window scripts are the only safe curve-table read path. (11): P1/BEFUNDE: `ec_firmware_release` = 0.14 is the readable source; the tool judges one register per channel |
| 0.3 | 2026-10-09 | (1) G4: Tctl span < 5 °C over the 5-min window; EC system (0x04) and HDD max < 1 °C and all limit sensors present unchanged (§3) — operator decision 2026-10-09. (2) §6 rewritten: EC automatic decoded (rpm control, target ± 100 rpm by stepping the duty; table format `(target_rpm/100, T_upper, T_lower)`; inputs CPU EC 0x09, SSD EC 0x05, HDD EC 0x06 for DCR3 and DCR4; no EC automatic for PCIe; tables not OS-writable; open on the device: 0x28–0x2B, overrides, BIOS User Mode — M1). T2/T3 stay on DCR; T4/T5 redefined on the target band of the tach as a function of the table input, PASS/FAIL/INCONCLUSIVE per §5.5 (new); HDD expected INCONCLUSIVE (input 0x06 needs ≥ 34 °C); PCIe T2–T5 `N/A`, new result `N/A` (§1 rule 2); `validate` reports T4/T5 INCONCLUSIVE with the reasons of §5, pwm4 T2/T3 `N/A`. Two relaxations follow from this and are marked as such: §1 rule 4 — T4/T5 are evaluated by hand from the log and the result says "evaluated by hand" (not computed by the tool); §1 rule 5 — PCIe is **validated** with T1 alone (T2/T3 `N/A`) — operator decision 2026-10-09. Review 2026-10-10, before any run against 0.3: overlapping target bands in §5.5 — a sample in the overlap counts for neither band (no relaxation: such a sample can neither pass nor fail). (3) F8a: every kernel-log hit is FAIL unless explained with evidence (annotation), as F8b; no INCONCLUSIVE for a hit (§5a) — operator decision 2026-10-09. (4) Restore = the configured stop value (≥ 100), not necessarily the EC automatic, equally safe (§4) — operator decision 2026-10-09. (5) pwm3: DCR3 and DCR4 both judged in T1/T2/T3, disagreement reported, never PASS (§2, §5.1, §5.2) — operator decision 2026-10-09. (6) Five relaxations against 0.1 confirmed and marked: G6 non-blocking (`PARTIAL`); G3–G5 `SKIPPED` without writing test; sub-run with \|W − A\| < 16 → INCONCLUSIVE; tach noise / co-moving tach → INCONCLUSIVE; C1 as a bracket over several runs (§3, §5.1, §5.2, §5.3) — operator decision 2026-10-09 | (1) A Tctl span < 1 °C over 5 min is a stricter start condition than the gate needs; the EC-side inputs keep < 1 °C. (2) The EC firmware 0.14 analysis (`p1-ec-tabellen-20261008.md` §1–§5) showed that the automatic controls rpm, not duty, and which sensor feeds each table; criteria on DCR alone cannot show a curve response. (3) F8a and F8b now judge kernel-log hits by the same rule. (4)–(6) accepted by the operator after the 0.2 review; (5) DCR3 and DCR4 regulate separately on their own tachs (§6), judging DCR3 alone would hide a fault of DCR4 |

## Annex: Rationale 0.4

Per point: what 0.3 said, why 0.4 differs, evidence. Window-1 evidence is in
`befunde/BEFUNDE.md` §Wartungsfenster 1 and `befunde/raw/*20261017*`; the window-1 run record is
local to the operator (not in this repo) and cited by step. Options not chosen are named where
they were weighed.

**#1 Tool names.** 0.3: "the tool for G1–G6, T0–T3, T6 is `n5-fangov validate`". One
implementation (testkit decision 2); `n5-ectest` bundles carried
`spec_deviations: ["tool n5-ectest in place of n5-fangov validate"]` under 0.3. Window 1 step D4
did not run (D1 stopped at G4) → condition in §9.

**#2 Restore value without n5-fangov.** 0.3 defined the restore value only through the n5-fangov
configuration; users of the testkit without n5-fangov had none. pwm4 = 255 follows the operator
decision 2026-10-16. On the reference host nothing changes.

**#3 Mode-A path.** Mode A in `n5-ectest` is not built (decision 2026-10-10; #9 instead).
`f8a-snapshot.sh` stays the module-unloaded path.

**#4 T4/T5 by tool.** Removes the 0.3 relaxation of rule 4 ("evaluated by hand"). Acceptance: §9.

**#5 G1 measurement.** 0.3: "systemd state, writer lock". Same criterion, more detection.

**#6 F8b tool.** Run 2 uses the watcher of run 1 (comparability, #16); later runs `n5-ectest log`.

**#7 G7.** 0.3: a board outside the write table showed up only as a failed write; RESULTS.md was
silent on read-only runs. A named gate replaces an implicit failure.

**#8 Redaction.** 0.3: only hostname and serials. Kernel-log lines in bundles carry MACs; users
of the testkit post bundles publicly. Looser (raw data less complete); redacted fields are never
verdict inputs. Not chosen: (b) 0.3 wording.

**#9 `ecram`.** With `ecram` the driver under test reads its own before/after state — weaker
independence for C1. G6(b) becomes measurable (stricter). C1 via `ecram` only after a byte-compare
against f8a (chosen (a)); not chosen: (b) `ecram` as C1 path from approval.

**#10 Third refusal.** 0.3: two refusals without bundle. A bundle would require starting a run that
cannot be made safe (foreign lock, unrestored channel). Looser: no record of the refused attempt;
the refusal is printed.

**#11 5-s cadence.** 0.3 judged the 12th sample at 60.00x s as "after 60 s" → INCONCLUSIVE from
timer jitter alone. Tolerance ±0.5 s. Not chosen: (b) real time only.

**#12 G3/G4 after G7 failed.** No write is possible; the 5-min window is spent for nothing.
Looser: less recorded.

**#13 Restore after a temperature abort.** 0.3 restored the configured value also after a
temperature abort (HDD 140 after HDD ≥ 50 °C = the duty that just ran). The restore must not
repeat the condition that tripped the limit; 255 rather than EC automatic because HDD automatic
targets 1200 rpm at EC 0x06 < 34 °C (§6).

**#14 F8b criteria.** 0.3 listed K1–K7 with fewer terms; "complete run" was undefined. The
baseline was judged on K1–K8 of the shipped conf file; no watcher change.

**#15 `WARNING:` in K1.** `K1_PATTERN` contains no `WARNING:`; a `WARN()` splat is caught only
through its `Call Trace` line (derived); `F8A_KLOG_PATTERN` does contain it. Measured 2026-10-16
(read-only `journalctl -k`, run-1 window 2026-10-08 22:25:44 – 2026-10-15 22:30:30): 84 kernel
lines, 0 matching `WARNING|cut here`; the run-1 verdict stands. Run 2 started 2026-10-17 with the
unchanged watcher, which keeps runs 1 and 2 comparable. Not chosen: (b) never.

**#16 Run comparison.** 0.3 said only "compares both runs per criterion". (b) n5-fangov 0.4.2 in
run 2: decided 2026-10-16, executed 2026-10-17 (window 1 block G). (a) run 2 (2026-10-17 – 10-24)
does not cover the 2nd-Sunday scrub (next 2026-11-08); run 1 contained one (2026-10-11, largest
pool ~13 h). Chosen (a1): scrub is load, not a driver path; a later patched-driver run covers it.
Not chosen: (a2) repeat run 2.

**#17 Load episode.** Measured (kennlinie, RRD): a guest container causes a daily load episode
~08:55 for 66–76 min, Tctl ≈ 62 °C; from the baseline samples (2026-10-16) EC 0x09 rises from
35–38 to 60–63 °C within ten minutes, load1 up to 2.7 — crosses CPU steps 45 and 55 °C. 0.3 had
no rule for a changing table input during T2/T3.

**#18 EC 0x06.** 0.3: "read 22 °C constant … moved neither with CPU nor with disk load". Corrected
from the kennlinie data; no criterion changed.

**#19 PCIe hand-back 255.** Evidence 2026-10-17: F1 DCR5 0xff at c +60/+300 s; Z1 DCR5 → 0xff
≤ 0.212 s after the hand-back (`befunde/raw/z1-dcr5-latenz-20261017T130459Z.txt`); T6: DCR5
survives a warm boot → 255 at hand-back is the safe value.

**#20 G4 on a productive host.** Measured 2026-10-17 (window 1, D1 pwm1): G4 FAIL with Tctl span
13.5 °C while EC 0x04 and HDD moved 0.0 °C; load1 0.33. Tctl jumped 35–43 °C second by second from
short guest load (container health checks every 10–30 s); T1 → INCONCLUSIVE, nothing written.
Tctl feeds no EC table (CPU input is EC 0x09, §6); the 0.3 criterion measured sensor noise, not a
condition of the channel under test. A CPU load change during the run is caught by #17/#24 on the
real table input. EC values and drivetemp have 1-°C resolution, so "< 1 °C" means "no change in
5 min"; it held in window 1. Not chosen: (b) 60-s mean of Tctl < 5 °C (not measured whether
satisfiable); (c) 0.3 (not satisfiable, measured).

**#21 Settle time.** Measured 2026-10-17: Z1 (DCR5 every 0.2 s,
`befunde/raw/z1-dcr5-latenz-20261017T130459Z.txt`) write ≤ 215 ms, hand-back ≤ 212 ms, then
stable 60 s. Z2 (DCR3/DCR4 every 0.2 s, `befunde/raw/z2-dcr34-hdd-20261017T133518Z.txt`): manual
255 → DCR3 ~1.5 s, DCR4 ~4.6 s; write 180 → DCR4 ~1.7 s, DCR3 ~4.8 s; after the hand-back both
move alternately, not settled after 15 s. Seen in window 1: register deviation in the first
regulator cycle (BEFUNDE C6/E3), F1 FAIL at c +0 s. 10 s = largest measured latency × 2, rounded
to whole samples; pwm1/pwm2 unmeasured → conservative value. Cost in T1: 11 of 12 samples per step (samples at 5–60 s, judged at or after 10 s, §5.1)
judged. Not chosen: (b) pwm1/pwm2 5 s (unmeasured guess); (c) none.

**#22 F1 without +0 s.** Measured 2026-10-17 (`befunde/raw/f8a-pcie-m2-20261017T120056Z.txt`): c
+0 s 0xa7, +60 s and +300 s 0xff → FAIL; Z1 shows the driver writes 255 and the EC takes it within
≤ 0.2 s. The +0 s read precedes the takeover. The window-1 result stays FAIL, annotated as a
procedure artefact, not counted as PASS. F1 is repeated in window 2 with f8a implementing it.

**#23 Output directory.** Observed 2026-10-17 (D1): `n5-fangov validate` creates the bundle only
after the run with a single-level `mkdir`; the `--out` directory did not exist → bundle lost.

**#24 T1 crossing of another channel.** Derived 2026-10-17: without Tctl in G4 (#20), a guest load
episode during the T1 of pwm2–pwm4 can move the CPU table input across a step; fan1 then changes
by 200–300 rpm (§6) and 0.3 would report FAIL for a cause outside the channel under test. A
following tach without crossing stays FAIL.

**#25 F8a corrections.** Unbind: DCR5 stayed 0x00 (E2,
`befunde/raw/f8a-stress-unbind-20261017T114401Z.txt`) — unbind hands back only touched channels.
`kill -9`: `systemctl kill` without `--kill-whom=main` also killed the `ExecStopPost` control
process, the failsafe was missing (C8, artefact); main process only: failsafe after 56–66 ms (E3).
Autoload: the module loaded at boot despite the removed autoload file (udev, DMI modalias); a
`modprobe.d` blacklist works (F3).

**#26 Stall detection.** Plan decision 2026-10-08 no. 4 (unplug a fan) was not in 0.3; window 1
step E4: connectors not reachable without opening the case → `N/A` (decided 2026-10-17). Rejected:
a low duty to stop a fan (violates "never below 100", §4); raising the regulator's stall threshold
(it detects only 0 rpm; `stall_min_duty` is a duty threshold). (b) tests detection → reaction →
notification of the regulator; it does not show that the EC reports 0 rpm for a stopped fan, nor
which HDD fan carries fan3. Not chosen: (a) alone; (c) unplug with the case opened.

**#27 D4 cross-check.** `validate` is frozen at 0.3; its G4 is not satisfiable on the reference
host (#20) and its T1 has no settle time (#21). A D4 under 0.3 can therefore not run, or can
disagree for a known reason. Chosen (a) with fallback (b). Not chosen: (c) run 0.3 until G4 passes
by chance; (d) drop D4. Same day: `validate` 0.5.0-rc4 implements 0.4 in full (empty
`spec_deviations`), so D4 compares two independent 0.4 implementations — a stronger cross-check
than the frozen-0.3 variant; criteria unchanged (operator decision 2026-10-17).

**#28 Regulator observation run.** Decided 2026-10-16: n5-fangov 0.5.0 gets its own run after
run 2. 0.3 knew only driver runs.

**Not changed:** §8 stores F8b raw data under `befunde/runs/`; both F8b runs are there since
2026-10-31 (editorial, not a spec change).
