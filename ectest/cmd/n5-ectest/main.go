// Command n5-ectest runs the TESTSPEC tests of the Minisforum N5 Pro EC on
// any N5-family box: identify, snapshot, diff, map, handback, log, report,
// restore. Read-only by default; map/handback write through hwmon sysfs only,
// interactively, with the operator at the device. See README.md.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
)

// Exit codes (design §3). diff uses 0/1/2 of TESTSPEC §5.3 and 64.
const (
	exitOK         = 0
	exitFail       = 1 // refused or aborted
	exitIncomplete = 3 // incomplete snapshot
	exitUsage      = 64
	// exitHelp is internal: -h was given, the caller exits 0
	exitHelp = -1
)

// Set at build time: -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "0.1.0-dev"
	commit  = ""
)

const toolName = "n5-ectest"

type command struct {
	run   func(args []string) int
	usage string
	help  string // one line for the command list
}

var commands = map[string]command{}

func register(name string, c command) { commands[name] = c }

var stdout io.Writer = os.Stdout
var stderr io.Writer = os.Stderr

// readmeURL is printed with the command list.
const readmeURL = "https://github.com/SirRenix/minisforum-n5pro-fan-proxmox/blob/main/ectest/README.md"

// commitID is the commit of the build: the ldflags value, else the VCS
// stamp of the Go toolchain with "-dirty" when the tree had changes.
func commitID() string {
	if commit != "" {
		return commit
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		rev, dirty := "", false
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				rev = s.Value
			case "vcs.modified":
				dirty = s.Value == "true"
			}
		}
		if len(rev) >= 12 {
			if dirty {
				return rev[:12] + "-dirty"
			}
			return rev[:12]
		}
	}
	return "unknown"
}

// binarySHA256 is the sha256 of the running executable ("" when it cannot
// be read): the provenance names the exact binary, also for a build without
// ldflags.
var binarySHA256 = sync.OnceValue(func() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	f, err := os.Open(exe)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
})

// parseFlags parses the flags of a command (valued: flags with a value,
// moved before the positional arguments; nil = no reordering). -h prints the
// flags and is exit 0; a flag error is exit 64.
func parseFlags(fs *flag.FlagSet, args []string, valued map[string]bool) (int, bool) {
	if valued != nil {
		args = splitFlags(args, valued)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK, false
		}
		return exitUsage, false
	}
	return exitOK, true
}

func goVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		return bi.GoVersion
	}
	return "unknown"
}

func usage() {
	fmt.Fprintf(stderr, "usage: %s <command> [args]   (%s <command> -h: the flags of a command)\n\ncommands:\n", toolName, toolName)
	var names []string
	for n := range commands {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(stderr, "  %-9s %s\n", n, commands[n].help)
		fmt.Fprintf(stderr, "            %s %s\n", toolName, commands[n].usage)
	}
	fmt.Fprintf(stderr, "\nRead-only except map and handback. Documentation: %s\n", readmeURL)
}

// run dispatches one command line (without the program name).
func run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitUsage
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage()
		return exitOK
	}
	c, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "%s: unknown command %q\n", toolName, args[0])
		usage()
		return exitUsage
	}
	return c.run(args[1:])
}

func main() {
	if os.Getenv(envWorker) == "1" {
		os.Exit(workerMain(os.Args[1:]))
	}
	os.Exit(run(os.Args[1:]))
}

func init() {
	register("version", command{usage: "version", help: "print version, commit and TESTSPEC version", run: func(args []string) int {
		fs := flag.NewFlagSet("version", flag.ContinueOnError)
		fs.SetOutput(stderr)
		if rc, ok := parseFlags(fs, args, nil); !ok {
			return rc
		}
		if fs.NArg() != 0 {
			fmt.Fprintln(stderr, "usage: n5-ectest version")
			return exitUsage
		}
		fmt.Fprintf(stdout, "%s %s (commit %s, %s), TESTSPEC %s\n", toolName, version, commitID(), goVersion(), specVersion())
		return exitOK
	}})
}

// splitFlags moves flags behind positional arguments to the front, so
// "map pwm3 --boot cold" parses like "map --boot cold pwm3". valued lists
// the flags that take a value.
func splitFlags(args []string, valued map[string]bool) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if strings.HasPrefix(a, "-") && len(a) > 1 {
			flags = append(flags, a)
			name := strings.TrimLeft(a, "-")
			if !strings.Contains(name, "=") && valued[name] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}
