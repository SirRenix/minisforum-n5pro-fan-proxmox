#!/bin/bash
# run-tests.sh - container tests for the f8a helpers (debian:trixie, root).
#
# Everything runs against a fake sysfs/procfs/debugfs tree below F8A_ROOT
# with stub binaries (test/stubs, test/fakek.sh). Interactive scripts are
# started under script(1) so that stdin is a real pty.
#
#   docker run --rm -v "$PWD:/src:ro" debian:trixie bash /src/test/run-tests.sh
set -uo pipefail

SRC=$(cd "$(dirname "$0")/.." && pwd)
# work on a copy with exec bits (the checkout may come from Windows)
F=/tmp/f8a-under-test
rm -rf "$F"
cp -r "$SRC" "$F"
chmod +x "$F"/*.sh "$F"/test/stubs/*
T=$F/test

export FK_TEST_DIR=$T
export F8A_ROOT=/tmp/f8a-root
export F8A_BIN_DIR=$F8A_ROOT/opt/n5bin
export PATH=$F8A_ROOT/stub-bin:$PATH
export F8A_T_CYCLE=0.3 F8A_T_HOLD=1 F8A_T_SERIES="0 1 2" F8A_T_UP=5 FAKE_TEARDOWN=0.4
export F8A_T_TACH_RETRY=0.1 F8A_T_WATCH=0.3
# shellcheck source=fakek.sh
. "$T/fakek.sh"

RAW=$F8A_ROOT/root/n5-mw1/raw
HOST=$(uname -n)
PASS=0 FAILN=0
OUT='' RC=0

ok() { PASS=$((PASS + 1)); echo "ok   - $*"; }
nok() { FAILN=$((FAILN + 1)); echo "FAIL - $*"; printf '%s\n' "$OUT" | grep -E "F8a step result|F1 run result|UNEXPECTED|abort:|errors by errno|ABORT|REFUSED" | sed "s/^/    | /"; }
check() { local d=$1; shift; if "$@"; then ok "$d"; else nok "$d"; fi; }
has() { grep -q -F -- "$1" <<< "$OUT"; }
hasnot() { ! grep -q -F -- "$1" <<< "$OUT"; }
file_has() { grep -q -F -- "$2" "$1"; }
file_hasnot() { ! grep -q -F -- "$2" "$1"; }
show_on_fail() { if [ "$FAILN" -gt "${1:-0}" ]; then echo "----- output -----"; printf '%s\n' "$OUT" | tail -n 60; echo "------------------"; fi; }

run() {
    # run CMD... - no tty
    OUT=$("$@" < /dev/null 2>&1); RC=$?
}

run_tty() {
    # run_tty INPUT CMD... - with a pty on stdin, INPUT typed
    local cmd
    printf -v cmd '%q ' "$@"
    OUT=$(printf '%s\n' "$1" | script -qefc "${cmd#* }" /dev/null 2>&1); RC=$?
}

BG_PID=''
run_bg() {
    # run_bg INPUT OUTFILE CMD... - like run_tty, in the background; BG_PID
    # is the pid of script(1). Background jobs of a non-interactive shell
    # inherit SIGINT/SIGQUIT ignored; perl resets them like a terminal would.
    local cmd
    printf -v cmd '%q ' "${@:3}"
    printf '%s\n' "$1" | perl -e '$SIG{INT}=$SIG{QUIT}="DEFAULT"; exec @ARGV or die' -- script -qefc "$cmd" /dev/null > "$2" 2>&1 &
    BG_PID=$!
}

mainpid() {
    # mainpid SCRIPT - pid of the bash running $F/SCRIPT whose parent is not
    # itself such a process (the loops and the watcher are forks of it)
    local p c pp all=''
    for p in /proc/[0-9]*; do
        c=$(tr '\0' ' ' 2>/dev/null < "$p/cmdline") || continue
        case "$c" in "/bin/bash $F/$1"*|"bash $F/$1"*) all="$all ${p#/proc/}" ;; esac
    done
    for p in $all; do
        pp=$(awk '/^PPid:/ { print $2 }' "/proc/$p/status" 2>/dev/null) || continue
        case " $all " in *" $pp "*) ;; *) echo "$p"; return 0 ;; esac
    done
    return 1
}

pgid_of() { sed 's/.*) //' "/proc/$1/stat" | awk '{ print $3 }'; }

wait_for() {
    # wait_for FILE TEXT SECONDS
    local i
    for ((i = 0; i < $3 * 10; i++)); do
        grep -q -F -- "$2" "$1" 2>/dev/null && return 0
        sleep 0.1
    done
    return 1
}

wait_gone() {
    # wait_gone SCRIPT SECONDS - until no process runs $F/SCRIPT
    local i p c n
    for ((i = 0; i < $2 * 10; i++)); do
        n=0
        for p in /proc/[0-9]*; do
            c=$(tr '\0' ' ' 2>/dev/null < "$p/cmdline") || continue
            case "$c" in script\ *|perl\ *) ;; *"$F/$1"*) n=$((n + 1)) ;; esac
        done
        [ "$n" -gt 0 ] || return 0
        sleep 0.1
    done
    return 1
}

latest() { # shellcheck disable=SC2012
    ls -t "$RAW"/"$1"-*.txt 2>/dev/null | head -n 1; }

fresh() {
    fk_setup
    mkdir -p "$RAW"
}

sim_start() {
    (
        while :; do
            if [ -p "$FK_DRV/unbind" ]; then
                read -r l < "$FK_DRV/unbind" && { fk_log "unbind $l"; fk_remove; }
            else
                sleep 0.05
            fi
        done
    ) &
    SIM_U=$!
    (
        while :; do
            if [ -p "$FK_DRV/bind" ]; then
                read -r l < "$FK_DRV/bind" && { fk_log "bind $l"; fk_probe; }
            else
                sleep 0.05
            fi
        done
    ) &
    SIM_B=$!
}

sim_stop() { kill "$SIM_U" "$SIM_B" 2>/dev/null; wait "$SIM_U" "$SIM_B" 2>/dev/null; }

hw() { cat "$FK_HW/$1" 2>/dev/null; }

# writes outside the allowed set ("" = none)
writes_other() { grep -v -E "$1" "$F8A_ROOT/writes.log" || true; }

# shellcheck disable=SC1091
echo "# f8a container tests on $(. /etc/os-release; echo "$PRETTY_NAME"), bash $BASH_VERSION"

# ---------------------------------------------------------------- guards
echo "## guards"
fresh
echo "N5" > "$F8A_ROOT/sys/class/dmi/id/product_name"
run "$F/f8a-snapshot.sh" g1 --yes
check "snapshot refuses wrong DMI product" [ "$RC" -ne 0 ]
check "  ... names the DMI mismatch" has "DMI mismatch"
fresh
echo "Other Vendor" > "$F8A_ROOT/sys/class/dmi/id/sys_vendor"
run "$F/f8a-snapshot.sh" g1b --yes
check "snapshot refuses wrong sys_vendor" has "DMI mismatch"

fresh
echo active > "$F8A_ROOT/fake/fangov"
run "$F/f8a-snapshot.sh" g2 --yes
check "snapshot refuses with n5-fangov active" [ "$RC" -ne 0 ]
check "  ... message" has "n5-fangov.service is 'active'"
echo dbus-fail > "$F8A_ROOT/fake/fangov"
: > "$F8A_ROOT/calls.log"
run "$F/f8a-snapshot.sh" g2u --yes
check "snapshot refuses when n5-fangov state is unknown (systemctl unusable)" has "n5-fangov.service is 'unknown'"
check "  ... and no research tool ran" [ ! -s "$F8A_ROOT/calls.log" ]
echo activating > "$F8A_ROOT/fake/fangov"
fk_load debug_regs=1
run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 1
check "stress refuses with n5-fangov activating" has "is 'activating'"
run_tty f8a-pcie.sh "$F/f8a-pcie.sh"
check "pcie refuses with n5-fangov activating" has "is 'activating'"

fresh
run "$F/f8a-snapshot.sh" g3
check "snapshot refuses without tty and without --yes" has "stdin is not a terminal"
fk_load debug_regs=1
run "$F/f8a-stress.sh" load --cycles 1
check "stress refuses without tty" has "stdin is not a terminal"
run "$F/f8a-pcie.sh"
check "pcie refuses without tty" has "stdin is not a terminal"

fresh
fk_load
run "$F/f8a-snapshot.sh" g4 --yes --require-mode A
check "snapshot --require-mode A refuses while module loaded" has "mode A required"
: > "$F8A_ROOT/calls.log"
run bash -c ". '$F/f8a-lib.sh'; f8a_research ec_probe dump"
check "research tool refused while module loaded" has "is loaded (checklist C2"
check "  ... and the stub was not called" [ ! -s "$F8A_ROOT/calls.log" ]
fk_unload
run bash -c ". '$F/f8a-lib.sh'; f8a_research ec_probe write 0x34 0x00"
check "ec_probe write refused (allowlist)" has "not on the read-only allowlist"
run bash -c ". '$F/f8a-lib.sh'; f8a_research ec_probe read 0x34"
check "ec_probe read refused (allowlist)" has "not on the read-only allowlist"
check "  ... no research call reached a stub" [ ! -s "$F8A_ROOT/calls.log" ]
printf '  0062-0062 : ACPI EC (stray)\n' >> "$F8A_ROOT/proc/ioports"
run bash -c ". '$F/f8a-lib.sh'; f8a_research ec_probe dump"
check "research tool refused while a port is reserved by someone else" has "ports are reserved in /proc/ioports"
check "  ... and the stub was not called" [ ! -s "$F8A_ROOT/calls.log" ]
sed -i '/stray/d' "$F8A_ROOT/proc/ioports"
run bash -c ". '$F/f8a-lib.sh'; mkdir -p /tmp/pw; f8a_write_pwm /tmp/pw 1 99"
check "pwm value 99 refused" has "duty below 100 is never written"
check "  ... and not written" [ ! -e /tmp/pw/pwm1 ]
OUT=$(env -u F8A_ROOT F8A_BIN_DIR=/tmp/evil bash -c ". '$F/f8a-lib.sh'; echo \"bin=\$F8A_BIN_DIR\"" 2>&1)
check "F8A_BIN_DIR ignored outside test mode" has "bin=/root/n5pro-ec/bin"

fk_load debug_regs=1
run_tty wrong-name "$F/f8a-stress.sh" load --cycles 1
check "stress cancels on wrong confirmation" has "not confirmed"
check "  ... module untouched (no modprobe call)" [ ! -s "$F8A_ROOT/calls.log" ]

fk_unload; fk_load
run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 1
check "stress refuses without debug_regs=1" has "not loaded with debug_regs=1"

fresh
fk_load debug_regs=1
printf 'options %s experimental_write=0\n' "$FK_MOD" > "$F8A_ROOT/etc/modprobe.d/zz-override.conf"
run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 1
check "stress refuses when a modprobe.d override sets experimental_write=0" has "experimental_write is '0' in the effective modprobe configuration"
run_tty f8a-pcie.sh "$F/f8a-pcie.sh"
check "pcie refuses on the same override" has "experimental_write is '0' in the effective modprobe configuration"

fresh
fk_load
echo 1 > "$FK_HW/pwm3_enable"
run_tty f8a-pcie.sh "$F/f8a-pcie.sh"
check "pcie refuses with pwm3 manual (failsafe 140 would be lost on unload)" has "pwm3_enable is '1', expected 2 on all channels"
check "  ... module untouched" [ ! -s "$F8A_ROOT/calls.log" ]

fresh
fk_load debug_regs=1
echo 85000 > "$F8A_ROOT/sys/class/hwmon/hwmon0/temp1_input"
run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 1
check "stress refuses at Tctl 85 C" has "temperature precondition: Tctl 85.0 >= 85.0"
fresh
fk_load debug_regs=1
rm -rf "$F8A_ROOT/sys/class/hwmon/hwmon2" "$F8A_ROOT/sys/class/hwmon/hwmon3"
run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 1
check "stress refuses without drivetemp source" has "no drivetemp temperature"
fresh
fk_load debug_regs=1
echo 0 > "$FK_HW/fan3_input"
run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 1
check "stress refuses with fan3 at 0 rpm" has "fan3_input=0 rpm"

fresh
OUT=$(PATH=/usr/bin:/bin "$F/f8a-snapshot.sh" g5 --yes 2>&1); RC=$?
check "test mode refuses real modprobe outside F8A_ROOT" has "resolves to"

# ------------------------------------------------------------ kernel log
echo "## kernel-log pattern and attribution"
fresh
fk_klog -AGO 100 "BUG: old line from before the start (must be ignored)"
fk_klog "minisforum_n5_it5571 minisforum_n5_it5571: debug_regs=1: debugfs directory not created (-EEXIST)" \
    "Watchdog detected hard LOCKUP on cpu 3" \
    "kernel BUG at mm/slub.c:123!" \
    "minisforum_n5_it5571: Unknown symbol devm_foo (err -2)" \
    "minisforum_n5_it5571 minisforum_n5_it5571: shutdown: PWM channel 3 not set to 255 (-110)" \
    "WARNING: CPU: 2 PID: 99 at module/zfs/foo.c:1 zfs_bar+0x1/0x2 [zfs]" \
    "minisforum_n5_it5571: module verification failed: signature and/or required key missing - tainting kernel"
OUT=$(bash -c ". '$F/f8a-lib.sh'; f8a_klog_count \$((EPOCHSECONDS - 10)) n l d; echo \"total=\$n driver=\$d\"; f8a_klog_verdict \"\$n\" \"\$d\"" 2>&1)
check "klog: 6 hits, 3 attributed to the driver" has "total=6 driver=3"
check "klog: old line ignored (--since)" hasnot "old line"
check "klog: verdict FAIL with driver hits, foreign hits named too" has "FAIL (3 kernel-log hit(s) attributed to the driver; 3 unexplained foreign hit(s) - annotate with evidence or rerun)"
fresh
fk_klog "WARNING: CPU: 2 PID: 99 at module/zfs/foo.c:1 zfs_bar+0x1/0x2 [zfs]"
OUT=$(bash -c ". '$F/f8a-lib.sh'; f8a_klog_count \$((EPOCHSECONDS - 10)) n l d; f8a_klog_verdict \"\$n\" \"\$d\"" 2>&1)
check "klog: foreign-only hit is FAIL until annotated (TESTSPEC 0.3 5a)" has "FAIL (unexplained foreign hit - annotate with evidence or rerun: 1 kernel-log hit(s) without driver reference)"
check "klog: a hit is never INCONCLUSIVE" hasnot "INCONCLUSIVE"
OUT=$(bash -c ". '$F/f8a-lib.sh'; printf '[%s]' \"\$(f8a_klog_verdict 0 0)\"; f8a_klog_verdict -1 -1" 2>&1)
check "klog: no hit gives no verdict part" has "[]"
check "klog: unreadable journal stays INCONCLUSIVE" has "INCONCLUSIVE (kernel log unreadable)"

# -------------------------------------------------------------- snapshot
echo "## snapshot"
fresh
F8A_TEST_SIGNWARN=0 run "$F/f8a-snapshot.sh" coldA --yes
f=$(latest f8a-snapshot-coldA)
check "snapshot A exit 0" [ "$RC" -eq 0 ]
check "snapshot A raw file exists" [ -n "$f" ]
check "snapshot A sha256 verifies" bash -c "cd '$RAW' && sha256sum -c '${f##*/}.sha256' >/dev/null"
check "snapshot A has two EC RAM dumps" [ "$(grep -c '^=== BEGIN EC_RAM' "$f")" = 2 ]
check "snapshot A has I2EC block" file_has "$f" "1800: 00 ff 00 55"
check "snapshot A has TACH" file_has "$f" "TACH3: 1656 RPM"
check "snapshot A dumps compared: curve regions identical" bash -c "[ \"\$(sed -n '/dump 1 vs dump 2/,/EC temperatures/p' '$f' | grep -c ': identical')\" = 3 ]"
check "snapshot A reports no non-temperature change" bash -c "sed -n '/dump 1 vs dump 2/,/curve-table/p' '$f' | grep -q '(none)'"
check "snapshot A RESULT complete" file_has "$f" "RESULT: complete"
check "snapshot A identity: ec_firmware_release" file_has "$f" "dmi.ec_firmware_release: 0.14"
check "snapshot A identity: effective modprobe options" file_has "$f" "options minisforum_n5_it5571 experimental_write=1"
check "snapshot A temps independent" file_has "$f" "Tctl=45.0 NVMe_max=41.0 HDD_max=37.0"
check "snapshot A host name redacted" file_hasnot "$f" "$HOST"
check "snapshot A n5host present" file_has "$f" "host: n5host"
check "snapshot A research calls only while unloaded" bash -c "! grep -q 'state=LOADED' '$F8A_ROOT/calls.log'"
check "snapshot A exactly 4 research calls" [ "$(wc -l < "$F8A_ROOT/calls.log")" = 4 ]
check "snapshot A wrote nothing to sysfs" [ ! -s "$F8A_ROOT/writes.log" ]

