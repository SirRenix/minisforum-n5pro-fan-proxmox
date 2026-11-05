package runner

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
)

// FangovConfig is the n5-fangov configuration whose [[channel]] stop values
// are the restore values of TESTSPEC §4.
const FangovConfig = "/etc/n5-fangov/config.toml"

// Restore value sources, recorded per channel in meta.json.
const (
	SourceArg     = "--restore"
	SourceConfig  = "n5-fangov config"
	SourceBuiltin = "n5-fangov built-in stop value (config unreadable)"
	SourceDefault = "default without n5-fangov config (TESTSPEC §4)"
	SourcePCIe    = "fixed 255 for pwm4 (no EC automatic; operator decision 3, 2026-10-16)"
)

// PCIeRestore is the restore value of pwm4 (DCR5): the EC has no automatic
// for it, so "auto" would leave the last manual duty standing. Fixed 255
// whatever the n5-fangov configuration says (operator decision 3,
// 2026-10-16).
const PCIeRestore = "255"

// RestoreValues are the restore value per channel ("auto" or a duty
// >= 100) and where each came from.
type RestoreValues struct {
	Values     map[int]string
	Sources    map[int]string
	Warnings   []string
	Deviations []string
}

// DeviationPCIeRestore is the spec_deviations entry when the n5-fangov
// configuration gives pwm4 a fixed stop duty other than 255 (TESTSPEC 0.4
// §4 takes the stop value; "auto" is the hand-back, which writes 255 from
// driver 0.2.1+sr1~rc2, so it is no deviation).
func DeviationPCIeRestore(stop string) string {
	return fmt.Sprintf("restore value of pwm4 is 255, not the n5-fangov stop value %q (PCIe/DCR5 has no EC automatic; TESTSPEC §4 takes the stop value; operator decision 3, 2026-10-16)", stop)
}

// ParseRestoreArg reads "pwmN=auto|D" (N 1..4, D 100..255); pwm4 takes
// only 255 (PCIeRestore).
func ParseRestoreArg(s string) (int, string, error) {
	k, v, ok := strings.Cut(strings.TrimSpace(s), "=")
	if !ok {
		return 0, "", fmt.Errorf("--restore %q: want pwmN=auto|D", s)
	}
	ch, err := strconv.Atoi(strings.TrimPrefix(k, "pwm"))
	if err != nil || ch < 1 || ch > 4 || !strings.HasPrefix(k, "pwm") {
		return 0, "", fmt.Errorf("--restore %q: channel is pwm1..pwm4", s)
	}
	if ch == spec.PCIeChannel && v != PCIeRestore {
		return 0, "", fmt.Errorf("--restore %q: the restore value of pwm4 is always %s (no EC automatic)", s, PCIeRestore)
	}
	if v == "auto" {
		return ch, v, nil
	}
	d, err := strconv.Atoi(v)
	if err != nil || d > 255 {
		return 0, "", fmt.Errorf("--restore %q: value is auto or a duty 100..255", s)
	}
	if d < spec.MinDuty {
		return 0, "", fmt.Errorf("--restore %q: a duty below %d is never written (TESTSPEC §4)", s, spec.MinDuty)
	}
	return ch, strconv.Itoa(d), nil
}

type fangovChannel struct {
	Name string `toml:"name"`
	PWM  int    `toml:"pwm"`
	Stop string `toml:"stop"`
}

// n5-fangov's built-in stop values (config.N5ProChannels; pwm3 "auto" is
// forced to 140 by its n5pro sanitizer; pwm4 is not managed: "auto").
var fangovBuiltin = map[int]string{1: "auto", 2: "auto", 3: "140", 4: "auto"}

// ResolveRestore returns the restore value of each channel in chans: the
// --restore argument; else the stop value of the n5-fangov config (read
// as n5-fangov does: pwm3 "auto" becomes 140, a missing channel gets the
// built-in value) when configPath exists; else the default of TESTSPEC 0.4
// §4 (pwm1-3 auto, pwm4 255). pwm4 is always 255 (PCIeRestore); a
// configured fixed stop duty other than 255 is recorded as a spec
// deviation. A fixed value below 100 is raised to 100
// and recorded in Warnings.
func ResolveRestore(args map[int]string, configPath string, chans []int) RestoreValues {
	rv := RestoreValues{Values: map[int]string{}, Sources: map[int]string{}}
	var cfg map[int]string
	cfgSource := ""
	b, err := os.ReadFile(configPath)
	switch {
	case err == nil:
		var doc struct {
			Channels []fangovChannel `toml:"channel"`
		}
		if _, derr := toml.Decode(string(b), &doc); derr != nil {
			rv.Warnings = append(rv.Warnings, fmt.Sprintf("%s unreadable (%v): n5-fangov built-in stop values used", configPath, derr))
			cfg, cfgSource = copyMap(fangovBuiltin), SourceBuiltin
			break
		}
		cfg, cfgSource = copyMap(fangovBuiltin), SourceConfig
		for _, c := range doc.Channels {
			if c.PWM < 1 || c.PWM > 4 {
				continue
			}
			stop := strings.TrimSpace(c.Stop)
			if stop == "" {
				stop = "auto"
			}
			if c.PWM == 3 && stop == "auto" {
				stop = fangovBuiltin[3]
				rv.Warnings = append(rv.Warnings, "n5-fangov config: pwm3 stop \"auto\" is replaced by 140 in n5-fangov (n5pro); restore value 140")
			}
			cfg[c.PWM] = stop
		}
	case errors.Is(err, fs.ErrNotExist):
	default:
		rv.Warnings = append(rv.Warnings, fmt.Sprintf("%s unreadable (%v): n5-fangov built-in stop values used", configPath, err))
		cfg, cfgSource = copyMap(fangovBuiltin), SourceBuiltin
	}
	sort.Ints(chans)
	for _, ch := range chans {
		v, src := "", ""
		switch {
		case args[ch] != "":
			// TESTSPEC 0.4 §4: --restore overrides; the value is in restore_values
			v, src = args[ch], SourceArg
		case cfg != nil && ch == spec.PCIeChannel:
			v, src = PCIeRestore, SourcePCIe
			if stop := cfg[ch]; stop != PCIeRestore && stop != "auto" {
				rv.Deviations = append(rv.Deviations, DeviationPCIeRestore(stop))
			}
		case cfg != nil:
			v, src = cfg[ch], cfgSource
		default:
			// TESTSPEC 0.4 §4 (#2): without a configuration pwm1-3 auto, pwm4 255
			v, src = "auto", SourceDefault
			if ch == spec.PCIeChannel {
				v = PCIeRestore
			}
		}
		if v != "auto" {
			d, err := strconv.Atoi(v)
			switch {
			case err != nil || d > 255:
				rv.Warnings = append(rv.Warnings, fmt.Sprintf("pwm%d: restore value %q is not auto or a duty: auto used", ch, v))
				v = "auto"
			case d < spec.MinDuty:
				rv.Warnings = append(rv.Warnings, fmt.Sprintf("pwm%d: restore value %d raised to %d (a duty below %d is never written, TESTSPEC §4)", ch, d, spec.MinDuty, spec.MinDuty))
				v = strconv.Itoa(spec.MinDuty)
			default:
				v = strconv.Itoa(d)
			}
		}
		rv.Values[ch], rv.Sources[ch] = v, src
	}
	return rv
}

func copyMap(m map[int]string) map[int]string {
	out := map[int]string{}
	for k, v := range m {
		out[k] = v
	}
	return out
}
