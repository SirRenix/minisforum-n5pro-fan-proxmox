# Ergebnisse — Hardware-Status je Gerät

Einzige Quelle für den Hardware-Status von Board, BIOS und EC-Firmware (Plan
`N5Pro/plan-treiber-validierung-release.md` E7/E10). n5-fangov bettet die Tabelle beim Build
ein (`n5-fangov/tools/gen-hwstatus.sh` → `internal/hwstatus/results.tsv`); nach jeder
Änderung hier den Generator laufen lassen. Das Treiber-Flag `validated` und die
Schreib-Allowlist (Patch 0002) sind Schreib-Gates, keine Statusaussage.

## Status

| Status | Bedeutung |
|---|---|
| `validated` | TESTSPEC T1–T3 PASS auf ≥ 2 Geräten |
| `tested` | auf 1 Gerät unter einer Spec-Version: T1 PASS auf jedem Kanal und F8b PASS, Bündel veröffentlicht |
| `experimental` | lädt, `tested` nicht erreicht; Schreiben nur, wenn in der Allowlist des Treibers |
| `unknown` | nicht in der Tabelle; nur read-only |

Schlüssel: DMI `product_name`, `board_name`, `bios_version` und EC-Firmware
(`/sys/class/dmi/id/ec_firmware_release`). `*` = jede Version. Eine Zeile mit konkreter
Version geht einer `*`-Zeile vor. Ein Gerät, auf das keine Zeile passt, ist `unknown`.

Herkunft `eigen` = auf dem Referenzgerät gemessen; `upstream` = Angabe des Treiberautors
(ltdstudio/minisforum-n5-it5571), nicht von uns nach TESTSPEC geprüft; `Bericht` =
Nutzerbericht im Upstream-Issue, nicht geprüft. `validated` und `tested` setzen Herkunft
`eigen` voraus; Generator und n5-fangov lehnen sonst die Tabelle ab.

## Tabelle

<!-- hwstatus:begin -->
| Produkt | Board | BIOS | EC-FW | Status | Herkunft | Stand | Bemerkung |
|---|---|---|---|---|---|---|---|
| N5 PRO | F8NAA | 1.05 | 0.14 | experimental | eigen | 2026-11-01 | TESTSPEC 0.4 T1 (2026-11-01): pwm1 PASS (zwei Werkzeuge), pwm3 FAIL (Luftstromkopplung fan2 bei konstantem DCR2), pwm2/pwm4 nicht gelaufen (Gate G4); T2/T3 nicht gelaufen; F8b 2026-10-08–15 und 2026-10-17–24 PASS; F1 (PCIe-Rückgabe) COMPLETE (BEFUNDE.md) |
| N5 | F8NAA | * | * | experimental | upstream | 2026-10-09 | upstream validated (Treiberautor, BIOS 1.04), nicht durch uns geprüft, kein TESTSPEC-Lauf; Treiber-Flag `validated` = Schreib-Gate |
| N5A | F8NAB | * | * | experimental | upstream | 2026-10-09 | N5 Air; read-only (keine Schreibfreigabe ab Treiber 0.2.1+sr1) |
| N5 AIR | F8NAB | * | * | experimental | upstream | 2026-10-09 | N5 Air, alternativer DMI-String; read-only (keine Schreibfreigabe ab Treiber 0.2.1+sr1) |
| N5 PRO | F8NAB | * | * | experimental | Bericht | 2026-11-02 | Upstream-Issues #6/#8; #7 (2026-11-01): zwei Nutzer melden Schreibbetrieb pwm1–3 mit Fork-Treiber (BIOS 1.04), nicht nach TESTSPEC; ab Treiber 0.2.1+sr2 DMI-Eintrag (bindet ohne force), read-only, Schreibfreigabe erst mit Rohdaten |
<!-- hwstatus:end -->

## TESTSPEC-Läufe