fresh
F8A_TEST_TOOLFAIL=n5_i2ec_read run "$F/f8a-snapshot.sh" failA --yes
f=$(latest f8a-snapshot-failA)
check "snapshot A with a failing research tool: exit 3" [ "$RC" -eq 3 ]
check "  ... RESULT INCOMPLETE names the tool" file_has "$f" "RESULT: INCOMPLETE (n5_i2ec_read failed"
check "  ... sha256 still written" [ -e "$f.sha256" ]

fresh
fk_load debug_regs=1
run "$F/f8a-snapshot.sh" warmB --yes
f=$(latest f8a-snapshot-warmB)
check "snapshot B exit 0" [ "$RC" -eq 0 ]
check "snapshot B no research tool called" [ ! -s "$F8A_ROOT/calls.log" ]
check "snapshot B dcr content" file_has "$f" "1800: 00 ff 00 55 4a 57 57 8c"
check "snapshot B DCR5 decoded" file_has "$f" "DCR5(PCIe)=8c"
check "snapshot B pwm labelled as cache" file_has "$f" "pwm1=255 pwm1_enable=2 (driver cache, not EC state)"
check "snapshot B ioports listed" file_has "$f" "0068-0068 : minisforum_n5_it5571"
check "snapshot B module params" file_has "$f" "debug_regs=Y"
check "snapshot B lsmod line" file_has "$f" "minisforum_n5_it5571 20480 0"
check "snapshot B lsmod nvme line" file_has "$f" "nvme 65536 7"
check "snapshot B wrote nothing to sysfs" [ ! -s "$F8A_ROOT/writes.log" ]
fk_unload; fk_load
run "$F/f8a-snapshot.sh" warmB2 --yes
f=$(latest f8a-snapshot-warmB2)
check "snapshot B without debug_regs: dcr unavailable" file_has "$f" "dcr unavailable"

