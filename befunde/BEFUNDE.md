# Befunde — N5 Pro EC / IT5571

Rohlogs: `befunde/raw/` (nur der Hostname in `uname -a` ersetzt). Alle Zeiten Europe/Berlin.

## System

| Feld | Wert | erfasst am |
|---|---|---|
| product_name | `N5 PRO` | 14.09.2026 16:02 |
| board_name | `F8NAA` | 14.09.2026 16:02 |
| board_version | `1.0` | 14.09.2026 16:02 |
| bios_version | `1.05` | 14.09.2026 16:02 |
| bios_date | `03/31/2026` | 14.09.2026 16:02 |
| Kernel (`uname -r`) | `7.0.12-1-pve` | 14.09.2026 16:02 |
| PVE-Version | `pve-manager/9.2.3`, Debian 13 (trixie) | 14.09.2026 16:02 |
| Upstream-Stand | `feb6d9a` (v0.2.1), Modul-Version 0.2.0 | 14.09.2026 16:03 |
| DMI-Match im Treiber vorhanden | **ja** — exakt, kein Patch, kein `force` | 14.09.2026 16:04 |
| 0x62/0x66 in /proc/ioports belegt durch | **niemanden** — `PNP0C09:00` hat `status=0` | 14.09.2026 16:02 |
| Secure Boot / sig_enforce / lockdown | aus / N / none | 14.09.2026 15:59 |

## Phase 3 — Read-only-Probe (Userspace, `ioperm`)

Lauf 14.09.2026 16:03:38, Log `raw/03-probe-20260914-160338.txt`.

| Prüfung | Ergebnis | Bemerkung |
|---|---|---|
| `sio_probe` Chip-ID | (Skript rief ohne Argument) | Chip-ID kam über `n5_fan probe` |
| `n5_fan probe` Chip-ID | **0x5571** | erwartet 0x5571 ✓ |
| `n5_fan probe` PMC1 aktiv | **ja**, data 0x62 / cmd 0x66 | ✓ |
| `n5_fan probe` PMC2 aktiv | **ja**, data 0x68 / cmd 0x6c | hartkodierte Ports des Moduls stimmen ✓ |
| EC[0x09] CPU idle / Last | 35 °C / **77 °C** | k10temp Tctl 41 → 65 °C (einige s nach Lastende gelesen) ✓ folgt |
| EC[0x04] System | 32 → 34 °C | ✓ |
| EC[0x05] Board | 25 → 28 °C | ✓ |
| EC[0x06] Ambient | 22 → 22 °C | ✓ |
| EC[0x34] PWM-Sollwert | 0x64 = 100 %, konstant | EC-Automatik meldet Vollfreigabe |
| TACH1 idle / Last | 2003 / **2965 RPM** | CPU-Lüfter reagiert auf Last ✓ |
| TACH2 idle / Last | 2236 / 2211 RPM | SSD-Lüfter, unter CPU-Last stabil ✓ |
| TACH3 idle / Last | 1656 / 1653 RPM | HDD-Gruppe, stabil ✓ |
| Zwischen Dump 1 und 2 geänderte Bytes | **nur 0x04, 0x05, 0x09** | genau die Temperaturoffsets |

Nebenbefund: EC-RAM 0x11–0x27 und 0x70–0x81 enthalten Wertepaare
(`19/00 14/2D 19/17 37/2D … 22/64`), die wie Temperatur/PWM-Kurventabellen
aussehen. Nicht weiter untersucht, nicht Teil des Treibers.

**Bewertung:** Werte plausibel — **ja.** Alle vier Temperaturoffsets und alle
drei Tachos liefern konsistente Werte, das Protokoll passt zum N5 Pro.

## Phase 4/5 — Modul

Lauf 14.09.2026 16:04–16:06, Logs `raw/05-load-20260914-160521.txt`.

| Prüfung | Ergebnis |
|---|---|
| Bau erfolgreich | **ja**, keine Warnungen gegen 7.0.12-1-pve (BTF übersprungen, kein vmlinux — unkritisch) |
| `vermagic` == `uname -r` | **ja** (`7.0.12-1-pve SMP preempt mod_unload modversions`) |
| Laden ohne `force` | **ja** — DMI-Match `Minisforum N5 Pro / F8NAA` |
| Laden mit `force=1` | nicht nötig, nicht getestet |
| Fehlercode bei Misserfolg | — |
| dmesg | `experimental read-only mode; PWM nodes hidden` / `registered 4 fan and 4 temperature channels` |
| hwmon-Pfad | `/sys/class/hwmon/hwmon14`, `sensors`-Chip `minisforum_n5_it5571-isa-0000` |
| /proc/ioports während Laden | 0x62, 0x66, 0x68, 0x6c reserviert durch `minisforum_n5_it5571`; nach `rmmod` wieder frei |
| temp1–4 plausibel | **ja**: CPU 36–37 / System 34 / Board 27 / Ambient 22 °C (k10temp 38–39 °C) |
| fan1–3 plausibel | **ja**: 2028 / 2225 / 1649 RPM — deckungsgleich mit `n5_fan status` (2034 / 2222 / 1649) |
| fan4 | 0 RPM, Label `PCIe Fan (no tach)` — erwartet |
| pwm-Knoten sichtbar | **nein** — korrekt ausgeblendet ohne `experimental_write` |
| Unload | sauber, EC-Automatik unverändert (Lüfter danach 2022 / 2229 / 1647 RPM) |
| Kernel-Log nach Unload | keine Warnungen, kein Oops |

## Phase 6 — PWM-Test, Kanal für Kanal

Läufe 14.09.2026 16:53–17:17, Logs `raw/06-pwmtest-ch*.txt`. Validierung
messtechnisch: pro Schritt alle drei Tachos, nur der beschriebene Kanal darf
sich bewegen. Abbruchgrenzen CPU 85 °C / NVMe 70 °C / RPM 0 nie erreicht.

Erster Lauf (16:53, Kanal 4) scheiterte mit `-EBUSY` beim Schreiben auf
`pwm4`: der Treiber verlangt `pwmN_enable=1` vor jedem `pwmN`-Write
(Default 2 = EC-Automatik). Skript entsprechend ergänzt; der Abbruchpfad hat
dabei funktioniert (Delta ≤ 9 RPM nach Restore).

| Kanal | erwarteter Lüfter | reagiert tatsächlich | RPM 100 % | 85 % | 70 % | 54 % | andere Tachos | Bemerkung |
|---|---|---|---|---|---|---|---|---|
| pwm4 | PCIe | **kein Tacho bewegt sich** | n/a | n/a | n/a | n/a | ±15 RPM (Rauschen) | kein Lüfter am Header; alle Writes akzeptiert |
| pwm1 | CPU | **ja, nur fan1** | 5073 | 4445 | 3830 | 3120 | fan2 −29…+16, fan3 −28…−6 | EC-Idle: 2020 RPM ≈ Duty 85 |
| pwm2 | SSD | **ja, nur fan2** | 4687 | 4230 | 3790 | 3280 | fan1 −15…0, fan3 −12…+8 | NVMe fiel dabei 46 → 44 °C |
| pwm3 | HDD-Gruppe | **ja, nur fan3** | 3540 | 3160 | 2725 | 2250 | fan1 −6…+11, fan2 −32…+36 | langsamer Anlauf (1873 → 2668 → 3523 in 10 s) |

Spalte „andere Tachos“: Spanne der Abweichung vom Ausgangswert (ein Sample) aus den Rohdaten
`raw/06-pwmtest-ch*`, je 36 Samples.

**Kanalzuordnung des N5-Pro-Profils ist vollständig bestätigt.** DCR3 und DCR4
tragen im I2EC-Register immer denselben Wert — die Gruppierung der beiden
HDD-Lüfter im Treiber stimmt.

**Nach Modul-Unload: EC-Automatik wiederhergestellt?**

