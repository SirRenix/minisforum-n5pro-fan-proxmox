# P1 (EC-Teil) — EC-RAM-Tabellen, EC-Versionsquelle, Kurvenüberschreibung

Stand 09.10.2026. Grundlage: die einzigen EC-RAM-Dumps `raw/03-probe-20260914-160338.txt`
(Dump 1 16:03 idle, Dump 2 nach 60 s Last), `raw/06-i2ec-dcr-20260914.txt`,
`raw/06-pwmtest-ch*.txt`, Treiber `feb6d9a`, Upstream `research-tools/` (n5_fan.c,
n5_i2ec_read.c, ec_probe.c, n5_pwm_diag.c) und README bei `feb6d9a`, `../bios-f8naa-1.05-analyse.md`.
Seit 09.10. zusätzlich (M5): BIOS-Paket F8NAA 1.05 — IFR des Setup-Moduls, PEI-Code, der die
Lüftereinstellungen an den EC schickt, Disassembly der EC-Firmware 0.14 aus demselben Image — und
read-only `efivars` auf n5host. Befehle, Hex-Auszüge, Fundstellen: `raw/m5-efivars-20261009.txt`.

Statusmarken: **gemessen** = steht so in einem Rohlog; **abgeleitet** = Arithmetik/Vergleich auf
Rohdaten oder aus dem disassemblierten Firmware-Code gelesen; **Hypothese** = Deutung ohne Beweis.
Die EC-Firmware im Image ist 0.14 (`ECVer 00.00.00.0E`), live meldet n5host `ec_firmware_release`
0.14 (gemessen) — dass Image-Code = laufender Code, ist abgeleitet, nicht am Gerät verifiziert.

## Ergebnis

| | Aussage | Status |
|---|---|---|
| ✅ | Die EC-RAM-Tabellen sind **nicht** die BIOS-Einstellungen „Fan 1–4 Setting“. Sie sind die eingebauten Automatikkurven der EC-Firmware, beim EC-Start aus Konstanten geschrieben (Code @0xA05E, 0xA2DB, 0xA4AB) | abgeleitet |
| ✅ | Tripel-Format ist `(Soll-Drehzahl/100, T_oben, T_unten)` — **nicht** `(T, D, D_vorher)`. Das dritte Byte ist die untere Bandgrenze = T_oben des Vorpunkts (daher die 13/13-Übereinstimmung) | abgeleitet |
| ✅ | Die Automatik regelt auf **Drehzahl**, nicht auf Duty: DCR wird in ±1-Schritten nachgeführt, bis der Tacho im Band Soll·100 ± 100 rpm liegt | abgeleitet |
| ✅ | Zuordnung: A 0x10–0x27 → CPU (DCR1, Eingang EC 0x09); B 0x30–0x38 → SSD (DCR2, Eingang EC 0x05); C 0x70–0x78 → HDD (DCR3), D 0x79–0x81 → HDD (DCR4), beide Eingang EC 0x06; PCIe (DCR5) hat **keine** Automatikkurve | abgeleitet |
| ✅ | Alle Drehzahlen vom 14.09. im Automatikbetrieb treffen die Tabellen-Sollwerte (CPU 2003/2965, SSD 2236/2211, HDD nach 0x2b 1237–1240) | abgeleitet aus gemessenen Werten |
| ✅ | **HDD-Kurveneingang dekodiert** (TESTSPEC §6): EC-RAM 0x06; < 34 °C → 1200 rpm, 34–99 °C → 1600 rpm, ≥ 100 °C → unverändert. Override-Register 0x88D5/0x88E5 (nicht im ACPI-Fenster) ersetzen 0x06, wenn ≠ 0 | abgeleitet |
| ✅ | BIOS-Zuordnung: Fan 1 = CPU, Fan 2 = PCIe, Fan 3 = HDD-Gruppe, Fan 4 = SSD (PEI-Code liest Setup 0xB9/0xC2/0xCB/0xCD und sendet die zugehörigen PMC2-Subkommandos) | abgeleitet |
| ✅ | PMC2-Subkommandos sind **keine** ACPI-EC-RAM-Offsets: 0x20–0x39 schreiben interne Register 0x8860–0x8871/0x887B/0x887C, die Tabellen liegen bei 0x8310–0x8381. Treiber-Writes können die Tabellen nicht überschreiben | abgeleitet (Subkommandotabelle EC @0xF70B) |
| ❌ | Live-Werte der BIOS-Lüftereinstellungen: Variable `Setup-ec87d643-…` ist zur Laufzeit **nicht sichtbar** | gemessen |
| ⚠️ | HDD-Zustand 16:03 (vor jedem Write): TACH3 1656 rpm bei EC 0x06 = 22 °C — Tabelle C sagt 1200. Anderer Modus aktiv; Kandidat: BIOS Fan 3 User Mode = Enabled mit PWM 100 (fester DCR, ohne Regelung) | gemessen (Werte) / Hypothese (Ursache) |
| ✅ | EC-Firmwareversion ohne Portzugriff lesbar: `/sys/class/dmi/id/ec_firmware_release` (SMBIOS Typ 0), am 20.09. = `0.14` | gemessen (bios-Analyse) |
| ⚠️ | EC-RAM 0x00–0x03 = `00 00 00 0E` = exakt `ECVer 00.00.00.0E` aus dem BIOS-Image | Bytegleichheit abgeleitet, Deutung Hypothese |

