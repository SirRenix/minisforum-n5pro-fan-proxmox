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
| pwm1 | CPU | **ja, nur fan1** | 5073 | 4445 | 3830 | 3120 | fan2/fan3 ±10 | EC-Idle: 2020 RPM ≈ Duty 85 |
| pwm2 | SSD | **ja, nur fan2** | 4687 | 4230 | 3790 | 3280 | fan1/fan3 ±10 | NVMe fiel dabei 46 → 44 °C |
| pwm3 | HDD-Gruppe | **ja, nur fan3** | 3540 | 3160 | 2725 | 2250 | fan1/fan2 ±20 | langsamer Anlauf (1873 → 2668 → 3523 in 10 s) |

**Kanalzuordnung des N5-Pro-Profils ist vollständig bestätigt.** DCR3 und DCR4
tragen im I2EC-Register immer denselben Wert — die Gruppierung der beiden
HDD-Lüfter im Treiber stimmt.

**Nach Modul-Unload: EC-Automatik wiederhergestellt?**

| Kanal | Automatik-Befehl | Tacho zurück auf Ausgang | Regelt der EC danach? |
|---|---|---|---|
| pwm1 CPU | 0x21 | ja (2020 → 2009) | **ja** — DCR1 0x55 → 0x94 unter CPU-Last |
| pwm2 SSD | 0x2e | ja (2220 → 2173, driftet auf 2130) | nicht separat geprüft |
| pwm3 HDD | 0x2b | **nein** (1647 → 1117, stabil ~1240) | **nein** — DCR3/4 bleiben 0x57 bei System-Temp 33 → 38 °C und HDD-Leselast |
| pwm4 PCIe | 0x31 | n/a | **nein** — DCR5 behält den letzten Schreibwert 0x8c (140) |

Details in `raw/06-i2ec-dcr-20260914.txt`. Ein I2EC-Snapshot **vor** den
Tests fehlt (seither im Skript). Deshalb ist nicht unterscheidbar, ob der
HDD-Kanal vor den Tests vom EC geregelt wurde oder ob das BIOS beim Boot
einen festen Duty gesetzt hatte, den 0x2b nicht kennt. Messbar ist nur: nach
0x2b steht der HDD-Kanal fest auf 34 % und reagiert auf keine Temperatur.

**Konsequenz:** Für den Dauerbetrieb wird die Regelung im OS übernommen
(`deploy/n5-fand`), nicht an den EC zurückgegeben. Stoppverhalten des
Reglers: CPU/SSD → EC-Automatik (nachweislich funktionsfähig), HDD → fester
sicherer Wert (119 ≈ 1900 RPM).

## Dauerbetrieb (seit 14.09.2026 17:33)

| Komponente | Stand |
|---|---|
| Modul | DKMS `minisforum-n5-it5571/0.2.0`, gebaut + MOK-signiert für 7.0.12-1-pve, `AUTOINSTALL=yes` |
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
  Beim nächsten Reboot außerdem I2EC-Snapshot lesen → klärt, wie BIOS 1.05
  die DCRs initialisiert (offene Frage aus Phase 6).
- Bedeutung von EC[0x34] = 100 % konstant ungeklärt; liegt innerhalb einer
  Tabelle bei 0x30–0x38, vermutlich Kurvenpunkt, kein Live-Sollwert.
- EC-RAM 0x10–0x27 (8 Tripel Temp/PWM/Hysterese, 25–100 %) und 0x70–0x81
  (2 identische 3-Punkt-Tabellen — DCR3/DCR4?) nicht weiter untersucht.
- `n5-fand` vor `modprobe -r` stoppen; das Modul hält keinen Refcount.

## Für den GitHub-Issue an ltdstudio/minisforum-n5-it5571

- [x] Distribution + Kernel: Proxmox VE 9.2.3 / Debian 13, `7.0.12-1-pve`
- [x] BIOS-Version: 1.05 (03/31/2026)
- [x] DMI: `N5 PRO` / `F8NAA` / board_version 1.0 (`raw/01-baseline-*.txt`)
- [x] `modinfo`: `raw/04-modinfo-20260914.txt`
- [x] `sensors`-Ausgabe: Phase 5
- [x] `dmesg | grep -i minisforum`: Phase 5
- [x] Kanal-für-Kanal-Verifikation: Phase 6, alle vier Kanäle
- [x] Befund: `pwmN_enable=1` vor `pwmN`-Write nötig (Doku-Hinweis für Nutzer)
- [x] Befund: Automatik-Befehle 0x2b (HDD) und 0x31 (PCIe) stellen auf dem
  N5 Pro / BIOS 1.05 keine Regelung wieder her — DCR bleibt stehen
- [x] Befund: `research-tools/ec_probe.c`, `sio_probe.c` kollidieren unter
  glibc mit `inb_p`/`outb_p` aus `sys/io.h`
- [x] Befund: `acpi_ec` reserviert 0x62/0x66 hier nicht (`PNP0C09:00 status=0`),
  das Modul lädt unter Debian/Proxmox ohne `-EBUSY`