#!/usr/bin/env bash
# Entfernt n5-fand, CLI, Autoload und das DKMS-Modul. Gibt die Luefter an die
# EC/BIOS-Automatik zurueck. Hinweis: der HDD-Kanal regelt nach einem Write
# erst nach einem Kaltstart wieder ueber das BIOS (Befund Phase 6).
set -u
[[ $EUID -eq 0 ]] || { echo "als root"; exit 1; }
PKG="minisforum-n5-it5571"; VER="0.2.0"

echo "--- Dienst stoppen (Failsafe setzt CPU/SSD auf Automatik, HDD auf festen Wert) ---"
systemctl disable --now n5-fand.service 2>/dev/null || true
rm -f /etc/systemd/system/n5-fand.service /etc/systemd/system/n5-fand-onfailure.service
systemctl daemon-reload

echo "--- Alle Kanaele an die EC-Automatik ---"
for h in /sys/class/hwmon/hwmon*; do
    [[ -r "$h/name" && "$(<"$h/name")" == "minisforum_n5_it5571" ]] || continue
    for i in 1 2 3 4; do echo 2 > "$h/pwm${i}_enable" 2>/dev/null && echo "  pwm$i -> auto"; done
done
modprobe -r minisforum_n5_it5571 2>/dev/null && echo "  Modul entladen"

echo "--- Dateien ---"
rm -f /usr/local/sbin/n5-fand /usr/local/sbin/n5-fand-failsafe /usr/local/sbin/n5-fand-alert /usr/local/sbin/n5-fand-onfailure /usr/local/bin/n5fan
rm -f /etc/modules-load.d/$PKG.conf /etc/modprobe.d/$PKG.conf
rm -rf /run/n5-fand
rm -f /etc/pve/notification-templates/default/n5-fand-subject.txt.hbs /etc/pve/notification-templates/default/n5-fand-body.txt.hbs 2>/dev/null
echo "  /etc/n5-fand.conf bleibt erhalten (bei Bedarf von Hand loeschen)"

echo "--- DKMS ---"
dkms remove "$PKG/$VER" --all 2>/dev/null && echo "  $PKG/$VER entfernt"
rm -rf "/usr/src/$PKG-$VER"

echo
echo "Fertig. Fuer die volle BIOS-Regelung des HDD-Kanals: Kaltstart."