## 1. Tabellen dekodiert (Dump 1 = Dump 2, Bytes unverändert)

Zwischen Dump 1 und 2 änderten sich nur 0x04 (0x20→0x22), 0x05 (0x19→0x1C), 0x09 (0x22→0x4D) —
alle Tabellenbytes sind konstant (abgeleitet). Die EC-Firmware schreibt exakt diese Bytes beim
Start aus Konstanten (`MOV A,#imm` / `MOVX @DPTR,A` auf XRAM 0x8310 ff.; XRAM 0x8300–0x83FF =
ACPI-EC-RAM 0x00–0xFF) — abgeleitet. Als zusammenhängende Bytefolge stehen sie nirgends im Image;
die Suche danach war deshalb ergebnislos (abgeleitet).

Format je Punkt `(S, T_ob, T_unt)`: S = Soll-Drehzahl in 100 rpm, T_ob/T_unt = Bandgrenzen in °C
(abgeleitet aus den Lesestellen im EC-Code, `raw/m5-efivars-20261009.txt` §4c).

### Tabelle A, 0x10–0x27 → CPU (DCR1), 8 Punkte

| Offset | Bytes | Soll (rpm) | T_ob | T_unt |
|---|---|---|---|---|
| 0x10 | `00 19 00` | 0 | 25 | 0 |
| 0x13 | `14 2D 19` | 2000 | 45 | 25 |
| 0x16 | `17 37 2D` | 2300 | 55 | 45 |
| 0x19 | `19 41 37` | 2500 | 65 | 55 |
| 0x1C | `1B 4B 41` | 2700 | 75 | 65 |
| 0x1F | `1E 52 4B` | 3000 | 82 | 75 |
| 0x22 | `21 57 52` | 3300 | 87 | 82 |
| 0x25 | `22 64 57` | 3400 | 100 | 87 |

Eingang: Register 0x888A, falls ≠ 0, sonst EC-RAM 0x09 (CPU). Der EC läuft per Index durch sieben
Punkt-Deskriptoren im ROM (@0x3F5D: Zeiger auf T_ob, T_unt, Soll, Steigung 0x88B0+i); über T_ob
eine Stufe hoch, unter T_unt eine runter; über 87 °C fest Punkt 8 (3400 rpm) — abgeleitet. Ein
Interpolationszweig mit Steigungen (0x88B0–0x88B6 = 00 0F 14 14 1E 2A 14) ist vorhanden, sein
Ergebnis wird im disassemblierten Pfad aber direkt wieder durch S ersetzt — abgeleitet, am Gerät
nicht geprüft.

### Tabelle B, 0x30–0x38 → SSD (DCR2), 2 Punkte + Endmarke

| Offset | Bytes | Soll (rpm) | T_ob | T_unt |
|---|---|---|---|---|
| 0x30 | `16 3C 00` | 2200 | 60 | 0 |
| 0x33 | `1A 64 3C` | 2600 | 100 | 60 |
| 0x36 | `1A 64 64` | (Wiederholung, wird nicht gelesen) | | |

Eingang: 0x888B, falls ≠ 0, sonst EC-RAM 0x05 (Board). EC[0x34] = 0x64 ist T_ob von Punkt 2
(100 °C), **kein** PWM-Sollwert — die Upstream-Anzeige „PWM request: 100 % (EC[0x34])“ liest eine
Konstante (abgeleitet).

