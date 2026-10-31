package spec

import (
	"strconv"
	"strings"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5/identity"
)

// T0Values builds the T0 fields from the identity, the tool version and
// the boot type stated by the operator.
func T0Values(id identity.Identity, toolVersion, boot string) map[string]string {
	f := map[string]string{
		"product_name": id.DMI.Product, "board_name": id.DMI.Board, "board_version": id.DMI.BoardVersion,
		"bios_version": id.DMI.BIOS, "bios_date": id.DMI.BIOSDate, "ec_firmware_release": id.DMI.EC,
		"kernel": id.Kernel, "driver_version": id.Driver.Version, "driver_srcversion": id.Driver.SrcVersion,
		"tool_version": toolVersion, "spec_version": SpecVersion, "boot_type": boot, "uptime": "", "driver_parameters": "",
	}
	if id.UptimeS > 0 {
		f["uptime"] = strconv.FormatFloat(id.UptimeS, 'f', 0, 64)
	}
	if len(id.Driver.Params) > 0 {
		f["driver_parameters"] = "present"
	}
	return f
}

// T0Fields is the field order of T0 (TESTSPEC §2 identity).
var T0Fields = []string{"product_name", "board_name", "board_version", "bios_version", "bios_date", "ec_firmware_release", "kernel", "driver_version", "driver_srcversion", "driver_parameters", "tool_version", "spec_version", "boot_type", "uptime"}

// T0 is the verdict over the identity fields: PASS when every field is
// present, else INCONCLUSIVE naming the missing ones.
func T0(fields map[string]string, boot string) Result {
	var missing []string
	for _, k := range T0Fields {
		if fields[k] == "" {
			missing = append(missing, k)
		}
	}
	res := Result{ID: "T0", Values: fields}
	if len(missing) > 0 {
		res.Verdict = Inconclusive
		res.Reasons = []string{"missing: " + strings.Join(missing, ", ")}
		if boot == "" {
			res.Reasons = append(res.Reasons, "give --boot cold|warm")
		}
	} else {
		res.Verdict = Pass
	}
	return res
}