# ------------------------------------------------------------------ diff
echo "## diff"
fresh
run "$F/f8a-snapshot.sh" d1 --yes
a=$(latest f8a-snapshot-d1)
run "$F/f8a-snapshot.sh" d2 --yes
b=$(latest f8a-snapshot-d2)
run "$F/f8a-snapshot.sh" --diff "$a" "$b"
check "diff identical: exit 0" [ "$RC" -eq 0 ]
check "diff identical: verdict" has "C1 curve tables 0x10-0x27, 0x30-0x38, 0x70-0x81: identical"
check "diff ignores temperature byte 0x09" hasnot "0x09:"
c=/tmp/f8a-diff-c.txt
sed -e 's/^30: 16 3C 00 1A 64/30: 16 3C 00 1A A7/' -e 's/^00: 00 00 00 0E 20/00: 00 00 00 0E 2F/' -e 's/^E0: 00 00 00 00 0D/E0: 00 00 00 00 0E/' "$b" > "$c"
run "$F/f8a-snapshot.sh" --diff "$a" "$c"
check "diff changed: exit 1" [ "$RC" -eq 1 ]
check "diff reports 0x34" has "0x34: 64 -> a7"
check "diff reports non-curve byte 0xe4" has "0xe4: 0d -> 0e"
check "diff hides temperature byte 0x04" hasnot "0x04:"
check "diff names the region" has "0x30-0x38: CHANGED at 0x34"
check "diff other regions identical" has "0x70-0x81: identical"
check "diff verdict CHANGED" has "C1 curve tables 0x10-0x27, 0x30-0x38, 0x70-0x81: CHANGED"
fk_load debug_regs=1
run "$F/f8a-snapshot.sh" dB --yes
run "$F/f8a-snapshot.sh" --diff "$a" "$(latest f8a-snapshot-dB)"
check "diff with a mode-B file: exit 2" [ "$RC" -eq 2 ]

