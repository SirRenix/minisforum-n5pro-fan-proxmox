module github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest

go 1.26

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5 v0.0.0
)

// go/n5 lives in this repository; a release pins a tag n5/vX.Y.Z instead.
replace github.com/SirRenix/minisforum-n5pro-fan-proxmox/go/n5 => ../go/n5
