package identity

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"sort"
	"strings"
)

// HostPlaceholder replaces the host name in every stored text.
const HostPlaceholder = "n5host"

var (
	macRe    = regexp.MustCompile(`\b[0-9A-Fa-f]{2}(?:[:-][0-9A-Fa-f]{2}){5}\b`)
	ipv4Re   = regexp.MustCompile(`\b(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])(?:\.(?:25[0-5]|2[0-4][0-9]|1[0-9][0-9]|[1-9]?[0-9])){3}\b`)
	ipv6Re   = regexp.MustCompile(`[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}(?:%[0-9A-Za-z_.-]+)?`)
	serialRe = regexp.MustCompile(`(?i)\b(serial(?:[ _-]?(?:number|no\.?))?|s/n|sn)(\s*[:=]\s*)([A-Za-z0-9._-]{4,})`)
	homeRe   = regexp.MustCompile(`/home/[^/\s"']+`)
	mailRe   = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.([A-Za-z]{2,})\b`)
	// systemd instance units (getty@tty1.service) look like mail addresses
	unitSuffix = map[string]bool{"service": true, "timer": true, "socket": true, "mount": true, "target": true,
		"path": true, "slice": true, "scope": true, "device": true, "swap": true, "automount": true}
)

// Redactor removes what identifies a person or a network from text:
// host names (full and short) -> n5host, MAC addresses, IPv4 and IPv6
// addresses, "serial: X" style values, /home/<user> paths and mail
// addresses (systemd instance units such as getty@tty1.service stay).
type Redactor struct {
	hosts     []string
	protected []string
}

// Protect keeps values that may contain the host name as a substring
// unchanged (a Proxmox host named "pve" runs kernel "7.0.14-20-pve"): kernel
// release and version, DMI and driver strings. Empty values are ignored.
func (r *Redactor) Protect(vals ...string) {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			r.protected = append(r.protected, v)
		}
	}
	sort.Slice(r.protected, func(i, j int) bool { return len(r.protected[i]) > len(r.protected[j]) })
}

// ProtectIdentity protects the identity strings of id.
func (r *Redactor) ProtectIdentity(id Identity) {
	d := id.DMI
	r.Protect(id.Kernel, id.KernelVersion, id.OS.ID, id.DKMSPackage, id.Driver.Version,
		d.Product, d.Board, d.BoardVersion, d.SysVendor, d.BIOS, d.BIOSDate, d.BIOSRelease, d.EC)
}

// NewRedactor redacts the given host names plus the host's own (os.Hostname,
// <proc>/sys/kernel/hostname). Names shorter than 3 characters are ignored.
func NewRedactor(procRoot string, extra ...string) *Redactor {
	cand := append([]string{}, extra...)
	if h, err := os.Hostname(); err == nil {
		cand = append(cand, h)
	}
	if procRoot != "" {
		cand = append(cand, read(procRoot+"/sys/kernel/hostname"))
	}
	seen := map[string]bool{}
	r := &Redactor{}
	for _, h := range cand {
		for _, n := range []string{h, strings.SplitN(h, ".", 2)[0]} {
			n = strings.TrimSpace(n)
			if len(n) < 3 || seen[n] || n == HostPlaceholder {
				continue
			}
			seen[n] = true
			r.hosts = append(r.hosts, n)
		}
	}
	// longest first: "host.example.lan" before "host"
	sort.Slice(r.hosts, func(i, j int) bool { return len(r.hosts[i]) > len(r.hosts[j]) })
	return r
}

// String returns s redacted.
func (r *Redactor) String(s string) string {
	if r != nil && len(r.hosts) > 0 {
		// protected values are masked while the host names are replaced
		var masks []string
		for i, p := range r.protected {
			hit := false
			for _, h := range r.hosts {
				hit = hit || strings.Contains(p, h)
			}
			if !hit || !strings.Contains(s, p) {
				continue
			}
			m := fmt.Sprintf("\x00%d\x00", i)
			s = strings.ReplaceAll(s, p, m)
			masks = append(masks, m, p)
		}
		for _, h := range r.hosts {
			s = strings.ReplaceAll(s, h, HostPlaceholder)
		}
		for i := 0; i < len(masks); i += 2 {
			s = strings.ReplaceAll(s, masks[i], masks[i+1])
		}
	}
	s = macRe.ReplaceAllString(s, "[mac]")
	s = ipv6Re.ReplaceAllStringFunc(s, func(m string) string {
		host := m
		if i := strings.IndexByte(host, '%'); i >= 0 {
			host = host[:i]
		}
		// a time (12:34:56) or a hex dump row is not an address: only a
		// string that parses as IPv6 and has "::" or all eight groups
		if net.ParseIP(host) == nil {
			return m
		}
		if !strings.Contains(host, "::") && strings.Count(host, ":") != 7 {
			return m
		}
		return "[ipv6]"
	})
	s = ipv4Re.ReplaceAllString(s, "[ipv4]")
	s = serialRe.ReplaceAllString(s, "${1}${2}[serial]")
	s = homeRe.ReplaceAllString(s, "/home/[user]")
	s = mailRe.ReplaceAllStringFunc(s, func(m string) string {
		if unitSuffix[strings.ToLower(mailRe.FindStringSubmatch(m)[1])] {
			return m
		}
		return "[mail]"
	})
	return s
}

// Bytes is String for a byte slice.
func (r *Redactor) Bytes(b []byte) []byte { return []byte(r.String(string(b))) }