# ---------------------------------------------------------------- stress
echo "## stress load"
fresh
fk_load debug_regs=1
: > "$F8A_ROOT/calls.log"
fk_klog -AGO 100 "BUG: from an earlier step (before the start)"
run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 3
f=$(latest f8a-stress-load)
check "stress load exit 0" [ "$RC" -eq 0 ]
check "stress load PASS" has "F8a step result (load): PASS (3 cycles"
check "stress load: kernel-log line from before the start not counted" has "kernel-log hits: 0 (attributed to the driver: 0)"
check "stress load 3 cycles" has "cycles done: 3/3"
check "stress load injected ENODEV counted" grep -Eq "writer ok: [0-9]+  errors by errno: .*ENODEV=[0-9]+" "$f"
check "stress load no unexpected errno" file_hasnot "$f" "UNEXPECTED"
check "stress load 3x modprobe -r" [ "$(grep -c '^modprobe -r ' "$F8A_ROOT/calls.log")" = 3 ]
check "stress load reload with debug_regs=1" [ "$(grep -c '^modprobe minisforum_n5_it5571 debug_regs=1 ' "$F8A_ROOT/calls.log")" = 3 ]
check "stress load final pwm1_enable=2" [ "$(hw pwm1_enable)" = 2 ]
check "stress load final pwm2_enable=2" [ "$(hw pwm2_enable)" = 2 ]
check "stress load: only pwm1/pwm2 written" [ -z "$(writes_other '^/sys/class/hwmon/hwmon9/pwm[12](_enable)? ')" ]
check "stress load: pwm1/pwm2 were written" grep -q '^/sys/class/hwmon/hwmon9/pwm2 100$' "$F8A_ROOT/writes.log"
check "stress load: no duty below 100 attempted" bash -c "! grep -Eq '/pwm[1-4] ([0-9]|[1-9][0-9])\$' '$F8A_ROOT/writes.log'"
check "stress load module loaded with debug_regs" [ "$(cat "$F8A_ROOT/sys/module/minisforum_n5_it5571/parameters/debug_regs")" = Y ]
check "stress load enable=2 recorded per cycle" [ "$(grep -c 'pwm1..4_enable=2 2 2 2 fan1=2003 fan2=2236 fan3=1656' "$f")" = 3 ]
check "stress load race note" file_has "$f" "exercised, not proven absent"
check "stress load sha256" bash -c "cd '$RAW' && sha256sum -c '${f##*/}.sha256' >/dev/null"
check "stress load host redacted" file_hasnot "$f" "$HOST"
check "stress load: no research tool called" bash -c "! grep -Eq '^(ec_probe|n5_fan|n5_i2ec_read)' '$F8A_ROOT/calls.log'"
check "stress load: no process left" wait_gone f8a-stress.sh 3
show_on_fail 0

