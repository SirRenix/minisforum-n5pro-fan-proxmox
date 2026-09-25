# Betriebsanleitung — n5-fand auf dem Minisforum N5 Pro

> **Archiviert.** n5-fand ist seit 15.09.2026 durch [n5-fangov](https://github.com/SirRenix/n5-fangov) abgelöst.
> Seine Dateien liegen in `legacy/`; `deploy/install.sh` installiert heute nur noch den Treiber,
> im Betrieb das Paket `minisforum-n5-it5571-dkms` nehmen (README).

Stand 14.09.2026. Gilt für Proxmox VE 9 / Debian 13 mit dem Community-Treiber
`ltdstudio/minisforum-n5-it5571` (Modul `minisforum_n5_it5571`).

## 1. Was läuft, und was es auf den EC schreibt

Vollständige Liste — es gibt nichts Verstecktes:

| Datei | Aufgabe | Schreibt auf die Hardware? |
|---|---|---|
| `/lib/modules/<kernel>/updates/dkms/minisforum_n5_it5571.ko` | Treiber (DKMS, Quelle `/usr/src/minisforum-n5-it5571-0.2.0/`) | beim Laden **nein**; nur wenn jemand `pwmN`/`pwmN_enable` beschreibt |
| `/etc/modules-load.d/minisforum-n5-it5571.conf` | lädt das Modul beim Boot | nein |
| `/etc/modprobe.d/minisforum-n5-it5571.conf` | `experimental_write=1` — macht `pwm*` sichtbar | nein |
| `/usr/local/sbin/n5-fand` | der Regler | **ja** — `pwm1..3` und `pwm1..3_enable`, alle 10 s |
| `/etc/n5-fand.conf` | Kurven und Grenzen | nein |
| `/etc/systemd/system/n5-fand.service` | Dienst mit Watchdog | nein |
| `/usr/local/sbin/n5-fand-failsafe` | läuft nach **jedem** Ende des Reglers | ja — setzt CPU/SSD auf Automatik, HDD auf `HDD_STOP_PWM` |
| `/usr/local/sbin/n5-fand-alert` | Alarm in den PVE-Notification-Stack | nein |
| `/etc/systemd/system/n5-fand-onfailure.service`, `/usr/local/sbin/n5-fand-onfailure` | Alarm bei jedem Ausfall des Reglers: echte Ursache, ob Neustart gelang | nein |
| `/etc/pve/notification-templates/default/n5-fand-*.hbs` | Mail-Vorlage | nein |
| `/usr/local/bin/n5fan` | CLI | nur indirekt (Override-Datei) |
| `/run/n5-fand/` | Laufzeitzustand: `state`, `override.N`, `alert.<typ>` | nein |

Der Regler fasst **nur `pwm1`, `pwm2`, `pwm3`** an. `pwm4` (PCIe-Header) bleibt unberührt.

## 2. Installation

Voraussetzungen: root, `build-essential git dkms lm-sensors smartmontools proxmox-headers-$(uname -r)`.

```
git clone <dieses Repo> n5pro-ec && cd n5pro-ec
./scripts/02-build-tools.sh     # holt den Upstream-Treiber, baut die Research-Tools
./deploy/install.sh             # DKMS, Autoload, Regler, CLI, Alarm-Template
systemctl restart n5-fand
n5fan check                     # muss "alles ok" melden
```

Beim ersten Einsatz auf **anderer Hardware** (anderes BIOS, andere Revision) vorher die
Phasen 1–6 aus `README.md` durchlaufen — die Kanalzuordnung ist nur für N5 Pro / F8NAA /
BIOS 1.05 verifiziert.

Deinstallation: `./deploy/uninstall.sh` (gibt die Kanäle an den EC zurück, entfernt alles
außer `/etc/n5-fand.conf`).

## 3. Täglicher Umgang — `n5fan`

```
n5fan status            Temperaturen, Duty, %, RPM, Modus je Kanal, letzte Alarme
n5fan set hdd 70%       HDD-Lüfter manuell (auch: set cpu 200, set 2 50%)
n5fan auto hdd          zurück auf die Kurve (oder: auto all)
n5fan curve             aktive Kurven anzeigen
n5fan log 50            letzte 50 Journalzeilen
n5fan check             Selbstcheck (Modul, DKMS, Dienst, hwmon, Alarmweg)
n5fan test 3            Kanalverifikation mit Tacho-Log (stoppt/startet den Regler selbst)
```

Manuelle Werte gelten bis `auto` oder Reboot. **Notfallschwellen und Stillstandserkennung
greifen auch im manuellen Modus.** Für den HDD-Kanal sind Werte unter 60 (~24 %) gesperrt.

`sensors minisforum_n5_it5571-*` zeigt dieselben Werte ohne den Regler.

## 4. Kurven anpassen

`/etc/n5-fand.conf`, danach `systemctl restart n5-fand`. Je Kanal vier Werte:

```
HDD_TMIN=36   # bis hier: Duty HDD_PMIN
HDD_PMIN=105
HDD_TMAX=46   # ab hier: Duty HDD_PMAX
HDD_PMAX=255
HDD_TCRIT=56  # ab hier: sofort 255, Alarm
```

Dazwischen linear. Sensorquellen sind fest: CPU ← `k10temp` Tctl, SSD ← heißeste NVMe
(Composite), HDD ← heißeste Platte (`drivetemp`). Duty ↔ RPM aus der Messung:

| Duty | CPU | SSD | HDD |
|---|---|---|---|
| 85 / 74 / 105 | 2000 | 2130 | 1650 |
| 140 | 3120 | 3280 | 2250 |
| 179 | 3830 | 3790 | 2725 |
| 217 | 4445 | 4230 | 3160 |
| 255 | 5073 | 4687 | 3540 |

Ungültige Werte (keine Zahl, außerhalb des Bereichs, `TMIN >= TMAX`, `TCRIT <= TMAX`)
werden im Journal gemeldet, durch eingebaute Defaults ersetzt und lösen einen Alarm aus —
der Regler startet trotzdem.

## 5. Alarme

Alarme gehen an den **Proxmox-Notification-Stack** (Severity `warning`, Template
`n5-fand`) — also dorthin, wo auch Backup- und ZFS-Meldungen landen. Betreff:
`[<host>] n5-fand: <Typ>`. Gleiche Alarme werden 30 min lang nicht wiederholt
(`ALERT_COOLDOWN`). Ohne PVE: `mail(1)` an root, immer zusätzlich `logger`.

| Typ | Bedeutung | Was der Regler tut | Was du tust |
|---|---|---|---|
| `sensor` | Temperatur unlesbar, außerhalb −20…120 °C oder k10temp 3 min exakt gleich | alle drei Kanäle 255, hwmon neu auflösen, weiter versuchen | `n5fan log`, `sensors`; Modul geladen? Platte ausgefallen? |
| `stall` | RPM 0 bei Duty ≥ 60 über 2 Zyklen | Kanal auf 255, Regelung des Kanals ausgesetzt; erholt sich bei 3× RPM > 0 | Lüfter/Kabel prüfen |
| `temp` | eine `*_TCRIT`-Schwelle erreicht | Kanal sofort 255 | Ursache klären (Last, Staub, Lüfter) |
| `write` | pwm-Write zweimal in Folge gescheitert oder Rücklesewert ≠ Sollwert | 255, Modul-Reload versucht | `dmesg`, `n5fan check` |
| `config` | `/etc/n5-fand.conf` fehlerhaft | Defaults aktiv | Datei korrigieren, `restart` |
| `start` | 30 s nach Start kein Modul/hwmon | wartet weiter | `dkms status minisforum-n5-it5571` — Kernel-Update? |
| `neustart` | Regler ist abgestürzt (Watchdog, Signal, Fehlercode) und wurde **automatisch neu gestartet**; Mail nennt Ursache und Neustart-Nummer | läuft wieder | `n5fan log 50` — Ursache verstehen; häufen sich die Neustarts, `journalctl -u n5-fand` |
| `service` | Dienst endgültig `failed` (5 Fehlstarts in 5 min, oder `modprobe` scheitert) — Mail nennt Ursache, Modul- und DKMS-Status | Failsafe-Zustand bleibt | `systemctl status n5-fand`, DKMS prüfen, `systemctl reset-failed n5-fand && systemctl start n5-fand` |

`neustart`/`service` werden von `n5-fand-onfailure` erzeugt (liest die echte Ursache
aus systemd, wartet den Neustart ab, Cooldown 30 min).

Alarmweg selbst testen: `/usr/local/sbin/n5-fand-alert test "Testalarm"`.

## 6. Was passiert wenn — Fehlerbilder und ihre Antwort

| Fehlerbild | Antwort | Belegt am 14.09.2026 |
|---|---|---|
| Regler hängt (z. B. blockierter sysfs-Read) | systemd-Watchdog killt nach 60 s, Failsafe, Neustart | ja (SIGSTOP → `ABRT` nach 60 s, Failsafe, Neustart) |
| Regler stirbt (Absturz, `kill -9`, OOM) | `ExecStopPost` setzt CPU/SSD Auto, HDD `HDD_STOP_PWM`; Neustart nach 5 s | ja (SIGKILL) |
| Kernel-Update ohne DKMS-Bau | `ExecStartPre=modprobe` scheitert → nach 5 Versuchen `failed` → Alarm `service`; Lüfter in EC-Automatik | nein (Ablauf per Unit-Logik, nicht provoziert) |
| Modul zur Laufzeit entladen | Writes scheitern → Alarm `write`, `modprobe` wird versucht | nein |
| Sensor fällt aus / liefert Unsinn | Alarm `sensor`, 255 | Konfigpfad ja, Sensorausfall nicht provoziert |
| Lüfter bleibt stehen | Alarm `stall`, Kanal 255 | nein (kein Lüfter abgezogen) |
| Jemand schreibt selbst nach `/sys` | jede Minute wird der Sollwert neu gesetzt, `pwmN_enable≠1` wird gemeldet und korrigiert | ja (Failsafe-Rücklauf erkannt) |
| Konfigdatei kaputt | Warnung, Defaults, Alarm `config` | ja |
| Reboot | Autoload → Dienst → Regler; ohne Regler bleibt der EC-Zustand | **noch nicht** (nächstes Wartungsfenster) |

**Grenze, die man kennen muss:** Wenn der Regler *und* der Failsafe beide nicht laufen
(z. B. Modul weg), steht der HDD-Kanal auf dem zuletzt geschriebenen Duty — der EC regelt
ihn nach einem Write nicht mehr. Bei Modulverlust wird dieser Duty vom EC beibehalten;
er ist nie kleiner als 105 (Kurvenminimum) bzw. 60 (CLI-Sperre).

## 7. Stoppverhalten — bewusst asymmetrisch

`systemctl stop n5-fand` gibt **CPU und SSD an die EC-Automatik** zurück (dort
nachweislich funktionsfähig) und setzt den **HDD-Kanal auf `HDD_STOP_PWM`** (Default
140 ≈ 2250 RPM), weil die EC-Automatik diesen Kanal nach einem Schreibzugriff nicht mehr
regelt (Messung: DCR3/4 blieben bei 0x57 trotz +5 °C). Erst ein Kaltstart stellt die
BIOS-Kurve wieder her.

## 8. Kernel-Update

```
apt dist-upgrade                      # ohne Reboot
dkms status                           # BEIDE Module für den neuen Kernel "installed"?
                                      #   xrt-amdxdna (falls vorhanden) und minisforum-n5-it5571
# fehlt eins: dkms install minisforum-n5-it5571/0.2.0 -k <neuer-kernel>
reboot
n5fan check
```

## 9. Ressourcen

Regler: Bash, ~4 MB RSS, drei sysfs-Writes je 10 s (plus je Minute die unveränderten).
Kein Netzwerk, keine Abhängigkeiten außer `coreutils`, `systemd`, `smartmontools`
(nur Testskripte), `perl` mit `PVE::Notify` (nur Alarm auf PVE).
