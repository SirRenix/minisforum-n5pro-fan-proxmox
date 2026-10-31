# Kennlinie Raumsensor (Rack-Klima) ↔ EC-Temperaturen

Stand 15.10.2026 23:40, final für den Zeitraum der F8b-Baseline. Gehört zu Plan P6 / Laufkarte V8.
Ersetzt den Entwurf vom 15.10. 18:45 (der Entwurf hatte die Ursache der Lastphasen offen und
nannte nur eine Lastphase — beides hier korrigiert).

Kennzeichnung: **gemessen** = direkt aus den Daten, **abgeleitet** = Schluss aus gemessenen
Werten, **offen** = nicht geklärt.

## Datenbasis

| Quelle | Inhalt | Zeitraum | Umfang |
|---|---|---|---|
| HA-Raumsensor (Rack-Klima) | Raumsensor am Rack, Supervisor-API `history/period` (`minimal_response`, `no_attributes`) | Sensor ab 10.10. 20:48; ausgewertet ab **10.10. 22:20** (Reporting-Schwelle 0,3 K seitdem aktiv) bis letzter Wert 15.10. 22:13 | 263 numerische Zustände, Auflösung 0,01 °C, Meldung nur bei Änderung (größte Pause 120 min, 15.10. 16:14) |
| F8b-Sampler `samples.csv` (`baseline-20261008`) | `ec_temp4_ambient_c` (EC 0x06), `ec_temp3_board_c` (EC 0x05), `ec_temp2_system_c` (EC 0x04), `load1`, `k10_tctl_c` | 08.10. 22:25 – 15.10. 22:30 | 20 061 Samples à 30 s, EC-Auflösung 1 °C; davon 14 336 mit HA verknüpft |
| PVE-RRD je Gast (`pvesh … rrddata --timeframe week`) | CPU-Mittel je 30 min | Woche bis 15.10. 23:20 | nur zur Zuordnung der Lastepisoden |

Verknüpfung: je Sample der zuletzt gemeldete HA-Wert (Stufenfunktion). Offset `d06 = EC06 − HA`
(entsprechend `d05`, `d04`). Ausgelassen: 6 Samples in der HA-Lücke `unavailable/unknown`
11.10. 01:28–01:38. Nicht verwendet: HA 10.10. 20:48–20:52 (31,15 → 27,33 °C, Sensor beim
Einrichten, Werte fallen in 4 min um 4 K) und die Einrichtungsphase bis 22:20.
Skripte und Ausgaben liegen außerhalb des Repos
(`kennlinie-final.ps1`, `episoden-final.ps1`, `*-final-out.txt`, Entwurfsauswertung `kennlinie*.ps1`).

## Ergebnis

| | Aussage | Art |
|---|---|---|
| ✅ | **Leerlauf (load1 < 0,5, Tctl < 40 °C, n = 6 624): EC 0x06 = HA − 3,93 °C** (σ 0,38, Einzelwerte −4,8 … −2,4). Je Tagesabschnitt −3,84 … −4,10 °C, je Tag −3,80 … −4,04 °C (10.10. nur 73 Samples: −4,38) | gemessen |
| ✅ | Leerlauf EC 0x05 (Board) = HA − 0,23 °C, EC 0x04 (System) = HA + 6,67 °C | gemessen |
| ✅ | Raum im Leerlauf 25,5–26,0 °C, Mittel 25,74 °C; über den ganzen Zeitraum 25,48–27,87 °C, Werte > 26,7 °C nur während Lastepisoden | gemessen |
| ✅ | **Last verkleinert den Offset d06:** load1 < 0,5 → −3,92 · 0,5–1 → −3,82 · 1–2 → −3,75 · 2–4 → −3,12 · ≥ 4 → −2,56 °C; nach Tctl: < 40 → −3,93 · 40–50 → −3,57 · ≥ 50 → −2,35 °C | gemessen |
| ✅ | **Vier wiederholte Lastepisoden mit HA-Abdeckung**, alle Tctl ≈ 62 °C für 66–130 min: EC06 steigt je um 3,0–3,2 K (Maximum 20–25 min nach Beginn), HA um 1,50–1,69 K (Maximum am Ende der Episode). Tabelle unten | gemessen |
| ✅ | Korrelation EC06 ~ HA: r = 0,86 gleichzeitig, **0,90 bei HA 20 min später** (10 min 0,897, 30 min 0,883, 60 min 0,724); nur 12.–15.10.: 0,77 → 0,84 bei 20 min | gemessen |
| ✅ | EC 0x06 Wertebereich 21–26 °C; Häufigkeit 22 °C: 67 %, 21 °C: 18 %. Ohne HA (08.10. 22:25 – 10.10. 22:20, 5 536 Samples) Mittel EC06 21,2 °C | gemessen |
| ➖ | 0x06 ist **kein reiner Lufteinlass-Fühler**: er steigt bei Tctl ≈ 62 °C um ~3 K, doppelt so stark wie der Raumsensor; r EC06~Tctl 0,70. Eigenwärme im Gehäuse | abgeleitet |
| ➖ | Der HA-Sensor ist **keine reine Raumreferenz**: er steigt in jeder Lastepisode um ~1,6 K und folgt EC06 mit ~20 min → Abwärme des Hosts/Racks erreicht den Fühler | abgeleitet |
| ➖ | Faustformel Leerlauf: **HA (Rack-Klima) ≈ EC06 + 3,9 °C (± 0,5)**. Lineare Regression über alle Samples ist von den Lastepisoden bestimmt und **nicht** als Kennlinie brauchbar | abgeleitet |
| ➖ | HDD-Kurve (Tabellen C/D, Eingang 0x06, Schwelle 34 °C): 0x06 erreicht 34 °C erst bei HA ≈ 38 °C im Leerlauf bzw. ≈ 36 °C unter Last. Im Normalbetrieb bleibt die HDD-Automatik unter 34 °C → T4/T5 HDD bleibt `INCONCLUSIVE` wie in TESTSPEC 0.3 erwartet | abgeleitet |