### Tabellen C/D, 0x70–0x78 → HDD DCR3, 0x79–0x81 → HDD DCR4 (identisch)

| Offset | Bytes | Soll (rpm) | T_ob | T_unt |
|---|---|---|---|---|
| 0x70 / 0x79 | `0C 22 00` | 1200 | 34 | 0 |
| 0x73 / 0x7C | `10 64 22` | 1600 | 100 | 34 |
| 0x76 / 0x7F | `10 64 64` | (Wiederholung, wird nicht gelesen) | | |

Eingang DCR3: 0x88D5, falls ≠ 0, sonst EC-RAM 0x06; DCR4: 0x88E5 bzw. EC-RAM 0x06. Tacho DCR3 =
TACH3 (Subkommando 0x14/0x15), DCR4 hat einen eigenen Tachowert 0x88E0/0x88E1, den der Treiber
nicht liest (abgeleitet). B, C, D lesen nur die ersten beiden Punkte und T_ob; T_unt wird dort
nicht ausgewertet → keine Hysterese (abgeleitet).

Weitere Nicht-Null-Bytes ohne Deutung: 0x2E=02, 0x40=01, 0x41=05 (= BIOS **1.05**? Hypothese),
0x45=03, 0x61=0C, 0xE4=0D, 0xF1=0A.

## 2. Abgleich mit den Beobachtungen vom 14.09.

Die Automatik stellt keinen Duty ein, sondern eine Drehzahl (Band ± 100 rpm). Der DCR-Wert, auf
dem ein Kanal stehen bleibt, hängt damit vom Lüfter ab, nicht von der Tabelle. Der frühere Versuch,
DCR-Werte als Tabellenbytes wiederzufinden (roh/Prozent), war deshalb falsch angesetzt; seine
Ausschlusstabelle ist gestrichen (abgeleitet).

| Kanal | Zustand (gemessen) | Eingang (gemessen) | Tabellen-Soll | Ergebnis |
|---|---|---|---|---|
| CPU | 16:03, TACH1 2003 | EC 0x09 = 34/35 | A, Band 25–45 → 2000 ± 100 | ✓ abgeleitet |
| CPU | nach 60 s Last, TACH1 2965 | EC 0x09 = 77 | Band 75–82 → 3000 ± 100 | ✓ abgeleitet |
| CPU | 17:27–17:29 nach 0x21, TACH1 2002 → 3281, DCR1 0x55 → 0x94 | k10temp 40 → 83 (EC 0x09 nicht geloggt) | 3281 liegt im Band von 3300 (82–87 °C) | passt, EC-Eingang nicht belegt — Hypothese |
| SSD | 16:03, TACH2 2236 / 2211 | EC 0x05 = 25/28 | B, < 60 → 2200 ± 100 | ✓ abgeleitet |
| HDD | 17:20–17:27 nach 0x2b, TACH3 1237–1240, DCR3 = DCR4 = 0x57 | EC 0x06 = 22 konstant; System 33→38, Board 26→32 | C, < 34 → 1200 ± 100 | ✓ abgeleitet — DCR bleibt, weil der Eingang (0x06) sich nicht bewegt hat; System/Board sind nicht der Eingang |
| HDD | 16:03 vor jedem Write, TACH3 1656 / 1653 | EC 0x06 = 22 | C sagt 1200 | ❌ passt nicht → Abschnitt 5.3 |

DCR3 = DCR4 in allen Lesungen: Beide Kanäle regeln getrennt auf je eigenen Tacho mit gleicher
Tabelle — gleiche Endwerte sind bei baugleichen Lüftern möglich, aber nicht zwingend. Ob DCR4 einen
eigenen Lüfter hat oder ob 0x88E0/E1 dasselbe Signal liest, ist offen.

