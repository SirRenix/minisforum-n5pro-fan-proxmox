# N5 Pro EC / IT5571 — fan driver package and validation

DKMS package of the community driver
[`ltdstudio/minisforum-n5-it5571`](https://github.com/ltdstudio/minisforum-n5-it5571) for
the Minisforum N5 Pro, plus the validation data of its experimental N5 Pro profile.
The regulator on top is [n5-fangov](https://github.com/SirRenix/n5-fangov).

## Install (Proxmox VE 9 / Debian 13)

Download `minisforum-n5-it5571-dkms_<ver>_all.deb` and its `.sha256` from the
[latest release](https://github.com/SirRenix/minisforum-n5pro-fan-proxmox/releases/latest), then as root:

```
sha256sum -c minisforum-n5-it5571-dkms_*_all.deb.sha256
apt install ./minisforum-n5-it5571-dkms_*_all.deb
```

The package pulls `dkms` and the kernel header meta-package (`proxmox-default-headers`,
on plain Debian `linux-headers-amd64`), builds the module for every kernel with headers
and rebuilds it for each new kernel. It sets `experimental_write=1` (without it the
`pwm*` nodes stay hidden) in `/usr/lib/modprobe.d/` and loads the module at boot via
`/usr/lib/modules-load.d/`; a file of the same name in `/etc/` overrides either.
Loading writes nothing to the EC — `pwm*_enable` starts at `2` (EC automatic), only a
regulator writes. A hand installation from `deploy/install.sh` is taken over.

Check: `dkms status minisforum-n5-it5571` lists every kernel as `installed`,
`ls /sys/class/hwmon/*/pwm1` finds the node. Remove: `apt remove minisforum-n5-it5571-dkms`.

The driver source is upstream's tag pinned in [`packaging/UPSTREAM`](packaging/UPSTREAM)
(commit verified at build time); the release workflow builds the package and tests the
installation in Debian 13.

---

## Validierung (deutsch)

Sicherheitsregeln, EC-Protokoll und Phasenplan: [`docs/VALIDIERUNG.md`](docs/VALIDIERUNG.md) —
vor dem ersten Skript lesen. Ergebnisse: [`befunde/BEFUNDE.md`](befunde/BEFUNDE.md),
Upstream-Issue: [ltdstudio/minisforum-n5-it5571#7](https://github.com/ltdstudio/minisforum-n5-it5571/issues/7).

### Aufsetzen

```
scp -r n5pro-ec root@<pve-host>:/root/
ssh root@<pve-host>
cd /root/n5pro-ec && chmod +x scripts/*.sh
```

Voraussetzungen: `build-essential git lm-sensors smartmontools dkms proxmox-headers-$(uname -r)`.

### Ablauf

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

### Treiber ohne Paket

`deploy/install.sh` baut den Treiber aus dem Upstream-Klon (`02-build-tools.sh`) als
DKMS-Modul und legt Optionen und Autoload nach `/etc/`. Das ist der Weg für Entwicklung
am Treiber; im Betrieb das Paket nehmen. `deploy/uninstall.sh` entfernt es wieder.
Beide verweigern, wenn das Paket installiert ist.

### Struktur

```
packaging/             DKMS-Paket: Upstream-Pin, Build, Installationstest
deploy/                dkms.conf, Modul-Optionen, Autoload, install.sh/uninstall.sh
docs/VALIDIERUNG.md    EC-Protokoll, Sicherheitsregeln, Phasenplan
docs/DMI-PATCH.md      falls der BIOS-DMI-String nicht exakt passt
docs/BETRIEB.md        Betrieb mit n5-fand (archiviert)
scripts/               die Phasenskripte
befunde/BEFUNDE.md     Ergebnistabellen
befunde/raw/           Rohlogs
legacy/                n5-fand (Bash-Regler, abgelöst durch n5-fangov) und sein Uninstaller
upstream/              Klon von ltdstudio/minisforum-n5-it5571 (02 legt ihn an)
build/, build-tools/, bin/   lokale Bauprodukte der Phasenskripte
```

Treiberdetail, das man wissen muss: `pwmN` ist nur beschreibbar, wenn
`pwmN_enable=1` (manuell) gesetzt ist — sonst `-EBUSY`. Der Wechsel auf 1
setzt intern zuerst 255. `pwmN_enable=2` gibt an die EC-Automatik zurück;
auf dem N5 Pro (BIOS 1.05) regelt der EC danach den **HDD-Kanal nicht mehr**
(Befund Phase 6). Ein Regler setzt diesen Kanal beim Stoppen deshalb auf einen
festen sicheren Wert.

### n5-fand (archiviert)

Der Bash-Regler `n5-fand` ist seit 15.09.2026 durch
[n5-fangov](https://github.com/SirRenix/n5-fangov) abgelöst und liegt nur noch in
`legacy/`; [`docs/BETRIEB.md`](docs/BETRIEB.md) beschreibt ihn. Entfernen:
`legacy/uninstall.sh` (nimmt auch eine Handinstallation des Treibers mit, nicht das Paket).