| Kanal | Automatik-Befehl | Tacho zurück auf Ausgang | Regelt der EC danach? |
|---|---|---|---|
| pwm1 CPU | 0x21 | ja (2020 → 2009) | **ja** — DCR1 0x55 → 0x94 unter CPU-Last |
| pwm2 SSD | 0x2e | ja (2220 → 2173, driftet auf 2130) | nicht separat geprüft |
| pwm3 HDD | 0x2b | nicht auf Ausgangswert (1647 → 1117, stabil ~1240) — Kriterium ungeeignet, s. u. | **verlässt den Schreibwert: ja** — DCR3/4 0x8c (140) → 0x57 (87); 0x8c ist der letzte Schreibwert (Stufe 140, fan3 ≈ 2250 RPM), **nicht per I2EC gemessen** — der erste I2EC-Wert (17:20) stammt schon von nach 0x2b. **Erklärung seit M5 (09.10., aus EC-Firmware 0.14 abgeleitet, Messwerte passen):** Die EC-Automatik regelt HDD auf eine **Solldrehzahl** (±100 rpm), Eingang ist **EC 0x06 (Ambient)**: < 34 °C → 1200 rpm, 34–99 °C → 1600 rpm. Gemessen nach 0x2b: 1237–1240 rpm bei 0x06 = 22 °C — im Band, also **geregelt**. Keine Reaktion auf HDD-/System-Last, weil 0x06 sich nicht bewegte. Details `p1-ec-tabellen-20261008.md` §5 |
| pwm4 PCIe | 0x31 | n/a | **nein** — DCR5 behält den letzten Schreibwert 0x8c (140). Seit M5 erklärt (abgeleitet): PCIe hat in der EC-Firmware **keine Automatik**, nach 0x31 schreibt der EC DCR5 nie wieder; kein Lüfter am Header |

Details in `raw/06-i2ec-dcr-20260914.txt` und `raw/06-pwmtest-ch3-20260914-171352.txt`
(letzte Stufe 140). Ein I2EC-Snapshot **vor** den Tests fehlt (seither im Skript).

Korrigiert 08.10.2026 nach Hinweis in Upstream-#7 (F8NAB: HDD kehrt nach 0x2b
zurück, Rampe ~4 min). Die ursprüngliche Aussage „EC regelt HDD nicht mehr" war
falsch: der Kanal bleibt nicht auf dem Schreibwert. Ob danach die EC-Kurve oder
ein fester Default greift, unterscheiden weder unsere noch die F8NAB-Daten.
Fehlerursache: Prüfkriterium „Tacho nach 20 s ±150 RPM am Ausgangswert" — Fenster
zu kurz, Ausgangswert kein gesicherter EC-Automatik-Zustand. Ob die HDD-Kurve
auf Temperatur reagiert, ist offen: der Lasttest hat ihren (unbekannten)
Eingangssensor vermutlich kaum bewegt. Neumessung: `N5Pro/validierung-konzept.md`.

**Konsequenz:** Für den Dauerbetrieb wird die Regelung im OS übernommen, nicht
an den EC zurückgegeben — seit 15.09.2026 durch n5-fangov (14.–15.09. durch den
Vorgänger `n5-fand`, heute `legacy/`). Stoppverhalten des Reglers: CPU/SSD →
EC-Automatik (nachweislich funktionsfähig), HDD → fester sicherer Wert 140
(≈ 2250 RPM; am 14.09. stand hier noch 119 ≈ 1900 RPM). Ab Treiber `0.2.1+sr1` (Patch 0006)
setzt der Treiber beim Reboot/Ausschalten HDD und PCIe auf 255 und CPU/SSD auf
EC-Automatik. T6 gemessen 17.10.2026 (§Wartungsfenster 1, F3/A4): BIOS/EC setzt DCR3/4 bei
Kalt- und Warmstart auf 0x64 (100), der Regler-Failsafe HDD 140 übersteht also keinen Neustart;
DCR5 (PCIe) behält seinen Wert über einen Warmstart, nach Kaltstart 0x00.

## Dauerbetrieb (seit 14.09.2026 17:33)

| Komponente | Stand |
|---|---|
| Modul | DKMS `minisforum-n5-it5571/0.2.0`, gebaut für 7.0.12-1-pve, `AUTOINSTALL=yes`; mit DKMS-Schlüssel signiert, Schlüssel nicht als MOK eingeschrieben (korrigiert 08.10., s. P1) |
| Autoload | `modules-load.d` + `modprobe.d` mit `experimental_write=1` (Modul schreibt beim Laden nichts) |
| Regler | `n5-fand.service`, Bash, ~4 MB RSS, 10-s-Zyklus, Kurven in `/etc/n5-fand.conf`; Härtung 14.09. abends: Watchdog, Failsafe-Hook, Stall-/Sensor-/Konfigprüfung, Alarme, CLI `n5fan` (Details `docs/BETRIEB.md`) |
| Lasttest | 6 Kerne 90 s: DCR1 0x55 → 0xe1, Tctl gehalten bei 73 °C (EC ließ 83 °C zu), Rückregelung 15/Zyklus, SSD/HDD unberührt |
| Stopptest | CPU/SSD zurück in EC-Automatik (DCR1 wieder 0x55), HDD manuell, Neustart sauber |
| Watchdog-Test | SIGSTOP → nach 60 s `ABRT` durch systemd → Failsafe `pwm1=auto pwm2=auto pwm3=140` → OnFailure-Alarm (Mail angekommen) → Neustart, Regler übernimmt |
| Absturz-Test | SIGKILL → Failsafe → Neustart nach 5 s |
| Alarmweg | PVE-Notification-Template `n5-fand`, Testmail über SMTP-Target zugestellt |
| CLI | `set hdd 75%` → 191 manuell, `auto hdd` → Rückkehr mit Schrittbegrenzung, `set hdd 10%` abgelehnt |
| Konfigfehler | `INTERVAL=abc`, `TMIN>=TMAX`, `TCRIT<=TMAX` → gemeldet, Defaults, Alarm, Start trotzdem |
| HDD-Kurve | 36 → 105 … 46 → 255 (schärfer als EC); Platten binnen 5 min von 40 auf 39 °C bei ~2500 RPM |
| Nicht provoziert | Lüfterstillstand, Sensorausfall, Modulverlust zur Laufzeit, Reboot |
| Boot-Persistenz | konfiguriert, **noch nicht durch Reboot bewiesen** (nächstes Wartungsfenster) |

## Offene Punkte

- Reboot-Nachweis: Modul lädt automatisch, `n5-fand` startet, Kurven greifen.
  DCR-Initialisierung durch BIOS 1.05 geklärt 17.10.2026 (§Wartungsfenster 1, T6).
- Bedeutung von EC[0x34] = 100 % konstant ungeklärt; liegt innerhalb einer
  Tabelle bei 0x30–0x38, vermutlich Kurvenpunkt, kein Live-Sollwert.
- EC-RAM-Tabellen: dekodiert 08.10. (Schreibtisch, Dumps 14.09.) — Tripel
  (Temperatur, Duty, Duty des Vorpunkts), nicht Temp/PWM/Hysterese. Details und
  offene Zuordnung in `p1-ec-tabellen-20261008.md`.
- `n5-fand` vor `modprobe -r` stoppen; das Modul hält keinen Refcount.

## P1 — Read-only-Klärung (08.10.2026)

Plan `N5Pro/plan-treiber-validierung-release.md` P1. Rohdaten `raw/p1-host-20261008.txt`
(Host, nur lesend), `p1-ec-tabellen-20261008.md` (Schreibtisch, kein Hostzugriff). Jede Aussage
von einem zweiten Agenten gegengeprüft.