echo "## stress unbind"
fresh
fk_load debug_regs=1
sim_start
run_tty f8a-stress.sh "$F/f8a-stress.sh" unbind --cycles 3
sim_stop
f=$(latest f8a-stress-unbind)
check "stress unbind exit 0" [ "$RC" -eq 0 ]
check "stress unbind PASS" has "F8a step result (unbind): PASS (3 cycles"
check "stress unbind 3 unbind + 3 bind" [ "$(grep -c '^unbind minisforum_n5_it5571' "$F8A_ROOT/calls.log") $(grep -c '^bind minisforum_n5_it5571' "$F8A_ROOT/calls.log")" = "3 3" ]
check "stress unbind no modprobe" bash -c "! grep -q '^modprobe' '$F8A_ROOT/calls.log'"
check "stress unbind ENODEV counted" grep -Eq "errors by errno: .*ENODEV=" "$f"
check "stress unbind final pwm1/2_enable=2" [ "$(hw pwm1_enable) $(hw pwm2_enable)" = "2 2" ]
check "stress unbind: only pwm1/pwm2 and bind/unbind written" [ -z "$(writes_other '^/sys/(class/hwmon/hwmon9/pwm[12](_enable)?|bus/platform/drivers/minisforum_n5_it5571/(un)?bind) ')" ]

echo "## stress abort on kernel-log hit (driver Oops)"
fresh
fk_load debug_regs=1
F8A_TEST_KLOG_AT=3 run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 3
check "klog abort exit != 0" [ "$RC" -ne 0 ]
check "klog abort after cycle 2" has "ABORT: kernel-log hit after cycle 2"
check "klog abort FAIL" grep -Eq '^F8a step result \(load\): FAIL \([0-9]+ kernel-log hit\(s\) attributed to the driver' <<< "$OUT"
check "klog abort shows the line" has "BUG: kernel NULL pointer dereference"
check "klog abort shows the module frame" has "[minisforum_n5_it5571]"
check "klog abort restore pwm1/2_enable=2" [ "$(hw pwm1_enable) $(hw pwm2_enable)" = "2 2" ]
check "klog abort module loaded" fk_loaded

echo "## stress abort on a foreign kernel-log hit"
fresh
fk_load debug_regs=1
F8A_TEST_KLOG_FOREIGN_AT=2 run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 3
check "foreign hit aborts" has "ABORT: kernel-log hit after cycle 1"
check "foreign hit: FAIL until annotated (TESTSPEC 0.3 5a)" has "F8a step result (load): FAIL (unexplained foreign hit - annotate with evidence or rerun: 2 kernel-log hit(s) without driver reference)"
check "foreign hit: exit != 0" [ "$RC" -ne 0 ]
check "foreign hit: never INCONCLUSIVE" hasnot "F8a step result (load): INCONCLUSIVE"
check "foreign hit restore pwm1/2_enable=2" [ "$(hw pwm1_enable) $(hw pwm2_enable)" = "2 2" ]

echo "## stress abort on temperature, restore"
fresh
fk_load debug_regs=1
F8A_TEST_HOT_AT=3 run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 3
check "hot abort exit != 0" [ "$RC" -ne 0 ]
check "hot abort reason" has "Tctl 90.0 >= 85.0"
check "hot abort INCONCLUSIVE" has "INCONCLUSIVE (temperature limit"
check "hot abort max Tctl reported" has "max temps: Tctl=90.0"
check "hot abort restore pwm1/2_enable=2" [ "$(hw pwm1_enable) $(hw pwm2_enable)" = "2 2" ]
check "hot abort module loaded" fk_loaded

echo "## stress: watcher sees a limit while modprobe -r blocks"
fresh
fk_load debug_regs=1
F8A_TEST_HOT_ON_REMOVE=2 run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 3
f=$(latest f8a-stress-load)
check "watcher warns during the blocked teardown" file_has "$f" "!!! LIMIT (watcher): Tctl 90.0 >= 85.0"
check "  ... warning comes before the abort" bash -c "[ \$(grep -n '!!! LIMIT (watcher)' '$f' | head -1 | cut -d: -f1) -lt \$(grep -n 'ABORT: temperature limit' '$f' | head -1 | cut -d: -f1) ]"
check "  ... abort right after the teardown" file_has "$f" "ABORT: temperature limit (after teardown in cycle 2)"
check "  ... restore pwm1/2_enable=2, module loaded" [ "$(hw pwm1_enable) $(hw pwm2_enable) $(fk_loaded && echo y)" = "2 2 y" ]

echo "## stress abort on a stalled fan"
fresh
fk_load debug_regs=1
F8A_TEST_STALL_AT=3 run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 3
check "stall abort exit != 0" [ "$RC" -ne 0 ]
check "stall abort names fan2" has "after reload in cycle 2: tach check: fan2_input=0 rpm"
check "stall abort FAIL" has "F8a step result (load): FAIL ("
check "stall abort restore pwm1/2_enable=2" [ "$(hw pwm1_enable) $(hw pwm2_enable)" = "2 2" ]

echo "## stress abort when n5-fangov starts"
fresh
fk_load debug_regs=1
F8A_TEST_FANGOV_AT=3 run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 3
check "fangov abort" has "ABORT: n5-fangov.service is 'active' (after cycle 2)"
check "fangov abort INCONCLUSIVE" has "F8a step result (load): INCONCLUSIVE (n5-fangov.service is 'active'"

