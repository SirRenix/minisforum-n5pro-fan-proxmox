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
| `tested` | TESTSPEC-Lauf auf 1 Gerät, Bündel veröffentlicht |
| `experimental` | lädt, kein TESTSPEC-Lauf; Schreiben nur, wenn in der Allowlist des Treibers |
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
| N5 PRO | F8NAA | 1.05 | 0.14 | experimental | eigen | 2026-10-17 | TESTSPEC-Lauf unvollständig (Wartungsfenster 1 am 2026-10-17: T1 nicht gelaufen, Gate G4; BEFUNDE.md); F8b-Baseline 2026-10-08–15 PASS; Kanalzuordnung am 2026-09-14 vor TESTSPEC gemessen (BEFUNDE.md Phase 6) |
| N5 | F8NAA | * | * | experimental | upstream | 2026-10-09 | upstream validated (Treiberautor, BIOS 1.04), nicht durch uns geprüft, kein TESTSPEC-Lauf; Treiber-Flag `validated` = Schreib-Gate |
| N5A | F8NAB | * | * | experimental | upstream | 2026-10-09 | N5 Air; read-only (keine Schreibfreigabe ab Treiber 0.2.1+sr1) |
| N5 AIR | F8NAB | * | * | experimental | upstream | 2026-10-09 | N5 Air, alternativer DMI-String; read-only (keine Schreibfreigabe ab Treiber 0.2.1+sr1) |
| N5 PRO | F8NAB | * | * | experimental | Bericht | 2026-10-09 | Upstream-Issues #6/#8; nicht in der DMI-Tabelle des Treibers, bindet nur mit force=1 und dann read-only |
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