| Punkt | Ergebnis | Status |
|---|---|---|
| Kernel | `7.0.14-20-pve` | gemessen |
| debugfs / Lockdown | eingehängt, `CONFIG_DEBUG_FS_ALLOW_ALL=y`, Lockdown `none` → debugfs-Knoten (Patch 0003) auf diesem Host nutzbar; Verhalten unter Lockdown hier nicht messbar | gemessen |
| Ports | 0x62/0x66/0x68/0x6c nur durch `minisforum_n5_it5571`; 0x2e/0x4e frei, kein Super-I/O-Treiber geladen (it87/nct6775 als Modul verfügbar) | gemessen |
| ACPI/Firmware auf den EC-Ports | `PNP0C09` status=0, keine `ACPI: EC`-Zeile; DSDT/SSDT-Zugriffe und SMM nicht geprüft | teils offen |
| Modulversion | `modinfo` 0.2.0 (`MODULE_VERSION` upstream), Paket/DKMS 0.2.1, `/usr/src/minisforum-n5-it5571-0.2.1` | gemessen |
| Signatur | mit DKMS-Schlüssel **signiert**, Schlüssel **nicht eingeschrieben** → Prüfung schlägt fehl, Taint `OE`; Secure Boot aus. „MOK-signiert" war falsch | gemessen |
| EC-Firmware | `/sys/class/dmi/id/ec_firmware_release` = **0.14** (SMBIOS, gefahrlos lesbar); EC-RAM 0x00–0x03 `00 00 00 0E` passt dazu, nur bei entladenem Modul lesbar | gemessen |
| n5-fangov Unit | `WatchdogSec=60`, `Restart=always`, `ExecStopPost=failsafe`, `KillMode=control-group`; Host-Konfig `profile = "n5pro"` | gemessen |
| SIGKILL/Absturz | `ExecStopPost` setzt Safe-State (CPU/SSD auto, HDD 140); Hänger: Watchdog nach ≤ 3×Intervall + 60 s (~90 s); Neustart erzeugt kurzen 255-Stoß auf pwm1/2 | aus Code abgeleitet, am Host zu messen |
| pwm-Cache | Treiber liefert pwm/pwm_enable nur aus Cache → n5-fangov-Read-back-Prüfung wirkungslos, EC-Übernahme nicht erkennbar | aus Code abgeleitet |
| Profil `auto` | fehlt `experimental_write`, fällt n5-fangov bei `profile = "auto"` (Default) **still auf `monitor`** zurück: `check` besteht, keine Regelung. Den Referenzhost betrifft das nicht (`n5pro`) | aus Code abgeleitet |
| `versionLess` | `0.2.1+sr1` gilt als < `0.2.1`; `~rc` nicht unterscheidbar; kein Test | aus Code abgeleitet |
| EC-Tabellen | 4 Tripel-Tabellen, Format **(Solldrehzahl/100, T_oben, T_unten)** (M5, 09.10., EC-Code). A 0x10–0x27 = CPU (DCR1, Eingang EC 0x09), B 0x30–0x38 = SSD (DCR2, Eingang EC 0x05), C 0x70–0x78 = HDD DCR3, D 0x79–0x81 = HDD DCR4 (Eingang EC 0x06). Werte aus Konstanten beim EC-Start, OS kann sie nicht beschreiben. Die Setup-Variable „Fan 1–4 Setting“ ist vom OS nicht lesbar | abgeleitet (Firmware-Code), Messwerte 14.09. passen; am Gerät offen: Startphase, Override-Register, BIOS-User-Mode (M1) |
| PCIe 0x8c | erklärt: keine EC-Automatik für DCR5 (s. o.); „Kurvenpunkte überschrieben“ damit unwahrscheinlich, F1/M2 bestätigt am Gerät | abgeleitet |

## F8b-Baseline `baseline-20261008` (08.10. 22:25 – 15.10. 22:25)

TESTSPEC §5a, Lauf 1 (ungepatchter Treiber). Watcher `tools/validation-watch/`, 30-s-Sampler,
5-min-Check. Zusammenfassung `raw/f8b-baseline-20261008-abschluss.txt`; volle Rohdaten auf dem
Host unter `/var/lib/n5-validation/baseline-20261008/` (`samples.csv`, 20 061 Zeilen).

**Ergebnis: PASS — kein Fehler beobachtet in 7 Tagen unter diesen Bedingungen.**
Bedingungen: Treiberpaket `minisforum-n5-it5571-dkms` 0.2.1-1 (modinfo 0.2.0, srcversion
`96E49785…`, `experimental_write=Y`), Kernel `7.0.14-20-pve`, n5-fangov 0.4.2 regelt pwm1–3
(`pwm*_enable` 1), pwm4 EC-Automatik (2), BIOS 1.05, EC 0.14. Normaler Produktivbetrieb; die
Woche enthielt Sonntag 11.10. mit allen ZFS-Scrubs (00:24–13:38, `media` 13 h 14 min,
`data` 3 h 08 min, 0 Fehler) und dem wöchentlichen vzdump-Backup (ein Container, 03:00, 13 s; im
Journal der einzige Backup-Job im Zeitraum), außerdem Builds in einer Build-VM (09.10.) und täglich
~1 h Last aus einem Container (Gast A, s. Kennlinie). Abschlussmail 15.10. 22:30:17 („ABSCHLUSS … PASS“).

| K | Kriterium (TESTSPEC §5a) | Ereignisse gesamt / annotiert / unerklärt | Verdikt |
|---|---|---|---|
| K1 | Kernel-Log (Oops/BUG/Call Trace/hung task/soft lockup/RCU stall/watchdog/MCE/AER/NVMe-Reset/EC-Fehler) | 0 / 0 / 0 | PASS |
| K2 | Boot-ID-Wechsel | 0 / 0 / 0 (1 Boot-ID `58aeb7af…`, Uptime durchgehend 346 132 → 951 201 s) | PASS |
| K3 | n5-fangov inaktiv / Restart / Journal ≥ warning | 0 / 0 / 0 (`active` in 20 061 von 20 061 Samples, NRestarts 0) | PASS |
| K4 | Lüfter 0 rpm, Temperaturgrenzen, EC-Lesefehler | 0 / 0 / 0 (`ec_read_errors` 0; Maxima Tctl 73,0 · NVMe 50,8 · HDD 44 · EC-CPU 70 °C; fan1 1 417–4 455, fan2 2 513–3 147, fan3 1 623–1 972 rpm) | PASS |
| K5 | Sampler-Lücke > 90 s | 0 / 0 / 0 | PASS |
| K6 | Datenverzeichnis > 200 MB / Root-FS < 5 GB | 0 / 0 / 0 (Datenverzeichnis 3,4 MB) | PASS |
| K7 | Kernel-Taint verändert | 0 / 0 / 0 (12289 = P+O+E bei Start und Ende) | PASS |
| K8 | Watcher selbst (Dienst, Journal, Abdeckung < 99 %) | 0 / 0 / 0 | PASS |

| Abdeckung (K8) | Wert |
|---|---|
| Sampler | 100,00 % (20 052 Samples bis Soll-Ende, ungedeckt 0 s); 20 061 bis zum letzten Sample 22:30:13 |
| Check | 100,00 % (2 007 Läufe, ungedeckt 0 s) |
| Annotationen / geplante Ausfallzeit | keine |
| Timer nach Laufende | `systemctl list-timers --all 'n5vw-*'` → 0 Timer, Units `inactive/disabled` (geprüft 15.10. 23:16) |

Hinweis zur Statistik: `status.sh` nach Laufende meldet „Check läuft nicht“ und „Samples im
Zeitraum 99,0 %“ — Folge des Abschaltens (Statistik-Zeitraum bis „jetzt“), kein Befund.

Bedeutung für B10 (SMM-Restrisiko, Plan §7): Lauf 1 zeigt 0 EC-Lesefehler/Timeouts. Akzeptanz
erst, wenn Lauf 2 (gepatchter Treiber, 7 Tage) dasselbe zeigt.

## Wartungsfenster 1 (17.10.2026)

12:06–15:43, Betreiber am Gerät, Prüfvorschrift TESTSPEC 0.3. Getestet: Treiber `0.2.1+sr1~rc2`
(srcversion `E9CF5951A3238F3B9930797`), n5-fangov 0.5.0-rc2; Kernel `7.0.14-20-pve`, BIOS 1.05,
EC 0.14. Host im Produktivbetrieb (Gäste liefen, Betreiberentscheidung 17.10.: keine Gäste
stilllegen). Rohdaten `raw/*20261017*` (je `.sha256`, auf `n5host` gegen die Originale geprüft,
10/10). Verdikte nach TESTSPEC 0.3; Schritte ohne TESTSPEC-Test (Checklisten B/C) mit PASS/FAIL
gegen die Erwartung der Laufkarte.

### Block A — Ausgangszustand, Kaltstart