**T4/T5 für HDD:** Eingang und Stufen sind jetzt bekannt (abgeleitet, s. §1). Eine Stufe liegt bei
34 °C am Sensor EC 0x06; der hat in allen Logs 22 °C konstant gezeigt und reagierte weder auf
CPU- noch auf Plattenlast (gemessen). Welcher physische Fühler 0x06 ist, ist offen (Treiber nennt
ihn „Ambient“; EC-Code kopiert ihn aus XRAM 0x893A, Quelle davor nicht verfolgt). Solange 0x06 im
Test nicht ≥ 36 °C erreicht, bleibt T4/T5 auf HDD INCONCLUSIVE („input did not cross a step“).
Für die Automatik ist die Regelgröße der Tacho (Soll ± 100 rpm), der DCR nur Stellgröße. TESTSPEC 0.3
(09.10., Betreiberentscheidung D2) übernimmt das: T2/T3 bleiben auf dem DCR, T4/T5 bewerten den Tacho im
Zielband abhängig vom Tabelleneingang (§5.5); §6 nennt das Tripel-Format `(Soll/100, T_ob, T_unt)`.

## 3. Überschreiben die Treiber-Writes Kurvenpunkte? (Aufgabe c)

| Punkt | Befund | Status |
|---|---|---|
| Vorher/Nachher-Dumps | Dump 16:03 liegt **vor** dem ersten Write (16:53). Danach kein EC-RAM-Dump. Direkter Vergleich unmöglich | gemessen |
| Ziel der Writes | Subkommandotabelle der EC-Firmware: 0x20/0x21 setzen/löschen Modusbit 0 (CPU-Host-Kurve), 0x22–0x29 → 0x8861–0x8868 (T1–T4, PWM1–4), 0x2A/0x2B/0x2C → Bit 2 / Festwert 0x887C (HDD), 0x2D/0x2E/0x2F → Bit 1 / 0x887B (SSD), 0x30/0x31/0x32–0x39 → Bit 3 / 0x886A–0x8871 (PCIe), 0x3A liest das Modusbyte 0x8860. Keine dieser Adressen liegt in 0x8310–0x8381 → **die Tabellen werden von den Treiber-Writes nicht berührt** | abgeleitet |
| PMC2-Index ≠ EC-RAM-Offset | 0x18/0x19 → TACH1 (0x8880/81), 0x16/0x17 → TACH2, 0x14/0x15 → TACH3 — bestätigt die frühere Tacho-Gegenprobe | abgeleitet |
| CPU nach 0x21 | 0x21 löscht Bit 0 → EC regelt wieder nach Tabelle A (DCR1 0x55 idle, steigt mit Last) | abgeleitet, passt zu gemessen |
| PCIe nach 0x31 | 0x31 löscht Bit 3. Die einzige Schreibstelle auf DCR5 (0x1807) läuft nur bei gesetztem Bit 3 → nach 0x31 schreibt der EC DCR5 nie mehr, der letzte Wert (0x8C) bleibt stehen. Erklärt „DCR5 = 0x8C auch nach erneutem 0x31“. PCIe hat im EC **keine** Automatik | abgeleitet |
| HDD nach 0x2b | 0x2b löscht Bit 2 → Tabelle C/D mit Eingang 0x06 → 1200 rpm | abgeleitet, passt zu gemessen |

Folgerung: TESTSPEC C1 („Tabellen unverändert“) ist nach dem Code für die bekannten Subkommandos
konstruktionsbedingt erfüllt (abgeleitet); messen muss C1 trotzdem, weil der Code nicht am Gerät
verifiziert ist. Für PCIe gibt es keinen Rückgabeweg an eine EC-Automatik — der Treiber muss beim
Hand-back einen definierten Festwert hinterlassen (abgeleitet).

## 4. EC-Firmwareversion (Aufgabe b)