echo "## stress: kernel log unreadable"
fresh
fk_load debug_regs=1
touch "$F8A_ROOT/fake/journal-broken"
run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 2
check "journal unreadable aborts" has "kernel log unreadable"
check "  ... verdict INCONCLUSIVE, not FAIL" has "F8a step result (load): INCONCLUSIVE (kernel log unreadable)"
check "  ... module loaded again" fk_loaded

echo "## stress: Ctrl-C (SIGINT to the process group)"
fresh
fk_load debug_regs=1
OUTF=/tmp/f8a-int.out
F8A_T_CYCLE=1 run_bg f8a-stress.sh "$OUTF" "$F/f8a-stress.sh" load --cycles 40
wait_for "$OUTF" "cycle 1/40" 30
sleep 0.5
MP=$(mainpid f8a-stress.sh)
kill -INT -- "-$(pgid_of "$MP")"
wait_gone f8a-stress.sh 30
wait "$BG_PID" 2>/dev/null
f=$(latest f8a-stress-load)
check "INT: summary written" file_has "$f" "F8a step result (load): INCONCLUSIVE (interrupted (SIGINT)"
check "INT: final pwm1/2_enable=2 logged" file_has "$f" "final pwm2_enable=2"
check "INT: pwm1/2_enable=2" [ "$(hw pwm1_enable) $(hw pwm2_enable)" = "2 2" ]
check "INT: module loaded with debug_regs" [ "$(cat "$F8A_ROOT/sys/module/$FK_MOD/parameters/debug_regs" 2>/dev/null)" = Y ]
check "INT: sha256 verifies" bash -c "cd '$RAW' && sha256sum -c '${f##*/}.sha256' >/dev/null"

echo "## stress: terminal hangup (script(1) killed = lost SSH session)"
fresh
fk_load debug_regs=1
OUTF=/tmp/f8a-hup.out
F8A_T_CYCLE=1 run_bg f8a-stress.sh "$OUTF" "$F/f8a-stress.sh" load --cycles 40
wait_for "$OUTF" "cycle 1/40" 30
sleep 0.5
kill -KILL "$BG_PID"
wait_gone f8a-stress.sh 30
f=$(latest f8a-stress-load)
check "HUP: summary written to the file" file_has "$f" "F8a step result (load): INCONCLUSIVE (hangup (SIGHUP"
check "HUP: pwm1/2_enable=2" [ "$(hw pwm1_enable) $(hw pwm2_enable)" = "2 2" ]
check "HUP: module loaded" fk_loaded
check "HUP: sha256 verifies" bash -c "cd '$RAW' && sha256sum -c '${f##*/}.sha256' >/dev/null"

echo "## stress: SIGTERM to the script"
fresh
fk_load debug_regs=1
OUTF=/tmp/f8a-term.out
F8A_T_CYCLE=1 run_bg f8a-stress.sh "$OUTF" "$F/f8a-stress.sh" unbind --cycles 40
sim_start
wait_for "$OUTF" "cycle 1/40" 30
sleep 0.5
kill -TERM "$(mainpid f8a-stress.sh)"
wait_gone f8a-stress.sh 30
wait "$BG_PID" 2>/dev/null
sim_stop
f=$(latest f8a-stress-unbind)
check "TERM: summary written" file_has "$f" "INCONCLUSIVE (terminated (SIGTERM)"
check "TERM: pwm1/2_enable=2, bound" [ "$(hw pwm1_enable) $(hw pwm2_enable)" = "2 2" ] && [ -L "$FK_DRV/$FK_MOD" ]

echo "## stress: SIGKILL of the main script leaves no writer behind"
fresh
fk_load debug_regs=1
OUTF=/tmp/f8a-kill.out
F8A_T_CYCLE=1 run_bg f8a-stress.sh "$OUTF" "$F/f8a-stress.sh" load --cycles 40
wait_for "$OUTF" "cycle 1/40" 30
sleep 0.5
kill -KILL "$(mainpid f8a-stress.sh)"
check "KILL: loops and watcher end on their own within 3 s" wait_gone f8a-stress.sh 3
kill "$BG_PID" 2>/dev/null; wait "$BG_PID" 2>/dev/null
n1=$(wc -l < "$F8A_ROOT/writes.log"); sleep 1; n2=$(wc -l < "$F8A_ROOT/writes.log")
check "KILL: no further pwm writes" [ "$n1" = "$n2" ]