| Schritt | Verdikt | Beleg |
|---|---|---|
| A1 Regler aus | PASS | Failsafe CPU/SSD auto, HDD 140 |
| A2 Autoload aus | PASS (Maßnahme unzureichend) | Datei entfernt, Modul lud nach dem Kaltstart trotzdem — über den DMI-Modalias (udev), nicht über `systemd-modules-load`. Kein Schreibzugriff seit Boot (Regler aus, `pwm1–4_enable` = 2). Ohne Autoload braucht es eine `modprobe.d`-Blacklist (in F2 so gemacht, wirkt) |
| A3 Kaltstart | PASS | ≥ 30 s stromlos, SATA 4/4, Pools healthy |
| A4 T6-kalt / M1 (Modus A) | REPORTED | `RESULT: complete`; EC-RAM-Dumps gleich, Kurventabellen identisch; TACH 2489/2232/1659 rpm; EC CPU 61 / System 41 / Board 32 / Ambient 21 °C; Tctl 62,5 °C (Gäste starten). `raw/f8a-snapshot-cold-20261017T102943Z.txt` |

### Block B — Treiber rc2

| Schritt | Verdikt | Beleg |
|---|---|---|
| B1 Installation | PASS | DKMS für 7.0.12-1/-19/-20 installed, kein Restart-Marker; postinst lädt das Modul und legt die Autoload-Datei neu an (Paketdatei) |
| B2 Identität (0001/0002/0007) | PASS | Version/srcversion = Build; neue Parametertexte; „WRITES ENABLED"; kein `fan*_min`/`fan4`; 3 fan, 4 temp, 4 pwm |
| B3 `experimental_write=0` | PASS | keine pwm-Knoten, dmesg nennt den Grund |
| B4 `force=1` | PASS | ohne Zusatzwirkung auf erkanntem Board |
| B5 debugfs `dcr` (0003) | PASS / Teil offen | `dcr` 3× = `n5_i2ec_read 0x1800 16` = `dcr` nach Reload, byteidentisch; 0x2e/0x2f danach frei. Belegung **während** des Lesens nicht abgreifbar (zu kurz) |
| B6 ohne `debug_regs` | PASS | kein debugfs-Verzeichnis |

### Block C — n5-fangov 0.5.0-rc2, Gates

