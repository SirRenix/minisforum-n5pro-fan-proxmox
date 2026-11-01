# Lauf-Rohdaten (TESTSPEC §8)

Je Lauf ein Verzeichnis mit den Rohdaten und `SHA256SUMS` über alle Dateien des Verzeichnisses
(`sha256sum -c SHA256SUMS` im Laufverzeichnis).

## F8b-Läufe (Watcher `tools/validation-watch`)

| Lauf | Zeitraum | Treiber | Verdict | Zusammenfassung |
|---|---|---|---|---|
| [`20261008-2225-F8NAA-1.05-f8b/`](20261008-2225-F8NAA-1.05-f8b/) (`baseline-20261008`) | 08.–15.10.2026 | upstream 0.2.1 | PASS | [BEFUNDE.md §F8b](../BEFUNDE.md), [`raw/f8b-baseline-20261008-abschluss.txt`](../raw/f8b-baseline-20261008-abschluss.txt) |
| [`20261017-1541-F8NAA-1.05-f8b/`](20261017-1541-F8NAA-1.05-f8b/) (`patched-20261017`) | 17.–24.10.2026 | `0.2.1+sr1~rc2` | PASS | [BEFUNDE.md §F8b-Lauf 2](../BEFUNDE.md), [`raw/f8b-patched-20261017-abschluss.txt`](../raw/f8b-patched-20261017-abschluss.txt) |

## Wartungsfenster 2a (01.11.2026, `n5-ectest` / n5-fangov `validate`, TESTSPEC 0.4)

Bündel nach §8 (`meta.json`, `events.log`, `samples.csv`, `verdict.json`, ggf. `restore.json`);
Namensregel `<YYYYMMDD-HHMM UTC>-<board>-<bios>-<test>`. Hostname von den Werkzeugen selbst
auf `n5host` gesetzt, keine weiteren Ersetzungen nötig (geprüft auf IP-, MAC-, Mailadressen).
Verdicts: [`../RESULTS.md`](../RESULTS.md) „TESTSPEC-Läufe“, Einordnung [`../BEFUNDE.md`](../BEFUNDE.md) §Wartungsfenster 2a.

| Lauf | Werkzeug | Verdict T1 |
|---|---|---|
| `20261101-1512-…-validate-pwm1` | n5-ectest map | INCONCLUSIVE (G4) |
| `20261101-1547-…-validate-pwm1` | n5-ectest map | PASS |
| `20261101-1557-…-validate-pwm2` | n5-ectest map | INCONCLUSIVE (G4) |
| `20261101-1604-…-validate-pwm3` | n5-ectest map | FAIL |
| `20261101-1617-…-validate-pwm4` | n5-ectest map | INCONCLUSIVE (G4) |
| `20261101-1631-…-validate-pwm1` | n5-fangov validate (D4) | PASS |
| `20261101-1637-…-validate-pwm4` | n5-ectest map | INCONCLUSIVE (G4) |
| `20261101-1702-…-validate-pwm2` | n5-ectest map | INCONCLUSIVE (G4) |
| `20261101-1721-…-snapshot-mw2-loaded` | n5-ectest snapshot | REPORTED (T6) |

## Watcher-Läufe: Inhalt

Inhalt je Lauf: `samples.csv` (ein Sample je 30 s), `checks.log` (Epoch je Check-Lauf, K8),
`reports/day-N.txt` und `reports/final.txt`, `config.snapshot` (Watcher-Konfig bei Installation),
`final.done` (Abschlusszeit und Verdict). Spalten und Kriterien: `tools/validation-watch/README.md`.

Nicht enthalten: `state` (Journal-Cursor), Lock- und leere Fehlerdateien. `alerts/` war in beiden
Läufen leer und fehlt deshalb (git speichert keine leeren Verzeichnisse). Ein `events.log` gab es
nicht, weil kein Ereignis auftrat.

Die Watcher-Läufe sind keine `n5-ectest`-Läufe: `meta.json`, `events.log` und `verdict.json` nach
§8 entstehen nur bei `n5-ectest`; das Verdict steht in `reports/final.txt` und `final.done`.
Verzeichnisname = Lauf-ID des Watchers statt `<YYYYMMDD-HHMM>-<board>-<bios>-<test>`;
Board/BIOS/EC-FW stehen in `../RESULTS.md`.

## Anonymisierung

Geholt am 31.10.2026 nur lesend von `/var/lib/n5-validation/<lauf>/` des Referenzhosts.
Ersetzt: Hostname → `n5host` (Berichtskopf). Sonst bytegleich: keine IP-, MAC- oder Mailadressen
enthalten (`MAIL_TO=root`); die Boot-IDs sind zufällige Werte je Boot und stehen auch in den
Zusammenfassungen unter `raw/`.
