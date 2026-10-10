#!/bin/bash
# fakek.sh - a tiny fake "kernel" for the f8a container tests.
#
# Sourced by the stubs (modprobe, research tools) and by run-tests.sh. All
# state lives below F8A_ROOT. Nothing here touches the real /sys or /proc.
#
# Injection (environment of the test):
#   F8A_TEST_KLOG_AT=N   on the N-th driver bring-up append an Oops with a
#                        stack frame of the module to the fake kernel log
#   F8A_TEST_KLOG_FOREIGN_AT=N  same, but a hung-task report of another
#                        subsystem (no driver reference)
#   F8A_TEST_HOT_AT=N    on the N-th bring-up set k10temp Tctl to 90 C
#   F8A_TEST_HOT_ON_REMOVE=N  on the N-th teardown set Tctl to 90 C, then
#                        block for 3 s (a slow modprobe -r)
#   F8A_TEST_STALL_AT=N  on the N-th bring-up fan2_input reads 0 rpm
#   F8A_TEST_FANGOV_AT=N on the N-th bring-up n5-fangov becomes active
#   F8A_TEST_CURVE_CHANGE=1  ec_probe dump shows EC[0x34]=0xa7 once pwm4 was
#                        167 at an unload (simulated curve-table change)
#   F8A_TEST_TOOLFAIL=TOOL  that research tool stub fails (exit 1)
#   F8A_TEST_SLOWTOOL=S  research tool stubs take S seconds
#   FAKE_TEARDOWN=S      length of the fake ENODEV teardown window (s)
#
# The fake kernel log ($R/kmsg) holds "EPOCH<TAB>line"; the journalctl stub
# filters by --since @EPOCH like the real one.

R=${F8A_ROOT:?F8A_ROOT not set}
FK_MOD=minisforum_n5_it5571
FK_HW=$R/sys/class/hwmon/hwmon9
FK_DRV=$R/sys/bus/platform/drivers/$FK_MOD
FK_DBG=$R/sys/kernel/debug/$FK_MOD

fk_log() { printf '%s\n' "$*" >> "$R/calls.log"; }

fk_klog() {
    # fk_klog [-AGO SECONDS] LINE... - append kernel log lines (now, or
    # SECONDS in the past)
    local t=$EPOCHSECONDS l
    if [ "${1:-}" = -AGO ]; then t=$((t - $2)); shift 2; fi
    for l in "$@"; do printf '%s\t%s\n' "$t" "$l" >> "$R/kmsg"; done
}

fk_modprobe_c() {
    # effective options lines like "modprobe -c": config files sorted by
    # basename over /etc, /run, /usr/lib; /etc overrides /run overrides
    # /usr/lib for the same basename
    local f b
    local -A pick=()
    for f in "$R"/usr/lib/modprobe.d/*.conf "$R"/run/modprobe.d/*.conf "$R"/etc/modprobe.d/*.conf; do
        [ -e "$f" ] || continue
        pick[${f##*/}]=$f
    done
    for b in $(printf '%s\n' "${!pick[@]}" | sort); do
        grep -E '^options ' "${pick[$b]}" || true
    done
}

fk_cfg_opt() {
    # fk_cfg_opt NAME - last value of NAME= in the effective options
    local l w v=
    while read -r l; do
        for w in $l; do case "$w" in "$1="*) v=${w#*=} ;; esac; done
    done < <(fk_modprobe_c | grep -E "^options $FK_MOD ")
    printf '%s' "$v"
}

fk_loaded() { grep -q "^$FK_MOD " "$R/proc/modules" 2>/dev/null; }

fk_state() { if fk_loaded; then printf 'LOADED'; else printf 'UNLOADED'; fi; }

fk_count() {
    # fk_count NAME - increment and print a counter
    local n=0
    [ -r "$R/fake/$1" ] && n=$(cat "$R/fake/$1")
    n=$((n + 1))
    printf '%s\n' "$n" > "$R/fake/$1"
    printf '%s' "$n"
}

