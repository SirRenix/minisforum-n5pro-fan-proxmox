# N5 Pro EC / IT5571 — Lüftersteuerung unter Proxmox

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
upstream/              Klon von ltdstudio/minisforum-n5-it5571 (02 legt ihn an)
build/                 lokaler Modulbau (04 legt ihn an)
bin/                   gebaute Userspace-Tools (02 legt sie an)
```

## Wenn es ans Dauerbetreiben geht

Läuft die Steuerung, übernimmt `fancontrol` aus `lm-sensors` die Kurven
(`pwmconfig` erzeugt `/etc/fancontrol`). Nur ein Regler pro `pwmN`.
Nach jedem Proxmox-Kernel-Update muss das Modul neu gebaut werden —
dann lohnt eine DKMS-Verpackung statt `insmod` von Hand.

## Nicht vergessen

Die Ergebnisse gehören als Issue zurück ins Upstream-Repo. Der Maintainer
sammelt genau diese Daten, um das N5-Pro-Profil von „experimentell" auf
„validiert" zu heben. Checkliste dafür steht am Ende von `befunde/BEFUNDE.md`.