| Quelle | Inhalt | Lesbar wie | Sicher bei geladenem Treiber? |
|---|---|---|---|
| Treiberkopf / `n5_fan.c` | „F8NAA EC firmware V0.14" — Kommentar des Upstream-Autors (RE-Grundlage), **kein Code liest eine Version** | — | — |
| Upstream-Tools `feb6d9a` | `ec_probe` (Dump/Read/Write/blinde Kommandos), `n5_fan` (Tacho, EC[0x34]), `n5_i2ec_read` (I2EC 0x2e/0x2f), `n5_pwm_diag` (Writes). **Keins liest eine Versionskennung**; README nennt keine | — | — |
| BIOS-Image F8NAA 1.05 | `ECVer 00.00.00.0E` = 0.14, Projekt „F8NAA_StrixHowk", Build „Aug 13 2025" | Image-Analyse | — |
| **SMBIOS Typ 0** | `ec_firmware_release` = `0.14` (gemessen 20.09. und 09.10.) | `cat /sys/class/dmi/id/ec_firmware_release` oder `dmidecode -t 0` („Firmware Revision") | **ja** — Tabelle im RAM, vom BIOS beim POST gefüllt, kein EC-Port. Zeigt den Stand, den das BIOS beim Boot gemeldet hat |
| EC-RAM 0x00–0x03 | `00 00 00 0E` = bytegleich mit `ECVer 00.00.00.0E` (Hypothese: Live-Versionsregister; F8NAB „EC 0.15" würde 0x0F erwarten lassen) | ACPI-EC-Read 0x80 über 0x62/0x66 | **nein** — gleiche Ports wie der Treiber, Userspace-`ioperm` nimmt dessen Mutex nicht; n5-fangov pollt hwmon laufend → Transaktionen verschränken sich. Nur mit entladenem Treiber oder über Patch 0003 unter Treiber-Lock |
| ASCII in unseren Dumps | einzige druckbare Folge ≥ 3 Zeichen: 0x20 `RK!WR"dW` = Tabellendaten | — | — |

Empfehlung für TESTSPEC/RESULTS: Schlüssel „EC firmware" = `ec_firmware_release` aus sysfs
(gefahrlos). EC-RAM 0x00–0x03 im Wartungsfenster einmal gegenlesen, um die Zuordnung zu bestätigen.

## 5. M5 — BIOS „Fan 1–4 Setting“ (09.10.2026)

### 5.1 Setup-Variable und IFR

| Punkt | Befund | Status |
|---|---|---|
| Ablage | Varstore `Setup`, GUID `ec87d643-eba4-4bb5-a1e5-3f3e36b20da9`, 210 Byte; Formular „Hardware Monitor“ (Form 10313) | abgeleitet (IFR) |
| Fan 1 | 0xB9 User Mode (0/1, Default 0), 0xBA–0xBD Temperatur 1–4 (0–120, Default 30/50/70/90), 0xBE–0xC1 PWM 1–4 (0–255, Default 100/150/200/250) | abgeleitet (IFR) |
| Fan 2 | 0xC2 User Mode, 0xC3–0xC6 Temperatur 1–4, 0xC7–0xCA PWM 1–4, gleiche Defaults | abgeleitet (IFR) |
| Fan 3 | 0xCB User Mode, 0xCC PWM 1 (Default 100) | abgeleitet (IFR) |
| Fan 4 | 0xCD User Mode, 0xCE PWM 1 (Default 100) | abgeleitet (IFR) |
| Laufzeit | `Setup-ec87d643-…` fehlt in `/sys/firmware/efi/efivars` (84 Variablen, 0 mit dieser GUID). Sichtbar sind nur AmdSetup, AMD_PBS_SETUP, AodSetupStx (Attribut 0x07); keine enthält Tabellen- oder IFR-Default-Bytefolgen | gemessen |
| Grund | Variable ohne Runtime-Attribut oder durch „Password protection of Runtime Variables“ (Setup 0x3F, Default Enabled) verborgen | Hypothese |

→ Die Live-Werte an den IFR-Offsets sind vom Betriebssystem aus **nicht lesbar**. Der direkte
Byte-Vergleich, wie in M5 geplant, ist zur Laufzeit nicht durchführbar (gemessen).

### 5.2 Weg der BIOS-Werte in den EC und Kanalzuordnung

Ein PEI-Modul im Reset-Image liest `Setup` (Größe 0xD2) und sendet bei jedem Start über PMC2
(Kommando 0xD5) — abgeleitet aus dem 32-Bit-Code, Offsets passen exakt auf das IFR:

| BIOS | User Mode = Enabled | Disabled | EC-Kanal |
|---|---|---|---|
| Fan 1 (0xB9) | 0x22/24/26/28 ← Temp 1–4 (monoton geklemmt), 0x23/25/27/29 ← PWM 1–4, dann 0x20 | 0x21 | CPU, DCR1 |
| Fan 2 (0xC2) | 0x32–0x39 analog, dann 0x30 | 0x31 | PCIe, DCR5 |
| Fan 3 (0xCB) | 0x2C ← PWM 1, dann 0x2A | 0x2B | HDD, DCR3 + DCR4 |
| Fan 4 (0xCD) | 0x2F ← PWM 1, dann 0x2D | 0x2E | SSD, DCR2 |

Folgerungen (abgeleitet):
- Die BIOS-Einstellungen landen in den **Host-Registern** des EC (0x8861–0x8871, 0x887B,
  0x887C) — dieselben, die der Treiber beschreibt. Mit User Mode = Enabled gibt der EC den
  PWM-Wert **direkt** als DCR aus (Fan 3/4 fest, Fan 1/2 als 4-Punkt-Kurve auf DCR), ohne
  Drehzahlregelung.
- Mit User Mode = Disabled schickt das BIOS genau die Rückgabe-Kommandos des Treibers
  (0x21/0x31/0x2B/0x2E); dann gelten die EC-Tabellen aus §1. Die BIOS-Werte ändern die Tabellen
  in keinem Fall.
- Die Default-Werte des IFR (30/50/70/90, 100/150/200/250) kommen in keiner EC-RAM-Tabelle vor.
- Das Setup-DXE-Modul selbst liest nur Tachos (Subkommandos 0x14–0x19) für die Anzeige.

### 5.3 Boot-Zustand HDD — offene Erklärung

16:03 (vor jedem Write, kein Treiber geladen) lief TACH3 mit 1656 rpm bei EC 0x06 = 22 °C
(gemessen). Tabelle C ergibt 1200 rpm; 1600 rpm wären Punkt 2 und verlangten einen Eingang
≥ 34 °C. Nach 0x2b sank TACH3 auf ~1240 bei DCR3 0x57 (gemessen). Die frühere lineare Schätzung
setzt 1647 rpm bei DCR ≈ 90–108 (abgeleitet). Die nächstliegende Erklärung: **Fan 3 User Mode
ist im BIOS von n5host Enabled, PWM 1 = 100** → PEI sendet 0x2C 100 + 0x2A → DCR3/DCR4 fest 0x64,
bis der Treiber 0x2b sendet (Hypothese). Alternativen: Override 0x88D5 ≠ 0 oder ein anderer
EC-Modus (0x88CB Bit 0, Zielregister 0x88CE) — Hypothese. CPU (2003 rpm) und SSD (2236 rpm)
lagen 16:03 auf den Automatik-Sollwerten → Fan 1 und Fan 4 vermutlich Disabled (Hypothese).

## 6. Was im Wartungsfenster die offenen Punkte klärt

Alle EC-Lesungen nur mit gestopptem n5-fangov **und** entladenem Modul (oder später über Patch
0003), je zwei Dumps zur Rauschkontrolle.

| # | Messung | Entscheidet |
|---|---|---|
| M1 | Nach **Kaltstart**, vor erstem Write: EC-RAM 0x00–0xFF + I2EC 0x1800–0x182F + alle EC-Temperaturen + alle Tachos; zusätzlich PMC2 0xD5/0x3A (liest Modusbyte 0x8860, kein Schreibzugriff) | Boot-Zustand DCR1–5; welche Host-Modi das BIOS gesetzt hat (Bit 2 gesetzt ⇒ Fan 3 User Mode aktiv, §5.3); Version 0x00–0x03 vs. sysfs |
| M2 | Je Kanal: Write (Testwert ≠ 140, z. B. 200) → Hand-back → nach 5 min Dump + I2EC | Tabellen byteidentisch (TESTSPEC C1); Code-Aussage aus §3 am Gerät bestätigen |
| M3 | PCIe nach 0x31: DCR5 über ≥ 10 min bei wechselnder Last; Warm-Reboot vs. Kaltstart | bestätigt „keine PCIe-Automatik, DCR5 eingefroren“ (§3) und misst, welchen DCR5 ein Neustart setzt |
| M4 | HDD nach 0x2b: TACH3 und DCR3 über ≥ 15 min; EC 0x06 dabei mitloggen | Drehzahlband 1200 ± 100 bestätigen; ob 0x06 überhaupt beweglich ist (Voraussetzung T4/T5 HDD) |
| M5 | ✅ 09.10. erledigt, s. §5. Rest: BIOS-Setup „Hardware Monitor“ im Wartungsfenster **ansehen** (nur lesen) oder in der eingebauten UEFI-Shell `dmpstore Setup` → Bytes 0xB9–0xCE | Live-Werte Fan 1–4, prüft Hypothese §5.3 |