Je Lauf eine Zeile: Board, BIOS, EC-FW, Test, Spec-Version, Verdict, Link auf
`befunde/runs/<lauf>/`. Gültig ist `docs/TESTSPEC.md` 0.4; ein Lauf behält die Version, nach
der er lief. Read-only-Ergebnisse von Boards außerhalb der Schreibtabelle gehören ebenfalls
hierher (TESTSPEC 0.4 §8).

| Lauf | Board | BIOS | EC-FW | Test | Spec | Verdict |
|---|---|---|---|---|---|---|
| `baseline-20261008` (08.–15.10.2026; Treiber `0.2.1-1` = upstream v0.2.1, Kernel `7.0.14-20-pve`, n5-fangov 0.4.2) — [BEFUNDE.md §F8b-Baseline](BEFUNDE.md#f8b-baseline-baseline-20261008-0810-2225--1510-2225), [Rohzusammenfassung](raw/f8b-baseline-20261008-abschluss.txt), [Rohdaten](runs/20261008-2225-F8NAA-1.05-f8b/) | N5 PRO / F8NAA | 1.05 | 0.14 | F8b | 0.1-draft | PASS (1 run; Baseline, Treiber unverändert upstream; K1–K8 wie gelaufen) |
| `patched-20261017` (17.–24.10.2026; Treiber `0.2.1+sr1~rc2`, Kernel `7.0.14-20-pve`, n5-fangov 0.4.2) — [BEFUNDE.md §F8b-Lauf 2](BEFUNDE.md#f8b-lauf-2-patched-20261017-1710-1541--2410-1541), [Rohzusammenfassung](raw/f8b-patched-20261017-abschluss.txt), [Rohdaten](runs/20261017-1541-F8NAA-1.05-f8b/) | N5 PRO / F8NAA | 1.05 | 0.14 | F8b | 0.3 | PASS (1 run; gestartet unter 0.3, K1–K8 wie gelaufen; Vergleich mit Baseline 0.2.1 nach 0.4 §5a erfüllt) |
| Wartungsfenster 2a (01.11.2026; Treiber `0.2.1+sr1~rc3`, Kernel `7.0.14-26-pve`, n5-ectest 0.1.0-mw2, n5-fangov 0.5.0-rc4) — [BEFUNDE.md §Wartungsfenster 2a](BEFUNDE.md#wartungsfenster-2a-01112026), [Rohdaten](runs/) `20261101-*` | N5 PRO / F8NAA | 1.05 | 0.14 | T1 pwm1 | 0.4 | PASS ([`…1547…-pwm1`](runs/20261101-1547-F8NAA-1.05-validate-pwm1/)); D4 Zweitumsetzung n5-fangov `validate` PASS ([`…1631…-pwm1`](runs/20261101-1631-F8NAA-1.05-validate-pwm1/)), DCR je Stufe gleich, Tacho Δ ≤ 6 rpm |
| dto. | N5 PRO / F8NAA | 1.05 | 0.14 | T1 pwm3 | 0.4 | FAIL „another tach follows“: fan2 max +58 rpm (Grenze ±50) bei konstantem DCR2 = 75 ([`…1604…-pwm3`](runs/20261101-1604-F8NAA-1.05-validate-pwm3/)) |
| dto. | N5 PRO / F8NAA | 1.05 | 0.14 | T1 pwm2, pwm4 | 0.4 | INCONCLUSIVE (Gate G4, ganzzahliger EC-Wert 29↔30 °C; `…1557…`, `…1617…`, `…1637…`, `…1702…`) |
| dto. | N5 PRO / F8NAA | 1.05 | 0.14 | F1 (PCIe-Rückgabe) | 0.4 | COMPLETE: DCR5 0xff an +1/+60/+300 s, Kurventabellen identisch ([`raw/f8a-pcie-m2-20261101T172305Z.txt`](raw/f8a-pcie-m2-20261101T172305Z.txt)) |
