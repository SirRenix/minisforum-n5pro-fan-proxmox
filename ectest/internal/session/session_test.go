package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeStat(t *testing.T, proc string, pid int, comm string, ppid, tty int) {
	t.Helper()
	d := filepath.Join(proc, fmt.Sprint(pid))
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf("%d (%s) S %d %d %d %d -1 4194560 0 0", pid, comm, ppid, pid, pid, tty)
	if err := os.WriteFile(filepath.Join(d, "stat"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetect(t *testing.T) {
	proc := t.TempDir()
	tty1 := 4<<8 | 1
	pts := 136 << 8
	// login on tty1 -> bash -> sudo -> n5-ectest (sudo use_pty: pts)
	writeStat(t, proc, 1, "systemd", 0, 0)
	writeStat(t, proc, 100, "login", 1, tty1)
	writeStat(t, proc, 200, "bash", 100, tty1)
	writeStat(t, proc, 300, "sudo", 200, pts)
	writeStat(t, proc, 400, "n5-ectest", 300, pts)
	none := func(string) string { return "" }
	s := Detect(proc, 400, none, "pts")
	if !s.Console || len(s.Remote) != 0 {
		t.Fatalf("console via sudo: %+v", s)
	}
	// over ssh: sshd ancestor, SSH_ vars cleared by sudo
	writeStat(t, proc, 100, "sshd-session", 1, 0)
	writeStat(t, proc, 200, "bash", 100, pts)
	s = Detect(proc, 400, none, "pts")
	if s.Console || len(s.Remote) != 1 {
		t.Fatalf("ssh: %+v", s)
	}
	s = Detect(proc, 400, func(k string) string {
		if k == "SSH_CONNECTION" {
			return "x"
		}
		return ""
	}, "pts")
	if len(s.Remote) != 2 || s.Remote[0] != "SSH_CONNECTION set" {
		t.Fatalf("env: %+v", s)
	}
	// inside tmux: the chain ends at the server; the clients decide
	writeStat(t, proc, 500, "tmux: server", 1, 0)
	writeStat(t, proc, 510, "bash", 500, pts)
	writeStat(t, proc, 520, "n5-ectest", 510, pts)
	writeStat(t, proc, 600, "login", 1, tty1)
	writeStat(t, proc, 610, "bash", 600, tty1)
	writeStat(t, proc, 620, "tmux: client", 610, tty1)
	writeStat(t, proc, 700, "sshd-session", 1, 0)
	writeStat(t, proc, 710, "bash", 700, pts)
	writeStat(t, proc, 720, "tmux: client", 710, pts)
	in := Detect(proc, 520, none, "pts")
	if in.Console {
		t.Fatalf("tmux server chain has a console: %+v", in)
	}
	if s := WithTmux(in, proc, []int{620}); !s.Console || len(s.Remote) != 0 || s.Tmux == "" {
		t.Fatalf("tmux, client on tty1: %+v", s)
	}
	if s := WithTmux(in, proc, []int{620, 720}); s.Console || len(s.Remote) != 1 {
		t.Fatalf("tmux, a second client over ssh: %+v", s)
	}
	if s := WithTmux(in, proc, nil); s.Console {
		t.Fatalf("tmux without clients: %+v", s)
	}
	for nr, want := range map[int]bool{0: false, tty1: true, 4<<8 | 64: true, 5<<8 | 1: true, 229 << 8: true, pts: false, 4 << 8: false} {
		if LocalConsoleTTY(nr) != want {
			t.Errorf("tty_nr %#x: %v", nr, !want)
		}
	}
}