fk_probe() {
    # bind: hwmon device, debugfs file, device link
    local i n
    mkdir -p "$FK_HW"
    echo "$FK_MOD" > "$FK_HW/name"
    for i in 1 2 3 4; do
        echo 2 > "$FK_HW/pwm${i}_enable"
        echo 255 > "$FK_HW/pwm$i"
    done
    echo 2003 > "$FK_HW/fan1_input"; echo "CPU Fan" > "$FK_HW/fan1_label"
    echo 2236 > "$FK_HW/fan2_input"; echo "SSD Fan" > "$FK_HW/fan2_label"
    echo 1656 > "$FK_HW/fan3_input"; echo "HDD Fan Group" > "$FK_HW/fan3_label"
    echo 0 > "$FK_HW/fan4_input"; echo "PCIe Fan (no tach)" > "$FK_HW/fan4_label"
    echo 35000 > "$FK_HW/temp1_input"; echo "CPU Temp" > "$FK_HW/temp1_label"
    echo 32000 > "$FK_HW/temp2_input"; echo "System Temp" > "$FK_HW/temp2_label"
    echo 25000 > "$FK_HW/temp3_input"; echo "Board Temp" > "$FK_HW/temp3_label"
    echo 22000 > "$FK_HW/temp4_input"; echo "Ambient Temp" > "$FK_HW/temp4_label"
    if [ "$(cat "$R/sys/module/$FK_MOD/parameters/debug_regs" 2>/dev/null)" = Y ]; then
        mkdir -p "$FK_DBG"
        echo "1800: 00 ff 00 55 4a 57 57 8c 00 00 00 06 00 00 00 00" > "$FK_DBG/dcr"
    fi
    ln -sfn "../../../../devices/platform/$FK_MOD" "$FK_DRV/$FK_MOD"
    n=$(fk_count up)
    if [ "${F8A_TEST_KLOG_AT:-0}" = "$n" ]; then
        fk_klog "BUG: kernel NULL pointer dereference, address: 0000000000000008 (injected)" \
            "Oops: 0000 [#1] PREEMPT SMP NOPTI" \
            "Call Trace:" \
            " n5_pwm_enable_store+0x52/0x90 [minisforum_n5_it5571]"
    fi
    if [ "${F8A_TEST_KLOG_FOREIGN_AT:-0}" = "$n" ]; then
        fk_klog "INFO: task txg_sync:1234 blocked for more than 122 seconds. (injected)" \
            "Call Trace:" \
            " zio_wait+0x12/0x30 [zfs]"
    fi
    if [ "${F8A_TEST_HOT_AT:-0}" = "$n" ]; then
        echo 90000 > "$R/sys/class/hwmon/hwmon0/temp1_input"
    fi
    if [ "${F8A_TEST_STALL_AT:-0}" = "$n" ]; then
        echo 0 > "$FK_HW/fan2_input"
    fi
    if [ "${F8A_TEST_FANGOV_AT:-0}" = "$n" ]; then
        echo active > "$R/fake/fangov"
    fi
}

fk_remove() {
    # unbind: an ENODEV window on every attribute, then everything goes
    local f files=()
    if [ "${F8A_TEST_HOT_ON_REMOVE:-0}" = "$(fk_count down)" ]; then
        echo 90000 > "$R/sys/class/hwmon/hwmon0/temp1_input"
        sleep 3
    fi
    if [ -d "$FK_HW" ]; then
        [ -r "$FK_HW/pwm4" ] && cat "$FK_HW/pwm4" > "$R/fake/last_pwm4"
        for f in "$FK_HW"/pwm[1-4] "$FK_HW"/pwm[1-4]_enable "$FK_HW"/fan*_input "$FK_HW"/temp*_input; do
            [ -e "$f" ] && files+=("$f")
        done
        for f in "${files[@]}"; do echo "No such device" > "$f.errno"; done
        sleep "${FAKE_TEARDOWN:-0.4}"
        rm -rf "$FK_HW"
    fi
    rm -rf "$FK_DBG"
    rm -f "$FK_DRV/$FK_MOD"
}

fk_load() {
    # fk_load [param=value...]; experimental_write also from modprobe.d
    local a dbg=N exp=N
    fk_loaded && return 0
    if [ "$(fk_cfg_opt experimental_write)" = 1 ]; then exp=Y; fi
    for a in "$@"; do
        case "$a" in
            debug_regs=1|debug_regs=Y) dbg=Y ;;
            experimental_write=1) exp=Y ;;
            experimental_write=0) exp=N ;;
        esac
    done
    mkdir -p "$R/sys/module/$FK_MOD/parameters"
    echo N > "$R/sys/module/$FK_MOD/parameters/force"
    echo "$exp" > "$R/sys/module/$FK_MOD/parameters/experimental_write"
    echo "$dbg" > "$R/sys/module/$FK_MOD/parameters/debug_regs"
    echo "$FK_MOD 20480 0 - Live 0x0000000000000000" >> "$R/proc/modules"
    printf '  0062-0062 : %s\n  0066-0066 : %s\n  0068-0068 : %s\n  006c-006c : %s\n' \
        "$FK_MOD" "$FK_MOD" "$FK_MOD" "$FK_MOD" >> "$R/proc/ioports"
    mkdir -p "$FK_DRV" "$R/sys/bus/platform/devices/$FK_MOD"
    [ -p "$FK_DRV/bind" ] || mkfifo "$FK_DRV/bind"
    [ -p "$FK_DRV/unbind" ] || mkfifo "$FK_DRV/unbind"
    ln -sfn "../../../../module/$FK_MOD" "$FK_DRV/module"
    if [ "$(fk_count loads)" = 1 ] && [ "${F8A_TEST_SIGNWARN:-0}" = 1 ]; then
        fk_klog "$FK_MOD: module verification failed: signature and/or required key missing - tainting kernel"
    fi
    fk_probe
}

