// Package session decides where a writing command is typed from (TESTSPEC
// G5: the operator sits at the device). Environment variables alone do not
// show that (sudo and su - clear SSH_*), so the process chain is read as
// well. Only process names (comm) and the terminal type are recorded, never
// user names or addresses (design §8).
package session

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Session describes the controlling situation of this process.
type Session struct {
	// Remote lists why the session counts as remote: SSH_* variables set, an
	// sshd/termproxy/... process among the ancestors.
	Remote []string `json:"remote"`
	// Console: some process of the chain has a local VT, serial or system
	// console as its controlling terminal.
	Console bool `json:"local_console"`
	// Stdin is the terminal type standard input is connected to ("tty",
	// "pts", "console", "hvc"); "" when it is not a terminal.
	Stdin string `json:"stdin"`
	// Tmux: the process runs inside tmux; its chain ends at the tmux server
	// and the attached clients were judged instead ("" = not in tmux).
	Tmux string `json:"tmux,omitempty"`
}

// WithTmux judges a session inside tmux by its attached clients (the pids
// of `tmux list-clients`): the process chain of the tmux server has no
// terminal. Every remote client makes the session remote; the session has a
// local console only when every client has one. Without clients the session
// stays as detected.
func WithTmux(s Session, procRoot string, clients []int) Session {
	if len(clients) == 0 {
		s.Tmux = "no attached client found"
		return s
	}
	none := func(string) string { return "" }
	all := true
	for _, pid := range clients {
		c := Detect(procRoot, pid, none, "")
		for _, r := range c.Remote {
			s.Remote = append(s.Remote, "tmux client pid "+strconv.Itoa(pid)+": "+r)
		}
		all = all && c.Console && len(c.Remote) == 0
	}
	s.Console = s.Console || all
	s.Tmux = strconv.Itoa(len(clients)) + " attached client(s)"
	return s
}

// remoteParents are the process names (comm, at most 15 characters) of
// servers that hand out remote shells.
var remoteParents = map[string]bool{
	"sshd": true, "sshd-session": true, "dropbear": true, "termproxy": true,
	"mosh-server": true, "telnetd": true, "in.telnetd": true, "ttyd": true,
	"shellinaboxd": true, "tmate": true,
}

// Detect walks pid and its ancestors through procRoot/<pid>/stat.
func Detect(procRoot string, pid int, getenv func(string) string, stdin string) Session {
	s := Session{Stdin: stdin}
	for _, k := range []string{"SSH_CONNECTION", "SSH_CLIENT", "SSH_TTY"} {
		if getenv(k) != "" {
			s.Remote = append(s.Remote, k+" set")
			break
		}
	}
	seen := map[int]bool{}
	for hops := 0; pid > 0 && hops < 128 && !seen[pid]; hops++ {
		seen[pid] = true
		comm, ppid, tty, ok := procStat(procRoot, pid)
		if !ok {
			break
		}
		if remoteParents[comm] {
			s.Remote = append(s.Remote, "ancestor process "+comm+" (pid "+strconv.Itoa(pid)+")")
		}
		if LocalConsoleTTY(tty) {
			s.Console = true
		}
		if pid == 1 {
			break
		}
		pid = ppid
	}
	return s
}

// procStat parses procRoot/<pid>/stat: comm, ppid and tty_nr.
func procStat(procRoot string, pid int) (comm string, ppid, tty int, ok bool) {
	raw, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", 0, 0, false
	}
	b := string(raw)
	open, end := strings.IndexByte(b, '('), strings.LastIndexByte(b, ')')
	if open < 0 || end < open {
		return "", 0, 0, false
	}
	comm = b[open+1 : end]
	f := strings.Fields(b[end+1:])
	if len(f) < 5 {
		return "", 0, 0, false
	}
	ppid, err1 := strconv.Atoi(f[1])
	tty, err2 := strconv.Atoi(f[4])
	if err1 != nil || err2 != nil {
		return "", 0, 0, false
	}
	return comm, ppid, tty, true
}

// LocalConsoleTTY reports whether tty_nr is a VT (major 4, minor 1..63), a
// serial port (major 4, minor 64..255), the system console (major 5, minor
// 1) or a hypervisor console (hvc, major 229). Pseudo terminals are not.
func LocalConsoleTTY(ttyNr int) bool {
	if ttyNr == 0 {
		return false
	}
	major := (ttyNr >> 8) & 0xfff
	minor := (ttyNr & 0xff) | ((ttyNr >> 12) & 0xfff00)
	switch major {
	case 4:
		return minor >= 1
	case 5:
		return minor == 1
	case 229:
		return true
	}
	return false
}

// StdinTerminal returns the terminal type of standard input ("tty", "pts",
// "console", "hvc"), or "" when it is not a terminal.
func StdinTerminal() string {
	st, err := os.Stdin.Stat()
	if err != nil || st.Mode()&os.ModeCharDevice == 0 {
		return ""
	}
	link, err := os.Readlink("/proc/self/fd/0")
	if err != nil {
		return ""
	}
	switch {
	case strings.HasPrefix(link, "/dev/pts/"):
		return "pts"
	case strings.HasPrefix(link, "/dev/console"):
		return "console"
	case strings.HasPrefix(link, "/dev/hvc"):
		return "hvc"
	case strings.HasPrefix(link, "/dev/tty"):
		return "tty"
	}
	return ""
}
