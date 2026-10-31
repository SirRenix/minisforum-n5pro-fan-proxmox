package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/SirRenix/minisforum-n5pro-fan-proxmox/ectest/internal/spec"
)

// ZpoolStatus runs `zpool status` (10 s timeout); exec.ErrNotFound when
// there is no zpool. Tests replace it.
var ZpoolStatus = func() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "zpool", "status").Output()
	return string(out), err
}

// G2 checks for scrub, resilver and backup (TESTSPEC §3 G2).
func G2(procRoot string) spec.Gate {
	g := spec.Gate{ID: "G2", Condition: "no scrub/resilver/backup"}
	var found []string
	zs, zerr := ZpoolStatus()
	switch {
	case zerr != nil && errors.Is(zerr, exec.ErrNotFound):
		g.Detail = "no zpool; "
	case zerr != nil:
		found = append(found, "zpool status failed: "+zerr.Error())
	default:
		for _, k := range []string{"scrub in progress", "resilver in progress"} {
			if strings.Contains(zs, k) {
				found = append(found, k)
			}
		}
	}
	if ents, err := os.ReadDir(procRoot); err == nil {
		for _, e := range ents {
			if _, err := strconv.Atoi(e.Name()); err != nil {
				continue
			}
			comm := strings.TrimSpace(readRaw(procRoot, e.Name(), "comm"))
			if what := BackupProcess(comm, readRaw(procRoot, e.Name(), "cmdline")); what != "" {
				found = append(found, what+" (pid "+e.Name()+")")
			}
		}
	}
	if len(found) > 0 {
		g.Result, g.Detail = "FAIL", g.Detail+strings.Join(found, ", ")
	} else {
		g.Result, g.Detail = "PASS", g.Detail+"no scrub, resilver, vzdump or proxmox-backup-client"
	}
	return g
}

var backupNames = []string{"vzdump", "proxmox-backup-client"}

// BackupProcess names a backup process from /proc/<pid>/comm and the raw
// /proc/<pid>/cmdline, "" when it is none. comm is cut to 15 characters and a
// PVE worker renames itself to "task UPID:<node>:...:<type>:<id>:<user>:", so
// the command line is read as well.
func BackupProcess(comm, cmdline string) string {
	for _, n := range backupNames {
		if comm == n {
			return "process " + n
		}
	}
	args := strings.Split(strings.TrimRight(cmdline, "\x00"), "\x00")
	for i, a := range args {
		if strings.HasPrefix(a, "task UPID:") {
			if f := strings.Split(strings.TrimPrefix(a, "task "), ":"); len(f) > 5 {
				for _, n := range backupNames {
					if f[5] == n {
						return "PVE task " + n
					}
				}
			}
			continue
		}
		if i > 2 {
			continue
		}
		base := filepath.Base(a)
		for _, n := range backupNames {
			if base == n {
				return "process " + n
			}
		}
	}
	return ""
}

func readRaw(root string, rel ...string) string {
	b, err := os.ReadFile(filepath.Join(append([]string{root}, rel...)...))
	if err != nil {
		return ""
	}
	return string(b)
}
