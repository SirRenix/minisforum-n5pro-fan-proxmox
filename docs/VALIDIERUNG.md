# Validierung: EC-Protokoll, Stolpersteine, Sicherheitsregeln, Phasenplan

Stand der Messungen: 14.09.2026, Minisforum N5 Pro (`F8NAA`, BIOS 1.05) unter
Proxmox VE 9.2 — Details und Rohdaten in [`befunde/BEFUNDE.md`](../befunde/BEFUNDE.md).

## Bekanntes EC-Protokoll (aus dem Treiber-Quellcode)

| Zweck | Zugang |
|---|---|
| Temperaturen | ACPI-EC-Ports 0x62 (Daten) / 0x66 (Status/Cmd), Lesekommando 0x80 |
| EC-RAM-Offsets | 0x09 CPU, 0x04 System, 0x05 Board, 0x06 Ambient |
| PWM-Sollwert | ACPI EC RAM 0x34 (Prozentwert) |
| Lüfter | PMC2 auf 0x68 (Daten) / 0x6c (Status/Cmd), Kommando **0xd5** + Subkommando |
| PMC-Portdiskovery | nur in `research-tools/n5_fan probe` (Super-I/O 0x2e/0x2f); das Kernelmodul hat 0x68/0x6c **hartkodiert** |

Treiberinterna (Quellcode-Stand 14.09.2026):

- PWM-Write pro Kanal: CPU → PMC2-Writes 0x23/0x25/0x27/0x29 + Action 0x20;
  SSD → 0x2f + Action 0x2d; HDD → 0x2c + Action 0x2a;
  PCIe → 0x33/0x35/0x37/0x39 + Action 0x30
- Rückgabe an EC-Automatik: Action `0x21` (CPU), `0x2e` (SSD), `0x2b` (HDD),
  `0x31` (PCIe). `n5_remove()` ruft das für jeden Kanal, der angefasst wurde
  (`pwm_touched[]`).
- `pwm*`-Attribute sind bei `validated=false` **unsichtbar**, solange
  `experimental_write=1` nicht gesetzt ist (`n5_is_visible()` → 0).
- Modulparameter: `force` (bool, lädt ohne DMI-Match, read-only),
  `experimental_write` (bool, schaltet PWM auf experimentellen Profilen frei).
- `pwmN_enable`: 0 = Vollgas, 1 = manuell (setzt beim Wechsel erst 255),
  2 = EC-Automatik (Default). `pwmN`-Write ohne `enable=1` → `-EBUSY`.
- I2EC (`bin/n5_i2ec_read 0x1800 16`) zeigt die echten Register: 0x1801 CTR,
  0x1802–0x1809 DCR0–7. DCR1 CPU, DCR2 SSD, DCR3=DCR4 HDD, DCR5 PCIe.
- **Befund N5 Pro / BIOS 1.05:** nach Automatik-Befehl 0x2b (HDD) und 0x31
  (PCIe) bleibt der DCR stehen, keine Regelung mehr. CPU (0x21) regelt weiter.

Kanalzuordnung laut Treiber:

| hwmon | Funktion | EC-Ressource |
|---|---|---|
| pwm1 / fan1 | CPU-Lüfter | DCR1, TACH1 |
| pwm2 / fan2 | SSD-Lüfter | DCR2, TACH2 |
| pwm3 / fan3 | HDD-Lüftergruppe | DCR3 + DCR4, TACH3 |
| pwm4 | PCIe-Lüfter | DCR5, kein Tacho (RPM = 0 ist erwartet) |

## Die zwei generischen Stolpersteine — auf dem Zielhost beide nicht vorhanden

1. **DMI-Match.** Der Treiber nutzt `DMI_EXACT_MATCH` auf
   `DMI_PRODUCT_NAME = "N5 PRO"` und `DMI_BOARD_NAME = "F8NAA"`. Auf dem Zielhost
   stimmen beide Strings exakt (gemessen 14.09.2026). Für andere BIOS-Stände
   liegt die Patch-Vorlage in `docs/DMI-PATCH.md`.
2. **Port-Reservierung.** `n5_probe()` macht `request_region()` auf
   0x62/0x66/0x68/0x6c. Auf dem Zielhost ist keiner dieser Ports belegt, weil das
   ACPI-EC-Gerät `PNP0C09:00` in der DSDT `status=0` hat. Auf anderen
   Debian-Systemen mit aktivem `acpi_ec` scheitert das Modul mit `-EBUSY`;
   dann bleibt der Userspace-Pfad (`ioperm()`, Phase 3).

Die Userspace-Tools in `research-tools/` bleiben trotzdem der erste Testpfad:
sie laufen ohne Kernelmodul und liefern den EC-Dump zum Abgleich.

## Sicherheitsregeln — verbindlich

- **Phase 1–5 (read-only) sind jederzeit erlaubt.** Kein PWM-Write.
- **Phase 6 (PWM-Write) nur mit physischem Zugang und im Wartungsfenster.**
  Nicht remote, nicht unbeaufsichtigt.
- Kein `dmesg -C` auf dem Host — der Ring-Buffer ist Beweismaterial.
- Niemals mit `pwm=0` beginnen. Immer bei voller Drehzahl starten und in
  kleinen Schritten reduzieren.
- Immer nur **ein** Kanal gleichzeitig.
- `ec_probe write` ist im Repo vorhanden — **nicht verwenden.** Blindes
  Schreiben in EC-RAM kann Firmware-Zustände zerschießen.
- Bei jedem unerwarteten Verhalten (Lüfter stoppt, falscher Header reagiert,
  Temperatur springt): sofort `scripts/99-restore.sh`.
- Keine konkurrierenden Regler auf denselben `pwmN`. Im Dauerbetrieb läuft
  `n5-fand` — vor jedem Test `systemctl stop n5-fand`, danach wieder starten.
- Vor jedem Schreibtest I2EC-Snapshot (steht im 06-Skript) — ohne
  Vergleichswert ist „Automatik wiederhergestellt" nicht beweisbar.
- Das Modul ist an `uname -r` gebunden. Nach jedem Kernel-Update neu bauen,
  sonst nicht laden.


## Phasenplan

| Phase | Skript | Schreibend? |
|---|---|---|
| 1 Baseline erfassen | `scripts/01-baseline.sh` | nein |
| 2 Research-Tools bauen | `scripts/02-build-tools.sh` | nein |
| 3 Read-only-Probe | `scripts/03-probe-readonly.sh` | nein |
| 4 Modul bauen | `scripts/04-build-module.sh` | nein |
| 5 Modul read-only laden | `scripts/05-load-readonly.sh` | nein (PWM gesperrt) |
| 6 PWM-Test, ein Kanal | `scripts/06-pwm-test.sh` | **ja — Wartungsfenster, n5-fand vorher stoppen** |
| 7 Dauerbetrieb | `deploy/install.sh` | ja (DKMS, systemd) |
| — Notfall | `scripts/99-restore.sh` | stellt BIOS-Automatik wieder her |
