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

## Phase 6 — PWM-Test (nur im Wartungsfenster)

**Noch nicht durchgeführt.** Voraussetzung: User am Gerät.

| Kanal | erwarteter Lüfter | reagiert tatsächlich | RPM 100 % | RPM 55 % | Bemerkung |
|---|---|---|---|---|---|
| pwm1 | CPU | | | | |
| pwm2 | SSD | | | | |
| pwm3 | HDD-Gruppe | | | | |
| pwm4 | PCIe | | n/a | n/a | kein Tacho |

**Temperaturverlauf / Auffälligkeiten:**

**Nach Modul-Unload: EC-Automatik wiederhergestellt?** ja / nein —

## Offene Punkte

- Phase 6 (PWM-Write) steht aus — Reihenfolge 4 → 1 → 2 → 3.
- Ist am PCIe-Header (pwm4) überhaupt ein Lüfter angeschlossen? Vor Phase 6
  am Gerät klären.
- EC[0x34] = 100 % konstant: Bedeutung im Automatikmodus unklar (Sollwert-
  Obergrenze? letzter Request?). Für die Validierung nicht relevant.
- Nach Phase 6: DKMS-Verpackung, `fancontrol`-Kurve, Betriebsdoku.

## Für den GitHub-Issue an ltdstudio/minisforum-n5-it5571

- [x] Distribution + Kernel: Proxmox VE 9.2.3 / Debian 13, `7.0.12-1-pve`
- [x] BIOS-Version: 1.05 (03/31/2026)
- [x] `dmidecode`-Äquivalent: `/sys/class/dmi/id/*` in `raw/01-baseline-*.txt`
- [x] `modinfo minisforum_n5_it5571`: in `raw/04-build-*` bzw. oben
- [x] `sensors`-Ausgabe: oben (Phase 5)
- [x] `dmesg | grep -i minisforum`: oben (Phase 5)
- [ ] Ergebnis der Kanal-für-Kanal-Verifikation (Phase 6)
- [ ] Hinweis für den Maintainer: `research-tools/ec_probe.c` und `sio_probe.c`
  definieren `inb_p`/`outb_p` selbst und kollidieren mit glibc `sys/io.h`
  (Redefinition, vertauschte Argumentreihenfolge). Unter zig/musl unauffällig.