fk_unload() {
    fk_loaded || return 0
    fk_remove
    rm -rf "$FK_DRV" "$R/sys/module/$FK_MOD" "$R/sys/bus/platform/devices/$FK_MOD"
    sed -i "/^$FK_MOD /d" "$R/proc/modules"
    sed -i "/$FK_MOD/d" "$R/proc/ioports"
}

fk_setup() {
    # fresh fake root
    local i
    rm -rf "$R"
    mkdir -p "$R"/sys/class/dmi/id "$R"/proc/sys/kernel/random "$R"/root \
        "$R"/usr/lib/modprobe.d "$R"/etc/modprobe.d "$R"/fake "$R"/sys/kernel/debug \
        "$R"/sys/bus/platform/drivers "$R"/sys/bus/platform/devices "$R"/sys/module
    echo "N5 PRO" > "$R/sys/class/dmi/id/product_name"
    echo "F8NAA" > "$R/sys/class/dmi/id/board_name"
    echo "1.0" > "$R/sys/class/dmi/id/board_version"
    echo "Micro Computer (HK) Tech Limited" > "$R/sys/class/dmi/id/sys_vendor"
    echo "American Megatrends International, LLC." > "$R/sys/class/dmi/id/bios_vendor"
    echo "1.05" > "$R/sys/class/dmi/id/bios_version"
    echo "03/31/2026" > "$R/sys/class/dmi/id/bios_date"
    echo "0.14" > "$R/sys/class/dmi/id/ec_firmware_release"
    echo "12345.67 99999.00" > "$R/proc/uptime"
    echo "00000000-1111-2222-3333-444444444444" > "$R/proc/sys/kernel/random/boot_id"
    printf 'k10temp 16384 0 - Live 0x0\ndrivetemp 16384 0 - Live 0x0\nnvme 65536 7 - Live 0x0\n' > "$R/proc/modules"
    printf '0000-0cf7 : PCI Bus 0000:00\n  0060-0060 : keyboard\n' > "$R/proc/ioports"
    printf 'options %s experimental_write=1\n' "$FK_MOD" > "$R/usr/lib/modprobe.d/$FK_MOD.conf"
    mkdir -p "$R"/sys/class/hwmon/hwmon0 "$R"/sys/class/hwmon/hwmon1 "$R"/sys/class/hwmon/hwmon2 "$R"/sys/class/hwmon/hwmon3
    echo k10temp > "$R/sys/class/hwmon/hwmon0/name"
    echo Tctl > "$R/sys/class/hwmon/hwmon0/temp1_label"
    echo 45000 > "$R/sys/class/hwmon/hwmon0/temp1_input"
    echo nvme > "$R/sys/class/hwmon/hwmon1/name"
    echo 41000 > "$R/sys/class/hwmon/hwmon1/temp1_input"
    echo 38000 > "$R/sys/class/hwmon/hwmon1/temp2_input"
    echo drivetemp > "$R/sys/class/hwmon/hwmon2/name"
    echo 36000 > "$R/sys/class/hwmon/hwmon2/temp1_input"
    echo drivetemp > "$R/sys/class/hwmon/hwmon3/name"
    echo 37000 > "$R/sys/class/hwmon/hwmon3/temp1_input"
    echo inactive > "$R/fake/fangov"
    mkdir -p "$R"/run/modprobe.d
    : > "$R/kmsg"
    : > "$R/calls.log"
    : > "$R/writes.log"
    mkdir -p "$R/stub-bin" "$R/opt/n5bin"
    for i in modprobe journalctl systemctl; do cp "$FK_TEST_DIR/stubs/$i" "$R/stub-bin/$i"; done
    for i in ec_probe n5_fan n5_i2ec_read; do cp "$FK_TEST_DIR/stubs/$i" "$R/opt/n5bin/$i"; done
    chmod +x "$R"/stub-bin/* "$R"/opt/n5bin/*
}