# ----------------------------------------------------------------- pcie
echo "## pcie (module loaded at start)"
fresh
fk_load
: > "$F8A_ROOT/calls.log"
F8A_TEST_CURVE_CHANGE=1 F8A_TEST_SIGNWARN=1 run_tty f8a-pcie.sh "$F/f8a-pcie.sh"
f=$(latest f8a-pcie-m2)
check "pcie exit 0" [ "$RC" -eq 0 ]
expected="modprobe -r minisforum_n5_it5571 state=LOADED
ec_probe dump state=UNLOADED
ec_probe dump state=UNLOADED
n5_i2ec_read 0x1800 48 state=UNLOADED
n5_fan rpm state=UNLOADED
modprobe minisforum_n5_it5571 debug_regs=1 state=UNLOADED
modprobe -r minisforum_n5_it5571 state=LOADED
ec_probe dump state=UNLOADED
ec_probe dump state=UNLOADED
n5_i2ec_read 0x1800 48 state=UNLOADED
n5_fan rpm state=UNLOADED
modprobe minisforum_n5_it5571 state=UNLOADED"
check "pcie call sequence exact" [ "$(cat "$F8A_ROOT/calls.log")" = "$expected" ]
[ "$(cat "$F8A_ROOT/calls.log")" = "$expected" ] || { echo "--- calls.log:"; cat "$F8A_ROOT/calls.log"; }
check "pcie: no research tool while loaded" bash -c "! grep -Eq '^(ec_probe|n5_fan|n5_i2ec_read).*state=LOADED' '$F8A_ROOT/calls.log'"
check "pcie: ec_probe never with write" bash -c "! grep -q '^ec_probe write' '$F8A_ROOT/calls.log'"
check "pcie: pwm4 was 167 at unload" [ "$(cat "$F8A_ROOT/fake/last_pwm4")" = 167 ]
check "pcie: final module loaded normally" [ "$(cat "$F8A_ROOT/sys/module/minisforum_n5_it5571/parameters/debug_regs")" = N ]
check "pcie: final pwm4_enable=2" [ "$(hw pwm4_enable)" = 2 ]
check "pcie: diff shows curve change" file_has "$f" "0x30-0x38: CHANGED at 0x34"
check "pcie: diff shows DCR5" file_has "$f" "0x1807: ff -> a7"
check "pcie: series printed" grep -q "c hand-back +2s" "$f"
check "pcie: impossibility note" file_has "$f" "research tools beside the loaded driver (forbidden"
check "pcie: M3 not decided note" file_has "$f" "this run decides M2 (curve tables, DCR5 after 0x31), not M3"
check "pcie: signature warning ignored" file_has "$f" "kernel-log hits since start: 0"
check "pcie: run result COMPLETE" file_has "$f" "F1 run result: COMPLETE"
check "pcie: only pwm4 written (exact)" [ "$(cat "$F8A_ROOT/writes.log")" = "/sys/class/hwmon/hwmon9/pwm4_enable 1
/sys/class/hwmon/hwmon9/pwm4 167
/sys/class/hwmon/hwmon9/pwm4_enable 2" ]
check "pcie: tach checked while loaded" grep -q "step b ok: .*fan1=2003 fan2=2236 fan3=1656" "$f"
check "pcie: before/after snapshots exist" [ -n "$(latest f8a-snapshot-pcie-before)" ] && [ -n "$(latest f8a-snapshot-pcie-after)" ]
show_on_fail 0

echo "## pcie (module not loaded at start)"
fresh
run_tty f8a-pcie.sh "$F/f8a-pcie.sh"
check "pcie unloaded-start exit 0" [ "$RC" -eq 0 ]
check "pcie unloaded-start: final module unloaded" bash -c "! grep -q '^minisforum_n5_it5571 ' '$F8A_ROOT/proc/modules'"
check "pcie unloaded-start: identical curve tables without injection" has "C1 curve tables 0x10-0x27, 0x30-0x38, 0x70-0x81: identical"

echo "## pcie abort on temperature during (b)"
fresh
fk_load
F8A_TEST_HOT_AT=2 run_tty f8a-pcie.sh "$F/f8a-pcie.sh"
check "pcie hot abort exit != 0" [ "$RC" -ne 0 ]
check "pcie hot abort reason" has "temperature limit"
check "pcie hot abort: INCONCLUSIVE" has "F1 run result: INCONCLUSIVE (temperature limit"
check "pcie hot abort: pwm4 back to 2" [ "$(hw pwm4_enable)" = 2 ]
check "pcie hot abort: module loaded normally" [ "$(cat "$F8A_ROOT/sys/module/minisforum_n5_it5571/parameters/debug_regs" 2>/dev/null)" = N ]
check "pcie hot abort: no after-snapshot" [ -z "$(latest f8a-snapshot-pcie-after)" ]
check "pcie hot abort: research tools only unloaded" bash -c "! grep -Eq '^(ec_probe|n5_fan|n5_i2ec_read).*state=LOADED' '$F8A_ROOT/calls.log'"

echo "## pcie abort on a foreign kernel-log hit during (b)"
fresh
fk_load
F8A_TEST_KLOG_FOREIGN_AT=2 run_tty f8a-pcie.sh "$F/f8a-pcie.sh"
check "pcie foreign hit exit != 0" [ "$RC" -ne 0 ]
check "pcie foreign hit: FAIL until annotated (TESTSPEC 0.3 5a)" has "F1 run result: FAIL (unexplained foreign hit - annotate with evidence or rerun: 2 kernel-log hit(s) without driver reference); abort: kernel-log hit"
check "pcie foreign hit: never INCONCLUSIVE" hasnot "F1 run result: INCONCLUSIVE"
check "pcie foreign hit: pwm4 back to 2" [ "$(hw pwm4_enable)" = 2 ]

echo "## pcie: research tool fails in the after-snapshot"
fresh
fk_load
OUT=''
# the stub fails only after the debug_regs=1 load of step (b), i.e. in pcie-after
cat > "$F8A_ROOT/opt/n5bin/n5_fan" <<'EOF'
#!/bin/bash
set -eu
. "$FK_TEST_DIR/fakek.sh"
fk_log "n5_fan $* state=$(fk_state)"
if grep -q 'debug_regs=1' "$R/calls.log"; then echo "n5_fan: TACH1 RPM read timed out (injected)" >&2; exit 1; fi
printf 'TACH1: 2003 RPM\nTACH2: 2236 RPM\nTACH3: 1656 RPM\n'
EOF
chmod +x "$F8A_ROOT/opt/n5bin/n5_fan"
run_tty f8a-pcie.sh "$F/f8a-pcie.sh"
check "pcie tool failure: exit 3" [ "$RC" -eq 3 ]
check "pcie tool failure: INCONCLUSIVE with data gap" has "F1 run result: INCONCLUSIVE (snapshot pcie-after incomplete"
check "pcie tool failure: module restored normally" [ "$(cat "$F8A_ROOT/sys/module/minisforum_n5_it5571/parameters/debug_regs" 2>/dev/null)" = N ]

echo "## pcie: Ctrl-C in (b) with pwm4 manual"
fresh
fk_load
OUTF=/tmp/f8a-pint.out
F8A_T_HOLD=20 run_bg f8a-pcie.sh "$OUTF" "$F/f8a-pcie.sh"
wait_for "$OUTF" "pwm4=167 written" 60
sleep 0.5
check "pcie INT: pwm4 manual before the signal" [ "$(hw pwm4_enable)" = 1 ]
MP=$(mainpid f8a-pcie.sh)
kill -INT -- "-$(pgid_of "$MP")"
wait_gone f8a-pcie.sh 30
wait "$BG_PID" 2>/dev/null
f=$(latest f8a-pcie-m2)
check "pcie INT: final line logged" file_has "$f" "final: module loaded, debug_regs=N, pwm4_enable=2"
check "pcie INT: pwm4_enable=2, debug_regs=N" [ "$(hw pwm4_enable) $(cat "$F8A_ROOT/sys/module/$FK_MOD/parameters/debug_regs")" = "2 N" ]
check "pcie INT: result INCONCLUSIVE (interrupted)" file_has "$f" "F1 run result: INCONCLUSIVE (interrupted (SIGINT), step b)"

echo "## pcie: SIGTERM during a research tool waits for it (C2)"
fresh
fk_load
OUTF=/tmp/f8a-pterm.out
F8A_TEST_SLOWTOOL=3 run_bg f8a-pcie.sh "$OUTF" "$F/f8a-pcie.sh"
for _ in $(seq 1 100); do grep -q '^ec_probe' "$F8A_ROOT/calls.log" && break; sleep 0.1; done
MP=$(mainpid f8a-pcie.sh)
kill -TERM -- "-$(pgid_of "$MP")"
wait_gone f8a-pcie.sh 40
wait "$BG_PID" 2>/dev/null
check "pcie TERM: the running research tool finished" grep -q '^ec_probe done state=UNLOADED' "$F8A_ROOT/calls.log"
check "pcie TERM: module reloaded only after the research tool ended" bash -c "awk '/^modprobe minisforum_n5_it5571 state=/ { m = NR } /^(ec_probe|n5_fan|n5_i2ec_read)/ { r = NR } END { exit !(m > r && m > 0) }' '$F8A_ROOT/calls.log'"
check "pcie TERM: no research tool while loaded" bash -c "! grep -Eq '^(ec_probe|n5_fan|n5_i2ec_read).*state=LOADED' '$F8A_ROOT/calls.log'"
check "pcie TERM: module loaded normally at the end" [ "$(cat "$F8A_ROOT/sys/module/$FK_MOD/parameters/debug_regs" 2>/dev/null)" = N ]

echo "## mutual exclusion with n5-ectest (TESTKIT-DESIGN 5)"
fresh
busy() { bash -c ". '$F/f8a-lib.sh'; f8a_research_busy"; }
check "no n5-ectest: not busy" bash -c "! busy_out=\$(. '$F/f8a-lib.sh'; f8a_research_busy)"
# n5-ectest log/snapshot never touch the ports: only the mode-A lock counts
cp /bin/sleep /tmp/n5-ectest
/tmp/n5-ectest 30 &
EP=$!
sleep 0.2
OUT=$(busy || true)
check "running n5-ectest (log) not reported" hasnot "$EP"
kill "$EP" 2>/dev/null; wait "$EP" 2>/dev/null
mkdir -p "$F8A_ROOT/run"
flock -o "$F8A_ROOT/run/n5-ectest.lock" sleep 30 &
LP=$!
sleep 0.3
OUT=$(busy || true)
check "held /run/n5-ectest.lock reported" has "lock"
kill "$LP" 2>/dev/null; wait "$LP" 2>/dev/null
check "free lock: not busy" bash -c "! busy_out=\$(. '$F/f8a-lib.sh'; f8a_research_busy)"

echo "## pwm writer lock (n5-ectest map/handback, n5-fangov >= 0.5)"
fresh
fk_load
mkdir -p "$F8A_ROOT/run/n5-fangov"
echo "n5-ectest map (guardian) pid 4242" > "$F8A_ROOT/run/n5-fangov/writer.lock"
flock -o "$F8A_ROOT/run/n5-fangov/writer.lock" sleep 30 &
LP=$!
sleep 0.3
: > "$F8A_ROOT/calls.log"
run_tty f8a-pcie.sh "$F/f8a-pcie.sh"
check "pcie refused while the writer lock is held" [ "$RC" -ne 0 ]
check "pcie names the holder" has "n5-ectest map (guardian) pid 4242"
check "pcie wrote nothing under a held lock" [ ! -s "$F8A_ROOT/writes.log" ]
check "pcie touched no module under a held lock" [ ! -s "$F8A_ROOT/calls.log" ]
run_tty f8a-stress.sh "$F/f8a-stress.sh" load --cycles 1
check "stress refused while the writer lock is held" [ "$RC" -ne 0 ]
check "stress names the lock" has "another pwm writer holds"
kill "$LP" 2>/dev/null; wait "$LP" 2>/dev/null
OUTF=/tmp/f8a-lock.out
F8A_T_HOLD=20 run_bg f8a-pcie.sh "$OUTF" "$F/f8a-pcie.sh"
wait_for "$OUTF" "pwm4=167 written" 60
check "pcie holds the writer lock while it runs" bash -c "! flock -n '$F8A_ROOT/run/n5-fangov/writer.lock' true"
check "lock file names f8a-pcie.sh" grep -q '^f8a-pcie.sh pid ' "$F8A_ROOT/run/n5-fangov/writer.lock"
MP=$(mainpid f8a-pcie.sh)
kill -INT -- "-$(pgid_of "$MP")"
wait_gone f8a-pcie.sh 30
wait "$BG_PID" 2>/dev/null
check "lock free after the run" flock -n "$F8A_ROOT/run/n5-fangov/writer.lock" true

echo
echo "# result: $PASS passed, $FAILN failed"
[ "$FAILN" -eq 0 ]
