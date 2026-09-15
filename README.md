# N5 Pro EC / IT5571 — Lüftersteuerung unter Proxmox

> **Nachfolger:** Der Bash-Regler `n5-fand` aus diesem Repo ist seit 15.09.2026 durch
> [n5-fangov](https://github.com/SirRenix/n5-fangov) (Go: Regler, CLI, Dashboard) abgelöst.
> Dieses Repo bleibt die Quelle für Validierung, Messwerte und das DKMS-Modul.

Arbeitsprojekt für Claude Code auf einem Proxmox-Host. Ziel: die vier Lüfterkanäle des
Minisforum N5 Pro über hwmon ansteuerbar machen und das experimentelle
N5-Pro-Profil des Community-Treibers validieren.

`CLAUDE.md` enthält den Kontext und die verbindlichen Sicherheitsregeln —
die liest Claude Code beim Projektstart automatisch.

## Aufsetzen

```
scp -r n5pro-ec root@<pve-host>:/root/
ssh root@<pve-host>
cd /root/n5pro-ec && chmod +x scripts/*.sh
```

Voraussetzungen (auf dem Zielhost am 14.09.2026 alle vorhanden, sonst nachziehen):
`build-essential git lm-sensors smartmontools dkms proxmox-headers-$(uname -r)`.

## Ablauf

```
./scripts/01-baseline.sh          # DMI, ioports, Referenztemperaturen   lesend
./scripts/02-build-tools.sh       # Repo klonen, Userspace-Tools bauen   lesend
./scripts/03-probe-readonly.sh    # EC-Dump vor/nach Last                lesend
./scripts/04-build-module.sh      # Modul gegen laufenden Kernel bauen   lesend
./scripts/05-load-readonly.sh     # Modul laden, PWM gesperrt            lesend
```

Danach `befunde/BEFUNDE.md` ausfüllen und bewerten. Erst wenn die Werte
plausibel sind und du im Wartungsfenster am Gerät sitzt:

```
./scripts/06-pwm-test.sh 4        # ein Kanal, schreibend
```

Jederzeit:

```
./scripts/99-restore.sh           # zurück auf EC/BIOS-Automatik
```

## Struktur

```
CLAUDE.md              Kontext + Sicherheitsregeln für Claude Code
docs/DMI-PATCH.md      falls der BIOS-DMI-String nicht exakt passt
scripts/               die Phasenskripte
befunde/BEFUNDE.md     Ergebnistabellen zum Ausfüllen
befunde/raw/           Rohlogs, von den Skripten automatisch abgelegt
deploy/                DKMS, Autoload, n5-fand, Installer (Dauerbetrieb)
upstream/              Klon von ltdstudio/minisforum-n5-it5571 (02 legt ihn an)
build/                 lokaler Modulbau (04 legt ihn an)
build-tools/           Baukopie der Research-Tools mit glibc-Fix (02 legt sie an)
bin/                   gebaute Userspace-Tools (02 legt sie an)
```

## Dauerbetrieb (`deploy/`)

Stand 14.09.2026: alle Phasen bestanden, Kanalzuordnung des N5-Pro-Profils
per Tacho verifiziert (`befunde/BEFUNDE.md`). **Anleitung für Betrieb, CLI,
Kurven, Alarme und Fehlerverhalten: [`docs/BETRIEB.md`](docs/BETRIEB.md).**

```
./deploy/install.sh        # DKMS-Modul, Autoload, n5-fand, CLI n5fan, Alarm-Template
n5fan check                # Selbstcheck
./deploy/uninstall.sh      # alles zurück
```

| Datei | Zweck |
|---|---|
| `deploy/dkms.conf` | Modul als DKMS-Paket `minisforum-n5-it5571/0.2.0`, baut bei Kernel-Updates automatisch nach |
| `deploy/*.modprobe.conf`, `*.modules-load.conf` | Autoload; `experimental_write=1` macht die `pwm*`-Knoten sichtbar, schreibt aber beim Laden nichts |
| `deploy/n5-fand` | Regler (Bash, ~4 MB): CPU ← k10temp, SSD ← max NVMe, HDD ← max drivetemp; Failsafe, Stillstands- und Sensorplausibilitätsprüfung, Konfigvalidierung, Watchdog-Ping |
| `deploy/n5-fand-failsafe` | `ExecStopPost`: sicherer Zustand nach jedem Ende, auch nach Absturz/Kill |
| `deploy/n5-fand-alert`, `n5-fand-onfailure.service`, `pve-notification/` | Alarme in den Proxmox-Notification-Stack |
| `deploy/n5fan` | CLI: `status`, `set`, `auto`, `curve`, `log`, `check`, `test` |
| `deploy/n5-fand.conf` | Kurven (→ `/etc/n5-fand.conf`, wird bei Neuinstallation nicht überschrieben) |
| `deploy/n5-fand.service` | systemd-Unit: `Type=notify`, `WatchdogSec=60`, `Restart=always`, `OnFailure` |

Warum kein `fancontrol`: ein Sensor pro Kanal reicht nicht (HDD = Maximum von
vier Platten), und die hwmon-Nummern sind nicht bootstabil.

Treiberdetail, das man wissen muss: `pwmN` ist nur beschreibbar, wenn
`pwmN_enable=1` (manuell) gesetzt ist — sonst `-EBUSY`. Der Wechsel auf 1
setzt intern zuerst 255. `pwmN_enable=2` gibt an die EC-Automatik zurück;
auf dem N5 Pro (BIOS 1.05) regelt der EC danach den **HDD-Kanal nicht mehr**
(Befund Phase 6). `n5-fand` setzt diesen Kanal beim Stoppen deshalb auf einen
festen sicheren Wert.

Nach einem Kernel-Update: `dkms status minisforum-n5-it5571` muss den neuen
Kernel als `installed` zeigen, sonst startet `n5-fand` nicht (Condition auf
`/sys/module/minisforum_n5_it5571`) und die Lüfter bleiben in der EC-Automatik.

## Upstream

Die Ergebnisse gehören als Issue zurück an `ltdstudio/minisforum-n5-it5571`,
damit das N5-Pro-Profil von „experimentell" auf „validiert" gehoben werden
kann. Checkliste am Ende von `befunde/BEFUNDE.md`.