| Schritt | Verdikt | Beleg |
|---|---|---|
| C1 Installation | PASS, Befund | postinst aktiviert den deaktivierten Dienst wieder (`systemctl enable` bei jedem configure; nicht neu in rc2) |
| C2 `identify` | PASS | DMI inkl. `sys_vendor`, EC 0.14, Treiber + srcversion, `dcr`, hw_status `experimental`. Status-Notiz veraltet (nennt laufende F8b-Baseline) |
| C3 `check` | PASS | Exit 0, Warnungen experimental + `debug_regs` |
| C4 Gate read-only | PASS, Hinweis | FAIL „driver loaded read-only", 1 Alarm `driver` (Rest unterdrückt), nach 5 Starts `failed` + Alarm. onfailure-Text nennt „exited with code 0", tatsächlich scheiterte `ExecStartPre` mit 1 |
| C5 Modul fehlt | PASS (Erwartung präzisiert) | FAIL „device not found …", Exit 1. „N5 EC driver missing" (`ErrDriverMissing`) kommt nur bei `profile = "auto"`; Host hat `profile = "n5pro"` |
| C6 Start, Lock | PASS, Befund | `writer.lock` = `serve pid …`, `test pwm3` abgelehnt; DCR3 = DCR4. Im ersten Zyklus „EC register deviation" für alle Kanäle (z. B. DCR1 116 statt 188), Sekunden später `dcr` = Schreibwerte. Lesezeitpunkt vor Übernahme durch den EC (Z1/Z2), keine dauerhafte Abweichung |
| C7 G5 remote | PASS | Daemon läuft → abgelehnt (G1); stdin kein Terminal → abgelehnt; `ssh -tt` → abgelehnt („session is remote"), Override `--i-am-at-the-console` by design |
| C8 G1 im Neustart | PASS / Teil offen | `validate` abgelehnt „running or (re)starting". Failsafe fehlte im Journal, weil `systemctl kill` auch den Control-Prozess traf (Testartefakt); Failsafe nach Kill des Hauptprozesses in E3 belegt |
| C9 Stopp | PASS | Failsafe, Lock leer |

### Block D — `validate` T0/T1/T6

| Schritt | Verdikt | Beleg |
|---|---|---|
| D1 pwm1 | T0 PASS, **T1 INCONCLUSIVE** (Gate G4), T6 REPORTED | G1/G2/G3/G5 PASS (G3 load 0,33), **G4 FAIL: Tctl-Spanne 13,5 °C** (Grenze < 5), EC-System 0,0 °C, HDD 0,0 °C; G6 PARTIAL. T1 nicht gelaufen, nichts geschrieben. Bündel verloren (Ausgabeordner fehlte, s. u.) |
| D1 pwm2–pwm4, D2–D4 | nicht gelaufen | → Fenster 2 |

Last vor D (Normalbetrieb): load 0,4–0,56, Tctl sekündlich 35–43 °C durch kurze Lastspitzen der
Gäste (Healthchecks); 1-min-Mittel der Gäste wie in den 12 h davor. Folgerungen:

- **G4-Tctl auf einem Host im Produktivbetrieb nicht erfüllbar:** die Spanne misst Rauschen
  des Tctl-Sensors, die EC-Eingänge (0x04, HDD) standen. Umgesetzt in TESTSPEC 0.4 (#20, `docs/TESTSPEC.md`, freigegeben 2026-10-17).
- **`validate` legt das Bündel erst nach dem Lauf an** (`os.Mkdir` ohne Elternordner) → bei
  fehlendem `--out`-Ordner gehen die Ergebnisse verloren, bei gelaufenem T1 auch die
  Schreibtest-Rohdaten.

### Block E — F8a Stress und Fehlerinjektion

| Schritt | Verdikt | Beleg |
|---|---|---|
| E1 load (50× Reload) | PASS | 50/50, 0 Kernel-Log-Treffer, `enable`=2 und fan1–3 > 0 nach jedem Reload, Schreibfehler nur ENOENT/ENODEV (0004); max Tctl 56,2 / NVMe 42,8 / HDD 39,0 °C. Danach DCR1/2 0xf9 (letzter Write), 1 min später EC-Automatik. `raw/f8a-stress-load-20261017T110418Z.txt` |
| E2 unbind (50×) | PASS | 50/50, 0 Treffer; max Tctl 58,7 / NVMe 44,8 / HDD 41,0 °C. DCR5 blieb 0x00: Unbind gibt nur berührte Kanäle zurück, E2 schreibt pwm4 nicht (Laufkarten-Erwartung „PCIe 255" war falsch). `raw/f8a-stress-unbind-20261017T114401Z.txt` |
| E3 `kill -9` (3×) | PASS | Failsafe (ExecStopPost; CPU/SSD auto, HDD 140) nach 56/66/63 ms, READY nach 5,5–5,6 s. DCR3-Meldung „106 after writing 140" = Lesezeitpunkt wie C6 |
| E4 Lüfter abstecken | N/A | Stecker ohne Öffnen des Gehäuses nicht erreichbar; Stall-Alarm am Gerät ungeprüft |
| E5 Latenz | REPORTED | Regler gestoppt. Read `fan1_input` median 4,21 / p95 5,22 / max 6,09 ms; Write `pwm1` median 15,80 / p95 17,29 / max 20,22 ms (davon ~6,7 ms `sudo tee`); keine EC-Timeouts |

### Block F — Shutdown-Pfad, PCIe, T6-warm

| Schritt | Verdikt | Beleg |
|---|---|---|
| F1 PCIe / M2 | **FAIL** (Skript), annotiert | DCR5 b0 0x00, b1/b2 0xa7, **c +0 s 0xa7**, c +60 s 0xff, c +300 s 0xff → `hand-back check … FAIL`. Kurventabellen 0x10–0x27/0x30–0x38/0x70–0x81 vorher = nachher, EC-RAM unverändert, 0 Kernel-Log-Treffer → **M2: der Treiber ändert keine EC-Kurven.** Annotation Z1: der +0-s-Punkt liest vor der Übernahme durch den EC; Treiber schreibt 255 korrekt. Kein PASS — Wiederholung mit Settle-Zeit (TESTSPEC 0.4) offen. `raw/f8a-pcie-m2-20261017T120056Z.txt`, `raw/f8a-snapshot-pcie-before-20261017T120057Z.txt`, `raw/f8a-snapshot-pcie-after-20261017T120615Z.txt` |
| F2 Warmstart nach manuellem pwm1/pwm3 | PASS (Wirkung nicht direkt belegbar) | Blacklist statt Autoload-Datei; pwm1 = 150, pwm3 = 180; keine „not set to"-Warnung im Vorboot-Journal. `.shutdown` loggt bei Erfolg nichts; DCR5 stand schon vor dem Reboot auf 0xff (Z1) → Wirkung von `.shutdown` auf PCIe nicht von F1/Z1 trennbar, auf HDD vom BIOS überschrieben (F3) |
| F3 T6-warm | REPORTED | SATA 4/4, Modul nicht geladen (Blacklist wirkt). Modus A: DCR1/2 0x62/0x4b (Automatik), **DCR3/4 0x64/0x64**, **DCR5 0xff**; nach Laden: 6b/4b/64/64/ff. `raw/f8a-snapshot-warm-20261017T133314Z.txt`, `raw/f8a-snapshot-warm-loaded-20261017T133332Z.txt` |
| F4 T6-kalt nach Treiber-Shutdown | N/A | durch A3/A4 abgedeckt (HDD 140 vor Stromtrennung → 0x64, DCR5 0x00); Betreiber 17.10. |

**T6 zusammen (A4, F3; je ein Kalt- und ein Warmstart):**

| Kanal | Kaltstart | Warmstart |
|---|---|---|
| DCR1/2 (CPU/SSD) | EC-Automatik | EC-Automatik |
| DCR3/4 (HDD) | 0x64 (vorher 140) | 0x64 (vorher manuell 180, `.shutdown` 255) |
| DCR5 (PCIe) | 0x00 | 0xff (= Wert vor dem Reboot) |

BIOS/EC setzen HDD bei beiden Boots auf 100; PCIe behält den letzten Wert über einen Warmstart.
Damit ist Entscheidung 2 (PCIe 255 beim Shutdown/Hand-back) begründet: ohne sie bliebe ein
niedriger PCIe-Wert über einen Warmstart stehen.

### Zusatzmessungen (kein Verdikt)

| Messung | Ergebnis | Rohdatei |
|---|---|---|
| Z1 DCR5-Latenz (alle 0,2 s) | Write 167 → DCR5 0xa7 nach ≤ 215 ms; Rückgabe (`pwm4_enable=2`) → 0xff nach ≤ 212 ms, danach 60 s stabil. PCIe springt ohne Rampe | `raw/z1-dcr5-latenz-20261017T130459Z.txt` |
| Z2 DCR3/4 (alle 0,2 s) | `pwm3_enable=1` (Treiber 255): DCR3 nach ~1,5 s, DCR4 nach ~4,6 s auf 0xff — **nacheinander**. Write 180: DCR4 nach ~1,7 s, DCR3 nach ~4,8 s. Rückgabe (0x2b): EC senkt DCR3 und DCR4 **abwechselnd** in Schüben von ~4 je 0,22 s; nach 15 s 0x72/0x6a (fan3 2736 → 2039 rpm), Ende des Logs, noch nicht eingeschwungen | `raw/z2-dcr34-hdd-20261017T133518Z.txt` |

Folgen:

- Registerprüfungen direkt nach einem Write lesen Zwischenstände: PCIe ≤ 0,2 s, HDD mehrere
  Sekunden je Register. Erklärt C6/E3 („deviation") und F1 (c +0 s).
- **M5 (HDD-Regelung durch den EC nach 0x2b) jetzt durch Z2 gestützt:** gemessen ist, dass der EC
  nach der Rückgabe DCR3/4 selbsttätig in Stufen vom Schreibwert weg bewegt (Rampe), passend zu
  Upstream-#7. Nicht gemessen: Endwert, Solldrehzahl und Eingang EC 0x06 — die bleiben aus der
  Firmware abgeleitet.

### Befunde aus Fenster 1

| Befund | Bereich | Schwere | Kurz |
|---|---|---|---|
| A2 | Doku/Laufkarte | – | Autoload über DMI-Modalias; Unterdrücken nur per Blacklist |
| C1 | n5-fangov Paket | low | postinst `systemctl enable` bei jedem configure |
| C4 | n5-fangov | low | onfailure-Text nennt falschen Exit-Grund |
| C6/E3 | n5-fangov | low | DCR-Vergleich ohne Settle-Zeit |
| D1-a | TESTSPEC | medium | G4-Tctl-Kriterium im Produktivbetrieb nicht erfüllbar |
| D1-b | n5-fangov | medium | Bündelordner erst nach dem Lauf, ohne Elternordner |
| F1 | f8a / TESTSPEC | low | c +0 s-Punkt ohne Settle-Zeit |
| F2 | Treiber (sr2) | low | `.shutdown` ohne Erfolgs-Logzeile |
| G3 | HA (Verbraucher) | low | REST-Sensoren erholen sich nach Start ohne erreichbare Quelle nicht von selbst |
| G5 | validation-watch | low | `install.sh` Exit 1 nach erfolgreichem Start (`pipefail` + fehlendes `outbox`) |

### Block G — Abschluss, Start F8b-Lauf 2

| Schritt | Verdikt | Beleg |
|---|---|---|
| G1 Autoload zurück, Modul ohne `debug_regs` | PASS | Blacklist entfernt, Autoload-Datei unverändert (sha256) zurück; kein debugfs |
| G2 n5-fangov 0.5.0-rc2 Dauerbetrieb | PASS | active/enabled, `check` Exit 0 (Warnung experimental), Journal ohne `warning`; Statuszeile auf Info (Entscheidung 9) |
| G3 API-Verbraucher | PASS, Hinweis | API `status ok` + 3 Kanäle; HA-REST-Sensoren erst nach `rest.reload` mit Werten |
| G4 Rückweg n5-fangov 0.4.2 | PASS | Downgrade, Konfig = Stand vorher, `check` all good, `check --after-update` Exit 0 (Modul für 3 Kernel), Journal ohne Warnungen |
| G5 F8b-Lauf 2 `patched-20261017` | gestartet | START-Mail laut Journal versandt, erster Check `success`, Ende 24.10.2026 15:41. Treiber rc2 `E9CF5951…`, n5-fangov 0.4.2, Kernel 7.0.14-20. `install.sh` endete mit Exit 1 (Befund G5) |
| G6 Rohdaten | PASS | 10 Rohdateien gegen `.sha256` geprüft, hier unter `raw/` |

**Offen für Fenster 2:** T1/D3/D4 (alle Kanäle, nach TESTSPEC 0.4 mit produktivtauglichem G4),
E4 (Stall-Alarm, Testidee ohne Abstecken), T2/T3 (pwm1–3), F1 mit Settle-Zeit.

## F8b-Lauf 2 `patched-20261017` (17.10. 15:41 – 24.10. 15:41)

TESTSPEC 0.4 §5a, Lauf 2 (gepatchter Treiber), Vergleich mit Lauf 1 nach §5a „Runs“ Nr. 1–4.
Ausgewertet 31.10.2026, nur lesend. Zusammenfassung `raw/f8b-patched-20261017-abschluss.txt`
(`.sha256`); Rückprüfung `WARNING:` `raw/k1-retro-warning-f8b-*-20261031T162822Z.txt` (je
`.sha256`, gegen die Originale auf `n5host` geprüft). Volle Watcher-Rohdaten (`samples.csv`,
20 061 Zeilen, Berichte, Konfig) anonymisiert mit `SHA256SUMS` unter
[`runs/20261017-1541-F8NAA-1.05-f8b/`](runs/20261017-1541-F8NAA-1.05-f8b/), Lauf 1 unter
[`runs/20261008-2225-F8NAA-1.05-f8b/`](runs/20261008-2225-F8NAA-1.05-f8b/).

**Ergebnis: PASS — kein Fehler beobachtet in 7 Tagen unter diesen Bedingungen.**
Lauf 2 ist für sich PASS (K1–K8 je 0 Ereignisse, Lauf vollständig) und hat in keinem K mehr
unerklärte Ereignisse als Lauf 1 (beide 0). Abschlussmail 24.10. 15:44:36 („ABSCHLUSS … PASS“),
`final.done` `2026-10-24T15:44:33+0200 PASS`; Timer `n5vw-*` danach `disabled`, 0 Units geladen
(geprüft 31.10.).

### Bedingungen

| | Lauf 1 `baseline-20261008` | Lauf 2 `patched-20261017` | gleich? |
|---|---|---|---|
| Zeitraum | 08.10. 22:25:44 – 15.10. 22:25:44 (Do–Do) | 17.10. 15:41:54 – 24.10. 15:41:54 (Sa–Sa) | 7 Tage ✅ |
| Treiber | Paket 0.2.1-1 (modinfo 0.2.0, srcversion `96E49785…`) | Paket `0.2.1+sr1~rc2-1`, srcversion `E9CF5951A3238F3B9930797`, `debug_regs=N`, `force=N` | **nein (Prüfgegenstand)** |
| `experimental_write` | Y | Y | ✅ |
| Kernel / Taint | `7.0.14-20-pve` / 12289 | `7.0.14-20-pve` / 12289 | ✅ |
| n5-fangov | 0.4.2, pwm1–3 geregelt (`enable` 1), pwm4 EC (2) | 0.4.2 (Rückweg 17.10. 15:39 von 0.5.0-rc2), Start 15:40:12, gleiche Kanalbelegung | ✅ |
| BIOS / EC | 1.05 / 0.14 | 1.05 / 0.14 | ✅ |
| Watcher-Schwellen | `config.snapshot` `b44b76a1…` | `config.snapshot` `f1102d53…` | ✅ ab Schwellwertblock zeilengleich (`diff` leer); Unterschied nur Kopf (RUN_ID, Start, Boot-ID) |
| Sampler / Check | 30 s / 300 s | 30 s / 300 s | ✅ |
| Boot | eine Boot-ID `58aeb7af…`, Laufbeginn bei Uptime 346 132 s (4 Tage) | eine Boot-ID `1c5be8a1…`, **Laufbeginn 614 s nach einem Warmstart** (Ende Fenster 1, `reboot.target` 15:31:08) | **nein** |
| Paketänderungen im Lauf | keine (apt-Historie leer) | **21.10. 20:04 und 20:11 Sicherheitsupdates** (s. u.) | **nein** |
| ZFS-Scrub (2. Sonntag) | 11.10. 00:24–13:38 (`media` 13 h 14 min, `data` 3 h 08 min, 0 Fehler) | keiner (nächster 08.11.) | **nein** (#16, Variante a1, hingenommen) |
| vzdump-Backup (Sonntag) | 11.10. 03:00, ein Container, 13 s | 18.10. 03:00, derselbe Container, 13 s | ✅ |
| Lastepisode Gast A | ~08:55 täglich (09., 11.–15.10.), dazu 20:55 (09., 15.10.); 4–127,5 min | ~03:55 (18., 19., 22.–24.10.), dazu 15:55 (18., 23.10.); 2,5–26 min | vorhanden ✅, Lage und Länge **verschieden** |
| VM-Neustart | ein VM-Gast gestoppt/gestartet (Bridge-/tap-Meldungen im Kernel-Log), 1× `kvm … Unhandled WRMSR` | keiner im Kernel-Log | nein (Gastbetrieb) |
| Raumtemperatur | nicht in den Watcher-Daten; EC 0x06 Tagesmittel 21,1–22,8 °C (min 20, max 26) | EC 0x06 Tagesmittel 21,2–22,2 °C (min 21, max 25) | vergleichbar (abgeleitet über EC 0x06, §6 HDD) |

### K1–K8 (gesamt / annotiert / unerklärt)

| K | Kriterium | Lauf 1 | Lauf 2 | Bewertung |
|---|---|---|---|---|
| K1 | Kernel-Log (`K1_PATTERN`, ohne `WARNING:`) | 0 / 0 / 0 | 0 / 0 / 0 | gleich; Rückprüfung `WARNING:` beide 0 (s. u.) |
| K2 | Boot-ID-Wechsel | 0 / 0 / 0 | 0 / 0 / 0 (Uptime 614 → 605 571 s) | gleich |
| K3 | n5-fangov inaktiv / Restart / Journal ≥ warning | 0 / 0 / 0 | 0 / 0 / 0 (`active` 20 061/20 061, NRestarts 0, Journal ≥ warning leer) | gleich |
| K4 | Lüfter 0 rpm, Temperaturgrenzen, EC-Lesefehler, hwmon fehlt | 0 / 0 / 0 | 0 / 0 / 0 (`ec_read_errors` 0 in allen Samples) | gleich |
| K5 | Sampler-Lücke > 90 s, Uhrsprung, veraltet | 0 / 0 / 0 | 0 / 0 / 0 (Abstand 29–31 s) | gleich |
| K6 | Datenverzeichnis / Root-FS | 0 / 0 / 0 (3,4 MB) | 0 / 0 / 0 (3,4 MB) | gleich |
| K7 | Kernel-Taint | 0 / 0 / 0 (12289) | 0 / 0 / 0 (12289) | gleich |
| K8 | Watcher (Dienst, Journal, Abdeckung) | 0 / 0 / 0 | 0 / 0 / 0 | gleich |

| Abdeckung / Dienste | Lauf 1 | Lauf 2 |
|---|---|---|
| Sampler | 100,00 % (20 052 bis Soll-Ende, ungedeckt 0 s) | 100,00 % (20 055 bis Soll-Ende, ungedeckt 0 s); 20 061 bis 15:44:32 |
| Check | 100,00 % (2 007 Läufe) | 100,00 % (2 007 Läufe) |
| `n5vw-*` NRestarts | 0 | 0 |
| n5-fangov NRestarts (max) | 0 | 0 |
| `ec_read_errors` | 0 (max) | 0 (max) |
| Annotationen | keine | keine |

### Temperatur- und Lüfterkennwerte (Watcher-Samples, gemessen)

| Wert | Lauf 1 min / max / Mittel | Lauf 2 min / max / Mittel |
|---|---|---|
| Tctl °C | 32,8 / 73,0 / 38,8 | 33,1 / 74,7 / 37,6 |
| EC CPU (0x09) °C | 32 / 70 / 37,9 | 32 / 73 / 36,7 |
| EC System / Board / Ambient °C | 29–50 / 24–39 / 20–26 | 31–50 / 25–39 / 21–25 |
| NVMe max °C | 41,8 / 50,8 / 42,9 | 41,8 / 49,8 / 42,7 |
| HDD max °C | 39 / 44 / 39,5 | 39 / 42 / 39,5 |
| fan1 / fan2 / fan3 rpm | 1 417–4 455 / 2 513–3 147 / 1 623–1 972 | 1 406–4 802 / 2 518–3 054 / 1 676–1 836 |
| pwm1 / pwm2 / pwm3 (gecacht) | 60–216 / 94–125 / 99–119 | 60–226 / 94–122 / 103–110 |
| pwm4 / `pwm4_enable` | 255 / 2 | 255 / 2 |
| load1 max / Mittel | 7,4 / 0,7 | 3,4 / 0,6 |
| Samples Tctl ≥ 50 / ≥ 60 / ≥ 70 | 1 101 (5,49 %) / 883 / 6 | 277 (1,38 %) / 172 / 5 |
| Samples fan1 ≥ 3 000 / ≥ 4 000 rpm | 1 021 (5,09 %) / 4 | 241 (1,20 %) / 8 |
| Samples load1 ≥ 2 | 1 664 (davon 11.10. 00–13 Uhr ~120/h: Scrub) | 39 |
| Episoden Tctl ≥ 55 °C, ≥ 2 min | 19, zusammen 441,5 min | 12, zusammen 87,0 min |
| Mittel ohne erste 60 min: Tctl / fan1 / load1 | 38,8 / 1 778 / 0,74 | 37,5 / 1 683 / 0,55 |

Die Maxima von Lauf 2 (Tctl 74,7 °C, fan1 4 802 rpm, EC CPU 73 °C) liegen in der Startphase nach
dem Warmstart: 17.10. 15:41–16:00 (Uptime 614–1 830 s) fan1 3 428–4 802 rpm, Tctl 61,5–74,0 °C,
ab 16:02 Ruhe (fan1 1 760, Tctl 41,5); Tctl-Max 17:02:57. Lauf 1 hatte keine Startphase. Alle
Werte unter den K4-Grenzen (Tctl 85, NVMe 70, HDD 50, EC CPU 90 °C).

### Rückprüfung `WARNING:` (TESTSPEC 0.4 #15)

`tools/validation-watch/retro-k1-warning.sh` 0.1.0, je Lauf mit `--run-dir` (Fenster
`START_EPOCH` + 7 Tage, K1-Muster aus dem `config.snapshot` des Laufs), 31.10. 16:28:22Z:

| Lauf | Kernel-Zeilen im Fenster | Treffer `WARNING:\|cut here` | davon außerhalb K1 (NEW) | Journal vollständig | Exit |
|---|---|---|---|---|---|
| 1 `baseline-20261008` | 84 | 0 | 0 | ja | 0 |
| 2 `patched-20261017` | 59 | 0 | 0 | ja | 0 |

Die Kernel-Zeilen beider Fenster sind fremd: Lauf 2 53× AppArmor-`DENIED` (LXC-Gäste), 2× `perf:
interrupt took too long`, 2× Journal-Vacuum, je 1× `kauditd`/`hrtimer`; Lauf 1 49× AppArmor,
Bridge-/tap-Meldungen eines VM-Neustarts, 1× `kvm … Unhandled WRMSR`, 1× `perf`. Zeilen mit
`minisforum` oder EC-Timeout: 0 in beiden Läufen.

### B10 (SMM-Restrisiko, §5a „Runs“ Nr. 3)

`ec_read_errors` = 0 in beiden Läufen, Lauf 2 ohne `EC timeout`-Zeile im Kernel-Log →
**Bedingung für die Annahme von B10 erfüllt.** Der gepatchte Treiber würde EC-Timeouts loggen,
die 0.2.1 nicht ausgab; Lauf 2 hätte damit mehr sehen können als Lauf 1 und hat 0 gesehen.

### Abweichungen von „nur der Treiber ist anders“ (§5a „Runs“ Nr. 4)

| Abweichung | Beleg | Wirkung auf das Urteil |
|---|---|---|
| Sicherheitsupdates 21.10. 20:04 (`ca-certificates`) und 20:11 (`cifs-utils`, `curl`/`libcurl*`, `xz-utils`/`liblzma*`, `libexpat1*`, `librsvg2-*`, `libjxl0.11`, `linux-libc-dev`) | `/var/log/apt/history.log`; kein Kernel, Treiber, n5-fangov, DKMS; kein Reboot (Boot-ID `1c5be8a1…` durchgehend), Taint unverändert | keine erkennbar; Lauf 2 gilt (Betreiber 31.10.) |
| Kein Monats-Scrub in Lauf 2 (Lauf 1: 11.10.) | `zpool status`: letzter Scrub 11.10.; Journal ohne `scrub_*` im Fenster | hingenommen (#16, Variante a1); Lauf 2 hatte weniger I/O- und Dauerlast. Ein 2. Sonntag mit gepatchtem Treiber steht aus |
| Laufbeginn 614 s nach einem Warmstart (Lauf 1: 4 Tage Uptime) | `uptime_s` erstes Sample; `--list-boots`, `reboot.target` 15:31:08 | Lauf 2 enthält die Gast-Startphase (18,5 min erhöhte Last) |
| Lastepisode Gast A auf ~03:55/15:55 verschoben, kürzer (2,5–26 statt 4–127,5 min) | Episoden aus `samples.csv` | weniger Zeit über den CPU-Stufen 55/65 °C; Ursache der Verschiebung (Neustart des Gasts mit dem Host) abgeleitet, nicht geprüft |
| Ein VM-Neustart nur in Lauf 1 | Kernel-Log Lauf 1 (Bridge-/tap-Meldungen) | Gastbetrieb, kein Kriterium |
| Wochentage Do–Do statt Sa–Sa | Start-/Endzeit | je ein Sonntag mit Backup |

Lauf 2 war damit insgesamt **weniger belastet** als Lauf 1 (Tctl ≥ 50 °C 1,38 % statt 5,49 %
der Samples, load1 ≥ 2 39 statt 1 664 Samples) — gemessen. Das Urteil gilt für diese Last.

### Urteil (Formulierung nach TESTSPEC §1 Regel 3; gelaufen nach 0.3, Vergleich nach 0.4 §5a)

**On board N5 PRO / F8NAA, BIOS 1.05, EC 0.14, test F8b v0.3: PASS (1 run)** — Treiber
`0.2.1+sr1~rc2` (srcversion `E9CF5951…`), Kernel `7.0.14-20-pve`, n5-fangov 0.4.2,
17.10.–24.10.2026: kein Fehler beobachtet in 7 Tagen unter diesen Bedingungen. Vergleich mit
Lauf 1 (Treiber 0.2.1, ebenfalls PASS): in keinem K mehr unerklärte Ereignisse (0 gegen 0).
Einschränkungen: K1-Muster ohne `WARNING:` (Rückprüfung 0 Treffer), kein Monats-Scrub, Lauf 2
leichter belastet als Lauf 1.

Watcher-Rohdaten beider Läufe liegen anonymisiert mit `SHA256SUMS` unter `befunde/runs/`
(abgelegt 31.10.2026, [`runs/README.md`](runs/README.md)).

**Offen:** ein Lauf mit gepatchtem Treiber über einen 2. Sonntag. Nicht mehr geplant
(Release ohne weiteren Beobachtungslauf, Entscheidung 01.11.).

## Wartungsfenster 2a (01.11.2026)

Treiber `0.2.1+sr1~rc3` (srcversion `D1E4D4D188041A61EC3D872`), Kernel `7.0.14-26-pve`,
n5-ectest 0.1.0-mw2 (TESTSPEC 0.4), n5-fangov 0.5.0-rc4. Kaltstart vor Block A (≥ 30 s stromlos).
Laufkarte `laufkarte-wartungsfenster-2.md` (nicht im Repo). Rohdaten `raw/*20261101*`, Bündel
`runs/20261101-*` (je `.sha256`/`SHA256SUMS`, gegen die Originale auf `n5host` geprüft).

| Punkt | Ergebnis | Beleg |
|---|---|---|
| Autoload bei Blacklist | Blacklist in `modprobe.d` reicht auch bei vorhandener `modules-load.d`-Datei (`deny-listed (by kmod)`, Modul nicht geladen) | Journal des Boots, nicht im Repo |
| Kurventabellen nach Kaltstart | 0x10–0x27, 0x30–0x38, 0x70–0x81 byte-gleich mit 17.10. | `raw/f8a-snapshot-cold-20261101T143156Z.txt` |
| Z3 DCR1/DCR2-Latenz (kein Verdikt) | Write → DCR nach ≤ 0,22 s; Rückgabe (`enable=2`) rampt ≈ 20 Stufen/s, 180 → Kurvenwert in 4,9 s (pwm1) / 5,3 s (pwm2) | `raw/z3-dcr12-latenz-20261101T143353Z.txt` |
| T1 pwm1 | **PASS**; Zweitumsetzung (n5-fangov `validate`) gleiches Verdikt, DCR je Stufe gleich, Tacho-Mittel Δ ≤ 6 rpm (Regel-4-Bedingung #1) | `runs/20261101-1547-*`, `runs/20261101-1631-*` |
| T1 pwm3 | **FAIL** „another tach follows“: fan2 max 2213 rpm bei pwm3 = 255 (Ruhe 2155; Mittel +43/+26/+20/+11 je Stufe), DCR2 im ganzen Lauf 75. Abgeleitet: Luftstromkopplung, keine Kanalverwechslung. DCR3 = DCR4 = Schreibwert in jedem bewerteten Sample | `runs/20261101-1604-*` |
| T1 pwm2, pwm4 | **INCONCLUSIVE**, Gate G4: EC 0x04 wechselt im 5-min-Fenster zwischen 29 und 30 °C (Ganzzahl). Kein Trend | `runs/20261101-1557-*`, `-1617-*`, `-1637-*`, `-1702-*` |
| T1 pwm1, erster Versuch | INCONCLUSIVE, G4: HDD +1 °C kurz nach dem Kaltstart | `runs/20261101-1512-*` |
| F1 (PCIe-Rückgabe) | **COMPLETE**: DCR5 0xff an +1/+60/+300 s, Kurventabellen vor/nach identisch, 0 Kernel-Log-Treffer | `raw/f8a-pcie-m2-20261101T172305Z.txt` |
| Snapshot mit Treiber (T6) | REPORTED; Kennzeichnung `INCOMPLETE (fan4_input unreadable)` ist ein Werkzeugfehler (PCIe ohne Tacho), Daten vollständig | `runs/20261101-1721-*` |

Lücken in TESTSPEC 0.4, die dieses Fenster gezeigt hat (Korrektur in 0.5, ohne Nachläufe):
G4 bei ganzzahligen EC-Werten, T1 wertet Luftstromkopplung als Kanalfehler, G3 absolut statt
relativ zur Threadzahl. D3 (Abbruchtest) und T2/T3 nicht gelaufen.

## Build `0.2.1+sr2` (02.11.2026, ohne Hardware)

Treiber `0.2.1+sr1` + `0008` (DMI `N5 PRO`/`F8NAB` read-only), Paket ohne `modules-load.d`,
postinst `modprobe -b`. Gebaut gegen `7.0.14-26-pve` und `7.0.12-1-pve` im Container auf dem
Build-Host; Referenzhost nur gelesen. Protokoll `raw/sr2-build-20261102.txt`.

| Punkt | Ergebnis | Beleg |
|---|---|---|
| Statische Prüfung | plain, W=1, sparse, smatch ohne Meldung (beide Kernel); checkpatch 0/0, `--strict` 6 CHECKs wie sr1; srcversion `CDDEDE26904251994A0DE35` | Abschnitte F1–F7 |
| Delta zu sr1 | nur Änderungsvermerk, Kopfkommentar, Profil + DMI-Eintrag F8NAB, Probe-Fehlertext, `MODULE_VERSION` | Abschnitt D |
| Alias-Load ohne `modules-load.d` | DMI-Modalias des Referenzhosts passt weiter auf zwei Aliase (`svn…pn*N5PRO*:rn*F8NAA*`, `pn*N5*:rn*F8NAA*`) → udev lädt beim Boot. Nicht per Neustart geprüft | Abschnitt A |
| `modprobe -b` (#50) | echtes kmod: gesperrtes Modul → Exit 0, nicht geladen; per Name → Ladeversuch. postinst prüft deshalb `lsmod` und meldet die Sperre | T1 |
| `test-deb.sh` | PASS (inkl. Blacklist-Fall, keine `modules-load.d`-Datei im Paket) | T1 |

### Einspielen auf dem Referenzhost (02.11.2026, 20:53)

Paket per `apt install`, Modul neu geladen, Lücke ohne Regler ≈ 1 s. Prüfung danach nur lesend.
Für diese Tabelle gibt es kein Rohprotokoll im Repo; sie gibt die Prüfung am Host wieder.

| Punkt | Ergebnis |
|---|---|
| Modul | `0.2.1+sr2`, srcversion `CDDEDE26904251994A0DE35` |
| DKMS | sr2 für alle 4 installierten Kernel gebaut |
| `modules-load.d` | Datei unter `/usr/lib/modules-load.d/` entfernt |
| Alias | `pn*N5PRO*:rn*F8NAB*` vorhanden |
| Regler | n5-fangov aktiv, `NRestarts=0`; `check` bestanden, nur Warnung `experimental` |
| Kernel-Log | nur die zwei erwarteten Zeilen |
| Autoload | nach echtem Neustart nicht geprüft, nur per Modalias belegt |

## Build `0.2.1+sr3` (09.11.2026, ohne Hardware)

`sr2` + `0009` (Lese-Cache `temp*_input`/`fan*_input`, BACKLOG #28; Entwurf r2 vom 17.10. auf
die released Serie umgesetzt, ohne den `ecram`-Entwurf). Gebaut gegen `7.0.14-26-pve`
(läuft), `7.0.14-28-pve` (installiert, nicht gebootet) und `7.0.12-1-pve` im Container auf dem
Build-Host; Referenzhost nur gelesen. Protokoll `raw/sr3-build-20261109.txt`.

| Punkt | Ergebnis | Beleg |
|---|---|---|
| Ausgangsmessung sr2 (09.11. 18:14, Referenzhost, unprivilegierter Benutzer, ein Prozess, 2 × 200 Lesezugriffe) | jedes Lesen ein EC-Zugriff: `temp1_input` Mittel ≈ 1,55 ms (max 3,2 ms), `fan1_input` ≈ 3,9 ms (max 9,1 ms) | Messschleife, Werte im Patch-Kopf 0009 |
| Statische Prüfung | plain, W=1, sparse, smatch ohne Meldung (drei Kernel); checkpatch 0/0, `--strict` 6 CHECKs wie sr2; srcversion `1F34C0D951CB0937C489768` | Abschnitte F1–F7 |
| Delta zu sr2 | nur Änderungsvermerk-Datum, `MODULE_VERSION`, `0009`; Code von 0009 zeilengleich mit Entwurf r2 | S1, D |
| Harness | h9 (Cache) 58/58, ASan/UBSan sauber; Gegenprobe ohne Cache 18 Fehlschläge genau in den Cache-Prüfungen; H8 (pwm/Rückgabe/`.shutdown`) 55/55 | H0–H3 |
| `test-deb.sh` | erster Lauf FAIL durch Testfehler (`grep -q` + `pipefail` → SIGPIPE bei 2 MB `modprobe -c`); Prüfung fängt die Ausgabe jetzt erst ein, `lsmod`-Prüfung liest `/proc/modules` (Hinweis aus dem Review); dann PASS | T0, T1 |
| Unabhängiges Review (Delta sr2 → sr3) | `0009` ohne Befund (Sperren, jiffies, Fehlerpfade, Sentinel, `dead`, Rebind, pwm-Pfad geprüft); zwei Hinweise zu `test-deb.sh`, beide umgesetzt | T0 |
| #35 (Erfolgszeile `.shutdown`) | **nicht umgesetzt**: Journal stoppt vor `.shutdown`, Konsole `quiet` + Loglevel 3 zeigt weder Info noch Warnung, `efi_pstore` sichert nur bei Panic → Zeile wäre nirgends lesbar (gemessen 09.11.) | `/proc/sys/kernel/printk`, Journal Vor-Boot |

## Für den GitHub-Issue an ltdstudio/minisforum-n5-it5571

- [x] Distribution + Kernel: Proxmox VE 9.2.3 / Debian 13, `7.0.12-1-pve`
- [x] BIOS-Version: 1.05 (03/31/2026)
- [x] DMI: `N5 PRO` / `F8NAA` / board_version 1.0 (`raw/01-baseline-*.txt`)
- [x] `modinfo`: `raw/04-modinfo-20260914.txt`
- [x] `sensors`-Ausgabe: Phase 5
- [x] `dmesg | grep -i minisforum`: Phase 5
- [x] Kanal-für-Kanal-Verifikation: Phase 6, alle vier Kanäle
- [x] Befund: `pwmN_enable=1` vor `pwmN`-Write nötig (Doku-Hinweis für Nutzer)
- [x] Befund: Automatik-Befehl 0x31 (PCIe) stellt auf dem N5 Pro / BIOS 1.05
  keine Regelung wieder her — DCR5 bleibt stehen. 0x2b (HDD): der EC regelt
  wieder (Stufen gemessen Z2 17.10., Solldrehzahl aus EC 0x06 abgeleitet M5);
  die frühere Aussage „DCR bleibt stehen“ galt auch für HDD und war falsch
- [x] Befund: `research-tools/ec_probe.c`, `sio_probe.c` kollidieren unter
  glibc mit `inb_p`/`outb_p` aus `sys/io.h`
- [x] Befund: `acpi_ec` reserviert 0x62/0x66 hier nicht (`PNP0C09:00 status=0`),
  das Modul lädt unter Debian/Proxmox ohne `-EBUSY`