### Lastepisoden mit HA-Abdeckung

| Episode | Dauer | HA vor → max | ΔHA | EC06 vor → max | ΔEC06 | Tctl Ø | load1 Ø | HA-Max rel. Ende | EC06-Max rel. Ende |
|---|---|---|---|---|---|---|---|---|---|
| 11.10. 08:55 (Scrub + Gast A) | 130 min | 26,37 → 27,87 | 1,50 | 23,0 → 26 | 3,0 | 62,4 | 2,68 | −49 min | −77 min |
| 12.10. 08:55 (Gast A) | 76 min | 25,74 → 27,33 | 1,59 | 21,9 → 25 | 3,1 | 62,5 | 0,92 | +3 min | −53 min |
| 13.10. 08:55 (Gast A) | 66 min | 25,74 → 27,40 | 1,66 | 21,8 → 25 | 3,2 | 62,3 | 0,68 | −3 min | −43 min |
| 15.10. 20:55 (Gast A) | 76 min | 25,80 → 27,49 | 1,69 | 22,0 → 25 | 3,0 | 62,4 | 0,87 | 0 min | −54 min |

Gemessen; „vor“ HA = letzter Wert bei Beginn, EC06 = Mittel 30 min vorher. 11.10.: EC06 lag
durch den Scrub schon bei 23 °C. 14.10. und 15.10. morgens war die Episode nur 4–10 min lang
und ist nicht aufgeführt.

## Störgrößen (Lastquellen im Zeitraum)

| Zeitraum | Quelle | Beleg | Wirkung auf die Kennlinie | Art |
|---|---|---|---|---|
| 11.10. 00:24–13:38 | ZFS-Scrubs aller Pools (media 13 h 14 min) | `zpool status`, zed `scrub_start/finish` | load1 2,4–4,7 bei Tctl nur 41–44 °C; d06 −2,3 … −2,6 °C (00:30–03:30, load ≈ 4,6), danach −3,3 °C (load ≈ 2,6) | gemessen |
| täglich ~08:55, 66–76 min (09., 12., 13. Okt.; 14./15. nur 4–10 min) und 15.10. 20:55–22:11 | Gast A (Container) | RRD: Gast A CPU 0,03 → 0,28 Kerne genau in diesen Fenstern, alle anderen Gäste flach | Tctl ≈ 62–65 °C bei load1 < 1 → Hauptquelle der Lastepisoden oben | gemessen (Zuordnung); Auslöser in Gast A **offen** (keine Timer/Cron im Container außer Systemjobs; vermutlich iGPU-Last, weil Tctl stark und load1 kaum steigt — abgeleitet) |
| 09.10. 17:30–20:00, 10.10. 00:00–03:00 | Gast B (Build-VM; Builds/Tests P4/P5 am 09.10.) | RRD: Gast B CPU 0,17 → 0,35–0,66 Kerne | Tctl bis 73 °C, load1 bis 4,2 — **vor** dem HA-Sensor, daher nicht in der Kennlinie | gemessen |
| 15.10. (V2-Build in Gast B, Session 17:00) | Gast B | RRD 15.10. 16:00–20:30 flach (0,16 Kerne), 21:30–22:00 0,23 | in den 30-min-Mitteln nicht sichtbar; kurze Spitze 15.10. 20:17–20:27 (load1 6,6, Tctl 46) nicht zugeordnet | gemessen / Zuordnung offen |
| 11.10. 03:00 | vzdump-Backup eines Containers | Journal, 13 s | vernachlässigbar | gemessen |

Nicht herausgerechnet: Die Leerlauf-Kennlinie verwendet nur Samples mit load1 < 0,5 und
Tctl < 40 °C, schließt die Episoden damit aus; der ~20-min-Nachlauf des HA-Sensors nach einer
Episode ist darin nicht eigens gefiltert (Wirkung: d06 im Leerlauf eher etwas zu negativ;
Größe nicht bestimmt).

## Offen

- Raumtemperatur fast konstant (Leerlauf 25,5–26,0 °C). Die Steigung EC06 gegen Raum bei
  **wechselndem Raum** ist nicht bestimmbar — braucht einen kühleren/wärmeren Zeitraum oder einen
  zweiten Fühler weiter weg vom Gerät.
- Lastkennlinie in Stufen fehlt (Episoden hatten alle Tctl ≈ 62 °C). Ggf. im Wartungsfenster
  gezielt (z. B. `stress-ng` 15 min je Stufe) — Entscheidung Betreiber.
- Welcher physische Fühler 0x06 ist (EC kopiert aus XRAM 0x893A, Quelle davor nicht verfolgt),
  bleibt offen (p1-ec-tabellen §5).
- Fühlerposition des HA-Sensors relativ zu Einlass/Auslass dokumentieren (Foto/Abstand), sonst
  ist der Verzug von ~20 min nicht einzuordnen.
- Auslöser der täglichen Gast-A-Last um 08:55 (und 15.10. 20:55) nicht ermittelt; für die
  Kennlinie unerheblich, für Lauf 2 als bekannte Störgröße vorab notieren.
