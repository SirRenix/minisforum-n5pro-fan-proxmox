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
| N5 PRO | F8NAA | 1.05 | 0.14 | experimental | eigen | 2026-10-09 | TESTSPEC-Lauf ausstehend (Wartungsfenster P7/P8); F8b-Baseline läuft seit 2026-10-08; Kanalzuordnung am 2026-09-14 vor TESTSPEC gemessen (BEFUNDE.md Phase 6) |
| N5 | F8NAA | * | * | experimental | upstream | 2026-10-09 | upstream validated (Treiberautor, BIOS 1.04), nicht durch uns geprüft, kein TESTSPEC-Lauf; Treiber-Flag `validated` = Schreib-Gate |
| N5A | F8NAB | * | * | experimental | upstream | 2026-10-09 | N5 Air; read-only (keine Schreibfreigabe ab Treiber 0.2.1+sr1) |
| N5 AIR | F8NAB | * | * | experimental | upstream | 2026-10-09 | N5 Air, alternativer DMI-String; read-only (keine Schreibfreigabe ab Treiber 0.2.1+sr1) |
| N5 PRO | F8NAB | * | * | experimental | Bericht | 2026-10-09 | Upstream-Issues #6/#8; nicht in der DMI-Tabelle des Treibers, bindet nur mit force=1 und dann read-only |
<!-- hwstatus:end -->

## TESTSPEC-Läufe

Noch keine. Je Lauf eine Zeile: Board, BIOS, EC-FW, Test, Spec-Version, Verdict, Link auf
`befunde/runs/<lauf>/`.

| Lauf | Board | BIOS | EC-FW | Test | Spec | Verdict |
|---|---|---|---|---|---|---|
